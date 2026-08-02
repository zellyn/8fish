package tiles

import (
	"image"
	"image/color"
	"image/png"
	"os"
)

// WritePNG renders a decoded screen at 1x2 (square-ish pixels), matching
// cmd/dhgr2png's output so the two are directly comparable. It is the
// ONLY renderer in this package: the round-trip test and cmd/gentiles
// -png both go through it, so a screenshot is never a second opinion
// about what the bytes mean.
func WritePNG(path string, s *Screen) error {
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
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// BlobPNG writes what the sliced tiles ACTUALLY look like: it composes a
// whole DHGR screen from the blob for `board`, decodes those bytes back
// with the same loop cmd/dhgr2png uses, and renders the result. What you
// see is post-trim — the clipped rows are simply gone — so you can judge
// a redraw without booting anything.
func BlobPNG(path string, blob []byte, board *[64]byte, originCol, originY int) error {
	raw, err := Render(blob, board, originCol, originY)
	if err != nil {
		return err
	}
	s, err := Decode(raw)
	if err != nil {
		return err
	}
	return WritePNG(path, s)
}
