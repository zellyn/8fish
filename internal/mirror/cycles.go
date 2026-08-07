package mirror

// This file adds a calibrated 6502 CYCLE-cost model to the mirror engine.
//
// WHY. The mirror screens features by node-budgeted self-play before they
// are ported to asm. A node budget charges every feature ZERO
// implementation cost, so a term that is cheap in nodes but expensive in
// cycles (e.g. the FT_ROOKX rook set, ~219 cycles/eval-call, ~5% of all
// cycles) screens far too favorably. A cycle budget taxes each feature by
// its real per-node cost, so its measured Elo is automatically discounted.
//
// HOW. The engine keeps a running estimate of emulated 6502 cycles
// (Cyc.Est), bumped at each accountable operation by a calibrated cost.
// The costs (CycleCosts) are fit by least squares against the asm
// differential harness (internal/chesstest TestMicroABPhase): 69 fixed-depth
// searches — 23 positions spanning 3-32 pieces x feature masks
// 0x1f/0x07/0x00 — for which the harness reports the TRUE emulated-6502
// cycle total, the operation-entry counts (search/make/eval/attacked/
// ttprobe/generate) and the MATERIAL sums over those entries. The mirror
// replays the SAME 69 searches (identical trees, so identical operation
// counts) and its counts are regressed onto the asm cycle totals. See
// cycles_test.go for the fit and the predicted-vs-actual validation, and
// cycles.md for the coefficient table and known limitations.
//
// One coefficient is not a flat per-operation cost: the per-NODE cost is
// Node + NodePhase*phase, because real per-node work shrinks as material
// comes off. A constant per-node cost over-priced endgame nodes by ~30-40%
// and made every cycle-budget screen run shallower than the ship exactly
// where the engine loses most of its games.
//
// The whole apparatus is inert unless accounting is active for the search
// (CycleBudget != 0 or CycleTrack), so the existing NodeBudget path is
// byte-for-byte unchanged.

