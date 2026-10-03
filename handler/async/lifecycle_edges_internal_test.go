package async

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

func TestEmptyDerivationsPreserveDeliveryAndLifecycle(t *testing.T) {
	sink := newCompletionSink(false)
	handler := completionHandler(t, sink, DropNewest)
	sink.releaseWorker()
	derived := handler.WithAttrs(nil).WithGroup("")
	if derived != handler {
		t.Fatal("empty derivations did not preserve the handler")
	}
	if err := derived.Handle(context.Background(), completionRecord("ordinary")); err != nil {
		t.Fatal(err)
	}
	flushCompletion(t, handler)
	if got := sink.messages(); len(got) != 1 || got[0] != "ordinary" {
		t.Fatalf("delivered messages = %v, want [ordinary]", got)
	}
	if got := handler.Stats(); got.Enqueued != 1 || got.Delivered != 1 || got.Lost() != 0 {
		t.Fatalf("empty derivation accounting = %#v", got)
	}
}

func TestExhaustedGroupDerivationRemainsDisabledAndSticky(t *testing.T) {
	sink := newCompletionSink(false)
	handler := completionHandler(t, sink, DropNewest)
	// Use the exact existing owner limit without constructing a nested payload.
	bounded := &Handler{next: handler.next, runtime: handler.runtime, usage: slogrecord.Usage{Groups: slogrecord.MaxRecordDepth}}
	derived := bounded.WithGroup("next")
	for _, candidate := range []*Handler{
		derived.(*Handler),
		derived.WithGroup("later").(*Handler),
		derived.WithAttrs(nil).(*Handler),
	} {
		if candidate.Enabled(context.Background(), 0) {
			t.Error("failed derivation remains enabled")
		}
		if err := candidate.Handle(context.Background(), completionRecord("rejected")); !errors.Is(err, slogrecord.ErrRecordLimit) {
			t.Errorf("failed derivation Handle = %v, want ErrRecordLimit", err)
		}
		if candidate.usage.Groups != slogrecord.MaxRecordDepth {
			t.Errorf("failed derivation changed retained usage to %d", candidate.usage.Groups)
		}
	}
	if got := handler.Stats(); got != (Stats{}) {
		t.Errorf("failed derivation changed delivery accounting: %#v", got)
	}
}

func TestAdmissionRechecksStoppedAcceptanceAfterTakingOwnership(t *testing.T) {
	rt := admissionEdgeRuntime()
	// Shutdown stores this state before closing its notification channel.
	rt.accepting.Store(false)
	handler := &Handler{next: newCompletionSink(false), runtime: rt}
	if err := handler.Handle(context.Background(), completionRecord("rejected")); !errors.Is(err, ErrClosed) {
		t.Fatalf("stopped admission = %v, want ErrClosed", err)
	}
	assertAdmissionEdgeRejected(t, handler)
}

func TestWaitingForAdmissionRejectsClosingAndTimeoutWithoutTakingOwnership(t *testing.T) {
	for _, closing := range []bool{true, false} {
		name, want := "timeout", ErrAdmissionTimeout
		if closing {
			name, want = "closing", ErrClosed
		}
		t.Run(name, func(t *testing.T) {
			rt := admissionEdgeRuntime()
			rt.accepting.Store(true)
			rt.lockSubmission()
			if closing {
				close(rt.closing)
			} else {
				rt.overflow = Block
				rt.admissionTimeout = 5 * time.Millisecond
			}
			handler := &Handler{next: newCompletionSink(false), runtime: rt}
			if err := handler.Handle(context.Background(), completionRecord("rejected")); !errors.Is(err, want) {
				t.Fatalf("waiting admission = %v, want %v", err, want)
			}
			if len(rt.submitMu) != 1 {
				t.Fatal("rejected waiter released another submission's ownership")
			}
			rt.unlockSubmission()
			assertAdmissionEdgeRejected(t, handler)
		})
	}
}

