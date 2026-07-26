// Package stack provides synchronous fan-out and per-sink level routing for
// standard log/slog handlers.
package stack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	var result error
	for _, route := range handler.routes {
		if !route.accepts(record.Level) || !route.Handler.Enabled(ctx, record.Level) {
			continue
		}
		if err := route.Handler.Handle(ctx, cloneRecord(record)); err != nil {
			result = errors.Join(result, err)
		}
	}

	return result
}

// WithAttrs returns a derived stack whose routes include attrs.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	routes := make([]Route, len(handler.routes))
	for index, route := range handler.routes {
		owned := cloneAttrs(attrs)
		route.Handler = route.Handler.WithAttrs(owned)
		routes[index] = route
	}

	return &Handler{routes: routes}
}

// WithGroup returns a derived stack whose routes qualify subsequent attrs
// with name.
func (handler *Handler) WithGroup(name string) slog.Handler {
	routes := make([]Route, len(handler.routes))
	for index, route := range handler.routes {
		route.Handler = route.Handler.WithGroup(name)
		routes[index] = route
	}

	return &Handler{routes: routes}
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

func cloneRecord(record slog.Record) slog.Record {
	cloned := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		cloned.AddAttrs(cloneAttr(attr))
		return true
	})

	return cloned
}

func cloneAttrs(attrs []slog.Attr) []slog.Attr {
	cloned := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		cloned[index] = cloneAttr(attr)
	}

	return cloned
}

func cloneAttr(attr slog.Attr) slog.Attr {
	if attr.Value.Kind() != slog.KindGroup {
		return attr
	}

	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(cloneAttrs(attr.Value.Group())...)}
}
