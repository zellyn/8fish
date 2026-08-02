package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/tiles"
)

// artFile returns the committed artwork's bytes and an ABSOLUTE path to
// it. Absolute because these tests chdir into a scratch directory to prove
// -check writes nothing.
func artFile(t *testing.T) []byte {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "assets", "chess-dazzledraw-save.bin"))
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

// TestCheckOnCommittedArtwork: the committed artwork breaks no geometric
// assumption, so -check never reports BROKEN, and its exit status agrees
// with what it printed. Deliberately NOT pinned to today's 28 clipped
// dots — that number lives in one place, internal/tiles' TestTrimCosts —
// so a successful redraw turns this green instead of red.
func TestCheckOnCommittedArtwork(t *testing.T) {
	code, out := runCheckOn(t, writeArt(t, artFile(t)))
	if code == exitBroken || strings.Contains(out, "RESULT: BROKEN") {
		t.Errorf("committed artwork reported as broken (exit %d):\n%s", code, out)
	}
	for _, want := range []string{"CLIPPED INK", "GEOMETRY CHECKS", "INK EXTENTS", "SUMMARY", "blob: 1824 bytes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	// Exit status and headline must tell the same story.
	clean := strings.Contains(out, "NONE. Every drawn dot survives the trim")
	if want := map[bool]int{true: exitOK, false: exitClipped}[clean]; code != want {
		t.Errorf("exit = %d but the clipped-ink headline says clean=%v", code, clean)
	}
}

// TestCheckCatchesInkInTheTrimmedRows: a dot drawn in source row dy 0 is
// ink the tile throws away. The checker must exit non-zero and name the
// square it is on.
func TestCheckCatchesInkInTheTrimmedRows(t *testing.T) {
	// a1 is a dark square (all-dark background) whose ink starts at dy 4,
	// and dx 20 is inside the content window: a pure clip, nothing else.
	art := artFile(t)
	x, y := tiles.SrcDotPos(7, 0, 0, 20)
	tiles.SetDot(art, x, y)

	code, out := runCheckOn(t, writeArt(t, art))
	if code == exitOK {
		t.Fatalf("exit = 0 for artwork with ink in a trimmed row:\n%s", out)
	}
	if code != exitClipped {
		t.Errorf("exit = %d, want %d (a clip, not a structural break)\n%s", code, exitClipped, out)
	}
	if !namesSquareInClipSection(out, "a1") {
		t.Errorf("the clipped-ink section does not name a1:\n%s", out)
	}
	if !strings.Contains(out, "dy=0") {
		t.Errorf("output does not report the offending row dy=0:\n%s", out)
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

// namesSquareInClipSection reports whether the CLIPPED INK section (not
// some later table) names the square, so a hit in the ink-extent listing
// cannot make the test pass vacuously.
func namesSquareInClipSection(out, sq string) bool {
	start := strings.Index(out, "CLIPPED INK")
	if start < 0 {
		return false
	}
	end := strings.Index(out, "GEOMETRY CHECKS")
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
