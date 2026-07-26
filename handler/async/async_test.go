package async_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/async"
	"github.com/faustbrian/golib/pkg/log/handler/capture"
)

func TestNewValidatesOptions(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		next    slog.Handler
		options async.Options
		want    error
	}{
		"nil handler": {
			next:    nil,
			options: async.Options{Capacity: 1},
			want:    async.ErrNilHandler,
		},
		"zero capacity": {
			next:    capture.New(),
			options: async.Options{Capacity: 0},
			want:    async.ErrInvalidCapacity,
		},
		"negative capacity": {
			next:    capture.New(),
			options: async.Options{Capacity: -1},
			want:    async.ErrInvalidCapacity,
		},
		"invalid policy": {
			next:    capture.New(),
			options: async.Options{Capacity: 1, Overflow: async.OverflowPolicy(255)},
			want:    async.ErrInvalidPolicy,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			handler, err := async.New(test.next, test.options)
			if handler != nil {
				t.Fatalf("New() handler = %v, want nil", handler)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestBlockWaitsForCapacityAndIgnoresCancellation(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.Block})
	t.Cleanup(func() { shutdown(t, handler) })

	handle(t, handler, "block")
	<-sink.firstStarted
	handle(t, handler, "queued")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan error, 1)
	go func() {
		result <- handler.Handle(ctx, newRecord("accepted"))
	}()
	var early error
	returnedEarly := false
	select {
	case err := <-result:
		early = err
		returnedEarly = true
	case <-time.After(20 * time.Millisecond):
	}
	close(sink.releaseFirst)
	if returnedEarly {
		t.Fatalf("Handle() returned before capacity was available: %v", early)
	}
	if err := <-result; err != nil {
		t.Fatalf("Handle() error after capacity became available = %v", err)
	}
	flush(t, handler)
	if got := handler.Stats(); got.Rejected != 0 || got.Enqueued != 3 {
		t.Fatalf("Stats() = %+v, want three enqueued and none rejected", got)
	}
	if got := sink.messages(); !equalStrings(got, []string{"block", "queued", "accepted"}) {
		t.Fatalf("messages = %v, want [block queued accepted]", got)
	}
}

func TestDeliveryContextPreservesValuesWithoutCancellation(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.Block})
	t.Cleanup(func() { shutdown(t, handler) })
	handle(t, handler, "block")
	<-sink.firstStarted
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), deliveryContextKey{}, "value"))
	if err := handler.Handle(ctx, newRecord("queued")); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	cancel()
	close(sink.releaseFirst)
	flush(t, handler)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if got := sink.contextErrors["queued"]; got != nil {
		t.Fatalf("downstream context error = %v, want nil", got)
	}
	if got := sink.contextValues["queued"]; got != "value" {
		t.Fatalf("downstream context value = %v, want value", got)
	}
}

func TestDropNewestRejectsCurrentRecord(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.DropNewest})
	t.Cleanup(func() { shutdown(t, handler) })

	handle(t, handler, "block")
	<-sink.firstStarted
	handle(t, handler, "queued")
	err := handler.Handle(context.Background(), newRecord("newest"))

	if !errors.Is(err, async.ErrDropped) {
		t.Fatalf("Handle() error = %v, want ErrDropped", err)
	}
	if got := handler.Stats(); got.DroppedNewest != 1 || got.Lost() != 1 {
		t.Fatalf("Stats() = %+v, want one newest loss", got)
	}
	close(sink.releaseFirst)
	flush(t, handler)
	if got := sink.messages(); !equalStrings(got, []string{"block", "queued"}) {
		t.Fatalf("messages = %v, want [block queued]", got)
	}
}

func TestDropOldestEvictsQueuedRecord(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.DropOldest})
	t.Cleanup(func() { shutdown(t, handler) })

	handle(t, handler, "block")
	<-sink.firstStarted
	handle(t, handler, "oldest")
	handle(t, handler, "newest")

	if got := handler.Stats(); got.DroppedOldest != 1 || got.Enqueued != 3 || got.Lost() != 1 {
		t.Fatalf("Stats() = %+v, want one oldest loss", got)
	}
	close(sink.releaseFirst)
	flush(t, handler)
	if got := sink.messages(); !equalStrings(got, []string{"block", "newest"}) {
		t.Fatalf("messages = %v, want [block newest]", got)
	}
}

