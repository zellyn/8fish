package chesstest

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/8fish/harness"
)

// TestBankedBuild runs the multi-segment banked image: loader installs a
// Language Card code segment and an aux data segment, and the LC routine
// reads the aux data back through RAMRD.
func TestBankedBuild(t *testing.T) {
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "banktest.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var cout bytes.Buffer
	m, err := harness.New(harness.Config{
		Bin:      bin,
		Org:      0x2000,
		Entry:    0x2000,
		CoutAddr: 0xBFF0,
		ExitAddr: 0xBFFF,
		Cout:     &cout,
	})
	if err != nil {
		t.Fatal(err)
	}
	exited, code, err := m.Run(1_000_000)
	if err != nil || !exited || code != 0 {
		t.Fatalf("exited=%v code=%d err=%v cout=%q", exited, code, err, cout.String())
	}
	if got := cout.String(); got != "BANKS OK\n" {
		t.Errorf("cout = %q, want BANKS OK", got)
	}
	// The marker must be in aux, not the main-bank shadow.
	if m.Mem.Aux[0x6000] != 0xA5 || m.Mem.Main[0x6000] != 0 {
		t.Errorf("aux[6000]=%02x main[6000]=%02x, want A5/00",
			m.Mem.Aux[0x6000], m.Mem.Main[0x6000])
	}
}
