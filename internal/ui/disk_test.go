package ui_test

// disk_test.go gates the SHIPPING ARTEFACT: asm/8fish.dsk. Three static
// gates on how the disk is built, and one that boots it.
//
// The margins matter more here than anywhere else in the project, because
// there are only ~744 bytes of total slack and they are split between two
// budgets that grow from opposite ends:
//
//	SD spare        the engine spends it (the image ends at engine.bin's
//	                last byte, and diskii mksd refuses over 45,056 B)
//	UI growth room  the UI spends it (the staged payload must end below
//	                the resident opening book at $2000)
//
// Raising the staging base trades one for the other 256 bytes at a time, and
// at $0C00 there is nowhere left to go: $0F00 would already overrun the book.
// So both numbers are asserted, both are printed on every run, and the day
// either goes negative this test says which one and by how much.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/delivery"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/ui"
)

// dskPath is where TestMain-built pieces are assembled into a disk.
func dskPath(t *testing.T) string {
	t.Helper()
	dsk := filepath.Join(root, "asm", "8fish.dsk")
	img := filepath.Join(root, "asm", "8fish.img")
	l, err := delivery.Build(root, img, dsk)
	if err != nil {
		if errors.Is(err, delivery.ErrNoDiskii) {
			t.Skip("SKIP: diskii not installed")
		}
		t.Fatalf("building the disk: %v", err)
	}
	t.Logf("%s", l)
	return dsk
}

// TestDiskLayout proves the disk build is the SAME UI, not a fork of it: the
// Standard Delivery link differs from the BLOAD link only in where the copier
// lives and where it looks for the payload, and the payload itself is
// byte-identical. If that ever stops being true, every gate in this package
// is testing something other than what the disk carries.
func TestDiskLayout(t *testing.T) {
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(root, "asm", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	shipped, staged := read("m8.bin"), read("m8sd.bin")
	if !bytes.Equal(shipped, staged) {
		t.Errorf("m8sd.bin (%d B) differs from m8.bin (%d B): the payload must not "+
			"depend on where it was staged", len(staged), len(shipped))
	}
	bootBload, bootDisk := read("m8boot.bin"), read("m8sdboot.bin")
	if len(bootBload) != len(bootDisk) {
		t.Fatalf("copiers differ in length: m8boot.bin %d B, m8sdboot.bin %d B",
			len(bootBload), len(bootDisk))
	}
	var diffs []int
	for i := range bootBload {
		if bootBload[i] != bootDisk[i] {
			diffs = append(diffs, i)
		}
	}
	// The copier is position independent (every branch is relative and the
	// only absolute reference is `jmp $E000`), so moving it from $0800 to
	// $0C00 changes nothing; moving the payload from $0900 to $0D00 changes
	// exactly the high byte of the source pointer.
	t.Logf("copiers: %d B each, differing at %v (payload page $%02X vs $%02X)",
		len(bootBload), diffs, bootBload[diffs[0]], bootDisk[diffs[0]])
	if len(diffs) != 1 {
		t.Errorf("m8boot.bin and m8sdboot.bin differ at %d offsets, want exactly 1 "+
			"(the payload's page): %v", len(diffs), diffs)
	}
	if got, want := bootDisk[diffs[0]], byte(delivery.PayloadOrg>>8); got != want {
		t.Errorf("m8sdboot.bin stages the payload from page $%02X, want $%02X",
			got, want)
	}
}

// TestDiskLedger is the margin ledger. It fails when EITHER margin is
// exhausted and prints both on every run, so growth is visible long before it
// is fatal. A comment would not survive the growth; this does.
func TestDiskLedger(t *testing.T) {
	pieces, err := delivery.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	img, l, err := delivery.Image(pieces)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Standard Delivery image, base $%04X:", l.Base)
	for _, p := range pieces {
		t.Logf("  $%04X-$%04X  %6d B  %s", p.Org, p.Org+len(p.Data)-1, len(p.Data), p.Path)
	}
	t.Logf("  ------------------------------------------------")
	t.Logf("  image        $%04X-$%04X  %6d B in %d sectors", l.Base, l.End-1, l.ImageBytes, l.Sectors)
	t.Logf("  MARGIN 1  SD spare:       %5d B of %d  (the ENGINE spends this)",
		l.SDSpare, delivery.MaxImage)
	t.Logf("  MARGIN 2  UI growth room: %5d B  (staged payload ends $%04X, book at $%04X)",
		l.UIRoom, l.PayloadEnd-1, delivery.BookOrg)

	if len(img) != l.ImageBytes {
		t.Errorf("image is %d B but the ledger says %d", len(img), l.ImageBytes)
	}
	if l.SDSpare < 0 {
		t.Errorf("MARGIN 1 EXHAUSTED: the image is %d B, which is %d B over `diskii mksd`'s "+
			"%d B limit. Raising internal/delivery.Base by one page buys 256 B here and "+
			"costs 256 B of MARGIN 2 -- but MARGIN 2 has only %d B, so at $%04X there is "+
			"nowhere left to raise it to.",
			l.ImageBytes, -l.SDSpare, delivery.MaxImage, l.UIRoom, delivery.Base)
	}
	if l.UIRoom < 0 {
		t.Errorf("MARGIN 2 EXHAUSTED: the staged UI payload ends at $%04X, %d B INTO the "+
			"resident opening book at $%04X. The boot loader would deliver the payload "+
			"over the book's first %d bytes and the engine would probe garbage.",
			l.PayloadEnd, -l.UIRoom, delivery.BookOrg, -l.UIRoom)
	}
	// The book blob on disk must be the one the Go side ships, or the UI's
	// book probe and internal/book disagree about what the engine knows.
	for _, p := range pieces {
		if p.Org == delivery.BookOrg && !bytes.Equal(p.Data, book.DefaultBlob()) {
			t.Errorf("the book on the disk is not internal/book's embedded blob "+
				"(%d B on disk, %d B embedded)", len(p.Data), len(book.DefaultBlob()))
		}
	}
}

// TestDiskRoundTrip reads the built .dsk back and asserts the delivered bytes
// are the image, sector by sector. Cheap, and it catches a sector-order or
// interleave mistake without booting anything.
func TestDiskRoundTrip(t *testing.T) {
	dsk := dskPath(t)
	pieces, err := delivery.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	img, l, err := delivery.Image(pieces)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dsk)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != delivery.Tracks*delivery.SectorsPerTrk*delivery.SectorBytes {
		t.Fatalf("%s is %d bytes, want %d", dsk, len(raw),
			delivery.Tracks*delivery.SectorsPerTrk*delivery.SectorBytes)
	}
	back, err := delivery.Extract(raw, len(img))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, img) {
		for i := range img {
			if img[i] != back[i] {
				t.Fatalf("disk differs from image at offset %d ($%04X): image $%02X, disk $%02X "+
					"(image page %d, .dsk offset %d)",
					i, delivery.Base+i, img[i], back[i], i/256, delivery.SectorOffset(i/256))
			}
		}
	}
	t.Logf("round trip: %d sectors, %d bytes, identical", l.Sectors, len(img))

	// And each piece is where its load address says it is.
	for _, p := range pieces {
		got := back[p.Org-delivery.Base : p.Org-delivery.Base+len(p.Data)]
		if !bytes.Equal(got, p.Data) {
			t.Errorf("%s does not read back at $%04X", p.Path, p.Org)
		}
	}
}

