package ui_test

// bank2_test.go gates the UIDATA2 relocation (2026-08-08): the UI's
// read-only strings and cold tables moved out of the $E000-$F6FF code budget
// into LANGUAGE CARD BANK 2 at $D700, above the opening book's name table.
// The copier lifts them there at boot, and a small set of reader routines
// (uiputs, ui80puts, uisetmsg, uititle's who-field, uiscore's mate label,
// uistartpos, uilimits) reach them through a bank-2 window on the same
// restore-bank-1 discipline uibookname established.
//
// Two things can rot silently and are gated here:
//
//  1. RESIDENCY. If the copier's bank-2 copy is wrong (or missing), every
//     string on the screen is garbage read from unwritten bank-2 RAM — but a
//     test that only checks one row could miss a partial copy. So the whole
//     segment is compared byte-for-byte against the linked file.
//
//  2. RESTORATION. A reader that returns with bank 2 still selected leaves
//     $D000-$DFFF as name-table-and-strings instead of LCCODE and the
//     artwork: the next transposition-table probe jsrs into a message
//     string. TestBookNameRestoresBank1 covers uibookname; the boot
//     assertion here covers every reader the boot paint exercises, and the
//     trampoline covers ui80puts, which only mixed mode reaches.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/8fish/internal/ui"
)

// restingBank1 asserts the Language Card is in the state everything assumes
// between routines: bank 1, RAM readable and writable.
func restingBank1(t *testing.T, u *ui.Machine, when string) {
	t.Helper()
	if u.M.Mem.LCBank2 || !u.M.Mem.LCReadRAM || !u.M.Mem.LCWriteEnabled {
		t.Fatalf("%s: Language Card not in the resting state: bank2=%v readRAM=%v writeRAM=%v",
			when, u.M.Mem.LCBank2, u.M.Mem.LCReadRAM, u.M.Mem.LCWriteEnabled)
	}
}

// TestBank2DataResident proves the UIDATA2 segment reads back from Language
// Card bank 2 byte-identical to what the linker put in the payload file, and
// that a boot — which has already painted the whole screen through the
// banked readers — ends on bank 1.
func TestBank2DataResident(t *testing.T) {
	u := boot(t)
	restingBank1(t, u, "after boot (uiputs/uititle/uisetmsg/uistartpos all ran)")

	bin, err := os.ReadFile(filepath.Join(root, "asm", "m8t.bin"))
	if err != nil {
		t.Fatal(err)
	}
	codeSize := int(u.Lbl["__UICODE_SIZE__"])
	dataSize := int(u.Lbl["__UIDATA2_SIZE__"])
	run := int(u.Lbl["__UIDATA2_RUN__"])
	// SPLASH is a third payload segment (the boot title card, delivered to
	// $F800; see asm/m8.s m8splash). It sits after UIDATA2, so the payload is
	// UICODE + UIDATA2 + SPLASH, and UIDATA2 is page-aligned so the copier can
	// find SPLASH where its UIDATA2 copy left off.
	splashSize := int(u.Lbl["__SPLASH_SIZE__"])
	if codeSize == 0 || dataSize == 0 || run == 0 || splashSize == 0 {
		t.Fatalf("missing linker symbols in asm/m8t.lbl: UICODE %d, UIDATA2 %d at $%04X, SPLASH %d",
			codeSize, dataSize, run, splashSize)
	}
	if codeSize+dataSize+splashSize != len(bin) {
		t.Fatalf("asm/m8t.bin is %d B but UICODE+UIDATA2+SPLASH = %d B: the payload file "+
			"no longer is the three segments back to back", len(bin), codeSize+dataSize+splashSize)
	}
	want := bin[codeSize : codeSize+dataSize]
	bad := 0
	for i, w := range want {
		if got := u.M.Mem.MainD000Bank2[run-0xD000+i]; got != w {
			if bad < 8 {
				t.Errorf("bank 2 $%04X = $%02X, want $%02X", run+i, got, w)
			}
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("UIDATA2 does not read back from Language Card bank 2: %d of %d bytes "+
			"differ — the copier's $C083 lift is wrong or missing", bad, len(want))
	}
	t.Logf("UIDATA2: %d B resident at bank-2 $%04X-$%04X, byte-identical to the file",
		dataSize, run, run+dataSize-1)
}

// TestUI80PutsRestoresBank1 drives the one banked reader a text-screen boot
// never reaches: ui80puts, the mixed-mode window's string writer. It must
// copy a bank-2 string into UI80BUF and come back on bank 1 — with bank 2
// left selected, the next board repaint would blit opening names as tiles.
func TestUI80PutsRestoresBank1(t *testing.T) {
	u := boot(t)
	fn, ok := u.Lbl["ui80puts"]
	if !ok {
		t.Fatal("asm/m8t.lbl has no ui80puts label")
	}
	str, ok := u.Lbl["s_help1"]
	if !ok {
		t.Fatal("asm/m8t.lbl has no s_help1 label")
	}

	// The expected bytes, from the payload file rather than from memory, so
	// this cannot pass by comparing garbage with garbage.
	bin, err := os.ReadFile(filepath.Join(root, "asm", "m8t.bin"))
	if err != nil {
		t.Fatal(err)
	}
	codeSize := int(u.Lbl["__UICODE_SIZE__"])
	run := int(u.Lbl["__UIDATA2_RUN__"])
	want := bin[codeSize+int(str)-run:]
	if i := indexByte(want, 0); i >= 0 {
		want = want[:i]
	}

	const (
		tramp   = 0x03A0 // free driver-scratch RAM (defs.inc)
		ui80buf = 0xFF00 // asm/ui.s UI80BUF
	)
	prog := []byte{
		0xA0, 0x00, // LDY #0 (column)
		0xA9, byte(str), // LDA #<s_help1
		0xA2, byte(str >> 8), // LDX #>s_help1
		0x20, byte(fn), byte(fn >> 8), // JSR ui80puts
		0xA9, 0x00, // LDA #0
		0x8D, 0xFF, 0xBF, // STA $BFFF (harness exit trap)
	}
	for i, v := range prog {
		u.Poke(tramp+uint16(i), v)
	}
	// Scrub the staging buffer so a no-op call cannot pass.
	for i := range 80 {
		u.Poke(ui80buf+uint16(i), 0)
	}
	u.M.CPU.SetPC(tramp)
	exited, _, err := u.M.Run(1 << 20)
	if err != nil || !exited {
		t.Fatalf("running ui80puts: exited=%v err=%v", exited, err)
	}
	restingBank1(t, u, "after ui80puts")
	for i, w := range want {
		if got := u.Peek(ui80buf + uint16(i)); got != w {
			t.Fatalf("UI80BUF[%d] = $%02X, want $%02X: ui80puts did not read the "+
				"bank-2 string", i, got, w)
		}
	}
	t.Logf("ui80puts: %d bank-2 bytes staged, bank 1 restored", len(want))
}

func indexByte(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}
