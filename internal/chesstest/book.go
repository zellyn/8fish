package chesstest

import (
	"bytes"
	"fmt"
	"io"

	"github.com/zellyn/chess6502/harness"
)

// BookBase is the resident opening book's fixed load address: AUXILIARY RAM
// $0800. Mirrors internal/book.BaseAddr and the asm BOOK_BASE in
// asm/book.inc. It was main $2000 until the DHGR board renderer claimed
// $2000-$3FFF in both banks, and aux $0200 until MIXED MODE claimed
// $0400-$07FF for the 80-column text window's even columns.
//
// Only the ENTRIES live here. The name table is in Language Card bank 2 and
// the probe never reads it (see package book's doc).
const BookBase = 0x0800

// LoadBook installs the resident opening-book ENTRIES blob into the emulated
// machine's AUXILIARY memory at BookBase ($0800). The asm probe detects the
// book by the 'B','K' magic there, so an unloaded machine (every existing
// test) is a pure no-book no-op.
//
// On real hardware the disk delivers the blob to main $2000 in the boot
// loader's SECOND stage and asm/m8.s's copier moves it to aux $0800 before
// anything runs; here we poke the identical bytes straight into aux, which is
// the state the probe actually sees.
func LoadBook(m *harness.Machine, blob []byte) {
	copy(m.Mem.Aux[BookBase:], blob)
}

// BookProbeResult is the outcome of one on-device (asm) book probe.
type BookProbeResult struct {
	Hit                     bool // true: a book move was selected
	From, To, Flags, NameID byte // the selected move (engine encoding) + name id
	Move                    string
	Cycles                  uint64
}

// AsmBookProbe runs the engine's resident book probe (the asm bookentry
// point) over pos with the 32-bit random value r, returning the move the
// on-device probe selects. It is the exact selection a real-hardware engine
// performs: a fresh machine is built with the position poked, the blob
// loaded into AUX $0800 (LoadBook -- the resident home since MIXED MODE took
// aux $0400-$07FF for the 80-column text window's even columns), r poked into
// BOOKRND, and execution started at bookentry (which computes HASH0-3 via
// evalinit, then binary-searches + weighted-picks). The parity test asserts
// this equals internal/book.Book.Probe.
//
// bookentry is the address of the asm bookentry label (from engine.lbl).
func AsmBookProbe(bin []byte, defs Defs, bookentry uint16, blob []byte, pos *Position, r uint32) (*BookProbeResult, error) {
	return AsmBookProbeAt(bin, defs, bookentry, 0, BookBase, blob, pos, r)
}

// AsmBookProbeAt is AsmBookProbe with the probe RE-BASED: the blob is loaded
// into aux at base, and the engine's `bookpg` byte (address bookpg, from
// engine.lbl; 0 = leave the shipped default) is poked to base's page — the
// exact one-byte re-point asm/m8.s performs when the BIG BOOK is resident in
// the idle TT window at aux $4000. Everything else — the machine, the probe
// entry, the read-only blob check — is identical, which is the point: the
// selection code must not know which blob it is walking.
func AsmBookProbeAt(bin []byte, defs Defs, bookentry, bookpg uint16, base uint16, blob []byte, pos *Position, r uint32) (*BookProbeResult, error) {
	m, err := NewMachine(bin, defs, pos, 0, io.Discard)
	if err != nil {
		return nil, err
	}
	copy(m.Mem.Aux[base:], blob)
	if bookpg != 0 {
		m.Mem.Main[bookpg] = byte(base >> 8)
	}
	rnd := defs["BOOKRND"]
	m.Mem.Main[rnd] = byte(r)
	m.Mem.Main[rnd+1] = byte(r >> 8)
	m.Mem.Main[rnd+2] = byte(r >> 16)
	m.Mem.Main[rnd+3] = byte(r >> 24)

	m.CPU.SetPC(bookentry)
	exited, code, err := m.Run(50_000_000)
	if err != nil {
		return nil, err
	}
	if !exited {
		return nil, fmt.Errorf("book probe did not finish")
	}
	if code != 0 {
		return nil, fmt.Errorf("book probe exited with code %d", code)
	}
	// THE BLOB IS READ-ONLY, and since it moved to aux that is worth checking
	// rather than assuming. bkfetch/bkhdr copy entries out of aux into BKENT/
	// BKHDR at main $03D6-$03E6 with RAMRD on and RAMWRT off; if RAMWRT is
	// ever left on, those stores land at AUX $03D6-$03E6 instead.
	//
	// While the blob was based at aux $0200 that WAS the book -- offsets
	// $01D6-$01E6, inside entries 52-53 -- and one slipped switch corrupted it
	// permanently. At $0800 the landing pads no longer overlap it, so today
	// the same slip would spray into aux page 3 rather than into the book.
	// The check stays anyway, and not out of sentiment: it is the only thing
	// in the suite that would notice a future primitive whose pointer
	// arithmetic writes THROUGH the blob, and it costs O(blob) per probe
	// against an emulated 6502 search, i.e. nothing.
	if got := m.Mem.Aux[base : int(base)+len(blob)]; !bytes.Equal(got, blob) {
		for i := range blob {
			if blob[i] != got[i] {
				return nil, fmt.Errorf("the probe MODIFIED the resident blob at aux "+
					"$%04X (offset %d of %d): $%02X became $%02X. The blob is read-only; "+
					"a write into it means bkfetch/bkhdr ran with RAMWRT on (see asm/book.s)",
					int(base)+i, i, len(blob), blob[i], got[i])
			}
		}
	}

	res := &BookProbeResult{Cycles: m.Cycles}
	if m.Mem.Main[defs["BOOKHIT"]] == 0 {
		return res, nil // out of book / no book
	}
	res.Hit = true
	res.From = m.Mem.Main[defs["BESTFROM"]]
	res.To = m.Mem.Main[defs["BESTTO"]]
	res.Flags = m.Mem.Main[defs["BESTFLAGS"]]
	res.NameID = m.Mem.Main[defs["CUROPENING"]]
	res.Move = MoveUCI(res.From, res.To, res.Flags)
	return res, nil
}
