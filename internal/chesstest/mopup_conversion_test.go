package chesstest

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/refchess"
)

// searchFT2 runs the asm engine over pos under FEATURES 0x1f + the given
// FEATURES2 byte, budget-mode (cycles) capped at maxDepth, and returns the
// UCI move ("" if none) and score. Used to drive both sides of a conversion
// game at a fixed feature mask.
func searchFT2(t *testing.T, bin []byte, pos *Position, ft2 byte, maxDepth byte, budget uint64) (string, int16) {
	t.Helper()
	var cout bytes.Buffer
	m, err := NewMachine(bin, defs, pos, 0, &cout)
	if err != nil {
		t.Fatal(err)
	}
	SetBudget(m, defs, budget, maxDepth)
	SetFeatures2(m, defs, ft2)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	exited, code, err := m.Run(60_000_000_000)
	if err != nil || !exited {
		t.Fatalf("search: exited=%v err=%v (fen state)", exited, err)
	}
	sc := int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
	if code == 2 {
		return "", sc // no legal move
	}
	return strings.TrimSpace(cout.String()), sc
}

// playConvert plays fen out with BOTH sides driven by the asm engine at the
// given FEATURES2 mask (winner is white by construction of the suite),
// refereed by refchess for legality and adjudication. Returns the outcome
// ("mate","50-move","threefold","material","stalemate","cap") and the full
// move count reached.
func playConvert(t *testing.T, bin []byte, fen string, ft2 byte, maxDepth byte, budget uint64, maxPlies int) (string, int) {
	t.Helper()
	ref, err := refchess.ParseFEN(fen)
	if err != nil {
		t.Fatalf("refchess fen %q: %v", fen, err)
	}
	seen := map[uint64]int{}
	for ply := 0; ply < maxPlies; ply++ {
		if ref.HalfmoveClock() >= 100 {
			return "50-move", ply / 2
		}
		if ref.InsufficientMaterial() {
			return "material", ply / 2
		}
		seen[ref.ZobristKey()]++
		if seen[ref.ZobristKey()] >= 3 {
			return "threefold", ply / 2
		}
		legal := ref.LegalMoves()
		if len(legal) == 0 {
			if ref.InCheck() {
				return "mate", ply / 2
			}
			return "stalemate", ply / 2
		}
		pos, err := ParseFEN(ref.FEN())
		if err != nil {
			t.Fatal(err)
		}
		mvUCI, _ := searchFT2(t, bin, pos, ft2, maxDepth, budget)
		if mvUCI == "" {
			t.Fatalf("ply %d: engine no move but %d legal (fen %q)", ply, len(legal), ref.FEN())
		}
		mv, err := refchess.ParseMove(mvUCI)
		if err != nil {
			t.Fatalf("ply %d: bad engine move %q: %v", ply, mvUCI, err)
		}
		if err := ref.Make(mv); err != nil {
			t.Fatalf("ply %d: illegal engine move %q (fen %q): %v", ply, mvUCI, ref.FEN(), err)
		}
	}
	return "cap", maxPlies / 2
}

// TestMopupConversion is the asm-side conversion gate: won endgames that the
// thin PSQT eval SHUFFLES to a draw (50-move / threefold) with FT2_MOPUP OFF
// must be MATED with FT2_MOPUP ON — the asm reproduction of the mirror's
// STEP-1 (off) vs STEP-3 (on) conversion result. Both sides run the asm
// engine at the same mask; refchess adjudicates.
func TestMopupConversion(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: full endgame playouts")
	}
	bin := loadEngine(t)

	// Canonical won endings that the bare eval cannot finish. KRK is the
	// classic shuffler (the mate is far past any reachable depth without a
	// king-drive term); KQK too from awkward placements.
	cases := []struct{ name, fen string }{
		{"KRK-a", "7k/8/8/8/8/8/8/R3K3 w - - 0 1"},
		{"KRK-b", "4k3/8/8/8/8/8/8/R3K3 w - - 0 1"},
		{"KQK-a", "8/8/4k3/8/8/8/8/Q2K4 w - - 0 1"},
	}

	const (
		maxDepth = 12
		budget   = 8_000_000 // ~8M cycles/move (~8ms); ID reaches useful depth
		maxPlies = 200       // 100 full moves: covers the 50-move window
	)

	var b strings.Builder
	converted := 0
	for _, c := range cases {
		offRes, offMoves := playConvert(t, bin, c.fen, 0x00, maxDepth, budget, maxPlies)
		onRes, onMoves := playConvert(t, bin, c.fen, ft2Mopup, maxDepth, budget, maxPlies)
		fmt.Fprintf(&b, "  %-6s OFF: %-10s (%d mv)   ON: %-10s (%d mv)\n",
			c.name, offRes, offMoves, onRes, onMoves)
		if onRes != "mate" {
			t.Errorf("%s: FT2_MOPUP ON failed to convert: got %q in %d moves (want mate)",
				c.name, onRes, onMoves)
		}
		if offRes == "mate" {
			t.Logf("%s: NOTE FT2_MOPUP OFF also mated in %d moves (no shuffling to contrast here)",
				c.name, offMoves)
		} else {
			converted++
		}
	}
	t.Logf("mop-up conversion (asm, both sides same mask):\n%s", b.String())
	t.Logf("positions where ON mates but OFF draws: %d/%d", converted, len(cases))
	if converted == 0 {
		t.Errorf("no position showed the OFF-draws / ON-mates contrast; the term's value is unproven")
	}
}
