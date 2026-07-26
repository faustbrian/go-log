package log_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	log "github.com/faustbrian/golib/pkg/log"
	"github.com/faustbrian/golib/pkg/log/handler/async"
	"github.com/faustbrian/golib/pkg/log/handler/capture"
	"github.com/faustbrian/golib/pkg/log/handler/redact"
	"github.com/faustbrian/golib/pkg/log/handler/rotate"
	"github.com/faustbrian/golib/pkg/log/handler/stack"
	logotel "github.com/faustbrian/golib/pkg/log/otel"
	"go.opentelemetry.io/otel/trace"
)

func ExampleNew() {
	options := &slog.HandlerOptions{ReplaceAttr: removeTime}
	logger, err := log.New(
		slog.NewTextHandler(os.Stdout, options),
		log.WithAttrs(slog.String("service", "orders")),
	)
	if err != nil {
		panic(err)
	}
	logger.Info("ready")

	// Output:
	// level=INFO msg=ready service=orders
}

func Example_stackRouting() {
	var application bytes.Buffer
	var failures bytes.Buffer
	options := &slog.HandlerOptions{ReplaceAttr: removeTime}
	handler, err := stack.New(
		stack.Route{
			Handler:  slog.NewTextHandler(&application, options),
			MinLevel: slog.LevelInfo,
		},
		stack.Route{
			Handler:  slog.NewTextHandler(&failures, options),
			MinLevel: slog.LevelError,
		},
	)
	if err != nil {
		panic(err)
	}
	logger := slog.New(handler)
	logger.Info("accepted")
	logger.Error("failed")

	fmt.Println(strings.TrimSpace(application.String()))
	fmt.Println(strings.TrimSpace(failures.String()))

	// Output:
	// level=INFO msg=accepted
	// level=ERROR msg=failed
	// level=ERROR msg=failed
}

func Example_structuralRedaction() {
	var output bytes.Buffer
	next := slog.NewJSONHandler(&output, &slog.HandlerOptions{ReplaceAttr: removeTime})
	handler, err := redact.New(next, &redact.Options{
		Rules: []redact.Rule{redact.Keys("password", "authorization")},
	})
	if err != nil {
		panic(err)
	}
	slog.New(handler).Info("login",
		slog.String("user", "alice"),
		slog.String("password", "secret"),
	)

	fmt.Print(output.String())

	// Output:
	// {"level":"INFO","msg":"login","user":"alice","password":"[REDACTED]"}
}

func Example_boundedAsync() {
	sink := capture.New()
	handler, err := async.New(sink, async.Options{
		Capacity: 16,
		Overflow: async.Block,
	})
	if err != nil {
		panic(err)
	}
	slog.New(handler).Info("queued")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Shutdown(ctx); err != nil {
		panic(err)
	}

	fmt.Println(sink.Len(), handler.Stats().Delivered)

	// Output:
	// 1 1
}

func Example_rotatingStandardJSON() {
	directory, err := os.MkdirTemp("", "log-example")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(directory)
	}()
	writer, err := rotate.New(rotate.Options{
		Path:     directory + "/service.log",
		MaxBytes: 1 << 20,
		Backups:  2,
	})
	if err != nil {
		panic(err)
	}
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: removeTime}))
	logger.Info("local")
	if err := writer.Close(); err != nil {
		panic(err)
	}
	contents, err := os.ReadFile(directory + "/service.log")
	if err != nil {
		panic(err)
	}
	fmt.Print(string(contents))

	// Output:
	// {"level":"INFO","msg":"local"}
}

func Example_traceCorrelation() {
	sink := capture.New()
	handler, err := logotel.New(sink, logotel.Options{})
	if err != nil {
		panic(err)
	}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		SpanID:  trace.SpanID{1, 0, 0, 0, 0, 0, 0, 1},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	slog.New(handler).InfoContext(ctx, "correlated")

	record, _ := sink.Last()
	record.Attrs(func(attr slog.Attr) bool {
		fmt.Printf("%s=%s\n", attr.Key, attr.Value.String())
		return true
	})

	// Output:
	// trace_id=01000000000000000000000000000001
	// span_id=0100000000000001
}

func removeTime(_ []string, attr slog.Attr) slog.Attr {
	if attr.Key == slog.TimeKey {
		return slog.Attr{}
	}

	return attr
}
