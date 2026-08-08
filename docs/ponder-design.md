# Pondering on the Apple IIe — design

Status: **DESIGN ONLY (2026-08-07).** No engine code, no `asm/` edits. This
document specifies how to make the shipped disk think on the opponent's clock
and keep the tree across a "ponder hit". It is written against the real code:
`asm/m8.s` (the LC-resident UI/driver), `asm/engine.s` (the reference ID
driver), `asm/search.s` (the abort path), `asm/tt.s` (the transposition
table), `asm/entropy.inc` (the keystroke entropy collector), and the Go
reference model already in `internal/ucibridge` (`ponder`, `ponderPrediction`,
`PonderMove`) plus its gate suite `internal/ucibridge/ponder_test.go`.

All symbol names below are the actual ones. Cycle and byte figures are
*derived* (instruction-level or by analogy to a measured neighbour) unless
they cite a measured result in `docs/results.md`.

---

## 0. Summary and recommendation

| question | answer |
|---|---|
| Can the search be made re-entrant cheaply? | **Yes.** Reuse the existing `ABORT` flag and the once-per-128-nodes `ccsite` poll. A keyboard poll joins at exactly the site the soft clock already patches. |
| Does normal (own-move) search change? | **No — byte-identical.** The keyboard poll is a *runtime operand patch* of `ccsite`, live only while pondering, exactly as `FT2_SOFTCLK` patches it. `engine.bin` is untouched; `TestMicroAB` is green by construction. |
| Where does the ponder code live? | **Language Card `$E000+`**, with the rest of the UI. **Zero** bytes of the main-image headroom. |
| How big? | **~230 B of LC code + ~6 B of RAM state.** LC had ~1,700 B free (ui-design §8). |
| Does "hit vs miss" need branching logic? | **No — it collapses.** The TT is self-verifying (entries carry `HASH1..3`); a hit reuses warm entries automatically and a miss ignores stale ones. Neither path needs special code beyond "don't wipe the TT". |
| What breaks? | Only the ponder-abort recovery interaction (see §4.3) and the measurement story (see §10). Both are contained. |
| **Recommendation** | **Build the reduced version (§11.A): Scheme A ponder with shallow-search prediction, full-budget real search on the warm TT, no ponder-time banking.** It is ~230 LC bytes, needs no new engine export, and every risky interaction is caught by an existing or a one-file-new gate. Defer the time-banking refinement (§7.3) and the free-TT prediction (§5.3) to a measured second pass. |

The single most important structural finding is in **§6**: because the TT
verifies every entry against the full position hash, the classic
hit/miss/discard state machine that pondering usually requires **does not need
to exist on device**. A ponder hit is "the warm entries happen to match"; a
miss is "they don't, and they are ignored." Both flow through the unchanged
`m8engine` path.

---

## 1. What the disk does today, and the opportunity

The shipped main loop (`asm/m8.s`, `mloop`) is strictly turn-serial:

```
mloop:  jsr uisync              ; recompute game-over / side-to-move state
        jsr uipaint             ; full 40x24 repaint (23,659 cyc, measured)
        lda UIRESULT
        bne mcmd                ; game over -> commands only
        jsr uimyturn
        bcc mengine             ; engine to move
mcmd:   jsr uiread              ; <-- BLOCKS here on the human's clock
        jsr uidispatch
        jmp mloop
mengine:
        jsr m8engine            ; the search (uidrive -> ENG_iterate)
        jmp mloop
```

`uiread` (m8.s:1498) loops on `jsr entkey`, and `entkey` (entropy.inc:55)
**spins** — `inc ENTCNT` / poll `$C000` / `bpl` — until a key is available. So
between the engine playing its move and the human pressing the first key of
their reply, the machine does **nothing but spin a 16-cycle entropy loop**.
That idle interval is the ponder budget, and it is entirely free (it is the
opponent's clock).

`docs/plan.md` M8 says "Pondering stays out of scope even here (it would
invalidate the calibration story)." That statement is about *measurement*
(§10), not feasibility. The mechanism below leaves the calibration intact for
own-move time; it only adds work on the opponent's clock, which the soft-clock
calibration never described.

---

## 2. Architecture: Scheme A ("ponder the predicted position")

The standard definition of pondering, and the one the Go reference and the
`sargon-symmatch` harness already implement:

1. Engine plays its move **M** (this already happens: `m8engine` → `uiapply`).
   The board now holds root+M, human to move.
