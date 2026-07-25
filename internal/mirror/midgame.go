package mirror

import "math/bits"

// MIDDLEGAME eval terms (task: the loss diagnosis of 2026-07-25 found the
// two largest loss buckets are POSITIONAL SQUEEZE (42%) and KING-SAFETY
// COLLAPSE (18%), and it separated them by mechanism:
//
//   - squeeze: blindGap median ~59cp, i.e. our shallow eval AGREES with a
//     5x-deeper oracle all the way down. Depth cannot fix it; the missing
//     thing is judgment.
//   - king safety: blindGap median 301 (15/17 >= 200) — the deep search
//     SEES the attack and the shallow one does not. A STATIC penalty is
//     unusually effective here because it needs no depth at all.
//
// What the eval already has (and this file must NOT duplicate): tapered
// PSQT (every term that is a pure function of ONE piece's square is
// already there and Texel-tuned), pawnterm's doubled/isolated/passed +
// a 3-file king-shield COUNT on the back rank only, tempo, and the
// phase-gated endgame set (endgame.go) + mop-up (mopup.go).
//
// Design filter used to pick terms: only RELATIONAL terms (piece x pawn
// structure, piece x enemy king) can add anything, because any per-square
// term is already in the PSQT. And per the FT_ROOKX rejection (rook file /
// doubled rooks / blockade: −19 ± 33 at 3.97% cycle cost), the terms must
// avoid that class and must be derivable from state we already maintain
// (per-file pawn bitmasks, the piece list, PHASE) — no ray walks, no
// movegen, no attacked() calls.
//
// PHASE GATE: everything here is silent when Pos.Phase < PhaseMin
// (default 7), which is exactly ABOVE the endgame terms' and mop-up's
// gate (Phase <= 6). The two sets are therefore disjoint by construction
// — proven by TestMidNoEndgameLeak.
type MidParams struct {
	Enable bool
	// PhaseMin gates the whole file: silent below it. Phase counts
	// N=1,B=1,R=2,Q=4 per side (max 24); the endgame set uses Phase <= 6,
	// so PhaseMin = 7 makes the two disjoint.
	PhaseMin int

	// ---- KING SAFETY -------------------------------------------------
	// KSAtk maps ATTACK UNITS in the king's neighbourhood to a penalty.
	// Units are summed over the enemy's N/B/R/Q by ksUnit weight
	// {N:2,B:2,R:3,Q:5}, full weight at Chebyshev distance <= 2 from the
	// king, half weight at distance 3, zero beyond. The table is the
	// classic superlinear king-danger curve: one attacker is nothing, four
	// is a mating attack. Indexed 0..15 (units clipped).
	KSAtk [16]int
	// KSDefend subtracts HALF the same unit sum for our OWN N/B/R/Q near
	// our own king (floored at 0) before the table lookup: a defended king
	// is not in danger.
	KSDefend bool
	// KSOpen is charged per file of the king's 3-file zone with no own pawn
	// in front of the king, and KSFullOpen additionally when that file has
	// no pawn of EITHER colour (a fully open file at the king).
	KSOpen     int
	KSFullOpen int
	// KSGap is charged per rank of distance between the king and its
	// nearest own pawn ahead on each zone file (clipped to 3): the shield
	// has marched away. pawnterm only counts shield pawns (present/absent)
	// and only on the back rank.
	KSGap int
	// KSPawn is the attack-unit weight of an ENEMY PAWN within Chebyshev 2
	// of the king (a pawn storm / a wedge next to the king). Cheap: the
	// pawn loop already computes nothing else per pawn.
	KSPawn int
	// KSExposed is charged per unit of max(0, 3 - CMD[king]) (CMD is the
	// mop-up's centre-manhattan table, 0 in the centre, 6 in a corner) but
	// ONLY while the enemy still has heavy material: full weight with an
	// enemy queen, HALF with rooks but no queen. This is the direct answer to
	// the diagnosis's king-safety FENs, where the taper's ENDGAME king
	// PSQT (which rewards centralization) is already ~2/3 of the king's
	// score at phase 7-8 while a queen is still on the board.
	KSExposed int

	// ---- POSITIONAL (the squeeze bucket) -----------------------------
	// OutpostN/OutpostB: a knight/bishop on the 4th-6th rank (relative)
	// that is PROTECTED by an own pawn and that NO enemy pawn can ever
	// attack (no enemy pawn on either adjacent file ahead of it). Pure
	// bitmask algebra on the per-file pawn rank masks.
	OutpostN int
	OutpostB int
	// Backward: own pawn whose neighbours are ALL more advanced and whose
	// advance square is controlled by an enemy pawn — it can never be
	// defended by a pawn again. Bitmask-derived.
	Backward int
	// Phalanx: own pawn with an own pawn beside it or defending it
	// (adjacent file, same or one lower rank). Bitmask-derived.
	Phalanx int
	// BadBishop: per bishop, weight * (own pawns on the bishop's OWN
	// colour complex − own pawns on the other complex). Signed, so a good
	// bishop is a bonus; centred so it adds no constant.
	BadBishop int
	// BlockedCtr: own d/e pawn on its 2nd or 3rd rank with an own NON-PAWN
	// piece standing directly in front of it.
	BlockedCtr int
}

