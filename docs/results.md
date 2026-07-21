# Match & measurement log

Newest first. Engine budgets are emulated time (1.0205 MHz); opponent
controls are wall time. See docs/plan.md for the measurement protocol.

## 2026-07-21 — aspiration windows — PORT: +19 to +23 Elo at asm-matched 0x1f, cheap re-search cost

Added **aspiration windows** at the iterative-deepening root of the
node-budgeted search (`internal/mirror/search.go` `aspIterate`, gated by
`AspirationParams`, plumbed through `PlayerCfg` and `cmd/mirror match
-aasp/-basp`). After ID iteration `d` completes with score `s`, iteration
`d+1` opens the window `(s-Delta, s+Delta)` instead of `(-Inf, +Inf)`; a
fail-low/high re-searches with a widened window per policy. The first
iteration, and any iteration seeded by a mate-zone score, always use the
full window, and any fail that returns a mate-zone score re-searches full —
**a narrow window never clips a mate score** (proved by
`TestAspirationMateSound` across all variants + policies). All arithmetic is
16-bit adds/compares plus a shift-doubling of Delta: no floats, no tables
beyond the constant Delta, so it is 6502-portable. A budget abort mid-
re-search leaves `e.aborted` set and the whole iteration is **discarded**
(the last completed result stands), so a re-search can never return a
fail-soft/garbage move.

**This screen is asm-matched from the start** (the lesson from LMP, which
flipped +39→−85 when re-screened off the mirror's strong ordering): both
sides mask **0x1f** (FtAll = null+killer+futil+pstruct+LMR, the five-pass
`moveLoop`, NO SEE/history), recap2 QS, shipped-asm weights, corrected-guard
RFP 120/500, **30 000 nodes/move**, aspiration-on (A) vs aspiration-off (B).
There is no ordering-transfer risk here — this **is** the asm's ordering.

**Field screen (500 games, seed 6502, ±25 Elo):**

| variant   | params (delta,policy)  | +W =D −L        | node-budget Elo |
|-----------|------------------------|-----------------|-----------------|
| d25asym   | 25, asymmetric widen   | +197 =152 −151  | **+32 ± 26**    |
| d25prog   | 25, progressive        | +196 =153 −151  | **+31 ± 25**    |
| d50full   | 50, widen-to-full      | +179 =180 −141  | +26 ± 24        |
| d25full   | 25, widen-to-full      | +195 =133 −172  | +16 ± 26        |
| d15full   | 15, widen-to-full      | +183 =145 −172  | +8 ± 26         |

Reads: within the simple **widen-to-full** policy, a **wider** delta does
better (15→+8, 25→+16, 50→+26) — a tight window fails constantly and each
fail costs a full re-search. **Progressive** (double + re-center, full on
3rd fail) and **asymmetric** (open only the failing bound) at delta 25 match
or beat the best full-widen, because both avoid over-widening: they claw
back the node budget the eager full-widen wastes.

**Promotions to 2000 games (2×1000-pair batches, seeds 11111+22222, ±13):**

| variant | +W =D −L       | score | node-budget Elo |
|---------|----------------|-------|-----------------|
| d25prog | +759 =613 −628 | 53.3% | **+22.8 ± 12.7** |
| d25asym | +740 =628 −632 | 52.7% | **+18.8 ± 12.6** |

Both clear the bar (lower bound > 0). `prog` (+23) edges `asym` (+19) but
they are within each other's error — a statistical tie. The point estimates
regress from the noisy 500g screen (+31/+32 → +23/+19), the expected
shrink, but stay solidly positive.

**Re-search rate & depth probe** (`TestAspirationStats`, 40 self-play games,
per-move `CompletedDepth` and cumulative fail counters, same 0x1f/30k config):

| variant | re-search rate (of windowed iters) | re-search/move | mean depth | median |
|---------|-----------------------------------|----------------|-----------|--------|
| off     | —                                 | —              | 5.45      | 5      |
| d15full | 27.1%                             | 1.29           | 5.47      | 5      |
| d25full | 13.5%                             | 0.61           | 5.28      | 5      |
| d50full | 4.4%                              | 0.21           | 5.40      | 5      |
| d25prog | 17.0%                             | 0.78           | 5.31      | 5      |
| d25asym | 13.5%                             | 0.61           | 5.28      | 5      |

**The mechanism finding, stated straight: aspiration does NOT buy depth
here.** Mean effective depth is flat-to-slightly-negative vs off (median 5
throughout) — at a 30k budget the tree sits near a depth boundary, and the
integer-depth granularity swallows the small node savings; the extra re-
searches even nudge it down a hair. So the **+19 to +23 Elo is a within-
depth accuracy/ordering effect**, not a nodes→depth conversion: the narrow
window produces sharper fail-hard TT bounds that improve next-iteration move
ordering, and the full re-search on a fail-high refines the root PV. The
re-search cost is already paid **inside** the node budget (a re-search
spends real budget nodes), so the measured Elo nets it out — the asm pays
~0.6–0.8 extra root re-searches per move for `prog`/`asym`, and still comes
out +20.

**Port recommendation: PORT aspiration windows, delta = 25 cp.** Between the
two tied winners, **`asym` is the better asm target**: identical delta, one
fewer constant and no per-fail state (on a fail just open the failing bound
to ±Inf — no delta-doubling, no fail counter), for a statistically-tied
+19 vs prog's +23. If the extra ~4 Elo point estimate is wanted and the few
bytes of state are cheap, `prog` (delta 25, double+re-center, full on 3rd
fail) is the alternative. Unlike LMP this transfers with confidence: it was
screened directly under the asm's own 0x1f ordering, so there is no
"strong-ordering artifact" to flip. A cheap, honest +20.

Toggle: `AspirationParams{Delta, Policy}` (Policy = AspFull|AspProgressive|
AspAsym); off (Delta==0) is a **byte-identical no-op** — the parity gates
(`TestSearchMirrorParity`/`TestPStructMirrorParity`) run with it off and
stay green. Soundness: `TestAspirationDeterminism` (same seed → identical
move/score/nodes/fail-counts across all policies), `TestAspirationMateSound`
(exact mate score, no clipping, every variant), `TestAspirationOffIsNoop`
(Delta==0 identical to the pre-aspiration path, zero windows opened).

## 2026-07-21 — mirror-vs-asm divergence at 0x1f: TWO STALE MIRROR DEFAULTS, fixed; parity gates added

Follow-up to the LMP investigation: the mirror's default config did
NOT model the shipped asm. Two independent mirror-side root causes,
both stale defaults (no asm bug, no Zobrist/TT involvement):

1. **DefaultWeights held pre-Texel pawn-structure values** (doubled 12
   /iso 12/old passer table) vs the asm's shipped hybrid (12/7, tuned
   passer table, shield 3−4·open). Logic was correct; constants
   drifted.
2. **Default QS was UNLIMITED** — NewEngine left QSParams zero, whose
   comment falsely claimed it matched the asm; the asm hardcodes
   recap2. This searched QS trees up to ~10× the asm's and was the
   dominant divergence (score flips ≤34cp, occasional best-move flips
   at 0x10).

With both pinned to the asm's shipped behavior, the mirror reproduces
the asm's fixed-depth search essentially byte-for-byte: new gates
TestSearchMirrorParity (masks 0x00/0x07/0x08/0x1f × 5 FENs: 20/20
best move + score, make counts exact except one off-by-1 legality
probe at a fail-hard boundary, tolerance ≤1) and
TestPStructMirrorParity (600/600). `mirror match` -aqs/-bqs now
default to "0,2" (recap2); "0,0" is the explicit unlimited-QS
experiment. Full mirror suite green post-fix.

Consequence: node-budget screens using default config now model the
real engine. Screens that predate this fix and relied on defaults
carry unknown baseline bias (recent screens set QS explicitly; the
0x7f-ordering caveat from the LMP entry below still applies
separately). Design question deferred to a human decision: the asm's
pawn weights are a hybrid (Texel passer/iso, older doubled/shield/
open-file) — finishing the TunedWeights port into the asm would
change asm behavior and needs its own screen + SPRT.

## 2026-07-21 — late move pruning (LMP / movecount pruning) — DO NOT PORT: +39 on mirror ordering, −85 on asm ordering (task #44)

Added **late-move (movecount) pruning** to the mirror's scored search
(`internal/mirror/ordering.go` `orderedMoveLoop`, gated by `LMPParams`,
plumbed through `PlayerCfg` and `cmd/mirror match -almp/-blmp`). At a
shallow (remaining `d <= Dmax`), **non-PV** (zero-window), not-in-check
node whose alpha is outside the mate zone, once the count of
already-searched legal moves reaches `threshold(d) = Base + Mult*d +
Quad*d*d`, the remaining **quiet** moves are skipped. Captures,
promotions, and the TT move are always searched. It is a pure per-move
`LEGALCNT`-vs-constant compare — no tables, no per-node lookup — so it is
6502-portable in spirit. Killers are **not** exempt and checking quiets
**are** pruned (both measured better/neutral; see below).

All matches: **30 000 nodes/move**, vs the adopted baseline (mask **0x7f**
= SEE+history, recap2 QS, history malus, corrected-guard RFP 120/500),
LMP-on vs the identical config LMP-off. `d` = remaining depth. Fixed-depth
node reductions are depth-6 on the same adopted config (`TestLMPSweepNodes`).

**Field screen (500 games, ±26 Elo):**

| variant            | params (dmax,base,mult,quad) | fixed-d6 nodes | node-budget Elo |
|--------------------|------------------------------|----------------|-----------------|
| 3+2d  Dmax3        | 3,3,2,0                      | **−35.6%**     | **+58 ± 26**    |
| 3+2d  Dmax2 keepC  | 2,3,2,0 keepchecks           | −36.5%         | +52 ± 25        |
| 3+2d  Dmax2        | 2,3,2,0                      | −35.7%         | +51 ± 26        |
| 3+d·d Dmax3        | 3,3,0,1                      | −36.9%         | +45 ± 26        |
| 4+2d  Dmax2 (cons) | 2,4,2,0                      | −34.0%         | +35 ± 26        |
| 3+2d  Dmax2 exK    | 2,3,2,0 exemptkillers        | −34.0%         | +35 ± 26        |
| 3+d·d Dmax2        | 2,3,0,1                      | **−41.9%**     | +27 ± 26        |
| 3+2d  Dmax4        | 4,3,2,0                      | —              | +21 ± 26        |
| 2+d   Dmax3 (aggr) | 3,2,1,0                      | −31.5%         | **−72 ± 27**    |

Reads: linear `3+2d` beats quadratic `3+d·d` (quadratic keeps too many
quiets at higher `d`, pruning less where the budget could convert it);
**exempting killers costs ~16 Elo** (+35 vs +51 — do NOT exempt, prune
them like any quiet); **KeepChecks is noise** (+52 vs +51 — use the cheap
pre-make variant, never make a move just to learn it gives check);
extending to **Dmax4 gives gains back** (+21) and the aggressive
low-threshold variant **over-prunes catastrophically** (−72). The most
node-savings (`3+d·d Dmax2`, −41.9%) is NOT the most Elo — past a point
the pruned quiets were carrying real information.

