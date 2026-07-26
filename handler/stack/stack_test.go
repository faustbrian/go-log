package stack_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/stack"
)

func TestNewRejectsInvalidRoutes(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		route stack.Route
		want  error
	}{
		"nil handler": {
			route: stack.Route{},
			want:  stack.ErrNilHandler,
		},
		"inverted range": {
			route: stack.Route{
				Handler:  &recordingHandler{},
				MinLevel: slog.LevelError,
				MaxLevel: slog.LevelDebug,
			},
			want: stack.ErrInvalidRange,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, err := stack.New(test.route)

			if handler != nil {
				t.Fatalf("New() handler = %v, want nil", handler)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEnabledUsesRouteBoundsAndDownstreamHandler(t *testing.T) {
	t.Parallel()

	info := &recordingHandler{enabled: true}
	errorOnly := &recordingHandler{enabled: true}
	disabled := &recordingHandler{enabled: false}
	handler := mustNew(t,
		stack.Route{Handler: info, MinLevel: slog.LevelInfo, MaxLevel: slog.LevelWarn},
		stack.Route{Handler: errorOnly, MinLevel: slog.LevelError},
		stack.Route{Handler: disabled},
	)

	tests := map[slog.Level]bool{
		slog.LevelDebug: false,
		slog.LevelInfo:  true,
		slog.LevelWarn:  true,
		slog.LevelError: true,
	}
	for level, want := range tests {
		if got := handler.Enabled(context.Background(), level); got != want {
			t.Errorf("Enabled(%v) = %v, want %v", level, got, want)
		}
	}

	if info.enabledCalls != 2 {
		t.Errorf("info Enabled calls = %d, want 2", info.enabledCalls)
	}
	if errorOnly.enabledCalls != 1 {
		t.Errorf("error Enabled calls = %d, want 1", errorOnly.enabledCalls)
	}
	if disabled.enabledCalls != 1 {
		t.Errorf("disabled Enabled calls = %d, want 1", disabled.enabledCalls)
	}
}

func TestHandleFansOutOnlyToEnabledRoutes(t *testing.T) {
	t.Parallel()

	first := &recordingHandler{enabled: true}
	second := &recordingHandler{enabled: false}
	third := &recordingHandler{enabled: true}
	handler := mustNew(t,
		stack.Route{Handler: first, MinLevel: slog.LevelInfo},
		stack.Route{Handler: second},
		stack.Route{Handler: third, MaxLevel: slog.LevelWarn},
	)
	record := slog.NewRecord(testTime(), slog.LevelInfo, "hello", 0)
	record.AddAttrs(slog.String("key", "value"))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if got := first.messages(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("first messages = %v, want [hello]", got)
	}
	if got := second.messages(); len(got) != 0 {
		t.Errorf("second messages = %v, want none", got)
	}
	if got := third.messages(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("third messages = %v, want [hello]", got)
	}
}

func TestHandleGivesEachSinkAnIndependentRecord(t *testing.T) {
	t.Parallel()

	mutator := &recordingHandler{
		enabled: true,
		mutate: func(record *slog.Record) {
			record.AddAttrs(slog.String("mutated", "yes"))
		},
	}
	observer := &recordingHandler{enabled: true}
	handler := mustNew(t,
		stack.Route{Handler: mutator},
		stack.Route{Handler: observer},
	)
	record := slog.NewRecord(testTime(), slog.LevelInfo, "hello", 0)
	record.AddAttrs(slog.String("original", "yes"))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if got := observer.attrsAt(0); got["mutated"] != "" {
		t.Fatalf("observer attrs = %v, unexpectedly contains mutation", got)
	}
	if got := observer.attrsAt(0)["original"]; got != "yes" {
		t.Fatalf("observer original = %q, want yes", got)
	}
}

func TestHandleGivesEachSinkIndependentNestedGroups(t *testing.T) {
	t.Parallel()

	mutator := &recordingHandler{
		enabled: true,
		mutate: func(record *slog.Record) {
			record.Attrs(func(attr slog.Attr) bool {
				if attr.Value.Kind() == slog.KindGroup {
					children := attr.Value.Group()
					children[0].Key = "mutated"
				}
				return true
			})
		},
	}
	observer := &recordingHandler{enabled: true}
	handler := mustNew(t,
		stack.Route{Handler: mutator},
		stack.Route{Handler: observer},
	)
	record := slog.NewRecord(testTime(), slog.LevelInfo, "hello", 0)
	record.AddAttrs(slog.Group("request", slog.String("id", "req-1")))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if got := observer.nestedKeyAt(0, "request"); got != "id" {
		t.Fatalf("observer nested key = %q, want id", got)
	}
}

func TestHandleJoinsErrorsWithoutStoppingFanout(t *testing.T) {
	t.Parallel()

	firstError := errors.New("first sink failed")
	secondError := errors.New("second sink failed")
	first := &recordingHandler{enabled: true, handleErr: firstError}
	success := &recordingHandler{enabled: true}
	second := &recordingHandler{enabled: true, handleErr: secondError}
	handler := mustNew(t,
		stack.Route{Handler: first},
		stack.Route{Handler: success},
		stack.Route{Handler: second},
	)
	record := slog.NewRecord(testTime(), slog.LevelInfo, "hello", 0)

	err := handler.Handle(context.Background(), record)

	if !errors.Is(err, firstError) || !errors.Is(err, secondError) {
		t.Fatalf("Handle() error = %v, want both sink errors", err)
	}
	if got := success.messages(); len(got) != 1 {
		t.Fatalf("successful sink messages = %v, want one", got)
	}
}

func TestDerivedHandlersAreImmutable(t *testing.T) {
	t.Parallel()

	sink := &recordingHandler{enabled: true}
	base := mustNew(t, stack.Route{Handler: sink})
	withAttrs := base.WithAttrs([]slog.Attr{slog.String("service", "api")})
	withGroup := withAttrs.WithGroup("request")

	for message, handler := range map[string]slog.Handler{
		"base":       base,
		"with-attrs": withAttrs,
		"with-group": withGroup,
	} {
		record := slog.NewRecord(testTime(), slog.LevelInfo, message, 0)
		record.AddAttrs(slog.String("id", message))
		if err := handler.Handle(context.Background(), record); err != nil {
			t.Fatalf("Handle(%q) error = %v", message, err)
		}
	}

	if got := sink.derivedCalls(); got != 3 {
		t.Fatalf("derived handler calls = %d, want 3", got)
	}
	if got := sink.derivations; len(got) != 2 || got[0] != "attr:service" || got[1] != "group:request" {
		t.Fatalf("derivations = %v", got)
	}
}

func TestWithAttrsGivesEachRouteAnOwnedSlice(t *testing.T) {
	t.Parallel()

	first := &ownedAttrsHandler{mutate: true}
	second := &ownedAttrsHandler{}
	handler := mustNew(t,
		stack.Route{Handler: first},
		stack.Route{Handler: second},
	)

	handler.WithAttrs([]slog.Attr{slog.String("service", "api")})

	if first.key != "service" {
		t.Fatalf("first key = %q, want service", first.key)
	}
	if second.key != "service" {
		t.Fatalf("second key = %q, want service", second.key)
	}
}

func TestWithAttrsGivesEachRouteOwnedNestedGroups(t *testing.T) {
	t.Parallel()

	first := &ownedAttrsHandler{mutateNested: true}
	second := &ownedAttrsHandler{}
	handler := mustNew(t,
		stack.Route{Handler: first},
		stack.Route{Handler: second},
	)

	handler.WithAttrs([]slog.Attr{slog.Group("request", slog.String("id", "req-1"))})

	if first.nestedKey != "id" {
		t.Fatalf("first nested key = %q, want id", first.nestedKey)
	}
	if second.nestedKey != "id" {
		t.Fatalf("second nested key = %q, want id", second.nestedKey)
	}
}

func TestEmptyStackIsDisabledAndHandlesNothing(t *testing.T) {
	t.Parallel()

	handler := mustNew(t)
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled() = true, want false")
	}
	if err := handler.Handle(context.Background(), slog.Record{}); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
}

func mustNew(t *testing.T, routes ...stack.Route) *stack.Handler {
	t.Helper()

	handler, err := stack.New(routes...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return handler
}

type recordingHandler struct {
	mu           sync.Mutex
	enabled      bool
	enabledCalls int
	records      []slog.Record
	handleErr    error
	mutate       func(*slog.Record)
	derivations  []string
}

type ownedAttrsHandler struct {
	key          string
	mutate       bool
	nestedKey    string
	mutateNested bool
}

func (*ownedAttrsHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (*ownedAttrsHandler) Handle(context.Context, slog.Record) error { return nil }
func (handler *ownedAttrsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handler.key = attrs[0].Key
	if attrs[0].Value.Kind() == slog.KindGroup {
		children := attrs[0].Value.Group()
		handler.nestedKey = children[0].Key
		if handler.mutateNested {
			children[0].Key = "mutated"
		}
	}
	if handler.mutate {
		attrs[0].Key = "mutated"
	}
	return handler
}
func (handler *ownedAttrsHandler) WithGroup(string) slog.Handler { return handler }

func (handler *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.enabledCalls++

	return handler.enabled
}

func (handler *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.mutate != nil {
		handler.mutate(&record)
	}
	handler.records = append(handler.records, record.Clone())

	return handler.handleErr
}

func (handler *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	for _, attr := range attrs {
		handler.derivations = append(handler.derivations, "attr:"+attr.Key)
	}

	return handler
}

func (handler *recordingHandler) WithGroup(name string) slog.Handler {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.derivations = append(handler.derivations, "group:"+name)

	return handler
}

func (handler *recordingHandler) messages() []string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	messages := make([]string, len(handler.records))
	for index, record := range handler.records {
		messages[index] = record.Message
	}

	return messages
}

func (handler *recordingHandler) attrsAt(index int) map[string]string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	attrs := make(map[string]string)
	handler.records[index].Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.String()
		return true
	})

	return attrs
}

func (handler *recordingHandler) nestedKeyAt(index int, group string) string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	var key string
	handler.records[index].Attrs(func(attr slog.Attr) bool {
		if attr.Key == group {
			key = attr.Value.Group()[0].Key
			return false
		}
		return true
	})

	return key
}

func (handler *recordingHandler) derivedCalls() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()

	return len(handler.records)
}

func testTime() time.Time {
	return time.Unix(1_700_000_000, 0)
}