func (t *MidParams) on() bool { return t.Enable }

// ksUnit is the attack-unit weight per piece type (index = TypeMask
// value). Pawns and kings contribute nothing.
var ksUnit = [8]int{Knight: 2, Bishop: 2, Rook: 3, Queen: 5}

// DefaultMid is the hand-set weight set for BOTH groups (see MidKingSafety
// / MidPositional for the single-group screens).
//
// SCREENED — DO NOT PORT (docs/results.md 2026-07-25, cycle-budget self-play
// at -cbudget 143000000, asm-matched 0x1f, ON vs OFF):
//
//	king safety, tax 657 (11.5% of all cycles)   -25 +/- 13  (2000 g)
//	king safety, UNTAXED                         -19 +/- 13  (2000 g)
//	  ... shield/open/exposed only, UNTAXED      -15 +/- 13  (2000 g)
//	  ... attacker table + pawn storm, UNTAXED    +2 +/- 13  (2000 g)
//	positional, tax 438 (7.9%), pooled 2 seeds   +12.9 +/- 7.3 (6000 g)
//	  ... pawn-only half (Backward+Phalanx), free  -2.6 +/- 7.3 (6000 g)
//	  ... piece-only half, tax 438                 +1.9 +/- 7.3 (6000 g)
//	both groups, tax 876 (14.7%)                 -28 +/- 13  (2000 g)
//
// The king-safety group loses with its cost set to ZERO, so the knowledge is
// wrong, not the price. The positional group's +12.9 is not reproduced by
// either of its halves (-2.6 and +1.9 at the same 6000-game precision),
// which is why the recorded verdict is "unconfirmed", not "port".
var DefaultMid = MidParams{
	Enable:     true,
	PhaseMin:   7,
	KSAtk:      [16]int{0, 2, 8, 18, 32, 50, 72, 98, 128, 162, 200, 242, 288, 338, 392, 450},
	KSDefend:   true,
	KSOpen:     12,
	KSFullOpen: 8,
	KSGap:      5,
	KSPawn:     1,
	KSExposed:  14,
	OutpostN:   16,
	OutpostB:   8,
	Backward:   8,
	Phalanx:    5,
	BadBishop:  3,
	BlockedCtr: 12,
}

// MidKingSafety is DefaultMid with the positional group zeroed (group A of
// the screen).
var MidKingSafety = MidParams{
	Enable:     true,
	PhaseMin:   7,
	KSAtk:      DefaultMid.KSAtk,
	KSDefend:   true,
	KSOpen:     12,
	KSFullOpen: 8,
	KSGap:      5,
	KSPawn:     1,
	KSExposed:  14,
}

// MidPositional is DefaultMid with the king-safety group zeroed (group B).
var MidPositional = MidParams{
	Enable:     true,
	PhaseMin:   7,
	OutpostN:   16,
	OutpostB:   8,
	Backward:   8,
	Phalanx:    5,
	BadBishop:  3,
	BlockedCtr: 12,
}

