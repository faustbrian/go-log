package capture_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/capture"
)

func TestHandlerCapturesEnabledRecords(t *testing.T) {
	t.Parallel()

	handler := capture.New(capture.WithLevel(slog.LevelInfo))
	if handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("Enabled(debug) = true, want false")
	}
	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled(info) = false, want true")
	}
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "accepted", 42)
	record.AddAttrs(slog.String("id", "one"))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if got := handler.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	captured, ok := handler.Last()
	if !ok {
		t.Fatal("Last() ok = false, want true")
	}
	if captured.Message != "accepted" || captured.PC != 42 {
		t.Fatalf("Last() = %+v, want original metadata", captured)
	}
	if got := attrValue(captured, "id"); got != "one" {
		t.Fatalf("Last() id = %v, want one", got)
	}
}

func TestDerivedHandlersApplyAttrsAndGroupsWithoutMutatingParent(t *testing.T) {
	t.Parallel()

	base := capture.New()
	derived := base.
		WithAttrs([]slog.Attr{slog.String("service", "api")}).
		WithGroup("request").
		WithAttrs([]slog.Attr{slog.String("bound", "yes")})
	emptyGroup := derived.WithGroup("")

	baseRecord := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "base", 0)
	baseRecord.AddAttrs(slog.String("id", "base"))
	if err := base.Handle(context.Background(), baseRecord); err != nil {
		t.Fatalf("base Handle() error = %v", err)
	}
	derivedRecord := slog.NewRecord(time.Unix(2, 0), slog.LevelInfo, "derived", 0)
	derivedRecord.AddAttrs(slog.String("id", "derived"))
	if err := emptyGroup.Handle(context.Background(), derivedRecord); err != nil {
		t.Fatalf("derived Handle() error = %v", err)
	}

	records := base.Records()
	if len(records) != 2 {
		t.Fatalf("Records() length = %d, want 2", len(records))
	}
	if got := attrValue(records[0], "service"); got != nil {
		t.Fatalf("base service = %v, want absent", got)
	}
	if got := attrValue(records[1], "service"); got != "api" {
		t.Fatalf("derived service = %v, want api", got)
	}
	if got := attrValue(records[1], "request.bound"); got != "yes" {
		t.Fatalf("derived request.bound = %v, want yes", got)
	}
	if got := attrValue(records[1], "request.id"); got != "derived" {
		t.Fatalf("derived request.id = %v, want derived", got)
	}
}

func TestSnapshotsAndResetDoNotShareMutableRecordStorage(t *testing.T) {
	t.Parallel()

	handler := capture.New()
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "first", 0)
	record.AddAttrs(
		slog.String("stable", "yes"),
		slog.Group("request", slog.String("id", "req-1")),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	snapshot := handler.Records()
	snapshot[0].AddAttrs(slog.String("external", "mutation"))
	mutateNestedKey(&snapshot[0], "request", "snapshot-mutation")
	last, ok := handler.Last()
	if !ok {
		t.Fatal("Last() ok = false, want true")
	}
	last.AddAttrs(slog.String("other", "mutation"))
	mutateNestedKey(&last, "request", "last-mutation")

	stored, _ := handler.Last()
	if got := attrValue(stored, "external"); got != nil {
		t.Fatalf("stored external = %v, want absent", got)
	}
	if got := attrValue(stored, "other"); got != nil {
		t.Fatalf("stored other = %v, want absent", got)
	}
	if got := nestedKey(stored, "request"); got != "id" {
		t.Fatalf("stored nested key = %q, want id", got)
	}

	handler.Reset()
	if handler.Len() != 0 {
		t.Fatalf("Len() after Reset = %d, want 0", handler.Len())
	}
	if _, ok := handler.Last(); ok {
		t.Fatal("Last() after Reset ok = true, want false")
	}
	if len(snapshot) != 1 {
		t.Fatalf("snapshot length after Reset = %d, want 1", len(snapshot))
	}
}

func TestHandlerIgnoresZeroAndEmptyGroupsAndInlinesEmptyKeys(t *testing.T) {
	t.Parallel()

	handler := capture.New()
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "effective", 0)
	record.AddAttrs(
		slog.Attr{},
		slog.Group("empty"),
		slog.Group("", slog.String("inline", "visible"), slog.Group("nested-empty")),
	)

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	captured, _ := handler.Last()
	if got := captured.NumAttrs(); got != 1 {
		t.Fatalf("NumAttrs() = %d, want 1", got)
	}
	if got := attrValue(captured, "inline"); got != "visible" {
		t.Fatalf("inline = %v, want visible", got)
	}
}

