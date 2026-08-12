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
	"github.com/zellyn/chess6502/internal/saveload"
	"github.com/zellyn/chess6502/internal/splash"
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

	// SplashFileBytes is the boot splash's on-disk size: the per-bank PackBits
	// blob padded to a whole number of blocks (internal/splash.DiskBytes). It
	// is a 5th file in the same directory, but its index and data blocks sit
	// just BELOW the directory key block — the tracks between the Standard
	// Delivery stages and block 208 are free, and the book already fills the
	// tracks above. asm/m8.s (m8splash) reads it at boot into main $2000.
	SplashFileBytes  = splash.DiskBytes
	splashDataBlocks = SplashFileBytes / 512
	// splashIndexBlock is the splash's sapling index block; its data blocks
	// follow it, and the whole run ends one block below the directory.
	splashIndexBlock = BookDirBlock - 1 - splashDataBlocks
	splashDataBase   = splashIndexBlock + 1
	// splashRegionBlocks is the splash's footprint (index + data).
	splashRegionBlocks = 1 + splashDataBlocks

	// The SAVE/LOAD trio (docs/saveload-feasibility.md §5/§6), chained
	// downward below the splash the same way the splash chains below the
	// directory — each file is [index block, data blocks...]:
	//
	//	SAVE      the 2-block saved-game slot, a valid-checksum EMPTY record
	//	          at build time, overwritten IN PLACE by the on-device save
	//	          (the one file on the disk whose content is not fixed)
	//	SAVELOAD  the transient save/load orchestrator (asm/saveload.s),
	//	          loaded to main $1A00 on demand; a FIXED 3-block budget so
	//	          the layout never moves when the code grows a byte
	//	RWTSW     the transient write-capable ProRWTS2 build (rwts.WBlob),
	//	          loaded to main $0E00 by a save, run once, discarded
	saveDataBlocks = saveload.RecordBytes / 512
	saveIndexBlock = splashIndexBlock - 1 - saveDataBlocks
	saveDataBase   = saveIndexBlock + 1

	// SaveLoadFileBytes is the orchestrator's fixed on-disk budget. Its
	// run-time window is $1A00-$1FFF (1,536 B, the top of dead MOVESTACK
	// scratch), so the file budget IS the memory budget.
	SaveLoadFileBytes  = 0x600
	saveloadDataBlocks = SaveLoadFileBytes / 512
	saveloadIndexBlock = saveIndexBlock - 1 - saveloadDataBlocks
	saveloadDataBase   = saveloadIndexBlock + 1

	rwtswDataBlocks = (rwtswBlobCap + 511) / 512
	rwtswIndexBlock = saveloadIndexBlock - 1 - rwtswDataBlocks
	rwtswDataBase   = rwtswIndexBlock + 1
	// rwtswBlobCap: the write driver is 1,024 B today (2 blocks); a growth
	// past 2 blocks moves every block number above, so it is pinned here and
	// checked against the real blob in buildBookRegion.
	rwtswBlobCap = 1024

	// entrySize is one ProDOS directory entry.
	entrySize = 0x27

	blocksPerDisk = Tracks * 8
	// bookRegionBlocks is the whole region's footprint: the directory, the
	// book's index+data blocks above it, and everything chained below.
	bookRegionBlocks = 1 + BookFiles + BookFiles*bookFileBlocks + splashRegionBlocks +
		(1 + saveDataBlocks) + (1 + saveloadDataBlocks) + (1 + rwtswDataBlocks)
)

// SplashFileName is the name of the boot-splash file in the directory block
// and in asm/m8.s's request (f_splash).
const SplashFileName = "SPLASH"

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

