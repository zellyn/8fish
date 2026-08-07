package tiles

import (
	"bytes"
	"strings"
	"testing"
)

// TestCheckAgreesWithTheAnalyticMeasurement: Check measures ink by
// comparing each square against a SAME-SHADE EMPTY square; BoardDeviations
// measures it analytically, against the dither formula. Two independent
// definitions, and every number the report prints has to come out the same
// under both. If the empty-square reference were the wrong shade — the
// easy mistake — every light square would read as solid ink and this would
// blow up by three orders of magnitude.
//
// It also pins the committed artwork's goal state: nothing lost, nothing
// broken, and the blob in the tree is the blob the artwork slices to.
func TestCheckAgreesWithTheAnalyticMeasurement(t *testing.T) {
	art := loadArt(t)
	s := loadScreen(t)
	rep, err := Check(art)
	if err != nil {
		t.Fatal(err)
	}

	// LOST ink, both halves, against the analytic measurement.
	y0, y1, x0, x1 := KeptWindow()
	total := BoardDeviations(s, 0, SrcSquareH-1, 0, SrcSquareW-1)
	if got, want := rep.LostTotal, total-BoardDeviations(s, y0, y1, x0, x1); got != want {
		t.Errorf("Check reports %d lost dots, analytic measurement says %d", got, want)
	}
	if rep.LostTotal != 0 {
		t.Errorf("the committed artwork loses %d dots; the redraw's whole point is that it loses none", rep.LostTotal)
	}
	// The row half of the split can no longer fail (SrcTrimTop is 0). Say
	// so as an assertion rather than letting a zero look like a passing
	// check: if a trim is ever reinstated this fails and has to be re-read.
	if SrcTrimTop == 0 && rep.LostRows != 0 {
		t.Errorf("LostRows = %d with no trim; the row half of the split must be structurally zero", rep.LostRows)
	}

	// The board-wide ink box: the number a redraw actually works against.
	if rep.InkMinDX < ContentMinDX || rep.InkMaxDX > ContentMaxDX {
		t.Errorf("ink spans dx %d..%d, outside the declared window %d..%d",
			rep.InkMinDX, rep.InkMaxDX, ContentMinDX, ContentMaxDX)
	}
	if rep.InkMinDY < 0 || rep.InkMaxDY > SrcSquareH-1 {
		t.Errorf("ink spans dy %d..%d, outside the square 0..%d", rep.InkMinDY, rep.InkMaxDY, SrcSquareH-1)
	}

	// The what-if trim curve, cross-checked the same way. It gates nothing
	// now, but a diagnostic that quietly disagrees with the measurement it
	// claims to report is worse than no diagnostic.
	for n := 1; n < len(rep.TrimCosts); n++ {
		if got, want := rep.TrimCosts[n], BoardDeviations(s, 0, n-1, 0, SrcSquareW-1); got != want {
			t.Errorf("top-%d trim cost = %d, analytic says %d", n, got, want)
		}
	}
	if got, want := rep.BottomCost, BoardDeviations(s, SrcSquareH-1, SrcSquareH-1, 0, SrcSquareW-1); got != want {
		t.Errorf("bottom-row cost = %d, analytic says %d", got, want)
	}

	// The committed artwork slices: no geometry finding at all.
	for _, v := range rep.Violations {
		t.Errorf("committed artwork has a geometry finding: %s", v)
	}
	if rep.Blob == nil {
		t.Fatal("Check did not produce a blob for the committed artwork")
	}
	if !bytes.Equal(rep.Blob, DefaultBlob()) {
		t.Error("Check's blob differs from the committed one")
	}
	if len(rep.Changed) != 0 {
		t.Errorf("Check reports %v changed vs the committed blob", rep.Changed)
	}
	if !rep.Clean() {
		t.Errorf("Clean() = false with %d lost dots and %d violations", rep.LostTotal, len(rep.Violations))
	}
}

// TestRefEmptyIsSameShade: the reference square an ink measurement is
// taken against must match the measured square's shade, and must really
// be blank. This is the assumption the whole measurement rests on.
func TestRefEmptyIsSameShade(t *testing.T) {
	s := loadScreen(t)
	empties := map[[2]int]bool{}
	for _, sq := range EmptySquares() {
		empties[sq] = true
	}
	for r := range 8 {
		for f := range 8 {
			er, ef := refEmpty(r, f)
			if Light(er, ef) != Light(r, f) {
				t.Fatalf("(%s): reference empty square %s has the wrong shade", Square(r, f), Square(er, ef))
			}
			if !empties[[2]int{er, ef}] {
				t.Fatalf("(%s): reference square %s is not one of the drawn-empty squares",
					Square(r, f), Square(er, ef))
			}
		}
	}
	// And an empty square measures as zero ink against itself.
	for _, sq := range EmptySquares() {
		if n := inkIn(s, sq[0], sq[1], 0, SrcSquareH-1, 0, SrcSquareW-1).Count(); n != 0 {
			t.Errorf("empty square %s measures %d ink dots", Square(sq[0], sq[1]), n)
		}
	}
}

