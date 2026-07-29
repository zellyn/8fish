package chesstest

import (
	"math/rand/v2"
	"testing"
)

// TestAdaptiveParity is the mirror-asm parity gate for FT2_ADAPT: it drives
// the REAL on-device adaptmaybe routine (engine.s) over synthetic
// iterative-deepening signal sequences and asserts its ceiling-raise /
// easy-stop decisions match, step for step, a Go reference transcribed from
// mirror.SearchTimed's per-iteration policy block (internal/mirror/effort.go
// lines ~276-328) with the adaptive-aggr parameters. Exact node/depth parity
// with a full mirror search is impractical (the asm and mirror use different
// base predictive gates and different cost models), so this pins the POLICY
// DECISION — the movable ceiling and the stop signal — which is the part the
// asm reproduces exactly.
//
// adaptmaybe is invoked directly: a 4-instruction caller stub (jsr adaptmaybe;
// sta capture; exit) is planted in free RAM and the CPU is started there, with
// the iteration signals poked into zero page and the "cycles spent so far"
// simulated by presetting m.Cycles (CLOCK_TRAP returns m.Cycles>>8).
func TestAdaptiveParity(t *testing.T) {
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	adapt := labels["adaptmaybe"]
	if adapt == 0 {
		t.Fatal("adaptmaybe label not found")
	}

	// adaptive-aggr params, in 256-cycle UNITS (all test values are exact
	// multiples of the unit so there is no /256 rounding to reconcile).
	const base = 100 // per-move base ceiling (income)
	// Three bank scenarios give three distinct hard-max ceilings:
	//   plentiful bank -> ceilMax = 4*base (MaxEighths cap binds)
	//   medium bank    -> ceilMax between 3*base and 4*base
	//   thin bank      -> ceilMax below 3*base (income+bank binds; unstable
	//                     target is clamped down to ceilMax)
	scenarios := []struct {
		name    string
		ceilMax uint64
	}{
		{"plentiful", 4 * base}, // 400
		{"medium", 350},
		{"thin", 260},
	}

	rng := rand.New(rand.NewPCG(0xADA9, 0x717E))
	for _, sc := range scenarios {
		ceilMax := sc.ceilMax
		unstTarget := uint64(3 * base) // min(3*base, ceilMax)
		if unstTarget > ceilMax {
			unstTarget = ceilMax
		}
		minSpend := uint64(base / 4) // 25

		for seq := 0; seq < 60; seq++ {
			// Carried state across the "search": the movable ceiling starts at
			// the base ceiling; stable and prev-move/prev-score accumulate.
			asmCeil := uint64(base)
			refCeil := uint64(base)
			refStable := 0
			var prevFrom, prevTo byte = 0xFF, 0xFF
			prevScore := 0

			depth := 3 + rng.IntN(8) // 3..10 iterations
			for d := 1; d <= depth; d++ {
				// This iteration's signals.
				score := rng.IntN(400) - 200 // -200..199 cp
				// Best move: mostly stable, sometimes changes.
				bestFrom := prevFrom
				bestTo := prevTo
				if d == 1 || rng.IntN(3) == 0 {
					bestFrom = byte(rng.IntN(64))
					bestTo = byte(rng.IntN(64))
				}
				spent := uint64(rng.IntN(6 * base)) // 0..6*base units spent

				// --- Go reference (mirror policy) ---
				bestChanged := d > 1 && (bestFrom != prevFrom || bestTo != prevTo)
				refNext, refStableNext, refStop := policyStep(
					d, score, prevScore, bestChanged, refStable, refCeil,
					spent, base, ceilMax, unstTarget, minSpend)

				// --- Asm adaptmaybe (real 6502 routine) ---
				asmNext, asmStop := runAdaptStep(t, bin, defs, labels, adapt, adaptStep{
					curDepth:  byte(d),
					score:     int16(score),
					prevScore: int16(prevScore),
					bestFrom:  bestFrom, bestTo: bestTo,
					prevFrom: prevFrom, prevTo: prevTo,
					stable:  refStable, // asm carries STABLE; we feed the carried value
					ceiling: asmCeil,
					spent:   spent,
					ceilMax: ceilMax, unstTarget: unstTarget, minSpend: minSpend,
				})

				if asmNext != refNext || asmStop != refStop {
					t.Fatalf("scenario=%s seq=%d d=%d: asm(ceil=%d stop=%v) != ref(ceil=%d stop=%v) "+
						"[score=%d prevScore=%d bestChanged=%v stable=%d spent=%d ceilMax=%d]",
						sc.name, seq, d, asmNext, asmStop, refNext, refStop,
						score, prevScore, bestChanged, refStable, spent, ceilMax)
				}
				// On easy-stop the search would end; stop advancing this sequence.
				asmCeil, refCeil, refStable = asmNext, refNext, refStableNext
				prevFrom, prevTo, prevScore = bestFrom, bestTo, score
				if refStop {
					break
				}
			}
		}
		t.Logf("scenario %-9s ceilMax=%d: 60 sequences, all steps parity-clean", sc.name, ceilMax)
	}
}

