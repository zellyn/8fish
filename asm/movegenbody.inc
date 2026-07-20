; Shared generate/generateq body, assembled twice with ::QMODE 0/1
; (see movegen.s). Restructured for cycle economy:
;
;   - piece dispatch: compare chain with the pawn case a short branch
;     away (8 of 16 slots are pawns); no GPIECE store (pawn color comes
;     from SIDE, and the king handler owns its castle tail directly)
;   - sliders and steppers are fully unrolled per direction with
;     immediate deltas: no direction table fetch, no GDIR/GCOUNT/CURPTR
;     bookkeeping. The running square stays in A/X and the board byte
;     is read into Y, so an empty slider square costs 19 cycles
;     (15 on vertical rays) instead of 28.
;   - slider ray adds keep a carry invariant instead of clc per step:
;     a positive delta can never carry out of an on-board add (max
;     $77+$21 = $98 < $100), and a negative delta always has carry set
;     after an on-board result (no borrow), so C stays correct with the
;     delta pre-decremented (sec once + adc #delta-1). After a quiet
;     emission mid-ray (full-width copy) the carry is re-established
;     explicitly - emitmove's exit carry is not part of its contract.
;   - vertical rays (+/-$10) never change file bits, so off-board is
;     just "bit 7 set" (bmi) - no and #$88, and A survives as the
;     running square across the board read.
;   - pawns: the quiescence copy tests the promotion rank BEFORE
;     probing the push square (only promo pushes are emitted in qs),
;     and both copies inline the two capture probes with cpx-based
;     promotion (cpx #$70 / cpx #$08) and en-passant (cpx EPSQ) tests.
;   - promoloop is unrolled (N,B,R,Q immediates; tail-jump to emitmove).
;
; Emission ORDER is bit-for-bit identical to the classic table loops:
; slots ascending, directions in KNIGHTOFF/KINGOFF/DIAGOFF/ORTHOOFF
; table order, pushes before captures ($0F before $11, -$0F before
; -$11), promotions N,B,R,Q, castling kingside before queenside after
; the king steps. Search move ordering breaks score ties by stack
; position, so this order IS the search tree shape: do not reorder.
;
; Register conventions inside the handlers: GFROM = current from
; square; X = candidate target square (emitmove preserves X, which the
; pawn/promo paths rely on, as genrecap already did); Y = board byte
; scratch (emitmove clobbers Y).

.if .not .definedmacro(GSLIDEV)

; GSLIDEV delta - vertical slider ray (delta = +/-$10). File bits never
; change on a vertical ray, so off-board is exactly "bit 7 set" and the
; and #$88 mask is unnecessary; A keeps the running square across the
; read because the board byte goes to Y.
.macro GSLIDEV delta
        .local walk, occ, next
        lda GFROM
.if (delta) & $80
        sec                     ; C=1 invariant: on-board result -> no borrow
.else
        clc                     ; C=0 invariant: on-board add can't carry
.endif
walk:
.if (delta) & $80
        adc #<((delta)-1)       ; +C makes it a true +delta
.else
        adc #delta
.endif
        bmi next                ; rank underflow/overflow: off board
        tax
        ldy BOARD,x
        bne occ
.if ::QMODE = 0
        stx GTO
        lda #0
        jsr emitmove            ; preserves X
        txa
.if (delta) & $80
        sec                     ; emitmove does not preserve carry
.else
        clc
.endif
.endif
        jmp walk
occ:    tya
        eor SIDE                ; enemy iff color bit differs
        and #COLORMASK
        beq next                ; own piece: stop
        stx GTO
        lda #0
        jsr emitmove            ; capture: emit, then stop
next:
.endmacro

; GSLIDED delta - diagonal/horizontal slider ray: full $88 off-board
; mask, running square shuttled A->X->A around the mask.
.macro GSLIDED delta
        .local walk, occ, next
        lda GFROM
.if (delta) & $80
        sec
.else
        clc
.endif
walk:
.if (delta) & $80
        adc #<((delta)-1)
.else
        adc #delta
.endif
        tax
        and #$88
        bne next                ; off board
        ldy BOARD,x
        bne occ
.if ::QMODE = 0
        stx GTO
        lda #0
        jsr emitmove
.if (delta) & $80
        sec
.else
        clc
.endif
.endif
        txa
        jmp walk
occ:    tya
        eor SIDE
        and #COLORMASK
        beq next
        stx GTO
        lda #0
        jsr emitmove
next:
.endmacro

; GSTEP off - one knight/king step with immediate offset.
.macro GSTEP off
        .local occ, next
        lda GFROM
        clc
        adc #off
        tax
        and #$88
        bne next                ; off board
        ldy BOARD,x
        bne occ
.if ::QMODE = 0
        stx GTO
        lda #0
        jsr emitmove
        jmp next
.else
        beq next                ; A=0 from the mask: always taken
.endif
occ:    tya
        eor SIDE
        and #COLORMASK
        beq next                ; own piece
        stx GTO
        lda #0
        jsr emitmove
next:
.endmacro

; WPAWNCAP off - one white-pawn capture probe (SIDE known 0 here, so
; the enemy test is just the color bit). Promotion iff target rank 8
; (cpx #$70: on-board targets are <= $77). Ep only onto an empty
; square (cpx EPSQ; EPSQ=NOSQ=$FF never matches an on-board X).
.macro WPAWNCAP off
        .local occ, empty, promo, next
        lda GFROM
        clc
        adc #off
        tax
        and #$88
        bne next                ; off board
        ldy BOARD,x
        beq empty
        tya
        and #COLORMASK
        beq next                ; own piece (white)
        stx GTO
        cpx #$70
        bcs promo               ; capture onto rank 8
        lda #0
        jsr emitmove
        jmp next
promo:  jsr promoloop
        jmp next
empty:  cpx EPSQ
        bne next
        stx GTO
        lda #FL_EP
        jsr emitmove
next:
.endmacro

; BPAWNCAP off - black mirror: enemy = color bit clear, promotion iff
; target rank 1 (cpx #$08 / bcc).
.macro BPAWNCAP off
        .local occ, empty, promo, next
        lda GFROM
        clc
        adc #off
        tax
        and #$88
        bne next
        ldy BOARD,x
        beq empty
        tya
        and #COLORMASK
        bne next                ; own piece (black)
        stx GTO
        cpx #$08
        bcc promo               ; capture onto rank 1
        lda #0
        jsr emitmove
        jmp next
promo:  jsr promoloop
        jmp next
empty:  cpx EPSQ
        bne next
        stx GTO
        lda #FL_EP
        jsr emitmove
next:
.endmacro

.endif  ; macro guard

.if ::QMODE = 1
        ; recap2 shaping: when gennode set RECAPONLY, take the
        ; attacker-driven recapture generator instead of walking every
        ; piece's full move surface and filtering the emissions.
        lda RECAPONLY
        beq :+
        jmp genrecap
:
.endif
        ; Slot 0 of each half is the king when present (piece index 0
        ; is reserved for it), so it skips the board read and the
        ; dispatch chain - and KING drops out of the chain below (queen
        ; is the else case). The tombstone test STAYS: kingless test
        ; positions (e.g. the pawn-cache endgame suite) are legal
        ; inputs with slot 0 = NOSQ. Same slot order, same tree.
        lda SIDE
        asl
        sta GSLOT
        tay
        ldx PIECESQ,y
        cpx #NOSQ
        beq gennext             ; no king on the board: skip slot 0
        stx GFROM
        jmp genking
genloop:
        ldy GSLOT
        ldx PIECESQ,y
        cpx #NOSQ
        beq gennext             ; tombstone
        stx GFROM
        lda BOARD,x
        and #TYPEMASK
        cmp #PAWN
        beq genpawn
        cmp #KNIGHT
        bne :+
        jmp genknight
:       cmp #BISHOP
        bne :+
        jmp genbishop
:       cmp #ROOK
        bne :+
        jmp genrook
:       jmp genqueen            ; only Q left: B/R/N/P/K handled above
gennext:
        inc GSLOT
        lda GSLOT
        and #$0F
        bne genloop
        rts

; ---- pawns ----
; White moves up (+$10), promotes on rank 8; black mirrors.
genpawn:
        lda SIDE
        beq :+
        jmp genbpawn            ; white block + inlined caps > branch range
:
.if ::QMODE = 0
        ; single push, promo push, double push (order matches the
        ; classic code: push emissions precede the capture probes)
        lda GFROM
        clc
        adc #$10
        tax
        ldy BOARD,x             ; push square (always on board: no
                                ;  white pawn sits on rank 8)
        bne gwcaps              ; blocked: no pushes
        stx GTO
        lda GFROM
        and #$F0
        cmp #$60
        beq gwpromopush
        lda #0
        jsr emitmove
        ; double push from rank 2
        lda GFROM
        and #$F0
        cmp #$10
        bne gwcaps
        txa                     ; emitmove preserved X = from+$10
        clc
        adc #$10
        tax
        ldy BOARD,x
        bne gwcaps
        stx GTO
        lda #FL_DOUBLE
        jsr emitmove
        jmp gwcaps
gwpromopush:
        jsr promoloop
.else
        ; quiescence: the only push emission is a promotion push, so
        ; check the rank BEFORE touching the board
        lda GFROM
        and #$F0
        cmp #$60
        bne gwcaps
        lda GFROM
        clc
        adc #$10
        tax
        ldy BOARD,x
        bne gwcaps              ; blocked
        stx GTO
        jsr promoloop
.endif
gwcaps: WPAWNCAP $0F
        WPAWNCAP $11
        jmp gennext

genbpawn:
.if ::QMODE = 0
        lda GFROM
        sec
        sbc #$10
        tax
        ldy BOARD,x             ; no black pawn sits on rank 1
        bne gbcaps              ; blocked
        stx GTO
        lda GFROM
        and #$F0
        cmp #$10
        beq gbpromopush
        lda #0
        jsr emitmove
        ; double push from rank 7
        lda GFROM
        and #$F0
        cmp #$60
        bne gbcaps
        txa
        sec
        sbc #$10
        tax
        ldy BOARD,x
        bne gbcaps
        stx GTO
        lda #FL_DOUBLE
        jsr emitmove
        jmp gbcaps
gbpromopush:
        jsr promoloop
.else
        lda GFROM
        and #$F0
        cmp #$10
        bne gbcaps
        lda GFROM
        sec
        sbc #$10
        tax
        ldy BOARD,x
        bne gbcaps
        stx GTO
        jsr promoloop
.endif
gbcaps: BPAWNCAP $F1            ; -$0F
        BPAWNCAP $EF            ; -$11
        jmp gennext

; promoloop: emit N/B/R/Q promotions for GFROM/GTO (flags = piece
; type). Unrolled; preserves X (emitmove does); tail-jumps the last.
promoloop:
        lda #KNIGHT
        jsr emitmove
        lda #BISHOP
        jsr emitmove
        lda #ROOK
        jsr emitmove
        lda #QUEEN
        jmp emitmove

; ---- knights / kings: 8 unrolled steps in table order ----
genknight:
        GSTEP $DF
        GSTEP $E1
        GSTEP $EE
        GSTEP $F2
        GSTEP $0E
        GSTEP $12
        GSTEP $1F
        GSTEP $21
        jmp gennext

genking:
        GSTEP $EF
        GSTEP $F0
        GSTEP $F1
        GSTEP $FF
        GSTEP $01
        GSTEP $0F
        GSTEP $10
        GSTEP $11
.if ::QMODE = 0
        ; fall into castling (quiet: absent from the qs copy); the
        ; dedicated king handler replaces the old GPIECE king test
        jmp gencastle
.else
        jmp gennext
.endif

; ---- sliders: unrolled rays in table order ----
genbishop:
        GSLIDED $EF
        GSLIDED $F1
        GSLIDED $0F
        GSLIDED $11
        jmp gennext

genrook:
        GSLIDEV $F0
        GSLIDED $FF
        GSLIDED $01
        GSLIDEV $10
        jmp gennext

genqueen:                       ; KINGOFF order, as the classic loop
        GSLIDED $EF
        GSLIDEV $F0
        GSLIDED $F1
        GSLIDED $FF
        GSLIDED $01
        GSLIDED $0F
        GSLIDEV $10
        GSLIDED $11
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
        sta GTO                 ; every emission lands on RECAPSQ
        clc
        adc #$78                ; $77 + 1: absorbed by the carry-clear
        sta ATT78               ;  adc below (eor #$FF is $FF-from)
                                ; (RECAPSQ <= $77, so C=0 falls out)
        ldx #0
grloop:
grslot0:lda PIECESQ,x           ; operand lo byte is SMC ($00/$10)
        ; diff = RECAPSQ - from + $77, via complement + add. eor #$FF
        ; maps a NOSQ tombstone to zero (cheaper than cmp #NOSQ), and
        ; carry is provably CLEAR here: at first entry the adc #$78
        ; above can't carry, and every later pass comes through the
        ; cpx #$10 at the bottom with X < $10. The +1 it doesn't add
        ; is baked into ATT78. Do NOT reorder these instructions.
        eor #$FF
        beq grnext              ; tombstone
        adc ATT78
        tay
        lda ATTACKTAB,y
        beq grnext              ; no geometric relation (hot exit)
        sta ATBITS
        sty DIFF                ; save diff on the hit path only
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
grhit:  lda #0                  ; GTO = RECAPSQ, set once at entry
        jsr emitmove            ; preserves X
grnext: inx
        cpx #$10                ; X < $10 on the loop path -> C=0 for
        bne grloop              ;  the diff adc above
grout:  rts
grpawn: ; pawn capture onto RECAPSQ; promotes if that is its last rank
        and #ATK_WPAWN          ; A = the matched pawn bit ($10/$20)
        beq grbp
        lda GTO                 ; white pawn: promotes on rank 8 ($7x)
        and #$F0
        cmp #$70
        beq grpromo
        bne grflat              ; always
grbp:   lda GTO                 ; black pawn: promotes on rank 1 ($0x)
        and #$F0
        beq grpromo
grflat: lda #0
        jsr emitmove
        jmp grnext
grpromo:
        jsr promoloop           ; N,B,R,Q - as the full generator
        jmp grnext
.endif
