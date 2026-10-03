package sample_test

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"testing"

	"github.com/faustbrian/go-log/v2/handler/sample"
)

func TestDeterministicKeyInclusiveBudget(t *testing.T) {
	for _, size := range []int{1024, 1025} {
		key := strings.Repeat("ordinary", size/8) + strings.Repeat("x", size%8)
		sampler, err := sample.Deterministic(math.Nextafter(1, 0), func(context.Context, slog.Record) string { return key })
		if err != nil {
			t.Fatal(err)
		}
		if got, want := sampler(context.Background(), slog.Record{}), size == 1024; got != want {
			t.Errorf("key bytes %d decision = %v, want %v", size, got, want)
		}
	}
}

func TestDeterministicExtremeRatesDoNotEvaluateKey(t *testing.T) {
	for _, rate := range []float64{0, 1} {
		calls := 0
		sampler, err := sample.Deterministic(rate, func(context.Context, slog.Record) string {
			calls++
			return "unused"
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := sampler(context.Background(), slog.Record{}); got != (rate == 1) || calls != 0 {
			t.Fatalf("rate %v decision=%v key calls=%d", rate, got, calls)
		}
	}
}