// TestAdaptiveMateScoreDrop is the mate-range half of the FT2_ADAPT parity
// gate. TestAdaptiveParity only ever feeds scores in +-200cp, so it can never
// straddle the 16-bit signed range; root scores really span [-MATE, MATEZONEHI)
// = [-30000, +29695], and the difference of two of them reaches +-59695, which
// does NOT fit in 16 bits.
//
// scoreDrop = PREVSC - SCORE is computed as a bare 16-bit subtract in
// adaptmaybe, so it wraps whenever |drop| > 32767; the panic test must read the
// TRUE sign (N eor V), not the wrapped one. Both directions are reachable in a
// real search:
//
//   - "mate found against us": we were up 3 pawns last iteration and this
//     iteration sees a forced mate. True drop >= +32768 (a catastrophic drop,
//     the textbook panic case) wraps NEGATIVE -> the panic is SUPPRESSED
//     exactly when the engine most needs the extra time.
//   - "mate escaped": last iteration was a losing mate, this one is +3cp or
//     better. True drop <= -32769 (a huge IMPROVEMENT) wraps POSITIVE -> a
//     SPURIOUS panic raises the movable ceiling to CEILMAX.
//
// The winning-mate early exit at idok (SCORE+1 >= MATEZONEHI) catches neither:
// it only fires on a WINNING mate for the side to move, and in both cases above
// the mate score is the LOSING one, which falls straight through to adaptmaybe.
//
// The Go mirror (mirror.SearchTimed, transcribed here as policyStep) computes
// the drop in full-width int, so it is exact; before the fix this test is a
// genuine asm/mirror divergence, not a change of intent.
func TestAdaptiveMateScoreDrop(t *testing.T) {
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	adapt := labels["adaptmaybe"]
	if adapt == 0 {
		t.Fatal("adaptmaybe label not found")
	}

	const (
		base       = 100
		ceilMax    = 4 * base // 400
		unstTarget = 3 * base // 300
		minSpend   = base / 4 // 25
	)

	// Root-score extremes actually producible by the search (asm defs.inc:
	// MATE = 30000, MATEZONEHI = $74 so SCORE <= $73FF = 29695 whenever
	// adaptmaybe runs at all).
	const (
		lostMate = -29990 // mate against us in a handful of plies
		wonHigh  = 29695  // the largest score that still reaches adaptmaybe
	)

	cases := []struct {
		name             string
		prevScore, score int16
	}{
		// --- the two wrapping cases (the defect) ---
		{"mate-found-against-us", 3072, lostMate},  // true drop +33062
		{"mate-found-big-edge", 20000, lostMate},   // true drop +49990
		{"mate-escaped-to-edge", lostMate, 3072},   // true drop -33062
		{"mate-escaped-to-won", lostMate, wonHigh}, // true drop -59685
		{"widest-possible", wonHigh, -30000},       // true drop +59695
		// --- boundary: the largest drops that still FIT in 16 bits ---
		{"just-fits-positive", 2767, -30000}, // +32767
		{"just-fits-negative", -30000, 2768}, // -32768
		// --- non-wrapping mate-range controls (must be unchanged by the fix) ---
		{"mate-to-mate", lostMate, -29980},
		{"flat-in-mate-zone", lostMate, lostMate},
		{"small-drop-near-zero", 40, 10},
	}

	for _, tc := range cases {
		// Stable best move + depth 3, so the ONLY thing that can move the
		// ceiling is the panic signal: any divergence is the score-drop test.
		const stableFrom, stableTo = 0x12, 0x22
		refCeil, _, refStop := policyStep(3, int(tc.score), int(tc.prevScore),
			false, 1, base, 4*base, base, ceilMax, unstTarget, minSpend)
		asmCeil, asmStop := runAdaptStep(t, bin, defs, labels, adapt, adaptStep{
			curDepth:  3,
			score:     tc.score,
			prevScore: tc.prevScore,
			bestFrom:  stableFrom, bestTo: stableTo,
			prevFrom: stableFrom, prevTo: stableTo,
			stable:  1,
			ceiling: base,
			spent:   4 * base,
			ceilMax: ceilMax, unstTarget: unstTarget, minSpend: minSpend,
		})
		trueDrop := int(tc.prevScore) - int(tc.score)
		if asmCeil != refCeil || asmStop != refStop {
			t.Errorf("%s: prev=%d score=%d trueDrop=%d (wraps to %d): "+
				"asm(ceil=%d stop=%v) != mirror(ceil=%d stop=%v)",
				tc.name, tc.prevScore, tc.score, trueDrop,
				int16(uint16(tc.prevScore)-uint16(tc.score)),
				asmCeil, asmStop, refCeil, refStop)
			continue
		}
		t.Logf("%-22s prev=%6d score=%6d trueDrop=%7d -> ceil=%d stop=%v (parity)",
			tc.name, tc.prevScore, tc.score, trueDrop, asmCeil, asmStop)
	}
}

