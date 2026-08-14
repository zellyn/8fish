package chesstest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/8fish/internal/asmbuild"
)

// ---------------------------------------------------------------------------
// Gates for the SINGLE-RAY PIN TEST (asm/board.s pinray, called from
// search.s's lazy-legality gate).
//
// WHY THESE ARE HARDER THAN THE EVASION FILTER'S. The pre-make evasion filter
// only ever REJECTS on its own authority: an accepted move still went through
// make + attacked(), so being too permissive cost cycles and nothing else.
// This one decides in the ACCEPTING direction — a move the ray test calls
// legal is searched with NO further verification — so an over-permissive ray
// test is a search that plays illegal moves. Both directions therefore have
// to be sound, and both are checked here in both directions.
//
// (The rejecting direction is deliberately NOT load-bearing in the shipped
// image: `bcs slfull` sends a "the ray is exposed" verdict to the real
// attacked() scan for confirmation, because there are only 91 of them in the
// whole profile workload against 13,069 accepts. So a too-STRICT ray test
// costs 325 cycles; a too-PERMISSIVE one is a bug. The differential asserts
// both anyway: a strict verdict that is wrong means the rule has drifted.)
//
// Three instruments, the same shape as the evasion filter's:
//
//  1. TestPinRayDifferential — EXHAUSTIVE over a corpus. For every move that
//     reaches the gate it recovers the asm's own verdict from which of
//     pinray's exits control took, and compares it against (a) an INDEPENDENT
//     legality oracle (shCheckers on a Go-constructed post-move board) and
//     (b) a model of the documented rule written from the geometry, not from
//     the asm's tables. Coverage counters prove the corpus reaches every
//     shape that could break it, including all eight ray directions.
//  2. TestPinRayVerify — a LIVE assertion in a `ca65 -D PINVERIFY` build
//     (the CKVERIFY/GDVERIFY/LEGALVERIFY pattern): every move the ray test
//     accepts ALSO runs make + attacked(), and a disagreement exits 104. It
//     proves its own trap fires by poking PVFORCE.
//  3. TestPinRayTables — the two table facts the walk rests on, checked
//     against the geometry: DELTATAB negation really is the reverse step, and
//     no non-slider shares a bit with a ray orientation.
// ---------------------------------------------------------------------------