**Promotions to 2000 games (±13 Elo):**

| variant     | +W =D −L        | score | node-budget Elo |
|-------------|-----------------|-------|-----------------|
| 3+2d Dmax3  | +821 =582 −597  | 55.6% | **+39 ± 13**    |
| 3+2d Dmax2  | +789 =571 −640  | 53.7% | +26 ± 13        |

Both clear their bar (>2σ). The point estimate regresses from the 500g
screen (+58/+51 → +39/+26) but stays solidly positive; **Dmax3 > Dmax2**.

**Fixed-depth sanity (depth 6, 400 games):** LMP `3+2d Dmax3` vs off =
**−100 ± 28** while the tree is **−35.6%** smaller. This is the task-#42
conversion in miniature: fixed depth *penalizes* a node-saver (same
depth, fewer moves searched = pure accuracy loss), and the budget flips
it to +39. Not catastrophic, and exactly the expected sign — fixed depth
is a floor, not the verdict, for this feature class.

**Futility interaction (does LMP pay on top of RFP/futility, or is it
subsumed?):** they overlap at `d <= 2` (adopted RFP is MaxRem 2).

| test                                          | Elo (500g) | reading                                  |
|-----------------------------------------------|------------|------------------------------------------|
| LMP on top of adopted futility (main screen)  | +39 ± 13   | LMP **pays on top of** futility          |
| (no-futil + LMP) vs adopted (futil, no LMP)   | −17 ± 26   | LMP does **not replace** futility        |
| (no-futil + LMP) vs (no-futil, no LMP)        | +48 ± 27   | LMP standalone ≈ same size as with futil |

**Verdict: complementary, NOT subsumed.** LMP is a *count*-based prune;
RFP/futility is a *value*-based prune — they cut different nodes. LMP
still delivers +39 on top of the adopted futility config, dropping
futility loses ~65 Elo that LMP only partly (+48) recovers, so keep
**both**. This is the honest opposite of the checks-in-QS subsumption
outcome: here the new feature is additive.

**THE ASM-MATCHED RE-SCREEN — and it FLIPS (do NOT port).** Everything
above used mask 0x7f (SEE + history + malus ordering), which only the
mirror carries — the real asm engine has NO SEE and NO history: its
ordering is TT + killers + two-tier MVV (mask 0x1f, the five-pass
`moveLoop`; SEE/history were tried in asm and rejected). LMP prunes by
move *count*, so its value rides on ordering quality — with weaker
ordering, late quiets more often hide a good move. Re-screened the
recommended variant with LMP wired into the five-pass path (commit adding
LMP to `moveLoop`), both sides mask 0x1f, recap2, RFP 120/500, 30k
nodes/move:

| config (both sides mask 0x1f) | fixed-d6 nodes | node-budget Elo (2000g) |
|-------------------------------|----------------|-------------------------|
| LMP 3+2d Dmax3 ON vs OFF      | **−24.4%**     | **−85 ± 14**            |

**Verdict: LMP does NOT transfer. The +39 was an artifact of the mirror's
strong ordering.** Under the asm's actual ordering the same variant is
**−85 Elo** — a full sign flip, not a shrink. The −24.4% node saving is
real but does not convert: the quiets LMP skips at count 5/7/9 are, under
TT+killers+MVV ordering, still carrying enough real value that losing them
outweighs the extra depth the saved nodes buy. This is the transfer risk
the 0x7f screen left open, and it fired.

**Port recommendation: DO NOT PORT LMP to the current asm engine.** The
feature is a clear win only *given* SEE+history-quality move ordering,
which the asm rejected. Two honest futures: (1) if the asm ordering is
ever upgraded (SEE/history revisited and adopted), re-open LMP — it is
worth +39 there; (2) otherwise LMP stays a mirror-only feature. A
last-ditch asm variant — much larger thresholds so only truly hopeless
tails are pruned — might claw back to neutral, but the −85 at the tested
(already moderate) thresholds makes a net win under 0x1f-ordering
unlikely; not worth an SPRT slot now. Recorded straight: a node-saver
that shrinks the tree 24% and still loses 85 Elo, because move ordering,
not node count, was doing the real work.

## 2026-07-21 — mirror gains a calibrated CYCLE-budget mode (screens now taxed by cost)

The fix for the flat-tax blind spot the rook rejection exposed: the
mirror now maintains an estimated-6502-cycles counter, bumped per
operation (node by type, make by move class, eval base + configurable
per-term surcharge, TT probe), with `PlayerCfg.CycleBudget` as a
drop-in alternative to `NodeBudget` (same soft-stop semantics; node
budgets unchanged). Coefficients were fit against the asm's measured
MicroAB cycles (per-mask grand-total error ≤ ~12%; full table and
limitations in internal/mirror/cycles.md). Validation on the known
case: a 219 cyc/call eval term reproduces a 4.17% tax vs the asm's
measured 3.5–5%, and under a fixed cycle budget provably buys fewer
nodes — a node budget sees zero difference, which is exactly the
mistake this closes. Feature screens from here on: cheap features may
still use node budgets; anything with per-node cost uses CycleBudget
with a pessimistic cost estimate until the asm exists.

Honest limitation recorded: the mirror's QS tree is 0.9–24× the asm's
by position (full-width trees match; asm QS prunes harder), so mirror
cycle predictions are self-consistent virtual cycles, not absolute asm
cycles — fractions/taxes transfer, absolute totals don't.

## 2026-07-21 — FT_ROOKX post-mortem audit: NO BUG; verdict stands, cost restated 4.5–6%

A pure eval-shape feature should transfer mirror→asm nearly 1:1 at
equal nodes, so the ~41-Elo shortfall (+22 expected, −19 measured)
justified an adversarial deep-audit of the whole chain. Result: **no
defect anywhere it could hide.**

- **Harness live**: the sprt mask plumbing was traced end-to-end
  (fresh machine per move, FEATURES poked after image load, SMC
  re-patched per run) and exercised directly at the real 30000ms
  budget: 0x3F vs 0x1F produce different moves/scores with plausible
  rook-term deltas on rook-heavy FENs. The SPRT was not
  engine-vs-itself.
- **Coverage gaps closed with new oracle tests** (worktree
  internal/chesstest/rookx_audit_test.go): QS hash-elided fast paths
  maintain RKBITS/WPASSM; castling all four wings, rook promotions +
  captures of promoted rooks (300K+ makes), ep×passer-mask interplay,
  null move — all node-count/score identical to fresh-recompute
  oracles. Scratch audit: no eval call site holds T2/T3 across eval
  (clobber comment updated).
- **Off side verified cross-binary for the first time**: the task52b
  binary at masks 0x1F/0x07/0x00 is fingerprint-identical to main's
  binary; side B was exactly main's engine (+1.19% cycles, paid by
  both sides).
- **Real cost restated**: 4.4–4.6% midgame, **4.5–6% in rook-heavy
  endgames** (the 3.97% figure omitted SMC save/restore and
  position-dependence) — cost concentrates in exactly the positions
  where the terms fire. Expected value drops from +22 to ≈ +18–20.

Residual gap ≈ 2σ combined: the documented mirror-NB→asm compression
pattern plus noise. Loose ends recorded, not pursued: the mirror-side
+30±18 measurement config wasn't re-verified, and 300 games can't
distinguish "true ≈ 0" from "true ≈ −19" — moot unless a Texel retune
of the four (hand-guessed, never-tuned) weights raises the mirror
number substantially, which is the only revisit path worth taking.

## 2026-07-20 — FT_ROOKX verdict: REJECTED at −19 ± 33 (the process working as intended)