func TestWithAttrsIgnoresEmptyGroups(t *testing.T) {
	t.Parallel()

	handler := capture.New()
	derived := handler.WithAttrs([]slog.Attr{
		slog.Group("empty"),
		slog.Group("", slog.String("bound-inline", "visible")),
	})

	if err := derived.Handle(context.Background(), slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "bound", 0)); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	captured, _ := handler.Last()
	if got := captured.NumAttrs(); got != 1 {
		t.Fatalf("NumAttrs() = %d, want 1", got)
	}
	if got := attrValue(captured, "bound-inline"); got != "visible" {
		t.Fatalf("bound-inline = %v, want visible", got)
	}
}

func TestHandlerSupportsConcurrentWritersAndReaders(t *testing.T) {
	t.Parallel()

	handler := capture.New()
	const workers = 16
	const recordsPerWorker = 20
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := 0; index < recordsPerWorker; index++ {
				record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
				if err := handler.Handle(context.Background(), record); err != nil {
					t.Errorf("Handle() error = %v", err)
				}
				_ = handler.Len()
				_ = handler.Records()
			}
		}()
	}
	wait.Wait()

	if got, want := handler.Len(), workers*recordsPerWorker; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
}

func TestResetIsSafeDuringConcurrentAccess(t *testing.T) {
	t.Parallel()

	handler := capture.New()
	const workers = 8
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := 0; index < 50; index++ {
				record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
				if err := handler.Handle(context.Background(), record); err != nil {
					t.Errorf("Handle() error = %v", err)
				}
				_ = handler.Records()
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for index := 0; index < 50; index++ {
			handler.Reset()
			_ = handler.Len()
		}
	}()
	wait.Wait()

	if err := handler.Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "final", 0)); err != nil {
		t.Fatalf("final Handle() error = %v", err)
	}
	if handler.Len() == 0 {
		t.Fatal("Len() = 0 after final record, want at least one")
	}
}

func TestAssertionsReportMatchesAndFailures(t *testing.T) {
	t.Parallel()

	handler := capture.New()
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelWarn, "careful", 0)
	record.AddAttrs(
		slog.Int("attempt", 3),
		slog.Group("request", slog.String("id", "req-1")),
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	passing := &testingRecorder{}
	if !capture.AssertCount(passing, handler, 1) {
		t.Fatal("AssertCount() = false, want true")
	}
	if !capture.AssertMessage(passing, handler, "careful") {
		t.Fatal("AssertMessage() = false, want true")
	}
	if !capture.AssertAttr(passing, handler, "attempt", int64(3)) {
		t.Fatal("AssertAttr() = false, want true")
	}
	if !capture.AssertAttr(passing, handler, "request.id", "req-1") {
		t.Fatal("AssertAttr(group) = false, want true")
	}
	if len(passing.errors) != 0 || passing.helpers != 4 {
		t.Fatalf("passing recorder = %+v", passing)
	}

	failing := &testingRecorder{}
	if capture.AssertCount(failing, handler, 2) {
		t.Fatal("AssertCount() = true, want false")
	}
	if capture.AssertMessage(failing, handler, "missing") {
		t.Fatal("AssertMessage() = true, want false")
	}
	if capture.AssertAttr(failing, handler, "missing", "value") {
		t.Fatal("AssertAttr(missing) = true, want false")
	}
	if capture.AssertAttr(failing, handler, "attempt", int64(4)) {
		t.Fatal("AssertAttr(wrong) = true, want false")
	}
	if capture.AssertAttr(failing, handler, "attempt.child", "value") {
		t.Fatal("AssertAttr(non-group path) = true, want false")
	}
	if capture.AssertAttr(failing, handler, "request.missing", "value") {
		t.Fatal("AssertAttr(missing group child) = true, want false")
	}
	if len(failing.errors) != 6 || failing.helpers != 6 {
		t.Fatalf("failing recorder = %+v, want six failures", failing)
	}
}

type testingRecorder struct {
	helpers int
	errors  []string
}

func (recorder *testingRecorder) Helper() {
	recorder.helpers++
}

func (recorder *testingRecorder) Errorf(format string, _ ...any) {
	recorder.errors = append(recorder.errors, format)
}

func attrValue(record slog.Record, path string) any {
	var found any
	record.Attrs(func(attr slog.Attr) bool {
		if value, ok := findAttr(attr, path); ok {
			found = value
			return false
		}
		return true
	})

	return found
}

func findAttr(attr slog.Attr, path string) (any, bool) {
	if attr.Key == path {
		return attr.Value.Resolve().Any(), true
	}
	if attr.Value.Kind() != slog.KindGroup {
		return nil, false
	}
	prefix := attr.Key + "."
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return nil, false
	}
	for _, child := range attr.Value.Group() {
		if value, ok := findAttr(child, path[len(prefix):]); ok {
			return value, true
		}
	}

	return nil, false
}

func mutateNestedKey(record *slog.Record, group, key string) {
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == group {
			attr.Value.Group()[0].Key = key
			return false
		}
		return true
	})
}

func nestedKey(record slog.Record, group string) string {
	var key string
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == group {
			key = attr.Value.Group()[0].Key
			return false
		}
		return true
	})

	return key
}