// pinCorpus is built to reach the shapes a ray walk can get wrong, not to be
// representative — the coverage counters at the end FAIL the test if any of
// them is missing, so this list is checked rather than trusted.
//
// The first block is one position per 0x88 ray direction from the king
// (+1/-1/+16/-16/+17/-17/+15/-15), each with a piece that CANNOT stay on the
// ray (a knight), so every one of its moves must be judged illegal. The
// second block pins pieces that CAN stay on their ray, which is the case a
// naive "aligned means illegal" test gets wrong in the expensive direction.
var pinCorpus = []struct {
	name string
	fen  string
}{
	// --- one pinned knight per ray direction from the white king ---
	{"pin-E-rook", "4k3/8/8/8/8/8/8/K1N4r w - - 0 1"},
	{"pin-W-rook", "4k3/8/8/8/8/8/8/r4N1K w - - 0 1"},
	{"pin-N-rook", "r3k3/8/8/8/8/N7/8/K7 w - - 0 1"},
	{"pin-S-rook", "K7/8/N7/8/8/8/8/r3k3 w - - 0 1"},
	{"pin-NE-bishop", "4k2b/8/8/8/8/2N5/8/K7 w - - 0 1"},
	{"pin-NW-bishop", "b3k3/8/8/8/8/5N2/8/7K w - - 0 1"},
	{"pin-SE-bishop", "K7/8/2N5/8/8/8/8/4k2B w - - 0 1"},
	{"pin-SW-bishop", "7K/8/5N2/8/8/8/8/b3k3 w - - 0 1"},
	// --- pinned pieces that CAN move along their own pin ray ---
	{"rook-along-file-pin", "r3k3/8/8/8/8/R7/8/K7 w - - 0 1"},
	{"bishop-along-diag-pin", "4k2b/8/8/8/8/2B5/8/K7 w - - 0 1"},
	{"queen-along-diag-pin", "4k2q/8/8/8/8/2Q5/8/K7 w - - 0 1"},
	{"queen-along-rank-pin", "4k3/8/8/8/8/8/8/K1Q4r w - - 0 1"},
	{"pawn-push-along-file-pin", "4r3/6k1/8/8/8/8/4P3/4K3 w - - 0 1"},
	{"pawn-takes-the-pinner", "6k1/8/8/8/8/2b5/3P4/4K3 w - - 0 1"},
	{"promo-along-file-pin", "4r2k/4P3/8/8/8/8/8/4K3 w - - 0 1"},
	// --- BLOCKED rays: aligned movers whose ray is stopped short ---
	{"blocked-behind-mover", "r3k3/8/P7/8/8/N7/8/K7 w - - 0 1"},
	{"blocked-before-mover", "r3k3/8/8/8/8/N7/P7/K7 w - - 0 1"},
	{"blocked-by-enemy-nonslider", "4k3/8/8/8/8/8/8/K1N3nr w - - 0 1"},
	{"aligned-but-nothing-behind", "6k1/8/8/8/8/8/4N3/4K3 w - - 0 1"},
	// --- both colors pinned, so the walk runs from a black king too ---
	{"both-sides-pinned", "3rk3/8/8/8/8/8/3N4/3K4 w - - 0 1"},
	{"both-sides-pinned-diag", "6k1/5n2/8/8/8/2B5/8/K6b w - - 0 1"},
	// --- en passant at a node that is NOT in check (the gate declines it) ---
	{"ep-quiet-node", "rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3"},
	{"ep-quiet-node-2", "rnbqkbnr/ppppp1pp/8/8/4Pp2/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 3"},
	// --- ordinary traffic, so the bulk of the sample is what a game sees ---
	{"midgame-1", "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8"},
	{"midgame-2", "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10"},
	{"midgame-3", "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8"},
	{"castling-rich", "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1"},
	{"endgame-pawns", "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1"},
	{"endgame-RvR", "r5k1/5ppp/8/8/8/8/5PPP/R5K1 w - - 0 1"},
	{"promotion-race", "8/P6k/8/8/8/8/1p6/K7 w - - 0 1"},
	{"mate-in-few", "1k1r4/pp1b1R2/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - - 0 1"},
	{"open-position", "2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11"},
}

// pinRayDirs are the eight 0x88 single steps a queen line can take. Named
// here so the coverage report can require every one of them.
var pinRayDirs = []struct {
	d    int
	name string
}{
	{1, "E"}, {-1, "W"}, {16, "N"}, {-16, "S"},
	{17, "NE"}, {-17, "SW"}, {15, "NW"}, {-15, "SE"},
}

// pinSlides reports whether piece type t attacks along a ray of the given
// orientation. Written from the rules, not from asm TYPEATKTAB.
func pinSlides(t byte, diag bool) bool {
	switch t {
	case 5: // queen
		return true
	case 4: // rook
		return !diag
	case 3: // bishop
		return diag
	}
	return false
}

// pinModel is the documented rule pinray implements, evaluated on a POST-move
// board: walk from the mover's king along the ray through the vacated square
// and report what the walk finds. aligned=false means the gate never calls
// pinray at all (the "provably legal" fast path).
//
// enemy is the attacking color (asm SIDE after make has flipped it).
func pinModel(board []byte, ksq, from int, enemy byte) (aligned, exposed bool, dir int, stop string) {
	dir = evRayDir(ksq, from)
	if dir == 0 {
		return false, false, 0, "unaligned"
	}
	diag := dir != 1 && dir != -1 && dir != 16 && dir != -16
	for s := ksq + dir; ; s += dir {
		if s < 0 || s > 0x77 || s&0x88 != 0 {
			return true, false, dir, "offboard"
		}
		p := board[s]
		if p == 0 {
			continue
		}
		if p&0x08 != enemy {
			return true, false, dir, "own"
		}
		if pinSlides(p&0x07, diag) {
			return true, true, dir, "slider"
		}
		return true, false, dir, "enemy-nonslider"
	}
}

