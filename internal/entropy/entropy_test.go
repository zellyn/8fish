package entropy_test

import (
	"testing"

	"github.com/zellyn/chess6502/internal/entropy"
)

// TestFold pins the mixing algorithm (asm/entropy.inc entfold):
// ENTROPY = ROL(ENTROPY) EOR x, and Key folds the counter's low byte.
func TestFold(t *testing.T) {
	cases := []struct {
		ent, x, want byte
	}{
		{0x00, 0x00, 0x00},
		{0x00, 0xA5, 0xA5},
		{0x01, 0x00, 0x02},
		{0x80, 0x00, 0x01}, // bit 7 rotates into bit 0
		{0xFF, 0xFF, 0x00},
		{0x5A, 0x3C, 0xB4 ^ 0x3C},
	}
	for _, c := range cases {
		col := entropy.New(0, c.ent)
		col.Fold(c.x)
		if _, got := col.State(); got != c.want {
			t.Errorf("fold(ent=%#02x, x=%#02x) = %#02x, want %#02x", c.ent, c.x, got, c.want)
		}
	}

	// Key folds ENTCNT's LOW byte only.
	col := entropy.New(0x1234, 0)
	col.Key()
	if _, got := col.State(); got != 0x34 {
		t.Errorf("Key with cnt=0x1234: ent = %#02x, want 0x34", got)
	}
}

// TestSeedNonzero: SEED = 0 means "dither off", so the collector must never
// hand out 0 (asm/entropy.inc entseed's lda #$5A fallback).
func TestSeedNonzero(t *testing.T) {
	if got := entropy.New(0, 0).Seed(); got != entropy.SeedFallback {
		t.Errorf("Seed of an all-zero collector = %#02x, want %#02x", got, entropy.SeedFallback)
	}
	col := entropy.New(0, 0x7B)
	if got := col.Seed(); got != 0x7B {
		t.Errorf("Seed = %#02x, want 0x7b", got)
	}
	// Seed must not disturb the accumulator (later keys keep folding).
	if _, ent := col.State(); ent != 0x7B {
		t.Errorf("Seed mutated the accumulator: %#02x", ent)
	}
}

// TestTickWraps: the counter is 16-bit and wraps exactly as ENTCNT does, so a
// long wait is (correctly) indistinguishable from a short one modulo ~1.03s.
func TestTickWraps(t *testing.T) {
	col := entropy.New(0xFFF0, 0)
	col.Tick(0x20)
	if cnt, _ := col.State(); cnt != 0x0010 {
		t.Errorf("cnt after wrap = %#04x, want 0x0010", cnt)
	}
	col2 := entropy.New(0, 0)
	col2.Tick(1<<32 + 5) // huge wait: only the low 16 bits can matter
	if cnt, _ := col2.State(); cnt != 5 {
		t.Errorf("cnt after a 2^32 tick = %#04x, want 0x0005", cnt)
	}
}

// TestWaitConversion: a wall-clock wait becomes cycles becomes poll
// iterations (TickCycles each).
func TestWaitConversion(t *testing.T) {
	col := entropy.New(0, 0)
	// 1s of waiting = CPUHz cycles = CPUHz/16 poll iterations.
	iters := col.WaitFor(1_000_000_000)
	if want := uint64(entropy.CPUHz / entropy.TickCycles); iters != want {
		t.Errorf("WaitFor(1s) = %d iterations, want %d", iters, want)
	}
	if got := entropy.CyclesForDuration(-5); got != 0 {
		t.Errorf("CyclesForDuration(negative) = %d, want 0", got)
	}
	if col.WaitFor(0) != 0 {
		t.Error("WaitFor(0) advanced the counter")
	}
}
