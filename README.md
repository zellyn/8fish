# 8fish

**A modern-strength chess engine for the Apple IIe** — the 1 MHz 6502, 128K
machine of 1983 — built to see how far chess-engine theory has moved since the
programs that actually ran on that hardware. The name is the joke: *Stockfish,
on 8 bits.*

![The 8fish splash screen: a rainbow fish, the 8FiSh wordmark, and "Seagull Sisters Software"](assets/8fish-splash.png)

It boots from a floppy, draws a double-hi-res board, plays a real opening book,
thinks on your clock while you decide, and can save your game back to the disk
— all on a machine with 64K of usable RAM and no clock to read.

![8fish playing a Sicilian: green double-hi-res board with an 80-column status window showing the level, "BOOK: Sicilian Defense", and the command bar](assets/8fish-board.png)

## Playing it

Boot `asm/8fish.dsk` in slot 6 of an Apple IIe (128K) — real hardware, or an
emulator like [Virtual \]\[](https://virtualii.com), MAME, or the bundled
`goapple2`.

- **Move** by typing coordinates (`e2e4`) *or* driving an on-board cursor with
  the arrow keys — space selects the from- and to-squares, ESC cancels.
- The 80-column window under the board shows the level, the opening name while
  you're in book, and a command bar:
  - **N** new game · **T** take back · **L** level · **S** switch sides ·
    **R** resign · **D** offer draw · **Q** quit · **?** help
  - **W** save the game to disk · **O** load it back
- It **ponders while it's your move** — and keeps thinking in the gaps between
  your keystrokes, so nudging the cursor around to study the position doesn't
  throw its thinking away.

The opening book on the disk holds **3,639 lines** (curated main lines plus the
public-domain [lichess opening](https://github.com/lichess-org/chess-openings)
tree for breadth); a smaller book is always resident so a diskless boot still
opens sensibly.

## Strength

8fish targets **Sargon III** — among the strongest programs of the early Apple
II era — and beats it in symmetric, equal-time matches. Across configurations
the measured margin has run from **+89 Elo** (156–81–63, 95% CI [+54, +126])
upward; the definitive *both-sides-pondering* gauntlet is being finalized (see
[docs/results.md](docs/results.md) for every match and its methodology). Every
Elo number comes from the real 6502 image driven through a cycle-accurate
emulator, never from the Go model alone.

## Quick start (building it)

Requires Go and cc65 (`brew install cc65`). Dependencies
([go6502](https://github.com/zellyn/go6502),
[goapple2](https://github.com/zellyn/goapple2)) resolve as normal Go modules;
to hack on them locally, `go work init . ../go6502 ../goapple2`.

```sh
make        # assemble, run smoke tests, build the engine, run the full suite
make dsk    # build asm/8fish.dsk, the bootable floppy
```

`make dsk` also needs [diskii](https://github.com/zellyn/diskii)
(`go install github.com/zellyn/diskii@latest`), which writes peterferrie's
"Standard Delivery" boot loader, and — only when regenerating the vendored
disk driver or the on-disk book — [ACME](https://sourceforge.net/projects/acme-crossass/).

`cmd/a2run` runs any 6502 binary against the go6502 core and the Apple IIe 128K
memory model (`goapple2/iie`, validated against
[a2audit](https://github.com/zellyn/a2audit)'s hardware-verified tests).

## How it works (the interesting part)

The **engine** is hand-written 6502 assembly: negamax/PVS with late-move
reductions, null-move pruning, check extensions, a
[PeSTO](https://www.chessprogramming.org/PeSTO%27s_Evaluation_Function)-style
tapered evaluation, and a transposition table living in the 128K machine's
auxiliary RAM. The on-device UI costs the engine image **zero bytes** — it
runs from Language-Card RAM, above the engine.

Two of the hardware's absences drove the most interesting work:

- **No clock to read.** The IIe has no timer the CPU can query, so time
  management runs on a *software clock* — a cycle estimator calibrated against
  the emulator — that spends its budget within ~1% of intent.
- **No spare RAM.** The opening book, the boot splash, and the save/load
  writer can't all stay resident. The book and splash load from disk into
  memory that's otherwise dead until the search needs it (the splash is
  PackBits-compressed and shown *over* the load); the write-capable disk driver
  is loaded on demand, used, and discarded. The disk I/O uses peterferrie's
  [ProRWTS2](https://github.com/peterferrie/prorwts).

The **development harness** (Go) is the other half of the project. A "mirror"
re-implements the engine's search in Go as a parity twin; changes are screened
in the mirror, ported to asm, and confirmed with an SPRT match against the real
image. Hundreds of `go test` gates assert everything from move legality to the
exact bytes on the double-hi-res screen; risky changes are additionally put
through an independent adversarial review and mutation-checked (break the thing
a test guards, confirm the test fails). The engine binary has stayed
byte-identical across the entire UI, book, and disk feature set.

More: [the plan](docs/plan.md) · [decisions](docs/decisions.md) ·
[dev loop & testing](docs/testing.md) · [UI design](docs/ui-design.md) ·
[the disk driver design](docs/prorwts2-design.md) ·
[research notes](docs/research/).

## Status

8fish runs correctly across three independent emulators (`goapple2`, MAME,
Virtual \]\[). It has **not yet been validated on real silicon** — the honest
next step, especially for the on-demand disk *writes* that back save/load
(exercise a save on a sacrificial disk and diff the image first).

## Credits

Engine, UI, and harness by Zellyn Hunter, with a lot of adversarial review.
The splash screen — the rainbow fish and "Seagull Sisters Software" — is
hand-drawn double-hi-res art. Standing on the shoulders of
[ProRWTS2](https://github.com/peterferrie/prorwts) and Standard Delivery
(peterferrie), [cc65](https://cc65.github.io/) and ACME, the
[PeSTO](https://www.chessprogramming.org/PeSTO%27s_Evaluation_Function)
evaluation, and the [lichess opening book](https://github.com/lichess-org/chess-openings)
(CC0).
