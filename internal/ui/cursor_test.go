package ui_test

// cursor_test.go gates ARROW-KEY MOVE ENTRY (docs/ui-design.md §18) on the
// SHIPPING DISK: every test here boots asm/8fish.dsk from the Disk II boot
// ROM and types on the modelled IIe keyboard — the same path a player's
// keystrokes take — and reads the highlight back out of DHGR page 1 the way
// TestDiskBoardParity does: as bytes, against an independent Go model.
//
// The model (cursorModel below) mirrors asm/dhgr.s exactly:
//
//	the cursor        a two-scanline/two-dot WHITE box (dhcursor): lit on
//	                  BOTH shades (the light squares are only ~50% dither),
//	                  so it no longer keys off the displayed shade
//	the latched FROM  the square repainted in its OPPOSITE shade (dhsq1
//	                  with DHFLIP — the artwork carries every piece on both
//	                  shades, so the flip is the other tile over the other
//	                  background)
//
// Everything a selection SUBMITS goes through the same uidispatch path as a
// typed line, so the move-legality half of these tests is the same engine
// machinery every other gate already trusts; what is new here is the input
// mode and the pixels.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/chesstest"
	"github.com/zellyn/8fish/internal/tiles"
	"github.com/zellyn/8fish/internal/ui"
)

// IIe key codes (asm/m8.s uiread).
const (
	keyLeft  = 0x08
	keyRight = 0x15
	keyUp    = 0x0B
	keyDown  = 0x0A
	keySpace = 0x20
	keyEsc   = 0x1B
)

// 0x88 squares used below.
const (
	sqE2 = 0x14
	sqE3 = 0x24
	sqE4 = 0x34
	sqA3 = 0x20
)

const curKeyBudget = 600_000_000

// bootCursorDisk boots the shipping disk to its first keyboard poll.
func bootCursorDisk(t *testing.T) *ui.DiskMachine {
	t.Helper()
	dsk := dskPath(t)
	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	if ok, err := m.BootToPrompt(curKeyBudget); err != nil || !ok {
		t.Fatalf("boot: ok=%v err=%v (PC $%04X)", ok, err, m.CPU.PC())
	}
	return m
}

