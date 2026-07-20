# Match & measurement log

Newest first. Engine budgets are emulated time (1.0205 MHz); opponent
controls are wall time. See docs/plan.md for the measurement protocol.

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
