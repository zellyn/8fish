# 8fish on-device user interface — design

Status: **BUILT AND PLAYABLE** (2026-07-28). `asm/m8.s` implements this design;
`internal/ui` boots the real image in the emulator, types on its keyboard and
plays whole games against it, refereed ply by ply by `internal/refchess`. See
§12 for what shipped, the measured byte budget and what was deliberately
deferred. The sections below are the design as written; where the
implementation chose differently, §12 says so.

All byte counts are **measured** (ca65 `.out` on label differences, or the ld65
segment map) unless labelled *derived*, in which case the instruction-level
accounting is shown. All cycle counts are **measured by difference** under
`harness` (run the same image with N and N+1000 repaints; divide) unless
labelled *derived*.

---

## 1. Summary

| decision | choice |
|---|---|
| display | **40-column text page 1**, inverse-video checkerboard, FEN-case pieces |
| where the code lives | **Language Card RAM `$E000-$FFEF`** — 8,192 B that cost **zero** of the image's 1,622 B headroom |
| move input | **typed coordinate** (`e2e4`, optional 5th promotion char), validated against the engine's own generator |
| levels / time control | **fixed depth OR timed** — the IIe has no readable clock, but `FT2_SOFTCLK` (shipped 2026-07-27) gives the engine an ESTIMATED one; see §6.2 |
| `FT2_ADAPT` | **now runnable on device** via `FT2_SOFTCLK`; exposing it is still a UI decision (its ceilings are host-computed). See §6.3 |
| progress during search | printed **between iterative-deepening iterations**, from the UI's own driver loop. **Zero lines of `search.s` change** |
| MAIN-RAM cost | **0 bytes permanent** (a run-once 57-byte copier lives in soon-to-be-overwritten RAM) |
| LC budget | **6,270 B of 8,176 B measured** (4,222 B code+data, 2,048 B RAM arrays, 256 B variables); **1,906 B free** |

The single most important finding is in §2: this project has never used its
Language Card. The UI does not have to compete for the 1,622 bytes.

---

## 2. The memory map, and where the UI actually fits

Verified against the current build (`ld65 -m`, engine.bin md5 `3902502c`):

```
MAIN
  $0000-$00FF  zero page          engine (BOARD $40-$BF, search/eval $C0-$FF)
  $0100-$01FF  6502 stack
  $0200-$030C  engine driver state (PWBITS, ENTCNT/ENTROPY, BOOKRND, ADAPT...)
  $030D-$03EF  FREE (227 B)       <- the PoC pokes its repeat counters here
  $0400-$07FF  TEXT PAGE 1        <- the UI's screen. Engine never touches it.
  $0800-$0DFF  engine per-ply arrays
  $0E00-$1FFF  MOVESTACK (4,608 B; dead until the first search)
  $2000-$3FFF  resident opening book (3,866 B used)  == HI-RES PAGE 1
  $4000-$78BC  engine CODE  (14,525 B)               == HI-RES PAGE 2
  $7900-$B958  engine TABLES (16,473 B)
  $B99A        image top.  Ceiling $BFF0 (harness traps).  HEADROOM 1,622 B
AUX
  $0200-$81FF  transposition table, 4096 x 8 B
  $8200-$BFFF  free (15.9 KB), reachable only through the LC aux primitives
LANGUAGE CARD  (engine runs with $C08B latched: bank 1, read RAM + write RAM)
  $D000-$D040  LCCODE, 65 bytes    <- the ENTIRE current LC usage
  $D041-$DFFF  FREE, bank 1        4,031 B
  $D000-$DFFF  FREE, bank 2        4,096 B   (one $C083/$C08B switch to reach)
  $E000-$FFEF  FREE, unbanked      8,176 B   <- THE UI LIVES HERE
  $FFF0-$FFFF  6502 vectors (RAM once LC read is enabled — see §9 risk 3)
```

Because the engine already latches `$C08B` at entry and never switches back,
`$E000-$FFFF` is ordinary, directly-executable, directly-addressable RAM in the
normal address space. No bank switching, no aux primitives, no soft-switch dance
around every access. The UI is `jsr`-callable from the engine's address space and
can `jsr` into the engine's routines by their published addresses.

**This is the design's foundation**: the UI is not competing for the 1,622-byte
headroom, so the honest answer to "what does the UI cost the engine?" is *nothing
in MAIN, nothing in the transposition table, nothing in the opening book*. No
space-reclamation pass is requested.

Loading it costs nothing permanent either. `UI.BIN` is `BLOAD`ed into engine
RAM that is garbage until the first search; a small copier — itself `BRUN` at
`$0800`, which the engine will later overwrite with `PIECESQ` — latches
`$C08B`, copies the payload to `$E000`, and jumps there. `asm/uitest.s` is
exactly this stub and it is proven to work in the emulator.

> **Staging address: see §12.2, which supersedes this paragraph.** This
> section originally said `$0E00`; that is wrong by construction, because
> `$0E00-$1FFF` is 4,608 bytes against a 5,888-byte LC code budget, so a UI
> grown past 4,608 B would have been `BLOAD`ed straight over the resident
> opening book at `$2000`. As built, the payload stages at `$0900`.

> Incidental finding — **resolved 2026-07-28**, see the coexistence note in
> `docs/book.md`. `defs.inc` used to reserve `$2000-$207F` as move-stack
> overflow guard slack "which must stay unallocated" while `book.inc` puts
> `BOOK_BASE = $2000`. The reservation was the wrong comment and has been
> deleted: it protected nothing in either environment, and the measured
> move-stack peak is 487 of 1,152 slots, now enforced by
> `chesstest.TestMoveStackWatermark`. The book keeps all of `$2000-$3FFF`.

---

## 3. Display mode

### 3.1 The options, priced

| mode | RAM it must take | book must move? | renderer bytes | repaint cost | verdict |
|---|---|---|---|---|---|
| **40-col text** (page 1 `$0400-$07FF`) | none — engine never uses it | no | **508 measured** | **23,659 cyc measured** (23.2 ms) | **CHOSEN** |
| 80-col text | AUX `$0400-$07FF` = 1,024 B **of the TT** (128 of 4096 entries, 3.1%) | no | ~750 *derived* (every row split across two banks; 80STORE/PAGE2 per write) | ~2x | reject |
| hi-res page 1 (`$2000-$3FFF`) | MAIN `$2000-$3FFF` | **yes** | ~1,850 *derived* | ~80,000 cyc *derived* | stage 2, viable |
| hi-res page 2 (`$4000-$5FFF`) | that is engine CODE | — | — | — | impossible |
| double hi-res | MAIN **and** AUX `$2000-$3FFF` = 8,192 B **of the TT** (1024 of 4096 entries, **25%**) | yes | ~2,600 *derived* | ~160,000 cyc *derived* | reject |

Derivations for the rejected/deferred rows:

