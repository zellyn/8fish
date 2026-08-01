# Driving Sargon III (1983) as a headless opponent

Sargon III (Hayden, 1983; Dan & Kathe Spracklen) is the canonical strong
1 MHz-6502 chess program (~1550-1600 strength) and our benchmark opponent. This
documents how we boot and drive it headlessly inside the goapple2 emulator.

Code: `internal/sargon` (driver library) + `cmd/sargon` (CLI/demo). Disk image:
`assets/sargon-iii.dsk`. Manual: `assets/sargon-iii.pdf`.

## Harness

`internal/sargon` constructs a headless Apple ][+ (48K + language card, Disk II
in slot 6) by driving goapple2's `Apple2` struct directly with a no-op plotter —
no GUI. ROMs are read from the sibling goapple2 checkout
(`/Users/zellyn/gh/goapple2/data/roms`, override with `$GOAPPLE2_ROMS`).

Key methods (`Machine`):
- `NewMachine(dsk)` — mount disk, no stepping yet.
- `BootToPrompt()` — step until the board is set up (piece list shows the start
  position **and** "SARGON III" is on screen), then a ~35M-cycle settle so the
  program finishes loading before the first move (entering a move too early
  corrupts input).
- `EasyMode()` — CTRL-E (see below).
- `Level(n)` — set level 1-9 via the shifted digit.
- `SubmitMove("E2-E4", budget)` — inject a move and return Sargon's reply.
- `ReadPieceList()` / `Screen()` / `TextRow()` — RAM + text-screen introspection.

Emulation runs ~50M CPU cycles/sec wall (≈ 50x realtime); boot-to-prompt is
~120M cycles ≈ 2-3 s wall.

**Headless speedup (~1.84x):** `NewMachine` constructs the Apple2 with
`goapple2.WithLazyVideoScan()` — since we render nothing (null plotter), the
emulator skips the per-cycle video scan and computes the floating bus on demand
only when the CPU reads it. It is **bit-identical** for any program (verified:
same RAM + text screen + step count after boot and a scripted game, lazy vs
per-cycle; `internal/sargon/lazyvideo_ab_test.go`), and roughly halves emulation
cost — matches run ~2x faster.

## Interfaces to Sargon

**Input — keyboard text entry.** Moves are typed as `FROM-TO`, e.g. `E2-E4`,
then Return. Captures may be typed with `-` or `X`. Keystrokes must be **paced**
(step the CPU ~200k cycles between characters) — injecting a whole move at once
races Sargon's keyboard poll and drops characters. `SubmitMove` handles pacing
and retries a dropped/garbled entry.

**Output — two channels:**

1. **Text move-list (authoritative, always reliable).** Text page 1
   (`$400-$7FF`, standard interleaved layout) shows a running move list on rows
   10-23. Columns: move number right-justified in cols 0-7 (two characters, and
   see hazard B — past 99 they are not both digits), WHITE's move from
   col 10 and BLACK's from col 23 — *regardless of who Sargon is*; only the
   SARGON/PLAYER header labels swap. A castle is printed one column right
   (`  O-O `). The widest token is 8 chars (`B7-B8/Q+`), so the scrape windows
   are cols [10,18) and [22,31). The list shows 13 moves and scrolls.
   Sargon's reply is read directly as algebraic (`E7-E5`, `E5XD4`, `0-0`,
   `PXPEP`, `.../Q`, `+` for check). This works regardless of pondering.
2. **Zero-page piece list (RAM, reliable only in Easy Mode).** See below.

### Three move-list hazards (found 2026-07/08; all now handled)

**A. Mid-search REPAINT.** While searching, Sargon transiently BLANKS the bottom
move-list rows and repaints them identically ~167K cycles later. Measured
(`internal/sargon` `TestRepaintWatch`): exactly one such blip, ~19.3M cycles into
the FIRST search after Sargon leaves its (disk-resident) opening library, none in
the following 130M cycles. Detecting "Sargon has moved" by comparing the move-list
column TEXT therefore false-positives on that blank frame; the caller then reads
the bottom-most *surviving* token, which is Sargon's PREVIOUS move — a stale,
illegal-on-our-board reply. That killed 15 of 300 games in the 2026-07
standard-start match (14 of them Sargon-as-White, all on the first out-of-book
move). **Commit detection must key on the displayed move NUMBER**
(`Machine.LastSargonEntry` / `sargonCommitted`), which only advances on a real
commit and is immune to both the repaint and the scrolling.

