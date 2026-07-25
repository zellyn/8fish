package mirror

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

// diagFENs are the loss-diagnosis positions (docs/results.md 2026-07-25,
// "LOSS DIAGNOSIS"): 8fish is to move and went on to LOSE. blindGap is the
// shallow eval minus a 5x-deeper oracle eval, i.e. how much our static
// judgment OVER-rates the position. The king-safety cluster has a median
// blindGap of 301 (the deep search sees the attack, the shallow one does
// not); the positional cluster's median is only 59 (our eval agrees with
// the oracle, so depth cannot help).
var diagFENs = []struct {
	name     string
	fen      string
	kind     string
	blindGap int // 0 = not reported individually
}{
	{"ks-1860", "8/3r1k2/2R3pp/p3bp2/N3p2P/3nP1P1/4KPB1/8 w - - 0 1", "kingsafety", 1860},
	{"ks-644", "8/8/6p1/P2p1k2/5P2/3KQ3/1q6/8 w - - 0 1", "kingsafety", 644},
	{"ks-432", "5k2/3n3Q/3r2p1/4p1K1/4P3/7P/3p2P1/8 w - - 0 1", "kingsafety", 432},
	{"pos-a", "5r1k/pR5p/3p3p/4pP2/3q3b/2rB4/4QPP1/3R2K1 w - - 0 1", "positional", 0},
	{"pos-b", "4rrk1/ppp2ppp/8/3PbN2/6P1/1Pq1P2P/4Q3/3R1RK1 w - - 0 1", "positional", 0},
}

// TestMidDiagnosisProbe is SCREEN A: does the term move the SHALLOW SEARCH
// score toward the deep oracle's verdict on the diagnosis positions? This
// reproduces the diagnosis's own instrument: blindGap = shallow score at the
// operating budget minus a 5x-deeper oracle score (positive = we over-rate
// the position). A useful king-safety term SHRINKS |gap| toward 0.
//
// The static eval shift is reported too, since that is what the term
// actually changes; the search-score gap is what matters for play.
func TestMidDiagnosisProbe(t *testing.T) {
	const budget = 143_000_000
	groups := []struct {
		name string
		p    MidParams
	}{
		{"OFF", MidParams{}},
		{"ks", MidKingSafety},
		{"pos", MidPositional},
		{"both", DefaultMid},
	}
	t.Logf("static eval shift (side-to-move POV) and shallow-minus-oracle gap per group")
	t.Logf("%-9s %-11s %5s | %-28s | %-34s", "fen", "kind", "phase",
		"static eval: OFF then delta", "gap = shallow(143M) - oracle(715M)")
	for _, d := range diagFENs {
		pos := mustFEN(t, d.fen)
		// Oracle: the same engine, terms OFF, 5x the operating budget.
		or := NewEngine()
		or.SetPosition(pos)
		or.Seed = 0
		_, oracle := or.SearchCycleBudget(5*budget, MaxPly-1)

		evalRow, gapRow, base := "", "", 0
		for i, g := range groups {
			e := NewEngine()
			e.Mid = g.p
			e.SetPosition(pos)
			e.Seed = 0
			ev := e.eval()
			if i == 0 {
				base = ev
				evalRow += fmt.Sprintf(" %6d", ev)
			} else {
				evalRow += fmt.Sprintf(" %+6d", ev-base)
			}
			s := NewEngine()
			s.Mid = g.p
			s.SetPosition(pos)
			s.Seed = 0
			_, sc := s.SearchCycleBudget(budget, MaxPly-1)
			gapRow += fmt.Sprintf(" %s=%+d", g.name, sc-oracle)
		}
		t.Logf("%-9s %-11s %5d |%s |%s (oracle %d, reported blindGap %d)",
			d.name, d.kind, or.Pos.Phase, evalRow, gapRow, oracle, d.blindGap)
	}
}

