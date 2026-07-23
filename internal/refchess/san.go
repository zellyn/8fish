package refchess

// Standard Algebraic Notation (SAN) <-> Move, resolved against the legal
// move generator so both directions are fully rules-correct: parsing an
// ambiguous or illegal SAN string fails loudly, and rendering emits the
// minimal disambiguation FIDE requires. This is what lets the opening
// book be authored in the notation a chess player actually reads
// (1. e4 e5 2. Nf3 ...) while every move is still validated move-by-move
// through this reference implementation.

import (
	"fmt"
	"strings"
)

// pieceLetter is the SAN letter for a non-pawn piece type.
func pieceLetter(typ byte) byte {
	switch typ {
	case knight:
		return 'N'
	case bishop:
		return 'B'
	case rook:
		return 'R'
	case queen:
		return 'Q'
	case king:
		return 'K'
	}
	return '?'
}

// letterToType maps a SAN piece letter to a piece type (0 if not a piece
// letter — i.e. a pawn move, whose SAN carries no leading letter).
func letterToType(c byte) byte {
	switch c {
	case 'N':
		return knight
	case 'B':
		return bishop
	case 'R':
		return rook
	case 'Q':
		return queen
	case 'K':
		return king
	}
	return 0
}

// isCapture reports whether m captures in position p (including en
// passant, where the destination square itself is empty).
func (p *Position) isCapture(m Move) bool {
	if p.board[m.To] != 0 {
		return true
	}
	return pieceType(p.board[m.From]) == pawn && int(m.From)%8 != int(m.To)%8
}

// SAN renders m (which must be legal in p) as Standard Algebraic Notation,
// including the minimal disambiguation and a trailing "+"/"#" for
// check/checkmate.
func (p *Position) SAN(m Move) string {
	typ := pieceType(p.board[m.From])

	// Castling is by king displacement of two files on the home rank.
	if typ == king {
		switch int(m.To) - int(m.From) {
		case 2:
			return p.withCheck(m, "O-O")
		case -2:
			return p.withCheck(m, "O-O-O")
		}
	}

	capture := p.isCapture(m)
	var sb strings.Builder
	if typ == pawn {
		if capture {
			sb.WriteByte('a' + m.From%8)
			sb.WriteByte('x')
		}
		sb.WriteString(sqName(m.To))
		if m.Promo != 0 {
			sb.WriteByte('=')
			sb.WriteByte(pieceLetter(promoCharToType(m.Promo)))
		}
	} else {
		sb.WriteByte(pieceLetter(typ))
		sb.WriteString(p.disambig(m, typ))
		if capture {
			sb.WriteByte('x')
		}
		sb.WriteString(sqName(m.To))
	}
	return p.withCheck(m, sb.String())
}

// disambig returns the file, rank, or file+rank string needed to tell m
// apart from any other same-type piece that could also legally move to
// m.To — "" when m is already unambiguous.
func (p *Position) disambig(m Move, typ byte) string {
	var others []Move
	for _, lm := range p.LegalMoves() {
		if lm.To == m.To && lm.From != m.From && pieceType(p.board[lm.From]) == typ {
			others = append(others, lm)
		}
	}
	if len(others) == 0 {
		return ""
	}
	sameFile, sameRank := false, false
	for _, o := range others {
		if o.From%8 == m.From%8 {
			sameFile = true
		}
		if o.From/8 == m.From/8 {
			sameRank = true
		}
	}
	switch {
	case !sameFile:
		return string('a' + m.From%8)
	case !sameRank:
		return string('1' + m.From/8)
	default:
		return sqName(m.From)
	}
}

// withCheck appends "+" (check) or "#" (checkmate) to san if m gives one.
func (p *Position) withCheck(m Move, san string) string {
	cp := p.Copy()
	cp.applyMove(m)
	if !cp.attacked(cp.findKing(cp.side), 1-cp.side) {
		return san
	}
	if len(cp.LegalMoves()) == 0 {
		return san + "#"
	}
	return san + "+"
}

