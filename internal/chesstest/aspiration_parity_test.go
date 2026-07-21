package chesstest

import (
	"fmt"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
)

// FT_ASP is the asm aspiration-window feature bit ($40). The mirror enables
// aspiration through Asp params, not a feature bit, so this bit is
// independent of the mirror's own 0x40 (FtHistory): the parity harness sets
// the asm FEATURES byte with $40 and the mirror's Features WITHOUT it.
const ftASP = 0x40

// aspShipMask is the shipped feature configuration both engines run under.
const aspShipMask = 0x1f

// asmIDRun runs the asm ID driver (budget mode) at depth cap d under a huge
// budget (never aborts) and returns the completed move, score, and make
// count. asp toggles FT_ASP.
func asmIDRun(t *testing.T, bin []byte, makeAddr uint16, fen string, d int, asp bool) (string, int, uint64) {
	t.Helper()
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	bits := byte(aspShipMask)
	if asp {
		bits |= ftASP
	}
	SetFeatures(m, defs, bits)
	SetBudget(m, defs, uint64(1)<<40, byte(d)) // huge budget: no aborts at these depths
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	var makes uint64
	exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
		if pc == makeAddr {
			makes++
		}
	})
	if err != nil || !exited || code > 2 {
		t.Fatalf("asm %s d%d asp=%v: exited=%v code=%d err=%v", fen, d, asp, exited, code, err)
	}
	sc := int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
	mv := MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
	return mv, sc, makes
}

// mirrorIDRun runs the mirror's SearchBudget at depth cap d under a huge
// (node) budget. asp selects Asp{25, AspAsym} vs off. It returns move,
// score, cumulative make count, and the aspiration counters.
func mirrorIDRun(t *testing.T, fen string, d int, asp bool) (mv string, sc int, makes, win, fl, fh uint64) {
	t.Helper()
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	me := mirror.NewEngine()
	me.Features = aspShipMask
	if asp {
		me.Asp = mirror.AspirationParams{Delta: 25, Policy: mirror.AspAsym}
	}
	me.CycleTrack = true // populate Cyc.Makes (exact tree fingerprint)
	me.Seed = 0
	me.SetPosition(mp)
	m, s := me.SearchBudget(uint64(1)<<60, d)
	return m.UCI(), s, me.Cyc.Makes, me.AspWindows, me.AspFailLow, me.AspFailHigh
}

