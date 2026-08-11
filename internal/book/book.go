// Package book is the engine's resident opening book: a hand-curated set of
// sound main-line openings, compiled to TWO binary pieces laid out EXACTLY as
// they sit resident on the machine, so the asm probe is a pure read-side
// addition.
//
// TWO PIECES, AND WHY (2026-08-01). The book used to be one contiguous blob:
// header + entries + name table. It is now split, because the two halves have
// completely different readers and completely different homes:
//
//	ENTRIES  header + sorted entry array -> AUXILIARY RAM $0800-$1FFF.
//	         Read by asm/book.s's bookprobe, thousands of times per game,
//	         through a Language Card primitive.
//	NAMES    the length-prefixed name table -> LANGUAGE CARD BANK 2 $D000.
//	         Read by asm/m8.s's uibookname, once per book move, to render
//	         "BOOK: <name>". bookprobe NEVER touches it.
//
// The split is what buys MIXED MODE. Double hi-res forces 80-column text,
// whose even columns come from AUX $0400-$07FF -- straight through the middle
// of the old blob's aux home at $0200. Moving the 1,702-byte name table to the
// Language Card leaves header+entries, which fit ABOVE the text page entirely.
// See docs/ui-design.md §13.4 and §14.
//
// KEYING (the correctness hinge): entries are keyed on the engine's
// 32-bit Zobrist hash — the asm HASH0-3. That hash is reproduced here by
// mirror.Pos.Hash, which is byte-identical to the value baked into
// engine.bin (proven by TestBookKeyMatchesASM, which reads the actual
// ZKEYS/STMKEY/CASTKEYS/EPKEYS bytes out of engine.bin and recomputes the
// hash from scratch). The identical entries blob therefore probes correctly
// from both this Go-side bridge and the asm-resident binary search.
package book

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/zellyn/chess6502/internal/mirror"
)

// On-disk / in-RAM layout constants. The entries blob is laid out to sit at
// BaseAddr; every field offset below is relative to its start, which equals
// its resident address minus BaseAddr.
const (
	// BaseAddr is the ENTRIES blob's resident base: AUXILIARY RAM $0800.
	//
	// It has moved twice, and each move was forced by the screen. It was main
	// $2000 (the hi-res page-1 hole) until that became the MAIN half of double
	// hi-res page 1; it was aux $0200 until mixed mode needed aux $0400-$07FF
	// for the 80-column text window's even columns. $0800 is the first page
	// ABOVE that window, and the entries end well below the DHGR aux half at
	// $2000. asm/m8.s's copier does the move at boot; asm/book.s reads the blob
	// through a Language Card primitive because RAMRD would switch instruction
	// fetches too.
	BaseAddr = 0x0800
	// MaxSize is aux $0800-$1FFF: everything between the 80-column text page
	// and the DHGR aux half.
	MaxSize = 0x2000 - BaseAddr

	// AuxTextLo/AuxTextHi bracket the 80-column text page's AUXILIARY half,
	// $0400-$07FF -- the even columns of all 24 rows, including the four the
	// mixed-mode window shows. The resident book MUST NOT overlap it; that is
	// the entire reason BaseAddr is $0800, and internal/delivery's
	// TestBookClearsTheAuxTextPage is where it FAILS rather than a comment.
	AuxTextLo = 0x0400
	AuxTextHi = 0x0800

	// NamesAddr is the NAME TABLE's resident base: LANGUAGE CARD BANK 2
	// $D000. Bank 2 and not bank 1 because bank 1 is full -- LCCODE at $D000,
	// the artwork at $D300, the scanline tables above it, leaving 1,048 B
	// contiguous -- while bank 2 is 4,096 B and was entirely unused.
	//
	// The name table is the ONE thing in the book that can live behind a bank
	// switch, because the probe never reads it: only asm/m8.s's uibookname
	// does, and uibookname runs at $E000, which bank switching does not touch.
	NamesAddr = 0xD000
	// NamesMaxSize is all of Language Card bank 2, $D000-$DFFF.
	NamesMaxSize = 0x1000

	// BigBase is the BIG BOOK's resident base: AUXILIARY RAM $4000, the
	// bottom of the transposition-table window. The TT is written only by
	// search, and from boot until the first out-of-book move no search runs —
	// so the whole window is dead freight the big book borrows
	// (docs/prorwts2-design.md §3). The first real search overwrites it and
	// the BIGBOOKOK latch closes; the probe then falls back to the resident
	// blob at BaseAddr, which is never overwritten.
	BigBase = 0x4000
	// BigWindow is the borrowed window's size: aux $4000-$BFFF, all 32,768
	// bytes of the TT.
	BigWindow = 0x8000
	// BigMaxEntries is the big blob's capacity: header + entries + the
	// 16-bit checksum trailer must fit the window. (32768-8-2)/9 = 3,639.
	// The design's 3,640 was priced without the trailer; the load-verify
	// checksum — which is what makes the BIGBOOKOK latch trustworthy — costs
	// exactly one entry of the budget.
	BigMaxEntries = (BigWindow - HeaderSize - 2) / EntryStride

	Magic0      = 'B'
	Magic1      = 'K'
	HeaderSize  = 8
	EntryStride = 9 // key:4 from:1 to:1 flags:1 weight:1 nameID:1

	// Header field offsets (from the entries blob's base).
	offMagic = 0 // 2 bytes 'B','K'
	offCount = 2 // uint16 LE: entry count
	// offNameAddr holds the name table's ABSOLUTE resident address, not an
	// offset into this blob: the table is not in this blob any more. It is
	// stored rather than hard-coded in asm so that moving the table is a
	// genbook change and nothing else -- uibookname reads the address from
	// here.
	offNameAddr = 4 // uint16 LE: resident address of the name table
	offNameCt   = 6 // byte: name count
	offStride   = 7 // byte: entry stride (== EntryStride)

	// Entry field offsets (from an entry's base).
	eKey    = 0 // uint32 LE == HASH0..3
	eFrom   = 4
	eTo     = 5
	eFlags  = 6
	eWeight = 7
	eNameID = 8
)

