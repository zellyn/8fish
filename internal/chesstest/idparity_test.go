package chesstest

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zellyn/8fish/internal/mirror"
)

// ---------------------------------------------------------------------------
// MULTI-ITERATION (iterative-deepening) asm<->mirror parity.
//
// The two existing tree gates are both blind to cross-iteration state:
//
//   - TestFullGameMirrorParity drives asmSearchFixed / mirror.SearchFixed,
//     which is ONE iterate at the cap ("a single iterate at the cap"). No TT
//     entry ever survives into a later iteration there, so a divergence in
//     what the TT carries FORWARD cannot show up at all.
//   - TestBudgetModeParity does run real ID, but it compares only the FINAL
//     totals, and only on the subset where both sides happened to complete the
//     same depth with no abort — and which depth a budget buys is decided by a
//     cycle model good to ~12%. A systematic node-count difference can hide
//     there as "different depth, not compared".
//
// This gate compares the two engines ITERATION BY ITERATION: the asm's ID
// driver is probed at its `iterate` label and the mirror at its IterHook, so
// the cumulative (nodes, makes, evals) after every completed iteration must
// agree EXACTLY. Only the common prefix of iterations is compared, so the
// (cycle-model-dependent) decision of WHEN to stop deepening — which
// TestBudgetModeParity already gates — cannot mask or fake a divergence here.
//
// Why the counts must be exact: iteration d's tree is a pure function of
// (position, d, feature mask, the state carried in from iterations 1..d-1).
// The fixed-depth gate proves the first part; this gate proves the carry-in —
// at mask 0x00 that carry-in is the TRANSPOSITION TABLE and nothing else
// (killers/history/countermoves are all off), so an exact match is the honest
// tolerance, and any drift is a real TT modelling difference.
// ---------------------------------------------------------------------------

// idHugeBudget is a per-move cycle budget large enough that the asm driver's
// predictive gate rarely stops the ID loop before the depth cap does. In the
// engine's 256-cycle units this is $7FFFFF, chosen so the driver's ABORTL =
// 2*BUDGET ($FFFFFE) still fits in 24 bits — at $800000 the shift would carry
// out and the hard-abort limit would wrap to 0, aborting instantly.
const idHugeBudget = 0x7FFFFF * 256

// idIter is one iteration's cumulative tree counters, sampled at the TOP of
// the NEXT iteration (asm: entry to `iterate`; mirror: IterHook), plus the
// grand totals at the end of the run.
type idIter struct{ nodes, makes, evals uint64 }

// idRun is one engine's ID run: starts[i] is the counter state at the top of
// iteration i+1 (so starts[i] is also the cumulative total after iteration i
// COMPLETED, for i >= 1), and tot is the grand total.
type idRun struct {
	starts []idIter
	tot    idIter
	move   string
	score  int
	depth  int
	abort  bool
	cycles uint64
}

// iters is the number of iterations the driver STARTED.
func (r idRun) iters() int { return len(r.starts) }

// cum returns the cumulative counters after iteration d completed, and whether
// that number is available (it is, for every iteration that a later iteration
// followed, plus the last one when the run did not hard-abort).
func (r idRun) cum(d int) (idIter, bool) {
	if d < 1 {
		return idIter{}, false
	}
	if d < len(r.starts) {
		return r.starts[d], true
	}
	if d == len(r.starts) && !r.abort {
		return r.tot, true
	}
	return idIter{}, false
}

