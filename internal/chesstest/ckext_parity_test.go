package chesstest

import (
	"fmt"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
)

// ftCkExt is FT_CKEXT (asm defs.inc): check extensions in the main search.
const ftCkExt = 0x40

// ckExtFENs span the shapes where a check extension can change the tree:
// middlegames with forcing checks, tactical positions, an in-check root
// (evasion node at ply 0), endgames where quiet checks are common, and
// positions whose forcing lines run into quiescence (where the extension must
// NOT fire at qs capture nodes but MUST fire at in-check evasion nodes, since
// the mirror models those as full-width nodes).
var ckExtFENs = []string{
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",  // middlegame
	"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",  // middlegame
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", // Kiwipete
	"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",                          // rook endgame
	"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",   // middlegame
	"4k3/8/4K3/8/8/8/4P3/8 w - - 0 1",                                     // KPK
	"8/8/8/3k4/8/3K4/3Q4/8 w - - 0 1",                                     // KQK: checks galore
	"3r1rk1/pp3ppp/2p1bn2/q7/2BP4/P1N1P3/1P2QPPP/R4RK1 w - - 0 15",        // tactical
	"2kr3r/ppp1qppp/2n1bn2/2b5/4P3/2NP1N2/PPPBQPPP/2KR3R w - - 0 10",      // opposite castling
	"8/8/8/4k3/8/4r3/8/4K3 w - - 0 1",                                     // ROOT IN CHECK
	"r2q1rk1/1b1nbppp/p2ppn2/1p6/3NPP2/2N1B3/PPPQB1PP/2KR3R w - - 0 12",   // sicilian
	"1k1r4/pp1b1R2/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - - 0 1",                // mate-ish tactic
	"6k1/5ppp/8/8/8/8/5PPP/R5K1 w - - 0 1",                                // rook vs bare king
}

