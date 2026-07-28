package ucibridge

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
)

// TestBridgeSoftClock drives a whole banked-clock UCI session the way a real
// Apple IIe would run it: FT2_SOFTCLK set, harness $BFF4 read trap disabled, so
// the engine stops itself on its OWN estimate of elapsed cycles and the bank
// settles on that estimate too (engineResult.spent). Nothing in the loop ever
// sees the true counter.
//
// The assertion is the one that matters for a real game: the TRUE cycles the
// engine burns over the session still track the time control. This test is
// what would catch a systematically UNDER-reporting clock — every move
// overruns, the bank settles in estimated units and never notices, and the
// session's real spend runs away from its income. That is exactly the failure
// the 2026-07-27 refit fixed, and this test was too small (eleven moves) and
// too loose (1.35x) to have caught it on its own; the gate is
// sprt.TestSoftClockAdherence, over real games.
//
// The estimator is now deliberately biased HIGH (SOFTMARGIN in asm/defs.inc),
// because the estimate is read by a threshold — idloop's predictive gate —
// where symmetric noise produces asymmetric SPEND. So the soft arm here is
// expected to come in at or below the harness-clock control, not above it.
//
// It also covers the plumbing that unit tests cannot: the entry-time operand
// patch surviving a fresh machine every move, and the estimate surviving the
// TT carry (b.aux) between moves.
func TestBridgeSoftClock(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: emulated 6502 game")
	}
	asmbuild.BuildT(t, "../..")
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("..", "..", "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	if defs["FT2_SOFTCLK"] == 0 {
		t.Fatal("FT2_SOFTCLK missing from defs.inc")
	}

	// A Ruy Lopez main line, replayed as a fixed script so both arms search the
	// same eleven positions. (Their per-move ALLOCATIONS still diverge — that
	// is the bank doing its job on two different opinions of what was spent —
	// so only the session total is comparable between arms.)
	line := strings.Fields("e2e4 e7e5 g1f3 b8c6 f1b5 a7a6 b5a4 g8f6 e1g1 f8e7 " +
		"f1e1 b7b5 a4b3 d7d6 c2c3 e8g8 h2h3 c6a5 b3c2 c7c5 d2d4 d8c7")
	var positions []string
	for i := 2; i <= len(line); i += 2 {
		positions = append(positions, "position startpos moves "+strings.Join(line[:i], " "))
	}
	// 3 s/move of emulated time: comfortably above the estimator's 0.41-0.59 s
	// resolution (one 128-node poll), which is where its error stops being
	// dominated by quantization.
	const moveMs = 3000

	run := func(soft bool) (spent []uint64, moves int) {
		b := &Bridge{Bin: bin, Defs: defs, FixedBudgetMs: moveMs, Banked: true, SoftClock: soft}
		var log strings.Builder
		b.Log = &log
		cmds := []string{"uci", "isready", "ucinewgame"}
		for _, p := range positions {
			cmds = append(cmds, p, "go movetime "+strconv.Itoa(moveMs))
		}
		cmds = append(cmds, "quit")
		var out strings.Builder
		if err := b.Run(strings.NewReader(strings.Join(cmds, "\n")), &out); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, "bestmove ") {
				mv := strings.Fields(line)[1]
				if mv == "0000" || len(mv) < 4 {
					t.Errorf("soft=%v: bad bestmove %q", soft, mv)
				}
				moves++
			}
			if strings.Contains(line, "engine error") {
				t.Errorf("soft=%v: %q", soft, line)
			}
		}
		// TIME-MOVE's spent= field is the TRUE emulated cycle count, always —
		// it is the audit line, so it never reports the engine's own opinion.
		for _, line := range strings.Split(log.String(), "\n") {
			if !strings.HasPrefix(line, "TIME-MOVE ") {
				continue
			}
			for _, f := range strings.Fields(line) {
				if v, ok := strings.CutPrefix(f, "spent="); ok {
					n, err := strconv.ParseUint(v, 10, 64)
					if err != nil {
						t.Fatalf("bad TIME-MOVE line %q", line)
					}
					spent = append(spent, n)
				}
			}
		}
		return spent, moves
	}

	softSpent, softMoves := run(true)
	hardSpent, hardMoves := run(false)
	if softMoves != len(positions) || hardMoves != len(positions) {
		t.Fatalf("moves: soft %d, hard %d, want %d", softMoves, hardMoves, len(positions))
	}
	sum := func(xs []uint64) uint64 {
		var s uint64
		for _, x := range xs {
			s += x
		}
		return s
	}
	income := uint64(moveMs) * 1_020_500 / 1000 * uint64(len(positions))
	softTot, hardTot := sum(softSpent), sum(hardSpent)
	t.Logf("session real spend: soft-clock %d (%.3f x income), harness clock %d (%.3f x income), income %d",
		softTot, float64(softTot)/float64(income), hardTot, float64(hardTot)/float64(income), income)
	for i := range softSpent {
		t.Logf("  move %d: soft %d, hard %d", i+1, softSpent[i], hardSpent[i])
	}

	// The whole-session bound. The harness-clock arm is the control: it also
	// misses its income (the engine's hard abort is 2x the allocation and the
	// bank only claws back afterwards), so the soft-clock arm is judged against
	// the SAME absolute bound rather than against a clock it cannot have. 1.35x
	// of income over eleven moves would mean the estimator is systematically
	// under-reporting by far more than its measured few-percent bias.
	if r := float64(softTot) / float64(income); r > 1.35 {
		t.Errorf("soft-clock session burned %.3f x its income: the estimate is under-reporting", r)
	}
	if r := float64(hardTot) / float64(income); r > 1.35 {
		t.Errorf("control (harness clock) session burned %.3f x its income — the bound, not the estimator, is wrong", r)
	}
}
