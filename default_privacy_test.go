package log_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	log "github.com/faustbrian/go-log/v2"
	"github.com/faustbrian/go-log/v2/handler/redact"
)

func TestDefaultConstructorsOmitPrivateRecordAndBoundData(t *testing.T) {
	constructors := map[string]func(*bytes.Buffer) (*slog.Logger, error){
		"new": func(output *bytes.Buffer) (*slog.Logger, error) {
			return log.New(slog.NewJSONHandler(output, nil))
		},
		"json": func(output *bytes.Buffer) (*slog.Logger, error) { return log.JSON(output, nil), nil },
		"text": func(output *bytes.Buffer) (*slog.Logger, error) { return log.Text(output, nil), nil },
		"redact": func(output *bytes.Buffer) (*slog.Logger, error) {
			handler, err := redact.New(slog.NewJSONHandler(output, nil), nil)
			if err != nil {
				return nil, err
			}
			return slog.New(handler), nil
		},
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := construct(&output)
			if err != nil {
				t.Fatal(err)
			}
			resolved := 0
			logger = logger.WithGroup("bound-group-fixture").With(
				slog.Any("bound-key-fixture", privacyCountingValuer{resolved: &resolved}),
			)
			logger.LogAttrs(context.Background(), slog.LevelInfo, "message-fixture",
				slog.String("record-key-fixture", "value-fixture"),
				slog.Group("record-group-fixture", slog.Any("nested-key-fixture", privacyCountingValuer{resolved: &resolved})),
			)
			got := output.String()
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("missing fixed replacement: %q", got)
			}
			if strings.Contains(got, "fixture") {
				t.Errorf("default output retained private input: %q", got)
			}
			if resolved != 0 {
				t.Errorf("discarded LogValuer resolved %d times", resolved)
			}
			if !strings.Contains(got, "INFO") {
				t.Errorf("lost nominal level metadata: %q", got)
			}
		})
	}
}

func TestRootWithAttrsRejectsLimitBeforeDownstreamDerivation(t *testing.T) {
	attrs := make([]slog.Attr, log.MaxRecordAttributes+1)
	called := 0
	next := &privacyDerivationHandler{called: &called}
	handler, err := log.WithAttrs(attrs...)(next)
	if handler != nil || !errors.Is(err, log.ErrRecordLimit) {
		t.Errorf("oversized WithAttrs: handler present = %t, error = %v", handler != nil, err)
	}
	if called != 0 {
		t.Errorf("oversized attributes reached downstream %d times", called)
	}
}

func TestRootWithAttrsOwnsIndependentGroupDataPerInvocation(t *testing.T) {
	children := []slog.Attr{slog.String("original", "value")}
	option := log.WithAttrs(slog.Attr{Key: "group", Value: slog.GroupValue(children...)})
	called := 0
	next := &privacyDerivationHandler{called: &called, mutate: true}
	for range 2 {
		if _, err := option(next); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(next.observed, ",") != "original,original" {
		t.Errorf("per-invocation group ownership = %v", next.observed)
	}
	if children[0].Key != "original" {
		t.Errorf("caller group changed to %q", children[0].Key)
	}
}

func TestDefaultRootConstructorEnforcesCombinedBoundAndRecordBudget(t *testing.T) {
	attrs := make([]slog.Attr, log.MaxRecordAttributes)
	for index := range attrs {
		attrs[index] = slog.Int("field", index)
	}
	var output bytes.Buffer
	logger, err := log.New(slog.NewJSONHandler(&output, nil), log.WithAttrs(attrs...))
	if err != nil {
		t.Fatal(err)
	}
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "event", 0)
	if err := logger.Handler().Handle(context.Background(), record); err != nil {
		t.Fatalf("exact bound budget: %v", err)
	}
	before := output.Len()
	record.AddAttrs(slog.Int("excess", 1))
	if err := logger.Handler().Handle(context.Background(), record); !errors.Is(err, log.ErrRecordLimit) {
		t.Errorf("combined bound and record excess error = %v, want ErrRecordLimit", err)
	}
	if output.Len() != before {
		t.Error("rejected combined structure reached downstream")
	}
}

func TestTrustedConstructorsPreserveOnlyBoundedNominalData(t *testing.T) {
	constructors := map[string]func(*bytes.Buffer) (*slog.Logger, error){
		"new": func(output *bytes.Buffer) (*slog.Logger, error) {
			return log.TrustedNew(slog.NewJSONHandler(output, nil))
		},
		"json": func(output *bytes.Buffer) (*slog.Logger, error) { return log.TrustedJSON(output, nil), nil },
		"text": func(output *bytes.Buffer) (*slog.Logger, error) { return log.TrustedText(output, nil), nil },
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := construct(&output)
			if err != nil {
				t.Fatal(err)
			}
			logger = logger.WithGroup("group").With(slog.String("bound", "nominal"))
			logger.Info("event", slog.String("field", "value"))
			for _, want := range []string{"event", "group", "bound", "nominal", "field", "value"} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("trusted output missing %q", want)
				}
			}
			before := output.Len()
			record := slog.NewRecord(time.Time{}, slog.LevelInfo, strings.Repeat("x", redact.MaxTrustedMessageBytes+1), 0)
			if err := logger.Handler().Handle(context.Background(), record); !errors.Is(err, redact.ErrTrustedMessageTooLong) {
				t.Errorf("trusted oversized message error = %v", err)
			}
			if output.Len() != before {
				t.Error("oversized trusted message reached downstream")
			}
		})
	}
}

type privacyCountingValuer struct{ resolved *int }

func (value privacyCountingValuer) LogValue() slog.Value {
	*value.resolved++
	return slog.StringValue("resolved-fixture")
}

type privacyDerivationHandler struct {
	called   *int
	mutate   bool
	observed []string
}

func (*privacyDerivationHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (*privacyDerivationHandler) Handle(context.Context, slog.Record) error { return nil }
func (handler *privacyDerivationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	*handler.called++
	if handler.mutate {
		children := attrs[0].Value.Group()
		handler.observed = append(handler.observed, children[0].Key)
		children[0].Key = "mutated"
	}
	return handler
}
func (handler *privacyDerivationHandler) WithGroup(string) slog.Handler { return handler }
