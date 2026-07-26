package redact_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/capture"
	"github.com/faustbrian/golib/pkg/log/handler/redact"
)

func TestNewRejectsNilHandler(t *testing.T) {
	t.Parallel()

	handler, err := redact.New(nil, nil)

	if handler != nil {
		t.Fatalf("New(nil) handler = %v, want nil", handler)
	}
	if !errors.Is(err, redact.ErrNilHandler) {
		t.Fatalf("New(nil) error = %v, want ErrNilHandler", err)
	}
}

func TestKeysRedactsNestedDuplicateAndTypedValues(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, &redact.Options{
		Rules: []redact.Rule{redact.Keys(
			"password", "authorization", "url", "error", "headers", "credential",
		)},
	})
	requestURL, err := url.Parse("https://user:secret@example.com/private?token=secret")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "request", 0)
	record.AddAttrs(
		slog.String("password", "first"),
		slog.Group("account",
			slog.String("Password", "second"),
			slog.Any("url", requestURL),
			slog.Any("error", errors.New("secret failure")),
			slog.Any("headers", http.Header{"Authorization": {"Bearer secret"}}),
			slog.Any("credential", credential{Token: "secret"}),
		),
		slog.String("password", "third"),
		slog.String("safe", "visible"),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	captured, _ := sink.Last()
	for _, path := range []string{
		"password", "account.Password", "account.url", "account.error",
		"account.headers", "account.credential",
	} {
		values := attrValues(captured, path)
		if len(values) == 0 {
			t.Errorf("%s values are missing", path)
		}
		for _, value := range values {
			if value != redact.DefaultReplacement {
				t.Errorf("%s value = %v, want %q", path, value, redact.DefaultReplacement)
			}
		}
	}
	if got := len(attrValues(captured, "password")); got != 2 {
		t.Fatalf("password value count = %d, want 2", got)
	}
	if got := attrValues(captured, "safe"); len(got) != 1 || got[0] != "visible" {
		t.Fatalf("safe values = %v, want [visible]", got)
	}
}

func TestPathsOnlyRedactsExactStructuralLocation(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	replacement := slog.StringValue("hidden")
	handler := mustNew(t, sink, &redact.Options{
		Rules:       []redact.Rule{redact.Paths("request.token")},
		Replacement: &replacement,
	})
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "request", 0)
	record.AddAttrs(
		slog.String("token", "root-visible"),
		slog.Group("request",
			slog.String("token", "nested-secret"),
			slog.Group("", slog.String("token", "inline-secret")),
		),
		slog.Group("other", slog.String("token", "other-visible")),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	captured, _ := sink.Last()
	wants := map[string][]any{
		"token":         {"root-visible"},
		"request.token": {"hidden", "hidden"},
		"other.token":   {"other-visible"},
	}
	for path, want := range wants {
		if got := attrValues(captured, path); !reflect.DeepEqual(got, want) {
			t.Errorf("%s values = %v, want %v", path, got, want)
		}
	}
}

func TestAnyCombinesRulesAndIgnoresNilRules(t *testing.T) {
	t.Parallel()

	rule := redact.Any(nil, redact.Keys("token"), redact.Paths("request.password"))
	tests := map[string]struct {
		path string
		attr slog.Attr
		want bool
	}{
		"key":       {path: "request.token", attr: slog.String("token", "x"), want: true},
		"path":      {path: "request.password", attr: slog.String("password", "x"), want: true},
		"safe":      {path: "request.id", attr: slog.String("id", "x"), want: false},
		"wrong key": {path: "token", attr: slog.String("other", "x"), want: false},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := rule(test.path, test.attr); got != test.want {
				t.Fatalf("rule(%q, %v) = %v, want %v", test.path, test.attr, got, test.want)
			}
		})
	}
}

func TestRuleMutationCannotBypassLaterRedaction(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	mutatingRule := redact.Rule(func(_ string, attr slog.Attr) bool {
		if attr.Value.Kind() == slog.KindGroup {
			attr.Value.Group()[0].Key = "mutated"
		}
		return false
	})
	handler := mustNew(t, sink, &redact.Options{
		Rules: []redact.Rule{mutatingRule, redact.Keys("secret")},
	})
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.Group("request", slog.String("secret", "sensitive")))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if !capture.AssertAttr(t, sink, "request.secret", redact.DefaultReplacement) {
		t.FailNow()
	}
}

