# Resident opening book

A hand-curated opening book, compiled to a compact binary blob laid out
**exactly** as it sits resident in AUXILIARY RAM at `$0200-$1EEF`. (It used
to live in main `$2000-$3FFF`, the hi-res page-1 hole; the double-hi-res
board needed those 8 KB, so as of 2026-07-31 the blob is delivered to main
`$2000` by stage 2 of the disk's chain load and lifted to aux `$0200` before
anything runs. See "Where it lives, and how it is read" below.) The engine now probes it **on-device**: the 6502 driver
binary-searches the resident blob and plays book moves itself (see
"Resident asm probe" below). The original Go-side bridge probe remains as
the reference the asm probe is proven byte-for-byte equal to.

## Files

| File | Role |
|------|------|
| **`internal/book/openings.txt`** | **the source of truth**: 179 lines in human SAN (`# name` + `ECO: moves`) — 48 deep main lines plus a BREADTH section. Edit this. |
| `cmd/genbook/` | the compiler: parses `openings.txt`, validates every line legal move-by-move through refchess, and generates everything below |
| `internal/book/lines.go` | **generated** Go `book.Lines` (UCI) — `DO NOT EDIT` |
| `internal/book/bookblob.bin` | **generated** resident blob (embedded by package book) |
| `asm/book.inc` | **generated** asm layout defs for the resident probe |
| `internal/book/build.go` | validates every line legal move-by-move through refchess (refuses illegal lines); keys each position on the engine hash |
| `internal/book/book.go` | blob format, encode/decode, binary-search probe, weighted pick, `HashFEN` keying |
| `internal/refchess/san.go` | `ParseSAN`/`SAN`: resolve SAN ⇄ moves against the legal-move generator (used by the compiler) |

The pipeline is `openings.txt` → `cmd/genbook` → `lines.go` + `bookblob.bin` + `book.inc`.
A single command does the whole thing:

```
go run ./cmd/genbook        # from the repo root
```

## Editing the book — the text format

`internal/book/openings.txt` is the one file a human edits. It is plain
text a chess player can read at a glance:

```
# ===== 1.e4 e5 — open games =====      # a comment (section divider)

# Ruy Lopez, Morphy                     # the opening's NAME
C78: 1. e4 e5 2. Nf3 Nc6 3. Bb5 a6 4. Ba4 Nf6 5. O-O Be7 6. Re1 b5 7. Bb3 d6

# Sicilian, Najdorf
B90: 1. e4 c5 2. Nf3 d6 3. d4 cxd4 4. Nxd4 Nf6 5. Nc3 a6
```

Rules:

- **Moves are Standard Algebraic Notation (SAN)** — exactly what you read
  in a chess book: `Nf3`, `Bb5`, `exd4`, `Nxd4`, `O-O`, `O-O-O`, `e8=Q`,
  checks `Bxc3+`, mate `Qh4#`. Disambiguation (`Nbd7`, `R1e2`) is written
  only when SAN requires it, and the compiler enforces it (an ambiguous
  move is refused).
- **Move numbers (`1.`, `2.`) are optional and ignored** — keep them for
  readability. `1.e4` (no space) is accepted too.
- **`#` starts a comment.** The `#` line *directly above* a moves line is
  that opening's display name; other `#` lines (section dividers, notes)
  are ignored.
- **A moves line is `<ECO>: <moves>`** — an ECO code (`A00`–`E99`), a
  colon, then the SAN move sequence. **Blank lines are ignored.**
- **The ECO code may carry a perspective suffix, `/w` or `/b`** — see the
  next section. A bare code means "both sides".

### `/w` and `/b` — whose moves the line speaks for

The book is a set of POSITIONS, so a line written out in full teaches the
engine both to **play** those moves and to **answer** them. For a main line
that is exactly right: we are happy on either side of a Ruy Lopez.

For a breadth line it is exactly wrong. `A00: 1. g4 d5 2. Bg2 c6` would give
the engine `1.g4` as a first move it can roll — a Grob, at weight 1, in the
same lottery as `1.e4`. The point of the line is to have an answer ready for
an opponent who plays the Grob, not to become a Grob player.

