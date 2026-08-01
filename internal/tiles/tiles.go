// Package tiles is the on-device board artwork: 24 per-square DHGR piece
// bitmaps sliced out of the owner's hand-drawn DazzleDraw board
// (assets/chess-dazzledraw-save.bin) by cmd/gentiles, laid out EXACTLY as
// they will sit resident so the asm renderer is a pure read-side addition
// (the same discipline internal/book uses for the opening book).
//
// GEOMETRY (all measured from the artwork; internal/tiles' tests assert
// every number here against the actual pixels, so a re-drawn board that
// breaks an assumption fails the build instead of rendering garbage):
//
//   - The drawn board's squares are 44x21 on a grid whose origin is (8,2).
//     Square (r,f) — r=0 is the TOP row (rank 8), f=0 is the a-file —
//     covers x = 8+44f .. +43, y = 2+21r .. +20. A square is light iff
//     (r+f) is even.
//
//   - The light-square dither is globally phase-locked to "even absolute x
//     is lit". In DHGR bytes that is the constant pair aux=$55, main=$2A
//     (an aux byte covers x=14c..14c+6 so even x is an even bit; the main
//     byte covers 14c+7.. so even x is an ODD bit). A dark square's
//     background is $00. Both are synthesised at runtime, which is why the
//     blob holds no empty-square tiles.
//
//   - Content is confined to dx 8..34 of a square: dx 0..7 and 35..43 are
//     pure background in all 64 squares. So the destination pitch can be
//     TileW=42 (= 6 DHGR bytes = 3 aux + 3 main) instead of 44 by dropping
//     the RIGHT two columns. 42 is a multiple of 14, so every file lands on
//     a byte boundary AND keeps the dither phase — no shifting, ever.
//
//   - Dropping the top TWO source rows of every square costs exactly 28
//     non-background pixels board-wide (three rows costs 80; dropping from
//     the bottom costs 115 for a single row). Two is the knee, so a tile
//     keeps source dy 2..20 → TileH=19 rows.
//
//   - Within a tile row's six byte columns [aux0 aux1 aux2 main0 main1
//     main2], columns 0 and 5 are pure background in all 24 piece tiles.
//     Only columns 1..4 = [aux1, aux2, main0, main1] are stored: 4 bytes x
//     19 rows = 76 bytes per tile, 24 tiles = 1824 bytes.
package tiles

import (
	"errors"
	"fmt"
)

// DHGR screen geometry (A2FC layout: 8 KB aux bank then 8 KB main bank,
// as DazzleDraw writes and as cmd/dhgr2png decodes).
const (
	ScreenW  = 560
	ScreenH  = 192
	BankSize = 8192
	A2FCSize = 2 * BankSize
)

// Source geometry of the hand-drawn board.
const (
	SrcOriginX = 8  // x of square (r,0)'s first column
	SrcOriginY = 2  // y of square (0,f)'s first row
	SrcSquareW = 44 // drawn square width in dots
	SrcSquareH = 21 // drawn square height in dots
	SrcTrimTop = 2  // source rows dy 0..1 dropped from every tile

	// Every drawn glyph lives inside dx ContentMinDX..ContentMaxDX; the
	// dot columns outside it are pure background in all 64 squares.
	ContentMinDX = 8
	ContentMaxDX = 34

	// Lit bounding box of the whole drawing, and the border it comes from.
	BoardMinX  = 0
	BoardMaxX  = 367
	BoardMinY  = 0
	BoardMaxY  = 171
	BorderLeft = 3   // columns 0..3 are fully lit
	BorderTop  = 0   // row 0 is fully lit
	BorderBot  = 171 // row 171 is fully lit
)

// Destination (tile) geometry.
const (
	TileW      = 42                     // dots per tile row = 6 DHGR bytes
	TileH      = 19                     // rows per tile (source dy 2..20)
	TileCols   = TileW / 7              // 6 byte columns: aux0..2 then main0..2
	FirstCol   = 1                      // stored byte columns: 1..4
	LastCol    = 4                      //   (0 and 5 are pure background)
	TileBytes  = LastCol - FirstCol + 1 // 4 bytes stored per tile row
	TileStride = TileBytes * TileH      // 76 bytes per tile
	NumTiles   = 24                     // 6 kinds x 2 colours x 2 square shades
	BlobSize   = NumTiles * TileStride  // 1824
	SquareCols = TileCols               // byte columns a rendered square occupies
	BGAux      = 0x55                   // light-square dither, aux plane
	BGMain     = 0x2A                   // light-square dither, main plane
)

