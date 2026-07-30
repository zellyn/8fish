// Package delivery builds the bootable 8fish disk: it lays the four payload
// pieces out as ONE contiguous memory image and hands that image to
// peterferrie's "Standard Delivery" boot loader via `diskii mksd`.
//
// WHY ONE IMAGE. Standard Delivery is a boot sector plus a fast loader: it
// reads a single contiguous span of sectors into a single contiguous span of
// memory and jumps to it. There is no file system, no DOS, and no way to say
// "this piece here, that piece there" — so the gaps between the pieces are
// part of the image and are paid for in disk sectors.
//
//	$0D00  m8sdboot.bin                57 B  the run-once copier
//	$0E00  m8.bin                   4,441 B  the UI payload (staged; the
//	                                         copier moves it to $E000)
//	$2000  internal/book/bookblob.bin 7,407 B the resident opening book
//	$4000  engine.bin              31,906 B  the engine
//
// WHY NOT $0800. `diskii mksd` refuses an image over MaxImage (45,056)
// bytes. The span runs from the staging base to the end of engine.bin, so the
// base is the only free variable, and at the BLOAD layout's $0800 the image
// does not fit. Raising the base shrinks the image byte for byte but eats the
// staging area the UI payload has to fit in (it must end below the book at
// $2000). At the file sizes of 2026-07-30 (engine 31,906 B, UI 4,441 B):
//
//	base    image    spare   UI growth room
//	$0800  46,242   -1,186        1,447
//	$0C00  45,218     -162          423
//	$0D00  44,962       94          167     <- chosen
//	$0E00      -        -             -     payload overlaps the book
//
// The chosen base is the LOWEST that fits, i.e. the one that leaves the UI
// the most room to grow. Both margins are small and both are consumed by
// ordinary growth (the engine eats the spare, the UI eats the growth room),
// so TestDiskLedger asserts both rather than trusting this comment — and
// recomputing the table is one call to LoadAt/Image per candidate base, which
// beats arguing from a comment that has already gone stale once.
//
// NEITHER MARGIN IS A WALL. It is our disk. The 44 KB cap and the contiguous
// span are properties of the SIMPLEST Standard Delivery layout — one shot,
// one span — not of the medium. When the margins run out the answer is a
// different LOADER, not a smaller program:
//
//   - chain-load: the first thing loaded loads the next. Cheap in principle,
//     but by the time it is robust it is most of a sector reader, at which
//     point taking a good one beats writing one.
//   - ProRWTS2 (peterferrie): reads AND WRITES ProDOS files. It is the
//     intended successor, because Standard Delivery is READ-ONLY and a saved
//     game needs a writer — and hand-rolling a Disk II WRITER (6-and-2 nibble
//     encoding plus write-splice timing) is the last thing this project
//     should write itself: getting it wrong corrupts disks instead of failing.
//     It would also remove the contiguous-span squeeze, handing the UI back
//     its full 5,888-byte LC budget.
//
// So the ledger is a TRIPWIRE that says "the simple path no longer fits,
// choose a mechanism", not a budget that has to be defended.
//
// The staging address is not hardcoded in asm/m8.s; it is a linker symbol
// defined by asm/m8sd.cfg, so this layout and the BLOAD layout are built from
// the same source and the copier is the only thing that differs (by two
// bytes: the payload address).
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
	// `diskii mksd --address` is given. It must be page-aligned.
	//
	// RAISED $0C00 -> $0D00 on 2026-07-30, spending MARGIN 2 to buy back
	// MARGIN 1 exactly as the table above and TestDiskLedger's failure
	// message prescribe. The engine's pre-make evasion filter (asm/search.s
	// sdevade, -5.3% of all search cycles) added 130 bytes of CODE, and
	// because the TABLES segment is page-aligned those 130 bytes cost the
	// IMAGE a full 256-byte page: SD spare went 94 B -> -162 B and the disk
	// stopped building. No placement fitted -- even moving the whole filter
	// into the page-tail-free TABLES segment costs 130 image bytes against
	// 94 bytes of spare -- so this is the deliberate decision the ledger
	// exists to force, taken with the numbers in hand. After the move: SD
	// spare 94 B again, UI growth room 423 -> 167 B. TestDiskLedger logs the
	// whole table from the REAL file sizes, so read it there, not here.
	Base = 0x0D00
	// CopierOrg is where m8sdboot.bin lands (and where execution starts).
	CopierOrg = Base
	// PayloadOrg is where m8.bin is staged before the copier lifts it to
	// $E000. asm/m8sd.cfg defines the same value as the UIPAYLOAD symbol.
	PayloadOrg = Base + SectorBytes
	// BookOrg is the resident opening book's home, and therefore the hard
	// ceiling on the staged payload.
	BookOrg = 0x2000
	// EngineOrg is where engine.bin runs.
	EngineOrg = 0x4000

	// MaxImage is the largest image `diskii mksd` accepts (44 KB).
	MaxImage = 45056
)

