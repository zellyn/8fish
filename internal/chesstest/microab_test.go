package chesstest

import (
	"fmt"
	"testing"
)

// TestMicroAB is an exact-tree A/B fingerprint for the cycle-shaving
// review. For a fixed suite at fixed depth under several feature masks,
// it prints per-case: entry counts of search/make/eval/attacked/ttprobe
// (which pin down tree shape and code-path traversal exactly), plus
// score, move, and total cycles. Run before and after a change and diff:
// counts+score+move must be identical; cycles is the win.
func TestMicroAB(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	type probe struct {
		name string
		addr uint16
	}
	probes := []probe{
		{"search", labels["search"]},
		{"make", labels["make"]},
		{"eval", labels["eval"]},
		{"attacked", labels["attacked"]},
		{"ttprobe", labels["ttprobe"]},
		{"generate", labels["generate"]},
	}
	// depth-tiered: 0x1F (full config) gets depth 6; the unpruned/
	// partial masks explode, so run them shallower but still exercising
	// every code path (null/futility/killer in 0x07, bare loop in 0x00).
	tiers := []struct {
		mask  byte
		depth byte
	}{{0x1F, 6}, {0x07, 5}, {0x00, 4}}
	var grand uint64
	for _, tier := range tiers {
		mask, depth := tier.mask, tier.depth
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			m, err := NewMachine(bin, defs, pos, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			SetFeatures(m, defs, mask)
			SetBudget(m, defs, 0, depth) // fixed depth
			m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
			counts := make([]uint64, len(probes))
			exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
				for i := range probes {
					if pc == probes[i].addr {
						counts[i]++
					}
				}
			})
			if err != nil || !exited || code > 2 {
				t.Fatalf("fen=%q mask=%#x exited=%v code=%d err=%v", fen, mask, exited, code, err)
			}
			score := int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
			bf, bt, bfl := m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]]
			grand += m.Cycles
			line := fmt.Sprintf("m%02x %-24s sc=%6d mv=%s cyc=%10d |", mask, fen[:24], score, MoveUCI(bf, bt, bfl), m.Cycles)
			for i := range probes {
				line += fmt.Sprintf(" %s=%d", probes[i].name, counts[i])
			}
			t.Log(line)
		}
	}
	t.Logf("GRAND TOTAL CYCLES: %d", grand)
}
