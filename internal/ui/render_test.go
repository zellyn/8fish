// This file exercises the 40-column text RENDERER (asm/ui.s) on its own,
// under the harness emulator. The rest of package ui_test drives the whole
// on-device UI (asm/m8.s) built on top of it.
//
// asm/uitest.s runs the REAL renderer from the address it will really run
// at: a stub at $4000 enables Language Card bank-1 RAM with the same $C08B
// double read engine.s uses, copies the UICODE segment to $E000, and jumps
// there. The test pokes a real position into BOARD/PIECESQ (exactly as
// chesstest does for the engine), runs the image, and reads text page 1
// back out of main RAM, decoding it with the Apple IIe character set.
//
// It also measures the renderer's cost EXACTLY, by difference: the image
// repaints the board UIREP extra times, so two runs with different UIREP
// values give the per-repaint cycle count with no timer resolution loss.
package ui_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/harness"
	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/ui"
)

const (
	uiOrg      = 0x4000
	uiRepAddr  = 0x030D // uitest.s UIREP  (16-bit): extra board repaints
	uiRep2Addr = 0x030F // uitest.s UIREP2 (16-bit): extra whole-screen repaints

	// Screen geometry, mirroring the equates at the top of asm/ui.s.
	brdRow0 = 2
	brdCol  = 3
)

// demoFEN is the Ruy Lopez (Closed) after 1.e4 e5 2.Nf3 Nc6 3.Bb5 a6
// 4.Ba4 Nf6 5.O-O Be7 — the game asm/uitest.s also lists in its move
// panel, so the board and the move list describe the same position.
const demoFEN = "r1bqk2r/1pppbppp/p1n2n2/4p3/B3P3/5N2/PPPP1PPP/RNBQ1RK1 w kq -"

// The screen decoding, the interleaved row bases and the Screen type all
// live in the ui package now, shared with the tests that drive the whole
// on-device UI (asm/m8.s).

