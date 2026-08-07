package mirror

// This file implements the COUNTERMOVE HEURISTIC for the five-pass
// moveLoop (the asm-matched ordering path — TT move, two-tier MVV
// captures, killers, quiets in generation order; NO SEE, NO history).
//
// On a quiet beta cutoff the cutting move is stored as the "counter" to
// the move that was made at the PREVIOUS ply. When a later node is reached
// by that same previous move, the stored counter is promoted ahead of the
// other quiets (its own ordering pass, adjacent to the killer pass). Unlike
// the butterfly history heuristic (rejected for the asm: table too coarse
// and costly on the 6502), the countermove table needs NO aging and only a
// tiny amount of state, which is why it is the cheap ordering-upgrade
// candidate for the port.
//
// PORTING NOTE (6502 table shapes, for Fable's queue):
//   - Indexing 1: [prev-move to-square] -> (from,to). 128 slots * 2 bytes =
//     256 B. The cheapest shape; all pieces landing on a square share one
//     counter slot.
//   - Indexing 2: [prev-moved piece-type][prev to-square] -> (from,to).
//     8 * 128 * 2 bytes = 2 KB. Distinguishes e.g. a knight vs a rook
//     arriving on the same square. Nothing bigger is affordable.
//   Update is a 2-byte store on a quiet beta cutoff; probe is one indexed
//   load plus a 2-byte compare in the existing quiet pass.

// CountermoveParams configures the countermove heuristic in moveLoop. The
// zero value (Indexing == 0) disables it entirely — a byte-identical no-op
// versus the pre-countermove five-pass search, which is what the parity
// gates rely on.
type CountermoveParams struct {
	// Indexing selects the table shape / key:
	//   0 = off
	//   1 = [prev-move to-square]            (128 slots, 256 B)
	//   2 = [prev piece-type][prev to-square] (8*128 slots, 2 KB)
	Indexing int
	// BeforeKillers promotes the countermove BEFORE the killer pass instead
	// of after it. Default (false) = after killers, so the killers, which
	// already refuted a sibling at THIS node, keep priority over the
	// countermove, which only refuted the previous ply's move.
	BeforeKillers bool
	// Persist keeps the table across root moves (like the TT). Default
	// (false) = cleared each root move, exactly as the killer table is, so
	// stale counters from earlier game positions never leak in.
	Persist bool
}

// on reports whether the countermove heuristic is enabled.
func (c CountermoveParams) on() bool { return c.Indexing != 0 }

// cmSlot returns the [pieceType][toSquare] table indices for the previous
// move's (piece, to) under the configured indexing variant. Variant 1 keys
// on the to-square alone (piece slot 0); variant 2 keys on piece type too.
func (e *Engine) cmSlot(piece, to byte) (int, int) {
	if e.CM.Indexing == 2 {
		return int(piece & TypeMask), int(to)
	}
	return 0, int(to)
}

// counterMove returns the stored countermove for the move made at ply-1,
// or NoMove at the root, after a null move, or when the slot is empty.
func (e *Engine) counterMove(ply int) Move {
	if ply == 0 {
		return NoMove
	}
	prev := &e.undo[ply-1]
	if prev.from == NoSq { // previous ply was a null move: no counter
		return NoMove
	}
	pt, to := e.cmSlot(prev.piece, prev.to)
	return e.counter[pt][to]
}

// counterMatch reports whether m is the stored countermove at this ply. An
// empty slot is the zero Move{0,0,0}; no real move has From==To==0, so it
// can never spuriously match. The NoSq guard rejects the root/null sentinel.
func (e *Engine) counterMatch(ply int, m Move) bool {
	c := e.counterMove(ply)
	return c.From != NoSq && c.From == m.From && c.To == m.To
}

// storeCounter records quiet move m as the refutation of the move made at
// the previous ply (called on a quiet beta cutoff, ply != 0).
func (e *Engine) storeCounter(ply int, m Move) {
	prev := &e.undo[ply-1]
	if prev.from == NoSq {
		return // previous ply was a null move
	}
	if e.cyc {
		e.chargeCounterStore()
	}
	pt, to := e.cmSlot(prev.piece, prev.to)
	e.counter[pt][to] = Move{m.From, m.To, 0}
}

// firstQuietPass returns the first quiet moveLoop pass for a non-qs,
// non-futile node, honoring the countermove slot-vs-killers order. With CM
// off it reproduces the original choice exactly (killers -> pass 3, else
// pass 4), keeping the disabled path byte-identical.
func (e *Engine) firstQuietPass() int {
	if e.CM.on() && e.CM.BeforeKillers {
		return 6 // countermove pass before the killers
	}
	if e.Features&FtKiller != 0 {
		return 3 // killer pass
	}
	if e.CM.on() {
		return 6 // after-killers, but no killer pass: countermove first
	}
	return e.finalQuietPass(e.qhPartActive())
}

// finalQuietPass returns the pass that follows the killer/countermove
// passes: the history-partition pass 8 when armed (qhist.go), else the
// final quiet pass 4. With QHist off it is the constant 4, keeping the
// disabled path byte-identical.
func (e *Engine) finalQuietPass(qhPart bool) int {
	if qhPart {
		return 8
	}
	return 4
}