func TestSynchronousFallbackDeliversWhenQueueIsFull(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.SyncFallback})
	t.Cleanup(func() { shutdown(t, handler) })

	handle(t, handler, "block")
	<-sink.firstStarted
	handle(t, handler, "queued")
	handle(t, handler, "fallback")

	stats := handler.Stats()
	if stats.SynchronousFallback != 1 || stats.Delivered != 1 || stats.Enqueued != 2 {
		t.Fatalf("Stats() = %+v, want one synchronous delivery", stats)
	}
	close(sink.releaseFirst)
	flush(t, handler)
	if got := sink.messages(); !equalStrings(got, []string{"fallback", "block", "queued"}) {
		t.Fatalf("messages = %v, want [fallback block queued]", got)
	}
}

func TestFlushHonorsDeadlineAndThenCompletes(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.Block})
	t.Cleanup(func() { shutdown(t, handler) })
	handle(t, handler, "block")
	<-sink.firstStarted

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := handler.Flush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Flush() error = %v, want deadline exceeded", err)
	}
	close(sink.releaseFirst)
	flush(t, handler)
}

func TestShutdownIsDeadlineAwareRepeatableAndClosesHandler(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.Block})
	handle(t, handler, "block")
	<-sink.firstStarted

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := handler.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want deadline exceeded", err)
	}
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled() during shutdown = true, want false")
	}
	if err := handler.Handle(context.Background(), newRecord("late")); !errors.Is(err, async.ErrClosed) {
		t.Fatalf("Handle() after shutdown error = %v, want ErrClosed", err)
	}
	close(sink.releaseFirst)
	shutdown(t, handler)
	shutdown(t, handler)
	if got := handler.Stats(); got.Rejected != 1 {
		t.Fatalf("Stats() = %+v, want one rejected record", got)
	}
}

func TestRecordsAndLogValuesAreFrozenBeforeRetention(t *testing.T) {
	t.Parallel()

	sink := newControlledHandler()
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.Block})
	t.Cleanup(func() { shutdown(t, handler) })
	handle(t, handler, "block")
	<-sink.firstStarted
	value := &mutableValuer{value: "before"}
	record := newRecord("queued")
	record.AddAttrs(slog.Any("state", value), slog.Group("nested", slog.Any("state", value)))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	value.value = "after"
	record.AddAttrs(slog.String("external", "mutation"))
	close(sink.releaseFirst)
	flush(t, handler)

	records := sink.recordsSnapshot()
	queued := records[1]
	if got := valuesAt(queued, "state"); !equalStrings(got, []string{"before"}) {
		t.Fatalf("state values = %v, want [before]", got)
	}
	if got := valuesAt(queued, "nested.state"); !equalStrings(got, []string{"before"}) {
		t.Fatalf("nested.state values = %v, want [before]", got)
	}
	if got := valuesAt(queued, "external"); len(got) != 0 {
		t.Fatalf("external values = %v, want none", got)
	}
}

func TestDeliveryErrorsAreCountedAndReportedWithoutStoppingWorker(t *testing.T) {
	t.Parallel()

	sink := &failingHandler{}
	var callbacks atomic.Uint64
	handler := mustNew(t, sink, async.Options{
		Capacity: 2,
		Overflow: async.Block,
		OnError: func(error) {
			callbacks.Add(1)
			panic("callback panic must not stop worker")
		},
	})
	handle(t, handler, "first")
	handle(t, handler, "second")
	flush(t, handler)
	shutdown(t, handler)

	if got := handler.Stats(); got.Failed != 2 || got.Delivered != 0 {
		t.Fatalf("Stats() = %+v, want two failures", got)
	}
	if got := callbacks.Load(); got != 2 {
		t.Fatalf("error callbacks = %d, want 2", got)
	}
}

