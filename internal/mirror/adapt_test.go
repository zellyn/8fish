package mirror

import (
	"os"
	"testing"
)

// Soundness gates for the modern-technique adaptation screens: cheap quiet
// history (qhist.go), IIR (iir.go), razoring + adaptive null-R (razor.go),
// and the TT replacement policy (ttrepl.go). Convention as elsewhere:
// zero value must be a byte-identical no-op, every variant must be
// deterministic under a budget, and pure-reordering features must not
// change a fixed-depth mate verdict.

var adaptFENs = []string{
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
}

// adaptConfigs enumerates one representative ON config per feature plus
// combinations, applied to an engine by a setter.
var adaptConfigs = []struct {
	name string
	set  func(e *Engine)
}{
	{"qhist-part-f1", func(e *Engine) { e.QHist = QHistParams{Form: 1, PartThresh: 8} }},
	{"qhist-part-f2", func(e *Engine) { e.QHist = QHistParams{Form: 2, PartThresh: 8} }},
	{"qhist-part-f3", func(e *Engine) { e.QHist = QHistParams{Form: 3, PartThresh: 8} }},
	{"qhist-lmr", func(e *Engine) { e.QHist = QHistParams{Form: 2, LMRHigh: 32, LMRLowThresh: 1} }},
	{"qhist-lmp", func(e *Engine) {
		e.QHist = QHistParams{Form: 2, LMPThresh: 8}
		e.LMP = LMPParams{Dmax: 3, Base: 3, Mult: 2}
	}},
	{"qhist-all", func(e *Engine) {
		e.QHist = QHistParams{Form: 2, PartThresh: 8, LMRHigh: 32, LMRLowThresh: 1, LMPThresh: 8}
		e.LMP = LMPParams{Dmax: 3, Base: 3, Mult: 2}
	}},
	{"iir", func(e *Engine) { e.IIR = IIRParams{MinRem: 4} }},
	{"iir-anyw", func(e *Engine) { e.IIR = IIRParams{MinRem: 3, AnyWindow: true} }},
	{"razor", func(e *Engine) { e.Razor = RazorParams{M1: 200, M2: 400} }},
	{"razor-r2", func(e *Engine) { e.Razor = RazorParams{M2: 300} }},
	{"nullr", func(e *Engine) { e.NullR = NullRParams{DeepR: 3, MinRem: 6} }},
	{"ttrepl", func(e *Engine) { e.TTRepl = TTReplParams{DepthPref: true} }},
	{"stack", func(e *Engine) {
		e.QHist = QHistParams{Form: 2, PartThresh: 8, LMPThresh: 8}
		e.LMP = LMPParams{Dmax: 3, Base: 3, Mult: 2}
		e.IIR = IIRParams{MinRem: 4}
		e.Razor = RazorParams{M2: 300}
		e.NullR = NullRParams{DeepR: 3, MinRem: 6}
		e.TTRepl = TTReplParams{DepthPref: true}
	}},
}

// TestAdaptOffIsNoop: an engine with every new feature left at its zero
// value replays the baseline search bit-identically (move, score, nodes)
// — the guarantee the asm parity gates rely on.
func TestAdaptOffIsNoop(t *testing.T) {
	for _, fen := range adaptFENs {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		base := NewEngine()
		base.SetPosition(pos)
		bm, bs := base.SearchBudget(30000, MaxPly-1)
		bn := base.Nodes

		off := NewEngine()
		off.QHist = QHistParams{}
		off.IIR = IIRParams{}
		off.Razor = RazorParams{}
		off.NullR = NullRParams{}
		off.TTRepl = TTReplParams{}
		off.SetPosition(pos)
		om, os := off.SearchBudget(30000, MaxPly-1)
		if om != bm || os != bs || off.Nodes != bn {
			t.Errorf("%s: all-off differs from baseline: (%v,%d,%d) vs (%v,%d,%d)",
				fen, om, os, off.Nodes, bm, bs, bn)
		}
	}
}

// TestAdaptChangesTree: each ON config must actually change the budgeted
// search tree on at least one bench position — the anti-vacuity gate
// (a screen of a feature that never fires measures nothing).
func TestAdaptChangesTree(t *testing.T) {
	for _, cfg := range adaptConfigs {
		changed := false
		for _, fen := range adaptFENs {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			// 120k nodes reaches the depths where the deep-node features
			// (NullR MinRem 6, IIR MinRem 4) actually fire.
			base := NewEngine()
			base.SetPosition(pos)
			base.SearchBudget(120000, MaxPly-1)

			on := NewEngine()
			cfg.set(on)
			on.SetPosition(pos)
			on.SearchBudget(120000, MaxPly-1)
			if on.Nodes != base.Nodes {
				changed = true
				break
			}
		}
		if !changed {
			t.Errorf("%s: identical node counts on every bench position (vacuous config)", cfg.name)
		}
	}
}

// TestAdaptDeterminism: every ON config replays bit-identically under both
// node and cycle budgets — the reproducibility gate for the screens.
func TestAdaptDeterminism(t *testing.T) {
	for _, cfg := range adaptConfigs {
		for _, fen := range adaptFENs {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			run := func(cycles bool) (Move, int, uint64) {
				e := NewEngine()
				cfg.set(e)
				e.SetPosition(pos)
				e.Seed = 0
				if cycles {
					m, s := e.SearchCycleBudget(20000000, MaxPly-1)
					return m, s, e.Nodes
				}
				m, s := e.SearchBudget(20000, MaxPly-1)
				return m, s, e.Nodes
			}
			for _, cycles := range []bool{false, true} {
				m1, s1, n1 := run(cycles)
				m2, s2, n2 := run(cycles)
				if m1 != m2 || s1 != s2 || n1 != n2 {
					t.Errorf("%s cycles=%v %s non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
						cfg.name, cycles, fen, m1, s1, n1, m2, s2, n2)
				}
				if m1.From == NoSq {
					t.Errorf("%s cycles=%v %s: no move produced", cfg.name, cycles, fen)
				}
			}
		}
	}
}