// Disk geometry of a 5.25" .dsk: 35 tracks of 16 sectors of 256 bytes.
const (
	Tracks         = 35
	SectorsPerTrk  = 16
	SectorBytes    = 256
	sectorsPerDisk = Tracks * SectorsPerTrk
)

// A Piece is one file placed into the image at a fixed address.
type Piece struct {
	Path string // relative to the repo root
	Org  int    // load address
	Data []byte // filled in by Load
	// Staged marks the UI payload: the one piece that is not delivered to
	// the address it runs at, and therefore the one whose growth is capped
	// by the book at BookOrg rather than by the image size.
	Staged bool
}

// Pieces returns the delivery layout, in ascending address order.
func Pieces() []Piece { return PiecesAt(Base) }

// PiecesAt is Pieces with the staging base as a parameter, so the base can be
// re-chosen (and the trade-off it makes re-derived from the real file sizes)
// rather than argued about. The copier goes at base and the payload one page
// above it; the book and the engine do not move.
func PiecesAt(base int) []Piece {
	return []Piece{
		{Path: filepath.Join("asm", "m8sdboot.bin"), Org: base},
		{Path: filepath.Join("asm", "m8.bin"), Org: base + SectorBytes, Staged: true},
		{Path: filepath.Join("internal", "book", "bookblob.bin"), Org: BookOrg},
		{Path: filepath.Join("asm", "engine.bin"), Org: EngineOrg},
	}
}

// Ledger is the margin accounting for one built image. Both margins are
// asserted by internal/ui's TestDiskLedger; neither has much slack.
type Ledger struct {
	Base       int // first address in the image
	End        int // one past the last address in the image
	ImageBytes int // End - Base
	// SDSpare is how many bytes the image could still grow before
	// `diskii mksd` rejects it. Engine growth spends this.
	SDSpare int
	// PayloadOrg and PayloadEnd bracket the staged UI payload.
	PayloadOrg int
	PayloadEnd int
	// UIRoom is how many bytes the UI payload could still grow before it
	// would be staged on top of the resident opening book at $2000. UI
	// growth spends this.
	UIRoom int
	// Sectors is how many 256-byte sectors the image occupies on disk
	// (excluding the boot sector).
	Sectors int
}

func (l Ledger) String() string {
	return fmt.Sprintf(
		"image $%04X-$%04X = %d B of %d (SD spare %d B, %d sectors); "+
			"staged payload $%04X-$%04X, book at $%04X (UI growth room %d B)",
		l.Base, l.End-1, l.ImageBytes, MaxImage, l.SDSpare, l.Sectors,
		l.PayloadOrg, l.PayloadEnd-1, BookOrg, l.UIRoom)
}

// Load reads the four pieces of the shipping layout from the repo rooted at
// root. LoadAt does the same for an arbitrary staging base.
func Load(root string) ([]Piece, error) { return LoadAt(root, Base) }

// LoadAt reads the four pieces of the layout based at base.
func LoadAt(root string, base int) ([]Piece, error) {
	ps := PiecesAt(base)
	for i := range ps {
		b, err := os.ReadFile(filepath.Join(root, ps[i].Path))
		if err != nil {
			return nil, fmt.Errorf("delivery: %w (run `make dsk`)", err)
		}
		ps[i].Data = b
	}
	return ps, nil
}

