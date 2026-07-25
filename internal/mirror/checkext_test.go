package mirror

import "testing"

// checkExtVariants: the check-extension configs screened (plus the off
// baseline), reused by the no-op, determinism, soundness and node-count
// tests.
var checkExtVariants = []struct {
	name string
	ce   CheckExtParams
}{
	{"off", CheckExtParams{}},
	{"cap1", CheckExtParams{MaxExt: 1}},
	{"cap2", CheckExtParams{MaxExt: 2}},
	{"cap3", CheckExtParams{MaxExt: 3}},
	{"cap2-caps", CheckExtParams{MaxExt: 2, CapturesOnly: true}},
}

var checkExtFENs = []string{
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
}

// TestCheckExtOffIsNoop: with MaxExt == 0 the budgeted search is identical
// (move, score, nodes) to the pre-check-extension path — the guarantee the
// parity gates rely on, asserted directly. Covers both budget kinds and both
// the five-pass (FtAll) and scored-ordering (FtSEE|FtHistory) move loops.
func TestCheckExtOffIsNoop(t *testing.T) {
	for _, fen := range checkExtFENs {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		for _, feat := range []byte{FtAll, FtAll | FtSEE | FtHistory} {
			base := NewEngine()
			base.Features = feat
			base.SetPosition(pos)
			bm, bs := base.SearchBudget(30000, MaxPly-1)
			bn := base.Nodes

			off := NewEngine()
			off.Features = feat
			off.CheckExt = CheckExtParams{} // explicit off
			off.SetPosition(pos)
			om, os := off.SearchBudget(30000, MaxPly-1)
			if om != bm || os != bs || off.Nodes != bn {
				t.Errorf("feat %#02x %s: check-ext-off differs from baseline: (%v,%d,%d) vs (%v,%d,%d)",
					feat, fen, om, os, off.Nodes, bm, bs, bn)
			}
		}
	}
}

// TestCheckExtDeterminism: with check extensions on (any variant), the
// budgeted search stays a pure function of (position, budget, features,
// seed) — two runs with the same seed replay bit-identically under both
// budget kinds.
func TestCheckExtDeterminism(t *testing.T) {
	for _, v := range checkExtVariants {
		for _, fen := range checkExtFENs {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			run := func(cycles bool) (Move, int, uint64) {
				e := NewEngine() // asm-matched: FtAll, recap2, shipped weights
				e.CheckExt = v.ce
				e.SetPosition(pos)
				e.Seed = 0
				if cycles {
					m, s := e.SearchCycleBudget(20_000_000, MaxPly-1)
					return m, s, e.Nodes
				}
				m, s := e.SearchBudget(20000, MaxPly-1)
				return m, s, e.Nodes
			}
			for _, cycles := range []bool{false, true} {
				m1, s1, n1 := run(cycles)
				m2, s2, n2 := run(cycles)
				if m1 != m2 || s1 != s2 || n1 != n2 {
					t.Errorf("ckext %s cycles=%v %s non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
						v.name, cycles, fen, m1, s1, n1, m2, s2, n2)
				}
				if m1.From == NoSq {
					t.Errorf("ckext %s %s: no move produced", v.name, fen)
				}
			}
		}
	}
}

// TestCheckExtNumExtBalanced: numExt is save/restored around every extended
// child search, so it must return to 0 after any completed search regardless
// of variant. A nonzero leftover would mean an unbalanced increment (a bug
// that would silently disable extensions on later moves).
func TestCheckExtNumExtBalanced(t *testing.T) {
	for _, v := range checkExtVariants {
		for _, fen := range checkExtFENs {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			e := NewEngine()
			e.CheckExt = v.ce
			e.SetPosition(pos)
			e.Seed = 0
			e.SearchFixed(5)
			if e.numExt != 0 {
				t.Errorf("ckext %s %s: numExt left at %d after search (want 0)", v.name, fen, e.numExt)
			}
		}
	}
}

// TestCheckExtMateSound: check extensions only deepen forcing lines, so a
// fixed-depth forced-mate verdict must never be worsened (a mate already
// found without extensions is still found, possibly sooner).
func TestCheckExtMateSound(t *testing.T) {
	cases := []struct {
		fen   string
		depth int
		want  int
	}{
		{"k7/8/2K5/8/8/8/8/6R1 w - - 0 1", 6, Mate - 3},    // KR mate in 2
		{"6k1/5ppp/8/8/8/8/8/4R2K w - - 0 1", 4, Mate - 1}, // back-rank mate in 1
	}
	for _, tc := range cases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range checkExtVariants {
			e := NewEngine()
			e.CheckExt = v.ce
			e.SetPosition(pos)
			e.Seed = 0
			m, s := e.SearchFixed(tc.depth)
			if s != tc.want {
				t.Errorf("ckext %s %s: score %d, want mate %d", v.name, tc.fen, s, tc.want)
			}
			if m.From == NoSq {
				t.Errorf("ckext %s %s: no move produced", v.name, tc.fen)
			}
		}
	}
}

// TestCheckExtSweepNodes: fixed-depth-6 node counts across variants under the
// asm-matched config (mask 0x1f, recap2 QS, shipped weights — the five-pass
// moveLoop), quantifying the tree blow-up each cap costs. Reported (not
// asserted) so the fixed-depth cost is visible next to the cycle-budget Elo.
func TestCheckExtSweepNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 1",
	}
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		var baseNodes uint64
		for _, v := range checkExtVariants {
			e := NewEngine()
			e.CheckExt = v.ce
			e.SetPosition(pos)
			e.Seed = 0
			e.SearchFixed(6)
			if v.name == "off" {
				baseNodes = e.Nodes
			}
			growth := 0.0
			if baseNodes > 0 {
				growth = 100 * (float64(e.Nodes)/float64(baseNodes) - 1)
			}
			t.Logf("%-12s %-52s nodes %10d  (+%.1f%%)", v.name, fen, e.Nodes, growth)
		}
	}
}