**B. THE MOVE NUMBER IS NOT DECIMAL PAST 99.** Sargon renders it with a
two-digit routine that adds `'0'` to each column and never carries out of the
tens, so the tens column runs off the end of the digits:

| move | 99 | 100 | 109 | 110 | 119 | 126 | 128 |
|---|---|---|---|---|---|---|---|
| shown | `99` | `:0` | `:9` | `;0` | `;9` | `<6` | `<8` |

Measured directly (`TestMoveNumberRendering`): Sargon's own move counter lives
at `$1388`, and forcing it just under a boundary and playing on shows what the
screen does. `strconv.Atoi(":0")` fails, so from move 100 the row DISAPPEARED
from the parsed list and — since hazard A made the NUMBER the commit and
move-accepted signal — `LastSargonEntry`/`LastOwnEntry` froze at 99: Sargon's
replies were on screen and invisible however long the driver waited, and every
injected move looked unaccepted. Every game that reached move 100 died as a
`quirk-unresolved` harness artifact scored as a draw (10/504 = 2.0% of the
2026-07-29 gauntlet; 1/150 measured directly on 2026-08-01 before the fix).
`parseListNumber` decodes the field the way Sargon renders it.

**C. 127-move capacity.** At move 128 Sargon restarts its move-list numbering at
1 and its screen record no longer describes the game being played. All 3 games
across two 300-game runs that reached move 128 broke there. `sargon.MaxSargonMoves`
= 127 and `sargon-symmatch` clamps `-max-moves` to it. It is *not* the number
field that gives out — 128 renders as `<8` and decodes cleanly (hazard B), so
whatever restarts is Sargon's move RECORD, not its display. `ErrListWrapped`
requires the backwards number to REPEAT on the next poll, because one backwards
reading is also what hazard A's blank frame looks like.

**Cross-check.** `Machine.CrossCheckHistory(hist, firstPly)` compares Sargon's own
displayed list against our referee history ply for ply (overlapping suffix only —
the window scrolls). `sargon-symmatch` runs it after every ply and dumps both
histories plus the screen on any mismatch, plus a periodic `SCREEN-DUMP-BEGIN/END`
capture every 10 full moves.

## Board in RAM: the zero-page piece-square list

Sargon keeps the live position as a **32-entry piece-square list** in zero page:

- `$60-$6F` — 16 **white** pieces
- `$70-$7F` — 16 **black** pieces

Each byte is the piece's square in **0x88-style coordinates**:
`square = rank*16 + file`, with rank 0 = rank 1 and file 0 = file a. Valid
squares are `$00-$77` (both nibbles 0-7; bit 7 always clear). Algebraic:
`file = 'a' + (sq & 0x0F)`, `rank = '1' + (sq >> 4)`. Examples: e2=`$14`,
e4=`$34`, e7=`$64`, a1=`$00`, h8=`$77`.

The index within each 16-entry half is a **fixed piece slot** (type implied by
slot):

| index | 0-7  | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 |
|-------|------|---|---|----|----|----|----|----|----|
| type  | pawn a-h | N | N | B | B | R | R | Q | K |

(Home files of slots 8-15: b, g, c, f, a, h, d, e.) A **captured** piece has its
square byte set to **`$80`** (bit 7 set). A promoted pawn keeps its slot but
changes type — detect promotions from the move text (`/Q`), not the slot.

Verified by diffing RAM around known moves: our `1.e4` set white e-pawn `$14`→
`$34` (e2→e4) at `$64`, and Sargon's `1...e6` set black e-pawn `$64`→`$54`
(e7→e6) at `$74`; `exd5` set the captured black d-pawn's byte to `$80`. There is a matching static
template of this list at `$1120-$113F` (used to reset a new game) and a
square→hi-res-coordinate table at `$1E00`. The board is drawn in hi-res on page
2 (`$4000-$5FFF`).

### Easy Mode is required for reliable RAM reads

At its normal levels Sargon **ponders on the player's time**, continuously
running its search and **overwriting the `$60-$7F` list as scratch**. So the
list is only coherent while Sargon is truly idle. Enabling **Easy Mode
(CTRL-E)** stops pondering; then the list is stable between moves and both our
own move confirmation and Sargon's reply decode cleanly from RAM — cross-checked
against the text move-list, they agree on normal moves, captures, and castling.

