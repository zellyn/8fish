# Mirror cycle-cost model — calibration report

The mirror screens features by node-budgeted self-play. A node budget
charges every feature **zero** implementation cost, which over-credits a
feature that is cheap in nodes but expensive in 6502 cycles (the FT_ROOKX
rook eval set: ~219 cyc/eval-call, ~5% of all cycles, screened +30 Elo
under a node budget but measured −19 in the real asm time-budget SPRT).

`cycles.go` adds a calibrated 6502 cycle-cost model. The engine keeps a
running estimate `Cyc.Est`, bumped at each accountable operation, and
`SearchCycleBudget` / `PlayerCfg.CycleBudget` budget by that estimate with
the same soft-stop semantics as the node budget. This lets a feature's per-
node cost automatically discount its measured Elo.

**2026-07-27 recalibration.** The model was refit from scratch: new ground
truth (the asm is ~10% faster than at the previous calibration), a
calibration set enlarged from 6 positions to 23 spanning 3–32 pieces, and a
new **material term** that fixes a known defect — the old constant-per-node
model over-priced low-material nodes by 30–40%, which the frozen test had
carved out rather than fixed. Details below.

## Ground truth

`internal/chesstest TestMicroABPhase` runs 69 fixed-depth searches under an
emulated 6502 (engine.bin built from `asm/` at main `5652dc1`): **23
positions × masks 0x1F / 0x07 / 0x00**. It reports the TRUE cycle total per
search, the operation-entry probe counts (search / make / eval / attacked /
ttprobe / generate), and — new — the **material sums**: the live piece count
summed over every search/make/eval entry, and the engine's own taper PHASE
summed over every search entry. Captured verbatim in `cycles_test.go:
microAB`.

Why 23 positions. The old set was 6: five 30–32-piece middlegames and one
10-piece endgame. A per-node cost that depends on material is simply not
identifiable from that — one endgame is one data point, and fitting a
material term to it would be fitting an outlier. Seventeen positions were
added at 3, 4, 4, 4, 4, 8, 8, 10, 12, 12, 16, 16, 20, 21, 23, 24 and 28
pieces, chosen so that piece count and phase are *not* collinear (12 pieces
at phase 0 vs 12 at phase 6; 8 at phase 0 vs 8 at phase 4) so the fit can
tell the two candidate material regressors apart. Sparse positions are
searched deeper (per-mask depth bonus in `calibFens`) so every search lands
in the 5M–2.5G cycle range instead of being a rounding error next to the
midgame searches. Depth is not a regressor — the model is per operation —
so mixing depths is sound, and it has the useful side effect of spanning
full-width-node fractions from 0.03 to 0.54 (see the TT premium below).

## The defect this recalibration fixes

The shipped model charged a **constant** cost per node. Real per-node cost
is not constant: quiescence move generation emits fewer moves and
`attacked()` walks shorter slider rays when there is less material on the
board. Measured with the old coefficients (1013 / 1137 / 872 / 9637):

| set | actual/predicted, low material | actual/predicted, rest |
|---|---:|---:|
| old 18-row truth, old model | **0.714** (endgame over-priced 40%) | 1.014 |
| new 69-row truth, old model | **0.488** | 0.821 |
| new 69-row truth, old FORM refit on the balanced set | 0.980 | 1.077 |
| new 69-row truth, **shipped phase model** | **0.993** | 1.019 |

The middle row is the honest scale of it: with searches at 3–12 pieces in
the pool, the old coefficients over-predict by up to **+169%**. Two separate
mispricings compound there, and it is worth keeping them apart:

1. **Material.** Comparing like with like — the original six positions at
   their original depths — the old model errs −4…+30% on the 28–32-piece
   middlegames and **+45…+57%** on the 10-piece endgame. That gap is the
   material defect, ~30–40%, exactly as reported.
2. **The full-width-node premium.** `TTProbe` was fitted at **9637**
   cycles/full-width node. Every position in the old set had
   ttprobe/search ≈ 0.08, so that coefficient was not identifiable — it was
   absorbing ~770 cycles/node of generic per-node cost. Deep sparse searches
   run at ttprobe/search up to 0.54 and reject it outright: the premium
   refits at **1824**, and the independent round-5 profile agrees (tt.s is
   1.5% of all cycles, so a 9637-cycle TT probe was never physical).

## Fitted coefficients (cycles per operation)

`TestCycleModelFit` regresses the asm cycle total onto the asm operation
counts: non-negative, **through the origin** (zero ops = zero cycles), and
minimizing **relative** error (each row divided by its own cycle total).
Relative weighting is not cosmetic — the set spans 4.8M to 2.5G cycles per
search, and plain least squares is a cycle-weighted average in which the
biggest midgame searches set every coefficient and the cheap low-material
ones have no leverage. That is precisely how a model that "fit" could be
30% wrong on endgames.

