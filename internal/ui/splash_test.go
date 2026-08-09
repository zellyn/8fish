package ui_test

// splash_test.go gates the BOOT TITLE CARD (asm/m8.s m8splash) on the SHIPPING
// DISK: it boots asm/8fish.dsk from the Disk II boot ROM, the same path a real
// machine takes, and proves the hand-drawn splash is read off the disk and
// decoded by the 6502 PackBits decoder straight into DHGR page 1 -- and then,
// the part this file exists for, that the splash STAYS UP through the whole
// ~9 s big-book load and only wipes to the board once the book is loaded and
// verified. That only works because the load reads each 8 KB file DIRECTLY
// INTO AUX ($4000+) and never stages through main $2000, the splash's DHGR
// main half (docs/loadcover-feasibility.md).

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/splash"
	"github.com/zellyn/chess6502/internal/ui"
)

const splashBudget = 2_000_000_000

// TestDiskSplashShowsThenAdvances is the end-to-end splash-covers-the-load gate.
func TestDiskSplashShowsThenAdvances(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots a disk and loads 32 KB under the splash")
	}
	dsk := dskPath(t)
	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	m8lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	m8bigbook, mbbverify := m8lbl["m8bigbook"], m8lbl["mbbverify"]
	if m8bigbook == 0 || mbbverify == 0 {
		t.Fatal("labels m8bigbook / mbbverify missing from m8.lbl")
	}

	asset, err := os.ReadFile(filepath.Join(root, "assets", "fish8-splash-dazzledraw-save.bin"))
	if err != nil {
		t.Fatal(err)
	}
	splashDiff := func(got []byte) string {
		n, first := 0, -1
		for i := range asset {
			if got[i] != asset[i] {
				if first < 0 {
					first = i
				}
				n++
			}
		}
		if first < 0 {
			return "no differences"
		}
		return fmt.Sprintf("%d of %d bytes differ; first at $%04X (bank/off): got $%02X want $%02X",
			n, len(asset), first, got[first], asset[first])
	}

	// ---- 1. as the load BEGINS, the splash is up, full-screen, correct ------
	// m8bigbook is reached on the boot path with no keypress: m8splash showed
	// the logo full-screen and returned WITHOUT holding. It is a full 192-line
	// image, so unlike the board it must NOT carry the four-row text window
	// (Mixed=false; MIXCLR in m8splash).
	if ok, err := m.RunUntilPC(m8bigbook, splashBudget); err != nil || !ok {
		t.Fatalf("boot never reached m8bigbook (ok=%v err=%v PC $%04X)", ok, err, m.CPU.PC())
	}
	if m.Mem.Mixed || m.Mem.Text || !m.Mem.DHires() {
		t.Fatalf("at load start the splash is not full-screen DHGR: Mixed=%v Text=%v "+
			"DHires=%v (want false/false/true)", m.Mem.Mixed, m.Mem.Text, m.Mem.DHires())
	}
	got := dhgrScreen(m)
	if !bytes.Equal(got, asset) {
		t.Fatalf("the splash in DHGR page 1 differs from the asset: %s — the disk read, "+
			"the aux lift, or the 6502 PackBits decoder is wrong", splashDiff(got))
	}
	want, err := splash.Decode(splash.Blob())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the on-screen splash is not splash.Decode(splash.Blob())")
	}
	t.Logf("splash decoded into DHGR page 1 (%d B) matches the asset; load beginning", len(got))

	// ---- 2. the splash SURVIVES the whole aux-direct load -------------------
	// Run to the checksum verify: by then all four 8 KB files have been read
	// STRAIGHT INTO AUX ($4000-$BFFF). Had the read staged through main $2000
	// (the DHGR main half) as it used to, the splash would be shredded here.
	// It is byte-for-byte intact -- the load never touched DHGR page 1, which
	// is the entire point of the allow_aux change.
	if ok, err := m.RunUntilPC(mbbverify, splashBudget); err != nil || !ok {
		t.Fatalf("the load never reached mbbverify (ok=%v err=%v PC $%04X)", ok, err, m.CPU.PC())
	}
	if m.Mem.Mixed || m.Mem.Text || !m.Mem.DHires() {
		t.Fatalf("after the 4-file load the splash is no longer full-screen DHGR: "+
			"Mixed=%v Text=%v DHires=%v", m.Mem.Mixed, m.Mem.Text, m.Mem.DHires())
	}
	if got := dhgrScreen(m); !bytes.Equal(got, asset) {
		t.Fatalf("the load SHREDDED the splash: %s — the aux-direct read touched DHGR "+
			"page 1 (it must land only in aux $4000+)", splashDiff(got))
	}
	if a, b := m.Mem.Aux[book.BigBase], m.Mem.Aux[book.BigBase+1]; a != 'B' || b != 'K' {
		t.Fatalf("aux $%04X lacks the 'BK' magic after the load: $%02X $%02X — the "+
			"aux-direct read did not land the book", book.BigBase, a, b)
	}
	t.Log("splash byte-identical after all four files loaded straight to aux; book present")

	// ---- 3. the load auto-advances to the board; the big book verified ------
	if ok, err := m.RunToKeyboard(splashBudget); err != nil || !ok {
		t.Fatalf("the load never advanced to the board prompt (ok=%v err=%v)", ok, err)
	}
	if m.Mem.Text || !m.Mem.Mixed {
		t.Fatalf("the board is not up (want MIXED DHGR): Mixed=%v Text=%v", m.Mem.Mixed, m.Mem.Text)
	}
	if bytes.Equal(dhgrScreen(m), asset) {
		t.Fatal("still the splash after the load: the boot never wiped to the board")
	}
	if got := m.Mem.Main[ui.BIGBOOKOK]; got != 1 {
		t.Fatalf("BIGBOOKOK = %d after the load, want 1 (the big book did not verify)", got)
	}
	// The board really painted (not just the mode flags): its coordinate row is
	// on screen.
	var joined string
	for r := range 24 {
		joined += m.Screen().Text(r) + "\n"
	}
	if !bytes.Contains(bytes.ToLower([]byte(joined)), []byte("a b c d e f g h")) {
		t.Fatalf("the board's coordinate row is not on screen after the load:\n%v", m.Screen())
	}
	t.Log("splash covered the whole load; board up and painted, big book verified (BIGBOOKOK=1)")
}