So the ECO code takes an optional suffix:

| written | compiles into entries |
|---|---|
| `A00:` | every move of the line (**default** — use for main lines) |
| `A00/b:` | only **Black's** moves — "this is how we ANSWER it" |
| `A00/w:` | only **White's** moves |

A marked line is also **weightless, and may not touch a covered position**:

- it never increments the weight of an entry an earlier line established, and
- `Build` **refuses** it outright if it would add a *second* move at a
  position the unmarked main lines already answer.

Both rules exist because the first draft of the breadth section broke them
and it cost **28 Elo over 2,000 games**. Weight is only "how many curated
lines played this move here", so thirty short lines added to *answer* junk
first moves each bumped the weight of their own opening move and quietly
moved the engine's repertoire from 65% `1.e4` to 44%. Breadth lines answer;
they do not advocate. If a breadth move really is one we want to play, drop
the suffix and adopt it as a main line — visibly. `TestBookRepertoire`
prints the distribution so the next such drift is seen, not inferred.

```
# Grob Opening
A00/b: 1. g4 d5 2. Bg2 c6 3. h3 e5      <- we answer 1.g4; we never play it

# Irregular Defence
B00/w: 1. e4 h5 2. d4 d5 3. Nc3 dxe4    <- we punish 1...h5; we never play it
```

Every move is still parsed, validated and made — the line has to be walked
to reach the later positions — but only the marked side's moves become
entries. Our own first-move repertoire is therefore still exactly `e4`,
`d4`, `c4`, `Nf3`, even though the book now answers all twenty legal first
moves.

**Transposition is free breadth.** Because the book is keyed on the position
and not the move order, a short breadth line that reaches a position already
in a deep main line inherits that whole main line. `B07: 1. d4 d6 2. e4 Nf6
3. Nc3 g6 4. Nf3 Bg7` costs four plies and joins the Pirc main line already
present under `1.e4 d6`. **41 of the 179 lines transpose this way** —
`TestBookTranspositions` lists exactly which, and recomputes it from the
source, so it cannot go stale the way a hand-written comment would.

