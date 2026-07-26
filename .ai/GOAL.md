# Goal: Production Logging for Go Services

## Objective

Build a serious open-source logging package on top of `log/slog`. The package
MUST preserve standard Go logging types, provide production-safe composition,
and avoid forcing applications behind a proprietary logger interface.

## Scope

- Use `*slog.Logger`, `slog.Handler`, `slog.Record`, and `slog.Attr` as the
  public foundation.
- Provide stack/fan-out handlers, per-handler level routing, deterministic
  attribute enrichment, groups, redaction, sampling, and bounded asynchronous
  delivery.
- Provide capture handlers and assertions suitable for tests.
- Provide optional local file rotation for non-container deployments.
- Integrate trace and span correlation through a small optional bridge to
  `telemetry`; this package MUST NOT own OpenTelemetry SDK lifecycle.
- Preserve standard JSON and text handlers rather than reimplementing them.
- Recommend stdout/stderr plus an OpenTelemetry Collector for Kubernetes.
  Direct Better Stack, Datadog, and similar vendor drivers are out of scope for
  v1 unless a transport cannot be represented safely through the Collector.

## Public API Principles

- Applications MUST remain able to accept and pass `*slog.Logger` directly.
- Handler decorators MUST obey the complete `slog.Handler` contract, including
  `Enabled`, `WithAttrs`, and `WithGroup` behavior.
- Async behavior MUST expose queue capacity, overflow policy, flush, shutdown,
  and delivery-loss accounting explicitly.
- Redaction MUST be structural and configurable; string replacement alone is
  not sufficient.
- Vendor-specific concerns MUST be isolated in optional subpackages.

## Package Shape

- Root package: stable constructors, options, errors, and composition.
- `handler/stack`: fan-out and routing.
- `handler/redact`: structural sensitive-value filtering.
- `handler/sample`: deterministic and rate-based sampling.
- `handler/async`: bounded asynchronous delivery.
- `handler/capture`: test capture and assertions.
- `handler/rotate`: optional rotating-file output.
- `otel`: optional trace/span correlation bridge.

## Quality Requirements

- Meaningful 100% statement coverage is required. Tests MUST prove behavior,
  invariants, failure modes, and concurrency semantics rather than merely
  execute lines.
- Race tests MUST cover every stateful handler and shutdown path.
- Fuzz tests MUST cover nested attributes, groups, `LogValuer`, malformed
  values, and redaction rules.
- Benchmarks MUST track allocation and latency overhead for common pipelines.
- Public APIs MUST remain small, idiomatic, context-aware, and semver-safe.

## Documentation Deliverables

- Complete API documentation for every exported symbol.
- Adoption guide for stdlib `slog`, HTTP services, workers, and Kubernetes.
- Recipes for stack routing, redaction, sampling, async delivery, testing,
  file rotation, and trace correlation.
- Operational guidance for backpressure, shutdown, loss policies, and secrets.
- Architecture guide, compatibility policy, migration guide, examples, FAQ,
  security policy, contribution guide, and maintained `CHANGELOG.md`.

## Automation And Release

GitHub Actions MUST run formatting, vetting, linting, tests, race tests, fuzz
smoke tests, coverage enforcement, vulnerability scanning, examples, and API
compatibility checks. Releases MUST be reproducible, tagged, and documented.

## Phases

1. Specify handler contracts, failure semantics, and public API.
2. Implement synchronous composition, routing, redaction, and capture.
3. Implement sampling, bounded async delivery, rotation, and telemetry bridge.
4. Complete hostile-input, race, fuzz, benchmark, and interoperability work.
5. Publish complete adoption and operational documentation.

## Acceptance Criteria

- The package composes standard `slog` handlers without semantic divergence.
- Concurrency, shutdown, overflow, redaction, and error behavior are explicit
  and verified.
- Meaningful 100% coverage and all GitHub Actions gates pass.
- Documentation supports adoption without reading implementation source.
- `CHANGELOG.md` accurately records every user-visible change.
