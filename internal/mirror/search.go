package mirror

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
// Policy (mirrors docs/plan.md D9, single-cap variant): begin a new
// iteration only while under ~50% of the budget (a deeper iteration
// roughly multiplies node count, so past the halfway mark the next one
// would blow the budget); a hard cap at `budget` aborts an in-progress
// iteration, whose partial result is DISCARDED in favor of the last
// completed iteration. Depth 1 always runs to completion (uncapped) so a
// move is always produced. Deterministic given (position, budget,
// features, dither seed): the budget is denominated in nodes, never wall
// time, so an A/B replay is bit-identical.
func (e *Engine) SearchBudget(budget uint64, maxDepth int) (Move, int) {
	e.Nodes = 0 // per-move node accounting for the budget
	e.resetCycles()
	e.newMove()
	best, bestScore := NoMove, 0
	e.CompletedDepth = 0
	for d := 1; d <= maxDepth; d++ {
		if d > 1 {
			// Soft start gate: skip a new iteration past the halfway mark.
			if e.Nodes*2 >= budget {
				break
			}
			e.NodeBudget = budget // hard cap for this (and later) iterations
		} else {
			e.NodeBudget = 0 // depth 1 always completes
		}
		e.aspIterate(d, bestScore) // window seeded by the last completed score
		if e.aborted {
			break // incomplete iteration: keep the last completed result
		}
		best, bestScore = e.Best, e.RootScore
		e.CompletedDepth = d
		if best.From == NoSq {
			break // no legal moves (mate/stalemate): deeper won't help
		}
	}
	e.NodeBudget = 0
	return best, bestScore
}

