# Contributing

Thank you for improving `log`. Contributions should preserve standard
`log/slog` types, explicit failure behavior, and bounded resource use.

## Development environment

- Go 1.24 or newer.
- Git.
- Optional CI tools: `golangci-lint`, `govulncheck`, and `apidiff`.

Clone the repository and run:

```sh
go mod download
go test ./...
go test -race ./...
go vet ./...
```

## Design rules

- Do not introduce a logger interface that replaces `*slog.Logger`.
- Handler decorators must implement the complete `slog.Handler` contract.
- Any retained records or attrs must be independently cloned.
- Every queue, retry, cache, and retained collection used in production must
  have an explicit bound.
- New vendor behavior belongs in an optional subpackage and should target the
  OpenTelemetry Collector when possible.
- OpenTelemetry SDK initialization and shutdown remain outside this module.
- Public APIs should be small and use standard context-aware types.

Discuss large API changes in an issue before implementation. Include failure,
concurrency, compatibility, and migration consequences.

## Tests

Use a red-green-refactor cycle for behavior changes. Tests should prove
observable behavior and failure semantics, not only execute lines.

Every change must preserve meaningful 100% statement coverage:

```sh
./scripts/check-coverage.sh
```

Stateful handlers require race tests. New attribute or redaction behavior must
add fuzz seeds where applicable. New pipeline overhead must include a benchmark
and an allocation budget when stable enough to enforce.

Run fuzz smoke tests locally:

```sh
go test ./handler/redact -run '^$' \
  -fuzz '^FuzzNestedAttributes$' -fuzztime=5s
go test ./handler/redact -run '^$' \
  -fuzz '^FuzzRedactionRules$' -fuzztime=5s
```

## Documentation

Every exported symbol needs a complete Go doc comment. User-visible changes
must update:

- the relevant guide or recipe;
- runnable examples when adoption changes;
- `CHANGELOG.md` under `Unreleased`;
- compatibility or security policy when guarantees change.

## Commits and pull requests

Use focused conventional commits with a body explaining why the change is
needed and its side effects. Pull requests should include:

- the problem and chosen contract;
- tests and failure injection performed;
- race, fuzz, coverage, vet, and lint results;
- compatibility and operational impact;
- documentation and changelog updates.

Do not bypass hooks or force-push shared review history. Maintainers may ask for
new commits rather than amended history.

## Releases

Maintainers create annotated semantic-version tags. Release automation verifies
the full suite, builds a deterministic source archive, publishes its checksum,
and uses the matching changelog section as release notes.
