// Package otel correlates slog records with the standard OpenTelemetry span
// context produced by telemetry. It does not initialize providers, mutate
// OpenTelemetry globals, export signals, or own SDK lifecycle.
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

// Handler decorates a standard slog handler with trace/span correlation.
type Handler struct {
	next    slog.Handler
	options Options
	usage   slogrecord.Usage
	err     error
}

// New constructs a correlation decorator without initializing OpenTelemetry.
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

// Handle adds correlation for a valid span context and delegates downstream.
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
