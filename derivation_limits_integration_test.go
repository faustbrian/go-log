package log_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	logpkg "github.com/faustbrian/go-log/v2"
	"github.com/faustbrian/go-log/v2/handler/async"
	"github.com/faustbrian/go-log/v2/handler/capture"
	"github.com/faustbrian/go-log/v2/handler/redact"
	"github.com/faustbrian/go-log/v2/handler/sample"
	"github.com/faustbrian/go-log/v2/handler/stack"
	logotel "github.com/faustbrian/go-log/v2/otel"
)

func TestHandlerDerivationChainsRemainStructurallyBounded(t *testing.T) {
	t.Parallel()

	factories := map[string]func(*testing.T) slog.Handler{
		"async": func(t *testing.T) slog.Handler {
			handler, err := async.New(discardHandler(), async.Options{Capacity: 1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := handler.Shutdown(ctx); err != nil {
					t.Errorf("Shutdown() error = %v", err)
				}
			})
			return handler
		},
		"capture": func(*testing.T) slog.Handler { return capture.New() },
		"redact": func(t *testing.T) slog.Handler {
			handler, err := redact.New(discardHandler(), nil)
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"sample": func(t *testing.T) slog.Handler {
			policy, err := sample.Every(1)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := sample.New(discardHandler(), policy)
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"stack": func(t *testing.T) slog.Handler {
			handler, err := stack.New(stack.Route{Handler: discardHandler()})
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
		"otel": func(t *testing.T) slog.Handler {
			handler, err := logotel.New(discardHandler(), logotel.Options{})
			if err != nil {
				t.Fatal(err)
			}
			return handler
		},
	}

	for name, factory := range factories {
		t.Run(name+" attrs", func(t *testing.T) {
			handler := factory(t)
			for range 2 {
				handler = handler.WithAttrs(repeatedAttrs(600))
			}
			handler = handler.WithAttrs([]slog.Attr{slog.String("safe", "value")})
			assertRecordLimit(t, handler)
		})

		t.Run(name+" groups", func(t *testing.T) {
			handler := factory(t)
			for range logpkg.MaxRecordDepth + 1 {
				handler = handler.WithGroup("nested")
			}
			handler = handler.WithAttrs([]slog.Attr{slog.String("safe", "value")})
			assertRecordLimit(t, handler)
		})

		t.Run(name+" effective record", func(t *testing.T) {
			handler := factory(t).WithAttrs(repeatedAttrs(logpkg.MaxRecordAttributes))
			record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
			record.AddAttrs(slog.String("excess", "value"))
			if err := handler.Handle(context.Background(), record); !errors.Is(err, logpkg.ErrRecordLimit) {
				t.Fatalf("Handle() error = %v, want ErrRecordLimit", err)
			}
		})
	}
}

func discardHandler() slog.Handler {
	return slog.NewTextHandler(io.Discard, nil)
}

func repeatedAttrs(count int) []slog.Attr {
	attrs := make([]slog.Attr, count)
	for index := range attrs {
		attrs[index] = slog.Int("value", index)
	}
	return attrs
}

func assertRecordLimit(t *testing.T, handler slog.Handler) {
	t.Helper()
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled() = true after structural limit failure")
	}
	if err := handler.Handle(context.Background(), slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)); !errors.Is(err, logpkg.ErrRecordLimit) {
		t.Fatalf("Handle() error = %v, want ErrRecordLimit", err)
	}
}
