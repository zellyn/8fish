package mirror

// Engine holds a position plus all the per-ply search state the asm
// keeps in absolute page-$09-$0D arrays, the TT, and the feature/weight
// configuration. One Engine per goroutine.
type Engine struct {
	Pos      Position
	Features byte
	Weights  Weights
	Seed     byte // eval dither PRNG state; 0 = off
	MaxDepth int
	Nodes    uint64

	// NodeBudget is the per-move hard node cap for SearchBudget's
	// iterative deepening (0 = no cap, i.e. fixed-depth mode). When Nodes
	// reaches it mid-iteration, aborted is set and search() unwinds; the
	// incomplete iteration's result is discarded.
	NodeBudget uint64
	aborted    bool

	// CycleBudget is the per-move hard cap on ESTIMATED 6502 cycles for
	// SearchCycleBudget's iterative deepening (0 = off). It is the cycle-
	// denominated analogue of NodeBudget: when Cyc.Est reaches it mid-
	// iteration, aborted is set and the iteration's partial result is
	// discarded. See cycles.go for the calibrated cost model.
	CycleBudget uint64
	// CycleTrack forces cycle accounting on even without a budget (for
	// calibration/diagnostics); it never changes search behavior.
	CycleTrack bool
	cyc        bool // accounting active this search (CycleBudget!=0||CycleTrack)
	// Costs is the per-operation cycle cost table (defaults to
	// DefaultCycleCosts at NewEngine); Cyc holds the live estimate+counters.
	Costs        CycleCosts
	Cyc          CycleAccount
	attackInMake bool // true while make() computes the child gives-check scan

	// FixFutilityGuard forces the signed-aware RFP/futility guard on
	// (equivalent to Fut.CorrectGuard). Kept for the original guard A/B;
	// prefer Fut for margin experiments.
	FixFutilityGuard bool

	// Fut configures the RFP/forward-futility block (guard + margins).
	Fut FutilityParams

	// LMR/PVS tuning knobs, defaulting to the asm's current rules.
	LMR LMRParams

	// QS-shape knobs. The asm's actual shape is DefaultQS (recap2); the
	// zero value is UNLIMITED QS (the experiment baseline), NOT the asm.
	QS           QSParams
	QSNodes      uint64 // nodes entered at ply >= MaxDepth (evasion included)
	QSCheckNodes uint64 // quiet checking moves searched from QS nodes (task #37)

	// LMP configures late-move (movecount) pruning in the scored move
	// loop (zero value = off).
	LMP LMPParams

	// CheckExt configures check extensions in the MAIN search (zero value =
	// off: a byte-identical no-op). See CheckExtParams. numExt is the running
	// count of check extensions applied on the current root-to-node path (the
	// cap enforcer); it is balanced save/restored around every extended child
	// search and reset to 0 at each iterate.
	CheckExt CheckExtParams
	numExt   int

	// CM configures the countermove heuristic in the five-pass moveLoop
	// (zero value = off: a byte-identical no-op). See countermove.go.
	CM CountermoveParams

	// SEE configures portable losing-capture classification in the
	// five-pass moveLoop (zero value = off: a byte-identical no-op). See
	// see.go. seeDefer holds each ply's deferred losing captures (list
	// order); seeScan suppresses the asm-op attacked() charge during a
	// mode-2 defended scan (its cost is charged via Costs.SEE instead);
	// seeAudit, when set by a test, tallies [fullSEE<0][variant-losing]
	// classification agreement.
	SEE      SEEParams
	seeDefer [MaxPly][]Move
	seeScan  bool
	seeAudit *[2][2]uint64
	// SEEProbeHook, when set (tests/measurement only), is called with every
	// gate-passing capture just before its classification runs — the tap the
	// asm cycle models use to price a design on the true operand
	// distribution. nil (the default) is a byte-identical no-op.
	SEEProbeHook func(m Move)
	// counter is the countermove table [prev-piece-type][prev-to-square] ->
	// refuting (from,to) move. Variant 1 uses only slot [0][to]; variant 2
	// uses [pieceType][to]. Cleared per root move (or kept, per CM.Persist).
	counter [8][128]Move

	// Asp configures aspiration windows at the iterative-deepening root
	// (zero value = off: every ID iteration searches the full window, a
	// byte-identical no-op).
	Asp AspirationParams

	// Improving configures the improving heuristic (zero value = off: a
	// byte-identical no-op). See improving.go. evalStack holds the static
	// eval recorded at each ply (by eval() when the heuristic is active);
	// evalValid marks which plies have a recorded eval this search path.
	Improving ImprovingParams
	evalStack [MaxPly]int
	evalValid [MaxPly]bool

	// Aspiration accounting, cumulative over the engine's lifetime (never
	// reset by newMove, so per-game totals survive to the caller).
	// AspWindows counts iterations that opened a narrow aspiration window;
	// AspFailLow/AspFailHigh count the fails that forced a re-search.
	AspWindows, AspFailLow, AspFailHigh uint64
	// CompletedDepth is the depth of the last COMPLETED ID iteration in the
	// most recent SearchBudget/SearchCycleBudget call (the effective depth
	// the budget bought). 0 if only depth 1 ran.
	CompletedDepth int

	// diag accumulates adaptive effort-management diagnostics over the
	// engine's lifetime (one engine per side per game in self-play), for the
	// time-management screen. See effort.go / SearchTimed.
	diag TimeDiag

	// Ord configures the scored move ordering used when FtSEE/FtHistory
	// are enabled (task #35).
	Ord OrderParams
	// hist is the butterfly history table [side][from][to], keyed by the
	// 0x88 squares. Bumped on quiet fail-high, decayed at SearchFixed.
	hist *[2][128][128]int32

	// KB, when non-nil, adds king-bucketed PSQT deltas to the eval
	// (task #30: piece values depend on the king's file zone). nil =
	// the asm's current single-PSQT eval.
	KB *KBTables

	// Extra holds the experimental cheap eval terms (task #18/#51). Zero
	// value = all off (the asm's current eval). The asm ports the fixed
	// RookTermsAsm subset behind FT_ROOKX.
	Extra EvalTerms

	// Mopup holds the phase-gated endgame mop-up term (drive the losing
	// king to a corner + bring the winning king up). Zero value = OFF: a
	// byte-identical no-op versus the asm's current eval. See mopup.go.
	Mopup MopupParams

	Best      Move // root best move (BESTFROM/BESTTO/BESTFLAGS)
	RootScore int

	// Per-ply state.
	alpha, beta [MaxPly]int
	undo        [MaxPly]undoRec
	hashStk     [MaxPly]uint32
	inChk       [MaxPly]bool
	futile      [MaxPly]bool
	qsKind      [MaxPly]bool
	raised      [MaxPly]bool
	legal       [MaxPly]int
	deltaT      [MaxPly]int
	ttFrom      [MaxPly]byte // TT move for ordering (NoSq = none)
	ttTo        [MaxPly]byte
	ttBF        [MaxPly]byte // best move found (for the TT store)
	ttBT        [MaxPly]byte
	killer      [MaxPly][2]Move
	moves       [MaxPly][]Move

	tt [4096]ttEntry
}

