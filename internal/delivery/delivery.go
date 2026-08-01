// Package delivery builds the bootable 8fish disk: it lays the payload out as
// SPANS grouped into STAGES and hands them to peterferrie's "Standard
// Delivery" boot loader via `diskii mksd`.
//
// WHY STAGES AND SPANS. Standard Delivery is a boot sector plus a fast loader.
// Its loader is a PAGE TABLE at $084F-$08FF in the boot sector: one destination
// page per sector read, in disk order, $C0 terminating, self-modified through
// the low byte of the `LDA $084E` at $0805 (which is pre-incremented, so the
// FIRST entry read is $084F, not $084E). That table is 177 bytes; one is the
// terminator, so
//
//	ONE PAGE TABLE = 176 SECTORS = 45,056 BYTES. EVER.
//
// Two consequences, and they point in opposite directions:
//
//   - The table is a LIST OF PAGES, not a base address. Nothing requires the
//     pages to be consecutive. A stage can therefore scatter-load DISJOINT
//     spans with no wasted sectors — `diskii mksd` writes a consecutive table
//     only because it is handed one contiguous image, and we overwrite it.
//   - 176 sectors is an absolute ceiling on ONE table, and 8fish is past it.
//     The exact overshoot is not written down here on purpose -- it moves
//     every time anything grows -- but internal/ui's TestDiskLedger computes
//     it from the real files on every run and FAILS if it ever goes negative,
//     because at that point the chain load has stopped being load-bearing and
//     collapsing back to one stage would be simpler and faster to boot.
//
// The way through is the loader's own survival. Our image starts at $0D00, so
// the boot sector's code at $0800-$08FF is never overwritten and its sequential
// read state is intact when it jumps to the copier:
//
//	$26 = $00        read buffer low            (set once at $C659)
//	$41              current track              (checked against every address
//	                                             field the ROM reads)
//	$2B = slot<<4    and X = $2B on entry
//	Y = last sector + 1                          (the ROM's `JMP $0801` lands on
//	                                              a TAY, so Y is the loop input)
//	$0800 = $01      the ROM's "one sector per call" count
//
// So the copier can write a FRESH page table into page $08, reset the
// self-modified pointer at $0806 to $4E, repoint the terminator's `JMP` at
// $084D/$084E, restore X and Y, and `JMP $0802`. The read continues from the
// next sector on the disk. No hand-rolled sector reader, no nibble code,
// nothing that could ever write to a disk. asm/m8.s does exactly that.
//
// THE SHIPPING LAYOUT
//
//	STAGE 1 (the boot sector's own table, patched by this package)
//	  $0D00  asm/m8sdboot.bin    the run-once copier + the chain loader
//	  $0E00  asm/m8.bin          the UI payload (staged; lifted to $E000)
//	  $4000  asm/engine.bin      the engine
//
//	STAGE 2 (table written by the copier, re-entering the loader at $0802)
//	  $0E00  tileblob.bin        the DHGR artwork, then copied to LC bank 1
//	  $2000  bookblob.bin        the opening book, then copied to AUX $0200
//
// Stage 2 lands in MAIN and is copied on, rather than being read straight into
// the Language Card or into aux. That is not caution, it is the boot ROM: its
// denibblise pass READS THE BUFFER BACK (`lda ($26),y` at $C6D9). Reading back
// a Language Card destination while ROM is banked in for $FCA8 (WAIT, which
// the loader's track step calls) returns ROM; reading back an aux destination
// with RAMRD off returns main. Either way the second pass would denibblise
// garbage over the first. Staging through main costs one 8-page copy and one
// 29-page copy — about 90 ms, against a 6-second boot — and it is immune to
// where the stage happens to fall on the disk.
//
// WHY $0E00 IS FREE FOR STAGE 2. It is where stage 1 staged the UI payload,
// and the copier lifts that to $E000 BEFORE re-entering the loader. $2000 is
// free for the same reason the book can leave it: nothing has run yet.
//
// WHAT IS NO LONGER A CONSTRAINT. The old layout's two margins — "SD spare"
// (the image must fit 45,056 B) and "UI growth room" (the staged payload must
// end below the book at $2000) — were both properties of the single-shot,
// single-span arrangement. With the book delivered in stage 2 the payload has
// the whole $0E00-$3FFF window to stage into, capped by its own $E000-$F6FF
// Language Card budget long before it runs out of staging room; and with two
// tables the 176-sector ceiling applies per stage, not to the program.
package delivery

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The delivery layout. Addresses are load addresses in main RAM.
const (
	// Base is the first byte of the delivered image, and the address
	// `diskii mksd --address` is given. It must be page-aligned. It is also
	// where execution starts.
	Base = 0x0D00
	// CopierOrg is where m8sdboot.bin lands (and where execution starts).
	CopierOrg = Base
	// PayloadOrg is where m8.bin is staged before the copier lifts it to
	// $E000. asm/m8sd.cfg defines the same value as the UIPAYLOAD symbol.
	PayloadOrg = Base + SectorBytes
	// TilesOrg is where stage 2 lands the DHGR tile blob, before the copier
	// copies it into Language Card bank 1. It deliberately REUSES the
	// payload's staging area, which is dead by then.
	TilesOrg = 0x0E00
	// BookOrg is where stage 2 lands the opening book, before the copier
	// copies it to AUX $0200. It is main hi-res page 1, which the DHGR
	// renderer needs and which is therefore not the book's home any more.
	BookOrg = 0x2000
	// BookAux is the book's RESIDENT home: auxiliary RAM $0200. The blob is
	// 7,407 B and aux $0200-$1FFF is 7,680 B, so it fits below the DHGR aux
	// half at $2000 with 273 B to spare. Keep equal to asm/book.inc's
	// BOOK_BASE and internal/book.BaseAddr.
	BookAux = 0x0200
	// EngineOrg is where engine.bin runs.
	EngineOrg = 0x4000
	// TilesLC is where the copier installs the tile blob: Language Card
	// bank 1, above the engine's 65-byte LCCODE at $D000. Keep equal to
	// asm/m8.s's DHTILES.
	TilesLC = 0xD300

	// MaxImage is the largest image `diskii mksd` accepts, and it is not an
	// arbitrary tool limit: it is 176 * 256, the boot sector's page table
	// filled to the last byte.
	MaxImage = MaxStageSectors * SectorBytes
)

