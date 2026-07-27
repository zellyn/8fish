# Match & measurement log

Newest first. Engine budgets are emulated time (1.0205 MHz); opponent
controls are wall time. See docs/plan.md for the measurement protocol.

## 2026-07-26 — differential audit: full-game parity CLEAN; two instrument bugs found; Sargon driver had 6 defects

zellyn asked two questions after the tt.s bug: could earlier verdicts be
wrong, and how do we make sure nothing else is lurking? He proposed the
right invariant — "should we make exactly the same plays as our mirror?"

**FULL-GAME PARITY GATE (new, and it is CLEAN).** Complete games, both
engines identical, fixed depth, comparing move + score + FIVE tree
fingerprints (search entries, makes, evals, makenulls, QS nodes) at every
ply, over 142 starts × 2 configs: **4581 plies / 3113 distinct positions /
71.4M emulated nodes, BYTE-IDENTICAL** — not even the ±1 legality-probe
tolerance the old spot-check allowed. The historical 0.9–24× QS gap is
genuinely gone, not papered over. Plus 510 random pawnless endgames, 0
eval mismatches. (Three initial "divergences" were illegal FENs the audit
itself wrote — the loop now rejects an illegal start loudly.)

**QUIRK AUDIT — the process failure named.** When mirror and asm
disagreed we had sometimes taught the MIRROR to imitate the asm rather
than asking which was right; that is how the tt.s bug survived every
gate. 12 quirks reviewed: TTPlyQuirk confirmed dead and DELETED; stale
lore still claimed the asm's futility guard was unsigned (fixed long
ago) — corrected; all SIX mate-zone compares in the image audited and all
now branch signed-first. The doubled-pawn "flat per file" quirk is
INTENTIONAL and self-consistent (Texel tunes against exactly that flat
indicator). STANDING RULE: a divergence is a BUG until proven a design
choice — never model it away.

**TWO INSTRUMENT BUGS FILED (they corrupt measurements, not games):**
1. **PlayerCfg.QS zero-value trap — FIXED HERE.** The zero value meant
   UNLIMITED quiescence, so any screen omitting `QS: DefaultQS` measured
   an engine with a several-times-larger QS tree than the ship. Six
   screens still omitted it (budget/effort/ordering/mopup×2/search) —
   their A/B deltas stay valid (both sides shared the wrong shape) but
   the absolute engine was fiction. It corrupted the Texel corpus once
   already. Zero now means DefaultQS; unlimited is the explicit
   `QSUnlimited` sentinel (CLI "0,0" maps to it, contract preserved).
2. **Budget-mode ID divergence (documented, not fixed).** The asm stops
   ID on a winning mate and hard-aborts at 2× BUDGET behind a predictive
   soft gate; the mirror does neither (1×, cumulative halfway gate). So
   budgeted mirror screens buy a slightly SHALLOWER ID than the asm —
   invisible to every gate we have, because they are all fixed-depth.
   This is a live candidate mechanism for residual mirror→asm compression.

**SARGON DRIVER AUDIT — 6 defects, all conservative.** Root cause of the
17/300 desyncs: Sargon transiently BLANKS and repaints its own move list
~19.3M cycles into its first search after leaving its opening library
(~167K cycles). Commit detection keyed on "column text changed", so a
poll inside the blank frame read it as a commit, skipped CTRL-T, and
scraped Sargon's PREVIOUS move → illegal → adjudicated draw. Fixed by
keying on MOVE NUMBER. Also fixed: move-list numbering restarts at 1
after move 127 (3/3 affected games broke); castle+check tokens never
stripped '+'; 8-char Black tokens truncated; REPETITION/50-MOVE messages
undetected; xboard PXPEP unparseable; Hard-mode/LEVEL-9 computed but
never asserted (600/600 historical games did read correctly).
Validation: 34 games / 446 replies, all new+legal, 0 stale reads.

**BLAST RADIUS — +110 STANDS, and was an UNDER-estimate.** Every harness
failure in 600 games was loud and conservative (adjudicated draw), never
a silently substituted move. Correcting the 18 lost games gives ≈ **+114**;
absolute bounds [+99, +122], inside the ±35 CI. The one path that could
have inflated silently (Easy mode / wrong level) never fired and is now
an assertion.

New permanent instrumentation: Sargon's own displayed move list is
cross-checked against our history EVERY ply, with screen dumps every 10
moves and on every anomaly.

## 2026-07-26 — FULL-GAME asm↔mirror parity + quirk audit: CLEAN BILL, no new engine bugs

Motivated by the tt.s mate-zone bug (below): it survived every parity gate
because when the two engines disagreed, the MIRROR was taught to reproduce the
asm's behavior (`mirror.Engine.TTPlyQuirk`) instead of anyone asking which
side was right. Two deliverables: a much stronger parity gate, and an audit of
every place the mirror models an asm oddity.

### 1. The new gate: `TestFullGameMirrorParity` (internal/chesstest/fullgame_parity_test.go)

Plays **complete games** with both engines configured identically and requires
the same move, the same score, AND the same tree at **every ply**. Fixed depth
(budget/cycle modes are not comparable across a 6502 emulator and a Go model);
dither off; fresh 6502 machine and fresh mirror `Engine` every ply, so there is
no hidden carry-over — each ply is a pure function of (position, halfmove
clock, depth, mask) on both sides. The referee (`internal/refchess`) drives the
game line; a divergence does not end the game, so later plies keep testing.

- Configs: **0x5F** (the shipped gameplay mask, `0x1F|FT_CKEXT`, FEATURES2 0)
  and **0x1F** (the tier every mirror screen is calibrated on).
- Starts: all **40** of `tools/openings-pool.epd` plus **31** hand-picked
  tactical / in-check / pawn-endgame / piece-endgame / near-mate / fortress
  positions.
- Compared per ply: best move, root score, and four exact tree fingerprints —
  `search()` entries, `make()`s, `eval()`s, `makenull()`s — plus (added after
  the first run) the **QS-node count** (`PLY >= MAXDEPTH` at the asm's search
  probe vs the mirror's `Cyc.NodesQS + Cyc.NodesEvasion`).
- Failure output dumps FEN, ply, both moves, both scores, all counters and the
  deltas. `PARITY_DEPTH/PLIES/STARTS/CFG` scale it; the default is sized for
  `make test` (~3 min).

**RESULT — depth 4, 30 plies/game, 142 games: 3551/3551 plies EXACT.**

| metric | exact |
|---|---|
| best move identical | 3551/3551 |
| root score identical | 3551/3551 |
| search-node count | 3551/3551 |
| make count | 3551/3551 |
| eval count | 3551/3551 |
| makenull count | 3551/3551 |
| QS-node count | 3551/3551 |
| make-delta histogram | `+0:3551` |

Coverage: **2,471 distinct positions**, 43 plies scored inside the mate zone,
**43.3 M asm search nodes** emulated (18 min wall on 8 cores). Note the
histogram: not even the ±1 legality-probe tolerance that
`TestSearchMirrorParity` allows was needed — the trees are byte-for-byte.

**RESULT — depth 5, 8 plies/game, 142 games: 1030/1030 plies EXACT** on all
six fingerprints (move, score, nodes, makes, evals, makenull, **QS nodes**),
642 distinct positions, 26 mate-zone plies, 28.1 M asm search nodes. The
exactness is depth-independent, as it should be.

### 1b. The QS-tree exactness check (the old 0.9–24× question)

The mirror's quiescence tree was once 0.9–24× the asm's, and that was "fixed"
by making the mirror's defaults match (`DefaultQS` = recap2). This audit
verified it directly rather than trusting it: the asm's QS-node count is
probed as `PLY >= MAXDEPTH` at the `search` label (read live, because check
extensions mutate MAXDEPTH) and compared to the mirror's. **1030/1030 exact at
depth 5 and 3551/3551 at depth 4** across both masks, on top of the exact
node/make/eval counts.

One measurement trap found and fixed *in the harness*: comparing against
`mirror.Engine.QSNodes` showed deltas of +1..+12 per move on ~53% of plies —
but that counter sits BELOW the hard ply cap and the 50-move / repetition /
insufficient-material early returns, so it undercounts QS-ply nodes that bail
out immediately. The entry-point quantity is `Cyc.NodesQS + Cyc.NodesEvasion`
(charged at the top of `search()`), and against that the counts are exact.
Worth knowing before anyone quotes `QSNodes` as "the QS tree size" again.

**FIRST RUN FOUND 3 DIVERGENCES — all traced to ILLEGAL test FENs, not the
engine.** Three of the hand-written endgame starts had the side NOT to move in
check (`8/8/4k3/8/8/3K4/8/4R3 w`, `8/2k5/8/8/8/3K4/8/2R2R2 w`, and a
kings-adjacent one). At such a root a king capture is pseudo-legal, both
engines' behavior is undefined, and they do diverge (score off by 4 at depth 1,
different QS trees). Root-caused with the new
`TestParityDivergenceProbe` (depth ladder + static eval of the root and every
root child: **no eval mismatch anywhere**, so it was never an eval bug), then
the FENs were fixed and `playParityGame` now **rejects an illegal start
loudly** instead of scoring it as an engine divergence.

### 2. New side-gate: `TestPawnlessEvalParity`

510 randomly generated pawnless endgames (KR/KQ/KRR/KBN/KBB/KNN/KRB/KQR/KN/KB,
both colors, kings never adjacent, illegal positions filtered): **0 static-eval
mismatches** between the asm and the mirror. Pawnless endgames are the eval
shape the fixed FEN suites cover least — no pawn-structure term fires, kings
sit on their back ranks (the open-file penalty applies with no pawns at all),
and PHASE sits on the insufficient-material boundary.

### 3. The quirk audit — every place the mirror models the asm