// TestCheckCatchesLostInk: a dot drawn in a square's right MARGIN is a dot
// the 4-byte tile row cannot carry. It must be reported as lost, named by
// square, and counted in the DOT-COLUMN half of the split.
//
// This is the replacement for TestCheckCatchesClippedInk, which drew into
// a trimmed row. With SrcTrimTop at 0 there are no trimmed rows, so that
// test could no longer fail for the reason it claimed — it would have
// become a passing test of nothing.
func TestCheckCatchesLostInk(t *testing.T) {
	// a1 (r=7,f=0) is a dark square, so any dot is background-dark and
	// lighting one is a real change. dx 40 is past ContentMaxDX and past
	// the stored dot span, i.e. genuinely unreachable by any tile.
	const dx, dy = 40, 10
	if _, _, _, x1 := KeptWindow(); dx <= x1 {
		t.Fatalf("dx %d is inside the kept dot span (..%d); this test would prove nothing", dx, x1)
	}
	broken := mustSetDot(t, loadArt(t), 7, 0, dy, dx)

	rep, err := Check(broken)
	if err != nil {
		t.Fatal(err)
	}
	if rep.LostTotal != 1 {
		t.Errorf("lost total = %d, want 1 (the committed artwork loses none, plus the one we drew)", rep.LostTotal)
	}
	if rep.LostCols != 1 || rep.LostRows != 0 {
		t.Errorf("lost split = %d rows / %d cols, want 0 / 1", rep.LostRows, rep.LostCols)
	}
	var found *SquareInk
	for i, sq := range rep.Lost {
		if sq.R == 7 && sq.F == 0 {
			found = &rep.Lost[i]
		}
	}
	if found == nil {
		t.Fatal("Check did not report lost ink on a1")
	}
	if len(found.Dots) != 1 || found.Dots[0] != (Dot{DX: dx, DY: dy}) {
		t.Errorf("a1 lost dots = %v, want exactly [{%d %d}]", found.Dots, dx, dy)
	}
	if rep.Clean() {
		t.Error("Clean() is true for artwork that loses a dot")
	}
}

// TestCheckCatchesInkInTheFrameGap: the blank margin between the drawn
// frame and the 8x8 grid is what ties the declared grid to the drawing.
// A dot there means the frame, the grid, or a piece has moved.
func TestCheckCatchesInkInTheFrameGap(t *testing.T) {
	gaps := FrameGaps()
	if len(gaps) != 4 {
		t.Fatalf("FrameGaps() returned %d rectangles, want 4", len(gaps))
	}
	for i, name := range []string{"left", "right", "top", "bottom"} {
		t.Run(name, func(t *testing.T) {
			g := gaps[i]
			// The middle of the gap, so the dot cannot be confused with the
			// frame itself or with a square.
			x, y := (g[0]+g[2])/2, (g[1]+g[3])/2
			art := bytes.Clone(loadArt(t))
			if s, err := Decode(art); err != nil {
				t.Fatal(err)
			} else if s.At(x, y) {
				t.Fatalf("gap dot (%d,%d) is already lit; this mutation would change nothing", x, y)
			}
			SetDot(art, x, y)

			rep, err := Check(art)
			if err != nil {
				t.Fatal(err)
			}
			var hit bool
			for _, v := range rep.Violations {
				if v.Kind == KindGrid && strings.Contains(v.Detail, name+" gap") {
					hit = true
				}
			}
			if !hit {
				t.Errorf("a lit dot at (%d,%d) in the %s frame gap produced no [grid] finding; got:\n  %s",
					x, y, name, strings.Join(violationStrings(rep.Violations), "\n  "))
			}
			if rep.Clean() {
				t.Error("Clean() is true for artwork with ink outside the grid")
			}
		})
	}
}

