# Migration guide

## From `log/slog`

Keep existing `*slog.Logger` parameters and call sites. Replace only startup
construction:

```go
// Before.
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

// After.
json := slog.NewJSONHandler(os.Stdout, nil)
safe, err := redact.New(json, redactionOptions)
if err != nil {
	return err
}
logger := slog.New(safe)
```

Do not wrap the logger in an application-wide interface merely for this
migration. For tests, inject `slog.New(capture.New())`.

## From the legacy `log` package

Introduce `*slog.Logger` at dependency boundaries and migrate event fields from
formatted strings to attributes:

```go
// Before.
log.Printf("processed order %s in %s", orderID, elapsed)

// After.
logger.InfoContext(ctx, "order processed",
	slog.String("order.id", orderID),
	slog.Duration("duration", elapsed),
)
```

Keep messages stable and place variable data in attributes. This enables
structural redaction, filtering, grouping, and backend queries.

During an incremental migration, direct standard-library log output to slog:

```go
slog.SetDefault(logger)
standardLog.SetOutput(slog.NewLogLogger(logger.Handler(), slog.LevelInfo).Writer())
```

Review source attribution and level mapping before enabling this globally.

## From Zap

Replace `*zap.Logger` parameters with `*slog.Logger` one boundary at a time.
Map fields to `slog.Attr`:

| Zap | slog |
| --- | --- |
| `zap.String(k, v)` | `slog.String(k, v)` |
| `zap.Int(k, v)` | `slog.Int(k, v)` |
| `zap.Duration(k, v)` | `slog.Duration(k, v)` |
| `zap.Error(err)` | `slog.Any("error", err)` |
| `logger.Named(name)` | `logger.WithGroup(name)` |

Replace core teeing with `handler/stack`, sampling cores with
`handler/sample`, and lumberjack or file cores with `handler/rotate` plus the
standard JSON handler.

Zap's `Sync` behavior is not identical to async shutdown. Keep the constructed
`async.Handler` and call its context-bounded `Shutdown` explicitly.

## From Logrus

Map `WithFields` to `With` and hooks that duplicate records to `handler/stack`.
Replace formatter configuration with `slog.NewJSONHandler` or
`slog.NewTextHandler` options.

```go
// Before.
logger.WithFields(logrus.Fields{"order_id": id}).Info("processed")

// After.
logger.Info("processed", slog.String("order_id", id))
```

Audit custom hooks carefully. Network shipping should move to an OpenTelemetry
Collector instead of being recreated as an in-process handler.

## Attribute naming

Choose a naming convention before migration. This project does not rewrite
keys. Dot-containing keys are ordinary keys to standard handlers, while capture
assertion and redaction paths use dots to describe nested groups. Prefer real
groups when structural nesting matters:

```go
logger.WithGroup("http").Info("request",
	slog.String("method", request.Method),
	slog.Int("status", status),
)
```

## Rollout sequence

1. Introduce standard `*slog.Logger` boundaries.
2. Preserve existing output format with the standard JSON or text handler.
3. Add structural redaction and verify representative secret fixtures.
4. Add routing and compare record counts per sink.
5. Add sampling only to explicitly loss-tolerant events.
6. Add async delivery behind metrics for loss, failure, and fallback.
7. Exercise shutdown and blocked-sink behavior before production rollout.
8. Move vendor transport and retry into the Collector.

Run both pipelines temporarily only when duplicate volume and secret policy are
understood. Avoid logging a secret to an old sink while validating redaction on
the new one.