type undoRec struct {
	cap, capSq, castle, ep, piece, from, to, flags byte
	mg, eg, phase, pstruct                         int
	halfmove                                       byte
}

type ttEntry struct {
	verify   uint32 // hash >> 8, 24 bits; 0 with bound 0 = empty
	from, to byte
	score    int16
	// depth<<2 | bound; bound 0 marks an empty entry.
	depthBound byte
}

// LMRParams are the PVS/LMR rule constants (asm search.s smset block).
// Legal-move counts include the move being classified, matching the
// asm's post-increment LEGALCNT compares.
type LMRParams struct {
	LateR1        int  // reduce by 1 when LEGALCNT >= this (asm: cmp #4)
	LateR2        int  // reduce by 2 when LEGALCNT >= this (asm: cmp #7)
	MinRemR1      int  // remaining-depth floor for any reduction (asm: cmp #3)
	MinRemR2      int  // remaining-depth floor for reduce-by-2 (asm: cmp #5)
	ReduceKillers bool // also reduce pass-3 killer quiets (asm: pass 4 only)
	EvasionPVS    bool // zero-window scouts at in-check nodes (asm: yes)
}

// DefaultLMR mirrors the asm's current constants.
var DefaultLMR = LMRParams{LateR1: 4, LateR2: 7, MinRemR1: 3, MinRemR2: 5, EvasionPVS: true}

