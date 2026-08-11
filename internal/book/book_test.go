package book

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

// buildOrFatal compiles the curated lines; Build fails loudly on any
// illegal move, so a success here is deliverable (a): every line legal.
func buildOrFatal(t *testing.T) *Book {
	t.Helper()
	entries, names, err := Build(Lines)
	if err != nil {
		t.Fatalf("Build (a line is illegal): %v", err)
	}
	bk, err := Load(Encode(entries, names))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return bk
}

// TestResidentPiecesFitTheirHomes (deliverable d): the two resident pieces fit
// where they go, and -- the constraint the split exists for -- the entries
// clear the 80-column text page's aux half entirely.
//
// The delivery-side gate (internal/delivery's TestBookClearsTheAuxTextPage)
// checks the same thing against the SHIPPED addresses; this one checks it
// against a freshly compiled book, so growing openings.txt fails here first.
func TestResidentPiecesFitTheirHomes(t *testing.T) {
	bk := buildOrFatal(t)
	if BaseAddr < AuxTextHi {
		t.Fatalf("the book is based at aux $%04X, inside or below the 80-column text "+
			"page's aux half ($%04X-$%04X). Mixed mode's four text rows are fetched "+
			"from there; the book cannot be under them", BaseAddr, AuxTextLo, AuxTextHi-1)
	}
	if BaseAddr+bk.Size() > 0x2000 {
		t.Errorf("the entries are %d B at aux $%04X: %d B into the DHGR aux half at $2000",
			bk.Size(), BaseAddr, BaseAddr+bk.Size()-0x2000)
	}
	if bk.NamesSize() > NamesMaxSize {
		t.Errorf("the name table is %d B: %d B more than all of Language Card bank 2 (%d B)",
			bk.NamesSize(), bk.NamesSize()-NamesMaxSize, NamesMaxSize)
	}
	t.Logf("entries %d / %d B at aux $%04X-$%04X (%d B spare below the DHGR aux half)",
		bk.Size(), MaxSize, BaseAddr, BaseAddr+bk.Size()-1, MaxSize-bk.Size())
	t.Logf("names   %d / %d B at LC bank 2 $%04X-$%04X (%d B spare)",
		bk.NamesSize(), NamesMaxSize, NamesAddr, NamesAddr+bk.NamesSize()-1,
		NamesMaxSize-bk.NamesSize())
}

// TestLoadWithoutNames: the name table is optional, because nothing about
// SELECTION reads it. `cmd/sprt --bookA <blob>` depends on this.
func TestLoadWithoutNames(t *testing.T) {
	entries, names, err := Build(Lines)
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := Encode(entries, names)
	bk, err := Load(blob, nil)
	if err != nil {
		t.Fatalf("Load without names: %v", err)
	}
	full := buildOrFatal(t)
	if len(bk.Entries()) != len(full.Entries()) {
		t.Errorf("a nameless book has %d entries, the full one %d",
			len(bk.Entries()), len(full.Entries()))
	}
	if got := bk.Name(0); got != "" {
		t.Errorf("a nameless book returned Name(0) = %q, want \"\"", got)
	}
	// The move it plays must be identical: names are display, not selection.
	for _, e := range full.Entries()[:1] {
		g, _, ok1 := bk.Probe(e.Key, 0)
		w, _, ok2 := full.Probe(e.Key, 0)
		if ok1 != ok2 || g != w {
			t.Errorf("nameless probe %v/%v differs from the full book's %v/%v", g, ok1, w, ok2)
		}
	}
}

// TestLinesLegal (deliverable a): every curated line is legal move-by-
// move through refchess, and re-validating independently confirms it.
func TestLinesLegal(t *testing.T) {
	if _, _, err := Build(Lines); err != nil {
		t.Fatalf("curated line illegal: %v", err)
	}
	for _, ln := range Lines {
		pos, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for i, uci := range ln.Moves {
			mv, err := refchess.ParseMove(uci)
			if err != nil {
				t.Fatalf("%s %s ply %d %q: %v", ln.ECO, ln.Name, i+1, uci, err)
			}
			if err := pos.Make(mv); err != nil {
				t.Fatalf("%s %s ply %d %q ILLEGAL: %v", ln.ECO, ln.Name, i+1, uci, err)
			}
		}
	}
}

// TestBuildRefusesIllegal proves the generator fails loudly on a bad line.
func TestBuildRefusesIllegal(t *testing.T) {
	bad := []Line{{"X", "Bogus", SideBoth, []string{"e2e4", "e7e5", "e1e3"}}} // king can't jump to e3
	if _, _, err := Build(bad); err == nil {
		t.Fatal("Build accepted an illegal line")
	}
}

