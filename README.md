# log

[![CI](https://github.com/faustbrian/go-log/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/faustbrian/go-log/actions/workflows/ci.yml)
[![CodeQL](https://img.shields.io/badge/CodeQL-required-blue)](https://github.com/faustbrian/go-log/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Mutation](https://img.shields.io/badge/mutation-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Documentation](https://img.shields.io/badge/docs-checked_in_CI-blue)](docs/)
[![Go Reference](https://pkg.go.dev/badge/github.com/faustbrian/go-log.svg)](https://pkg.go.dev/github.com/faustbrian/go-log)
[![Release](https://img.shields.io/github/v/release/faustbrian/go-log?sort=semver)](https://github.com/faustbrian/go-log/releases)
[![Go](https://img.shields.io/badge/go-1.27.0-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`log` is a production-oriented toolkit built on Go's standard `log/slog`
types. Applications keep accepting and passing `*slog.Logger`; this module adds
small handlers for composition, redaction, sampling, bounded delivery, test
capture, local rotation, and OpenTelemetry correlation.

The package does not define a proprietary logger interface, replace the
standard JSON or text encoders, initialize OpenTelemetry, or ship direct vendor
drivers.

The latest published stable module is v1.0.0. This source tree prepares the
next v2 module and requires Go 1.27.0 or newer; v2 is not installable until a
v2 tag is published.

Shared construction, ownership, lifecycle, and composition expectations are in
the versioned [Golib ecosystem index](https://github.com/faustbrian/go-library-tools/blob/v1.4.0/docs/ecosystem/README.md)
and [observability family guidance](https://github.com/faustbrian/go-library-tools/blob/v1.4.0/docs/ecosystem/design-language.md#package-families-and-selection).

## Requirements

- Go 1.27.0 or newer.
- OpenTelemetry API v1.41 when importing the optional `otel` bridge.

## Install released v1

```sh
go get github.com/faustbrian/go-log@v1
```

The secure defaults documented below belong to the pending v2 release.

`New`, `JSON`, and `Text` omit caller attributes and group identifiers and
replace free-form messages without evaluating discarded values. Time and level
remain. Use `TrustedNew`, `TrustedJSON`, or `TrustedText` only for explicitly
classified, bounded application data; trusted messages are limited to 1,024
bytes. Custom options, prebound handlers, output callbacks, and I/O remain
application-owned collaborators.

## Next v2 quick start

```go
package main

import (
	"log/slog"
	"os"

	log "github.com/faustbrian/go-log/v2"
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

## Lifecycle and ownership

Callers own the `*slog.Logger`, wrapped handlers, OpenTelemetry providers, and
shutdown ordering. The root, stack, redaction, sampling, capture, and OTel
handlers start no background workers and require no close operation. Capture
retains records until `Reset`; collection returns snapshots without clearing
them. Async owns a bounded worker and must be drained with `Shutdown`; rotate
owns its active file and must be closed. Concurrent use requires wrapped
handlers and every caller-provided collaborator, including rules, samplers,
key functions, and levelers, to be concurrency-safe. The built-in stateless
and atomic policies are safe within their documented resource bounds.

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

- `DropNewest` (default): reject the current record with `async.ErrDropped`
  when the queue is full.
- `Block`: wait for admission up to a positive `AdmissionTimeout`, without
  treating call-site context cancellation as record cancellation.
- `DropOldest`: evict the oldest queued record and accept the current record.
- `SyncFallback`: deliver the current record on the caller goroutine, with at
  most one fallback inside the downstream handler at a time; a positive
  `AdmissionTimeout` bounds waiting for that slot, not the delivery itself.

Admission expiry returns `async.ErrAdmissionTimeout` before acceptance and
increments `Rejected`. Queue capacity counts records, not bytes; downstream
callbacks and I/O need application-owned bounds.

`Flush` waits for all records accepted before its call. `Shutdown` stops new
acceptance, cancels cooperative downstream work when its context expires, is
repeatable, and honors each caller's deadline. `Stats` exposes enqueued,
delivered, failed, dropped, fallback, and rejected counts. Applications must
call `Shutdown` during graceful shutdown.

## Security defaults

- Root constructors and redaction omit all caller attributes and group names
  by default; discarded `LogValuer` values are not evaluated.
- Selective attribute redaction requires `PreserveTrustedAttributes: true`;
  this trusts attribute names, group identifiers, and unmatched payloads.
- Redaction is structural; it never searches rendered strings.
- Matching keys are case-insensitive and paths are exact structural paths.
- Matched `LogValuer` values are replaced without being evaluated.
- Rotated files default to mode `0600`, enforce the configured mode, and reject
  symbolic-link or non-regular active and backup paths.
- Messages are replaced with `[REDACTED]` by default. Set
  `PreserveTrustedMessage` only for bounded, fixed application messages;
  omit private data or classify it before explicit trusted-attribute opt-in.
- Structural processing rejects cumulative bound and record structure above
  1,024 attributes or 32 nested levels with `log.ErrRecordLimit`.
- Correlation attributes consume the same total budget. Deterministic sampling
  drops keys above `sample.MaxKeyBytes` (1,024 bytes) at fractional rates before
  hashing; applications own key callback execution and allocation.

See [adoption](docs/adoption.md), [recipes](docs/recipes.md),
[operations](docs/operations.md), and [architecture](docs/architecture.md) for
complete guidance.

## Documentation

Use the [documentation index](docs/README.md) for adoption, architecture,
operations, recipes, compatibility, and migration guidance. For project help,
use [Support](SUPPORT.md); report vulnerabilities through the private process
in the [Security policy](SECURITY.md). See the [changelog](CHANGELOG.md) for
release history.

## License

MIT. See [LICENSE](LICENSE).
