package chesstest

// EXPERIMENTAL (SEE feasibility design): exact-ish 6502 cycle models of
// the three SEE losing-capture classification variants (mirror see.go),
// evaluated on the TRUE operand distribution — every gate-passing capture
// classification the mirror engine performs during asm-matched searches.
// This follows the attacked()-redesign methodology (attackdist_test.go):
// per-fragment instruction-counted models, with modelB (the ADOPTED
// attacked() implementation, validated within 0.6% of the emulator's
// measured cycles) reused directly for the mode-2 defended scan.
//
//	mode 1 (pawn-defended):    modelSEEPawn — two masked board probes
//	mode 2 (attacked-defended): 20 cyc glue + modelB(to, enemy)
//	mode 3 (full boolean SEE):  modelSEESwap — superpiece both-color
//	   attacker enumeration with x-ray ray-continuation + byte-value
//	   iterative swap with early exit (the cheapest full-SEE asm shape
//	   we could sketch; fragment prices from the validated models)

import (
	"testing"

	"github.com/zellyn/8fish/internal/mirror"
)

// seeVal10 is the piece value in tens (byte-sized), by type 1..6.
var seeVal10 = [8]int{0, 10, 32, 33, 50, 98, 255, 0}

// modelSEEPawn: exact cycle count of the pawn-defended probe.
//
//	; jsr seepawn (6). GTO holds the capture destination.
//	seepawn: lda SIDE(3) bne black(2/3)
//	  ; white mover: enemy pawn = black = $09 nibble at to+$0F/to+$11
//	  lda GTO(3) clc(2) adc #$0F(2) tax(2) and #$88(2) bne s1(2/3)
//	  lda BOARD,x(4) and #$0F(2) cmp #$09(2) beq hit(2/3)
//	  s1: lda GTO(3) clc(2) adc #$11(2) tax(2) and #$88(2) bne miss(2/3)
//	  lda BOARD,x(4) and #$0F(2) cmp #$09(2) beq hit(2/3)
//	  miss: clc(2) rts(6)    hit: sec(2) rts(6)
func modelSEEPawn(to, side byte, board func(byte) byte) (uint64, bool) {
	cyc := uint64(6 + 3 + 2) // jsr, lda SIDE, bne
	if side != 0 {
		cyc++ // branch taken to the black body
	}
	offs := [2]int{0x0F, 0x11}
	if side != 0 {
		offs = [2]int{-0x0F, -0x11}
	}
	enemyPawn := byte(0x09) // black pawn nibble
	if side != 0 {
		enemyPawn = 0x01 // white pawn nibble
	}
	for _, off := range offs {
		s := int(to) + off
		cyc += 3 + 2 + 2 + 2 + 2 + 2 // lda,clc,adc,tax,and,bne
		if s < 0 || s > 255 || s&0x88 != 0 {
			cyc++ // bne taken
			continue
		}
		cyc += 4 + 2 + 2 + 2 // lda BOARD, and, cmp, beq
		if board(byte(s))&0x0F == enemyPawn {
			cyc += 1 + 2 + 6 // beq taken, sec, rts
			return cyc, true
		}
	}
	return cyc + 2 + 6, false // clc, rts
}

// seeAttacker is one enumerated attacker of the target square.
type seeAttacker struct {
	val  int  // value in tens
	side byte // 0 white, 8 black
}

