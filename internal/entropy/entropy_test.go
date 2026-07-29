package entropy_test

import (
	"testing"

	"github.com/zellyn/chess6502/internal/entropy"
)

// TestFold pins the mixing algorithm (asm/entropy.inc entfold): one step of
// the maximal-period 16-bit Galois LFSR, then EOR the byte into the low half.
func TestFold(t *testing.T) {
	cases := []struct {
		ent, want uint16
		x         byte
	}{
		{0x0000, 0x0000, 0x00},        // 0 is the LFSR's absorbing state
		{0x0000, 0x00A5, 0xA5},        // ...which any nonzero byte escapes
		{0x0002, 0x0001, 0x00},        // even state: plain shift, no taps
		{0x0003, 0xB401, 0x00},        // odd state: taps applied
		{0x0003, 0xB4FF, 0xFE},        // ...and the byte lands in the low half
		{0xFFFF, 0xB400 ^ 0x7FFF, 00}, // shift out of the high half
	}
	for _, c := range cases {
		col := entropy.New(0, c.ent)
		col.Fold(c.x)
		if _, got := col.State(); got != c.want {
			t.Errorf("fold(ent=%#04x, x=%#02x) = %#04x, want %#04x", c.ent, c.x, got, c.want)
		}
	}

	// Key folds ENTCNT's low byte and THEN its high byte.
	col := entropy.New(0x1234, 0)
	col.Key()
	want := entropy.New(0, 0)
	want.Fold(0x34)
	want.Fold(0x12)
	_, wantEnt := want.State()
	if _, got := col.State(); got != wantEnt {
		t.Errorf("Key with cnt=0x1234: ent = %#04x, want %#04x (fold 0x34 then 0x12)", got, wantEnt)
	}

	// The LFSR polynomial must be PRIMITIVE: stepping it with a zero input
	// must walk all 65535 nonzero states before returning to the start. That
	// is the whole guarantee against the cycling this fold replaced.
	walk := entropy.New(0, 1)
	steps := 0
	for {
		walk.Fold(0)
		steps++
		if _, s := walk.State(); s == 1 {
			break
		}
		if steps > 1<<17 {
			t.Fatalf("LFSR did not return to its start within %d steps", steps)
		}
	}
	if steps != 65535 {
		t.Errorf("LFSR period = %d, want 65535 (polynomial %#04x is not primitive)", steps, entropy.LFSRTaps)
	}
}

// TestFoldCannotCycleShort is the exhaustive proof that replaced the defect:
// for EVERY constant input byte and EVERY one of the 65536 accumulator
// states, the fold's orbit is either the single fixed point of that input or
// the full 65535-long cycle. Nothing in between, and nothing short.
//
// The fold this replaced (8-bit ENTROPY = ROL(ENTROPY) EOR x) failed this so
// badly that its LONGEST orbit was 16.
func TestFoldCannotCycleShort(t *testing.T) {
	if testing.Short() {
		t.Skip("exhaustive: 16.7M state-input pairs")
	}
	color := make([]int32, 65536)
	pos := make([]int32, 65536)
	fixedPoints, longCycles := 0, 0
	for a := 0; a < 256; a++ {
		for i := range color {
			color[i] = 0
		}
		for start := 0; start < 65536; start++ {
			if color[start] == -1 {
				continue
			}
			step := func(s uint16) uint16 {
				c := entropy.New(0, s)
				c.Fold(byte(a))
				_, n := c.State()
				return n
			}
			s, n := uint16(start), int32(0)
			for color[s] == 0 {
				color[s], pos[s] = int32(start)+1, n
				n++
				s = step(s)
			}
			if color[s] == int32(start)+1 {
				switch l := int(n - pos[s]); l {
				case 1:
					fixedPoints++
				case 65535:
					longCycles++
				default:
					t.Fatalf("input %#02x: found a cycle of length %d (want 1 or 65535)", a, l)
				}
			}
			for s2 := uint16(start); color[s2] != -1; s2 = step(s2) {
				color[s2] = -1
			}
		}
	}
	if fixedPoints != 256 || longCycles != 256 {
		t.Errorf("cycle census = %d fixed points + %d 65535-cycles, want 256 + 256",
			fixedPoints, longCycles)
	}
	t.Logf("exhaustive over 256 inputs x 65536 states: every orbit is either the "+
		"one fixed point (%d of them, one per input) or the full 65535-cycle (%d)",
		fixedPoints, longCycles)
}

