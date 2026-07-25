# Decision log

Lightweight ADR-style records. Statuses: **accepted**, **proposed**,
**revisit** (accepted but flagged for re-evaluation at a named milestone).

## D1: Cross-assemble with ca65/ld65 — accepted

Merlin32, ACME (which a2audit pins), 64tass, and go6502's own assembler
(Merlin/SCMA flavors) all work, but ca65 has the strongest linker story for
a multi-segment image (main RAM, Language Card RAM, aux RAM segments with
separate load/run addresses), maintained macro tooling, listing/map files
we can feed back into the Go harness for symbolized traces, and the largest
Apple II community usage. go6502's assembler stays useful for its own
tests; a2audit stays on its pinned ACME. Revisit only if ld65 segment
handling proves awkward for the bank-copy loader.

## D2: Target the NMOS 6502, not the 65C02 — accepted (revisit at M4)

The 65C02 (enhanced IIe) offers PHX/PHY/PLX/PLY, BRA, STZ, (zp) indirect,
INC A, JMP (abs,X). Honest ceiling (adversarial review): ~8-15% in hot
paths, which at fixed clock is roughly 12-17 Elo — real, but smaller than
any single search feature on the list. Against it:

- go6502 has no 65C02 opcode support (documented-NMOS only), and its
  gate-level perfect6502 lockstep verification — our strongest correctness
  asset — exists only for the NMOS part. A 65C02 core would be new; it
  could be *functionally* verified (Dormann's 65C02 extended-opcode test
  exists) but not at gate level (no public 65C02 netlist).
- NMOS runs on every IIe including unenhanced ones (and the ][+ for a
  possible 64K build), broadening real-hardware targets.
- The engine's speed lives mostly in algorithmics (pruning, ordering), not
  15% instruction-level wins.

Decision: all engine code is documented-NMOS-6502 clean. If profiling at
M4+ shows hot loops where 65C02 forms would win big, revisit with a
build-flag approach (conditional macros emitting 65C02 forms) — and only
then design 6502/65C02 switching for go6502.

## D3: Own harness importing go6502 + goapple2/iie; no full emulator — accepted

The harness (`cmd/a2run`) is ~150 lines: go6502 CPU + the iie memory model
+ traps. No ROM boot, no video, no disk. goapple2 remains the interactive
emulator; the new `iie` package retrofits the IIe memory system into the
goapple2 repo where it is generally useful (stage 2 wires it into the
full machine).

## D4: Aux memory via RAMRD/RAMWRT with 80STORE off; aux-touching code runs from LC RAM or $0000-$01FF — accepted

With 80STORE off, RAMRD/RAMWRT bank all of `$0200-$BFFF`, giving ~47.5K of
aux for tables with dead-simple rules. Critical correctness details
(confirmed by adversarial review against Sather):

- RAMRD switches *instruction fetches* in `$0200-$BFFF` too, so any
  routine that turns RAMRD on must itself execute from `$0000-$01FF`,
  Language Card RAM (`$D000+`), or must only toggle RAMWRT
  (writes-to-aux while executing from main is safe). Aux-access
  primitives therefore live in LC RAM.
- While RAMRD is on, main `$0200-$BFFF` is unreadable — so aux
  primitives take arguments and return results **in zero page only**
  (hash key in, entry copy out); they cannot touch main-RAM tables
  mid-toggle.
- **ALTZP is never touched** (hard invariant for stage 1): it swaps ZP,
  the stack, and LC RAM simultaneously — code in main LC RAM that sets
  ALTZP pulls its own instruction fetches out from under itself, and any
  code that sets it loses its stack for RTS. The aux-LC "stage-2 ideas"
  in the memory budget are parked behind lifting this invariant safely.

The audit suite validates the underlying switch semantics.

## D5: Board representation: 0x88 board + piece lists — accepted (M1 measured)

M1 outcome (2026-07-17): implemented with board-byte-encoded list indexes
and tombstone removal; perft exact on all six standard positions, at
~1,600-1,800 cycles/leaf (make + in-check + unmake + amortized movegen)
=> ~580 NPS equivalent, inside the plan's 420-680 budget band. The
comparison 10x12 arm was not built: the budget gate passed with margin,
so the extra arm would only be justified if optimization later stalls.
Original proposal follows.

0x88 gives a one-instruction off-board test (`AND #$88`), signed-byte move
deltas, and difference-indexed attack/direction lookup tables (256-byte,
page-aligned) that prune candidate directions for is-square-attacked
(sliders still ray-walk for blockers — shorter loops, not no loops).
16-entry piece lists per side drive move generation (no 64/128-square
board scans), following H.G. Muller's "mailbox trials" results: captures
generated first (per-victim staging approximates MVV order with no
sorting). Tables indexed directly by 0x88 square (128 bytes/table, 64
wasted) to avoid index conversion in hot paths.