func TestSensitiveLogValuerIsNotEvaluated(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, &redact.Options{Rules: []redact.Rule{redact.Keys("secret")}})
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "valuer", 0)
	record.AddAttrs(
		slog.Any("secret", panicValuer{}),
		slog.Any("ordinary", panicValuer{}),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	captured, _ := sink.Last()
	if got := attrValues(captured, "secret"); len(got) != 1 || got[0] != redact.DefaultReplacement {
		t.Fatalf("secret = %v, want redacted", got)
	}
	ordinary := attrValues(captured, "ordinary")
	if len(ordinary) != 1 || !strings.Contains(ordinary[0].(error).Error(), "LogValue panicked") {
		t.Fatalf("ordinary = %v, want safe LogValuer error", ordinary)
	}
}

func TestWithAttrsAndWithGroupPreserveStructuralPaths(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	base := mustNew(t, sink, &redact.Options{Rules: []redact.Rule{redact.Paths("request.token")}})
	derived := base.
		WithAttrs([]slog.Attr{slog.String("root", "visible")}).
		WithGroup("request").
		WithAttrs([]slog.Attr{slog.String("token", "bound-secret")}).
		WithGroup("")
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "derived", 0)
	record.AddAttrs(slog.String("token", "record-secret"))

	if err := derived.Handle(context.Background(), record); err != nil {
		t.Fatalf("derived Handle() error = %v", err)
	}
	baseRecord := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "base", 0)
	baseRecord.AddAttrs(slog.String("token", "base-visible"))
	if err := base.Handle(context.Background(), baseRecord); err != nil {
		t.Fatalf("base Handle() error = %v", err)
	}

	records := sink.Records()
	if got := attrValues(records[0], "request.token"); len(got) != 2 ||
		got[0] != redact.DefaultReplacement || got[1] != redact.DefaultReplacement {
		t.Fatalf("derived request.token = %v, want two redactions", got)
	}
	if got := attrValues(records[0], "root"); len(got) != 1 || got[0] != "visible" {
		t.Fatalf("derived root = %v, want visible", got)
	}
	if got := attrValues(records[1], "token"); len(got) != 1 || got[0] != "base-visible" {
		t.Fatalf("base token = %v, want base-visible", got)
	}
}

func TestDecoratorDelegatesEnabledAndHandleErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("sink failed")
	sink := &stubHandler{enabled: true, err: want}
	handler := mustNew(t, sink, &redact.Options{})
	if !handler.Enabled(context.Background(), slog.LevelWarn) {
		t.Fatal("Enabled() = false, want true")
	}
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelWarn, "message", 0)

	if err := handler.Handle(context.Background(), record); !errors.Is(err, want) {
		t.Fatalf("Handle() error = %v, want %v", err, want)
	}
}

func mustNew(t *testing.T, next slog.Handler, options *redact.Options) *redact.Handler {
	t.Helper()

	handler, err := redact.New(next, options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return handler
}

type credential struct {
	Token string
}

type panicValuer struct{}

func (panicValuer) LogValue() slog.Value {
	panic("secret panic")
}

type stubHandler struct {
	enabled bool
	err     error
}

func (handler *stubHandler) Enabled(context.Context, slog.Level) bool  { return handler.enabled }
func (handler *stubHandler) Handle(context.Context, slog.Record) error { return handler.err }
func (handler *stubHandler) WithAttrs([]slog.Attr) slog.Handler        { return handler }
func (handler *stubHandler) WithGroup(string) slog.Handler             { return handler }

func attrValues(record slog.Record, path string) []any {
	parts := strings.Split(path, ".")
	var values []any
	record.Attrs(func(attr slog.Attr) bool {
		values = append(values, findValues(attr, parts)...)
		return true
	})

	return values
}

func findValues(attr slog.Attr, path []string) []any {
	value := attr.Value.Resolve()
	if attr.Key == "" && value.Kind() == slog.KindGroup {
		var values []any
		for _, child := range value.Group() {
			values = append(values, findValues(child, path)...)
		}
		return values
	}
	if attr.Key != path[0] {
		return nil
	}
	if len(path) == 1 {
		return []any{value.Any()}
	}
	if value.Kind() != slog.KindGroup {
		return nil
	}
	var values []any
	for _, child := range value.Group() {
		values = append(values, findValues(child, path[1:])...)
	}

	return values
}
