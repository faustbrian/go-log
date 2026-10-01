// Package stack provides synchronous fan-out and per-sink level routing for
// standard log/slog handlers.
package stack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

var (
	// ErrNilHandler reports a route without a handler.
	ErrNilHandler = errors.New("stack: nil handler")
	// ErrInvalidRange reports a route whose minimum exceeds its maximum.
	ErrInvalidRange = errors.New("stack: invalid level range")
)

// Route associates a handler with optional inclusive level bounds.
//
// A nil MinLevel or MaxLevel leaves that side unbounded. The downstream
// handler's Enabled method remains authoritative within the configured range.
type Route struct {
	Handler  slog.Handler
	MinLevel slog.Leveler
	MaxLevel slog.Leveler
}

// Handler synchronously fans each record out to every matching route.
//
// Handler is immutable after construction. Derived handlers returned by
// WithAttrs and WithGroup do not modify their parent.
type Handler struct {
	routes []Route
	usage  slogrecord.Usage
	err    error
}

// New validates routes and constructs a fan-out handler.
func New(routes ...Route) (*Handler, error) {
	cloned := append([]Route(nil), routes...)
	for index, route := range cloned {
		if route.Handler == nil {
			return nil, fmt.Errorf("%w at route %d", ErrNilHandler, index)
		}
		if route.MinLevel != nil && route.MaxLevel != nil &&
			route.MinLevel.Level() > route.MaxLevel.Level() {
			return nil, fmt.Errorf("%w at route %d", ErrInvalidRange, index)
		}
	}

	return &Handler{routes: cloned}, nil
}

// Enabled reports whether at least one matching downstream handler accepts
// level.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	if handler.err != nil {
		return false
	}
	for _, route := range handler.routes {
		if route.accepts(level) && route.Handler.Enabled(ctx, level) {
			return true
		}
	}

	return false
}

// Handle delivers record to every matching enabled route and joins all sink
// errors. A failure from one route never prevents delivery to later routes.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	if handler.err != nil {
		return handler.err
	}
	var result error
	for _, route := range handler.routes {
		if !route.accepts(record.Level) || !route.Handler.Enabled(ctx, record.Level) {
			continue
		}
		cloned, err := slogrecord.CloneRawWithUsage(record, handler.usage)
		if err != nil {
			return err
		}
		if err := route.Handler.Handle(ctx, cloned); err != nil {
			result = errors.Join(result, err)
		}
	}

	return result
}

// WithAttrs returns a derived stack whose routes include attrs.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if handler.err != nil {
		return &Handler{routes: append([]Route(nil), handler.routes...), usage: handler.usage, err: handler.err}
	}
	if len(attrs) == 0 {
		return handler
	}
	owned, usage, err := slogrecord.CloneRawAttrsWithUsage(attrs, handler.usage)
	if err != nil {
		return &Handler{routes: append([]Route(nil), handler.routes...), usage: handler.usage, err: err}
	}
	routes := make([]Route, len(handler.routes))
	for index, route := range handler.routes {
		routeAttrs, err := slogrecord.CloneRawAttrs(owned)
		if err != nil {
			return &Handler{routes: append([]Route(nil), handler.routes...), usage: handler.usage, err: err}
		}
		route.Handler = route.Handler.WithAttrs(routeAttrs)
		routes[index] = route
	}

	return &Handler{routes: routes, usage: usage}
}

// WithGroup returns a derived stack whose routes qualify subsequent attrs
// with name.
func (handler *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	if handler.err != nil {
		return &Handler{routes: append([]Route(nil), handler.routes...), usage: handler.usage, err: handler.err}
	}
	usage, err := handler.usage.WithGroup(name)
	if err != nil {
		return &Handler{routes: append([]Route(nil), handler.routes...), usage: handler.usage, err: err}
	}
	routes := make([]Route, len(handler.routes))
	for index, route := range handler.routes {
		route.Handler = route.Handler.WithGroup(name)
		routes[index] = route
	}

	return &Handler{routes: routes, usage: usage}
}

func (route Route) accepts(level slog.Level) bool {
	if route.MinLevel != nil && level < route.MinLevel.Level() {
		return false
	}
	if route.MaxLevel != nil && level > route.MaxLevel.Level() {
		return false
	}

	return true
}