// Disk geometry of a 5.25" .dsk: 35 tracks of 16 sectors of 256 bytes.
const (
	Tracks         = 35
	SectorsPerTrk  = 16
	SectorBytes    = 256
	SectorsPerDisk = Tracks * SectorsPerTrk

	// MaxStageSectors is how many sectors ONE page table can name: the table
	// runs $084F-$08FF (177 bytes) and the last usable byte must be the $C0
	// terminator. Asserted against the real boot sector by
	// internal/ui's TestBootSectorPageTable.
	MaxStageSectors = 176

	// bootTableOff is the offset of the page table's FIRST entry inside the
	// boot sector ($084F - $0800). bootJmpOff is the offset of the operand of
	// the `JMP` the terminator branches to ($084D - $0800).
	bootTableOff = 0x4F
	bootJmpOff   = 0x4D
	// tableTerm is the page table's terminator byte.
	tableTerm = 0xC0
)

// A Piece is one file placed into a stage at a fixed address.
type Piece struct {
	Path string // relative to the repo root
	Org  int    // load address
	Data []byte // filled in by Load
	// Staged marks a piece that is not delivered to the address it runs at:
	// the UI payload (lifted to $E000), the tile blob (lifted to LC bank 1)
	// and the book (lifted to aux $0200).
	Staged bool
}

// A Span is one contiguous, page-aligned run of the delivered image. Spans are
// what the page table actually names; pieces that abut merge into one.
type Span struct {
	Org  int
	Data []byte // length is a multiple of SectorBytes
}

// Sectors is how many disk sectors the span occupies.
func (s Span) Sectors() int { return len(s.Data) / SectorBytes }

// A Stage is one page table's worth of spans: everything one entry into the
// loader delivers.
type Stage struct {
	Name  string
	Spans []Span
}

// Sectors is how many disk sectors the stage occupies.
func (s Stage) Sectors() int {
	n := 0
	for _, sp := range s.Spans {
		n += sp.Sectors()
	}
	return n
}

// Bytes is the stage's sector data, in disk order.
func (s Stage) Bytes() []byte {
	var b []byte
	for _, sp := range s.Spans {
		b = append(b, sp.Data...)
	}
	return b
}

// PageTable is the loader's table for this stage: one destination page per
// sector, in disk order, terminated by $C0.
func (s Stage) PageTable() []byte {
	var t []byte
	for _, sp := range s.Spans {
		for i := range sp.Sectors() {
			t = append(t, byte(sp.Org>>8)+byte(i))
		}
	}
	return append(t, tableTerm)
}