// TestDiskSplashAuxreqPoisonedStillShows guards the one hardware defect the
// zeroed-RAM emulator structurally cannot see. With allow_aux=1 the driver
// reads its auxreq flag ($51, inside the swapped zp window) on EVERY call. Its
// held image is RWTSHOLD ($F7BF) + ($51-$3C)=$15 -> $F7D4, which nothing at
// boot initialises -- and after any big-book load m8bigbook leaves auxreq=1 in
// the driver zp, which rwtszp writes back to $F7D4, and Ctrl-Reset does NOT
// wipe it. So on every boot AFTER the first, m8splash would inherit auxreq=1,
// read the splash into AUX instead of main $2000, fail its '8F' magic, and load
// under a black screen. m8splash must force RWTS_AUXREQ=0. Zeroed emulator RAM
// makes $F7D4=0 and hides this, so we POISON it and require the splash anyway.
func TestDiskSplashAuxreqPoisonedStillShows(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots a disk")
	}
	dsk := dskPath(t)
	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	// The auxreq hold byte: RWTSHOLD $F7BF + ($51-$3C). Poison it with 1 --
	// exactly what a prior m8bigbook leaves there, and the only value that bites
	// (the driver's setaux does `sta CLRAUXRD,x` with x=auxreq, so only x==1
	// lands on the real $C003/$C005 aux switches; other values miss).
	const auxreqHold = 0xF7BF + (0x51 - 0x3C)
	m.Mem.Main[auxreqHold] = 1

	m8lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	m8bigbook := m8lbl["m8bigbook"]
	if m8bigbook == 0 {
		t.Fatal("label m8bigbook missing from m8.lbl")
	}
	// Run to the START of the load: m8splash has run. With the fix it forced
	// auxreq=0, read the splash to main $2000, and decoded it. Without the fix
	// the poisoned auxreq sent the read to aux, the magic failed, and the
	// screen was cleared to black.
	if ok, err := m.RunUntilPC(m8bigbook, splashBudget); err != nil || !ok {
		t.Fatalf("boot never reached m8bigbook (ok=%v err=%v PC $%04X)", ok, err, m.CPU.PC())
	}
	asset, err := os.ReadFile(filepath.Join(root, "assets", "fish8-splash-dazzledraw-save.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dhgrScreen(m), asset) {
		t.Fatal("with the auxreq hold byte ($F7D4) poisoned, the splash did NOT show: " +
			"m8splash must store 0 to RWTS_AUXREQ so the read lands in main $2000, not aux. " +
			"Without it the splash reads into aux, fails its magic, and every boot after the " +
			"first loads under a black screen (a real-hardware defect the zeroed emulator hides).")
	}
	t.Log("splash shows despite a poisoned auxreq hold byte: m8splash forces auxreq=0")
}
