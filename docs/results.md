# Match & measurement log

Newest first. Engine budgets are emulated time (1.0205 MHz); opponent
controls are wall time. See docs/plan.md for the measurement protocol.

## 2026-08-09 — Boot SPLASH title card ships (the owner's hand-drawn 8fish logo), + white arrow-cursor box

Two UI-polish items, engine and driver both **byte-identical** (`engine.bin`
md5 `c7998397…` unchanged; `rwtsblob.bin` sha `51656c…`, 724 B unchanged — the
ProRWTS2 driver code was NOT touched, so its pending real-hardware validation
stands).

- **Boot splash**: the owner drew a 16 KB double-hi-res 8fish logo
  (`FISH8LCDSSS` on the DazzleDraw disk, captured to
  `assets/fish8-splash-dazzledraw-save.bin` via `make pull-splash`). It ships
  **PackBits-compressed to 5,598 B** (34% of raw — the image is 78% `$80`
  DHGR-black) as a fifth disk file `SPLASH` in the book region's directory
  (directory data only; the driver reads whatever it finds at block 208, so no
  driver/`genrwts` change). At boot it is read to main `$2000` via the existing
  book read path, copied to aux `$4000`, and a ~40-byte 6502 PackBits decoder
  (`spldecode`) expands it straight into DHGR page 1 — aux `$2000` then main
  `$2000`, one continuous source stream, switching banks at exactly 8192 output
  bytes per half. Shown before the board, auto-advancing on any key or a
  ~2.6 s timeout. A `'8','F'` magic guards a gross bad read; any failure
  (no disk / no file / bad status / magic mismatch) **skips silently to the
  board** — the splash can never block boot.
  - **Where the code lives**: UICODE (`$E000-$F6FF`) was full, so the splash
    code + decoder ride the payload and are delivered to the transient
    ply-history region at `$F800` (bank-independent LC; three pages
    `UIHFROM/UIHTO/UIHFLAG`, all reclaimed by `m8new` right after the splash
    runs). Zero permanent memory; runs in the resting bank 1, no bank
    switching, the driver at `$DC00` right there.
  - **Cost**: boot-to-board **~7.1 s → ~9.5 s** (the ~11-block splash read +
    decode/display), first input ~18.4 s. Book region 138 → 162 sectors
    (359/560 disk sectors used).
  - **Deferred, on purpose**: the splash does NOT cover the ~9 s book-load
    wait (that still shows the text "LOADING…" screen), because the book
    stages through main `$2000` — the DHGR main half — and would scribble any
    graphic there. Covering the load needs the book to read straight to aux
    (ProRWTS2 `allow_aux`), i.e. **rebuilding the driver that is about to be
    validated on real hardware** — so it waits until that validation. When it
    lands it both holds the splash through the load AND drops the copy-to-aux
    staging (faster load).
  - **Gates**: `internal/splash` PackBits round-trip + size; `internal/delivery`
    5-file region layout; `internal/ui` `TestDiskSplashShowsThenAdvances` boots
    the shipping `.dsk` through the real Disk II ROM and byte-compares decoded
    DHGR page 1 against the raw asset, then dismisses the splash. Three
    mutations verified (decoder run-count `257-c`, directory FILE_COUNT, decoder
    branch sense). An independent adversarial review cleared all six risk areas
    of the boot-path change (no bugs; one comment nit fixed).
- **White cursor box**: the arrow-key selection box (`dhcursor`) is now lit on
  BOTH square shades instead of contrast-keyed (dark-on-light) — the light
  squares are only a ~50% dither, so a white frame reads cleanly over them.
  Simpler (no `DHFLIP` dependency), and the Go model gate updated to match.

## 2026-08-08 — ★ THE BIG BOOK ships (ProRWTS2 feature 1): a resident 724 B read-only disk driver, and a 32 KB opening book living in the idle transposition table

`docs/prorwts2-design.md` feature 1 is implemented, the design held, and the
mechanism ships full-size: the disk now carries a **32,768-byte big-book
window** (capacity **3,639 entries** — the design's 3,640 less one entry for
the load-verify checksum trailer), loaded after the board paints into aux
`$4000-$BFFF`, which is dead TT freight until the first real search. Today it
carries the same 633 curated entries as the resident book (17% full):
**filling it is the follow-on content task**; nothing in the mechanism knows
the count.

The pieces, all measured:

- **Toolchain**: upstream `PRORWTS2.S` vendored pristine (BSD-3-Clause,
  commit `56f76d40`, sha256-pinned), assembled by `cmd/genrwts` with
  a2audit's ACME 0.97 into a fully-relocated blob. The 8fish read-only
  config came out **495 B of code** (the design measured 658 with
  `use_smartport=1`; dropping the unused SmartPort init path is 163 B);
  724 B shipped (code + prebuilt denibble table + the slot-poke site
  table). One structural patch: `dirbuf` → main `$1300` (MOVESTACK, dead
  between searches). ProRWTS2's own `init` needs a running ProDOS and is
  never assembled in; its products are reproduced at build time (denibble
  table, directory block number) or at boot (`m8rwtsinit` pokes slot<<4
  from the saved `$2B` into 10 operands, seeds `trackd1` from `$41`).
- **Resident cost**: LC bank 1 `$DC00-$DED3` (the resting bank — no call
  ever switches banks), 300 B of bank 1 still free. UICODE glue **374 B**
  in its own budget section + ~30 B of hooks — the design guessed ~300 —
  of the 417 free after arrow keys; **10 B of UICODE headroom** remain,
  after two densifications (fixed-window checksum instead of count·9
  bounds; the poke table appended to the blob so it reads from bank 1).
  Copier 255 of 256 B. zp: the driver's `$3C-$67` window (live BOARD rows
  included) is **swapped** around every call, 44 B held at `$F7BF`.
- **Disk**: a ProDOS-*shaped* region (directory key block 208 + 4 sapling
  index blocks + `BOOK0`-`BOOK3`), 138 sectors in tracks 26-34, disjoint
  from the SD stages (tracks 0-12) by gate. Ledger: stage 1 154/176,
  stage 2 41/176 (+3 sectors: the staged reader), region 138 — **334 of
  560 sectors**.
- **Latch semantics**: `BIGBOOKOK` opens only on a verified load (magic +
  16-bit checksum over the whole window, recomputed on-device in ~0.5 s);
  `mesearch` closes it one-way and re-points the engine's new `bookpg`
  byte (`$40` → `$08`, the ONE engine-image change, +8 B into LCCODE's
  108/128); `m8ponder` no-ops while open (the TT *is* the book); New Game
  reloads only if the game left book. Failure (bad read, wrong disk, bad
  sum) degrades to exactly the old shipped behaviour plus one message.
- **Timing, measured on the booted disk**: board paints at **7.40 s**
  (was 7.13: +3 stage-2 sectors and one pre-load paint), the load and
  verify take **+8.95 s** (the design's ~5 s estimate was optimistic:
  ProRWTS2 reads 2-sector blocks with address-field sync), first input
  ~**16.4 s** on game 1 and after a post-book New Game; 0 s when a game
  never left book.

Gates, all executed and mutation-checked: `TestMicroAB` vs `microABGolden`
**byte-identical, exact cycles unchanged** (grand total 3,225,425,169 — the
bookless tree never reaches the probe); big-book probe parity ASM==Go at
base `$40` (264 positions, 2,082 probes) beside the unchanged resident-base
parity; `TestDiskBigBook` boots `$C600` and proves the whole chain — zp
equality across the load, aux == `bigbook.bin` == the `.dsk`'s own blocks,
the opening served from `$4000` with the resident entries neutered, the
latch closing on the first search, New Game re-reading the searched-over
window; corrupt-disk refusal with the resident book still playing; ponder
latch respected (control: a ponder writes 2,179 window bytes; gated: 0).
Mutations (latch clear deleted, zp swap-back deleted, ponder gate inverted)
each killed by a named assertion. Per §3.7 of the design this feature is
gated on correctness, not Elo: book breadth measured +3 ± 10 in July and the
win here is product value plus zero resident-byte cost during the
middlegame.

## 2026-08-08 — ARROW-KEY MOVE ENTRY ships: cursor square-picking on the DHGR board, 658 B of UICODE, and the BLOAD tripwire fired on schedule

The on-device UI has a second input mode (docs/ui-design.md §18): an arrow
press pops a cursor box onto the board (starting on the last-moved piece's
destination), SPACE latches from / plays to, ESC cancels, and any letter
falls back to typed entry — which is unchanged keystroke-for-keystroke. The
cursor contains **zero chess rules**: a selection is synthesized into `UIBUF`
as the typed 4-character line and submitted through `uidispatch`, so
`uifind`/`uitrylegal`/`uiapply` validate it, promotions route through the
existing `uiaskpromo`, and an illegal pick gets the existing `m_illegal` with
the cursor kept for another try.

Highlights, on both screens: the cursor is a two-dot **contrast box**
(`dhcursor` — dark on light, lit on dark, keyed off the displayed shade);
the latched FROM square repaints in its **opposite shade** (`dhsq1` +
`DHFLIP` — the artwork already carries every piece on both shades, so the
flip is one EOR). A cursor step re-blits exactly two squares (~6 ms) via the
new `dhsetsq`/`dhsq1` factoring instead of the 190 ms full repaint; full
repaints still paint plain and overlay after (`uicursor`), so every existing
screen gate gates the same bytes.

Costs and the tripwire: **658 B of UICODE** (headroom 1,075 → 417 B), 40 B
of bank-2 string, 5 B of `$F700` state. `m8.bin` is now 6,508 B and BLOADs
past `$2000`, which fired `TestUIByteBudget`'s BLOAD-contract tripwire — the
deliberate call it exists to force: **the disk is the shipping path** (its
chain load lifts the payload before stage 2 lands the book at `$2000`;
ledger now stage 1 152/176, stage 2 38/176, 191/560 sectors, payload ends
`$27FF`), and **BLOAD-with-a-book-preloaded is retired** (the harness's
`stageBook` now lands the book after the copier runs, mirroring the disk's
ordering; the tripwire was re-aimed at the engine at `$4000`). Boot 7.26 s
(was 7.13 s: four more stage-1 sectors).

Gates: eight new `TestDiskCursor*` gates drive the SHIPPING DISK by
keystroke and assert the highlights as all 16,384 DHGR bytes against an
independent Go model of the overlays (the `TestDiskBoardParity` discipline);
typed entry, ESC-swap, promotion and illegal-move behaviour asserted
end-to-end. **Seven mutations** (box not drawn; edge clamp removed; from=to
synth; cursor never retired; ESC never cancels; latch flip dropped; promo
prompt bypassed) each killed by a named assertion. `make test` (short) green
end to end; `engine.bin` untouched (md5 `7ddc6a22…`).

## 2026-08-08 — ★★ THE DEVICE PONDER MEASURED: **+112 ± 30** self-play Elo (8fish-ponder vs 8fish-noponder, both real `m8.s` UIs); predictor hit rate **84%**; and pondering makes the shipped clock spend **1.33x** on its own moves

Every published ponder Elo (~+116 vs Sargon) came from `internal/ucibridge`'s
SIMULATED ponder, whose predictor is the free-TT walk ("source a"). The device
(`asm/m8.s m8ponder`) predicts with a depth-3 shallow search ("source b") and
interrupts through `pkclk`. Nobody had measured the device's actual mechanism.
This entry does, with a new harness: **`cmd/ponder-match`** runs two REAL
`m8.s` UIs under the harness emulator (`ui.Boot`, HARNESSKBD) and relays moves
between them as typed keystrokes — the way `sargon-symmatch` drives the real
Sargon disk. Side A is the shipped device ponder (`PONDERON=1`); side B is the
shipped no-ponder behaviour (`PONDERON=0`, the same poke every move-by-move
gate uses). Per move pair: A plays; A's main loop falls into `m8ponder` and
parks at the first `pkclk` poll; A's move is typed into B, whose reply costs
T_B measured cycles; A is then run for a **ponder window of exactly T_B**
with the keyboard trap disabled (a hardware $C000 read returns "no key" — the
modelling `ponder_test.go` established); B's move is typed into A, the first
keystroke interrupting the live ponder through the real `pkclk` path. Both
sides UI level 5 (4 s/move on the estimated clock, FT2_SOFTCLK + FT2_ADAPT —
the shipped stack). Referee = `refchess`; every injected move is read back
from the receiving machine's own history, with a whole-position FEN
cross-check whenever the machine is not parked mid-ponder. Deterministic per
`-seed` (replay verified); the per-game PRNG drives the entropy-collector
pokes that stand in for human key timing, so the shipped dither path is the
one exercised.

**The instrument was gated before the number was read.**

| gate | result |
|---|---|
| control (noponder vs noponder), seed 11, 200 games | +41.9 ± 39.4 |
| control, seed 12, 200 games | +24.4 ± 40.2 |
| control, seed 13, 200 games | +24.4 ± 37.2 |
| pooled control, 600 games | **+30.2 ± 22.4** — uncomfortably one-sided |
| **`-pairseed` symmetry proof**, 100 games | **exactly 0.0** (27-27-46); all 50 pairs bit-identical mirrors (same plies, same termination, result sign flipped) |

The pooled control LOOKED like an A-bias (z ≈ 2.6), and per the broken-gates
rule it was not waved off: the `-pairseed` mode reuses one game seed for both
games of a pair, which (both configs being identical in control mode) makes
game 2 game 1 with the roles exactly swapped — so ANY plumbing asymmetry
(scoring, colour assignment, injection, jitter-draw order, adjudication)
would surface as a nonzero total. It came out exactly zero, game-for-game.
The harness is symmetric end-to-end; the pooled +30 is a ~1% tail draw over
dither randomness (three same-sign 200-game controls at z ≈ 1-2 each). Both
readings of the measurement are quoted below anyway. Zero harness errors in
all 900 games of this campaign.

**The measurement — 400 games, paired openings, colours swapped:**

| quantity | value |
|---|---|
| record (ponder side) | 205 − 80 − 115 |
| **device-ponder Elo, raw** | **+112.3 ± 29.7** |
| conservative (pooled control subtracted) | **+82 ± 37** |
| predictor hit rate | **12173/14460 = 84.2%** of completed predictions |
| prediction coverage | 14460/19950 = 72.5% of windows (rest: predictor interrupted before finishing, or no legal P) |
| effective hit rate over ALL windows | 12173/19950 = 61.0% |
| reply depth after a HIT vs after a MISS | **3.69 vs 3.31** (Δ +0.39) — the warm TT demonstrably warms |
| overall reply depth, A vs B | 3.40 vs 2.92 |
| deep search live when the key landed | 16808/19950 = 84% of windows |
| ponder hit its ~8 s walk-away backstop | 3142/19950 = 15.7% of windows |

**Answer to the question this harness exists for: the DEVICE ponder works,
and using the opponent's time is worth on the order of +80–110 self-play Elo
at level 5.** The shallow-search predictor is NOT a weakness: 84% of its
completed predictions matched the opponent's actual move. (That is far above
the bridge's 45–47% hit rate vs Sargon, but the questions differ — here the
predictor guesses what an 8fish will play, and it is itself an 8fish. The
number does not transfer to human or Sargon opponents; the mechanism's health
does.) No 1:1 comparison to the +116 is possible — that figure is vs SARGON
with both sides pondering at B=30M on the harness clock — but nothing in this
measurement suggests the device mechanism falls short of the simulation: the
hit rate is high, the TT carryover is real, and the Elo is of the same order
as what pondering bought in every bridge measurement.

**Spend accounting (the asymmetry is the experiment; it is reported, not
equalised away):**

| component | ponder side (A) | no-ponder side (B) |
|---|---|---|
| own-move think, mean | 6.79M cyc | 5.14M cyc |
| ponder windows | 102.9G total, mean 5.16M (== B's think, by construction) | — |
| ponder overhead (interrupt tail + ponder entry) | 34.9G (≈1.75M/move) | — |
| whole-match total | 274.9G | 103.1G |
| total ratio | **2.67** | |
| **own-move ratio** | **1.33** | |

**The second finding, previously unmeasured: pondering makes the shipped
adaptive clock spend 1.33x on its OWN moves.** Decomposed per prediction
class (device soft-clock estimate sampled at the end of each reply search,
next to true cycles):

| A's reply after | n | true mean | est mean | est/true | depth |
|---|---|---|---|---|---|
| HIT | 12173 | 6.90M | 6.89M | 0.998 | 3.69 |
| MISS | 2287 | 4.85M | 4.72M | 0.973 | 3.31 |
| no prediction | 5490 | 7.49M | 7.76M | 1.036 | 2.84 |
| first move (never pondered) | 200 | 3.64M | 3.69M | 1.015 | 2.46 |
| B, all | 20077 | 5.03M | 4.78M | 0.950 | 2.92 |

est/true ≈ 1.0 everywhere: the soft-clock estimator is NOT being fooled by
the warm TT — the engine **believes** its 6.9M spends and chooses them. The
mechanism is the shipped FT2_ADAPT interaction: warm early iterations are
near-free, so iterative deepening reaches depths whose final iteration is
expensive and whose instability/panic signals engage the 3x/4x ceilings far
more often than on a cold TT. A's cold first moves (3.64M) behave exactly
like the control (own ratio there 1.006), pinning the whole own-move
overspend on ponder-induced TT warmth. On real hardware this is the disk
thinking LONGER on its own clock after pondering — v1 shipped "a hit spends
the full budget going deeper" (design §7.2), and this is what that costs at
level 5. If a future pass wants the own-move spend held at 1.0x, that is the
deferred ponder-time banking work (§7.3), now with a measured reason to
exist. Also measured for v2: the fixed ~8 s walk-away backstop truncated
15.7% of windows at level 5 — scaling it with the level (§11.A deferred)
would recover free time at the slow levels.

**What this does NOT change: the headline Sargon number.** Design §10 stands:
the representative product figure is the ponder-enabled symmetric
`sargon-symmatch` gauntlet, which is a separate (wall-clock-heavy) re-run and
is not superseded by a self-play number. This entry establishes that the
device mechanism the disk ships is sound and quantifies what it buys in
like-vs-like play.

Repro: `go run ./cmd/ponder-match -mode measure -pairs 200 -seed 7` (the
control: `-mode control -pairs 100 -seed 11..13`; the symmetry proof:
`-mode control -pairs 50 -seed 21 -pairseed`). Full per-move logs were
written under `runs/ponder-match/`.
## 2026-08-08 — UI code-region reclamation: $E000-$F6FF headroom 52 B -> 1,056 B

**Size-only, behavior-identical** (all internal/ui gates green unchanged,
`engine.bin` byte-identical to main, disk boots in the same 7.16 s / 149
stage-1 sectors). Two levers, in order of preference and landed as two
commits:

**1. Densification of cold code (-93 B of UICODE).** The UI runs between
moves at human speed, so unlike the engine it trades cycles for bytes freely:
`uistatic` (50 B) was DEAD in the shipping payload — only `asm/uitest.s`
calls it; it is now assembled only under the new `UITESTBUILD` define.
`uigoto0` (uigotorc at column 0, the overwhelmingly common call) folded
fifteen `ldx #0` sites into one. `m8main`'s take-the-display block was
`uidhoff` minus one store and now calls it.

**2. Read-only data relocation to LANGUAGE CARD BANK 2 (-997 B of UICODE,
+~120 B of window/copier code).** The new `UIDATA2` segment at $D700 — above
the book's name table, in the 2,394 B that were free there — holds EVERY
string (873 B incl. WHOOFF), the start position (STSQ/STPC, 64 B), the level
tables (36 B) and KTAB (24 B). The copier lifts it at boot with the same
whole-page loop that lifts UICODE ($C083 window, bank 1 restored; m8boot.bin
61 -> 94 B, still transient). The readers — `uiputs`, `ui80puts`, `uisetmsg`,
`uititle`'s who-field, `uiscore`'s mate label, `uistartpos`, `uilimits`
(spanning `uimargin`'s KTAB read) — open a bank-2 window and restore bank 1
on every exit, the discipline `uibookname` established. All of them are cold:
nothing inside the search pays a bank switch (the only paint during a search,
`uithinkln`, runs between ID iterations).

**Measured result** (ld65 sizes, `TestUIByteBudget`): UICODE 5,836 -> 4,832 B
of the 5,888 B $E000-$F6FF cap = **1,056 B headroom** (was 52). UIDATA2:
997 B at bank-2 $D700-$DAE4, 1,307 B of bank-2 room left above it. m8.bin
total 5,829 B (was 5,836).

**New gates.** `TestBank2DataResident` (the whole segment reads back from
bank 2 byte-identical to the file, and boot ends on bank 1),
`TestUI80PutsRestoresBank1` (the one banked reader a text boot never runs),
a bank-1 assert in `TestSoftClockLimits`, two link-time asserts (the name
table's page-rounded copy must stay below $D700; UIDATA2 caps at $E000), and
a BLOAD-ledger check in `TestUIByteBudget` (below).

**The next binding ceiling is the BLOAD contract, not $E000.** m8.bin is one
file (UICODE + UIDATA2 back to back) BLOADed at $0900, and it must end below
the resident book at $2000: that leaves **59 B of total-file growth** on the
BLOAD path, against 6,912 B of staging room below the engine on the DISK
path (the shipping artifact). A future feature bigger than that faces a
delivery-contract decision — split the payload, restage, or accept a
bookless BLOAD (the probe's magic check fails closed) — and the new
TestUIByteBudget check makes it a deliberate one. A ~300-400 B feature like
arrow-key move entry fits the $E000 budget six times over, but needs that
call made first.

**Tile-blob redundancy, measured for the record** (zellyn's sprite ideas —
they compress LC BANK 1, which already has 1,048 B free, so they do not help
the $E000 budget and were not pursued): of the 24 tiles' 456 stored 4-byte
rows, only **173 are unique** (283 duplicates, 62%). A per-row 1-byte index
would cut the blob 1,824 -> 1,148 B (456 B index + 692 B unique rows), a
676 B ceiling if bank-1 space is ever short. Unique rows by row-index run
13..22 at the crowns down to 2 at the bases.

## 2026-08-07 — device pondering v1 landed (Scheme A): the disk now thinks on the opponent's clock

**This is an IMPLEMENTATION landing, not a gauntlet.** It ships
`docs/ponder-design.md` §11.A on device (`asm/m8.s` `m8ponder`/`pkclk`,
`asm/ui.s` state). While the human is on move, the engine predicts his reply P
with a shallow search, makes P, and searches root+M+P as deeply as his clock
allows, warming the aux TT for exactly the line a hit reaches. Hit vs miss
collapses (the TT self-verifies); the rule is "never wipe the TT, never commit
the guess." `PONDERON` defaults on — the disk ponders.

**Own-move search is byte-identical.** The keyboard poll joins the search at
`ccsite` via a runtime operand patch to an LC-resident `pkclk`, live ONLY while
pondering — exactly the `FT2_SOFTCLK` precedent. `engine.bin` is unchanged
(SHA identical to the previous commit); `TestMicroAB` is green by
construction. Mutation-checked both new correctness points: breaking the
own-move ccsite isolation makes the own-move search poll the keyboard (device
`TestEngineParity` catches it — "the engine did not move"); removing the
`PONDERKEY` discard guard lets a ponder abort fall into the round-6 own-move
recovery and paint the think line (the new interrupt gate catches it,
`UITHINK=0xc4`).

**Cost.** +326 B of Language Card code (5,477 → 5,803 of the 5,888 B UICODE
cap; ~85 B headroom), ~10 B LC RAM + a 160 B board snapshot in the
previously-free `$FF50-$FFEF`. Zero main-image bytes, zero `engine.bin` bytes,
zero measured own-move hot-path cycles. Cold boot unchanged at ~463 ms. The
ponder walk-away backstop is a fixed ~8 s of estimated clock (the human's
keystroke is the normal terminator); scaling it to the level is deferred.

**Why P is restored by snapshot, not unmake.** `iterate` roots the search at
ply 0 and overwrites P's undo slot, so `ENG_unmake(P)` cannot recover root+M
(the ~20 B the design budgeted). `m8ponder` snapshots BOARD/PIECESQ + the four
position scalars before making P and rebuilds the rest with `ENG_evalinit` —
~50 B, the bulk of the overage over the design's ~230 B estimate.

**Gates** (`internal/ui/ponder_test.go`, device image under HARNESSKBD): the
disk ponders by default; a ponder restores the board exactly, commits nothing,
predicts the same reply as the Go `runEngine` shallow search, and leaves the
aux TT warm for root+M+P; a hit reaches greater reply depth (cold 2 → warm 4
at two of three test positions); a miss's reply is bit-identical to a
no-ponder reply; a real keystroke interrupt discards the guess, keeps the key
for `entkey`, and advances `ENTROPY`. The move-by-move gates disable pondering
in `boot()` — the harness breaks `Run` on any non-blocking keyboard poll, so a
ponder would park mid-search there; the ponder gate drives it with the poll
trap disabled, modelling the hardware.

**★ MEASUREMENT CONSEQUENCE — the headline Sargon number now needs re-picking
(design §10).** The +161 [+126, +199] device number (below) was a *no-ponder*
gauntlet, chosen because "the disk actually runs" without pondering. The disk
now ponders. The representative number becomes the *ponder-enabled, symmetric*
`sargon-symmatch` match (both sides pondering, Hard Mode), and the no-ponder
+161 reverts to the artificial control it always was. That gauntlet has NOT
been re-run here — this entry lands the mechanism and its gates; the
re-measurement (and the device's own shallow-predictor hit-rate, which may
differ from the harness's free-TT predictor) is the next step.

**Deferred** (design §11.B): free-TT prediction (needs the trivial
`ENG_ttprobe` export) and ponder-time banking under `FT2_ADAPT`.

## 2026-08-08 — ★ VERDICT: history + gated-LMP DO NOT PORT — mirror +31 → asm SPRT **−23 ± 19** at device depth. The pruning cluster dies at asm a SECOND time.

The 08-07 adaptation screen made `[ptype][to]` history partition + history-gated
LMP the prize at **+31 ± 11** (mirror, asm-matched cost/ordering) and flagged it
for an asm port gated on the SPRT — because this exact class (LMP) screened +39
in the mirror and died at −85 in asm. It was ported (FT_QHIST, the freed
FT_ROOKX bit) and the SPRT was run. **It died again.**

**The measurement (feature ON, 0x7f, vs the shipped engine, 0x5f):**

| budget | games | Elo | llr(0,10) | note |
|---|---|---|---|---|
| 5 s/move | 300 | +6 ± 30 | 0.03 | underpowered + shallow; did not clear |
| **30 s/move (device depth)** | **800** | **−23 ± 19** | **−3.03 REJECT** | the arbiter |

**Depth is the tell: +6 at 5 s → −23 at 30 s. The regression GROWS with depth**,
the signature of LMP pruning away lines that matter more the deeper you look —
the same mechanism behind the original −85. Spend is clean: the qhist arm spent
**5.94% LESS** total compute (adherence 0.7498 vs 0.7966), so the −23 is not a
compute confound — the feature searches less AND scores worse. A ~6% compute
refund could not rescue a −23.

**Why the mirror was wrong, precisely** (the port's most useful finding): for a
history-based ordering/pruning feature the mirror is a **predictor, not a
bit-exact spec**. The asm↔mirror base parity already tolerates ±1 make at
score-tied cutoffs (independent generators); when the tied cutter is a QUIET,
the two engines bump DIFFERENT `[ptype][to]` slots, and the partition (reorder)
and LMP (prune) AMPLIFY that into node-count and, rarely, score divergence. So
the mirror systematically over-credits history techniques. The +31 was a false
positive of the same family as LMP's +39.

**Disposition: NOT MERGED.** Feature-OFF was verified byte-identical
(`TestMicroAB` fingerprints + `TestFullGameMirrorParity` 780/780, re-checked
here on the main-vs-worktree engines), so the shipped engine is provably
untouched. But the port costs **+399 B of CODE** (headroom 912 → 370 B) even
gated off, for a −23 feature — so it does NOT go into the image (the FT_ASP
cycle: port, reject, gate off, then reclaim the bytes — skip straight to not
merging). The work is preserved on branch `worktree-agent-a5069aff1957caf00`.
A real LMP arming bug (per-move live-window over-pruned PV nodes) was found and
fixed IN that branch's code; it never touched shipped code.

**The strategic close on question (c) "adapt modern techniques":** the cheap
wins were already taken (improving-heuristic LMR +13, check extensions +24).
Everything left in the pruning/reduction family — LMP, IIR, razoring, adaptive
null-R, and now history-fed LMP — is NEGATIVE at asm/device depth. The 6502's
move ordering is not the bottleneck a history table can fix cheaply enough to
pay. Recorded as closed: the technique-adaptation well is dry at the price the
6502 charges.

## 2026-08-07 — ★ ADAPTATION ROUND: the PRUNING CLUSTER pays once its enabler exists — cheap quiet-history ordering + history-gated LMP = **+31 ± 11** at asm cost; ordering alone +19 ± 12; IIR/razoring/null-R/TT-replacement all DROP

The modern-technique round asked: **what ordering signal can the 6502
afford, and what does it unlock?** Six candidate families were built in
`internal/mirror` behind zero-value-off toggles (qhist.go, iir.go,
razor.go, ttrepl.go), each charged its asm-realistic cycles, and screened
at the configuration that predicted the LMP failure — **mask 0x1f
(TT+killers+two-tier MVV, NO SEE, NO history), recap2 QS, check
extensions on both sides, cbudget 143M** (the 30 s operating point).
Feature-off is byte-identical to the current engine (`TestAdaptOffIsNoop`,
and the asm↔mirror parity gates `TestSearchMirrorParity` /
`TestBudgetModeParity` / `TestCheckExtMirrorParity` stay green).

### Verdict table (Elo at 2000–2500 games unless noted; ± is ~1σ)

| technique / form | Elo ± err | asm cost charged | port bytes | verdict |
|---|---|---|---|---|
| **history `[ptype][to]` partition + history-gated LMP (3+2d Dmax3)** | **+31 ± 11** (2500 g) | probe 23c, update 30c, aging 8c/byte + free LMP compare (**≈0.6% of budget**) | **768 B table** (LC bank 1) + ~150 B code | **PORT — the prize** |
| quiet-history partition alone, form 1 `[side][to]`, thr 8 | +19 ± 12 (2000 g) | same rates (**0.46–0.58% of budget**) | **256 B table** + ~120 B code | **PORT — cheap fallback** |
| quiet-history partition alone, form 2 `[ptype][to]`, thr 16 | +18 ± 12 (2000 g) | same | 768 B table | ≈ form 1 on ordering alone |
| history `[side][to]` partition + LMP (form 1) | +20 ± 11 (2500 g) | same + LMP compare | 256 B | LMP adds ~nothing to the coarse key |
| quiet-history LMR-gate (no partition) | +10 ± 26 (500 g) | same | — | weaker than partition |
| TT replacement (depth-pref + age bit) | +7 ± 13 (2000 g) | 15c/store | 0 B (spare depthBound bit 7) | DROP (neutral; field +11 was noise) |
| IIR (reduce 1 at TT-miss), rem≥4 / rem≥3 / any-window | −28 / −17 / −51 ± 25 | 10c/full-width node | ~10 B | DROP |
| razoring, rem≤2 (300 / 400 / 200+400 margins) | −47 / −40 / −17 ± 25 | 10c/test | ~15 B | DROP |
| adaptive null-move R (R=3 at rem≥6 / rem≥8) | −31 / −23 ± 25 | ~0 (below model res.) | ~6 B | DROP |
| **LMP alone, 3+2d Dmax3 (control)** | **−140 ± 29** | pure count compare | — | confirms LMP dead at 0x1f |

### The strategic question, answered — the interaction lesson vindicated

**LMP alone at 0x1f re-measured −140 ± 29** — fully consistent with the
prior −85/−105 verdicts; the enabler was genuinely missing. **Cheap quiet
history is a real ordering signal the 6502 can afford** (256–768 B in LC
bank 1, <0.6% of the cycle budget), used WITHOUT a sort — a one-pass
partition that emits high-history quiets before the generation-order tail
(the O(n²) selection the 6502 cannot pay is never run). On its own it is
**+19 ± 12**.

But the round's real prize is the **combination**: `[ptype][to]` history
partition + LMP whose count-prune EXEMPTS high-history quiets =
**+31 ± 11** over 2500 games (five seeds +11/+27/+26/+30/+57, only the
6502 seed low and reproducibly so). This is the feature-interaction
lesson firing exactly as written (2026-07-2x): **pruning depends on
ordering; build the ENABLER first, then the cluster pays.** LMP went
−140 → +31 once history tells it which late quiets are safe to skip. It
lands in the neighbourhood of the +39 the original LMP screen named as
LMP's "potential given SEE+history-quality ordering" — now realized with
an ordering signal the asm can actually afford.

**The table shape matters for the cluster, not for ordering alone.** The
coarse `[side][to]` key (form 1) gives the same +19 for partition-only but
adds *nothing* under LMP (+20, i.e. LMP is a wash on it): it cannot tell
which specific quiets deserve the exemption. The finer `[ptype][to]` key
(form 2, 768 B) is what turns the exemption into +31. So the port is a
genuine bytes/strength choice: **768 B → +31, or 256 B → +19.**

Everything value-side that isn't ordering **died at asm cost**: IIR,
razoring, and deep null-R all trade shallow-node accuracy the 0x1f search
cannot spare, exactly as fixed-depth theory predicts for aggressive
reductions under coarse ordering. TT depth-preferred replacement promoted
to a flat **+7 ± 13** — neutral; its +11 field screen was the usual fluke.

### Port plan (fits the 811-byte ceiling)

**Primary — the +31 cluster.** A **768-byte `[ptype][to]` byte table in
LC bank 1 $DBE8-$DFFF** (1,048 B free, zero switch cost — the table never
competes for the 811 B main budget). Code in `search.s` (main RAM):
(a) a saturating `CLC/ADC/BCS`-to-$FF bump of rem² on the quiet
beta-cutoff (~30 c); (b) an `LSR`-sweep aging pass in the per-root-move
reset (768×~8 c ≈ 6 K c/move, negligible); (c) one extra tier-filtered
rescan pass in `moveLoop` emitting quiets whose counter ≥ threshold before
the generation-order quiets — the asm form is a move-stack tier-bit
rewrite (like the SEE deferred pass), so the final quiet pass pays no
second probe; (d) the LMP count-prune, gated to skip a quiet only when its
history counter is below `LMPThresh` — one indexed load + compare per
late quiet, table already in a register from (c). Estimated **~150 B of
code**, well under the 811 B ceiling with the 768 B table off in LC bank 1.
Thresholds: partition 8, LMP 8 (screened). If LC bank 1 is tighter than
the 1,048 B measured, fall back to the **256 B form-1 partition-only**
port (+19), whose code is ~120 B and whose table also lives in LC bank 1.

**Do NOT port** IIR, razoring, null-R, or TT replacement — all screened
neutral-to-negative at asm cost, recorded above.

**Skipped, with reasons:** *singular extensions* — needs a TT-move
exclusion re-search per node, doubling the search at candidate nodes;
unaffordable at 1 MHz and the asm has no verified-exclusion path.
*Continuation history* — a second (piece,to)×(piece,to) plane is ≥1.5 KB
and its update is a two-key write per cutoff; the single-ply table already
captures most of the quiet-refutation signal, and countermove (its cheap
cousin) was neutral (+4 ± 9).

Prior art: the March `task41-history-heuristic` branch (f54e84b) SPRT'd
its `[side][ptype][to]` counting-SORT at **−16 ± 30**. This work differs
on every axis the compression story flagged: persistent + aged across
moves (not zeroed each move), rem² bump (not linear), an O(n) PARTITION
(not a 256-bucket counting sort), and — decisively — it is **paired with
LMP**, which the sort port never was. The partition-not-sort choice is
what makes history affordable; the LMP pairing is what makes it big.

Instruments: `internal/mirror/qhist.go` (+ iir/razor/ttrepl.go), CLI
`-aqh/-aiir/-arazor/-anullr/-attrepl` with cost knobs, gates in
`adapt_test.go` (`TestAdaptOffIsNoop`, `TestAdaptChangesTree`,
`TestAdaptDeterminism`, `TestAdaptMateSound`, `TestQHistPartitionExhaustive`,
`TestQHistDiag`/`TestQHistTaxDiag` behind `QHIST_DIAG=1`), run scripts
`runs/adapt-{triage,triage2,stage2,stage3,confirm}.sh`.
## 2026-08-07 — deep optimization review round 6: −0.661% at identical trees, ZERO image bytes, and the fresh profile confirms r5's flatness

Round 6 of the cycle/space review, the first since the evasion filter, pin
ray, pawn count, check extensions and the aux book landed. Strictly
tree-identity-preserving: the gate for every applied change is `TestMicroAB`
— all 18 fingerprints (score, move, search/make/eval/attacked/ttprobe/
generate) byte-identical — plus the EXACT cycle total over those 18
fixed-depth searches, per the 2026-07-31 rule that for a tree-identical
speedup the cycle count outranks any Elo estimate.

**RE-PROFILE FIRST (the brief's order): r5's flatness still holds, slightly
flatter.** `TestProfileR5` at the shipped config (0x5F, FT2_GENDEFER, 30M
budget): top-1 label 4.66% (generateq), top-10 22.2%, top-40 48.8% of 139.4M
cycles — the four near-equal quarters (movegen 26.7% / search 24.4% / board
23.1% / eval 21.3%) are unchanged in shape. The new code from the structural
wins is CHEAP: `sdevade` 0.37% (47.0 cyc/entry), `pinray`+`prwalk` 0.53%,
and the hottest single LINE in the image is eval's per-file pawn recompute
(`PTFILEC`, 3.24%) — already cache-gated by r4. So this round hunted
constant-factor waste in per-node plumbing, and found it in two places: the
per-node ABORT test and the runtime FEATURE-BIT tests.

**Applied (each measured on the exact MicroAB cycle total, fingerprints
byte-identical at every step):**

| change | cycles before → after | delta |
|---|---|---|
| 1. ABORT test folded into the poll site: `checkclock` rearms the divider to 1 once aborting, so search's entry drops its per-node `lda ABORT/beq` (−6 cyc/node) | 3,249,555,705 → 3,243,838,552 | **−5,717,153 (−0.176%)** |
| 2. `snorep` tests NPAWNS before PHASE (conjunction commutes; nearly every node has a pawn): 8 → 6 cyc common path | 3,243,838,552 → 3,242,098,791 | **−1,739,761 (−0.054%)** |
| 3. `fgpatch` feature-gate constant folding (below) | 3,242,098,791 → 3,230,743,497 | **−11,355,294 (−0.350%)** |
| 4. `sgo` cursor bump skips the high-byte +1/store when the 4-byte advance stays in page (63/64) | 3,230,743,497 → 3,228,081,237 | **−2,662,260 (−0.082%)** |
| 5. checkclocks abort-freeze FIX for change 1 (below; delta is address realignment — the touched path is softclk-only) | 3,228,081,237 → 3,225,425,169 | **−2,656,068 (−0.082%)** |
| **TOTAL** | **3,249,555,705 → 3,225,425,169** | **−24,130,536 (−0.743%)** |

**★ The headline mechanism: FEATURES/FEATURES2 are per-search CONSTANTS,
and the engine was re-testing their bits with `lda/and/beq` on hot paths —
0.51% of all cycles across ten sites** (measured per site by scanning the
binary for the gate triples and summing their per-PC profile cycles). The
four hottest — slegal's FT_LMR staging gate (0.130%), eval's FT_PSTRUCT
term gate (0.118%) and FT2_MOPUP gate (0.191%, a gate for a feature that is
OFF in every shipped config), and make's FT_PSTRUCT dispatch gate (0.125%)
— are now single `jmp abs` sites whose operands `fgpatch` folds from the
live feature bytes once per `iterate`, the one place BOTH drivers (engine.s
and m8.s uidrive) already pass: the ccsite FT2_SOFTCLK patch precedent,
generalized. Assembled defaults encode the shipped config. `fgpatch` (81 B)
lives with mopfin/mopcd in the RATTACK-region alignment hole, so the image
does not grow a byte; all five ca65 variants and perft still build (the
PTNOCACHE variant assembles no fgmkps site and its fgpatch skips it).

**TWO GATES FIRED THIS ROUND, and both caught real defects — the round's
best evidence that the gate lattice is doing its job.**

1. `TestMopupEvalParity` failed immediately after the fold: it drives
   `eval` by TRAMPOLINE (callSub), never touching `iterate`, so the
   FT2_MOPUP site still held its shipped-default operand and the mop-up
   never fired with the bit poked on. That is the exact defect class this
   project's gates exist for — a second driver that bypasses the arming
   path — caught in minutes. Fix: `asmStaticEval` now runs the engine's
   OWN `fgpatch` before eval, exactly what the real drivers do (not a Go
   re-implementation of the fold).
2. `TestSoftClockAccuracy` failed in the full battery: change 1's unwind
   mechanism (once ABORT is set, every node polls) meant that under
   FT2_SOFTCLK the poll site is `checkclocks`, which charged a **128-node
   quantum per unwinding node** — pool est/true exploded to **6.843** at
   1 s budgets, invisible to every tree gate because only the estimated
   clock feels it. Fix: `checkclocks` tests ABORT first and **freezes the
   estimate while unwinding**, which is also nearer the truth than the old
   1-in-128 sampling (an unwinding node costs ~45 cycles against PCOST's
   ~4,600/node). After the fix the accuracy gate reads pool est/true
   **1.029** (gate [0.85, 1.25]; 1 s budgets 0.934, 59 s 1.021, against
   ~1.02 before the round). Note the unwind-accounting semantics CHANGED
   (frozen instead of 128-sampled): this is clock accounting, gated by the
   clock instruments, not by tree identity — the same footing as the
   07-30 SOFTA/SOFTB rescales. TestSoftClockAdherence re-run for the same
   reason (result below).

**Measured and CLOSED as negligible (the brief's leads #2 and #3):**
- **checkclocks (softclock accounting)**: the identical R5 workload with
  FT2_SOFTCLK on costs **+11,052 cycles of 139.4M = +0.0079%** — matching
  the +0.0073% recorded at the feature's build. Its per-call cost does not
  need retuning for the new node mix; there is nothing to hunt.
- **bkfetch's 9-byte aux copies**: ~157 cyc/entry × ~100 entries/probe ≈
  **16k cycles once per book move** — 0.05% of ONE 30 s move, only while in
  book (when no search runs at all). Derived, not assumed; no action.
- **check-extension gating** (lead #4): the sckext FEATURES triple measured
  0.009% (1,782 execs) — left as a runtime test; the per-child
  `lda INCHK,y / bne` (5 cyc) is the minimal tree-defining form.

**Tried and REJECTED by the gate — recorded so nobody re-tries it:**
dropping slegal's `clc` on a carry-known-clear argument. The audit missed
the third entry path (`beq spinleg` from the king-alignment test), which
arrives with a data-dependent carry out of `adc #$77`; TestMicroAB caught
the tree change instantly (LEGALCNT double-stepped, LMR staging shifted).
The clc now carries a comment saying exactly why it is load-bearing.

**Space: nothing applied beyond free placement; the ledger for next time.**
- Layout after the round: CODE $4000-$7BC8 (page tail to TABLES: **55 B**),
  TABLES $7C00-$BC60 (RATTACK-hole slack now **8 B** — fgpatch took 81 of
  89), LCCODE load $BC61-$BCC4 (28 B under the 128 B copier ceiling), image
  ends $BCC5: **headroom 811 B, unchanged** — engine.bin 31,941 B all round.
- A 256 B headroom raise needs CODE's end under $7B01 = **196 B of CODE
  shrink**; nothing near that was found dead (the r1/r4/audit passes took
  it). Small pieces exist (ptkapply2's 13-byte branch-range duplicate could
  fold for +3 cyc on the black-king path) but do not reach the page.
- **FT2_MOPUP disposition needs a DECISION, not this round's hands**: it is
  a landed conversion win (2026-07-23, +2 ± 30 self-play, converts KRK/KQK)
  that is OFF in the harness config AND on the device — every match since
  2026-07-28, including both +148/+161 headline numbers, played without it.
  Either it should be ON on-device (zero bytes, its own PHASE gate, but a
  strength decision), or it is dead weight worth **237 B off the image top**
  (mopupterm 221 + MOPMAT tables 16, plus 48 B of hole back). Its eval-path
  gate cost, at least, is now 3 cycles instead of 8.

**Found, NOT applied (tree-identical but needs design work — candidate for
r7):** a sentinel end-of-move-list byte would replace the 16-bit
CURPTR/SENDL+SENDH compare in all five pass-scan loops (~8-11 cyc per list
step; p2loop alone is 0.5%). Interacts with deferred generation's staged
moves, GLIMIT and the batched emitters; estimated 0.2-0.3% for a wide,
carefully-gated change.

**Soft-clock staleness, stated per the standing rule**: every structural
cycle win invalidates the per-node cost table invisibly. This round made a
node ~0.7% cheaper — well inside the margin fit's slack (the 07-30 entries
rescaled at 2.7% and 8.4%; the equal-spend band is ±10%) — so SOFTA/SOFTB
were NOT rescaled, and the next calibration pass should fold this round in.
TestSoftClockAccuracy post-round: pool est/true 1.029 (was ~1.02).

**Verification**: `TestMicroAB` fingerprints byte-identical after every
change and at the end; exact totals as tabled. TestTablePacking,
TestPStructParity, TestPTCacheIdenticalTree, TestPTCacheRandomWalk,
TestMopupConversion, TestMopupEvalParity, TestSoftClockAccuracy green on
the final image; all five build variants + perft assemble and link;
`make test` green across every package; the full `internal/chesstest`
battery (58 min — including TestFullGameMirrorParity 780/780 plies exact
on move/score/every counter, TestIDIterationParity, TestTTSequenceParity,
TestBudgetModeParity, the pin-ray and evasion differentials, and the
TABLES ceiling guard) green on the final image, plus
`internal/sprt.TestSoftClockAdherence` for the unwind-accounting change.

## 2026-08-06 — the board is REDRAWN at 42x19, and the gate that could not fire is replaced

zellyn redrew every piece. The old artwork (DazzleDraw picture **CHESS1**) was
drawn on a **44x21** grid and `cmd/gentiles` sliced it into 42x19 tiles by
dropping the top two source rows of every square — **28 dots of bishop and king
finial, thrown away silently**, which he noticed on screen. The new artwork
(**CHESS2**, `assets/chess2-dazzledraw-save.bin`) is drawn at the engine's own
square size, so the source square and the rendered tile are the same rectangle
and `SrcTrimTop` is **0**. Nothing is clipped. Full write-up:
**docs/ui-design.md §17**; the drawing spec is `assets/README.md`.

**The delivery did not move.** The blob is still **1,824 B** (4 B x 19 rows x 24
tiles), and `asm/tiledefs.inc` and `asm/tiles.inc` regenerated **byte-identical**
— so the stage-2 page table is untouched. Disk: **stage 1 148/176, stage 2
38/176, 187 of 560 sectors**, all three unchanged against a baseline build of
`main`. The two disk images differ in exactly **130 bytes**, which is exactly the
number of bytes that changed in the blob. Boot time **7.13 s** of emulated IIe
time, unchanged. `TestMicroAB` vs `microABGolden` and `TestBookProbeParityASMvsGo`
green: this was the artwork, not the engine.

**The point of the day: `-check`'s headline had gone vacuous, and was not left
that way.** Its top section measured *ink the top trim throws away*. With
`SrcTrimTop = 0` that measurement is structurally zero and can never fail again
— the sixth entry in this project's ledger of gates that cannot fire. Two
changes, both mutation-checked:

- The headline was **widened, not deleted**. "LOST INK" now measures ink outside
  the whole **kept window** — rows *and* dot columns. The dot-column half is
  live (the 4-byte tile row only covers dx 7..34); the row half is a tautology
  today, and the report **says so in as many words** rather than printing a zero
  that looks like a passing check. Mutation confirms the split exactly:
  disabling the dot-column half fails `TestCheckCatchesLostInk`; disabling the
  row half changes **nothing**, which is the claim.
- A **new** check, `[grid]`, asserts what actually went wrong: that the declared
  8x8 grid **exactly fills the frame the artist drew around it**, and that the
  gap between frame and grid is blank. Nothing had ever said the source square
  and the tile were the same size. Redraw at 44x21 now and it fails at the
  source: *"grid right edge x=359 runs into the right border (starts at 348)"*.

Exit status **1 is retired** (it meant "slices but the trim clips ink") and left
unallocated rather than reused, so a script that special-cases it cannot
silently mis-read a new run. `make test` now requires `-check` to exit **0**,
which it could not do while the committed artwork clipped 28 dots.

Board constants re-derived from the pixels, not carried over: bounding box
x 0..**351** y 0..**155** (was 367/171), bottom rule y=**155**, right bar starts
at x=**348** (the old literal 364 is gone — it is derived now). Unchanged and
re-verified against the pixels: origin **(8,2)**, `TileW` **42**, `TileH` **19**,
content window dx **8..34**.

## 2026-08-01 — ★ MIXED MODE SHIPS: the board keeps four lines of 80-column text under it

`asm/8fish.dsk` now boots to the double-hi-res board **with a four-row,
80-column text window under it**, instead of a full-screen board and ESC to a
text screen. §13.4 of docs/ui-design.md priced this on 2026-07-31 and did not
build it; this is what got built. Full write-up: **docs/ui-design.md §14**.

**The board did not move and was not re-sliced.** `DHTOP` is 4 and `DHROWS` is
19, so the eight ranks are scanlines 4-155; MIXED shows graphics on 0-159.
`asm/dhgr.s` changed only in its header comment.

**The blocker was aux RAM, and the fix was to split the book.** Double hi-res
forces 80COL on, so the text window is 80-COLUMN text, whose EVEN columns are
fetched from **aux `$0400-$07FF`** — four 40-byte spans through the middle of
the 7,407-byte blob that lived at aux `$0200`. The book split along a line its
own code already drew:

| piece | size | home | read by |
|---|---|---|---|
| header + entries | 5,705 B | AUX `$0800-$1E48` (439 B spare) | `bookprobe`, ~100 entries/probe |
| name table | 1,702 B | **LC BANK 2** `$D000-$D6A5` (2,394 B spare) | `uibookname`, once per book move |

`bookprobe` has never read a name. Only `uibookname` does, and it runs at
`$E000`, which Language Card bank switching does not re-map — so it selects
bank 2 (`$C083` twice: bank 2 with WRITE ENABLED, because write-protecting the
card protects all of `$D000-$FFFF` including its own `$F780` output buffer),
reads, and restores bank 1. Bank 2 and not bank 1 because bank 1 has 1,048 B
contiguous free against a 1,702-byte table.

**What the four rows show** (the design call): row 20 the inverse title bar
beside whose move it is and CHECK; row 21 the think line — depth, score, the
engine's best move, updated between completed ID iterations — beside
`BOOK: <opening>`; rows 22-23 the message row and the input prompt, each
beside one help line. Left out and one ESC away: the move list, the
coordinates, the long help. Every field is COPIED from the 40-column row that
already renders it, so there is one piece of code per string.

```
 20 | 8FISH 1.0    LEVEL 1     YOU ARE WHITE WHITE TO MOVE                      |
 21 |D 2 -0.05 g8f6                          BOOK: B90 Sicilian, Najdorf        |
 22 |                                         N-NEW T-TAKEBACK R-RESIGN D-DRAW  |
 23 |YOUR MOVE? e2e4_                         L-LEVEL S-SIDES Q-QUIT ?-HELP     |
```

**The one thing it cost.** The window's ODD columns *are* rows 20-23 of the
40-column text page — the same 160 bytes of main RAM. So each window row is
composed in an 80-byte staging line in LC RAM (`UI80BUF`, `$FF00`) and blitted,
never read back off the screen it is overwriting; and `uiswap` repaints the
40-column screen on the way back from the board. "ESC is instant because the
screen behind it is already correct" becomes "ESC costs one more 23,000-cycle
repaint" — 23 ms. `TestDiskBoots` now compares rows 0-19 against the gated
shipping image and asserts the window separately, out of BOTH banks.

**Two new gates, both mutation-checked.**

- `internal/delivery.TestBookClearsTheAuxTextPage` reads `BOOK_BASE` out of the
  generated `asm/book.inc` and the blob length off disk and fails on any
  intersection with aux `$0400-$07FF`. Mutation (`BaseAddr` back to `$0200`):
  *"THE RESIDENT BOOK OVERLAPS THE 80-COLUMN TEXT PAGE'S AUX HALF … overlap:
  $0400-$07FF, 1024 bytes"*. The reason for the whole change now lives
  somewhere that FAILS.
- `internal/ui.TestBookNameRestoresBank1` calls `uibookname` through a `jsr`
  stub in the booted machine and asserts BOTH that the card comes back on
  bank 1 read+write AND that the name it wrote is right — two-sided, because a
  one-sided check would pass a routine that never switched and read the artwork
  instead. Mutations: drop the restoring `lda $C08B` pair → *"returned with
  LANGUAGE CARD BANK 2 still selected"*; select `$C08B` instead of `$C083` →
  *"wrote \"BOOK: \\x03@ \\a1p…\", want \"BOOK: C78 Ruy Lopez, Morphy\""*.

**Unchanged and green:** `TestMicroAB` vs `microABGolden` (the search tree is
byte-identical — this was packaging, not engine), `TestBookProbeParityASMvsGo`
(264 positions / 4,194 probes, blob-integrity check after every probe),
`TestDiskBoardParity` (all 16,384 bytes of DHGR page 1 from a cold `$C600`
boot), the `internal/delivery` layout suite, and `make test` end to end.

**Cost on the disk:** stage 2 grew 37 → 38 sectors (the name table is 7, and it
abuts the artwork so the two stay one span); 186 of 560 disk sectors.
**Boot time: 7.10 s** of emulated IIe time from `$C600` to the first keyboard
poll, against 7.01 s before — the 0.09 s is the extra sectors and their copy.

**Still unverifiable in emulation** (added to docs/ui-design.md §13.5's list):
goapple2 models MIXED as a state bit and has no video scanner, so *where the
graphics/text split actually falls* on hardware is untested — the board has a
4-line margin top and bottom, which absorbs a small error and not a large one
— and 80-column TEXT pixels have never driven a real 14M shift register. The
aux/main column interleave itself is NOT on that list: `Window()`
de-interleaves it the way the scanner does and the gates assert real strings
read back out of both banks.

## 2026-08-01 — ★★ THE DECISIVE NUMBER: +148 Elo vs Sargon III over 360 standard-start games, on the DEVICE config, with ZERO harness artifacts

The measurement this project has been trying to make since the beginning, on
an instrument that can finally be trusted.

    GAMES 360:  +203 =99 -58
    score 70.14%   Elo +148   95% CI [+117, +182]
    360/360 games classified   0 quirk adjudications

**Configuration: the shipped disk, not something adjacent to it.**
`-standard-start -book -noponder -softclock` — both engines on their own books
from move 1, the estimated clock a IIe requires (FT2_SOFTCLK + FT2_ADAPT), and
ponder removed symmetrically because `asm/m8.s` never ponders. Six shards of 60
games, independent dither and book seeds.

| how the 360 games ended | | |
|---|---:|---:|
| checkmate | 261 | 72.5% |
| threefold | 86 | 23.9% |
| insufficient material | 5 | 1.4% |
| move-cap adjudication | 4 | 1.1% |
| stalemate | 3 | 0.8% |
| fifty-move | 1 | 0.3% |
| **harness artifacts** | **0** | **0.0%** |

**Zero is the number that matters.** The previous standard-start run
(2026-07-26, 300 games, +132) reported **17 quirk adjudications, 5.7%**, and
the 07-29 gauntlet 2.0%. Those were the move-number defect fixed this morning:
Sargon renders move 100 as `:0`, the driver could not parse it, and every game
that got that far died with Sargon's reply visible on screen and invisible to
the harness. Here every one of 360 games ended by a rule of chess.

**The number is if anything CONSERVATIVE on compute.** Mean `spend_ratio`
(8fish total / Sargon total) is **0.9506** — 8fish used about 5% LESS emulated
compute than its opponent across the run — and own-budget adherence is 0.9676.
It won by 148 Elo while spending less than the machine it beat.

**Why this supersedes +132 rather than merely updating it.** That number came
from an instrument with a 5.7% artifact rate, and the artifacts were **not
conservative** as that entry claimed: measured this morning, the games that
crossed move 100 scored 0.375 for 8fish, not the 0.5 an adjudicated draw hands
out, so the old figure was biased UPWARD by roughly 2 Elo. The honest reading
is not "+132 improved to +148"; it is that +132 was measured through a broken
instrument and +148 is the first standard-start figure worth quoting.

**What is being claimed, precisely.** 8fish beats Sargon III decisively at
equal per-move cycle budget on an emulated 1.02 MHz 6502, from the standard
starting position, with both engines using their own opening books, in the
configuration the bootable disk actually runs. The lower bound of the interval
is **+117**.

**What is NOT being claimed.** Nothing about real hardware — no game in this run
was played on an Apple IIe, and nobody has yet played 8fish on one. Nothing
about Sargon III at its own native time control on original hardware. And
nothing about other engines of the era: Sargon III is one benchmark, and
docs/results.md's own 6502 landscape note puts Schröder/Mephisto above it.

## 2026-08-01 — ★ the DISPLAY is a gate now: goapple2 learns IIe double-res video, and zellyn's 7-dot shift is MEASURED

zellyn implemented Apple IIe graphics for OpenEmulator, found that the simplest
correct implementation shifted the screen **left by 7 dots**, and was then
surprised to see **the same shift on his physical IIe**. That made
`libemulation` a reference cross-checked against real hardware, and it was
worth spending on: goapple2's `videoscan` scanned 40-column text, lo-res and
hi-res out of one bank, so everything about the double-resolution display was
prose.

**What the 7-dot shift IS.** The aux byte is clocked out **half a cell early**:
cell *c* runs x=14c−7..14c−1 (aux) then x=14c..14c+6 (main), so the whole
double-resolution picture sits 7 dots left of where the same bytes appear in 40
columns. In OpenEmulator it is one term — the 80-column painters start at
`x*14 - CELL_WIDTH/2`, the 40-column ones at `x*14`.

**And it is a MOVE, not a crop.** The port's first model dropped the dots at
x<0, and the 80-column readback test immediately spelled the screen's first
character as a space. Wrong: those dots land in the **overscan**, which is
exactly why the effect reads as "shifted" rather than "lost a character". The
frame's coordinate space starts at MinX = −7.

**Three caveats retired, and they were retired the same way 80STORE was:** the
oracle existed, only the emulator side was missing. §13.5's "video output
itself", §14.6's "where the graphics/text split falls" and "80-column text
pixels" are now `internal/ui/videoscan_test.go`, which boots the SHIPPING DISK
from `$C600`, runs one complete 65x262 field through the scanner, and asserts
what a monitor would show: the split at scanline 160, the board on 4–155 with
blank borders, all four 80-column rows read back as text, and the video sense
(inverse/normal) correct.

**Verified here, not taken on report.** Deleting the shift term fails **7**
goapple2 tests and **all five** chess6502 gates, with the diagnostic naming the
hardware observation: *"the board's memory fits the rendered dots at offsets
[0]; want exactly one, −7"*. goapple2's `videoscan`/`iie`/`chargen` and
chess6502's `make test` are green; `shiny/shiny.go` still carries its
uncommitted local changes, untouched.

**★ ONE GATE FELT SUFFICIENT AND WAS NOT.** A *half* mutation — the MIXED split
moved in the data path but not the address path — did **not** fail
`TestDiskScannerMixedSplit`: the scanner fetched text bytes and drew them as
hi-res, matching no DHGR row, so the split still "looked" right. The window
readback tests caught it. Worth recording next to the other gate failures this
week: a mutation that survives your best single gate is not proof the gate is
wrong, it is proof the gate is narrow.

**§14.7's prediction was 105/119; the measurement is 105/120.** The extra dot is
the ARTWORK, not the hardware — the h-file's last dot column is background in
all eight squares. The left edge is exact.

**Also found on the way:** `go test ./videoscan` **had never been able to run** —
three `%s` verbs applied to a `byte`/`rune` in `convert.go`, and vet's printf
check runs as part of `go test`, so the whole package failed to build. Fixed in
its own commit. The per-cell repaint cache was also a package-level variable, so
two Scanners over different memories silently shared it. And two screen facts no
byte-level gate could see are now asserted: the prompt cursor is a solid inverse
block (56/56 dots) one column past the prompt, and the title bar is inverse for
exactly its first 40 columns, not all 80.

**What is still NOT verified**, written up in ui-design §15.4 rather than
implied away: glyph shapes (both sides use the same generator — what is gated is
geometry and video sense), NTSC colour (this is monochrome dots), double lo-res
(modelled by neither side; `RenderFrame` returns an error rather than drawing
something plausible and wrong), the overscan extent, and all of §13.5's
surviving table. **This is emulation agreeing with emulation**, cross-checked
against a model that was itself checked against a real IIe for one datum. It is
a large step up from prose and it is not a hardware test.

## 2026-08-01 — ★ the residual Sargon quirk class, ROOT-CAUSED: SARGON'S MOVE NUMBER IS NOT DECIMAL PAST 99

Every standard-start game that reached move 100 was ending as a harness
artifact scored as a draw. Reproduced live at the measurement budget, traced to
the byte, fixed, and re-measured.

**The mechanism, measured on the machine.** Sargon III renders its move-list
number with a two-digit routine that adds `'0'` to each column and never
carries out of the tens, so the tens column runs off the end of the digits:

| move | 99 | 100 | 109 | 110 | 119 | 126 | 128 |
|---|---|---|---|---|---|---|---|
| shown | `99` | `:0` | `:9` | `;0` | `;9` | `<6` | `<8` |

Found by diffing **all of main RAM** across consecutive moves: `$1388` is the
only byte that increments by exactly one per full move AND equals the number on
screen. Forcing it just under each boundary and playing on gives the table
above directly (`TestMoveNumberRendering`).

**Why that killed games.** Since 2026-07-26 the displayed move NUMBER — not the
column text — is the driver's commit and move-accepted signal, because it is
the only reading immune to the mid-search repaint. `strconv.Atoi(":0")` fails,
so from move 100 the row vanished from the parsed list: `LastSargonEntry` and
`LastOwnEntry` froze at 99, Sargon's replies were **on screen and invisible**
however long the driver waited, every injected move looked unaccepted, and the
game died on `no reply after CTRL-T` and was adjudicated a draw. Verbatim from
the failing game's dump — Sargon's reply is right there:

	      98  D6-D2        F3-G3
	      99  C3-B2        C1-F1
	      :0  B2-C3

**★ The fix that removed one artifact class created this one.** `eb1e719` moved
commit detection from the column TEXT to the move NUMBER. That was right — it
removed the stale-token desyncs that cost 15 of 300 games — and it quietly made
the driver depend on parsing a field that stops being decimal at move 100.
Nothing tested past 99 because nothing had ever *looked* past 99: the old
text-based driver did not care what the number said, which is also why the
three games (across 600) that reached move 128 were ever observed. The 2026-07-29 audit named this
class correctly ("games that pass move 99, where the move-number column
overflows to `:0`") and it was never verified or fixed; it stood as the whole
residual quirk count of that gauntlet (10/504 = 2.0%).

**The measurement.** Same binary, same disk, same budget, standard start, books
live on both sides; the arms differ only in the parse.

| standard-start, B = 30M cyc/move | games | quirk-adjudications | games that crossed move 100 | what happened to them |
|---|---|---|---|---|
| **before** | 83 | **1 (1.2%)** | 1 | 1/1 died there |
| **after** | 170 | **0 (0.0%)** | 12 | **12/12 finished by a rule** |

**Read the third column, not the second.** 1-in-83 against 0-in-170 is by
itself weak (Fisher p ~ 0.33) — the event is rare because only ~2% of games get
that far. The decisive number is CONDITIONAL: reaching move 100 was a
mechanically certain death (1/1 here, 10/10 in the 2026-07-29 gauntlet), and
after the fix 12 games crossed it and **all 12** ended by a rule of chess —
5 checkmates, 2 insufficient-material, 1 fifty-move, and 4 at the `move-cap`
adjudication at Sargon's 127-move capacity (254 plies). One of them is 8fish
losing a 251-ply game it would previously have been handed a draw in.

The before arm's 1.2% is a FLOOR, not an estimate: it was stopped mid-flight to
free cores, and the games in progress when a run is stopped are exactly the long
ones. The complete-run figure to quote for the defect is the gauntlet's 2.0%.

**★ And these artifacts were NOT conservative, which corrects 2026-07-26.** That
entry argued the quirk draws "cost 8fish wins and cannot manufacture them", so
the score was an under-estimate. Measured: of the 12 games that crossed move
100, 8fish scored **1 win, 4 losses, 7 draws = 0.375**, not the 0.5 an
adjudicated draw hands it. Games long enough to reach move 100 are games 8fish
did NOT win, and forcing them to draws was generous to 8fish: at a 2%
incidence that is +0.25% of score, about **+2 Elo**. Small, and in the opposite
direction from the one that was claimed.

Both arms: **0** `CrossCheckHistory` desyncs, **0** unreadable/illegal Sargon
tokens, **0** Hard-Mode/LEVEL-9 mode warnings. The 2026-07-26 repaint class
stays fixed; nothing else showed up.

Neither arm's Elo is worth quoting at these sample sizes (before 64.5%, after
66.5%); the point of the runs is the artifact count. Spend symmetry held
throughout (mean per-game `spend_ratio` 0.96).

**Gates.** `TestParseListNumber` and `TestParseMoveListPastNinetyNine` (fast,
over rows captured verbatim from the live failure) and `TestMoveNumberRendering`
/ `TestMoveNumberRenderingTensRollover` (`SARGON_SLOW`), which force Sargon's
counter to 97 and 118 and require the driver to keep playing through 100 and
120 with every reply legal on an independent referee. All four fail with the
decode reverted, and the live pair fails **exactly as production did**, with
`no reply after CTRL-T` and Sargon's move sitting on the `:0` row.

**Two related instrument defects, found on the way and fixed:**

- **`ErrListWrapped` would have replaced one artifact class with another.** It
  fired the first time Sargon's newest move number went backwards near the
  127-move capacity — but that is also exactly what the repaint's blank frame
  looks like, and the window (`baseNo >= 126`) was unreachable only because the
  driver froze at 100. It now requires the reading to REPEAT on the next poll
  (polls are >= 500K cycles apart, past the ~167K repaint). Observation-based
  on purpose: a "settle and re-read" would hand Sargon thinking cycles the
  budget never granted it.
- **Six exits from `playGame` returned a draw with NO `TERMINATION` line at
  all** (bad FEN, machine construction, boot, InfiniteLevel, the Hard-Mode
  assertion, setboard). Same shape as the 2026-07-31 `plies=-1` regex defect
  one level further up: there the line existed and the regex missed it, here
  there was no line to miss. Every exit now logs exactly one `TERMINATION`.

**What the entry of 2026-07-26 got wrong, for the record.** Its guess at the
17 quirks was "most plausibly a mis-scrape of one of Sargon's 2078 instant book
replies", from the correlation with the book-heavy run. The correlation was
real and the mechanism was not: it was the repaint, which fires on the FIRST
search after Sargon leaves its book — which is why a book-heavy run showed it
and the pool run did not. Diagnosed and fixed the same evening (`eb1e719`);
this entry is the class that was left.

## 2026-08-01 — adversarial review of the board merge: a build rule that could ship a BROKEN DISK, and a gate that had gone quiet

Two reviewers went at the chain-load branch before it merged, one on the
loader/delivery half and one on the engine-visible half (the book's move to
aux). Both verified their base ref first — the fix from 07-31 held, 0 commits
behind. Everything below was reproduced here before being fixed.

**★ `make dsk` could ship a disk whose stage-2 page table disagrees with the
disk. Reproduced end to end.** The tiles rule used a GNU Make **grouped
target** (`out1 out2 out3 &: deps`) to say "one gentiles run writes all
three". `&:` needs make >= 4.3; **macOS ships 3.81**, which parses it as four
INDEPENDENT targets — one of them named literally `&`. Their mtimes then float
apart, and make will assemble the copier from a stale `asm/tiledefs.inc` and
regenerate the blob afterwards:

    cd asm && ca65 -g -D SDCHAIN m8.s ...   <- stage-2 page table, OLD sizes
    go run ./cmd/gentiles                   <- blob regenerated
    go run ./cmd/mkdsk                      <- disk built from the NEW blob

Demonstrated with a stale `TILE_BLOB_SIZE = 1500`: the copier shipped **6**
tile pages where the disk carried **8**, so tile pages 7-8 would land at
`$2000`, the entire opening book would arrive two pages low, and its last two
pages would fall off the end of the table. `make dsk` exits 0 and prints a
normal-looking ledger.

**The Go gates could not have caught it**, and that is the part worth keeping:
they re-assemble from the freshly regenerated `.inc`, so the tests never see
the artifact `make dsk` produced. A green suite and a broken disk, from the
same tree. Fixed with the portable spelling (the two `.inc` files depend on
the blob, empty recipe), verified by `make -n` ordering before and after.

**A gate that had gone quiet.** `chesstest.newStubMachine`'s guard asserted
that a GDVERIFY build and a resident book were mutually exclusive, because
GDVBUF (main `$3000`) overlapped the book at main `$2000-$3CEE`. It could no
longer fire, for **two independent reasons**: it read `m.Mem.MAIN[BOOK_BASE]`
while `BOOK_BASE` had become an AUX address, and `LoadBook` now writes
`m.Mem.Aux`; and main `$0200` is PWBITS/PBBITS, always zero on a fresh
machine. The invariant it guarded had also dissolved — the book is in aux, so
there is no overlap to be exclusive about. Deleted, and replaced with the
invariant that IS live: GDVBUF sits inside the **DHGR main half**, so a
GDVERIFY build and the on-device board are mutually exclusive
(`TestDebugBufferPlacement`). `asm/defs.inc`'s GDVBUF comment has now been
wrong twice — first "above the blob" with a pre-widening size, then
"overlaps the resident book" — and says so.

**Two new assertions, both mutation-checked here:**
- `patchPageTable` now checks the loader's SHAPE before writing 147 bytes into
  it (`$0800 = $01`, `INC $0806` at `$0802`, `LDA $084E` at `$0805`, the `JMP`
  at `$084C`). Those four bytes were asserted only by a test; a `diskii`
  upgrade that moved the table would have had `make dsk` write a page table
  over loader code and exit 0. Flipping one expected byte makes the build
  refuse, as it should.
- `.assert SD2TABLEN <= 177` in `asm/m8.s`. The store loop writes
  `sta $084E,x`, so a longer table runs past `$08FF` into the engine's per-ply
  arrays and the loader wraps its table read into its own code at `$0800`.
  The Go side enforced the 176-sector ceiling; ca65 did not.
- `AsmBookProbe` now compares the whole aux blob after **every** probe in the
  parity suite (264 positions, 4,194 probes). `bkfetch`/`bkhdr` copy entries
  out of aux with RAMWRT off; leave RAMWRT on and those stores land at AUX
  `$03D6-$03E6` = blob offset `$01D6-$01E6`, **inside entries 52-53**, so one
  slipped switch corrupts the book permanently rather than misreading once.
  A targeted mutation (RAMWRT on for `bkhdr`'s 8 stores only) is caught with
  the exact predicted address: *"MODIFIED the resident blob at aux $03DF
  (offset 479 of 7407)"*.

**Two comments cited ROM addresses that are not the instructions they name.**
Checked byte-for-byte against the P5 ROM image: the denibble read-back is
`lda ($26),y` at **`$C6DC`**, not `$C6D9` (which is the `DEX`); and `$26` is
set at **`$C652`**, not `$C659` (not an instruction boundary at all). The
mechanism the comments describe is right, which is exactly why the wrong
address would have survived — nobody re-derives an address that is already
being used to justify a correct decision.

**What the reviewers CONFIRMED**, which matters as much as what they found:
the `$084F` page-table offset (proved with a disambiguating disk, since the
shipping one has `Base == $0D00` making the JMP's high byte and the first
table entry both `$0D`); the read-back mechanism; `BKENT`/`BKHDR`'s one-byte
margin above the Disk II ROM's denibble table, at both ends; that the two page
tables are derived independently in ca65 and Go and compared; and — walking it
instruction by instruction — that the book probe's SELECTION is byte-identical
after the move to aux, with `BKENT` refilled at exactly the four points where
`ENTPTR` changes. Nothing in the Language Card page moved; `ttfetch` is still
`$D000`.

Stale claims corrected in `asm/defs.inc` (RUNPTR is a save slot now, not an
indirect base; aux `$0200+` is the book, not the TT; page 3 is free of ENGINE
allocations but NOT of the Disk II ROM), `cmd/genbook` (the generator emitted
a stale header four lines above the `BOOK_BASE = $0200` it also emits),
`docs/book.md`, `internal/delivery`, and `asm/dhgr.s`.

## 2026-07-31 — the DHGR board SHIPS: two-stage chain load, book to aux, 7.01 s boot

The disk boots to the hand-drawn double-hi-res board. Full write-up in
`docs/ui-design.md` §13; this is the measurement record.

**Delivery.**

| | sectors | of one page table | contents |
|---|---|---|---|
| stage 1 | 146 | 176 | copier $0D00 (176 B), UI payload $0E00 (5,074 B), engine $4000 (31,941 B) |
| stage 2 | 37 | 176 | tile blob $0E00 (1,824 B), opening book $2000 (7,407 B) |
| disk | 184 | of 560 | 376 sectors free |

**Boot time: 7.01 s**, measured from the `$C600` entry to the first keyboard
poll, on the real nibblised disk through the real Disk II boot ROM. Previous
figure 6.15 s for 176 sectors of a single-shot image. The breakdown:

| | cycles | s |
|---|---:|---:|
| stage 1 (146 sectors) | 5,478,812 | 5.37 |
| stage 2 + both copies + dhclear/dhinit + first paint | 1,677,631 | 1.64 |
| **total** | **7,156,443** | **7.01** |

+0.86 s against the old disk, for 8 more sectors, 9,231 B of new payload, two
block copies (9,231 B), a 16,384-byte screen clear and a whole-board repaint.
The loader stayed sequential: same ROM, same 2:1 interleave, one extra entry.

**The ceiling was misstated, and the correction is worth more than the number.**
Standard Delivery's page table is `$084F-$08FF` = **176 sectors = 45,056 B**,
not `$084E`/177/45,312: the `LDA $084E` at `$0805` is pre-incremented at
`$0802`. `diskii mksd`'s "44 KB cap" is not a tool policy, it is that same 176
× 256. 8fish is 183 sectors, so single-shot was over by SEVEN sectors, not one.

**And the table is a list of pages, not a base address.** Stage 1 scatter-loads
three spans ($0D, $0E-$21, $40-$BC) with zero gap sectors; the old contiguous
image would have paid 30 sectors for the $2200-$3FFF hole. This was not part of
the plan and is worth more than the chain load: every future "it does not fit"
is now a layout question.

**Renderer cost, re-measured**: `dhboard` is **192,667 cycles** per whole-board
repaint (188.3 ms; the earlier 193,667 was measured before `dhclear` shifted
the code, and the 1,000-cycle difference is the measurement's own granularity,
not a speed-up). Plus a one-time ~115,000 for `dhclear`. The 40-column screen
is repainted every time as well (23,826 cycles) so ESC is instantaneous;
together that is 0.7% of a 30-second move.

**Language Card budget**: UICODE 5,074 B of 5,888 (was 4,453); the renderer is
380 B of code + 97 B of generated tables and the board-default byte. 1,054 B of
the $E000-$FFEF LC still free. The 1,824-byte artwork plus its 456 B of
init-built tables are in LC BANK 1 at $D300-$DBE7 and cost the payload nothing,
leaving 1,048 B free there; LC bank 2 is still entirely unused (4,096 B).

**Proof**: `TestDiskBoardParity` boots the shipping `.dsk` from `$C600` and
asserts all 16,384 bytes of DHGR page 1 against `internal/tiles`' independent
Go model. Not a harness variant — the artwork came off the disk in a second
loader entry, through LC bank 1, painted into aux through RAMWRT.

**★ 80STORE and AN3 are no longer untestable.** goapple2's `iie` package models
both now (with the RAMRD/RAMWRT override precedence, validated against a2audit,
and a `DHires()` accessor), so `sta CLR80STORE` and `sta SETDHIRES` are
ASSERTED on the booted disk rather than documented as hardware-only hopes. This
project's own lesson — the ALTCHARSET bug was found only when someone modelled
the switch — paid out again, this time confirming the code was already right.

**Which screen comes up is patched, not compiled.** The renderer blits from LC
bank 1 and only the chain loader can put artwork there, so a BRUN of
m8boot.bin would have painted 1,824 bytes of leftovers as a chessboard. The
default is a payload byte that is $00 in both links; the chain loader stores $1
into it in RAM. Found by adversarial review, not by a gate — there was no gate,
because internal/ui's BLOAD path pokes the blob in for fidelity and could not
tell the two apart. There is one now (TestBoardNeedsTheChainLoad), and it
asserts on the artefacts.

**Three bugs found by gates, one by review:**
- the aux-capability probe wrote aux `$0300`, which stopped being transposition
  table and became the RESIDENT BOOK when the table moved to `$4000`. It probes
  `$3F00` now: DHGR page 1 in both banks, scratch by construction.
- the book's main→aux copy, run from the copier, overwrote the copier itself on
  an Apple ][+ (where `$C005` is not a soft switch), so the machine check that
  prints NEEDS A 128K APPLE IIE never ran. It runs AFTER the check now.
- `internal/chesstest`'s TABLES ceiling guard computed the image top with a
  hardcoded copy of `__LCCODE_SIZE__`. Growing LCCODE by 35 B did not fail it —
  826 B of slack — it just made the printed headroom 35 B too generous. It
  reads the linker symbol now.

**Engine**: `TestMicroAB` matches `microABGolden` — the search tree is
untouched. `engine.bin` grew 35 B (31,906 → 31,941) for the book probe's two
aux primitives; no engine core source was modified.

**What did NOT ship: mixed mode.** DHGR forces 80-column text, whose even
columns come from aux `$0400-$07FF`; rows 20-23 are 160 bytes in four pieces in
the middle of the 7,680-byte aux hole the 7,407-byte book occupies, and the two
largest chunks either side are 7,256 B — 151 B short. The board is full screen
with ESC to the 40-column text screen (Sargon III's arrangement). The route
through is costed in ui-design §13.4: move the book's 1,702-byte name table
into the Language Card, leaving 5,705 B that fit aux `$0800-$1FFF`.

## 2026-07-31 — the 80STORE finding now has a TEST: goapple2 stage 2 lands, `TestA2AuditAuxmem` un-skipped, a2audit untouched

Two days ago I filed the missing `sta CLR80STORE` with the note that
**"goapple2 does not model 80STORE, so no test in the repo could ever have
caught it."** That was true about goapple2 and false about the project: the
oracle already existed. `a2audit/audit/auxmem.asm` runs its four banking
tests with 80STORE ON, and again with 80STORE + PAGE2 ON; `video.asm`
exercises AN3. `TestA2AuditLangcard` had been passing all along. The aux
half was sitting behind a `t.Skip` whose message named exactly what was
missing — *"needs 80STORE/PAGE2/HIRES and INTCXROM/SLOTC3ROM support
(stage 2)"*. I had dismissed stage 2 as infrastructure with no consumer in
heartbeat after heartbeat. The DHGR renderer was its consumer.

**goapple2 (`e646c2f`, `923fbdc` on `master`, not pushed).** 80STORE
($C000/$C001, status $C018) with the precedence that is the entire point of
the switch: it **overrides** RAMRD/RAMWRT for $0400-$07FF — and, with HIRES,
$2000-$3FFF — which then follow PAGE2 alone for reads and writes. Plus AN3
($C05E/$C05F) with a `DHires()` helper, and $C100-$CFFF ROM switching
(INTCXROM, SLOTC3ROM, and the INTC8ROM decode a $C3xx access sets and a
$CFFF access clears).

**`TestA2AuditAuxmem` is un-skipped and passes IN FULL** — all twenty
RAMRD x RAMWRT x 80STORE x PAGE2 x HIRES combinations plus a2audit's Cxxx
ROM tests, verified running here (not skipping) with a IIe ROM from the MAME
`apple2e` set that `internal/ui/diskboot.go` already uses. Without a ROM only
tests $15-$1D skip, by name, with the reason logged; everything through $14
always runs. Nine mutants of the new code (PAGE2 sense inverted, HIRES
gating dropped, RAMRD allowed to win, SLOTC3ROM ignored, $CFFF not clearing
INTC8ROM, …) were each caught by the audit — the pass is not vacuous.

**Three harness bugs blocked the run; none were in a2audit, which stays
byte-for-byte untouched** (verified, and the repo is clean):
- Missing `JSR COPYTOAUX`. a2audit does this itself on a IIe; without it the
  tests turn RAMRD on and the audit code vanishes from under the PC.
- SP starts at $00, parking the stub's return address in **$0100 — one of
  the thirteen addresses the tests seed and INC**. Fixed with `TXS`, as
  a2audit's own `main` does.
- Missing `JSR SETNORM`. COUT1 ANDs every character with INVFLG, so **every
  a2audit failure screen had been coming back blank** — including
  `TestA2AuditLangcard`'s diagnostics, for as long as that test has existed.

**The payoff, verified independently here.** `TestDiskBoots` now boots from
the state a IIe actually hands 8fish — 80STORE + PAGE2 **on**, as PR#3 and
ProDOS leave them — and asserts `m8main` turned 80STORE back off. Commenting
out the one store makes it **fail**; restoring it passes. The old assertion
used `Unhandled[0xC000]` as a proxy and would have broken the moment the
switch became real.

**One finding worth keeping.** With the store deleted, **the screen still
renders correctly**, because `sta TXTPAGE1` leaves the text page on main.
Only the direct switch assertion catches it; an end-state screen comparison
never would. The damage stays invisible until the engine's aux TT traffic
reaches $0400-$07FF.

**The lesson.** "No test can catch this" was a claim about the emulator that
I stated as a claim about the project, and it retired a real defect into the
"hardware-only, argue it in prose" bucket for two days. Before concluding
that something is untestable, check whether the missing piece is an oracle
or merely an implementation — this one was hardware-verified and already
vendored.

## 2026-07-31 — ★ THE AUDIT WAS LYING: 2.4% of gauntlet games were harness artifacts reported as "0 quirk adjudications"

Chasing a loose end I had noted twice and never opened — "8 games have no
TERMINATION line (unclassified)" — turned out to be an instrument defect in
the analyser that produces this project's headline number.

**Root cause: `plies=-1`.** Quirk-unresolved games emit
`TERMINATION g19 result=draw reason=quirk-unresolved plies=-1`, and
`analyze.py` matched `plies=(\d+)` — no sign. Those lines therefore failed the
regex **entirely**, landing in NEITHER the termination classifier NOR the
quirk counter. The data was never missing; every shard has exactly one
TERMINATION per game (verified: 42/42 in all twelve).

**The consequence.** Two consecutive gauntlets printed
`quirk-adjudications: 0/252 = 0.0%` and `CrossCheckHistory DESYNC: 0 (CLEAN)`
while **12 games (2.4%) had ended as harness artifacts scored as draws** —
and asymmetrically: **8 in the soft arm, 4 in the exact arm**. I relayed
"zero quirk adjudications" as evidence of a clean run, twice. It was a false
clean bill of health.

**Impact on the numbers: small, and it does not change any conclusion.**
Excluding the artifact draws:

| arm | as reported | artifacts excluded | delta |
|---|---|---|---|
| `soft` (device) | 66.47%, +119 | 67.01%, **+123** | +4.2 |
| `off` (exact) | 62.70%, +90 | 62.90%, **+92** | +1.5 |

Both move slightly UP, the gap between arms is essentially unchanged, and the
previous entry's conclusion (the decline was noise; the direct A/B says the
build is not weaker) stands. The earlier +161 run had the same 13 unmatched
lines, so it was affected identically and the before/after comparison is
unaffected in direction.

**Fixed:** the regex accepts a sign, so both arms now classify 252/252 and the
audit reports the artifacts as artifacts (3.2% and 1.6%, tagged
`<-- HARNESS ARTIFACT`).

**The lesson, which is the fifth of this exact shape this week.** An audit
that cannot parse a case reports that case as ABSENT rather than as UNKNOWN,
and "0.0%" reads as reassurance. The "unclassified" count was printed right
next to the "0 quirks" line the whole time and I read past it twice, because
the number I was looking for said what I wanted. A count of things the
instrument could not classify belongs in the FAILURE path, not in a footnote.

## 2026-07-31 — the direct A/B settles it: the Sargon decline was NOISE, and for a tree-identical speedup the CYCLE COUNT is the better instrument

Current engine vs the pre-optimization build (`d36fda6`, before the evasion
filter), equal 30 s cycle budget, same opponent, same conditions, only the
engine differs. 300 games:

    +92 =121 -87   score 50.8%   elo +6 +/- 30   llr(0,10) 0.03
    spend A=402.4G B=400.9G  equal-total-spend A/B = 1.0038 (0.38%)
    adherence 0.8830 / 0.8791

**Spend is equal to 0.38%**, so both builds burned the same compute and the
comparison is clean.

**This closes the previous entry's open question.** The Sargon re-measure had
both arms drifting down and could not say whether that was noise or something
real. Here the point estimate is **positive** (+6), so the current build is
not weaker; the Sargon decline was sampling noise, as the 1.2-1.8 SE drops
suggested.

**But it does NOT confirm the gain either, and cannot.** −8.5% cycles is
1/0.915 = 1.093x the nodes, log2 = **0.128 doublings**, which at this
project's measured 130-150 Elo/doubling predicts **+17**. The interval here
is [−24, +36] — it *contains* +17 comfortably, but ±30 cannot resolve a
17-point effect. Getting to ±8 would need roughly (30/8)² ≈ 14x the games,
about 4,200 at 30 s/move. That is not worth buying.

**★ The methodological point, which matters more than the number.** For a
**tree-identical** speedup, the Elo SPRT is the WEAKER instrument. The cycle
measurement is exact and noise-free (−5.26%, −2.48%, −0.74%, each verified on
identical trees with `TestMicroAB` pinning score, move and every node count);
the SPRT adds ±30 of sampling noise and no information. The causal chain —
fewer cycles per node → more nodes per budget → more depth → more Elo — has
no free parameter to doubt once the tree is proven identical.

So the standing rule "asm time-budget SPRT is the final gate" is right for
FEATURES, which change what the engine plays and therefore need a
play-quality verdict. It is the wrong gate for a pure speedup: there,
**demanding an SPRT substitutes a noisy instrument for an exact one.** Record
the cycles, prove the tree, and stop.

Practical consequence: the −8.5% stands as measured, and no further Sargon
games should be spent trying to see it.

## 2026-07-31 — ★ HONEST NEGATIVE: the Sargon re-measure did NOT show the predicted gain. Both arms moved DOWN, within noise.

I predicted +30-35 Elo. The measurement says otherwise, and the prediction is
retracted rather than defended.

**The prediction.** Since the +161 device measurement the engine gained
−8.5% cycles (evasion filter −5.26%, pin ray −2.48%, pawn count −0.74%) and
the margin-tail fit restored 8-10% of clock the long levels had been
discarding. Those compound to ~18% more effective compute at 30 s — about
0.24 doublings, order +30-35 Elo, which should sit above a 252-game arm's
noise floor.

**The measurement** (504 games, paired, ponder off both sides, same rig,
same seeds, same openings, same budget as the +161 run):

| arm | before | after | delta |
|---|---|---|---|
| `soft` (device config) | 71.63%, **+161** [+126, +199] | 66.47%, **+119** [+83, +157] | −5.16% |
| `off` (exact clock) | 70.24%, **+149** [+109, +193] | 62.70%, **+90** [+54, +128] | −7.54% |

Each drop is only **1.2-1.8 SE** — neither is significant on its own, and the
intervals overlap. But both moved DOWN when the prediction was firmly UP, so
the honest summary is: **the predicted gain is not visible, and the point
estimates moved the wrong way.**

**Spend symmetry, read first as always:** `soft` 0.9618, `off` 0.9387 (both
under 1.0, 8fish still taking less compute than Sargon; ponder confirmed zero
on both). The soft arm's ratio rose from 0.9554, and its edge over the exact
arm grew from +11.7 to **+28.6 Elo** — which IS consistent with the margin fit
doing what it was built to do. That part of the story holds; it is the
absolute level that did not move as predicted.

**Audit: clean.** Both arms 0 CrossCheckHistory desyncs, 0 unreadable/illegal
markers, 0 Hard-Mode/LEVEL-9 violations across all 504, 0 quirk
adjudications, 0 games reaching the move-99 overflow zone. So this is not a
harness artifact. (8 and 5 games respectively still have no TERMINATION line
and are unclassified — the same unexplained residue as the previous run.)

**Why I am not concluding "the optimizations made it weaker".** The exact-clock
arm's only change is a faster engine under an unchanged cycle budget, which
can only buy depth. Two candidate explanations remain and the gauntlet cannot
separate them: (a) ordinary sampling noise, which 1.2-1.8 SE is entirely
consistent with; (b) something real that this instrument is too blunt to see.

**Next step, chosen because the gauntlet is the wrong tool for this question:**
a direct asm SPRT of the current engine against the pre-optimization build
(`d36fda6`, before the evasion filter) at an equal 30 s cycle budget. Same
opponent, same conditions, only the engine differs — far sharper per game
than Sargon, and it answers "did −8.5% cycles make it stronger?" directly.
Running now.

## 2026-07-30 — ★ THE LONG LEVELS WERE THROWING AWAY 8-10% OF THEIR CLOCK, at every opening and since the 2026-07-27 recalibration. Margin table octaves 15+ fitted: **100% → 92%**, for THREE bytes and no engine change

The `PROBE_FIRSTOPEN=16` slice that failed the equal-spend band was real but
**not what it looked like**. Chasing it found a bigger and much duller defect
underneath: the flat, never-measured tail of the margin table was costing the
two levels a user actually picks about a tenth of their allocated compute, on
*every* opening, and the gate could not see it because it ran six openings out
of twenty and one octave out of four.

### 1. The slice is not a population — it is four games

The paired probe reproduces at **0.8641** on openings 16-19. But run on **all
twenty** curated openings, the per-opening soft/exact at 30 s has standard
deviation **0.062** and a range of **0.7467 to 1.0109**. That makes the
standard error of a 4-game slice **0.031** and of an 8-game slice 0.022, so
the three slices' means — 0.9176, 0.9136, **0.8689** — differ by about 1.3
combined standard errors. There is no distinct opening population here.

Two openings inside the *passing* slice 0-7 (opening 3 at 0.8326, opening 7 at
0.8119) are worse than two inside the failing one. Correlation between an
opening's ratio at 4 s and at 30 s is only **0.39**: an opening is barely
"intrinsically" anything.

**The reported interval was also too tight, and that is a real instrument bug
now fixed.** `TestPairedClockProbe` bootstrapped by resampling PLIES. The
plies of one game are one deterministic trajectory sharing a position, a
material balance and a carried TT — resampling them treats ~40 correlated
observations as 40 independent ones. That is how four games produced a
confident-looking [0.8076, 0.9248]. The probe now resamples **whole games**.

### 2. What is real: the whole population, at every long level

All twenty openings, n≈800 per octave, shipped margin table, before any fix:

| octave | level | margin | soft/exact | 95% (game-clustered) | est/truth | soft adh | exact adh |
|---|---|---:|---:|---|---:|---:|---:|
| 13 | 4 s | 127% | 0.9853 | [0.9568, 1.0112] | 0.9577 | 0.9120 | 0.9256 |
| 15 | **15 s** | 100% | **0.9206** | [0.8844, 0.9619] | 1.0469 | 0.8253 | 0.8964 |
| 16 | **30 s** | 100% | **0.9035** | [0.8731, 0.9301] | 1.0284 | 0.8630 | 0.9552 |
| 17 | 60 s | 100% | 0.9700 | [0.9537, 0.9870] | 1.0321 | 0.8247 | 0.8502 |

LEVEL 7 and LEVEL 8 sit on the floor of the [0.90, 1.10] band with a fifth and
a third of their interval *underneath* it. Both **passed** the gate. And 15 s
was supposedly a MEASURED anchor — measured, it turns out, on eight openings.

### 3. It is BOTH diseases, and the sweep separates them

Sweeping the soft arm's margin at 30 s over all twenty openings:

| margin | effective per-move bias | soft/exact | soft adh | mean depth (exact 3.888) |
|---:|---:|---:|---:|---:|
| 100% | 1.0486 | 0.9035 | 0.8630 | 3.824 |
| 95% | 0.9962 | 0.9461 | 0.9038 | 3.871 |
| 90% | 0.9437 | **1.0013** | 0.9564 | 3.924 |

Interpolating to an **exactly unbiased** clock (effective bias 1.000, i.e.
margin 95.4) gives soft/exact **0.9430**. So of the 9.65-point deficit:

- **4.0 points is BIAS** — per-move mean est/truth **1.0486** at this octave.
  Deep trees take far more TT cutoffs and a TT-cutoff node is counted but
  nearly free, which the taper-phase regressor cannot see.
- **5.7 points is VARIANCE** — relative RMS **8.5%**, fed through `idloop`'s
  `now + 2*cost <= BUDGET` threshold. **No unbiased cost model can recover
  it.** This is the 2026-07-27 finding reproduced from the other side: a
  perfect clock still underspends here, so the clock has to be biased.

Between openings, correlation(est/truth, soft/exact) is **−0.63**: bias
explains about 40% of the spread and threshold luck the rest. Openings 17 and
18 have near-identical est/truth (1.0914, 1.0852) and soft/exact 0.9154 vs
**0.7467**.

### 4. How far back: it is the 2026-07-27 recalibration, not this week's rescales

Same probe, same twenty openings, 30 s, three builds:

| build | soft/exact | 95% | est/truth |
|---|---:|---|---:|
| `5c25da9` — recalibrated clock, before both rescales | 0.8910 | [0.8575, 0.9288] | 1.0444 |
| `b5837b5` — after the pre-make evasion filter + /1.0845 | 0.9034 | [0.8766, 0.9326] | 1.0294 |
| `f6ba3e0` — after the pin test + /1.0278 (HEAD) | 0.9035 | [0.8731, 0.9301] | 1.0284 |

**Flat at 0.89-0.90 across all three.** The two rescales were correct and did
their job — they restored the level and slightly improved it. This deficit
predates them and is residue of the recalibration itself, which fitted one
cost curve across two octaves and left everything above octave 15 at a guessed
100%.

### 5. The fix: fit the tail, which needs a margin BELOW 100%

`softMarginPct[15..23]` **100 → 92**, and `asm/m8.s` KTAB re-encoded from a
multiplier over 256 to one over **128**. That second half is not cosmetic: the
correction needs the poked budget to be *larger* than the nominal allocation,
i.e. K = 25600/92 = **278**, which is not a byte. Over 128 the same byte spans
50-255% and the ladder is 101 / 113 / 139.

On device that costs **three bytes**: `uimargin` pre-doubles the addend with
`asl BUDGET0 / rol BUDGET1 / rol BUDGET2` (zero page) so the existing `>>8`
delivers `BUDGET*K/128`, and the `entry 0 means leave it alone` early-out is
deleted. **The disk image does not grow at all** — 44,962 B, SD spare **94 B**
unchanged; UI growth room 167 → **164 B**. `engine.bin` is untouched: no
engine source changed, so the search tree is identical by construction, not by
assertion.

92 and not 90 because 90 measures at dead parity (1.0013, adherence 0.9564)
while 92 lands 1-3% under it — the same posture the 4 s anchor was fitted to
(0.9853). Underspending is benign; overspending on hardware is a forfeit, and
this estimator's bias has moved 4% twice in one week under structural wins.

**Measured after the change**, same instrument, all twenty openings:

| octave | level | margin | soft/exact | 95% | soft adh | exact adh | mean depth soft / exact |
|---|---|---:|---:|---|---:|---:|---:|
| 13 | 4 s | 127% | 0.9853 | [0.9568, 1.0112] | 0.9120 | 0.9256 | 2.219 / 2.289 |
| 15 | 15 s | **92%** | **0.9935** | [0.9548, 1.0325] | 0.8906 | 0.8964 | **3.395** / 3.364 |
| 16 | 30 s | **92%** | **0.9666** | [0.9462, 0.9859] | 0.9233 | 0.9552 | **3.890** / 3.888 |
| 17 | 60 s | **92%** | **1.0191** | [0.9965, 1.0440] | 0.8664 | 0.8502 | **4.457** / 4.425 |

Every adherence is under 1.00, so nothing moved toward the forfeit line, and
the 4 s octave is byte-for-byte unchanged. **The recovered compute is real and
it arrives as depth**: at 30 s the soft clock's mean completed depth goes
3.824 → 3.890 against the exact clock's 3.888 — it now reaches the same depth
an engine with a real clock reaches, which is the entire point of the feature.
Move agreement 0.973 → 0.981.

### 6. Octave 17 is not octave 16, and that is why the gate got longer

The pre-fix table above is the argument: at margin 100 the three long levels
read 0.9206, 0.9035 and **0.9700**. A 60 s search completes depth 4.4 and its
exact-clock control spends only 0.8502, so both arms sit far from the budget
and the predictive gate is much less sensitive there. One entry fitted at 30 s
therefore lands 60 s slightly ABOVE parity (1.0191), which is fine — but
nothing would have said so. Gating one octave and calling the other eight
covered is the same mistake as the flat tail itself.

### 7. The gate

Three holes, all of the "the gate does not cover what the product uses" shape
that four other defects took this week:

1. **`TestSoftClockAdherence` ran six openings of twenty.** `Run` gives pair
   `p` the opening `Openings[p % 20]`, so `pairs = 6` played openings 0-5 —
   which are the healthy end of a distribution with sd 0.062. Now
   `pairs = len(Openings)`.
2. **`TestPairedClockProbe` defaulted to eight openings and two octaves.** Now
   all twenty, and 4 s / 15 s / 30 s / 60 s — every timed level `asm/m8.s`
   offers, and both ends of the entry that covers more than one of them. ~27
   minutes; that is the price.
3. **The band was asserted on a point estimate.** The pre-fix build passed
   [0.90, 1.10] at 0.9035 with an interval of [0.8731, 0.9301]. The band is
   **not widened** — it is the same [0.90, 1.10] — but the game-clustered 95%
   interval must now be contained in it, so a midpoint parked on the floor is
   reported as what it is: unresolved, not passing.

### 8. Priced and REFUSED: the makes-per-node regressor

The 2026-07-27 entry named it (a make counter at ~0.29% of runtime) and this
investigation is the evidence that would have justified it — so it was priced
properly and still refused. It attacks the **variance** term (5.7 of the 9.65
points), because a TT-cutoff node makes no moves and that is exactly what the
phase regressor is blind to. But the margin fix already recovers the spend for
**three bytes and zero runtime**, so the regressor would not buy more compute;
it would only tighten the per-opening spread (sd 0.062). Against that: 0.29%
of every search, engine bytes out of a 94-byte spare, and a standing
obligation to refit it on every structural win that makes a node cheaper — an
obligation that has already come due twice this week. Not worth it. Revisit
only if the per-opening spread itself starts costing Elo.

**THE UNPAIRED GATE, post-fix, 20 pairs = 40 games per arm.** This is the
instrument that would forfeit a game, so it is the one that has to be under
1.00, and it is:

| level | soft adherence | exact adherence | equal spend |
|---|---:|---:|---:|
| 4 s | 0.9137 (3,726 moves) | 0.9102 (3,660 moves) | 1.0039 |
| 30 s | **0.9175** (3,986 moves) | 0.9043 (4,094 moves) | 1.0147 |

The 4 s row is identical to the pre-fix run to four decimals, which is the
check that octave 13 really was left alone. (This instrument carries the whole
variance of game composition — its two arms play different games and here
produced 3,986 vs 4,094 moves — which is why the ratio reads 1.015 where the
composition-free paired probe reads 0.967. That is the disagreement
`TestPairedClockProbe` was built to remove, not a contradiction.)

**GATES.** `TestPairedClockProbe` (all four octaves, all twenty openings),
`TestSoftClockAdherence` (20 pairs, both arms), the five parity gates
(`TestFullGameMirrorParity`, `TestBudgetModeParity`, `TestIDIterationParity`,
`TestSearchMirrorParity`, `TestTTSequenceParity`), `TestMicroAB` (fingerprint
asserted, unchanged — no engine source was touched), `TestSoftClockLimits`
(device KTAB vs `chesstest.SoftClockMargin` at every level, now including
three sub-100% margins), `TestSoftClockMarginRule`,
`TestSoftClockMarginEquivalence`, and the full `internal/chesstest`,
`internal/ui`, `internal/ucibridge`, `internal/sprt`, `internal/entropy`,
`internal/delivery`: green.

## 2026-07-30 — ★ STRUCTURAL: single-ray pin test replaces `attacked()` for king-aligned movers, **−2.48% of all cycles for ZERO disk bytes**, tree-identical, no SPRT

Structural win #2 from the 2026-07-30 slack hunt, and the cheapest one yet:
the whole change fits in the `TABLES` page-alignment tail, so **the Standard
Delivery image does not grow by one byte** and SD spare stays at 94 B.

| | before | after | delta | predicted |
|---|---:|---:|---:|---:|
| TestStructHunt/R5 workload (6 FENs) | 142,947,168 | 139,403,400 | **−3,543,768 (−2.479%)** | +2.3% |
| TestMicroAB (18 fixed-depth searches) | 3,365,500,241 | 3,249,555,705 | **−115,944,536 (−3.445%)** | — |
| `attacked` entries (R5) | 28,574 | 15,505 | **−13,069 (−45.7%)** | — |
| `slfull` entries (R5) | 23,641 | 10,572 | −13,069 | — |
| engine.bin / SD image | 31,906 / 44,962 B | 31,906 / 44,962 B | **0 / 0** | — |

Same work in 97.5% of the cycles = **+2.54% more search per unit of clock**
(and +3.57% on the MicroAB workload, which is more full-width).

**THE WASTE.** `slfull`'s legality test asked "is my king attacked after this
move" with `attacked()`, a 16-slot piece-list scan. 13,160 of the workload's
23,641 `slfull` calls (56%; 43% of the 30,894 there were before yesterday's
evasion filter, which is the figure the hunt priced) were for movers whose FROM
square lies on a ray through their own king — the `bne slfull` arm of the
lazy-legality gate — and **99.3% of them turned out LEGAL**. 13,069 full
scans, 325 cycles each, 4.25 Mcyc: **3.0% of everything, spent proving moves
legal.**

**THE REPLACEMENT** (`asm/board.s` `pinray`, 50 bytes). A move can only leave
its own king attacked by opening a line, and the only line a plain move opens
is the one through the square it VACATED — the capture square becomes occupied
by the mover, so it opens nothing. The parent was not in check (that class goes
to `sdevade`), so no other attack can exist. So the whole question is "does the
ray from the king through FROM now end on an enemy slider of the right kind":
one walk from the king, **2.81 BOARD probes per call measured**. Measured
end-to-end, an accepted king-aligned move went from **1,233 to 963 cycles**
(sdomove → verdict, make included), so the walk plus its `jsr`/`rts` and two
setup stores costs about **88 cycles against the scan's 325** — better than the
hunt's 50-60 cyc estimate for the walk once the gate's own 33-cycle alignment
lookup is credited to it rather than counted twice.

Running it POST-make is what makes it small. A pre-make version needs extra
logic for a pinned piece moving ALONG its pin ray (legal) and for one moving
to the far side of its own king; post-make both fall out for free — the mover
on TO simply IS the ray's first blocker in the first case, and is not on the
ray at all in the second.

**ONLY THE ACCEPTING DIRECTION IS LOAD-BEARING, deliberately.** `bcs slfull`
sends a "the ray is exposed" verdict to the real `attacked()` scan for
confirmation, because there are only **91** of them against 13,069 accepts
(0.7%) — 30k cycles to keep the rejecting direction out of the trust boundary
entirely. So a too-STRICT ray test costs 325 cycles; only a too-PERMISSIVE one
could corrupt a search. (The differential asserts both anyway: a wrong strict
verdict means the rule has drifted.)

**REUSE, MEASURED AND DECLINED.** `pinray` is the same ray-walk shape as
`cknodir`'s discovered-check walk, with the color test's polarity inverted and
different exits. Folding them into one `jsr`'d routine was costed: it saves
~31 image bytes and taxes `cknodir`, which this workload enters **35,942**
times against `pinray`'s 13,160, for **+359k cycles (0.25% of everything,
~13% of this change's whole win)**. And the 31 bytes buy nothing *today*: both
versions fit the page tail, so both cost the disk zero. What sharing would buy
is a bigger tail for the NEXT change (140 B instead of 109 B), which is worth
less than a quarter percent of every search from here on. Two routines, one
cross-reference comment that states the polarity difference, and
`TestPinRayTables` + `PINVERIFY` as the things that actually keep them in step.
Revisit if the tail ever becomes the binding constraint again.

**TREE IDENTITY, measured.** Every tree-shape counter is byte-identical:
`search`, `squiesce`, `snode`, `snodeq`, `eval`, `generate`, `generateq`,
`ttprobe`, `ttstore`, `scut`, `emitmove`, `sgo`, `sdomove`, `p1go`–`p4nkgo`,
`sreploop`, `pawntermfull`, `genrecapent`, `sntry`, `srdefer`, `hashcatchup`,
`cknodir`, `ckdwalk`, plus **`make` and `unmake` (38,117 both)** and every
score and best move. `attacked` falls **alone**, by exactly the 13,069 accepts
— which is this change's signature and, unlike the evasion filter's, does NOT
move `make`, because the test runs after it. `TestMicroAB`'s 18 rows agree on
score, move, `search`, `make`, `eval`, `ttprobe`, `generate` and disagree on
`attacked` in all 18; the golden table's `attacked` column was re-recorded on
that argument and nothing else was touched.

**THE MIRROR DID NOT NEED IT — and that is verified, not assumed.** The
evasion filter needed mirroring because it removed `make` calls, a parity-gate
quantity. This one removes only `attacked()` calls, and `Costs.Attacked` is
**0** in `DefaultCycleCosts`: `attacked` was never in the cycle fit, precisely
because its mirror/asm per-node frequency already diverged (the mirror does
not model the pre-existing lazy-legality fast path either). So mirror `Est` is
unchanged, the trees are unchanged, and all five parity gates are green with
no mirror code change. What DID move is the asm's true cost per node, so the
fitted coefficients now over-price the asm by ~2.5% — visible as
`TestBudgetModeParity`'s reported spend ratio and nothing else:

| TestBudgetModeParity | before | after |
|---|---:|---:|
| spend ratio asm/mirror, median (all / pool / endgame) | 1.060 / 1.096 / 0.997 | 1.035 / 1.066 / 0.979 |
| completed depth matched exactly (n=284) | 254 (89.4%) | **261 (91.9%)** |
| depth skew (all / pool / endgame), tolerance ±n/10 | −14 / −7 / −7 | **−7 / −3 / −4** |
| tree / move / score divergences | 0 / 0 / 0 | 0 / 0 / 0 |

The ratio fell by exactly the cycles the asm stopped spending, and depth
agreement IMPROVED — the mirror was already the slower twin, so making the asm
cheaper moved the two together. No refit was made: a refit is
`TestCycleModelFit`'s job against a regenerated `TestMicroABPhase` ground
truth, and the frozen table it fits has not been regenerated since 5652dc1.
`internal/mirror/search.go` records all of this so the next reader is not left
to infer it.

**AND IT MADE THE SOFT CLOCK STALE AGAIN — same mechanism, same instrument,
same fix.** `FT2_SOFTCLK`'s table is a price PER NODE, so every structural win
that removes per-node work invalidates it, invisibly to every tree-identity
check, because the estimated clock is the only thing the shipped device runs
on. In game conditions a node got **~2.7% cheaper**:

| octave | est/truth before | after the pin test | after the rescale |
|---|---:|---:|---:|
| 4,000 ms | 0.9249 | 0.9528 | **0.9264** |
| 30,000 ms | 1.0123 | 1.0380 | **1.0101** |

Left alone the engine believed it had spent 2.7% more than it had and stopped
early: at 30 s its paired spend against an exact-clock control fell 0.9260 →
**0.8933**, outside `TestPairedClockProbe`'s [0.90, 1.10] equal-spend gate.
`SOFTA`/`SOFTB` were therefore **rescaled by 1/1.0278** (25357 → 24671,
587 → 571) — a RESCALE and not a refit because the two octaves agree on the
factor to within 0.5% (1.0302 vs 1.0254), so `SOFTC`/`SOFTK` and every
per-octave margin stand as measured:

| octave | soft/exact before | after the pin test | after the rescale | depth agreement |
|---|---:|---:|---:|---:|
| 4,000 ms | 1.0125 | 0.9935 | **1.0066** | 0.963 |
| 30,000 ms | 0.9260 | 0.8933 (FAIL) | **0.9143** | 0.978 |

Paired completed depth at 30 s rose 3.753 → **3.944** on the same clock, which
is the win arriving where it is supposed to. An INDEPENDENT replicate on a
disjoint opening slice (`PROBE_FIRSTOPEN=8`, n=320 per octave) agrees that the
rescaled clock is inside the band: soft/exact **0.9736** at 4 s and **0.9125**
at 30 s, est/truth 0.9926 and 1.0332 — the same picture on positions the fit
above never saw.

**AN HONEST CAVEAT, MEASURED BOTH WAYS.** A THIRD slice
(`PROBE_FIRSTOPEN=16`, and only n=160 because the opening list runs out) fails
the 30 s band at soft/exact **0.8641** [0.8076, 0.9248]. That is NOT this
change: the **baseline build fails the same slice harder, 0.8401**
[0.7764, 0.9077]. So it is a property of that opening population plus half the
sample size, it predates the pin test, and the rescaled build is the better of
the two on it. The gate's own configuration (openings 0-7, n=320) and the
disjoint 8-15 replicate both pass. Worth a look on its own account — the 30 s
octave is the shipped level and 0.84-0.86 is real compute left on the table for
some openings — but not as part of this change.

**SPACE — the whole change is FREE on disk.** 59 bytes of CODE
(`pinray` 50, the gate +9), and CODE's end moved $7B58 → $7B93 while `TABLES`
stays page-aligned at $7C00: the growth landed entirely in the 168-byte page
tail the last change left behind. engine.bin is **unchanged at 31,906 B**, the
SD image at **44,962 B**, MARGIN 1 spare at **94 B** and MARGIN 2 UI room at
**167 B**. The page tail is now 109 bytes; the next change that exceeds it
costs a full 256-byte page against 94 B of spare and forces the chain-loader
decision, so the base-raise lever staying exhausted is unchanged news.

**GATES.** A `ca65 -D PINVERIFY` variant runs BOTH the ray test and the old
make + `attacked()` path at every move the ray test accepts and exits 104 on
disagreement — 206,700 accepted-and-confirmed-legal over the corpus, 0 traps —
and it PROVES ITS OWN TRAP twice: `PVFORCE=1` manufactures a disagreement out
of the declined-and-illegal population and must exit 104, and inverting
`pinray`'s slider test in the source (a real break, not a hook) makes both
`TestPinRayVerify` and `TestPinRayDifferential` fail loudly. The differential
is exhaustive over a 32-position corpus: **208,895 gate-eligible moves,
101,683 of them king-aligned so `pinray` actually ran, 0 disagreements** in
either direction against an independent oracle (`shCheckers` on a
Go-constructed post-move board) AND against a model of the rule written from
the geometry rather than from the asm's tables. Coverage counters, all required
nonzero: not-aligned 107,212, exposed verdicts 2,195, rays blocked by an own
piece 67,718 / by a non-sliding enemy 8,695 / running off the board 23,075,
**pinned piece moving ALONG its ray (legal) 2,128 versus OFF it (illegal)
2,195**, promotions 370, white-king walks 114,161, black-king walks 94,734,
king moves declined 32,658, en passant declined 71, in-check parents declined
36,143 — and **all eight ray directions separately** (E 13,434, W 20,339,
N 18,734, S 10,192, NE 12,045, SW 9,381, NW 10,396, SE 7,162), because a walk
broken in one direction only is invisible to a corpus that never walks that
way. `TestPinRayTables` additionally proves the two table facts the code's
shortcuts rest on: `DELTATAB` is exactly antisymmetric about $77 over all
4,032 on-board square pairs (`pinray` NEGATES one lookup instead of computing
the reverse difference), and no non-slider type shares a bit with
`ATK_DIAG|ATK_ORTHO` (so a knight, king or pawn behind the vacated square
reads as "no pin" with no extra compare).

All five parity gates (`TestFullGameMirrorParity`, `TestBudgetModeParity`,
`TestIDIterationParity`, `TestSearchMirrorParity`, `TestTTSequenceParity`),
the full `internal/chesstest` and `internal/mirror` suites, `internal/ui`,
`internal/ucibridge`, `internal/sprt`, `internal/entropy`,
`internal/delivery`: green. `TestSoftClockAccuracy` pool est/truth 1.023
(unchanged, gate [0.85, 1.25]).

## 2026-07-30 — ★ STRUCTURAL: pre-make evasion filter + maintained pawn count, **−6.01% of all cycles**, tree-identical, no SPRT

Two structural wins from the 2026-07-30 slack hunt, both **tree-identical**, so
the gate is a cycle measurement plus a legality differential rather than an
SPRT. Measured on the TestStructHunt/TestProfileR5 workload (six phase-diverse
FENs, shipped `FEATURES=0x5F`, `FEATURES2=FT2_GENDEFER`, 30 Mcycle budget):

| | baseline | after | delta | predicted |
|---|---:|---:|---:|---:|
| total cycles | 152,082,034 | 142,947,168 | **−9,134,866 (−6.01%)** | — |
| ... pre-make evasion filter | | | **−8,004,758 (−5.26%)** | +5.4% |
| ... maintained pawn count | | | **−1,130,108 (−0.74%)** | +0.72% |

Same work in 94% of the cycles = **+6.39% more search per unit of clock**.

**#1 PRE-MAKE EVASION FILTER** (`asm/search.s` `sdevade`). At an in-check node
a non-king, non-ep move can only be legal if it CAPTURES the checker or
INTERPOSES on the checker→king ray. The engine used to find that out by doing
make + gives-check + accumulator update + `attacked()` + unmake and throwing
89.4% of the results away (7,253 of 8,114 such moves illegal, 8.69 Mcyc =
5.71% of everything). It is now two `DELTATAB` lookups on the parent position,
because make's own gives-check propagation already knows the checker: the two
`ckhit` exits store `CHECKERSQ[ply]` and `CHECKDIR[ply]` (the checker→king
step) for ~10 cycles each. Measured filter cost 47.0 cyc/entry over 10,971
entries = 0.36%; the plumbing costs 0.03%.

*All 7,253 rejects are caught*: the `1b-evasion-other` illegal count went
7,253 → **0**, and the 861 legal ones still verify through `attacked()`.

**SOUNDNESS ARGUMENT.** Only REJECTION must be sound — accepting still runs
make + `attacked()`, so an over-permissive filter costs cycles and nothing
else. Rejection needs only ONE genuine checker, and "this move does not
address THAT check" stays true under DOUBLE check (where no non-king move is
legal at all), so the filter has no double-check test and needs none. King
moves, ep captures and the `curincheck` plies that record no square
(`CHECKERSQ = NOSQ`: castle/ep parents, the root) fall back unchanged.

**#2 INSUFFICIENT MATERIAL → maintained pawn count** (`NPAWNS`, ZP `$32`).
`smdloop`/`smdnext` walked all 32 piece slots asking "is there a pawn":
1,161,636 cycles, 79,033 iterations, **all of it in the two endgame positions**
(2.2% of endgame cycles). Now one `lda`/`bne` (31,528 cycles total). The count
is maintained where pawns leave the board — `takepiece`/`takepieceq`'s
pawn-victim prologues (every capture path funnels through them, en passant
included) and make's promotion branch — and restored on exactly the matching
unmake paths. Verified against make/unmake for ep and all four promotion types.

**TREE IDENTITY, measured not assumed.** Every tree-shape counter is
byte-identical on both workloads: `search`, `squiesce`, `snode`, `snodeq`,
`eval`, `generate`, `generateq`, `ttprobe`, `ttstore`, `scut`, `emitmove`,
`p1go`–`p4nkgo`, `sgo`, `sreploop`, `pawntermfull`, `genrecapent`, `sntry`,
`srdefer`, plus every score and best move. `TestMicroAB`'s 18 rows agree on
score, move and all of search/eval/ttprobe/generate.

`make`, `unmake`, `slfull`, `sloopj` and `attacked` each fall by EXACTLY 7,253
(the rejected moves) — one removed make per removed `attacked()`, in every
MicroAB row too, which is the filter's signature and nothing else's.
`hashcatchup` falls 1,382 → 1,271: making and unmaking an illegal move used to
repair the elided-hash suffix as a side effect. The repair is idempotent and
the hash VALUES are unchanged (identical `ttprobe`/`ttstore` counts and
identical trees), so this is a conservative cache doing less work.

**THE MIRROR NEEDED THE FILTER TOO** — verified, not assumed. The filter is
tree-identical but it removes `make()` calls, and `TestBudgetModeParity`
compares make COUNTS and prices them through `Costs.Make`. So
`internal/mirror` got the same pre-make filter plus `asmCheckerSq`, which
reproduces the asm's CHOICE of checker (direct first, then the discovered
walk) rather than merely finding some checker — under double check the two
differ, and picking the other one would diverge the accounting. Post-change
spend ratio asm/mirror: **median 0.997**, 0 tree / 0 move / 0 score
divergences over 252 same-tree positions.

**GATES.** A `ca65 -D LEGALVERIFY` variant runs BOTH the filter and the old
make + `attacked()` path at every filtered move and exits 103 on any
disagreement — and, because "never fired" and "cannot fire" look identical
from outside (the lesson of `CKVERIFY`/`GDVERIFY`), the test PROVES ITS OWN
TRAP by poking `LVFORCE` to manufacture a disagreement and requiring 103.
Alongside it, `TestEvasionFilterDifferential` is exhaustive over a 25-position
check-heavy corpus: 190,799 in-check moves, the asm's verdict recovered from
whether control reached `make` or `sdrej`, compared in both directions against
an independent oracle (`shCheckers`) and against the documented rule.
**126,480 rejections, 0 unsound, 0 rule disagreements**; the two instruments
independently agree on the same 126,480. Coverage counters (all required
nonzero): single-check 190,619, double-check 180, checker-capture accepts
6,950, interposition accepts 3,788, king moves 53,512, en passant under check
6, `CHECKERSQ=NOSQ` fallbacks 108, knight checkers 42,118, promotions under
check 360, DIRECT `CHECKERSQ` writes 34,635, DISCOVERED writes 500. That last
one matters: the profile workload contains **zero** discovered checks, so
`cksetcx` would have been shipped unexercised without a corpus built to reach
it.

**AND IT MADE THE SOFT CLOCK STALE — the second-order cost, caught by
`internal/sprt` and fixed.** `FT2_SOFTCLK`'s cost table is a price PER NODE,
so any change that makes a node cheaper makes it stale — and this one attacked
precisely the cost the 2026-07-27 refit said the phase regressor cannot see
("check evasions and illegal-move rejections"). In GAME conditions, where
those moves live, a node got **8.4% cheaper** — more than the 6.0% on the
profile workload, exactly as that diagnosis predicts:

| octave | est/truth before | after the filter | after the rescale |
|---|---:|---:|---:|
| 4,000 ms | 0.9266 | 1.0031 | **0.9249** |
| 30,000 ms | 1.0146 | 1.1022 | **1.0123** |

Left alone the engine believed it had spent 8.4% more than it had and stopped
early: at 30 s its paired spend against an exact-clock control fell 0.9146 →
**0.8524**, outside `TestPairedClockProbe`'s [0.90, 1.10] equal-spend gate. It
was handing back a fifth of the win the filter had just bought — invisible to
every tree-identity check, because the device configuration is the only one
that runs on the estimate. `SOFTA`/`SOFTB` were therefore **rescaled by
1/1.0845** (27498 → 25357, 637 → 587). A RESCALE and not a refit because the
two octaves agree on the factor to within 0.4%: the curve's shape is
unchanged, only its scale, so `SOFTC`/`SOFTK` and every per-octave margin stay
as measured. Result — better than before the filter at both octaves:

| octave | soft/exact before | after the filter | after the rescale | depth agreement |
|---|---:|---:|---:|---:|
| 4,000 ms | 1.0761 | 1.0084 | **1.0125** | 0.944 |
| 30,000 ms | 0.9146 | 0.8524 (FAIL) | **0.9260** | 0.994 (was 0.978) |

Pool est/truth 1.024 (`TestSoftClockAccuracy`, gate [0.85, 1.25]). The 4 s
in-game adherence also moved AWAY from the forfeit line, 0.9928 → 0.9150,
which on hardware is the difference that matters most.

**SPACE — this is what the change actually cost.** 130 bytes of CODE, but the
`TABLES` segment is page-aligned and only 42 bytes of page tail were left, so
the IMAGE grew a full **256 bytes** (engine.bin 31,650 → 31,906; MAIN headroom
1,102 → 911 B under the `$BFEF` linker ceiling — nothing reserves
`$BF00-$BFEF`, only the harness traps at `$BFF0+`). That took the Standard
Delivery image 162 B OVER `diskii mksd`'s 45,056 B cap, so
`internal/delivery.Base` was **raised $0C00 → $0D00**, the lever
`TestDiskLedger`'s own failure message prescribes: SD spare back to **94 B**,
UI growth room 423 → **167 B**. There was no way to dodge it — even moving the
whole filter into the page-tail-free `TABLES` segment costs 130 image bytes
against 94 B of spare. The next ~126 engine bytes are free (they fit the new
page tail); the ~130 after that cost another page, and MARGIN 2 has one page
left before the answer is a different LOADER, not a smaller program.

## 2026-07-29 — ★★ THE DEVICE CONFIGURATION MEASURED: **+161 [+126, +199]** vs Sargon III, and the soft clock costs NOTHING once ponder is removed

504 games, paired, ponder removed from **both** sides — the configuration the
disk actually runs. `runs/noponder-paired/`, analysed by its own `analyze.py`.

**SPEND SYMMETRY FIRST** (this is what invalidated an earlier measurement, so
it is read before any Elo):

| arm | 8fish Gcyc | Sargon Gcyc | spend_ratio | adherence | ponder |
|---|---:|---:|---:|---:|---:|
| `off` (exact clock) | 367.0 | 385.1 | **0.9529** | 0.9551 | 0 |
| `soft` (**the device**) | 353.4 | 369.9 | **0.9554** | 0.9633 | 0 |

Delta **+0.25%** of compute. 8fish takes *less* than Sargon in both arms, so
neither arm bought its result with free cycles. Ponder confirmed zero on both
sides. Per-move re-derivation matches the harness accumulators to 0.03%.

**RESULT:**

| arm | games | W-L-D | score | Elo [95% CI] |
|---|---:|---|---:|---|
| `soft` — **the shipped device config** | 252 | 137-28-87 | 71.63% | **+161 [+126, +199]** |
| `off` — exact clock | 252 | 154-52-46 | 70.24% | +149 [+109, +193] |

**Clock cost (soft − off): +11.7 Elo ± 28.2, 95% CI [−43.5, +66.9], z = +0.42,
p = 0.709.** The soft clock costs nothing measurable. If anything the device
arm scored higher, not significantly.

**This resolves the −64 as ponder-specific.** The 2026-07-29 paired gauntzlet
found the soft arm 64 Elo behind *with both engines pondering*, where the soft
arm underpondered (ratio 0.8916 vs 0.9374) because the ponder window is
governed by the same estimate. Remove ponder from both sides and the gap
vanishes. Being careful about how much this settles: the old CI was
[−120, −8] and excluded zero, while this one is [−43.5, +66.9] and contains
it — so part of the −64 was likely noise as well as ponder. What is now solid
is that **the shipped configuration is not paying a clock penalty.**

**★ COMPARABILITY — do not put +161 next to +116.** Removing ponder weakens
BOTH engines, and Sargon relies on it more (at its normal levels it ponders on
the player's time). So the no-ponder gauntlet is a *different benchmark*, not
a better estimate of the same quantity. Quote it as: **in a no-ponder match
the device configuration scores +161 [+126, +199]**; the ponder-enabled
harness figure remains +116 [+75, +160]. It is the device-faithful number
because the disk never ponders — but it is not the old number re-measured.

**AUDIT — the cleanest Sargon run to date.** Both arms: **0** CrossCheckHistory
desyncs, **0** unreadable/illegal markers, **0** Hard-Mode/LEVEL-9 violations
(all 504 games verified), **0** quirk adjudications (down from 10), and **0**
games reaching the move-99 column-overflow zone (longest 185/189 plies).
Terminations are almost all real chess: 137/154 checkmate wins, 28/52
checkmate losses, 82/41 threefold, 2 insufficient material.

**Honest gap:** **8 of 504 games (3 soft, 5 off) have no TERMINATION line** and
are unclassified. They are counted in the W-L-D totals from the session
summaries, so the Elo is over all 252 per arm, but their endings are not
attributed. Not chased down; worth a look if anyone revisits this rig.

Note the shape difference that persists: the soft arm draws far more (87 vs
41) and loses far less (28 vs 52) — the same "converts won positions less
reliably" signature seen before, but here it nets positive rather than
negative.

## 2026-07-29 — ★ RESOLVED, and it was never a driver bug: the "uidrive vs iterate" divergence was the REFERENCE running a DIFFERENT ENGINE ($1F vs the shipped $5F)

Closes the OPEN BUG filed earlier today (entry below). **The invariant holds.**
`uidrive` and `engine.s`'s `$4000` entry produce the same move at every one of
**60 position x depth cells** swept. Nothing in `asm/m8.s`'s driver was wrong,
and nothing in it changed.

**Root cause.** `internal/ui`'s `refSearch` built its reference with
`chesstest.NewMachine`, whose FEATURES default is the **test** value `$1F`, and
then only ever overrode FEATURES2. It never set FEATURES. Since 2026-07-28
`asm/m8.s` ships the **gameplay** mask `$1F|FT_CKEXT = $5F`. So the gate was
comparing an engine WITH check extensions (the UI) against an engine WITHOUT
them (the reference) and reporting the difference as a divergence between two
DRIVERS. The Sicilian at depth 5 is simply a position where FT_CKEXT changes
the answer.

| driver / config | d1 | d2 | d3 | d4 | d5 | d6 |
|---|---|---|---|---|---|---|
| UI `uidrive`, FEATURES `$5F` (shipped) | — | f3d4 | f3d4 | f1b5 | **f3d4** | f3d4 |
| `$4000` `iterate`, FEATURES `$5F` | f3d4 | f3d4 | f3d4 | f1b5 | **f3d4** | f3d4 |
| `$4000` `iterate`, FEATURES `$1F` (what the gate used) | f3d4 | f3d4 | f3d4 | f3d4 | **f1b5** | f3d4 |

The two shipped-config rows agree everywhere. The old entry's "the reference
itself flip-flops, f1b5 only at depth 5" was a property of the **$1F** engine,
which is not the engine that ships.

**This is the same defect the 07-28 fix was written about, half-applied.** That
entry's own diagnosis reads: "internal/ui's reference searches go through
`chesstest.NewMachine`, whose TEST default is also `$1F`, so `TestEngineParity`
compared `$1F` against `$1F`." The `m8.s` side was raised to `$5F`; the
reference side was left at `$1F`. A gate that had been passing for the wrong
reason (both sides wrong, equally) started failing for the wrong reason (one
side fixed). `shipconfig_test.go`'s header still said "every gate in this
package compares the UI against a reference built by `chesstest.NewMachine`,
whose FEATURES default is 0x1F" — accurate, and the bug. The shipped mask now
has exactly ONE definition in the package (`engine_test.go shippedConfig`) and
`shipconfig_test.go` consumes it, so the two cannot describe different engines
again.

**The driver state was audited anyway**, since a features explanation that
happened to be sufficient is not the same as the drivers being equivalent.
Everything `engine.s`'s ENTRY establishes before `iterate`, `uidrive`
establishes too: `PSP0/1`, `ABORT`, `NODECNT`, `evalinit`, `BESTFROM=NOSQ`, the
`ccsite` SMC operand and the `CLOCK_TRAP` PHASE prime under `FT2_SOFTCLK`,
`MAXCAP`, `ABORTL = 2*BUDGET`, and the `adaptaborthi` override under
`FT2_ADAPT`. Differences found, all benign:

* `uidrive` ADDITIONALLY zeroes `PLY` and `HVALID` before `evalinit`. `engine.s`
  does not, because its machine is fresh; the UI's is not. `uidrive` is the
  more careful of the two, and `iterate` re-zeroes both regardless.
* `SCORE` is stale on the UI side (the previous move's) where the reference's
  is 0, so `iterate`'s `SCORE -> PREVSC0/1` snapshot differs at iteration 1.
  Never consumed: abort recovery is gated on `PREVFROM != NOSQ` and the driver
  sets `BESTFROM = NOSQ` before the loop, and `adaptmaybe` returns early at
  `CURDEPTH < 2`.
* Fixed-depth levels poke `BUDGET = 0`, and `search.s checkclock` is a no-op
  when the budget is zero — so `ABORTL`, `CEILMAX`, `UNSTCEIL`, `MINSPEND` and
  the whole `FT2_ADAPT` cluster are unreachable in every cell measured here,
  and `uilimits` clears `FT2_ADAPT` for those levels in any case.
* The TT and its aux bank persist across moves on device. So does the UCI
  bridge's (it copies `Mem.Aux` forward between moves), so this is not a
  driver difference — but it does mean the gate is only well-posed on the
  session's FIRST search, which is exactly what `twoPlayerPoke` exists to
  guarantee.

**Scope sweep — 12 lines x depths 2-6, dither off, fixed depth, cold TT.**
Positions reached by typing into the UI (start, 1.e4, Ruy Lopez, Sicilian,
French, QGD, King's Indian, Italian, Scandinavian, Caro-Kann, plus an 18-ply
Ruy middlegame and a 16-ply QGA queenless middlegame); depth 6 reached by
poking `LVDEPTH+3`, which is what makes the sweep go past the shipped level
cap of 5.

| reference config | cells agreeing with `uidrive` |
|---|---|
| FEATURES `$5F` (shipped) | **60 / 60** |
| FEATURES `$1F` (the old gate's) | 55 / 60 |

The five `$1F` disagreements are Ruy Lopez d5, Sicilian d4, Sicilian d5, QGD d5
and Italian d6 — i.e. the artefact is **not** depth-5-specific and **not**
one-position; it is exactly "positions where check extensions matter", which is
what FT_CKEXT is for. The Sicilian/d5 cell was merely the one the case table
happened to contain.

**Fix (tests only; ZERO engine or driver bytes).**
`internal/ui/engine_test.go` grows a `shippedConfig(defs)` that spells the
shipped configuration out **from `defs.inc`** — `$1F|FT_CKEXT` and
`FT2_GENDEFER|FT2_SOFTCLK` — and `refSearch` installs both bytes. It is
constructed, never peeked from the image: a reference derived from the thing it
checks agrees by definition, which is how both of this week's bad gates
happened. `TestEngineParity` then separately requires the BOOTED image to equal
that construction, and fails with "this gate is comparing two different ENGINES
and can say nothing about the two DRIVERS" if either side ever moves again.
Mutation-checked: forcing `shippedConfig` back to `$1F` fires that fatal.

The Sicilian depth-5 subtest is **un-skipped**; all four cases pass, and the
`known`/`t.Skip` machinery is gone from the table so a future divergence cannot
be parked there quietly.

`m8.bin` 4441 B — **unchanged**. UI growth room stays 423 B, SD spare stays
94 B. `engine.bin` byte-identical, so `TestMicroAB` fingerprints do not move.

## 2026-07-29 — the opening book's 32-bit random was really 24: `uibookrnd` had GF(2) rank 24, fixed for ZERO bytes

`asm/m8.s uibookrnd` turns the keystroke collector into the book's 32-bit
weighted-pick random. Its last byte read `ENTROPY`'s **low** half:

    BOOKRND+3 = ENTROPY0 EOR ENTCNT1 = BOOKRND+0 EOR BOOKRND+2

so `ENTROPY`'s HIGH byte reached `BOOKRND` **nowhere at all**. The routine is a
fixed pattern of EORs, hence affine over GF(2), hence entirely described by a
32x32 bit matrix — and that matrix had **rank 24**. The book drew from 2^24
values however good the collector got, and nothing downstream could tell: it
still picks a legal move, and the loss shows only as openings recurring more
often than they should.

Reading `ENTROPY+1` instead removes it. Both operands are absolute, so the fix
is **0 bytes** (`m8.bin` 4441 → 4441); it is only possible because `ENTROPY`
widened to 16 bits earlier today. The map is now a bijection:

    B0 = E0          ->  E0 = B0
    B1 = C0 EOR E0   ->  C0 = B1 EOR B0
    B2 = C1          ->  C1 = B2
    B3 = E1 EOR C1   ->  E1 = B3 EOR B2

**Gated**, because an affine map's rank is exactly the thing no behavioural
test would ever notice: `internal/ui TestBookRandomIsFullWidth` runs the real
`uibookrnd` under the emulator on 33 inputs (0 and the 32 unit vectors),
recovers the matrix, and requires rank 32. Mutation-checked: restoring the old
operand reports rank 24.

## 2026-07-29 — ~~★ OPEN BUG: the UI's ID driver and the engine's `iterate` DISAGREE at depth 5~~ — SUPERSEDED, see the entry above

> **★ THIS ENTRY'S HEADLINE CONCLUSION IS WRONG AND IS KEPT ONLY AS A RECORD.**
> There is no divergence between `uidrive` and `iterate`. The reference search
> this entry compares against was running FEATURES `$1F`, not the shipped
> `$5F`, so the two sides were different ENGINES, not different drivers. What
> this entry gets right and is worth keeping is the SEED diagnosis below —
> `SEED` is `evseed`'s live PRNG state, the dither must be off on both sides,
> and the gate really was passing by luck before that was fixed.

Found while fixing the entropy collector (entry below); **not caused by it**.

`TestEngineParity` is the gate that makes the on-device UI safe to ship: a move
chosen by `uidrive` (the UI's own ID driver, docs/ui-design.md §7) must be the
move the engine's normal $4000 entry produces for the same position, depth and
features. It ran the UI **dithered** and then handed the reference search
`u.Peek(SEED)` — but `SEED` is not the seed, it is `eval.s evseed`'s LIVE PRNG
STATE, advanced `seed = seed*3+29` at every dithered leaf. So the test read the
END of the UI's dither stream and started the reference from there: two
DIFFERENT streams, agreeing only because 0-3cp of noise rarely flips a best
move. The tell was in its own log — it once printed `SEED 0x00` for a move,
and `entseed` cannot install a zero seed.

Handing the reference the TRUE installed seed does not rescue it either: at the
Ruy Lopez position the two drivers disagree for **33 of 40 seeds**, because the
dither's noise depends on the POSITION IN THE EVAL-CALL SEQUENCE and the two
drivers' sequences do not align. The comparison is only well-posed with the
dither OFF, which the test now does (it clears `SEED` the instant `entseed`
installs it; `evseed`'s `lda SEED / beq evdone` then costs nothing and never
writes it back).

With that — a genuine, strictly stronger tree-identity gate — three of the four
positions pass and **the Sicilian at depth 5 does not**:

| driver | move |
|---|---|
| UI `uidrive`, depth 5, undithered | **f3d4** (`ABORT=0 CURDEPTH=5 BUDGET=0`) |
| $4000 `iterate`, depth 5, undithered | **f1b5** |
| $4000 `iterate`, depths 1, 2, 3, 4, 6 | f3d4 |

The UI completed a clean, unbudgeted depth-5 iterative deepening in exactly the
reference's configuration, and chose a different move. It is a knife-edge
position (+0.12) where the reference itself flip-flops — f1b5 only at depth 5 —
so the practical Elo cost is likely nil, but the INVARIANT the gate exists to
enforce does not hold, and that is worth knowing before the disk ships.

The subtest is `t.Skip`ped with the full explanation inline in the case table,
so it stays visible; the other three are now a real gate rather than a lucky
one. (Both are now moot: the subtest is un-skipped and passing — see above.)
**Fixing `uidrive` is not attempted here** — it is UI/engine driver work
with its own risk, and this task was the entropy collector.

## 2026-07-29 — the entropy collector's fold CYCLED on quantized input (longest possible orbit: 16). Fixed for +13 B. **Blast radius on past measurements: NONE**, and that is measured, not assumed.

`ucibridge.TestDitherEntropySeeds` failed 17 times in 400 runs under gauntlet
load — never idle, never under synthetic CPU load. The seeds did not merely
lose entropy, they REPEATED, with the distinct count clustering at exactly 32.

**Mechanism — proven directly, no load required.** The collector's accumulator
was ONE byte and its fold was `ENTROPY = ROL(ENTROPY) EOR x`. That is affine
over GF(2), so with a CONSTANT input the fold is a deterministic affine map on
256 states and its orbits are not merely finite, they are tiny. Exhaustive walk
of all 256 inputs x all 256 states:

| fold | longest possible orbit | orbits <= 16 |
|---|---|---|
| **old** `ROL8 + EOR` | **16** | all of them |
| new `LFSR16 + EOR` | 65535 | none |

The captured failing sequence is reproduced EXACTLY by a constant arrival of 80
poll iterations (1.254 ms): the orbit is 32 long and contains 28 distinct
seeds, and its 24-value tail matches the reporter's capture byte for byte. The
"period 32" reading needed the caveat it was given — the *state* period is 32
while the arrival's own period is 16, and the run only enters the orbit once
the machine settles into the quantized regime, which is why a whole-sequence
periodicity test refutes it.

The cliff is razor-sharp: ±1 poll iteration of jitter (15.7 **microseconds**)
restores full diversity even to the old fold. The defect needs the wait
quantized to a 15.7 us window, held indefinitely.

**Fix (asm/entropy.inc, driver-only).** `ENTROPY` widens to 16 bits ($0216;
$0212 freed, and $0213-$0215 are the GDVERIFY debug build's) and `entfold`
becomes one step of a maximal-period 16-bit Galois LFSR — polynomial $B400 =
x^16+x^14+x^13+x^11+1, primitive — with the byte EORed into the low half.
`entkey` also folds the counter's HIGH byte, so a repeating input now needs the
whole 16-bit counter to repeat (a 1.03 s grid) instead of just its low byte (a
4.01 ms grid). **+13 B** (m8.bin 4428 → 4441; UI growth room 436 → 423 B; SD
spare unchanged at 94 B) and +1 B of RAM.

Why it cannot cycle: exhaustively, over all 256 constant inputs x all 65536
states, EVERY orbit is either the lone fixed point of that input or the full
65535-cycle — nothing in between (`entropy.TestFoldCannotCycleShort`). On the
dangerous quantization class (every arrival delta that is a multiple of 8, x 64
start states) the rate of dropping below 40 distinct seeds per 60 moves:

| | old fold | LFSR16 only | LFSR16 + fold both counter bytes |
|---|---|---|---|
| collapse rate | **100%** | 0.025% | **0.00019%** |

Rejected on measurement: a 35-byte explicit fixed-point guard (clean worst case
of 65535 but it moved the collapse rate by nothing — it only kills period-1);
24- and 32-bit LFSRs (no improvement); and every non-linear perturbation tried
(`+1` before or after the step, ADC instead of EOR) — all of them made things
WORSE, creating 1500-2000 short cycles where the affine LFSR has 256.

**★ BLAST RADIUS: NONE.** The harness feeds the collector the elapsed time
between `position` commands — an interval that ALWAYS contains a full emulated
8fish search, so it can never be small or quantized. Measured on a live
symmatch (new `DITHER-GAME-SUMMARY` audit line), against a degenerate regime
that needs arrivals constant to ONE iteration:

| game | moves | distinct seeds | ideal expectation | arrival range (poll iterations) |
|---|---|---|---|---|
| 1 | 50 | 43 | 45.5 | 465 .. 230,253 (7.3 ms .. 3.6 s) |
| 2 | 56 | 52 | 50.2 | 4,126 .. 1,345,907 (65 ms .. 21 s) |
| 3 | 72 | 61 | 62.3 | 2,301 .. 1,069,634 (36 ms .. 17 s) |

Three to five orders of magnitude of spread per game, and the distinct counts
sit on the ideal expectation. Replaying the OLD fold over that measured arrival
distribution gives **45.5 distinct seeds per 50 moves — exactly the ideal
uniform-RNG expectation**. The old collector was never in trouble here.

And most of the corpus never touched the collector at all: `internal/sprt`
(every SPRT Elo number) draws its SEED from a host PCG, the 504-game paired
softclock gauntlet ran `-dither prng` deliberately, and every cutechess PGN in
`tools/pgn/` predates the collector (2026-07-25). Only `cmd/sargon-symmatch`
and `cmd/uci` defaulted to it — and for those, the measurement above says the
input was never in the failing regime. **No past result needs restating.**

Floor NOT loosened: `TestDitherEntropySeeds` keeps `>= 40`. It was correctly
reporting a real defect in shipped code, and the fix's worst case over EVERY
constant arrival delta is 45 distinct in 60 (`entropy.TestQuantizedArrivals`,
which pins the whole thing deterministically so the sleep-based test is no
longer the only guard).

**Unfixed, reported not changed:** `asm/m8.s uibookrnd` builds the book's
32-bit weighted-pick random as `BOOKRND+3 = BOOKRND+0 EOR BOOKRND+2`, a linear
dependency that caps it at 24 bits of entropy. Now that `ENTROPY` is 16 bits it
is fixable at ZERO byte cost by using `ENTROPY+1` for the fourth byte.

## 2026-07-29 — ★★ IN THE DISK'S OWN TIME MANAGEMENT THE SHIPPED CLOCK COSTS **0.4% OF COMPUTE AND NO DEPTH**. The −64's proposed mechanism is falsified, and half the gauntlet's 8fish compute is a feature the disk does not have.

Follow-up to the entry below, which measured the shipped clock at −64 [−120, −8]
Elo and named the ponder-spend gap (0.9374 harness vs 0.8916 soft) as the lead,
with "the margin table reads high at 29 s" as the mechanism.

Four instruments say otherwise. The estimate does not read high at 29 s (it reads
5-6% LOW at 4 s and is exact at 29 s). The ponder gap is real but worth ≈2 Elo,
and the **shipped disk does not ponder at all**. And measured for the first time
in the DISK's actual time-management configuration — on-device `FT2_ADAPT`, no
bank, LEVEL 8 — the soft clock delivers **1.0033 [0.982, 1.030]** and, on a
disjoint replicate, **0.9895 [0.962, 1.019]** of what an exact clock delivers on
identical positions, completing slightly MORE depth in both runs and playing the
same move 98% of the time.

**No engine, margin-table or UI code was changed.** There is nothing there to fix.
What changed is the acceptance gate, which certified an octave the product does
not use; a new paired instrument ~20x sharper than the one the margin table has
been fitted against; and a header on `cmd/sargon-symmatch` saying what its Elo
does and does not describe.

### 0. ★★ THE SHIPPED CONFIGURATION, MEASURED — and it is clean

Everything below was chased in the FLAT search configuration, because that is
what the gauntlet and the acceptance gate both run. The disk does not: `asm/m8.s
uilimits` turns **FT2_ADAPT** on for every timed level and derives CEILMAX = 4x,
UNSTCEIL = 3x, MINSPEND = base/4 straight off the level's budget, with NO bank.
That configuration had never been measured against an exact clock.

`sprt.TestPairedClockProbe` with `PROBE_ADAPT=1`, LEVEL 8 (30 s/move), 1177
position-paired searches:

| config | openings | n | soft adh | exact adh | **paired soft/exact** | bootstrap 95% | completed depth soft / exact | same move |
|---|---|---:|---:|---:|---:|---|---:|---:|
| **FT2_ADAPT, 30 s (the disk)** | 1-8 | 1177 | 1.1313 | 1.1276 | **1.0033** | **[0.982, 1.030]** | 4.798 / 4.793 | 98.1% |
| **FT2_ADAPT, 30 s (replicate)** | 9-16 | 1107 | 1.1624 | 1.1747 | **0.9895** | **[0.962, 1.019]** | 4.993 / 4.971 | 97.7% |
| flat, 29.4 s (the gauntlet) | 1-8 | 671 | 0.9202 | 0.9397 | 0.9792 | [0.945, 1.017] | 4.744 / 4.756 | 97.5% |
| flat, 30 s (replicate, disjoint openings) | 9-16 | 879 | 0.8534 | 0.8746 | 0.9758 | [0.951, 1.001] | 4.356 / 4.345 | 97.4% |

**Under the disk's own time management the estimated clock and a real clock are
indistinguishable, on two disjoint opening sets.** Per-ply p10 = p50 = p90 =
1.000 in both: the overwhelming majority of positions cost the SAME true cycles
either way, and the soft arm completes slightly MORE depth in both runs (4.798 vs
4.793, and 4.993 vs 4.971).

The FLAT deficit is real and replicates on disjoint openings — 0.9792 and 0.9758,
so call it 2.3% — but it does not cost DEPTH in either run (4.744 vs 4.756, and
4.356 vs 4.345, the second one in the soft arm's favour). The mechanism is easy
to see once measured: FT2_ADAPT's movable ceiling, and the ABORTL = 2 x CEILMAX
that comes with it, give the search room to absorb a noisy clock, so a stop
decision landing a few percent early or late is re-absorbed instead of truncating
an iteration. A flat search has no such slack. **The 2.3% therefore lives in the
gauntlet's configuration, not the product's.**

### 1. FALSIFIED — the estimate does not read high at ~29 s. It reads *low* at 4 s.

The hypothesis was that the cost table, fitted at 4 s and 15 s, over-prices nodes
in a 29 s search (deeper trees take more nearly-free TT-cutoff nodes), so the
engine believes its window is up before it is. `cmd/softclkdiag` measures exactly
this under game conditions — warm TT, real per-move allocation, the shipped
table:

| budget | in-game estimate/truth | per-move bias | per-move RMS | adherence | n moves |
|---|---:|---:|---:|---:|---:|
| 4 000 ms (margin 127%) | **0.9376** | −3.9% | 29.0% | 0.9550 | 830 |
| 15 000 ms (margin 100%) | **0.9500** | +1.6% | 35.6% | 0.8947 | 650 |
| 29 397 ms (margin 100%) | **0.9929** | +3.4% | 26.0% | 0.8592 | 992 |

The estimate reads 5-6% **LOW** at the short octaves — which is what the 127%
margin exists to counteract, since reading low means the engine keeps going and
overruns — and is essentially **exact** at 29 s. The 100% bucket is not an
unmeasured extrapolation that happens to be wrong; it is the octave where the
raw table needs no correction at all. The variance half of the hypothesis dies
with it: relative RMS does not grow with budget (29.0% at 4 s, 26.0% at 29 s).

### 2. ★ The new instrument: position-paired, and it is what should have been used

`sprt.TestPairedClockProbe`. At every ply of a self-play game the SAME machine
state — same position, same halfmove clock, same carried TT, same poked limits —
is searched TWICE, exact clock and FT2_SOFTCLK, and both TRUE cycle costs,
completed depths and chosen moves are recorded. The exact run decides the move,
so the two arms walk an identical trajectory and game composition cancels.

| budget | margin | n | soft adh | exact adh | **paired soft/exact** | bootstrap 95% | mean completed depth soft / exact | same move |
|---|---:|---:|---:|---:|---:|---|---:|---:|
| 4 000 ms | 127% | 853 | 0.9408 | 0.9161 | **1.0269** | [0.996, 1.061] | 2.517 / 2.533 | 95.2% |
| 29 397 ms | 100% | 671 | 0.9202 | 0.9397 | **0.9792** | [0.945, 1.017] | 4.744 / 4.756 | 97.5% |
| 30 000 ms (disjoint openings) | 100% | 879 | 0.8534 | 0.8746 | **0.9758** | [0.951, 1.001] | 4.356 / 4.345 | 97.4% |

**In the FLAT configuration the soft clock delivers ~97.7% of the exact clock's
compute at the gauntlet's budget (replicated on disjoint openings), completes the
same depth to within 0.012 iterations — in the replicate, slightly MORE — and
plays the same move 97.5% of the time.** That is an upper bound on the whole
effect even before §0 removes it: even charging every one of the 2.5% changed
moves a full 30 cp costs under 1 cp per move.

Why this matters as a method point: the unpaired gate divides two independent
runs' totals, so its ratio carries the entire variance of game composition. At 3
pairs and 29 s the two arms produced 654 and 444 moves and a soft/exact of 1.01
— a number with no resolving power, which disagreed by 10 points with a 6-pair
run. The paired probe gets a ±3-point interval for the same wall time, and it
reports depth and move agreement, which the gate cannot.

**★ And it has a trap that cost a false positive.** The first version carried ONE
transposition table forward across plies, so every search started on the table
its immediate predecessor built. That inflated BOTH arms' adherence (exact 1.26
at 4 s against the 0.92 the same build shows in real games), because a one-ply-old
table already covers the tree the search is about to build and idloop's "the next
iteration costs 2x the last" gate stops holding. It moved the paired ratio by 11
points — 0.913 vs 1.027 at 4 s — and would have reported a soft-clock deficit at
EVERY octave that does not exist. Two auxes alternating by side, which is what
`sprt.Run` does, and (see §3) what the disk does.

### 3. ★★ The disk does not ponder — so 49% of the gauntlet's 8fish compute is not in the product

`asm/m8.s`'s main loop is `uisync` / `uipaint` / `m8engine` on our turn, and on
the human's turn `uiread` → `entkey`, a **blocking** keyboard poll. There is no
ponder path anywhere in `asm/`, and `docs/plan.md` M8 says so on purpose:
"Pondering stays out of scope even here (it would invalidate the calibration
story)."

So a ponder-specific safety margin — the leading candidate fix — would be dead
code on the artifact. More importantly, decomposing the 504-game run's own
published totals (8fish = own + ponder, Sargon = own + think; ponder =
r × asked, asked ≈ think):

| arm | 8fish own | 8fish ponder | Sargon think | ponder share of 8fish compute |
|---|---:|---:|---:|---:|
| harness clock | ≈ 332 G | ≈ 316 G | ≈ 337 G | **48.7%** |
| soft clock | ≈ 423 G | ≈ 401 G | ≈ 450 G | **48.7%** |

(Derived, not separately measured: the entry below publishes the two per-side
totals and the two ponder ratios, and the four components are then determined —
`8fish_total − sargon_total = ponder − think` with `ponder = r × think`, taking
`asked ≈ think` since Sargon's instant book replies are ~1.3M cycles against
~35M thinks. Both rows re-derive the published totals to within 0.1%. A 6-game
diagnostic pair rerun today reproduces the ingredients: per-game ponder ratios
0.928/0.992/0.861/0.866 on the exact clock and 0.826/0.866/0.963/0.916 on the
soft one — the same gap, and a vivid demonstration of why 252 games were needed
to see it.)

**Half of 8fish's compute in both arms is a component the shipped disk does not
have.** Neither +116 nor +51 describes the artifact a user boots.

And the ponder gap, closed, is worth what the entry below already implies:
lifting the soft arm's ponder ratio from 0.8916 to the harness arm's 0.9374 adds
≈21 G to 824.3 G, i.e. **+2.5% total compute**, moving spend_ratio 0.9442 →
0.968 (the harness arm's 0.9684). At ~60-70 Elo per doubling that is **≈ +2
Elo**. The entire spend asymmetry between the arms IS the ponder gap, and the
entire ponder gap is ≈2 Elo.

### 4. Why own-move adherence being 0.9982 was never evidence of anything

`cmd/sargon-symmatch` runs the bridge with `Banked=false, Adaptive=false` and
keeps its OWN host-side `chesstest.BankedClock`, settled on TRUE cycles
(`clock.Settle(sr.cycles)`). Every under-spend is therefore refunded into the
next move's allocation and own-move total compute telescopes to income × moves
BY CONSTRUCTION, whatever the clock does. Measured on a 6-game diagnostic pair:
own-move adherence 0.9990 (exact) and 1.0034 (soft). The 0.9982 soft/exact in
the entry below is not evidence that own moves are unaffected; it is evidence
that this instrument cannot see them. The ponder path has no bank, which is the
only reason the estimator's signature showed up there.

That is also a third shipped/tested gap in the same family: the disk runs
`FT2_ADAPT` with **no bank at all** (`asm/m8.s uilimits` derives CEILMAX = 4x,
UNSTCEIL = 3x, MINSPEND = base/4 straight from the level's budget), while the
gauntlet runs flat searches fed by a host bank. `sprt.Run`'s adaptive mode cannot
model it either — its `pokeAlloc` caps the hard ceiling at `income + bank`, which
with a fresh bank is `income`, i.e. no headroom. Filed, not fixed.

### 5. What DID change: the acceptance gate now covers the octave the product uses

`sprt.TestSoftClockAdherence` ran at **4000 ms only**. `asm/m8.s` ships levels
5-9 at 4 s, 8 s, 15 s, 30 s and 60 s, and every Sargon number is measured at 30M
cycles ≈ 29 s. So the gate certified margin-table octave 13 while the product
lived in octaves 16-17 — the flat, never-measured 100% tail. Same shape as the
harness clock the hardware lacks, the pool that was not game conditions, and the
disk that shipped `FEATURES=$1F`.

The gate is now a loop over `{4000, 30000}` ms, applying all three assertions
(forfeit ≤ 1.00, a collapse alarm, soft/exact ∈ [0.90, 1.10]) at each.
`TestPairedClockProbe` runs the same two octaves and asserts the paired ratio in
the same band.

| budget | margin | SOFT adherence | EXACT adherence | soft/exact | (1) ≤1.00 | (3) ∈[0.90,1.10] |
|---|---:|---:|---:|---:|---|---|
| 4 000 ms | 127% | **0.9447** (1212 mv) | 0.9107 (1138 mv) | 1.0373 | PASS | PASS |
| 30 000 ms | 100% | **0.8496** (1254 mv) | 0.8964 (882 mv) | 0.9478 | PASS | PASS |

**Own-move adherence stays ≤ 1.0 at both octaves** — 0.9447 and 0.8496 — which is
the deliverable's hard constraint, and nothing was changed that could move it.

★ **Extending the gate immediately caught a defect in the gate itself.** Assertion
(2), "not so conservative it throws the budget away", was a bare
`softAdh < 0.85` — a constant calibrated when the gate ran at 4000 ms only, and
the 30 s soft arm trips it at 0.8496. It is the wrong test at that octave:

- The FLAT predictive gate leaves more of a LONG budget unspent by design, so the
  EXACT clock's own adherence falls too, 0.9107 → 0.8964. A floor of 0.85 sits
  5.5% under its own control.
- That is inside this instrument's composition noise, because the arms play
  different games: 1254 moves vs 882, a 42% difference, against an adherence that
  falls steeply with game ply (softclkdiag at 29 s: 0.96 at plies 0-19, 0.75 at
  plies 120-139).
- The paired probe, same build and budget with zero composition noise, puts
  soft/exact at 0.9792 [0.945, 1.017] — i.e. the estimator did not do what 0.85
  accused it of.

So (2) is now documented as a COLLAPSE ALARM with its constant moved clear of
every measured exact-clock adherence plus that noise (0.75), and the
estimator-vs-control judgement is left to (3), which has the control in the same
run. Loosening a gate needs a reason; the reason is that at 30 s it was measuring
which arm played longer games.

### 6. The Elo re-measurement, and why it was not run

There is no code change to measure. And the instrument could not resolve one:
the paired gauntlet's difference came in at ±56 Elo over 252 games per arm, so
resolving the ≈2 Elo the only real mechanism is worth would need (56/2)² ≈ 800x
the games — roughly 200,000 games per arm. Re-running it would have produced a
number with the same ±56 and no information.

**On the −64 itself.** It stands as measured, and its SIGN may well be right, but
nothing found here supports its magnitude. The compute difference between the
arms is 2.4 points (≈2 Elo, all of it ponder). On identical positions in the
gauntlet's own flat configuration the soft clock changes 2.5% of moves and loses
0.012 completed iterations; in the disk's adaptive configuration it changes ~2%
of moves, delivers 99.6% of the compute across two disjoint opening sets, and
completes MORE depth in both. A z of −2.23 with an interval of [−120, −8] is
exactly the shape that produces an inflated point estimate, and the honest
reading is that the truth sits near the top of that interval.

**What would still be worth chasing**, if the −64 is real at all: it is not a
SPEND effect, so it can only be an allocation-QUALITY one — the estimator's
per-move relative RMS is 26-36%, so a flat-configuration search's effective
budget is randomly scaled move to move, uncorrelated with how hard the position
is. That is a real cost and it is the one thing here that is not bounded by a
measurement. It would be tested with a soft-vs-exact SELF-PLAY SPRT at 30 s
(no Sargon, no ponder), which is the same instrument the 2026-07-27 entry used at
4 s to get −23 ± 27. §0 says the adaptive ceiling should absorb most of it.

**If a fix ever is warranted**, the shape is known and so is its price. The two
flat-configuration measurements at octave 16 (paired 0.9792 and 0.9758, unpaired 0.9478)
would put the octave's margin entry near 96-98% rather than 100%. That cannot be
expressed in today's `asm/m8.s KTAB`, which stores K = 25600/margin for a
`(BUDGET * K) >> 8` multiply: any margin below 100% needs K > 255. The 6502-side
change is to rescale the table to `>> 7` (K = 12800/margin, so 127% -> 101,
113% -> 113, 100% -> 128, 96% -> 133), which is **one extra 32-bit shift after
the existing multiply loop: 4 zero-page ASL/ROL, +8 bytes**, no division, and
inside the 1% slack `internal/ui TestSoftClockLimits` already allows between the
device rule and the host rule. It is NOT being taken: the better instrument's
interval contains 1.0, the configuration that ships measures 1.0033, and this
project's rule is that margin entries are measured anchors, not extrapolations
fitted to one run.

## 2026-07-29 — ★★ THE SHIPPED CLOCK COSTS **−64 ± 57 Elo**: the disk beats Sargon III by **+51**, not **+116**. Paired control, 504 games.

Every Sargon number in this log — the headline **+110 over 600 games** (pool
+89, standard-start +132) — was measured through `cmd/sargon-symmatch`, whose
`ucibridge.Bridge` runs `FEATURES = $5F, FEATURES2 = FT2_GENDEFER`. The soft
clock is OFF there on purpose: the harness owns a real cycle counter.

**The device also sets `FT2_SOFTCLK`** (`asm/m8.s:250`), because an Apple IIe
has no readable clock — a difference `TestShippedFeatureConfig` already
documents as deliberate. So the artifact a user boots had **never been played
against Sargon III at all**. This is the same "test what you ship" gap that
produced the harness clock the hardware lacks, the pool that was not game
conditions, the `-D HARNESSKBD` build, and the disk that shipped `FEATURES=$1F`.

`cmd/sargon-symmatch` now takes **`-softclock`**. One bool drives both the
feature bit and the `$BFF4` read-trap disable (`ucibridge.runEngine` derives
them together, the same single-source-of-truth rule `internal/sprt` applies in
the other direction), so the two cannot disagree. With it set the harness config
is byte-identical to the disk: `$5F` / `FT2_GENDEFER|FT2_SOFTCLK`.

### ★ 1. SPEND SYMMETRY FIRST — the mirror held; there is no free compute

`sargon-symmatch` is symmetric BY CONSTRUCTION only if each side's two
components mirror the other's. An estimated clock can break that, and did once
before: "+29 ± 26 Elo" three days ago was really a 26% overrun. So this is
checked before the result, not after.

| arm | 8fish Gcyc | Sargon Gcyc | spend_ratio | own-move adherence | ponder |
|---|---|---|---|---|---|
| harness clock | 648.5 | 669.6 | 0.9684 | 0.9871 | 0.9374 |
| **soft clock (shipped)** | 824.3 | 873.1 | **0.9442** | 0.9853 | 0.8916 |

**8fish takes LESS compute than Sargon in BOTH arms**, and slightly less still
under the soft clock (−2.4 points). The estimator is mildly self-handicapping,
not overrunning — the opposite of the failure that invalidated the earlier
measurement, and the Elo below is therefore interpretable.

Two further checks, both clean:

- **Own-move adherence is IDENTICAL** (soft/exact = 0.9982, versus the
  `sprt.TestSoftClockAdherence` acceptance band of [0.90, 1.10]). The entire
  gap is **ponder** spend (0.8916 vs 0.9374). That is the one component the
  harness does not mirror: 8fish is merely *asked* to ponder for
  `reply.ThinkCycles` and under `FT2_SOFTCLK` decides for itself when the
  window is up. Own-move spend is mirrored to Sargon exactly, and the
  `sargon_ponder_window == think` assertion held on all ~25,700 logged moves.
- Totals re-derived independently from the per-move log agree with the
  harness's own accumulators to **0.03%**.

A 2.4-point compute deficit is worth ≈ −2 Elo. It does not explain what follows.

### ★ 2. The paired result — same gauntlet twice, only the clock changed

Identical openings (`-standard-start`, both engines on their own books),
identical seeds (`-dither prng`, seeds 101-106), identical budget (B = 30M
cycles), identical colour alternation. 6 shards × 42 games per arm.

| arm | record | score | Elo | 95% CI |
|---|---|---|---|---|
| harness clock (what this log measured) | 150−69−33 | 66.07% | **+116** | [+75, +160] |
| **soft clock (what the disk runs)** | 118−81−53 | 57.34% | **+51** | [+13, +91] |
| **paired difference** | | −8.73% | **−64** | **[−120, −8]**, z = −2.23 |

**The instrument validates against history**: the harness arm's +116 [+75,
+160] contains the +132 the 2026-07-26 standard-start run recorded, on the same
mode, after 220 commits. This is a like-for-like control, not a comparison to
history — that comparison would have conflated deferred generation, the
cost-model recalibration, the book widening and the `FEATURES` fix.

**8fish still beats Sargon III on the shipped disk** (+51, CI excludes zero),
but by roughly HALF the margin this log claims. The claim "+110 Elo over Sargon
III" describes a configuration no user can boot.

Robust to how the 10 quirk games are scored: −64 as measured, −68 scoring all
quirks as 8fish wins, −66 dropping them. Between-seed spread (sd 0.101 on the
per-seed delta) matches pure sampling noise for n = 42 (predicted 0.095), so
there is no cluster structure to correct for; 4 of 6 seeds negative.

**Do not over-claim the magnitude.** 252 games per arm puts the difference
anywhere in [−120, −8]. The SIGN is established; the size is not.

### 3. Audit — the cleanest Sargon run yet, and one bounded artifact

| termination | harness arm | soft arm |
|---|---|---|
| checkmate, 8fish wins | 150 | 118 |
| checkmate, 8fish loses | 69 | 81 |
| threefold | 26 | 48 |
| insufficient material | 1 | 1 |
| quirk-adjudication (draw) | 6 | 4 |

- **`CrossCheckHistory` DESYNC: 0 in 504 games.** The mid-search move-list
  repaint class that desynced 17 of 300 games is GONE. No unreadable/illegal
  Sargon tokens, no illegal book moves, no engine errors.
- **Hard Mode + LEVEL 9 verified on every one of the 504 games** (0 warnings),
  so no game was silently played against a weaker Sargon.
- No resignations (the harness has no resign path), no stalemates, and **no
  move-cap draws** — nothing hit the 160-move adjudication.
- **All 10 quirk-adjudications are ONE bounded cause**, not a new failure
  class: games that pass move 99, where Sargon III's two-digit move-number
  column overflows to `:0` and it stops accepting injected moves. They are long
  drawish games adjudicated as draws — conservative, and the result is robust
  to all three treatments above. 2.4%/1.6%, down from the 5.7% of 2026-07-26.

The soft arm played **27% more moves** (14,377 vs 11,290) and drew by
threefold **nearly twice as often** (48 vs 26): the signature of an engine that
converts won positions less reliably, which is what a noisier clock buys.

**What this does NOT say.** It is not a verdict on `FT2_SOFTCLK` as a feature —
the device has no alternative. It says the NUMBER attached to the product was
measured on a configuration the product does not run, and the honest headline
for the disk is **+51 [+13, +91]**, pending a larger run.

Reproduce: `runs/softclock-paired/launch.sh` (12 shards) then
`runs/softclock-paired/analyze.py`.

## 2026-07-28 — `adaptmaybe`'s score-drop overflow: CONFIRMED and fixed, and it fires ZERO times in 530 games

The four-lens review's one SUSPECTED item (§5). `adaptmaybe` computed
`scoreDrop = PREVSC - SCORE` as a bare 16-bit subtract and branched on `bmi`,
with no `bvc`/`eor #$80` correction. Root scores span `[-MATE, MATEZONEHI*256)`
= `[-30000, +29695]` — `idok` reports a WINNING mate before `adaptmaybe` runs,
but a LOSING mate falls straight through — so the true difference reaches
±59695 and does not fit in 16 bits.

**CONFIRMED, both directions, on the real 6502 routine**
(`chesstest.TestAdaptiveMateScoreDrop`, which drives the shipped `adaptmaybe`
through the same stub `TestAdaptiveParity` uses and compares it against
`mirror.SearchTimed`'s policy, whose drop is full-width `int`):

| prev | score | true drop | wraps to | asm (before) | mirror |
|---|---|---|---|---|---|
| +3072 | −29990 | **+33062** | −32474 | ceiling 100 (**no panic**) | ceiling 400 |
| +20000 | −29990 | +49990 | −15546 | no panic | panic |
| +29695 | −30000 | +59695 | −5841 | no panic | panic |
| −29990 | +3072 | **−33062** | +32474 | ceiling 400 (**spurious panic**) | ceiling 100 |
| −29990 | +29695 | −59685 | +5851 | spurious panic | no panic |

The first direction is the bad one: "up three pawns last iteration, forced mate
against us this one" is the textbook panic case, and the wrap **suppressed** the
extension exactly where the engine most wants the extra time. This was a real
asm/mirror divergence, not a difference of intent, and `TestAdaptiveParity`
could never have caught it — it only ever feeds ±200cp scores.

Fixed with the house idiom (`bvc`/`eor #$80`, the same correction the TT-bound
and window compares in `search.s` use), plus a `bpl @dopanic` because the panic
test is a threshold (≥25) and not just a sign test: on overflow the true |drop|
is ≥ 32768, so a positive one is a panic outright. **+8 bytes.** `T1:T0` keep the
WRAPPED value for the `@easy` `|drop| ≤ 30` test, which is provably safe: a wrap
needs |true drop| > 32767 and the widest reachable is 59695, so the wrapped
magnitude is always ≥ 65536−59695 = 5841 and can never read as flat.

### How often it fires in real games: ZERO in 530 games / 51,314 searches

The wrap needs |PREVSC − SCORE| > 32767 with both in [−30000, +29695], which
forces one iteration to be a **mate score** and the adjacent one to be **≥ 2768cp**
(≈ three queens) on the far side. Legal; not game-like. Measured with a **canary
build** — byte-identical to the shipped one except that the overflow branch does
`lda #3 / sta EXIT_TRAP / brk`, so any firing surfaces as an `engine exit code 3`
error line naming the FEN. Positive control: the same build with `bvc`→`bvs`
errors on the very first search, so the instrument is live.

| control | games | searches | firings |
|---|---:|---:|---:|
| 4000 ms/move, `-pergame -adaptA -adaptB` | 500 | 48,310 | **0** |
| 15000 ms/move, same | 30 | 3,004 | **0** |

### Spend first, then Elo

A/B is the fixed binary vs a **size-identical control** carrying the OLD wrapping
behaviour in exactly the same bytes and the same cycles on the non-overflow path
(`bvc` + 6 bytes of pad), so the two differ in NOTHING but the overflow decision
— no layout shift, no cycle shift, no bank drift. `go run ./cmd/sprt`, never a
cached binary.

    -a 0x5f -b 0x5f -a2 0x30 -b2 0x30 -pergame -adaptA -adaptB -budget 4000 -pairs 250
    A_total = 96,421,363,809 (24,155 moves, adherence 0.9784)
    B_total = 96,421,363,809 (24,155 moves, adherence 0.9784)
    equal-total-spend A/B = 1.0000 (0.00%)   <- EXACT, to the cycle
    +160 =180 -160   score 50.0%   elo -0 +/- 24   llr(0,10) -0.32

Spend is not merely close, it is **bit-identical**, and so is the result table:
the paired design means two functionally identical engines produce exactly
mirrored games, so `A_total == B_total` to the cycle is the strongest possible
statement that the panic decision never once differed over 500 games. The Elo of
−0 ± 24 carries no information beyond that; it is quoted only to show the run
was real. This is the expected outcome given zero firings — **the fix is a
correctness change with no measurable behavioural consequence at either device
level.** It is kept anyway: the standing rule is that the code does what we
intend and the tuning is then a real choice, and this one costs 8 bytes, is now
pinned by a test, and removes an asm/mirror divergence.

### The one real side effect: +8 bytes moved `LCCODE_LOAD`, worth +8 cycles/search

`TestMicroAB` is **fingerprint-identical** — all 18 searches match on
search/make/eval/attacked/ttprobe/generate entry counts, score and move (the
default config has FT2_ADAPT off, so this is the tree-identity check). Every
search costs exactly **+8 cycles**, all of it in the one-time LC install loop:
`LCCODE_LOAD` moved `$BB59` → `$BB61`, and `lda (ZPTR),y` over 256 iterations
pays the indexed page-crossing penalty on `lo` of them — 89 before, 97 after.
Grand total 3,819,284,672 → 3,819,284,816 (+144 = 18 × 8). Under FT2_SOFTCLK
the engine reads its own node-based estimate, so those 8 cycles are invisible to
every on-device decision.

Measured for the record against the **actual pre-fix binary** (not the control):
over 8 games the layout shift moved total spend by 4455 cycles in 3.6 × 10⁹
(0.0001%) — 2856 of it the 8 cycles × 357 searches, the rest second-order bank
drift, since the host bank settles on true cycles.

### The other two review items, re-checked (both confirmed harmless, unchanged)

- **`TTBT[ply]` is never reset at node entry.** Confirmed exactly as described:
  `search.s` clears `TTBF,y` to `NOSQ` on both the full-width and the evasion
  entry paths but not `TTBT,y`, so a moveless `TT_UPPER` store at `sret` carries
  a stale `to` byte. Safe because every reader keys on `from == NOSQ`
  (`TTFROMA`/`ttmovevalid`), and not worse than described. Left alone.
- **RFP is skipped whenever alpha is still −INF.** Confirmed: `INF = 32000` is
  `$7D00`, and `-INF`'s high byte `$83` is below `NMATEZONEHI` `$8C`, so ±INF
  classify as mate scores and the guard skips futility at any full-window node,
  the root included. The mirror does the identical thing
  (`search.go` `alpha > nmateZoneHi && alpha < mateZoneLo`), so this is
  mirror-matched and deliberate. Left alone.

Gates, all green with the fix in: `TestMicroAB` (fingerprints identical),
`TestAdaptiveParity`, `TestAdaptiveEngineBehavior`, `TestIDIterationParity`,
`TestTTSequenceParity`, `TestFullGameMirrorParity`, `TestBudgetModeParity`,
`TestGenDeferTreeIdentity`, `TestSoftClockAdherence` (soft 0.9447 / exact
0.9107), and all of `internal/ucibridge` and `internal/ui`.
## 2026-07-28 — THE REVIEW'S OPEN FINDINGS, APPLIED: Ctrl-Reset, machine detection, Q, three-digit move numbers, and the draw that no rule licensed

Everything §5 of the review below left filed, fixed. **+195 B of UI**
(4,233 -> 4,428 B; LC free 1,895 -> 1,700; disk UI growth room 631 -> 436 B,
SD spare unchanged at 102 B). `engine.bin` md5 `58ef9645...`, byte-identical
to a `git archive HEAD` build; no engine source touched; `FEATURES` and
`FEATURES2` unchanged and still gated.

### 1. Ctrl-Reset is now a deterministic COLD boot (+4 B)

The IIe language card **is** disabled by a hardware reset — that is exactly
why Apple Pascal could use Ctrl-Reset as a warm start — so `$FFFC` is fetched
from ROM and `m8main`'s `sta $FFFC/$FFFD` was dead code. What actually
decided the outcome was Autostart's power-up test, and it PASSED: 8fish uses
only `$0300-$030C` and Standard Delivery never touches page 3, so
`jmp ($03F2)` handed the machine to an Applesoft whose entire zero page the
engine had trampled — neither 8fish nor a reboot.

`m8main` now writes `$03F3` = 0 and `$03F4` = `$FF` (the test is
`$03F4 == $03F3 EOR $A5`), so Ctrl-Reset cold boots and restarts 8fish off
the disk still in the drive. On the BLOAD path it is better still: ProDOS's
language-card image is long gone by then, so a warm start there was a
guaranteed crash. The dead vector writes are gone.

**What the gate can and cannot do.** goapple2 does not model the reset line's
effect on the language card, so no test can press Ctrl-Reset.
`TestColdStartHardening` gates the STATE the fix leaves — the power-up byte
is invalid — and `TestDiskQuitReboots` (below) exercises the same ROM
decision through the same vector, end to end.

### 2. 8fish now refuses machines that cannot host the engine (+100 B)

There was no detection at all, and the two failure modes are both invisible
to the person holding the disk:

- **Apple ][+ (or anything without the aux switches).** `$C002-$C005` are not
  soft switches, so every TT access at aux `$0200-$81FF` goes to MAIN — over
  the book at `$2000` and the engine image at `$4000`. **The engine
  overwrites itself mid-search.**
- **64K IIe.** The switches exist, but there is no RAM behind them: writes
  vanish, reads float. At ~10^7 probes a game against a 24-bit verify, that
  is roughly **one false TT hit per game** — a silent blunder, not a crash.

`m8machine` runs a **two-sided** probe: `$A5` to MAIN `$0300`, `$5A` to AUX
`$0300`, and BOTH must hold — aux reads back `$5A` *and* main still reads
`$A5`. The two-sidedness is what separates the two machines: a ][+ passes the
first half (the switch was a no-op, so its own `$5A` is what comes back) and
fails the second. `$0300` is `CEILMAX0`, engine scratch `uilimits` rewrites
every move, so the probe costs no storage. It forces the switches to MAIN
before it starts (whatever the firmware that ran before us left them at) and
puts aux `$0300` back to zero afterwards, because that address is INSIDE the
transposition table and `TestDiskPlays` requires the TT to be untouched until
something actually searches. On failure: `8FISH NEEDS A 128K APPLE IIE`, then
halt — with the display switches thrown FIRST, so the message is actually
visible on a machine whose firmware left the screen elsewhere.

`TestRefusesMachinesWithoutAuxRAM` boots the SHIPPING image on all three
machines, modelled by sabotaging exactly one fact about goapple2's IIe: the
control comes up and plays, and both bad machines get the message, halt, and
leave the engine image unmodified.

### 3. `Q-QUIT` quits (+12 B)

It stored to `EXIT_TRAP` (`$BFFF`) and fell into `m8new`. On hardware `$BFFF`
is plain RAM, so Q **silently discarded the game** under a label promising
the opposite, and no test pressed the key.

The switch to ROM cannot be made from code executing IN the language card —
the instruction after `lda $C082` would be fetched from ROM — so `cmd_quit`
copies six bytes to `$0300` and runs them from MAIN: `lda $C082`,
`jmp ($FFFC)`. With finding 1 applied that is a clean cold boot, which on a
machine with no resident OS is exactly what QUIT means.

`TestDiskQuitReboots` presses Q on the DISK-booted machine. It always asserts
that control leaves the language card through the ROM's own RESET vector;
with the Apple ][+ Autostart ROM — whose reset path this emulator can execute
end to end, unlike a real IIe ROM's, which calls into the unmodelled
`$C100-$CFFF` firmware — it follows the whole round trip back through the
Standard Delivery copier at `$0C00` to a fresh 8fish, screen-identical to the
first boot.

### 4. Move numbers past 99 (+37 B)

`uidec2` is a two-digit routine, so move 100 rendered as `:0` and 123 as
`;3` — its tens digit is a raw counter and `10 + '0'` is `':'`. `uidec3` is a
three-column right-aligned field; the panel is 19 columns and the line is now
13, so it fits. It **cannot** call `m8.s`'s `uid2z` (`ui.s` is also linked
standalone by `uitest.s`), so `uid2z` moved into `ui.s` and is now shared with
`uiscore`'s hundredths field — net cost of the move, 3 bytes.

One bug found while fixing it: `uimoves` computed the first panel row with
`lda UIHCNT / clc / adc #1 / lsr`, which **carries out at `UIHCNT` = 255** and
showed move 1 instead of move 128. Unreachable while the ply cap existed, and
reachable the moment finding 5 removed it. `ROR` instead of `LSR`, 0 bytes.

`TestMoveNumberOverNinetyNine` was the characterization test that pinned the
defect; it now asserts the fix.

### 5. A legal game is never drawn for being long (+33 B)

`uisync` set `RES_LONG` ("DRAW: TOO LONG") in **any** position at
`UIHCNT == 250`. 125 moves is not a draw under any rule — the FIDE ceiling is
the 75-MOVE rule, a counter — so this was a wrong RESULT, not a cosmetic
limit, and `TestHistoryCapEndsALiveGameInADraw` had it drawing a game with
White up a bishop, a knight and two pawns.

**The result code is gone.** The game continues and the RECORD degrades
instead. At ply 255 the move is played into the last slot, `UIHCNT` pins at
255 (nothing wraps to index 0, nothing is written past index 255) and
`UIHFULL` comes up. The current position's hash still lands at index
`UIHCNT`, so `uireps` keeps comparing like with like and can only ever
UNDER-count repetitions; the 50-move rule is a counter, not a history, and
mate and stalemate are untouched — which is everything needed to FINISH the
game. What is lost is exact and stated: the move panel stops growing, and
takeback is refused **in words** (`MOVE LIST FULL - CANNOT TAKE BACK`) rather
than replaying to a position that is not the one before the last move.

That last point is why the alternative — sliding a window over the arrays, or
wrapping — was rejected: both need a saved replay BASE (~164 B of position
snapshot plus the code to save and restore it) or they silently take back to
the wrong position, and both restart the move numbering.

`TestLongGameIsNotDrawn` types a 262-ply draw-free game and requires: no
result ever declared; the position still tracking refchess after the arrays
fill; `UIHCNT` pinned at 255; canaries intact past the end of every array;
the takeback refusal; and the game still playable after it.

### 6. Minor hardening (+9 B)

`cld` at entry (D is undefined after reset on NMOS; the ROM clears it in
practice); `bit $C010`, so a key held down while the disk loads is not eaten
as the first character typed; and a `RES_ERR` guard in `uiresultmsg`, which
used to print `INTERNAL ERROR` **and** `GAME DRAWN`.

### Ledger

| | before | after |
|---|---:|---:|
| UICODE | 4,233 B | **4,428 B** |
| LC total ($E000-$FFEF, 8,176 B) | 6,281 B | **6,476 B** |
| LC free | 1,895 B | **1,700 B** |
| disk SD spare | 102 B | **102 B** |
| disk UI growth room | 631 B | **436 B** |

## 2026-07-28 — FOUR-LENS ADVERSARIAL REVIEW: the shipping disk was ~24 Elo weaker than everything we measured, and would have sprayed garbage on a IIe

Four independent reviewers, one lens each (hardware fidelity, engine asm,
chess rules, instrument/memory-map integrity), told to verify rather than
speculate and not to pad. All findings below are CONFIRMED unless marked.

### ★ 1. The disk shipped `FEATURES = $1F`; every measurement used `$5F` — FIXED

`asm/m8.s` installed `$1F` under a comment reading "all search + eval
features" — true only until **FT_CKEXT was adopted 2026-07-25 at +24 ± 23
Elo over 600 SPRT games**. Every rig plays `$5F`: `ucibridge.runEngine`,
`internal/sprt`, `TestProfileR5`, the soft-clock gates, and **both Sargon
gauntlets (+89 and +110)**. So the bootable disk — the artifact a user is
handed — played about **24 Elo below the artifact this log describes**. Not
a crash; a silently weaker product.

**Two independent reviewers found it the same day**, which is the strongest
evidence the lens split was worth it.

**Why every gate missed it, and this is the durable lesson:** `internal/ui`'s
reference searches go through `chesstest.NewMachine`, whose TEST default is
also `$1F`, so `TestEngineParity` compared **`$1F` against `$1F`**. The
reference had been built to match the UI rather than to match what ships —
the identical shape as the harness clock the hardware does not have, and the
position pool that was not game conditions. `engine.bin` is unaffected (the
mask is poked at runtime), so MicroAB fingerprints never moved.

Now gated from both directions by `TestShippedFeatureMask` and
`TestShippedFeatureConfig`, which read the bytes out of the BOOTED image and
compare them against the bridge.

Second-order, still open: the FT2_SOFTCLK cost table was **fit on `$5F`
games** while the device ran `$1F`, and check evasions are exactly the
per-node cost the phase regressor cannot see. Fixing FEATURES realigns them —
but the adherence numbers should be re-run before anyone touches SOFTMARGIN.

### ★ 2. `80STORE` was never cleared: the aux TT would land on the text screen — FIXED

With `80STORE` on, `$0400-$07FF` follows `PAGE2` and **ignores
`RAMRD`/`RAMWRT`**. The TT spans aux `$0200-$81FF`, which contains that
range — so 1,024 TT slots would write to the **MAIN text page**: garbage
characters spraying across the board on every search, and a TT reading the
screen back as entries.

A IIe powers up with `80STORE` off, so the **disk boot was never affected**.
But the 80-column firmware turns it ON — exactly what a `BLOAD`/`BRUN` from
BASIC.SYSTEM in 80-column mode inherits (the default on a IIc, and on any IIe
booted after `PR#3`), which §12.2 ships as a first-class path. Fixed with one
`sta $C000` (+3 B).

**goapple2 does not implement `80STORE`** (it counts `$C000/$C001` as
Unhandled), so **no test in this repo could ever have caught it**.
`TestDiskBoots` now *requires* the store to appear in Unhandled, so it cannot
be silently removed.

### 3. Two rules bugs a player would have hit — both FIXED

- **Resigning always announced "BLACK WINS" in two-player mode.**
  `cmd_resign` computed the winner as `UIHUMAN EOR COLORMASK`, but in referee
  mode `UIHUMAN` is the sentinel `$FF`, so `$FF EOR $08 = $F7` → read as
  Black whoever resigned. Fixed to `lda SIDE` — the same expression `uisync`
  already uses for mate, and one byte smaller.
- **A draw offer was answered from a search of a retracted position.**
  Takebacks stack, so the engine could agree a draw on the strength of a line
  many plies from the board. `cmd_take` now clears the stale score, the same
  rule `m8engine` already applied after a book move.

### 4. The chess itself is CLEAN

Worth stating positively. Differential gates added: 28 hand-built corners
(every castling transit square, castling out of/into check, the horizontal-pin
en passant, promotion with capture, smothered mate, double check), ~800 random
positions, and 12 random games **typed into the UI** with the whole position
compared every ply. Verified: rights lost to a rook **captured on its home
square**; repetition is position-based (castling rights, ep file and side to
move all in the Zobrist) and fires on the third occurrence; the 50-move rule
is halfmoves and **mate on the hundredth halfmove is mate**; all four
promotions reachable including on captures. Insufficient material is a
*missing* draw, never a wrong one.

The engine reviewer separately audited `ttmovevalid` line-by-line against
`generate` (the routine that, since deferred generation, is the ONLY thing
stopping a 2⁻²⁰ TT collision from putting an illegal move on the board) and
found no permissiveness; also make/unmake symmetry path-by-path, every
signed/unsigned score compare, the TT depth/bound proof, page alignment and
both SMC sites, and a scripted zero-page overlap check. Nothing found.

### 5. Open, NOT fixed (filed) — ALL FIXED 2026-07-28, see the entry above

- **Ctrl-Reset does not return to 8fish.** The IIe language card *is*
  disabled by a hardware reset, so `$FFFC` comes from ROM and `m8main`'s
  vector writes are dead code. Autostart's warm-start test then passes and
  jumps into Applesoft **with zero page trashed by the engine**. 8-byte fix
  proposed (invalidate the power-up byte so Ctrl-Reset is a clean cold boot).
- **No machine detection.** On a ][+ the aux switches are no-ops, so the TT
  writes over the book and the engine image — it overwrites itself mid-search.
  On a 64K IIe the TT is dead and ~1 false hit per game slips the 24-bit
  verify. ~30 B two-sided probe proposed.
- **`Q-QUIT` does not quit** — it silently starts a new game, and no test
  presses Q.
- **Move numbers ≥ 100 render as punctuation** (`uidec2` is two-digit); and a
  legal game is **force-drawn at ply 250** regardless of position.
- ~~**`adaptmaybe`'s score-drop test lacks overflow correction** (SUSPECTED) —
  time management only, same class as the `cmp #$74` bug, in code nobody
  re-audited after that fix.~~ **CONFIRMED and FIXED** the same day; it fires
  zero times in 530 games. See the entry above.

### 6. Gate weaknesses found (several fixed)

`TestUIByteBudget`'s fit assertion is unreachable (the linker always fires
first); `TestDiskLedger`'s book identity check compares a file to itself;
`TestBlobSize` compares blob **length**, not content; `TestMoveStackWatermark`
reads MSP at hardcoded `$10/$11` instead of `defs["MSP"]` — relocate MSP and
it silently samples the wrong bytes and **passes**, since the gate is
one-sided. New `TestMainMemoryLayout`/`TestLanguageCardLayout` derive the whole
map from the `.cfg`, `defs.inc` and real binary sizes.

**GDVBUF was the third stale memory-map comment** (after the `$2000-$207F`
reservation and the `$0E00` staging address): it sits at `$3000` justified as
"above the book", quoting the size the book had *before* widening — it now
overlaps by 3,311 B. Since there is no other 4 KB hole in MAIN, the overlap is
now documented as deliberate and **enforced where it can fail**:
`chesstest.newStubMachine` refuses to build a GDVERIFY machine with a resident
book.

### 7. Infrastructure

`make test` **cannot run inside a `.claude/worktrees/` worktree** at all —
`go.work` lists the repo root, and the worktree is inside the repo but outside
the workspace, so `go build ./...` fails in a way easily mistaken for a build
error. And one reviewer's worktree was created from a commit **~40 commits
behind main**, where the files under review did not yet exist; it had to reset
to main first. Both are worth fixing before the next agent fleet.

## 2026-07-28 — ADVERSARIAL RULES REVIEW of the UI's own referee: the chess is CLEAN, two GAME-RESULT bugs were not

The on-device UI (`asm/m8.s`) is a second, independent implementation of the
rules — legality, check, and game termination — because on hardware there is
no Go referee. This pass hunted it differentially against `internal/refchess`.

**The chess itself came back clean.** New gates in `internal/ui/rulesdiff_test.go`:

| gate | what it swept | result |
|---|---|---|
| `TestRulesCorpus` | 28 hand-built castling / en-passant / promotion / mate corners: every transit square of all four castles, castling out of and into check, the horizontal-pin en passant, promotion with capture, smothered mate, double check | legal-move count, in-check flag and termination code all agree with refchess |
| `TestRulesRandomSweep` | ~800 random positions | no disagreement |
| `TestRulesPlaythrough` | 12 random games TYPED IN, whole position compared every ply | rights lost to a rook CAPTURED on its home square, ep set/expiry, and the halfmove clock all exact |
| `TestThreefoldExactPly` | a knight shuffle | draw fires on the third occurrence, not the second |
| `TestThreefoldRespectsCastlingRights` | `Ra1b1 Ra8b8 Rb1a1 Rb8a8` x3 | the repetition hash is POSITION-based: identical placement with different castling rights is **not** a repetition, so the draw comes at ply 12 and not ply 8 |
| `TestFiftyMoveBoundary` | 99/98/capture/pawn-move/mate-on-the-100th | halfmoves not fullmoves, resets correct, and **mate beats the 50-move rule** |
| `TestEnPassantExpiry`, `TestUnderpromotionThroughThePrompt`, `TestNoMoveAfterGameOver`, `TestTakebackAgainstTheEngine`, `TestTakebackAcrossABookMove`, `TestSideSwapBookkeeping` | the paths the two-player gates could not reach | all clean |

Notably the two-plies-at-a-time takeback branch had never been exercised: in
referee mode `UIHUMAN` is `$FF`, so `cmd_take` always stepped back one.

### The two real bugs, both about the RESULT rather than the moves

**1. In TWO-PLAYER mode, resigning always announced "BLACK WINS."**
`cmd_resign` derived the winner from `UIHUMAN ^ COLORMASK`, and in referee
mode `UIHUMAN` is the sentinel `$FF`, not a colour — `$FF ^ $08` is nonzero,
which reads as Black. Two people playing each other got the wrong result
printed whichever of them resigned. Fixed by deriving it from `SIDE`, which
is the resigning side in EVERY mode (`R` is only reachable while `UIRESULT`
is 0, i.e. on the side to move's own turn) and is the same expression
`uisync` already uses for a mate. **One byte smaller** — `SIDE` is zero page.

**2. `D` could be answered from a search of a position the player had just
withdrawn.** `cmd_draw` accepts iff `UILSC` — the last completed search score
— is below −150 cp, and `cmd_take` did not clear it. Takebacks stack, so the
score could be arbitrarily many plies away from the position being offered a
draw in: search at ply N says −900, take back ten times, offer a draw at ply
N−20 where the engine is fine, and it agrees. Fixed by retracting `UILSC`
(and the think line) in `cmd_take` — the same rule `m8engine` already applies
after a book move, where "the previous move's depth/score readout would be a
lie." 9 bytes.

Net **+8 B** of UI: 4,222 → 4,230, free 1,898 of 8,176. `engine.bin` md5
unchanged (`58ef9645…`); no engine source touched.

### Confirmed limits, not bugs

- **`RES_LONG`: at 250 plies the UI declares "DRAW: TOO LONG" in any
  position.** `TestHistoryCapEndsALiveGameInADraw` builds a legal 250-ply
  game that meets NO draw rule (halfmove clock and repetitions both kept
  clear) and shows the UI drawing it with White up a bishop, a knight and two
  pawns against a bare king. It is a hard stop, not a wrap — no further move
  is accepted and the one-page arrays are never written past their last byte
  — but it IS a result the rules do not license. 125 moves is not a draw and
  the FIDE ceiling is the 75-move rule. Raising it costs 4 bytes of LC RAM
  per extra ply of hash history (1,898 free ⇒ room for ~470 more plies).
- **Insufficient material stays a MISSING draw, never a wrong one.** In KK /
  KNK / KBK the side to move always has legal moves, so it is never
  mis-called stalemate, and neither a capture nor a pawn move is available,
  so the halfmove clock runs to 100 and `RES_50` draws the game. The cost is
  up to 50 moves of shuffling, not a wrong result.
- **The en-passant square is hashed on every double push**, whether or not a
  capture is possible — the usual over-strict convention, which can only miss
  a repetition. Here it cannot even do that: any position with a live ep
  square has `HALFMOVE == 0`, so `uireps`' lower bound equals `UIHCNT` and the
  scan does not run at all.
- **Move numbers ≥ 100 render as garbage in the side panel** (`;3`, `<0`):
  `uidec2` is a two-digit routine and the 250-ply cap allows move 125. Purely
  cosmetic, and fixing it widens the panel's move-number field from 2 columns
  to 3, which moves every panel string the existing screen gates assert. Left
  alone deliberately; see the `TestHistoryCapEndsALiveGameInADraw` screen.

### Where the hunt found nothing

`ttmovevalid`'s promotion/castling rejections (already gated by
`TestTTMoveValidExhaustive` and `TestTTMoveValidRejectsPromoAndCastle`); the
`uifind` promotion match (`FL_PROMO` is `$07` and holds the piece type, so
`MVFLAGS & FL_PROMO` compares directly against `UIMPROM`); `uireps`' bounds
and 2-ply stride; the LC variable map (`$F700-$FEFF` fully accounted, buffers
all terminated within their extents); the engine moving in a terminal
position (`mloop` syncs before it ever calls `m8engine`); and `uiapply`'s
history-array indices, which the `RES_LONG` guard keeps below 251.

## 2026-07-28 — ★ 8fish BOOTS FROM A DISK. Not built — booted, on two unrelated emulators.

`make dsk` → `asm/8fish.dsk` (143,360 B), a Standard Delivery disk built with
`diskii mksd`. Independently re-verified on main:

```
machine: Apple IIe (goapple2/iie 128K) + Disk II in slot 6,
         ROM = genuine Apple IIe (MAME apple2e: 342-0134-a + 342-0135-b)
entry:   $C600  — the Disk II controller's own 341-0027 P5 boot ROM (i.e. PR#6)
LOADED:  44,954 B ($0C00-$BB99, 176 sectors) in 6,275,994 cycles (6.15 s),
         byte-identical to the image
BOOTED:  painted board at 6,430,240 cycles (6.30 s), PC $E20A
```

**Nothing is placed in RAM by the harness.** The CPU starts at `$C600`, the
boot ROM reads T0S0 to `$0800`, Standard Delivery reads 176 sectors, and the
copier lifts the UI to `$E000`. `TestDiskPlays` goes further: `e2e4` typed on
the modelled keyboard → book reply, then offbeat moves leave the book, the
engine searches, and **193 non-zero bytes appear in the aux-RAM TT at
`$0200-$81FF`** — proof the aux bank is live under a real disk boot.

Asserted: delivered bytes byte-identical to the image; ALTCHARSET on, 80COL
off, TEXT/NOMIX/PAGE1; **no unimplemented `$C0xx` touched**; and the screen
**byte-identical to the in-memory shipping build's, all 960 bytes**.

**MAME cross-check.** The same disk boots in `apple2ee`, and typing `e2e4`
produces a *different* book reply (correct — the dither seed is keystroke
arrival time). Pieces render as inverse letters through the real
342-0265-a character ROM: **the first independent confirmation of the
ALTCHARSET fix**, on an emulator with no shared code with ours.

**No fork of the UI.** `UIPAYLOAD` moved out of `asm/m8.s` into the linker
config, so the disk layout (`asm/m8sd.cfg`: copier `$0C00`, payload `$0D00`)
and the BLOAD layout are two links of one object file. `m8sd.bin` is
byte-identical to `m8.bin`; the two copiers are 57 B each and differ in
**exactly one byte** (`$09`→`$0D`). `engine.bin` md5 unchanged.

**The goapple2 bridge was small, not large** — 244 non-comment lines wiring
`cards.DiskCard` into slot 6 in front of the existing `iie` memory model, and
**zero goapple2 changes**. A fixed-point check on `$FF58`/`$FCA8` caught the
ROM halves being swapped, which would otherwise have surfaced as "unknown
opcode $04 at $FF59".

**Layout note.** zellyn: *"It's our disk. We can do whatever we want."* The
44 KB `mksd` cap and the contiguous span are therefore **not** hard limits —
`$0C00` single-shot is chosen because it works today, not because it is
forced. `TestDiskLedger` gates both margins (**SD spare 102 B, UI growth room
642 B**) but is framed as a **tripwire**: its failure text says "the one-shot
path no longer fits — chain-load or ProRWTS2", not "make 8fish smaller".
`TestMarginsCanFail` proves the ledger can actually go negative, and
`TestBaseTradeoff` re-derives the whole base/span/margin table from real file
sizes. `TestDiskRoundTrip`/`TestSectorOffset` verify the interleave against
all 176 pages.

**Boot speed is a design goal, not an accident** (zellyn: *"everyone likes it
when software boots really fast"*). Standard Delivery reads sequential tracks
with no directory parse and nothing resident — 6.15 s for 44 KB.

### ProRWTS2 recon (read, not run) — for when save games are designed

Standard Delivery is **read-only**, so saves need write routines. Boot path
and save path are separable, though: it is our disk, so SD can keep the fast
sequential boot while reserved tracks hold saves. Findings:

- ACME, not ca65 — a toolchain cost (a2audit already uses ACME).
- Driver `one_page`/**`two_pages` (default)**/`three_pages` ≈ 512 B, plus
  `dirbuf`/`encbuf`/`treebuf` at 512 B each, independently placeable.
- Default `reloc = $D000, lc_bank = 1` **collides with 8fish** (LCCODE at
  `$D000`, UI at `$E000`) — but `lc_bank = 2` is supported and 8fish leaves
  all **4,096 B of LC bank 2 free**. That is the obvious home.
- `enable_write` requires a **pre-existing fixed-size file**, written in block
  multiples. A saved game must ship as a placeholder overwritten in place —
  that shapes the UI feature before it is designed.
- `allow_aux` is likely unnecessary (the TT is scratch and never saved),
  dodging its `load_high`/`swap_scrn` complications.
- `init` unhooks ProDOS, so ProDOS need not be resident — no `$BF00` clash.
- Open: whether a ProRWTS2 chain still delivers the engine as cheaply as
  6.15 s. **Do not hand-roll a sector writer** — 6-and-2 nibble encoding and
  write-splice timing corrupt the disk when wrong rather than failing clean.

## 2026-07-28 — ★ THERE IS A DISK, AND IT BOOTS: `make dsk` → `asm/8fish.dsk`, booted from `$C600` and played

8fish had a shipping image and no way to hand it to anyone. It now has a
bootable 5.25" floppy, built by `make dsk`, and the boot is **executed, not
asserted**: `internal/ui`'s `TestDiskBoots` puts **nothing** in memory, starts
the CPU at `$C600` — the Disk II controller's own 341-0027 P5 boot ROM — and
lets the machine do the rest.

```
$C600  boot ROM        reads track 0 sector 0 to $0800, JMP $0801
$0801  Standard        reads 176 sectors into $0C00-$BB99, JMP $0C00
       Delivery
$0C00  m8sdboot.bin    latch $C08B, lift the UI to $E000, install LCCODE
                       at $D000, JMP $E000
$E000  the UI          paints, blocks in entkey's $C000 poll
```

**RESULT: it paints the start position, and then it plays.** Measured, from
the emulated IIe's own clock: **6,275,994 cycles (6.15 s) to load 44,954 bytes
off the disk**, 6,430,240 (6.30 s) to a painted board waiting for a key.

| assertion | result |
|---|---|
| the loader delivers the image **byte-identical** into `$0C00-$BB99` | **pass** (44,954 B, 176 sectors) |
| the UI is executing from LC RAM (`PC $E20A`), `ALTCHARSET` on, `80COL` off, TEXT/NOMIX/PAGE1 | **pass** |
| the painted screen is **byte-identical** to `BootShipping`'s, all 960 bytes | **pass** |
| the boot touched no `$C0xx` location the IIe model leaves unimplemented | **pass** (`Unhandled` empty) |
| `e2e4` typed on the modelled IIe keyboard → book reply `c7c5`, screen names *B90 Sicilian, Najdorf* | **pass** |
| out of book, the engine **searches** (`D 2 -0.06 g8f6`) and writes the transposition table in **AUX RAM** | **pass** (193 non-zero bytes in aux `$0200-$81FF`) |

That last row is why the machine has to be a IIe. `internal/sargon` boots
`sargon-iii.dsk` on goapple2's interactive `Apple2`, which is a ][+ with a flat
64K array and no aux at all; 8fish's TT lives in aux. Rather than retrofit
`Apple2` (goapple2 "stage 2", still open), `internal/ui/diskboot.go` **bridges
the two halves that already exist**: goapple2's `iie` 128K memory model — which
`harness.Machine` has used all along — plus goapple2's real `cards.DiskCard`
wired into slot 6 at `$C0E0-$C0EF` and `$C600-$C6FF`, driven by go6502. That
bridge is 244 lines of Go (`internal/ui/diskboot.go`) and **no changes to
goapple2 at all**. The `$D000-$FFFF` ROM is a genuine Apple IIe image where one
is installed locally, with the ][+ ROM as a fallback — the boot path reads
exactly two ROM locations, `$FF58` (RTS) and `$FCA8` (WAIT), identical on both
machines, and after the copier's `lda $C08B` the ROM is banked out for good.

### CROSS-CHECKED ON MAME, which shares no code with any of the above

goapple2 is our emulator; agreeing with ourselves proves less than agreeing
with someone else's. The same `asm/8fish.dsk`, unmodified, in
**MAME's `apple2ee`** (a real Apple IIe with the real 342-0265-a character
ROM), booted from slot 6 and driven by `-autoboot_command "e2e4\n"` — i.e.
typed on MAME's emulated IIe keyboard, not injected:

```
mame64 apple2ee -flop1 8fish.dsk -video none -str 25 \
       -autoboot_delay 12 -autoboot_command "e2e4\n"
```

It boots, paints the checkerboard, takes the move and answers from the book
(`BOOK: B18 Caro-Kann, Classical` — a different book line from goapple2's B90
Najdorf, which is *correct*: the engine's seed is the arrival time of the
keystrokes and the two emulators do not type at the same moment). Two
independent emulators, two character ROMs, one disk.

**And the ALTCHARSET fix is confirmed against a real character ROM.** MAME
renders the pieces as inverse letters cut out of lit squares — not the
flashing punctuation the primary character set would have produced. That was
the defect found earlier today by reasoning; this is the first time a
character generator other than goapple2's `chargen` has been asked.

### The two margins — a TRIPWIRE, not a wall

Standard Delivery loads **one contiguous image**, so the gaps between the four
pieces cost sectors, and `diskii mksd` refuses anything over **45,056 bytes**.
The image runs from the staging base to `engine.bin`'s last byte at `$BB99`, so
the base is the only free variable — and raising it trades disk margin for UI
staging room, 256 bytes at a time:

| base | image | SD spare | UI growth room |
|---|---:|---:|---:|
| `$0800` (the BLOAD layout) | 45,978 | **−922** | 1,666 |
| **`$0C00` (chosen)** | **44,954** | **102** | **642** |
| `$0F00` | 44,186 | 870 | −126 |

744 bytes of slack in this layout, split between two budgets that grow from
opposite ends: the **engine** spends the SD spare, the **UI** spends the growth
room. `TestDiskLedger` prints both on every run and fails when either goes
negative, naming which one and by how much.

**But neither is a wall, and the test says so.** zellyn: *"It's our disk. We
can do whatever we want. We can load one thing that loads another, etc."* The
44 KB cap and the contiguous span are properties of the *simplest* Standard
Delivery layout, not of the medium. So the ledger's failure message is "the
one-shot path no longer fits, pick a mechanism" — chain-load, or **ProRWTS2** —
and explicitly not "make 8fish smaller". The layout was chosen because it fits
today and boots today, and it was deliberately **not** optimised further.

`TestDiskRoundTrip` reads the built `.dsk` back sector by sector and requires
it to equal the image handed to `mksd` (Standard Delivery's interleave is
`sector 0, 14, 13 … 1, 15` per track, with track 0 sector 0 the boot sector).

### No fork of the UI

The staging address moved out of `asm/m8.s` and into the **linker config**
(`asm/m8sd.cfg` defines `UIPAYLOAD`), so the disk layout and the BLOAD layout
are two links of one object file. `TestDiskLayout` asserts the consequence:
`m8sd.bin` is **byte-identical** to `m8.bin`, and `m8sdboot.bin` differs from
`m8boot.bin` in **exactly one byte** — the payload's page (`$09` → `$0D`).
`asm/m8.bin` and `asm/m8boot.bin` are `cmp`-identical to a `git archive HEAD`
build, and `asm/engine.bin` is still md5 `58ef9645…`.

### What this does NOT do: save/load

**Standard Delivery is READ-ONLY.** It boots and it loads; it has no file
system and no writer, so a saved game is impossible on this mechanism, full
stop. That is the reason ProRWTS2 (peterferrie) is the intended successor
rather than merely a nicer option: it reads *and writes* ProDOS files, so one
mechanism covers boot, load and save — and it incidentally removes the
contiguous-span squeeze, handing the UI back its full 5,888-byte LC budget.

Deliberately **not** started here, and deliberately not worked around:

- **No chain-loader.** Once you are chain-loading you have written most of a
  sector reader, and at that point taking a good one beats hand-rolling.
- **No hand-rolled sector WRITER, ever.** Writing a Disk II means 6-and-2
  nibble encoding and write-splice timing; getting it wrong corrupts the disk
  rather than failing cleanly. This is the last thing this project should
  write itself.

#### Reconnaissance for that task (read from `PRORWTS2.S`, not measured here)

Not started, but read, so the follow-on begins informed. Source:
[peterferrie/prorwts2](https://github.com/peterferrie/prorwts2), BSD-3-Clause,
3,928 lines, © 2013-2022. Everything below is quoted or read out of the config
block at the top of that file; **none of it has been run**.

| finding | why it matters to 8fish |
|---|---|
| **Assembled with ACME**, not ca65 | a real cost this project has not paid before — either add ACME to the toolchain (a2audit already uses it, so there is precedent in zellyn's own repos) or port it |
| driver size is a build option: `one_page` / **`two_pages` (default)** / `three_pages`, "set to 1 if verbose mode says that you should" | the driver itself is ~512 B of relocated code, not a library |
| buffers `dirbuf`, `encbuf`, `treebuf`, 512 B each, "independent of each other so they can be placed separately", defaulting just below `reloc` | budget ~512 B (read) to ~1,024 B (read+write) of buffers on top of the driver |
| default `reloc = $D000` with `load_banked = 1, lc_bank = 1` | **would land on top of 8fish**: `$D000` is the engine's `LCCODE` and `$E000-$FFEF` is the UI. But `reloc` is any page the caller picks, and `lc_bank = 2` is supported — and **8fish leaves all 4,096 B of LC bank 2 free** (§2), which is enough for a two-page driver plus both buffers. That is the obvious home |
| `enable_floppy = 0` by default | must be set to 1; the default build is hard-disk only |
| `enable_write = 1`: *"file must exist already and its size cannot be altered; writes occur in multiples of block size"* | a saved game must be a **pre-created fixed-size file** shipped on the disk and overwritten in place — not a file created at save time. That shapes the UI's save feature before a line of it is written |
| `allow_aux = 1` exists ("read/write directly to/from aux memory"), but *"requires `load_high`… else driver must be running from same memory target"*, and `swap_scrn` is "recommended if `allow_aux` is used, to avoid device reset" | the TT in aux `$0200-$81FF` is search scratch and never needs saving, so `allow_aux` is probably **not** needed — which avoids the SmartPort screen-hole complication entirely |
| `init` "unhook[s] ProDOS, detect[s] drive type, and relocate[s] code if needed" | ProDOS does **not** have to be resident, so its `$BF00` global page and LC residency do not collide with 8fish |

Open question the source does not answer: whether a ProDOS-formatted volume
plus a ProRWTS2 boot chain still delivers the engine as cheaply as Standard
Delivery does today (6.15 s for 44,954 B), or whether boot time regresses.

**Still not de-risked:** nobody has run this on a real Apple IIe. What is new
is that there is now something to put in the drive, and that everything
between the drive head and the painted board has been executed rather than
assumed.

## 2026-07-28 — Book WIDENED (3,866 → 7,407 B): coverage transformed, Elo **+3 ± 10**. The null is an INSTRUMENT LIMIT, not a verdict on the book.

zellyn's brief was breadth, not depth: *"Books are more to stop you from
doing something dumb, and to leave you reasonably set up, than to try to
drill as deep as possible."* Deferred until everything else was done; that
condition was met.

**Before:** 10 plies deep, 1-3 moves wide. Answered **4 of 20** first moves
as Black. Against our own engine it died on move 2 (mean exit ply
2.50/3.00). Crucially the exit positions were *fine* (+9/−29 cp) — the book
was not misplacing us, it was simply **leaving**. That is what made breadth
the right lever rather than better lines.

**After:** 48 → 179 lines, **7,407 B of 8,192 (90.4%, 785 free)**. 20/20
first moves answered in both colours; ply-1 opponent replies covered
**17.5% → 72.5%**; exit ply 5.00/9.00; 45% more book moves played. Two
compiler additions: a `/w` `/b` perspective suffix (answer 1.g4 without
playing it) and name dedup. 41 lines transpose into existing main lines and
inherit their depth for free.

**★ The measured result: +3 ± 10 Elo over 3,000 paired games (new vs old),
LLR(0,10) −0.79. And the reason is structural.** Head to head, both sides
played **exactly 15,594 book moves**. The book is a property of the
POSITION, so both engines enter and leave it together and the extra width
is never exercised. Worse, self-play's opponent is our own engine — which
never plays 1.Nc3, and so is structurally incapable of charging us for
losing the thread on move one. **Self-play cannot price book breadth.** The
null is a statement about the instrument, not about the book.

For scale, the same rig prices the book as a whole honestly:

| match | games | result |
|---|---|---|
| new vs old (paired) | 3,000 | **+3 ± 10**, LLR −0.79 |
| new vs NO book | 2,000 | +37 ± 12, LLR **+8.72** (accepts +10) |
| old vs NO book | 2,000 | +35 ± 12, LLR +8.17 |

**The book is worth ~+36 Elo; widening it is worth nothing self-play can
see.** Where the coverage gain IS visible is against a foreign opponent:
**4 of 40 recorded Sargon games left the OLD book at ply 1** to 1.Nc3, and
a live game showed the new `1.d4 d6` line transposing into the Pirc and
holding book three moves where the old book had nothing. A Sargon gauntlet
for an Elo number was refused as prohibitively expensive (thousands of
games at ~5 min each), so **whether the coverage converts to Elo against
diverse opponents remains UNMEASURED** — claimed neither way.

**Kept** on: not a regression, repertoire provably unchanged, and the space
had no other claimant. Honest cost to record: `$2000-$3FFF` is now 90% full,
so a future hi-res board would have to move the book to LC bank 2 — which
the UI design already assumed it would.

### The defect the measurement caught

The first draft measured **worse than the book it replaced** (+7 vs +35 vs
no-book). Not the lines — the **weights**. A four-ply line added merely to
*answer* 1...h5 still bumped the weight of its own first move, and thirty
such lines moved our repertoire from **65% 1.e4 to 44%**. `Build` now
refuses this structurally: marked lines are weightless and may not add an
alternative where main lines already answer. Repertoire is byte-identical
to the old book and the same match returns +37. A widening pass that
quietly rewrites your opening repertoire is exactly the failure a
book-vs-book A/B exists to catch.

### The `$2000-$207F` reservation was WRONG, and is deleted (task #38)

`defs.inc` reserved it as move-stack guard slack that "must stay
unallocated" while `book.inc` put `BOOK_BASE` there. Resolved against the
reservation, on three counts:
- **The premise was false.** The exposed window is fixed at 128 bytes by
  `MOVESTACKTOP`; it does not grow with the book.
- **It protected nothing in either environment.** Under the harness the
  `EXIT_TRAP` store ends the run *on that instruction*, so the ≤124 bytes
  already written are the whole blast radius. On real hardware `$BFFF` is
  plain RAM, so the trap is a no-op and an overrunning generator writes past
  `$2000` unbounded — 128 bytes could never contain it.
- **What actually protects it is distance, now enforced.** New
  `TestMoveStackWatermark` samples `MSP` once per executed instruction:
  worst case **487 of 1,152 slots (42.3%)** across tactical, 3-queen
  max-mobility and depth-10 endgame searches; it fails at 50%. A comment
  became a tripwire.

### Instrument work (reusable)

`sprt` gained per-side `BookA`/`BookB`, probed **on device** via
`AsmBookProbe` (banking the probe's unspent allocation as `ucibridge`
does); `NoOpening`, because the rig previously forced a 6-ply prefix
straight through the book and `Run` now *refuses* a book match without it;
and `ABookMoves`/`BBookMoves` printed **before** the Elo, so a match that
engaged no book cannot masquerade as a null.

Gates: `TestBookProbeParityASMvsGo` (264 → 585 positions), weighted
distribution, out-of-book, e2e driver, bridge, on-device UI name walk,
`TestCompiledMatchesGeneratedLines`, `TestBlobSize`,
`TestMoveStackWatermark`, full `go test ./...` green. No engine core source
touched.

## 2026-07-28 — the opening book, MEASURED and then WIDENED: coverage 4/20 → 20/20 first moves, and no Elo to show for it

The last strength lever on the list, deferred until everything else was
done. Three separate things happened: a memory-map contradiction was
settled, the existing book was measured for the first time, and it was
widened. The first two are unambiguous wins. The third is a large,
verifiable improvement in what the book COVERS and a **null result** in
what it is worth over the board, and the null is reported first because it
is the part that would otherwise get lost.

### 1. `$2000`: `defs.inc` and `book.inc` disagreed, and `defs.inc` was wrong

`defs.inc` reserved `$2000-$207F` as move-stack overflow guard slack that
"must stay unallocated". `book.inc` has always had `BOOK_BASE = $2000`. The
reservation was the wrong comment and has been deleted.

**The premise that this blocked growing the book was itself false.** The
exposed window is fixed at `$2000-$207F` by `MOVESTACKTOP`; it does not get
larger when the book does. A 3,866-byte book and an 8,192-byte book expose
exactly the same 128 bytes.

**The slack would not have worked in either environment.**

- Under the harness the store to `EXIT_TRAP` ends the run *on that
  instruction*, so the ≤124 bytes already written are the entire blast
  radius and nothing runs afterwards to observe them.
- On real hardware `$BFFF` is plain RAM (`cmd_quit`, `asm/m8.s`), so the
  trap is a **no-op**: the generator returns and keeps emitting past `$2000`
  for the rest of that ply and every deeper one. The overrun is unbounded
  there. 128 bytes could not contain it.

**What actually keeps the book safe is distance, and that is now measured
and enforced.** `chesstest.TestMoveStackWatermark` samples `MSP` (`$10-$11`)
**once per executed instruction** across tactical, maximum-mobility (3
queens + 2 rooks) and depth-10 endgame searches:

| position | depth | peak MSP | slots used | % of 1,152 |
|---|---|---|---|---|
| kiwipete | 6 | `$159C` | 487 | 42.3% |
| open middlegame | 7 | `$14FC` | 447 | 38.8% |
| max mobility | 6 | `$14EC` | 443 | 38.5% |
| pawn endgame | 10 | `$12BC` | 303 | 26.3% |
| start position | 7 | `$129C` | 295 | 25.6% |

Overflow needs 1,152 slots. The worst case is 487. The test **fails** if
that ever passes half of capacity — which is the point at which the
argument stops holding. A comment was replaced with a tripwire.

### 2. The book, measured before touching it

Nobody had ever measured this book. Three instruments, all new:

**Structural (`TestBookOpponentCoverage`).** Walk every position the book's
own lines reach; at each node where the OPPONENT moves, count how many of
its legal moves leave us still in book. A book move of ours never ends the
book — we always have a reply to our own line — so opponent nodes are the
only thing that matters.

**Against the engine itself (`TestBookExitAgainstEngineOpponent`).** Play
the book out with the asm-matched mirror at depth 6 as the opponent, and
record the ply at which the book runs out and the engine's own evaluation
of the position it leaves us in.

**Against real recorded games (`TestBookCoverageAgainstPGN`, `BOOKPGN=…`).**
Replay an actual match archive and report where the book would have ended.

The verdict was blunt. **The book was 10 plies deep and one to three moves
wide.**

| | before |
|---|---|
| as Black, White first moves answered | **4 of 20** (e4, d4, c4, Nf3) |
| as White, Black replies answered after 1.e4 | 8 of 20 |
| … after 1.d4 / 1.c4 / 1.Nf3 | 3 / 2 / 1 of 20 |
| opponent replies covered at ply 1 (as White) | 14 of 80 (17.5%) |
| mean exit ply vs mirror @ d6, book as White | **2.50** |
| mean exit ply vs mirror @ d6, book as Black | **3.00** |
| eval at exit (our POV) | +9 cp White, −29 cp Black |
| mean exit ply over 40 recorded Sargon III games | 4.30 |

A 48-line, 10-ply-deep book was being abandoned on **move 2**. The mirror
answers 1.e4, 1.c4 and 1.Nf3 all with `1...Nc6`, which the book had never
heard of, and plays `2.Nc3` as White, which it had also never heard of. Of
40 recorded Sargon games, 4 left the book at **ply 1** — Sargon plays
`1.Nc3`.

The eval column is the useful negative: the positions the book left us in
were **fine** (+9 / −29 cp). The book was not leaving us badly placed. It
was just leaving. That is what made breadth, and not repair, the right
lever — exactly as the design brief predicted.

### 3. What was added

`openings.txt` goes from 48 lines to 179; the 48 main lines are untouched.
Two changes to the compiler made breadth affordable and safe:

**`/w` and `/b` — whose moves a line speaks for.** The book is a set of
positions, so a line written out in full teaches the engine both to play the
moves and to answer them. `A00: 1. g4 d5 2. Bg2 c6` would put the Grob in
the engine's own first-move lottery. `A00/b:` compiles only Black's moves.
Our own first-move repertoire is therefore still exactly e4/d4/c4/Nf3 while
the book answers all twenty legal first moves. `/w` does the mirror image
for junk defences we want to punish but never play.

**Name deduplication.** Lines whose `ECO Name` text matches share one name
ID and one stored string, so a dozen four-ply answers to junk first moves
can all honestly be `A00 Irregular Opening` for ~22 bytes once. Without it
the name table alone was 2,760 B of an 8,192 B budget; with it, 1,702 B.

**Transposition was the cheapest breadth in the file.** Because the book is
keyed on the position, a short line that reaches a position an earlier line
already established inherits its whole depth free. **41 of the 179 lines do
this** (`TestBookTranspositions` enumerates them from source, so the claim
cannot go stale). `B07: 1. d4 d6 2. e4 Nf6 3. Nc3 g6 4. Nf3 Bg7` costs four
plies and joins the Pirc.

### 4. The defect the measurement caught, and the tripwire that replaces it

The first draft of the breadth section measured **worse than the book it
replaced**: `+7 ± 12` Elo against a bookless opponent where the *old* book
scored `+35 ± 12`. Same engine, same budget, same 2,000 games. Widening the
book had cost 28 Elo.

The cause was not the new lines. It was the **weights**. A weight is only
"how many curated lines played this move from this position", so a four-ply
line added purely to have an answer ready for `1...h5` still bumped the
weight of its own first move, `1.e4`. Thirty such lines, and:

| our first move | before | first draft |
|---|---|---|
| 1.e4 | **65%** | **44%** |
| 1.d4 | 25% | 37% |
| 1.c4 | 6% | 10% |
| 1.Nf3 | 4% | 10% |

Nobody chose that. The engine's opening repertoire had been re-rolled as a
side effect of adding coverage, and it cost more than the coverage was
worth. Two rules now make it structurally impossible:

1. **A perspective-marked line is weightless** — it never increments an
   entry an earlier line established.
2. **`Build` REFUSES a marked line that would add a second move at a
   position the main lines already answer**, naming the line and the ply.
   Adding an alternative is a repertoire decision; it has to be made by
   dropping the suffix, deliberately and visibly.

With both in place the repertoire is **byte-identical to the old book** at
every node (`TestBookRepertoire` prints it), and the same match returns
`+37 ± 12`. Breadth lines answer; they do not advocate.

### 5. The coverage result

| | before | after |
|---|---|---|
| lines / entries / distinct positions | 48 / 312 / 264 | **179 / 633 / 585** |
| blob | 3,866 B (47.2%) | **7,407 B (90.4%), 785 B free** |
| our own repertoire (first move, replies) | — | **unchanged, verified** |
| as Black, White first moves answered | 4 of 20 | **20 of 20** |
| as White, Black replies after 1.e4 | 8 of 20 | **20 of 20** |
| … after 1.d4 | 3 of 20 | **20 of 20** |
| … after 1.c4 / 1.Nf3 | 2 / 1 of 20 | 10 / 8 of 20 |
| opponent replies covered at ply 1 (as White) | 14/80 (17.5%) | **58/80 (72.5%)** |
| mean exit ply vs mirror @ d6, as White | 2.50 | **5.00** |
| mean exit ply vs mirror @ d6, as Black | 3.00 | **9.00** |
| eval at exit (our POV) | +9 / −29 cp | +18 / −33 cp |
| mean exit ply over 40 recorded Sargon games | 4.30 | 4.80 |
| book moves played per 2,000 self-play games | 2,948 | **4,275 (+45%)** |

### 6. The Elo: measurement design first, number second

**An ordinary self-play SPRT cannot see a book change.** Both sides would
share one book, play identical opening moves, and the book would cancel
exactly. Worse, `internal/sprt` forces a 6-ply opening prefix, which plays
straight *through* the region the book is supposed to be choosing — a book
match run that way returns a beautiful null having measured nothing. Three
things were added to `internal/sprt` for this, and `Run` now **refuses** a
book match without the third:

| added | why |
|---|---|
| `BookA` / `BookB` (+ `BookEntry`, `BookSeed`) | per-side resident blobs, probed **on device** via `chesstest.AsmBookProbe` before any search, exactly as `ucibridge` does — one PRNG draw per probe hit or miss, and the probe's cycles are settled so the unspent allocation banks toward the first real search, which is part of what a book buys |
| `NoOpening` | start from the standard position; the books choose everything |
| `Dither` | fresh `SEED` per search, as the bridge does. Without it the engine is deterministic and a match whose only variety is the book's weighted pick replays a handful of games and reports a confidence interval it has not earned |
| `Result.ABookMoves` / `BBookMoves` | printed **before** the Elo. A book match where the two counts are zero, or where they are equal because neither book ever engaged, has measured nothing — and that failure is otherwise indistinguishable from a true null |

`cmd/sprt` gains `-bookA`, `-bookB`, `-book-seed`, `-lbl`, `-dither`
(`-bookA default` uses the embedded blob). `cmd/sargon-symmatch` gains
`-bookfile` for the same reason.

**Instrument validated before use.** Against a bookless opponent the new
book plays 4,275 book moves per 2,000 games where the old plays 2,948
(+45%) — the width is real and the rig can see it.

**The numbers** (self-play, 300 ms/move emulated, `0x5f`/`0x10`, per-game
bank, both sides adaptive, colours swapped):

| match | games | result |
|---|---|---|
| **new book vs old book (paired)** | **3,000** | **+3 ± 10 Elo**, LLR(0,10) −0.79 |
| new book vs no book | 2,000 | +37 ± 12 Elo, LLR(0,10) **+8.72 (accepts +10)** |
| old book vs no book | 2,000 | +35 ± 12 Elo, LLR(0,10) +8.17 (accepts +10) |
| first-draft book vs no book | 2,000 | +7 ± 12 Elo — the repertoire defect |

**The book is worth about +36 Elo. Widening it is worth +3 ± 10 — nothing
measurable.** The two independent bookless comparisons (37 and 35) agree
with the paired estimate (+3) to well inside their error bars, so this is
not one noisy run; it is the answer.

**Why no Sargon gauntlet.** It is the other honest instrument, and it was
deliberately not used for the *number*: at ~5 minutes a game a gauntlet
would need thousands of games to resolve a ±10 Elo effect, and a gauntlet is
unpaired on top of that. Sargon was used for **coverage** instead, where it
is decisive and cheap. `cmd/sargon-symmatch -bookfile` now exists for the
A/B if it is ever worth the days. Two things came out of it:

- Replaying the 40 surviving Sargon III games (`TestBookCoverageAgainstPGN`,
  `BOOKPGN=…`) and extracting **Sargon's own moves**: as White it opened
  `1.e4` in 20 of 20 games; as Black it answered `1.Nf3` with `1...d5`
  (11/11), `1.Nc3` with `1...d5` (4/4) and `1.d4` with `1...Nf6` (5/5). Four
  of those games left the old book at **ply 1** — Sargon plays `1.Nc3`,
  which the book had no answer to at all.
- A live standard-start game with the new book: 8fish played `1.d4`, Sargon
  answered `1...d6`, and the new breadth line `B07/w: 1. d4 d6 2. e4 Nf6
  3. Nc3 g6 4. Nf3 Bg7` — four plies that transpose into the Pirc main line
  already present under `1.e4 d6` — kept it in book for `d4`, `e4` and
  `Nc3`. With the old book `1...d6` was uncovered and the game left book on
  move 2. That is the mechanism working, on the real opponent, for four
  bytes of new line and no new depth.

### 7. Why the null, and what it does not say

Head to head, both sides played **exactly 15,594 book moves**. That is not
a coincidence: the book is a property of the *position*, so the moment
either side plays a searched move the resulting position is almost always
outside both books. Book-versus-book, the two leave together, and the extra
width is rarely exercised. The +45% only appears against a bookless
opponent — which is also the realistic case, and there the difference is
still only 37 vs 35.

**The honest reading is that breadth did not move the needle in self-play,
and that the ceiling was low to begin with**: the entire book is worth ~36
Elo, so no amount of widening could have shown more than a few. This is a
negative result and it is reported as one. The book was **not** widened for
free — it went from 47% to 90% of the resident hole — and the return on
those 3,541 bytes is, in self-play, statistically indistinguishable from
zero.

What the measurement **cannot** say is what the book is for. Self-play's
opponent is our own engine, which never plays `1.Nc3`, `1.b4` or `1...h5`;
against it the book's old four-move repertoire was never really tested. The
brief for this work was "stop you from doing something dumb and leave you
reasonably set up" against whatever turns up — a human on an Apple IIe,
Sargon III, an engine with its own ideas. The coverage numbers say that job
is now done (4 of 20 first moves answered → 20 of 20; the book survives to
ply 5.0/9.0 against the engine instead of 2.5/3.0). The Elo number says
self-play cannot tell the difference. Both are true.

**Recommendation: keep it.** It costs nothing the engine wanted (the
`$2000-$3FFF` hole has no other claimant, and the engine image is
byte-identical), it is not a regression (+3 ± 10, and +37 vs +35 against a
bookless opponent), the repertoire is provably unchanged, and it removes a
class of embarrassment — losing the thread on move one to `1.Nc3` — that
self-play was structurally unable to charge us for.

### 8. Gates

| gate | result |
|---|---|
| `TestBookProbeParityASMvsGo` (264 → 585 in-book positions) | **PASS** |
| `TestBookProbeWeightedDistribution` / `TestBookProbeOutOfBook` | PASS |
| `TestBookFollowThenSearchDriver`, `TestBridgeBookFollowThenSearch` | PASS |
| `TestBookIntegration` (on-device UI name walk) | PASS |
| `TestCompiledMatchesGeneratedLines`, `TestBlobSize` | PASS |
| `TestMoveStackWatermark` (new) | PASS — 487/1,152 slots |
| `asm/engine.bin` byte-identical | **yes** — only unused stat constants in `book.inc` changed, so `TestMicroAB` cannot have moved |
| `internal/sprt` package tests | PASS |

## 2026-07-28 — ★ THE SHIPPING BUILD NOW PLAYS, and it was showing FLASHING PUNCTUATION for half the pieces

Closing the gap between the UI we test and the UI we ship. Two things were
only ever verified adjacent to the real situation, and one of them was
wrong.

**★ THE FINDING: the shipping build never selected the IIe's alternate
character set.** `asm/ui.s`'s `PIECECH` encodes a black piece on a dark
square as `$60-$7F` — inverse lowercase, exactly as documented. That
encoding is correct **only on the ALTERNATE character set**. An Apple IIe
powers up on the **PRIMARY** set, where `$60-$7F` is **flashing
punctuation**. Nothing in `asm/m8.s` ever wrote `$C00F`. So on real
hardware, every black piece standing on a dark square — 8 of the 16 black
men in the start position — would have been a *blinking digit or bracket*,
and the inverse-video checkerboard would have been half missing.

Every byte on the screen was right. Every byte would have been displayed as
something else. The bytes were verified against the documented encoding;
the encoding's precondition was never verified, because the emulator did not
model the switch that establishes it.

Fixed in the SHIPPING BUILD, not the emulator: `m8main` now takes the
display in five stores — `80COL off` (ProDOS boots a IIe into 80 columns,
which would show this 40-column screen one column out of two), **`ALTCHARSET
on`**, `TEXT`, `NOMIX`, `PAGE1` (which also pulls `$0400-$07FF` back to MAIN
if the 80-column firmware left `80STORE` on). **+15 bytes**, all in
Language Card RAM; free budget 1,921 → 1,906 B. Engine untouched.

**The shipping build now plays a whole game through the real keyboard.**
`asm/m8.bin` — built WITHOUT `-D HARNESSKBD`, polling `$C000` for bit 7 and
clearing the strobe at `$C010` — played the same **75-ply game to
checkmate**, 193 keystrokes, 127.2 s of emulated IIe time. Previously it was
proven to boot, paint and block, and nothing else.

**Shipping and test builds are byte-identical on screen.** `TestShippingScreenParity` and `TestShippingFullGame` drive both builds in lockstep and
compare all 960 screen bytes **after every keystroke**. No divergence, at
any keystroke, in any test — including the engine's own moves, which are
seeded from the `ENTROPY` accumulator the keyboard feeds, so the two
keyboard paths collect identical entropy and the engine plays identical
chess either way. The two payloads differ by 3 bytes (4,222 vs 4,219): the
operands of `entkey`'s two loads, plus the `bit KBDSTRB` that clears the
strobe. `m8boot.bin` is byte-identical for both.

**goapple2 gained what made this checkable** (committed there, on `master`):

| added | what it is |
|---|---|
| `iie` keyboard | `$C000-$C00F` read the data latch, `$C010` clears the strobe and reports any-key-down; `KeyDown`/`KeyUp`/`KeyPress` inject; `KbdIdlePolls` distinguishes blocked-for-input from still-working |
| `iie` ALTCHARSET + display state | `$C00E`/`$C00F` write, `$C01E` read, plus 80COL and the `$C050-$C057` switches with their `$C01A-$C01D` status reads; reset state is 40-column primary-set text |
| `chargen` (new package) | the IIe character generator: screen byte + ALTCHARSET → the dots a IIe lights, for both character sets |

Deliberately **left** of the broader stage-2 retrofit: `80STORE` (a second
compare on the hottest path in the emulator for a switch nobody throws —
it stays loud in `Unhandled`), aux-bank/80-column video, slot ROM
switching, mouse/paddles/speaker, key auto-repeat, and the 32 MouseText
glyph shapes (`chargen` names MouseText but has no pixels for it and says
so rather than inventing them). The list is in `iie`'s package doc.

**Inverse video is now verified as dots, not bytes.** `TestInverseVideoPixels`
renders the board through `chargen` with the character set the image
actually selected, and asserts: all 128 board cells are inverse iff the
square is dark (by lit-dot count, not by byte value); a black knight on a
dark square is a lowercase `n` *cut out of a lit background*, dot for dot;
and **nothing anywhere on the 40x24 screen would flash or come out as
MouseText**. That last assertion is the one that fails if the `$C00F` store
is ever deleted.

### Gates

| gate | result |
|---|---|
| `TestShippingFullGame` (75 plies to checkmate, real keyboard, refereed by refchess, screen-compared to the harness build after each of 193 keystrokes) | **pass** |
| `TestShippingScreenParity` (68 keystrokes: moves, capture, castling, illegal move, takeback, level, help) | **pass** |
| `TestShippingEngineParity` (both builds play the same engine moves; `ENTROPY` identical) | **pass** |
| `TestShippingKeyboard` (echo, backspace, move applied, entropy folded) | **pass** |
| `TestInverseVideoPixels` / `TestPrimaryCharsetWouldBeWrong` | **pass** |
| `TestShippingImageBoots` (now also asserts ALTCHARSET) | **pass** |
| rest of `internal/ui` (19 gates), `harness`, `internal/entropy` | **pass** |
| goapple2 `iie` (incl. new `TestKeyboard`, `TestDisplaySwitches`), `chargen` | **pass** |
| `chesstest.TestMicroAB` | **pass**, fingerprint unchanged |
| `asm/engine.bin` | **`cmp`-identical** to a `git archive HEAD` build, md5 `58ef9645…` |

**Still not de-risked:** nobody has run this on a real Apple IIe. What
changed is that the emulator no longer *quietly agrees* with the shipping
image about the keyboard and the character set — it now disagrees where the
hardware would.

## 2026-07-28 — ★ MILESTONE: 8fish is PLAYABLE on an Apple IIe (asm/m8.s), and it cost the engine ZERO bytes

The on-device UI is built and a human can sit down and play: boot, choose a
side, see the board, type moves, the engine replies, and the game reaches a
real termination.

**Evidence, not assertion.** A complete **75-ply game ending in checkmate**,
driven keystroke-by-keystroke through the UI's own keyboard under the
harness (goapple2 IIe memory + go6502), refereed ply-by-ply by `refchess` —
127.2 s of emulated IIe time. A deterministic-opponent variant ended at 88
plies in a genuine **threefold repetition**, detected by the UI's own
1,024-byte hash history. `TestTerminations` additionally drives checkmate
(two ways), stalemate, the 50-move rule and threefold, each asserting the
resulting screen text.

**Byte budget (measured from label deltas, `TestUIByteBudget`):**

| component | bytes |
|---|---:|
| renderer / entropy | 508 / 56 |
| main loop, position bookkeeping | 111 / 164 |
| move generation + validation + legality | 333 |
| game state (mate/stalemate/50/repetition) | 105 |
| engine turn, limits+margin, ID driver | 147 / 296 / 267 |
| input+parse, commands, painting, think line | 349 / 230 / 470 / 280 |
| tables and strings | 888 |
| **UICODE total** | **4,207** |
| variables + history + hash history | 256 + 768 + 1,024 |
| **TOTAL of 8,176 B Language Card** | **6,255 (77%), 1,921 free** |

**It cost the engine nothing: 0 MAIN bytes, 0 TT entries, book unmoved.**
The Language Card discovery from the design pass is what made that possible.
Verified structurally, not just by comparison — the merge touched **no engine
core source at all** (engine.s, search.s, eval.s, board.s, movegen.s, tt.s,
tables.s, defs.inc, movegenbody.inc all untouched), and `TestMicroAB` passes
with a fingerprint byte-identical to a clean `git archive HEAD` build (GRAND
TOTAL CYCLES 3819284672 both sides), `asm/engine.bin` `cmp`-identical.

**Settled decisions honoured:** FEATURES2 on device = FT2_GENDEFER |
FT2_SOFTCLK (+ FT2_ADAPT for timed levels), deliberately NOT copied from the
harness config. The budget-octave margin rule is implemented in
`uimargin`/`uilimits` — octave shift-loop, table read, one 24×8
shift-and-add, no division — scaling the base and deriving CEILMAX/UNSTCEIL/
MINSPEND from the scaled value so all five limits share one margin, gated
against `chesstest.SoftClockMargin` at all 9 levels. No per-move
"thinking for N seconds" readout; row 14 shows depth/score/best move, which
are exact.

### Two findings worth keeping

1. **The design's `$0E00` payload address was wrong by construction.**
   `$0E00-$1FFF` is 4,608 B but the LC code budget is 5,888 B — so a UI that
   grew into its own budget would have BLOADed straight over the opening book
   at `$2000`. Moved to `$0900`, which makes the staging area and the budget
   the same size, so the error cannot recur silently. A latent
   corrupt-the-book bug that only appears once the UI is big enough.
2. **Parity has to compare piece-list bytes, not FENs.** The move generator
   walks the piece list, so a FEN round trip re-slots the pieces and yields a
   different — equally valid — search tree. That is a real property of the
   engine, not a test artifact, and anything comparing two positions for
   tree-identity has to respect it.

### Deferred, with prices (full table in docs/ui-design.md §12.6)

Board flip for a Black human (~40 B) · hi-res board (~1,850 B, fits) ·
insufficient-material draw (~50 B) · fivefold/75-move (~15 B) · mate distance
in the think line (~40 B) · FT2_ADAPT's per-game bank (~120 B) · FEN/position
setup (~250 B) · cursor entry (~200-260 B) · disk save (unpriced).

**★ NOT deferred so much as BLOCKED — real-hardware validation.** The IIe
model has no keyboard, so the shipping build is proven only to boot, paint,
and block in `entkey` with the entropy counter spinning. And `ALTCHARSET` is
not modelled, so inverse lowercase is verified as BYTES against the
documented IIe encoding, not as pixels. Everything above is measured on an
emulator; nobody has yet seen 8fish on a real Apple IIe.

> **Both emulator gaps were closed later the same day, and closing the
> second one found a real bug in the shipping build** — it never selected
> the alternate character set, so half the black pieces would have been
> flashing punctuation on hardware. See the entry at the top of this file.

## 2026-07-28 — THE ON-DEVICE UI IS PLAYABLE (8fish, `asm/m8.s`)

The design in `docs/ui-design.md` is built. You can boot it, choose a side,
see the board, type moves, have the engine reply, and reach a real game end.
`internal/ui` boots the REAL image the way the real machine will, types on
its keyboard and reads its screen back; `internal/refchess` referees every
ply.

**Headline evidence: a complete game, keystroke by keystroke, to checkmate.**
75 plies, White (a scripted depth-4 opponent) mating the UI's engine at level
1, 127.2 s of emulated IIe time, every ply cross-checked against refchess.
The final screen:

```
 0  | 8FISH 1.0    LEVEL 1     YOU ARE WHITE |
 2  | 8   R     k b   r    MOVES             |
 3  | 7           b p     26 g3e5 e7e6       |
 ...
12  |CHECKMATE            37 b8d6 e7e8       |
14  |D 2 -MATE e7e8       38 b1b8            |
17  |WHITE WINS - N STARTS A NEW GAME        |
23  |COMMAND?                                |
```

A second run with a deterministic opponent ended at 88 plies in a genuine
threefold repetition, detected by the UI's own 1,024-byte game hash history —
the thing the engine cannot do at the root, where `HASHSTK` is empty.

**Measured byte budget** — 4,207 B of code+static data plus 2,048 B of RAM
arrays and 256 B of variables = **6,255 of the Language Card's 8,176 B, 77%,
1,921 B free**. **MAIN cost 0 bytes**, TT cost 0 bytes, book unmoved. The
copier is 57 B at $0800, which the engine later overwrites with `PIECESQ`. The
design's *derived* estimate was ~4,011 B; the overrun is almost entirely
strings, the level/limit arithmetic and the ID driver.

**The engine is untouched.** `asm/engine.bin` is byte-identical to a build of
a clean tree (md5 `58ef9645…`, checked by `git archive HEAD` into a temp
directory and `cmp`), and `TestMicroAB` is green. The UI is a SEPARATE link
running from Language Card RAM at `$E000`, calling the engine by address
through `asm/engsyms.inc`, which `internal/engsyms` generates from
`asm/engine.lbl` against a fixed whitelist — so a renamed engine symbol is a
build failure, never a stale address.

**Parity gate for the UI's own ID driver.** `docs/ui-design.md` §7 has the UI
supply its own iterative-deepening loop so the screen can show the search
deepening without touching `search.s`. `TestEngineParity` proves that costs
nothing: at four positions and depths 2-5, the move the UI-driven engine
plays is the move the engine's ordinary `$4000` entry plays, given the same
position bytes, features, depth and dither seed. (The comparison has to hand
the reference the UI's PIECE-LIST bytes, not a FEN: the generator walks the
piece list, so a FEN round trip re-slots the pieces and yields a different —
equally valid — tree. That is a real property of this engine and worth
recording.)

**The safety-margin rule (§6.2) is implemented and gated.** Octave of the
budget, `KTAB` lookup, one 24x8 shift-and-add, no division; applied to the
BASE, with `CEILMAX`/`UNSTCEIL`/`MINSPEND` derived from the already-scaled
value so all five limits share ONE margin. `TestSoftClockLimits` checks every
level against `chesstest.SoftClockMargin`'s reference to within 1% (the
device rule is `x * K >> 8` with `K = 25600/margin`; the host rule is
`x * 100 / margin` — same margin, different rounding):

| level | control | margin | BUDGET | CEILMAX | UNSTCEIL | MINSPEND |
|---|---|---:|---:|---:|---:|---:|
| 1-4 | fixed depth 2/3/4/5 | — | 0 | — | — | — |
| 5 | 4 s | 127% | 12,581 | 50,324 | 37,743 | 3,145 |
| 6 | 8 s | 113% | 28,278 | 113,112 | 84,834 | 7,069 |
| 7 | 15 s | 100% | 59,794 | 239,176 | 179,382 | 14,948 |
| 8 | 30 s | 100% | 119,588 | 478,352 | 358,764 | 29,897 |
| 9 | 60 s | 100% | 239,176 | 956,704 | 717,528 | 59,794 |

(256-cycle units. `ABORTL` is derived on device and inherits the scaling.)

`FEATURES2` on device is `FT2_GENDEFER | FT2_SOFTCLK` = `$30`, plus
`FT2_ADAPT` for timed levels — NOT `ucibridge.runEngine`'s config, which
deliberately leaves `FT2_SOFTCLK` off because the harness has a real counter.
A level-5 (4 s) move ran on the ESTIMATED clock with the harness clock trap
DISABLED — hardware semantics — and spent 2.79 emulated seconds, i.e. it
stopped short of its allocation, which is the direction the estimator is
biased to fail in.

**Validation is the engine's own.** The UI contains no chess rules: it calls
`gennodef`, walks the list, and runs `search.s sdomove`'s legality test
verbatim. `TestLegalityAgainstRefchess` types every move refchess calls legal
at six positions (124 of them) and 240 illegal coordinate strings, checking
the resulting position against refchess each time — castling both ways, en
passant, both promotion spellings, pins and evasions all come out right with
zero UI code, because the generator already emits them.

**Cost of the interface, measured.** Full 40x24 repaint 23,659 cycles
(23.2 ms) after every move — 0.1% of a 30-second move — so there are no
partial repaints anywhere and the whole class of incremental-update bugs does
not exist. Cold boot (LC copy, LCCODE install, start position, legal-move
count, first paint) 154,280 cycles = 151 ms.

**What is deliberately NOT there:** a per-move "thinking for N seconds"
readout. The estimator's per-move RMS is ~27% and worse at phase 14-19; it
would be visibly wrong and would make the engine look broken. Row 14 shows
depth, score and the current best move instead, and those are exact. Full
deferral list with prices in `docs/ui-design.md` §12.5.

## 2026-07-27 — FT2_SOFTCLK ENABLEMENT DECISION: on-device YES, harness NO

Both controls independently re-verified on main, from source (see the
stale-binary near-miss below):

| control | soft adherence | exact adherence | spend A/B | Elo |
|---|---:|---:|---:|---:|
| 4000 ms, 400 games | 0.9429 | 0.9285 | 1.0149 | **−23 ± 27** |
| 15000 ms, 80 games | 0.8761 | 0.8859 | 0.9903 | **+30 ± 62** |

At equal compute the estimator is worth **about zero**, in the safe
direction (it stops early, it never flags). Both intervals contain zero and
they lean opposite ways.

**The decision, and the reason it is not "enable it everywhere":** the A/B
compares the estimator against the harness's EXACT clock — a reference that
does not exist on the target machine. On an Apple IIe there is no readable
clock at all, so the real choice on device is estimator vs. no time
management (fixed plies). On the harness the exact counter is available and
is strictly the better instrument.

So:
- **On device (the UI build): ENABLE.** It is how budget mode runs at all,
  and its measured price is ~0.
- **In the harness / gameplay config (ucibridge): LEAVE OFF.** Every screen
  and SPRT in this log was measured against the exact counter; switching the
  measurement rig to an estimator would add noise for no benefit and would
  fold the estimator's error into every future number. `Bridge.SoftClock`
  and the sprt `-a2/-b2` path exist precisely so the estimator can be
  A/B'd deliberately rather than inherited silently.

This also preserves the exact-clock path permanently, which has now caught
three separate defects nothing else could: the accumulator-priming bug (first
128 nodes of every search uncharged), the game-vs-pool miscalibration, and
the stale-binary near-miss below.

**Still weak, recorded not hidden:** the cost MODEL remains budget-dependent
even though the SPEND no longer is (4 s fits `4715 + 41.4×phase`, 15 s
`3708 + 47.5×phase`, 22.6% apart at phase 10). The margin absorbs the
consequence for time management but does not make the model right — a
per-move estimate is ~5% low at 4 s and ~2% high at 15 s, and anything
reading it as a cycle count inherits that. Phase 14-19 is still the worst
bucket and per-move RMS is ~27%, so **a per-move "thinking for N seconds"
display would be visibly wrong** and should not be built on this. The real
fix remains the missing regressor the mechanism points at (makes per node,
priced at ~0.29% of runtime).

**★ A near-miss worth institutionalising.** The first re-run of the 4 s A/B
reported "+28 ± 26 with 29% more spend" — the original bug's exact
signature. The cause was a stale compiled `cmd/sprt` binary: built before the
margin moved, so it linked the old non-scaling `SetBudget` while loading the
NEW `engine.bin` off disk at runtime, giving margin 100% everywhere. **The Go
tool and the 6502 image are versioned separately and nothing forces them to
agree.** Run matches with `go run ./cmd/sprt`, not a cached binary. What
caught it was the adherence gate, because `go test` always recompiles: it
said 0.9447 while the stale binary said 1.2012.

## 2026-07-27 — ★ FT2_SOFTCLK RECALIBRATED under game conditions, and the safety margin MOVED OFF THE COST TABLE: adherence 1.171 → **0.941**, spend parity at **both** 4 s and 15 s, honest Elo **−23 ± 27**

The pool gate was measuring the wrong POSITIONS *and* the wrong QUANTITY.

Fixes the entry below. Three things came out of it: a mechanism (with the
leading hypothesis falsified), a recalibration, and a replacement gate.

### 1. The mechanism — measured, not assumed

Every isolation below is at the shipped config, 4000 ms/move, with the
ORIGINAL cost table, so the numbers are comparable to the failure.

**(a) The transposition table is NOT the cause.** The leading hypothesis was
that pool searches start from a cold TT while a game carries a warm one
across moves in the aux bank. Ran the game loop with the TT DELIBERATELY
DROPPED between moves (`sprt.Config.ColdTT`), which turns a game into a
sequence of pool-like cold searches and changes nothing else:

| arm | estimate/truth | adherence |
|---|---:|---:|
| games, warm TT (as shipped) | 0.871 | 1.188 |
| games, TT dropped every move | **0.856** | 1.169 |

TT warmth is worth **−1.5%** — and it is the *wrong sign* to explain a
21-point gap. Hypothesis falsified.

**(b) It is the POSITIONS, and it is nearly all of it.** Same protocol on
both sides — one budgeted search per FEN, cold TT, no game context, i.e.
exactly what `TestSoftClockAccuracy` does — run over the pool's own 71 FENs
and over 525 FENs the engine actually visited in self-play:

| position set (identical protocol) | estimate/truth | adherence | cycles/node | makes/node |
|---|---:|---:|---:|---:|
| the pool's own 71 starts | **1.071** | 0.942 | 3819 | **1.211** |
| 525 game-visited positions | **0.844** | 1.171 | 4291 | **1.576** |

The pool is 40 opening starts plus 31 curated endgames, 47 of them at taper
phase 20-24 — the full-material shape the cost model was fit on, and the one
place it is right. A position the engine actually reaches makes **30% more
moves per node**, and cycles-per-node tracks makes-per-node almost exactly.
At MATCHED sampled phase 20-23 it is pool 4065 vs game 4729 cycles/node
(makes/node 1.202 vs 1.336); at phase 0-3, 3230 vs 3659 (1.177 vs 1.418). So
this is **not a phase-distribution artifact** — it is the same 18-20% at
every phase, and the phase regressor cannot see it. The extra makes are check
evasions and illegal-move rejections, which quiet balanced positions do not
have.

**(c) Ruled out, quantified anyway.** The halfmove clock (game positions have
a live repetition scan; every pool FEN is forced to `0 1`): forcing game FENs
to halfmove 0 moves estimate/truth 0.844 → 0.850, and pool FENs to halfmove
30 moves 1.071 → 1.059 — **~1%**. The banked income: both sides of the
failing A/B were FLAT (non-adaptive), so `pokeAlloc` gave every move exactly
`income` and the bank never fed back — **0%**. Book moves: the harness enters
at `$4000` and never reaches `bookentry` — **0%**.

**(d) The entry prime is 12-13% of every estimate**, because a 4 s move is
only ~7 polls. That is why the pool reads 1.071 while its cost model is
really ~0.95 of truth at its own phases. Correct — it charges the 128 nodes
before the first poll — but it means short searches are dominated by a
constant.

**★ (e) The SECOND cause, and the one that makes estimate/truth the wrong
quantity entirely.** Refit the table on game data so that in-game
estimate/truth = **0.9873** — a very nearly perfect clock — and the engine
STILL overran, adherence **1.119** against the exact clock's 0.921. Paired on
525 identical positions, soft clock vs exact clock:

    soft/exact total cycles 1.2213
    mean completed depth    2.752 vs 2.615
    took an extra iteration 86 positions;  one fewer  16 positions

The estimate is consumed by a THRESHOLD — `idloop` starts iteration d+1 only
if `now + 2*cost(d)` fits the budget — and a threshold converts symmetric
clock noise into ASYMMETRIC SPEND, because an extra iteration costs 2-6x what
stopping early saves. Quantifying it as
`amp = adherence_soft / (adherence_exact / (estimate/truth))`, i.e. spend
beyond what a perfectly-proportional clock would produce:

| budget | amp |
|---|---:|
| 4000 ms (typical completed depth 2-3) | **1.24** |
| 15000 ms (typical completed depth 4-5) | **1.00** |

It is a short-budget effect because the payoff asymmetry is depth-dependent:
at depth 2→3 the next iteration is 3-6x the last, at depth 4→5 nearer 2-3x.
**No unbiased cost model can fix this.** The clock has to be biased.

### 2. The recalibration

Same structure — `128 × cost(PHASE)` into the 24-bit accumulator at
`checkclocks`, one indexed fetch, table built at assembly time. Nothing new
in the hot path. Two changes to what the table contains:

**Fit on real games, both level lengths.** 3120 moves played through the SPRT
match loop (warm TT, real per-move allocation) at 4 s AND 15 s, fit against
the exact vector of cost-table lookups each move made — the per-move phase
HISTOGRAM plus the root-phase prime — so the fit reproduces the on-device
estimate to the byte instead of approximating it through a per-move mean.
Relative least squares, the loss a budget cares about.

    raw fit: cost/node = 3437.3 + 79.7 x min(phase, 14)     (flat above 14)
    relative RMS 16.5% over 3120 moves

Two segments, not a line, because the game-condition curve is concave —
cycles/node climbs steeply to about phase 14-15 and then flattens (the free
25-entry fit agrees; a straight line is ~10% wrong at both ends). The knee
costs nothing: `.repeat`/`.if` at assembly time.

**A deliberate margin, kept separate from the fit.** `SOFTMARGIN = 127%`
(percent, applied in the table expression) is POLICY, not physics: it is the
correction for (e), and it is calibrated on ADHERENCE, not on estimate/truth.
Measured at 4000 ms, 40 games per point:

| margin | in-game adherence | estimate/truth |
|---|---:|---:|
| 100% | 1.119 | 0.99 |
| 115% | 0.993 | 1.10 |
| 130% | 0.936 | 1.26 |
| 145% | 0.887 | 1.42 |
| *exact clock* | *0.921* | — |

### 3. The result

| control | soft adherence | exact adherence | soft/exact spend |
|---|---:|---:|---:|
| **4000 ms** (2 × 3400 moves) | **0.9413** | 0.9228 | **1.020** |
| **15000 ms** (2 × 3300 moves) | **0.8805** | 0.8603 | **1.023** |

(The 15 s row was 0.6836 / **0.795** until the margin moved off the cost
table — see §4a. That fix is the difference between "20% of a long level's
allocation thrown away" and spend parity.)

At the 4 s control the estimator now lands just under its allocation and
within 2% of what the exact clock spends — which is the only setting at which
an Elo A/B measures the estimator instead of a compute advantage.

**The A/B, re-run. SPEND FIRST, because that is the whole lesson:**

    -a 0x5f -a2 0x30 -b 0x5f -b2 0x10 -pergame -budget 4000 -pairs 200
    A(soft) adherence 0.9429   B(exact) adherence 0.9285
    equal-total-spend A/B = 1.0149   (A used 1.49% more)
    +109 =156 -135   score 46.8%   elo -23 +/- 27   llr(0,10) -1.51

Compare with the run this supersedes: A/B spend **1.2601** and "+29 ± 26".
Now that the two sides spend the same compute to within 1.5%, the Elo is
readable, and it reads **−23 ± 27** — i.e. the estimator costs somewhere
between nothing and about 25 Elo, and certainly does not WIN anything. That
is the expected sign: at equal total spend, a clock with 40% per-move RMS
allocates its time worse than a perfect one. The 1.5% residual spend
advantage is worth ~+2 Elo, so the bias-corrected figure is nearer −25.

**And the same A/B at the 15 s control**, which the budget-indexed margin
(§4a) is what made worth running at all — before it, side A was handicapped
20% on compute and the Elo would have been meaningless:

    -budget 15000 -pairs 40 (80 games)
    A(soft) adherence 0.8761   B(exact) adherence 0.8859
    equal-total-spend A/B = 0.9903   (A used 0.97% LESS)
    +29 =29 -22   score 54.4%   elo +30 +/- 62

Spend within 1%. The Elo is not significant at 80 games (the interval spans
−32 to +92) and is quoted only to show it is not a disaster; the point of the
run is the spend column.

**Taken together: at equal compute the estimator is worth about zero** — −23 ±
27 at 4 s, +30 ± 62 at 15 s. It is a way to run budget mode on hardware at all,
not a source of strength.

**This is what the feature actually costs.** The earlier +29 was 26% extra
compute (~+43 Elo of expected gain) with the estimator contributing nothing.

**Note what "adherence 0.95-1.00" can and cannot mean here.** The EXACT clock
only reaches 0.921: the predictive gate leaves ~8% of the budget unspent by
design. So "adherence 1.00 with equal spend" is not a reachable pair, and the
target that matters is parity with the exact clock.

### 4a. ★ The margin moved OFF the cost table — which is what fixed 15 s

The first cut of this recalibration folded the safety margin into the cost
table at assembly time and reported the 15 s deficit as unfixable: "the
estimator has no depth or budget regressor, so one table cannot serve both."
That was true of the table. It was not true of the FEATURE, and the reason is
one line of algebra.

Every use of the estimate is a comparison `estimate >= limit`. The estimate is
a plain sum of cost-table entries, and every limit (BUDGET, ABORTL, CEILMAX,
UNSTCEIL, MINSPEND) is host-poked once per move. So

    m * raw_estimate >= limit      <=>      raw_estimate >= limit / m

is the SAME COMPARISON, and doing it on the limit side is free: the limits are
installed at move setup, outside the hot path. Folded into the table, m had to
be one constant for every level length. On the budget side it can be any
function of the budget at **zero engine cycles and zero engine bytes**.

**Verified as an identity, not asserted.** `TestSoftClockMarginEquivalence`
runs the same positions two ways — raw table with a halved budget, and a
doubled table with the full budget — and requires the SEARCHES to be identical:
same completed depth, same move, same score, same true cycle count, and an
estimate exactly 2x. It uses m = 2 so both sides are exact in integer
arithmetic; the shipped margins are worth a fraction of a percent of rounding
drift, which is priced and accepted.

**Confirmed end to end, three times.** At 4 s the margin is unchanged (127%),
so every game-level measurement had to reproduce — and each did, to the last
digit:

| measurement | before | after |
|---|---|---|
| diag adherence, 4 s | 0.9413 / 3468 moves | **0.9413 / 3468 moves** |
| `TestSoftClockAdherence` | 0.9447 / 0.9107 / 1.0373 | **0.9447 / 0.9107 / 1.0373** |
| A/B, 4000 ms, 400 games | +109 =156 -135, −23 ± 27, spend 1.0149 | **identical** |

400 complete games, same result, with the margin living somewhere else.

**Also: the cost model is honest again.** With the margin out of the table,
`TestSoftClockAccuracy` reads pool estimate/truth **1.050** (it was 1.332), so
the number that test reports is once more a statement about the cost model
rather than about a policy knob folded into it. Its assertion is correspondingly
two-sided again.

**THE RULE**, stated for the on-device UI to reimplement, indexed by the
OCTAVE of the budget (the top set bit of the budget in 256-cycle units):

| budget | margin | poked BUDGET |
|---|---:|---|
| ≤ ~8 s | 127% | `BUDGET × 202 >> 8` |
| ~8-16 s | 113% | `BUDGET × 227 >> 8` |
| > ~16 s | 100% | unchanged |

A 6502 needs a shift loop to find the top bit, one table read and one 24×8
shift-and-add multiply, once per move. **No division on device.** The two
anchors are measured (4 s and 15 s, against an exact-clock control); the
8-16 s entry is interpolated and the ends are held flat. Below ~2 s the engine
cannot spend less than its first two iterations, so the margin is powerless
there and the safe direction is to leave it high.

Two things had to be got right and are worth flagging for anyone touching
this. **All five limits scale, not just BUDGET** — the engine compares
CLOCK_TRAP against BUDGET, ABORTL (derived on device as 2×BUDGET or 2×CEILMAX)
and the three FT2_ADAPT ceilings, so the scaling lives inside
`chesstest.SetBudget` and `SetAdaptive`, the only two functions that write
them, rather than at ~50 call sites. And **all limits for one move must share
ONE margin**, which is why `SetAdaptive` now takes the move's base allocation
explicitly: letting CEILMAX (up to 4× base) pick its own octave would quietly
loosen the adaptive ceiling relative to the budget.

**A side benefit worth having:** with a raw table the engine's own `spent`
figure is an honest estimate of true elapsed cycles again (in-game
estimate/truth 0.935 at 4 s, 0.976 at 15 s, against 1.19/1.26 before). A
banked clock on hardware — which can only settle on the engine's own opinion —
now settles in approximately real units instead of inflated ones.

### 4. ★ Where it is still weakest

**The cost MODEL is still budget-dependent, even though the spend no longer
is.** Fit separately, 4 s data gives `4715 + 41.4×phase` and 15 s data gives
`3708 + 47.5×phase` — a **22.6% difference at phase 10**, because deeper trees
take far more TT cutoffs and a TT-cutoff node is counted but nearly free. The
estimator has no depth or budget regressor, so one table cannot serve both;
the shipped table is the joint fit and splits the difference. §4a's
budget-indexed margin absorbs the consequence for time management — spend is
now within 2.3% of the exact clock at both controls — but it does not make the
model right. A per-move estimate is still ~5% low at 4 s and ~2% high at 15 s,
and anything that reads the estimate as a cycle count inherits that. The real
fix is the regressor the mechanism in (b) points at: makes per node, priced at
~0.29% of runtime and refused.

**Taper phase 14-19 is the worst bucket, and the pool's n=3 warning was
right.** With real samples (n=406 at 4 s, not 3) it is the only phase bucket
whose adherence exceeds 1.0:

| root phase | n | estimate/truth | adherence | cycles/node |
|---|---:|---:|---:|---:|
| 0-3 | 148 | 1.212 | 0.802 | 4777 |
| 4-7 | 324 | 1.252 | 0.832 | 4799 |
| 8-13 | 366 | 1.209 | 0.993 | 5158 |
| **14-19** | **406** | **1.122** | **1.075** | **6060** |
| 20-24 | 592 | 1.247 | 0.942 | 5556 |

Cycles/node PEAKS at phase 14-19 and falls again at full material, so the
monotone-non-decreasing table under-prices exactly there. An unconstrained
fit does pick that shape up (knee 18, then falling) but buys only 0.12 points
of RMS over 3120 moves, so it was not taken; the bucket stays the known weak
spot. The single pool position that produced the original −51.7% (the
Kaufman-style rook sac) turns out not to be a phase story at all: it makes
**3.5 moves per node** against the pool's 1.21.

**Per-move error is large and that is by design.** Bias +21%, RMS 40%, p10
−9%, p90 +48% at 4 s. A single move is a bad estimate; the GAME total is the
guarantee. Anything that reads the estimate per move (a "thinking for N
seconds" display) will be visibly wrong.

### 5. The gate changed

`sprt.TestSoftClockAdherence` — in-game adherence against an exact-clock
control, measured the way `cmd/sprt` measures it. It asserts adherence ≤ 1.00
(the forfeit test the pool test could not make), ≥ 0.85, and soft/exact spend
in [0.90, 1.10]. Running it (12 games/arm, 4000 ms, ~35 s):

    SOFT  clock: adherence 0.9447
    EXACT clock: adherence 0.9107
    equal-total-spend soft/exact = 1.0373

`ucibridge.TestBridgeSoftClock`, the eleven-move banked session at 3 s/move,
moves from **1.102 x income to 0.870 x income** against a harness-clock
control at 0.997 — i.e. from running 10% long to running 13% short, which is
the whole point of the sign change.

`chesstest.TestSoftClockAccuracy` is DEMOTED to a diagnostic and says so in
its own comment; its assertion is now one-sided (the pool is the easy case,
so the estimator must not read LOW there) and its phase-bucket check is
relative to the pool ratio rather than to 1.0, so the deliberate margin
neither trips nor masks it. `TestSoftClockCalibrate` is kept as the record of
the procedure that failed, labelled as such.

New: `cmd/softclkdiag` (the game-condition measurement rig: per-move traces,
the TT/halfmove/position isolations above, and `-fit`), plus
`sprt.Config.MoveTrace`/`ProbeAddrs`/`ColdTT`, all inert when unset.
`TestSoftClockMarginEquivalence` and `TestSoftClockMarginRule` pin §4a.

**★ A near-miss worth writing down.** The first re-run of the 4 s A/B after
§4a reported "+28 ± 26 with 29% more spend" — the original bug's signature,
exactly. The cause was a STALE TOOL BINARY: `cmd/sprt` had been compiled
before the margin moved, so it linked the old (no-scaling) `SetBudget` while
loading the NEW raw `engine.bin` off disk at runtime, giving margin 100% at
every budget. The Go tool and the 6502 image are versioned separately and
nothing forces them to agree. Rebuilt, it reproduced the previous result
digit-for-digit. Two lessons: run these from source (`go run ./cmd/sprt`)
rather than from a cached binary, and note that the thing which caught it was
the adherence gate — `go test` always recompiles, so it said 0.9447 while the
stale binary said 1.2012. The 15 s A/B was unaffected and did not need
re-running: the rule prescribes margin 100% there, which is byte-for-byte what
an unscaled poke does.

**Unchanged:** estimator cost **+0.0073%** (`TestSoftClockNoTreeEffect`,
trees bit-identical on/off), image size 31642 B, feature-OFF is still the
identical instruction stream. `TestMicroAB`, `TestIDIterationParity`,
`TestTTSequenceParity`, `TestFullGameMirrorParity`, `TestBudgetModeParity`,
`TestSearchMirrorParity`, `TestGenDeferTreeIdentity` all PASS.

One diagnostic assertion had to be repaired rather than merely re-aimed: the
sub-resolution bound in `TestSoftClockAccuracy` compared the estimate against
TRUTH, so a deliberately biased-high table failed it by construction. It now
compares against the run's own pool ratio (one sampling period of slack), so
it still catches the entry prime going missing or doubled — the bug it was
written for — without asserting a bias the calibration no longer has. Its
hardcoded quantum (`128 × 3250`) was stale too; it is now derived from the
SOFT* constants in defs.inc, which `chesstest.ParseDefs` cannot see because
they are decimal rather than `$hex`.

**Asked and answered, separately from the calibration: "don't multiply by 128
and do a 24-bit add; keep the accumulator in 128-cycle units and do a 17-bit
add."** That is already the code. There is no runtime multiply — the ×128 and
the conversion to CLOCK_TRAP's units are both folded into the table by
`.repeat` at assembly time — and the third byte is already `bcc`/`inc`, not an
`adc`. Rescaling the accumulator from 256-cycle to 128-cycle units assembles
to the IDENTICAL instruction stream: **0 cycles, 0 bytes.** It would buy one
bit of table rounding (~0.04% against a 17% RMS) and cost a dual-units rule
for BUDGET at five host call sites, because $BFF4 is the harness's REAL
256-cycle counter whenever FT2_SOFTCLK is clear. Not taken; the reasoning is
recorded at the instruction in `search.s` so it is not re-proposed.

## 2026-07-27 — FT2_SOFTCLK Elo A/B: **DO NOT ENABLE.** It "wins" +29 ± 26 by OVERRUNNING its clock 17%, and the static accuracy test had the bias BACKWARDS (superseded above)

The deciding measurement for the entry below. Soft clock vs exact clock,
both sides otherwise the shipped config, **per-GAME banked** mode (the
honest one: the bank is debited REAL cycles, so an engine whose estimate
runs short cannot launder the overrun — which is what a tournament clock
would do to it on hardware).

    A(0x5f/a2 0x30 soft) vs B(0x5f/a2 0x10 exact) @ 4000ms, 400 games
    +132 =169 -99   score 54.1%   elo +29 +/- 26   llr(0,10) 1.37
    spend: A_total=88,200,069,492  intended=75,337,200,000  adherence=1.1707
           B_total=69,997,151,425  intended=75,300,480,000  adherence=0.9296
           equal-total-spend A/B = 1.2601  (A used 26.01% MORE)

**The +29 Elo is an artifact, not a result.** The soft-clock side used
**26% more total cycles** than its opponent. At the project's measured
130-150 Elo/doubling, 26% more compute is ~0.33 doublings ≈ +43 Elo of
expected gain — so the observed +29 is fully explained by the extra
compute, with nothing left over to credit the estimator. Read as
"FT2_SOFTCLK is worth +29 Elo" this would be one of the worst overclaims
in this log; the spend accounting is the only reason it is not.

**On real hardware this is not a bonus, it is a forfeit.** A 17% overrun
against a tournament clock means flagging. The engine believes it still
has time when it does not.

**★ And the static accuracy test had the SIGN BACKWARDS.**
`TestSoftClockAccuracy` measured aggregate est/truth = **1.052**, i.e. the
estimator OVERSTATES elapsed time, which would make the engine stop EARLY
and spend LESS (adherence < 1). In actual games it spends **17% MORE**, so
in game conditions the estimator runs LOW. The two measurements disagree
in direction, not merely in magnitude.

So the 284-position pool test is **not representative of game conditions**
and must not be trusted as the estimator's acceptance gate. Most likely
mechanism (to be confirmed, not assumed): the pool searches each start
from a comparatively cold TT, while a real game carries a warm TT across
moves via the aux bank, changing the node mix the per-node cost model is
supposed to price. This is the same class of instrument defect as the
budgeted-ID divergence and the endgame cost mispricing — a measurement
that looked clean because it was measuring the wrong situation.

**Actions taken:** FT2_SOFTCLK stays OFF everywhere. The estimator needs
recalibration under GAME conditions (warm TT, banked income), and the
acceptance gate must be adherence-in-game, not pool est/truth. Re-measure
after. The feature bit, the identical-instruction-stream OFF path, and the
harness's exact-clock mode all stay — this is a calibration failure, not a
design failure, and the exact-clock path is what caught it.

**Also note the safe direction:** overestimating elapsed time is benign
(the engine stops early and plays slightly weaker), underestimating is
not (it overruns and, on hardware, loses on time). The recalibration
should be deliberately biased to overestimate.

## 2026-07-27 — the FT2_SOFTCLK build: an on-device elapsed-time ESTIMATOR, **+0.0073% cycles**, aggregate estimate/truth **1.052** (superseded above)

The blocker filed with the UI design that morning: `checkclock` reads
`CLOCK_TRAP` (`$BFF4`), which is a live cycle counter **only under the
harness**. On a IIe it is plain RAM and the machine has no readable clock at
all — no RTC, no readable video counters (that is the IIgs), VBL needs polling
by code that is busy searching, the paddle timer is a 3 ms one-shot. So budget
mode could not run on hardware, levels had to ship as raw plies, and
`FT2_ADAPT` — validated and measured — could not run at all.

**The decision was to do what Sargon III does: estimate.** Sargon has no clock
either and counts toggles of its "thinking" asterisk (docs/sargon.md). This is
the same concession, with a calibrated cost model behind it.

**Mechanism — 125 B of CODE and one table lookup.** `checkclock` already fires
once per 128 nodes. Behind `FT2_SOFTCLK` ($20) the poll enters one instruction
earlier, at `checkclocks`, which adds `128 × cost(PHASE)` to a 24-bit
accumulator and falls through into the ordinary poll:

    lda PHASE / cmp #25 / bcc + / lda #24 / + tax      ; clamp (promotions)
    clc / lda CLOCK_TRAP / adc PCOSTLO,x / sta CLOCK_TRAP
          lda CLOCK_TRAP+1 / adc PCOSTHI,x / sta CLOCK_TRAP+1
          bcc + / inc CLOCK_TRAP+2

**The accumulator IS `$BFF4`**, so **not one clock-read site changes** — the
three reads in `checkclock`, the six in the `idloop` predictive gate, the three
in `adaptmaybe`'s easy-stop. On hardware those reads see this accumulator; under
the harness they see the real counter. That is the whole integration.

**Feature-OFF is the identical instruction stream, not merely equivalent.**
`engine.s` patches the operand of search's one `ccsite: jsr checkclock` to
`checkclocks` only when the bit is set (CODE is ordinary RAM at $4000; the entry
already block-copies LCCODE). A run with the bit clear executes byte-for-byte
today's engine at today's cost — which is what makes the A/B measure the
estimator and nothing else.

**Cycles, not raw nodes**, deliberately. Per-node cost varies ~2.5× by phase, so
a node budget would make "30 seconds" mean materially different things by phase
— and would drift worst exactly where `FT2_ADAPT` is trying to allocate
cleverly. Phase is the regressor because the mirror's recalibration that morning
already showed it beats piece count *and* is free at runtime (both engines
maintain it incrementally).

**One regressor, per the brief — and the measurement says one is enough.**
Candidate models, fit against the 69-search microAB ground truth:

| model | RMS | worst | CV RMS |
|---|---:|---:|---:|
| nodes only (constant per node) | 17.6% | 44.1% | 18.1% |
| **nodes × (a + b·phase)** | **13.8%** | **35.9%** | **14.5%** |
| + a make counter | 9.6% | 23.7% | 9.9% |
| full 4-regressor mirror model | 7.3% | 20.3% | 7.8% |

A make counter would buy ~4 points of RMS for a 16-bit increment in `make`
itself: `inc lo / bne + / inc hi` is ~8 cycles, and at the measured 1.2 makes
per node that is ~9.7 cycles/node against ~3.3k — **~0.29%, forty times the
whole estimator's cost**. Not taken. (Those figures are the microAB fit across three
feature masks; the shipped-config fit below does better because it is one
config and uses the sampled regressors directly.)

**Calibrated with the engine as its own instrument.** `TestSoftClockCalibrate`
runs each of the 23 calibration positions twice at fixed depth with the cost
table overwritten — all entries 1 (so `$BFF4` ends at the POLL COUNT) and
entry[p] = p (so it ends at the SAMPLED PHASE SUM) — and regresses true cycles
onto those two columns, minimizing relative error. So the regressors are
measured exactly as the runtime forms them, sampling and 128-node quantization
included, at the **shipped** config (0x5F + FT2_GENDEFER) rather than at the
0x1F/0x07/0x00 masks the mirror's model was fit at. Result:

    cost/node = 3250.1 + 61.90 × phase        (SOFTA 52001, SOFTB 990, SOFTSCALE 16)
    in-sample RMS 9.5%, worst 23.3%, pool actual/predicted 0.990

**A bug the measurement caught, worth its own line.** The first build primed the
accumulator at zero. `NODECNT` starts at 0, so the first poll lands on node 256
and charges nodes 129-256 — nodes 1-128 were never charged. Every search read
~0.4 s short: a **−41.0% bias at a 1 s/move budget**, and sub-256-node searches
estimated a flat ZERO (−100%). Fixed by priming `$BFF4` with one table entry at
`PHASE` in the entry block. Derivation would not have found this; the harness
did, immediately.

**Accuracy — the gate (`TestSoftClockAccuracy`).** Budget mode, shipped config,
harness `$BFF4` read trap DISABLED so the engine runs exactly as a IIe would,
stopping itself on its own estimate, while `m.Cycles` reports what it really
spent. **Closed loop**: the estimate is scored against the tree the estimate
itself produced. 71 positions × 4 budgets = 284 searches, an INDEPENDENT pool
from the 23 the coefficients were fit on.

*Resolution first, because it dominates the tail.* One poll = 128 nodes =
**0.41 s (phase 0) to 0.59 s (phase 24)**. 66 of the 284 searches finished
below 2M cycles (~2 s) — mate-stops and one-legal-move positions — and no
estimator with this sampling period can score them; they are reported in
absolute terms (worst **−792k cycles = −0.78 s**, ~1.3 sampling periods) and
excluded from the percentages.

| resolvable searches (n=218) | bias | RMS | p10 | p50 | p90 | **worst** | pool est/true |
|---|---:|---:|---:|---:|---:|---:|---:|
| all | +6.5% | 17.8% | −11.3% | +5.7% | +23.9% | **+52.6%** | 1.052 |
| budget 1M (~1 s) | −0.5% | 15.6% | −20.3% | +1.4% | +15.3% | −49.3% | 0.974 |
| budget 4M (~4 s) | +4.9% | 20.3% | −17.9% | +3.4% | +26.2% | +52.6% | 1.060 |
| budget 15M (~15 s) | +11.9% | 20.9% | −4.2% | +9.6% | +33.7% | +52.6% | 1.091 |
| budget 60M (~59 s) | +5.6% | **11.8%** | −7.2% | +5.7% | +17.5% | +29.3% | 1.044 |
| phase 0-3 (pawn/minor) | −0.0% | 12.7% | −17.5% | +1.2% | +13.9% | −41.4% | 1.029 |
| phase 4-7 | +8.2% | 15.2% | −7.4% | +8.6% | +23.5% | +23.9% | 1.037 |
| phase 8-13 (n=4) | +4.1% | 9.0% | | | | +16.7% | 0.995 |
| phase 14-19 (n=3) | −38.2% | 39.7% | | | | −51.7% | 0.698 |
| phase 20-24 (middlegame) | +8.7% | 18.6% | −8.1% | +8.1% | +29.3% | +52.6% | 1.066 |

**Pool estimate/truth over ALL 284 searches: 1.052.** That is the number that
governs GAME length, not the worst case: `BankedClock` settles every move in
ESTIMATED units, so a game's total telescopes to sum(income) in estimated
cycles and the real-time drift is the estimator's mean BIAS. (`ucibridge` now
settles the bank on `res.spent` — the engine's own figure when `SoftClock` is
on — rather than on the harness counter, so a hardware-mode game cannot launder
the estimator's error out of its own clock.)

**Whole-game check** (`ucibridge.TestBridgeSoftClock`): eleven Ruy Lopez
positions at 3 s/move with banking, run twice — once entirely on the estimate
with the harness `$BFF4` trap disabled, once on the exact counter as the
control. Real cycles burned over the session:

    soft clock      37,108,824   = 1.102 x income
    harness clock   33,584,114   = 0.997 x income

So **a whole game on the estimated clock runs ~10% long**, against a control
that lands on its income to 0.3%. That is the honest per-game figure: a
30-minute game becomes ~33 minutes. It is worse than the 1.052 pool ratio
because the two arms' banks diverge and the estimate's per-move error feeds
back into the next move's allocation — small sample (11 moves), and the
direction is the one to watch on a real tournament clock.

**★ WHERE IT IS WEAKEST — by completed depth, which is the real explanatory
variable:**

| completed depth | n | bias | RMS | worst | pool |
|---|---:|---:|---:|---:|---:|
| 1 | 17 | +2.5% | 15.8% | −49.3% | 0.989 |
| 2 | 39 | +0.5% | 17.8% | −51.7% | 1.031 |
| **3** | **64** | **+16.3%** | **24.9%** | **+52.6%** | **1.146** |
| 4 | 50 | +2.8% | 11.1% | −41.4% | 1.027 |
| 5 | 23 | +6.8% | 13.3% | +27.3% | 1.085 |
| 6 | 12 | +4.1% | 12.5% | +24.3% | 1.076 |
| ≥7 | 13 | −3.1% | 7.8% | −13.5% | 0.977 |

Every one of the twelve worst searches completes at depth 1-3. Restricted to
the 98 searches that reach depth ≥ 4 the estimator is **RMS 11.5%, pool
1.040** overall, and **RMS 9.8-11.0% with pool 0.939/1.038/1.042** at budgets
4M/15M/60M. The weakness is therefore SHALLOW
searches, not endgames: at depth ≤ 3 the tree is small, quiescence-heavy, and
its full-width/QS node mix is nothing like the deep searches the coefficients
were fit on, while the phase term — the only thing the estimator can see — is
constant across the whole opening. That is also why the 15 s column is the
worst budget (+9.1% pool): it is the budget that most often lands the engine on
a depth-3 iteration in an opening position. The 60 s column, where depth ≥ 4
dominates, is the best (RMS 11.8%).

The `phase 14-19` row (pool 0.698) is n=3 — one tactical position (`1k1r4/…`,
the Kaufman-style rook sac) at three budgets, where the engine spent 4.4M
against a 4M budget while believing it had spent 2.1M. It is a real 10%
overrun, and it is a single position; nothing about the phase bucket is
implied.

**Cost — measured, not derived.** Fixed-depth ON/OFF A/B over 6 positions
spanning phase 0 to 24 (`TestSoftClockNoTreeEffect`), on trees proven identical
in the same run: **+0.0073% overall**, per position +0.0056% … +0.0101%. That
matches the instruction-level account: 32 cycles per 128 nodes = 0.25 cycles per
node against ~3.3k. The same test asserts the two-sided property that matters —
search/make/eval/attacked/ttprobe/generate entry counts, score and move all
identical with the bit set and clear, which is what would catch `checkclocks`
clobbering a live register (it takes X, dead at search's entry) even though the
clobber only happens on 1 node in 128.

**Space.** CODE size $3A59 → $3AD6, **+125 B** (75 B of code, 50 B of table).
The image did **not** grow — still 31642 B of 32752, headroom 1110 B — but only
because `TABLES` is `align $100` and CODE ended at $7A59 with 167 B of
alignment slack before $7B00. **42 B of that slack remain**, so the next CODE
addition pays full price out of the 1110. No Language Card space was taken; the
UI's ~4,011 B claim on $E000-$FFEF is untouched.

**Gates.** `TestSoftClockNoTreeEffect` PASS (trees identical, cost 0.0073%).
`TestSoftClockAccuracy` PASS. `TestBridgeSoftClock` PASS (whole game on the
estimate). `TestMicroAB` PASS (fingerprints unchanged).
`TestIDIterationParity`, `TestTTSequenceParity`, `TestFullGameMirrorParity`,
`TestBudgetModeParity`, `TestSearchMirrorParity`, `TestGenDeferTreeIdentity` all
PASS — as they must, since every one of them runs the bit CLEAR and therefore
the identical instruction stream.

**Not enabled anywhere by default, deliberately.** Every screen and SPRT in this
log was measured against the harness's exact counter; folding a 5%-biased,
18%-RMS clock into all future numbers silently would be the wrong trade. The
flag exists so the estimator's error can be priced as Elo when someone wants to
(`ucibridge.Bridge.SoftClock` runs a whole match on it). What it unblocks
immediately is the thing it was built for: **budget mode, timed levels and
`FT2_ADAPT` now run on real hardware.**

**Priced but not built**, in case a blitz level is ever wanted: polling every 32
nodes instead of 128 quarters the resolution to ~0.10-0.15 s at a cost of ~1.45
cycles/node ≈ 0.044% (from the same instruction-level account as the measured
0.0073%). It needs `checkclocks` to rearm `NODECNT` itself and jump past
`checkclock`'s own rearm — 3 extra bytes, still zero when the bit is clear. At
the level lengths a 1 MHz machine actually offers (a 1 s move is ~310 nodes),
128 is the right divider and the 0.41-0.59 s quantum is below the shortest
sensible level.

## 2026-07-27 — Deferred move generation SHIPPED (FT2_GENDEFER): **−2.52% cycles**, bit-identical tree, 412 B

The build of the entry below. `snode` no longer generates the move list
when the TT offers a move: `ttmovevalid` validates the move against the
board, it is staged as a real 4-byte move-stack record at `PLYBASE[PLY]`
and searched, and `generate` runs only if it fails to cut. Behind
`FT2_GENDEFER` ($10 in FEATURES2); OFF is today's path.

**ENABLED IN THE SHIPPED GAMEPLAY CONFIG** (`ucibridge.runEngine` now sets
FEATURES2 = FT2_GENDEFER) after independent re-verification of every gate:
`TestTTMoveValidExhaustive` PASS with non-vacuous coverage (792 accepted,
66 captures, 64 double pushes, 3 en passant, 164 pawn moves, 38 rejected
promo/castle), `TestTTMoveValidRejectsPromoAndCastle` PASS,
`TestGenDeferVerify` PASS, `TestGenDeferTreeIdentity` PASS (24 A/B pairs,
all identical), `TestMicroAB` PASS, `TestMicroABPhase` PASS. Adopted with
NO SPRT, deliberately: the tree is bit-identical, so there is nothing for a
game-playing test to resolve that the fingerprint does not already settle.

`TestProfileR5` now profiles at FEATURES2 = FT2_GENDEFER as well, so round
5 aims at shares that still exist. Generation's share moved as predicted:
movegenbody.inc **26.0% → 24.5%**, leaving board.s the largest quarter at
27.4% (search.s 23.1%, eval.s 21.9%). Note the profile's per-run totals are
NOT the way to read the saving — it is a cycle-budget run, so faster code
buys more work rather than a smaller total. The saving is measured on
identical trees, where it ranges −0.74% to −2.83% per position at ID depth
6 and −2.52% at the shipped control.

**Measured saving −2.52%** (`TestGenDeferCycleDelta`) on the r5 profile
position set at the shipped mask, per position deepened under real
iterative deepening until it passed the ~30M-cycle control — against the
predicted +1.9..+2.5%. Per phase, prediction in brackets: endgame 4.01%
[3.8-4.1], opening 2.18% [3.5-3.6], midgame 1.80% [1.35], tactical 1.38%
[1.55]. `generate` calls fall 8-26% per position.

**The measurement regime matters and nearly went wrong — twice.**
(1) Fixed-depth mode is ONE iteration against a COLD TT, where almost no
node has a TT move to defer on: over the MicroAB fixed-depth tiers the
saving is 0.96%/0.08%/0.24% (masks 0x1F/0x07/0x00). Every cycle number
that counts is therefore taken under real ID, with the budget SATURATED
(2e9) so the ID driver's predictive gate never fires and both A and B run
identical trees; a real time budget cannot be used at all, since it
converts the saving into extra nodes and the trees diverge by
construction. (2) Depth must be calibrated to the CONTROL. MicroAB's
positions at depth 6 cost 180-600M cycles — 6-20x the shipped ~30M — and
there the ID-tier saving is only 1.17%, because a tree that big thrashes
the 4096-entry TT and the per-node TT-move availability falls. Deepening
each r5 position only until it passes 30M gives 2.52%. Same code, same
positions, 2x apart on depth choice alone.

**Tree identity** (`TestGenDeferTreeIdentity`, the acceptance test — no
SPRT): over the MicroAB suite at its three fixed-depth tiers PLUS an ID
tier at the shipped mask, the FNV hash of the (FROM,TO,MVFLAGS) stream at
every `make`, the score, the best move and the search/make/eval/ttprobe
counts are identical ON vs OFF. `generate` and `attacked` are excluded
and are the only things that move: `attacked` falls with `generate`
because castle emission calls `gcsafe2` (two `attacked` scans per
castle-eligible node).

**`ttmovevalid` is proved exactly as strict as `generate`**, which is the
whole risk (tt.s indexes on 12 bits and verifies 24, so a hit against a
different position survives with p = 2^-20 — and the eager generation was
the only thing catching it). `TestTTMoveValidExhaustive` compares the
routine against `generate`'s actual output over **all 128x128 (from,to)
pairs** — off-board square codes included — for 32 corpus positions, in
both the "is it a move" and the "which flags" directions. Not sampled: a
2^-20 mode cannot be. The ONLY disagreements are the two deliberate
rejections (promotions, castles), and the test asserts they are exactly
that set. A mutation making pawn pushes accept an occupied target is
caught immediately.

**`ca65 -D GDVERIFY`** (`TestGenDeferVerify`) is the live CKVERIFY-style
assertion: it generates the list anyway at every staged node and exits 102
if the validated move is absent. Worth recording honestly: that gate did
NOT catch the pawn-push mutation the exhaustive test caught in one run —
the TT only ever offers moves that are real in *some* position, so a live
gate can only catch the 2^-20 case. It is a long-run assertion, not a
substitute for the exhaustive test.

**Implementation notes.** Staging the move as a real move-stack record
(tier byte 0, the same "consumed by pass 0" marking pass 0 writes today)
is what keeps `setmove4`, the `sdemote` scout refetch, the killer flag
read and the root `BESTFLAGS` read working unchanged. `PASSNO[PLY]` =
`PASS_TTDEF` ($80) while it is live; `sloopret` dispatches it on one
`bpl`, so the hot pass-4 scan grew no compare. When it fails to cut,
`srdefer` generates at `PLYBASE+4` (the staged record stays at PLYBASE so
`spop`'s MSP restore is unchanged) and a consume scan zeroes the
generated copy's tier before joining the normal passes at `p0done` —
exactly one entry can match, because promotions (the only repeated
from/to) are rejected.

Promotions and castles fall through to normal generation, as the entry
below required: replaying a promotion would change the tree (pass 0
searches all four variants N,B,R,Q), and a castle is rejected for free —
`ATTACKTAB` has no `ATK_KING` bit for a two-file king step, so the
geometry test declines it with no extra code.

**Cost: 412 B of CODE** (ttmovevalid 213, staging 70, srdefer + consume
scan 105, `snode`/`sloopret` restructure ~24). MAIN headroom 1622 → 1110 B
(the extra 100 is TABLES' 256-byte alignment). Feature OFF costs ~7 cycles
per full-width node for the gate — 0.03% of total, and symmetric, so it
does not flatter the A/B.

Gates, all green: `TestTTMoveValidExhaustive` (32 positions x 16384 pairs;
coverage asserted to include captures, pawn moves, double pushes, en
passant and rejected promos/castles), `TestTTMoveValidRejectsPromoAndCastle`,
`TestGenDeferVerify`, `TestGenDeferTreeIdentity` (24 A/B pairs, all
identical), `TestGenDeferAsmCost` (the pre-existing diagnostic still
runs), `TestMicroAB` at FEATURES2 = 0 — all 18 fingerprints identical to
the pre-change image, cycles +0.036% — and the parity suite:
`TestFullGameMirrorParity` (780/780 exact, 436 positions, 7.78M nodes),
`TestIDIterationParity` (399 iterations, 0 divergences),
`TestSearchMirrorParity`, `TestTTSequenceParity` (150,041 TT operations),
`TestBudgetModeParity`. Parity runs at FEATURES2 = 0, which with ON ≡ OFF
gives ON ≡ mirror transitively.
## 2026-07-27 — Cycle-cost model: the endgame over-pricing is FIXED (phase-aware per-node cost, recalibrated on 23 positions)

The mirror's cycle-cost model charged a **constant** cost per node, which
over-priced low-material nodes by 30-40%. This was known and carved out
rather than fixed: `TestCycleModelFrozen` excluded every FEN starting `8/`
from its per-search accuracy check. Fixed now — the carve-out is **gone**
and endgames are held to the same bound as everything else.

**Instrument.** New `TestMicroABPhase` (internal/chesstest) = TestMicroAB
plus material instrumentation: at every search/make/eval probe it also
accumulates the live piece count, and at every search entry the engine's own
PHASE byte. Those sums make a per-node cost of `Node + NodePhase*phase`
a linear model in measurable columns.

**Calibration set: 6 positions -> 23** (69 searches over masks
0x1F/0x07/0x00). The old set was five 30-32-piece middlegames plus one
10-piece endgame — a material term fit from that would have been fit to one
outlier. Added 17 positions at 3, 4, 4, 4, 4, 8, 8, 10, 12, 12, 16, 16, 20,
21, 23, 24, 28 pieces, with piece count and phase deliberately
non-collinear. Sparse positions are searched deeper so every search is 5M-2.5G
cycles (depth is not a regressor; the model is per operation).

**Ground truth had also gone stale**: the asm is **7-14% faster** than at
the previous calibration (23 asm commits), with op counts unchanged on 15 of
the 18 old rows. Everything was re-measured.

**New model** (fit non-negative, through the origin, minimizing RELATIVE
error — the set spans 4.8M..2.5G cycles/search, so unweighted least squares
would let the midgame searches set every coefficient, which is how the old
model came to be 30% wrong on endgames and still "fit"):

    Node 0 | NodePhase 44 (x the node's taper phase) | Make 1585 | Eval 888 | TTProbe 1824

| | old model, old truth | old model, new truth | old FORM refit | **new model** |
|---|---:|---:|---:|---:|
| actual/pred, <=12 pieces | **0.714** | 0.488 | 0.980 | **0.993** |
| actual/pred, rest | 1.014 | 0.821 | 1.077 | **1.019** |
| worst per-search err | 46.9% | 168.8% | 28.2% | **20.4%** |
| RMS rel err | — | 75.3% | 10.2% | **7.3%** |
| leave-one-position-out CV RMS | — | — | 10.8% | **7.8%** |

Per-position, old vs new (err% at 1f/07/00, new ground truth):

    3pc  KPvK                +134/+132/+128   ->   -3/ -1/ -2
    4pc  KP vs KR            +109/+169/ +64   ->   +5/ +9/+11
    8pc  pawn ending          +95/+122/+141   ->   -6/ -1/ +4
    10pc the old endgame      +57/ +45/ +48   ->   -9/ -2/ -3
    16pc rook ending          +80/ +72/ +67   ->   -7/+10/+15
    24pc queenless middlegame +23/ +29/ +16   ->   -7/ +4/ -4
    32pc full middlegame      +12/ +16/  -4   ->   -7/+20/-13

**Phase, not piece count**, and that is the physical result: the per-node
work that scales with material is slider ray walks in `attacked()` and how
many moves a piece emits, which phase (N=B=1,R=2,Q=4) weights and pawns
barely affect. Piece count fits worse (CV 8.0% vs 7.8%, worst 22.4% vs
20.3%) and would cost a 32-slot scan per node; phase is already maintained
incrementally by both engines, so the fix is free at runtime.

**Two mispricings, not one.** Separating them honestly: comparing like with
like (the original six positions at their original depths) the material
defect is the +45..+57% endgame error against -4..+30% on middlegames. The
rest of the blow-up on deep sparse searches is a SECOND defect the extended
set exposed — `TTProbe` was fitted at **9637** cycles/full-width node, an
artifact of every old position having ttprobe/search ~ 0.08. Searches
spanning 0.03..0.54 refit it at **1824**, which the round-5 profile
independently supports (tt.s is 1.5% of all cycles).

**Independent corroboration of the whole coefficient split.** The round-5
per-file profile and the fitted model, on the same mask-0x1F searches:
eval 21.3% profile vs 21.0% fitted; board.s+movegen 54.4% vs make 53.0%;
search.s+tt.s 24.2% vs node+phase+ttprobe 22.2%. Two independent
instruments, ~2 points apart.

**End-to-end validation on an INDEPENDENT pool** — `TestBudgetModeParity`,
284 budget-mode positions (2 configs x 2 budgets x 71 starts) at FEATURES
0x5F and 0x1F, comparing the mirror's `Cyc.Est` against the asm's real
emulated cycles on identical trees. This is the instrument that filed the
defect (the 0.776 / 0.999 numbers). Same gate, same positions, only the cost
table changed:

| | OLD table | NEW table |
|---|---:|---:|
| spend ratio (asm/mirror Est), openings pool | 0.999 | 1.073 |
| spend ratio, **endgame/near-mate** | **0.776** | **1.023** |
| ... p10 | 0.530 | 0.886 |
| completed depth exact, all 284 | 82.4% | **93.3%** |
| completed depth exact, endgame subset | 71.8% | **92.7%** |
| endgame depth skew (asm-deeper − mirror-deeper) | **+35 / −0** | **−5** |

The two subsets used to disagree by 22 points and now agree to 5. The
remaining ~5-7% level offset (mirror slightly UNDER-charges) is expected:
the model is fit at masks 0x1F/0x07/0x00 and half this gate runs the shipped
0x5F, whose check extensions the model never saw. **A second carve-out fell with the first**: that
gate's one-sided depth-skew assertion was scoped to the openings pool
precisely because of this pricing bias; it now asserts on the endgame subset
too.

**Transfer preserved** (the property the reduced 4-regressor form existed
for). `TestEvalTermTax` stays green: the rook term's mirror-side cycle
fraction is 4.75% against an asm ground truth of 4.78-5.17% — better
centred than before (4.17% vs 3.47%). New `TestPhaseTransfers` checks the
new regressor specifically: the mirror reproduces the asm's node count
EXACTLY on all 69 searches and its mean phase per node matches to 0.00%.
(The old "mirror QS is 0.9x-24x the asm's" caveat is stale; full-width
TT-probe parity is 1.00 on all 69.)

**Frozen-test guards, all with NO carve-out**: every search within 21%
(worst 20.4%, and it is a 32-piece MIDGAME — the residual is no longer
material-structured), per-mask grand total within 8% (was 12%), and both
material ratios within 5% of 1.0 — the last is the guard that would have
caught the original defect.

**Impact on past verdicts: no recorded verdict is known to flip, and where
the bias had a direction it FLATTERED the features that were rejected
anyway.** Two things are worth separating.

*Common-mode.* Every screen charges both sides A and B from the same cost
table, and screens compare A vs B at one budget. A uniformly inflated
endgame node price therefore mostly cancels: both engines simply searched
less once material came off. That is a shift in how much search a screen
bought in endgame positions, not an A-vs-B distortion, and nothing here
changes an A-vs-B sign that was outside its error bar.

*Where it did NOT cancel: the size of a feature's tax.* A screen taxes a
feature by adding cycles per operation; what the feature actually pays is
that tax as a FRACTION of the per-node cost. Inflating the endgame per-node
cost shrinks that fraction, so a feature whose cost is charged mainly in
endgame positions was **under-taxed** — screened too favorably, not too
harshly. Measured directly (`TestEndgameCosted`, identical trees, only
the cost table changed): the endgame-terms tax of 438 cyc/gated-eval was
**4.84% / 5.05% / 3.94%** of estimated cycles on its three endgame probe
positions under the old table and is **9.62% / 8.59% / 6.02%** under the
new one — the term was screened paying **1.5-2.0x less** than its own cost
model intended. That compounds with the already-documented 3x under-charge
of the term's per-eval cost itself (438 charged vs 1278 real).

That is exactly the feature whose screen did not survive: the endgame
technique (`internal/mirror/endgame.go`) screened **+10 ± 9**, was ported,
then measured **−9 ± 24** over 600 asm SPRT games and was stripped from the
image. The 2026-07-26 entry blamed the 3x per-eval under-charge; this defect
was pushing in the SAME direction on top of it. Correcting it makes endgame-specialist features screen slightly *worse*, so
no rejected feature is owed a re-screen on account of this bug — a
re-screen would only make the verdicts more negative. The reverse case
(a feature unfairly rejected because the model over-charged it) would
require a feature whose costs are paid in endgames and whose benefit is
elsewhere; there is no such feature in the log.

*Budget constants.* These now mean more search than they used to (~15% more
per midgame node, far more per endgame node), so a `CycleBudget` copied from
an older screen is not the same amount of work. Comparisons within a screen
are unaffected; comparisons of node counts ACROSS the recalibration are not.

## 2026-07-27 — Deferred move generation at `snode`: measured GO (net ≈ +2.0% cycles, +3.6..+5.5 Elo) — and today's eager generation is silently doing safety work

Round 5's structural lever, priced before building. Instruments:
`TestGenDeferAsmCost` (internal/chesstest) and `TestGenDeferOpportunity` /
`TestGenDeferSelfPlay` (internal/mirror), all diagnostic; mirror counters
sit behind `GenDeferCount`, default false, and `TestGenDeferCountersAreNoOp`
pins that. No asm was modified.

**The opportunity**, measured twice — in the mirror and, as a cross-check,
directly in the asm (counting `stop0` = no TT move, and `scut` with
`PASSNO[PLY]==0` = cut on the TT move). The two agree exactly on midgame,
opening and tactical; they differ only on the endgames, where the mirror's
cycle model stopped one ID iteration earlier.

| | 6 FENs (asm direct) | 432 self-play moves (mirror) |
|---|---|---|
| full-width nodes | 7,357 | 359,567 |
| TT move available | 22.3% | 18.2% |
| → cut on the TT move alone | 86.3% | 83.5% |
| **⇒ generation entirely avoidable** | **19.3%** of FW nodes | **15.2%** |
| TT move *missing* from the list | — | **0 of 65,368** |

Both at the shipped config and the shipped ~30M-cycle control. At a
10×-smaller budget the opportunity roughly halves (8.0%) — the effect is
TT-warmth-driven, so the 30M numbers are the ones that count. **QS is
N/A**: QS capture nodes never probe the TT in either engine (`squiesce`
sets `TTFROMA = NOSQ`), so there is no short-circuit to defer against.

**The price of full-width generation** (exact per-PC attribution over
disjoint link-map ranges; the shared emitter region is the only estimated
split and moves the headline by ≤0.6pp): `gennodef` is **14.68%** of all
cycles, **3,114 cyc/call**. Both generators together are 28.83%, which
cross-checks the r5 rollup's 26.0% + 1.6% (the 0.39% wrapper lands in
search.s there). Crucially, cost/call is strongly phase-dependent —
endgame 1,957 vs tactical 4,885 — and **the phases with the most avoidable
nodes have the cheapest generation**, so the naive `14.68% × 19.3%` is
wrong and was not used.

**★ The cost side contains the real finding: validation is mandatory, and
today the eager generation IS the validator.** `asm/tt.s` indexes on 12
bits and `ttfetch` verifies 24 more — the union is the full 32-bit Zobrist,
so a hit against a *different* position survives with p = 2⁻²⁰. At ~800-1,200
probes/move that is **≈ one wrong-position TT move per 25 games**.
Measured: 0 of 65,368 hits, exactly as 2⁻²⁰ predicts. That is the shape of
a defect that passes every unit test and every short parity run and then
corrupts the board in a gauntlet days later. Right now pass 0 simply fails
to find such a move; delete the generation and that protection goes with
it.

`ttmovevalid` priced at instruction level against the existing
`ATTACKTAB`/`DELTATAB` tables and `atgeo` ray walk: knight 144 cyc, slider
~199, pawn ~100, king ~84, weighted by the measured mover mix to **≈133
cyc**, budget 150-250 with move-record staging. Cost is paid at *all*
TT-available nodes but saves only at the 86.3% that cut — accounted.

**Net: +2.43..+2.54%** on the profile workload, **+1.91..+1.99%**
re-weighted to self-play. Per phase: endgame +3.8..+4.1%, opening
+3.5..+3.6%, tactical +1.55%, midgame +1.35%. At the project's measured
130-150 Elo/doubling that is **+3.6 to +5.5 Elo**. For scale, the
emit-fusion this project merged was −1.92% cycles.

**Condition making it tree-identical (and so MicroAB-gated, not
SPRT-gated): reject promotions and castling**, falling through to normal
generation. The TT stores from/to only, and pass 0 today searches all four
promo variants in list order N,B,R,Q — so the "promotion" the TT cutter
searches today is the **knight** promo, and replaying a queen promo would
change the tree. Castling needs two `attacked()` scans to validate.
Rejecting both costs 0.07pp of the saving and buys a bit-identical tree.
En passant and double pushes reconstruct exactly and are safe.

**Adopted gates before any merge:** an exhaustive equivalence test over all
128×128 (from,to) pairs per corpus position asserting `ttmovevalid` agrees
with membership in `generate()`'s output, plus a CKVERIFY-style debug
variant that generates anyway and traps on disagreement. A 2⁻²⁰ failure
mode cannot be gated by sampling.

## 2026-07-27 — UI design + PoC: the Language Card was never used (8,176 B free), and a REAL hardware blocker found

Design doc: `docs/ui-design.md`. PoC: `asm/ui.s` + `asm/uitest.s` +
`internal/ui/`. Engine untouched — `engine.bin` md5 `3902502c` byte-identical
across the merge, `TestMicroAB` green.

**Recommendation adopted:** 40-column text page 1, inverse-video
checkerboard, typed coordinate move entry (`e2e4`) validated against the
engine's own generator, fixed-depth levels, progress printed between ID
iterations from the UI's own driver loop — so `search.s` is not touched.

**The resource finding: this project has never used its Language Card.**
The engine latches `$C08B` at entry and uses just 65 bytes of it (`LCCODE`
at `$D000`). `$E000-$FFEF` is **8,176 bytes of ordinary, directly
executable RAM** in the normal address space — no banking, no aux
primitives. The UI lives there, so it costs the engine **nothing**: 0 MAIN
bytes, 0 TT entries, book stays at `$2000`, the 1,622 B MAIN headroom is
untouched and no space-reclamation pass is needed. Measured PoC: board
repaint **4,410 cycles (4.32 ms)**, full 40×24 repaint **23,659 cycles
(23.2 ms)** — by difference over N vs N+1000 repaints, so no
timer-resolution error. Budget ~4,011 B of 8,176, leaving 51% headroom.

Note for optimization work: this is >5× the MAIN headroom, and at least one
optimization was previously rejected **on space** (colour-specialised PSQT
needed 3,072 B against 1,622 B available). Whether search tables can live
in LC is now worth asking — with the UI's own claim on the space counted.

Hi-res was priced, not dismissed: 28-pixel byte-aligned squares cost
~1,850 B and ~80,000 cycles and would also fit in LC (book moving to LC
bank 2 behind one `$C083`/`$C08B` pair), but mixed mode leaves only 4 text
rows, losing the simultaneous move list + opening name + thinking readout
that 40-col text affords — which is exactly why Sargon needs ESC to
toggle. DHGR is rejected outright: it eats aux `$2000-$3FFF`, **25% of the
transposition table**.

**★ THE BLOCKER: the Apple IIe has no readable clock, so budget mode
cannot run on real hardware.** `checkclock` reads `CLOCK_TRAP` (`$BFF4`),
which is a live counter only under the harness; on hardware it is plain
RAM. There is no substitute — no RTC, no readable video counters (that is
the IIgs), VBL needs polling by code that is busy searching, and the paddle
timer is a 3 ms one-shot. Consequences: levels must ship as **plies**, and
**`FT2_ADAPT` — a validated, measured feature — cannot run at all**, being
budget-mode-only with host-computed ceilings.

This has been latent all along: every match result in this log was measured
under a harness that supplies a clock the target hardware does not have.
It does not invalidate those Elo numbers (both engines were measured on the
same instrument) but it does mean the shipped on-device engine is not yet
the engine we benchmarked. Proposed fix, priced but NOT yet applied because
it is a `search.s` hot-path change: give the engine a **node** budget —
`checkclock` already runs once per 128 nodes, so a 24-bit increment there
is **13 bytes and ~0.004%** (derived), and replacing the three `CLOCK_TRAP`
reads is byte-neutral. Nodes/sec on a 1 MHz 6502 is near-constant, so a
node budget *is* a time budget, and it is the currency `internal/mirror`
already uses. Filed as its own task; decide before shipping, since it
changes the level menu and how 8fish compares to Sargon's timed levels.

Secondary risks recorded in the design doc: goapple2 carries the ][+
character ROM and does not model `ALTCHARSET`, so inverse lowercase is
verified as *bytes* against the documented IIe encoding rather than pixels
(32-byte table swap is the fallback); and `$FFFA-$FFFF` are RAM once LC
read is enabled, so the UI must write sane vectors.

## 2026-07-27 — ★ THE MULTI-ITERATION TT DIVERGENCE IS CLOSED: it was the tt.s unsigned mate-zone compare, already fixed. Two new gates now cover the hole.

The last unexplained asm↔mirror divergence — "past ~depth 6, in
multi-iteration ID, same best move and same score but DIFFERENT node/make
counts, present even at mask 0x00" (recorded during the 2026-07-21
aspiration port, never root-caused) — is **not open, and not a design
difference. It was the asm/tt.s UNSIGNED mate-zone compare** (`cmp #$74`
on the score's high byte, ply-shifting every NEGATIVE stored score),
fixed 2026-07-26 in ec9646a/4228474.

**Current state: EXACT.** Both engines were run through real iterative
deepening (1,2,…,N) at a budget large enough that only the depth cap
stops them, and compared iteration by iteration:

| gate | coverage | result |
|---|---|---|
| TestIDIterationParity (default) | 66 (mask, position) pairs, masks 0x00/0x1f/0x5f, caps 5/7/7, **399 completed iterations** | 0 tree, 0 move, 0 score divergences |
| same, deepened to cap 9 | 54 pairs, 362 iterations | 0 divergences |
| TestTTSequenceParity (default) | ID depth 5, all three masks: **150,041 TT operations** (89,380 stores, 10,793 probe hits) | every operation identical |
| same, ID depth 6 | **389,276 TT operations** (228,146 stores, 31,704 probe hits) | identical in op, hash, ply, move, ply-adjusted score, and packed depth\|bound |

**Proof it was the tt.s bug, not something still live.** Reintroducing
that exact defect on the MIRROR side (unsigned `hi >= $74` in
ttstore/ttprobe, nothing else changed) reproduces the reported signature
to the letter (4 of 12 probed mask×position pairs go dirty at cap 6):

```
plain-0x1f  8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1   DIVERGES at iteration 6
    iter 1: asm n=19    mk=34    | mirror n=19    mk=34
    iter 2: asm n=122   mk=187   | mirror n=122   mk=187
    iter 3: asm n=355   mk=485   | mirror n=355   mk=485
    iter 4: asm n=571   mk=661   | mirror n=571   mk=661
    iter 5: asm n=590   mk=693   | mirror n=590   mk=693
    iter 6: asm n=2246  mk=2712  | mirror n=2395  mk=2882   <<<
    asm b4f4/96 d6 | mirror b4f4/96 d6        (same move, same score)
```

Iterations 1–5 are byte-identical and iteration 6 is not; the root move
and score still agree; it reproduces at mask **0x00**, where the TT is the
only state carried between iterations (778,956 vs 779,022 nodes on the
0x00 middlegame row — a 0.008% drift). At the shipped 0x5f mask it bites
one iteration EARLIER (iteration 5) — which is exactly why the aspiration
harness's second recorded finding ("at 0x1f|FT_CKEXT the multi-iteration
ID trees diverge in make count, same move, same score … present on 41 of
50 ckext rows" at maxD 5, filed as an unexplained check-extension/TT
interaction) fired at ckext while 0x1f looked clean at that cap. That was
**the same bug, one iteration earlier**, not a second defect. Both
historical findings are now accounted for by one cause, and the ckext
finding can be struck.

**Why every gate missed it, and what now doesn't.** TestFullGameMirrorParity
is a single `iterate` at the cap (no TT survives into a later iteration)
AND runs at depth 4 by default — blind twice over. TestBudgetModeParity
runs real ID but compares only final totals on the subset where both sides
happened to buy the same depth, with the cycle model's ±12% deciding that
subset. The new gates close both holes:

- **TestIDIterationParity** — real ID on both engines; cumulative
  node/make/eval counts must match **exactly after every completed
  iteration** of the common prefix, so the cycle model's stop decision
  can neither mask nor manufacture a divergence. Masks 0x00/0x1f/0x5f.
  Vacuity-guarded: ≥80% of pairs must compare ≥4 iterations, and a
  one-iteration run is only accepted when BOTH engines took the
  winning-mate stop.
- **TestTTSequenceParity** — every TT operation, in order, with operands,
  diffed against the asm probed at `tthit`/`ttfmiss`/`tsgo`. Sensitivity
  measured: with the historical bug reintroduced it fails on operation
  **#1** of some positions (`asm store … score=-88` vs `mirror … score=-87`
  at ply 1) instead of at iteration 6 of a 780k-node search.

No asm change was needed and none was made. The mirror needed no
behavioural change either — only two nil-default test taps
(`Engine.IterHook`, `Engine.TTHook`).
## 2026-07-27 — Deep optimization round 5, target selection: the profile is FLAT (four near-equal quarters)

Re-profiled the whole search at the **shipped** gameplay config (FEATURES
0x5F = 0x1F | FT_CKEXT, FEATURES2 0), budget mode, aggregated over six
phase-diverse positions (2 midgame, 1 opening, 1 tactical, 2 endgame;
156M cycles total). Instrument: `TestProfileR5`
(internal/chesstest/profile_r5_test.go).

Round 4 profiled only two midgame FENs at FEATURES 0x1F — a config that
predates check extensions — so its target list was both midgame-shaped
and stale. This re-profile replaces it.

**Per-file rollup** (per-PC attribution by nearest preceding label whose
defining source file is known; attributing by label NAME alone leaves
~25% unattributed, because macros like ATSLOT/MVPBODY emit ca65 `.local`
labels that exist in the link map but in no source file):

| file | share | what |
|---|---|---|
| board.s | 26.8% | attacked / make / unmake |
| movegenbody.inc | 26.0% | move generation (inlined macro) |
| search.s | 22.7% | search control |
| eval.s | 21.3% | evaluation |
| movegen.s | 1.6% | generator entry/dispatch |
| tt.s | 1.5% | transposition table |

**The headline is the flatness, not the ranking.** Concentration by
label: top-1 = **4.16%**, top-3 = 11.2%, top-5 = 15.4%, top-10 = 22.3%,
top-20 = 34.0%, top-40 = 48.6%, across 687 labels. There is no hot spot
left to attack — the engine's time is four near-equal quarters, which is
the expected signature after rounds 2-4 (−40%, then −12.6%).

**What that implies for round 5.** A 10% win inside any single quarter
buys only ~2.5% overall, so per-routine micro-optimization now has a low
ceiling — this is why round 4's follow-up items all came back null
(MG/(MG-D) accumulator NEGATIVE, king-shield cache negative). The
remaining leverage is in **doing less work**, not in doing the same work
faster.

The largest such lever identified: **generation is EAGER**. In
asm/search.s the full-width node entry is `snode: jsr gennodef`, which
builds the ENTIRE move list, and only then does pass 0 hunt the TT move
inside it. The move-ordering PASSES are staged (0 TT move, 1 heavy
captures/promos, 2 light captures, 3 killer quiets, 4 remaining quiets)
but the LIST is not. Every node whose TT move alone fails high paid for a
full generation it never used, and generation is the biggest quarter.
Deferring it is measurement-gated, not obvious: the saving is real only
if a standalone `ttmovevalid` (needed because TT collisions can return a
move illegal in the current position, and making it unchecked would
corrupt the board) costs materially less than generation, and non-cutoff
nodes pay that validation with no saving at all. Being priced now.

Per-phase shares are logged by the instrument; the notable movers are
`mvpbody` (0.6-0.9% midgame/opening/tactical but **2.9%** endgame) and
`generateq` (5.7% opening vs 2.9% endgame) — consistent with task #32's
finding that the cost model over-prices endgame nodes ~30%.

## 2026-07-27 — RE-SCREEN of the rejected depth-class features: NOTHING recovered; and a CORRECTION to the compression claim

Hypothesis under test: the budgeted-ID fix revealed the cycle screen was
capping depth early, which should specifically undervalue features whose
payoff IS depth. Three such features had been rejected on a screen number
alone (no asm SPRT). Re-screened all three on the corrected instrument,
2000 games each (4 seeds x 500), asm-matched 0x1f, cbudget 143M, original
cost taxes — PLUS a CONTROL: king safety, an EVAL term, NOT depth-class,
which should NOT move if the mechanism is what we think.

| feature | corrected | published | shift |
|---|---|---|---|
| LMP 3+2d Dmax3 | −104.7 ± 13.7 | −85 ± 14 | −19.7 |
| SEE atk-fw | +14.4 ± 12.5 | +2 ± 7 | +12.4 |
| Countermove | +3.8 ± 12.6 | +4 ± 9 | −0.2 |
| **CONTROL king safety** | **−1.4 ± 12.6** | **−19 ± 13** | **+17.6** |

(All four features at 2000 games = 4 seeds × 500. Shifts with both
intervals propagated: LMP −19.7 ± 19.6, SEE +12.4 ± 14.4, countermove
−0.2 ± 15.5, **CONTROL +17.6 ± 18.1**.)

**THE CONTROL MOVED MORE THAN ANY CANDIDATE** (+17.6 vs SEE's +12.4), and
**not one shift reaches significance** — every interval contains zero.
There is no coherent depth-class pattern.

**Verdict: nothing is recovered.** LMP is confirmed dead and if anything
worse (~−105; a deeper search exposes aggressive pruning errors more, not
less). SEE and countermove remain statistically indistinguishable from
their published verdicts. All screen-only verdicts stand as recorded, and
the remaining ones (bishop pair, Texel retune, positional set,
checks-in-QS) do not need revisiting.

**CORRECTION TO THE 2026-07-27 COMPRESSION ENTRY.** That entry claimed
the budgeted-ID divergence was "a REAL compression mechanism", resting on
the check-extension screen reading +0.5 pre-fix and +19.7 corrected
against an SPRT truth of +24. This control experiment shows a **+20.9
shift occurring with NO mechanism present**, so a ~+19 shift is within
what run-to-run variance produces at these game counts. That agent had
already flagged its shift as "only marginally significant, the 4/4 sign
agreement carrying most of the evidence" — the correct reading now is
weaker: **the check-extension shift is NOT established as a mechanism
effect.**

What DOES stand: the budgeted-ID divergence was REAL and the fix is
CORRECT — proven independently of any Elo measurement by
TestBudgetModeParity (exact-depth agreement with the asm 54.6% → 82.4%,
pool skew +53 → +3). We fixed a genuine fidelity bug in the instrument.
What we cannot claim is that it was materially distorting our verdicts.

So the compression mechanisms that remain EVIDENCED are: node-budget bias
(proven directly), stale mirror defaults (a config bug, fixed), and
ordering context (LMP +39 at 0x7f vs −85 at 0x1f). The tt.s TT bug was
ruled out by the aspiration re-test; the budgeted-ID divergence is real
but its verdict impact is unproven.

**Consequence: the feature hunt is finished.** Every candidate has been
screened, and the rejected ones stay rejected under a corrected
instrument with a control.

## 2026-07-27 — ASPIRATION re-tested against a CORRECT TT: still NEUTRAL (+1.7 ± 23). The TT-taint hypothesis is NOT supported.

FT_ASP was SPRT-rejected at −21 ± 32 on 2026-07-21, while asm/tt.s was
corrupting 54% of TT stores (unsigned mate-zone compare ply-shifting
every negative score). Aspiration is the most TT-score-dependent feature
in the image — it seeds its window from the PREVIOUS ITERATION'S SCORE —
so the rejection was re-tested from scratch on the fixed engine, with an
asm review pass first (the new standing practice).

| batch (aspiration ON vs OFF, same binary, 30M/move) | Elo |
|---|---|
| openseed default, 300 g: +97 =97 −106 (48.50%) | −10 ± 32 |
| openseed 7, 300 g: +102 =108 −90 (52.00%) | +14 ± 32 |
| **combined 600 g: +199 =205 −196 (50.25%)** | **+1.7 ± 23** |

Equal-spend verified both batches (0.9987 / 1.0027).

**Verdict: aspiration is NEUTRAL, and the rejection stands on its own
merits.** +1.7 ± 23 lies inside the old −21 ± 32 — statistically the same
answer — and is centred on ZERO, not on the +19–23 the node-budget mirror
screen once promised. **The cycle-budget screen called this exactly**: the
same config re-screened at 143M cyc/move measured −2 ± 13 over 2000 games
on a mirror whose TT was always signed-correct. Two independent
instruments, one TT-clean from the start, agree on zero.

**Consequence — this CLOSES the TT-taint line.** The tt.s bug was real,
worth fixing, and made mate searches 6.8% faster, but it was NOT a
significant source of wrong feature verdicts. It does not license
re-opening other TT-dependent rejections; history ordering (mirror +56 →
asm −16) should only be revisited for the node-vs-cycle budget bias,
which is separately documented. The −21 → +1.7 shift is inside the noise
of either measurement: a corrupted TT plausibly cost aspiration a few
Elo, not twenty.

So the compression story's mechanisms are: node-budget bias (proven),
stale mirror defaults (fixed), ordering context (LMP +39→−85), and the
budgeted-ID divergence (proven, large — +0.5→+19.7 on a known +24
feature). The TT bug is NOT among them.

**Disposition: main keeps FT_ASP OUT.** Rejected twice now, and our
standing discipline strips rejected features (that is what freed 1752 B
across FT_ASP/FT_ROOKX/FT2_IMPROV/FT2_EGTECH). The re-port is preserved,
parity-verified, on branch `worktree-agent-a6cf9e23e982687c0` (f0ef8d7);
this re-test proved resurrecting from git history works cleanly.

## 2026-07-27 — ★ COMPRESSION MECHANISM FOUND AND FIXED: the mirror's budgeted ID was shallower than the ship's

The residual "mirror over-promises" effect finally has a measured cause.
The mirror's budgeted iterative deepening differed from the asm's in
three ways: no mate stop; hard abort at 1× budget (asm: **2×**); and a
CUMULATIVE halfway start-gate where the asm uses a **PREDICTIVE** one
("start iff now + 2×last_iteration_cost < budget"). Net: at the same
budget the mirror bought a systematically SHALLOWER search than the
engine. Invisible to every gate — they are all fixed-depth — while every
cycle-budget SCREEN runs exactly this path.

**BLAST RADIUS, measured on a feature whose truth we know.** Re-ran the
check-extension screen, same seeds, 2000 games/arm:

| instrument | reading |
|---|---|
| pre-fix mirror | **+0.5 ± 12.7** |
| corrected mirror | **+19.7 ± 12.6** |
| asm SPRT (truth) | **+24 ± 23** |

The corrected instrument lands on the truth; the old one read ~ZERO for
a feature genuinely worth +24. All 4 seeds moved up. (Honest caveat: at
2000 g/arm the shift +19.1 ± 17.9 is only marginally significant on its
own — the 4/4 sign agreement carries most of the evidence.)

So the compression story now has FOUR measured mechanisms, not a vibe:
node-budget bias (proven), stale mirror defaults (fixed), ordering
context (LMP +39→−85), and this. Plus the tt.s TT bug as a fifth
candidate still under test.

**New gate: TestBudgetModeParity** (284 positions) compares completed
depth, move, score, trees and spend against the asm with justified
tolerances. It FAILS on the pre-fix mirror (54.6% exact depth, pool skew
+53) and passes corrected (82.4%, skew +3) — it would have caught this.
Note budgeted screens now legitimately spend up to 2× budget per move
(the ship's real behaviour), so they take longer.

**BONUS FINDING (filed, not fixed): the cycle model over-prices ENDGAME
nodes ~30%.** On identical trees Cyc.Est/real is 0.999 on the openings
pool but **0.776 on endgames/near-mate** — so budgeted screens run
shallower than the ship precisely where 29% of our losses live. A
DefaultCycleCosts re-fit; the new gate's skew assertion is deliberately
scoped to the fit region so this cannot be silently absorbed.

## 2026-07-27 — INSTRUMENT FIX: budgeted mirror ID now matches the asm driver; new BUDGET-MODE parity gate

The 2026-07-26 audit filed (but did not fix) a divergence in the mirror's
budgeted iterative-deepening driver — the code path every `-cbudget` screen
runs. Fixed here, mirror-side only (`internal/mirror/search.go`,
`effort.go`); asm untouched.

**The two drivers, before:**

| | asm/engine.s (`idloop`/`idok`) | mirror (before) |
|---|---|---|
| start iteration d+1 | PREDICTIVE: cost = clock − ITSTART of the iteration just finished; start only if `now + 2*cost < BUDGET` (saturating; strict) | CUMULATIVE halfway: start only if `spent*2 < budget` |
| hard abort | `ABORTL = 2*BUDGET` (`2*CEILMAX` under FT2_ADAPT), polled every 128 nodes, never below CURDEPTH 2 | `1*budget`, checked exactly, never at depth 1 |
| partial iteration | DISCARDED; restore PREV\* move/score, `dec CURDEPTH` | DISCARDED; keep last completed (same) |
| winning mate | STOP: `SCORE >= MATEZONEHI` ⇒ report (deepening can't improve an exact mate) | kept deepening |
| moveless root | keeps iterating to the depth cap | broke out at depth 1 |
| depth cap | `CURDEPTH <= MAXCAP` (leaves CURDEPTH at cap+1 on exit) | `d <= maxDepth` |

Net: at the same budget the mirror bought a systematically SHALLOWER ID
than the shipped engine — which is precisely the shape of a mirror→asm
"compression" mechanism, and invisible to every gate we had, because they
are all fixed-depth.

**Fixed** by transcribing the asm's policy (including its 2x-growth
estimate — fidelity, not improvement: the real growth ratio is 2-6 and the
2x hard abort is what backstops it). `idPredict` is shared by
`SearchBudget` (node-denominated), `SearchCycleBudget` and `SearchTimed`
(whose asm counterpart, adaptmaybe, hangs off the same `idok`).

**New gate: `TestBudgetModeParity`** (internal/chesstest/budget_parity_test.go,
~8s, 284 positions = 71 starts × 2 masks × {2M, 8M} cycle budgets). At a
matched budget it compares completed depth (asm CURDEPTH vs mirror
CompletedDepth), chosen move, root score, tree size (search/make/eval
entries) and spend. Tolerances are honest about the cycle model: exact
depth on ≥70% of positions, |Δdepth| ≤ 1 everywhere, EXACT trees/moves/
scores on the same-depth-no-abort subset (there the tree is a pure
function of the depth sequence, so zero is the right tolerance), and a
one-sided-SKEW test on the openings pool — the region the cycle model was
fit on. Sensitivity, same 284 positions:

| driver | depth exact (all) | openings-pool skew (asm-deeper − mirror-deeper) |
|---|---|---|
| before | 54.6% | **+53** (54 vs 1) — FAILS |
| after | 82.4% | **+3** (9 vs 6) — passes |

**BLAST RADIUS — the corrected instrument moves a known screen TOWARD its
SPRT truth.** Re-ran the check-extension screen (a feature we KNOW is
genuinely positive: SPRT +24 ± 23 / +12 ± 33) on the same openings and the
same four seeds, once per driver, 2000 games per arm,
`-cbudget 143000000 -afeat 0x1f -bfeat 0x1f -ackext 1`:

| seed (500 g) | pre-fix driver | corrected driver |
|---|---|---|
| 6502 | +13 ± 26 | +26 ± 26 |
| 4711 | +9 ± 25 | +24 ± 25 |
| 1234 | +6 ± 25 | +8 ± 25 |
| 9001 | −26 ± 26 | +21 ± 25 |
| **aggregate (2000 g)** | **+0.5 ± 12.7** (700/603/697) | **+19.7 ± 12.6** (745/623/632) |

Shift **+19.1 ± 17.9** (95%), and 4 of 4 seeds moved UP. The corrected
instrument lands on top of the SPRT point estimate (+24) where the pre-fix
one read ~0. So this divergence WAS a compression mechanism: a shallower
budgeted ID under-pays a feature whose value is depth-dependent. (Caveat:
2000 games/arm, so the shift itself is only marginally significant on its
own; the 4/4 sign agreement is the stronger part of the evidence.)

**Cost note:** the corrected driver may spend up to 2× budget on a move
(the asm's real hard abort), where the mirror previously capped at 1×, so
budgeted mirror screens now take noticeably more wall time. That is the
shipped engine's actual spending behaviour, not overhead we added.

**Second finding, from the gate's own diagnostics.** On identical trees the
mirror's `Cyc.Est` tracks real emulated cycles with median ratio **0.999**
on the openings pool but **0.776** on endgames/near-mate — the cycle model
over-prices endgame nodes by ~30%, so a budgeted mirror screen runs
shallower than the asm exactly where our losses live (29% endgame
conversion). Not fixed here (that is a re-fit of `DefaultCycleCosts`); the
gate reports it every run and the skew assertion is deliberately scoped to
the fit region so it cannot be silently absorbed.

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

> **⚠ READ WITH THE 2026-07-29 ENTRIES.** This number is correct and
> reproducible **for the harness configuration it measures** — a later
> 252-game re-run of the same mode returned +116 [+75, +160], containing it,
> 220 commits on.
>
> A paired 504-game control then found the **soft-clock** arm at +51 [+13,
> +91], a difference of −64 [−120, −8]. **My first correction here said
> "quote +51 for the disk". That was wrong and is retracted**: the follow-up
> investigation established that **the shipped disk does not ponder at all**
> (`asm/m8.s` blocks in `uiread`/`entkey` on the opponent's turn; there is no
> ponder path in `asm/`, and docs/plan.md excludes it from M8 deliberately),
> while ponder is ~49% of 8fish's compute in a symmatch game. So **neither
> gauntlet arm is the shipped artifact** — both ponder, and it does not.
>
> What is established: the −64 stands as measured, its sign may be right, and
> **its magnitude is unexplained**. The ponder-spend gap accounts for ~2 Elo,
> not 64. A paired same-state probe puts the shipped ADAPTIVE configuration
> at soft/exact **1.0033** — no spend deficit at all. Quote **+116** for the
> harness engine; for the disk, the honest answer is that no number has been
> measured yet.

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
