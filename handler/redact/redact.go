// Package redact provides structural attribute redaction for standard
// log/slog handlers.
package redact

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

// DefaultReplacement is used when Options does not provide a replacement.
const DefaultReplacement = "[REDACTED]"

// ErrNilHandler is returned when New is called without a downstream handler.
var ErrNilHandler = errors.New("redact: nil handler")

// Rule reports whether attr at its dot-separated structural path is
// sensitive. Rules receive the raw value so matching can occur before a
// LogValuer is evaluated.
type Rule func(path string, attr slog.Attr) bool

// Options configures structural redaction.
type Options struct {
	// Rules are evaluated in order until one matches. Nil rules are ignored.
	Rules []Rule
	// Replacement overrides DefaultReplacement when non-nil.
	Replacement *slog.Value
}

// Handler decorates a standard handler by replacing matched attribute values.
// It is immutable and safe for concurrent use when the downstream handler is.
type Handler struct {
	next        slog.Handler
	rules       []Rule
	replacement slog.Value
	groups      []string
}

// New constructs a structural redaction decorator.
func New(next slog.Handler, options *Options) (*Handler, error) {
	if next == nil {
		return nil, ErrNilHandler
	}
	replacement := slog.StringValue(DefaultReplacement)
	var rules []Rule
	if options != nil {
		rules = append([]Rule(nil), options.Rules...)
		if options.Replacement != nil {
			replacement = *options.Replacement
		}
	}

	return &Handler{next: next, rules: rules, replacement: replacement}, nil
}

// Keys returns a case-insensitive rule matching attribute keys at any depth.
func Keys(keys ...string) Rule {
	cloned := append([]string(nil), keys...)

	return func(_ string, attr slog.Attr) bool {
		for _, key := range cloned {
			if strings.EqualFold(attr.Key, key) {
				return true
			}
		}

		return false
	}
}

// Paths returns a case-insensitive rule matching exact dot-separated paths.
func Paths(paths ...string) Rule {
	cloned := append([]string(nil), paths...)

	return func(path string, _ slog.Attr) bool {
		for _, candidate := range cloned {
			if strings.EqualFold(path, candidate) {
				return true
			}
		}

		return false
	}
}

// Any combines rules with logical OR. Nil rules are ignored.
func Any(rules ...Rule) Rule {
	cloned := append([]Rule(nil), rules...)

	return func(path string, attr slog.Attr) bool {
		for _, rule := range cloned {
			if rule != nil && rule(path, attr) {
				return true
			}
		}

		return false
	}
}

// Enabled delegates level decisions to the downstream handler.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

// Handle redacts record attributes and delegates delivery downstream.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		redacted.AddAttrs(handler.transform(attr, handler.groups))
		return true
	})

	return handler.next.Handle(ctx, redacted)
}

// WithAttrs returns a derived handler that redacts bound attrs using the
// current structural group path.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		redacted[index] = handler.transform(attr, handler.groups)
	}

	return &Handler{
		next:        handler.next.WithAttrs(redacted),
		rules:       handler.rules,
		replacement: handler.replacement,
		groups:      append([]string(nil), handler.groups...),
	}
}

// WithGroup returns a derived handler that includes name in structural paths.
// An empty name leaves the handler unchanged.
func (handler *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}

	return &Handler{
		next:        handler.next.WithGroup(name),
		rules:       handler.rules,
		replacement: handler.replacement,
		groups:      append(append([]string(nil), handler.groups...), name),
	}
}

func (handler *Handler) transform(attr slog.Attr, parent []string) slog.Attr {
	path := joinPath(parent, attr.Key)
	for _, rule := range handler.rules {
		if rule != nil && rule(path, cloneRuleAttr(attr)) {
			return slog.Attr{Key: attr.Key, Value: handler.replacement}
		}
	}

	value := attr.Value.Resolve()
	if value.Kind() != slog.KindGroup {
		return slog.Attr{Key: attr.Key, Value: value}
	}
	children := value.Group()
	redacted := make([]slog.Attr, len(children))
	childParent := parent
	if attr.Key != "" {
		childParent = append(append([]string(nil), parent...), attr.Key)
	}
	for index, child := range children {
		redacted[index] = handler.transform(child, childParent)
	}

	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redacted...)}
}

func joinPath(parent []string, key string) string {
	if len(parent) == 0 {
		return key
	}
	if key == "" {
		return strings.Join(parent, ".")
	}

	return strings.Join(parent, ".") + "." + key
}

func cloneRuleAttr(attr slog.Attr) slog.Attr {
	if attr.Value.Kind() != slog.KindGroup {
		return attr
	}
	children := attr.Value.Group()
	cloned := make([]slog.Attr, len(children))
	for index, child := range children {
		cloned[index] = cloneRuleAttr(child)
	}

	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(cloned...)}
}
