.if ::QMODE = 1
        ; recap2 shaping: when gennode set RECAPONLY, take the
        ; attacker-driven recapture generator instead of walking every
        ; piece's full move surface and filtering the emissions.
        lda RECAPONLY
        beq :+
        jmp genrecap
:
.endif
        lda SIDE
        asl
        sta GSLOT
genloop:
        ldy GSLOT
        lda PIECESQ,y
        cmp #NOSQ
        beq gennext
        sta GFROM
        tax
        lda BOARD,x
        sta GPIECE
        and #TYPEMASK
        cmp #PAWN
        bne :+
        jmp genpawn
:       cmp #KNIGHT
        bne :+
        jmp genknight
:       cmp #KING
        bne :+
        jmp genking
:       cmp #BISHOP
        beq genbishop
        cmp #ROOK
        beq genrook
        ; queen: all 8 directions
        lda #<KINGOFF
        sta CURPTR
        lda #>KINGOFF
        sta CURPTR+1
        lda #8
        bne genslide            ; always
genbishop:
        lda #<DIAGOFF
        sta CURPTR
        lda #>DIAGOFF
        sta CURPTR+1
        lda #4
        bne genslide
genrook:
        lda #<ORTHOOFF
        sta CURPTR
        lda #>ORTHOOFF
        sta CURPTR+1
        lda #4
        bne genslide
gennext:
        inc GSLOT
        lda GSLOT
        and #$0F
        bne genloop
        rts

; ---- sliders: A = direction count; CURPTR -> offset table ----
genslide:
        sta GCOUNT
        lda #0
        sta GDIR
gsdirloop:
        ldy GDIR
        lda (CURPTR),y
        sta GDELTA
        lda GFROM
gswalk: clc
        adc GDELTA
        sta GTO
        and #$88
        bne gsnextdir           ; off board
        ldx GTO
        lda BOARD,x
        beq gsempty
        eor SIDE                ; enemy iff color bit differs
        and #COLORMASK
        beq gsnextdir           ; own piece: stop
        lda #0
        jsr emitmove            ; capture: emit, then stop
        jmp gsnextdir
gsempty:
.if ::QMODE = 0
        lda #0
        jsr emitmove
.endif
        lda GTO
        jmp gswalk
gsnextdir:
        inc GDIR
        lda GDIR
        cmp GCOUNT
        bne gsdirloop
        jmp gennext

; ---- knight / king: 8 single steps ----
genknight:
        lda #<KNIGHTOFF
        sta CURPTR
        lda #>KNIGHTOFF
        sta CURPTR+1
        bne genstep             ; always
genking:
        lda #<KINGOFF
        sta CURPTR
        lda #>KINGOFF
        sta CURPTR+1
genstep:
        lda #0
        sta GDIR
gstloop:
        ldy GDIR
        lda (CURPTR),y
        clc
        adc GFROM
        sta GTO
        and #$88
        bne gstnext
        ldx GTO
        lda BOARD,x
        beq gstquiet
        eor SIDE
        and #COLORMASK
        beq gstnext             ; own piece
        lda #0
        jsr emitmove
        jmp gstnext
gstquiet:
.if ::QMODE = 0
        lda #0
        jsr emitmove
.endif
gstnext:
        inc GDIR
        lda GDIR
        cmp #8
        bne gstloop
.if ::QMODE = 0
        ; kings also try castling (quiet: absent from the qs copy)
        lda GPIECE
        and #TYPEMASK
        cmp #KING
        beq gencastle
.endif
        jmp gennext

.if ::QMODE = 0
; ---- castling ----
; Emits if: rights bit set, between-squares empty, king square and the
; square the king passes through not attacked by the enemy.
gencastle:
        lda SIDE
        bne gcblack
        ; white kingside: e1(4) f1 g1
        lda CASTLE
        and #CR_WK
        beq gcwq
        ldx #$05
        lda BOARD,x
        bne gcwq
        ldx #$06
        lda BOARD,x
        bne gcwq
        lda #$04
        ldy #$05
        jsr gcsafe2
        bcs gcwq
        lda #$04
        sta GFROM
        lda #$06
        sta GTO
        lda #FL_CASTLE
        jsr emitmove
gcwq:   ; white queenside: e1 d1 c1 b1
        lda CASTLE
        and #CR_WQ
        beq gcdone
        ldx #$03
        lda BOARD,x
        bne gcdone
        ldx #$02
        lda BOARD,x
        bne gcdone
        ldx #$01
        lda BOARD,x
        bne gcdone
        lda #$04
        ldy #$03
        jsr gcsafe2
        bcs gcdone
        lda #$04
        sta GFROM
        lda #$02
        sta GTO
        lda #FL_CASTLE
        jsr emitmove
gcdone: jmp gennext

