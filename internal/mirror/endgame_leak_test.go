package mirror

import (
	"math/rand/v2"
	"testing"
)

// TestEndgameNoMiddlegameLeak is the definitive gate-leak proof for the
// endgame-technique terms: over thousands of positions reached by random
// legal play, the EG-ON eval must EQUAL the EG-OFF eval at every position
// whose phase is above PhaseMax. Only low-phase endgames may differ.
// (Same shape as TestMopupNoMiddlegameLeak.)
func TestEndgameNoMiddlegameLeak(t *testing.T) {
	off := NewEngine() // EG zero value = OFF
	on := NewEngine()
	on.EG = DefaultEndgame

	rnd := rand.New(rand.NewPCG(0xA11CE, 0xB0B))
	start, err := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		t.Fatal(err)
	}

	var checked, gated, leaks int
	walker := NewEngine()
	for game := 0; game < 300; game++ {
		gp := *start
		for ply := 0; ply < 120; ply++ {
			off.SetPosition(&gp)
			on.SetPosition(&gp)
			off.Seed, on.Seed = 0, 0
			eOff := off.eval()
			eOn := on.eval()
			checked++
			if eOff != eOn {
				gated++
				if on.Pos.Phase > DefaultEndgame.PhaseMax {
					leaks++
					if leaks <= 5 {
						t.Errorf("LEAK: phase %d > %d but eval differs (off=%d on=%d) fen=%s",
							on.Pos.Phase, DefaultEndgame.PhaseMax, eOff, eOn, gp.FEN())
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
	t.Logf("checked=%d positions, gate-active(differ)=%d, middlegame-leaks=%d", checked, gated, leaks)
	if leaks != 0 {
		t.Fatalf("%d middlegame leaks: the phase gate is not tight", leaks)
	}
	if gated == 0 {
		t.Fatalf("the gate never fired: the leak result would be vacuous")
	}

	// Positive controls: the gate MUST fire on a real pawn endgame, and MUST
	// be silent in the opening.
	assertDiff := func(fen string, wantDiff bool) {
		off.SetPosition(mustFEN(t, fen))
		on.SetPosition(mustFEN(t, fen))
		off.Seed, on.Seed = 0, 0
		d := off.eval() != on.eval()
		if d != wantDiff {
			t.Errorf("gate control fen=%q: got differ=%v want %v (phase=%d)", fen, d, wantDiff, on.Pos.Phase)
		}
	}
	assertDiff("8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1", true)               // g137 pawn ending
	assertDiff("8/5p2/pk1P4/8/P5p1/3Pp1P1/2r1b1BP/4R1K1 b - - 0 1", true)         // g61 R+B ending (phase 6)
	assertDiff("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", false) // opening: silent
	assertDiff("r1bqkb1r/pppp1ppp/2n2n2/4p3/4P3/2N2N2/PPPP1PPP/R1BQKB1R w KQkq - 0 1", false)
}

// TestEndgameOffIdentical proves the OFF path is byte-identical: the search
// tree (nodes, best move, score) of an engine with EndgameParams{} must
// exactly match an engine that has no EG config at all, at every feature
// mask, including with the shipped mop-up on.
func TestEndgameOffIdentical(t *testing.T) {
	fens := []string{
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1",
		"8/8/8/4N1pk/1K3p1p/1P3P2/P5n1/8 w - - 0 1",
		"8/5p2/pk1P4/8/P5p1/3Pp1P1/2r1b1BP/4R1K1 b - - 0 1",
	}
	for _, mask := range []byte{0x00, 0x07, 0x1f} {
		for _, mop := range []MopupParams{{}, DefaultMopup} {
			for _, fen := range fens {
				base := NewEngine()
				base.Features, base.Mopup = mask, mop
				base.SetPosition(mustFEN(t, fen))
				bm, bs := base.SearchFixed(5)
				bn := base.Nodes

				zero := NewEngine()
				zero.Features, zero.Mopup = mask, mop
				zero.EG = EndgameParams{} // explicit OFF
				zero.SetPosition(mustFEN(t, fen))
				zm, zs := zero.SearchFixed(5)
				zn := zero.Nodes

				if bm != zm || bs != zs || bn != zn {
					t.Errorf("mask %#02x mop=%v fen=%s: OFF path differs base(%v %d %d) vs zero(%v %d %d)",
						mask, mop.Enable, fen, bm, bs, bn, zm, zs, zn)
				}
			}
		}
	}
}

// TestEndgameCosted checks the cycle tax is charged exactly on the eval
// calls where the gate fires, and never in a middlegame search.
func TestEndgameCosted(t *testing.T) {
	run := func(fen string, depth int) (uint64, uint64, uint64) {
		e := NewEngine()
		e.EG = DefaultEndgame
		e.Costs.EGTerm = EvalTermsCost(2)
		e.CycleTrack = true
		e.SetPosition(mustFEN(t, fen))
		e.SearchFixed(depth)
		return e.Cyc.Evals, e.Cyc.EGEvals, e.Cyc.Est
	}
	if evals, eg, _ := run("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 4); eg != 0 {
		t.Errorf("middlegame: EGEvals=%d of %d evals, want 0", eg, evals)
	}
	// The tax's real cycle SHARE in an endgame search — the number the port
	// recommendation rests on (the asm design is ~120 cyc, not 438, so this
	// is a pessimistic upper bound).
	for _, fen := range []string{
		"8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1",
		"8/4k3/R3P3/2P3K1/8/2r5/8/8 w - - 7 1",
		"8/5p2/pk1P4/8/P5p1/3Pp1P1/2r1b1BP/4R1K1 b - - 0 1",
	} {
		evals, eg, est := run(fen, 7)
		if eg == 0 {
			t.Errorf("%s: the gate never fired", fen)
			continue
		}
		tax := float64(eg) * EvalTermsCost(2)
		t.Logf("%-50s %d/%d evals taxed, %.2f%% of %d est cycles", fen, eg, evals,
			100*tax/float64(est), est)
	}
}

// TestEndgameSanity pins the term's judgment on textbook positions: the
// signs must match chess knowledge.
func TestEndgameSanity(t *testing.T) {
	// The DESIGNED set (incl. the two screened-out knobs) so the rejected
	// terms' logic stays covered.
	egOf := func(fen string) int {
		e := NewEngine()
		e.EG = EndgameDesigned
		e.SetPosition(mustFEN(t, fen))
		return e.egEval()
	}
	cases := []struct {
		name     string
		a, b     string // a should score HIGHER (white POV) than b
		wantMore bool
	}{
		{"king centralized beats king in the corner",
			"8/8/8/3K4/8/8/8/7k w - - 0 1", "K7/8/8/8/8/8/8/7k w - - 0 1", true},
		{"unstoppable passer (king outside the square) beats a caught one",
			"7k/8/8/8/8/8/P7/K7 w - - 0 1", "8/8/8/8/8/1k6/P7/K7 w - - 0 1", true},
		{"king ahead of its own passer beats king blocking from behind",
			"8/3k4/8/4K3/3P4/8/8/8 w - - 0 1", "8/3k4/8/8/3P4/3K4/8/8 w - - 0 1", true},
	}
	// RookBehind and Unstoppable need ISOLATED cases: the review found the
	// original "rook behind" case passed on the strength of Unstoppable's
	// +250 (position b blocked the pawn's path with the rook, killing the
	// square rule), so it would still have passed with RookBehind's sign
	// inverted. Screen each alone.
	egOnly := func(fen string, f func(*EndgameParams)) int {
		e := NewEngine()
		e.EG = EndgameParams{Enable: true, PhaseMax: EndgameDesigned.PhaseMax}
		f(&e.EG)
		e.SetPosition(mustFEN(t, fen))
		return e.egEval()
	}
	rb := func(fen string) int {
		return egOnly(fen, func(p *EndgameParams) { p.RookBehind = EndgameDesigned.RookBehind })
	}
	// Same pawn, same kings, ONLY the rook moves: behind (d1) vs beside (a5).
	if a, b := rb("7k/8/8/3P4/8/8/8/3R1K2 w - - 0 1"), rb("7k/8/8/R2P4/8/8/8/5K2 w - - 0 1"); a <= b {
		t.Errorf("RookBehind not isolated-correct: behind=%+d beside=%+d (want behind > beside)", a, b)
	} else {
		t.Logf("%-52s eg(a)=%+5d eg(b)=%+5d", "RookBehind alone: rook behind our passer > beside it", a, b)
	}
	// THEIR rook behind OUR passer must score for THEM (negative for us).
	if a := rb("7k/8/8/3P4/8/8/8/3r1K2 w - - 0 1"); a >= 0 {
		t.Errorf("RookBehind sign: enemy rook behind our passer scored %+d, want < 0", a)
	}
	// Unstoppable alone, with the path clear in BOTH positions so only the
	// square rule can differ.
	un := func(fen string) int {
		return egOnly(fen, func(p *EndgameParams) { p.Unstoppable = EndgameDesigned.Unstoppable })
	}
	if a, b := un("7k/8/8/8/8/8/P7/K7 w - - 0 1"), un("8/8/8/8/8/1k6/P7/K7 w - - 0 1"); a <= b {
		t.Errorf("Unstoppable not isolated-correct: outside=%+d inside=%+d", a, b)
	}
	for _, c := range cases {
		ea, eb := egOf(c.a), egOf(c.b)
		if (ea > eb) != c.wantMore {
			t.Errorf("%s: eg(a)=%d eg(b)=%d (want a>b = %v)", c.name, ea, eb, c.wantMore)
		} else {
			t.Logf("%-52s eg(a)=%+5d eg(b)=%+5d", c.name, ea, eb)
		}
	}

	// Mirror symmetry: flipping ranks and colors must negate the score.
	for _, fen := range []string{
		"8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1",
		"8/8/8/4N1pk/1K3p1p/1P3P2/P5n1/8 w - - 0 1",
		"8/5p2/pk1P4/8/P5p1/3Pp1P1/2r1b1BP/4R1K1 b - - 0 1",
		"7k/8/8/8/8/8/P7/K7 w - - 0 1",
		"8/1p6/1P6/8/6Pk/8/5K2/8 w - - 0 1",
	} {
		fl := flipFEN(t, fen)
		if a, b := egOf(fen), -egOf(fl); a != b {
			t.Errorf("mirror asymmetry: %s => %+d, flipped %s => %+d", fen, a, fl, -b)
		}
	}
}

// flipFEN mirrors a FEN vertically and swaps colors (a color-symmetry
// transform: any correct white-POV eval must negate under it).
func flipFEN(t *testing.T, fen string) string {
	t.Helper()
	p := mustFEN(t, fen)
	var b []byte
	for rank := 0; rank < 8; rank++ { // emit rank 1..8 as if it were 8..1
		empty := 0
		for file := 0; file < 8; file++ {
			pc := p.Board[rank*16+file]
			if pc == 0 {
				empty++
				continue
			}
			if empty > 0 {
				b = append(b, byte('0'+empty))
				empty = 0
			}
			c := " pnbrqk"[pc&TypeMask]
			if pc&ColorMask != 0 { // black becomes white
				c -= 32
			}
			b = append(b, byte(c))
		}
		if empty > 0 {
			b = append(b, byte('0'+empty))
		}
		if rank < 7 {
			b = append(b, '/')
		}
	}
	side := " w "
	if p.Side == 0 {
		side = " b "
	}
	return string(b) + side + "- - 0 1"
}
