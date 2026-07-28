package chesstest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/harness"
	"github.com/zellyn/chess6502/internal/asmbuild"
)

// Correctness gates for FT2_GENDEFER (deferred full-width move generation
// at snode, docs/results.md 2026-07-27).
//
// The feature deletes the eager generate() at TT-available nodes and
// searches the TT move directly, so ttmovevalid replaces generate() as the
// thing that decides whether a TT move is playable AT ALL. That matters
// because asm/tt.s indexes on 12 bits and ttfetch verifies 24 more: a hit
// against a DIFFERENT position survives with p = 2^-20, roughly one
// wrong-position TT move per 25 games at the shipped control. A validator
// that is even slightly more permissive than generate() therefore makes an
// illegal move and corrupts the board — a defect that passes every unit
// test and every short parity run and only surfaces in a gauntlet days
// later. A 2^-20 failure mode cannot be gated by sampling, so:
//
//   - TestTTMoveValidExhaustive checks ALL 128x128 (from,to) pairs of the
//     0x88 square codes — off-board codes included — against generate()'s
//     actual output, for every corpus position, in both the "is it a move"
//     and the "what flags would it carry" directions.
//   - TestGenDeferVerify runs real searches in the `ca65 -D GDVERIFY`
//     variant, which re-generates at every staged node and traps (exit
//     102) if the validated move is not in the list.
//   - TestGenDeferTreeIdentity is the acceptance test: MicroAB
//     fingerprints must be bit-identical with the feature ON and OFF.