// policyStep is the mirror's per-iteration effort policy, transcribed from
// internal/mirror/effort.go SearchTimed (the adaptive-aggr config: PanicDrop
// 25 -> 4*base, Unstable -> 3*base, MaxEighths 32, EasyStop StableIters 2 /
// MinDepth 2 / ScoreFlat 30 / MinSpend base/4). ceilMax = min(4*base,
// income+bank); unstTarget = min(3*base, ceilMax); minSpend = base/4. All in
// 256-cycle units.
func policyStep(d, score, prevScore int, bestChanged bool, stable int,
	ceiling, spent, base, ceilMax, unstTarget, minSpend uint64) (newCeil uint64, newStable int, easyStop bool) {
	// stable = 1 on a change (or d==1), else ++.
	if d == 1 || bestChanged {
		stable = 1
	} else {
		stable++
	}
	scoreDrop := 0
	if d > 1 {
		scoreDrop = prevScore - score
	}
	// Panic: score dropped >= 25 -> raise to min(4*base, ceilMax) == ceilMax.
	if d > 1 && scoreDrop >= 25 {
		if ceilMax > ceiling {
			ceiling = ceilMax
		}
	}
	// Instability: best move changed -> raise to unstTarget.
	if bestChanged {
		if unstTarget > ceiling {
			ceiling = unstTarget
		}
	}
	// Easy-stop: stable best (>=2 past depth 2), flat score, enough spent.
	if d >= 2 && stable >= 2 && spent >= minSpend {
		drop := scoreDrop
		if drop < 0 {
			drop = -drop
		}
		if drop <= 30 {
			easyStop = true
		}
	}
	return ceiling, stable, easyStop
}

