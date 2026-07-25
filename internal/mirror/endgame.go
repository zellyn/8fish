package mirror

// Endgame TECHNIQUE eval (task: the loss diagnosis of 2026-07-25 found
// 28/96 distinct losses were even-or-better endgames thrown away and
// 42/51 distinct draws were clearly-winning positions not converted —
// with a median blindGap of only ~57cp, i.e. our eval AGREES with a
// 5x-deeper oracle. That rules out depth: the gap is missing endgame
// KNOWLEDGE in the evaluation.)
//
// mopup.go is the floor: it drives a lone king to a corner once one side
// is a rook up. What was missing is technique in endgames that still have
// pawns:
//
//	(1) KING ACTIVITY. The EG king PSQT centralizes weakly (+24 centre /
//	    -53 corner, tapered) and says nothing about WHERE the play is. Two
//	    terms: extra centralization (CMD table, the mop-up's) and king
//	    proximity to the nearest pawn of either color ("the king is a
//	    fighting piece in the endgame").
//	(2) PASSED-PAWN TECHNIQUE. pawnterm's Texel-tuned PASSEDBONUS is
//	    {0,15,0,21,50,52,20,0} by advancement — NOT monotone on its own,
//	    and it values a pawn on the 7th (20) BELOW one on the 5th (50).
//	    CAVEAT (2026-07-25 investigation, see docs/results.md): that is
//	    NOT the tuning artifact it looks like — the bonus is a top-up on
//	    the PeSTO pawn PSQT, and PSQT+bonus (what a passer is really
//	    worth) IS strictly increasing from the 4th rank up; the free
//	    Texel re-tune reproduces the shipped table exactly and monotone
//	    replacements screen neutral-to-negative. EGPass adds an
//	    endgame-only, monotone, advancement-scaled top-up. Plus the two
//	    king/passer relations (our king escorting, their king blockading)
//	    and the rule of the square (an unstoppable passer is worth a
//	    queen, and it costs two coordinate compares to see).
//	(3) K+P BASICS. KingAhead rewards our king in front of our own passer
//	    (the cheap subset of "key squares"); the rule of the square
//	    handles the running pawn; "don't trade into a lost pawn ending"
//	    falls out of the eval being right about the resulting ending.
//	(4) ROOK ENDINGS. RookBehind: a rook (either color) on the passer's
//	    file BEHIND it (Tarrasch's rule). One file scan per passer.
//
// Everything is phase-gated (Pos.Phase <= PhaseMax, the mop-up's gate
// shape) so no middlegame position can change — proven by
// TestEndgameNoMiddlegameLeak over ~24k random-play positions.
//
// 6502 portability: one pass over the 32-slot piece list fills the same
// per-file pawn bytes pawnterm already maintains incrementally (PWBITS/
// PBBITS + the per-file max/min rank); the king terms are a 64-byte CMD
// table lookup and |dr|,|df| compares; the square rule is a Chebyshev
// distance (one MAX of two subtracts) against a rank-count; RookBehind is
// a file walk over at most 6 board bytes. All integer, no multiplies
// beyond a table-indexed weight (the weights are small enough for the
// asm's shift-and-add pattern). See the asm sketch at the bottom.

