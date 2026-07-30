package chesstest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
)

// ---------------------------------------------------------------------------
// Gates for the PRE-MAKE EVASION FILTER (asm/search.s sdevade, plus the
// CHECKERSQ/CHECKDIR plumbing at asm/board.s cksetck/cksetcx).
//
// The filter drops moves at in-check nodes that provably cannot answer the
// check, BEFORE make. Being too permissive is harmless (the move then goes
// through make + attacked() exactly as before); being too STRICT silently
// deletes a legal move, and a deleted legal move is a wrong evaluation, a
// wrong best move, or a missed mate escape. So strictness has to be
// impossible, not merely untested, and that takes three separate things:
//
//  1. TestEvasionFilterDifferential - EXHAUSTIVE over a corpus. For every
//     move the engine considers at an in-check node it recovers the asm's
//     own verdict (did control reach `make` or `sdrej`?) and compares it,
//     in both directions, against an INDEPENDENT legality oracle and
//     against a model of the documented rule. Coverage counters prove the
//     corpus actually contains the shapes that could break it.
//  2. TestEvasionFilterVerify - a LIVE assertion, in a `ca65 -D LEGALVERIFY`
//     build (the CKVERIFY/GDVERIFY pattern) that runs the filter AND the old
//     make + attacked() path at every filtered move and traps on
//     disagreement. It also PROVES ITS OWN TRAP FIRES, by poking LVFORCE to
//     manufacture a disagreement - because the honest limit of a live
//     assertion is that "never fired" and "cannot fire" look identical.
//  3. TestEvasionFilterPlumbing - CHECKERSQ must be a REAL checker at every
//     in-check node, since that is the one fact the filter's soundness
//     argument rests on. (Folded into the differential, which checks it on
//     every move it sees.)
//
// The oracle is shCheckers (structhunt_test.go): the same attack scan the
// structural hunt used to price this change, which agreed with the engine's
// own make + attacked() verdict on all 8,114 in-check moves of the profile
// workload before any of this code existed.
// ---------------------------------------------------------------------------

