// This file renders the SHIPPING DISK's screen through goapple2's video
// scanner and asserts what a monitor would show, at dot resolution.
//
// Every other gate in this package compares BYTES: the board against
// internal/tiles' model, the text window against strings read out of both
// banks. That leaves a gap docs/ui-design.md §13.5/§14.6 was explicit
// about — "goapple2 renders nothing", "no test here has ever rendered
// scanline 160" — and this file closes the part of it that can be closed
// from software. videoscan now models double hi-res, 80-column text and
// MIXED, so the three things that were prose are assertions:
//
//   - the graphics/text split falls at scanline 160,
//   - the four text rows are READABLE as pixels, not just as bytes,
//   - the double-resolution picture is displayed SEVEN DOTS LEFT.
//
// What this does NOT become is a hardware test. videoscan is a model of a
// IIe, cross-checked against OpenEmulator's (itself cross-checked against
// zellyn's physical IIe for the seven-dot shift). Two models agreeing is
// worth much more than one model with a comment, and it is still less than
// a machine.
package ui_test

import (
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/tiles"
	"github.com/zellyn/chess6502/internal/ui"
	"github.com/zellyn/goapple2/chargen"
	"github.com/zellyn/goapple2/videoscan"
)

// Board geometry on the shipping screen, from asm/dhgr.s. DHTOP=4 and
// 8 rows of DHROWS=19 scanlines each.
const (
	boardTop    = 4                              // first scanline of the board
	boardBottom = boardTop + 8*19 - 1            // 155
	boardLeftX  = dhOriginCol * 14               // 112: DHCOL0=8 byte columns in
	boardRightX = boardLeftX + 8*tiles.TileW - 1 // 447
)