// asmSearchIDLadder runs the asm engine in BUDGET mode (real iterative
// deepening) with idHugeBudget and a depth cap, sampling the tree counters at
// every entry to `iterate`.
func asmSearchIDLadder(bin []byte, probes parityProbes, iterateAddr uint16,
	fen string, cap byte, features byte) (idRun, error) {

	var r idRun
	pos, err := ParseFEN(fen)
	if err != nil {
		return r, err
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		return r, err
	}
	SetFeatures(m, defs, features)
	SetFeatures2(m, defs, 0)
	SetBudget(m, defs, idHugeBudget, cap)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove

	var cur idIter
	exited, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
		switch pc {
		case probes.search:
			cur.nodes++
		case probes.make_:
			cur.makes++
		case probes.eval:
			cur.evals++
		case iterateAddr:
			r.starts = append(r.starts, cur)
		}
	})
	if err != nil {
		return r, err
	}
	if !exited {
		return r, fmt.Errorf("engine did not exit")
	}
	r.tot = cur
	r.cycles = m.Cycles
	r.score = int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
	if m.Mem.Main[defs["BESTFROM"]] == byte(defs["NOSQ"]) {
		r.move = "none"
	} else {
		r.move = MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
	}
	r.depth = int(m.Mem.Main[defs["CURDEPTH"]])
	if r.depth > int(cap) {
		r.depth = int(cap)
	}
	r.abort = m.Mem.Main[defs["ABORT"]] != 0
	return r, nil
}

// mirrorSearchIDLadder is the mirror-side twin: the same ID mode
// (SearchCycleBudget) at the same budget and cap, sampled at IterHook.
func mirrorSearchIDLadder(cfg parityConfig, fen string, cap int) (idRun, error) {
	var r idRun
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		return r, err
	}
	me := cfg.mirrorEngine()
	me.CycleTrack = false // SearchCycleBudget turns accounting on
	me.SetPosition(mp)
	me.IterHook = func(int) {
		r.starts = append(r.starts, idIter{me.Cyc.Nodes, me.Cyc.Makes, me.Cyc.Evals})
	}
	mb, sc := me.SearchCycleBudget(idHugeBudget, cap)
	r.tot = idIter{me.Cyc.Nodes, me.Cyc.Makes, me.Cyc.Evals}
	r.move, r.score = mb.UCI(), sc
	r.depth, r.abort = me.CompletedDepth, me.HardAborted()
	r.cycles = me.Cyc.Est
	return r, nil
}

// idParityConfigs are the masks the divergence report named: 0x00 (bare — the
// TT is then the ONLY state carried between iterations), 0x1f (the screening
// tier) and 0x5f (the shipped gameplay mask).
var idParityConfigs = []parityConfig{
	{"bare-0x00", 0x00},
	{"plain-0x1f", 0x1f},
	{"ship-0x5f", 0x1f | ftCkExt},
}

// idDiff is one (config, fen) comparison's outcome.
type idDiff struct {
	cfg, fen           string
	aIters, mIters     int
	compared           int
	firstBad           int // first iteration whose cumulative counts differ (0 = none)
	aCum, mCum         idIter
	aMove, mMove       string
	aScore, mScore     int
	aDepth, mDepth     int
	aCycles, mCycles   uint64
	aAborted, mAborted bool
	perIterA, perIterB []idIter // per-iteration (non-cumulative) costs
}

// compareIDRuns compares two ID runs iteration by iteration over their common
// prefix and reports the first iteration at which the cumulative tree counters
// differ (0 = none).
func compareIDRuns(a, m idRun) (firstBad, compared int, aCum, mCum idIter) {
	for d := 1; ; d++ {
		ac, aok := a.cum(d)
		mc, mok := m.cum(d)
		if !aok || !mok {
			return 0, d - 1, idIter{}, idIter{}
		}
		compared = d
		if ac != mc {
			return d, d, ac, mc
		}
	}
}

