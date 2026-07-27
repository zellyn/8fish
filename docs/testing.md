# Testing and the development loop

This project develops a 6502 chess engine without touching disk images or
GUI emulators: assemble on the Mac, run headless in a Go harness built on
the cycle-accurate `go6502` CPU core, assert on results, repeat.

## The pieces

| Piece | Where | Status |
|---|---|---|
| 6502 CPU core | `github.com/zellyn/go6502/cpu` (sibling checkout `../go6502`) | Modernized: `go.mod` added, all tests pass, including Klaus Dormann's functional suite run in lockstep against the `visual/` gate-level (perfect6502) simulation |
| IIe 128K memory model | `github.com/zellyn/goapple2/iie` (sibling checkout `../goapple2`) | New package; see below |
| Headless runner | `cmd/a2run` (this repo) | Working |
| Assembler | ca65/ld65 (cc65 V2.18, via Homebrew) | Working; raw-binary link config in `hello/raw2000.cfg` |
| Hardware-truth memory tests | `github.com/zellyn/a2audit` (sibling checkout `../a2audit`) | Language Card suite passing against `iie`; aux-memory suite blocked on stage 2 |

## The dev loop

```sh
make            # assemble hello, run it in the harness, run all Go tests
make engine     # build the engine binary (asm/engine.bin)
# or by hand:
cd asm && ca65 -g engine.s -o engine.o
cd asm && ld65 -C engine.cfg engine.o -o engine.bin -Ln engine.lbl
go run ./cmd/a2run -bin asm/engine.bin -org 0x4000 [-dump 0300:0320] [-trace]
```

`a2run` loads the raw binary into main RAM (validating org/entry ranges
and overruns), jumps to it, and runs at full host speed (~100-170x real
time depending on workload — an emulated hour takes well under a wall
minute, so self-play gauntlets are practical; a `go test -bench` rig to
pin this number down lands with M1). It reports cycles and emulated time
(1.0205 MHz effective IIe clock) to stderr; program output (COUT trap)
goes to stdout, instruction traces (`-trace`) to stderr, so the two never
interleave. Memory dumps use the side-effect-free `Peek` path — dumping
`$C08x` does not flip Language Card state. The cycle limit is checked
between instructions (overshoot <= 7 cycles). `-rom` loads a 12K
$D000-$FFFF image for runs that need monitor/Applesoft ROM.

### Harness I/O conventions

On real hardware these addresses are plain RAM, so engine binaries that use
them via the swappable I/O module still run there unmodified. Traps fire
only on **main-bank** stores (aux writes to the same addresses via RAMWRT
are ordinary memory), and `$BF00-$BFFF` is reserved in both banks by the
memory map (D8) so engine tables can never collide with them.

| Address | Access | Means |
|---|---|---|
| `$BFF0` | store (main bank) | emit byte to stdout |
| `$BFFF` | store (main bank) | exit; stored value becomes the process exit code |
| `$BFF1` | read (main bank) | pop and return the next input byte (0 if none) |
| `$BFF2` | read (main bank) | `$80` if input is waiting, else 0; reading with an empty buffer also sets `WaitingForInput`, so a driving process can supply input (`SendInput`) and resume the run |
| `$BFF4`-`$BFF6` | read (main bank) | cycle count / 256, 24 bits little-endian, latched on the `$BFF4` read. **Disable it** (`m.Mem.ClockAddr = 0`) to get real hardware semantics — plain RAM — which is what `FT2_SOFTCLK` needs: the engine's estimated-cycle accumulator lives at this same address, and with the trap enabled the trap's answer wins. `m.Cycles` still reports the true count either way, which is what makes the estimator measurable (internal/chesstest/softclock_test.go). |
| `$C019` | read | VBL status derived from the cycle counter (bit 7 low during VBL, IIe sense) — lets the hardware timing path be tested pre-metal |

The `-cout`/`-exit` flags can relocate the traps for experiments, but
$BFF0/$BFFF are canonical — all checked-in code assumes them.

Landed as the engine needed them (M3, D12): the harness package supports
the input traps above — `(*Machine).Run` is resumable (each call gives
it a fresh cycle budget on top of cycles already run) and returns early
when the program polls `$BFF2` with no input pending (`WaitingForInput`),
so a driving process can `SendInput` and call `Run` again without
restarting the process — and the a2run core is now the importable
`harness` package (`harness.New` / `(*Machine).Run`); `cmd/a2run` is a
thin CLI wrapper around it, and perft/gauntlet rigs (`internal/chesstest`)
call it in-process instead of scraping CLI output. The UCI bridge
(`cmd/uci`, M3) ended up not needing the input traps: the only 6502 code
that reads `$BFF1`/`$BFF2` is `asm/entropytest.s`, which assembles the M8
entropy collector (`asm/entropy.inc`, D13) with `HARNESSKBD` so the input
traps stand in for the real `$C000`/`$C010` and `internal/entropy`'s tests
can play the human, choosing the emulated cycle at which each key arrives
— `cmd/uci` instead keeps one long-lived *Go*
process that pokes each position directly into a fresh `Machine` per
move and carries the aux-bank TT bytes forward between them (see
`internal/ucibridge`). Still planned: PC-trap callbacks, and a
symbol-aware trace using the ca65 listing/map files. Perft results come
out via COUT as ASCII (exit codes are 8-bit; counts are 32-bit).

## The IIe 128K memory model (`goapple2/iie`)

