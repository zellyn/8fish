package mirror

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
)

// convPos is one canonical won (or drawn) endgame. White is always the
// stronger side; want is the correct result ("win" for won endings,
// "draw" for the drawn KPK).
type convPos struct {
	name string
	fen  string
	want string
}

var convSuite = []convPos{
	// KQK — queen mate.
	{"KQK-a", "8/8/8/4k3/8/8/8/3QK3 w - - 0 1", "win"},
	{"KQK-b", "7k/8/8/8/8/8/8/Q3K3 w - - 0 1", "win"},
	{"KQK-c", "8/8/4k3/8/8/8/8/Q2K4 w - - 0 1", "win"},
	// KRK — rook mate.
	{"KRK-a", "8/8/8/4k3/8/8/8/R3K3 w - - 0 1", "win"},
	{"KRK-b", "7k/8/8/8/8/8/8/R3K3 w - - 0 1", "win"},
	{"KRK-c", "4k3/8/8/8/8/8/8/R3K3 w - - 0 1", "win"},
	// KRRK — two rooks.
	{"KRRK-a", "3k4/8/8/8/8/8/8/R3K1R1 w - - 0 1", "win"},
	{"KRRK-b", "4k3/8/8/8/8/8/R7/R3K3 w - - 0 1", "win"},
	// KBBK — two bishops (opposite colors).
	{"KBBK-a", "8/8/8/4k3/8/8/8/2B1KB2 w - - 0 1", "win"},
	{"KBBK-b", "7k/8/8/8/8/8/8/2B1KB2 w - - 0 1", "win"},
	// KPK — a clearly winning one (king in front of pawn) and a drawn
	// rook-pawn one (defender traps the king in the corner).
	// WARNING (2026-07-25): this label is WRONG. With WHITE to move this is
	// the textbook MUTUAL ZUGZWANG DRAW (1.Kc3 Kc5) — Stockfish 18 says cp 0
	// at depth 67. It is "won" only with BLACK to move. Left as-is so the
	// shipped mop-up's historical numbers stay comparable, but the mop-up
	// entry's "partially recovers KPK (0->2/4)" therefore describes turning a
	// DRAW into a win against a handicapped twin, not a recovered win. See
	// convEndgame's kpk-front in endgame_conversion_test.go.
	{"KPK-win", "8/8/8/3k4/8/3K4/3P4/8 w - - 0 1", "win"},
	{"KPK-draw", "8/8/8/8/8/k7/P7/K7 w - - 0 1", "draw"},
	// KBNK — bishop + knight (the hard one; must reach the bishop-color
	// corner). Optional.
	{"KBNK-a", "8/8/8/4k3/8/8/8/2BNK3 w - - 0 1", "win"},
}

// playOut drives white (winner) against black (defender) from fen under
// a per-move node budget, returning the outcome reason and the number of
// full moves played. Mirrors PlayGame's adjudication (mate/stalemate,
// 50-move, threefold, insufficient material) with dither on for
// realistic play.
func playOut(t *testing.T, fen string, winner, defender PlayerCfg, budget uint64, maxPlies int, seed uint64) (string, int) {
	start, err := ParseFEN(fen)
	if err != nil {
		t.Fatalf("fen %q: %v", fen, err)
	}
	we, be := winner.engine(), defender.engine()
	rnd := rand.New(rand.NewPCG(seed, 0x5eed))
	gp := *start
	seen := map[uint32]int{}
	for ply := 0; ply < maxPlies; ply++ {
		eng := we
		cfg := winner
		if gp.Side != 0 {
			eng, cfg = be, defender
		}
		eng.SetPosition(&gp) // recomputes Phase/Hash (ParseFEN leaves them 0)
		seen[eng.Pos.Hash]++
		inChk := eng.curInCheck()
		switch {
		case eng.Pos.Halfmove >= 100:
			return "50-move", ply / 2
		case seen[eng.Pos.Hash] >= 3:
			return "threefold", ply / 2
		case eng.Pos.Phase < 2 && !eng.anyPawn():
			return "material", ply / 2
		}
		eng.Seed = byte(rnd.IntN(255)) + 1
		var best Move
		if cfg.NodeBudget > 0 || budget > 0 {
			b := budget
			if cfg.NodeBudget > 0 {
				b = cfg.NodeBudget
			}
			mi := cfg.MaxIters
			if mi <= 0 {
				mi = MaxPly - 1
			}
			best, _ = eng.SearchBudget(b, mi)
		} else {
			best, _ = eng.SearchFixed(cfg.Depth)
		}
		if best.From == NoSq {
			if inChk {
				if gp.Side != 0 { // black (defender) is mated
					return "mate", ply / 2
				}
				return "lost", ply / 2 // winner got mated (should not happen)
			}
			return "stalemate", ply / 2
		}
		eng.SetPosition(&gp)
		eng.make(best)
		gp = eng.Pos
		gp.Ply = 0
	}
	return "move-cap", maxPlies / 2
}

