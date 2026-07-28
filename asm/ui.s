; ---------------------------------------------------------------------------
; 8fish on-device UI: 40-column text screen renderer (docs/ui-design.md).
;
; DRIVER CODE. Nothing in this file is reachable from search/eval/movegen/
; board; it only READS BOARD/PIECESQ/SIDE/SCORE/CURDEPTH/BEST* and writes the
; text screen. The engine plays byte-identical chess whether it is driven by
; the UCI bridge or by this file.
;
; This module is assembled to run in LANGUAGE-CARD RAM ($E000-$FFEF), not in
; the $4000-$BEFF engine image: the engine already runs with LC bank-1 RAM
; read+write enabled ($C08B, see engine.s), and it uses only 65 bytes of it
; (LCCODE at $D000), so $E000-$FFFF is 8 KB of directly-addressable RAM that
; costs ZERO of the image's 1622-byte MAIN headroom. See docs/ui-design.md.
;
; Video encoding (Apple IIe primary character set):
;   normal video  = ASCII | $80          ($80-$FF; lowercase at $E0-$FF)
;   inverse upper = ASCII & $3F          ($00-$3F)
;   inverse lower = ASCII                ($60-$7F, IIe only)
; The board's DARK squares are drawn in inverse video and its piece colour is
; carried by CASE (uppercase = white, lowercase = black — the FEN convention),
; so both the checker pattern and the piece colour survive under a piece.
; ---------------------------------------------------------------------------

; ---- Screen geometry (40x24 text page 1) ----
BRDROW0   = 2           ; screen row holding rank 8
BRDCOL    = 3           ; screen column of the a-file cell's left character
PANCOL    = 21          ; move-list panel column
STATROW   = 12          ; side-to-move / status
CHKROW    = 13          ; check / mate / draw-claim status
THINKROW  = 14          ; depth / score / current best move (left column only:
                        ;  rows 3-15 of columns 21-39 are the move panel)
BOOKROW   = 16          ; opening name (full width, below the panel)
MSGROW    = 17          ; errors and prompts (full width)
HELPROW   = 20          ; command reminder (full width)
PROMPTROW = 23          ; input line

; ---- Borrowed zero page ----
; Every byte below is search/eval scratch that is provably dead at driver
; time (no search is running when the UI paints). The UI CLAIMS nothing.
SCRPTR    = T0          ; $CB-$CC  16-bit screen pointer
UISQ      = T2          ; $CD      current 0x88 square
UIDARK    = T3          ; $CE      $00 = light square, $10 = dark square
STRPTR    = MUL0        ; $CF-$D0  16-bit string pointer
UIROW     = EVTMP       ; $D7      loop scratch
UITMP     = PSQSQ       ; $D8      loop scratch
UIPLY     = PSQPIECE    ; $D9      loop scratch
UIMFIRST  = MULCNT      ; $DA      loop scratch
LAYPTR    = PSP0        ; $D2-$D3  16-bit layout-table pointer

