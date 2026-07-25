package ucibridge

import (
	"testing"
	"time"
)

// TestDitherEntropySeeds: with the default (hardware-faithful) source, each
// arriving opponent move folds the real elapsed wait into the modelled
// on-device collector, so consecutive moves get well-spread, never-zero seeds.
// This is the harness's stand-in for the human's keystroke timing.
func TestDitherEntropySeeds(t *testing.T) {
	b := &Bridge{Dither: true} // DitherSource "" == DitherEntropy
	const n = 60
	seeds := make([]byte, 0, n)
	counts := map[byte]int{}
	for i := 0; i < n; i++ {
		b.ditherArrival()            // "the opponent's move arrived"
		time.Sleep(time.Millisecond) // a wait whose real jitter is the entropy
		s := b.ditherSeed()
		if s <= 0 || s > 255 {
			t.Fatalf("seed %d out of range 1..255", s)
		}
		seeds = append(seeds, byte(s))
		counts[byte(s)]++
	}
	// 60 uniform bytes: ~53 distinct expected; 40 is a very loose floor that
	// still fails hard if the wait jitter were quantized or the fold broken.
	if len(counts) < 40 {
		t.Errorf("only %d distinct seeds in %d moves: %v", len(counts), n, seeds)
	}
	t.Logf("%d moves: %d distinct seeds, first ten %v", n, len(counts), seeds[:10])
}

// TestDitherPRNGReproducible: the legacy source with a pinned DitherSeed is
// byte-for-byte reproducible (what fixed-seed tests need), and differs from a
// differently pinned stream.
func TestDitherPRNGReproducible(t *testing.T) {
	stream := func(seed uint64) []int {
		b := &Bridge{Dither: true, DitherSource: DitherPRNG, DitherSeed: seed}
		var out []int
		for i := 0; i < 12; i++ {
			b.ditherArrival() // must not affect the prng source
			out = append(out, b.ditherSeed())
		}
		return out
	}
	a, bb, c := stream(7), stream(7), stream(8)
	for i := range a {
		if a[i] != bb[i] {
			t.Fatalf("pinned stream not reproducible at %d: %v vs %v", i, a, bb)
		}
		if a[i] < 1 || a[i] > 255 {
			t.Fatalf("seed %d out of range", a[i])
		}
	}
	if equalInts(a, c) {
		t.Errorf("different DitherSeed produced the same stream: %v", a)
	}
}

// TestDitherOff: no dither means "leave SEED alone" (-1), which is what keeps
// every fingerprint/parity test deterministic.
func TestDitherOff(t *testing.T) {
	for _, b := range []*Bridge{
		{},
		{Dither: false, DitherSource: DitherEntropy},
		{Dither: true, DitherSource: DitherOff},
	} {
		b.ditherArrival()
		b.ditherFold(0x5A)
		if got := b.ditherSeed(); got != -1 {
			t.Errorf("Dither=%v Source=%q: seed = %d, want -1", b.Dither, b.DitherSource, got)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
