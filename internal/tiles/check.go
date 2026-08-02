package tiles

// check.go is the artwork's DIAGNOSTIC pass, as opposed to Build's gate.
//
// Build stops at the first broken assumption, which is right for a
// generator but useless for redrawing: you fix one pixel, re-run, and hit
// the next assertion. Check runs every assumption the tile format rests
// on and returns ALL the findings at once, so one run tells you
// everything the artwork still owes you. cmd/gentiles -check prints it.
//
// Two kinds of finding, deliberately distinguished:
//
//   - CLIPPED ink: piece dots in source rows dy 0..SrcTrimTop-1, which
//     the tile drops. The art still slices; it just loses those dots.
//     Zero is the goal state.
//   - VIOLATIONS: assumptions whose failure means the art does not slice
//     at all (content outside dx ContentMinDX..ContentMaxDX, a broken
//     dither phase, an empty square that is no longer empty, ...).

import (
	"bytes"
	"fmt"
	"sort"
)

// Dot is a square-local dot coordinate.
type Dot struct{ DX, DY int }

// SquareInk is the ink one square contributes to a measurement, with the
// dots that make it up.
type SquareInk struct {
	R, F int
	Dots []Dot
}

// Count is the number of ink dots.
func (s SquareInk) Count() int { return len(s.Dots) }

// Extent is the bounding box of one square's piece ink, in square-local
// coordinates. Empty reports whether the square holds no ink at all.
type Extent struct {
	R, F         int
	MinDX, MaxDX int
	MinDY, MaxDY int
	Count        int
	Empty        bool
}

// Violation is one broken geometric assumption. Kind groups related
// findings so a report can cap each category independently; Where names
// the square (and piece) and Detail the offending coordinates.
type Violation struct {
	Kind   string
	Where  string
	Detail string
}

func (v Violation) String() string {
	if v.Where == "" {
		return fmt.Sprintf("[%s] %s", v.Kind, v.Detail)
	}
	return fmt.Sprintf("[%s] %s: %s", v.Kind, v.Where, v.Detail)
}

// Violation kinds, in the order a report should present them.
const (
	KindArtMap   = "artmap"         // the transcription itself
	KindBBox     = "bounding-box"   // lit extent of the whole drawing
	KindBorder   = "border"         // the drawn frame that pins the origin
	KindDither   = "dither"         // light-square phase / $55,$2A constants
	KindWindow   = "content-window" // ink outside dx ContentMinDX..ContentMaxDX
	KindOuterCol = "outer-column"   // byte columns 0 and 5 of a piece tile
	KindEmptySq  = "empty-square"   // a square the runtime synthesises
	KindExtraSq  = "extra-square"   // the four off-start-position squares
	KindPawnRow  = "pawn-row"       // all eight pawns must slice identically
	KindBuild    = "build"          // Build failed for some other reason
)

// CheckKinds are the assumptions Check measures against the pixels, in
// report order. KindBuild is not one of them: it is the escape hatch for
// a Build failure none of these explains.
var CheckKinds = []string{
	KindArtMap, KindBBox, KindBorder, KindDither, KindWindow,
	KindOuterCol, KindEmptySq, KindExtraSq, KindPawnRow,
}

var kindOrder = append(append([]string{}, CheckKinds...), KindBuild)

// Report is everything one Check run learned about a piece of artwork.
type Report struct {
	// Clipped is the per-square piece ink that the top trim discards,
	// squares in board order, only squares that lose something.
	Clipped []SquareInk
	// ClippedTotal is the board-wide clipped dot count.
	ClippedTotal int

	// Violations are the broken assumptions, in kindOrder.
	Violations []Violation

	// Extents is the ink bounding box of every square the transcription
	// names, in blob-tile-index order.
	Extents []Extent

	// TrimCosts[n] is the board-wide ink in the top n source rows, for
	// n = 0..3, plus BottomCost for the last row alone. These are the
	// numbers behind choosing SrcTrimTop.
	TrimCosts  [4]int
	BottomCost int

	// Blob is the sliced blob, nil if the artwork would not slice.
	Blob []byte
	// Changed names the tiles whose bytes differ from the committed blob
	// (nil if Blob is nil or the committed blob is the wrong size).
	Changed []string
}

