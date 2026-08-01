package harness

import "testing"

// A 6502 `sta (zp),Y` performs a hardware-accurate DUMMY READ of the target
// address (cycle T4) before the write (cycle T5) — go6502's zpiy6w models it.
// That read is a real bus cycle, so it obeys RAMRD, not RAMWRT. Code running
// the aux-write discipline of asm/tt.s (RAMWRT on, RAMRD off) therefore emits
// a MAIN-bank read of every address it writes to AUX.
//
// On real hardware that is harmless: $BFF0-$BFFF is ordinary RAM. In the
// harness it was not, because the read traps live there with side effects
// (InAddr pops a byte; InStatusAddr sets WaitingForInput and makes Run
// return early). That, and nothing else, is why a transposition table at
// TTBASE = $4000 — whose entry 4094 covers aux $BFF0-$BFF7 — made searches
// stop terminating, while a table topping out at $BFEF was fine.
//
// TrapMemory now gates the read traps on !RamWrt as well as !RamRd. These
// tests pin that, and the control case, so it cannot silently regress.

// auxStoreProgram builds a program that points $06/$07 at hi/lo, turns RAMWRT
// on, stores $AA through `sta ($06),Y`, turns RAMWRT off, then exits 0.
// RAMRD is never touched, so it stays off throughout — exactly ttstore.
func auxStoreProgram(lo, hi, y byte) []byte {
	return []byte{
		0xA9, lo, 0x85, 0x06, // lda #lo   ; sta $06
		0xA9, hi, 0x85, 0x07, // lda #hi   ; sta $07
		0x8D, 0x05, 0xC0, // sta $C005 ; RAMWRT on (RAMRD stays OFF)
		0xA0, y, // ldy #y
		0xA9, 0xAA, // lda #$AA
		0x91, 0x06, // sta ($06),y  <-- T4 dummy read, T5 write
		0x8D, 0x04, 0xC0, // sta $C004 ; RAMWRT off
		0xA9, 0x00, // lda #$00
		0x8D, 0xFF, 0xBF, // sta $BFFF ; exit 0
	}
}

func newAuxMachine(t *testing.T, prog []byte) *Machine {
	t.Helper()
	m, err := New(Config{
		Bin:          prog,
		Org:          0x4000,
		Entry:        0x4000,
		CoutAddr:     0xBFF0,
		ExitAddr:     0xBFFF,
		InAddr:       0xBFF1,
		InStatusAddr: 0xBFF2,
		ClockAddr:    0xBFF4,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// runAuxStore runs auxStoreProgram at aux $hi$lo+y and reports what happened.
func runAuxStore(t *testing.T, lo, hi, y byte, input string) (m *Machine, exited bool, code byte) {
	t.Helper()
	m = newAuxMachine(t, auxStoreProgram(lo, hi, y))
	if input != "" {
		m.SendInput([]byte(input))
	}
	exited, code, err := m.Run(10000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return m, exited, code
}

// TestAuxStoreOverTrapsCompletes covers every address the top two TT entries
// of a $4000-$BFFF table touch ($BFF0-$BFFF). Each must behave as plain aux
// RAM: the run completes, the byte lands in AUX, main is untouched, and no
// trap side effect fires.
func TestAuxStoreOverTraps(t *testing.T) {
	for y := byte(0); y < 16; y++ {
		addr := 0xBFF0 + uint16(y)
		m, exited, code := runAuxStore(t, 0xF0, 0xBF, y, "go\n")

		if m.WaitingForInput() {
			t.Errorf("$%04X: aux store spuriously set WaitingForInput", addr)
		}
		if !exited || code != 0 {
			t.Errorf("$%04X: exited=%v code=%d, want exited=true code=0", addr, exited, code)
		}
		if got := m.Mem.Aux[addr]; got != 0xAA {
			t.Errorf("$%04X: aux = $%02X, want $AA", addr, got)
		}
		if got := string(m.Mem.Input); got != "go\n" {
			t.Errorf("$%04X: pending input = %q, want %q — an aux store consumed a byte", addr, got, "go\n")
		}
	}
}

// TestAuxStoreBelowTrapsIsClean is the control: the same instruction aimed at
// the top slot of a table ending at $BFEF, which worked even before the fix.
func TestAuxStoreBelowTrapsIsClean(t *testing.T) {
	m, exited, code := runAuxStore(t, 0xE8, 0xBF, 2, "") // -> $BFEA
	if !exited || code != 0 {
		t.Fatalf("aux store to $BFEA: exited=%v code=%d, want exited=true code=0", exited, code)
	}
	if got := m.Mem.Aux[0xBFEA]; got != 0xAA {
		t.Fatalf("aux $BFEA = $%02X, want $AA", got)
	}
}

// TestMainBankReadTrapsStillFire is the other half of the gate: with normal
// (all-main) banking the read traps must behave exactly as before. Without
// this, "fix the dummy read" could degenerate into "disable the traps".
func TestMainBankReadTrapsStillFire(t *testing.T) {
	// lda $BFF2 (status, empty buffer -> WaitingForInput); then exit.
	prog := []byte{
		0xAD, 0xF2, 0xBF, // lda $BFF2
		0xA9, 0x00, // lda #$00
		0x8D, 0xFF, 0xBF, // sta $BFFF
	}
	m := newAuxMachine(t, prog)
	exited, _, err := m.Run(10000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !m.WaitingForInput() {
		t.Fatalf("main-bank read of $BFF2 did not set WaitingForInput; the trap is over-gated")
	}
	if exited {
		t.Fatalf("Run exited despite the input stall")
	}

	// And the input trap must still pop bytes on a main-bank read.
	m2 := newAuxMachine(t, []byte{
		0xAD, 0xF1, 0xBF, // lda $BFF1
		0x8D, 0xFF, 0xBF, // sta $BFFF  (exit code = the byte read)
	})
	m2.SendInput([]byte{0x07})
	exited, code, err := m2.Run(10000)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !exited || code != 0x07 {
		t.Fatalf("main-bank read of $BFF1: exited=%v code=%d, want exited=true code=7", exited, code)
	}
}