// FutilityParams configure the reverse-futility-pruning (RFP) and
// forward (leaf) futility block in search(). The asm currently hard-
// codes a single unsigned-compare guard plus two static margins
// (120 @ remaining 1, 250 @ remaining 2). This struct makes both the
// guard and the depth-indexed margins tunable so the corrected guard
// can be re-margined rather than reverted.
type FutilityParams struct {
	// CorrectGuard selects the signed-aware guard (RFP/futility active
	// in every non-mate-zone window). The asm's current guard uses an
	// unsigned compare, so ANY negative alpha or beta silently disables
	// the block — a bug. false reproduces that bug; true is the fix.
	CorrectGuard bool
	// RFP is the reverse-futility margin indexed by remaining depth
	// (RFP[r] used when remaining == r). A zero margin disables RFP at
	// that depth. MaxRem bounds how deep the block is considered.
	RFP    [8]int
	MaxRem int
	// Fut is the forward (leaf) futility margin, applied at remaining 1
	// only (skip quiets when eval + Fut <= alpha). 0 disables it.
	Fut int
}

// DefaultFutility is the adopted scheme (task #34): the corrected
// signed-aware guard with RFP re-margined to 120 @ remaining 1, 500 @
// remaining 2 (the leaf-futility margin stays 120). The old shipped
// behavior was the unsigned-compare guard with RFP 120/250; enabling
// the correct guard at 250 over-prunes remaining-2 nodes in negative
// windows for −43 Elo, but at 500 it is neutral-to-positive
// (+4 ± 14 over 1600 depth-6 games) while keeping −16.9% nodes. This is
// the asm port target; we do not re-enshrine the unsigned-compare bug.
var DefaultFutility = FutilityParams{
	CorrectGuard: true,
	RFP:          [8]int{0, 120, 500},
	MaxRem:       2,
	Fut:          120,
}

// LMPParams configure late-move / movecount pruning (LMP) in the scored
// ordered move loop. At a shallow, non-PV, not-in-check node whose alpha
// is outside the mate zone, once the count of already-searched legal
// moves reaches threshold(d) = Base + Mult*d + Quad*d*d (d = remaining
// depth), the remaining QUIET moves are skipped without being searched.
// Captures, promotions and the TT move are always searched. This is a
// pure per-move counter compare — no tables, no per-node allocation — so
// it is cheap enough for a 6502 port (the count already lives in the
// asm's LEGALCNT).
//
// Zero value (Dmax == 0) disables LMP entirely.
type LMPParams struct {
	// Dmax bounds how deep LMP applies: active only when remaining depth
	// d satisfies 1 <= d <= Dmax. 0 disables LMP.
	Dmax int
	// threshold(d) = Base + Mult*d + Quad*d*d. Standard schemes:
	// Base=3,Mult=2,Quad=0 gives 3+2d; Base=3,Mult=0,Quad=1 gives 3+d*d.
	Base, Mult, Quad int
	// ExemptKillers keeps killer-class quiets searched even past the
	// threshold (they are the highest-scoring quiets and cheap to spare).
	ExemptKillers bool
	// KeepChecks keeps quiet moves that give check searched (requires the
	// move to be made to know it gives check, so it only saves the child
	// search, not the make/unmake — less 6502-portable than the default).
	KeepChecks bool
}

// lmpThreshold returns Base + Mult*d + Quad*d*d for remaining depth d.
func (p *LMPParams) lmpThreshold(d int) int {
	return p.Base + p.Mult*d + p.Quad*d*d
}