// Clean reports whether the artwork slices AND loses nothing: the state a
// redraw is aiming at.
func (r *Report) Clean() bool { return r.ClippedTotal == 0 && len(r.Violations) == 0 }

// Check runs every assumption over an A2FC artwork save and reports them
// all. It writes nothing, and it never mutates its argument.
func Check(a2fc []byte) (*Report, error) {
	s, err := Decode(a2fc)
	if err != nil {
		return nil, err
	}
	rep := &Report{}

	// --- The headline: ink the top trim would throw away. --------------
	for r := range 8 {
		for f := range 8 {
			if ink := inkIn(s, r, f, 0, SrcTrimTop-1, 0, SrcSquareW-1); ink.Count() > 0 {
				rep.Clipped = append(rep.Clipped, ink)
				rep.ClippedTotal += ink.Count()
			}
		}
	}

	// --- The trim-cost curve, for context. -----------------------------
	for n := range 4 {
		if n > 0 {
			rep.TrimCosts[n] = boardInk(s, 0, n-1, 0, SrcSquareW-1)
		}
	}
	rep.BottomCost = boardInk(s, SrcSquareH-1, SrcSquareH-1, 0, SrcSquareW-1)

	// --- Every other assumption, as a list. ----------------------------
	rep.Violations = append(rep.Violations, checkArtMapV()...)
	rep.Violations = append(rep.Violations, checkBBox(s)...)
	rep.Violations = append(rep.Violations, checkBorder(s)...)
	rep.Violations = append(rep.Violations, checkDither(s)...)
	rep.Violations = append(rep.Violations, checkWindow(s)...)
	rep.Violations = append(rep.Violations, checkOuterCols(s)...)
	rep.Violations = append(rep.Violations, checkEmpties(s)...)
	rep.Violations = append(rep.Violations, checkExtras(s)...)
	rep.Violations = append(rep.Violations, checkPawnRows(s)...)
	sortViolations(rep.Violations)

	// --- Per-tile ink extents, the redraw's working numbers. -----------
	for _, e := range artMap {
		rep.Extents = append(rep.Extents, extentOf(s, e.R, e.F))
	}
	sort.SliceStable(rep.Extents, func(i, j int) bool {
		return tileIndexAt(rep.Extents[i].R, rep.Extents[i].F) < tileIndexAt(rep.Extents[j].R, rep.Extents[j].F)
	})

	// --- Would it slice, and what changed? -----------------------------
	blob, err := Build(a2fc)
	if err != nil {
		if !hasKindOtherThan(rep.Violations, KindBuild) {
			// Build refused for a reason no check above caught: surface it
			// verbatim rather than reporting a clean bill of health.
			rep.Violations = append(rep.Violations, Violation{Kind: KindBuild, Detail: err.Error()})
		}
	} else {
		rep.Blob = blob
		if old := DefaultBlob(); len(old) == len(blob) {
			for _, e := range artMap {
				idx := e.Index()
				lo, hi := idx*TileStride, (idx+1)*TileStride
				if !bytes.Equal(old[lo:hi], blob[lo:hi]) {
					rep.Changed = append(rep.Changed, fmt.Sprintf("%d %s", idx, PieceName(e.Nib, e.Dark)))
				}
			}
		}
	}
	return rep, nil
}

// ---------------------------------------------------------------------
// Ink: measured against a same-shade EMPTY square, not against a formula.
// ---------------------------------------------------------------------

