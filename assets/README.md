# Art assets

Hand-drawn in DazzleDraw on the Apple II (well, an emulator) by zellyn.

- `chess-dazzledraw-save.bin` — the chessboard and piece set, as a raw
  16 K double-hires A2FC save: 8 K aux bank then 8 K main bank, the
  actual byte layout DazzleDraw writes. Decode it with `cmd/dhgr2png`.
  Monochrome-targeted: the striped light squares NTSC-artifact into
  colour on composite displays, so a monochrome monitor profile is the
  intended look.

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

The board is an 8x8 grid of **44 x 21 dot** squares whose top-left
square starts at screen dot **(8, 2)**. So square (row *r*, file *f*)
covers x = 8 + 44*f* .. +43 and y = 2 + 21*r* .. +20, with row 0 = rank
8 and file 0 = the a-file. A square is **light** when (*r* + *f*) is
even; row 0 reads L D L D L D L D.

Inside a square, coordinates are written **dx** (0..43, left to right)
and **dy** (0..20, top to bottom).

### The two rules

> **All ink must lie inside `dy 2..20` and `dx 8..34`.**

Everything else in this document is the reason for those two numbers.

**`dy 2..20` — the top trim.** A rendered square is 19 scanlines, not
21: eight squares of 21 would not fit the screen alongside the rest of
the UI. The two rows that go are the **top** two, so anything drawn in
`dy 0` or `dy 1` is silently thrown away. (Two is the measured knee:
dropping the top two rows costs far less ink than dropping three, and
dropping from the bottom is much worse still — the checker prints the
whole curve.)

**`dx 8..34` — the content window.** A rendered square is 42 dots wide
(three DHGR bytes per plane), not 44, and only the **middle four** of
its six byte columns are stored. That is what makes a tile 76 bytes
instead of 120 — the difference between the tiles fitting in memory and
not. The window has a little slack on each side, but ink in `dx 0..7` or
`dx 35..43` breaks the format outright and the generator refuses to run.

### The background

- A **dark** square is entirely blank.
- A **light** square is a vertical dither: every **even absolute x** dot
  is lit, every odd one dark. Since both 8 (the origin) and 44 (the
  square width) are even, that is the same as "every even `dx` is lit"
  in every light square.

The dither is what makes the light-square background a constant byte
pair (`$55` aux / `$2A` main), and a 42-dot pitch keeps its phase. Do
not shift it, do not redraw it by hand, and do not let a piece leave a
stray dot in the margins: the runtime *synthesises* every background
byte rather than storing it.

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
  dot there ships as a rendering artefact.
- **All eight pawns of a rank must be drawn identically.** The
  transcription slices one light-square pawn and one dark-square pawn
  and reuses them for all eight files; a pawn touched up on only one
  file would ship a board that does not match the drawing.
- **The border stays put.** Row 0 and row 171 are fully lit across the
  drawing, as are columns 0..3 and 364..367, and the whole drawing's lit
  bounding box is x 0..367, y 0..171. The border is what pins the grid
  origin — move it and every square moves.

---

## Checking a redraw

`-check` reads the artwork, **writes nothing**, and prints every problem
in one pass — clipped ink first, then each broken assumption with the
square and coordinates named, then a per-piece table of where the ink
actually sits and how much room it has above it.

```sh
# check a work in progress, and see what the tiles will look like AFTER
# slicing (clipped rows already gone), without booting anything:
go run ./cmd/gentiles -check -art /path/to/work-in-progress.bin -png /tmp/tiles.png

# check the committed artwork:
go run ./cmd/gentiles -check
```

Exit status:

| code | meaning |
| --- | --- |
| 0 | clean — the artwork slices and loses nothing. **The goal.** |
| 1 | the artwork slices, but the top trim clips ink (dots are lost) |
| 2 | a geometric assumption is broken — fix before regenerating |

Add `-dots` to list every clipped dot's `dx` individually instead of as
ranges.

The headline section reads like this while ink is still being lost:

```
CLIPPED INK (source rows dy 0..1, dropped by the tile)
  28 pixels of piece ink are LOST, across 10 squares:

  c8  black bishop on light                5 px
        dy=0:  2 px at dx 20,22
        dy=1:  3 px at dx 18,21,24
  ...
  Fix by moving that ink down: nothing may be drawn above dy 2.
```

and like this when the redraw is done:

```
CLIPPED INK (source rows dy 0..1, dropped by the tile)
  NONE. Every drawn dot survives the trim. This is the goal state.
```

Once it is clean, regenerate the committed outputs:

```sh
go run ./cmd/gentiles     # or: make tiles
```

That rewrites `internal/tiles/tileblob.bin`, `asm/tiledefs.inc` and
`asm/tiles.inc`. `internal/tiles`' tests pin the measured trim cost
(currently 28 dots), so a successful redraw will need that number
updated in `TestTrimCosts` and in package `tiles`' doc comment — which
is the point: the numbers in the source are measurements, not guesses.
