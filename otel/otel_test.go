package otel_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/capture"
	logotel "github.com/faustbrian/golib/pkg/log/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestNewRejectsNilHandler(t *testing.T) {
	t.Parallel()

	handler, err := logotel.New(nil, logotel.Options{})

	if handler != nil {
		t.Fatalf("New(nil) handler = %v, want nil", handler)
	}
	if !errors.Is(err, logotel.ErrNilHandler) {
		t.Fatalf("New(nil) error = %v, want ErrNilHandler", err)
	}
}

func TestHandleAddsValidSpanCorrelation(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, logotel.Options{IncludeTraceFlags: true})
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		SpanID:     trace.SpanID{1, 2, 3, 4, 5, 6, 7, 8},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), spanContext)
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.String("safe", "value"))

	if err := handler.Handle(ctx, record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if !capture.AssertAttr(t, sink, "trace_id", spanContext.TraceID().String()) {
		t.FailNow()
	}
	if !capture.AssertAttr(t, sink, "span_id", spanContext.SpanID().String()) {
		t.FailNow()
	}
	if !capture.AssertAttr(t, sink, "trace_flags", "01") {
		t.FailNow()
	}
	if !capture.AssertAttr(t, sink, "safe", "value") {
		t.FailNow()
	}
}

func TestHandleOmitsCorrelationWithoutValidSpan(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, logotel.Options{})
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if !capture.AssertCount(t, sink, 1) {
		t.FailNow()
	}
	if capture.AssertAttr(&quietTestingT{}, sink, "trace_id", "anything") {
		t.Fatal("invalid context unexpectedly contains trace correlation")
	}
}

func TestCustomKeysAndOptionalFlags(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, logotel.Options{
		TraceIDKey: "otel_trace",
		SpanIDKey:  "otel_span",
	})
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
		SpanID:  trace.SpanID{1, 0, 0, 0, 0, 0, 0, 1},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)

	if err := handler.Handle(ctx, slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if !capture.AssertAttr(t, sink, "otel_trace", spanContext.TraceID().String()) {
		t.FailNow()
	}
	if !capture.AssertAttr(t, sink, "otel_span", spanContext.SpanID().String()) {
		t.FailNow()
	}
	if capture.AssertAttr(&quietTestingT{}, sink, "trace_flags", "00") {
		t.Fatal("trace flags unexpectedly included")
	}
}

func TestDecoratorDelegatesContractAndPreservesDerivation(t *testing.T) {
	t.Parallel()

	want := errors.New("sink failed")
	sink := &stubHandler{enabled: true, err: want}
	handler := mustNew(t, sink, logotel.Options{})
	if !handler.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("Enabled() = false, want true")
	}
	derived := handler.
		WithAttrs([]slog.Attr{slog.String("service", "api")}).
		WithGroup("request")

	if err := derived.Handle(context.Background(), slog.NewRecord(time.Unix(1, 0), slog.LevelWarn, "message", 0)); !errors.Is(err, want) {
		t.Fatalf("Handle() error = %v, want %v", err, want)
	}
	if sink.attrs != 1 || sink.groups != 1 {
		t.Fatalf("derivation calls attrs=%d groups=%d, want one each", sink.attrs, sink.groups)
	}
}

func mustNew(t *testing.T, next slog.Handler, options logotel.Options) *logotel.Handler {
	t.Helper()
	handler, err := logotel.New(next, options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}

type quietTestingT struct{}

func (*quietTestingT) Helper()               {}
func (*quietTestingT) Errorf(string, ...any) {}

type stubHandler struct {
	enabled bool
	err     error
	attrs   int
	groups  int
}

func (handler *stubHandler) Enabled(context.Context, slog.Level) bool  { return handler.enabled }
func (handler *stubHandler) Handle(context.Context, slog.Record) error { return handler.err }
func (handler *stubHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handler.attrs += len(attrs)
	return handler
}
func (handler *stubHandler) WithGroup(string) slog.Handler {
	handler.groups++
	return handler
}