Easy Mode also makes per-move timing ponder-free and reproducible, which
**matches our non-pondering engine** and is what we want for fair, repeatable
matches. Per the manual it roughly halves effective strength at a given level,
so pick the level with that in mind. The driver defaults Easy Mode on.

## Fair-match timing: infinite mode + CTRL-T at our cycle budget (PRIMARY)

Sargon's levels are **average** per-move targets and it **banks time** (instant
opening-book moves fund longer thinks later — the same idea as our BankedClock),
and it only **estimates** seconds (the Apple has no real-time clock, so it
counts a software loop). Matching its self-managed timer is therefore fragile.

The robust, reproducible mechanism is to **take the clock away from Sargon**:
put it on the **Infinite** level (SHIFT-9) and force the move ourselves with
**CTRL-T** after exactly the number of 6502 cycles *we* choose — symmetric with
our own cycle-budgeted engine and fully deterministic in the cycle-accurate
emulator. This is `Machine.RequestMove(move, budgetCycles)` (set the level once
with `InfiniteLevel()`).

Cycle timing uses a new cycle-accurate clock added to goapple2
(`Apple2.Cycles()`, one tick per 6502 cycle; **note our `Machine.Steps`/`Run`
count instructions, not cycles**). `MoveResult.ThinkCycles` reports the cost
Sargon spent on each reply. `CyclesPerSecond = 1_020_500` (≈1.0205 MHz), so a
~30 s / ~30M-cycle budget matches our engine's standing budget.

**Verified empirically (the make-or-break unknowns):**
1. **CTRL-T plays a legal best-move-so-far**, not an abort: budgeting 30M cycles,
   Sargon thought 31.65M then CTRL-T → legal capture `D5xC4`, board consistent.
2. **Commit latency after CTRL-T ≈ 1.6M cycles** (~1.6 s); we detect the
   committed move by the text move-list token changing, not a fixed delay.
3. **Opening-book moves are handled**: in infinite mode a book reply appears on
   its own before the budget elapses (e.g. `d7d5` at 1.29M cycles); `RequestMove`
   returns it as-is (ThinkCycles ≈ 0) without needing CTRL-T.

### Alternative mode: real tournament clock (cross-check)

Set Sargon to SHIFT-3 (30 s/move avg = 60 moves / 30 min whole-game budget) and
give our engine a matching 60-moves/30-min banked control, letting each side
manage its own clock (exercises Sargon's own time management). Use once as a
cross-check; the infinite + CTRL-T path stays primary.

### Empirical shift-3 cost (Sargon self-estimates; measure in cycles)

Level 3 ("30 s/move") in the emulator: an opening-book reply cost ~1.3M cycles;
an out-of-book recapture cost ~10.3M cycles (~10 s) — **not** the nominal 30 s,
confirming Sargon banks/averages rather than spending a fixed budget per move.
So shift-3 is a rough quality anchor (an infinite + CTRL-T-at-~30M-cycle move
should be ≈ shift-3 strength), but for a controlled match use the cycle budget.

## Levels and per-move time (manual p.18)

Levels are selected with **SHIFT + digit** (ASCII `! @ # $ % ^ & * (` for 1-9),
changeable on any turn. Times are Sargon's internal per-move budget; the Apple
has no real-time clock, so Sargon estimates seconds by counting toggles of its
"thinking" asterisk — these track wall-clock closely on the original 1 MHz Apple.

This is the precedent 8fish followed for `FT2_SOFTCLK` (docs/results.md
2026-07-27): 8fish also estimates rather than measures, but estimates CYCLES
from a calibrated per-node cost model indexed by taper phase instead of
counting a fixed-cost display event, because 8fish's per-node cost varies ~2.5×
by phase. Note also that Sargon's level table below tops out at 5 s/move at
level 1, which is roughly where 8fish's estimator stops being useful too (its
resolution is one 128-node poll = 0.45-0.57 s).

