package ui_test

// shipconfig_test.go: DIAGNOSTIC — does the on-device build run the
// configuration every Elo number was measured under?
//
// Every gate in this package compares the UI against a reference built by
// chesstest.NewMachine, whose FEATURES default is 0x1F. But the GAMEPLAY
// configuration — the one internal/ucibridge uses for SPRT, the Sargon
// gauntlet, and every Elo figure in docs/results.md — is
// FT_CKEXT|0x1F = 0x5F. If asm/m8.s ships 0x1F, the parity gates still pass
// (both sides are 0x1F) while the disk plays weaker chess than anything
// that was ever measured.

import (
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/ui"
)

func TestShippedFeatureConfig(t *testing.T) {
	defs, err := chesstest.ParseDefs(filepath.Join(root, "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	u, err := ui.BootShipping(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Spin(4_000_000); err != nil {
		t.Fatal(err)
	}
	gotF := u.Peek(defs["FEATURES"])
	gotF2 := u.Peek(defs["FEATURES2"])

	// ucibridge.runEngine's gameplay config.
	wantF := byte(defs["FT_CKEXT"]) | 0x1F
	// FEATURES2: the bridge uses FT2_GENDEFER; on device FT2_SOFTCLK is
	// additionally REQUIRED (there is no hardware clock) — that difference
	// is deliberate and documented.
	wantF2 := byte(defs["FT2_GENDEFER"]) | byte(defs["FT2_SOFTCLK"])

	t.Logf("on-device FEATURES  = $%02X   gameplay (ucibridge) = $%02X", gotF, wantF)
	t.Logf("on-device FEATURES2 = $%02X   gameplay + SOFTCLK   = $%02X", gotF2, wantF2)
	if gotF != wantF {
		t.Errorf("FEATURES on device is $%02X but the gameplay config is $%02X "+
			"(missing $%02X). Every Elo number in docs/results.md was measured "+
			"with those bits set.", gotF, wantF, gotF^wantF)
	}
	if gotF2 != wantF2 {
		t.Errorf("FEATURES2 on device is $%02X, want $%02X", gotF2, wantF2)
	}
}

// TestCkextActuallyChangesTheTree is the mutation check for the above: it
// proves FT_CKEXT is not a no-op, so shipping without it is a real
// difference, not a cosmetic one.
func TestCkextActuallyChangesTheTree(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: eight depth-5 searches")
	}
	bin, defs := engineBin(t)
	fens := []string{
		"r1bqkb1r/pppp1ppp/2n2n2/4p3/2B1P3/5Q2/PPPP1PPP/RNB1K1NR w KQkq - 4 4",
		"2rq1rk1/pp1bbppp/2np1n2/4p3/2P1P3/2NBBN2/PPQ2PPP/3R1RK1 w - - 0 12",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	}
	diff := 0
	for _, fen := range fens {
		a, ac := refSearchFeat(t, bin, defs, fen, 5, 0x1F)
		b, bc := refSearchFeat(t, bin, defs, fen, 5, byte(defs["FT_CKEXT"])|0x1F)
		if a != b || ac != bc {
			diff++
		}
		t.Logf("%s\n  FEATURES $1F -> %q %d cycles\n  FEATURES $5F -> %q %d cycles (%+.1f%%)",
			fen, a, ac, b, bc, 100*(float64(bc)/float64(ac)-1))
	}
	if diff == 0 {
		t.Errorf("FT_CKEXT changed nothing over %d positions: either the bit is "+
			"dead or this diagnostic is not exercising it", len(fens))
	}
}

func refSearchFeat(t *testing.T, bin []byte, defs chesstest.Defs, fen string, depth, feat byte) (string, uint64) {
	t.Helper()
	pos, err := chesstest.ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	var cout stringsBuilder
	m, err := chesstest.NewMachine(bin, defs, pos, 0, &cout)
	if err != nil {
		t.Fatal(err)
	}
	chesstest.SetFeatures(m, defs, feat)
	chesstest.SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
	chesstest.SetBudget(m, defs, 0, depth)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	exited, code, err := m.Run(1 << 34)
	if err != nil || !exited || code != 0 {
		t.Fatalf("search: exited=%v code=%d err=%v", exited, code, err)
	}
	return cout.String(), m.Cycles
}

type stringsBuilder struct{ b []byte }

func (s *stringsBuilder) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *stringsBuilder) String() string              { return string(s.b) }