gcblack:
        ; black kingside: e8($74) f8 g8
        lda CASTLE
        and #CR_BK
        beq gcbq
        ldx #$75
        lda BOARD,x
        bne gcbq
        ldx #$76
        lda BOARD,x
        bne gcbq
        lda #$74
        ldy #$75
        jsr gcsafe2
        bcs gcbq
        lda #$74
        sta GFROM
        lda #$76
        sta GTO
        lda #FL_CASTLE
        jsr emitmove
gcbq:   ; black queenside: e8 d8 c8 b8
        lda CASTLE
        and #CR_BQ
        beq gcbdone
        ldx #$73
        lda BOARD,x
        bne gcbdone
        ldx #$72
        lda BOARD,x
        bne gcbdone
        ldx #$71
        lda BOARD,x
        bne gcbdone
        lda #$74
        ldy #$73
        jsr gcsafe2
        bcs gcbdone
        lda #$74
        sta GFROM
        lda #$72
        sta GTO
        lda #FL_CASTLE
        jsr emitmove
gcbdone:
        jmp gennext

; gcsafe2: carry set if either square A or square Y is attacked by the
; side NOT to move. Clobbers attacked()'s scratch.
gcsafe2:
        sta ATSQ
        sty GTMP
        lda SIDE
        eor #COLORMASK
        sta ATSIDE
        jsr attacked
        bcs gcs2out
        lda GTMP
        sta ATSQ
        jsr attacked
gcs2out:
        rts
.endif

; ---- pawns ----
genpawn:
        lda GPIECE
        and #COLORMASK
        bne genbpawn

        ; ---- white pawn: moves up (+$10) ----
        lda GFROM
        clc
        adc #$10
        sta GTO
        tax
        lda BOARD,x
        bne gwcaps              ; blocked: no pushes
        lda GFROM
        and #$F0
        cmp #$60
        beq gwpromopush
.if ::QMODE = 0
        lda #0
        jsr emitmove
        ; double push from rank 2
        lda GFROM
        and #$F0
        cmp #$10
        bne gwcaps
        lda GTO
        clc
        adc #$10
        tax
        lda BOARD,x
        bne gwcaps
        stx GTO
        lda #FL_DOUBLE
        jsr emitmove
.endif
        jmp gwcaps
gwpromopush:
        jsr promoloop
gwcaps: lda #$0F
        jsr gwcap1
        lda #$11
        jsr gwcap1
        jmp gennext

; gwcap1: A = capture offset for a white pawn.
gwcap1: clc
        adc GFROM
        sta GTO
        and #$88
        bne gwc1out
        ldx GTO
        lda BOARD,x
        beq gwc1ep
        eor SIDE
        and #COLORMASK
        beq gwc1out             ; own piece
        lda GTO
        and #$F0
        cmp #$70
        beq gwc1promo
        lda #0
        jmp emitmove
gwc1promo:
        jmp promoloop
gwc1ep: lda GTO
        cmp EPSQ
        bne gwc1out
        lda #FL_EP
        jmp emitmove
gwc1out:
        rts

genbpawn:
        ; ---- black pawn: moves down (-$10) ----
        lda GFROM
        sec
        sbc #$10
        sta GTO
        tax
        lda BOARD,x
        bne gbcaps
        lda GFROM
        and #$F0
        cmp #$10
        beq gbpromopush
.if ::QMODE = 0
        lda #0
        jsr emitmove
        ; double push from rank 7
        lda GFROM
        and #$F0
        cmp #$60
        bne gbcaps
        lda GTO
        sec
        sbc #$10
        tax
        lda BOARD,x
        bne gbcaps
        stx GTO
        lda #FL_DOUBLE
        jsr emitmove
.endif
        jmp gbcaps
gbpromopush:
        jsr promoloop
gbcaps: lda #$F1                ; -$0F
        jsr gbcap1
        lda #$EF                ; -$11
        jsr gbcap1
        jmp gennext

gbcap1: clc
        adc GFROM
        sta GTO
        and #$88
        bne gbc1out
        ldx GTO
        lda BOARD,x
        beq gbc1ep
        eor SIDE
        and #COLORMASK
        beq gbc1out
        lda GTO
        and #$F0
        bne gbc1nopromo         ; promotion only on rank 1 ($00-$07)
        jmp promoloop
gbc1nopromo:
        lda #0
        jmp emitmove
gbc1ep: lda GTO
        cmp EPSQ
        bne gbc1out
        lda #FL_EP
        jmp emitmove
gbc1out:
        rts

; promoloop: emit N/B/R/Q promotions for GFROM/GTO.
promoloop:
        lda #KNIGHT
        sta PROMTYPE
prloop: lda PROMTYPE
        jsr emitmove
        inc PROMTYPE
        lda PROMTYPE
        cmp #QUEEN+1
        bne prloop
        rts