// SearchCycleBudget is the CYCLE-denominated analogue of SearchBudget: it
// runs iterative deepening under a per-move budget of ESTIMATED 6502
// cycles (Cyc.Est, see cycles.go) instead of raw nodes, with identical
// soft-stop semantics — begin a new iteration only while under ~50% of the
// budget, hard-cap aborts an in-progress iteration and DISCARDS its
// partial result, depth 1 always completes. Deterministic given
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
			// Soft start gate: skip a new iteration past the halfway mark.
			if e.Cyc.Est*2 >= budget {
				break
			}
			e.CycleBudget = budget // hard cap for this (and later) iterations
		} else {
			e.CycleBudget = 0 // depth 1 always completes (cyc stays on)
		}
		e.aspIterate(d, bestScore) // window seeded by the last completed score
		if e.aborted {
			break // incomplete iteration: keep the last completed result
		}
		best, bestScore = e.Best, e.RootScore
		e.CompletedDepth = d
		if best.From == NoSq {
			break // no legal moves (mate/stalemate): deeper won't help
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
	e.inChk[0] = e.curInCheck()
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

	if ply >= e.MaxDepth {
		// Quiescence entry; in check it becomes a full evasion node
		// (no TT probe, no sprep, but full move loop + mate detection).
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

	// Full-width node: probe the transposition table.
	if ent, score, hit := e.ttprobe(); hit {
		e.ttFrom[ply] = ent.from
		e.ttTo[ply] = ent.to
		if ply != 0 {
			stored := int(ent.depthBound >> 2)
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

	// sprep: pruning before move generation (never when in check).
	if !e.inChk[ply] {
		// Null move R=2: not at root, not right after a null,
		// remaining >= 4, phase >= 3, beta below the +mate zone, and
		// the static eval already meets beta.
		if e.Features&FtNull != 0 && ply != 0 && e.undo[ply-1].from != NoSq &&
			e.MaxDepth-ply >= 4 && p.Phase >= 3 && e.beta[ply] < mateZoneLo &&
			e.eval() >= e.beta[ply] {
			e.makenull()
			e.alpha[ply+1] = -e.beta[ply]
			e.beta[ply+1] = -e.beta[ply] + 1
			e.MaxDepth -= 2
			score := -e.search()
			e.MaxDepth += 2
			e.unmakenull()
			if score >= e.beta[ply] {
				// Null cutoff: fail hard; store a moveless lower bound.
				e.ttstore(ttLower, NoSq, NoSq, e.beta[ply])
				return e.beta[ply]
			}
		}
		// RFP + forward futility, guarded away from mate-zone windows.
		// The asm's current guard uses an unsigned compare, so ANY
		// negative alpha or beta silently skips the block (futility
		// fires only when the whole window is in [0, +mate-zone)) — a
		// bug. The signed-aware guard (Fut.CorrectGuard / the deprecated
		// FixFutilityGuard) enables the block in every non-mate window;
		// the margins (Fut.RFP by remaining depth, Fut.Fut for the leaf)
		// are then the real tuning surface, since the corrected guard
		// re-margined is the port target, not the reverted bug.
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
				ev := e.eval()
				if m := e.Fut.RFP[remaining]; m > 0 && ev-m >= e.beta[ply] {
					return e.beta[ply] // reverse futility: fail high
				}
				if remaining == 1 && e.Fut.Fut > 0 && ev+e.Fut.Fut <= e.alpha[ply] {
					e.futile[ply] = true
				}
			}
		}
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

	// Late-move pruning arming (same rule as orderedMoveLoop): quiets are
	// passes 3 (killers) and 4 (non-killer), so once the node qualifies the
	// per-move test is a pure count compare. Never armed at qs nodes
	// (rem <= 0 there, so lmpNodeOK already returns false).
	rem := e.MaxDepth - ply
	lmpOn := !qs && e.lmpNodeOK(ply, rem)

	// Countermove probe cost: one indexed table load per full-width node
	// that reaches the quiet passes (charged pessimistically for every
	// non-qs node when CM is active). Zero unless a cycle screen sets it.
	if e.cyc && !qs && e.CM.on() {
		e.chargeCounterProbe()
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
			} else if e.ttFrom[ply] != NoSq && m.From == e.ttFrom[ply] && m.To == e.ttTo[ply] {
				continue
			}
			if pass != 0 {
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
					case 4: // final pass: skip killers and the counter (both done)
						if e.Features&FtKiller != 0 && e.killerMatch(ply, m) {
							continue
						}
						if e.CM.on() && e.counterMatch(ply, m) {
							continue
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
					}
				}
			}

			// Late-move (movecount) pruning: quiets in passes 3/4 past
			// threshold(rem) legal moves are skipped. Killers (pass 3) are
			// spared only with ExemptKillers; checks are spared only with
			// KeepChecks (which needs the move made to be known).
			lmpQuiet := pass == 4 || (pass == 3 && !e.LMP.ExemptKillers)
			lmpCand := lmpOn && lmpQuiet && e.legal[ply] >= e.LMP.lmpThreshold(rem)
			if lmpCand && !e.LMP.KeepChecks {
				continue
			}

			// Make + legality: the mover must not leave their king
			// attacked. (The asm's lazy-legality fast path is a pure
			// optimization with identical results.)
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

			// Child window mode (FT_LMR: PVS + late move reductions),
			// mirroring the asm smset block: 0 = full window, 1 = zero-
			// window scout, 2/3 = scout reduced by 1/2. First legal move
			// always full window; reductions only for late quiets, never
			// in check or giving check, never at the root.
			mode := 0
			if e.Features&FtLMR != 0 && !qs && e.legal[ply] >= 2 &&
				(e.LMR.EvasionPVS || !e.inChk[ply]) {
				mode = 1
				passOK := pass == 4 || (e.LMR.ReduceKillers && pass == 3)
				rem := e.MaxDepth - ply
				if ply != 0 && passOK && !e.inChk[ply] && !e.inChk[ply+1] &&
					e.legal[ply] >= e.LMR.LateR1 && rem >= e.LMR.MinRemR1 {
					mode = 2
					if rem >= e.LMR.MinRemR2 && e.legal[ply] >= e.LMR.LateR2 {
						mode = 3
					}
				}
			}

			var score int
			for {
				e.beta[ply+1] = -e.alpha[ply]
				if mode != 0 {
					e.alpha[ply+1] = e.beta[ply+1] - 1
				} else {
					e.alpha[ply+1] = -e.beta[ply]
				}
				if mode >= 2 {
					e.MaxDepth -= mode - 1 // reduced scout: shrink the horizon
					score = -e.search()
					e.MaxDepth += mode - 1
				} else {
					score = -e.search()
				}
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

		// End of list: advance the pass.
		switch pass {
		case 0, 1:
			pass++
		case 2:
			if qs {
				if genChecks {
					pass = 5 // QS quiet-checks pass after the captures
				} else {
					return e.done()
				}
			} else if e.futile[ply] {
				return e.done()
			} else {
				pass = e.firstQuietPass()
			}
		case 3:
			if e.CM.on() && !e.CM.BeforeKillers {
				pass = 6 // after-killers countermove pass follows the killers
			} else {
				pass = 4
			}
		case 6:
			if e.CM.BeforeKillers && e.Features&FtKiller != 0 {
				pass = 3 // before-killers: the killers still follow
			} else {
				pass = 4
			}
		default: // pass 4 (full-width final) or pass 5 (QS quiet-checks)
			return e.done()
		}
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
		return nil, 0, false
	}
	// Mate scores are stored node-relative.
	score := int(ent.score)
	if score >= mateZoneLo {
		score -= p.Ply
	} else if score <= nmateZoneHi {
		score += p.Ply
	}
	return ent, score, true
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
	if score >= mateZoneLo {
		score += p.Ply
	} else if score <= nmateZoneHi {
		score -= p.Ply
	}
	e.tt[ttIndex(p.Hash)] = ttEntry{
		verify:     p.Hash >> 8,
		from:       from,
		to:         to,
		score:      int16(score),
		depthBound: byte(depth)<<2 | byte(bound),
	}
}
