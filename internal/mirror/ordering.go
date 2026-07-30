package mirror

import "sort"

// This file implements the task #35 move-ordering enablers: static
// exchange evaluation (SEE) for capture ordering, a butterfly (from/to)
// history heuristic for quiet ordering, and a unified scored/sorted
// full-width move loop that consumes them. The asm baseline keeps its
// five-pass MVV-bucket + killer ordering (see moveLoop); this path is
// selected only when FtSEE or FtHistory is set, so single-toggle A/B
// tests measure exactly the ordering change.
//
// PORTING NOTE (6502-idiomatic feasibility, for Fable's queue):
//   - History: a [from][to] byte table (128*128) is 16 KB — too big for
//     a 64 KB machine to spend lightly. A [piece-type][to] table
//     (7*128 = 896 bytes) or a [from-file..][to] compressed table is the
//     realistic 6502 shape; aging is a cheap LSR sweep. Sizing/aging is
//     an open PORT decision, flagged not solved here.
//   - SEE: needs repeated least-valuable-attacker scans and swap
//     arithmetic. No hardware multiply on the NMOS 6502, but SEE uses
//     only adds/subtracts and table-indexed piece values, so the
//     multiply objection is weaker than it looks; the real cost is the
//     attacker re-scan per swap ply. Open PORT question, flagged here.

// OrderParams tunes the scored ordered move loop.
type OrderParams struct {
	// LosingLast, with FtSEE, sorts SEE<0 captures after quiet moves
	// (they rank below history-ordered quiets rather than among winning
	// captures). Off = all captures rank above quiets (MVV-style bands).
	LosingLast bool
	// HistMalus applies the "history gravity" malus: on a quiet fail-high
	// the quiet moves tried before the cutter get their history score
	// decremented (only meaningful with FtHistory).
	HistMalus bool
}

// seeVal is the exchange value of a piece type (reuses the delta-pruning
// victim values; king huge so a king recapture never looks profitable to
// trade into).
func seeVal(typ byte) int32 { return int32(vicVal[typ&TypeMask]) }

// leastAttacker returns the square and type of the least valuable piece
// of side `side` (0/8) attacking square `to` under occupancy occ, or
// NoSq if none. It rescans occ each call, so removing a used attacker and
// calling again naturally reveals x-ray attackers behind sliders.
func leastAttacker(occ *[128]byte, to, side byte) (byte, byte) {
	bestSq := byte(NoSq)
	var bestType byte
	bestVal := int32(1 << 30)
	consider := func(sq int, typ byte) {
		v := seeVal(typ)
		if v < bestVal {
			bestVal, bestSq, bestType = v, byte(sq), typ
		}
	}

	// Pawns (cheapest; a pawn of `side` sits on the square from which it
	// captures onto `to`).
	if side == 0 {
		for _, off := range [2]int{-0x0F, -0x11} {
			s := int(to) + off
			if s >= 0 && s&0x88 == 0 {
				pc := occ[s]
				if pc != 0 && pc&ColorMask == 0 && pc&TypeMask == Pawn {
					return byte(s), Pawn
				}
			}
		}
	} else {
		for _, off := range [2]int{0x0F, 0x11} {
			s := int(to) + off
			if s >= 0 && s&0x88 == 0 {
				pc := occ[s]
				if pc != 0 && pc&ColorMask != 0 && pc&TypeMask == Pawn {
					return byte(s), Pawn
				}
			}
		}
	}

	// Knights.
	for _, off := range knightOffs {
		s := int(to) + off
		if s < 0 || s&0x88 != 0 {
			continue
		}
		pc := occ[s]
		if pc != 0 && pc&ColorMask == side&ColorMask && pc&TypeMask == Knight {
			consider(s, Knight)
		}
	}

	// Diagonal sliders (bishop, queen): nearest piece along each diagonal.
	for _, off := range diagOffs {
		s := int(to)
		for {
			s += off
			if s < 0 || s&0x88 != 0 {
				break
			}
			pc := occ[s]
			if pc == 0 {
				continue
			}
			if pc&ColorMask == side&ColorMask {
				if t := pc & TypeMask; t == Bishop || t == Queen {
					consider(s, t)
				}
			}
			break
		}
	}

	// Orthogonal sliders (rook, queen): nearest piece along each rank/file.
	for _, off := range orthoOffs {
		s := int(to)
		for {
			s += off
			if s < 0 || s&0x88 != 0 {
				break
			}
			pc := occ[s]
			if pc == 0 {
				continue
			}
			if pc&ColorMask == side&ColorMask {
				if t := pc & TypeMask; t == Rook || t == Queen {
					consider(s, t)
				}
			}
			break
		}
	}

	// King (most valuable, so only chosen when nothing cheaper exists).
	for _, off := range kingOffs {
		s := int(to) + off
		if s < 0 || s&0x88 != 0 {
			continue
		}
		pc := occ[s]
		if pc != 0 && pc&ColorMask == side&ColorMask && pc&TypeMask == King {
			consider(s, King)
		}
	}

	return bestSq, bestType
}