**Names are deduplicated.** Two lines whose `ECO Name` text is identical
share one name ID and one stored string, so a dozen four-ply lines answering
a dozen junk first moves can all honestly be `A00 Irregular Opening` and cost
~22 bytes once. Breadth lines therefore use family names ("Sicilian Defence",
"Queen's Pawn Game") while the deep main lines keep their exact ones ("B90
Sicilian, Najdorf"). Name IDs must still fit in a byte, so there is a hard
ceiling of 256 *distinct* names (the build fails loudly at 257).

**Why SAN and not coordinate (`e2e4`)?** SAN is what a human authors and
reviews naturally, and the compiler resolves it against refchess's legal
moves — so it is *self-validating*: a typo that names no legal move, or an
under-specified ambiguous move, fails the build with a clear error naming
the opening and the bad move. (Coordinate notation is trivial to parse but
unreadable and does not catch a "looks fine but illegal" typo.)

### How to add an opening

1. Open `internal/book/openings.txt`.
2. Add two lines in the appropriate section:

   ```
   # Vienna Game
   C25: 1. e4 e5 2. Nc3 Nc6 3. f4 exf4 4. Nf3 g5
   ```

3. Run `go run ./cmd/genbook`. It re-validates every line, regenerates
   `lines.go` + `bookblob.bin` + `book.inc`, and prints the new sizes. If
   your line has an illegal, ambiguous, or misspelled move, it refuses to
   compile and tells you which opening and which move — e.g.

   ```
   genbook: parse failed: internal/book/openings.txt:123: Vienna Game (C25): ply 6 "Nf7": no legal move in ...
   ```

4. `go test ./internal/book/ ./cmd/genbook/` — the tests confirm the
   source compiles cleanly, every line is legal, and `lines.go` matches
   the source (a stale `lines.go` fails `TestCompiledMatchesGeneratedLines`).
5. Commit `openings.txt` **and** the three generated files together.

## Keying — the correctness hinge

Entries are keyed on the engine's 32-bit Zobrist hash, i.e. the asm
`HASH0-3`. This value is reproduced in Go by `mirror.Pos.Hash`, and the
equivalence is **proven, not assumed**: `TestBookKeyMatchesASM` reads the
actual `ZKEYS/STMKEY/CASTKEYS/EPKEYS` bytes out of `engine.bin`,
recomputes the hash from scratch for every position reachable in the
book, and asserts it equals `HashFEN`. The equivalence holds because
`internal/mirror/tables.go` and `cmd/gentables/main.go` seed the same PRNG
(`rand.NewPCG(0x6502c4e5, 0x0a11babe)`) and consume it in identical order
(12 kinds × 64 squares, then side-to-move, 16 castle, 8 ep), combining via
the identical position decomposition. The same blob is therefore probeable
byte-for-byte by both this Go bridge and a future asm-resident binary
search.

## Blob layout (as it sits at aux `$0200`)

```
+0   magic 'B','K'
+2   entryCount   (uint16 LE)
+4   nameOff      (uint16 LE, base-relative)
+6   nameCount    (byte)
+7   stride       (byte, = 9)
+8   entries[entryCount], 9 bytes each, SORTED ascending by key:
        +0 key    uint32 LE  == HASH0..3
        +4 from   0x88 square
        +5 to     0x88 square
        +6 flags  move flags (bits 0-2 = promotion; matches BESTFLAGS)
        +7 weight weighted-pick weight
        +8 nameID name-table index
nameOff  name table: nameCount × [len:1B, text bytes]
```

`from`/`to`/`flags` are the engine's own move encoding (0x88 + `FL_PROMO`),
so the bytes feed straight into the move machinery — the asm probe never
re-encodes.

**Name table:** raw length-prefixed strings. Measured against a
word-tokenized dictionary form (`genbook` prints both): raw = 1050 B,
tokenized = 885 B — a 165 B (16%) saving that is not meaningful against
4 KB of headroom and costs asm-side decode complexity, so raw is used.
On-device the name text is not needed at all: `nameID` **is** the
current-opening byte; text lookup is host-side only (logging).

## Sizes (current build)

- 179 lines → 633 entries (5697 B) + 80 distinct names (1702 B) + 8 B header
- **blob = 7407 bytes** = 90.4% of the 8 KB hole, **785 bytes free**
- `TestBlobSize` fails if free headroom drops below 256 B. The old "≤ 6 KB
  margin target" is gone: it was set when the book was 48 deep lines using
  47% of the hole, and nothing else wants `$2000-$3FFF`. Breadth is what the
  space is for.
- The name table is 22% of the blob. `genbook` still prints the
  word-tokenised alternative each build (currently 1242 B, a 460 B saving);
  it is not implemented, because it would need a decoder in `uibookname`
  (`asm/m8.s`) and 460 B is not yet worth that. If the book needs to grow
  again, that is the next lever, before cutting lines.

## Bridge integration (`internal/ucibridge`)

Before each search, `think` calls `probeBook`:

1. If `Bridge.Book == nil`, return immediately — behavior is
   byte-identical to the bookless engine (existing tests/fingerprints
   unaffected).
2. Compute the current position's key via `book.HashFEN(pos.FEN())`.
3. Draw one value from a deterministic PRNG seeded by `Bridge.BookSeed`
   (drawn on every move, hit or miss, so the whole game's choices depend
   only on the seed and move number → A/B replays reproduce).
4. Binary-search the sorted entries; on a hit, weighted-pick among the
   equal-key moves, play it **without searching**, set `CurOpening`
   (nameID+1) / `CurOpeningName`, and log `info string in book: <name>`.
5. On a miss, fall through to the normal search exactly as before.

`ucinewgame` resets the PRNG stream and the current-opening state.

## v1 blob delivery vs. real hardware

v1 holds the blob **bridge-side**: the bridge plays book moves before it
ever invokes the emulated engine, so the blob need not be present in
machine memory. The bytes are nonetheless laid out exactly as they will
sit where the resident blob sits, so promoting to an asm-resident probe was
a pure read-side addition — no format change.

**Real-hardware equivalent:** at startup the loader reads the blob from disk
**once** into main `$2000`, the copier lifts it to aux `$0200`, and nothing
writes it again. It is independent of the transposition table, which is also
in the aux bank, above it at `$4000`.
`asm/book.inc` gives the resident base and field offsets the loader and probe
share.

**Where it lives, and how it is read (2026-07-31).** The blob is delivered to
main `$2000` by stage 2 of the disk's chain load and lifted to AUX `$0200` by
`m8bookaux` before anything runs. `bookprobe` is fetched from main `$4000`, so
it cannot turn RAMRD on — that would switch instruction fetches too, and aux
`$4000-$BFFF` is the transposition table. It reads the blob through `bkhdr` and
`bkfetch`, two primitives in the same Language Card page as `ttfetch`, which
copy the 8-byte header and one 9-byte entry into main `$03DF`/`$03D6`. Every
comparison below that is unchanged, which is why `TestBookProbeParityASMvsGo`
needed no new cases: the proof is about the SELECTION, and the selection did
not move.

## Resident asm probe (implemented)

`asm/book.s` (included at the end of engine.s's CODE segment) is the
on-device probe. The 6502 engine now plays book moves **itself** — the Go
bridge only supplies the random value and reads back the engine's choice.

Entry point `bookentry` (a standalone harness/bridge entry, NOT the normal
`$4000` search entry) does:

1. **Key**: `jsr evalinit` recomputes `HASH0-3` for the root exactly as the
   search entry does (byte-identical key).
2. **Binary search** (`bookprobe`): unsigned 4-byte-LE lower-bound over
   `BOOK_ENTRIES` (`BOOK_COUNT` entries, stride 9); midpoint address =
   `BOOK_ENTRIES + mid*9`.
3. On a hit, **scan** the contiguous equal-key run summing `BOOK_E_WEIGHT`,
   compute `pick = BOOKRND mod total` (32-bit r ÷ 16-bit total, MSB-first
   shift/subtract — byte-for-byte the Go `r % total`), and walk the run
   subtracting weights to select.
4. **Play** `BOOK_E_FROM/TO/FLAGS` directly into `BESTFROM/TO/FLAGS`
   (already engine move encoding); copy `BOOK_E_NAMEID` to `CUROPENING`,
   the "which opening am I in" byte. `BOOKHIT`=1. No node search runs.
5. **Miss** (no `BK` magic at the resident base, or key not found) → `BOOKHIT`=0; the
   caller runs the normal search, unchanged.

312 entries → at most 9 key comparisons per probe; trivial against a
search. The book is consulted only until the first out-of-book position.

### Claimed memory (asm/defs.inc)

| Symbol | Addr | Role |
|--------|------|------|
| `CUROPENING` | `$3D` (ZP) | selected move's `NAMEID` — current-opening state |
| `RUNPTR` | `$3E-$3F` (ZP) | probe's equal-key-run / weighted-pick pointer |
| `BOOKRND` | `$0222-$0225` | 32-bit LE random r, poked by the loader/harness; **consumed** by the modulo |
| `BOOKHIT` | `$0226` | 1 = book move played, 0 = miss |

All other probe scratch is borrowed search/eval ZP (`T0-T3`, `MUL*`,
`PSP*`, `CURPTR`), all dead at the root before the first search node; the
probe never writes `HASH0-3`.

### Zero-cost for the bookless engine

`book.s` is appended at the very end of CODE, so it moves nothing before it
and shifts the page-aligned `TABLES`/`LCCODE` only by whole pages (low
bytes unchanged → no page-crossing cycle changes). The normal `$4000`
search entry is not modified and never branches into book code. With no
book loaded (the resident base has no magic — the state for every existing
test) the
search path is byte-identical: `TestMicroAB`/`Improving`/`Adopted` grand
totals are unchanged.

### Blob delivery

On real hardware stage 2 of the chain load reads the blob once from disk into
main `$2000`, `m8bookaux` lifts it to aux `$0200`, and nothing writes it
again; independent of the aux-bank TT at `$4000`. In the harness,
`chesstest.LoadBook(m, blob)` pokes the identical bytes straight into
`m.Mem.Aux`; `chesstest.AsmBookProbe` drives one probe pass. Existing tests
never call `LoadBook`, so the resident base stays clear.

### Correctness gate (asm == Go)

`chesstest.TestBookProbeParityASMvsGo` walks every in-book position (264,
30 multi-move) and, over a full residue period of r plus wide edge values
(4194 probes), asserts the asm probe's `FROM/TO/FLAGS` and `NAMEID` equal
`Book.Probe(key, r)` byte-for-byte; `TestBookProbeWeightedDistribution`
confirms the pick frequencies equal the weights; `TestBookProbeOutOfBook`
confirms misses match. `TestBookFollowThenSearchDriver` and ucibridge's
`TestBridgeBookFollowThenSearch` play a full opening on-device.

### Coexistence note (move stack) — settled 2026-07-28, DISSOLVED 2026-07-31

**The overlap this note is about no longer exists.** `BOOK_BASE` is AUX `$0200`
as of 2026-07-31 (main `$2000-$3FFF` is the double-hi-res MAIN half; see
`docs/ui-design.md` §13), so a move-stack overrun past `MOVESTACKTOP = $2000`
can no longer reach the book at all — it now runs into the BOARD's main half.
The symptom is visible garbage, but PERMANENT rather than repainted: `dhboard`
only rewrites byte columns 8-31 of scanlines 4-155, and main `$2000-$207F` is
scanline 0's top border, written once by `dhclear` at boot. Still an
improvement on a silently wrong opening move. `MOVESTACKTOP` stays `$2000` and
`chesstest.TestMoveStackWatermark` stays exactly as it was; only what is on the
other side of the line changed. The reasoning below is kept because it is the
record of how the overlap was priced.

`MOVESTACKTOP` is `$2000` and the generator's batched emission may write up
to ~124 bytes past it (`$2000-$207F`) before its flush traps. `BOOK_BASE` was
also `$2000`, so those 128 bytes — the header plus the ~13 lowest-keyed
entries — were the book's exposure. `defs.inc` used to claim the same 128
bytes were reserved guard slack that "must stay unallocated"; that claim was
false the day the book landed and has been removed. Three facts settle it:

1. **The exposed window does not grow with the book.** It is fixed at
   `$2000-$207F` by `MOVESTACKTOP`. A 3,866-byte book and an 8,192-byte book
   expose exactly the same 128 bytes, so book size was never the variable.
2. **Slack would not have helped in either environment.** Under the harness
   the store to `EXIT_TRAP` ends the run on that instruction, so the ~124
   bytes already written are the entire blast radius and nothing runs
   afterwards to observe them. On real hardware `$BFFF` is plain RAM (see
   `cmd_quit` in `asm/m8.s`), so the trap is a no-op: the generator returns
   and keeps writing past `$2000` for the rest of that ply and every deeper
   one. The overrun is unbounded there; 128 bytes cannot contain it.
3. **Overflow is far out of reach, and that is now enforced by a test.**
   `chesstest.TestMoveStackWatermark` samples `MSP` (`$10-$11`) once per
   executed instruction across tactical, maximum-mobility (3 queens + 2
   rooks) and depth-10 endgame searches. Measured peaks:

   | position | depth | peak MSP | slots used | % of 1,152 |
   |---|---|---|---|---|
   | kiwipete | 6 | `$159C` | 487 | 42.3% |
   | open middlegame | 7 | `$14FC` | 447 | 38.8% |
   | max mobility | 6 | `$14EC` | 443 | 38.5% |
   | start position | 7 | `$129C` | 295 | 25.6% |
   | pawn endgame | 10 | `$12BC` | 303 | 26.3% |

   The test **fails** if the worst case ever exceeds half of capacity, which
   is the point at which this argument would stop holding. It is the guard;
   the deleted comment was not.

Residual risk if it somehow happened anyway: the corrupting bytes are move
records (tier/from/to/flags), so `BOOK_MAGIC` would almost certainly stop
reading `'B','K'` and the probe would cleanly miss and search. But this is
downstream of a condition that has already destroyed the search, so it is
not the interesting failure.