2. **Predict the human's reply P** (§5).
3. **Make P on the board** (`ENG_make`) and search the resulting position
   root+M+P as deeply as time allows, warming the aux TT (§3). This search runs
   *until the human presses a key* (§4) or a safety cap fires.
4. **Unmake P** (`ENG_unmake`) to restore the board to root+M, human to move.
5. The human's actual reply **S** arrives with the interrupting keypress.
   `uiread` reads the whole line; `uidispatch` plays S (or handles a command).
6. If **S == P** (a *hit*), the TT is already warm for exactly the position now
   on the board, and the ordinary `m8engine` real search flies deeper for the
   same budget. If **S != P** (a *miss*), the warm entries are for a different
   position and are silently ignored; the real search is identical to today's.

Steps 2–4 are new; they insert between `uipaint`/`uimyturn` and `uiread`:

```
mloop:  jsr uisync
        jsr uipaint
        lda UIRESULT
        bne mcmd
        jsr uimyturn
        bcc mengine
        jsr m8ponder            ; NEW: predict P, make P, search until keypress,
                                ;      unmake P. Leaves the key buffered.
mcmd:   jsr uiread              ; reads the buffered key + the rest of the line
        jsr uidispatch
        jmp mloop
```

`m8ponder` is a no-op (returns immediately) when there is no legal reply to
predict (mate/stalemate — but then `UIRESULT` already routed us to `mcmd`), or
when pondering is disabled.

