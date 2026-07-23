package mirror

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
)

// PlayerCfg configures one side of a self-play game. A nil LMR means
// the asm's current rules (DefaultLMR).
type PlayerCfg struct {
	Features    byte
	Weights     Weights
	Depth       int
	LMR         *LMRParams
	QS          QSParams
	FixFutility bool
	Fut         *FutilityParams
	KB          *KBTables
	Ord         OrderParams
	LMP         LMPParams
	Asp         AspirationParams
	// CM enables the countermove heuristic (zero value = off). Pair it with
	// CMCost so cycle-budgeted screens pay its real per-node 6502 tax.
	CM CountermoveParams
	// CMCost is the estimated per-probe/per-store 6502 cost of the
	// countermove heuristic, charged only in CycleBudget mode
	// (Costs.Countermove). 0 leaves it untaxed (node-budget behavior).
	CMCost float64

	// SEE enables portable losing-capture classification in the five-pass
	// moveLoop (zero value = off). Pair it with SEECosts so cycle-budgeted
	// screens pay the variant's real per-call 6502 tax.
	SEE SEEParams
	// SEECosts is the estimated 6502 cost triple {gate, call, rescan-item}
	// of the SEE classification (Costs.SEEGate/SEE/SEERescan), charged only
	// in CycleBudget mode. Zeros leave it untaxed (node-budget behavior).
	SEECosts [3]float64

	// Improving enables the improving heuristic (zero value = off). The
	// full-signal design (Mode==2) forces an extra eval() at every full-width
	// node that lacks one; that eval's real cost (Costs.Eval) is charged
	// automatically in CycleBudget mode, so no separate cost knob is needed.
	Improving ImprovingParams

	// Extra enables the experimental cheap eval terms (task #18/#51) for
	// this side. Zero = off (the asm's current eval). Pair it with
	// EvalTermsCost so cycle-budgeted screens pay the term's real 6502 tax.
	Extra EvalTerms
	// EvalTermsCost is the estimated per-eval-call 6502 cost of the Extra
	// terms, charged only in CycleBudget mode (Costs.EvalTerm). 0 leaves the
	// terms untaxed (node-budget behavior). For the ported rook set use
	// RookTermCost (219); for an unimplemented term use EvalTermsCost(passes)
	// for a deliberately pessimistic estimate. This is the lever that makes
	// a feature's measured Elo automatically discount its cycle cost.
	EvalTermsCost float64

	// NodeBudget selects the node-budgeted iterative-deepening mode: when
	// > 0, each move runs SearchBudget(NodeBudget, MaxIters) instead of
	// the fixed-depth SearchFixed(Depth). This is the mode that lets node-
	// saving features (ordering, QS shaping, LMR) convert their savings
	// into extra search depth, which fixed-depth self-play cannot see.
	NodeBudget uint64
	// CycleBudget selects the CYCLE-budgeted iterative-deepening mode: when
	// > 0 (and NodeBudget == 0), each move runs SearchCycleBudget(CycleBudget,
	// MaxIters), stopping on estimated 6502 cycles instead of nodes. This is
	// the mode that taxes each feature by its real per-node cost. Takes
	// precedence over NodeBudget if both are set.
	CycleBudget uint64
	MaxIters    int // ID depth ceiling for budgeted mode (0 => MaxPly-1)

	// Time, when non-nil, selects the adaptive effort-management driver
	// (effort.go): each move runs SearchTimed against a per-GAME cycle bank
	// (Base = Time.Base income per move), with the allocation policy deciding
	// how much of the bank to spend. It takes precedence over CycleBudget/
	// NodeBudget/Depth. With Time.On == false (and Smooth off, signals off)
	// each move spends exactly Base — byte-identical to CycleBudget = Base.
	Time *TimeParams
}

