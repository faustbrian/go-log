# Security policy

## Supported versions

The latest stable v2 release receives security fixes. Older releases and the
`main` branch are unsupported; upgrade before reporting unless the issue is a
regression under active development.

| Version | Supported |
| --- | --- |
| Latest stable v2 release | Yes |
| Older releases | No |
| `main` | No |

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use
[GitHub's private vulnerability reporting for
`faustbrian/go-log`](https://github.com/faustbrian/go-log/security/advisories/new).
Include:

- affected version or commit;
- minimal reproduction;
- impact and realistic attack path;
- whether secrets or data were exposed;
- any proposed remediation or embargo constraints.

Expect acknowledgement within five business days. A fix, advisory, and release
timeline depends on severity and coordinated disclosure needs. No bounty is
currently promised.

## Threat model

Threat model version: 2.0 (reviewed 2026-10-01), scoped to the v2.0.0
source contract. Version v2.0.0 was published on 2026-10-03.

The package assumes attributes, messages, contexts, filesystem state, and
downstream handlers may be slow or malformed. It protects against:

- unbounded async queues;
- cross-sink record mutation;
- panicking and recursively returning `LogValuer` values;
- attribute trees exceeding the fixed 1,024-attribute or 32-level budget;
- private attribute names, values, and group identifiers through default data
  omission without evaluating discarded values;
- free-form record messages through default replacement, unless the caller
  explicitly preserves a bounded, trusted fixed message;
- indefinite caller waits during flush and shutdown when contexts are bounded,
  including cooperative cancellation of in-flight async deliveries;
- unbounded owned admission waits: the default overflow policy drops newest,
  and waiting policies require an explicit positive admission timeout;
- permissive, symbolic-link, and non-regular rotating file paths;
- one sink error preventing other stack routes.

It does not protect against:

- values rendered before redaction;
- a downstream handler placed before redaction;
- an arbitrary handler that blocks forever while ignoring context;
- abrupt process or host failure before in-memory async delivery;
- a filesystem adversary that can replace path components during an operation,
  hard-link files, or modify the parent directory;
- compromised Collector, backend, or process memory;
- unsafe custom redaction rules that deliberately expose values;
- disk exhaustion caused outside the configured numbered backups.

## Secret-exposure review

Root `New`, `JSON`, and `Text`, and zero-option redaction, omit all caller
attributes and group identifiers and replace the message with `[REDACTED]`.
Discarded `LogValuer` values are not evaluated. Time and level remain visible;
explicit source and replacement callbacks remain application collaborators.

`TrustedNew`, `TrustedJSON`, and `TrustedText` explicitly preserve classified
data through the same structural owner. Selective redaction requires
`PreserveTrustedAttributes: true`: rules see independent bounded structural
copies, matched values are replaced before resolution, and groups and duplicate
keys are walked independently. Key and path rules are case-insensitive.

Messages are replaced completely by default because string inspection cannot
reliably distinguish secret data. Applications may set
`PreserveTrustedMessage` only for fixed, application-controlled messages; the
opt-in rejects messages larger than 1,024 bytes. Variable or untrusted data
must be omitted or explicitly classified and selectively redacted. JSON is
recommended over text when downstream
line-oriented parsers could be vulnerable to newline log forging.

The replacement itself is configuration. Do not configure it with a sensitive
value. Review rule changes like access-control policy changes and test concrete
secret fixtures.

## Resource-exhaustion review

Async queue record count is bounded by `Capacity`; this is not a byte-memory
bound. Completion tracking coalesces completed sequences into disjoint intervals
behind unresolved accepted gaps; storage follows the worker, queue capacity and
single active fallback, not accumulated submission history. Sampling and capture
counters use fixed-size atomic state, although
capture deliberately grows with retained test records until `Reset` and is not
a production sink. Admission timeout bounds owned waiting before acceptance,
not callback execution or downstream delivery.

Attribute processing stops when cumulative bound and record structure reaches
1,024 attributes or 32 nested levels and returns `log.ErrRecordLimit`.
Correlation checks record admission before copying attributes, and its added
two or three attributes consume the same total budget. Deterministic sampling
hashes at most `sample.MaxKeyBytes` (1,024 bytes); a larger returned key is
dropped for rates strictly between zero and one. Rates zero and one do not call
the key callback. This does not bound callback execution or string allocation.
`slog.Value.Resolve` bounds recursive
`LogValuer` evaluation. File rotation bounds numbered backups but one atomic
record may exceed `MaxBytes`.

## Accepted residual risks

Severity below is an accepted deployment-dependent impact assessment, not a
claim that collaborator behavior is controlled by this library.

| Risk and severity | Owner and rationale | Mitigation and review condition |
| --- | --- | --- |
| Uncooperative downstream delivery or OnError can block shutdown — high | Application owners; Go cannot forcibly stop arbitrary code without detached work | Use cooperative bounded transports and prompt callbacks; revisit when an owned downstream adapter or slog lifecycle contract is added. |
| Parent-directory replacement or hard links during rotation — high | Operators; portable path checks are not an atomic cross-platform no-follow open | Restrict directory ownership and permissions; prefer stdout plus a Collector. Revisit platform-specific secure-open support. |
| One atomic encoded record can exceed MaxBytes — medium | Operators; splitting would corrupt record boundaries | Bound classified fields and monitor disk use; revisit if record-splitting semantics are introduced. |
| Structural limits do not bound trusted string/key/opaque-value bytes or already materialized caller input — medium | Application owners; slog permits arbitrary payload sizes and opaque objects retain their ownership | Default omission protects ordinary root output; for trusted variants bound fields upstream and use finite queue capacity. Revisit an owned byte-budget contract. |
| Trusted LogValuer/rules/samplers/key/level/output callbacks can block or allocate — high | Application owners; callbacks cannot be forcibly preempted | Use prompt callbacks and cooperative I/O. AdmissionTimeout bounds acquisition, not callback execution; revisit owned cancelable adapters. |
| Capture retains records until Reset — medium | Test owners; this is a test assertion sink, not production storage | Keep fixtures finite and reset scopes; revisit if capture is used in production. |
| Explicit trusted variants and selective rules can expose private names or values — high | Application owners; opt-in intentionally delegates classification | Classify all preserved names/values, test rules, and put privacy before every sink. Structural copies do not deep-copy opaque objects; revisit newly owned value types. |
| Capacity, backups, paths, option/rule/key/path lists and routing composition remain application configuration — medium | Operators; record bounds do not impose universal configuration or callback-work budgets | Set finite operational limits and restrict paths; revisit externally supplied configuration. |
| Returned collaborator errors and OnError diagnostics remain private caller-facing data — medium | Application owners; these are not library-emitted logs | Publish only bounded nominal diagnostics; revisit any owned surface that emits collaborator errors. |
| Supplied or prebound handlers, custom Option and ReplaceAttr callbacks can bypass privacy or introduce private data — high | Application owners; a wrapper cannot erase data already bound in an arbitrary downstream handler | Do not prebind private data; custom options must retain the incoming privacy owner, and replacement/source options require explicit review. Revisit an owned downstream implementation. |

## Public variant ownership

All eight packages share the structural record owner; they do not all implement
a privacy sink. Compose the root default or default redaction before processing
request-controlled data. Direct use of a decorator preserves its intentionally
distinct application-selected contract.

| Package / variant | Owned boundary | Application responsibility |
| --- | --- | --- |
| Root New/JSON/Text | Default omission, fixed message, cumulative derivation/record bounds | Supplied/prebound handlers, custom options, explicit source/replacement callbacks and output I/O |
| Root TrustedNew/TrustedJSON/TrustedText | Same bounds, preserved data, 1,024-byte message limit | Explicit classification and byte budgets for preserved fields |
| handler/redact | Default omission; explicit selective mode with per-rule independent structural copies | Trusted rules and opaque values; private group/name classification in selective mode |
| handler/async | Finite queue count, nonblocking default, timed waiting admission, ordered accepted delivery, cancelable Flush and cooperative shutdown | Callback cooperation, queue byte budget, bounded Flush/Shutdown contexts; Handle call cancellation intentionally does not discard accepted delivery |
| handler/capture | Bounded structural copies and isolated snapshots | Test-only finite retention and Reset; no production storage claim |
| handler/rotate | File identity/regular-file checks, bounded numbered backups, atomic writes | Classified output, directory trust and bounded underlying I/O |
| handler/sample | Structural admission and bounded owned deterministic hash | Callback time/allocation and application-selected sampling loss policy |
| handler/stack | Per-route structural copies and independent error routing | Finite route composition, private predicates/errors and cooperative sinks |
| otel | Structural preflight before correlation copying and total correlation budget | Explicit correlation keys, private context metadata and cooperative downstream I/O |

## Dependency and supply-chain policy

The root and handler packages use the Go standard library only. The optional
`otel` package uses the stable OpenTelemetry trace API and no SDK/exporter.
CI runs `govulncheck`, dependency review for pull requests, and reproducible
release archive checksums.

Release tags are never force-updated. Consumers should verify tags and the
published SHA-256 checksum before using a source archive outside the Go module
proxy.
