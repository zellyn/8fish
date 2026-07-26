package chesstest

import (
	"fmt"
	"testing"
)

// TestTablePacking guards the TABLES layout established by space
// optimization round 1 (2026-07-25). TABLES is larger than all the
// engine's code and it had never been packed: the `.align` directives
// left 977 bytes of pure fill scattered through it. cmd/gentables now
// emits the tables in groups whose sizes are each a multiple of 256, with
// every unaligned small table packed into one run at the tail, so the
// only remaining fill in the whole segment is the deliberate slack after
// RATTACK (shared with mopfin/mopcd).
//
// Two invariants matter and both are cheap to break by accident:
//
//  1. ALIGNMENT. A table read through a pointer or a self-modified
//     operand low byte (PSQTBASE, ZKEYS, SQR*/ISQ*, RATTACK, TIERTAB)
//     must be page-aligned or the address arithmetic silently reads the
//     wrong bytes.
//
//  2. PAGE CROSSING = CYCLES. `lda abs,x`/`abs,y` costs +1 cycle when
//     base+index crosses a page. The engine's cycle budget was tuned
//     over four deep-optimization rounds against the layout's crossing
//     behaviour, so every table must keep it: the ones indexed 0..255 or
//     0..127 stay at a low byte that cannot cross, and WBLOCKM (indexed
//     0..255 from offset $80, so it always crossed) keeps offset $80.
func TestTablePacking(t *testing.T) {
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	addr := func(name string) uint16 {
		a, ok := labels[name]
		if !ok {
			t.Fatalf("label %s missing", name)
		}
		return a
	}

	// Page-aligned by construction (pointer / SMC-low-byte addressing).
	for _, n := range []string{
		"TIERTAB", "RATTACK", "ATTACKTAB", "DELTATAB", "SLOTTAB",
		"WPASSB", "BBLOCKM", "BPASSB", "DBLTAB", "SPREADTAB",
		"SHL3TAB", "SHR5TAB", "TTHITAB", "PHASEWX",
		"SQRLO", "SQRHI", "ISQLO", "ISQHI", "PSQTBASE", "ZKEYS",
		"RANKBIT", "CASTLEMASK",
	} {
		if a := addr(n); a&0xFF != 0 {
			t.Errorf("%s at $%04X is not page-aligned", n, a)
		}
	}

	// WBLOCKM is indexed 0..255 from offset $80: it crossed a page in the
	// pre-packing layout and must keep doing so, or eval's pawnterm gets
	// cycle-cheaper than every measurement on record.
	if a := addr("WBLOCKM"); a&0xFF != 0x80 {
		t.Errorf("WBLOCKM at $%04X: low byte must stay $80 (page-crossing parity)", a)
	}

	// No-crossing tables: base low byte + highest index used must stay
	// inside the page.
	noCross := []struct {
		name   string
		maxIdx uint16
	}{
		{"TYPEATK2", 15},    // piece & $0F
		{"RANKBIT", 127},    // 0x88 square
		{"FILEBIT", 127},    // 0x88 square
		{"CASTLEMASK", 127}, // 0x88 square
		{"KNIGHTOFF", 7}, {"KINGOFF", 7}, {"DIAGOFF", 3}, {"ORTHOOFF", 3},
		{"TYPEPAGE0", 7}, {"TYPEPAGE1", 7}, {"PHASEVAL", 7},
		{"ZKHI0", 15}, {"DIRTYTAB", 15}, {"TYPEPG0X", 15}, {"TYPEPG1X", 15},
		{"PHASEV16", 15}, {"STMKEY", 3},
		{"CASTKEYS", 63}, // (rights nibble)<<2, +3 for the key byte
		{"EPKEYS", 31},   // file<<2, +3
		{"MOPMATLO", 7}, {"MOPMATHI", 7},
	}
	for _, c := range noCross {
		if lo := addr(c.name) & 0xFF; lo+c.maxIdx > 0xFF {
			t.Errorf("%s at $%04X: base+%d crosses a page (+1 cycle per crossing read)",
				c.name, addr(c.name), c.maxIdx)
		}
	}

	// The group-F tail must stay one gap-free run: each table starts
	// exactly where the previous one ends. A gap here means an `.align`
	// crept back in and the segment grew.
	tail := []struct {
		name string
		size uint16
	}{
		{"CASTLEMASK", 128}, {"TYPEATK2", 16},
		{"KNIGHTOFF", 8}, {"KINGOFF", 8}, {"DIAGOFF", 4}, {"ORTHOOFF", 4},
		{"TYPEPAGE0", 8}, {"TYPEPAGE1", 8}, {"PHASEVAL", 8},
		{"ZKHI0", 16}, {"DIRTYTAB", 16}, {"TYPEPG0X", 16}, {"TYPEPG1X", 16},
		{"PHASEV16", 16}, {"STMKEY", 4}, {"hashstm", 25},
		{"CASTKEYS", 64}, {"EPKEYS", 32}, {"MOPMATLO", 8}, {"MOPMATHI", 8},
	}
	for i := 0; i+1 < len(tail); i++ {
		want := addr(tail[i].name) + tail[i].size
		if got := addr(tail[i+1].name); got != want {
			t.Errorf("group-F gap: %s ends at $%04X but %s starts at $%04X (%d bytes of fill)",
				tail[i].name, want, tail[i+1].name, got, int(got)-int(want))
		}
	}

	// The page-aligned groups must be gap-free too: each group's first
	// table sits exactly where the previous group ended.
	groups := []struct {
		last  string // last table of the previous group
		size  uint16 // ... and its size
		first string // first table of this group
	}{
		{"PHASEWX", 256, "SQRLO"},     // group A -> B
		{"ISQHI", 512, "RANKBIT"},     // group B -> C
		{"FILEBIT", 128, "PSQTBASE"},  // group C -> D
		{"ZKEYS", 6144, "CASTLEMASK"}, // group E -> F
	}
	for _, g := range groups {
		want := addr(g.last) + g.size
		if got := addr(g.first); got != want {
			t.Errorf("group boundary %s..%s: %d bytes of alignment fill",
				g.last, g.first, int(got)-int(want))
		}
	}

	// Ceiling guard: the image must stay below the harness trap page.
	top := addr("__LCCODE_LOAD__") + 0x41
	if top > 0xBFF0 {
		t.Errorf("image top $%04X is past the $BFF0 trap ceiling", top)
	}
	fmt.Printf("TABLES packed: image top $%04X, %d bytes of MAIN headroom below $BFF0\n",
		top, 0xBFF0-int(top))
}
