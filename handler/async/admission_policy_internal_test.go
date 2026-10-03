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

func TestDefaultOverflowDoesNotWaitForCapacity(t *testing.T) {
	sink := &flushAdmissionSink{started: make(chan struct{}), release: make(chan struct{})}
	handler, err := New(sink, Options{Capacity: 1})
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	t.Cleanup(func() {
		release.Do(func() { close(sink.release) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Shutdown: %v", err)
		}
	})
	record := func(message string) slog.Record { return slog.NewRecord(time.Time{}, slog.LevelInfo, message, 0) }
	if err := handler.Handle(context.Background(), record("first")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("first delivery did not start")
	}
	if err := handler.Handle(context.Background(), record("second")); err != nil {
		t.Fatal(err)
	}
	pending := make(chan error, 1)
	go func() { pending <- handler.Handle(context.Background(), record("third")) }()
	returned := false
	select {
	case err := <-pending:
		returned = true
		if !errors.Is(err, ErrDropped) {
			t.Errorf("default overflow error = %v, want ErrDropped", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("default overflow waited for capacity")
	}
	release.Do(func() { close(sink.release) })
	if !returned {
		select {
		case <-pending:
		case <-time.After(time.Second):
			t.Fatal("pending submission did not finish after release")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !reflect.DeepEqual(sink.messages, []string{"first", "second"}) {
		t.Errorf("accepted delivery order = %v", sink.messages)
	}
	if stats := handler.Stats(); stats.Enqueued != 2 || stats.Delivered != 2 || stats.DroppedNewest != 1 || stats.Rejected != 0 {
		t.Errorf("default overflow accounting = %#v", stats)
	}
}

func TestWaitingPoliciesRequireAdmissionTimeout(t *testing.T) {
	for _, policy := range []OverflowPolicy{Block, SyncFallback} {
		for _, timeout := range []time.Duration{0, -time.Nanosecond} {
			name := map[OverflowPolicy]string{Block: "block", SyncFallback: "sync_fallback"}[policy] + "/" + timeout.String()
			t.Run(name, func(t *testing.T) {
				handler, err := New(&flushAdmissionSink{}, Options{Capacity: 1, Overflow: policy, AdmissionTimeout: timeout})
				if handler != nil {
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					defer cancel()
					if shutdownErr := handler.Shutdown(ctx); shutdownErr != nil {
						t.Errorf("cleanup Shutdown: %v", shutdownErr)
					}
				}
				if !errors.Is(err, ErrInvalidAdmissionTimeout) || handler != nil {
					t.Errorf("waiting policy without admission timeout: handler present = %t, error = %v", handler != nil, err)
				}
			})
		}
	}
}

func TestBlockAdmissionTimeoutRejectsBeforeAcceptance(t *testing.T) {
	sink := &flushAdmissionSink{started: make(chan struct{}), release: make(chan struct{})}
	handler, err := New(sink, Options{Capacity: 1, Overflow: Block, AdmissionTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	t.Cleanup(func() {
		release.Do(func() { close(sink.release) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Shutdown: %v", err)
		}
	})
	record := func(message string) slog.Record { return slog.NewRecord(time.Time{}, slog.LevelInfo, message, 0) }
	if err := handler.Handle(context.Background(), record("first")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sink.started:
	case <-time.After(time.Second):
		t.Fatal("first delivery did not start")
	}
	if err := handler.Handle(context.Background(), record("second")); err != nil {
		t.Fatal(err)
	}
	pending := make(chan error, 1)
	go func() { pending <- handler.Handle(context.Background(), record("third")) }()
	returned := false
	select {
	case err := <-pending:
		returned = true
		if !errors.Is(err, ErrAdmissionTimeout) {
			t.Errorf("blocked admission error = %v, want ErrAdmissionTimeout", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("blocked admission exceeded its configured timeout")
	}
	release.Do(func() { close(sink.release) })
	if !returned {
		select {
		case <-pending:
		case <-time.After(time.Second):
			t.Fatal("pending submission did not finish after release")
		}
	}
	// A later accepted record must still flush: rejection leaves no sequence hole.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), record("fourth")); err != nil {
		t.Fatal(err)
	}
	if err := handler.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !reflect.DeepEqual(sink.messages, []string{"first", "second", "fourth"}) {
		t.Errorf("accepted delivery order = %v", sink.messages)
	}
	if stats := handler.Stats(); stats.Enqueued != 3 || stats.Delivered != 3 || stats.Rejected != 1 || stats.Lost() != 0 {
		t.Errorf("admission timeout accounting = %#v", stats)
	}
}

func TestSyncFallbackAdmissionTimeoutRejectsBeforeAcceptance(t *testing.T) {
	sink := &admissionFallbackSink{started: make(chan string, 3), release: make(chan struct{})}
	handler, err := New(sink, Options{Capacity: 1, Overflow: SyncFallback, AdmissionTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	t.Cleanup(func() {
		release.Do(func() { close(sink.release) })
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := handler.Shutdown(ctx); err != nil {
			t.Errorf("cleanup Shutdown: %v", err)
		}
	})
	record := func(message string) slog.Record { return slog.NewRecord(time.Time{}, slog.LevelInfo, message, 0) }
	if err := handler.Handle(context.Background(), record("first")); err != nil {
		t.Fatal(err)
	}
	awaitStarted := func(want string) {
		t.Helper()
		select {
		case got := <-sink.started:
			if got != want {
				t.Fatalf("started = %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("delivery %q did not start", want)
		}
	}
	awaitStarted("first")
	if err := handler.Handle(context.Background(), record("second")); err != nil {
		t.Fatal(err)
	}
	fallback := make(chan error, 1)
	go func() { fallback <- handler.Handle(context.Background(), record("fallback")) }()
	awaitStarted("fallback")
	pending := make(chan error, 1)
	go func() { pending <- handler.Handle(context.Background(), record("rejected")) }()
	returned := false
	select {
	case err := <-pending:
		returned = true
		if !errors.Is(err, ErrAdmissionTimeout) {
			t.Errorf("fallback admission error = %v, want ErrAdmissionTimeout", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("fallback-slot admission exceeded its configured timeout")
	}
	release.Do(func() { close(sink.release) })
	select {
	case err := <-fallback:
		if err != nil {
			t.Errorf("accepted fallback: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted fallback did not finish after release")
	}
	if !returned {
		select {
		case <-pending:
		case <-time.After(time.Second):
			t.Fatal("pending fallback did not finish after release")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := handler.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := handler.Stats(); stats.Enqueued != 2 || stats.Delivered != 3 || stats.SynchronousFallback != 1 || stats.Rejected != 1 || stats.Lost() != 0 {
		t.Errorf("fallback timeout accounting = %#v", stats)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.delivered["rejected"] {
		t.Error("timed-out fallback reached downstream")
	}
}

type admissionFallbackSink struct {
	started   chan string
	release   chan struct{}
	mu        sync.Mutex
	delivered map[string]bool
}

func (*admissionFallbackSink) Enabled(context.Context, slog.Level) bool { return true }
func (sink *admissionFallbackSink) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "first" || record.Message == "fallback" {
		sink.started <- record.Message
		<-sink.release
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.delivered == nil {
		sink.delivered = make(map[string]bool)
	}
	sink.delivered[record.Message] = true
	return nil
}
func (sink *admissionFallbackSink) WithAttrs([]slog.Attr) slog.Handler { return sink }
func (sink *admissionFallbackSink) WithGroup(string) slog.Handler      { return sink }
