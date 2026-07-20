# Deep optimization review round 3: the attacked() scan (task #49)

2026-07-20. Per-routine cycle hunt over the attack-detection family in
asm/board.s: `attacked()` and its ray walk. Companion diagnostics:
`TestAttackedDistribution` (decision instrument: exact cycle models of
three candidate designs evaluated on 90,512 real attacked() operand
snapshots), `TestAttackedDifferential` (old-vs-new answer equivalence
over 23,296 states).

## Caller audit (contract)

Every call site consumes ONLY the carry flag; no caller expects any
register or scratch preserved ("Clobbers A,X,Y" was already the
documented contract):

| caller | ATSQ | ATSIDE | consumes |
|---|---|---|---|
| `curincheck` (search.s, tail-jmp) | own king | ~SIDE | carry |
| `slfull` legality (search.s) | mover's king | SIDE (post-make) | carry |
| perft legality (perft.s) | mover's king | SIDE (post-make) | carry |
| `gcsafe2` castle-transit (movegenbody.inc) | e1/f1/d1/e8/f8/d8 | ~SIDE | carry |

So ~95% of calls are king-target queries (measured targetOcc = 95.4%),
and a contract change was on the table — but the winning design needed
none: same entry label, same inputs, same output, identical entry
counts in the TestMicroAB fingerprint.

## The decision: measured operand distribution, not vibes

Instrumented every attacked() entry during depth-5 searches on four
suite FENs (90,512 calls): hits 38.9%, live slots/call 11.16,
tombstones/call 1.86, geometric relations/call 2.27, slider walk
steps/call 1.23. Exact instruction-level cycle models on those
snapshots (model A validated against emulator-measured per-call cycles,
465.6 modeled vs 490.1 measured, ~5% from branch page-crossings):

| design | cycles/call | vs A |
|---|---|---|
| A: round-2 slot-scan loop (SMC slot base, eor/adc diff) | 465.6 | — |
| B: unrolled two-body scan, sbc tombstone-carry, jsr tail | 385.6 | **−17.2%** |
| B': same but ascending slot order | 399.6 | −14.2% |
| C: superpiece target-outward (documented in round 2) | 399.2 | −14.3% |

The superpiece rewrite LOSES to an improved slot scan on the real
distribution: its 8 knight probes are a ~150-cycle fixed tax and its
rays walk long in open positions, while the slot scan's early-out on
hits (38.9% of calls!) and cheap tombstones scale down with material.
The high→low slot order beats ascending by 14 cycles/call because
high slots (later FEN pieces: attacker pawns and minors near the
action) supply most early hits. Rejected: C (worse + ~2x code + takes
on off-board masking obligations), B' (worse), hybrid A/C dispatch
(adds dispatch cost to recover at most the B−C gap in a minority of
calls).

## What shipped (design B)

Two fully unrolled 16-slot bodies (white/black halves of PIECESQ as
fixed absolute operands — the SMC operand patching, the dex/bpl loop,
and the ATT78 zero-page staging all disappear). X holds ATSQ+$77 for
the whole scan; the per-slot filter is 6 instructions:

    txa                 ; A = ATSQ+$77
    sbc PIECESQ+n       ; diff = ATSQ-from+$77, AND the tombstone test:
    bcc next_tomb       ;  live from <= $77 <= ATSQ+$77 never borrows;
    tay                 ;  NOSQ $FF > $EE >= ATSQ+$77 always does
    lda ATTACKTAB,y
    beq next            ; no geometric relation (hot exit)

The sbc's carry-set precondition rides through every hot path free of
charge; the two carry-breaking paths (tombstone bcc, geometric-miss
return) re-enter the next slot through a 1-byte `sec` prefix entry.
Geometric relations jsr to a shared tail (atgeo) that reconstructs the
from-square arithmetically (from = (ATSQ+$77) − diff, one sbc — the
piece-list reload and its SMC second patch die too) and returns carry
= "this slot attacks". Slot 0 ends the chain: its post-atgeo carry IS
attacked()'s answer, so it plain rts's.

Costs: +512 bytes (engine.bin 19,965 → 20,477; MAIN ceiling $BF00,
~12KB free). Cheap-local labels were avoided so the profile stays
attributable (atw15..atw00/atb15..atb00/atgeo).

## Measured

- Model B predicted 385.6 cycles/call; shipped code measures 386.7 on
  the same operand mix (page-cross penalties ≈ 1 cycle/call). Old
  measured 490.1 → **−21.1%/call** on the depth-5 mix.
- TestQSProfile budget workload (the round-2 profile FENs): family
  share (label range attacked..make) 4.83% → 3.68% of ALL search
  cycles, 539.1 → 406.3 cycles/call (−24.6%), identical 3,761 calls.
  atslot0 (2.6% of QS both FENs in round 2) leaves the QS top-10.
- TestMicroAB (18 case×mask fingerprints): every score, move, and
  entry count (search/make/eval/attacked/ttprobe/generate) IDENTICAL;
  grand total 6,159,590,974 → 6,062,750,140 = **−1.57%** whole-search
  cycles (per-case −0.99%..−2.51%).

## Verification

- TestAttackedDifferential: 182 positions (recapture corpus, microab,
  WAC, plus 240 mid-search board/piece-list snapshots with tombstoned
  slots and promotions) × 64 squares × both attacker sides = 23,296
  old-vs-new calls, 0 mismatches.
- Model cross-check: A, B, and C booleans agree on all 90,512
  instrumented live-search states.
- Full non-short go test ./internal/chesstest/ green (perft suite is
  the attack-correctness workhorse; FT_CKVERIFY cross-check included).

## Not touched (and why)

- `ckwalk`/`ckdwalk` (gives-check propagation): they live inside
  make(), owned by the concurrent make/unmake review — flagged there.
- Tombstone value or piece-list compaction: NOSQ is make/unmake's
  contract; no ATTACKTAB-transparent tombstone value exists (proof
  sketch: the 64 on-board ATSQ values map any fixed tombstone to an
  8x8 diff block, and every 8x8 block of ATTACKTAB contains a nonzero
  entry via the long diagonals/orthogonals).
- Occupied-target walk specialization (skip `cmp ATSQ` per step until
  a nonzero square): saves ~6 cycles/call on 95.4% of calls but needs
  a gcsafe2 split or a board write; ~1.5% of the routine — not worth
  the second entry point.
