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
        ; phase contribution. PDIRTY takes the DIRTYTAB encoding ($81
        ; pawn, $03 king, 0 otherwise — type-indexed low half of the
        ; nibble table): bit 7 = pawn placement changed, bit 0 = the
        ; per-make transient "THIS move dirtied" flag that make's tail
        ; tests and strips; PDIRTY&$FE == 2 means EXACTLY "king moved,
        ; no pawn placement changed" (the ptkonly dispatch key).
        and #TYPEMASK
        tax
        lda DIRTYTAB,x
        ora PDIRTY
        sta PDIRTY
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
; The toggles are inlined (r3), sharing one color-bit computation; the
; FROM toggle runs last so Y = FROM holds for the mvpbody contract.
; pbtoggle itself remains for the board.s callers (promotion make, ep
; capture, slow-path unmake records).
mvppawn:
        ora PDIRTY
        sta PDIRTY
        lda MVPIECE
        and #COLORMASK
        sta GTMP                ; color bit = PWBITS/PBBITS selector
        ldy TO
        tya
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        ldy FROM
        tya
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        lda MVPIECE             ; re-establish X for the psqt body
        and #$0F
        tax
        jmp mvpbody

movepiece:
        lda MVPIECE
        and #$0F
        tax
        lda DIRTYTAB,x          ; $81 pawn, $03 king (both colors), else 0
        bmi mvppawn             ; pawn: maintain the file bitmasks
        ora PDIRTY
        sta PDIRTY
mvpbody:
        ; contract: Y = FROM on entry (make and both prologues ensure it)
        lda TYPEPG0X,x          ; PSQT pages for this type
        sta PSP0+1
        lda TYPEPG1X,x
        sta PSP1+1
        ; hash: xor key[kind][FROM] ^ key[kind][TO], all four planes
        lda ZKHI0,x
        sta ZPTR+1
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
        ; (mvpsqt is also the tail of movepieceq. Entry contract: X =
        ; MVPIECE nibble — both entries establish it and neither hash
        ; body touches X — so the color bit comes from X, not a reload.)
mvpsqt:
        txa
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
; Y holds the capture square throughout (the inlined toggle keeps it).
tkppawn:
        ora PDIRTY
        sta PDIRTY
        lda VICTIM
        and #COLORMASK
        sta GTMP
        tya
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        dec NPAWNS              ; one fewer pawn on the board (every capture
                                ;  path funnels through here, ep included)
        lda VICTIM              ; re-establish X (nibble); Y unchanged
        and #$0F
        tax
        jmp tkphash             ; pawn victim: PHASEV16 is 0, skip the sub

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
        sbc PHASEV16,x          ; (pawn victims enter at tkphash instead:
        sta PHASE               ;  their PHASEV16 is 0 by construction)
tkphash:
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
        ; (tkpsqt is also the tail of takepieceq: X = victim nibble,
        ;  EVTMP = capture square)
tkpsqt:
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
; movepieceq / takepieceq: hash-elided variants for quiescence makes
; whose child provably never consumes the hash (deep opt r3; see the
; HVALID contract in defs.inc). Identical to movepiece/takepiece minus
; the Zobrist xors: psqt, PHASE, PDIRTY and the pawn-file bitmask
; maintenance still happen (they are position state, not hash state).
; movepieceq contract: Y = FROM on entry (unused, kept for symmetry).
; takepieceq: A = victim byte, Y = capture square, VICTIM set.
; ---------------------------------------------------------------
mvqpawn:
        ora PDIRTY
        sta PDIRTY
        lda MVPIECE
        and #COLORMASK
        sta GTMP
        ldy TO
        tya
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        ldy FROM
        tya
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        lda MVPIECE
        and #$0F
        tax
        jmp mvqbody
movepieceq:
        lda MVPIECE
        and #$0F
        tax
        lda DIRTYTAB,x
        bmi mvqpawn
        ora PDIRTY
        sta PDIRTY
mvqbody:
        lda TYPEPG0X,x          ; PSQT pages for this type
        sta PSP0+1
        lda TYPEPG1X,x
        sta PSP1+1
        jmp mvpsqt              ; shared psqt delta tail

tkqpawn:
        ora PDIRTY
        sta PDIRTY
        lda VICTIM
        and #COLORMASK
        sta GTMP
        tya
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        dec NPAWNS              ; (as tkppawn: the hash-elided qs twin)
        lda VICTIM
        and #$0F
        tax
        jmp tkpsqt              ; pawn victim: PHASEV16 is 0, skip the sub
