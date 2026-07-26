//go:build !race

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

func TestLatencyBudgets(t *testing.T) {
	record := benchmarkRecord()
	jsonHandler := slog.NewJSONHandler(io.Discard, nil)
	redacted, err := redact.New(jsonHandler, &redact.Options{
		Rules: []redact.Rule{redact.Keys("token")},
	})
	if err != nil {
		t.Fatalf("redact.New() error = %v", err)
	}
	stacked, err := stack.New(
		stack.Route{Handler: redacted, MinLevel: slog.LevelInfo},
		stack.Route{Handler: slog.NewTextHandler(io.Discard, nil), MinLevel: slog.LevelError},
	)
	if err != nil {
		t.Fatalf("stack.New() error = %v", err)
	}
	every, err := sample.Every(10)
	if err != nil {
		t.Fatalf("sample.Every() error = %v", err)
	}
	sampled, err := sample.New(stacked, every)
	if err != nil {
		t.Fatalf("sample.New() error = %v", err)
	}
	asyncHandler, err := async.New(jsonHandler, async.Options{
		Capacity: 1024,
		Overflow: async.Block,
	})
	if err != nil {
		t.Fatalf("async.New() error = %v", err)
	}

	tests := map[string]struct {
		handler slog.Handler
		maxNS   int64
	}{
		"stdlib-json": {handler: jsonHandler, maxNS: 5_000},
		"redact":      {handler: redacted, maxNS: 10_000},
		"stack":       {handler: stacked, maxNS: 15_000},
		"sampled":     {handler: sampled, maxNS: 10_000},
		"async":       {handler: asyncHandler, maxNS: 20_000},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			result := testing.Benchmark(func(benchmark *testing.B) {
				for benchmark.Loop() {
					if err := test.handler.Handle(context.Background(), record); err != nil {
						benchmark.Fatal(err)
					}
				}
			})
			if result.NsPerOp() > test.maxNS {
				t.Fatalf(
					"latency = %d ns/op, budget = %d ns/op",
					result.NsPerOp(),
					test.maxNS,
				)
			}
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := asyncHandler.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