// runIDPair runs both engines on one (config, fen) and compares them.
func runIDPair(bin []byte, probes parityProbes, iterateAddr uint16,
	cfg parityConfig, fen string, cap int) (idDiff, error) {

	d := idDiff{cfg: cfg.name, fen: fen}
	a, err := asmSearchIDLadder(bin, probes, iterateAddr, fen, byte(cap), cfg.features)
	if err != nil {
		return d, fmt.Errorf("asm %q: %w", fen, err)
	}
	m, err := mirrorSearchIDLadder(cfg, fen, cap)
	if err != nil {
		return d, fmt.Errorf("mirror %q: %w", fen, err)
	}
	d.aIters, d.mIters = a.iters(), m.iters()
	d.aMove, d.mMove = a.move, m.move
	d.aScore, d.mScore = a.score, m.score
	d.aDepth, d.mDepth = a.depth, m.depth
	d.aCycles, d.mCycles = a.cycles, m.cycles
	d.aAborted, d.mAborted = a.abort, m.abort
	d.firstBad, d.compared, d.aCum, d.mCum = compareIDRuns(a, m)
	// per-iteration costs, for the report
	prevA, prevM := idIter{}, idIter{}
	for i := 1; ; i++ {
		ac, aok := a.cum(i)
		mc, mok := m.cum(i)
		if !aok || !mok {
			break
		}
		d.perIterA = append(d.perIterA, idIter{ac.nodes - prevA.nodes, ac.makes - prevA.makes, ac.evals - prevA.evals})
		d.perIterB = append(d.perIterB, idIter{mc.nodes - prevM.nodes, mc.makes - prevM.makes, mc.evals - prevM.evals})
		prevA, prevM = ac, mc
	}
	return d, nil
}

func (d idDiff) String() string {
	s := fmt.Sprintf("[%s] %s\n  compared %d iterations (asm started %d, mirror %d)\n",
		d.cfg, d.fen, d.compared, d.aIters, d.mIters)
	if d.firstBad > 0 {
		s += fmt.Sprintf("  FIRST DIVERGING ITERATION: depth %d\n"+
			"    asm    cum nodes=%d makes=%d evals=%d\n"+
			"    mirror cum nodes=%d makes=%d evals=%d\n"+
			"    delta      nodes=%+d makes=%+d evals=%+d (%.3f%% of nodes)\n",
			d.firstBad, d.aCum.nodes, d.aCum.makes, d.aCum.evals,
			d.mCum.nodes, d.mCum.makes, d.mCum.evals,
			int64(d.aCum.nodes)-int64(d.mCum.nodes),
			int64(d.aCum.makes)-int64(d.mCum.makes),
			int64(d.aCum.evals)-int64(d.mCum.evals),
			100*float64(int64(d.aCum.nodes)-int64(d.mCum.nodes))/float64(d.mCum.nodes))
		for i := range d.perIterA {
			s += fmt.Sprintf("    iter %d: asm n=%d mk=%d ev=%d | mirror n=%d mk=%d ev=%d\n",
				i+1, d.perIterA[i].nodes, d.perIterA[i].makes, d.perIterA[i].evals,
				d.perIterB[i].nodes, d.perIterB[i].makes, d.perIterB[i].evals)
		}
	}
	s += fmt.Sprintf("  asm    move=%s score=%d depth=%d abort=%v cyc=%d\n"+
		"  mirror move=%s score=%d depth=%d abort=%v est=%d",
		d.aMove, d.aScore, d.aDepth, d.aAborted, d.aCycles,
		d.mMove, d.mScore, d.mDepth, d.mAborted, d.mCycles)
	return s
}

// idGateFENs is the gate's position set: structurally varied middlegames,
// endgames and near-mate positions (the same shapes parityExtraStarts covers),
// plus a slice of the openings pool taken at run time.
var idGateFENs = []string{
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
	"1k1r4/pp1b1R2/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - - 0 1",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	"4k3/8/4K3/8/8/8/4P3/8 w - - 0 1",
	"8/8/1p6/p1p5/P1P5/1P6/8/K6k w - - 0 1",
	"6k1/5ppp/8/8/8/8/5PPP/R5K1 w - - 0 1",
	"5rk1/5ppp/8/8/8/8/5PPP/4R1K1 w - - 0 1",
	"8/8/4k3/8/8/3K4/8/3R4 w - - 0 1",
	"8/8/3k4/8/8/3K4/8/7R w - - 40 1",
	"rnb1kbnr/pppp1ppp/8/4p3/6Pq/5P2/PPPPP2P/RNBQKBNR w KQkq - 1 3",
	"2r3k1/1p1b1pp1/p2p3p/4n3/2P1P3/1P4PP/P2N1PB1/2R3K1 w - - 0 25",
}

