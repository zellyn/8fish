; Board primitives: attacked, make, unmake. See defs.inc for layout.

; TYPEATKTAB[type] = attack-table bits that mean "this piece type attacks
; across this difference (geometrically)". Pawns are special-cased.
TYPEATKTAB:
        .byte 0, 0, ATK_KNIGHT, ATK_DIAG, ATK_ORTHO, ATK_DIAG|ATK_ORTHO, ATK_KING

; ---------------------------------------------------------------
; attacked: is ATSQ attacked by any piece of side ATSIDE (0/$08)?
; Out: carry set if attacked. Clobbers A,X,Y, ATP2, ATTMP/ATBITS/
; DIFF/ATDELTA (ATT78 is no longer touched; genrecap still uses it).
;
; Deep-opt round 4: reversed-attack-table slot filter. RATTACK (below)
; is ATTACKTAB with the difference axis reversed plus a zero tail:
;
;   RATTACK[($77 - ATSQ) + from] = ATTACKTAB[ATSQ - from + $77]
;   RATTACK[j] = 0 for j >= $EF  (from = NOSQ tombstones land here)
;
; so with ATP2 = RATTACK + $77 - ATSQ (page-aligned base: the low byte
; is just $77-ATSQ), the whole per-slot filter is
;
;         ldy PIECESQ+n         ; the slot's square, or NOSQ
;         lda (ATP2),y          ; attack bits for ATSQ-from+$77
;         beq next              ; no relation / tombstone: next slot
;
; - three instructions, no carry discipline, and tombstones need no
; separate test (they read the zero tail, +1 cycle for the page cross).
; Geometric relations (~2 per call) jsr the shared atgeo tail with
; Y = from-square, A = attack bits, X = ATSQ+$77; carry = "this slot
; attacks ATSQ".
;
; Scan order (measured on real operand distributions, task r4): white
; slots ascending 1..15 then king (slot 0) last, black descending
; 15..0. FEN slot assignment scans rank 8 downward, so white's LOW
; slots and black's HIGH slots hold its advanced pieces - the likely
; attackers. This "advance" order is within 0.8pp of every greedy
; frequency-fitted order ON HELD-OUT positions (fitted orders do not
; transfer between position sets) and is stable at -13.3%..-14.1%
; modeled cost vs the r3 scan across bench/held-out/corpus taps.
; ---------------------------------------------------------------

; One slot of the scan. base+slot is the PIECESQ entry; next is where
; both "no relation" and atgeo's miss return continue.
.macro ATSLOT base, slot, next
        ldy base+slot
        lda (ATP2),y
        beq next
        jsr atgeo
        bcc next
        rts
.endmacro

attacked:
        lda #$77
        sec
        sbc ATSQ                ; $77 - ATSQ (ATSQ <= $77: no borrow)
        sta ATP2
        lda #>RATTACK
        sta ATP2+1
        lda ATSQ
        adc #$76                ; carry still set: A = ATSQ + $77
        tax
        lda ATSIDE
        beq atwentry
        jmp atbentry            ; (black body is out of branch reach)
atwentry:
; ---- white body: slots 1..15, king (0) last ----
.repeat 15, i
.ident(.sprintf("atw%02d", i+1)):
        ATSLOT PIECESQ, i+1, .ident(.sprintf("atw%02d", (i+2)*((i+2)<16)))
.endrepeat
atw00:  ATSLOT PIECESQ, 0, atwmiss
atwmiss:
        clc
        rts
; ---- black body: slots 15..0 ----
atbentry:
.repeat 15, i
.ident(.sprintf("atb%02d", 15-i)):
        ATSLOT PIECESQ+16, 15-i, .ident(.sprintf("atb%02d", 14-i))
.endrepeat
atb00:  ATSLOT PIECESQ+16, 0, atbmiss
atbmiss:
        clc
        rts

