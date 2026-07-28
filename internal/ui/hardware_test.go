package ui_test

// Real-hardware fidelity probes for the SHIPPING image (2026-07-28 review).
//
// Everything here drives asm/m8.bin — the build that goes on the disk —
// through goapple2/iie's modelled IIe keyboard, and asks questions about
// what a physical Apple IIe would do rather than what the harness does.
//
// Where the emulator cannot model the thing (80STORE, Ctrl-Reset), the
// finding is argued in the review text, not here; these are the parts that
// CAN be executed.

import (
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/ui"
)

// TestCapsLock: a IIe keyboard with CAPS LOCK UP sends lowercase ($E1-$FA,
// i.e. $61-$7A after the strobe bit is masked); with CAPS LOCK DOWN it sends
// uppercase ($C1-$DA -> $41-$5A). goapple2's keyboard latches key&$7F|$80,
// which is exactly what the hardware latch holds, so both cases are faithful
// here. Every input path must accept both: move entry, the single-letter
// commands, the promotion prompt and the level prompt.
func TestCapsLock(t *testing.T) {
	for _, tc := range []struct{ name, moves string }{
		{"lowercase (caps lock UP)", "e2e4"},
		{"uppercase (caps lock DOWN)", "E2E4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := bootShipping(t)
			if err := u.TwoPlayer(); err != nil {
				t.Fatal(err)
			}
			if err := u.Enter(tc.moves); err != nil {
				t.Fatal(err)
			}
			if got := u.History(); len(got) != 1 || got[0] != "e2e4" {
				t.Fatalf("typing %q played %v, want [e2e4]", tc.moves, got)
			}
		})
	}

	// Commands. "S" cycles sides, "T" takes back, "N" starts a new game,
	// "?" is help. Each must work with CAPS LOCK down.
	t.Run("uppercase commands", func(t *testing.T) {
		u := bootShipping(t)
		if err := u.TwoPlayer(); err != nil {
			t.Fatal(err)
		}
		if err := u.Enter("E2E4"); err != nil {
			t.Fatal(err)
		}
		if err := u.Enter("T"); err != nil { // takeback
			t.Fatal(err)
		}
		if got := u.History(); len(got) != 0 {
			t.Errorf("uppercase T did not take back: history %v", got)
		}
		if err := u.Enter("?"); err != nil {
			t.Fatal(err)
		}
		if got := u.Screen().Text(17); !strings.Contains(got, "MOVES ARE") {
			t.Errorf("uppercase ? gave row 17 = %q, want the help message", strings.TrimSpace(got))
		}
		if err := u.Enter("N"); err != nil {
			t.Fatal(err)
		}
		if got := u.Peek(ui.UIHCNT); got != 0 {
			t.Errorf("uppercase N did not start a new game: UIHCNT = %d", got)
		}
	})

	// The promotion prompt reads a bare key, not a line, so it has its own
	// case-folding call. Play a position where a pawn promotes and answer
	// the prompt in upper case.
	t.Run("uppercase promotion answer", func(t *testing.T) {
		u := bootShipping(t)
		if err := u.TwoPlayer(); err != nil {
			t.Fatal(err)
		}
		// 1.a4 b5 2.axb5 Nf6 3.b6 Ng8 4.bxa7 Nf6 5.axb8=? (capture-promotion)
		for _, m := range []string{"a2a4", "b7b5", "a4b5", "g8f6", "b5b6", "f6g8", "b6a7", "g8f6"} {
			if err := u.Enter(m); err != nil {
				t.Fatalf("%s: %v", m, err)
			}
		}
		if err := u.Enter("a7b8"); err != nil { // no fifth character: prompts
			t.Fatal(err)
		}
		if got := u.Screen().Text(17); !strings.Contains(got, "PROMOTE TO") {
			t.Fatalf("row 17 = %q, want the promotion prompt", strings.TrimSpace(got))
		}
		if err := u.Key('Q'); err != nil { // CAPS LOCK down
			t.Fatal(err)
		}
		h := u.History()
		if len(h) == 0 || h[len(h)-1] != "a7b8q" {
			t.Errorf("uppercase Q at the promotion prompt gave %v, want ...a7b8q", h)
		}
	})
}

