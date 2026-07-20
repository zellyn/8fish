; Board primitives: attacked, make, unmake. See defs.inc for layout.

; TYPEATKTAB[type] = attack-table bits that mean "this piece type attacks
; across this difference (geometrically)". Pawns are special-cased.
TYPEATKTAB:
        .byte 0, 0, ATK_KNIGHT, ATK_DIAG, ATK_ORTHO, ATK_DIAG|ATK_ORTHO, ATK_KING

; ---------------------------------------------------------------
; attacked: is ATSQ attacked by any piece of side ATSIDE (0/$08)?
; Out: carry set if attacked. Clobbers A,X,Y, ATTMP/ATBITS/DIFF/
; ATDELTA (ATT78 is no longer touched; genrecap still uses it).
;
; Deep-opt round 3: fully unrolled per-side bodies (no SMC, no dex/bpl
; loop, no zp ATT78 reload) scanning slots high-to-low like the old
; loop. X holds ATSQ+$77 for the whole scan and the per-slot filter is
;
;         txa                   ; A = ATSQ+$77
;         sbc PIECESQ+n         ; diff = ATSQ-from+$77; carry doubles
;         bcc next_tomb         ;  as the tombstone test: a live square
;         tay                   ;  (<= $77 <= ATSQ+$77) never borrows,
;         lda ATTACKTAB,y       ;  NOSQ ($FF > $EE >= ATSQ+$77) always
;         beq next              ;  does
;
; The sbc needs carry SET on entry; every arithmetic path preserves it
; (tay/lda/beq/jsr don't touch C), and the two paths that break it -
; a tombstone's bcc and atgeo's carry-clear miss return - re-enter the
; next slot at its next_tomb entry, which is a sec prefix. Geometric
; relations (~2 per call) take a jsr to the shared atgeo tail, which
; reconstructs the from-square as (ATSQ+$77)-diff instead of reloading
; the piece list, and returns carry = "this slot attacks ATSQ".
; ---------------------------------------------------------------

; One 16-slot body. prefix names the per-slot labels (atwNN/atbNN, with
; NN_t the carry-repairing tombstone entries); base is the side's
; PIECESQ half. Slot 0 ends the chain: its tombstone bcc lands on a
; plain rts with carry already clear, its no-relation beq on clc/rts,
; and after its jsr atgeo the carry IS the answer, so it just rts's.
.macro ATBODY prefix, base
        .local missclc, rtsonly
.repeat 15, i
.ident(.sprintf("%s%02d_t", prefix, 15-i)):
        sec
.ident(.sprintf("%s%02d", prefix, 15-i)):
        txa
        sbc base+15-i
        bcc .ident(.sprintf("%s%02d_t", prefix, 14-i))
        tay
        lda ATTACKTAB,y
        beq .ident(.sprintf("%s%02d", prefix, 14-i))
        jsr atgeo
        bcc .ident(.sprintf("%s%02d_t", prefix, 14-i))
        rts
.endrepeat
.ident(.sprintf("%s00_t", prefix)):
        sec
.ident(.sprintf("%s00", prefix)):
        txa
        sbc base
        bcc rtsonly             ; tombstone: carry already clear
        tay
        lda ATTACKTAB,y
        beq missclc
        jsr atgeo               ; carry = answer
rtsonly:
        rts
missclc:
        clc
        rts
.endmacro

attacked:
        lda ATSIDE
        beq atwentry
        lda ATSQ
        clc
        adc #$77                ; never carries: ATSQ <= $77
        tax
        jmp atb15_t
atwentry:
        lda ATSQ
        clc
        adc #$77
        tax
        ; fall through into the white body's sec entry
ATBODY "atw", PIECESQ
ATBODY "atb", PIECESQ+16

; atgeo: shared geometric tail. In: A = ATTACKTAB bits (nonzero),
; Y = diff, X = ATSQ+$77 (preserved), carry SET. Out: carry set iff
; this slot's piece attacks ATSQ. Clobbers A,Y, ATTMP/ATBITS/DIFF/
; ATDELTA.
atgeo:  sty DIFF
        sta ATBITS
        txa
        sbc DIFF                ; carry set: A = from = ATSQ+$77-diff
        sta ATTMP
        tay
        lda a:BOARD,y
        and #TYPEMASK|COLORMASK
        tay
        lda TYPEATK2,y          ; piece's attack bit (pawns by color)
        and ATBITS
        beq atgmiss             ; wrong piece for this diff, incl.
                                ;  wrong-direction pawns
        cmp #ATK_DIAG
        bcc atghit              ; $01/$02: knight/king, no ray to walk
        cmp #ATK_WPAWN
        bcs atghit              ; $10/$20: pawn, adjacent
        ; slider: walk the ray from attacker toward ATSQ
        ldy DIFF
        lda DELTATAB,y
        sta ATDELTA
        lda ATTMP
atwalk: clc
        adc ATDELTA
        cmp ATSQ
        beq atghit
        tay
        ; UNMASKED board read: safe only because this ray walks BETWEEN two
        ; on-board squares and exits at ATSQ before leaving the board (every
        ; square colinear between two on-board squares is on-board). See the
        ; off-board dead-space contract at BOARD in defs.inc.
        lda a:BOARD,y
        bne atgmiss             ; blocked
        tya
        jmp atwalk
atgmiss:
        clc
        rts
atghit: sec
        rts

; ---------------------------------------------------------------
; make: play FROM/TO/MVFLAGS. Saves undo state indexed by PLY,
; updates board, piece lists, castling rights, ep, side; PLY++.
; Clobbers A,X,Y and most scratch.
; ---------------------------------------------------------------
make:
        ldx PLY
        lda CASTLE
        sta UNDOCASTLE,x
        lda EPSQ
        sta UNDOEP,x
        lda FROM
        sta UNDOFROM,x
        lda TO
        sta UNDOTO,x
        lda MVFLAGS
        sta UNDOFLAGS,x
.ifndef NOEVAL
        lda MGSCORE
        sta UNDOMGLO,x
        lda MGSCORE+1
        sta UNDOMGHI,x
        lda EGSCORE
        sta UNDOEGLO,x
        lda EGSCORE+1
        sta UNDOEGHI,x
        lda PHASE
        sta UNDOPHASE,x
        lda HALFMOVE
        sta UNDOHALF,x
        lda HASH0
        sta HASHSTK0,x
        lda HASH1
        sta HASHSTK1,x
        lda HASH2
        sta HASHSTK2,x
        lda HASH3
        sta HASHSTK3,x
        lda PSTRUCT
        sta UNDOPSL,x
        lda PSTRUCT+1
        sta UNDOPSH,x
.endif
        ldy FROM
        lda a:BOARD,y           ; force absolute: no lda zp,y mode exists
        sta MVPIECE
        sta UNDOPIECE,x

        ; capture square: TO, or the pushed-past square for en passant
        lda MVFLAGS
        and #FL_EP
        beq mknotep
        lda MVPIECE
        and #COLORMASK
        bne mkepb
        lda TO                  ; white captures: victim is below TO
        sec
        sbc #$10
        jmp mkhavecap
mkepb:  lda TO                  ; black captures: victim is above TO
        clc
        adc #$10
        jmp mkhavecap
mknotep:
        lda TO
mkhavecap:
        sta UNDOCAPSQ,x
        tay
        lda a:BOARD,y           ; victim byte (0 if quiet move)
        sta UNDOCAP,x
        beq mknocap
        ; remove victim: hash+eval out, clear square, tombstone list slot
        sta VICTIM
.ifndef NOEVAL
        jsr takepiece           ; A = victim, Y = capture square: fused
        ldx PLY
.endif
        ldy UNDOCAPSQ,x
        lda #0
        sta a:BOARD,y
        ldy VICTIM
        lda SLOTTAB,y
        tay
        lda #NOSQ
        sta PIECESQ,y
mknocap:
.ifndef NOEVAL
        ; 50-move clock: reset on capture or pawn move
        ldx PLY
        lda UNDOCAP,x
        bne mkhmzero
        lda MVPIECE
        and #TYPEMASK
        cmp #PAWN
        beq mkhmzero
        inc HALFMOVE
        jmp mkhmdone
mkhmzero:
        lda #0
        sta HALFMOVE
mkhmdone:
.endif
        ; move the piece. Non-promotion movers take the fused
        ; movepiece (hash + psqt delta; PHASE provably cancels);
        ; promotions change the piece byte, so they keep the split
        ; hashpiece/rempiece/addpiece path.
        ldy FROM
        lda #0
        sta a:BOARD,y
        lda MVFLAGS
        and #FL_PROMO
        beq mknopromo
        sta GTMP
        lda MVPIECE
        and #INDEXMASK|COLORMASK
        ora GTMP
        sta CRTMP               ; final piece byte (post-promotion)
.ifndef NOEVAL
        lda MVPIECE
        ldy FROM
        jsr hashpiece
        lda MVPIECE
        jsr rempiece            ; hashpiece preserved Y = FROM
        lda MVPIECE             ; promotion: the pawn leaves FROM and no
        ldy FROM                ;  pawn appears — toggle only its FROM bit
        jsr pbtoggle
        lda CRTMP
        ldy TO
        jsr hashpiece
        lda CRTMP
        jsr addpiece            ; hashpiece preserved Y = TO
.endif
        jmp mkplace
mknopromo:
        lda MVPIECE
        sta CRTMP
.ifndef NOEVAL
        jsr movepiece           ; fused hash + psqt for the mover
.endif
mkplace:
        ldy TO
        lda CRTMP
        sta a:BOARD,y
        ldy CRTMP
        lda SLOTTAB,y
        tay
        lda TO
        sta PIECESQ,y

        ; castling: also move the rook
        lda MVFLAGS
        and #FL_CASTLE
        beq mknocastle
        jsr castlerook
mknocastle:
        ; rights: CASTLE &= CASTLEMASK[FROM] & CASTLEMASK[TO]
        ldy FROM
        lda CASTLEMASK,y
        ldy TO
        and CASTLEMASK,y
        and CASTLE
        sta CASTLE
        ; ep square: midpoint of a double push, else none
        lda MVFLAGS
        and #FL_DOUBLE
        beq mknodouble
        lda FROM
        clc
        adc TO                  ; never carries for double-push squares
        lsr
        sta EPSQ
        jmp mkflip
mknodouble:
        lda #NOSQ
        sta EPSQ
mkflip:
.ifndef NOEVAL
        ; hash: castling-rights change, ep change, side to move
        ldx PLY
        lda UNDOCASTLE,x
        cmp CASTLE
        beq mknocch
        jsr hashcastle          ; xor out the old rights
        ldx PLY
        lda CASTLE
        jsr hashcastle          ; xor in the new
mknocch:
        ldx PLY
        lda UNDOEP,x
        cmp EPSQ
        beq mknoech
        cmp #NOSQ
        beq :+
        jsr hashep              ; xor out the old ep file
:       lda EPSQ
        cmp #NOSQ
        beq mknoech
        jsr hashep              ; xor in the new
mknoech:
        jsr hashstm
.endif
        lda SIDE
        eor #COLORMASK
        sta SIDE
        inc PLY
.ifndef NOEVAL
        ; gives-check propagation (perf review F2): INCHK for the child
        ; ply is computed here from the difference tables instead of a
        ; full attacked() scan at the child's entry. Castles and ep
        ; captures (two vacated squares / rook lines) take the full scan.
        lda MVFLAGS
        and #FL_CASTLE|FL_EP
        beq ckfast
        jsr curincheck          ; side to move (the opponent) in check?
        ldx PLY
        lda #0
        rol
        sta INCHK,x
        jmp ckdone
ckfast: ; direct check: does the piece now on TO attack the enemy king?
        lda SIDE
        asl
        tay
        lda PIECESQ,y           ; enemy king (side to move after the flip)
        sta ATSQ
        sec
        sbc TO
        clc
        adc #$77
        tay
        lda ATTACKTAB,y
        beq cknodir
        sta ATBITS
        sty DIFF
        ldx TO
        lda BOARD,x
        and #TYPEMASK
        tax
        cpx #PAWN
        bne cknp
        lda SIDE                ; mover color = SIDE ^ COLORMASK
        eor #COLORMASK
        bne ckbp
        lda ATBITS              ; white pawn just moved
        and #ATK_WPAWN
        bne ckhit
        beq cknodir             ; always
ckbp:   lda ATBITS
        and #ATK_BPAWN
        bne ckhit
        beq cknodir             ; always
cknp:   lda TYPEATKTAB,x
        and ATBITS
        beq cknodir
        cpx #KNIGHT
        beq ckhit
        cpx #KING
        beq cknodir             ; a king never gives check
        ldy DIFF                ; slider: walk TO -> K for blockers
        lda DELTATAB,y
        sta ATDELTA
        lda TO
ckwalk: clc
        adc ATDELTA
        cmp ATSQ
        beq ckhit
        tax
        ; UNMASKED board read: safe only by colinear termination at ATSQ (an
        ; on-board target); see the off-board dead-space contract at BOARD.
        lda BOARD,x
        bne cknodir             ; blocked
        txa
        jmp ckwalk
cknodir:
        ; discovered check: vacating FROM may open a ray from K through
        ; FROM to one of the mover's sliders
        lda ATSQ
        sec
        sbc FROM
        clc
        adc #$77
        tay
        lda ATTACKTAB,y
        and #ATK_DIAG|ATK_ORTHO
        beq cknone
        sta ATBITS              ; the K-FROM ray's orientation
        lda FROM
        sec
        sbc ATSQ
        clc
        adc #$77
        tay
        lda DELTATAB,y
        beq cknone
        sta ATDELTA
        lda ATSQ
ckdwalk: clc
        adc ATDELTA
        tax
        and #$88
        bne cknone              ; off the board: nothing behind FROM
        lda BOARD,x
        beq ckdnext              ; empty (including FROM itself)
        eor SIDE                ; first piece: mover's color?
        and #COLORMASK
        beq cknone              ; checked side's own piece: blocked
        lda BOARD,x
        and #TYPEMASK
        tay
        lda TYPEATKTAB,y
        and ATBITS              ; slider matching the ray orientation?
        bne ckhit
        beq cknone              ; always
ckdnext: txa
        jmp ckdwalk
ckhit:
        ldx PLY
        lda #1
        sta INCHK,x
        bne ckdone              ; always
cknone: ldx PLY
        lda #0
        sta INCHK,x
ckdone:
.ifdef PTNOCACHE
        ; measurement baseline (task #47): disable the incremental
        ; pawn-structure cache. make never recomputes; eval recomputes
        ; PSTRUCT fresh on every call. Same PSTRUCT at every eval, so
        ; identical search trees — only cycles differ.
        lda #0
        sta PDIRTY
.else
        ; refresh the pawn/king structure term if a pawn or king moved
        lda PDIRTY
        beq :+
        lda FEATURES
        and #FT_PSTRUCT
        beq :+
        jmp pawnterm            ; clears PDIRTY; rts returns to caller
:       lda #0
        sta PDIRTY
.endif
.endif
        rts

; ---------------------------------------------------------------
; castlerook: move the rook for the castle move being made; TO tells
; which corner. Uses GTMP/GTO as rook from/to. Clobbers A,Y.
; ---------------------------------------------------------------
castlerook:
        lda TO
        cmp #$06                ; g1
        bne crnot1
        lda #$07
        sta GTMP
        lda #$05
        bne crgo                ; always
crnot1: cmp #$02                ; c1
        bne crnot2
        lda #$00
        sta GTMP
        lda #$03
        bne crgo
crnot2: cmp #$76                ; g8
        bne crnot3
        lda #$77
        sta GTMP
        lda #$75
        bne crgo
crnot3: lda #$70                ; c8
        sta GTMP
        lda #$73
crgo:   sta GTO
        ldy GTMP                ; rook from
        lda a:BOARD,y
        sta CRTMP
.ifndef NOEVAL
        jsr hashpiece           ; A = rook byte, Y = from square (kept)
        lda CRTMP
        jsr rempiece
        lda CRTMP
        ldy GTO
        jsr hashpiece
        lda CRTMP
        jsr addpiece
.endif
        ldy GTMP
        lda #0
        sta a:BOARD,y
        ldy GTO
        lda CRTMP
        sta a:BOARD,y
        ldy CRTMP
        lda SLOTTAB,y           ; rook byte -> slot
        tay
        lda GTO
        sta PIECESQ,y
        rts

; ---------------------------------------------------------------
; uncastlerook: undo the rook move; UNDOTO,x (x=PLY) tells the corner.
; ---------------------------------------------------------------
uncastlerook:
        lda UNDOTO,x
        cmp #$06
        bne ucnot1
        lda #$05
        sta GTMP
        lda #$07
        bne ucgo
ucnot1: cmp #$02
        bne ucnot2
        lda #$03
        sta GTMP
        lda #$00                ; Z is set: must jmp, not branch-always
        jmp ucgo
ucnot2: cmp #$76
        bne ucnot3
        lda #$75
        sta GTMP
        lda #$77
        bne ucgo
ucnot3: lda #$73
        sta GTMP
        lda #$70
ucgo:   sta GTO
        ldy GTMP                ; rook currently here
        lda a:BOARD,y
        sta CRTMP
        lda #0
        sta a:BOARD,y
        ldy GTO
        lda CRTMP
        sta a:BOARD,y
        ldy CRTMP
        lda SLOTTAB,y           ; rook byte -> slot
        tay
        lda GTO
        sta PIECESQ,y
        rts

; ---------------------------------------------------------------
; unmake: undo the move recorded at PLY-1. Restores side, castle, ep,
; board, piece lists.
; ---------------------------------------------------------------
unmake:
        dec PLY
        ldx PLY
        lda SIDE
        eor #COLORMASK
        sta SIDE
        lda UNDOCASTLE,x
        sta CASTLE
        lda UNDOEP,x
        sta EPSQ
.ifndef NOEVAL
        lda UNDOMGLO,x
        sta MGSCORE
        lda UNDOMGHI,x
        sta MGSCORE+1
        lda UNDOEGLO,x
        sta EGSCORE
        lda UNDOEGHI,x
        sta EGSCORE+1
        lda UNDOPHASE,x
        sta PHASE
        lda UNDOHALF,x
        sta HALFMOVE
        lda HASHSTK0,x
        sta HASH0
        lda HASHSTK1,x
        sta HASH1
        lda HASHSTK2,x
        sta HASH2
        lda HASHSTK3,x
        sta HASH3
        lda UNDOPSL,x
        sta PSTRUCT
        lda UNDOPSH,x
        sta PSTRUCT+1
.endif
        ; clear TO, put the original piece byte back on FROM
        ldy UNDOTO,x
        lda #0
        sta a:BOARD,y
        lda UNDOPIECE,x
        ldy UNDOFROM,x
        sta a:BOARD,y
        tay                     ; piece byte -> mover's slot
        lda SLOTTAB,y
        tay
        lda UNDOFROM,x
        sta PIECESQ,y
        ; castle: move the rook back
        lda UNDOFLAGS,x
        and #FL_CASTLE
        beq umnocastle
        jsr uncastlerook
umnocastle:
        ; restore any captured piece
        lda UNDOCAP,x
        beq umnocap
        ldy UNDOCAPSQ,x
        sta a:BOARD,y
        tay                     ; victim byte -> its slot
        lda SLOTTAB,y
        tay
        lda UNDOCAPSQ,x
        sta PIECESQ,y
umnocap:
.ifndef NOEVAL
        ; pawn-file bitmask maintenance: re-apply the same pbtoggle XOR
        ; toggles make applied (self-inverse), reconstructed from the
        ; undo record. Null moves never come through unmake, so
        ; UNDOPIECE,x here is always a real piece byte.
        lda UNDOPIECE,x
        and #$0F
        tay
        lda DIRTYTAB,y          ; bit 7: pawn (either color)
        bmi umpmover
umpvictim:
        lda UNDOCAP,x
        beq umpdone
        and #$0F
        tay
        lda DIRTYTAB,y
        bpl umpdone
        ldy UNDOCAPSQ,x         ; pawn victim: its bit returns
        lda UNDOCAP,x
        jsr pbtoggle
umpdone:
.endif
        rts
.ifndef NOEVAL
umpmover:
        ldy UNDOFROM,x          ; pawn mover: FROM bit returns
        lda UNDOPIECE,x
        jsr pbtoggle
        ldx PLY                 ; pbtoggle clobbered X
        lda UNDOFLAGS,x
        and #FL_PROMO           ; promotion: the pawn never occupied TO
        bne umpvictim
        ldy UNDOTO,x
        lda UNDOPIECE,x
        jsr pbtoggle
        ldx PLY
        jmp umpvictim
.endif
