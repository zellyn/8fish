; Evaluation: incremental tapered PeSTO piece-square tables + tempo.
; Accumulators (MGSCORE/EGSCORE, white POV, and PHASE) are updated by
; make via addpiece/rempiece and restored wholesale by unmake.
;
; Table layout (see cmd/gentables): per type t, a 512-byte block at
; PSQTBASE+(t-1)*512: MGLO[sq] +0, MGHI +128, EGLO +256, EGHI +384.
; TYPEPAGE0/1[type] give the two page-hi bytes; PSP0/PSP1 lo bytes stay 0.
; Black pieces use sq^$70 and contribute with opposite sign.

; ---------------------------------------------------------------
; addpiece / rempiece: A = piece byte, Y = 0x88 square.
; Updates MGSCORE/EGSCORE/PHASE. Clobbers A,X,Y, T0-T3, EVTMP,
; PSQPIECE, PSQSQ, MULCNT.
; ---------------------------------------------------------------
addpiece:
        ldx #0
        beq psqcom              ; always
rempiece:
        ldx #1
psqcom: sta PSQPIECE
        sty PSQSQ
        stx EVTMP               ; 0 = add piece, 1 = remove piece
        ; phase contribution
        and #TYPEMASK
        tax
        cpx #PAWN               ; pawn/king changes invalidate PSTRUCT
        beq :+
        cpx #KING
        bne :++
:       stx PDIRTY              ; (nonzero type byte)
:
        lda PHASEVAL,x
        beq psqnoph
        sta MULCNT
        lda EVTMP
        bne psqphsub
        lda PHASE
        clc
        adc MULCNT
        sta PHASE
        jmp psqnoph
psqphsub:
        lda PHASE
        sec
        sbc MULCNT
        sta PHASE
psqnoph:
        ; table pointers for this type
        lda TYPEPAGE0,x
        sta PSP0+1
        lda TYPEPAGE1,x
        sta PSP1+1
        ; black: flip rank, flip sign
        lda PSQPIECE
        and #COLORMASK
        beq psqwhite
        lda PSQSQ
        eor #$70
        sta PSQSQ
        lda EVTMP
        eor #1
        sta EVTMP
psqwhite:
        ; fetch mg/eg values
        ldy PSQSQ
        lda (PSP0),y
        sta T0
        lda (PSP1),y
        sta T2
        tya
        ora #$80
        tay
        lda (PSP0),y
        sta T1
        lda (PSP1),y
        sta T3
        ; apply: EVTMP 0 = add to white score, 1 = subtract
        lda EVTMP
        bne psqsub
        clc
        lda MGSCORE
        adc T0
        sta MGSCORE
        lda MGSCORE+1
        adc T1
        sta MGSCORE+1
        clc
        lda EGSCORE
        adc T2
        sta EGSCORE
        lda EGSCORE+1
        adc T3
        sta EGSCORE+1
        rts
psqsub: sec
        lda MGSCORE
        sbc T0
        sta MGSCORE
        lda MGSCORE+1
        sbc T1
        sta MGSCORE+1
        sec
        lda EGSCORE
        sbc T2
        sta EGSCORE
        lda EGSCORE+1
        sbc T3
        sta EGSCORE+1
        rts

; ---------------------------------------------------------------
; hashpiece: xor the Zobrist key for (A = piece byte, Y = square) into
; HASH0-3. Y is preserved. Clobbers A,X, ZPTR+1.
; Keys are kind-major (ZKEYS + kind*512 = p0[128] p1[128] p2[128]
; p3[128]); ZPTR's lo byte is permanently 0 (evalinit invariant), so
; one page byte selects the block and Y|$80 reaches the odd planes.
; ---------------------------------------------------------------
hashpiece:
        and #$0F
        tax
        lda ZKHI0,x             ; kind block page
        sta ZPTR+1
        lda (ZPTR),y            ; +0:   plane0[sq]
        eor HASH0
        sta HASH0
        tya
        ora #$80
        tay
        lda (ZPTR),y            ; +128: plane1[sq]
        eor HASH1
        sta HASH1
        inc ZPTR+1
        lda (ZPTR),y            ; +384: plane3[sq]
        eor HASH3
        sta HASH3
        tya
        and #$7F
        tay                     ; Y restored = sq
        lda (ZPTR),y            ; +256: plane2[sq]
        eor HASH2
        sta HASH2
        rts

