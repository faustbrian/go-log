// Package async provides bounded asynchronous delivery for standard log/slog
// handlers.
package async

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

var (
	// ErrNilHandler is returned when New receives no downstream handler.
	ErrNilHandler = errors.New("async: nil handler")
	// ErrInvalidCapacity is returned when queue capacity is not positive.
	ErrInvalidCapacity = errors.New("async: capacity must be greater than zero")
	// ErrInvalidPolicy is returned for an unknown overflow policy.
	ErrInvalidPolicy = errors.New("async: invalid overflow policy")
	// ErrInvalidAdmissionTimeout reports a negative timeout or a waiting policy
	// without a positive admission timeout.
	ErrInvalidAdmissionTimeout = errors.New("async: invalid admission timeout")
	// ErrDropped reports that DropNewest rejected the current record.
	ErrDropped = errors.New("async: record dropped")
	// ErrClosed reports that shutdown has stopped accepting records.
	ErrClosed = errors.New("async: handler closed")
	// ErrAdmissionTimeout reports expiration before a record is accepted.
	ErrAdmissionTimeout = errors.New("async: admission timeout")
)

// OverflowPolicy controls behavior when the bounded queue is full.
type OverflowPolicy uint8

const (
	// DropNewest is the default and rejects the current record with ErrDropped.
	DropNewest OverflowPolicy = iota
	// Block waits for queue capacity up to AdmissionTimeout. Call-site context cancellation does not
	// affect record processing, as required by slog.Handler; shutdown
	// cancellation is separate.
	Block
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
	// AdmissionTimeout bounds owned admission waiting for Block and SyncFallback.
	// These policies require a positive duration. It does not bound callback execution.
	AdmissionTimeout time.Duration
	// OnError receives asynchronous downstream delivery failures. The worker
	// recovers callback panics so reporting cannot stop delivery. The callback
	// runs on the worker and must return promptly.
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

type completionInterval struct {
	first uint64
	last  uint64
}

type runtime struct {
	queue            chan delivery
	enqueue          enqueueFunc
	overflow         OverflowPolicy
	admissionTimeout time.Duration
	onError          func(error)
	accepting        atomic.Bool
	submitMu         chan struct{}
	nextSeq          uint64
	completeMu       sync.Mutex
	watermark        uint64
	completed        []completionInterval
	progress         chan struct{}
	stats            atomicStats
	shutdown         sync.Once
	closing          chan struct{}
	workerDone       chan struct{}
	delivery         context.Context
	cancel           context.CancelFunc
	fallbackMu       sync.Mutex
	fallbacks        int
	fallbackCh       chan struct{}
	fallbackSlot     chan struct{}
}

type enqueueFunc func(*runtime, context.Context, slog.Handler, slog.Record, <-chan time.Time) error

// Handler delivers records through a bounded worker queue. Derived handlers
// share queue lifecycle and statistics while retaining their own downstream
// slog derivation.
type Handler struct {
	next    slog.Handler
	runtime *runtime
	usage   slogrecord.Usage
	err     error
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
	if options.AdmissionTimeout < 0 || ((options.Overflow == Block || options.Overflow == SyncFallback) && options.AdmissionTimeout == 0) {
		return nil, ErrInvalidAdmissionTimeout
	}
	deliveryCtx, cancel := context.WithCancel(context.Background())
	rt := &runtime{
		queue:            make(chan delivery, options.Capacity),
		submitMu:         make(chan struct{}, 1),
		overflow:         options.Overflow,
		admissionTimeout: options.AdmissionTimeout,
		onError:          options.OnError,
		progress:         make(chan struct{}),
		closing:          make(chan struct{}),
		workerDone:       make(chan struct{}),
		delivery:         deliveryCtx,
		cancel:           cancel,
		fallbackCh:       make(chan struct{}),
		fallbackSlot:     make(chan struct{}, 1),
	}
	switch options.Overflow {
	case Block:
		rt.enqueue = (*runtime).enqueueBlocking
	case DropNewest:
		rt.enqueue = (*runtime).enqueueDropNewest
	case DropOldest:
		rt.enqueue = (*runtime).enqueueDropOldest
	case SyncFallback:
	}
	rt.accepting.Store(true)
	go rt.work()

	return &Handler{next: next, runtime: rt}, nil
}

// Enabled delegates to the downstream handler while delivery is accepting
// records. It returns false after shutdown begins.
func (handler *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.err == nil && handler.runtime.accepting.Load() && handler.next.Enabled(ctx, level)
}

// Handle freezes record values, preserves context values without call-site
// cancellation, and applies the configured overflow policy. Shutdown may
// cancel the delivery context.
func (handler *Handler) Handle(ctx context.Context, record slog.Record) error {
	if handler.err != nil {
		return handler.err
	}
	frozen, err := slogrecord.CloneResolvedWithUsage(record, handler.usage)
	if err != nil {
		return err
	}
	deliveryCtx := deliveryContext{Context: context.WithoutCancel(ctx), lifecycle: handler.runtime.delivery}
	runtime := handler.runtime
	var deadline <-chan time.Time
	if runtime.overflow == Block || runtime.overflow == SyncFallback {
		timer := time.NewTimer(runtime.admissionTimeout)
		defer timer.Stop()
		deadline = timer.C
	}
	select {
	case runtime.submitMu <- struct{}{}:
	case <-runtime.closing:
		runtime.stats.rejected.Add(1)
		return ErrClosed
	case <-deadline:
		runtime.stats.rejected.Add(1)
		return ErrAdmissionTimeout
	}
	if !runtime.accepting.Load() {
		runtime.unlockSubmission()
		runtime.stats.rejected.Add(1)
		return ErrClosed
	}
	if runtime.overflow == SyncFallback {
		return runtime.enqueueSyncFallback(deliveryCtx, handler.next, frozen, deadline)
	}
	defer runtime.unlockSubmission()

	return runtime.enqueue(runtime, deliveryCtx, handler.next, frozen, deadline)
}

// WithAttrs returns a derived handler that shares queue lifecycle and stats.
func (handler *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if handler.err != nil {
		return &Handler{next: handler.next, runtime: handler.runtime, usage: handler.usage, err: handler.err}
	}
	if len(attrs) == 0 {
		return handler
	}
	frozen, usage, err := slogrecord.CloneResolvedAttrsWithUsage(attrs, handler.usage)
	if err != nil {
		return &Handler{next: handler.next, runtime: handler.runtime, usage: handler.usage, err: err}
	}

	return &Handler{next: handler.next.WithAttrs(frozen), runtime: handler.runtime, usage: usage}
}

// WithGroup returns a derived handler that shares queue lifecycle and stats.
func (handler *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	if handler.err != nil {
		return &Handler{next: handler.next, runtime: handler.runtime, usage: handler.usage, err: handler.err}
	}
	usage, err := handler.usage.WithGroup(name)
	if err != nil {
		return &Handler{next: handler.next, runtime: handler.runtime, usage: handler.usage, err: err}
	}
	return &Handler{next: handler.next.WithGroup(name), runtime: handler.runtime, usage: usage}
}

// Flush waits until every record accepted before the call is delivered,
// failed, or explicitly dropped. It does not stop new submissions.
func (handler *Handler) Flush(ctx context.Context) error {
	runtime := handler.runtime
	select {
	case runtime.submitMu <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	target := runtime.nextSeq
	runtime.unlockSubmission()

	return runtime.wait(ctx, target)
}

// Shutdown stops accepting records, drains accepted work, and independently
// honors ctx. An expired context cancels cooperative downstream deliveries.
func (handler *Handler) Shutdown(ctx context.Context) error {
	runtime := handler.runtime
	runtime.shutdown.Do(func() {
		runtime.accepting.Store(false)
		close(runtime.closing)
		runtime.lockSubmission()
		close(runtime.queue)
		runtime.unlockSubmission()
	})
	if err := runtime.waitWorker(ctx); err != nil {
		return err
	}

	return runtime.waitFallbacks(ctx)
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

func (runtime *runtime) enqueueBlocking(ctx context.Context, next slog.Handler, record slog.Record, deadline <-chan time.Time) error {
	delivery := runtime.newDelivery(ctx, next, record)
	select {
	case runtime.queue <- delivery:
		runtime.stats.enqueued.Add(1)
		return nil
	case <-runtime.closing:
		runtime.nextSeq--
		runtime.stats.rejected.Add(1)
		return ErrClosed
	case <-deadline:
		runtime.nextSeq--
		runtime.stats.rejected.Add(1)
		return ErrAdmissionTimeout
	}
}

func (runtime *runtime) enqueueDropNewest(ctx context.Context, next slog.Handler, record slog.Record, _ <-chan time.Time) error {
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

func (runtime *runtime) enqueueDropOldest(ctx context.Context, next slog.Handler, record slog.Record, _ <-chan time.Time) error {
	delivery := runtime.newDelivery(ctx, next, record)
	for {
		select {
		case <-runtime.closing:
			runtime.nextSeq--
			runtime.stats.rejected.Add(1)
			return ErrClosed
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

func (runtime *runtime) enqueueSyncFallback(ctx context.Context, next slog.Handler, record slog.Record, deadline <-chan time.Time) error {
	delivery := runtime.newDelivery(ctx, next, record)
	select {
	case runtime.queue <- delivery:
		runtime.unlockSubmission()
		runtime.stats.enqueued.Add(1)
		return nil
	default:
		select {
		case runtime.fallbackSlot <- struct{}{}:
			select {
			case <-runtime.closing:
				<-runtime.fallbackSlot
				runtime.nextSeq--
				runtime.unlockSubmission()
				runtime.stats.rejected.Add(1)
				return ErrClosed
			default:
			}
		case <-runtime.closing:
			runtime.nextSeq--
			runtime.unlockSubmission()
			runtime.stats.rejected.Add(1)
			return ErrClosed
		case <-deadline:
			runtime.nextSeq--
			runtime.unlockSubmission()
			runtime.stats.rejected.Add(1)
			return ErrAdmissionTimeout
		}
		runtime.fallbackMu.Lock()
		runtime.fallbacks++
		runtime.fallbackMu.Unlock()
		runtime.unlockSubmission()
		runtime.stats.synchronousFallback.Add(1)
		defer func() {
			runtime.markComplete(delivery.sequence)
			runtime.finishFallback()
			<-runtime.fallbackSlot
		}()
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

func (runtime *runtime) lockSubmission() { runtime.submitMu <- struct{}{} }

func (runtime *runtime) unlockSubmission() { <-runtime.submitMu }

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

func (runtime *runtime) finishFallback() {
	runtime.fallbackMu.Lock()
	runtime.fallbacks--
	close(runtime.fallbackCh)
	runtime.fallbackCh = make(chan struct{})
	runtime.fallbackMu.Unlock()
}

func (runtime *runtime) waitWorker(ctx context.Context) error {
	select {
	case <-runtime.workerDone:
		return nil
	default:
	}
	select {
	case <-runtime.workerDone:
		return nil
	case <-ctx.Done():
		runtime.cancel()
		return ctx.Err()
	}
}

func (runtime *runtime) waitFallbacks(ctx context.Context) error {
	for {
		runtime.fallbackMu.Lock()
		if runtime.fallbacks == 0 {
			runtime.fallbackMu.Unlock()
			return nil
		}
		progress := runtime.fallbackCh
		runtime.fallbackMu.Unlock()

		select {
		case <-progress:
		case <-ctx.Done():
			runtime.cancel()
			return ctx.Err()
		}
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
	if sequence > runtime.watermark {
		// Sorted, disjoint, non-adjacent intervals coalesce completed history.
		// Every retained interval is separated from the watermark or its prior
		// interval by unresolved accepted work, so retention cannot grow merely
		// because delivery or eviction continues behind an older held record.
		first := 0
		for first < len(runtime.completed) && runtime.completed[first].last < sequence-1 {
			first++
		}
		merged := completionInterval{first: sequence, last: sequence}
		after := first
		for after < len(runtime.completed) {
			interval := runtime.completed[after]
			if interval.first > merged.last && interval.first-merged.last > 1 {
				break
			}
			merged.first = min(merged.first, interval.first)
			merged.last = max(merged.last, interval.last)
			after++
		}
		if after == first {
			runtime.completed = append(runtime.completed, completionInterval{})
			copy(runtime.completed[first+1:], runtime.completed[first:])
		} else {
			copy(runtime.completed[first+1:], runtime.completed[after:])
			runtime.completed = runtime.completed[:len(runtime.completed)-(after-first)+1]
		}
		runtime.completed[first] = merged
		for len(runtime.completed) > 0 && runtime.completed[0].first == runtime.watermark+1 {
			runtime.watermark = runtime.completed[0].last
			copy(runtime.completed, runtime.completed[1:])
			runtime.completed = runtime.completed[:len(runtime.completed)-1]
		}
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

type deliveryContext struct {
	context.Context
	lifecycle context.Context
}

func (ctx deliveryContext) Done() <-chan struct{} { return ctx.lifecycle.Done() }
func (ctx deliveryContext) Err() error            { return ctx.lifecycle.Err() }