// midFeatures is the shared single-pass scan: per-file pawn RANK bitmasks
// (bit r set = a pawn of that colour on rank r of that file — the natural
// 6502 form, 8 bytes per colour, and an incremental extension of the
// PWBITS/PBBITS presence bytes pawnterm already maintains), pawn counts by
// square colour, queen counts, and the king attack/defence unit sums.
type midFeatures struct {
	pr   [2][8]uint8 // [colour][file] rank bitmask
	cp   [2][2]int   // [colour][square colour] pawn count
	nq   [2]int      // queens per colour
	nr   [2]int      // rooks per colour
	atk  [2]int      // attack units against [colour]'s king
	def  [2]int      // defence units around [colour]'s king
	nb   [2][10]byte // knight/bishop squares per colour (for outposts)
	nbT  [2][10]byte // ... their type
	nbN  [2]int
	pawn [2]int // pawn count per colour
}

// midEval returns the white-POV middlegame-term sum (positive helps
// white), or 0 below the phase gate. Only ever called when e.Mid.on(), so
// the OFF path is byte-identical to the shipped eval.
func (e *Engine) midEval() int {
	t := &e.Mid
	p := &e.Pos
	if p.Phase < t.PhaseMin {
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
		e.Cyc.MidEvals++
		e.Cyc.Est += uint64(e.Costs.MidTerm)
	}
	f := &e.midBuf
	*f = midFeatures{}
	king := [2]byte{wk, bk}

	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		typ := p.Board[sq] & TypeMask
		c := 0
		if slot >= 16 {
			c = 1
		}
		switch typ {
		case King:
			continue
		case Pawn:
			file, rank := int(sq&7), int(sq>>4)
			f.pr[c][file] |= 1 << uint(rank)
			f.cp[c][(rank+file)&1]++
			f.pawn[c]++
			if t.KSPawn != 0 && cheb(sq, king[1-c]) <= 2 {
				f.atk[1-c] += t.KSPawn
			}
			continue
		case Queen:
			f.nq[c]++
		case Rook:
			f.nr[c]++
		case Knight, Bishop:
			if n := f.nbN[c]; n < len(f.nb[c]) {
				f.nb[c][n], f.nbT[c][n] = sq, typ
				f.nbN[c]++
			}
		}
		// King-safety units: this piece attacks the ENEMY king's zone and
		// defends its OWN king's zone.
		if u := ksUnit[typ]; u != 0 {
			f.atk[1-c] += ksScale(u, cheb(sq, king[1-c]))
			f.def[c] += ksScale(u, cheb(sq, king[c]))
		}
	}

	score := 0
	// King safety: a penalty for the side whose king is in danger.
	if t.KSAtk != [16]int{} || t.KSOpen|t.KSFullOpen|t.KSGap|t.KSExposed != 0 {
		score -= e.midKingDanger(f, wk, 0)
		score += e.midKingDanger(f, bk, 1)
	}
	// Positional terms (white minus black).
	score += e.midPositional(f)
	return score
}

// ksScale applies the distance taper to an attack-unit weight: full inside
// the 2-square king zone, half at distance 3, nothing beyond.
func ksScale(u, d int) int {
	switch {
	case d <= 2:
		return u
	case d == 3:
		return u >> 1
	}
	return 0
}

// midKingDanger returns the penalty (>= 0 in the usual case) for the king
// of colour c standing on square k.
func (e *Engine) midKingDanger(f *midFeatures, k byte, c int) int {
	t := &e.Mid
	units := f.atk[c]
	if t.KSDefend {
		units -= f.def[c] >> 1
		if units < 0 {
			units = 0
		}
	}
	if units > 15 {
		units = 15
	}
	pen := t.KSAtk[units]

	kf, kr := int(k&7), int(k>>4)
	lo, hi := kf-1, kf+1
	if lo < 0 {
		lo = 0
	}
	if hi > 7 {
		hi = 7
	}
	for file := lo; file <= hi; file++ {
		own, opp := f.pr[c][file], f.pr[1-c][file]
		// Own pawns strictly AHEAD of the king on this file.
		var ahead uint8
		if c == 0 {
			ahead = own >> uint(kr+1) << uint(kr+1)
		} else {
			ahead = own & (1<<uint(kr) - 1)
		}
		if ahead == 0 {
			pen += t.KSOpen
			if own == 0 && opp == 0 {
				pen += t.KSFullOpen
			}
			continue
		}
		var gap int
		if c == 0 {
			gap = bits.TrailingZeros8(ahead) - kr - 1
		} else {
			gap = kr - (bits.Len8(ahead) - 1) - 1
		}
		if gap > 3 {
			gap = 3
		}
		pen += t.KSGap * gap
	}

	// Exposure: only dangerous while the enemy has heavy material (full
	// weight with a queen, half with rooks only).
	if t.KSExposed != 0 {
		if x := 3 - mopupCMD[k]; x > 0 {
			switch {
			case f.nq[1-c] > 0:
				pen += t.KSExposed * x
			case f.nr[1-c] > 0:
				pen += t.KSExposed * x >> 1
			}
		}
	}
	return pen
}

