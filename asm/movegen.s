; Move generator: pseudo-legal moves for SIDE, appended to the move stack
; at MSP as (from,to,flags) triples. Castling emits only if the king's
; start and through squares are safe; the landing square is covered by the
; caller's make/in-check filter like every other move.

; ---------------------------------------------------------------
; emitmove: A = flags; GFROM/GTO = squares. Advances MSP.
; CONTRACT: the quiescence generator (generateq below) must emit no
; quiet moves. That is enforced structurally: every quiet call site
; in movegenbody.inc sits inside .if QMODE = 0, and the QMODE = 1
; copy keeps only captures, ep, and promotions (including promo
; pushes, which ARE kept in quiescence). A new quiet emission in the
; body must be wrapped the same way. The overflow check lives on the
; page-cross path: MSP+1 only reaches >MOVESTACKTOP via a carry out
; of the bump.
; ---------------------------------------------------------------
emitmove:
        ldy #2
        sta (MSP),y
        dey
        lda GTO
        sta (MSP),y
        dey
        lda GFROM
        sta (MSP),y
        lda MSP
        clc
        adc #3
        sta MSP
        bcs empage
        rts
empage: inc MSP+1
        lda MSP+1
        cmp #>MOVESTACKTOP
        bcc :+
        lda #100                ; move-stack overflow: abort the run
        sta EXIT_TRAP
:       rts

; ---------------------------------------------------------------
; generate / generateq: all pseudo-legal moves for SIDE. Two copies
; of one body (movegenbody.inc), specialized at assembly time by
; QMODE: generateq is the quiescence generator - no quiet emission
; code at all (no quiet slider/step moves, no pawn pushes except
; promotions, no castling), so its board walks carry zero
; captures-only overhead. Keep the body single-source; a change to
; generation logic edits movegenbody.inc once.
; ---------------------------------------------------------------
; ---------------------------------------------------------------
; recap2 quiescence shaping: past RecapAfter=2 qs plies, generateq
; emits ONLY recaptures - captures whose destination is the square
; the previous move landed on (RECAPSQ = UNDOTO[PLY-1], set by
; gennode when RECAPONLY != 0). The QMODE=1 copy dispatches to an
; attacker-driven generator (genrecap in movegenbody.inc) that asks
; "which of my pieces capture on RECAPSQ" instead of generating the
; full move surface and filtering the emissions; the full-width
; generate copy is untouched.
; ---------------------------------------------------------------

QMODE   .set 0
.proc generate
        .include "movegenbody.inc"
.endproc

QMODE   .set 1
.proc generateq
        .include "movegenbody.inc"
.endproc