// Check reports whether the stage is deliverable: page-aligned spans, no
// overlapping destinations, and within the one-table sector ceiling.
func (s Stage) Check() error {
	seen := map[byte]string{}
	for _, sp := range s.Spans {
		if sp.Org%SectorBytes != 0 {
			return fmt.Errorf("delivery: %s span at $%04X is not page aligned", s.Name, sp.Org)
		}
		if sp.Org+len(sp.Data) > 0xC000 {
			return fmt.Errorf("delivery: %s span $%04X+%d runs into the $C000 I/O page",
				s.Name, sp.Org, len(sp.Data))
		}
		for i := range sp.Sectors() {
			p := byte(sp.Org>>8) + byte(i)
			if prev, dup := seen[p]; dup {
				return fmt.Errorf("delivery: %s delivers page $%02X twice (%s and $%04X)",
					s.Name, p, prev, sp.Org)
			}
			seen[p] = fmt.Sprintf("$%04X", sp.Org)
		}
	}
	if n := s.Sectors(); n > MaxStageSectors {
		return fmt.Errorf("delivery: %s needs %d sectors, %d more than ONE Standard Delivery "+
			"page table can name (%d). Split it: the loader survives the load and can be "+
			"re-entered with a fresh table (see this package's doc)",
			s.Name, n, n-MaxStageSectors, MaxStageSectors)
	}
	return nil
}

// Stage1Pieces returns the pieces the BOOT SECTOR's own page table delivers.
func Stage1Pieces() []Piece {
	return []Piece{
		{Path: filepath.Join("asm", "m8sdboot.bin"), Org: CopierOrg},
		{Path: filepath.Join("asm", "m8.bin"), Org: PayloadOrg, Staged: true},
		{Path: filepath.Join("asm", "engine.bin"), Org: EngineOrg},
	}
}

// Stage2Pieces returns the pieces the COPIER's page table delivers, after it
// has re-entered the surviving loader.
func Stage2Pieces() []Piece {
	return []Piece{
		{Path: filepath.Join("internal", "tiles", "tileblob.bin"), Org: TilesOrg, Staged: true},
		{Path: filepath.Join("internal", "book", "bookblob.bin"), Org: BookOrg, Staged: true},
	}
}

// Pieces returns every piece of the delivery, stage 1 then stage 2.
func Pieces() []Piece { return append(Stage1Pieces(), Stage2Pieces()...) }

// Load reads the pieces of one stage from the repo rooted at root.
func Load(root string, pieces []Piece) ([]Piece, error) {
	out := append([]Piece(nil), pieces...)
	for i := range out {
		b, err := os.ReadFile(filepath.Join(root, out[i].Path))
		if err != nil {
			return nil, fmt.Errorf("delivery: %w (run `make dsk`)", err)
		}
		out[i].Data = b
	}
	return out, nil
}

// SpansOf turns loaded pieces into page-aligned spans, merging pieces that
// abut and zero-padding each span to a whole number of sectors. Pieces must be
// in ascending address order and must not overlap.
func SpansOf(pieces []Piece) ([]Span, error) {
	var spans []Span
	for _, p := range pieces {
		if p.Org%SectorBytes != 0 {
			return nil, fmt.Errorf("delivery: %s loads at $%04X, which is not page aligned",
				p.Path, p.Org)
		}
		if n := len(spans); n > 0 {
			last := &spans[n-1]
			end := last.Org + len(last.Data)
			if p.Org < end {
				return nil, fmt.Errorf("delivery: %s at $%04X overlaps the piece before it, "+
					"which ends at $%04X", p.Path, p.Org, end)
			}
			if p.Org == end {
				last.Data = append(last.Data, p.Data...)
				continue
			}
		}
		spans = append(spans, Span{Org: p.Org, Data: append([]byte(nil), p.Data...)})
	}
	for i := range spans {
		if n := len(spans[i].Data) % SectorBytes; n != 0 {
			spans[i].Data = append(spans[i].Data, make([]byte, SectorBytes-n)...)
		}
	}
	return spans, nil
}

// LoadStages reads every piece and groups it into the two shipping stages.
func LoadStages(root string) (stage1, stage2 Stage, err error) {
	build := func(name string, ps []Piece) (Stage, error) {
		loaded, err := Load(root, ps)
		if err != nil {
			return Stage{}, err
		}
		spans, err := SpansOf(loaded)
		if err != nil {
			return Stage{}, err
		}
		s := Stage{Name: name, Spans: spans}
		return s, s.Check()
	}
	if stage1, err = build("stage 1", Stage1Pieces()); err != nil {
		return
	}
	stage2, err = build("stage 2", Stage2Pieces())
	return
}

