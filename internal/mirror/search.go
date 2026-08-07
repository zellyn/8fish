package mirror

import "fmt"

// SearchFixed runs one full-window fixed-depth search (the asm driver's
// fixed-depth mode: a single iterate at the cap). It returns the root
// best move (NoMove if no legal moves) and the root score.
func (e *Engine) SearchFixed(depth int) (Move, int) {
	e.NodeBudget = 0
	e.resetCycles() // accounting active only if CycleBudget/CycleTrack set
	e.newMove()
	e.iterate(depth)
	return e.Best, e.RootScore
}

// SearchBudget runs the asm driver's node-budgeted iterative-deepening
// mode: deepen depth 1,2,... reusing killers/history/TT across
// iterations, spending up to budget nodes for this move, then play the
// best move from the last COMPLETED iteration. maxDepth caps the ID loop
// (a safety ceiling; the budget normally stops it first). It returns the
// root best move (NoMove only if the position has no legal moves) and
// score.
//
// Policy: this is a NODE-denominated transcription of the asm driver's
// budget-mode ID loop (asm/engine.s `idloop`/`idok`), with the node count
// standing in for the asm's CLOCK:
//
//   - START a new iteration only when the PREDICTIVE gate says it fits:
//     the next iteration is estimated at 2x the cost of the one just
//     finished, and it is begun only if now + 2*lastCost is strictly under
//     the budget (idPredict, the asm's own heuristic — see its comment).
//   - HARD-ABORT at 2x budget (the asm's ABORTL = BUDGET*2), which
//     backstops an underestimate by the predictive gate. The aborted
//     iteration's partial result is DISCARDED in favor of the last
//     COMPLETED iteration (a partial iteration's "best" is just the first
//     root move it happened to search).
//   - Depth 1 always runs to completion (uncapped) so a move is always
//     produced (the asm's `CURDEPTH < 2` skip in checkclock).
//   - STOP on a winning mate: a completed iteration whose root score is in
//     the winning-mate zone is exact and final, so deepening cannot
//     improve it (the asm's MATEZONEHI test at `idok`).
//
// Deterministic given (position, budget, features, dither seed): the budget
// is denominated in nodes, never wall time, so an A/B replay is
// bit-identical.
//
// Remaining (deliberate, documented) differences from the asm driver:
//   - the asm polls its clock every 128 nodes, so its hard abort can
//     overshoot by up to one poll interval; the mirror aborts exactly at
//     the cap;
//   - the asm's clock starts at machine reset, so its "now" includes the
//     ~fixed engine entry cost, while the mirror's counter starts at the
//     search.
//
// (A moveless root — mate/stalemate — used to break the mirror's loop early;
// it does not any more, because the asm keeps iterating there. Each such
// iteration costs one move generation, and the position ends the game either
// way, so this is fidelity at no measurable price.)
//
// See TestBudgetModeParity (internal/chesstest) for the asm<->mirror
// budget-mode gate.
func (e *Engine) SearchBudget(budget uint64, maxDepth int) (Move, int) {
	e.Nodes = 0 // per-move node accounting for the budget
	e.resetCycles()
	e.newMove()
	best, bestScore := NoMove, 0
	e.CompletedDepth = 0
	for d := 1; d <= maxDepth; d++ {
		if d > 1 {
			// Hard abort at 2x budget (asm ABORTL), cumulative over the move.
			e.NodeBudget = budget * 2
		} else {
			e.NodeBudget = 0 // depth 1 always completes
		}
		if e.IterHook != nil {
			e.IterHook(d)
		}
		iterStart := e.Nodes
		e.aspIterate(d, bestScore) // window seeded by the last completed score
		if e.aborted {
			break // incomplete iteration: keep the last completed result
		}
		best, bestScore = e.Best, e.RootScore
		e.CompletedDepth = d
		if bestScore >= mateZoneLo {
			break // winning mate: exact and final, deepening can't improve it
		}
		if !idPredict(e.Nodes, iterStart, budget) {
			break // predicted to overshoot: stop with the completed move
		}
	}
	e.NodeBudget = 0
	return best, bestScore
}

// idPredict is the asm driver's PREDICTIVE soft-start gate (asm/engine.s,
// the block after `idok`), transcribed exactly — including its saturating
// arithmetic and its strict comparison. now is the clock (nodes or estimated
// cycles) at the end of the iteration that just completed, iterStart the
// clock when that iteration began, budget the soft budget.
//
// The estimate "next iteration costs 2x the last one" is the ASM's
// heuristic, not a better one: QS-dominated growth ratios actually run 2-6,
// and the 2x-budget hard abort backstops the underestimate. Fidelity to the
// shipped driver is the point.
func idPredict(now, iterStart, budget uint64) bool {
	cost := now - iterStart
	est := cost * 2
	if est/2 != cost {
		return false // asm: `asl` overflowed -> "nowhere near fitting"
	}
	proj := now + est
	if proj < now {
		return false // asm: carry out of the 24-bit add -> can't fit
	}
	return proj < budget // asm: `bcs report` on proj >= BUDGET
}

// HardAborted reports whether the most recent budgeted search ended by HARD
// ABORT (the 2x-budget cap fired mid-iteration and that iteration was
// discarded), as opposed to stopping cleanly at the predictive gate, the mate
// stop or the depth cap. It is the mirror's analogue of the asm's ABORT byte
// being nonzero at `report`. Only meaningful right after
// SearchBudget/SearchCycleBudget/SearchTimed.
func (e *Engine) HardAborted() bool { return e.aborted }

