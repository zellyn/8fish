; Test driver for the entropy collector (asm/entropy.inc).
;
; Runs the REAL collector code under the harness emulator: the harness input
; traps stand in for $C000/$C010 (HARNESSKBD), and the driving Go test decides
; WHEN each key becomes available, which is exactly what a human's timing does
; on hardware. For each simulated move it collects KEYSPM keystrokes, installs
; the seed with entseed, and emits the resulting SEED byte through the COUT
; trap, so the test sees the exact per-move seed stream the shipped driver
; would hand the search. Exits 0 after NMOVES moves; NMOVES = 0 means "run
; forever", for tests that simply stop stepping the machine.
;
; The Go side pokes NMOVES/KEYSPM (and the collector's initial ENTCNT/ENTROPY,
; which hardware deliberately leaves as RAM garbage).

        .include "defs.inc"

HARNESSKBD = 1          ; read the harness input traps as "the keyboard"

NMOVES  = $0300         ; poked: number of moves (seeds) to produce
KEYSPM  = $0301         ; poked: keystrokes collected per move (>= 1)

        .segment "CODE"

entry:  ldx #$FF
        txs
moveloop:
        ldx KEYSPM
keyloop:
        jsr entkey              ; blocking read + entropy fold (A = key)
        dex
        bne keyloop
        jsr entseed             ; SEED = ENTROPY (forced nonzero)
        lda SEED
        sta COUT_TRAP           ; emit this move's seed byte
        lda NMOVES
        beq moveloop            ; 0 = run forever
        dec NMOVES
        bne moveloop
        lda #0
        sta EXIT_TRAP
        rts

        .include "entropy.inc"