type adaptStep struct {
	curDepth         byte
	score, prevScore int16
	bestFrom, bestTo byte
	prevFrom, prevTo byte
	stable           int
	ceiling          uint64 // current movable ceiling (poked into BUDGET), units
	spent            uint64 // cycles-spent-so-far, units (CLOCK = m.Cycles>>8)
	ceilMax          uint64
	unstTarget       uint64
	minSpend         uint64
}

// runAdaptStep pokes one iteration's signals and invokes the real adaptmaybe
// routine, returning the (possibly raised) ceiling and the easy-stop flag.
func runAdaptStep(t *testing.T, bin []byte, defs Defs, labels map[string]uint16, adapt uint16, s adaptStep) (uint64, bool) {
	t.Helper()
	pos, err := ParseFEN("8/8/8/8/8/8/8/K6k w - - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Caller stub in free RAM ($BF20, past the loaded image, below the traps):
	//   jsr adaptmaybe ; sta $BF10 (capture A) ; lda #0 ; sta EXIT_TRAP ; brk
	const stub = 0xBF20
	const capAddr = 0xBF10
	m.Mem.Main[stub+0] = 0x20 // jsr
	m.Mem.Main[stub+1] = byte(adapt)
	m.Mem.Main[stub+2] = byte(adapt >> 8)
	m.Mem.Main[stub+3] = 0x8D // sta abs
	m.Mem.Main[stub+4] = byte(capAddr & 0xFF)
	m.Mem.Main[stub+5] = byte(capAddr >> 8)
	m.Mem.Main[stub+6] = 0xA9 // lda #0
	m.Mem.Main[stub+7] = 0x00
	m.Mem.Main[stub+8] = 0x8D // sta EXIT_TRAP
	m.Mem.Main[stub+9] = 0xFF
	m.Mem.Main[stub+10] = 0xBF
	m.Mem.Main[stub+11] = 0x00 // brk

	poke24u := func(sym string, v uint64) {
		a := defs[sym]
		m.Mem.Main[a] = byte(v)
		m.Mem.Main[a+1] = byte(v >> 8)
		m.Mem.Main[a+2] = byte(v >> 16)
	}
	m.Mem.Main[defs["FEATURES2"]] = byte(defs["FT2_ADAPT"])
	m.Mem.Main[defs["CURDEPTH"]] = s.curDepth
	m.Mem.Main[defs["SCORE"]] = byte(uint16(s.score))
	m.Mem.Main[defs["SCORE"]+1] = byte(uint16(s.score) >> 8)
	m.Mem.Main[defs["PREVSC0"]] = byte(uint16(s.prevScore))
	m.Mem.Main[defs["PREVSC1"]] = byte(uint16(s.prevScore) >> 8)
	m.Mem.Main[defs["BESTFROM"]] = s.bestFrom
	m.Mem.Main[defs["BESTTO"]] = s.bestTo
	m.Mem.Main[defs["PREVFROM"]] = s.prevFrom
	m.Mem.Main[defs["PREVTO"]] = s.prevTo
	m.Mem.Main[defs["STABLE"]] = byte(s.stable)
	poke24u("BUDGET0", s.ceiling)
	poke24u("CEILMAX0", s.ceilMax)
	poke24u("UNSTCEIL0", s.unstTarget)
	poke24u("MINSPEND0", s.minSpend)

	m.Cycles = s.spent << 8 // CLOCK_TRAP returns m.Cycles>>8 == spent units
	m.CPU.SetPC(stub)
	exited, code, err := m.Run(2_000_000)
	if err != nil || !exited {
		t.Fatalf("adaptmaybe stub: exited=%v code=%d err=%v", exited, code, err)
	}
	ceil := uint64(m.Mem.Main[defs["BUDGET0"]]) |
		uint64(m.Mem.Main[defs["BUDGET0"]+1])<<8 |
		uint64(m.Mem.Main[defs["BUDGET0"]+2])<<16
	return ceil, m.Mem.Main[capAddr] != 0
}
