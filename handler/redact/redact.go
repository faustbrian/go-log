// Package redact provides structural attribute redaction for standard
// log/slog handlers.
package redact

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

// DefaultReplacement is used when Options does not provide a replacement.
const DefaultReplacement = "[REDACTED]"

// MaxTrustedMessageBytes bounds explicitly preserved fixed messages.
const MaxTrustedMessageBytes = 1024

var (
	// ErrNilHandler is returned when New is called without a downstream handler.
	ErrNilHandler = errors.New("redact: nil handler")
	// ErrTrustedMessageTooLong reports an explicitly preserved message that
	// exceeds MaxTrustedMessageBytes.
	ErrTrustedMessageTooLong = errors.New("redact: trusted message too long")
)

// Rule reports whether attr at its dot-separated structural path is
// sensitive. Rules receive the raw value so matching can occur before a
// LogValuer is evaluated. Each callback receives an independent bounded copy
// of group structure. Opaque non-group values retain caller-owned identity;
// rules must not mutate those objects and must return promptly.
type Rule func(path string, attr slog.Attr) bool

// Options configures structural redaction.
type Options struct {
	// Rules are evaluated in order until one matches. Nil rules are ignored.
	Rules []Rule
	// Replacement overrides DefaultReplacement when non-nil.
	Replacement *slog.Value
	// PreserveTrustedMessage preserves fixed application-controlled messages
	// up to MaxTrustedMessageBytes.
	// The secure default replaces the complete message because arbitrary text
	// cannot be reliably inspected for secrets.
	PreserveTrustedMessage bool
	// PreserveTrustedAttributes opts explicitly trusted attribute names, values,
	// and group identifiers into selective Rules processing. The secure default
	// omits all caller attributes and groups without resolving discarded values.
	PreserveTrustedAttributes bool
}

// Handler omits attributes/groups and replaces messages by default. Explicit
// trusted-attribute mode replaces matched values and preserves unmatched data.
// It is immutable and safe for concurrent use when the downstream handler is.
type Handler struct {
	next               slog.Handler
	rules              []Rule
	replacement        slog.Value
	groups             []string
	usage              slogrecord.Usage
	preserveMessage    bool
	preserveAttributes bool
	err                error
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

	return &Handler{
		next: next, rules: rules, replacement: replacement,
		preserveMessage:    options != nil && options.PreserveTrustedMessage,
		preserveAttributes: options != nil && options.PreserveTrustedAttributes,
	}, nil
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

// Any combines rules with logical OR. Nil rules are ignored. Each callback
// receives independent bounded group data. An over-limit standalone attribute
// matches fail-closed without invoking callbacks.
func Any(rules ...Rule) Rule {
	cloned := append([]Rule(nil), rules...)

	return func(path string, attr slog.Attr) bool {
		for _, rule := range cloned {
			if rule == nil {
				continue
			}
			attributes := 0
			owned, err := cloneRuleAttr(attr, 1, &attributes)
			if err != nil || rule(path, owned) {
				return true
			}
		}

		return false
	}
}

// Enabled delegates level decisions to the downstream handler.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.err == nil && handler.next.Enabled(ctx, level)
}

// Handle redacts record attributes and delegates delivery downstream.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	if handler.err != nil {
		return handler.err
	}
	attributes, depth, err := handler.usage.Start(record.NumAttrs())
	if err != nil {
		return err
	}
	message := DefaultReplacement
	if handler.preserveMessage {
		if len(record.Message) > MaxTrustedMessageBytes {
			return ErrTrustedMessageTooLong
		}
		message = record.Message
	}
	redacted := slog.NewRecord(record.Time, record.Level, message, record.PC)
	if !handler.preserveAttributes {
		if _, err := slogrecord.CloneRawWithUsage(record, handler.usage); err != nil {
			return err
		}
		return handler.next.Handle(ctx, redacted)
	}
	var result error
	record.Attrs(func(attr slog.Attr) bool {
		transformed, err := handler.transform(attr, handler.groups, depth, &attributes)
		if err != nil {
			result = err
			return false
		}
		redacted.AddAttrs(transformed)
		return true
	})
	if result != nil {
		return result
	}

	return handler.next.Handle(ctx, redacted)
}

