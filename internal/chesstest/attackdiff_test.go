package chesstest

import (
	"os"
	"testing"

	"github.com/zellyn/8fish/harness"
)

// TestAttackedDifferential compares the current attacked() against a
// baseline binary answer-for-answer: for a corpus of positions (the
// recapture corpus, the microab/WAC suites, plus board snapshots taken
// mid-search so promoted pieces, sparse endgames and tombstone-heavy
// piece lists are represented), it calls attacked() for EVERY on-board
// square with BOTH attacker sides and requires identical carry answers.
//
// The baseline build (engine.bin + engine.lbl of the parent commit)
// must be present in attackedBaselineDir; the test skips if absent.
var attackedBaselineDir = os.Getenv("ATTACKED_BASELINE")

type boardState struct {
	board [128]byte
	psq   [32]byte
}

// callAttacked injects "jsr attacked; lda #0; rol a; sta EXIT" and
// returns the carry answer for (atsq, atside) on the given state.
func callAttacked(t *testing.T, bin []byte, attackedAddr uint16,
	st *boardState, atsq, atside byte) bool {
	t.Helper()
	const stubAddr = 0x0300
	m, err := harness.New(harness.Config{
		Bin:          bin,
		Org:          0x4000,
		Entry:        stubAddr,
		CoutAddr:     0xBFF0,
		ExitAddr:     0xBFFF,
		InAddr:       0xBFF1,
		InStatusAddr: 0xBFF2,
		ClockAddr:    0xBFF4,
	})
	if err != nil {
		t.Fatal(err)
	}
	board := defs["BOARD"]
	for rank := uint16(0); rank < 8; rank++ {
		base := rank * 16
		copy(m.Mem.Main[board+base:board+base+8], st.board[base:base+8])
	}
	psq := defs["PIECESQ"]
	copy(m.Mem.Main[psq:psq+32], st.psq[:])
	m.Mem.Main[defs["ATSQ"]] = atsq
	m.Mem.Main[defs["ATSIDE"]] = atside
	stub := []byte{0x20, byte(attackedAddr), byte(attackedAddr >> 8),
		0xA9, 0x00, 0x2A, 0x8D, 0xFF, 0xBF}
	copy(m.Mem.Main[stubAddr:], stub)
	exited, code, err := m.Run(1_000_000)
	if err != nil || !exited || code > 1 {
		t.Fatalf("attacked stub: exited=%v code=%d err=%v", exited, code, err)
	}
	return code == 1
}

func TestAttackedDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: differential corpus")
	}
	if attackedBaselineDir == "" {
		attackedBaselineDir = "/tmp/attacked-baseline"
	}
	oldBin, err := os.ReadFile(attackedBaselineDir + "/engine.bin")
	if err != nil {
		t.Skipf("baseline binary not present: %v", err)
	}
	oldLabels, err := ParseLabelFile(attackedBaselineDir + "/engine.lbl")
	if err != nil {
		t.Skip("baseline labels not present")
	}
	newBin := loadEngine(t)
	newLabels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	oldAddr, newAddr := oldLabels["attacked"], newLabels["attacked"]
	if oldAddr == 0 || newAddr == 0 {
		t.Fatal("missing attacked label")
	}

	// static corpus
	var states []*boardState
	fens := append([]string{}, recapCorpus...)
	fens = append(fens,
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	)
	for _, tc := range wacCases {
		fens = append(fens, tc.fen)
	}
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatalf("%s: %v", fen, err)
		}
		st := &boardState{}
		copy(st.board[:], pos.Board[:])
		copy(st.psq[:], pos.PieceSq[:])
		states = append(states, st)
	}

	// mid-search snapshots: every 200th make() of a depth-4 search on
	// four seed positions, capped, so the corpus includes tombstoned
	// lists, promotions, castled kings and quiet endgames.
	snapSeeds := []string{
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
	}
	boardA, psqA := defs["BOARD"], defs["PIECESQ"]
	for _, fen := range snapSeeds {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(newBin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetBudget(m, defs, 0, 4)
		makeAddr := newLabels["make"]
		var makes, taken int
		_, _, err = m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
			if pc == makeAddr {
				makes++
				if makes%200 == 0 && taken < 60 {
					taken++
					st := &boardState{}
					copy(st.board[:], m.Mem.Main[boardA:boardA+128])
					copy(st.psq[:], m.Mem.Main[psqA:psqA+32])
					states = append(states, st)
				}
			}
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	var checked, attacked, mismatches int
	for si, st := range states {
		for sq := byte(0); sq < 0x78; sq++ {
			if sq&0x88 != 0 {
				continue
			}
			for _, side := range []byte{0, 8} {
				oldAns := callAttacked(t, oldBin, oldAddr, st, sq, side)
				newAns := callAttacked(t, newBin, newAddr, st, sq, side)
				checked++
				if oldAns {
					attacked++
				}
				if oldAns != newAns {
					mismatches++
					if mismatches < 20 {
						t.Errorf("state %d sq=%s side=%d: old=%v new=%v",
							si, SqName(sq), side, oldAns, newAns)
					}
				}
			}
		}
	}
	t.Logf("%d states x squares x sides checked over %d positions (%d attacked=true), %d mismatches",
		checked, len(states), attacked, mismatches)
}
