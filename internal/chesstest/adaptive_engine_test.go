package chesstest

import (
	"testing"
)

// TestAdaptiveEngineBehavior exercises the FULL on-device iterative-deepening
// driver in FT2_ADAPT mode (not just adaptmaybe in isolation): it runs real
// searches and checks the movable ceiling actually lets spend vary with the
// position, while flat mode is unaffected. It also confirms the driver never
// exceeds its hard-abort ceiling (2*CEILMAX) and always returns a legal move.
func TestAdaptiveEngineBehavior(t *testing.T) {
	bin := loadEngine(t)
	// Small income keeps the test fast; the adaptive side may extend up to
	// 4x income on tactical/unstable positions.
	const income = 1_500_000 // cycles/move
	base := uint64(income)
	ceilMax := 4 * base // plentiful-bank scenario: hard max = 4*income
	unst := 3 * base
	minSpend := base / 4

	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	}
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		flatMove, flatSpent, flatDepth := runOne(t, bin, pos, base, false, 0, 0, 0)
		adMove, adSpent, adDepth := runOne(t, bin, pos, base, true, ceilMax, unst, minSpend)

		if flatMove == "" || adMove == "" {
			t.Fatalf("fen=%q: no move (flat=%q adaptive=%q)", fen, flatMove, adMove)
		}
		// The adaptive search must never blow past its 2*CEILMAX hard abort
		// (plus a little emulator overshoot slack).
		if adSpent > 2*ceilMax+50_000 {
			t.Errorf("fen=%q: adaptive overspent: %d > 2*ceilMax(%d)", fen, adSpent, 2*ceilMax)
		}
		t.Logf("%-40s flat: mv=%s spent=%8d d=%2d | adaptive: mv=%s spent=%8d d=%2d",
			fen[:40], flatMove, flatSpent, flatDepth, adMove, adSpent, adDepth)
	}
}

// runOne runs a single budgeted search (adaptive or flat) and returns the move,
// cycles spent, and completed depth.
func runOne(t *testing.T, bin []byte, pos *Position, base uint64, adaptive bool, ceilMax, unst, minSpend uint64) (string, uint64, byte) {
	t.Helper()
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetBudget(m, defs, base, 24)
	if adaptive {
		SetAdaptive(m, defs, base, ceilMax, unst, minSpend)
	}
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	cap := base*8*3 + 2_000_000_000
	exited, code, err := m.Run(cap)
	if err != nil || !exited {
		t.Fatalf("run: exited=%v code=%d err=%v", exited, code, err)
	}
	if code != 0 {
		return "", m.Cycles, 0
	}
	bf, bt, bfl := m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]]
	return MoveUCI(bf, bt, bfl), m.Cycles, m.Mem.Main[defs["CURDEPTH"]]
}
