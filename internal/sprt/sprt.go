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

	"github.com/zellyn/8fish/harness"
	"github.com/zellyn/8fish/internal/book"
	"github.com/zellyn/8fish/internal/chesstest"
	"github.com/zellyn/8fish/internal/refchess"
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
	Pairs        int // games = 2*Pairs (each opening pair, colors swapped)
	Parallel     int
	OpenSeed     uint64 // opening-generation seed; 0 = the historical default (42)

	// PerGame switches from a flat per-move budget to a per-GAME cycle BANK:
	// each side earns BudgetCycles of income per move into its own bank and
	// may spend from it, so time management has something to manage (a flat
	// per-move budget gives it nothing). Total game compute is conserved
	// (income == BudgetCycles per move) whether or not a side redistributes,
	// which is what makes the A/B honest. Mirrors mirror.EffortBank /
	// TimeMatch. When false the harness is byte-identical to before.
	PerGame bool
	// AdaptiveA / AdaptiveB select which side runs on-device FT2_ADAPT
	// (adaptive-aggr: banking baseline + panic/instability/easy-stop signals)
	// vs flat (spends exactly its per-move income). Only consulted in PerGame
	// mode. The canonical time-management SPRT is AdaptiveA=true,
	// AdaptiveB=false — adaptive vs flat.
	AdaptiveA bool
	AdaptiveB bool

	// MoveTrace, if non-nil, is called once per engine move with that move's
	// measurement. It exists for the FT2_SOFTCLK calibration and its gate:
	// the estimator has to be fit and judged under GAME conditions (warm TT
	// carried in the aux bank, the real per-move allocation, the phase
	// distribution games actually visit), and the only way to see those is
	// from inside this loop. Nil in every ordinary match, and the loop is
	// byte-identical when it is nil.
	//
	// GAMES RUN IN PARALLEL: the callback must be goroutine-safe.
	MoveTrace func(MoveInfo)

	// ProbeAddrs, when MoveTrace is set, switches the per-move run to
	// harness.RunProfile and counts entries to each named address (2-3x
	// slower). The name "checkclocks" is special: at each entry the trace also
	// samples PHASE, which — with the poll count — reconstructs EXACTLY the
	// two regressors the on-device estimator forms, without perturbing the
	// tree the way poking the cost table would.
	ProbeAddrs map[string]uint16

	// BookA / BookB are the per-side resident opening books. The whole point
	// of having them PER SIDE is that a book A/B is INVISIBLE to an ordinary
	// SPRT: both engines would share one book, play the same opening moves,
	// and the book would cancel out exactly. Set BookA to the candidate blob
	// and BookB to the incumbent (a nil book = that side plays no book and
	// searches from move one).
	//
	// A book match must also NOT force an opening prefix — Openings would
	// play right through the region the book is supposed to be choosing. Set
	// NoOpening when BookA/BookB are set; Run refuses to do otherwise.
	BookA *book.Book
	BookB *book.Book
	// BookEntry / BookEntryB are the addresses of the asm `bookentry` label
	// (from engine.lbl) for each side's binary. Required when that side has a
	// book. BookEntryB defaults to BookEntry when BinB is nil.
	BookEntry  uint16
	BookEntryB uint16
	// BookSeed seeds the per-game deterministic PRNG that drives each side's
	// weighted book pick. Mirrors ucibridge's stream exactly (one draw per
	// probe, hit or miss), so replays reproduce.
	BookSeed uint64
	// Dither pokes a fresh eval-dither SEED byte before every search, as the
	// UCI bridge does on device. Without it the engine is deterministic, so a
	// match whose only variety comes from the book's weighted pick would
	// replay a handful of distinct games over and over and report a confidence
	// interval it has not earned. Required for a book match; harmless
	// otherwise (it is what real games do).
	Dither bool
	// NoOpening starts every game from the standard position with no forced
	// opening moves. Required for a book match; also the honest setting for
	// any measurement of opening play.
	NoOpening bool

	// ColdTT is a DIAGNOSTIC knob: drop the aux-bank transposition table
	// between moves so every search starts from an empty TT, the way a
	// position-pool test does. It exists to isolate how much of the
	// soft-clock estimator's game-vs-pool disagreement is TT warmth. Never
	// set in a real match — it makes the engine materially weaker.
	ColdTT bool
}

