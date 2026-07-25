package mirror

import (
	"math/rand/v2"
	"testing"
)

// ZZ: reviewer scratch. Deleted after the review.

// 1. Mirror symmetry of egEval over thousands of random-play positions.
func TestZZMirrorSymmetry(t *testing.T) {
	on := NewEngine()
	on.EG = EndgameDesigned
	on.EG.PhaseMax = 100 // no gate: exercise every position
	fl := NewEngine()
	fl.EG = on.EG

	rnd := rand.New(rand.NewPCG(7, 11))
	start := mustFEN(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	walker := NewEngine()
	bad := 0
	for game := 0; game < 400; game++ {
		gp := *start
		for ply := 0; ply < 160; ply++ {
			on.SetPosition(&gp)
			a := on.egEval()
			f := flipFEN(t, gp.FEN())
			fl.SetPosition(mustFEN(t, f))
			b := -fl.egEval()
			if a != b {
				bad++
				if bad <= 10 {
					t.Errorf("asym: %s => %+d ; flip %s => %+d", gp.FEN(), a, f, -b)
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
	t.Logf("asymmetries=%d", bad)
}

// 2. The Pass top-up must be EXACTLY pawnterm's passer set (same
// definition, same advancement index).
func TestZZPasserMatchesPawnterm(t *testing.T) {
	tbl := [8]int{3, 5, 7, 11, 13, 17, 19, 23}
	on := NewEngine()
	on.EG = EndgameParams{Enable: true, PhaseMax: 100, Pass: tbl}
	w := Weights{Passed: tbl}

	rnd := rand.New(rand.NewPCG(3, 5))
	start := mustFEN(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	walker := NewEngine()
	bad := 0
	for game := 0; game < 400; game++ {
		gp := *start
		for ply := 0; ply < 160; ply++ {
			on.SetPosition(&gp)
			got := on.egEval()
			f := extractPawnFeatures(&on.Pos)
			want := 0
			for r := range 8 {
				want += w.Passed[r] * (f.passedW[r] - f.passedB[r])
			}
			if got != want {
				bad++
				if bad <= 10 {
					t.Errorf("passer mismatch %s: egEval=%d pawnterm=%d", gp.FEN(), got, want)
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
	t.Logf("mismatches=%d", bad)
}

// 3. Determinism / no cross-call state: egEval must be a pure function.
func TestZZDeterministic(t *testing.T) {
	on := NewEngine()
	on.EG = EndgameDesigned
	on.EG.PhaseMax = 100
	rnd := rand.New(rand.NewPCG(13, 17))
	start := mustFEN(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	walker := NewEngine()
	for game := 0; game < 60; game++ {
		gp := *start
		for ply := 0; ply < 160; ply++ {
			on.SetPosition(&gp)
			a := on.egEval()
			for i := 0; i < 3; i++ {
				if b := on.egEval(); b != a {
					t.Fatalf("nondeterministic %s: %d vs %d", gp.FEN(), a, b)
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
}

// 4. Search-time robustness: run deep searches from endgame FENs with the
// EG term on and PhaseMax wide open, so pseudo-legal / king-captured nodes
// are exercised.
func TestZZSearchNoPanic(t *testing.T) {
	for _, fen := range []string{
		"8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1",
		"8/P4k2/5p1p/3b4/6P1/3pK3/3B4/8 w - - 4 1",
		"1b2k3/1P4p1/4p2p/4P3/3Q4/6P1/5P2/6K1 w - - 5 1",
		"4k3/PPPPPPPP/8/8/8/8/pppppppp/4K3 w - - 0 1",
		"4k3/8/8/8/8/8/PPPPPPPP/RNBQKBNR w - - 0 1",
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	} {
		e := NewEngine()
		e.EG = EndgameDesigned
		e.EG.PhaseMax = 100
		e.SetPosition(mustFEN(t, fen))
		m, s := e.SearchFixed(6)
		t.Logf("%-50s best=%v score=%d nodes=%d", fen, m, s, e.Nodes)
	}
}

// 5. A white pawn parked on rank 7 / black pawn on rank 0 (reachable only
// in a hand-built position, but the asm's pseudo-legal tree could see it):
// no OOB, no bogus index.
func TestZZPawnOnPromoRank(t *testing.T) {
	e := NewEngine()
	e.EG = EndgameDesigned
	e.EG.PhaseMax = 100
	for _, fen := range []string{
		"3P3k/8/8/8/8/8/8/K7 w - - 0 1",
		"k7/8/8/8/8/8/8/K2p4 b - - 0 1",
		"3Pk3/8/8/8/8/8/8/K7 w - - 0 1",
	} {
		p, err := ParseFEN(fen)
		if err != nil {
			t.Logf("%s: parse: %v", fen, err)
			continue
		}
		e.SetPosition(p)
		t.Logf("%-30s egEval=%+d", fen, e.egEval())
	}
}