// refEmpty returns an artwork square that is drawn empty and has the same
// shade as (r,f). Measuring "ink" as "differs from this square" counts
// piece dots and not the light-square dither — comparing against a
// DIFFERENT-shade square (or against nothing) makes every light square
// look like solid ink.
func refEmpty(r, f int) (int, int) {
	want := Light(r, f)
	for _, sq := range EmptySquares() {
		if Light(sq[0], sq[1]) == want {
			return sq[0], sq[1]
		}
	}
	// Unreachable while rows 2..5 hold both shades; fall back to the
	// analytic background by pointing at the square itself, which makes
	// InkAt report no ink rather than reporting nonsense.
	return r, f
}

// InkAt reports whether dot (dx,dy) of square (r,f) is piece ink: it
// differs from the corresponding dot of a same-shade empty square.
func InkAt(s *Screen, r, f, dy, dx int) bool {
	er, ef := refEmpty(r, f)
	return srcDot(s, r, f, dy, dx) != srcDot(s, er, ef, dy, dx)
}

// inkIn collects the ink dots of square (r,f) inside the inclusive
// dy [y0,y1] x dx [x0,x1] window.
func inkIn(s *Screen, r, f, y0, y1, x0, x1 int) SquareInk {
	er, ef := refEmpty(r, f)
	out := SquareInk{R: r, F: f}
	for dy := y0; dy <= y1; dy++ {
		for dx := x0; dx <= x1; dx++ {
			if srcDot(s, r, f, dy, dx) != srcDot(s, er, ef, dy, dx) {
				out.Dots = append(out.Dots, Dot{DX: dx, DY: dy})
			}
		}
	}
	return out
}

// boardInk sums ink over all 64 squares in a window.
func boardInk(s *Screen, y0, y1, x0, x1 int) int {
	n := 0
	for r := range 8 {
		for f := range 8 {
			n += inkIn(s, r, f, y0, y1, x0, x1).Count()
		}
	}
	return n
}

// extentOf is square (r,f)'s ink bounding box.
func extentOf(s *Screen, r, f int) Extent {
	e := Extent{R: r, F: f, MinDX: SrcSquareW, MaxDX: -1, MinDY: SrcSquareH, MaxDY: -1, Empty: true}
	for _, d := range inkIn(s, r, f, 0, SrcSquareH-1, 0, SrcSquareW-1).Dots {
		e.Empty = false
		e.Count++
		e.MinDX = min(e.MinDX, d.DX)
		e.MaxDX = max(e.MaxDX, d.DX)
		e.MinDY = min(e.MinDY, d.DY)
		e.MaxDY = max(e.MaxDY, d.DY)
	}
	return e
}

// ---------------------------------------------------------------------
// The individual assumptions.
// ---------------------------------------------------------------------

func checkArtMapV() []Violation {
	var out []Violation
	if err := checkArtMap(); err != nil {
		out = append(out, Violation{Kind: KindArtMap, Detail: err.Error()})
	}
	seen := map[int]string{}
	for _, e := range artMap {
		idx := e.Index()
		if idx < 0 || idx >= NumTiles {
			out = append(out, Violation{Kind: KindArtMap, Where: Square(e.R, e.F),
				Detail: fmt.Sprintf("%q maps to tile index %d", e.Name, idx)})
			continue
		}
		if prev, dup := seen[idx]; dup {
			out = append(out, Violation{Kind: KindArtMap, Where: Square(e.R, e.F),
				Detail: fmt.Sprintf("tile %d claimed by both %q and %q", idx, prev, e.Name)})
		}
		seen[idx] = e.Name
	}
	if len(seen) != NumTiles {
		out = append(out, Violation{Kind: KindArtMap,
			Detail: fmt.Sprintf("the transcription covers %d of %d tiles", len(seen), NumTiles)})
	}
	return out
}