takepieceq:
        sty EVTMP               ; capture square
        and #$0F
        tax
        lda DIRTYTAB,x
        bmi tkqpawn
        ora PDIRTY
        sta PDIRTY
tkqbody:
        lda PHASE
        sec
        sbc PHASEV16,x
        sta PHASE
        jmp tkpsqt              ; shared psqt removal tail

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

; hashcastle: xor CASTKEYS[A] into HASH0-3. Clobbers A,Y; X preserved.
hashcastle:
        asl
        asl
        tay
        lda CASTKEYS+0,y
        eor HASH0
        sta HASH0
        lda CASTKEYS+1,y
        eor HASH1
        sta HASH1
        lda CASTKEYS+2,y
        eor HASH2
        sta HASH2
        lda CASTKEYS+3,y
        eor HASH3
        sta HASH3
        rts

; hashep: xor EPKEYS[file of A] into HASH0-3 (A = ep square, not NOSQ).
; Clobbers A,Y; X preserved.
hashep:
        and #$07
        asl
        asl
        tay
        lda EPKEYS+0,y
        eor HASH0
        sta HASH0
        lda EPKEYS+1,y
        eor HASH1
        sta HASH1
        lda EPKEYS+2,y
        eor HASH2
        sta HASH2
        lda EPKEYS+3,y
        eor HASH3
        sta HASH3
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
        lda #$FF                ; masks rebuilt from scratch: every file's
        sta FDIRTY              ;  cached per-file term (PTFVAL) is stale
        lda #0
        sta NPAWNS              ; the pawn count is rebuilt by the same scan
                                ;  (make/unmake maintain it from here on)
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
        inc NPAWNS
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

; PTFILEC: one file's white+black terms, fully unrolled, accumulated
; into the single signed byte MULCNT (deep opt r4 integration: the
; 16-bit T0/T1 updates of the old PTFILE became 8-bit — a file's total
; is bounded by 12+7+52 = 71 in magnitude, so a signed byte always
; holds it exactly). All loads are absolute (PWBITS/PBBITS bytes and
; page-aligned lookup tables), boundary files drop the missing
; neighbor at assembly time, and the doubled test is a DBLTAB lookup
; (12 iff >= 2 bits set). Semantics term-exact with the old PTFILE
; (TestPStructParity); the caller folds MULCNT into T0/T1 with PTADD8,
; and two's-complement addition being order-independent makes the
; final 16-bit sum bit-identical to the old interleaved walk.
.macro PTFILEC f
        .local bterm, fdone
        ; ---- white terms ----
        ldy PWBITS+f            ; Y = own-file bits, kept for lookups
        beq bterm               ; no white pawns here
        lda DBLTAB,y            ; 12 iff doubled (>= 2 pawns)
        beq :+
        lda MULCNT
        sec
        sbc #12
        sta MULCNT
:
.if f = 0
        lda PWBITS+1            ; isolated: no own pawns on neighbors
.elseif f = 7
        lda PWBITS+6
.else
        lda PWBITS+(f-1)
        ora PWBITS+(f+1)
.endif
        bne :+
        lda MULCNT
        sec
        sbc #7
        sta MULCNT
:
.if f = 0
        lda PBBITS+0            ; black bits on files f-1..f+1
        ora PBBITS+1
.elseif f = 7
        lda PBBITS+6
        ora PBBITS+7
.else
        lda PBBITS+(f-1)
        ora PBBITS+f
        ora PBBITS+(f+1)
.endif
        and WBLOCKM,y           ; any black pawn at rank >= our best?
        bne bterm
        lda WPASSB,y            ; passed: bonus by the best pawn's rank
        clc
        adc MULCNT
        sta MULCNT
bterm:  ; ---- black terms, mirrored (advancement = low rank) ----
        ldy PBBITS+f
        beq fdone
        lda DBLTAB,y
        beq :+
        lda MULCNT
        clc
        adc #12
        sta MULCNT
:
.if f = 0
        lda PBBITS+1
.elseif f = 7
        lda PBBITS+6
.else
        lda PBBITS+(f-1)
        ora PBBITS+(f+1)
.endif
        bne :+
        lda MULCNT
        clc
        adc #7
        sta MULCNT
:
.if f = 0
        lda PWBITS+0
        ora PWBITS+1
.elseif f = 7
        lda PWBITS+6
        ora PWBITS+7