; ---------------------------------------------------------------
; movepiece: fused hash + psqt update for MVPIECE moving FROM -> TO
; with the piece byte unchanged (i.e. NOT promotions; make keeps the
; split path for those and for castlerook). PHASE is untouched — the
; remove/add of the same piece provably cancels. Clobbers A,X,Y,
; ZPTR+1, PSP0+1/PSP1+1, T0/T1. Requires the ZPTR/PSP0/PSP1 lo == 0
; evalinit invariant.
; ---------------------------------------------------------------
; movepiece pawn prologue (deep optimization review r2): keep the
; per-file pawn bitmasks current so pawnterm never rescans the piece
; list. XOR toggles; unmake re-applies the same toggles (self-inverse).
mvppawn:
        ora PDIRTY
        sta PDIRTY
        lda MVPIECE
        ldy FROM
        jsr pbtoggle
        lda MVPIECE
        ldy TO
        jsr pbtoggle
        lda MVPIECE             ; re-establish X for the psqt body
        and #$0F
        tax
        jmp mvpbody

movepiece:
        lda MVPIECE
        and #$0F
        tax
        lda DIRTYTAB,x          ; $81 pawn, 1 king (both colors), else 0
        bmi mvppawn             ; pawn: maintain the file bitmasks
        ora PDIRTY
        sta PDIRTY
mvpbody:
        lda TYPEPG0X,x          ; PSQT pages for this type
        sta PSP0+1
        lda TYPEPG1X,x
        sta PSP1+1
        ; hash: xor key[kind][FROM] ^ key[kind][TO], all four planes
        lda ZKHI0,x
        sta ZPTR+1
        ldy FROM
        lda (ZPTR),y            ; p0[from]
        eor HASH0
        sta HASH0
        ldy TO
        lda (ZPTR),y            ; p0[to]
        eor HASH0
        sta HASH0
        tya
        ora #$80
        tay
        lda (ZPTR),y            ; p1[to]
        eor HASH1
        sta HASH1
        lda FROM
        ora #$80
        tay
        lda (ZPTR),y            ; p1[from]
        eor HASH1
        sta HASH1
        inc ZPTR+1
        lda (ZPTR),y            ; p3[from] (block +384)
        eor HASH3
        sta HASH3
        lda TO
        ora #$80
        tay
        lda (ZPTR),y            ; p3[to]
        eor HASH3
        sta HASH3
        ldy TO
        lda (ZPTR),y            ; p2[to]  (block +256)
        eor HASH2
        sta HASH2
        ldy FROM
        lda (ZPTR),y            ; p2[from]
        eor HASH2
        sta HASH2
        ; psqt as a from/to delta straight into the accumulators.
        ; white: score += tbl[TO] - tbl[FROM]
        ; black: score += tbl[FROM^$70] - tbl[TO^$70]
        lda MVPIECE
        and #COLORMASK
        beq mpwh
        lda TO
        eor #$70
        sta T0                  ; subtract-square
        lda FROM
        eor #$70
        sta T1                  ; add-square
        jmp mpgo
mpwh:   lda FROM
        sta T0
        lda TO
        sta T1
mpgo:   ldy T1                  ; MG += mg[T1]
        clc
        lda MGSCORE
        adc (PSP0),y
        sta MGSCORE
        tya
        ora #$80
        tay                     ; (tya/ora/tay preserve carry)
        lda MGSCORE+1
        adc (PSP0),y
        sta MGSCORE+1
        ldy T0                  ; MG -= mg[T0]
        sec
        lda MGSCORE
        sbc (PSP0),y
        sta MGSCORE
        tya
        ora #$80
        tay
        lda MGSCORE+1
        sbc (PSP0),y
        sta MGSCORE+1
        ldy T1                  ; EG += eg[T1]
        clc
        lda EGSCORE
        adc (PSP1),y
        sta EGSCORE
        tya
        ora #$80
        tay
        lda EGSCORE+1
        adc (PSP1),y
        sta EGSCORE+1
        ldy T0                  ; EG -= eg[T0]
        sec
        lda EGSCORE
        sbc (PSP1),y
        sta EGSCORE
        tya
        ora #$80
        tay
        lda EGSCORE+1
        sbc (PSP1),y
        sta EGSCORE+1
        rts

; takepiece pawn prologue: a pawn was captured — toggle its file bit.
; Y still holds the capture square here; pbtoggle preserves it.
tkppawn:
        ora PDIRTY
        sta PDIRTY
        lda VICTIM
        jsr pbtoggle
        ldy EVTMP               ; re-establish Y (square) and X (nibble)
        lda VICTIM
        and #$0F
        tax
        jmp tkpbody

