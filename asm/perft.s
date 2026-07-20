; Perft driver. The Go test rig pokes BOARD, PIECESQ, SIDE, EPSQ, CASTLE,
; and ROOTDEPTH into memory (addresses parsed from asm/defs.inc), then
; runs this binary. It prints the 32-bit leaf count as 8 hex digits + LF
; via the COUT trap, leaves it in PCOUNT, and exits 0.

NOEVAL = 1
        .include "defs.inc"

        .segment "CODE"

entry:
        ldx #$FF
        txs
        lda #0
        sta PLY
        sta PCOUNT
        sta PCOUNT+1
        sta PCOUNT+2
        sta PCOUNT+3
        lda #<MOVESTACK
        sta MSP
        lda #>MOVESTACK
        sta MSP+1
        jsr perft
        jsr printcount
        lda #0
        sta EXIT_TRAP
        brk                     ; unreachable in the harness

; ---------------------------------------------------------------
; perft: count leaves at ROOTDEPTH below the current position.
; ---------------------------------------------------------------
perft:
        lda PLY
        cmp ROOTDEPTH
        bne pnotleaf
        inc PCOUNT
        bne pdone0
        inc PCOUNT+1
        bne pdone0
        inc PCOUNT+2
        bne pdone0
        inc PCOUNT+3
pdone0: rts
pnotleaf:
        ldy PLY
        lda MSP
        sta PLYBASELO,y
        sta CURSORLO,y
        lda MSP+1
        sta PLYBASEHI,y
        sta CURSORHI,y
        jsr generate
        ldy PLY
        lda MSP
        sta PLYENDLO,y
        lda MSP+1
        sta PLYENDHI,y
ploop:
        ldy PLY
        lda CURSORLO,y
        cmp PLYENDLO,y
        bne pnotdone
        lda CURSORHI,y
        cmp PLYENDHI,y
        beq pdone
pnotdone:
        lda CURSORLO,y
        sta CURPTR
        lda CURSORHI,y
        sta CURPTR+1
        ldy #1                  ; moves are (tier,from,to,flags)
        lda (CURPTR),y
        sta FROM
        iny
        lda (CURPTR),y
        sta TO
        iny
        lda (CURPTR),y
        sta MVFLAGS
        jsr make
        ; legality: did the mover leave their king attacked?
        lda SIDE                ; now the opponent = attacker
        sta ATSIDE
        eor #COLORMASK
        asl                     ; mover's slot base; king is slot 0
        tay
        lda PIECESQ,y
        sta ATSQ
        jsr attacked
        bcs pillegal
        jsr perft
pillegal:
        jsr unmake
        ; pop any child moves, advance cursor
        ldy PLY
        lda PLYENDLO,y
        sta MSP
        lda PLYENDHI,y
        sta MSP+1
        lda CURSORLO,y
        clc
        adc #4
        sta CURSORLO,y
        bcc ploop
        lda CURSORHI,y
        adc #0                  ; carry is set: +1
        sta CURSORHI,y
        jmp ploop
pdone:
        ; pop our own moves
        ldy PLY
        lda PLYBASELO,y
        sta MSP
        lda PLYBASEHI,y
        sta MSP+1
        rts

; ---------------------------------------------------------------
; printcount: PCOUNT as 8 hex digits, most significant first, then LF.
; ---------------------------------------------------------------
printcount:
        ldx #3
pcloop: lda PCOUNT,x
        lsr
        lsr
        lsr
        lsr
        jsr pnib
        lda PCOUNT,x
        and #$0F
        jsr pnib
        dex
        bpl pcloop
        lda #$0A
        sta COUT_TRAP
        rts
pnib:   cmp #10
        bcc pdig
        adc #'A'-10-1           ; carry is set
        sta COUT_TRAP
        rts
pdig:   adc #'0'
        sta COUT_TRAP
        rts

; TIERTAB: emitmove's victim-byte -> tier map (see search.s for the
; engine's copy; perft ignores the tier byte but shares emitmove).
        .segment "TABLES"
        .align 256
TIERTAB:
.repeat 32
        .byte $04
        .byte (PAWN<<4)|2, (KNIGHT<<4)|2, (BISHOP<<4)|2
        .byte (ROOK<<4)|1, (QUEEN<<4)|1, (KING<<4)|1
        .byte $04
.endrepeat
        .segment "CODE"

        .include "board.s"
        .include "movegen.s"
        .include "tables.s"
