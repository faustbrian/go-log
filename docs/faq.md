# FAQ and troubleshooting

## Does this replace `log/slog`?

No. Applications construct and pass `*slog.Logger`. The root package and all
subpackages compose standard handlers and records.

## Why not define a Logger interface for tests?

The standard logger is already concrete, cheap to derive, and accepts any
`slog.Handler`. Use `handler/capture` in tests. An extra interface tends to hide
standard features and makes libraries depend on application abstractions.

## Why are JSON and text handlers not reimplemented?

The standard handlers define encoding, groups, `ReplaceAttr`, source fields,
and value resolution. Delegating avoids semantic drift and follows Go updates.

## Why is my record emitted twice?

Stack routes overlap. Bounds are inclusive, so an error record matches both an
unbounded info route and an error route. Add `MaxLevel: slog.LevelWarn` to make
the first route disjoint.

## Why did `slog.Logger` not return a handler error?

The standard logger API does not expose `Handler.Handle` errors. Observe async
failures through `OnError` and `Stats`; observe file or Collector health through
operational monitoring. Direct handler calls still return errors.

## Why does shutdown return a deadline error while logs continue draining?

The context bounds the caller's wait, not the shared background drain. A later
`Shutdown` call can wait again. This prevents one short deadline from silently
discarding accepted records.

## Why does `Enabled` return false after shutdown begins?

The async handler has stopped accepting work. Returning false lets standard
loggers avoid constructing attributes that would be rejected.

## Which overflow policy should I choose?

Start with `Block` for correctness and measure latency. Use `SyncFallback` when
loss is unacceptable but occasional caller latency is permitted. Use a drop
policy only when the owning event class explicitly tolerates loss.

## Does `DropOldest` return an error?

No. The current record is accepted; an older queued record is lost. Inspect
`DroppedOldest` or `Lost()` to detect it. `DropNewest` returns `ErrDropped`
because the current call is rejected.

## Are records safe to modify after calling async `Handle`?

Yes. Metadata, attribute slices, nested groups, and resolved `LogValuer` values
are frozen before acceptance. Arbitrary pointer values inside `KindAny` remain
opaque; implement `LogValuer` to provide an immutable structured snapshot.

## Why was my `LogValuer` evaluated early?

Async must freeze values before retaining them, and redaction resolves
non-sensitive values to handle panics and recursive values safely. A value
matched by a redaction rule is never evaluated.

## Can redaction remove secrets from messages?

No. Messages should be fixed event names. Put variable data in attributes. A
rendered string no longer has reliable structure or type information.

## Why did an exact redaction path stop matching after `WithGroup`?

`WithGroup` changes the structural path, as required by `slog.Handler`. Update
the path or use a broad key rule for values that are sensitive at every depth.

## Can a custom key contain a dot?

Yes for standard handlers. Redaction paths and capture assertions interpret
dots as group separators, so prefer group structure or keys without dots when
those helpers must address the value.

## Why is a rotated file larger than `MaxBytes`?

One `Write` is atomic and never split. A single encoded record may exceed the
limit; it is written whole and rotated before the next write.

## Why did rotation fail after moving the directory?

Rotation requires rename within one filesystem and a writable existing
directory. The writer does not create parent directories. Inspect the returned
filesystem error and permissions.

## Should I use rotation in Kubernetes?

Usually no. Write JSON to stdout/stderr and let the platform plus an
OpenTelemetry Collector own ingestion, rotation, retry, and vendor export.

## Does the OTel bridge start tracing?

No. It only reads a standard span context. `telemetry` or application code
must initialize providers and own shutdown.

## How do I investigate a blocked shutdown?

1. Capture a goroutine dump.
2. Identify the downstream handler currently in `Handle`.
3. Verify the sink honors its own I/O timeouts.
4. Inspect async queue and failure metrics.
5. Keep the shutdown context bounded while correcting the sink.

The logging package cannot forcibly cancel an arbitrary handler that ignores
context.
