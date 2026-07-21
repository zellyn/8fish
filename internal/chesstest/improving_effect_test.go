package chesstest

import (
	"fmt"
	"testing"
)

// TestImprovingEffect measures the improving heuristic (FT2_IMPROV) at fixed
// depth under the ship mask (0x1f): OFF vs ON per FEN, reporting search/make/
// eval entry counts and cycles. It quantifies two things the port must show:
//
//   - the FORCED-EVAL accounting (verification #3): with the feature ON,
//     full-signal forces a static eval at every full-width node that computed
//     none naturally, so the eval count rises by roughly the ~6% share of
//     full-width nodes that skip null/RFP — NOT a per-node double-eval blowup;
//   - the fixed-depth TREE EFFECT (verification #4): late-quiet reductions run
//     one ply deeper when not improving, so the tree (search/make counts)
//     shrinks materially (~-20% at d6 per the mirror screen).
//
// Diagnostic (skipped in -short): run explicitly and read the log.
func TestImprovingEffect(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	// Kept lean (depth 5, 4 FENs) so the diagnostic does not blow the package
	// test timeout; the task report carries the fuller depth-6 / 6-FEN numbers.
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	searchAddr, makeAddr, evalAddr := labels["search"], labels["make"], labels["eval"]
	const mask = 0x1f
	const depth = 5

	run := func(fen string, imp bool) (searches, makes, evals, cyc uint64, mv string, sc int) {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, mask)
		if imp {
			SetFeatures2(m, defs, 0x01)
		}
		SetBudget(m, defs, 0, depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, c uint8) {
			switch pc {
			case searchAddr:
				searches++
			case makeAddr:
				makes++
			case evalAddr:
				evals++
			}
		}); err != nil {
			t.Fatal(err)
		}
		cyc = m.Cycles
		mv = MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
		sc = int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
		return
	}

	var offS, offM, offE, offC, onS, onM, onE, onC uint64
	for _, fen := range fens {
		s0, m0, e0, c0, mv0, sc0 := run(fen, false)
		s1, m1, e1, c1, mv1, sc1 := run(fen, true)
		offS, offM, offE, offC = offS+s0, offM+m0, offE+e0, offC+c0
		onS, onM, onE, onC = onS+s1, onM+m1, onE+e1, onC+c1
		t.Logf("%-40s OFF s=%d mk=%d ev=%d cyc=%d mv=%s/%d | ON s=%d mk=%d ev=%d cyc=%d mv=%s/%d",
			fen[:40], s0, m0, e0, c0, mv0, sc0, s1, m1, e1, c1, mv1, sc1)
	}
	pct := func(on, off uint64) float64 { return 100 * (float64(on)/float64(off) - 1) }
	fmt.Printf("IMPROVING EFFECT (mask 0x1f, d%d, %d FENs):\n", depth, len(fens))
	fmt.Printf("  search: off=%d on=%d (%+.1f%%)\n", offS, onS, pct(onS, offS))
	fmt.Printf("  make:   off=%d on=%d (%+.1f%%)\n", offM, onM, pct(onM, offM))
	fmt.Printf("  eval:   off=%d on=%d (%+.1f%%)  [net: tree shrink masks forcing]\n", offE, onE, pct(onE, offE))
	fmt.Printf("  cycles: off=%d on=%d (%+.1f%%)\n", offC, onC, pct(onC, offC))

	// FORCED-EVAL ISOLATION (verification #3): under a mask with NO LMR bit,
	// improving's reduction application is inert (the smset reduced path needs
	// FT_LMR), so the tree is IDENTICAL off vs on — but full-signal STILL forces
	// an eval at every full-width node that had none. The make count must be
	// unchanged and the eval count must rise by only the forced share (~6% of
	// full-width nodes), never a per-node double-eval blowup (~+100%).
	const noLMR = 0x07 // NULL|KILLER|FUTIL, no FT_LMR ($10)
	run2 := func(fen string, imp bool) (makes, evals uint64) {
		pos, _ := ParseFEN(fen)
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, noLMR)
		if imp {
			SetFeatures2(m, defs, 0x01)
		}
		SetBudget(m, defs, 0, depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, c uint8) {
			switch pc {
			case makeAddr:
				makes++
			case evalAddr:
				evals++
			}
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	var m0t, e0t, m1t, e1t uint64
	for _, fen := range fens {
		m0, e0 := run2(fen, false)
		m1, e1 := run2(fen, true)
		if m0 != m1 {
			t.Errorf("no-LMR full-signal changed the tree on %s: make %d -> %d", fen, m0, m1)
		}
		m0t, e0t, m1t, e1t = m0t+m0, e0t+e0, m1t+m1, e1t+e1
	}
	fmt.Printf("FORCED-EVAL ISOLATION (mask 0x07 no-LMR, d%d, %d FENs):\n", depth, len(fens))
	fmt.Printf("  make:  off=%d on=%d (identical tree: %v)\n", m0t, m1t, m0t == m1t)
	fmt.Printf("  eval:  off=%d on=%d (%+.2f%% = forced-eval rate)\n", e0t, e1t, pct(e1t, e0t))
}
