package slogrecord

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestCloneResolvedEnforcesExactStructuralBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("attribute count", func(t *testing.T) {
		t.Parallel()

		record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
		attrs := make([]slog.Attr, MaxRecordAttributes)
		for index := range attrs {
			attrs[index] = slog.Int("value", index)
		}
		record.AddAttrs(attrs...)
		if _, err := CloneResolved(record); err != nil {
			t.Fatalf("CloneResolved(maximum) error = %v", err)
		}
		record.AddAttrs(slog.Int("excess", 1))
		if _, err := CloneResolved(record); !errors.Is(err, ErrRecordLimit) {
			t.Fatalf("CloneResolved(excess) error = %v, want ErrRecordLimit", err)
		}
	})

	t.Run("group depth", func(t *testing.T) {
		t.Parallel()

		attr := slog.String("value", "safe")
		for depth := 1; depth < MaxRecordDepth; depth++ {
			attr = slog.Group("nested", attr)
		}
		record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
		record.AddAttrs(attr)
		if _, err := CloneResolved(record); err != nil {
			t.Fatalf("CloneResolved(maximum) error = %v", err)
		}
		record = slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
		record.AddAttrs(slog.Group("excess", attr))
		if _, err := CloneResolved(record); !errors.Is(err, ErrRecordLimit) {
			t.Fatalf("CloneResolved(excess) error = %v, want ErrRecordLimit", err)
		}
	})
}

func TestCloneResolvedRejectsImpossibleCountsBeforeResolvingValues(t *testing.T) {
	t.Parallel()

	t.Run("top-level attrs", func(t *testing.T) {
		t.Parallel()

		resolved := 0
		attrs := make([]slog.Attr, MaxRecordAttributes+1)
		attrs[0] = slog.Any("value", countingValuer{count: &resolved})
		if _, err := CloneResolvedAttrs(attrs); !errors.Is(err, ErrRecordLimit) {
			t.Fatalf("CloneResolvedAttrs() error = %v, want ErrRecordLimit", err)
		}
		if resolved != 0 {
			t.Fatalf("resolved values = %d, want zero", resolved)
		}
	})

	t.Run("group children", func(t *testing.T) {
		t.Parallel()

		resolved := 0
		children := make([]slog.Attr, MaxRecordAttributes)
		children[0] = slog.Any("value", countingValuer{count: &resolved})
		record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
		record.AddAttrs(slog.Attr{Key: "oversized", Value: slog.GroupValue(children...)})
		if _, err := CloneResolved(record); !errors.Is(err, ErrRecordLimit) {
			t.Fatalf("CloneResolved() error = %v, want ErrRecordLimit", err)
		}
		if resolved != 0 {
			t.Fatalf("resolved values = %d, want zero", resolved)
		}
	})
}

func TestUsageBoundsDerivedAttrsAndRecordTogether(t *testing.T) {
	t.Parallel()

	first := make([]slog.Attr, 600)
	second := make([]slog.Attr, 424)
	for index := range first {
		first[index] = slog.Int("first", index)
	}
	for index := range second {
		second[index] = slog.Int("second", index)
	}
	_, usage, err := CloneRawAttrsWithUsage(first, Usage{})
	if err != nil {
		t.Fatalf("first CloneRawAttrsWithUsage() error = %v", err)
	}
	_, usage, err = CloneRawAttrsWithUsage(second, usage)
	if err != nil {
		t.Fatalf("second CloneRawAttrsWithUsage() error = %v", err)
	}
	record := slog.NewRecord(time.Unix(1, 0), slog.LevelInfo, "message", 0)
	record.AddAttrs(slog.String("excess", "value"))
	if _, err := CloneRawWithUsage(record, usage); !errors.Is(err, ErrRecordLimit) {
		t.Fatalf("CloneRawWithUsage() error = %v, want ErrRecordLimit", err)
	}
}

func TestUsageBoundsDerivedGroupChains(t *testing.T) {
	t.Parallel()

	usage := Usage{}
	var err error
	for range MaxRecordDepth {
		usage, err = usage.WithGroup("group")
		if err != nil {
			t.Fatalf("WithGroup(maximum) error = %v", err)
		}
	}
	if _, err := usage.WithGroup("excess"); !errors.Is(err, ErrRecordLimit) {
		t.Fatalf("WithGroup(excess) error = %v, want ErrRecordLimit", err)
	}
	if _, _, err := CloneRawAttrsWithUsage([]slog.Attr{slog.String("value", "safe")}, usage); !errors.Is(err, ErrRecordLimit) {
		t.Fatalf("CloneRawAttrsWithUsage(deep) error = %v, want ErrRecordLimit", err)
	}
}

type countingValuer struct{ count *int }

func (valuer countingValuer) LogValue() slog.Value {
	*valuer.count++
	return slog.StringValue("value")
}
