# chess6502: a maximum-strength chess engine for the Apple IIe (128K)

## Goal

Demonstrate how far chess engine theory has advanced since the 1980s by
building the strongest chess engine we can that runs on a stock Apple IIe
with 128K, at "reasonable" per-move time (headline setting: 30-60 s/move;
also playable at faster settings). Success is measured, not vibed:

Adversarial review forced an important honesty fix here: Elo numbers only
mean something relative to a pool and its conditions, so the targets are
split by scale rather than quoted as one number.

1. **Beat the 80s** (the thesis headline). Convincingly outplay the
   strongest period Apple II software (Sargon III class, ~1550-1600
   measured at 1 MHz) in direct emulated matches at equal 1 MHz —
   pre-registered criterion: >=65% over >=100 paired-opening games. By
   extension, beat anything that ever ran on a 1 MHz 6502.
2. **The node-odds crossover** (the most communicable number): how many
   nodes does micro-Max (CCRL ~1891 at full speed) need per move to hold
   50% against us at our ~15K? "Modern-technique assembly at 1 MHz gives
   up only Nx nodes to a modern minimal engine" quantifies the theory
   advance without scale games.
3. **Absolute CCRL-anchored rating**, measured against full-speed
   opponents at their rating-valid conditions: honest prediction
   **~1250-1550** (we sit ~12 node-doublings below micro-Max's CCRL
   conditions; Thompson's Belle data says doublings are worth MORE at
   low depth, ~125 Elo, partially offset by our technique/eval edge).
   On the *period/SSDF-style scale* — against opponents at comparable
   node counts — **1700-1900 is the defensible claim** (Mephisto MM V:
   500-600 NPS -> 1883 current-SSDF, so ~1880-at-500-NPS is
   demonstrated). Both get reported, clearly labeled; there is no single
   "our Elo".
4. **Runs on the metal.** Documented-NMOS-6502-clean code with a memory
   map that fits a real IIe; harness-only conveniences isolated behind
   an I/O vector table (decision D8, planned module — a2run's traps are
   the only I/O today).

Graphics are out of scope for now, but hires page 1 stays reserved (D7).

## Hardware model and constraints

- CPU: NMOS 6502 at 1.0205 MHz effective (D2: no 65C02 opcodes).
- RAM: 64K main + 64K aux (IIe Extended 80-Column card semantics),
  Language Card banking for $D000-$FFFF in both banks.
- No interrupts (D9): go6502 doesn't deliver them, and polling suffices.
- Aux access rules per D4: RAMRD/RAMWRT with 80STORE off; aux-touching
  routines execute from LC RAM or $0000-$01FF.
- I/O: harness traps now; keyboard/screen or serial later (D8).

### Memory budget (working draft; the plan's most-attacked table)

Main bank:

| Region | Size | Use |
|---|---|---|
| $0000-$00FF | 256 | Zero page: board pointers, search locals, accumulators (hot state) |
| $0100-$01FF | 256 | 6502 stack: **return addresses only** (~2 bytes/ply + transient JSR nesting ≈ 120 bytes at MAXPLY; per-ply locals live in software frames below) |
| $0200-$07FF | 1.5K | Input buffer, text screen (if used), scratch. (M8 note: screen holes $478-$47F etc. are card scratchpad on real hardware — no engine state there) |
| $0800-$1FFF | 6K | Contiguous move stack with per-ply base pointers (see Search), per-ply software frames (alpha/beta/best/killers indexed by ply), game history + repetition list |
| $2000-$3FFF | 8K | **Reserved: hires page 1** (D7; the load image transits it harmlessly at load time) |
| $4000-$BEFF | 31.7K | Engine code + hot tables (0x88 attack tables, PSQT, Zobrist keys) |
| $BF00-$BFFF | 256 | **Reserved: harness I/O traps** ($BFF0/$BFFF), kept clear in BOTH banks (D8) |
| LC $D000-$FFFF | 12K+4K | Aux-access primitives, book probe, cold code (init, UI), spillover tables. Note: $D000 bank1/bank2 are mutually exclusive (a $C08x dance to switch); while LC RAM is read-enabled, monitor ROM routines are unmapped — M8 UI drives $C000/$C010 and the text page directly |

