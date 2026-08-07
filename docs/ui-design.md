# 8fish on-device user interface — design

Status: **SHIPPING, WITH THE DOUBLE-HI-RES BOARD** (2026-07-31). See **§13**,
which is the current state of the delivery and corrects §3.1.1's page-table
arithmetic; §12 remains the 2026-07-28 text-UI record.

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
| LC budget | **6,476 B of 8,176 B measured** (4,428 B code+data, 2,048 B RAM arrays, 256 B variables); **1,700 B free** |

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
| double hi-res | MAIN **and** AUX `$2000-$3FFF`, and **zero TT entries** (the table now starts at aux `$4000`) | yes | **347 code + 96 tables measured**, plus 456 B built at init and a 1,824 B tile blob | **193,667 cyc measured** (189 ms) | **BUILT AND PROVEN; blocked on DELIVERY, not on RAM** |

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
- **Double hi-res.** ~~Costs a quarter of the transposition table.~~ The
  *reasoning* here was wrong, but the verdict happens to survive. Investigated
  2026-07-31; see the measurements below before repeating either mistake.

  **The "25% of the TT" figure was an artefact of the TT's base address, not a
  property of DHGR.** DHGR needs aux `$2000-$3FFF`; the table merely happened to
  start at `$0200` and so happened to span it. The TT is the only aux consumer
  in the codebase, and `TTBASE` is a single constant in `asm/defs.inc` —
  `asm/tt.s` reaches it only through `adc #>TTBASE`, so relocating the table
  costs **2 bytes of `engine.bin`** (the two inlined immediates in
  `ttprobe`/`ttstore`) and nothing else. Relocation was measured to be
  transparent: with `TTBASE` at `$0400`, `$2000`, `$3000` and `$3F00`,
  `internal/chesstest` passes exactly as at `$0200`.

  **And the obvious destination DOES fit — exactly.** (This paragraph replaces
  a second wrong answer, recorded 2026-07-31 and corrected the same day. The
  first version of it said the table missed by one page. It does not miss at
  all.) The arithmetic `$C000 - $4000 = $8000 = 4096 x 8` is an exact fit for
  the table above the DHGR aux half, and it holds:

  ```
  aux $0200-$3FFF   -----     free (DHGR aux half is $2000-$3FFF)
  aux $4000-$BFFF   32,768 B  the transposition table, all 4096 entries
  ```

  `TTBASE = $4000` really did fail `internal/chesstest` hard — searches stopped
  terminating — and the earlier note correctly refused to call that a paper
  violation. But it was a **harness bug, not a memory-map constraint**, and the
  root cause is now demonstrated (D8 amendment, 2026-07-31):

  > A 6502 `sta (zp),Y` performs a hardware-accurate DUMMY READ of the target
  > address one cycle before the write. That read is a real bus cycle, so it
  > obeys **RAMRD, not RAMWRT**. `ttstore` writes aux with RAMWRT on and RAMRD
  > off (D4), so each of its eight `sta (TTPTR),y` stores emitted a *main*-bank
  > read of the address it was writing to *aux*. At `TTBASE = $4000`, entry
  > 4094 covers `$BFF0-$BFF7`, so storing it read main `$BFF1` (pops an input
  > byte) and main `$BFF2` (sets `WaitingForInput`, which makes `Machine.Run`
  > return). On real hardware those are plain RAM and the dummy read does
  > nothing; the hazard was the harness's own invention.

  The read traps are now gated on RAMWRT as well as RAMRD — symmetric with the
  store traps, which have always been — so aux is usable to `$BFFF`. Measured
  with `TTBASE = $4000`: `engine.bin` is byte-identical in size (31,906 B),
  `TestMicroAB` matches `microABGolden` (the search tree is unchanged, as a
  pure relocation must be), and all five parity gates plus `internal/ui`,
  `internal/ucibridge`, `internal/entropy` and `internal/delivery` are green.

  So **DHGR and a full 4096-entry TT are NOT mutually exclusive.** The cost of
  double hi-res to the engine is: two `adc` immediates in `engine.bin`
  (`$02` -> `$40`), zero table entries, zero Elo. The reject verdict above
  stands or falls on the UI arguments alone — the memory argument is gone.

  A bonus the relocation carries: `asm/m8.s` documents a hardware-only hazard
  in which 80STORE (turned on by the 80-column firmware, i.e. by any
  BLOAD/BRUN from BASIC.SYSTEM in 80-column mode) makes aux `$0400-$07FF`
  follow PAGE2 and ignore RAMRD/RAMWRT, so TT slots in that range would land
  on the main text page. At `TTBASE = $4000` the table no longer covers
  `$0400-$07FF` *or* `$2000-$3FFF`, so that failure mode disappears rather
  than being fended off by a `sta CLR80STORE`.

  Two lessons, both paid for twice. When a cost is attributed to a *feature*,
  check whether it is really attributable to the current *arrangement*. And
  when a rule in the memory map is corroborated by a real failure, that is not
  yet evidence the rule explains the failure — find the mechanism before you
  price the trade, because a documented precaution makes a very convincing
  false cause.

- **Hi-res page 1, and the actual artwork.** *(The 44 x 21 source grid and
  its top-2-row trim, below, were SUPERSEDED on 2026-08-06: the board was
  redrawn at 42 x 19 as `assets/chess2-dazzledraw-save.bin`, so there is no
  trim any more and the 28 clipped pixels are no longer paid. See §17 and
  `assets/README.md`. Everything else here still holds.)* The 28-px/4-byte
  square priced above was sized for *invented* glyphs. The real asset
  (`assets/chess-dazzledraw-save.bin`, hand-drawn in DazzleDraw) is 560-wide
  DHGR with **44 x 21** squares, i.e. 22 px at 280 — neither 28 nor
  byte-aligned. Two further facts any hi-res attempt must budget for, both
  measured from the asset:
  - The board is **172 scanlines** as drawn, but mixed mode leaves only 160.
    Dropping the top 2 rows of each square (the most redundant: 98-99%
    identical to their neighbours, 28 deviating pixels across all 64 squares)
    gives 19 rows/square = 152 + borders = 154, which fits with 6 to spare.
  - Columns 0-7 and 35-43 of every square are **pure background in all 64
    squares**, so dropping 2 of them is lossless — and dropping an *even*
    number preserves the 1-on-1-off dither phase exactly. 42 px = 6 DHGR bytes
    (3 aux + 3 main), which makes every square byte-aligned and removes
    pre-shifting entirely.

### 3.1.1 Double hi-res, BUILT — and where it actually stops

Implemented 2026-07-31 (`asm/dhgr.s`, `cmd/gentiles`, `internal/tiles`). Every
number in the bullet above was re-measured against the pixels and held; the
generator now ASSERTS each of them, so re-drawing the board fails the build
instead of rendering garbage on the IIe.

What the implementation added to the analysis:

- **42 is a multiple of 14, and that is the whole trick.** A 14-px pitch keeps
  every file at the *same* bit phase, so a tile is horizontally
  position-independent. `assets/README.md`'s "at most 8 bit-phases per sprite"
  is unnecessary — there is exactly one phase. The trim being EVEN is what
  preserves the dither, which is globally locked to "even absolute x is lit";
  in DHGR bytes that is the constant pair aux `$55`, main `$2A`.
- **Byte columns 0 and 5 of every tile are background too**, so only the middle
  four are stored: 4 B x 19 rows = **76 B/tile, 24 tiles = 1,824 B** (not the
  2,964 B a 6-byte row implies). The two empty-square tiles are synthesised at
  init from the background constants.
- **The artwork really does carry all 24 combinations**: rows 2 and 5 hold the
  king and queen *swapped* relative to the back rank, which is what supplies
  king-on-dark / queen-on-light and their mirrors. Nothing is hand-maintained.
- **Byte cost, measured from the link map** (`asm/dhgrtest.lbl`):
  `dhinit` 105 B + `dhboard` 242 B = **347 B of code**; `TILEIDX`/`TILEOFFL`/
  `TILEOFFH` = **96 B** of generated index tables. Those 443 B are all that
  rides in the UI payload. The scanline-base and blank-tile tables are **456 B
  built at init**, and the artwork is **1,824 B**; those 2,280 B live in LC
  bank 2 (`$D000-$DFFF`, 4,096 B and until now entirely unused), leaving
  1,816 B spare there.
- **Cost: 193,667 cycles (189 ms) per whole-board repaint**, measured by
  difference. That is ~2x the 90,000 this section previously derived — the
  derivation assumed ~12 cyc/store and the straightforward loop runs ~26 — but
  it is still 0.6% of a 30 s move, so repaint-everything holds and partial
  repaints stay designed out. There is easy headroom in an unrolled inner loop
  if it is ever wanted.
- **`sta CLR80STORE` is now LOAD-BEARING, not defence in depth.** (The
  "no test in this repo can catch this" half of the note below is **out of
  date as of 2026-07-31**: goapple2's IIe model implements 80STORE, AN3 and
  `DHires()` now, and `internal/ui` asserts all three on the booted disk.
  See §13.5.) This renderer
  reaches the aux half through RAMWRT (the discipline D4 already uses for the
  TT), not through 80STORE/PAGE2. With 80STORE *on*, `$2000-$3FFF` follows
  PAGE2 and ignores RAMWRT, so the aux half would silently land in main. The
  TT relocation removed the *other* 80STORE hazard (the table no longer covers
  `$0400-$07FF` or `$2000-$3FFF` at all), but this one is new and points the
  opposite way. Note goapple2 models neither 80STORE nor AN3/DHIRES, so **no
  test in this repo can catch getting those switches wrong** — same
  hardware-only exposure `asm/m8.s` already documents.

**It is proven by parity, not by eyeball.** `TestDHGRRenderParity` asserts the
6502's whole 16,384-byte screen is byte-identical to `internal/tiles`'
independent Go model, over four positions including one that exercises all 24
tiles — the same discipline as the engine's search gates.

