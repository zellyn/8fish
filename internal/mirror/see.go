package mirror

// This file implements PORTABLE losing-capture classification (the cheap
// SEE variants, task: SEE feasibility design) for the five-pass moveLoop —
// the asm-matched ordering path (TT move, two-tier MVV captures, killers,
// quiets in generation order). Unlike the FtSEE/orderedMoveLoop path
// (which sorts every move by a full SEE value — not 6502-pass-portable),
// this design keeps the asm's pass structure and adds exactly one new
// mechanism: a capture reached in the capture passes (1/2) may be
// CLASSIFIED as losing and then either DEFERRED to a new pass after the
// quiets (full-width) or deferred/PRUNED (quiescence).
//
// 6502 port shape (why this is pass-portable): the asm move stack carries
// a tier byte per move (victimtype<<4 | class, class 1=heavy 2=light
// 4=quiet). Classification happens at most once per capture per node —
// when the capture pass first reaches it — and the asm records the verdict
// by REWRITING the class bits to a new "losing capture" class (8), so the
// deferred pass is one more tier-filtered scan of the list with zero
// re-classification. The mirror models the same single-classification
// semantics with a per-ply deferred list (same order: list order).
// CONTRACT CHANGE FLAG: this adds tier class 8 to the MOVESTACK encoding
// and a sixth pass to PASSNO — both are extensions, not repurposings.
//
// The classification gate exploits a SEE theorem: a capture with
// vicVal[victim] >= vicVal[attacker] can never have SEE < 0 (the capturer
// may always stand pat after the first recapture), so only captures with
// victim < attacker are ever classified — MVV already orders the rest
// correctly. vicVal is monotone in piece type (P100 N320 B330 R500 Q975
// K20000), so the asm gate is a type compare (or a 64-byte type-pair
// table when Margin != 0).
//
// Variants (SEEParams.Mode), cheapest first:
//  1 pawn-defended: losing iff the destination is defended by an enemy
//    pawn. Two masked board probes (~40 cyc): the poor man's filter.
//  2 attacked-defended: losing iff the destination is defended AT ALL —
//    one attacked(to, enemy) call on the pre-capture board. No x-rays
//    through the moving attacker, no exchange arithmetic: equivalent to
//    a depth-2 swap-off (defender recaptures, we stop). ~150-250 cyc.
//  3 full boolean SEE: losing iff seeValue(m) < 0 — the full iterative
//    swap with x-ray reveal (ordering.go). The classification reference;
//    an asm port would need repeated least-attacker scans (~400-900 cyc).

// SEEParams configures losing-capture classification in the five-pass
// moveLoop. The zero value (Mode == 0) disables it entirely — a
// byte-identical no-op versus the pre-SEE five-pass search, which is what
// the parity gates rely on.
type SEEParams struct {
	// Mode selects the classification variant: 0 off; 1 pawn-defended;
	// 2 attacked-defended; 3 full boolean SEE (seeValue < 0).
	Mode int
	// Margin: classify only captures with vicVal[attacker]-vicVal[victim]
	// > Margin. 0 = every victim<attacker capture (BxN included, diff 10);
	// e.g. 20 excludes the near-equal BxN.
	Margin int
	// DeferFW: at full-width nodes, losing captures are deferred to a new
	// pass AFTER the quiet passes (they stay searched — ordering only).
	DeferFW bool
	// PruneQS: at quiescence nodes, losing captures are skipped entirely
	// (like delta pruning: a real search reduction, not just ordering).
	PruneQS bool
	// DeferQS: at quiescence nodes, losing captures are deferred after the
	// winning captures instead of pruned. Ignored when PruneQS is set.
	DeferQS bool
}

// on reports whether SEE classification is enabled at all.
func (s *SEEParams) on() bool { return s.Mode != 0 }

// seeNodeActive reports whether classification runs at this node kind.
func (e *Engine) seeNodeActive(qs bool) bool {
	if !e.SEE.on() {
		return false
	}
	if qs {
		return e.SEE.PruneQS || e.SEE.DeferQS
	}
	return e.SEE.DeferFW
}

// seeGate reports whether capture (attacker type a, victim type v) needs
// classification: vicVal[a] - vicVal[v] > Margin. Asm: a type compare
// (Margin 0) or one 64-byte type-pair table load.
func (e *Engine) seeGate(a, v int) bool {
	return vicVal[a]-vicVal[v] > e.SEE.Margin
}

// SeeValueForTest exposes the full-SEE exchange value (ordering.go
// seeValue) to the chesstest cycle-model measurements. Never used by the
// search itself.
func (e *Engine) SeeValueForTest(m Move) int32 { return e.seeValue(m) }

// seeLosing classifies capture m per the configured variant. Called only
// when the gate passed (victim < attacker), at most once per capture per
// node. The audit hook (tests only) tallies agreement vs the full-SEE
// reference without changing behavior.
func (e *Engine) seeLosing(m Move) bool {
	if e.SEEProbeHook != nil {
		e.SEEProbeHook(m)
	}
	var losing bool
	switch e.SEE.Mode {
	case 1:
		losing = e.pawnAttacks(m.To, e.Pos.Side^ColorMask)
	case 2:
		e.seeScan = true
		losing = e.attacked(m.To, e.Pos.Side^ColorMask)
		e.seeScan = false
	default:
		losing = e.seeValue(m) < 0
	}
	if e.seeAudit != nil {
		ref := e.seeValue(m) < 0
		b2i := func(b bool) int { return map[bool]int{true: 1}[b] }
		e.seeAudit[b2i(ref)][b2i(losing)]++
	}
	return losing
}
