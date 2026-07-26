# Goal: Add Logging Interoperability And Fair Comparisons

## Objective

Preserve `log/slog` as the public logging foundation while adding optional
interoperability and fair performance evidence where justified.

## Required Work

- Keep `*slog.Logger` and `slog.Handler` as application boundaries.
- Do not introduce Zap or Zerolog as alternate core logger interfaces.
- Add optional `bridge/zap` or `bridge/zerolog` modules only when a verified
  consumer needs incremental migration or third-party interoperability.
- Keep bridge dependencies outside core consumers.
- Preserve levels, names, attributes, groups, duplicate fields, context, caller
  information, errors, panic-safe values, disabled logging, and delivery
  semantics across every bridge.
- Add correctness-gated comparative benchmarks against stdlib slog, Zap,
  Zerolog, and a credible performance-oriented logger.
- Separate disabled, JSON, text, pre-bound attributes, groups, errors,
  redaction, sampling, fan-out, synchronous, and bounded asynchronous tracks.
- Match semantic fields, encoded bytes, timestamps, caller capture,
  synchronization, durability, flush, shutdown, and failure handling before
  ranking.

## Completion Criteria

- Core consumers remain free of optional logger dependencies.
- Any bridge is semantically tested, independently versioned, and documented.
- Comparative results are reproducible and do not rank unequal delivery paths.

