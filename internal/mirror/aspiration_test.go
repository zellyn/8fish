package mirror

import "testing"

// aspVariants is the set of aspiration configs screened (plus the off
// baseline), reused by the soundness tests.
var aspVariants = []struct {
	name string
	asp  AspirationParams
}{
	{"off", AspirationParams{}},
	{"d15full", AspirationParams{Delta: 15, Policy: AspFull}},
	{"d25full", AspirationParams{Delta: 25, Policy: AspFull}},
	{"d50full", AspirationParams{Delta: 50, Policy: AspFull}},
	{"d25prog", AspirationParams{Delta: 25, Policy: AspProgressive}},
	{"d25asym", AspirationParams{Delta: 25, Policy: AspAsym}},
}

// TestAspirationDeterminism: with aspiration on (any policy), SearchBudget
// stays a pure function of (position, budget, features, seed) — two runs
// with the same seed replay bit-identically (move, score, nodes, and the
// aspiration counters). This is the A/B soundness gate: an aspiration game
// replays exactly, so screens are reproducible.
func TestAspirationDeterminism(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, v := range aspVariants {
		for _, budget := range []uint64{8000, 30000} {
			for _, fen := range fens {
				pos, err := ParseFEN(fen)
				if err != nil {
					t.Fatal(err)
				}
				run := func() (Move, int, uint64, uint64) {
					e := NewEngine() // asm-matched: FtAll, recap2, shipped weights
					e.Asp = v.asp
					e.SetPosition(pos)
					e.Seed = 0
					m, s := e.SearchBudget(budget, MaxPly-1)
					researches := e.AspFailLow + e.AspFailHigh
					return m, s, e.Nodes, researches
				}
				m1, s1, n1, r1 := run()
				m2, s2, n2, r2 := run()
				if m1 != m2 || s1 != s2 || n1 != n2 || r1 != r2 {
					t.Errorf("asp %s budget %d %s non-deterministic: (%v,%d,%d,%d) vs (%v,%d,%d,%d)",
						v.name, budget, fen, m1, s1, n1, r1, m2, s2, n2, r2)
				}
				if m1.From == NoSq {
					t.Errorf("asp %s budget %d %s: no move produced", v.name, budget, fen)
				}
			}
		}
	}
}

// TestAspirationMateSound: aspiration windows must never clip a mate score.
// With the budget slack (MaxIters caps the ID loop, budget never binds),
// every variant completes iterations 1..depth identically to the off
// baseline, and the returned score must be the exact mate score — the same
// value the full-window search reports. A window that clipped the mate zone
// would return a truncated (non-mate) score here.
func TestAspirationMateSound(t *testing.T) {
	cases := []struct {
		fen   string
		depth int
		want  int
	}{
		// KR mate in 2 (asm id test): mating at ply 3.
		{"k7/8/2K5/8/8/8/8/6R1 w - - 0 1", 6, Mate - 3},
		// Back-rank mate in 1.
		{"6k1/5ppp/8/8/8/8/8/4R2K w - - 0 1", 4, Mate - 1},
	}
	const unlimited = 1_000_000_000_000
	for _, tc := range cases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range aspVariants {
			e := NewEngine()
			e.Asp = v.asp
			e.SetPosition(pos)
			e.Seed = 0
			m, s := e.SearchBudget(unlimited, tc.depth)
			if s != tc.want {
				t.Errorf("asp %s %s: score %d, want mate %d (clipped?)", v.name, tc.fen, s, tc.want)
			}
			if s < mateZoneLo {
				t.Errorf("asp %s %s: score %d not in winning mate zone", v.name, tc.fen, s)
			}
			if m.From == NoSq {
				t.Errorf("asp %s %s: no move produced", v.name, tc.fen)
			}
		}
	}
}

// TestAspirationOffIsNoop: with Delta == 0 the budgeted search is identical
// (move, score, nodes) to the pre-aspiration path, and opens zero windows —
// the guarantee the parity gates rely on, asserted directly here.
func TestAspirationOffIsNoop(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		base := NewEngine()
		base.SetPosition(pos)
		bm, bs := base.SearchBudget(30000, MaxPly-1)

		off := NewEngine()
		off.Asp = AspirationParams{} // explicit off
		off.SetPosition(pos)
		om, os := off.SearchBudget(30000, MaxPly-1)

		if bm != om || bs != os || base.Nodes != off.Nodes {
			t.Errorf("%s: Asp{} not a no-op: (%v,%d,%d) vs (%v,%d,%d)",
				fen, bm, bs, base.Nodes, om, os, off.Nodes)
		}
		if off.AspWindows != 0 {
			t.Errorf("%s: Asp off opened %d windows", fen, off.AspWindows)
		}
	}
}
