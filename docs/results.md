# Match & measurement log

Newest first. Engine budgets are emulated time (1.0205 MHz); opponent
controls are wall time. See docs/plan.md for the measurement protocol.

## 2026-07-25 — CHECK EXTENSIONS (mirror): cap N=1, all checks = +12.6 ± 9.0 (4000g) — PORT

Standard check extension in the MAIN search (never QS): when a move gives
check (inChk[ply+1], already computed in make), search the child one ply
deeper (don't decrement remaining depth), capped at MaxExt extensions per
root-to-leaf path (numExt counter, balanced save/restore around the child
search). LMR interaction: a checking move is already never depth-reduced
(the mode≥2 gate requires !inChk[ply+1]), so extension and reduction are
mutually exclusive by construction — no double-counting. Futility: the
extension is on the CHILD horizon; the parent's RFP/futility (parent not in
check) is untouched. Cost model: no cycle-cost knob — the extension is
taxed purely through the extra nodes it searches (normal chargeNode), so
the 143M cycle budget discounts it honestly.

Fixed-depth-6 tree blow-up (asm-matched 0x1f, 3 positions):
| variant       | nodes vs off |
|---------------|--------------|
| cap1 (N=1)    | +12.9% / +11.5% / +26.7% |
| cap2 (N=2)    | +13.9% / +15.1% / +33.1% |
| cap3 (N=3)    | +13.9% / +15.3% / +33.2% (≈ cap2: check chains >2 are rare) |
| cap2 caps-only| +5.8% / +9.2% / +33.8% |

Cycle-budget screen (cbudget 143M, asm-matched 0x1f both sides, ON vs OFF,
self-play, aggregated across seeds):
| variant (A=ON)          | Elo ± err | games |
|-------------------------|-----------|-------|
| **cap1 (N=1, all checks)** | **+12.6 ± 9.0** | 4000 |
| cap2 (N=2, all checks)  | +7.1 ± 12.6 | 2000 |
| cap2 captures-only      | −0.3 ± 17.8 | 1000 |

Read: cap1 is the operating point — a SINGLE check extension captures the
tactical benefit (+12.6 ± 9.0, lower bound +3.6) while adding the fewest
expensive nodes, so the cycle budget taxes it least. cap2/cap3 add depth
past the first check for little extra Elo but more cost (nets +7). Restricting
to checking CAPTURES kills the gain (−0.3) — the QUIET forcing checks carry
the value. RECOMMEND porting the plain single-ply check extension (MaxExt=1,
all checks): cheap (gives-check is already in make), 6502-idiomatic (skip the
depth decrement for a checking move, 1-ply path cap), modest but real. Mirror
only; asm untouched. OFF path byte-identical (TestCheckExtOffIsNoop, both
move loops, both budget kinds). Final gate remains the asm SPRT.

## 2026-07-24 — FIXED the Sargon Hard-Mode promotion "no reply" quirk (root cause: forced-reply detection, not promotion entry)

