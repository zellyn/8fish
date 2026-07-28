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

// softClockGateBudgetMs is the level length the gate runs at. 4000 ms is the
// budget the deciding A/B used and is short enough that the ~0.5 s sampling
// quantum still bites — the hard case.
const softClockGateBudgetMs = 4000

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
	income := uint64(softClockGateBudgetMs) * chesstest.CyclesPerMs

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

	t.Logf("SOFT  clock: adherence %.4f  (%d cycles over %d moves)", softAdh, softCyc, softMv)
	t.Logf("EXACT clock: adherence %.4f  (%d cycles over %d moves)", exactAdh, exactCyc, exactMv)
	t.Logf("equal-total-spend soft/exact = %.4f", softAdh/exactAdh)

	// (1) THE FORFEIT TEST. On hardware the estimate IS the clock, so an
	// adherence above 1 is the engine spending more than its allocation, move
	// after move, with nothing to catch it but the flag. This is the
	// assertion the pool test could not make.
	if softAdh > 1.00 {
		t.Errorf("in-game adherence %.4f > 1.00: the engine OVERRUNS its clock — "+
			"raise SOFTMARGIN in asm/defs.inc (see cmd/softclkdiag)", softAdh)
	}
	// (2) ... but not by throwing the budget away. Below this the estimator
	// is buying its safety with strength it did not have to give up.
	if softAdh < 0.85 {
		t.Errorf("in-game adherence %.4f < 0.85: the estimator is so conservative "+
			"it is leaving %.0f%% of the budget unspent", softAdh, 100*(1-softAdh))
	}
	// (3) EQUAL SPEND against the exact-clock control. Only when the two
	// sides spend the same total compute does an Elo A/B measure the
	// estimator instead of a compute advantage — the exact defect that made
	// the first FT2_SOFTCLK measurement worthless. The exact clock's own
	// adherence is ~0.92 (the predictive gate leaves budget unspent by
	// design), so this is the binding constraint, not (1).
	ratio := softAdh / exactAdh
	if ratio < 0.90 || ratio > 1.10 {
		t.Errorf("soft/exact total spend %.4f outside [0.90, 1.10]: an Elo A/B at "+
			"this setting would be measuring compute, not the estimator", ratio)
	}
}

func parallelDefault() int {
	if n := runtime.NumCPU(); n > 2 {
		return n - 1
	}
	return 1
}
