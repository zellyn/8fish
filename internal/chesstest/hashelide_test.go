package chesstest

import (
	"testing"
)

// TestHashConsumptionExact: the r3 quiescence hash elision (HVALID in
// defs.inc) skips Zobrist maintenance for qs makes whose child provably
// never consumes the hash. This oracle proves the "provably" part: at
// EVERY point the search consumes HASH0-3 - ttprobe entry, ttstore
// entry, and every repetition-scan compare iteration (sreploop) - the
// incremental hash must equal a fresh Go-side recomputation from the
// engine's own key tables and live board state.
// TestHashElisionCoverage: cheap path-count diagnostic - proves the
// elision fast paths, the deferred upgrade, and the catchup replay all
// actually execute on representative searches (a green oracle means
// nothing if the rare paths never ran).
func TestHashElisionCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	bin := loadEngine(t)
	fens := []struct {
		fen   string
		depth byte
	}{
		{"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 6},
		{"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 3 9", 8},
		{"8/8/4kpp1/3p1b2/p6P/2B5/6P1/6K1 b - - 12 44", 8},
	}
	probes := []string{"make", "mkfcapoff", "mkfmvoff", "mkfqon", "mkslow", "hashcatchup", "hcreal"}
	for _, tc := range fens {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1F)
		SetBudget(m, defs, 0, tc.depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		addrs := make([]uint16, len(probes))
		for i, p := range probes {
			a, ok := labels[p]
			if !ok {
				t.Fatalf("label %s missing", p)
			}
			addrs[i] = a
		}
		counts := make([]uint64, len(probes))
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, _ uint8) {
			for i := range addrs {
				if pc == addrs[i] {
					counts[i]++
				}
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("%s: exited=%v code=%d err=%v", tc.fen, exited, code, err)
		}
		line := tc.fen[:16]
		for i, p := range probes {
			line += " " + p + "=" + itoa64(counts[i])
		}
		t.Log(line)
	}
}

func itoa64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func TestHashConsumptionExact(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: profiled searches with per-consumption verification")
	}
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ZKEYS", "STMKEY", "CASTKEYS", "EPKEYS", "ttprobe", "ttstore", "sreploop"} {
		if _, ok := labels[name]; !ok {
			t.Fatalf("label %s missing from engine.lbl", name)
		}
	}
	bin := loadEngine(t)
	fens := []struct {
		fen   string
		depth byte
	}{
		// middlegames with deep qs tails
		{"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 6},
		{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 5},
		// endgames where shuffling lines drive the halfmove clock >= 4
		// and repetition scans fire inside quiescence
		{"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 3 9", 6},
		{"8/8/4kpp1/3p1b2/p6P/2B5/6P1/6K1 b - - 12 44", 6},
	}
	for _, tc := range fens {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1F)
		SetBudget(m, defs, 0, tc.depth)
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
					continue // tolerate transient states; entries are live squares
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

		// shadow[p] = true hash of the position that was current when
		// ply p last made a move (what HASHSTK[p] is supposed to hold)
		var shadow [32][4]byte
		tp, ts, rl := labels["ttprobe"], labels["ttstore"], labels["sreploop"]
		mk, mn := labels["make"], labels["makenull"]
		checks, bad := 0, 0
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, _ uint8) {
			mem := m.Mem.Main
			if pc == mk || pc == mn {
				shadow[mem[defs["PLY"]]] = fresh()
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
				// the repetition scan also reads HASHSTK history:
				// every same-parity entry in the window must be exact
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
		t.Logf("%.24s depth %d: %d consumption points verified", tc.fen, tc.depth, checks)
	}
}
