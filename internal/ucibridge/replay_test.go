package ucibridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

// TestReplayMatchPrefix replays the losing game's move prefix through
// the bridge (TT carried across moves, like the real match) and, at
// each engine turn, also searches the same position cold. Divergence
// implicates the carryover.
func TestReplayMatchPrefix(t *testing.T) {
	asmbuild.BuildT(t, "../..")
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("..", "..", "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	// The match game, from the engine's (White's) side. Trailing replies
	// are trimmed to the prefix that stays legal against the CURRENT
	// engine: since this game was recorded (M3, 2026-07-18) the engine's
	// play improved, and by move 8 it now plays Qc5 (bridge and cold search
	// agree — the parity point still holds), which blocks the old historical
	// reply d6a3. The bridge-vs-cold parity assertion runs on every White
	// move of the surviving prefix, which is the test's actual purpose.
	replies := []string{"d7d5", "b8c6", "g8f6", "e7e6", "f8b4", "b4c3", "d8d6"}

	b := &Bridge{Bin: bin, Defs: defs, FixedBudgetMs: 30000}
	// Drive move by move via bridge internals so cold searches can be
	// interleaved.
	b.pos, _ = refchess.ParseFEN(refchess.StartFEN)
	for i := 0; i <= len(replies); i++ {
		fenBefore := b.pos.FEN()
		mv, err := b.think(nil)
		if err != nil {
			t.Fatalf("ply %d: %v", i, err)
		}
		// Cold search of the same position.
		pos, err := chesstest.ParseFEN(fenBefore)
		if err != nil {
			t.Fatal(err)
		}
		cold, err := chesstest.SearchBudget(bin, defs, pos, 24, 30_600_000, 500_000_000)
		if err != nil {
			t.Fatal(err)
		}
		// docs/results.md 2026-07-18 (M3) first ran this replay to confirm
		// bridge-vs-cold-engine parity (no TT-carryover corruption) for a
		// real losing game; assert it so a future regression fails the
		// build instead of only appearing in -v log output.
		mark := ""
		if cold.Move != mv {
			mark = "  <-- DIVERGES"
		}
		t.Logf("move %2d: bridge=%s cold=%s (cold score %d)%s", i+1, mv, cold.Move, cold.Score, mark)
		if cold.Move != mv {
			t.Errorf("move %2d: bridge move %s diverges from cold search %s (cold score %d, fen %q)",
				i+1, mv, cold.Move, cold.Score, fenBefore)
		}
		if i < len(replies) {
			rmv, err := refchess.ParseMove(replies[i])
			if err != nil {
				t.Fatal(err)
			}
			if err := b.pos.Make(rmv); err != nil {
				t.Fatalf("reply %s: %v", replies[i], err)
			}
		}
	}
}
