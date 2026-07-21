package chesstest

import (
	"fmt"
	"testing"
)

// TestAttackedDistribution (diagnostic): instruments every attacked()
// entry during realistic searches and evaluates exact 6502 cycle models
// of candidate attacked() designs on the true operand distribution:
//
//	A: the round-2 16-slot high-to-low SMC loop scan
//	B: unrolled two-body slot scan (sbc tombstone-carry trick, X=ATT77,
//	   jsr'd shared geometric tail) -- ADOPTED in round 3; model A now
//	   describes the superseded implementation, kept as the yardstick
//	   that drove the decision
//	C: superpiece target-outward scan (8 masked knight probes + 8
//	   masked ray walks with first-blocker classification) -- REJECTED:
//	   on the measured operand distribution it saves less than B
//	   (-14.3% vs -17.2%) while costing ~2x the code and taking on the
//	   off-board dead-space masking obligations
//
// Model A was validated against the emulator's actual per-call cycles
// (entry-to-exit, within 5%) so the comparison is grounded, not vibes.
// NOTE: the "ACTUAL measured" line only equals model A when run against
// the round-2 baseline binary; on the current binary it measures the
// adopted design (round 4: the RATTACK reversed-table slot filter with
// the "advance" scan order — see costRATT in board4_test.go, which
// prices that design; models A/B/C remain the historical yardsticks).
func TestAttackedDistribution(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	attackedAddr := labels["attacked"]
	attackedEnd := labels["make"] // attacked body ends where make begins
	atsqA, atsideA := defs["ATSQ"], defs["ATSIDE"]
	boardA, psqA := defs["BOARD"], defs["PIECESQ"]

	var calls, hits uint64
	var cycA, cycB, cycB2, cycC uint64 // modeled cycles
	var actualA uint64          // measured cycles inside attacked() (routine body)
	var liveSlots, tombs, geoRels, walkSteps, occTarget uint64

	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetBudget(m, defs, 0, 5)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		inAttacked := false
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
			// measure actual cycles spent between entry and the rts
			// region: approximate by attributing cycles while PC is
			// within [attacked, attacked+0x80) -- the routine is small
			// and self-contained (helpers inline).
			if pc == attackedAddr {
				inAttacked = true
				calls++
				actualA += uint64(cycles)
				atsq := m.Mem.Main[atsqA]
				atside := m.Mem.Main[atsideA]
				board := func(sq byte) byte { return m.Mem.Main[boardA+uint16(sq)] }
				half := uint16(atside) << 1
				var psq [16]byte
				for i := range psq {
					psq[i] = m.Mem.Main[psqA+half+uint16(i)]
				}
				a, hit, st := modelA(atsq, atside, psq, board)
				b, hitB, _ := modelB(atsq, atside, psq, board)
				var psqRev [16]byte
				for i := range psqRev {
					psqRev[i] = psq[15-i]
				}
				b2, hitB2, _ := modelB(atsq, atside, psqRev, board)
				c, hitC := modelC(atsq, atside, board)
				if hit != hitB || hit != hitC || hit != hitB2 {
					t.Fatalf("model disagreement: A=%v B=%v C=%v atsq=%02x atside=%d", hit, hitB, hitC, atsq, atside)
				}
				cycA += a
				cycB += b
				cycB2 += b2
				cycC += c
				if hit {
					hits++
				}
				liveSlots += st.live
				tombs += st.tomb
				geoRels += st.geo
				walkSteps += st.walk
				if board(atsq) != 0 {
					occTarget++
				}
			} else if inAttacked {
				if pc >= attackedAddr && pc < attackedEnd {
					actualA += uint64(cycles)
				} else {
					inAttacked = false
				}
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
	}
	t.Logf("calls=%d hits=%d (%.1f%%) live/call=%.2f tomb/call=%.2f geo/call=%.2f walksteps/call=%.2f targetOcc=%.1f%%",
		calls, hits, 100*float64(hits)/float64(calls),
		float64(liveSlots)/float64(calls), float64(tombs)/float64(calls),
		float64(geoRels)/float64(calls), float64(walkSteps)/float64(calls),
		100*float64(occTarget)/float64(calls))
	t.Logf("model A (current): %d cycles (%.1f/call); ACTUAL measured: %d (%.1f/call)",
		cycA, float64(cycA)/float64(calls), actualA, float64(actualA)/float64(calls))
	t.Logf("model B (unrolled sbc scan): %d cycles (%.1f/call)  %+.1f%% vs A",
		cycB, float64(cycB)/float64(calls), 100*(float64(cycB)-float64(cycA))/float64(cycA))
	t.Logf("model B ascending-slot order: %d cycles (%.1f/call)  %+.1f%% vs A",
		cycB2, float64(cycB2)/float64(calls), 100*(float64(cycB2)-float64(cycA))/float64(cycA))
	t.Logf("model C (superpiece): %d cycles (%.1f/call)  %+.1f%% vs A",
		cycC, float64(cycC)/float64(calls), 100*(float64(cycC)-float64(cycA))/float64(cycA))
}

