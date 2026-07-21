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

// TestSearchMirrorParityImproving is the FEATURE-ON parity gate for the
// improving heuristic (FT2_IMPROV / mirror ImprovingParams full-signal LMR).
// It mirrors TestSearchMirrorParity but enables improving on BOTH engines:
// the asm sets FEATURES2=FT2_IMPROV, the mirror sets Improving = full-signal
// LMR (+1 extra reduction when not improving). The reduction decisions shape
// the tree, so an exact best-move + score + make-count match (save the single
// legality-probe tolerance the base gate allows) proves the asm reproduces the
// mirror's improving decisions node-for-node — including the ply-2 static-eval
// comparison and the full-signal forced eval.
//
// Mask 0x1f (the ship config) is the one where the reduction actually fires
// (LMR present); the FEN set spans middlegames, endgames, in-check/evasion
// lines, and eval-swinging tactics so the improving signal flips both ways.
// The test also asserts the heuristic is genuinely ACTIVE: at least one FEN's
// tree must differ from its improving-off tree, else the "parity" is vacuous.
func TestSearchMirrorParityImproving(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",  // middlegame
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",  // middlegame
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", // Kiwipete (checks)
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",                   // rook endgame
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", // middlegame
		"4k3/8/4K3/8/8/8/4P3/8 w - - 0 1",                            // KPK endgame
		"8/8/8/3k4/8/3K4/3Q4/8 w - - 0 1",                            // KQK endgame
		"3r1rk1/pp3ppp/2p1bn2/q7/2BP4/P1N1P3/1P2QPPP/R4RK1 w - - 0 15", // tactical, checks
		"2kr3r/ppp1qppp/2n1bn2/2b5/4P3/2NP1N2/PPPBQPPP/2KR3R w - - 0 10", // opposite castling
		"8/8/8/4k3/8/4r3/8/4K3 w - - 0 1",                            // ROOT IN CHECK (Re3+), legal escapes
		"r2q1rk1/1b1nbppp/p2ppn2/1p6/3NPP2/2N1B3/PPPQB1PP/2KR3R w - - 0 12", // sicilian, swings
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	makeAddr := labels["make"]
	const mask = 0x1f // ship config; the mask where LMR (and so improving) fires
	const depth = 5
	// asm run helper: features + optional FEATURES2 improving bit.
	asmRun := func(fen string, imp bool) (string, int, uint64) {
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
			SetFeatures2(m, defs, 0x01) // FT2_IMPROV
		}
		SetBudget(m, defs, 0, depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		var makes uint64
		if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
			if pc == makeAddr {
				makes++
			}
		}); err != nil {
			t.Fatal(err)
		}
		sc := int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
		mv := MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
		return mv, sc, makes
	}
	// mirror run helper.
	mirrorRun := func(fen string, imp bool) (string, int, uint64) {
		mp, err := mirror.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		me := mirror.NewEngine()
		me.Features = mask
		if imp {
			// ImprovingParams{Mode: improvingFull(=2), LMR: true} — full-signal
			// LMR application, +1 extra reduction (LMRExtra 0 => 1). The exact
			// config the asm FT2_IMPROV bit implements. RFP application omitted.
			me.Improving = mirror.ImprovingParams{Mode: 2, LMR: true}
		}
		me.CycleTrack = true
		me.SetPosition(mp)
		mb, ms := me.SearchFixed(depth)
		return mb.UCI(), ms, me.Cyc.Makes
	}
	var active int
	for _, fen := range fens {
		amv, asc, amk := asmRun(fen, true)
		mmv, msc, mmk := mirrorRun(fen, true)
		mkdiff := int64(amk) - int64(mmk)
		// When the root is mate/stalemate the mirror reports no move ("none")
		// and the asm's BESTFROM/BESTTO are undefined, so the move strings are
		// not comparable; score + make-count still fingerprint the tree exactly.
		moveOK := mmv == "none" || amv == mmv
		if !moveOK || asc != msc || mkdiff < -1 || mkdiff > 1 {
			t.Errorf("IMPROVING m%#02x d%d %s: asm=%s/%d mk=%d  mirror=%s/%d mk=%d",
				mask, depth, fen, amv, asc, amk, mmv, msc, mmk)
		}
		// Cross-check the asm on/off trees to confirm the feature is active,
		// and that the mirror agrees on the SAME on/off delta.
		_, _, amkOff := asmRun(fen, false)
		_, _, mmkOff := mirrorRun(fen, false)
		if amk != amkOff || mmk != mmkOff {
			active++
			// on/off differs; the asm and mirror deltas should have the same sign
			// of effect (improving reduces, so on <= off, allowing the ±1 probe).
			if (int64(amkOff)-int64(amk))*(int64(mmkOff)-int64(mmk)) < 0 {
				t.Errorf("on/off delta sign mismatch %s: asm off-on=%d mirror off-on=%d",
					fen, int64(amkOff)-int64(amk), int64(mmkOff)-int64(mmk))
			}
		}
	}
	if active == 0 {
		t.Errorf("improving heuristic never changed the tree across %d FENs — test is vacuous", len(fens))
	}
	fmt.Printf("asm==mirror exact with improving ON (mask 0x1f, d%d); feature active on %d/%d FENs\n",
		depth, active, len(fens))
}
