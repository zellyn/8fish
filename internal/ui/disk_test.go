package ui_test

// disk_test.go gates the SHIPPING ARTEFACT: asm/8fish.dsk. Static gates on how
// the disk is built, and the ones that boot it.
//
// The disk is now delivered in TWO STAGES. One Standard Delivery page table
// names 176 sectors and no more -- that is the table's size, not a policy --
// and 8fish is 181 sectors, so the single-shot layout is finished. The boot
// sector's loader survives the load, though (our image starts at $0D00 and the
// loader lives in $0800-$08FF), so the copier writes a fresh page table into
// page $08 and re-enters it at $0802 for stage 2. See internal/delivery.
//
// What that changed about the margins: the old "SD spare" and "UI growth room"
// were both properties of ONE contiguous span, and neither survives. Stage 1
// scatter-loads two disjoint spans, so its gap costs nothing; the payload
// stages into $0E00-$1FFF with the book no longer in the way; and the ceiling
// that binds is per stage. What is asserted here now is that each stage fits
// its own table, that the two together fit the disk, and -- the gate that
// matters -- that the disk BOOTS and PLAYS.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/delivery"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/tiles"

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
	// The two COPIERS are no longer the same program: -D SDCHAIN gives the
	// disk one the chain loader, which the BLOAD one cannot have (a BRUN from
	// BASIC arrives with none of the boot loader's read state). What must
	// still hold is that the difference is confined to the BOOT segment --
	// the payload above is byte-identical, which is what the first assertion
	// just proved -- and that the disk copier is a SUPERSET: the BLOAD
	// copier's work is still all in there, in the same order.
	bootBload, bootDisk := read("m8boot.bin"), read("m8sdboot.bin")
	if len(bootDisk) <= len(bootBload) {
		t.Errorf("m8sdboot.bin (%d B) is not larger than m8boot.bin (%d B): the chain "+
			"loader is missing -- was -D SDCHAIN dropped?", len(bootDisk), len(bootBload))
	}
	if len(bootDisk) > delivery.SectorBytes {
		t.Errorf("the copier is %d B, %d B past the one page asm/m8sd.cfg gives it (and "+
			"past $%04X, where stage 2's own landing zone starts)",
			len(bootDisk), len(bootDisk)-delivery.SectorBytes, delivery.TilesOrg)
	}
	t.Logf("copiers: m8boot.bin %d B (BLOAD), m8sdboot.bin %d B (disk, +%d B of chain loader)",
		len(bootBload), len(bootDisk), len(bootDisk)-len(bootBload))
	// The one byte that still has to be right in both: the page the payload
	// is staged at, which the .cfg supplies as a linker symbol.
	for _, c := range []struct {
		name string
		bin  []byte
		page byte
	}{
		{"m8boot.bin", bootBload, byte(readCfgPayloadPage(t, "m8.cfg"))},
		{"m8sdboot.bin", bootDisk, byte(delivery.PayloadOrg >> 8)},
	} {
		if !bytes.Contains(c.bin[:24], []byte{0xA9, c.page}) {
			t.Errorf("%s does not load the payload page $%02X in its first 24 bytes",
				c.name, c.page)
		}
	}
}

// readCfgPayloadPage pulls the UIPAYLOAD linker symbol's page out of a .cfg.
func readCfgPayloadPage(t *testing.T, cfg string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "asm", cfg))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`UIPAYLOAD:\s*type\s*=\s*export,\s*value\s*=\s*\$([0-9A-Fa-f]+)`).
		FindSubmatch(b)
	if m == nil {
		t.Fatalf("no UIPAYLOAD symbol in asm/%s", cfg)
	}
	v, err := strconv.ParseInt(string(m[1]), 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	return int(v) >> 8
}