// WithAttrs returns a derived handler that redacts bound attrs using the
// current structural group path.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if handler.err != nil {
		return handler.withError(handler.err)
	}
	if len(attrs) == 0 {
		return handler
	}
	if !handler.preserveAttributes {
		_, usage, err := slogrecord.CloneRawAttrsWithUsage(attrs, handler.usage)
		if err != nil {
			return handler.withError(err)
		}
		derived := handler.withError(nil)
		derived.usage = usage
		return derived
	}
	attributes, depth, err := handler.usage.Start(len(attrs))
	if err != nil {
		return handler.withError(err)
	}
	redacted := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		transformed, err := handler.transform(attr, handler.groups, depth, &attributes)
		if err != nil {
			return handler.withError(err)
		}
		redacted[index] = transformed
	}
	usage := handler.usage
	usage.Attributes = attributes

	return &Handler{
		next:               handler.next.WithAttrs(redacted),
		rules:              handler.rules,
		replacement:        handler.replacement,
		groups:             append([]string(nil), handler.groups...),
		usage:              usage,
		preserveMessage:    handler.preserveMessage,
		preserveAttributes: handler.preserveAttributes,
	}
}

// WithGroup returns a derived handler that includes name in structural paths.
// An empty name leaves the handler unchanged.
func (handler *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	if handler.err != nil {
		return handler.withError(handler.err)
	}
	usage, err := handler.usage.WithGroup(name)
	if err != nil {
		return handler.withError(err)
	}
	if !handler.preserveAttributes {
		derived := handler.withError(nil)
		derived.usage = usage
		return derived
	}

	return &Handler{
		next:               handler.next.WithGroup(name),
		rules:              handler.rules,
		replacement:        handler.replacement,
		groups:             append(append([]string(nil), handler.groups...), name),
		usage:              usage,
		preserveMessage:    handler.preserveMessage,
		preserveAttributes: handler.preserveAttributes,
	}
}

func (handler *Handler) withError(err error) *Handler {
	return &Handler{
		next: handler.next, rules: handler.rules, replacement: handler.replacement,
		groups: append([]string(nil), handler.groups...), usage: handler.usage,
		preserveMessage: handler.preserveMessage, err: err,
		preserveAttributes: handler.preserveAttributes,
	}
}

func (handler *Handler) transform(
	attr slog.Attr,
	parent []string,
	depth int,
	attributes *int,
) (slog.Attr, error) {
	if depth > slogrecord.MaxRecordDepth || *attributes >= slogrecord.MaxRecordAttributes {
		return slog.Attr{}, slogrecord.ErrRecordLimit
	}
	*attributes++
	path := joinPath(parent, attr.Key)
	for _, rule := range handler.rules {
		if rule == nil {
			continue
		}
		ruleAttributes := 0
		ruleAttr, err := cloneRuleAttr(attr, depth, &ruleAttributes)
		if err != nil {
			return slog.Attr{}, err
		}
		if rule(path, ruleAttr) {
			return slog.Attr{Key: attr.Key, Value: handler.replacement}, nil
		}
	}

	value := attr.Value.Resolve()
	if value.Kind() != slog.KindGroup {
		return slog.Attr{Key: attr.Key, Value: value}, nil
	}
	children := value.Group()
	if len(children) > slogrecord.MaxRecordAttributes-*attributes {
		return slog.Attr{}, slogrecord.ErrRecordLimit
	}
	redacted := make([]slog.Attr, len(children))
	childParent := parent
	if attr.Key != "" {
		childParent = append(append([]string(nil), parent...), attr.Key)
	}
	for index, child := range children {
		transformed, err := handler.transform(child, childParent, depth+1, attributes)
		if err != nil {
			return slog.Attr{}, err
		}
		redacted[index] = transformed
	}

	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redacted...)}, nil
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

func cloneRuleAttr(attr slog.Attr, depth int, attributes *int) (slog.Attr, error) {
	if depth > slogrecord.MaxRecordDepth || *attributes >= slogrecord.MaxRecordAttributes {
		return slog.Attr{}, slogrecord.ErrRecordLimit
	}
	*attributes++
	if attr.Value.Kind() != slog.KindGroup {
		return attr, nil
	}
	children := attr.Value.Group()
	if len(children) > slogrecord.MaxRecordAttributes-*attributes {
		return slog.Attr{}, slogrecord.ErrRecordLimit
	}
	cloned := make([]slog.Attr, len(children))
	for index, child := range children {
		owned, err := cloneRuleAttr(child, depth+1, attributes)
		if err != nil {
			return slog.Attr{}, err
		}
		cloned[index] = owned
	}

	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(cloned...)}, nil
}
