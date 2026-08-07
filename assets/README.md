# Art assets

Hand-drawn in DazzleDraw on the Apple II (well, an emulator) by zellyn.

- `chess2-dazzledraw-save.bin` — the chessboard and piece set, as a raw
  16 K double-hires A2FC save: 8 K aux bank then 8 K main bank, the
  actual byte layout DazzleDraw writes. Decode it with `cmd/dhgr2png`.
  Monochrome-targeted: the striped light squares NTSC-artifact into
  colour on composite displays, so a monochrome monitor profile is the
  intended look. The name is the DazzleDraw picture it came off:
  **CHESS2**, which supersedes the original CHESS1 (see *History* below).

The raw save is the preferred committed format; a PNG export alongside
it is welcome for browsing on GitHub, but the `.bin` is what the build
reads.

**This file is the single source of truth for the board.** Nothing in
the repo writes it — `cmd/gentiles` slices it into
`internal/tiles/tileblob.bin` and the two generated `asm/tile*.inc`
files, and every geometric assumption those rest on is asserted against
the actual pixels. Edit it in DazzleDraw, then re-run the generator.

---

## Drawing spec

Everything below is measured from the drawing itself, and enforced by
`go run ./cmd/gentiles -check`. You do not need to read any Go to follow
it.

### The grid

The board is an 8x8 grid of **42 x 19 dot** squares whose top-left
square starts at screen dot **(8, 2)**. So square (row *r*, file *f*)
covers x = 8 + 42*f* .. +41 and y = 2 + 19*r* .. +18, with row 0 = rank
8 and file 0 = the a-file. A square is **light** when (*r* + *f*) is
even; row 0 reads L D L D L D L D.

Inside a square, coordinates are written **dx** (0..41, left to right)
and **dy** (0..18, top to bottom).

**42 x 19 is the size the engine renders.** The drawn square and the
rendered tile are the same rectangle, so nothing you draw inside a
square is thrown away. That was not always true; see *History*.

### The one rule

> **All ink must lie inside `dx 8..34`. The full height, `dy 0..18`, is
> yours.**

A rendered square is 42 dots wide — three DHGR bytes per plane — and
only the **middle four** of its six byte columns are stored. That is
what makes a tile 76 bytes instead of 114: the difference between the
tiles fitting in memory and not. Ink in `dx 0..7` or `dx 35..41` breaks
the format and the generator refuses to run.

(The hard limit is actually `dx 7..34`, the dots the four stored byte
columns cover; `dx 8..34` is the declared spec, one dot tighter on the
left, so the drawing has somewhere to be wrong before it is broken.
There is **no slack on the right**: `dx 34` is the last stored dot.)

Vertically there is no rule to break: the tile is all 19 rows. Today's
artwork uses `dy 0..17` and leaves row 18 spare.

### The frame

The drawing carries its own frame, and **the frame is what pins the
grid**:

```
x:  0..3    bar        4..7    gap      8..343   the 8x8 grid   344..347 gap   348..351 bar
y:  0       rule       1       gap      2..153   the 8x8 grid   154      gap   155      rule
```

- The side **bars** are 4 dots wide and fully lit down the whole height.
- The top and bottom **rules** are 1 dot and fully lit across the whole
  width.
- The **gap** between the frame and the grid — 4 dots at the sides, one
  row top and bottom — must be **completely dark**.
- The whole drawing's lit bounding box is therefore x 0..351, y 0..155.

The checker asserts that eight squares of 42 x 19, plus those gaps,
**exactly span** what the frame encloses. Redraw the board at a
different square size and it fails there, at the source, instead of
quietly clipping every piece.

### The background

- A **dark** square is entirely blank.
- A **light** square is a vertical dither: every **even absolute x** dot
  is lit, every odd one dark. Since both 8 (the origin) and 42 (the
  square width) are even, that is the same as "every even `dx` is lit"
  in every light square.

The dither is what makes the light-square background a constant byte
pair (`$55` aux / `$2A` main), and a 42-dot pitch keeps its phase (42 is
a multiple of 14). Do not shift it, do not redraw it by hand, and do not
let a piece leave a stray dot in the margins: the runtime *synthesises*
every background byte rather than storing it.

### What must be drawn where

The artwork is the **standard start position** — rows 0, 1, 6 and 7
fully occupied, all sixteen pawns drawn — plus **four extra squares**
that supply the king and queen on the square shade their own back rank
cannot reach:

| square | piece |
| --- | --- |
| d6 (row 2, file 3) | black king, on a dark square |
| e6 (row 2, file 4) | black queen, on a light square |
| d3 (row 5, file 3) | white king, on a light square |
| e3 (row 5, file 4) | white queen, on a dark square |

Constraints that follow:

- **Every other square in rows 2..5 must be exactly background.** The
  runtime draws empty squares from the background constants, so a stray
  dot there ships as a rendering artefact. Ranks 5 and 4 (rows 3 and 4)
  are the only *completely* empty ranks — rows 2 and 5 carry the four
  extras on the d- and e-files, so do not use one of those rows as a
  blanket "this rank is background" reference.
- **All eight pawns of a rank must be drawn identically.** The
  transcription slices one light-square pawn and one dark-square pawn
  and reuses them for all eight files; a pawn touched up on only one
  file would ship a board that does not match the drawing.
- **The frame stays put**, for the reason in *The frame* above.

---

## Checking a redraw

`-check` reads the artwork, **writes nothing**, and prints every problem
in one pass — lost ink first, then the grid fit, then each broken
assumption with the square and coordinates named, then a per-piece table
of where the ink actually sits.

```sh
# check a work in progress, and see what the tiles will look like AFTER
# slicing, without booting anything:
go run ./cmd/gentiles -check -art /path/to/work-in-progress.bin -png /tmp/tiles.png

# check the committed artwork:
go run ./cmd/gentiles -check     # or: make check-tiles
```

Exit status:

| code | meaning |
| --- | --- |
| 0 | clean — the artwork slices and loses nothing. **The goal, and where it is today.** |
| 2 | ink is lost, or a geometric assumption is broken — fix before regenerating |

Status **1 is retired**. It used to mean "slices, but the top trim clips
ink", and there is no trim any more; see *History*. It is left
unallocated rather than reused, so an old script that special-cases it
cannot silently mis-read a new run.

Add `-dots` to list every lost dot's `dx` individually instead of as
ranges.

The headline reads like this when the artwork is where it should be:

```
LOST INK (drawn outside the kept window: rows dy 0..18, dots dx 7..34)
  NONE. Every drawn dot reaches a tile. This is the goal state.
  Live half of this check: the DOT COLUMNS (dx outside 7..34 is dropped by the
  4-byte tile row). The ROW half cannot fail while SrcTrimTop=0 and TileH=19
  cover the whole 19-row square — that half is a tautology today, not a gate.
```

and like this while ink is still being lost:

```
LOST INK (drawn outside the kept window: rows dy 0..18, dots dx 7..34)
  3 pixels of piece ink are LOST, across 2 squares (0 outside the kept rows,
  3 outside the kept dot columns):

  a1  white rook on dark                   2 px
        dy=10: 2 px at dx 39-40
  ...
  Fix by moving that ink inside dy 0..18, dx 7..34.
```

`make test` runs the checker (`cmd/gentiles`' `TestCheckOnCommittedArtwork`)
and requires exit 0, so a redraw that breaks an assumption fails a gate
rather than shipping.

Once it is clean, regenerate the committed outputs:

```sh
go run ./cmd/gentiles     # or: make tiles
```

That rewrites `internal/tiles/tileblob.bin`, `asm/tiledefs.inc` and
`asm/tiles.inc`. `internal/tiles`' tests then compare the committed blob
against a fresh slice, so a forgotten regeneration is also a failure and
not a surprise on the disk.

---

## History: why the board was redrawn (2026-08-06)

The first artwork was DazzleDraw picture **CHESS1**, a specimen sheet
drawn on a **44 x 21** grid. The UI only has 152 scanlines for eight
ranks, so `cmd/gentiles` sliced 44x21 squares into **42x19** tiles by
dropping the two rightmost dot columns (harmless — they were background)
and **the top two source rows** (not harmless).

That top trim silently threw away **28 dots** of bishop and king finial,
board-wide. It showed up where these things always show up: on screen.
The trim depth had been chosen as a measured knee — two rows cost 28
dots, three cost 80, one from the bottom cost 115 — which made it a
defensible bad option, not a good one.

The fix was not a cleverer trim. The board was **redrawn at the engine's
own square size** as picture **CHESS2**: 42 x 19, so the source square
and the rendered tile are the same rectangle and `SrcTrimTop` is 0.

Two things changed in the checker as a result, and it is worth knowing
which:

- The old headline measured *ink the top trim threw away*. With no trim
  that measurement is structurally zero and could never fail again — a
  gate that had quietly evaporated. It was **widened**, not deleted: the
  headline now measures ink outside the whole **kept window**, rows and
  dot columns together. The dot-column half is very much alive; the row
  half is a tautology today, and the report says so in as many words
  rather than letting a zero look like a passing check.
- A **new** check asserts the thing that actually went wrong: that the
  declared grid exactly fills the frame the artist drew around it, and
  that the gap between frame and grid is blank. Nothing had ever said
  the source square and the tile were the same size. Now something does.

The 44x21 CHESS1 save is superseded; it remains in this repository's git
history at `assets/chess-dazzledraw-save.bin` for anyone who wants to
compare.