func TestDeliveryErrorWithoutCallbackIsStillCounted(t *testing.T) {
	t.Parallel()

	handler := mustNew(t, &failingHandler{}, async.Options{Capacity: 1, Overflow: async.Block})
	handle(t, handler, "failed")
	flush(t, handler)
	shutdown(t, handler)

	if got := handler.Stats(); got.Failed != 1 {
		t.Fatalf("Stats() = %+v, want one failure", got)
	}
}

func TestFallbackReturnsDeliveryError(t *testing.T) {
	t.Parallel()

	want := errors.New("fallback failed")
	sink := newControlledHandler()
	sink.errFor = map[string]error{"fallback": want}
	handler := mustNew(t, sink, async.Options{Capacity: 1, Overflow: async.SyncFallback})
	t.Cleanup(func() { closeOnce(sink.releaseFirst); shutdown(t, handler) })
	handle(t, handler, "block")
	<-sink.firstStarted
	handle(t, handler, "queued")

	if err := handler.Handle(context.Background(), newRecord("fallback")); !errors.Is(err, want) {
		t.Fatalf("Handle() error = %v, want %v", err, want)
	}
	if got := handler.Stats(); got.Failed != 1 || got.SynchronousFallback != 1 {
		t.Fatalf("Stats() = %+v, want one failed fallback", got)
	}
}

func TestDerivedHandlersPreserveAttrsAndGroups(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, async.Options{Capacity: 2, Overflow: async.Block})
	bound := &mutableValuer{value: "before"}
	derived := handler.
		WithAttrs([]slog.Attr{slog.String("service", "api"), slog.Any("bound", bound)}).
		WithGroup("request")
	bound.value = "after"
	record := newRecord("message")
	record.AddAttrs(slog.String("id", "req-1"))
	if err := derived.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	flush(t, handler)
	shutdown(t, handler)

	if !capture.AssertAttr(t, sink, "service", "api") {
		t.FailNow()
	}
	if !capture.AssertAttr(t, sink, "request.id", "req-1") {
		t.FailNow()
	}
	if !capture.AssertAttr(t, sink, "bound", "before") {
		t.FailNow()
	}
}

func TestConcurrentHandlingFlushAndStatsAreRaceSafe(t *testing.T) {
	t.Parallel()

	handler := mustNew(t, capture.New(), async.Options{Capacity: 200, Overflow: async.Block})
	const count = 200
	var wait sync.WaitGroup
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := handler.Handle(context.Background(), newRecord("message")); err != nil {
				t.Errorf("Handle() error = %v", err)
			}
			_ = handler.Stats()
		}()
	}
	wait.Wait()
	flush(t, handler)
	shutdown(t, handler)

	if got := handler.Stats(); got.Enqueued != count || got.Delivered != count || got.Lost() != 0 {
		t.Fatalf("Stats() = %+v, want %d delivered", got, count)
	}
}

func TestConcurrentShutdownCallersShareOneDrain(t *testing.T) {
	t.Parallel()

	handler := mustNew(t, capture.New(), async.Options{Capacity: 64, Overflow: async.Block})
	for index := 0; index < 50; index++ {
		handle(t, handler, "message")
	}

	const callers = 16
	results := make(chan error, callers)
	var wait sync.WaitGroup
	for index := 0; index < callers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			results <- handler.Shutdown(ctx)
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	}

	if got := handler.Stats(); got.Delivered != 50 {
		t.Fatalf("Stats() = %+v, want 50 delivered", got)
	}
}

func TestCompletedShutdownWinsCanceledCallerContext(t *testing.T) {
	t.Parallel()

	handler := mustNew(t, capture.New(), async.Options{Capacity: 1, Overflow: async.Block})
	shutdown(t, handler)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for attempt := 0; attempt < 100; attempt++ {
		if err := handler.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown() attempt %d error = %v, want nil", attempt, err)
		}
	}
}