// seeValue returns the static exchange evaluation of capture move m, in
// centipawns from the mover's point of view. Non-captures return 0.
// Promotions are approximated: the capture value only (the +promotion
// gain is not modeled, so promo captures are slightly undervalued — good
// enough for ordering, where all promotions already rank high).
func (e *Engine) seeValue(m Move) int32 {
	p := &e.Pos
	to := m.To
	capSq := to
	if m.Flags&FlEP != 0 {
		if p.Board[m.From]&ColorMask == 0 {
			capSq = to - 0x10
		} else {
			capSq = to + 0x10
		}
	}
	victim := p.Board[capSq]
	if victim == 0 {
		return 0
	}

	occ := p.Board // value copy of the 0x88 board
	mover := p.Board[m.From]
	occ[m.From] = 0
	occ[capSq] = 0
	occ[to] = mover // the attacker now stands on `to`

	var g [32]int32
	g[0] = seeVal(victim)
	aVal := seeVal(mover)
	side := p.Side ^ ColorMask // side to recapture next
	d := 0
	for {
		d++
		g[d] = aVal - g[d-1]
		sq, t := leastAttacker(&occ, to, side)
		if sq == NoSq {
			break
		}
		aVal = seeVal(t)
		occ[sq] = 0
		side ^= ColorMask
	}
	// Negamax the exchange back to the root capture.
	for d--; d > 0; d-- {
		if -g[d-1] > g[d] {
			g[d-1] = -(-g[d-1])
		} else {
			g[d-1] = -g[d]
		}
	}
	return g[0]
}

// Score bands for ordered move selection. TT move first, then captures,
// killers, history-ordered quiets, and (optionally) losing captures last.
const (
	scoreTT      = int64(1) << 40
	scoreWinCap  = int64(1) << 32
	scoreKiller  = int64(1) << 28
	scoreLoseCap = -(int64(1) << 32)
	histClampMax = int64(1)<<27 - 1 // keep quiets below the killer band
)

// scoredMove pairs a move with its ordering key and its LMR class.
type scoredMove struct {
	m         Move
	score     int64
	reducible bool // eligible for late-move reduction (quiet, non-killer class)
	quiet     bool // non-capturing, non-promotion (killers, history, futility)
}

