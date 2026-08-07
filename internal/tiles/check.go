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
//   - LOST ink: piece dots outside KeptWindow — the region of a source
//     square that actually reaches a tile. Zero is the goal state, and
//     since the CHESS2 redraw it is the state the artwork is in.
//   - VIOLATIONS: assumptions whose failure means the art does not slice
//     at all (content outside dx ContentMinDX..ContentMaxDX, a broken
//     dither phase, an empty square that is no longer empty, ...).
//
// WHAT IS AND IS NOT LOAD-BEARING SINCE THE REDRAW (2026-08-06). The old
// artwork was 44x21 with the top two source rows trimmed away, and this
// file's headline measured the ink that trim threw out. With SrcTrimTop
// at 0 the VERTICAL half of that measurement is structurally zero: the
// tile is the whole square, so no row can be dropped and no assertion
// about dy can fail. Saying "no ink was clipped" would now be a tautology
// dressed as a gate, which is the specific way this project has been
// bitten before.
//
// So the measurement was widened rather than deleted. LOST ink is now
// measured against KeptWindow in BOTH axes, and its HORIZONTAL half is
// very much alive: the 4-byte tile row only covers dx 7..34, so a dot in
// a square's dx margins is still lost and still fails. And a new check —
// KindGrid — asserts the thing the redraw actually changed: that the
// declared 42x19 grid is the grid that was drawn, by requiring it to
// exactly fill the frame the artist drew around it. That is the assertion
// that would have caught this entire episode at the source.
//
// Load-bearing today: artmap, grid, bounding-box, border, dither,
// content-window, outer-column, empty-square, extra-square, pawn-row, and
// the horizontal half of LOST ink.
// Tautological today, and labelled as such where they are reported: the
// vertical half of LOST ink, and the trim-cost curve (which is now a
// what-if diagnostic for a future UI, not a gate).

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
	KindGrid     = "grid"           // the 8x8 grid vs the frame drawn around it
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
	KindArtMap, KindGrid, KindBBox, KindBorder, KindDither, KindWindow,
	KindOuterCol, KindEmptySq, KindExtraSq, KindPawnRow,
}

var kindOrder = append(append([]string{}, CheckKinds...), KindBuild)

// Report is everything one Check run learned about a piece of artwork.
type Report struct {
	// Lost is the per-square piece ink that falls outside KeptWindow and
	// therefore never reaches a tile, squares in board order, only squares
	// that lose something.
	Lost []SquareInk
	// LostTotal is the board-wide lost dot count.
	LostTotal int
	// LostRows / LostCols split LostTotal by WHY a dot was lost: outside
	// the kept ROW range (the trim), outside the kept DOT COLUMNS (the
	// 4-byte format). A dot outside both counts in each. With SrcTrimTop
	// at 0 the row half is structurally zero and the report says so
	// instead of quietly presenting it as a passing check.
	LostRows, LostCols int

	// Violations are the broken assumptions, in kindOrder.
	Violations []Violation

	// Extents is the ink bounding box of every square the transcription
	// names, in blob-tile-index order.
	Extents []Extent

	// InkMinDX..InkMaxDY is the board-wide ink bounding box, square-local:
	// where the drawing actually sits inside its squares, and therefore
	// how much slack it has against the declared window.
	InkMinDX, InkMaxDX int
	InkMinDY, InkMaxDY int

	// TrimCosts[n] is the board-wide ink in the top n source rows, for
	// n = 0..3, plus BottomCost for the last row alone. DIAGNOSTIC ONLY
	// since SrcTrimTop went to 0: it prices a trim nobody is taking, for
	// a future UI that might need shorter ranks.
	TrimCosts  [4]int
	BottomCost int

	// Blob is the sliced blob, nil if the artwork would not slice.
	Blob []byte
	// Changed names the tiles whose bytes differ from the committed blob
	// (nil if Blob is nil or the committed blob is the wrong size).
	Changed []string
}

// Clean reports whether the artwork slices AND loses nothing: the state a
// redraw is aiming at, and the state the committed artwork is in.
func (r *Report) Clean() bool { return r.LostTotal == 0 && len(r.Violations) == 0 }