; ---------------------------------------------------------------
; takepiece: fused hash + phase + psqt removal of a captured piece.
; A = victim piece byte, Y = capture square. Clobbers A,X,Y, ZPTR+1,
; PSP0+1/PSP1+1, EVTMP. Same invariants as movepiece. Additionally
; requires VICTIM = the victim piece byte (make sets it just before
; the call) for the pawn-bitmask maintenance.
; ---------------------------------------------------------------
takepiece:
        sty EVTMP               ; capture square
        and #$0F
        tax
        lda DIRTYTAB,x
        bmi tkppawn             ; pawn victim: maintain the file bitmasks
        ora PDIRTY
        sta PDIRTY
tkpbody:
        lda PHASE
        sec
        sbc PHASEV16,x          ; 0 for pawns: no-op by construction
        sta PHASE
        ; hash: xor key[kind][sq], all four planes
        lda ZKHI0,x
        sta ZPTR+1
        lda (ZPTR),y            ; p0
        eor HASH0
        sta HASH0
        tya
        ora #$80
        tay
        lda (ZPTR),y            ; p1
        eor HASH1
        sta HASH1
        inc ZPTR+1
        lda (ZPTR),y            ; p3
        eor HASH3
        sta HASH3
        tya
        and #$7F
        tay
        lda (ZPTR),y            ; p2
        eor HASH2
        sta HASH2
        ; psqt: white victim: score -= tbl[sq]; black: += tbl[sq^$70]
        lda TYPEPG0X,x
        sta PSP0+1
        lda TYPEPG1X,x
        sta PSP1+1
        txa
        and #COLORMASK
        bne tpblack
        ldy EVTMP
        sec
        lda MGSCORE
        sbc (PSP0),y
        sta MGSCORE
        tya
        ora #$80
        tay
        lda MGSCORE+1
        sbc (PSP0),y
        sta MGSCORE+1
        ldy EVTMP
        sec
        lda EGSCORE
        sbc (PSP1),y
        sta EGSCORE
        tya
        ora #$80
        tay
        lda EGSCORE+1
        sbc (PSP1),y
        sta EGSCORE+1
        rts
tpblack:
        lda EVTMP
        eor #$70
        tay
        clc
        lda MGSCORE
        adc (PSP0),y
        sta MGSCORE
        tya
        ora #$80
        tay
        lda MGSCORE+1
        adc (PSP0),y
        sta MGSCORE+1
        lda EVTMP
        eor #$70
        tay
        clc
        lda EGSCORE
        adc (PSP1),y
        sta EGSCORE
        tya
        ora #$80
        tay
        lda EGSCORE+1
        adc (PSP1),y
        sta EGSCORE+1
        rts

; ---------------------------------------------------------------
; pbtoggle: toggle the per-file rank bit for a pawn. A = pawn piece
; byte (either color), Y = its 0x88 square. The color bit
; (COLORMASK = $08) equals PBBITS-PWBITS, so file|color indexes the
; joint 16-byte array directly. Clobbers A,X,GTMP; preserves Y.
; GTMP is movegen/castlerook scratch, dead at every pbtoggle call
; site (all inside make/unmake, after any castlerook use).
; ---------------------------------------------------------------
pbtoggle:
        and #COLORMASK
        sta GTMP
        tya
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y           ; 1 << rank
        eor PWBITS,x
        sta PWBITS,x
        rts

; hashcastle: xor CASTKEYS[A] into HASH0-3. Clobbers A,X,Y.
hashcastle:
        asl
        asl
        tay
        ldx #0
hcloop: lda CASTKEYS,y
        eor HASH0,x
        sta HASH0,x
        iny
        inx
        cpx #4
        bne hcloop
        rts

; hashep: xor EPKEYS[file of A] into HASH0-3 (A = ep square, not NOSQ).
hashep:
        and #$07
        asl
        asl
        tay
        ldx #0
heloop: lda EPKEYS,y
        eor HASH0,x
        sta HASH0,x
        iny
        inx
        cpx #4
        bne heloop
        rts

; hashstm (the side-to-move xor) is emitted unrolled by cmd/gentables,
; since only tables.s knows the key bytes at assembly time.

