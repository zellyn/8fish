package delivery

// bookregion.go builds the PRORWTS2-SHAPED region of the 8fish disk: the
// directory chain, index blocks and data blocks the resident read-only driver
// walks to load the BIG BOOK (docs/prorwts2-design.md §5).
//
// This is not a legal, mountable ProDOS volume, and does not have to be.
// The driver walks a directory chain whose STARTING BLOCK is baked into it
// at build time (rwts.DirBlock), and with fixed in-place reads it never
// touches the volume bitmap or blocks 2-6 — so the classic collision between
// ProDOS metadata (track 0) and Standard Delivery's stage-1 sectors never
// happens. The whole region lives in tracks the SD stages don't reach, and
// TestBookRegionDoesNotTouchTheSDRegion is where that is enforced.

import (
	"bytes"
	"fmt"

	"github.com/zellyn/chess6502/internal/rwts"
)

// The region's layout in ProDOS block numbers. A block is 512 bytes = two
// disk sectors; a 35-track disk holds blocks 0-279 (8 per track).
const (
	// BookDirBlock is the directory KEY block: one block, whose entries name
	// the four book files. Baked into the driver blob by cmd/genrwts.
	BookDirBlock = rwts.DirBlock
	// BookFiles is how many files the big book is split across. Each is read
	// with one driver call into the 8 KB staging window at main $2000-$3FFF
	// (the DHGR main half — the largest dead window below the engine).
	BookFiles = 4
	// BookFileBytes is each file's exact size: 16 blocks. The driver's
	// aligned_read build reads sizehi/512 blocks, no EOF logic involved.
	BookFileBytes = 0x2000
	bookFileBlocks = BookFileBytes / 512
	// bookIndexBase / bookDataBase: the files' sapling index blocks, then
	// their data blocks, contiguous above the directory.
	bookIndexBase = BookDirBlock + 1
	bookDataBase  = bookIndexBase + BookFiles

	blocksPerDisk = Tracks * 8
	// bookRegionBlocks is the region's total footprint.
	bookRegionBlocks = 1 + BookFiles + BookFiles*bookFileBlocks
)

// BookFileName returns the name of book file i as it appears both in the
// directory block and in asm/m8.s's request ("BOOK0".."BOOK3").
func BookFileName(i int) string { return fmt.Sprintf("BOOK%d", i) }

// physToDos maps a PHYSICAL sector number (what the drive's address fields
// carry, and what ProRWTS2's block->track/sector translation produces) to the
// DOS 3.3 logical sector a .dsk file stores at that slot — the same soft skew
// `diskii mksd` and the boot ROM path use (see SectorOffset).
var physToDos = [16]int{0, 7, 14, 6, 13, 5, 12, 4, 11, 3, 10, 2, 9, 1, 8, 15}

// BlockOffsets returns the two .dsk byte offsets holding ProDOS block b, in
// the order the driver reads them. This mirrors, in Go, exactly the
// arithmetic in ProRWTS2's seekrdwr: track = b>>3, first physical sector =
// ((b&3)<<2) | ((b>>2)&1), second = first+2.
func BlockOffsets(b int) [2]int {
	track := b >> 3
	first := ((b & 3) << 2) | ((b >> 2) & 1)
	var out [2]int
	for i := range 2 {
		phys := first + 2*i
		out[i] = (track*SectorsPerTrk + physToDos[phys]) * SectorBytes
	}
	return out
}

