package mirror

import (
	"math/rand/v2"
	"sync"
)

// Adaptive time/effort management for the Go mirror.
//
// This engine has NO clock: "time" is EFFORT — estimated 6502 cycles per
// move (cycles.go). The default mirror spends a FLAT per-move cycle budget.
// This file adds a per-GAME cycle BANK from which each move draws, plus an
// allocation POLICY that spends MORE effort on critical/hard moves and LESS
// on easy ones, at the same total per-game compute. The signals are all
// cheap and read straight out of the iterative-deepening loop:
//
//   - best-move instability: the root best move keeps changing across ID
//     iterations => the position is critical => allocate more (extend).
//   - score drop ("panic"): this iteration's root score dropped vs the
//     previous completed one => danger => extend.
//   - easy move: the root best move has been stable for several iterations
//     (and the score is flat) => stop early, bank the savings.
//
// Everything is integer-only and shift-friendly (fractions are expressed in
// EIGHTHS of Base, /8 = three shifts) so the policy ports to the 6502: a
// 24-bit bank counter in zero page, a 2-byte best-move compare, one prev
// score, and a small stable counter. See docs/plan.md and the asm-port note
// at the bottom of this file.
//
// The whole apparatus is inert unless a TimeParams is installed and On:
// PlayerCfg.Time == nil leaves the existing CycleBudget/NodeBudget/fixed-
// depth paths byte-for-byte unchanged, and TimeParams with every signal off
// (and Smooth off) spends exactly Base each move — byte-identical to a flat
// SearchCycleBudget(Base). TestEffortOffIdentical is the gate.

// TimeParams configures the adaptive effort driver. All multipliers are in
// EIGHTHS of Base (e.g. MaxEighths=24 means a per-move ceiling of 3*Base).
// The zero value with On=false is never engaged; On=true with Smooth=false
// and every signal off reproduces the flat per-move budget exactly.
type TimeParams struct {
	On   bool   // master switch (false => driver not engaged at all)
	Base uint64 // per-move cycle income == the flat per-move budget
	// BankCap bounds the carried bank; 0 => 8*Base (the BankedClock cap).
	BankCap uint64

	// Smooth selects the BankedClock-style flat baseline: the per-move base
	// ceiling is Base + bank/8 instead of Base. This spends banked savings
	// EVENLY across later moves (no signal targeting) — the fair equal-bank
	// flat baseline against which the adaptive signals are measured. When
	// Smooth is false the base ceiling is exactly Base.
	Smooth bool

	// --- Easy-move early stop (banks effort) ---
	// EasyStop ends the ID loop after a completed iteration once the root
	// best move has been stable for >= StableIters iterations and (if
	// ScoreFlat>0) the score moved <= ScoreFlat cp since the previous
	// iteration. Never fires before CompletedDepth >= MinDepth or before the
	// move has spent >= Base*MinSpendEighths/8 cycles.
	EasyStop        bool
	StableIters     int
	ScoreFlat       int
	MinDepth        int
	MinSpendEighths uint64

	// --- Panic extension (spends banked effort) ---
	// Panic raises the move's ceiling to Base*PanicEighths/8 when a completed
	// iteration's score DROPPED by >= PanicDrop cp vs the previous completed
	// iteration (a fail-low / newly-discovered danger).
	Panic        bool
	PanicDrop    int
	PanicEighths uint64

	// --- Instability extension (spends banked effort) ---
	// Unstable raises the move's ceiling to Base*UnstableEighths/8 when the
	// root best move CHANGED on the latest completed iteration.
	Unstable        bool
	UnstableEighths uint64

	// MaxEighths caps the per-move ceiling at Base*MaxEighths/8 regardless of
	// signals (and it is additionally clamped to Base+bank — a move can never
	// draw more than its income plus the whole bank). 0 => 8 (== Base, no
	// extension possible).
	MaxEighths uint64
}