// buildGenDeferVerify assembles the GDVERIFY engine variant (the live
// validator/list cross-check plus ttmvsweep, the exhaustive-test entry
// point). Same pattern as buildCkVerify.
func buildGenDeferVerify(t *testing.T) ([]byte, map[string]uint16) {
	t.Helper()
	root := filepath.Join("..", "..")
	if err := asmbuild.BuildVariant(root, "engine_gdverify", "GDVERIFY"); err != nil {
		if err == asmbuild.ErrCA65NotInstalled {
			t.Skip("ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine_gdverify.bin"))
	if err != nil {
		t.Fatal(err)
	}
	labels, err := ParseLabelFile(filepath.Join("..", "..", "asm", "engine_gdverify.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	return bin, labels
}

// newStubMachine loads bin and pos into a fresh machine whose entry point
// is a hand-built stub at $0300 ("jsr <sub>; lda #0; sta EXIT"). Same
// shape as callGenerateq's setup in recapgen_test.go.
func newStubMachine(t *testing.T, bin []byte, pos *Position, sub uint16) *harness.Machine {
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
	// GDVBUF (ttmvsweep's 32x128 buffer at $3000) DELIBERATELY overlaps the
	// resident book, which spans $2000-$3CEE since the 2026-07-28 widening --
	// there is no other 4 KB hole in MAIN. A GDVERIFY build and a loaded book
	// are therefore mutually exclusive, and this is where that invariant is
	// enforced rather than merely asserted in a comment: a book loaded here
	// would be shredded by the sweep, and the sweep's results by the book.
	if bookBase := defs["BOOK_BASE"]; bookBase != 0 {
		if m.Mem.Main[bookBase] == 'B' && m.Mem.Main[bookBase+1] == 'K' {
			t.Fatalf("a resident book is loaded at $%04X, but this is a GDVERIFY "+
				"machine whose GDVBUF sweep buffer at $3000 overlaps it: the two "+
				"are mutually exclusive (see asm/defs.inc GDVBUF)", bookBase)
		}
	}
	board := defs["BOARD"]
	for rank := uint16(0); rank < 8; rank++ {
		base := rank * 16
		copy(m.Mem.Main[board+base:board+base+8], pos.Board[base:base+8])
	}
	copy(m.Mem.Main[defs["PIECESQ"]:defs["PIECESQ"]+32], pos.PieceSq[:])
	m.Mem.Main[defs["SIDE"]] = pos.Side
	m.Mem.Main[defs["EPSQ"]] = pos.EpSq
	m.Mem.Main[defs["CASTLE"]] = pos.Castle
	const moveStack = 0x0E00
	m.Mem.Main[defs["MSP"]] = byte(moveStack & 0xFF)
	m.Mem.Main[defs["MSP"]+1] = byte(moveStack >> 8)
	copy(m.Mem.Main[stubAddr:], []byte{
		0x20, byte(sub), byte(sub >> 8), // jsr sub
		0xA9, 0x00, // lda #0
		0x8D, 0xFF, 0xBF, // sta EXIT_TRAP
	})
	return m
}

// promoOrCastle masks the flag bits that make a move ineligible for the
// deferred path: FL_PROMO (bits 0-2) and FL_CASTLE. (A function, not a
// package var: defs is populated by TestMain, after var initialization.)
func promoOrCastle() byte { return byte(defs["FL_PROMO"] | defs["FL_CASTLE"]) }

// genMove is one move as generate() emitted it.
type genMove struct{ from, to, flags byte }

// callGenerate runs `generate` (the full-width copy) over pos and returns
// its emissions in order.
func callGenerate(t *testing.T, bin []byte, labels map[string]uint16, pos *Position) []genMove {
	t.Helper()
	gen, ok := labels["generate"]
	if !ok {
		t.Fatal("no generate label")
	}
	m := newStubMachine(t, bin, pos, gen)
	exited, code, err := m.Run(10_000_000)
	if err != nil || !exited || code != 0 {
		t.Fatalf("generate stub: exited=%v code=%d err=%v", exited, code, err)
	}
	end := uint16(m.Mem.Main[defs["MSP"]]) | uint16(m.Mem.Main[defs["MSP"]+1])<<8
	var moves []genMove
	for p := uint16(0x0E00); p < end; p += 4 {
		moves = append(moves, genMove{m.Mem.Main[p+1], m.Mem.Main[p+2], m.Mem.Main[p+3]})
	}
	return moves
}

// sweepTTMoveValid runs ttmvsweep over all 128x128 (from,to) pairs in four
// 32-from chunks and returns verdict[from][to] = (valid, flags).
func sweepTTMoveValid(t *testing.T, bin []byte, labels map[string]uint16, pos *Position) (valid [128][128]bool, flags [128][128]byte) {
	t.Helper()
	sweep, ok := labels["ttmvsweep"]
	if !ok {
		t.Fatal("no ttmvsweep label (GDVERIFY variant not built?)")
	}
	buf := uint16(defs["GDVBUF"])
	for base := 0; base < 128; base += 32 {
		m := newStubMachine(t, bin, pos, sweep)
		m.Mem.Main[defs["GDVBASE"]] = byte(base)
		exited, code, err := m.Run(100_000_000)
		if err != nil || !exited || code != 0 {
			t.Fatalf("ttmvsweep stub (base %d): exited=%v code=%d err=%v", base, exited, code, err)
		}
		for f := 0; f < 32; f++ {
			for to := 0; to < 128; to++ {
				b := m.Mem.Main[buf+uint16(f*128+to)]
				valid[base+f][to] = b&0x80 != 0
				flags[base+f][to] = b & 0x3F
			}
		}
	}
	return valid, flags
}

// gdCorpus: the recapture-generator corpus (perft suite + WAC tactics +
// crafted promotion/edge cases) plus positions chosen for the cases
// ttmovevalid reconstructs by hand rather than by geometry — en passant
// for both colours, double pushes, blocked and unblocked pawns, kings with
// castling rights (which must be REJECTED), and pieces on the a/h files
// where a naive 0x88 difference test would wrap.
var gdCorpus = append(append([]string{}, recapCorpus...),
	// white ep available on d6, black pawns able to double-push
	"rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq d6 0 3",
	// black ep available on e3
	"rnbqkbnr/pppp1ppp/8/8/4Pp2/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 3",
	// both sides on their home ranks: every double push live
	"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR b KQkq - 0 1",
	// double pushes blocked at the target (a4) and at the skipped
	// square (b3), and the same for black
	"4k3/8/8/8/p7/1p6/PP6/4K3 w - - 0 1",
	"4k3/6pp/6P1/7P/8/8/8/4K3 b - - 0 1",
	// all four castles available and legal
	"r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1",
	// castling rights present but the path is attacked / occupied
	"r3k2r/8/8/8/8/5q2/8/R3K2R w KQkq - 0 1",
	// a-file and h-file pawns and pieces (0x88 wrap bait)
	"7k/P6p/8/8/8/8/p6P/K7 w - - 0 1",
	// black to move, same
	"7k/P6p/8/8/8/8/p6P/K7 b - - 0 1",
	// pawns one step from promoting, both colours
	"8/PPPPPPPP/8/4k3/4K3/8/pppppppp/8 w - - 0 1",
	"8/PPPPPPPP/8/4k3/4K3/8/pppppppp/8 b - - 0 1",
	// kingless slot-0 tombstone case (a legal input to generate)
	"8/8/3n4/8/4R3/8/2b5/8 w - - 0 1",
)

// TestTTMoveValidExhaustive is the load-bearing gate: for every corpus
// position and every one of the 128x128 (from,to) square-code pairs,
// ttmovevalid's verdict must agree EXACTLY with membership in generate()'s
// output, and the flags it reconstructs must equal the flags generate()
// emitted.
//
// "Agree exactly" is qualified by the two documented, tree-motivated
// rejections: a promotion (generate emits four entries, N/B/R/Q, and pass 0
// searches all four in that order) and a castle must be REJECTED. Those are
// the only permitted disagreements, and they are checked to be exactly the
// disagreements that occur — a validator that accepted one would change the
// tree, and one that rejected anything else would silently lose the saving.
func TestTTMoveValidExhaustive(t *testing.T) {
	bin, labels := buildGenDeferVerify(t)
	// Coverage counters: an exhaustive test that exercised none of the
	// hand-reconstructed cases would pass vacuously, so the corpus is
	// required to contain each of them.
	cover := map[string]int{}
	for _, fen := range gdCorpus {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatalf("%s: %v", fen, err)
		}
		moves := callGenerate(t, bin, labels, pos)

		// What generate() would have the deferred path search: the
		// single non-promotion, non-castle move for this (from,to).
		type want struct {
			ok    bool
			flags byte
		}
		var expect [128][128]want
		var special [128][128]bool // a promo or castle exists for this pair
		for _, mv := range moves {
			if mv.from >= 128 || mv.to >= 128 {
				t.Fatalf("%s: generate emitted an off-board move %02x->%02x", fen, mv.from, mv.to)
			}
			if mv.flags&promoOrCastle() != 0 {
				special[mv.from][mv.to] = true
				continue
			}
			if expect[mv.from][mv.to].ok {
				t.Fatalf("%s: generate emitted %s twice with plain flags",
					fen, MoveUCI(mv.from, mv.to, mv.flags))
			}
			expect[mv.from][mv.to] = want{true, mv.flags}
		}

		valid, flags := sweepTTMoveValid(t, bin, labels, pos)
		var bad, missed int
		for f := 0; f < 128; f++ {
			for to := 0; to < 128; to++ {
				w := expect[f][to]
				got, gotFlags := valid[f][to], flags[f][to]
				switch {
				case got && !w.ok:
					// The fatal direction: an illegal make().
					bad++
					if bad <= 5 {
						t.Errorf("%s: ttmovevalid ACCEPTS %s (flags %#02x) but generate does not emit it (promo/castle present: %v)",
							fen, MoveUCI(byte(f), byte(to), gotFlags), gotFlags, special[f][to])
					}
				case !got && w.ok:
					missed++
					if missed <= 5 {
						t.Errorf("%s: ttmovevalid REJECTS %s, which generate emits with flags %#02x",
							fen, MoveUCI(byte(f), byte(to), w.flags), w.flags)
					}
				case got && gotFlags != w.flags:
					t.Errorf("%s: ttmovevalid %s flags %#02x, generate emitted %#02x",
						fen, MoveUCI(byte(f), byte(to), w.flags), gotFlags, w.flags)
				case got:
					cover["accepted"]++
					switch w.flags {
					case byte(defs["FL_EP"]):
						cover["en passant"]++
					case byte(defs["FL_DOUBLE"]):
						cover["double push"]++
					}
					if pos.Board[to] != 0 {
						cover["capture"]++
					}
					if pos.Board[f]&byte(defs["TYPEMASK"]) == 1 { // PAWN
						cover["pawn move"]++
					}
				case !got && special[f][to]:
					cover["rejected promo/castle"]++
				}
			}
		}
		if bad > 5 || missed > 5 {
			t.Errorf("%s: ... and %d more accepted / %d more rejected", fen, bad-5, missed-5)
		}
	}
	for _, k := range []string{"accepted", "capture", "pawn move", "double push",
		"en passant", "rejected promo/castle"} {
		if cover[k] == 0 {
			t.Errorf("corpus never exercised %q — the exhaustive test proves nothing about it", k)
		}
	}
	t.Logf("coverage over %d positions: %v", len(gdCorpus), cover)
}

// TestTTMoveValidRejectsPromoAndCastle pins the two DELIBERATE rejections,
// which the exhaustive test above treats as "generate emits it, validator
// says no". They are load-bearing for tree identity (the TT stores from/to
// only, and pass 0 searches all four promo variants in list order N,B,R,Q,
// so the promotion the TT cutter searches today is the KNIGHT one), so a
// future "optimization" that started accepting them must fail a test.
func TestTTMoveValidRejectsPromoAndCastle(t *testing.T) {
	bin, labels := buildGenDeferVerify(t)
	cases := []struct {
		fen  string
		what string
	}{
		{"5k2/4P3/8/8/8/8/8/4K3 w - - 0 1", "promotion push"},
		{"3r1k2/4P3/8/8/8/8/8/4K3 w - - 0 1", "promotion capture"},
		{"4k3/8/8/8/8/8/4p3/3R2K1 b - - 0 1", "black promotion capture"},
		{"r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1", "castling"},
	}
	for _, c := range cases {
		pos, err := ParseFEN(c.fen)
		if err != nil {
			t.Fatal(err)
		}
		moves := callGenerate(t, bin, labels, pos)
		valid, _ := sweepTTMoveValid(t, bin, labels, pos)
		n := 0
		for _, mv := range moves {
			if mv.flags&promoOrCastle() == 0 {
				continue
			}
			n++
			if valid[mv.from][mv.to] {
				t.Errorf("%s (%s): ttmovevalid accepted %s, which must fall through to generation",
					c.fen, c.what, MoveUCI(mv.from, mv.to, mv.flags))
			}
		}
		if n == 0 {
			t.Errorf("%s: no %s in generate's output — the case is not covered", c.fen, c.what)
		}
	}
}

// TestGenDeferVerify is the CKVERIFY-style live gate: real searches in the
// GDVERIFY variant, which generates the list anyway at every staged node
// and exits 102 if the validated TT move is not in it. Run with the feature
// ON (it is a no-op with it off).
func TestGenDeferVerify(t *testing.T) {
	bin, _ := buildGenDeferVerify(t)
	fens := []string{
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 b - - 3 9",
		"rnbqkbnr/ppppp1pp/8/8/4Pp2/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 3",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		"k7/2P5/1K6/8/8/8/8/8 w - - 0 1",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"8/3k4/p1p5/P1P5/3r4/5R2/5PPP/6K1 w - - 0 1",
	}
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1F|byte(defs["FT_CKEXT"]))
		SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
		SetBudget(m, defs, 0, 6)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		exited, code, err := m.Run(400_000_000_000)
		if err != nil || !exited {
			t.Fatalf("%.30s: exited=%v err=%v", fen, exited, err)
		}
		if code == 102 {
			t.Fatalf("%.30s: ttmovevalid accepted a move generate does not emit (exit 102)", fen)
		}
		if code != 0 && code != 2 {
			t.Fatalf("%.30s: exit code %d", fen, code)
		}
	}
}
