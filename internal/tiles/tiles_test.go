package tiles

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// artPath is the hand-drawn source of truth, relative to this package.
var artPath = filepath.Join("..", "..", "assets", "chess-dazzledraw-save.bin")

func loadArt(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(artPath)
	if err != nil {
		t.Fatalf("reading artwork: %v", err)
	}
	if len(data) != A2FCSize {
		t.Fatalf("artwork is %d bytes, want %d (A2FC: 8 KB aux + 8 KB main)", len(data), A2FCSize)
	}
	return data
}

func loadScreen(t *testing.T) *Screen {
	t.Helper()
	s, err := Decode(loadArt(t))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestBoundingBox: the drawing occupies exactly x=0..367, y=0..171.
// Everything else on the 560x192 screen is dark.
func TestBoundingBox(t *testing.T) {
	s := loadScreen(t)
	minX, minY := ScreenW, ScreenH
	maxX, maxY := -1, -1
	for y := range ScreenH {
		for x := range ScreenW {
			if !s.At(x, y) {
				continue
			}
			minX = min(minX, x)
			maxX = max(maxX, x)
			minY = min(minY, y)
			maxY = max(maxY, y)
		}
	}
	if minX != BoardMinX || maxX != BoardMaxX || minY != BoardMinY || maxY != BoardMaxY {
		t.Errorf("lit bounding box = x %d..%d, y %d..%d; want x %d..%d, y %d..%d",
			minX, maxX, minY, maxY, BoardMinX, BoardMaxX, BoardMinY, BoardMaxY)
	}
	if h := maxY - minY + 1; h != 172 {
		t.Errorf("board height %d scanlines, want 172", h)
	}
}

// TestBorder: rows 0 and 171 are fully lit across the board's width, and
// columns 0..3 and 364..367 are fully lit down its height. That border is
// what pins the grid origin, so it is worth asserting directly.
func TestBorder(t *testing.T) {
	s := loadScreen(t)
	for _, y := range []int{BorderTop, BorderBot} {
		for x := BoardMinX; x <= BoardMaxX; x++ {
			if !s.At(x, y) {
				t.Fatalf("border row %d: dot (%d,%d) is dark", y, x, y)
			}
		}
	}
	for _, x := range []int{0, 1, 2, 3, 364, 365, 366, 367} {
		for y := BoardMinY; y <= BoardMaxY; y++ {
			if !s.At(x, y) {
				t.Fatalf("border column %d: dot (%d,%d) is dark", x, x, y)
			}
		}
	}
	// The left border is BorderLeft+1 columns wide and the grid origin sits
	// clear of it.
	if SrcOriginX <= BorderLeft {
		t.Errorf("grid origin x=%d overlaps the border (0..%d)", SrcOriginX, BorderLeft)
	}
}

// TestGridFitsInsideBorder: 8 squares of 44x21 from origin (8,2) land
// strictly inside the drawn border.
func TestGridFitsInsideBorder(t *testing.T) {
	if lastX := SrcOriginX + 8*SrcSquareW - 1; lastX >= 364 {
		t.Errorf("grid right edge x=%d runs into the right border (starts at 364)", lastX)
	}
	if lastY := SrcOriginY + 8*SrcSquareH - 1; lastY >= BorderBot {
		t.Errorf("grid bottom edge y=%d runs into the bottom border (%d)", lastY, BorderBot)
	}
}

// TestSquareShades: row 0 reads L D L D L D L D, and each square's
// background really is the dither (light) or blank (dark) that Light says
// it is. Sampled in the guaranteed-background left margin, dx 0..7.
func TestSquareShades(t *testing.T) {
	s := loadScreen(t)
	want := "LDLDLDLD"
	for f := range 8 {
		got := byte('D')
		if Light(0, f) {
			got = 'L'
		}
		if got != want[f] {
			t.Errorf("row 0 file %d: Light says %c, want %c", f, got, want[f])
		}
	}
	for r := range 8 {
		for f := range 8 {
			dark := !Light(r, f)
			for dx := range ContentMinDX {
				for dy := range SrcSquareH {
					got := srcDot(s, r, f, dy, dx)
					want := !dark && dx%2 == 0
					if got != want {
						t.Fatalf("square (r=%d,f=%d) is %s but dot (dx=%d,dy=%d) is %v",
							r, f, shade(!dark), dx, dy, got)
					}
				}
			}
		}
	}
}

// TestDitherPhaseLock: on a light square the lit background dots are the
// EVEN ABSOLUTE x ones, which is what makes the background a constant
// aux=$55 / main=$2A byte pair and lets a 42-dot pitch preserve phase.
func TestDitherPhaseLock(t *testing.T) {
	s := loadScreen(t)
	for r := range 8 {
		for f := range 8 {
			if !Light(r, f) {
				continue
			}
			for dx := range ContentMinDX {
				x := SrcOriginX + SrcSquareW*f + dx
				if lit := srcDot(s, r, f, 10, dx); lit != (x%2 == 0) {
					t.Fatalf("light square (r=%d,f=%d): absolute x=%d lit=%v, want lit == (x even)",
						r, f, x, lit)
				}
			}
		}
	}
	// And the byte-level consequence: every background byte column of a
	// light square is exactly $55 (aux) / $2A (main).
	for r := range 8 {
		for f := range 8 {
			dark := !Light(r, f)
			for _, c := range []int{0, TileCols - 1} {
				if got, want := srcByte(s, r, f, 5, c), Background(c, dark); got != want {
					t.Errorf("square (r=%d,f=%d) byte column %d = $%02X, want $%02X",
						r, f, c, got, want)
				}
			}
		}
	}
	if SrcSquareW%2 != 0 || SrcOriginX%2 != 0 || TileW%14 != 0 {
		t.Errorf("phase preservation needs even SrcOriginX/SrcSquareW and TileW %% 14 == 0; got %d,%d,%d",
			SrcOriginX, SrcSquareW, TileW)
	}
}

// TestOuterColumnsPureBackground: dx 0..7 and 35..43 deviate from the
// background in ZERO of the 64 squares. This is what lets the tile drop
// the two rightmost dot columns losslessly and store only byte columns
// 1..4.
func TestOuterColumnsPureBackground(t *testing.T) {
	s := loadScreen(t)
	if n := BoardDeviations(s, 0, SrcSquareH-1, 0, ContentMinDX-1); n != 0 {
		t.Errorf("left margin dx 0..%d has %d non-background dots, want 0", ContentMinDX-1, n)
	}
	if n := BoardDeviations(s, 0, SrcSquareH-1, ContentMaxDX+1, SrcSquareW-1); n != 0 {
		t.Errorf("right margin dx %d..%d has %d non-background dots, want 0",
			ContentMaxDX+1, SrcSquareW-1, n)
	}
	// The trimmed columns (dx 42,43) are inside that right margin, so the
	// 44 -> 42 narrowing is lossless.
	if n := BoardDeviations(s, 0, SrcSquareH-1, TileW, SrcSquareW-1); n != 0 {
		t.Errorf("trimmed dot columns %d..%d hold %d non-background dots, want 0",
			TileW, SrcSquareW-1, n)
	}
}

// TestTrimCosts: dropping the top two source rows costs exactly 28
// non-background dots board-wide. Three costs 80, and the bottom row
// alone costs 115 — the numbers behind choosing "top 2".
func TestTrimCosts(t *testing.T) {
	s := loadScreen(t)
	for _, tc := range []struct {
		name   string
		y0, y1 int
		want   int
	}{
		{"top 2 rows (the trim we take)", 0, 1, 28},
		{"top 3 rows", 0, 2, 80},
		{"bottom 1 row", SrcSquareH - 1, SrcSquareH - 1, 115},
	} {
		if got := BoardDeviations(s, tc.y0, tc.y1, 0, SrcSquareW-1); got != tc.want {
			t.Errorf("%s: %d non-background dots, want %d", tc.name, got, tc.want)
		}
	}
	if SrcTrimTop != 2 {
		t.Errorf("SrcTrimTop = %d; the measured knee is 2", SrcTrimTop)
	}
	if TileH != SrcSquareH-SrcTrimTop {
		t.Errorf("TileH = %d, want SrcSquareH-SrcTrimTop = %d", TileH, SrcSquareH-SrcTrimTop)
	}
}

// TestNothingElseIsLost: the ONLY content the tile format discards is
// those 28 top-trim dots. Everything outside the retained window
// (dy 2..20 x the stored byte columns' dots, dx 7..34) is background.
func TestNothingElseIsLost(t *testing.T) {
	s := loadScreen(t)
	total := BoardDeviations(s, 0, SrcSquareH-1, 0, SrcSquareW-1)
	// The stored byte columns are ordered aux0..2 then main0..2, so their
	// dot coverage is a UNION; assert it is the contiguous span 7..34.
	covered := map[int]bool{}
	for c := FirstCol; c <= LastCol; c++ {
		lo, hi, _ := colPixels(c)
		for x := lo; x <= hi; x++ {
			covered[x] = true
		}
	}
	loCol, hiCol := 7, 34
	if len(covered) != hiCol-loCol+1 {
		t.Fatalf("stored columns cover %d dots, want %d", len(covered), hiCol-loCol+1)
	}
	for x := loCol; x <= hiCol; x++ {
		if !covered[x] {
			t.Fatalf("stored byte columns %d..%d leave dot dx=%d uncovered", FirstCol, LastCol, x)
		}
	}
	if loCol > ContentMinDX || hiCol < ContentMaxDX {
		t.Fatalf("stored span %d..%d does not contain the content span %d..%d",
			loCol, hiCol, ContentMinDX, ContentMaxDX)
	}
	kept := BoardDeviations(s, SrcTrimTop, SrcSquareH-1, loCol, hiCol)
	if lost := total - kept; lost != 28 {
		t.Errorf("tile format discards %d non-background dots (total %d, kept %d); want exactly the 28 top-trim dots",
			lost, total, kept)
	}
}

// TestArtMapShades: every transcribed source square really has the shade
// its _L/_D tag claims — the check that catches a typo'd rank or file —
// and the 24 entries cover all 24 tile slots exactly once.
func TestArtMapShades(t *testing.T) {
	if err := checkArtMap(); err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, e := range ArtMap() {
		if Light(e.R, e.F) == e.Dark {
			t.Errorf("%q at (r=%d,f=%d): shade tag disagrees with (r+f)%%2", e.Name, e.R, e.F)
		}
		idx := e.Index()
		if idx < 0 || idx >= NumTiles {
			t.Fatalf("%q maps to tile index %d", e.Name, idx)
		}
		if prev, dup := seen[idx]; dup {
			t.Errorf("tile %d claimed by both %q and %q", idx, prev, e.Name)
		}
		seen[idx] = e.Name
	}
	if len(seen) != NumTiles {
		t.Errorf("artMap covers %d of %d tiles", len(seen), NumTiles)
	}
	// Rows 0/1 are black, 6/7 white; the four extras live in rows 2 and 5.
	for _, e := range ArtMap() {
		black := e.Nib >= BPawn
		switch e.R {
		case 0, 1, 2:
			if !black {
				t.Errorf("%q is white but drawn in row %d (the black half)", e.Name, e.R)
			}
		case 5, 6, 7:
			if black {
				t.Errorf("%q is black but drawn in row %d (the white half)", e.Name, e.R)
			}
		default:
			t.Errorf("%q drawn in row %d, which holds no pieces", e.Name, e.R)
		}
	}
}

// TestEmptySquaresAreSynthetic: every square the artwork leaves empty is
// EXACTLY the background the runtime synthesises — so shipping no
// empty-square tiles loses nothing.
func TestEmptySquaresAreSynthetic(t *testing.T) {
	s := loadScreen(t)
	empties := EmptySquares()
	if len(empties) != 28 {
		t.Fatalf("expected 28 empty squares (rows 2..5 minus the 4 extras), got %d", len(empties))
	}
	for _, sq := range empties {
		r, f := sq[0], sq[1]
		if n := Deviations(s, r, f, 0, SrcSquareH-1, 0, SrcSquareW-1); n != 0 {
			t.Errorf("empty square (r=%d,f=%d) has %d dots differing from the synthetic background",
				r, f, n)
		}
	}
	// ...and the four extras are NOT empty, which is why they exist.
	for _, sq := range extraSquares {
		if n := Deviations(s, sq[0], sq[1], 0, SrcSquareH-1, 0, SrcSquareW-1); n == 0 {
			t.Errorf("extra square (r=%d,f=%d) is blank; the transcription expects a piece there",
				sq[0], sq[1])
		}
	}
}

// TestBuild: the blob is 1824 bytes, byte columns 0 and 5 of every tile
// row are background, and the committed blob matches a fresh build.
func TestBuild(t *testing.T) {
	art := loadArt(t)
	blob, err := Build(art)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) != BlobSize {
		t.Fatalf("blob is %d bytes, want %d", len(blob), BlobSize)
	}
	if BlobSize != NumTiles*TileStride || TileStride != TileBytes*TileH || TileBytes != 4 {
		t.Errorf("size arithmetic drifted: %d tiles x %d B/tile (%d B x %d rows) != %d",
			NumTiles, TileStride, TileBytes, TileH, BlobSize)
	}
	if got, want := len(DefaultBlob()), BlobSize; got != want {
		t.Fatalf("embedded tileblob.bin is %d bytes, want %d: re-run go run ./cmd/gentiles", got, want)
	}
	if !bytes.Equal(DefaultBlob(), blob) {
		t.Error("embedded tileblob.bin differs from a fresh build: re-run go run ./cmd/gentiles")
	}

	// Byte columns 0 and 5 are background in ALL 24 tiles: the fact the
	// 4-byte-per-row format rests on.
	s := loadScreen(t)
	for _, e := range ArtMap() {
		for ty := range TileH {
			for _, c := range []int{0, TileCols - 1} {
				if got, want := srcByte(s, e.R, e.F, ty, c), Background(c, e.Dark); got != want {
					t.Errorf("tile %q row %d byte column %d = $%02X, want background $%02X",
						e.Name, ty, c, got, want)
				}
			}
			// Every stored byte matches the source square it came from.
			row := Row(blob, e.Index(), ty)
			for c := FirstCol; c <= LastCol; c++ {
				if got, want := row[c-FirstCol], srcByte(s, e.R, e.F, ty, c); got != want {
					t.Errorf("tile %q row %d column %d = $%02X, want $%02X", e.Name, ty, c, got, want)
				}
			}
		}
	}
}