// MoveInfo is one engine move as the soft-clock work needs to see it.
type MoveInfo struct {
	FEN      string
	SideA    bool   // the move was played by side A
	Ply      int    // halfmove index within the game (0 = first out-of-book)
	Phase    int    // engine taper phase at the root
	Budget   uint64 // this move's soft budget, in cycles
	Cycles   uint64 // TRUE emulated cycles the move cost
	Estimate uint64 // the engine's own estimate (0 unless FT2_SOFTCLK is on)
	Depth    int    // completed ID depth
	Polls    uint64 // checkclocks entries (ProbeAddrs only)
	PhaseSum uint64 // sum of PHASE sampled at those polls (ProbeAddrs only)
	// PhaseHist counts the polls at each CLAMPED phase, exactly as
	// checkclocks indexes the cost table. With the root phase counted once
	// more for the entry prime, this is the complete set of table lookups the
	// move made — so a fit against it reproduces the on-device estimate to
	// the byte, instead of approximating it through a per-move mean.
	PhaseHist [NPCost]uint32
	Counts    map[string]uint64
}

// NPCost is the cost table's entry count (phase 0..24), mirroring NPCOST in
// asm/defs.inc; checkclocks clamps PHASE into that range.
const NPCost = 25

// effortBank is a per-game cycle bank (host-side; the engine's zero page does
// NOT persist across the harness's fresh-machine-per-move, so the bank must
// live here and be poked into each move's budget). Mirrors mirror.EffortBank
// and chesstest.BankedClock: income per move, spend, carry forward. The bank
// is SIGNED — an overspend (device hard-abort on a thin bank) drives it
// NEGATIVE (debt) rather than clamping at zero, so subsequent moves' shrunken
// allocations repay it and total game compute telescopes to N*income. Only the
// positive side is capped at 8x.
type effortBank struct {
	income uint64
	bank   int64
}

func (b *effortBank) settle(spent uint64) {
	b.bank += int64(b.income) - int64(spent)
	if cap := int64(8 * b.income); b.bank > cap {
		b.bank = cap
	}
}

// pokeAlloc installs this move's cycle budget for a side drawing from its
// per-game bank and returns the machine run cap. The flat side spends exactly
// its income. The adaptive side pokes the FT2_ADAPT movable-ceiling params,
// computed exactly as mirror.SearchTimed does (base = income; smoothed base
// ceiling = income + bank/8; hard max = min(4*income, income+bank); instability
// target = min(3*income, hard max); easy-stop min spend = income/4). The engine
// then raises the ceiling on-device via the panic/instability signals.
func pokeAlloc(m *harness.Machine, defs chesstest.Defs, income uint64, bank int64, adaptive bool) uint64 {
	if !adaptive {
		chesstest.SetBudget(m, defs, income, 24)
		return income*3 + 2_000_000_000
	}
	// Signed bank: a negative (debt) bank shrinks the base ceiling and hard max
	// so the overspend is repaid; the minSpend floor (income/4) keeps a move
	// from being starved to zero.
	minSpend := int64(income / 4)
	baseCeiling := int64(income) + bank/8
	if baseCeiling < minSpend {
		baseCeiling = minSpend
	}
	maxCeiling := int64(4 * income)
	if lim := int64(income) + bank; maxCeiling > lim {
		maxCeiling = lim
	}
	if maxCeiling < baseCeiling {
		maxCeiling = baseCeiling
	}
	unstTarget := int64(3 * income)
	if unstTarget > maxCeiling {
		unstTarget = maxCeiling
	}
	chesstest.SetBudget(m, defs, uint64(baseCeiling), 24)
	chesstest.SetAdaptive(m, defs, uint64(baseCeiling), uint64(maxCeiling), uint64(unstTarget), income/4)
	// Hard abort is 2*maxCeiling on device; allow generous slack + fixed cost.
	return uint64(maxCeiling)*3 + 2_000_000_000
}

