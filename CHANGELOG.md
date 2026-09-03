# Changelog

All notable user-visible changes to this project are documented here. The
format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Replace copied repository verification tooling with the released
  `go-library-tools` v1.0.5 contract while retaining logging-specific API,
  fuzzing, benchmark, and OpenTelemetry boundary verification.
- Adopt the checksum-verified `go-library-tools` v1.4.0 CLI, schema-v2 cohesion
  metadata, and repository-local cohesion gate while retaining package-owned
  source, tests, and evidence.
- Pin reusable CI to the immutable v1.4.0 W14 enforcement workflow and resolve
  public module releases ahead of bootstrap-only fallbacks.

### Documentation

- Replace obsolete repository links and completed execution artifacts with a
  standalone, human-oriented documentation structure.
- Link the module to the versioned Golib ecosystem and observability-family
  guidance, and align the minimum Go claim with `go.mod`.

## [1.0.0] - 2026-08-25

### Changed

- Exclude intentional nested modules from root local-proxy archives so local,
  bootstrap, CI, and public module checksums describe the same source
  boundary.

- Track the pinned documentation-tool lockfile so clean CI checkouts install
  the exact validated cspell dependency.

- Reconcile standalone dependency checksums against deterministic current
  module archives so CI, local verification, and release consumers resolve
  identical content.

- Harden standalone documentation validation with deterministic spelling and
  link checks, package-specific documentation gates, and repository-local
  contributor guidance.

### Documentation

- Replace obsolete standalone-repository links and workflow claims with
  current package documentation and release guidance.

- Link the package README to package-owned documentation.

### Changed

- Publish the module from its standalone `github.com/faustbrian/go-log` identity while preserving its documented API and behavior.
- Execute API compatibility tooling against the isolated module graph so owned
  dependency source changes cannot conflict with release checksums.
- Use the repository-pinned current `apidiff` revision for the canonical API
  compatibility gate.

### Added

- Standard `*slog.Logger`, JSON, and text constructors with ordered handler
  options.
- Synchronous fan-out with inclusive per-handler level routing, isolated record
  clones, and joined sink errors.
- Structural key and path redaction with configurable replacement values.
- Concurrency-safe every-N and deterministic key sampling with counters.
- Bounded asynchronous delivery with block, drop-newest, drop-oldest, and
  synchronous-fallback overflow policies.
- Deadline-aware flush and repeatable shutdown with explicit delivery and loss
  accounting.
- Concurrent capture handler, record snapshots, reset, and test assertions.
- Permission-enforced local file rotation with bounded numbered backups.
- Optional OpenTelemetry trace, span, and trace-flags correlation bridge.
- Race, failure-injection, fuzz, allocation-budget, latency-budget, and
  benchmark coverage.

### Fixed

- Give every stack route an independently owned `WithAttrs` slice so one
  downstream handler cannot mutate attributes observed by another.
- Deep-clone nested group slices for stack fan-out and bound attributes so
  group mutation cannot cross sink boundaries.
- Preserve context values while stripping cancellation from async delivery, and
  make block overflow independent of cancellation as required by `slog.Handler`.
- Normalize capture output by ignoring zero and empty-group attributes, inlining
  empty-key groups, and deep-cloning nested groups in returned snapshots.
- Return successful repeated shutdown deterministically once async draining has
  completed, even when a later caller supplies an already canceled context.
- Deep-clone nested groups before invoking custom samplers so sampler mutation
  cannot alter the downstream record.
- Isolate every redaction rule from nested group storage so a custom rule cannot
  mutate structure and bypass later secret rules.

[Unreleased]: https://github.com/faustbrian/go-log/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/faustbrian/go-log/releases/tag/v1.0.0
