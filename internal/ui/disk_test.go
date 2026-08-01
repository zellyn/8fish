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
// there is almost nowhere left to go: the base is $0D00 (raised from $0C00 on
// 2026-07-30 to pay for the engine's pre-make evasion filter) and one more
// page would already overrun the book. So both numbers are asserted, both are
// printed on every run along with the whole base/margin table recomputed from
// the real files, and the day either goes negative this test says which one
// and by how much.

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

// TestDiskLedger is the margin ledger for the SIMPLE SINGLE-SHOT layout.
//
// It is a TRIPWIRE, not a wall. It is our disk: nothing stops us chain-loading
// (one loaded thing loading the next) or moving to ProRWTS2, and either
// removes the contiguous-span squeeze entirely. What this test says when a
// margin goes negative is "the one-shot Standard Delivery path no longer fits
// -- pick a different mechanism", NOT "8fish cannot grow". It exists so that
// choice is made deliberately on the day it arrives, instead of a disk quietly
// coming out wrong.
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

	// The base/margin trade-off table, recomputed from the REAL file sizes on
	// every run. delivery.go carries the same table as a comment for readers,
	// and a comment that has to be maintained by hand is a comment that goes
	// stale (it already did once, quoting sizes two releases old). This is the
	// copy to trust when the decision "raise the base by a page?" comes round
	// again, which the margins guarantee it will.
	t.Logf("  base/margin trade-off, from the files on disk right now:")
	t.Logf("    %-6s %8s %8s %s", "base", "image", "spare", "UI room")
	for base := 0x0800; base <= 0x1000; base += delivery.SectorBytes {
		ps, err := delivery.LoadAt(root, base)
		if err != nil {
			t.Fatal(err)
		}
		_, bl, err := delivery.Image(ps)
		if err != nil {
			// Overlap (the payload has grown into the book): report the base
			// as unusable rather than failing the whole ledger.
			t.Logf("    $%04X  %8s %8s %s", base, "-", "-", err)
			continue
		}
		mark := ""
		if base == delivery.Base {
			mark = "  <- chosen"
		}
		t.Logf("    $%04X  %8d %8d %7d%s", base, bl.ImageBytes, bl.SDSpare, bl.UIRoom, mark)
	}

	if len(img) != l.ImageBytes {
		t.Errorf("image is %d B but the ledger says %d", len(img), l.ImageBytes)
	}
	if l.SDSpare < 0 {
		t.Errorf("MARGIN 1: the image is %d B, %d B over `diskii mksd`'s %d B limit, so "+
			"8fish has outgrown the SIMPLE SINGLE-SHOT Standard Delivery layout.\n"+
			"This is a decision point, not a dead end. Raising internal/delivery.Base by "+
			"one page buys 256 B here at the cost of 256 B of MARGIN 2 (%d B left); when "+
			"that runs out, the answer is a different LOADER, not a smaller program -- "+
			"chain-load in two stages, or move to ProRWTS2 (which also lifts the "+
			"contiguous-span constraint and gives the UI its full LC budget back).",
			l.ImageBytes, -l.SDSpare, delivery.MaxImage, l.UIRoom)
	}
	if l.UIRoom < 0 {
		t.Errorf("MARGIN 2: the staged UI payload ends at $%04X, %d B INTO the resident "+
			"opening book at $%04X. In THIS layout the loader would deliver the payload "+
			"over the book's first %d bytes and the engine would probe garbage. The fix "+
			"is a delivery mechanism that does not have to stage the payload below the "+
			"book -- see MARGIN 1's note -- not a smaller UI.",
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

	// Boot from the HOSTILE switch state, not the reset state. A IIe's
	// 80-column firmware (PR#3, or ProDOS starting up) leaves 80STORE and
	// PAGE2 on, and whatever ran before 8fish is what 8fish inherits. With
	// them on, $0400-$07FF follows PAGE2 and IGNORES RAMRD/RAMWRT, so the
	// engine's aux transposition table would have its $0400-$07FF slots
	// land on the text page. asm/m8.s m8main's `sta CLR80STORE` is what
	// fends that off; setting the switches here is what makes that store
	// load-bearing in this test rather than decorative. (Deleting it from
	// m8.s fails the Store80 check below. The SCREEN still comes out right
	// even then, because m8main's `sta TXTPAGE1` leaves the text page
	// pointing at main -- which is exactly why the switch itself has to be
	// asserted, and why an end-state screen comparison is not enough.)
	//
	// goapple2's iie model gained 80STORE for this (its TestA2AuditAuxmem);
	// before that it counted $C000/$C001 in Unhandled and no test here
	// could tell the difference between the store being present and absent.
	m.Mem.Store80 = true
	m.Mem.Page2 = true

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
	// Nothing may stray outside the modelled subset. $C000/$C001 used to be
	// an allowed exception here because the model did not implement 80STORE;
	// it does now, so there is no exception left.
	for addr, n := range m.Unhandled() {
		t.Errorf("the boot touched $%04X (%d times), which the IIe model does not implement", addr, n)
	}
	// The switches were left hostile at power-on (see the top of this test);
	// m8main has to have turned 80STORE back off, or the engine's AUX
	// transposition table traffic at $0400-$07FF and the UI's text page are
	// the same memory.
	if m.Mem.Store80 {
		t.Error("80STORE is still ON: m8main never did its `sta CLR80STORE`, so on a IIe " +
			"booted from the 80-column firmware the screen and the engine's aux " +
			"scratch at $0400-$07FF would be the same bytes")
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

// TestDiskQuitReboots: Q QUITS.
//
// On an Apple II with no resident operating system there is nowhere to quit
// TO, so quitting means a cold boot — which, with the disk still in the
// drive, restarts 8fish. `cmd_quit` copies six bytes to $0300 (it cannot bank
// the ROM back in while executing FROM the language card: the very next
// instruction fetch would come from ROM), reads $C082 to put ROM over
// $D000-$FFFF, and jumps through the ROM's own RESET vector. Because m8main
// invalidated the Autostart power-up byte, that RESET takes the COLD path,
// which scans the slots and boots the disk.
//
// Until 2026-07-28 Q stored a byte to the harness exit trap at $BFFF and fell
// into m8new. On hardware $BFFF is plain RAM, so the only thing Q did there
// was silently DISCARD the game — under a label promising the opposite, and
// with no test pressing the key. This is that test.
func TestDiskQuitReboots(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots a disk twice")
	}
	const budget = 600_000_000

	// bootAndQuit boots the disk, plays a move so that "it came back to a
	// start position" is a claim about a RESTART rather than about nothing
	// having happened, and presses Q.
	bootAndQuit := func(t *testing.T) (*ui.DiskMachine, ui.Screen) {
		t.Helper()
		m, err := ui.NewDiskMachine(dskPath(t), ui.RomDir())
		if err != nil {
			t.Skipf("SKIP: no Apple II machine available: %v", err)
		}
		t.Logf("booted from disk, ROM = %s", m.ROMName)
		if ok, err := m.RunToKeyboard(budget); err != nil || !ok {
			t.Fatalf("boot: ok=%v err=%v (PC $%04X)", ok, err, m.CPU.PC())
		}
		first := *m.Screen()
		if err := m.Enter("e2e4", budget); err != nil {
			t.Fatalf("typing e2e4: %v\n%v", err, m.Screen())
		}
		if s := m.Screen(); s.Raw == first.Raw {
			t.Fatalf("the screen did not change when a move was played\n%v", s)
		}
		if err := m.Key('q', budget); err != nil { // typed and echoed; still polling
			t.Fatalf("typing q: %v", err)
		}
		m.SendKey(0x0D) // RETURN submits the line and cmd_quit runs
		return m, first
	}

	// The part 8fish is responsible for: control leaves the language card
	// through the ROM's RESET vector. Nothing in the UI writes $FFFC any
	// more, so reaching that address is proof the ROM was banked back in
	// and its vector — not a leftover byte of LC RAM — was taken.
	t.Run("jumps through the ROM RESET vector", func(t *testing.T) {
		m, _ := bootAndQuit(t)
		reset := uint16(m.Mem.ROM[0xFFFC-0xD000]) | uint16(m.Mem.ROM[0xFFFD-0xD000])<<8
		ok, err := m.RunUntilPC(reset, 10_000_000)
		if err != nil {
			t.Fatalf("after Q: %v", err)
		}
		if !ok {
			t.Fatalf("after Q the CPU never reached the ROM's RESET vector $%04X "+
				"(PC $%04X) — Q did not reboot\n%v", reset, m.CPU.PC(), m.Screen())
		}
		t.Logf("Q reached the ROM RESET handler at $%04X", reset)
	})

	// ...and the whole way round, when the ROM's reset path is one this
	// emulator can execute. goapple2's iie model deliberately implements no
	// $C100-$CFFF internal ROM (see internal/ui/diskboot.go), and a real IIe
	// ROM's cold start calls into it, so that machine cannot complete a
	// reboot here for reasons that have nothing to do with 8fish. The Apple
	// ][+ Autostart ROM makes the same two decisions this fix depends on —
	// the power-up byte test and the slot scan — without the IIe firmware,
	// so it is the one that can prove the round trip.
	t.Run("cold boots the disk", func(t *testing.T) {
		plus := filepath.Join(ui.RomDir(), "apple2+.rom")
		if _, err := os.Stat(plus); err != nil {
			t.Skipf("SKIP: no Apple ][+ ROM at %s", plus)
		}
		t.Setenv("CHESS6502_A2E_ROM", plus) // ui.FindROM's explicit-image hook
		m, first := bootAndQuit(t)
		ok, err := m.RunUntilPC(uint16(delivery.CopierOrg), budget)
		if err != nil {
			t.Fatalf("after Q: %v", err)
		}
		if !ok {
			t.Fatalf("after Q the machine never re-entered the disk copier at $%04X "+
				"(PC $%04X after %d cycles) — Q did not cold boot\n%v",
				delivery.CopierOrg, m.CPU.PC(), m.Cycles, m.Screen())
		}
		t.Logf("Q reached the Standard Delivery copier at $%04X again: the ROM cold "+
			"start re-read the disk", delivery.CopierOrg)
		if ok, err := m.RunToKeyboard(budget); err != nil || !ok {
			t.Fatalf("reboot: ok=%v err=%v (PC $%04X)\n%v", ok, err, m.CPU.PC(), m.Screen())
		}
		got := *m.Screen()
		if got.Raw != first.Raw {
			for row := range 24 {
				if got.Raw[row] != first.Raw[row] {
					t.Errorf("row %d after the reboot:\n got:  %q\n want: %q",
						row, got.Text(row), first.Text(row))
				}
			}
			t.Fatalf("the machine came back to a different screen:\n%v", &got)
		}
		if n := m.Mem.Main[ui.UIHCNT]; n != 0 {
			t.Errorf("UIHCNT = %d after the reboot, want a fresh game", n)
		}
	})
}
