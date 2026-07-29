// Package entropy models — bit for bit — the on-device entropy collector in
// asm/entropy.inc, the thing that manufactures the eval dither's per-move SEED
// on a machine with no clock and no RNG.
//
// The hardware story (design: zellyn): the only unpredictable quantity an
// Apple IIe offers is WHEN THE HUMAN PRESSES A KEY. The driver's keyboard-wait
// loop therefore spins a 16-bit counter (ENTCNT, one 16-cycle iteration per
// poll) and folds it into a 16-bit accumulator (ENTROPY) at every keypress;
// ENTROPY's low byte becomes SEED at move time. The counter's low byte turns
// over every 4096 cycles (~4.0ms), so any human's jitter — tens of ms at best,
// seconds typically — spreads it uniformly over 0..255.
//
// The accumulator is a maximal-period 16-bit Galois LFSR rather than the
// 8-bit ROL-then-EOR it started as, because the old fold's orbits under a
// QUANTIZED (constant-delta) arrival stream were at most 16 states long, and
// the harness reproduced exactly that under load. asm/entropy.inc carries the
// measurement and the argument.
//
// This package is the host-side twin of that code, so the test harness can
// derive its per-move seeds through the SAME mixing algorithm the shipped
// build will run instead of bypassing it with a host PRNG. The only thing that
// differs is what drives the counter: on hardware it is 6502 cycles spent
// waiting for a human, in the harness it is the real elapsed time until the
// opponent's move arrives (plus the emulated cycle count the last search
// spent, the analogue of folding NODECNT at a ponder interruption).
// TestASMParity in this package proves the equivalence by running the actual
// 6502 collector under the emulator and comparing seed streams.
package entropy

import "time"

const (
	// TickCycles is the measured cost of one iteration of the collector's
	// keyboard-poll loop (asm/entropy.inc entkey: inc/bne/lda/bpl = 16
	// cycles; 21 on the 1-in-256 high-byte carry). TestASMLoopCycles pins
	// it against the emulator.
	TickCycles = 16

	// SeedFallback is what entseed installs when the accumulator happens to
	// be 0, since SEED = 0 means "dither off" (asm: lda #$5A).
	SeedFallback = 0x5A

	// CPUHz is the IIe's effective clock (65 cycles per 63.7µs line),
	// matching harness.EffectiveHz. Used to convert host wall-clock waits
	// into the 6502 cycles the device's loop would have counted.
	CPUHz = 1020484
)

// LFSRTaps is the Galois feedback constant of the accumulator's step, the
// primitive polynomial x^16 + x^14 + x^13 + x^11 + 1 (asm: eor #$B4 into
// ENTROPY+1). Primitive means the step's orbit is all 65535 nonzero states.
const LFSRTaps = 0xB400

// Collector is the ENTCNT/ENTROPY pair. The zero value is the "cold RAM was
// all zeroes" case; use Boot for a realistic power-on state.
type Collector struct {
	cnt uint16 // ENTCNT: free-running keyboard-wait loop counter
	ent uint16 // ENTROPY: 16-bit accumulator (LFSR state); low byte = SEED
}

// New returns a Collector with the given initial ENTCNT/ENTROPY. On hardware
// these are deliberately uninitialized (cold-boot RAM garbage, or the previous
// session's value after a warm restart), so any value is legitimate.
func New(cnt, ent uint16) *Collector { return &Collector{cnt: cnt, ent: ent} }

// Boot returns a Collector seeded from the host clock, standing in for the
// power-on RAM garbage the device starts with. Everything that matters comes
// from the folds afterwards; this only avoids starting every process from the
// same state.
func Boot() *Collector {
	ns := uint64(time.Now().UnixNano())
	return New(uint16(ns), uint16(ns>>16))
}

// State returns the current ENTCNT/ENTROPY, for parity checks against the
// emulated collector.
func (c *Collector) State() (cnt, ent uint16) { return c.cnt, c.ent }

// Tick advances the counter by n keyboard-poll iterations — what the 6502
// loop does while it waits. It wraps at 16 bits exactly as ENTCNT does.
func (c *Collector) Tick(n uint64) { c.cnt += uint16(n) }

// Wait advances the counter by the number of poll iterations that fit in the
// given number of 6502 cycles of waiting, and returns that iteration count.
func (c *Collector) Wait(cycles uint64) uint64 {
	n := cycles / TickCycles
	c.Tick(n)
	return n
}

// WaitFor advances the counter by however many poll iterations the device
// would have run while waiting for the given wall-clock duration. Negative
// durations are ignored.
func (c *Collector) WaitFor(d time.Duration) uint64 {
	if d <= 0 {
		return 0
	}
	return c.Wait(CyclesForDuration(d))
}

// Key folds BOTH bytes of the counter into the accumulator, low then high:
// the arrival time of one keystroke (asm: lda ENTCNT / jsr entfold /
// lda ENTCNT+1 / jsr entfold). The high byte carries almost no entropy of its
// own, but folding it means a repeating arrival needs the whole 16-bit counter
// to repeat rather than just its low byte — see entropy.inc for why that
// matters under a quantized (constant-delta) arrival stream.
func (c *Collector) Key() {
	c.Fold(byte(c.cnt))
	c.Fold(byte(c.cnt >> 8))
}

// Fold mixes an arbitrary byte into the accumulator (asm: entfold): one step
// of the maximal-period 16-bit Galois LFSR, then EOR the byte into the low
// half. Also used for non-keyboard events, e.g. the low byte of NODECNT when a
// ponder search is interrupted by the human's first keystroke: how far the
// search got is as unpredictable as the keypress.
func (c *Collector) Fold(x byte) {
	tap := c.ent&1 != 0 // asm: lsr ENTROPY+1 / ror ENTROPY leaves C = bit 0
	c.ent >>= 1
	if tap {
		c.ent ^= LFSRTaps
	}
	c.ent ^= uint16(x) // asm: pla / eor ENTROPY / sta ENTROPY (low half only)
}

// Seed returns the eval-dither seed to poke into SEED: the accumulator's low
// byte, forced nonzero (asm: entseed). The accumulator itself is not
// disturbed, so later folds keep accumulating.
func (c *Collector) Seed() byte {
	if byte(c.ent) == 0 {
		return SeedFallback
	}
	return byte(c.ent)
}

// CyclesForDuration converts a host wall-clock wait into the 6502 cycles the
// device's keyboard loop would have spun through in the same interval.
func CyclesForDuration(d time.Duration) uint64 {
	if d <= 0 {
		return 0
	}
	ns := uint64(d.Nanoseconds())
	if ns > 1<<43 { // ~2.4h: avoid overflow, and it is all wrap anyway
		ns = 1 << 43
	}
	return ns * CPUHz / 1e9
}