// Piece nibbles: the low four bits of a board byte, colour<<3|type
// (asm/defs.inc). These are the values PIECECH — and TILEIDX — are
// indexed by, as dark<<4 | (piece & $0F).
const (
	WPawn   = 1
	WKnight = 2
	WBishop = 3
	WRook   = 4
	WQueen  = 5
	WKing   = 6
	BPawn   = 9
	BKnight = 10
	BBishop = 11
	BRook   = 12
	BQueen  = 13
	BKing   = 14
)

// TileIndex maps a piece nibble and the shade of the square it stands on
// to a blob tile index, or -1 for empty/impossible nibbles (those render
// as synthesised background). The ordering is deliberately the PIECECH
// ordering — light pieces $01..$06,$09..$0E then the same on dark — so
// the generated TILEIDX table is a straight mirror of PIECECH and the asm
// renderer reuses the existing dark<<4|nibble indexing idiom.
func TileIndex(nib byte, dark bool) int {
	var i int
	switch {
	case nib >= WPawn && nib <= WKing:
		i = int(nib) - WPawn
	case nib >= BPawn && nib <= BKing:
		i = int(nib) - BPawn + 6
	default:
		return -1
	}
	if dark {
		i += 12
	}
	return i
}

// Light reports whether square (r,f) is a light square. r=0 is the top
// row (rank 8), f=0 is the a-file, so row 0 reads L D L D L D L D.
func Light(r, f int) bool { return (r+f)%2 == 0 }

// Background returns the constant byte a background (piece-free) DHGR
// byte column c of a square holds: the light-square dither for a light
// square, $00 for a dark one.
func Background(c int, dark bool) byte {
	if dark {
		return 0
	}
	if c < TileCols/2 {
		return BGAux
	}
	return BGMain
}

// colPixels returns the tile-local x range [lo,hi] covered by byte column
// c, and whether it lives in the aux bank. Columns are ordered
// [aux0 aux1 aux2 main0 main1 main2]: aux_i covers 14i..14i+6, main_i
// covers 14i+7..14i+13.
func colPixels(c int) (lo, hi int, aux bool) {
	if c < TileCols/2 {
		return 14 * c, 14*c + 6, true
	}
	i := c - TileCols/2
	return 14*i + 7, 14*i + 13, false
}

// ---------------------------------------------------------------------
// Screen: a decoded monochrome DHGR bitmap.
// ---------------------------------------------------------------------

// Screen is a 560x192 monochrome DHGR bitmap.
type Screen struct {
	pix [ScreenH][ScreenW]bool
}

// lineOffset is the DHGR byte offset of scanline y's byte column 0,
// within either bank. This is cmd/dhgr2png's address arithmetic verbatim.
func lineOffset(y int) int { return (y&7)*1024 + ((y>>3)&7)*128 + (y>>6)*40 }

// Decode unpacks a 16384-byte A2FC screen save into a bitmap.
func Decode(data []byte) (*Screen, error) {
	if len(data) != A2FCSize {
		return nil, fmt.Errorf("tiles: expected %d bytes (aux+main), got %d", A2FCSize, len(data))
	}
	aux, mainb := data[:BankSize], data[BankSize:]
	s := &Screen{}
	for y := range ScreenH {
		off := lineOffset(y)
		x := 0
		for col := range 40 {
			for _, b := range []byte{aux[off+col], mainb[off+col]} {
				for bit := range 7 {
					s.pix[y][x] = b>>bit&1 != 0
					x++
				}
			}
		}
	}
	return s, nil
}

// At reports whether dot (x,y) is lit; out-of-range dots read dark.
func (s *Screen) At(x, y int) bool {
	if x < 0 || x >= ScreenW || y < 0 || y >= ScreenH {
		return false
	}
	return s.pix[y][x]
}

