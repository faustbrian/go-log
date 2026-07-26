// Package async provides bounded asynchronous delivery for standard log/slog
// handlers.
package async

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
)

var (
	// ErrNilHandler is returned when New receives no downstream handler.
	ErrNilHandler = errors.New("async: nil handler")
	// ErrInvalidCapacity is returned when queue capacity is not positive.
	ErrInvalidCapacity = errors.New("async: capacity must be greater than zero")
	// ErrInvalidPolicy is returned for an unknown overflow policy.
	ErrInvalidPolicy = errors.New("async: invalid overflow policy")
	// ErrDropped reports that DropNewest rejected the current record.
	ErrDropped = errors.New("async: record dropped")
	// ErrClosed reports that shutdown has stopped accepting records.
	ErrClosed = errors.New("async: handler closed")
)

// OverflowPolicy controls behavior when the bounded queue is full.
type OverflowPolicy uint8

const (
	// Block waits for queue capacity. Context cancellation does not affect
	// record processing, as required by slog.Handler.
	Block OverflowPolicy = iota
	// DropNewest rejects the current record with ErrDropped.
	DropNewest
	// DropOldest evicts the oldest queued record and accepts the current one.
	DropOldest
	// SyncFallback delivers the current record synchronously.
	SyncFallback
)

// Options configures bounded asynchronous delivery.
type Options struct {
	// Capacity is the maximum number of records waiting for delivery.
	Capacity int
	// Overflow selects behavior when Capacity is exhausted.
	Overflow OverflowPolicy
	// OnError receives asynchronous downstream delivery failures. The worker
	// recovers callback panics so reporting cannot stop delivery.
	OnError func(error)
}

// Stats is an atomic point-in-time delivery counter snapshot.
type Stats struct {
	Enqueued            uint64
	Delivered           uint64
	Failed              uint64
	DroppedNewest       uint64
	DroppedOldest       uint64
	SynchronousFallback uint64
	Rejected            uint64
}

// Lost returns records accepted or offered for delivery that did not reach
// the downstream handler successfully. Rejected records are excluded because
// callers receive an immediate error before acceptance.
func (stats Stats) Lost() uint64 {
	return stats.Failed + stats.DroppedNewest + stats.DroppedOldest
}

type atomicStats struct {
	enqueued            atomic.Uint64
	delivered           atomic.Uint64
	failed              atomic.Uint64
	droppedNewest       atomic.Uint64
	droppedOldest       atomic.Uint64
	synchronousFallback atomic.Uint64
	rejected            atomic.Uint64
}

type delivery struct {
	sequence uint64
	ctx      context.Context
	next     slog.Handler
	record   slog.Record
}

type runtime struct {
	queue      chan delivery
	enqueue    enqueueFunc
	onError    func(error)
	accepting  atomic.Bool
	submitMu   sync.Mutex
	nextSeq    uint64
	completeMu sync.Mutex
	watermark  uint64
	completed  map[uint64]struct{}
	progress   chan struct{}
	stats      atomicStats
	shutdown   sync.Once
	done       chan struct{}
	workerDone chan struct{}
}

type enqueueFunc func(*runtime, context.Context, slog.Handler, slog.Record) error

// Handler delivers records through a bounded worker queue. Derived handlers
// share queue lifecycle and statistics while retaining their own downstream
// slog derivation.
type Handler struct {
	next    slog.Handler
	runtime *runtime
}

// New constructs and starts a bounded asynchronous handler.
func New(next slog.Handler, options Options) (*Handler, error) {
	if next == nil {
		return nil, ErrNilHandler
	}
	if options.Capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if options.Overflow > SyncFallback {
		return nil, ErrInvalidPolicy
	}
	rt := &runtime{
		queue:      make(chan delivery, options.Capacity),
		onError:    options.OnError,
		completed:  make(map[uint64]struct{}, options.Capacity),
		progress:   make(chan struct{}),
		done:       make(chan struct{}),
		workerDone: make(chan struct{}),
	}
	switch options.Overflow {
	case Block:
		rt.enqueue = (*runtime).enqueueBlocking
	case DropNewest:
		rt.enqueue = (*runtime).enqueueDropNewest
	case DropOldest:
		rt.enqueue = (*runtime).enqueueDropOldest
	case SyncFallback:
		rt.enqueue = (*runtime).enqueueOrFallback
	}
	rt.accepting.Store(true)
	go rt.work()

	return &Handler{next: next, runtime: rt}, nil
}

// Enabled delegates to the downstream handler while delivery is accepting
// records. It returns false after shutdown begins.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.runtime.accepting.Load() && handler.next.Enabled(ctx, level)
}

// Handle freezes record values, preserves context values without cancellation,
// and applies the configured overflow policy.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	frozen := freezeRecord(record)
	deliveryCtx := context.WithoutCancel(ctx)
	runtime := handler.runtime
	runtime.submitMu.Lock()
	defer runtime.submitMu.Unlock()
	if !runtime.accepting.Load() {
		runtime.stats.rejected.Add(1)
		return ErrClosed
	}

	return runtime.enqueue(runtime, deliveryCtx, handler.next, frozen)
}

// WithAttrs returns a derived handler that shares queue lifecycle and stats.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	frozen := make([]slog.Attr, len(attrs))
	for index, attr := range attrs {
		frozen[index] = freezeAttr(attr)
	}

	return &Handler{next: handler.next.WithAttrs(frozen), runtime: handler.runtime}
}

// WithGroup returns a derived handler that shares queue lifecycle and stats.
func (handler *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: handler.next.WithGroup(name), runtime: handler.runtime}
}

