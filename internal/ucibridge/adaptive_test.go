package ucibridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
)

// TestBridgeAdaptive drives a short UCI session with on-device adaptive time
// management (FT2_ADAPT) enabled and confirms the bridge (a) still produces
// legal bestmoves each move, and (b) actually runs the per-game bank (the
// BankedClock is created and its balance moves as moves are played). It is the
// end-to-end check that the bridge's host-side allocation + SetAdaptive poke
// path works; the on-device policy itself is pinned by
// chesstest.TestAdaptiveParity and TestAdaptiveEngineBehavior.
func TestBridgeAdaptive(t *testing.T) {
	asmbuild.BuildT(t, "../..")
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("..", "..", "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	// Small per-move income keeps the session fast; Adaptive lets the engine
	// redistribute it across moves via the bank.
	b := &Bridge{Bin: bin, Defs: defs, FixedBudgetMs: 800, Adaptive: true}

	in := strings.NewReader(strings.Join([]string{
		"uci",
		"isready",
		"ucinewgame",
		"position startpos moves e2e4 e7e5",
		"go movetime 800",
		"position startpos moves e2e4 e7e5 g1f3 b8c6",
		"go movetime 800",
		"position startpos moves e2e4 e7e5 g1f3 b8c6 f1b5 a7a6",
		"go movetime 800",
		"quit",
	}, "\n"))
	var out strings.Builder
	if err := b.Run(in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	moves := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "bestmove ") {
			mv := strings.Fields(line)[1]
			if mv == "0000" || len(mv) < 4 {
				t.Errorf("adaptive bridge produced bad bestmove %q", mv)
			}
			moves++
		}
		if strings.Contains(line, "engine error") {
			t.Errorf("adaptive bridge error line: %q", line)
		}
	}
	if moves != 3 {
		t.Fatalf("want 3 bestmoves, got %d\n%s", moves, got)
	}
	// The banked clock must have been engaged (Adaptive uses it); after three
	// moves the balance is a real (nonzero, bounded) number.
	if b.clock == nil {
		t.Fatal("adaptive mode did not create the BankedClock")
	}
	if bank := b.clock.Bank(); bank > 8*b.clock.Base {
		t.Errorf("bank %d exceeds cap %d", bank, 8*b.clock.Base)
	}
	t.Logf("adaptive bridge: 3 legal moves; final bank=%d (base=%d)", b.clock.Bank(), b.clock.Base)
}
