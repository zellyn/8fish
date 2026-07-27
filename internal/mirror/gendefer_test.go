package mirror

import (
	"fmt"
	"sort"
	"testing"
)

// gdPos is the phase-diverse profiling set used by the asm round-5
// profile (internal/chesstest/profile_r5_test.go), kept identical so the
// mirror's opportunity numbers and the asm's cost numbers describe the
// same workload.
type gdPos struct {
	phase string
	fen   string
}

var gdPositions = []gdPos{
	{"midgame", "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8"},
	{"midgame", "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8"},
	{"opening", "rnbqkb1r/pp2pppp/3p1n2/2pP4/4P3/2N5/PPP2PPP/R1BQKBNR w KQkq - 0 5"},
	{"tactical", "r3k2r/pbpnqppp/1p2pn2/3p4/2PP4/1PN1PN2/PB2QPPP/R3K2R w KQkq - 0 11"},
	{"endgame", "8/2p2pk1/1p1p2p1/p2P4/P1P1PP2/1P4K1/8/8 w - - 0 1"},
	{"endgame", "8/3k4/p1p5/P1P5/3r4/5R2/5PPP/6K1 w - - 0 1"},
}

// gdEngine builds the SHIPPED gameplay configuration: FEATURES 0x5F
// (FtAll = 0x1F plus the asm's FT_CKEXT check extensions, MaxExt = 1),
// FEATURES2 = 0, recap2 quiescence, default LMR/futility/weights. No
// FtSEE/FtHistory, so the five-pass moveLoop runs — the loop the asm
// actually ships.
func gdEngine() *Engine {
	e := NewEngine() // FtAll, DefaultLMR/Futility/QS/Weights
	e.CheckExt = CheckExtParams{MaxExt: 1}
	e.GenDeferCount = true
	return e
}

// TestGenDeferOpportunity (diagnostic, deferred-generation study):
// at full-width nodes, how often does the TT move ALONE end the node?
//
// asm/search.s snode generates the ENTIRE pseudo-legal move list before
// pass 0 hunts the TT move in it. Every node whose TT move produces a
// beta cutoff therefore paid for a full generation it never used. This
// test measures that fraction on the same phase-diverse workload and at
// the same per-move budget (30M estimated cycles, the asm's SetBudget)
// as the asm-side cost instrument.
//
// The counters are pure diagnostics (GenDeferCount off by default); the
// search tree is bit-identical with them on or off.
func TestGenDeferOpportunity(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	const budget = 30_000_000 // estimated 6502 cycles, = asm SetBudget

	agg := GenDeferStats{}
	perPhase := map[string]*GenDeferStats{}
	var aggNodes uint64

	for _, pos := range gdPositions {
		p, err := ParseFEN(pos.fen)
		if err != nil {
			t.Fatal(err)
		}
		e := gdEngine()
		e.SetPosition(p)
		mv, sc := e.SearchCycleBudget(budget, 24)
		s := e.GenDefer
		t.Logf("%-8s %-24s best %s score %5d  depth %d  nodes %d  est %dM cyc",
			pos.phase, pos.fen[:24], mv.UCI(), sc, e.CompletedDepth, e.Nodes, e.Cyc.Est/1_000_000)
		aggNodes += e.Nodes
		if perPhase[pos.phase] == nil {
			perPhase[pos.phase] = &GenDeferStats{}
		}
		for _, dst := range []*GenDeferStats{&agg, perPhase[pos.phase]} {
			dst.FWNodes += s.FWNodes
			dst.Evasion += s.Evasion
			dst.TTAvail += s.TTAvail
			dst.TTFound += s.TTFound
			dst.TTLegal += s.TTLegal
			dst.TTCut += s.TTCut
			dst.ListLen += s.ListLen
			dst.CutLen += s.CutLen
			dst.AvailLen += s.AvailLen
			dst.QSNodes += s.QSNodes
			for i := range dst.AvailType {
				dst.AvailType[i] += s.AvailType[i]
			}
			dst.AvailRay += s.AvailRay
			dst.AvailRayN += s.AvailRayN
			dst.AvailCap += s.AvailCap
			dst.AvailPromo += s.AvailPromo
			dst.AvailCastle += s.AvailCastle
			dst.AvailEP += s.AvailEP
		}
	}

	out := gdReport("AGGREGATE", &agg) + gdPhases(perPhase)
	t.Logf("\ntotal mirror nodes %d\n%s", aggNodes, out)
}

