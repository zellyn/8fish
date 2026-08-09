package ui

// diskboot.go boots the SHIPPING ARTEFACT: asm/8fish.dsk, from the disk,
// through the Disk II controller's own boot ROM, on an Apple IIe memory
// system with auxiliary RAM.
//
// It exists because "the image runs when a Go test lays it out in RAM by hand"
// and "the disk boots" are different claims, and only the second one is what a
// person with a IIe gets. The pieces:
//
//	goapple2/iie      the IIe 128K memory system: main + aux, RAMRD/RAMWRT/
//	                  ALTZP, the Language Card in both banks, the keyboard,
//	                  ALTCHARSET. This is the part harness.Machine already
//	                  uses, and it is why the transposition table in aux
//	                  $0200-$81FF has somewhere to live.
//	goapple2/cards    the real Disk II controller, driven by the real
//	                  341-0027 P5 boot ROM at $C600, reading a real
//	                  nibblised .dsk. Nothing about the disk is faked: the
//	                  Standard Delivery loader seeks, reads address and data
//	                  fields, and denibblises, exactly as on hardware.
//	go6502/cpu        the same 6502 core the rest of the project runs on.
//
// This is the "small bridge" version of goapple2 stage 2: rather than
// retrofitting the interactive Apple2 machine (flat 64K, ][+ only) with aux
// memory, it wires the existing iie memory model to the existing disk card.
// It renders nothing -- the caller reads the text page out of RAM -- so no
// video scanner is involved.
//
// 80STORE and the $C100-$CFFF internal-ROM switching (INTCXROM/SLOTC3ROM/
// INTC8ROM) ARE modelled by goapple2/iie now, though no internal ROM is
// loaded here, so $C100-$CFFF is slots only. NOT MODELLED: aux video, and
// every slot other than 6. Accesses to unimplemented $C0xx locations are
// counted in the iie model's Unhandled map, and DiskMachine.Unhandled
// surfaces them so an unnoticed dependency is loud.

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zellyn/go6502/cpu"
	"github.com/zellyn/goapple2/cards"
	"github.com/zellyn/goapple2/disk"
	"github.com/zellyn/goapple2/iie"
)

// DiskSlot is the slot the Disk II controller sits in (and therefore the
// slot the machine boots from: entry at $C600 is what PR#6 does).
const DiskSlot = 6

// BootEntry is the Disk II boot ROM's entry point.
const BootEntry = 0xC000 + DiskSlot*0x100

// romSize is the $D000-$FFFF Language Card ROM image size.
const romSize = 0x3000

// DiskMemory is an iie.Memory with a Disk II controller wired into slot 6:
// its 256-byte boot ROM at $C600-$C6FF and its data/control registers at
// $C0E0-$C0EF. Everything else is the plain IIe memory system.
type DiskMemory struct {
	*iie.Memory
	Card *cards.DiskCard

	// waitingKey is set when the program read the keyboard data latch with
	// no key pending, i.e. it is sitting in its "wait for a keypress" loop.
	waitingKey bool
}

func (d *DiskMemory) cardAddr(addr uint16) (isCard bool, reg byte, romByte bool) {
	switch {
	case addr >= 0xC080 && addr < 0xC100:
		return byte((addr-0xC080)>>4) == DiskSlot, byte(addr & 0x0F), false
	case addr >= 0xC100 && addr < 0xC800:
		return byte((addr-0xC000)>>8) == DiskSlot, byte(addr & 0xFF), true
	}
	return false, 0, false
}

// Read implements cpu.Memory.
func (d *DiskMemory) Read(addr uint16) byte {
	if isCard, reg, romByte := d.cardAddr(addr); isCard {
		if romByte {
			return d.Card.Read256(reg)
		}
		return d.Card.Read16(reg)
	}
	// The keyboard DATA latch is $C000 exactly (KBD; $C010 is the strobe). Key
	// on that one address, not the whole $C000-$C00F block: the resident
	// ProRWTS2 driver's allow_aux setaux does `sta $C002,x`/`sta $C004,x`, and
	// a 6502 indexed store dummy-READS its (unfixed) target first -- $C002-$C005
	// here -- which is not a keyboard poll. Counting those stalled RunToKeyboard
	// inside the driver mid-load. 8fish only ever reads the keyboard at $C000.
	if addr == 0xC000 && !d.Memory.KeyWaiting() {
		d.waitingKey = true
	}
	return d.Memory.Read(addr)
}

