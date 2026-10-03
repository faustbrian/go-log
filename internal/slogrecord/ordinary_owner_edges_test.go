package slogrecord

import (
	"errors"
	"log/slog"
	"testing"
	"time"
)

func TestOrdinaryRecordWrappersPreserveIndependentGroupValues(t *testing.T) {
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "ordinary", 0)
	record.AddAttrs(slog.Group("group", slog.String("value", "original")))
	for _, clone := range []func(slog.Record) (slog.Record, error){CloneRaw, CloneResolved} {
		owned, err := clone(record)
		if err != nil {
			t.Fatal(err)
		}
		owned.Attrs(func(attr slog.Attr) bool {
			if children := attr.Value.Group(); len(children) != 1 || children[0].Value.String() != "original" {
				t.Fatalf("cloned group = %v", children)
			} else {
				children[0] = slog.String("value", "changed")
			}
			return true
		})
		record.Attrs(func(attr slog.Attr) bool {
			if got := attr.Value.Group()[0].Value.String(); got != "original" {
				t.Fatalf("clone mutation changed original group to %q", got)
			}
			return true
		})
	}
}

func TestOrdinaryUsageStartRejectsInvalidOwnerStateWithoutPartialAccounting(t *testing.T) {
	usage := Usage{Groups: -1}
	attributes, depth, err := usage.Start(1)
	if !errors.Is(err, ErrRecordLimit) || attributes != 0 || depth != 0 || usage.Groups != -1 {
		t.Fatalf("invalid owner Start = %d, %d, %v; owner=%#v", attributes, depth, err, usage)
	}
	attrs, retained, err := CloneRawAttrsWithUsage([]slog.Attr{slog.String("value", "ordinary")}, usage)
	if !errors.Is(err, ErrRecordLimit) || attrs != nil || retained != (Usage{}) {
		t.Fatalf("invalid owner clone = %v, %#v, %v", attrs, retained, err)
	}
}

func TestOrdinaryCumulativeGroupAccountingUsesInclusiveRemainingBudget(t *testing.T) {
	usage := Usage{Attributes: MaxRecordAttributes - 2, Groups: 1}
	attributes, depth, err := usage.Start(1)
	if err != nil || attributes != MaxRecordAttributes-1 || depth != 2 {
		t.Fatalf("inclusive owner Start = %d, %d, %v", attributes, depth, err)
	}
	usage.Attributes++
	attributes, depth, err = usage.Start(1)
	if !errors.Is(err, ErrRecordLimit) || attributes != 0 || depth != 0 {
		t.Fatalf("cumulative owner Start = %d, %d, %v", attributes, depth, err)
	}
}

func TestOrdinaryEmptyDerivationsPreserveOwnerAccounting(t *testing.T) {
	usage := Usage{Attributes: 1, Groups: 1}
	grouped, err := usage.WithGroup("")
	if err != nil || grouped != usage {
		t.Fatalf("empty group changed usage: %#v, %v", grouped, err)
	}
	attrs, retained, err := CloneRawAttrsWithUsage(nil, usage)
	if err != nil || len(attrs) != 0 || retained != usage {
		t.Fatalf("empty attrs clone changed usage: %v, %#v, %v", attrs, retained, err)
	}
}

func TestOrdinaryEmptyBatchesPreserveInclusiveOwnerLimits(t *testing.T) {
	for _, usage := range []Usage{
		{Attributes: 1, Groups: 1},
		{Groups: MaxRecordDepth},
		{Attributes: MaxRecordAttributes, Groups: 1},
	} {
		attributes, depth, err := usage.Start(0)
		if err != nil || attributes != usage.Attributes || depth != usage.Groups+1 {
			t.Fatalf("empty Start(%#v) = %d, %d, %v", usage, attributes, depth, err)
		}
		for _, clone := range []func([]slog.Attr, Usage) ([]slog.Attr, Usage, error){CloneRawAttrsWithUsage, CloneResolvedAttrsWithUsage} {
			attrs, retained, err := clone(nil, usage)
			if err != nil || len(attrs) != 0 || retained != usage {
				t.Fatalf("empty clone(%#v) = %v, %#v, %v", usage, attrs, retained, err)
			}
		}
	}
}