// layFile writes ONE file into the region: its directory entry in slot `slot`
// (0-based, after the volume header), its sapling index block at `indexBlock`,
// and its data blocks starting at `dataBase`. data must be exactly
// dataBlocks*512 bytes. The index block carries the data-block lo bytes in its
// first half and hi bytes in its second (`ldx dirbuf,y / lda dirbuf+256,y`).
func layFile(blocks map[int][]byte, dir []byte, slot int, name string, indexBlock, dataBase, dataBlocks int, data []byte) {
	e := dir[4+(slot+1)*entrySize:]
	e[0] = 0x20 | byte(len(name)) // sapling
	copy(e[1:], name)
	e[0x11] = byte(indexBlock) // KEY_POINTER
	e[0x12] = byte(indexBlock >> 8)
	e[0x13] = byte(dataBlocks + 1) // blocks used (index + data)
	fileBytes := len(data)
	e[0x15] = byte(fileBytes) // EOF, 3 bytes LE
	e[0x16] = byte(fileBytes >> 8)
	e[0x17] = byte(fileBytes >> 16)

	idx := make([]byte, 512)
	for j := 0; j < dataBlocks; j++ {
		b := dataBase + j
		idx[j] = byte(b)
		idx[256+j] = byte(b >> 8)
		blocks[b] = data[j*512 : (j+1)*512]
	}
	blocks[indexBlock] = idx
}

// Directory slot numbers (0-based, after the volume header). The save/load
// gates read files back by slot, so the assignment is named once.
const (
	slotSplash   = BookFiles // 4
	slotRwtsw    = BookFiles + 1
	slotSaveLoad = BookFiles + 2
	slotSave     = BookFiles + 3
	dirFileCount = BookFiles + 4
)

// padToBlocks zero-pads data to exactly n*512 bytes (it must already fit).
func padToBlocks(data []byte, n int) []byte {
	out := make([]byte, n*512)
	copy(out, data)
	return out
}

// buildBookRegion lays the region out as block-number -> 512-byte block: the
// directory key block, the four BOOK files' index+data above it, and — in
// order downward below it — the SPLASH, SAVE, SAVELOAD and RWTSW files.
// saveloadBin is asm/m8saveload.bin (the caller reads it; tests may pass a
// stand-in). The SAVE file ships as the valid-checksum EMPTY record.
func buildBookRegion(bigbook, saveloadBin []byte) (map[int][]byte, error) {
	if len(bigbook) > BookFiles*BookFileBytes {
		return nil, fmt.Errorf("delivery: big book is %d B, over the %d B region",
			len(bigbook), BookFiles*BookFileBytes)
	}
	if bookDataBase+BookFiles*bookFileBlocks > blocksPerDisk {
		return nil, fmt.Errorf("delivery: book region runs past block %d", blocksPerDisk-1)
	}
	if rwtswIndexBlock <= 0 {
		return nil, fmt.Errorf("delivery: region runs below block 0 (RWTSW index block %d): "+
			"the save/load files no longer fit under the splash", rwtswIndexBlock)
	}
	if s := splash.Disk(); len(s) != SplashFileBytes {
		return nil, fmt.Errorf("delivery: splash blob is %d B, want %d", len(s), SplashFileBytes)
	}
	if len(rwts.WBlob) > rwtswBlobCap {
		return nil, fmt.Errorf("delivery: write-driver blob is %d B, over the %d B cap "+
			"(rwtswBlobCap moves every block below the splash — re-plan the chain)",
			len(rwts.WBlob), rwtswBlobCap)
	}
	if len(saveloadBin) == 0 || len(saveloadBin) > SaveLoadFileBytes {
		return nil, fmt.Errorf("delivery: save/load orchestrator is %d B, want 1..%d "+
			"(its $1A00-$1FFF run window is the budget)", len(saveloadBin), SaveLoadFileBytes)
	}
	blocks := map[int][]byte{}

	// The directory KEY block. ProRWTS2 reads: the header entry's FILE_COUNT
	// (block offset $25), then each 39-byte entry from offset $2B on —
	// storage type + name length, the name, and KEY_POINTER ($11). EOF is
	// stored honestly but our aligned-read config never reads it.
	dir := make([]byte, 512)
	// prev/next block pointers: none — one block of directory.
	hdr := dir[4:]
	vol := "EIGHTFISH"
	hdr[0] = 0xF0 | byte(len(vol)) // volume directory header
	copy(hdr[1:], vol)
	hdr[0x21] = dirFileCount // FILE_COUNT (lo; block offset $25)

	// The four book files, laid contiguously ABOVE the directory.
	pad := make([]byte, BookFiles*BookFileBytes)
	copy(pad, bigbook)
	for i := range BookFiles {
		layFile(blocks, dir, i, BookFileName(i),
			bookIndexBase+i, bookDataBase+i*bookFileBlocks, bookFileBlocks,
			pad[i*BookFileBytes:(i+1)*BookFileBytes])
	}

	// The splash file, laid just BELOW the directory.
	layFile(blocks, dir, slotSplash, SplashFileName,
		splashIndexBlock, splashDataBase, splashDataBlocks, splash.Disk())

	// The save/load trio, chained on downward.
	layFile(blocks, dir, slotSave, saveload.DiskFileName,
		saveIndexBlock, saveDataBase, saveDataBlocks,
		padToBlocks(saveload.Empty(), saveDataBlocks))
	layFile(blocks, dir, slotSaveLoad, saveload.CodeFileName,
		saveloadIndexBlock, saveloadDataBase, saveloadDataBlocks,
		padToBlocks(saveloadBin, saveloadDataBlocks))
	layFile(blocks, dir, slotRwtsw, saveload.WriterFileName,
		rwtswIndexBlock, rwtswDataBase, rwtswDataBlocks,
		padToBlocks(rwts.WBlob, rwtswDataBlocks))

	blocks[BookDirBlock] = dir
	return blocks, nil
}