A prediction-free **Scheme B** ("just keep searching root+M, the opponent's own
move") is discussed in §5.4 as a reduced fallback; Scheme A is primary because
it concentrates the free time on the one line a hit will actually reach, which
is what the measured head-start (`TestPonderHeadStartDepth`) rewards.

---

## 3. The search engine reused unchanged

`m8ponder`'s search is the **existing UI ID driver** `uidrive` (m8.s:1356),
with two differences, both external to it:

- it runs with `ccsite` patched to a keyboard-polling variant (§4);
- it must **not** apply or report a move — it only warms the TT.

`uidrive` already does everything else correctly for an arbitrary root: it
zeroes `PSP0/1`, `ABORT`, `NODECNT`, `PLY`, `HVALID`; calls `ENG_evalinit`
(which rebuilds `HASH0..3`, `PHASE` and the eval accumulators from the current
board); primes the soft clock; sets `MAXCAP`/`ABORTL`; and runs the
`ENG_iterate` loop painting the thinking line between iterations. Pointed at
root+M+P it warms the TT for that position exactly the way a real search would.

The cleanest implementation is a thin wrapper: a `PONDERING` flag that
(a) selects the keyboard-poll `ccsite` target and (b) tells the driver's tail
to `rts` after the loop instead of falling into apply/report. `uidrive`'s
abort-recovery block (`PREVFROM` restore, m8.s:1425) is **skipped when
pondering** — see §4.3.

---

## 4. Interruptible search (requirement 1)

### 4.1 The mechanism reused

`search.s` polls once per 128 nodes:

```
search: dec NODECNT
        bne :+
ccsite: jsr checkclock          ; operand patched to checkclocks by FT2_SOFTCLK
:       lda ABORT
        beq :+
        lda #0                  ; aborting: unwind with a dummy SCORE, rts
        sta SCORE
        sta SCORE+1
        rts
```

Once `ABORT` is set, **every** node on the stack takes this early return and
the whole recursion unwinds to `iterate`'s caller with a dummy score. This is
the identical path the soft/hard clock uses (`checkclock` sets `ABORT` when
`CLOCK_TRAP >= ABORTL`). A keypress is just a second reason to set `ABORT`.

### 4.2 Where the keypress joins — and why normal search stays byte-identical

`FT2_SOFTCLK` shipped a pattern we reuse verbatim: `engine.s`/`uidrive` patch
the **operand** of the single `ccsite: jsr checkclock` at runtime, redirecting
it to `checkclocks` (the accumulating clock). The disk image still contains the
default `checkclock` address; the patch happens in RAM. That is why an OFF run
is "not merely equivalent but the identical instruction stream"
(search.s:105-107).

Pondering adds a **third** target, LC-resident (so it is *not* engine code and
`engine.bin` is untouched):

```
; pkclk  — LC-resident, installed as ccsite's target only while PONDERING.
pkclk:  jsr ENG_checkclocks     ; do the soft-clock accumulate + hard-abort backstop
        lda ENTKSTAT            ; poll the keyboard (does NOT clear the strobe)
        bpl pkdone              ; no key: return, search continues
        lda NODECNT             ; interrupted: fold the interruption timing into
        jsr entfold             ;  entropy (entropy.inc's documented ponder hook)
        lda #1
        sta ABORT               ; -> search unwinds through the existing path
        sta PONDERKEY           ; -> tell m8ponder "a key ended this, discard"
pkdone: rts
```

`m8ponder` sets `ENG_ccsite+1/2` to `pkclk` before the driver runs and restores
the soft-clock/`checkclock` target after. The keyboard poll therefore exists
**only on the opponent's clock**; the own-move search that every SPRT and
gauntlet measures executes the unchanged instruction stream. `TestMicroAB` and
every tree-identity gate are green by construction — the same argument that
made `FT2_SOFTCLK` free.

**Why `jsr ENG_checkclocks` rather than a bare poll.** The ponder search needs
a safety cap so it cannot run forever (an opponent who walks away) or overflow
its estimate. Delegating to `ENG_checkclocks` gives the ponder search the same
`ABORTL` backstop and soft-clock accounting the real search has, for free; the
keyboard poll is appended after it. For a fixed-depth level (`BUDGET = 0`,
`checkclock` no-ops the abort test) the depth cap `MAXCAP` is the terminator
and the keypress is the normal one; a synthetic large `BUDGET` for the ponder
search (§7) is the tidy way to give even fixed-depth levels a wall.

### 4.3 Interaction with round-6's abort recovery — the one real trap

`uidrive` (and `engine.s idloop`) treat an abort as "the clock ran out": they
recover the **last completed iteration's** move from `PREVFROM/PREVTO/
PREVFLAGS/PREVSC0/1` (m8.s:1425-1440), and `checkclock` guards `CURDEPTH < 2`
so iteration 1 always completes and "abort can never leave a garbage move"
(search.s:168-170). That recovery is exactly right for a real search and
exactly **wrong** for a ponder search, whose "best move" must be thrown away —
the human has not moved yet, and P was only a guess.

Design rule: **when `PONDERKEY` is set, `m8ponder`'s driver skips the
`PREV*` recovery and the report/apply tail entirely, and just `rts`es.** It
warmed the TT; that is its whole product. The abort-recovery code is not on the
ponder path at all. (`internal/ucibridge`'s `ponder()` embodies this: it runs
`runEngine` and keeps only the TT — the returned move is stashed in
`ponderBest` for a UCI "stop" reply and is otherwise discarded.)

This is the interaction the brief flags: round 6's two abort-path fixes (the
`CURDEPTH<2` freeze guard, and preferring the completed iteration's move over
an aborted partial, docs/results.md:8780) both live in the *own-move* recovery
logic. Pondering must **not** reuse that recovery; if it did, a ponder abort
would try to "play" P's refutation. Keeping the ponder driver's tail separate
(one `bit PONDERKEY / bne` at the loop exit) is the whole fix, and it is ~4
bytes.

---

## 5. Ponder-move prediction (requirement 2)

### 5.1 Does the engine keep a principal variation? Where?

There is **no explicit stored PV array.** The engine keeps only the root best
move (`BESTFROM/BESTTO/BESTFLAGS`, updated by `iterate`) and, implicitly, the
*continuation* inside the transposition table: after a search, the TT entry for
root+M holds the best reply from that node, its child holds the reply-to-that,
and so on. The "PV 2nd move" is therefore recoverable as a TT walk, which is
exactly what the Go reference reads (`PonderMove`, ucibridge.go:1021).

So there are two prediction sources, matching `ponderPrediction`
(ucibridge.go:1067):

### 5.2 Source (b) — a shallow search (the device primary for v1)

After M is on the board (human to move), run a **short** search of the current
position and read its `BESTFROM/BESTTO` — that is the opponent's predicted best
reply P. Concretely: set a small budget (or a low `MAXCAP`, e.g. depth 2–3),
`jsr uidrive`-equivalent once, capture `BESTFROM/BESTTO` into `PPFROM/PPTO`.

This reuses the entire existing driver and needs **no new engine export**. It
costs a few hundred ms of the free opponent time (the Go reference pins it at
`ponderProbeMs = 300`, ucibridge.go:1008), and — bonus — that probe *itself*
warms the TT for root+M, so it is never wasted. This is the recommended v1
predictor.

### 5.3 Source (a) — the free TT move (a later optimization)

`PonderMove` reads P straight out of the TT for root+M with zero search:
compute the position hash, index `(HASH1 & $0F)<<8 | HASH0`, read the 8-byte
entry at `TTBASE + index*8` (`TTBASE = $4000`), verify `+0..2 == HASH1..3` and
a non-zero bound in `+7`, and take `+3/+4` as from/to (tt.s:9-11). On device
that is the `TTADDR` macro + `ttprobe` (tt.s:20,39) reading `TTENTRY+3/+4`.

The catch: **`ttprobe` is not currently exported** to the UI —
`asm/engsyms.inc` lists `ENG_evalinit`, `ENG_iterate`, `ENG_make`, … but no
`ENG_ttprobe`. `ttprobe` *has* a label (`asm/engine.lbl`: `.ttprobe = $4E4A`),
so exporting it is a one-line regeneration of `engsyms.inc` (via
`cmd/genengsyms`) with **zero `engine.bin` change**. It is left out of v1 only
to keep the first cut dependency-free; add it when the ~300 ms probe cost is
worth reclaiming. When present, the predictor is `ponderPrediction`'s exact
shape: try source (a); on empty/mismatch/`NOSQ`/illegal, fall back to (b).

### 5.4 Legality and the prediction-free Scheme B

Whatever the source, **validate P against the generator before making it**,
with the UI's own `uifind` (m8.s:942) — the same validator the engine's own
move already goes through (m8.s:1121). An illegal/absent P means skip
pondering this turn.

Scheme B (reduced): skip prediction entirely and ponder root+M (the opponent's
own move). The TT fills for *every* reply, so a hit on any P leaves that
subtree partially warm — robust to prediction error but a smaller head start
per hit. Scheme B needs neither a predictor nor `ENG_make`/`ENG_unmake` glue
(~60 B saved) but gives up most of the measured head-start. Offered as the
floor; Scheme A is the recommendation.

---

## 6. Hit vs miss — the state machine that isn't (requirement 3)

The usual ponder engine tracks "am I in a hit or a miss" and resets accordingly.
**On this device that branch collapses**, because the TT verifies every probe:

- **Hit (S == P).** After the human plays P, the board is root+M+P — the exact
  position the ponder search rooted at. The ordinary `m8engine` real search
  re-roots there, `ENG_evalinit` rebuilds the hash, and every `ttprobe` finds
  the warm, deep entries the ponder left. The real search reaches a greater
  depth for the same budget. **No hit-specific code:** the only requirement is
  that the TT is not wiped between ponder and real search (it never is — see
  §7.1).

- **Miss (S != P).** The board is root+M+S. The ponder's entries are keyed to
  positions under root+M+P; where root+M+S transposes into them they help, and
  where it doesn't the 20-bit `HASH1..3` verify simply fails and the entry is
  ignored (tt.s ttfetch: "a miss bails without copying anything"). The real
  search is **bit-identical to today's** because `uidrive`/`m8engine` reset all
  search state at entry: `ABORT`, `NODECNT`, `PLY`, `HVALID`, `BESTFROM`, the
  soft-clock prime, and `ccsite` (restored from `pkclk` back to
  `checkclock`/`checkclocks`). The TT carrying stale-but-verified entries is
  the normal cross-move condition the engine already runs in (`m8new` leaves
  the TT alone across games precisely because entries self-verify,
  ui-design §6.1).

So the entire hit/miss handling is: **(1) never wipe the TT; (2) on return from
`m8ponder`, unmake P and clear `PONDERKEY`/`PONDERING` and restore `ccsite`.**
The "reset so the fresh search is identical to today's" that requirement 3 asks
for is already done, unconditionally, by the driver's own entry code — there is
nothing extra to reset on a miss.

`internal/ucibridge/ponder_test.go` proves both halves against the real image:
`TestPonderHeadStartDepth`/`…Nodes` (a hit reaches the same depth for far fewer
nodes) and **`TestPonderMissCorrectness`** (after pondering P, a follow-up on a
*different* reply X returns exactly the move it returns with no ponder at all).
Those tests are the device design's specification; the on-device port must
satisfy the same invariants.

---

## 7. TT carryover and time management (requirements 4 & 5)

### 7.1 Nothing between ponder and real search invalidates the TT

- **Side to move / hashing.** The side to move is folded into `HASH0..3` by
  `ENG_evalinit`, and every entry stores `HASH1..3` for verification. A ponder
  entry (opponent-to-move nodes under root+M+P) and a real entry are keyed by
  the same scheme; there is no separate "side" flag to get stale. Re-rooting via
  `evalinit` is all that is needed and the driver does it.
- **TT base / aux banking.** `TTBASE = $4000`, `$4000-$BFFF` aux, reached only
  through the LC-resident `ttfetch` under RAMRD (tt.s:1-7). Pondering does not
  touch that discipline; it calls the same `ENG_iterate`.
- **The clock/ADAPT state is re-primed, not carried.** `CLOCK_TRAP` is reset at
  every `uidrive` entry (the `PHASE`-primed value, m8.s:1379-1389). So ponder
  cycles do **not** leak into the real search's soft-clock accounting — the real
  move's adherence math is measured from its own fresh `CLOCK_TRAP`, exactly as
  today. This is the property that keeps the soft-clock calibration (the thing
  round 6 shipped a bug in, and the thing plan.md worried about) valid for
  own-move time.

### 7.2 Ponder time is free; a hit spends the full budget going deeper (v1)

In v1, a hit does **not** shorten the real search: `m8engine` runs to the full
per-move `BUDGET` on the warm TT and simply reaches a greater depth. This is the
simplest correct policy and it is strictly a strength gain on the opponent's
free time. The soft clock keeps accumulating **during the real search only**;
the ponder search's accumulation is thrown away with its `CLOCK_TRAP` re-prime.

### 7.3 Ponder-time banking (the FT2_ADAPT refinement — deferred)

The classic optimization is: on a hit, *bank* the time you would have spent, or
stop early once you exceed the depth the ponder reached. That folds ponder time
into the per-move budget and, under `FT2_ADAPT`, into the signed cycle bank
(`chesstest.BankedClock`). It is deliberately **out of v1** because:

- it requires knowing hit-vs-miss *before* the real search (to decide the
  budget), which reintroduces the branch §6 eliminated;
- the on-device bank arithmetic is the `4x/3x/÷4` shift-and-add the UI does not
  yet implement (ui-design §6.3), and getting soft-clock accounting wrong is the
  exact class of bug round 6 shipped;
- the Go reference keeps ponder cycles rigorously **separate** (`ponderCyc` vs
  own-move cycles, ucibridge.go:223-234) precisely so the two are never
  conflated. V1 mirrors that separation by discarding ponder time rather than
  banking it.

Spending the free time as extra depth (v1) captures most of the value with none
of the accounting risk. Banking is a measured second pass.

---

## 8. Entropy interaction (requirement 6)

`entropy.inc` **already anticipated pondering** (lines 27-30):

> optional, if the driver ever polls the keyboard from a non-blocking context
> (e.g. a ponder search interrupted by the human's first keystroke):
> `lda NODECNT` / `jsr entfold`.

The dither seed comes from *when* the human presses a key, measured by `entkey`
spinning `ENTCNT` while it waits (entropy.inc:8-20). Pondering removes that
wait: when the interrupting key lands, the search is running, `ENTCNT` has not
been spinning in `entkey`, and if the ponder poll cleared the strobe the key's
arrival time would be lost.

Two design commitments preserve entropy exactly:

1. **The ponder poll reads status only; it never clears the strobe.** `pkclk`
   reads `ENTKSTAT` (`$C000` bit 7 on hardware; the harness trap under test) and
   leaves the key buffered. When `m8ponder` returns, `uiread`'s `entkey` reads
   the *same* key normally and clears the strobe (entropy.inc:72-75). The key is
   consumed once, by `entkey`, so the collector, the line editor, and
   `HARNESSKBD` all behave unchanged.
2. **The interruption timing is folded at the poll.** `pkclk` does
   `lda NODECNT / jsr entfold` (and optionally `CLOCK_TRAP`) before setting
   `ABORT`. How far the search got before the key landed is as unpredictable as
   the keypress itself, so this replaces the entropy that the shortened `entkey`
   spin would otherwise have supplied. This is verbatim the hook entropy.inc
   documents.

Net: the entropy source is not degraded, and `internal/entropy`'s existing
collector tests still describe the shipped behaviour (the extra `entfold` at
interruption only *adds* mixing).

---

## 9. Space and cycles (requirement 7)

### 9.1 Bytes — and why the main-image headroom is the wrong budget

The ponder logic is UI/driver code and lives in the **Language Card** with the
rest of `m8.s`, at `$E000+`. The main-image headroom the brief cites (811 B;
the most recent audit in `docs/results.md`:6451 records 278 B) is **not the
relevant budget** — no ponder byte lands in the main image, so `engine.bin` and
the main-RAM ceiling are untouched. LC free space was measured at ~1,700 B
(ui-design §8, total 4,011 of 8,176 B used).

| new LC component | bytes (derived) |
|---|---|
| `pkclk` keyboard-poll `ccsite` variant | ~22 |
| `m8ponder`: predict P (shallow search via existing driver), validate via `uifind`, `ENG_make`(P) | ~70 |
| ponder driver wrapper (`PONDERING`/`PONDERKEY` gating of `uidrive`'s tail; `ccsite` patch/restore) | ~50 |
| `ENG_unmake`(P) + restore board/flags | ~20 |
| `mloop` insertion + synthetic ponder budget setup + skip-when-game-over | ~40 |
| entropy fold at interrupt (inside `pkclk`, counted above) | 0 |
| **code total** | **~200** |
| RAM state: `PONDERING`, `PONDERKEY`, `PPFROM`, `PPTO`, `PPFLAGS` (predicted move), 1 spare | **~6** |

Round-number budget: **~230 B code + ~6 B RAM, all in LC.** A `FT2`-style
enable is free: `FEATURES2` bits `$40` and `$80` are unused (defs.inc lists
`$10/$20/$04/$02` used, `$01/$08` reserved), or — better, since pondering is
driver-side and never touches `engine.bin` — a UI-only flag byte.

### 9.2 Cycles on the hot path: zero for own-move search

Because `ccsite` is patched to `pkclk` **only during the ponder search**, the
own-move search — every cycle any SPRT or gauntlet measures — runs the
unchanged `checkclock`/`checkclocks` stream. The per-node cost added to the
measured hot path is **0**.

During the ponder search (free opponent time), `pkclk` adds, once per 128
nodes: `jsr ENG_checkclocks` (already the soft-clock cost, ~32 cyc) + `lda
ENTKSTAT` (4) + `bpl` (2/3) ≈ 6 extra cycles per 128 nodes ≈ **0.05 cyc/node ≈
0.003%** on top of the soft clock — and it is spent on the clock we are trying
to fill, not the one we are rationing. Round 6 fought for hot-path cycles; this
adds none to it.

---

## 10. Measurement (requirement 8)

Pondering is **valuable against a human and unmeasurable in our harness**
against a human — there is no automated human. Against Sargon it is roughly
**symmetric**: Sargon III ponders in Hard Mode, and the `sargon-symmatch`
harness already ponders **both** engines (`cmd/sargon-symmatch`, and the
`internal/ucibridge` ponder machinery — `Ponder`, `PonderSelf`,
`PonderBudgetMs`). Ponder-hit rate there is measured at ~45-47%
(docs/results.md:6384, 6512).

**The honest problem: several published Elo figures rest on "the disk does not
ponder."** Specifically:

- The **+161 [+126, +199]** device-configuration number (docs/results.md:1290)
  is a *no-ponder* gauntlet, chosen because "the disk actually runs" without
  pondering. Its own note (results.md:1327-1333) warns: "do not put +161 next to
  +116" — removing ponder weakens both engines and Sargon relies on it more, so
  the no-ponder match is *a different benchmark*, not a better estimate.
- The ponder-enabled gauntlet figures (~+116/+126) were treated as *not* the
  shipped artifact because the disk didn't ponder (results.md:5010-5017,
  7-column decomposition at 1736-1772).

**Implementing device ponder flips which gauntlet describes the product.** Once
the disk ponders, the *ponder-enabled, symmetric* match (`sargon-symmatch` with
both sides pondering, Hard Mode) becomes the representative number, and the
no-ponder +161 reverts to being the artificial control it always was.

What must be re-measured / newly measured:

1. **A device-vs-Go ponder parity gate.** The on-device `m8ponder` must warm the
   TT and predict P equivalently to `internal/ucibridge`'s `ponder`/
   `ponderPrediction`. Extend `ponder_test.go`'s discipline to the m8 driver:
   after an on-device ponder of root+M+P, the follow-up real search's node count
   must match the bridge's warm-follow-up (the head-start invariant), and a miss
   must reproduce the cold move (`TestPonderMissCorrectness`).
2. **The ponder-enabled symmatch gauntlet re-run** as the headline device
   number, with the device's *own* ponder-hit rate reported (the shallow-search
   predictor's hit rate may differ from the free-TT predictor the harness uses —
   worth logging, per §5.2 vs §5.3).
3. **Own-move soft-clock adherence unchanged** — assert it, don't assume it.
   §7.1's "CLOCK_TRAP re-primed, ponder cycles discarded" claim is exactly the
   thing to verify with the existing soft-clock gates run on a pondering build:
   the own-move spend ratios (ui-design §6.2 table) must be identical to the
   non-pondering build, because ponder time is not banked in v1.

---

## 11. Blast radius and go/no-go (requirement 9)

**What this touches.** The search's *control flow* — but only via a runtime
operand patch of `ccsite` that is already patched by `FT2_SOFTCLK`, and only
while pondering. It adds no instruction to `engine.bin`. Everything else is new
LC code in `m8.s`.

**What could break, and the gate that catches it:**

| failure mode | caught by |
|---|---|
| Own-move search tree changed (the cardinal sin) | `TestMicroAB` (fingerprint), `TestIDIterationParity`, `TestTTSequenceParity`, `TestBudgetModeParity`, `TestSearchMirrorParity`, `TestGenDeferTreeIdentity` — all run `ccsite` at its default target and stay byte-identical by construction |
| Ponder abort tries to "play" P's refutation (the round-6 recovery trap, §4.3) | new device-ponder test mirroring `TestPonderMissCorrectness`; the invariant is "a ponder never commits a move" |
| Miss corrupts the fresh search | `TestPonderMissCorrectness` (Go ref) + its device port; also the existing full-game parity (`TestFullGameMirrorParity`) run on a pondering build must still play identical own-moves after a miss |
| `ENG_make`(P)/`ENG_unmake`(P) don't perfectly restore the board | perft already proves make/unmake is exact; add a board-hash before/after assertion in the device ponder test |
| Soft-clock own-move accounting drifts because ponder cycles leaked | soft-clock gates (`TestSoftClockNoTreeEffect`, `TestBridgeSoftClock`, ui-design §6.2 adherence) run on the pondering build — must match the non-pondering build |
| Entropy degraded by the non-blocking poll | `internal/entropy` collector tests; assert `ENTROPY` still changes across a pondered keystroke and that the strobe is consumed exactly once |
| Key consumed by the poll, lost to `uiread` | `internal/ui` full-game driver: a pondered game must read every typed move (the harness types on `$C000`/`$C010` already) |

**The blast radius is small precisely because the two hardest things are
already solved elsewhere:** the abort-and-unwind mechanism (`ABORT` + the
`ccsite` poll, round-6-hardened) and the hit/miss correctness model (the
self-verifying TT, proven by the Go ponder suite). Pondering wires those two
together on the opponent's clock.

**Recommendation: BUILD THE REDUCED VERSION (§0, and §11.A below).**

### 11.A The build

- Scheme A, shallow-search prediction (§5.2), full-budget real search on the
  warm TT (§7.2), ponder time discarded not banked (§7.3 deferred).
- ~230 LC bytes, ~6 RAM bytes, **zero** main-image and `engine.bin` cost, zero
  measured hot-path cycles.
- Every risky interaction maps to an existing gate or a one-file extension of
  `internal/ucibridge/ponder_test.go`.

### 11.B Deferred to a measured second pass

- Free-TT prediction (§5.3, needs the trivial `ENG_ttprobe` export).
- Ponder-time banking under `FT2_ADAPT` (§7.3, the
  soft-clock-accounting-sensitive part round 6 would warn about).

### 11.C The one thing not to do

Do **not** wire the ponder search into `uidrive`'s own-move abort recovery
(§4.3) — that is the single correctness landmine, and keeping the ponder
driver's tail separate (one flag test) disarms it.

There is no fatal flaw. The search is already re-entrant in the only sense
pondering needs (cooperative abort at the 128-node poll), the prediction and
carryover are already modelled and gated in Go, and the whole thing fits in the
Language Card without spending a byte the strength work was counting on.
