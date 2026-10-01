// Package log provides small constructors for composing standard log/slog
// loggers and handlers without introducing a replacement logger interface.
package log

import (
	"errors"
	"io"
	"log/slog"

	"github.com/faustbrian/go-log/v2/handler/redact"
	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

const (
	// MaxRecordAttributes bounds cumulative derived and record structure cloned
	// or resolved by a package handler.
	MaxRecordAttributes = slogrecord.MaxRecordAttributes
	// MaxRecordDepth bounds cumulative nested slog groups retained, cloned, or
	// resolved by a package handler.
	MaxRecordDepth = slogrecord.MaxRecordDepth
)

var (
	// ErrNilHandler is returned when New is called without a handler.
	ErrNilHandler = errors.New("log: nil handler")
	// ErrRecordLimit reports that a record exceeded the fixed structural
	// attribute count or nesting-depth budget.
	ErrRecordLimit = slogrecord.ErrRecordLimit
)

// Option decorates a slog handler while constructing a logger.
//
// Options are applied in the order supplied to New. An option should return
// an immutable derived handler and leave its input unchanged. Custom options
// are trusted collaborators and must retain the incoming privacy boundary.
type Option func(slog.Handler) (slog.Handler, error)

// New constructs a standard slog.Logger with secure default data omission.
//
// New keeps *slog.Logger as the application-facing type. It returns the first
// option error and never constructs a logger around a nil handler.
// Record messages are replaced and caller attributes and groups are omitted
// without resolving their values. Supplied handlers may already contain data;
// applications own prebound handlers, custom options, and downstream I/O.
func New(handler slog.Handler, options ...Option) (*slog.Logger, error) {
	return newLogger(handler, nil, options...)
}

// TrustedNew preserves explicitly trusted messages and attributes through the
// same bounded owner. Messages remain limited to redact.MaxTrustedMessageBytes.
// Callers own payload classification, private diagnostics, and downstream I/O.
func TrustedNew(handler slog.Handler, options ...Option) (*slog.Logger, error) {
	return newLogger(handler, &redact.Options{PreserveTrustedMessage: true, PreserveTrustedAttributes: true}, options...)
}

func newLogger(handler slog.Handler, privacy *redact.Options, options ...Option) (*slog.Logger, error) {
	if handler == nil {
		return nil, ErrNilHandler
	}
	// The handler was checked above and privacy is package-owned configuration;
	// the redaction constructor has no remaining failure condition.
	owned, _ := redact.New(handler, privacy)
	// Apply derivations inside the single owner so it accounts for bound and
	// record structure together before omitting private data.
	handler = owned

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
	cloned, err := slogrecord.CloneRawAttrs(attrs)

	return func(handler slog.Handler) (slog.Handler, error) {
		if err != nil {
			return nil, err
		}
		owned, err := slogrecord.CloneRawAttrs(cloned)
		if err != nil {
			return nil, err
		}
		return handler.WithAttrs(owned), nil
	}
}

// WithGroup returns an option that qualifies subsequent attributes with name.
func WithGroup(name string) Option {
	return func(handler slog.Handler) (slog.Handler, error) {
		return handler.WithGroup(name), nil
	}
}

// JSON constructs a secure-default slog JSON logger using New's privacy policy.
func JSON(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	// The standard constructor always returns a non-nil handler and no options
	// are applied, so New cannot fail here.
	logger, _ := New(slog.NewJSONHandler(writer, options))
	return logger
}

// Text constructs a secure-default slog text logger using New's privacy policy.
func Text(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	logger, _ := New(slog.NewTextHandler(writer, options))
	return logger
}

// TrustedJSON constructs a JSON logger for explicitly trusted, structurally
// bounded data. It uses TrustedNew's message limit and collaborator contract.
func TrustedJSON(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	logger, _ := TrustedNew(slog.NewJSONHandler(writer, options))
	return logger
}

// TrustedText constructs a text logger for explicitly trusted, structurally
// bounded data. It uses TrustedNew's message limit and collaborator contract.
func TrustedText(writer io.Writer, options *slog.HandlerOptions) *slog.Logger {
	logger, _ := TrustedNew(slog.NewTextHandler(writer, options))
	return logger
}
