package ui_test

import (
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/ui"
)

// TestBookIntegration: with the resident book at $2000, the engine's turn
// goes through the probe instead of a search, and the UI streams the
// opening's name out of the blob's own name table onto row 16.
//
// The cross-check is against internal/book's parsed entries, keyed on the
// engine's own HASH0-3 read straight out of the machine — so it proves both
// that the played move really came from the book and that the UI's
// Pascal-string walk lands on the name the book agrees with.
func TestBookIntegration(t *testing.T) {
	blob := book.DefaultBlob()
	b, err := book.Load(blob)
	if err != nil {
		t.Fatal(err)
	}
	u, err := ui.Boot(root, blob)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	// Two-player mode so we can look at the position after 1.e4 before the
	// engine answers it.
	twoPlayerPoke(t, u)
	mustEnter(t, u, "e2e4")

	h := u.Defs["HASH0"]
	key := uint32(u.Peek(h)) | uint32(u.Peek(h+1))<<8 |
		uint32(u.Peek(h+2))<<16 | uint32(u.Peek(h+3))<<24
	var candidates []book.Entry
	for _, e := range b.Entries() {
		if e.Key == key {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		t.Fatalf("the book has no entry for the position after 1.e4 (key %#08x)", key)
	}

	const sentinel = 0xEE
	u.Poke(u.Defs["CUROPENING"], sentinel)
	u.Poke(ui.UIHUMAN, 0) // human is White, so the engine answers as Black
	if err := u.Enter(""); err != nil {
		t.Fatal(err)
	}

	hist := u.History()
	if len(hist) != 2 {
		t.Fatalf("the engine did not reply: %v\n%s", hist, u.Screen())
	}
	s := u.Screen()
	t.Logf("after 1.e4 with the resident book loaded:\n%s", s)

	nameID := u.Peek(u.Defs["CUROPENING"])
	if nameID == sentinel {
		t.Fatalf("the probe never ran: CUROPENING is still the sentinel")
	}
	var picked *book.Entry
	for i, e := range candidates {
		if chesstest.MoveUCI(e.From, e.To, e.Flags) == hist[1] {
			picked = &candidates[i]
		}
	}
	if picked == nil {
		var opts []string
		for _, e := range candidates {
			opts = append(opts, chesstest.MoveUCI(e.From, e.To, e.Flags))
		}
		t.Fatalf("the engine played %s, which is not a book move for this key (%v)",
			hist[1], opts)
	}
	if nameID != picked.NameID {
		t.Errorf("CUROPENING = %d, want %d (the chosen entry's NameID)", nameID, picked.NameID)
	}
	row := strings.TrimSpace(s.Text(16))
	if want := "BOOK: " + b.Name(nameID); row != want {
		t.Errorf("row 16 = %q, want %q", row, want)
	}
	t.Logf("book reply %s from %d candidates; CUROPENING %d -> row 16 %q",
		hist[1], len(candidates), nameID, row)

	// Playing out of book must drop the line: a position the book does not
	// know leaves row 16 blank again.
	twoPlayerPoke(t, u)
	mustEnter(t, u, "a2a3")
	mustEnter(t, u, "h7h6")
	mustEnter(t, u, "h2h3")
	u.Poke(ui.UIHUMAN, 0)
	if err := u.Enter(""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(u.Screen().Text(16)); got != "" {
		t.Errorf("out of book, row 16 still says %q", got)
	}
}
