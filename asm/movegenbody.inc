; Shared generate/generateq body, assembled twice with ::QMODE 0/1
; (see movegen.s). Restructured for cycle economy:
;
;   - piece dispatch: compare chain with the pawn case a short branch
;     away (8 of 16 slots are pawns); no GPIECE store (pawn color comes
;     from SIDE, and the king handler owns its castle tail directly)
;   - sliders and steppers are fully unrolled per direction with
;     immediate deltas: no direction table fetch, no GDIR/GCOUNT/CURPTR
;     bookkeeping. The running square stays in A/X and the board byte
;     is read into A (full copy) or Y (qs copy).
;   - slider ray adds keep a carry invariant instead of clc per step:
;     a positive delta can never carry out of an on-board add (max
;     $77+$21 = $98 < $100), and a negative delta always has carry set
;     after an on-board result (no borrow), so C stays correct with the
;     delta pre-decremented (sec once + adc #delta-1). The batched
;     quiet emit (EMITQY below) touches no flags except N/Z, so the
;     invariant survives quiet emission with no re-establishment.
;   - BATCHED QUIET EMISSION (deep optimization review round 4): the
;     full-width copy keeps a running move-stack byte offset in Y and
;     writes quiet moves inline via sta (MSP),y - no jsr/rts, no
;     per-emit MSP bump, and the quiet tier is an immediate (TIER_QUIET
;     = TIERTAB[0]). MSP catches up at flush points: before any classic
;     jsr emit (captures, promotions, ep, castling) and at handler end
;     (FLUSHY / FLUSHEND in movegen.s). The bytes written and their
;     order are identical to the old per-emit path - only WHEN MSP
;     advances changes, which nothing observes mid-generation. The
;     overflow trap now fires at flush granularity; see the guard-slack
;     note at MOVESTACKTOP in defs.inc.
;   - vertical rays (+/-$10) never change file bits, so off-board is
;     just "bit 7 set" (bmi) - no and #$88.
;   - pawns: the quiescence copy tests the promotion rank BEFORE
;     probing the push square (only promo pushes are emitted in qs),
;     and both copies inline the two capture probes with cpx-based
;     promotion (cpx #$70 / cpx #$08) and en-passant (cpx EPSQ) tests.
;   - promoloop / emitmovep live in movegen.s (shared by both copies
;     and genrecap).
;
; Emission ORDER is bit-for-bit identical to the classic table loops:
; slots ascending, directions in KNIGHTOFF/KINGOFF/DIAGOFF/ORTHOOFF
; table order, pushes before captures ($0F before $11, -$0F before
; -$11), promotions N,B,R,Q, castling kingside before queenside after
; the king steps. Search move ordering breaks score ties by stack
; position, so this order IS the search tree shape: do not reorder.
;
; Register conventions inside the handlers:
;   GFROM = current from square; X = candidate target square.
;   Full copy (QMODE = 0): Y = pending emit offset from MSP (batched
;     quiets); the board byte is read into A. Classic emits (captures
;     etc.) FLUSH Y into MSP first, call with X=to Y=victim, and
;     re-zero Y after.
;   QS copy (QMODE = 1): no quiet emissions exist, so no batching:
;     Y = board byte / victim, emit via jsr emitmove (X=to, Y=victim).
; emitmove/emitmovep/emitmovef and EMITQY all preserve X (the
; pawn/promo/genrecap paths rely on it) and clobber A.

.if .not .definedmacro(GSLIDEV)

; EMITQY - batched quiet emit (full copy only): writes (TIER_QUIET,
; GFROM, X, 0) at (MSP),y .. (MSP),y+3 and advances Y by 4. Preserves
; X and the carry flag (only lda/sta/iny/txa - no arithmetic), so
; slider ray carry invariants survive. MSP itself is bumped later at a
; flush point (FLUSHY/FLUSHEND).
.macro EMITQY
        lda #TIER_QUIET
        sta (MSP),y             ; tier
        iny
        lda GFROM
        sta (MSP),y             ; from
        iny
        txa
        sta (MSP),y             ; to (X preserved)
        iny
        lda #0
        sta (MSP),y             ; flags = 0
        iny
.endmacro

; EMITFY flags - batched flagged emit with a QUIET tier (full copy:
; double pushes). Same shape as EMITQY with an immediate flags byte.
.macro EMITFY flags
        lda #TIER_QUIET
        sta (MSP),y             ; tier (double push: victim 0 -> quiet)
        iny
        lda GFROM
        sta (MSP),y             ; from
        iny
        txa
        sta (MSP),y             ; to (X preserved)
        iny
        lda #flags
        sta (MSP),y             ; flags
        iny
.endmacro

; FLUSHY - fold the pending emit offset into MSP and re-zero Y (ready
; for more batched emission). Page-cross/overflow handling in the
; shared flushpage helper (movegen.s). Clobbers A and carry. No-op
; when Y = 0.
.macro FLUSHY
        .local nopage, done
        tya
        beq done
        clc
        adc MSP
        sta MSP
        bcc nopage
        jsr flushpage
nopage: ldy #0
done:
.endmacro

; FLUSHEND - flush variant for handler exits (no further batched
; emission follows, so Y is left dirty).
.macro FLUSHEND
        .local done
        tya
        beq done
        clc
        adc MSP
        sta MSP
        bcc done
        jsr flushpage
done:
.endmacro

; GSLIDEV delta - vertical slider ray (delta = +/-$10). File bits never
; change on a vertical ray, so off-board is exactly "bit 7 set" and the
; and #$88 mask is unnecessary; A keeps the running square across the
; qs-copy read (board byte to Y); the full copy reads the board byte
; into A and restores the square with txa after a quiet emission.
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
.if ::QMODE = 0
        lda BOARD,x
        bne occ
        EMITQY                  ; batched quiet; preserves X and carry
        txa
        jmp walk
occ:    eor SIDE                ; enemy iff color bit differs
        and #COLORMASK
        beq next                ; own piece: stop
        FLUSHY                  ; MSP catches up before the classic emit
        ldy BOARD,x
        jsr emitmove            ; X=to, Y=victim; capture, then stop
        ldy #0                  ; offset restarts for the next ray
.else
        ldy BOARD,x
        bne occ
        jmp walk                ; A still holds the running square
occ:    tya
        eor SIDE
        and #COLORMASK
        beq next
        jsr emitmove            ; X=to, Y=victim; capture, then stop
.endif
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
.if ::QMODE = 0
        lda BOARD,x
        bne occ
        EMITQY                  ; batched quiet; preserves X and carry
        txa
        jmp walk
occ:    eor SIDE
        and #COLORMASK
        beq next
        FLUSHY
        ldy BOARD,x
        jsr emitmove            ; X=to, Y=victim; capture, then stop
        ldy #0
.else
        ldy BOARD,x
        bne occ
        txa
        jmp walk
occ:    tya
        eor SIDE
        and #COLORMASK
        beq next
        jsr emitmove            ; X=to, Y=victim; capture, then stop
.endif
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
.if ::QMODE = 0
        lda BOARD,x
        bne occ
        EMITQY                  ; batched quiet; preserves X
        jmp next
occ:    eor SIDE
        and #COLORMASK
        beq next                ; own piece
        FLUSHY
        ldy BOARD,x
        jsr emitmove            ; X=to, Y=victim
        ldy #0
.else
        ldy BOARD,x
        beq next                ; empty: nothing in qs
        tya
        eor SIDE
        and #COLORMASK
        beq next                ; own piece
        jsr emitmove            ; X=to, Y=victim; capture
.endif
next:
.endmacro

; WPAWNCAP off - one white-pawn capture probe (SIDE known 0 here, so
; the enemy test is just the color bit of the board byte). Promotion
; iff target rank 8 (cpx #$70: on-board targets are <= $77). Ep only
; onto an empty square (cpx EPSQ; EPSQ=NOSQ=$FF never matches an
; on-board X).
.macro WPAWNCAP off
        .local occ, empty, promo, next
        lda GFROM
        clc
        adc #off
        tax
        and #$88
        bne next                ; off board
.if ::QMODE = 0
        lda BOARD,x
        beq empty
        and #COLORMASK
        beq next                ; own piece (white: color bit clear)
        cpx #$70
        bcs promo               ; capture onto rank 8
        FLUSHY
        ldy BOARD,x
        jsr emitmove            ; X=to, Y=victim
        ldy #0
        jmp next
promo:  FLUSHY
        jsr promoloop           ; X=to
        ldy #0
        jmp next
empty:  cpx EPSQ
        bne next
        FLUSHY
        lda #FL_EP
        jsr emitmovef           ; X=to, ep
        ldy #0
.else
        lda BOARD,x
        beq empty
        and #COLORMASK
        beq next                ; own piece (white: color bit clear)
        cpx #$70
        bcs promo               ; capture onto rank 8
        ldy BOARD,x             ; Y = victim (capture path only)
        jsr emitmove            ; X=to, Y=victim
        jmp next
promo:  jsr promoloop           ; X=to
        jmp next
empty:  cpx EPSQ
        bne next
        lda #FL_EP
        jsr emitmovef           ; X=to, ep
.endif
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
.if ::QMODE = 0
        lda BOARD,x
        beq empty
        and #COLORMASK
        bne next                ; own piece (black: color bit set)
        cpx #$08
        bcc promo               ; capture onto rank 1
        FLUSHY
        ldy BOARD,x
        jsr emitmove            ; X=to, Y=victim
        ldy #0
        jmp next
promo:  FLUSHY
        jsr promoloop           ; X=to
        ldy #0
        jmp next
empty:  cpx EPSQ
        bne next
        FLUSHY
        lda #FL_EP
        jsr emitmovef           ; X=to, ep
        ldy #0
.else
        lda BOARD,x
        beq empty
        and #COLORMASK
        bne next                ; own piece (black: color bit set)
        cpx #$08
        bcc promo               ; capture onto rank 1
        ldy BOARD,x             ; Y = victim (capture path only)
        jsr emitmove            ; X=to, Y=victim
        jmp next
promo:  jsr promoloop           ; X=to
        jmp next
empty:  cpx EPSQ
        bne next
        lda #FL_EP
        jsr emitmovef           ; X=to, ep
.endif
next:
.endmacro

.endif  ; macro guard

.if ::QMODE = 0
        ; FUTILE full-width nodes (search.s sngen/srdefer): p2done jumps
        ; straight past the quiet passes at a futile node, so quiet
        ; emission (batched ray quiets, pawn pushes, castling and its
        ; attacked() probes) is pure waste there. The caller arms GENFUT
        ; immediately before the call; consume it and take the
        ; captures-only copy. RECAPONLY is cleared so generateq's own
        ; dispatch falls through to the full capture surface (it can
        ; hold a stale 1 from a deep qs node in another subtree).
        ; Entering through `generate` (instead of dispatching at the
        ; call sites) keeps every probe and profile count on the
        ; `generate` label honest — only `attacked` moves, by exactly
        ; the castle-safety probes the futile nodes no longer run.
        lda GENFUT
        beq :+
        lda #0
        sta GENFUT
        sta RECAPONLY
        jmp generateq
:
.endif
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
        ; GLIMIT = slot-half base + $10 bounds the slot walk (replaces
        ; the old per-slot and #$0F wrap test).
        lda SIDE
        asl
        sta GSLOT
        tay
        adc #$10                ; C=0 from the asl (SIDE is $00/$08)
        sta GLIMIT
        ldx PIECESQ,y
        cpx #NOSQ
        beq gennext             ; no king on the board: skip slot 0
        stx GFROM
        jmp genking
gendone:
        rts
gennext:
        ldy GSLOT
        iny
        sty GSLOT
        cpy GLIMIT
        beq gendone
genloop:
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
        ldy #0                  ; batched emit offset
        lda GFROM
        clc
        adc #$10
        tax
        lda BOARD,x             ; push square (always on board: no
                                ;  white pawn sits on rank 8)
        bne gwcaps              ; blocked: no pushes
        lda GFROM
        and #$F0
        cmp #$60
        beq gwpromopush
        EMITQY                  ; single push (batched quiet)
        ; double push from rank 2
        lda GFROM
        and #$F0
        cmp #$10
        bne gwcaps
        txa                     ; EMITQY preserved X = from+$10
        clc
        adc #$10
        tax
        lda BOARD,x
        bne gwcaps
        EMITFY FL_DOUBLE        ; batched (quiet tier, FL_DOUBLE flags)
        jmp gwcaps
gwpromopush:
        jsr promoloop           ; X=to(push sq); Y=0 here (no pending)
        ldy #0                  ; offset restarts for the capture probes
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
        jsr promoloop           ; X=to(push sq)
.endif
gwcaps: WPAWNCAP $0F
        WPAWNCAP $11
.if ::QMODE = 0
        FLUSHEND
.endif
        jmp gennext

genbpawn:
.if ::QMODE = 0
        ldy #0                  ; batched emit offset
        lda GFROM
        sec
        sbc #$10
        tax
        lda BOARD,x             ; no black pawn sits on rank 1
        bne gbcaps              ; blocked
        lda GFROM
        and #$F0
        cmp #$10
        beq gbpromopush
        EMITQY                  ; single push (batched quiet)
        ; double push from rank 7
        lda GFROM
        and #$F0
        cmp #$60
        bne gbcaps
        txa
        sec
        sbc #$10
        tax
        lda BOARD,x
        bne gbcaps
        EMITFY FL_DOUBLE
        jmp gbcaps
gbpromopush:
        jsr promoloop           ; X=to(push sq); Y=0 here (no pending)
        ldy #0
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
        jsr promoloop           ; X=to(push sq)
.endif
gbcaps: BPAWNCAP $F1            ; -$0F
        BPAWNCAP $EF            ; -$11
.if ::QMODE = 0
        FLUSHEND
.endif
        jmp gennext

; ---- knights / kings: 8 unrolled steps in table order ----
genknight:
.if ::QMODE = 0
        ldy #0                  ; batched emit offset
.endif
        GSTEP $DF
        GSTEP $E1
        GSTEP $EE
        GSTEP $F2
        GSTEP $0E
        GSTEP $12
        GSTEP $1F
        GSTEP $21
.if ::QMODE = 0
        FLUSHEND
.endif
        jmp gennext

genking:
.if ::QMODE = 0
        ldy #0                  ; batched emit offset
.endif
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
        FLUSHEND                ; MSP correct before castle's classic emits
        jmp gencastle
.else
        jmp gennext
.endif

; ---- sliders: unrolled rays in table order ----
genbishop:
.if ::QMODE = 0
        ldy #0                  ; batched emit offset (persists across rays)
.endif
        GSLIDED $EF
        GSLIDED $F1
        GSLIDED $0F
        GSLIDED $11
.if ::QMODE = 0
        FLUSHEND
.endif
        jmp gennext

genrook:
.if ::QMODE = 0
        ldy #0
.endif
        GSLIDEV $F0
        GSLIDED $FF
        GSLIDED $01
        GSLIDEV $10
.if ::QMODE = 0
        FLUSHEND
.endif
        jmp gennext

genqueen:                       ; KINGOFF order, as the classic loop
.if ::QMODE = 0
        ldy #0
.endif
        GSLIDED $EF
        GSLIDEV $F0
        GSLIDED $F1
        GSLIDED $FF
        GSLIDED $01
        GSLIDED $0F
        GSLIDEV $10
        GSLIDED $11
.if ::QMODE = 0
        FLUSHEND
.endif
        jmp gennext

.if ::QMODE = 0
; ---- castling ----
; Emits if: rights bit set, between-squares empty, king square and the
; square the king passes through not attacked by the enemy. Reached
; with MSP flushed (FLUSHEND above), so the classic emitmovef path is
; correct; emitmovef ignores Y (castle tier is the quiet constant).
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
        ldx #$06                ; to = g1
        lda #FL_CASTLE
        jsr emitmovef
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
        ldx #$02                ; to = c1
        lda #FL_CASTLE
        jsr emitmovef
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
        ldx #$76                ; to = g8
        lda #FL_CASTLE
        jsr emitmovef
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
        ldx #$72                ; to = c8
        lda #FL_CASTLE
        jsr emitmovef
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
; TYPEATK2 piece test -> slider ray walk. Deep opt round 4: the 16-slot
; scan is UNROLLED per side (white slots 0-15, black slots 16-31), so
; there is no loop counter, no SMC operand patching, and no per-slot
; inx/cpx/bne. The diff's +1 rides a carry-SET invariant instead of the
; old carry-clear one: ATT78 holds RECAPSQ+$77 here and every live
; slot's adc leaves C=1 (A = $FF-from >= $88 and ATT78 >= $77, so the
; sum always carries); tombstones branch before the adc; grproc
; restores C=1 before rts. GRSLOT candidates dispatch to the shared
; grproc, which re-checks the piece type and walks slider rays.
;
; GRSLOT n - one unrolled slot probe. diff = RECAPSQ - PIECESQ[n] + $77
; via complement + add (see above); ATTACKTAB gates the geometric
; relation, then grproc does the real work with A = from-square.
.macro GRSLOT n
        .local next
        lda PIECESQ+n
        eor #$FF                ; $FF-from; NOSQ tombstone -> 0
        beq next                ; tombstone (C unchanged: still set)
        adc ATT78               ; + RECAPSQ+$77 + C(=1) -> diff; C=1 out
        tay
        lda ATTACKTAB,y
        beq next                ; no geometric relation (hot exit)
        sta ATBITS
        sty DIFF
        lda PIECESQ+n           ; A = from-square for grproc
        jsr grproc              ; emits if real; returns with C=1
next:
.endmacro

genrecap:
        ldx RECAPSQ
        lda BOARD,x             ; RECAPSQ is a move destination: on-board
        beq grret               ; empty: nothing to recapture (see above)
        eor SIDE
        and #COLORMASK
        bne grgo                ; enemy piece there: candidates exist
grret:  rts                     ; empty/own piece: nothing to capture
grgo:   lda RECAPSQ
        sta GTO                 ; kept only for the grppawn promo-rank test
                                ;  (emit takes to=RECAPSQ in X directly)
        clc
        adc #$77                ; ATT78 = RECAPSQ+$77 here (the +1 comes
        sta ATT78               ;  from the carry-set invariant; attacked()
                                ;  keeps its own +$78 value, re-derived on
                                ;  every call - no conflict)
        sec                     ; C=1 invariant for the GRSLOT adds
        lda SIDE
        beq :+
        jmp grblack
:       GRSLOT 0
        GRSLOT 1
        GRSLOT 2
        GRSLOT 3
        GRSLOT 4
        GRSLOT 5
        GRSLOT 6
        GRSLOT 7
        GRSLOT 8
        GRSLOT 9
        GRSLOT 10
        GRSLOT 11
        GRSLOT 12
        GRSLOT 13
        GRSLOT 14
        GRSLOT 15
grout:  rts
grblack:
        GRSLOT 16
        GRSLOT 17
        GRSLOT 18
        GRSLOT 19
        GRSLOT 20
        GRSLOT 21
        GRSLOT 22
        GRSLOT 23
        GRSLOT 24
        GRSLOT 25
        GRSLOT 26
        GRSLOT 27
        GRSLOT 28
        GRSLOT 29
        GRSLOT 30
        GRSLOT 31
        rts

; grproc: candidate slot passed the ATTACKTAB gate. A = from-square,
; ATBITS/DIFF set. Verify the piece attacks along this diff (TYPEATK2),
; walk the ray for sliders, and emit the capture onto RECAPSQ (promo
; quartet for a pawn reaching its last rank). Returns with C=1 (the
; slot-scan invariant). Clobbers A, X, Y.
grproc:
        sta GFROM
        tay
        lda a:BOARD,y
        and #TYPEMASK|COLORMASK
        tay
        lda TYPEATK2,y          ; piece's attack bit (pawns by color)
        and ATBITS
        beq grpout              ; wrong piece for this diff, incl.
                                ;  wrong-direction pawns
        cmp #ATK_DIAG
        bcc grphit              ; $01/$02: knight/king, no ray to walk
        cmp #ATK_WPAWN
        bcs grppawn             ; $10/$20: pawn, adjacent (promo check)
        ; slider: walk the ray from attacker toward RECAPSQ
        ldy DIFF
        lda DELTATAB,y
        sta ATDELTA
        lda GFROM
grpwalk:clc
        adc ATDELTA
        cmp RECAPSQ
        beq grphit
        tay
        ; UNMASKED board read: safe only because this ray walks BETWEEN two
        ; on-board squares and exits at RECAPSQ before leaving the board (every
        ; square colinear between two on-board squares is on-board). See the
        ; off-board dead-space contract at BOARD in defs.inc.
        lda a:BOARD,y
        bne grpout              ; blocked
        tya
        jmp grpwalk
grphit: ldx RECAPSQ             ; X = to
        ldy BOARD,x             ; Y = victim byte (RECAPSQ is on-board, occupied)
        jsr emitmove            ; X=to, Y=victim
grpout: sec                     ; restore the slot-scan carry invariant
        rts
grppawn:; pawn capture onto RECAPSQ; promotes if that is its last rank
        and #ATK_WPAWN          ; A = the matched pawn bit ($10/$20)
        beq grpbp
        lda GTO                 ; white pawn: promotes on rank 8 ($7x)
        and #$F0
        cmp #$70
        beq grppromo
        bne grphit              ; always (A != $70)
grpbp:  lda GTO                 ; black pawn: promotes on rank 1 ($0x)
        and #$F0
        beq grppromo
        bne grphit              ; always (A != 0)
grppromo:
        ldx RECAPSQ             ; X = to for promoloop
        jsr promoloop           ; N,B,R,Q - as the full generator
        sec                     ; restore the slot-scan carry invariant
        rts
.endif
