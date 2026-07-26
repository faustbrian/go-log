# Compatibility policy

## Semantic versioning

`log` follows Semantic Versioning for exported Go APIs and documented
behavior.

Before v1.0.0, incompatible public API changes may occur in a minor release and
will be called out under `Changed` in `CHANGELOG.md`. Patch releases remain
backward-compatible. Starting with v1.0.0, incompatible changes require a major
version module path.

The following are part of the compatibility promise:

- exported identifiers, method signatures, constants, and sentinel errors;
- `errors.Is` relationships for documented errors;
- overflow, routing, redaction, flush, and shutdown semantics;
- counter definitions;
- default attribute names and replacement values;
- supported Go and OpenTelemetry versions.

Exact error strings, benchmark timings, internal goroutine structure, and
unexported implementation details are not stable APIs.

## Standard library contract

Every handler follows the `log/slog.Handler` contract of the minimum supported
Go release. Applications can always expose `*slog.Logger`, `slog.Handler`,
`slog.Record`, and `slog.Attr` directly.

The project does not promise byte-for-byte equivalence between different Go
versions' standard JSON or text handlers. Encoding is delegated to the standard
library specifically so its documented behavior remains authoritative.

## Go versions

The module's `go` directive is the minimum supported release. CI tests that
release and the current stable release. Support moves forward only in a minor
or major module release and is recorded in the changelog.

The current minimum is Go 1.24.

## OpenTelemetry versions

The optional `otel` package depends only on the stable OpenTelemetry trace API.
The current baseline is `go.opentelemetry.io/otel/trace` v1.41.0, the newest
release compatible with Go 1.24 when the dependency was selected.

Minor OpenTelemetry updates may be accepted after CI verifies the supported Go
matrix. The bridge does not depend on an SDK, exporter, or the `telemetry`
runtime module.

## Deterministic sampling

The deterministic key hash is stable within a major version. Changing it would
move sampled cohorts and is therefore a breaking behavioral change after v1.
Rates of exactly zero and one always drop or keep all records, respectively.

## File format

This project defines no custom wire or file format. JSON and text are emitted by
the standard library. Numbered rotated backups use `PATH.1`, `PATH.2`, and so
on; that naming convention is stable within a major version.

## API compatibility automation

CI compares exported APIs against the latest reachable release tag. A breaking
change fails unless performed on an appropriate major-version path. The initial
unreleased history has no tag and therefore records a baseline without a prior
release comparison.

## Deprecation

Deprecated APIs remain available for at least one minor release before v1 and
for at least one major release after v1, unless retaining them would preserve a
security vulnerability. Deprecation comments identify the replacement and the
changelog records removal timing.
