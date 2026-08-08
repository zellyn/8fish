package chesstest

import (
	"math/rand"
	"testing"

	"github.com/zellyn/chess6502/harness"
	"github.com/zellyn/chess6502/internal/mirror"
	"github.com/zellyn/chess6502/internal/refchess"
)

// ft2Mopup is FT2_MOPUP (asm defs.inc): the phase-gated endgame mop-up
// eval-term gate ported in this task.
const ft2Mopup = 0x02

// callSub runs the emulated CPU as a subroutine: it points the PC at addr
// and single-steps until the routine's final rts pops the stack back past
// the entry SP (SP == sp0+2). evalinit/eval touch no I/O and no LC-resident
// code, so they run correctly without the normal $4000 entry setup.
func callSub(t *testing.T, m *harness.Machine, addr uint16) {
	t.Helper()
	sp0 := m.CPU.SP()
	target := byte(sp0 + 2)
	m.CPU.SetPC(addr)
	for i := 0; i < 20_000_000; i++ {
		if err := m.CPU.Step(); err != nil {
			t.Fatalf("callSub(%04x) step: %v", addr, err)
		}
		if m.CPU.SP() == target {
			return
		}
	}
	t.Fatalf("callSub(%04x) never returned", addr)
}

// asmStaticEval returns the asm engine's STATIC eval for fen (side-to-move
// POV, tempo, no dither) under FEATURES 0x1f + the given FEATURES2 byte, by
// invoking evalinit then eval directly and reading SCORE. This is exactly
// what a search node's stand-pat sees.
//
// fgpatch (board.s) runs between poking the feature bytes and eval, because
// eval's feature gates are constant-folded jmp operands set from FEATURES/
// FEATURES2 once per iterate (deep opt r6): a driver that bypasses iterate —
// as this trampoline does — must run the fold itself, exactly as the real
// drivers do. The engine's OWN routine is called, not a Go re-implementation,
// so the fold under test is the shipped one.
func asmStaticEval(t *testing.T, bin []byte, evalinit, evalAddr uint16, fen string, ft2 byte) int {
	t.Helper()
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatalf("chesstest ParseFEN %q: %v", fen, err)
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetFeatures(m, defs, 0x1f)
	SetFeatures2(m, defs, ft2)
	m.Mem.Main[defs["SEED"]] = 0 // no dither: deterministic
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	if labels["fgpatch"] == 0 {
		t.Fatal("fgpatch label missing from engine.lbl")
	}
	callSub(t, m, labels["fgpatch"])
	callSub(t, m, evalinit)
	callSub(t, m, evalAddr)
	return int(int16(uint16(m.Mem.Main[defs["SCORE"]]) |
		uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
}

// refMopupEval returns the mirror's static eval with the mop-up term set to
// DefaultMopup (on) or the zero value (off), Features FtAll, no dither.
func refMopupEval(t *testing.T, fen string, on bool) int {
	t.Helper()
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		t.Fatalf("mirror ParseFEN %q: %v", fen, err)
	}
	me := mirror.NewEngine() // Features FtAll, DefaultWeights
	me.SetPosition(mp)
	if on {
		me.Mopup = mirror.DefaultMopup
	}
	me.Seed = 0
	return me.Eval()
}

// mopupFireFENs are positions where the term MUST fire: low-phase endgames
// with a >= rook material edge, from varied king/piece placements, both
// colors winning (KxK and its color-flip). KBNK is the hard mate; KPK-win
// and KPK-draw exercise the pawn-edge boundary (a lone pawn = 100 < 450 so
// KPK never fires — a good negative control inside the "endgame" set).
var mopupFireFENs = []string{
	// KQK
	"8/8/8/4k3/8/8/8/3QK3 w - - 0 1",
	"7k/8/8/8/8/8/8/Q3K3 w - - 0 1",
	"8/8/4k3/8/8/8/8/Q2K4 b - - 0 1",
	"3qk3/8/8/8/4K3/8/8/8 w - - 0 1", // black winning (KQ vs K)
	// KRK
	"8/8/8/4k3/8/8/8/R3K3 w - - 0 1",
	"7k/8/8/8/8/8/8/R3K3 b - - 0 1",
	"4k3/8/8/8/8/8/8/R3K3 w - - 0 1",
	"r3k3/8/8/8/8/8/8/4K3 w - - 0 1", // black winning (KR vs K)
	// KRRK / KBBK / KBNK / KQ vs KR
	"3k4/8/8/8/8/8/8/R3K1R1 w - - 0 1",
	"8/8/8/4k3/8/8/8/2B1KB2 w - - 0 1",
	"7k/8/8/8/8/8/8/2B1KB2 b - - 0 1",
	"8/8/8/4k3/8/8/8/2BNK3 w - - 0 1",
	"8/8/8/3rk3/8/8/8/3QK3 w - - 0 1", // KQ vs KR, white leads by 475 >= 450
	// pawn endgames: a lone pawn (100) is < 450, so the gate stays SHUT —
	// these must match mopup-OFF exactly (negative controls in the set).
	"8/8/8/3k4/8/3K4/3P4/8 w - - 0 1",
	"8/8/8/8/8/k7/P7/K7 w - - 0 1",
}

// mopupQuietFENs are middlegame/opening positions above the phase gate: the
// term must NEVER fire (mop-up ON eval == mop-up OFF eval == mirror).
var mopupQuietFENs = []string{
	"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", // rook endgame, phase 4 but ~equal material
}

// TestMopupEvalParity is the KEY gate: the asm static eval with FT2_MOPUP ON
// must EQUAL the mirror's Eval() with Mopup=DefaultMopup, to the centipawn,
// on (a) the endgame positions where the term fires (KQK/KRK/KRRK/KBBK/KBNK/
// KQvKR, both colors), (b) pawn/quiet endgames where it must stay silent,
// and (c) middlegame positions. It additionally pins that ON==OFF wherever
// the mirror says the gate is shut (no middlegame leak, asm side), and that
// the asm OFF eval still equals the mirror with Mopup off (the term is a
// pure, exactly-mirrored addition). Analogue of TestRookTermsParity /
// TestPStructParity for the mop-up term.
func TestMopupEvalParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: thousands of engine runs")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	evalinit, evalAddr := labels["evalinit"], labels["eval"]
	if evalinit == 0 || evalAddr == 0 {
		t.Fatalf("labels missing: evalinit=%#x eval=%#x", evalinit, evalAddr)
	}

	check := func(fen string, wantFire bool) {
		t.Helper()
		asmOn := asmStaticEval(t, bin, evalinit, evalAddr, fen, ft2Mopup)
		asmOff := asmStaticEval(t, bin, evalinit, evalAddr, fen, 0x00)
		refOn := refMopupEval(t, fen, true)
		refOff := refMopupEval(t, fen, false)
		// The core gate: asm ON == mirror ON, exactly.
		if asmOn != refOn {
			t.Errorf("ON eval mismatch %q: asm=%d mirror=%d (asmOff=%d refOff=%d)",
				fen, asmOn, refOn, asmOff, refOff)
		}
		// The OFF path stays exactly the mirror's mop-up-off eval.
		if asmOff != refOff {
			t.Errorf("OFF eval mismatch %q: asm=%d mirror=%d", fen, asmOff, refOff)
		}
		// Firing bookkeeping: the mirror and asm agree on WHETHER it fired.
		refFired := refOn != refOff
		asmFired := asmOn != asmOff
		if refFired != asmFired {
			t.Errorf("fire disagreement %q: asmFired=%v mirrorFired=%v", fen, asmFired, refFired)
		}
		if refFired != wantFire {
			t.Errorf("gate expectation %q: fired=%v want=%v (refOn=%d refOff=%d)",
				fen, refFired, wantFire, refOn, refOff)
		}
	}

	fires := 0
	for _, fen := range mopupFireFENs {
		// KPK positions carry a lone pawn (100 < 450), so the gate stays
		// shut even though they are endgames; everything else fires.
		want := true
		if fen == "8/8/8/3k4/8/3K4/3P4/8 w - - 0 1" ||
			fen == "8/8/8/8/8/k7/P7/K7 w - - 0 1" {
			want = false
		}
		check(fen, want)
		if want {
			fires++
		}
	}
	for _, fen := range mopupQuietFENs {
		check(fen, false) // above the phase gate: never fires
	}

	// Breadth: thousands of positions from random legal games. Almost all
	// are middlegames (gate shut) and must match exactly ON and OFF; the
	// occasional low-phase decisive endgame that surfaces must match too.
	rng := rand.New(rand.NewSource(0x309))
	positions, games := 0, 0
	for games = 0; games < 120 && positions < 2500; games++ {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for ply := 0; ply < 80; ply++ {
			legal := ref.LegalMoves()
			if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
				break
			}
			if err := ref.Make(legal[rng.Intn(len(legal))]); err != nil {
				t.Fatal(err)
			}
			fen := ref.FEN()
			positions++
			asmOn := asmStaticEval(t, bin, evalinit, evalAddr, fen, ft2Mopup)
			refOn := refMopupEval(t, fen, true)
			if asmOn != refOn {
				asmOff := asmStaticEval(t, bin, evalinit, evalAddr, fen, 0x00)
				t.Fatalf("random-corpus ON mismatch %q: asm=%d mirror=%d (asmOff=%d)",
					fen, asmOn, refOn, asmOff)
			}
		}
	}
	t.Logf("mop-up eval parity: %d curated + %d random positions, all exact; %d fired",
		len(mopupFireFENs)+len(mopupQuietFENs), positions, fires)
}