; ---------------------------------------------------------------
; pawnterm: recompute PSTRUCT (white POV): doubled/isolated/passed
; pawns and a minimal king shield. Called by make when PDIRTY is set
; (a pawn or king changed) and by evalinit.
;
; PWBITS/PBBITS at $0200-$020F are PERSISTENT state, not scratch:
; ptbuild fills them at evalinit and make/unmake keep them current
; via pbtoggle XOR toggles on every pawn placement change (mover in
; movepiece/the promotion path, victim in takepiece, all re-applied
; symmetrically by the unmake tail — XOR is self-inverse). pawnterm
; therefore reads them directly with no per-call scan. Nothing else
; may write $0200-$020F.
;
; Per file, one byte of rank-occupancy bits per side; the derived
; per-file terms come from gentables lookups on that byte (RANKBIT/
; WBLOCKM/WPASSB/BBLOCKM/BPASSB - the passed-bonus weights live in
; cmd/gentables now). Semantics are term-exact with the previous
; count/min/max implementation, gated by TestPStructParity:
; doubled = flat -12 for any count >= 2 (NOT per extra pawn);
; blocked uses >= for white and <= for black (adjacent same-rank
; enemy pawns block), so WBLOCKM includes the pawn's own rank bit.
; ---------------------------------------------------------------
PWBITS = $0200          ; white pawn rank-occupancy bits per file (8)
PBBITS = $0208          ; black pawn rank-occupancy bits per file (8)

; ptbuild: rebuild PWBITS/PBBITS from the piece lists. Called once by
; evalinit; after that make/unmake keep the masks current via pbtoggle
; (deep optimization review r2), so pawnterm itself no longer scans.
; The PTNOCACHE oracle build still rebuilds here on every pawnterm so
; its PSTRUCT is independent of the maintained state.
ptbuild:
        lda #0
        ldx #7
ptclr:  sta PWBITS,x
        sta PBBITS,x
        dex
        bpl ptclr
        ; scan the piece lists for pawns
        ldx #31
ptscan: lda PIECESQ,x
        cmp #NOSQ
        beq ptnext
        tay
        lda a:BOARD,y
        and #TYPEMASK
        cmp #PAWN
        bne ptnext
        lda RANKBIT,y           ; 1 << rank
        sta EVTMP
        tya
        and #$07
        tay                     ; Y = file
        lda EVTMP
        cpx #16
        bcs ptblack
        ora PWBITS,y
        sta PWBITS,y
        bcc ptnext              ; always (cpx cleared carry above;
                                ;  ora/sta leave it untouched)
ptblack:
        ora PBBITS,y
        sta PBBITS,y
ptnext: dex
        bpl ptscan
        rts

pawnterm:
        lda #0
        sta PDIRTY
        sta T0                  ; T0/T1: signed accumulator
        sta T1
.ifdef PTNOCACHE
        jsr ptbuild             ; oracle: fresh masks, ignore maintenance
.endif
        ; per-file terms (helpers preserve X and Y)
        ldx #7
ptfile: lda PWBITS,x
        beq ptwd0               ; no white pawns on this file
        tay                     ; Y = own-file bits, kept for lookups
        sec
        sbc #1
        and PWBITS,x
        beq :+                  ; bits & (bits-1): doubled iff nonzero
        jsr ptsub12
:       jsr ptneighw            ; isolated: no own pawns on neighbors
        bne :+
        jsr ptsub7
:       jsr ptorb3              ; A = black bits on files x-1..x+1
        and WBLOCKM,y           ; any black pawn at rank >= our best?
        bne ptwd0
        lda WPASSB,y            ; passed: bonus by the best pawn's rank
        jsr ptadda
ptwd0:  ; black side, mirrored (advancement = low rank)
        lda PBBITS,x
        beq ptnextf
        tay
        sec
        sbc #1
        and PBBITS,x
        beq :+
        jsr ptadd12
:       jsr ptneighb
        bne :+
        jsr ptadd7
:       jsr ptorw3
        and BBLOCKM,y
        bne ptnextf
        lda BPASSB,y
        jsr ptsuba
ptnextf:
        dex
        bmi ptkings
        jmp ptfile

ptkings:
        ; king shield: only for kings on their own back two ranks
        ldy PIECESQ+0           ; white king
        tya
        and #$70
        bne ptbk                ; not on rank 1 (shield only when home-ish)
        tya
        and #$07
        jsr ptshieldw
ptbk:   ldy PIECESQ+16          ; black king
        tya
        and #$70
        cmp #$70
        bne ptdone
        tya
        and #$07
        jsr ptshieldb
ptdone: lda T0
        sta PSTRUCT
        lda T1
        sta PSTRUCT+1
        rts