// ---------------------------------------------------------------------
// Slicing the artwork.
// ---------------------------------------------------------------------

// srcByte packs the seven source dots that byte column c of square (r,f)
// covers, taking tile-local row ty (source dy = ty+SrcTrimTop) and tile
// column tx directly from the source square's dx (the tile drops the two
// RIGHT dot columns, an even trim that leaves the dither phase alone).
func srcByte(s *Screen, r, f, ty, c int) byte {
	lo, _, _ := colPixels(c)
	x0 := SrcOriginX + SrcSquareW*f + lo
	y := SrcOriginY + SrcSquareH*r + SrcTrimTop + ty
	var b byte
	for bit := range 7 {
		if s.At(x0+bit, y) {
			b |= 1 << bit
		}
	}
	return b
}

// artEntry names one drawn piece and where it was drawn. The artwork is
// the standard start position plus four extra squares (rows 2 and 5) that
// supply the king and queen on the square shade their back rank misses.
type artEntry struct {
	Name string // FEN letter: uppercase white, lowercase black
	Nib  byte
	Dark bool
	R, F int
}

// artMap is the verified transcription of the drawing.
var artMap = []artEntry{
	{"r", BRook, true, 0, 7}, {"r", BRook, false, 0, 0},
	{"R", WRook, false, 7, 7}, {"R", WRook, true, 7, 0},
	{"n", BKnight, true, 0, 1}, {"n", BKnight, false, 0, 6},
	{"N", WKnight, false, 7, 1}, {"N", WKnight, true, 7, 6},
	{"b", BBishop, true, 0, 5}, {"b", BBishop, false, 0, 2},
	{"B", WBishop, false, 7, 5}, {"B", WBishop, true, 7, 2},
	{"q", BQueen, true, 0, 3}, {"q", BQueen, false, 2, 4},
	{"Q", WQueen, false, 7, 3}, {"Q", WQueen, true, 5, 4},
	{"k", BKing, false, 0, 4}, {"k", BKing, true, 2, 3},
	{"K", WKing, true, 7, 4}, {"K", WKing, false, 5, 3},
	{"p", BPawn, true, 1, 0}, {"p", BPawn, false, 1, 1},
	{"P", WPawn, false, 6, 0}, {"P", WPawn, true, 6, 1},
}

// ArtMap returns the source-square transcription, in blob-index order.
func ArtMap() []artEntry { return artMap }

// Index is the blob tile index this drawn piece slices into.
func (e artEntry) Index() int { return TileIndex(e.Nib, e.Dark) }

// extraSquares are the four squares the artist drew OUTSIDE the start
// position, in rows 2 and 5, to supply the king and queen on the square
// shade their own back rank cannot cover. (Both rows carry the king on
// the f-file's left neighbour and the queen beside it — swapped relative
// to the back rank — which is exactly what makes the shades come out.)
var extraSquares = [][2]int{{2, 3}, {2, 4}, {5, 3}, {5, 4}}

// EmptySquares lists the artwork squares that must be pure background:
// the artwork is the standard START POSITION (rows 0, 1, 6 and 7 fully
// occupied — every pawn is drawn, artMap just names one of each shade)
// plus extraSquares, so the empties are the rest of rows 2..5.
func EmptySquares() [][2]int {
	occupied := map[[2]int]bool{}
	for _, sq := range extraSquares {
		occupied[sq] = true
	}
	var out [][2]int
	for _, r := range []int{2, 3, 4, 5} {
		for f := range 8 {
			if !occupied[[2]int{r, f}] {
				out = append(out, [2]int{r, f})
			}
		}
	}
	return out
}