// TestDiskLedger is the delivery ledger for the two-stage layout: what each
// stage costs against the 176 sectors one page table can name, what the pair
// costs against the 560 sectors on the disk, and where every piece lands.
//
// It is a TRIPWIRE, not a wall. When a stage overflows its table the answer is
// another stage -- the loader can be re-entered as many times as we like -- or
// ProRWTS2, which would also give 8fish a disk it can WRITE (a saved game).
// What this test exists to do is make that choice a deliberate one on the day
// it arrives, instead of a disk quietly coming out wrong.
func TestDiskLedger(t *testing.T) {
	stage1, stage2, err := delivery.LoadStages(root)
	if err != nil {
		t.Fatal(err)
	}
	l := delivery.LedgerOf(stage1, stage2)

	report := func(name string, st delivery.Stage, pieces []delivery.Piece) {
		loaded, err := delivery.Load(root, pieces)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s:", name)
		for _, p := range loaded {
			t.Logf("    $%04X-$%04X  %6d B  %s", p.Org, p.Org+len(p.Data)-1, len(p.Data), p.Path)
		}
		for _, sp := range st.Spans {
			t.Logf("      span $%04X-$%04X = %d sectors (pages $%02X-$%02X)",
				sp.Org, sp.Org+len(sp.Data)-1, sp.Sectors(),
				sp.Org>>8, (sp.Org>>8)+sp.Sectors()-1)
		}
		t.Logf("    %d of %d sectors one page table can name (%d spare)",
			st.Sectors(), delivery.MaxStageSectors, delivery.MaxStageSectors-st.Sectors())
	}
	report("STAGE 1  (the boot sector's table, patched to scatter-load)",
		stage1, delivery.Stage1Pieces())
	report("STAGE 2  (the copier's table, loader re-entered at $0802)",
		stage2, delivery.Stage2Pieces())
	t.Logf("  disk: %d of %d sectors used, %d free", l.TotalSectors,
		delivery.SectorsPerDisk, l.DiskSpare)

	// What a SINGLE-SHOT delivery would have needed, so the reason for the
	// chain load stays a measured number rather than a memory.
	oneShot := 1 + stage1.Sectors() + stage2.Sectors() - 1
	t.Logf("  a single-shot layout would need %d sectors in ONE table of %d: over by %d",
		oneShot, delivery.MaxStageSectors, oneShot-delivery.MaxStageSectors)
	if oneShot <= delivery.MaxStageSectors {
		t.Errorf("everything now fits in one page table (%d sectors of %d). The chain "+
			"load is no longer load-bearing; collapsing back to a single stage would "+
			"be simpler and faster to boot.", oneShot, delivery.MaxStageSectors)
	}

	if err := stage1.Check(); err != nil {
		t.Errorf("stage 1: %v", err)
	}
	if err := stage2.Check(); err != nil {
		t.Errorf("stage 2: %v", err)
	}
	if l.TotalSectors > delivery.SectorsPerDisk {
		t.Errorf("the two stages need %d sectors, %d more than the disk holds",
			l.TotalSectors, l.TotalSectors-delivery.SectorsPerDisk)
	}
	// The staged payload must still end below stage 2's landing zone for the
	// book at $2000, or stage 2 would be read over a payload that has not
	// been lifted yet. (It HAS been lifted by then -- but only because the
	// copier does the lift first, and that ordering is worth a gate.)
	if l.PayloadRoom < 0 {
		t.Errorf("the staged UI payload ends at $%04X, %d B into stage 2's landing zone "+
			"for the book at $%04X", l.PayloadEnd, -l.PayloadRoom, delivery.BookOrg)
	}

	// The blobs on the disk must be the ones the Go side ships, or the UI's
	// book probe and internal/book disagree about what the engine knows, and
	// internal/tiles' render model is a model of different artwork.
	s2, err := delivery.Load(root, delivery.Stage2Pieces())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range s2 {
		var want []byte
		switch p.Org {
		case delivery.BookOrg:
			want = book.DefaultBlob()
		case delivery.TilesOrg:
			want = tiles.DefaultBlob()
		default:
			continue
		}
		if !bytes.Equal(p.Data, want) {
			t.Errorf("the blob delivered to $%04X is not the one Go ships (%d B on disk, "+
				"%d B embedded)", p.Org, len(p.Data), len(want))
		}
	}
}