| # | quirk / modelled behavior | where | verdict |
|---|---|---|---|
| 1 | **TT mate-zone classified with an UNSIGNED compare** (`cmp #$74`) | `asm/tt.s`, modelled as `mirror.TTPlyQuirk` | **BUG** — found and fixed 2026-07-25 (signed zone test). Flag confirmed dead (nothing set it) and **DELETED** this pass, along with its two branches in `ttprobe`/`ttstore`. |
| 2 | **RFP/futility guard with an UNSIGNED compare** (any negative α or β silently disabled the whole block) | `asm/search.s`, modelled as `Fut.CorrectGuard=false` / `FixFutilityGuard` | **BUG** — already found and fixed (task #34: signed guard + RFP re-margined 120/500). The mirror's *doc comments still described the asm as buggy*; corrected. The false branch stays only as the A/B lever that measured the fix (`cmd/mirror -afix`, `PlayerCfg.FixFutility`). |
| 3 | **Null-move β guard with an UNSIGNED compare** | `asm/search.s` snonull | Fixed (comment at search.s:459 documents it). All six `MATEZONEHI` compares in the image now branch on `bmi` first — audited, none left unguarded. |
| 4 | **Doubled-pawn penalty is FLAT per file** (3 pawns on a file cost the same as 2) | `asm/eval.s` `DBLTAB` (12 iff ≥2 rank bits set); `mirror/eval.go` `doubledW++` when `pwcnt>1`; `refPStruct` spec | **INTENTIONAL, and self-consistent.** The Texel tuner tunes `Weights.Doubled` against *exactly this* flat indicator (`Sample.F` comes from `extractPawnFeatures`), so eval and tuning agree; 12 is the best flat coefficient for the indicator. Chess-wise it under-penalizes tripled pawns (rare); changing it to per-extra-pawn would need a re-tune, not a fix. |
| 5 | **A passed pawn is blocked by an enemy pawn at the SAME rank on an adjacent file** | `WBLOCKM` includes the pawn's own rank bit; mirror `max3(pbmax) < r` | **ARGUABLE** (conservative, non-standard: a black pawn on d5 cannot stop a white pawn on e5). Identical on both sides and baked into the Texel features, so it is a design choice, not a divergence. Revisiting it is a re-tune. |
| 6 | **Only the most advanced pawn per file is tested for passed status** | both | INTENTIONAL (standard; avoids double-counting a doubled passer). |
| 7 | **King shield/open-file only when the king is on its OWN back rank** | both + `refPStruct` | ARGUABLE simplification (a king on g2 after h3/Kh2 gets no shield). Identical on both sides. Note the open-file −4 also applies in a totally pawnless position. |
| 8 | **Mate delivered exactly on the 100th halfmove scores as a draw** | `asm/search.s` (comment: "Nuance accepted"), same in mirror | ARGUABLE, documented, identical on both sides. |
| 9 | Lazy legality fast path; conservative `CLSPRES` class bits; conservative PDIRTY deferral | `asm/search.s`, `asm/board.s`, `defs.inc` | INTENTIONAL — pure optimizations with identical results; the mirror deliberately does not model them, and the exact tree match proves they are results-neutral. |
| 10 | "A pseudo-legal node can have captured a king" defensive checks | `mirror/mopup.go`, `endgame.go`, `midgame.go` | INTENTIONAL defensive code. Confirmed unreachable from a LEGAL root (the legality test guarantees the child's side to move cannot capture a king); reachable only from an illegal root, which is exactly where the first run's three false divergences came from. |
| 11 | `DefaultWeights` once drifted from the asm's retuned immediates | `mirror/eval.go` | Historical BUG, already fixed; now matches `refPStruct` exactly. |
| 12 | `DefaultQS` = recap2 is the asm's shape; the **zero value is UNLIMITED QS** | `mirror/engine.go` | The shape itself is INTENTIONAL. But see the finding below — the zero-value trap is live. |

### 4. FILED (not fixed): two model gaps that no parity gate covers

**(a) `PlayerCfg.QS` zero-value trap — mirror-side, affects screens, not the
ship.** `PlayerCfg.engine()` does `e.QS = c.QS` UNCONDITIONALLY, so a screen
that omits `QS: DefaultQS` silently measures an engine with UNLIMITED
quiescence instead of the asm's recap2 — the failure mode `search_parity_test.go`
already names, and the one that corrupted the Texel corpus until the
2026-07-23 `GenerateData` fix. Still omitted today by **`budget_test.go`,
`effort_test.go`, `ordering_test.go`, `mopup_match_test.go`,
`mopup_conversion_test.go`, `search_test.go`**. Their A/B sides share the same
wrong QS so the *deltas* remain internally valid, but the absolute engine they
screen is not the shipped one. A warning is now on the field; the real fix
(treat the zero value as `DefaultQS` and add an explicit `QSUnlimited` opt-in)
changes those screens' numbers and so was left to a deliberate decision.

**(b) Budget-mode ID divergences (mirror vs asm driver).** Every asm↔mirror
gate is fixed-depth, so these are invisible to all of them:
1. `asm/engine.s idok` STOPS iterative deepening as soon as a completed
   iteration returns a WINNING mate; `mirror.SearchBudget` /
   `SearchCycleBudget` / `SearchTimed` keep deepening and spend the budget.
2. The asm's hard abort is **2× BUDGET** with a PREDICTIVE soft gate
   (`now + 2*last-iteration-cost <= BUDGET`); the mirror hard-aborts at **1×**
   with a cumulative halfway gate — so for the same nominal budget the mirror
   buys a slightly SHALLOWER ID than the asm does.
   Both are now documented at `mirror.SearchBudget`.

### 5. Verdict

No new engine bug. Across **4,581 plies (3,551 at depth 4 + 1,030 at depth 5)
/ 3,113 distinct positions / 71.4 M emulated search nodes** at the shipped mask
and the plain mask, the asm and the
mirror are identical in move, score and tree, with zero tolerance. Combined
with the pawnless eval sweep and the re-audited mate-zone compares, the mirror
is currently a faithful model of the shipped engine on every fixed-depth path.
The remaining known infidelities are the two budget-mode gaps above, which are
now written down instead of being discovered again.

**Process rule established** (also in docs/testing.md): when the asm and the
mirror disagree, find out which side is right. NEVER add a flag that teaches
the mirror to reproduce an asm oddity.

## 2026-07-26 — ★★ CONFIRMED DECISIVE across 600 games: +110 Elo (335-151-114)

A second, INDEPENDENT 300-game match — this one STANDARD-START, so both
engines played from move 1 with their own opening books (the mode built
but never measured at scale; the pool positions always started past
8fish's book).

| match | record | score | Elo | 95% CI |
|---|---|---|---|---|
| pool openings | 156−81−63 | 62.50% | **+89** | [+54, +126] |
| **standard-start (books live)** | 179−70−51 | 68.17% | **+132** | [+96, +172] |
| **COMBINED 600 games** | **335−151−114** | **65.33%** | **+110** | **[+85, +137]** |

Both intervals exclude zero by a wide margin; the combined lower bound is
**+85**. 8fish decisively beats Sargon III, confirmed twice under
different opening regimes on fudge-free symmetric terms.

**The book pays.** Standard-start ran ~43 Elo HIGHER than pool. 8fish
played 1018 book moves (7.8% of all moves) and Sargon 2078 instant book
replies (16%); 8fish's book moves cost ~0.24 Gcyc total and bank their
unspent income for the first real search out of book. This is the first
evidence the opening book is worth anything against Sargon — it was
inert in every previous measurement. (Caveat: opening regime and book
effect are confounded here; isolating them would need a book-off
standard-start run.)

**AUDIT — 17 quirk-adjudications (5.7%), ALL resolved to DRAWS.** They
are conservative: they cost 8fish wins, they cannot manufacture them, so
68.17% is if anything an UNDER-estimate. New failure class, distinct
from the fixed promotion bug: ordinary-looking Sargon moves
(`D1XD4`, `D1-E2`, `F3-E5+`) read back as "unreadable/illegal", i.e. a
BOARD DESYNC — most plausibly a mis-scrape of one of Sargon's 2078
instant book replies (the pool run, with almost no book replies, had
exactly 1 quirk). Worth fixing before the next standard-start
measurement; it does not threaten this result's direction.

Adherence 0.9844 (own-move budget), symmetry exact.
## 2026-07-26 — FEATURE AUDIT: FT2_IMPROV + FT2_EGTECH removed, 983 B freed (639 → 1622 B headroom), tree-identical

Both features had been MEASURED and REJECTED but were still shipping in the
image, gated off, costing bytes and a runtime gate walk. The queued on-device
UI milestone (board display + human move input) needs image space, so this is
the FT_ASP/FT_CKVERIFY removal (space round 1) applied to the next two
candidates, with the same verification: byte-identical fingerprints for the
shipped config.

| target | content removed | verdict it was carrying |
|--------|-----------------|-------------------------|
| **FT2_IMPROV ($01)** — improving heuristic | **101 B** (search.s, CODE) | adopted 2026-07-21, RETRACTED 2026-07-22 at −1.8 ± 8.6 / 4200 games |
| **FT2_EGTECH ($08)** — endgame technique | **727 B** (engine.s, TABLES) | −9 ± 24 over 600 SPRT games (neutral), never enabled |
| **TOTAL content** | **828 B** | |
| → CODE address space realized (1 page boundary un-crossed) | **256 B** | |
| **TOTAL image** | **983 B** | |

- **Image: 32,113 → 31,130 B. Top $BD71 → $B99A. Headroom 639 → 1622 B**
  (plus 67 B of CODE fill = 1689 B usable). Segments: CODE $4000-$78BC
  (14,525 B, was 14,626), TABLES $7900-$B958 (16,473 B, was 17,200),
  LCCODE 65 B at $B959. engine.bin md5 `3902502c`.
- **Tree-identical for the shipped config.** All 18 TestMicroAB fingerprints
  (0x1f/d6, 0x07/d5, 0x00/d4 × 6 FENs) — score, best move, and every profile
  counter (search/make/eval/attacked/ttprobe/generate) — byte-identical to the
  pre-removal baseline. FT_CKEXT (TestCheckExtMirrorParity) and FT2_MOPUP
  (TestMopupEvalParity, TestMopupConversion) untouched and green.
- **Cycles: +0.120%** (3,814,106,998 → 3,818,665,555 over the 18 MicroAB
  fingerprints). This is a small LOSS, not the expected win, and the cause is
  not the removed code — the executed instruction stream is strictly smaller
  and the tree is identical, so the only mechanism left is **code-address
  realignment**: search.s is the FIRST include in CODE, so shrinking it by
  101 B slides tt.s / eval.s / board.s / movegen.s / book.s down 101 B and
  reshuffles which TAKEN BRANCHES cross a page (+1 cycle each). The per-node
  gate savings that were recovered are 3 cyc at every full-width node init
  (the blind `sta EVALVALID`), 9 cyc at every full-width node reaching the
  move loop (the `sprepj` full-signal gate), 9 cyc at each null-move-gate and
  RFP eval, and 9 cyc at each reduced late quiet; the realignment costs more
  than that, uniformly (+0.09% to +0.15% per position, largest at mask 0x00
  where the fewest gates ran). Space round 1 crossed the same seam and got
  lucky in the other direction (its TABLES repack moved totals by −64 cyc per
  position). At 0.12% on a node-budgeted engine this is noise; the 983 B is
  the deliverable.
  - **The clawback exists if a future round wants it**: making the CODE
    shrink a multiple of 256 B *before* tt.s (e.g. by relocating ~155 B of
    cold search.s tail into TABLES, the mopfin/mopcd move) would preserve
    every downstream page-crossing exactly. It costs 155 B of image to buy
    back ~0.25%, which is the wrong trade for the UI milestone.

**1. FT2_IMPROV (bit $01) removed.** Five sites in search.s, all in the hot
path, all now gone: the unconditional `sta EVALVALID` at full-width node init
(2 B); the two inlined eval-record blocks after the null-move gate's and the
RFP's natural `jsr eval` (`nonrec1`/`nonrec2`, 22 B each); the `sprepj`
full-signal forced eval + record and its `snodeg` trampoline (25 B — `sprepj`
is now the bare `jmp snode` trampoline the branch ranges need); and `smimp`,
the improving-LMR compare that added +1 reduction ply at a not-improving node
(30 B). Also removed: the EVALVALID ZP byte ($32 is free ZP again) and the
EVALSTKL/EVALSTKH per-ply eval stack ($0238-$0277, 64 B of free RAM again),
plus the stale everec comment in eval.s. The FEATURES2 bit is left RESERVED
and documented in defs.inc. **mirror.ImprovingParams is untouched**, so a
re-port stays a mirror-verified exercise if the verdict ever flips.

Test churn (the aspiration-parity precedent — vacuous tests are deleted, not
skipped): microab_improving_test.go (TestMicroABImproving), improving_effect_
test.go (TestImprovingEffect), search_parity_test.go's TestSearchMirrorParity-
Improving (its own "the feature must change the tree, else this is vacuous"
assertion would now fire), and the two duplicate adopted-config fingerprints
(movegencluster's TestMicroABAdopted, board4's TestMicroABAdoptedProbes — both
ran the MicroAB FEN set at 0x1F+0x01, which is now literally TestMicroAB's
0x1F tier). The surviving diagnostics (TestProfileR4, TestBoardClusterShare/
Fine, TestMovegenClusterProfile, TestEmitSites, TestEvalClusterProfile) just
dropped their `SetFeatures2(m, defs, 0x01)` line.

**2. FT2_EGTECH (bit $08) removed.** The whole TABLES-tail block: `egterm`
(the KingCent term + the single 8-file pass, with EGKPDF expanded twice),
`egpass`/`egadds`/`egadd`/`egcheb`, and the EGPASS (8 B) / EGCD8 (8 B) /
RWIN (64 B) tables. `endterms` — the 25-byte shared PHASE gate that fanned
out to the mop-up and the endgame terms — goes too: with only FT2_MOPUP left,
eval.s calls `jsr mopupterm` directly, which is EXACTLY the pre-port form
(mopupterm re-checks the PHASE gate itself), so the mop-up's own gate walk is
restored bit-for-bit rather than re-derived. eval's feature test drops from
`and #FT2_MOPUP|FT2_EGTECH` to `and #FT2_MOPUP` — same 2 bytes, same cycles,
so the FEATURES2=0 eval path is unchanged. The 6 bytes of per-king
nearest-pawn state at $0213-$0218 fall back into the free driver-scratch
range defs.inc already documents. PWMAX/PBMIN never existed in the asm (the
port read PWBITS/PBBITS directly and the sketch's pawnterm stash was never
built), so pawnterm needed no change at all — verified by the pstruct parity
tests.

TestTablePacking lost its EGPASS/EGCD8/RWIN no-crossing entries; the rest of
the segment is unchanged (TABLES moved down exactly one page, so every
table's page-crossing behaviour is preserved by construction). egterm_parity_
test.go deleted (TestEGTermEvalParity / TestEGTermSearchParity / TestEGTerm-
CycleCost — all vacuous with the bit inert). **internal/mirror/endgame.go and
its mirror-side tests STAY**: they document a real conversion finding the asm
cannot price (draws 96 → 57 in mirror self-play), and endgame.go's ASM PORT
SKETCH now carries the port's own history and measurement so a re-port starts
from the record, not from scratch.

## 2026-07-26 — ★ DECISIVE: 8fish beats Sargon III, +89 Elo (156-81-63, CI [+54,+126])

The 300-game symmetric re-measure with check extensions + the TT
mate-zone fix live. Same fudge-free conditions as the +21 baseline (pool
openings, Hard Mode, both engines pondering exactly the other's measured
cycles, no multiplier), so it is a clean before/after.

**156W −81L =63D = 62.50% → +88.7 Elo, 95% CI [+54, +126].** Nearly a
2:1 winning record. Draw rate 21.0% (was 25.3%). Budget adherence
0.9923; symmetry exact; **1 adjudication quirk in 300 games** (game 279,
resolved to a DRAW, so it cannot have inflated the score) — down from 30
in the first run, thanks to the promotion fix.

**The lower bound is +54, so this is statistically decisive** — the
project's original goal (out-play the program that defined 1980s
computer chess, on a 1 MHz 6502) is met and measured without asterisks.

Trajectory, all audited: −117 (fair conditions, dawn 07-20) → −26 →
+17 → +21 ± 34 (clean symmetric) → **+89 [+54,+126]**.

HONEST NOTE ON ATTRIBUTION: the two shipped features measured +24 ± 23
(check extensions) and +8 ± 22 (TT fix) in self-play SPRTs ≈ +32
combined, while the observed match delta is +9.50pp ± 6.86pp. Those are
CONSISTENT (the delta's interval comfortably contains +32) — so this is
the features' measured value plus favorable variance, NOT evidence of a
2× amplification effect. Do not claim self-play systematically
understates until it is measured directly.

## 2026-07-25 — PASSED-PAWN TABLE: THE "NON-MONOTONE BUG" IS NOT A BUG — NO CHANGE (3 monotone candidates all neutral-to-negative)

Investigation of the suspected weight bug in the Texel-tuned passed-pawn
bonus `{0, 15, 0, 21, 50, 52, 20, 0}` (cmd/gentables/main.go, consumed by
`WPASSB`/`BPASSB` in asm/eval.s pawnterm and by `mirror.Weights.Passed`).
**Verdict: the shipped table stays. Nothing changed in the engine.**

**Indexing (pinned by `TestPassedBonusIndexing`, internal/mirror).** Index
N = the pawn's 0x88 rank for white and `7 - rank` for black, i.e. N =
advancement, chess rank N+1. So index 6 (=20) really is a passer one
square from promoting and index 5 (=52) is the 6th rank. Index 0 and 7 are
unreachable (no pawn stands on rank 1 or 8) and correctly 0. The docs'
`passed2..passed7` names = chess rank = index 1..6.

**Why it is not a bug: the bonus is only a TOP-UP on the PeSTO pawn
PSQT**, which already carries the advancement curve. File-averaged pawn
placement value (base value removed) plus the bonus — the real value of a
passer, `TestPassedEffectiveCurve`:

| chess rank | psqt MG | psqt EG | bonus | eff MG | eff EG |
|---|---|---|---|---|---|
| 2 | −6.8 | 5.9 | 15 | 8.2 | 20.9 |
| 3 | −2.1 | −1.0 | 0 | −2.1 | −1.0 |
| 4 | −1.8 | −0.1 | 21 | 19.2 | 20.9 |
| 5 | 6.9 | 13.8 | 50 | 56.9 | 63.8 |
| 6 | 23.0 | 77.6 | 52 | 75.0 | **129.6** |
| 7 | 75.6 | 159.2 | 20 | 95.6 | **179.2** |

The effective curve is strictly increasing from the 4th rank up in BOTH
phases: PeSTO alone adds +81.6 EG / +52.6 MG between the 6th and the 7th,
so the tuner spent less top-up exactly where the base eval already pays.
The one genuine inversion in the effective curve is rank 2 → rank 3
(20.9 → −1.0 EG), worth ~22 cp, and it is the thing the screens below
tried to fix. `TestPassedEffectiveCurve` now asserts the rank-4-and-up
invariant so this is not re-opened.

**The sparse-data theory is false.** The diversified corpus
(`testdata/texel-rows-2026-07-19.gz`, 108,908 rows = 101,202 self-play +
7,706 pool — it is already the UNION, do not append the 07-18 file to it)
constrains every bucket: nonzero rows per bucket — rank 2 9,206 (8.45%),
rank 3 9,829 (9.03%), rank 4 11,001 (10.10%), rank 5 10,055 (9.23%),
rank 6 9,239 (8.48%), **rank 7 5,443 (5.00%)**. A 1-D loss scan puts each
shipped value at its own argmin (15/0/20/50/50/20 vs shipped
15/0/21/50/52/20), and an unconstrained re-tune from the shipped point
reproduces `{0,15,0,21,50,52,20,0}` **exactly**. Bucket 3's optimum is at
the 0 clamp (the fit wants negative). Raising rank 7 to 110 costs
+0.000288 loss — larger than the entire self-play→diversified re-tune gain
(0.000285) and far outside the tuning entry's bootstrap CI [15, 31].

**Candidates and Texel loss** (K=0.85, shipped loss 0.103654):

| curve | passed table | Δloss |
|---|---|---|
| shipped | {0,15,0,21,50,52,20,0} | — |
| effmono (minimal effective-monotone fix) | {0,15,22,25,50,52,20,0} | +0.000053 |
| mono (monotonicity-constrained re-tune) | {0,15,15,23,52,52,52,0} | +0.000055 |
| handset (task's suggested shape) | {0,10,15,25,45,70,110,0} | +0.000322 |

**SCREEN — cycle-budget self-play**, `-cbudget 143000000`, asm-matched
0x1f + recap2 QS + mop-up ON both sides, candidate weights vs shipped
weights, everything else identical (pure weight change: zero cycle cost,
no tax model needed), seeds 11111 + 22222:

| curve | games | +W =D −L | Elo ± err |
|---|---|---|---|
| effmono | 2000 | +704 =607 −689 | **+3 ± 13** |
| mono | 2000 | +690 =603 −707 | **−3 ± 13** |
| handset | 1000 | +338 =306 −356 | **−6 ± 18** |

(per batch: effmono −12 ± 18 / +17 ± 18; mono +3 ± 18 / −9 ± 18; handset
−0 ± 26 / −13 ± 25.)

Nothing wins. Imposing monotonicity on the base table is neutral at best
and the naive steep curve is the worst of the three — consistent with the
loss table and with the effective curve already being monotone where it
matters. **No asm regeneration, no SPRT.** `asm/tables.s` md5
1a8aa476dd81a82ea27474cf49bfe610 / `asm/engine.bin` md5
c6147d39e799995e16334c49f3492626 unchanged. Gates re-run green:
mirror `-short`, TestPStructParity (4,045 positions), TestPStructMirrorParity
(600), TestTablePacking.

Note for the endgame technique (currently gated OFF at −9 ± 24): its
separate monotone top-up `{0,0,10,20,40,60,100,0}` was justified in that
entry by "pawnterm's table is non-monotone". That justification is wrong
as stated — the base eval's effective curve is monotone — so if that term
is ever revisited, its Pass sub-term needs its own screen rather than
inheriting this rationale.

## 2026-07-25 — TT MATE-ZONE BUG FIXED (asm/tt.s): unsigned `cmp #$74` corrupted every negative score; SPRT +8 ± 22 over 600 games

The bug flagged at the bottom of the check-extension entry, confirmed,
fixed, tested and SPRT'd.

### The bug

`ttstore`/`ttprobe` classified a score by its HIGH byte with an UNSIGNED
compare:

```
        lda TTENTRY+6
        cmp #$74                ; >= +29696: winning mate
        bcc tspneg              ; ... else fall through and ply-shift
```

Winning mates live in `$74xx+`, losing mates in `$80xx-$8Bxx`. But every
NEGATIVE score has a high byte of `$80-$FF`, which is `>= $74` UNSIGNED —
so every negative score took the winning-mate arm and got `+Ply` on store
/ `-Ply` on probe. Two consequences:

1. **The losing-mate arm was unreachable dead code.** It was only entered
   when `hi < $74`, and it then tested `hi >= $80` — impossible. So losing
   mates were re-based in the WRONG DIRECTION (`+Ply` instead of `-Ply`).
2. **Ordinary negative scores were ply-shifted.** A score stored at ply s
   and read at ply p came back off by `s - p`. Worse, a small negative
   stored at ply >= |score| WRAPS NON-NEGATIVE (`-1` at ply 4 is written as
   `+3`), and then the probe's `hi >= $74` test fails on the *stored* value
   so it is never un-shifted: **`-1` in, `+3` out.**

The search uses that score for real: `ttexact` writes it straight to
SCORE, and `ttlower`/`ttupper` compare it against beta/alpha for the
cutoff. So this was wrong cutoffs, not just cosmetics.

**How often it fired** (emulator census, mask 0x1f, 5 middlegame/endgame
FENs at depth 6, 44,093 TT stores / 7,305 probe hits):

| | count | share |
|---|---|---|
| stores carrying a negative score | 23,822 | 54.0% |
| ...wrongly ply-shifted | 23,740 | 53.8% |
| ...that wrapped to non-negative when shifted | 278 | |
| losing-mate stores re-based the WRONG WAY | 82 | |
| probe hits returning a wrongly-shifted score | 3,957 | 54.2% |

A tighter store-side audit (`TestTTMateStoreAdjust`, 3 mating positions)
found **2,128 of 2,762 ttstore ply adjustments wrong** — every one of the
545 losing-mate stores plus 1,586 ordinary negatives.

### The fix

`asm/tt.s:44-69` (ttprobe) and `asm/tt.s:94-114` (ttstore): SIGNED zone
test, the same shape search.s' RFP/null guards already use —

```
ttadj:  lda TTENTRY+6
        bmi ttpneg
        cmp #MATEZONEHI         ; hi >= 0 and >= $74: winning mate, -= PLY
        bcc tthit               ; ordinary non-negative: UNCHANGED
        ...
ttpneg: cmp #NMATEZONEHI        ; hi $80-$8B: losing mate, += PLY
        bcs tthit               ; ordinary negative: UNCHANGED
```

`bmi` splits the sign first, so each arm's `cmp` is only ever reached with
a known sign; the named constants (`MATEZONEHI = $74`,
`NMATEZONEHI = $8C`) replace the bare literals. It is also **4 bytes
smaller and cheaper on the hot path**: the common ordinary-score case is
now `lda/bmi/cmp/bcc` (10 cyc) instead of walking three compares (16 cyc).
No new ZP, no new RAM, no illegal opcodes.

### Effect on the tree

MicroAB (18 rows, masks 0x1f/0x07/0x00): **all 18 scores and all 18 best
moves identical**; node counts changed on 3 of 18 rows (all mask 0x1f: the
two middlegames and the rook endgame), byte-identical on the other 15.
Grand total cycles **3,815,285,469 → 3,814,106,998 (−0.031%)**. The fix is
slightly *cheaper* per position too (mate-in-4 at d8: 683.7M → 637.0M cyc,
−6.8%). These are the new fingerprint baselines.

### Tests added

- `TestTTScoreRoundTrip` (internal/chesstest/ttroundtrip_test.go): hooks
  `tsadj` (ttstore's adjustment point) and `ttpdone` (the instruction after
  `jsr ttprobe`) and shadows all 4096 indices, asserting that every
  store→probe round trip returns the stored score unchanged for ordinary
  scores and correctly re-based for mates. **45/2427 round trips corrupted
  on the old binary; 0/2438 on the fixed one.**
- `TestTTMateStoreAdjust`: audits ttstore's write directly (pre vs post
  bytes) for all three score classes, which catches the losing-mate
  direction error that a same-ply round trip hides. **2128/2762 wrong
  before, 0/2762 after.**
- `asm/search.s` gained one label (`ttpdone`) as the probe-exit test hook.
  Byte-identical image.

### Mirror

`mirror.Engine.TTPlyQuirk` (which modelled the bug) is now OFF everywhere:
the mirror's own signed-correct `ttstore`/`ttprobe` is the faithful model
again. `ckext_parity_test.go` and `egterm_parity_test.go` had the quirk
turned on for the port; both now pass with it off, exact. All four
asm<->mirror tree gates green with the quirk off:
TestSearchMirrorParity, TestSearchMirrorParityImproving,
TestCheckExtMirrorParity, TestEGTermSearchParity.

### The SPRT (fixed vs unfixed, two binaries)

`cmd/sprt -bin <fixed> -binB <unfixed> -a 0x5f -b 0x5f -a2 0x02 -b2 0x02
-budget 30000 -pairs 150` (the shipped gameplay configuration, 30M
cyc/move):

| batch | result | Elo | LLR(0,10) |
|---|---|---|---|
| openseed default (300 games) | +95 =116 -89, 51.0% | +7 +/- 31 | +0.08 |
| openseed 7 (300 games) | +100 =108 -92, 51.3% | +9 +/- 32 | +0.17 |
| **combined (600 games)** | **+195 =224 -181, 51.17%** | **+8 +/- 22** | |

Equal-spend check A/B = 0.9949 (-0.51%) in BOTH batches, so neither side
bought compute.

**Verdict: no measurable Elo either way (+8 +/- 22 over 600 games), which is what a
correctness fix of this shape should look like** — the corruption is a
few centipawns on scores read at a different ply than they were stored,
so it perturbs cutoffs without systematically favouring either side in
self-play. It is adopted on correctness grounds (it is also 4 bytes
smaller, 6 cycles cheaper per TT adjustment, and -0.03% cycles overall),
not because self-play could price it. The classes it protects against —
a losing mate re-based the WRONG WAY, and `-1` coming back as `+3` — are
exactly the blunder-in-a-decided-position failures self-play at equal
strength cancels out.

### Position-level A/B (23 mate/tactical/endgame FENs, old vs new binary)

Best move and score identical on 22 of 23, including every mate score
(mate-in-3 f6a6/29995, back-rank a1a8/29999, smothered h6f7/29999,
WAC.005 c6c4/29997, WAC.012 g4f3/29997, "mated" side -520/-1005/-204 all
unchanged). The one divergence is KQK at d6 (d2e3/1012 -> d2c1/1010) — a
tree change, 19% cheaper. So the fix does not move the engine's reported
mate distances on these probes; what it removes is the silent few-cp
corruption of ordinary negative scores that was steering cutoffs.

## 2026-07-25 — CHECK EXTENSIONS ADOPTED (+24 ± 23 over 600 SPRT games); ENDGAME TECHNIQUE neutral, stays off

Both features ported behind bits (FT_CKEXT=$40 reusing the freed FT_ASP
bit; FT2_EGTECH=$08), then SPRT'd separately so a loser could be dropped
independently. Off-path tree-identical (30/30 fingerprints byte-identical;
+0.11% cycles = the ckext gate test). Image 32,113 B, top $BD71, 639 B
headroom — both fit thanks to SPACE round 1.

**CHECK EXTENSIONS: ADOPTED.** Two independent 300-game SPRTs at 30M/move:
+107 =96 −97 (+12 ± 33) and +113 =106 −81 (+37 ± 32) → **combined
220W-178L-202D = 53.50% = +24.4 Elo, CI [+2,+47]**. NO mirror→asm
compression (screen said +12.6 ± 9.0) — unusual, and consistent with the
middlegame-eval finding that the horizon-blind losses are a SEARCH
problem. Now enabled in gameplay (the bridge sets 0x1F|FT_CKEXT); tests
keep the plain 0x1F default so fingerprints stay exact. Cost: 6 cyc per
child search; the extra nodes are taxed honestly by the budget.

**ENDGAME TECHNIQUE: NEUTRAL — stays gated OFF.** Two independent
300-game SPRTs: +121 =57 −122 (−1 ± 36) and +98 =89 −113 (−17 ± 33) →
**combined 219W-235L-146D = 48.67% = −9 ± 24**, consistent and not
positive. Its mirror
+10 ± 9 was flattered by a 3×-understated cost model: the real cost is
**+1278 cyc/gated eval = +30.9% cycles/node in endgame searches**, vs the
438 the screen charged (one optimization pass already cut it 45%). Same
pattern that sank FT_ROOKX. Middlegame cost is zero (it shares the
mop-up's gate). NOTABLE SIDE EFFECT worth remembering: same openings,
draws fell **96 → 57 (−40%)** — the endgame knowledge really does break
the shuffling draws, it just converts them SYMMETRICALLY in self-play, so
self-play cannot price it. If it is ever revisited, the test that could
show its value is an asymmetric one (vs Sargon), not self-play — but the
+30.9% endgame-node cost would have to be paid there too.

**BUG FOUND (pre-existing; FIXED same day — see the entry above):** asm/tt.s
classifies mate scores with an UNSIGNED `cmp #$74`, so every NEGATIVE TT
score is ply-shifted (+Ply on store / −Ply on probe) and the losing-mate
branch is dead code. Modelled exactly (13k-node traces match byte-for-byte)
as mirror.Engine.TTPlyQuirk (default OFF) so the new parity gates could be
written. Fixing it CHANGES THE SHIPPED TREE, so it is a real search-side
candidate with its own SPRT — and per the eval-ceiling finding, search is
where the remaining Elo is.

## 2026-07-25 — PORTED to asm: check extensions (FT_CKEXT) + endgame technique (FT2_EGTECH); SPRTs +12 +/- 33 (ckext, no compression) and -1 +/- 36 (endgame, neutral)

Both mirror-validated features from the same day's screens are now in the
6502 image, each behind its own bit, each mirror-asm parity-exact, and each
SPRT'd separately at the 30M-cyc/move operating point.

**Bits and state claimed** (asm/defs.inc):

| feature | bit | RAM claimed |
|---|---|---|
| check extensions | `FT_CKEXT = $40` (FEATURES; the freed FT_ASP bit) | `NUMEXT = $0233` (1 B, ex-FT_ASP scratch) |
| endgame technique | `FT2_EGTECH = $08` (FEATURES2) | `$0213-$0218` (6 B, ex-FT_ROOKX scratch): per-king nearest-pawn state |

Tables added (TABLES tail, engine.s, ahead of the code so none of them
crosses a page): `EGPASS` 8 B (the advancement top-up), `EGCD8` 8 B
(per-coordinate centre distance), `RWIN` 64 B (rank-window masks for the
nearest-pawn scan). ZP: both features reuse only eval-tail-dead bytes
(T0-T3, EVTMP, MULCNT, MUL0-MUL2, PSQSQ, PSQPIECE, PSP0+1/PSP1+1 — the PSQT
pointer HIGH bytes, whose lo bytes stay 0); no new ZP.

**Image: 31,130 → 32,113 B. Top $B99A → $BD71. Headroom 1622 → 639 B.**
Check extensions cost 46 B of code but +256 B of image (CODE crossed a page
boundary, shoving page-aligned TABLES up); the endgame term is 727 B
(code + 80 B of tables). engine.bin md5 `9cb22bb1`.

### 1. Check extensions (FT_CKEXT)

Mirror design ported verbatim (`internal/mirror/search.go` `checkExt`,
`CheckExtParams{MaxExt: 1}`): when the move just made gives check, the child
subtree is searched one ply DEEPER (`inc MAXDEPTH`, balanced restore),
capped at ONE extension per root-to-leaf path (`NUMEXT`, incremented and
decremented in lockstep so a scout re-search re-derives the same decision).
The hook is `asm/search.s:snored` — one `lda INCHK,y / bne` on the child-
search path, so the whole cost when the bit is clear is 6 cycles per child
search. Never in quiescence: the gate is `QSKIND[parent]`, so a qs capture
node's children never extend while an in-check EVASION node past the horizon
does — exactly `checkExt`'s `qsKind[ply]` test. Extension and LMR reduction
are mutually exclusive by construction (the reduction gate already requires
`!INCHK[child]`), and the block is only reachable from the mode 0/1 path,
so it cannot even be expressed.

### 2. Endgame technique (FT2_EGTECH)

All six screened terms, gate `PHASE <= 6`, hooked into eval beside the
mop-up (`asm/eval.s:evrookx`); the two engines' gates are FUSED into one
compare (`endterms`), so a middlegame eval pays the same 7 cycles it already
paid for the mop-up alone and nothing more.

The design sketch's PWMAX/PBMIN stash in pawnterm was NOT needed and is not
used: `PWBITS/PBBITS` are maintained by `pbtoggle` on every pawn placement
change independently of FT_PSTRUCT and of pawnterm's lazy PDIRTY deferral,
so they are always current at eval time, and the per-file most-advanced rank
is the index of the highest (white) / lowest (black) set bit of one byte — a
5-byte shift loop, run only for an actual passer. That also keeps the
nearest-pawn distance EXACT on doubled files, which the sketch's
most-advanced-only shortcut would not have been (mirror `egScan` minimizes
over ALL pawns).

One loop over the 8 files does everything: both kings' nearest-pawn
distances and both colors' passer terms. The nearest-pawn scan is a
per-file rank-window walk (`RWIN[kingrank*8 + d]`) with two prunes — a file
whose |file-kingfile| already reaches the best-so-far cannot improve it, and
the window only grows while d < best — so it is O(pawn files), never the
O(d²) box expansion the first cut used.

### Verification

- **BOTH FEATURES OFF = tree-identical.** All 30 MicroAB / MicroABImproving /
  MicroABAdopted fingerprints (score, move, and every profile counter —
  search/make/eval/attacked/ttprobe/generate) byte-identical to the
  pre-port baseline across 0x1f/0x07/0x00 + improving + adopted. Cycles
  +0.109% (7,136,729,488 → 7,144,523,847): the check-extension gate's
  `lda INCHK,y / bne` at every child search. The endgame term adds ZERO to
  the off path (its gate compare replaced the mop-up's, one for one).
- **Check-extension mirror parity, feature ON** (`TestCheckExtMirrorParity`,
  14 FENs, mask 0x1f|0x40, depth 5): same best move, same score, same make
  count as `mirror` with `CheckExt{MaxExt:1}` on every FEN; active (tree
  changed) on 13/14; total makes +10.5%, matching the mirror's screened
  +11.5…+26.7% blow-up. `TestCheckExtPathBudget` pins NUMEXT <= 1 throughout
  the search and 0 on exit (the asm analogue of
  `TestCheckExtNumExtBalanced`).
- **Endgame-term eval parity** (`TestEGTermEvalParity`): asm static eval with
  FT2_EGTECH ON == `mirror.Eval()` with `EG = DefaultEndgame`, exact to the
  centipawn, over 30 curated positions (24 of which fire: KPK both ways,
  doubled/tripled files, 2nd- and 7th-rank pawns, mutual blockades, both
  colors' passers, rook/bishop endgames, pawnless KQK/KRK/KBNK, edge files)
  + 3022 random-play positions + 2789 positions from random play out of the
  curated endgames. Also exact with the mop-up on at the same time (the
  fused-gate path).
- **No middlegame leak, asm side:** every one of the 3022 above-gate
  positions has ON eval == OFF eval — 0 leaks (the mirror proved the same
  over 35,567).
- **Endgame-term tree parity** (`TestEGTermSearchParity`, 8 endgames, depth
  5): move + score + make count identical to the mirror with
  `EG = DefaultEndgame`, and the term changed the search on 8/8 — so the
  dozen borrowed eval scratch bytes provably clobber nothing.

### A real asm bug found on the way (pre-existing, NOT fixed here)

The check-extension tree parity failed at first — not because of the
extension, but because the deeper tree finally exposed a defect in the TT's
node-relative mate bookkeeping. `asm/tt.s` classifies scores with
`lda scoreHi : cmp #$74 : bcc ...`, an UNSIGNED compare, so **every NEGATIVE
score takes the winning-mate path** (+Ply on store, -Ply on probe) and the
losing-mate branch is dead code. The round trip is self-consistent at a
fixed ply, so nothing is corrupt at the storing node — but an entry stored at
ply s and read at ply p is off by s-p centipawns whenever it is negative,
which flips the occasional cutoff.

Node-and-TT-store traces of both engines (13k nodes) matched byte for byte
once the mirror was taught the same quirk, with and without extensions. It
is therefore modelled behind `mirror.Engine.TTPlyQuirk` (default OFF, used by
the two new parity gates) rather than fixed: fixing the asm changes the
shipped tree and so needs its own SPRT. **Follow-up task: fix the signed
compare in tt.s ttstore/ttprobe, then SPRT it** (and flip TTPlyQuirk's
default, or delete it, depending on the verdict).

### Cost

| measurement | value |
|---|---|
| check extensions, bit CLEAR | 6 cyc per child search (+0.109% over the 30 fingerprints) |
| check extensions, bit SET | no per-node cost knob: it is paid in nodes (+10.5% makes at d5) |
| endgame terms, gate SHUT (middlegame) | ZERO on top of the mop-up: the two bits share one gate compare (+19 cyc/eval vs FEATURES2 = 0, which is what the mop-up alone already cost) |
| endgame terms, gate OPEN | **+1278 cyc/eval** (24-FEN mean; +400 pawnless, +1250 typical K+P, +4200 in a 6-passer position) |
| endgame terms, whole search | **+30.9% cycles/node** (8 endgames, depth 5, ON vs OFF) |

The eval itself is only ~137 cycles in an endgame (the accumulators are
incremental), so this term is the dominant eval cost when it fires. One
optimization pass already cut it 45% (a first cut measured +1980 cyc/eval and
+2100 of that was an O(d²) box-expansion nearest-pawn scan; the shipped
version is the O(pawn files) rank-window walk, plus a table-driven CMD and a
fused single file pass). It is still ~3x the 438 cyc/gated-eval the mirror
screen CHARGED for these terms, so the screened +10 ± 9 is an OPTIMISTIC
bound for the ported version, not the pessimistic one the sketch predicted.
### The SPRTs (the real gate)

Same binary both sides, `cmd/sprt`, 30M cyc/move (`-budget 30000`), 150
opening pairs = 300 games, mop-up ON on both sides (the shipped engine, and
the configuration the endgame mirror screen used):

| feature | A vs B | result | Elo | LLR(0,10) |
|---|---|---|---|---|
| **check extensions** | `-a 0x5f -b 0x1f -a2 0x02 -b2 0x02` | +107 =96 -97, 51.7% | **+12 +/- 33** | +0.24 |
| **endgame technique** | `-a 0x1f -b 0x1f -a2 0x0a -b2 0x02` | +121 =57 -122, 49.8% | **-1 +/- 36** | -0.19 |

Equal-spend check: A/B total cycles 1.0072 and 1.0062 - both sides really did
get the same compute, so neither number is a compute artifact.

**Check extensions: PASS as a no-regression with the screen's sign and
magnitude intact.** +12 +/- 33 over 300 games is not significant on its own,
but the point estimate lands on top of the mirror's +12.6 +/- 9.0 (4000
cycle-budgeted games) - i.e. NO mirror-to-asm compression, which is the
failure mode that killed aspiration (cycle -2 -> SPRT -21) and improving-LMR
(screen +13 -> SPRT -1.8 over 4200 games). Combined with the cost profile
(6 cycles/node when clear, zero per-node cost when set - the extension is
paid purely in nodes, which the time budget already taxes honestly) this is
the cheapest positive-EV feature the project has landed since futility. A
rolling confirmation series is the way to make it significant; the mirror's
4000-game screen already carries most of the evidence.

**Endgame technique: NEUTRAL - stays gated OFF.** -1 +/- 36 is
indistinguishable from zero, and the mirror's +10 +/- 9 does not survive the
port. The mechanism is measured, not guessed: the mirror screen charged 438
cyc per gated eval, the port really costs ~1278 (+30.9% cycles/node in
endgames), so the feature buys its knowledge at ~3x the screened price. Two
independent reasons the self-play number under-reports it remain on the
record (the mirror's both-sides control fell from +2.08 to +0.80 ns, and the
conversion component is asymmetric by construction) - but this SPRT is the
gate, and the gate says no. It ships in the image behind FT2_EGTECH, OFF.

**One measured side effect worth keeping.** The two SPRTs share their
openings and their B side, so their draw counts are comparable: 96 draws with
check extensions on one side, **57** with the endgame terms on one side (-40%).
The endgame knowledge is demonstrably breaking the shuffling draws it was
designed to break - it just converts them into wins and losses in equal
measure against a twin that plays the same endgames. That is exactly the
signature the mirror's conversion suite (SIG+, vs a knowledge-removed
defender) versus its both-sides control (ns) predicted.

### Next steps (recorded, not done here)

1. **Fix tt.s' unsigned mate classification** (above) and SPRT it: it is a
   real cross-ply TT score corruption, cheap to fix, and it currently forces
   `mirror.TTPlyQuirk` on every asm-vs-mirror tree gate.
2. **Cheapen the endgame terms and re-SPRT.** The remaining hot spots are
   measured: ~310 cyc per passer (two Chebyshev distances + four sign-extended
   adds - the front-square distances could reuse the |file-kingfile| the
   nearest-pawn scan already computes, and the four adds collapse to two
   byte-sized ones) and ~50 cyc + 17/step per pawn-file per king in the
   nearest-pawn scan. A 2x cut would put the port at the screened price.
3. **Or gate the terms harder** (e.g. only when a passer exists, or only at
   PHASE <= 4) and re-screen: the mirror can answer that in minutes.
4. A rolling confirmation series on FT_CKEXT, batched with fresh open-seeds,
   is what turns +12 +/- 33 into a decision.

## 2026-07-25 — SPACE optimization round 1: 1344 B freed (278 → 1622 B headroom), tree-identical

The first space-only pass over the image (all four prior deep-optimization
rounds traded space FOR cycles). Motivation: 278 B of headroom below the
$BFF0 harness-trap ceiling and two validated ports (check extensions +
endgame technique) needing ~400-450 B.

| source | bytes freed |
|--------|-------------|
| **FT_ASP removed** (rejected feature, still in the binary) | **322** (CODE content) |
| **FT_CKVERIFY made assembly-time** (`.ifdef CKVERIFY`) | **24** (CODE content) |
| mopfin/mopcd relocated CODE tail → RATTACK's alignment hole | 48 (CODE content, into pre-existing TABLES fill) |
| → CODE address space realized (2 page boundaries crossed) | **512** |
| **TABLES repack** (969 B of alignment fill measured, 880 reclaimed) | **832** (net, after absorbing mopfin/mopcd's 48 B) |
| **TOTAL** | **1344** |

- **Image: 32,474 → 31,130 B. Top $BEDA → $B99A. Headroom 278 → 1622 B.**
  Segments: CODE $4000-$78F7 (14,584 B, was 14,978), TABLES $7900-$B958
  (16,473 B, was 17,305), LCCODE 65 B. engine.bin md5 `8231f781`.
  Plus 89 B of leftover RATTACK-region slack and 8 B of CODE fill = 1719 B
  usable in total.
- **Tree-identical**: all 30 MicroAB / MicroABImproving / MicroABAdopted
  fingerprints (score, move, and every profile counter: search/make/eval/
  attacked/ttprobe/generate) byte-identical to the baseline across
  0x1f/0x07/0x00 + improving + adopted.
- **Cycles: −0.178% total** (7,149,463,403 → 7,136,729,488 over the 30
  fingerprints), i.e. a small WIN, not a regression. Nearly all of it is
  FT_CKVERIFY: its `lda FEATURES / and / beq` probe ran at every
  full-width node (6 cycles/node ≈ 0.19%); the TABLES repack itself moves
  the totals by −64 cycles per position (one cold-path page crossing in
  the relocated LC-install/driver code), i.e. 1e-5%. Every table's
  page-crossing behaviour is preserved by construction (below).

**1. FT_ASP (bit $40) removed.** Aspiration windows lost their own asm SPRT
(−21 ± 32, 2026-07-21) and had been sitting in the image gated off ever
since, on the FT_ROOKX precedent. Removed `aspiterate`/`aspbump`/`aspfull`/
`itsearch` (engine.s), the FT_ASP bit, ASPDELTA, and the ASPAL/AH/BL/BH/
ASPFAIL state at $0233-$0237 (now free driver scratch again). The
budget-mode ID driver's `jsr aspiterate` goes back to `jsr iterate` — the
plain path, which performed the identical PREV* snapshot plus the identical
per-iteration root setup, so this restores the exact FT_ASP-off iteration
path (proved by the fingerprints). FT2_ADAPT's hooks (`adaptmaybe`,
`adaptaborthi`) are untouched and still fire from the same points in
`idloop`. internal/chesstest/aspiration_parity_test.go deleted (vacuous
once the bit is inert); mirror.aspIterate is untouched, so a re-port is
still a mirror-verified exercise if the verdict ever flips.

**2. FT_CKVERIFY (bit $80) is now assembly-time optional.** The
gives-check cross-check is a pure debug assertion; it was shipping in the
image and costing 6 cycles at every full-width node. Now behind
`.ifdef CKVERIFY`, built only by `ca65 -D CKVERIFY`
(asmbuild.BuildVariant, the PTNOCACHE/RKNOCACHE pattern);
TestGiveCheckVerify builds that variant and is unchanged otherwise. The
FEATURES bit stays reserved so the variant's encoding is unchanged.

**3. TABLES repack — the unexplored seam.** TABLES (17,305 B) was LARGER
than all the engine's code and had never been audited. Findings:

- **No dead tables.** All 42 tables have live readers (checked symbol by
  symbol); VV16L/VV16H were the only orphans and were already gone in r4.
- **Alignment fill was the win: 969 B of pure `.align` padding** inside
  TABLES, never before measured — 240 B after TYPEATK2, 232 after
  PHASEVAL, 137 after RATTACK, 128 each after WBLOCKM and FILEBIT, 104
  after ORTHOOFF — plus 126 B of CODE-side fill between CODE's end and the
  page-aligned TABLES base.
  cmd/gentables now emits the tables in groups whose sizes are each an
  exact multiple of 256, with every unaligned small table packed into one
  gap-free run at the tail:
  `A` 12×256 B page-aligned tables · `B` the four 512 B quarter-square
  tables · `C` RANKBIT(128)+WBLOCKM(256)+FILEBIT(128) · `D` PSQT 12 pages
  · `E` ZKEYS 24 pages · `F` CASTLEMASK(128) + all 19 unaligned small
  tables + hashstm (413 B). Fill in the whole segment is now **zero** except the
  RATTACK region, where `mopfin`/`mopcd` moved in from CODE's tail (48 of
  its 137 B).
- **Cycle-identity rule** (the reason this is free): an `abs,x`/`abs,y`
  read costs +1 cycle when base+index crosses a page, so every table keeps
  its exact crossing behaviour. Verified table by table: all 35
  index-addressed tables have an unchanged crossing count, WBLOCKM keeps
  its offset $80 (indexed 0..255, so it always crossed — deliberately NOT
  "fixed", which would have made pawnterm cheaper than every measurement
  on record), and the pointer/SMC-addressed tables (PSQTBASE, ZKEYS,
  SQRLO/HI, ISQLO/HI, RATTACK, TIERTAB) stay page-aligned. Every table's
  bytes are byte-for-byte unchanged except the five that HOLD page numbers
  (ZKHI0/TYPEPAGE0/1/TYPEPG0X/1X), which correctly track the new
  addresses. New TestTablePacking pins all of it, plus the $BFF0 ceiling.
- The Zobrist PRNG draw order is deliberately preserved (keys are drawn in
  the original order and emitted in the new one), so no hash value moved.

**Deliberately NOT done** (measured, quantified, declined — the ledger for
the next time space runs out):

- **Merging SQR/ISQ into one 320-entry table pair: 1408 B.** ISQ[i] =
  f(i−32) and SQR[i] = f(i) are the same function shifted, so one table
  can serve both if `evmul` indexes ISQ with X = −w instead of 32−w (bases
  then differ by exactly one page, both still page-aligned). Costs ~+2 to
  +4 cycles per eval in added page crossings — a real hot-path regression
  (~0.1%), so declined while 1622 B of headroom exists. Note the four
  tables are also only 288/512 entries used (max index 287 = Dlo 255 +
  PHASEWX's capped w 32); truncating alone buys nothing, because the
  freed 224 B per table just becomes alignment fill with no partner left
  to fill it.
- **ZKEYS' 3072 structural zero bytes** (half of every 128-byte 0x88
  plane is off-board files). Removing them needs a compressed square
  index in make/unmake's hash update = hot path. Declined.
- **Deriving the PSQT EG plane from MG+D, or black planes by flip**: both
  add work to the hottest eval loop for 1.5 KB. Declined.
- **Generating derivable tables at init into free RAM** (RATTACK is
  literally ATTACKTAB reversed; SQR/ISQ are closed-form): there is no free
  MAIN RAM. $2000-$3FFF is D7-reserved for hires page 1 and already holds
  the resident opening book, $0400-$07FF is the text screen, and the rest
  of $0200-$0DFF is dense engine state. Init-generating a table into its
  own space saves FILE bytes but not the RAM footprint, which is the
  binding constraint ($BFF0 ceiling). Dead end — recorded so nobody
  re-derives it.

Full internal/chesstest battery green. `make engine` + `make perft` clean.
## 2026-07-25 — MIDDLEGAME EVAL TERMS (mirror): king safety FAILS (-19 +/- 13 even UNTAXED), positional set +13 +/- 7 but its own halves do not reproduce it — DO NOT PORT YET

The two biggest loss buckets from the same-day loss diagnosis (positional
squeeze 42%, king-safety collapse 18%) built as `internal/mirror/midgame.go`,
phase-gated to `Pos.Phase >= 7` — exactly ABOVE the endgame set's and mop-up's
`Phase <= 6`, so the two are disjoint by construction. Mirror only; no asm
change. Commit 480777f (feature+tests).

**THE HEADLINE IS A NEGATIVE.** The king-safety group loses even when it is
charged NOTHING for its cycles, and the positional group's gain does not
survive decomposition. Neither is port-worthy today, and per byte both are
an order of magnitude behind the check-extension port already queued.

**The terms** (white POV; one shared 32-slot piece-list pass; all relational,
because any term that is a pure function of one piece's square is already in
the tapered Texel-tuned PSQT):

| group | term | formula | weight |
|---|---|---|---|
| KS | KSAtk | superlinear table over ATTACK UNITS = sum over enemy N/B/R/Q of {N:2,B:2,R:3,Q:5}, full inside Chebyshev 2 of the king, half at 3 | `{0,2,8,18,32,50,72,98,128,162,200,242,288,338,392,450}` |
| KS | KSDefend | subtract half the same unit sum for OUR pieces near OUR king, floored at 0 | on |
| KS | KSPawn | +1 unit per enemy pawn within Chebyshev 2 of the king | 1 |
| KS | KSOpen / KSFullOpen | per king-zone file (kf-1..kf+1) with no own pawn ahead of the king / additionally with no pawn of either colour | 12 / 8 |
| KS | KSGap | per rank of distance to the nearest own pawn ahead on each zone file, capped 3 | 5 |
| KS | KSExposed | `max(0, 3 - CMD[ourK])` while the enemy has a queen (halved with rooks but no queen) | 14 |
| POS | OutpostN / OutpostB | N/B on the relative 4th-6th rank, protected by an own pawn, with NO enemy pawn on either adjacent file ahead of it (pure bitmask algebra) | 16 / 8 |
| POS | Backward | pawn whose neighbours are ALL more advanced and whose advance square is controlled by an enemy pawn | 8 |
| POS | Phalanx | pawn with an own pawn beside it or defending it | 5 |
| POS | BadBishop | per bishop, `w * (own pawns on its complex - own pawns on the other)` (signed, centred) | 3 |
| POS | BlockedCtr | own d/e pawn on its 2nd/3rd rank with an own NON-PAWN piece directly in front | 12 |

State used: the piece list, PHASE, the two kings, per-file pawn RANK bitmasks
(8 B per colour — NOT maintained today, but pawnterm's existing per-file scan
can fill them at ~zero cost, and pawnterm is only recomputed when the pawn
structure changes), and the mop-up's existing 64-byte CMD table.

**SCREEN A — the diagnosis-FEN probe: THE KING-SAFETY TERM DOES NOT SEE THE
DANGER.** Reproducing the diagnosis's own instrument (gap = shallow score at
the 143M operating budget minus a 5x-deeper oracle at 715M, terms OFF for the
oracle; positive = we over-rate the position):

| FEN | phase | static eval OFF | ks delta | pos delta | gap OFF | gap ks | gap both | oracle |
|---|---|---|---|---|---|---|---|---|
| `8/3r1k2/2R3pp/p3bp2/N3p2P/3nP1P1/4KPB1/8 w` (blindGap 1860) | 8 | -133 | -42 | -20 | +12 | -18 | -20 | -105 |
| `8/8/6p1/P2p1k2/5P2/3KQ3/1q6/8 w` (644) | 8 | +45 | **+32** | 0 | -700 | -700 | -700 | +709 |
| `5k2/3n3Q/3r2p1/4p1K1/4P3/7P/3p2P1/8 w` (432) | 7 | +79 | -6 | +5 | +432 | +432 | +432 | -432 |
| `5r1k/pR5p/3p3p/4pP2/3q3b/2rB4/4QPP1/3R2K1 w` | 18 | -7 | 0 | +11 | -6 | -6 | +5 | -6 |
| `4rrk1/ppp2ppp/8/3PbN2/6P1/1Pq1P2P/4Q3/3R1RK1 w` | 18 | -66 | -27 | -25 | +4 | -23 | -35 | -81 |

Read: on ks-644 the term moves the eval the WRONG WAY (+32 when white is the
side in trouble) because attacker counting is symmetric — both kings are
active at phase 8 and the term cannot tell whose attack lands first. On ks-432
it moves -6, nowhere near the 432 gap, and the shallow SEARCH score does not
move at all. Only ks-1860 and pos-b move the intended way. Note also that all
three king-safety FENs are at phase 7-8, i.e. they are the tail of the bucket
(blindGap 432..1860 vs the bucket median 301) and largely tactical.

**SCREEN A at corpus scale (TestMidBlindGap) — THERE IS NO HORIZON BLINDNESS
LEFT TO HARVEST at the operating point.** 400 quiet self-play FENs, 329 of
them middlegame:

| group | median \|gap\| | mean \|gap\| | mean signed gap | positions closer/further vs OFF |
|---|---|---|---|---|
| OFF | **8.0** | 14.7 | -3.3 | — |
| ks | 12.0 | 19.6 | -1.1 | 96 / 203 |
| pos | 12.0 | 18.0 | -2.4 | 105 / 195 |
| both | 17.0 | 22.9 | -0.4 | 87 / 229 |

At 143M cycles/move our shallow score already agrees with a 5x-deeper search
to a MEDIAN OF 8cp on quiet middlegame positions, and only **1 of 329** has
\|gap\| >= 200. The premise "a static term steers the shallow search away from
what only the deep search sees" has almost no room to operate here: the
positions with a large blindGap are not quiet positions, they are tactical
ones. Every group makes the shallow score DISAGREE slightly more with the OFF
oracle (which is expected — the terms shift the scale — but it is also the
only thing this instrument can measure).

**SCREEN B — the real gate: cycle-budget self-play** (`-cbudget 143000000`,
asm-matched 0x1f both sides, shipped weights/QS, terms ON vs OFF, seed 6502
unless noted). "Tax" is the per-gated-eval-call 6502 cost charged via
`Costs.MidTerm`; the gate fires on **100% of eval calls** in a middlegame
search (TestMidCosted), so a tax of 438 is 7.94% of ALL cycles, 657 is 11.5%
and 876 is 14.7% (for scale, the REJECTED FT_ROOKX set cost 3.97%):

| variant | tax (cyc/call) | games | W/D/L | Elo +/- err |
|---|---|---|---|---|
| **king safety (all KS terms)** | 657 | 2000 | 644/567/789 | **-25 +/- 13** |
| **king safety, UNTAXED** | 0 | 2000 | 652/586/762 | **-19 +/- 13** |
| KS shield/open/exposed only, UNTAXED | 0 | 2000 | 643/627/730 | **-15 +/- 13** |
| KS attacker table + pawn storm only, UNTAXED | 0 | 2000 | 706/599/695 | +2 +/- 13 |
| **positional (all POS terms)** | 438 | 2000 | 729/606/665 | **+11 +/- 13** |
| **positional, seed 4711** | 438 | 4000 | 1423/1313/1264 | **+14 +/- 9** |
| **positional, POOLED** | 438 | **6000** | 2152/1919/1929 | **+12.9 +/- 7.3** |
| positional | 219 | 2000 | 694/636/670 | +4 +/- 13 |
| positional, UNTAXED | 0 | 2000 | 733/628/639 | +16 +/- 13 |
| pawn-only half (Backward+Phalanx), UNTAXED | 0 | 2000 | 666/639/695 | -5 +/- 13 |
| pawn-only half, UNTAXED, seed 4711 | 0 | 4000 | 1377/1230/1393 | -1 +/- 9 |
| pawn-only half, POOLED | 0 | 6000 | 2043/1869/2088 | **-2.6 +/- 7.3** |
| piece-only half (Outpost+BadBishop+BlockedCtr) | 438 | 2000 | 680/632/688 | -1 +/- 13 |
| piece-only half, seed 4711 | 438 | 4000 | 1407/1226/1367 | +3 +/- 9 |
| piece-only half, POOLED | 438 | 6000 | 2087/1858/2055 | **+1.9 +/- 7.3** |
| **both groups combined** | 876 | 2000 | 600/637/763 | **-28 +/- 13** |
| both groups combined, UNTAXED | 0 | 2000 | 709/602/689 | +3 +/- 13 |

**VERDICTS.**

1. **King safety: DEAD, and not for cost reasons.** -19 +/- 13 with the cycle
   tax set to ZERO. The harmful part is the shield/open-file/exposure
   sub-group (-15 +/- 13 untaxed); the attacker-count table is a null
   (+2 +/- 13). This is the first feature in this project to measure
   NEGATIVE with its cost removed — i.e. the KNOWLEDGE is wrong, not the
   price. Most likely mechanism: our king-shield/exposure penalties fight
   the tapered EG king PSQT (which starts rewarding centralization from
   phase ~8) and, in self-play, punish exactly the active-king play that the
   endgame terms were shipped to encourage. No optimization pass can save a
   term that loses for free, so the "optimize before rejecting" rule does
   not apply.
2. **Positional set: a real but unconfirmed +13 +/- 7.3 at its honest tax**
   (6000 games, two seeds, both positive: +11 and +14). BUT its two halves,
   measured to +/-7.3 each over 6000 games, are **-2.6** (pawn-only) and
   **+1.9** (piece-only) — they do not reproduce the union, and 2.6 + 1.9
   does not add up to 13. Either there is a real interaction, or the union
   number is ~1.5 sigma high. Given the FT_ROOKX precedent (a mirror screen
   of +26..+30 that became -19 in the asm SPRT), a union result its own
   decomposition cannot explain is NOT sufficient evidence to spend image
   bytes.
3. **Combined: -28 +/- 13** at the honest tax — the king-safety group's loss
   plus the cost of both.

**PORT RECOMMENDATION, ranked by Elo per byte (the image has 278 B free):**

| candidate | Elo (honest tax) | est. asm cost | est. bytes (code+tables+scratch) | Elo/byte |
|---|---|---|---|---|
| check extensions (already screened, docs above) | +12.6 +/- 9.0 | 0 (taxed via nodes) | ~50-80 | **~0.2** |
| positional set (5 terms) | +12.9 +/- 7.3 | ~430 cyc/call = 7.9% of all cycles | ~300-400 code + 16 scratch | ~0.03 |
| piece-only half | +1.9 +/- 7.3 | ~350 cyc/call | ~250 | ~0.008 |
| pawn-only half (would be FREE — fits pawnterm's cached scan) | -2.6 +/- 7.3 | ~0 marginal | ~120 | negative |
| king safety | -19 (untaxed) / -25 (taxed) | ~650 cyc/call | ~400-500 | negative |

**DO NOT PORT anything from this batch.** Port the check extension first: it
is ~7x better per byte and its Elo is confirmed over 4000 games. The
positional set should be re-run on a third independent seed (and its
decomposition re-examined) before it is allowed to compete for the 278 free
bytes; if it is ever ported, the two pawn terms belong INSIDE pawnterm
(cached, zero marginal cost) and only the three piece terms need a per-eval
pass.

**Asm cost model** (arithmetic Chebyshev, no new distance table; full sketch
at the bottom of midgame.go): KS ~650 cyc/call (219 piece-list pass + ~230
for 10 king distances + ~60 pawns + ~90 zone files + ~50 tables), POS ~430
(219 pass + 120 per-file bit algebra + 60 outposts + 30 bad bishop), both
~830 shared. Gate is 7 cycles (`LDA PHASE : CMP #7 : BCC done`) and is the
only cost in an endgame leaf. Tables: KSAtk 16 B, per-file pawn rank masks
16 B of scratch.

**Gate/no-leak proofs.** TestMidNoEndgameLeak: 40,882 random-play positions,
1,273 of them below the gate, **0 leaks** — no position with Phase <= 6 can
have its eval changed, so the shipped endgame-technique terms and mop-up are
provably untouched (positive controls included: silent in the two endgame
FENs, active in two asymmetric middlegames; a SYMMETRIC opening cancels
exactly, which is the antisymmetry property, not a gate). TestMidOffIdentical:
OFF path byte-identical (same best move, score and node count) at masks
0x00/0x07/0x1f with the mop-up and endgame set on. TestMidSanity pins every
term's mechanics plus exact colour antisymmetry. TestMidCosted proves the tax
is charged on exactly the gated calls and never in an endgame search.

**What this says about the strategy.** Two independent attempts at
middlegame eval knowledge have now failed here: FT_ROOKX (rook files /
doubled rooks / blockade, -19 +/- 33 in the asm SPRT) and this batch. Add
bishop pair (+3 +/- 9), the Texel retune (+7 +/- 9) and king-bucketed PSQT
(rejected), and the pattern is consistent: **hand-added middlegame eval
shape does not convert on this engine at this operating point.** The one new
piece of evidence is WHY the king-safety branch of the diagnosis was
mis-prioritized: at 143M cycles/move the shallow score already tracks a
5x-deeper search to 8cp median on quiet positions, so there is no horizon
gap for a static term to close — the diagnosis's blindGap >= 200 bucket
lives in tactical positions, which is a SEARCH problem (check extensions,
QS) and not an eval one.

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
## 2026-07-25 — ENDGAME TECHNIQUE (mirror): +2.08 ± 0.71 match pts/position on the conversion suite (CI excludes 0); self-play +10 ± 9

The top lever from the same-day loss diagnosis, built and screened.
`internal/mirror/endgame.go`, phase-gated, mirror-only (asm port is a
follow-on). Commits 56da578 (feature) + bfef001/e9a3b0e (review fixes).

**THIS ENTRY WAS REWRITTEN AFTER AN ADVERSARIAL REVIEW.** The first version
claimed "+52 pts, +78 correct results, 47.6% → 63.2% conversion". The review
verified every corpus FEN with Stockfish 18 and found **8 of 25 labels
wrong**, and pointed out that the error bar had been computed over 500 GAMES
when the independent unit is the 25 POSITIONS. Both are fixed below. The
feature still passes — but the headline is ~half what was first claimed, and
the framing "conversion rate" was not supportable. Corrections listed at the
end; the original numbers should not be quoted.

**Why this and not more search.** The diagnosis measured `blindGap`
(shallow eval minus a 5x-deeper oracle) at a median of **57cp** on the 28
thrown-away endgames — our eval AGREES with a much deeper search all the
way down, so depth cannot fix them. The gap is KNOWLEDGE. mopup.go
(shipped, FT2_MOPUP) only knows "drive a lone king to a corner"; nothing
in the eval knew anything about endgames that still have pawns.

**The terms** (white POV, added in eval() like PStruct/mopup, ALL gated on
`Pos.Phase <= 6` — the mop-up's endgame definition, N=1/B=1/R=2/Q=4 per
side, so 6 = R+B vs R+B or less):

| term | formula | weight |
|---|---|---|
| KingCent | `CMD[theirK] - CMD[ourK]` (the mop-up's 64-byte centre-manhattan table, 0 centre .. 6 corner) | 8 |
| KingPawn | `chebDistToNearestPawn(theirK) - (ourK)`, pawns of EITHER color | 6 |
| Pass | endgame-only top-up by advancement, same index as pawnterm's PASSEDBONUS | `{0,0,10,20,40,60,100,0}` |
| PassKingOur | per own passer: `4 - cheb(ourK, frontSq)` | 6 |
| PassKingThem | per own passer: `cheb(theirK, frontSq) - 4` | 4 |
| KingAhead | per own passer: +flat if our king is strictly ahead of it within 1 file; -flat if it sits directly behind on the same file | 15 |

The **Pass** table exists because pawnterm's Texel-tuned PASSEDBONUS is
`{0,15,0,21,50,52,20,0}` — NOT monotone, and it rates a pawn on the 7th
(20) BELOW one on the 5th (50). That is a self-play tuning artifact (both
sides push symmetrically) and it is precisely the shape that fails to
convert.

**SCREEN A — CONVERSION SUITE.** 25 endgames: the 3 diagnosis FENs + 11
positions mined from the 300-game match log (`TestHarvestEndgames` replays
each non-win game and takes the 8fish-to-move, phase-gated position where
its own depth-6 score peaked) + 11 textbook. **Every FEN's ground truth is
an external Stockfish 18 verdict**, recorded in the corpus's `sf` field —
not our own eval, and not intuition. 20 dither seeds each, **30M cyc/move**
(the Sargon-match operating point), hero's terms ON vs OFF against a FIXED
reference defender that always has them OFF (a both-sides screen cancels
symmetric knowledge).

**Headline, paired over the 25 positions:**

| metric | OFF | ON |
|---|---|---|
| match points (win 1, draw 0.5) | 301.0 | **353.0** (+52.0) |
| **per-position delta** | — | **+2.08 ± 0.71, CI [+0.69,+3.47] — excludes 0** |
| positions better / worse / same | — | **12 / 1 / 12** |
| correct result vs Stockfish (excl. 4 objectively-lost positions) | 324/420 (77.1%) | **371/420 (88.3%)** (+47) |
| W / D / L | 158 / 286 / 56 | 247 / 212 / 41 |

**The decomposition that matters, by the position's OBJECTIVE class:**

| class | n | pts delta |
|---|---|---|
| objectively WON (real conversion) | 11 | **+24.5** |
| objectively DRAWN (overshoot: beat the weaker twin) | 10 | +11.5 |
| objectively LOST (ditto) | 4 | +16.0 |

So **only +24.5 of the +52.0 is conversion of winning endings**; the other
+27.5 is the ON engine out-playing a knowledge-removed twin in positions
that are objectively drawn or lost. "Better-than-objective results" rise
38 → 80. That is still a genuine A/B strength gain (and it is what the
self-play screen independently confirms), but it is NOT a conversion rate,
and the earlier framing overstated the feature by ~2x.

Per-position (OFF → ON, W/D/L; `sf` = Stockfish, hero POV):

| position | sf verdict | want | OFF | ON | moves-to-win |
|---|---|---|---|---|---|
| kpk-file-e | decisive | win | 4/16/0 | **19/1/0** | 16 → 15 |
| two-connected | decisive | win | 4/16/0 | **20/0/0** | 21 → 18 |
| k2p-vs-kp | decisive | win | 0/20/0 | **6/14/0** | – → 19 |
| peak-g229 | decisive | win | 0/20/0 | **5/15/0** | – → 17 |
| peak-g194 | decisive | win | 16/4/0 | **20/0/0** | **18 → 8** |
| square-win | decisive | win | 18/2/0 | 20/0/0 | 12 → 13 |
| lucena | decisive | win | 5/15/0 | 6/14/0 | 26 → 26 |
| peak-g163 / g34 / g196 / g132 | decisive | win | 20/0/0 | 20/0/0 | 1/12/14/22 → 1/13/16/18 |
| kpk-front | **cp 0 — DRAWN** | draw | 0/20/0 | 12/8/0 | overshoot, not conversion |
| peak-g91 | **cp 0 — DRAWN** | draw | 0/20/0 | 9/11/0 | overshoot |
| rook-behind | cp 0 | draw | 3/17/0 | 11/9/0 | overshoot |
| peak-g48 | cp −25 | draw | 5/12/3 | **1/14/5** | **REGRESSION** (−3.0 pts) |
| peak-g234, peak-g2, outside-passer | cp 0 | draw | 0/20/0 | 0/20/0 | correctly held |
| kpk-rookdraw, kpk-blocked, square-draw | cp 0 | draw | 0/20/0 | 0/20/0 | correctly held |
| diag-g137 | cp −444 | lost | 0/1/19 | 0/6/14 | still lost |
| diag-g246 | cp −501 | lost | 0/6/14 | 15/3/2 | overshoot (+13.5) |
| diag-g61 | cp −362 | lost | 0/0/20 | 0/0/20 | still lost |
| peak-g8 | cp −349 | lost | 20/0/0 | 20/0/0 | – |

Honest negatives: **peak-g48 regresses** (−3.0 pts, 3 losses → 5), and
`diag-g61` / `diag-g137` are unchanged-or-still-lost. Nine positions are
completely untouched.

**Ablation** (each arm's hero vs the OFF defender, paired vs the OFF arm;
per-position delta ± SE):

| arm | pts | per-position | sig |
|---|---|---|---|
| **shipped set** | 353.0 (+52.0) | **+2.08 ± 0.71** | SIG+ |
| all 8 designed | 333.5 (+32.5) | +1.30 ± 0.70 | ns |
| shipped − kcent | 335.0 (+34.0) | +1.36 ± 0.71 | ns |
| shipped − kpawn | 336.5 (+35.5) | +1.42 ± 0.70 | SIG+ |
| shipped − BOTH king terms | 316.5 (+15.5) | +0.62 ± 0.49 | ns |
| shipped − pass | 317.0 (+16.0) | +0.64 ± 0.41 | ns |
| shipped − pking | 335.5 (+34.5) | +1.38 ± 0.44 | SIG+ |
| shipped − kahead | 312.5 (+11.5) | +0.46 ± 0.42 | ns |
| pass doubled | 309.5 (+8.5) | +0.34 ± 0.84 | ns |
| pking doubled | 317.0 (+16.0) | +0.64 ± 0.50 | ns |
| gate phase 8 | 332.5 (+31.5) | +1.26 ± 0.70 | ns |

Read with the CIs: the SET is significantly better than OFF, but the
individual leave-one-out gaps are mostly INSIDE the noise of a 25-position
screen. What the data does support: removing the king-activity pair, `pass`,
or `kahead` roughly halves the gain (each drop lands back in "ns"), so those
four terms carry it; and doubling either `pass` or `pking` is worse, so the
weights are not obviously under-set. What it does NOT support is a precise
per-term ranking.

**The two ship/kill decisions, paired against the shipped set directly**
(`TestEndgameConversionDecide` — the decision-relevant comparison, which the
per-arm table above cannot give):

| adding to the shipped set | per-position delta | sig |
|---|---|---|
| Unstoppable @250 (rule of the square) | **−0.66 ± 0.34, CI [−1.32,−0.00]** | **SIG−** |
| Unstoppable @80 | −0.46 ± 0.31 | ns, same sign |
| RookBehind @20 (Tarrasch) | **−0.10 ± 0.15, CI [−0.40,+0.20]** | ns — a true null |
| both (= the full designed set) | **−0.78 ± 0.36, CI [−1.48,−0.08]** | **SIG−** |

- **Unstoppable is DROPPED as harmful.** Mechanism: the bonus flips with the
  side to move (the defender's free tempo), so it makes the leaf eval
  tempo-dependent beyond Tempo — poison for alpha-beta — and the search
  already sees a 4-move pawn race directly.
- **RookBehind is DROPPED for COST, not strength** — it is a measured null,
  and it is the only term needing a board walk (a per-passer file scan in
  the asm). Rook-endgame technique therefore stays unported; "cut off the
  enemy king" was never attempted (it needs an attack scan, which the cost
  history says dies).

**Both-sides control** (the mop-up precedent): both arms ON vs both OFF is
**+0.80 ± 0.45 per position, CI [−0.08,+1.68] — ns**, versus +2.08 ± 0.71
isolated. Less than half the effect and no longer significant, because the
knowledge is symmetric. This is the measured mechanism (not an assertion)
behind self-play under-reporting this feature class.

**SCREEN B — SELF-PLAY CYCLE BUDGET.** asm-matched config on BOTH sides
(mask 0x1f, recap2 QS, Default weights, corrected-guard RFP 120/500,
**mop-up ON** — the real shipped engine), A = terms ON taxed at 438
cyc/gated eval, B = terms OFF, `-cbudget 143000000`, 4 seed batches
(`runs/mirror-endgame.sh 500 <out> default 3 143000000`):

| seed | +W =D -L | Elo ± err |
|---|---|---|
| 6502 | +377 =288 -335 | +15 ± 18 |
| 1337 | +368 =302 -330 | +13 ± 18 |
| 4242 | +345 =315 -340 | +2 ± 18 |
| 9001 | +383 =266 -351 | +11 ± 18 |
| **pooled 4000** | **+1473 =1171 -1356 (51.46%)** | **+10 ± 9, CI [+1,+19]** |

**+10 ± 9 is weakly positive, not decisive** — the lower bound only just
clears zero. Combined with the isolated conversion result (SIG+) and the
both-sides control (ns), the picture is consistent: a real but modest gain,
concentrated in endgames, largely symmetric in self-play. Better than the
mop-up's flat −7 ± 32, and it is not negative anywhere.

**MIDDLEGAME UNTOUCHED (proof).** `TestEndgameNoMiddlegameLeak`: over
**35,567 positions** from 300 random-play games, EG-ON eval == EG-OFF eval
at every position with phase > 6 — **0 leaks**; 278 positions differ, all
inside the gate (plus positive controls: g137/g61 must differ, the opening
and a 4-knights middlegame must not). `TestEndgameOffIdentical`: with
`EndgameParams{}` the depth-5 tree (best move, score, node count) is
IDENTICAL to an engine with no EG config, at masks 0x00/0x07/0x1f with the
mop-up both on and off. Every asm gate green unchanged: TestMicroAB,
TestMicroABAdopted(+Probes), TestMicroABImproving, TestSearchMirrorParity
(+Improving), TestMopupEvalParity, TestPStructParity/MirrorParity,
TestAspirationMirrorParity, TestAdaptiveParity, TestBookProbeParity. An
independent exhaustive color-flip check (`egEval(p) == -egEval(flip(p))`
with the gate wide open) found **0 asymmetries over ~55k positions**, and
the `pass`-only term reproduced `extractPawnFeatures`'s passer counts
exactly on all of them — the black mirror and the passer definition are
verified, not assumed.

**Cost.** Charged honestly at `EvalTermsCost(2) = 438 cyc` per gated eval
call in every screen above; measured share of a real endgame search:
**3.9-5.1%** of estimated cycles (0% in the middlegame — the gate is one
compare). The asm design is ~120 cyc + ~65/passer, so the screened numbers
are a PESSIMISTIC bound on the ported feature.

**PORT RECOMMENDATION: PORT IT** (FT2_EGTECH), with the mop-up's rationale:
a significant isolated endgame gain (CI excludes 0) plus a positive-leaning
self-play number, in the bucket the diagnosis says costs the most points.
But port it with the corrected expectation — the honest conversion component
is +24.5 match points over 11 won endings, not +52, and the self-play gain
is ~+10 Elo, not the +50 class of the adaptive-time win. Design sketch is in
the asm-port comment at the bottom of endgame.go. The one decision that
makes it cheap: have pawnterm stash its per-file most-advanced ranks
(PWMAX/PBMIN, 16 B) in the loop it ALREADY runs, so the endgame term needs
no piece-list pass of its own. ~250-350 B of code + 16 B of tables; the
image had 278 B headroom at the last audit, so it needs a feature-audit
slot.

**CORRECTIONS FORCED BY THE REVIEW** (recorded so the same errors are not
repeated):
1. **8 of 25 corpus labels were wrong**, all in the optimistic direction.
   Worst: `kpk-front` (`8/8/8/3k4/8/3K4/3P4/8 w`) was labelled "king in
   front of the pawn: won" — with WHITE to move it is the textbook MUTUAL
   ZUGZWANG DRAW (1.Kc3 Kc5); the note described the position with BLACK to
   move. Also mislabelled: `outside-passer`, `peak-g234`, `peak-g91`,
   `peak-g2`, `peak-g48` (all cp 0 or worse, labelled "win"), and `peak-g8`
   (cp −349) / `diag-g137` (cp −444) / `diag-g246` (cp −501) / `diag-g61`
   (cp −362), which are objectively LOST. **The three diagnosis FENs being
   lost matters beyond this screen: the loss diagnosis' "reached an
   even-or-better endgame and lost it" describes the MATERIAL COUNT, not the
   position — at those plies the game was already gone, so the point of no
   return was earlier than the diagnosis located it.** Ground truth now
   comes from Stockfish 18 and is stored in the corpus.
   **`mopup_conversion_test.go` carries the same wrong label**: its
   `KPK-win` is this exact drawn FEN, so the mop-up entry's "partially
   recovers KPK (0→2/4)" was recovering a DRAW into a win against a
   handicapped twin. Not fixed here (it would change a shipped test), but
   flagged in both files.
2. **The error bar was computed over games, not positions.** 20 runs of one
   position share everything but the dither seed, so the independent unit is
   the position (n=25). All numbers above are paired per-position; the
   original "+52 pts / +78 correct" carried no uncertainty at all, and the
   drop-one verdicts quoted from it ("Unstoppable −17.5", "RookBehind +2.5 =
   neutral", "phase 8 identical") were point estimates inside the noise.
   They are re-derived above as direct paired comparisons.
3. **Selection bias acknowledged**: 14 of 25 FENs are conditioned on the OFF
   engine failing, and 11 were mined by taking the ply where its own eval
   PEAKED — which is exactly where that eval is most over-optimistic. That
   is why 6 of the 11 "unconverted wins" turned out not to be wins. The
   paired A/B is still valid, but the OFF baseline is biased low on those
   positions, so the corpus-wide delta is an upper bound on the transfer to
   real games.
4. `TestEndgameSanity`'s RookBehind case did not isolate RookBehind (it
   passed on Unstoppable's +250 and would have passed with RookBehind's sign
   inverted); it now screens both terms alone, including the enemy-rook sign.
5. Harness fixes: `egPlayOut` no longer calls `t.Fatalf` from worker
   goroutines; the ablations nil-check missing corpus entries;
   `TestEndgameHarnessPOV` pins the hero-POV mapping on forced mates for
   both colors (11 of 25 positions are black-to-move, so a sign error there
   would have inverted everything); `egScan` no longer allocates per gated
   eval.

## 2026-07-25 — check extensions: +12.6 ± 9.0 (mirror, cycle-budgeted) — PORT-worthy, batched with endgame work

Single-ply extension when a move gives check (gives-check is already
computed in make, so the signal is free). Cycle-budgeted asm-matched
screens:

| variant | Elo ± err | games |
|---|---|---|
| **MaxExt=1, all checks** | **+12.6 ± 9.0** | 4000 |
| MaxExt=2 | +7.1 ± 12.6 | 2000 |
| MaxExt=2, captures-only | −0.3 ± 17.8 | 1000 |

Findings: one extension is enough (chains >2 are rare; cap3≈cap2), and
gating to checking CAPTURES kills the gain — the **quiet forcing checks**
carry the value (matters for the port: no cheap captures-only shortcut).
Fixed-depth tree growth is moderate (+12–27% at cap1). Never reduced and
extended simultaneously (LMR already excludes checking moves). Off is
byte-identical.

Note vs the loss diagnosis: it predicted this would underperform (only
4% of losses are tactical) — correct in rank order (endgame technique is
the bigger lever) but it is still a real positive, so the diagnosis
informed priority without vetoing.

PORT DEFERRED-AND-BATCHED with the endgame-technique port: MAIN has only
~278 bytes of headroom (the previous two features overflowed it by 169 B
and forced the FT_ROOKX audit), so two features = one space negotiation,
one union rebuild, one SPRT campaign.

## 2026-07-25 — LOSS DIAGNOSIS: we lose to TECHNIQUE, not tactics; contempt is dead, endgame conversion is the lever

Clustered all 103 losses (96 distinct games) from the clean 300-game
symmetric match by walking each game with the mirror and locating the
point of no return via a 5×-deeper (150M) oracle search.

| theme | count | % |
|---|---|---|
| **Positional squeeze** | 40 | **42%** |
| **Endgame conversion/defense** | 28 | **29%** |
| King-safety / horizon-missed attack | 17 | 18% |
| Opening disadvantage | 7 | 7% |
| Tactical material blunder | 4 | **4%** |

**71% of losses occur at roughly EVEN material; 79% of losing moves are
8fish's OWN choice.** We don't hang pieces — we get ground down.

THE KEY METRIC — `blindGap` (how far our shallow eval over-rates a
position vs the deep oracle): positional med **59**, endgame med **57**
(0/68 ≥200). Our eval AGREES with a 5×-deeper search all the way down,
so **more depth would not have saved these games** — it's an EVALUATION
(judgment) gap, not a search gap. By contrast king-safety losses have
med blindGap **301** (15/17 ≥200) — those genuinely are horizon-blind,
so a static king-safety penalty would steer the shallow search away
from them without needing depth.

**CONTEMPT IS DEAD — my hypothesis was wrong.** I assumed the 25% draws
were 8fish settling for half-points in equal positions. Actually **42/51
distinct draws (82%) are games where 8fish reached a clearly BETTER or
WINNING position (peaks +300..+2000) and failed to convert**; only 5
were quiet-equal. So contempt has ~5 games of upside, not 76 — and the
draws share the SAME root cause as the losses: missing technique.

PRIORITY (evidence-ordered, supersedes the earlier contempt-first plan):
1. **Endgame technique** — 29% of losses AND 82% of draws: the same
   weakness bleeding through both channels, so the highest total-points
   lever. Our shipped mop-up (KRK/KQK shuffling) is the floor; what's
   missing is real technique (K+P, rook endings, converting +1 pawn).
2. **Positional eval terms** — biggest single bucket (42%), harder;
   needs cheap incrementally-maintained terms (per the cost history).
3. **King safety** — 18%, horizon-blind, so a static term is effective
   and cheaper than feared.
4. ~~Contempt~~ — dropped (~5 games of upside).
5. Book — deprioritized for STRENGTH (7%, cancels on a symmetric pool);
   standard-start work still matters for authenticity.

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