// TestAspirationMirrorParity proves the asm FT_ASP iterative-deepening driver
// reproduces mirror.aspIterate (Delta 25, Policy AspAsym) EXACTLY: same
// per-iteration best move, score, and cumulative make count.
//
// Caveat, discovered and characterized while porting: the asm and the mirror
// carry a shared transposition table across ID iterations, and although a
// single fixed-depth search matches byte-for-byte (TestSearchMirrorParity),
// their TTs diverge slightly once the 4096-entry table starts colliding
// across iterations — a PRE-EXISTING asm-vs-mirror substrate divergence that
// is present with FT_ASP OFF and even at mask 0x00 (bare alpha-beta, TT the
// only cross-iteration state), and is byte-identical between this engine and
// the pre-aspiration engine.bin. It surfaces as a make-count difference from
// roughly depth 6 / tens of thousands of nodes onward (same move+score), and
// aspiration can amplify it into a score difference in the dirty regime. It
// is NOT an aspiration bug.
//
// So this test is substrate-aware: at each (FEN, depth) it first runs both
// engines WITHOUT aspiration; where that substrate agrees exactly — which is
// exactly where a faithful aspiration port MUST also agree — it asserts the
// WITH-aspiration asm and mirror runs match EXACTLY (move+score+make). The
// accumulated window/fail counters prove the exact-match regime actually
// exercised narrow windows, fail-lows, fail-highs, and a mate-zone iteration.
func TestAspirationMirrorParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	fens := []string{
		// Quiet / positional (score stable: narrow window usually holds).
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		// Tactical, chosen (via a mirror scan) to swing >Delta between shallow
		// iterations and drive fail-lows and fail-highs at d<=5:
		"r1bqkb1r/pppp1ppp/2n2n2/4p3/2B1P3/5N2/PPPP1PPP/RNBQK2R b KQkq - 4 4", // fail-lows d2-5
		"3r1rk1/p1q2ppp/1pn1p3/2P1n3/3P4/P3PN2/1B3PPP/R2Q1RK1 w - - 0 1",      // fail-low + fail-high
		"2r3k1/1p3ppp/p1nqp3/3n4/3P4/2P2N2/P1Q2PPP/1RB2RK1 b - - 0 1",         // fail-high then both
		"rnbqk2r/pp2bppp/2p2n2/3p4/3P4/2N1PN2/PPP1BPPP/R1BQK2R w KQkq - 0 1",  // fail-low
		// Endgame with tactical swings.
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		// Winning KR mate: at the mate-discovery iteration the (non-mate) seed
		// opens a narrow window and the search finds mate through it (aspbump's
		// mate path). The asm then halts ID on the mate; capped by cleanliness.
		"k7/8/2K5/8/8/8/8/6R1 w - - 0 1",
		// Side to move being mated: the ID produces a losing-mate score, whose
		// next iteration seeds aspiterate's mate-zone guard (full window).
		"6R1/8/8/8/8/2k5/8/K7 b - - 0 1",
	}
	const maxD = 5 // stay in the substrate-clean regime (divergence bites ~d6+)

	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	makeAddr := labels["make"]

	var cleanChecks int
	var totWin, totFL, totFH uint64
	var mateSeen int
	for _, fen := range fens {
		for d := 1; d <= maxD; d++ {
			// Substrate probe: do the two engines agree WITHOUT aspiration?
			anMv, anSc, anMk := asmIDRun(t, bin, makeAddr, fen, d, false)
			mnMv, mnSc, mnMk, _, _, _ := mirrorIDRun(t, fen, d, false)
			substrateClean := anMv == mnMv && anSc == mnSc && anMk == mnMk

			// Aspiration runs.
			aMv, aSc, aMk := asmIDRun(t, bin, makeAddr, fen, d, true)
			mMv, mSc, mMk, win, fl, fh := mirrorIDRun(t, fen, d, true)

			if !substrateClean {
				continue // pre-existing TT divergence; not comparable exactly
			}
			cleanChecks++
			// Counters are cumulative over iterations 1..d within a run; the
			// running max across clean checks proves each branch fired in some
			// exact-matched run.
			if win > totWin {
				totWin = win
			}
			if fl > totFL {
				totFL = fl
			}
			if fh > totFH {
				totFH = fh
			}
			if mSc >= 0x7400 || mSc <= -0x7401 {
				mateSeen++
			}
			if aMv != mMv || aSc != mSc || aMk != mMk {
				t.Errorf("%s d%d (substrate clean): asm=%s/%d mk=%d  mirror=%s/%d mk=%d",
					fen, d, aMv, aSc, aMk, mMv, mSc, mMk)
			}
		}
	}

	// The exact-match regime must actually exercise every aspiration branch.
	if cleanChecks < 20 {
		t.Fatalf("only %d substrate-clean iterations checked; test too weak", cleanChecks)
	}
	if totWin == 0 {
		t.Errorf("no narrow aspiration windows were opened in the clean regime")
	}
	if totFL == 0 {
		t.Errorf("no fail-lows exercised in the clean regime")
	}
	if totFH == 0 {
		t.Errorf("no fail-highs exercised in the clean regime")
	}
	if mateSeen == 0 {
		t.Errorf("no mate-zone iteration exercised in the clean regime")
	}
	fmt.Printf("asm FT_ASP == mirror aspIterate{25,AspAsym} exact over %d substrate-clean iterations "+
		"(windows=%d failLow=%d failHigh=%d mateIters=%d)\n", cleanChecks, totWin, totFL, totFH, mateSeen)
}