Resolved the bug that drew ~10% of the 300-game symmetric run (g10 f7f8r,
g30 f7f8q, …). ROOT CAUSE was NOT the promotion entry (the "ENTER PROMOTED
PIECE" prompt was always answered; the /Q,/R token always registered).
It was REPLY DETECTION: when our move — here a stalemate-avoiding
under-promotion f7-f8=R — leaves Sargon a SINGLE legal reply, Sargon plays
it INSTANTLY the moment the move registers, on the Infinite level, WITHOUT
waiting for CTRL-T. RequestMove captured its commit baseline AFTER
enterMove, by which point the reply (e.g. H7-H6) was already on the list, so
forceCommitHard waited forever → "no reply after CTRL-T" → adjudicated draw.

FIX (internal/sargon/driver.go): RequestMove now snapshots Sargon's
newest-move token BEFORE entering our move (the token is scroll-immune and
our move lands in the opposite color's column, so it only changes on a
Sargon commit) and returns immediately if a reply appears during entry.
Also hardened completePromotion for Hard mode: longer prompt-wait +
re-strobe the confirming keystroke until the prompt clears (mirrors
forceCommitHard's repeated CTRL-T), so a confirm keystroke missed by the
rarely-polled keyboard latch can't park Sargon at the prompt.

VALIDATION. Exact g10 position (8/5P1k/5K2/8/8/8/8/8 w): RequestMove F7-F8R
BEFORE → "no reply after CTRL-T"; AFTER → Sargon replies H7-H6 (regression
test TestHardPromotionForcedReply). Deterministic 11-game rerun (book-seed
1, pool, B=30M): BEFORE 3W-2L-6D (g10 = draw); AFTER **4W-2L-5D, g10 =
8fish-wins** — the masked position IS a win (KR-vs-K mate). Games g0-g9
byte-identical, so no perturbation of non-promotion play. Symmetry exact
(459/459 moves 8fish_think == sargon_ponder_window), adherence 0.9836,
ponder hits 47.9%. All Easy- + Hard-mode promotion tests pass (Q, under-
promotion N/R, checking promotion, Sargon's own promotion decode).

Implication for the 300-game number: the ~30 masked draws are promotion
endgames the promoter wins, so the fixed score should rise above +8 — a
full rerun (coordinator) is needed to quantify. Ready command:
runs/sargon-symmatch.sh 300 30000000 <outdir> 1.

## 2026-07-23 — adaptive time/effort management (mirror screen): banking + aggressive targeting = +54 over the flat mirror

Mirror-side measurement of ADAPTIVE effort allocation (internal/mirror/
effort.go: a per-GAME cycle bank + an ID-loop-signal allocation policy;
`mirror atime`). The engine has no clock, so "time" is EFFORT (est. 6502
cycles/move). Both arms earn `Base` cycles/move of income into a shared
per-game bank (EffortBank, the mirror twin of chesstest.BankedClock); the
only difference is HOW the bank is spent. asm-matched config (mask 0x1f,
recap2, shipped weights, corrected-guard RFP 120/500), self-play, dither
on, `Base = 40 M` cycles/move (≈ depth 4.5; spot-checked at 80 M ≈ depth
5.1). The OFF path (TimeParams.On=false, no smoothing) is byte-identical
to a flat `SearchCycleBudget(Base)` — TestEffortOffIdentical.

Signals (all cheap, read from the ID loop, 6502-portable — see the
asm-port note in effort.go): best-move **instability** (best changed →
extend), **score drop / panic** (this iteration worse than the last →
extend), **easy move** (best stable N iters → stop early, bank it).

Results (A vs B, A POV, 2000 games/row @40M unless noted, ±~13 Elo):

| A (policy)        | B (baseline) | Elo ± err | A Mcyc/mv | B Mcyc/mv | ext%  | short% |
|-------------------|--------------|-----------|-----------|-----------|-------|--------|
| flatbank (bank+/8)| flat (nobank)| **+23**   | 38.3      | 33.2      | 0     | 0      |
| adaptive (moder.) | flatbank     | −0        | 39.3      | 38.3      | 50.5  | 36.3   |
| easy-stop only    | flatbank     | −0        | 37.6      | 38.3      | 0     | 37.7   |
| instability only  | flatbank     | −2        | 39.6      | 38.3      | 43.5  | 0      |
| panic (drop) only | flatbank     | +8        | 39.2      | 38.3      | 15.1  | 0      |
| adaptive-smooth   | flatbank     | +9        | 39.4      | 38.3      | 50.1  | 37.2   |
| **adaptive-aggr** | flatbank     | **+21 ± 9** (4000g) | 36.1 | 38.3 | 60  | 71 |
| **adaptive-aggr** | flat (nobank)| **+54**   | 35.9      | 33.2      | 60    | 71     |
| adaptive-aggr @80M| flatbank     | +30 ± 20 (800g) | 68.2 | 76.5    | 70    | 83     |

Findings:
- **Banking recovers wasted slack.** The flat per-move budget (current
  mirror) underspends by ~15% — the ~50% soft-start gate leaves the top
  of the budget unused whenever the next iteration would overshoot.
  Recycling it into a bank (BankedClock `Base + bank/8`) spends that slack
  on later moves: **+23 Elo** at +15% compute. (Matches the 2026-07-19
  banked-time TSCP result; here quantified at 2000 self-play games.)
- **Even targeting is near-neutral; AGGRESSIVE targeting wins at LESS
  compute.** On top of even smoothing, the moderate policy and each single
  signal are within noise. But an aggressive policy — commit early on
  stable moves (StableIters 2, MinDepth 2, stop after Base/4) and dump up
  to 4× Base on panic/instability — is **+21 ± 9** over even banking while
  spending 6% FEWER cycles at shallower average depth (a strict Pareto
  win: stronger AND cheaper). Robust at 80 M (deeper): +30 ± 20 at −11%
  compute. Total over the flat mirror: **+54**.
- **Where the gain lives.** Panic (score-drop) is the only single signal
  that leans positive (+8); the win emerges from the *combination* of
  hard-move extension and aggressive easy-move banking, not any one
  signal. Instability alone is non-selective at this depth (best flips
  between iterations constantly → extends 43% wastefully, neutral).

Winning params (adaptive-aggr): EasyStop StableIters=2 ScoreFlat=30
MinDepth=2 MinSpend=Base/4; Panic drop≥25cp → ceil 4×Base; Unstable →
ceil 3×Base; MaxEighths=32 (4×Base per-move cap, also clamped to
Base+bank). asm port = the existing soft-start gate with a MOVABLE
ceiling + a 24-bit zp bank (effort.go asm-port note; no floats, /k are
shifts, signals are a 2-byte compare + a counter). NOT YET PORTED — a
follow-on; the mirror screen is the go/no-go and it says GO.

## 2026-07-24 — CLEAN symmetric rematch, 300 games: +21 ± 34 — 8fish AHEAD of Sargon III (winning record 121-103)

The fully-honest number, all three flaws fixed: (1) Hard-Mode promotion
no-reply fixed (quirk-adjudications 30 → 3), (2) DITHER on so the 40-
opening pool yields independent games (not ~40 deterministic replays),
(3) budget conserved (adherence 0.9935). Fudge-free symmetric Hard-Mode
pondering as before.

**121W −103L =76D = 53.00% ≈ +21 ± 34 Elo** (95% CI [−13, +55]).
Winning record: **+18 (121 vs 103)**; draw rate down to 25.3% (the
promotion quirk had inflated it to 34%).

INTERPRETATION: the point estimate jumped +8 → +21 once the promotion
wins stopped being scored as draws, and the ±34 is now HONEST (dither →
independent games; the prior run's ±32 was false-tight). So 8fish is
GENUINELY AHEAD of full-strength Sargon III on fair terms — a real
winning record — but the CI just barely includes zero, so not yet
STATISTICALLY decisive. "Decisive" needs either more games (~600-900 to
push the lower bound above 0) or a strength gain that widens the edge
(contempt to convert the ~25% draws is the leading candidate; composes
with the endgame mop-up). Best honest verdict so far: **8fish beats
Sargon III more often than it loses, on honest symmetric terms** — the
thing it was built to do.

## 2026-07-24 — SYMMETRIC rematch, 300 games: +8 ± 32 (parity, slightly positive) — but a promotion quirk masks games

The long clean run. cmd/sargon-symmatch, B=30M, Hard Mode, mirrored
ponder, full 8fish stack, en-passant token fix + debt-bank conservation
in place. **103W −96L =101D = 51.17% ≈ +8 ± 32 Elo** (95% CI [−24,+40]).
Adherence 0.9903 over all 300 games (debt-bank holds); genuine draw rate
~26% after removing the quirk below.

**HARNESS QUIRK — 30/300 games (10%) adjudicated to draws by ONE bug on
ONE opening.** A specific pool line reaches a position where 8fish plays
F7-F8 promotion; Sargon in Hard Mode does not reply to the promotion
after CTRL-T, so the harness times out → "unresolved → draw." Because
both engines are deterministic and the pool cycles, it recurs every ~20
games (g10 f7f8r, g30 f7f8q, g50 …). All 30 → draws. Excluding them the
score is ~51.3% (unchanged — the conclusion holds), BUT these are
PROMOTION games (the promoter usually wins); if that position is a win
for 8fish, fixing the quirk converts up to ~30 draws to wins → as high
as ~+43 Elo. So +8 ± 32 is our best number and may UNDER-state us.

PRIORITY: fix the Sargon-promotion-no-reply-in-Hard-Mode quirk (a real
adapter bug, likely the same class as the earlier promotion-confirm
issue) before the next measurement — it's the biggest lever toward a
trustworthy number and may uncover a real edge currently masked.

Verdict on "decisively beating Sargon III": still NO — best honest
estimate is parity, a hair positive (+8 ± 32). But the trend across
clean-er conditions and the masked promotion games leave open that the
true value is modestly positive. The genuine ~26% draw rate (no
contempt: 8fish settles equal positions at score 0) keeps contempt on
the table as a strength lever, now less inflated than the raw 34%.

## 2026-07-23 — SYMMETRIC fudge-free Sargon III rematch: PARITY (48.75%, −9 ± ~100)

The definitive test, on honest terms at last. cmd/sargon-symmatch: an
in-process match where 8fish and Sargon III BOTH ponder for exactly the
emulated-cycle time the other consumed — no 1.5× multiplier — both in
Hard Mode (true Infinite level + repeated CTRL-T), and move-acceptance
read from the ponder-immune SCREEN column (which also retires the old
promotion-confirm bug that inflated prior wins). Full 8fish stack: book
+ pondering + adaptive time mgmt + mop-up, B = 30M cyc/move.

**15W −16L =9D over 40 games = 48.75% ≈ −9 ± ~100 Elo. Statistical
parity.** Symmetry verified EXACT: 8fish_think == sargon_ponder_window
on 1787/1787 moves (cycle-for-cycle). Ponder hits ~45%.

Answer to "are we decisively beating Sargon III?": **No — honest coin
flip.** But it's the first Sargon number with zero asterisks (no fudge
multiplier, no promotion-bug inflation, symmetric Hard-Mode pondering),
and it's robust: gauntlet #3 (1.5×) 52.5%, gauntlet #4 (1.5×, budget-
honest) 48.75%, symmetric 48.75% — all converge on parity. A modern-
technique engine on a 1 MHz 6502 holding EVEN with full-strength Sargon
III is the real result; "decisive" would need more strength (levers
largely exhausted) or many more games to resolve a small edge.

Minor harness follow-ups (do not affect the verdict): (1) screenTokenToCoord
doesn't parse the en-passant capture token "PXPEP" → 1/40 games adjudicated
on the unreadable reply; (2) 8fish runs ~1.08× its nominal per-move budget
(intrinsic iteration-boundary overshoot) — mirrored onto Sargon's ponder,
so symmetry holds, but worth reconciling with the debt-bank conservation
the cutechess path showed (0.998).

## 2026-07-23 — time management HONEST number: +34 ± 32 (the +51 was a ~13% budget overspend)

zellyn asked to confirm we actually honor our own time budget. We did
NOT: the audit logging showed adaptive time management OVERSPENDING its
own-move allotment by ~13% (session own_total/intended = 1.13). Cause:
the per-game bank clamped at 0, so a panic move that ran to the device
hard-abort on a thin bank had the overshoot FORGIVEN instead of clawed
back. The +51 SPRT was measured under this leak — the adaptive side was
quietly using ~13% more compute than flat, so it was NOT an equal-budget
comparison.

FIX (host-side, no asm change): the bank now goes into DEBT (bank int64;
overspends repaid by reduced allocation on later moves), consistently in
BankedClock (bridge/gauntlet), sprt.effortBank, and mirror.EffortBank.
Restores conservation — own-move adherence 1.13 → ~1.0, and the A/B
equal-total-spend is now within **2.18%** (was ~13%).

HONEST RE-SPRT (fixed, genuinely equal budget, 300 games):
**+111 =107 −82 = 54.8% → +34 ± 32, llr 1.12.** So ~17 Elo of the
confounded +51 was the overspend; **+34 ± 32 is the real value of
allocation skill** — still a positive gain (CI ≈ [+2,+66]), just modest
and wide. The +34 supersedes the +51 everywhere. Lesson: audit that a
feature honors its stated budget BEFORE trusting its SPRT — an
equal-budget A/B is only equal if both sides actually spend equally.

## 2026-07-23 — LANDED: adaptive time management (SPRT +51) + endgame mop-up, on-device; FT_ROOKX removed

Both winning levers are now shipped in the asm engine, and the feature
audit was forced (and done) to make room.

- **Adaptive time management (FT2_ADAPT=$04): asm SPRT +51 ± 36** vs
  flat (per-game bank mode, 260 games, CI [+15,+87]) — no mirror
  compression (mirror said +54). **First SPRT-confirmed strength gain
  since futility.** Per-game cycle bank (host) + movable per-move
  ceiling (engine): panic-extend on score drop, extend on best-move
  instability, early-stop+bank on stable moves.
- **Endgame mop-up (FT2_MOPUP=$02)**: phase+material-gated king-driving
  term; mirror-asm parity exact, converts the KRK/KQK shuffling draws,
  no-regression SPRT +2 ± 30 (a conversion win, self-play-neutral).
- **Feature audit (task #31/#1): FT_ROOKX removed.** The two new
  features overflowed MAIN by 169 B (image was at the $BFF0 trap
  ceiling). Removed the rejected gated-off FT_ROOKX rook/blockade eval
  (−19 ± 33, dead since 2026-07-21) — freed 447 B; FT_ASP kept. Its
  state (RKCNT/XSTRUCT) was fully exclusive; the shared pawn masks
  (PWBITS/PBBITS/WBLOCKM/BBLOCKM) were read-only to extraterm, so pawn
  eval is provably unchanged (PStructParity/PTCache green).

Both features OFF = tree-identical (all 24 MicroAB fingerprints
byte-identical across 0x1f/0x07/0x00 + improving; only layout cycles
drift). Image top $BEDA (278 B headroom). engine.bin md5 a41840a4.
FT2_ADAPT relocated $02→$04 (mop-up bit collision). The adaptive win is
the real prize — stacked on gauntlet #3's nominal parity, it's the kind
of gain that could turn the next Sargon gauntlet from a coin-flip into
an edge.

## 2026-07-23 — three strength levers investigated; verdicts (time-mgmt WIN, endgame conversion-win, Texel neutral)

Pushing toward a decisive Sargon lead, three levers screened mirror-side
(cycle-budget, asm-matched):

**1. Adaptive time/effort management — WIN, porting to asm.** A per-game
cycle bank + a movable per-move ceiling (panic-extend on score drop,
extend on best-move instability, early-stop+bank on stable moves).
**+54 Elo vs the current flat budget / +21 ± 9 vs even banking while
spending FEWER cycles** (Pareto), at equal per-game budget. Banking
alone recovers ~15% of budget the soft-start gate wastes (+23); the
aggressive targeting adds the rest. Off byte-identical. asm port +
per-game-budget SPRT in progress.

**2. Endgame mop-up eval — conversion win, porting to asm.** Diagnosis
overturned the hypothesis: KQK/KRK/KRRK/KBBK already convert; the real
leaks are KPK-win, KBNK, and occasional threefold shuffling draws (the
equal-material perpetuals seen in the gauntlets). A phase+material-gated
term (drive losing king to corner + bring winning king close) removes
the shuffling draws, shortens mates, partially recovers KPK (0→2/4) and
KBNK (0→1/4). Byte-identical above the gate (0 diffs / 23849 midgame
positions); self-play −7 ± 32 (NEUTRAL — self-play can't see asymmetric
conversion). A practical/conversion win, not an Elo jump; ported for the
half-points it saves vs Sargon at near-zero risk.

**3. Fresh-corpus Texel retune — NEUTRAL, do not port.** Regenerated the
corpus from current-strength self-play (112,613 rows) and refit: +7 ± 9
cycle-budget (CI [−2,+16], spans zero), same as the 2026-07-21 hybrid
screen. The eval SHAPE, not the weight values, is the ceiling. Bonus: a
real tooling bug fixed — GenerateData/GenerateFENData generated corpora
under UNLIMITED QS, not the shipped recap2 (QS: DefaultQS now); every
prior corpus used a quiescence the engine never plays.

## 2026-07-23 — Sargon III gauntlet #3 (round-4 engine + pondering): 52.5%, point estimate crosses positive

40 games, pool openings (varied), Sargon at 1.5×, 30M-cycle budget —
same conditions as gauntlet #2 (2026-07-20), now with the round-4
optimized engine (−12.6% cycles → deeper search at the same budget)
AND self-pondering on.

**Raw 18W-16L-6D = 52.5% ≈ +17 ± ~110 Elo.** Audited clean: 3
adapter-judgment screen dumps — 2 genuine repetition draws (repTracker
played the drawing move, correctly counted as draws), and 1 promotion-
driver hiccup that was a GENUINE win (we had just promoted a SECOND
queen with check, `A2-A1/Q+`, against Sargon's bare wandering king;
Sargon processed the move on its own screen and replied — the
"submit failed" was only a CTRL-T timing glitch, not a technicality).
No reclassification needed; raw == audited.

Progress vs gauntlet #2's audited 46.25% (−26 ± 100): the point
estimate crosses to POSITIVE for the first time (statistical parity
still — the interval spans 50% — but nominally we're now ahead).
CONFOUND: this reflects round-4 speed AND pondering together; a clean
pondering-only number needs a ponder-off control run (offered).

**Ponder-hit rate: 891/1888 = 47.2%** over the 40 games — rock-steady
(per-game 27–62%, aggregate never strays far from ~48%), and above the
3M-budget validation's 43.7% (deeper search predicts Sargon's replies
better). Roughly every other move inherits a warm TT worth the measured
head start (same depth at 4–12% of the nodes, or +1 ply). BOOK was
inert here (0/40 pool openings are in-book — the pool starts past our
main lines); the book's own value needs a standard-start run.

## 2026-07-23 — pondering mechanics (step 1): warm-TT head start measured, gauntlet measurement pending

Built the pondering machinery bridge-side (Go), no asm change, gated
behind `Bridge.Ponder` (default off, byte-identical). Model: the TT
already carries across moves, so during the opponent's turn we search
the position after our move + our predicted reply, warming the carried
TT; a ponder-HIT then starts the next real search warm.

- Ponder move read FREE from the carried TT's PV 2nd move (matches a
  fresh post-move search exactly on every tested position); shallow-
  search fallback for empty slots.
- **Measured head start on a hit** (nodes to fixed depth 6, warm vs
  cold): startpos 6.8M vs 180.6M (**0.038×**), Ruy 30.2M vs 251.7M
  (0.12×), Sicilian 8.4M vs 158.7M (0.053×); or **+1 ply** for a fixed
  short follow-up. That is pondering's value — measured, not assumed.
- Miss never corrupts (TT always valid): follow-up on a different reply
  returns the identical move/score to cold. Ponder=off byte-identical.

HONEST CAVEAT (the whole verdict hinges on it): this head start is the
per-HIT payoff; realized Elo = (per-hit value) × (ponder-hit rate),
and the hit rate against Sargon is UNMEASURED. Step 2 (the follow-on)
drives this in a Sargon gauntlet: time Sargon's actual think, convert
to an emulated ponder budget, and measure the REAL hit rate — crediting
only real hits, never an assumed rate. The Elo number is meaningless
without the measured hit rate reported beside it.

## 2026-07-22 — opening book: hand-curated, now RESIDENT and probed on-device

The engine is no longer bookless. Two steps, both landed:

**v1 (Go-side probe).** 48 hand-curated sound main-line openings (Ruy
Lopez family, full Open Sicilian, French, Caro-Kann, QGD/QGA, Slav,
the Indians, Grünfeld, Catalan, English, Réti, KIA, Dutch), each with
ECO code + name, every line validated legal move-by-move through
refchess (the generator refuses illegal lines). Blob = 312 entries +
48 names = **3866 B = 47% of the free 8 KB main hole at $2000-$3FFF**.
Keyed on the engine's 32-bit Zobrist, PROVEN == asm HASH0-3 over 311
positions (checked against the actual keys baked into engine.bin).

**On-device (asm probe).** asm/book.s binary-searches the resident blob
at $2000 on the root HASH0-3, sums the equal-key run, weighted-picks
via the dither seed, plays the move directly (no search), and copies
its NAMEID into a 1-byte CUROPENING state ($3D) — the whole "which
opening am I in" cost is that one byte, exactly as scoped. The name
text (1050 B) is display-only and flushes with the book. Proven asm==Go
byte-for-byte over **4194 probes / 264 positions** (incl. weighted
distribution + out-of-book miss). The bookless path is **exactly
zero-cost**: MicroAB grand totals byte-identical to baseline
(3,819,525,749 / adopted 1,666,146,144), because the probe is a
separate driver entry and only shifts page-aligned tables by whole
pages. engine.bin md5 b4066820…; full battery green.

Load path: chesstest.LoadBook pokes the blob into MAIN $2000 (on real
hardware, a one-time disk read into $2000-$3FFF, resident, independent
of the aux-bank TT). Design + coexistence notes in docs/book.md. The
book's real evaluation is a future Sargon gauntlet (variety + avoiding
early bad lines); self-play would show ~nothing, so no SPRT was faked.

## 2026-07-22 — deep optimization review r4 follow-up: the three carried items ALL measure null/blocked — nothing shipped

The r4 integration pass carried three unbuilt cross-file items. All
three were built and measured here; none clears the tree-identity
"cycles only drop per-search at both configs" bar, so the tree is
UNCHANGED (engine.bin md5 3fb25f1b1b546ea84e82b34dd535acd7 == the r4
integration baseline). Honest nulls; each analyzed to root cause below.
Baselines held: MicroAB masks 3,819,525,749; adopted (0x1F+FT2_IMPROV)
1,666,146,144.

**Item 4 — MG/(MG−EG) accumulator pair: MEASURED-NEGATIVE (built,
verified bit-exact, reverted).** Replaced EGSCORE with DSCORE = MG−EG
(gentables emits a D = MG−EG table plane in place of EG; add/rem/move/
take accumulate it identically). Taper reworked to the exact algebraic
identity `score_w = MG − sign(D)·(|D| − (|D|·w>>5))` (proven bit-for-bit:
for D≥0, MG−R = EG+P; for D<0, MG+R = EG−P, R = |D|−P). Every fingerprint
stayed byte-identical (all 24 searches — counts, score, move), confirming
the refactor is correct. But cycles ROSE: masks 3,819,525,749 →
3,826,093,607 (+0.17%), adopted 1,666,146,144 → 1,669,049,886 (+0.17%);
20 of 24 fingerprinted searches regressed (+0.10%..+0.31%), only 4
endgame-heavy ones improved (−0.02%..−0.08%). Root cause: the premise
("the taper subtracts MG−EG every eval") predates the w=32/w=0 fast paths
that already own the majority of evals (w=32 is pure MG, no subtract).
On the taper path, storing D instead of EG does not save: the single
MG−EG subtract that produces D ALSO lands it straight in the multiply
operands, and the common positive-D case then needs no reapply-sign and
gets EG for free (1 post-multiply op). Reconstructing from MG+D always
costs one MORE 16-bit op (R = |D|−P, then MG∓R), and the w=0 pure-endgame
path must now compute EG = MG−D (~15 cyc it did not pay before). The
EG-accumulator form is genuinely optimal; item 4 is dead.

**Item 1 completion — king-shield validity cache: CORRECT but
MEASURED-NEGATIVE (built, all oracles green, reverted).** Cached each
side's king-shield term (a single signed SHLDW/SHLDB byte) keyed on
(king square, the three shield-file dirty bits): pawntermfull recomputes
the shield only when the king moved (square != cached) or a shield file
(kf−1..kf+1) appears in the raw FDIRTY captured at entry (FDSAVE), else
adds the cached byte. Content-addressed (needs no unmake save/restore;
the PTNOCACHE oracle forces FDIRTY=$FF ⇒ always recomputes). Correctness
FULLY verified: TestPStructParity (4045 pos), TestPTCacheIdenticalTree
(identical node counts vs the fresh-recompute build), TestPTCacheRandomWalk
(4060 pos, all scores identical), TestSearchMirrorParity(+Improving) —
all green. But cycles ROSE: masks 3,819,525,749 → 3,819,695,539 (+0.004%),
adopted 1,666,146,144 → 1,666,694,971 (+0.033%). Root cause: the shield
term is too CHEAP to cache. Recompute is ~39 cyc/side (SHIDX ~20 + table
lookup + signed add). A *correct* validity check is ~26 cyc/side (king
compare + FILEBIT→SPREADTAB→&FDSAVE mask test) on top of the same signed
add — so the reuse path is not cheaper than just recomputing, and every
miss pays the check as pure overhead. The shield-file test cannot be
dropped (pawn moves genuinely change shield inputs), so no cheaper
correct variant exists. Design is sound and reusable; the economics are
not.

**Item 5 — color-specialized PSQT pages: BLOCKED on space (not built).**
Pre-flipped+negated black tables (drop the per-op sq^$70 + sign-flip in
add/rem/move) need +3,072 bytes in MAIN (a second 6×512-byte PSQT set).
Current engine.bin is 31,758 bytes: loads $4000, image ends $BC0E, MAIN
ceiling $BF00 → **754 bytes headroom, need 3,072**. Does not fit; not
shrinking other things to force it. BLOCKED.

Net: r4 follow-up ships zero code. The r4 cluster+integration line stands
at adopted 1,666,146,144 (−12.6% vs round-3 7425e66). Take-away for the
next optimizer: eval's taper and king-shield are already at the point
where the obvious "store the difference / cache the sub-result" moves
cost more than they save — the surrounding fast paths and the cheapness
of the terms leave no slack. The pawn-cache stack and its oracle remain
the verified backbone.

## 2026-07-22 — improving-LMR confirmation series COMPLETE: −1.8 ± 8.6 over 4200 games — the cycle screen over-promised

The rolling SPRT confirmation of the adopted improving-LMR feature
(FT2_IMPROV) ran to its 12-batch cap (fresh open-seeds per batch,
pinned engine at 7425e66). Rolling total, newest last: +3.5(600) →
−2.3(1500) → −4.6(3000) → −3.5(3600) → **−1.8 ± 8.6 (4200)**. Final:
+1408 =1362 −1430 = 49.7%.

The full evidence ladder for this feature:
- cycle-budget mirror screen: **+13 ± 9** (4000 games)
- asm SPRT, 4200 games: **−1.8 ± 8.6** — indistinguishable from zero,
  point estimate slightly negative, and **the screen's +13 is outside
  the CI [−10.4, +6.8].**

So even the CYCLE budget — our best screen instrument — carried
residual mirror-to-asm optimism here, the same direction aspiration
showed (cycle −2 → SPRT −21). The methodology ladder holds: the asm
time-budget SPRT is the only truth; every mirror instrument, node OR
cycle, is an upper bound. The feature is statistically neutral and
carries a +0.19% feature-off cycle tax plus code — pending the
adopt/retract decision.

## 2026-07-22 — deep opt r4 integration pass (partial): −3.67% masks / −4.84% adopted, on top of the union

Cross-file wins the clusters proposed but couldn't build across
boundaries. Built (items 1-3 + 6): per-file pawn-term cache with a
dirty-file mask (the biggest item, over-delivered vs its −2/−3%
estimate), emit-side class-presence byte (search skips empty capture
passes), lazy pawnterm (deferred PSTRUCT recompute for subtrees that
never eval), gennode→genrecap direct entry. Union 3,964,939,714 →
3,819,525,749 masks; adopted 1,750,861,350 → **1,666,146,144
(−4.84%)** — every fingerprint identical at both configs.

Not built (Fable budget exhausted mid-pass): the king-shield validity
cache that completes item 1, the MG/(MG−EG) accumulator pair (item 4,
~0.3%), and color-specialized PSQT pages (item 5, ~0.3-0.4% but +3KB —
needs a memory-map check against the post-movegen $B60E top first).
Carried to a follow-up.

Running r4 total (union + this pass) vs the round-3 baseline 7425e66:
adopted config 1,905,559,028 → 1,666,146,144 = **−12.6%**.

## 2026-07-21 — deep optimization review round 4 UNION: −7.67% masks / −8.12% adopted config

All four clusters merged (board 6f97dac, search 214152f, eval e88bcb8,
movegen f65f972 → union b31eb40): MicroAB 4,294,459,755 →
3,964,939,714 (−7.67%); adopted gameplay config (0x1F + FT2_IMPROV)
1,905,559,028 → 1,750,861,350 (**−8.12%**, slightly better than naive
compounding) — every fingerprint identical at both configs. Integration
resolved one ZP collision (EVALVALID $30→$32; ATP2 keeps $30/$31), one
duplicate test name, and removed the now-dead everec.

The eval cluster (no separate entry; from its report): −1.26% adopted —
pawnterm king-only fast path (17.5% of calls, ~55–130 vs ~850 cyc),
eval() fast-path fusion, PDIRTY encoding unified via DIRTYTAB; the
documented per-ply eval accumulator idea measured DEAD (structurally
exclusive eval sites; 1.0% cross-visit cache-hit rate).

Cross-file proposals consolidated for the integration pass: per-file
pawn-term cache with dirty-file mask (est −2 to −3%), lazy pawnterm
(~0.5–1%), emit-side class-presence byte for empty-pass skipping
(≥−0.5%), color-specialized PSQT pages (~0.3–0.4%, +3KB — check MAIN
headroom after movegen's +2KB), MG/(MG−EG) accumulator pair (~0.3%),
gennode→genrecap direct entry (~0.05%).

## 2026-07-21 — deep optimization review round 4, movegen cluster: batched quiet emission, −2.56% total cycles at identical trees

The movegen cluster (generate/generateq/genrecap + the emit interface)
measured at **31.35%** of ALL adopted-config cycles (FEATURES 0x1F +
FEATURES2 0x01, 2-FEN 30M-budget profile) — far above the round-3
"~1-1.5% residual" framing, because that framing counted only the emit
buckets, not the walks. Round-4 changes, every one proven
tree-identical:

1. **Batched quiet emission** (the headline, −2.39% total). The
   full-width copy no longer emits quiets through a subroutine: it
   keeps a running move-stack byte offset in Y and writes the 4-byte
   record inline via `sta (MSP),y` (EMITQY: immediate TIER_QUIET, no
   flags save, no per-emit MSP bump, no jsr/rts), flushing MSP into
   place at ray/handler boundaries and before any classic emit
   (FLUSHY/FLUSHEND + shared flushpage). The batched emit touches no
   flags but N/Z, so the slider carry invariant survives quiet
   emission — the old per-emit `sec`/`clc` re-establishment is gone
   too. A quiet slider emission fell ~88→60 cycles, a step emission
   ~89→63, a single push the same, and a double push became an inline
   EMITFY (~78→43). Stack bytes and their order are UNCHANGED (same
   records at the same addresses); only when MSP advances changes,
   which nothing observes mid-generation. The overflow trap now fires
   at flush granularity: $2000-$207F is documented guard slack.
   Emit census on the profile workload: 42.6k of 52.8k emitmove calls
   were full-copy (mostly quiets) — now ~3.6k classic calls remain
   there (captures), plus 7.7k qs captures and 2.5k genrecap.
2. **Slot-walk merge** (in change 1's measurement): gennext/genloop
   fused — `iny/sty/cpy GLIMIT` with the bound precomputed at entry
   (GLIMIT, reusing the dead GDIR zero-page slot) replaces
   `inc/lda/and #$0F` + reload; the not-done path falls through into
   the dispatch with Y already loaded (−3/slot, +5 setup, net
   ~−43/call).
3. **Emit-interface specialization**: emitmovef now serves only
   ep/castle (Y ignored, fixed tiers TIER_EPCAP/TIER_QUIET — both
   callers land on empty squares); promotions get a dedicated
   emitmovep (fixed TIER_PROMO, no flag-bit tests); promoloop is
   assembled once in movegen.s instead of twice.
4. **QS micro**: GSTEP's empty/own tests restructured (board byte read
   once, `beq next` direct: −2/empty probe, −1/occupied); pawn-cap
   color test off A with the victim Y-load deferred to the actual
   capture path (−2/occupied probe).
5. **genrecap unrolled + side-split** (−0.15% total, honest recap):
   the 16-slot scan is unrolled per side — no loop counter, no SMC
   operand patching, no per-slot inx/cpx/bne — with the diff's +1
   riding a carry-SET invariant (ATT78 holds RECAPSQ+$77 here; every
   live slot's complement-add provably carries; grproc restores C=1).
   Candidates dispatch to a shared grproc (jsr). Measured honestly:
   the per-slot −7 is largely offset by +12 jsr/rts on the
   ATTACKTAB-candidate path (~5/call are geometric candidates), so
   the net is small; kept because it is positive, simpler to extend,
   and biggest exactly where recaptures dominate (tactical FEN's
   cluster share 30.1%→23.3%).

Considered and REJECTED: full-byte pawn dispatch compares (white saves
8/slot but black pays +3 and non-pawns +4 — a wash on average);
inlining the remaining classic emit sites (12-18 cyc × ~14k calls ≈
0.2-0.3% for ~1-2.5KB — poor cycles/byte); SMC absolute stack base
(re-analyzed: abs,y stores never page-cross at stride 4, so it is a
clean −4/emit, but it needs MSP to live in the operand = search-side
contract change, and batching already removed the quiet-emit bump it
would have optimized — see cross-file notes).

MicroAB fixed-depth suite (18 cases, masks 1f/07/00): every
search/make/eval/attacked/ttprobe/generate count, score, and best move
byte-identical to baseline (7425e66); cycles 4,294,459,755 →
4,184,587,625 = **−2.56%** (per mask: 1f −2.53%, 07 −2.42%,
00 −2.84%). Adopted-config fingerprint (same 6 FENs,
depth 6, FEATURES2=0x01, new TestMicroABAdopted): counts/scores/moves
identical, cycles 1,905,559,028 → 1,858,837,508 = **−2.45%**. Cluster
share 31.35% → 29.61%. Full non-short battery green (perft,
RecapGenEquivalence 1280 + RecapGenTreeIdentity, LegalityTorture,
HashConsumptionExact, PStructParity, PTCache, WAC, banked build, etc).
engine.bin +2,048 bytes (28,174 → 30,222; ends $B60E, 2.3KB below the
$BF00 ceiling). New diagnostics: TestMovegenClusterProfile (cluster
share + dup-safe per-label breakdown), TestEmitSites (per-call-site
emit census), TestMicroABAdopted.

Cross-file proposals (NOT done, for the search-cluster owner):
(a) gennode computes RECAPONLY and generateq immediately re-tests it —
export the genrecap entry and let gennode call it directly (~6-8
cyc/qs generate call, ~0.05%);
(b) if the move format ever revisits, the remaining classic-emit
residual (~1.6%) is the place: a 3-byte record (tier|flags fused)
would shave a store per emit and a byte per scan step, but tier bits
are full today.

## 2026-07-21 — deep optimization review round 4, search cluster: −2.1/−2.3% total cycles at identical trees

Round-4 pass over asm/search.s (the five-pass move loop, node init/
teardown, TT-probe glue, killers, QS entry/delta filter, and the
first-ever optimization pass on the improving-heuristic code shipped
days ago). Every change proven tree-identical per search: MicroAB
fingerprints (counts + score + move) byte-identical at masks
1f/07/00 AND — via the new TestMicroABImproving gate — on the ADOPTED
gameplay config (FEATURES 0x1F + FT2_IMPROV), with cycles dropping for
every one of the 24 fingerprinted searches (−1.6% to −3.4%).

Structural changes:
- **Killer consumption (TT-move style):** pass 3 zeroes a searched
  killer's tier byte, so pass 4 needs NO by-value killer compares and
  no killer ZP mirrors — the FT_KILLER-off pass-4 copy became THE
  pass-4 loop (old duplicate deleted), and sloopret's pass-4 re-entry
  no longer refreshes the four killer mirror bytes.
- **THRT tier-byte delta filter:** the QS delta threshold is
  classified ONCE per node into minvictimtype<<4 (6-way ladder on
  alpha−standpat−200, exact wrap semantics preserved), so the per-
  capture filter in passes 1/2 is one unsigned tier compare instead of
  a 16-bit signed victim-value subtract; VV16L/VV16H tables retired
  (−224 table bytes), DELTATL/H arrays replaced by one THRT array.
- **Scout fail-low fast path:** spostsr tests ALPHA+rawSCORE on the
  un-negated child score; a failed-low scout (the common outcome)
  skips the 16-bit negate AND the provably-dead beta-cutoff/alpha-
  raise tests. A zero-window scout fail-high jumps straight to the
  beta-cutoff body (the beta test is a proven cutoff there).
- **EVALVALID → ZP:** the improving heuristic's per-ply "eval
  recorded" array collapsed to one ZP byte ($30) — safe because the
  only write→recurse→read path (failed null) now jumps directly to
  the move loop (its RFP/futility gate walk was a proven no-op at
  remaining ≥ 4); reset is now a blind 3-cycle ZP store (no FEATURES2
  gate), everec inlined at its three sites, and the smimp ply-2 read
  uses the assembled EVALSTK*-2,y base (no X save/restore).
- **Node-kind splits:** gennode/snode split into snodeq/snode +
  gennodeq/gennodef (no QSKIND tests); qs nodes preset SMODE=0 once at
  entry so slegal's per-move LMR chain is skipped entirely for qs
  moves; empty qs capture lists (common) skip both no-op pass scans.
- Micros: PVS scout child alpha via one's complement (−ALPHA−1 = ~ALPHA,
  no carry chain or T0/T1 staging); TT depth cutoff compares rem<<2
  against the packed depth<<2|bound byte (no scratch); NODECNT is a
  countdown divider (dec+bne vs inc/inc/bne); dead second `lda PLY`
  at entry; LMR depth test uses the child PLY directly (remaining−1);
  sret/sretqs drop redundant ldy.

Rejected on measurement: sentinel end-of-list markers for the scan
loops (lists average ~4 scanned moves/pass; sentinel maintenance in
sloopret/gennd2 eats the per-iteration win — net ≈ 0) and fusing the
king-move legality test into the alignment diff (helps middlegames
~−0.1% but regresses king-heavy endgames +0.1%: violates the
per-search cycles-only-drop bar; REVERTED).

MicroAB grand totals (fixed-depth suite, 6 FENs):
| config | before | after | delta |
|---|---|---|---|
| mask 0x00 | 886,254,010 | 870,915,474 | −1.73% |
| mask 0x07 | 1,350,864,262 | 1,322,030,349 | −2.13% |
| mask 0x1f | 2,057,341,483 | 2,011,853,841 | −2.21% |
| masks total | 4,294,459,755 | 4,204,799,664 | −2.09% |
| **0x1f + FT2_IMPROV (adopted)** | 1,905,559,028 | 1,861,907,969 | **−2.29%** |

search.s cluster share (30M-cyc budget profile, adopted config, 2
FENs): 22.83/23.60% → 21.40/21.92% of all cycles (−8.3/−9.2% of the
cluster's own cycles). Memory/contract changes: EVALVALID moved to ZP
$30 (old $0278-$0297 array FREE), DELTATL→THRT at $0D60 ($0D80-$0D9F
FREE), everec in eval.s now dead code (left in place — eval.s not
touched), NODECNT is a countdown rearmed to 128 by checkclock (driver
init 0 still valid: first poll after 256 nodes). Full battery green;
engine.bin md5 c04ff93825c96a1cc557ffadf901a1f8.

Biggest residual (cross-file, for the movegen cluster): an emit-side
"class presence" byte (OR each emitted tier's low nibble into a ZP
flag, ~+6 cyc/emit) would let p0done/p1done SKIP entire empty passes —
pass-1 scans (~3.6% of all cycles) run over lists with no heavy
capture at most quiet-position nodes, pass-2 (~2.5%) likewise for
light captures; est. net ≥ −0.5%.

## 2026-07-21 — deep optimization review round 4, board cluster: −2.46% total cycles at identical trees

Board cluster (board.s make/unmake/attacked + tt.s), measured share
27.35% of adopted-config cycles before the pass, 25.91% after. Every
change proven tree-identical: MicroAB fingerprints (18 cases,
masks 1f/07/00) and the new adopted-config fingerprint (0x1F +
FT2_IMPROV, depth 6) byte-identical in every entry count, score and
best move; only cycles moved.

**attacked(): reversed-attack-table slot filter + measured scan order
(−12.9% of attacked's cycles).** New 375-byte RATTACK table (TABLES
segment, page-aligned): ATTACKTAB with the difference axis reversed
plus a zero tail, so with ZP pointer ATP2 = RATTACK+$77−ATSQ the whole
per-slot filter is ldy PIECESQ+n / lda (ATP2),y / beq next — 12 cycles
per live slot vs 17, no carry discipline, and tombstones (NOSQ indexes
the zero tail) cost 13 with NO separate test. atgeo now takes Y = the
from-square directly (it used to reconstruct it from the diff).
Scan order measured on 336K tapped attacked() operands across three
position sets: greedy frequency-fitted orders do NOT transfer between
sets (bench-fit −26% in-sample but only −17% held-out; corpus-fit −22%
in-sample, −10% on bench), so the adopted order is the principled
"advance" order — white slots 1..15 then king, black 15..0 — which is
stable at −13.3%..−14.1% modeled on every set (FEN slot assignment
scans rank 8 down, so white's low/black's high slots hold the advanced
pieces, the likely attackers). TestRATTackTable (short suite) pins
RATTACK[j] == ATTACKTAB[$EE−j] byte-for-byte against the engine's own
table; attackdiff/attackdist stay green.

**Piece-list liveness metadata: evaluated, honest NULL.** On the
measured distribution dead slots are just too rare and captures too
frequent: tombs/call = 1.86 × 13 cyc ≈ 24 cyc/call ceiling, while slot
compaction costs ~120 cyc per capture make/unmake pair (≈56M over the
suite vs ≈14M recoverable) and a liveness bitmask can't beat the RATT
filter's 5-cycle liveness-included test. The RATT zero tail already
absorbs the old per-slot tombstone branch; nothing cheaper exists to
skip 1.86 slots/call.

**make/unmake micro-batch (−0.78%):** fused the side-to-move hash xor
into the two fast-path HASHSTK saves (kills jsr hashstm, −28/make);
CASTLE==0 skip of the castling-rights mask lookups in both fast movers
(−20/−14 per make once both sides castled, +5 else); UNDOCAPSQ written
only by capture branches; removed the dead BOARD[TO]=0 victim clear
(mover placement overwrites it; takepiece never reads the board);
unmake fast captures write the victim straight onto TO instead of
clear-then-restore; PHASE save/restore moved off the quiet fast path
(only captures/promos change it); ttaddr inlined into ttprobe/ttstore
(−12 each).

**PSTRUCT save/restore made PDIRTY-conditional (−0.35%):** make saves
UNDOPSL/H only when this move set PDIRTY (pawn/king mover or pawn
victim — the only moves that can change PSTRUCT), in the tail right
before pawnterm; unmake mirrors the exact condition from the undo
record (pawn-victim branch, pawn/king-mover branch, all slow moves).
PStructParity (4045) + PTCacheIdenticalTree/RandomWalk green.

MicroAB: 4,294,459,755 → 4,188,647,016 cycles (−2.46%; the three
steps alone: micros −0.78%, attacked −1.33%, PSTRUCT −0.35%).
Adopted config (0x1F + FT2_IMPROV): 1,905,559,028 → 1,862,305,445
(−2.27%). Full battery green: Perft, LegalityTorture, HashConsumption
Exact, HashElisionCoverage, HashConsistency, PStructParity, PTCache,
RecapGen, GiveCheckVerify, AttackedDiff/Distribution, WAC, MicroAB,
banked build. New ZP claim: ATP2 = $30/$31 (was free). engine.bin md5
1338316e99b8b7b038324195309e58cf.

## 2026-07-21 — SEE feasibility design: cheap SEE IS 6502-affordable (1.9% tax) but buys NOTHING — DO NOT PORT; SEE+LMP bundle decisively dead

Full design pass on the long-open SEE question, replacing the old
cost-guess rejection with measured numbers. Key result: **the cost
objection to SEE was wrong, but so was the value hypothesis** — a
losing-capture classifier costing only 1.9% of the cycle budget is
buildable, and it still delivers ~0 Elo, because the nodes it saves are
cheap ones (the same compression that killed node-budget screening).

**Portable design (implemented in the mirror, `internal/mirror/see.go`):**
losing-capture classification in the FIVE-PASS moveLoop (the asm-matched
path), not the orderedMoveLoop sort. A capture reached in capture passes
1/2 is classified at most once; losing ones are deferred to a new pass 7
after the quiets (full-width) or pruned/deferred at QS. Asm shape: rewrite
the move-stack tier class bits to a new class 8 = "losing capture", plus
one more tier-filtered rescan pass (flagged MOVESTACK/PASSNO contract
extension). Classification gate exploits the SEE theorem *victim >=
attacker => SEE >= 0*: only victim < attacker captures (vicVal monotone in
type, so a type compare) are ever classified.

**Variants and measured 6502 costs** (cycle models on the TRUE operand
distribution — 68k gate-passing classifications tapped from asm-matched d6
searches via `SEEProbeHook`; fragment prices from the attacked()-redesign
models, whose adopted modelB matches the emulator within 0.6%:
`internal/chesstest/seecost_test.go`; attacked() re-measured on the current
binary at 388 cyc/call):

| variant | test | cyc/call avg | agreement vs full SEE |
|---------|------|-------------:|----------------------|
| 1 pawn-defended | 2 masked probes | 61 | 62.5% (misses 36% of losers) |
| 2 attacked-defended | 1 attacked(to,enemy) call | 419 (max 1019) | **96.0%** (false-losing 4.0%, missed 0.1%) |
| 3 full boolean SEE | superpiece enum + byte swap | 728 (max 1327) | 100% (verdict = seeValue sign, 0.09% model rounding) |

Gate 30 cyc, rescan 10 cyc/item. **Mode 2 dominates mode 3**: half the
price, same classification quality in practice (its d6 tree is even
slightly smaller). Attacker-enumeration answer (design Q2): the winning
variant needs NO new enumerator — it IS the existing attacked(); genrecap's
slot-scan is the same machinery. A value-ordered enumerator (only needed
for mode 3) would be new superpiece code, and mode 3 is dominated anyway.

**Fixed-depth d6 trees** (asm-matched 0x1f, `TestSEESweepNodes`): atk-fw
(defer at full-width only) **−22.0%** nodes; atk-fw+pruneQS **−49.3%**;
pawn variants −14/−22%. Calls/node ≈ 0.23 (fw-only) to 0.54 (fw+qs).

**Operating point** (143M cyc/move, `TestSEEOperatingPoint`): 4 748
calls/move + 5 875 gates + 19 454 rescan items = 2.36M cyc/move = **1.9%
of spent cycles (1.7% of the 143M budget)** for atk-fw. Affordability was
never the problem.

**Cycle-budgeted screens** (143M cyc/move, tax charged via new
`Costs.SEEGate/SEE/SEERescan`, `mirror match -asee/-aseecost`):

Field, 500 games, seed 6502, ±25: atk-fw **+19**, full-fw +11, pawn-fw+pq
+7, pawn-fw −3, atk-fw+dq −10, atk-fw+pq **−25**, atk-pq **−43**. Every
QS-prune/defer variant is ≤ 0 — the recap2+delta-shaped QS over-prunes
further; SEE-in-QS is dead on arrival.

atk-fw deciding run, 12 × 500-game batches (seeds 11111…99999, 13131,
24242, 35353): +17 +10 +7 +13 +8 +6 +13 −10 −10 +8 −15 −29 →
**+2092 =1844 −2064 over 6000 games = 50.2% ≈ +2 ± 7. NEUTRAL.** (The +19
field and +12-at-2000 were the usual upward flukes; same pattern as
countermove.)

**Untaxed control** (same config, costs 0/0/0, 2000 games, seeds
11111-44444): +698 =620 −682 = **+3 ± 13**. Even a FREE SEE buys nothing:
the −22% fixed-depth node saving is concentrated in cheap nodes (deferred
losing captures spawn quick stand-pat QS children), so a cycle budget
converts almost none of it into depth. **No implementation-cost
breakthrough can reopen this variant — the value side is zero, not the
cost side.**

**Bundle screen (the real prize): atk-fw + LMP(3+2d, Dmax3) vs baseline:
+139 =128 −233 = 40.6% → −66 ± 27 over 500 games. DECISIVELY NEGATIVE.**
LMP at 0x1f was −85; SEE capture-deferral recovers almost none of it. The
LMP enabler in the task-#44 mirror config was HISTORY (quiet ordering),
not SEE (capture ordering) — with quiets still in generation order, LMP
prunes the same good quiets regardless of where losing captures sit.

**Port recommendation: DO NOT PORT — with the strong form of the negative:
SEE is affordable (1.9%) and worthless (+2 ± 7), and it does not unlock
LMP (−66).** What would reopen the ordering-upgrade line is a viable
QUIET-ordering signal (history-class) on the asm, not a cheaper SEE; the
896-byte [piece-type][to] history remains the only untested enabler for
the LMP/LMR pruning stack.

Toggle: `SEEParams{Mode, Margin, DeferFW, PruneQS, DeferQS}`; off (Mode==0)
is a **byte-identical no-op** — the parity gates
(`TestSearchMirrorParity`/`TestSearchMirrorParityImproving`) stay green.
Soundness: `TestSEEOffIsNoop`, `TestSEEDeterminism` (node AND cycle
budgets), `TestSEEMateSound`, `TestSEESweepNodes`, `TestSEEAgreement`,
`TestSEEOperatingPoint`, `TestSEEVariantCost` (chesstest cycle models).

## 2026-07-21 — improving heuristic ADOPTED: first feature shipped through the corrected pipeline

The deciding evidence stack for the improving-LMR port (FT2_IMPROV):
- cycle-budget mirror screen: **+13 ± 9** (4000 asm-matched games)
- asm SPRT #1 (stock openings): −2 ± 31 (300 games)
- asm SPRT #2 (fresh openings via new sprt -openseed): +9 ± 32 (300)
- **combined SPRT: +193 =220 −187 = 50.5% ≈ +3.5 ± 22 (600 games)**

The SPRTs are individually underpowered for a ~+10 effect but agree
with the screen (unlike aspiration, where the honest screen predicted
the SPRT sign). Per the pre-declared rule (combined ≥ 0 given the
screen prior): **ADOPTED** — the gameplay bridge now sets FEATURES2 =
FT2_IMPROV; test harness defaults keep FEATURES2 = 0 so every stored
fingerprint stays baseline-exact. Future screens against the adopted
config should enable improving on both sides (-aimp/-bimp "2,0,1").

This is the first strength feature shipped since futility, and the
first through the full corrected pipeline: faithful mirror →
asm-matched config → cycle-budget screen → port with exact parity →
asm SPRT.

## 2026-07-21 — re-screens under the honest instrument: checks-in-QS CONFIRMED DEAD, bishop pair flips sign but lands neutral

Both pre-fidelity-fix verdicts re-tested at asm-matched 0x1f on the
faithful mirror, deciding runs cycle-budgeted (143M cyc/move):

**Checks-in-QS: DO NOT PORT, strengthened.** Node-budget re-screen
looked deceptively neutral (checks1+safe field +16, promo ≈ −1) but
the cycle budget is decisive: **checks1 −32 ± 13, checks1+safe
−40 ± 13** (2000g each). Mechanism: quiet checks spawn full-width
evasion children — cheap in nodes, expensive in cycles; the node
budget under-counted exactly them. The weak-ordering-revival
hypothesis is rejected.

**Bishop pair: old sign flips, still no port.** Task #18's −12 ± 21
(fixed-depth, pre-faithful mirror) becomes weakly POSITIVE on the
faithful mirror (+11 to +25 untaxed node-budget) but honestly taxed
(25 cyc/eval) and cycle-budgeted over 4000 games: **+3 ± 9 — neutral,
spans zero** (the countermove outcome again). Weight 20 is the target
if ever revisited; ~25 cyc/eval means the cost side is a non-issue.

Also merged: `mirror match -aextra/-bextra` (EvalTerms key:val) and
`-aextracost/-bextracost` (per-eval-call cycle surcharge) — eval-term
screens are now first-class CLI experiments.

## 2026-07-21 — COMPRESSION ROOT-CAUSED: node budgets are systematically biased; CycleBudget is now the standard screen

The diagnosis re-screen of the identical aspiration config (0x1f,
delta 25 asym) under a CYCLE budget (143M cyc/move, 4 seed batches):

**+681 =626 −693 = 49.7% ≈ −2 ± 13 over 2000 games** — versus
**+19–23 ± 13** under the 30k NODE budget, and −21 ± 32 in the asm
SPRT. The cycle-denominated mirror agrees with the asm; the node
budget was the bias.

Mechanism: a node budget charges every node the same price, so a
feature whose "savings" or extra work concentrate in CHEAP nodes
(fail-fast cutoffs, TT-hit-heavy re-searches — precisely aspiration's
profile) converts saved nodes into extra depth at par, while the real
engine only banks the (small) cycle value of those cheap nodes. This
also cleanly explains the historical compression pattern (history
+56→−16, rook +30→−19, recap2 +30→+9.9): node-saving features
disproportionately save cheap nodes.

**STANDING RULE: port-deciding mirror screens use CycleBudget
(-cbudget 143000000 at the 30s-equivalent operating point). Node
budgets are for triage only.** The improving-heuristic verdict
(+13 ± 9) was already cycle-budgeted and stands; its port proceeds.
Checks-in-QS/bishop-pair re-screens switched mid-flight.

## 2026-07-21 — FT_ASP SPRT: REJECTED at −21 ± 32; diagnosis of the 4th compression event underway

The aspiration-windows asm SPRT (0x5f vs 0x1f, same binary, 30s
emulated/move, 300 games): **+89 =104 −107 = 47.0% → −21 ± 32,
llr(0,10) −0.99. REJECTED** — despite a +19–23 ± 13 mirror screen run
at asm-matched ordering on the post-fidelity-fix mirror.

This is the 4th mirror→asm compression event (history +56→−16, rook
+30→−19, LMP +39→−85-at-0x1f, aspiration +19→−21), and the most
diagnostic: implementation drift (parity gates), ordering config
(screened at 0x1f), per-node cost (~zero), and TT size (both 4096)
are all eliminated. Prime remaining suspect: **budget denomination**
— the screen ran under a NODE budget; the asm plays under a
TIME/cycle budget. A cycle-budget re-screen of the identical
aspiration config is running to test exactly this; if the +19
evaporates, node-budget screens overvalue features systematically and
the CycleBudget mode becomes the standard instrument.

Disposition: FT_ASP stays in the binary gated off (feature-bit test
only), like the FT_ROOKX precedent. The port itself is verified
exact-parity vs the mirror, so any future re-verdict needs no new
implementation work.

## 2026-07-21 — full TunedWeights vs shipped hybrid pawn weights: NEUTRAL, keep the hybrid

The mirror-fidelity fix revealed the asm's pawn weights are a hybrid
(Texel passer/isolated; older doubled/shield/open-file) — the full
TunedWeights set was never completely ported. Screened on the faithful
mirror, asm-matched 0x1f, 30k nodes/move, 4 seed batches:

**tuned vs hybrid: 660W 654D 686L = 49.35% ≈ −5 ± 13 over 2000 games.**
(Batches: +4, −1, −8, −13, each ±25.)

Verdict: the unported remainder of TunedWeights buys nothing; the
shipped hybrid stands. No asm change. (Any future retune should
regenerate the corpus from current-strength self-play first — the old
corpus predates the last ~150 Elo of engine strength.)

## 2026-07-21 — improving heuristic — PORT (full-signal LMR only): +13 ± 9 at asm-matched 0x1f over 4000 games, +0.19% eval tax

Added the **improving heuristic** to the shared search pruning block and the
five-pass `moveLoop` (the asm-matched path — TT + two-tier MVV + killers, NO
SEE/history). "improving" = the side to move's static eval at THIS ply beats
its eval at the same side's previous turn (ply-2). When NOT improving, prune/
reduce harder. Static evals are recorded per-ply into `evalStack` by `eval()`
itself (only when the heuristic is active, so OFF is a byte-identical no-op).
Two applications were swept, each under **two signal designs**:

- **(a) RFP/futility**: when not improving, replace the shipped RFP 120/500
  and leaf-futility 120 with a tighter set (screened 60/300, futni 60).
- **(b) LMR**: when not improving, add **+1** reduction ply to late quiets.

- **free-signal** (`Mode=1`): improving read only from evals that already
  exist (QS stand-pat, null-move, RFP); missing ply ⇒ **assume improving**
  (permissive, prune nothing extra). Zero added cost.
- **full-signal** (`Mode=2`): force an eval at every full-width node lacking
  one, so the ply-2 comparison is always available; each **added** eval is
  charged its real ~872-cyc cost via `chargeEval` (a natural null/RFP eval is
  reused, never double-charged).

Code: `internal/mirror/improving.go` + `search.go`/`eval.go`/`engine.go`,
gated by `ImprovingParams`, plumbed through `PlayerCfg` and `cmd/mirror match
-aimp/-bimp`. Wired into the **five-pass** `moveLoop`, not `orderedMoveLoop`
(the LMP/countermove trap).

**Fixed-depth d6 tree effect** (asm-matched, `TestImprovingSweepNodes`,
7-FEN bench, vs baseline):

| variant   | signal | node effect |
|-----------|--------|-------------|
| free-rfp  | free   | −4.24%      |
| free-lmr  | free   | **0.00%** (byte-identical) |
| full-rfp  | full   | −6.28%      |
| full-lmr  | full   | **−21.87%** |
| full-both | full   | −25.73%     |

**free-signal LMR is inert**: LMR reductions fire at deeper full-width nodes
(remaining ≥ 3) where the asm computes NO natural eval, so the ply-2 signal
is essentially never present and the permissive default reduces nothing —
the tree is byte-identical to baseline. Only the **full-signal** (forced
eval) can feed LMR.

**Cost accounting — full-signal eval tax** (`TestImprovingFullSignalEvalCost`,
Mode=2 with no applications = same tree, pure overhead): only **6.2%** of
full-width nodes need a forced eval — **93.8% already compute one** via
null-move (remaining ≥ 4) or RFP (remaining ≤ 2). Over the d6 bench that is
**6,779 added evals**, an eval TAX of **+0.19%** of estimated cycles. The
task's worry about an expensive extra full eval at deep nodes is unfounded:
null-move already evaluates almost everywhere. This tax is **internalized in
the cycle-budget screens below** (every forced eval charged at 872 cyc).

**Field screen (500 games, seed 6502, ±25 Elo)** — free-signal under a
30 000-node budget, full-signal under a matched **143 M cycle/move** budget:

| variant  | signal | budget | +W =D −L      | Elo      |
|----------|--------|--------|---------------|----------|
| free-rfp | free   | node   | +172 =163 −165 | +5 ± 25  |
| full-rfp | full   | cycle  | +178 =150 −172 | +4 ± 26  |
| **full-lmr** | full | cycle | +190 =155 −155 | **+24 ± 25** |
| full-both | full  | cycle  | +182 =154 −164 | +13 ± 25 |

Both RFP applications are neutral (+4/+5); combining RFP with LMR (full-both
+13) is **worse** than LMR alone (+24) — the shipped 120/500 margins are
already tuned, and conditional tightening over-prunes. **full-lmr is the sole
signal carrier.**

**full-lmr promotion to 2000 then 4000 games** (cycle budget, ±13/±9),
matching the countermove protocol (it sat on the bar at 2000, so extended):

| seed  | games | +W =D −L       | Elo     |
|-------|-------|----------------|---------|
| 11111 | 1000  | +378 =315 −307 | +25 ± 18 |
| 22222 | 1000  | +342 =322 −336 | +2 ± 18  |
| 33333 | 1000  | +378 =310 −312 | +23 ± 18 |
| 44444 | 1000  | +340 =324 −336 | +1 ± 18  |
| **4000 total** |  | **+1438 =1271 −1291** | **+13 ± 9** |

Big per-seed spread (+25/+2/+23/+1) — the same variance that fooled the
countermove field screen. But unlike the countermove (which collapsed to
+4 ± 9 spanning zero), full-lmr holds at **+13 ± 9 with the lower bound ≈ +4,
clearly positive.**

**free vs full, plainly** (the deciding question): for LMR, full-signal
(+13 ± 9) beats free-signal (0, inert) by far more than the 0.19% tax → the
answer is **full-signal**. For RFP, full (+4) does NOT beat free (+5); both
neutral → **nothing** ports there.

**Port recommendation: PORT the full-signal LMR application; DO NOT PORT the
RFP application.** Recommended constants:
`ImprovingParams{Mode: 2 (full-signal), LMR: true, LMRExtra: 1}` — no RFP
tightening (`RFP:false`). 6502 cost: an `evalStack` of 2 bytes/ply (~64 B)
plus a forced static eval at ~6% of full-width nodes (the remaining-3 shell,
in-check nodes, and null-skipped nodes — everywhere null-move/RFP don't
already eval). The +13 is measured net of that cost.

Toggle: `ImprovingParams{Mode, RFP, RFPNI, FutNI, LMR, LMRExtra}`; off
(Mode==0) is a **byte-identical no-op** — the parity gates
(`TestSearchMirrorParity`/`TestPStructMirrorParity`) run with it off and stay
green. Soundness: `TestImprovingOffIsNoop` (Mode==0, even with RFP/LMR flags
set, identical move/score/nodes to baseline), `TestImprovingDeterminism`
(same seed → identical replay, node AND cycle budget, all variants),
`TestImprovingMateSound` (exact fixed-depth mate score, every variant),
`TestImprovingFullSignalEvalCost` (added-eval tax), `TestImprovingSweepNodes`
(the d6 node sweep).

## 2026-07-21 — countermove heuristic — DO NOT PORT: neutral (+4 ± 9) at asm-matched 0x1f over 4000 games

Added the **countermove heuristic** to the five-pass `moveLoop` (the
asm-matched ordering path — TT move, two-tier MVV captures, killers, quiets
in generation order; NO SEE, NO history). On a **quiet beta cutoff** the
cutting move is stored as the "counter" to the move made at the PREVIOUS
ply; a later node reached by that same previous move promotes the stored
counter ahead of the other quiets, in its own ordering pass adjacent to the
killer pass. Unlike butterfly history (rejected for the asm: table too
coarse/costly) it needs **no aging and tiny state**. Wired into the
five-pass loop, not `orderedMoveLoop` — the exact trap the LMP work hit
(`internal/mirror/countermove.go` + `search.go`, gated by
`CountermoveParams`, plumbed through `PlayerCfg`/`CMCost` and `cmd/mirror
match -acm/-bcm/-cbudget/-acmcost`). Update stores only quiet cutters and
never after a null move; clearing is per-root-move like the killers
(`CM.Persist` keeps it).

Two table shapes and two ordering slots were swept, all under the
**asm-matched config** (mask **0x1f**, recap2 QS, shipped weights,
corrected-guard RFP 120/500, **30 000 nodes/move**, CM-on A vs CM-off B):
indexing **(a)** `[prev-to-square]` (128 slots, 256 B) vs **(b)**
`[prev-piece-type][prev-to]` (8×128, 2 KB); countermove promoted **after**
vs **before** the killer pass.

**Fixed-depth node counts** (depth 6, asm-matched, `TestCountermoveSweepNodes`,
7-FEN bench): a-after **+2.9%**, a-before **−3.9%**, b-after **−2.0%**,
b-before **+9.1%** (vs baseline). Small and mixed — reordering only quiets
after the captures/killers barely moves the tree; not a node-saver.

**Field screen (500 games, seed 6502, ±25 Elo):**

| variant  | indexing / slot        | +W =D −L       | node-budget Elo |
|----------|------------------------|----------------|-----------------|
| a-before | [to] / before killers  | +210 =126 −164 | **+32 ± 26**    |
| b-after  | [pc][to] / after killers | +188 =158 −154 | +24 ± 25      |
| b-before | [pc][to] / before killers | +181 =163 −156 | +17 ± 25     |
| a-after  | [to] / after killers   | +178 =153 −169 | +6 ± 25         |

**Promotions to 2000 games (2×1000, seeds 11111+22222, ±13):**

| variant  | +W =D −L        | score | node-budget Elo |
|----------|-----------------|-------|-----------------|
| a-before | +747 =573 −680  | 51.7% | +12 ± 13        |
| b-after  | +725 =572 −703  | 50.5% | +4 ± 13         |

`a-before` (the field-screen leader) sat right at the bar (lower bound ≈ 0),
so it was **extended to 4000 games** (adding seeds 33333: −7, 44444: −1):

| a-before, 4000 games | +1438 =1169 −1393 | 50.6% | **+4 ± 9** |
|----------------------|-------------------|-------|------------|

**The +32 field estimate and the +23 first-seed batch were upward flukes.**
At 4000 games the best variant is **+4 ± 9, error bar spanning zero** —
neutral. The story mirrors LMP: a technique that helps engines with rich
ordering delivers nothing under the asm's coarse ordering, where the two
killers already capture most of the local quiet-refutation signal, leaving
little for a countermove to add.

**Cost accounting (cycle-budget screen, the standing rule).** Per-node
probe = one indexed load + 2-byte compare (~20–40 cyc when it fires),
update = a 2-byte store on a quiet cutoff (~20 cyc), wired via a new
`Costs.Countermove` hook (`PlayerCfg.CMCost`, charged only in CycleBudget
mode). At a matched **143 M cycles/move** budget (≈ the 30k-node tree),
CM fires ~3–6 k probes + ~150–1000 stores per move, so at a pessimistic
**40 cyc each** the tax is **~0.1% of the budget**. Taxed a-before over
2000 games (seeds 11111+22222): +732 =595 −673, **+10 ± 13** — essentially
unchanged from the untaxed +12, confirming the feature is genuinely cheap.
But there is no real Elo for the tax to eat: the 4000-game verdict is
neutral either way.

**Port recommendation: DO NOT PORT.** The countermove heuristic is cheap
(256 B for indexing-a, ~0.1% cycle cost) but delivers **no measurable
strength** (+4 ± 9) under the asm's own TT+MVV+killer ordering. Best-case
shape, should it ever be revisited (e.g. after an ordering change that adds
history/SEE): **indexing (a) `[prev-to-square]`, promoted before the
killers** — the leader in every cut, and the smallest table. Recorded as a
negative result; the code stays behind the off-by-default toggle.

Toggle: `CountermoveParams{Indexing, BeforeKillers, Persist}`; off
(Indexing==0) is a **byte-identical no-op** — the parity gates
(`TestSearchMirrorParity`/`TestPStructMirrorParity`) run with it off and
stay green. Soundness: `TestCountermoveOffIsNoop` (Indexing==0 identical
move/score/nodes to the pre-countermove path), `TestCountermoveDeterminism`
(same seed → identical replay across all variants), `TestCountermoveMateSound`
(exact fixed-depth mate score, every variant — reordering quiets never
changes a mate verdict), `TestCountermoveSweepNodes` (the node-count sweep).

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