Open sub-decisions the M1 spike must price in BOTH arms (0x88+lists vs
10x12), per adversarial review — the naked movegen inner loop is not the
whole cost:
- Finding a captured piece's list entry: board-byte encodes list index
  (color|type|index fits 8 bits; Kittinger-style) vs a 128-byte
  square-to-index map vs linear scan of 16.
- Removal discipline: tombstone (square=$FF — skipped for free by the
  `AND #$88` test, a genuine 0x88 synergy) vs swap-with-last (breaks
  index stability; grows the undo frame).
- Promotion bookkeeping (piece type changes mid-list).

## D6: 32-bit Zobrist hashing; transposition table in aux RAM — accepted, implemented (M3, 2026-07-18)

32-bit keys, incrementally XOR-updated in make/unmake (~160 cycles for
the two square-keys plus side-to-move; see the cycle budgets in plan.md).
Key set: 12 piece-kinds x 128 squares x 4 bytes = 6K in main RAM, 0x88-
indexed, **plus side-to-move, 16 castling-rights, and 8 ep-file keys**
(forgetting these is a classic TT bug); keys generated offline by a
vetted PRNG (bad 32-bit key sets measurably raise collision rates).

TT in aux `$0200-$BFFF`: **4096 entries x 8 bytes = 32K — power-of-2
only** (mod-5888 indexing needs a division the 6502 won't pay per probe,
and 8192x8 = 64K can never fit), leaving ~15K of aux explicitly for the
opening book and optional KPK data. Entry: 20 verify bits (all the
non-index bits — they're free), packed move, 16-bit score, depth, bound
flags. Collision math: ~0.3-0.6 false verify-matches per game at our
node rates — harmless for scores (Hyatt & Cozzie), **fatal for an
unvalidated move on a 6502** (piece-list/accumulator desync). As built,
this is handled by **list-match validation, not a standalone pseudo-
legality checker**: the TT move (and killer moves — also from sibling
nodes) are only searched if they match a from/to pair actually present
in this node's freshly generated move list. The move loop already runs
that list as ordered passes — pass 0 fetches the TT move and searches it
only on a match, later passes explicitly skip it, and killer passes work
the same way (search.s's `sfetch`/`squietk`/`squietr`; pass order: TT
move, heavy captures, light captures, killers, quiets). A hash collision
that names a from/to pair absent from the real list simply matches
nothing and is never applied — free correctness from a scan the loop
runs anyway, superseding the originally planned standalone checker
(right piece on from-square, target not own piece, slider path clear),
which would have cost ~60-100 extra cycles per TT-move node for no
extra safety.

Aux probe cost ~180-230 cycles round trip (RAMRD toggles + 8-byte ZP
copy from LC-resident primitive); store ~120 (RAMWRT-only, callable from
main code). A main-LC TT would save ~100 cycles/probe but caps at ~1024
entries — two halvings ≈ ~16 Elo by Muller's rule — aux wins. QS
probing: default OFF (~230 cycles on the ~60% of nodes that are QS, for
ordering MVV staging mostly provides); settle by measurement at M3-M4.

Replacement: always-replace vs two-slot depth-preferred — measured, not
assumed (evidence says always-replace is nearly as good at small sizes).
Repetition detection uses a separate game-history + search-path list of
truncated hashes in main RAM, not the TT; a truncated-hash match is
verified against the full 32-bit key before scoring a draw. See plan.md
for repetition-vs-TT ordering rules.

Amendment (M4, 2026-07-18): a null-move cutoff has no associated move, so
it stores a moveless `TT_LOWER` entry (from = to = `NOSQ`, score = beta)
instead of skipping the store. On a later visit, pass 0's list-match
against `NOSQ` simply fails, so the node falls through with no TT move —
the cutoff is captured purely via the score/bound fields, consistent
with the list-match design above.

## D7: Reserve hires page 1 ($2000-$3FFF) for a future board display — accepted

The engine and its tables stay out of `$2000-$3FFF` so a simple hires board
(nothing fancy) can arrive later without a memory-map upheaval. Engine code
loads at `$4000` upward; `$0800-$1FFF` holds low tables/move stack. If we
end up desperate for main RAM, this is the first decision to revisit.

## D8: Harness I/O via store-traps at $BFF0 (COUT) / $BFFF (exit); engine I/O behind a vector table — accepted (amended)

Store-traps are invisible on real hardware (plain RAM writes). The design
calls for the engine to reach I/O only through a small vectored module
(put-char, get-line, move-out), so the same engine core would link
against a harness I/O module today and a keyboard/screen (or Super
Serial) module later. **Not yet built** (through M4): `engine.s` stores
to `COUT_TRAP`/`EXIT_TRAP` and reads `CLOCK_TRAP` directly (defs.inc);
the vector module is worth building once a second I/O backend (M8's
keyboard/screen) makes the indirection pay for itself.

Amendments from adversarial review (both critics found the collision):
- **$BF00-$BFFF is reserved in BOTH banks** — the memory-budget tables
  and ld65 segment configs must exclude it. Without this, engine tables
  growing to the top of main RAM, or aux TT/book data crossing $BFF0,
  would spuriously emit protocol bytes or kill the run.
- Traps fire **only on main-bank stores** (the harness checks RAMWRT),
  so aux writes to those addresses are ordinary memory. Implemented.
- The trap addresses are canonical: the `-cout`/`-exit` flags exist for
  experiments, but the checked-in I/O module and all tooling assume
  $BFF0/$BFFF.

## D12: Harness input and long-lived sessions — accepted; landed differently than designed (M3, 2026-07-18)

Design: memory-mapped **input traps** mirroring keyboard-strobe hardware
shape — read $BFF1 = next input byte (pops), read $BFF2 = status ($80
when a byte is waiting). The engine polls status; the harness feeds
bytes from stdin (a goroutine). One long-lived a2run-style process holds
the session; the same engine code drives real keyboard hardware later by
swapping the I/O module (D8). Prefer this over PC-callbacks (less
hardware-faithful) and over $C000 keyboard emulation (drags in
ROM/soft-switch surface the trap harness avoids).

Both halves of the plan landed, but the input-trap half went unused: the
`harness` package now implements $BFF1/$BFF2 (`TrapMemory`), and
`(*Machine).Run` is resumable and returns on `WaitingForInput` so a
driver can `SendInput` and continue — exactly as designed, and exercised
by the harness package's own tests. And the a2run core was extracted
into the importable `harness` Go package (load/run/traps as a library),
so perft and gauntlet rigs (`internal/chesstest`) call it in-process
instead of scraping CLI output, also as designed. But the UCI bridge
(`cmd/uci`, `internal/ucibridge`) that motivated this decision doesn't
poll the input traps at all: no 6502 code reads $BFF1/$BFF2 today.
Instead the long-lived process is the Go UCI loop itself, which parses
each `position`/`go` on the Go side, pokes the position directly into a
fresh `Machine` per move, and carries the aux-bank TT forward between
moves (see `internal/ucibridge`). Revisit if M8's real keyboard hardware
needs the 6502 side to actually poll for input — until then the input
traps are implemented but dormant.

## D9: Time management: cycle/node budgets; VBL ($C019) polling on real hardware — accepted; cycle-budget scheme implemented (M3, 2026-07-18); hardware VBL polling still pending M8 (amended)

No interrupts: go6502 doesn't emulate IRQ/NMI delivery, unenhanced IIe VBL
polling is cheap anyway, and interrupt-free code is simpler to verify.

The original "poll $C019 every 256 nodes and count VBL edges" scheme was
**killed by adversarial review**: at ~2000 cycles/node, 256 nodes is
26-38 frames between samples, and a 1-bit flag cannot count frames it
never saw — the clock would run 15-30x slow. Corrected scheme:

- Primary through M7 (emulator): **fixed emulated-cycle budgets**. The
  engine's own node counter gives in-engine pacing; the SPRT/gauntlet
  rigs enforce cycles (deterministic AND time-faithful, unlike node
  budgets, which are blind to per-node cost changes — see plan.md
  testing). $C019 is now emulated (cycle-fed) so the hardware path is
  testable in the harness.
- Hardware (M8): poll $C019 every 1-2 nodes (LDA/EOR/BPL ≈ 10-13 cycles
  ≈ 0.5% overhead) so no VBL period (4,550 cycles) is ever missed;
  count frames at 59.92 Hz. Sense: on the IIe, $C019 bit 7 is LOW
  during vertical blanking (inverted on IIgs; IIc has no polled VBL).
- Iteration policy: don't start a new iteration past ~50% of budget
  (EBF ~3 means a new iteration roughly doubles spend); on mid-iteration
  hard abort at 2x, play the best move found so far in the current
  iteration if at least one root move completed and improved on the
  previous iteration's score — else the previous iteration's move.
  Iteration 1 is abort-immune (`checkclock` is a no-op while `CURDEPTH`
  < 2), so a hard abort can never land before at least one full
  iteration has completed and produced a real root move — this closes
  the M3 bug where an aborted partial iteration's move (arbitrary once
  its TT entry was evicted) got reported instead (see docs/results.md).