// Write implements cpu.Memory.
func (d *DiskMemory) Write(addr uint16, val byte) {
	if isCard, reg, romByte := d.cardAddr(addr); isCard {
		if romByte {
			d.Card.Write256(reg, val)
		} else {
			d.Card.Write16(reg, val)
		}
		return
	}
	d.Memory.Write(addr, val)
}

// cardManager satisfies cards.CardManager for a machine with exactly one
// card, which never asks for the $C800 ROM area or the 12K bank.
type cardManager struct{}

func (cardManager) HandleROM(onOff bool, slot byte) {}
func (cardManager) Handle12k(onOff bool, slot byte) {}
func (cardManager) EmptyRead() byte                 { return 0xFF }

// DiskMachine is a headless Apple IIe booting a 5.25" floppy from slot 6.
type DiskMachine struct {
	Mem    *DiskMemory
	CPU    cpu.Cpu
	Cycles uint64
	// ROMName records which $D000-$FFFF ROM image was used (see FindROM).
	ROMName string

	// PonderDefault is the PONDERON value the disk image set (1 — the disk
	// ponders). The first time the boot reaches the keyboard it is captured and
	// pondering is turned off, because the harness parks the machine on a
	// non-blocking keyboard poll (see Machine.PonderDefault); pondering itself
	// is gated by internal/ui/ponder_test.go on the HARNESSKBD image.
	PonderDefault  byte
	ponderDisabled bool
}

// FindROM returns a $D000-$FFFF ROM image and a name for it, preferring a
// real Apple IIe ROM and falling back to the Apple ][+ ROM that ships in the
// sibling goapple2 checkout.
//
// The fallback is honest rather than lazy: the ONLY ROM the 8fish boot path
// touches is the Disk II boot ROM's two calls into the monitor -- $FF58 (an
// RTS, used to find the controller's own slot) and $FCA8 (WAIT) -- and both
// are at the same addresses with the same behaviour on a ][+ and a IIe. From
// the moment the copier reads $C08B twice, ROM is banked out and never read
// again. Where a genuine IIe ROM is available, though, we use it.
//
// Sources tried, in order:
//
//	$CHESS6502_A2E_ROM              a raw 12,288-byte $D000-$FFFF image
//	MAME's apple2e ROM set          342-0134-a.64 + 342-0135-b.64
//	$GOAPPLE2_ROMS/apple2+.rom      goapple2's Apple ][+ ROM
func FindROM(goapple2Roms string) ([]byte, string, error) {
	if p := os.Getenv("CHESS6502_A2E_ROM"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, "", err
		}
		if len(b) != romSize {
			return nil, "", fmt.Errorf("%s: want %d bytes, got %d", p, romSize, len(b))
		}
		if err := checkMonitorROM(b); err != nil {
			return nil, "", fmt.Errorf("%s: %w", p, err)
		}
		return b, "Apple IIe ($CHESS6502_A2E_ROM)", nil
	}
	if b, err := mameIIeROM(); err == nil {
		return b, "Apple IIe (MAME apple2e: 342-0134-a + 342-0135-b)", nil
	}
	p := filepath.Join(goapple2Roms, "apple2+.rom")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", fmt.Errorf("no ROM image: %w", err)
	}
	if len(b) != romSize {
		return nil, "", fmt.Errorf("%s: want %d bytes, got %d", p, romSize, len(b))
	}
	if err := checkMonitorROM(b); err != nil {
		return nil, "", fmt.Errorf("%s: %w", p, err)
	}
	return b, "Apple ][+ (goapple2 apple2+.rom)", nil
}

