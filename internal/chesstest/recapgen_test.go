package chesstest

import (
	"fmt"
	"os"
	"hash/fnv"
	"testing"

	"github.com/zellyn/chess6502/harness"
)

// This file verifies the attacker-driven recapture generator (genrecap,
// the RECAPONLY=1 path of generateq) two independent ways:
//
//  1. TestRecapGenEquivalence calls generateq directly (via an injected
//     JSR stub) and, for every enemy-occupied square of every corpus
//     position, compares its RECAPONLY=1 emission SEQUENCE against the
//     RECAPONLY=0 full quiescence emissions filtered to that square -
//     the exact definition of the old generate-then-filter behavior,
//     including order. Empty and own-piece squares (unreachable as
//     RECAPSQ in real search; see the genrecap comment) must emit
//     nothing.
//
//  2. TestRecapGenTreeIdentity runs full fixed-depth searches on the
//     old (pre-genrecap) binary and the new one, hashing the
//     (FROM,TO,MVFLAGS) stream at every make() entry. Identical hashes
//     mean identical trees - the change is then a pure cycle saving.
//     The old binary+labels must be present in oldBinDir (they are a
//     build product of the parent commit, not committed here); the
//     test skips if absent.

// callGenerateq injects "jsr generateq; lda #0; sta EXIT" at $0300 and
// runs it over pos with the given RECAPONLY/RECAPSQ, returning the
// emitted (from,to,flags) triples in emission order.
func callGenerateq(t *testing.T, bin []byte, labels map[string]uint16,
	pos *Position, recapOnly, recapSq byte) [][3]byte {
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
		copy(m.Mem.Main[board+base:board+base+8], pos.Board[base:base+8])
	}
	psq := defs["PIECESQ"]
	copy(m.Mem.Main[psq:psq+32], pos.PieceSq[:])
	m.Mem.Main[defs["SIDE"]] = pos.Side
	m.Mem.Main[defs["EPSQ"]] = pos.EpSq
	m.Mem.Main[defs["CASTLE"]] = pos.Castle
	m.Mem.Main[defs["RECAPONLY"]] = recapOnly
	m.Mem.Main[defs["RECAPSQ"]] = recapSq
	// MSP -> bottom of the move stack
	const moveStack = 0x0E00
	m.Mem.Main[defs["MSP"]] = byte(moveStack & 0xFF)
	m.Mem.Main[defs["MSP"]+1] = byte(moveStack >> 8)
	genq, ok := labels["generateq"]
	if !ok {
		t.Fatal("no generateq label")
	}
	stub := []byte{0x20, byte(genq), byte(genq >> 8), 0xA9, 0x00, 0x8D, 0xFF, 0xBF}
	copy(m.Mem.Main[stubAddr:], stub)
	exited, code, err := m.Run(10_000_000)
	if err != nil || !exited || code != 0 {
		t.Fatalf("generateq stub: exited=%v code=%d err=%v", exited, code, err)
	}
	end := uint16(m.Mem.Main[defs["MSP"]]) | uint16(m.Mem.Main[defs["MSP"]+1])<<8
	var moves [][3]byte
	for p := uint16(moveStack); p < end; p += 4 { // 4 bytes: tier,from,to,flags
		moves = append(moves, [3]byte{m.Mem.Main[p+1], m.Mem.Main[p+2], m.Mem.Main[p+3]})
	}
	return moves
}

// recapCorpus: the perft suite positions, the WAC tactics, and crafted
// promotion-recapture / edge cases (promo recaptures are rare in
// organic corpora, so they get explicit coverage, both colors, edge
// files, x-ray-blocked sliders, multiple same-type attackers).
var recapCorpus = []string{
	"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1",
	"rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8",
	"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
	"2rr3k/pp3pp1/1nnqbN1p/3pN3/2pP4/2P3Q1/PPB4P/R4RK1 w - - 0 1",
	"8/7p/5k2/5p2/p1p2P2/Pr1pPK2/1P1R3P/8 b - - 0 1",
	"r1bq2rk/pp3pbp/2p1p1pQ/7P/3P4/2PB1N2/PP3PPR/2KR4 w - - 0 1",
	"5k2/6pp/p1qN4/1p1p4/3P4/2PKP2Q/PP3r2/3R4 b - - 0 1",
	"r4q1k/p2bR1rp/2p2Q1N/5p2/5p2/2P5/PP3PPP/R5K1 w - - 0 1",
	"2br2k1/2q3rn/p2NppQ1/2p1P3/Pp5R/4P3/1P3PPP/3R2K1 w - - 0 1",
	"4k1r1/2p3r1/1pR1p3/3pP2p/3P2qP/P4N2/1PQ4P/5RK1 b - - 0 1",
	// white recaptures on d8 with e7/c7 pawns: promo captures
	"3r1k2/2P1P3/8/8/8/8/8/4K3 w - - 0 1",
	// black recaptures on d1 with c2/e2 pawns: promo captures
	"4k3/8/8/8/8/8/2p1p3/3R1K2 b - - 0 1",
	// a-file edge: promo recapture from b7 only (no a-file wrap)
	"R2r1k2/1P6/8/8/8/8/8/4K3 w - - 0 1",
	// h-file edge, black
	"4k3/8/8/8/8/8/6p1/5K1R b - - 0 1",
	// x-ray: queen behind rook on the recapture file, both same file
	"3r1k2/8/8/8/3R4/8/3Q4/4K3 w - - 0 1",
	// two knights covering one square, king adjacent, ep square set
	"r2qk2r/ppp2ppp/2n2n2/3pP3/8/2N2N2/PPP2PPP/R1BQK2R w KQkq d6 0 2",
	// queens and bishops crossing on a diagonal target
	"6k1/1q6/2b5/8/4N3/5B2/6Q1/6K1 b - - 0 1",
}