// Ledger is the delivery accounting for one built disk. internal/ui's
// TestDiskLedger asserts it and prints it in full.
type Ledger struct {
	Stage1Sectors int
	Stage2Sectors int
	// Stage1Spare is how many more sectors stage 1's page table could name.
	Stage1Spare int
	Stage2Spare int
	// TotalSectors counts the boot sector too, i.e. everything the disk uses.
	TotalSectors int
	// DiskSpare is how many of the disk's 560 sectors are still empty.
	DiskSpare int
	// PayloadOrg / PayloadEnd bracket the staged UI payload; PayloadRoom is
	// how far it could still grow before it collided with the ENGINE at
	// EngineOrg, which is stage 1's next span.
	//
	// It is NOT measured against $2000 any more, and that is the single
	// biggest thing the chain load bought. The old layout staged the payload
	// under a book that was already resident at $2000, so 164 bytes of
	// staging room was the wall the UI kept running into. The book is
	// delivered in stage 2 now, AFTER the copier has lifted the payload to
	// $E000, so the two share $2000-$21FF quite happily -- at different
	// times. The cap that actually binds is the UI's own Language Card
	// budget ($E000-$F6FF, 5,888 B), which the link config enforces as a
	// link error.
	PayloadOrg  int
	PayloadEnd  int
	PayloadRoom int
}

func (l Ledger) String() string {
	return fmt.Sprintf(
		"stage 1 %d/%d sectors, stage 2 %d/%d sectors, %d of %d disk sectors used "+
			"(staged payload $%04X-$%04X, %d B below the engine)",
		l.Stage1Sectors, MaxStageSectors, l.Stage2Sectors, MaxStageSectors,
		l.TotalSectors, SectorsPerDisk, l.PayloadOrg, l.PayloadEnd-1, l.PayloadRoom)
}

// LedgerOf computes the ledger for a pair of stages.
func LedgerOf(stage1, stage2 Stage) Ledger {
	l := Ledger{
		Stage1Sectors: stage1.Sectors(),
		Stage2Sectors: stage2.Sectors(),
	}
	l.Stage1Spare = MaxStageSectors - l.Stage1Sectors
	l.Stage2Spare = MaxStageSectors - l.Stage2Sectors
	l.TotalSectors = 1 + l.Stage1Sectors + l.Stage2Sectors
	l.DiskSpare = SectorsPerDisk - l.TotalSectors
	for _, sp := range stage1.Spans {
		if sp.Org == PayloadOrg || (sp.Org == CopierOrg && len(sp.Data) > SectorBytes) {
			l.PayloadOrg = PayloadOrg
			l.PayloadEnd = sp.Org + len(sp.Data)
			l.PayloadRoom = EngineOrg - l.PayloadEnd
		}
	}
	return l
}

// SectorOffset returns the byte offset in a DOS-order .dsk of the sector
// holding image page n (n = 0 is the first payload page, i.e. the sector right
// after the boot sector).
//
// The loader reads PHYSICAL sectors 0, 2, 4, ... 14, 1, 3, ... 15 — a 2:1
// interleave, because the ROM's `INC $3D` plus the loop's own `INY` advance
// the sector number by two. Through the DOS 3.3 soft skew that `diskii mksd`
// writes, that comes out as DOS-order sector 0, then 14, 13, ... 1, then 15,
// one track at a time.
//
// This is a property of the loader on the disk, not a guess: TestDiskRoundTrip
// checks every page of the real image against it, and TestDiskBoots checks the
// bytes the loader actually delivers into RAM.
func SectorOffset(n int) int {
	k := n + 1
	track, j := k/SectorsPerTrk, k%SectorsPerTrk
	var sector int
	switch j {
	case 0:
		sector = 0
	case SectorsPerTrk - 1:
		sector = SectorsPerTrk - 1
	default:
		sector = SectorsPerTrk - 1 - j
	}
	return (track*SectorsPerTrk + sector) * SectorBytes
}

// ExtractPages reads count image pages back out of a .dsk, starting at image
// page first: the inverse of what the loader reads, sector by sector.
func ExtractPages(dsk []byte, first, count int) ([]byte, error) {
	if len(dsk) != SectorsPerDisk*SectorBytes {
		return nil, fmt.Errorf("delivery: bad .dsk size %d, want %d", len(dsk), SectorsPerDisk*SectorBytes)
	}
	out := make([]byte, 0, count*SectorBytes)
	for n := first; n < first+count; n++ {
		off := SectorOffset(n)
		if off+SectorBytes > len(dsk) {
			return nil, fmt.Errorf("delivery: image page %d maps past the end of the disk", n)
		}
		out = append(out, dsk[off:off+SectorBytes]...)
	}
	return out, nil
}

// ErrNoDiskii reports that the `diskii` tool is not on PATH.
var ErrNoDiskii = errors.New(
	"delivery: `diskii` not found on PATH. It builds the Standard Delivery boot disk; install it with\n" +
		"    go install github.com/zellyn/diskii@latest\n" +
		"(and make sure $(go env GOPATH)/bin is on PATH)")

