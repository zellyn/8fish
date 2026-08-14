package ucibridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/asmbuild"
	"github.com/zellyn/8fish/internal/chesstest"
)

// TestBridgeSession drives a short UCI session: handshake, a position
// with moves, two searches (exercising the TT carryover), and a mate.
func TestBridgeSession(t *testing.T) {
	asmbuild.BuildT(t, "../..")
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("..", "..", "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	b := &Bridge{Bin: bin, Defs: defs, FixedBudgetMs: 2000}

	in := strings.NewReader(strings.Join([]string{
		"uci",
		"isready",
		"ucinewgame",
		"position startpos moves e2e4",
		"go movetime 1000",
		"position startpos moves e2e4 e7e5 g1f3",
		"go movetime 1000",
		"position fen k7/8/2K5/8/8/8/8/6R1 w - - 0 1",
		"go depth 3",
		"quit",
	}, "\n"))
	var out strings.Builder
	if err := b.Run(in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	t.Logf("session output:\n%s", got)
	for _, want := range []string{"uciok", "readyok", "bestmove"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if n := strings.Count(got, "bestmove"); n != 3 {
		t.Errorf("expected 3 bestmoves, got %d", n)
	}
	if !strings.Contains(got, "bestmove c6b6") {
		t.Errorf("mate-in-2 position should yield c6b6")
	}
	if strings.Contains(got, "info string") {
		t.Errorf("unexpected error output")
	}
}
