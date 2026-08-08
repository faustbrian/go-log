package sample

import (
	"context"
	"log/slog"
	"math"
	"testing"
)

func TestDeterministicUsesStrictStableHashThreshold(t *testing.T) {
	t.Parallel()

	const key = "hello"
	const wantHash = uint64(16844978562278765124)
	if got := fnv64a(key); got != wantHash {
		t.Fatalf("fnv64a(%q) = %d, want %d", key, got, wantHash)
	}

	threshold := float64(wantHash) / float64(math.MaxUint64)
	record := slog.Record{}
	keyFunc := func(context.Context, slog.Record) string { return key }
	exact, err := Deterministic(threshold, keyFunc)
	if err != nil {
		t.Fatal(err)
	}
	above, err := Deterministic(math.Nextafter(threshold, 1), keyFunc)
	if err != nil {
		t.Fatal(err)
	}
	if exact(context.Background(), record) {
		t.Fatal("record at the exact hash threshold was kept")
	}
	if !above(context.Background(), record) {
		t.Fatal("record above the hash threshold was dropped")
	}
}

func TestStableHashMixesEmptyAndShortKeys(t *testing.T) {
	t.Parallel()

	tests := map[string]uint64{
		"":      17280346270528514342,
		"a":     9413272369427828315,
		"key-0": 1734865316076021129,
	}
	for input, want := range tests {
		if got := fnv64a(input); got != want {
			t.Errorf("fnv64a(%q) = %d, want %d", input, got, want)
		}
	}
}