// SearchCycleBudget is the CYCLE-denominated analogue of SearchBudget: it
// runs iterative deepening under a per-move budget of ESTIMATED 6502
// cycles (Cyc.Est, see cycles.go) instead of raw nodes, with identical
// stop semantics — the asm's predictive soft-start gate (idPredict), its
// 2x-budget hard abort (which DISCARDS the partial iteration), depth 1
// always completes, and the winning-mate stop. This is the CLOSEST mirror
// analogue of the shipped driver, since the asm's own budget is in cycles.
// Deterministic given
// (position, budget, features, dither seed): the cycle estimate is a pure
// function of the tree, so an A/B replay is bit-identical, exactly like
// the node-budgeted mode. This is the mode that taxes a feature by its
// real 6502 cost, so a cheap-in-nodes/expensive-in-cycles term (e.g. an
// eval term costed via Costs.EvalTerm) converts fewer of its node savings
// into extra depth than a node budget would grant it.
func (e *Engine) SearchCycleBudget(budget uint64, maxDepth int) (Move, int) {
	e.Nodes = 0
	e.CycleBudget = budget // enables accounting for the whole move
	e.CycleTrack = false
	e.resetCycles() // cyc=true (budget!=0); zero Est and the counters
	e.newMove()
	best, bestScore := NoMove, 0
	e.CompletedDepth = 0
	for d := 1; d <= maxDepth; d++ {
		if d > 1 {
			// Hard abort at 2x budget (asm ABORTL), cumulative over the move.
			e.CycleBudget = budget * 2
		} else {
			e.CycleBudget = 0 // depth 1 always completes (cyc stays on)
		}
		if e.IterHook != nil {
			e.IterHook(d)
		}
		iterStart := e.Cyc.Est
		e.aspIterate(d, bestScore) // window seeded by the last completed score
		if e.aborted {
			break // incomplete iteration: keep the last completed result
		}
		best, bestScore = e.Best, e.RootScore
		e.CompletedDepth = d
		if bestScore >= mateZoneLo {
			break // winning mate: exact and final (asm idok)
		}
		if !idPredict(e.Cyc.Est, iterStart, budget) {
			break // predicted to overshoot: stop with the completed move
		}
	}
	e.CycleBudget = 0
	return best, bestScore
}

// newMove resets the per-move ordering state (killers cleared, history
// decayed so stale scores fade but useful ordering carries across the
// game). Called once per root move, before the (possibly iterated)
// search — killers/history then persist across ID iterations.
func (e *Engine) newMove() {
	e.killer = [MaxPly][2]Move{}
	if e.CM.on() && !e.CM.Persist {
		// Cleared per root move like the killers; CM.Persist keeps it (the
		// per-game fresh Engine in self-play resets it between games anyway).
		e.counter = [8][128]Move{}
	}
	if e.QHist.on() {
		e.qhNewMove() // age (halve) the quiet-history counters
	}
	if e.TTRepl.on() {
		e.ttAge ^= 0x80 // flip the TT generation bit (asm: EOR #$80)
	}
	if e.Features&FtHistory != 0 {
		if e.hist == nil {
			e.hist = &[2][128][128]int32{}
		} else {
			for s := range e.hist {
				for f := range e.hist[s] {
					for t := range e.hist[s][f] {
						e.hist[s][f][t] >>= 1
					}
				}
			}
		}
	}
}

// iterate runs one full-window search to the given depth.
func (e *Engine) iterate(depth int) {
	e.iterateWindow(depth, -Inf, Inf)
}

// iterateWindow runs one search to the given depth with an explicit root
// window [alpha, beta]. iterate is the full-window special case; the
// aspiration driver (aspIterate) calls this with narrow windows.
func (e *Engine) iterateWindow(depth, alpha, beta int) {
	e.MaxDepth = depth
	e.Pos.Ply = 0
	e.Best = NoMove
	e.aborted = false
	e.numExt = 0 // per-iterate reset; balanced save/restore keeps it 0 anyway
	e.inChk[0] = e.curInCheck()
	e.checkerSq[0] = NoSq // as asm engine.s: the root's in-check state comes
	//  from a full scan, which yields no square, so
	//  root evasions take the make + attacked() path
	e.alpha[0] = alpha
	e.beta[0] = beta
	e.RootScore = e.search()
}

// aspIterate runs ID iteration d at the root, using an aspiration window
// around prev (the previous completed iteration's score). With aspiration
// off, d == 1, or prev in the mate zone it is exactly a full-window
// iterate(d). Otherwise it opens (prev-Delta, prev+Delta) and re-searches
// on a fail-low/high per Asp.Policy, widening to the full window before the
// search can return a fail-soft/garbage move.
//
// On a budget abort mid-(re)search e.aborted is left set and e.Best is not
// trustworthy; the caller discards the whole iteration (keeps the last
// completed result), exactly as for a plain iterate. Because every widening
// path ends at the full window, a completed (non-aborted) aspIterate always
// leaves e.Best/e.RootScore holding a real, non-failing search result.
func (e *Engine) aspIterate(d, prev int) {
	if !e.Asp.on() || d == 1 || inMateZone(prev) {
		e.iterate(d)
		return
	}
	alpha, beta := prev-e.Asp.Delta, prev+e.Asp.Delta
	if alpha < -Inf {
		alpha = -Inf
	}
	if beta > Inf {
		beta = Inf
	}
	delta := e.Asp.Delta
	fails := 0
	e.AspWindows++
	for {
		e.iterateWindow(d, alpha, beta)
		if e.aborted {
			return // budget hit: iteration discarded by the caller
		}
		s := e.RootScore
		failLow := s <= alpha && alpha > -Inf
		failHigh := s >= beta && beta < Inf
		if !failLow && !failHigh {
			return // score inside the window: accept this iteration
		}
		if failLow {
			e.AspFailLow++
		} else {
			e.AspFailHigh++
		}
		// A mate discovered through a narrow window, or the third fail,
		// forces the full window (no mate-score clipping, guaranteed
		// terminal). Otherwise widen per policy.
		fails++
		switch {
		case inMateZone(s) || fails >= 3:
			alpha, beta = -Inf, Inf
		case e.Asp.Policy == AspProgressive:
			delta *= 2 // shift-doubling; portable
			alpha, beta = s-delta, s+delta
			if alpha < -Inf {
				alpha = -Inf
			}
			if beta > Inf {
				beta = Inf
			}
		case e.Asp.Policy == AspAsym:
			if failLow {
				alpha = -Inf // only the failing bound opens
			} else {
				beta = Inf
			}
		default: // AspFull: both bounds open on the first fail
			alpha, beta = -Inf, Inf
		}
	}
}

