# Recipes

All recipes use standard `slog.Handler` composition. Constructors return or
accept standard handlers, and applications finish with `slog.New(handler)`.

## Route errors separately

```go
handler, err := stack.New(
	stack.Route{
		Handler:  slog.NewJSONHandler(os.Stdout, nil),
		MinLevel: slog.LevelInfo,
	},
	stack.Route{
		Handler:  slog.NewJSONHandler(os.Stderr, nil),
		MinLevel: slog.LevelError,
	},
)
```

Bounds are inclusive. A nil minimum or maximum is unbounded. Each route still
consults its downstream `Enabled` method. Every matching sink is attempted and
errors are joined with `errors.Join`.

## Create disjoint level streams

```go
handler, err := stack.New(
	stack.Route{
		Handler:  informational,
		MinLevel: slog.LevelInfo,
		MaxLevel: slog.LevelWarn,
	},
	stack.Route{
		Handler:  failures,
		MinLevel: slog.LevelError,
	},
)
```

## Redact common secret keys

```go
safe, err := redact.New(next, &redact.Options{
	Rules: []redact.Rule{redact.Keys(
		"api_key",
		"authorization",
		"cookie",
		"password",
		"refresh_token",
		"secret",
		"set-cookie",
		"token",
	)},
})
```

Key matching is case-insensitive at every nesting depth. Duplicate keys are
redacted independently. Matching occurs before `LogValuer` evaluation.

## Redact only an exact path

```go
safe, err := redact.New(next, &redact.Options{
	Rules: []redact.Rule{redact.Paths(
		"request.credentials.password",
		"response.headers.set-cookie",
	)},
})
```

Paths are dot-separated and case-insensitive. They describe group structure,
not a substring search. Use `redact.Any` to combine key and path policies.

## Use a typed replacement

```go
replacement := slog.BoolValue(true)
safe, err := redact.New(next, &redact.Options{
	Rules:       []redact.Rule{redact.Keys("secret")},
	Replacement: &replacement,
})
```

The default replacement is the string `[REDACTED]`.

## Keep one of every N records

```go
policy, err := sample.Every(100)
if err != nil {
	return err
}
sampled, err := sample.New(next, policy)
```

The first record is kept, then one record from every consecutive group of 100.
The counter is shared by derived handlers and safe for concurrent calls.

## Make a stable decision per tenant

```go
policy, err := sample.Deterministic(0.10,
	func(_ context.Context, record slog.Record) string {
		var tenant string
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "tenant" {
				tenant = attr.Value.String()
				return false
			}
			return true
		})
		return tenant
	},
)
```

The rate is in `[0,1]`. The same key always receives the same decision for that
rate. Do not derive the key from secrets or uncontrolled high-cardinality data
unless that behavior is intentional.

## Add bounded async delivery

```go
queued, err := async.New(next, async.Options{
	Capacity: 4096,
	Overflow: async.Block,
	OnError: func(err error) {
		asyncDeliveryFailures.Add(context.Background(), 1)
		_, _ = fmt.Fprintf(os.Stderr, "async log delivery: %v\n", err)
	},
})
```

The callback must be fast and must not log back through the same handler. A
panic is recovered, but callback work runs on the single delivery worker.

## Flush without shutdown

```go
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()
if err := queued.Flush(ctx); err != nil {
	return fmt.Errorf("flush logs: %w", err)
}
```

Flush snapshots accepted queue sequence under the submission lock and waits
for those records. New submissions may continue.

## Capture and assert logs

```go
captured := capture.New(capture.WithLevel(slog.LevelInfo))
logger := slog.New(captured)

logger.Info("created", slog.String("order.id", "ord-1"))

capture.AssertCount(t, captured, 1)
capture.AssertMessage(t, captured, "created")
capture.AssertAttr(t, captured, "order.id", "ord-1")
```

Assertion paths use dots for nested groups. Use `Records` for complex or
domain-specific assertions.

## Rotate a standard JSON log

```go
writer, err := rotate.New(rotate.Options{
	Path:     "/var/log/orders/service.json",
	MaxBytes: 100 * 1024 * 1024,
	Backups:  5,
	Mode:     0o600,
})
if err != nil {
	return err
}
logger := slog.New(slog.NewJSONHandler(writer, nil))
```

A write is never split. An individual record larger than `MaxBytes` is written
to an empty file and rotated before the following write.

## Add trace and span IDs

```go
correlated, err := logotel.New(next, logotel.Options{
	IncludeTraceFlags: true,
})
```

The bridge reads a standard OpenTelemetry span context. It initializes no SDK,
provider, exporter, or global. Put it before async delivery.

## A complete service pipeline

```go
stacked, _ := stack.New(stack.Route{Handler: json, MinLevel: slog.LevelInfo})
queued, _ := async.New(stacked, async.Options{
	Capacity: 4096,
	Overflow: async.Block,
})
sampled, _ := sample.New(queued, samplingPolicy)
safe, _ := redact.New(sampled, redactionOptions)
correlated, _ := logotel.New(safe, logotel.Options{})
logger := slog.New(correlated).With("service", "orders")
```

Production code must handle every constructor error; omitted checks above keep
the composition example compact.
