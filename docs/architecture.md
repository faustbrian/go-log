# Architecture

## Design boundaries

The standard library owns the logger API, record model, and JSON/text encoding.
`log` only provides handlers and writers that compose those types.

```text
application -> *slog.Logger -> decorator handlers -> sink handlers -> io.Writer
```

There is no package-wide global logger, proprietary interface, hidden provider,
or vendor transport. Applications can remove any decorator without changing
their logger parameter types.

## Handler contract

Every decorator implements all four `slog.Handler` methods:

- `Enabled` delegates or aggregates downstream decisions without evaluating
  record attributes.
- `Handle` receives records by value, preserves metadata, and returns or joins
  downstream errors according to its documented policy.
- `WithAttrs` creates an independent derived handler and never mutates its
  parent.
- `WithGroup` preserves standard group order and treats downstream behavior as
  authoritative.

Stack clones a record for every sink. Async freezes metadata, the attribute
slice, nested group slices, and resolved `LogValuer` values before queueing.
Capture clones retained records and returns independent snapshots.

## Ordering and ownership

Decorators observe only values that reach them. Therefore:

- redaction must be outside every sink that requires protection;
- trace correlation should be outside async so it reads the live context;
- sampling should be outside async to avoid queueing records that will drop;
- stack should be inside redaction when all sinks share one secret policy;
- separate redaction handlers may be placed inside stack for sink-specific
  policy.

Async and rotate resources are owned by the application that constructs them.
The application must call `Shutdown` and `Close`, respectively.

## Failure semantics

| Component | Failure behavior |
| --- | --- |
| Root constructor | Returns the first option error or `ErrNilHandler` |
| Stack | Attempts every matching route and joins handler errors |
| Redact | Resolves non-sensitive values safely; matched values are never evaluated |
| Sample | A drop succeeds synchronously and increments `Dropped` |
| Async queued delivery | Reports failures through `OnError` and `Stats` |
| Async overflow | Follows the selected explicit policy |
| Capture | Retains in memory and returns no delivery error |
| Rotate | Returns filesystem errors and attempts reopen after partial rotation |
| OTel | Omits correlation for invalid contexts and delegates sink errors |

No handler retries a failed network transport. Retry and durable buffering
belong in the Collector or another dedicated agent.

## Async sequencing

Each accepted queued record receives a monotonically increasing sequence under
the submission lock. The worker completes records in queue order. Drop-oldest
marks an evicted sequence complete. A bounded out-of-order set advances a
completion watermark when gaps close.

Flush snapshots the latest accepted sequence under the same submission lock and
waits for the watermark. Synchronous fallback completes before releasing the
submission lock, so it needs no retained sequence. The completion set is
bounded by queue capacity.

Shutdown switches acceptance off atomically, waits for all accepted sequences,
closes the queue, waits for the worker, and closes one shared completion signal.
Caller contexts bound waiting, not background delivery.

## Stateful concurrency matrix

| Component | Mutable state | Synchronization | Race coverage |
| --- | --- | --- | --- |
| Stack | None after construction | Immutable route copies | Concurrent downstream contract tests |
| Redact | None after construction | Immutable rule/options copies | Handler race suite |
| Sample every-N | Seen counter | `atomic.Uint64` | Parallel exact-count test |
| Sample stats | Kept/dropped | `atomic.Uint64` | Parallel Handle/Stats test |
| Async queue | Channel and submission sequence | Channel plus submission mutex | Parallel Handle/Flush/Stats/Shutdown tests |
| Async completion | Watermark and bounded gap set | Completion mutex and progress channel | Overflow and deadline tests |
| Capture | Retained records | RW mutex | Parallel readers and writers |
| Rotate | File, size, rotation count | One mutex | Concurrent whole-write test |

All stateful packages run under `go test -race ./...` in CI.

## Hostile values

The redaction and async walkers resolve public `slog.Value` kinds and recursively
copy group slices. `slog.Value.Resolve` converts panicking or recursively
returning `LogValuer` implementations into error values with bounded resolution.
Matched secret values are replaced before resolution.

Fuzz targets cover invalid UTF-8, zero values, nested and empty groups,
duplicates, recursive values, panicking values, arbitrary rules, and typed
values. Individual writes remain bounded by input size; async retention remains
bounded by configured queue capacity.

## OpenTelemetry boundary

The optional bridge imports only the stable OpenTelemetry trace API. It reads
`trace.SpanContextFromContext` and adds IDs to the record. It does not import an
SDK package, create providers, register globals, sample spans, export data, or
shut down telemetry. The separate `telemetry` runtime owns those concerns and
returns standard contexts and APIs.

This direction prevents dependency cycles: `telemetry` does not need
`log`, and `log/otel` does not need the `telemetry` runtime module.

## Performance policy

Benchmarks report latency and allocations for standard JSON, redaction, stack,
sampling, and async pipelines. `TestAllocationBudgets` enforces allocation
ceilings for synchronous decorators. `TestLatencyBudgets` enforces deliberately
conservative latency ceilings from 5 to 20 microseconds per operation. These
ceilings catch order-of-magnitude regressions without treating shared-runner
noise as a release failure. The wall-clock gate is excluded under race-detector
instrumentation. Benchmark output remains the source for finer trend analysis
and is retained in release notes when performance changes materially.
