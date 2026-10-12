# Source documentation audit

## Inventory and scope

Baseline `28dec559ca2cc54acf610c27af12e262aaf69491` contains one public v2
module and 44 tracked Go files: nine maintained implementation files and 35
test files in eight public packages and `internal/slogrecord`. Reproduce this
inventory with `git ls-files '*.go' 'go.mod' '**/go.mod'` and
`golib source-docs inventory --json`. All source and test files were inspected,
including the `!race` latency-test variant and Windows-specific runtime skips
in rotation identity tests. No generated/vendor sources, embedded executable
templates, generation/deprecation directives or actionable debt markers were
found. The baseline has 18 missing field/interface-member comments.

| Owner | Contract gaps and maintained rationale |
| --- | --- |
| Root `log` | Default omission versus explicit trusted data, ordered options, group-copy versus opaque identity, standard slog formatting/error behavior and caller-owned sinks. |
| `handler/redact` | Raw rule matching before resolution, isolated group trees, trusted unmatched data/replacement/callbacks, cumulative limits and sticky derivation failures. |
| `internal/slogrecord` | Shared cumulative structural admission, raw versus resolved copying, partial results on failure and retained opaque objects. |
| `handler/stack` | Ordered synchronous routes, dynamic levels, joined returned errors versus propagated panics, independent group copies. |
| `handler/sample` | Shared sampler/counters, exact counter order, stable strict hash threshold, callback/returned-key limits and non-atomic aggregate snapshots. |
| `handler/async` | Queue versus byte/callback bounds, copied groups versus opaque values, admission rollback, fallback concurrency/reordering, completion ownership and cooperative shutdown. |
| `handler/capture` | Unbounded test retention, shared reset/buffer, resolved groups versus opaque objects, assertions that may emit fixture values. |
| `handler/rotate` | Trusted directory ownership, checked leaf identities, oversized/partial writes, nontransactional rotation/reopen, sync/close lifetime. |
| `otel` | Existing context correlation only, total budget including inserted IDs, duplicate keys, no provider/exporter/SDK lifecycle. |

All tests were audited for the boundaries they protect, rather than changed
uniformly. Two fuzz targets exercise selective structural redaction and
recursive/panicking values; two benchmarks compare deliberately different
pipelines or producer-side queue work. Six runnable examples cover default
omission, routing, selective redaction, async shutdown, local rotation and
trace correlation. Existing output/directive bytes remain authoritative.

## Preservation and selected checks

This is a prose-only pass: preserve every executable token/literal, compiler
and lint directive, example output, API signature, dependency and policy byte.
The native `.golib.yaml` API mode is `apidiff` at `api/log-v2.txt`; comments do
not regenerate symbol snapshots. The previous baseline remains historical.
`make -f verification/package.mk docs` selects existing examples and package
Go documentation; all tracked Go files also receive parser/token inspection.
Inspect full public Go-doc rendering and canonical package comment rendering,
objective source structure, local links, changed-content secrets and the
complete final diff. The OTel dependency guard is retained unchanged.

No behavior, performance, durability or cross-platform qualification is
claimed from this pass. Unchanged race, active fuzz, latency/allocation,
rotation failure-injection and release campaigns are not selected. The
coordinator owns remote-main/exact CI and shared-enforcement adoption; this
document does not replace their native authority. No module release is needed.
