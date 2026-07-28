// Package harness is a headless test harness for 6502 binaries targeting
// the Apple IIe 128K memory system. It loads a raw binary into main RAM,
// jumps to an entry point, and runs at full host speed until the program
// stores to the exit trap, a caller-supplied cycle budget runs out, or the
// CPU hits an unknown opcode.
//
// It is the emulation core behind cmd/a2run; that command is a thin CLI
// wrapper around New and (*Machine).Run, so tests can run 6502 binaries
// in-process instead of shelling out.
//
// Harness I/O conventions (see docs/testing.md; on real hardware these
// addresses are plain RAM, and $BF00-$BFFF is reserved in both banks):
//
//	store to CoutAddr (main bank): emit the byte to Config.Cout
//	store to ExitAddr (main bank): exit; the stored value is the exit code
//
// Traps fire only on main-bank stores; aux writes (RAMWRT on) to the same
// addresses are ordinary memory writes. Reads of $C019 return VBL status
// derived from the cycle counter.
package harness

import (
	"fmt"
	"io"

	"github.com/zellyn/go6502/cpu"
	"github.com/zellyn/goapple2/iie"
)

// ROMSize is the required length of Config.ROM: the $D000-$FFFF Language
// Card ROM image.
const ROMSize = 0x3000

// EffectiveHz is the IIe's effective clock rate: 65 cycles per 63.7µs
// line, averaged over long-cycle and short-cycle lines.
const EffectiveHz = 1020484

// Config configures a Machine. Bin is required; the rest have meaningful
// zero values (Org 0, Entry 0, trap addresses 0, no ROM, Cout discarded,
// tracing off), though callers will usually want to set Org, Entry, and
// the trap addresses explicitly.
type Config struct {
	// Bin is the raw binary to load into main RAM at Org.
	Bin []byte

	// Org is the load address for Bin in main RAM.
	Org uint16

	// Entry is the address execution starts at. The harness always uses
	// Entry as given; callers that want "default to Org" semantics (as
	// cmd/a2run's -entry flag does) must resolve that themselves before
	// constructing Config.
	Entry uint16

	// CoutAddr and ExitAddr are the main-bank store-trap addresses (see
	// the package doc for trap semantics).
	CoutAddr uint16
	ExitAddr uint16

	// ROM, if non-nil, is the $D000-$FFFF Language Card ROM image and must
	// be exactly ROMSize bytes.
	ROM []byte

	// Cout receives bytes stored to CoutAddr. If nil, they are discarded.
	Cout io.Writer

	// InAddr/InStatusAddr/ClockAddr enable the read traps (input byte,
	// input status, latched cycle counter — see TrapMemory). Zero
	// disables all three.
	InAddr       uint16
	InStatusAddr uint16
	ClockAddr    uint16

	// RealKeyboard makes the machine serve keystrokes the way an Apple IIe
	// does — from the $C000 data latch, cleared by $C010 — instead of
	// through the InAddr/InStatusAddr traps. Feed it with Machine.SendKey.
	// This is what lets a test drive the SHIPPING build of an image rather
	// than a harness-keyboard variant of it.
	RealKeyboard bool

	// Trace, if true, prints each executed instruction to stderr (a
	// behavior of the underlying go6502/cpu package, which traces
	// unconditionally to os.Stderr regardless of other I/O routing).
	Trace bool
}

// Machine is a loaded, runnable 6502 program plus the memory system and CPU
// backing it. The zero Machine is not usable; construct one with New.
type Machine struct {
	// Mem is the machine's memory system, including the I/O traps.
	Mem *TrapMemory
	// CPU is the 6502 core driving Mem.
	CPU cpu.Cpu
	// Cycles is the total number of CPU cycles executed so far.
	Cycles uint64
}

