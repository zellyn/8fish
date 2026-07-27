package chesstest

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
)

// ---------------------------------------------------------------------------
// BUDGET-MODE asm<->mirror parity.
//
// TestFullGameMirrorParity is FIXED-DEPTH: it proves the two engines build the
// same tree when told the same depth. It says nothing about the ITERATIVE
// DEEPENING DRIVER that decides WHICH depths get searched under a per-move
// budget — and that driver is what every `-cbudget` screen runs. A divergence
// there (the mirror used a cumulative "halfway" soft gate and a 1x hard abort
// where asm/engine.s uses a PREDICTIVE soft gate and a 2x hard abort, and the
// mirror had no winning-mate stop) is invisible to every fixed-depth gate: it
// changes only how much DEPTH a given budget buys, which is exactly the
// quantity a budgeted screen measures. That divergence shipped, screened
// features for weeks, and was found by hand-reading the two drivers.
//
// This gate closes that hole. At a matched per-move cycle budget it compares,
// per position:
//
//	completed depth   asm CURDEPTH at report  vs  mirror CompletedDepth
//	chosen move       BESTFROM/BESTTO/FLAGS   vs  mirror Best
//	root score        SCORE                   vs  mirror RootScore
//	tree size         search()/make()/eval() entries on both sides
//	spend             emulated 6502 cycles    vs  mirror Cyc.Est
//
// WHAT IT CAN CATCH: any difference in the ID POLICY — the soft-start gate,
// the hard-abort multiple, the mate stop, the discard-partial-iteration rule,
// the depth-cap comparison — and any drift in the mirror's CYCLE MODEL, which
// is the other half of what a budgeted screen measures. Measured sensitivity
// (284 positions, this exact configuration): with the pre-fix mirror driver
// the openings subset reports 65.6% exact depth and a +53/-1 one-sided skew
// (both assertions fail); with the corrected driver, 90.6% and +9/-6.
//
// WHAT IT CANNOT CATCH:
//   - sub-iteration timing effects. The two engines price cycles differently
//     (the asm counts real emulated 6502 cycles; the mirror accumulates a
//     least-squares fit good to ~8% per MASK but ~21% per individual search),
//     so near a gate boundary the same policy legally buys one more or one
//     fewer iteration on either side. Only a SYSTEMATIC (one-sided) depth skew
//     is diagnostic.
//   - anything that leaves the completed-depth sequence intact.
//   - it is not a tree gate in its own right: tree equality is only asserted
//     on the subset where both sides completed the same depths without a hard
//     abort. (There it must be EXACT — see below.)
//   - the asm's 128-node clock-poll granularity and its ~8-12k-cycle engine
//     preamble (measured here as aEntry) are real asm<->mirror differences the
//     gate measures but deliberately does not fail on: both are <1% of any
//     real budget.
//   - a SYSTEMATIC per-iteration tree difference. Tree equality is asserted
//     only on the same-depth/no-abort subset, and which depth a budget buys is
//     itself a cycle-model decision, so a cross-iteration divergence can hide
//     as "different depth, not compared". TestIDIterationParity closes that:
//     it compares the two ID ladders ITERATION BY ITERATION over their common
//     prefix, which no stop decision can mask.
//   - the NODE-budgeted mode (mirror.SearchBudget) has NO asm counterpart at
//     all — the asm's budget is denominated in cycles only — so it cannot be
//     gated this way. It shares SearchCycleBudget's driver code and idPredict,
//     which is the best available assurance.
//
// TOLERANCES, and why they are what they are:
//   - completed depth: |delta| <= 1 on EVERY position. A ~20% cycle-model
//     error cannot move a decision by two iterations: an iteration costs 2-6x
//     the previous one, so two iterations is a >4x error.
//   - completed depth exact on >= 70% of positions overall.
//   - depth SKEW (asm-deeper minus mirror-deeper) within +-10% of n, asserted
//     on the openings-pool AND endgame/near-mate subsets. Until 2026-07-27 it
//     was asserted on the openings pool only, because the cycle model
//     over-priced endgame nodes ~30% (this gate measured endgame spend ratio
//     0.776 and skew +35/-0 against +3 on the openings pool) and asserting a
//     policy invariant on top of a pricing bias would just encode the bias.
//     The phase-aware re-fit removed the bias — endgame spend ratio 1.023,
//     skew -5, depth exact 71.8% -> 92.7% — so the carve-out went with it.
//   - node/make/eval counts, move and score: EXACT on the same-depth, no-abort
//     subset. The honest tolerance there is zero, because the tree is a pure
//     function of (position, depth sequence) and the fixed-depth gate already
//     proves the per-depth trees are identical.
//   - spend ratio (asm cycles / mirror Cyc.Est) on identical trees: median
//     within [0.85, 1.15] on each of the two subsets — the cycle model's own
//     documented accuracy. (Measured 2026-07-27: 1.073 openings, 1.023
//     endgame. The model is fit at masks 0x1F/0x07/0x00; half of this gate
//     runs the shipped 0x5F, whose check extensions the model never saw, so a
//     few points of level offset is expected. What matters is that the two
//     subsets now agree with each other to 5 points, where they used to
//     differ by 22.) This is the instrument-health assertion; it fires
//     if the model drifts or an operation stops being charged.
// ---------------------------------------------------------------------------