// TestAdaptMateSound: the pure-REORDERING configs (quiet-history partition,
// TT replacement) must never change a fixed-depth mate verdict. The
// depth-CHANGING features (IIR, razoring, deep null-R, history-gated
// LMR/LMP) are excluded by design — they trade exactness at fixed depth
// for budget, like LMR/futility before them.
func TestAdaptMateSound(t *testing.T) {
	cases := []struct {
		fen   string
		depth int
		want  int
	}{
		{"k7/8/2K5/8/8/8/8/6R1 w - - 0 1", 6, Mate - 3},    // KR mate in 2
		{"6k1/5ppp/8/8/8/8/8/4R2K w - - 0 1", 4, Mate - 1}, // back-rank mate in 1
	}
	reorderers := []struct {
		name string
		set  func(e *Engine)
	}{
		{"qhist-part-f1", func(e *Engine) { e.QHist = QHistParams{Form: 1, PartThresh: 8} }},
		{"qhist-part-f2", func(e *Engine) { e.QHist = QHistParams{Form: 2, PartThresh: 8} }},
		{"qhist-part-f3", func(e *Engine) { e.QHist = QHistParams{Form: 3, PartThresh: 8} }},
		{"ttrepl", func(e *Engine) { e.TTRepl = TTReplParams{DepthPref: true} }},
	}
	for _, tc := range cases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range reorderers {
			e := NewEngine()
			v.set(e)
			e.SetPosition(pos)
			e.Seed = 0
			m, s := e.SearchFixed(tc.depth)
			if s != tc.want {
				t.Errorf("%s %s: score %d, want mate %d", v.name, tc.fen, s, tc.want)
			}
			if m.From == NoSq {
				t.Errorf("%s %s: no move produced", v.name, tc.fen)
			}
		}
	}
}

// TestQHistDiag (QHIST_DIAG=1): plays one cycle-budgeted self-play game
// with the quiet-history table maintained (form 2, no uses armed, so the
// game is the baseline game) and prints, every 10 plies, the nonzero-
// counter histogram and the fraction of counters clearing candidate
// partition thresholds — the calibration for PartThresh/LMPThresh.
func TestQHistDiag(t *testing.T) {
	if os.Getenv("QHIST_DIAG") == "" {
		t.Skip("set QHIST_DIAG=1 to run the threshold calibration diagnostic")
	}
	pos, err := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	mk := func() *Engine {
		e := NewEngine()
		e.CheckExt = CheckExtParams{MaxExt: 1}
		e.QHist = QHistParams{Form: 2} // table maintained, no uses armed
		return e
	}
	w, b := mk(), mk()
	engines := [2]*Engine{w, b}
	gp := *pos
	report := func(e *Engine, tag string, ply int) {
		var hist [9]int // log2 buckets of counter values
		total, nz := 0, 0
		thresh := []int{2, 4, 8, 16, 32, 64, 128}
		over := make([]int, len(thresh))
		for pt := 0; pt < 6; pt++ {
			for sq := 0; sq < 128; sq++ {
				if sq&0x88 != 0 {
					continue
				}
				v := int(e.qhist[0][pt][sq])
				total++
				if v > 0 {
					nz++
					bkt := 0
					for x := v; x > 1; x >>= 1 {
						bkt++
					}
					hist[bkt]++
					for i, th := range thresh {
						if v >= th {
							over[i]++
						}
					}
				}
			}
		}
		t.Logf("ply %d %s: nonzero %d/%d, log2-hist %v, >=T counts (T=2,4,8,16,32,64,128): %v",
			ply, tag, nz, total, hist, over)
	}
	for ply := 0; ply < 60; ply++ {
		e := engines[ply%2]
		e.SetPosition(&gp)
		m, _ := e.SearchCycleBudget(143000000, MaxPly-1)
		if m == NoMove {
			break
		}
		e.SetPosition(&gp)
		e.make(m)
		gp = e.Pos
		gp.Ply = 0
		if ply%10 == 9 {
			report(w, "white-table", ply)
		}
	}
}

// TestQHistPartitionExhaustive: with the partition pass armed, the set of
// LEGAL moves searched at a node must be unchanged — partitioning may only
// reorder quiets, never drop or duplicate one. Asserted indirectly but
// sharply: at fixed depth with all PRUNING features off (mask: null/killer
// only — no futility, no LMR/PVS zero windows), reordering quiets cannot
// change the minimax score.
func TestQHistPartitionExhaustive(t *testing.T) {
	for _, fen := range adaptFENs {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		base := NewEngine()
		base.Features = FtNull | FtKiller
		base.SetPosition(pos)
		_, bs := base.SearchFixed(4)

		for _, form := range []int{1, 2, 3} {
			on := NewEngine()
			on.Features = FtNull | FtKiller
			on.QHist = QHistParams{Form: form, PartThresh: 4}
			on.SetPosition(pos)
			_, s := on.SearchFixed(4)
			if s != bs {
				t.Errorf("%s form %d: partition changed a pruning-free minimax score: %d vs %d",
					fen, form, s, bs)
			}
		}
	}
}
