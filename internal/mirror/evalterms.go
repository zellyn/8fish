package mirror

// Experimental cheap eval terms screened in task #18. Each term is a
// flat centipawn weight (pawn ~= 100, matching vicVal); 0 disables it.
// extraEval returns the WHITE-POV sum (white contribution minus black),
// added in eval() before the side-to-move negation, exactly like the
// PStruct term. Every term is derivable either from the per-file pawn
// occupancy bytes (which the asm maintains incrementally in PWBITS/
// PBBITS) or from a single pass over the 32-slot piece list, so each
// has a plausible cheap 6502 implementation.
type EvalTerms struct {
	RookOpen   int // rook on a fully open file (no pawns of either color)
	RookSemi   int // rook on a semi-open file (no OWN pawns, enemy present)
	BishopPair int // bonus if a side has >= 2 bishops
	Rook7th    int // rook on its relative 7th rank (white r6 / black r1)
	DoubledRk  int // bonus if 2+ own rooks share a file
	Blockade   int // penalty: enemy piece directly in front of a passed pawn
	Tropism    int // per-unit bonus for (7 - Chebyshev(N/B/R/Q, enemy king))
}

// anyOn reports whether any term is enabled.
func (t EvalTerms) anyOn() bool {
	return t != EvalTerms{}
}

// ExtraEval exposes the white-POV extra-terms sum for the asm parity
// harness (internal/chesstest). It is the exact reference the asm
// extraterm/XSTRUCT must match.
func (e *Engine) ExtraEval() int { return e.extraEval() }

// RookTermsAsm is the fixed weight set ported to the asm engine in
// task #51 (FT_ROOKX): rook open +25 / semi-open +12 (per rook),
// doubled rooks +15, passed-pawn blockade -18. The asm bakes these in;
// the parity test drives the mirror with exactly this set.
var RookTermsAsm = EvalTerms{RookOpen: 25, RookSemi: 12, DoubledRk: 15, Blockade: 18}

// extraEval computes the white-POV sum of the enabled experimental
// terms. Cheap-scan design: one pass fills per-file pawn presence and
// the most-advanced rank per file (the PWBITS/PBBITS data), then one
// pass over the piece list applies the piece-based terms.
func (e *Engine) extraEval() int {
	t := e.Extra
	p := &e.Pos

	// Per-file pawn presence per color (the PWBITS/PBBITS != 0 test).
	var pw, pb [8]bool
	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq || p.Board[sq]&TypeMask != Pawn {
			continue
		}
		file := int(sq & 7)
		if slot < 16 {
			pw[file] = true
		} else {
			pb[file] = true
		}
	}

	wk := p.PieceSq[0]
	bk := p.PieceSq[16]

	var bishW, bishB int
	var rookFileW, rookFileB [8]int // rooks per file, per color
	score := 0

	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		piece := p.Board[sq]
		typ := piece & TypeMask
		white := slot < 16
		file := int(sq & 7)
		rank := int(sq >> 4)

		switch typ {
		case Bishop:
			if white {
				bishW++
			} else {
				bishB++
			}
		case Rook:
			if white {
				rookFileW[file]++
			} else {
				rookFileB[file]++
			}
			// Open / semi-open file.
			if t.RookOpen != 0 || t.RookSemi != 0 {
				if white {
					if !pw[file] {
						if !pb[file] {
							score += t.RookOpen
						} else {
							score += t.RookSemi
						}
					}
				} else {
					if !pb[file] {
						if !pw[file] {
							score -= t.RookOpen
						} else {
							score -= t.RookSemi
						}
					}
				}
			}
			// Rook on relative 7th rank.
			if t.Rook7th != 0 {
				if white && rank == 6 {
					score += t.Rook7th
				} else if !white && rank == 1 {
					score -= t.Rook7th
				}
			}
		}

		// King tropism: reward own N/B/R/Q near the ENEMY king.
		if t.Tropism != 0 && (typ == Knight || typ == Bishop || typ == Rook || typ == Queen) {
			if white {
				score += t.Tropism * (7 - cheb(sq, bk))
			} else {
				score -= t.Tropism * (7 - cheb(sq, wk))
			}
		}
	}

	// Bishop pair.
	if t.BishopPair != 0 {
		if bishW >= 2 {
			score += t.BishopPair
		}
		if bishB >= 2 {
			score -= t.BishopPair
		}
	}

	// Doubled rooks on a file.
	if t.DoubledRk != 0 {
		for f := range 8 {
			if rookFileW[f] >= 2 {
				score += t.DoubledRk
			}
			if rookFileB[f] >= 2 {
				score -= t.DoubledRk
			}
		}
	}

	// Passed-pawn blockade: enemy piece directly on the square in front
	// of a passed pawn (penalty for the pawn's owner).
	if t.Blockade != 0 {
		score += e.blockadeTerm(t.Blockade)
	}

	return score
}

// blockadeTerm computes the white-POV passed-pawn blockade penalty. It
// reuses the exact passed-pawn definition from extractPawnFeatures
// (most-advanced pawn per file; blocked by an adjacent-or-same-file
// enemy pawn at rank >= r for white). Front-square occupancy by an
// enemy piece is the blockade.
func (e *Engine) blockadeTerm(w int) int {
	p := &e.Pos
	var pwmax, pbmax, pwmin, pbmin [8]int
	var pw, pb [8]bool
	for i := range pwmin {
		pwmin[i], pbmin[i] = 15, 15
	}
	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq || p.Board[sq]&TypeMask != Pawn {
			continue
		}
		file := int(sq & 7)
		rank := int(sq >> 4)
		if slot < 16 {
			pw[file] = true
			if rank > pwmax[file] {
				pwmax[file] = rank
			}
			if rank < pwmin[file] {
				pwmin[file] = rank
			}
		} else {
			pb[file] = true
			if rank > pbmax[file] {
				pbmax[file] = rank
			}
			if rank < pbmin[file] {
				pbmin[file] = rank
			}
		}
	}
	max3 := func(a *[8]int, x int) int {
		m := a[x]
		if x > 0 && a[x-1] > m {
			m = a[x-1]
		}
		if x < 7 && a[x+1] > m {
			m = a[x+1]
		}
		return m
	}
	min3 := func(a *[8]int, x int) int {
		m := a[x]
		if x > 0 && a[x-1] < m {
			m = a[x-1]
		}
		if x < 7 && a[x+1] < m {
			m = a[x+1]
		}
		return m
	}
	score := 0
	for f := range 8 {
		if pw[f] {
			r := pwmax[f]
			if max3(&pbmax, f) < r { // white passed pawn on file f at rank r
				front := byte((r+1)<<4 | f)
				if r < 7 && p.Board[front] != 0 && p.Board[front]&ColorMask != 0 {
					score -= w // black piece blockades white's passer
				}
			}
		}
		if pb[f] {
			r := pbmin[f]
			if min3(&pwmin, f) > r { // black passed pawn on file f at rank r
				front := byte((r-1)<<4 | f)
				if r > 0 && p.Board[front] != 0 && p.Board[front]&ColorMask == 0 {
					score += w // white piece blockades black's passer
				}
			}
		}
	}
	return score
}

// cheb is Chebyshev (king-move) distance between two 0x88 squares.
func cheb(a, b byte) int {
	dr := int(a>>4) - int(b>>4)
	if dr < 0 {
		dr = -dr
	}
	df := int(a&7) - int(b&7)
	if df < 0 {
		df = -df
	}
	if dr > df {
		return dr
	}
	return df
}