// TestDiskBoots is the point of the whole exercise: BOOT THE DISK.
//
// Nothing is placed in memory by this test. The CPU starts at $C600, the Disk
// II controller's own boot ROM; that ROM reads track 0 sector 0 into $0800 and
// jumps to it; the Standard Delivery loader in that sector reads the other 176
// sectors into $0C00-$BB99 and jumps to $0C00; the copier there latches the
// Language Card, lifts the UI to $E000, installs the engine's aux primitives
// at $D000 and jumps to $E000; and the UI paints the board and blocks on the
// keyboard. Every byte of 8fish in RAM got there off the disk.
//
// Three assertions, in the order the boot produces them:
//
//  1. the loader delivered the image EXACTLY, byte for byte, into RAM;
//  2. the UI is running from Language Card RAM, with ALTCHARSET selected;
//  3. the screen it painted is byte-identical to the one the gated shipping
//     image paints under the harness -- so every screen gate in this package
//     is a gate on what the disk shows.
func TestDiskBoots(t *testing.T) {
	dsk := dskPath(t)

	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	t.Logf("machine: Apple IIe (goapple2/iie 128K) + Disk II in slot %d, ROM = %s; "+
		"entry $%04X (PR#%d)", ui.DiskSlot, m.ROMName, ui.BootEntry, ui.DiskSlot)

	// ---- 1. the loader delivers the image ----------------------------------
	// Stop the moment the loader jumps to the copier, before the copier has
	// had a chance to touch anything.
	const loadBudget = 400_000_000 // ~390 emulated seconds; a real load is far less
	ok, err := m.RunUntilPC(uint16(delivery.CopierOrg), loadBudget)
	if err != nil {
		t.Fatalf("booting: %v", err)
	}
	if !ok {
		t.Fatalf("the boot never reached the copier at $%04X after %d cycles (PC $%04X)",
			delivery.CopierOrg, m.Cycles, m.CPU.PC())
	}
	loadCycles := m.Cycles
	pieces, err := delivery.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	img, l, err := delivery.Image(pieces)
	if err != nil {
		t.Fatal(err)
	}
	delivered := m.Mem.Main[delivery.Base : delivery.Base+len(img)]
	if !bytes.Equal(delivered, img) {
		for i := range img {
			if img[i] != delivered[i] {
				t.Fatalf("the loader delivered the wrong byte at $%04X: want $%02X, got $%02X",
					delivery.Base+i, img[i], delivered[i])
			}
		}
	}
	t.Logf("LOADED: %d B ($%04X-$%04X, %d sectors) delivered from disk in %d cycles (%.2f s), "+
		"byte-identical to the image", l.ImageBytes, l.Base, l.End-1, l.Sectors,
		loadCycles, float64(loadCycles)/1_020_484)

	// ---- 2. the UI comes up and blocks for a key ---------------------------
	ok, err = m.RunToKeyboard(200_000_000)
	if err != nil {
		t.Fatalf("running the UI: %v", err)
	}
	if !ok {
		t.Fatalf("the UI never blocked for a keystroke (PC $%04X after %d cycles)",
			m.CPU.PC(), m.Cycles)
	}
	if pc := m.CPU.PC(); pc < 0xE000 {
		t.Errorf("the UI is blocked at $%04X, not in Language Card RAM ($E000-$FFEF)", pc)
	}
	if !m.Mem.AltCharset {
		t.Error("ALTCHARSET is off: every black piece on a dark square would be " +
			"flashing punctuation on a real IIe (see docs/results.md 2026-07-28)")
	}
	if m.Mem.Col80 {
		t.Error("80COL is on: this 40-column screen would show one column in two")
	}
	if !m.Mem.Text || m.Mem.Mixed || m.Mem.Page2 {
		t.Errorf("display state wrong: TEXT=%v MIXED=%v PAGE2=%v", m.Mem.Text, m.Mem.Mixed, m.Mem.Page2)
	}
	if len(m.Unhandled()) != 0 {
		t.Errorf("the boot touched $C0xx locations the IIe model does not implement: %v",
			m.Unhandled())
	}

	// ---- 3. the screen is the gated screen ---------------------------------
	got := *m.Screen()
	t.Logf("BOOTED FROM DISK in %d cycles (%.2f s of emulated IIe time), PC $%04X:\n%s",
		m.Cycles, float64(m.Cycles)/1_020_484, m.CPU.PC(), &got)

	want, err := ui.BootShipping(root, book.DefaultBlob())
	if err != nil {
		t.Fatalf("harness boot for comparison: %v", err)
	}
	ref := want.Screen()
	if got.Raw != ref.Raw {
		for row := range 24 {
			if got.Raw[row] != ref.Raw[row] {
				t.Errorf("row %d differs:\n disk: %q\n ref:  %q", row,
					got.Text(row), ref.Text(row))
			}
		}
		t.Fatalf("the disk boot paints a different screen from the gated shipping image")
	}

	// And it really is a start position, not just "the same 960 bytes".
	joined := strings.Join(func() []string {
		var rows []string
		for r := range 24 {
			rows = append(rows, got.Text(r))
		}
		return rows
	}(), "\n")
	for _, want := range []string{"8fish", "a b c d e f g h"} {
		if !strings.Contains(strings.ToLower(joined), strings.ToLower(want)) {
			t.Errorf("the booted screen does not contain %q", want)
		}
	}
}

