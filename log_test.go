package log_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	log "github.com/faustbrian/golib/pkg/log"
)

func TestNewRejectsNilHandler(t *testing.T) {
	t.Parallel()

	logger, err := log.New(nil)

	if logger != nil {
		t.Fatalf("New(nil) logger = %v, want nil", logger)
	}
	if !errors.Is(err, log.ErrNilHandler) {
		t.Fatalf("New(nil) error = %v, want ErrNilHandler", err)
	}
}

func TestNewAppliesOptionsInOrder(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := log.New(
		slog.NewJSONHandler(&output, nil),
		log.WithAttrs(slog.String("service", "orders")),
		log.WithGroup("request"),
		log.WithAttrs(slog.String("id", "req-1")),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("accepted")

	got := output.String()
	for _, want := range []string{
		`"service":"orders"`,
		`"request":{"id":"req-1"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q does not contain %q", got, want)
		}
	}
}

func TestNewReturnsOptionError(t *testing.T) {
	t.Parallel()

	want := errors.New("invalid option")
	logger, err := log.New(slog.NewTextHandler(&bytes.Buffer{}, nil), func(slog.Handler) (slog.Handler, error) {
		return nil, want
	})

	if logger != nil {
		t.Fatalf("New() logger = %v, want nil", logger)
	}
	if !errors.Is(err, want) {
		t.Fatalf("New() error = %v, want %v", err, want)
	}
}

func TestNewRejectsOptionThatRemovesHandler(t *testing.T) {
	t.Parallel()

	logger, err := log.New(slog.NewTextHandler(&bytes.Buffer{}, nil), func(slog.Handler) (slog.Handler, error) {
		return nil, nil
	})

	if logger != nil {
		t.Fatalf("New() logger = %v, want nil", logger)
	}
	if !errors.Is(err, log.ErrNilHandler) {
		t.Fatalf("New() error = %v, want ErrNilHandler", err)
	}
}

func TestJSONAndTextPreserveStandardHandlers(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		newLogger func(*bytes.Buffer) *slog.Logger
		contains  string
	}{
		"json": {
			newLogger: func(output *bytes.Buffer) *slog.Logger {
				return log.JSON(output, nil)
			},
			contains: `"msg":"hello"`,
		},
		"text": {
			newLogger: func(output *bytes.Buffer) *slog.Logger {
				return log.Text(output, nil)
			},
			contains: `msg=hello`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			test.newLogger(&output).Info("hello")

			if got := output.String(); !strings.Contains(got, test.contains) {
				t.Fatalf("output %q does not contain %q", got, test.contains)
			}
		})
	}
}
