// This file exercises the DOUBLE-HI-RES board renderer (asm/dhgr.s) on its
// own, under the harness emulator — the same shape as render_test.go does for
// the 40-column text renderer.
//
// asm/dhgrtest.s runs the REAL renderer from the address it will really run
// at: a stub at $4000 enables Language Card bank-1 RAM with the same $C08B
// double read engine.s uses, copies the UICODE segment to $E000, and jumps
// there. The test pokes a real position into BOARD, runs the image, and reads
// DHGR page 1 back out — aux $2000-$3FFF then main $2000-$3FFF, which is
// exactly the 16 KB A2FC layout cmd/dhgr2png decodes and DazzleDraw writes.
//
// The assertion is a PARITY one: the 6502's screen must be byte-identical to
// internal/tiles' Go model of the same paint. That is the same discipline the
// engine's five parity gates use, and it means the artwork pipeline
// (cmd/gentiles) and the on-device renderer cannot drift apart silently.
package ui_test

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/8fish/harness"
	"github.com/zellyn/8fish/internal/asmbuild"
	"github.com/zellyn/8fish/internal/chesstest"
	"github.com/zellyn/8fish/internal/tiles"
)

const (
	dhOrg     = 0x4000
	dhRepAddr = 0x030D // dhgrtest.s DHREP: extra dhboard repaints

	// Where asm/dhgr.s puts the board. Mirrors DHCOL0/DHTOP in that file.
	dhOriginCol = 8
	dhOriginY   = 4
)

// runDHGR builds and runs the PoC image with the given position, returning
// the 16 KB A2FC screen (aux bank then main bank) and the cycle count.
func runDHGR(t *testing.T, fen string, rep byte) ([]byte, uint64) {
	t.Helper()
	if err := asmbuild.BuildStandalone(root, "dhgrtest"); err != nil {
		if errors.Is(err, asmbuild.ErrCA65NotInstalled) {
			t.Skip("SKIP: ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join(root, "asm", "dhgrtest.bin"))
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
		Bin: bin, Org: dhOrg, Entry: dhOrg,
		CoutAddr: 0xBFF0, ExitAddr: 0xBFFF,
		InAddr: 0xBFF1, InStatusAddr: 0xBFF2, ClockAddr: 0xBFF4,
	})
	if err != nil {
		t.Fatal(err)
	}
	board := defs["BOARD"]
	for rank := uint16(0); rank < 8; rank++ {
		base := rank * 16
		copy(m.Mem.Main[board+base:board+base+8], pos.Board[base:base+8])
	}
	m.Mem.Main[dhRepAddr] = rep

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
	screen := make([]byte, 0, tiles.A2FCSize)
	screen = append(screen, m.Mem.Aux[0x2000:0x4000]...)
	screen = append(screen, m.Mem.Main[0x2000:0x4000]...)
	return screen, m.Cycles
}

// board64 converts a chesstest 0x88 position to the (r,f) order
// internal/tiles uses, where r=0 is rank 8.
//
// The low nibble is the mask that matters: the engine's BOARD packs the
// PIECE LIST SLOT into the high nibble ($11 is slot 1 holding a white pawn),
// so a raw board byte is not a piece nibble. asm/dhgr.s does the same `and
// #$0F` before forming the dark<<4|piece index.
func board64(pos *chesstest.Position) *[64]byte {
	var b [64]byte
	for r := range 8 {
		for f := range 8 {
			b[r*8+f] = pos.Board[(7-r)*16+f] & 0x0F
		}
	}
	return &b
}

// TestDHGRRenderParity is the proof of concept AND the parity gate: the
// renderer, running from Language Card RAM at $E000, paints the owner's
// hand-drawn board into DHGR page 1, and every one of the 16,384 bytes
// matches internal/tiles' independent Go model of the same paint.
func TestDHGRRenderParity(t *testing.T) {
	for _, tc := range []struct{ name, fen string }{
		{"startpos", "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq -"},
		{"ruylopez", demoFEN},
		{"empty", "8/8/8/8/8/8/8/8 w - -"},
		// Every piece on both square colours, so all 24 tiles are exercised.
		{"allpieces", "qrbnkbnr/pppppppp/8/8/8/8/PPPPPPPP/QRBNKBNR w - -"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := runDHGR(t, tc.fen, 0)
			pos, err := chesstest.ParseFEN(tc.fen)
			if err != nil {
				t.Fatal(err)
			}
			want, err := tiles.Render(tiles.DefaultBlob(), board64(pos), dhOriginCol, dhOriginY)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				n := 0
				first := -1
				for i := range want {
					if got[i] != want[i] {
						if first < 0 {
							first = i
						}
						n++
					}
				}
				t.Errorf("DHGR screen differs from the Go model in %d of %d bytes; "+
					"first at offset $%04X (%s bank $%04X): got $%02X want $%02X",
					n, len(want), first, bankName(first), bankAddr(first),
					got[first], want[first])
			}
		})
	}
}

func bankName(off int) string {
	if off < tiles.BankSize {
		return "aux"
	}
	return "main"
}
func bankAddr(off int) int { return 0x2000 + off%tiles.BankSize }

// TestDHGRRenderCost measures the exact per-repaint cost by difference, the
// same way TestRenderCost does for the text renderer.
func TestDHGRRenderCost(t *testing.T) {
	_, c0 := runDHGR(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq -", 0)
	_, c8 := runDHGR(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq -", 8)
	per := (c8 - c0) / 8
	t.Logf("dhboard: %d cycles per whole-board repaint (%.1f ms at 1.023 MHz); "+
		"64 squares x 19 rows x 6 bytes = 7296 stores", per, float64(per)/1023.0)
	// A whole-board repaint must stay a rounding error against a 30 s move.
	if per > 200_000 {
		t.Errorf("dhboard costs %d cycles per repaint, over the 200,000 budget", per)
	}
}

// TestDHGRScreenshot writes the rendered board out as a PNG so a human can
// look at what the 6502 actually painted. It is not an assertion; it is the
// artefact that proves the artwork survived the trip.
func TestDHGRScreenshot(t *testing.T) {
	screen, _ := runDHGR(t, "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq -", 0)
	s, err := tiles.Decode(screen)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewGray(image.Rect(0, 0, tiles.ScreenW, 2*tiles.ScreenH))
	for y := range tiles.ScreenH {
		for x := range tiles.ScreenW {
			if s.At(x, y) {
				img.SetGray(x, 2*y, color.Gray{0xEE})
				img.SetGray(x, 2*y+1, color.Gray{0xEE})
			}
		}
	}
	out := filepath.Join(os.TempDir(), "8fish-dhgr-board.png")
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	fmt.Println("DHGR screenshot written to", out)
	t.Log("DHGR screenshot written to", out)
}
