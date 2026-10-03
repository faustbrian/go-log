package redact_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	log "github.com/faustbrian/go-log/v2"
	"github.com/faustbrian/go-log/v2/handler/capture"
	"github.com/faustbrian/go-log/v2/handler/redact"
)

func TestSelectiveRedactionRejectsStructuralExcessWithoutPartialOutput(t *testing.T) {
	wide := make([]slog.Attr, log.MaxRecordAttributes)
	for index := range wide {
		wide[index] = slog.Int("field", index)
	}
	deep := slog.String("leaf", "ordinary")
	for range log.MaxRecordDepth {
		deep = slog.Group("group", deep)
	}
	for _, rules := range []bool{false, true} {
		for _, attr := range []slog.Attr{{Key: "wide", Value: slog.GroupValue(wide...)}, deep} {
			sink := capture.New()
			options := &redact.Options{PreserveTrustedAttributes: true}
			if rules {
				options.Rules = []redact.Rule{nil, redact.Keys("other")}
			}
			handler, err := redact.New(sink, options)
			if err != nil {
				t.Fatal(err)
			}
			record := slog.NewRecord(time.Time{}, slog.LevelInfo, "fixed", 0)
			record.AddAttrs(slog.String("nominal", "ordinary"), attr)
			if err := handler.Handle(context.Background(), record); !errors.Is(err, log.ErrRecordLimit) {
				t.Errorf("record structural error = %v", err)
			}
			capture.AssertCount(t, sink, 0)
			bound := handler.WithAttrs([]slog.Attr{attr})
			if err := bound.Handle(context.Background(), slog.NewRecord(time.Time{}, slog.LevelInfo, "fixed", 0)); !errors.Is(err, log.ErrRecordLimit) {
				t.Errorf("bound structural error = %v", err)
			}
			capture.AssertCount(t, sink, 0)
		}
	}
}
