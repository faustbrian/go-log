# log

`log` is a production-oriented toolkit built on Go's standard `log/slog`
types. Applications keep accepting and passing `*slog.Logger`; this module adds
small handlers for composition, redaction, sampling, bounded delivery, test
capture, local rotation, and OpenTelemetry correlation.

The package does not define a proprietary logger interface, replace the
standard JSON or text encoders, initialize OpenTelemetry, or ship direct vendor
drivers.

## Requirements

- Go 1.24 or newer.
- OpenTelemetry API v1.41 when importing the optional `otel` bridge.

## Install

```sh
go get github.com/faustbrian/golib/pkg/log
```

## Quick start

```go
package main

import (
	"log/slog"
	"os"

	log "github.com/faustbrian/golib/pkg/log"
)

func main() {
	logger, err := log.New(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
		log.WithAttrs(slog.String("service", "orders")),
	)
	if err != nil {
		panic(err)
	}

	logger.Info("service ready", slog.String("component", "http"))
}
```

All application APIs remain standard:

```go
func RunWorker(logger *slog.Logger) error {
	logger.Info("worker started")
	return nil
}
```

## Packages

| Package | Purpose |
| --- | --- |
| root | Standard logger constructors and ordered handler options |
| `handler/stack` | Synchronous fan-out and inclusive per-sink level routes |
| `handler/redact` | Structural key and path redaction before value evaluation |
| `handler/sample` | Concurrent every-N and stable key-based sampling |
| `handler/async` | Bounded delivery with explicit overflow and shutdown |
| `handler/capture` | Concurrent record capture and test assertions |
| `handler/rotate` | Permission-enforced rotating `io.WriteCloser` |
| `otel` | Optional trace/span correlation from standard context |

## Recommended production topology

For Kubernetes, write standard JSON to stdout or stderr and let the platform
forward it to an OpenTelemetry Collector. Configure routing, buffering,
retries, and Better Stack, Datadog, or another backend in the Collector. This
keeps credentials and vendor transports out of application processes.

Use `handler/rotate` only where a platform log stream is unavailable, such as a
single-host or desktop deployment.

## Composition order

Handler order changes guarantees. A typical service pipeline is:

```text
slog.Logger
  -> trace correlation
  -> structural redaction
  -> sampling
  -> bounded async delivery
  -> stack routing
  -> standard JSON/text handlers
```

Put redaction before every sink that can observe values. Put correlation before
async delivery so span IDs are captured while the request context is current.
Put sampling before async delivery to avoid consuming queue capacity for
dropped records.

## Delivery guarantees

`handler/async` uses a fixed-capacity queue and one worker. Its policies are:

- `Block`: wait for space without treating context cancellation as record
  cancellation, as required by `slog.Handler`.
- `DropNewest`: reject the current record with `async.ErrDropped`.
- `DropOldest`: evict the oldest queued record and accept the current record.
- `SyncFallback`: deliver the current record on the caller goroutine.

`Flush` waits for all records accepted before its call. `Shutdown` stops new
acceptance, drains in the background, is repeatable, and honors each caller's
deadline. `Stats` exposes enqueued, delivered, failed, dropped, fallback, and
rejected counts. Applications must call `Shutdown` during graceful shutdown.

## Security defaults

- Redaction is structural; it never searches rendered strings.
- Matching keys are case-insensitive and paths are exact structural paths.
- Matched `LogValuer` values are replaced without being evaluated.
- Rotated files default to mode `0600` and enforce the configured mode.
- Messages are not redacted. Never place secrets or untrusted multiline data
  in log messages; use attributes and configure redaction rules.

See [adoption](docs/adoption.md), [recipes](docs/recipes.md),
[operations](docs/operations.md), and [architecture](docs/architecture.md) for
complete guidance.

## Stability and support

The compatibility promise is documented in
[docs/compatibility.md](docs/compatibility.md). Security issues should follow
[SECURITY.md](SECURITY.md). Contributions follow
[CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT. See [LICENSE](LICENSE).