// mameIIeROM assembles $D000-$FFFF from a locally installed MAME apple2e ROM
// set, if there is one. 342-0135-b.64 is the CD ROM ($C000-$DFFF) and
// 342-0134-a.64 is the EF ROM ($E000-$FFFF), so the Language Card ROM image is
// the top 4K of the former followed by all of the latter. The result is
// checked against the two fixed points the Disk II boot ROM calls -- $FF58
// must be RTS and $FCA8 must be WAIT -- because a ROM assembled the wrong way
// round fails much later and much less legibly, as an unknown opcode inside
// the boot ROM. (It did, the first time: with the halves swapped, $FF58 held
// $04 and the boot died at $FF59.)
func mameIIeROM() ([]byte, error) {
	dirs := []string{
		os.Getenv("MAME_ROMS"),
		filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Ample", "roms"),
		filepath.Join(os.Getenv("HOME"), "mame", "roms"),
	}
	for _, d := range dirs {
		if d == "" {
			continue
		}
		zr, err := zip.OpenReader(filepath.Join(d, "apple2e.zip"))
		if err != nil {
			continue
		}
		parts := map[string][]byte{}
		for _, f := range zr.File {
			switch f.Name {
			case "342-0134-a.64", "342-0135-b.64":
				rc, err := f.Open()
				if err != nil {
					continue
				}
				b, err := io.ReadAll(rc)
				rc.Close()
				if err == nil {
					parts[f.Name] = b
				}
			}
		}
		zr.Close()
		cd, ef := parts["342-0135-b.64"], parts["342-0134-a.64"]
		if len(cd) == 0x2000 && len(ef) == 0x2000 {
			rom := append(append([]byte{}, cd[0x1000:]...), ef...)
			if err := checkMonitorROM(rom); err != nil {
				return nil, err
			}
			return rom, nil
		}
	}
	return nil, errors.New("no MAME apple2e ROM set found")
}

// checkMonitorROM verifies the two entry points the Disk II boot ROM calls.
func checkMonitorROM(rom []byte) error {
	for _, c := range []struct {
		addr int
		want byte
		what string
	}{
		{0xFF58, 0x60, "RTS (the boot ROM's find-my-own-slot trick)"},
		{0xFCA8, 0x38, "SEC, the first instruction of the monitor's WAIT routine"},
	} {
		if got := rom[c.addr-0xD000]; got != c.want {
			return fmt.Errorf("ROM image looks wrong: $%04X is $%02X, want $%02X -- %s",
				c.addr, got, c.want, c.what)
		}
	}
	return nil
}

// NewDiskMachine builds a headless Apple IIe with the given .dsk in drive 1
// of a Disk II controller in slot 6, and points the CPU at the controller's
// boot ROM -- which is exactly what typing PR#6 (or powering on with a disk
// in the drive) does. Nothing is loaded into RAM: every byte of 8fish that
// ends up in memory gets there by being read off the disk.
func NewDiskMachine(dskPath, romDir string) (*DiskMachine, error) {
	diskRom, err := os.ReadFile(filepath.Join(romDir,
		"Apple Disk II 16 Sector Interface Card ROM P5 - 341-0027.bin"))
	if err != nil {
		return nil, fmt.Errorf("disk II boot ROM: %w", err)
	}
	rom, romName, err := FindROM(romDir)
	if err != nil {
		return nil, err
	}

	mem := &DiskMemory{Memory: iie.New()}
	copy(mem.ROM[:], rom)

	card, err := cards.NewDiskCard(diskRom, DiskSlot, cardManager{})
	if err != nil {
		return nil, fmt.Errorf("disk card: %w", err)
	}
	card.Init()
	mem.Card = card

	d, err := disk.DiskFromFile(dskPath, 0)
	if err != nil {
		return nil, fmt.Errorf("load disk %q: %w", dskPath, err)
	}
	card.LoadDisk(d, 0)

	m := &DiskMachine{Mem: mem, ROMName: romName}
	mem.Memory.Clock = func() uint64 { return m.Cycles }
	m.CPU = cpu.NewCPU(mem, func() { m.Cycles++; card.Tick() }, cpu.VERSION_6502)
	m.CPU.Reset()
	m.CPU.SetPC(BootEntry)
	return m, nil
}

// Step executes one instruction.
func (m *DiskMachine) Step() error { return m.CPU.Step() }

// RunUntilPC runs until the CPU is about to execute the instruction at pc,
// or maxCycles have elapsed. It reports whether pc was reached.
func (m *DiskMachine) RunUntilPC(pc uint16, maxCycles uint64) (bool, error) {
	limit := m.Cycles + maxCycles
	for m.Cycles < limit {
		if m.CPU.PC() == pc {
			return true, nil
		}
		if err := m.CPU.Step(); err != nil {
			return false, err
		}
	}
	return false, nil
}

