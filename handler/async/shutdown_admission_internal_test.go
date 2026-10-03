package async

import (
	"context"
	"errors"
	goruntime "runtime"
	"testing"
	"time"
)

func TestShutdownHonorsCallerCancellationAcrossHeldFallback(t *testing.T) {
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
		if err := <-fallback; err != nil {
			t.Error(err)
		}
	})
	awaitCompletionSignal(t, sink.fallbackStarted)
	sink.releaseWorker()
	shutdown := make(chan error, 1)
	go func() { shutdown <- handler.Shutdown(context.Background()) }()
	t.Cleanup(func() {
		sink.releaseFallback()
		if err := <-shutdown; err != nil {
			t.Error(err)
		}
	})
	awaitCompletionSignal(t, handler.runtime.workerDone)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handler.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("held fallback Shutdown = %v", err)
	}
	if err := handler.Handle(context.Background(), completionRecord("rejected")); !errors.Is(err, ErrClosed) {
		t.Errorf("post-shutdown admission = %v", err)
	}
	sink.releaseFallback()
	flushCompletion(t, handler)
	if got := handler.Stats(); got.Enqueued != 2 || got.SynchronousFallback != 1 || got.Delivered != 3 || got.Rejected != 1 || got.Lost() != 0 {
		t.Errorf("shutdown accounting = %#v", got)
	}
}

func TestShutdownRejectsCapacityWaitWithoutSequenceHole(t *testing.T) {
	sink := newCompletionSink(false)
	handler := completionHandler(t, sink, Block)
	submitCompletionRecord(t, handler, "1")
	awaitCompletionSignal(t, sink.workerStarted)
	submitCompletionRecord(t, handler, "2")
	pending := make(chan error, 1)
	go func() { pending <- handler.Handle(context.Background(), completionRecord("3")) }()
	t.Cleanup(func() {
		sink.releaseWorker()
		if err := <-pending; !errors.Is(err, ErrClosed) {
			t.Errorf("pending admission = %v", err)
		}
	})
	// Existing submission ownership confirms the public call is waiting for
	// capacity, rather than installing a synthetic locked runtime.
	setup, cancelSetup := context.WithTimeout(context.Background(), time.Second)
	defer cancelSetup()
	for len(handler.runtime.submitMu) == 0 {
		if setup.Err() != nil {
			t.Fatal("capacity submission did not reach admission")
		}
		goruntime.Gosched()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handler.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("held worker Shutdown = %v", err)
	}
	sink.releaseWorker()
	flushCompletion(t, handler)
	if got := handler.Stats(); got.Enqueued != 2 || got.Delivered != 2 || got.Rejected != 1 || got.Lost() != 0 {
		t.Errorf("shutdown accounting = %#v", got)
	}
}