; atgeo: shared geometric tail. In: A = attack-table bits (nonzero),
; Y = the candidate from-square, X = ATSQ+$77 (preserved). Out: carry
; set iff this slot's piece attacks ATSQ. Clobbers A,Y, ATTMP/ATBITS/
; DIFF/ATDELTA.
atgeo:  sta ATBITS
        sty ATTMP               ; from-square
        txa
        sec
        sbc ATTMP               ; diff = ATSQ - from + $77
        sta DIFF
        lda a:BOARD,y           ; Y = from-square
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
        ; (UNDOPHASE is no longer saved unconditionally: PHASE only
        ;  changes on captures and promotions, so the fast capture
        ;  branch and mkslow save it, and unmake restores it only on
        ;  those same paths - deep opt r4)
        lda HALFMOVE
        sta UNDOHALF,x
        ; (HASHSTK is no longer saved unconditionally here: each path
        ;  below saves it exactly when it keeps the hash - see HVALID.
        ;  UNDOPSL/H likewise moved to the PDIRTY-gated tail: PSTRUCT
        ;  can only change when a pawn or king event set PDIRTY, and
        ;  unmake mirrors that condition from the undo record - r4)
.endif
        ldy FROM
        lda a:BOARD,y           ; force absolute: no lda zp,y mode exists
        sta MVPIECE
        sta UNDOPIECE,x
        lda MVFLAGS
        beq mkfast              ; no flags: plain move or plain capture
        jmp mkslow              ; ep/double/promo/castle: rare path

; ---- fast path: MVFLAGS == 0 (plain quiet move or plain capture).
; Implicitly: capture square is TO, no promotion, no castle, and the
; new ep square is NOSQ. This covers the overwhelming share of makes.
; Hash elision (deep opt r3, see HVALID in defs.inc): a quiescence
; child (PLY+1 >= MAXDEPTH) with 50-move clock < 4 and not in check
; consumes no hash (no TT probe/store, no repetition scan), so those
; makes skip HASHSTK save + all Zobrist xors; the gives-check case is
; repaired by the deferred upgrade after ckdone.
; (deep opt r4 fast-path shaves: UNDOCAPSQ is written only by the
; capture branch - nothing reads it for quiet moves; the BOARD[TO]=0
; victim clear is dead - the mover placement in mkfmvon/mkfmvoff
; overwrites TO before any read, and takepiece/takepieceq never read
; the board; UNDOPHASE is saved here rather than in the prologue.)
mkfast:
        ldy TO
        lda a:BOARD,y           ; victim byte (0 if quiet move)
        sta UNDOCAP,x
        beq mkfquiet
        ; capture: remove victim, tombstone list slot
        sta VICTIM
        lda TO
        sta UNDOCAPSQ,x
.ifdef NOEVAL
        ldy VICTIM
        lda SLOTTAB,y
        tay
        lda #NOSQ
        sta PIECESQ,y
        jmp mkfmvoff
.else
        lda PHASE
        sta UNDOPHASE,x
        lda #0
        sta HALFMOVE            ; capture: 50-move clock resets (child<4)
        inx                     ; child ply = PLY+1: quiescence?
        cpx MAXDEPTH
        dex
        bcs mkfcapoff           ; qs capture: hash provably unconsumed
        ; hash-on capture: save the pre-move hash, advance the watermark.
        ; The side-to-move xor is fused into the save (HASHSTK gets the
        ; pre-move hash, HASH gets the stm flip; all later hash updates
        ; are commutative xors), replacing the old jsr hashstm.
        lda HASH0
        sta HASHSTK0,x
        eor STMKEY+0
        sta HASH0
        lda HASH1
        sta HASHSTK1,x
        eor STMKEY+1
        sta HASH1
        lda HASH2
        sta HASHSTK2,x
        eor STMKEY+2
        sta HASH2
        lda HASH3
        sta HASHSTK3,x
        eor STMKEY+3
        sta HASH3
        inx
        stx HVALID
        lda VICTIM              ; Y = TO (capture square) throughout
        jsr takepiece           ; fused hash+phase+psqt
        ldy VICTIM
        lda SLOTTAB,y
        tay
        lda #NOSQ
        sta PIECESQ,y
        jmp mkfmvon
mkfcapoff:
        lda VICTIM              ; Y = TO (capture square)
        jsr takepieceq          ; phase+psqt+pawn-bits only
        ldy VICTIM
        lda SLOTTAB,y
        tay
        lda #NOSQ
        sta PIECESQ,y
        jmp mkfmvoff
.endif
mkfquiet:
.ifndef NOEVAL
        ; quiet: a pawn push resets the 50-move clock, else it ticks
        lda MVPIECE
        and #TYPEMASK
        cmp #PAWN
        bne mkfqnp
        lda #0
        sta HALFMOVE            ; pawn push: child clock = 0
        inx
        cpx MAXDEPTH
        dex
        bcc mkfqon              ; full-width child: hash on
        jmp mkfmvoff            ; qs pawn push: hash off