// EndgameParams configures the endgame-technique terms. The zero value is
// OFF: eval() then never calls egEval and the search tree is byte-
// identical to the shipped asm's, so every parity gate is unaffected.
type EndgameParams struct {
	Enable bool
	// PhaseMax is the taper-phase gate: the whole term is silent above it.
	// Phase counts N=1,B=1,R=2,Q=4 per side (max 24), so 6 = "queen-or-
	// less each side" (R+B vs R+B, R vs R, N vs N, bare pawns).
	PhaseMax int

	// KingCent weights (CMD[theirKing] - CMD[ourKing]): extra king
	// centralization on top of the EG PSQT. CMD is 0 in the centre, 6 in
	// a corner, so the term spans +/- 6*KingCent.
	KingCent int
	// KingPawn weights (theirKingDistToNearestPawn - ourKingDist), a
	// Chebyshev distance to the nearest pawn of EITHER color: "get the
	// king to where the play is". Silent when the board has no pawns
	// (that is the mop-up's job).
	KingPawn int

	// Pass is an endgame-only top-up to pawnterm's passed bonus, indexed
	// by advancement exactly like Weights.Passed (white: the pawn's rank
	// 1..6; black: 7-rank), fixing the non-monotone tuned table.
	Pass [8]int
	// PassKingOur weights (4 - cheb(ourKing, frontSq)) per own passer:
	// escort your passer. frontSq is the square directly ahead of it.
	PassKingOur int
	// PassKingThem weights (cheb(theirKing, frontSq) - 4) per own passer:
	// a passer whose path their king cannot reach is worth more.
	PassKingThem int
	// KingAhead is a flat bonus per own passer whose own king stands
	// strictly AHEAD of it within one file (the cheap key-square proxy);
	// the same magnitude is charged as a penalty when our king sits
	// directly BEHIND it on the same file (blocking its own pawn).
	KingAhead int
	// Unstoppable is the rule-of-the-square bonus: the defending side has
	// no pieces at all (king + pawns only), the passer's path is clear,
	// and their king is outside the square. Big, but < a queen so it can
	// never outweigh real material.
	Unstoppable int
	// RookBehind is the Tarrasch bonus for a rook on the passer's file
	// behind the pawn: + when it is the pawn owner's rook, - when it is
	// the defender's (their rook behind our passer is good for them).
	RookBehind int
}

// on reports whether the endgame-technique term is active.
func (p *EndgameParams) on() bool { return p.Enable }

// DefaultEndgame is the SCREENED endgame-technique set (conversion suite, 25
// positions x 20 dither seeds, hero-only vs a fixed OFF defender;
// docs/results.md 2026-07-25). Versus the shipped engine it is
// **+2.08 +/- 0.71 match points per position, 95% CI [+0.69,+3.47]** — the
// error bar is PAIRED OVER POSITIONS, since the 20 runs within a position
// share everything but the dither seed (an earlier version quoted a
// per-GAME bar over 500 games, which was fraudulently tight).
//
// Gate phase <= 6, matching the mop-up. Phase 8 measured indistinguishable
// (+1.26 +/- 0.70 vs +1.30 +/- 0.70), so the tighter gate is kept on the
// principle that a narrower gate cannot hurt the middlegame.
//
// Two designed terms are shipped OFF (knobs kept so the result stays
// reproducible — TestEndgameConversionDecide pairs them against this set
// directly, which is the decision-relevant comparison):
//
//   - Unstoppable (rule of the square): adding it at 250 costs
//     **-0.66 +/- 0.34 per position, CI [-1.32,-0.00]** (SIG-, and -0.78
//     +/- 0.36 together with RookBehind); at 80 it is -0.46 +/- 0.31 (same
//     sign, not significant). Mechanism: the bonus flips with the side to
//     move (the defender's free tempo), making the leaf eval
//     tempo-dependent beyond Tempo — poison for alpha-beta — and it is
//     redundant with the search, which sees a 4-move pawn race directly.
//   - RookBehind (Tarrasch): **-0.10 +/- 0.15, CI [-0.40,+0.20]** — a
//     genuine null. Dropped for COST, not strength: it is the only term
//     needing a board walk, and it is the only reason the asm design would
//     need a per-passer file scan.
//
// Honest scope limit: of the +52.0 total match points, only **+24.5 come
// from the 11 objectively WON positions**; +11.5 comes from drawn and +16.0
// from lost ones, i.e. from out-playing a knowledge-removed twin rather
// than from conversion technique. See the class decomposition in
// TestEndgameConversion.
var DefaultEndgame = EndgameParams{
	Enable:       true,
	PhaseMax:     6,
	KingCent:     8,
	KingPawn:     6,
	Pass:         [8]int{0, 0, 10, 20, 40, 60, 100, 0},
	PassKingOur:  6,
	PassKingThem: 4,
	KingAhead:    15,
	Unstoppable:  0, // screened NEGATIVE (see above)
	RookBehind:   0, // screened NEUTRAL (see above)
}

