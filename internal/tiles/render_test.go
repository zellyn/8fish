package tiles

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// renderPNG is where the round-trip test drops its human-inspectable
// screenshot. Under /tmp on purpose, at a fixed guessable path: PNGs are
// never committed, and you want to be able to just open it.
const renderPNG = "/tmp/8fish-tiles-roundtrip.png"

// TestRoundTrip rebuilds a whole DHGR screen from the blob for the
// standard start position, decodes those bytes back with the SAME decode
// loop cmd/dhgr2png uses, and checks every dot against the artwork the
// tile was sliced from. It then writes a PNG so a human can look at it.
//
// This is the end-to-end proof that the packing, the 42-dot pitch and the
// synthesised background reproduce the drawing: if the dither phase or a
// plane order were wrong, the comparison fails here rather than on an
// Apple IIe.
func TestRoundTrip(t *testing.T) {
	src := loadScreen(t)
	blob, err := Build(loadArt(t))
	if err != nil {
		t.Fatal(err)
	}
	board := StartPosition()

	const originCol, originY = 1, 8 // inset a little so the PNG is readable
	raw, err := Render(blob, &board, originCol, originY)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}

	// Where each start-position piece was DRAWN in the artwork.
	srcOf := map[int]artEntry{}
	for _, e := range ArtMap() {
		srcOf[e.Index()] = e
	}

	bad := 0
	for r := range 8 {
		for f := range 8 {
			dark := !Light(r, f)
			idx := TileIndex(board[r*8+f], dark)
			for ty := range TileH {
				for tx := range TileW {
					got := out.At(originCol*14+f*TileW+tx, originY+r*TileH+ty)
					var want bool
					if idx < 0 {
						want = bgDot(tx, dark) // empty square: synthesised
					} else {
						e := srcOf[idx]
						want = srcDot(src, e.R, e.F, ty+SrcTrimTop, tx)
					}
					if got != want {
						if bad++; bad <= 10 {
							t.Errorf("square (r=%d,f=%d) dot (tx=%d,ty=%d): got %v want %v",
								r, f, tx, ty, got, want)
						}
					}
				}
			}
		}
	}
	if bad > 10 {
		t.Errorf("... and %d more mismatched dots", bad-10)
	}

	// Nothing outside the rendered board may be lit: Render must not spill
	// into neighbouring byte columns or scanlines.
	for y := range ScreenH {
		for x := range ScreenW {
			inBoard := x >= originCol*14 && x < originCol*14+8*TileW &&
				y >= originY && y < originY+8*TileH
			if !inBoard && out.At(x, y) {
				t.Fatalf("Render lit a dot outside the board at (%d,%d)", x, y)
			}
		}
	}

	if err := writePNG(renderPNG, out); err != nil {
		t.Fatal(err)
	}
	t.Logf("round-trip screenshot: %s", renderPNG)
}

// writePNG renders a decoded screen at 1x2 (square-ish pixels), matching
// cmd/dhgr2png's output so the two are directly comparable.
func writePNG(path string, s *Screen) error {
	img := image.NewGray(image.Rect(0, 0, ScreenW, 2*ScreenH))
	on := color.Gray{0xEE}
	for y := range ScreenH {
		for x := range ScreenW {
			if s.At(x, y) {
				img.SetGray(x, 2*y, on)
				img.SetGray(x, 2*y+1, on)
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
