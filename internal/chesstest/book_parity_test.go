package chesstest

import (
	"path/filepath"
	"testing"

	"github.com/zellyn/8fish/internal/book"
	"github.com/zellyn/8fish/internal/refchess"
)

// bookProbeDeps loads the engine binary, the bookentry label, the parsed
// Go book, and the raw resident blob.
func bookProbeDeps(t *testing.T) (bin []byte, bookentry uint16, bk *book.Book, blob []byte) {
	t.Helper()
	bin = loadEngine(t)
	labels, err := ParseLabelFile(filepath.Join("..", "..", "asm", "engine.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	be, ok := labels["bookentry"]
	if !ok {
		t.Fatal("bookentry label not found in engine.lbl")
	}
	bk, err = book.Default()
	if err != nil {
		t.Fatal(err)
	}
	return bin, be, bk, book.DefaultEntries()
}

// enumerateBookPositions BFS-walks every position reachable by playing book
// moves from the start, deduped by 32-bit key. It returns, per position, a
// representative FEN and the summed weight of its equal-key move run.
func enumerateBookPositions(t *testing.T, bk *book.Book) []struct {
	fen   string
	key   uint32
	total uint32
	moves int
} {
	t.Helper()
	type posInfo struct {
		fen   string
		key   uint32
		total uint32
		moves int
	}
	// entries sharing a key, indexed by key.
	byKey := map[uint32][]book.Entry{}
	for _, e := range bk.Entries() {
		byKey[e.Key] = append(byKey[e.Key], e)
	}

	var out []posInfo
	seen := map[uint32]bool{}
	queue := []string{refchess.StartFEN}
	for len(queue) > 0 {
		fen := queue[0]
		queue = queue[1:]
		key, err := book.HashFEN(fen)
		if err != nil {
			t.Fatalf("hash %q: %v", fen, err)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		run := byKey[key]
		if len(run) == 0 {
			continue // out of book: nothing to enumerate here
		}
		var total uint32
		for _, e := range run {
			total += uint32(e.Weight)
		}
		out = append(out, posInfo{fen: fen, key: key, total: total, moves: len(run)})
		// enqueue in-book children
		for _, e := range run {
			pos, err := refchess.ParseFEN(fen)
			if err != nil {
				t.Fatalf("parse %q: %v", fen, err)
			}
			uci := MoveUCI(e.From, e.To, e.Flags)
			mv, err := refchess.ParseMove(uci)
			if err != nil {
				t.Fatalf("book move %q from %q: %v", uci, fen, err)
			}
			if err := pos.Make(mv); err != nil {
				t.Fatalf("make %q from %q: %v", uci, fen, err)
			}
			child := pos.FEN()
			ck, err := book.HashFEN(child)
			if err != nil {
				t.Fatalf("hash child %q: %v", child, err)
			}
			if !seen[ck] && len(byKey[ck]) > 0 {
				queue = append(queue, child)
			}
		}
	}
	res := make([]struct {
		fen   string
		key   uint32
		total uint32
		moves int
	}, len(out))
	for i, p := range out {
		res[i] = struct {
			fen   string
			key   uint32
			total uint32
			moves int
		}{p.fen, p.key, p.total, p.moves}
	}
	return res
}

// TestBookProbeParityASMvsGo is THE correctness gate: for every in-book
// position and a sweep of raw random values r, the on-device asm probe
// (bookentry) selects byte-for-byte the same move (FROM/TO/FLAGS) and NAMEID
// as internal/book.Book.Probe(key, r) fed the identical r. It also proves
// out-of-book positions miss on both sides.
func TestBookProbeParityASMvsGo(t *testing.T) {
	bin, bookentry, bk, blob := bookProbeDeps(t)
	positions := enumerateBookPositions(t, bk)
	if len(positions) < 40 {
		t.Fatalf("enumerated only %d book positions, expected many more", len(positions))
	}

	// r values that exercise the modulo hard: 0, small, mid, large, edges,
	// plus a full residue period 0..total-1 (below) that pins the exact pick.
	extraR := []uint32{0, 1, 2, 3, 7, 41, 255, 256, 1000, 12345, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFE, 0xFFFFFFFF}

	multiMove := 0
	probes := 0
	for _, p := range positions {
		pos, err := ParseFEN(p.fen)
		if err != nil {
			t.Fatalf("chesstest parse %q: %v", p.fen, err)
		}
		if p.moves > 1 {
			multiMove++
		}
		// Sweep one full residue period (r = 0..total-1) so pick = r%total
		// hits every value exactly once — this pins the weighted selection
		// and, aggregated, the exact weight distribution. Add the wide edge
		// values on top.
		rs := make([]uint32, 0, int(p.total)+len(extraR))
		for r := uint32(0); r < p.total; r++ {
			rs = append(rs, r)
		}
		rs = append(rs, extraR...)

		for _, r := range rs {
			wantE, _, wantOK := bk.Probe(p.key, r)
			got, err := AsmBookProbe(bin, defs, bookentry, blob, pos, r)
			if err != nil {
				t.Fatalf("asm probe fen=%q r=%d: %v", p.fen, r, err)
			}
			probes++
			if got.Hit != wantOK {
				t.Fatalf("fen=%q r=%d: hit asm=%v go=%v", p.fen, r, got.Hit, wantOK)
			}
			if !wantOK {
				continue
			}
			wantMove := MoveUCI(wantE.From, wantE.To, wantE.Flags)
			if got.Move != wantMove {
				t.Fatalf("fen=%q r=%d: move asm=%s go=%s", p.fen, r, got.Move, wantMove)
			}
			if got.From != wantE.From || got.To != wantE.To || got.Flags != wantE.Flags {
				t.Fatalf("fen=%q r=%d: bytes asm=(%d,%d,%d) go=(%d,%d,%d)",
					p.fen, r, got.From, got.To, got.Flags, wantE.From, wantE.To, wantE.Flags)
			}
			if got.NameID != wantE.NameID {
				t.Fatalf("fen=%q r=%d: nameID asm=%d go=%d", p.fen, r, got.NameID, wantE.NameID)
			}
		}
	}
	t.Logf("parity OK: %d positions (%d multi-move), %d probes", len(positions), multiMove, probes)
	if multiMove < 3 {
		t.Errorf("expected several multi-move key-runs, saw %d", multiMove)
	}
}

// TestBookProbeWeightedDistribution: over a full residue period the asm
// probe's per-move pick frequency equals each move's weight exactly (same
// as Go), for the multi-reply positions (e.g. startpos's 4 replies and the
// deepest 4-way transposition).
func TestBookProbeWeightedDistribution(t *testing.T) {
	bin, bookentry, bk, blob := bookProbeDeps(t)
	byKey := map[uint32][]book.Entry{}
	for _, e := range bk.Entries() {
		byKey[e.Key] = append(byKey[e.Key], e)
	}
	positions := enumerateBookPositions(t, bk)

	tested := 0
	for _, p := range positions {
		if p.moves < 2 {
			continue
		}
		run := byKey[p.key]
		wantFreq := map[string]uint32{}
		for _, e := range run {
			wantFreq[MoveUCI(e.From, e.To, e.Flags)] += uint32(e.Weight)
		}
		pos, err := ParseFEN(p.fen)
		if err != nil {
			t.Fatal(err)
		}
		gotFreq := map[string]uint32{}
		for r := uint32(0); r < p.total; r++ {
			got, err := AsmBookProbe(bin, defs, bookentry, blob, pos, r)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Hit {
				t.Fatalf("fen=%q r=%d: unexpected miss", p.fen, r)
			}
			gotFreq[got.Move]++
		}
		for mv, w := range wantFreq {
			if gotFreq[mv] != w {
				t.Errorf("fen=%q move=%s: asm freq=%d want weight=%d", p.fen, mv, gotFreq[mv], w)
			}
		}
		if len(gotFreq) != len(wantFreq) {
			t.Errorf("fen=%q: asm produced %d distinct moves, want %d", p.fen, len(gotFreq), len(wantFreq))
		}
		tested++
	}
	t.Logf("weighted-distribution parity over %d multi-move positions", tested)
	if tested < 3 {
		t.Errorf("expected >=3 multi-move positions, tested %d", tested)
	}
}

// TestBigBookProbeParityASMvsGo re-runs the correctness gate over the BIG
// BOOK at its aux $4000 home (the idle transposition-table window), with the
// engine's `bookpg` byte poked to $40 exactly as asm/m8.s does while the
// BIGBOOKOK latch is open. Same sweep discipline as the resident gate: every
// in-book position, a full residue period of r, plus the wide edges.
//
// It also pins the shipped DEFAULT: engine.bin must carry bookpg = $08 (the
// resident blob), because every bookless and BLOAD path relies on it.
func TestBigBookProbeParityASMvsGo(t *testing.T) {
	bin := loadEngine(t)
	labels, err := ParseLabelFile(filepath.Join("..", "..", "asm", "engine.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	bookentry, ok := labels["bookentry"]
	if !ok {
		t.Fatal("bookentry label not found in engine.lbl")
	}
	bookpg, ok := labels["bookpg"]
	if !ok {
		t.Fatal("bookpg label not found in engine.lbl (the probe's base-page byte)")
	}
	if got := bin[bookpg-0x4000]; got != byte(BookBase>>8) {
		t.Fatalf("engine.bin ships bookpg = $%02X, want $%02X (the resident blob's page)",
			got, BookBase>>8)
	}
	bk, err := book.DefaultBigBook()
	if err != nil {
		t.Fatal(err)
	}
	blob := book.DefaultBig()
	if len(blob) != book.BigWindow {
		t.Fatalf("bigbook.bin is %d B, want the full %d-byte window (zero-padded, "+
			"checksum trailer in the last two bytes)", len(blob), book.BigWindow)
	}
	if s := book.Checksum16(blob[:len(blob)-2]); blob[len(blob)-2] != byte(s) || blob[len(blob)-1] != byte(s>>8) {
		t.Fatalf("bigbook.bin checksum trailer %02X%02X does not match the recomputed %04X",
			blob[len(blob)-1], blob[len(blob)-2], s)
	}

	positions := enumerateBookPositions(t, bk)
	if len(positions) < 40 {
		t.Fatalf("enumerated only %d big-book positions", len(positions))
	}
	extraR := []uint32{0, 1, 255, 12345, 0x80000000, 0xFFFFFFFF}
	probes := 0
	for _, p := range positions {
		pos, err := ParseFEN(p.fen)
		if err != nil {
			t.Fatalf("parse %q: %v", p.fen, err)
		}
		rs := make([]uint32, 0, int(p.total)+len(extraR))
		for r := uint32(0); r < p.total; r++ {
			rs = append(rs, r)
		}
		rs = append(rs, extraR...)
		for _, r := range rs {
			wantE, _, wantOK := bk.Probe(p.key, r)
			got, err := AsmBookProbeAt(bin, defs, bookentry, bookpg, book.BigBase, blob, pos, r)
			if err != nil {
				t.Fatalf("asm big-book probe fen=%q r=%d: %v", p.fen, r, err)
			}
			probes++
			if got.Hit != wantOK {
				t.Fatalf("fen=%q r=%d: hit asm=%v go=%v", p.fen, r, got.Hit, wantOK)
			}
			if !wantOK {
				continue
			}
			if got.From != wantE.From || got.To != wantE.To || got.Flags != wantE.Flags ||
				got.NameID != wantE.NameID {
				t.Fatalf("fen=%q r=%d: asm=(%d,%d,%d,n%d) go=(%d,%d,%d,n%d)", p.fen, r,
					got.From, got.To, got.Flags, got.NameID,
					wantE.From, wantE.To, wantE.Flags, wantE.NameID)
			}
		}
	}
	t.Logf("big-book parity OK at aux $%04X: %d positions, %d probes", book.BigBase, len(positions), probes)
}

// TestBookProbeOutOfBook: positions never in the book miss on the asm probe
// (BOOKHIT=0), matching Go.
func TestBookProbeOutOfBook(t *testing.T) {
	bin, bookentry, bk, blob := bookProbeDeps(t)
	outOfBook := []string{
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	}
	for _, fen := range outOfBook {
		key, err := book.HashFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok := bk.Probe(key, 0); ok {
			t.Fatalf("fen=%q unexpectedly IN the Go book", fen)
		}
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range []uint32{0, 12345, 0xFFFFFFFF} {
			got, err := AsmBookProbe(bin, defs, bookentry, blob, pos, r)
			if err != nil {
				t.Fatal(err)
			}
			if got.Hit {
				t.Errorf("fen=%q r=%d: asm probe hit an out-of-book position (move %s)", fen, r, got.Move)
			}
		}
	}
}