func mustNew(t *testing.T, next slog.Handler, options async.Options) *async.Handler {
	t.Helper()
	handler, err := async.New(next, options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}

func handle(t *testing.T, handler slog.Handler, message string) {
	t.Helper()
	if err := handler.Handle(context.Background(), newRecord(message)); err != nil {
		t.Fatalf("Handle(%q) error = %v", message, err)
	}
}

func flush(t *testing.T, handler *async.Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Flush(ctx); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
}

func shutdown(t *testing.T, handler *async.Handler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func newRecord(message string) slog.Record {
	return slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, message, 0)
}

type controlledHandler struct {
	firstStarted  chan struct{}
	releaseFirst  chan struct{}
	startOnce     sync.Once
	mu            sync.Mutex
	records       []slog.Record
	errFor        map[string]error
	contextErrors map[string]error
	contextValues map[string]any
}

type deliveryContextKey struct{}

func newControlledHandler() *controlledHandler {
	return &controlledHandler{
		firstStarted:  make(chan struct{}),
		releaseFirst:  make(chan struct{}),
		contextErrors: make(map[string]error),
		contextValues: make(map[string]any),
	}
}

func (handler *controlledHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *controlledHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "block" {
		handler.startOnce.Do(func() { close(handler.firstStarted) })
		<-handler.releaseFirst
	}
	handler.mu.Lock()
	handler.records = append(handler.records, record.Clone())
	handler.contextErrors[record.Message] = ctx.Err()
	if value := ctx.Value(deliveryContextKey{}); value != nil {
		handler.contextValues[record.Message] = value
	}
	err := handler.errFor[record.Message]
	handler.mu.Unlock()
	return err
}

func (handler *controlledHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &boundHandler{next: handler, attrs: append([]slog.Attr(nil), attrs...)}
}

func (handler *controlledHandler) WithGroup(name string) slog.Handler {
	return &boundHandler{next: handler, groups: []string{name}}
}

func (handler *controlledHandler) messages() []string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	messages := make([]string, len(handler.records))
	for index, record := range handler.records {
		messages[index] = record.Message
	}
	return messages
}

func (handler *controlledHandler) recordsSnapshot() []slog.Record {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	records := make([]slog.Record, len(handler.records))
	for index, record := range handler.records {
		records[index] = record.Clone()
	}
	return records
}

type boundHandler struct {
	next   *controlledHandler
	attrs  []slog.Attr
	groups []string
}

func (handler *boundHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *boundHandler) Handle(ctx context.Context, record slog.Record) error {
	record.AddAttrs(handler.attrs...)
	if len(handler.groups) > 0 {
		var attrs []slog.Attr
		record.Attrs(func(attr slog.Attr) bool { attrs = append(attrs, attr); return true })
		record = slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
		record.AddAttrs(slog.Attr{Key: handler.groups[0], Value: slog.GroupValue(attrs...)})
	}
	return handler.next.Handle(ctx, record)
}

func (handler *boundHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	derived := *handler
	derived.attrs = append(append([]slog.Attr(nil), handler.attrs...), attrs...)
	return &derived
}

func (handler *boundHandler) WithGroup(name string) slog.Handler {
	derived := *handler
	derived.groups = append(append([]string(nil), handler.groups...), name)
	return &derived
}

type failingHandler struct{}

func (*failingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (*failingHandler) Handle(context.Context, slog.Record) error {
	return errors.New("delivery failed")
}
func (handler *failingHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *failingHandler) WithGroup(string) slog.Handler      { return handler }

type mutableValuer struct {
	value string
}

func (valuer *mutableValuer) LogValue() slog.Value {
	return slog.StringValue(valuer.value)
}

func valuesAt(record slog.Record, path string) []string {
	var values []string
	var visit func(slog.Attr, string)
	visit = func(attr slog.Attr, prefix string) {
		current := attr.Key
		if prefix != "" {
			current = prefix + "." + attr.Key
		}
		value := attr.Value.Resolve()
		if value.Kind() == slog.KindGroup {
			for _, child := range value.Group() {
				visit(child, current)
			}
			return
		}
		if current == path {
			values = append(values, value.String())
		}
	}
	record.Attrs(func(attr slog.Attr) bool { visit(attr, ""); return true })
	return values
}

func equalStrings(first, second []string) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func closeOnce(channel chan struct{}) {
	select {
	case <-channel:
	default:
		close(channel)
	}
}
