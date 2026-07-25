package chesstest

import (
	"math/rand"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
	"github.com/zellyn/chess6502/internal/refchess"
)

// ft2EGTech is FT2_EGTECH (asm defs.inc): the phase-gated endgame-technique
// eval terms ported in this task (mirror endgame.go DefaultEndgame).
const ft2EGTech = 0x08

// refEGEval returns the mirror's static eval with the endgame-technique term
// set to DefaultEndgame (on) or the zero value (off), optionally with the
// mop-up term on too, Features FtAll, no dither.
func refEGEval(t *testing.T, fen string, eg, mop bool) int {
	t.Helper()
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		t.Fatalf("mirror ParseFEN %q: %v", fen, err)
	}
	me := mirror.NewEngine() // Features FtAll, DefaultWeights
	me.SetPosition(mp)
	if eg {
		me.EG = mirror.DefaultEndgame
	}
	if mop {
		me.Mopup = mirror.DefaultMopup
	}
	me.Seed = 0
	return me.Eval()
}

// egPhase returns the mirror's taper phase for a FEN (the gate variable).
func egPhase(t *testing.T, fen string) int {
	t.Helper()
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		t.Fatalf("mirror ParseFEN %q: %v", fen, err)
	}
	me := mirror.NewEngine()
	me.SetPosition(mp)
	return me.Pos.Phase
}

// egFireFENs are pawn endgames inside the phase gate, chosen to exercise
// every one of the six terms and every awkward corner of the port:
// passers for both colors, DOUBLED files (where the nearest-pawn distance
// differs from the per-file most-advanced rank — the sketch's shortcut would
// be wrong here and the port must be exact), blocked/unblocked passers,
// pawns on the 2nd and 7th ranks (front-square edge cases), kings ahead of /
// directly behind / beside their own passer, and pawnless endgames where the
// KingPawn term must be silent.
var egFireFENs = []string{
	// --- king-and-pawn basics
	"4k3/8/4K3/8/8/8/4P3/8 w - - 0 1",     // KPK, king ahead of the pawn
	"4k3/8/8/8/8/4K3/4P3/8 w - - 0 1",     // KPK, king directly behind (penalty)
	"8/4p3/4k3/8/8/8/4K3/8 b - - 0 1",     // black pawn + king escorting
	"8/8/8/8/8/1k6/1p6/1K6 b - - 0 1",     // black pawn on the 2nd (front = rank 0)
	"8/1P6/1K6/8/8/1k6/8/8 w - - 0 1",     // white pawn on the 7th (front = rank 7)
	"8/8/8/8/8/8/1P6/1K5k w - - 0 1",      // white pawn on its start rank
	// --- doubled pawns: nearest-pawn distance != most-advanced-rank distance
	"8/8/8/8/3P4/3P4/3K4/7k w - - 0 1",    // doubled white d-pawns, king behind
	"7k/3p4/3p4/8/8/8/3K4/8 b - - 0 1",    // doubled black d-pawns
	"8/6k1/2P5/2P5/2P5/8/6K1/8 w - - 0 1", // tripled, king far away
	// --- passers vs blockers on adjacent files
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", // the standard rook endgame
	"6k1/8/1p6/1P6/8/8/6K1/8 w - - 0 1",         // mutually blocked (no passers)
	"8/p7/8/8/8/8/1P6/K6k w - - 0 1",            // a- vs b-passer, both passed
	"8/8/8/2k5/2p5/8/2K5/8 w - - 0 1",           // black passer, white king in front
	// --- both colors with passers and real pieces (phase 4..6)
	"8/8/4k3/8/2r5/8/4KP2/4R3 w - - 0 1",
	"8/5ppk/8/8/8/8/PP6/K7 w - - 0 1",
	"6k1/5p1p/6p1/8/8/1P6/P1P5/6K1 w - - 0 1",
	"1n6/1P6/8/8/6k1/8/8/6K1 w - - 0 1", // white 7th-rank passer, knight blockade
	"3k4/2p2ppp/3p4/1B6/1R6/4P1P1/5P1P/6K1 w - - 0 1", // R+B endgame, phase 3
	// --- pawnless endgames: KingPawn must be silent, KingCent still fires
	"8/8/8/4k3/8/8/8/3QK3 w - - 0 1",
	"8/8/8/4k3/8/8/8/R3K3 w - - 0 1",
	"8/8/8/4k3/8/8/8/2BNK3 w - - 0 1",
	// --- kings on the same file/rank as passers, edge files
	"8/8/8/8/8/k1K5/P7/8 w - - 0 1",
	"8/7p/7k/8/8/7K/8/8 b - - 0 1",
	"8/8/8/8/8/6k1/7p/6K1 b - - 0 1",
}

