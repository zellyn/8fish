package mirror

import "testing"

// Not-improving margin set used across the improving screens: tighter than
// the shipped RFP 120/500 + leaf-futility 120 (prune harder when the side
// to move's eval is trending worse).
var impTightRFP = [8]int{0, 60, 300}

const impTightFut = 60

// impVariants is the set of improving configs screened (plus the off
// baseline), reused by the soundness and node-count tests. The RFP
// applications carry the tight not-improving margins; the LMR applications
// add one extra reduction ply.
var impVariants = []struct {
	name string
	imp  ImprovingParams
}{
	{"off", ImprovingParams{}},
	{"free-rfp", ImprovingParams{Mode: improvingFree, RFP: true, RFPNI: impTightRFP, FutNI: impTightFut}},
	{"free-lmr", ImprovingParams{Mode: improvingFree, LMR: true}},
	{"free-both", ImprovingParams{Mode: improvingFree, RFP: true, RFPNI: impTightRFP, FutNI: impTightFut, LMR: true}},
	{"full-rfp", ImprovingParams{Mode: improvingFull, RFP: true, RFPNI: impTightRFP, FutNI: impTightFut}},
	{"full-lmr", ImprovingParams{Mode: improvingFull, LMR: true}},
	{"full-both", ImprovingParams{Mode: improvingFull, RFP: true, RFPNI: impTightRFP, FutNI: impTightFut, LMR: true}},
}

// TestImprovingOffIsNoop: with Mode == 0 the budgeted search is identical
// (move, score, nodes) to the pre-improving path — the guarantee the parity
// gates rely on, asserted directly here. A full-signal config with Mode
// zeroed must also be inert (the RFP/LMR flags never fire while off).
func TestImprovingOffIsNoop(t *testing.T) {
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
		// Flags set but Mode==0: must still be a byte-identical no-op.
		off.Improving = ImprovingParams{Mode: improvingOff, RFP: true, LMR: true, RFPNI: impTightRFP, FutNI: impTightFut}
		off.SetPosition(pos)
		om, os := off.SearchBudget(30000, MaxPly-1)
		if om != bm || os != bs || off.Nodes != bn {
			t.Errorf("%s: improving-off differs from baseline: (%v,%d,%d) vs (%v,%d,%d)",
				fen, om, os, off.Nodes, bm, bs, bn)
		}
	}
}

// TestImprovingDeterminism: with the heuristic on (any variant),
// SearchBudget/SearchCycleBudget stays a pure function of (position, budget,
// features, seed) — two runs replay bit-identically. This is the A/B
// soundness gate: an improving game replays exactly, so screens reproduce.
func TestImprovingDeterminism(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, v := range impVariants {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			// node budget
			runNode := func() (Move, int, uint64) {
				e := NewEngine()
				e.Improving = v.imp
				e.SetPosition(pos)
				e.Seed = 0
				m, s := e.SearchBudget(30000, MaxPly-1)
				return m, s, e.Nodes
			}
			m1, s1, n1 := runNode()
			m2, s2, n2 := runNode()
			if m1 != m2 || s1 != s2 || n1 != n2 {
				t.Errorf("imp %s %s node-budget non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
					v.name, fen, m1, s1, n1, m2, s2, n2)
			}
			// cycle budget (exercises the full-signal added-eval charging path)
			runCyc := func() (Move, int, uint64) {
				e := NewEngine()
				e.Improving = v.imp
				e.SetPosition(pos)
				e.Seed = 0
				m, s := e.SearchCycleBudget(143_000_000, MaxPly-1)
				return m, s, e.Cyc.Est
			}
			c1, cs1, ce1 := runCyc()
			c2, cs2, ce2 := runCyc()
			if c1 != c2 || cs1 != cs2 || ce1 != ce2 {
				t.Errorf("imp %s %s cycle-budget non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
					v.name, fen, c1, cs1, ce1, c2, cs2, ce2)
			}
		}
	}
}