// ParseSAN resolves a SAN move string in position p to the unique legal
// Move it names. It fails if the string is malformed, names no legal move,
// or is ambiguous (matches more than one legal move). Trailing check/mate
// and annotation glyphs (+ # ! ?) are ignored.
func (p *Position) ParseSAN(san string) (Move, error) {
	s := strings.TrimRight(strings.TrimSpace(san), "+#!?")
	if s == "" {
		return Move{}, fmt.Errorf("refchess: empty SAN move")
	}
	legal := p.LegalMoves()

	// Castling.
	if s == "O-O" || s == "0-0" {
		return p.matchCastle(legal, 2, san)
	}
	if s == "O-O-O" || s == "0-0-0" {
		return p.matchCastle(legal, -2, san)
	}

	// Promotion suffix: "=Q" or a bare trailing piece letter ("e8Q").
	var promo byte
	if i := strings.IndexByte(s, '='); i >= 0 {
		if i+1 >= len(s) {
			return Move{}, fmt.Errorf("refchess: bad SAN %q: missing promotion piece", san)
		}
		promo = promoLetterToChar(s[i+1])
		if promo == 0 {
			return Move{}, fmt.Errorf("refchess: bad SAN %q: bad promotion piece", san)
		}
		s = s[:i]
	} else if n := len(s); n >= 3 && letterToType(s[n-1]) != 0 && s[n-2] >= '1' && s[n-2] <= '8' {
		promo = promoLetterToChar(s[n-1])
		s = s[:n-1]
	}

	// Leading piece letter (absent for pawn moves).
	typ := byte(pawn)
	if t := letterToType(s[0]); t != 0 {
		typ = t
		s = s[1:]
	}

	// Drop the capture marker; what precedes the last two chars (the
	// destination square) is 0-2 disambiguation chars.
	s = strings.Replace(s, "x", "", 1)
	if len(s) < 2 {
		return Move{}, fmt.Errorf("refchess: bad SAN %q: no destination square", san)
	}
	dest, err := parseSquare(s[len(s)-2:])
	if err != nil {
		return Move{}, fmt.Errorf("refchess: bad SAN %q: %v", san, err)
	}
	df, dr := -1, -1
	for i := 0; i < len(s)-2; i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'h':
			df = int(c - 'a')
		case c >= '1' && c <= '8':
			dr = int(c - '1')
		default:
			return Move{}, fmt.Errorf("refchess: bad SAN %q: unexpected %q", san, string(c))
		}
	}

	var match []Move
	for _, lm := range legal {
		if lm.To != dest || pieceType(p.board[lm.From]) != typ {
			continue
		}
		if (promo == 0) != (lm.Promo == 0) || (promo != 0 && lm.Promo != promo) {
			continue
		}
		if df >= 0 && int(lm.From)%8 != df {
			continue
		}
		if dr >= 0 && int(lm.From)/8 != dr {
			continue
		}
		match = append(match, lm)
	}
	switch len(match) {
	case 0:
		return Move{}, fmt.Errorf("refchess: SAN %q: no legal move in %s", san, p.FEN())
	case 1:
		return match[0], nil
	default:
		return Move{}, fmt.Errorf("refchess: SAN %q is ambiguous in %s", san, p.FEN())
	}
}

func (p *Position) matchCastle(legal []Move, delta int, san string) (Move, error) {
	for _, lm := range legal {
		if pieceType(p.board[lm.From]) == king && int(lm.To)-int(lm.From) == delta {
			return lm, nil
		}
	}
	return Move{}, fmt.Errorf("refchess: SAN %q: castling not legal in %s", san, p.FEN())
}

// promoLetterToChar maps a SAN promotion letter (either case) to the
// lowercase promotion char refchess Moves use (0 if not a promotable piece).
func promoLetterToChar(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		c += 'a' - 'A'
	}
	switch c {
	case 'n', 'b', 'r', 'q':
		return c
	}
	return 0
}