**Shipped runtime model** — `[node, node×phase, make, eval, ttprobe]`:

    | operation             | cycles | charged                                  |
    |-----------------------|-------:|------------------------------------------|
    | Node                  |      0 | every search() node entry (see below)    |
    | NodePhase             |     44 | ... × the node's taper phase (0..24)     |
    | Make (+ paired unmake)|   1585 | every real make() (bundles unmake)       |
    | Eval (base)           |    888 | every eval() call                        |
    | TTProbe (full-node)   |   1824 | every full-width node (TT + sprep block) |
    | EvalTerm              |      0 | + per eval when EvalTerms enabled (knob) |
    | Attacked / Generate / MovePerGen / MakeNull / TTStore | 0 | folded into the above |

So a node costs `44 × phase`: ~1050 cycles in a full-material middlegame,
~90 in a rook-and-pawn ending, on top of its makes/eval/TT probe.

**Why `Node` is 0.** With a material term present, the material-INDEPENDENT
part of the per-node cost is not separately identifiable — `make` and `eval`
both fire ~once per node, so a constant per-node cost is collinear with
them, and the non-negative fit puts it on the boundary. Pinning `Node` at
200…1000 and refitting the rest costs accuracy monotonically (RMS 7.3% →
8.6%) and, more tellingly, wrecks the agreement with the independent profile
below. The zero is a statement about identifiability, not a claim that
entering a node is free: that cost is inside the make/eval coefficients.

**Independent corroboration.** The round-5 per-file profile (`TestProfileR5`,
docs/results.md 2026-07-27) attributes cycles to source files by PC. Its
split and the fitted model's split of the same mask-0x1F searches:

    | quantity                          | profile | fitted model |
    |-----------------------------------|--------:|-------------:|
    | eval.s                            |  21.3%  |  eval  21.0% |
    | board.s + movegen (make/unmake/attacked/generation) | 54.4% | make 53.0% |
    | search.s + tt.s (node control)    |  24.2%  |  node+phase+ttprobe 22.2% |

Two independent instruments — a linear fit over 69 searches and a per-PC
profile — agree within ~2 points on where the cycles go. Notably the pinned
alternatives do not: `Node=600` implies an eval share of 10.1%, half of what
the profile measures.

**Rejected alternatives** (all reported by `TestCycleModelFit`; CV is
leave-one-POSITION-out, so a model that merely memorized one endgame cannot
score well):

    | model                          | RMS  | worst | rows>15% | CV RMS | low-material ratio |
    |--------------------------------|-----:|------:|---------:|-------:|-------------------:|
    | CONST (old form) refit          | 10.2% | 28.2% |   12     | 10.8%  | 0.980 vs 1.077 rest |
    | **PHASE (shipped)**             | 7.3% | 20.3% |    2     |  7.8%  | 0.994 vs 1.019 rest |
    | PIECES (live piece count)       | 7.6% | 22.4% |    3     |  8.0%  | 1.008 vs 1.026 rest |
    | PHASE + per-eval piece scaling  | 7.1% | 20.8% |    3     |  7.7%  | 1.002 vs 1.015 rest |
    | PHASE + generate regressor      | 7.2% | 18.8% |    2     |  7.8%  | 0.994 vs 1.019 rest |

Phase beats raw piece count, which is the physically sensible outcome: the
per-node work that actually scales with material is slider ray walks in
`attacked()` and the number of moves a piece emits, and phase (N=B=1, R=2,
Q=4) weights exactly those pieces while ignoring pawns. It is also **free**:
both engines already maintain it incrementally. Scaling the per-EVAL cost by
material as well buys 0.2 points of RMS for a sixth regressor and a second
runtime multiply — not worth it. Adding `generate` fits better still but is
rejected on transfer grounds (below), which is the same reason it was
excluded from the original model.

### Predicted vs actual — shipped model on asm counts (`TestCycleModelFrozen`)

    mask 0x1f grand total  err  -3.90%
    mask 0x07 grand total  err  +4.13%
    mask 0x00 grand total  err  -1.40%
    worst per-search err 20.4%  (a 32-PIECE MIDGAME position, m07)
    actual/predicted: <=12 pieces 0.9934 (33 searches) | >12 pieces 1.0187

Guards in the frozen test: every search within 21%, per-mask grand total
within 8%, and both material ratios within 5% of 1.0. **There is no endgame
carve-out any more** — the old test excluded every FEN starting `8/` from
its per-search check. The worst residual is now a full-material middlegame,
i.e. what is left is not material-structured. The per-search bound is 21%
rather than 20% because the worst row measures 20.4%; that is a strictly
stronger guard than the old "20%, endgames exempt" over a set 3.8× larger
and materially diverse.

