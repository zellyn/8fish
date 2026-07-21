package mirror

import (
	"math/rand/v2"
	"sort"
	"testing"
)

// TestAspirationStats measures, for each aspiration variant against the
// off baseline, the re-search rate (fail-low/high frequency) and the
// effective depth the 30000-node budget buys — the two mechanism numbers
// the port decision needs beyond Elo. Self-play both sides with the same
// asm-matched config (FtAll, recap2, shipped weights); each side keeps its
// engine across the game so the cumulative aspiration counters accrue.
// Skipped under -short (it runs millions of nodes); invoke with:
//
//	go test ./internal/mirror/ -run TestAspirationStats -v
func TestAspirationStats(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: instrumented self-play over many nodes")
	}
	const (
		budget   = 30000
		games    = 40
		plyCap   = 60 // plies per game (measurement sample, not a full game)
		openSeed = 6502
	)
	openings, err := GenOpenings([][]string{
		{"e2e4", "e7e5"}, {"d2d4", "d7d5"}, {"c2c4", "e7e5"}, {"g1f3", "d7d5"},
	}, games, openSeed)
	if err != nil {
		t.Fatal(err)
	}

	type stat struct {
		moves                        int
		depths                       []int
		windows, failLow, failHigh   uint64
	}
	// play one side-symmetric game with the given aspiration config and fold
	// its per-move depth and end-of-game counters into s.
	play := func(asp AspirationParams, opening []string, rnd *rand.Rand, s *stat) {
		start, _ := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
		mk := func() *Engine { e := NewEngine(); e.Asp = asp; return e }
		we, be := mk(), mk()
		gp := *start
		for _, ms := range opening {
			if applyUCI(we, &gp, ms) != nil {
				return
			}
		}
		for ply := 0; ply < plyCap; ply++ {
			eng := we
			if gp.Side != 0 {
				eng = be
			}
			eng.SetPosition(&gp)
			if gp.Halfmove >= 100 {
				break
			}
			eng.Seed = byte(rnd.IntN(255)) + 1
			best, _ := eng.SearchBudget(budget, MaxPly-1)
			if best.From == NoSq {
				break
			}
			s.moves++
			s.depths = append(s.depths, eng.CompletedDepth)
			eng.SetPosition(&gp)
			eng.make(best)
			gp = eng.Pos
			gp.Ply = 0
		}
		for _, eng := range []*Engine{we, be} {
			s.windows += eng.AspWindows
			s.failLow += eng.AspFailLow
			s.failHigh += eng.AspFailHigh
		}
	}

	summ := func(s stat) (meanDepth, medDepth float64) {
		if len(s.depths) == 0 {
			return 0, 0
		}
		sum := 0
		ds := append([]int(nil), s.depths...)
		for _, d := range ds {
			sum += d
		}
		sort.Ints(ds)
		med := float64(ds[len(ds)/2])
		if len(ds)%2 == 0 {
			med = float64(ds[len(ds)/2-1]+ds[len(ds)/2]) / 2
		}
		return float64(sum) / float64(len(ds)), med
	}

	var offMean float64
	t.Logf("%-9s  %6s  %8s  %8s  %10s  %10s", "variant", "moves", "meanDep", "medDep", "windows", "researchRate")
	for i, v := range aspVariants {
		var s stat
		for g, opening := range openings {
			rnd := rand.New(rand.NewPCG(openSeed, uint64(g)*0x9e3779b97f4a7c15+0x1234567))
			play(v.asp, opening, rnd, &s)
		}
		mean, med := summ(s)
		if i == 0 {
			offMean = mean
		}
		rate := 0.0
		if s.windows > 0 {
			rate = float64(s.failLow+s.failHigh) / float64(s.windows)
		}
		researchPerMove := float64(s.failLow+s.failHigh) / float64(max(1, s.moves))
		t.Logf("%-9s  %6d  %8.2f  %8.0f  %10d  %9.1f%%   (faillow %d failhigh %d, re-search/move %.3f, depth d %+.2f)",
			v.name, s.moves, mean, med, s.windows, 100*rate, s.failLow, s.failHigh, researchPerMove, mean-offMean)
	}
}
