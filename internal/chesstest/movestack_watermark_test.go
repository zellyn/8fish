package chesstest

import (
	"testing"
)

// TestMoveStackWatermark measures the HIGH-WATER MARK of MSP (the move-stack
// top-of-stack pointer, $10-$11) across real searches. It is the empirical
// basis for the $2000 memory-map decision recorded in docs/book.md: the
// generator's batched emission can run up to ~124 bytes past MOVESTACKTOP
// ($2000) before its flush traps, and the resident opening book is based at
// $2000, so the two regions can only collide if a search actually drives MSP
// to the top of MOVESTACK ($0E00-$1FFF, 4608 bytes / 1152 stacked moves).
//
// MSP is sampled once per executed instruction via RunProfile, so the peak is
// exact, not a statistical estimate.
func TestMoveStackWatermark(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: per-instruction sampling")
	}
	bin := loadEngine(t)
	// Wide, tactical, and endgame positions: the ones that generate the most
	// moves per ply and search the deepest.
	cases := []struct {
		name  string
		fen   string
		depth byte
	}{
		{"kiwipete", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 6},
		{"open-mid", "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 7},
		{"start", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", 7},
		// Maximum-mobility position: 3 queens + 2 rooks, ~100 legal moves.
		{"max-mobility", "3Q4/1Q4Q1/4Q3/2Q4R/Q4Q2/3Q4/1Q4Rp/1K1BBNNk w - - 0 1", 6},
		// Deep pawn endgame: shallow branching but the longest recursions.
		{"pawn-endgame", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 10},
	}
	const movestackBase = 0x0E00
	const movestackTop = 0x2000
	worst := 0
	for _, tc := range cases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Depth is the iterative-deepening cap (MAXDEPTH), not ROOTDEPTH:
		// ROOTDEPTH is the perft binary's knob. budget 0 = fixed depth.
		SetBudget(m, defs, 0, tc.depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		peak := 0
		_, code, err := m.RunProfile(4_000_000_000, func(pc uint16, cycles uint8) {
			msp := int(m.Mem.Main[0x10]) | int(m.Mem.Main[0x11])<<8
			if msp > peak {
				peak = msp
			}
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if code != 0 {
			t.Fatalf("%s: engine exited %d", tc.name, code)
		}
		used := peak - movestackBase
		if used > worst {
			worst = used
		}
		t.Logf("%-14s depth %2d  MSP peak $%04X  used %5d B (%4d moves)  headroom %5d B (%.1f%% of capacity used)",
			tc.name, tc.depth, peak, used, used/4, movestackTop-peak,
			100*float64(used)/float64(movestackTop-movestackBase))
	}
	t.Logf("WORST CASE across suite: %d B of %d (%d of %d move slots), headroom %d B",
		worst, movestackTop-movestackBase, worst/4, (movestackTop-movestackBase)/4,
		(movestackTop-movestackBase)-worst)
	// Regression guard: if a future change drives the move stack anywhere
	// near MOVESTACKTOP, the $2000 book coexistence argument stops holding
	// and this test must fail loudly rather than the book being corrupted.
	if limit := (movestackTop - movestackBase) / 2; worst > limit {
		t.Errorf("move-stack peak %d B exceeds half of capacity (%d B): the resident "+
			"book at $2000 is no longer safely out of the generator's reach", worst, limit)
	}
}
