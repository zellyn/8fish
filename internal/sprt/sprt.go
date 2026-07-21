// Package sprt is the self-play feature-gating rig: paired openings,
// fixed emulated-cycle budgets, refchess adjudication, parallel games,
// and SPRT/Elo statistics. This is the merge gate for search features
// (docs/plan.md, D11 part 1).
package sprt

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"sync"

	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

// Openings: short balanced lines (UCI moves). Each is played twice per
// pair, colors reversed.
var Openings = [][]string{
	{"e2e4", "e7e5", "g1f3", "b8c6", "f1b5", "a7a6"},
	{"e2e4", "e7e5", "g1f3", "b8c6", "f1c4", "f8c5"},
	{"e2e4", "c7c5", "g1f3", "d7d6", "d2d4", "c5d4"},
	{"e2e4", "c7c5", "g1f3", "b8c6", "d2d4", "c5d4"},
	{"e2e4", "e7e6", "d2d4", "d7d5", "b1c3", "g8f6"},
	{"e2e4", "c7c6", "d2d4", "d7d5", "e4e5", "c8f5"},
	{"e2e4", "d7d5", "e4d5", "d8d5", "b1c3", "d5a5"},
	{"d2d4", "d7d5", "c2c4", "e7e6", "b1c3", "g8f6"},
	{"d2d4", "d7d5", "c2c4", "c7c6", "g1f3", "g8f6"},
	{"d2d4", "g8f6", "c2c4", "e7e6", "b1c3", "f8b4"},
	{"d2d4", "g8f6", "c2c4", "g7g6", "b1c3", "f8g7"},
	{"d2d4", "g8f6", "c2c4", "e7e6", "g1f3", "b7b6"},
	{"d2d4", "f7f5", "g2g3", "g8f6", "f1g2", "e7e6"},
	{"g1f3", "d7d5", "g2g3", "g8f6", "f1g2", "e7e6"},
	{"c2c4", "e7e5", "b1c3", "g8f6", "g1f3", "b8c6"},
	{"c2c4", "c7c5", "g1f3", "g8f6", "d2d4", "c5d4"},
	{"e2e4", "e7e5", "f1c4", "g8f6", "d2d3", "b8c6"},
	{"d2d4", "d7d6", "e2e4", "g8f6", "b1c3", "g7g6"},
	{"e2e4", "g7g6", "d2d4", "f8g7", "b1c3", "d7d6"},
	{"g1f3", "g8f6", "c2c4", "c7c5", "b1c3", "b8c6"},
}

// Config for a match between two feature configurations of the same
// engine binary, or between two different binaries (set BinB/DefsB;
// each side needs its own defs since memory layouts drift between
// builds).
type Config struct {
	Bin          []byte
	Defs         chesstest.Defs
	BinB         []byte         // nil: side B runs Bin
	DefsB        chesstest.Defs // nil: side B uses Defs
	FeaturesA    byte
	FeaturesB    byte
	FeaturesA2   byte // second feature byte (FT2_*) for side A (0 = none)
	FeaturesB2   byte // second feature byte (FT2_*) for side B (0 = none)
	BudgetCycles uint64
	Pairs        int    // games = 2*Pairs (each opening pair, colors swapped)
	Parallel     int
	OpenSeed     uint64 // opening-generation seed; 0 = the historical default (42)
}

// Result tallies from A's perspective.
type Result struct {
	Wins, Draws, Losses int
	Errors              []string
}

func (r *Result) Games() int { return r.Wins + r.Draws + r.Losses }

// Score returns A's score fraction.
func (r *Result) Score() float64 {
	if r.Games() == 0 {
		return 0.5
	}
	return (float64(r.Wins) + 0.5*float64(r.Draws)) / float64(r.Games())
}