// Build assembles both stages, writes stage 1's sector image to imgPath, runs
// `diskii mksd` to make the bootable disk, patches the boot sector's page
// table to stage 1's actual span list, and writes stage 2's sectors after it.
// It returns the ledger it built.
func Build(root, imgPath, dskPath string) (Ledger, error) {
	stage1, stage2, err := LoadStages(root)
	if err != nil {
		return Ledger{}, err
	}
	l := LedgerOf(stage1, stage2)
	if l.TotalSectors > SectorsPerDisk {
		return l, fmt.Errorf("delivery: the two stages need %d sectors, %d more than a "+
			"35-track disk holds", l.TotalSectors, l.TotalSectors-SectorsPerDisk)
	}
	if _, err := exec.LookPath("diskii"); err != nil {
		return l, ErrNoDiskii
	}
	img := stage1.Bytes()
	if err := os.WriteFile(imgPath, img, 0o644); err != nil {
		return l, err
	}
	// mksd appends to an existing image rather than replacing it, so start clean.
	if err := os.Remove(dskPath); err != nil && !os.IsNotExist(err) {
		return l, err
	}
	cmd := exec.Command("diskii", "mksd", dskPath, imgPath,
		fmt.Sprintf("--address=%#04x", Base),
		fmt.Sprintf("--start=%#04x", CopierOrg))
	if out, err := cmd.CombinedOutput(); err != nil {
		return l, fmt.Errorf("delivery: %s: %w\n%s", cmd, err, bytes.TrimSpace(out))
	}
	dsk, err := os.ReadFile(dskPath)
	if err != nil {
		return l, err
	}
	if len(dsk) != SectorsPerDisk*SectorBytes {
		return l, fmt.Errorf("delivery: %s is %d B, want %d", dskPath, len(dsk),
			SectorsPerDisk*SectorBytes)
	}
	// PATCH THE PAGE TABLE. mksd writes a consecutive table because we handed
	// it one flat image; the real stage 1 is two disjoint spans (the copier +
	// payload at $0D00, the engine at $4000), and the table is a list of
	// pages, so scatter-loading them costs nothing but these bytes.
	if err := patchPageTable(dsk, stage1, CopierOrg); err != nil {
		return l, err
	}
	// STAGE 2's sectors follow stage 1's on the disk, in the same interleave;
	// the copier's own page table names their destinations.
	s2 := stage2.Bytes()
	for i := range stage2.Sectors() {
		off := SectorOffset(l.Stage1Sectors + i)
		copy(dsk[off:off+SectorBytes], s2[i*SectorBytes:(i+1)*SectorBytes])
	}
	if err := os.WriteFile(dskPath, dsk, 0o644); err != nil {
		return l, err
	}
	// Round-trip immediately: a disk that does not read back as the stages we
	// handed it is not a disk we should ship.
	for _, c := range []struct {
		name  string
		first int
		want  []byte
	}{
		{"stage 1", 0, img},
		{"stage 2", l.Stage1Sectors, s2},
	} {
		back, err := ExtractPages(dsk, c.first, len(c.want)/SectorBytes)
		if err != nil {
			return l, err
		}
		if !bytes.Equal(back, c.want) {
			return l, fmt.Errorf("delivery: %s does not read back as %s", dskPath, c.name)
		}
	}
	return l, nil
}

// patchPageTable rewrites the boot sector's page table (and the start address
// its terminator jumps to) from a stage's real span list.
func patchPageTable(dsk []byte, s Stage, start int) error {
	tab := s.PageTable()
	if bootTableOff+len(tab) > SectorBytes {
		return fmt.Errorf("delivery: %s needs a %d-byte page table, %d B more than the "+
			"boot sector's $%04X-$08FF holds", s.Name, len(tab),
			bootTableOff+len(tab)-SectorBytes, 0x0800+bootTableOff)
	}
	boot := dsk[0:SectorBytes]
	if boot[bootJmpOff-1] != 0x4C {
		return fmt.Errorf("delivery: boot sector $%04X is $%02X, not a JMP ($4C): this is not "+
			"the Standard Delivery loader this package knows how to patch",
			0x0800+bootJmpOff-1, boot[bootJmpOff-1])
	}
	boot[bootJmpOff] = byte(start)
	boot[bootJmpOff+1] = byte(start >> 8)
	for i := bootTableOff; i < SectorBytes; i++ {
		boot[i] = 0
	}
	copy(boot[bootTableOff:], tab)
	return nil
}
