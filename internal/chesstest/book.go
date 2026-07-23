package chesstest

import (
	"fmt"
	"io"

	"github.com/zellyn/chess6502/harness"
)

// BookBase is the resident opening book's fixed load address ($2000), the
// verified-free hi-res page-1 hole. Mirrors internal/book.BaseAddr and the
// asm BOOK_BASE in asm/book.inc.
const BookBase = 0x2000

// LoadBook installs the resident opening-book blob into the emulated
// machine's MAIN memory at BookBase ($2000). The asm probe detects the book
// by the 'B','K' magic at $2000, so an unloaded machine (every existing
// test) is a pure no-book no-op.
//
// On real hardware this is the loader's one-time disk read of the ~3.9 KB
// blob (e.g. 8 consecutive 512-byte sectors) into $2000-$3FFF; the blob
// stays resident and is never re-read. Here we poke the identical bytes.
func LoadBook(m *harness.Machine, blob []byte) {
	copy(m.Mem.Main[BookBase:], blob)
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
// loaded at $2000, r poked into BOOKRND, and execution started at bookentry
// (which computes HASH0-3 via evalinit, then binary-searches + weighted-
// picks). The parity test asserts this equals internal/book.Book.Probe.
//
// bookentry is the address of the asm bookentry label (from engine.lbl).
func AsmBookProbe(bin []byte, defs Defs, bookentry uint16, blob []byte, pos *Position, r uint32) (*BookProbeResult, error) {
	m, err := NewMachine(bin, defs, pos, 0, io.Discard)
	if err != nil {
		return nil, err
	}
	LoadBook(m, blob)
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
