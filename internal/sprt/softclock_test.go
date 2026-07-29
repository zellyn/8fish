package sprt

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
)

// ---------------------------------------------------------------------------
// FT2_SOFTCLK's ACCEPTANCE GATE: in-game adherence.
//
// WHY THIS TEST EXISTS, AND WHY IT LIVES HERE. The estimator's original gate
// was chesstest.TestSoftClockAccuracy — 284 budgeted searches from a pool of
// opening positions, scored on aggregate estimate/truth. It PASSED at 1.052,
// meaning "the engine thinks it has spent 5% more than it has, so it stops
// early". In actual games the same build overran its clock by 17%: the error
// was backwards IN SIGN, not merely in size, and the +29 Elo it appeared to
// win was 26% more compute (docs/results.md 2026-07-27).
//
// Two independent reasons pool estimate/truth cannot be the gate:
//
//  1. It measures the wrong POSITIONS. A pool of opening starts makes ~1.21
//     moves per search node; positions the engine actually reaches in play
//     make ~1.58, and at matched taper phase they cost ~18% more per node.
//     The phase regressor cannot see the difference.
//
//  2. Even with the position set fixed, estimate/truth is the wrong
//     QUANTITY. The estimate is consumed by a threshold — idloop starts
//     another iteration only if now + 2*cost(last) fits the budget — and a
//     threshold turns symmetric clock noise into asymmetric SPEND, because
//     an extra iteration costs 2-6x what stopping early saves. A clock with
//     estimate/truth = 0.99 measured an adherence of 1.12.
//
// So the gate is the thing that would actually forfeit a game on hardware:
// own true cycles divided by own intended cycles, over real games, measured
// exactly the way cmd/sprt measures it. And it is checked against the
// EXACT-clock control in the same run, because the estimator is only honest
// if it spends what the real clock would spend.
// ---------------------------------------------------------------------------

// softClockGateBudgetsMs are the level lengths the gate runs at.
//
// ★ WHY THERE IS MORE THAN ONE, added 2026-07-29. Until this list existed the
// gate ran at 4000 ms ONLY — and 4000 ms is not a length the shipped disk can
// be set to spend most of its life at. `asm/m8.s` LVB*/LVDEPTH ship levels 5-9
// at 4 s, 8 s, 15 s, 30 s and 60 s per move, and the Sargon gauntlets that
// produce the headline Elo run at 30M cycles ≈ 29 s. So the acceptance gate
// certified octave 13 while the product used octaves 16-17.
//
// That matters here specifically, because the thing being gated — the
// `chesstest.SoftClockMargin` table — is MEASURED at two anchors (4 s and
// 15 s) and held FLAT at 100% above octave 15. Everything the long levels use
// is extrapolation, and it was extrapolation no test looked at. This is the
// same "test what you ship" shape as the harness clock the hardware lacks, the
// position pool that was not game conditions, and the disk that shipped
// FEATURES=$1F.
//
//	4000  — the deciding A/B's control, and the hard SHORT case: a 4 s move is
//	        only ~7 clock polls, so the sampling quantum and the entry prime
//	        both bite. Margin-table octave 13 (127%).
//	30000 — LEVEL 8, the shipped long control and the gauntlet budget.
//	        Margin-table octave 16: the flat, never-measured 100% tail.
var softClockGateBudgetsMs = []uint64{4000, 30000}

