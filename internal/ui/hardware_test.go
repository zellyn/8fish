package ui_test

// Real-hardware fidelity probes for the SHIPPING image (2026-07-28 review).
//
// Everything here drives asm/m8.bin — the build that goes on the disk —
// through goapple2/iie's modelled IIe keyboard, and asks questions about
// what a physical Apple IIe would do rather than what the harness does.
//
// Where the emulator cannot model the thing (80STORE, Ctrl-Reset), the
// finding is argued in the review text, not here; these are the parts that
// CAN be executed.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/ui"
	"github.com/zellyn/go6502/cpu"
	"github.com/zellyn/goapple2/iie"
)

// TestCapsLock: a IIe keyboard with CAPS LOCK UP sends lowercase ($E1-$FA,
// i.e. $61-$7A after the strobe bit is masked); with CAPS LOCK DOWN it sends
// uppercase ($C1-$DA -> $41-$5A). goapple2's keyboard latches key&$7F|$80,
// which is exactly what the hardware latch holds, so both cases are faithful
// here. Every input path must accept both: move entry, the single-letter
// commands, the promotion prompt and the level prompt.
func TestCapsLock(t *testing.T) {
	for _, tc := range []struct{ name, moves string }{
		{"lowercase (caps lock UP)", "e2e4"},
		{"uppercase (caps lock DOWN)", "E2E4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := bootShipping(t)
			if err := u.TwoPlayer(); err != nil {
				t.Fatal(err)
			}
			if err := u.Enter(tc.moves); err != nil {
				t.Fatal(err)
			}
			if got := u.History(); len(got) != 1 || got[0] != "e2e4" {
				t.Fatalf("typing %q played %v, want [e2e4]", tc.moves, got)
			}
		})
	}

	// Commands. "S" cycles sides, "T" takes back, "N" starts a new game,
	// "?" is help. Each must work with CAPS LOCK down.
	t.Run("uppercase commands", func(t *testing.T) {
		u := bootShipping(t)
		if err := u.TwoPlayer(); err != nil {
			t.Fatal(err)
		}
		if err := u.Enter("E2E4"); err != nil {
			t.Fatal(err)
		}
		if err := u.Enter("T"); err != nil { // takeback
			t.Fatal(err)
		}
		if got := u.History(); len(got) != 0 {
			t.Errorf("uppercase T did not take back: history %v", got)
		}
		if err := u.Enter("?"); err != nil {
			t.Fatal(err)
		}
		if got := u.Screen().Text(17); !strings.Contains(got, "MOVES ARE") {
			t.Errorf("uppercase ? gave row 17 = %q, want the help message", strings.TrimSpace(got))
		}
		if err := u.Enter("N"); err != nil {
			t.Fatal(err)
		}
		if got := u.Peek(ui.UIHCNT); got != 0 {
			t.Errorf("uppercase N did not start a new game: UIHCNT = %d", got)
		}
	})

	// The promotion prompt reads a bare key, not a line, so it has its own
	// case-folding call. Play a position where a pawn promotes and answer
	// the prompt in upper case.
	t.Run("uppercase promotion answer", func(t *testing.T) {
		u := bootShipping(t)
		if err := u.TwoPlayer(); err != nil {
			t.Fatal(err)
		}
		// 1.a4 b5 2.axb5 Nf6 3.b6 Ng8 4.bxa7 Nf6 5.axb8=? (capture-promotion)
		for _, m := range []string{"a2a4", "b7b5", "a4b5", "g8f6", "b5b6", "f6g8", "b6a7", "g8f6"} {
			if err := u.Enter(m); err != nil {
				t.Fatalf("%s: %v", m, err)
			}
		}
		if err := u.Enter("a7b8"); err != nil { // no fifth character: prompts
			t.Fatal(err)
		}
		if got := u.Screen().Text(17); !strings.Contains(got, "PROMOTE TO") {
			t.Fatalf("row 17 = %q, want the promotion prompt", strings.TrimSpace(got))
		}
		if err := u.Key('Q'); err != nil { // CAPS LOCK down
			t.Fatal(err)
		}
		h := u.History()
		if len(h) == 0 || h[len(h)-1] != "a7b8q" {
			t.Errorf("uppercase Q at the promotion prompt gave %v, want ...a7b8q", h)
		}
	})
}