**What blocks shipping it is DELIVERY, and the wall is not the one we expected.**
Two independent walls, both arithmetic:

> **SUPERSEDED BY §13 (2026-07-31): both walls are gone, and one of the two
> numbers below is wrong.** The page table is `$084F-$08FF` = **176** sectors,
> not 177: the `LDA $084E` at `$0805` is pre-incremented at `$0802`. And the
> table is a LIST OF PAGES, so stage 1 scatter-loads disjoint spans for free.
> The board ships; read §13 rather than re-deriving any of this.

1. *No room in main RAM at load time.* Standard Delivery loads one contiguous
   span into main. At load time the free main holes are 164 B (above the staged
   UI payload), 785 B (above the book) and 1,102 B (above the engine, below the
   `$BFF0` traps). The largest is **1,102 B < 1,824 B**: the tile blob does not
   fit anywhere, at all. It *can* only live in the space the book VACATES when
   it moves to aux — which by definition is not free until init has run.
2. *No room on the sector list.* Perfectly packed — no gaps at all, which the
   single-span layout cannot actually achieve — the pieces are now
   57 + 4,444 + 7,407 + 1,824 + 31,906 = **45,638 B**, and that 4,444 is
   today's `m8.bin`, i.e. it does NOT yet include the renderer (another ~540 B
   of code and tables, which would also overrun the staged payload's 164 B of
   room below the book — the same squeeze a third time). `diskii mksd` caps an
   image at 45,056 B, and the real ceiling is the boot sector itself: Standard
   Delivery's loader is a **page table at `$084E-$08FF`** (one destination page
   per sector, `$C0` terminating), self-modified through the low byte of the
   `LDA $084E` at `$0806`. 178 bytes of table minus the terminator is **177
   sectors = 45,312 B maximum, ever**. We are 326 B over that, so no repacking,
   no base move and no gap-squeezing gets there.

Both walls say the same thing, and it is the thing `internal/delivery`'s own
doc-comment predicted: *when the margins run out the answer is a different
LOADER, not a smaller program.* The tiles must arrive **after** the book has
been lifted to aux — i.e. sequentially chain-loaded into the freed
`$2000-$3FFF`. That is mechanism, not capacity, and the mechanism is cheap
because the loader survives: our image starts at `$0D00`, so the boot sector's
code at `$0800-$08FF` is never overwritten, its sequential read state
(`$27`, `$3D`, `$40`, `$41`) is intact, and re-entering at `$0802` with a fresh
page table written into page `$08` continues reading from the next sector.
The cost is the Go side (a second span in `delivery.Image`/`SectorOffset` and a
post-`mksd` patch of the table) plus banking ROM back in around the `$C65C`
read — NOT a hand-rolled sector reader.

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
deliberately biased. **RE-FITTED ABOVE OCTAVE 15 ON 2026-07-30** — the tail was
a guess, and it was costing LEVEL 7 and LEVEL 8 about a tenth of their clock.
What a UI can promise now, measured over all twenty curated openings against an
exact-clock control in the same run (own true cycles / own allocation):

| level | engine spends | an exact clock spends | equal-spend ratio |
|---|---:|---:|---:|
| 5 (4 s) | 0.912 | 0.926 | 0.985 |
| 7 (15 s) | 0.891 | 0.896 | 0.994 |
| 8 (30 s) | 0.923 | 0.955 | 0.967 |
| 9 (60 s) | 0.866 | 0.850 | 1.019 |

Under 1.0 at every level, so it never flags, and within 3.5% of what a machine
with a real clock would spend.

**★ The UI must implement one rule.** The safety bias is NOT in the engine: the
cost table holds the raw measured cost, and the bias is applied by whoever
installs the per-move limits. Before poking BUDGET (and, with `FT2_ADAPT`,
CEILMAX / UNSTCEIL / MINSPEND — all four, sharing ONE margin taken from the
base allocation), scale by the octave of the budget:

| budget | poked value |
|---|---|
| ≤ ~8 s | `BUDGET × 101 >> 7` (margin 127%) |
| ~8-16 s | `BUDGET × 113 >> 7` (margin 113%) |
| > ~16 s | `BUDGET × 139 >> 7` (margin **92%** — the poked budget is LARGER) |

**★ The divisor is 128, changed 2026-07-30, and the last row is why.** Above
~16 s the estimator reads 3-5% HIGH (deep trees take far more TT cutoffs, and
a TT-cutoff node is counted but nearly free) and `idloop`'s `now + 2×cost`
threshold doubles that into a ~10% SPEND deficit: measured over all twenty
curated openings, LEVEL 7 (15 s) spent **0.9206** and LEVEL 8 (30 s) **0.9035**
of what an exact clock spends. Correcting that needs a margin BELOW 100%,
i.e. `25600/92 = 278` over 256 — which does not fit in a byte. Over 128 it
does, and the shipped `asl/rol/rol` that pre-doubles the addend costs six
bytes. A margin below 100 is safe here and only here: it lands adherence at
~0.94, still under the exact clock's own 0.955.

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
   proving the UI-driven engine plays byte-identical chess. **The comparison is
   only well-posed with the dither OFF and the SHIPPED feature mask on both
   sides**, and the reference's mask must be constructed from `defs.inc`, not
   peeked out of the image under test; getting either wrong has now produced one
   gate that passed by luck and one that reported a nonexistent bug
   (docs/results.md 2026-07-29).
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
3. ~~**The 6502 vectors at `$FFFA-$FFFF` are RAM** once LC read is enabled.~~
   **RESOLVED 2026-07-28 (§12.7).** NMI and IRQ/BRK are RAM and are written.
   RESET is not reachable: a hardware reset on a IIe DISABLES the built-in
   language card — which is exactly why Apple Pascal could use Ctrl-Reset as
   a warm start — so `$FFFC` is fetched from ROM and the UI's copy was dead
   code. What Ctrl-Reset actually does is Autostart's warm/cold decision on
   the power-up byte at `$03F4`, and that decision was landing on WARM: a
   `jmp ($03F2)` into an Applesoft whose entire zero page the engine had
   trampled. `m8main` now invalidates the byte, so Ctrl-Reset is a
   deterministic COLD boot — i.e. it restarts 8fish off the disk in the
   drive.
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
m8.bin    4,428 B   BLOAD at $0900  the UI payload
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
| `$0800` (the BLOAD layout) | 45,978 | **−922** | 1,460 |
| **`$0C00`** | **44,954** | **102** | **436** |
| `$0D00` | 44,698 | 358 | 180 |
| `$0F00` | 44,186 | 870 | **−332** (payload lands on the book) |

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
$0D00  m8.bin                     4,428 B   staged; copied to $E000
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

**Both margins are gated, not commented.** There are 538 bytes of slack in
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
| `asm/ui.s` renderer (incl. `uidec3`/`uid2z`) | 569 |
| `asm/entropy.inc` | 56 |
| cold start, main loop, machine check, whose-turn-is-it | 208 |
| position bookkeeping (new game, apply, hash history) | 181 |
| move generation / validation / legality | 333 |
| game state (legal count, mate/stalemate/50/repetition) | 92 |
| the engine's turn (seed, book probe, apply) | 147 |
| level table + the soft-clock margin rule | 296 |
| the UI's own iterative-deepening driver | 267 |
| line editor, move parsing, promotion prompt | 349 |
| commands | 262 |
| painting | 475 |
| think line + signed centipawn formatting | 256 |
| tables and strings | 934 |
| **UICODE total (measured)** | **4,428** |
| UI variables + screen buffers (`$F700`) | 256 |
| game history from/to/flags (`$F800-$FAFF`) | 768 |
| game hash history (`$FB00-$FEFF`) | 1,024 |
| **TOTAL** | **6,476 of 8,176 (79%)** |
| **FREE** | **1,700** |