// CycleCosts is the calibrated cost, in emulated 6502 cycles, of each
// accountable mirror operation. Est accumulates count*cost as the search
// runs. The coefficients are effective asm costs: they price the mirror's
// OWN operations (as the mirror counts them) so that their weighted sum
// reproduces the asm's measured cycle total. Where a mirror operation does
// not correspond 1:1 to an asm routine (see the attacked() note below),
// the fitted coefficient absorbs the difference; it is not a pure
// per-routine cost.
type CycleCosts struct {
	// Node is the MATERIAL-INDEPENDENT part of the per-search()-entry
	// overhead not already attributed to the node's TT probe / eval /
	// makes (bookkeeping, repetition/50-move checks, pass sequencing).
	Node float64
	// NodePhase is the MATERIAL-DEPENDENT part of the per-node cost: it is
	// charged once per node, multiplied by the node's taper phase
	// (Pos.Phase, N=B=1 R=2 Q=4 per side, 0..24). It exists because the
	// per-node work is NOT constant — a node's quiescence move generation
	// and its attacked() slot scans get cheaper as pieces come off, so a
	// constant per-node cost over-charges low-material nodes (it
	// over-priced the 7-piece calibration endgame by ~30-40%). Phase, not
	// raw piece count, is the regressor the calibration selected: the
	// per-node work that actually scales is dominated by SLIDER ray walks
	// and by how many moves a piece emits, which pawns barely contribute
	// to. Both were measured (see cycles.md); phase fit better and is
	// already maintained incrementally by the mirror AND the asm, so it
	// costs nothing to charge. Applied as an integer cost per phase unit.
	NodePhase float64
	// Make is charged once per real make() and covers the paired unmake()
	// as well (every make has exactly one matching unmake, so the two are
	// perfectly collinear and cannot be separated by the fit). It also
	// carries the asm's in-make "gives-check" difference-table update,
	// which in the asm lives inside make() (the mirror computes the same
	// bit with a full attacked() scan; that scan is deliberately NOT
	// charged to Attacked — see below).
	Make float64
	// MakeNull is charged per makenull()/unmakenull() pair. makenull is a
	// separate asm routine, so it is not part of the make() count.
	MakeNull float64
	// Eval is the base cost of one eval() call (taper + pstruct + tempo).
	Eval float64
	// EvalTerm is the ADDITIONAL cost of one eval() call when experimental
	// EvalTerms are enabled — the configurable feature tax. The asm
	// FT_ROOKX extraterm measured 219 cycles/call; a future term with no
	// asm implementation yet should set a deliberately pessimistic value.
	// This is not part of the calibration fit (the calibration masks have
	// no extra terms); it is a knob a feature screen sets. See
	// EvalTermsCost.
	EvalTerm float64
	// Attacked is charged per attacked() scan that corresponds to an asm
	// attacked() call: legality (mover's king safe), castle safety, and the
	// root in-check test. The gives-check scan the mirror does inside
	// make() is NOT charged here (the asm does that via difference tables
	// inside make(); its cost lives in Make). See chargeAttacked.
	Attacked float64
	// TTProbe is charged once per FULL-WIDTH node (the mirror probes the TT
	// exactly once per full-width node). In the fitted reduced model it
	// therefore doubles as the full-width-node premium: it bundles the TT
	// probe/store plus the sprep dispatch (null-move, RFP/futility) that
	// only full-width nodes run. TTStore is a separate counter, priced 0 by
	// default (its cost is inside TTProbe).
	TTProbe float64
	TTStore float64
	// Generate is the fixed per-call cost of the move generator; MovePerGen
	// is the marginal cost per pseudo-legal move it emits.
	Generate   float64
	MovePerGen float64
	// SEE-classification costs (see.go), NOT part of the calibration fit
	// (default 0 = untaxed node-budget behavior; a cycle screen sets them
	// via PlayerCfg.SEECosts to the measured asm numbers).
	//   SEEGate: per capture reaching the victim-vs-attacker gate in the
	//     capture passes (asm: a type compare or one 64-byte table load).
	//   SEE: per classification call (the defended test / swap) — the
	//     per-variant headline number.
	//   SEERescan: per move-list item rescanned by the deferred
	//     losing-capture pass (asm: one more tier-filtered stack scan,
	//     charged per item, only when the pass actually runs).
	SEEGate, SEE, SEERescan float64
	// Countermove is the per-operation cost of the countermove heuristic:
	// one indexed table load + 2-byte compare on the probe (once per
	// full-width node where CM is active), and the 2-byte store on a quiet
	// beta cutoff. Like EvalTerm it is NOT part of the calibration fit
	// (defaults to 0, an untaxed node-budget run); a cycle screen sets it
	// via PlayerCfg.CMCost to a pessimistic per-node surcharge (~20-40).
	Countermove float64
	// QHist* are the cheap-quiet-history costs (qhist.go), NOT part of the
	// calibration fit (default 0 = untaxed; a cycle screen sets them via
	// PlayerCfg.QHistCosts). QHistProbe per counter probe (one indexed load
	// + compare, ~13 cyc with the base-page setup amortized); QHistUpdate
	// per saturating bump on a quiet cutoff (~30 cyc read-modify-write);
	// QHistAge per table BYTE halved once per root move (~8 cyc/byte LSR
	// sweep — 2-12K cycles/move depending on form, charged in newMove).
	QHistProbe, QHistUpdate, QHistAge float64
	// IIR is charged once per full-width node while internal iterative
	// reduction is enabled (the test itself: ~10 cyc — TTFROM is already in
	// a register after the probe). See iir.go.
	IIR float64
	// Razor is charged per razor margin test (one CMP against an eval the
	// RFP block already computed, ~10 cyc). See razor.go.
	Razor float64
	// TTRepl is charged per ttstore while the depth-preferred replacement
	// policy is enabled (read-compare of the incumbent's depth/age byte,
	// ~15 cyc). See ttrepl.go.
	TTRepl float64
	// EGTerm is the per-eval-call cost of the endgame-TECHNIQUE terms
	// (endgame.go), charged ONLY on the calls where the phase gate actually
	// fires (in the asm the gate is a 3-cycle compare-and-return, so a
	// middlegame eval pays nothing). Like EvalTerm it is not part of the
	// calibration fit; a screen sets it via PlayerCfg.EGCost. The term does
	// one piece-list pass plus per-file/per-passer work, so the honest
	// pessimistic value is EvalTermsCost(2) = 438.
	EGTerm float64
	// MidTerm is the per-eval-call cost of the MIDDLEGAME terms
	// (midgame.go), charged only on the calls where the phase gate fires
	// (Phase >= PhaseMin) — which in a middlegame search is nearly every
	// call, so this is a heavy and honest tax. Set via PlayerCfg.MidCost;
	// the pessimistic default convention is EvalTermsCost(passes).
	MidTerm float64
}