// classify maps a playOut reason to win/draw/fail for reporting.
func classify(reason string) string {
	switch reason {
	case "mate":
		return "WIN"
	case "50-move", "threefold", "material", "stalemate":
		return "draw"
	default:
		return "FAIL"
	}
}

const convBudget = 300000 // nodes/move

// runConvSuite plays every position N times (different dither seeds) with
// the given winner/defender configs and returns a per-position summary.
func runConvSuite(t *testing.T, winner, defender PlayerCfg, runs, workers int) map[string]string {
	out := map[string]string{}
	var mu sync.Mutex
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, pos := range convSuite {
		wg.Add(1)
		sem <- struct{}{}
		go func(pos convPos) {
			defer wg.Done()
			defer func() { <-sem }()
			wins, movesSum, winCount := 0, 0, 0
			reasons := map[string]int{}
			for r := 0; r < runs; r++ {
				reason, moves := playOut(t, pos.fen, winner, defender, convBudget, 220, uint64(r)*2654435761+1)
				reasons[reason]++
				if reason == "mate" {
					wins++
					movesSum += moves
					winCount++
				}
			}
			avg := "-"
			if winCount > 0 {
				avg = fmt.Sprintf("%.0f", float64(movesSum)/float64(winCount))
			}
			s := fmt.Sprintf("want=%-4s  win=%d/%d  avgMoves=%-3s  reasons=%v",
				pos.want, wins, runs, avg, fmtReasons(reasons))
			mu.Lock()
			out[pos.name] = s
			mu.Unlock()
		}(pos)
	}
	wg.Wait()
	return out
}

func fmtReasons(m map[string]int) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	s := ""
	for _, k := range keys {
		s += fmt.Sprintf("%s:%d ", k, m[k])
	}
	return s
}

// TestMopupSmoke plays a couple of single games to check timing/wiring.
func TestMopupSmoke(t *testing.T) {
	base := func(m MopupParams) PlayerCfg {
		return PlayerCfg{Features: FtAll, Weights: DefaultWeights, NodeBudget: convBudget, Mopup: m}
	}
	off := base(MopupParams{})
	on := base(DefaultMopup)
	for _, tc := range []struct {
		name string
		fen  string
		w, d PlayerCfg
	}{
		{"KQK OFF", "8/8/8/4k3/8/8/8/3QK3 w - - 0 1", off, off},
		{"KQK ON", "8/8/8/4k3/8/8/8/3QK3 w - - 0 1", on, on},
		{"KRK ON", "7k/8/8/8/8/8/8/R3K3 w - - 0 1", on, on},
	} {
		reason, moves := playOut(t, tc.fen, tc.w, tc.d, convBudget, 220, 1)
		t.Logf("%-8s -> %-10s in %d moves", tc.name, reason, moves)
	}
}

// TestMopupConversion is the STEP-1 / STEP-3 conversion suite. Run with
//
//	GOWORK=off go test ./internal/mirror -run TestMopupConversion -v -timeout 30m
func TestMopupConversion(t *testing.T) {
	if testing.Short() {
		t.Skip("conversion suite is slow")
	}
	const runs = 4
	const workers = 7
	base := func(m MopupParams) PlayerCfg {
		return PlayerCfg{Features: FtAll, Weights: DefaultWeights, NodeBudget: convBudget, Mopup: m}
	}
	off := base(MopupParams{})
	on := base(DefaultMopup)

	t.Log("=== STEP 1: mop-up OFF (current engine) — winner OFF vs defender OFF ===")
	resOff := runConvSuite(t, off, off, runs, workers)
	for _, pos := range convSuite {
		t.Logf("  %-9s %s", pos.name, resOff[pos.name])
	}

	t.Log("=== STEP 3: mop-up ON (both sides) — winner ON vs defender ON ===")
	resOn := runConvSuite(t, on, on, runs, workers)
	for _, pos := range convSuite {
		t.Logf("  %-9s %s", pos.name, resOn[pos.name])
	}
}
