package tiles

import (
	"bytes"
	"strings"
	"testing"
)

// TestCheckAgreesWithTrimCosts: Check's clipped-ink total, measured by
// comparing each square against a SAME-SHADE EMPTY square, must equal the
// analytic measurement (deviation from bgDot) that TestTrimCosts uses.
// Two independent definitions of "ink", one number: if the empty-square
// reference were the wrong shade — the easy mistake — every light square
// would read as solid ink and this would blow up by three orders of
// magnitude.
func TestCheckAgreesWithTrimCosts(t *testing.T) {
	art := loadArt(t)
	rep, err := Check(art)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rep.ClippedTotal, BoardDeviations(loadScreen(t), 0, SrcTrimTop-1, 0, SrcSquareW-1); got != want {
		t.Errorf("Check reports %d clipped dots, analytic measurement says %d", got, want)
	}
	// The whole trim curve, cross-checked the same way. TestTrimCosts owns
	// the absolute numbers; this owns the agreement between the two
	// measurements at every trim depth.
	s := loadScreen(t)
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
	if rep.Clean() != (rep.ClippedTotal == 0) {
		t.Errorf("Clean() = %v with %d clipped dots and no violations", rep.Clean(), rep.ClippedTotal)
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

// TestCheckCatchesClippedInk: a dot drawn in the trimmed rows of a square
// that is clean today must show up as clipped ink, named by square.
func TestCheckCatchesClippedInk(t *testing.T) {
	// a1 (r=7,f=0) is a dark square whose ink starts at dy 4 today, and
	// dx 20 is inside the content window, so this dot is PURELY a clip.
	broken := mustSetDot(t, loadArt(t), 7, 0, 0, 20)

	rep, err := Check(broken)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ClippedTotal != 29 {
		t.Errorf("clipped total = %d, want 29 (the 28 in the artwork plus the one we drew)", rep.ClippedTotal)
	}
	var found *SquareInk
	for i, sq := range rep.Clipped {
		if sq.R == 7 && sq.F == 0 {
			found = &rep.Clipped[i]
		}
	}
	if found == nil {
		t.Fatal("Check did not report clipped ink on a1")
	}
	if len(found.Dots) != 1 || found.Dots[0] != (Dot{DX: 20, DY: 0}) {
		t.Errorf("a1 clipped dots = %v, want exactly [{20 0}]", found.Dots)
	}
	// A clip is not a structural break: the artwork still slices.
	for _, v := range rep.Violations {
		t.Errorf("a clipped dot produced a geometry finding: %s", v)
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
		// Only ONE pawn of the four same-shade pawns in row 6.
		{"pawn row", 6, 2, 3, 21, KindPawnRow, "c2", false},
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
