# Mirror cycle-cost model — calibration report

The mirror screens features by node-budgeted self-play. A node budget
charges every feature **zero** implementation cost, which over-credits a
feature that is cheap in nodes but expensive in 6502 cycles (the FT_ROOKX
rook eval set: ~219 cyc/eval-call, ~4–6% of all cycles, screened +30 Elo
under a node budget but measured −19 in the real asm time-budget SPRT).

`cycles.go` adds a calibrated 6502 cycle-cost model. The engine keeps a
running estimate `Cyc.Est`, bumped at each accountable operation, and
`SearchCycleBudget` / `PlayerCfg.CycleBudget` budget by that estimate with
the same soft-stop semantics as the node budget. This lets a feature's per-
node cost automatically discount its measured Elo.

## Ground truth

`internal/chesstest TestMicroAB` runs 18 fixed-depth searches under an
emulated 6502 (engine.bin @ main `0c8ce38`): masks **0x1f** (d6), **0x07**
(d5), **0x00** (d4), 6 FENs each. It reports the TRUE cycle total per
search plus operation-entry probe counts (search / make / eval / attacked /
ttprobe / generate). Captured verbatim in `cycles_test.go: microAB`.

    mask   searches   Σ cycles          Σ search   Σ eval
    0x1f   6          2,053,165,151     352,846    325,422
    0x07   6          1,348,563,636     337,914    310,382
    0x00   6            884,451,797     178,808    151,428
    GRAND TOTAL        4,286,180,584

## Fitted coefficients (cycles per operation)

Regressing the asm cycle total onto the asm operation counts (`TestCycleModelFit`).

**Full 6-op OLS** (best per-mask fit, but physically meaningless negative
costs from severe collinearity — the counts are all ~proportional to node
count, so NOT shipped):

    search -2410  make 514  eval 2781  attacked 1062  ttprobe -2170  generate 23319  const -6.6M
    per-mask err: 0x1f -1.1%   0x07 +2.8%   0x00 -1.8%   (worst per-search 56%)

**Shipped runtime model** — reduced `[node, make, eval, ttprobe]`, ridge-
regularized (λ≈2), through the origin (zero ops = zero cycles). The
generate/attacked regressors are dropped on purpose: their per-node
frequency diverges badly between mirror and asm (see limitations), so their
asm-fitted cost does not transfer to mirror counts. `ttprobe` fires once per
full-width node, so it doubles as the full-width-node premium (TT probe +
null/RFP/futility dispatch).

    | operation            | cycles | charged                                  |
    |----------------------|-------:|------------------------------------------|
    | Node                 |   1013 | every search() node entry (full + QS)    |
    | Make (+ paired unmake)|  1137 | every real make() (bundles unmake)       |
    | Eval (base)          |    872 | every eval() call                        |
    | TTProbe (full-node)  |   9637 | every full-width node (TT + sprep block) |
    | EvalTerm             |      0 | + per eval when EvalTerms enabled (knob) |
    | Attacked / Generate / MovePerGen / MakeNull / TTStore | 0 | folded into the above |

### Predicted vs actual — shipped model on asm counts (`TestCycleModelFrozen`)

    mask 0x1f grand total  err  -3.35%
    mask 0x07 grand total  err  +8.32%
    mask 0x00 grand total  err -10.09%
    worst per-search err 46.9%  (the 7-piece endgame "8/2p5/…" outlier)

Per-mask within ~12%; every middlegame search within ~20%. The endgame is a
genuine outlier (fewer pieces => cheaper attacked/make scans than a global
per-op cost can capture).

## Feature-cost validation — the point of the exercise (`TestEvalTermTax`)

The rook set costs **219 cyc/eval-call** in the asm (`RookTermCost`). Its
share of total cycles, straight from the ground truth:

    mask 0x1f: 219 × 325,422 / 2,053,165,151 = 3.47%
    mask 0x07: 219 × 310,382 / 1,348,563,636 = 5.04%
    mask 0x00: 219 × 151,428 /   884,451,797 = 3.75%

The mirror reproduces this: with `Extra=RookTermsAsm` and
`Costs.EvalTerm=219`, the term is **4.17%** of the mirror's estimated cycles
at m1f fen0 — inside the 4–6% band the asm shows. Under a fixed
`CycleBudget`, that fraction is exactly the discount applied to the feature
(`TestCycleBudgetDiscountsFeature`: same tree, taxed vs untaxed, the taxed
engine reaches no deeper and searches fewer nodes; a NODE budget sees no
difference at all).

## How a future feature-screen sets its cost

Set both the term and its per-eval-call cost on the `PlayerCfg`:

    cfg := PlayerCfg{
        Features:      FtAll,
        Extra:         myTerms,        // the experimental eval terms
        EvalTermsCost: 219,            // measured asm cost, or a pessimistic estimate
        CycleBudget:   50_000_000,     // budget in estimated 6502 cycles
    }

- **Term already ported to asm** → use its measured cycles/eval-call.
- **No asm implementation yet** → use `EvalTermsCost(pieceListPasses)`, which
  charges ≥219 cyc per piece-list pass the term needs (deliberately
  pessimistic — a feature should have to *beat* a conservative cost estimate,
  never screen cheaper than the cheapest thing we have actually measured).

## Known limitations

1. **Mirror ≠ asm tree.** The mirror's full-width tree matches the asm's at
   mask 0x00 exactly (TT-probe counts agree to <1%), but at masks with
   search features the full-width trees drift, and at ALL masks the mirror's
   **quiescence** tree is much larger than the asm's (asm QS prunes/inlines
   more; ratio 0.9×–24× by position, see `TestFullWidthParity`). So the
   calibration is fit on the asm's OWN operation counts, and per-position
   mirror-count predictions are NOT apples-to-apples with asm cycles. The
   runtime applies the real per-op costs to the mirror's (larger) tree,
   yielding a self-consistent virtual-cycle budget; the eval-term *fraction*
   transfers correctly because the QS inflation cancels in the ratio.
2. **Per-op costs are not constant.** `make` is more expensive when
   `FtPstruct` is on (pawnterm runs inside make); `attacked`/`make` are
   cheaper with fewer pieces. A single global linear model has ~10%
   irreducible per-mask error and a large endgame outlier.
3. **Collinearity.** The operation counts are near-proportional, so
   individual coefficients are not uniquely identifiable; ridge + a reduced
   regressor set buys interpretable, transferable costs at the price of some
   asm-side per-mask accuracy.
4. **Absolute Est is a mirror-internal quantity**, roughly the QS-inflation
   factor (~3×) larger than the asm's actual cycles for the same position.
   It is valid for A/B budgeting (both sides costed identically) and for
   feature-cost fractions; it is not a prediction of the asm's wall time.

Recalibrate (re-run `TestMicroAB`, paste the new rows into `microAB`, re-run
`TestCycleModelFit`, update the `fitted*` constants) whenever the mirror's
QS shape or the asm engine change materially.
