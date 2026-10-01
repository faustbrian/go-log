package async

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"
)

// These finite fixtures hold one accepted delivery while later accepted work
// completes. Representation assertions address the completion owner's memory
// contract; Flush, order, and stats assert the corresponding public lifecycle.
func TestDropOldestCoalescesCompletionBehindHeldWorker(t *testing.T) {
	sink := newCompletionSink(false)
	handler := completionHandler(t, sink, DropOldest)
	submitCompletionRecord(t, handler, "1")
	awaitCompletionSignal(t, sink.workerStarted)
	for _, message := range []string{"2", "3", "4", "5"} {
		submitCompletionRecord(t, handler, message)
	}
	assertCompletionRetention(t, handler, 0, 1)
	assertCompletionFlushBlocked(t, handler)
	sink.releaseWorker()
	flushCompletion(t, handler)
	assertCompletionRetention(t, handler, 5, 0)
	if got := sink.messages(); !reflect.DeepEqual(got, []string{"1", "5"}) {
		t.Errorf("delivery order = %v, want [1 5]", got)
	}
	if got := handler.Stats(); got.Enqueued != 5 || got.Delivered != 2 || got.DroppedOldest != 3 || got.Rejected != 0 || got.Lost() != 3 {
		t.Errorf("accounting = %#v", got)
	}
}

func TestSyncFallbackCoalescesCompletionBehindHeldFallback(t *testing.T) {
	sink := newCompletionSink(true)
	handler := completionHandler(t, sink, SyncFallback)
	submitCompletionRecord(t, handler, "1")
	awaitCompletionSignal(t, sink.workerStarted)
	submitCompletionRecord(t, handler, "2")
	fallback := make(chan error, 1)
	go func() { fallback <- handler.Handle(context.Background(), completionRecord("3")) }()
	t.Cleanup(func() {
		sink.releaseWorker()
		sink.releaseFallback()
		select {
		case err := <-fallback:
			if err != nil {
				t.Errorf("fallback cleanup: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("fallback cleanup did not finish")
		}
	})
	awaitCompletionSignal(t, sink.fallbackStarted)
	sink.releaseWorker()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.runtime.wait(ctx, 2); err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"4", "5", "6"} {
		handler.runtime.completeMu.Lock()
		progress := handler.runtime.progress
		handler.runtime.completeMu.Unlock()
		submitCompletionRecord(t, handler, message)
		awaitCompletionSignal(t, progress)
	}
	assertCompletionRetention(t, handler, 2, 1)
	assertCompletionFlushBlocked(t, handler)
	sink.releaseFallback()
	flushCompletion(t, handler)
	assertCompletionRetention(t, handler, 6, 0)
	if got := sink.messages(); !reflect.DeepEqual(got, []string{"1", "2", "4", "5", "6", "3"}) {
		t.Errorf("delivery order = %v", got)
	}
	if got := handler.Stats(); got.Enqueued != 5 || got.Delivered != 6 || got.SynchronousFallback != 1 || got.Lost() != 0 || got.Rejected != 0 {
		t.Errorf("accounting = %#v", got)
	}
}

func TestSyncFallbackPreservesSeparateCompletionIntervals(t *testing.T) {
	sink := &splitCompletionSink{completionSink: newCompletionSink(false), secondStarted: make(chan struct{}), secondRelease: make(chan struct{})}
	handler, err := New(sink, Options{Capacity: 1, Overflow: SyncFallback, AdmissionTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sink.releaseWorker()
		sink.releaseSecond()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown cleanup: %v", err)
		}
	})
	submitCompletionRecord(t, handler, "1")
	awaitCompletionSignal(t, sink.workerStarted)
	submitCompletionRecord(t, handler, "2")
	submitCompletionRecord(t, handler, "3")
	sink.releaseWorker()
	awaitCompletionSignal(t, sink.secondStarted)
	submitCompletionRecord(t, handler, "4")
	submitCompletionRecord(t, handler, "5")

	handler.runtime.completeMu.Lock()
	watermark := handler.runtime.watermark
	intervals := append([]completionInterval(nil), handler.runtime.completed...)
	handler.runtime.completeMu.Unlock()
	if watermark != 1 || !reflect.DeepEqual(intervals, []completionInterval{{first: 3, last: 3}, {first: 5, last: 5}}) {
		t.Errorf("completion watermark=%d intervals=%v, want 1/[3..3 5..5]", watermark, intervals)
	}
	assertCompletionFlushBlocked(t, handler)
	if got := handler.Stats(); got.Enqueued != 3 || got.Delivered != 3 || got.SynchronousFallback != 2 || got.Rejected != 0 || got.Lost() != 0 {
		t.Errorf("held accounting = %#v", got)
	}
	sink.releaseSecond()
	flushCompletion(t, handler)
	assertCompletionRetention(t, handler, 5, 0)
	if got := sink.messages(); !reflect.DeepEqual(got, []string{"3", "1", "5", "2", "4"}) {
		t.Errorf("delivery order = %v, want [3 1 5 2 4]", got)
	}
	if got := handler.Stats(); got.Enqueued != 3 || got.Delivered != 5 || got.SynchronousFallback != 2 || got.Rejected != 0 || got.Lost() != 0 {
		t.Errorf("drained accounting = %#v", got)
	}
}

