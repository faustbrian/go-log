package otel_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	log "github.com/faustbrian/go-log/v2"
	"github.com/faustbrian/go-log/v2/handler/capture"
	logotel "github.com/faustbrian/go-log/v2/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestOversizedRecordRejectedBeforeCorrelationCopy(t *testing.T) {
	sink := capture.New()
	handler := mustNew(t, sink, logotel.Options{})
	ctx := boundedSpanContext()
	record := correlationRecord(log.MaxRecordAttributes + 1)
	var got error
	allocations := testing.AllocsPerRun(1, func() { got = handler.Handle(ctx, record) })
	if !errors.Is(got, log.ErrRecordLimit) {
		t.Fatalf("Handle error = %v, want ErrRecordLimit", got)
	}
	if allocations != 0 {
		t.Fatalf("rejected preconstructed record allocated %v times, want zero before correlation copying", allocations)
	}
	capture.AssertCount(t, sink, 0)
}

func TestCorrelationInclusiveAttributeBudget(t *testing.T) {
	for _, flags := range []bool{false, true} {
		t.Run(map[bool]string{false: "two", true: "three"}[flags], func(t *testing.T) {
			sink := capture.New()
			handler := mustNew(t, sink, logotel.Options{IncludeTraceFlags: flags})
			added := 2
			if flags {
				added++
			}
			if err := handler.Handle(boundedSpanContext(), correlationRecord(log.MaxRecordAttributes-added)); err != nil {
				t.Fatalf("exact total limit rejected: %v", err)
			}
			if err := handler.Handle(boundedSpanContext(), correlationRecord(log.MaxRecordAttributes-added+1)); !errors.Is(err, log.ErrRecordLimit) {
				t.Fatalf("limit+1 error = %v, want ErrRecordLimit", err)
			}
			capture.AssertCount(t, sink, 1)
		})
	}
}

func boundedSpanContext() context.Context {
	span := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	})
	return trace.ContextWithSpanContext(context.Background(), span)
}

func correlationRecord(count int) slog.Record {
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "fixed", 0)
	for range count {
		record.AddAttrs(slog.Int("field", 1))
	}
	return record
}