// TestIDIterationParity is the MULTI-ITERATION tree gate. Both engines run
// real iterative deepening (1,2,...,cap) on the same position at the same
// feature mask, and their cumulative node/make/eval counts must agree EXACTLY
// after every completed iteration of the common prefix — plus the same best
// move and score at the end.
//
// Knobs (env): IDPARITY_DEPTH (cap, default 7), IDPARITY_STARTS (cap on the
// number of openings-pool FENs added, default 8), IDPARITY_CFG (substring
// filter on the config name), IDPARITY_FEN (single FEN override).
func TestIDIterationParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: multi-iteration 6502 searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	probes := parityProbes{search: labels["search"], make_: labels["make"],
		eval: labels["eval"], makenull: labels["makenull"]}
	iterateAddr := labels["iterate"]
	if probes.search == 0 || probes.make_ == 0 || probes.eval == 0 || iterateAddr == 0 {
		t.Fatal("search/make/eval/iterate labels missing from asm/engine.lbl")
	}
	for _, s := range []string{"CURDEPTH", "ABORT", "BUDGET0", "NOSQ"} {
		if defs[s] == 0 {
			t.Fatalf("defs symbol %q missing", s)
		}
	}

	fens := append([]string(nil), idGateFENs...)
	if n := envInt("IDPARITY_STARTS", 8); n > 0 {
		pool := loadPoolStarts(t)
		if n > len(pool) {
			n = len(pool)
		}
		// Spread across the pool rather than taking the first n (openings from
		// one ECO family would all share a shape).
		step := len(pool) / n
		for i := 0; i < n; i++ {
			fens = append(fens, pool[i*step])
		}
	}
	if f := os.Getenv("IDPARITY_FEN"); f != "" {
		fens = []string{f}
	}
	cfgs := idParityConfigs
	if s := os.Getenv("IDPARITY_CFG"); s != "" {
		var sel []parityConfig
		for _, c := range idParityConfigs {
			if strings.Contains(c.name, s) {
				sel = append(sel, c)
			}
		}
		cfgs = sel
	}
	// The bare mask prunes nothing, so its trees explode ~6x per ply: cap it
	// two plies shallower to keep the emulator run tractable. Multi-iteration
	// TT effects are already fully exercised there (5 iterations of carry-in).
	capFor := func(name string) int {
		d := envInt("IDPARITY_DEPTH", 7)
		if name == "bare-0x00" && d > 5 {
			return 5
		}
		return d
	}

	type job struct {
		cfg parityConfig
		fen string
	}
	var jobs []job
	for _, cfg := range cfgs {
		for _, fen := range fens {
			jobs = append(jobs, job{cfg, fen})
		}
	}

	var mu sync.Mutex
	var all []idDiff
	var errs []string
	var wg sync.WaitGroup
	ch := make(chan job)
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				d, err := runIDPair(bin, probes, iterateAddr, j.cfg, j.fen, capFor(j.cfg.name))
				mu.Lock()
				if err != nil {
					errs = append(errs, fmt.Sprintf("[%s] %v", j.cfg.name, err))
				} else {
					all = append(all, d)
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()

	for _, e := range errs {
		t.Error(e)
	}
	sort.Slice(all, func(i, k int) bool {
		if all[i].cfg != all[k].cfg {
			return all[i].cfg < all[k].cfg
		}
		return all[i].fen < all[k].fen
	})

	var totalIters, diverged, moveDiv, scoreDiv, reported, deep, shallow int
	for _, d := range all {
		totalIters += d.compared
		if d.compared >= 4 {
			deep++
		}
		if d.compared < 2 {
			// The only legitimate one-iteration run is the driver's winning-mate
			// stop (asm engine.s `idok`: a completed iteration whose root score is
			// in the winning-mate zone is exact and final). Both engines must have
			// taken it, for the same reason.
			shallow++
			if d.aIters != 1 || d.mIters != 1 || d.aScore < 29696 || d.mScore < 29696 {
				t.Errorf("run stopped after one iteration for a reason OTHER than the "+
					"agreed winning-mate stop\n%s", d)
			}
		}
		if d.firstBad > 0 {
			diverged++
			if reported < 10 {
				reported++
				t.Errorf("MULTI-ITERATION TREE DIVERGENCE #%d\n%s", reported, d)
			}
			continue // move/score after a tree divergence is not independent news
		}
		// Move and score are only comparable when both drivers stopped at the
		// same depth; WHICH depth a budget buys is TestBudgetModeParity's job.
		if d.aDepth != d.mDepth || d.aAborted || d.mAborted {
			continue
		}
		if d.aMove != d.mMove {
			moveDiv++
			if reported < 10 {
				reported++
				t.Errorf("MULTI-ITERATION MOVE DIVERGENCE #%d\n%s", reported, d)
			}
		}
		if d.aScore != d.mScore {
			scoreDiv++
			if reported < 10 {
				reported++
				t.Errorf("MULTI-ITERATION SCORE DIVERGENCE #%d\n%s", reported, d)
			}
		}
	}

	t.Logf("multi-iteration parity: %d (config, position) pairs, %d iterations compared exactly",
		len(all), totalIters)
	t.Logf("  divergences: %d tree, %d move, %d score", diverged, moveDiv, scoreDiv)
	t.Logf("  ladder depth: %d pairs compared >= 4 iterations, %d stopped at the mate stop",
		deep, shallow)

	// Vacuity guards: this gate is worthless if the ladders are one iteration
	// deep (that is just the fixed-depth gate again) or if nothing ran.
	if len(all) == 0 {
		t.Fatal("no positions compared")
	}
	if deep < 4*len(all)/5 {
		t.Errorf("only %d of %d pairs compared >= 4 iterations: the ID ladders are too "+
			"shallow to exercise cross-iteration TT reuse", deep, len(all))
	}
}