- **80-column.** Text page 1 in 80-col mode is main RAM for odd columns and AUX
  `$0400-$07FF` for even ones. AUX `$0400-$07FF` sits inside the TT
  (`TTBASE = $0200`, 4096 × 8 B = `$0200-$81FF`), so 128 TT entries die. In
  exchange we get 80 columns for a board that occupies 16 of them. It also puts
  `80STORE`/`PAGE2` into the same soft-switch space the LC aux primitives use for
  `RAMRD`/`RAMWRT`, which is a real interaction risk for zero benefit.
- **Hi-res page 1.** Make squares **28 pixels** wide (exactly 4 hi-res bytes) so
  every square is byte-aligned — this is the trick that dodges the sub-byte glyph
  alignment problem `docs/sargon.md` records for Sargon's own board. Board is
  224 × 192 of 280 × 192. Glyphs: 4 B × 20 rows = 80 B each; 6 types × 2 colours
  = 960 B. Row-base table 192 × 2 = 384 B. Blit + background ~250 B. Text
  primitives are still needed for the mixed-mode bottom 4 rows (`$C053`): ~250 B.
  Total ≈ 1,850 B — which **fits in LC** alongside the text UI. Repaint: 64
  squares × 24 lines × 4 bytes = 6,144 stores at ~13 cyc ≈ 80,000 cyc (78 ms).
  So hi-res is *affordable*; it is deferred because of what it costs in
  **information**, not bytes: mixed mode leaves 4 text rows, so the move list,
  the opening name, the thinking readout and the prompt cannot all be on screen
  at once. Sargon III has exactly this problem, which is why ESC toggles between
  its board and its text screen. 40-column text shows **all of it
  simultaneously**, and that is a straight UX win over the period reference.
- **Double hi-res.** Costs a quarter of the transposition table. Paying
  measurable playing strength for prettier pieces is the wrong trade for a
  project whose whole thesis is squeezing strength out of a 1 MHz 6502.

### 3.2 The chosen encoding

Two characters per square (piece letter + blank), so a cell is 14 × 8 pixels —
close to square, and the board is a 16 × 8 character block.

- **Square colour** → **inverse video** on dark squares (both characters of the
  cell), giving a real checkerboard with no glyph cost at all.
- **Piece colour** → **letter case**: uppercase = White, lowercase = Black. The
  FEN convention, instantly readable, and it survives *under* the checkering, so
  neither channel is lost when a piece stands on a dark square.

Apple IIe screen-byte encoding used:

```
normal video   ASCII | $80     ($80-$FF; lowercase at $E0-$FF)
inverse upper  ASCII & $3F     ($00-$3F)
inverse lower  ASCII           ($60-$7F)
```

**★ PRECONDITION, added 2026-07-28 after it bit us.** That table describes
the IIe's **ALTERNATE** character set. A IIe powers up on the **PRIMARY**
set, where `$60-$7F` is **flashing punctuation** — so a UI that writes those
bytes without first storing to `$C00F` shows a blinking digit or bracket
wherever it meant a black piece on a dark square. `m8main` now takes the
display in five stores (`$C00C` 80COL-off, `$C00F` ALTCHARSET, `$C051` TEXT,
`$C052` NOMIX, `$C054` PAGE1); see docs/results.md 2026-07-28. `$60-$7F` is
inverse lowercase on both the enhanced and unenhanced IIe once the alternate
set is selected (they differ only at `$40-$5F`, which this design does not
use). The whole mapping is a
single **32-byte table** indexed by `dark<<4 | (piece & $0F)` — see `PIECECH` in
`asm/ui.s`. Entry `$00` is a normal space and entry `$10` an inverse space, which
also serves as the blank half-cell, so the inner loop needs no second table.

*Fallback for a II+ / non-lowercase machine:* drop the checkering and use
normal = White, inverse = Black on uppercase letters only. Same code, a different
32-byte table. Not implemented; noted so the choice is reversible.

---

## 4. Screen layout

Verified — this is a literal capture of the PoC running in the emulator
(`go test ./internal/ui -run TestRenderPosition -v`), with the inverse-video map
beside it:

```
    +----------------------------------------+
 0  | 8FISH 1.0    LEVEL 4     YOU ARE WHITE |  ########################################
 1  |                                        |  ........................................
 2  | 8 r   b q k     r    MOVES             |  .....##..##..##..##..###################
 3  | 7   p p p b p p p    1 e2e4 e7e5       |  ...##..##..##..##.......................
 4  | 6 p   n     n        2 g1f3 b8c6       |  .....##..##..##..##.....................
 5  | 5         p          3 f1b5 a7a6       |  ...##..##..##..##.......................
 6  | 4 B       P          4 b5a4 g8f6       |  .....##..##..##..##.....................
 7  | 3           N        5 e1g1 f8e7       |  ...##..##..##..##.......................
 8  | 2 P P P P   P P P                      |  .....##..##..##..##.....................
 9  | 1 R N B Q   R K                        |  ...##..##..##..##.......................
10  |   a b c d e f g h                      |  ........................................
11  |                                        |  ........................................
12  |WHITE TO MOVE                           |  ........................................
13  |CHECK                                   |  ........................................
14  |D7  +0.34  b1c3                         |  ........................................
15  |                                        |  ........................................
16  |BOOK: RUY LOPEZ, CLOSED                 |  ........................................
17  |ILLEGAL MOVE - TRY AGAIN                |  ........................................
18  |                                        |  ........................................
19  |                                        |  ........................................
20  |N-NEW T-TAKEBACK R-RESIGN D-DRAW L-LEVEL|  ........................................
21  |                                        |  ........................................
22  |                                        |  ........................................
23  |YOUR MOVE? e2e4                         |  ........................................
```

Regions:

| rows | cols | content |
|---|---|---|
| 0 | 0-39 | inverse status bar: name/version, level, which colour the human has |
| 2-9 | 1 | rank digits 8..1 |
| 2-9 | 3-18 | the board (16 × 8 cells) |
| 10 | 3-18 | file letters a..h |
| 2 | 21-39 | inverse `MOVES` panel header |
| 3-15 | 21-39 | move list, 13 full moves (matching Sargon's 13), scrolling |
| 12 | 0-19 | side to move |
| 13 | 0-19 | check / mate / stalemate / draw-claim status |
| 14 | 0-19 | thinking readout: depth, score, current best move |
| 16 | 0-39 | opening name while in book (`CUROPENING` → name table) |
| 17 | 0-39 | messages: illegal move, promotion prompt, draw offer |
| 20 | 0-39 | command reminder |
| 23 | 0-39 | input line |

Everything the player needs is on screen at once. The status column (0-19) and
the move panel (21-39) never overlap, so a status update never repaints the
panel and vice versa.

Rank/file labels use the same lowercase files the input syntax uses, so the
mapping from what you see to what you type is direct.

---

## 5. Move input

### 5.1 Typed coordinate, not cursor selection

Recommendation: **typed from-to coordinate**, e.g. `e2e4`, with an optional 5th
character for promotion (`e7e8q`), `RETURN` to submit, `←`/DELETE to backspace.

| | typed `e2e4` | cursor / joystick square picking |
|---|---|---|
| code | ~70 B parse + shared validation | ~200-260 B *derived*: 2 bytes of cursor state, 4 arrow keys + select + cancel dispatch, highlight/unhighlight repaint, board clamping — plus it *still* needs the same validation and a separate promotion prompt |
| notation | identical to the engine's internal `(from,to,flags)` and to every log, test and PGN in this repo | needs its own translation for the move list |
| keystrokes per move (entropy) | 4-5 | 6-14 |
| errors | typo → one clear message | mis-click → same problem, more state |

Typed entry wins on every axis that matters here, and `asm/entropy.inc`'s own
doc-comment already assumes it ("a UCI-style move entry is 4-5 of them"). A
joystick/cursor mode is a clean later addition on top of the same validator; it
is not the thing to build first.

Every keystroke — menus, confirmations, move entry, all of it — goes through
`jsr entkey`. The 16-cycle poll loop and the ROL-then-EOR fold are the shipped,
byte-verified collector; the UI does not get its own keyboard loop. Just before
each search: `jsr entseed`. For the book's `BOOKRND` (4 bytes at `$0222`) the UI
seeds from `ENTROPY`/`ENTCNT` (~20 B *derived*).

### 5.2 Validation — reuse, don't reimplement

The UI never contains chess rules. Validation is:

```
jsr evalinit            ; root accumulators + HASH0-3
PLY = 0 ; MSP = MOVESTACK
jsr gennodef            ; the engine's own pseudo-legal generator
walk PLYBASELO/HI[0] .. PLYENDLO/HI[0]   ; 4 bytes/move: tier, from, to, flags
    match FROM and TO (and, if the entry has FL_PROMO, the promotion type)
jsr make
    ; the engine's own legality test, lifted verbatim from search.s sdomove:
    ATSIDE = SIDE ; ATSQ = PIECESQ[(SIDE ^ COLORMASK) << 1] ; jsr attacked
    C = 1 -> the mover left his own king attacked: jsr unmake, reject
```

This makes every special case free, because the generator already emits them:

- **castling** — type `e1g1`; the generated entry carries `FL_CASTLE` and `make`
  moves the rook. No UI code.
- **en passant** — type `e5d6`; the entry carries `FL_EP`. No UI code.
- **promotion** — the generator emits four separate entries. If the typed move
  has a 5th char, match it against `flags & FL_PROMO`; if not, and the matched
  from/to is a promotion, prompt on row 17: `PROMOTE TO (Q R B N)?` and read one
  key (through `entkey`). ~30 B.
- **illegal move** — no match, or the legality test rejects: print
  `ILLEGAL MOVE - TRY AGAIN` on row 17, clear the input line, do not advance the
  clock or history. ~20 B.
- **mate / stalemate** — run the same loop over the whole list counting legal
  moves. Zero legal moves and `curincheck` set → checkmate; zero and clear →
  stalemate. ~90 B including the message.

### 5.3 Commands

Files are `a`-`h`, so any other leading letter is unambiguously a command. Single
keys on the input line:

`N` new game · `T` take back · `R` resign · `D` offer/claim draw · `L` level ·
`S` swap sides · `?` help.

**Take back** replays rather than un-makes. The UI keeps the game as three
parallel 256-byte arrays (`UIHFROM`/`UIHTO`/`UIHFLAG`, so a ply index is a plain
`X` with no multiply — 256 plies = 128 full moves, more than Sargon's 127-move
limit). Take back = restore the start position, then `make` plies
0..n-3. At order 300-600 cycles per `make`, a 100-ply replay is ~30-60k cycles
= 30-60 ms *derived* — imperceptible,
and it costs **80 B of code instead of a per-ply undo record**, while
simultaneously being the move list the panel draws.

**Draw claims.** The engine detects repetition and the 50-move rule *inside* the
search (`search.s` scans `HASHSTK` back `HALFMOVE` plies), but at the root
`PLY = 0` and there is no game history in `HASHSTK` — exactly the situation
`internal/ucibridge` handles host-side with `refchess`. On device the UI keeps
its own 4 × 256-byte hash history: after each game move, `jsr evalinit` leaves
`HASH0-3` for the new position; push it, and scan the last `HALFMOVE` entries for
two prior matches. `HALFMOVE >= 100` is the 50-move claim. ~80 B + 1,024 B of LC
data. The status line then reports `DRAW BY REPETITION` / `DRAW - 50 MOVE RULE`,
and `D` claims it.

---

## 6. Game flow

### 6.1 New game / colour / level

- `N` → copy a 160-byte start image (128 B `BOARD` + 32 B `PIECESQ`) into place,
  `CASTLE = $0F`, `EPSQ = NOSQ`, `HALFMOVE = 0`, `SIDE = 0`, `UIHCNT = 0`,
  `CUROPENING = 0`. The TT is left alone: entries verify `HASH1-3`, so stale
  entries from the previous game are noise, never wrong.
- `S` → swap which colour the human has; if the engine now has the move, it moves
  immediately.
- `L` → `LEVEL 1..9`, stored to `MAXCAP`. This is the **search depth**, not
  seconds; see below.

### 6.2 The Apple IIe has no clock — so levels are depths

This is the design's most consequential constraint and it deserves to be stated
plainly.

The engine's budget mode reads `CLOCK_TRAP` (`$BFF4`) — three absolute reads in
`search.s checkclock`, plus the predictive gate in `engine.s idloop`. Under the
harness that address is a live 24-bit cycle counter. **On real hardware it is
plain RAM.** And the IIe offers no substitute: no real-time clock, no readable
video counters (that is the IIgs), VBL (`$C019`) only tells you *whether* you are
in blanking and would have to be polled ~60 times a second by code that is busy
searching, and the paddle timer is a ~3 ms one-shot.

**RESOLVED 2026-07-27 — `FT2_SOFTCLK`.** The engine now ESTIMATES elapsed
cycles instead of measuring them, exactly as Sargon III does (it counts toggles
of its thinking asterisk; docs/sargon.md). `checkclock`'s existing once-per-128-
nodes poll adds `128 × cost(PHASE)` to a 24-bit accumulator kept **at `$BFF4`
itself**, so every clock READER above is unchanged — on hardware they read the
estimate, under the harness the real counter. Measured (docs/results.md): cost
**+0.0073%**, resolution one poll = 0.45-0.57 s. Feature OFF is the identical
instruction stream.

**RECALIBRATED the same day** — the first cost table was fit on quiet
calibration positions and gated on a position pool, both of which said it
over-estimated by 5%; in real games it under-estimated and the engine overran
its clock by 17%. The table is now fit on moves from real games and
deliberately biased. What a UI can promise, measured in games: at a **4 s**
level the engine spends **0.94** of its allocation and at a **15 s** level
**0.88**, against an exact clock's 0.92 and 0.86 — within 2.3% at both, and
under 1.0 at both, so it never flags.

**★ The UI must implement one rule.** The safety bias is NOT in the engine: the
cost table holds the raw measured cost, and the bias is applied by whoever
installs the per-move limits. Before poking BUDGET (and, with `FT2_ADAPT`,
CEILMAX / UNSTCEIL / MINSPEND — all four, sharing ONE margin taken from the
base allocation), scale by the octave of the budget:

| budget | poked value |
|---|---|
| ≤ ~8 s | `BUDGET × 202 >> 8` (margin 127%) |
| ~8-16 s | `BUDGET × 227 >> 8` (margin 113%) |
| > ~16 s | unchanged (margin 100%) |

A shift loop to find the top set bit of BUDGET, a table read, one 24×8
shift-and-add multiply — once per move, no division. Skip it entirely for
fixed-depth levels (BUDGET = 0). The reference implementation and the reason
the rule exists are in `internal/chesstest` `SoftClockMargin`; get it wrong in
the permissive direction and the engine overruns its clock by ~17%.

So the UI may offer **either** kind of level:

- **fixed depth** (`BUDGET0/1/2 = 0`), which `engine.s` supports as a
  first-class path — one iteration at `MAXCAP`, no clock read anywhere. Levels
  1-9 map to depths (≈ 2..8 plus quiescence, top level "as deep as you dare").
  Period-honest: fixed-ply levels were standard for 8-bit engines.
- **timed** (`BUDGET` = seconds × 1,020,500 ÷ 256, `FT2_SOFTCLK` set), which is
  what makes 8fish comparable to Sargon's timed levels and what `FT2_ADAPT`
  needs.

A timed level is DELIBERATELY conservative: the engine believes it has spent
~20-30% more than it really has, because the estimate is read by a threshold
(the `idloop` predictive gate) where symmetric clock noise turns into
asymmetric overspending. Per move the error is ±30-50%; per GAME the engine
lands at 0.94 of its allocation at a 4 s level and 0.88 at a 15 s one. Levels
below ~4 seconds are not worth offering: one poll is 0.45-0.57 s, and a
1-second move on a 1 MHz 6502 is only ~310 nodes deep anyway.

**One integration note for §7's UI-supplied ID driver.** `FT2_SOFTCLK` is armed
by `engine.s`'s ENTRY block, which patches `ccsite`'s operand and primes `$BFF4`
from `PHASE`. A UI driver that jumps straight to `iterate` skips that. For
fixed-depth levels this does not matter (nothing reads the clock). For timed
levels the UI must either enter through `engine.s` or replicate those two steps
— ~20 B, and `PHASE` is only valid after `evalinit`.

### 6.3 `FT2_ADAPT` — the clock half is solved; the ceiling half is not

`FT2_ADAPT` is budget-mode-only by construction, and its parameters
(`CEILMAX`/`UNSTCEIL`/`MINSPEND`) are deliberately computed **host-side**
because the on-device engine has no multiply for the ceiling arithmetic. With no
clock and no host, both halves were missing.

**The clock half is done** (§6.2): `FT2_SOFTCLK` gives budget mode a working
clock on hardware, so `FT2_ADAPT` can run. The engine-side change the earlier
draft of this section priced — a raw **node** budget — was NOT what shipped: a
node budget charges every node the same, and per-node cost varies ~2.5× by
phase, so "30 seconds" would have meant materially different things in an
opening and an endgame. The shipped estimator costs one extra table lookup over
that (measured 0.0073%, not the 0.004% derived for the counter) and buys a
phase-aware answer instead.

**The ceiling half remains a UI job.** On device, `CEILMAX`/`UNSTCEIL`/
`MINSPEND` need `4×`, `3×` and `÷4` of the per-move income plus a signed bank —
shift-and-add arithmetic on 24-bit values, ~60-80 B in LC, with
`chesstest.BankedClock` as the reference the port must match. Whether a level
menu wants adaptive effort at all is a product decision; the engine no longer
blocks it.

---

## 7. The search blocks — what the screen does about it

The search runs to completion with interrupts disabled and no callback. Two
things could give the player feedback, and only one of them is free.

**Rejected: a callback from `checkclock`.** It is the only periodic hook, it
lives in `search.s`, and it is on the hot path. Not touched.

**Chosen: paint between iterative-deepening iterations.** The UI supplies its own
ID driver — a fixed-depth-mode port of `engine.s`'s loop, ~40 B, living in LC —
which calls `jsr iterate` and then repaints row 14 with the completed depth, the
score, and the best move:

```
D1  +0.21  e2e4
D2  +0.14  e2e4
D3  +0.31  g1f3
...
```

Cost: row 14 is ~20 characters. The measured full repaint is 23,659 cycles for
960 characters = 24.6 cycles/character, so a 20-character line is **~500 cycles**
(*derived from a measured per-character rate*), plus ~200 cycles of number
formatting. Call it **~700 cycles per iteration**, ~10 iterations per move =
**~7,000 cycles per move**. Against a level-5 search of order 10⁷ cycles that is
**0.07%**, and it is entirely outside `search.s`, `eval.s`, `movegen.s` and
`board.s`. The engine's tree shape is byte-identical.

This is strictly better than the period reference: Sargon III shows a blinking
asterisk; 8fish shows the search actually getting deeper and the evaluation
actually moving, with the natural exponential pacing of iterative deepening
supplying the "it's alive" signal for free.

The static half is a `THINKING` marker on row 13 painted before the search and
cleared after (~30 cycles).

---

## 8. Where the code lives, and the byte budget

New files only. **No engine file is modified.** The UI links against the engine's
published symbols (`iterate`, `generate`, `gennodef`, `make`, `unmake`,
`attacked`, `curincheck`, `evalinit`, `bookprobe`) via an `engsyms.inc`
generated from `asm/engine.lbl` by the Makefile, so `engine.bin` stays
bit-identical and `TestMicroAB` is green by construction (verified: `make engine`
produces md5 `3902502c…` before and after this work, and `TestMicroAB` passes).

| component | bytes | basis |
|---|---|---|
| `uiboard` — 8×8 board paint | **81** | measured |
| `uicoords` — rank/file labels | **54** | measured |
| `uimoves` — move-list panel | **114** | measured |
| `uistatic` — layout-table painter | **50** | measured |
| `uicls` / `uiputs` / `uigotorc` | **55** | measured |
| `uisqout` / `uidec2` — square + decimal formatting | **54** | measured |
| `ROWLO`/`ROWHI` row-base tables | **48** | measured |
| `PIECECH` piece→screen-byte table | **32** | measured |
| **renderer subtotal (`asm/ui.s` today)** | **508** | **measured** |
| `entropy.inc` (keyboard + fold + seed) | 56 | measured (existing file) |
| line editor (buffer, echo, backspace, RETURN) | 80 | derived |
| move parse (`e2e4[q]` → FROM/TO/promo) | 70 | derived |
| move validation + legality (§5.2) | 120 | derived |
| legal-move count → mate/stalemate | 90 | derived |
| game hash history + repetition/50-move claim | 80 | derived |
| ID driver + score formatting + thinking line | 160 | derived |
| take back (replay from start) | 80 | derived |
| new game (start-position image + reset) | 190 | derived (160 B is data) |
| book probe glue + opening-name streaming | 90 | derived |
| command dispatch (N/T/R/D/L/S/?) | 150 | derived |
| LCCODE install + reset/IRQ vectors | 45 | derived |
| static strings, help text, menus | 400 | derived |
| **code + static data** | **≈ 2,119** | |
| game history `UIHFROM`/`UIHTO`/`UIHFLAG` | 768 | 3 × 256 |
| game hash history | 1,024 | 4 × 256 |
| input buffer, misc state | 100 | |
| **RAM data** | **≈ 1,892** | |
| **TOTAL** | **≈ 4,011 of 8,176 B at `$E000-$FFEF`** | **51% headroom** |

**MAIN-RAM cost: 0 bytes.** **TT cost: 0 bytes.** **Book: unmoved.** No
space-reclamation pass is requested, and the 1,622-byte headroom stays available
for the strength work it was freed for.

If a later stage wants the hi-res board (§3.1, ~1,850 B) it also fits in LC, and
the book would move to LC **bank 2** (`$D000-$DFFF`, 4,096 B free, book is
3,866 B) behind a `$C083`/`$C08B` pair around the once-per-move probe — about
8 bytes and ~10 cycles. Bank 1's `$D041-$DFFF` (4,031 B) would also just fit, but
with only 165 B of slack; bank 2 is the safer home.

---

## 9. Proof of concept — what was built and what it proved

Files (all new): `asm/ui.s`, `asm/uitest.s`, `asm/uitest.cfg`,
`internal/ui/render_test.go`, plus a `uitest` Makefile target.

`asm/uitest.cfg` mirrors the shipping plan exactly: `CODE` loads at `$4000`,
`UICODE` loads inside the image but **runs at `$E000`**. The `$4000` stub latches
`$C08B` with the same double read `engine.s` uses, copies `UICODE` up to `$E000`,
and jumps there. The renderer then paints from `BOARD`, which the Go test poked
from a FEN exactly the way `chesstest.NewMachine` pokes the engine.

`go test ./internal/ui -v` — **both tests pass**:

```
=== RUN   TestRenderPosition        (screen capture reproduced in §4)
--- PASS: TestRenderPosition (0.02s)
=== RUN   TestRenderCost
    uiboard  (8x8 board only):      4410.0 cycles =  4.32 ms at 1.0205 MHz
    paint    (whole 40x24 screen): 23659.0 cycles = 23.18 ms at 1.0205 MHz
    cold start (LC copy + one paint): 40688 cycles = 39.87 ms
--- PASS: TestRenderCost (0.52s)
PASS
```

`TestRenderPosition` asserts, against a real Ruy Lopez position
(`r1bqk2r/1pppbppp/p1n2n2/4p3/B3P3/5N2/PPPP1PPP/RNBQ1RK1 w kq -`):

- all 8 board ranks render the correct pieces in the correct case;
- all 128 board characters carry the correct inverse/normal attribute
  (dark iff `rank+file` is even, **both** halves of every cell);
- rank digits, file letters, the inverse title bar, the move panel (which lists
  the same game that produced the position), and every status/prompt row.

`TestRenderCost` measures by difference — the image repaints N extra times and
two runs are subtracted — so the numbers carry no timer-resolution error.

Byte sizes come from `ca65` evaluating label differences at assembly time, and
the image layout from the `ld65` segment map.

**What this de-risks (the riskiest parts, in order):**

1. **The Language Card really is usable and really is free.** Code assembled for
   `$E000`, copied there at run time, executes correctly under the emulator. This
   is what makes the whole zero-MAIN-cost budget real rather than aspirational.
2. **The text board is legible and the two-channel encoding works.** Checker
   *and* piece colour survive together; the attribute assertion proves it for all
   64 squares.
3. **The cost is negligible.** A full-screen repaint after every single move
   costs 23 ms — about 0.1% of a 30-second move, and about one and a half video
   frames. There is no reason to ever do partial repaints, which removes a whole
   category of incremental-update bugs from the design.
4. **The engine is untouched.** `engine.bin` md5 is unchanged and `TestMicroAB`
   passes.

**Not yet de-risked** (and honestly so, as of the PoC): keyboard input on real
hardware (the harness stands in for `$C000`/`$C010` via `HARNESSKBD`, which is
how `internal/entropy` already validates the collector); the actual IIe
character generator (goapple2 carries the ][+ 2 KB character ROM and does not
model `ALTCHARSET`, so the inverse-lowercase glyphs are verified as *bytes*
against the documented IIe encoding, not as pixels); and everything in §5-§7
that is a plan rather than code.

> **Both emulator gaps were closed on 2026-07-28** — goapple2's `iie` gained
> the IIe keyboard and `ALTCHARSET`, and a new `chargen` package renders IIe
> glyphs — and closing the second one found a real bug in the shipping
> build: it never selected the alternate character set (§3.2, §11 risk 2,
> docs/results.md). The shipping image now plays a full game through
> `$C000`/`$C010` and is screen-identical to the HARNESSKBD build.

---

## 10. Implementation plan

Each step is independently verifiable in the emulator, and each ends green before
the next starts.

1. **Renderer** — `asm/ui.s` + `internal/ui`. **DONE**; 508 B, 23.2 ms full
   repaint, tests passing.
2. **Symbol bridge.** Makefile rule turning `asm/engine.lbl` into
   `asm/engsyms.inc`, and an `asm/ui.cfg` that links `ui.s` standalone at `$E000`
   against it. *Verify:* build `ui.bin`; assert `engine.bin` md5 is unchanged and
   `TestMicroAB` still passes.
3. **Keyboard + line editor.** `entkey`-driven input line with echo, backspace and
   RETURN, painting row 23. *Verify:* extend `internal/ui` to drive the harness
   input traps (`internal/entropy`'s existing pattern) and assert both the echoed
   line and that `ENTROPY` changed on every keystroke.
4. **Move parse + validation.** `e2e4[q]` → generator match → legality test.
   *Verify:* a Go table of (FEN, typed move, accept/reject) including castling,
   en passant, both promotion spellings, a pinned piece, and a move that leaves
   the king in check — cross-checked against `internal/refchess`.
5. **Make the move, update the display.** Push to the history arrays, `make`,
   repaint. *Verify:* play a scripted 10-ply game; compare the resulting `BOARD`
   against `refchess` and the move panel against the script.
6. **Game-over detection.** Legal-move count, mate/stalemate, `HALFMOVE >= 100`,
   repetition via the UI's hash history. *Verify:* known mate-in-1, stalemate,
   50-move and threefold positions; assert the status row text.
7. **Engine's turn.** Fixed-depth ID driver + between-iteration thinking line +
   apply the engine's move. *Verify:* a fixed FEN and depth must produce the same
   `BESTFROM/BESTTO/BESTFLAGS` as `chesstest.SearchMove` on the same position —
   proving the UI-driven engine plays byte-identical chess.
8. **Book integration.** `jsr bookprobe` before searching; on a hit, play it and
   stream the `CUROPENING` name from the blob's name table to row 16.
   *Verify:* a booked position must reproduce
   `TestBookProbeParityASMvsGo`'s choice for the same `BOOKRND`.
9. **Commands.** New game, take back (replay), resign, draw offer/claim, level,
   swap sides, help. *Verify:* scripted key sequences; take back must restore a
   `BOARD` byte-identical to the position before the move.
10. **Boot path + vectors.** The `$0800` copier, `BLOAD` layout, LCCODE install,
    `$FFFA-$FFFF`. *Verify:* full cold start in the emulator; then on hardware or
    a full goapple2 machine with a disk image.
11. **Whole-game soak.** Drive a complete game keystroke-by-keystroke through the
    harness against `refchess` as referee, asserting agreement every ply — the
    same discipline `internal/sargon`'s `CrossCheckHistory` applies to Sargon.

**Status 2026-07-28: steps 1-11 are DONE**, each with the verification listed
(see §12.4 for the gate names). Step 10's second half — "then on hardware" —
is the only thing outstanding, and it is the only thing that needs hardware.

---

## 11. Risks

1. ~~**Biggest risk — no clock on the target machine (§6.2).**~~ **CLOSED
   2026-07-27** by `FT2_SOFTCLK`, an estimated elapsed-cycle clock in
   `checkclock` (+125 B CODE, +0.0073% cycles, tree-identical when off).
   Budget mode, timed levels and `FT2_ADAPT` all run on hardware now. What
   REMAINS a UI decision is which levels to offer. The estimator was
   RECALIBRATED under game conditions the same day (its first calibration had
   the bias backwards and the engine overran its clock by 17%); it is now
   lands at 0.94 of its allocation over a game at a 4 s level and 0.88 at a
   15 s one, with a 0.45-0.57 s resolution — so timed levels below ~4 s are not
   worth offering. NOTE the UI owns the safety margin (§6.2): the engine's cost
   table is raw, and the per-move limits must be scaled before they are poked. See docs/results.md for the error distribution and
   the residual budget dependence.
2. ~~**Character-generator verification gap.**~~ **CLOSED 2026-07-28, and it
   WAS A BUG.** goapple2 now models `ALTCHARSET` and carries a IIe character
   generator (`chargen`), so inverse lowercase is verified as dots. Doing
   that immediately caught the shipping build never selecting the alternate
   character set — on the primary set the board's `$60-$7F` bytes are
   flashing punctuation, so every black piece on a dark square would have
   blinked as a digit or bracket on real hardware. Fixed in `m8main` (five
   display stores, +15 B). `TestInverseVideoPixels` now asserts that
   *nothing on the whole screen* would flash or render as MouseText, which
   is what fails if the store is ever removed. The §3.2 fallback table
   remains available if a real IIe still disagrees.
3. **The 6502 vectors at `$FFFA-$FFFF` are RAM** once LC read is enabled. The UI
   must write them (RESET → UI entry, IRQ/BRK → a safe handler). Whether Ctrl-Reset
   on a IIe forces ROM back in before the vector fetch needs hardware
   confirmation; the emulator will not settle it.
4. **Symbol drift.** The UI links against addresses from `engine.lbl`, which move
   on every engine rebuild. The Makefile regenerates `engsyms.inc`, so the failure
   mode is a stale build, not a wrong one — but the rule must be a real
   dependency, not a manual step.
5. **Rebase.** The hot path is being optimised concurrently. Everything here is in
   new files (`asm/ui.s`, `asm/uitest.s`, `asm/uitest.cfg`, `internal/ui/`) plus
   two additive Makefile rules, so the rebase surface is a Makefile and nothing
   else.

---

## 12. What shipped (2026-07-28)

The design above is implemented. This section is the delta: the files, the
MEASURED byte budget, the places the implementation chose differently, and
the explicit list of what was deferred.

### 12.1 Files

| file | what |
|---|---|
| `asm/m8.s` | the UI: main loop, move entry and validation, game-over detection, the engine turn, the ID driver, the level/limit arithmetic, commands, painting. Two output files from one link. |
| `asm/m8.cfg` / `asm/m8t.cfg` | link configs. `LC` is capped at `$E000-$F6FF`, so overflowing the code budget is a **link error**, not a silent overwrite of the game history. `m8t.cfg` is the `-D HARNESSKBD` variant with distinct output names. |
| `asm/ui.s` | unchanged renderer, plus the LC memory map and the UI variable block. |
| `asm/engsyms.inc` | **generated**: the engine entry points the UI calls by address. |
| `internal/engsyms` + `cmd/genengsyms` | the generator. A fixed whitelist, so a renamed engine symbol is a build failure, never a stale address. |
| `internal/ui/m8.go` | the Go driver: boots the real image, types keys, reads the screen, reads/writes the position. |
| `internal/ui/*_test.go` | the gates (§12.4). |

**No engine source file is modified.** `asm/engine.bin` is byte-identical to a
build from a clean tree (md5 `58ef9645…`, verified by building `HEAD` into a
temp directory and `cmp`).

### 12.2 Boot

Two BLOADable files, essentially as designed:

```
m8boot.bin   57 B   BRUN at $0800   latch $C08B, copy $0900 -> $E000,
                                    install the engine's LC aux primitives
                                    at $D000, JMP $E000
m8.bin    4,222 B   BLOAD at $0900  the UI payload
```

`$0800` is `PIECESQ` and `$0900-$1FFF` is the per-ply undo/search arrays and
then `MOVESTACK` — all engine RAM that is garbage until the first search, so
the **permanent MAIN cost is 0 bytes**.

The design said `$0E00`. That is wrong by construction: `$0E00-$1FFF` is only
4,608 bytes, while the LC code budget is 5,888, so a UI grown past 4,608 B
would have been BLOADed straight over the resident opening book at `$2000`.
Loading at `$0900` makes the staging area and the code budget exactly the
same size, so the overrun cannot happen.
The payload's first three bytes are `JMP m8main`, so `m8boot.bin` does not
change when the UI does.

One thing the design did not price: a UI that drives `iterate` directly never
runs `engine.s`'s `$4000` entry, so it must **install `LCCODE` itself** or the
first transposition-table probe reads garbage. That is the second loop in the
copier (8 bytes).

#### 12.2.1 The bootable disk (`make dsk` → `asm/8fish.dsk`)

BLOAD is how the tests deliver the image; a **disk** is how a person gets it.
`make dsk` builds one with peterferrie's **Standard Delivery** loader, via
`diskii mksd` (`~/gh/a2audit/audit/build` is the precedent). Standard Delivery
is a boot sector plus a fast loader: it reads ONE contiguous span of sectors
into ONE contiguous span of memory and jumps to it. There is no DOS, no file
system, and nothing to type.

The consequence is that the gaps between the four pieces are paid for in
sectors, and `diskii mksd` refuses an image over **45,056 bytes**:

| base | image ($base to `engine.bin`'s last byte, `$BB99`) | SD spare | UI growth room |
|---|---:|---:|---:|
| `$0800` (the BLOAD layout) | 45,978 | **−922** | 1,666 |
| **`$0C00`** | **44,954** | **102** | **642** |
| `$0D00` | 44,698 | 358 | 386 |
| `$0F00` | 44,186 | 870 | **−126** (payload lands on the book) |

`internal/delivery`'s `TestBaseTradeoff` **re-derives that table from the real
file sizes** on every run and fails if `Base` is not the lowest base that fits,
so the choice stays the choice the sizes justify rather than the one that was
right in July.

**Neither number is a wall.** It is our disk: the 44 KB cap and the contiguous
span are properties of the *simplest* Standard Delivery layout — one shot, one
span — not of the medium. When they run out the answer is a different LOADER,
not a smaller program (chain-load, or **ProRWTS2**, which reads *and writes*
ProDOS files and would hand the UI back its full 5,888-byte LC budget). The
ledger below is a **tripwire** that forces that choice to be made deliberately;
it is not a budget to be defended. See §12.6 on save/load.

So the disk layout is **copier `$0C00`, payload staged `$0D00`** — the lowest
base that fits, i.e. the one that leaves the UI the most room. The staging
address is a **linker symbol** (`asm/m8sd.cfg`), not a constant in `asm/m8.s`,
so both layouts are built from one source: `m8sd.bin` is byte-identical to
`m8.bin`, and `m8sdboot.bin` differs from `m8boot.bin` in **exactly one byte**,
the payload's page. `TestDiskLayout` asserts both.

```
$0C00  m8sdboot.bin                  57 B   the copier (= --start)
$0D00  m8.bin                     4,222 B   staged; copied to $E000
$2000  internal/book/bookblob.bin  7,407 B  the resident opening book
$4000  engine.bin                31,642 B   the engine
       ------------------------------------
       image $0C00-$BB99         44,954 B in 176 sectors
```

**It boots, and not only in our own emulator.** `TestDiskBoots` starts the CPU
at `$C600` with nothing in RAM and lets the Disk II boot ROM, the Standard
Delivery loader and the copier do the rest; the screen it ends up painting is
byte-identical to the harness boot's. The same disk also boots **MAME's
`apple2ee`**, which shares no code with goapple2, and plays a move typed on
MAME's emulated IIe keyboard — with the pieces rendered by the real 342-0265-a
character ROM, which is the first independent confirmation that the
`ALTCHARSET` encoding is right.

**Both margins are gated, not commented.** There are 744 bytes of slack in
this layout and the two budgets grow from opposite ends — the engine spends the
SD spare, the UI spends the growth room, and raising the base trades one for
the other 256 bytes at a time. `internal/ui`'s **`TestDiskLedger`** prints both
numbers on every run and fails when either goes negative, naming which one and
saying what to do about it (change the loader).

### 12.3 Measured byte budget

From the linker's segment size and label deltas (`internal/ui`
`TestUIByteBudget`), against the 8,176 bytes of `$E000-$FFEF`:

| component | bytes |
|---|---|
| entry vector (fixed at `$E000`) | 3 |
| `asm/ui.s` renderer | 508 |
| `asm/entropy.inc` | 56 |
| cold start, main loop, whose-turn-is-it | 126 |
| position bookkeeping (new game, apply, hash history) | 164 |
| move generation / validation / legality | 333 |
| game state (legal count, mate/stalemate/50/repetition) | 105 |
| the engine's turn (seed, book probe, apply) | 147 |
| level table + the soft-clock margin rule | 296 |
| the UI's own iterative-deepening driver | 267 |
| line editor, move parsing, promotion prompt | 349 |
| commands | 230 |
| painting | 470 |
| think line + signed centipawn formatting | 280 |
| tables and strings | 888 |
| **UICODE total (measured)** | **4,222** |
| UI variables + screen buffers (`$F700`) | 256 |
| game history from/to/flags (`$F800-$FAFF`) | 768 |
| game hash history (`$FB00-$FEFF`) | 1,024 |
| **TOTAL** | **6,270 of 8,176 (77%)** |
| **FREE** | **1,906** |

**MAIN cost 0 B. TT cost 0 B. Book unmoved.** The design's estimate was
~4,011 B; the real thing is 6,270 B, the difference being almost entirely
strings, the level/limit arithmetic and the ID driver coming in heavier than
their *derived* rows.

### 12.4 The gates

All under `internal/ui`, plus the two engine-side ones:

| gate | what it proves |
|---|---|
| `TestDiskBoots` | **`asm/8fish.dsk` BOOTS.** Nothing is placed in memory: the CPU starts at `$C600`, the Disk II controller's own boot ROM reads track 0 sector 0 and jumps to it, Standard Delivery reads the other 176 sectors into `$0C00-$BB99`, the copier lifts the UI to `$E000` — and the screen it paints is **byte-identical** to the one `BootShipping` paints under the harness |
| `TestDiskPlays` | the disk-booted machine is a working program, not a picture: a move typed on the modelled IIe keyboard is answered from the book, and once out of book the engine searches and **writes the transposition table in AUXILIARY RAM** |
| `TestDiskLedger` | the two delivery margins (§12.2.1), printed every run and failed when either is exhausted |
| `TestDiskRoundTrip` | the built `.dsk` reads back, sector by sector, as the image handed to `diskii mksd` |
| `TestDiskLayout` | the disk build is the SAME UI: `m8sd.bin` == `m8.bin`, and the two copiers differ in exactly one byte |
| `delivery.TestBaseTradeoff` | the staging-base decision table, re-derived from the real file sizes: `Base` must be the lowest one that fits |
| `delivery.TestMarginsCanFail` | the gate on the gate — both margins really do go negative when the pieces grow |
| `delivery.TestSectorOffset` | Standard Delivery's interleave is a bijection onto the disk's sectors and never lands on the boot sector |
| `TestM8Boot` | the shipping boot path runs, the UI executes from `$E000`, and it paints a start position and waits for a key |
| `TestShippingImageBoots` | the REAL-keyboard build (not the HARNESSKBD one) boots, paints, blocks in `entkey`'s poll loop with `ENTCNT` spinning — the engine's only source of entropy — and has selected the alternate character set |
| `TestShippingKeyboard` | a key pressed on the modelled IIe keyboard reaches the SHIPPING image through `$C000`/`$C010`: echoed, backspaced, applied, and folded into `ENTROPY` |
| `TestShippingScreenParity` | the shipping and HARNESSKBD builds paint **byte-identical screens after every keystroke** across 68 keys of moves, a capture, castling, an illegal move, a takeback, a level change and help |
| `TestShippingEngineParity` | the two builds play the same **engine** moves and end with the same `ENTROPY`, so the two keyboard paths collect identical entropy |
| `TestShippingFullGame` | **the SHIPPING image plays a complete game to a real termination through `$C000`/`$C010`**, refereed by refchess, screen-compared to the harness build at every keystroke |
| `TestInverseVideoPixels` | the checkerboard as DOTS through `goapple2/chargen`, with the character set the image actually selected: all 128 board cells inverse iff dark, the piece glyphs dot-for-dot, and nothing on the screen that would flash or render as MouseText |
| `TestPrimaryCharsetWouldBeWrong` | why the `$C00F` store is load-bearing: the same byte is a flashing `.` on the primary set and an inverse `n` on the alternate one |
| `TestStartPosition` | the hand-written 64-byte start image is byte-identical to `chesstest.ParseFEN`, piece-list slots included (the Zobrist hash, and therefore every book key, is computed from those bytes) |
| `TestMoveEntry` | a typed opening produces refchess's position at every ply, and the move panel lists it |
| `TestSpecialMoves` | castling both ways, en passant, both promotion spellings — with zero UI chess code |
| `TestPromotionPrompt` | a promotion typed without its fifth character asks, and the answer completes the move |
| `TestLegalityAgainstRefchess` | 124 legal moves accepted with the right resulting position, 240 illegal ones rejected with a clear message and no state change, across six positions |
| `TestBadInput` | garbage is named, not swallowed, and never touches the game |
| `TestTerminations` | checkmate (two ways), stalemate, the 50-move rule and threefold repetition, each with the screen text it produces, and a move typed afterwards refused |
| `TestTakeback` | replaying from the start position restores the exact prior FEN, all the way back to move 1 |
| `TestCommands` | N / T / R / L / S / ? |
| `TestDrawOffer` | the engine accepts a draw only when its last search said it was losing |
| `TestEngineParity` | **the UI-driven engine plays the move the `$4000`-entry engine plays**, at four positions and depths 2-5 |
| `TestSoftClockLimits` | all five limits, at all nine levels, against `chesstest.SoftClockMargin`'s reference |
| `TestTimedLevel` | one move on the ESTIMATED clock with the harness clock trap disabled — hardware semantics — coming in under its allocation |
| `TestBookIntegration` | the resident book is probed instead of searched, and the opening name streamed from the blob matches `internal/book` |
| `TestUIByteBudget` | the measured budget above, and that the components sum to the linker's segment size |
| `TestFullGame` | **a complete game to a real termination, refereed ply by ply by refchess** |
| `TestRenderPosition` / `TestRenderCost` | the pre-existing renderer gates, unchanged |
| `chesstest.TestMicroAB` | the engine's tree fingerprint, byte-identical to a clean-tree run |
| `engine.bin` md5 | identical to a `git archive HEAD` build |

### 12.5 Where the implementation differs from the design

1. **Levels.** 1-4 are fixed depth 2/3/4/5 (`BUDGET = 0`, no clock read
   anywhere); 5-9 are timed at 4/8/15/30/60 s with `FT2_SOFTCLK` **and**
   `FT2_ADAPT`. The design left adaptive effort open as a product decision;
   the ceiling arithmetic turned out to be three shifts and an add off the
   already-scaled base (`CEILMAX = 4x`, `UNSTCEIL = 3x`, `MINSPEND = /4`), so
   it shipped.
2. **The margin rule is implemented exactly as specified** — octave of the
   budget, `KTAB` lookup, one 24x8 shift-and-add, no division — and applied to
   the BASE before the ceilings are derived from it, which is what makes all
   five limits share one margin. `ABORTL` inherits it (the engine derives
   `ABORTL` from `BUDGET`, or from `CEILMAX` under `FT2_ADAPT`).
3. **A third side mode.** `S` cycles WHITE -> BLACK -> **TWO PLAYERS**.
   Referee mode never searches: it validates and displays. It is what you want
   for playing a friend or setting a position up by hand, and it is what makes
   the test suite able to drive both sides.
4. **Draws are adjudicated, not claimed.** A threefold repetition or a
   halfmove clock of 100 ends the game by itself, which is what a casual
   player expects. `D` is therefore a genuine draw OFFER: the engine accepts
   only if its last completed search scored it below -150 cp.
5. **Command/move disambiguation is by LENGTH.** A one-character line is a
   command, a four- or five-character line is a move. That resolves `d`
   (offer draw) against `d2d4` without a special case, and every letter is
   case-folded so CAPS LOCK does not matter.
6. **`uiresultmsg` yields to a fresher message**, so "GAME OVER - N STARTS A
   NEW ONE" is not wiped out by the next repaint.
7. **The engine's own move is re-validated** against the generator before it
   is played (one generate plus one make/unmake, invisible next to a search).
   The search always returns a legal move and the book is keyed on 32 bits, so
   this never fires — but "never corrupt the position" is cheaper to guarantee
   than to debug.

### 12.6 Deferred, with prices

Nothing below is needed to play a game.

| deferred | why | price to add |
|---|---|---|
| **Board flip when the human plays Black** | `uiboard` walks `a8` downward with a fixed square-colour phase | ~40 B: a direction byte and a phase seed in `uiboard`, plus reversing `uicoords` |
| **Hi-res board** (§3.1) | costs information, not bytes: mixed mode leaves 4 text rows and the panel/status/prompt no longer fit at once | ~1,850 B, fits in the 1,906 B free; book moves to LC bank 2 |
| **Insufficient-material draw** (KK, KNK, KBK) | the engine detects it *inside* the search; at the root the UI does not | ~50 B: the same piece-list scan `search.s` already has, hoisted |
| **Fivefold / 75-move automatic ends** | threefold and 50 already adjudicate | ~15 B (two constants) |
| **Mate distance in the think line** | shows `+MATE` / `-MATE`, not `#4` | ~40 B: one subtract from `MATE` and a halve |
| **`FT2_ADAPT`'s per-GAME bank** | the host bridge banks unspent time across a game; on device each move gets a flat allocation | ~120 B: a signed 24-bit bank, income accrual and `min(4*base, income+bank)` |
| **Position setup / FEN entry** | two-player mode plus takeback covers replaying a game | ~250 B for a FEN parser, or ~150 B for a cursor-driven piece placer |
| **Cursor / joystick move entry** | typed entry wins on code, notation, entropy and errors (§5.1) | ~200-260 B on top of the same validator |
| **A saved game / disk I/O** | **Standard Delivery is READ-ONLY** — it boots and loads, and has no file system and no writer, so this is impossible on the shipped mechanism rather than merely unimplemented. **ProRWTS2** (peterferrie) is the intended successor because it reads *and writes* ProDOS files: one mechanism for boot + load + save, and it drops the contiguous-span squeeze (§12.2.1) as a side effect. Explicitly NOT to be hand-rolled: a Disk II *writer* means 6-and-2 nibble encoding and write-splice timing, and getting it wrong corrupts disks instead of failing cleanly | unpriced; a separate task |
| **Real-hardware validation** | **narrowed again 2026-07-28.** The disk exists and boots (§12.2.1): `TestDiskBoots` runs the real Disk II boot ROM against the real nybblised image on an Apple IIe memory model, `TestDiskPlays` plays a move on it, and MAME's `apple2ee` boots the same disk with the real character ROM. What remains is genuinely a hardware question: video timing, drive speed, and Ctrl-Reset | **`make dsk`**, then a IIe |
| **80-column / `80STORE`** | goapple2's `iie` deliberately leaves `80STORE` unmodelled (a compare on the hottest path for a switch nobody throws); `m8main` writes `PAGE1`, which pulls `$0400-$07FF` back to MAIN if firmware left it on | an emulator feature, not a UI one |
| **MouseText glyphs** | `chargen` names MouseText but has no shapes for it; this UI does not use `$40-$5F` | 32 glyph bitmaps in goapple2 |
| **Ctrl-Reset behaviour** (§11 risk 3) | the UI writes `$FFFA-$FFFF`, but whether a IIe forces ROM back in before the reset vector fetch is a hardware question | nothing to write; a hardware answer |