// EffortBank is the per-game cycle bank. Each move earns Base cycles of
// income; the driver spends between minSpend and Base+bank on the move; settle
// carries the difference forward. The bank is SIGNED: an overspend (the
// device hard-abort, or the uncapped depth-1 iteration, on a thin bank) drives
// it NEGATIVE (debt) rather than clamping at zero, so subsequent moves' shrunken
// allocations repay it and total game effort telescopes to N*Base — the
// property that makes the A/B honest (a flat side spends the same grand total).
// Only the positive side is capped (an underspender can't hoard unboundedly).
// This mirrors chesstest.BankedClock but lives in the mirror so the screen is
// self-contained, and exposes the raw bank for the driver's signal-based (not
// just /8) draws.
type EffortBank struct {
	Base uint64
	Cap  uint64 // 0 => 8*Base
	bank int64  // signed carry: >0 saved, <0 debt (clawed-back overspend)
}

func (b *EffortBank) cap() int64 {
	if b.Cap != 0 {
		return int64(b.Cap)
	}
	return int64(8 * b.Base)
}

// settle accounts a finished move: income Base earned, spent subtracted. An
// overspend drives the bank NEGATIVE (debt); only the positive side is capped.
func (b *EffortBank) settle(spent uint64) {
	b.bank += int64(b.Base) - int64(spent)
	if m := b.cap(); b.bank > m {
		b.bank = m
	}
}

// Bank exposes the current signed balance (diagnostics/tests).
func (b *EffortBank) Bank() int64 { return b.bank }

// Diag returns the engine's accumulated effort-management diagnostics.
func (e *Engine) Diag() TimeDiag { return e.diag }

// add accumulates another game's diagnostics into d.
func (d *TimeDiag) add(o TimeDiag) {
	d.Moves += o.Moves
	d.Extended += o.Extended
	d.Shortened += o.Shortened
	d.Panics += o.Panics
	d.Unstables += o.Unstables
	d.Cycles += o.Cycles
	d.DepthSum += o.DepthSum
}