// TestSoftClockAdherence is the gate. Both arms are the shipped configuration
// (FEATURES 0x5F, FEATURES2 FT2_GENDEFER); the only difference is the
// FT2_SOFTCLK bit, which also switches the harness's $BFF4 read trap off so
// the engine runs on its own estimate exactly as a IIe would.
func TestSoftClockAdherence(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: budgeted self-play games")
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
	softBit := byte(defs["FT2_SOFTCLK"])
	genDefer := byte(defs["FT2_GENDEFER"])
	if softBit == 0 || genDefer == 0 {
		t.Fatal("FT2_SOFTCLK / FT2_GENDEFER missing from defs.inc")
	}
	feat := byte(0x1F) | byte(defs["FT_CKEXT"])

	pairs := 6
	if v := os.Getenv("SOFTCLK_PAIRS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			pairs = n
		}
	}
	for _, budgetMs := range softClockGateBudgetsMs {
		income := budgetMs * chesstest.CyclesPerMs

		run := func(feat2 byte) (adherence float64, cycles uint64, moves int) {
			res := Run(Config{
				Bin: bin, Defs: defs,
				FeaturesA: feat, FeaturesB: feat,
				FeaturesA2: feat2, FeaturesB2: feat2,
				BudgetCycles: income,
				Pairs:        pairs,
				Parallel:     max(1, parallelDefault()),
				PerGame:      true,
			})
			for _, e := range res.Errors {
				t.Error(e)
			}
			cycles = res.ACycles + res.BCycles
			moves = res.AMoves + res.BMoves
			if moves == 0 {
				t.Fatal("no moves played")
			}
			return float64(cycles) / (float64(income) * float64(moves)), cycles, moves
		}

		softAdh, softCyc, softMv := run(genDefer | softBit)
		exactAdh, exactCyc, exactMv := run(genDefer)

		t.Logf("budget %d ms (margin %d%%)", budgetMs, chesstest.SoftClockMargin(income))
		t.Logf("  SOFT  clock: adherence %.4f  (%d cycles over %d moves)", softAdh, softCyc, softMv)
		t.Logf("  EXACT clock: adherence %.4f  (%d cycles over %d moves)", exactAdh, exactCyc, exactMv)
		t.Logf("  equal-total-spend soft/exact = %.4f", softAdh/exactAdh)

		// (1) THE FORFEIT TEST. On hardware the estimate IS the clock, so an
		// adherence above 1 is the engine spending more than its allocation, move
		// after move, with nothing to catch it but the flag. This is the
		// assertion the pool test could not make.
		if softAdh > 1.00 {
			t.Errorf("%d ms: in-game adherence %.4f > 1.00: the engine OVERRUNS its clock — "+
				"raise this octave's entry in chesstest.softMarginPct / asm/m8.s KTAB "+
				"(see cmd/softclkdiag)", budgetMs, softAdh)
		}
		// (2) COLLAPSE ALARM — and note what it is NOT.
		//
		// This used to be an absolute `softAdh < 0.85`, calibrated when the gate
		// ran at 4000 ms only. It does not survive the octave: the FLAT
		// predictive gate leaves more of a long budget unspent BY DESIGN (it
		// only starts iteration d+1 if now + 2*cost(d) fits, and the last
		// iteration of a 30 s search is a much bigger thing to decline), so the
		// EXACT clock's own adherence falls from 0.9107 at 4 s to 0.8964 at
		// 30 s. A floor of 0.85 sits 5.5% under that control — well inside this
		// instrument's composition noise, which is large because the two arms
		// play DIFFERENT games: the 30 s run's arms produced 1254 and 882 moves,
		// a 42% difference in move count, and adherence falls steeply with game
		// ply (softclkdiag: 0.96 at plies 0-19, 0.75 at plies 120-139). The soft
		// arm measured 0.8496 there and TestPairedClockProbe — same build, same
		// budget, zero composition noise — puts the true soft/exact at
		// 0.9792 [0.945, 1.017]. So a 0.85 absolute floor at 30 s reports which
		// arm happened to play longer games, not what the estimator did.
		//
		// The estimator-vs-control judgement is (3), which has the control in
		// the same run. This is only the "did the whole thing collapse" alarm,
		// and its constant must therefore sit clear of every measured
		// exact-clock adherence plus that composition noise.
		if softAdh < 0.75 {
			t.Errorf("%d ms: in-game adherence %.4f < 0.75: the estimator is so conservative "+
				"it is leaving %.0f%% of the budget unspent (exact-clock control: %.4f)",
				budgetMs, softAdh, 100*(1-softAdh), exactAdh)
		}
		// (3) EQUAL SPEND against the exact-clock control. Only when the two
		// sides spend the same total compute does an Elo A/B measure the
		// estimator instead of a compute advantage — the exact defect that made
		// the first FT2_SOFTCLK measurement worthless. The exact clock's own
		// adherence is ~0.91 at 4 s and ~0.90 at 30 s (the predictive gate leaves
		// budget unspent by design), so this is the binding constraint, not (1).
		ratio := softAdh / exactAdh
		if ratio < 0.90 || ratio > 1.10 {
			t.Errorf("%d ms: soft/exact total spend %.4f outside [0.90, 1.10]: an Elo A/B at "+
				"this setting would be measuring compute, not the estimator", budgetMs, ratio)
		}
	}
}

func parallelDefault() int {
	if n := runtime.NumCPU(); n > 2 {
		return n - 1
	}
	return 1
}