## D10: Plain docs/ with this decision log, not full ADR directory — accepted

Two-contributor hobby repo; decision granularity above is enough. If the
project grows contributors or the log gets unwieldy, graduate entries to
docs/adr/.

## D11: Elo measurement: emulated engine bridged to UCI; four-part protocol — accepted (amended)

`cmd/uci` (Go) presents the emulated engine as a UCI engine to
cutechess-cli (MessChess/retro-Sargon prior art). Adversarial review
rewrote the measurement design; the four products, in order of rigor:

1. **Feature gates (SPRT self-play)**: both sides at fixed emulated-cycle
   budgets, **paired openings from a standard suite with color reversal**
   (a deterministic engine from startpos plays one photocopied game —
   without openings the ±Elo math is fiction), SPRT elo0=0/elo1=10.
2. **Absolute CCRL-anchored gauntlet**: engine at its real control
   (30-60s x 1.0205 MHz as a cycle budget ≈ 0.2-0.4s wall at ~170x);
   opponents at their CCRL-valid conditions (40/4-style), chosen to
   **bracket the engine's actual level within ±200 Elo** (pool roughly
   1000-1700 — not just TSCP/micro-Max, which may sit 200-500 above).
   Generous cutechess wall margins so the emulated engine never forfeits
   on wall clock. ~500-1000 games; report error bars including anchor
   uncertainty, not just 630/sqrt(N).
