# Changelog

All notable user-visible changes to this project are documented here. The
format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
- Race, failure-injection, fuzz, allocation-budget, and benchmark coverage.

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

[Unreleased]: https://github.com/faustbrian/go-log/compare/HEAD...HEAD