// TestKeyboardEdgeCases: the keys a IIe keyboard produces that are not
// letters or digits. entkey masks the strobe off, so what the line editor
// sees is $00-$7F:
//
//	$08  left arrow   -> backspace
//	$7F  DELETE       -> backspace (the IIe DELETE key sends $FF)
//	$15  right arrow  -> ignored (it is a control code, < $20)
//	$0B / $0A  up / down arrow -> ignored
//	$1B  ESC          -> ignored on a line, CANCELS the modal prompts
//
// Nothing here may enter the buffer, hang the editor, or reach the board.
func TestKeyboardEdgeCases(t *testing.T) {
	u := bootShipping(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	// Arrows and ESC in the middle of a move must be swallowed, and the
	// move must still play.
	for _, k := range []byte{'e', '2', 0x15, 0x0B, 0x0A, 0x1B, 0x00, 0x1F, 'e', '4'} {
		if err := u.Key(k); err != nil {
			t.Fatalf("key $%02X: %v", k, err)
		}
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE? e2e4") {
		t.Fatalf("row 23 = %q, want e2e4 with the control keys swallowed", strings.TrimSpace(got))
	}
	if err := u.Key(0x0D); err != nil {
		t.Fatal(err)
	}
	if got := u.History(); len(got) != 1 || got[0] != "e2e4" {
		t.Fatalf("history %v, want [e2e4]", got)
	}
	// DELETE ($FF on the wire, $7F after the mask) is the other backspace.
	if err := u.Type("e7e5"); err != nil {
		t.Fatal(err)
	}
	if err := u.Key(0x7F); err != nil {
		t.Fatal(err)
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE? e7e ") {
		t.Errorf("after DELETE row 23 = %q, want e7e", strings.TrimSpace(got))
	}
	// Backspacing past the start of an empty line must not underflow.
	for range 6 {
		if err := u.Key(0x08); err != nil {
			t.Fatal(err)
		}
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE?  ") ||
		strings.Contains(got, "e7") {
		t.Errorf("after backspacing past the start row 23 = %q, want an empty line",
			strings.TrimSpace(got))
	}
	// ESC backs out of the modal LEVEL prompt (a prompt with no way out is
	// a trap), leaving the level alone.
	lvl := u.Peek(ui.UILEVEL)
	if err := u.Enter("l"); err != nil {
		t.Fatal(err)
	}
	if err := u.Key(0x1B); err != nil {
		t.Fatal(err)
	}
	if got := u.Peek(ui.UILEVEL); got != lvl {
		t.Errorf("ESC at the level prompt changed the level to %d, want %d", got, lvl)
	}
	if got := u.Screen().Text(23); !strings.Contains(got, "YOUR MOVE?") {
		t.Errorf("after ESC row 23 = %q, want the move prompt back", strings.TrimSpace(got))
	}
}

// TestScreenHolesUntouched: the eight per-page "screen holes" ($x478-$x47F,
// $x4F8-$x4FF, ... $x7F8-$x7FF) belong to peripheral firmware — on a IIe with
// a Disk II or a mouse card in a slot, scribbling there breaks the card.
// asm/ui.s says uicls deliberately avoids them; this asserts that NOTHING in
// a whole run of UI activity writes one, which uicls alone does not prove
// (uiputs, uiprompt, uititle and the panel all index off row bases too).
func TestScreenHolesUntouched(t *testing.T) {
	u := bootShipping(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	// Poison every hole byte with a distinctive value.
	var holes []uint16
	for page := uint16(0x0400); page < 0x0800; page += 0x80 {
		for off := uint16(0x78); off < 0x80; off++ {
			holes = append(holes, page+off)
		}
	}
	for i, a := range holes {
		u.Poke(a, byte(0x11+i))
	}
	// Exercise as much of the painter as a short session can: moves, an
	// illegal move, garbage input, help, level prompt, takeback, new game.
	for _, line := range []string{"e2e4", "e7e5", "g1f3", "b8c6", "f1b5"} {
		if err := u.Enter(line); err != nil {
			t.Fatal(err)
		}
	}
	for _, line := range []string{"e2e9", "zzzz", "?", "t"} {
		if err := u.Enter(line); err != nil {
			t.Fatal(err)
		}
	}
	if err := u.Enter("l"); err != nil { // level prompt
		t.Fatal(err)
	}
	if err := u.Key('2'); err != nil {
		t.Fatal(err)
	}
	if err := u.Enter("n"); err != nil {
		t.Fatal(err)
	}
	for i, a := range holes {
		if got, want := u.Peek(a), byte(0x11+i); got != want {
			t.Errorf("screen hole $%04X was written: $%02X, want $%02X", a, got, want)
		}
	}
}

// TestMoveNumberOverNinetyNine: the move panel formats the move number in a
// THREE-column right-aligned field (ui.s uidec3), so a game past move 99
// reads "100 e2e3 e2e3".
//
// It used to call uidec2, documented as "A = 0..99", and a real game can
// pass move 99 — with the ply cap gone the only ceiling is the 256-entry
// history, i.e. move 128 — so moves 100+ came out as PUNCTUATION: uidec2's
// tens digit is a raw counter, and 10 + '0' is ':', 11 ';', 12 '<'. This
// test was the characterization that pinned that defect; it now asserts the
// fix, including the two things a three-digit field gets wrong most easily:
// 100 must be "100" and not "1 0", and 99 must stay right-aligned.
//
// Driven by poking the history arrays: playing 202 plies through the
// keyboard would take minutes and prove the same thing about uidec3.
func TestMoveNumberOverNinetyNine(t *testing.T) {
	u := bootShipping(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	const plies = 202 // -> moves 1..101, panel shows the last 13
	for i := range plies {
		u.Poke(ui.UIHFROM+uint16(i), 0x14) // e2
		u.Poke(ui.UIHTO+uint16(i), 0x24)   // e3
		u.Poke(ui.UIHFLAG+uint16(i), 0)
	}
	u.Poke(ui.UIHCNT, plies)
	u.Poke(u.Defs["HALFMOVE"], 0)        // keep uireps from scanning the poked hashes
	if err := u.Enter("?"); err != nil { // any key: forces a repaint
		t.Fatal(err)
	}
	s := u.Screen()
	var panel []string
	for row := 3; row <= 15; row++ {
		panel = append(panel, strings.TrimRight(s.Text(row)[21:], " "))
	}
	t.Logf("move panel with %d plies played (moves 89-101):\n%s", plies, strings.Join(panel, "\n"))
	want := []string{" 99 e2e3 e2e3", "100 e2e3 e2e3", "101 e2e3 e2e3"}
	got := panel[len(panel)-3:]
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("panel line %d = %q, want %q", i, got[i], want[i])
		}
	}
	// The whole panel must fit the 19 columns the design gives it (21-39),
	// and must not spill onto the board.
	for i, line := range panel {
		if len(line) > 19 {
			t.Errorf("panel line %d is %d columns: %q", i, len(line), line)
		}
	}
}

// ---------------------------------------------------------------------------
// Machine detection (m8main -> m8machine)
// ---------------------------------------------------------------------------

// probeMemory is an Apple IIe memory system with its AUXILIARY RAM sabotaged
// in one of the two ways real hardware sabotages it. Everything else — the
// language card, the display switches, the keyboard — is goapple2's ordinary
// IIe model, because the point is to change exactly one fact about the
// machine and see whether 8fish notices.
//
//	auxPlus  an Apple ][+ (or anything without the aux switches): writes to
//	         $C002-$C005 are not soft switches at all, they are nothing. So
//	         every "aux" access lands in MAIN — over the book at $2000 and
//	         the engine at $4000, which is the engine overwriting itself
//	         mid-search.
//	aux64    a 64K IIe: the switches work, but there is no RAM behind them.
//	         Writes vanish and reads float (modelled as $FF, which is what
//	         goapple2 returns for an unconnected read).
//	aux128   the real thing, as a control.
type auxKind int

const (
	aux128 auxKind = iota
	auxPlus
	aux64
)

type probeMemory struct {
	*iie.Memory
	kind auxKind
}

func (p *probeMemory) Read(addr uint16) byte {
	if p.kind == aux64 && p.RamRd && addr >= 0x0200 && addr < 0xC000 {
		return 0xFF // nothing is connected: the bus floats
	}
	return p.Memory.Read(addr)
}

func (p *probeMemory) Write(addr uint16, val byte) {
	switch {
	case p.kind == auxPlus && addr >= 0xC002 && addr <= 0xC005:
		return // not a soft switch on this machine
	case p.kind == aux64 && p.RamWrt && addr >= 0x0200 && addr < 0xC000:
		return // the write goes nowhere
	}
	p.Memory.Write(addr, val)
}

// TestRefusesMachinesWithoutAuxRAM boots the SHIPPING image on each of the
// three machines and requires 8fish to refuse the two it cannot run on.
//
// This is not a style point. The transposition table lives in aux
// $0200-$81FF: on a ][+ every TT write goes to MAIN, over the resident book
// and the engine image, so the engine overwrites itself mid-search; on a 64K
// IIe the TT is dead, and at ~10^7 probes a game roughly one false hit per
// game slips a 24-bit verify — a silent blunder rather than a crash. Neither
// is diagnosable by the person holding the disk, so the program says so and
// stops.
func TestRefusesMachinesWithoutAuxRAM(t *testing.T) {
	lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	halt, ok := lbl["m8halt"]
	if !ok {
		t.Fatal("asm/m8.lbl has no m8halt: the refusal loop was renamed")
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(root, "asm", name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	engine, boot, payload := read("engine.bin"), read("m8boot.bin"), read("m8.bin")

	for _, tc := range []struct {
		name    string
		kind    auxKind
		refuses bool
	}{
		{"128K IIe (the control)", aux128, false},
		{"Apple ][+ (the aux switches are not switches)", auxPlus, true},
		{"64K IIe (switches, but no RAM behind them)", aux64, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := &probeMemory{Memory: iie.New(), kind: tc.kind}
			copy(mem.Main[ui.EngineOrg:], engine)
			copy(mem.Main[ui.BootOrg:], boot)
			copy(mem.Main[ui.PayloadOrg:], payload)
			var cycles uint64
			mem.Memory.Clock = func() uint64 { return cycles }
			c := cpu.NewCPU(mem, func() { cycles++ }, cpu.VERSION_6502)
			c.Reset()
			c.SetPC(ui.BootOrg)

			// The refusal is a tight `jmp *`; the control reaches the
			// keyboard poll. Either way a few hundred thousand cycles is
			// more than enough (a cold boot paints in ~24,000).
			halted := false
			for i := 0; i < 300_000 && !halted; i++ {
				if err := c.Step(); err != nil {
					t.Fatalf("cpu: %v", err)
				}
				halted = c.PC() == halt
			}
			var s ui.Screen
			for row := range 24 {
				for col := range 40 {
					s.Raw[row][col] = mem.Main[ui.RowBase(row)+uint16(col)]
				}
			}
			var joined []string
			for row := range 24 {
				joined = append(joined, s.Text(row))
			}
			text := strings.Join(joined, "\n")
			t.Logf("%s:\n%s", tc.name, &s)

			if halted != tc.refuses {
				t.Errorf("halted at m8halt = %v, want %v", halted, tc.refuses)
			}
			if got := strings.Contains(text, "NEEDS A 128K APPLE IIE"); got != tc.refuses {
				t.Errorf("the refusal message is on screen = %v, want %v", got, tc.refuses)
			}
			if tc.refuses {
				// It must refuse BEFORE it can corrupt anything: the engine
				// image and the resident book area are untouched, and no
				// search ever started.
				if !bytes.Equal(mem.Main[ui.EngineOrg:ui.EngineOrg+len(engine)], engine) {
					t.Error("the engine image was modified before the machine was refused")
				}
			} else if !strings.Contains(text, "YOUR MOVE?") {
				t.Errorf("the control machine did not come up:\n%s", text)
			}
		})
	}
}

// TestColdStartHardening covers the three cheap things m8main does before it
// does anything else, each of which is invisible until the day it is not.
func TestColdStartHardening(t *testing.T) {
	lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}

	// 1. CLD. D is undefined after a 6502 reset; the ROM clears it in
	// practice, so this is hardening — but every ADC/SBC in the UI and the
	// engine is binary. Asserted statically, out of the shipping payload,
	// because the emulator's own reset already clears D and could therefore
	// never fail this.
	t.Run("clears decimal mode", func(t *testing.T) {
		payload, err := os.ReadFile(filepath.Join(root, "asm", "m8.bin"))
		if err != nil {
			t.Fatal(err)
		}
		off := int(lbl["m8main"]) - ui.LCOrg
		if off < 0 || off+8 > len(payload) {
			t.Fatalf("m8main at $%04X is not inside the payload", lbl["m8main"])
		}
		if !bytes.Contains(payload[off:off+8], []byte{0xD8}) {
			t.Errorf("no CLD ($D8) in the first 8 bytes of m8main: % 02X", payload[off:off+8])
		}
	})

	// 2. The keyboard strobe. A key held down while the disk loads is
	// latched before the UI ever polls, and would arrive as the first
	// character the player "typed".
	t.Run("clears the keyboard strobe", func(t *testing.T) {
		read := func(name string) []byte {
			b, err := os.ReadFile(filepath.Join(root, "asm", name))
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		mem := &probeMemory{Memory: iie.New(), kind: aux128}
		copy(mem.Main[ui.EngineOrg:], read("engine.bin"))
		copy(mem.Main[ui.BootOrg:], read("m8boot.bin"))
		copy(mem.Main[ui.PayloadOrg:], read("m8.bin"))
		mem.KeyPress('x') // held down while the disk was loading
		var cycles uint64
		mem.Memory.Clock = func() uint64 { return cycles }
		c := cpu.NewCPU(mem, func() { cycles++ }, cpu.VERSION_6502)
		c.Reset()
		c.SetPC(ui.BootOrg)
		for i := 0; i < 300_000; i++ {
			if err := c.Step(); err != nil {
				t.Fatalf("cpu: %v", err)
			}
		}
		var s ui.Screen
		for row := range 24 {
			for col := range 40 {
				s.Raw[row][col] = mem.Main[ui.RowBase(row)+uint16(col)]
			}
		}
		if got := strings.TrimSpace(s.Text(23)); got != "YOUR MOVE?" {
			t.Errorf("prompt row = %q, want an EMPTY move prompt: the key held down "+
				"during the boot was eaten as the first character typed", got)
		}
	})

	// 3. The Autostart power-up byte. $03F4 must NOT equal $03F3 EOR $A5, or
	// Ctrl-Reset takes the warm path — jmp ($03F2) into an Applesoft whose
	// entire zero page the engine has trampled. Invalidating it makes
	// Ctrl-Reset a cold boot, which restarts 8fish off the disk.
	t.Run("invalidates the Autostart power-up byte", func(t *testing.T) {
		u, err := ui.BootShipping(root, nil)
		if err != nil {
			t.Fatal(err)
		}
		lo, pwr := u.Peek(0x03F3), u.Peek(0x03F4)
		t.Logf("$03F3 = $%02X, $03F4 = $%02X (warm start would need $%02X)", lo, pwr, lo^0xA5)
		if pwr == lo^0xA5 {
			t.Errorf("the power-up byte is VALID ($03F4 = $%02X = $03F3 EOR $A5): "+
				"Ctrl-Reset would warm-start into a trashed Applesoft", pwr)
		}
	})
}