// Go mirrors of the engine's tables (must match cmd/gentables).
var attackTabGo, deltaTabGo [256]byte
var typeAtk2Go = [16]byte{0, 0x10, 0x01, 0x04, 0x08, 0x0C, 0x02, 0, 0, 0x20, 0x01, 0x04, 0x08, 0x0C, 0x02, 0}

func init() {
	// build ATTACKTAB/DELTATAB exactly as gentables does: for every
	// (from,to) on-board pair, diff = to-from+0x77.
	sliders := []struct {
		delta int
		bit   byte
	}{
		{0x0F, 0x04}, {0x11, 0x04}, {-0x0F, 0x04}, {-0x11, 0x04},
		{0x10, 0x08}, {-0x10, 0x08}, {0x01, 0x08}, {-0x01, 0x08},
	}
	for from := 0; from < 128; from++ {
		if from&0x88 != 0 {
			continue
		}
		for _, s := range sliders {
			for to, n := from+s.delta, 1; to&0x88 == 0 && to >= 0 && to < 128; to, n = to+s.delta, n+1 {
				d := byte(to - from + 0x77)
				attackTabGo[d] |= s.bit
				deltaTabGo[d] = byte(s.delta)
				if n == 1 {
					attackTabGo[d] |= 0x02 // king
				}
			}
		}
		for _, kd := range []int{0xDF - 0x100, 0xE1 - 0x100, 0xEE - 0x100, 0xF2 - 0x100, 0x0E, 0x12, 0x1F, 0x21} {
			to := from + kd
			if to >= 0 && to < 128 && to&0x88 == 0 {
				attackTabGo[byte(to-from+0x77)] |= 0x01
			}
		}
		// pawns: white pawn on from attacks from+0x0F/from+0x11
		for _, pd := range []int{0x0F, 0x11} {
			if to := from + pd; to < 128 && to&0x88 == 0 {
				attackTabGo[byte(to-from+0x77)] |= 0x10
			}
			if to := from - pd; to >= 0 && to&0x88 == 0 {
				attackTabGo[byte(to-from+0x77)] |= 0x20
			}
		}
	}
}

type scanStats struct{ live, tomb, geo, walk uint64 }

// modelA: exact cycle count of the current attacked() implementation.
func modelA(atsq, atside byte, psq [16]byte, board func(byte) byte) (uint64, bool, scanStats) {
	var st scanStats
	cyc := uint64(25) // entry
	for x := 15; x >= 0; x-- {
		loop := uint64(5) // dex+bpl taken
		if x == 0 {
			loop = 4
		}
		from := psq[x]
		if from == 0xFF {
			st.tomb++
			cyc += 9 + loop
			continue
		}
		st.live++
		diff := atsq - from + 0x77
		bits := attackTabGo[diff]
		if bits == 0 {
			cyc += 22 + loop
			continue
		}
		st.geo++
		cyc += 21 // lda,cmp,beq(nt),eor,adc,tay,lda,beq(nt) = 4+2+2+2+3+2+4+2
		cyc += 6  // sty DIFF, sta ATBITS
		cyc += 4 + 3 + 2 + 4 + 2 + 2 + 4 + 3 // reload from, sta ATTMP, tay, lda BOARD, and, tay, lda TYPEATK2, and ATBITS
		pieceAtk := typeAtk2Go[board(from)&0x0F] & bits
		if pieceAtk == 0 {
			cyc += 3 + loop // beq taken
			continue
		}
		cyc += 2 + 2 // beq nt, cmp #ATK_DIAG
		if pieceAtk < 0x04 {
			cyc += 3 + 8 // bcc taken, sec+rts
			return cyc, true, st
		}
		cyc += 2 + 2 // bcc nt, cmp #ATK_WPAWN
		if pieceAtk >= 0x10 {
			cyc += 3 + 8
			return cyc, true, st
		}
		cyc += 2                 // bcs nt
		cyc += 3 + 4 + 3 + 3     // ldy DIFF, lda DELTATAB, sta ATDELTA, lda ATTMP
		delta := deltaTabGo[diff]
		sq := from
		blocked := false
		for {
			sq += delta
			st.walk++
			cyc += 2 + 3 + 3 // clc, adc, cmp ATSQ
			if sq == atsq {
				cyc += 3 + 8 // beq taken, sec+rts
				return cyc, true, st
			}
			cyc += 2 + 2 + 4 // beq nt, tay, lda BOARD
			if board(sq) != 0 {
				cyc += 3 + loop // bne taken
				blocked = true
				break
			}
			cyc += 2 + 2 + 3 // bne nt, tya, jmp
		}
		_ = blocked
	}
	return cyc + 8, false, st // bpl nt accounted in loop=4; clc+rts=8
}