func TestOrdinaryEmptyBatchRejectsInvalidRetainedAttributeCount(t *testing.T) {
	usage := Usage{Attributes: MaxRecordAttributes + 1}
	original := usage
	attributes, depth, err := usage.Start(0)
	if !errors.Is(err, ErrRecordLimit) || attributes != 0 || depth != 0 || usage != original {
		t.Fatalf("invalid retained owner Start = %d, %d, %v; owner=%#v", attributes, depth, err, usage)
	}
	for _, clone := range []func([]slog.Attr, Usage) ([]slog.Attr, Usage, error){CloneRawAttrsWithUsage, CloneResolvedAttrsWithUsage} {
		attrs, retained, err := clone(nil, usage)
		if !errors.Is(err, ErrRecordLimit) || attrs != nil || retained != (Usage{}) || usage != original {
			t.Fatalf("invalid retained owner clone = %v, %#v, %v; owner=%#v", attrs, retained, err, usage)
		}
	}
}

func TestOrdinaryLastScalarAdmissionAndFullOwnerRejection(t *testing.T) {
	usage := Usage{Attributes: MaxRecordAttributes - 1}
	for _, clone := range []func([]slog.Attr, Usage) ([]slog.Attr, Usage, error){CloneRawAttrsWithUsage, CloneResolvedAttrsWithUsage} {
		attrs, retained, err := clone([]slog.Attr{slog.Int("value", 7)}, usage)
		if err != nil || len(attrs) != 1 || !attrs[0].Equal(slog.Int("value", 7)) || retained.Attributes != MaxRecordAttributes || retained.Groups != 0 {
			t.Fatalf("last scalar clone = %v, %#v, %v", attrs, retained, err)
		}
		attrs[0] = slog.Int("changed", 8)
		if usage.Attributes != MaxRecordAttributes-1 {
			t.Fatal("clone changed caller accounting")
		}
	}
	full := Usage{Attributes: MaxRecordAttributes}
	attributes, depth, err := full.Start(1)
	if !errors.Is(err, ErrRecordLimit) || attributes != 0 || depth != 0 || full.Attributes != MaxRecordAttributes {
		t.Fatalf("full owner Start = %d, %d, %v; owner=%#v", attributes, depth, err, full)
	}
}

func TestOrdinaryVisitExhaustionPrecedesScalarResolution(t *testing.T) {
	owner := budget{attributes: MaxRecordAttributes - 1}
	if err := owner.visit(1); err != nil || owner.attributes != MaxRecordAttributes {
		t.Fatalf("last visit = %v, count=%d", err, owner.attributes)
	}
	if err := owner.visit(1); !errors.Is(err, ErrRecordLimit) || owner.attributes != MaxRecordAttributes {
		t.Fatalf("exhausted visit = %v, count=%d", err, owner.attributes)
	}
	resolved := 0
	attr, err := cloneAttr(slog.Any("value", countingValuer{count: &resolved}), 1, &owner, true)
	if !errors.Is(err, ErrRecordLimit) || !attr.Equal(slog.Attr{}) || resolved != 0 || owner.attributes != MaxRecordAttributes {
		t.Fatalf("exhausted scalar clone = %v, %v; resolved=%d count=%d", attr, err, resolved, owner.attributes)
	}
}

func TestOrdinaryGroupChildrenAdmittedBeforeAnyChildWork(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		original := slog.Group("group", slog.Int("value", 7))
		owner := budget{attributes: MaxRecordAttributes - 2}
		owned, err := cloneAttr(original, 1, &owner, resolve)
		if err != nil || owner.attributes != MaxRecordAttributes || owned.Key != "group" || len(owned.Value.Group()) != 1 || !owned.Value.Group()[0].Equal(slog.Int("value", 7)) {
			t.Fatalf("exact child clone = %v, %v; count=%d", owned, err, owner.attributes)
		}
		owned.Value.Group()[0] = slog.Int("changed", 8)
		if !original.Value.Group()[0].Equal(slog.Int("value", 7)) {
			t.Fatal("cloned child shared caller group storage")
		}
		resolved := 0
		owner = budget{attributes: MaxRecordAttributes - 2}
		partialFit := slog.Group("group", slog.Any("first", countingValuer{count: &resolved}), slog.Int("second", 8))
		owned, err = cloneAttr(partialFit, 1, &owner, resolve)
		if !errors.Is(err, ErrRecordLimit) || !owned.Equal(slog.Attr{}) || owner.attributes != MaxRecordAttributes-1 || resolved != 0 {
			t.Fatalf("partial-fit clone = %v, %v; count=%d resolved=%d", owned, err, owner.attributes, resolved)
		}
	}
}
