# Security policy

## Supported versions

Until the first stable release, security fixes are applied to the latest minor
release only. After v1, the latest major release receives fixes; additional
support windows will be documented here when offered.

| Version | Supported |
| --- | --- |
| Unreleased | Yes |

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private
vulnerability reporting for `faustbrian/log`. Include:

- affected version or commit;
- minimal reproduction;
- impact and realistic attack path;
- whether secrets or data were exposed;
- any proposed remediation or embargo constraints.

Expect acknowledgement within five business days. A fix, advisory, and release
timeline depends on severity and coordinated disclosure needs. No bounty is
currently promised.

## Threat model

The package assumes attributes, messages, contexts, filesystem state, and
downstream handlers may be slow or malformed. It protects against:

- unbounded async queues;
- cross-sink record mutation;
- panicking and recursively returning `LogValuer` values;
- configured secret values hidden in nested or duplicate attributes;
- indefinite caller waits during flush and shutdown when contexts are bounded;
- permissive newly created rotating files;
- one sink error preventing other stack routes.

It does not protect against:

- secrets placed in record messages or rendered before redaction;
- a downstream handler placed before redaction;
- an arbitrary handler that blocks forever while ignoring context;
- abrupt process or host failure before in-memory async delivery;
- compromised filesystem, Collector, backend, or process memory;
- unsafe custom redaction rules that deliberately expose values;
- disk exhaustion caused outside the configured numbered backups.

## Secret-exposure review

Redaction evaluates rules against raw structural attributes. A matching value is
replaced before resolution, including errors, URLs, headers, credentials,
structs, and `LogValuer` implementations. Group children and duplicate keys are
walked independently. Key rules are case-insensitive; path rules match exact
case-insensitive structural paths.

Messages are intentionally outside redaction because string replacement cannot
reliably distinguish secret data. Applications must use fixed messages and put
untrusted values in attributes. JSON is recommended over text when downstream
line-oriented parsers could be vulnerable to newline log forging.

The replacement itself is configuration. Do not configure it with a sensitive
value. Review rule changes like access-control policy changes and test concrete
secret fixtures.

## Resource-exhaustion review

Async queue memory is bounded by `Capacity`. Completion tracking is bounded by
accepted queue sequences. Sampling and capture counters use fixed-size atomic
state, although capture deliberately grows with retained test records until
`Reset` and is not a production sink.

Attribute processing allocates in proportion to the supplied record and nested
groups. `slog.Value.Resolve` bounds recursive `LogValuer` evaluation. File
rotation bounds numbered backups but one atomic record may exceed `MaxBytes`.

## Dependency and supply-chain policy

The root and handler packages use the Go standard library only. The optional
`otel` package uses the stable OpenTelemetry trace API and no SDK/exporter.
CI runs `govulncheck`, dependency review for pull requests, and reproducible
release archive checksums.

Release tags are never force-updated. Consumers should verify tags and the
published SHA-256 checksum before using a source archive outside the Go module
proxy.