// modelB: proposed unrolled two-body scan.
// entry: lda ATSIDE, bne (2 white/3 black), lda ATSQ, clc, adc #$77,
// tax, sec.  Per slot: [sec if entered via tombstone bcc] txa, sbc
// PIECESQ+n, bcc next_tomb, tay, lda ATTACKTAB,y, beq next; geometric:
// jsr atgeo; bcc next_tomb; rts.
func modelB(atsq, atside byte, psq [16]byte, board func(byte) byte) (uint64, bool, scanStats) {
	var st scanStats
	cyc := uint64(16)
	if atside != 0 {
		cyc = 17
	}
	for x := 15; x >= 0; x-- {
		from := psq[x]
		if from == 0xFF {
			st.tomb++
			cyc += 2 + 4 + 3 // txa, sbc, bcc taken
			if x != 0 {
				cyc += 2 // sec at next slot's tomb entry
			}
			// x==0: bcc lands on plain rts (carry already clear)
			continue
		}
		st.live++
		diff := atsq - from + 0x77
		bits := attackTabGo[diff]
		if bits == 0 {
			cyc += 2 + 4 + 2 + 2 + 4 + 3 // txa,sbc,bcc nt,tay,lda,beq taken
			if x == 0 {
				cyc-- // slot 0 falls through to clc+rts: beq nt(2)... use taken to amiss anyway; keep 3
				cyc++
			}
			continue
		}
		st.geo++
		cyc += 2 + 4 + 2 + 2 + 4 + 2 // ... beq not taken
		cyc += 6                     // jsr atgeo
		// atgeo body:
		cyc += 3 + 3 + 2 + 3 // sty DIFF, sta ATBITS, txa, sbc DIFF -> from
		cyc += 3 + 2 + 4 + 2 + 2 + 4 + 3 // sta ATTMP, tay, lda BOARD, and #$0F, tay, lda TYPEATK2, and ATBITS
		pieceAtk := typeAtk2Go[board(from)&0x0F] & bits
		if pieceAtk == 0 {
			cyc += 3 + 2 + 6 // beq miss taken, clc, rts
			cyc += 3         // stub bcc taken to next tomb entry
			if x != 0 {
				cyc += 2 // sec
			}
			continue
		}
		cyc += 2 + 2 // beq nt, cmp #ATK_DIAG
		hit := false
		if pieceAtk < 0x04 {
			cyc += 3 + 2 + 6 // bcc taken, sec, rts
			hit = true
		} else {
			cyc += 2 + 2 // bcc nt, cmp #ATK_WPAWN
			if pieceAtk >= 0x10 {
				cyc += 3 + 2 + 6
				hit = true
			} else {
				cyc += 2 + 3 + 4 + 3 + 3 // bcs nt, ldy DIFF, lda DELTATAB, sta ATDELTA, lda ATTMP
				delta := deltaTabGo[diff]
				sq := from
				for {
					sq += delta
					st.walk++
					cyc += 2 + 3 + 3
					if sq == atsq {
						cyc += 3 + 2 + 6
						hit = true
						break
					}
					cyc += 2 + 2 + 4
					if board(sq) != 0 {
						cyc += 3 + 2 + 6 // bne miss, clc, rts
						break
					}
					cyc += 2 + 2 + 3
				}
			}
		}
		if hit {
			cyc += 2 + 6 // stub: bcc nt, rts (returns to caller)
			return cyc, true, st
		}
		cyc += 3 // stub bcc taken
		if x != 0 {
			cyc += 2 // sec
		}
	}
	return cyc + 2 + 6, false, st // amiss: clc, rts
}

