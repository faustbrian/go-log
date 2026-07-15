// Package sample provides deterministic and rate-based sampling decorators for
// standard log/slog handlers.
package sample

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync/atomic"
)

var (
	// ErrNilHandler is returned when New receives no downstream handler.
	ErrNilHandler = errors.New("sample: nil handler")
	// ErrNilSampler is returned when New receives no sampling policy.
	ErrNilSampler = errors.New("sample: nil sampler")
	// ErrInvalidEvery is returned when Every receives zero.
	ErrInvalidEvery = errors.New("sample: every must be greater than zero")
	// ErrInvalidRate is returned when a deterministic rate is outside [0, 1].
	ErrInvalidRate = errors.New("sample: rate must be between zero and one")
	// ErrNilKey is returned when Deterministic receives no key function.
	ErrNilKey = errors.New("sample: nil key function")
)

// Sampler decides whether a record should be delivered.
type Sampler func(context.Context, slog.Record) bool

// KeyFunc returns the stable identity used for deterministic sampling.
type KeyFunc func(context.Context, slog.Record) string

// Stats is a point-in-time sampling counter snapshot.
type Stats struct {
	Kept    uint64
	Dropped uint64
}

type counters struct {
	kept    atomic.Uint64
	dropped atomic.Uint64
}

// Handler decorates a standard handler with a sampling policy.
type Handler struct {
	next    slog.Handler
	sampler Sampler
	stats   *counters
}

// New constructs a sampling handler.
func New(next slog.Handler, sampler Sampler) (*Handler, error) {
	if next == nil {
		return nil, ErrNilHandler
	}
	if sampler == nil {
		return nil, ErrNilSampler
	}

	return &Handler{next: next, sampler: sampler, stats: &counters{}}, nil
}

// Every constructs a concurrency-safe sampler that keeps the first record and
// then one record from each consecutive group of n records.
func Every(n uint64) (Sampler, error) {
	if n == 0 {
		return nil, ErrInvalidEvery
	}
	var seen atomic.Uint64

	return func(_ context.Context, _ slog.Record) bool {
		return (seen.Add(1)-1)%n == 0
	}, nil
}

// Deterministic constructs a stable hash sampler. Records with the same key
// always receive the same decision for a given rate.
func Deterministic(rate float64, key KeyFunc) (Sampler, error) {
	if math.IsNaN(rate) || rate < 0 || rate > 1 {
		return nil, ErrInvalidRate
	}
	if key == nil {
		return nil, ErrNilKey
	}

	return func(ctx context.Context, record slog.Record) bool {
		if rate == 0 {
			return false
		}
		if rate == 1 {
			return true
		}
		hash := fnv64a(key(ctx, record))

		return float64(hash)/float64(math.MaxUint64) < rate
	}, nil
}

// Enabled delegates level decisions to the downstream handler.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

// Handle samples an independent record clone before optionally delivering the
// original record downstream.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	if !handler.sampler(ctx, cloneRecord(record)) {
		handler.stats.dropped.Add(1)
		return nil
	}
	handler.stats.kept.Add(1)

	return handler.next.Handle(ctx, record)
}

// WithAttrs returns a derived handler that shares the sampler and counters.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{
		next:    handler.next.WithAttrs(attrs),
		sampler: handler.sampler,
		stats:   handler.stats,
	}
}

// WithGroup returns a derived handler that shares the sampler and counters.
func (handler *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		next:    handler.next.WithGroup(name),
		sampler: handler.sampler,
		stats:   handler.stats,
	}
}

// Stats returns an atomic point-in-time counter snapshot.
func (handler *Handler) Stats() Stats {
	return Stats{
		Kept:    handler.stats.kept.Load(),
		Dropped: handler.stats.dropped.Load(),
	}
}

func fnv64a(value string) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for index := 0; index < len(value); index++ {
		hash ^= uint64(value[index])
		hash *= prime
	}
	hash ^= hash >> 33
	hash *= 0xff51afd7ed558ccd
	hash ^= hash >> 33
	hash *= 0xc4ceb9fe1a85ec53
	hash ^= hash >> 33

	return hash
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