// Entry is one book move: play (From,To,Flags) from the position whose
// asm hash is Key. From/To are 0x88 squares and Flags are the engine's
// move flags (bits 0-2 = promotion, matching chesstest.MoveUCI /
// BESTFROM/BESTTO/BESTFLAGS), so the bytes are usable verbatim by the asm
// engine. Weight biases the weighted-random pick among the moves sharing
// a Key; NameID indexes the name table.
type Entry struct {
	Key                             uint32
	From, To, Flags, Weight, NameID byte
}

// Book is a parsed book: entries sorted ascending by Key (for binary
// search) and the name table (NameID -> "ECO Name" text).
type Book struct {
	entries   []Entry
	names     []string
	blob      []byte // the ENTRIES piece: header + entry array
	namesBlob []byte // the NAMES piece: length-prefixed strings
}

// Encode builds the two resident pieces from entries and names. Entries are
// sorted by Key (stable, so equal-Key move order is preserved). The returned
// slices are exactly what loads at BaseAddr and at NamesAddr.
func Encode(entries []Entry, names []string) (entriesBlob, namesBlob []byte) {
	es := make([]Entry, len(entries))
	copy(es, entries)
	sort.SliceStable(es, func(i, j int) bool { return es[i].Key < es[j].Key })

	blob := make([]byte, HeaderSize+len(es)*EntryStride)
	blob[offMagic] = Magic0
	blob[offMagic+1] = Magic1
	binary.LittleEndian.PutUint16(blob[offCount:], uint16(len(es)))
	binary.LittleEndian.PutUint16(blob[offNameAddr:], uint16(NamesAddr))
	blob[offNameCt] = byte(len(names))
	blob[offStride] = EntryStride

	for i, e := range es {
		b := blob[HeaderSize+i*EntryStride:]
		binary.LittleEndian.PutUint32(b[eKey:], e.Key)
		b[eFrom] = e.From
		b[eTo] = e.To
		b[eFlags] = e.Flags
		b[eWeight] = e.Weight
		b[eNameID] = e.NameID
	}

	return blob, EncodeNames(names)
}

// EncodeNames builds the NAMES piece alone: the length-prefixed string run
// that goes resident in Language Card bank 2 at NamesAddr. Split out of
// Encode because the SHIPPED table is the BIG book's (curated names first,
// byte-identical, then the eco names BuildBig appends) while the small blob's
// header keeps its own curated-only name count — asm/m8.s's uibookname walks
// the table positionally by NameID, so a superset table serves both books.
func EncodeNames(names []string) []byte {
	total := 0
	for _, n := range names {
		total += 1 + len(n)
	}
	nb := make([]byte, total)
	p := 0
	for _, n := range names {
		nb[p] = byte(len(n))
		copy(nb[p+1:], n)
		p += 1 + len(n)
	}
	return nb
}

// EncodeBig builds the BIG BOOK blob: the identical header + sorted entry
// layout Encode produces (so asm/book.s walks it unchanged from base page
// $40), zero-padded to the full BigWindow with a 16-bit checksum trailer in
// the window's LAST TWO bytes. asm/m8.s's m8bigbook recomputes that sum over
// the loaded aux window and refuses to open the BIGBOOKOK latch on a
// mismatch — which is what turns "the driver returned" into "the load
// actually happened". The trailer sits at a FIXED address (aux $BFFE) rather
// than after the entries so the 6502 verify loop needs no count arithmetic:
// its bounds are immediates, and the sum covers the count bytes themselves.
func EncodeBig(entries []Entry, names []string) ([]byte, error) {
	if len(entries) > BigMaxEntries {
		return nil, fmt.Errorf("book: %d entries, %d more than the big book's %d-entry capacity",
			len(entries), len(entries)-BigMaxEntries, BigMaxEntries)
	}
	blob, _ := Encode(entries, names)
	out := make([]byte, BigWindow)
	copy(out, blob)
	s := Checksum16(out[:BigWindow-2])
	out[BigWindow-2] = byte(s)
	out[BigWindow-1] = byte(s >> 8)
	return out, nil
}