func TestDropOldestClosingRejectsWithoutEvictionOrSequenceHole(t *testing.T) {
	rt := admissionEdgeRuntime()
	rt.queue <- delivery{sequence: 1, record: completionRecord("retained")}
	rt.nextSeq = 1
	close(rt.closing)
	err := rt.enqueueDropOldest(context.Background(), newCompletionSink(false), completionRecord("rejected"), nil)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("closing DropOldest admission = %v, want ErrClosed", err)
	}
	if rt.nextSeq != 1 || len(rt.queue) != 1 {
		t.Fatalf("rejection changed accepted state: sequence=%d queued=%d", rt.nextSeq, len(rt.queue))
	}
	if retained := <-rt.queue; retained.record.Message != "retained" {
		t.Errorf("queued message = %q, want retained", retained.record.Message)
	}
	if got := (&Handler{runtime: rt}).Stats(); got.Rejected != 1 || got.Lost() != 0 || got.Enqueued != 0 {
		t.Errorf("closing DropOldest accounting = %#v", got)
	}
}

func admissionEdgeRuntime() *runtime {
	return &runtime{
		queue: make(chan delivery, 1), submitMu: make(chan struct{}, 1),
		closing: make(chan struct{}), delivery: context.Background(),
	}
}

func TestFallbackAdmissionCommitsOrReleasesOwnedReservation(t *testing.T) {
	for _, closing := range []bool{false, true} {
		name := "accepted"
		if closing {
			name = "closing"
		}
		t.Run(name, func(t *testing.T) {
			rt := admissionEdgeRuntime()
			rt.fallbackSlot = make(chan struct{}, 1)
			rt.fallbackCh = make(chan struct{})
			rt.progress = make(chan struct{})
			rt.lockSubmission()
			rt.fallbackSlot <- struct{}{}
			rt.nextSeq = 2
			rt.watermark = 1
			if closing {
				close(rt.closing)
			}
			sink := newCompletionSink(true)
			defer sink.releaseFallback()
			reserved := delivery{sequence: 2, ctx: context.Background(), next: sink, record: completionRecord("3")}
			var err error
			var result chan error
			if closing {
				err = rt.deliverReservedFallback(reserved)
			} else {
				result = make(chan error, 1)
				go func() { result <- rt.deliverReservedFallback(reserved) }()
				awaitCompletionSignal(t, sink.fallbackStarted)
			}
			if len(rt.submitMu) != 0 || rt.watermark != 1 || len(rt.completed) != 0 {
				t.Fatal("admission retained submission ownership or changed completion")
			}
			stats := (&Handler{runtime: rt}).Stats()
			if closing {
				if !errors.Is(err, ErrClosed) || rt.nextSeq != 1 || len(rt.fallbackSlot) != 0 || rt.fallbacks != 0 {
					t.Fatalf("rejected reservation: error=%v sequence=%d slots=%d fallbacks=%d", err, rt.nextSeq, len(rt.fallbackSlot), rt.fallbacks)
				}
				if stats != (Stats{Rejected: 1}) {
					t.Fatalf("rejected reservation accounting = %#v", stats)
				}
				if len(sink.messages()) != 0 {
					t.Fatal("rejected reservation reached downstream delivery")
				}
				return
			}
			if err != nil || rt.nextSeq != 2 || len(rt.fallbackSlot) != 1 || rt.fallbacks != 1 {
				t.Fatalf("accepted reservation: error=%v sequence=%d slots=%d fallbacks=%d", err, rt.nextSeq, len(rt.fallbackSlot), rt.fallbacks)
			}
			if stats != (Stats{SynchronousFallback: 1}) {
				t.Fatalf("accepted reservation accounting = %#v", stats)
			}
			// Completion, not admission, owns release of an accepted slot.
			sink.releaseFallback()
			if err = <-result; err != nil {
				t.Fatal(err)
			}
			if rt.fallbacks != 0 || len(rt.fallbackSlot) != 0 || rt.watermark != 2 || len(rt.completed) != 0 {
				t.Fatal("completed fallback retained ownership or left incomplete work")
			}
			if stats = (&Handler{runtime: rt}).Stats(); stats != (Stats{SynchronousFallback: 1, Delivered: 1}) {
				t.Fatalf("completed reservation accounting = %#v", stats)
			}
		})
	}
}

func assertAdmissionEdgeRejected(t *testing.T, handler *Handler) {
	t.Helper()
	if rt := handler.runtime; rt.nextSeq != 0 || len(rt.queue) != 0 || len(rt.submitMu) != 0 {
		t.Errorf("rejection retained admission ownership or accepted work: sequence=%d queued=%d owned=%d", rt.nextSeq, len(rt.queue), len(rt.submitMu))
	}
	if got := handler.Stats(); got.Rejected != 1 || got.Enqueued != 0 || got.Lost() != 0 {
		t.Errorf("rejected admission accounting = %#v", got)
	}
}