// midPositional returns the white-POV sum of the squeeze-bucket terms.
func (e *Engine) midPositional(f *midFeatures) int {
	t := &e.Mid
	p := &e.Pos
	score := 0

	// Per-file pawn terms: backward, phalanx, blocked centre pawn.
	if t.Backward|t.Phalanx|t.BlockedCtr != 0 {
		for c := range 2 {
			sign := 1
			if c == 1 {
				sign = -1
			}
			for file := range 8 {
				m := f.pr[c][file]
				if m == 0 {
					continue
				}
				var nbOwn, nbOpp uint8
				if file > 0 {
					nbOwn |= f.pr[c][file-1]
					nbOpp |= f.pr[1-c][file-1]
				}
				if file < 7 {
					nbOwn |= f.pr[c][file+1]
					nbOpp |= f.pr[1-c][file+1]
				}
				for m != 0 {
					r := bits.TrailingZeros8(m)
					m &= m - 1
					// Relative geometry: white advances up, black down.
					fwd, back := r+1, r-1
					if c == 1 {
						fwd, back = r-1, r+1
					}
					if t.Phalanx != 0 && nbOwn&(bit8(r)|bit8(back)) != 0 {
						score += sign * t.Phalanx
					}
					if t.Backward != 0 && nbOwn != 0 {
						// All own neighbours strictly more advanced ...
						var behindOrLevel uint8
						if c == 0 {
							behindOrLevel = nbOwn & (bit8(r+1) - 1)
						} else {
							behindOrLevel = nbOwn >> uint(r) << uint(r)
						}
						// ... and the advance square is controlled by an
						// enemy pawn (enemy pawn one further on a
						// neighbour file).
						if behindOrLevel == 0 {
							ctl := fwd + 1
							if c == 1 {
								ctl = fwd - 1
							}
							if nbOpp&bit8(ctl) != 0 {
								score -= sign * t.Backward
							}
						}
					}
					if t.BlockedCtr != 0 && (file == 3 || file == 4) {
						home := (c == 0 && (r == 1 || r == 2)) ||
							(c == 1 && (r == 6 || r == 5))
						if home && fwd >= 0 && fwd <= 7 {
							pc := p.Board[fwd<<4|file]
							if pc != 0 && pc&TypeMask != Pawn &&
								int(pc&ColorMask) == c*int(ColorMask) {
								score -= sign * t.BlockedCtr
							}
						}
					}
				}
			}
		}
	}

	// Outposts (knights and bishops).
	if t.OutpostN|t.OutpostB != 0 {
		for c := range 2 {
			sign := 1
			if c == 1 {
				sign = -1
			}
			for i := range f.nbN[c] {
				sq := f.nb[c][i]
				file, rank := int(sq&7), int(sq>>4)
				rel := rank
				if c == 1 {
					rel = 7 - rank
				}
				if rel < 3 || rel > 5 {
					continue
				}
				var own, opp uint8
				if file > 0 {
					own |= f.pr[c][file-1]
					opp |= f.pr[1-c][file-1]
				}
				if file < 7 {
					own |= f.pr[c][file+1]
					opp |= f.pr[1-c][file+1]
				}
				// Protected by an own pawn one rank behind ...
				var prot, attackable uint8
				if c == 0 {
					prot = own & bit8(rank-1)
					attackable = opp >> uint(rank+1) << uint(rank+1)
				} else {
					prot = own & bit8(rank+1)
					attackable = opp & (bit8(rank) - 1)
				}
				if prot == 0 || attackable != 0 {
					continue
				}
				w := t.OutpostN
				if f.nbT[c][i] == Bishop {
					w = t.OutpostB
				}
				score += sign * w
			}
		}
	}

	// Bad bishop: own pawns on the bishop's own colour complex, net of the
	// other complex.
	if t.BadBishop != 0 {
		for c := range 2 {
			sign := 1
			if c == 1 {
				sign = -1
			}
			for i := range f.nbN[c] {
				if f.nbT[c][i] != Bishop {
					continue
				}
				sq := f.nb[c][i]
				col := (int(sq>>4) + int(sq&7)) & 1
				score -= sign * t.BadBishop * (f.cp[c][col] - f.cp[c][1-col])
			}
		}
	}
	return score
}