// TestKeyboardEdgeCases: the keys a IIe keyboard produces that are not
// letters or digits. entkey masks the strobe off, so what the line editor
// sees is $00-$7F:
//
//	$08  left arrow   -> backspace
//	$7F  DELETE       -> backspace (the IIe DELETE key sends $FF)
//	$15  right arrow  -> ignored (it is a control code, < $20)
//	$0B / $0A  up / down arrow -> ignored
//	$1B  ESC          -> ignored on a line, CANCELS the modal prompts
//
// Nothing here may enter the buffer, hang the editor, or reach the board.
func TestKeyboardEdgeCases(t *testing.T) {
	u := bootShipping(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	// Arrows and ESC in the middle of a move must be swallowed, and the
	// move must still play.
	for _, k := range []byte{'e', '2', 0x15, 0x0B, 0x0A, 0x1B, 0x00, 0x1F, 'e', '4'} {
		if err := u.Key(k); err != nil {
			t.Fatalf("key $%02X: %v", k, err)
		}
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE? e2e4") {
		t.Fatalf("row 23 = %q, want e2e4 with the control keys swallowed", strings.TrimSpace(got))
	}
	if err := u.Key(0x0D); err != nil {
		t.Fatal(err)
	}
	if got := u.History(); len(got) != 1 || got[0] != "e2e4" {
		t.Fatalf("history %v, want [e2e4]", got)
	}
	// DELETE ($FF on the wire, $7F after the mask) is the other backspace.
	if err := u.Type("e7e5"); err != nil {
		t.Fatal(err)
	}
	if err := u.Key(0x7F); err != nil {
		t.Fatal(err)
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE? e7e ") {
		t.Errorf("after DELETE row 23 = %q, want e7e", strings.TrimSpace(got))
	}
	// Backspacing past the start of an empty line must not underflow.
	for range 6 {
		if err := u.Key(0x08); err != nil {
			t.Fatal(err)
		}
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE?  ") ||
		strings.Contains(got, "e7") {
		t.Errorf("after backspacing past the start row 23 = %q, want an empty line",
			strings.TrimSpace(got))
	}
	// ESC backs out of the modal LEVEL prompt (a prompt with no way out is
	// a trap), leaving the level alone.
	lvl := u.Peek(ui.UILEVEL)
	if err := u.Enter("l"); err != nil {
		t.Fatal(err)
	}
	if err := u.Key(0x1B); err != nil {
		t.Fatal(err)
	}
	if got := u.Peek(ui.UILEVEL); got != lvl {
		t.Errorf("ESC at the level prompt changed the level to %d, want %d", got, lvl)
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE?") {
		t.Errorf("after ESC row 23 = %q, want the move prompt back", strings.TrimSpace(got))
	}
}

// TestScreenHolesUntouched: the eight per-page "screen holes" ($x478-$x47F,
// $x4F8-$x4FF, ... $x7F8-$x7FF) belong to peripheral firmware — on a IIe with
// a Disk II or a mouse card in a slot, scribbling there breaks the card.
// asm/ui.s says uicls deliberately avoids them; this asserts that NOTHING in
// a whole run of UI activity writes one, which uicls alone does not prove
// (uiputs, uiprompt, uititle and the panel all index off row bases too).
func TestScreenHolesUntouched(t *testing.T) {
	u := bootShipping(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	// Poison every hole byte with a distinctive value.
	var holes []uint16
	for page := uint16(0x0400); page < 0x0800; page += 0x80 {
		for off := uint16(0x78); off < 0x80; off++ {
			holes = append(holes, page+off)
		}
	}
	for i, a := range holes {
		u.Poke(a, byte(0x11+i))
	}
	// Exercise as much of the painter as a short session can: moves, an
	// illegal move, garbage input, help, level prompt, takeback, new game.
	for _, line := range []string{"e2e4", "e7e5", "g1f3", "b8c6", "f1b5"} {
		if err := u.Enter(line); err != nil {
			t.Fatal(err)
		}
	}
	for _, line := range []string{"e2e9", "zzzz", "?", "t"} {
		if err := u.Enter(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Enter("l"); err != nil { // level prompt
		t.Fatal(err)
	}
	if err := u.Key('2'); err != nil {
		t.Fatal(err)
	}
	if err := u.Enter("n"); err != nil {
		t.Fatal(err)
	}
	for i, a := range holes {
		if got, want := u.Peek(a), byte(0x11+i); got != want {
			t.Errorf("screen hole $%04X was written: $%02X, want $%02X", a, got, want)
		}
	}
}

// TestMoveNumberOverNinetyNine_KnownDefect: the move panel formats the move
// number with ui.s's uidec2, documented as "A = 0..99". A real game can pass
// move 99 — the UI does not adjudicate RES_LONG until 250 PLIES, i.e. move
// 125 — so moves 100-125 render their number as PUNCTUATION: uidec2's tens
// digit is X, and X=10 becomes ':' (10 + '0'), X=11 ';', X=12 '<'.
//
// This is a CHARACTERIZATION test: it pins the current (wrong) output so the
// defect is executable rather than merely argued. Fixing it means a
// three-digit formatter in uimoves — the panel is 19 columns wide and the
// line is 12, so there is room — at which point this test must be inverted.
//
// Driven by poking the history arrays: playing 202 plies through the
// keyboard would take minutes and prove the same thing about uidec2.
func TestMoveNumberOverNinetyNine_KnownDefect(t *testing.T) {
	u := bootShipping(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	const plies = 202 // -> moves 1..101, panel shows the last 13
	for i := range plies {
		u.Poke(ui.UIHFROM+uint16(i), 0x14) // e2
		u.Poke(ui.UIHTO+uint16(i), 0x24)   // e3
		u.Poke(ui.UIHFLAG+uint16(i), 0)
	}
	u.Poke(ui.UIHCNT, plies)
	u.Poke(u.Defs["HALFMOVE"], 0)        // keep uireps from scanning the poked hashes
	if err := u.Enter("?"); err != nil { // any key: forces a repaint
		t.Fatal(err)
	}
	s := u.Screen()
	var panel []string
	for row := 3; row <= 15; row++ {
		panel = append(panel, strings.TrimRight(s.Text(row)[21:], " "))
	}
	t.Logf("move panel with %d plies played (moves 89-101):\n%s", plies, strings.Join(panel, "\n"))
	// Moves 89..99 are fine; 100 and 101 are the defect.
	want := []string{"99 e2e3 e2e3", ":0 e2e3 e2e3", ":1 e2e3 e2e3"}
	got := panel[len(panel)-3:]
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("panel line %d = %q, want %q (if this now reads \"100 ...\" the "+
				"uidec2 defect has been FIXED and this characterization test should be "+
				"replaced by a real assertion)", i, got[i], want[i])
		}
	}
	t.Logf("CONFIRMED DEFECT: move 100 renders as %q and move 101 as %q — "+
		"uidec2's tens digit is a raw counter, so 10 becomes ':' and 11 ';'",
		got[1][:2], got[2][:2])
}