// Checksum16 is the big blob's load-verify checksum: a plain 16-bit sum of
// bytes, cheap enough for a 6502 to recompute over 32 KB in well under a
// second. Keep byte-identical to asm/m8.s's m8bigbook verify loop.
func Checksum16(b []byte) uint16 {
	var s uint16
	for _, x := range b {
		s += uint16(x)
	}
	return s
}

// Load parses the two resident pieces (as produced by Encode).
//
// namesBlob may be nil: the name text is HOST-SIDE ONLY (logging and the
// on-device "BOOK: <name>" row, which reads its own copy out of Language Card
// bank 2). Nothing about move SELECTION depends on it, so a caller that has
// only an entries blob -- `cmd/sprt --bookA <file>`, for instance -- gets a
// fully working book whose Name() returns "".
func Load(blob, namesBlob []byte) (*Book, error) {
	if len(blob) < HeaderSize {
		return nil, fmt.Errorf("book: entries blob too short (%d bytes)", len(blob))
	}
	if blob[offMagic] != Magic0 || blob[offMagic+1] != Magic1 {
		return nil, fmt.Errorf("book: bad magic %q%q", blob[offMagic], blob[offMagic+1])
	}
	if s := blob[offStride]; s != EntryStride {
		return nil, fmt.Errorf("book: stride %d != %d", s, EntryStride)
	}
	count := int(binary.LittleEndian.Uint16(blob[offCount:]))
	nameCt := int(blob[offNameCt])
	if HeaderSize+count*EntryStride > len(blob) {
		return nil, fmt.Errorf("book: truncated entries blob")
	}

	entries := make([]Entry, count)
	for i := range entries {
		b := blob[HeaderSize+i*EntryStride:]
		entries[i] = Entry{
			Key:    binary.LittleEndian.Uint32(b[eKey:]),
			From:   b[eFrom],
			To:     b[eTo],
			Flags:  b[eFlags],
			Weight: b[eWeight],
			NameID: b[eNameID],
		}
	}

	var names []string
	if namesBlob != nil {
		names = make([]string, 0, nameCt)
		p := 0
		for range nameCt {
			if p >= len(namesBlob) {
				return nil, fmt.Errorf("book: name table truncated")
			}
			n := int(namesBlob[p])
			p++
			if p+n > len(namesBlob) {
				return nil, fmt.Errorf("book: name string truncated")
			}
			names = append(names, string(namesBlob[p:p+n]))
			p += n
		}
	}
	return &Book{entries: entries, names: names, blob: blob, namesBlob: namesBlob}, nil
}

// Size is the ENTRIES piece's byte length: the book's AUX footprint.
func (b *Book) Size() int { return len(b.blob) }

// NamesSize is the NAMES piece's byte length: the book's Language Card
// bank 2 footprint.
func (b *Book) NamesSize() int { return len(b.namesBlob) }

// EntriesBlob returns the raw bytes that go resident at aux BaseAddr -- the
// piece the asm probe reads, and the one chesstest.LoadBook installs.
func (b *Book) EntriesBlob() []byte { return b.blob }

// NamesBlob returns the raw bytes that go resident in Language Card bank 2 at
// NamesAddr, or nil if the book was loaded without them.
func (b *Book) NamesBlob() []byte { return b.namesBlob }

// Entries returns the sorted entry slice (read-only; for tests/tools).
func (b *Book) Entries() []Entry { return b.entries }

// Name returns the text for a name ID ("" if out of range).
func (b *Book) Name(id byte) string {
	if int(id) < len(b.names) {
		return b.names[id]
	}
	return ""
}

// Probe binary-searches for key and, on a hit, returns a book move chosen
// by weighted-random among all moves sharing that key. The choice is a
// pure function of (key, r): the caller supplies r from a deterministic
// PRNG so A/B replays reproduce. This is the exact algorithm a future asm
// probe implements over the resident array.
func (b *Book) Probe(key uint32, r uint32) (e Entry, name string, ok bool) {
	lo := sort.Search(len(b.entries), func(i int) bool { return b.entries[i].Key >= key })
	if lo >= len(b.entries) || b.entries[lo].Key != key {
		return Entry{}, "", false
	}
	hi := lo
	var total uint32
	for hi < len(b.entries) && b.entries[hi].Key == key {
		total += uint32(b.entries[hi].Weight)
		hi++
	}
	if total == 0 {
		return Entry{}, "", false
	}
	pick := r % total
	for i := lo; i < hi; i++ {
		w := uint32(b.entries[i].Weight)
		if pick < w {
			return b.entries[i], b.Name(b.entries[i].NameID), true
		}
		pick -= w
	}
	last := b.entries[hi-1]
	return last, b.Name(last.NameID), true
}

// HashFEN returns the engine's 32-bit Zobrist hash (== asm HASH0-3, LE)
// for a position given as FEN. This is the keying used to build and probe
// the book; see the package doc and TestBookKeyMatchesASM for the
// mirror==asm equivalence proof.
func HashFEN(fen string) (uint32, error) {
	pos, err := mirror.ParseFEN(fen)
	if err != nil {
		return 0, err
	}
	e := mirror.NewEngine()
	e.SetPosition(pos)
	return e.Pos.Hash, nil
}