// CheckExtParams configures check extensions in the MAIN search (never in
// quiescence). When a move just made gives check, the child is searched one
// ply DEEPER (remaining depth is not decremented for that move) so a forcing
// checking sequence is seen past the normal horizon. This is the standard
// check extension.
//
// Explosion guard: MaxExt caps the number of check extensions applied along a
// single root-to-leaf path (numExt in Engine). A long checking sequence can
// then deepen the search by at most MaxExt plies, so the tree cannot blow up
// without bound. LMR interaction: a checking move (inChk[ply+1]) is already
// never depth-reduced by the LMR gate (which requires !inChk[ply+1]), so an
// extended move is never simultaneously reduced — the two are mutually
// exclusive by construction. Futility interaction: the extension is applied
// to the CHILD's remaining depth; the parent's RFP/futility pruning (which
// keys off the parent node, not in check) is unaffected.
//
// Under a cycle budget the extension carries no direct per-node cost knob: it
// simply produces a bigger tree, whose extra nodes are charged by the normal
// chargeNode accounting — so the budget taxes it honestly and automatically.
//
// Zero value (MaxExt == 0) disables check extensions: a byte-identical no-op.
type CheckExtParams struct {
	// MaxExt caps check extensions along a root-to-leaf path. 0 = off.
	MaxExt int
	// CapturesOnly, when set, extends only checking moves that are also
	// captures (a tighter variant to curb explosion — a checking capture is
	// the most likely to be tactically forcing). Off = extend every check.
	CapturesOnly bool
}

// on reports whether check extensions are enabled.
func (c *CheckExtParams) on() bool { return c.MaxExt > 0 }

// AspPolicy selects how an aspiration-window fail-low/high is widened for
// the re-search.
type AspPolicy byte

const (
	// AspFull widens BOTH bounds to the full (-Inf, Inf) window on the
	// first fail — a single guaranteed-terminal re-search.
	AspFull AspPolicy = iota
	// AspProgressive doubles Delta and re-centers the window on the failing
	// bound each fail, dropping to the full window on the third fail.
	AspProgressive
	// AspAsym widens ONLY the failing bound to full, keeping the other bound
	// at its aspiration value (so a subsequent fail on the other side can
	// still occur and is handled the same way).
	AspAsym
)

// AspirationParams configures aspiration windows at the iterative-deepening
// root. After ID iteration d completes with score s, iteration d+1 opens a
// window (s-Delta, s+Delta) instead of (-Inf, +Inf); on a fail-low/high the
// iteration is re-searched with a widened window per Policy.
//
// The first iteration and any iteration whose seeding score s is in the mate
// zone always use the full window, and any fail that returns a mate-zone
// score immediately re-searches full — a narrow window must never clip a mate
// score. All arithmetic is 16-bit adds/compares plus a shift-doubling of
// Delta: no floats, no tables beyond the constant Delta, so it ports to the
// 6502.
//
// Zero value (Delta == 0) disables aspiration entirely: every iteration uses
// the full window, a byte-identical no-op versus the pre-aspiration search.
type AspirationParams struct {
	// Delta is the initial half-window width in centipawns. 0 disables
	// aspiration.
	Delta int
	// Policy selects the fail-widening scheme (see AspPolicy).
	Policy AspPolicy
}

// on reports whether aspiration windows are enabled.
func (a *AspirationParams) on() bool { return a.Delta > 0 }

// inMateZone reports whether score s sits in either mate zone (a narrow
// window must never be opened around, nor clip, such a score).
func inMateZone(s int) bool { return s >= mateZoneLo || s <= nmateZoneHi }

// QSParams shape the quiescence search. QS ply = PLY - MAXDEPTH.
type QSParams struct {
	// PlyCap: at qs ply >= this, non-evasion nodes return the stand-pat
	// result without generating captures. 0 = no cap.
	PlyCap int
	// RecapAfter: at qs ply >= this, capture nodes consider only
	// captures landing on the previous move's TO square. 0 = off.
	RecapAfter int
	// Checks: generate quiet CHECKING moves (in addition to captures/
	// promotions) at qs ply < this. 0 = off (captures-only QS, the asm's
	// current behavior). Checks=1 adds quiet checks at the first qs ply
	// only; Checks=2 at the first two. A quiet check's child is a normal
	// in-check node (all evasions, mate detection), so a quiet mating or
	// forcing check is now seen in quiescence. Task #37 (Schröder-style).
	Checks int
	// SafeChecks gates the quiet-check generation: skip a checking move
	// whose destination square is attacked by an enemy pawn (cheap "the
	// checker hangs to a pawn" filter). Off = all quiet checks searched.
	SafeChecks bool
}