// Build slices the artwork into the resident tile blob, failing loudly if
// any of the measured facts the format rests on no longer hold.
func Build(a2fc []byte) ([]byte, error) {
	s, err := Decode(a2fc)
	if err != nil {
		return nil, err
	}
	if err := checkArtMap(); err != nil {
		return nil, err
	}
	if err := CheckBackgrounds(s); err != nil {
		return nil, err
	}

	blob := make([]byte, BlobSize)
	seen := map[int]string{}
	for _, e := range artMap {
		idx := e.Index()
		if idx < 0 {
			return nil, fmt.Errorf("tiles: %q has non-piece nibble $%02X", e.Name, e.Nib)
		}
		if prev, dup := seen[idx]; dup {
			return nil, fmt.Errorf("tiles: tile %d claimed by both %q and %q", idx, prev, e.Name)
		}
		seen[idx] = e.Name
		base := idx * TileStride
		for ty := range TileH {
			for c := range TileCols {
				b := srcByte(s, e.R, e.F, ty, c)
				if c < FirstCol || c > LastCol {
					// The whole 4-byte format depends on this.
					if want := Background(c, e.Dark); b != want {
						return nil, fmt.Errorf(
							"tiles: %q (r=%d,f=%d) row %d byte column %d is $%02X, not background $%02X: "+
								"the artwork no longer fits in byte columns %d..%d",
							e.Name, e.R, e.F, ty, c, b, want, FirstCol, LastCol)
					}
					continue
				}
				blob[base+ty*TileBytes+(c-FirstCol)] = b
			}
		}
	}
	if len(seen) != NumTiles {
		return nil, fmt.Errorf("tiles: artMap filled %d of %d tiles", len(seen), NumTiles)
	}
	return blob, nil
}

// checkArtMap verifies the transcription against the board's colouring:
// every entry's Dark flag must match the shade of the square it names.
// This is the check that catches a typo'd rank or file.
func checkArtMap() error {
	if len(artMap) != NumTiles {
		return fmt.Errorf("tiles: artMap has %d entries, want %d", len(artMap), NumTiles)
	}
	for _, e := range artMap {
		if e.R < 0 || e.R > 7 || e.F < 0 || e.F > 7 {
			return fmt.Errorf("tiles: %q at out-of-range square (%d,%d)", e.Name, e.R, e.F)
		}
		if Light(e.R, e.F) == e.Dark {
			return fmt.Errorf("tiles: %q maps to (r=%d,f=%d), which is a %s square, but is tagged %s",
				e.Name, e.R, e.F, shade(Light(e.R, e.F)), shade(!e.Dark))
		}
	}
	return nil
}

func shade(light bool) string {
	if light {
		return "light"
	}
	return "dark"
}

// CheckBackgrounds asserts the two facts that let the runtime synthesise
// empty squares and skip the outer dot columns: every square's dx 0..7
// and dx 35..43 are pure background, and every undrawn square is EXACTLY
// the synthetic background over its whole area.
func CheckBackgrounds(s *Screen) error {
	var errs []error
	for r := range 8 {
		for f := range 8 {
			dark := !Light(r, f)
			for dy := range SrcSquareH {
				for dx := range SrcSquareW {
					if dx >= ContentMinDX && dx <= ContentMaxDX {
						continue // content region
					}
					if got, want := srcDot(s, r, f, dy, dx), bgDot(dx, dark); got != want {
						errs = append(errs, fmt.Errorf(
							"square (r=%d,f=%d) dot (dx=%d,dy=%d) is %v, want background %v",
							r, f, dx, dy, got, want))
					}
				}
			}
		}
	}
	for _, sq := range EmptySquares() {
		r, f := sq[0], sq[1]
		dark := !Light(r, f)
		for dy := range SrcSquareH {
			for dx := range SrcSquareW {
				if got, want := srcDot(s, r, f, dy, dx), bgDot(dx, dark); got != want {
					errs = append(errs, fmt.Errorf(
						"empty square (r=%d,f=%d) dot (dx=%d,dy=%d) is %v, want background %v",
						r, f, dx, dy, got, want))
				}
			}
		}
	}
	if len(errs) > 0 {
		if len(errs) > 8 {
			errs = append(errs[:8], fmt.Errorf("... and %d more", len(errs)-8))
		}
		return fmt.Errorf("tiles: background assumption broken: %w", errors.Join(errs...))
	}
	return nil
}