func checkBBox(s *Screen) []Violation {
	minX, minY := ScreenW, ScreenH
	maxX, maxY := -1, -1
	for y := range ScreenH {
		for x := range ScreenW {
			if !s.At(x, y) {
				continue
			}
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	if minX == BoardMinX && maxX == BoardMaxX && minY == BoardMinY && maxY == BoardMaxY {
		return nil
	}
	return []Violation{{Kind: KindBBox, Detail: fmt.Sprintf(
		"lit bounding box is x %d..%d, y %d..%d; want x %d..%d, y %d..%d (the drawing moved or grew)",
		minX, maxX, minY, maxY, BoardMinX, BoardMaxX, BoardMinY, BoardMaxY)}}
}

// borderCols are the fully-lit frame columns the grid origin is measured
// from: the left border is 0..BorderLeft, and the right one mirrors it.
var borderCols = []int{0, 1, 2, 3, 364, 365, 366, 367}

func checkBorder(s *Screen) []Violation {
	var out []Violation
	for _, y := range []int{BorderTop, BorderBot} {
		for x := BoardMinX; x <= BoardMaxX; x++ {
			if !s.At(x, y) {
				out = append(out, Violation{Kind: KindBorder,
					Detail: fmt.Sprintf("border row y=%d is broken at x=%d", y, x)})
				break
			}
		}
	}
	for _, x := range borderCols {
		for y := BoardMinY; y <= BoardMaxY; y++ {
			if !s.At(x, y) {
				out = append(out, Violation{Kind: KindBorder,
					Detail: fmt.Sprintf("border column x=%d is broken at y=%d", x, y)})
				break
			}
		}
	}
	if SrcOriginX <= BorderLeft {
		out = append(out, Violation{Kind: KindBorder,
			Detail: fmt.Sprintf("grid origin x=%d overlaps the border (0..%d)", SrcOriginX, BorderLeft)})
	}
	if lastX := SrcOriginX + 8*SrcSquareW - 1; lastX >= borderCols[4] {
		out = append(out, Violation{Kind: KindBorder,
			Detail: fmt.Sprintf("grid right edge x=%d runs into the right border (starts at %d)", lastX, borderCols[4])})
	}
	if lastY := SrcOriginY + 8*SrcSquareH - 1; lastY >= BorderBot {
		out = append(out, Violation{Kind: KindBorder,
			Detail: fmt.Sprintf("grid bottom edge y=%d runs into the bottom border (%d)", lastY, BorderBot)})
	}
	return out
}

// checkDither asserts the phase lock the whole byte format rests on: on a
// light square the LIT background dots are the even-absolute-x ones, and
// the byte-level consequence is the constant pair aux=$55 / main=$2A.
// Sampled in the guaranteed-background left margin, dx 0..ContentMinDX-1,
// so a piece drawn over the dither cannot mask a phase error.
func checkDither(s *Screen) []Violation {
	var out []Violation
	for r := range 8 {
		for f := range 8 {
			dark := !Light(r, f)
			for dy := range SrcSquareH {
				for dx := range ContentMinDX {
					x := SrcOriginX + SrcSquareW*f + dx
					if got, want := srcDot(s, r, f, dy, dx), bgDot(dx, dark); got != want {
						out = append(out, Violation{Kind: KindDither, Where: Square(r, f),
							Detail: fmt.Sprintf(
								"dot (dx=%d,dy=%d) (absolute x=%d) is %s; a %s square's background wants %s",
								dx, dy, x, lit(got), shade(!dark), lit(want))})
					}
				}
			}
			// The byte-level statement of the same fact, in the two byte
			// columns that are background in every square.
			for _, c := range []int{0, TileCols - 1} {
				for ty := range TileH {
					if got, want := srcByte(s, r, f, ty, c), Background(c, dark); got != want {
						out = append(out, Violation{Kind: KindDither, Where: Square(r, f),
							Detail: fmt.Sprintf("byte column %d of tile row %d is $%02X, want $%02X",
								c, ty, got, want)})
					}
				}
			}
		}
	}
	return out
}

// checkWindow asserts that all ink lives inside dx ContentMinDX..ContentMaxDX.
// Everything outside is what lets a tile be 42 dots wide and store only
// four bytes per row.
func checkWindow(s *Screen) []Violation {
	var out []Violation
	for r := range 8 {
		for f := range 8 {
			for _, w := range [][2]int{{0, ContentMinDX - 1}, {ContentMaxDX + 1, SrcSquareW - 1}} {
				for _, d := range inkIn(s, r, f, 0, SrcSquareH-1, w[0], w[1]).Dots {
					side := "left"
					if d.DX > ContentMaxDX {
						side = "right"
					}
					out = append(out, Violation{Kind: KindWindow, Where: Square(r, f),
						Detail: fmt.Sprintf("ink at (dx=%d,dy=%d) is in the %s margin; all ink must be inside dx %d..%d",
							d.DX, d.DY, side, ContentMinDX, ContentMaxDX)})
				}
			}
		}
	}
	return out
}

// checkOuterCols is Build's own assertion, in Build's own terms: byte
// columns 0 (aux0) and TileCols-1 (main2) of all 24 piece tiles are pure
// background, which is why only columns 1..4 are stored.
func checkOuterCols(s *Screen) []Violation {
	var out []Violation
	for _, e := range artMap {
		for ty := range TileH {
			for _, c := range []int{0, TileCols - 1} {
				if got, want := srcByte(s, e.R, e.F, ty, c), Background(c, e.Dark); got != want {
					out = append(out, Violation{Kind: KindOuterCol, Where: Square(e.R, e.F),
						Detail: fmt.Sprintf("%s: tile row %d byte column %d is $%02X, not background $%02X (only columns %d..%d are stored)",
							PieceName(e.Nib, e.Dark), ty, c, got, want, FirstCol, LastCol)})
				}
			}
		}
	}
	return out
}

// checkEmpties asserts every undrawn square is EXACTLY the background the
// runtime synthesises, which is why the blob ships no empty-square tiles.
func checkEmpties(s *Screen) []Violation {
	var out []Violation
	for _, sq := range EmptySquares() {
		r, f := sq[0], sq[1]
		dark := !Light(r, f)
		for dy := range SrcSquareH {
			for dx := range SrcSquareW {
				if got, want := srcDot(s, r, f, dy, dx), bgDot(dx, dark); got != want {
					out = append(out, Violation{Kind: KindEmptySq, Where: Square(r, f),
						Detail: fmt.Sprintf("dot (dx=%d,dy=%d) is %s, but this square must be pure background (%s)",
							dx, dy, lit(got), lit(want))})
				}
			}
		}
	}
	return out
}

// checkExtras asserts the four off-start-position squares still hold the
// king and queen the transcription reads out of them.
func checkExtras(s *Screen) []Violation {
	var out []Violation
	for _, sq := range extraSquares {
		if n := inkIn(s, sq[0], sq[1], 0, SrcSquareH-1, 0, SrcSquareW-1).Count(); n == 0 {
			out = append(out, Violation{Kind: KindExtraSq, Where: Square(sq[0], sq[1]),
				Detail: "blank, but the transcription reads a piece out of it"})
		}
	}
	return out
}

// checkPawnRows asserts all eight pawns of a row are drawn identically:
// the transcription slices ONE of each shade, so a pawn redrawn on only
// some files would ship silently.
func checkPawnRows(s *Screen) []Violation {
	var out []Violation
	for _, pr := range []struct {
		row int
		nib byte
	}{{1, BPawn}, {6, WPawn}} {
		ref := map[bool]int{} // shade -> reference file
		for f := range 8 {
			dark := !Light(pr.row, f)
			rf, ok := ref[dark]
			if !ok {
				ref[dark] = f
				continue
			}
			// Compare the WHOLE source square, clipped rows included: a
			// redraw that fixes one pawn and not its neighbours must show
			// up here even before the trim throws the difference away.
			for dy := range SrcSquareH {
				for dx := range SrcSquareW {
					got, want := srcDot(s, pr.row, f, dy, dx), srcDot(s, pr.row, rf, dy, dx)
					if got != want {
						out = append(out, Violation{Kind: KindPawnRow, Where: Square(pr.row, f),
							Detail: fmt.Sprintf("%s differs from the same-shade pawn on %s: dot (dx=%d,dy=%d) is %s, not %s",
								PieceName(pr.nib, dark), Square(pr.row, rf), dx, dy, lit(got), lit(want))})
					}
				}
			}
		}
	}
	return out
}

func hasKindOtherThan(vs []Violation, kind string) bool {
	for _, v := range vs {
		if v.Kind != kind {
			return true
		}
	}
	return false
}

func sortViolations(vs []Violation) {
	rank := map[string]int{}
	for i, k := range kindOrder {
		rank[k] = i
	}
	sort.SliceStable(vs, func(i, j int) bool { return rank[vs[i].Kind] < rank[vs[j].Kind] })
}

// ---------------------------------------------------------------------
// Naming, so a report reads like a chessboard and not like an array.
// ---------------------------------------------------------------------

// Square is (r,f) in algebraic notation: r=0 is rank 8, f=0 is the a-file.
func Square(r, f int) string {
	if r < 0 || r > 7 || f < 0 || f > 7 {
		return fmt.Sprintf("(r=%d,f=%d)", r, f)
	}
	return fmt.Sprintf("%c%c", 'a'+f, '8'-r)
}

var pieceNames = map[byte]string{
	WPawn: "pawn", WKnight: "knight", WBishop: "bishop",
	WRook: "rook", WQueen: "queen", WKing: "king",
}

// PieceName spells out a piece nibble and the shade of square it stands
// on, e.g. "black bishop on dark".
func PieceName(nib byte, dark bool) string {
	colour := "white"
	base := nib
	if nib >= BPawn {
		colour, base = "black", nib-8
	}
	name, ok := pieceNames[base]
	if !ok {
		return fmt.Sprintf("nibble $%02X", nib)
	}
	return fmt.Sprintf("%s %s on %s", colour, name, shade(!dark))
}

// tileIndexAt is the blob tile index of the piece the transcription draws
// at (r,f), or -1.
func tileIndexAt(r, f int) int {
	for _, e := range artMap {
		if e.R == r && e.F == f {
			return e.Index()
		}
	}
	return -1
}

// PieceAt names the piece the artwork draws on square (r,f): the four
// transcribed extras, otherwise the start position, otherwise "".
func PieceAt(r, f int) string {
	for _, e := range artMap {
		if e.R == r && e.F == f {
			return PieceName(e.Nib, e.Dark)
		}
	}
	if nib := StartPosition()[r*8+f]; nib != 0 {
		return PieceName(nib, !Light(r, f))
	}
	return ""
}

func lit(b bool) string {
	if b {
		return "lit"
	}
	return "dark"
}

// ---------------------------------------------------------------------
// Synthesising broken artwork, for tests and tools ONLY.
// ---------------------------------------------------------------------

// SrcDotPos maps square-local (dx,dy) of square (r,f) to absolute screen
// coordinates.
func SrcDotPos(r, f, dy, dx int) (x, y int) {
	return SrcOriginX + SrcSquareW*f + dx, SrcOriginY + SrcSquareH*r + dy
}

// SetDot lights dot (x,y) in an in-memory A2FC screen save, inverting
// Decode's address arithmetic: byte column x/14, aux bank for the low 7
// dots of the pair and main for the high 7, LSB first.
//
// This exists so tests can synthesise BROKEN artwork and prove the
// checker catches it. Nothing in this repo writes assets/ — the drawing
// is the single source of truth and is edited only in DazzleDraw.
func SetDot(data []byte, x, y int) {
	off := lineOffset(y) + x/14
	if x%14 >= 7 {
		off += BankSize
	}
	data[off] |= 1 << (x % 14 % 7)
}