// TestAspirationAbortSafety verifies the asm-side abort invariant with
// aspiration active: a budget that aborts an iteration mid-(re)search must
// discard that whole iteration and play the LAST COMPLETED iteration's move
// and score — never a fail-soft/partial-window artifact. The asm and mirror
// abort at different points (cycle vs node budgets), so this is checked
// purely on the asm side, per the task brief.
//
// Method: run under a budget tight enough to hard-abort; the abort path
// leaves CURDEPTH = the completed depth j and restores PREV*. We then re-run
// depth-capped at j under a huge budget, which replays iterations 1..j of the
// identical, deterministic ID sequence (the budget never shapes the tree) and
// completes cleanly. The budgeted (move, score) must equal that clean
// iteration-j result exactly. Since j >= 2 and the seed is a non-mate score,
// iteration j+1 opened a narrow aspiration window, so the abort necessarily
// landed inside aspiterate's windowed-search loop.
func TestAspirationAbortSafety(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	// These positions hard-abort an aspiration iteration (d>=2, narrow window)
	// mid-(re)search at cycle budgets in the scanned bands (the predictive gate
	// otherwise stops cleanly between iterations; a hard abort needs a started
	// iteration to overshoot 2x budget, which aspiration re-searches can cause).
	// Bands are wide enough that a small cycle drift still lands a hard abort.
	type band struct {
		fen          string
		lo, hi, step uint64
	}
	bands := []band{
		{"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", 20_000_000, 40_000_000, 1_000_000},
		{"3r1rk1/p1q2ppp/1pn1p3/2P1n3/3P4/P3PN2/1B3PPP/R2Q1RK1 w - - 0 1", 24_000_000, 44_000_000, 1_000_000},
	}
	bin := loadEngine(t)

	runBudget := func(fen string, budget uint64, maxDepth byte) (mv string, score int, curdepth, abort byte) {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, aspShipMask|ftASP)
		SetBudget(m, defs, budget, maxDepth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {})
		if err != nil || !exited || code > 2 {
			t.Fatalf("asm %s budget %d: exited=%v code=%d err=%v", fen, budget, exited, code, err)
		}
		sc := int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
		mv = MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
		return mv, sc, m.Mem.Main[defs["CURDEPTH"]], m.Mem.Main[defs["ABORT"]]
	}

	checked, genuineAborts := 0, 0
	for _, bd := range bands {
		for budget := bd.lo; budget <= bd.hi; budget += bd.step {
			bmv, bsc, j, ab := runBudget(bd.fen, budget, 20)
			if j < 2 {
				continue // stopped in iteration 1: no aspiration iteration to discard
			}
			if bsc >= 0x7400 {
				continue // winning mate: asm halts ID early, replay differs — skip
			}
			// Clean replay: complete exactly iterations 1..j (huge budget, cap j).
			// The maxDepth-capped driver reports the depth-j move/score but leaves
			// CURDEPTH at j+1 (it increments before the > MAXCAP test), so cj is
			// not compared; (cmv, csc) is the depth-j result we want.
			cmv, csc, _, _ := runBudget(bd.fen, uint64(1)<<40, j)
			if bmv != cmv || bsc != csc {
				t.Errorf("%s budget %d (ABORT=%d): played %s/%d, last completed iter %d is %s/%d (fail-soft leak?)",
					bd.fen, budget, ab, bmv, bsc, j, cmv, csc)
			}
			checked++
			if ab != 0 {
				genuineAborts++ // a genuine mid-(re)search hard abort exercised the discard path
			}
		}
	}
	if checked == 0 {
		t.Fatal("no budget produced a post-iteration-1 stop; test exercised nothing")
	}
	if genuineAborts == 0 {
		t.Fatal("no genuine mid-search hard abort (ABORT=1) landed in the scanned bands; retune budgets")
	}
	fmt.Printf("asm FT_ASP abort-safety: %d budgeted stops verified, %d genuine mid-(re)search hard aborts, "+
		"each played the last completed iteration exactly\n", checked, genuineAborts)
}