| Level (SHIFT-n) | Avg response | Time control |
|-----------------|--------------|--------------|
| 1 | **5 s/move** (blitz) | 60 moves / 5 min |
| 2 | 15 s/move | 60 moves / 15 min |
| **3** | **30 s/move** | 60 moves / 30 min |
| 4 | 1 min/move | 60 moves / 1 hr |
| 5 | 2 min/move | 30 moves / 55 min |
| 6 | 3 min/move | 40 moves / 1h50 |
| 7 | 6 min/move | 30 moves / 3 hr |
| 8 | 10 min/move | 40 moves / 6h40 |
| 9 | infinite | no limit |

**For our ~30 s/emulated-move engine, use Level 3.** Avoid Level 1/2 (blitz) and
6-9 (multi-hour / infinite). Note Easy Mode halves this, so Level 3 + Easy Mode
gives a faster, weaker opponent than Level 3 pondering; when we set up the fair
match we can compare Level 3 (Easy) vs Level 4 (Easy) against our engine's
actual per-move compute.

## Other useful Sargon commands (manual)

CTRL-S change sides (Sargon takes your side / plays White), CTRL-N new game,
CTRL-T force move now, CTRL-B take back, CTRL-A analysis/board-edit mode, CTRL-G
save game, CTRL-L load game, ESC toggle board/text view. Rejected moves show
`ILLEGAL MOVE` / `INVALID MOVE. TRY AGAIN.` on the message line (text row 5).

## CLI

```
# Boot and dump the screen (proves it boots):
go run ./cmd/sargon -boot 110000000

# Sample the screen during boot:
go run ./cmd/sargon -boot 120000000 -sample 20000000

# Scripted keys: wait:N ; type:STR ; key:HH (hex) ; dump ; save:FILE
go run ./cmd/sargon -boot 110000000 -script 'type:E2-E4\r; wait:8000000; dump'

# Play mode: feed player moves, print Sargon's replies (screen + RAM), Easy Mode:
go run ./cmd/sargon -play 'E2-E4,D2-D4,B1-C3' -level 3

# Set an arbitrary FEN (validated), then have Sargon play it as White:
go run ./cmd/sargon -fen 'r1bqk1nr/1ppp1ppp/2n5/p1b1p3/2B1P3/2N2N2/PPPP1PPP/R1BQK2R w KQkq -' \
  -sargon-white -budget-cycles 25000000
```

## xboard adapter (`cmd/sargon-xboard`)