// egPassTable is DefaultEndgame.Pass, kept separate so the CLI's "pass:N"
// scaling and the ablation tests have a stable reference even after the
// default is retuned.
var egPassTable = [8]int{0, 0, 10, 20, 40, 60, 100, 0}

// EndgameDesigned is the FULL designed set including the two terms that
// screened out. It exists only so the rejected terms stay exercised by
// TestEndgameSanity and re-measurable; never use it as a default.
var EndgameDesigned = EndgameParams{
	Enable:       true,
	PhaseMax:     6,
	KingCent:     8,
	KingPawn:     6,
	Pass:         egPassTable,
	PassKingOur:  6,
	PassKingThem: 4,
	KingAhead:    15,
	Unstoppable:  250,
	RookBehind:   20,
}

// egFeatures holds the one-pass scan the endgame terms share: the same
// per-file pawn bytes pawnterm derives, plus the piece counts the square
// rule needs.
type egFeatures struct {
	pw, pb           [8]bool // own pawn on this file
	pwmax, pbmin     [8]int  // most-advanced pawn rank per file (white: max, black: min)
	pwmin, pbmax     [8]int  // least-advanced (the blocking test's operand)
	nPawns           int
	piecesW, piecesB int // non-king non-pawn pieces
	kpDistW, kpDistB int // king Chebyshev distance to the nearest pawn
}

// egScan does the single piece-list pass, filling the caller's buffer (the
// Engine keeps one, so a gated eval does not allocate: this runs at every
// endgame leaf).
func egScan(f *egFeatures, p *Position, wk, bk byte) *egFeatures {
	*f = egFeatures{kpDistW: 99, kpDistB: 99}
	for i := range 8 {
		f.pwmin[i], f.pbmin[i] = 15, 15
	}
	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		typ := p.Board[sq] & TypeMask
		white := slot < 16
		switch typ {
		case King:
			continue
		case Pawn:
			file := int(sq & 7)
			rank := int(sq >> 4)
			f.nPawns++
			if d := cheb(wk, sq); d < f.kpDistW {
				f.kpDistW = d
			}
			if d := cheb(bk, sq); d < f.kpDistB {
				f.kpDistB = d
			}
			if white {
				f.pw[file] = true
				if rank > f.pwmax[file] {
					f.pwmax[file] = rank
				}
				if rank < f.pwmin[file] {
					f.pwmin[file] = rank
				}
			} else {
				f.pb[file] = true
				if rank > f.pbmax[file] {
					f.pbmax[file] = rank
				}
				if rank < f.pbmin[file] {
					f.pbmin[file] = rank
				}
			}
		default:
			if white {
				f.piecesW++
			} else {
				f.piecesB++
			}
		}
	}
	return f
}

// egEval returns the white-POV endgame-technique bonus (positive helps
// white), or 0 when the phase gate is not met. Only ever called when
// e.EG.on(), so the OFF path is byte-identical to the shipped eval.
func (e *Engine) egEval() int {
	t := &e.EG
	p := &e.Pos
	if p.Phase > t.PhaseMax {
		return 0
	}
	wk := p.PieceSq[0]
	bk := p.PieceSq[16]
	// A pseudo-legal node can have "captured" a king (the asm's illegal-
	// move refutation); the score there is irrelevant.
	if wk == NoSq || bk == NoSq {
		return 0
	}
	if e.cyc {
		e.Cyc.EGEvals++
		e.Cyc.Est += uint64(e.Costs.EGTerm)
	}
	f := egScan(&e.egBuf, p, wk, bk)
	score := 0

	// (1) King activity.
	if t.KingCent != 0 {
		score += t.KingCent * (mopupCMD[bk] - mopupCMD[wk])
	}
	if t.KingPawn != 0 && f.nPawns > 0 {
		score += t.KingPawn * (f.kpDistB - f.kpDistW)
	}

	// (2)-(4) Per-passer technique. The passed test is pawnterm's exactly:
	// only the most-advanced pawn per file, blocked by an enemy pawn at
	// rank >= r (white) on files f-1..f+1.
	for file := range 8 {
		if f.pw[file] {
			r := f.pwmax[file]
			if egMax3(&f.pbmax, file) < r {
				score += e.egPasser(f, true, file, r, wk, bk)
			}
		}
		if f.pb[file] {
			r := f.pbmin[file]
			if egMin3(&f.pwmin, file) > r {
				score -= e.egPasser(f, false, file, r, bk, wk)
			}
		}
	}
	return score
}

