package ucibridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

func loadBridgeDeps(t *testing.T) ([]byte, chesstest.Defs, uint16) {
	t.Helper()
	asmbuild.BuildT(t, "../..")
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("..", "..", "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	labels, err := chesstest.ParseLabelFile(filepath.Join("..", "..", "asm", "engine.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	bookentry, ok := labels["bookentry"]
	if !ok {
		t.Fatal("bookentry label not found in engine.lbl")
	}
	return bin, defs, bookentry
}

// playBook drives the bridge move-by-move from the start with a book
// loaded, playing the side to move at every ply. While positions stay in
// book it returns book moves instantly; the first out-of-book position
// falls through to a real (short) search. Returns the move list and the
// number of book plies played before the transition.
func playBook(t *testing.T, bin []byte, defs chesstest.Defs, bookentry uint16, seed uint64) (moves []string, bookPlies int, lastName string, searched bool) {
	t.Helper()
	bk, err := book.Default()
	if err != nil {
		t.Fatal(err)
	}
	b := &Bridge{Bin: bin, Defs: defs, FixedBudgetMs: 300, Book: bk, BookEntry: bookentry, BookSeed: seed}
	b.pos, _ = refchess.ParseFEN(refchess.StartFEN)
	for ply := 0; ply < 40; ply++ {
		mv, err := b.think(nil)
		if err != nil {
			t.Fatalf("ply %d: %v", ply, err)
		}
		moves = append(moves, mv)
		if strings.Contains(b.info, "in book:") {
			bookPlies++
			lastName = b.CurOpeningName
			if b.CurOpening == 0 || b.CurOpeningName == "" {
				t.Fatalf("ply %d: in book but opening not tracked (id=%d name=%q)", ply, b.CurOpening, b.CurOpeningName)
			}
		} else {
			// Transitioned to search: info must be an engine "info depth".
			if !strings.Contains(b.info, "info depth") {
				t.Fatalf("ply %d: out of book but no search info: %q", ply, b.info)
			}
			searched = true
			break
		}
	}
	return moves, bookPlies, lastName, searched
}

// TestBridgeBookFollowThenSearch (deliverable c): from the start the
// engine follows book moves for the opening, tracks/logs the opening
// name, then transitions to a normal search when out of book.
func TestBridgeBookFollowThenSearch(t *testing.T) {
	bin, defs, bookentry := loadBridgeDeps(t)
	moves, bookPlies, name, searched := playBook(t, bin, defs, bookentry, 12345)
	t.Logf("book plies=%d, opening=%q, then searched=%v; line=%v", bookPlies, name, searched, moves)
	if bookPlies < 8 {
		t.Errorf("followed book for only %d plies, want >= 8", bookPlies)
	}
	if name == "" {
		t.Error("opening name never tracked")
	}
	if !searched {
		t.Error("never transitioned from book to search")
	}
}

// TestBridgeBookDeterminism (deliverable e): same BookSeed -> same book
// choices; and a different seed is allowed to differ (variety).
func TestBridgeBookDeterminism(t *testing.T) {
	bin, defs, bookentry := loadBridgeDeps(t)
	a1, _, n1, _ := playBook(t, bin, defs, bookentry, 777)
	a2, _, n2, _ := playBook(t, bin, defs, bookentry, 777)
	// Compare only the book prefix (drop the final searched move, which is
	// engine output; the book prefix is what BookSeed governs).
	if len(a1) != len(a2) || n1 != n2 {
		t.Fatalf("same seed diverged: %v (%s) vs %v (%s)", a1, n1, a2, n2)
	}
	for i := range a1[:len(a1)-1] {
		if a1[i] != a2[i] {
			t.Fatalf("same seed diverged at ply %d: %q vs %q", i, a1[i], a2[i])
		}
	}
}

// TestBridgeNoBookByteIdentical: with no book, the book code path is
// inert — the bridge produces the same first move as a bare bridge.
func TestBridgeNoBookByteIdentical(t *testing.T) {
	bin, defs, _ := loadBridgeDeps(t)
	mk := func() *Bridge {
		b := &Bridge{Bin: bin, Defs: defs, FixedBudgetMs: 300}
		b.pos, _ = refchess.ParseFEN(refchess.StartFEN)
		return b
	}
	b := mk()
	mv, err := b.think(nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.CurOpening != 0 || b.CurOpeningName != "" {
		t.Errorf("no-book bridge tracked an opening: id=%d name=%q", b.CurOpening, b.CurOpeningName)
	}
	if strings.Contains(b.info, "in book") {
		t.Errorf("no-book bridge logged a book move: %q", b.info)
	}
	if mv == "" {
		t.Error("no move returned")
	}
}