// run builds and runs the PoC image with the given position and UIREP,
// returning the resulting screen and the total emulated cycle count.
func run(t *testing.T, fen string, rep, rep2 uint16) (*ui.Screen, uint64) {
	t.Helper()
	if err := asmbuild.BuildStandalone(root, "uitest"); err != nil {
		if errors.Is(err, asmbuild.ErrCA65NotInstalled) {
			t.Skip("SKIP: ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join(root, "asm", "uitest.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join(root, "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	pos, err := chesstest.ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := harness.New(harness.Config{
		Bin: bin, Org: uiOrg, Entry: uiOrg,
		CoutAddr: 0xBFF0, ExitAddr: 0xBFFF,
		InAddr: 0xBFF1, InStatusAddr: 0xBFF2, ClockAddr: 0xBFF4,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Poke the position the same way chesstest.NewMachine does: only the
	// 64 real squares, so the 0x88 board's off-board scratch is untouched.
	board := defs["BOARD"]
	for rank := uint16(0); rank < 8; rank++ {
		base := rank * 16
		copy(m.Mem.Main[board+base:board+base+8], pos.Board[base:base+8])
	}
	psq := defs["PIECESQ"]
	copy(m.Mem.Main[psq:psq+32], pos.PieceSq[:])
	m.Mem.Main[defs["SIDE"]] = pos.Side
	m.Mem.Main[defs["EPSQ"]] = pos.EpSq
	m.Mem.Main[defs["CASTLE"]] = pos.Castle
	m.Mem.Main[uiRepAddr] = byte(rep)
	m.Mem.Main[uiRepAddr+1] = byte(rep >> 8)
	m.Mem.Main[uiRep2Addr] = byte(rep2)
	m.Mem.Main[uiRep2Addr+1] = byte(rep2 >> 8)

	exited, code, err := m.Run(1 << 32)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !exited {
		t.Fatal("image never hit the exit trap")
	}
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	var s ui.Screen
	for row := range 24 {
		base := ui.RowBase(row)
		for col := range 40 {
			s.Raw[row][col] = m.Mem.Peek(base + uint16(col))
		}
	}
	return &s, m.Cycles
}

// TestRenderPosition is the proof of concept: the renderer, running from
// Language Card RAM at $E000, paints a real position into text page 1.
func TestRenderPosition(t *testing.T) {
	s, _ := run(t, demoFEN, 0, 0)

	t.Logf("rendered screen (%s):\n%s", demoFEN, func() string {
		var sb strings.Builder
		sb.WriteString("    +----------------------------------------+\n")
		for row := 0; row < 24; row++ {
			fmt.Fprintf(&sb, "%2d  |%s|  %s\n", row, s.Text(row), s.Video(row))
		}
		sb.WriteString("    +----------------------------------------+")
		return sb.String()
	}())

	// The 16 board characters of each rank: piece letter + blank per square,
	// uppercase = white, lowercase = black (the FEN convention).
	wantBoard := []string{
		"r   b q k     r ", // rank 8
		"  p p p b p p p ", // rank 7
		"p   n     n     ", // rank 6
		"        p       ", // rank 5
		"B       P       ", // rank 4
		"          N     ", // rank 3
		"P P P P   P P P ", // rank 2
		"R N B Q   R K   ", // rank 1
	}
	for i, want := range wantBoard {
		got := s.Text(brdRow0 + i)[brdCol : brdCol+16]
		if got != want {
			t.Errorf("board rank %d: got %q, want %q", 8-i, got, want)
		}
	}

	// Checkerboard: a square is DARK (inverse video) iff rank+file is even,
	// and BOTH characters of its cell carry the attribute.
	for r := 0; r < 8; r++ { // r = 0 is rank 8
		rank := 7 - r
		for f := 0; f < 8; f++ {
			wantDark := (rank+f)%2 == 0
			for half := 0; half < 2; half++ {
				_, inv := ui.Decode(s.Raw[brdRow0+r][brdCol+2*f+half])
				if inv != wantDark {
					t.Errorf("square rank%d file%d half%d: inverse=%v, want %v",
						rank+1, f, half, inv, wantDark)
				}
			}
		}
	}

	// Coordinates and the rest of the layout.
	for i, want := range []string{"8", "7", "6", "5", "4", "3", "2", "1"} {
		if got := string(s.Text(brdRow0 + i)[brdCol-2]); got != want {
			t.Errorf("rank label row %d: got %q, want %q", brdRow0+i, got, want)
		}
	}
	if got := s.Text(brdRow0 + 8)[brdCol : brdCol+16]; got != "a b c d e f g h " {
		t.Errorf("file labels: got %q", got)
	}
	for _, tc := range []struct {
		row  int
		want string
	}{
		{0, "8FISH 1.0"},
		{2, "MOVES"},
		{3, " 1 e2e4 e7e5"},
		{4, " 2 g1f3 b8c6"},
		{5, " 3 f1b5 a7a6"},
		{6, " 4 b5a4 g8f6"},
		{7, " 5 e1g1 f8e7"},
		{12, "WHITE TO MOVE"},
		{13, "CHECK"},
		{14, "D7  +0.34  b1c3"},
		{16, "BOOK: RUY LOPEZ, CLOSED"},
		{17, "ILLEGAL MOVE - TRY AGAIN"},
		{20, "N-NEW T-TAKEBACK R-RESIGN D-DRAW L-LEVEL"},
		{23, "YOUR MOVE?"},
	} {
		if !strings.Contains(s.Text(tc.row), tc.want) {
			t.Errorf("row %d = %q, want it to contain %q", tc.row, s.Text(tc.row), tc.want)
		}
	}
	// The whole title row must be inverse video (the status bar).
	if v := s.Video(0); v != strings.Repeat("#", 40) {
		t.Errorf("title row video = %q, want all inverse", v)
	}
}

// TestRenderCost measures the board repaint's exact cycle cost by
// difference, so no clock resolution is lost.
func TestRenderCost(t *testing.T) {
	const reps = 1000
	_, c0 := run(t, demoFEN, 0, 0)
	_, cBoard := run(t, demoFEN, reps, 0)
	_, cFull := run(t, demoFEN, 0, reps)
	if cBoard <= c0 || cFull <= c0 {
		t.Fatalf("cycles did not grow with the repeat counts: %d / %d / %d", c0, cBoard, cFull)
	}
	perBoard := float64(cBoard-c0) / reps
	perFull := float64(cFull-c0) / reps
	t.Logf("uiboard  (8x8 board only):  %7.1f cycles = %5.2f ms at 1.0205 MHz",
		perBoard, perBoard/1020.484)
	t.Logf("paint    (whole 40x24 screen): %7.1f cycles = %5.2f ms at 1.0205 MHz",
		perFull, perFull/1020.484)
	t.Logf("cold start (LC copy + one paint): %d cycles = %.2f ms",
		c0, float64(c0)/1020.484)
	if perBoard > 6000 {
		t.Errorf("board repaint costs %.0f cycles; budget is 6000", perBoard)
	}
	if perFull > 40000 {
		t.Errorf("full repaint costs %.0f cycles; budget is 40000", perFull)
	}
}