The rook-file/blockade eval set got the full escalation ladder before
its verdict: mirror screen (+26±14 FD / +30±18 NB as a set), exact-
parity asm port (task #51, ~34% whole-search cost), incremental rook
bitmasks (task #52, →16.8%), then a deep-optimization pass on the
blockade term (task #52b: cached passer-file masks, blockade 737→30
cyc/call, extraterm 930→219, feature cost →+3.97%; OFF-path tax +1.19%
combined). Only THEN the deciding asm time-budget SPRT (300 games,
FT_ROOKX on vs off, same binary, 30s emulated/move):

**+97 =90 −113 = 47.3% → −19 ± 33 Elo, llr(0,10) −0.84. REJECTED.**

The mirror's node-budget number was once again an optimistic upper
bound (history +56→−16, LMR +15→−5, now rook terms +30→−19): even
eval-shape features compress against the asm's time-budget dynamics.
Disposition: task51's gated-off scan port stays on main (costs only
the feature-bit test; #31 feature audit may strip it); the task52/52b
maintenance layers (+1.19% OFF-path tax) stay UNMERGED on their
branches, which hold the full verified implementation + oracles for
any future weight-retune revisit.

## 2026-07-20 — Sargon III gauntlet #2 (post-optimization): PARITY at 1.5× time

Same fair conditions as the morning's −117 gauntlet — 40 games, varied
openings via setboard (no book for either side), Sargon at 1.5×
pondering-equivalent time — but with the day's optimized engine
(aee43fd stack + FT_ROOKX off) and the fixed promotion driver, plus
the new screen-dump audit on every adapter-judgment ending.

Raw cutechess: 18W-18L-4D (50.0%). **Audited reclassification:
15W-18L-7D = 46.25% ≈ −26 ± ~100 Elo — statistical parity.**

Audit (the point of the screen dumps): exactly 3 adapter-judgment
endings, all "MY MOVE DRAWS BY REPETITION" at material +0 — in two,
WE held perpetual check (knight-check shuttles) and Sargon declared
the draw to accept it; in one, Sargon held the perpetual. All genuine
draws. Zero promotion/boot/panic failures — the driver fixes held.
All other endings were cutechess-native (mates, adjudications).

Trajectory: −117 (morning, pre-optimization) → −26 ± 100 (tonight)
after +92 ± 33 of measured self-play gain — the speed-derived strength
carried to a foreign opponent essentially undiminished. Next levers
for a decisive lead: checks-in-QS (#37 — notably, three games ended
in perpetual-check draws, the exact motif QS check awareness
addresses), the FT_ROOKX eval set (pending its cost optimization +
SPRT), and the remaining documented micro-scraps.

## 2026-07-20 — FULL-STACK speedup Elo conversion: +92 ± 33 (300 games)

The headline measurement of the day's deep-optimization campaign:
round-2+3 binary (49129bd, −39.5% cycles = ×1.65 speed) vs the
pre-round-2 morning baseline (5573b65), both full-featured at equal
30s-emulated/move budgets, 150 opening pairs, two-binary sprt mode.

**+138 =102 −60 = 63.0% → +92 ± 33 Elo, llr(0,10) 3.58.**

Together with the round-2-only point (+35 ± 32 at ×1.17), this puts
the engine's speed→Elo conversion at roughly 130–150 Elo per speed
doubling — well above the classic ~70/doubling, as expected at shallow
6502 depths where each extra ply is worth a lot. Identical-tree cycle
reduction is now the project's best-proven strength lever end to end:
cycles → (measured) → Elo.

Note: the emit-fusion (−1.92%, merged after this match started) and
the FT_ROOKX port are not in the measured binary; current main is
slightly ahead of the +92 point.

## 2026-07-20 — deep optimization review: emit-interface fusion, −1.92% total cycles at identical trees (task #50)

The emit-interface fusions the round-3 movegen/search reviews designed
but could not build (they did not own both sides of the emit boundary).
Every emit call site in the unrolled generator already holds the target
square in X and BOARD[target] (the victim byte) in Y at the instant it
emits; the old emitmove re-derived both from GTO with a board read plus
a `stx GTO` at each call site. New register convention:

  emitmove   quiet + normal capture (flags == 0): X = to, Y = victim.
             No flags arg; stores flags 0, reads tier straight off Y.
  emitmovef  ep / promo / double / castle: A = flags, X = to, Y = victim.

Both write `to` via `txa`/`sta (MSP),y` (STX has no indirect-indexed
mode), which inherently PRESERVES X — genrecap keeps its slot index
there, promoloop/pawn/double keep the target square there. GTO drops
out of every hot path (genrecap keeps its own copy only for the
promotion-rank test). Splitting off the flags == 0 fast path is what
pays: it needs no flags save, so the hot quiet/capture emission fell
from ~78 cyc (incl. call-site `stx GTO`/`lda #0`) to ~61 — −17 cyc/emit.
emitmovef carries a stack save/restore of the flags across the tier
lookup (+7 cyc) but only ep/promo/double/castle pay it. genrecap's rare
emit points stash the slot in GSLOT (free there) and reload to/victim.

Rejected: **change 3 (SMC absolute move-stack base).** `sta BASE,y`
(5 cyc) vs `sta (MSP),y` (6) saves 4 cyc/emit on the four stores, but
the base operand still needs the same 4-byte advance per emit, the
abs,y stores take the page-cross penalty across the $0E00–$1FFF stack,
and it puts self-modifying code in the hottest leaf — all for a residual
~0.3–0.5% after the fast path already took the big cut. Not worth the
risk; skipped per the "<0.3% + risk → drop" bar.

MicroAB fixed-depth suite (18 cases, masks 1f/07/00): every search/make/
eval/attacked/ttprobe/generate count, score, and best move byte-identical
to baseline (49129bd); cycles 4,368,733,938 → 4,284,733,466 = **−1.92%**.
Full non-short suite green: RecapGenEquivalence (1280) + RecapGenTree
Identity, Perft, LegalityTorture, GiveCheckVerify, HashConsumptionExact
(60,345 pts), HashConsistency/ElisionCoverage, PStructParity (4045),
PTCache IdenticalTree + RandomWalk (4060), Attacked Diff/Distribution,
WAC, TreeSize, Mates, IterativeDeepening, BankedBuild. engine.bin md5
aee43fd592c321413f54df1a1241f1ea (256 bytes smaller).
## 2026-07-20 — selective checks-in-QS screen (task #37): DO NOT PORT

Schröder-style quiet checks in quiescence, screened in the mirror with
the node-budgeted self-play rig (the task #42 protocol; mirror wins are
an optimistic upper bound on asm reality). Implementation:
`QSParams.Checks` — at the first N qs plies, generate quiet CHECKING
moves in addition to captures/promotions, as a dedicated pass 5 after
the capture passes; the child of a quiet check is the existing full
in-check evasion node (all evasions + mate detection), which the QS
already had. Gives-check reuses make()'s per-child `curInCheck()` flag —
no new movegen path — and was cross-checked node-exact against refchess
over 40 random-walk games / ~4000 checking moves
(`TestGivesCheckVsRefchess`). Optional `SafeChecks` gate: skip a check
whose destination is attacked by an enemy pawn. `Checks=0` reproduces
the shipped captures-only QS bit-for-bit (perft/WAC/budget tests
unchanged). CLI: `-aqs/-bqs plycap,recapafter[,checks[,safechecks]]`.

**Node shape (depth 6, bench FENs; baseline QS = 87% of all nodes):**

| variant         | total nodes | QS nodes | quiet-check moves searched |
|-----------------|-------------|----------|----------------------------|
| checks1 (N=1)   | +10.2%      | +12.5%   | 3% of QS nodes             |
| checks2 (N=2)   | +38.6%      | +44.9%   | 7% of QS nodes             |
| checks1+safe    | +7.6%       | +9.5%    | 2% of QS nodes             |
| recap2+checks1  | −17.7%      | −20.4%   | 5% of QS nodes (vs recap2 alone: +9.6% total) |

**Node-budget Elo (250 pairs = 500 games per cell, candidate POV):**

| candidate vs opponent        | 10k        | 30k        | 100k       |
|------------------------------|------------|------------|------------|
| checks1 vs base (uncapped)   | −9 ± 25    | +29 ± 26   | +22 ± 25   |
| checks2 vs base              | —          | −5 ± 25    | —          |
| checks1+safe vs base         | —          | +22 ± 26   | —          |
| recap2+checks1 vs recap2     | −10 ± 26   | −1 ± 26    | −17 ± 25   |
| recap2+checks1+safe vs recap2| —          | +3 ± 26    | —          |

Seed-777 confirmation of the decisive pairing (30k):
recap2+checks1 = **−8 ± 26**. Aggregates: checks1 vs unshaped base
**+14 ± 15** (1500 games — weakly positive, does not clear zero);
recap2+checks1 vs recap2 **−9 ± 13** (2000 games — neutral-to-negative,
CI top +4). Secondary signals: depth-3 WAC subset unchanged (5/7 for
caps-only, checks1, and checks2).

**Verdict: clear "not worth it" — do NOT port.** The engine's adopted
QS shape is recap2 (+30 under budget, task #42); on top of it, quiet
checks are ≤ 0 at every budget tested even in the mirror's optimistic
rig. The coherent story: checks1's tactical coverage is worth roughly
its +10% node cost against an unshaped QS, but recap2's −25% node
saving converts to depth that finds the same tactics one iteration
deeper, leaving nothing for check generation to add. On the 6502 the
case is strictly worse: mirror gives-check is a free byproduct of the
Go make(), but the asm would pay a per-quiet-move gives-check test (an
attack scan or difference-table extension) inside the hottest loop to
buy Elo that is negative in the best case measured here. Branch
`task37-qschecks` keeps the implementation and the refchess-verified
gives-check cross-check for any future revisit (e.g. if the QS shape
ever changes away from recap2).

## 2026-07-20 — round-2 speedup Elo conversion: +35 ± 32 (300 games)

First measurement of cycles→Elo on the asm engine using the new
two-binary self-play mode (`cmd/sprt -binB/-defsB`; each side runs its
own binary + memory-layout defs, per-side aux TT carryover): round-2
binary (b5a3cfe, −14.7% cycles) vs pre-round-2 baseline (5573b65),
both full-featured at equal 30s-emulated/move budgets, 150 opening
pairs.

**+113 =104 −83 = 55.0% → +35 ± 32 Elo.** The −14.7% cycle reduction
(×1.17 speed) converted at or above the classic ~70-Elo-per-doubling
rate. This is the direct experimental confirmation that identical-tree
speedups convert to strength under the time budget — the lever the
deep optimization reviews are pulling. A second match measuring the
full round-2+3 stack (−39.5%, ×1.65) is running.

## 2026-07-20 — deep optimization review round 3 COMPLETE: −29.1% total cycles at identical trees (task #49)

Five parallel per-routine reviews, each with license to renegotiate
contracts (register conventions, call boundaries, data layouts), each
proven tree-identical by the MicroAB fingerprint (make count + score +
best move over 18 fixed searches) plus per-cluster oracles:

| cluster | change (headline) | alone vs ba8a940 |
|---|---|---|
| search/TT | 4-byte tier-tagged moves, per-pass scan loops, ZP cursor, TT probe early-bail | −8.87% |
| QS movegen | unrolled rays w/ carry-invariant stepping, unrolled steps/pawns/promo, king-first dispatch | −7.89% |
| eval | pawnterm file walk fully unrolled, king-shield count+lookup, quarter-squares taper multiply (exhaustively proven), toggle inlining | −5.66% |
| make/unmake | flag-free fast paths, hash-xor unrolls, QS HASH ELISION (HVALID watermark + deferred hashcatchup) | −6.30% |
| attacked() | unrolled slot scan, borrow-as-tombstone filter; superpiece rewrite REJECTED on measured operand distribution (90,512 states) | −1.57% |

**Union (all five merged): 6,159,590,974 → 4,368,733,938 = −29.1%**,
slightly better than naive compounding (−27.4%). Integration resolved
two cross-branch collisions: mvppawn/tkppawn inlining (kept the
make-branch variant whose FROM-toggle-last preserves the new Y=FROM
mvpbody entry contract) and an HVALID/SENDL zero-page collision
(HVALID relocated $05→$07).

Union battery all green: MicroAB, HashConsumptionExact (60,345
consumption points, Go-side oracle at every ttprobe/ttstore/rep-scan),
HashElisionCoverage, HashConsistency, PStructParity (4045), PTCache
IdenticalTree + RandomWalk (4060), RecapGenEquivalence (1280),
LegalityTorture, GiveCheckVerify, WAC (6/7 unchanged), full short
suite. engine.bin md5 49129bd3d492f9e3f062aba0f40e0dea.

**Cumulative day total (rounds 2+3): −39.5% of all search cycles —
1.65× faster — at provably identical search trees throughout.**

Post-round-3 profile is flat (top label 3.5–5.7%): generateq residual,
emtier+emitmove (~4–5%, the designed emit-interface fusions — Y=victim
argument, X=to + SMC stack base — are the remaining ~1–1.5%), make/
unmake residuals. Diminishing returns; next leverage is features
(checks-in-QS #37) and eval terms, not more cycle-shaving.

## 2026-07-20 — deep optimization review round 3, search/TT cluster: −8.87% total cycles at identical trees (task #49)

Move-storage format change: moves are now 4 bytes — (tier, from, to,
flags) — with the tier byte computed once by emitmove at generation
time (victimtype<<4 | class; class 1 = heavy capture/promo, 2 = light
capture, 4 = quiet, $00 = TT move consumed by pass 0). The search's
five ordering passes each run a specialized scan loop with the cursor
ZP-resident (CURPTR + SENDL/H, persisted per-ply only around
recursion), so skipping a wrong-pass move costs ~33 cycles instead of
the old ~120-140 (sloop+sfetch+snotp0+snotttm+classify). Pass 0 marks
the TT move's tier $00 when it searches it, which deletes the per-fetch
4-way TT-move compare (snotttm, ~2.2% of all cycles) outright. The QS
delta filter reads victim value straight off the tier byte (VV16
tables) instead of re-deriving it from the board.

MicroAB fixed-depth suite (18 cases, masks 1f/07/00): every
search/make/eval/attacked/ttprobe/generate count, score, and best move
identical to baseline; cycles 6,159.6M → 5,613.4M (−8.87%; per mask:
1f −8.6%, 07 −9.2%, 00 −8.9%). Move-loop cluster share (2-FEN budget
profile): ~13.5% → ~5.2%; emitmove grew 3.7% → ~5.0% as designed
(+23 cyc/emit buys the tier byte). Also: ttprobe now verifies the hash
inside the LC-resident aux reader and bails before copying on a miss;
pure-QS nodes skip node-init stores only full-width/evasion nodes read.

Consumers updated for the format: perft.s iteration, recapgen/debug
test readers. Move-stack capacity now 1151 moves (peak measured usage
~1.3K bytes of 4.5K; emitmove's exit-100 overflow trap unchanged).
One contract note: emitmove must preserve X (genrecap keeps its slot
index there) — the tier lookup runs through Y.

## 2026-07-20 — deep optimization review round 2 COMPLETE: −14.7% total cycles at identical trees (tasks #46/#47/#48 + micro pass)

The full round-2 batch, every change proven tree-identical (same best
moves, scores, and make counts; only cycles drop):

| change | measured (alone, vs its baseline) |
|---|---|
| attacker-driven recapture generator (#46) | −6.03% |
| move-loop fetch + attack-scan micro pass | −1.28% |
| incremental pawn bitmasks: ptscan eliminated (#48) | −8.07% |
| **compounded batch (5573b65 → b5a3cfe)** | **≈ −14.7%** |

#47 (pawn-structure cache) turned out to ALREADY exist since M5a; its
deliverable became the PTNOCACHE oracle build + differential harness
that now proves the whole pawn-eval caching stack (cache + maintained
bitmasks) bit-identical and worth 15.5% vs fresh recomputation.

#48 design: XOR reverse-delta toggles through one pbtoggle helper
(39 cyc/toggle; self-inverse, so unmake re-applies the same toggles —
no ordering hazards for pawn-takes-pawn or ep). Non-pawn nodes pay +2
cyc in make, +21 in unmake. Pawn ENDGAMES improve most (−11.0%),
fixing the one case #47 had measured as cache-negative. New persistent
state: PWBITS/PBBITS at MAIN $0200–$020F (contract documented at
PDIRTY in defs.inc).

Combined binary verified end-to-end: TestMicroAB fingerprints,
TestPStructParity (4045 pos), TestPTCacheRandomWalk (4060 pos),
TestPTCacheIdenticalTree (9 FENs), TestRecapGenEquivalence (1280
states), full short suite — all green. engine.bin md5
b5a3cfeb2004376965e4c06aa69ff919.

Post-batch profile: generateq full-QS mode 12–19%, pawnterm per-file
residual ~8–9%, search move-loop/TT ~8%, make/unmake ~5%, attacked()
~3–4%, eval taper ~3% → the round-3 per-routine sweep target list.

## 2026-07-20 — deep optimization review round 2, part 1: attacker-driven recapture generator (task #46)

Profiling the shipped engine (recap2 era) showed RECAPONLY quiescence
plies consuming 27–46% of ALL search cycles, with `generateq` alone at
7.5–15.4% of total — because recapture-only nodes ran the FULL move
generator and discarded every emission not landing on RECAPSQ.

Replaced with `genrecap`: an attacker-driven generator that scans the
16 piece slots in ascending order and asks "does this piece capture on
RECAPSQ?" via the attacked()-style diff → ATTACKTAB → TYPEATK2 test
plus a colinear-terminated slider ray walk. Promotion-recaptures emit
N/B/R/Q via promoloop as before. The qemit filter and EMITJSR/EMITJMP
macros are gone (non-recap QS emissions also shed the filter call).

Verification (independent, two ways, re-run by the integrator):
- Set/order equality: 20 positions × 64 squares = 1280 states, new
  generator vs full-generator-filtered — 0 mismatches, order included.
- Tree identity: old vs new binaries at fixed d5/d6 over 6 positions,
  hashing (FROM,TO,MVFLAGS) at every make — all IDENTICAL.

Measured: **−6.03% total search cycles suite-wide** (−0.0% quiet
endgame → −9.5% tactical middlegame) at provably identical trees; the
recapture-generation slice fell from 7.5–15.4% of total to 0.7–1.6%.
Pure speedup → depth under the time budget; no SPRT required.
Commit 1533f2c, engine.bin md5 23d17cc1347e6c4c4ff82a3a2877b7ef.

Post-merge profile: remaining hot spots are the pawn-structure eval
cluster (pt*, ~13–15%) and full-QS-mode generateq (11–16.7%).

## 2026-07-20 — Sargon driver: promotion submission fixed (task #45)

Root cause of the two gauntlet harness deaths: Sargon does not promote
on the FROM-TO RETURN — it shows "ENTER PROMOTED PIECE" and blocks.
The driver now types FROM-TO, waits for the prompt, and answers it
(RETURN accepts the Queen default; N/R/B + RETURN under-promotes; the
letter typed inside the FROM-TO field is rejected as INVALID MOVE).
The prompt itself is the accept signal — post-promotion the $60-$7F
piece list is Sargon's search scratch, so board-polling misfired (the
"screen: CHECK" death). Decode of Sargon's own promotions ("/Q" etc.)
was audited correct. Regression tests cover Q/N/R promotions both
colors, promotion-with-check, and Sargon-side decode; full driver
suite green. Commit f81d8e9.

## 2026-07-20 — FAIR Sargon III gauntlet: varied openings, Sargon at pondering-equivalent time (task #44 partial)

The first *fair* Sargon III benchmark: 40 games (20 openings × both
colors) from the varied-openings pool via the validated setboard path,
our engine (36bdaa7 = corrected futility + recap2) at 30M cyc/move,
**Sargon at budget-multiplier 1.5×** (pondering-equivalent — Sargon
thinks on 45M cyc/move). This removes the two big unfairnesses of the
first gauntlet: Sargon's opening book no longer applies (varied
positions), and Sargon is no longer handicapped to Easy-mode time.

Raw cutechess: 14W-22L-4D (40%, −70 ± 108). **Honest reclassification**
(5 of our 7 resignation "wins" were SARGON-DECLARED-DRAW — Sargon
declaring repetition; the adapter must resign to end the game):

**9W-22L-9D = 33.8% ≈ −117 Elo vs Sargon III at 1.5× time.**

Caveats, both directions:
- 2 of our remaining wins ended on a **driver bug: promotion moves
  (c2c1q, f2f1n) failed to submit into Sargon** ("no reply after
  CTRL-T" / "not accepted, screen: CHECK"). We were promoting, so
  likely winning anyway, but the games didn't finish over the board.
  Driver promotion entry needs fixing before the next gauntlet.
- 14 of our 22 losses were material adjudications (down material,
  legitimate).

Verdict: at matched-to-1.5× compute with no book skew, **Sargon III is
still ~100–120 Elo ahead**. Combined with the earlier even result vs
Easy-mode Sargon at 1×, the picture is consistent: we are in Sargon's
neighborhood but below its full pondering strength. The lever that
converts (recap2: −74% QS cycles → +9.9) says speed→depth is the path;
ordering micro-tweaks are not (history, LMR ports washed on the asm).
Per protocol this stays a periodic milestone, not an inner-loop gate.

## 2026-07-20 — node-budgeted self-play + the conversion re-measurements (task #42)

Built a **node-budgeted self-play mode** in the mirror
(`internal/mirror/search.go` `SearchBudget`, wired through
`PlayerCfg.NodeBudget` and `cmd/mirror match -budget`). Each move runs
iterative deepening, reusing killers/history/TT across iterations,
spending up to a per-move NODE budget, then plays the best move from the
last COMPLETED iteration (start a new iteration only under ~50% budget; a
hard cap at the budget aborts an in-flight iteration whose partial result
is discarded; depth 1 always completes so a move is always produced).
Because the cap is denominated in **nodes, never wall time**, a game is a
pure deterministic function of (position, budget, features, dither seed)
— A/B replays bit-identically (`TestBudgetDeterminism`). Also fixed a
latent A/B-cleanliness hole: `Match` now seeds dither **per pair**, not
per worker, so a match result is reproducible regardless of worker-pool
scheduling (`TestBudgetMatchRuns`, 1 worker == 4 workers). Fixed-depth
mode is untouched.

**WHY this mode exists:** fixed-depth self-play is structurally blind to
node-saving features — ordering/LMR/futility/QS-shaping change *which*
node cuts, not the minimax value, so they read neutral at fixed depth.
Under a budget the saved nodes buy extra search depth = real Elo. This is
the conversion the asm futility port already showed (~+47 Elo LOS 91%
time-budgeted vs the mirror's +4 at fixed depth). Now the mirror sees it.

All matches below: **30 000 nodes/move, 250 pairs = 500 games**, vs the
FtAll (0x1f) baseline unless noted; fixed-depth numbers are the task #35
/ #29 depth-6 measurements for the same feature.

**(a) Move-ordering enablers — the node savings DO convert to Elo:**

| enabler (node saving)      | fixed-depth Elo | node-budget Elo (500g) |
|----------------------------|-----------------|------------------------|
| SEE (−24%)                 | +0 ± 31         | **+31 ± 26**           |
| history+malus (−42%)       | +8 ± 30         | **+56 ± 25**           |
| SEE+history (−46%)         | −2 ± 31         | **+64 ± 25**           |

Verdict: **confirmed, and large.** The ~24–46% smaller tree is not free-
but-neutral as fixed depth implied — under a budget it is worth
+31/+56/+64 Elo. This is the number we'd been inferring but could not
measure. History carries most of it (the deeper the reuse across ID
iterations, the more its quiet ordering compounds). These are now the
clear asm port targets for the "land wins in the 6502 code" goal.

**(b) QS recap2 (#29, −21% nodes):** fixed-depth neutral → **+30 ± 25**
under budget. Verdict: **confirmed** — the cheaper quiescence (recaptures-
only past qs-ply 2) spends its saving on depth for +30 Elo. Worth porting.

**(c) THE STRESS TEST — aggressive LMR under a node budget (the #35
falsification re-test).** At FIXED depth, with STRONG ordering
(SEE+history+malus, 0x7f) held identical on both sides, aggressive LMR
was *worse* (the negative ordering×LMR coupling). Re-run under the node
budget, strong ordering both sides, aggressive-LMR side vs default-LMR
(4,7,3,5) side:

| aggressive LMR   | fixed-depth (strong ord) | node-budget (strong ord)        |
|------------------|--------------------------|---------------------------------|
| late 3,6 (3,6,3,5) | −20 (6502), −15 (777)  | **+15 ± 25 (6502), +3 ± 25 (777)** |
| rem1=2 (4,7,2,5)   | −62 (6502), −41 (777)  | **−5 ± 25 (6502), −6 ± 24 (777)**  |

**Verdict: the negative coupling SOFTENS sharply — for one variant it
FLIPS.** rem1=2 goes from strongly negative (−62/−41) to essentially
neutral (−5/−6): the extra depth its saved nodes buy almost exactly
cancels the reduction cost that fixed depth counted as pure loss, but no
net gain — lowering the min-remaining floor is a wash, **do NOT port it.**
The 3,6,3,5 variant (lower the late-move reduction *rank* thresholds)
flips from −20/−15 to **+15/+3** (avg ≈ +9, one seed clears its bar): the
saved-nodes-into-depth trade is net-positive here. So fixed-depth
measurement was **directionally wrong** about aggressive LMR — it is not
uniformly harmful under a budget; it depends on WHICH knob.

**LMR port recommendation (gates task #41):** carry **3,6,3,5** (late1=3,
late2=6) — not rem1=2 — into the asm as the LMR-change candidate for a
TIME-budgeted SPRT, which is the real gate. Expected ≈ +3…+15 Elo; the
mirror can no longer call it neutral/negative. Skip rem1=2. (Not run here
to free cores: a larger-budget robustness sweep and more seeds — nice-to-
have if the asm SPRT is ambiguous, not required.)

**Methodological upshot:** every node-saving feature we'd shelved as
"fixed-depth neutral" (ordering, recap2) is in fact a +30…+64 Elo win
under a budget, and at least one "fixed-depth negative" (3,6,3,5 LMR)
is actually positive. Fixed-depth Elo is a floor, not the verdict, for
this whole feature class; the budget mode is now the mirror's primary
A/B rig for them.

## 2026-07-20 — FIRST benchmark vs Sargon III at matched 30M-cyc/move (task #36) — ROUGHLY EVEN (preliminary)

Our first head-to-head against the historical bar: our engine vs **Sargon III**
(1983, Spracklens), **both sides ~30M 6502 cycles/move** — our standing ~30 s
budget. This is a *benchmark vs Sargon III at matched compute*, NOT a calibrated
Elo.

Method: Sargon is driven headless in goapple2 (`internal/sargon`,
`cmd/sargon-xboard`, `runs/sargon-match.sh`). Fair per-move compute via
**Infinite level (SHIFT-9) + CTRL-T after exactly 30M cycles** (`RequestMove`);
our engine `-budget 29411` ms (≈30.6M cyc) matches. cutechess-cli, color-paired
(Sargon alternates W/B via the CTRL-S path), sequential, from the **standard
start** (see caveat), our engine `-dither` for opening variety.

Result (first **10** games; the detached 40-game batch continues and its final
tally supersedes this): raw cutechess **us 8–2**, but 7 of those "wins" are
Sargon-declared repetition draws the adapter has to resign (see caveat) —
**reclassified: us 1 W – 2 L – 7 D over 10 = ~45% (≈ −35 Elo, CI huge at n=10)**.
Essentially even; **draw-heavy (~70%)**; decisive games 1–2 to Sargon.

**Verdict: our engine is COMPETITIVE with the ~1550–1600 Sargon III bar at
matched 30M-cyc/move** — a strong first benchmark. Read with three caveats
(smaller effect than it looks): (1) small sample, run continuing to 40; (2)
**CTRL-T interrupts Sargon mid-search-iteration**, playing its best-move-so-far
rather than a completed iteration, which likely *understates* Sargon's natural
strength — the number is a floor on Sargon, a ceiling on us; (3) opening variety
is our-engine dither from the start position, not `openings-pool.epd`.

Known limitations / follow-ups (all in docs/sargon.md): **setboard doesn't work**
— Sargon reconstructs its board from its on-screen move list, so poking the
$60–$7F piece list reverts; the opening pool needs Sargon master-board RE.
**Repetition draws**: Sargon declares a 3-fold one ply before cutechess counts
it, so a "1/2-1/2" claim is rejected and deadlocks; the adapter resigns instead
and logs `SARGON-DECLARED-DRAW`, reclassified here as draws. A cleaner match
wants those two fixed (real draw results) and, ideally, a natural-time-control
cross-check (Sargon SHIFT-3 = 30 s/move vs our matched banked clock) to bound
the CTRL-T interruption effect.

## 2026-07-20 — move-ordering enablers + the ordering×pruning coupling test (task #35)

Built the three ordering enablers in the mirror (internal/mirror/
ordering.go, behind FtSEE=0x20 / FtHistory=0x40, NOT in FtAll so single-
toggle A/B measures exactly the ordering change): SEE capture ordering
(full swap-off with x-ray reveal, TestSEE-validated), butterfly from/to
history for quiets (depth² bonus, optional gravity malus, per-move
decay), and a unified scored/sorted full-width move loop (TT-move-first
verified reliable). Soundness gate: with the heuristic pruners off (pure
fail-hard αβ) reordering is minimax-invariant across the bench set
(TestOrderingScoreParity). Baseline and the QS path are untouched.

**Depth-6 node counts vs the FtAll baseline (766,536; TestOrderingNodes):**

| ordering variant           | nodes    |
|----------------------------|----------|
| SEE                        | −23.9%   |
| SEE + losing-caps-last     | −38.0%   |
| history (+malus)           | −41.9% (−45.3%) |
| SEE + history              | −46.4%   |
| SEE + history + malus + LL | **−59.9%** |

Ordering front-loads cutoffs onto the first move — up to a 60% smaller
tree at the same depth. But at FIXED depth the ordering is Elo-neutral
(it changes which move cuts, not the minimax result), the do-no-harm
signature we want from an enabler. Depth-6 self-play, 300 games, seed
6502, vs the FtAll baseline:

| config (both correct-guard futility) | Elo (300g)  |
|--------------------------------------|-------------|
| SEE                                  | **+0 ± 31** |
| history + malus                      | **+8 ± 30** |
| SEE + history + malus + losing-last  | **−2 ± 31** |

So the ~24–60% node saving is free (≈that many more reachable nodes at
1 MHz → depth when banked), bought at no fixed-depth strength cost.

**THE KEY EXPERIMENT — does strong ordering make the task #28 "no-winner"
LMR variants light up?** Hypothesis (task framing): the LMR sweep found
nothing because ordering was too weak for deep reductions to be safe.
Re-ran two of task #28's aggressive LMR variants against the default LMR
(4,7,3,5), holding ORDERING identical on both sides, weak (FtAll) vs
strong (SEE+history+malus, 0x7f aord 1,0); 300 games/seed:

| aggressive LMR variant | weak ordering        | strong ordering                |
|------------------------|----------------------|--------------------------------|
| late 3,6 (3,6,3,5)     | −6 ± 32 (6502)       | −20 ± 30 (6502), −15 ± 29 (777)|
| rem1=2 (4,7,2,5)       | −19 ± 31 (6502)      | **−62 ± 30 (6502), −41 ± 31 (777)** |

**Hypothesis FALSIFIED — the coupling is NEGATIVE, not positive.**
Aggressive LMR does not light up; strong ordering makes it *worse*
(rem1=2: −19 weak → −62/−41 strong). Mechanism: SEE+history packs the
genuinely-good moves into ranks 1–3, so lowering the reduction threshold
to rank 3 (late1=3) or to shallow remaining (rem1=2) now reduces *real*
candidates; under weak ordering those ranks are a random mix, so the
same aggression is diluted. The task #28 verdict ("current LMR is
already well-tuned, no change to port") was NOT a weak-ordering
artifact — better ordering makes the well-tuned default *more* clearly
optimal, and reduction thresholds must be tuned TO the ordering, not
loosened because of it. (Untested axis, knob-limited: deeper *R* for the
very-late tail — the one direction good ordering might still unlock.)

**Futility re-margining under strong ordering (the task #34 analogue):**
correct-guard RFP2 250 (over-pruner) vs 500 (adopted), ordering held
identical both sides, 300 games seed 6502:

| RFP2 250 vs 500 | weak ordering | strong ordering |
|-----------------|---------------|-----------------|
| Elo             | −14 ± 31      | −5 ± 30         |

Futility is essentially ordering-DECOUPLED (the +9 shift is well within
noise; RFP returns before move generation, so ordering can't touch its
own node). 250 stays ~10–14 Elo worse than 500 regardless — task #34's
120/500 remains correct; strong ordering does not rescue the tight
margin. So the two pruners couple to ordering oppositely: LMR strongly &
negatively (rank-based, so it *is* ordering-sensitive), futility not at
all.

**Combination A/B harness (TestCombinationAB, COMBO_PAIRS-tunable):**
toggles coupled features jointly vs a shared baseline and prints each
combo's Elo against the sum of its parts. Verdict from the data above:
the "super-additive pruning cluster" premise does not hold here —
{strong ordering + aggressive LMR} is markedly SUB-additive (−62 vs a
sum-of-parts ≈ −21), and {SEE + history} is merely additive-neutral
(−2 vs 0+8). Ordering's payoff is the node saving (→ depth), not a
strength multiplier on the existing depth-6 pruners.

**PORT RECOMMENDATIONS (ranked; Fable's port-spec queue):**
1. **History heuristic for quiet ordering** — biggest single node cut
   (−42%) at +8 ± 30 Elo (neutral-positive), and it needs no exchange
   arithmetic. 6502 feasibility: the 16 KB butterfly [from][to] table is
   too big; port as [piece-type][to] (896 bytes) or a compressed
   from-zone×to table; aging is a cheap LSR sweep. **Adopt first.**
2. **SEE for capture ordering** — −24% nodes, +0 ± 31 (do-no-harm),
   stacks with history to −46/−60%. 6502 feasibility: OPEN — SEE uses
   only adds/table-indexed values (no multiply, so that objection is
   weaker than feared), but the least-valuable-attacker rescan per swap
   ply is the real inner-loop cost; needs a careful attacker-gen pass.
   **Adopt second, after profiling the rescan.** A cheaper MVV-LVA
   capture sort (adds LVA to the current heavy/light victim split) is a
   strictly-easier fallback if SEE proves too costly.
3. **LMR / futility: NO change.** The default LMR (4,7,3,5) and futility
   (120/500) are confirmed optimal and do not loosen under strong
   ordering — port them as-is.

Net: adopt the ordering enablers for their node savings (free depth via
banked time), not for a fixed-depth Elo jump; leave the pruning knobs
alone. All matches are mirror self-play, depth 6, dither on, vs the
current-rules baseline; CIs are ~95%.

## 2026-07-19 — Texel diversified weights: asm rig confirmation (task #23 port) — CONFIRMED, PORT SAFE

Rig-side head-to-head confirming the mirror's diversified-corpus weights
(the pool-match confirmation the task #23 entry below deferred).
Candidate = the committed pool-baseline asm engine with exactly the two
proposed edits applied: the passed-pawn bonus `{0,18,0,33,62,69,28,0} →
{0,15,0,21,50,52,20,0}` (cmd/gentables/main.go) and the isolated penalty
`lda #10 → lda #7` (asm/eval.s, both ptadd10/ptsub10 sites). Baseline =
the committed engine.bin unchanged — a clean source rebuild reproduced it
bit-exact (md5 d662c12…), and the candidate was built in an isolated
worktree, so main's tracked source stays pristine (neither edit is
committed; only this log entry is).

Method: deep optimization review of the mirror's Texel deltas, then a
paired asm A/B via cutechess-cli — 600 games, openings-pool.epd
(sequential, -repeat color-paired), -dither -bank, concurrency 3.
**Reduced 8000 ms emulated budget** (the standing pool control is
30000 ms) for overnight throughput; the engine thinks on emulated cycles,
so CPU contention changes only wall-clock, not outcomes.

Result (candidate vs baseline): **+202 =210 −188, 51.2%, +8.1 ± 22.4
Elo, LOS 76.1%** (draw ratio 35.0%). The CI spans zero — no significant
gain at this budget/n — but the point estimate is positive with no hint
of a regression, corroborating the mirror's own **+21 ± 39** self-play
validation. Two independent rigs agree: neutral-to-mildly-positive.

**Verdict: CONFIRMED — the diversified weights are safe to port (do no
harm, slight positive lean).** The passed-pawn overvaluation correction
carries into the asm engine without costing strength; green-light the
two-edit port. Caveat: confirmation ran at 8000 ms, not the 30000 ms pool
control — a full-budget pool gauntlet would tighten the estimate but is
not needed to clear the do-no-harm bar.

## 2026-07-19 — futility re-margining (task #34) — CORRECT GUARD, ADOPT RFP 120/500

Resolves the task #27 caveat. That A/B flipped only the guard while
holding the RFP/futility margins (120 @ rem 1, 250 @ rem 2) tuned for
positive windows, so it measured over-pruning, not the technique. With
the corrected signed-aware guard now the fixed baseline (we do NOT keep
the unsigned-compare bug), the margins are the real decision — and the
node data pinpointed the culprit before a single match: disabling RFP at
remaining 1 (RFP1→0) erases nearly all the correct-guard node saving
(+0.2% vs shipped), so the saving is almost entirely RFP@rem1. RFP@rem2
saves few nodes but each cut removes a 2-ply subtree — the Elo suspect,
exactly as flagged.

Depth-6 self-play, correct guard, A = candidate vs B = shipped (buggy
guard + 120/250), 400 games/seed unless noted. Node deltas on the mirror
bench (vs shipped 922,898):

| scheme (RFP1/RFP2) | nodes    | Elo (400g, seed 6502) |
|--------------------|----------|-----------------------|
| 120/250            | −20.5%   | **−43 ± 27**          |
| 150/300            | −17.8%   | −37 ± 27              |
| 200/350            | −14.9%   | −4 ± 27               |
| 120/700            | −16.5%   | −17 ± 27 (noise)      |
| **120/500**        | **−16.9%** | **+2 ± 28**         |

The Elo lever is RFP2, not RFP1: RFP2 250→−43, 300→−37, then 350/500/700
all statistically neutral (CIs cross 0). RFP1 120 vs 150 barely moves it.
Node savings plateau at ~−17% (RFP1 dominates), so pushing RFP2 below 500
buys almost nothing (120/400 = −17.3%, only 0.4% more) at more Elo risk,
and a depth-3 RFP extension (120/500/700) reaches only −17.1%.

**Winner confirmed across 4 seeds (1600 games), 120/500 vs shipped:**
+2±28 (6502), +24±27 (777), −8±27 (12345), −2±27 (31337) →
**pooled +4 ± 14 Elo** (524-571-505, 50.6%). Neutral-to-positive, CI
comfortably excludes any meaningful loss.

**ADOPTED as DefaultFutility and the asm port spec:** signed-aware guard
(RFP/futility active in every non-mate window), RFP margin 120 @ rem 1,
**500 @ rem 2**, leaf-futility margin 120, block at remaining ≤ 2. Net
vs the shipped bug: **−16.9% nodes at +4 ± 14 Elo** — ≈17% more
reachable nodes (free depth at 1 MHz) at no strength cost. WAC holds
6/7, mate search green. Re-margining RFP2 250→500 recovered ~47 Elo off
the −43 over-pruner. Asm port: fix the guard to a signed compare AND
change the rem-2 RFP margin constant 250→500 (both, together — the guard
fix alone re-enshrines the over-pruning).

## 2026-07-19 — king-bucketed PSQT (task #30) — DOES NOT CARRY ITS WEIGHT

NNUE/HalfKP-inspired but network-free: non-king pieces get a per-square
value DELTA on top of base PeSTO, selected by the bucket of their own
king's square (4 buckets by king file zone: a-b/c-d/e-f/g-h; cheap
kingfile>>1, castling-aligned, no runtime multiply). Prototype behind
Engine.KB (internal/mirror/kingpsqt.go), tuned by full-batch AdamW on a
fresh 69,893-position FEN corpus (66,088 self-play depth-5 +
3,805 pool; testdata/fenrows-2026-07-19.gz), then measured by depth-6
self-play Elo. Tables and pipeline: `mirror genfen` / `mirror tunekb` /
`match -akb`.

Texel loss falls a LOT (much more than the 10-param pawn tune's
~0.0003): val 0.1030 → 0.094-0.099 depending on L2. But the depth-6
self-play matches (200 games each, A = tuned+KB vs B = tuned, seed 6502)
go the other way:

| weight decay | max\|delta\| | val loss | match Elo   |
|--------------|-------------|----------|-------------|
| 0.05         | 20          | 0.0974   | **−102 ± 42** (+44 =55 −101) |
| 0.10         | 10          | 0.0995   | **−44 ± 40**  (+54 =67 −79)  |

The Elo loss scales smoothly with delta magnitude (−44 at ≤10, −102 at
≤20), so there is **no regularization sweet spot that gains** — every
amount of king-file bucketing costs strength, extrapolating to 0 only as
the deltas vanish. Not a sign bug: the eval/tuner consistency test
passes and the effect scales cleanly with magnitude.

**Verdict: king-file-bucketed PSQT does not carry its weight.** It costs
44-102 Elo while adding ~2.5 KB of table storage (4×5×64×2 bytes, deltas
do fit int8). The lesson is the Texel-loss / playing-strength divergence:
a 2,560-param king-conditioned table overfits self-play result-
correlation (and the hard bucket boundary at files d↔e — exactly where
castling kings cross — revalues every piece discontinuously). NNUE's
strength needs the actual network (end-to-end training + eval scale
calibration), not just the HalfKP bucketing intuition bolted onto a
hand PSQT. Infrastructure kept (toggle off by default) for any future
king-safety eval work; the FEN corpus is reusable. **No asm port.**

Aside: the AdamW fix matters and is guarded by TestKingBucketTune —
folding L2 into the gradient (vs decoupled decay) lets Adam's per-param
normalization amplify it into a restoring force that pins every delta to
zero (loss frozen), which masqueraded as "no signal" until diagnosed.

## 2026-07-19 — Texel corpus diversification (task #23 remainder)

Folded the 210-game rating-pool gauntlet (non-self-play: vs TSCP-d3,
FairyMax, NEG, minnow, SF-n10/n100/n1000; tools/pgn/pool_c96f604_*.pgn)
into the Texel corpus. New pipeline: `mirror pgnrows` extracts quiet,
non-check, labeled positions from PGNs (mirror.PGNSamples in pgn.go,
honoring [FEN] setup headers from openings-pool.epd); LoadRows now reads
gzip transparently. 210 games → 7,706 quiet rows on top of 101,202
self-play rows = 108,908-row diversified corpus
(testdata/texel-rows-2026-07-19.gz).

Re-tune (K 0.80→0.85, loss 0.1005→0.1037 — pool labels are noisier/more
decisive). Bootstrap B=200 95% CIs on the combined corpus, flagging any
weight whose old self-play value falls outside the CI:

| weight   | self→comb | 95% CI      | moved? |
|----------|-----------|-------------|--------|
| doubled  | 12 → 14   | [11, 18]    | no     |
| isolated | 10 → 7    | [6, 9]      | **yes**|
| passed2  | 18 → 15   | [9, 23]     | no     |
| passed4  | 33 → 21   | [16, 27]    | **yes**|
| passed5  | 62 → 50   | [44, 58]    | **yes**|
| passed6  | 69 → 52   | [46, 63]    | **yes**|
| passed7  | 28 → 20   | [15, 31]    | no     |
| shield   | 3 → 2     | [0, 3]      | no     |
| openfile | 4 → 3     | [0, 9]      | no     |

Real movement: the advanced passed-pawn bonuses (ranks 4/5/6) drop
~20-25% and the isolated penalty drops 10→7. Interpretation: self-play
overvalues passed pawns (both sides push them symmetrically, so the
label correlation inflates the bonus); diverse real-opponent games
correct it downward. Validation A/B (depth 6, 200 games, diversified
weights vs old self-play-tuned): **+21 ± 39** (+71 =70 −59, 53.0%) — the
self-play match environment has home-field bias toward the incumbent, so
neutral-to-positive here is a genuine (if sub-significant) endorsement.

**Adopted** the diversified set as mirror `TunedWeights`
(D14 I7 P[15,0,21,50,52,20] S2 O3; old set kept as
`SelfPlayTunedWeights`). Asm-side port target updated; final
confirmation is a rig-side pool match (deferred, not mirror work).

## 2026-07-19 — mirror verdicts: futility, LMR sweep, QS-shape (3 tasks)

Three mirror A/B campaigns (internal/mirror/ab_test.go), all depth-6,
node counts vs the current-rules base of 922,898 on the mirror bench
set (qs = 819,637 = 89% of nodes). Matches are mirror self-play at
depth 6, ~200-800 games, Elo vs the current-rules baseline. **Bottom
line: two "obvious" improvements are duds, one QS knob is a keeper.**

**1. Futility mate-zone guard fix (task #27) — DON'T PORT AS-IS; the
guard and margin are one tuning problem.**
The asm's futility/RFP guard uses an unsigned compare, so futility is
silently disabled in every negative-alpha/beta window (the same bug
class as the old null-move one). Flipping the guard signed-aware
(futility active in those windows) cuts −20.7% nodes but costs
strength:
- 800-game match, fix=true vs current: +229 =295 −276, 47.1%,
  **−20.4 ± 19.2 Elo** (weak: CI barely clears zero). Four smaller
  200-game runs agree (−31, −23, −17, −10).
- **CAVEAT (added on review — the first read of this over-claimed).**
  What the A/B changed is ONLY the guard; the futility/RFP margins
  (static 120 at rem 1, 250 at rem 2) were left at values chosen while
  futility ran only in positive windows. Extrapolating those margins
  into negative windows over-prunes — that is exactly the −20%-nodes/
  −20-Elo fingerprint. The experiment therefore CANNOT distinguish
  "futility in negative windows is bad" from "these static margins are
  wrong there," because guard and margin were flipped together. So the
  "bug is protective" reading holds only conditional on leaving the
  margins untuned — it is not a property of the technique. Also: the
  mirror models the bug as an idealized "off when negative"; the real
  asm is an unsigned compare on signed bytes, whose per-window
  behavior is not necessarily a clean cutoff. NEXT: don't retire this
  — enable the signed guard and sweep/Texel the futility + RFP margins
  (depth-scaled) for negative windows; suspect RFP@rem2 static-250
  first. The −20% node saving is worth ~20% reachable depth at 1 MHz
  IF it can be had at neutral Elo, which this A/B never tried.
  **RESOLVED by task #34 (newest entry): the suspicion was exactly
  right — RFP@rem2 was the coster; re-margined to 500 the correct guard
  is neutral-to-positive at −16.9% nodes.**

**2. LMR/PVS parameter sweep (task #28) — no porting winner.**
Swept lateness {2,3,4}×{5,6,8}, R floors, reduce-killers, evasion-PVS.
Node cuts are large; strength is flat-to-negative everywhere:
- `2,6,2,4+killers` −53.4% nodes; `3,6,2,4` −48.6%; `rem1=2` −40.5%.
- But matches (±38 Elo, ~200 games each): `4,8,3,5,0,1` −0,
  `4,7,3,4,0,1` −2 / −14, `3,6,3,5,1,1` −14 / −37, `4,7,3,5,0,0` −26,
  `4,7,2,5,0,1` −44. Nothing beats current rules; the deep node-cutters
  lose Elo. **Current LMR is already well-tuned — no change to port.**

**3. QS-shape experiments (task #29, COMPLETE) — recap2 is the keeper.**
Deep-QS knobs: PlyCap (force stand-pat past N qs plies, evasions
exempt) and RecapAfter (past N qs plies, captures only onto the
previous move's TO square). Node cuts (total / qs):
- `recap1` −35.5% / −39.4% · `cap2` −29.3% / −32.7% · `recap2`
  −21.4% / −24.4% · `cap4` −20.0% · `cap6` −13.2%.
- Matches, depth 6, 200 games (100 color-swapped pairs), Elo vs the
  current-rules (uncapped-QS) baseline, all seed 6502:
  - `recap1` (qs 0,1) = **−123 ± 42, catastrophic** (chops the
    recapture tree one ply too early).
  - `recap2` (qs 0,2) = **−12 ± 39** (+61 =71 −68, 48.2%) — confirms
    the prior −10 ± 38 run; statistically neutral at −21%/−24% nodes.
  - `cap2` (qs 2,0) = **−113 ± 40** (+34 =69 −97, 34.2%) — a dud,
    nearly as bad as recap1. Forcing stand-pat at qs ply 2 blinds QS
    to deeper captures/recaptures; the PlyCap knob only pays off much
    deeper (cap6/cap8 barely cut nodes, so there's no useful window).
- **Winner: recap2 (qs=0,2), no combo.** A cap+recap blend is not
  worth pursuing — the only cap shallow enough to save real nodes
  (cap2) is catastrophic, and recap2 already delivers the full −24% QS
  saving at neutral strength on its own. recap2 is the one portable
  lever of the three campaigns. Asm port (a RecapAfter gate in
  generateq/qsearch — capture onto undo[ply-1].to only, past 2 qs
  plies) deferred to a careful inner-loop QS-generation pass.

## 2026-07-19 — first full rating-pool gauntlet (standing scoreboard)

runs/pool.sh @c96f604: 30 games per opponent, 30s emulated/move with
-dither -bank, paired colors from tools/openings-pool.epd (TSCP runs
bookless — xboard v1, no setboard, forfeits on book positions; its
variety comes from -dither). PGNs in tools/pgn/pool_c96f604_*.pgn.

| Opponent  | Result (W-L-D) | Score | Elo diff (logistic) |
|-----------|----------------|-------|---------------------|
| NEG       | 30-0-0         | 100%  | (unbounded +)       |
| minnow    | 30-0-0         | 100%  | (unbounded +)       |
| SF-n10    | 6-23-1         | 21.7% | −223                |
| SF-n100   | 2-26-2         | 10.0% | −382                |
| FairyMax  | 1-27-2         | 6.7%  | −458                |
| TSCP-d3   | 1-29-0         | 3.3%  | −585                |
| SF-n1000  | 0-30-0         | 0%    | (unbounded −)       |

Anchoring, with caveats stacked high: Fairy-Max is CCRL ~1890, so the
−458 puts us at ≈**1430** — but Fairy-Max here plays st=2 wall (below
its CCRL time control, so the true gap is larger than the CCRL number
implies), and n=30 gives roughly ±120 Elo bars per match. TSCP-d3
implies lower still, but depth-3-capped TSCP has no published rating
(full TSCP is ~1700). Treat ≈1400±150 as the first honest anchor and
the SF node ladder (21.7% / 10% / 0%) as the sensitive internal
yardstick — node-limited Stockfish is perfectly reproducible, so
future builds move that ladder or they didn't get stronger.

Notes: TSCP-d3 3.3% vs the banked-time 11.7% two entries down is n=30
noise plus opening-variety differences (both bookless, different
dither seeds) — the pool number is the standing-conditions one going
forward. The 180 pool PGNs are the first non-self-play Texel corpus
material (diversification queued, task #23 remainder).

## 2026-07-19 — banked time: first real move in the TSCP needle

Chess-clock banking rig-side (chesstest.BankedClock, bridge -bank
flag): unused per-move cycles carry forward, each move spends
base + bank/8, bank capped at 8x base, total game time conserved
(protocol (c) comparability). Predictive iteration gating is the
enabler — honest early stops on easy moves now fund extra iterations
on hard ones. 6502 driver port noted (24-bit zp bank, /8 = shifts).

- Diagnostic 60-ply carryover game, realized depth: unbanked
  d1:11 d2:30 d3:16 d4:3 → banked d1:3 d2:20 d3:22 d4:7 d5:2.
  Depth-1 emergency stops nearly gone; depth >= 4 tripled.
- **TSCP-d3, 30 games, -dither -bank: 1-24-5 = 11.7%** — the best
  score yet against it (historical band 3.3-6.7%), and the first
  post-change match to move in the predicted direction. n=30 keeps
  the error bar wide (~±6%); the queued rating-pool gauntlet will
  firm it up. Next depth levers: the mirror's LMR-parameter ranked
  table, then its QS-shape verdicts.

## 2026-07-19 — pawnterm rank-bitmask + two latent bugs + Texel weights

The restructure (per-file rank-occupancy bytes + gentables lookup
tables; scratch $0200-$020F) was gated on exact parity — and the gate
earned its keep twice before passing:

- **h-file isolated bug (old code)**: ptneighw/b returned the flags of
  `cpx #7` (Z set) on the file-h path, so h-file pawns with a g-file
  neighbor were scored isolated anyway. Symmetric ±12 wash in balanced
  positions, real error otherwise. Fixed (`ora #0`, commented
  load-bearing).
- **king-shield scratch clobber (old code)**: ptsuba stomped EVTMP,
  which the shield loops use for the king file, so the black shield
  read past the array (into stale PWMAX bytes) after its first term.
  Fixed (ptsuba uses MULCNT).

With buggy-old off the table as a reference, the gate is now a Go
model of the intended semantics: TestPStructParity, asm == model over
4,045 random-game positions, exact. QS profile: pawnterm's share of
total cycles 15.7% → 12.9% (the 32-slot piece-list scan still
dominates its cost).

**Texel-tuned weights** (from the mirror, task #20: +18 ± 17 vs
no-pstruct over 800 mirror games): isolated 12→10 (split from doubled,
which stays 12), PASSEDBONUS → [0,18,0,33,62,69,28,0], shield 8→3,
open king file 10→4. Asm-side 50-pair SPRT 0x1F vs 0x17: −7 ± 50 —
under-powered at 100 games, consistent with the mirror's CI; the
mirror number is the load-bearing one.

## 2026-07-18 — time-management campaign, items 1-2 (gating, generateq)

1. **Predictive iteration gating + mate-stop** (driver): start iteration
   N+1 only if now + 2x(last iteration's cost) fits the budget; stop
   deepening once a winning mate is exact. Diagnostic game: hard aborts
   13 → 11 of 60 moves, avg think 37M → 32M cycles at the same realized
   depths; mate-in-2 budget search 20M → 2.6M cycles. TSCP-d3 at 30
   games: 0-28-2 (3.3%) vs the 5.0-6.7% band of the previous three
   matches — within noise, and as predicted the honest stops don't gain
   strength at a fixed per-move budget: the savings must be BANKED
   (carry unspent cycles into later moves) to convert into Elo. Banked
   time is the queued follow-up.
2. **generateq** (compile-time captures-only movegen copy, GENCAPS
   retired): behavior-identical (perft/WAC/torture/ckverify green),
   honest measurement only ~0.5% at fixed depth on capture-dense
   positions — the win is structural plus a few percent on quiet ones.
3. **QS profile** (new TestQSProfile): qs = 85-93% of all cycles;
   pawnterm ≈ 13-14% of total (retriggered by every qs pawn capture);
   movegen walks 9-23%. pawnterm rank-bitmask restructure is item 3.

## 2026-07-18 — the "LMR depth collapse" that wasn't; honest abort reporting

The first post-LMR TSCP-d3 match (2-28-0, pgn/…_lmr.pgn) looked like a
regression: PGN depths showed 2-3 where the pre-LMR match had shown 4.
Diagnosis with the new PC-cycle profiler (harness.RunProfile /
chesstest.RunProfiled) and a per-move depth-logging carryover game
(internal/sprt TestDebugDepthGame) found **no regression**:

- Head-to-head on identical positions/seeds, the f2-era build realizes
  depth 1-3 in middlegames at 30 emulated s — same as current; the
  current build hard-aborts *less* (4 vs 9 times in the 60-ply
  diagnostic game). Cold budget searches: 0x1F ≈ 0x0F, sometimes
  cheaper. Dither: no effect (seed 0 vs 17 within noise). No re-search
  storms in the profile.
- The "depth 4" belief came from two reporting bugs: on hard abort the
  driver reported CURDEPTH = the *aborted* iteration (one more than
  completed) and left the abort dummy score in SCORE — hence PGN lines
  like "0.00/5" while dead lost. Both fixed: the driver now restores
  the last completed iteration's score (PREVSC0/1) and decrements
  CURDEPTH on the abort-fallback path.
- Match scores agree: f2-era 0-18-2 (5.0%), first LMR match 2-28-0
  (6.7%), post-fix rerun 0-27-3 (5.0%, pgn/…_lmr2.pgn). All the same
  within noise — TSCP-d3 simply outclasses us at this budget.
- −87% fixed-depth win re-confirmed post-fix: 0x0F 5,502M vs 0x1F
  718M at depth 6.

What the diagnosis exposed as the REAL costs (next levers):
1. **QS is the iteration floor**: at CURDEPTH 2-3, 60-95% of cycles sit
   at plies 5-14 — quiescence trees, which LMR cannot touch. Iteration
   costs are QS-dominated and erratic; that's why realized depth stalls
   at 2-3 regardless of full-width improvements. Candidates: qs ply
   cap, deep-qs recapture-only, SEE-ish pruning.
2. **Hard aborts waste ~45M cycles** on ~25% of middlegame moves (2x
   budget spent, aborted iteration discarded). Predictive iteration
   gating (spent + est(next) vs limit) would reclaim most of it.
3. **pawnterm is ~9-12% of total cycles** in real games — promotes the
   rank-bitmask restructure from "nice to have" to "next batch".

## 2026-07-18 — PVS + LMR (FT_LMR): −87% depth-6 tree

PVS zero-window scouts after the first legal move, with LMR on late
quiets (depth−1 at >= 3 moves searched and remaining >= 3; depth−2 at
>= 6 and remaining >= 5; never in check, never for checking moves,
never at the root). Fail-high scouts re-search: reduced → unreduced
zero-window → full window when open. Depth-6 fixed-tree cycles:

| features | cycles |
|----------|--------|
| 0x00     | 6,509M |
| 0x07     | 3,757M |
| 0x0F     | 5,498M |
| 0x1F     | **718M** |

−87% vs 0x0F; masks without FT_LMR moved <1% (mode-select overhead
only — gated-off behavior is bit-identical). Self-play 0x1F vs 0x0F,
50 pairs at 30 emulated s: +37 =31 −32, **+17 ± 57 Elo** — positive,
inside noise, as expected where the budget sits between depth
thresholds; the tree cut is the durable win (roughly two extra plies
at fixed cycles, EBF ~6.5 → the same budget now reaches depth 5-6
where it reached 4). WAC stays 6/7 at depth 4 (b3b2 remains the
historical miss); root reductions were tried and reverted — WAC.001's
quiet mate move g3g6 was reduced into a fail-low at the root, which is
why the root scouts but never reduces. Mate suite exact throughout
(re-searches rescue reduced mates). Next: rematch TSCP-d3 at the new
realized depth; tune reduction thresholds (the 3/6-move, R=1/2
boundaries) in the Go mirror rather than 30-minute asm SPRTs.

## 2026-07-18 — deep-optimization batch: −15% cycles at identical trees

Six items from the optimization review docs applied (taper right-shift
multiply, SLOTTAB, emitmove filter removal, TT table-addr + unrolls,
movepiece/takepiece fusion with kind-major Zobrist relayout, attacked()
SMC rewrite). All behavior-identical — same trees, same scores, full
suite green after every step. Depth-6 fixed-tree cycles:

| features | before | after | delta |
|----------|--------|-------|-------|
| 0x00     | 7,604M | 6,480M | −14.8% |
| 0x01     | 6,708M | 5,724M | −14.7% |
| 0x07     | 4,404M | 3,742M | −15.0% |

The uniform ratio across configs confirms the cuts are in the
per-node constant factor, not tree shape. Cumulative since the perf
campaign began: 0x00 8,575M → 6,480M (−24%), all-features 4,912M →
3,742M (−24%). Still open from the review: two-ended emit
(~200-600/node, needs node-count A/B), two-copy generate, pawnterm
bitmask (lands with the Texel-tuned weights). Next depth lever: PVS/
LMR (F4b restructure).

## 2026-07-18 — long battery: gates at 100 games, features-vs-budget verdict, first TSCP wins

Full battery in tmux (runs/battery1.log), post give-check-propagation
build; full test suite green first.

- Depth-6 fixed-tree cycles: 0x00 = 7,604M, null-only = 6,708M,
  null+killers+futility (0x07) = 4,404M. The 0x00 baseline itself
  dropped from 8,243M — give-check propagation pays even with pruning
  off.
- Feature gates, 100 games each at 30 emulated s/move: null −3 ± 53,
  killers +17 ± 53, futility +10 ± 53, pstruct −35 ± 61. All still
  inside noise; accumulation continues (task #17).
- **All-features (0x0F) vs baseline, 200 games: −7 ± 39.** The
  features cut the depth-6 tree ~42% but buy no Elo at this budget —
  and the arithmetic says why: 30 emulated s ≈ 31M cycles, while even
  the 0x07 depth-5 tree costs hundreds of millions. Both configs
  realize depth 4; the saved cycles can't cross the next depth
  threshold, so they're wasted. The pruning features are levers that
  only cash out once constant-factor cuts (deep optimization review,
  ~750-850 cycles/node identified) + PVS/LMR bring depth 5 in range.
  pstruct's −35 drag is separately fixable: weights are untuned
  (Texel tuning in progress on the Go mirror, task #20).
- TSCP-d3 rematch, 30 games, dither on: **2-27-1 (8.3%)** — the first
  outright WINS against TSCP-d3 (previous best 0-18-2 = 5%). Still
  decisively outgunned at realized depth 4 vs its depth 3 + better
  eval + faster wall clock.

## 2026-07-18 — perf batch 1 (lazy legality, attacked() micro, hashstm unroll)

Depth-6 fixed-tree cycles: baseline 8,575M -> 8,243M; all features
4,912M -> 4,727M (~4% each). Well under the review's 15-20% model for
lazy legality — that model predated the QS surgery, which had already
removed most per-node legality work. Full suite green throughout
(perft exact; legality torture is the gate that would catch an
over-eager skip). Next ply must come from the structural items:
give-check propagation (kills the second full attacked() scan per
node) and the move-loop restructure.

## 2026-07-18 — M5a: eval terms, dither, and the depth verdict

- Pawn structure + king shield (FT_PSTRUCT) self-play A/B: +14 +/- 57
  over 100 games at 30 emulated s/move — directionally positive,
  unresolved at this sample size (accumulation continues).
- TSCP-d3 rematch with dither: **0-18-2** (from 0-20). First draws, and
  the games are finally distinct (per-move seeded eval dither, the
  simulation of the hardware plan to seed from input timing).
- **The decisive diagnostic**: the bridge now emits depth/score into
  the PGNs — we search **depth 4** at 30 emulated s/move in the
  middlegame. The remaining gap to TSCP-d3 is depth, not sanity.
  Next lever: the open performance items (lazy legality ~15-20%,
  move-loop restructure ~10-15%, make() fusion ~5-7%, give-check
  propagation ~15-20%) — together roughly a node doubling, ~+1 ply,
  compounding with ID/TT. Then re-measure.

## 2026-07-18 — post-fix measurements

Feature gates, self-play at 30 emulated s/move, generated paired
openings (80 games each; the fourth run was cut off by a task limit):
null +0 +/- 47, killers +26 +/- 50, futility +0 +/- 49. The 43%
tree-size win compresses to small Elo in self-play at these depths;
resolving +20-30 needs 400+ games (queued).

TSCP-d3 rematch at 30 emulated s/move: **0-20**. But the game character
changed completely: no more degenerate moves — legal, coherent,
planless chess, ground down positionally (opponent evals creep +0.4 to
+5 over ~25 moves). Diagnosis for next session: (a) instrument realized
search depth per move in games; (b) the eval gap — TSCP has pawn
structure + king safety, we are PSQT-only (M5 terms may now be worth
more than search); (c) deterministic + bookless = the same losing line
repeats every White game (book/variety, M6); (d) A/B the TT aux
carryover, which was never gated.

## 2026-07-18 — M4 debugging night: the pruning stack made real

Fixed-depth tree size on the reference middlegame position (cycles to
complete the search; the honest metric after learning that budget-mode
soft-stops invalidate comparisons):

| Features | Depth 6 cycles | vs baseline |
|---|---|---|
| none | 8,575M | — |
| null move | 7,498M | −13% |
| null + killers + futility | 4,912M | −43% |

What it took to get there (full detail in
docs/reviews/2026-07-18-code-review.md):
1. Adversarial review found null move disabled by an unsigned compare
   on the beta high byte (negative betas always read as "mate zone").
2. Fixing that made trees BIGGER (+73%): single-step instrumentation
   showed the search was QS-dominated (capture chains to ply 30,
   captures in piece-list order, delta pruning documented but never
   implemented). Fixed: two-tier MVV capture passes, per-ply delta
   threshold, capture-only qs generation.
3. Still bigger with null: shallow nulls (remaining 2-3) reduce to bare
   QS sweeps that start fail-low (with the eval>=beta gate, the null
   child's stand-pat can never cut) — all cost, no value when ordering
   already cuts on the first real move. Floor raised to remaining >= 4;
   null cutoffs now also store TT lower bounds, and attempts are gated
   on static eval >= beta.

Also: TT upper-bound cutoffs missed score==alpha (fixed; the baseline
itself dropped ~40% from this), eval got w=32/w=0 taper fast paths,
futility/RFP gained mate-zone window guards, iteration 1 is now
abort-immune.

## 2026-07-18 — M3 complete: first calibration matches

Engine: ID + aux TT + UCI bridge, 30 emulated s/move (~30.6M cycles).

| Opponent | Conditions | Result |
|---|---|---|
| N.E.G. 1.1 (very weak) | st=1 wall | **2-0** (mates both colors) |
| TSCP 1.81 (~1700 CCRL) | st=2 wall (full strength) | 0-10 |
| TSCP depth-limited to 3 | st=2, depth=3 | 0-6 |

Analysis (verified, not guessed):
- Bridge vs cold-engine replay of the loss prefix: identical moves at
  every turn — no TT-carryover corruption, no bridge bug.
- The "hung pawn" moves are PeSTO working as specified: an a-file pawn
  is worth only ~47cp in the mg table (82 - 35 PSQT), so trading it for
  ~20cp of activity is what the eval orders. Verified arithmetically:
  startpos minus the white a-pawn evals -37 = -(82-35) + 10 tempo.
- The real gap is depth: measured EBF is still ~8 (depth 5 on a
  middlegame position cost 5.1B cycles; 30M budget reaches ~depth 4).
  TSCP at depth 3 + its fuller eval outplays that.
- One real bug found and fixed en route: an aborted ID iteration's
  partial "best move" (fail-hard: the first root move always raises
  alpha from -INF) was preferred over the last completed iteration's
  move; with the root TT entry evicted this returned near-arbitrary
  moves, degrading play as the game went on.

This is the recorded pre-pruning baseline. M4 (null move R=2, killers,
futility/RFP) attacks the EBF directly; the plan's model expects those
to buy 2-4 effective plies at the same budget.

Also measured this cycle:
- ID + TT vs cold fixed-depth: WAC.001 to depth 4 in 706M cycles vs
  2,473M (3.5x, including depths 1-3). Suite: 1,537M vs 3,715M.
- WAC subset: 6/7 at depth 4 (both modes).