// TestBlobSize (deliverable d): the resident ENTRIES fit their aux window.
func TestBlobSize(t *testing.T) {
	bk := buildOrFatal(t)
	if bk.Size() >= MaxSize {
		t.Fatalf("entries blob %d >= %d (aux $%04X-$1FFF)", bk.Size(), MaxSize, BaseAddr)
	}
	// The old 6 KB "margin target" dated from the 48-line depth-first book,
	// which used 47% of the hole. Breadth is what the book is FOR, and the
	// hole is not needed by anything else, so the target is now a headroom
	// floor: leave at least 256 bytes so openings.txt stays editable without
	// an immediate re-budget.
	if free := MaxSize - bk.Size(); free < 256 {
		t.Errorf("entries blob %d bytes leaves only %d free of %d; keep >= 256 B of "+
			"headroom", bk.Size(), free, MaxSize)
	}
	t.Logf("entries=%d bytes  count=%d  free=%d  |  names=%d bytes  free=%d",
		bk.Size(), len(bk.Entries()), MaxSize-bk.Size(),
		bk.NamesSize(), NamesMaxSize-bk.NamesSize())
	// The embedded (generated) pieces must match a fresh build.
	if got, want := len(DefaultEntries()), bk.Size(); got != want {
		t.Errorf("embedded entries blob %d bytes != freshly built %d; run "+
			"`go run ./cmd/genbook`", got, want)
	}
	// The embedded name table is the BIG book's (curated names first, then the
	// eco names BuildBig appends — see EncodeNames); the curated table must be
	// its exact PREFIX, so the small book's 80 NameIDs read the same text.
	if got, want := DefaultNames(), bk.NamesBlob(); len(got) < len(want) ||
		!bytes.Equal(got[:len(want)], want) {
		t.Errorf("embedded name table does not start with the freshly built curated "+
			"table (%d B vs %d B prefix); run `go run ./cmd/genbook`", len(got), len(want))
	}
}

// allMoves returns every book UCI move for a position given by FEN.
func allMoves(t *testing.T, bk *Book, fen string) []string {
	t.Helper()
	key, err := HashFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range bk.Entries() {
		if e.Key == key {
			out = append(out, chesstest.MoveUCI(e.From, e.To, e.Flags))
		}
	}
	sort.Strings(out)
	return out
}

// TestProbeKnownPositions (deliverable b): probe returns the expected
// book move(s) on a set of known opening positions.
func TestProbeKnownPositions(t *testing.T) {
	bk := buildOrFatal(t)
	cases := []struct {
		name string
		fen  string
		want []string // sorted set of all book moves for this position
	}{
		{"startpos", refchess.StartFEN, []string{"c2c4", "d2d4", "e2e4", "g1f3"}},
		{"after 1.e4",
			"rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1",
			[]string{"c7c5", "c7c6", "d7d5", "d7d6", "e7e5", "e7e6", "g7g6", "g8f6"}},
		// After 5...Nf6 6.Nc3 the four main Sicilians transpose to one
		// position; the book therefore offers all four replies here.
		{"Open Sicilian move 6 (transposition)",
			"rnbqkb1r/pp2pppp/3p1n2/8/3NP3/2N5/PPP2PPP/R1BQKB1R b KQkq - 2 5",
			[]string{"a7a6", "b8c6", "e7e6", "g7g6"}},
	}
	for _, tc := range cases {
		got := allMoves(t, bk, tc.fen)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: book moves = %v, want %v", tc.name, got, tc.want)
		}
		// Probe must return one of the listed moves.
		key, _ := HashFEN(tc.fen)
		e, _, ok := bk.Probe(key, 0)
		if !ok {
			t.Errorf("%s: Probe miss on a known book position", tc.name)
			continue
		}
		uci := chesstest.MoveUCI(e.From, e.To, e.Flags)
		if !contains(tc.want, uci) {
			t.Errorf("%s: Probe returned %q not in %v", tc.name, uci, tc.want)
		}
	}
}

// TestProbeMiss confirms an out-of-book position returns ok=false.
func TestProbeMiss(t *testing.T) {
	bk := buildOrFatal(t)
	// A random middlegame position not in any curated line.
	key, err := HashFEN("8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 3 9")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := bk.Probe(key, 0); ok {
		t.Fatal("Probe hit on an out-of-book position")
	}
}

