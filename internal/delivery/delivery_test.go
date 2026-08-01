package delivery

import (
	"bytes"
	"strings"
	"testing"
)

// TestStageCeilingIsTheRealCeiling. `diskii mksd`'s 45,056-byte limit is not
// an arbitrary tool policy: it is 176 * 256, one Standard Delivery page table
// filled to its last usable byte. Assert the two agree, so that MaxImage can
// never be "fixed" by editing a constant.
func TestStageCeilingIsTheRealCeiling(t *testing.T) {
	// $084F..$08FF is 177 bytes; one of them must be the $C0 terminator.
	if got := SectorBytes - bootTableOff - 1; got != MaxStageSectors {
		t.Errorf("the page table at $%04X-$08FF holds %d entries plus a terminator, "+
			"but MaxStageSectors is %d", 0x0800+bootTableOff, got, MaxStageSectors)
	}
	// And it is the number `diskii mksd` enforces. That agreement is the
	// whole point of the assertion: the tool's "44 KB cap" was treated for
	// months as an arbitrary policy, and it is not -- it is this table,
	// counted. (Comparing MaxImage against its own definition would prove
	// nothing, so compare against the tool's documented figure instead.)
	const diskiiCap = 45056
	if MaxImage != diskiiCap {
		t.Errorf("MaxImage is %d but `diskii mksd` refuses over %d: if the tool's cap "+
			"and one page table have stopped being the same number, one of them is "+
			"not what this package thinks it is", MaxImage, diskiiCap)
	}
}

// TestOverfullStageSaysSplitIt is the gate on the gate: a stage that outgrows
// one page table must fail, and must say what to do about it.
func TestOverfullStageSaysSplitIt(t *testing.T) {
	// Two spans, not one: a single span of more than 256 sectors would trip
	// the duplicate-page check first (page numbers are bytes and wrap), and
	// the message this test is about is the ceiling one.
	s := Stage{Name: "stage 1", Spans: []Span{
		{Org: 0x0200, Data: make([]byte, 100*SectorBytes)},
		{Org: 0x6600, Data: make([]byte, (MaxStageSectors+1-100)*SectorBytes)},
	}}
	err := s.Check()
	if err == nil {
		t.Fatalf("a %d-sector stage passed Check", s.Sectors())
	}
	if !strings.Contains(err.Error(), "re-entered with a fresh table") {
		t.Errorf("the over-ceiling message does not point at the chain load: %v", err)
	}
	// One sector fewer is exactly the ceiling, and must pass.
	s.Spans[1].Data = s.Spans[1].Data[:(MaxStageSectors-100)*SectorBytes]
	if err := s.Check(); err != nil {
		t.Errorf("a full-but-legal %d-sector stage failed Check: %v", s.Sectors(), err)
	}
}

// TestSpansScatterWithNoWastedSectors is the whole reason the chain load is
// affordable. The page table is a LIST OF PAGES, so a stage whose pieces have
// a hole between them costs nothing for the hole: two spans, no gap sectors,
// and a page table that jumps.
func TestSpansScatterWithNoWastedSectors(t *testing.T) {
	spans, err := SpansOf([]Piece{
		{Path: "low", Org: 0x0D00, Data: make([]byte, 0x300)},
		{Path: "high", Org: 0x4000, Data: make([]byte, 0x200)},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := Stage{Name: "t", Spans: spans}
	if got := st.Sectors(); got != 5 {
		t.Errorf("two spans of 3 and 2 pages occupy %d sectors, want 5 (a contiguous "+
			"image would have cost %d)", got, (0x4200-0x0D00)/SectorBytes)
	}
	want := []byte{0x0D, 0x0E, 0x0F, 0x40, 0x41, tableTerm}
	if got := st.PageTable(); !bytes.Equal(got, want) {
		t.Errorf("page table = % 02X, want % 02X", got, want)
	}
	if err := st.Check(); err != nil {
		t.Error(err)
	}
}

// TestAbuttingPiecesMerge: the copier and the payload it stages are adjacent,
// so they are one span and one run of table entries, not two.
func TestAbuttingPiecesMerge(t *testing.T) {
	spans, err := SpansOf([]Piece{
		{Path: "copier", Org: CopierOrg, Data: make([]byte, SectorBytes)},
		{Path: "payload", Org: PayloadOrg, Data: make([]byte, 300)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 1 || spans[0].Org != CopierOrg || spans[0].Sectors() != 3 {
		t.Fatalf("got %d spans (%+v), want one 3-sector span at $%04X",
			len(spans), spans, CopierOrg)
	}
}

// TestOverlapAndAlignmentAreRefused: a piece that lands on another, or off a
// page boundary, is a delivery that would arrive scrambled.
func TestOverlapAndAlignmentAreRefused(t *testing.T) {
	if _, err := SpansOf([]Piece{
		{Path: "a", Org: 0x1000, Data: make([]byte, 0x200)},
		{Path: "b", Org: 0x1100, Data: make([]byte, 0x100)},
	}); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Errorf("overlapping pieces: err = %v", err)
	}
	if _, err := SpansOf([]Piece{
		{Path: "a", Org: 0x1080, Data: make([]byte, 0x100)},
	}); err == nil || !strings.Contains(err.Error(), "page aligned") {
		t.Errorf("misaligned piece: err = %v", err)
	}
	s := Stage{Name: "t", Spans: []Span{
		{Org: 0x2000, Data: make([]byte, 0x200)},
		{Org: 0x2100, Data: make([]byte, 0x100)},
	}}
	if err := s.Check(); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a stage delivering one page twice: err = %v", err)
	}
}

// TestSectorOffset checks the Standard Delivery interleave is a bijection onto
// the disk's sectors, skipping the boot sector at track 0 sector 0. A formula
// that mapped two image pages to one sector would silently drop half the
// program; TestDiskRoundTrip would catch it, but only after building a disk.
func TestSectorOffset(t *testing.T) {
	const pages = SectorsPerDisk - 1
	seen := map[int]int{}
	for n := range pages {
		off := SectorOffset(n)
		if off == 0 {
			t.Errorf("image page %d maps onto the boot sector (track 0 sector 0)", n)
		}
		if off%SectorBytes != 0 {
			t.Errorf("image page %d maps to offset %d, which is not sector aligned", n, off)
		}
		if prev, ok := seen[off]; ok {
			t.Fatalf("image pages %d and %d both map to .dsk offset %d", prev, n, off)
		}
		seen[off] = n
	}
	if len(seen) != pages {
		t.Errorf("%d pages mapped to %d distinct sectors", pages, len(seen))
	}
	// The whole 44 KB fits inside the 35 tracks, with room to spare.
	maxTrack := 0
	for off := range seen {
		if tr := off / (SectorsPerTrk * SectorBytes); tr > maxTrack {
			maxTrack = tr
		}
	}
	t.Logf("%d pages (%d B) occupy tracks 0-%d of %d", pages, pages*SectorBytes, maxTrack, Tracks-1)
	if maxTrack >= Tracks {
		t.Errorf("the image reaches track %d, past the last track %d", maxTrack, Tracks-1)
	}
}