// buildBookRegion lays the region out as block-number -> 512-byte block.
func buildBookRegion(bigbook []byte) (map[int][]byte, error) {
	if len(bigbook) > BookFiles*BookFileBytes {
		return nil, fmt.Errorf("delivery: big book is %d B, over the %d B region",
			len(bigbook), BookFiles*BookFileBytes)
	}
	if bookDataBase+BookFiles*bookFileBlocks > blocksPerDisk {
		return nil, fmt.Errorf("delivery: book region runs past block %d", blocksPerDisk-1)
	}
	blocks := map[int][]byte{}

	// The directory KEY block. ProRWTS2 reads: the header entry's FILE_COUNT
	// (block offset $25), then each 39-byte entry from offset $2B on —
	// storage type + name length, the name, and KEY_POINTER ($11). EOF is
	// stored honestly but our aligned-read config never reads it.
	const entrySize = 0x27
	dir := make([]byte, 512)
	// prev/next block pointers: none — one block of directory.
	hdr := dir[4:]
	name := "EIGHTFISH"
	hdr[0] = 0xF0 | byte(len(name)) // volume directory header
	copy(hdr[1:], name)
	hdr[0x21] = BookFiles // FILE_COUNT (lo; block offset $25)
	for i := range BookFiles {
		e := dir[4+(i+1)*entrySize:]
		fn := BookFileName(i)
		e[0] = 0x20 | byte(len(fn)) // sapling
		copy(e[1:], fn)
		key := bookIndexBase + i
		e[0x11] = byte(key) // KEY_POINTER
		e[0x12] = byte(key >> 8)
		e[0x13] = byte(bookFileBlocks + 1)        // blocks used (index + data)
		e[0x15] = byte(BookFileBytes & 0xFF)      // EOF, 3 bytes LE
		e[0x16] = byte(BookFileBytes >> 8 & 0xFF)
		e[0x17] = byte(BookFileBytes >> 16)
	}
	blocks[BookDirBlock] = dir

	// One sapling index block per file: data-block lo bytes in the first
	// half, hi bytes in the second (`ldx dirbuf,y / lda dirbuf+256,y`).
	pad := make([]byte, BookFiles*BookFileBytes)
	copy(pad, bigbook)
	for i := range BookFiles {
		idx := make([]byte, 512)
		for j := range bookFileBlocks {
			b := bookDataBase + i*bookFileBlocks + j
			idx[j] = byte(b)
			idx[256+j] = byte(b >> 8)
		}
		blocks[bookIndexBase+i] = idx
		for j := range bookFileBlocks {
			b := bookDataBase + i*bookFileBlocks + j
			off := i*BookFileBytes + j*512
			blocks[b] = pad[off : off+512]
		}
	}
	return blocks, nil
}

// BookRegionSectors is the region's disk cost in sectors.
func BookRegionSectors() int { return bookRegionBlocks * 2 }

// BookRegionOffsets returns every .dsk sector offset the region occupies —
// the overlap gate against the Standard Delivery stages reads this.
func BookRegionOffsets(bigbook []byte) ([]int, error) {
	blocks, err := buildBookRegion(bigbook)
	if err != nil {
		return nil, err
	}
	var offs []int
	for b := range blocks {
		o := BlockOffsets(b)
		offs = append(offs, o[0], o[1])
	}
	return offs, nil
}

// writeBookRegion writes the region into a .dsk image in place.
func writeBookRegion(dsk, bigbook []byte) error {
	blocks, err := buildBookRegion(bigbook)
	if err != nil {
		return err
	}
	for b, data := range blocks {
		offs := BlockOffsets(b)
		for i := range 2 {
			if offs[i]+SectorBytes > len(dsk) {
				return fmt.Errorf("delivery: block %d maps past the end of the disk", b)
			}
			copy(dsk[offs[i]:offs[i]+SectorBytes], data[i*SectorBytes:(i+1)*SectorBytes])
		}
	}
	return nil
}

// ExtractBookFile reads book file i back out of a .dsk exactly as the driver
// would: directory entry -> index block -> data blocks, through BlockOffsets.
// The disk end-to-end gate compares what a booted machine loaded against
// this, so a skew or placement mistake shows up as a byte diff, not a hang.
func ExtractBookFile(dsk []byte, i int) ([]byte, error) {
	if i < 0 || i >= BookFiles {
		return nil, fmt.Errorf("delivery: no book file %d", i)
	}
	readBlock := func(b int) ([]byte, error) {
		out := make([]byte, 0, 512)
		for _, off := range BlockOffsets(b) {
			if off+SectorBytes > len(dsk) {
				return nil, fmt.Errorf("delivery: block %d beyond the disk", b)
			}
			out = append(out, dsk[off:off+SectorBytes]...)
		}
		return out, nil
	}
	dir, err := readBlock(BookDirBlock)
	if err != nil {
		return nil, err
	}
	e := dir[4+(i+1)*0x27:]
	fn := BookFileName(i)
	if e[0] != 0x20|byte(len(fn)) || !bytes.Equal(e[1:1+len(fn)], []byte(fn)) {
		return nil, fmt.Errorf("delivery: directory entry %d is not %s", i, fn)
	}
	idx, err := readBlock(int(e[0x11]) | int(e[0x12])<<8)
	if err != nil {
		return nil, err
	}
	var out []byte
	for j := range bookFileBlocks {
		d, err := readBlock(int(idx[j]) | int(idx[256+j])<<8)
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	return out, nil
}
