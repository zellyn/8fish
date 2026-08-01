package harness

import (
	"io"

	"github.com/zellyn/goapple2/iie"
)

// TrapMemory is an iie.Memory with the harness's I/O traps layered on top:
// a main-bank store to CoutAddr emits the byte to Cout, and a main-bank
// store to ExitAddr ends the run with the stored byte as the exit code.
//
// Traps model main-bank RAM locations: writes made with RAMWRT on (i.e. to
// the aux bank) are ordinary memory writes and never fire a trap. Peek is
// promoted from the embedded *iie.Memory unchanged, so memory dumps remain
// side-effect free.
//
// Both the read and the store traps require the machine to be in the "all
// main" banking the harness I/O convention assumes: RAMRD off AND RAMWRT
// off. RAMWRT matters to the READ traps because of a 6502 bus artifact:
// `sta (zp),Y` (and the other indexed-store modes) perform a hardware-
// accurate DUMMY READ of the target address one cycle before the write.
// That read is a real bus cycle, so it follows RAMRD, not RAMWRT — code
// running the aux-write discipline of asm/tt.s (RAMWRT on, RAMRD off; see
// D4) therefore emits a MAIN-bank read of every address it writes to AUX.
// On real hardware that is harmless, because $BFF0-$BFFF is plain RAM
// there. Here it is not: the read traps have side effects (InAddr pops an
// input byte, InStatusAddr sets WaitingForInput and makes Run return), so
// an aux data structure covering $BFF0-$BFF7 would stall every run that
// wrote to it. Gating on RAMWRT reproduces the hardware behaviour instead
// of inventing a hazard the hardware does not have, which is what lets the
// transposition table run all the way to aux $BFFF (docs/testing.md D8).
//
// Read traps (all main-bank, like the store traps; fixed addresses per
// docs/testing.md):
//
//	InAddr ($BFF1): pop and return the next input byte (0 if none)
//	InStatusAddr ($BFF2): $80 if input is waiting, else 0. Reading with
//	    an empty buffer also sets the WaitingForInput flag, which makes
//	    Machine.Run return so a driving process can supply input.
//	ClockAddr ($BFF4-$BFF6): cycle count / 256, 24 bits little-endian,
//	    latched when the low byte ($BFF4) is read.
type TrapMemory struct {
	*iie.Memory
	CoutAddr uint16
	ExitAddr uint16
	Cout     io.Writer

	InAddr       uint16
	InStatusAddr uint16
	ClockAddr    uint16
	Input        []byte // pending input; append via Machine.SendInput

	// RealKeyboard drives the SHIPPING keyboard path instead of the input
	// traps: keys are injected into the iie.Memory keyboard latch with
	// Machine.SendKey and the program reads them from $C000/$C010, exactly
	// as it will on hardware. A read of the keyboard latch with no key
	// waiting sets WaitingForInput, which is the same "the program is
	// blocked" signal InStatusAddr gives — the program is in its keyboard
	// poll loop and nothing will change until a key arrives.
	RealKeyboard bool

	exited       bool
	exitCode     byte
	waitingInput bool
	clockLatch   [3]byte
}

// Write implements cpu.Memory, applying the harness's store traps before
// delegating to the underlying iie.Memory.
func (t *TrapMemory) Write(addr uint16, val byte) {
	// Traps model main-bank RAM locations: ignore aux-bank stores.
	if !t.Memory.RamWrt {
		switch addr {
		case t.CoutAddr:
			if t.Cout != nil {
				t.Cout.Write([]byte{val})
			}
		case t.ExitAddr:
			t.exited = true
			t.exitCode = val
		}
	}
	t.Memory.Write(addr, val)
}

// Read implements cpu.Memory, applying the read traps (main-bank reads
// only) before delegating to the underlying iie.Memory.
func (t *TrapMemory) Read(addr uint16) byte {
	// The keyboard is I/O, not RAM: RAMRD does not shadow it.
	if t.RealKeyboard && addr >= 0xC000 && addr <= 0xC00F && !t.Memory.KeyWaiting() {
		t.waitingInput = true
	}
	// RamWrt as well as RamRd: with RAMWRT on this may be the dummy read
	// cycle of an indexed store to AUX, which on hardware has no effect
	// (see the type doc).
	if !t.Memory.RamRd && !t.Memory.RamWrt {
		switch {
		case t.InAddr != 0 && addr == t.InAddr:
			if len(t.Input) == 0 {
				return 0
			}
			b := t.Input[0]
			t.Input = t.Input[1:]
			return b
		case t.InStatusAddr != 0 && addr == t.InStatusAddr:
			if len(t.Input) == 0 {
				t.waitingInput = true
				return 0
			}
			return 0x80
		case t.ClockAddr != 0 && addr == t.ClockAddr:
			if t.Memory.Clock != nil {
				c := t.Memory.Clock() >> 8
				t.clockLatch = [3]byte{byte(c), byte(c >> 8), byte(c >> 16)}
			}
			return t.clockLatch[0]
		case t.ClockAddr != 0 && (addr == t.ClockAddr+1 || addr == t.ClockAddr+2):
			return t.clockLatch[addr-t.ClockAddr]
		}
	}
	return t.Memory.Read(addr)
}

// Exited reports whether the exit trap has fired.
func (t *TrapMemory) Exited() bool { return t.exited }

// ExitCode returns the byte most recently stored to ExitAddr. Meaningful
// only once Exited reports true.
func (t *TrapMemory) ExitCode() byte { return t.exitCode }
