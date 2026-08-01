// Package book is the engine's resident opening book: a hand-curated set
// of sound main-line openings, compiled to a compact binary blob that is
// laid out EXACTLY as it will sit resident at $2000-$3FFF (the verified-
// free hi-res page-1 hole) so a future asm probe is a pure read-side
// addition.
//
// KEYING (the correctness hinge): entries are keyed on the engine's
// 32-bit Zobrist hash — the asm HASH0-3. That hash is reproduced here by
// mirror.Pos.Hash, which is byte-identical to the value baked into
// engine.bin (proven by TestBookKeyMatchesASM, which reads the actual
// ZKEYS/STMKEY/CASTKEYS/EPKEYS bytes out of engine.bin and recomputes the
// hash from scratch). The identical blob therefore probes correctly from
// both this Go-side bridge and a future asm-resident binary search.
package book

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/zellyn/chess6502/internal/mirror"
)

// On-disk / in-RAM layout constants. The blob is laid out to sit at
// BaseAddr; every field offset below is relative to the blob start, which
// equals its resident address minus BaseAddr.
const (
	// BaseAddr is the blob's resident base: AUXILIARY RAM $0200. It used to
	// be main $2000 -- the hi-res page 1 hole -- but that is the MAIN half of
	// double hi-res page 1, which the board renderer needs, so the book moved
	// to the aux hole below the DHGR aux half. asm/m8.s's copier does the
	// move at boot; asm/book.s reads the blob through a Language Card
	// primitive because RAMRD would switch instruction fetches too.
	BaseAddr = 0x0200
	// MaxSize is aux $0200-$1FFF: everything below the DHGR aux half.
	MaxSize     = 0x2000 - BaseAddr
	Magic0      = 'B'
	Magic1      = 'K'
	HeaderSize  = 8
	EntryStride = 9 // key:4 from:1 to:1 flags:1 weight:1 nameID:1

	// Header field offsets (from blob base).
	offMagic   = 0 // 2 bytes 'B','K'
	offCount   = 2 // uint16 LE: entry count
	offNameOff = 4 // uint16 LE: offset from base to name table
	offNameCt  = 6 // byte: name count
	offStride  = 7 // byte: entry stride (== EntryStride)

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

// Book is a parsed blob: entries sorted ascending by Key (for binary
// search) and the name table (NameID -> "ECO Name" text, host-side only).
type Book struct {
	entries []Entry
	names   []string
	blob    []byte
}

// Encode builds the resident blob from entries and names. Entries are
// sorted by Key (stable, so equal-Key move order is preserved). The
// returned bytes are exactly what loads at BaseAddr.
func Encode(entries []Entry, names []string) []byte {
	es := make([]Entry, len(entries))
	copy(es, entries)
	sort.SliceStable(es, func(i, j int) bool { return es[i].Key < es[j].Key })

	nameOff := HeaderSize + len(es)*EntryStride
	total := nameOff
	for _, n := range names {
		total += 1 + len(n)
	}
	blob := make([]byte, total)
	blob[offMagic] = Magic0
	blob[offMagic+1] = Magic1
	binary.LittleEndian.PutUint16(blob[offCount:], uint16(len(es)))
	binary.LittleEndian.PutUint16(blob[offNameOff:], uint16(nameOff))
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

	p := nameOff
	for _, n := range names {
		blob[p] = byte(len(n))
		copy(blob[p+1:], n)
		p += 1 + len(n)
	}
	return blob
}

// Load parses a resident blob (as produced by Encode).
func Load(blob []byte) (*Book, error) {
	if len(blob) < HeaderSize {
		return nil, fmt.Errorf("book: blob too short (%d bytes)", len(blob))
	}
	if blob[offMagic] != Magic0 || blob[offMagic+1] != Magic1 {
		return nil, fmt.Errorf("book: bad magic %q%q", blob[offMagic], blob[offMagic+1])
	}
	if s := blob[offStride]; s != EntryStride {
		return nil, fmt.Errorf("book: stride %d != %d", s, EntryStride)
	}
	count := int(binary.LittleEndian.Uint16(blob[offCount:]))
	nameOff := int(binary.LittleEndian.Uint16(blob[offNameOff:]))
	nameCt := int(blob[offNameCt])
	if HeaderSize+count*EntryStride > len(blob) || nameOff > len(blob) {
		return nil, fmt.Errorf("book: truncated blob")
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

	names := make([]string, 0, nameCt)
	p := nameOff
	for range nameCt {
		if p >= len(blob) {
			return nil, fmt.Errorf("book: name table truncated")
		}
		n := int(blob[p])
		p++
		if p+n > len(blob) {
			return nil, fmt.Errorf("book: name string truncated")
		}
		names = append(names, string(blob[p:p+n]))
		p += n
	}
	return &Book{entries: entries, names: names, blob: blob}, nil
}

// Size is the blob byte length (the resident footprint).
func (b *Book) Size() int { return len(b.blob) }

// Blob returns the raw resident bytes (for injection at $2000).
func (b *Book) Blob() []byte { return b.blob }

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