// TestIDLadderProbe is the DIAGNOSTIC form of the gate: it prints the full
// per-iteration ladder for every (config, position) it runs, diverging or not.
// Set IDPROBE=1 to run it; it never fails.
func TestIDLadderProbe(t *testing.T) {
	if os.Getenv("IDPROBE") == "" {
		t.Skip("set IDPROBE=1 to run the ID-ladder diagnostic sweep")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	probes := parityProbes{search: labels["search"], make_: labels["make"],
		eval: labels["eval"], makenull: labels["makenull"]}
	iterateAddr := labels["iterate"]
	maxCap := envInt("IDPROBE_MAX", 7)
	fens := idGateFENs
	if f := os.Getenv("IDPROBE_FEN"); f != "" {
		fens = []string{f}
	}
	cfgs := idParityConfigs
	if s := os.Getenv("IDPROBE_CFG"); s != "" {
		var sel []parityConfig
		for _, c := range idParityConfigs {
			if strings.Contains(c.name, s) {
				sel = append(sel, c)
			}
		}
		cfgs = sel
	}
	for _, cfg := range cfgs {
		for _, fen := range fens {
			d, err := runIDPair(bin, probes, iterateAddr, cfg, fen, maxCap)
			if err != nil {
				t.Fatal(err)
			}
			status := "OK"
			if d.firstBad > 0 {
				status = fmt.Sprintf("DIVERGES at iteration %d", d.firstBad)
			}
			fmt.Printf("%-11s %-52s iters a=%d m=%d compared=%d  %s\n",
				cfg.name, fen, d.aIters, d.mIters, d.compared, status)
			for i := range d.perIterA {
				mark := ""
				if d.perIterA[i] != d.perIterB[i] {
					mark = "  <<<"
				}
				fmt.Printf("    iter %d: asm n=%-8d mk=%-8d ev=%-8d | mir n=%-8d mk=%-8d ev=%-8d%s\n",
					i+1, d.perIterA[i].nodes, d.perIterA[i].makes, d.perIterA[i].evals,
					d.perIterB[i].nodes, d.perIterB[i].makes, d.perIterB[i].evals, mark)
			}
			fmt.Printf("    asm %s/%d d%d abort=%v | mirror %s/%d d%d abort=%v\n",
				d.aMove, d.aScore, d.aDepth, d.aAborted, d.mMove, d.mScore, d.mDepth, d.mAborted)
		}
	}
}
