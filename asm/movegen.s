; Move generator: pseudo-legal moves for SIDE, appended to the move stack
; at MSP as (tier,from,to,flags) 4-byte records. Castling emits only if
; the king's start and through squares are safe; the landing square is
; covered by the caller's make/in-check filter like every other move.

; ---------------------------------------------------------------
; Emit interface (deep optimization review rounds 3/4).
;
; The tier byte classifies the move for the search's pass scans at
; generation time (victimtype<<4 | class; see MOVESTACK in defs.inc);
; TIERTAB maps the victim piece byte to that tier, and the flagged
; emitters use fixed tier constants (TIER_QUIET/TIER_EPCAP/TIER_PROMO).
;
; QUIET moves in the full-width copy are no longer emitted through a
; subroutine at all: the body writes them inline at a running Y offset
; from MSP and flushes MSP at ray/handler boundaries (EMITQY/FLUSHY in
; movegenbody.inc) - the bytes and their order are identical to the
; classic path, only the MSP update is batched. The subroutines below
; serve the remaining call sites:
;
;   emitmove   normal captures (flags == 0 implicitly).
;              X = to, Y = victim byte (nonzero). Preserves X.
;   emitmovep  promotions. A = promotion piece type (KNIGHT..QUEEN,
;              = the flags byte), X = to. Tier is TIER_PROMO. Preserves
;              X. promoloop (below) emits the N,B,R,Q quartet.
;   emitmovef  ep and castling. A = flags (FL_EP or FL_CASTLE), X = to.
;              Y is IGNORED: both moves land on an empty square, so the
;              tier is TIER_EPCAP for ep and TIER_QUIET for castling.
;              (Double pushes, formerly here, are batched inline in the
;              full copy - EMITFY.)
;
; All emitters store `to` via `txa`/`sta (MSP),y` (STX has no
; indirect-indexed mode), so X is PRESERVED across the call (genrecap
; keeps its slot index there; promoloop and the pawn paths keep the
; target square there). GFROM supplies the from byte.
;
; CONTRACT: the quiescence generator (generateq below) must emit no
; quiet moves. That is enforced structurally: every quiet emission
; site in movegenbody.inc sits inside .if QMODE = 0, and the QMODE = 1
; copy keeps only captures, ep, and promotions (including promo
; pushes, which ARE kept in quiescence). A new quiet emission in the
; body must be wrapped the same way.
;
; Overflow: MSP+1 only reaches >MOVESTACKTOP via a carry out of a bump,
; checked at empage (per-emit paths) and flushpage (batched paths).
; Batched emission can write up to ~124 bytes past MOVESTACKTOP before
; its flush traps; the guard slack above MOVESTACKTOP is unused (see
; defs.inc).
; ---------------------------------------------------------------
; emitmove: X = to, Y = victim byte. flags == 0 (normal capture, or a
; quiet from a non-batched caller).
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

; emitmovep: A = promotion piece type (= flags byte), X = to.
emitmovep:
        pha                     ; flags = promo piece type
        lda #TIER_PROMO         ; heavy class, no delta filter
emtier: ldy #0
        sta (MSP),y             ; tier
        lda GFROM
        iny
        sta (MSP),y             ; from
        txa                     ; to (X preserved)
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

; emitmovef: A = flags (FL_EP or FL_CASTLE), X = to. Y ignored: both
; moves land on an empty square (ep tier is the fixed pawn-victim
; light-capture tier; castle tier is the quiet tier).
emitmovef:
        pha                     ; save flags across the tier select
        and #FL_EP
        beq emfq
        lda #TIER_EPCAP         ; ep: pawn victim, light-capture class
        bne emtier              ; always
emfq:   lda #TIER_QUIET         ; castle: quiet tier
        bne emtier              ; always

empage: inc MSP+1
        lda MSP+1
        cmp #>MOVESTACKTOP
        bcc :+
        lda #100                ; move-stack overflow: abort the run
        sta EXIT_TRAP
:       rts

; flushpage: page-cross tail for the batched-emission flush (FLUSHY/
; FLUSHEND in movegenbody.inc): the Y-offset fold into MSP carried out.
flushpage:
        inc MSP+1
        lda MSP+1
        cmp #>MOVESTACKTOP
        bcc :+
        lda #100                ; move-stack overflow: abort the run
        sta EXIT_TRAP
:       rts

; promoloop: emit N/B/R/Q promotions for GFROM, to = X. Shared by both
; body copies and genrecap (it was previously assembled twice).
; Unrolled; preserves X (emitmovep does); tail-jumps the last.
promoloop:
        lda #KNIGHT
        jsr emitmovep
        lda #BISHOP
        jsr emitmovep
        lda #ROOK
        jsr emitmovep
        lda #QUEEN
        jmp emitmovep

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
