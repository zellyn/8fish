package ucibridge

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// ttBase is a hand-copied duplicate of the asm TTBASE constant: PonderMove
// reaches into the aux bank at that offset to read the engine's own predicted
// reply out of the transposition table. Nothing bound the two together, so
// relocating TTBASE (as a double-hi-res build wants to — see docs/ui-design.md)
// would leave PonderMove reading whatever happens to sit at $0200 in aux.
//
// That failure is SILENT and slow rather than loud: a mismatched key just
// looks like a TT miss, so ponder quietly falls back to the source-(b) probe
// search on every move and the only symptom is lost time. Bind them.
func TestTTBaseMatchesASM(t *testing.T) {
	src, err := os.ReadFile("../../asm/defs.inc")
	if err != nil {
		t.Fatalf("reading asm/defs.inc: %v", err)
	}
	m := regexp.MustCompile(`(?m)^TTBASE\s*=\s*\$([0-9A-Fa-f]+)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no `TTBASE = $xxxx` line in asm/defs.inc")
	}
	want, err := strconv.ParseInt(string(m[1]), 16, 32)
	if err != nil {
		t.Fatalf("parsing TTBASE: %v", err)
	}
	if int64(ttBase) != want {
		t.Errorf("ucibridge.ttBase = $%04X but asm/defs.inc TTBASE = $%04X; "+
			"PonderMove would read the wrong aux addresses (a silent ponder "+
			"regression, not a crash)", ttBase, want)
	}
}

// TestTTFitsInAux pins the other half of the relocation constraint: the table
// is 4096 8-byte entries indexed by a 12-bit key, so it occupies exactly
// 32,768 B starting at TTBASE and must end at or before the top of aux RAM
// ($BFFF). The asm TTADDR macro forms the high byte with `clc / adc #>TTBASE`,
// which silently wraps past $FFFF, so a base that does not fit corrupts page
// zero rather than failing loudly.
func TestTTFitsInAux(t *testing.T) {
	const entries, entrySize = 4096, 8
	end := ttBase + entries*entrySize // exclusive
	if end > 0xC000 {
		t.Errorf("TT at $%04X spans %d B and ends at $%04X, past the top of aux RAM ($BFFF)",
			ttBase, entries*entrySize, end-1)
	}
	if ttBase%entrySize != 0 {
		t.Errorf("TTBASE $%04X is not %d-byte aligned; entries would cross pages "+
			"and the unrolled (TTPTR),y stores in asm/tt.s assume they never do",
			ttBase, entrySize)
	}
	t.Logf("TT occupies aux $%04X-$%04X (%d B); aux above it: %d B",
		ttBase, end-1, entries*entrySize, 0xC000-end)
}