// EloDiff returns the Elo estimate and its ~95% error margin.
func (r *Result) EloDiff() (elo, margin float64) {
	n := float64(r.Games())
	if n == 0 {
		return 0, math.Inf(1)
	}
	s := r.Score()
	if s <= 0 {
		return math.Inf(-1), math.Inf(1)
	}
	if s >= 1 {
		return math.Inf(1), math.Inf(1)
	}
	elo = -400 * math.Log10(1/s-1)
	// Binomial-ish std error on the score fraction.
	w, d, l := float64(r.Wins)/n, float64(r.Draws)/n, float64(r.Losses)/n
	varS := w*(1-s)*(1-s) + d*(0.5-s)*(0.5-s) + l*s*s
	se := math.Sqrt(varS / n)
	if se > 0 {
		lo, hi := s-1.96*se, s+1.96*se
		lo = math.Max(lo, 1e-9)
		hi = math.Min(hi, 1-1e-9)
		margin = (-400*math.Log10(1/hi-1) + 400*math.Log10(1/lo-1)) / 2
	}
	return elo, margin
}

// LLR computes the simple trinomial SPRT log-likelihood ratio for
// H1: elo=elo1 vs H0: elo=elo0 (GSPRT approximation).
func (r *Result) LLR(elo0, elo1 float64) float64 {
	n := float64(r.Games())
	if n == 0 || r.Wins == 0 || r.Losses == 0 {
		return 0
	}
	s := r.Score()
	w, d := float64(r.Wins)/n, float64(r.Draws)/n
	varS := w*(1-s)*(1-s) + d*(0.5-s)*(0.5-s) + (1-w-d)*s*s
	if varS <= 0 {
		return 0
	}
	s0 := 1 / (1 + math.Pow(10, -elo0/400))
	s1 := 1 / (1 + math.Pow(10, -elo1/400))
	return (s1 - s0) * (2*s*n - n*(s0+s1)) / (2 * varS)
}

// GenOpenings extends the curated openings with seeded random 2-ply
// tails, keeping only lines the engine itself evaluates as roughly
// balanced (|eval| <= 60cp at depth 2). Deterministic engines replay
// identical games from identical starts, so opening variety is what
// makes each game pair carry information.
func GenOpenings(bin []byte, defs chesstest.Defs, n int, seed uint64) [][]string {
	if seed == 0 {
		seed = 42 // historical default: reproduces every pre-flag opening set
	}
	rnd := rand.New(rand.NewPCG(0x09e41145, seed))
	out := make([][]string, 0, n)
	seen := map[string]bool{}
	for len(out) < n {
		base := Openings[rnd.IntN(len(Openings))]
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			panic(err)
		}
		line := make([]string, 0, len(base)+2)
		ok := true
		for _, ms := range base {
			mv, _ := refchess.ParseMove(ms)
			if err := ref.Make(mv); err != nil {
				ok = false
				break
			}
			line = append(line, ms)
		}
		if !ok {
			continue
		}
		for range 2 {
			legal := ref.LegalMoves()
			if len(legal) == 0 {
				ok = false
				break
			}
			mv := legal[rnd.IntN(len(legal))]
			if err := ref.Make(mv); err != nil {
				ok = false
				break
			}
			line = append(line, mv.String())
		}
		key := strings.Join(line, " ")
		if !ok || seen[key] {
			continue
		}
		// Balance filter: quick engine eval of the resulting position.
		pos, err := chesstest.ParseFEN(ref.FEN())
		if err != nil {
			continue
		}
		res, err := chesstest.SearchMove(bin, defs, pos, 2, 2_000_000_000)
		if err != nil || res.Move == "" || res.Score > 60 || res.Score < -60 {
			continue
		}
		seen[key] = true
		out = append(out, line)
	}
	return out
}