// budgetPly is one position's budget-mode comparison.
type budgetPly struct {
	cfg    string
	budget uint64
	fen    string
	pool   bool // from tools/openings-pool.epd (the cycle model's fit region)

	aMove   string
	aScore  int
	aDepth  int // CURDEPTH at report = last COMPLETED iteration
	aAbort  bool
	aCycles uint64
	aEntry  uint64 // cycles before the first search node (engine preamble)
	aNodes  uint64
	aMakes  uint64
	aEvals  uint64

	mMove   string
	mScore  int
	mDepth  int
	mAbort  bool
	mCycles uint64 // Cyc.Est
	mNodes  uint64
	mMakes  uint64
	mEvals  uint64
}

// sameTree reports whether this position is in the subset where the trees MUST
// be identical: both engines completed the same depths and neither threw away
// a partial iteration.
func (b budgetPly) sameTree() bool {
	return b.aDepth == b.mDepth && !b.aAbort && !b.mAbort
}

func (b budgetPly) String() string {
	return fmt.Sprintf(
		"[%s budget=%d] %s\n"+
			"    asm    depth=%-2d move=%-6s score=%-7d nodes=%-8d makes=%-8d evals=%-8d cyc=%-10d abort=%v\n"+
			"    mirror depth=%-2d move=%-6s score=%-7d nodes=%-8d makes=%-8d evals=%-8d est=%-10d abort=%v",
		b.cfg, b.budget, b.fen,
		b.aDepth, b.aMove, b.aScore, b.aNodes, b.aMakes, b.aEvals, b.aCycles, b.aAbort,
		b.mDepth, b.mMove, b.mScore, b.mNodes, b.mMakes, b.mEvals, b.mCycles, b.mAbort)
}