// TestSeedNonzero: SEED = 0 means "dither off", so the collector must never
// hand out 0 (asm/entropy.inc entseed's lda #$5A fallback).
func TestSeedNonzero(t *testing.T) {
	if got := entropy.New(0, 0).Seed(); got != entropy.SeedFallback {
		t.Errorf("Seed of an all-zero collector = %#02x, want %#02x", got, entropy.SeedFallback)
	}
	col := entropy.New(0, 0xC37B)
	if got := col.Seed(); got != 0x7B { // the LOW byte is SEED
		t.Errorf("Seed = %#02x, want 0x7b", got)
	}
	// Seed must not disturb the accumulator (later keys keep folding).
	if _, ent := col.State(); ent != 0xC37B {
		t.Errorf("Seed mutated the accumulator: %#04x", ent)
	}
	// A zero LOW byte is the fallback case even when the high half is not.
	if got := entropy.New(0, 0xC300).Seed(); got != entropy.SeedFallback {
		t.Errorf("Seed with low byte 0 = %#02x, want %#02x", got, entropy.SeedFallback)
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

// TestQuantizedArrivals is the direct regression test for the defect this
// collector was rebuilt to fix: feed it a CONSTANT elapsed time between moves
// — what a loaded machine's scheduler actually delivers, and what an emulated
// opponent can deliver — and it must still hand out well-spread seeds.
//
// The fold this replaced failed this catastrophically. Its state was 8 bits
// and its mix was affine, so a constant arrival delta walked it round an orbit
// of at most 16 states. The captured failure was a real 1.254ms-quantized
// gauntlet run: a 32-long orbit of 28 distinct seeds, repeating forever, with
// the engine replaying the same game. Over every arrival delta that is a
// multiple of 8 — one in eight of them — the old fold dropped below this
// test's floor 100% of the time; this one does not drop below it at all.
func TestQuantizedArrivals(t *testing.T) {
	// Every wait from 0.1ms to ~26ms in 16-cycle (one poll iteration) steps,
	// i.e. every arrival delta the collector can possibly perceive, held
	// perfectly constant for 60 moves — the worst case the harness can hit.
	const moves = 60
	worst, worstTicks := 999, uint64(0)
	for ticks := uint64(0); ticks <= 1664; ticks++ {
		col := entropy.New(0x9F3C, 0x39C7)
		var seen [256]bool
		distinct := 0
		for i := 0; i < moves; i++ {
			col.Tick(ticks)
			col.Key()
			if s := col.Seed(); !seen[s] {
				seen[s] = true
				distinct++
			}
		}
		if distinct < worst {
			worst, worstTicks = distinct, ticks
		}
		if distinct < 40 {
			t.Errorf("constant arrival of %d poll iterations (%.3fms): only %d distinct seeds in %d moves",
				ticks, float64(ticks*entropy.TickCycles)*1000/entropy.CPUHz, distinct, moves)
		}
	}
	t.Logf("every constant arrival delta from 0 to 1664 poll iterations (0-26.1ms): "+
		"worst case %d distinct seeds in %d moves, at delta=%d (%.3fms)",
		worst, moves, worstTicks, float64(worstTicks*entropy.TickCycles)*1000/entropy.CPUHz)

	// And the specific captured failure: a 1.254ms wait, quantized hard.
	col := entropy.New(0x9F3C, 0x39C7)
	var seen [256]bool
	distinct := 0
	for i := 0; i < moves; i++ {
		col.WaitFor(1254 * 1000) // 1.254ms, every single move
		col.Key()
		if s := col.Seed(); !seen[s] {
			seen[s] = true
			distinct++
		}
	}
	if distinct < 40 {
		t.Errorf("the captured 1.254ms-quantized failure still collapses: %d distinct seeds in %d moves",
			distinct, moves)
	}
	t.Logf("the captured failure (1.254ms every move, which produced a 32-long orbit of "+
		"28 seeds): now %d distinct seeds in %d moves", distinct, moves)
}