mkfqnp: inc HALFMOVE            ; bounded: root value + MAXPLY < 256
        inx
        cpx MAXDEPTH
        dex
        bcc mkfqon              ; full-width child: hash on
        lda HALFMOVE
        cmp #4
        bcs :+
        jmp mkfmvoff            ; qs, clock < 4: no repetition scan below
:       ; qs quiet with clock >= 4: the child rep-scans; keep the hash
        ; exact, catching up any stale suffix first (rare)
        lda HVALID
        cmp PLY
        beq mkfqon
        jsr hashcatchup
        ldx PLY
mkfqon: lda HASH0               ; save pre-move hash; watermark = child.
        sta HASHSTK0,x          ; The stm xor is fused into the save
        eor STMKEY+0            ; (see the capture-path comment).
        sta HASH0
        lda HASH1
        sta HASHSTK1,x
        eor STMKEY+1
        sta HASH1
        lda HASH2
        sta HASHSTK2,x
        eor STMKEY+2
        sta HASH2
        lda HASH3
        sta HASHSTK3,x
        eor STMKEY+3
        sta HASH3
        inx
        stx HVALID
mkfmvon:
        ; move the piece: clear FROM, fused hash+psqt, place on TO
        ldy FROM
        lda #0
        sta a:BOARD,y
        jsr movepiece           ; contract: Y = FROM at entry
        ldy TO
        lda MVPIECE
        sta a:BOARD,y
        tay
        lda SLOTTAB,y
        tay
        lda TO
        sta PIECESQ,y
        ; castling rights: CASTLE &= CASTLEMASK[FROM] & CASTLEMASK[TO];
        ; with no rights left (the common case once both sides have
        ; castled) nothing can change - skip the mask lookups entirely
        ldx PLY
        lda CASTLE
        beq mkfnocch
        ldy FROM
        lda CASTLEMASK,y
        ldy TO
        and CASTLEMASK,y
        and CASTLE
        cmp CASTLE              ; Z = "rights unchanged" (sta keeps flags)
        sta CASTLE
        beq mkfnocch
        lda UNDOCASTLE,x
        jsr hashcastle          ; xor out the old rights (X preserved)
        lda CASTLE
        jsr hashcastle          ; xor in the new
mkfnocch:
        ; ep: never a double push here; hash out the old ep file if set
        lda UNDOEP,x
        cmp #NOSQ
        beq mkfnoep
        jsr hashep
mkfnoep:
        lda #NOSQ
        sta EPSQ
        lda SIDE                ; (stm hash already applied at the save)
        eor #COLORMASK
        sta SIDE
        inc PLY
        jmp ckfast              ; flags==0: direct/discovered scan only
.endif
mkfmvoff:
        ; hash-off mover: psqt/pawn-bits only, no rights/ep/stm hash
        ldy FROM
        lda #0
        sta a:BOARD,y
.ifndef NOEVAL
        jsr movepieceq          ; contract: Y = FROM at entry
.endif
        ldy TO
        lda MVPIECE
        sta a:BOARD,y
        tay
        lda SLOTTAB,y
        tay
        lda TO
        sta PIECESQ,y
        lda CASTLE              ; no rights left: skip the mask lookups
        beq mkfoffnc
        ldy FROM
        lda CASTLEMASK,y
        ldy TO
        and CASTLEMASK,y
        and CASTLE
        sta CASTLE
mkfoffnc:
        lda #NOSQ
        sta EPSQ
        lda SIDE
        eor #COLORMASK
        sta SIDE
        inc PLY
.ifndef NOEVAL
        jmp ckfast              ; flags==0: direct/discovered scan only
.else
        rts
.endif

; ---- slow path: en passant, double push, promotion, castle ----
mkslow:
.ifndef NOEVAL
        lda PHASE               ; slow path always saves PHASE (promos
        sta UNDOPHASE,x         ;  and ep captures may change it)
        ; rare move kinds always keep the hash exact: catch up any stale
        ; qs suffix, save the pre-move hash, advance the watermark. (The
        ; upgrade tail and hashcatchup can then assume replayed plies
        ; are always flag-free.)
        lda HVALID
        cmp PLY
        beq :+
        jsr hashcatchup