// egPasser scores one passer from ITS OWNER's POV (the caller negates for
// black). ourK/theirK are already owner-relative. r is the pawn's rank
// (absolute); white advances up, black down.
func (e *Engine) egPasser(f *egFeatures, white bool, file, r int, ourK, theirK byte) int {
	t := &e.EG
	p := &e.Pos
	// Advancement index, matching Weights.Passed.
	adv := r
	if !white {
		adv = 7 - r
	}
	score := t.Pass[adv]

	// Square directly in front of the pawn (its next step).
	fr := r + 1
	if !white {
		fr = r - 1
	}
	if fr >= 0 && fr <= 7 {
		front := byte(fr<<4 | file)
		if t.PassKingOur != 0 {
			score += t.PassKingOur * (4 - cheb(ourK, front))
		}
		if t.PassKingThem != 0 {
			score += t.PassKingThem * (cheb(theirK, front) - 4)
		}
	}

	// (3) K+P basics: our king ahead of our own passer (within a file) is
	// the key-square proxy; our king directly behind it on the same file
	// blocks its own pawn.
	if t.KingAhead != 0 {
		kr := int(ourK >> 4)
		kf := int(ourK & 7)
		df := kf - file
		if df < 0 {
			df = -df
		}
		ahead := kr > r
		if !white {
			ahead = kr < r
		}
		switch {
		case ahead && df <= 1:
			score += t.KingAhead
		case !ahead && df == 0:
			score -= t.KingAhead
		}
	}

	// (2) Rule of the square: only the defending KING can stop it, the
	// path is clear, and the king is outside the square.
	if t.Unstoppable != 0 {
		theirPieces := f.piecesB
		if !white {
			theirPieces = f.piecesW
		}
		if theirPieces == 0 && egPathClear(p, white, file, r) {
			// Steps to promote (a pawn still on its start rank may double).
			var steps, promoRank int
			if white {
				steps, promoRank = 7-r, 7
				if r == 1 {
					steps = 5
				}
			} else {
				steps, promoRank = r, 0
				if r == 6 {
					steps = 5
				}
			}
			kd := cheb(theirK, byte(promoRank<<4|file))
			// The defender gets a free tempo when it is their move.
			slack := 0
			ownerToMove := (p.Side == 0) == white
			if !ownerToMove {
				slack = 1
			}
			if kd > steps+slack {
				score += t.Unstoppable
			}
		}
	}

	// (4) Rook behind the passer (Tarrasch): the first piece found walking
	// the file backwards from the pawn.
	if t.RookBehind != 0 {
		step := -16
		if !white {
			step = 16
		}
		for sq := int(r<<4|file) + step; sq >= 0 && sq <= 0x77; sq += step {
			pc := p.Board[sq]
			if pc == 0 {
				continue
			}
			if pc&TypeMask == Rook {
				ourRook := (pc&ColorMask == 0) == white
				if ourRook {
					score += t.RookBehind
				} else {
					score -= t.RookBehind
				}
			}
			break
		}
	}
	return score
}

// egPathClear reports whether every square strictly ahead of the pawn on
// its own file, up to and including the promotion square, is empty.
func egPathClear(p *Position, white bool, file, r int) bool {
	if white {
		for rr := r + 1; rr <= 7; rr++ {
			if p.Board[rr<<4|file] != 0 {
				return false
			}
		}
		return true
	}
	for rr := r - 1; rr >= 0; rr-- {
		if p.Board[rr<<4|file] != 0 {
			return false
		}
	}
	return true
}

func egMax3(a *[8]int, x int) int {
	m := a[x]
	if x > 0 && a[x-1] > m {
		m = a[x-1]
	}
	if x < 7 && a[x+1] > m {
		m = a[x+1]
	}
	return m
}