// DefaultQS is the asm's current quiescence shape: recap-gated at qs ply
// >= 2 (asm gennode hardcodes RecapAfter=2), no ply cap, no quiet checks.
// The zero value QSParams{} is UNLIMITED QS and does NOT model the asm —
// it is the "no shaping" baseline for the QS-shape experiments only. Bare
// NewEngine (and any asm-parity comparison) must use this, exactly as it
// uses DefaultLMR/DefaultFutility rather than the zero values. Getting this
// wrong searches a QS tree up to ~10x the asm's and shifts tactical scores.
var DefaultQS = QSParams{RecapAfter: 2}

// NewEngine returns an engine with all features on and the asm's current
// pstruct weights, LMR rules, and QS shape (recap2).
func NewEngine() *Engine {
	e := &Engine{Features: FtAll, Weights: DefaultWeights, LMR: DefaultLMR, Fut: DefaultFutility, QS: DefaultQS, Costs: DefaultCycleCosts}
	for i := range e.moves {
		e.moves[i] = make([]Move, 0, 128)
	}
	return e
}

// ClearTT empties the transposition table (new game).
func (e *Engine) ClearTT() {
	e.tt = [4096]ttEntry{}
}

// SetPosition installs a position and recomputes the incremental state
// (the asm driver's evalinit).
func (e *Engine) SetPosition(pos *Position) {
	e.Pos = *pos
	e.Pos.Ply = 0
	e.evalinit()
}

// hashPiece xors the Zobrist key for (piece, sq) into the hash.
func (e *Engine) hashPiece(piece, sq byte) {
	e.Pos.Hash ^= zobPiece[kindOf(piece)][sq]
}

// evalinit recomputes accumulators, hash, and PSTRUCT from the board.
func (e *Engine) evalinit() {
	p := &e.Pos
	p.MG, p.EG, p.Phase = 0, 0, 0
	p.Hash = 0
	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		piece := p.Board[sq]
		e.addPiece(piece, sq)
		e.hashPiece(piece, sq)
	}
	if p.Side != 0 {
		p.Hash ^= zobStm
	}
	p.Hash ^= zobCast[p.Castle]
	if p.EpSq != NoSq {
		p.Hash ^= zobEP[p.EpSq&7]
	}
	e.pawnterm() // initial PSTRUCT (clears PDirty)
}

// addPiece/remPiece update MG/EG/Phase (and PDirty for pawns/kings),
// mirroring asm addpiece/rempiece.
func (e *Engine) addPiece(piece, sq byte) {
	p := &e.Pos
	typ := piece & TypeMask
	if typ == Pawn || typ == King {
		p.PDirty = true
	}
	p.Phase += phaseVal[typ]
	if piece&ColorMask != 0 {
		p.MG -= psqtMG[typ][sq^0x70]
		p.EG -= psqtEG[typ][sq^0x70]
	} else {
		p.MG += psqtMG[typ][sq]
		p.EG += psqtEG[typ][sq]
	}
}

func (e *Engine) remPiece(piece, sq byte) {
	p := &e.Pos
	typ := piece & TypeMask
	if typ == Pawn || typ == King {
		p.PDirty = true
	}
	p.Phase -= phaseVal[typ]
	if piece&ColorMask != 0 {
		p.MG += psqtMG[typ][sq^0x70]
		p.EG += psqtEG[typ][sq^0x70]
	} else {
		p.MG -= psqtMG[typ][sq]
		p.EG -= psqtEG[typ][sq]
	}
}

// attacked reports whether sq is attacked by any piece of side
// bySide (0/8), mirroring asm attacked().
func (e *Engine) attacked(sq byte, bySide byte) bool {
	if e.cyc {
		// The in-make gives-check scan is the asm's difference-table update
		// inside make(), not an asm attacked() call; don't charge it here.
		// A SEE mode-2 defended scan is likewise priced via Costs.SEE, not
		// as an asm attacked() op.
		e.chargeAttacked(!e.attackInMake && !e.seeScan)
	}
	p := &e.Pos
	base := int(bySide) << 1 // 0 or 16
	for slot := base; slot < base+16; slot++ {
		from := p.PieceSq[slot]
		if from == NoSq {
			continue
		}
		idx := (int(sq) - int(from) + 0x77) & 0xFF
		bits := attackTab[idx]
		if bits == 0 {
			continue
		}
		typ := p.Board[from] & TypeMask
		if typ == Pawn {
			if bySide == 0 {
				if bits&atkWPawn != 0 {
					return true
				}
			} else if bits&atkBPawn != 0 {
				return true
			}
			continue
		}
		if typeAtkTab[typ]&bits == 0 {
			continue
		}
		if typ == Knight || typ == King {
			return true
		}
		// Slider: walk the ray from attacker toward sq checking blockers.
		delta := int(deltaTab[idx])
		cur := int(from)
		for {
			cur += delta
			if byte(cur) == sq {
				return true
			}
			if p.Board[cur] != 0 {
				break
			}
		}
	}
	return false
}

