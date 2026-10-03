package redact

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/go-log/v2/handler/capture"
	"github.com/faustbrian/go-log/v2/internal/slogrecord"
)

type ordinaryOwnerValue struct {
	calls *int
	value slog.Value
}

func (value ordinaryOwnerValue) LogValue() slog.Value {
	*value.calls++
	return value.value
}

func TestOrdinaryTrustedMessageExactLimitAndNilRule(t *testing.T) {
	sink := capture.New()
	handler, err := New(sink, &Options{
		PreserveTrustedMessage: true, PreserveTrustedAttributes: true,
		Rules: []Rule{nil, Keys("field")},
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	message := strings.Repeat("x", MaxTrustedMessageBytes)
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, message, 0)
	record.AddAttrs(slog.Any("field", ordinaryOwnerValue{&calls, slog.StringValue("ordinary")}))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	captured, ok := sink.Last()
	if !ok || captured.Message != message || calls != 0 {
		t.Fatal("exact trusted message or raw matching contract changed")
	}
	capture.AssertAttr(t, sink, "field", DefaultReplacement)
}

func TestOrdinaryOwnerStructuralBoundaries(t *testing.T) {
	for _, clone := range []bool{false, true} {
		for _, test := range []struct {
			name                    string
			attr                    slog.Attr
			count, depth, wantCount int
			limited                 bool
		}{
			{"scalar exact count", slog.Int("field", 7), slogrecord.MaxRecordAttributes - 1, 1, slogrecord.MaxRecordAttributes, false},
			{"scalar beyond count", slog.Int("field", 7), slogrecord.MaxRecordAttributes, 1, slogrecord.MaxRecordAttributes, true},
			{"group exact count", slog.Group("group", slog.Int("field", 7)), slogrecord.MaxRecordAttributes - 2, 1, slogrecord.MaxRecordAttributes, false},
			{"group child beyond count", slog.Group("group", slog.Int("field", 7)), slogrecord.MaxRecordAttributes - 1, 1, slogrecord.MaxRecordAttributes, true},
			{"scalar exact depth", slog.Int("field", 7), 0, slogrecord.MaxRecordDepth, 1, false},
			{"scalar beyond depth", slog.Int("field", 7), 0, slogrecord.MaxRecordDepth + 1, 0, true},
			{"child exact depth", slog.Group("group", slog.Int("field", 7)), 0, slogrecord.MaxRecordDepth - 1, 2, false},
			{"child beyond depth", slog.Group("group", slog.Int("field", 7)), 0, slogrecord.MaxRecordDepth, 1, true},
		} {
			t.Run(test.name+map[bool]string{false: "/transform", true: "/raw-clone"}[clone], func(t *testing.T) {
				count := test.count
				var result slog.Attr
				var err error
				if clone {
					result, err = cloneRuleAttr(test.attr, test.depth, &count)
				} else {
					handler := &Handler{}
					result, err = handler.transform(test.attr, nil, test.depth, &count)
				}
				if errors.Is(err, slogrecord.ErrRecordLimit) != test.limited || (err != nil && !test.limited) {
					t.Fatalf("limit result = %v", err)
				}
				if count != test.wantCount {
					t.Fatalf("count = %d, want %d", count, test.wantCount)
				}
				if test.limited {
					if !result.Equal(slog.Attr{}) {
						t.Fatal("failed owner returned partial attribute")
					}
					return
				}
				if result.Key != test.attr.Key {
					t.Fatal("owner changed key")
				}
				if result.Value.Kind() == slog.KindGroup {
					if result.Value.Group()[0].Value.Int64() != 7 {
						t.Fatal("owner changed child")
					}
					result.Value.Group()[0].Key = "changed"
					if test.attr.Value.Group()[0].Key != "field" {
						t.Fatal("owner retained caller group storage")
					}
				} else if result.Value.Int64() != 7 {
					t.Fatal("owner changed scalar")
				}
			})
		}
	}
}

func TestOrdinaryResolvedGroupAdmissionHasNoPartialDelivery(t *testing.T) {
	for _, initial := range []int{slogrecord.MaxRecordAttributes - 2, slogrecord.MaxRecordAttributes - 1} {
		sink := capture.New()
		handler, err := New(sink, &Options{PreserveTrustedAttributes: true})
		if err != nil {
			t.Fatal(err)
		}
		handler.usage.Attributes = initial
		calls := 0
		record := slog.NewRecord(time.Time{}, slog.LevelInfo, "fixed", 0)
		record.AddAttrs(slog.Any("group", ordinaryOwnerValue{&calls, slog.GroupValue(slog.Int("field", 7))}))
		err = handler.Handle(context.Background(), record)
		if calls != 1 {
			t.Fatalf("resolved group calls = %d", calls)
		}
		if initial == slogrecord.MaxRecordAttributes-1 {
			if !errors.Is(err, slogrecord.ErrRecordLimit) {
				t.Fatalf("error = %v", err)
			}
			capture.AssertCount(t, sink, 0)
		} else {
			if err != nil {
				t.Fatal(err)
			}
			capture.AssertCount(t, sink, 1)
			capture.AssertAttr(t, sink, "group.field", int64(7))
		}
		if handler.usage.Attributes != initial {
			t.Fatal("record changed retained admission state")
		}
	}
}

func TestOrdinaryRuleCopiesRemainRawAndIndependentlyCounted(t *testing.T) {
	calls, callbacks := 0, 0
	attr := slog.Group("group", slog.Any("field", ordinaryOwnerValue{&calls, slog.IntValue(7)}))
	rules := []Rule{
		func(_ string, owned slog.Attr) bool {
			callbacks++
			owned.Value.Group()[0].Key = "changed"
			return false
		},
		func(_ string, owned slog.Attr) bool {
			callbacks++
			return owned.Value.Group()[0].Key == "field" && owned.Value.Group()[0].Value.Kind() == slog.KindLogValuer
		},
	}
	if !Any(nil, rules[0], rules[1])("group", attr) {
		t.Fatal("standalone callbacks did not get independent raw groups")
	}
	if callbacks != 2 || calls != 0 || attr.Value.Group()[0].Key != "field" {
		t.Fatal("standalone callback changed raw owner")
	}
	handler := &Handler{rules: rules, replacement: slog.StringValue(DefaultReplacement)}
	count := slogrecord.MaxRecordAttributes - 1
	result, err := handler.transform(attr, nil, 1, &count)
	if err != nil || result.Value.String() != DefaultReplacement || count != slogrecord.MaxRecordAttributes {
		t.Fatalf("independent callback budget result = %v, %v, %d", result, err, count)
	}
	if callbacks != 4 || calls != 0 || attr.Value.Group()[0].Key != "field" {
		t.Fatal("transform callback changed raw owner")
	}
	count = 0
	owned, err := cloneRuleAttr(attr, 1, &count)
	if err != nil || count != 2 || owned.Value.Group()[0].Value.Kind() != slog.KindLogValuer || calls != 0 {
		t.Fatal("raw clone resolved or miscounted child")
	}
}

func TestOrdinaryRuleCloneDepthFailureSkipsCallback(t *testing.T) {
	callbacks := 0
	handler := &Handler{rules: []Rule{func(string, slog.Attr) bool {
		callbacks++
		return false
	}}}
	count := 0
	result, err := handler.transform(slog.Group("group", slog.Int("field", 7)), nil, slogrecord.MaxRecordDepth, &count)
	if !errors.Is(err, slogrecord.ErrRecordLimit) || !result.Equal(slog.Attr{}) || callbacks != 0 || count != 1 {
		t.Fatalf("rule clone failure returned %v, %v; callbacks=%d count=%d", result, err, callbacks, count)
	}
}
