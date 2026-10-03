package otel

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/faustbrian/go-log/v2/handler/capture"
	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

func TestOrdinaryUncorrelatedGroupRespectsRemainingOwnedBudget(t *testing.T) {
	sink := capture.New()
	handler := &Handler{next: sink, usage: slogrecord.Usage{Attributes: slogrecord.MaxRecordAttributes - 1}}
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "group", 0)
	record.AddAttrs(slog.Group("ordinary", slog.String("value", "kept")))
	if err := handler.Handle(context.Background(), record); !errors.Is(err, slogrecord.ErrRecordLimit) {
		t.Fatalf("group beyond remaining owned budget = %v", err)
	}
	capture.AssertCount(t, sink, 0)
	flat := slog.NewRecord(time.Time{}, slog.LevelInfo, "flat", 0)
	flat.AddAttrs(slog.String("value", "kept"))
	if err := handler.Handle(context.Background(), flat); err != nil {
		t.Fatalf("single attribute at remaining owned budget = %v", err)
	}
	capture.AssertCount(t, sink, 1)
}
