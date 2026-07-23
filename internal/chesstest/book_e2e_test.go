package chesstest

import (
	"math/rand/v2"
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/refchess"
)

// TestBookFollowThenSearchDriver (deliverable e, driver level): load the
// resident blob, play from the start driving the engine's ON-DEVICE probe
// move-by-move, and assert the emulated engine follows the Go book probe's
// line for a fixed seed — same moves AND same NAMEID (current-opening byte)
// at every ply — then reports a miss (out of book) exactly where Go does, so
// the caller would fall through to search.
func TestBookFollowThenSearchDriver(t *testing.T) {
	bin, bookentry, bk, blob := bookProbeDeps(t)

	const seed = 12345
	r := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))

	pos, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}

	var line []string
	bookPlies := 0
	lastName := ""
	transitioned := false
	for ply := 0; ply < 40; ply++ {
		fen := pos.FEN()
		key, err := book.HashFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		rv := r.Uint32() // one draw per ply, hit or miss (matches the bridge)

		// Go reference selection for this (position, r).
		wantE, wantName, wantOK := bk.Probe(key, rv)

		// On-device selection for the same (position, r).
		cpos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		got, err := AsmBookProbe(bin, defs, bookentry, blob, cpos, rv)
		if err != nil {
			t.Fatalf("ply %d: asm probe: %v", ply, err)
		}

		if got.Hit != wantOK {
			t.Fatalf("ply %d fen=%q: hit asm=%v go=%v", ply, fen, got.Hit, wantOK)
		}
		if !wantOK {
			transitioned = true // out of book: the engine would now search
			break
		}
		wantMove := MoveUCI(wantE.From, wantE.To, wantE.Flags)
		if got.Move != wantMove {
			t.Fatalf("ply %d fen=%q: move asm=%s go=%s", ply, fen, got.Move, wantMove)
		}
		if got.NameID != wantE.NameID {
			t.Fatalf("ply %d: NAMEID (current-opening byte) asm=%d go=%d", ply, got.NameID, wantE.NameID)
		}
		// The current-opening byte must name a real opening.
		if bk.Name(got.NameID) == "" {
			t.Fatalf("ply %d: current-opening byte %d has no name", ply, got.NameID)
		}
		lastName = wantName

		mv, err := refchess.ParseMove(got.Move)
		if err != nil {
			t.Fatalf("ply %d: parse engine move %q: %v", ply, got.Move, err)
		}
		if err := pos.Make(mv); err != nil {
			t.Fatalf("ply %d: engine played illegal %q: %v", ply, got.Move, err)
		}
		line = append(line, got.Move)
		bookPlies++
	}

	t.Logf("on-device book line (%d plies), opening=%q: %v", bookPlies, lastName, line)
	if bookPlies < 8 {
		t.Errorf("engine followed book for only %d plies, want >= 8", bookPlies)
	}
	if !transitioned {
		t.Error("engine never transitioned out of book to search within 40 plies")
	}
	if lastName == "" {
		t.Error("current-opening byte never named an opening")
	}
}
