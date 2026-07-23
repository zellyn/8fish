package mirror

// Endgame "mop-up" eval (task: convert basic won endgames). The thin
// PSQT eval has no term that drives the enemy king to the edge/corner
// nor brings our king up to help mate, so with only a queen or rook the
// engine can shuffle without progress and draw by repetition / 50-move.
// This term adds exactly that knowledge, and ONLY in low-phase endgames
// where one side has a clear material edge, so normal play is untouched.
//
// Design (white POV, added in eval() like PStruct/extraEval, before the
// side-to-move negation):
//   (a) drive the LOSING king toward a corner: Edge * CMD[losingKing],
//       where CMD is the centre-manhattan-distance table (0 in the
//       middle, 6 in the corners).
//   (b) bring the WINNING king CLOSE to the losing king:
//       Close * (14 - manhattan(kings)).
// Both are worth at most ~1 minor piece combined, so they break the
// shuffling tie / guide the mate without ever overriding material.
//
// 6502 portability: CMD is a 64-byte table indexed by the (packed) king
// square; the king-distance term is |dr|+|df| with a 14-x subtract; the
// material edge and phase are already maintained incrementally
// (Pos.Phase; a material sum is one pass over the 32-slot piece list, or
// an incrementally-kept byte in the asm). Everything is integer.

// MopupParams configures the endgame mop-up term. The zero value is OFF:
// eval() then never calls mopupEval and is byte-identical to the asm's
// current eval, so every existing test and parity gate is unaffected.
type MopupParams struct {
	Enable bool
	// Edge weights CMD[losingKing] (corner distance): larger = harder
	// drive to the corner.
	Edge int
	// Close weights (14 - manhattan(kings)): larger = winning king hugs
	// the losing king harder.
	Close int
	// MatThreshold is the minimum material edge (centipawns, king
	// excluded) for the winning side before the term fires. Below it the
	// term is silent (a lone minor cannot mate; equal material is not a
	// conversion).
	MatThreshold int
	// PhaseMax caps the taper phase at which the term fires (low phase =
	// endgame). Above it (any real middlegame) the term is silent.
	PhaseMax int
}

// on reports whether the mop-up term is active.
func (m *MopupParams) on() bool { return m.Enable }

// DefaultMopup is the adopted endgame mop-up set: fires at phase <= 6
// (queen-or-less material left) with at least a rook's edge, driving the
// losing king to a corner (Edge*CMD, max 6*10=60cp) and pulling the
// winning king in (Close*(14-md), max 4*14=56cp). Combined max ~116cp <
// a minor piece, so it never trades material for mop-up.
var DefaultMopup = MopupParams{
	Enable:       true,
	Edge:         10,
	Close:        4,
	MatThreshold: 450,
	PhaseMax:     6,
}

// mopMatVal is the material value per piece type (king = 0), matching
// vicVal, used for the winning-side gate.
var mopMatVal = [8]int{0, 100, 320, 330, 500, 975, 0, 0}

// mopupCMD is the centre-manhattan-distance table indexed by 0x88
// square: 0 in the four centre squares, rising to 6 in the corners. The
// asm port is a 64-byte table indexed by the packed king square.
var mopupCMD [128]int

func init() {
	for sq := range 128 {
		if sq&0x88 != 0 {
			continue
		}
		file := sq & 7
		rank := sq >> 4
		var fd, rd int
		if file >= 4 {
			fd = file - 4
		} else {
			fd = 3 - file
		}
		if rank >= 4 {
			rd = rank - 4
		} else {
			rd = 3 - rank
		}
		mopupCMD[sq] = fd + rd
	}
}

// manhattan returns |dr|+|df| between two 0x88 squares (max 14).
func manhattan(a, b byte) int {
	dr := int(a>>4) - int(b>>4)
	if dr < 0 {
		dr = -dr
	}
	df := int(a&7) - int(b&7)
	if df < 0 {
		df = -df
	}
	return dr + df
}

// mopupEval returns the white-POV mop-up bonus (positive helps white),
// or 0 when the phase/material gate is not met. Only ever called when
// e.Mopup.on() (the OFF path is byte-identical to the asm eval).
func (e *Engine) mopupEval() int {
	p := &e.Pos
	if p.Phase > e.Mopup.PhaseMax {
		return 0
	}
	matW, matB := 0, 0
	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		v := mopMatVal[p.Board[sq]&TypeMask]
		if slot < 16 {
			matW += v
		} else {
			matB += v
		}
	}
	diff := matW - matB
	wk := p.PieceSq[0]
	bk := p.PieceSq[16]
	// A pseudo-legal search node can have captured a king (the asm's
	// king-capture refutation of an illegal move); its square is NoSq.
	// The score is irrelevant there, so skip the table lookup.
	if wk == NoSq || bk == NoSq {
		return 0
	}
	switch {
	case diff >= e.Mopup.MatThreshold: // white winning: mop up black king
		return e.Mopup.Edge*mopupCMD[bk] + e.Mopup.Close*(14-manhattan(wk, bk))
	case -diff >= e.Mopup.MatThreshold: // black winning: mop up white king
		return -(e.Mopup.Edge*mopupCMD[wk] + e.Mopup.Close*(14-manhattan(wk, bk)))
	}
	return 0
}