; helpers: T0/T1 16-bit signed accumulator ---------------------------
ptadda: clc                     ; A (unsigned small) added
        adc T0
        sta T0
        bcc :+
        inc T1
:       rts
ptsuba: sta MULCNT              ; NOT EVTMP: the king-shield loops keep
        sec                     ;  the king file there across calls
        lda T0
        sbc MULCNT
        sta T0
        bcs :+
        dec T1
:       rts
ptadd12:
        lda #12                 ; doubled
        bne ptadda              ; always
ptsub12:
        lda #12
        bne ptsuba              ; always
ptadd7:
        lda #7                  ; isolated (Texel-tuned, diversified corpus)
        bne ptadda              ; always
ptsub7:
        lda #7
        bne ptsuba              ; always

; ptneighw/b: Z set if both neighbor files have no own pawns.
; The final ora #0 is load-bearing: on the file-h path the last
; flag-setting op would otherwise be cpx #7 (Z set!), which mis-
; flagged h-file pawns as isolated in the count-based version.
ptneighw:
        lda #0
        cpx #0
        beq :+
        ora PWBITS-1,x
:       cpx #7
        beq :+
        ora PWBITS+1,x
:       ora #0
        rts
ptneighb:
        lda #0
        cpx #0
        beq :+
        ora PBBITS-1,x
:       cpx #7
        beq :+
        ora PBBITS+1,x
:       ora #0
        rts

; ptorb3/ptorw3: A = OR of that side's bits on files x-1, x, x+1
ptorb3:
        lda PBBITS,x
        cpx #0
        beq :+
        ora PBBITS-1,x
:       cpx #7
        beq :+
        ora PBBITS+1,x
:       rts
ptorw3:
        lda PWBITS,x
        cpx #0
        beq :+
        ora PWBITS-1,x
:       cpx #7
        beq :+
        ora PWBITS+1,x
:       rts

; ptshieldw/b: A = king file; +8 per shielded file, -10 for an open
; own file under the king. Clobbers Y, EVTMP.
ptshieldw:
        sta EVTMP
        tay
        lda PWBITS,y
        beq :+
        lda #3
        jsr ptadda
:       ldy EVTMP
        beq :+                  ; file a: no left neighbor
        lda PWBITS-1,y
        beq :+
        lda #3
        jsr ptadda
:       ldy EVTMP
        cpy #7
        beq :+
        lda PWBITS+1,y
        beq :+
        lda #3
        jsr ptadda
:       ldy EVTMP
        lda PWBITS,y
        bne :+
        lda #4                  ; open file under the king
        jsr ptsuba
:       rts
ptshieldb:
        sta EVTMP
        tay
        lda PBBITS,y
        beq :+
        lda #3
        jsr ptsuba
:       ldy EVTMP
        beq :+
        lda PBBITS-1,y
        beq :+
        lda #3
        jsr ptsuba
:       ldy EVTMP
        cpy #7
        beq :+
        lda PBBITS+1,y
        beq :+
        lda #3
        jsr ptsuba
:       ldy EVTMP
        lda PBBITS,y
        bne :+
        lda #4
        jsr ptadda
:       rts

; ---------------------------------------------------------------
; evalinit: recompute accumulators and the Zobrist hash from the board
; (root setup, and a debug cross-check against the incremental path).
; ---------------------------------------------------------------
evalinit:
        lda #0
        sta MGSCORE
        sta MGSCORE+1
        sta EGSCORE
        sta EGSCORE+1
        sta PHASE
        sta HASH0
        sta HASH1
        sta HASH2
        sta HASH3
        sta PSP0                ; pointer lo bytes are always 0
        sta PSP1
        sta ZPTR                ; (the loader used ZPTR as scratch)
        sta GSLOT
eviloop:
        ldy GSLOT
        lda PIECESQ,y
        cmp #NOSQ
        beq evinext
        tay
        lda a:BOARD,y
        jsr addpiece
        ldy GSLOT
        lda PIECESQ,y
        tay
        lda a:BOARD,y
        jsr hashpiece
evinext:
        inc GSLOT
        lda GSLOT
        cmp #32
        bne eviloop
        ; side to move, castling rights, ep square
        lda SIDE
        beq :+
        jsr hashstm
:       lda CASTLE
        jsr hashcastle
        lda EPSQ
        cmp #NOSQ
        beq :+
        jsr hashep
:       jsr ptbuild             ; initial pawn-file bitmasks; make/unmake
        jmp pawnterm            ;  maintain them from here on (clears PDIRTY)

