package chesstest

import (
	"fmt"
	"testing"
)

// microABRow is one recorded fingerprint, in the loop order below:
// tier-major (0x1F depth 6, then 0x07 depth 5, then 0x00 depth 4), each
// over the six FENs in order.
type microABRow struct {
	mask                                             byte
	score                                            int16
	move                                             string
	search, make_, eval, attacked, ttprobe, generate uint64
}

// microABGolden pins the tree. Recorded 2026-07-30, immediately after the
// pre-make evasion filter landed.
//
// WHY THIS TABLE EXISTS. This test used to only PRINT the fingerprint and
// assert that the searches completed; its own comment said "run before and
// after a change and diff". That is a reasonable manual instrument and a
// useless automated one — and for a week every agent brief in this project
// cited "TestMicroAB fingerprints identical" as THE tree-identity gate, so
// every agent could truthfully report it passing without any tree having been
// compared to anything at all. The asm-vs-mirror parity gates
// (TestFullGameMirrorParity, TestIDIterationParity, TestTTSequenceParity,
// TestBudgetModeParity, TestSearchMirrorParity, TestGenDeferTreeIdentity)
// were doing the real work throughout. Now this one does its share.
//
// UPDATING IT IS ALLOWED, AND MUST BE ARGUED. A change that legitimately
// removes wasted work moves `make`/`attacked` while leaving search, eval,
// ttprobe, generate, score and move untouched — that is exactly what the
// evasion filter did, and its signature is make and attacked falling by the
// SAME amount. A change that moves score, move, or the node counts has
// changed the TREE: that is a bug, or a feature needing an SPRT. Never a
// silent table update.
var microABGolden = []microABRow{
	{0x1f, -40, "a3a4", 53273, 54507, 49821, 33618, 8495, 8384},
	{0x1f, 354, "e1c1", 89096, 89726, 83136, 51213, 13262, 13935},
	{0x1f, -38, "e2a6", 105654, 110128, 97883, 115313, 24985, 16827},
	{0x1f, 84, "c3d5", 74627, 75467, 66420, 36192, 18723, 14615},
	{0x1f, 96, "b4f4", 4807, 5455, 4141, 3327, 1366, 1262},
	{0x1f, -18, "d4c6", 25474, 25617, 24065, 14142, 4039, 3734},
	{0x07, -24, "a3a4", 55781, 56742, 50855, 32916, 9593, 8679},
	{0x07, 349, "e1c1", 81098, 81509, 75289, 46082, 8006, 8165},
	{0x07, 7, "d5e6", 106475, 110351, 99233, 92426, 5490, 7578},
	{0x07, 77, "c3d5", 54679, 55196, 48443, 24602, 5634, 7681},
	{0x07, 102, "b4f4", 2247, 2620, 1890, 1797, 432, 548},
	{0x07, -29, "f1e2", 37634, 37897, 34672, 22066, 4745, 4274},
	{0x00, -16, "a3a4", 18616, 18869, 16121, 12610, 1434, 2342},
	{0x00, 343, "e1c1", 63364, 63660, 53890, 30022, 5452, 9207},
	{0x00, 14, "d5e6", 14888, 15407, 11900, 18695, 2400, 2798},
	{0x00, 64, "c3d5", 51639, 52101, 43276, 22799, 5121, 7859},
	{0x00, 93, "b4f4", 1229, 1494, 854, 1090, 297, 318},
	{0x00, -42, "f1e2", 29072, 29389, 25387, 15567, 2649, 3398},
}

// TestMicroAB is an exact-tree A/B fingerprint for the cycle-shaving review.
// For a fixed suite at fixed depth under several feature masks it checks
// per-case entry counts of search/make/eval/attacked/ttprobe/generate (which
// pin down tree shape and code-path traversal exactly) plus score and move
// against microABGolden, and LOGS total cycles — cycles are the win, so they
// are reported and never asserted.
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
	idx := 0
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

			if idx < len(microABGolden) {
				w := microABGolden[idx]
				if mask != w.mask || score != w.score || MoveUCI(bf, bt, bfl) != w.move {
					t.Errorf("row %d (m%02x %s): TREE CHANGED — got sc=%d mv=%s, want sc=%d mv=%s. "+
						"A moved score or best move is a tree change: a bug, or a feature needing "+
						"an SPRT — not a golden-table update.",
						idx, mask, fen[:24], score, MoveUCI(bf, bt, bfl), w.score, w.move)
				}
				want := []uint64{w.search, w.make_, w.eval, w.attacked, w.ttprobe, w.generate}
				for i := range probes {
					if counts[i] != want[i] {
						t.Errorf("row %d (m%02x %s): %s = %d, want %d (delta %+d)",
							idx, mask, fen[:24], probes[i].name, counts[i], want[i],
							int64(counts[i])-int64(want[i]))
					}
				}
			}
			idx++
		}
	}
	t.Logf("GRAND TOTAL CYCLES: %d", grand)
}
