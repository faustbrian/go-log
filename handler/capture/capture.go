// Package capture provides an in-memory slog handler and lightweight test
// assertions. It is intended for tests, not as a production log sink.
package capture

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
)

// Option configures a capture Handler.
type Option func(*config)

type config struct {
	minLevel slog.Leveler
}

// WithLevel configures the inclusive minimum captured level. A nil level
// captures every record.
func WithLevel(level slog.Leveler) Option {
	return func(config *config) {
		config.minLevel = level
	}
}

type state struct {
	mu      sync.RWMutex
	records []slog.Record
}

// Handler retains cloned records in memory and is safe for concurrent use.
// Derived handlers share the same capture buffer while keeping independent
// attribute and group configuration.
type Handler struct {
	state    *state
	minLevel slog.Leveler
	attrs    []slog.Attr
	groups   []string
}

// New constructs an empty capture handler.
func New(options ...Option) *Handler {
	config := config{}
	for _, option := range options {
		option(&config)
	}

	return &Handler{state: &state{}, minLevel: config.minLevel}
}

// Enabled reports whether level meets the configured minimum.
func (handler *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return handler.minLevel == nil || level >= handler.minLevel.Level()
}

// Handle clones and retains record. It never returns a delivery error.
func (handler *Handler) Handle(_ context.Context, record slog.Record) error {
	attrs := cloneAttrs(handler.attrs)
	recordAttrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		recordAttrs = append(recordAttrs, attr)
		return true
	})
	attrs = append(attrs, wrapGroups(cloneAttrs(recordAttrs), handler.groups)...)

	cloned := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	cloned.AddAttrs(attrs...)
	handler.state.mu.Lock()
	handler.state.records = append(handler.state.records, cloned)
	handler.state.mu.Unlock()

	return nil
}

// WithAttrs returns a derived handler whose records include attrs.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	derived := handler.clone()
	derived.attrs = append(derived.attrs, wrapGroups(cloneAttrs(attrs), handler.groups)...)

	return derived
}

// WithGroup returns a derived handler that qualifies subsequent attributes
// with name. An empty name leaves the handler unchanged.
func (handler *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	derived := handler.clone()
	derived.groups = append(derived.groups, name)

	return derived
}

// Len returns the number of retained records.
func (handler *Handler) Len() int {
	handler.state.mu.RLock()
	defer handler.state.mu.RUnlock()

	return len(handler.state.records)
}

// Records returns an independent snapshot of retained records.
func (handler *Handler) Records() []slog.Record {
	handler.state.mu.RLock()
	defer handler.state.mu.RUnlock()
	records := make([]slog.Record, len(handler.state.records))
	for index, record := range handler.state.records {
		records[index] = cloneRecord(record)
	}

	return records
}

// Last returns an independent copy of the most recently retained record.
func (handler *Handler) Last() (slog.Record, bool) {
	handler.state.mu.RLock()
	defer handler.state.mu.RUnlock()
	if len(handler.state.records) == 0 {
		return slog.Record{}, false
	}

	return cloneRecord(handler.state.records[len(handler.state.records)-1]), true
}

// Reset removes every retained record without affecting derived handlers.
func (handler *Handler) Reset() {
	handler.state.mu.Lock()
	handler.state.records = nil
	handler.state.mu.Unlock()
}

func (handler *Handler) clone() *Handler {
	return &Handler{
		state:    handler.state,
		minLevel: handler.minLevel,
		attrs:    cloneAttrs(handler.attrs),
		groups:   append([]string(nil), handler.groups...),
	}
}

// TestingT is the subset of testing.TB used by capture assertions.
type TestingT interface {
	Helper()
	Errorf(format string, args ...any)
}

// AssertCount reports whether handler retained exactly want records.
func AssertCount(test TestingT, handler *Handler, want int) bool {
	test.Helper()
	got := handler.Len()
	if got == want {
		return true
	}
	test.Errorf("capture: record count = %d, want %d", got, want)

	return false
}

// AssertMessage reports whether at least one captured record has message.
func AssertMessage(test TestingT, handler *Handler, message string) bool {
	test.Helper()
	for _, record := range handler.Records() {
		if record.Message == message {
			return true
		}
	}
	test.Errorf("capture: no record has message %q", message)

	return false
}

// AssertAttr reports whether at least one captured record contains the
// dot-separated attribute path with a value deeply equal to want.
func AssertAttr(test TestingT, handler *Handler, path string, want any) bool {
	test.Helper()
	for _, record := range handler.Records() {
		if got, ok := recordAttr(record, strings.Split(path, ".")); ok && reflect.DeepEqual(got, want) {
			return true
		}
	}
	test.Errorf("capture: no record has attribute %q equal to %s", path, fmt.Sprint(want))

	return false
}

func recordAttr(record slog.Record, path []string) (any, bool) {
	var result any
	var found bool
	record.Attrs(func(attr slog.Attr) bool {
		result, found = nestedAttr(attr, path)
		return !found
	})

	return result, found
}

func nestedAttr(attr slog.Attr, path []string) (any, bool) {
	if attr.Key != path[0] {
		return nil, false
	}
	value := attr.Value.Resolve()
	if len(path) == 1 {
		return value.Any(), true
	}
	if value.Kind() != slog.KindGroup {
		return nil, false
	}
	for _, child := range value.Group() {
		if result, found := nestedAttr(child, path[1:]); found {
			return result, true
		}
	}

	return nil, false
}

func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	cloned := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Equal(slog.Attr{}) {
			continue
		}
		value := attr.Value.Resolve()
		if value.Kind() != slog.KindGroup {
			cloned = append(cloned, slog.Attr{Key: attr.Key, Value: value})
			continue
		}
		children := cloneAttrs(value.Group())
		if len(children) == 0 {
			continue
		}
		if attr.Key == "" {
			cloned = append(cloned, children...)
			continue
		}
		cloned = append(cloned, slog.Attr{Key: attr.Key, Value: slog.GroupValue(children...)})
	}

	return cloned
}

func cloneRecord(record slog.Record) slog.Record {
	cloned := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	cloned.AddAttrs(cloneAttrs(attrs)...)

	return cloned
}

func wrapGroups(attrs []slog.Attr, groups []string) []slog.Attr {
	if len(attrs) == 0 {
		return nil
	}
	wrapped := attrs
	for index := len(groups) - 1; index >= 0; index-- {
		wrapped = []slog.Attr{{Key: groups[index], Value: slog.GroupValue(wrapped...)}}
	}

	return wrapped
}