// TestDiskPlays goes past painting. A screen is not a program: this types a
// move on the modelled IIe keyboard of the DISK-BOOTED machine, lets the
// engine answer, and checks that a legal reply appeared on the board — which
// means the engine at $4000, the opening book at $2000 and the aux-RAM
// transposition table all arrived off the disk in working order.
//
// It plays at level 1 (fixed depth 2) so the test costs seconds, not minutes;
// the depth is not the point, the plumbing is.
func TestDiskPlays(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots a disk and searches")
	}
	dsk := dskPath(t)
	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	if ok, err := m.RunToKeyboard(600_000_000); err != nil || !ok {
		t.Fatalf("boot: ok=%v err=%v (PC $%04X)", ok, err, m.CPU.PC())
	}

	// L prompts for a level; 1 is fixed depth 2, which keeps this test in
	// seconds rather than minutes. The depth is not the point; the plumbing is.
	const keyBudget = 1_500_000_000 // ~1,470 emulated seconds
	if err := m.Enter("l", keyBudget); err != nil {
		t.Fatalf("l: %v\n%v", err, m.Screen())
	}
	if got := m.Screen().Text(17); !strings.Contains(got, "LEVEL?") {
		t.Fatalf("row 17 = %q, want the level prompt\n%v", strings.TrimSpace(got), m.Screen())
	}
	if err := m.Key('1', keyBudget); err != nil {
		t.Fatalf("level 1: %v", err)
	}
	if got := m.Screen().Text(0); !strings.Contains(got, "LEVEL 1") {
		t.Fatalf("title = %q, want LEVEL 1", got)
	}

	before := m.Cycles
	if err := m.Enter("e2e4", keyBudget); err != nil {
		t.Fatalf("typing e2e4: %v\n%v", err, m.Screen())
	}
	s := m.Screen()
	t.Logf("after e2e4 and the engine's reply (%d cycles = %.2f s of IIe time):\n%v",
		m.Cycles-before, float64(m.Cycles-before)/1_020_484, s)

	// White's pawn moved.
	if got := s.Text(8); !strings.HasPrefix(got, " 2 P P P P   P P P") {
		t.Errorf("rank 2 is %q, want the e-pawn gone", strings.TrimRight(got, " "))
	}
	if got := s.Text(6); !strings.Contains(got, "P") {
		t.Errorf("rank 4 is %q, want a white pawn on e4", strings.TrimRight(got, " "))
	}
	// And Black answered: the move panel's first line is "1 <white> <black>".
	// The panel occupies columns 22 onward of the board rows.
	var line string
	for row := 2; row < 11 && line == ""; row++ {
		if txt := strings.TrimSpace(s.Text(row)[22:]); strings.HasPrefix(txt, "1 ") {
			line = txt
		}
	}
	t.Logf("move panel line 1: %q", line)
	f := strings.Fields(line)
	if len(f) < 2 || !strings.EqualFold(f[1], "e2e4") {
		t.Fatalf("the move panel does not show 1 e2e4: %q", line)
	}
	if len(f) < 3 {
		t.Fatalf("black never replied: the move panel says %q", line)
	}
	reply := strings.ToLower(f[2])
	pos, err := refchess.ParseFEN("rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1")
	if err != nil {
		t.Fatal(err)
	}
	legal := false
	for _, mv := range pos.LegalMoves() {
		if mv.String() == reply {
			legal = true
		}
	}
	if !legal {
		t.Errorf("black's reply %q is not a legal move after 1 e4", reply)
	}
	t.Logf("BLACK REPLIED %s -- the resident opening book at $2000 came off the disk "+
		"and was probed (the screen names the line)", reply)

	// The book answered, which means nothing SEARCHED yet. Play offbeat moves
	// until the engine has to think, then check that it did -- and that the
	// transposition table in AUXILIARY RAM was written, which is the whole
	// reason this machine has to be a IIe and not the ][+ the rest of the
	// project's emulator work boots.
	auxTouched := func() int {
		n := 0
		for _, b := range m.Mem.Aux[0x0200:0x8200] {
			if b != 0 {
				n++
			}
		}
		return n
	}
	if n := auxTouched(); n != 0 {
		t.Errorf("the aux transposition table already has %d non-zero bytes before any search", n)
	}
	searched := false
	for _, mv := range []string{"g1h3", "h1g1", "f1e2", "e1f1"} {
		if err := m.Enter(mv, keyBudget); err != nil {
			t.Fatalf("typing %s: %v\n%v", mv, err, m.Screen())
		}
		s := m.Screen()
		book := false
		for row := range 24 {
			if strings.Contains(s.Text(row), "BOOK:") {
				book = true
			}
		}
		if !book {
			t.Logf("out of book after %s:\n%v", mv, s)
			searched = true
			break
		}
	}
	if !searched {
		t.Fatal("never left the book, so nothing searched")
	}
	n := auxTouched()
	t.Logf("AUX RAM: %d non-zero bytes in the transposition table at $0200-$81FF "+
		"(aux) after the first real search", n)
	if n == 0 {
		t.Error("the engine searched but wrote nothing to the aux-RAM transposition " +
			"table: the copier's LCCODE install or the RAMWRT path is broken")
	}
}
