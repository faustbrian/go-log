# Hardening Goal: Production Logging

## Objective

Prove that `log` remains correct under concurrency, hostile values, blocked
outputs, partial failures, and process shutdown.

## Required Audits

- Verify `Enabled`, `Handle`, `WithAttrs`, and `WithGroup` against the complete
  `slog.Handler` contract, including immutable derived handlers.
- Verify records and attributes are cloned before asynchronous retention.
- Exercise recursive and panicking `LogValuer` implementations safely.
- Prove redaction across nested groups, duplicate keys, errors, URLs, headers,
  tokens, credentials, and user-defined values.
- Define fan-out error aggregation and ensure one failed sink cannot corrupt
  successful sinks.
- Exercise bounded queues under saturation for block, drop-newest, drop-oldest,
  and synchronous-fallback policies.
- Prove flush and shutdown deadlines, repeated shutdown, and loss accounting.
- Test file rotation, permissions, rename failures, disk-full behavior, and
  concurrent writers.
- Verify trace correlation does not create dependency cycles or initialize the
  OpenTelemetry SDK implicitly.

## Required Deliverables

- Concurrency and race-test matrix for every stateful handler.
- Fuzz corpus for attributes, groups, values, redaction, and malformed inputs.
- Failure-injection suite for slow, blocked, short-writing, and failing sinks.
- Allocation and latency benchmark baselines with regression thresholds.
- Security review of secret exposure and log-forging boundaries.
- Updated API, operations, examples, FAQ, and `CHANGELOG.md`.

## Release Blockers

- Any data race, deadlock, unbounded queue, silent loss, or shutdown hang.
- Any path that leaks configured sensitive values.
- Any handler behavior that diverges from the `slog.Handler` contract.
- Any unbounded allocation caused by hostile attributes or values.
- Missing Meaningful 100% coverage or a failing GitHub Actions gate.

## Completion Criteria

- All audits and failure scenarios are automated and passing.
- Race, fuzz, vulnerability, compatibility, and benchmark gates pass.
- Documentation states every delivery and failure guarantee precisely.
- No release blocker remains open and `CHANGELOG.md` is current.