func (c *PlayerCfg) engine() *Engine {
	e := NewEngine()
	e.Features, e.Weights = c.Features, c.Weights
	if c.LMR != nil {
		e.LMR = *c.LMR
	}
	e.QS = c.QS
	e.FixFutilityGuard = c.FixFutility
	if c.Fut != nil {
		e.Fut = *c.Fut
	}
	e.KB = c.KB
	e.Ord = c.Ord
	e.Extra = c.Extra
	if c.EvalTermsCost != 0 {
		e.Costs.EvalTerm = c.EvalTermsCost
	}
	e.LMP = c.LMP
	e.Asp = c.Asp
	e.CM = c.CM
	if c.CMCost != 0 {
		e.Costs.Countermove = c.CMCost
	}
	e.SEE = c.SEE
	e.Costs.SEEGate, e.Costs.SEE, e.Costs.SEERescan = c.SEECosts[0], c.SEECosts[1], c.SEECosts[2]
	e.Improving = c.Improving
	return e
}

// Sample is one labeled training position for Texel tuning: the eval
// decomposed into a fixed base (taper + side-to-move tempo, white POV)
// and the pawn-structure feature counts, plus the game outcome.
type Sample struct {
	Base int
	F    pawnFeatures
	R    float64 // game result, white POV: 1, 0.5, 0
}

// GameRec is one finished self-play game.
type GameRec struct {
	Result    float64 // white POV
	Plies     int
	Samples   []Sample
	QuietFENs []string // FENs of the sampled quiet positions (collect only)
	Reason    string
	// WhiteDiag/BlackDiag carry each side's adaptive effort-management
	// diagnostics for the game (zero unless PlayerCfg.Time was set).
	WhiteDiag, BlackDiag TimeDiag
}

// PlayGame plays one fixed-depth game from the given opening (UCI
// moves). Each side gets its own engine (own TT, carried across the
// game like the bridge carries aux). Dither is on: a fresh random SEED
// byte per move, as the UCI bridge pokes. With collect, quiet non-check
// positions after ply 8 are sampled for tuning.
func PlayGame(white, black PlayerCfg, opening []string, rnd *rand.Rand, collect bool) (GameRec, error) {
	start, err := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		return GameRec{}, err
	}
	we, be := white.engine(), black.engine()

	gp := *start
	for _, ms := range opening {
		if err := applyUCI(we, &gp, ms); err != nil {
			return GameRec{}, fmt.Errorf("opening %v: %w", opening, err)
		}
	}

	rec := GameRec{}
	seen := map[uint32]int{}
	var pending []Sample // samples awaiting the game result
	winStreak, drawStreak := 0, 0
	collectGate := 0

	// Per-side per-game cycle banks for the adaptive effort driver (nil
	// Time leaves these unused).
	var wbank, bbank EffortBank
	if white.Time != nil {
		wbank = EffortBank{Base: white.Time.Base, Cap: white.Time.BankCap}
	}
	if black.Time != nil {
		bbank = EffortBank{Base: black.Time.Base, Cap: black.Time.BankCap}
	}

	for ply := 0; ; ply++ {
		eng := we
		cfg := white
		bank := &wbank
		if gp.Side != 0 {
			eng, cfg, bank = be, black, &bbank
		}
		eng.SetPosition(&gp)
		seen[eng.Pos.Hash]++

		// Adjudication (the referee's job in the real rig).
		inChk := eng.curInCheck()
		switch {
		case gp.Halfmove >= 100:
			rec.Reason = "50-move"
		case seen[eng.Pos.Hash] >= 3:
			rec.Reason = "threefold"
		case gp.Phase < 2 && !eng.anyPawn():
			rec.Reason = "material"
		case ply >= 400:
			rec.Reason = "move-cap"
		}
		if rec.Reason != "" {
			rec.Result = 0.5
			break
		}

		if collect && ply >= 8 && !inChk && isQuiet(eng) {
			// Every 2nd qualifying position: in-game samples are
			// heavily correlated, so favor game count over density.
			collectGate++
			if collectGate%2 == 0 {
				base := eng.Pos.taperedWhite()
				if gp.Side == 0 {
					base += Tempo
				} else {
					base -= Tempo
				}
				pending = append(pending, Sample{Base: base, F: *extractPawnFeatures(&eng.Pos)})
				rec.QuietFENs = append(rec.QuietFENs, gp.FEN())
			}
		}

		eng.Seed = byte(rnd.IntN(255)) + 1 // dither on
		var best Move
		var score int
		switch {
		case cfg.Time != nil:
			maxIters := cfg.MaxIters
			if maxIters <= 0 {
				maxIters = MaxPly - 1
			}
			var spent uint64
			best, score, spent = eng.SearchTimed(cfg.Time, bank.bank, maxIters)
			bank.settle(spent)
		case cfg.CycleBudget > 0:
			maxIters := cfg.MaxIters
			if maxIters <= 0 {
				maxIters = MaxPly - 1
			}
			best, score = eng.SearchCycleBudget(cfg.CycleBudget, maxIters)
		case cfg.NodeBudget > 0:
			maxIters := cfg.MaxIters
			if maxIters <= 0 {
				maxIters = MaxPly - 1
			}
			best, score = eng.SearchBudget(cfg.NodeBudget, maxIters)
		default:
			best, score = eng.SearchFixed(cfg.Depth)
		}
		if best.From == NoSq {
			if inChk { // checkmate: side to move loses
				if gp.Side == 0 {
					rec.Result = 0
				} else {
					rec.Result = 1
				}
				rec.Reason = "mate"
			} else {
				rec.Result = 0.5
				rec.Reason = "stalemate"
			}
			break
		}
		eng.SetPosition(&gp) // rewind any search-side state, then commit
		eng.make(best)
		gp = eng.Pos
		gp.Ply = 0
		rec.Plies++

		// Score adjudication (cutechess-style, for tuning/match speed;
		// the real emulated-engine rig plays to the referee's rules).
		scoreW := score
		if eng.Pos.Side == 0 {
			scoreW = -score // the mover was black
		}
		switch {
		case scoreW >= 600:
			if winStreak < 0 {
				winStreak = 0
			}
			winStreak++
		case scoreW <= -600:
			if winStreak > 0 {
				winStreak = 0
			}
			winStreak--
		default:
			winStreak = 0
		}
		if winStreak >= 6 || winStreak <= -6 {
			if winStreak > 0 {
				rec.Result = 1
			}
			rec.Reason = "adjudicated"
			break
		}
		if ply >= 140 && scoreW >= -15 && scoreW <= 15 {
			drawStreak++
			if drawStreak >= 10 {
				rec.Result = 0.5
				rec.Reason = "adjudicated-draw"
				break
			}
		} else {
			drawStreak = 0
		}
	}
	for i := range pending {
		pending[i].R = rec.Result
	}
	rec.Samples = pending
	rec.WhiteDiag, rec.BlackDiag = we.diag, be.diag
	return rec, nil
}