// TestBootSectorPageTable reads the SHIPPED boot sector back and asserts the
// two facts the whole delivery rests on: the loader really is the page-table
// loader internal/delivery models, and the table it carries is stage 1's real
// span list -- the scatter, not mksd's consecutive default.
func TestBootSectorPageTable(t *testing.T) {
	dsk := dskPath(t)
	raw, err := os.ReadFile(dsk)
	if err != nil {
		t.Fatal(err)
	}
	boot := raw[:delivery.SectorBytes]

	// $0805 is `LDA $084E`, pre-incremented at $0802, so the first entry the
	// loader reads is $084F. If this instruction ever moves, every page in
	// the table is off by one and the disk delivers garbage.
	if boot[0x05] != 0xAD || boot[0x06] != 0x4E || boot[0x07] != 0x08 {
		t.Fatalf("boot sector $0805 is %02X %02X %02X, not `LDA $084E`: this is not the "+
			"Standard Delivery loader internal/delivery patches",
			boot[0x05], boot[0x06], boot[0x07])
	}
	if boot[0x02] != 0xEE || boot[0x03] != 0x06 || boot[0x04] != 0x08 {
		t.Fatalf("boot sector $0802 is %02X %02X %02X, not `INC $0806`",
			boot[0x02], boot[0x03], boot[0x04])
	}
	if boot[0x00] != 0x01 {
		t.Errorf("boot sector $0800 is $%02X, not $01: the boot ROM reads that many "+
			"sectors per call, and the loader depends on it being exactly one",
			boot[0x00])
	}

	stage1, _, err := delivery.LoadStages(root)
	if err != nil {
		t.Fatal(err)
	}
	want := stage1.PageTable()
	got := boot[0x4F : 0x4F+len(want)]
	if !bytes.Equal(got, want) {
		t.Errorf("boot sector page table = % 02X\n                     want = % 02X", got, want)
	}
	t.Logf("stage 1 page table: %d entries + terminator, %d spans", len(want)-1, len(stage1.Spans))
	if start := int(boot[0x4D]) | int(boot[0x4E])<<8; start != delivery.CopierOrg {
		t.Errorf("the terminator jumps to $%04X, not the copier at $%04X",
			start, delivery.CopierOrg)
	}
}