// pawnAttacks reports whether square sq is attacked by a pawn of side
// bySide (0 = white, 8 = black). Cheap gate for QS SafeChecks: a white
// pawn attacking sq sits on sq-0x0F/sq-0x11; a black pawn on sq+0x0F/
// sq+0x11.
func (e *Engine) pawnAttacks(sq, bySide byte) bool {
	p := &e.Pos
	var offs [2]int
	if bySide == 0 {
		offs = [2]int{-0x0F, -0x11}
	} else {
		offs = [2]int{0x0F, 0x11}
	}
	for _, off := range offs {
		s := int(sq) + off
		if s < 0 || s&0x88 != 0 {
			continue
		}
		pc := p.Board[s]
		if pc != 0 && pc&ColorMask == bySide && pc&TypeMask == Pawn {
			return true
		}
	}
	return false
}

// curInCheck: is the side to move in check?
func (e *Engine) curInCheck() bool {
	p := &e.Pos
	kingSlot := int(p.Side) << 1 // own slot base; king is slot 0
	return e.attacked(p.PieceSq[kingSlot], p.Side^ColorMask)
}

// make plays m, mirroring asm make: undo save, capture removal, 50-move
// clock, board/list/hash/eval updates, castling rights, ep, side flip,
// ply++, child in-check computation, and the PDirty pawnterm refresh.
func (e *Engine) make(m Move) {
	p := &e.Pos
	u := &e.undo[p.Ply]
	u.castle = p.Castle
	u.ep = p.EpSq
	u.from = m.From
	u.to = m.To
	u.flags = m.Flags
	u.mg, u.eg = p.MG, p.EG
	u.phase = p.Phase
	u.halfmove = p.Halfmove
	e.hashStk[p.Ply] = p.Hash
	u.pstruct = p.PStruct

	piece := p.Board[m.From]
	u.piece = piece

	// Capture square: TO, or the pushed-past square for en passant.
	capSq := m.To
	if m.Flags&FlEP != 0 {
		if piece&ColorMask == 0 {
			capSq = m.To - 0x10
		} else {
			capSq = m.To + 0x10
		}
	}
	u.capSq = capSq
	victim := p.Board[capSq]
	u.cap = victim
	if e.cyc {
		e.chargeMake(m, victim)
	}
	if victim != 0 {
		e.hashPiece(victim, capSq)
		e.remPiece(victim, capSq)
		p.Board[capSq] = 0
		p.PieceSq[slotOf(victim)] = NoSq
	}

	// 50-move clock: reset on capture or pawn move.
	if victim != 0 || piece&TypeMask == Pawn {
		p.Halfmove = 0
	} else {
		p.Halfmove++
	}

	// Move the piece (promotion replaces the type bits).
	e.hashPiece(piece, m.From)
	e.remPiece(piece, m.From)
	p.Board[m.From] = 0
	final := piece
	if promo := m.Flags & FlPromo; promo != 0 {
		final = piece&(IndexMask|ColorMask) | promo
	}
	p.Board[m.To] = final
	e.hashPiece(final, m.To)
	e.addPiece(final, m.To)
	p.PieceSq[slotOf(final)] = m.To

	if m.Flags&FlCastle != 0 {
		e.castleRook(m.To)
	}

	// Rights and ep square.
	p.Castle &= castleMask[m.From] & castleMask[m.To]
	if m.Flags&FlDouble != 0 {
		p.EpSq = (m.From + m.To) / 2
	} else {
		p.EpSq = NoSq
	}

	// Hash: castling-rights change, ep change, side to move.
	if u.castle != p.Castle {
		p.Hash ^= zobCast[u.castle]
		p.Hash ^= zobCast[p.Castle]
	}
	if u.ep != p.EpSq {
		if u.ep != NoSq {
			p.Hash ^= zobEP[u.ep&7]
		}
		if p.EpSq != NoSq {
			p.Hash ^= zobEP[p.EpSq&7]
		}
	}
	p.Hash ^= zobStm
	p.Side ^= ColorMask
	p.Ply++

	// Gives-check for the child ply. The asm propagates this from
	// difference tables (F2, verified node-exact against a full scan by
	// FT_CKVERIFY); the full scan is semantically identical. Its cost is
	// booked to make() (attackInMake suppresses the attacked() charge).
	e.attackInMake = true
	e.inChk[p.Ply] = e.curInCheck()
	e.attackInMake = false

	if p.PDirty && e.Features&FtPstruct != 0 {
		e.pawnterm()
	} else {
		p.PDirty = false
	}
}