// asmCkExtRun runs the asm engine at fixed depth with FEATURES 0x1f (+the
// check-extension bit when on) and returns move, score and make count — the
// exact-tree fingerprint the mirror must reproduce.
func asmCkExtRun(t *testing.T, bin []byte, makeAddr uint16, fen string, depth byte, on bool) (string, int, uint64) {
	t.Helper()
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	mask := byte(0x1f)
	if on {
		mask |= ftCkExt
	}
	SetFeatures(m, defs, mask)
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

// mirrorCkExtRun is the mirror side: stock NewEngine at mask 0x1f, with
// CheckExtParams{MaxExt: 1} when on.
func mirrorCkExtRun(t *testing.T, fen string, depth int, on bool) (string, int, uint64) {
	t.Helper()
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	me := mirror.NewEngine()
	me.Features = 0x1f
	if on {
		me.CheckExt = mirror.CheckExtParams{MaxExt: 1}
	}
	// Model the asm's TT ply-adjustment defect (see Engine.TTPlyQuirk): the
	// asm classifies mate scores with an UNSIGNED hi >= $74 compare, so every
	// NEGATIVE stored score is ply-shifted. Without this the two engines'
	// trees diverge here — not because of check extensions, but because the
	// deeper extended tree finally makes that pre-existing shift flip a TT
	// cutoff. With it, the asm and mirror trees are node-for-node and
	// store-for-store identical both with and without extensions (verified by
	// full node/TT-store traces during the port).
	me.TTPlyQuirk = true
	me.CycleTrack = true
	me.SetPosition(mp)
	mb, ms := me.SearchFixed(depth)
	return mb.UCI(), ms, me.Cyc.Makes
}

// TestCheckExtMirrorParity is the FEATURE-ON parity gate for check extensions
// (FT_CKEXT / mirror CheckExtParams{MaxExt: 1}). The extension decisions ARE
// the tree, so an exact best-move + score + make-count match proves the asm
// reproduces the mirror's decisions node for node: the gives-check signal, the
// one-per-path budget (NUMEXT vs numExt), the never-in-quiescence rule
// (QSKIND[parent] vs qsKind[ply], so an in-check evasion node past the horizon
// DOES extend in both), and the re-search path re-deriving the same extension.
//
// The ±1 make tolerance is the same one TestSearchMirrorParity documents: the
// two engines have independent move generators, so a score-tied fail-hard
// cutoff can differ by a single legality probe with no effect on the result.
func TestCheckExtMirrorParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	makeAddr := labels["make"]
	const depth = 5
	var active, extraNodes, offNodes int64
	for _, fen := range ckExtFENs {
		amv, asc, amk := asmCkExtRun(t, bin, makeAddr, fen, depth, true)
		mmv, msc, mmk := mirrorCkExtRun(t, fen, depth, true)
		mkdiff := int64(amk) - int64(mmk)
		moveOK := mmv == "none" || amv == mmv
		if !moveOK || asc != msc || mkdiff < -1 || mkdiff > 1 {
			t.Errorf("CKEXT ON d%d %s: asm=%s/%d mk=%d  mirror=%s/%d mk=%d",
				depth, fen, amv, asc, amk, mmv, msc, mmk)
		}
		// The OFF trees must also agree (they are the shipped-mask trees), and
		// ON must actually differ from OFF somewhere or the gate is vacuous.
		aOffMv, aOffSc, aOffMk := asmCkExtRun(t, bin, makeAddr, fen, depth, false)
		mOffMv, mOffSc, mOffMk := mirrorCkExtRun(t, fen, depth, false)
		offdiff := int64(aOffMk) - int64(mOffMk)
		if (mOffMv != "none" && aOffMv != mOffMv) || aOffSc != mOffSc || offdiff < -1 || offdiff > 1 {
			t.Errorf("CKEXT OFF d%d %s: asm=%s/%d mk=%d  mirror=%s/%d mk=%d",
				depth, fen, aOffMv, aOffSc, aOffMk, mOffMv, mOffSc, mOffMk)
		}
		if amk != aOffMk {
			active++
			// The extension can only ADD nodes; both engines must move the same
			// way (a shared sign check, as in the improving parity gate).
			if (int64(amk)-int64(aOffMk))*(int64(mmk)-int64(mOffMk)) < 0 {
				t.Errorf("on/off make-delta sign mismatch %s: asm %+d mirror %+d",
					fen, int64(amk)-int64(aOffMk), int64(mmk)-int64(mOffMk))
			}
		}
		extraNodes += int64(amk)
		offNodes += int64(aOffMk)
	}
	if active == 0 {
		t.Errorf("check extensions never changed the tree over %d FENs — test is vacuous", len(ckExtFENs))
	}
	fmt.Printf("asm==mirror exact with CHECK EXTENSIONS ON (mask 0x1f|0x40, d%d); "+
		"active on %d/%d FENs; makes %d -> %d (%+.1f%%)\n",
		depth, active, len(ckExtFENs), offNodes, extraNodes,
		100*float64(extraNodes-offNodes)/float64(offNodes))
}

// TestCheckExtPathBudget proves the per-path cap is honoured and balanced: at
// the end of a search NUMEXT must be back to 0 (the mirror's
// TestCheckExtNumExtBalanced), and it must never exceed 1 (MaxExt) at any
// point during the search — sampled by watching every write path through the
// increment site. A leaked counter would silently disable all further
// extensions; an unbounded one would explode the tree.
func TestCheckExtPathBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	numext := defs["NUMEXT"]
	if numext == 0 {
		t.Fatal("NUMEXT not defined in defs.inc")
	}
	searchAddr := labels["search"]
	for _, fen := range ckExtFENs[:6] {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1f|ftCkExt)
		SetBudget(m, defs, 0, 5)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		var maxSeen, extended uint64
		if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
			if pc == searchAddr {
				v := uint64(m.Mem.Main[numext])
				if v > maxSeen {
					maxSeen = v
				}
				if v != 0 {
					extended++
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if got := m.Mem.Main[numext]; got != 0 {
			t.Errorf("%s: NUMEXT left at %d after the search (unbalanced)", fen, got)
		}
		if maxSeen > 1 {
			t.Errorf("%s: NUMEXT reached %d, above MaxExt=1", fen, maxSeen)
		}
		if extended == 0 {
			t.Errorf("%s: no node was ever searched inside an extension", fen)
		}
	}
	fmt.Println("check-extension path budget: NUMEXT <= 1 throughout and 0 on exit")
}