// TimeMatch is Match with per-side effort diagnostics: it plays pairs of
// games (colors swapped) between A and B and returns the match result plus
// A's and B's cumulative TimeDiag across all games. Deterministic per pair
// exactly like Match (independent of worker count).
func TimeMatch(a, b PlayerCfg, openings [][]string, pairs, workers int, seed uint64) (*MatchResult, TimeDiag, TimeDiag, error) {
	if workers <= 0 {
		workers = 1
	}
	jobs := make(chan int)
	var mu sync.Mutex
	res := &MatchResult{}
	var aDiag, bDiag TimeDiag
	var firstErr error
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pair := range jobs {
				rnd := rand.New(rand.NewPCG(seed, uint64(pair)*0x9e3779b97f4a7c15+0x1234567))
				opening := openings[pair%len(openings)]
				g1, err1 := PlayGame(a, b, opening, rnd, false) // A white, B black
				g2, err2 := PlayGame(b, a, opening, rnd, false) // B white, A black
				mu.Lock()
				if err1 != nil && firstErr == nil {
					firstErr = err1
				}
				if err2 != nil && firstErr == nil {
					firstErr = err2
				}
				if err1 == nil {
					addResult(res, g1.Result)
					aDiag.add(g1.WhiteDiag)
					bDiag.add(g1.BlackDiag)
				}
				if err2 == nil {
					addResult(res, 1-g2.Result)
					aDiag.add(g2.BlackDiag)
					bDiag.add(g2.WhiteDiag)
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
	return res, aDiag, bDiag, firstErr
}

// TimeDiag accumulates per-side effort-management diagnostics over a game
// (cumulative on the Engine, reset by resetTimeDiag at each new game/engine).
type TimeDiag struct {
	Moves     int    // moves driven by SearchTimed
	Extended  int    // ... whose ceiling was raised above the base ceiling
	Shortened int    // ... ended early by the easy-move stop
	Panics    int    // ... that fired the panic (score-drop) extension
	Unstables int    // ... that fired the instability extension
	Cycles    uint64 // total estimated cycles spent across the game
	DepthSum  int    // sum of CompletedDepth (for an average)
}

// SearchTimed runs the adaptive effort driver for one move: iterative
// deepening under a DYNAMIC per-move cycle ceiling that starts at the base
// ceiling (Base, or Base+bank/8 when Smooth) and is raised by the panic /
// instability signals or cut short by the easy-move signal, all clamped to
// [baseCeiling, min(Base*MaxEighths/8, Base+bank)]. It returns the root best
// move (NoMove only on mate/stalemate), the root score, and the cycles
// spent. The predictive soft-start gate / mate stop / discard-incomplete-
// iteration semantics are exactly SearchCycleBudget's; the ceiling moves, and
// the hard abort is 2x the hard-MAX ceiling (asm adaptaborthi: ABORTL =
// 2*CEILMAX) rather than 2x the current one, so a raised ceiling is not
// hard-aborted at the base ceiling's limit. With every signal off,
// maxCeiling == baseCeiling and this is byte-identical to
// SearchCycleBudget(Base) — TestEffortOffIdentical is the gate.
//
// bank is the current EffortBank balance (income already conceptually
// available as Base). The caller settles the bank with the returned spend.
func (e *Engine) SearchTimed(tp *TimeParams, bank int64, maxDepth int) (Move, int, uint64) {
	base := tp.Base

	// minSpend floor: a negative (debt) bank never shrinks a move below Base/4.
	minSpend := int64(base / 4)

	// Base ceiling: flat Base, or the BankedClock /8 smoothing. A negative bank
	// lowers the smoothed ceiling to repay debt, floored at minSpend.
	baseCeiling := base
	if tp.Smooth {
		bc := int64(base) + bank/8
		if bc < minSpend {
			bc = minSpend
		}
		if bc < 0 {
			bc = 0
		}
		baseCeiling = uint64(bc)
	}
	// Hard cap on a single move's spend: Base*MaxEighths/8, but never more
	// than income+bank (floored at minSpend for a debt bank), and never below
	// the base ceiling.
	maxEighths := tp.MaxEighths
	if maxEighths < 8 {
		maxEighths = 8 // < Base makes no sense; floor at Base
	}
	maxCeiling := base * maxEighths / 8
	if lim := int64(base) + bank; int64(maxCeiling) > lim {
		if lim < minSpend {
			lim = minSpend
		}
		if lim < 0 {
			lim = 0
		}
		maxCeiling = uint64(lim)
	}
	if maxCeiling < baseCeiling {
		maxCeiling = baseCeiling
	}

	ceiling := baseCeiling
	e.CycleBudget = ceiling // enables cycle accounting for the whole move
	e.CycleTrack = false
	e.resetCycles()
	e.newMove()

	e.diag.Moves++

	best, bestScore := NoMove, 0
	e.CompletedDepth = 0
	var prevScore int
	var prevBest Move
	stable := 0
	extended := false
	panicked := false
	unstable := false

	for d := 1; d <= maxDepth; d++ {
		if d > 1 {
			// Hard abort at 2x the hard-max ceiling: the asm's adaptaborthi
			// sets ABORTL = 2*CEILMAX at entry, so a RAISED ceiling is not
			// hard-aborted at 2x the base. Constant for the whole move.
			e.CycleBudget = maxCeiling * 2
		} else {
			e.CycleBudget = 0 // depth 1 always completes (accounting stays on)
		}
		iterStart := e.Cyc.Est
		e.aspIterate(d, bestScore)
		if e.aborted {
			break // incomplete iteration: keep the last completed result
		}
		best, bestScore = e.Best, e.RootScore
		e.CompletedDepth = d
		if bestScore >= mateZoneLo {
			break // winning mate: exact and final (asm idok, before adaptmaybe)
		}

		// --- signal tracking on the just-completed iteration ---
		bestChanged := d > 1 && (best.From != prevBest.From || best.To != prevBest.To)
		if d == 1 || bestChanged {
			stable = 1
		} else {
			stable++
		}
		scoreDrop := 0
		if d > 1 {
			scoreDrop = prevScore - bestScore // >0 means the score worsened
		}

		if tp.On {
			// Panic extension: score dropped => raise the ceiling.
			if tp.Panic && d > 1 && scoreDrop >= tp.PanicDrop {
				if raiseTo := base * tp.PanicEighths / 8; raiseTo > ceiling {
					if raiseTo > maxCeiling {
						raiseTo = maxCeiling
					}
					if raiseTo > ceiling {
						ceiling = raiseTo
						extended = true
					}
				}
				panicked = true
			}
			// Instability extension: best move changed => raise the ceiling.
			if tp.Unstable && bestChanged {
				if raiseTo := base * tp.UnstableEighths / 8; raiseTo > ceiling {
					if raiseTo > maxCeiling {
						raiseTo = maxCeiling
					}
					if raiseTo > ceiling {
						ceiling = raiseTo
						extended = true
					}
				}
				unstable = true
			}
			// Easy-move early stop: stable best (+ flat score) => bank it.
			if tp.EasyStop && d >= tp.MinDepth && stable >= tp.StableIters &&
				e.Cyc.Est >= base*tp.MinSpendEighths/8 {
				flat := tp.ScoreFlat <= 0 || abs(scoreDrop) <= tp.ScoreFlat
				if flat {
					e.diag.Shortened++
					prevScore, prevBest = bestScore, best
					break
				}
			}
		}

		prevScore, prevBest = bestScore, best

		// Predictive soft-start gate for the NEXT iteration, against the
		// ceiling as just raised (asm: adaptmaybe runs first, then the flat
		// predictive gate reads the movable BUDGET).
		if !idPredict(e.Cyc.Est, iterStart, ceiling) {
			break
		}
	}

	e.CycleBudget = 0
	if extended {
		e.diag.Extended++
	}
	if panicked {
		e.diag.Panics++
	}
	if unstable {
		e.diag.Unstables++
	}
	e.diag.Cycles += e.Cyc.Est
	e.diag.DepthSum += e.CompletedDepth
	return best, bestScore, e.Cyc.Est
}

// ASM-PORT NOTE (design; no asm written here).
//
// The driver already has the exact stop condition this reuses: the
// node/cycle soft-start gate ("begin iteration N+1 only while under ~50% of
// the budget") plus the hard-cap abort that discards an incomplete
// iteration. Adaptive management is that same loop with a MOVABLE budget and
// a game-level bank:
//
//   1. Bank: a 24-bit SIGNED counter BANK in zero page. Per move, income =
//      BASE (the shipped per-move cycle budget). settle: BANK = min(CAP,
//      BANK + BASE - SPENT) — NOT floored at 0, so an overspend leaves a
//      negative (debt) bank that later moves repay (total conserves to
//      N*BASE). A minSpend floor (BASE/4) on the drawn ceiling keeps a debt
//      bank from starving a move. /8 draws and the positive CAP (=8*BASE) are
//      shift chains; all adds/compares are 24-bit signed. (chesstest.BankedClock
//      is the already-validated reference for this half.)
//
//   2. Ceiling: CEIL starts at BASE (or BASE + BANK/8 for the smoothed
//      baseline). After each completed ID iteration the driver already knows
//      BESTFROM/BESTTO and the root score; keep one previous copy of each
//      (2 bytes + 1 int16) and a 1-byte STABLE counter:
//        - score dropped by >= PANICDROP  => CEIL = max(CEIL, BASE*k/8)
//        - BESTFROM/BESTTO changed         => CEIL = max(CEIL, BASE*k/8)
//        - STABLE >= N (and score flat)    => stop now (bank the rest)
//      clamped to min(BASE*MAXK/8, BASE+BANK). The gate that decides whether
//      to start the next iteration then compares against CEIL instead of the
//      constant budget — a one-word substitution in the existing driver.
//
// No floats, no tables: the multipliers are eighths (shift+add), the signals
// are a compare and a counter. The winning mirror policy's parameters map
// directly to (PANICDROP, MAXK, N) constants.