.if ::QMODE = 1
; ---- attacker-driven recapture generator (quiescence copy only) ----
; genrecap: RECAPONLY=1 - emit exactly the captures whose destination
; is RECAPSQ, i.e. the recapture subset of the full quiescence
; emissions. RECAPSQ = UNDOTO[PLY-1] always holds the enemy piece that
; just moved there (every make leaves its mover on TO; the plies above
; a RECAPONLY node are qs plies, which never pass the move), so the
; only candidates are captures ONTO RECAPSQ: a promotion PUSH would
; need RECAPSQ empty, and an ep capture would need RECAPSQ == EPSQ
; while a live EPSQ square is empty by construction - both impossible.
; The empty/own-piece branches below are unreachable belt-and-braces:
; they emit nothing.
;
; Scans the side's 16 piece slots in ASCENDING order - the order the
; full generator walks GSLOT - and each piece can capture on RECAPSQ
; at most one way, so the emitted sequence matches the old
; generate-then-filter path move for move: identical search trees,
; only cheaper. (A pawn that recaptures onto the last rank emits its
; four promotion captures via promoloop, N/B/R/Q, same as before.)
;
; Structure mirrors attacked() in board.s: diff -> ATTACKTAB bits ->
; TYPEATK2 piece test -> slider ray walk; same scratch, and the slot
; half is patched into this routine's own PIECESQ operand low bytes
; ($00/$10) exactly like attacked()'s SMC.
genrecap:
        ldx RECAPSQ
        lda BOARD,x             ; RECAPSQ is a move destination: on-board
        beq grout               ; empty: nothing to recapture (see above)
        eor SIDE
        and #COLORMASK
        beq grout               ; own piece there: nothing to capture
        lda SIDE
        asl                     ; 0/$10 = slot-half base
        sta grslot0+1
        sta grslot1+1
        lda RECAPSQ
        clc
        adc #$78                ; $77 + 1: absorbed by the carry-clear
        sta ATT78               ;  adc below (eor #$FF is $FF-from)
        ldx #0
grloop:
grslot0:lda PIECESQ,x           ; operand lo byte is SMC ($00/$10)
        cmp #NOSQ
        beq grnext              ; tombstone
        ; diff = RECAPSQ - from + $77, via complement + add. The cmp
        ; above fell through, so A < $FF and carry is provably CLEAR
        ; here - the +1 it doesn't add is baked into ATT78. Do NOT
        ; reorder these three instructions.
        eor #$FF
        adc ATT78
        sta DIFF
        tay
        lda ATTACKTAB,y
        beq grnext              ; no geometric relation (hot exit)
        sta ATBITS
grslot1:lda PIECESQ,x           ; reload from-square (operand SMC too)
        sta GFROM
        tay
        lda a:BOARD,y
        and #TYPEMASK|COLORMASK
        tay
        lda TYPEATK2,y          ; piece's attack bit (pawns by color)
        and ATBITS
        beq grnext              ; wrong piece for this diff, incl.
                                ;  wrong-direction pawns
        cmp #ATK_DIAG
        bcc grhit               ; $01/$02: knight/king, no ray to walk
        cmp #ATK_WPAWN
        bcs grpawn              ; $10/$20: pawn, adjacent (promo check)
        ; slider: walk the ray from attacker toward RECAPSQ
        ldy DIFF
        lda DELTATAB,y
        sta ATDELTA
        lda GFROM
grwalk: clc
        adc ATDELTA
        cmp RECAPSQ
        beq grhit
        tay
        ; UNMASKED board read: safe only because this ray walks BETWEEN two
        ; on-board squares and exits at RECAPSQ before leaving the board (every
        ; square colinear between two on-board squares is on-board). See the
        ; off-board dead-space contract at BOARD in defs.inc.
        lda a:BOARD,y
        bne grnext              ; blocked
        tya
        jmp grwalk
grhit:  lda RECAPSQ
        sta GTO
        lda #0
        jsr emitmove            ; preserves X
grnext: inx
        cpx #$10
        bne grloop
grout:  rts
grpawn: ; pawn capture onto RECAPSQ; promotes if that is its last rank
        ldy RECAPSQ
        sty GTO
        and #ATK_WPAWN          ; A = the matched pawn bit ($10/$20)
        beq grbp
        tya                     ; white pawn: promotes on rank 8 ($7x)
        and #$F0
        cmp #$70
        beq grpromo
        bne grflat              ; always
grbp:   tya                     ; black pawn: promotes on rank 1 ($0x)
        and #$F0
        beq grpromo
grflat: lda #0
        jsr emitmove
        jmp grnext
grpromo:
        jsr promoloop           ; N,B,R,Q - as the full generator
        jmp grnext
.endif