// Flush waits until every record accepted before the call is delivered,
// failed, or explicitly dropped. It does not stop new submissions.
func (handler *Handler) Flush(ctx context.Context) error {
	runtime := handler.runtime
	runtime.submitMu.Lock()
	target := runtime.nextSeq
	runtime.submitMu.Unlock()

	return runtime.wait(ctx, target)
}

// Shutdown stops accepting records and begins an irreversible background
// drain. Each call waits for the same shutdown and independently honors ctx.
func (handler *Handler) Shutdown(ctx context.Context) error {
	runtime := handler.runtime
	runtime.shutdown.Do(func() {
		runtime.accepting.Store(false)
		go runtime.drainAndClose()
	})
	select {
	case <-runtime.done:
		return nil
	default:
	}

	select {
	case <-runtime.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats returns an atomic point-in-time delivery counter snapshot.
func (handler *Handler) Stats() Stats {
	stats := &handler.runtime.stats

	return Stats{
		Enqueued:            stats.enqueued.Load(),
		Delivered:           stats.delivered.Load(),
		Failed:              stats.failed.Load(),
		DroppedNewest:       stats.droppedNewest.Load(),
		DroppedOldest:       stats.droppedOldest.Load(),
		SynchronousFallback: stats.synchronousFallback.Load(),
		Rejected:            stats.rejected.Load(),
	}
}

func (runtime *runtime) enqueueBlocking(ctx context.Context, next slog.Handler, record slog.Record) error {
	delivery := runtime.newDelivery(ctx, next, record)
	runtime.queue <- delivery
	runtime.stats.enqueued.Add(1)

	return nil
}

func (runtime *runtime) enqueueDropNewest(ctx context.Context, next slog.Handler, record slog.Record) error {
	delivery := runtime.newDelivery(ctx, next, record)
	select {
	case runtime.queue <- delivery:
		runtime.stats.enqueued.Add(1)
		return nil
	default:
		runtime.nextSeq--
		runtime.stats.droppedNewest.Add(1)
		return ErrDropped
	}
}

func (runtime *runtime) enqueueDropOldest(ctx context.Context, next slog.Handler, record slog.Record) error {
	delivery := runtime.newDelivery(ctx, next, record)
	for {
		select {
		case runtime.queue <- delivery:
			runtime.stats.enqueued.Add(1)
			return nil
		default:
		}

		select {
		case oldest := <-runtime.queue:
			runtime.stats.droppedOldest.Add(1)
			runtime.markComplete(oldest.sequence)
		default:
		}
	}
}

func (runtime *runtime) enqueueOrFallback(ctx context.Context, next slog.Handler, record slog.Record) error {
	delivery := runtime.newDelivery(ctx, next, record)
	select {
	case runtime.queue <- delivery:
		runtime.stats.enqueued.Add(1)
		return nil
	default:
		runtime.nextSeq--
		runtime.stats.synchronousFallback.Add(1)
		err := next.Handle(ctx, record)
		if err != nil {
			runtime.stats.failed.Add(1)
			return err
		}
		runtime.stats.delivered.Add(1)
		return nil
	}
}

func (runtime *runtime) newDelivery(ctx context.Context, next slog.Handler, record slog.Record) delivery {
	runtime.nextSeq++

	return delivery{sequence: runtime.nextSeq, ctx: ctx, next: next, record: record}
}

func (runtime *runtime) work() {
	defer close(runtime.workerDone)
	for delivery := range runtime.queue {
		err := delivery.next.Handle(delivery.ctx, delivery.record)
		if err != nil {
			runtime.stats.failed.Add(1)
			if runtime.onError != nil {
				runtime.report(err)
			}
		} else {
			runtime.stats.delivered.Add(1)
		}
		runtime.markComplete(delivery.sequence)
	}
}

func (runtime *runtime) report(err error) {
	defer func() {
		_ = recover()
	}()
	runtime.onError(err)
}

func (runtime *runtime) markComplete(sequence uint64) {
	runtime.completeMu.Lock()
	if sequence == runtime.watermark+1 {
		runtime.watermark = sequence
		for {
			if _, ok := runtime.completed[runtime.watermark+1]; !ok {
				break
			}
			delete(runtime.completed, runtime.watermark+1)
			runtime.watermark++
		}
	} else if sequence > runtime.watermark {
		runtime.completed[sequence] = struct{}{}
	}
	close(runtime.progress)
	runtime.progress = make(chan struct{})
	runtime.completeMu.Unlock()
}

func (runtime *runtime) wait(ctx context.Context, target uint64) error {
	for {
		runtime.completeMu.Lock()
		if runtime.watermark >= target {
			runtime.completeMu.Unlock()
			return nil
		}
		progress := runtime.progress
		runtime.completeMu.Unlock()

		select {
		case <-progress:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (runtime *runtime) drainAndClose() {
	runtime.submitMu.Lock()
	target := runtime.nextSeq
	runtime.submitMu.Unlock()
	_ = runtime.wait(context.Background(), target)
	close(runtime.queue)
	<-runtime.workerDone
	close(runtime.done)
}

func freezeRecord(record slog.Record) slog.Record {
	frozen := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		frozen.AddAttrs(freezeAttr(attr))
		return true
	})

	return frozen
}

func freezeAttr(attr slog.Attr) slog.Attr {
	value := attr.Value.Resolve()
	if value.Kind() != slog.KindGroup {
		return slog.Attr{Key: attr.Key, Value: value}
	}
	children := value.Group()
	frozen := make([]slog.Attr, len(children))
	for index, child := range children {
		frozen[index] = freezeAttr(child)
	}

	return slog.Attr{Key: attr.Key, Value: slog.GroupValue(frozen...)}
}