type splitCompletionSink struct {
	*completionSink
	secondStarted chan struct{}
	secondRelease chan struct{}
	secondOnce    sync.Once
}

func (sink *splitCompletionSink) releaseSecond() {
	sink.secondOnce.Do(func() { close(sink.secondRelease) })
}

func (sink *splitCompletionSink) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "2" {
		close(sink.secondStarted)
		<-sink.secondRelease
	}
	return sink.completionSink.Handle(ctx, record)
}

func completionHandler(t *testing.T, sink *completionSink, policy OverflowPolicy) *Handler {
	t.Helper()
	handler, err := New(sink, Options{Capacity: 1, Overflow: policy, AdmissionTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sink.releaseWorker()
		sink.releaseFallback()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown cleanup: %v", err)
		}
	})
	return handler
}

func completionRecord(message string) slog.Record {
	return slog.NewRecord(time.Time{}, slog.LevelInfo, message, 0)
}

func submitCompletionRecord(t *testing.T, handler *Handler, message string) {
	t.Helper()
	if err := handler.Handle(context.Background(), completionRecord(message)); err != nil {
		t.Fatal(err)
	}
}

func awaitCompletionSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("completion fixture did not reach the expected boundary")
	}
}

func assertCompletionRetention(t *testing.T, handler *Handler, watermark uint64, blocks int) {
	t.Helper()
	handler.runtime.completeMu.Lock()
	defer handler.runtime.completeMu.Unlock()
	if handler.runtime.watermark != watermark || len(handler.runtime.completed) != blocks {
		t.Errorf("completion watermark=%d retained blocks=%d, want %d/%d", handler.runtime.watermark, len(handler.runtime.completed), watermark, blocks)
	}
}

func assertCompletionFlushBlocked(t *testing.T, handler *Handler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handler.Flush(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Flush across held delivery = %v, want canceled", err)
	}
}

func flushCompletion(t *testing.T, handler *Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

type completionSink struct {
	workerStarted   chan struct{}
	fallbackStarted chan struct{}
	workerRelease   chan struct{}
	fallbackRelease chan struct{}
	holdFallback    bool
	workerOnce      sync.Once
	fallbackOnce    sync.Once
	mu              sync.Mutex
	delivered       []string
}

func newCompletionSink(holdFallback bool) *completionSink {
	return &completionSink{workerStarted: make(chan struct{}), fallbackStarted: make(chan struct{}), workerRelease: make(chan struct{}), fallbackRelease: make(chan struct{}), holdFallback: holdFallback}
}

func (sink *completionSink) releaseWorker() { sink.workerOnce.Do(func() { close(sink.workerRelease) }) }
func (sink *completionSink) releaseFallback() {
	sink.fallbackOnce.Do(func() { close(sink.fallbackRelease) })
}
func (*completionSink) Enabled(context.Context, slog.Level) bool { return true }
func (sink *completionSink) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "1" {
		close(sink.workerStarted)
		<-sink.workerRelease
	}
	if record.Message == "3" && sink.holdFallback {
		close(sink.fallbackStarted)
		<-sink.fallbackRelease
	}
	sink.mu.Lock()
	sink.delivered = append(sink.delivered, record.Message)
	sink.mu.Unlock()
	return nil
}
func (sink *completionSink) WithAttrs([]slog.Attr) slog.Handler { return sink }
func (sink *completionSink) WithGroup(string) slog.Handler      { return sink }
func (sink *completionSink) messages() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]string(nil), sink.delivered...)
}