goapple2 proper is a ][+ emulator with a single flat 64K array; it had
none of the IIe auxiliary-memory machinery. The new `iie` package is a pure
memory model (no video, no cards) implementing:

- 64K main + 64K aux RAM
- RAMRD/RAMWRT (`$C002-$C005`): read/write banking for `$0200-$BFFF`
- ALTZP (`$C008/$C009`): banking for `$0000-$01FF` and Language Card RAM
- Language Card banking (`$C080-$C08F`), both banks, including the
  double-read write-enable (prewrite) behavior
- Status reads: `$C011-$C016`, `$C018`
- An `Unhandled` counter for accesses to anything it does not implement,
  so the harness warns when code strays outside the supported subset

Deliberately unimplemented in stage 1 (the engine keeps 80STORE off, which
on real hardware makes RAMRD/RAMWRT govern all of `$0200-$BFFF`):
80STORE/PAGE2/HIRES display-coupled banking, INTCXROM/SLOTC3ROM Cxxx ROM
mapping, keyboard. Stage 2 adds these, validated by the rest of a2audit,
and wires the model into the interactive goapple2 machine.

### Validation against a2audit

`iie/audit_test.go` assembles a2audit's test binary with the ACME assembler
pinned inside that repo (note: the pin is a hermit shim — the first-ever
run on a fresh machine fetches ACME over the network), loads it at `$6000`
with the ][+ ROM, runs the monitor init a real boot would perform, calls
test entry points directly (addresses parsed from ACME's `--symbollist`
output at test time, so they can't go stale), and checks the zero-page
result flags:

- `TestA2AuditLangcard`: **passing** — the data-driven Language Card
  suite, verified against real hardware, including WRTCOUNT quirks.
- `TestA2AuditAuxmem`: skipped pending stage 2 (exercises 80STORE/PAGE2/
  HIRES aliasing and Cxxx ROM in its later stages).

A subtlety this loop already caught: calling audit code on a cold machine
hangs in a BRK loop, because monitor output vectors through CSW (`$36/$37`)
and the BRK vector (`$3F0`) is uninitialized — the stub must run
SETKBD/SETVID/INIT/HOME first, like a real boot.

## Engine-specific test rigs (`internal/chesstest`; see docs/plan.md milestones)

- **Perft in the emulator**: `TestPerft` (`perft_test.go`) runs the engine's
  move generator to fixed depths from standard positions (startpos,
  Kiwipete, etc.) inside the harness and compares node counts against
  published values — the movegen correctness gate, passing since M1.
- **Search invariants**: `TestWACBaseline`/`TestWACDeepening` run a WAC
  subset with expected best moves at fixed depth and under iterative
  deepening; `TestLegalityTorture` (`match_test.go`) validates every move
  the engine emits in self-play against an independent Go chess library.
  All run via `go test` — there is no CI yet (see Known infrastructure
  gaps below).
- **asm↔mirror parity** (the faithfulness gates): `TestSearchMirrorParity`
  (root positions, masks 0x00/0x07/0x08/0x1f) and `TestCheckExtMirrorParity`
  (FT_CKEXT on/off) compare the asm and `internal/mirror` at fixed depth on
  best move, score and make count. `TestFullGameMirrorParity`
  (`fullgame_parity_test.go`) is the STRONG version: it plays COMPLETE GAMES
  with both engines configured identically (shipped 0x5F and plain 0x1F,
  dither off, fresh TT/killers each ply) from the 40 `tools/openings-pool.epd`
  starts plus 31 tactical/endgame/near-mate starts, and requires the same
  move, the same score AND the same tree (search-node / make / eval /
  makenull counts) at every ply. Env knobs `PARITY_DEPTH` / `PARITY_PLIES` /
  `PARITY_STARTS` / `PARITY_CFG` scale it; the default is sized for
  `make test`. Rationale: the fixed-FEN gates are what let the tt.s unsigned
  mate-zone bug survive for weeks (it was modelled into the mirror as a
  "quirk" flag instead of being investigated). A game walk generates
  positions nobody chose, in score ranges (mates, 50-move, insufficient
  material) a FEN list never reaches. NEVER add a flag that teaches the
  mirror to reproduce an asm oddity — find out which side is right.
- **UCI bridge** (`cmd/uci`, `internal/ucibridge`): a long-lived Go
  process that speaks UCI to cutechess-cli, tracks position on the Go
  side, and pokes each move's position directly into a fresh `Machine`
  (carrying the aux-bank TT forward between moves) rather than using the
  D12 input traps (see D12's amendment). Gauntlets follow the four-part
  protocol in plan.md/D11: paired openings always; opponents bracketing
  our level at their rating-valid conditions; node-odds ladder for the
  headline. cutechess must be run with generous wall margins
  (`timemargin`) — the engine's real clock is its internal cycle budget,
  and its wall usage (~0.2-0.4s per 30-60s emulated move) must never be
  what decides a game.
- **Determinism**: an engine-side property, not a harness feature — the
  engine enforces a fixed *emulated-cycle* budget internally (cycles,
  not nodes: cycle budgets are deterministic AND sensitive to per-node
  cost, which is what half our features change). Same position + same
  budget = same move, regardless of host speed.

## Speed / capacity notes

- Measured harness speed: ~100-170x real time depending on workload
  (M-series Mac, informal; tiny runs are dominated by process startup
  and measure ~1-2x). A benchmark rig lands with M1 so this number is
  monitored, not asserted.
- One 40-move game at 30 s/move ≈ 20 emulated minutes ≈ ~10 wall
  seconds of engine time; opponents at CCRL-valid controls dominate
  gauntlet wall time (~4 min/game at 40/4) — ~35 core-hours per
  500-game gauntlet, parallelizable across cores.

## Known infrastructure gaps (from adversarial review)

- **No CI anywhere** (chess6502, goapple2, go6502, a2audit) — the
  hardware-truth gates currently run only on one machine. GitHub Actions
  for `make test` is the obvious first step once repos are pushed.
- a2audit assembly writes `audit.o` into the a2audit checkout (gitignored
  there, but still a cross-repo side effect of `go test`).
