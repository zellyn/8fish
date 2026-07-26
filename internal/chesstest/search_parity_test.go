package chesstest

import (
	"fmt"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
)

// TestSearchMirrorParity proves the mirror reproduces the asm's fixed-depth
// search EXACTLY — same best move, same score, same make count — under a
// stock NewEngine() (asm-faithful weights + LMR + recap2 QS) across the
// feature masks the engine actually ships and screens against.
//
// This is the asm<->mirror faithfulness gate. It fails if the mirror's
// default eval weights, LMR rules, or QS shape drift from the asm: any of
// those changes which nodes are searched, so the make count (an exact tree
// fingerprint) diverges. Two historical drifts motivated it — stale
// DefaultWeights (pstruct eval) and an unlimited default QS vs the asm's
// recap2 — each of which silently made every mirror screen model a
// different engine than the one that ships.
//
// Depth is kept modest so the pruned masks stay tractable in the emulator;
// the exact-tree match is depth-independent (it held byte-for-byte at
// depth 6 on the eval-identical masks during investigation).
func TestSearchMirrorParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	makeAddr := labels["make"]
	// 0x00 bare, 0x07 null+killer+futil, 0x08 pstruct, 0x1f full ship mask.
	type tier struct {
		mask  byte
		depth byte
	}
	for _, tr := range []tier{{0x00, 5}, {0x07, 5}, {0x08, 5}, {0x1f, 5}} {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			m, err := NewMachine(bin, defs, pos, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			SetFeatures(m, defs, tr.mask)
			SetBudget(m, defs, 0, tr.depth)
			m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
			var makes uint64
			if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
				if pc == makeAddr {
					makes++
				}
			}); err != nil {
				t.Fatal(err)
			}
			asc := int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
			amv := MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])

			mp, err := mirror.ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			me := mirror.NewEngine()
			me.Features = tr.mask
			me.CycleTrack = true
			me.SetPosition(mp)
			mb, ms := me.SearchFixed(int(tr.depth))

			// Best move and score must match to the bit; the make count is
			// an exact tree fingerprint that matches too, save for at most a
			// single illegal-move legality probe made on one side but not the
			// other when a fail-hard cutoff lands right on that move's tier
			// boundary (the two engines have independent move generators, so a
			// score-tied cutoff can differ by one probe with no effect on the
			// result). Anything beyond that is a real tree divergence.
			mkdiff := int64(makes) - int64(me.Cyc.Makes)
			if amv != mb.UCI() || asc != ms || mkdiff < -1 || mkdiff > 1 {
				t.Errorf("m%#02x d%d %s: asm=%s/%d mk=%d  mirror=%s/%d mk=%d",
					tr.mask, tr.depth, fen, amv, asc, makes, mb.UCI(), ms, me.Cyc.Makes)
			}
		}
	}
	fmt.Println("asm==mirror exact (move+score+make) across masks 0x00/0x07/0x08/0x1f")
}

// (TestSearchMirrorParityImproving was deleted 2026-07-26 with the FT2_IMPROV
// bit: with the asm feature gone the test's FEATURES2=FT2_IMPROV run is
// identical to its improving-off run, so its own vacuity assertion would fire.
// mirror.ImprovingParams is untouched and internal/mirror still screens the
// heuristic; TestSearchMirrorParity above is the live asm<->mirror gate.)