// pinPostBoard applies a non-king, non-castle, non-ep move to a copy of the
// pre-make 0x88 board — the same construction evLegalAfter uses, so the
// oracle and the model see the same independently-built position.
func pinPostBoard(board []byte, from, to int, mover, flags byte) [128]byte {
	var b [128]byte
	copy(b[:], board)
	final := mover
	if p := flags & 0x07; p != 0 { // FL_PROMO: bits 0-2 are the new type
		final = mover&0xF8 | p
	}
	b[from] = 0
	b[to] = final
	return b
}

type pinCoverage struct {
	seen         int // moves that reached the gate at all (post-make)
	eligible     int // ... non-king, non-ep, parent not in check: the gate's own class
	aligned      int // ... king-aligned FROM: pinray actually ran
	notAligned   int // ... not aligned: accepted with no walk
	exposed      int // ... pinray said the king is exposed (discovered check)
	stopOwn      int // ... walk stopped on the king's own piece
	stopEnemyNon int // ... walk stopped on a non-sliding enemy piece
	stopOffBoard int // ... walk ran off the board
	pinnedAlong  int // ... a real pin, and the mover STAYED on the ray: LEGAL
	pinnedOff    int // ... a real pin, and the mover left the ray: ILLEGAL
	promo        int // ... the move is a promotion
	kingSq       int // white-king and black-king walks both exercised
	blackKing    int
	declKing     int // declined by the gate: king move
	declEP       int // ... en passant capture
	declInChk    int // ... parent in check (sdevade's class, not this one)
	dirs         map[int]int
}

func (c pinCoverage) String() string {
	s := fmt.Sprintf(
		"gate moves %d (eligible %d: aligned %d, not aligned %d)\n"+
			"    ray verdicts: exposed %d, legal by blocker: own %d, enemy-nonslider %d, ran off board %d\n"+
			"    real pins: mover STAYED on the ray (legal) %d, mover LEFT it (illegal) %d\n"+
			"    shapes: promotions %d, white-king walks %d, black-king walks %d\n"+
			"    declined by the gate: king moves %d, en passant %d, parent in check %d\n"+
			"    ray directions:",
		c.seen, c.eligible, c.aligned, c.notAligned, c.exposed,
		c.stopOwn, c.stopEnemyNon, c.stopOffBoard, c.pinnedAlong, c.pinnedOff,
		c.promo, c.kingSq, c.blackKing, c.declKing, c.declEP, c.declInChk)
	for _, d := range pinRayDirs {
		s += fmt.Sprintf(" %s=%d", d.name, c.dirs[d.d])
	}
	return s
}

