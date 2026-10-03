package async

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	goruntime "runtime"
	"sync"
	"testing"
	"time"
)

func TestCanceledFlushDoesNotWaitForBlockedSubmission(t *testing.T) {
	sink := &flushAdmissionSink{started: make(chan struct{}), release: make(chan struct{})}
	handler, err := New(sink, Options{Capacity: 1, Overflow: Block, AdmissionTimeout: time.Second})
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
	<-sink.started
	if err := handler.Handle(context.Background(), record("second")); err != nil {
		t.Fatal(err)
	}
	pending := make(chan error, 1)
	go func() { pending <- handler.Handle(context.Background(), record("third")) }()
	setup, cancelSetup := context.WithTimeout(context.Background(), time.Second)
	defer cancelSetup()
	// Observe the actual pending public submission's existing lock ownership;
	// no production hook or synthetic locked state is introduced.
	for {
		select {
		case handler.runtime.submitMu <- struct{}{}:
			<-handler.runtime.submitMu
		default:
			goto submissionBlocked
		}
		if setup.Err() != nil {
			t.Fatal("pending submission did not acquire admission ownership")
		}
		goruntime.Gosched()
	}
submissionBlocked:
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	flushed := make(chan error, 1)
	go func() { flushed <- handler.Flush(ctx) }()
	returned := false
	select {
	case err := <-flushed:
		returned = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Flush error = %v, want canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("canceled Flush waited for a submission blocked on queue capacity")
	}
	release.Do(func() { close(sink.release) })
	if err := <-pending; err != nil {
		t.Fatal(err)
	}
	if !returned {
		<-flushed
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := handler.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !reflect.DeepEqual(sink.messages, []string{"first", "second", "third"}) {
		t.Fatalf("delivery order = %v", sink.messages)
	}
	if stats := handler.Stats(); stats.Enqueued != 3 || stats.Delivered != 3 || stats.Lost() != 0 {
		t.Fatalf("delivery accounting = %#v", stats)
	}
}

type flushAdmissionSink struct {
	started  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	messages []string
}

func (*flushAdmissionSink) Enabled(context.Context, slog.Level) bool { return true }
func (sink *flushAdmissionSink) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "first" {
		close(sink.started)
		<-sink.release
	}
	sink.mu.Lock()
	sink.messages = append(sink.messages, record.Message)
	sink.mu.Unlock()
	return nil
}
func (sink *flushAdmissionSink) WithAttrs([]slog.Attr) slog.Handler { return sink }
func (sink *flushAdmissionSink) WithGroup(string) slog.Handler      { return sink }