// DefaultCycleCosts is the fit against TestMicroABPhase's 69 searches (23
// positions spanning 3-32 root pieces x masks 0x1f/0x07/0x00), reduced to
// the [node, node*phase, make, eval, ttprobe] regressors so the costs
// transfer to the mirror's own counts without distortion. Applied
// to the asm operation counts it predicts each per-mask GRAND TOTAL within
// ~4.2% and every one of the 69 searches within 20.4%, with NO material
// bias: the actual/predicted ratio is 0.993 over the low-material positions
// and 1.019 over the rest (the old constant-per-node model: 0.71 and 1.02 —
// it over-charged an endgame node by ~40%). Applied to the mirror's own
// counts it reproduces the ~4-6% rook-term cycle fraction the asm shows
// (TestEvalTermTax) — the property that makes a cycle budget discount a
// feature's Elo by its real cost. Derived in TestCycleModelFit; see
// cycles.md. EvalTerm defaults to 0 (no experimental term); a screen sets
// it via PlayerCfg.EvalTermsCost.
var DefaultCycleCosts = CycleCosts{
	Node:      fittedNode,
	NodePhase: fittedNodePhase,
	Make:      fittedMake,
	Eval:      fittedEval,
	TTProbe:   fittedTTProbe,
	EvalTerm:  0,
	// Attacked / Generate / MovePerGen / MakeNull / TTStore are folded into
	// the fitted coefficients above (their asm cost is absorbed via
	// correlation with the node/phase/make counts) and default to 0. They
	// exist so a future recalibration can price them separately. See
	// cycles.md.
}

// Fitted coefficients (emulated 6502 cycles) for the reduced
// [node, node*phase, make, eval, ttprobe] model, from TestCycleModelFit
// (regressing the asm cycle total onto the asm operation counts over the 69
// microAB searches, non-negative, through the origin, minimizing RELATIVE
// error). These ops have the closest mirror/asm per-node frequency so the
// costs transfer to mirror counts with least distortion; generate/attacked
// are deliberately excluded because their mirror/asm per-node frequency
// diverges. fittedConst is the per-search intercept (reported/validated, not
// charged at runtime). Values are rounded to integers: the runtime charges
// them with integer arithmetic so Est stays exactly the cost table dotted
// with the counters. See cycles.md for the table, residuals, and limits.
const (
	// fittedNode is 0: with a material term in the model, the per-node cost
	// that does NOT scale with material is not separately identifiable from
	// the make/eval counts (which are themselves ~1 per node) and the fit
	// drives it to the non-negativity boundary. The split the fit chose is
	// corroborated by an INDEPENDENT measurement — the round-5 per-file
	// profile (TestProfileR5, docs/results.md 2026-07-27) — see cycles.md.
	fittedNode      = 0.0    // per node entry, material-independent part
	fittedNodePhase = 44.0   // per node entry, per unit of taper phase (0..24)
	fittedMake      = 1585.0 // per make()+unmake() pair
	fittedEval      = 888.0  // per eval() base call
	fittedTTProbe   = 1824.0 // per full-width node (TT probe + sprep/null/futility dispatch)
	fittedConst     = 0.0    // through-origin fit (zero ops = zero cycles)
)