// Run plays the match. Openings cycle; each pair is one opening with
// colors swapped. With more pairs than curated openings, generated
// balanced variations keep every pair distinct.
func Run(cfg Config) *Result {
	if cfg.Parallel <= 0 {
		cfg.Parallel = 1
	}
	openings := Openings
	if cfg.Pairs > len(openings) || cfg.OpenSeed != 0 {
		openings = GenOpenings(cfg.Bin, cfg.Defs, cfg.Pairs, cfg.OpenSeed)
	}
	res := &Result{}
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.Parallel)
	var wg sync.WaitGroup
	for p := 0; p < cfg.Pairs; p++ {
		for _, aWhite := range []bool{true, false} {
			wg.Add(1)
			sem <- struct{}{}
			go func(opening int, aWhite bool) {
				defer wg.Done()
				defer func() { <-sem }()
				outcome, err := playGame(cfg, openings[opening%len(openings)], aWhite)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					res.Errors = append(res.Errors, err.Error())
					return
				}
				switch outcome {
				case 1:
					res.Wins++
				case 0:
					res.Draws++
				case -1:
					res.Losses++
				}
			}(p, aWhite)
		}
	}
	wg.Wait()
	return res
}

// playGame returns +1/0/-1 from A's perspective.
func playGame(cfg Config, opening []string, aWhite bool) (int, error) {
	ref, err := refchess.ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		return 0, err
	}
	for _, ms := range opening {
		mv, err := refchess.ParseMove(ms)
		if err != nil {
			return 0, err
		}
		if err := ref.Make(mv); err != nil {
			return 0, err
		}
	}
	seen := map[uint64]int{}
	auxes := map[bool][]byte{} // per-side TT carryover
	for range 400 {
		if ref.HalfmoveClock() >= 100 || ref.InsufficientMaterial() {
			return 0, nil
		}
		seen[ref.ZobristKey()]++
		if seen[ref.ZobristKey()] >= 3 {
			return 0, nil
		}
		legal := ref.LegalMoves()
		aTurn := (ref.SideToMove() == 0) == aWhite
		if len(legal) == 0 {
			if !ref.InCheck() {
				return 0, nil // stalemate
			}
			if aTurn {
				return -1, nil
			}
			return 1, nil
		}
		features := cfg.FeaturesA
		features2 := cfg.FeaturesA2
		bin, defs := cfg.Bin, cfg.Defs
		if !aTurn {
			features = cfg.FeaturesB
			features2 = cfg.FeaturesB2
			if cfg.BinB != nil {
				bin, defs = cfg.BinB, cfg.DefsB
			}
		}
		pos, err := chesstest.ParseFEN(ref.FEN())
		if err != nil {
			return 0, err
		}
		m, err := chesstest.NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			return 0, err
		}
		chesstest.SetFeatures(m, defs, features)
		chesstest.SetFeatures2(m, defs, features2)
		chesstest.SetBudget(m, defs, cfg.BudgetCycles, 24)
		m.Mem.Main[defs["HALFMOVE"]] = byte(min(ref.HalfmoveClock(), 255))
		if aux := auxes[aTurn]; aux != nil {
			copy(m.Mem.Aux[:], aux)
		}
		exited, code, err := m.Run(cfg.BudgetCycles*3 + 2_000_000_000)
		if err != nil || !exited {
			return 0, fmt.Errorf("engine run: exited=%v err=%v (fen %q)", exited, err, ref.FEN())
		}
		if auxes[aTurn] == nil {
			auxes[aTurn] = make([]byte, len(m.Mem.Aux))
		}
		copy(auxes[aTurn], m.Mem.Aux[:])
		if code == 2 {
			return 0, fmt.Errorf("engine says no move but referee disagrees (fen %q)", ref.FEN())
		}
		if code != 0 {
			return 0, fmt.Errorf("engine exit code %d (fen %q)", code, ref.FEN())
		}
		from := m.Mem.Main[defs["BESTFROM"]]
		to := m.Mem.Main[defs["BESTTO"]]
		flags := m.Mem.Main[defs["BESTFLAGS"]]
		ms := chesstest.MoveUCI(from, to, flags)
		mv, err := refchess.ParseMove(ms)
		if err != nil {
			return 0, err
		}
		if err := ref.Make(mv); err != nil {
			return 0, fmt.Errorf("ILLEGAL MOVE %q (fen %q): %w", ms, ref.FEN(), err)
		}
	}
	return 0, nil
}
