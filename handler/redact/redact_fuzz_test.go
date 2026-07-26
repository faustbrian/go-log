package redact_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/faustbrian/golib/pkg/log/handler/redact"
)

func FuzzNestedAttributes(fuzz *testing.F) {
	fuzz.Add([]byte("safe"), uint8(0), uint8(0))
	fuzz.Add([]byte{0xff, 0xfe, 0xfd}, uint8(8), uint8(1))
	fuzz.Add([]byte("nested.value"), uint8(16), uint8(2))
	fuzz.Add([]byte{}, uint8(4), uint8(3))

	fuzz.Fuzz(func(t *testing.T, input []byte, requestedDepth, valueKind uint8) {
		const secret = "TOP_SECRET_NEVER_EMIT"
		depth := int(requestedDepth % 24)
		var output bytes.Buffer
		next := slog.NewJSONHandler(&output, nil)
		handler, err := redact.New(next, &redact.Options{
			Rules: []redact.Rule{redact.Any(
				redact.Keys("secret", "token", "password"),
				redact.Paths("request.credentials.password"),
			)},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		ordinary := fuzzValue(input, valueKind)
		sensitive := &observedValuer{value: secret}
		attrs := []slog.Attr{
			slog.Any("ordinary", ordinary),
			slog.Any("secret", sensitive),
			slog.String("token", secret),
			slog.Group("request", slog.Group("credentials", slog.String("password", secret))),
			{Key: "zero", Value: slog.Value{}},
		}
		for index := 0; index < depth; index++ {
			name := string(input)
			if !utf8.ValidString(name) || name == "" {
				name = "group"
			}
			attrs = []slog.Attr{{Key: name, Value: slog.GroupValue(attrs...)}}
		}
		record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "fuzz", 0)
		record.AddAttrs(attrs...)

		if err := handler.Handle(context.Background(), record); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if sensitive.called {
			t.Fatal("sensitive LogValuer was evaluated")
		}
		if strings.Contains(output.String(), secret) {
			t.Fatalf("redacted output leaked secret: %q", output.String())
		}
	})
}

func FuzzRedactionRules(fuzz *testing.F) {
	fuzz.Add("token", "request.token", "value")
	fuzz.Add("PASSWORD", "account.password", "\xff\x00")
	fuzz.Add("", "", "")

	fuzz.Fuzz(func(t *testing.T, key, path, value string) {
		var output bytes.Buffer
		handler, err := redact.New(slog.NewTextHandler(&output, nil), &redact.Options{
			Rules: []redact.Rule{redact.Any(redact.Keys(key), redact.Paths(path), nil)},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "rules", 0)
		record.AddAttrs(
			slog.String(key, value),
			slog.Group("request", slog.String("token", value)),
			slog.Any("recursive", recursiveValuer{}),
			slog.Any("panicking", panicFuzzValuer{}),
		)
		if err := handler.Handle(context.Background(), record); err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
	})
}

func fuzzValue(input []byte, kind uint8) any {
	switch kind % 4 {
	case 0:
		return string(input)
	case 1:
		return append([]byte(nil), input...)
	case 2:
		return recursiveValuer{}
	default:
		return panicFuzzValuer{}
	}
}

type observedValuer struct {
	value  string
	called bool
}

func (valuer *observedValuer) LogValue() slog.Value {
	valuer.called = true
	return slog.StringValue(valuer.value)
}

type recursiveValuer struct{}

func (recursiveValuer) LogValue() slog.Value {
	return slog.AnyValue(recursiveValuer{})
}

type panicFuzzValuer struct{}

func (panicFuzzValuer) LogValue() slog.Value {
	panic("fuzz panic")
}
