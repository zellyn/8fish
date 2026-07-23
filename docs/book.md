# Resident opening book

A hand-curated opening book, compiled to a compact binary blob laid out
**exactly** as it will sit resident at `$2000-$3FFF` (the verified-free
hi-res page-1 hole, which the engine never uses — it displays via the
text screen). The engine now probes it **on-device**: the 6502 driver
binary-searches the resident blob and plays book moves itself (see
"Resident asm probe" below). The original Go-side bridge probe remains as
the reference the asm probe is proven byte-for-byte equal to.

## Files

| File | Role |
|------|------|
| `internal/book/lines.go` | curated source: 48 sound main lines, each `{ECO, Name, UCI moves}` |
| `internal/book/build.go` | validates every line legal move-by-move through refchess (refuses illegal lines); keys each position on the engine hash |
| `internal/book/book.go` | blob format, encode/decode, binary-search probe, weighted pick, `HashFEN` keying |
| `cmd/genbook/main.go` | generator → `internal/book/bookblob.bin` + `asm/book.inc` |
| `internal/book/bookblob.bin` | generated resident blob (embedded by package book) |
| `asm/book.inc` | generated asm layout defs for the future resident probe |

Regenerate with `go run ./cmd/genbook` (from repo root).

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

## Blob layout (as it sits at `$2000`)

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

- 48 lines → 312 entries (2808 B) + 48 names (1050 B) + 8 B header
- **blob = 3866 bytes** = 47.2% of the 8 KB hole (target was ≤ 6 KB)

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
sit at `$2000`, so promoting to an asm-resident probe is a pure read-side
addition — no format change.

**Real-hardware equivalent:** at startup the loader reads the ~3.9 KB blob
from disk **once** into `$2000-$3FFF` (e.g. 8 consecutive 512-byte
sectors, ~4 KB) and never touches it again. It is independent of the
transposition table, which lives in the aux bank. `asm/book.inc` gives the
resident base and field offsets the loader and probe share.

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
5. **Miss** (no `BK` magic at `$2000`, or key not found) → `BOOKHIT`=0; the
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
book loaded (`$2000` has no magic — the state for every existing test) the
search path is byte-identical: `TestMicroAB`/`Improving`/`Adopted` grand
totals are unchanged.

### Blob delivery

On real hardware the loader reads the ~3.9 KB blob once from disk into
`$2000-$3FFF` (e.g. 8×512-byte sectors) and never touches it again;
independent of the aux-bank TT. In the harness, `chesstest.LoadBook(m,
blob)` pokes the identical bytes; `chesstest.AsmBookProbe` drives one probe
pass. Existing tests never call `LoadBook`, so `$2000` stays clear.

### Correctness gate (asm == Go)

`chesstest.TestBookProbeParityASMvsGo` walks every in-book position (264,
30 multi-move) and, over a full residue period of r plus wide edge values
(4194 probes), asserts the asm probe's `FROM/TO/FLAGS` and `NAMEID` equal
`Book.Probe(key, r)` byte-for-byte; `TestBookProbeWeightedDistribution`
confirms the pick frequencies equal the weights; `TestBookProbeOutOfBook`
confirms misses match. `TestBookFollowThenSearchDriver` and ucibridge's
`TestBridgeBookFollowThenSearch` play a full opening on-device.

### Coexistence note (move stack)

`MOVESTACKTOP` is `$2000` and the generator's overflow guard may write up
to ~124 bytes past it (`$2000-$207F`) before trapping — only when the move
stack is at capacity (~1151 stacked moves across the recursion), which no
realistic search approaches. In that pathological case it would corrupt the
book header; the book is probed only at the root before search, so it would
affect at worst the *next* move's probe (which would see no magic and
search). Not a concern at practical depths.