// orderMoves builds the sorted move list for a full-width node.
func (e *Engine) orderMoves() []scoredMove {
	p := &e.Pos
	ply := p.Ply
	list := e.generate(false)
	killersOn := e.Features&FtKiller != 0
	histOn := e.Features&FtHistory != 0
	seeOn := e.Features&FtSEE != 0
	reduceKillers := e.LMR.ReduceKillers
	ttFrom, ttTo := e.ttFrom[ply], e.ttTo[ply]

	out := make([]scoredMove, 0, len(list))
	for _, m := range list {
		isCap := p.Board[m.To] != 0 || m.Flags&FlEP != 0
		isPromo := m.Flags&FlPromo != 0
		sm := scoredMove{m: m}

		switch {
		case m.From == ttFrom && m.To == ttTo:
			sm.score = scoreTT
		case isPromo:
			// Promotions always rank with the winning captures.
			sm.score = scoreWinCap + int64(seeVal(Queen))
		case isCap:
			var capScore int64
			if seeOn {
				capScore = int64(e.seeValue(m))
			} else {
				// MVV-LVA fallback (still a big step up from the asm's
				// two-bucket heavy/light victim split — it adds LVA).
				victim := Pawn
				if m.Flags&FlEP == 0 {
					victim = int(p.Board[m.To] & TypeMask)
				}
				attacker := int(p.Board[m.From] & TypeMask)
				capScore = int64(vicVal[victim])*16 - int64(vicVal[attacker])
			}
			if seeOn && e.Ord.LosingLast && capScore < 0 {
				sm.score = scoreLoseCap + capScore
			} else {
				sm.score = scoreWinCap + capScore
			}
		default:
			// Quiet move.
			sm.quiet = true
			if killersOn && e.killerMatch(ply, m) {
				sm.score = scoreKiller
				sm.reducible = reduceKillers
			} else {
				sm.reducible = true
				if histOn {
					h := int64(e.hist[p.Side>>3][m.From][m.To])
					if h > histClampMax {
						h = histClampMax
					}
					sm.score = h
				}
			}
		}
		out = append(out, sm)
	}
	// Stable sort keeps generation order among equal scores (so with
	// FtHistory off, quiets stay in the asm's generation order).
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

// orderedMoveLoop is the scored-ordering replacement for moveLoop's five
// passes, used at full-width nodes when FtSEE/FtHistory is enabled. It is
// a faithful re-expression of moveLoop's per-move search body (make,
// legality, LMR/PVS, fail-hard cutoff, killer/TT store) over a single
// sorted list, plus the history update.
func (e *Engine) orderedMoveLoop() int {
	p := &e.Pos
	ply := p.Ply
	moves := e.orderMoves()
	histOn := e.Features&FtHistory != 0
	rem := e.MaxDepth - ply

	// Quiet moves tried before a cutoff, for the history malus.
	var triedQuiets []Move

	// LMP is armed once for the node: whether this node qualifies (shallow,
	// non-PV, not in check, alpha outside the mate zone) is independent of
	// the move, so the per-move test collapses to a counter compare.
	lmpOn := e.lmpNodeOK(ply, rem)

	for _, sm := range moves {
		m := sm.m
		// Leaf futility: quiets are pruned (captures/promotions and the
		// TT move still searched), matching moveLoop's e.futile handling.
		if e.futile[ply] && sm.quiet && sm.score != scoreTT {
			continue
		}

		// Late-move (movecount) pruning: once threshold(d) legal moves have
		// been searched, skip the remaining quiets. Pre-make when possible
		// (a pure counter compare); with KeepChecks the move must be made
		// first to learn whether it gives check.
		lmpCand := lmpOn && e.lmpQuietOK(sm)
		if lmpCand && !e.LMP.KeepChecks {
			continue
		}

		// Pre-make evasion filter, as moveLoop (asm sdevade): provably
		// illegal in-check moves never reach make().
		if e.inChk[ply] && e.evasionFiltered(m, ply) {
			continue
		}
		e.make(m)
		moverKing := p.PieceSq[int(p.Side^ColorMask)<<1]
		if e.attacked(moverKing, p.Side) {
			e.unmake()
			continue
		}
		if lmpCand && !e.inChk[ply+1] {
			// KeepChecks path: legal, quiet, past the count, and not a
			// checking move — prune it (undo the probing make, don't count).
			e.unmake()
			continue
		}
		e.legal[ply]++

		// LMR/PVS child window mode (mirrors moveLoop's smset block).
		mode := 0
		if e.Features&FtLMR != 0 && e.legal[ply] >= 2 &&
			(e.LMR.EvasionPVS || !e.inChk[ply]) {
			mode = 1
			if ply != 0 && sm.reducible && !e.inChk[ply] && !e.inChk[ply+1] &&
				e.legal[ply] >= e.LMR.LateR1 && rem >= e.LMR.MinRemR1 {
				mode = 2
				if rem >= e.LMR.MinRemR2 && e.legal[ply] >= e.LMR.LateR2 {
					mode = 3
				}
			}
		}

		// Check extension (see moveLoop / CheckExtParams): extend the child
		// one ply when this move gives check, capped per path. Never in QS
		// (orderedMoveLoop is full-width only, so qs=false here), never with an
		// LMR reduction (a checking move is mode < 2 via the reduction gate).
		ext := e.checkExt(false, ply)

		var score int
		for {
			e.beta[ply+1] = -e.alpha[ply]
			if mode != 0 {
				e.alpha[ply+1] = e.beta[ply+1] - 1
			} else {
				e.alpha[ply+1] = -e.beta[ply]
			}
			dd := ext
			if mode >= 2 {
				dd = -(mode - 1)
			}
			e.MaxDepth += dd
			e.numExt += ext
			score = -e.search()
			e.numExt -= ext
			e.MaxDepth -= dd
			e.unmake()
			if e.aborted {
				return e.alpha[ply] // node budget hit: unwind (result discarded)
			}
			if mode == 0 || score <= e.alpha[ply] {
				break
			}
			if mode >= 2 {
				mode = 1
			} else if e.beta[ply]-e.alpha[ply] >= 2 {
				mode = 0
			} else {
				break
			}
			e.make(m) // legality already proven
		}

		if score >= e.beta[ply] {
			if sm.quiet {
				if e.Features&FtKiller != 0 {
					k := &e.killer[ply]
					if k[0].From != m.From || k[0].To != m.To {
						k[1] = k[0]
						k[0] = Move{m.From, m.To, 0}
					}
				}
				if histOn {
					e.histBump(m, triedQuiets, rem)
				}
			}
			e.ttstore(ttLower, m.From, m.To, e.beta[ply])
			return e.beta[ply]
		}
		if score > e.alpha[ply] {
			e.alpha[ply] = score
			e.raised[ply] = true
			e.ttBF[ply] = m.From
			e.ttBT[ply] = m.To
			if ply == 0 {
				e.Best = m
			}
		}
		if histOn && sm.quiet {
			triedQuiets = append(triedQuiets, m)
		}
	}

	return e.done()
}

// lmpNodeOK reports whether this node qualifies for late-move pruning:
// LMP on, shallow (1 <= rem <= Dmax), not the root, not in check, a
// non-PV (zero-width) window, and alpha outside the mate zone. These
// depend only on the node, not the move, so they are tested once.
func (e *Engine) lmpNodeOK(ply, rem int) bool {
	if e.LMP.Dmax == 0 || rem < 1 || rem > e.LMP.Dmax {
		return false
	}
	if ply == 0 || e.inChk[ply] {
		return false
	}
	if e.beta[ply]-e.alpha[ply] >= 2 {
		return false // PV node: never movecount-prune
	}
	// Mate-zone guard: don't prune quiets while proving/avoiding a mate.
	return e.alpha[ply] > nmateZoneHi && e.alpha[ply] < mateZoneLo
}

// lmpQuietOK reports whether move sm is a pruning candidate given the
// node already qualified: it is a quiet non-TT move, optionally not a
// killer, and enough legal moves have already been searched. The count
// (e.legal[ply]) is post-increment, so it holds the number of moves
// already searched at the moment of this test (pre-make).
func (e *Engine) lmpQuietOK(sm scoredMove) bool {
	if !sm.quiet || sm.score == scoreTT {
		return false
	}
	if e.LMP.ExemptKillers && sm.score == scoreKiller {
		return false
	}
	rem := e.MaxDepth - e.Pos.Ply
	return e.legal[e.Pos.Ply] >= e.LMP.lmpThreshold(rem)
}

// histBump rewards the quiet move that caused a fail-high and (with
// HistMalus) penalizes the quiets that were tried first but did not.
func (e *Engine) histBump(cut Move, tried []Move, rem int) {
	side := e.Pos.Side >> 3
	bonus := int32(rem * rem)
	h := &e.hist[side]
	h[cut.From][cut.To] += bonus
	if h[cut.From][cut.To] > 1<<28 {
		// Halve the whole side's table if any entry saturates.
		for f := range h {
			for t := range h[f] {
				h[f][t] >>= 1
			}
		}
	}
	if e.Ord.HistMalus {
		for _, q := range tried {
			h[q.From][q.To] -= bonus
		}
	}
}