`cmd/sargon-xboard` presents headless Sargon as an xboard (CECP) engine so
cutechess-cli can pit it against another engine. It plays the side that moves
**second** (run it as Black; Sargon's keyboard player is White): opponent moves
arrive as `usermove e2e4`, are typed into Sargon, and Sargon's reply is emitted
as `move e7e6` (coordinate notation from the RAM from/to squares).

Key implementation points, learned the hard way:
- **Boot in a goroutine** and **think in a goroutine** — Sargon's ~3 s boot and
  multi-second search must not block the protocol loop, or cutechess declares a
  stalled connection when its liveness `ping` goes unanswered.
- **force/go handshake**: cutechess starts a Black engine with
  `force; usermove <white1>; go`. Sargon replies as soon as it gets the
  usermove, so the adapter computes the reply but **holds it until `go`**.
- `-budget-cycles N` uses the fair mechanism (Infinite + CTRL-T at N cycles);
  otherwise `-level`/`-easy` timed mode.
- **`setboard` (varied openings)**: the adapter advertises `setboard=1` and, on
  `setboard <fen>`, applies the position with the validated CTRL-A editor
  `SetupPosition` (once, after boot, before the first move). cutechess sends it
  for each game under `-openings file=tools/openings-pool.epd format=epd`. Sargon
  plays whichever colour cutechess assigns: `go`-before-`usermove` → Sargon takes
  the side to move (CTRL-S); `usermove` first → the opponent moves and Sargon
  replies. Both verified (Sargon-White `Bxh6`, Sargon-Black `Nbd7` from a pool
  position).
- **`-budget-multiplier M`** scales `-budget-cycles` for Sargon only. Easy Mode
  (needed for reliable headless reads) disables pondering, weakening Sargon vs
  the real machine; giving Sargon `M`x our per-move compute approximates the lost
  pondering. Bracket the truth with `M=1.5` and `M=2.0`.

**Smoke game played** (our engine White, fast budget, vs Sargon level 1 Black):
a complete 78-ply game via cutechess-cli ending in checkmate (`39...Qh4#`,
Sargon 0-1), with castling, captures and checks all handled correctly.

```
cutechess-cli \
  -engine name=us cmd=./us proto=uci \
  -engine name=SargonIII cmd="./sargon-xboard -budget-cycles 30000000" proto=xboard \
  -each tc=inf -games 1 -pgnout smoke.pgn
```

## Sargon as White (CTRL-S) and matched gauntlet

`StartAsWhite(budgetCycles)` sends CTRL-S so Sargon takes White and plays the
opening move (the keyboard opponent becomes Black); the driver flips which
piece-list half and move-list column belong to Sargon (`Machine.SargonWhite`).
Note the move-list header swaps ("SARGON PLAYER") but the columns don't: White's
move is always the LEFT column (cols 10-15), Black's the RIGHT (22-27). The
xboard adapter detects White automatically (a `go` with no prior `usermove`).

For matches the adapter runs **Infinite level + Easy Mode + CTRL-T**: Easy Mode
stops Sargon pondering after it moves, so the piece list stays stable (reliable
reads) and play is reproducible and ponder-free (matching our non-pondering
engine). The adapter emits Sargon's move parsed from the authoritative on-screen
token (RAM decode is a fallback) and **never claims a game result** — cutechess
adjudicates mate/stalemate/draws from the moves; a wrong claim (our game-over
scrape can false-positive on "CHECK") would lose the game as an "invalid result
claim".

## Arbitrary-position setup via the CTRL-A board editor (WORKS, validated)

`Machine.SetupPosition(fen)` sets Sargon to any FEN/EPD position headlessly and
**validates** that it landed, so it can seed unbalanced-opening gauntlets. All
40 `tools/openings-pool.epd` positions plus edge cases round-trip exactly (see
`internal/sargon/setup_roundtrip_test.go`, `setup_behavior_test.go`; run with
`SARGON_SLOW=1`).

### Why poking `$60-$7F` fails but the editor works

Sargon's authoritative game state is derived by replaying its move list from a
base position, so poking the `$60-$7F` piece list is reverted. The **supported**
way to set a base position is Analysis Mode (CTRL-A). Reverse-engineering what it
does (by diffing RAM before/after) shows the clean primitive:

- **While the CTRL-A editor is active, Sargon holds the edited position as a
  plain 0x88-style board array in zero page based at `$80`**: the byte for a
  square is at `$80 + rank*16 + file` (rank 0 = rank 1, file 0 = file a). Each
  byte encodes the piece directly, independent of the slot list: **empty =
  `$80`; a piece = colourBit | typeCode**, colourBit `$40` white / `$50` black,
  typeCode **P=$0 N=$8 B=$A R=$C Q=$E K=$F**.
- **On RETURN (exit), Sargon reconciles that board into its AUTHORITATIVE
  state**: it rebuilds `$60-$7F` *and* the `$1120-$113F` new-game template, and
  assigns castle status. This state **persists** — Sargon plays legal moves from
  it and does not revert (verified: from a set position Sargon played `F4xH6`,
  `C3-A4`, `D8-B6`, all illegal from the standard start).

So `SetupPosition` drives the editor's **data**, not keystroke pantomime: press
CTRL-A, poke the `$80` board, press RETURN. **Two keypresses**, each confirmed by
a **RAM state change** (piece list cleared on enter, repopulated on exit), never
a fixed delay — so nothing can be dropped or misordered. This sidesteps all the
keypress-streaming fragility (dropped keys, cursor off-by-one, colour-toggle
state) that pantomiming the editor's cursor/piece keys would incur.

### Side-to-move: CTRL-C, not CTRL-S

Side-to-move on a set-up position is set with **CTRL-C** ("Change Color with the
Move" — the manual's Feature 19, the supported companion to Analysis Mode). Do
**not** confuse it with **CTRL-S** (switch sides mid-game / "Sargon as White").
The stable indicator is **`$0035`: `$01` = white to move, `$05` = black to
move**. (`$0002` looks like a flag right after CTRL-C but is really editor
scratch — do not use it.) Verified behaviorally: with the *same* board,
white-to-move makes Sargon play a white piece, black-to-move a black piece.

### Validation (built into `SetupPosition`)

`SetupAndValidate` reads the position back on **two independent channels** and
requires both to match the target, plus the side-to-move flag:

1. **`$60-$7F` piece-slot list**, rendered by slot type (`PieceList.Board()`).
2. **Re-entered `$80` editor board**: pressing CTRL-A again makes Sargon rebuild
   its 0x88 board from the authoritative state; we read the actual piece-type
   bytes and decode them, then exit leaving the position unchanged.

Plus a **behavioral** check (`setup_behavior_test.go`): Sargon is asked to move
and must play a *legal* move of the *correct colour* — the strongest,
truly-independent confirmation, since Sargon computes it from its own
authoritative state and would break loudly on a corrupt position.
`SetupPosition` returns an error (not a silent wrong board) unless every channel
agrees. `checkPieceCounts` also rejects positions the fixed slot list cannot
hold (e.g. a promoted 3rd knight / 2nd queen), which the exit reconciliation
would otherwise silently drop.

### Limitations of what Sargon can represent

- **Castling rights are inferred from home squares, not the FEN flags.** Per the
  manual, Sargon assumes a K or R on its original square has never moved and
  grants castling accordingly. So a FEN that *denies* castling while the K/R sit
  home (moved-and-returned) will wrongly get castling, and vice-versa. For the
  opening pool this matches (rights track home-square presence), but the `KQkq`
  field is not honoured explicitly.
- **En passant is not representable.** The editor has no ep-target input and
  Sargon carries no "last move was a double push" state, so a one-time ep
  capture in the FEN's ep field (several pool entries have `a6`/`b6`/`g6`/`h6`)
  is unavailable to the side to move. Minor for an opening *start* position.
- **50-move / repetition history resets** to zero at setup (irrelevant for
  openings).

### Hi-res display cross-check: DEFERRED (specific blocker)

A third, display-side channel (decode the ESC hi-res board and compare) was
scoped but deferred. Findings: the board is on **hi-res page 2** (`$4000`), ESC
redraws it from the position, it is an 8x8 grid ~21-24 scanlines x ~4 bytes per
square. A per-square **exact-bitmap dictionary** decode was prototyped but
**does not** generalise: the same piece renders to *different* bytes on
different files (glyphs are drawn at sub-byte, non-4-byte-aligned X offsets), so
no single geometry gives a collision-free per-(piece,bg) table across positions.
A working decoder needs a **per-(piece, file) bitmap dictionary** (or bit-level
glyph alignment) — a bounded follow-up. It is **not on the game-correctness
path**: the gauntlet reads the text move list + RAM (both validated), never the
hi-res image, so a hypothetical display-vs-state desync could not corrupt games.

## Status / next steps

Done: headless boot; text-screen scraping; paced keyboard move injection; reply
parsing (text + RAM) for normal moves / captures / castling; board RE
(zero-page piece list); level table; **fair-match primitive `RequestMove`
(Infinite + CTRL-T at a chosen cycle budget), verified**; cycle-accurate clock
in goapple2; **xboard adapter with a completed cutechess smoke game**;
**validated arbitrary-position `SetupPosition` via the CTRL-A editor (40/40 pool
positions round-trip; side-to-move verified behaviorally)**; **~1.84x headless
speedup (lazy floating-bus video scan), bit-identical**; **varied-opening
gauntlet wired: adapter `setboard` + `runs/sargon-pool-match.sh`, with a
`-budget-multiplier` pondering-proxy handicap**.

### Rematch harness — READY (not yet run)

`runs/sargon-pool-match.sh [ROUNDS] [BUDGET_CYCLES] [SARGON_MULT] [OUTDIR]`
plays our engine vs Sargon from `tools/openings-pool.epd` (each opening both
colours via `-openings ... -repeat -games 2`), so unbalanced starts produce
decisive games instead of the standard-start ~70%-draw problem. Sargon gets
`SARGON_MULT`x our per-move cycle budget to approximate its (Easy-Mode-disabled)
pondering — run `MULT=1.5` and `MULT=2.0` to bracket. Launch under **tmux** (not
nohup); check `CPU%` idle and leave 2-3 cores headroom before starting; poll the
log for `SARGON-POOL-DONE`. Reclassify `SARGON-DECLARED-DRAW` (grep the debug
log) as draws when tallying. Trigger the actual rematch once the strength ports
(recap2, ordering) are in `asm/engine.bin`.

Follow-ups: run the bracketed rematch; optionally finish the hi-res display
cross-check (per-(piece,file) dictionary); tournament-clock cross-check mode.
