package chesstest

import (
	"fmt"
	"io"

	"github.com/zellyn/chess6502/harness"
)

// BookBase is the resident opening book's fixed load address: AUXILIARY RAM
// $0200. Mirrors internal/book.BaseAddr and the asm BOOK_BASE in
// asm/book.inc. It was main $2000 until the DHGR board renderer claimed
// $2000-$3FFF in both banks.
const BookBase = 0x0200

// LoadBook installs the resident opening-book blob into the emulated
// machine's AUXILIARY memory at BookBase ($0200). The asm probe detects the
// book by the 'B','K' magic there, so an unloaded machine (every existing
// test) is a pure no-book no-op.
//
// On real hardware the disk delivers the blob to main $2000 in the boot
// loader's SECOND stage and asm/m8.s's copier moves it to aux $0200 before
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
