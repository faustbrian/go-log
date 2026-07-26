package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/async"
	"github.com/faustbrian/golib/pkg/log/handler/redact"
	"github.com/faustbrian/golib/pkg/log/handler/sample"
	"github.com/faustbrian/golib/pkg/log/handler/stack"
	logotel "github.com/faustbrian/golib/pkg/log/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestStandardLoggerInteroperatesWithCompletePipeline(t *testing.T) {
	t.Parallel()

	var informational bytes.Buffer
	var failures bytes.Buffer
	options := &slog.HandlerOptions{ReplaceAttr: removeTime}
	stacked, err := stack.New(
		stack.Route{
			Handler:  slog.NewJSONHandler(&informational, options),
			MinLevel: slog.LevelInfo,
			MaxLevel: slog.LevelWarn,
		},
		stack.Route{
			Handler:  slog.NewJSONHandler(&failures, options),
			MinLevel: slog.LevelError,
		},
	)
	if err != nil {
		t.Fatalf("stack.New() error = %v", err)
	}
	queued, err := async.New(stacked, async.Options{Capacity: 8, Overflow: async.Block})
	if err != nil {
		t.Fatalf("async.New() error = %v", err)
	}
	policy, err := sample.Every(1)
	if err != nil {
		t.Fatalf("sample.Every() error = %v", err)
	}
	sampled, err := sample.New(queued, policy)
	if err != nil {
		t.Fatalf("sample.New() error = %v", err)
	}
	safe, err := redact.New(sampled, &redact.Options{Rules: []redact.Rule{redact.Keys("token")}})
	if err != nil {
		t.Fatalf("redact.New() error = %v", err)
	}
	correlated, err := logotel.New(safe, logotel.Options{})
	if err != nil {
		t.Fatalf("otel.New() error = %v", err)
	}
	logger := slog.New(correlated).
		With(slog.String("service", "orders")).
		WithGroup("request")
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		SpanID:  trace.SpanID{1, 0, 0, 0, 0, 0, 0, 1},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	logger.InfoContext(ctx, "accepted", slog.String("token", "secret"))
	logger.ErrorContext(ctx, "failed", slog.String("token", "secret"))
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := queued.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	info := decodeJSONLines(t, informational.Bytes())
	if len(info) != 1 || info[0]["msg"] != "accepted" {
		t.Fatalf("informational records = %v, want accepted only", info)
	}
	errors := decodeJSONLines(t, failures.Bytes())
	if len(errors) != 1 || errors[0]["msg"] != "failed" {
		t.Fatalf("failure records = %v, want failed only", errors)
	}
	for _, record := range append(info, errors...) {
		if record["service"] != "orders" {
			t.Errorf("service = %v, want orders", record["service"])
		}
		request, ok := record["request"].(map[string]any)
		if !ok {
			t.Fatalf("request = %T, want object", record["request"])
		}
		if request["token"] != redact.DefaultReplacement {
			t.Errorf("request.token = %v, want redacted", request["token"])
		}
		if request["trace_id"] != spanContext.TraceID().String() {
			t.Errorf("request.trace_id = %v, want %s", request["trace_id"], spanContext.TraceID())
		}
		if request["span_id"] != spanContext.SpanID().String() {
			t.Errorf("request.span_id = %v, want %s", request["span_id"], spanContext.SpanID())
		}
	}
}

func decodeJSONLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var records []map[string]any
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		records = append(records, record)
	}

	return records
}