// Result tallies from A's perspective.
type Result struct {
	Wins, Draws, Losses int
	Errors              []string
	// ACycles/BCycles are the total emulated cycles each side actually spent
	// across all games (own-move search only). AMoves/BMoves are the own-move
	// counts. In per-game bank mode the intended per-side budget is
	// income*moves; own_total/own_intended is the adherence ratio (the leak the
	// conserving/debt bank fixes), and ACycles≈BCycles is the equal-total-spend
	// check that makes the adaptive-vs-flat A/B honest.
	ACycles, BCycles uint64
	AMoves, BMoves   int
	// ABookMoves/BBookMoves count moves each side played straight out of its
	// resident book with no search. Report these: a book A/B whose two sides
	// played the SAME number of book moves, or zero, has measured nothing,
	// and that failure is otherwise indistinguishable from a true null.
	ABookMoves, BBookMoves int
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
	switch {
	case cfg.NoOpening:
		// One "opening" of zero moves: every game starts from the standard
		// position and the engines (their books, if any) choose everything.
		openings = [][]string{nil}
	case cfg.Pairs > len(openings) || cfg.OpenSeed != 0:
		openings = GenOpenings(cfg.Bin, cfg.Defs, cfg.Pairs, cfg.OpenSeed)
	}
	res := &Result{}
	if (cfg.BookA != nil || cfg.BookB != nil) && !cfg.NoOpening {
		// Guard rail for the trap this whole feature exists to avoid: with a
		// forced opening prefix the books never get to choose, so the match
		// measures nothing and looks like a clean null result.
		res.Errors = append(res.Errors,
			"book match without NoOpening: the forced opening prefix would play through the book")
		return res
	}
	var mu sync.Mutex
	sem := make(chan struct{}, cfg.Parallel)
	var wg sync.WaitGroup
	gameNo := 0
	for p := 0; p < cfg.Pairs; p++ {
		for _, aWhite := range []bool{true, false} {
			wg.Add(1)
			sem <- struct{}{}
			gn := gameNo
			gameNo++
			go func(opening int, aWhite bool, gn int) {
				defer wg.Done()
				defer func() { <-sem }()
				outcome, aCyc, bCyc, aMv, bMv, aBk, bBk, err := playGame(cfg, openings[opening%len(openings)], aWhite, gn)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					res.Errors = append(res.Errors, err.Error())
					return
				}
				res.ACycles += aCyc
				res.BCycles += bCyc
				res.AMoves += aMv
				res.BMoves += bMv
				res.ABookMoves += aBk
				res.BBookMoves += bBk
				switch outcome {
				case 1:
					res.Wins++
				case 0:
					res.Draws++
				case -1:
					res.Losses++
				}
			}(p, aWhite, gn)
		}
	}
	wg.Wait()
	return res
}

// bookFor returns the book the given side plays with (nil = no book).
func bookFor(cfg Config, aTurn bool) *book.Book {
	if aTurn {
		return cfg.BookA
	}
	return cfg.BookB
}