// BookRegionSectors is the region's disk cost in sectors.
func BookRegionSectors() int { return bookRegionBlocks * 2 }

// SaveFileOffsets returns the .dsk byte offsets of the SAVE file's DATA
// sectors — the ONLY sectors an on-device save may change. The disk
// integrity gate byte-compares the whole image around a save and requires
// every difference to be inside these.
func SaveFileOffsets() []int {
	var offs []int
	for b := saveDataBase; b < saveDataBase+saveDataBlocks; b++ {
		o := BlockOffsets(b)
		offs = append(offs, o[0], o[1])
	}
	return offs
}

// BookRegionOffsets returns every .dsk sector offset the region occupies —
// the overlap gate against the Standard Delivery stages reads this.
func BookRegionOffsets(bigbook, saveloadBin []byte) ([]int, error) {
	blocks, err := buildBookRegion(bigbook, saveloadBin)
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
func writeBookRegion(dsk, bigbook, saveloadBin []byte) error {
	blocks, err := buildBookRegion(bigbook, saveloadBin)
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
	return extractSlot(dsk, i, BookFileName(i), bookFileBlocks)
}

// extractSlot reads one file back out of a .dsk exactly as the driver would:
// directory entry -> index block -> data blocks, through BlockOffsets.
func extractSlot(dsk []byte, slot int, name string, dataBlocks int) ([]byte, error) {
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
	e := dir[4+(slot+1)*entrySize:]
	if e[0] != 0x20|byte(len(name)) || !bytes.Equal(e[1:1+len(name)], []byte(name)) {
		return nil, fmt.Errorf("delivery: directory slot %d is not %s", slot, name)
	}
	idx, err := readBlock(int(e[0x11]) | int(e[0x12])<<8)
	if err != nil {
		return nil, err
	}
	var out []byte
	for j := 0; j < dataBlocks; j++ {
		d, err := readBlock(int(idx[j]) | int(idx[256+j])<<8)
		if err != nil {
			return nil, err
		}
		out = append(out, d...)
	}
	return out, nil
}

// ExtractSplashFile reads the SPLASH file back out of a .dsk exactly as the
// driver would. It returns the SplashFileBytes-byte padded blob, so the disk
// gate can compare what the booted machine will read against
// internal/splash.Disk().
func ExtractSplashFile(dsk []byte) ([]byte, error) {
	return extractSlot(dsk, slotSplash, SplashFileName, splashDataBlocks)
}

// ExtractSaveFile reads the SAVE file's 1,024-byte record back out of a .dsk
// — the round-trip gate's view of what the on-device save actually wrote.
func ExtractSaveFile(dsk []byte) ([]byte, error) {
	return extractSlot(dsk, slotSave, saveload.DiskFileName, saveDataBlocks)
}

// ExtractSaveLoadFile reads the orchestrator file back (padded to its fixed
// budget); ExtractRwtswFile the transient write driver's blob (block-padded).
func ExtractSaveLoadFile(dsk []byte) ([]byte, error) {
	return extractSlot(dsk, slotSaveLoad, saveload.CodeFileName, saveloadDataBlocks)
}

func ExtractRwtswFile(dsk []byte) ([]byte, error) {
	return extractSlot(dsk, slotRwtsw, saveload.WriterFileName, rwtswDataBlocks)
}
