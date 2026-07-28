package chesstest

import (
	"testing"
)

// TestHashConsumptionShipped is TestHashConsumptionExact's oracle run in the
// regime the SHIPPED engine actually uses, which the original cannot see:
//
//   - iterative deepening against a WARM transposition table (the original
//     runs fixed-depth mode: one iteration, cold TT), and
//   - FEATURES2 = FT2_GENDEFER, where a full-width node with a TT move
//     performs no generation at all and reaches make() through sntry's
//     staged record instead of through pass 0.
//
// Both change WHICH nodes probe/store the TT and therefore which nodes
// consume HASH0-3 — exactly the population the r3 hash elision (HVALID)
// reasons about. At every consumption point (ttprobe, ttstore, sreploop)
// the incremental hash must still equal a fresh recomputation from the
// engine's own key tables and live board state, and every same-parity
// HASHSTK entry the repetition scan reads must be exact.
func TestHashConsumptionShipped(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: profiled iterative-deepening searches with per-consumption verification")
	}
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ZKEYS", "STMKEY", "CASTKEYS", "EPKEYS", "ttprobe", "ttstore", "sreploop", "sntry"} {
		if _, ok := labels[name]; !ok {
			t.Fatalf("label %s missing from engine.lbl", name)
		}
	}
	bin := loadEngine(t)
	cases := []struct {
		fen   string
		depth byte
	}{
		{"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 6},
		{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 5},
		{"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 3 9", 7},
		{"8/8/4kpp1/3p1b2/p6P/2B5/6P1/6K1 b - - 12 44", 7},
		// en passant live, castling rights live: the two move kinds the
		// slow make path and ttmovevalid reconstruct by hand.
		{"rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3", 6},
	}
	for _, tc := range cases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1F|byte(defs["FT_CKEXT"]))
		SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
		// Saturated budget: real iterative deepening 1..depth (so the TT
		// is warm and sntry actually fires) without the clock ever
		// deciding anything. Same device gendefer_identity_test.go uses.
		SetBudget(m, defs, 2_000_000_000, tc.depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove

		zkeys := labels["ZKEYS"]
		fresh := func() (h [4]byte) {
			mem := m.Mem.Main
			for slot := uint16(0); slot < 32; slot++ {
				sq := mem[0x0800+slot]
				if sq == 0xFF {
					continue
				}
				piece := mem[uint16(defs["BOARD"])+uint16(sq)]
				if piece == 0 {
					continue
				}
				kind := uint16(piece&7) - 1 + 6*uint16(piece>>3&1)
				for p := range h {
					h[p] ^= mem[zkeys+kind*512+uint16(p)*128+uint16(sq)]
				}
			}
			if mem[defs["SIDE"]] != 0 {
				for p := range h {
					h[p] ^= mem[labels["STMKEY"]+uint16(p)]
				}
			}
			for p := range h {
				h[p] ^= mem[labels["CASTKEYS"]+4*uint16(mem[defs["CASTLE"]])+uint16(p)]
			}
			if ep := mem[defs["EPSQ"]]; ep != 0xFF {
				for p := range h {
					h[p] ^= mem[labels["EPKEYS"]+4*uint16(ep&7)+uint16(p)]
				}
			}
			return h
		}

		var shadow [32][4]byte
		tp, ts, rl := labels["ttprobe"], labels["ttstore"], labels["sreploop"]
		mk, mn, sn := labels["make"], labels["makenull"], labels["sntry"]
		checks, staged, bad := 0, 0, 0
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, _ uint8) {
			mem := m.Mem.Main
			if pc == mk || pc == mn {
				shadow[mem[defs["PLY"]]] = fresh()
				return
			}
			if pc == sn {
				staged++
				return
			}
			if pc != tp && pc != ts && pc != rl {
				return
			}
			checks++
			if bad > 0 {
				return
			}
			var got [4]byte
			copy(got[:], mem[defs["HASH0"]:defs["HASH0"]+4])
			if got != fresh() {
				bad++
				t.Errorf("%s: HASH mismatch at pc=%04x (consumption #%d): got %x want %x",
					tc.fen, pc, checks, got, fresh())
			}
			if pc == rl {
				ply, t2 := int(mem[defs["PLY"]]), int(mem[defs["T2"]])
				for x := ply - 2; x >= t2 && x >= 0; x -= 2 {
					var e [4]byte
					for p := range e {
						e[p] = mem[defs["HASHSTK0"]+uint16(p)*0x20+uint16(x)]
					}
					if e != shadow[x] {
						bad++
						t.Errorf("%s: HASHSTK[%d] stale at rep scan (ply %d): got %x want %x",
							tc.fen, x, ply, e, shadow[x])
						break
					}
				}
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("%s: exited=%v code=%d err=%v", tc.fen, exited, code, err)
		}
		if staged == 0 {
			t.Errorf("%.24s: sntry never fired — the deferred-generation regime was not exercised", tc.fen)
		}
		t.Logf("%.24s depth %d: %d consumption points verified, %d deferred (sntry) nodes",
			tc.fen, tc.depth, checks, staged)
	}
}