// isQuiet reports whether a root quiescence sweep returns the static
// eval (no capture improves the position). Dither is disabled during
// the check.
func isQuiet(e *Engine) bool {
	seed := e.Seed
	e.Seed = 0
	static := e.eval()
	e.MaxDepth = 0
	e.Pos.Ply = 0
	e.inChk[0] = false // caller ensured not in check
	e.alpha[0] = -Inf
	e.beta[0] = Inf
	qv := e.search()
	e.Seed = seed
	return qv == static
}

// applyUCI plays a UCI move string on gp using eng as scratch.
func applyUCI(eng *Engine, gp *Position, ms string) error {
	eng.SetPosition(gp)
	for _, m := range eng.generate(false) {
		if m.UCI() != ms {
			continue
		}
		eng.make(m)
		king := eng.Pos.PieceSq[int(eng.Pos.Side^ColorMask)<<1]
		if eng.attacked(king, eng.Pos.Side) {
			eng.unmake()
			continue
		}
		*gp = eng.Pos
		gp.Ply = 0
		return nil
	}
	return fmt.Errorf("illegal or unknown move %q", ms)
}

// MatchResult accumulates A's wins/draws/losses.
type MatchResult struct {
	Wins, Draws, Losses int
}

func (r *MatchResult) Games() int { return r.Wins + r.Draws + r.Losses }

func (r *MatchResult) Score() float64 {
	if r.Games() == 0 {
		return 0.5
	}
	return (float64(r.Wins) + 0.5*float64(r.Draws)) / float64(r.Games())
}