3. **Node-odds ladder** (the thesis number): node/cycle-limit micro-Max
   and TSCP to create calibrated opponents near our level, rate them
   against pool 2, and find the crossover: "micro-Max needs Nx our
   nodes to hold 50%." This is the cleanest quantification of how far
   theory has come.
4. **Period showdown (M7)**: emulated period engines at authentic 1 MHz,
   equal per-move emulated time, paired openings, pre-registered win
   criterion (>=65% over >=100 paired games).

## D13: Eval-dither SEED manufactured from human keystroke timing — accepted, module landed (2026-07-25)

The eval dither (`eval.s evseed`, `SEED` = $FD) needs a fresh per-move
byte or the engine is fully deterministic and replays identical games
from a repeated opening (measured: with dither off, a 300-game symmatch
over a 40-opening pool is really ~40 distinct games; mirror measurement
below: 20 repetitions of one opening produced **1** distinct game).
The harness pokes `SEED` from the host, but the IIe has **no real-time
clock, no timer, and no hardware RNG**, so the shipped M8 build has to
manufacture entropy locally.

**Source: when the human presses a key.** Nobody releases a key on a
cycle boundary. `asm/entropy.inc`:

- `ENTCNT` ($0210, 16-bit) spins in the driver's keyboard-wait loop
  (`entkey`: `inc`/`bne`/`lda KBD`/`bpl` = **16 cycles/iteration**, so its
  low byte turns over every 4096 cycles = **4.01ms** and the full 16 bits
  every ~1.03s) — both far below human timing jitter, which makes the low
  byte uniform over 0..255 for any real keypress.
- At **every** keypress (menus, move entry, confirmations — the UI reads
  all keys through `entkey`) the counter's low byte folds into `ENTROPY`
  ($0212) with `ENTROPY = ROL(ENTROPY) EOR ENTCNT` (`entfold`). A 4-5
  character move entry therefore mixes several independent inter-keystroke
  intervals, and the accumulator is never cleared: entropy accumulates
  across the session.
- `entseed` installs `SEED = ENTROPY`, forced nonzero ($5A fallback, since
  `SEED` = 0 means dither off), leaving the accumulator to keep folding.
- `ENTCNT`/`ENTROPY` are deliberately **never initialized**: cold-boot RAM
  garbage and a warm-restart carryover of the previous session are free
  entropy. First move of a game is already covered because starting a game
  is itself keystrokes (menu/setup); a machine-first game with literally no
  prior keypress falls back to whatever RAM held (worst case a fixed first
  move, which is a book move anyway).
- Optional ponder hook: `entfold` also takes an arbitrary byte, so a driver
  that ponders can fold `NODECNT`/`CLOCK` at the moment the human's first
  keystroke interrupts the search — how far the search got is as
  unpredictable as the keypress. Nothing in the search polls the keyboard,
  so this costs the hot path nothing.

**Separation from the engine.** The collector is driver-only cold code:
no engine file includes `entropy.inc`, and `engine.bin` is byte-identical
with this landed (verified: same sha256; `TestMicroAB` fingerprint gate
unchanged). A given `SEED` still produces exactly the same tree — this
decision changes only where the seed comes FROM.

**Harness parity.** `internal/entropy` is the byte-for-byte host model of
the same collector, proven identical to the real 6502 code by
`TestASMParity` (the actual `entropy.inc` runs under the emulator with the
harness input traps standing in for $C000, and the test plays the human).
`ucibridge`'s `Dither` now derives its per-move seed through that model
(`DitherSource: "entropy"`, the default) fed by the real elapsed time until
the opponent's move arrives plus the emulated cycles our own search spent —
so the gauntlet validates the shipping entropy plan instead of bypassing it
with a PRNG the IIe could never have. `DitherSource: "prng"` +
`DitherSeed` keeps a reproducible stream for tests.