// srcDot reads dot (dx,dy) of source square (r,f).
func srcDot(s *Screen, r, f, dy, dx int) bool {
	return s.At(SrcOriginX+SrcSquareW*f+dx, SrcOriginY+SrcSquareH*r+dy)
}

// bgDot is the background state of dot dx: the light-square dither is
// phase-locked to even ABSOLUTE x, and SrcOriginX and SrcSquareW are both
// even, so dx's parity is the absolute parity.
func bgDot(dx int, dark bool) bool { return !dark && dx%2 == 0 }

// Deviations counts dots in square (r,f) that differ from its background,
// over the dy rows in [y0,y1] and dx columns in [x0,x1] inclusive.
func Deviations(s *Screen, r, f, y0, y1, x0, x1 int) int {
	dark := !Light(r, f)
	n := 0
	for dy := y0; dy <= y1; dy++ {
		for dx := x0; dx <= x1; dx++ {
			if srcDot(s, r, f, dy, dx) != bgDot(dx, dark) {
				n++
			}
		}
	}
	return n
}

// BoardDeviations sums Deviations over all 64 squares.
func BoardDeviations(s *Screen, y0, y1, x0, x1 int) int {
	n := 0
	for r := range 8 {
		for f := range 8 {
			n += Deviations(s, r, f, y0, y1, x0, x1)
		}
	}
	return n
}

// ---------------------------------------------------------------------
// Reading and rendering the blob.
// ---------------------------------------------------------------------

// Row returns tile idx's row ty: the 4 stored bytes, byte columns 1..4,
// i.e. [aux1, aux2, main0, main1].
func Row(blob []byte, idx, ty int) []byte {
	off := idx*TileStride + ty*TileBytes
	return blob[off : off+TileBytes]
}

// Render composes a DHGR screen (A2FC bytes, ready to BLOAD) showing
// `board` — 64 piece nibbles in (r,f) order, r=0 = rank 8, 0 = empty —
// drawn from the blob at byte column originCol and scanline originY.
// This is the reference for what the asm renderer must do: per square per
// row, write TileCols bytes, of which the outer two are the synthesised
// background constant and the inner four come straight from the tile.
func Render(blob []byte, board *[64]byte, originCol, originY int) ([]byte, error) {
	if len(blob) != BlobSize {
		return nil, fmt.Errorf("tiles: blob is %d bytes, want %d", len(blob), BlobSize)
	}
	if originCol < 0 || originCol+8*(SquareCols/2) > 40 {
		return nil, fmt.Errorf("tiles: originCol %d does not fit 8 squares", originCol)
	}
	if originY < 0 || originY+8*TileH > ScreenH {
		return nil, fmt.Errorf("tiles: originY %d does not fit 8 squares", originY)
	}
	out := make([]byte, A2FCSize)
	aux, mainb := out[:BankSize], out[BankSize:]
	for r := range 8 {
		for f := range 8 {
			dark := !Light(r, f)
			idx := TileIndex(board[r*8+f], dark)
			for ty := range TileH {
				line := lineOffset(originY + r*TileH + ty)
				var row []byte
				if idx >= 0 {
					row = Row(blob, idx, ty)
				}
				for c := range TileCols {
					b := Background(c, dark)
					if idx >= 0 && c >= FirstCol && c <= LastCol {
						b = row[c-FirstCol]
					}
					// Column c is aux plane index c, or main plane
					// index c-3; a square is 3 byte columns wide in
					// each plane.
					bank, i := aux, c
					if c >= TileCols/2 {
						bank, i = mainb, c-TileCols/2
					}
					bank[line+originCol+f*(TileCols/2)+i] = b
				}
			}
		}
	}
	return out, nil
}

// StartPosition is the standard start position as 64 piece nibbles in
// (r,f) order (r=0 = rank 8, f=0 = the a-file).
func StartPosition() [64]byte {
	var b [64]byte
	back := []byte{WRook, WKnight, WBishop, WQueen, WKing, WBishop, WKnight, WRook}
	for f := range 8 {
		b[0*8+f] = back[f] | 0x08 // black back rank
		b[1*8+f] = BPawn
		b[6*8+f] = WPawn
		b[7*8+f] = back[f]
	}
	return b
}
