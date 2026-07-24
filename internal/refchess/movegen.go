package refchess

// Move generation strategy: generate pseudo-legal moves (piece movement
// rules only, plus the extra board-state checks castling needs), then
// filter by actually playing each one on a scratch copy and testing
// whether the mover's own king ends up attacked. That single generic
// filter is what correctly handles pins, discovered checks, and the
// classic "horizontal pin through an en passant capture" edge case
// (perft position 3: a rook/king on the same rank as two pawns that
// would both vanish in an en passant capture) — no special-casing
// needed, because the filter plays the *actual* resulting board.

var knightOffsets = [8][2]int{{1, 2}, {2, 1}, {2, -1}, {1, -2}, {-1, -2}, {-2, -1}, {-2, 1}, {-1, 2}}
var kingOffsets = [8][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
var bishopDirs = [4][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
var rookDirs = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

func onBoard(file, rank int) bool { return file >= 0 && file < 8 && rank >= 0 && rank < 8 }

// attacked reports whether square sq is attacked by any piece of side
// bySide, given the current board (ignoring whose turn it actually is —
// callers use this for check/castling-safety tests on hypothetical
// positions too).
func (p *Position) attacked(sq int, bySide byte) bool {
	file, rank := sq%8, sq/8

	// Pawns: a bySide pawn attacks diagonally "forward" from its own
	// point of view, so the attacker sits one rank behind sq (from
	// bySide's perspective).
	pawnRank := rank - 1
	if bySide == black {
		pawnRank = rank + 1
	}
	for _, df := range [2]int{-1, 1} {
		af := file + df
		if onBoard(af, pawnRank) {
			pc := p.board[pawnRank*8+af]
			if pc != 0 && pieceColor(pc) == bySide && pieceType(pc) == pawn {
				return true
			}
		}
	}

	for _, o := range knightOffsets {
		nf, nr := file+o[0], rank+o[1]
		if onBoard(nf, nr) {
			pc := p.board[nr*8+nf]
			if pc != 0 && pieceColor(pc) == bySide && pieceType(pc) == knight {
				return true
			}
		}
	}

	for _, o := range kingOffsets {
		nf, nr := file+o[0], rank+o[1]
		if onBoard(nf, nr) {
			pc := p.board[nr*8+nf]
			if pc != 0 && pieceColor(pc) == bySide && pieceType(pc) == king {
				return true
			}
		}
	}

	for _, d := range bishopDirs {
		nf, nr := file+d[0], rank+d[1]
		for onBoard(nf, nr) {
			pc := p.board[nr*8+nf]
			if pc != 0 {
				if pieceColor(pc) == bySide && (pieceType(pc) == bishop || pieceType(pc) == queen) {
					return true
				}
				break
			}
			nf, nr = nf+d[0], nr+d[1]
		}
	}

	for _, d := range rookDirs {
		nf, nr := file+d[0], rank+d[1]
		for onBoard(nf, nr) {
			pc := p.board[nr*8+nf]
			if pc != 0 {
				if pieceColor(pc) == bySide && (pieceType(pc) == rook || pieceType(pc) == queen) {
					return true
				}
				break
			}
			nf, nr = nf+d[0], nr+d[1]
		}
	}

	return false
}

// pseudoLegalMoves generates all moves following piece-movement rules.
// Castling additionally checks "not currently in check" and "doesn't
// pass through or land on an attacked square" here (those aren't things
// the generic king-safety filter in LegalMoves can catch, since it only
// examines the final position). Everything else — including whether the
// move leaves the mover's own king in check — is left to that filter.
func (p *Position) pseudoLegalMoves() []Move {
	moves := make([]Move, 0, 48)
	us := p.side
	them := 1 - us
	for sq := 0; sq < 64; sq++ {
		pc := p.board[sq]
		if pc == 0 || pieceColor(pc) != us {
			continue
		}
		file, rank := sq%8, sq/8
		switch pieceType(pc) {
		case pawn:
			moves = p.genPawnMoves(sq, file, rank, us, moves)
		case knight:
			moves = p.genOffsetMoves(sq, file, rank, us, knightOffsets[:], moves)
		case king:
			moves = p.genOffsetMoves(sq, file, rank, us, kingOffsets[:], moves)
			moves = p.genCastling(us, them, moves)
		case bishop:
			moves = p.genSlideMoves(sq, file, rank, us, bishopDirs[:], moves)
		case rook:
			moves = p.genSlideMoves(sq, file, rank, us, rookDirs[:], moves)
		case queen:
			moves = p.genSlideMoves(sq, file, rank, us, bishopDirs[:], moves)
			moves = p.genSlideMoves(sq, file, rank, us, rookDirs[:], moves)
		}
	}
	return moves
}

func (p *Position) genOffsetMoves(sq, file, rank int, us byte, offsets [][2]int, moves []Move) []Move {
	for _, o := range offsets {
		nf, nr := file+o[0], rank+o[1]
		if !onBoard(nf, nr) {
			continue
		}
		to := nr*8 + nf
		pc := p.board[to]
		if pc == 0 || pieceColor(pc) != us {
			moves = append(moves, Move{From: byte(sq), To: byte(to)})
		}
	}
	return moves
}

func (p *Position) genSlideMoves(sq, file, rank int, us byte, dirs [][2]int, moves []Move) []Move {
	for _, d := range dirs {
		nf, nr := file+d[0], rank+d[1]
		for onBoard(nf, nr) {
			to := nr*8 + nf
			pc := p.board[to]
			if pc == 0 {
				moves = append(moves, Move{From: byte(sq), To: byte(to)})
			} else {
				if pieceColor(pc) != us {
					moves = append(moves, Move{From: byte(sq), To: byte(to)})
				}
				break
			}
			nf, nr = nf+d[0], nr+d[1]
		}
	}
	return moves
}

func appendPawnMove(moves []Move, from, to byte, promo bool) []Move {
	if promo {
		for _, pr := range [4]byte{'q', 'r', 'b', 'n'} {
			moves = append(moves, Move{From: from, To: to, Promo: pr})
		}
	} else {
		moves = append(moves, Move{From: from, To: to})
	}
	return moves
}

func (p *Position) genPawnMoves(sq, file, rank int, us byte, moves []Move) []Move {
	forward, startRank, promoRank := 8, 1, 7
	if us == black {
		forward, startRank, promoRank = -8, 6, 0
	}

	oneRank := rank + 1
	if us == black {
		oneRank = rank - 1
	}
	if oneRank < 0 || oneRank > 7 {
		// A pawn "about to move off the board" shouldn't occur in any
		// legal position (it would already have promoted), but guard
		// against malformed FEN input rather than index out of range.
		return moves
	}
	one := sq + forward

	if p.board[one] == 0 {
		moves = appendPawnMove(moves, byte(sq), byte(one), oneRank == promoRank)
		if rank == startRank && p.board[sq+2*forward] == 0 {
			moves = append(moves, Move{From: byte(sq), To: byte(sq + 2*forward)})
		}
	}

	for _, df := range [2]int{-1, 1} {
		nf := file + df
		if nf < 0 || nf > 7 {
			continue
		}
		to := one + df // same rank as 'one', file shifted by df
		target := p.board[to]
		if target != 0 && pieceColor(target) != us {
			moves = appendPawnMove(moves, byte(sq), byte(to), oneRank == promoRank)
		} else if to == p.epSquare {
			moves = append(moves, Move{From: byte(sq), To: byte(to)})
		}
	}
	return moves
}

// genCastling adds castling moves when the right is held, the squares
// between king and rook are empty (and the rook is actually still
// there — belt and suspenders against inconsistent input FENs), the
// king is not currently in check, and the king's path (including
// destination) is not attacked. Square numbers are hardcoded corners
// since standard chess only castles from the back rank home squares.
func (p *Position) genCastling(us, them byte, moves []Move) []Move {
	if us == white {
		if p.castle&1 != 0 && p.board[5] == 0 && p.board[6] == 0 &&
			p.board[7] == makePiece(white, rook) && p.board[4] == makePiece(white, king) &&
			!p.attacked(4, them) && !p.attacked(5, them) && !p.attacked(6, them) {
			moves = append(moves, Move{From: 4, To: 6})
		}
		if p.castle&2 != 0 && p.board[1] == 0 && p.board[2] == 0 && p.board[3] == 0 &&
			p.board[0] == makePiece(white, rook) && p.board[4] == makePiece(white, king) &&
			!p.attacked(4, them) && !p.attacked(3, them) && !p.attacked(2, them) {
			moves = append(moves, Move{From: 4, To: 2})
		}
	} else {
		if p.castle&4 != 0 && p.board[61] == 0 && p.board[62] == 0 &&
			p.board[63] == makePiece(black, rook) && p.board[60] == makePiece(black, king) &&
			!p.attacked(60, them) && !p.attacked(61, them) && !p.attacked(62, them) {
			moves = append(moves, Move{From: 60, To: 62})
		}
		if p.castle&8 != 0 && p.board[57] == 0 && p.board[58] == 0 && p.board[59] == 0 &&
			p.board[56] == makePiece(black, rook) && p.board[60] == makePiece(black, king) &&
			!p.attacked(60, them) && !p.attacked(59, them) && !p.attacked(58, them) {
			moves = append(moves, Move{From: 60, To: 58})
		}
	}
	return moves
}

// LegalMoves returns every fully legal move in the position: pseudo-legal
// generation followed by the play-it-and-check-the-king filter described
// above the type's methods.
func (p *Position) LegalMoves() []Move {
	pseudo := p.pseudoLegalMoves()
	legal := make([]Move, 0, len(pseudo))
	us := p.side
	for _, m := range pseudo {
		cp := p.Copy()
		cp.applyMove(m)
		// cp.side has flipped to the opponent, which is exactly the
		// side we need to check isn't attacking us's king.
		if !cp.attacked(cp.findKing(us), cp.side) {
			legal = append(legal, m)
		}
	}
	return legal
}

// EnPassantCaptures returns every legal en passant capture in the position.
// There is usually zero or one; there are two only when friendly pawns flank
// the pawn that just double-pushed (both can capture to the same target
// square). Sargon III renders en passant as the square-less token "PXPEP"
// (see its manual's notation section), so a caller reading that token
// disambiguates it against this list — a from/to move it can apply.
func (p *Position) EnPassantCaptures() []Move {
	if p.epSquare < 0 {
		return nil
	}
	var out []Move
	for _, m := range p.LegalMoves() {
		// An en passant capture is a pawn moving diagonally onto the (empty)
		// en passant target square. A straight pawn push can never reach that
		// square (the double-pushed pawn blocks it), but the file guard keeps
		// this robust against any generator change.
		if int(m.To) != p.epSquare {
			continue
		}
		if pieceType(p.board[m.From]) != pawn {
			continue
		}
		if int(m.From)%8 == int(m.To)%8 {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Make plays m if legal, mutating p and returning an error otherwise.
func (p *Position) Make(m Move) error {
	for _, lm := range p.LegalMoves() {
		if lm == m {
			p.applyMove(lm)
			return nil
		}
	}
	return &illegalMoveError{m: m, fen: p.FEN()}
}

type illegalMoveError struct {
	m   Move
	fen string
}

func (e *illegalMoveError) Error() string {
	return "refchess: illegal move " + e.m.String() + " in position " + e.fen
}

// applyMove mutates p by playing m, which is assumed pseudo-legal (it is
// not re-validated for legality here — LegalMoves/Make/Perft are
// responsible for that). It handles captures, en passant, castling rook
// movement, promotion, castling-rights bookkeeping, en passant square
// bookkeeping, the halfmove clock, and side/fullmove advancement.
func (p *Position) applyMove(m Move) {
	us := p.side
	pc := p.board[m.From]
	typ := pieceType(pc)
	captured := p.board[m.To]

	fromFile, toFile := int(m.From)%8, int(m.To)%8
	// En passant: a pawn moving diagonally onto an empty square is only
	// legal (as pseudo-legal move generation only offers it when
	// to==p.epSquare) as an en passant capture; the captured pawn sits
	// on the mover's start rank, at the destination file.
	isEP := typ == pawn && captured == 0 && fromFile != toFile

	p.board[m.From] = 0
	if m.Promo != 0 {
		p.board[m.To] = makePiece(us, promoCharToType(m.Promo))
	} else {
		p.board[m.To] = pc
	}

	if isEP {
		capSq := int(m.From)/8*8 + toFile
		captured = p.board[capSq]
		p.board[capSq] = 0
	}

	if typ == king {
		diff := int(m.To) - int(m.From)
		switch diff {
		case 2: // king side: rook h-file -> f-file
			rookFrom, rookTo := int(m.From)+3, int(m.From)+1
			p.board[rookTo] = p.board[rookFrom]
			p.board[rookFrom] = 0
		case -2: // queen side: rook a-file -> d-file
			rookFrom, rookTo := int(m.From)-4, int(m.From)-1
			p.board[rookTo] = p.board[rookFrom]
			p.board[rookFrom] = 0
		}
	}

	p.updateCastleRights(m.From, m.To)

	p.epSquare = -1
	if typ == pawn {
		diff := int(m.To) - int(m.From)
		if diff == 16 || diff == -16 {
			p.epSquare = (int(m.From) + int(m.To)) / 2
		}
	}

	if typ == pawn || captured != 0 {
		p.halfmove = 0
	} else {
		p.halfmove++
	}

	if us == black {
		p.fullmove++
	}
	p.side = 1 - us
}

// updateCastleRights clears rights implied lost by a piece moving away
// from (or a capture landing on) one of the six squares that matter:
// clearing based on the square, not on what's moving, correctly handles
// both "king/rook moved" and "rook got captured on its home square" with
// one rule.
func (p *Position) updateCastleRights(from, to byte) {
	clear := func(sq byte) {
		switch sq {
		case 0:
			p.castle &^= 2
		case 4:
			p.castle &^= 1 | 2
		case 7:
			p.castle &^= 1
		case 56:
			p.castle &^= 8
		case 60:
			p.castle &^= 4 | 8
		case 63:
			p.castle &^= 4
		}
	}
	clear(from)
	clear(to)
}

func promoCharToType(c byte) byte {
	switch c {
	case 'n':
		return knight
	case 'b':
		return bishop
	case 'r':
		return rook
	default:
		return queen
	}
}
