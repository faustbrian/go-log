package redact_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	log "github.com/faustbrian/go-log/v2"
	"github.com/faustbrian/go-log/v2/handler/capture"
	"github.com/faustbrian/go-log/v2/handler/redact"
)

func TestEachRuleReceivesIndependentGroupData(t *testing.T) {
	for _, combined := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "Any"}[combined], func(t *testing.T) {
			for _, bound := range []bool{false, true} {
				t.Run(map[bool]string{false: "record", true: "bound"}[bound], func(t *testing.T) {
					sink := capture.New()
					rules := []redact.Rule{
						func(_ string, attr slog.Attr) bool {
							if attr.Value.Kind() == slog.KindGroup {
								attr.Value.Group()[0].Key = "changed"
							}
							return false
						},
						func(_ string, attr slog.Attr) bool {
							return attr.Value.Kind() == slog.KindGroup && attr.Value.Group()[0].Key == "sensitive"
						},
					}
					if combined {
						rules = []redact.Rule{redact.Any(rules...)}
					}
					handler, err := redact.New(sink, &redact.Options{PreserveTrustedAttributes: true, Rules: rules})
					if err != nil {
						t.Fatal(err)
					}
					attr := slog.Group("request", slog.String("sensitive", "fixture"))
					record := slog.NewRecord(time.Time{}, slog.LevelInfo, "event", 0)
					var target slog.Handler = handler
					if bound {
						target = target.WithAttrs([]slog.Attr{attr})
					} else {
						record.AddAttrs(attr)
					}
					if err := target.Handle(context.Background(), record); err != nil {
						t.Fatal(err)
					}
					if !capture.AssertAttr(t, sink, "request", redact.DefaultReplacement) {
						t.Fatal("later rule did not receive the original independent group")
					}
					if attr.Value.Group()[0].Key != "sensitive" {
						t.Fatal("rule changed caller-owned group")
					}
				})
			}
		})
	}
}

func TestAnyRejectsOverLimitStandaloneGroupBeforeCallbacks(t *testing.T) {
	attr := slog.String("leaf", "fixture")
	for range log.MaxRecordDepth {
		attr = slog.Group("group", attr)
	}
	calls := 0
	rule := redact.Any(nil, func(string, slog.Attr) bool {
		calls++
		return false
	})
	if !rule("group", attr) {
		t.Fatal("over-limit standalone group was not matched fail-closed")
	}
	if calls != 0 {
		t.Fatalf("invalid group reached %d callbacks", calls)
	}
}