:       ldx PLY
        lda HASH0
        sta HASHSTK0,x
        lda HASH1
        sta HASHSTK1,x
        lda HASH2
        sta HASHSTK2,x
        lda HASH3
        sta HASHSTK3,x
        inx
        stx HVALID
        dex
.endif
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
        dec NPAWNS              ; ... and one fewer pawn is on the board
                                ;  (the ONLY non-capture way a pawn leaves;
                                ;  captures decrement in takepiece/takepieceq)
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
        jsr hashcastle          ; xor out the old rights (X preserved)
        lda CASTLE
        jsr hashcastle          ; xor in the new
mknocch:
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
        lda #NOSQ               ; the full scan found no checker SQUARE, so
        sta CHECKERSQ,x         ;  the pre-make evasion filter falls back to
                                ;  make + attacked() at this ply (rare: only
                                ;  castles and ep captures land here, and
                                ;  neither store touches the carry curincheck
                                ;  returned)
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
        bne ckhitx
        beq cknone              ; always
ckdnext: txa
        jmp ckdwalk
        ; The two check-detection exits are STUBS: the branches that reach
        ; them (four in ckfast, one in ckdwalk) are already near their
        ; range limit, so the bodies live at cksetck/cksetcx, out of the
        ; way of every branch in this routine.
ckhitx: stx GTMP                ; DISCOVERED: X = the checker's square
        jmp cksetcx             ;  (GTMP is dead here: only a castle uses
                                ;   it in make, and castles take the
                                ;   curincheck path, never ckdwalk)
ckhit:  ldy DIFF                ; DIRECT: the checker is the piece on TO
        jmp cksetck
cknone: ldx PLY
        lda #0
        sta INCHK,x
ckdone:
        ; deferred hash upgrade (deep opt r3): a hash-elided make that
        ; turns out to give check makes the child an EVASION node, which
        ; ttstores (QSKIND=0) and may repetition-scan - so the hash must
        ; be exact after all. hashcatchup replays the stale suffix
        ; INCLUDING this move from the undo records (rare: checking
        ; moves inside quiescence). Register contract: all three INCHK
        ; writers above leave A = INCHK[PLY] and X = PLY.
        beq :+                  ; no check: elision stands
        cpx HVALID
        beq :+                  ; hash already current for the child
        jsr hashcatchup
:
        ; PDIRTY bit 0 (the DIRTYTAB transient) set means THIS move
        ; touched a pawn or king, i.e. the only kind of move that can
        ; change PSTRUCT: save the pre-move value into this move's undo
        ; slot (clean moves skip both the save here and the restore in
        ; unmake). The pawnterm dispatch lives HERE (r4 integration),
        ; and it is LAZY: only king-only dirt on a valid base takes the
        ; eager ptkonly shield delta; everything else marks its changed
        ; files into FDIRTY and leaves a stale marker in PDIRTY for the
        ; first eval below to consume (pawntermfull) — makes whose
        ; subtree never evals skip the recompute entirely.
        lda PDIRTY
        beq mkpdone             ; clean move, nothing inherited
        lsr                     ; C = bit 0: did THIS make dirty it?
        bcc mkpdone             ; no: an inherited deferral rides along
        ldx PLY
        dex                     ; the undo slot for THIS move
        tay                     ; Y = PDIRTY>>1: 1 iff king-only fresh
                                ;  dirt on a clean base ($03 >> 1)
        lda PSTRUCT
        sta UNDOPSL,x
        lda PSTRUCT+1
        sta UNDOPSH,x
.ifdef PTNOCACHE
        ; measurement baseline (task #47): disable the incremental
        ; pawn-structure cache. make never recomputes; eval recomputes
        ; PSTRUCT fresh on every call. Same PSTRUCT at every eval, so
        ; identical search trees — only cycles differ.
        lda #0
        sta PDIRTY
        sta UNDOPD,x
.else
        lda FEATURES
        and #FT_PSTRUCT
        beq mkptoff
        cpy #1
        beq mkkonly
        ; defer: the stale marker is the constant $40 — a bit DIRTYTAB
        ; never sets, so the pre-make value is recoverable here as
        ; PDIRTY's bit 6 and unmake can restore it EXACTLY (a
        ; conservative "restore the marker wholesale" measured a
        ; regression: an unmade deferred pawn move left its parent
        ; falsely stale and every sibling subtree re-consumed).
        tya
        and #$20                ; bit 6 >> 1: was the base already stale?
        asl                     ; A = pre-make PDIRTY ($00 or $40)
        sta UNDOPD,x
        lda #$40
        sta PDIRTY              ; deferred stale marker
        lda MVPIECE             ; mark this move's changed files (the
        and #TYPEMASK           ;  per-file cache, item 1)
        cmp #PAWN
        bne mknpm
        ldy FROM                ; pawn mover: FROM/TO files change (an ep
        lda FILEBIT,y           ;  victim shares TO's file; a promotion
        ldy TO                  ;  marks TO conservatively)
        ora FILEBIT,y
        bne mkfd                ; always (file bits are nonzero)
