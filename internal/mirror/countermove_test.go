package mirror

import "testing"

// cmVariants is the set of countermove configs screened (plus the off
// baseline), reused by the soundness and node-count tests.
var cmVariants = []struct {
	name string
	cm   CountermoveParams
}{
	{"off", CountermoveParams{}},
	{"a-after", CountermoveParams{Indexing: 1}},
	{"a-before", CountermoveParams{Indexing: 1, BeforeKillers: true}},
	{"b-after", CountermoveParams{Indexing: 2}},
	{"b-before", CountermoveParams{Indexing: 2, BeforeKillers: true}},
	{"a-after-persist", CountermoveParams{Indexing: 1, Persist: true}},
	{"b-after-persist", CountermoveParams{Indexing: 2, Persist: true}},
}

// TestCountermoveOffIsNoop: with Indexing == 0 the budgeted search is
// identical (move, score, nodes) to the pre-countermove path — the
// guarantee the parity gates rely on, asserted directly here.
func TestCountermoveOffIsNoop(t *testing.T) {
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
		bn := base.Nodes

		off := NewEngine()
		off.CM = CountermoveParams{} // explicit off
		off.SetPosition(pos)
		om, os := off.SearchBudget(30000, MaxPly-1)
		if om != bm || os != bs || off.Nodes != bn {
			t.Errorf("%s: CM-off differs from baseline: (%v,%d,%d) vs (%v,%d,%d)",
				fen, om, os, off.Nodes, bm, bs, bn)
		}
	}
}

// TestCountermoveDeterminism: with the countermove heuristic on (any
// variant), SearchBudget stays a pure function of (position, budget,
// features, seed) — two runs with the same seed replay bit-identically
// (move, score, nodes). This is the A/B soundness gate: a countermove game
// replays exactly, so screens are reproducible.
func TestCountermoveDeterminism(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, v := range cmVariants {
		for _, budget := range []uint64{8000, 30000} {
			for _, fen := range fens {
				pos, err := ParseFEN(fen)
				if err != nil {
					t.Fatal(err)
				}
				run := func() (Move, int, uint64) {
					e := NewEngine() // asm-matched: FtAll, recap2, shipped weights
					e.CM = v.cm
					e.SetPosition(pos)
					e.Seed = 0
					m, s := e.SearchBudget(budget, MaxPly-1)
					return m, s, e.Nodes
				}
				m1, s1, n1 := run()
				m2, s2, n2 := run()
				if m1 != m2 || s1 != s2 || n1 != n2 {
					t.Errorf("cm %s budget %d %s non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
						v.name, budget, fen, m1, s1, n1, m2, s2, n2)
				}
				if m1.From == NoSq {
					t.Errorf("cm %s budget %d %s: no move produced", v.name, budget, fen)
				}
			}
		}
	}
}

// TestCountermoveMateSound: the countermove heuristic only reorders quiet
// moves, so it must never change a fixed-depth mate verdict. Every variant
// must report the exact mate score the baseline search reports.
func TestCountermoveMateSound(t *testing.T) {
	cases := []struct {
		fen   string
		depth int
		want  int
	}{
		{"k7/8/2K5/8/8/8/8/6R1 w - - 0 1", 6, Mate - 3}, // KR mate in 2
		{"6k1/5ppp/8/8/8/8/8/4R2K w - - 0 1", 4, Mate - 1}, // back-rank mate in 1
	}
	for _, tc := range cases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range cmVariants {
			e := NewEngine()
			e.CM = v.cm
			e.SetPosition(pos)
			e.Seed = 0
			m, s := e.SearchFixed(tc.depth)
			if s != tc.want {
				t.Errorf("cm %s %s: score %d, want mate %d", v.name, tc.fen, s, tc.want)
			}
			if m.From == NoSq {
				t.Errorf("cm %s %s: no move produced", v.name, tc.fen)
			}
		}
	}
}

// TestCountermoveSweepNodes: depth-6 node counts across countermove
// variants under the ASM-MATCHED ordering config (mask 0x1f = TT + killers
// + two-tier MVV, NO SEE, NO history — the real asm ordering, which routes
// through the five-pass moveLoop). Confirms the countermove is wired into
// that path and quantifies the tree cut the asm port would see. Strength is
// judged under a node budget by the self-play screen, not here.
func TestCountermoveSweepNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	adopt := func(cm CountermoveParams) *Engine {
		e := NewEngine()          // FtAll (0x1f), recap2 QS, shipped weights
		e.CM = cm
		return e
	}
	fens := benchFENs(t)
	var baseTotal uint64
	for vi, v := range cmVariants {
		var total uint64
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			eng := adopt(v.cm)
			eng.SetPosition(pos)
			eng.SearchFixed(6)
			total += eng.Nodes
		}
		if vi == 0 {
			baseTotal = total
		}
		t.Logf("%-16s total %9d nodes (%+.2f%% vs baseline)",
			v.name, total, 100*(float64(total)/float64(baseTotal)-1))
	}
}
