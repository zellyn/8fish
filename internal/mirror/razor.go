package mirror

// Razoring, screened for the modern-technique adaptation round: at a
// shallow full-width node (remaining 1..2) whose static eval sits far BELOW
// alpha, drop straight into quiescence instead of searching the last
// ply(ies) full-width. It is the alpha-side twin of the adopted RFP (which
// returns when eval - margin >= beta): RFP prunes nodes that are already
// winning; razoring prunes nodes so far behind that only a capture sequence
// could matter, which is exactly what QS searches.
//
// Placement: inside the existing RFP/futility block, so it inherits the
// SAME signed mate-zone guard, the not-in-check gate, and the static eval
// the RFP compare just paid for — on the 6502 the whole feature is one CMP
// against a second margin plus a branch into the existing QS entry,
// charged at Costs.Razor per test. The engine already owns every
// mechanism used (eval-at-shallow-nodes, QS entry); no new memory.
//
// The QS drop reuses the engine's own quiescence entry (qsEntry) with the
// horizon pulled to the current ply for the subtree, exactly as if the
// node had been reached at the horizon. No TT store is made on the razored
// path (like an RFP return).
//
// The zero value (both margins 0) is a byte-identical no-op.
type RazorParams struct {
	// M1/M2 are the margins (centipawns) at remaining 1 / remaining 2.
	// 0 disables razoring at that depth.
	M1, M2 int
}

func (r *RazorParams) on() bool { return r.M1 > 0 || r.M2 > 0 }

func (r *RazorParams) margin(rem int) int {
	switch rem {
	case 1:
		return r.M1
	case 2:
		return r.M2
	}
	return 0
}

// razorDrop searches the current node as a quiescence node: the horizon is
// pulled to the current ply for the whole subtree and restored after.
// Caller guarantees: full-width node, not in check, mate-zone guard passed.
func (e *Engine) razorDrop() int {
	save := e.MaxDepth
	e.MaxDepth = e.Pos.Ply
	score := e.qsEntry()
	e.MaxDepth = save
	return score
}

// Adaptive null-move reduction, screened alongside razoring: the shipped
// null move reduces the horizon by a fixed 2; with NullR enabled the
// reduction grows to DeepR when remaining depth >= MinRem (deep nodes
// tolerate — and profit from — a cheaper null verification). 6502 shape:
// one CMP/branch choosing between two constants in the existing null-move
// prep, ~4 instructions, no memory, cost within measurement noise (no
// charge hook; stated in the screen record).
//
// The zero value (DeepR == 0) keeps the shipped fixed R=2.
type NullRParams struct {
	DeepR  int // reduction used at deep nodes (3 = classic adaptive R)
	MinRem int // remaining-depth threshold for DeepR
}

// nullReduction returns the horizon decrement for the null-move search at
// the current node (2 unless NullR fires).
func (e *Engine) nullReduction(ply int) int {
	if e.NullR.DeepR > 2 && e.MaxDepth-ply >= e.NullR.MinRem {
		return e.NullR.DeepR
	}
	return 2
}
