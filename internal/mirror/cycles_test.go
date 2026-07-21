package mirror

import (
	"math"
	"testing"
)

// asmRow is one ground-truth search from the asm differential harness
// (internal/chesstest TestMicroAB, engine.bin @ main 0c8ce38). cyc is the
// TRUE emulated-6502 cycle total; the remaining fields are the asm
// operation-entry probe counts (search=all node entries, make/eval across
// full+QS, attacked, ttprobe=full-width nodes, generate).
type asmRow struct {
	mask, depth            byte
	fen                    string
	score                  int
	move                   string
	cyc                    uint64
	search, make_, eval    uint64
	attacked, ttprobe, gen uint64
}

// microAB is the captured TestMicroAB ground truth: 18 fixed-depth
// searches over masks 0x1f (d6), 0x07 (d5), 0x00 (d4), 6 FENs each.
var microAB = []asmRow{
	// mask 0x1f, depth 6.
	{0x1f, 6, "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", -40, "a3a4", 276598893, 53273, 69839, 49821, 48950, 8495, 8384},
	{0x1f, 6, "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", 354, "e1c1", 427953717, 89164, 101668, 83209, 63124, 13262, 13954},
	{0x1f, 6, "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", -38, "e2a6", 681627594, 105654, 207167, 97883, 212352, 24985, 16827},
	{0x1f, 6, "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 84, "c3d5", 526938836, 74716, 166659, 66507, 127331, 18719, 14637},
	{0x1f, 6, "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 96, "b4f4", 19559820, 4565, 5713, 3937, 3683, 1471, 1217},
	{0x1f, 6, "2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", -18, "d4c6", 120486291, 25474, 28790, 24065, 17315, 4039, 3734},
	// mask 0x07, depth 5.
	{0x07, 5, "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", -24, "a3a4", 253212618, 55781, 78831, 50855, 55005, 9593, 8679},
	{0x07, 5, "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", 349, "e1c1", 273066886, 81098, 84194, 75289, 48767, 8006, 8165},
	{0x07, 5, "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 7, "d5e6", 395121809, 106475, 149134, 99233, 131209, 5490, 7578},
	{0x07, 5, "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 77, "c3d5", 272379639, 54679, 97208, 48443, 66614, 5634, 7681},
	{0x07, 5, "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 102, "b4f4", 9040064, 2247, 3190, 1890, 2367, 432, 548},
	{0x07, 5, "2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", -29, "f1e2", 145742620, 37634, 45758, 34672, 29927, 4745, 4274},
	// mask 0x00, depth 4.
	{0x00, 4, "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", -16, "a3a4", 87373900, 18616, 31979, 16121, 25720, 1434, 2342},
	{0x00, 4, "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", 343, "e1c1", 269939633, 63364, 67616, 53890, 33978, 5452, 9207},
	{0x00, 4, "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 14, "d5e6", 91972482, 14888, 27147, 11900, 30435, 2400, 2798},
	{0x00, 4, "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 64, "c3d5", 297614443, 51639, 105634, 43276, 76332, 5121, 7859},
	{0x00, 4, "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 93, "b4f4", 5356502, 1229, 2022, 854, 1618, 297, 318},
	{0x00, 4, "2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", -42, "f1e2", 132194837, 29072, 42451, 25387, 28629, 2649, 3398},
}

// replay runs one microAB row in the mirror with the asm-matching config
// and cycle accounting on, returning the engine and its cycle account.
func replay(t *testing.T, r asmRow) (*Engine, CycleAccount) {
	t.Helper()
	pos, err := ParseFEN(r.fen)
	if err != nil {
		t.Fatalf("ParseFEN %q: %v", r.fen, err)
	}
	e := NewEngine()
	e.Features = r.mask
	e.CycleTrack = true // accounting without a budget
	e.Seed = 0          // no dither (the harness leaves SEED = 0)
	e.SetPosition(pos)
	e.SearchFixed(int(r.depth))
	return e, e.Cyc
}

// lstsq solves min||X c - y|| via the normal equations (X'X)c = X'y with
// Gaussian elimination + partial pivoting. X is n rows x k cols.
func lstsq(X [][]float64, y []float64) []float64 {
	n, k := len(X), len(X[0])
	a := make([][]float64, k) // X'X augmented with X'y
	for i := range a {
		a[i] = make([]float64, k+1)
	}
	for i := 0; i < k; i++ {
		for j := 0; j < k; j++ {
			var s float64
			for r := 0; r < n; r++ {
				s += X[r][i] * X[r][j]
			}
			a[i][j] = s
		}
		var s float64
		for r := 0; r < n; r++ {
			s += X[r][i] * y[r]
		}
		a[i][k] = s
	}
	for col := 0; col < k; col++ {
		piv := col
		for r := col + 1; r < k; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[piv][col]) {
				piv = r
			}
		}
		a[col], a[piv] = a[piv], a[col]
		for r := 0; r < k; r++ {
			if r == col {
				continue
			}
			f := a[r][col] / a[col][col]
			for j := col; j <= k; j++ {
				a[r][j] -= f * a[col][j]
			}
		}
	}
	c := make([]float64, k)
	for i := 0; i < k; i++ {
		c[i] = a[i][k] / a[i][i]
	}
	return c
}

// TestCycleAccountingConsistency hand-checks the accounting invariants on a
// fixed search: the node-type counts partition the node total, every make
// has a matching unmake, the TT is probed exactly once per full-width node,
// and Est is EXACTLY the cost table dotted with the counters (the four
// priced ops plus the zero-priced ones). It also confirms accounting is
// fully inert when neither CycleBudget nor CycleTrack is set.
func TestCycleAccountingConsistency(t *testing.T) {
	pos, err := ParseFEN("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8")
	if err != nil {
		t.Fatal(err)
	}

	// Inert by default: no budget, no track => zero accounting.
	off := NewEngine()
	off.SetPosition(pos)
	off.SearchFixed(4)
	if off.Cyc != (CycleAccount{}) {
		t.Errorf("accounting ran with CycleBudget=0 && CycleTrack=false: %+v", off.Cyc)
	}

	e := NewEngine()
	e.CycleTrack = true
	e.SetPosition(pos)
	e.SearchFixed(4)
	c := e.Cyc

	if got := c.NodesFull + c.NodesQS + c.NodesEvasion; got != c.Nodes {
		t.Errorf("node partition %d != Nodes %d", got, c.Nodes)
	}
	if c.Makes != c.Unmakes {
		t.Errorf("makes %d != unmakes %d", c.Makes, c.Unmakes)
	}
	if mc := c.MakeQuiet + c.MakeCap + c.MakePromo + c.MakeCastle + c.MakeEP; mc != c.Makes {
		t.Errorf("make-class partition %d != Makes %d", mc, c.Makes)
	}
	if c.TTProbes != c.NodesFull {
		t.Errorf("ttprobes %d != full-width nodes %d (mirror probes once per full node)", c.TTProbes, c.NodesFull)
	}
	if c.EvalsExtra != 0 {
		t.Errorf("EvalsExtra %d without Extra terms", c.EvalsExtra)
	}
	// Est is exactly the priced dot product (generate/attacked/etc. priced 0).
	k := e.Costs
	want := uint64(k.Node)*c.Nodes + uint64(k.Make)*c.Makes + uint64(k.Eval)*c.Evals +
		uint64(k.TTProbe)*c.TTProbes + uint64(k.Attacked)*c.Attacked +
		uint64(k.Generate)*c.Generates + uint64(k.MovePerGen)*c.MovesGen +
		uint64(k.MakeNull)*c.MakeNull + uint64(k.TTStore)*c.TTStores
	if want != c.Est {
		t.Errorf("Est %d != cost-dot-counts %d", c.Est, want)
	}
	t.Logf("d4 counts: nodes=%d (full=%d qs=%d eva=%d) makes=%d evals=%d ttp=%d Est=%d",
		c.Nodes, c.NodesFull, c.NodesQS, c.NodesEvasion, c.Makes, c.Evals, c.TTProbes, c.Est)
}

// TestCycleBudgetDeterminism: SearchCycleBudget is a pure function of
// (position, budget, features) — the estimate never consults wall time —
// so two runs are bit-identical, the A/B soundness gate (same as
// TestBudgetDeterminism for node budgets).
func TestCycleBudgetDeterminism(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, budget := range []uint64{5_000_000, 50_000_000, 300_000_000} {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			run := func() (Move, int, uint64, int) {
				e := NewEngine()
				e.SetPosition(pos)
				m, s := e.SearchCycleBudget(budget, MaxPly-1)
				return m, s, e.Cyc.Est, e.MaxDepth
			}
			m1, s1, est1, d1 := run()
			m2, s2, est2, _ := run()
			if m1 != m2 || s1 != s2 || est1 != est2 {
				t.Errorf("budget %d %s: non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
					budget, fen[:16], m1, s1, est1, m2, s2, est2)
			}
			if m1.From == NoSq {
				t.Errorf("budget %d %s: no move produced", budget, fen[:16])
			}
			t.Logf("cyc-budget %10d  %-16s best %s score %5d  est %10d  depth %d",
				budget, fen[:16], m1.UCI(), s1, est1, d1)
		}
	}
}

// TestCycleBudgetDiscountsFeature is the end-to-end proof of the whole
// exercise: under a FIXED cycle budget, taxing an eval term (the rook set,
// 219 cyc/call) makes the engine spend more of its budget per node and so
// search FEWER nodes / reach no deeper than the untaxed engine — i.e. the
// term's cost is automatically charged against it, which a node budget can
// never do. Compare that to the same term under a NODE budget, which grants
// it identical work for free.
func TestCycleBudgetDiscountsFeature(t *testing.T) {
	pos, err := ParseFEN("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8")
	if err != nil {
		t.Fatal(err)
	}
	// Both engines run the rook term (identical eval => identical tree). The
	// ONLY difference is whether the term's 219-cyc/call cost is charged, so
	// any node-count difference is purely the tax biting into the budget.
	// Swept over budgets: the tax never lets the taxed engine out-search the
	// untaxed one, and at some budgets it costs it a whole iteration.
	var totUn, totTx uint64
	bit := false
	for _, budget := range []uint64{60_000_000, 120_000_000, 180_000_000, 240_000_000, 360_000_000, 520_000_000} {
		untaxed := NewEngine()
		untaxed.Extra, untaxed.Costs.EvalTerm = RookTermsAsm, 0
		untaxed.SetPosition(pos)
		untaxed.SearchCycleBudget(budget, MaxPly-1)
		taxed := NewEngine()
		taxed.Extra, taxed.Costs.EvalTerm = RookTermsAsm, RookTermCost
		taxed.SetPosition(pos)
		taxed.SearchCycleBudget(budget, MaxPly-1)
		if taxed.Nodes > untaxed.Nodes {
			t.Errorf("budget %d: taxed searched MORE nodes (%d) than untaxed (%d)", budget, taxed.Nodes, untaxed.Nodes)
		}
		if taxed.Nodes < untaxed.Nodes {
			bit = true
		}
		totUn += untaxed.Nodes
		totTx += taxed.Nodes
		t.Logf("cyc-budget %10d: untaxed nodes=%7d d%d | taxed nodes=%7d d%d",
			budget, untaxed.Nodes, untaxed.MaxDepth, taxed.Nodes, taxed.MaxDepth)
	}
	if !bit {
		t.Errorf("the rook-term tax never reduced node count across the budget sweep")
	}
	t.Logf("swept totals: untaxed %d nodes vs taxed %d nodes (%.1f%% fewer under the tax)",
		totUn, totTx, 100*(1-float64(totTx)/float64(totUn)))

	// A NODE budget cannot see the tax at all: identical tree => identical
	// nodes/depth regardless of EvalTermsCost. That is exactly the blind
	// spot the cycle budget fixes.
	un := NewEngine()
	un.Extra, un.Costs.EvalTerm = RookTermsAsm, 0
	un.SetPosition(pos)
	un.SearchBudget(200000, MaxPly-1)
	tn := NewEngine()
	tn.Extra, tn.Costs.EvalTerm = RookTermsAsm, RookTermCost
	tn.SetPosition(pos)
	tn.SearchBudget(200000, MaxPly-1)
	if un.Nodes != tn.Nodes {
		t.Errorf("node budget: EvalTermsCost changed the search (%d vs %d nodes) — it must not", un.Nodes, tn.Nodes)
	}
	t.Logf("node budget 200000, rook term on both: nodes=%d depth=%d regardless of EvalTermsCost (term untaxed)",
		un.Nodes, un.MaxDepth)
}

// nnls solves min||X c - y|| subject to c >= 0 (no intercept: zero ops =
// zero cycles) by clamped coordinate descent. The unconstrained OLS on
// these collinear operation counts produces physically meaningless
// negative costs; non-negativity yields an interpretable, transferable
// per-operation cost vector.
func nnls(X [][]float64, y []float64) []float64 {
	n, k := len(X), len(X[0])
	c := make([]float64, k)
	// Precompute column norms.
	norm := make([]float64, k)
	for j := 0; j < k; j++ {
		var s float64
		for r := 0; r < n; r++ {
			s += X[r][j] * X[r][j]
		}
		norm[j] = s
	}
	resid := make([]float64, n)
	copy(resid, y) // c starts at 0 so residual = y
	for iter := 0; iter < 5000; iter++ {
		var maxDelta float64
		for j := 0; j < k; j++ {
			if norm[j] == 0 {
				continue
			}
			// Partial residual excluding column j.
			var dot float64
			for r := 0; r < n; r++ {
				dot += X[r][j] * (resid[r] + X[r][j]*c[j])
			}
			nc := dot / norm[j]
			if nc < 0 {
				nc = 0
			}
			d := nc - c[j]
			if d != 0 {
				for r := 0; r < n; r++ {
					resid[r] -= X[r][j] * d
				}
				c[j] = nc
				if math.Abs(d) > maxDelta {
					maxDelta = math.Abs(d)
				}
			}
		}
		if maxDelta < 1e-6 {
			break
		}
	}
	return c
}

// TestFullWidthParity documents where the mirror tree tracks the asm and
// where it diverges. It is INFORMATIONAL (never fails): the mirror's
// full-width TT-probe count matches the asm's exactly at mask 0x00
// (identical full-width tree), but at masks with search features
// (null/futility/LMR) the full-width trees drift, and at ALL masks the
// mirror's quiescence tree is larger than the asm's (the asm QS prunes /
// inlines more). This is the reason the cycle model is calibrated on the
// asm's OWN operation counts (TestCycleModelFit), not on mirror counts.
func TestFullWidthParity(t *testing.T) {
	for _, r := range microAB {
		_, c := replay(t, r)
		ttRatio := float64(c.TTProbes) / float64(r.ttprobe)
		// asm QS ~= asm search - asm full-width (ttprobe ~= full-width).
		asmQS := int(r.search) - int(r.ttprobe)
		qsRatio := float64(c.NodesQS) / float64(asmQS)
		t.Logf("m%02x %-14s full: mirror-ttp %d vs asm-ttp %d (%.2f) | QS: mirror %d vs asm~%d (%.2f)",
			r.mask, r.fen[:14], c.TTProbes, r.ttprobe, ttRatio, c.NodesQS, asmQS, qsRatio)
	}
}

// fitModel fits asm_cycles onto the given asm-count columns (by name) plus
// an intercept, returning coefficients aligned with cols and the intercept
// last. Regressing on the asm's OWN operation counts (same tree that
// produced the cycle total) is the sound calibration.
func fitModel(cols []string) []float64 {
	col := func(r asmRow, name string) float64 {
		switch name {
		case "search":
			return float64(r.search)
		case "make":
			return float64(r.make_)
		case "eval":
			return float64(r.eval)
		case "attacked":
			return float64(r.attacked)
		case "ttprobe":
			return float64(r.ttprobe)
		case "generate":
			return float64(r.gen)
		}
		panic("bad col " + name)
	}
	X := make([][]float64, len(microAB))
	y := make([]float64, len(microAB))
	for i, r := range microAB {
		row := make([]float64, len(cols)+1)
		for j, name := range cols {
			row[j] = col(r, name)
		}
		row[len(cols)] = 1 // intercept
		X[i] = row
		y[i] = float64(r.cyc)
	}
	return lstsq(X, y)
}

func predErrors(t *testing.T, cols []string, coef []float64) (worst float64) {
	col := func(r asmRow, name string) float64 {
		switch name {
		case "search":
			return float64(r.search)
		case "make":
			return float64(r.make_)
		case "eval":
			return float64(r.eval)
		case "attacked":
			return float64(r.attacked)
		case "ttprobe":
			return float64(r.ttprobe)
		case "generate":
			return float64(r.gen)
		}
		return 0
	}
	maskAct := map[byte]float64{}
	maskPred := map[byte]float64{}
	for _, r := range microAB {
		var p float64
		if len(coef) > len(cols) {
			p = coef[len(cols)] // intercept
		}
		for j, name := range cols {
			p += coef[j] * col(r, name)
		}
		act := float64(r.cyc)
		e := 100 * (p - act) / act
		if math.Abs(e) > worst {
			worst = math.Abs(e)
		}
		maskAct[r.mask] += act
		maskPred[r.mask] += p
		t.Logf("  m%02x %-16s act=%11.0f pred=%11.0f err=%+6.1f%%", r.mask, r.fen[:16], act, p, e)
	}
	for _, m := range []byte{0x1f, 0x07, 0x00} {
		e := 100 * (maskPred[m] - maskAct[m]) / maskAct[m]
		t.Logf("mask %#02x GRAND TOTAL act=%13.0f pred=%13.0f err=%+.2f%%", m, maskAct[m], maskPred[m], e)
	}
	return worst
}

// TestCycleModelFit derives the least-squares per-operation 6502 cycle
// costs from the 18 microAB searches by regressing the asm cycle total
// onto the asm operation-entry counts, and reports predicted-vs-actual per
// search and per mask for two models: the full 6-count model and the
// reduced [search,make,eval,ttprobe] model the runtime uses (these four
// ops have the closest mirror/asm per-node frequency, so the fitted costs
// transfer to mirror counts with the least distortion). Run with -v.
func TestCycleModelFit(t *testing.T) {
	full := []string{"search", "make", "eval", "attacked", "ttprobe", "generate"}
	cf := fitModel(full)
	t.Log("FULL model coefficients (cycles/op):")
	for i, l := range full {
		t.Logf("  %-9s % 10.2f", l, cf[i])
	}
	t.Logf("  %-9s % 10.2f", "intercept", cf[len(full)])
	t.Log("FULL model predicted vs actual:")
	w1 := predErrors(t, full, cf)
	t.Logf("FULL worst per-search error: %.1f%%", w1)

	// Non-negative fits through the origin (physically interpretable costs).
	colVal := func(r asmRow, name string) float64 {
		switch name {
		case "search":
			return float64(r.search)
		case "make":
			return float64(r.make_)
		case "eval":
			return float64(r.eval)
		case "attacked":
			return float64(r.attacked)
		case "ttprobe":
			return float64(r.ttprobe)
		case "generate":
			return float64(r.gen)
		}
		return 0
	}
	nnlsFit := func(cols []string) []float64 {
		X := make([][]float64, len(microAB))
		y := make([]float64, len(microAB))
		for i, r := range microAB {
			row := make([]float64, len(cols))
			for j, name := range cols {
				row[j] = colVal(r, name)
			}
			X[i] = row
			y[i] = float64(r.cyc)
		}
		return nnls(X, y)
	}
	for _, cols := range [][]string{
		{"search", "make", "eval", "attacked", "ttprobe"},
		{"make", "eval", "attacked"},
	} {
		cc := nnlsFit(cols)
		t.Logf("NNLS model %v (no intercept):", cols)
		for i, l := range cols {
			t.Logf("  %-9s % 10.2f", l, cc[i])
		}
		w := predErrors(t, cols, cc)
		t.Logf("NNLS %v worst per-search error: %.1f%%", cols, w)
	}

	// Ridge regression (L2) through the origin: tames the collinearity so
	// the six per-op costs stay positive and interpretable while the fit
	// stays accurate at the per-mask level.
	ridgeFit := func(cols []string, lambda float64) []float64 {
		k := len(cols)
		// Standardize columns so lambda penalizes each equally, then unscale.
		mean := make([]float64, k)
		std := make([]float64, k)
		for j := range cols {
			var s, s2 float64
			for _, r := range microAB {
				v := colVal(r, cols[j])
				s += v
				s2 += v * v
			}
			n := float64(len(microAB))
			mean[j] = s / n
			std[j] = math.Sqrt(s2/n - mean[j]*mean[j])
			if std[j] == 0 {
				std[j] = 1
			}
		}
		A := make([][]float64, k)
		for i := range A {
			A[i] = make([]float64, k+1)
		}
		for i := 0; i < k; i++ {
			for j := 0; j < k; j++ {
				var s float64
				for _, r := range microAB {
					xi := colVal(r, cols[i]) / std[i]
					xj := colVal(r, cols[j]) / std[j]
					s += xi * xj
				}
				A[i][j] = s
			}
			A[i][i] += lambda
			var s float64
			for _, r := range microAB {
				xi := colVal(r, cols[i]) / std[i]
				s += xi * float64(r.cyc)
			}
			A[i][k] = s
		}
		for col := 0; col < k; col++ {
			piv := col
			for r := col + 1; r < k; r++ {
				if math.Abs(A[r][col]) > math.Abs(A[piv][col]) {
					piv = r
				}
			}
			A[col], A[piv] = A[piv], A[col]
			for r := 0; r < k; r++ {
				if r == col {
					continue
				}
				f := A[r][col] / A[col][col]
				for j := col; j <= k; j++ {
					A[r][j] -= f * A[col][j]
				}
			}
		}
		c := make([]float64, k)
		for i := 0; i < k; i++ {
			c[i] = (A[i][k] / A[i][i]) / std[i] // unscale
		}
		return c
	}
	rcols := []string{"search", "make", "eval", "ttprobe"}
	for _, lambda := range []float64{0.5, 2, 5, 10} {
		cc := ridgeFit(rcols, lambda)
		t.Logf("RIDGE lambda=%g model %v (no intercept):", lambda, rcols)
		allPos := true
		for i, l := range rcols {
			t.Logf("  %-9s % 10.2f", l, cc[i])
			if cc[i] < 0 {
				allPos = false
			}
		}
		w := predErrors(t, rcols, cc)
		t.Logf("RIDGE lambda=%g worst per-search error: %.1f%% (all-positive=%v)", lambda, w, allPos)
	}
}

// TestCycleModelFrozen checks the FROZEN DefaultCycleCosts (the reduced
// [node,make,eval,ttprobe] model shipped in cycles.go) applied to the asm
// operation counts (the same tree that produced the cycle total). The
// reduced model deliberately drops the generate/attacked regressors (whose
// mirror/asm per-node frequency diverges) so the costs transfer cleanly to
// the mirror's counts; it trades a little asm-side per-mask accuracy for
// that. Guard: per-mask grand total within 12%, and every non-endgame
// search within 20% (the 7-piece endgame position has cheaper per-op costs
// than a global model can capture — a documented limitation).
func TestCycleModelFrozen(t *testing.T) {
	k := DefaultCycleCosts
	predict := func(r asmRow) float64 {
		return fittedConst +
			k.Node*float64(r.search) +
			k.Make*float64(r.make_) +
			k.Eval*float64(r.eval) +
			k.TTProbe*float64(r.ttprobe)
	}
	maskAct := map[byte]float64{}
	maskPred := map[byte]float64{}
	var worst float64
	for _, r := range microAB {
		p := predict(r)
		act := float64(r.cyc)
		e := 100 * (p - act) / act
		if math.Abs(e) > worst {
			worst = math.Abs(e)
		}
		maskAct[r.mask] += act
		maskPred[r.mask] += p
		endgame := r.fen[:2] == "8/"
		if !endgame && math.Abs(e) > 20 {
			t.Errorf("m%02x %s: frozen err %+.1f%% (>20%%) act=%.0f pred=%.0f", r.mask, r.fen[:16], e, act, p)
		}
	}
	for _, m := range []byte{0x1f, 0x07, 0x00} {
		e := 100 * (maskPred[m] - maskAct[m]) / maskAct[m]
		t.Logf("mask %#02x grand-total frozen err %+.2f%%", m, e)
		if math.Abs(e) > 12 {
			t.Errorf("mask %#02x grand-total frozen err %+.2f%% exceeds 12%%", m, e)
		}
	}
	t.Logf("worst per-search frozen err: %.1f%% (incl. endgame outlier)", worst)
}

// TestEvalTermTax validates deliverable #4: an eval term costing 219
// cycles/call (the asm FT_ROOKX extraterm) is ~4-6% of all cycles, so
// under a fixed cycle budget it discounts effective search by that much.
// It computes the fraction directly from the asm ground truth
// (219*eval_calls / total_cycles) per mask, and confirms the mirror's
// EvalTerm accounting reproduces the same per-eval surcharge.
func TestEvalTermTax(t *testing.T) {
	maskEval := map[byte]uint64{}
	maskCyc := map[byte]uint64{}
	for _, r := range microAB {
		maskEval[r.mask] += r.eval
		maskCyc[r.mask] += r.cyc
	}
	for _, m := range []byte{0x1f, 0x07, 0x00} {
		frac := 100 * RookTermCost * float64(maskEval[m]) / float64(maskCyc[m])
		t.Logf("mask %#02x: rook term %g cyc/eval-call over %d eval calls = %.2f%% of %d cycles",
			m, RookTermCost, maskEval[m], frac, maskCyc[m])
	}

	// Mirror side: run WITH the rook term and its 219-cyc/call tax, then
	// read the term's share of the engine's own estimated cycles. (Enabling
	// the term changes the eval and thus the tree, so this is measured on
	// the taxed engine directly, not by differencing against an untaxed
	// run.)
	r := microAB[0] // m1f fen0
	pos, _ := ParseFEN(r.fen)
	taxed := NewEngine()
	taxed.Features, taxed.CycleTrack, taxed.Seed = r.mask, true, 0
	taxed.Extra = RookTermsAsm
	taxed.Costs.EvalTerm = RookTermCost
	taxed.SetPosition(pos)
	taxed.SearchFixed(int(r.depth))

	if taxed.Cyc.EvalsExtra != taxed.Cyc.Evals {
		t.Errorf("EvalsExtra %d != Evals %d (term should fire on every eval)", taxed.Cyc.EvalsExtra, taxed.Cyc.Evals)
	}
	added := uint64(RookTermCost) * taxed.Cyc.EvalsExtra
	frac := 100 * float64(added) / float64(taxed.Cyc.Est)
	t.Logf("mirror m1f fen0 (rook term on): term %d cyc over %d eval calls = %.2f%% of Est %d",
		added, taxed.Cyc.EvalsExtra, frac, taxed.Cyc.Est)
	// The whole point (deliverable #4): the rook term's mirror-side cycle
	// fraction lands in the same 4-6% band the asm ground truth shows, so a
	// cycle budget discounts its Elo by that much. If this drifts out of
	// band, the runtime cost coefficients need re-tuning.
	if frac < 3.5 || frac > 6.5 {
		t.Errorf("mirror rook-term fraction %.2f%% outside the 3.5-6.5%% band (asm reality ~4-6%%)", frac)
	}
}