mknpm:  lda UNDOCAP,x           ; non-pawn mover: only a pawn victim
        beq mknofd              ;  changes files (a king move deferred on
        and #TYPEMASK           ;  an inherited stale base may capture
        cmp #PAWN               ;  nothing, or a non-pawn)
        bne mknofd
        ldy UNDOCAPSQ,x
        lda FILEBIT,y
mkfd:   sta UNDOFD,x            ; unmake's re-dirty mask: THIS move's
        ora FDIRTY              ;  files ONLY (an accumulated mask here
        sta FDIRTY              ;  measured a net regression)
        rts                     ; DEFERRED: no recompute now
mknofd: lda #0
        sta UNDOFD,x
        rts                     ; deferred, no file changes
mkkonly:
        lda #0                  ; king-only on a valid base: no pawn
        sta UNDOFD,x            ;  placement changed, and ptkonly leaves
        sta UNDOPD,x            ;  PSTRUCT valid again (restore marker 0)
        jmp ptkonly             ; eager shield delta; clears PDIRTY,
                                ;  rts returns to caller
mkptoff:
        lda #0
        sta PDIRTY
        sta UNDOPD,x
.endif
mkpdone:
.endif
        rts

.ifndef NOEVAL
; ---------------------------------------------------------------
; cksetck / cksetcx: the bodies of ckfast's two check exits. Both record
; INCHK[PLY] = 1 plus what the PRE-MAKE evasion filter (search.s sdevade)
; needs: CHECKERSQ[PLY], a square the newly-checked side is checked FROM,
; and CHECKDIR[PLY], the single-step 0x88 delta from there toward that
; king. Getting them here is nearly free, which is the whole point - the
; check-detection tables already know the answer, and reconstructing it
; later would cost a scan.
;
;   cksetck  DIRECT check: the checker is the piece now on TO, and Y is
;            already DIFF, the (king - TO) difference index, so
;            DELTATAB[DIFF] IS the checker->king step. It is 0 for a
;            knight, which is exactly right: nothing can interpose
;            against a knight, and a 0 CHECKDIR makes the filter's
;            DELTATAB compare reject every non-capture with no extra test.
;   cksetcx  DISCOVERED check: the checker is the slider ckdwalk stopped
;            on (its square parked in GTMP), found by walking AWAY from
;            the king along ATDELTA - so the step back toward the king is
;            -ATDELTA.
;
; Register contract at ckdone: A = INCHK[PLY] (nonzero), X = PLY.
; ---------------------------------------------------------------
cksetck:
        lda DELTATAB,y
        ldx PLY
        sta CHECKDIR,x
        lda TO
        sta CHECKERSQ,x
        lda #1
        sta INCHK,x
        jmp ckdone
cksetcx:
        lda #0
        sec
        sbc ATDELTA
        ldx PLY
        sta CHECKDIR,x
        lda GTMP
        sta CHECKERSQ,x
        lda #1
        sta INCHK,x
        jmp ckdone
.endif

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

.ifndef NOEVAL
; ---------------------------------------------------------------
; hashcatchup: repair the elided-hash suffix (deep opt r3; see the
; HVALID contract in defs.inc). Replays the moves recorded at plies
; HVALID..PLY-1 into HASH0-3, rewriting each ply's HASHSTK entry, and
; sets HVALID = PLY. Replayed plies are always flag-free moves (the
; slow path never elides) except a possible null marker at the base
; (inside a null subtree; makenull already applied stm/ep and saved
; HASHSTK itself, so the marker just steps past). Consequences used:
; no promotion/castle/ep-capture replays, and the ep square after
; every replayed move is NOSQ (only a pre-existing ep file xors out).
; Deliberately simple - this path is rare. Also correct when
; HVALID > PLY (post-null transient): replays nothing, clamps.
; Clobbers A,X,Y,T0,T1, ZPTR+1.
; ---------------------------------------------------------------
hashcatchup:
        lda HVALID
        sta T0
hcloop: lda T0
        cmp PLY
        bcc hcstep
        lda PLY
        sta HVALID              ; done: current through position PLY
        rts
hcstep: tax
        lda UNDOFROM,x
        cmp #NOSQ
        bne hcreal
        inc T0                  ; null marker at the base (see above)
        bne hcloop              ; always (T0 <= MAXPLY)
hcreal:
        lda HASH0               ; repair this ply's pre-move hash entry
        sta HASHSTK0,x
        lda HASH1
        sta HASHSTK1,x
        lda HASH2
        sta HASHSTK2,x
        lda HASH3
        sta HASHSTK3,x
        ; mover: xor key[kind][from] ^ key[kind][to]
        lda UNDOPIECE,x
        ldy UNDOFROM,x
        jsr hashpiece           ; clobbers X; preserves Y
        ldx T0
        lda UNDOPIECE,x
        ldy UNDOTO,x
        jsr hashpiece
        ldx T0
        ; victim, if any
        lda UNDOCAP,x
        beq hcnocap
        ldy UNDOCAPSQ,x
        jsr hashpiece
        ldx T0
hcnocap:
        jsr hashstm             ; side to move (A only)
        ; ep: xor out the old file; flag-free moves never set a new one
        lda UNDOEP,x
        cmp #NOSQ
        beq hcnoep
        jsr hashep              ; clobbers A,Y; X preserved
hcnoep:
        ; castling rights: the next ply's saved copy holds this move's
        ; resulting rights - except at PLY itself, where they are the
        ; live CASTLE (the current make has already updated it when
        ; called from the deferred upgrade, and not yet saved a record
        ; at PLY... its prologue wrote UNDOCASTLE[PLY] = pre-move
        ; rights, which is NOT this replay's result - hence the split)
        inc T0
        ldx T0
        cpx PLY
        bne hcnext
        lda CASTLE
        jmp hccmp
hcnext: lda UNDOCASTLE,x
hccmp:  dex
        cmp UNDOCASTLE,x
        beq hcgo                ; unchanged: the common case
        sta T1                  ; changed: out with the old, in the new
        lda UNDOCASTLE,x
        jsr hashcastle          ; clobbers A,Y; X preserved
        lda T1
        jsr hashcastle
hcgo:   jmp hcloop
.endif

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
        ; (PHASE is restored only on the capture/slow paths below: quiet
        ;  flag-free moves never change it and their makes never save
        ;  it - deep opt r4)
        lda UNDOHALF,x
        sta HALFMOVE
        ; hash: restore only when this ply's HASHSTK entry is valid
        ; (its make kept the hash, or hashcatchup repaired it); elided
        ; plies never touched HASH, so there is nothing to undo. See
        ; the HVALID contract in defs.inc.
        cpx HVALID
        bcs umnohash            ; PLY >= HVALID: hash was elided here
        lda HASHSTK0,x
        sta HASH0
        lda HASHSTK1,x
        sta HASH1
        lda HASHSTK2,x
        sta HASH2
        lda HASHSTK3,x
        sta HASH3
        stx HVALID              ; clamp the watermark to this ply
umnohash:
        ; (PSTRUCT is restored only on the pawn/king paths below,
        ;  mirroring make's PDIRTY-gated save - deep opt r4)
.endif
        lda UNDOFLAGS,x
        bne umtramp             ; castle rook / promo handling: rare
; ---- fast path: flags == 0, no castle, no promo, capsq == TO ----
        lda UNDOCAP,x
        beq umfquiet
        ; capture: the victim goes straight back onto TO (no dead
        ; clear-then-restore; deep opt r4), and PHASE returns
        ldy UNDOTO,x
        sta a:BOARD,y
        tay                     ; victim byte -> its slot
        lda SLOTTAB,y
        tay
        lda UNDOTO,x
        sta PIECESQ,y
.ifndef NOEVAL
        lda UNDOPHASE,x
        sta PHASE
        ; pawn victim: its file bit returns (inlined self-inverse toggle)
        lda UNDOCAP,x
        and #TYPEMASK
        cmp #PAWN
        bne umfmover
        lda UNDOCAP,x
        and #COLORMASK
        sta GTMP
        lda UNDOTO,x
        tay
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        inc NPAWNS              ; the captured pawn is back on the board
        ldx PLY
        lda UNDOFD,x            ; the re-toggled file's cached term goes
        ora FDIRTY              ;  stale again: OR back this move's
        sta FDIRTY              ;  make-time dirty-file mask (r4)
        lda UNDOPD,x            ; the lazy-pawnterm stale marker returns
        sta PDIRTY              ;  with PSTRUCT (same save condition)
        lda UNDOPSL,x           ; pawn event: PSTRUCT returns (make saved
        sta PSTRUCT             ;  it under the same condition)
        lda UNDOPSH,x
        sta PSTRUCT+1
.endif
        jmp umfmover
umtramp:
        jmp umslow2             ; (bne range trampoline)
umfquiet:
        ; quiet: clear TO
        ldy UNDOTO,x
        lda #0
        sta a:BOARD,y
umfmover:
        ; put the original piece byte back on FROM
        lda UNDOPIECE,x
        ldy UNDOFROM,x
        sta a:BOARD,y
        tay                     ; piece byte -> mover's slot
        lda SLOTTAB,y
        tay
        lda UNDOFROM,x
        sta PIECESQ,y
.ifndef NOEVAL
        ; pawn mover: FROM and TO file bits re-toggle (never a promo
        ; here) and PSTRUCT returns; a king mover restores PSTRUCT only
        ; (the shield term). Other movers leave both alone, mirroring
        ; make's PDIRTY-gated save.
        lda UNDOPIECE,x
        and #TYPEMASK
        cmp #PAWN
        beq umfpawn
        cmp #KING
        bne umfdone
        lda UNDOPD,x            ; the lazy-pawnterm stale marker returns
        sta PDIRTY              ;  (0 after an eager ptkonly make)
        lda UNDOPSL,x           ; king mover: PSTRUCT returns
        sta PSTRUCT
        lda UNDOPSH,x
        sta PSTRUCT+1
        rts
umfpawn:
        lda UNDOFD,x            ; re-toggled files' cached terms go stale:
        ora FDIRTY              ;  OR back this move's dirty-file mask (r4)
        sta FDIRTY
        lda UNDOPD,x            ; the lazy-pawnterm stale marker returns
        sta PDIRTY
        lda UNDOPSL,x           ; pawn mover: PSTRUCT returns
        sta PSTRUCT
        lda UNDOPSH,x
        sta PSTRUCT+1
        lda UNDOPIECE,x
        and #COLORMASK
        sta GTMP
        lda UNDOFROM,x
        tay
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
        ldx PLY
        lda UNDOTO,x
        tay
        and #$07
        ora GTMP
        tax
        lda RANKBIT,y
        eor PWBITS,x
        sta PWBITS,x
.endif
umfdone:
        rts

; ---- slow path: castle / promo / ep flags set ----
umslow2:
        ; common board restore (the fast path specializes this): clear
        ; TO, put the original piece byte back on FROM
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
.ifndef NOEVAL
        lda UNDOPHASE,x
        sta PHASE
        lda UNDOPSL,x           ; every slow move is a pawn/king event:
        sta PSTRUCT             ;  make always saved PSTRUCT (promo/ep =
        lda UNDOPSH,x           ;  pawn, castle = king, double = pawn)
        sta PSTRUCT+1
        lda UNDOFD,x            ; re-toggled files' cached terms go stale:
        ora FDIRTY              ;  OR back this move's dirty-file mask
        sta FDIRTY              ;  (castle saved it on the king-only path)
        lda UNDOPD,x            ; the lazy-pawnterm stale marker returns
        sta PDIRTY
.endif
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
        inc NPAWNS              ; ... and so does the pawn (ep captures too:
                                ;  UNDOCAPSQ is the pushed-past square)
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
        beq umpmto
        inc NPAWNS              ; ... and the promoted pawn comes back
        jmp umpvictim
umpmto: ldy UNDOTO,x
        lda UNDOPIECE,x
        jsr pbtoggle
        ldx PLY
        jmp umpvictim
.endif

; ---------------------------------------------------------------
; RATTACK (deep opt r4): the reversed attack table used by attacked()'s
; slot filter. RATTACK[j] = ATTACKTAB[$EE - j] for j = 0..$EE, then a
; zero tail through j = $176 so tombstone squares (NOSQ = $FF) indexed
; as ($77-ATSQ)+$FF always read 0. Literal bytes (a ca65 closed-form
; .repeat generator proved impractically slow to assemble); the layout
; is just ATTACKTAB reversed, and TestRATTackTable (board4_test.go, in
; the short suite) proves RATTACK[j] == ATTACKTAB[$EE-j] plus the zero
; tail against the engine's own gentables-emitted table byte-for-byte,
; so the two can never drift apart unnoticed.
; ---------------------------------------------------------------
.segment "TABLES"
.align 256
RATTACK:
        .byte $04,$00,$00,$00,$00,$00,$00,$08,$00,$00,$00,$00,$00,$00,$04,$00
        .byte $00,$04,$00,$00,$00,$00,$00,$08,$00,$00,$00,$00,$00,$04,$00,$00
        .byte $00,$00,$04,$00,$00,$00,$00,$08,$00,$00,$00,$00,$04,$00,$00,$00
        .byte $00,$00,$00,$04,$00,$00,$00,$08,$00,$00,$00,$04,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$04,$00,$00,$08,$00,$00,$04,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$04,$01,$08,$01,$04,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$01,$16,$0A,$16,$01,$00,$00,$00,$00,$00,$00
        .byte $08,$08,$08,$08,$08,$08,$0A,$00,$0A,$08,$08,$08,$08,$08,$08,$00
        .byte $00,$00,$00,$00,$00,$01,$26,$0A,$26,$01,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$04,$01,$08,$01,$04,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$04,$00,$00,$08,$00,$00,$04,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$04,$00,$00,$00,$08,$00,$00,$00,$04,$00,$00,$00,$00
        .byte $00,$00,$04,$00,$00,$00,$00,$08,$00,$00,$00,$00,$04,$00,$00,$00
        .byte $00,$04,$00,$00,$00,$00,$00,$08,$00,$00,$00,$00,$00,$04,$00,$00
        .byte $04,$00,$00,$00,$00,$00,$00,$08,$00,$00,$00,$00,$00,$00,$04,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00,$00
        .byte $00,$00,$00,$00,$00,$00,$00

; ---------------------------------------------------------------
; mopfin / mopcd (FT2_MOPUP): the mop-up term's tail (close term + signed
; apply) and its corner-distance helper. Placed HERE, immediately after
; RATTACK, to fill the page-alignment fill between RATTACK's 375 bytes and
; the page-aligned start of the generated tables (137 bytes that would
; otherwise be wasted). Moving these 48 bytes out of CODE's tail is what
; drops CODE below $7900 and the whole page-aligned TABLES base with it
; (space round 1, 2026-07-25). mopupterm (engine.s, also TABLES) reaches
; mopfin by `jmp mopfin` with A = manhattan(kings) and mopcd by
; `jsr mopcd` — both absolute, so the move is transparent. Cold: nothing
; here runs unless FT2_MOPUP is set.
; ---------------------------------------------------------------
mopfin: ; A = manhattan(losing,winning king); T2 = Edge*CMD (running B);
        ; MULCNT = winner (0 white / 1 black). Add Close*(14-md), then apply.
        sta T0                  ; md
        lda #14
        sec
        sbc T0                  ; 14 - md (>= 0, md <= 14)
        asl
        asl                     ; *4 = Close*(14-md); C=0 (value <= 52)
        adc T2                  ; B = Edge*CMD + Close term (<= 116, positive)
        sta T2
        ; apply B to SCORE, sign-extended, negated for a black winner
        ; (B > 0, so -B is a negative byte -> hi = $FF). One 16-bit add.
        ldy #0
        lda T2
        ldx MULCNT
        beq :+
        eor #$FF                ; -B (two's complement)
        clc
        adc #1
        ldy #$FF                ; sign extension
:       clc
        adc SCORE
        sta SCORE
        tya
        adc SCORE+1
        sta SCORE+1
        rts
; mopcd: A = coordinate 0..7 -> centre corner distance (~c)&3 for c<4 else
; c&3, i.e. {3,2,1,0,0,1,2,3}. Shared by the two CMD terms.
mopcd:  cmp #4
        bcs :+
        eor #$FF
:       and #$03
        rts
.segment "CODE"
