// Package otel correlates slog records with the standard OpenTelemetry span
// context produced by telemetry. It does not initialize providers, mutate
// OpenTelemetry globals, export signals, or own SDK lifecycle. New decorates a
// caller-owned sink using the OpenTelemetry trace API v1.47.0 dependency; no
// Collector transport, cgo or telemetry-runtime dependency is introduced.
package otel

import (
	"context"
	"errors"
	"log/slog"

	"github.com/faustbrian/go-log/v2/internal/slogrecord"
	"go.opentelemetry.io/otel/trace"
)

// ErrNilHandler is returned when New receives no downstream handler.
var ErrNilHandler = errors.New("otel: nil handler")

// Options configures correlation attribute names.
type Options struct {
	// TraceIDKey defaults to trace_id.
	TraceIDKey string
	// SpanIDKey defaults to span_id.
	SpanIDKey string
	// TraceFlagsKey defaults to trace_flags.
	TraceFlagsKey string
	// IncludeTraceFlags includes the W3C trace flags byte as two hex digits.
	IncludeTraceFlags bool
}

// Handler decorates a standard slog handler with trace/span correlation. Use
// New; the zero value is unusable. Concurrent use depends on borrowed downstream
// handlers/values. No redaction, callback timeout or close propagation is added.
// Over-limit WithAttrs/WithGroup returns a disabled derived handler whose
// Handle keeps returning ErrRecordLimit; empty derivation does not clear it.
type Handler struct {
	next    slog.Handler
	options Options
	usage   slogrecord.Usage
	err     error
}

// New copies options and retains next without initializing OpenTelemetry.
// Nil next returns ErrNilHandler; typed-nil collaborators remain caller-invalid.
// Empty keys take defaults, but other spelling/duplicate names are not normalized.
func New(next slog.Handler, options Options) (*Handler, error) {
	if next == nil {
		return nil, ErrNilHandler
	}
	if options.TraceIDKey == "" {
		options.TraceIDKey = "trace_id"
	}
	if options.SpanIDKey == "" {
		options.SpanIDKey = "span_id"
	}
	if options.TraceFlagsKey == "" {
		options.TraceFlagsKey = "trace_flags"
	}

	return &Handler{next: next, options: options}, nil
}

// Enabled delegates level decisions to the downstream handler.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.err == nil && handler.next.Enabled(ctx, level)
}

// Handle reads a non-nil context and prepends IDs for any valid span context,
// local/remote and sampled/unsampled. Flags are the complete trace flags byte,
// not only its sampled bit. Existing same-name attrs are retained (not replaced).
// Correlation nodes count against retained-plus-record limits; structural
// rejection emits nothing downstream. Raw groups are copied without resolving
// LogValuer values; opaque objects remain shared. Sink errors propagate.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	if handler.err != nil {
		return handler.err
	}
	if _, _, err := handler.usage.Start(record.NumAttrs()); err != nil {
		return err
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		owned, err := slogrecord.CloneRawWithUsage(record, handler.usage)
		if err != nil {
			return err
		}
		return handler.next.Handle(ctx, owned)
	}
	correlated := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	correlated.AddAttrs(
		slog.String(handler.options.TraceIDKey, spanContext.TraceID().String()),
		slog.String(handler.options.SpanIDKey, spanContext.SpanID().String()),
	)
	if handler.options.IncludeTraceFlags {
		correlated.AddAttrs(slog.String(handler.options.TraceFlagsKey, spanContext.TraceFlags().String()))
	}
	record.Attrs(func(attr slog.Attr) bool {
		correlated.AddAttrs(attr)
		return true
	})

	owned, err := slogrecord.CloneRawWithUsage(correlated, handler.usage)
	if err != nil {
		return err
	}
	return handler.next.Handle(ctx, owned)
}

// WithAttrs returns an independently derived correlation handler.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if handler.err != nil {
		return &Handler{next: handler.next, options: handler.options, usage: handler.usage, err: handler.err}
	}
	if len(attrs) == 0 {
		return handler
	}
	owned, usage, err := slogrecord.CloneRawAttrsWithUsage(attrs, handler.usage)
	if err != nil {
		return &Handler{next: handler.next, options: handler.options, usage: handler.usage, err: err}
	}
	return &Handler{next: handler.next.WithAttrs(owned), options: handler.options, usage: usage}
}

// WithGroup returns an independently derived correlation handler.
func (handler *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	if handler.err != nil {
		return &Handler{next: handler.next, options: handler.options, usage: handler.usage, err: handler.err}
	}
	usage, err := handler.usage.WithGroup(name)
	if err != nil {
		return &Handler{next: handler.next, options: handler.options, usage: handler.usage, err: err}
	}
	return &Handler{next: handler.next.WithGroup(name), options: handler.options, usage: usage}
}
