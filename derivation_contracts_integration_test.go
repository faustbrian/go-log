package log_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	log "github.com/faustbrian/go-log/v2"
	"github.com/faustbrian/go-log/v2/handler/async"
	"github.com/faustbrian/go-log/v2/handler/capture"
	"github.com/faustbrian/go-log/v2/handler/redact"
	"github.com/faustbrian/go-log/v2/handler/sample"
	"github.com/faustbrian/go-log/v2/handler/stack"
	logotel "github.com/faustbrian/go-log/v2/otel"
)

func TestDecoratorDerivationPreservesRejectionAndEmptyNoOps(t *testing.T) {
	factories := map[string]func(*testing.T, *capture.Handler) slog.Handler{
		"capture": func(_ *testing.T, sink *capture.Handler) slog.Handler { return sink },
		"redact-default": func(t *testing.T, sink *capture.Handler) slog.Handler {
			handler, err := redact.New(sink, nil)
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"redact-trusted": func(t *testing.T, sink *capture.Handler) slog.Handler {
			handler, err := redact.New(sink, &redact.Options{PreserveTrustedAttributes: true})
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"sample": func(t *testing.T, sink *capture.Handler) slog.Handler {
			policy, err := sample.Every(1)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := sample.New(sink, policy)
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"stack": func(t *testing.T, sink *capture.Handler) slog.Handler {
			handler, err := stack.New(stack.Route{Handler: sink})
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"otel": func(t *testing.T, sink *capture.Handler) slog.Handler {
			handler, err := logotel.New(sink, logotel.Options{})
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"async": func(t *testing.T, sink *capture.Handler) slog.Handler {
			handler, err := async.New(sink, async.Options{Capacity: 1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := handler.Shutdown(ctx); err != nil {
					t.Error(err)
				}
			})
			return handler
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			sink := capture.New()
			handler := factory(t, sink)
			record := slog.NewRecord(time.Time{}, slog.LevelInfo, "fixed", 0)
			empty := handler.WithAttrs([]slog.Attr{slog.String("bound", "ordinary")}).WithAttrs(nil).WithGroup("")
			if !empty.Enabled(context.Background(), slog.LevelInfo) {
				t.Fatal("empty derivation disabled nominal delivery")
			}
			if err := empty.Handle(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			if lifecycle, ok := handler.(*async.Handler); ok {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := lifecycle.Flush(ctx); err != nil {
					t.Fatal(err)
				}
			}
			capture.AssertCount(t, sink, 1)
			if name != "redact-default" {
				capture.AssertAttr(t, sink, "bound", "ordinary")
			}
			sink.Reset()
			invalid := handler.WithAttrs(make([]slog.Attr, log.MaxRecordAttributes+1))
			derived := invalid.WithAttrs([]slog.Attr{slog.Int("field", 1)}).WithGroup("group").WithGroup("")
			if derived.Enabled(context.Background(), slog.LevelInfo) {
				t.Error("failed derivation re-enabled delivery")
			}
			if err := derived.Handle(context.Background(), record); !errors.Is(err, log.ErrRecordLimit) {
				t.Errorf("sticky error = %v", err)
			}
			capture.AssertCount(t, sink, 0)
		})
	}
}