; ---------------------------------------------------------------
; eval: SCORE = tapered eval from the side to move's point of view,
; including tempo. score_w = EG + ((MG-EG) * w) >> 5, w = PHASEW[phase].
; Clobbers A,X,Y, T0-T1, MUL0-1, EVTMP, PSQSQ.
; ---------------------------------------------------------------
eval:
.ifdef PTNOCACHE
        ; measurement baseline (task #47): recompute the pawn-structure
        ; term from scratch on every eval instead of reading the
        ; incrementally-maintained cache. Gated by FT_PSTRUCT to match
        ; the consumer below. pawnterm clobbers A/X/Y/T0-T3/EVTMP/MULCNT/
        ; PSQSQ and $0200-$020F scratch, all dead on entry to eval.
        lda FEATURES
        and #FT_PSTRUCT
        beq :+
        jsr pawnterm
:
.endif
        lda PHASE
        cmp #25
        bcc :+
        lda #24                 ; cap (early promotions can exceed 24)
:       tax
        lda PHASEW,x
        sta EVTMP               ; w, 0..32
        ; fast paths: w=32 (full middlegame) is pure MG, w=0 pure EG —
        ; no multiply needed. w=32 covers every opening/middlegame node.
        cmp #32
        bne :+
        lda MGSCORE
        sta SCORE
        lda MGSCORE+1
        sta SCORE+1
        jmp evpov
:       cmp #0
        bne :+
        lda EGSCORE
        sta SCORE
        lda EGSCORE+1
        sta SCORE+1
        jmp evpov
:       ; D = MG - EG, signed
        sec
        lda MGSCORE
        sbc EGSCORE
        sta T0
        lda MGSCORE+1
        sbc EGSCORE+1
        sta T1
        ; sign-magnitude for the multiply
        ldx #0
        lda T1
        bpl evpos
        ldx #1
        sec
        lda #0
        sbc T0
        sta T0
        lda #0
        sbc T1
        sta T1
evpos:  stx PSQSQ               ; sign flag (scratch reuse)
        ; MUL1:MUL0 = (T1:T0 * w) >> 5 via right-shift multiply.
        ; w is 1..31 here (w=32/w=0 fast-pathed above), so 5 iterations
        ; produce the >>5 for free: the shifted-out bits are exactly
        ; the product's low 5 bits. T1:T0 is a magnitude < $8000, so
        ; the accumulator never exceeds 17 bits (carry + 16).
        lda #0
        sta MUL0
        sta MUL1
        ldx #5
evmul:  lsr EVTMP
        bcc evskip              ; bit clear: carry clear into the ror
        clc                     ; carry is set here (from the lsr)
        lda MUL0
        adc T0
        sta MUL0
        lda MUL1
        adc T1
        sta MUL1                ; adc carry-out falls into the ror
evskip: ror MUL1
        ror MUL0
        dex
        bne evmul
        ; reapply sign
        lda PSQSQ
        beq evnosgn
        sec
        lda #0
        sbc MUL0
        sta MUL0
        lda #0
        sbc MUL1
        sta MUL1
evnosgn:
        ; white score = EG + product
        clc
        lda EGSCORE
        adc MUL0
        sta SCORE
        lda EGSCORE+1
        adc MUL1
        sta SCORE+1
evpov:  ; pawn-structure/king-shield term (white POV, kept current by
        ; make via PDIRTY)
        lda FEATURES
        and #FT_PSTRUCT
        beq :+
        clc
        lda SCORE
        adc PSTRUCT
        sta SCORE
        lda SCORE+1
        adc PSTRUCT+1
        sta SCORE+1
:       ; side-to-move POV
        lda SIDE
        beq evwtm
        sec
        lda #0
        sbc SCORE
        sta SCORE
        lda #0
        sbc SCORE+1
        sta SCORE+1
evwtm:  ; tempo
        clc
        lda SCORE
        adc #TEMPO
        sta SCORE
        bcc :+
        inc SCORE+1
:       ; dither: 0-3cp of seeded noise breaks deterministic move
        ; repetition (hardware seeds SEED from input timing; the bridge
        ; pokes a random byte; 0 = off, keeping tests reproducible)
        lda SEED
        beq evdone
        asl
        clc
        adc SEED                ; seed = seed*3 + 29
        adc #29
        sta SEED
        and #$03
        clc
        adc SCORE
        sta SCORE
        bcc evdone
        inc SCORE+1
evdone: rts