Aux bank:

| Region | Size | Use |
|---|---|---|
| $0200-$BEFF | 47.2K | Transposition table (32K, power-of-2 — D6), opening book (2-8K), optional KPK data (~12K fits) |
| $BF00-$BFFF | 256 | Reserved (trap-address mirror; D8) |
| aux LC + aux ZP | 16.5K | Unused. Stage-2 ideas (pawn hash, deeper book) are parked behind D4's "ALTZP never touched" invariant |

Hot-table math: PSQT tapered (6 piece types x 128 squares x mg-lo/mg-hi/
eg-lo/eg-hi) = 3K; Zobrist keys (12 x 128 x 4 = 6K, plus side-to-move,
castling, ep keys); attack/direction tables ~1.5K; all page-aligned in
main. Estimated engine code size: 8-16K (Sargon was ~14K including eval).
Stack discipline: **MAXPLY = 32-48 enforced at search entry** (return
static eval when hit), extension budget capped (extended depth <= 2x root
depth), debug builds assert stack headroom — a wrapped 6502 stack corrupts
silently, and check-extension chains would otherwise reach it.

## Architecture

### Board representation (D5)

0x88 board (128 bytes) + 16-entry piece lists per side. Off-board test is
`AND #$88`. Difference-indexed attack tables give is-square-attacked
without loops per direction. Movegen follows H.G. Muller's mailbox-trials
pattern: iterate piece lists; generate captures first, staged by victim
value so MVV ordering falls out of generation order; non-captures append
to the back of the per-ply move list. Pseudo-legal generation; king
capture returns +INF (legality handled lazily); null-move result doubles
as a check detector where convenient.

### Search

Negamax alpha-beta with, in order of implementation (each SPRT-gated per
the testing plan). **MAXPLY guard and extension budget from day one** (see
memory budget), and one contiguous move stack with per-ply base pointers —
not fixed per-ply slots, whose worst case (218 legal moves exists; 50-80
pseudo-legal is routine — Kiwipete has 48 legal at the root) would demand
~256 bytes/ply while the sum along any real search path stays far smaller.
The generator carries an overflow guard (drop + flag, slightly unsound in
freak positions rather than corrupt).

1. **Quiescence**: captures **plus queen promotions**, MVV/LVA, stand
   pat, delta pruning (**implemented, M4**: a 200cp margin against
   alpha-minus-standpat, always on above phase 6, **disabled below
   phase 6** — in late endgames delta pruning prunes the pawn captures
   that decide the game; promotions are exempt from the prune
   entirely, not given a separate margin). When in check at a QS node:
   no stand-pat; search evasions (cheap with the attack tables, and
   removes a class of horizon mates micro-Max accepts).
2. **Iterative deepening** with TT/previous-best move first. Move
   ordering is five move-loop passes over the generated list (search.s):
   TT move, heavy captures (victim >= rook, MVV-tiered) and promotions,
   light captures, killers, remaining quiets in generation order.
3. **Transposition table** (D6): 32-bit Zobrist, 4096x8-byte entries in
   aux, 20 verify bits. The stored move matters more than the score at
   our depths — and is therefore never applied blindly: the TT move
   (and killer moves) are only searched if they **match an entry in the
   freshly generated move list** for that node (pass 0 above), so a
   hash collision that names a from/to pair absent from the real move
   list simply matches nothing. Mate scores stored/probed with the
   standard ±ply adjustment.
4. **Null move**, R=2, with micro-Max-style material-count zugzwang guard.
5. **Killer moves** (2 per ply, validated like TT moves).
6. **Futility + reverse futility** at depth <= 2 (saves make/eval cycles —
   extra valuable at 1 MHz).
7. **Check extension** (bounded by the extension budget); mate scores as
   MATE-ply. **Deferred to the rest of M4** — not yet implemented; null
   move, killers, and futility/RFP landed first per the one-feature-
   at-a-time SPRT-gate discipline above.
8. **Repetition & draws** (semantics fixed by review — these interact
   with the TT): twofold repetition checked against game history AND the
   current search path, **before the TT probe** (a TT cutoff must never
   mask a draw); repetition/50-move draw scores are not stored in the TT
   (or stored bound-only); truncated-hash matches verified against the
   full 32-bit key. Insufficient-material (KK/KBK/KNK = draw) lands at
   M2, not M6 — without it, won-position shuffle-draws pollute every
   early gauntlet.