// RunToKeyboard runs until the program polls the keyboard data latch with no
// key waiting -- the UI's main loop blocking in entkey, which is the only
// point at which its state is quiescent. It reports whether that happened.
func (m *DiskMachine) RunToKeyboard(maxCycles uint64) (bool, error) {
	limit := m.Cycles + maxCycles
	m.Mem.waitingKey = false
	for m.Cycles < limit {
		if err := m.CPU.Step(); err != nil {
			return false, err
		}
		if m.Mem.waitingKey {
			if !m.ponderDisabled { // at the first prompt the game has not started
				m.PonderDefault = m.Mem.Main[ponderOnAddr]
				m.Mem.Main[ponderOnAddr] = 0
				m.ponderDisabled = true
			}
			return true, nil
		}
	}
	return false, nil
}

// ponderOnAddr is asm/ui.s PONDERON, mirrored (an equate, not an emitted
// label). Keep equal to internal/ui.PONDERON.
const ponderOnAddr = 0xF7AC

// SendKey presses one key on the modelled IIe keyboard. The key stays in the
// latch until the program clears the strobe at $C010.
func (m *DiskMachine) SendKey(k byte) { m.Mem.KeyPress(k) }

// Screen snapshots text page 1 out of main RAM. The UI writes PAGE1 and
// 80STORE is off, so the text page is plain main RAM at $0400-$07FF.
func (m *DiskMachine) Screen() *Screen {
	var s Screen
	for row := range 24 {
		base := RowBase(row)
		for col := range 40 {
			s.Raw[row][col] = m.Mem.Main[base+uint16(col)]
		}
	}
	return &s
}

// Window snapshots the mixed-mode 80-column text window -- the four rows under
// the board -- out of BOTH banks. In 80-column text the aux byte is the even
// column and the main byte the odd one, so nothing short of both banks is a
// reading of that window.
func (m *DiskMachine) Window() *Window80 {
	return window80(&m.Mem.Main, &m.Mem.Aux)
}

// Unhandled returns the $C0xx locations the IIe model does not implement that
// the booted program touched, if any.
func (m *DiskMachine) Unhandled() map[uint16]int { return m.Mem.Memory.Unhandled }

// RomDir is where the Apple II ROMs live in the sibling goapple2 checkout,
// overridable with GOAPPLE2_ROMS. Same convention as internal/sargon.
func RomDir() string {
	if d := os.Getenv("GOAPPLE2_ROMS"); d != "" {
		return d
	}
	return "/Users/zellyn/gh/goapple2/data/roms"
}

// Key presses one key and runs the booted image until it asks for the next
// one. The key goes into the modelled IIe keyboard latch at $C000 and the
// image clears the strobe at $C010 — the hardware path, because the disk
// carries the shipping build and the shipping build has no other way in.
func (m *DiskMachine) Key(c byte, maxCycles uint64) error {
	m.SendKey(c)
	ok, err := m.RunToKeyboard(maxCycles)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("after key %q the image ran %d cycles without asking for another",
			c, maxCycles)
	}
	return nil
}

// BootToPrompt advances a freshly created DiskMachine from power-on, PAST the
// boot splash and the big-book load that runs behind it, to the game's first
// keyboard prompt. The splash no longer waits for a key: it shows full-screen
// while the big book loads straight into aux, then auto-advances to the board
// when the load completes (asm/m8.s m8main). So the FIRST keyboard poll is the
// game's own prompt (mloop), and a single RunToKeyboard reaches it -- no key is
// pressed here, and none is consumed, so a key a test sends afterward is the
// game's genuine first input. It reports whether the game prompt was reached.
func (m *DiskMachine) BootToPrompt(maxCycles uint64) (bool, error) {
	return m.RunToKeyboard(maxCycles)
}

// Enter types a line and the RETURN that submits it, one key at a time, the
// way a human does (and the way asm/entropy.inc expects: the arrival time of
// each keystroke is the engine's only seed material).
func (m *DiskMachine) Enter(s string, maxCycles uint64) error {
	for i := range len(s) {
		if err := m.Key(s[i], maxCycles); err != nil {
			return err
		}
	}
	return m.Key(0x0D, maxCycles)
}