// TestBuildRejectsBrokenArtwork proves the generator fails loudly rather
// than shipping a blob that silently drops content: light a dot in the
// margin that the format assumes is background.
func TestBuildRejectsBrokenArtwork(t *testing.T) {
	// Square (0,0) is light, so an ODD dx is background-dark. dx=41 sits in
	// the right margin the tile format assumes holds nothing.
	broken := bytes.Clone(loadArt(t))
	SetDot(broken, SrcOriginX+41, SrcOriginY+10)
	if _, err := Build(broken); err == nil {
		t.Fatal("Build accepted artwork with content outside the stored byte columns")
	}
	// Same for the top-left square's left margin (dx=1, also odd).
	broken = bytes.Clone(loadArt(t))
	SetDot(broken, SrcOriginX+1, SrcOriginY+10)
	if _, err := Build(broken); err == nil {
		t.Fatal("Build accepted artwork with content in the left margin")
	}
	// And an empty square that stops being empty.
	broken = bytes.Clone(loadArt(t))
	SetDot(broken, SrcOriginX+SrcSquareW*0+20, SrcOriginY+SrcSquareH*3+10)
	if _, err := Build(broken); err == nil {
		t.Fatal("Build accepted artwork with a non-blank 'empty' square")
	}
}

// TestTileIndexMirrorsPIECECH: the dark<<4|nibble index space maps to
// tiles exactly as PIECECH maps it to characters — 12 light-square tiles
// then 12 dark-square ones, in nibble order, with empty and the two
// impossible nibbles reserved for the synthesised background.
func TestTileIndexMirrorsPIECECH(t *testing.T) {
	for nib := byte(0); nib < 16; nib++ {
		for _, dark := range []bool{false, true} {
			idx := TileIndex(nib, dark)
			piece := nib >= WPawn && nib <= WKing || nib >= BPawn && nib <= BKing
			if piece == (idx < 0) {
				t.Errorf("nibble $%02X dark=%v: TileIndex = %d, piece = %v", nib, dark, idx, piece)
			}
			if !piece {
				continue
			}
			if dark != (idx >= 12) {
				t.Errorf("nibble $%02X dark=%v maps to tile %d (wrong half)", nib, dark, idx)
			}
			if idx*TileStride+TileStride > BlobSize {
				t.Errorf("tile %d runs past the blob", idx)
			}
		}
	}
	// Light and dark variants of the same piece are 12 tiles apart, and no
	// two pieces collide.
	for nib := byte(1); nib <= BKing; nib++ {
		if TileIndex(nib, false) < 0 {
			continue
		}
		if d, l := TileIndex(nib, true), TileIndex(nib, false); d-l != 12 {
			t.Errorf("nibble $%02X: dark tile %d - light tile %d = %d, want 12", nib, d, l, d-l)
		}
	}
}

// TestPawnRowsAreConsistent: the artwork draws all eight pawns per side,
// so the four same-shade pawns of a row must slice to IDENTICAL tiles.
// An independent check that the drawing is regular and that the
// transcription picked a representative square correctly.
func TestPawnRowsAreConsistent(t *testing.T) {
	s := loadScreen(t)
	blob, err := Build(loadArt(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range []struct {
		row int
		nib byte
	}{{1, BPawn}, {6, WPawn}} {
		for f := range 8 {
			dark := !Light(pr.row, f)
			idx := TileIndex(pr.nib, dark)
			for ty := range TileH {
				row := Row(blob, idx, ty)
				for c := FirstCol; c <= LastCol; c++ {
					if got, want := srcByte(s, pr.row, f, ty, c), row[c-FirstCol]; got != want {
						t.Fatalf("pawn at (r=%d,f=%d) row %d column %d = $%02X, but its tile has $%02X",
							pr.row, f, ty, c, got, want)
					}
				}
			}
		}
	}
}