.else
        lda PWBITS+(f-1)
        ora PWBITS+f
        ora PWBITS+(f+1)
.endif
        and BBLOCKM,y
        bne fdone
        lda BPASSB,y            ; MULCNT -= bonus, via +(~bonus)+1
        eor #$FF
        sec
        adc MULCNT
        sta MULCNT
fdone:
.endmacro

; PTADD8: add the signed byte in A (whose N flag is still live from
; the load) into the T0/T1 16-bit accumulator, sign-extended. Uses
; only .local labels, so surrounding anonymous labels stay safe.
.macro PTADD8
        .local neg, done
        bmi neg
        clc                     ; positive: 16-bit add of 0:A
        adc T0
        sta T0
        bcc done
        inc T1
        bcs done                ; always (bcc above fell through: C=1,
                                ;  inc does not touch it)
neg:    clc                     ; negative: add $FF:A (sign extension)
        adc T0
        sta T0
        lda T1
        adc #$FF
        sta T1
done:
.endmacro

; SHIDX: king-shield index computation (hoisted from ptshieldw/b, deep
; optimization review r4). A = king file on entry; Y = 1..3 (shielded
; count) or 4..6 (4 + neighbor count, own file open) on exit. bits is
; PWBITS (white) or PBBITS (black). Clobbers A,X.
.macro SHIDX bits
        tax                     ; X = king file
        ldy #1                  ; assume own file shielded
        lda bits,x
        bne :+
        ldy #4                  ; open file under the king (count = 0)
:       cpx #0
        beq :+                  ; file a: no left neighbor
        lda bits-1,x
        beq :+
        iny
:       cpx #7
        beq :+                  ; file h: no right neighbor
        lda bits+1,x
        beq :+
        iny
:
.endmacro

; SHADD: add the signed byte tbl,y (sign-extended) into T0/T1.
.macro SHADD tbl
        ldx #0
        lda tbl,y
        bpl :+
        dex                     ; negative: sign-extend
:       clc
        adc T0
        sta T0
        txa
        adc T1
        sta T1
.endmacro

