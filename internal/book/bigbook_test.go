package book

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/zellyn/8fish/internal/refchess"
)

// bigbook_test.go gates the CONTENT of the big book — the lichess ECO breadth
// import (BuildBig). The load MECHANISM (ProRWTS2, the checksum verify, the
// BIGBOOKOK latch) has its own end-to-end gates in internal/ui/bigbook_test.go;
// everything here is about the bytes being the right bytes:
//
//   - the curated book is preserved byte-identically inside the big one,
//   - every eco entry is a legal move at a position nothing curated answers,
//   - one move per position, capacity and name budgets hold,
//   - the build is deterministic and the committed blobs are current.

// buildBigOrFatal compiles the big book from the two committed sources.
func buildBigOrFatal(t *testing.T) (entries []Entry, names []string, stats BigStats) {
	t.Helper()
	eco, err := ECOLines()
	if err != nil {
		t.Fatalf("ECOLines (the vendored lichess dataset failed to compile): %v", err)
	}
	entries, names, stats, err = BuildBig(Lines, eco)
	if err != nil {
		t.Fatalf("BuildBig: %v", err)
	}
	return entries, names, stats
}

// TestBigBookDeterministic: two independent builds produce identical bytes —
// no map-iteration order leaks into the emitted blob or the name table.
func TestBigBookDeterministic(t *testing.T) {
	e1, n1, _ := buildBigOrFatal(t)
	e2, n2, _ := buildBigOrFatal(t)
	b1, err := EncodeBig(e1, n1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := EncodeBig(e2, n2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatal("two BuildBig+EncodeBig runs differ: the build is nondeterministic")
	}
	if !bytes.Equal(EncodeNames(n1), EncodeNames(n2)) {
		t.Fatal("two BuildBig name tables differ: the build is nondeterministic")
	}
}

// TestBigBookGeneratedCurrent: the committed bigbook.bin and booknames.bin are
// exactly what the committed sources build — run `go run ./cmd/genbook` if
// this fails. This is also the determinism gate's other half: the blob on disk
// was built by an earlier run.
func TestBigBookGeneratedCurrent(t *testing.T) {
	entries, names, _ := buildBigOrFatal(t)
	blob, err := EncodeBig(entries, names)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(blob, DefaultBig()) {
		t.Fatal("embedded bigbook.bin != freshly built big book; run `go run ./cmd/genbook`")
	}
	if !bytes.Equal(EncodeNames(names), DefaultNames()) {
		t.Fatal("embedded booknames.bin != freshly built name table; run `go run ./cmd/genbook`")
	}
	// The trailer really is the checksum of everything before it (the 6502
	// verify loop's contract).
	sum := Checksum16(blob[:BigWindow-2])
	if got := uint16(blob[BigWindow-2]) | uint16(blob[BigWindow-1])<<8; got != sum {
		t.Fatalf("big blob trailer $%04X != checksum $%04X", got, sum)
	}
}

// TestBigBookCuratedPreserved: the curated book rides inside the big one
// UNCHANGED — same entries, same bytes, same relative order, same name IDs.
// Encode's sort is stable and BuildBig appends eco entries after the curated
// ones, so the curated entries must appear in the encoded big blob as an
// ordered subsequence whose 9-byte encodings are byte-identical to the
// resident blob's; and the curated names must be the first names, verbatim.
func TestBigBookCuratedPreserved(t *testing.T) {
	curEntries, curNames, err := Build(Lines)
	if err != nil {
		t.Fatal(err)
	}
	smallBlob, _ := Encode(curEntries, curNames)
	bigEntries, bigNames, _ := buildBigOrFatal(t)
	bigBlob, err := EncodeBig(bigEntries, bigNames)
	if err != nil {
		t.Fatal(err)
	}

	// Names: curated first, verbatim, in order.
	if len(bigNames) < len(curNames) {
		t.Fatalf("big book has %d names, fewer than the %d curated", len(bigNames), len(curNames))
	}
	for i, n := range curNames {
		if bigNames[i] != n {
			t.Fatalf("big-book name %d is %q, want the curated %q — curated NameIDs must not move",
				i, bigNames[i], n)
		}
	}

	// Entries: every curated 9-byte record appears in the big blob, in the
	// small blob's order (ordered subsequence over the sorted arrays).
	nSmall := int(binary.LittleEndian.Uint16(smallBlob[offCount:]))
	nBig := int(binary.LittleEndian.Uint16(bigBlob[offCount:]))
	j := 0
	for i := 0; i < nSmall; i++ {
		want := smallBlob[HeaderSize+i*EntryStride : HeaderSize+(i+1)*EntryStride]
		found := false
		for ; j < nBig; j++ {
			got := bigBlob[HeaderSize+j*EntryStride : HeaderSize+(j+1)*EntryStride]
			if bytes.Equal(got, want) {
				found = true
				j++
				break
			}
		}
		if !found {
			t.Fatalf("curated entry %d/%d (key %08X) is missing from the big book, or out of "+
				"order, or its bytes changed — the curated portion must be byte-identical",
				i, nSmall, binary.LittleEndian.Uint32(want))
		}
	}
	t.Logf("all %d curated entries found byte-identical and in order among the big book's %d",
		nSmall, nBig)
}

// TestBigBookECOEntriesSound: every non-curated entry in the shipped big book
// is (a) at a position NO curated line answers — curated wins, always — and
// (b) a move refchess deems legal at that position, and (c) the only eco
// entry at its position (one move per position: breadth, not variety).
func TestBigBookECOEntriesSound(t *testing.T) {
	big, err := DefaultBigBook()
	if err != nil {
		t.Fatal(err)
	}
	curEntries, _, err := Build(Lines)
	if err != nil {
		t.Fatal(err)
	}
	curated := map[[7]byte]bool{}
	curatedKeys := map[uint32]bool{}
	for _, e := range curEntries {
		curated[[7]byte{byte(e.Key), byte(e.Key >> 8), byte(e.Key >> 16), byte(e.Key >> 24),
			e.From, e.To, e.Flags}] = true
		curatedKeys[e.Key] = true
	}

	// The legality oracle needs positions, not just keys: re-walk the dataset
	// and index every position it reaches by book key.
	eco, err := ECOLines()
	if err != nil {
		t.Fatal(err)
	}
	fenByKey := map[uint32]string{}
	for _, ln := range eco {
		pos, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for _, uci := range ln.Moves {
			k, err := HashFEN(pos.FEN())
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := fenByKey[k]; !ok {
				fenByKey[k] = pos.FEN()
			}
			mv, err := refchess.ParseMove(uci)
			if err != nil {
				t.Fatal(err)
			}
			if err := pos.Make(mv); err != nil {
				t.Fatal(err)
			}
		}
	}

	ecoSeen := map[uint32]bool{}
	nECO := 0
	for _, e := range big.Entries() {
		mk := [7]byte{byte(e.Key), byte(e.Key >> 8), byte(e.Key >> 16), byte(e.Key >> 24),
			e.From, e.To, e.Flags}
		if curated[mk] {
			continue // a curated entry, byte-preserved (see the gate above)
		}
		nECO++
		if curatedKeys[e.Key] {
			t.Errorf("eco entry %08X %s offers an ALTERNATIVE move at a position the curated "+
				"book answers — curated must win", e.Key, entryUCI(e))
			continue
		}
		if ecoSeen[e.Key] {
			t.Errorf("position %08X has more than one eco entry — the import promises one "+
				"move per position", e.Key)
			continue
		}
		ecoSeen[e.Key] = true
		fen, ok := fenByKey[e.Key]
		if !ok {
			t.Errorf("eco entry %08X %s is at a position the dataset never reaches — where "+
				"did it come from?", e.Key, entryUCI(e))
			continue
		}
		pos, err := refchess.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		legal := false
		for _, mv := range pos.LegalMoves() {
			if mv.String() == entryUCI(e) {
				legal = true
				break
			}
		}
		if !legal {
			t.Errorf("eco entry %08X: %s is not a legal move at %s", e.Key, entryUCI(e), fen)
		}
	}
	if nECO == 0 {
		t.Fatal("the shipped big book contains no eco entries at all — the import is not wired in")
	}
	t.Logf("verified %d eco entries: legal, curated-disjoint, one per position", nECO)
}

// TestBigBookBudgets: the hard ceilings, asserted against the SHIPPED blobs.
// EncodeBig and BuildBig enforce these at build time; this holds the committed
// artifacts to them, so a hand-edited or stale blob fails too.
func TestBigBookBudgets(t *testing.T) {
	big, err := DefaultBigBook()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(big.Entries()); got > BigMaxEntries {
		t.Errorf("big book has %d entries, over the %d capacity", got, BigMaxEntries)
	}
	if got := len(DefaultBig()); got != BigWindow {
		t.Errorf("bigbook.bin is %d bytes, want exactly the %d-byte window", got, BigWindow)
	}
	nameCt := int(DefaultBig()[offNameCt])
	if nameCt > 255 {
		t.Errorf("big book claims %d names; NameID is one byte", nameCt)
	}
	if got := len(DefaultNames()); got > NamesStageMax {
		t.Errorf("booknames.bin is %d bytes, over the %d-byte stage-2 window "+
			"($1600-$1CFF): this moves asm/m8.s's SD2RWTS and delivery.RwtsOrg — "+
			"a delivery change, which a book fill must not make", got, NamesStageMax)
	}
	// Every entry's NameID resolves inside the shipped table (uibookname walks
	// it positionally with no bounds check — an out-of-range ID would render
	// garbage from Language Card bank 2).
	for _, e := range big.Entries() {
		if int(e.NameID) >= nameCt {
			t.Fatalf("entry %08X has NameID %d but the table has %d names", e.Key, e.NameID, nameCt)
		}
	}
	// How full did we get? Informational, but a floor guards against the
	// import silently importing almost nothing.
	fill := 100 * float64(len(big.Entries())) / float64(BigMaxEntries)
	t.Logf("big book: %d / %d entries (%.1f%% full), %d names, name table %d B of %d",
		len(big.Entries()), BigMaxEntries, fill, nameCt, len(DefaultNames()), NamesStageMax)
	if fill < 90 {
		t.Errorf("big book is only %.1f%% full; the lichess import should fill it past 90%%", fill)
	}
}