## Transfer to the mirror's own counts

The reduced regressor set exists so the asm-fitted costs can be applied to
the MIRROR's operation counts without distortion; `generate`/`attacked` are
excluded because their mirror/asm per-node frequency diverges. The phase
term needs its own transfer argument, because it is charged per node
*multiplied* by the node's material: what has to transfer is the mirror's
mean phase per node.

`TestPhaseTransfers` measures it on all 69 calibration searches: the mirror
reproduces the asm's node count **exactly** on every one of them, and its
mean phase per node matches the asm's to **0.00%**. (The old caveat about
the mirror's quiescence tree being 0.9×–24× the asm's is stale — that was
fixed by the QS-shape and budgeted-ID work; `TestFullWidthParity` now shows
full-width TT-probe parity at 1.00 across all masks.)

## Feature-cost validation — the point of the exercise (`TestEvalTermTax`)

The rook set costs **219 cyc/eval-call** in the asm (`RookTermCost`). Its
share of total cycles, straight from the ground truth:

    mask 0x1f: 219 × 2,661,337 / 11,279,626,078 = 5.17%
    mask 0x07: 219 × 2,099,997 /  9,050,910,334 = 5.08%
    mask 0x00: 219 ×   581,059 /  2,661,888,027 = 4.78%

The mirror reproduces this: with `Extra=RookTermsAsm` and
`Costs.EvalTerm=219`, the term is **4.75%** of the mirror's estimated cycles
at m1f fen0 — inside the 4–6% band the asm shows, and closer to it than
before the recalibration (4.17% against an asm 3.47%). Under a fixed
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

**The Est scale changed with this recalibration.** A midgame node now costs
~15% fewer estimated cycles than under the old table (and a low-material
node far fewer), so a given `CycleBudget` number buys a somewhat longer
search than it did before, and much longer in an endgame. Budgets are A/B
levers, not physical times, so this does not invalidate any past *comparison*
— but a budget constant copied from an older screen is not the same amount
of search it used to be.

## What it changes for screens

Measured on identical trees with only the cost table swapped
(`TestEndgameCosted`): the endgame-technique term's tax, 438 cyc per gated
eval, was **4.84% / 5.05% / 3.94%** of estimated cycles on its three endgame
probe positions under the old table and is **9.62% / 8.59% / 6.02%** under
this one. A feature whose cost falls in low-material positions was being
charged 1.5-2.0x too little, because the denominator — the per-node cost it
is a fraction of — was inflated. Endgame-specialist features will now screen
somewhat worse, and (from `TestBudgetModeParity`) budgeted screens now reach
the same depth the ship does in endgames: 71.8% -> 92.7% exact-depth
agreement, one-sided skew +35/-0 -> -5.

## Known limitations

1. **What the residual still is.** Across the 69 rows the shipped model's
   relative error correlates with material at **r = −0.14** — i.e. essentially
   not at all, which is the whole point of the exercise — and with generation
   intensity (makes per generate call) at only r = +0.21. What is left is
   mostly the per-MASK offsets below plus position-specific noise: the four
   largest residuals are three mask-0x07 middlegames and one mask-0x00
   16-piece ending.
2. **Per-op costs still vary with FEATURES.** `make` is more expensive when
   `FtPstruct` is on (pawnterm runs inside make), and the model is
   deliberately mask-independent, which shows up as the per-mask grand-total
   spread (−3.9% / +4.1% / −1.4%). A per-mask cost table would fit better
   and would make cross-mask comparisons less honest; not done.
3. **Collinearity.** The operation counts are near-proportional, so the
   split between `Node`, `make` and `eval` is not uniquely identifiable
   (hence `Node = 0`). The chosen point is corroborated by the independent
   per-file profile; individual coefficients should still not be read as
   pure per-routine costs.
4. **The material term is per NODE only.** `make` and `eval` also get
   cheaper with less material; the fit says modelling that explicitly buys
   ~0.2 points of RMS, so their material dependence stays absorbed in the
   node term. If eval grows a lot of per-piece work, revisit.
5. **Absolute Est is a mirror-internal quantity.** It is valid for A/B
   budgeting (both sides costed identically) and for feature-cost fractions;
   it is not a prediction of the asm's wall time, and the mirror is one
   search-shape change away from tracking the asm less exactly than it does
   today.

Recalibrate (re-run `TestMicroABPhase`, paste the new rows into `microAB`,
re-run `TestCycleModelFit`, update the `fitted*` constants) whenever the
mirror's search shape or the asm engine change materially. The asm moved
~10% in the 23 commits between the previous calibration and this one, which
is roughly the size of the residual the model is guarded against — so this
is not a once-a-year chore.