; ---------------------------------------------------------------
; ptkonly: king-only fast path (deep optimization review r4).
; Make's tail dispatches here when this make's dirt is EXACTLY the
; king pattern (post-lsr PDIRTY == 1, i.e. $03: king mover incl.
; castling, no pawn placement change — promotions and pawn victims
; all set bit 7) AND nothing deferred was inherited (a stale base
; takes the deferred pawntermfull path instead). The pawn masks,
; every per-file term, and the NON-moving side's shield are therefore
; unchanged, so
;   PSTRUCT' = PSTRUCT - shield_side(FROM) + shield_side(TO),
; each term gated by the same home-rank test as ptkings and negated
; via the existing SHLDB/SHLDW mirror tables (exact negations of each
; other). Reads FROM/TO/MVPIECE, still live at make's tail (search
; writes them pre-make; make never overwrites; castlerook uses
; GTMP/GTO). Sound because the dispatch guarantees PSTRUCT was
; current at make entry (PDIRTY==0 there). ~55-130 cycles vs the
; full recompute.
; ---------------------------------------------------------------
ptkonly:
        lda #0
        sta PDIRTY
        sta T0                  ; T0/T1 = signed shield delta
        sta T1
        lda MVPIECE
        and #COLORMASK
        beq ptkow
        jmp ptkob               ; (branch range: each side's block > 127b)
ptkow:  ; white king: shield only exists on rank 1 (sq & $70 == 0)
        ; (named gate labels: ":+" would bind inside the macro bodies)
        lda FROM
        and #$70
        bne ptkwto
        lda FROM
        and #$07
        SHIDX PWBITS
        SHADD SHLDB             ; - old shieldw (SHLDB[i] = -SHLDW[i])
ptkwto: lda TO
        and #$70
        bne ptkapply
        lda TO
        and #$07
        SHIDX PWBITS
        SHADD SHLDW             ; + new shieldw
ptkapply:
        clc
        lda T0
        adc PSTRUCT
        sta PSTRUCT
        lda T1
        adc PSTRUCT+1
        sta PSTRUCT+1
        rts
ptkob:  ; black king: shield only exists on rank 8 (sq & $70 == $70)
        lda FROM
        and #$70
        cmp #$70
        bne ptkbto
        lda FROM
        and #$07
        SHIDX PBBITS
        SHADD SHLDW             ; - old shieldb (SHLDW[i] = -SHLDB[i])
ptkbto: lda TO
        and #$70
        cmp #$70
        bne ptkapply2
        lda TO
        and #$07
        SHIDX PBBITS
        SHADD SHLDB             ; + new shieldb
ptkapply2:                      ; (duplicate of ptkapply: branch range)
        clc
        lda T0
        adc PSTRUCT
        sta PSTRUCT
        lda T1
        adc PSTRUCT+1
        sta PSTRUCT+1
        rts

; pawntermfull: full PSTRUCT recompute with the per-file cache (deep
; opt r4 integration). The old pawnterm dispatch entry is gone: make's
; tail dispatches to ptkonly/pawntermfull itself (board.s); evalinit
; and the PTNOCACHE oracle enter here directly as before.
; Only the files in SPREADTAB[FDIRTY] (changed files widened by one:
; a file's doubled/isolated/passed terms read files f-1..f+1 of both
; sides) are recomputed and re-cached in PTFVAL; every other file
; contributes its cached signed byte. The 16-bit T0/T1 sum is
; bit-identical to the old always-recompute walk (each file's value is
; exact in 8 bits and two's-complement addition commutes); the king
; shields are recomputed fresh as before.
pawntermfull:
        lda #0
        sta PDIRTY
        sta T0                  ; T0/T1: signed accumulator
        sta T1
.ifdef PTNOCACHE
        jsr ptbuild             ; oracle: fresh masks AND FDIRTY = $FF, so
                                ;  every file recomputes below - PSTRUCT
                                ;  stays independent of maintained state
.endif
        ldx FDIRTY
        lda SPREADTAB,x         ; dirty files + neighbors
        sta EVTMP               ; per-file dispatch shifter (lsr per file)
        lda #0
        sta FDIRTY
ptcf0:  lsr EVTMP
        bcc :+
        jmp ptrc0               ; dirty: recompute (bulk is out of range)
:       lda PTFVAL+0            ; clean: cached per-file value
        beq ptcf1               ; zero contribution (the common case)
        PTADD8
ptcf1:  lsr EVTMP
        bcc :+
        jmp ptrc1
:       lda PTFVAL+1
        beq ptcf2
        PTADD8
ptcf2:  lsr EVTMP
        bcc :+
        jmp ptrc2
:       lda PTFVAL+2
        beq ptcf3
        PTADD8
ptcf3:  lsr EVTMP
        bcc :+
        jmp ptrc3
:       lda PTFVAL+3
        beq ptcf4
        PTADD8
ptcf4:  lsr EVTMP
        bcc :+
        jmp ptrc4
:       lda PTFVAL+4
        beq ptcf5
        PTADD8
ptcf5:  lsr EVTMP
        bcc :+
        jmp ptrc5
:       lda PTFVAL+5
        beq ptcf6
        PTADD8
ptcf6:  lsr EVTMP
        bcc :+
        jmp ptrc6
:       lda PTFVAL+6
        beq ptcf7
        PTADD8
ptcf7:  lsr EVTMP
        bcc :+
        jmp ptrc7
:       lda PTFVAL+7
        beq ptkings
        PTADD8

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

; Per-file recompute blocks (out of line: each PTFILEC expansion is far
; past branch range from the consume walk above). MULCNT accumulates
; the 8-bit signed per-file value; it is re-cached in PTFVAL and folded
; into T0/T1 exactly like a clean file's cached byte.
.macro PTRECF f, back
        lda #0
        sta MULCNT
        PTFILEC f
        lda MULCNT
        sta PTFVAL+f
        beq :+
        PTADD8
:       jmp back
.endmacro
ptrc0:  PTRECF 0, ptcf1
ptrc1:  PTRECF 1, ptcf2
ptrc2:  PTRECF 2, ptcf3
ptrc3:  PTRECF 3, ptcf4
ptrc4:  PTRECF 4, ptcf5
ptrc5:  PTRECF 5, ptcf6
ptrc6:  PTRECF 6, ptcf7
ptrc7:  PTRECF 7, ptkings

; (doubled/isolated magnitudes — 12 and 7, Texel-tuned on the
; diversified corpus — are inlined in the PTFILE macro; DBLTAB gates
; the doubled hit.)

; ptshieldw/b: A = king file; +3 per file in {kf-1, kf, kf+1} holding
; own pawns (any rank), -4 when the king's own file is open. Count-
; then-lookup (deep optimization review r3): Y = count (1..3) when the
; own file is shielded, or 4 + neighbor count (4..6) when it is open;
; one signed SHLDW/SHLDB table add replaces the four jsr ptadda/ptsuba
; round trips. Clobbers A,X,Y; T0/T1 accumulator as before.
ptshieldw:
        SHIDX PWBITS
        ldx #0                  ; X = high byte of the signed term
        lda SHLDW,y
shtail: bpl :+
        dex                     ; negative: sign-extend
:       clc
        adc T0
        sta T0
        txa
        adc T1
        sta T1
        rts
ptshieldb:
        SHIDX PBBITS
        ldx #0
        lda SHLDB,y             ; sets N for shtail's sign test
        jmp shtail              ; (jmp preserves flags)

; shield term by Y index: 1-3 = shielded count, 4-6 = 4 + neighbor
; count with the own file open. value = 3*count - 4*open, white POV;
; SHLDB is the black-POV negation.
SHLDW:  .byte 0, 3, 6, 9, $FC, $FF, $02, 0
SHLDB:  .byte 0, $FD, $FA, $F7, $04, $01, $FE, 0

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
        jsr pawntermfull        ;  maintain them from here on (clears PDIRTY;
                                ;  full entry: FROM/MVPIECE mean nothing here)
        rts

; ---------------------------------------------------------------
; eval: SCORE = tapered eval from the side to move's point of view,
; including tempo. score_w = EG + ((MG-EG) * w) >> 5, w = PHASEWX[phase].
; Clobbers A,X,Y, T0-T1, MUL0-1, EVTMP, PSQSQ, MULCNT, PSQPIECE.
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
        jsr pawntermfull        ; full entry: no make context here
:
.endif
        ldx PHASE
        lda PHASEWX,x           ; A = w, 0..32 (cap at 24 baked in)
        ; fast paths (deep opt r4 restructure): w=32 (full middlegame)
        ; is pure MG, w=0 pure EG — no multiply, and the FT_PSTRUCT
        ; flat term is FUSED into the accumulator copy (one 16-bit add
        ; instead of copy-then-add; identical arithmetic). w=32 covers
        ; every opening/middlegame node. EVTMP (w) is only stored on
        ; the taper path, its only consumer.
        cmp #32
        bne evnotmg
        lda FEATURES
        and #FT_PSTRUCT
        beq evmgraw
        lda PDIRTY              ; lazy pawnterm (r4): consume a deferred
        beq :+                  ;  recompute before reading PSTRUCT
        jsr pawntermfull
:       clc
        lda MGSCORE
        adc PSTRUCT
        sta SCORE
        lda MGSCORE+1
        adc PSTRUCT+1
        sta SCORE+1
        jmp evrookx
evmgraw:
        lda MGSCORE
        sta SCORE
        lda MGSCORE+1
        sta SCORE+1
        jmp evrookx
evnotmg:
        sta EVTMP               ; w, 1..31 (or 0)
        cmp #0
        bne evtaper
        lda FEATURES
        and #FT_PSTRUCT
        beq evegraw
        lda PDIRTY              ; lazy pawnterm: consume before reading
        beq :+
        jsr pawntermfull
:       clc
        lda EGSCORE
        adc PSTRUCT
        sta SCORE
        lda EGSCORE+1
        adc PSTRUCT+1
        sta SCORE+1
        jmp evrookx
evegraw:
        lda EGSCORE
        sta SCORE
        lda EGSCORE+1
        sta SCORE+1
        jmp evrookx
evtaper:
        ; D = MG - EG, signed
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
        ; MUL1:MUL0 = (T1:T0 * w) >> 5 via quarter-square tables
        ; (deep optimization review r3): a*b = f(a+b) - f(a-b),
        ; f(i) = floor(i*i/4) — exact, a+b and a-b share parity.
        ; SQRLO/HI[i] = f(i), ISQLO/HI[i] = f(i-32); the operand low
        ; byte is self-modified to a magnitude byte and indexed with
        ; Y = w, X = 32-w, giving f(a+w) - f(a-w) = a*w. Recombined by
        ;   (D*w)>>5 = Dhi*w*8 + (Dlo*w)>>5
        ; (exact: Dhi*w*256 is a multiple of 32) with the page-aligned
        ; SHL3TAB/SHR5TAB shift tables. Safe for any |D| < $8000:
        ; Dhi*w < 4096 keeps QH below 16, so SHL3TAB[QH] drops no bits
        ; and the field ORAs never overlap; verified exhaustively for
        ; all (D, w) byte-for-byte against this recombination. Old
        ; shift-add loop cost 120-219 (worst at the heavy-bit w = 27-31
        ; that dominate QS); this is 153 flat, 85 when |D| < 256.
        ; Clobbers EVTMP/MULCNT/PSQPIECE as scratch.
evmul:  lda T0
        sta evsm1+1             ; SMC: table bases += Dlo
        sta evsm2+1
        sta evsm3+1
        sta evsm4+1
        ldy EVTMP               ; Y = w (1..31)
        lda #32
        sec
        sbc EVTMP
        tax                     ; X = 32-w
evsm1:  lda SQRLO,y             ; f(Dlo+w) lo
        sec
evsm2:  sbc ISQLO,x             ; - f(Dlo-w) lo
        sta EVTMP               ; PL (w now lives only in Y/X)
evsm3:  lda SQRHI,y
evsm4:  sbc ISQHI,x             ; PH = (Dlo*w) hi, < 31
        sta MULCNT
        lda T1
        beq evtlo               ; |D| < 256: no high-byte product
        sta evsm5+1             ; SMC: table bases += Dhi
        sta evsm6+1
        sta evsm7+1
        sta evsm8+1
evsm5:  lda SQRLO,y             ; f(Dhi+w) lo
        sec
evsm6:  sbc ISQLO,x             ; QL = (Dhi*w) lo
        sta PSQPIECE
evsm7:  lda SQRHI,y
evsm8:  sbc ISQHI,x             ; QH = (Dhi*w) hi, < 16
        tay
        ldx PSQPIECE
        ; MUL1:MUL0 = (QH:QL) << 3 = Dhi*w*8
        lda SHR5TAB,x
        ora SHL3TAB,y           ; disjoint fields: QH<<3 | QL>>5
        sta MUL1
        lda SHL3TAB,x
        sta MUL0
        ; += (PH:PL) >> 5, a single byte: PH<<3 | PL>>5, disjoint
        ldx MULCNT
        ldy EVTMP
        lda SHL3TAB,x
        ora SHR5TAB,y
        clc
        adc MUL0
        sta MUL0
        bcc evsgn
        inc MUL1
        bne evsgn               ; always: MUL1 <= 127 before the inc
evtlo:  sta MUL1                ; A = 0: product fits one byte
        ldx MULCNT
        ldy EVTMP
        lda SHL3TAB,x
        ora SHR5TAB,y
        sta MUL0
evsgn:  ; reapply sign
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
        ; pawn-structure/king-shield term (white POV) — taper path
        ; only; the w=32/w=0 fast paths fused it into their accumulator
        ; copy above. Lazy pawnterm: a deferred recompute is consumed
        ; here first (pawntermfull clobbers T0-T3/EVTMP/MULCNT/PSQ*,
        ; all dead at this point; SCORE/MUL are untouched).
        lda FEATURES
        and #FT_PSTRUCT
        beq evrookx
        lda PDIRTY
        beq :+
        jsr pawntermfull
:       clc
        lda SCORE
        adc PSTRUCT
        sta SCORE
        lda SCORE+1
        adc PSTRUCT+1
        sta SCORE+1
evrookx: ; FT2_MOPUP endgame mop-up term (white POV): drive the losing king
        ; to a corner + pull the winning king in, gated on low phase and a
        ; >= rook material edge. Added here — after pstruct, before
        ; the side-to-move negation — so its white-POV sign matches the other
        ; terms. mopupterm (TABLES tail) tests the PHASE gate itself and adds
        ; straight into SCORE; when the bit is clear it never runs and the
        ; eval instruction stream below is unchanged.
        lda FEATURES2
        and #FT2_MOPUP
        beq :+
        jsr mopupterm
:       ; side-to-move POV + tempo. Black fuses the negate with the
        ; tempo add: TEMPO - SCORE == (0 - SCORE) + TEMPO exactly in
        ; 16-bit two's complement (deep opt r4).
        lda SIDE
        beq evwtm
        sec
        lda #TEMPO
        sbc SCORE
        sta SCORE
        lda #0
        sbc SCORE+1
        sta SCORE+1
        jmp evseed
evwtm:  ; white tempo
        clc
        lda SCORE
        adc #TEMPO
        sta SCORE
        bcc evseed
        inc SCORE+1
evseed: ; dither: 0-3cp of seeded noise breaks deterministic move
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