// TestStage2PageTable: the table the COPIER writes into page $08 for stage 2
// must name exactly the pages internal/delivery put on the disk. The two are
// derived independently -- ca65 builds the copier's from the blob sizes in
// asm/book.inc and asm/tiledefs.inc, Go builds the disk's from the blob files
// -- so this is a real cross-check and not a tautology.
func TestStage2PageTable(t *testing.T) {
	boot, err := os.ReadFile(filepath.Join(root, "asm", "m8sdboot.bin"))
	if err != nil {
		t.Fatal(err)
	}

	syms, err := ui.ParseLbl(filepath.Join(root, "asm", "m8sd.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	sd2tab, ok := syms["sd2tab"]
	if !ok {
		t.Fatal("m8sd.lbl has no sd2tab: the chain loader was built without -D SDCHAIN")
	}
	_, stage2, err := delivery.LoadStages(root)
	if err != nil {
		t.Fatal(err)
	}
	want := stage2.PageTable()
	base := int(sd2tab) - delivery.CopierOrg
	if base < 0 || base+len(want) > len(boot) {
		t.Fatalf("sd2tab is at $%04X, outside the %d-byte copier", sd2tab, len(boot))
	}
	got := boot[base : base+len(want)]
	if !bytes.Equal(got, want) {
		t.Errorf("the copier's stage 2 page table = % 02X\n"+
			"          the disk's stage 2 layout = % 02X", got, want)
	}
	t.Logf("stage 2 page table (%d entries + terminator) agrees between asm and Go",
		len(want)-1)
}

// TestDiskRoundTrip reads the built .dsk back and asserts the delivered bytes
// are the image, sector by sector. Cheap, and it catches a sector-order or
// interleave mistake without booting anything.
func TestDiskRoundTrip(t *testing.T) {
	dsk := dskPath(t)
	stage1, stage2, err := delivery.LoadStages(root)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dsk)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != delivery.SectorsPerDisk*delivery.SectorBytes {
		t.Fatalf("%s is %d bytes, want %d", dsk, len(raw),
			delivery.SectorsPerDisk*delivery.SectorBytes)
	}
	first := 0
	for _, st := range []delivery.Stage{stage1, stage2} {
		want := st.Bytes()
		back, err := delivery.ExtractPages(raw, first, st.Sectors())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(back, want) {
			for i := range want {
				if want[i] != back[i] {
					t.Fatalf("%s differs from the disk at byte %d (image page %d, "+
						".dsk offset %d): want $%02X, got $%02X", st.Name, i,
						first+i/256, delivery.SectorOffset(first+i/256), want[i], back[i])
				}
			}
		}
		// And each span reads back at the page the table names for it.
		off := 0
		for _, sp := range st.Spans {
			if !bytes.Equal(back[off:off+len(sp.Data)], sp.Data) {
				t.Errorf("%s: the span for $%04X does not read back", st.Name, sp.Org)
			}
			off += len(sp.Data)
		}
		t.Logf("round trip: %s, %d sectors from image page %d, identical",
			st.Name, st.Sectors(), first)
		first += st.Sectors()
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
	stage1, stage2, err := delivery.LoadStages(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range stage1.Spans {
		delivered := m.Mem.Main[sp.Org : sp.Org+len(sp.Data)]
		if !bytes.Equal(delivered, sp.Data) {
			for i := range sp.Data {
				if sp.Data[i] != delivered[i] {
					t.Fatalf("stage 1 delivered the wrong byte at $%04X: want $%02X, got $%02X",
						sp.Org+i, sp.Data[i], delivered[i])
				}
			}
		}
		t.Logf("STAGE 1 span $%04X-$%04X (%d sectors) delivered byte-identically",
			sp.Org, sp.Org+len(sp.Data)-1, sp.Sectors())
	}
	l := delivery.LedgerOf(stage1, stage2)
	t.Logf("STAGE 1: %d sectors in %d cycles (%.2f s)",
		l.Stage1Sectors, loadCycles, float64(loadCycles)/1_020_484)

	// ---- 2. the UI comes up and blocks for a key ---------------------------
	ok, err = m.RunToKeyboard(200_000_000)
	if err != nil {
		t.Fatalf("running the UI: %v", err)
	}
	if !ok {
		t.Fatalf("the UI never blocked for a keystroke (PC $%04X after %d cycles)",
			m.CPU.PC(), m.Cycles)
	}
	t.Logf("BOOT COMPLETE: %d cycles (%.2f s) from $C600 to the first keyboard poll",
		m.Cycles, float64(m.Cycles)/1_020_484)

	// ---- 2a. STAGE 2 arrived, and was put where it belongs -----------------
	// The chain load is invisible from the screen, so assert its two products
	// directly: the artwork in Language Card bank 1 (m.Mem.Main[$D000-$DFFF]
	// IS bank 1 in goapple2's model) and the opening book in AUX $0200.
	blob := tiles.DefaultBlob()
	if got := m.Mem.Main[delivery.TilesLC : delivery.TilesLC+len(blob)]; !bytes.Equal(got, blob) {
		for i := range blob {
			if blob[i] != got[i] {
				t.Fatalf("the tile blob is wrong at LC $%04X (offset %d of %d): want $%02X, "+
					"got $%02X -- stage 2 did not land, or the copier put it somewhere else",
					delivery.TilesLC+i, i, len(blob), blob[i], got[i])
			}
		}
	}
	t.Logf("STAGE 2: %d B of artwork resident at LC bank 1 $%04X-$%04X",
		len(blob), delivery.TilesLC, delivery.TilesLC+len(blob)-1)
	bk := book.DefaultBlob()
	if got := m.Mem.Aux[delivery.BookAux : delivery.BookAux+len(bk)]; !bytes.Equal(got, bk) {
		for i := range bk {
			if bk[i] != got[i] {
				t.Fatalf("the opening book is wrong at AUX $%04X (offset %d of %d): want "+
					"$%02X, got $%02X", delivery.BookAux+i, i, len(bk), bk[i], got[i])
			}
		}
	}
	t.Logf("STAGE 2: %d B of opening book resident at AUX $%04X-$%04X",
		len(bk), delivery.BookAux, delivery.BookAux+len(bk)-1)
	// And main $2000-$3FFF -- the DHGR main half -- is nobody's home now.
	// Whatever the loader left there is dead; what matters is that nothing
	// RESIDENT is there, which the two assertions above establish by having
	// found both blobs elsewhere.

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
	// ★ 80STORE. This USED to be untestable: goapple2's IIe model implemented
	// neither 80STORE state and counted both switch addresses in Unhandled,
	// so `sta CLR80STORE` in m8main was documented as a hardware-only
	// precaution nothing here could check. The model implements it now --
	// including the precedence that makes it dangerous, 80STORE OVERRIDING
	// RAMRD/RAMWRT for $0400-$07FF and (with HIRES) $2000-$3FFF -- so assert
	// the state directly instead of inferring it from an unhandled-access
	// count. That precedence is exactly what would send the DHGR renderer's
	// aux half into main.
	if m.Mem.Store80 {
		t.Error("80STORE is ON: $0400-$07FF and hi-res page 1 would follow PAGE2 and " +
			"IGNORE RAMWRT, so the DHGR renderer's aux half would land in MAIN. " +
			"m8main's `sta CLR80STORE` is load-bearing, not defence in depth")
	}
	// Everything else straying outside the modelled subset is still a failure.
	for addr, n := range m.Unhandled() {
		t.Errorf("the boot touched $%04X (%d times), which the IIe model does not implement", addr, n)
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
		// The transposition table is aux $4000-$BFFF (all 4096 entries; it
		// moved off $0200 when the DHGR aux half claimed $2000-$3FFF). Aux
		// $0200-$1EEE below it is the resident opening book, which the copier
		// filled at boot, so counting from $0200 would count the book.
		for _, b := range m.Mem.Aux[0x4000:0xC000] {
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
