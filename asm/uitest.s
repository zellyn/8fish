; Proof-of-concept driver for the on-device UI renderer (asm/ui.s).
;
; Runs the REAL renderer under the harness emulator, from the address it will
; really run at: the stub at $4000 enables Language Card RAM exactly the way
; engine.s does, copies the UICODE segment to $E000, and jumps there. The
; renderer then paints a full 8fish screen from the position the Go test poked
; into BOARD/PIECESQ, and exits.
;
; The Go side (internal/ui) pokes the position, pokes UIREP, runs the image,
; and reads back text page 1 ($0400-$07FF). Running twice with different UIREP
; values yields the exact per-repaint cycle cost by difference.

        .include "defs.inc"

UIREP   = $030D         ; poked: extra uiboard repaints, for cycle measurement
UIREP2  = $030F         ; poked: extra WHOLE-SCREEN repaints
                        ;  (both in free driver-scratch RAM; see defs.inc)

        .segment "CODE"

        .import __UICODE_LOAD__, __UICODE_RUN__, __UICODE_SIZE__

entry:  ldx #$FF
        txs
        lda $C08B               ; LC bank 1, read RAM + write RAM
        lda $C08B
        lda #<__UICODE_LOAD__
        sta ZPTR
        lda #>__UICODE_LOAD__
        sta ZPTR+1
        lda #<__UICODE_RUN__
        sta TTPTR
        lda #>__UICODE_RUN__
        sta TTPTR+1
        ldx #>(__UICODE_SIZE__ + 255)
        ldy #0
ldloop: lda (ZPTR),y
        sta (TTPTR),y
        iny
        bne ldloop
        inc ZPTR+1
        inc TTPTR+1
        dex
        bne ldloop
        jmp uimain

        .segment "UICODE"

        ; ui.s first: it defines the SCRSTR/INVSTR macros this file uses,
        ; and ca65 macros must be defined before they are invoked.
        ; UITESTBUILD keeps uistatic, which only this driver still calls;
        ; the shipping payload (asm/m8.s) does not define it and saves
        ; the 50 bytes.
UITESTBUILD = 1
        .include "ui.s"

; ---------------------------------------------------------------
; uimain: paint one complete 8fish screen, then exit.
; ---------------------------------------------------------------
uimain:
        jsr demohist            ; install the demo game history in LC RAM
        jsr paint               ; the one repaint the screen assertion sees
        ; --- cycle measurement: UIREP2 extra WHOLE-SCREEN repaints ---
        lda UIREP2
        sta reps
        lda UIREP2+1
        sta reps+1
        ora reps
        beq brdrep
frep:   jsr paint
        lda reps
        bne :+
        dec reps+1
:       dec reps
        lda reps
        ora reps+1
        bne frep
brdrep:
        ; --- cycle measurement: UIREP extra board-only repaints ---
        lda UIREP
        sta reps
        lda UIREP+1
        sta reps+1
        ora reps
        beq done
rep:    jsr uiboard
        lda reps
        bne :+
        dec reps+1
:       dec reps
        lda reps
        ora reps+1
        bne rep
done:   lda #0
        sta EXIT_TRAP
        brk

reps:   .byte 0, 0

; paint: one complete screen repaint.
paint:  jsr uicls
        lda #<layout
        ldx #>layout
        jsr uistatic
        jsr uicoords
        jsr uimoves
        jmp uiboard

; ---------------------------------------------------------------
; demohist: copy the demo game (Ruy Lopez, closed, through 5...Be7)
; into the UI's Language-Card history arrays.
; ---------------------------------------------------------------
DEMOPLIES = 10

demohist:
        ldx #DEMOPLIES-1
dh1:    lda dfrom,x
        sta UIHFROM,x
        lda dto,x
        sta UIHTO,x
        lda dflag,x
        sta UIHFLAG,x
        dex
        bpl dh1
        lda #DEMOPLIES
        sta UIHCNT
        rts

;        e2e4 e7e5 g1f3 b8c6 f1b5 a7a6 b5a4 g8f6 e1g1 f8e7
dfrom:  .byte $14, $64, $06, $71, $05, $60, $41, $76, $04, $75
dto:    .byte $34, $44, $25, $52, $41, $50, $30, $55, $06, $64
dflag:  .byte $10, $10, $00, $00, $00, $00, $00, $00, $20, $00

; ---------------------------------------------------------------
; The proposed 8fish screen layout, as a uistatic table.
; ---------------------------------------------------------------
layout:
        .byte  0,  0
        .word  s_title
        .byte STATROW, 0
        .word  s_stm
        .byte CHKROW, 0
        .word  s_chk
        .byte THINKROW, 0
        .word  s_think
        .byte BOOKROW, 0
        .word  s_open
        .byte MSGROW, 0
        .word  s_msg
        .byte HELPROW, 0
        .word  s_help
        .byte PROMPTROW, 0
        .word  s_prompt
        .byte $FF

s_title:  INVSTR " 8FISH 1.0    LEVEL 4     YOU ARE WHITE "
s_stm:    SCRSTR "WHITE TO MOVE"
s_chk:    SCRSTR "CHECK"
s_think:  SCRSTR "D7  +0.34  b1c3"
s_open:   SCRSTR "BOOK: RUY LOPEZ, CLOSED"
s_msg:    SCRSTR "ILLEGAL MOVE - TRY AGAIN"
s_help:   SCRSTR "N-NEW T-TAKEBACK R-RESIGN D-DRAW L-LEVEL"
s_prompt: SCRSTR "YOUR MOVE? e2e4"
