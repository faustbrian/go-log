package stack_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/faustbrian/go-log/v2/handler/stack"
)

func TestOrdinaryDerivationKeepsTwoLevelGroupsIndependentWithoutResolvingValues(t *testing.T) {
	lazy := &ordinaryOwnedValuer{}
	attrs := []slog.Attr{slog.Group("outer", slog.Group("inner", slog.Any("lazy", lazy)))}
	first, second := &ordinaryOwnedSink{mutate: true}, &ordinaryOwnedSink{}
	handler := mustNew(t, stack.Route{Handler: first}, stack.Route{Handler: second})
	derived := handler.WithAttrs(attrs)
	if !derived.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("ordinary derived handler is disabled")
	}
	for _, sink := range []*ordinaryOwnedSink{first, second} {
		if sink.outer != "outer" || sink.inner != "inner" || sink.leaf != "lazy" || sink.value != lazy {
			t.Fatalf("route observed changed derivation: %#v", sink)
		}
	}
	leaf := attrs[0].Value.Group()[0].Value.Group()[0]
	if attrs[0].Key != "outer" || leaf.Key != "lazy" || leaf.Value.Kind() != slog.KindLogValuer || leaf.Value.Any() != lazy || lazy.calls != 0 {
		t.Fatalf("derivation changed caller-owned groups or resolved value: attrs=%v calls=%d", attrs, lazy.calls)
	}
}

type ordinaryOwnedValuer struct{ calls int }

func (value *ordinaryOwnedValuer) LogValue() slog.Value {
	value.calls++
	return slog.StringValue("resolved")
}

type ordinaryOwnedSink struct {
	mutate             bool
	outer, inner, leaf string
	value              any
}

func (*ordinaryOwnedSink) Enabled(context.Context, slog.Level) bool  { return true }
func (*ordinaryOwnedSink) Handle(context.Context, slog.Record) error { return nil }
func (sink *ordinaryOwnedSink) WithGroup(string) slog.Handler        { return sink }
func (sink *ordinaryOwnedSink) WithAttrs(attrs []slog.Attr) slog.Handler {
	outer := attrs[0]
	inner := outer.Value.Group()[0]
	leaf := inner.Value.Group()[0]
	sink.outer, sink.inner, sink.leaf, sink.value = outer.Key, inner.Key, leaf.Key, leaf.Value.Any()
	if sink.mutate {
		attrs[0].Key = "changed"
		outer.Value.Group()[0].Key = "changed"
		inner.Value.Group()[0] = slog.String("changed", "changed")
	}
	return sink
}