// search mirrors asm search.s node for node. Fail-hard negamax with
// quiescence; scores are from the side to move's POV.
func (e *Engine) search() int {
	if e.aborted {
		return 0 // unwinding after a node-budget hard abort
	}
	e.Nodes++
	if e.NodeBudget != 0 && e.Nodes >= e.NodeBudget {
		e.aborted = true // spent the per-move budget: abandon this iteration
		return 0
	}
	p := &e.Pos
	ply := p.Ply
	if e.cyc {
		// Book this node's base cost (by type), then apply the cycle budget
		// soft-stop, mirroring the node-budget hard abort above.
		qsNode := ply >= e.MaxDepth
		e.chargeNode(qsNode, qsNode && e.inChk[ply])
		if e.CycleBudget != 0 && e.Cyc.Est >= e.CycleBudget {
			e.aborted = true
			return 0
		}
	}

	if ply >= MaxPly-1 {
		return e.eval() // hard ply cap: static eval
	}
	if ply != 0 {
		// 50-move rule.
		hm := p.Halfmove
		if hm >= 100 {
			return 0
		}
		// Twofold repetition against the search path: only reachable
		// within the last halfmove plies, same side to move.
		if hm >= 4 {
			lo := ply - int(hm)
			if lo < 0 {
				lo = 0
			}
			for x := ply - 2; x >= lo; x -= 2 {
				if e.hashStk[x] == p.Hash {
					return 0
				}
			}
		}
		// Insufficient material: phase <= 1 and no pawns.
		if p.Phase < 2 && !e.anyPawn() {
			return 0
		}
	}

	e.legal[ply] = 0
	e.qsKind[ply] = false
	e.raised[ply] = false
	e.futile[ply] = false
	e.deltaT[ply] = -32768
	e.ttFrom[ply] = NoSq
	e.ttBF[ply] = NoSq
	if e.Improving.on() {
		// Invalidate this ply's recorded eval; it is re-set only if an eval
		// is actually computed at this node (naturally or, in full-signal,
		// forced before moveLoop). An ancestor at ply-2 that computed no
		// eval must read as "missing", not stale.
		e.evalValid[ply] = false
	}

	if ply >= e.MaxDepth {
		return e.qsEntry()
	}

	// Full-width node: probe the transposition table.
	if ent, score, hit := e.ttprobe(); hit {
		e.ttFrom[ply] = ent.from
		e.ttTo[ply] = ent.to
		if ply != 0 {
			// &31: bit 7 of depthBound is the TTRepl age bit (never set
			// with the policy off — depth is clamped to 31 on store, so
			// the mask is behavior-identical there).
			stored := int(ent.depthBound>>2) & 31
			remaining := e.MaxDepth - ply
			if remaining <= stored {
				switch ent.depthBound & 3 {
				case ttExact:
					return score
				case ttLower:
					if score >= e.beta[ply] {
						return e.beta[ply]
					}
				case ttUpper:
					if score <= e.alpha[ply] {
						return e.alpha[ply]
					}
				}
			}
		}
	}

	// Internal iterative reduction: a TT-missing node with real remaining
	// depth is searched one ply shallower (iir.go). The horizon change must
	// cover the whole node (sprep gates, children, the exit TT store), so
	// the remainder of the node is the factored sprepAndMove.
	if e.iirApplies(ply) {
		e.MaxDepth--
		score := e.sprepAndMove(ply)
		e.MaxDepth++
		return score
	}
	return e.sprepAndMove(ply)
}

// qsEntry is the quiescence entry (the ply >= MaxDepth branch of search):
// stand pat, the QS ply cap, the delta-pruning threshold, then the move
// loop. In check it falls through to a full evasion node. Factored so
// razoring (razor.go) can drop a shallow full-width node into quiescence
// through the engine's own entry path.
func (e *Engine) qsEntry() int {
	p := &e.Pos
	ply := p.Ply
	e.QSNodes++
	if !e.inChk[ply] {
		e.qsKind[ply] = true
		score := e.eval()
		if score >= e.beta[ply] {
			return e.beta[ply] // stand pat
		}
		if score > e.alpha[ply] {
			e.alpha[ply] = score
		}
		// QS ply cap: beyond it, the stand-pat result is final.
		if e.QS.PlyCap > 0 && ply-e.MaxDepth >= e.QS.PlyCap {
			return e.alpha[ply]
		}
		// Delta-pruning threshold, disabled at low phase.
		if p.Phase >= 6 {
			e.deltaT[ply] = e.alpha[ply] - score - 200
		}
	}
	return e.moveLoop()
}