9. Later, measured (M5): **PVS** (zero-window scouts after the first
   move; measured +55 in Rustic, and it defines the PV/non-PV node
   distinction LMR wants anyway), conservative LMR (reduce 1, move > 4,
   depth >= 3, non-capture, not in check, non-PV), IID.
10. **Skipped deliberately** (evidence says poor value at our depths/NPS):
    history heuristic, checks in qsearch, aspiration windows, SEE.
    Underpromotions are generated in the main search (perft needs them),
    queen-only in QS.

Scores are 16-bit centipawns, symmetric INF (~±32000), negamax throughout.

### Evaluation

Incremental tapered PSQT (PeSTO tables, public, Texel-tuned — gave TSCP
+200 Elo) + tempo. Three 16-bit accumulators (mg, eg, phase) updated in
make/unmake; taper once per evaluated node.

The taper itself needs designing on a multiply-less CPU (review finding —
naively it's two 16x5-bit multiplies plus a divide by 24, ~300-450 cycles
per eval): (a) rescale phase to /32 at table-build time so the divide
becomes shifts; (b) exploit convexity — min(mg,eg) <= taper <= max(mg,eg),
so most stand-pat/futility decisions resolve with two 16-bit compares
(~40 cycles) and the multiply runs only when the window straddles the
bounds. Both land with the first eval implementation.

Later, measured: passed pawns, doubled/isolated pawns with a tiny pawn
hash. Mobility: skipped (doubles eval cost; poor Elo/cycle at 1 MHz).

### Time management (D9, corrected by review)

Emulator (through M7): fixed emulated-cycle budgets — deterministic AND
time-faithful (node budgets are blind to per-node cost changes; see
Testing). Hardware (M8): poll $C019 every 1-2 nodes (~10-13 cycles,
~0.5% — the original every-256-nodes scheme mathematically cannot count
frames and was scrapped); IIe sense: bit 7 LOW during VBL. Policy: no
new iteration past ~50% budget; on hard abort at 2x, play the current
iteration's best move if a root move completed and improved on the
previous iteration, else the previous iteration's move.

### Protocol and bridging

`cmd/uci` (M3) wraps the emulated engine as a UCI engine for cutechess-cli
and (eventually) lichess-bot — the MessChess/retro-Sargon pattern, but
in-process Go. As built, the position doesn't travel to the engine as
text at all: the long-lived Go process parses UCI/FEN on the Go side,
pokes the position directly into a fresh `Machine`'s memory per move,
and carries the aux-bank TT forward between moves; only the move out is
a harness trap (COUT, ASCII algebraic). The engine-side input traps
($BFF1/$BFF2, D12) exist in the harness but aren't read by any 6502 code
yet; a real on-engine text protocol behind the I/O vector table (D8) is
still future work, not what powers today's UCI bridge.

## Performance model (to be validated at M1-M3)

Anchored to documented data points (docs/research/prior-art.md), with
per-primitive cycle budgets added by adversarial review so the M1 gate is
falsifiable:

- Per-node budget (quiet node): make <= 400 cycles (capture <= 550) —
  incremental PSQT is ~110-125 and Zobrist ~160 of that; unmake <= 200
  (restore from undo frame); movegen <= 60/move amortized; eval taper
  <= 400 worst case, ~40 when the convexity shortcut resolves it; TT
  probe <= 250 (aux round trip; not probed in QS by default); search
  bookkeeping 150-300. Total ~1,500-2,400 cycles/node.
- => NPS estimate: **~420-680**, consistent with the external anchors:
  Colossus Chess documented 520 NPS on the ~1 MHz C64 (single-source
  manual figure, unknown node-counting convention — treat as order-of-
  magnitude); Mephisto MM V did 500-600 NPS at 5 MHz with heavier eval.
  Cheap lever if needed: skip Zobrist updates in QS makes (no QS probe)
  for ~10% NPS.
- At 30 s/move and ~500 NPS: ~15K nodes/move. With effective branching
  factor ~2.5-3.5 (full ordering + null move): **depth 6-8 nominal plus
  quiescence** in the middlegame, deeper in endings via TT.
