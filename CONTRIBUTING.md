# Contributing

## Before Editing

1. Read [`AGENTS.md`](AGENTS.md) and the affected package's docs.
2. Run the affected package's deterministic Go tests as described below.
3. Identify owned dependencies and reverse dependants in `modules.json`.
4. Preserve unrelated work and generated/corpus provenance.

## Changes

Keep commits focused and conventional. Update every affected changelog with
the behavior and migration impact. Public API changes require compatibility
evidence and documentation. Specification behavior requires a decision record,
fixture coverage, and interoperability evidence.

New direct dependencies and dependency updates must follow the
[dependency governance policy](AGENTS.md#dependencies-and-supply-chain). Package-local
update bots are forbidden; the root policy owns every module and action update.

Specification-backed changes must follow the
[specification governance contract](AGENTS.md#design), update
the affected stable decision entries, and complete the Specification Decisions
section of the pull request template. An unresolved interpretation or stale
source pin is release-blocking; peer behavior cannot silently select policy.

Selected full/release mutation gates must finish with zero surviving viable
mutants; mutation is not required for an ordinary local or pull-request check.

Do not add package-local workflows, permanent replacements, machine-specific
paths, bypass flags, broad mutation exclusions, or aggregate quality metrics
that hide a failing package.

## Verification

Use Go 1.27.2 for the producer; the public module minimum remains Go 1.27.0.
An isolated checkout needs no sibling repositories or coordination goal files.
Start with deterministic Go tests and, for a changed hostile-input boundary,
one focused active fuzz target:

```bash
GOWORK=off go test ./...
GOWORK=off go test ./handler/redact -run '^$' -fuzz '^FuzzRedactionRules$' -fuzztime=2s -timeout=2m
```

The first command includes fuzz seeds/corpus, not active fuzzing. Install the
verified public `golib` release declared in `.golib.yaml` for repository checks;
CI verifies the same release without building it from source:

```bash
make check
make ci
```

Both run bounded local checks and the existing OTel no-SDK/no-telemetry
dependency assertion; `ci` also checks repository/cohesion structure.
The selected async race runs once with a two-minute timeout and asserts
canceled flush responsiveness, delivery order/accounting, canceled shutdown
and post-shutdown refusal. No service or test tag is required. Base-free checks
run deterministic fuzz seeds/corpus; changed-source CI selects active smoke.

Optional one-package local mutation:

```bash
golib mutation --module . --package handler/redact
```

Use explicit full qualification for the affected release/risk boundary:

```bash
make check-full
golib api check
golib release dry-run --module .
```

Full checks retain documentation examples, the OTel dependency boundary, both
redaction fuzz targets at 10000x/2m, benchmarks and all original module gates.
The CLI's explicit `release dry-run` route can run locally for release
rehearsal; it is not restricted to CI. Report unavailable or failing commands
and never describe bounded checks alone as release qualification.

## Adding A Module

Follow [repository structure policy](AGENTS.md#repository-structure). New modules
require an explicit purpose, ownership boundary, dependency review, package
catalog entry, full quality gates, documentation, changelog, license, security
policy, compatibility plan, and release dry-run.