// CycleAccount holds the live cycle estimate plus per-operation counters.
// The counters double as the calibration regressors and as diagnostics;
// they are exact deterministic functions of the search tree.
type CycleAccount struct {
	Est uint64 // running estimated 6502 cycles (the budget quantity)

	Nodes        uint64 // search() entries (== the asm "search" probe)
	NodesFull    uint64 // ... at ply < MaxDepth (TT-probing full-width nodes)
	NodesQS      uint64 // ... at ply >= MaxDepth, not in check (stand-pat QS)
	NodesEvasion uint64 // ... at ply >= MaxDepth, in check (full evasion node)

	// PhaseSum is Pos.Phase summed over every search() entry — the material
	// regressor. Est's node term is exactly Node*Nodes + NodePhase*PhaseSum,
	// which is what keeps Est reconstructible from the counters.
	PhaseSum uint64

	Makes    uint64 // real make() calls (== the asm "make" probe)
	Unmakes  uint64 // unmake() calls (== Makes; folded into Make cost)
	MakeNull uint64 // makenull() calls (separate asm routine)

	// Make count broken out by move class (all priced at Costs.Make today;
	// exposed for transparency and future per-class calibration).
	MakeQuiet, MakeCap, MakePromo, MakeCastle, MakeEP uint64

	Evals      uint64 // eval() calls (== the asm "eval" probe)
	EvalsExtra uint64 // ... of which computed an EvalTerms (extra) sum
	EGEvals    uint64 // ... of which computed an endgame-technique sum (gate fired)
	MidEvals   uint64 // ... of which computed a middlegame-term sum (gate fired)

	Attacked    uint64 // attacked() scans charged at Costs.Attacked
	AttackedAll uint64 // ALL attacked() scans incl. in-make gives-check

	TTProbes uint64 // ttprobe() calls (== the asm "ttprobe" probe)
	TTStores uint64 // ttstore() calls

	Generates uint64 // generate() calls (== the asm "generate" probe)
	MovesGen  uint64 // pseudo-legal moves emitted across all generate() calls

	CounterProbes uint64 // countermove table probes (per full-width CM node)
	CounterStores uint64 // countermove table stores (quiet beta cutoffs)

	// SEE classification counters (see.go). SEEGates counts captures that
	// reached the gate compare; SEECalls the classifications actually run
	// (gate passed), SEECallsQS the subset at quiescence nodes; SEELosing
	// the losing verdicts; SEERescans the move-list items rescanned by
	// deferred passes.
	SEEGates, SEECalls, SEECallsQS, SEELosing, SEERescans uint64

	// Cheap quiet-history counters (qhist.go).
	QHistProbes, QHistUpdates uint64
	// IIR counters: tests run / reductions applied (iir.go).
	IIRTests, IIRHits uint64
	// Razoring counters: margin tests / QS drops taken (razor.go).
	RazorTests, RazorDrops uint64
	// TTReplSkips counts stores skipped by the depth-preferred policy.
	TTReplSkips uint64
}

// EvalTermsCost returns a conservative estimated per-eval-call cost, in
// emulated 6502 cycles, for an experimental EvalTerms set that has NO asm
// implementation yet. It is deliberately pessimistic: the asm's ported
// FT_ROOKX subset (RookTermsAsm) measured 219 cycles/call doing rook
// open/semi-open/doubled + blockade scans; a term set doing comparable
// per-slot work should be taxed at least that much, and a richer set more.
// A screen that has a real asm cost should use the measured number
// instead. The rule of thumb baked in: ~219 cycles per "piece-list pass"
// the term needs, floored at 219 so nothing screens cheaper than the
// cheapest thing we have actually measured.
func EvalTermsCost(pieceListPasses int) float64 {
	const perPass = RookTermCost
	if pieceListPasses < 1 {
		pieceListPasses = 1
	}
	return perPass * float64(pieceListPasses)
}

// RookTermCost is the measured asm cost of the FT_ROOKX extraterm
// (RookTermsAsm), 219 cycles per eval call. Use it (via
// PlayerCfg.EvalTermsCost) to reproduce the real cycle tax of the rook set.
const RookTermCost = 219.0

// cycleActive reports whether cycle accounting runs for the current
// search. Kept off the hot NodeBudget path so that mode is untouched.
func (e *Engine) cycleActive() bool { return e.cyc }

// resetCycles zeroes the account and computes whether accounting is active
// for this search. Called at the top of every search entry point.
func (e *Engine) resetCycles() {
	e.Cyc = CycleAccount{}
	e.cyc = e.CycleBudget != 0 || e.CycleTrack
	if e.Costs == (CycleCosts{}) {
		e.Costs = DefaultCycleCosts
	}
}

