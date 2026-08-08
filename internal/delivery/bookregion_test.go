package delivery

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/internal/rwts"
)

// TestBlockOffsetsMatchTheCanonicalSkew pins BlockOffsets — which mirrors
// ProRWTS2's own seekrdwr arithmetic — against the CANONICAL ProDOS-block to
// DOS-3.3-sector table, written here from the published mapping rather than
// derived from the same code. If the driver's translation and the builder's
// ever disagree, the end-to-end boot gate fails with garbage; this one fails
// with a block number.
func TestBlockOffsetsMatchTheCanonicalSkew(t *testing.T) {
	// ProDOS block b lives in track b/8; its two 256-byte halves are DOS 3.3
	// logical sectors canon[b%8][0] and canon[b%8][1], in that read order.
	canon := [8][2]int{{0, 14}, {13, 12}, {11, 10}, {9, 8}, {7, 6}, {5, 4}, {3, 2}, {1, 15}}
	for b := range blocksPerDisk {
		want := [2]int{
			(b/8*SectorsPerTrk + canon[b%8][0]) * SectorBytes,
			(b/8*SectorsPerTrk + canon[b%8][1]) * SectorBytes,
		}
		if got := BlockOffsets(b); got != want {
			t.Fatalf("block %d: BlockOffsets = %v, canonical = %v", b, got, want)
		}
	}
	t.Logf("all %d blocks agree with the canonical ProDOS/DOS-3.3 skew", blocksPerDisk)
}

// TestBookRegionRoundTrip writes the region into a blank disk image and reads
// every file back through the directory chain, exactly as the driver walks it.
func TestBookRegionRoundTrip(t *testing.T) {
	big, err := os.ReadFile(filepath.Join("..", "..", "internal", "book", "bigbook.bin"))
	if err != nil {
		t.Fatalf("%v (run `go run ./cmd/genbook`)", err)
	}
	dsk := make([]byte, SectorsPerDisk*SectorBytes)
	if err := writeBookRegion(dsk, big); err != nil {
		t.Fatal(err)
	}
	padded := make([]byte, BookFiles*BookFileBytes)
	copy(padded, big)
	for i := range BookFiles {
		got, err := ExtractBookFile(dsk, i)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, padded[i*BookFileBytes:(i+1)*BookFileBytes]) {
			t.Errorf("%s does not read back through the directory chain", BookFileName(i))
		}
	}
	// The directory really is where the DRIVER will look: cmd/genrwts baked
	// rwts.DirBlock into the blob's unrblocklo/hi operands; a drift between
	// that constant and the builder's would read an empty directory.
	if BookDirBlock != rwts.DirBlock {
		t.Errorf("delivery.BookDirBlock = %d but rwts.DirBlock = %d", BookDirBlock, rwts.DirBlock)
	}
	t.Logf("book region: dir block %d, %d files x %d B, %d sectors total",
		BookDirBlock, BookFiles, BookFileBytes, BookRegionSectors())
}

// TestBookRegionDoesNotTouchTheSDRegion: the ProRWTS2-shaped region and the
// Standard Delivery stages must be disjoint SETS OF SECTORS, computed from
// the real artefacts. delivery.Build refuses to build on overlap; this is the
// same check where `go test` can see it, plus the disk-bounds check.
func TestBookRegionDoesNotTouchTheSDRegion(t *testing.T) {
	stage1, stage2, err := LoadStages("../..")
	if err != nil {
		t.Fatal(err)
	}
	big, err := os.ReadFile(filepath.Join("..", "..", "internal", "book", "bigbook.bin"))
	if err != nil {
		t.Fatal(err)
	}
	sd := map[int]string{0: "boot sector"}
	for i := range stage1.Sectors() + stage2.Sectors() {
		sd[SectorOffset(i)] = "SD stage sector"
	}
	offs, err := BookRegionOffsets(big)
	if err != nil {
		t.Fatal(err)
	}
	minTrack, maxTrack := Tracks, 0
	for _, off := range offs {
		if what, clash := sd[off]; clash {
			t.Errorf("book region sector at offset %#x collides with a %s", off, what)
		}
		if off < 0 || off+SectorBytes > SectorsPerDisk*SectorBytes {
			t.Errorf("book region sector at offset %#x is outside the disk", off)
		}
		track := off / SectorBytes / SectorsPerTrk
		minTrack = min(minTrack, track)
		maxTrack = max(maxTrack, track)
	}
	t.Logf("SD stages: %d sectors (tracks 0-%d); book region: %d sectors in tracks %d-%d",
		1+stage1.Sectors()+stage2.Sectors(),
		(stage1.Sectors()+stage2.Sectors())/SectorsPerTrk, len(offs), minTrack, maxTrack)
}
