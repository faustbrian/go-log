# Adoption guide

This guide adopts `log` without changing application-facing logger types.
The boundary remains `*slog.Logger`, so packages that already use `log/slog`
need no adapter.

## From the standard library

An existing service usually starts with:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
```

Keep that standard sink and decorate its handler:

```go
json := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
})

redacted, err := redact.New(json, &redact.Options{
	Rules: []redact.Rule{redact.Keys(
		"authorization",
		"cookie",
		"password",
		"refresh_token",
		"token",
	)},
})
if err != nil {
	return err
}

logger := slog.New(redacted).With("service", "orders")
```

No call site changes are required. Continue to use `InfoContext`, `LogAttrs`,
`With`, and `WithGroup` from `log/slog`.

## HTTP services

Construct one process logger during startup. Add request attributes by deriving
a logger; do not mutate a global logger.

```go
type contextKey struct{}

func withRequestLogger(next http.Handler, base *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		logger := base.With(
			slog.String("http.method", request.Method),
			slog.String("http.route", routeTemplate(request)),
		)
		ctx := context.WithValue(request.Context(), contextKey{}, logger)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}
```

Use route templates, not raw paths containing identifiers, to control log
volume and sensitive data. Do not log request or response bodies by default.
Place the `otel` handler before async delivery so trace/span IDs are read while
the request context is available.

## Workers

Pass `*slog.Logger` into the worker constructor and derive job metadata at the
start of each execution.

```go
type Worker struct {
	logger *slog.Logger
}

func NewWorker(logger *slog.Logger) *Worker {
	return &Worker{logger: logger}
}

func (worker *Worker) Run(ctx context.Context, job Job) error {
	logger := worker.logger.With(
		slog.String("job.type", job.Type),
		slog.String("job.id", job.ID),
	)
	logger.InfoContext(ctx, "job started")
	// Work omitted.
	return nil
}
```

Choose `Block` when losing audit or billing records is unacceptable and the
worker may slow down. Choose `DropNewest` or `DropOldest` only for explicitly
loss-tolerant diagnostic streams. Export `Stats().Lost()` to service metrics.

## Graceful shutdown

The application owns async and rotating resources. Stop producers first, then
drain logging within the remaining termination budget.

```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := asyncHandler.Shutdown(shutdownCtx); err != nil {
	_, _ = fmt.Fprintf(os.Stderr, "log shutdown: %v\n", err)
}
if err := rotatingWriter.Close(); err != nil {
	_, _ = fmt.Fprintf(os.Stderr, "log file close: %v\n", err)
}
```

Do not log shutdown failures through the handler being shut down. Use stderr,
a process supervisor, or an already independent emergency sink.

## Kubernetes

Use the standard JSON handler on stdout:

```go
handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	Level: slog.LevelInfo,
})
logger := slog.New(handler)
```

Deploy an OpenTelemetry Collector as a DaemonSet, sidecar, or gateway according
to platform requirements. The Collector should own:

- file or container-runtime ingestion;
- batching and retry queues;
- resource enrichment;
- TLS and vendor credentials;
- routing to Better Stack, Datadog, or other backends.

Avoid application-side file rotation in containers. Container runtimes and the
node logging agent need stdout/stderr to preserve lifecycle and backpressure
semantics.

## Local development

Use the standard text handler for readable output and the same redaction rules
as production:

```go
text := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
	Level: slog.LevelDebug,
})
safe, _ := redact.New(text, productionRedactionOptions())
logger := slog.New(safe)
```

Changing format must not weaken secret handling.

## Tests

Inject a standard logger backed by capture:

```go
captured := capture.New()
service := NewService(slog.New(captured))

service.Run(context.Background())

capture.AssertMessage(t, captured, "service started")
capture.AssertAttr(t, captured, "service", "orders")
```

Call `Reset` between table cases or create a new handler for each test. Returned
records are independent snapshots and may be inspected without locking.