- Strength: see Goal section for the scale-split targets (~1250-1550
  CCRL vs full-speed opponents; 1700-1900 on the period/node-matched
  scale; node-odds crossover as the headline). Period 1 MHz software
  measured ~1500-1600 with some modern-adjacent features (Colossus
  already had ID+TT+killers+null move+futility in 1984; Sargon III had
  quiescence+TT); our edge is tuned tapered PSQT (+200 on TSCP),
  all-captures QS, a 16x larger TT, futility/RFP, and SPRT-tested
  tuning throughout.

## Milestones

Each milestone has an exit gate. Nothing merges past M3 without SPRT
evidence at **fixed emulated-cycle budgets** (not node budgets — half our
features are Elo/second features whose whole point is cycle savings;
fixed-node testing is structurally blind to them and would approve
regressions. Cycles are just as deterministic and are time-faithful to
the target machine).

- **M0 — Dev loop (done, 2026-07-17).** ca65 -> a2run round trip; iie
  memory model passing a2audit Language Card suite; go6502 modernized;
  ~100-170x realtime (workload-dependent; benchmark rig lands with M1).
- **M1 — Board + movegen + perft (done, 2026-07-17).** 0x88 movegen complete incl. castling,
  e.p., underpromotion; **perft(1-5) matches published values for
  startpos, Kiwipete, and 4-6 tricky positions, running inside a2run**
  (counts out via COUT as ASCII — exit codes are 8-bit); make/unmake
  with incremental Zobrist + PSQT accumulators; perft via make/unmake
  equals copy-make reference. Representation spike prices 0x88+lists vs
  10x12 including capture/unmake/promotion list bookkeeping (D5). Gate:
  perft exact; measured cycles within 2x of the published per-primitive
  budgets (see Performance model). Also lands: multi-segment ld65
  config + bank-copy loader (LC write-enable dance + RAMWRT aux copies)
  proven by a hello-world touching main+LC+aux; a2run core extracted to
  an importable package (D12) so perft rigs run in-process.
- **M2 — It plays chess (done, 2026-07-17).** Fixed-depth negamax + QS + stand-pat +
  MVV/LVA + material/PSQT eval; MAXPLY + extension budget enforced;
  repetition + 50-move + insufficient-material handling per Search #8.
  Gate: mate-in-1/2/3 suite exact; >=97.5% vs random mover over 200
  games (draws allowed, losses not); 100-game legality torture vs a Go
  referee (every emitted move validated by an independent chess
  library); a WAC-subset baseline score recorded at fixed cycles.
- **M3 — Real engine (done, 2026-07-18).** Iterative deepening, TT in aux, cycle-budget
  time management, UCI bridge (D12; long-lived Go-side session per
  cmd/uci — see Protocol and bridging for how it actually reached that),
  paired-openings suite wired into cutechess (deterministic engines
  from startpos replay one game — openings are what make N games carry
  N games of information), generous wall-clock margins so the emulated
  engine never forfeits. Gate: measured rating exists from the
  bracketing-pool gauntlet (whatever the number is); determinism
  verified (same position + same cycle budget = same move).