// egZeroFENs are INSIDE the phase gate but sum to exactly zero (mutually
// blocked pawns, mirror-symmetric kings): a firing-detection control that
// pins asm == mirror on a configuration where the six terms cancel.
var egZeroFENs = []string{
	"8/8/3k4/2p5/2P5/3K4/8/8 w - - 0 1",
}

// egQuietFENs are middlegame/opening positions above the phase gate: the
// endgame terms must NEVER fire (ON eval == OFF eval == mirror).
var egQuietFENs = []string{
	"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
}

// TestEGTermEvalParity is the KEY eval gate for FT2_EGTECH: the asm static
// eval with the bit ON must EQUAL the mirror's Eval() with EG =
// DefaultEndgame, to the centipawn, on (a) endgame positions where the terms
// fire, (b) pawnless endgames, (c) middlegame positions where they must stay
// silent, and (d) thousands of random-play positions. It also pins the
// no-middlegame-leak property on the ASM side (ON == OFF above the phase
// gate) and that the OFF eval is still exactly the mirror's EG-off eval, both
// with the mop-up off and with the mop-up on (the two terms share one gate,
// so the combined path is tested too). Analogue of TestMopupEvalParity.
func TestEGTermEvalParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: thousands of engine runs")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	evalinit, evalAddr := labels["evalinit"], labels["eval"]
	if evalinit == 0 || evalAddr == 0 {
		t.Fatalf("labels missing: evalinit=%#x eval=%#x", evalinit, evalAddr)
	}

	fires, leaks := 0, 0
	check := func(fen string, wantFire bool) {
		t.Helper()
		phase := egPhase(t, fen)
		asmOn := asmStaticEval(t, bin, evalinit, evalAddr, fen, ft2EGTech)
		asmOff := asmStaticEval(t, bin, evalinit, evalAddr, fen, 0x00)
		refOn := refEGEval(t, fen, true, false)
		refOff := refEGEval(t, fen, false, false)
		if asmOn != refOn {
			t.Errorf("EG ON eval mismatch %q (phase %d): asm=%d mirror=%d (asmOff=%d refOff=%d)",
				fen, phase, asmOn, refOn, asmOff, refOff)
		}
		if asmOff != refOff {
			t.Errorf("EG OFF eval mismatch %q: asm=%d mirror=%d", fen, asmOff, refOff)
		}
		// The combined path: mop-up + endgame technique share one phase gate.
		asmBoth := asmStaticEval(t, bin, evalinit, evalAddr, fen, ft2EGTech|ft2Mopup)
		refBoth := refEGEval(t, fen, true, true)
		if asmBoth != refBoth {
			t.Errorf("EG+MOPUP eval mismatch %q: asm=%d mirror=%d", fen, asmBoth, refBoth)
		}
		fired := asmOn != asmOff
		if fired != (refOn != refOff) {
			t.Errorf("fire disagreement %q: asmFired=%v mirrorFired=%v", fen, fired, refOn != refOff)
		}
		if fired != wantFire {
			t.Errorf("gate expectation %q (phase %d): fired=%v want=%v (on=%d off=%d)",
				fen, phase, fired, wantFire, asmOn, asmOff)
		}
		if fired && phase > 6 {
			leaks++
			t.Errorf("MIDDLEGAME LEAK %q: phase %d > 6 but eval changed (%d -> %d)",
				fen, phase, asmOff, asmOn)
		}
		if fired {
			fires++
		}
	}

	for _, fen := range egFireFENs {
		check(fen, true)
	}
	for _, fen := range egZeroFENs {
		check(fen, false) // inside the gate, but the six terms cancel exactly
	}
	for _, fen := range egQuietFENs {
		check(fen, false)
	}

	// Breadth: positions from random legal games. Most are middlegames (gate
	// shut: ON must equal OFF, the asm-side leak proof); the endgames that
	// surface must match the mirror exactly.
	rng := rand.New(rand.NewSource(0x4547))
	positions, gated, above := 0, 0, 0
	for games := 0; games < 140 && positions < 3000; games++ {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for ply := 0; ply < 100; ply++ {
			legal := ref.LegalMoves()
			if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
				break
			}
			if err := ref.Make(legal[rng.Intn(len(legal))]); err != nil {
				t.Fatal(err)
			}
			fen := ref.FEN()
			positions++
			asmOn := asmStaticEval(t, bin, evalinit, evalAddr, fen, ft2EGTech)
			refOn := refEGEval(t, fen, true, false)
			if asmOn != refOn {
				asmOff := asmStaticEval(t, bin, evalinit, evalAddr, fen, 0x00)
				t.Fatalf("random-corpus EG ON mismatch %q (phase %d): asm=%d mirror=%d (asmOff=%d)",
					fen, egPhase(t, fen), asmOn, refOn, asmOff)
			}
			if egPhase(t, fen) > 6 {
				above++
				asmOff := asmStaticEval(t, bin, evalinit, evalAddr, fen, 0x00)
				if asmOn != asmOff {
					leaks++
					t.Fatalf("MIDDLEGAME LEAK %q: phase %d, on=%d off=%d", fen, egPhase(t, fen), asmOn, asmOff)
				}
			} else {
				gated++
			}
		}
	}
	// Breadth INSIDE the gate: random legal play out of each curated endgame,
	// which is where the terms actually run. Random play from the start
	// position essentially never trades down to phase <= 6, so without this
	// the corpus would only prove the no-leak half.
	egPositions := 0
	for _, start := range egFireFENs {
		for g := 0; g < 3; g++ {
			ref, err := refchess.ParseFEN(start)
			if err != nil {
				t.Fatalf("refchess ParseFEN %q: %v", start, err)
			}
			for ply := 0; ply < 40; ply++ {
				legal := ref.LegalMoves()
				if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
					break
				}
				if err := ref.Make(legal[rng.Intn(len(legal))]); err != nil {
					t.Fatal(err)
				}
				fen := ref.FEN()
				egPositions++
				asmOn := asmStaticEval(t, bin, evalinit, evalAddr, fen, ft2EGTech)
				refOn := refEGEval(t, fen, true, false)
				if asmOn != refOn {
					asmOff := asmStaticEval(t, bin, evalinit, evalAddr, fen, 0x00)
					t.Fatalf("endgame-corpus EG ON mismatch %q (phase %d): asm=%d mirror=%d (asmOff=%d)",
						fen, egPhase(t, fen), asmOn, refOn, asmOff)
				}
				asmBoth := asmStaticEval(t, bin, evalinit, evalAddr, fen, ft2EGTech|ft2Mopup)
				if refBoth := refEGEval(t, fen, true, true); asmBoth != refBoth {
					t.Fatalf("endgame-corpus EG+MOPUP mismatch %q: asm=%d mirror=%d",
						fen, asmBoth, refBoth)
				}
				if egPhase(t, fen) <= 6 {
					gated++
				}
			}
		}
	}
	t.Logf("EG eval parity: %d curated (%d fired) + %d random-play + %d endgame-corpus "+
		"positions all exact; %d above the gate with %d leaks, %d inside the gate",
		len(egFireFENs)+len(egZeroFENs)+len(egQuietFENs), fires, positions, egPositions,
		above, leaks, gated)
	if fires < 20 {
		t.Errorf("only %d curated positions fired — the term set is under-exercised", fires)
	}
}