// bootedFrame boots the shipping disk and renders one complete video field
// through videoscan, returning the frame and the machine.
func bootedFrame(t *testing.T) (*videoscan.Frame, *ui.DiskMachine) {
	t.Helper()
	m, err := ui.NewDiskMachine(dskPath(t), ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	ok, err := m.BootToPrompt(600_000_000)
	if err != nil {
		t.Fatalf("booting: %v", err)
	}
	if !ok {
		t.Fatalf("the disk never reached the keyboard poll (PC $%04X)", m.CPU.PC())
	}
	mode := videoscan.Mode{
		Text:       m.Mem.Text,
		Mixed:      m.Mem.Mixed,
		Page2:      m.Mem.Page2,
		Hires:      m.Mem.Hires,
		Col80:      m.Mem.Col80,
		DHires:     m.Mem.DHires(),
		AltCharset: m.Mem.AltCharset,
		Store80:    m.Mem.Store80,
	}
	// The screen this file is about only exists in this configuration; if
	// the image ever boots into something else, say so rather than
	// asserting pixels of the wrong mode.
	if mode.Text || !mode.Hires || !mode.Mixed || !mode.DHires {
		t.Fatalf("the disk booted into %+v, want graphics + hi-res + mixed + double-res", mode)
	}
	f, err := videoscan.RenderFrame(m.Mem, m.Mem, chargen.IIe(), mode)
	if err != nil {
		t.Fatalf("rendering the booted screen: %v", err)
	}
	return f, m
}

// TestDiskScannerMixedSplit finds the graphics/text boundary on the
// SHIPPING screen by looking at pixels, and checks it is where §14.1 says:
// graphics on scanlines 0-159, four rows of 80-column text on 160-191.
//
// It locates the split without being told where it is: scanline y belongs
// to the graphics half iff its dots match internal/tiles' decode of DHGR
// page 1 (displaced by the seven-dot shift), and to the text half iff they
// match the character generator's rendering of the window. The two halves
// are found independently and must meet exactly once.
func TestDiskScannerMixedSplit(t *testing.T) {
	f, m := bootedFrame(t)

	screen := make([]byte, 0, tiles.A2FCSize)
	screen = append(screen, m.Mem.Aux[0x2000:0x4000]...)
	screen = append(screen, m.Mem.Main[0x2000:0x4000]...)
	dhgr, err := tiles.Decode(screen)
	if err != nil {
		t.Fatal(err)
	}

	// The last scanline that is the DHGR page, and the first that is not.
	lastGraphics := -1
	for y := range videoscan.Lines {
		if !dhgrRowMatches(f, dhgr, y) {
			break
		}
		lastGraphics = y
	}
	if lastGraphics != 159 {
		t.Errorf("the DHGR page is displayed on scanlines 0-%d; want 0-159 "+
			"(MIXED puts the split at %d)", lastGraphics, videoscan.MixedSplit)
	}

	// And the rows below it are the text window, not more graphics.
	for y := videoscan.MixedSplit; y < videoscan.Lines; y++ {
		if dhgrRowMatches(f, dhgr, y) && !dhgrRowBlank(dhgr, y) {
			t.Errorf("scanline %d still shows the DHGR page; the text window "+
				"should start at %d", y, videoscan.MixedSplit)
		}
	}
	if !t.Failed() {
		t.Logf("MIXED split confirmed at scanline %d by pixels: 0-%d are DHGR "+
			"page 1, %d-191 are the 80-column text window",
			videoscan.MixedSplit, lastGraphics, videoscan.MixedSplit)
	}
}

// dhgrRowMatches reports whether scanline y of the rendered frame is the
// DHGR page's row y, displaced left by the seven-dot double-resolution
// shift.
func dhgrRowMatches(f *videoscan.Frame, s *tiles.Screen, y int) bool {
	for x := range tiles.ScreenW {
		if f.At(x-videoscan.DoubleShift, y) != s.At(x, y) {
			return false
		}
	}
	return true
}

func dhgrRowBlank(s *tiles.Screen, y int) bool {
	for x := range tiles.ScreenW {
		if s.At(x, y) {
			return false
		}
	}
	return true
}

// TestDiskScannerSevenDotShift measures the horizontal displacement of the
// SHIPPING board on the SHIPPING screen, by sliding internal/tiles' decode
// of the same memory across the rendered dots and finding the offsets that
// fit. Exactly one does, and it is -7.
//
// This is the assertion behind §14.7. The shift is not a constant this
// project chose: it is what a correct double-resolution scanner does,
// because the aux byte is shifted out half a cell early, and zellyn saw the
// same displacement on a physical IIe.
func TestDiskScannerSevenDotShift(t *testing.T) {
	f, m := bootedFrame(t)

	screen := make([]byte, 0, tiles.A2FCSize)
	screen = append(screen, m.Mem.Aux[0x2000:0x4000]...)
	screen = append(screen, m.Mem.Main[0x2000:0x4000]...)
	dhgr, err := tiles.Decode(screen)
	if err != nil {
		t.Fatal(err)
	}

	var fits []int
	for off := -14; off <= 14; off++ {
		ok := true
		for y := range videoscan.MixedSplit { // the graphics half only
			for x := range tiles.ScreenW {
				if x+off < videoscan.MinX || x+off > videoscan.MaxX {
					continue // outside the raster: nothing to compare
				}
				if f.At(x+off, y) != dhgr.At(x, y) {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
		}
		if ok {
			fits = append(fits, off)
		}
	}
	if len(fits) != 1 || fits[0] != -videoscan.DoubleShift {
		t.Fatalf("the board's memory fits the rendered dots at offsets %v; "+
			"want exactly one, %d — double-resolution video is displayed "+
			"SEVEN DOTS LEFT (docs/ui-design.md §14.7, observed on hardware)",
			fits, -videoscan.DoubleShift)
	}
	t.Logf("the shipping board is displayed at offset %d: %d dots LEFT of where "+
		"its bytes sit in a 40-column-indexed screen", fits[0], videoscan.DoubleShift)
}

// TestDiskScannerBoardExtent checks where the board actually lands on the
// screen, in dots: scanlines 4-155 with a blank border above and below, and
// a left edge at x=105 rather than the x=112 its bytes describe. §14.7's
// "105/119 margins instead of 112/112" is now a measurement (it comes out
// 105/120: the h-file's last dot column is background, see below).
func TestDiskScannerBoardExtent(t *testing.T) {
	f, _ := bootedFrame(t)

	for _, y := range []int{0, 1, 2, 3, 156, 157, 158, 159} {
		if n := f.LitRow(y); n != 0 {
			t.Errorf("scanline %d has %d lit dots; the board's border rows "+
				"(0-%d and %d-159) should be blank", y, n, boardTop-1, boardBottom+1)
		}
	}
	for y := boardTop; y <= boardBottom; y++ {
		if f.Blank(y) {
			t.Errorf("scanline %d is blank; the board covers %d-%d",
				y, boardTop, boardBottom)
		}
	}

	// The LEFT edge is exact: the board's leftmost byte column is DHCOL0=8,
	// its first dot is lit (the artwork's border column), and the shift
	// moves it from x=112 to x=105.
	//
	// The RIGHT edge is not asserted as boardLeftX+8*TileW-1: whether the
	// rightmost dot COLUMN of the h-file is lit depends on the artwork (a
	// dark square's background is $00 and the drawn border is on the left),
	// so a number there would be a fact about the picture rather than about
	// the video hardware. The bounding box as a whole is checked against
	// internal/tiles' decode of the same memory instead.
	wantLeft := boardLeftX - videoscan.DoubleShift // 105
	left, right := videoscan.MaxX+1, videoscan.MinX-1
	for y := boardTop; y <= boardBottom; y++ {
		f0, f1 := f.Span(y)
		if f0 < left {
			left = f0
		}
		if f1 > right {
			right = f1
		}
	}
	if left != wantLeft {
		t.Errorf("the board's leftmost lit dot is at x=%d, want %d "+
			"(byte column %d is x=%d, less the seven-dot shift)",
			left, wantLeft, dhOriginCol, boardLeftX)
	}
	if right > boardRightX-videoscan.DoubleShift {
		t.Errorf("the board's rightmost lit dot is at x=%d, past its last "+
			"byte column's last dot (%d)", right, boardRightX-videoscan.DoubleShift)
	}
	t.Logf("the board occupies scanlines %d-%d and x=%d..%d. Its BYTES describe "+
		"x=%d..%d; the seven-dot shift makes the left margin %d dots instead of "+
		"%d, and leaves %d on the right", boardTop, boardBottom, left, right,
		boardLeftX, boardRightX, left, boardLeftX, videoscan.DotsPerLine-1-right)
}

// TestDiskScannerWindowIsReadable reads the four-row 80-column text window
// back out of the DOTS and compares it with the same four rows read out of
// memory. This is the §14.6 caveat — "nothing here has driven the 14M shift
// register in 80-column TEXT mode" — turned into a gate: the aux/main
// column interleave, the character generator, ALTCHARSET and the inverse
// title bar all have to be right for a string to come back.
func TestDiskScannerWindowIsReadable(t *testing.T) {
	f, m := bootedFrame(t)
	cg := chargen.IIe()
	w := m.Window()

	for i := range ui.Win80Rows {
		row := ui.Win80Top + i
		want := w.Text(i)
		got := scanText80Row(f, cg, m.Mem.AltCharset, row)
		if got != want {
			t.Errorf("window row %d read back from PIXELS as\n  %q\nbut memory says\n  %q",
				row, got, want)
			continue
		}
		t.Logf("row %d, read out of the rendered dots: %q", row, strings.TrimRight(got, " "))
	}
}

// TestDiskScannerWindowVideoSense checks, cell by cell, the two things a
// byte comparison cannot see: WHERE each of the 320 window cells lands on
// the raster, and whether it is drawn in inverse or normal video.
//
// The glyph SHAPES come from the same character generator the renderer
// used, so this is not a test of the shapes; it is a test of the geometry —
// the aux/main interleave, the seven-dot shift and the cell pitch — and of
// the video sense, which is where the inverse title bar of §14.1 lives.
// A one-cell interleave slip or a wrong shift moves every glyph and fails
// here.
func TestDiskScannerWindowVideoSense(t *testing.T) {
	f, m := bootedFrame(t)
	cg := chargen.IIe()
	w := m.Window()
	alt := m.Mem.AltCharset

	inverse := map[int]int{}
	var invCols23 []int
	for i := range ui.Win80Rows {
		row := ui.Win80Top + i
		for col := range 80 {
			raw := w.Raw[i][col]
			want, ok := cg.Glyph(raw, alt, false)
			if !ok {
				t.Fatalf("row %d col %d holds $%02X, which is MouseText; the "+
					"UI is not supposed to write it", row, col, raw)
			}
			if got := cellDots(f, col, row); got != want {
				t.Fatalf("row %d col %d ($%02X): the rendered dots at x=%d are\n"+
					"  %v\nbut its glyph is\n  %v",
					row, col, raw, cellX(col), got, want)
			}
			if chargen.Class(raw, alt) == chargen.Inverse {
				inverse[row]++
				if row == 23 {
					invCols23 = append(invCols23, col)
				}
			}
		}
	}

	// Row 20 is the inverse title bar for its first 40 columns. Assert the
	// SHAPE of that, not an exact count, so re-wording a field does not
	// fail the test.
	if inverse[20] < 40 {
		t.Errorf("text row 20 has only %d inverse cells; §14.1's title bar is "+
			"inverse video and covers at least its first 40 columns", inverse[20])
	}
	// Row 23 is the prompt, in normal video, with ONE inverse cell: the
	// CURSOR. It is a solid block on the screen, and it sits immediately
	// after the prompt's last character — which is how a player finds where
	// they are typing.
	if len(invCols23) != 1 {
		t.Errorf("text row 23 has %d inverse cells at %v; want exactly one, the cursor",
			len(invCols23), invCols23)
	} else {
		col := invCols23[0]
		if raw := w.Raw[3][col]; raw != 0x20 {
			t.Errorf("row 23's inverse cell at column %d is $%02X, want $20 "+
				"(an inverse SPACE — a solid cursor block)", col, raw)
		}
		// It follows the prompt text, in the LEFT half of the window (the
		// right half is a help line): at the first free column, allowing
		// the one space the prompt puts after "YOUR MOVE?".
		prompt := strings.TrimRight(w.Text(3)[:40], " ")
		if col <= len(prompt)-1 || col > len(prompt)+1 {
			t.Errorf("the cursor is at column %d, but the prompt %q ends at "+
				"column %d — the cursor should be where the player is typing",
				col, prompt, len(prompt)-1)
		}
		// And it is genuinely solid on the raster: 56 lit dots, not a glyph.
		if lit := litCell(f, col, 23); lit != 7*8 {
			t.Errorf("the cursor cell at column %d has %d lit dots of 56; an "+
				"inverse space is a solid block", col, lit)
		}
	}
	t.Logf("all 320 window cells match their glyphs at the shifted 80-column "+
		"pitch; inverse cells per row: 20=%d 21=%d 22=%d 23=%d (row 23's is the "+
		"cursor, at column %v). ROM %s",
		inverse[20], inverse[21], inverse[22], inverse[23], invCols23, m.ROMName)
}

// litCell counts the lit dots in one 80-column text cell.
func litCell(f *videoscan.Frame, col, row int) int {
	n := 0
	for _, line := range cellDots(f, col, row) {
		for i := range 7 {
			if line&(1<<i) != 0 {
				n++
			}
		}
	}
	return n
}

// cellX is the leftmost dot of 80-column text column col. The pair sharing
// one memory address sits seven dots left of the 40-column cell it shares
// that address with: the EVEN column (from aux) at 14*(col/2)-7 and the ODD
// one (from main) at 14*(col/2).
func cellX(col int) int {
	return (col/2)*videoscan.DotsPerCell - videoscan.DoubleShift +
		(col%2)*videoscan.DoubleShift
}

// cellDots reads the 7x8 dots of 80-column cell (col, row) out of a frame,
// as eight bytes with bit 0 the leftmost dot — the same shape
// chargen.Glyph returns.
func cellDots(f *videoscan.Frame, col, row int) [8]byte {
	x0, y0 := cellX(col), row*8
	var lines [8]byte
	for dy := range 8 {
		var bits byte
		for i := range 7 {
			if f.At(x0+i, y0+dy) {
				bits |= 1 << i
			}
		}
		lines[dy] = bits
	}
	return lines
}

// scanText80Row reads one 80-column text row back out of a rendered frame
// by matching each cell's dots against the character generator.
func scanText80Row(f *videoscan.Frame, cg *chargen.CharGen, alt bool, row int) string {
	var sb strings.Builder
	for col := range 80 {
		sb.WriteByte(matchGlyph(cg, alt, cellDots(f, col, row)))
	}
	return sb.String()
}

// matchGlyph names the character whose glyph these eight scan lines are, in
// either video sense, or '?' if none is. Window80.Text reports ASCII
// regardless of inverse/normal, so this does too.
//
// A glyph does not identify a screen byte uniquely — the IIe character
// generator repeats, so $97 and $D7 both draw a normal-video W — and the
// aliases decode to DIFFERENT ASCII ($17 and $57). Preferring the candidate
// whose ASCII is PRINTABLE picks the one a program would actually have
// written, which is what ui.Decode and Window80.Text report.
func matchGlyph(cg *chargen.CharGen, alt bool, lines [8]byte) byte {
	for b := range 256 {
		rows, ok := cg.Glyph(byte(b), alt, false)
		if !ok || rows != lines {
			continue
		}
		a, ok := chargen.ASCII(byte(b), alt)
		if ok && a >= 0x20 && a < 0x7F {
			return a
		}
	}
	return '?'
}