// chargeNode books one search() entry at the given node type. The node's
// cost is Node + NodePhase*phase: the material-dependent half is what keeps
// a low-material node from being charged midgame prices (see CycleCosts).
func (e *Engine) chargeNode(qs, evasion bool) {
	c := &e.Cyc
	c.Nodes++
	switch {
	case !qs:
		c.NodesFull++
	case evasion:
		c.NodesEvasion++
	default:
		c.NodesQS++
	}
	ph := e.Pos.Phase
	if ph < 0 { // cannot happen; a negative would corrupt the uint64 sum
		ph = 0
	}
	c.PhaseSum += uint64(ph)
	c.Est += uint64(e.Costs.Node) + uint64(e.Costs.NodePhase)*uint64(ph)
}

// chargeMake books one real make()+unmake() pair, classified by move.
func (e *Engine) chargeMake(m Move, victim byte) {
	c := &e.Cyc
	c.Makes++
	switch {
	case m.Flags&FlCastle != 0:
		c.MakeCastle++
	case m.Flags&FlEP != 0:
		c.MakeEP++
	case m.Flags&FlPromo != 0:
		c.MakePromo++
	case victim != 0:
		c.MakeCap++
	default:
		c.MakeQuiet++
	}
	c.Est += uint64(e.Costs.Make)
}

func (e *Engine) chargeMakeNull() {
	e.Cyc.MakeNull++
	e.Cyc.Est += uint64(e.Costs.MakeNull)
}

// chargeEval books one eval() call. extra reports whether an EvalTerms sum
// was computed (adds the configurable feature tax).
func (e *Engine) chargeEval(extra bool) {
	c := &e.Cyc
	c.Evals++
	c.Est += uint64(e.Costs.Eval)
	if extra {
		c.EvalsExtra++
		c.Est += uint64(e.Costs.EvalTerm)
	}
}

// chargeAttacked books an attacked() scan. countedAgainstAsm is false for
// the in-make gives-check scan, which the asm does via difference tables
// inside make() (its cost is in Costs.Make): that scan bumps AttackedAll
// for diagnostics but is not priced or counted as the asm "attacked" op.
func (e *Engine) chargeAttacked(countedAgainstAsm bool) {
	c := &e.Cyc
	c.AttackedAll++
	if countedAgainstAsm {
		c.Attacked++
		c.Est += uint64(e.Costs.Attacked)
	}
}

func (e *Engine) chargeTTProbe() {
	e.Cyc.TTProbes++
	e.Cyc.Est += uint64(e.Costs.TTProbe)
}

func (e *Engine) chargeTTStore() {
	e.Cyc.TTStores++
	e.Cyc.Est += uint64(e.Costs.TTStore)
}

// chargeSEEGate books one victim-vs-attacker gate compare in a capture
// pass; passed reports whether it triggered a classification call, and qs
// whether at a quiescence node.
func (e *Engine) chargeSEEGate(passed, qs bool) {
	c := &e.Cyc
	c.SEEGates++
	c.Est += uint64(e.Costs.SEEGate)
	if passed {
		c.SEECalls++
		if qs {
			c.SEECallsQS++
		}
		c.Est += uint64(e.Costs.SEE)
	}
}

// chargeSEERescan books n move-list items rescanned by a deferred
// losing-capture pass.
func (e *Engine) chargeSEERescan(n int) {
	c := &e.Cyc
	c.SEERescans += uint64(n)
	c.Est += uint64(n) * uint64(e.Costs.SEERescan)
}

func (e *Engine) chargeCounterProbe() {
	e.Cyc.CounterProbes++
	e.Cyc.Est += uint64(e.Costs.Countermove)
}

func (e *Engine) chargeCounterStore() {
	e.Cyc.CounterStores++
	e.Cyc.Est += uint64(e.Costs.Countermove)
}

func (e *Engine) chargeGenerate(nMoves int) {
	c := &e.Cyc
	c.Generates++
	c.MovesGen += uint64(nMoves)
	c.Est += uint64(e.Costs.Generate) + uint64(nMoves)*uint64(e.Costs.MovePerGen)
}