// Check runs every assumption over an A2FC artwork save and reports them
// all. It writes nothing, and it never mutates its argument.
func Check(a2fc []byte) (*Report, error) {
	s, err := Decode(a2fc)
	if err != nil {
		return nil, err
	}
	rep := &Report{InkMinDX: SrcSquareW, InkMaxDX: -1, InkMinDY: SrcSquareH, InkMaxDY: -1}

	// --- The headline: ink that never reaches a tile. ------------------
	ky0, ky1, kx0, kx1 := KeptWindow()
	for r := range 8 {
		for f := range 8 {
			lost := SquareInk{R: r, F: f}
			for _, d := range inkIn(s, r, f, 0, SrcSquareH-1, 0, SrcSquareW-1).Dots {
				rep.InkMinDX, rep.InkMaxDX = min(rep.InkMinDX, d.DX), max(rep.InkMaxDX, d.DX)
				rep.InkMinDY, rep.InkMaxDY = min(rep.InkMinDY, d.DY), max(rep.InkMaxDY, d.DY)
				badRow := d.DY < ky0 || d.DY > ky1
				badCol := d.DX < kx0 || d.DX > kx1
				if badRow {
					rep.LostRows++
				}
				if badCol {
					rep.LostCols++
				}
				if badRow || badCol {
					lost.Dots = append(lost.Dots, d)
				}
			}
			if lost.Count() > 0 {
				rep.Lost = append(rep.Lost, lost)
				rep.LostTotal += lost.Count()
			}
		}
	}

	// --- The trim-cost curve, DIAGNOSTIC ONLY (SrcTrimTop is 0). -------
	for n := range 4 {
		if n > 0 {
			rep.TrimCosts[n] = boardInk(s, 0, n-1, 0, SrcSquareW-1)
		}
	}
	rep.BottomCost = boardInk(s, SrcSquareH-1, SrcSquareH-1, 0, SrcSquareW-1)

	// --- Every other assumption, as a list. ----------------------------
	rep.Violations = append(rep.Violations, checkArtMapV()...)
	rep.Violations = append(rep.Violations, checkGrid(s)...)
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

// BorderCols are the fully-lit frame columns the grid origin is measured
// from: BorderW columns at each end of the drawing, DERIVED from the
// measured bounding box rather than written down, so moving the drawing
// cannot leave a stale literal behind.
func BorderCols() []int {
	var out []int
	for i := range BorderW {
		out = append(out, BoardMinX+i, BorderRight+i)
	}
	sort.Ints(out)
	return out
}

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
	for _, x := range BorderCols() {
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
	if lastX := SrcOriginX + 8*SrcSquareW - 1; lastX >= BorderRight {
		out = append(out, Violation{Kind: KindBorder,
			Detail: fmt.Sprintf("grid right edge x=%d runs into the right border (starts at %d)", lastX, BorderRight)})
	}
	if lastY := SrcOriginY + 8*SrcSquareH - 1; lastY >= BorderBot {
		out = append(out, Violation{Kind: KindBorder,
			Detail: fmt.Sprintf("grid bottom edge y=%d runs into the bottom border (%d)", lastY, BorderBot)})
	}
	return out
}

// FrameGaps are the four dark rectangles between the drawn frame and the
// 8x8 grid, in absolute screen coordinates (inclusive). They are the empty
// margin the artist left inside the frame; nothing may be drawn there, and
// their widths are what tie the declared grid to the drawn frame.
func FrameGaps() [][4]int {
	gx0, gy0 := SrcOriginX, SrcOriginY
	gx1, gy1 := SrcOriginX+8*SrcSquareW-1, SrcOriginY+8*SrcSquareH-1
	inX0, inX1 := BorderLeft+1, BorderRight-1 // strictly inside the side bars
	inY0, inY1 := BorderTop+1, BorderBot-1    // strictly inside the rules
	return [][4]int{
		{inX0, inY0, gx0 - 1, inY1}, // left gap
		{gx1 + 1, inY0, inX1, inY1}, // right gap
		{inX0, inY0, inX1, gy0 - 1}, // top gap
		{inX0, gy1 + 1, inX1, inY1}, // bottom gap
	}
}

// checkGrid asserts that the declared grid is the grid that was DRAWN.
//
// This is the check the 2026-08-06 redraw exists because nobody had: the
// first artwork was on a 44x21 grid and the code trimmed it into 42x19
// tiles, and no assertion anywhere said "the source square and the tile
// are the same size". Two things are asserted here, and they meet in the
// middle:
//
//   - the CONSTANTS are self-consistent — the 8x8 grid, plus the declared
//     gaps, exactly spans the frame the bounding box and border checks
//     measure out of the pixels;
//   - the PIXELS agree — the gap between the frame and the grid is
//     entirely dark, so neither the frame nor any piece has moved into it.
//
// Break either half and the artwork stops slicing, loudly, at the source.
func checkGrid(s *Screen) []Violation {
	var out []Violation
	bad := func(format string, args ...any) {
		out = append(out, Violation{Kind: KindGrid, Detail: fmt.Sprintf(format, args...)})
	}

	// --- The constants describe one consistent drawing. ----------------
	if BorderLeft != BorderW-1 {
		bad("BorderLeft=%d but the side bars are BorderW=%d dots wide", BorderLeft, BorderW)
	}
	if BorderTop != BoardMinY || BorderBot != BoardMaxY {
		bad("the frame rules (y=%d,%d) are not the bounding box's top and bottom (y=%d,%d)",
			BorderTop, BorderBot, BoardMinY, BoardMaxY)
	}
	if got := SrcOriginX - (BorderLeft + 1); got != FrameGapX {
		bad("left gap is %d dots (origin x=%d, bar ends at %d) but FrameGapX=%d", got, SrcOriginX, BorderLeft, FrameGapX)
	}
	if got := BorderRight - (SrcOriginX + 8*SrcSquareW); got != FrameGapX {
		bad("right gap is %d dots (grid ends at x=%d, bar starts at %d) but FrameGapX=%d",
			got, SrcOriginX+8*SrcSquareW-1, BorderRight, FrameGapX)
	}
	if got := SrcOriginY - (BorderTop + 1); got != FrameGapY {
		bad("top gap is %d rows (origin y=%d, rule at %d) but FrameGapY=%d", got, SrcOriginY, BorderTop, FrameGapY)
	}
	if got := BorderBot - (SrcOriginY + 8*SrcSquareH); got != FrameGapY {
		bad("bottom gap is %d rows (grid ends at y=%d, rule at %d) but FrameGapY=%d",
			got, SrcOriginY+8*SrcSquareH-1, BorderBot, FrameGapY)
	}

	// --- The tile is the square, and the stored columns reach the ink. -
	if TileH != SrcSquareH-SrcTrimTop {
		bad("TileH=%d but the source square is %d rows with %d trimmed", TileH, SrcSquareH, SrcTrimTop)
	}
	if TileW != SrcSquareW {
		bad("TileW=%d but the source square is %d dots wide: the tile no longer covers the square",
			TileW, SrcSquareW)
	}
	if TileW%14 != 0 || SrcOriginX%2 != 0 || SrcSquareW%2 != 0 {
		bad("the dither phase needs TileW %% 14 == 0 and even SrcOriginX/SrcSquareW; got %d, %d, %d",
			TileW, SrcOriginX, SrcSquareW)
	}
	lo, hi, contiguous := StoredDotSpan()
	if !contiguous {
		bad("stored byte columns %d..%d cover a NON-contiguous dot span %d..%d", FirstCol, LastCol, lo, hi)
	}
	if lo > ContentMinDX || hi < ContentMaxDX {
		bad("stored byte columns cover dx %d..%d, which does not contain the declared content window dx %d..%d",
			lo, hi, ContentMinDX, ContentMaxDX)
	}

	// --- The pixels agree: the frame's inner margin is empty. ----------
	for i, g := range FrameGaps() {
		name := []string{"left", "right", "top", "bottom"}[i]
		for y := g[1]; y <= g[3]; y++ {
			for x := g[0]; x <= g[2]; x++ {
				if s.At(x, y) {
					bad("the %s gap between the frame and the grid is lit at (%d,%d): "+
						"nothing may be drawn between the frame and the 8x8 grid", name, x, y)
					if len(out) > maxGapFindings {
						return out
					}
				}
			}
		}
	}
	return out
}

// maxGapFindings caps the gap sweep: a frame drawn one dot off would
// otherwise report thousands of identical dots.
const maxGapFindings = 24

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