// egSearchFENs are endgames deep enough inside the phase gate that the terms
// steer the whole search, with enough material for a real depth-5 tree.
var egSearchFENs = []string{
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	"4k3/8/4K3/8/8/8/4P3/8 w - - 0 1",
	"8/8/8/3k4/8/3K4/3Q4/8 w - - 0 1",
	"6k1/5p1p/6p1/8/8/1P6/P1P5/6K1 w - - 0 1",
	"8/8/4k3/8/2r5/8/4KP2/4R3 w - - 0 1",
	"3k4/2p2ppp/3p4/1B6/1R6/4P1P1/5P1P/6K1 w - - 0 1",
	"8/5ppk/8/8/8/8/PP6/K7 w - - 0 1",
	"7k/3p4/3p4/8/8/8/3K4/8 b - - 0 1",
}

// TestEGTermSearchParity is the TREE-level gate for FT2_EGTECH: with the term
// on, the asm's fixed-depth search must reproduce the mirror's (EG =
// DefaultEndgame) best move, score and make count exactly. Eval parity alone
// would leave open a state-corruption bug — the term runs at every endgame
// leaf and borrows a dozen eval scratch bytes, so a clobbered byte would show
// up here as a diverging tree, not as a wrong eval.
//
// (Until asm/tt.s' unsigned mate-zone compare was fixed, this gate needed
// Engine.TTPlyQuirk to reproduce the asm's ply-shifted negative scores; with
// the asm signed-correct the mirror's stock path matches.)
func TestEGTermSearchParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	makeAddr := labels["make"]
	const depth = 5
	asmRun := func(fen string, ft2 byte) (string, int, uint64) {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1f)
		SetFeatures2(m, defs, ft2)
		SetBudget(m, defs, 0, depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		var makes uint64
		if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
			if pc == makeAddr {
				makes++
			}
		}); err != nil {
			t.Fatal(err)
		}
		sc := int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
		mv := MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
		return mv, sc, makes
	}
	mirRun := func(fen string, eg bool) (string, int, uint64) {
		mp, err := mirror.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		me := mirror.NewEngine()
		me.Features = 0x1f
		if eg {
			me.EG = mirror.DefaultEndgame
		}
		me.CycleTrack = true
		me.SetPosition(mp)
		mb, ms := me.SearchFixed(depth)
		return mb.UCI(), ms, me.Cyc.Makes
	}
	active := 0
	for _, fen := range egSearchFENs {
		amv, asc, amk := asmRun(fen, ft2EGTech)
		mmv, msc, mmk := mirRun(fen, true)
		mkdiff := int64(amk) - int64(mmk)
		if (mmv != "none" && amv != mmv) || asc != msc || mkdiff < -1 || mkdiff > 1 {
			t.Errorf("EG ON d%d %s: asm=%s/%d mk=%d  mirror=%s/%d mk=%d",
				depth, fen, amv, asc, amk, mmv, msc, mmk)
		}
		aOffMv, aOffSc, aOffMk := asmRun(fen, 0)
		mOffMv, mOffSc, mOffMk := mirRun(fen, false)
		offdiff := int64(aOffMk) - int64(mOffMk)
		if (mOffMv != "none" && aOffMv != mOffMv) || aOffSc != mOffSc || offdiff < -1 || offdiff > 1 {
			t.Errorf("EG OFF d%d %s: asm=%s/%d mk=%d  mirror=%s/%d mk=%d",
				depth, fen, aOffMv, aOffSc, aOffMk, mOffMv, mOffSc, mOffMk)
		}
		if amk != aOffMk || asc != aOffSc || amv != aOffMv {
			active++
		}
	}
	if active == 0 {
		t.Errorf("the endgame terms never changed a search over %d FENs — test is vacuous", len(egSearchFENs))
	}
	t.Logf("asm==mirror exact with ENDGAME TECHNIQUE ON (mask 0x1f, FEATURES2 0x08, d%d); "+
		"changed the search on %d/%d FENs", depth, active, len(egSearchFENs))
}

