package mirror

import "testing"

// seeVariants: the SEE classification configs screened (plus the off
// baseline), reused by the soundness and node-count tests.
var seeVariants = []struct {
	name string
	see  SEEParams
}{
	{"off", SEEParams{}},
	{"pawn-fw", SEEParams{Mode: 1, DeferFW: true}},
	{"pawn-fw+pq", SEEParams{Mode: 1, DeferFW: true, PruneQS: true}},
	{"atk-fw", SEEParams{Mode: 2, DeferFW: true}},
	{"atk-fw+dq", SEEParams{Mode: 2, DeferFW: true, DeferQS: true}},
	{"atk-fw+pq", SEEParams{Mode: 2, DeferFW: true, PruneQS: true}},
	{"atk-pq", SEEParams{Mode: 2, PruneQS: true}},
	{"full-fw", SEEParams{Mode: 3, DeferFW: true}},
	{"full-fw+dq", SEEParams{Mode: 3, DeferFW: true, DeferQS: true}},
	{"full-fw+pq", SEEParams{Mode: 3, DeferFW: true, PruneQS: true}},
	{"full-pq", SEEParams{Mode: 3, PruneQS: true}},
}

// TestSEEOffIsNoop: with Mode == 0 the budgeted search is identical
// (move, score, nodes) to the pre-SEE path — the guarantee the parity
// gates rely on, asserted directly here.
func TestSEEOffIsNoop(t *testing.T) {
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
		off.SEE = SEEParams{} // explicit off
		off.SetPosition(pos)
		om, os := off.SearchBudget(30000, MaxPly-1)
		if om != bm || os != bs || off.Nodes != bn {
			t.Errorf("%s: SEE-off differs from baseline: (%v,%d,%d) vs (%v,%d,%d)",
				fen, om, os, off.Nodes, bm, bs, bn)
		}
	}
}

// TestSEEDeterminism: with SEE classification on (any variant), the
// budgeted search stays a pure function of (position, budget, features,
// seed) — two runs replay bit-identically under both budget kinds.
func TestSEEDeterminism(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, v := range seeVariants {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			run := func(cycles bool) (Move, int, uint64) {
				e := NewEngine() // asm-matched: FtAll, recap2, shipped weights
				e.SEE = v.see
				e.Costs.SEEGate, e.Costs.SEE, e.Costs.SEERescan = 20, 200, 8
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
					t.Errorf("see %s cycles=%v %s non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
						v.name, cycles, fen, m1, s1, n1, m2, s2, n2)
				}
				if m1.From == NoSq {
					t.Errorf("see %s %s: no move produced", v.name, fen)
				}
			}
		}
	}
}

// TestSEEMateSound: deferring losing captures at full-width nodes only
// reorders the move list (nothing is dropped), so a fixed-depth mate
// verdict must never change for defer-only variants. QS pruning variants
// may change QS scores but must still find these forced mates.
func TestSEEMateSound(t *testing.T) {
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
		for _, v := range seeVariants {
			e := NewEngine()
			e.SEE = v.see
			e.SetPosition(pos)
			e.Seed = 0
			m, s := e.SearchFixed(tc.depth)
			if s != tc.want {
				t.Errorf("see %s %s: score %d, want mate %d", v.name, tc.fen, s, tc.want)
			}
			if m.From == NoSq {
				t.Errorf("see %s %s: no move produced", v.name, tc.fen)
			}
		}
	}
}