// sprepAndMove is the full-width node's tail: sprep (null move, RFP/
// futility, razoring) and the move loop. Factored from search so IIR can
// wrap it in a horizon reduction; with IIR off the extraction is
// behavior-identical.
func (e *Engine) sprepAndMove(ply int) int {
	p := &e.Pos
	// sprep: pruning before move generation (never when in check).
	if !e.inChk[ply] {
		// Null move R=2 (NullR can deepen the reduction at deep nodes —
		// razor.go): not at root, not right after a null, remaining >= 4,
		// phase >= 3, beta below the +mate zone, and the static eval
		// already meets beta.
		if e.Features&FtNull != 0 && ply != 0 && e.undo[ply-1].from != NoSq &&
			e.MaxDepth-ply >= 4 && p.Phase >= 3 && e.beta[ply] < mateZoneLo &&
			e.eval() >= e.beta[ply] {
			R := e.nullReduction(ply)
			e.makenull()
			e.alpha[ply+1] = -e.beta[ply]
			e.beta[ply+1] = -e.beta[ply] + 1
			e.MaxDepth -= R
			score := -e.search()
			e.MaxDepth += R
			e.unmakenull()
			if score >= e.beta[ply] {
				// Null cutoff: fail hard; store a moveless lower bound.
				e.ttstore(ttLower, NoSq, NoSq, e.beta[ply])
				return e.beta[ply]
			}
		}
		// RFP + forward futility, guarded away from mate-zone windows.
		// The asm SHIPS the signed-aware guard (asm/search.s rfpapos:
		// bmi first, then MATEZONEHI/NMATEZONEHI), so Fut.CorrectGuard =
		// true (DefaultFutility) is the faithful model. The false branch
		// reproduces the HISTORICAL asm bug — an unsigned compare, under
		// which ANY negative alpha or beta silently skipped the block —
		// and is retained only as the A/B lever that measured the fix
		// (Fut.CorrectGuard / the deprecated FixFutilityGuard).
		correctGuard := e.FixFutilityGuard || e.Fut.CorrectGuard
		var guardOK bool
		if correctGuard {
			guardOK = e.alpha[ply] > nmateZoneHi && e.alpha[ply] < mateZoneLo &&
				e.beta[ply] > nmateZoneHi && e.beta[ply] < mateZoneLo
		} else {
			guardOK = e.alpha[ply] >= 0 && e.alpha[ply] < mateZoneLo &&
				e.beta[ply] >= 0 && e.beta[ply] < mateZoneLo
		}
		if e.Features&FtFutil != 0 && guardOK {
			remaining := e.MaxDepth - ply
			if remaining >= 1 && remaining <= e.Fut.MaxRem {
				ev := e.eval() // records evalStack[ply] when the heuristic is on
				rfpM, futM := e.Fut.RFP[remaining], e.Fut.Fut
				// Improving-conditional RFP/futility: not improving => tighter
				// margins (prune harder). improving() sees this node's freshly
				// recorded eval; the ply-2 side is present only when it too
				// computed an eval (always in full-signal, sparsely in free).
				if e.Improving.on() && e.Improving.RFP && !e.improving(ply) {
					if m := e.Improving.RFPNI[remaining]; m > 0 {
						rfpM = m
					}
					if e.Improving.FutNI > 0 {
						futM = e.Improving.FutNI
					}
				}
				if rfpM > 0 && ev-rfpM >= e.beta[ply] {
					return e.beta[ply] // reverse futility: fail high
				}
				// Razoring (razor.go): eval far BELOW alpha at remaining
				// 1-2 -> drop into quiescence. Shares the RFP compare's
				// eval and this block's mate-zone guard; one extra CMP on
				// the 6502 (Costs.Razor).
				if rm := e.Razor.margin(remaining); rm > 0 {
					if e.cyc {
						e.Cyc.RazorTests++
						e.Cyc.Est += uint64(e.Costs.Razor)
					}
					if ev+rm <= e.alpha[ply] {
						if e.cyc {
							e.Cyc.RazorDrops++
						}
						return e.razorDrop()
					}
				}
				if remaining == 1 && futM > 0 && ev+futM <= e.alpha[ply] {
					e.futile[ply] = true
				}
			}
		}
	}

	// Full-signal improving: guarantee a static eval at this full-width node
	// so the ply-2 comparison is always available (for this node's LMR and
	// for any descendant that reads it). Skip when a natural eval already ran
	// (null-move/RFP) so that eval is never charged twice; the ADDED eval
	// here pays its real ~872-cyc cost via chargeEval — the honest
	// full-signal tax under a cycle budget.
	if e.Improving.Mode == improvingFull && !e.evalValid[ply] {
		e.eval()
	}

	return e.moveLoop()
}

