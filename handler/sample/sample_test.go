package sample_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/log/handler/capture"
	"github.com/faustbrian/golib/pkg/log/handler/sample"
)

func TestNewRejectsInvalidDependencies(t *testing.T) {
	t.Parallel()

	sampler := sample.Sampler(func(context.Context, slog.Record) bool { return true })
	tests := map[string]struct {
		next    slog.Handler
		sampler sample.Sampler
		want    error
	}{
		"nil handler": {next: nil, sampler: sampler, want: sample.ErrNilHandler},
		"nil sampler": {next: capture.New(), sampler: nil, want: sample.ErrNilSampler},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			handler, err := sample.New(test.next, test.sampler)
			if handler != nil {
				t.Fatalf("New() handler = %v, want nil", handler)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("New() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestEveryKeepsFirstAndEachNthRecord(t *testing.T) {
	t.Parallel()

	sampler, err := sample.Every(3)
	if err != nil {
		t.Fatalf("Every() error = %v", err)
	}
	wants := []bool{true, false, false, true, false, false, true}
	for index, want := range wants {
		record := slog.NewRecord(time.Unix(int64(index), 0), slog.LevelInfo, "message", 0)
		if got := sampler(context.Background(), record); got != want {
			t.Errorf("sample %d = %v, want %v", index, got, want)
		}
	}

	if sampler, err := sample.Every(0); sampler != nil || !errors.Is(err, sample.ErrInvalidEvery) {
		t.Fatalf("Every(0) = (%v, %v), want nil ErrInvalidEvery", sampler, err)
	}
}

func TestDeterministicValidatesConfiguration(t *testing.T) {
	t.Parallel()

	key := func(_ context.Context, record slog.Record) string { return record.Message }
	tests := map[string]struct {
		rate float64
		key  sample.KeyFunc
		want error
	}{
		"negative":  {rate: -0.1, key: key, want: sample.ErrInvalidRate},
		"above one": {rate: 1.1, key: key, want: sample.ErrInvalidRate},
		"nan":       {rate: 0.0 / zero(), key: key, want: sample.ErrInvalidRate},
		"nil key":   {rate: 0.5, key: nil, want: sample.ErrNilKey},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sampler, err := sample.Deterministic(test.rate, test.key)
			if sampler != nil {
				t.Fatalf("Deterministic() sampler = %v, want nil", sampler)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("Deterministic() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDeterministicIsStableAndHonorsBoundaryRates(t *testing.T) {
	t.Parallel()

	key := func(_ context.Context, record slog.Record) string { return record.Message }
	none, err := sample.Deterministic(0, key)
	if err != nil {
		t.Fatalf("Deterministic(0) error = %v", err)
	}
	all, err := sample.Deterministic(1, key)
	if err != nil {
		t.Fatalf("Deterministic(1) error = %v", err)
	}
	half, err := sample.Deterministic(0.5, key)
	if err != nil {
		t.Fatalf("Deterministic(0.5) error = %v", err)
	}

	var kept, dropped bool
	for index := 0; index < 100; index++ {
		record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, fmt.Sprintf("key-%d", index), 0)
		if none(context.Background(), record) {
			t.Fatal("zero-rate sampler kept a record")
		}
		if !all(context.Background(), record) {
			t.Fatal("full-rate sampler dropped a record")
		}
		first := half(context.Background(), record)
		if second := half(context.Background(), record); first != second {
			t.Fatalf("same key decisions differ: %v and %v", first, second)
		}
		kept = kept || first
		dropped = dropped || !first
	}
	if !kept || !dropped {
		t.Fatalf("half-rate outcomes kept=%v dropped=%v, want both", kept, dropped)
	}
}

func TestHandlerDelegatesEnabledTracksStatsAndPreservesErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("sink failed")
	sink := &stubHandler{enabled: true, err: want}
	decisions := []bool{false, true}
	index := 0
	handler := mustNew(t, sink, func(context.Context, slog.Record) bool {
		decision := decisions[index]
		index++
		return decision
	})
	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("Enabled() = false, want true")
	}
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("dropped Handle() error = %v", err)
	}
	if err := handler.Handle(context.Background(), record); !errors.Is(err, want) {
		t.Fatalf("kept Handle() error = %v, want %v", err, want)
	}

	if got := handler.Stats(); got != (sample.Stats{Kept: 1, Dropped: 1}) {
		t.Fatalf("Stats() = %+v, want one kept and dropped", got)
	}
}

func TestSamplerReceivesIndependentRecord(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, func(_ context.Context, record slog.Record) bool {
		record.AddAttrs(slog.String("sampler", "mutation"))
		return true
	})
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.String("original", "yes"))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	captured, _ := sink.Last()
	if hasAttr(captured, "sampler") {
		t.Fatal("downstream record contains sampler mutation")
	}
	if !hasAttr(captured, "original") {
		t.Fatal("downstream record lost original attribute")
	}
}

func TestSamplerReceivesIndependentNestedGroups(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, func(_ context.Context, record slog.Record) bool {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Value.Kind() == slog.KindGroup {
				attr.Value.Group()[0].Key = "mutated"
			}
			return true
		})
		return true
	})
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.Group("request", slog.String("id", "req-1")))

	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	captured, _ := sink.Last()
	if !capture.AssertAttr(t, sink, "request.id", "req-1") {
		t.Fatalf("captured record = %+v, want original nested key", captured)
	}
}

func TestDerivedHandlersPreserveDownstreamAttrsAndGroups(t *testing.T) {
	t.Parallel()

	sink := capture.New()
	handler := mustNew(t, sink, func(context.Context, slog.Record) bool { return true })
	derived := handler.
		WithAttrs([]slog.Attr{slog.String("service", "api")}).
		WithGroup("request")
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.String("id", "req-1"))

	if err := derived.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if !capture.AssertAttr(t, sink, "service", "api") {
		t.FailNow()
	}
	if !capture.AssertAttr(t, sink, "request.id", "req-1") {
		t.FailNow()
	}
}

func TestEveryAndStatsAreRaceSafe(t *testing.T) {
	t.Parallel()

	sampler, err := sample.Every(4)
	if err != nil {
		t.Fatalf("Every() error = %v", err)
	}
	handler := mustNew(t, capture.New(), sampler)
	const records = 400
	var wait sync.WaitGroup
	for index := 0; index < records; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			record := slog.NewRecord(time.Now(), slog.LevelInfo, "message", 0)
			if err := handler.Handle(context.Background(), record); err != nil {
				t.Errorf("Handle() error = %v", err)
			}
			_ = handler.Stats()
		}()
	}
	wait.Wait()

	if got := handler.Stats(); got != (sample.Stats{Kept: 100, Dropped: 300}) {
		t.Fatalf("Stats() = %+v, want 100 kept and 300 dropped", got)
	}
}

func mustNew(t *testing.T, next slog.Handler, sampler sample.Sampler) *sample.Handler {
	t.Helper()

	handler, err := sample.New(next, sampler)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return handler
}

type stubHandler struct {
	enabled bool
	err     error
}

func (handler *stubHandler) Enabled(context.Context, slog.Level) bool  { return handler.enabled }
func (handler *stubHandler) Handle(context.Context, slog.Record) error { return handler.err }
func (handler *stubHandler) WithAttrs([]slog.Attr) slog.Handler        { return handler }
func (handler *stubHandler) WithGroup(string) slog.Handler             { return handler }

func hasAttr(record slog.Record, key string) bool {
	found := false
	record.Attrs(func(attr slog.Attr) bool {
		found = attr.Key == key
		return !found
	})

	return found
}

func zero() float64 { return 0 }
