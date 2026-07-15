// Package log provides small constructors for composing standard log/slog
// loggers and handlers without introducing a replacement logger interface.
package log

import (
	"errors"
	"io"
	"log/slog"
)

// ErrNilHandler is returned when New is called without a handler.
var ErrNilHandler = errors.New("log: nil handler")

// Option decorates a slog handler while constructing a logger.
//
// Options are applied in the order supplied to New. An option should return
// an immutable derived handler and leave its input unchanged.
type Option func(slog.Handler) (slog.Handler, error)

// New constructs a standard slog.Logger from handler.
//
// New keeps *slog.Logger as the application-facing type. It returns the first
// option error and never constructs a logger around a nil handler.
func New(handler slog.Handler, options ...Option) (*slog.Logger, error) {
	if handler == nil {
		return nil, ErrNilHandler
	}

	var err error
	for _, option := range options {
		handler, err = option(handler)
		if err != nil {
			return nil, err
		}
		if handler == nil {
			return nil, ErrNilHandler
		}
	}

	return slog.New(handler), nil
}

// WithAttrs returns an option that adds attrs to every record.
func WithAttrs(attrs ...slog.Attr) Option {
	cloned := append([]slog.Attr(nil), attrs...)

	return func(handler slog.Handler) (slog.Handler, error) {
		return handler.WithAttrs(cloned), nil
	}
}

// WithGroup returns an option that qualifies subsequent attributes with name.
func WithGroup(name string) Option {
	return func(handler slog.Handler) (slog.Handler, error) {
		return handler.WithGroup(name), nil
	}
}

// JSON constructs a standard slog JSON logger.
func JSON(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, options))
}

// Text constructs a standard slog text logger.
func Text(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	return slog.New(slog.NewTextHandler(writer, options))
}
