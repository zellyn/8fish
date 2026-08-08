package mirror

// Internal iterative reduction (IIR), screened for the modern-technique
// adaptation round: at a full-width node with NO TT move and enough
// remaining depth, search the node one ply shallower. The rationale is
// budget-shaped: a node the TT has never seen with real remaining depth is
// probably off the principal variation; spending one ply less there and
// letting a later, TT-seeded re-visit deepen it is close to free.
//
// 6502 shape: after the TT probe, TTFROM is already in a register — the
// whole feature is "BNE skip / remaining CMP / window CMP / DEC horizon"
// plus the restoring INC, ~4 instructions and no memory. Charged at
// Costs.IIR per full-width node while enabled (pessimistic: the test runs
// whether or not it fires).
//
// The zero value (MinRem == 0) is a byte-identical no-op.
type IIRParams struct {
	// MinRem enables IIR at nodes with remaining depth >= MinRem. 0 = off.
	MinRem int
	// AnyWindow applies IIR at every qualifying node; false restricts it to
	// zero-window (expected-cut) nodes, the classical gate.
	AnyWindow bool
}

func (i *IIRParams) on() bool { return i.MinRem > 0 }

// iirApplies reports whether this node takes the reduction (TT probe
// already done; ttFrom records the miss) and charges the per-node test.
func (e *Engine) iirApplies(ply int) bool {
	if !e.IIR.on() || ply == 0 {
		return false
	}
	if e.cyc {
		e.Cyc.IIRTests++
		e.Cyc.Est += uint64(e.Costs.IIR)
	}
	if e.ttFrom[ply] != NoSq || e.MaxDepth-ply < e.IIR.MinRem {
		return false
	}
	if !e.IIR.AnyWindow && e.beta[ply]-e.alpha[ply] > 1 {
		return false
	}
	if e.cyc {
		e.Cyc.IIRHits++
	}
	return true
}