// TestMidNoEndgameLeak is the gate-leak proof: over positions reached by
// random legal play, the Mid-ON eval must EQUAL the Mid-OFF eval at every
// position with Phase < PhaseMin — so the endgame-technique terms and the
// mop-up (both gated at Phase <= 6) can never be perturbed. Run with the
// shipped endgame set + mop-up ON, which is the real shipping combination.
func TestMidNoEndgameLeak(t *testing.T) {
	off := NewEngine()
	off.EG, off.Mopup = DefaultEndgame, DefaultMopup
	on := NewEngine()
	on.EG, on.Mopup = DefaultEndgame, DefaultMopup
	on.Mid = DefaultMid

	rnd := rand.New(rand.NewPCG(0x5A11, 0xF00D))
	start := mustFEN(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")

	var checked, gated, leaks, lowPhase int
	walker := NewEngine()
	for game := 0; game < 300; game++ {
		gp := *start
		for ply := 0; ply < 140; ply++ {
			off.SetPosition(&gp)
			on.SetPosition(&gp)
			off.Seed, on.Seed = 0, 0
			eOff, eOn := off.eval(), on.eval()
			checked++
			if on.Pos.Phase < DefaultMid.PhaseMin {
				lowPhase++
			}
			if eOff != eOn {
				gated++
				if on.Pos.Phase < DefaultMid.PhaseMin {
					leaks++
					if leaks <= 5 {
						t.Errorf("LEAK: phase %d < %d but eval differs (off=%d on=%d) fen=%s",
							on.Pos.Phase, DefaultMid.PhaseMin, eOff, eOn, gp.FEN())
					}
				}
			}
			moves := legalMoves(walker, &gp)
			if len(moves) == 0 {
				break
			}
			m := moves[rnd.IntN(len(moves))]
			walker.SetPosition(&gp)
			walker.make(m)
			gp = walker.Pos
			gp.Ply = 0
		}
	}
	t.Logf("checked=%d positions (%d below the gate), gate-active(differ)=%d, endgame-leaks=%d",
		checked, lowPhase, gated, leaks)
	if leaks != 0 {
		t.Fatalf("%d endgame leaks: the phase gate is not tight", leaks)
	}
	if gated == 0 || lowPhase == 0 {
		t.Fatalf("vacuous: gated=%d lowPhase=%d", gated, lowPhase)
	}

	// Positive controls: silent in a real endgame, active in the opening.
	assertDiff := func(fen string, wantDiff bool) {
		off.SetPosition(mustFEN(t, fen))
		on.SetPosition(mustFEN(t, fen))
		off.Seed, on.Seed = 0, 0
		d := off.eval() != on.eval()
		if d != wantDiff {
			t.Errorf("gate control fen=%q: got differ=%v want %v (phase=%d)",
				fen, d, wantDiff, on.Pos.Phase)
		}
	}
	assertDiff("8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1", false)              // pawn ending: silent
	assertDiff("8/5p2/pk1P4/8/P5p1/3Pp1P1/2r1b1BP/4R1K1 b - - 0 1", false)        // R+B, phase 6: silent
	// Symmetric openings cancel exactly (a correctness property, not a
	// silent gate), so the ACTIVE controls must be asymmetric middlegames.
	assertDiff("4rrk1/ppp2ppp/8/3PbN2/6P1/1Pq1P2P/4Q3/3R1RK1 w - - 0 1", true)
	assertDiff("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", true)
}

// TestMidOffIdentical proves the OFF path is byte-identical: an engine with
// MidParams{} must reproduce the search of an engine that has no Mid config
// at all, at every feature mask, with the shipped mop-up/endgame on.
func TestMidOffIdentical(t *testing.T) {
	fens := []string{
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"4rrk1/ppp2ppp/8/3PbN2/6P1/1Pq1P2P/4Q3/3R1RK1 w - - 0 1",
		"8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1",
	}
	for _, mask := range []byte{0x00, 0x07, 0x1f} {
		for _, eg := range []EndgameParams{{}, DefaultEndgame} {
			for _, fen := range fens {
				base := NewEngine()
				base.Features, base.EG, base.Mopup = mask, eg, DefaultMopup
				base.SetPosition(mustFEN(t, fen))
				bm, bs := base.SearchFixed(5)
				bn := base.Nodes

				zero := NewEngine()
				zero.Features, zero.EG, zero.Mopup = mask, eg, DefaultMopup
				zero.Mid = MidParams{} // explicit OFF
				zero.SetPosition(mustFEN(t, fen))
				zm, zs := zero.SearchFixed(5)
				zn := zero.Nodes
				if bm != zm || bs != zs || bn != zn {
					t.Errorf("mask %#02x eg=%v fen=%s: OFF path differs base(%v %d %d) vs zero(%v %d %d)",
						mask, eg.Enable, fen, bm, bs, bn, zm, zs, zn)
				}
			}
		}
	}
}

// TestMidCosted checks the cycle tax is charged exactly on the eval calls
// where the gate fires, and that a low-phase (endgame) search pays nothing.
func TestMidCosted(t *testing.T) {
	run := func(fen string, cost float64) (evals, midEvals uint64, est uint64) {
		e := NewEngine()
		e.Mid = DefaultMid
		e.Costs.MidTerm = cost
		e.CycleTrack = true
		e.SetPosition(mustFEN(t, fen))
		e.SearchFixed(4)
		return e.Cyc.Evals, e.Cyc.MidEvals, e.Cyc.Est
	}
	// Middlegame: the gate fires on (nearly) every eval call.
	ev, mid, est0 := run("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 0)
	_, mid2, est1 := run("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 438)
	if mid == 0 || mid != mid2 {
		t.Fatalf("middlegame gate did not fire consistently: %d vs %d", mid, mid2)
	}
	if got, want := est1-est0, uint64(438)*mid; got != want {
		t.Errorf("tax mismatch: est delta %d want %d", got, want)
	}
	t.Logf("middlegame: evals=%d gated=%d (%.1f%%), tax at 438 = %.2f%% of total cycles",
		ev, mid, 100*float64(mid)/float64(ev), 100*float64(est1-est0)/float64(est1))

	// Endgame: below the gate, nothing is charged.
	_, midEG, _ := run("8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1", 438)
	if midEG != 0 {
		t.Errorf("endgame search charged the middlegame tax %d times", midEG)
	}
}

// TestMidSanity pins each term's mechanics on a crafted position, so a
// weight change cannot silently break the formula.
func TestMidSanity(t *testing.T) {
	probe := func(fen string, p MidParams) int {
		e := NewEngine()
		e.Mid = p
		e.SetPosition(mustFEN(t, fen))
		return e.midEval()
	}
	one := func(set func(*MidParams)) MidParams {
		p := MidParams{Enable: true, PhaseMin: 7}
		set(&p)
		return p
	}

	// Outpost: white Nd5 protected by e4-pawn, no black pawn on c/e ahead.
	// Black Nd4 is NOT protected (no black pawn on c5/e5).
	got := probe("3qk3/8/8/3N4/3nP3/8/8/4K2Q w - - 0 1", one(func(p *MidParams) { p.OutpostN = 16 }))
	if got != 16 {
		t.Errorf("outpost: got %d want 16", got)
	}
	// Same knight, but a black c-pawn can attack d5 -> no outpost.
	got = probe("3qk3/2p5/8/3N4/4P3/8/8/4K2Q w - - 0 1", one(func(p *MidParams) { p.OutpostN = 16 }))
	if got != 0 {
		t.Errorf("outpost (attackable): got %d want 0", got)
	}
	// Backward: white b2 pawn, neighbours a4+c4 both advanced, and b3 is
	// controlled by a black a4/c4 pawn... construct: white b2, white a4/c4,
	// black a3? Use black c3 to control b4? Simplest: white pawn b2,
	// neighbours a3/c3 (own, advanced), black pawn a4 controls b3.
	got = probe("3qk3/8/8/8/p7/P1P5/1P6/4K2Q w - - 0 1", one(func(p *MidParams) { p.Backward = 8 }))
	if got != -8 {
		t.Errorf("backward: got %d want -8", got)
	}
	// Phalanx: white d4+e4 duo -> both get the bonus.
	got = probe("3qk3/8/8/8/3PP3/8/8/4K2Q w - - 0 1", one(func(p *MidParams) { p.Phalanx = 5 }))
	if got != 10 {
		t.Errorf("phalanx: got %d want 10", got)
	}
	// Bad bishop: white Bc1 (dark), white pawns d4 (light? d4 = file 3 rank 3
	// -> (3+3)&1 = 0 dark) ... just assert the sign is negative when the
	// bishop's own complex is crowded.
	dark := probe("3qk3/8/8/8/8/2P1P3/8/2B1K2Q w - - 0 1", one(func(p *MidParams) { p.BadBishop = 3 }))
	if dark >= 0 {
		t.Errorf("bad bishop: got %d, want negative (own complex crowded)", dark)
	}
	// Blocked centre pawn: white d2 pawn with Bd3 in front.
	got = probe("3qk3/8/8/8/8/3B4/3P4/4K2Q w - - 0 1", one(func(p *MidParams) { p.BlockedCtr = 12 }))
	if got != -12 {
		t.Errorf("blocked centre: got %d want -12", got)
	}
	// King safety, open file zone: white Kg1 with no pawns at all vs black
	// Kg8 fully sheltered -> white must be penalized.
	got = probe("q5k1/5ppp/8/8/8/8/8/5RKQ w - - 0 1", one(func(p *MidParams) {
		p.KSOpen, p.KSFullOpen, p.KSGap = 12, 8, 5
	}))
	if got >= 0 {
		t.Errorf("king shield: got %d want negative for the naked white king", got)
	}
	// Attack units: three black pieces next to Kg1, none near Kg8.
	got = probe("6k1/8/8/8/8/5nq1/6r1/5RK1 w - - 0 1", one(func(p *MidParams) {
		p.KSAtk = DefaultMid.KSAtk
	}))
	if got > -50 {
		t.Errorf("attack units: got %d, want a large negative penalty", got)
	}
	// Exposure scales with the enemy's heavy material: full with a queen,
	// half with rooks only, nothing without either. White Kd3 has CMD 1, so
	// x = 3-1 = 2 -> 28 / 14 / 0 at KSExposed 14.
	withQ := probe("4k2q/8/8/8/8/3K4/8/6RR w - - 0 1", one(func(p *MidParams) { p.KSExposed = 14 }))
	rookOnly := probe("3rk2r/8/8/8/8/3K4/8/6RR w - - 0 1", one(func(p *MidParams) { p.KSExposed = 14 }))
	noHeavy := probe("1nbknb2/8/8/8/8/3K4/8/1NB2BN1 w - - 0 1", one(func(p *MidParams) { p.KSExposed = 14 }))
	if withQ != -28 || rookOnly != -14 || noHeavy != 0 {
		t.Errorf("exposure: queen=%d (want -28) rooksOnly=%d (want -14) noHeavy=%d (want 0)",
			withQ, rookOnly, noHeavy)
	}
	// Enemy pawns next to the king count as attack units.
	pawnStorm := probe("q5k1/5ppp/8/8/8/5ppp/8/5RKQ w - - 0 1", one(func(p *MidParams) {
		p.KSAtk, p.KSPawn = DefaultMid.KSAtk, 1
	}))
	if pawnStorm >= 0 {
		t.Errorf("pawn storm: got %d, want negative (3 black pawns at the white king)", pawnStorm)
	}
	// Mirror symmetry: a colour-flipped position must give the negated score.
	for _, fen := range []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"4rrk1/ppp2ppp/8/3PbN2/6P1/1Pq1P2P/4Q3/3R1RK1 w - - 0 1",
		"5r1k/pR5p/3p3p/4pP2/3q3b/2rB4/4QPP1/3R2K1 w - - 0 1",
	} {
		a := probe(fen, DefaultMid)
		b := probe(flipFEN(t, fen), DefaultMid)
		if a != -b {
			t.Errorf("not colour-symmetric: %s -> %d, flipped -> %d", fen, a, b)
		}
	}
}
