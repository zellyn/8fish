package mirror

// This file implements the IMPROVING HEURISTIC for the five-pass moveLoop
// (the asm-matched search path) plus the shared search() pruning block.
//
// "improving" = the side to move's static eval at THIS ply is better than
// it was at the same side's previous turn (ply-2, same side to move). When
// NOT improving, the position is trending worse for us, so it is safe to
// prune/reduce harder: tighten the RFP/futility margins and add one extra
// LMR reduction for late quiets.
//
// TWO SIGNAL DESIGNS (the whole reason this is screened twice):
//
//   1. FREE-SIGNAL (Mode == improvingFree). "improving" is computed only
//      from static evals that ALREADY exist in the asm's tree — the QS
//      stand-pat eval, the null-move eval, and the RFP/futility eval. When
//      either ply's eval is missing (the common case at deeper full-width
//      nodes, which compute no eval today) it FALLS BACK to "assume
//      improving" — the permissive default that prunes/reduces nothing
//      extra. Zero added cost; a weak, sparse signal.
//
//   2. FULL-SIGNAL (Mode == improvingFull). A static eval is computed at
//      EVERY full-width node so the ply-2 comparison is always available.
//      This adds an eval() call at every full-width node that would not
//      otherwise compute one; in a CycleBudget screen each such ADDED eval
//      is charged its real ~872-cycle cost automatically (it flows through
//      eval() -> chargeEval), while a node that already computed an eval
//      (null/RFP) reuses it and is not charged twice.
//
// The evals feeding the signal are recorded into evalStack[ply] by eval()
// itself (only when the heuristic is active), so the OFF path (Mode == 0)
// is a byte-identical no-op: no recording, no reset, no margin swap, no
// extra reduction. That is what the parity gates rely on.

// Improving Mode values.
const (
	improvingOff  = 0 // heuristic disabled: byte-identical no-op
	improvingFree = 1 // free-signal: use only evals that already exist
	improvingFull = 2 // full-signal: force an eval at every full-width node
)

// ImprovingParams configures the improving heuristic. The zero value
// (Mode == 0) disables it entirely.
type ImprovingParams struct {
	// Mode selects the signal design: 0 = off, 1 = free-signal,
	// 2 = full-signal (see the constants above).
	Mode int

	// RFP applies the improving-conditional RFP/futility margins: when NOT
	// improving, the reverse-futility margin RFPNI[remaining] and the leaf-
	// futility margin FutNI replace the normal Fut.RFP[remaining] / Fut.Fut,
	// pruning harder. A zero entry in RFPNI (or FutNI == 0) leaves that
	// margin at its normal value.
	RFP   bool
	RFPNI [8]int
	FutNI int

	// LMR adds LMRExtra plies of reduction to late quiets when NOT improving
	// (on top of the normal LMR reduction). LMRExtra == 0 is treated as 1
	// when LMR is set.
	LMR      bool
	LMRExtra int
}

// on reports whether the improving heuristic is enabled.
func (p ImprovingParams) on() bool { return p.Mode != improvingOff }

// lmrExtra returns the extra reduction plies applied to a late quiet at a
// not-improving node (0 when the LMR application is off).
func (p ImprovingParams) lmrExtra() int {
	if !p.on() || !p.LMR {
		return 0
	}
	if p.LMRExtra > 0 {
		return p.LMRExtra
	}
	return 1
}

// improving reports whether the side to move is improving at this ply: its
// recorded static eval now is strictly greater than its recorded static
// eval two plies ago (same side to move). It returns the PERMISSIVE default
// (true = "assume improving", prune/reduce nothing extra) whenever the
// comparison cannot be made: at the first two plies, or when either ply's
// eval was never recorded (the free-signal fallback). evalStack/evalValid
// are only ever populated when the heuristic is active.
func (e *Engine) improving(ply int) bool {
	if ply < 2 {
		return true
	}
	if !e.evalValid[ply] || !e.evalValid[ply-2] {
		return true
	}
	return e.evalStack[ply] > e.evalStack[ply-2]
}