// EloDiff returns the Elo estimate and ~95% margin (same formula as
// internal/sprt.Result.EloDiff).
func (r *MatchResult) EloDiff() (elo, margin float64) {
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

func (r *MatchResult) String() string {
	elo, margin := r.EloDiff()
	return fmt.Sprintf("+%d =%d -%d  score %.1f%%  elo %+.0f +/- %.0f",
		r.Wins, r.Draws, r.Losses, 100*r.Score(), elo, margin)
}

// Match plays pairs of games (colors swapped) between A and B over the
// opening set, in parallel. Openings cycle; each worker gets its own
// deterministic RNG stream for dither.
func Match(a, b PlayerCfg, openings [][]string, pairs, workers int, seed uint64) (*MatchResult, error) {
	if workers <= 0 {
		workers = 1
	}
	type job struct{ pair int }
	jobs := make(chan int)
	var mu sync.Mutex
	res := &MatchResult{}
	var firstErr error
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pair := range jobs {
				// Seed dither per PAIR, not per worker: a pair's random
				// stream is then a pure function of (seed, pair), so the
				// match result is fully reproducible regardless of how the
				// worker pool schedules pairs. (Per-worker seeding made the
				// dither depend on nondeterministic work-stealing order.)
				rnd := rand.New(rand.NewPCG(seed, uint64(pair)*0x9e3779b97f4a7c15+0x1234567))
				opening := openings[pair%len(openings)]
				g1, err1 := PlayGame(a, b, opening, rnd, false) // A white
				g2, err2 := PlayGame(b, a, opening, rnd, false) // A black
				mu.Lock()
				if err1 != nil && firstErr == nil {
					firstErr = err1
				}
				if err2 != nil && firstErr == nil {
					firstErr = err2
				}
				if err1 == nil {
					addResult(res, g1.Result)
				}
				if err2 == nil {
					addResult(res, 1-g2.Result)
				}
				mu.Unlock()
			}
		}()
	}
	go func() {
		for p := range pairs {
			jobs <- p
		}
		close(jobs)
	}()
	wg.Wait()
	return res, firstErr
}

// addResult records a game from A's POV (score 1 = A won).
func addResult(r *MatchResult, aScore float64) {
	switch aScore {
	case 1:
		r.Wins++
	case 0:
		r.Losses++
	default:
		r.Draws++
	}
}

// GenOpenings builds n distinct opening lines: a base line plus a
// seeded random 2-ply tail, kept only if the resulting position's
// static eval is roughly balanced (mirroring sprt.GenOpenings, with
// the mirror itself as the balance filter at depth 2).
func GenOpenings(bases [][]string, n int, seed uint64) ([][]string, error) {
	rnd := rand.New(rand.NewPCG(seed, 0x09e41145))
	eng := NewEngine()
	start, err := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		return nil, err
	}
	var out [][]string
	seen := map[string]bool{}
	for tries := 0; len(out) < n && tries < n*200; tries++ {
		base := bases[rnd.IntN(len(bases))]
		gp := *start
		line := make([]string, 0, len(base)+2)
		ok := true
		for _, ms := range base {
			if err := applyUCI(eng, &gp, ms); err != nil {
				ok = false
				break
			}
			line = append(line, ms)
		}
		for range 2 {
			if !ok {
				break
			}
			legal := legalMoves(eng, &gp)
			if len(legal) == 0 {
				ok = false
				break
			}
			m := legal[rnd.IntN(len(legal))]
			line = append(line, m.UCI())
			if err := applyUCI(eng, &gp, m.UCI()); err != nil {
				ok = false
				break
			}
		}
		key := fmt.Sprint(line)
		if !ok || seen[key] {
			continue
		}
		eng.ClearTT()
		eng.SetPosition(&gp)
		eng.Seed = 0
		_, score := eng.SearchFixed(2)
		if score > 60 || score < -60 {
			continue
		}
		seen[key] = true
		out = append(out, line)
	}
	if len(out) < n {
		return nil, fmt.Errorf("only generated %d/%d openings", len(out), n)
	}
	return out, nil
}

// legalMoves returns the legal moves in gp (helper for openings).
func legalMoves(eng *Engine, gp *Position) []Move {
	eng.SetPosition(gp)
	var out []Move
	for _, m := range eng.generate(false) {
		eng.make(m)
		king := eng.Pos.PieceSq[int(eng.Pos.Side^ColorMask)<<1]
		if !eng.attacked(king, eng.Pos.Side) {
			out = append(out, m)
		}
		eng.unmake()
	}
	return out
}