**MAIN cost 0 B. TT cost 0 B. Book unmoved.** The design's estimate was
~4,011 B; the real thing is 6,476 B, the difference being almost entirely
strings, the level/limit arithmetic and the ID driver coming in heavier than
their *derived* rows. The 2026-07-28 hardware pass (§12.7) added 195 B of
that total.

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
| `TestRulesCorpus` / `TestRulesRandomSweep` / `TestRulesPlaythrough` | the UI's own referee **differentially against refchess**: 28 hand-built castling / en-passant / promotion / mate corners, ~800 random positions, and 12 random games TYPED IN with the whole position — rights, ep square, halfmove clock — compared every ply |
| `TestThreefoldExactPly` / `TestThreefoldRespectsCastlingRights` | the draw fires on the THIRD occurrence, and the repetition hash is position-based: identical placement with different castling rights is not a repetition |
| `TestFiftyMoveBoundary` | halfmoves not fullmoves, reset on capture and on a pawn move, and **checkmate on the hundredth halfmove is a mate, not a draw** |
| `TestLongGameIsNotDrawn` | a legal 262-ply game that meets no draw rule is NEVER declared over; the position still tracks refchess after the arrays fill; `UIHCNT` pins at 255 instead of wrapping; canaries prove nothing is written past any array's last byte; and takeback is refused in words rather than replaying to the wrong position |
| `TestMoveNumberOverNinetyNine` | the move panel's three-column number field: 99 stays right-aligned, 100 renders as `100` and not `1 0`, and no panel line exceeds its 19 columns |
| `TestRefusesMachinesWithoutAuxRAM` | the machine check (§12.7), on three modelled machines: a 128K IIe comes up, an Apple ][+ (aux switches that are not switches) and a 64K IIe (switches with no RAM behind them) both get `8FISH NEEDS A 128K APPLE IIE` and halt with the engine image untouched |
| `TestColdStartHardening` | `CLD` is in the first bytes of `m8main`; a key held down through the boot is not eaten as the first character typed; and the Autostart power-up byte is INVALID, so Ctrl-Reset cold boots instead of warm-starting into a trashed Applesoft |
| `TestDiskQuitReboots` | `Q` really quits: on the disk-booted machine it leaves the language card through the ROM's own RESET vector, and (with the ][+ Autostart ROM, whose reset path this emulator can execute end to end) comes all the way back round through the Standard Delivery copier to a fresh 8fish |
| `TestTakebackAgainstTheEngine` / `TestTakebackAcrossABookMove` | the two-plies-at-a-time branch of `cmd_take`, which referee mode cannot reach (`UIHUMAN` is `$FF` there, so it always steps back one) |
| `TestResignAwardsTheRightSide` / `TestDrawOfferIsNotAnsweredFromARetractedSearch` | the two result-reporting bugs found by that pass, in all three side modes |
| `TestCommands` | N / T / R / L / S / ? |
| `TestDrawOffer` | the engine accepts a draw only when its last search said it was losing |
| `TestEngineParity` | **the UI-driven engine plays the move the `$4000`-entry engine plays**, at four positions and depths 2-5, dither OFF on both sides and the SHIPPED feature mask on both sides. The reference's configuration is constructed from `defs.inc` and cross-checked against the booted image, never peeked from it: this gate had been comparing two different ENGINES (see docs/results.md 2026-07-29) |
| `TestBookRandomIsFullWidth` | `uibookrnd`'s GF(2) matrix has rank 32, so all 32 bits of collected entropy reach the book's weighted pick |
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
| **Hi-res board** (§3.1) | costs information, not bytes: mixed mode leaves 4 text rows and the panel/status/prompt no longer fit at once | ~1,850 B, fits in the 1,700 B free only just; book moves to LC bank 2 |
| **Insufficient-material draw** (KK, KNK, KBK) | the engine detects it *inside* the search; at the root the UI does not | ~50 B: the same piece-list scan `search.s` already has, hoisted |
| **Fivefold / 75-move automatic ends** | threefold and 50 already adjudicate | ~15 B (two constants) |
| **RECORDING games longer than 255 plies** | no longer a draw and no longer a stop (§12.7): past ply 255 the game continues and the RECORD degrades — the move panel stops growing, takeback is refused in words, and repetition detection can only see the first 255 plies (so it can only UNDER-count). Mate, stalemate and the 50-move rule are untouched, which is everything needed to finish the game | 4 B of LC RAM per extra recorded ply (1 history byte is already spare in each of three pages; the four hash pages are the cost). The 1,700 free bytes are room for ~420 more plies, but past 256 the arrays stop being page-indexable — that is the real price, not the RAM |
| **Mate distance in the think line** | shows `+MATE` / `-MATE`, not `#4` | ~40 B: one subtract from `MATE` and a halve |
| **`FT2_ADAPT`'s per-GAME bank** | the host bridge banks unspent time across a game; on device each move gets a flat allocation | ~120 B: a signed 24-bit bank, income accrual and `min(4*base, income+bank)` |
| **Position setup / FEN entry** | two-player mode plus takeback covers replaying a game | ~250 B for a FEN parser, or ~150 B for a cursor-driven piece placer |
| **Cursor / joystick move entry** | typed entry wins on code, notation, entropy and errors (§5.1) | ~200-260 B on top of the same validator |
| **A saved game / disk I/O** | **Standard Delivery is READ-ONLY** — it boots and loads, and has no file system and no writer, so this is impossible on the shipped mechanism rather than merely unimplemented. **ProRWTS2** (peterferrie) is the intended successor because it reads *and writes* ProDOS files: one mechanism for boot + load + save, and it drops the contiguous-span squeeze (§12.2.1) as a side effect. Explicitly NOT to be hand-rolled: a Disk II *writer* means 6-and-2 nibble encoding and write-splice timing, and getting it wrong corrupts disks instead of failing cleanly | unpriced; a separate task |
| **Real-hardware validation** | **narrowed again 2026-07-28.** The disk exists and boots (§12.2.1): `TestDiskBoots` runs the real Disk II boot ROM against the real nybblised image on an Apple IIe memory model, `TestDiskPlays` plays a move on it, and MAME's `apple2ee` boots the same disk with the real character ROM. What remains is genuinely a hardware question: video timing, drive speed, and Ctrl-Reset | **`make dsk`**, then a IIe |
| **80-column / `80STORE`** | goapple2's `iie` deliberately leaves `80STORE` unmodelled (a compare on the hottest path for a switch nobody throws); `m8main` writes `PAGE1`, which pulls `$0400-$07FF` back to MAIN if firmware left it on | an emulator feature, not a UI one |
| **MouseText glyphs** | `chargen` names MouseText but has no shapes for it; this UI does not use `$40-$5F` | 32 glyph bitmaps in goapple2 |
| **Ctrl-Reset behaviour** (§11 risk 3) | **no longer deferred: see §12.7.** A IIe's reset disables the language card, so the vector comes from ROM and Autostart's power-up byte decides everything; `m8main` invalidates it, making Ctrl-Reset a cold boot. What is still a hardware answer is only the CONFIRMATION — no emulator here models the reset line | **done**; confirm on a IIe |

### 12.7 The hardware pass (2026-07-28)

The four-lens adversarial review's open findings, applied. **+195 B of UI**
(4,233 -> 4,428), which spends the disk's UI growth room down from 631 B to
436 B; the SD spare is untouched at 102 B, and `engine.bin` is byte-identical
to a `git archive HEAD` build.

| finding | what shipped | bytes |
|---|---:|---:|
| **Ctrl-Reset returned to a trashed Applesoft.** A IIe's hardware reset disables the language card, so `$FFFC` comes from ROM and the UI's copy was dead code. Autostart then passed its power-up test (8fish uses only `$0300-$030C`, and Standard Delivery never touches page 3) and warm-started into an Applesoft whose zero page the engine had trampled | `m8main` invalidates the power-up byte (`$03F3` = 0, `$03F4` = `$FF`), so Ctrl-Reset is a deterministic COLD boot: it restarts 8fish off the disk. The dead `$FFFC/$FFFD` writes are gone | **+4** |
| **No machine detection.** On a ][+ the aux switches are no-ops, so every TT access at aux `$0200-$81FF` lands in MAIN — over the book at `$2000` and the engine at `$4000`: the engine overwrites itself mid-search. On a 64K IIe the switches work but nothing is behind them, and at ~10^7 probes a game roughly one false hit per game slips the 24-bit verify | `m8machine`: a TWO-SIDED probe (`$A5` to MAIN `$0300`, `$5A` to AUX `$0300`; aux must read back `$5A` **and** main must still read `$A5`). On failure, `8FISH NEEDS A 128K APPLE IIE` and halt — display switches thrown first, so the message is visible. The probe forces the switches to MAIN before it starts, and puts aux `$0300` back to zero afterwards, because that address is INSIDE the transposition table | **+100** |
| **`Q-QUIT` did not quit.** It stored to `$BFFF` (the harness exit trap; plain RAM on hardware) and fell into `m8new`, silently discarding the game | `cmd_quit` copies six bytes to `$0300` — the switch to ROM cannot be made from code executing IN the language card — reads `$C082` and jumps through the ROM's RESET vector: with the power-up byte invalidated, a clean cold boot, which is what QUIT means on a machine with no resident OS | **+12** |
| **Move numbers >= 99 rendered as punctuation.** `uidec2` is a two-digit routine, so move 100 came out `:0` | `uidec3`, a three-column right-aligned field in `ui.s` (it cannot call `m8.s`'s `uid2z`, because `ui.s` is also linked standalone by `uitest.s` — so `uid2z` moved to `ui.s` and is now shared). `uimoves` also had to `ROR` rather than `LSR`: at `UIHCNT` = 255 the `+1` carries out | **+37** |
| **A legal game was force-drawn at ply 250.** `RES_LONG` in any position — a wrong RESULT, not a cosmetic limit | The result code is GONE. The game continues and the RECORD degrades: at ply 255 the move is played into the last slot, `UIHCNT` pins there (nothing wraps, nothing is written past index 255), `UIHFULL` comes up, and takeback is refused in words. Repetition can then only under-count; the 50-move rule, mate and stalemate are unaffected | **+33** |
| **Minor hardening.** No `CLD` at entry; a key held through the boot was eaten as the first character typed; `RES_ERR` printed `INTERNAL ERROR` *and* `GAME DRAWN` | `cld`, `bit $C010`, and a `RES_ERR` guard in `uiresultmsg` | **+9** |

What is deliberately NOT claimed: the emulator cannot perform a real
Ctrl-Reset (goapple2's IIe model does not implement the reset line's effect
on the language card), so finding 1 is gated on the STATE it leaves — the
power-up byte — plus the `Q` path, which exercises the same ROM decision by
jumping through the same vector. And `TestDiskQuitReboots` can only complete
the round trip with the Apple ][+ Autostart ROM: a real IIe ROM's cold start
calls into the `$C100-$CFFF` internal firmware, which this emulator
deliberately does not model.

---

## 13. The board SHIPS (2026-07-31)

> **§14 supersedes this section's display arrangement (2026-08-01).** The board
> ships MIXED, with four rows of 80-column text under it; everything below
> about the delivery, the chain load and the memory map still holds, except
> that the book is now two pieces and based at aux `$0800`.

`asm/8fish.dsk` boots to the hand-drawn double-hi-res board. Not to a harness
variant of it: `internal/ui`'s `TestDiskBoardParity` starts a IIe at `$C600`,
lets the Disk II boot ROM read a real nibblised disk, and asserts that all
**16,384 bytes** of DHGR page 1 in the resulting machine are byte-identical to
`internal/tiles`' independent Go model. ESC swaps to the 40-column text screen,
which is unchanged and still carries every gate it did before.

### 13.1 The loader's ceiling, corrected — and then made irrelevant

§3.1.1 said Standard Delivery's page table was `$084E-$08FF`, 177 sectors,
45,312 B. **It is `$084F-$08FF`, 176 sectors, 45,056 B.** The `LDA $084E` at
`$0805` is PRE-incremented by the `INC $0806` at `$0802`, so the first entry
the loader ever reads is `$084F`. One byte, and it turns out to explain a
number nobody had explained: `diskii mksd`'s "44 KB cap" is not a tool policy,
it is exactly 176 × 256. The two agree because they are the same fact.

The verdict is unchanged and slightly worse. 8fish is 183 sectors of real
payload; the single-shot layout is over by **seven** sectors, not one.

But the same disassembly carries two things that make the ceiling stop
mattering, and only one of them was in the plan:

- **The loader survives.** Our image starts at `$0D00`, so `$0800-$08FF` is
  never overwritten and the loader's sequential read state is intact when it
  jumps to the copier — `$26` = 0, `$41` = track, `$2B` = slot<<4, Y = last
  sector + 1 (the ROM's `JMP $0801` lands on a `TAY`, so Y is the loop's
  input), `$0800` = `$01`. The copier writes a fresh page table into page
  `$08`, resets `$0806` to `$4E`, repoints the terminator's `JMP` at
  `$084D/$084E`, restores X and Y and jumps to `$0802`. The read continues from
  the next sector on the disk. No sector reader, no nibble code, nothing that
  could ever write to a disk.

- **The table is a LIST OF PAGES, not a base address**, so a stage can
  scatter-load DISJOINT spans at no cost. `diskii mksd` writes a consecutive
  table only because it is handed one contiguous image; `internal/delivery`
  overwrites it. Stage 1's `$2000-$3FFF` hole used to be 32 wasted sectors and
  is now zero. This was not in the plan and it is worth more than the chain
  load on its own: **every future "it doesn't fit" is now a layout question,
  not a delivery question.**

### 13.2 The shipping shape

```
STAGE 1   146 / 176 sectors      (the boot sector's own table, patched)
  $0D00   m8sdboot.bin      171 B   copier + chain loader        1 sector
  $0E00   m8.bin          5,064 B   UI payload -> $E000         20 sectors
  $4000   engine.bin     31,941 B   the engine                 125 sectors

STAGE 2    37 / 176 sectors      (the copier's table, loader re-entered at $0802)
  $0E00   tileblob.bin    1,824 B   artwork -> LC bank 1 $D300   8 sectors
  $2000   bookblob.bin    7,407 B   book -> AUX $0200           29 sectors

disk      184 / 560 sectors, 376 free
boot      7.01 s   ($C600 to the first keyboard poll, measured)
```

**Stage 2 lands in MAIN and is copied on. That is the boot ROM's doing, not
caution.** Its denibblise pass READS THE BUFFER BACK (`lda ($26),y` at
`$C6D9`). A Language Card destination reads back as ROM — ROM has to be banked
in, because the loader's track step ends in `jmp $FCA8` — and an aux
destination reads back as main with RAMRD off. Either way the second pass
denibblises garbage over the first. Reading straight into LC or aux is not a
tuning opportunity; it is broken, and it would have been broken *intermittently*
(only when a track step fell inside the stage). Staging through main costs one
8-page and one 29-page copy, about 90 ms of a 7-second boot.

Two margins from §12 are simply gone. "SD spare" and "UI growth room" were
properties of one contiguous span. The payload now stages into `$0E00-$21FF`,
which OVERLAPS stage 2's landing zone for the book — safely, because the copier
lifts the payload to `$E000` before it re-enters the loader. Its real cap is its
own `$E000-$F6FF` Language Card budget, which the link config enforces as a link
error. 1,064 B of that budget are still free.

### 13.3 Where everything lives now

```
MAIN  $2000-$3FFF   DOUBLE HI-RES PAGE 1, main half   (was the resident book)
AUX   $0200-$1EEE   the resident opening book, 7,407 B of 7,680 (273 B spare)
AUX   $2000-$3FFF   double hi-res page 1, aux half
AUX   $4000-$BFFF   the transposition table, all 4096 entries
LC 1  $D000-$D040   LCCODE: the engine's aux primitives (now 3 of them)
LC 1  $D300-$DA1F   the artwork, 1,824 B
LC 1  $DA20-$DC7F   scanline bases + the two synthesised blank tiles (456 B)
LC 2  $D000-$DFFF   FREE, 4,096 B, still untouched
```

**The book moved because main `$2000-$3FFF` is the DHGR main half**, and that
is the only reason. `asm/book.s` now reads the blob through two primitives in
the same `$D000` page `ttfetch` already lives in: it is fetched from main
`$4000`, so it cannot simply turn RAMRD on — RAMRD switches instruction fetches
too, and aux `$4000-$BFFF` is the transposition table. `bkfetch` copies one
9-byte entry into main `$03D6`, and every comparison, carry and fallback below
it is byte-identical to what it was at `$2000`. `TestBookProbeParityASMvsGo`
holds unchanged, which is the point: the probe's Go parity is a proof about the
SELECTION, and the selection did not move.

`asm/m8.s`'s `uibookname` *can* just turn RAMRD on, because it runs from the
Language Card, which RAMRD does not switch. The asymmetry between those two
routines is the whole discipline in one page.

### 13.4 ★ MIXED MODE DOES NOT FIT, and here is the arithmetic — SUPERSEDED 2026-08-01

**This section is kept as the record of how the price was worked out, and its
last paragraph is what got built.** Mixed mode ships; see §14. Everything below
about the aux arithmetic is still true — the book at 7,407 contiguous bytes and
the text window could not both have aux `$0200-$1FFF`, and no re-slicing of the
board was ever the problem.


The board ships FULL SCREEN with ESC swapping to the 40-column text screen —
Sargon III's arrangement — and not as §3.1's mixed mode with a four-line text
window. That is a forced move, and the force is in aux RAM, not on the screen:

- Double hi-res requires 80COL on. With 80COL on, the mixed-mode text window is
  **80-column** text, whose even columns come from **AUX** `$0400-$07FF`.
- Rows 20-23 are aux `$0650-$0677`, `$06D0-$06F7`, `$0750-$0777`, `$07D0-$07F7`:
  **160 bytes, in four pieces, in the MIDDLE** of the 7,680-byte aux hole.
- The book is 7,407 B. The two largest chunks either side of the window are
  1,104 B (`$0200-$064F`) and 6,152 B (`$07F8-$1FFF`) = **7,256 B, 151 B short.**
  Adding one 88-byte screen hole gets 7,344; it takes FOUR chunks to reach
  7,407, and a four-piece book is not a book.

**The way through, worked out but not built.** Move the book's 1,702-byte NAME
TABLE out of the blob and into the Language Card. The probe never touches it —
`bookprobe` reads offsets 0..5,704 and stops; only `uibookname` walks the names,
and it already runs from LC. That leaves 5,705 B of header + entries, which fits
aux `$0800-$1FFF` (6,144 B) with 439 B to spare and leaves the whole of aux
`$0200-$07FF` free for the text window. It is a **blob-format** change
(`cmd/genbook`, `internal/book`, the probe parity gate), not a renderer change,
and it also wants an 80-column text writer the project does not have yet.

Cost of the current arrangement: while the board is up, the player types blind.
Mitigation, and it is cheap: the 40-column screen is repainted EVERY time
regardless (23,826 cycles against the board's 192,667 — both re-measured on
the shipping build), so ESC is instant and the screen behind it is always
already correct.

### 13.5 ★ What a real IIe can still surprise us with — a MUCH shorter list

§3.1.1 said "goapple2 models neither 80STORE nor AN3, so no test in this repo
can catch getting those switches wrong." **That is no longer true, and it is
the single most important thing in this section.** The sibling checkout's
`iie` package now models:

- **80STORE**, including the precedence that makes it dangerous — 80STORE
  OVERRIDES RAMRD/RAMWRT for `$0400-$07FF` and, with HIRES, for `$2000-$3FFF` —
  validated against a2audit over all sixteen switch combinations.
- **AN3** (`$C05E`/`$C05F`) and `DHires()` = 80COL on && AN3 low.

So `sta CLR80STORE` is now ASSERTED rather than commented: `TestDiskBoots`
requires `Store80 == false` on the booted disk, and `TestDiskEscapeSwapsScreens`
requires `DHires()` true on the board and 80COL off on the text screen. The
project's own lesson — "the ALTCHARSET omission was found only when someone
modelled the switch" — has now paid out a second time, in the other direction:
someone modelled the switches, and the code was already right.

What is STILL unverifiable here, and where a first real-hardware boot should
look if the board comes up wrong:

**The "video output itself" row is GONE, retired on 2026-08-01 — see §15.**
goapple2's `videoscan` now models double hi-res, 80-column text and MIXED at
dot resolution, and `internal/ui/videoscan_test.go` renders the shipping
disk's screen through it. What remains unverifiable is listed below, and it
is now about the SHAPE of the emulation rather than its absence.

| unverifiable | why | symptom on hardware |
|---|---|---|
| **AN3 as a soft switch vs. IOUDIS** | `$C07E/$C07F` (IOUDIS) and the AN3 status read are not modelled | on a IIc or an enhanced IIe with IOUDIS off, `$C05E` may be an annunciator rather than DHIRES: single-resolution hi-res showing only the main half — a board with every other byte-column missing |
| **The unenhanced IIe's DHGR** | not modelled, and not a switch we set | the original IIe needs the "double hi-res" jumper/revision; on a Rev A board there is no DHGR at all |
| **Ctrl-Reset and the language card** | as §12 already records | unchanged by this work |
| **Drive timing** | the emulated Disk II has no seek-time or rotational-latency model beyond the nibble stream | boot time is 7.01 s of EMULATED time; a real drive's head settling is not in that number |

The chain load itself is NOT on this list, and deliberately so. It is the same
boot ROM, reading the same nibbles, through the same `$C65C` entry the first
stage used; everything it depends on ($26, $2B, $41, Y, `$0800`) is asserted by
`TestDiskBoots` in the delivered machine, and `TestStage2PageTable` cross-checks
the table ca65 built from the blob sizes against the one Go wrote on the disk.

### 13.6 Two bugs the gates caught, both worth keeping

**The aux-capability probe was writing the book.** `m8machine` proves the aux
switches are real by writing `$A5` to main `$0300` and `$5A` to aux `$0300`, and
it justified the collateral damage with "aux `$0300` is inside the transposition
table, which is rewritten anyway". That was true until the table moved to
`$4000`; aux `$0300` is now inside the RESIDENT OPENING BOOK. It probes `$3F00`
now — DHGR page 1 in both banks, above the book's staging zone in main, scratch
*by construction* rather than by argument. Same shape as the D8 lesson: a
precaution that survives the reason for it becomes a bug with an alibi.

**The book's move to aux crashed an Apple ][+.** The copy started life in the
copier. On a ][+, `$C005` is not a soft switch, so 29 pages landed in MAIN
`$0200-$1F3F` — straight over the BLOAD copier running at `$0800`. The copier
overwrote itself mid-loop, and the machine check that exists to print NEEDS A
128K APPLE IIE never ran. It is `m8bookaux` now, called from `m8main` AFTER
`m8machine` has PROVED the switches are real. The rule this generalises to:
**nothing may write through RAMWRT in bulk before the machine check, because on
exactly the machines the check exists to reject, a RAMWRT-on write is an
ordinary write to main.**

## 14. MIXED MODE SHIPS (2026-08-01)

`asm/8fish.dsk` boots to the board **with four lines of 80-column text under
it**. §13.4 priced this and did not build it; this section is what got built,
what it cost, and the two gates that now hold it.

### 14.1 The screen

```
scanlines 0-3      border
scanlines 4-155    the 8x8 board, 8 x DHROWS(19), unchanged artwork
scanlines 156-159  border
scanlines 160-191  FOUR ROWS OF 80-COLUMN TEXT  (text rows 20-23)
```

The board did not move and was not re-sliced. `DHTOP` is still 4 and `DHROWS`
is still 19, so 152 scanlines start at 4 and end at 155; MIXED shows graphics
on 0-159. `asm/dhgr.s`'s geometry comment already said so in 2026-07-31's
commit; the only change to that file is the comment now describing the
shipping arrangement instead of a spare property. No tile was re-cut, no blit
changed, and `TestDiskBoardParity` still asserts all 16,384 bytes.

What the four rows carry, and why. Four rows and 320 characters have to hold
what a player needs *without leaving the board*; everything else has to be one
ESC away.

```
     0                   20                  40                  60         79
 20 | 8FISH 1.0    LEVEL 4     YOU ARE WHITE | WHITE TO MOVE     |CHECK       |
 21 |D 5  +0.34 e2e4                         | BOOK: C78 Ruy Lopez, Morphy    |
 22 |ILLEGAL MOVE - TRY AGAIN                | N-NEW T-TAKEBACK R-RESIGN D-DRAW|
 23 |YOUR MOVE? e2e4_                        | L-LEVEL S-SIDES Q-QUIT ?-HELP   |
```

Column 40 is a **gutter on every row**, and on row 20 it is load-bearing; §16.1
is why the two right-hand fields moved into it.

- **Row 20** is the inverse title bar (version, level, which colour you have)
  beside **whose move it is** — or how the game ended — and CHECK.
- **Row 21** is the **think line**: depth, score and the engine's current best
  move, updated between completed iterative-deepening iterations, beside the
  **opening name**. Those are the two things you cannot get from looking at the
  board.
- **Rows 22-23** are the **message row** and the **input prompt with its
  cursor**, each beside one of the two help lines.

What was left out: the **move list**, the rank/file coordinates, and the
long-form help. All three are on the 40-column screen, and ESC still swaps to
it. The choice is "what does a player need while looking at the position",
and a move list is what you consult *deliberately*.

**Every field is COPIED from the 40-column row that already renders it**
(`ui80get` reads text rows 0, 12, 13, 14, 16 and 17). There is exactly one
piece of code per string, the two screens cannot disagree, and every existing
`internal/ui` gate on the 40-column screen is transitively a gate on the
window.

### 14.2 What it cost: the book is TWO PIECES now

The blocker was never pixels. Double hi-res forces 80COL on, and with 80COL on
the text window is **80-column** text, whose EVEN columns are fetched from
**aux `$0400-$07FF`**. Rows 20-23 are `$0650`, `$06D0`, `$0750`, `$07D0` — four
40-byte spans in the middle of the aux hole the 7,407-byte book filled from
`$0200`.

The book was split along the line its own code already draws:

| piece | size | home | read by |
|---|---|---|---|
| header + entries | **5,705 B** | AUX `$0800-$1E48` | `bookprobe` (`asm/book.s`), ~100 entries per probe |
| name table | **1,702 B** | **LC BANK 2** `$D000-$D6A5` | `uibookname` (`asm/m8.s`), once per book move |

`bookprobe` reads offsets 0..5,704 and stops — it has never touched a name.
Only `uibookname` walks the table, and `uibookname` runs at **`$E000`, which
Language Card bank switching does not re-map**, so it can select bank 2, read,
and select bank 1 again with no risk to its own instruction fetch, its zero
page, its stack or its output buffer.

Bank 2 and not bank 1: bank 1 holds `LCCODE` (`$D000`), `DHTILES` (`$D300`),
the two 152-byte scanline tables and the synthesised blank tiles, leaving
**1,048 B contiguous** for a 1,702-byte table. Bank 2 was 4,096 B and entirely
unused. `TestLanguageCardBank1Layout` now FAILS if bank 1 ever frees up enough
to hold the table, because at that point the bank switch is complexity with no
reason left.

The header's `+4` field changed meaning with the split: it was a base-relative
offset to the name table, and it is now the table's **absolute resident
address**. `uibookname` reads it rather than assembling it in, so moving the
table is a `cmd/genbook` change and nothing else.

### 14.3 The memory map, after

```
MAIN  $0400-$07FF   80-column text page, MAIN half = the ODD columns
MAIN  $2000-$3FFF   DOUBLE HI-RES PAGE 1, main half
AUX   $0400-$07FF   80-column text page, AUX half = the EVEN columns  (NEW)
AUX   $0800-$1E48   the resident opening book's ENTRIES, 5,705 B of 6,144
AUX   $2000-$3FFF   double hi-res page 1, aux half
AUX   $4000-$BFFF   the transposition table, all 4096 entries
LC 1  $D000-$D063   LCCODE: the engine's aux primitives
LC 1  $D300-$DA1F   the artwork, 1,824 B
LC 1  $DA20-$DBE7   scanline bases + the two synthesised blank tiles (456 B)
LC 1  $DBE8-$DFFF   free, 1,048 B
LC 2  $D000-$D6A5   THE OPENING BOOK'S NAME TABLE, 1,702 B            (NEW)
LC 2  $D6A6-$DFFF   free, 2,394 B
LC*   $FF00-$FF4F   UI80BUF: the window's 80-column staging line      (NEW)
```

Aux `$0200-$03FF` is now free (512 B) and aux `$0800` has 439 B of book
headroom below the DHGR aux half. The disk is 186 of 560 sectors; stage 2
grew from 37 to 38 sectors (the name table is 7 of them, and it abuts the
artwork, so the two are one span).

### 14.4 The one thing mixed mode cost, and why it is not hidden

**The two screens share main `$0400-$07FF`, and text rows 20-23 are where they
collide.** The window's ODD columns *are* the 40-column screen's help and
prompt rows. There is no way around that — it is the same 160 bytes of RAM —
so the design faces it:

- Every window row is composed in **UI80BUF**, an 80-byte staging line in LC
  RAM at `$FF00`, and then de-interleaved into the two banks by `ui80row`.
  Nothing ever reads a screen it is about to overwrite. `uiprompt` in
  particular now builds its line in UI80BUF and writes it *outwards* to both
  screens, which is why a backspace still visibly erases in both.
- `uiswap` **repaints the 40-column screen** on the way back from the board.
  §13.4's "ESC is instant because the screen behind it is already correct" is
  now "ESC costs one more 23,000-cycle repaint", which is 23 ms.
- `TestDiskBoots` compares the disk's screen against the gated shipping image
  for **rows 0-19 only**, and asserts the window separately out of both banks
  (`ui.Machine.Window()` / `DiskMachine.Window()`). Weakening a comparison is
  exactly the move that hides a regression, so the exclusion is stated with
  its reason and the excluded rows are asserted somewhere else, not dropped.

### 14.5 The gates

Two are new, and both were mutation-checked — shown failing with the thing
they guard broken:

- **`internal/delivery.TestBookClearsTheAuxTextPage`.** Reads `BOOK_BASE` out
  of the generated `asm/book.inc` and the blob length off disk, and fails if
  `[BOOK_BASE, BOOK_BASE+size)` intersects aux `$0400-$07FF`. Mutation: set
  `book.BaseAddr` back to `$0200`, regenerate → *"THE RESIDENT BOOK OVERLAPS
  THE 80-COLUMN TEXT PAGE'S AUX HALF … overlap: $0400-$07FF, 1024 bytes"*.
  The whole reason for this change now lives somewhere that fails.
- **`internal/ui.TestBookNameRestoresBank1`.** Calls `uibookname` directly
  through a `jsr` stub in the booted machine, for three name IDs, and asserts
  BOTH that the Language Card comes back on bank 1 with read+write RAM AND
  that the name it wrote is the right one. Two-sided on purpose: a one-sided
  "bank 1 is back" would pass a routine that never switched at all and read
  the artwork instead. Mutations: delete the restoring `lda $C08B` pair →
  *"returned with LANGUAGE CARD BANK 2 still selected"*; swap the `$C083`
  select for `$C08B` → *"wrote "BOOK: \x03@ \a1p…", want "BOOK: C78 Ruy Lopez,
  Morphy""*.

Unchanged and still green: `TestMicroAB` against `microABGolden` (the search
tree is byte-identical — this was packaging, not engine),
`TestBookProbeParityASMvsGo` (264 positions / 4,194 probes, including the
whole-blob integrity check after every probe), `TestDiskBoardParity` (all
16,384 bytes of DHGR page 1 from a cold `$C600` boot), and the
`internal/delivery` layout suite.

Boot time: **7.10 s** of emulated IIe time from `$C600` to the first keyboard
poll, against 7.01 s before. The 0.09 s is the name table's seven extra
sectors and its copy.

### 14.6 What a real IIe can still surprise us with — the mixed-mode additions — SUPERSEDED 2026-08-01

**Both rows this section added were retired the day after it was written.**
They are kept here because the reasoning is the record of what was missing;
§15 is what replaced them.

| ~~unverifiable~~ RETIRED | why it was on the list | what covers it now |
|---|---|---|
| ~~**Where the graphics/text split actually falls**~~ | goapple2 had no video scanner for this: `Mixed` was a modelled bit, and no test here had rendered scanline 160 | `TestDiskScannerMixedSplit` finds the boundary in the PIXELS of the booted disk and requires 160 |
| ~~**80-column text pixels**~~ | the byte layout was checked against the model, but nothing had driven the 14M shift register in 80-column TEXT mode | `TestDiskScannerWindowIsReadable` reads all four rows back out of the dots; `TestDiskScannerWindowVideoSense` checks all 320 cells and the inverse title bar |

### 14.7 ★ HARDWARE-VERIFIED: DHGR is displayed 7 dots to the LEFT

Reported by zellyn on 2026-08-01, and this is a different KIND of fact from
everything else in §13.5/§14.6 — it was **observed on a real Apple IIe**, not
modelled. While implementing IIe graphics for OpenEmulator he found that the
simplest correct implementation shifted the screen **left by 7 dots** (7
560-resolution "small pixels" = one 7-bit byte, half a 14-dot cell), and was
then surprised to see **the same shift on his physical IIe**.

**What it costs 8fish: about a millimetre, and we are not compensating.**
The board spans x=112 to x=447 of 560 (`DHCOL0`=8, 8 squares x 42 dots), so
it is centred with 112 dots of margin either side. Shifted left by 7 that
becomes 105/119 — a 14-dot asymmetry, 2.5% of screen width. Compensating
would be expensive out of proportion to the gain: 7 dots is HALF a 14-dot
cell, so it changes which BANK each byte belongs to. That is a re-slice of
the tile blob, not an offset.

**What it does NOT affect: any BYTE gate in this repo.** The shift is in the
display pipeline, not in memory. `internal/tiles` decodes aux-then-main per
column pair and the parity tests compare BYTES, so a whole-screen offset is
invisible to them by construction. (An interleave ERROR would be caught —
see the paragraph below — but that is a different failure.) It *is* a gate
now, though: `TestDiskScannerSevenDotShift` measures it. See §15.

**Why it is recorded here anyway.** It is the first hardware-verified datum
this project has about the video pipeline, and it is evidence that
OpenEmulator's model is a usable reference for the rows above: if goapple2's
`videoscan` package were taught DHGR and MIXED from that model, "where the
graphics/text split falls" and "80-column text pixels" would stop being
prose caveats and become gates. That is the same move that retired the
80STORE caveat — the oracle already existed (a2audit), only the emulator
side was missing. **That is what §15 did, the same day.**

The aux/main interleave itself (aux byte = even column) is NOT on this list:
`Window()` de-interleaves it the same way the scanner does, and
`TestDiskBoots` asserts real strings — "8FISH 1.0", "WHITE TO MOVE", "YOUR
MOVE?" — read back out of both banks. Getting the interleave backwards
produces every other character, which those assertions catch.

## 15. THE SCREEN IS RENDERED NOW (2026-08-01)

§13.5 said "goapple2 renders nothing; it is a memory + switch model", and
§14.6 added two more rows to the unverifiable table for the same reason.
§14.7 then noticed the way out: OpenEmulator's IIe video model is a usable
reference, and teaching `videoscan` from it would turn those caveats into
gates — "the same move that retired the 80STORE caveat: the oracle already
existed, only the emulator side was missing".

That is what this section is. `videoscan` scans an Apple IIe now, and this
repo renders the shipping disk's screen through it.

### 15.1 What goapple2 gained

`videoscan` had 40-column text, lo-res and hi-res out of one memory bank.
It now has, ported from OpenEmulator's `AppleIIEVideo` — read for its
behaviour and rewritten, with no code copied:

- **Double hi-res** and **80-column text**: a cell is TWO bytes, aux then
  main, seven dots each, no doubling and no half-dot delay. `AuxRamReader`
  is the aux bank as the SCANNER sees it — the scanner has its own address
  bus and bank select, so RAMRD/RAMWRT/80STORE/ALTZP are irrelevant to it.
  `iie.Memory` gained `RamRead`/`AuxRamRead` and plugs straight in.
- **MIXED**, as a scanner rather than a bit. The split was already implicit
  in `videoscan`'s vertical counter (`lastFour` is `(v>>5)&5 == 5`, i.e.
  scanlines 160-191); nothing had ever driven it.
- **`PlotData.DotX`**: where a cell's leftmost dot lands on the 560-dot
  line. This is where the seven-dot shift lives — see §15.3.
- **`Frame` / `RenderFrame`**: a 567x192 dot bitmap and a helper that runs
  one complete field (65 x 262 cycles) through the real scanner. Same
  address arithmetic, same waveform, same split an interactive renderer
  gets; not a second model.
- **80STORE** now takes the displayed page away from PAGE2, in both the
  per-cycle scanner and the closed-form floating-bus address.

Two bugs fell out of the port: `videoscan`'s per-cell "has this changed?"
cache was a PACKAGE-LEVEL variable that two Scanners silently shared, and
the closed-form floating-bus address ignored 80STORE. Both fixed there.

`go test ./videoscan` had also been failing to BUILD since before this
work — three `%s` verbs applied to a `byte`/`rune` in `convert.go`, which
`go vet`'s printf check runs as part of `go test`. No test in that package
could run. Fixed, in its own commit. (goapple2's `a2` package still does
not build, for unrelated `gonuts/flag` reasons. Untouched.)

**Not modelled, and it says so:** double LO-RES. `RenderFrame` returns
`ErrDoubleLores` rather than drawing something plausible and wrong.

### 15.2 What this repo gained: `internal/ui/videoscan_test.go`

Five gates, all against the SHIPPING disk booted from `$C600`:

| test | what it asserts |
|---|---|
| `TestDiskScannerMixedSplit` | finds the graphics/text boundary in the PIXELS — the last scanline whose dots match `internal/tiles`' decode of DHGR page 1 — and requires 160 |
| `TestDiskScannerSevenDotShift` | slides the decoded board across the rendered dots over ±14 offsets; exactly one fits, and it is **-7** |
| `TestDiskScannerBoardExtent` | scanlines 0-3 and 156-159 blank, 4-155 not, leftmost lit dot at x=105 |
| `TestDiskScannerWindowIsReadable` | reads all four 80-column rows back OUT OF THE DOTS and compares with `Window().Text` |
| `TestDiskScannerWindowVideoSense` | all 320 window cells match their glyphs at the shifted 80-column pitch; row 20 is ≥40 inverse cells, row 23 has exactly one — the cursor |

The measurement, from the passing run:

```
the board occupies scanlines 4-155 and x=105..439
row 23, read out of the rendered dots: "YOUR MOVE?    ...    L-LEVEL S-SIDES Q-QUIT ?-HELP"
inverse cells per row: 20=40 21=0 22=0 23=1   (row 23's is the CURSOR, column 11)
```

Two things showed up that no byte gate could have. The **cursor** is a real
inverse block on the raster, 56 of 56 dots lit, sitting one column past the
prompt — that is now asserted, because it is how a player finds where they
are typing. And the title bar is inverse for exactly its first **40**
columns, not all 80: the right half of row 20 is normal video.

### 15.3 ★ What the seven-dot shift IS, mechanically

§14.7 recorded the shift as an observation. This is the mechanism, and it is
the interesting part: **a correct implementation shifts left because the AUX
byte is clocked out half a cell EARLY.**

In a 40-column mode, the byte fetched for memory cycle *c* fills the whole
14-dot cell at x = 14*c*. In a double-resolution mode the same cycle fetches
TWO bytes and shifts out fourteen dots at 14M — but the aux byte goes first,
and it is not delayed to the cell boundary. So cell *c* runs

```
   x = 14c-7 .. 14c-1     the AUX byte's seven dots
   x = 14c   .. 14c+6     the MAIN byte's seven dots
```

and the whole picture sits seven dots left of where the same bytes would
appear in 40 columns. In OpenEmulator this is literally one term: the
80-column line painters start at `x*14 - CELL_WIDTH/2`, the 40-column ones
at `x*14`. In `videoscan` it is `PlotData.DotX`.

**It is a MOVE, not a crop.** The first modelling of this dropped the dots
at negative x and the 80-column readback test immediately spelled the
screen's first character as a space — which is wrong, and the test caught
it. On real hardware those seven dots land in the overscan the monitor
shows either side of the 560-dot active area; nothing is lost, and that is
precisely why the effect reads as "the screen is shifted" rather than "the
screen lost a character". `videoscan.Frame`'s coordinate space therefore
starts at `MinX = -7`.

§14.7's arithmetic is confirmed as a measurement: the board's bytes describe
x=112..447, and its lit dots run x=105..439, so the margins are **105 left
and 120 right** instead of 112 and 112. (§14.7 predicted 105/119. The extra
dot on the right is the artwork, not the hardware: the h-file's very last
dot column is background in all eight squares, so the rightmost LIT dot is
440-1. The left edge is exact, because the drawn border occupies the
board's first columns.) We are still not compensating, for §14.7's reason —
seven dots is half a cell, so compensating changes which BANK each byte
belongs to, and that is a re-slice of the tile blob.

### 15.4 What is STILL not verified, precisely

This is emulation agreeing with emulation. Being exact about what that is
worth:

| still unverified | why |
|---|---|
| **The glyph SHAPES** | `TestDiskScannerWindowVideoSense` compares the rendered dots against `chargen`'s glyphs — the same generator the renderer used. What it gates is the GEOMETRY (interleave, pitch, shift) and the video sense, not the shapes. `chargen`'s shapes come from goapple2's own character-ROM dump, which is a separate check but not a hardware one |
| **NTSC colour** | `videoscan` produces monochrome dots. Every assertion here is about WHERE dots are, never what colour a IIe makes of them. The board's light-square dither is phase-locked in the artwork (`internal/tiles`), and nothing tests what it looks like on a colour monitor |
| **Double LO-RES** | not modelled at all, by either side. 8fish never selects it |
| **The overscan itself** | `Frame` keeps x = -7..559 because that is what the scanner writes. How much of it a given monitor actually shows is a property of the monitor |
| **Everything in §13.5's surviving table** | IOUDIS vs AN3, the unenhanced IIe's DHGR, Ctrl-Reset, drive timing. A video scanner says nothing about any of them |

And the honest framing: `videoscan` is now a second model of IIe video,
cross-checked against OpenEmulator's, which is itself cross-checked against
zellyn's physical IIe for the seven-dot shift. Two models agreeing is worth
a great deal more than one model with a comment beside it. It is still less
than a machine.

### 15.5 The mutation checks

Each mutation was made in `videoscan` and the gates re-run:

- **Drop the `-7` from `DotX`.** goapple2: seven tests fail. Here: all five
  fail — `"the board's memory fits the rendered dots at offsets [0]; want
  exactly one, -7"`, `"the board's leftmost lit dot is at x=112, want 105"`.
- **Move the MIXED split to 176** (`lastFour = v >= 0x1B0`).
  `TestDiskScannerMixedSplit`: *"the DHGR page is displayed on scanlines
  0-175; want 0-159"*. The window tests fail too, because rows 20-21 stop
  being displayed at all.
- **Swap the aux and main halves of a cell.** `TestDiskScannerWindowIsReadable`
  reads row 20 as `"8 IFHS1 0.    ELEV L 4    OY URA EHWTI EHWTI EOTM VO E"`.
  All five fail.

One near-miss worth recording. An earlier, HALF mutation — moving the split
in the data path only, leaving the address path alone — did NOT fail
`TestDiskScannerMixedSplit`, because the scanner then fetched text-page
bytes and drew them as hi-res, which matches no row of the DHGR page. The
window tests caught it. A single gate would have been enough to feel safe
and would not have been; the pair is the coverage.

## 16. WHAT A PLAYER FOUND (2026-08-01)

Everything above this section was built from tests and from reading. Then
zellyn booted `asm/8fish.dsk` on a real Apple IIe and **played it** — the
project's first real usage feedback — and hit three things in one sitting:

> 1. no gap between "YOU ARE WHITE" and "WHITE TO MOVE"
> 2. "L" only shows extra info in text mode
> 3. typing "S" when white immediately makes the computer move and switches to
>    black. Typing "S" again when black does nothing the first time, then
>    switches the second.

All three were reproduced through `internal/ui`'s harness against the shipping
disk before anything was changed, and all three are now gated by
`internal/ui/window_test.go`. What is worth recording is **why the existing
suite could not see any of them**: every window gate used
`strings.Contains`. That predicate cannot see *where* a field landed, cannot
see whether a field was painted on the screen actually being shown, and cannot
see whether a state change said anything at all. Three defects, one blind spot.

### 16.1 The gutter: column 40 (issue 1)

Row 20's title bar is **inverse video for exactly its first 40 columns** — the
video-sense gate of §14.6 measured that — and its last character is an inverse
space. The side-to-move field started at column 40, so `WHITE TO MOVE` began
in the cell immediately after a solid white block, and the row read as one
word. Nothing was overlapping; the two fields were simply flush.

The fix is one column: `WIN80R = 41`, which is where the two help lines
already started, so the window now has a single right-hand column origin on
all four rows, with column 40 blank on every one of them. It costs **zero
bytes** (an immediate operand changed value) and it must be **normal** video —
an inverse space there is another white cell, which is more bar, not a
separator.

Room check, since the field moved right: the longest side-to-move string is
`DRAW: REPETITION` (16), which now ends at column 56 and still clears
CHECK/THINKING at 60; the longest opening line is
`BOOK: B34 Sicilian, Accelerated Dragon` (38), which now ends at column 78.
Nothing is pushed off the end of the 80.

`TestWindowTitleBarHasAGapBeforeTheStatus` asserts the concrete contract
(column 40 is a normal-video space, the field starts at 41) *and* the general
property, derived from the video attributes rather than assumed: the first
non-blank cell after the inverse run must be at least two columns past its
end, and every cell between must be normal video.

### 16.2 The level prompt was painted on an invisible screen (issue 2)

`cmd_level` sets a message, calls `uipaintmsg`, and then **blocks in `entkey`**
waiting for the digit — so it never reaches the main loop's full repaint. And
`uipaintmsg` painted the 40-column message row, **row 17**, which mixed mode
does not display: MIXED shows text rows 20-23 only. The prompt was rendered
correctly, onto a screen the player cannot see. `uiaskpromo`, the promotion
prompt, is the other caller and had the identical bug.

The fix is in `uipaintmsg`, not at the two call sites: it now blits the
window's message row too whenever the board is up (`ui80msg`, factored out of
`uidhtext`). One routine paints a message and it paints it wherever the
message row currently is; a third blocking prompt cannot reacquire the bug.
Cost: 9 bytes, plus one redundant window-row blit per full repaint (a few
thousand cycles against the 23,659 + 193,667 a repaint already spends).

`TestWindowShowsTheLevelPrompt` asserts **equality** between the window's
message field and 40-column row 17, not a substring — that is §14.1's
one-piece-of-code-per-string invariant, and it fails if the window ever grows
its own copy of the text.

### 16.3 The S key: reordered, and it announces itself (issue 3)

Not a logic bug. `cmd_swap` was a deliberate three-way cycle and two-player
mode is the referee mode, where the UI validates and displays but never
searches. Both halves of what zellyn saw were the code working. But there were
**two real defects around it**, and they compounded:

- **The cycle was ordered wrong.** The very first press of an unlabelled key
  was the *destructive* one: WHITE → BLACK hands White to the engine, which
  searches and commits a move before anything can be reconsidered. The cycle is
  now **WHITE → TWO PLAYERS → BLACK → WHITE**: one press gets you referee mode
  with nothing moving, and handing a colour to the engine takes a second,
  deliberate press. Zero bytes — the same cycle walked the other way round.
- **Nothing was announced.** `cmd_swap` ended in `jmp uiclrmsg`, so landing in
  a mode actively *blanked* the one row that could have named it. The only
  evidence of the third state was thirteen characters inside a forty-column
  inverse title bar — which issue 1 had made hard to read. It now names the
  mode it landed in **and what the next press does**, on both screens:

  ```
  YOU PLAY WHITE. S NEXT: TWO PLAYERS
  TWO PLAYERS. S NEXT: YOU PLAY BLACK
  YOU PLAY BLACK. S NEXT: YOU PLAY WHITE
  ```

  Indexed by `uiwhoidx`, the same index the title bar's who-field uses, so the
  two cannot disagree. Cost: 10 bytes of code, 6 of table, 111 of strings.

**Is a three-way cycle on one key the right design?** With the reorder and the
announcement, yes — and the reasoning is worth stating because the alternatives
are not obviously worse:

- *Separate keys* is the obvious alternative and it is what a keyboard with
  spare letters would want. `W`/`B`/`2` reads better than "press S until". But
  the eight command letters are already `N T R D L S Q ?`, the help line that
  advertises them is 29 characters in a 39-column half-row, and three keys
  would replace one `S-SIDES` with something like `W/B/2-SIDES` — more keys to
  document in less space, for a setting that is changed once or twice a game.
- *A prompt* (`SIDES? W/B/2`) is the `L` pattern and is self-documenting, but
  it costs a second keystroke on every use and another blocking `entkey` loop —
  the exact construct that produced issue 2.
- *An explicit mode line* is what the title bar already is. The bar was not
  wrong; it was unreadable (issue 1) and unremarkable (nothing drew the eye to
  a 13-character field changing).

What actually made the cycle unusable was that its first step was destructive
and its transitions were silent. Both are fixed, and a cycle whose every state
names its successor is discoverable from the keyboard alone — you can find all
three modes by pressing one key three times and reading. If the command set
ever gets more room, `W`/`B`/`2` is the better shape and this is the note that
says so.

### 16.4 One thing found on the way

`cmd_resign` and `cmd_draw`'s acceptance are the only two places a result is
**latched** outside `uisync` (which re-derives its own every turn), and
`uistatrow` only fills in `WHITE WINS` / `BLACK WINS` / `GAME DRAWN` when the
message row is **empty** — so that `GAME OVER - N STARTS A NEW ONE` survives a
repaint. Adding a message to `cmd_swap` therefore turned "S then R" into a
resignation that announced the winner nowhere. Both now clear the row as they
latch (5 bytes). The same hole was already reachable through `D` (declined) →
`R`; it is closed for both.

### 16.5 The cost, and the gates

**+144 bytes** of the UI payload: 5,333 → 5,477 B of `UICODE`, leaving **651 B**
free of `$E000-$FFEF` (`TestUIByteBudget`). Stage 1 of the disk grew by one
sector, 147 → 148 of its 176, and boot time went from **7.10 s to 7.13 s** of
emulated IIe time ($C600 to the first keyboard poll) — the 0.03 s is that
sector. Nothing was added to the boot path itself.

Three new gates, all driven through the **shipping disk**, all mutation-checked
by breaking the thing they guard and watching them fail:

| mutation | what failed |
|---|---|
| status field back to column 40 | *"column 40 is $D7 (\"W\", inverse=false); it must be a NORMAL-video space"* |
| `ui80clr` blanks with `$20` (inverse space) | *"column 40 is $20 (\" \", inverse=true) … an INVERSE space is a white block"* |
| `uipaintmsg`'s window half removed | *"L prompts on the 40-column screen but NOT in the window under the board"* |
| `ui80msg` composes from the wrong 40-column row | *"the announcement must be on both screens, from one string"*; the level prompt gate fails too |
| `cmd_swap` back to `jmp uiclrmsg` | *"it must NAME the mode S just entered (\"TWO PLAYERS\")"* — and `TestCommands` |
| `cmd_swap` back to the old cycle order | *"S #1 must be the HARMLESS press … but the game went from 0 plies to 1"* |

`TestMicroAB` against `microABGolden` and `TestBookProbeParityASMvsGo` are
unchanged and green: this was the screen, not the engine.

Three existing tests encoded the old cycle order and were updated to the new
one — `TestCommands`, `TestResignAwardsTheRightSide`, `TestSideSwapBookkeeping`.
The last of those now leaves the game with **Black** to move, because the press
whose harmlessness it asserts is `TWO PLAYERS → BLACK`, and it gained an
assertion that the following press adds *exactly one* ply.

### 16.6 Not ours, and still failing

`internal/ui`'s `TestLongGameIsNotDrawn` fails on `main` and fails here,
identically: it pokes canary bytes into `$FF00-$FF0F` as "free LC RAM just past
`UIHASH3`", and that is `UI80BUF`, the mixed-mode staging line, which
`uiprompt` rewrites on every keystroke *in both display modes*. The canary has
been aimed at occupied memory since mixed mode landed. It is skipped in
`-short`, which is why it was not noticed. Not touched here — it is a
pre-existing gate bug with its own fix (move the canary above `$FF50`), and
changing a failing test inside a change that also touches the code it tests is
how a real regression gets laundered.

## 17. THE BOARD IS REDRAWN AT 42x19 (2026-08-06)

**§3.1's 44x21 source grid and its top-2-row trim are superseded.** The board
was redrawn at the size the engine renders it, so the slicer no longer throws
anything away.

### 17.1 What was wrong

The first artwork, DazzleDraw picture **CHESS1**, was a specimen sheet on a
**44 x 21** grid. The UI has 152 scanlines for eight ranks, so `cmd/gentiles`
cut 42 x 19 tiles out of it: the two rightmost dot columns (harmless — measured
pure background in all 64 squares) and **the top two source rows** (not
harmless). That trim cost **28 dots** of bishop and king finial, board-wide.

The depth had been chosen as a measured knee — top 2 rows cost 28 dots, top 3
cost 80, the bottom row alone cost 115 — and the checker printed the whole
curve. That made it a *defensible* bad option. It was still a bad option, and it
showed up where these things always show up: on screen.

### 17.2 What was done

zellyn redrew all 24 pieces into **CHESS2**, a picture laid out at **42 x 19**,
which is the engine's own square size. `assets/chess2-dazzledraw-save.bin`
replaces `assets/chess-dazzledraw-save.bin`; the old save is in git history.

| constant | CHESS1 | CHESS2 | re-derived or carried? |
|---|---|---|---|
| `SrcSquareW` x `SrcSquareH` | 44 x 21 | **42 x 19** | re-derived from the pixels |
| `SrcTrimTop` | 2 | **0** | re-derived |
| `SrcOriginX`, `SrcOriginY` | 8, 2 | **8, 2** | re-verified against the pixels |
| `TileW` x `TileH` | 42 x 19 | 42 x 19 | unchanged (and now == the source square) |
| `ContentMinDX`..`ContentMaxDX` | 8..34 | 8..34 | re-verified: ink spans exactly 8..34 |
| bounding box | x 0..367, y 0..171 | **x 0..351, y 0..155** | re-derived |
| `BorderBot` | 171 | **155** | re-derived |
| right bar | literal `364` | **`BorderRight` = 348** | re-derived, and now *computed* |
| `BorderW`, `FrameGapX`, `FrameGapY` | — | **4, 4, 1** | new, measured |

The frame the artist draws is 4-dot bars down each side, 1-dot rules top and
bottom, and a gap of 4 dots / 1 row between the frame and the grid. `8 x 42 =
336` dots plus two 4-dot gaps and two 4-dot bars is 352 = the measured width;
`8 x 19 = 152` rows plus two gap rows and two rules is 156 = the measured
height. The grid **exactly fills** the frame, and that is now asserted.

### 17.3 The delivery did not move

The blob is still **1,824 B** (4 B x 19 rows x 24 tiles), and both generated
includes — `asm/tiledefs.inc` and `asm/tiles.inc` — regenerated **byte-identical**.
So §14.3's memory map and the stage-2 page table are unchanged, and so are the
sector counts: **stage 1 148/176, stage 2 38/176, 187 of 560**, verified against
a baseline build of `main`. The two disk images differ in **130 bytes**, exactly
the number of bytes the blob changed by. Boot time **7.13 s**, unchanged.

### 17.4 The gate that could not fire, and what replaced it

`gentiles -check` headlined **CLIPPED INK**: the dots the top trim discarded.
With `SrcTrimTop = 0` that section is structurally empty. Leaving it there would
have been this project's sixth gate that cannot fail (see docs/results.md).

- The headline was **widened, not deleted**. **LOST INK** measures ink outside
  the whole *kept window* — rows `SrcTrimTop..SrcSquareH-1` crossed with the
  dots the four stored byte columns cover (`dx 7..34`). The dot-column half is
  live; the row half cannot fail today, and the report **prints that fact**
  instead of a bare zero. If a future UI ever needs shorter ranks, one constant
  brings the row half back, and the what-if trim curve is still measured so the
  cost can be priced.
- A **new** `[grid]` check asserts the thing that actually went wrong: the
  constants must describe one consistent drawing (grid + gaps span the frame
  exactly; `TileW == SrcSquareW`; `TileH == SrcSquareH - SrcTrimTop`; the stored
  byte columns contain the content window), **and** the pixels must agree (the
  frame-to-grid gap is entirely dark). Nothing had ever said the source square
  and the tile were the same size. Now something does.
- **Exit status 1 is retired** and left unallocated. It meant "slices, but the
  trim clips ink"; there is no trim, and any lost dot is now also outside the
  content window, which is a structural break (2). Reusing 1 would let an old
  script mis-read a new run.
- **`-check` is wired into a gate.** `cmd/gentiles`' `TestCheckOnCommittedArtwork`
  requires exit **0** — it could only tolerate exit 1 before, because the
  committed artwork clipped 28 dots — and `make test` runs it. `make check-tiles`
  is the convenient spelling for a human mid-redraw.

### 17.5 Mutation checks

Every assertion added or changed was shown failing with the thing it guards
broken:

| mutation | what failed |
|---|---|
| `SrcSquareW` 42 → 44 (redraw at the old grid) | *"grid right edge x=359 runs into the right border (starts at 348)"*; `TestBoundingBox` *"board width 352 dots, want 368"* |
| `SrcTrimTop` 0 → 2 (reinstate the trim) | `TestNoTrim`; *"[grid] TileH=19 but the source square is 19 rows with 2 trimmed"*; 130 dots reported lost |
| `FrameGapX` 4 → 3 | *"[grid] left gap is 4 dots (origin x=8, bar ends at 3) but FrameGapX=3"* |
| `BorderW` 4 → 5 | *"[grid] BorderLeft=3 but the side bars are BorderW=5 dots wide"*; *"[border] border column x=4 is broken at y=1"* |
| `BoardMaxX` 351 → 367 (the old bbox) | *"lit bounding box is x 0..351 … want x 0..367"*; *"[grid] the right gap … is lit at (348,1)"* |
| `SrcOriginY` 2 → 3 (grid off by one row) | 28 dots reported lost; *"ink spans dx 0..40, outside the declared window 8..34"* |
| `ContentMaxDX` 34 → 33 | *"[content-window] a8: ink at (dx=34,dy=16) is in the right margin"* |
| `LastCol` 4 → 3 (store one byte column fewer) | `TestNothingIsLost`; `TestCheckOnCommittedArtwork` *"exit = 2, want 0 (clean)"* |
| delete `checkGrid`'s frame-gap pixel sweep | *"a lit dot at (5,77) in the left frame gap produced no [grid] finding"* |
| LOST INK stops counting the **dot-column** half | *"lost total = 0, want 1"* — the live half |
| LOST INK stops counting the **row** half | **nothing fails.** That is the claim, confirmed: the row half is a tautology today, which is why the report labels it rather than presenting it as a passing check |
| committed blob reverted to the pre-redraw one | *"Check's blob differs from the committed one"*, naming all 13 changed tiles |

### 17.6 Gates

`make test` (short) green end to end. Named: `TestDiskBoardParity` (all 16,384
bytes of DHGR page 1 from a cold `$C600` boot), `TestDiskBoots`, the five
`internal/ui` videoscan gates (`TestDiskScannerMixedSplit`,
`…SevenDotShift`, `…BoardExtent`, `…WindowIsReadable`, `…WindowVideoSense`),
`internal/delivery`'s layout suite, `internal/tiles`, `cmd/gentiles`,
`TestMicroAB` vs `microABGolden`, `TestBookProbeParityASMvsGo` (264 positions /
4,194 probes). A decoded PNG of the board **as the shipping disk boots it** is
written by `TestDiskBoardParity` to `$TMPDIR/8fish-disk-board.png`.