- **M4 — Modern pruning (in progress, started 2026-07-18; see
  docs/results.md).** Null move, killers (validated), futility/RFP,
  check extension — **each individually SPRT-gated at fixed cycles**
  (no aggregate Elo bar: a milestone must not fail because one feature
  overperformed and another was neutral); updated gauntlet number. Null
  move, killers, and futility/RFP have landed behind feature bits; check
  extension has not (see Search #7).
- **M5 — Eval and ordering polish.** PeSTO tuning pass, passed/doubled/
  isolated pawns (pawn hash), PVS, LMR, IID trials. Gate: SPRT wins only.
- **M6 — Book + endgame touch-ups.** 2-8K compiled opening book in aux;
  rule-based KPK recognizer. Gate: full four-part measurement (D11):
  bracketing gauntlet + node-odds ladder — the headline numbers.
- **M7 — Period showdown.** Automated matches vs Sargon III (~1550-1600
  measured at 1 MHz) and friends under emulation, equal emulated time,
  paired openings. Gate: pre-registered criterion (>=65% over >=100
  paired games) met + writeup.
- **M8 — On the metal.** Keyboard/text UI build (drives $C000/$C010 and
  the text page directly — monitor ROM is unmapped while LC RAM is
  enabled); VBL timing path (D9) validated against the harness's
  cycle-fed $C019 first, then real or fully-emulated IIe (stage-2
  goapple2); optional hires board (D7); optional Super Serial bridge.
  The UI must read every key through `entkey` (asm/entropy.inc) and call
  `entseed` before each search: that keystroke-timing counter is the only
  entropy source the machine has, and the eval dither needs it (D13).
  Pondering stays out of scope even here (it would invalidate the
  calibration story).

## Testing strategy (see docs/testing.md for mechanics)

- Movegen: emulated perft vs published values + Go reference library.
- Search: fixed-cycle tactical suite (WAC subset) as regression tests.
- Strength — the four-part protocol (D11, reshaped by review):
  1. SPRT self-play feature gates at fixed emulated-cycle budgets,
     paired openings with color reversal.
  2. Absolute gauntlet: us at our real control (cycle budget ≈ 0.2-0.4s
     wall at ~170x) vs 4-6 CCRL engines **bracketing our level within
     ±200** at their rating-valid conditions (~500-1000 games; error
     bars include anchor uncertainty).
  3. Node-odds ladder vs node-limited micro-Max/TSCP -> the crossover
     factor, our headline number.
  4. Period matches at equal emulated 1 MHz (M7).
- Correctness on the metal: the same binary bit-for-bit runs in a2run and
  (at M8) on hardware; a2audit keeps the memory model honest; go6502's
  gate-level lockstep keeps the CPU model honest.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| NPS lands well below estimate (e.g. <300) | M1 gate vs published per-primitive cycle budgets catches it early; fall back: cheaper eval increments, no QS Zobrist, reduced QS |
| Stack overflow / unbounded extensions corrupt silently | MAXPLY + extension budget from day one; return-addresses-only hardware stack; debug stack-headroom assert (review finding — would have failed silently and late) |
| Taper arithmetic eats the eval budget | /32 phase rescale + convexity shortcut (designed in, not retrofitted) |
| Aux TT probe overhead swamps benefit | Measure at M3 with TT on/off self-play; entries/probe layout tunable; worst case: shrink TT into main LC RAM (~16 Elo penalty by Muller's rule) |
| Corrupted TT/killer move desyncs board state | Mandatory list-match validation against the node's generated move list before use (D6) |
| 16-bit score handling bloats hot paths | Split-table PSQT (lo/hi bytes) keeps adds 8-bit with carry; only compare/negate at 16-bit |
| Search bug wrecks strength invisibly | Determinism + perft + fixed-cycle regression suite; every feature lands with SPRT evidence; legality torture rig vs Go referee |
| Assembler/linker friction (banked segments) | D1: ld65 segment configs; loader copies LC/aux segments at init; hello-world for each bank wiring lands in M1; Makefile pins/warns on toolchain version |
| Emulator infidelity re: hardware | a2audit (hardware-verified) gates the memory model; gate-level lockstep gates the CPU; cycle-fed $C019 makes the timing path testable pre-metal; M8 runs on real hardware |
| Elo numbers misread (scale conflation) | Scale-split targets (Goal section); bracketing opponent pool; node-odds ladder as the primary headline; error bars include anchor uncertainty |

## Open questions

(Original list pruned by the adversarial review: per-ply storage is now
specified — contiguous move stack + software frames; QS probing defaults
off pending M3-M4 measurement; both are recorded in D5/D6.)

1. Is 0x88+piece-lists really the cycle winner on 6502 vs 10x12 with
   pre-scaled offsets? (M1 spike, both arms priced including list
   bookkeeping — see D5.)
2. Does the engine need contempt vs weaker 80s opponents to convert +2
   positions instead of shuffling? (M7 concern; insufficient-material
   scoring at M2 removes the worst cases.)
3. 48K ][+ compatibility build (drop aux TT, shrink book): worth it?
4. Piece-list capture bookkeeping: board-byte index encoding vs
   square-to-index map vs tombstones (D5 sub-decisions; M1 spike).