// bit8 is 1<<r for r in 0..7, and 0 outside (so rank arithmetic that runs
// off the board contributes nothing).
func bit8(r int) uint8 {
	if r < 0 || r > 7 {
		return 0
	}
	return 1 << uint(r)
}

// MidEval exposes the white-POV middlegame-term sum for tests and a future
// asm parity harness (the analogue of EGEval/ExtraEval).
func (e *Engine) MidEval() int { return e.midEval() }

// ASM PORT SKETCH + HONEST COST MODEL (FT2_MIDTERM; nothing is ported —
// the mirror screen is the go/no-go, and the image had only 278 B free at
// the last audit, so cheapness is a hard gate).
//
// Gate (both groups share it): LDA PHASE : CMP #7 : BCC done — 7 cycles,
// and it is the ONLY cost in an endgame leaf. Unlike endgame.go's gate,
// this one FIRES on essentially every middlegame eval call (measured:
// 100% of eval calls in a middlegame search, TestMidCosted), so the tax is
// paid nearly always. That is why the screens below are run at a
// deliberately pessimistic per-call cost.
//
// State it needs, and what is already maintained:
//   - PHASE, WKSQ/BKSQ, the piece list: all present.
//   - Per-file pawn RANK bitmasks (PWRANK[8]/PBRANK[8], 16 B). NOT
//     maintained today; pawnterm's per-file scan can fill them at ~zero
//     extra cost (the same trick the endgame sketch proposes for
//     PWMAX/PBMIN), and pawnterm only runs when the pawn structure
//     changes. With that, the POSITIONAL group needs no pawn pass at all.
//   - A 240-byte Chebyshev-distance table CHEBD indexed by (sqA-sqB+0x77)
//     turns each king-distance into one indexed LDA (~12 cyc) instead of
//     the ~20-cycle two-subtract/abs/max sequence. It is the single
//     biggest lever on the king-safety cost, and it costs 240 B of ROM we
//     do not have — so the honest asm variant is the arithmetic form.
//
// Per-call cycle estimate (arithmetic Chebyshev, no new tables):
//
//	KING SAFETY
//	  piece-list pass, dispatch only                     ~219  (the measured
//	                                                            FT_ROOKX scan)
//	  per non-pawn piece: 1 cheb + weight + add (x ~10)  ~230
//	  per pawn: file-proximity reject, then cheb (x8)     ~60
//	  3 zone files: mask/scan/gap                         ~90
//	  attack-unit table lookup x2, exposure CMD x2        ~50
//	  TOTAL                                              ~650 cyc/call  ->
//	                                                     EvalTermsCost(3)=657
//	POSITIONAL (with PWRANK/PBRANK maintained by pawnterm)
//	  piece-list pass for N/B squares only               ~219
//	  8 files x (backward/phalanx bit algebra)           ~120
//	  outposts, <= 4 per side                             ~60
//	  bad bishop (2 counters kept in the pawn pass)       ~30
//	  TOTAL                                              ~430 cyc/call  ->
//	                                                     EvalTermsCost(2)=438
//	BOTH (one shared pass)                               ~830 cyc/call  ->
//	                                                     EvalTermsCost(4)=876
//
// Against the calibrated Costs.Eval = 872 and the whole-search totals the
// mirror measures, those taxes are 11.5% / 7.9% / 14.7% of ALL cycles
// respectively (TestMidCosted reports the exact fraction). For scale: the
// REJECTED FT_ROOKX eval set cost 3.97% and measured −19 ± 33 Elo. So this
// term class starts three to four times deeper in the hole, which is why
// the screens are also run UNTAXED (cost 0) — an untaxed screen isolates
// the KNOWLEDGE value from the cost, and if the knowledge is worth nothing
// even for free, no optimization pass can save it.
//
// Space, if any group survives: KSAtk is a 16-byte table; the per-file rank
// bitmasks are 16 B of zp/absolute scratch; code is ~400-500 B for the
// king-safety group and ~300-400 B for the positional group. The image has
// 278 B free, so ANY port needs a feature-audit slot first (the FT_ROOKX
// removal precedent).