// moveLoop is snode..sdone: generate, run the five passes, terminal
// handling, and the TT store on the way out.
func (e *Engine) moveLoop() int {
	p := &e.Pos
	ply := p.Ply
	qs := e.qsKind[ply]
	if !qs && e.Features&FtOrder != 0 {
		return e.orderedMoveLoop()
	}
	// QS quiet-checks (task #37): at the first QS.Checks qs plies, also
	// search quiet checking moves. This needs the full move list (quiets
	// included), so generate all moves and add a dedicated quiet-checks
	// pass (pass 5) after the two capture passes.
	genChecks := qs && e.QS.Checks > 0 && ply-e.MaxDepth < e.QS.Checks
	list := e.generate(qs && !genChecks)

	pass := 1
	if !qs && e.ttFrom[ply] != NoSq {
		pass = 0
	}

	// GenDefer diagnostic (deferred-generation study): tally this node's
	// generation and TT-move situation. gdFound/gdLegal are per-node
	// latches so a TT promotion (four list entries share from/to) counts
	// once. Off by default: a byte-identical no-op.
	gd := e.GenDeferCount
	var gdFound, gdLegal bool
	if gd {
		if qs {
			e.GenDefer.QSNodes++
		} else {
			e.GenDefer.FWNodes++
			e.GenDefer.ListLen += uint64(len(list))
			if ply >= e.MaxDepth {
				e.GenDefer.Evasion++
			}
			if e.ttFrom[ply] != NoSq {
				e.GenDefer.TTAvail++
				e.GenDefer.AvailLen += uint64(len(list))
			}
		}
	}

	// Late-move pruning arming (same rule as orderedMoveLoop): quiets are
	// passes 3 (killers) and 4 (non-killer), so once the node qualifies the
	// per-move test is a pure count compare. Never armed at qs nodes
	// (rem <= 0 there, so lmpNodeOK already returns false).
	rem := e.MaxDepth - ply
	lmpOn := !qs && e.lmpNodeOK(ply, rem)

	// Quiet-history partition (qhist.go): when armed, pass 8 (between the
	// killers/countermove and the final quiet pass) searches quiets whose
	// history counter clears PartThresh; pass 4 then skips exactly those
	// (recorded in qhPart8 — the counters move during child searches, so a
	// second probe could disagree with the pass-8 verdict). On the asm this
	// is one more tier-filtered rescan with a tier-bit rewrite on the
	// searched moves, as in the SEE deferred pass.
	qhPart := !qs && e.qhPartActive()
	if qhPart {
		e.qhPart8[ply] = e.qhPart8[ply][:0]
	}

	// Improving-conditional LMR: computed once per node (the signal is a
	// property of the node, not the move). notImp is true only when the LMR
	// application is on and this node is NOT improving; then late quiets get
	// e.Improving.lmrExtra() extra reduction plies below.
	notImp := !qs && e.Improving.on() && e.Improving.LMR && !e.improving(ply)

	// Countermove probe cost: one indexed table load per full-width node
	// that reaches the quiet passes (charged pessimistically for every
	// non-qs node when CM is active). Zero unless a cycle screen sets it.
	if e.cyc && !qs && e.CM.on() {
		e.chargeCounterProbe()
	}

	// SEE losing-capture classification (see.go): armed once per node.
	// The per-ply deferred list collects losing captures skipped in the
	// capture passes; pass 7 searches them after the quiets (full-width)
	// or after the winning captures (QS defer). Off = untouched state.
	seeOn := e.seeNodeActive(qs)
	if seeOn {
		e.seeDefer[ply] = e.seeDefer[ply][:0]
	}

	for {
		for i := range list {
			m := list[i]
			// Pass 0: search only the TT move (from/to match, so a TT
			// promotion matches all four flag variants); later passes
			// skip anything matching it.
			if pass == 0 {
				if m.From != e.ttFrom[ply] || m.To != e.ttTo[ply] {
					continue
				}
				if gd && !gdFound {
					gdFound = true
					e.GenDefer.TTFound++
					e.gdSample(m)
				}
			} else if pass != 7 && e.ttFrom[ply] != NoSq && m.From == e.ttFrom[ply] && m.To == e.ttTo[ply] {
				continue
			}
			// Pass 7 iterates the already-classified deferred losing
			// captures (list swapped below): no filters re-run, matching
			// the asm's rewritten-tier rescan (classification happens at
			// most once per capture per node).
			if pass != 0 && pass != 7 {
				isCap := p.Board[m.To] != 0 || m.Flags&(FlEP|FlPromo) != 0
				if !isCap {
					switch pass {
					case 3: // killer pass: only killer matches
						if !e.killerMatch(ply, m) {
							continue
						}
						// Before-killers countermove already ran in pass 6;
						// don't re-search a counter that is also a killer.
						if e.CM.on() && e.CM.BeforeKillers && e.counterMatch(ply, m) {
							continue
						}
					case 6: // countermove pass: only the stored counter (a quiet)
						if !e.counterMatch(ply, m) {
							continue
						}
						// After-killers: a counter that is also a killer was
						// already searched in the killer pass — skip it here.
						if !e.CM.BeforeKillers && e.Features&FtKiller != 0 && e.killerMatch(ply, m) {
							continue
						}
					case 8: // history-partition pass: high-history quiets only
						if e.Features&FtKiller != 0 && e.killerMatch(ply, m) {
							continue // killers already searched
						}
						if e.CM.on() && e.counterMatch(ply, m) {
							continue // counter already searched
						}
						if e.qhVal(m) < e.QHist.PartThresh {
							continue // low history: left for pass 4
						}
						// Record the pass-8 verdict (asm: tier-bit rewrite)
						// so pass 4 skips exactly this move, probe-free.
						e.qhPart8[ply] = append(e.qhPart8[ply], m)
					case 4: // final pass: skip killers and the counter (both done)
						if e.Features&FtKiller != 0 && e.killerMatch(ply, m) {
							continue
						}
						if e.CM.on() && e.counterMatch(ply, m) {
							continue
						}
						if qhPart && e.qhPart8Match(ply, m) {
							continue // searched in pass 8 (asm: rewritten tier)
						}
					case 5: // QS quiet-checks pass: keep quiets; the
						// gives-check filter is applied after make() below.
						// Optional SafeChecks gate: skip a check whose
						// destination is attacked by an enemy pawn.
						if e.QS.SafeChecks && e.pawnAttacks(m.To, p.Side^ColorMask) {
							continue
						}
					default: // capture passes: no quiets
						continue
					}
				} else {
					// Deep-qs recapture-only: past the threshold, only
					// captures landing on the previous move's TO square.
					if qs && e.QS.RecapAfter > 0 && ply > 0 &&
						ply-e.MaxDepth >= e.QS.RecapAfter && m.To != e.undo[ply-1].to {
						continue
					}
					if pass >= 3 {
						continue // captures were passes 1/2
					}
					if m.Flags&FlPromo != 0 {
						// Promotions: always heavy; qs takes queen promos only.
						if pass != 1 {
							continue
						}
						if qs && m.Flags&FlPromo != Queen {
							continue
						}
					} else {
						victim := Pawn
						if m.Flags&FlEP == 0 {
							victim = int(p.Board[m.To] & TypeMask)
						}
						heavy := victim >= Rook
						if heavy != (pass == 1) {
							continue
						}
						// Delta pruning: skip if the victim can't lift
						// stand-pat to alpha.
						if vicVal[victim] < e.deltaT[ply] {
							continue
						}
						// SEE gate + classification (see.go): only
						// victim < attacker (minus margin) captures are
						// ever classified; losing ones are deferred to
						// pass 7 (or pruned outright at QS with PruneQS).
						if seeOn {
							attacker := int(p.Board[m.From] & TypeMask)
							passed := e.seeGate(attacker, victim)
							if e.cyc {
								e.chargeSEEGate(passed, qs)
							}
							if passed && e.seeLosing(m) {
								if e.cyc {
									e.Cyc.SEELosing++
								}
								if !qs {
									e.seeDefer[ply] = append(e.seeDefer[ply], m)
								} else if e.SEE.DeferQS && !e.SEE.PruneQS {
									e.seeDefer[ply] = append(e.seeDefer[ply], m)
								}
								continue
							}
						}
					}
				}
			}

			// Late-move (movecount) pruning: quiets in passes 3/4 past
			// threshold(rem) legal moves are skipped. Killers (pass 3) are
			// spared only with ExemptKillers; checks are spared only with
			// KeepChecks (which needs the move made to be known).
			lmpQuiet := pass == 4 || pass == 8 || (pass == 3 && !e.LMP.ExemptKillers)
			lmpCand := lmpOn && lmpQuiet && e.legal[ply] >= e.LMP.lmpThreshold(rem)
			// History-gated LMP (qhist.go): a late quiet whose history
			// counter clears LMPThresh survives the count prune — the
			// ordering-enabler form of the LMP re-screen.
			if lmpCand && e.QHist.on() && e.QHist.LMPThresh > 0 &&
				e.qhVal(m) >= e.QHist.LMPThresh {
				lmpCand = false
			}
			if lmpCand && !e.LMP.KeepChecks {
				continue
			}

			// Pre-make evasion filter (asm sdevade): at an in-check node,
			// a move that neither captures the checker nor interposes on
			// its ray is illegal, and the asm proves that from two table
			// lookups instead of make + attacked() + unmake. Tree-
			// identical, but it removes makes, and makes are compared by
			// the parity gates — so the twin has to skip them too.
			if e.inChk[ply] && e.evasionFiltered(m, ply) {
				continue
			}
			// Quiet-history LMR gate operands, captured BEFORE make (the
			// gate itself runs post-make, when From is empty and Side has
			// flipped). No charge: the asm reads these from the move-stack
			// entry it already holds.
			var qhSide, qhMover byte
			if e.QHist.on() {
				qhSide, qhMover = p.Side, p.Board[m.From]&TypeMask
			}
			// Make + legality: the mover must not leave their king
			// attacked.
			//
			// DELIBERATELY NOT MIRRORED, unlike the evasion filter above:
			// the asm settles this WITHOUT a full scan for most movers —
			// the lazy-legality fast path (a non-king, non-ep mover whose
			// FROM square is not on a ray through its own king cannot
			// expose it) and, since 2026-07-30, the SINGLE-RAY PIN TEST
			// (asm/board.s pinray) for the movers that ARE aligned. Both
			// are POST-make and tree-identical, so they move no quantity
			// any parity gate compares: `make` is unchanged (the ray test
			// runs after it), and Cyc.Attacked is priced at ZERO in
			// DefaultCycleCosts — attacked() never entered the fit,
			// precisely because its mirror/asm per-node frequency already
			// diverged (see cycles.md). What the pin test DID move is the
			// asm's true cycles per node, ~2.5% cheaper, so the fitted
			// coefficients now over-price the asm by about that much; that
			// shows up as TestBudgetModeParity's reported spend ratio, not
			// as a divergence. Cyc.Attacked / Cyc.AttackedAll remain
			// mirror-side diagnostics and are NOT comparable to the asm's
			// `attacked` probe count.
			e.make(m)
			moverKing := p.PieceSq[int(p.Side^ColorMask)<<1]
			if e.attacked(moverKing, p.Side) {
				e.unmake()
				continue
			}
			if lmpCand && !e.inChk[ply+1] {
				e.unmake() // KeepChecks: legal quiet past the count, not a check
				continue
			}
			// QS quiet-checks pass: only search moves that give check.
			// make() has already set e.inChk[p.Ply] for the child, so this
			// reuses the same perft-verified attack scan.
			if pass == 5 {
				if !e.inChk[p.Ply] {
					e.unmake()
					continue
				}
				e.QSCheckNodes++
			}
			e.legal[ply]++
			if gd && pass == 0 && !gdLegal {
				gdLegal = true
				e.GenDefer.TTLegal++
			}

			// Child window mode (FT_LMR: PVS + late move reductions),
			// mirroring the asm smset block: 0 = full window, 1 = zero-
			// window scout, 2/3 = scout reduced by 1/2. First legal move
			// always full window; reductions only for late quiets, never
			// in check or giving check, never at the root.
			mode := 0
			if e.Features&FtLMR != 0 && !qs && e.legal[ply] >= 2 &&
				(e.LMR.EvasionPVS || !e.inChk[ply]) {
				mode = 1
				passOK := pass == 4 || pass == 8 || (e.LMR.ReduceKillers && pass == 3)
				rem := e.MaxDepth - ply
				if ply != 0 && passOK && !e.inChk[ply] && !e.inChk[ply+1] &&
					e.legal[ply] >= e.LMR.LateR1 && rem >= e.LMR.MinRemR1 {
					mode = 2
					if rem >= e.LMR.MinRemR2 && e.legal[ply] >= e.LMR.LateR2 {
						mode = 3
					}
					// Improving: reduce late quiets one (LMRExtra) ply deeper
					// when NOT improving. improving() reads this node's eval
					// (present at every full-width node in full-signal; only
					// when a natural eval ran in free-signal, else permissive).
					if notImp {
						mode += e.Improving.lmrExtra()
					}
					// History-gated LMR (qhist.go): a high-history late quiet
					// is not reduced; a no-history one is reduced one ply
					// more. One probe + compare per reduced candidate (the
					// mover's side/type were captured pre-make above).
					if e.QHist.on() && (e.QHist.LMRHigh > 0 || e.QHist.LMRLowThresh > 0) {
						hv := e.qhValAt(qhSide, qhMover, m.To)
						if e.QHist.LMRHigh > 0 && hv >= e.QHist.LMRHigh {
							mode = 1
						} else if e.QHist.LMRLowThresh > 0 && hv < e.QHist.LMRLowThresh {
							mode++
						}
					}
				}
			}

			// Check extension: the move just made gives check (inChk[ply+1]),
			// so search the child one ply deeper (don't decrement remaining
			// depth), capped at CheckExt.MaxExt extensions per path. Never in
			// quiescence, never simultaneously with an LMR reduction (a checking
			// move is already mode < 2 via the !inChk[ply+1] reduction gate).
			ext := e.checkExt(qs, ply)

			var score int
			for {
				e.beta[ply+1] = -e.alpha[ply]
				if mode != 0 {
					e.alpha[ply+1] = e.beta[ply+1] - 1
				} else {
					e.alpha[ply+1] = -e.beta[ply]
				}
				// Child horizon delta: +ext for a check extension, or the LMR
				// reduction for a reduced scout (mutually exclusive with ext).
				dd := ext
				if mode >= 2 {
					dd = -(mode - 1) // reduced scout: shrink the horizon
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
					break // full-window result, or scout failed low: final
				}
				// Scout failed high: reduced retries unreduced; unreduced
				// retries full-window only if the window is open (at a
				// zero-window node the fail-high result is final).
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
				// Fail-hard beta cutoff.
				if gd && pass == 0 {
					e.GenDefer.TTCut++
					e.GenDefer.CutLen += uint64(len(list))
				}
				if !qs {
					if e.Features&FtKiller != 0 && p.Board[m.To] == 0 &&
						m.Flags&(FlEP|FlPromo) == 0 {
						k := &e.killer[ply]
						if k[0].From != m.From || k[0].To != m.To {
							k[1] = k[0]
							k[0] = Move{m.From, m.To, 0}
						}
					}
					// Countermove: a quiet cutter refutes the previous ply's
					// move; store it against that move's (piece, to) key.
					if e.CM.on() && ply != 0 && p.Board[m.To] == 0 &&
						m.Flags&(FlEP|FlPromo) == 0 {
						e.storeCounter(ply, m)
					}
					// Quiet-history bump (qhist.go): reward the quiet cutter
					// with rem*rem, saturating at the byte ceiling.
					if e.QHist.on() && p.Board[m.To] == 0 &&
						m.Flags&(FlEP|FlPromo) == 0 {
						e.qhBump(m, rem)
					}
					e.ttstore(ttLower, m.From, m.To, e.beta[ply])
				}
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
		}

		// End of list: advance the pass. seePass7 enters the deferred
		// losing-capture pass when any capture was deferred (asm: one more
		// tier-filtered rescan of the move stack, charged per item), else
		// the node is done.
		seePass7 := func() bool {
			if !seeOn || len(e.seeDefer[ply]) == 0 {
				return false
			}
			if e.cyc {
				e.chargeSEERescan(len(list))
			}
			list = e.seeDefer[ply]
			pass = 7
			return true
		}
		switch pass {
		case 0, 1:
			pass++
		case 2:
			if qs {
				if genChecks {
					pass = 5 // QS quiet-checks pass after the captures
				} else if !seePass7() {
					return e.done()
				}
			} else if e.futile[ply] {
				// Futile node: quiets are pruned but captures are still
				// searched — deferred losing captures included.
				if !seePass7() {
					return e.done()
				}
			} else {
				pass = e.firstQuietPass()
			}
		case 3:
			if e.CM.on() && !e.CM.BeforeKillers {
				pass = 6 // after-killers countermove pass follows the killers
			} else {
				pass = e.finalQuietPass(qhPart)
			}
		case 6:
			if e.CM.BeforeKillers && e.Features&FtKiller != 0 {
				pass = 3 // before-killers: the killers still follow
			} else {
				pass = e.finalQuietPass(qhPart)
			}
		case 8:
			pass = 4 // history partition done: the remaining quiets
		case 7:
			return e.done()
		default: // pass 4 (full-width final) or pass 5 (QS quiet-checks)
			if !seePass7() {
				return e.done()
			}
		}
	}
}

// gdSample records the operand shape of a TT move that pass 0 matched,
// for the deferred-generation cost model (diagnostic only; see
// GenDeferStats). Called at most once per node.
func (e *Engine) gdSample(m Move) {
	p := &e.Pos
	typ := p.Board[m.From] & TypeMask
	e.GenDefer.AvailType[typ&7]++
	if p.Board[m.To] != 0 {
		e.GenDefer.AvailCap++
	}
	if m.Flags&FlPromo != 0 {
		e.GenDefer.AvailPromo++
	}
	if m.Flags&FlCastle != 0 {
		e.GenDefer.AvailCastle++
	}
	if m.Flags&FlEP != 0 {
		e.GenDefer.AvailEP++
	}
	if typ == Bishop || typ == Rook || typ == Queen {
		df := int(m.To&0x0F) - int(m.From&0x0F)
		dr := int(m.To>>4) - int(m.From>>4)
		if df < 0 {
			df = -df
		}
		if dr < 0 {
			dr = -dr
		}
		d := df
		if dr > d {
			d = dr
		}
		e.GenDefer.AvailRay += uint64(d)
		e.GenDefer.AvailRayN++
	}
}

// done is sdone: mate/stalemate detection and the exit TT store.
func (e *Engine) done() int {
	ply := e.Pos.Ply
	if e.qsKind[ply] {
		return e.alpha[ply]
	}
	if e.legal[ply] == 0 {
		if e.futile[ply] {
			return e.alpha[ply] // quiets were pruned: can't claim mate
		}
		var score int
		if e.inChk[ply] {
			score = ply - Mate
		}
		e.ttstore(ttExact, NoSq, NoSq, score)
		return score
	}
	bound := ttUpper
	if e.raised[ply] {
		bound = ttExact
	}
	e.ttstore(bound, e.ttBF[ply], e.ttBT[ply], e.alpha[ply])
	return e.alpha[ply]
}

// checkExt returns the child horizon extension (0 or 1) for the move just
// made at parent ply (so p.Ply == ply+1, inChk[ply+1] and undo[ply] are set).
// It extends by 1 when the feature is on, the node is not quiescence, the move
// gives check, the per-path extension budget (numExt < MaxExt) is not spent,
// and — with CapturesOnly — the checking move is also a capture.
func (e *Engine) checkExt(qs bool, ply int) int {
	if qs || !e.CheckExt.on() {
		return 0
	}
	if !e.inChk[ply+1] || e.numExt >= e.CheckExt.MaxExt {
		return 0
	}
	if e.CheckExt.CapturesOnly && e.undo[ply].cap == 0 {
		return 0
	}
	e.CheckExts++
	return 1
}

func (e *Engine) killerMatch(ply int, m Move) bool {
	k := &e.killer[ply]
	return (k[0].From == m.From && k[0].To == m.To) ||
		(k[1].From == m.From && k[1].To == m.To)
}

// anyPawn reports whether any pawn is on the board (insufficient-
// material check).
func (e *Engine) anyPawn() bool {
	for slot := range 32 {
		sq := e.Pos.PieceSq[slot]
		if sq != NoSq && e.Pos.Board[sq]&TypeMask == Pawn {
			return true
		}
	}
	return false
}

// ttIndex: 12 bits from hash bytes 0 and 1, as ttaddr.
func ttIndex(hash uint32) int {
	return int(hash&0xFF) | int(hash>>8&0x0F)<<8
}

// ttprobe returns the entry and its ply-adjusted score on a hit.
func (e *Engine) ttprobe() (*ttEntry, int, bool) {
	if e.cyc {
		e.chargeTTProbe()
	}
	p := &e.Pos
	ent := &e.tt[ttIndex(p.Hash)]
	if ent.depthBound&3 == 0 || ent.verify != p.Hash>>8 {
		if e.TTHook != nil {
			e.TTHook(TTOp{Op: TTProbeMiss, Hash: p.Hash, Ply: p.Ply})
		}
		return nil, 0, false
	}
	// Mate scores are stored node-relative (asm tt.s ttadj: the SIGNED
	// MATEZONEHI/NMATEZONEHI zone test).
	score := int(ent.score)
	if score >= mateZoneLo {
		score -= p.Ply
	} else if score <= nmateZoneHi {
		score += p.Ply
	}
	if e.TTHook != nil {
		e.TTHook(TTOp{Op: TTProbeHit, Hash: p.Hash, Ply: p.Ply, From: ent.from,
			To: ent.to, Score: int16(score), DepthBound: ent.depthBound})
	}
	return ent, score, true
}

// TTOp is one transposition-table operation, reported through Engine.TTHook.
// It is the mirror-side twin of probing asm/tt.s at `ttprobe` / `tthit` /
// `ttfmiss` / `tsgo`, so a harness can diff the two engines' TT operation
// SEQUENCES rather than only their aggregate tree sizes. Score is the value
// as the engine sees it at this node — ply-ADJUSTED on a probe hit, and
// ply-adjusted to node-relative form on a store, exactly as the asm's
// TTENTRY+5/6 reads at `tthit` / `tsgo`.
type TTOp struct {
	Op         byte
	Hash       uint32
	Ply        int
	From, To   byte
	Score      int16
	DepthBound byte
}

// TT operation kinds for TTOp.Op.
const (
	TTProbeMiss byte = iota
	TTProbeHit
	TTStore
)

func (o TTOp) String() string {
	name := [...]string{"miss ", "hit  ", "store"}[o.Op]
	s := fmt.Sprintf("%s hash=%08x ply=%d", name, o.Hash, o.Ply)
	if o.Op != TTProbeMiss {
		s += fmt.Sprintf(" from=%02x to=%02x score=%d depth=%d bound=%d",
			o.From, o.To, o.Score, o.DepthBound>>2, o.DepthBound&3)
	}
	return s
}

// ttstore writes an always-replace entry; depth = MaxDepth - Ply
// clamped to 0..31, mate scores converted to node-relative.
func (e *Engine) ttstore(bound int, from, to byte, score int) {
	if e.aborted {
		return // don't poison the TT with garbage scores while unwinding
	}
	if e.cyc {
		e.chargeTTStore()
	}
	p := &e.Pos
	depth := e.MaxDepth - p.Ply
	if depth < 0 {
		depth = 0
	}
	if depth > 31 {
		depth = 31
	}
	// Inverse of the probe adjustment (asm tt.s tsadj).
	if score >= mateZoneLo {
		score += p.Ply
	} else if score <= nmateZoneHi {
		score -= p.Ply
	}
	ent := ttEntry{
		verify:     p.Hash >> 8,
		from:       from,
		to:         to,
		score:      int16(score),
		depthBound: byte(depth)<<2 | byte(bound),
	}
	idx := ttIndex(p.Hash)
	if e.TTRepl.on() {
		// Depth-preferred + age replacement (ttrepl.go): keep a deeper
		// current-generation entry for a different position; tag stores
		// with the generation bit.
		if e.ttKeepOld(&e.tt[idx], ent.verify, depth) {
			return
		}
		ent.depthBound |= e.ttAge
	}
	if e.TTHook != nil {
		e.TTHook(TTOp{Op: TTStore, Hash: p.Hash, Ply: p.Ply, From: from, To: to,
			Score: ent.score, DepthBound: ent.depthBound})
	}
	e.tt[idx] = ent
}