// modelSEESwap prices the sketched boolean-SEE asm routine and returns its
// verdict. Fragment prices (per-instruction counts, matching the validated
// GSTEP/GSLIDED/modelB fragments):
//
//	knight probe: 13 off-board / 21 on-board miss / +10 append
//	ray step: 19 per empty square walked (GSLIDED walk shape)
//	ray classification at first piece: 25 (nibble -> 16-byte class table,
//	  direction-class test, append); +19 continue-step when the piece is
//	  the mover (from-square vacates: x-ray continuation)
//	swap: per ply 45 (min-scan ~9/live entry + remove 6 + 16-bit adc/sbc
//	  arithmetic 20 + loop control 10), entry/exit 40
func modelSEESwap(from, to, side byte, victim, attacker int,
	board func(byte) byte) (uint64, bool) {
	cyc := uint64(40) // jsr, setup: lists cleared, GTO/GFROM staged
	var atk []seeAttacker

	// 8 knight probes (both colors classified in one pass).
	for _, d := range []int{-0x21, -0x1F, -0x12, -0x0E, 0x0E, 0x12, 0x1F, 0x21} {
		s := int(to) + d
		if s < 0 || s > 255 || s&0x88 != 0 {
			cyc += 13
			continue
		}
		cyc += 21
		b := board(byte(s))
		if b != 0 && b&0x07 == 2 && byte(s) != from {
			cyc += 10
			atk = append(atk, seeAttacker{seeVal10[2], b & 0x08})
		}
	}
	// 8 rays: walk to the first piece; classify; continue behind the mover
	// (its square vacates) and behind ray-matching sliders (batteries).
	type ray struct {
		delta int
		diag  bool
	}
	rays := []ray{{0x0F, true}, {0x11, true}, {-0x0F, true}, {-0x11, true},
		{0x10, false}, {-0x10, false}, {0x01, false}, {-0x01, false}}
	for _, r := range rays {
		s := int(to)
		first := true
		for {
			s += r.delta
			if s < 0 || s > 255 || s&0x88 != 0 {
				cyc += 13 // masked step off-board exit
				break
			}
			b := board(byte(s))
			if b == 0 {
				cyc += 19 // empty walk step
				first = false
				continue
			}
			cyc += 25 // classification at an occupied square
			typ := int(b & 0x07)
			bySide := b & 0x08
			if byte(s) == from {
				// The mover: its square vacates when it lands on `to`;
				// x-ray continues (it is NOT re-added — it is the first
				// capturer, already accounted).
				first = false
				continue
			}
			attacks := false
			switch typ {
			case 1: // pawn: adjacent, right direction, diagonal ray
				if first && r.diag {
					if bySide == 0 { // white pawn attacks upward
						attacks = r.delta == -0x0F || r.delta == -0x11
					} else {
						attacks = r.delta == 0x0F || r.delta == 0x11
					}
				}
			case 3:
				attacks = r.diag
			case 4:
				attacks = !r.diag
			case 5:
				attacks = true
			case 6:
				attacks = first
			}
			if !attacks {
				break // blocker that never joins the exchange ends the ray
			}
			cyc += 10
			atk = append(atk, seeAttacker{seeVal10[typ], bySide})
			if typ == 6 {
				break // nothing x-rays through a king usefully
			}
			first = false
			// continue walking: battery pieces behind this attacker
		}
	}

	// Swap: we (side) just captured victim with attacker; enemy to move.
	// Values in tens; iterative swap, then the seeValue negamax fold.
	cyc += 20 // swap entry
	g := []int{seeVal10[victim&7]}
	aVal := seeVal10[attacker&7]
	stm := side ^ 0x08
	used := make([]bool, len(atk))
	for {
		// least-valuable live attacker of stm (linear min-scan)
		best, bestI := 1<<30, -1
		live := 0
		for i, a := range atk {
			if used[i] || a.side != stm {
				continue
			}
			live++
			if a.val < best {
				best, bestI = a.val, i
			}
		}
		cyc += uint64(9*live + 16) // min-scan + loop control
		if bestI < 0 {
			break
		}
		cyc += 45 // remove + 16-bit gain arithmetic + ply bookkeeping
		g = append(g, aVal-g[len(g)-1])
		aVal = best
		used[bestI] = true
		stm ^= 0x08
	}
	for d := len(g) - 1; d > 0; d-- {
		if -g[d] < g[d-1] { // g[d-1] = -max(-g[d-1], g[d]) = min(g[d-1], -g[d])
			g[d-1] = -g[d]
		}
	}
	return cyc, g[0] < 0
}

// TestSEEVariantCost drives the mirror engine (asm-matched 0x1f + SEE
// classification) at fixed depth over the bench positions and prices every
// classification on the true operand distribution. The mode-2 defended scan
// is priced by modelB — the exact model of the ADOPTED attacked()
// implementation. Also cross-checks modelSEESwap's verdict against the
// mirror's full seeValue sign (in value-tens space).
func TestSEEVariantCost(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	}
	var calls, defended uint64
	var cycPawn, cycAtk, cycSwap uint64
	var swapPlies, swapDisagree uint64
	var maxAtk, maxSwap uint64

	for _, fen := range fens {
		pos, err := mirror.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		eng := mirror.NewEngine() // asm-matched: 0x1f, recap2, shipped weights
		eng.SEE = mirror.SEEParams{Mode: 2, DeferFW: true, PruneQS: true}
		eng.SEEProbeHook = func(m mirror.Move) {
			p := &eng.Pos
			board := func(sq byte) byte { return p.Board[sq] }
			side := p.Side
			enemy := side ^ 8
			var psq [16]byte
			copy(psq[:], p.PieceSq[int(enemy)<<1:int(enemy)<<1+16])
			calls++

			c1, _ := modelSEEPawn(m.To, side, board)
			cycPawn += c1

			c2, hit, _ := seeModelB(m.To, enemy, psq, board)
			c2 += 20 // glue: stx ATSQ, lda SIDE, eor #8, sta ATSIDE, branch
			cycAtk += c2
			if c2 > maxAtk {
				maxAtk = c2
			}
			if hit {
				defended++
			}

			victim := int(p.Board[m.To] & 7)
			attacker := int(p.Board[m.From] & 7)
			c3, losing := modelSEESwap(m.From, m.To, side, victim, attacker, board)
			cycSwap += c3
			if c3 > maxSwap {
				maxSwap = c3
			}
			// Cross-check vs the mirror's full SEE sign (tens-space rounding
			// can flip near-zero exchanges; count disagreements honestly).
			if ref := eng.SeeValueForTest(m); (ref < 0) != losing {
				swapDisagree++
			}
			_ = swapPlies
		}
		eng.SetPosition(pos)
		eng.SearchFixed(6)
	}
	t.Logf("classifications: %d  defended %.1f%%", calls, 100*float64(defended)/float64(calls))
	t.Logf("mode 1 pawn-defended:    %6.1f cyc/call avg", float64(cycPawn)/float64(calls))
	t.Logf("mode 2 attacked-defended:%6.1f cyc/call avg (max %d)", float64(cycAtk)/float64(calls), maxAtk)
	t.Logf("mode 3 boolean-SEE swap: %6.1f cyc/call avg (max %d)  verdict disagree vs seeValue: %.2f%%",
		float64(cycSwap)/float64(calls), maxSwap, 100*float64(swapDisagree)/float64(calls))
}

// seeModelB adapts attackdist_test.go's modelB (the exact cycle model of
// the adopted attacked() implementation) for reuse here.
func seeModelB(atsq, atside byte, psq [16]byte, board func(byte) byte) (uint64, bool, scanStats) {
	return modelB(atsq, atside, psq, board)
}