// TestDeterminism (deliverable e): same seed -> same book choice; and the
// weighted pick honors weights across the r range.
func TestDeterminism(t *testing.T) {
	bk := buildOrFatal(t)
	key, _ := HashFEN(refchess.StartFEN)
	for r := uint32(0); r < 1000; r++ {
		a, _, ok1 := bk.Probe(key, r)
		b, _, ok2 := bk.Probe(key, r)
		if !ok1 || !ok2 || a != b {
			t.Fatalf("r=%d: non-deterministic probe", r)
		}
	}
	// Sweep the whole weight range: every equal-key move must be reachable.
	seen := map[string]bool{}
	var total uint32
	for _, e := range bk.Entries() {
		if e.Key == key {
			total += uint32(e.Weight)
		}
	}
	for r := uint32(0); r < total; r++ {
		e, _, _ := bk.Probe(key, r)
		seen[chesstest.MoveUCI(e.From, e.To, e.Flags)] = true
	}
	if len(seen) != 4 { // startpos has 4 distinct book moves
		t.Errorf("weighted sweep reached %d distinct moves, want 4", len(seen))
	}
}

// TestBookKeyMatchesASM is the KEYING EVIDENCE (the correctness hinge):
// the book key (HashFEN, via mirror.Pos.Hash) must equal the asm engine's
// HASH0-3. It reads the ACTUAL Zobrist key tables out of engine.bin and
// recomputes the hash from scratch, then compares to HashFEN. Skips if
// engine.lbl has not been built (`make engine`).
func TestBookKeyMatchesASM(t *testing.T) {
	root := filepath.Join("..", "..")
	lblPath := filepath.Join(root, "asm", "engine.lbl")
	binPath := filepath.Join(root, "asm", "engine.bin")
	lblData, err := os.ReadFile(lblPath)
	if err != nil {
		t.Skipf("engine.lbl not built (run `make engine`): %v", err)
	}
	bin, err := os.ReadFile(binPath)
	if err != nil {
		t.Skipf("engine.bin missing: %v", err)
	}
	labels := map[string]uint16{}
	sc := bufio.NewScanner(strings.NewReader(string(lblData)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 3 && f[0] == "al" && strings.HasPrefix(f[2], ".") {
			if v, e := strconv.ParseUint(f[1], 16, 32); e == nil && v <= 0xFFFF {
				labels[f[2][1:]] = uint16(v)
			}
		}
	}
	for _, n := range []string{"ZKEYS", "STMKEY", "CASTKEYS", "EPKEYS"} {
		if _, ok := labels[n]; !ok {
			t.Fatalf("label %s missing from engine.lbl", n)
		}
	}
	const base = 0x4000 // engine.cfg: MAIN start
	rd := func(addr uint16) byte { return bin[int(addr)-base] }

	asmHash := func(fen string) uint32 {
		pos, err := chesstest.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		var w [4]byte
		xor4 := func(addr uint16) {
			for i := range w {
				w[i] ^= rd(addr + uint16(i))
			}
		}
		zkeys := labels["ZKEYS"]
		for _, sq := range pos.PieceSq {
			if sq == 0xFF {
				continue
			}
			piece := pos.Board[sq]
			kind := uint16(piece&7) - 1 + 6*uint16(piece>>3&1)
			for p := range w {
				w[p] ^= rd(zkeys + kind*512 + uint16(p)*128 + uint16(sq))
			}
		}
		if pos.Side != 0 {
			xor4(labels["STMKEY"])
		}
		xor4(labels["CASTKEYS"] + 4*uint16(pos.Castle))
		if pos.EpSq != 0xFF {
			xor4(labels["EPKEYS"] + 4*uint16(pos.EpSq&7))
		}
		return uint32(w[0]) | uint32(w[1])<<8 | uint32(w[2])<<16 | uint32(w[3])<<24
	}

	// Every position reachable in the curated book, plus a few edge cases.
	fens := map[string]bool{}
	for _, ln := range Lines {
		pos, _ := refchess.ParseFEN(refchess.StartFEN)
		fens[pos.FEN()] = true
		for _, uci := range ln.Moves {
			mv, _ := refchess.ParseMove(uci)
			_ = pos.Make(mv)
			fens[pos.FEN()] = true
		}
	}
	for fen := range fens {
		want := asmHash(fen)
		got, err := HashFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("hash mismatch for %s: HashFEN=%08X asm=%08X", fen, got, want)
		}
	}
	t.Logf("verified HashFEN == asm engine.bin hash for %d book positions", len(fens))
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
