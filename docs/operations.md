# Operations guide

Logging is part of a service's failure surface. Capacity, loss, shutdown, and
secret policies must be chosen before deployment and observed continuously.

## Backpressure policy

| Policy | Caller effect | Loss | Appropriate use |
| --- | --- | --- | --- |
| `Block` | Waits for space; context cancellation is ignored | None after acceptance unless sink fails | Audit, billing, low-volume critical logs |
| `DropNewest` | Immediate `ErrDropped` | Current record | Burst-tolerant diagnostics where older context matters |
| `DropOldest` | Current call succeeds | Oldest queued record | Fresh state is more valuable than stale diagnostics |
| `SyncFallback` | Sink latency moves to caller | Only sink failure | Loss-intolerant streams that may tolerate latency spikes |

Queue capacity is a memory and burst-duration budget, not a throughput fix.
Measure sustained sink throughput before choosing it. Each queued record owns a
frozen record copy and resolved attribute tree until delivery.

## Loss accounting

Read `async.Handler.Stats` on a regular interval and export deltas through the
service's metrics system. The counters are monotonic for the handler lifetime.

- `Enqueued`: accepted by the worker queue.
- `Delivered`: successful queued and synchronous deliveries.
- `Failed`: downstream handler errors.
- `DroppedNewest` and `DroppedOldest`: policy losses.
- `SynchronousFallback`: calls moved onto producer goroutines.
- `Rejected`: records not accepted because shutdown began.
- `Lost()`: failed plus both drop counters; rejected calls are excluded.

Alert on any unexpected `Lost()` delta. Alert separately on sustained fallback
because it predicts request latency even when no logs are lost.

## Delivery errors

Standard `slog.Logger` does not return handler errors to callers. Configure
`OnError` for asynchronous failures and keep it independent from the same log
pipeline. Suitable actions are:

- incrementing a pre-created metric instrument;
- writing a bounded diagnostic to stderr;
- setting an in-memory health flag.

Do not perform network retries, blocking I/O, or recursive logging in the
callback. Transport retry belongs in the OpenTelemetry Collector.

Synchronous stack and fallback calls return joined or downstream errors when
handlers are invoked directly. Applications using `slog.Logger` should still
observe sink health out of band.

## Flush and shutdown

`Flush(ctx)` waits for records accepted before its snapshot. A timeout stops
the caller's wait but does not cancel the worker or discard records.

`Shutdown(ctx)` performs three actions:

1. atomically stops new acceptance;
2. starts one irreversible background drain;
3. lets every caller wait on that same drain with its own context.

If the first caller times out, later calls can continue waiting. A downstream
handler that ignores context and blocks forever can prevent the background
drain from finishing, but no `Shutdown` call waits beyond its own context.

Stop request servers, consumers, and periodic jobs before shutdown so they do
not receive `async.ErrClosed`. Reserve part of the platform termination grace
period for logging after other producers stop.

## Process crashes

Async delivery is in memory and does not survive abrupt process termination,
`SIGKILL`, kernel failure, or power loss. `Flush` and `Shutdown` improve orderly
termination only. Use a durable local agent or Collector when crash durability
is a requirement.

## Secrets and privacy

Maintain a reviewed key policy that includes authentication headers, cookies,
passwords, tokens, credentials, connection strings, and vendor-specific secret
names. Prefer broad key rules for secret categories and exact path rules when a
key is only sensitive in a particular structure.

Redaction covers attributes, including nested groups, duplicate keys, typed
values, errors, URLs, headers, structs, and `LogValuer` implementations. It does
not alter:

- record messages;
- source file or function fields emitted by `slog`;
- values rendered before they enter the handler;
- data sent to a sink positioned before the redaction handler.

Treat messages as fixed event names. Normalize carriage returns and newlines in
untrusted strings before assigning them to text-handler attributes if downstream
line-oriented tools do not safely escape them. JSON handlers provide a stronger
log-forging boundary.

## Sampling

Sampling is deliberate loss. Never sample audit, security, billing, or state
transition records unless the owning policy explicitly permits it. Export
`sample.Handler.Stats` to quantify kept and dropped records.

Every-N sampling is process-local and restarts its sequence after restart.
Deterministic sampling is stable for the same key and rate across processes,
subject to this module's compatibility policy.

## Local rotation

`rotate.Writer` serializes concurrent writes and enforces file permissions.
Rotation syncs and closes the active file, removes the oldest backup, shifts
numbered backups, renames the active file, and opens a new active file.

Operational implications:

- The directory must already exist and be writable.
- Rename must remain on one filesystem.
- A disk-full or short write is returned by the standard handler.
- Partial rename failure is reported and the writer attempts to reopen the
  current path.
- `Backups: 0` truncates instead of retaining old data.
- One record larger than `MaxBytes` is kept whole and may exceed the limit.
- `Close` syncs the active file and joins sync and close failures.

Monitor filesystem usage independently. Rotation bounds numbered files, not
space consumed by external copies or open deleted files.

## Kubernetes and collectors

Prefer stdout/stderr with JSON. Application-side async buffering may smooth
short encoder or pipe stalls, but it must not duplicate the Collector's durable
retry role. Keep vendor endpoints, TLS credentials, tenant tokens, routing, and
retry configuration in the Collector.

## Capacity rollout

Before enabling async in production:

1. benchmark the exact composed pipeline;
2. load test at expected peak log rate;
3. inject a blocked sink and verify the chosen policy;
4. verify shutdown inside the platform grace period;
5. export and alert on loss/fallback counters;
6. run with the race detector in integration tests;
7. document the queue memory budget and owner.