func TestRecapGenEquivalence(t *testing.T) {
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	states, mismatches := 0, 0
	for _, fen := range recapCorpus {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatalf("%s: %v", fen, err)
		}
		// full quiescence emissions once per position
		full := callGenerateq(t, bin, labels, pos, 0, 0)
		for sq := byte(0); sq < 0x78; sq++ {
			if sq&0x88 != 0 {
				continue
			}
			got := callGenerateq(t, bin, labels, pos, 1, sq)
			states++
			occ := pos.Board[sq]
			enemy := occ != 0 && (occ^pos.Side)&0x08 != 0
			var want [][3]byte
			if enemy {
				// definition of the old filter: full emissions with TO==sq
				for _, mv := range full {
					if mv[1] == sq {
						want = append(want, mv)
					}
				}
			} // else: empty or own square - genrecap must emit nothing
			if fmt.Sprint(got) != fmt.Sprint(want) {
				mismatches++
				t.Errorf("%s recapsq=%s (enemy=%v):\n  got  %v\n  want %v",
					fen, SqName(sq), enemy, fmtMoves(got), fmtMoves(want))
			}
		}
	}
	t.Logf("%d (position, square) states compared, %d mismatches", states, mismatches)
}

func fmtMoves(moves [][3]byte) []string {
	out := make([]string, len(moves))
	for i, mv := range moves {
		out[i] = MoveUCI(mv[0], mv[1], mv[2]) + fmt.Sprintf("/f%02x", mv[2])
	}
	return out
}

const oldBinDir = "/tmp/recapgen-baseline"

// searchTrace runs a fixed-depth search and returns (move, score,
// make-trace hash, make count, cycles). The trace hash folds in the
// (FROM,TO,MVFLAGS) bytes at every entry to make(): equal hashes mean
// the two engines made the same moves in the same order everywhere in
// the tree.
func searchTrace(t *testing.T, bin []byte, labels map[string]uint16,
	fen string, depth byte) (string, int16, uint64, uint64, uint64) {
	t.Helper()
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	var cout bytesBuffer
	m, err := NewMachine(bin, defs, pos, 0, &cout)
	if err != nil {
		t.Fatal(err)
	}
	SetBudget(m, defs, 0, depth)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	makeAddr := labels["make"]
	if makeAddr == 0 {
		t.Fatal("no make label")
	}
	fromA, toA, flagsA := defs["FROM"], defs["TO"], defs["MVFLAGS"]
	h := fnv.New64a()
	var makes uint64
	exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
		if pc == makeAddr {
			makes++
			h.Write([]byte{m.Mem.Main[fromA], m.Mem.Main[toA], m.Mem.Main[flagsA]})
		}
	})
	if err != nil || !exited || code > 2 {
		t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
	}
	score := int16(uint16(m.Mem.Main[defs["SCORE"]]) |
		uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
	return cout.String(), score, h.Sum64(), makes, m.Cycles
}

type bytesBuffer struct{ b []byte }

func (w *bytesBuffer) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }
func (w *bytesBuffer) String() string              { return string(w.b) }

func TestRecapGenTreeIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	oldBin, err := readFileMaybe(oldBinDir + "/engine.bin")
	if err != nil {
		t.Skipf("old baseline binary not present: %v", err)
	}
	oldLabels, err := ParseLabelFile(oldBinDir + "/engine.lbl")
	if err != nil {
		t.Skip("old baseline labels not present")
	}
	newBin := loadEngine(t)
	newLabels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"2rr3k/pp3pp1/1nnqbN1p/3pN3/2pP4/2P3Q1/PPB4P/R4RK1 w - - 0 1",
		"8/7p/5k2/5p2/p1p2P2/Pr1pPK2/1P1R3P/8 b - - 0 1",
		"3r1k2/2P1P3/8/8/8/8/8/4K3 w - - 0 1",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
	}
	var oldTot, newTot uint64
	for i, fen := range fens {
		depth := byte(5)
		if i < 2 && !testing.Short() {
			depth = 6
		}
		om, os, oh, on, oc := searchTrace(t, oldBin, oldLabels, fen, depth)
		nm, ns, nh, nn, nc := searchTrace(t, newBin, newLabels, fen, depth)
		oldTot += oc
		newTot += nc
		status := "IDENTICAL"
		if oh != nh || om != nm || os != ns || on != nn {
			status = "DIVERGED"
			t.Errorf("%s: old move=%s score=%d makes=%d hash=%x; new move=%s score=%d makes=%d hash=%x",
				fen[:16], om, os, on, oh, nm, ns, nn, nh)
		}
		t.Logf("%-16s d%d %s: makes=%d cycles old=%dk new=%dk (%+.1f%%)",
			fen[:16], depth, status, nn, oc/1000, nc/1000,
			100*(float64(nc)-float64(oc))/float64(oc))
	}
	t.Logf("suite cycles: old=%dM new=%dM (%+.2f%%)", oldTot/1e6, newTot/1e6,
		100*(float64(newTot)-float64(oldTot))/float64(oldTot))
}

func readFileMaybe(path string) ([]byte, error) {
	return os.ReadFile(path)
}
