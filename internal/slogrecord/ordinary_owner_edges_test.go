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