// TestEGTermCycleCost reports the measured cycle cost of the two endgame eval
// terms: one eval call, ON vs OFF, in endgames (where the gate opens) and in
// middlegames (where it must cost nothing beyond the shared gate compare).
func TestEGTermCycleCost(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	evalinit, evalAddr := labels["evalinit"], labels["eval"]
	evalCycles := func(fen string, ft2 byte) uint64 {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1f)
		SetFeatures2(m, defs, ft2)
		m.Mem.Main[defs["SEED"]] = 0
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		callSub(t, m, evalinit)
		before := m.Cycles
		callSub(t, m, evalAddr)
		return m.Cycles - before
	}
	report := func(label string, fens []string) {
		var off, eg, mop, both uint64
		for _, fen := range fens {
			off += evalCycles(fen, 0)
			eg += evalCycles(fen, ft2EGTech)
			mop += evalCycles(fen, ft2Mopup)
			both += evalCycles(fen, ft2EGTech|ft2Mopup)
		}
		n := uint64(len(fens))
		t.Logf("%s (%d FENs): eval OFF=%d  +EG=%d (%+d)  +MOPUP=%d (%+d)  +BOTH=%d (%+d) cycles/eval",
			label, n, off/n, eg/n, int64(eg-off)/int64(n), mop/n, int64(mop-off)/int64(n),
			both/n, int64(both-off)/int64(n))
	}
	for _, fen := range egFireFENs {
		t.Logf("  %-46s off=%4d eg=%5d (%+5d)", fen, evalCycles(fen, 0),
			evalCycles(fen, ft2EGTech), int64(evalCycles(fen, ft2EGTech))-int64(evalCycles(fen, 0)))
	}
	report("endgame (gate open)", egFireFENs)
	report("middlegame (gate shut)", egQuietFENs)

	// The number that actually predicts the SPRT: whole-search cycles per
	// node, ON vs OFF, at a fixed depth on real endgames (this weights the
	// term by how often eval runs and by the real position mix, and separates
	// the tree change from the per-eval cost).
	labelsSearch := labels["search"]
	searchCost := func(fen string, ft2 byte) (uint64, uint64) {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1f)
		SetFeatures2(m, defs, ft2)
		SetBudget(m, defs, 0, 5)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		var nodes uint64
		if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
			if pc == labelsSearch {
				nodes++
			}
		}); err != nil {
			t.Fatal(err)
		}
		return m.Cycles, nodes
	}
	var onCyc, onNodes, offCyc, offNodes uint64
	for _, fen := range egSearchFENs {
		c1, n1 := searchCost(fen, ft2EGTech)
		c0, n0 := searchCost(fen, 0)
		onCyc, onNodes, offCyc, offNodes = onCyc+c1, onNodes+n1, offCyc+c0, offNodes+n0
		t.Logf("  d5 %-46s off %9d cyc/%6d nodes = %4d | on %9d/%6d = %4d (%+.1f%%/node)",
			fen, c0, n0, c0/n0, c1, n1, c1/n1, 100*(float64(c1)/float64(n1)/(float64(c0)/float64(n0))-1))
	}
	t.Logf("ENDGAME SEARCH TAX (d5, %d FENs): off %d cyc/node, on %d cyc/node (%+.1f%%); "+
		"nodes %d -> %d", len(egSearchFENs), offCyc/offNodes, onCyc/onNodes,
		100*(float64(onCyc)/float64(onNodes)/(float64(offCyc)/float64(offNodes))-1),
		offNodes, onNodes)
}
