package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/tiles"
)

// artFile returns the committed artwork's bytes and an ABSOLUTE path to
// it. Absolute because these tests chdir into a scratch directory to prove
// -check writes nothing.
func artFile(t *testing.T) []byte {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "assets", "chess2-dazzledraw-save.bin"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// writeArt drops an artwork image in a scratch dir and returns its path.
// Nothing here ever touches assets/: the drawing is the single source of
// truth and is edited only in DazzleDraw.
func writeArt(t *testing.T, art []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "art.bin")
	if err := os.WriteFile(path, art, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runCheckOn runs `gentiles -check -art <path>` in an EMPTY working
// directory and returns the exit status and everything it printed. The
// empty cwd is the assertion that check mode writes no outputs: the
// generator's three output paths are relative, so any write would land
// here and be visible.
func runCheckOn(t *testing.T, artPath string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	code := run([]string{"-check", "-art", artPath}, &out, &errOut)

	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		t.Errorf("-check wrote %s; check mode must write no outputs", e.Name())
	}
	return code, out.String() + errOut.String()
}

// TestCheckOnCommittedArtwork is the GATE, not just a smoke test: `make
// test` runs it, so a redraw that breaks an assumption the tile format
// rests on fails here rather than on an Apple IIe.
//
// It requires exit 0 exactly. Before the CHESS2 redraw the committed
// artwork clipped 28 dots and this test had to tolerate exit 1, which
// meant the tool's own headline could not be asserted. The artwork is now
// in the goal state, so the gate can demand it — and if a future redraw
// loses a single dot, this goes red.
func TestCheckOnCommittedArtwork(t *testing.T) {
	code, out := runCheckOn(t, writeArt(t, artFile(t)))
	if code != exitOK {
		t.Errorf("committed artwork: exit = %d, want %d (clean)\n%s", code, exitOK, out)
	}
	if strings.Contains(out, "RESULT: BROKEN") {
		t.Errorf("committed artwork reported as broken:\n%s", out)
	}
	for _, want := range []string{
		"LOST INK", "NONE. Every drawn dot reaches a tile",
		"GRID FIT", "GEOMETRY CHECKS", "INK EXTENTS", "SUMMARY",
		"blob: 1824 bytes", "RESULT: clean",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	// Every assumption the checker knows about must actually have run and
	// held — "all N assumptions hold" listing a SHORTER list than
	// tiles.CheckKinds would be a check quietly dropped.
	for _, kind := range tiles.CheckKinds {
		if !strings.Contains(out, kind) {
			t.Errorf("the report never mentions the %q check:\n%s", kind, out)
		}
	}
	// The committed blob is the blob this artwork slices to: if they had
	// drifted, `make dsk` and the checker would disagree about the board.
	if strings.Contains(out, "DIFFER from the committed") {
		t.Errorf("the committed blob is stale; run `go run ./cmd/gentiles`:\n%s", out)
	}
}

// TestCheckCatchesInkInTheFrameGap: ink between the drawn frame and the
// 8x8 grid means the grid the code declares is not the grid that was
// drawn. Exit 2, and the report has to say where.
//
// This replaced TestCheckCatchesInkInTheTrimmedRows. That test drew into
// source row dy 0 and expected the tool to call it clipped; with
// SrcTrimTop at 0, dy 0 is a perfectly good tile row and the test could
// only have passed by asserting nothing.
func TestCheckCatchesInkInTheFrameGap(t *testing.T) {
	art := artFile(t)
	g := tiles.FrameGaps()[0] // the left gap, between the left bar and file a
	x, y := (g[0]+g[2])/2, (g[1]+g[3])/2
	tiles.SetDot(art, x, y)

	code, out := runCheckOn(t, writeArt(t, art))
	if code != exitBroken {
		t.Errorf("exit = %d, want %d\n%s", code, exitBroken, out)
	}
	for _, want := range []string{"[grid]", "left gap", "RESULT: BROKEN"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

// TestCheckCatchesLostInk: a dot in a square's margin cannot be carried by
// the 4-byte tile row. The LOST INK headline must name the square, and the
// tool must exit non-zero.
func TestCheckCatchesLostInk(t *testing.T) {
	art := artFile(t)
	x, y := tiles.SrcDotPos(7, 0, 10, 40) // a1, dx 40: past the stored dot span
	tiles.SetDot(art, x, y)

	code, out := runCheckOn(t, writeArt(t, art))
	if code != exitBroken {
		t.Errorf("exit = %d, want %d\n%s", code, exitBroken, out)
	}
	if !namesSquareInLostSection(out, "a1") {
		t.Errorf("the LOST INK section does not name a1:\n%s", out)
	}
	if !strings.Contains(out, "dy=10") {
		t.Errorf("output does not report the offending row dy=10:\n%s", out)
	}
}

// TestCheckCatchesInkOutsideTheContentWindow: a dot past ContentMaxDX
// breaks the assumption that only byte columns 1..4 need storing. Exit 2,
// square named.
func TestCheckCatchesInkOutsideTheContentWindow(t *testing.T) {
	art := artFile(t)
	x, y := tiles.SrcDotPos(7, 0, 10, 40) // a1, dx 40 > ContentMaxDX (34)
	tiles.SetDot(art, x, y)

	code, out := runCheckOn(t, writeArt(t, art))
	if code != exitBroken {
		t.Errorf("exit = %d, want %d\n%s", code, exitBroken, out)
	}
	for _, want := range []string{
		"[content-window] a1",
		"dx=40",
		"RESULT: BROKEN",
		"WOULD NOT SLICE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

// TestCheckListsEveryProblem: Build stops at the first broken assumption;
// -check must name all three squares in one run.
func TestCheckListsEveryProblem(t *testing.T) {
	art := artFile(t)
	for _, d := range []struct{ r, f, dy, dx int }{
		{7, 0, 10, 40}, // a1, right margin
		{0, 7, 10, 3},  // h8, left margin
		{3, 3, 10, 21}, // d5, an empty square (odd dx: d5 is LIGHT)
	} {
		x, y := tiles.SrcDotPos(d.r, d.f, d.dy, d.dx)
		tiles.SetDot(art, x, y)
	}
	code, out := runCheckOn(t, writeArt(t, art))
	if code != exitBroken {
		t.Errorf("exit = %d, want %d\n%s", code, exitBroken, out)
	}
	for _, sq := range []string{"a1", "h8", "d5"} {
		if !strings.Contains(out, "] "+sq+":") {
			t.Errorf("no finding names %s:\n%s", sq, out)
		}
	}
}

// TestCheckRejectsUnreadableArtwork: a truncated or missing image is a
// hard failure, not a clean bill of health.
func TestCheckRejectsUnreadableArtwork(t *testing.T) {
	if code, out := runCheckOn(t, writeArt(t, artFile(t)[:100])); code != exitBroken {
		t.Errorf("truncated artwork: exit = %d, want %d\n%s", code, exitBroken, out)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	if code := run([]string{"-check", "-art", filepath.Join(dir, "nope.bin")}, &out, &errOut); code != exitBroken {
		t.Errorf("missing artwork: exit = %d, want %d", code, exitBroken)
	}
}

// TestCheckWritesThePNG: -png renders the post-slice tiles, and that is
// the ONLY file check mode is allowed to create.
func TestCheckWritesThePNG(t *testing.T) {
	artPath := writeArt(t, artFile(t))
	dir := t.TempDir()
	t.Chdir(dir)
	png := filepath.Join(dir, "tiles.png")

	var out, errOut bytes.Buffer
	code := run([]string{"-check", "-art", artPath, "-png", png}, &out, &errOut)
	if code == exitBroken {
		t.Errorf("exit = %d for the committed artwork\n%s%s", code, out.String(), errOut.String())
	}
	info, err := os.Stat(png)
	if err != nil {
		t.Fatalf("no PNG written: %v", err)
	}
	if info.Size() == 0 {
		t.Error("PNG is empty")
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Errorf("check mode created %d files, want only the PNG", len(ents))
	}
}

// namesSquareInLostSection reports whether the LOST INK section (not some
// later table) names the square, so a hit in the ink-extent listing cannot
// make the test pass vacuously.
func namesSquareInLostSection(out, sq string) bool {
	start := strings.Index(out, "LOST INK")
	if start < 0 {
		return false
	}
	end := strings.Index(out, "GRID FIT")
	if end < 0 || end < start {
		end = len(out)
	}
	for _, line := range strings.Split(out[start:end], "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), sq+" ") {
			return true
		}
	}
	return false
}