; ---- UI RAM (Language Card, above the UI's own code) ----
;
; LC map ($E000-$FFFF; see docs/ui-design.md §2 — the engine runs with
; $C08B latched, so this is ordinary directly-executable RAM costing the
; engine ZERO MAIN bytes):
;
;   $E000-$F6FF  UICODE segment: code + static data (5,888 B; the link
;                config caps it here, so an overflow is a link error)
;   $F700-$F7FF  UI variables and screen-string buffers (this block)
;   $F800-$FAFF  game history: from / to / flags, one page each
;   $FB00-$FEFF  game hash history: HASH0-3, one page each
;   $FF00-$FFEF  free (240 B)
;   $FFF0-$FFFF  6502 vectors (RAM once LC read is enabled; m8.s writes them)
M8VARS    = $F700
UIHCNT    = M8VARS+$00  ; plies played so far
UILEVEL   = M8VARS+$01  ; 1..9
UIHUMAN   = M8VARS+$02  ; which colour the human has (0 / COLORMASK)
UIRESULT  = M8VARS+$03  ; RES_* game-over code, 0 = still playing
UINLEGAL  = M8VARS+$04  ; legal moves for the side to move (capped at 255)
UICHK     = M8VARS+$05  ; nonzero: side to move is in check
UIBLEN    = M8VARS+$06  ; characters in the input line
UIMFROM   = M8VARS+$07  ; parsed / matched move
UIMTO     = M8VARS+$08
UIMPROM   = M8VARS+$09  ; promotion piece type (0 = none, else N/B/R/Q = 2..5)
UIFFLAGS  = M8VARS+$0A  ; the generator's flags for the matched move
UIPTRL    = M8VARS+$0B  ; move-list walk cursor (kept OUT of zero page:
UIPTRH    = M8VARS+$0C  ;  make/unmake clobber most of the engine's ZP)
UITMPB    = M8VARS+$0D
UICNT2    = M8VARS+$0E
UIFOUND   = M8VARS+$0F  ; nonzero: uifind matched
UISCR0    = M8VARS+$10  ; 16-bit scratch (score formatting, scan bounds)
UISCR1    = M8VARS+$11
UIQ0      = M8VARS+$12  ; 16-bit quotient scratch
UIQ1      = M8VARS+$13
UIK       = M8VARS+$14  ; soft-clock margin multiplier
UIACC0    = M8VARS+$15  ; 32-bit shift-and-add multiply accumulator
UIACC1    = M8VARS+$16
UIACC2    = M8VARS+$17
UIACC3    = M8VARS+$18
UIWIN     = M8VARS+$19  ; winning side for a decisive result (0 / COLORMASK)
UILSC0    = M8VARS+$1A  ; last completed engine search score (engine POV)
UILSC1    = M8VARS+$1B
UIT0      = M8VARS+$1C  ; 24-bit temp for the per-move limit arithmetic
UIT1      = M8VARS+$1D
UIT2      = M8VARS+$1E
UISEEN    = M8VARS+$1F  ; repetition count of the current position
UIBUF     = M8VARS+$20  ; input line (UIBUFMAX bytes)
UITHINK   = M8VARS+$30  ; think line: depth / score / best move
UIMSGB    = M8VARS+$50  ; message row (40 B + terminator)
UIBOOKB   = M8VARS+$80  ; opening-name row (40 B + terminator)
                        ; $F7AA-$F7FF free

; The game history is three PARALLEL 256-byte arrays so a ply index fits in
; X with no multiply: 256 plies = 128 full moves (Sargon III's own move list
; caps at 127). Takeback replays the game from the start position through
; these arrays, so no per-ply undo record is stored.
UIHFROM   = $F800       ; 256 bytes: from-square per ply
UIHTO     = $F900       ; 256 bytes: to-square per ply
UIHFLAG   = $FA00       ; 256 bytes: move flags per ply

; Game hash history: UIHASHn[i] is the position's 32-bit Zobrist hash
; BEFORE ply i was played, so the CURRENT position is always at index
; UIHCNT. The engine's own repetition scan works off HASHSTK, which at the
; root holds nothing — the game history has to live here (design §5.3).
UIHASH0   = $FB00
UIHASH1   = $FC00
UIHASH2   = $FD00
UIHASH3   = $FE00

UIBUFMAX  = 8           ; longest accepted input line ("e7e8q" + slack)

; Game-over codes (UIRESULT).
RES_MATE   = 1
RES_STALE  = 2
RES_50     = 3
RES_REP    = 4
RES_RESIGN = 5
RES_AGREED = 6
RES_LONG   = 7
RES_ERR    = 8

; ---------------------------------------------------------------
; uigotorc: SCRPTR = the text-page-1 address of row A (0-23),
; column X (0-39). Clobbers A, Y.
;
; No carry propagation is needed: the largest row-base low byte is
; $D0 (row 23) and $D0 + 39 = $F7, so base+col never leaves the
; base's page. Callers MUST keep X <= 47.
; ---------------------------------------------------------------
uigotorc:
        tay
        txa
        clc
        adc ROWLO,y
        sta SCRPTR
        lda ROWHI,y
        sta SCRPTR+1
        rts

; ---------------------------------------------------------------
; uiputs: write the $00-terminated string at A/X (lo/hi) starting at
; SCRPTR. String bytes are stored RAW, so the data carries its own
; video attribute (see the encoding note above). Clobbers A, Y.
; ---------------------------------------------------------------
uiputs:
        sta STRPTR
        stx STRPTR+1
        ldy #0
upsl:   lda (STRPTR),y
        beq upsx
        sta (SCRPTR),y
        iny
        bne upsl
upsx:   rts

; ---------------------------------------------------------------
; SCRSTR/INVSTR: assemble a $00-terminated screen string in normal
; (ASCII|$80) or inverse-uppercase (ASCII&$3F) video. INVSTR must not
; contain '@' (which would encode as the $00 terminator).
; ---------------------------------------------------------------
.macro SCRSTR str
        .repeat .strlen(str), I
        .byte .strat(str, I) | $80
        .endrepeat
        .byte 0
.endmacro

.macro INVSTR str
        .repeat .strlen(str), I
        .byte .strat(str, I) & $3F
        .endrepeat
        .byte 0
.endmacro

; ---------------------------------------------------------------
; uistatic: paint a layout table at A/X (lo/hi). Each entry is four
; bytes — row, column, string lo, string hi — and the table ends with
; row = $FF. Clobbers A, X, Y and the borrowed ZP.
; ---------------------------------------------------------------
uistatic:
        sta LAYPTR
        stx LAYPTR+1
        ldy #0
        sty UITMP               ; byte cursor into the table
ustl:   ldy UITMP
        lda (LAYPTR),y
        cmp #$FF
        beq ustx
        pha                     ; row
        iny
        lda (LAYPTR),y
        tax                     ; column
        pla
        jsr uigotorc
        ldy UITMP
        iny
        iny
        lda (LAYPTR),y
        pha
        iny
        lda (LAYPTR),y
        tax
        pla
        jsr uiputs
        lda UITMP
        clc
        adc #4
        sta UITMP
        bne ustl
ustx:   rts


; ---------------------------------------------------------------
; uicls: fill the 40 visible columns of all 24 rows with normal
; spaces. Deliberately does NOT touch the eight per-page "screen
; holes" ($x478-$x47F etc.), which belong to peripheral firmware.
; Clobbers A, X, Y, SCRPTR.
; ---------------------------------------------------------------
uicls:  ldx #23
        stx UIROW
ucls1:  lda UIROW
        ldx #0
        jsr uigotorc
        ldy #39
        lda #$A0                ; normal space
ucls2:  sta (SCRPTR),y
        dey
        bpl ucls2
        dec UIROW
        bpl ucls1
        rts

; ---------------------------------------------------------------
; uiboard: paint the 8x8 board from BOARD into the text page.
;
; Two characters per square (piece letter + blank, both carrying the
; square's video attribute), so the board is a 16x8 block of 14x8-pixel
; cells at rows BRDROW0..BRDROW0+7, columns BRDCOL..BRDCOL+15.
;
; Reads BOARD only. Clobbers A, X, Y and the borrowed ZP above.
; ---------------------------------------------------------------
uiboard:
        lda #$70                ; a8 (rank 7, file 0)
        sta UISQ
        lda #0
        sta UIDARK              ; a8 is a LIGHT square
        lda #0
        sta UITMP               ; board row 0..7 (rank 8 down to rank 1)
ubrow:  lda UITMP
        clc
        adc #BRDROW0
        ldx #BRDCOL
        jsr uigotorc
        ldy #0
ubcol:  ldx UISQ
        lda BOARD,x             ; piece byte (zp,x: BOARD=$40, sq <= $77)
        and #$0F                ; colour<<3 | type; 0 = empty
        ora UIDARK
        tax
        lda PIECECH,x
        sta (SCRPTR),y
        iny
        ldx UIDARK              ; blank half-cell, same video attribute
        lda PIECECH,x           ;  (PIECECH[$00]=space, PIECECH[$10]=inv space)
        sta (SCRPTR),y
        iny
        lda UIDARK
        eor #$10                ; next file: flip the square colour
        sta UIDARK
        inc UISQ
        cpy #16
        bcc ubcol
        lda UISQ                ; step down a rank: we advanced 8 files, so
        sec                     ;  the next rank's a-file is UISQ - $18
        sbc #$18
        sta UISQ
        lda UIDARK              ; 8 flips = no net change; flip once more so
        eor #$10                ;  the new rank starts on the other colour
        sta UIDARK
        inc UITMP
        lda UITMP
        cmp #8
        bcc ubrow
        rts

; ---------------------------------------------------------------
; uicoords: rank digits down the left of the board and file letters
; underneath it. Clobbers A, X, Y, SCRPTR, UIROW.
; ---------------------------------------------------------------
uicoords:
        lda #0
        sta UIROW
ucrk:   lda UIROW
        clc
        adc #BRDROW0
        ldx #BRDCOL-2
        jsr uigotorc
        lda #('8'|$80)
        sec
        sbc UIROW               ; '8' down to '1'
        ldy #0
        sta (SCRPTR),y
        inc UIROW
        lda UIROW
        cmp #8
        bcc ucrk
        lda #BRDROW0+8
        ldx #BRDCOL
        jsr uigotorc
        ldy #0
        lda #('a'|$80)
ucfl:   sta (SCRPTR),y
        iny
        iny
        clc
        adc #1
        cpy #16
        bcc ucfl
        rts

; ---------------------------------------------------------------
; uisqout: A = 0x88 square -> two characters ("e2") at (SCRPTR),y,
; advancing Y by 2. Clobbers A.
; ---------------------------------------------------------------
uisqout:
        pha
        and #$0F
        clc
        adc #('a'|$80)
        sta (SCRPTR),y
        iny
        pla
        lsr
        lsr
        lsr
        lsr
        clc
        adc #('1'|$80)
        sta (SCRPTR),y
        iny
        rts

; ---------------------------------------------------------------
; uidec2: A = 0..99 -> two right-aligned digits at (SCRPTR),y,
; advancing Y by 2. A leading zero is printed as a space.
; Clobbers A, X.
; ---------------------------------------------------------------
uidec2:
        ldx #0
ud2l:   cmp #10
        bcc ud2d
        sbc #10
        inx
        bne ud2l
ud2d:   pha
        txa
        beq ud2sp
        clc
        adc #('0'|$80)
        bne ud2put
ud2sp:  lda #$A0
ud2put: sta (SCRPTR),y
        iny
        pla
        clc
        adc #('0'|$80)
        sta (SCRPTR),y
        iny
        rts

; ---------------------------------------------------------------
; uimoves: render the game move list into the side panel.
;
; UIHIST holds three bytes per ply (from, to, flags) and UIHCNT the
; number of plies played. The panel shows the last PANROWS full moves
; as "nn ffff tttt". Clobbers A, X, Y and the borrowed ZP.
; ---------------------------------------------------------------
PANROWS = 13

uimoves:
        lda #BRDROW0
        ldx #PANCOL
        jsr uigotorc
        lda #<pantxt
        ldx #>pantxt
        jsr uiputs
        ; first full move to show: max(0, ((UIHCNT+1)/2) - PANROWS)
        lda UIHCNT
        clc
        adc #1
        lsr                     ; number of full moves touched
        sec
        sbc #PANROWS
        bcs :+
        lda #0
:       sta UIMFIRST
        lda #0
        sta UIROW               ; panel line 0..PANROWS-1
umrow:  lda UIMFIRST
        clc
        adc UIROW
        sta UITMP               ; full-move index (0-based)
        asl
        cmp UIHCNT
        bcs umdone              ; no ply for this line: stop
        lda UIROW
        clc
        adc #BRDROW0+1
        ldx #PANCOL
        jsr uigotorc
        ldy #0
        lda UITMP
        clc
        adc #1                  ; move NUMBER is 1-based
        jsr uidec2              ; umply supplies the separating space
        lda UITMP               ; white's ply index = 2*move
        asl
        jsr umply
        lda UITMP               ; black's ply index = 2*move + 1
        asl
        clc
        adc #1
        cmp UIHCNT
        bcs umnext              ; game ended on white's move
        jsr umply
umnext: inc UIROW
        lda UIROW
        cmp #PANROWS
        bcc umrow
umdone: rts

; umply: A = ply index -> " e2e4" at (SCRPTR),y. Clobbers A, X.
umply:  tax
        lda #$A0
        sta (SCRPTR),y
        iny
        lda UIHFROM,x
        jsr uisqout
        lda UIHTO,x
        jmp uisqout

; ---------------------------------------------------------------
; Tables
; ---------------------------------------------------------------

; Text page 1 row bases: $0400 + (row .mod 8)*$80 + (row/8)*$28.
ROWLO:
        .repeat 24, I
        .byte <($0400 + (I .mod 8) * $80 + (I / 8) * $28)
        .endrepeat
ROWHI:
        .repeat 24, I
        .byte >($0400 + (I .mod 8) * $80 + (I / 8) * $28)
        .endrepeat

; Screen byte for a board square, indexed by  dark<<4 | (piece & $0F).
; piece & $0F = colour<<3 | type, so $01-$06 are white P N B R Q K and
; $09-$0E are black. $00 is an empty square; $07/$08/$0F cannot occur.
PIECECH:
        ; --- light squares: normal video (ASCII | $80) ---
        .byte $A0               ; $00 empty          ' '
        .byte $D0, $CE, $C2     ; $01-$03 wP wN wB   P N B
        .byte $D2, $D1, $CB     ; $04-$06 wR wQ wK   R Q K
        .byte $BF, $BF          ; $07-$08 impossible '?'
        .byte $F0, $EE, $E2     ; $09-$0B bP bN bB   p n b
        .byte $F2, $F1, $EB     ; $0C-$0E bR bQ bK   r q k
        .byte $BF               ; $0F impossible     '?'
        ; --- dark squares: inverse video ---
        ; uppercase/punct inverse = ASCII & $3F; lowercase inverse = ASCII
        .byte $20               ; $10 empty          inverse ' '
        .byte $10, $0E, $02     ; $11-$13 wP wN wB
        .byte $12, $11, $0B     ; $14-$16 wR wQ wK
        .byte $3F, $3F          ; $17-$18 impossible
        .byte $70, $6E, $62     ; $19-$1B bP bN bB
        .byte $72, $71, $6B     ; $1C-$1E bR bQ bK
        .byte $3F               ; $1F impossible

; Panel header, inverse video: "MOVES" padded to the panel width.
pantxt: .byte $20, $0D, $0F, $16, $05, $13
        .byte $20, $20, $20, $20, $20, $20, $20, $20, $20, $20, $20, $20, $20
        .byte 0