// TestPinRayDifferential is the EXHAUSTIVE pin differential.
//
// For every move that reaches the lazy-legality gate, over a corpus built to
// contain every shape the walk can get wrong, it asserts:
//
//	(1) the asm's alignment verdict (did pinray run at all?) equals the
//	    model's — a gate that never calls pinray would otherwise pass (3)
//	    trivially and be wrong for a different reason;
//	(2) the asm's ray verdict equals the model of the documented rule,
//	    EXACTLY, in both directions;
//	(3) BOTH directions against an INDEPENDENT oracle: every move the ray
//	    test calls LEGAL really is legal (the direction that skips work, so
//	    the one that can corrupt a search), and every move it calls EXPOSED
//	    really is illegal (which the shipped image re-verifies, so this
//	    direction only proves the rule has not drifted).
func TestPinRayDifferential(t *testing.T) {
	bin := loadEngine(t)
	labels := labelsFor(t)
	need := map[string]uint16{}
	for _, n := range []string{"sdomove", "pinray", "prno", "pryes", "spinleg",
		"slfull", "slegal", "sloopj"} {
		a, ok := labels[n]
		if !ok || a == 0 {
			t.Fatalf("label %q missing from asm/engine.lbl", n)
		}
		need[n] = a
	}
	sdomove, pinrayA, prnoA, pryesA := need["sdomove"], need["pinray"], need["prno"], need["pryes"]
	spinlegA, slfullA, slegalA, sloopjA := need["spinleg"], need["slfull"], need["slegal"], need["sloopj"]

	boardA, psqA := defs["BOARD"], defs["PIECESQ"]
	inchkA, plyA, sideA := defs["INCHK"], defs["PLY"], defs["SIDE"]
	fromA, toA, flagsA := defs["FROM"], defs["TO"], defs["MVFLAGS"]

	cov := pinCoverage{dirs: map[int]int{}}
	var fails int
	report := func(format string, args ...any) {
		fails++
		if fails < 12 {
			t.Errorf(format, args...)
		}
	}

	for _, pos := range pinCorpus {
		p, err := ParseFEN(pos.fen)
		if err != nil {
			t.Fatalf("%s: %v", pos.name, err)
		}
		m, err := NewMachine(bin, defs, p, 0, nil)
		if err != nil {
			t.Fatalf("%s: %v", pos.name, err)
		}
		// The SHIPPED configuration, so the differential judges the code that
		// actually runs.
		SetFeatures(m, defs, byte(defs["FT_CKEXT"])|0x1F)
		SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
		SetBudget(m, defs, 0, 5) // fixed depth: deterministic and quick
		m.Mem.Main[defs["HALFMOVE"]] = p.Halfmove

		// Snapshot taken at sdomove (PRE-make, the parent position); the
		// verdict is whichever of pinray's exits control reaches.
		type snap struct {
			live     bool
			board    [128]byte
			ply      int
			ksq      int
			from, to int
			mover    byte
			flags    byte
			side     byte // the MOVER's side (SIDE has not flipped yet)
			inChk    bool
			ranPin   bool // control reached pinray
			verdict  int  // 0 = none yet, 1 = prno (legal), 2 = pryes (exposed)
		}
		var s snap

		judge := func() {
			if !s.live {
				return
			}
			s.live = false
			cov.seen++
			king := s.mover&0x07 == 6
			ep := s.flags&0x08 != 0
			// The gate's own class: parent not in check, non-king, non-ep.
			// Everything else went to slfull for a pre-existing reason and
			// this change did not touch it.
			switch {
			case s.inChk:
				cov.declInChk++
				return
			case king:
				cov.declKing++
				return
			case ep:
				cov.declEP++
				return
			}
			cov.eligible++
			if s.flags&0x07 != 0 {
				cov.promo++
			}
			if s.side == 0 {
				cov.kingSq++
			} else {
				cov.blackKing++
			}
			post := pinPostBoard(s.board[:], s.from, s.to, s.mover, s.flags)
			enemy := s.side ^ 0x08
			aligned, exposed, dir, stop := pinModel(post[:], s.ksq, s.from, enemy)

			// (1) alignment: did the asm run the walk exactly when the model
			// says the FROM square is on a ray through the king?
			if aligned != s.ranPin {
				report("%s: asm ran pinray=%v but the model says aligned=%v "+
					"(king $%02X, from $%02X, to $%02X)\n  fen %s",
					pos.name, s.ranPin, aligned, s.ksq, s.from, s.to, pos.fen)
			}
			if !aligned {
				cov.notAligned++
			} else {
				cov.aligned++
				cov.dirs[dir]++
				switch stop {
				case "own":
					cov.stopOwn++
				case "enemy-nonslider":
					cov.stopEnemyNon++
				case "offboard":
					cov.stopOffBoard++
				}
				// (2) the asm's ray verdict must be the model's, exactly.
				asmExposed := s.verdict == 2
				if s.verdict == 0 {
					report("%s: pinray ran but neither exit was reached "+
						"(from $%02X to $%02X)\n  fen %s", pos.name, s.from, s.to, pos.fen)
				} else if asmExposed != exposed {
					report("%s: asm ray verdict exposed=%v, the RULE says %v "+
						"(king $%02X from $%02X to $%02X dir %+d stop %s)\n  fen %s",
						pos.name, asmExposed, exposed, s.ksq, s.from, s.to, dir, stop, pos.fen)
				}
				if exposed {
					cov.exposed++
				}
				// Was this a REAL pin (an enemy slider behind FROM with a
				// clear ray, ignoring where the mover went)? Then legal means
				// the mover stayed on the ray or took the pinner, and illegal
				// means it left the ray. Those are the two cases a
				// post-make walk has to get right without any extra test.
				// (a capture of the pinner counts as "stayed on the ray":
				// TO still holds the pinner in this vacated view, so the
				// model calls it pinned, and the post-move answer is legal.)
				vac := s.board
				vac[s.from] = 0
				if _, pinned, _, _ := pinModel(vac[:], s.ksq, s.from, enemy); pinned {
					if exposed {
						cov.pinnedOff++
					} else {
						cov.pinnedAlong++
					}
				}
			}

			// (3) both directions against the independent oracle.
			legal := len(shCheckers(post[:], s.ksq, enemy)) == 0
			if !exposed && !legal {
				report("UNSOUND %s: the ray test ACCEPTED an ILLEGAL move "+
					"$%02X->$%02X (flags $%02X, king $%02X): the search would play it\n  fen %s",
					pos.name, s.from, s.to, s.flags, s.ksq, pos.fen)
			}
			if exposed && legal {
				report("%s: the ray test called $%02X->$%02X exposed but the oracle "+
					"says it is LEGAL (king $%02X)\n  fen %s",
					pos.name, s.from, s.to, s.ksq, pos.fen)
			}
		}

		exited, code, err := m.RunProfile(200_000_000_000, func(pc uint16, cycles uint8) {
			switch pc {
			case sdomove:
				ply := int(m.Mem.Main[plyA]) // PARENT ply: make has not run
				side := m.Mem.Main[sideA]
				s = snap{
					live:  true,
					ply:   ply,
					ksq:   int(m.Mem.Main[psqA+uint16(side)*2]),
					from:  int(m.Mem.Main[fromA]),
					to:    int(m.Mem.Main[toA]),
					flags: m.Mem.Main[flagsA],
					side:  side,
					inChk: m.Mem.Main[inchkA+uint16(ply)] != 0,
				}
				copy(s.board[:], m.Mem.Main[boardA:boardA+128])
				s.mover = s.board[s.from]
			case pinrayA:
				s.ranPin = true
			case prnoA:
				if s.verdict == 0 {
					s.verdict = 1
				}
			case pryesA:
				s.verdict = 2
			case spinlegA, slfullA:
				judge()
			case slegalA, sloopjA:
				s.live = false
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("%s: exited=%v code=%d err=%v", pos.name, exited, code, err)
		}
	}

	t.Logf("pin-ray differential over %d positions:\n    %s", len(pinCorpus), cov)

	for _, req := range []struct {
		name string
		got  int
	}{
		{"gate-eligible moves", cov.eligible},
		{"king-aligned movers (pinray ran)", cov.aligned},
		{"non-aligned movers (fast accept)", cov.notAligned},
		{"exposed verdicts (discovered check)", cov.exposed},
		{"rays blocked by the king's own piece", cov.stopOwn},
		{"rays blocked by a non-sliding enemy piece", cov.stopEnemyNon},
		{"rays that ran off the board (unblocked)", cov.stopOffBoard},
		{"pinned piece moving ALONG its pin ray (legal)", cov.pinnedAlong},
		{"pinned piece moving OFF its pin ray (illegal)", cov.pinnedOff},
		{"promotions through the gate", cov.promo},
		{"walks from a WHITE king", cov.kingSq},
		{"walks from a BLACK king", cov.blackKing},
		{"king moves declined by the gate", cov.declKing},
		{"en-passant captures declined by the gate", cov.declEP},
		{"in-check parents declined by the gate", cov.declInChk},
	} {
		if req.got == 0 {
			t.Errorf("COVERAGE GAP: the corpus produced no %s, so this test says nothing about them", req.name)
		}
	}
	// Every ray direction, separately: a walk that is broken in one direction
	// only (a sign error in the DELTATAB negation, say) is invisible to a
	// corpus that never walks that way.
	for _, d := range pinRayDirs {
		if cov.dirs[d.d] == 0 {
			t.Errorf("COVERAGE GAP: no king-aligned mover on the %s ray (%+d), "+
				"so this test says nothing about that direction", d.name, d.d)
		}
	}
	if fails >= 12 {
		t.Errorf("(%d disagreements total; only the first 11 are shown)", fails)
	}
}

// TestPinRayTables checks the two table facts pinray's shortcuts rest on,
// against the geometry rather than against the tables themselves.
//
//   - pinray gets its step by NEGATING DELTATAB[king - from + $77] instead of
//     recomputing DELTATAB[from - king + $77]. That is only valid if DELTATAB
//     is exactly antisymmetric about $77, so check it over every difference.
//   - the walk decides "is this a pinner" with TYPEATKTAB[type] & ATBITS,
//     where ATBITS holds only ATK_DIAG|ATK_ORTHO. That is only a slider test
//     if no non-slider type shares a bit with those two.
func TestPinRayTables(t *testing.T) {
	bin := loadEngine(t)
	labels := labelsFor(t)
	const base = 0x4000 // engine.cfg loads MAIN at $4000, file = %O
	for _, sym := range []string{"DELTATAB", "TYPEATKTAB"} {
		if labels[sym] == 0 {
			t.Fatalf("label %q missing from asm/engine.lbl", sym)
		}
	}
	rd := func(sym string, i int) byte {
		return bin[int(labels[sym])-base+i]
	}
	// Antisymmetry: DELTATAB[$77+d] == -DELTATAB[$77-d] for every d that
	// indexes the table, and both are the true single step for that
	// difference.
	checked := 0
	for from := 0; from <= 0x77; from++ {
		if from&0x88 != 0 {
			continue
		}
		for to := 0; to <= 0x77; to++ {
			if to&0x88 != 0 || to == from {
				continue
			}
			fwd := int8(rd("DELTATAB", (to-from+0x77)&0xFF))
			rev := int8(rd("DELTATAB", (from-to+0x77)&0xFF))
			if int(fwd) != -int(rev) {
				t.Fatalf("DELTATAB is not antisymmetric at from $%02X to $%02X: "+
					"forward %+d, reverse %+d (pinray negates one to get the other)",
					from, to, fwd, rev)
			}
			if want := evRayDir(from, to); int(fwd) != want && !(want == 0 && fwd == 0) {
				t.Fatalf("DELTATAB[$%02X - $%02X + $77] = %+d, geometry says %+d",
					to, from, fwd, want)
			}
			checked++
		}
	}
	// Slider test: only bishop/rook/queen may share a bit with a ray
	// orientation. A knight, king or pawn behind the vacated square must read
	// as "no pin" with no extra compare.
	rays := byte(defs["ATK_DIAG"] | defs["ATK_ORTHO"])
	if rays == 0 {
		t.Fatal("ATK_DIAG/ATK_ORTHO missing from defs")
	}
	for typ := 0; typ <= 6; typ++ {
		bits := rd("TYPEATKTAB", typ) & rays
		slider := typ == 3 || typ == 4 || typ == 5
		if (bits != 0) != slider {
			t.Errorf("TYPEATKTAB[%d] & (ATK_DIAG|ATK_ORTHO) = $%02X, but slider=%v: "+
				"pinray would misjudge a piece of this type sitting behind the vacated square",
				typ, bits, slider)
		}
	}
	t.Logf("DELTATAB antisymmetry verified over %d on-board square pairs; "+
		"TYPEATKTAB slider bits exclusive to B/R/Q", checked)
}

// buildPinVerify assembles the PINVERIFY engine variant: the ray test's live
// equivalence assertion is assembly-time optional (it would cost the shipped
// image the whole attacked() scan it exists to remove), so it lives only in
// this build. Same pattern as CKVERIFY, GDVERIFY and LEGALVERIFY.
func buildPinVerify(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("..", "..")
	if err := asmbuild.BuildVariant(root, "engine_pinverify", "PINVERIFY"); err != nil {
		if err == asmbuild.ErrCA65NotInstalled {
			t.Skip("ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine_pinverify.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestPinRayVerify runs the corpus under the PINVERIFY build, where every move
// the single-ray pin test calls LEGAL takes the old make + attacked() path as
// well and exits 104 if that disagrees.
//
// THE HONEST LIMIT, which CKVERIFY and GDVERIFY taught: a live assertion only
// ever sees what the workload reaches, so "never fired" and "cannot fire" look
// identical from outside. Two things are done about it. The counters are read
// back and the assertion is required to have been REACHED. And the trap itself
// is proven: PVFORCE=1 inverts the compare, turning every move the gate
// DECLINED and attacked() found illegal into a manufactured disagreement, so a
// healthy engine must then exit 104.
func TestPinRayVerify(t *testing.T) {
	bin := buildPinVerify(t)
	var totLeg, totIll int
	forceIdx, forceIll := -1, 0
	for i, pos := range pinCorpus {
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
		m.Mem.Main[defs["PVFORCE"]] = 0 // real run: compare honestly
		exited, code, err := m.Run(200_000_000_000)
		if err != nil || !exited {
			t.Fatalf("%s: exited=%v err=%v", pos.name, exited, err)
		}
		if code == 104 {
			t.Errorf("%s: UNSOUND - the ray test accepted a move make+attacked() calls "+
				"ILLEGAL (exit 104)\n  fen %s", pos.name, pos.fen)
			continue
		}
		if code != 0 && code != 2 {
			t.Fatalf("%s: exit code %d", pos.name, code)
		}
		rd16 := func(a uint16) int {
			return int(m.Mem.Main[a]) | int(m.Mem.Main[a+1])<<8
		}
		totLeg += rd16(defs["PVNLEG"])
		ill := rd16(defs["PVNILL"])
		totIll += ill
		if forceIdx < 0 && ill > 0 {
			forceIdx, forceIll = i, ill
		}
	}
	t.Logf("PINVERIFY over %d positions: accepted-and-confirmed-legal %d, "+
		"declined-and-illegal %d (the population PVFORCE inverts)",
		len(pinCorpus), totLeg, totIll)
	if totLeg == 0 {
		t.Error("the assertion was never REACHED: no move was accepted by the ray test, " +
			"so a green run proves nothing")
	}

	if forceIdx < 0 {
		t.Fatal("no corpus position produced a declined-and-illegal move, so the trap " +
			"mechanism cannot be exercised at all")
	}
	fpos := pinCorpus[forceIdx]
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
	m.Mem.Main[defs["PVFORCE"]] = 1
	exited, code, err := m.Run(200_000_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if code != 104 {
		t.Errorf("on %q, where the honest run found %d declined-and-illegal moves, "+
			"PVFORCE=1 manufactured a disagreement at every one of them and the trap did "+
			"NOT fire (exited=%v code=%d): the assertion above is decoration, not a gate",
			fpos.name, forceIll, exited, code)
	} else {
		t.Logf("trap mechanism verified on %q (%d comparisons): PVFORCE=1 exits %d as designed",
			fpos.name, forceIll, code)
	}
}