// asmSearchBudgeted runs the asm engine in BUDGET mode (iterative deepening
// under a cycle budget) with tree probes, and reports what the ID driver
// actually did: the completed depth (CURDEPTH, which the driver decrements
// when it discards an aborted iteration) and whether ABORT fired.
func asmSearchBudgeted(bin []byte, probes parityProbes, fen string, budget uint64,
	depthCap byte, features byte) (mv string, score, depth int, abort bool,
	nodes, makes, evals, cycles, entry uint64, err error) {

	pos, err := ParseFEN(fen)
	if err != nil {
		return
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		return
	}
	SetFeatures(m, defs, features)
	SetFeatures2(m, defs, 0)
	SetBudget(m, defs, budget, depthCap)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	exited, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
		switch pc {
		case probes.search:
			if nodes == 0 {
				// Cycles burned before the first search node: the LC-code copy,
				// evalinit and the driver preamble. The mirror never charges
				// this, so it is the constant part of the spend offset.
				entry = m.Cycles
			}
			nodes++
		case probes.make_:
			makes++
		case probes.eval:
			evals++
		}
	})
	if err != nil {
		return
	}
	if !exited {
		err = fmt.Errorf("engine did not exit")
		return
	}
	score = int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
	if m.Mem.Main[defs["BESTFROM"]] == byte(defs["NOSQ"]) {
		mv = "none" // mate/stalemate root: matches mirror Move.UCI()
	} else {
		mv = MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
	}
	// CURDEPTH at report is the last COMPLETED iteration, EXCEPT when the depth
	// cap bound the loop: `inc CURDEPTH / cmp MAXCAP / jmp report` leaves it at
	// cap+1. Clamp so both sides mean the same thing.
	depth = int(m.Mem.Main[defs["CURDEPTH"]])
	if depth > int(depthCap) {
		depth = int(depthCap)
	}
	abort = m.Mem.Main[defs["ABORT"]] != 0
	cycles = m.Cycles
	return
}

// budgetStats accumulates one subset's comparison statistics.
type budgetStats struct {
	n, exact, deeper, shallower int
	sameTree                    int
	ratios                      []float64 // asm search cycles / mirror Est, same-tree only
}