// TestGenDeferSelfPlay (diagnostic): the same measurement over WHOLE
// self-play games at the shipped config, so the phase mix is the one the
// engine actually meets over a game rather than six hand-picked FENs.
// Six games from distinct openings at the SHIPPED per-move budget
// (~30.6M emulated cycles = the 30000ms control in runs/pool.sh).
// Bucketed by the root position's material phase (0..24).
func TestGenDeferSelfPlay(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	const budget = 30_000_000 // = the shipped ~30s emulated control
	openings := [][]string{
		{"e2e4", "e7e5", "g1f3", "b8c6"},
		{"d2d4", "d7d5", "c2c4", "e7e6"},
		{"e2e4", "c7c5", "g1f3", "d7d6"},
		{"c2c4", "e7e5", "b1c3", "g8f6"},
		{"d2d4", "g8f6", "c2c4", "g7g6"},
		{"e2e4", "e7e6", "d2d4", "d7d5"},
	}
	bucket := func(phase int) string {
		switch {
		case phase >= 20:
			return "opening"
		case phase >= 10:
			return "midgame"
		default:
			return "endgame"
		}
	}
	agg := GenDeferStats{}
	perPhase := map[string]*GenDeferStats{}
	addTo := func(dst *GenDeferStats, d GenDeferStats) {
		dst.FWNodes += d.FWNodes
		dst.Evasion += d.Evasion
		dst.TTAvail += d.TTAvail
		dst.TTFound += d.TTFound
		dst.TTLegal += d.TTLegal
		dst.TTCut += d.TTCut
		dst.ListLen += d.ListLen
		dst.CutLen += d.CutLen
		dst.AvailLen += d.AvailLen
		dst.QSNodes += d.QSNodes
		for i := range dst.AvailType {
			dst.AvailType[i] += d.AvailType[i]
		}
		dst.AvailRay += d.AvailRay
		dst.AvailRayN += d.AvailRayN
		dst.AvailCap += d.AvailCap
		dst.AvailPromo += d.AvailPromo
		dst.AvailCastle += d.AvailCastle
		dst.AvailEP += d.AvailEP
	}
	sub := func(a, b GenDeferStats) GenDeferStats {
		out := a
		out.FWNodes -= b.FWNodes
		out.Evasion -= b.Evasion
		out.TTAvail -= b.TTAvail
		out.TTFound -= b.TTFound
		out.TTLegal -= b.TTLegal
		out.TTCut -= b.TTCut
		out.ListLen -= b.ListLen
		out.CutLen -= b.CutLen
		out.AvailLen -= b.AvailLen
		out.QSNodes -= b.QSNodes
		for i := range out.AvailType {
			out.AvailType[i] -= b.AvailType[i]
		}
		out.AvailRay -= b.AvailRay
		out.AvailRayN -= b.AvailRayN
		out.AvailCap -= b.AvailCap
		out.AvailPromo -= b.AvailPromo
		out.AvailCastle -= b.AvailCastle
		out.AvailEP -= b.AvailEP
		return out
	}

	start, err := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	moves := 0
	for gi, opening := range openings {
		we, be := gdEngine(), gdEngine()
		gp := *start
		for _, ms := range opening {
			if err := applyUCI(we, &gp, ms); err != nil {
				t.Fatal(err)
			}
		}
		seen := map[uint32]int{}
		for ply := 0; ply < 200; ply++ {
			eng := we
			if gp.Side != 0 {
				eng = be
			}
			eng.SetPosition(&gp)
			seen[eng.Pos.Hash]++
			if gp.Halfmove >= 100 || seen[eng.Pos.Hash] >= 3 ||
				(gp.Phase < 2 && !eng.anyPawn()) {
				break
			}
			before := eng.GenDefer
			best, _ := eng.SearchCycleBudget(budget, MaxPly-1)
			if best.From == NoSq {
				break // mate or stalemate
			}
			d := sub(eng.GenDefer, before)
			b := bucket(gp.Phase)
			if perPhase[b] == nil {
				perPhase[b] = &GenDeferStats{}
			}
			addTo(&agg, d)
			addTo(perPhase[b], d)
			moves++
			if err := applyUCI(eng, &gp, best.UCI()); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("game %d done (%d moves so far)", gi+1, moves)
	}
	t.Logf("\n%d self-play moves at %dM cyc/move\n%s", moves, budget/1_000_000,
		gdReport("AGGREGATE (self-play)", &agg)+gdPhases(perPhase))
}

// TestGenDeferCountersAreNoOp: the diagnostic counters must not perturb
// the tree. Same position, same budget, counters off vs on: identical
// move, score, node count and cycle estimate.
func TestGenDeferCountersAreNoOp(t *testing.T) {
	for _, pos := range gdPositions {
		p, err := ParseFEN(pos.fen)
		if err != nil {
			t.Fatal(err)
		}
		run := func(count bool) (Move, int, uint64, uint64) {
			e := gdEngine()
			e.GenDeferCount = count
			e.SetPosition(p)
			m, s := e.SearchCycleBudget(2_000_000, 24)
			return m, s, e.Nodes, e.Cyc.Est
		}
		m0, s0, n0, c0 := run(false)
		m1, s1, n1, c1 := run(true)
		if m0 != m1 || s0 != s1 || n0 != n1 || c0 != c1 {
			t.Errorf("%s: counters perturbed the search: (%v,%d,%d,%d) vs (%v,%d,%d,%d)",
				pos.fen, m0, s0, n0, c0, m1, s1, n1, c1)
		}
	}
}

// gdReport renders one GenDeferStats bucket.
func gdReport(name string, s *GenDeferStats) string {
	pc := func(a, b uint64) float64 {
		if b == 0 {
			return 0
		}
		return 100 * float64(a) / float64(b)
	}
	av := func(a, b uint64) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	return fmt.Sprintf(`--- %s ---
  full-width nodes (asm snode)      %8d   (of which in-check evasions %d = %.1f%%)
  qs capture nodes                  %8d
  TT move available                 %8d  = %5.1f%% of full-width nodes
  TT move present in the list       %8d  = %5.1f%% of TT-available   (missing: %d = %.1f%%)
  TT move legal (king-safe)         %8d  = %5.1f%% of TT-available
  TT move ALONE cut (beta cutoff)   %8d  = %5.1f%% of TT-available
  ==> generation entirely avoidable %8d  = %5.1f%% of ALL full-width nodes
  mean list length: all %.1f  tt-available %.1f  tt-cut %.1f (ratio cut/all %.3f)
`, name,
		s.FWNodes, s.Evasion, pc(s.Evasion, s.FWNodes),
		s.QSNodes,
		s.TTAvail, pc(s.TTAvail, s.FWNodes),
		s.TTFound, pc(s.TTFound, s.TTAvail), s.TTAvail-s.TTFound, pc(s.TTAvail-s.TTFound, s.TTAvail),
		s.TTLegal, pc(s.TTLegal, s.TTAvail),
		s.TTCut, pc(s.TTCut, s.TTAvail),
		s.TTCut, pc(s.TTCut, s.FWNodes),
		av(s.ListLen, s.FWNodes), av(s.AvailLen, s.TTAvail), av(s.CutLen, s.TTCut),
		av(s.CutLen, s.TTCut)/max(av(s.ListLen, s.FWNodes), 1e-9)) +
		fmt.Sprintf("  TT-move operand mix (n=%d): pawn %.1f%% knight %.1f%% bishop %.1f%% rook %.1f%% queen %.1f%% king %.1f%%\n"+
			"    captures %.1f%%  promos %.1f%%  castles %.1f%%  ep %.1f%%  mean slider ray steps %.2f (n=%d)\n",
			s.TTFound,
			pc(s.AvailType[Pawn], s.TTFound), pc(s.AvailType[Knight], s.TTFound),
			pc(s.AvailType[Bishop], s.TTFound), pc(s.AvailType[Rook], s.TTFound),
			pc(s.AvailType[Queen], s.TTFound), pc(s.AvailType[King], s.TTFound),
			pc(s.AvailCap, s.TTFound), pc(s.AvailPromo, s.TTFound),
			pc(s.AvailCastle, s.TTFound), pc(s.AvailEP, s.TTFound),
			av(s.AvailRay, s.AvailRayN), s.AvailRayN)
}

// gdPhases renders the per-phase buckets in a stable order.
func gdPhases(perPhase map[string]*GenDeferStats) string {
	phases := make([]string, 0, len(perPhase))
	for ph := range perPhase {
		phases = append(phases, ph)
	}
	sort.Strings(phases)
	out := ""
	for _, ph := range phases {
		out += gdReport(ph, perPhase[ph])
	}
	return out
}