// TestSEESweepNodes: depth-6 node counts across SEE variants under the
// ASM-MATCHED ordering config (mask 0x1f, recap2 QS, shipped weights —
// the five-pass moveLoop). Also reports the classification traffic
// (gates/calls/losing per 1000 nodes) that drives the cycle-cost model.
func TestSEESweepNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	fens := benchFENs(t)
	var baseTotal uint64
	for vi, v := range seeVariants {
		var total, gates, calls, callsQS, losing, rescans uint64
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			eng := NewEngine() // FtAll (0x1f), recap2 QS, shipped weights
			eng.SEE = v.see
			eng.CycleTrack = true // counters on, costs zero: tree unchanged
			eng.SetPosition(pos)
			eng.SearchFixed(6)
			total += eng.Nodes
			gates += eng.Cyc.SEEGates
			calls += eng.Cyc.SEECalls
			callsQS += eng.Cyc.SEECallsQS
			losing += eng.Cyc.SEELosing
			rescans += eng.Cyc.SEERescans
		}
		if vi == 0 {
			baseTotal = total
		}
		t.Logf("%-11s total %9d nodes (%+6.2f%%)  per-1000-nodes: gates %5.0f calls %5.0f (qs %5.0f) losing %4.0f rescan %5.0f",
			v.name, total, 100*(float64(total)/float64(baseTotal)-1),
			1000*float64(gates)/float64(total), 1000*float64(calls)/float64(total),
			1000*float64(callsQS)/float64(total), 1000*float64(losing)/float64(total),
			1000*float64(rescans)/float64(total))
	}
}

// TestSEEOperatingPoint: SEE classification traffic and cycle tax of the
// adopted candidate (mode 2 atk-fw, costs 30/420/10) at the REAL operating
// point — full 143M-cycle budget searches over the bench positions. This is
// the honest calls-per-move / %-of-budget bill the port would pay.
func TestSEEOperatingPoint(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	fens := benchFENs(t)
	var moves, gates, calls, losing, rescans, nodes, est uint64
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		eng := NewEngine()
		eng.SEE = SEEParams{Mode: 2, DeferFW: true}
		eng.Costs.SEEGate, eng.Costs.SEE, eng.Costs.SEERescan = 30, 420, 10
		eng.SetPosition(pos)
		eng.SearchCycleBudget(143_000_000, MaxPly-1)
		moves++
		gates += eng.Cyc.SEEGates
		calls += eng.Cyc.SEECalls
		losing += eng.Cyc.SEELosing
		rescans += eng.Cyc.SEERescans
		nodes += eng.Nodes
		est += eng.Cyc.Est
	}
	seeCyc := 30*gates + 420*calls + 10*rescans
	t.Logf("moves %d  nodes/move %.0f  per move: gates %.0f calls %.0f losing %.0f rescanitems %.0f",
		moves, float64(nodes)/float64(moves), float64(gates)/float64(moves),
		float64(calls)/float64(moves), float64(losing)/float64(moves),
		float64(rescans)/float64(moves))
	t.Logf("SEE cycles/move %.0f = %.2f%% of spent cycles, %.2f%% of the 143M budget (est spent %.0f)",
		float64(seeCyc)/float64(moves), 100*float64(seeCyc)/float64(est),
		100*float64(seeCyc)/float64(moves)/143e6,
		float64(est)/float64(moves))
}

// TestSEEAgreement: classification agreement of the cheap variants (pawn-
// defended, attacked-defended) against the full-SEE reference, measured on
// the true operand distribution (every gate-passing capture reached during
// asm-matched d6 searches). The confusion matrix is the "what ordering
// quality does the cheap variant buy" number.
func TestSEEAgreement(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	fens := benchFENs(t)
	for mode, name := range map[int]string{1: "pawn-defended", 2: "attacked-defended"} {
		var cm [2][2]uint64
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			eng := NewEngine()
			eng.SEE = SEEParams{Mode: mode, DeferFW: true, DeferQS: true}
			eng.seeAudit = &cm
			eng.SetPosition(pos)
			eng.SearchFixed(6)
			// Note: audited runs shape their own trees (variant ordering),
			// so distributions differ slightly per mode; each matrix is the
			// variant's own operating distribution — the honest one.
		}
		tot := cm[0][0] + cm[0][1] + cm[1][0] + cm[1][1]
		agree := cm[0][0] + cm[1][1]
		t.Logf("%s: calls %d  agree %.1f%%  ref-losing %.1f%%  false-losing %.1f%% (winning deferred/pruned)  missed-losing %.1f%%",
			name, tot, 100*float64(agree)/float64(tot),
			100*float64(cm[1][0]+cm[1][1])/float64(tot),
			100*float64(cm[0][1])/float64(tot),
			100*float64(cm[1][0])/float64(tot))
	}
}