func egMin3(a *[8]int, x int) int {
	m := a[x]
	if x > 0 && a[x-1] < m {
		m = a[x-1]
	}
	if x < 7 && a[x+1] < m {
		m = a[x+1]
	}
	return m
}

// EGEval exposes the white-POV endgame-technique sum for a future asm
// parity harness (the analogue of ExtraEval/mopupEval's exposure).
func (e *Engine) EGEval() int { return e.egEval() }

// ASM PORT SKETCH (FT2_EGTECH, not yet ported — the mirror screen is the
// go/no-go). The shipped set is KingCent 8, KingPawn 6, Pass{0,0,10,20,40,
// 60,100,0}, PassKingOur 6 / PassKingThem 4, KingAhead 15, gate phase <= 6.
//
// Entry: extend the existing FT2_MOPUP hook in eval. Both terms share the
// same gate, so ONE compare covers both:
//
//	LDA PHASE : CMP #7 : BCS done      ; ~7 cycles in the middlegame
//
// Data it needs, all already maintained:
//   - PHASE (zp byte, incremental).
//   - WKSQ/BKSQ (PieceSq[0]/[16] equivalents; the mop-up already loads them).
//   - PWBITS/PBBITS: per-file pawn presence bitmaps, kept incrementally by
//     pawnterm. The per-file MOST-ADVANCED rank is NOT kept; pawnterm
//     recomputes it in its own scan, so the cheapest port shares that scan:
//     have pawnterm stash PWMAX[8]/PBMIN[8] (16 B) in its existing per-file
//     loop, at zero extra cost, and let the endgame term read them. The
//     endgame term then needs NO piece-list pass of its own — this is the
//     one design decision that makes the port cheap (~120 cyc, not ~440).
//   - CMD table: the mop-up's existing 64-byte MOPCMD.
//
// Per term:
//   - KingCent: LDA MOPCMD,X(bk) : SEC : SBC MOPCMD,Y(wk) -> A in -6..6;
//     x8 is three ASLs of the magnitude, sign restored. ~20 cycles.
//   - KingPawn: needs the nearest-pawn Chebyshev distance per king. Cheapest
//     6502 form: walk the 8 PWMAX/PBMIN file entries (files with a pawn),
//     computing max(|df|,|dr|) with the standard two-subtract/compare, and
//     keep the min. 8 iterations x ~18 cycles = ~150 worst case, ~90 typical
//     (empty files skipped via the PWBITS/PBBITS bit test). x6 = ASL+ASL+ADD.
//   - Pass table: a 16-byte EGPASSW table (8 per color is not needed —
//     index by advancement, same as PASSEDBONUS). Read inside the SAME
//     per-file passed test pawnterm already does: pawnterm computes the
//     passed verdict, so the endgame top-up is one extra indexed LDA/ADC per
//     passer. ~12 cycles per passer.
//   - PassKingOur/Them: per passer, two Chebyshev distances to the front
//     square (which pawnterm has as rank+1 on the same file). ~36 cycles per
//     passer for both, weights x6/x4 = shift+add.
//   - KingAhead: per passer, a rank compare and |file diff| <= 1 test on the
//     already-loaded king square. ~15 cycles per passer.
//
// Total cost: ~120 cycles + ~65 per passer on eval calls where the gate
// fires; ZERO (7 cycles) in the middlegame. Against Costs.Eval = 872 that
// is a ~15-25% eval surcharge in endgames only — comfortably under the 438
// (EvalTermsCost(2)) the mirror screen charged, so the screened Elo is a
// PESSIMISTIC bound on the ported feature.
//
// Space: ~16 B of new tables (EGPASSW) + 16 B of zp/absolute scratch
// (PWMAX/PBMIN) + ~250-350 B of code. The image had 278 B headroom at the
// last audit, so the port needs a feature-audit slot (the FT_ROOKX removal
// precedent) or the PWMAX/PBMIN sharing to be done as part of pawnterm.
//
// Two designed terms are deliberately NOT in the port: Unstoppable (rule of
// the square) measured NEGATIVE and RookBehind NEUTRAL — see DefaultEndgame.
// Dropping them also removes the only board-walking loop, which is what
// keeps the asm cost at ~120 cycles.