// evCorpus is the differential's position corpus. It is deliberately
// check-heavy and shape-diverse rather than representative: the coverage
// counters at the end of the differential FAIL the test if any of the shapes
// that could break the filter is missing, so this list is not trusted, it is
// checked.
var evCorpus = []struct {
	name string
	fen  string
}{
	// --- roots already in check (the filter's own node type) ---
	{"rook-check-open-board", "8/8/8/4k3/8/4r3/8/4K3 w - - 0 1"},
	{"queen-check-h4", "rnb1kbnr/pppp1ppp/8/4p3/6Pq/5P2/PPPPP2P/RNBQKBNR w KQkq - 1 3"},
	{"knight-check", "rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8"},
	{"pawn-check", "k7/2P5/1K6/8/8/8/8/8 w - - 0 1"},
	{"bishop-check-long-diag", "r1bqkbnr/pppp1ppp/2n5/4p3/2B5/4PN2/PPPP1PPP/RNBQK2R b KQkq - 3 4"},
	// Double check: king in check from two pieces at once, so NO non-king
	// move is legal and the filter must reject every one of them.
	{"double-check-N+B", "r3k2r/pppp1ppp/8/8/8/2n5/PPPP1PPP/R3K2R w KQkq - 0 1"},
	{"double-check-discovered", "4k3/8/8/8/8/1n6/3K4/7r w - - 0 1"},
	{"double-check-mate-net", "3rk3/8/8/8/8/8/3K4/3q4 w - - 0 1"},
	// Interposition available (a rook/queen check with a gap to block).
	{"interpose-rook-file", "4k3/8/8/8/R7/8/3B4/3K1r2 w - - 0 1"},
	{"interpose-queen-diag", "4k3/8/8/8/7q/8/4R3/4K3 w - - 0 1"},
	{"interpose-long-rank", "4k3/8/8/8/8/8/8/r2N1K1R w - - 0 1"},
	// En passant WHILE in check: the capture that resolves a check by taking
	// the checking pawn off a square that is not the move's TO. The filter
	// must decline to judge these.
	{"ep-under-check", "8/8/8/2k5/3Pp3/8/8/4K3 b - d3 0 1"},
	{"ep-capture-available", "rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3"},
	// Discovered checks: a mover vacating a line between the enemy king and
	// one of its own sliders is what drives asm cknodir/ckdwalk -> ckhitx,
	// i.e. the CHECKERSQ path the DIRECT exit never reaches. The profile
	// workload contains ZERO of these, which is exactly why they are listed.
	{"discovery-rook-behind-N", "4k3/8/8/8/8/8/4N3/4RK2 w - - 0 1"},
	{"discovery-bishop-behind-P", "6k1/8/8/8/3P4/8/1B6/6K1 w - - 0 1"},
	{"discovery-queen-behind-R", "4k3/8/8/8/8/8/4R3/4QK2 w - - 0 1"},
	{"discovery-both-colors", "r5k1/4n3/8/8/8/8/4N3/R5K1 w - - 0 1"},
	// Castling and ep at the PARENT ply: the curincheck paths, which record
	// CHECKERSQ = NOSQ and make the filter fall back.
	{"castling-rich", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1"},
	{"ep-rich", "rnbqkbnr/ppppp1pp/8/8/4Pp2/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 3"},
	// Ordinary middlegames and endgames, so the bulk of the sample is the
	// traffic the filter actually sees in a game.
	{"midgame-1", "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8"},
	{"midgame-2", "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10"},
	{"endgame-pawns", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1"},
	{"endgame-RvR", "r5k1/5ppp/8/8/8/8/5PPP/R5K1 w - - 0 1"},
	{"promotion-race", "8/P6k/8/8/8/8/1p6/K7 w - - 0 1"},
	{"mate-in-few", "1k1r4/pp1b1R2/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - - 0 1"},
}

// evCoverage counts the shapes the corpus must actually contain. Every field
// is required to be nonzero: a coverage counter at zero means the
// differential proved nothing about that shape, which is the failure mode
// that makes "we tested it" a lie.
type evCoverage struct {
	moves       int // in-check moves seen at all
	single      int // ... at nodes with exactly one checker
	double      int // ... at nodes with two or more checkers
	rejected    int // ... the asm filter rejected
	accepted    int // ... the asm filter accepted (or declined to judge)
	permissive  int // ... accepted and yet illegal (harmless; costs cycles)
	interpose   int // ... accepted BECAUSE TO blocks the checking ray
	capture     int // ... accepted BECAUSE TO captures the checker
	kingMove    int // ... declined: king move
	epUnderChk  int // ... declined: en passant capture
	noSq        int // ... declined: CHECKERSQ was NOSQ (curincheck/root)
	knightChk   int // ... checker is a knight (no interposition possible)
	promoUnder  int // ... the move is a promotion
	directRec   int // asm ckhit  entries: DIRECT checker recorded
	discoverRec int // asm ckhitx entries: DISCOVERED checker recorded
}

func (c evCoverage) String() string {
	return fmt.Sprintf(
		"in-check moves %d (single-check %d, double-check %d)\n"+
			"    filter: rejected %d, accepted %d (of which illegal-but-accepted %d)\n"+
			"    accept reasons: captures-checker %d, interposes %d\n"+
			"    declined: king move %d, en passant %d, CHECKERSQ=NOSQ %d\n"+
			"    shapes: knight checker %d, promotion under check %d\n"+
			"    CHECKERSQ writes: direct (ckhit) %d, discovered (ckhitx) %d",
		c.moves, c.single, c.double, c.rejected, c.accepted, c.permissive,
		c.capture, c.interpose, c.kingMove, c.epUnderChk, c.noSq,
		c.knightChk, c.promoUnder, c.directRec, c.discoverRec)
}

// evRayDir is asm DELTATAB[(to - from + $77) & $FF]: the single 0x88 step
// from `from` toward `to` along a queen line, or 0 if they are not on one.
// Written from the geometry rather than read out of the table, so the
// differential does not validate the filter against the same bytes it uses.
func evRayDir(from, to int) int {
	dr, df := to/16-from/16, to%16-from%16
	sgn := func(v int) int {
		if v > 0 {
			return 1
		} else if v < 0 {
			return -1
		}
		return 0
	}
	switch {
	case dr == 0 && df == 0:
		return 0
	case dr == 0:
		return sgn(df)
	case df == 0:
		return 16 * sgn(dr)
	case dr == df || dr == -df:
		return 16*sgn(dr) + sgn(df)
	}
	return 0
}

// evModelRejects is the documented rule the asm filter implements: at an
// in-check node, reject a move iff it is not an ep capture, not a king move,
// the checker square is known, and TO neither IS the checker square nor lies
// strictly between the checker and the king.
func evModelRejects(csq, ksq, from, to int, mover, flags byte, nosq byte) bool {
	if flags&0x08 != 0 { // FL_EP
		return false
	}
	if mover&0x07 == 6 { // KING
		return false
	}
	if csq == int(nosq) {
		return false
	}
	if to == csq {
		return false
	}
	dir := evRayDir(csq, ksq)
	if dir == 0 || evRayDir(csq, to) != dir {
		return true
	}
	return evRayDir(to, ksq) != dir
}

// evLegalAfter applies a non-king, non-castle, non-ep move to a copy of the
// 0x88 board and reports whether the mover's king is left un-attacked, using
// shCheckers as the (independent) attack oracle. This is the ORACLE the
// filter's rejections are judged against: the asm skipped make + attacked()
// for these moves, so their legality has to be established from outside the
// engine, not read back out of it.
func evLegalAfter(board []byte, ksq, from, to int, mover, flags byte, side byte) bool {
	var b [128]byte
	copy(b[:], board)
	final := mover
	if p := flags & 0x07; p != 0 { // FL_PROMO: bits 0-2 are the new type
		final = mover&0xF8 | p
	}
	b[from] = 0
	b[to] = final
	return len(shCheckers(b[:], ksq, side^0x08)) == 0
}

// TestEvasionFilterDifferential is the EXHAUSTIVE legality differential.
//
// For every move the engine considers at an in-check node, across a
// check-heavy corpus, it asserts:
//
//	(1) CHECKERSQ[ply] really is a square the side to move is checked from
//	    (NOSQ excepted) - the single fact the filter's soundness rests on;
//	(2) the asm's verdict equals the documented rule's verdict, EXACTLY, so
//	    the shipped code cannot have drifted from the rule that was proven
//	    sound;
//	(3) BOTH directions of the thing that matters: every REJECTED move is
//	    illegal, and every LEGAL move was accepted.
//
// (2) and (3) are separate on purpose. (3) alone would pass a filter that
// rejects nothing; (2) alone would pass a rule that is wrong. Together they
// say "the code implements the rule, and the rule loses no legal move".
//
// Exact equality with make + attacked() is IMPOSSIBLE by construction and is
// deliberately not asserted: the filter cannot see pins, so a pinned piece
// interposing is accepted here and rejected by attacked(). That direction is
// counted (permissive) and reported, never failed.
func TestEvasionFilterDifferential(t *testing.T) {
	bin := loadEngine(t)
	labels := labelsFor(t)
	sdomove, mk, sdrej := labels["sdomove"], labels["make"], labels["sdrej"]
	ckhit, ckhitx := labels["ckhit"], labels["ckhitx"]
	for n, a := range map[string]uint16{
		"sdomove": sdomove, "make": mk, "sdrej": sdrej, "ckhit": ckhit, "ckhitx": ckhitx,
	} {
		if a == 0 {
			t.Fatalf("label %q missing from asm/engine.lbl", n)
		}
	}
	nosq := byte(defs["NOSQ"])
	boardA, psqA := defs["BOARD"], defs["PIECESQ"]
	inchkA, ckSqA, ckDirA := defs["INCHK"], defs["CHECKERSQ"], defs["CHECKDIR"]
	plyA, sideA := defs["PLY"], defs["SIDE"]
	fromA, toA, flagsA := defs["FROM"], defs["TO"], defs["MVFLAGS"]

	var cov evCoverage
	var fails int

	for _, pos := range evCorpus {
		p, err := ParseFEN(pos.fen)
		if err != nil {
			t.Fatalf("%s: %v", pos.name, err)
		}
		m, err := NewMachine(bin, defs, p, 0, nil)
		if err != nil {
			t.Fatalf("%s: %v", pos.name, err)
		}
		// The SHIPPED configuration, so the differential judges the code that
		// actually runs: all features + check extensions + deferred generation.
		SetFeatures(m, defs, byte(defs["FT_CKEXT"])|0x1F)
		SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
		SetBudget(m, defs, 0, 5) // fixed depth: deterministic and quick
		m.Mem.Main[defs["HALFMOVE"]] = p.Halfmove

		// Per-move snapshot taken at sdomove, BEFORE make: the filter's own
		// input. Resolved when control reaches `make` (accepted) or `sdrej`
		// (rejected) - the two mutually exclusive successors of sdomove.
		type snap struct {
			live       bool
			board      [128]byte
			ply        int
			csq, cdir  int
			ksq        int
			from, to   int
			mover      byte
			flags      byte
			side       byte
			checkers   []int
			modelRejct bool
		}
		var s snap
		judge := func(rejected bool) {
			if !s.live {
				return
			}
			s.live = false
			cov.moves++
			if len(s.checkers) >= 2 {
				cov.double++
			} else {
				cov.single++
			}
			// (1) CHECKERSQ must be a genuine checker.
			if s.csq != int(nosq) {
				found := false
				for _, c := range s.checkers {
					if c == s.csq {
						found = true
					}
				}
				if !found {
					fails++
					if fails < 10 {
						t.Errorf("%s: CHECKERSQ[%d] = $%02X is NOT a checker of the king on $%02X (real checkers %v)\n  fen %s",
							pos.name, s.ply, s.csq, s.ksq, s.checkers, pos.fen)
					}
				}
				// CHECKDIR must be the step from the checker toward the king.
				if want := evRayDir(s.csq, s.ksq) & 0xFF; s.cdir != want {
					fails++
					if fails < 10 {
						t.Errorf("%s: CHECKDIR[%d] = $%02X, want $%02X (checker $%02X -> king $%02X)\n  fen %s",
							pos.name, s.ply, s.cdir, want, s.csq, s.ksq, pos.fen)
					}
				}
			} else {
				cov.noSq++
			}
			// (2) asm verdict == the documented rule.
			if rejected != s.modelRejct {
				fails++
				if fails < 10 {
					t.Errorf("%s: asm rejected=%v but the RULE says %v (from $%02X to $%02X flags $%02X mover $%02X, checker $%02X dir $%02X, king $%02X)\n  fen %s",
						pos.name, rejected, s.modelRejct, s.from, s.to, s.flags,
						s.mover, s.csq, s.cdir, s.ksq, pos.fen)
				}
			}
			// (3) both directions of soundness, against the independent oracle.
			// King moves and ep captures move a second piece / clear a third
			// square, so the simple oracle does not model them - but the
			// filter never rejects them either, so nothing is lost.
			king := s.mover&0x07 == 6
			ep := s.flags&0x08 != 0
			castle := s.flags&0x20 != 0
			if !king && !ep && !castle {
				legal := evLegalAfter(s.board[:], s.ksq, s.from, s.to, s.mover, s.flags, s.side)
				// Direction A: rejected => illegal. Direction B: legal =>
				// accepted. They are contrapositives, so one report suffices,
				// but the failure message names both readings because the
				// second is the one that says why it matters.
				if rejected && legal {
					fails++
					if fails < 10 {
						t.Errorf("UNSOUND %s: the filter REJECTED a LEGAL move $%02X->$%02X "+
							"(flags $%02X, checker $%02X) - equivalently, a legal move was not accepted\n  fen %s",
							pos.name, s.from, s.to, s.flags, s.csq, pos.fen)
					}
				}
				if !legal && !rejected {
					cov.permissive++
				}
			}
			switch {
			case rejected:
				cov.rejected++
			default:
				cov.accepted++
				switch {
				case king:
					cov.kingMove++
				case ep:
					cov.epUnderChk++
				case s.to == s.csq:
					cov.capture++
				case s.csq != int(nosq):
					cov.interpose++
				}
			}
			if s.flags&0x07 != 0 {
				cov.promoUnder++
			}
			if s.csq != int(nosq) && s.board[s.csq]&0x07 == 2 { // KNIGHT
				cov.knightChk++
			}
		}

		exited, code, err := m.RunProfile(200_000_000_000, func(pc uint16, cycles uint8) {
			switch pc {
			case ckhit:
				cov.directRec++
			case ckhitx:
				cov.discoverRec++
			case sdomove:
				ply := int(m.Mem.Main[plyA]) // PARENT ply: make has not run
				if m.Mem.Main[inchkA+uint16(ply)] == 0 {
					s.live = false
					return
				}
				side := m.Mem.Main[sideA]
				s = snap{
					live:  true,
					ply:   ply,
					csq:   int(m.Mem.Main[ckSqA+uint16(ply)]),
					cdir:  int(m.Mem.Main[ckDirA+uint16(ply)]),
					ksq:   int(m.Mem.Main[psqA+uint16(side)*2]),
					from:  int(m.Mem.Main[fromA]),
					to:    int(m.Mem.Main[toA]),
					flags: m.Mem.Main[flagsA],
					side:  side,
				}
				copy(s.board[:], m.Mem.Main[boardA:boardA+128])
				s.mover = s.board[s.from]
				s.checkers = shCheckers(s.board[:], s.ksq, side^0x08)
				s.modelRejct = evModelRejects(s.csq, s.ksq, s.from, s.to,
					s.mover, s.flags, nosq)
			case sdrej:
				judge(true)
			case mk:
				judge(false)
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("%s: exited=%v code=%d err=%v", pos.name, exited, code, err)
		}
	}

	t.Logf("evasion-filter differential over %d positions:\n    %s", len(evCorpus), cov)

	// Coverage: every shape that could break the filter must be present, or
	// this test proved nothing about it. Reported as one failure per gap so a
	// corpus regression names the missing shape.
	for _, req := range []struct {
		name string
		got  int
	}{
		{"in-check moves", cov.moves},
		{"single-check nodes", cov.single},
		{"double-check nodes", cov.double},
		{"filter rejections", cov.rejected},
		{"filter acceptances", cov.accepted},
		{"interposition accepts", cov.interpose},
		{"checker-capture accepts", cov.capture},
		{"king moves under check", cov.kingMove},
		{"en-passant captures under check", cov.epUnderChk},
		{"CHECKERSQ=NOSQ fallbacks", cov.noSq},
		{"knight checkers", cov.knightChk},
		{"DIRECT CHECKERSQ writes (ckhit)", cov.directRec},
		{"DISCOVERED CHECKERSQ writes (ckhitx)", cov.discoverRec},
	} {
		if req.got == 0 {
			t.Errorf("COVERAGE GAP: the corpus produced no %s, so this test says nothing about them", req.name)
		}
	}
	if fails >= 10 {
		t.Errorf("(%d disagreements total; only the first 9 are shown)", fails)
	}
}

// buildLegalVerify assembles the LEGALVERIFY engine variant: the pre-make
// evasion filter's live equivalence assertion is assembly-time optional (it
// would cost the shipped image the whole old make + attacked() path it
// exists to remove), so it lives only in this build. Same pattern as
// CKVERIFY (ckverify_test.go) and GDVERIFY (gendefer_valid_test.go).
func buildLegalVerify(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("..", "..")
	if err := asmbuild.BuildVariant(root, "engine_legalverify", "LEGALVERIFY"); err != nil {
		if err == asmbuild.ErrCA65NotInstalled {
			t.Skip("ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine_legalverify.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestEvasionFilterVerify runs the corpus under the LEGALVERIFY build, where
// every move at an in-check node takes BOTH the filter and the old make +
// attacked() path and any move the filter rejected but attacked() calls legal
// exits 103.
//
// THE HONEST LIMIT, which CKVERIFY and GDVERIFY taught: a live assertion only
// ever sees what the workload reaches, so "never fired" and "cannot fire" are
// indistinguishable from the outside. Two things are done about that here.
// First, the counters are read back and the test requires the assertion to
// have been REACHED (rejections and acceptances both nonzero). Second, the
// trap itself is proven to work: poking LVFORCE inverts the comparison, so a
// healthy engine MUST then exit 103, and if it does not, the trap is broken
// and every green run above meant nothing.
func TestEvasionFilterVerify(t *testing.T) {
	bin := buildLegalVerify(t)
	var totRej, totPerm, totLeg, totFall int
	// The position used to prove the trap fires has to be one where the
	// comparison actually reaches the trapping side: the FIRST corpus entry
	// is a bare K+R endgame whose every legal evasion is a king move, so the
	// filter declines them all and a forced disagreement never arises. Pick
	// the position by MEASUREMENT (accepted-and-legal count > 0) instead of
	// by assumption.
	forceIdx, forceLeg := -1, 0
	for i, pos := range evCorpus {
		p, err := ParseFEN(pos.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, p, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, byte(defs["FT_CKEXT"])|0x1F)
		SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
		SetBudget(m, defs, 0, 5)
		m.Mem.Main[defs["HALFMOVE"]] = p.Halfmove
		m.Mem.Main[defs["LVFORCE"]] = 0 // real run: compare honestly
		exited, code, err := m.Run(200_000_000_000)
		if err != nil || !exited {
			t.Fatalf("%s: exited=%v err=%v", pos.name, exited, err)
		}
		if code == 103 {
			t.Errorf("%s: UNSOUND - the filter rejected a move make+attacked() calls LEGAL (exit 103)\n  fen %s",
				pos.name, pos.fen)
			continue
		}
		if code != 0 && code != 2 {
			t.Fatalf("%s: exit code %d", pos.name, code)
		}
		rd16 := func(a uint16) int {
			return int(m.Mem.Main[a]) | int(m.Mem.Main[a+1])<<8
		}
		totRej += rd16(defs["LVNREJ"])
		totPerm += rd16(defs["LVNPERM"])
		leg := rd16(defs["LVNLEG"])
		totLeg += leg
		totFall += rd16(defs["LVNFALL"])
		if forceIdx < 0 && leg > 0 {
			forceIdx, forceLeg = i, leg
		}
	}
	t.Logf("LEGALVERIFY over %d positions: rejected-and-illegal %d, accepted-and-legal %d, "+
		"accepted-but-illegal %d (permissive), declined %d",
		len(evCorpus), totRej, totLeg, totPerm, totFall)
	if totRej == 0 {
		t.Error("the assertion was never REACHED: no move was rejected, so a green run proves nothing")
	}
	if totLeg == 0 {
		t.Error("no accepted-and-legal move was seen: the comparison never ran on the side that traps")
	}

	// Prove the trap MECHANISM fires. LVFORCE = 3 xors the filter verdict
	// before the compare, turning every accepted-and-legal move into a
	// manufactured "rejected a legal move" - so a working trap must kill the
	// run at the first one.
	if forceIdx < 0 {
		t.Fatal("no corpus position produced an accepted-and-legal filtered move, " +
			"so the trap mechanism cannot be exercised at all")
	}
	fpos := evCorpus[forceIdx]
	p, err := ParseFEN(fpos.fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(bin, defs, p, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetFeatures(m, defs, byte(defs["FT_CKEXT"])|0x1F)
	SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
	SetBudget(m, defs, 0, 5)
	m.Mem.Main[defs["HALFMOVE"]] = p.Halfmove
	m.Mem.Main[defs["LVFORCE"]] = 3
	exited, code, err := m.Run(200_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if code != 103 {
		t.Errorf("on %q, where the honest run compared %d accepted-and-legal moves, LVFORCE=3 "+
			"manufactured a disagreement at every one of them and the trap did NOT fire "+
			"(exited=%v code=%d): the assertion above is decoration, not a gate",
			fpos.name, forceLeg, exited, code)
	} else {
		t.Logf("trap mechanism verified on %q (%d comparisons): LVFORCE=3 exits %d as designed",
			fpos.name, forceLeg, code)
	}
}