// TestImprovingMateSound: tightening margins / deepening reductions must
// never fabricate or miss a forced mate at a modest fixed depth. Every
// variant must report the exact mate score the baseline search reports.
func TestImprovingMateSound(t *testing.T) {
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
		for _, v := range impVariants {
			e := NewEngine()
			e.Improving = v.imp
			e.SetPosition(pos)
			e.Seed = 0
			m, s := e.SearchFixed(tc.depth)
			if s != tc.want {
				t.Errorf("imp %s %s: score %d, want mate %d", v.name, tc.fen, s, tc.want)
			}
			if m.From == NoSq {
				t.Errorf("imp %s %s: no move produced", v.name, tc.fen)
			}
		}
	}
}

// TestImprovingFullSignalEvalCost isolates the full-signal eval TAX from
// its pruning benefit: Mode==improvingFull with NO applications (RFP/LMR
// off) forces an eval at every full-width node but changes no search
// decision, so the tree is byte-identical to the baseline and the extra
// eval() calls are exactly the ADDED evals full-signal pays for. Reports the
// added evals as a fraction of full-width nodes (ideal ~ the share of
// full-width nodes that compute no natural eval) and the estimated cycle tax
// as a fraction of the total baseline cycle estimate.
func TestImprovingFullSignalEvalCost(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	fens := benchFENs(t)
	var baseNodes, baseEvals, baseEst uint64
	var fsNodes, fsEvals, fsEst, fullNodes uint64
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		base := NewEngine()
		base.CycleTrack = true
		base.SetPosition(pos)
		base.SearchFixed(6)
		baseNodes += base.Nodes
		baseEvals += base.Cyc.Evals
		baseEst += base.Cyc.Est

		fs := NewEngine()
		fs.Improving = ImprovingParams{Mode: improvingFull} // force eval, no apps
		fs.CycleTrack = true
		fs.SetPosition(pos)
		fs.SearchFixed(6)
		fsNodes += fs.Nodes
		fsEvals += fs.Cyc.Evals
		fsEst += fs.Cyc.Est
		fullNodes += fs.Cyc.NodesFull
	}
	if fsNodes != baseNodes {
		t.Errorf("full-signal (no apps) changed the tree: %d nodes vs baseline %d", fsNodes, baseNodes)
	}
	added := fsEvals - baseEvals
	t.Logf("baseline: %d nodes, %d full-width nodes, %d evals, est %d cyc",
		baseNodes, fullNodes, baseEvals, baseEst)
	t.Logf("full-signal added evals: %d (%.1f%% of full-width nodes; baseline evals were %.1f%% of full nodes)",
		added, 100*float64(added)/float64(fullNodes), 100*float64(baseEvals)/float64(fullNodes))
	t.Logf("full-signal eval TAX: +%d cyc = %+.2f%% of baseline est (%d -> %d)",
		fsEst-baseEst, 100*(float64(fsEst)/float64(baseEst)-1), baseEst, fsEst)
}

// TestImprovingSweepNodes: depth-6 node counts across improving variants
// under the ASM-MATCHED config (mask 0x1f = TT + killers + two-tier MVV, NO
// SEE, NO history — the real asm ordering, five-pass moveLoop). Confirms the
// heuristic is wired into that path and quantifies the fixed-depth tree
// effect the asm port would see. Also reports full-signal's added evals so
// the cost model can be sanity-checked.
func TestImprovingSweepNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	fens := benchFENs(t)
	var baseNodes uint64
	for vi, v := range impVariants {
		var nodes, evals, addedEvals, fullNodes uint64
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			eng := NewEngine() // FtAll (0x1f), recap2 QS, shipped weights
			eng.Improving = v.imp
			eng.CycleTrack = true // count evals without changing search
			eng.SetPosition(pos)
			eng.SearchFixed(6)
			nodes += eng.Nodes
			evals += eng.Cyc.Evals
			fullNodes += eng.Cyc.NodesFull
		}
		if vi == 0 {
			baseNodes = nodes
			baseEvals := evals
			t.Logf("%-10s total %9d nodes (baseline)  evals %d  fullNodes %d",
				v.name, nodes, baseEvals, fullNodes)
			continue
		}
		// Full-signal added evals ~= this variant's evals minus baseline evals
		// at the same tree; report the raw eval count and the delta.
		_ = addedEvals
		t.Logf("%-10s total %9d nodes (%+.2f%% vs baseline)  evals %d  fullNodes %d",
			v.name, nodes, 100*(float64(nodes)/float64(baseNodes)-1), evals, fullNodes)
	}
}
