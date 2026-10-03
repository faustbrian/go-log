# Operations guide

Logging is part of a service's failure surface. Capacity, loss, shutdown, and
secret policies must be chosen before deployment and observed continuously.

## Backpressure policy

| Policy | Caller effect | Loss | Appropriate use |
| --- | --- | --- | --- |
| `Block` | Waits for admission up to the configured timeout; call-site cancellation is ignored | None after acceptance unless sink fails | Critical logs with explicit rejection handling |
| `DropNewest` (default) | Immediate `ErrDropped` when full | Current record | Burst-tolerant diagnostics where older context matters |
| `DropOldest` | Current call succeeds | Oldest queued record | Fresh state is more valuable than stale diagnostics |
| `SyncFallback` | Bounded admission wait, then sink latency moves to caller | Only sink failure after acceptance | Streams with bounded downstream I/O and explicit rejection handling |

Queue capacity bounds the number of waiting records, not their byte size or
total process memory. It is a burst-duration budget, not a throughput fix.
Measure sustained sink throughput before choosing it. Each queued record owns a
frozen record copy and resolved attribute tree until delivery.

`Block` and `SyncFallback` require a positive `AdmissionTimeout`. One admission
deadline covers submission ownership and capacity or fallback-slot waiting;
expiry returns `ErrAdmissionTimeout` before acceptance. It does not preempt
record-resolution callbacks or an accepted downstream delivery. Bound those
operations in application-owned handlers and transports.

## Loss accounting

Read `async.Handler.Stats` on a regular interval and export deltas through the
service's metrics system. The counters are monotonic for the handler lifetime.

- `Enqueued`: accepted by the worker queue.
- `Delivered`: successful queued and synchronous deliveries.
- `Failed`: downstream handler errors.
- `DroppedNewest` and `DroppedOldest`: policy losses.
- `SynchronousFallback`: calls moved onto producer goroutines.
- `Rejected`: records not accepted because shutdown began or admission timed out.
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

`Flush(ctx)` bounds both obtaining its submission snapshot and waiting for
records accepted before that snapshot. A timeout stops
the caller's wait but does not cancel the worker or discard records.

`Shutdown(ctx)` performs three actions:

1. atomically stops new acceptance;
2. closes the bounded queue and waits for its worker and synchronous fallback
   calls;
3. cancels the shared delivery context if a caller's deadline expires.

If the first caller times out, later calls can continue waiting. A downstream
handler that ignores context and blocks forever can prevent completion, but no
`Shutdown` call waits beyond its own context and the package does not create a
detached goroutine to outlive the caller. `OnError` runs on the worker and must
also return promptly; a blocking callback prevents further delivery and drain.

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

Default root construction and redaction omit all caller attributes, including
their keys and group names, and replace the complete message without resolving
discarded values. Selective rules require `PreserveTrustedAttributes: true`;
that opt-in trusts names and unmatched values, while matched nested/duplicate
fields are replaced before resolution. Neither variant can alter:

- source file or function fields emitted by `slog`;
- values rendered before they enter the handler;
- data sent to a sink positioned before the redaction handler;
- data already bound into a supplied downstream handler, or injected by an
  application-selected source, encoder, option, or callback collaborator.

Opt into `PreserveTrustedMessage` only for fixed application event names; the
opt-in rejects messages above 1,024 bytes. Normalize carriage returns and
newlines in untrusted text-handler attributes if downstream line-oriented tools
do not safely escape them. JSON handlers provide a stronger log-forging
boundary.

## Sampling

Sampling is deliberate loss. Never sample audit, security, billing, or state
transition records unless the owning policy explicitly permits it. Export
`sample.Handler.Stats` to quantify kept and dropped records.

Every-N sampling is process-local and restarts its sequence after restart.
Deterministic sampling is stable for the same key and rate across processes,
subject to this module's compatibility policy. Fractional rates reject returned
keys above `sample.MaxKeyBytes` (1,024 bytes) before hashing. Zero and one rates
do not evaluate the key callback. Keep callback execution and allocation bounded.

## Local rotation

`rotate.Writer` serializes concurrent writes and enforces file permissions.
It rejects symbolic links and non-regular active or numbered backup paths.
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
