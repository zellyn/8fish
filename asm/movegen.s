; Move generator: pseudo-legal moves for SIDE, appended to the move stack
; at MSP as (from,to,flags) triples. Castling emits only if the king's
; start and through squares are safe; the landing square is covered by the
; caller's make/in-check filter like every other move.

; ---------------------------------------------------------------
; emitmove / emitmovef: emit one move (4 bytes: tier, from, to, flags)
; and advance MSP. The tier byte classifies the move for the search's
; pass scans at generation time (victimtype<<4 | class; see MOVESTACK
; in defs.inc); TIERTAB maps the victim piece byte to that tier, with
; ep and promo special-cased off the flags.
;
; Emit-interface fusion (deep optimization review, task #50). Every
; emit call site in the unrolled body already holds the target square
; in X and BOARD[target] (the victim byte, 0 = empty/quiet) in Y at the
; instant it calls emit; the old routine re-derived both from GTO via a
; board read. The convention now takes them as inputs:
;
;   emitmove   quiet moves and normal captures (flags == 0 implicitly).
;              X = to, Y = victim byte. No flags argument: the fast path
;              stores flags = 0 and reads the tier straight off Y.
;   emitmovef  flagged moves (ep, promo, double push, castle).
;              A = flags, X = to, Y = victim byte.
;
; Both store `to` via `txa`/`sta (MSP),y` (STX has no indirect-indexed
; mode), so X is PRESERVED across the call (genrecap keeps its slot
; index in X; promoloop and the pawn/double paths keep the target
; square there). GFROM still supplies the from byte; GTO is no longer
; consulted here (genrecap keeps its own copy for the promotion-rank
; test only). emitmovef preserves flags across the tier lookup on the
; stack.
;
; CONTRACT: the quiescence generator (generateq below) must emit no
; quiet moves. That is enforced structurally: every quiet call site
; in movegenbody.inc sits inside .if QMODE = 0, and the QMODE = 1
; copy keeps only captures, ep, and promotions (including promo
; pushes, which ARE kept in quiescence). A new quiet emission in the
; body must be wrapped the same way. The overflow check lives on the
; page-cross path: MSP+1 only reaches >MOVESTACKTOP via a carry out
; of the bump.
; ---------------------------------------------------------------
; emitmove: X = to, Y = victim byte. flags == 0 (quiet/normal capture).
emitmove:
        lda TIERTAB,y           ; victim byte -> victimtype<<4 | class
        ldy #0
        sta (MSP),y             ; tier
        lda GFROM
        iny
        sta (MSP),y             ; from
        txa                     ; to (X preserved: txa only reads X)
        iny
        sta (MSP),y             ; to
        lda #0
        iny
        sta (MSP),y             ; flags = 0
        lda MSP
        clc
        adc #4
        sta MSP
        bcs empage
        rts

; emitmovef: A = flags (nonzero), X = to, Y = victim byte.
emitmovef:
        pha                     ; save flags across the tier lookup
        and #FL_EP|FL_PROMO
        bne emsp                ; ep/promo: fixed tiers (Y unused)
        lda TIERTAB,y           ; double/castle: quiet tier off Y (=0)
emtier: ldy #0
        sta (MSP),y             ; tier
        lda GFROM
        iny
        sta (MSP),y             ; from
        txa                     ; to (X preserved: txa only reads X)
        iny
        sta (MSP),y             ; to
        pla                     ; flags
        iny
        sta (MSP),y             ; flags
        lda MSP
        clc
        adc #4
        sta MSP
        bcs empage
        rts
emsp:   and #FL_PROMO
        bne emprom
        lda #(PAWN<<4)|2        ; ep: pawn victim, light-capture class
        bne emtier              ; always
emprom: lda #$01                ; promotion: heavy class, no delta filter
        bne emtier              ; always
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
