package log_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/async"
	"github.com/faustbrian/golib/pkg/log/handler/redact"
	"github.com/faustbrian/golib/pkg/log/handler/sample"
	"github.com/faustbrian/golib/pkg/log/handler/stack"
)

func BenchmarkPipelines(benchmark *testing.B) {
	record := benchmarkRecord()
	jsonHandler := slog.NewJSONHandler(io.Discard, nil)
	redacted, err := redact.New(jsonHandler, &redact.Options{
		Rules: []redact.Rule{redact.Keys("token")},
	})
	if err != nil {
		benchmark.Fatalf("redact.New() error = %v", err)
	}
	stacked, err := stack.New(
		stack.Route{Handler: redacted, MinLevel: slog.LevelInfo},
		stack.Route{Handler: slog.NewTextHandler(io.Discard, nil), MinLevel: slog.LevelError},
	)
	if err != nil {
		benchmark.Fatalf("stack.New() error = %v", err)
	}
	every, err := sample.Every(10)
	if err != nil {
		benchmark.Fatalf("sample.Every() error = %v", err)
	}
	sampled, err := sample.New(stacked, every)
	if err != nil {
		benchmark.Fatalf("sample.New() error = %v", err)
	}

	benchmarks := map[string]slog.Handler{
		"stdlib-json": jsonHandler,
		"redact":      redacted,
		"stack":       stacked,
		"sampled":     sampled,
	}
	for name, handler := range benchmarks {
		benchmark.Run(name, func(benchmark *testing.B) {
			benchmark.ReportAllocs()
			for benchmark.Loop() {
				if err := handler.Handle(context.Background(), record); err != nil {
					benchmark.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkAsync(benchmark *testing.B) {
	handler, err := async.New(slog.NewJSONHandler(io.Discard, nil), async.Options{
		Capacity: 1024,
		Overflow: async.Block,
	})
	if err != nil {
		benchmark.Fatalf("async.New() error = %v", err)
	}
	record := benchmarkRecord()
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for benchmark.Loop() {
		if err := handler.Handle(context.Background(), record); err != nil {
			benchmark.Fatal(err)
		}
	}
	benchmark.StopTimer()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := handler.Shutdown(ctx); err != nil {
		benchmark.Fatalf("Shutdown() error = %v", err)
	}
}

func TestAllocationBudgets(t *testing.T) {
	record := benchmarkRecord()
	noop := noopHandler{}
	stacked, err := stack.New(stack.Route{Handler: noop})
	if err != nil {
		t.Fatalf("stack.New() error = %v", err)
	}
	redacted, err := redact.New(noop, &redact.Options{Rules: []redact.Rule{redact.Keys("token")}})
	if err != nil {
		t.Fatalf("redact.New() error = %v", err)
	}
	every, err := sample.Every(2)
	if err != nil {
		t.Fatalf("sample.Every() error = %v", err)
	}
	sampled, err := sample.New(noop, every)
	if err != nil {
		t.Fatalf("sample.New() error = %v", err)
	}

	tests := map[string]struct {
		handler slog.Handler
		max     float64
	}{
		"stack":  {handler: stacked, max: 2},
		"redact": {handler: redacted, max: 12},
		"sample": {handler: sampled, max: 2},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			allocations := testing.AllocsPerRun(1_000, func() {
				if err := test.handler.Handle(context.Background(), record); err != nil {
					t.Fatal(err)
				}
			})
			if allocations > test.max {
				t.Fatalf("allocations = %.2f, budget = %.2f", allocations, test.max)
			}
		})
	}
}

type noopHandler struct{}

func (noopHandler) Enabled(context.Context, slog.Level) bool   { return true }
func (noopHandler) Handle(context.Context, slog.Record) error  { return nil }
func (handler noopHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler noopHandler) WithGroup(string) slog.Handler      { return handler }

func benchmarkRecord() slog.Record {
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "request complete", 0)
	record.AddAttrs(
		slog.String("service", "orders"),
		slog.String("token", "secret"),
		slog.Group("http", slog.String("method", "GET"), slog.Int("status", 200)),
	)

	return record
}