// playGame returns +1/0/-1 from A's perspective, plus the total emulated
// cycles and own-move count each side spent across the game (own-move search).
func playGame(cfg Config, opening []string, aWhite bool, gameNo int) (outcome int, aCyc, bCyc uint64, aMv, bMv int, aBook, bBook int, err error) {
	ref, err := refchess.ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		return
	}
	for _, ms := range opening {
		mv, err := refchess.ParseMove(ms)
		if err != nil {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, err
		}
		if err := ref.Make(mv); err != nil {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, err
		}
	}
	seen := map[uint64]int{}
	auxes := map[bool][]byte{} // per-side TT carryover
	// Per-game cycle banks (PerGame mode). aTurn==true is side A.
	banks := map[bool]*effortBank{
		true:  {income: cfg.BudgetCycles},
		false: {income: cfg.BudgetCycles},
	}
	// Per-side, per-game book PRNG. The two sides get DIFFERENT streams (the
	// side bit is folded in) so that a shared book would still produce varied
	// games rather than a mirror image, and the game number is folded in with
	// the same golden-ratio step ucibridge uses.
	mkRnd := func(side uint64) *rand.Rand {
		seed := cfg.BookSeed + uint64(gameNo)*0x9E3779B97F4A7C15 + side*0xD1B54A32D192ED03
		return rand.New(rand.NewPCG(seed, seed^0x5DEECE66D))
	}
	bookRnds := map[bool]*rand.Rand{true: mkRnd(0), false: mkRnd(1)}
	for ply := range 400 {
		if ref.HalfmoveClock() >= 100 || ref.InsufficientMaterial() {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, nil
		}
		seen[ref.ZobristKey()]++
		if seen[ref.ZobristKey()] >= 3 {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, nil
		}
		legal := ref.LegalMoves()
		aTurn := (ref.SideToMove() == 0) == aWhite
		if len(legal) == 0 {
			if !ref.InCheck() {
				return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, nil // stalemate
			}
			if aTurn {
				return -1, aCyc, bCyc, aMv, bMv, aBook, bBook, nil
			}
			return 1, aCyc, bCyc, aMv, bMv, aBook, bBook, nil
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
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, err
		}
		// ---- Resident opening book, probed on device, before any search ----
		// This mirrors ucibridge exactly: one PRNG draw per probe whether it
		// hits or misses (so the stream depends only on seed and move number,
		// not on how long the book lasted), and on a hit the move is PLAYED
		// with no search at all. The probe's few thousand cycles are still
		// charged and settled, so the unspent allocation banks toward the
		// first out-of-book search — which is a real part of what a book buys
		// and must not be given away for free.
		if bk := bookFor(cfg, aTurn); bk != nil {
			r := bookRnds[aTurn].Uint32()
			entry := cfg.BookEntry
			if !aTurn && cfg.BinB != nil && cfg.BookEntryB != 0 {
				entry = cfg.BookEntryB
			}
			res, perr := chesstest.AsmBookProbe(bin, defs, entry, bk.EntriesBlob(), pos, r)
			if perr != nil {
				return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, fmt.Errorf("book probe: %w", perr)
			}
			if res.Hit {
				mv, merr := refchess.ParseMove(res.Move)
				if merr != nil {
					return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, merr
				}
				if merr := ref.Make(mv); merr != nil {
					return 0, aCyc, bCyc, aMv, bMv, aBook, bBook,
						fmt.Errorf("ILLEGAL BOOK MOVE %q (fen %q): %w", res.Move, ref.FEN(), merr)
				}
				if aTurn {
					aCyc += res.Cycles
					aMv++
					aBook++
				} else {
					bCyc += res.Cycles
					bMv++
					bBook++
				}
				if cfg.PerGame {
					banks[aTurn].settle(res.Cycles)
				}
				continue
			}
		}
		m, err := chesstest.NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, err
		}
		chesstest.SetFeatures(m, defs, features)
		chesstest.SetFeatures2(m, defs, features2)
		// FT2_SOFTCLK means "run on the engine's own ESTIMATED elapsed-cycle
		// clock", which is only actually exercised if the harness's $BFF4 read
		// trap is off — the estimator accumulates INTO $BFF4, so with the trap
		// live the engine would keep reading the true counter and the feature
		// would measure exactly nothing. Derive the trap from the bit rather
		// than taking a separate flag, so the two cannot disagree.
		if ft2 := defs["FT2_SOFTCLK"]; ft2 != 0 && features2&byte(ft2) != 0 {
			m.Mem.ClockAddr = 0
		}
		// Budget/time-management poke. Per-move mode: flat BudgetCycles.
		// Per-game mode: draw this move's allocation from the side's bank; the
		// adaptive side additionally installs the FT2_ADAPT ceiling params.
		runCap := cfg.BudgetCycles*3 + 2_000_000_000
		if cfg.PerGame {
			adaptive := cfg.AdaptiveA
			if !aTurn {
				adaptive = cfg.AdaptiveB
			}
			runCap = pokeAlloc(m, defs, banks[aTurn].income, banks[aTurn].bank, adaptive)
		} else {
			chesstest.SetBudget(m, defs, cfg.BudgetCycles, 24)
		}
		m.Mem.Main[defs["HALFMOVE"]] = byte(min(ref.HalfmoveClock(), 255))
		if cfg.Dither {
			// Fresh SEED per search, from the same per-side per-game stream
			// that drives the book pick. Never 0 — 0 means "dither off".
			seed := byte(bookRnds[aTurn].Uint32())
			if seed == 0 {
				seed = 1
			}
			m.Mem.Main[defs["SEED"]] = seed
		}
		if aux := auxes[aTurn]; aux != nil && !cfg.ColdTT {
			copy(m.Mem.Aux[:], aux)
		}
		var exited bool
		var code byte
		var counts map[string]uint64
		var polls, phaseSum uint64
		var hist [NPCost]uint32
		if cfg.MoveTrace != nil && len(cfg.ProbeAddrs) > 0 {
			// Diagnostic path: count probe entries and sample PHASE at each
			// clock poll. Non-perturbing — RunProfile only observes.
			ccAddr := cfg.ProbeAddrs["checkclocks"]
			phaseAddr := defs["PHASE"]
			counts = make(map[string]uint64, len(cfg.ProbeAddrs))
			names := make([]string, 0, len(cfg.ProbeAddrs))
			addrs := make([]uint16, 0, len(cfg.ProbeAddrs))
			for n, a := range cfg.ProbeAddrs {
				names = append(names, n)
				addrs = append(addrs, a)
			}
			hits := make([]uint64, len(addrs))
			exited, code, err = m.RunProfile(runCap, func(pc uint16, _ uint8) {
				for i, a := range addrs {
					if pc == a {
						hits[i]++
					}
				}
				if ccAddr != 0 && pc == ccAddr {
					polls++
					ph := int(m.Mem.Main[phaseAddr])
					phaseSum += uint64(ph)
					if ph >= NPCost {
						ph = NPCost - 1 // the clamp checkclocks applies
					}
					hist[ph]++
				}
			})
			for i, n := range names {
				counts[n] = hits[i]
			}
		} else {
			exited, code, err = m.Run(runCap)
		}
		if err != nil || !exited {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, fmt.Errorf("engine run: exited=%v err=%v (fen %q)", exited, err, ref.FEN())
		}
		if cfg.MoveTrace != nil {
			budget := uint64(m.Mem.Main[defs["BUDGET0"]]) |
				uint64(m.Mem.Main[defs["BUDGET1"]])<<8 |
				uint64(m.Mem.Main[defs["BUDGET2"]])<<16
			var est uint64
			if m.Mem.ClockAddr == 0 {
				ca := defs["CLOCK_TRAP"]
				est = (uint64(m.Mem.Main[ca]) | uint64(m.Mem.Main[ca+1])<<8 |
					uint64(m.Mem.Main[ca+2])<<16) << 8
			}
			cfg.MoveTrace(MoveInfo{
				FEN: ref.FEN(), SideA: aTurn, Ply: ply, Phase: int(m.Mem.Main[defs["PHASE"]]),
				Budget: budget << 8, Cycles: m.Cycles, Estimate: est,
				Depth: int(m.Mem.Main[defs["CURDEPTH"]]),
				Polls: polls, PhaseSum: phaseSum, PhaseHist: hist, Counts: counts,
			})
		}
		// Tally actual cycles + own-move count to the side that moved.
		if aTurn {
			aCyc += m.Cycles
			aMv++
		} else {
			bCyc += m.Cycles
			bMv++
		}
		if cfg.PerGame {
			banks[aTurn].settle(m.Cycles) // actual emulated cycles spent
		}
		if auxes[aTurn] == nil {
			auxes[aTurn] = make([]byte, len(m.Mem.Aux))
		}
		copy(auxes[aTurn], m.Mem.Aux[:])
		if code == 2 {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, fmt.Errorf("engine says no move but referee disagrees (fen %q)", ref.FEN())
		}
		if code != 0 {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, fmt.Errorf("engine exit code %d (fen %q)", code, ref.FEN())
		}
		from := m.Mem.Main[defs["BESTFROM"]]
		to := m.Mem.Main[defs["BESTTO"]]
		flags := m.Mem.Main[defs["BESTFLAGS"]]
		ms := chesstest.MoveUCI(from, to, flags)
		mv, err := refchess.ParseMove(ms)
		if err != nil {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, err
		}
		if err := ref.Make(mv); err != nil {
			return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, fmt.Errorf("ILLEGAL MOVE %q (fen %q): %w", ms, ref.FEN(), err)
		}
	}
	return 0, aCyc, bCyc, aMv, bMv, aBook, bBook, nil
}