func cursorDefs(t *testing.T) chesstest.Defs {
	t.Helper()
	defs, err := chesstest.ParseDefs(filepath.Join(root, "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	return defs
}

// dhgrScreen reads DHGR page 1 in A2FC order (aux bank, then main).
func dhgrScreen(m *ui.DiskMachine) []byte {
	s := make([]byte, 0, tiles.A2FCSize)
	s = append(s, m.Mem.Aux[0x2000:0x4000]...)
	s = append(s, m.Mem.Main[0x2000:0x4000]...)
	return s
}

// machineBoard64 reads the position off the machine's own BOARD, as the
// piece nibbles internal/tiles renders (r=0 is rank 8).
func machineBoard64(m *ui.DiskMachine, defs chesstest.Defs) *[64]byte {
	var b [64]byte
	board := defs["BOARD"]
	for r := range 8 {
		for f := range 8 {
			b[r*8+f] = m.Mem.Main[board+uint16((7-r)*16+f)] & 0x0F
		}
	}
	return &b
}

// dhLineOff is the DHGR byte offset of scanline y's column 0 within either
// bank (the same interleave tiles.Decode and asm/dhgr.s dhinit compute).
func dhLineOff(y int) int { return (y&7)*1024 + ((y>>3)&7)*128 + (y>>6)*40 }

// squareRF converts an 0x88 square to tiles' (r, f), r=0 = rank 8.
func squareRF(sq int) (r, f int) { return 7 - sq>>4, sq & 7 }

// flipSquareModel repaints square sq of the model in its OPPOSITE shade —
// the Go mirror of dhsq1 with DHFLIP set (the latched-FROM highlight).
func flipSquareModel(model []byte, b64 *[64]byte, sq int) {
	r, f := squareRF(sq)
	shownDark := tiles.Light(r, f) // flipped: light square shows dark
	idx := tiles.TileIndex(b64[r*8+f], shownDark)
	blob := tiles.DefaultBlob()
	aux, mainb := model[:tiles.BankSize], model[tiles.BankSize:]
	for ty := range tiles.TileH {
		line := dhLineOff(dhOriginY + r*tiles.TileH + ty)
		for c := range tiles.TileCols {
			b := tiles.Background(c, shownDark)
			if idx >= 0 && c >= tiles.FirstCol && c <= tiles.LastCol {
				b = tiles.Row(blob, idx, ty)[c-tiles.FirstCol]
			}
			bank, i := aux, c
			if c >= tiles.TileCols/2 {
				bank, i = mainb, c-tiles.TileCols/2
			}
			bank[line+dhOriginCol+f*(tiles.TileCols/2)+i] = b
		}
	}
}

// boxSquareModel draws the cursor's WHITE box on square sq — the Go mirror
// of asm/dhgr.s dhcursor. The box is now lit on BOTH shades (the light
// squares are only ~50% dither), so it no longer keys off the displayed
// shade; the values below are shade-independent.
func boxSquareModel(model []byte, sq int) {
	full, left, right := byte(0x7F), byte(0x03), byte(0x60) // lit box, both shades
	r, f := squareRF(sq)
	aux, mainb := model[:tiles.BankSize], model[tiles.BankSize:]
	col := dhOriginCol + f*(tiles.TileCols/2)
	for ty := range tiles.TileH {
		line := dhLineOff(dhOriginY + r*tiles.TileH + ty)
		if ty < 2 || ty >= tiles.TileH-2 {
			for i := range 3 {
				aux[line+col+i] = full
				mainb[line+col+i] = full
			}
		} else {
			aux[line+col] = left
			mainb[line+col+2] = right
		}
	}
}

// cursorModel renders the Go model of the DHGR screen for b64 with the
// cursor on cur and the latch on latch (either may be -1 for none).
func cursorModel(t *testing.T, b64 *[64]byte, cur, latch int) []byte {
	t.Helper()
	model, err := tiles.Render(tiles.DefaultBlob(), b64, dhOriginCol, dhOriginY)
	if err != nil {
		t.Fatal(err)
	}
	if latch >= 0 {
		flipSquareModel(model, b64, latch)
	}
	if cur >= 0 {
		boxSquareModel(model, cur) // white box, shade-independent
	}
	return model
}

// compareDHGR requires the machine's DHGR page to be byte-identical to the
// model.
func compareDHGR(t *testing.T, m *ui.DiskMachine, want []byte, what string) {
	t.Helper()
	got := dhgrScreen(m)
	if bytes.Equal(got, want) {
		return
	}
	n, first := 0, -1
	for i := range want {
		if got[i] != want[i] {
			if first < 0 {
				first = i
			}
			n++
		}
	}
	bank, addr := "aux", 0x2000+first
	if first >= tiles.BankSize {
		bank, addr = "main", 0x2000+first-tiles.BankSize
	}
	t.Fatalf("%s: the DHGR screen differs from the model in %d of %d bytes; first at "+
		"%s $%04X: got $%02X, want $%02X", what, n, len(want), bank, addr,
		got[first], want[first])
}

// TestDiskCursorAppearsAndMoves: the first arrow press pops the cursor — a
// visible contrast box on e2 (no game underway) — the arrows move it one
// square at a time, the board edge clamps it, and the mode announces its
// keys on the message row of both screens.
func TestDiskCursorAppearsAndMoves(t *testing.T) {
	m := bootCursorDisk(t)
	defs := cursorDefs(t)
	b64 := machineBoard64(m, defs)

	// Before any arrow: the plain board (this is TestDiskBoardParity's
	// claim, re-checked here so "the box appeared" is a delta, not a hope).
	compareDHGR(t, m, cursorModel(t, b64, -1, -1), "before the first arrow")

	// First arrow press: the cursor pops on e2 (no game is underway).
	if err := m.Key(keyUp, curKeyBudget); err != nil {
		t.Fatalf("up arrow: %v", err)
	}
	if got := m.Mem.Main[ui.CURACT]; got != 1 {
		t.Fatalf("CURACT = %d after the first arrow, want 1", got)
	}
	if got := m.Mem.Main[ui.CURSQ]; got != sqE2 {
		t.Errorf("CURSQ = $%02X after the first arrow, want e2 ($%02X)", got, sqE2)
	}
	compareDHGR(t, m, cursorModel(t, b64, sqE2, -1), "cursor popped on e2")

	// The mode announces itself — on the 40-column message row AND in the
	// mixed-mode window (the only text a player sees while the board is up).
	if got := m.Screen().Text(17); !strings.Contains(got, "ARROWS MOVE") {
		t.Errorf("message row = %q, want the cursor announcement", strings.TrimSpace(got))
	}
	if got := m.Window().Text(2); !strings.Contains(got, "ARROWS MOVE") {
		t.Errorf("window row 2 = %q, want the cursor announcement", strings.TrimSpace(got))
	}

	// Arrows move it: up to e3, then e4.
	for _, step := range []struct {
		key  byte
		want byte
		name string
	}{
		{keyUp, sqE3, "e3"},
		{keyUp, sqE4, "e4"},
	} {
		if err := m.Key(step.key, curKeyBudget); err != nil {
			t.Fatalf("arrow to %s: %v", step.name, err)
		}
		if got := m.Mem.Main[ui.CURSQ]; got != step.want {
			t.Fatalf("CURSQ = $%02X, want %s ($%02X)", got, step.name, step.want)
		}
		compareDHGR(t, m, cursorModel(t, b64, int(step.want), -1), "cursor on "+step.name)
	}

	// The board edge clamps: from e4, four lefts reach a4 and four more do
	// nothing; the a-file keeps its box the whole time.
	for range 8 {
		if err := m.Key(keyLeft, curKeyBudget); err != nil {
			t.Fatalf("left arrow: %v", err)
		}
	}
	if got := m.Mem.Main[ui.CURSQ]; got != 0x30 {
		t.Errorf("CURSQ = $%02X after eight lefts from e4, want a4 ($30): the edge "+
			"must clamp, not wrap", got)
	}
	compareDHGR(t, m, cursorModel(t, b64, 0x30, -1), "cursor clamped at a4")
	t.Log("the cursor pops on e2, arrows move it, and the board edge clamps it")
}

// TestDiskCursorPlaysMove is the feature's whole point: SPACE latches the
// FROM square (highlighted in its opposite shade), SPACE on the destination
// plays the move through the SAME validation path a typed move takes, and
// the board both changes and matches the played position. Two-player mode,
// so the assertion is about the move machinery, not a search.
func TestDiskCursorPlaysMove(t *testing.T) {
	m := bootCursorDisk(t)
	defs := cursorDefs(t)
	b64 := machineBoard64(m, defs)

	if err := m.Enter("s", curKeyBudget); err != nil { // referee mode
		t.Fatalf("s: %v", err)
	}
	if err := m.Key(keyDown, curKeyBudget); err != nil { // pop: e2
		t.Fatalf("pop: %v", err)
	}
	if err := m.Key(keySpace, curKeyBudget); err != nil { // latch e2
		t.Fatalf("latch: %v", err)
	}
	if got := m.Mem.Main[ui.CURFROM]; got != sqE2 {
		t.Fatalf("CURFROM = $%02X after SPACE, want e2 ($%02X)", got, sqE2)
	}
	// Mid-selection, cursor still on the latch: the square shows its
	// OPPOSITE shade under the box.
	compareDHGR(t, m, cursorModel(t, b64, sqE2, sqE2), "from latched, cursor on it")

	// Move to the destination: the latch highlight STAYS on e2 while the
	// box walks to e4 — this is the mid-selection state a player reads.
	for range 2 {
		if err := m.Key(keyUp, curKeyBudget); err != nil {
			t.Fatalf("up: %v", err)
		}
	}
	compareDHGR(t, m, cursorModel(t, b64, sqE4, sqE2), "from latched on e2, cursor on e4")

	// SPACE on the destination: e2e4 is played.
	if err := m.Key(keySpace, curKeyBudget); err != nil {
		t.Fatalf("select to: %v", err)
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 1 {
		t.Fatalf("UIHCNT = %d after SPACE-SPACE, want 1 (the move must be PLAYED)\n%v",
			got, m.Screen())
	}
	after := machineBoard64(m, defs)
	if after[6*8+4] != 0 || after[4*8+4] != tiles.WPawn {
		t.Fatalf("the board did not play e2e4: e2 nibble %d, e4 nibble %d",
			after[6*8+4], after[4*8+4])
	}
	// The cursor retired with the move: the screen is the PLAIN new
	// position, byte-identical to the model — the same parity claim
	// TestDiskBoardParity makes at boot, now for a cursor-entered move.
	if got := m.Mem.Main[ui.CURACT]; got != 0 {
		t.Errorf("CURACT = %d after the move played, want 0 (retired)", got)
	}
	compareDHGR(t, m, cursorModel(t, after, -1, -1), "after e2e4 played by cursor")

	// ...and the move panel wrote it down, like any typed move.
	var line string
	s := m.Screen()
	for row := 2; row < 11 && line == ""; row++ {
		if txt := strings.TrimSpace(s.Text(row)[22:]); strings.HasPrefix(txt, "1 ") {
			line = txt
		}
	}
	if !strings.Contains(line, "e2e4") {
		t.Errorf("the move panel does not show the cursor-entered move: %q", line)
	}

	// The next pop starts on the LAST-MOVED destination (e4), not e2.
	if err := m.Key(keyRight, curKeyBudget); err != nil {
		t.Fatalf("re-pop: %v", err)
	}
	if got := m.Mem.Main[ui.CURSQ]; got != sqE4 {
		t.Errorf("after the move, the cursor pops on $%02X, want the last destination "+
			"e4 ($%02X)", got, sqE4)
	}
	t.Log("SPACE-SPACE played e2e4 through uidispatch; the panel and board agree")
}

// TestDiskCursorIllegalKeepsCursor: an illegal from/to is rejected with the
// EXISTING m_illegal message and the cursor survives for another try, latch
// cleared.
func TestDiskCursorIllegalKeepsCursor(t *testing.T) {
	m := bootCursorDisk(t)
	defs := cursorDefs(t)
	b64 := machineBoard64(m, defs)

	if err := m.Enter("s", curKeyBudget); err != nil {
		t.Fatalf("s: %v", err)
	}
	// Pop at e2, walk to a1, latch the rook, try a1-a3 (blocked by the a2
	// pawn: not even pseudo-legal, so uifind rejects it).
	if err := m.Key(keyUp, curKeyBudget); err != nil {
		t.Fatalf("pop: %v", err)
	}
	for _, k := range []byte{keyLeft, keyLeft, keyLeft, keyLeft, keyDown} {
		if err := m.Key(k, curKeyBudget); err != nil {
			t.Fatalf("navigate: %v", err)
		}
	}
	if got := m.Mem.Main[ui.CURSQ]; got != 0x00 {
		t.Fatalf("CURSQ = $%02X, want a1 ($00)", got)
	}
	if err := m.Key(keySpace, curKeyBudget); err != nil {
		t.Fatalf("latch a1: %v", err)
	}
	for range 2 {
		if err := m.Key(keyUp, curKeyBudget); err != nil {
			t.Fatalf("up: %v", err)
		}
	}
	if err := m.Key(keySpace, curKeyBudget); err != nil {
		t.Fatalf("submit a1a3: %v", err)
	}

	if got := m.Screen().Text(17); !strings.Contains(got, "ILLEGAL MOVE") {
		t.Fatalf("message row = %q, want the existing ILLEGAL MOVE message\n%v",
			strings.TrimSpace(got), m.Screen())
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 0 {
		t.Fatalf("UIHCNT = %d: the illegal move must not be played", got)
	}
	if got := m.Mem.Main[ui.CURACT]; got != 1 {
		t.Fatalf("CURACT = %d after the rejection, want 1: the cursor must survive "+
			"for another try", got)
	}
	if got := m.Mem.Main[ui.CURFROM]; got != 0xFF {
		t.Errorf("CURFROM = $%02X after the rejection, want none ($FF)", got)
	}
	// ...and the surviving cursor is VISIBLE, on a3 where the rejection
	// left it, over an otherwise-plain board.
	compareDHGR(t, m, cursorModel(t, b64, sqA3, -1), "cursor survives the rejection")
	t.Log("a1a3 rejected with ILLEGAL MOVE; the cursor is still up on a3")
}

// TestDiskCursorEscapeCancels: ESC cancels the whole interaction — latch
// and all — nothing is played, every highlight is erased, and typed entry
// still works immediately afterwards. A second ESC (no cursor) swaps
// screens, exactly as it always did.
func TestDiskCursorEscapeCancels(t *testing.T) {
	m := bootCursorDisk(t)
	defs := cursorDefs(t)
	b64 := machineBoard64(m, defs)

	if err := m.Enter("s", curKeyBudget); err != nil {
		t.Fatalf("s: %v", err)
	}
	// Pop, latch e2, move off it — a mid-selection state — then ESC.
	for _, k := range []byte{keyUp, keySpace, keyUp, keyUp} {
		if err := m.Key(k, curKeyBudget); err != nil {
			t.Fatalf("navigate: %v", err)
		}
	}
	if err := m.Key(keyEsc, curKeyBudget); err != nil {
		t.Fatalf("ESC: %v", err)
	}
	if got := m.Mem.Main[ui.CURACT]; got != 0 {
		t.Fatalf("CURACT = %d after ESC, want 0", got)
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 0 {
		t.Fatalf("UIHCNT = %d after ESC, want 0: nothing may be played", got)
	}
	// Every highlight is gone: the board is byte-identical to the plain
	// model again, and the announcement came down with the mode.
	compareDHGR(t, m, cursorModel(t, b64, -1, -1), "after ESC cancelled the cursor")
	if got := m.Screen().Text(17); strings.Contains(got, "ARROWS MOVE") {
		t.Errorf("message row still announces the cursor after ESC: %q",
			strings.TrimSpace(got))
	}
	// ESC with no cursor still swaps screens (the behaviour it always had).
	if err := m.Key(keyEsc, curKeyBudget); err != nil {
		t.Fatalf("ESC swap: %v", err)
	}
	if !m.Mem.Text {
		t.Error("a second ESC (no cursor up) did not swap to the text screen")
	}
	if err := m.Key(keyEsc, curKeyBudget); err != nil {
		t.Fatalf("ESC back: %v", err)
	}
	if m.Mem.Text || !m.Mem.DHires() {
		t.Error("ESC did not swap back to the board")
	}
	// And typed entry is untouched: the move plays.
	if err := m.Enter("e2e4", curKeyBudget); err != nil {
		t.Fatalf("typed e2e4 after the cancel: %v", err)
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 1 {
		t.Fatalf("typed entry broken after a cursor cancel: UIHCNT = %d, want 1\n%v",
			got, m.Screen())
	}
	t.Log("ESC cancelled with nothing played; ESC still swaps; typing still plays")
}

// TestDiskCursorTypedFallback: a letter key while the cursor is up falls
// back to typed entry — the cursor drops, the character lands in the line
// editor, and the typed move plays exactly as if the cursor had never been
// up.
func TestDiskCursorTypedFallback(t *testing.T) {
	m := bootCursorDisk(t)
	defs := cursorDefs(t)
	b64 := machineBoard64(m, defs)

	if err := m.Enter("s", curKeyBudget); err != nil {
		t.Fatalf("s: %v", err)
	}
	for _, k := range []byte{keyUp, keySpace} { // pop and latch, then type
		if err := m.Key(k, curKeyBudget); err != nil {
			t.Fatalf("navigate: %v", err)
		}
	}
	if err := m.Key('g', curKeyBudget); err != nil {
		t.Fatalf("g: %v", err)
	}
	if got := m.Mem.Main[ui.CURACT]; got != 0 {
		t.Fatalf("CURACT = %d after a letter key, want 0: typing must take the "+
			"keyboard back", got)
	}
	compareDHGR(t, m, cursorModel(t, b64, -1, -1), "highlights erased by the letter key")
	// The board is up, so the prompt is the mixed-mode window's bottom row
	// (40-column row 23 holds only that row's ODD columns while in mixed
	// mode — see uiprompt).
	if got := m.Window().Text(3); !strings.Contains(got, "YOUR MOVE? g") {
		t.Errorf("window prompt row = %q, want the letter echoed", strings.TrimSpace(got))
	}
	if err := m.Enter("1f3", curKeyBudget); err != nil { // ...completes g1f3
		t.Fatalf("1f3: %v", err)
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 1 {
		t.Fatalf("the typed move after the fallback did not play: UIHCNT = %d\n%v",
			got, m.Screen())
	}
	t.Log("a letter key dropped the cursor and the typed move played")
}

// TestDiskCursorPromotion: a promotion picked with the cursor routes
// through the EXISTING uiaskpromo prompt — the cursor synthesizes a 4-char
// move, uidispatch notices the missing piece and asks, exactly as a typed
// 4-char promotion does.
func TestDiskCursorPromotion(t *testing.T) {
	m := bootCursorDisk(t)
	defs := cursorDefs(t)

	if err := m.Enter("s", curKeyBudget); err != nil {
		t.Fatalf("s: %v", err)
	}
	// March the a-pawn to a7 in referee mode (all typed — the fixture, not
	// the feature): 1.a4 b5 2.axb5 a6 3.bxa6 c6 4.a7 d6, leaving a7xb8 as
	// a promotion capture for the cursor to pick.
	for _, mv := range []string{"a2a4", "b7b5", "a4b5", "a7a6", "b5a6", "c7c6", "a6a7", "d7d6"} {
		if err := m.Enter(mv, curKeyBudget); err != nil {
			t.Fatalf("%s: %v", mv, err)
		}
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 8 {
		t.Fatalf("fixture: UIHCNT = %d after 8 plies, want 8\n%v", got, m.Screen())
	}

	// Pop (on d6, the last destination), walk to a7, latch, walk to b8,
	// select: uiaskpromo must ask.
	if err := m.Key(keyUp, curKeyBudget); err != nil {
		t.Fatalf("pop: %v", err)
	}
	if got := m.Mem.Main[ui.CURSQ]; got != 0x53 {
		t.Fatalf("cursor popped on $%02X, want d6 ($53)", got)
	}
	for _, k := range []byte{keyLeft, keyLeft, keyLeft, keyUp, keySpace, keyUp, keyRight, keySpace} {
		if err := m.Key(k, curKeyBudget); err != nil {
			t.Fatalf("navigate: %v", err)
		}
	}
	if got := m.Screen().Text(17); !strings.Contains(got, "PROMOTE TO") {
		t.Fatalf("message row = %q, want uiaskpromo's PROMOTE TO prompt\n%v",
			strings.TrimSpace(got), m.Screen())
	}
	if err := m.Key('q', curKeyBudget); err != nil {
		t.Fatalf("q: %v", err)
	}
	after := machineBoard64(m, defs)
	if got := after[0*8+1]; got != tiles.WQueen {
		t.Fatalf("b8 nibble = %d after a7xb8=Q, want a white queen (%d)\n%v",
			got, tiles.WQueen, m.Screen())
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 9 {
		t.Fatalf("UIHCNT = %d after the promotion, want 9", got)
	}
	compareDHGR(t, m, cursorModel(t, after, -1, -1), "after the cursor promotion")
	t.Log("the cursor promotion asked PROMOTE TO and delivered a queen on b8")
}

// TestDiskCursorTextScreen: the 40-column text board (one ESC away) carries
// the cursor too — the cell flips to its opposite video shade, the latch
// adds a '*' in the blank half-cell, and a cancel restores the plain cell.
func TestDiskCursorTextScreen(t *testing.T) {
	m := bootCursorDisk(t)

	if err := m.Key(keyEsc, curKeyBudget); err != nil { // to the text screen
		t.Fatalf("ESC: %v", err)
	}
	if !m.Mem.Text {
		t.Fatal("ESC did not reach the 40-column text screen")
	}
	// e2's cell is row 8, columns 11-12: a white pawn on a LIGHT square,
	// normal video 'P' + space.
	s := m.Screen()
	if got := [2]byte{s.Raw[8][11], s.Raw[8][12]}; got != [2]byte{0xD0, 0xA0} {
		t.Fatalf("e2 cell reads % 02X, want D0 A0 (normal 'P ')", got)
	}
	if err := m.Key(keyUp, curKeyBudget); err != nil { // pop on e2
		t.Fatalf("pop: %v", err)
	}
	s = m.Screen()
	// The cursor flips the cell to its opposite shade: inverse 'P' + inverse
	// space.
	if got := [2]byte{s.Raw[8][11], s.Raw[8][12]}; got != [2]byte{0x10, 0x20} {
		t.Fatalf("e2 cell reads % 02X under the cursor, want 10 20 (inverse 'P ')", got)
	}
	if got := s.Text(17); !strings.Contains(got, "ARROWS MOVE") {
		t.Errorf("message row = %q, want the announcement on the text screen too",
			strings.TrimSpace(got))
	}
	if err := m.Key(keySpace, curKeyBudget); err != nil { // latch e2
		t.Fatalf("latch: %v", err)
	}
	s = m.Screen()
	// The latch marks the blank half-cell with '*' (inverse, on the flipped
	// cell).
	if got := [2]byte{s.Raw[8][11], s.Raw[8][12]}; got != [2]byte{0x10, 0x2A} {
		t.Fatalf("latched e2 cell reads % 02X, want 10 2A (inverse 'P*')", got)
	}
	if err := m.Key(keyEsc, curKeyBudget); err != nil { // cancel (NOT a swap)
		t.Fatalf("ESC cancel: %v", err)
	}
	if !m.Mem.Text {
		t.Fatal("ESC with the cursor up must cancel it, not swap screens")
	}
	s = m.Screen()
	if got := [2]byte{s.Raw[8][11], s.Raw[8][12]}; got != [2]byte{0xD0, 0xA0} {
		t.Fatalf("e2 cell reads % 02X after the cancel, want D0 A0 (plain again)", got)
	}
	t.Log("the 40-column board flips the cell, stars the latch, and restores on ESC")
}

// TestDiskCursorScreenshots writes the two deliverable screenshots: the
// cursor visible on a square, and mid-selection (from latched, cursor on
// the destination). Not an assertion — the parity gates above are the
// assertions — this is the artefact a human looks at.
func TestDiskCursorScreenshots(t *testing.T) {
	m := bootCursorDisk(t)

	shoot := func(name string) string {
		t.Helper()
		s, err := tiles.Decode(dhgrScreen(m))
		if err != nil {
			t.Fatal(err)
		}
		const mixedH = 160 // MIXED: graphics on scanlines 0-159
		img := image.NewGray(image.Rect(0, 0, tiles.ScreenW, 2*mixedH))
		for y := range mixedH {
			for x := range tiles.ScreenW {
				if s.At(x, y) {
					img.SetGray(x, 2*y, color.Gray{0xEE})
					img.SetGray(x, 2*y+1, color.Gray{0xEE})
				}
			}
		}
		out := filepath.Join(os.TempDir(), name)
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		t.Log("screenshot written to", out)
		return out
	}

	if err := m.Enter("s", curKeyBudget); err != nil {
		t.Fatalf("s: %v", err)
	}
	if err := m.Key(keyUp, curKeyBudget); err != nil { // pop on e2
		t.Fatalf("pop: %v", err)
	}
	shoot("8fish-cursor.png")
	for _, k := range []byte{keySpace, keyUp, keyUp} { // latch e2, walk to e4
		if err := m.Key(k, curKeyBudget); err != nil {
			t.Fatalf("navigate: %v", err)
		}
	}
	shoot("8fish-cursor-latched.png")
}
