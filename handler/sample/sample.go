// Package sample provides deterministic and rate-based sampling decorators for
// standard log/slog handlers.
package sample

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync/atomic"

	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

// MaxKeyBytes bounds owned deterministic hashing of a returned sampling key.
const MaxKeyBytes = 1024

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

// KeyFunc returns the stable identity used for deterministic sampling. For
// rates strictly between zero and one, keys exceeding MaxKeyBytes are dropped.
// Callers own the callback's execution time and returned string allocation.
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
	usage   slogrecord.Usage
	err     error
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
		identity := key(ctx, record)
		if len(identity) > MaxKeyBytes {
			return false
		}
		hash := fnv64a(identity)

		return float64(hash)/float64(math.MaxUint64) < rate
	}, nil
}

// Enabled delegates level decisions to the downstream handler.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.err == nil && handler.next.Enabled(ctx, level)
}

// Handle samples an independent record clone before optionally delivering the
// original record downstream.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	if handler.err != nil {
		return handler.err
	}
	cloned, err := slogrecord.CloneRawWithUsage(record, handler.usage)
	if err != nil {
		return err
	}
	if !handler.sampler(ctx, cloned) {
		handler.stats.dropped.Add(1)
		return nil
	}
	handler.stats.kept.Add(1)

	return handler.next.Handle(ctx, record)
}

// WithAttrs returns a derived handler that shares the sampler and counters.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if handler.err != nil {
		return &Handler{next: handler.next, sampler: handler.sampler, stats: handler.stats, usage: handler.usage, err: handler.err}
	}
	if len(attrs) == 0 {
		return handler
	}
	owned, usage, err := slogrecord.CloneRawAttrsWithUsage(attrs, handler.usage)
	if err != nil {
		return &Handler{next: handler.next, sampler: handler.sampler, stats: handler.stats, usage: handler.usage, err: err}
	}
	return &Handler{
		next:    handler.next.WithAttrs(owned),
		sampler: handler.sampler,
		stats:   handler.stats,
		usage:   usage,
	}
}

// WithGroup returns a derived handler that shares the sampler and counters.
func (handler *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	if handler.err != nil {
		return &Handler{next: handler.next, sampler: handler.sampler, stats: handler.stats, usage: handler.usage, err: handler.err}
	}
	usage, err := handler.usage.WithGroup(name)
	if err != nil {
		return &Handler{next: handler.next, sampler: handler.sampler, stats: handler.stats, usage: handler.usage, err: err}
	}
	return &Handler{
		next:    handler.next.WithGroup(name),
		sampler: handler.sampler,
		stats:   handler.stats,
		usage:   usage,
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