// Image lays the pieces out as one contiguous image based at the first
// piece's address, zero filling the gaps, and returns it with its margin
// ledger. It fails if a piece overlaps its successor or falls outside the
// image; it does NOT fail on an over-cap image or an overrun payload, so that
// a caller (the ledger test) can report both numbers rather than the first one
// that broke.
func Image(pieces []Piece) ([]byte, Ledger, error) {
	if len(pieces) == 0 {
		return nil, Ledger{}, errors.New("delivery: no pieces")
	}
	base := pieces[0].Org
	end := 0
	for i, p := range pieces {
		if p.Org < base {
			return nil, Ledger{}, fmt.Errorf("delivery: %s at $%04X is below the image base $%04X",
				p.Path, p.Org, base)
		}
		if p.Org+len(p.Data) > 0x10000 {
			return nil, Ledger{}, fmt.Errorf("delivery: %s (%d B at $%04X) runs past $FFFF",
				p.Path, len(p.Data), p.Org)
		}
		if i > 0 && p.Org < end {
			return nil, Ledger{}, fmt.Errorf("delivery: %s at $%04X overlaps the piece before it, which ends at $%04X",
				p.Path, p.Org, end)
		}
		if e := p.Org + len(p.Data); e > end {
			end = e
		}
	}
	img := make([]byte, end-base)
	for _, p := range pieces {
		copy(img[p.Org-base:], p.Data)
	}

	l := Ledger{
		Base:       base,
		End:        end,
		ImageBytes: len(img),
		SDSpare:    MaxImage - len(img),
		Sectors:    (len(img) + SectorBytes - 1) / SectorBytes,
	}
	for _, p := range pieces {
		if p.Staged {
			l.PayloadOrg = p.Org
			l.PayloadEnd = p.Org + len(p.Data)
			l.UIRoom = BookOrg - l.PayloadEnd
		}
	}
	return img, l, nil
}

// SectorOffset returns the byte offset in a DOS-order .dsk of the sector
// holding image page n (n = 0 is the page at Base).
//
// Standard Delivery reserves track 0 sector 0 for the boot sector and then
// fills the disk in a fixed interleave, one track at a time: within a track
// the order is sector 0, then 14, 13, ... 1, then 15. Numbering the payload
// sectors k = n+1 (so k skips the boot sector) makes that a closed form.
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

// Extract reads the delivered image back out of a .dsk: the inverse of what
// `diskii mksd` wrote, sector by sector, for size bytes starting at Base.
func Extract(dsk []byte, size int) ([]byte, error) {
	if len(dsk) != sectorsPerDisk*SectorBytes {
		return nil, fmt.Errorf("delivery: bad .dsk size %d, want %d", len(dsk), sectorsPerDisk*SectorBytes)
	}
	pages := (size + SectorBytes - 1) / SectorBytes
	out := make([]byte, 0, pages*SectorBytes)
	for n := range pages {
		off := SectorOffset(n)
		if off+SectorBytes > len(dsk) {
			return nil, fmt.Errorf("delivery: image page %d maps past the end of the disk", n)
		}
		out = append(out, dsk[off:off+SectorBytes]...)
	}
	return out[:size], nil
}

// ErrNoDiskii reports that the `diskii` tool is not on PATH.
var ErrNoDiskii = errors.New(
	"delivery: `diskii` not found on PATH. It builds the Standard Delivery boot disk; install it with\n" +
		"    go install github.com/zellyn/diskii@latest\n" +
		"(and make sure $(go env GOPATH)/bin is on PATH)")

// Build assembles the image, writes it to imgPath, and runs `diskii mksd` to
// produce dskPath. It returns the ledger it built.
func Build(root, imgPath, dskPath string) (Ledger, error) {
	pieces, err := Load(root)
	if err != nil {
		return Ledger{}, err
	}
	img, l, err := Image(pieces)
	if err != nil {
		return Ledger{}, err
	}
	if l.SDSpare < 0 {
		return l, fmt.Errorf("delivery: image is %d B, %d B over `diskii mksd`'s %d B limit -- "+
			"8fish has outgrown the simple single-shot Standard Delivery layout. Raising the "+
			"base above $%04X buys 256 B per page from the %d B of UI staging room; past that, "+
			"chain-load or move to ProRWTS2 (see this package's doc)",
			l.ImageBytes, -l.SDSpare, MaxImage, Base, l.UIRoom)
	}
	if l.UIRoom < 0 {
		return l, fmt.Errorf("delivery: the staged UI payload ends at $%04X, %d B INTO the resident "+
			"opening book at $%04X: in this layout the copier would load the book's first %d bytes "+
			"over itself. A loader that does not stage below the book fixes it (see this package's doc)",
			l.PayloadEnd, -l.UIRoom, BookOrg, -l.UIRoom)
	}
	if _, err := exec.LookPath("diskii"); err != nil {
		return l, ErrNoDiskii
	}
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
	// Round-trip immediately: a disk that does not read back as the image
	// we handed it is not a disk we should ship.
	dsk, err := os.ReadFile(dskPath)
	if err != nil {
		return l, err
	}
	back, err := Extract(dsk, len(img))
	if err != nil {
		return l, err
	}
	if !bytes.Equal(back, img) {
		return l, fmt.Errorf("delivery: %s does not read back as the image written to %s", dskPath, imgPath)
	}
	return l, nil
}