// New validates cfg and returns a Machine with Bin loaded at Org, ready to
// run from Entry. It returns an error if the binary overruns 64K or ROM is
// present but not exactly ROMSize bytes.
func New(cfg Config) (*Machine, error) {
	if int(cfg.Org)+len(cfg.Bin) > 0x10000 {
		return nil, fmt.Errorf("binary (%d bytes at org %#x) overruns 64K by %d bytes",
			len(cfg.Bin), cfg.Org, int(cfg.Org)+len(cfg.Bin)-0x10000)
	}
	if cfg.ROM != nil && len(cfg.ROM) != ROMSize {
		return nil, fmt.Errorf("bad ROM image: need %d bytes, got %d", ROMSize, len(cfg.ROM))
	}

	mem := &TrapMemory{
		Memory:       iie.New(),
		CoutAddr:     cfg.CoutAddr,
		ExitAddr:     cfg.ExitAddr,
		Cout:         cfg.Cout,
		InAddr:       cfg.InAddr,
		InStatusAddr: cfg.InStatusAddr,
		ClockAddr:    cfg.ClockAddr,
		RealKeyboard: cfg.RealKeyboard,
	}
	if cfg.ROM != nil {
		copy(mem.ROM[:], cfg.ROM)
	}
	copy(mem.Main[cfg.Org:], cfg.Bin)

	m := &Machine{Mem: mem}
	mem.Clock = func() uint64 { return m.Cycles }
	m.CPU = cpu.NewCPU(mem, func() { m.Cycles++ }, cpu.VERSION_6502)
	m.CPU.Reset()
	m.CPU.SetPC(cfg.Entry)
	m.CPU.Print(cfg.Trace)
	return m, nil
}

// Run executes instructions until the exit trap fires, the CPU hits an
// unknown opcode, or maxCycles cycles have elapsed during this call (the
// cycle limit is checked between instructions, so a run may overshoot it by
// up to 7 cycles). err is non-nil only for a CPU error; running out of
// maxCycles without exiting is reported as exited == false with a nil err.
//
// Run is resumable: each call gives it a fresh budget of maxCycles on top
// of the cycles already run, so calling Run again after it returns with
// exited == false and err == nil continues exactly where the previous call
// left off.
// Run also returns (with exited == false) when the program polls the
// input-status trap while no input is pending — check WaitingForInput,
// supply bytes with SendInput, and call Run again to continue.
func (m *Machine) Run(maxCycles uint64) (exited bool, exitCode byte, err error) {
	limit := m.Cycles + maxCycles
	m.Mem.waitingInput = false
	for !m.Mem.exited && m.Cycles < limit && !m.Mem.waitingInput {
		if stepErr := m.CPU.Step(); stepErr != nil {
			return false, 0, stepErr
		}
	}
	return m.Mem.exited, m.Mem.exitCode, nil
}

// RunProfile executes like Run, additionally invoking fn after every
// instruction with that instruction's address and cycle cost. Roughly
// 2-3x slower than Run; intended for profiling and diagnostics.
func (m *Machine) RunProfile(maxCycles uint64, fn func(pc uint16, cycles uint8)) (exited bool, exitCode byte, err error) {
	limit := m.Cycles + maxCycles
	m.Mem.waitingInput = false
	for !m.Mem.exited && m.Cycles < limit && !m.Mem.waitingInput {
		pc := m.CPU.PC()
		before := m.Cycles
		if stepErr := m.CPU.Step(); stepErr != nil {
			return false, 0, stepErr
		}
		fn(pc, uint8(m.Cycles-before))
	}
	return m.Mem.exited, m.Mem.exitCode, nil
}

// WaitingForInput reports whether the last Run returned because the
// program polled the input-status trap with an empty input buffer.
func (m *Machine) WaitingForInput() bool { return m.Mem.waitingInput }

// SendInput appends bytes to the input buffer served by the input trap.
func (m *Machine) SendInput(data []byte) { m.Mem.Input = append(m.Mem.Input, data...) }

// SendKey presses and releases one key on the modelled Apple IIe keyboard
// (Config.RealKeyboard). The key is latched at $C000 with the strobe set and
// stays there until the program clears it with $C010 — one key at a time,
// exactly like the hardware's single-byte latch, so callers must run the
// machine between keystrokes rather than queueing a whole line.
func (m *Machine) SendKey(key byte) { m.Mem.KeyPress(key) }