// TestCheckCatchesGeometryViolations: each structural assumption, broken
// on purpose, is reported — as a LIST, with the square named, and without
// stopping at the first one.
func TestCheckCatchesGeometryViolations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		r, f     int
		dy, dx   int
		wantKind string
		wantSq   string
		// buildFails: whether Build ALSO refuses. Most breaks stop the
		// slice outright; a pawn-row inconsistency does not — the blob
		// still builds, it just no longer matches what is drawn. That gap
		// is exactly why the checker exists alongside Build.
		buildFails bool
	}{
		// dx 40 is past ContentMaxDX (34) and lands in byte column 5.
		{"right margin", 7, 0, 10, 40, KindWindow, "a1", true},
		// dx 3 is before ContentMinDX (8) and lands in byte column 0.
		{"left margin", 7, 0, 10, 3, KindWindow, "a1", true},
		// (r=3,f=3) is one of the 28 squares the artwork leaves blank.
		// dx must be ODD on a light square: the even dots are already lit
		// by the dither, so setting one would be a no-op.
		{"empty square", 3, 3, 10, 21, KindEmptySq, "d5", true},
		// Only ONE pawn of the four same-shade pawns in row 6. dy 10 / dx 16
		// is a dark dot inside c2's pawn body: inside the content window, so
		// nothing but the pawn-row check can object to it. (mustSetDot fails
		// loudly if a redraw ever lights it, rather than silently mutating
		// nothing.)
		{"pawn row", 6, 2, 10, 16, KindPawnRow, "c2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := mustSetDot(t, loadArt(t), tc.r, tc.f, tc.dy, tc.dx)

			rep, err := Check(broken)
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Violations) == 0 {
				t.Fatal("Check reported no geometry findings")
			}
			if rep.Clean() {
				t.Error("Clean() is true for artwork that does not slice")
			}
			var hit bool
			for _, v := range rep.Violations {
				if v.Kind == tc.wantKind && strings.Contains(v.Where, tc.wantSq) {
					hit = true
				}
			}
			if !hit {
				t.Errorf("no %s finding naming %s; got:\n  %s",
					tc.wantKind, tc.wantSq, strings.Join(violationStrings(rep.Violations), "\n  "))
			}
			// Where Build also refuses, Check must agree and hand back no
			// blob; where it does not, the checker is the only gate.
			_, buildErr := Build(broken)
			if gotFail := buildErr != nil; gotFail != tc.buildFails {
				t.Errorf("Build failed = %v, want %v (err: %v)", gotFail, tc.buildFails, buildErr)
			}
			if (rep.Blob == nil) != tc.buildFails {
				t.Errorf("Check blob == nil is %v, want %v", rep.Blob == nil, tc.buildFails)
			}
		})
	}
}

// TestCheckReportsEveryProblemAtOnce is the whole point of the tool: two
// independent breaks on two different squares must BOTH appear, where
// Build would have stopped at the first.
func TestCheckReportsEveryProblemAtOnce(t *testing.T) {
	broken := loadArt(t)
	for _, d := range []struct{ r, f, dy, dx int }{
		{7, 0, 10, 40}, // a1, right margin
		{0, 7, 10, 3},  // h8, left margin
		{3, 3, 10, 21}, // d5, an empty square (odd dx: it is a LIGHT square)
	} {
		broken = mustSetDot(t, broken, d.r, d.f, d.dy, d.dx)
	}
	rep, err := Check(broken)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(violationStrings(rep.Violations), "\n")
	for _, sq := range []string{"a1", "h8", "d5"} {
		if !strings.Contains(all, sq) {
			t.Errorf("square %s missing from the findings:\n%s", sq, all)
		}
	}
}

// TestCheckDoesNotMutateItsInput: the artwork is the single source of
// truth, and Check is a read-only pass over a copy of it.
func TestCheckDoesNotMutateItsInput(t *testing.T) {
	art := loadArt(t)
	before := bytes.Clone(art)
	if _, err := Check(art); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(art, before) {
		t.Error("Check modified the artwork bytes it was handed")
	}
}

// TestSquareNaming: (r,f) reads as algebraic notation, r=0 being rank 8.
func TestSquareNaming(t *testing.T) {
	for _, tc := range []struct {
		r, f int
		want string
	}{{0, 0, "a8"}, {7, 0, "a1"}, {0, 7, "h8"}, {7, 7, "h1"}, {5, 3, "d3"}} {
		if got := Square(tc.r, tc.f); got != tc.want {
			t.Errorf("Square(%d,%d) = %q, want %q", tc.r, tc.f, got, tc.want)
		}
	}
	if got, want := PieceAt(0, 0), "black rook on light"; got != want {
		t.Errorf("PieceAt(0,0) = %q, want %q", got, want)
	}
	if got := PieceAt(3, 3); got != "" {
		t.Errorf("PieceAt on an empty square = %q, want %q", got, "")
	}
}

// mustSetDot returns a COPY of art with square-local dot (dx,dy) of
// square (r,f) lit, failing if that dot is already lit — a SetDot that
// changes nothing would make the test that depends on it vacuous.
func mustSetDot(t *testing.T, art []byte, r, f, dy, dx int) []byte {
	t.Helper()
	out := bytes.Clone(art)
	x, y := SrcDotPos(r, f, dy, dx)
	s, err := Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	if s.At(x, y) {
		t.Fatalf("%s dot (dx=%d,dy=%d) is already lit; this test needs a dark dot to light",
			Square(r, f), dx, dy)
	}
	SetDot(out, x, y)
	s, err = Decode(out)
	if err != nil {
		t.Fatal(err)
	}
	if !s.At(x, y) {
		t.Fatalf("SetDot failed to light %s dot (dx=%d,dy=%d)", Square(r, f), dx, dy)
	}
	return out
}

func violationStrings(vs []Violation) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.String()
	}
	return out
}
