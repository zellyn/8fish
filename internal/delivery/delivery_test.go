package delivery

import (
	"strings"
	"testing"
)

// TestBaseTradeoff re-derives the staging-base decision table from the REAL
// file sizes rather than restating it. Both margins move 256 bytes in opposite
// directions per page, $0C00 is the lowest base that fits, and $0F00 already
// overruns the book.
//
// The table describes the SIMPLE SINGLE-SHOT layout only. When it runs out,
// the answer is a different loader (chain-load, or ProRWTS2), not a smaller
// program — it is our disk and it can do whatever we want. What this test
// protects is that, for as long as the simple path is the one in use, the base
// is the best available choice rather than a historical one.
func TestBaseTradeoff(t *testing.T) {
	var firstFit, lastRoom int
	t.Logf("%-6s %8s %9s %15s", "base", "image", "SD spare", "UI growth room")
	for base := 0x0800; base <= 0x1000; base += 0x100 {
		pieces, err := LoadAt("../..", base)
		if err != nil {
			t.Skipf("SKIP: %v", err)
		}
		_, l, err := Image(pieces)
		if err != nil {
			// Past the point where the payload runs into the book, the
			// layout is not merely tight, it is invalid.
			t.Logf("$%04X  %s", base, err)
			continue
		}
		t.Logf("$%04X  %8d %9d %15d", base, l.ImageBytes, l.SDSpare, l.UIRoom)
		if l.SDSpare >= 0 && firstFit == 0 {
			firstFit = base
		}
		if l.UIRoom >= 0 {
			lastRoom = base
		}
	}
	if firstFit != Base {
		t.Errorf("the lowest base that fits `diskii mksd` is $%04X, but Base is $%04X. "+
			"Base should be the lowest one that fits: every page higher costs the UI "+
			"256 bytes of room to grow.", firstFit, Base)
	}
	if lastRoom < Base {
		t.Errorf("Base $%04X already overruns the book at $%04X", Base, BookOrg)
	}
}

// TestMarginsCanFail is the gate on the gate. A ledger that cannot go negative
// would pass forever and warn about nothing, so both failure modes are
// exercised with synthetic pieces.
func TestMarginsCanFail(t *testing.T) {
	// MARGIN 1: an engine that grows past the cap.
	big := []Piece{
		{Path: "copier", Org: Base, Data: make([]byte, 57)},
		{Path: "payload", Org: Base + 256, Data: make([]byte, 4222), Staged: true},
		{Path: "book", Org: BookOrg, Data: make([]byte, 7407)},
		{Path: "engine", Org: EngineOrg, Data: make([]byte, MaxImage)},
	}
	_, l, err := Image(big)
	if err != nil {
		t.Fatal(err)
	}
	if l.SDSpare >= 0 {
		t.Errorf("a %d-byte image reports SD spare %d, want negative", l.ImageBytes, l.SDSpare)
	}
	if _, err := Build(t.TempDir(), t.TempDir()+"/x.img", t.TempDir()+"/x.dsk"); err == nil {
		t.Error("Build with no pieces on disk should fail")
	}

	// MARGIN 2: a UI payload that grows into the book. With the book present
	// the layout is outright invalid...
	fat := []Piece{
		{Path: "copier", Org: Base, Data: make([]byte, 57)},
		{Path: "payload", Org: Base + 256, Data: make([]byte, BookOrg-Base-256+1), Staged: true},
		{Path: "book", Org: BookOrg, Data: make([]byte, 7407)},
	}
	if _, _, err := Image(fat); err == nil {
		t.Error("a payload that runs into the book should not lay out at all")
	} else if !strings.Contains(err.Error(), "overlaps") {
		t.Errorf("unexpected error: %v", err)
	}

	// ...and the ledger reports the same overrun as a negative margin.
	fat[1].Data = make([]byte, BookOrg-Base-256+100)
	if _, l, err := Image(fat[:2]); err != nil {
		t.Fatal(err)
	} else if l.UIRoom != -100 {
		t.Errorf("UI growth room = %d, want -100", l.UIRoom)
	}
}

// TestSectorOffset checks the Standard Delivery interleave is a bijection onto
// the disk's sectors, skipping the boot sector at track 0 sector 0. A formula
// that mapped two image pages to one sector would silently drop half the
// program; TestDiskRoundTrip would catch it, but only after building a disk.
func TestSectorOffset(t *testing.T) {
	const pages = MaxImage / SectorBytes
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
	t.Logf("%d pages (%d B) occupy tracks 0-%d of %d", pages, MaxImage, maxTrack, Tracks-1)
	if maxTrack >= Tracks {
		t.Errorf("the image reaches track %d, past the last track %d", maxTrack, Tracks-1)
	}
}