// TestBudgetModeParity is the BUDGET-MODE gate: at a matched per-move cycle
// budget, the asm driver and mirror.SearchCycleBudget must buy the same depth
// and (where they do) run the same tree.
//
// Knobs (env): BPARITY_STARTS (cap on positions, 0 = all), BPARITY_CFG
// (substring filter on the config name), BPARITY_DUMP (log every row as TSV).
func TestBudgetModeParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: budgeted 6502 searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	probes := parityProbes{
		search: labels["search"], make_: labels["make"],
		eval: labels["eval"], makenull: labels["makenull"],
	}
	if probes.search == 0 || probes.make_ == 0 || probes.eval == 0 {
		t.Fatal("search/make/eval labels missing from asm/engine.lbl")
	}
	for _, s := range []string{"CURDEPTH", "ABORT", "BUDGET0", "NOSQ"} {
		if defs[s] == 0 {
			t.Fatalf("defs symbol %q missing", s)
		}
	}

	// Budgets in emulated cycles (the IIe runs ~1.02M cycles/second), spanning
	// the range the -cbudget screens use.
	budgets := []uint64{2_000_000, 8_000_000}
	const depthCap = 20

	pool := loadPoolStarts(t)
	nPool := len(pool)
	starts := append(pool, parityExtraStarts...)
	if n := envInt("BPARITY_STARTS", 0); n > 0 && n < len(starts) {
		starts = starts[:n]
		if nPool > n {
			nPool = n
		}
	}
	cfgFilter := os.Getenv("BPARITY_CFG")

	type job struct {
		cfg    parityConfig
		idx    int
		budget uint64
	}
	var jobs []job
	for _, cfg := range parityConfigs {
		if cfgFilter != "" && !strings.Contains(cfg.name, cfgFilter) {
			continue
		}
		for _, b := range budgets {
			for i := range starts {
				jobs = append(jobs, job{cfg, i, b})
			}
		}
	}

	var mu sync.Mutex
	var all []budgetPly
	var errs []string
	var wg sync.WaitGroup
	ch := make(chan job)
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				fen := starts[j.idx]
				amv, asc, ad, aab, an, amk, aev, acyc, aentry, err := asmSearchBudgeted(
					bin, probes, fen, j.budget, depthCap, j.cfg.features)
				if err != nil {
					mu.Lock()
					errs = append(errs, fmt.Sprintf("asm %q: %v", fen, err))
					mu.Unlock()
					continue
				}
				mp, err := mirror.ParseFEN(fen)
				if err != nil {
					mu.Lock()
					errs = append(errs, fmt.Sprintf("mirror ParseFEN %q: %v", fen, err))
					mu.Unlock()
					continue
				}
				me := j.cfg.mirrorEngine()
				me.CycleTrack = false // SearchCycleBudget turns accounting on
				me.SetPosition(mp)
				mb, msc := me.SearchCycleBudget(j.budget, depthCap)
				mu.Lock()
				all = append(all, budgetPly{
					cfg: j.cfg.name, budget: j.budget, fen: fen, pool: j.idx < nPool,
					aMove: amv, aScore: asc, aDepth: ad, aAbort: aab,
					aCycles: acyc, aEntry: aentry, aNodes: an, aMakes: amk, aEvals: aev,
					mMove: mb.UCI(), mScore: msc, mDepth: me.CompletedDepth,
					mAbort: me.HardAborted(), mCycles: me.Cyc.Est,
					mNodes: me.Cyc.Nodes, mMakes: me.Cyc.Makes, mEvals: me.Cyc.Evals,
				})
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
		if all[i].budget != all[k].budget {
			return all[i].budget < all[k].budget
		}
		return all[i].fen < all[k].fen
	})
	if len(all) == 0 {
		t.Fatal("no positions compared")
	}

	// Per-subset statistics: the openings pool is the cycle model's fit
	// region, the extras (endgames, near-mate) are outside it.
	var poolS, extraS, allS budgetStats
	depthDelta := map[int]int{}
	var treeDiv, moveDiv, scoreDiv, aborts, mAborts, reported int

	for _, r := range all {
		d := r.aDepth - r.mDepth
		depthDelta[d]++
		sub := &extraS
		if r.pool {
			sub = &poolS
		}
		for _, s := range []*budgetStats{&allS, sub} {
			s.n++
			switch d {
			case 0:
				s.exact++
			case 1:
				s.deeper++
			case -1:
				s.shallower++
			}
		}
		if (d < -1 || d > 1) && reported < 20 {
			reported++
			t.Errorf("DEPTH DIVERGENCE #%d (|delta|=%d > 1)\n%s", reported, d, r)
		}
		if r.aAbort {
			aborts++
		}
		if r.mAbort {
			mAborts++
		}
		if !r.sameTree() {
			continue
		}
		for _, s := range []*budgetStats{&allS, sub} {
			s.sameTree++
			if r.mCycles > 0 {
				s.ratios = append(s.ratios, float64(r.aCycles-r.aEntry)/float64(r.mCycles))
			}
		}
		if r.aNodes != r.mNodes || r.aMakes != r.mMakes || r.aEvals != r.mEvals {
			treeDiv++
			if reported < 20 {
				reported++
				t.Errorf("TREE DIVERGENCE #%d (same depth, no abort: trees must be identical)\n%s",
					reported, r)
			}
			continue
		}
		if r.aMove != r.mMove {
			moveDiv++
			if reported < 20 {
				reported++
				t.Errorf("MOVE DIVERGENCE #%d (same depth, no abort, same tree)\n%s", reported, r)
			}
		}
		if r.aScore != r.mScore {
			scoreDiv++
			if reported < 20 {
				reported++
				t.Errorf("SCORE DIVERGENCE #%d (same depth, no abort, same tree)\n%s", reported, r)
			}
		}
	}

	if os.Getenv("BPARITY_DUMP") != "" {
		for _, r := range all {
			t.Logf("ROW\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%v\t%v\t%s",
				r.cfg, r.budget, r.aDepth, r.mDepth, r.aCycles, r.aEntry, r.mCycles,
				r.aNodes, r.mNodes, r.aAbort, r.mAbort, r.fen)
		}
	}

	var deltas []int
	for d := range depthDelta {
		deltas = append(deltas, d)
	}
	sort.Ints(deltas)
	var hist []string
	for _, d := range deltas {
		hist = append(hist, fmt.Sprintf("%+d:%d", d, depthDelta[d]))
	}
	n := allS.n
	t.Logf("budget-mode parity: %d positions (%d configs x %d budgets x %d starts)",
		n, len(parityConfigs), len(budgets), len(starts))
	t.Logf("  depth delta (asm-mirror) histogram: %s", strings.Join(hist, " "))
	for _, s := range []struct {
		name string
		st   *budgetStats
	}{{"all", &allS}, {"openings-pool", &poolS}, {"endgame/extra", &extraS}} {
		if s.st.n == 0 {
			continue
		}
		t.Logf("  [%-13s] n=%-4d depth exact %d (%.1f%%)  asm-deeper %d  mirror-deeper %d  skew %+d",
			s.name, s.st.n, s.st.exact, 100*float64(s.st.exact)/float64(s.st.n),
			s.st.deeper, s.st.shallower, s.st.deeper-s.st.shallower)
		t.Logf("  %17s same-tree n=%d  spend ratio asm/mirror: median %.3f  p10 %.3f  p90 %.3f",
			"", s.st.sameTree, median(s.st.ratios), pctile(s.st.ratios, 10), pctile(s.st.ratios, 90))
	}
	t.Logf("  hard aborts: asm %d, mirror %d", aborts, mAborts)
	t.Logf("  exact-tree subset: %d positions, %d tree / %d move / %d score divergences",
		allS.sameTree, treeDiv, moveDiv, scoreDiv)

	// --- assertions (rationale in the file header) ---
	if frac := float64(allS.exact) / float64(n); frac < 0.70 {
		t.Errorf("completed depth matched exactly on only %.1f%% of positions (want >= 70%%): "+
			"the ID drivers have diverged, or the cycle model has drifted", 100*frac)
	}
	// The BIAS test — the sharp one, on the cycle model's fit region. Model
	// error is symmetric noise there, so a systematic one-sided skew means the
	// two ID drivers disagree on POLICY: one buys more depth than the other at
	// the same budget, which is exactly the mirror->asm "compression" this gate
	// exists to detect. Measured over 160 openings positions: corrected driver
	// +9/-6 (skew +3); pre-fix driver +54/-1 (skew +53, tolerance +-16).
	//
	// This is now asserted on the ENDGAME subset too. It used to be scoped to
	// the openings pool because the cycle model over-priced endgame nodes
	// ~30% (endgame spend ratio 0.776, skew +35/-0 on this very gate), and
	// asserting a policy invariant on top of a pricing bias would have
	// encoded the bias. The 2026-07-27 phase-aware re-fit removed it —
	// endgame spend ratio 1.023, skew -5 — so the carve-out is gone with it.
	for _, sub := range []struct {
		name string
		s    *budgetStats
	}{{"openings pool", &poolS}, {"endgame/extra", &extraS}} {
		if sub.s.n == 0 {
			continue
		}
		skew, tol := sub.s.deeper-sub.s.shallower, sub.s.n/10
		if skew < -tol || skew > tol {
			t.Errorf("one-sided depth skew on the %s subset: asm deeper on %d, mirror deeper on %d "+
				"(skew %+d, tolerance +-%d of %d): the budget-mode ID drivers disagree on POLICY, "+
				"not just on cycle pricing", sub.name, sub.s.deeper, sub.s.shallower, skew, tol, sub.s.n)
		}
		// Instrument health: on identical trees the mirror's cycle estimate must
		// track the asm's real spend to the model's documented accuracy.
		if m := median(sub.s.ratios); m < 0.85 || m > 1.15 {
			t.Errorf("cycle model drift on the %s subset: median asm/mirror spend ratio on identical "+
				"trees is %.3f (want 0.85..1.15) — every cycle-budget screen is mis-scaled by this factor",
				sub.name, m)
		}
	}
	if allS.sameTree < n/4 {
		t.Errorf("exact-tree subset is only %d/%d positions: the gate lost its teeth", allS.sameTree, n)
	}
}

func median(xs []float64) float64 { return pctile(xs, 50) }

// pctile returns the p-th percentile of xs (nearest-rank), or 0 for an empty
// slice. It sorts a copy.
func pctile(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := p * len(s) / 100
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}
