package chesstest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
)

// buildCkVerify assembles the CKVERIFY engine variant: the give-check
// cross-check is assembly-time optional (space round 1, 2026-07-25 — it
// cost the shipped image 24 bytes of a nearly-full MAIN), so the assertion
// exists only in this build. Same pattern as PTNOCACHE (ptcache_test.go).
func buildCkVerify(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("..", "..")
	if err := asmbuild.BuildVariant(root, "engine_ckverify", "CKVERIFY"); err != nil {
		if err == asmbuild.ErrCA65NotInstalled {
			t.Skip("ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine_ckverify.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// TestGiveCheckVerify searches varied positions with FT_CKVERIFY set in the
// CKVERIFY variant build: every node cross-checks make's propagated in-check
// flag against a full attacked() scan and exits 101 on any mismatch.
func TestGiveCheckVerify(t *testing.T) {
	bin := buildCkVerify(t)
	fens := []string{
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 b - - 3 9",
		"rnbqkbnr/ppppp1pp/8/8/4Pp2/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 3",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		"k7/2P5/1K6/8/8/8/8/8 w - - 0 1",
	}
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x0F|0x80) // all features + FT_CKVERIFY
		SetBudget(m, defs, 0, 5)
		exited, code, err := m.Run(100_000_000_000)
		if err != nil || !exited {
			t.Fatalf("%.20s: exited=%v err=%v", fen, exited, err)
		}
		if code == 101 {
			t.Fatalf("%.30s: give-check mismatch (exit 101)", fen)
		}
		if code != 0 && code != 2 {
			t.Fatalf("%.30s: exit code %d", fen, code)
		}
	}
}