// castleRook moves the rook for the castle being made; to tells the corner.
func (e *Engine) castleRook(to byte) {
	var rf, rt byte
	switch to {
	case 0x06:
		rf, rt = 0x07, 0x05
	case 0x02:
		rf, rt = 0x00, 0x03
	case 0x76:
		rf, rt = 0x77, 0x75
	default:
		rf, rt = 0x70, 0x73
	}
	p := &e.Pos
	rook := p.Board[rf]
	e.hashPiece(rook, rf)
	e.remPiece(rook, rf)
	e.hashPiece(rook, rt)
	e.addPiece(rook, rt)
	p.Board[rf] = 0
	p.Board[rt] = rook
	p.PieceSq[slotOf(rook)] = rt
}

// unmake undoes the move recorded at Ply-1.
func (e *Engine) unmake() {
	if e.cyc {
		e.Cyc.Unmakes++ // cost folded into chargeMake's paired cost
	}
	p := &e.Pos
	p.Ply--
	u := &e.undo[p.Ply]
	p.Side ^= ColorMask
	p.Castle = u.castle
	p.EpSq = u.ep
	p.MG, p.EG = u.mg, u.eg
	p.Phase = u.phase
	p.Halfmove = u.halfmove
	p.Hash = e.hashStk[p.Ply]
	p.PStruct = u.pstruct

	p.Board[u.to] = 0
	p.Board[u.from] = u.piece
	p.PieceSq[slotOf(u.piece)] = u.from
	if u.flags&FlCastle != 0 {
		e.uncastleRook(u.to)
	}
	if u.cap != 0 {
		p.Board[u.capSq] = u.cap
		p.PieceSq[slotOf(u.cap)] = u.capSq
	}
}

func (e *Engine) uncastleRook(to byte) {
	var rf, rt byte // rook currently at rf, moves back to rt
	switch to {
	case 0x06:
		rf, rt = 0x05, 0x07
	case 0x02:
		rf, rt = 0x03, 0x00
	case 0x76:
		rf, rt = 0x75, 0x77
	default:
		rf, rt = 0x73, 0x70
	}
	p := &e.Pos
	rook := p.Board[rf]
	p.Board[rf] = 0
	p.Board[rt] = rook
	p.PieceSq[slotOf(rook)] = rt
}

// makenull passes the move: only ep, hash, halfmove clock, and side
// change. The child is never in check (null is only tried when not in
// check).
func (e *Engine) makenull() {
	if e.cyc {
		e.chargeMakeNull()
	}
	p := &e.Pos
	u := &e.undo[p.Ply]
	u.from = NoSq // marks this ply's move as a null
	u.ep = p.EpSq
	u.halfmove = p.Halfmove
	e.hashStk[p.Ply] = p.Hash
	if p.EpSq != NoSq {
		p.Hash ^= zobEP[p.EpSq&7]
		p.EpSq = NoSq
	}
	p.Hash ^= zobStm
	p.Side ^= ColorMask
	p.Halfmove++
	p.Ply++
	e.inChk[p.Ply] = false
}

func (e *Engine) unmakenull() {
	p := &e.Pos
	p.Ply--
	u := &e.undo[p.Ply]
	p.Side ^= ColorMask
	p.EpSq = u.ep
	p.Halfmove = u.halfmove
	p.Hash = e.hashStk[p.Ply]
}