// modelC: superpiece. Two color bodies; knight probes with running
// square in A; 8 unrolled rays with masked steps and first-blocker
// classification via 16-byte tables indexed by nibble^ATSIDE.
func modelC(atsq, atside byte, board func(byte) byte) (uint64, bool) {
	cyc := uint64(3 + 2) // lda ATSIDE, bne
	// knight probes
	cyc += 3 + 8 // lda ATSQ; entry precompute KN nibble: lda/ora/sta
	knightOff := []int{-0x21, -0x1F, -0x12, -0x0E, 0x0E, 0x12, 0x1F, 0x21}
	for _, d := range knightOff {
		sq := int(atsq) + d
		// clc, adc #delta, tax, and #$88, bne
		cyc += 2 + 2 + 2 + 2
		if sq < 0 || sq > 255 || sq&0x88 != 0 {
			cyc += 3 + 2 // bne taken, txa at skip
			continue
		}
		cyc += 2 + 4 + 2 + 3 // bne nt, lda BOARD,x, and #$0F, cmp KN
		if board(byte(sq))&0x0F == atside|2 {
			cyc += 3 + 8 // beq hit, sec+rts
			return cyc, true
		}
		cyc += 2 + 2 // beq nt, txa
	}
	// rays: first step classification includes king + pawn
	type ray struct {
		delta int
		diag  bool
	}
	rays := []ray{{0x0F, true}, {0x11, true}, {-0x0F, true}, {-0x11, true},
		{0x10, false}, {-0x10, false}, {0x01, false}, {-0x01, false}}
	for _, r := range rays {
		sq := int(atsq) + r.delta
		cyc += 3 + 2 + 2 + 2 + 2 // lda ATSQ, clc, adc, tax, and #$88
		if sq < 0 || sq > 255 || sq&0x88 != 0 {
			cyc += 3 // bne nextdir taken
			continue
		}
		cyc += 2 + 4 // bne nt, lda BOARD,x
		first := true
		for {
			b := board(byte(sq))
			if b != 0 {
				// classification: bne firstblock taken(3)/later path
				nib := (b & 0x0F) ^ atside
				var attacks bool
				if nib&0x08 == 0 { // attacker's color
					typ := nib & 0x07
					if r.diag {
						attacks = typ == 3 || typ == 5
						if first {
							attacks = attacks || typ == 6
							// pawn: attacker pawn attacks atsq if delta
							// direction opposes pawn's push direction
							if typ == 1 {
								if atside == 0 { // white attacker sits below atsq
									attacks = attacks || r.delta == -0x0F || r.delta == -0x11
								} else {
									attacks = attacks || r.delta == 0x0F || r.delta == 0x11
								}
							}
						}
					} else {
						attacks = typ == 4 || typ == 5
						if first {
							attacks = attacks || typ == 6
						}
					}
				}
				cyc += 3 + 2 + 3 + 2 + 4 // bne taken, and #$0F, eor ATSIDE, tay, lda TAB,y
				if attacks {
					cyc += 3 + 8 // bne hit, sec+rts
					return cyc, true
				}
				cyc += 2 // bne nt -> nextdir (jmp folded as fallthrough approximation)
				break
			}
			// empty: continue walk: [beq walk 3] txa,clc,adc,tax,and,bne,lda
			cyc += 3
			sq += r.delta
			cyc += 2 + 2 + 2 + 2 + 2
			if sq < 0 || sq > 255 || sq&0x88 != 0 {
				cyc += 3
				break
			}
			cyc += 2 + 4 // bne nt, lda BOARD,x
			first = false
		}
	}
	return cyc + 2 + 6, false // clc, rts
}

var _ = fmt.Sprintf
