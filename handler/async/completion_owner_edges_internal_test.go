package async

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestCompletionOwnerFallbackTimeoutPreservesAcceptedReservations(t *testing.T) {
	rt := admissionEdgeRuntime()
	rt.nextSeq = 2
	rt.queue <- delivery{sequence: 1, record: completionRecord("retained")}
	rt.fallbackSlot = make(chan struct{}, 1)
	rt.fallbackSlot <- struct{}{}
	rt.fallbacks = 1
	rt.stats.enqueued.Store(1)
	rt.stats.synchronousFallback.Store(1)
	rt.lockSubmission()
	deadline := make(chan time.Time)
	close(deadline)
	sink := newCompletionSink(false)
	err := rt.enqueueSyncFallback(context.Background(), sink, completionRecord("rejected"), deadline)
	if !errors.Is(err, ErrAdmissionTimeout) {
		t.Fatalf("fallback admission = %v, want ErrAdmissionTimeout", err)
	}
	if rt.nextSeq != 2 || len(rt.submitMu) != 0 || len(rt.queue) != 1 || len(rt.fallbackSlot) != 1 || rt.fallbacks != 1 {
		t.Fatalf("timeout changed accepted reservation/ownership: sequence=%d submission=%d queue=%d slot=%d fallbacks=%d", rt.nextSeq, len(rt.submitMu), len(rt.queue), len(rt.fallbackSlot), rt.fallbacks)
	}
	retained := <-rt.queue
	if retained.sequence != 1 || retained.record.Message != "retained" {
		t.Fatalf("timeout changed queued delivery: %#v", retained)
	}
	if got := (&Handler{runtime: rt}).Stats(); got != (Stats{Enqueued: 1, SynchronousFallback: 1, Rejected: 1}) {
		t.Fatalf("timeout accounting = %#v", got)
	}
	if len(sink.messages()) != 0 || rt.watermark != 0 || len(rt.completed) != 0 {
		t.Fatal("rejected admission reached downstream or changed completion")
	}
}

func TestCompletionOwnerCoalescesAdjacencyBridgesAndOnlyCompletedPrefixes(t *testing.T) {
	type step struct {
		sequence  uint64
		watermark uint64
		intervals []completionInterval
	}
	tests := map[string][]step{
		"left adjacency": {
			{sequence: 4, intervals: []completionInterval{{4, 4}}},
			{sequence: 3, intervals: []completionInterval{{3, 4}}},
		},
		"right adjacency": {
			{sequence: 3, intervals: []completionInterval{{3, 3}}},
			{sequence: 4, intervals: []completionInterval{{3, 4}}},
		},
		"middle bridge preserves earlier interval": {
			{sequence: 3, intervals: []completionInterval{{3, 3}}},
			{sequence: 5, intervals: []completionInterval{{3, 3}, {5, 5}}},
			{sequence: 7, intervals: []completionInterval{{3, 3}, {5, 5}, {7, 7}}},
			{sequence: 6, intervals: []completionInterval{{3, 3}, {5, 7}}},
		},
		"bridge preserves following interval": {
			{sequence: 3, intervals: []completionInterval{{3, 3}}},
			{sequence: 5, intervals: []completionInterval{{3, 3}, {5, 5}}},
			{sequence: 7, intervals: []completionInterval{{3, 3}, {5, 5}, {7, 7}}},
			{sequence: 4, intervals: []completionInterval{{3, 5}, {7, 7}}},
		},
		"gaps duplicates and full prefix": {
			{sequence: 2, intervals: []completionInterval{{2, 2}}},
			{sequence: 4, intervals: []completionInterval{{2, 2}, {4, 4}}},
			{sequence: 4, intervals: []completionInterval{{2, 2}, {4, 4}}},
			{sequence: 3, intervals: []completionInterval{{2, 4}}},
			{sequence: 1, watermark: 4},
			{sequence: 3, watermark: 4},
			{sequence: 5, watermark: 5},
		},
	}
	for name, steps := range tests {
		t.Run(name, func(t *testing.T) {
			rt := &runtime{progress: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			for _, step := range steps {
				progress := rt.progress
				rt.markComplete(step.sequence)
				if rt.watermark != step.watermark || !slices.Equal(rt.completed, step.intervals) {
					t.Fatalf("completion %d: watermark=%d intervals=%v, want %d/%v", step.sequence, rt.watermark, rt.completed, step.watermark, step.intervals)
				}
				select {
				case <-progress:
				default:
					t.Fatal("completion did not notify existing waiters")
				}
				select {
				case <-rt.progress:
					t.Fatal("completion left the next progress channel closed")
				default:
				}
				if err := rt.wait(context.Background(), step.watermark); err != nil {
					t.Fatalf("completed prefix wait: %v", err)
				}
				if err := rt.wait(ctx, step.watermark+1); !errors.Is(err, context.Canceled) {
					t.Fatalf("incomplete next prefix wait = %v, want canceled", err)
				}
			}
		})
	}
}
