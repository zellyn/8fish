package mirror

// Cheap quiet-history heuristic for the FIVE-PASS moveLoop (the asm-matched
// ordering path), screened for the modern-technique adaptation round.
//
// This is NOT the butterfly [from][to] history behind FtHistory (16 KB,
// sort-based, mirror-only, rejected for the asm). It is the 6502-affordable
// family the SEE-feasibility entry (docs/results.md 2026-07-21) left open:
// byte counters indexed by a SMALL key, used without any O(n^2) selection
// sort — only pass-partitioning, LMR gating, and LMP exemption, each of
// which is a single indexed load + compare per candidate move on the 6502.
//
// Table forms (all uint8 counters, 0x88 to-square indexing):
//
//	Form 1: [side][to]          —   256 B (fits zero-page-adjacent or LC bank 1)
//	Form 2: [piece-type][to]    —   768 B (shared across sides; LC bank 1)
//	Form 3: [side][ptype][to]   — 1,536 B (would NOT fit LC bank 1's 1,048 B —
//	                                       screened only to price the signal)
//
// Update: on a QUIET beta cutoff in the main search, saturating add of
// rem*rem (clamped to 255) to the cutter's slot — the asm form is CLC/ADC/
// BCS-saturate on one indexed byte. Aging: every byte is halved (LSR) once
// per root move in newMove, ~8 cyc/byte on the 6502, charged via
// Costs.QHistAge.
//
// Uses (independently toggleable; all are cheap on the 6502 because each is
// one probe + compare per move CONSIDERED, never a sort):
//
//   - PartThresh: a dedicated moveLoop pass (pass 8) between the killers and
//     the final quiet pass searches quiets whose counter >= PartThresh; the
//     final pass skips them. On the asm this is one more tier-filtered
//     rescan of the move stack — the same machinery as the killer pass.
//   - LMRHigh: a late quiet whose counter >= LMRHigh is not LMR-reduced
//     (mode capped at the zero-window scout).
//   - LMRLowThresh: a reduced late quiet whose counter < LMRLowThresh is
//     reduced one ply MORE (the history-gated extra reduction).
//   - LMPThresh: with LMP armed, a quiet past the movecount threshold is
//     skipped only if its counter < LMPThresh — high-history late quiets
//     survive the prune. This is the "ordering enabler" form: LMP's -85 at
//     0x1f was driven by good quiets hiding late in generation order.
//
// The zero value (Form == 0) is a byte-identical no-op: no table, no
// probes, no charges — the parity gates run with it off.
type QHistParams struct {
	Form         int // 0 = off, 1 = [side][to], 2 = [ptype][to], 3 = [side][ptype][to]
	PartThresh   int // >0: partition pass threshold
	LMRHigh      int // >0: counter >= this exempts a quiet from LMR reduction
	LMRLowThresh int // >0: counter < this adds one reduction ply
	LMPThresh    int // >0: LMP skips only quiets whose counter < this
}

func (q *QHistParams) on() bool { return q.Form != 0 }

// bytes reports the table's 6502 footprint for the selected form (the
// port-cost number and the aging-charge multiplier).
func (q *QHistParams) bytes() int {
	switch q.Form {
	case 1:
		return 2 * 128
	case 2:
		return 6 * 128
	case 3:
		return 2 * 6 * 128
	}
	return 0
}

// qhSlotAt returns a pointer to the counter for a quiet move to `to` by a
// `ptype` mover of `side` (raw Side byte, White 0 / Black 8). Index shapes
// match the asm forms: side = Side>>3, ptype 1..6 -> plane 0..5.
func (e *Engine) qhSlotAt(side, ptype, to byte) *uint8 {
	switch e.QHist.Form {
	case 1:
		return &e.qhist[side>>3][0][to]
	case 2:
		return &e.qhist[0][ptype-1][to]
	default: // 3
		return &e.qhist[side>>3][ptype-1][to]
	}
}

// qhSlot is qhSlotAt for a move at the CURRENT node, BEFORE make (or after
// unmake): Board[m.From] is the mover and Side the side to move. The LMR
// gate, which runs after make, must instead use qhValAt with the mover
// captured pre-make.
func (e *Engine) qhSlot(m Move) *uint8 {
	p := &e.Pos
	return e.qhSlotAt(p.Side, p.Board[m.From]&TypeMask, m.To)
}

// qhVal probes the counter for m (pre-make state) and charges the probe's
// 6502 cost (one indexed load + compare, Costs.QHistProbe).
func (e *Engine) qhVal(m Move) int {
	if e.cyc {
		e.Cyc.QHistProbes++
		e.Cyc.Est += uint64(e.Costs.QHistProbe)
	}
	return int(*e.qhSlot(m))
}

// qhValAt probes the counter using an explicitly captured (side, ptype)
// — the post-make-safe probe — and charges the probe cost.
func (e *Engine) qhValAt(side, ptype, to byte) int {
	if e.cyc {
		e.Cyc.QHistProbes++
		e.Cyc.Est += uint64(e.Costs.QHistProbe)
	}
	return int(*e.qhSlotAt(side, ptype, to))
}

// qhBump rewards the quiet cutter: saturating add of rem*rem (clamped to
// the byte ceiling), charged at Costs.QHistUpdate.
func (e *Engine) qhBump(m Move, rem int) {
	if rem > 15 {
		rem = 15
	}
	bonus := rem * rem
	if bonus > 255 {
		bonus = 255
	}
	s := e.qhSlot(m)
	nv := int(*s) + bonus
	if nv > 255 {
		nv = 255
	}
	*s = uint8(nv)
	if e.cyc {
		e.Cyc.QHistUpdates++
		e.Cyc.Est += uint64(e.Costs.QHistUpdate)
	}
}

// qhNewMove ages the table (halve every counter — the asm's LSR sweep) and
// charges it at Costs.QHistAge per table byte. Allocates the table on first
// use so an off engine never touches it.
func (e *Engine) qhNewMove() {
	if e.qhist == nil {
		e.qhist = &[2][6][128]uint8{}
		return
	}
	for s := range e.qhist {
		for t := range e.qhist[s] {
			for i := range e.qhist[s][t] {
				e.qhist[s][t][i] >>= 1
			}
		}
	}
	if e.cyc {
		e.Cyc.Est += uint64(e.Costs.QHistAge * float64(e.QHist.bytes()))
	}
}

// qhPartActive reports whether the partition pass runs at this node
// (feature on, partition threshold set, non-qs handled by the caller).
func (e *Engine) qhPartActive() bool {
	return e.QHist.on() && e.QHist.PartThresh > 0
}

// qhPart8Match reports whether pass 8 searched m at this ply. Mirror-side
// this is a linear scan of the recorded list; the asm skips via the
// rewritten tier bits at no per-move cost, which is why no probe is
// charged here.
func (e *Engine) qhPart8Match(ply int, m Move) bool {
	for _, r := range e.qhPart8[ply] {
		if r.From == m.From && r.To == m.To {
			return true
		}
	}
	return false
}
