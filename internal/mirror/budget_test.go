package mirror

import (
	"math/rand/v2"
	"testing"
)

// TestBudgetDeterminism: SearchBudget is a pure function of (position,
// budget, features, dither seed) — the node-denominated cap never
// consults wall time, so two runs are bit-identical. This is the
// soundness gate for A/B: a budgeted game replays exactly.
func TestBudgetDeterminism(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, budget := range []uint64{2000, 8000, 30000} {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			run := func(seed byte) (Move, int, uint64, int) {
				e := NewEngine()
				e.Features = FtAll | FtSEE | FtHistory
				e.SetPosition(pos)
				e.Seed = seed
				m, s := e.SearchBudget(budget, MaxPly-1)
				return m, s, e.Nodes, e.MaxDepth
			}
			m1, s1, n1, d1 := run(0)
			m2, s2, n2, _ := run(0)
			if m1 != m2 || s1 != s2 || n1 != n2 {
				t.Errorf("budget %d %s: non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
					budget, fen, m1, s1, n1, m2, s2, n2)
			}
			if m1.From == NoSq {
				t.Errorf("budget %d %s: no move produced", budget, fen)
			}
			// The per-move budget bounds work once a capped iteration ran.
			// Depth 1 runs uncapped (so a move is always produced), so it
			// alone may overshoot on tactical positions with a big QS tree.
			// The bound is 2x budget, not 1x: the asm driver's hard-abort
			// limit is ABORTL = 2*BUDGET (asm/engine.s entry), and the
			// predictive soft gate is what normally keeps the spend near 1x.
			// A run that legitimately overshoots 1x under the asm-faithful
			// policy is not a bug, so the assertion tracks the real cap.
			if d1 >= 2 && n1 > 2*budget {
				t.Errorf("budget %d %s: spent %d nodes > 2x budget at depth %d", budget, fen, n1, d1)
			}
			t.Logf("budget %6d  %-52s best %s score %5d  nodes %6d  reached depth %d",
				budget, fen, m1.UCI(), s1, n1, d1)
		}
	}
}

// TestLMPSound: late-move pruning stays a pure function of the inputs
// (bit-identical replays) and never turns a node with legal moves into a
// false mate/stalemate (pruning only fires after >= Base legal moves).
func TestLMPSound(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	}
	lmps := []LMPParams{
		{Dmax: 2, Base: 3, Mult: 2},
		{Dmax: 3, Base: 3, Quad: 1},
		{Dmax: 2, Base: 3, Mult: 2, ExemptKillers: true},
		{Dmax: 2, Base: 3, Mult: 2, KeepChecks: true},
	}
	for _, lmp := range lmps {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			run := func() (Move, int, uint64) {
				e := NewEngine()
				e.Features = FtAll | FtSEE | FtHistory
				e.QS = QSParams{RecapAfter: 2}
				e.Ord = OrderParams{HistMalus: true}
				e.LMP = lmp
				e.SetPosition(pos)
				e.Seed = 0
				m, s := e.SearchBudget(20000, MaxPly-1)
				return m, s, e.Nodes
			}
			m1, s1, n1 := run()
			m2, s2, n2 := run()
			if m1 != m2 || s1 != s2 || n1 != n2 {
				t.Errorf("LMP %+v %s non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
					lmp, fen, m1, s1, n1, m2, s2, n2)
			}
			if m1.From == NoSq {
				t.Errorf("LMP %+v %s: false mate (no move) at a node with legal moves", lmp, fen)
			}
		}
	}
}

// TestBudgetReachesDepth: a bigger node budget must reach at least as
// deep as a smaller one (more nodes => more completed iterations), and
// node savings turn into depth — the whole premise of the mode.
func TestBudgetReachesDepth(t *testing.T) {
	pos, err := ParseFEN("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8")
	if err != nil {
		t.Fatal(err)
	}
	depthAt := func(feat byte, budget uint64) int {
		e := NewEngine()
		e.Features = feat
		e.Ord = OrderParams{HistMalus: true}
		e.SetPosition(pos)
		// MaxDepth after SearchBudget is the last STARTED iteration; the
		// last COMPLETED one is what mattered, but for a monotonicity
		// smoke test the started depth tracks budget closely enough.
		e.SearchBudget(budget, MaxPly-1)
		return e.MaxDepth
	}
	var prev int
	for _, b := range []uint64{1000, 4000, 16000, 64000} {
		d := depthAt(FtAll, b)
		if d < prev {
			t.Errorf("budget %d reached depth %d < previous %d (non-monotone)", b, d, prev)
		}
		prev = d
	}
	// With strong ordering the same budget should reach at least as deep
	// as the baseline (fewer nodes/iteration => more iterations fit).
	base := depthAt(FtAll, 16000)
	strong := depthAt(FtAll|FtSEE|FtHistory, 16000)
	t.Logf("depth @16000 nodes: baseline %d, SEE+history %d", base, strong)
	if strong < base {
		t.Errorf("strong ordering reached shallower depth (%d) than baseline (%d)", strong, base)
	}
}

// TestBudgetMatchRuns is a tiny end-to-end check that a node-budgeted
// match completes and is reproducible across two runs with the same seed.
func TestBudgetMatchRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	openings, err := GenOpenings([][]string{{"e2e4", "e7e5"}, {"d2d4", "d7d5"}}, 8, 6502)
	if err != nil {
		t.Fatal(err)
	}
	a := PlayerCfg{Features: FtAll | FtSEE | FtHistory, Weights: DefaultWeights, NodeBudget: 6000, Ord: OrderParams{HistMalus: true}}
	b := PlayerCfg{Features: FtAll, Weights: DefaultWeights, NodeBudget: 6000}
	// Per-pair RNG seeding makes the result independent of the worker
	// count: 1 worker and 4 workers must agree exactly.
	r1, err := Match(a, b, openings, 8, 1, 6502)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Match(a, b, openings, 8, 4, 6502)
	if err != nil {
		t.Fatal(err)
	}
	if *r1 != *r2 {
		t.Errorf("budgeted match not reproducible across worker counts: %v vs %v", r1, r2)
	}
	_ = rand.Int // keep import if trimmed
	t.Logf("SEE+history vs baseline @6000 nodes/move: %s", r1)
}
