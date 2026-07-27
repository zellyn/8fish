; Negamax alpha-beta search with quiescence.
;
; Node protocol: caller sets ALPHA/BETA at the child's ply, then calls
; search with PLY already advanced by make. SCORE returns the fail-hard
; result from the side-to-move's POV. Full-width until PLY >= MAXDEPTH,
; then quiescence: stand pat + captures + queen promotions; when in
; check, a full evasion node instead (no stand pat, all moves, mate
; detection) — which is exactly a full-width node, so it reuses one.
;
; Move loop runs up to five passes over the generated list (see the
; move-loop comment below): 0 TT move, 1 heavy captures/promos, 2
; light captures, 3 killers, 4 remaining quiets. QS capture-only
; nodes run passes 1-2 only.

; ---------------------------------------------------------------
; gennodeq / gennodef: set base/cursor from MSP, generate, set end.
; Split by node kind (deep opt r4): the caller always knows whether
; this is a qs capture node (snodeq) or a full-width/evasion node
; (snode), so neither entry tests QSKIND. QS nodes use the
; specialized captures-only generator copy.
; ---------------------------------------------------------------
gennodeq:
        ldy PLY
        lda MSP
        sta PLYBASELO,y
        lda MSP+1
        sta PLYBASEHI,y
        ; recap2 gate: past RecapAfter=2 qs plies, restrict generateq
        ; to recaptures onto the previous move's TO (UNDOTO[PLY-1]).
        ; qs depth = PLY - MAXDEPTH (>= 0 here; MAXDEPTH is constant
        ; through a qs subtree - qs does no null/LMR reductions).
        lda #0
        sta CLSP                ; class-presence accumulator (r4)
        sta RECAPONLY
        lda PLY
        sec
        sbc MAXDEPTH
        cmp #2                  ; RecapAfter = 2
        bcc genq                ; qs depth 0/1: full-width captures
        ldx PLY
        dex
        lda UNDOTO,x            ; square the previous move landed on
        sta RECAPSQ
        lda #1
        sta RECAPONLY           ; (kept current: diagnostics key on it)
        jsr genrecapent         ; direct entry (r4): skip generateq's
        jmp gennd2              ;  RECAPONLY re-dispatch
genq:   jsr generateq
        jmp gennd2
gennodef:
        ldy PLY
        lda MSP
        sta PLYBASELO,y
        lda MSP+1
        sta PLYBASEHI,y
        lda #0
        sta CLSP                ; class-presence accumulator (r4)
        jsr generate
gennd2: ldy PLY
        lda MSP
        sta PLYENDLO,y
        sta SENDL               ; ZP mirrors for the scan loops; the
        lda MSP+1               ;  cursor starts at the list base
        sta PLYENDHI,y
        sta SENDH
        lda PLYBASELO,y
        sta CURPTR
        lda PLYBASEHI,y
        sta CURPTR+1
        lda CLSP                ; latch class presence for this node's
        sta CLSPRES,y           ;  pass-entry skips (survives recursion)
        rts

; ---------------------------------------------------------------
; curincheck: carry set if the side to move is in check.
; ---------------------------------------------------------------
curincheck:
        lda SIDE
        eor #COLORMASK
        sta ATSIDE
        lda SIDE
        asl
        tay
        lda PIECESQ,y           ; own king (slot 0 of own base)
        sta ATSQ
        jmp attacked

; ---------------------------------------------------------------
; checkclock: poll the harness clock; set ABORT once cycles reach the
; hard limit (2x budget). No-op in fixed-depth mode (budget 0).
; ---------------------------------------------------------------
checkclock:
        lda #128                ; rearm the poll divider (search counts it
        sta NODECNT             ;  down; 0 -> poll here -> reset to 128)
        lda BUDGET0
        ora BUDGET1
        ora BUDGET2
        beq ccout
        lda CURDEPTH            ; iteration 1 always completes, so an
        cmp #2                  ; abort can never leave a garbage move
        bcc ccout
        lda CLOCK_TRAP          ; latches all three bytes
        cmp ABORTL0
        lda CLOCK_TRAP+1
        sbc ABORTL1
        lda CLOCK_TRAP+2
        sbc ABORTL2
        bcc ccout               ; still under the limit
        lda #1
        sta ABORT
ccout:  rts

; ---------------------------------------------------------------
; search
; ---------------------------------------------------------------
search:
        dec NODECNT             ; countdown: poll the clock every 128 nodes
        bne :+                  ;  (checkclock rearms; first poll after 256)
        jsr checkclock
:       lda ABORT
        beq :+
        lda #0                  ; aborting: unwind with a dummy score
        sta SCORE
        sta SCORE+1
        rts
:       lda PLY
        beq sdrawend            ; root: no draw checks; a move is required
                                ;  (and never at the ply cap)
        cmp #MAXPLY-1
        bcc :+
        jmp eval                ; hard ply cap: static eval
:
        ; 50-move rule. (Nuance accepted: a mate delivered exactly on the
        ; 100th halfmove is scored as a draw here.)
        lda HALFMOVE
        cmp #100
        bcs sdraw
        ; twofold repetition against the search path: only reachable
        ; within the last HALFMOVE plies, same side to move (step 2)
        cmp #4
        bcc snorep
        lda PLY
        sec
        sbc HALFMOVE
        bcs :+
        lda #0
:       sta T2                  ; scan lower bound
        lda PLY
        sec
        sbc #2
        bcc snorep
sreploop:
        cmp T2
        bcc snorep
        tax
        lda HASHSTK0,x
        cmp HASH0
        bne srepnext
        lda HASHSTK1,x
        cmp HASH1
        bne srepnext
        lda HASHSTK2,x
        cmp HASH2
        bne srepnext
        lda HASHSTK3,x
        cmp HASH3
        bne srepnext
        beq sdraw               ; repetition
srepnext:
        txa
        sec
        sbc #2
        bcs sreploop
snorep:
        ; insufficient material: PHASE <= 1 and no pawns (covers KK,
        ; KNK, KBK; same-color-bishops draws are the referee's problem)
        lda PHASE
        cmp #2
        bcs sdrawend
        ldx #31
smdloop:
        lda PIECESQ,x
        cmp #NOSQ
        beq smdnext
        tay
        lda a:BOARD,y
        and #TYPEMASK
        cmp #PAWN
        beq sdrawend            ; a pawn exists: playable
smdnext:
        dex
        bpl smdloop
sdraw:  lda #0
        sta SCORE
        sta SCORE+1
        rts
sdrawend:
; debug (FT_CKVERIFY): cross-check the propagated in-check flag against a
; full scan; a mismatch kills the run with code 101. ASSEMBLY-TIME
; optional (space round 1, 2026-07-25): this is a pure debug assertion
; that costs the shipped image 24 bytes of a nearly-full MAIN, so it is
; built only into the `ca65 -D CKVERIFY` variant (asmbuild.BuildVariant,
; same pattern as PTNOCACHE/RKNOCACHE); TestGiveCheckVerify builds that
; variant. FEATURES bit $80 stays reserved so the variant's feature
; encoding is unchanged.
.ifdef CKVERIFY
.ifndef NOEVAL
        lda FEATURES
        and #FT_CKVERIFY
        beq ckvdone
        jsr curincheck
        ldy PLY
        lda #0
        rol
        cmp INCHK,y
        beq ckvdone
        lda #101
        sta EXIT_TRAP
ckvdone:
.endif
.endif
        ldy PLY
        lda #0                  ; (INCHK,y is NOT cleared here: it was
        sta LEGALCNT,y          ;  propagated by make / the root driver)
        sta QSKIND,y
        tya
        cmp MAXDEPTH
        bcc :+
        jmp squiesce            ; quiescence entry below
:       ; Init read only by full-width/evasion nodes: pure qs nodes
        ; never touch RAISED/FUTILE (sdone and p2done test QSKIND
        ; first), TTFROMA (snode tests QSKIND first), or TTBF (only
        ; sret reads it), and qs sets its own delta threshold before
        ; snode (qsdelta/qsnothr).
        lda #0
        sta RAISED,y
        sta FUTILE,y
        sta THRT,y              ; tier threshold 0: no delta pruning
        lda #NOSQ
        sta TTFROMA,y
        sta TTBF,y
        ; full-width node: probe the transposition table
        jsr ttprobe
ttpdone:                        ; test hook: carry = hit, TTENTRY+5/6 = the
                                ;  ply-adjusted score (TestTTScoreRoundTrip)
        bcc snodej
        ldy PLY
        lda TTENTRY+3
        sta TTFROMA,y           ; TT move: searched first (pass 0)
        lda TTENTRY+4
        sta TTTOA,y
        ; cutoff allowed if stored depth >= remaining depth, not at root.
        ; Compare remaining<<2 against the packed depth<<2|bound byte: the
        ; bound bits are 1-3 (never 0 on a hit), so rem<<2 < depth<<2|bound
        ; is exactly rem <= depth — one shift-compare, no scratch byte.
        lda PLY
        beq snodej
        lda MAXDEPTH
        sec
        sbc PLY
        asl
        asl
        cmp TTENTRY+7
        bcc ttcut               ; remaining <= stored depth: cutoff ok
snodej: jmp sprep               ; otherwise ordering only
ttcut:  lda TTENTRY+7
        and #$03
        cmp #TT_EXACT
        beq ttexact
        cmp #TT_LOWER
        beq ttlower
        ; upper bound: usable if score <= alpha, i.e. alpha - score >= 0
        sec
        lda ALPHALO,y
        sbc TTENTRY+5
        lda ALPHAHI,y
        sbc TTENTRY+6
        bvc :+
        eor #$80
:       bmi snodej              ; alpha < score: not usable
        lda ALPHALO,y           ; fail-hard low
        sta SCORE
        lda ALPHAHI,y
        sta SCORE+1
        rts
ttlower:                        ; usable if score >= beta
        sec
        lda TTENTRY+5
        sbc BETALO,y
        lda TTENTRY+6
        sbc BETAHI,y
        bvc :+
        eor #$80
:       bmi snodej              ; score < beta: not usable
        lda BETALO,y
        sta SCORE
        lda BETAHI,y
        sta SCORE+1
        rts
ttexact:
        lda TTENTRY+5
        sta SCORE
        lda TTENTRY+6
        sta SCORE+1
        rts

squiesce:
        lda INCHK,y             ; propagated by make (Y = PLY)
        beq :+
        lda #0                  ; in check: full evasion node, which
        sta RAISED,y            ;  exits through sret/p2done like a
        sta FUTILE,y            ;  full-width one: full init
        sta THRT,y              ; tier threshold 0: no delta pruning
        lda #NOSQ
        sta TTFROMA,y
        sta TTBF,y
        jmp snode
:       lda #1
        sta QSKIND,y
        jsr eval
        ldy PLY
        ; stand pat: if SCORE >= BETA return BETA
        sec
        lda SCORE
        sbc BETALO,y
        lda SCORE+1
        sbc BETAHI,y
        bvc :+
        eor #$80
:       bmi qsnofh
        lda BETALO,y
        sta SCORE
        lda BETAHI,y
        sta SCORE+1
        rts
qsnofh: ; if SCORE > ALPHA: ALPHA = SCORE
        sec
        lda ALPHALO,y
        sbc SCORE
        lda ALPHAHI,y
        sbc SCORE+1
        bvc :+
        eor #$80
:       bmi qsraise
qsdelta:
        ; delta pruning: search a capture only if its victim value clears
        ; T = alpha - standpat - 200 (16-bit, same wrap semantics as the
        ; old stored threshold). Disabled at low phase, where every pawn
        ; matters. T is classified ONCE per node into a tier-byte
        ; threshold THRT = minvictimtype<<4 (see defs.inc), making the
        ; per-capture filter in passes 1/2 a single unsigned tier compare
        ; (deep opt r4; VV16 victim-value tables retired).
        ; Every qs path below also presets SMODE = 0 once: a qs node's
        ; children all get the full window, so the per-move store in the
        ; old slegal chain is gone.
        lda PHASE
        cmp #6
        bcc qsnothr
        sec
        lda ALPHALO,y
        sbc SCORE               ; SCORE still holds the stand-pat eval
        sta T0
        lda ALPHAHI,y
        sbc SCORE+1
        tax
        sec
        lda T0
        sbc #<200               ; margin
        sta T0
        txa
        sbc #>200               ; A:T0 = T (hi:lo)
        bmi qsnothr             ; T < 0: every capture passes
        sta T1
        ldx #$00                ; T <= VV[pawn]=100: everything passes
        lda T0
        cmp #<101
        lda T1
        sbc #>101
        bcc qsthr
        ldx #$20                ; T <= 320: knight victims and up
        lda T0
        cmp #<321
        lda T1
        sbc #>321
        bcc qsthr
        ldx #$30                ; T <= 330: bishop victims and up
        lda T0
        cmp #<331
        lda T1
        sbc #>331
        bcc qsthr
        ldx #$40                ; T <= 500: rook victims and up
        lda T0
        cmp #<501
        lda T1
        sbc #>501
        bcc qsthr
        ldx #$50                ; T <= 975: queen/king victims only
        lda T0
        cmp #<976
        lda T1
        sbc #>976
        bcc qsthr
        ldx #$60                ; T <= 20000: pseudo-legal king capture only
        lda T0
        cmp #<20001
        lda T1
        sbc #>20001
        bcc qsthr
        ldx #$70                ; T > 20000: nothing passes (as before:
qsthr:  txa                     ;  even the king row's 20000 fails then)
        sta THRT,y
        lda #0
        sta SMODE,y
        jmp snodeq              ; qs nodes skip sprep (no null/futility)
qsraise:
        lda SCORE               ; stand pat raised alpha: ALPHA = SCORE, so
        sta ALPHALO,y           ;  T = -200 < 0: every capture passes
        lda SCORE+1
        sta ALPHAHI,y
qsnothr:
        lda #0                  ; no delta pruning: tier threshold 0
        sta THRT,y
        sta SMODE,y
        jmp snodeq

; ---------------------------------------------------------------
; sprep: full-width-node pruning, before move generation.
; ---------------------------------------------------------------
sprep:  ldy PLY
        lda INCHK,y             ; propagated by make (root: the driver)
        beq :+
        jmp sprepj              ; in check: no null, no RFP, no futility
                                ;  (still needs the full-signal forced eval)
:       ; ---- null move: FT_NULL, not at the root, not right after a
        ; null, remaining >= 2, phase >= 3, beta below the +mate zone
        lda FEATURES
        and #FT_NULL
        bne :+
        jmp snonull
:       lda PLY
        bne :+
        jmp snonull             ; root must produce a move, never a null cutoff
:       tax
        lda UNDOFROM-1,x        ; NOSQ marks the parent move as a null
        cmp #NOSQ
        bne :+
        jmp snonull
:       lda MAXDEPTH
        sec
        sbc PLY
        cmp #4                  ; with R=2, a shallower null child is a
        bcc snonullj            ; bare QS sweep: all cost, no cut value
        lda PHASE
        cmp #3
        bcc snonullj
        lda BETAHI,y
        bmi :+                  ; negative beta: nowhere near the +mate zone
        cmp #MATEZONEHI         ; (signed-aware zone test; an unsigned compare
        bcs snonullj            ;  here silently disabled null move — see
:       ; only worth trying when the static eval already meets beta:
        ; failed nulls are pure cost
        jsr eval
        ldy PLY
        sec
        lda SCORE
        sbc BETALO,y
        lda SCORE+1
        sbc BETAHI,y
        bvc :+
        eor #$80
:       bpl :+
snonullj:
        jmp snonull             ; eval < beta: don't bother
:       jsr makenull            ;
        ldy PLY                 ; child ply: zero window around -beta
        sec
        lda #0
        sbc BETALO-1,y
        sta ALPHALO,y
        sta BETALO,y
        lda #0
        sbc BETAHI-1,y
        sta ALPHAHI,y
        sta BETAHI,y
        lda BETALO,y
        clc
        adc #1
        sta BETALO,y
        bcc :+
        lda BETAHI,y
        adc #0                  ; carry set: +1
        sta BETAHI,y
:       lda MAXDEPTH            ; reduce by R=2 for the null subtree
        pha
        sec
        sbc #2
        sta MAXDEPTH
        jsr search
        pla
        sta MAXDEPTH
        jsr unmakenull
        sec                     ; SCORE = -SCORE
        lda #0
        sbc SCORE
        sta SCORE
        lda #0
        sbc SCORE+1
        sta SCORE+1
        ldy PLY
        sec
        lda SCORE
        sbc BETALO,y
        lda SCORE+1
        sbc BETAHI,y
        bvc :+
        eor #$80
:       bmi snullf              ; below beta: search normally
nullcut:
        lda BETALO,y            ; null cutoff: fail hard
        sta SCORE
        lda BETAHI,y
        sta SCORE+1
        ; store a moveless lower bound so re-visits get the cutoff free
        lda #NOSQ
        sta TTENTRY+3
        sta TTENTRY+4
        lda SCORE
        sta TTENTRY+5
        lda SCORE+1
        sta TTENTRY+6
        lda #TT_LOWER
        jsr ttstore
        rts
snullf: jmp snode               ; failed null: remaining >= 4, so RFP and
                                ;  futility can never fire (their gate walk
                                ;  is a pure no-op at that depth) — straight
                                ;  to the move loop.
snonull:
        ; ---- RFP + futility: FT_FUTIL, remaining <= 2, and the window
        ; not inside a mate zone (static eval can't speak to mates)
        lda FEATURES
        and #FT_FUTIL
        bne gstart
gskip:  jmp sprepj              ; trampoline: the guard skips below are past
                                ;  branch range once the block grew
        ; SIGNED guard: skip only in a TRUE mate zone. A negative hi byte
        ; ($80-$FF) is >= MATEZONEHI ($74) unsigned, so the winning-mate
        ; cmp must run ONLY on the alpha/beta >= 0 path - otherwise every
        ; negative-window node wrongly skips futility (the old bug).
gstart: lda ALPHAHI,y
        bpl rfpapos             ; alpha >= 0: test winning-mate zone
        cmp #NMATEZONEHI
        bcc gskip               ; alpha in the losing-mate zone
        bcs rfpaok              ; alpha negative, not mate: futility active
rfpapos:
        cmp #MATEZONEHI
        bcs gskip               ; alpha in the winning-mate zone
rfpaok:
        lda BETAHI,y
        bpl rfpbpos             ; beta >= 0: test winning-mate zone
        cmp #NMATEZONEHI
        bcc gskip               ; beta in the losing-mate zone
        bcs rfpbok              ; beta negative, not mate: futility active
rfpbpos:
        cmp #MATEZONEHI
        bcs gskip               ; beta in the winning-mate zone
rfpbok:
        lda MAXDEPTH
        sec
        sbc PLY
        sta REMDEPTH
        cmp #3
        bcs gskip
        jsr eval
        ldy PLY
        ; margin: 120 @ rem1, 500 @ rem2 (16-bit; 500 = $01F4). rem2 at
        ; the tight 250 over-pruned negative windows (mirror task #34).
        ldx REMDEPTH
        cpx #2
        bcc rfpm1
        lda #$F4
        sta FUTMARG
        lda #$01
        sta FUTMARGH
        bne rfphave             ; always (hi = $01)
rfpm1:  lda #120
        sta FUTMARG
        lda #0
        sta FUTMARGH
rfphave:
        ; reverse futility: eval - margin >= beta -> fail high
        sec
        lda SCORE
        sbc FUTMARG
        sta T0
        lda SCORE+1
        sbc FUTMARGH
        sta T1
        sec
        lda T0
        sbc BETALO,y
        lda T1
        sbc BETAHI,y
        bvc :+
        eor #$80
:       bmi srfpno
        lda BETALO,y
        sta SCORE
        lda BETAHI,y
        sta SCORE+1
        rts
srfpno: ; futility (depth 1): eval + margin <= alpha -> quiets can't help
        lda REMDEPTH
        cmp #1
        bne sprepj
        clc
        lda SCORE
        adc FUTMARG
        sta T0
        lda SCORE+1
        adc FUTMARGH
        sta T1
        sec
        lda ALPHALO,y
        sbc T0
        lda ALPHAHI,y
        sbc T1
        bvc :+
        eor #$80
:       bmi sprepj              ; alpha < eval+margin: quiets may matter
        lda #1
        sta FUTILE,y
sprepj: ; branch trampoline: the in-check entry (jmp sprepj), the guard-skip
        ; trampoline (gskip) and the RFP/futility exits all land here, out of
        ; branch range of snode.
        jmp snode

; makenull / unmakenull: pass the move. Only ep, the hash, the halfmove
; clock, and the side flip change; accumulators are untouched.
makenull:
        ldx PLY
        lda #NOSQ               ; mark this ply's move as a null
        sta UNDOFROM,x
        lda EPSQ
        sta UNDOEP,x
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
        lda EPSQ
        cmp #NOSQ
        beq :+
        jsr hashep              ; xor out the ep file
        lda #NOSQ
        sta EPSQ
:       jsr hashstm
        lda SIDE
        eor #COLORMASK
        sta SIDE
        inc HALFMOVE
        inc PLY
        ldx PLY                 ; passing the move never gives check
        lda #0                  ; (null is only tried when not in check,
        sta INCHK,x             ;  so the opponent can't be in check)
        rts
unmakenull:
        dec PLY
        ldx PLY
        lda SIDE
        eor #COLORMASK
        sta SIDE
        lda UNDOEP,x
        sta EPSQ
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
        rts

; ---------------------------------------------------------------
; Move loop. Moves are 4 bytes: +0 tier, +1 from, +2 to, +3 flags
; (tier = victimtype<<4 | class, computed by emitmove; see defs.inc).
; Each pass runs its own specialized scan loop over the list, so a
; wrong-pass move is skipped on a single tier-byte compare; pass 0
; consumes the TT move by zeroing its tier, which makes passes 1-4
; skip it with no from/to comparisons at all.
;
; The live cursor is CURPTR (ZP) and the list end SENDL/H. A searched
; move persists the advanced cursor in CURSORLO/HI[PLY] (sgo); after
; the recursion sloopret rebuilds the ZP state (cursor, end, pass
; mirrors) from the per-ply arrays and re-enters the right loop.
; Pass structure is unchanged: 0 TT move -> 1 heavy captures/promos
; -> 2 light captures -> 3 killer quiets -> 4 remaining quiets; qs
; and futility nodes stop after pass 2.
; ---------------------------------------------------------------
snodeq: jsr gennodeq            ; qs capture node: no TT pass
        ldy PLY
        ; class-presence pass entry (deep opt r4 integration): CLSPRES
        ; bit 0 = a heavy capture/promo was emitted, bit 1 = a light
        ; capture. Quiets are batched and never OR the byte, so 0 means
        ; "empty list" ONLY for qs capture nodes (their generator emits
        ; captures/promos exclusively) - full-width nodes always fall
        ; through to the killer/quiet passes via p1done/p2done.
        lda CLSPRES,y
        bne stop1               ; captures exist: presence dispatch
        jmp sdone               ; qs: nothing scannable (empty list)
stop0:  lda CLSPRES,y           ; full-width entry (TT move absent)
stop1:  lsr                     ; C = heavy capture/promo present
        bcs stoph
        jmp p1done              ; no heavies: pass-2 presence decides
stoph:  lda #1
        sta PASSNO,y
        ldy #0
        beq p1loop              ; always
snode:  ldy PLY
        lda TTFROMA,y
        cmp #NOSQ
        beq sngen               ; no TT move: generate, then pass 1/2
        sta TTF0                ; pass 0 mirrors (both paths need them)
        lda TTTOA,y
        sta TTT0
        lda FEATURES2
        and #FT2_GENDEFER
        beq snp0                ; off: eager, exactly as before
        jmp sntry               ; deferred generation (out of line below)
snp0:   jsr gennodef            ; eager: CURPTR = base, SENDL/H = end,
        ldy PLY                 ;  then pass 0 hunts the TT move in the list
        lda #0
        sta PASSNO,y
        beq p0loopj             ; always
sngen:  jsr gennodef            ; (gennd2 leaves Y = PLY, which stop0 wants)
        jmp stop0
p0loopj:
        jmp p0loop

; ---- pass 1: heavy captures and promotions (tier class 1) ----
; Y = 0 on entry and kept 0 throughout the scan.
p1page: inc CURPTR+1            ; rare: list crossed a page
        jmp p1loop
p1next: lda CURPTR
        clc
        adc #4
        sta CURPTR
        bcs p1page
p1loop: lda CURPTR
        cmp SENDL
        bne p1go
        lda CURPTR+1
        cmp SENDH
        beq p1done
p1go:   lda (CURPTR),y          ; tier byte
        tax
        and #$0F
        cmp #1
        bne p1next              ; not this pass: 1-byte skip
        ; heavy capture or promotion; X = tier (victimtype<<4 | 1)
        ldy #3
        lda (CURPTR),y
        sta MVFLAGS
        and #FL_PROMO
        bne p1promo
        ldy PLY                 ; delta filter: one unsigned tier compare
        txa                     ;  (X = tier = victimtype<<4 | class)
        cmp THRT,y
        bcs sload               ; tier >= threshold: search it
p1rej:  ldy #0
        beq p1next              ; always
p1promo:
        ldy PLY                 ; promos skip delta; qs takes queen only
        lda QSKIND,y
        beq sload
        lda MVFLAGS
        and #FL_PROMO
        cmp #QUEEN
        bne p1rej
sload:  ldy #1                  ; searched: fetch from/to and go
        lda (CURPTR),y
        sta FROM
        iny
        lda (CURPTR),y
        sta TO
        jmp sgo
p1done: ldy PLY                 ; -> pass 2 over the same list
        lda CLSPRES,y           ; class presence: skip an empty pass 2
        and #$02                ;  scan outright (bit 1 = light present)
        bne p1lgt
        jmp p2done              ; (p2done re-loads Y = PLY itself)
p1lgt:  lda #2
        sta PASSNO,y
        lda PLYBASELO,y
        sta CURPTR
        lda PLYBASEHI,y
        sta CURPTR+1
        ldy #0
        beq p2loop              ; always

; ---- pass 2: light captures (tier class 2) ----
p2page: inc CURPTR+1
        jmp p2loop
p2next: lda CURPTR
        clc
        adc #4
        sta CURPTR
        bcs p2page
p2loop: lda CURPTR
        cmp SENDL
        bne p2go
        lda CURPTR+1
        cmp SENDH
        beq p2done
p2go:   lda (CURPTR),y          ; tier byte
        tax
        and #$0F
        cmp #2
        bne p2next
        ; light capture (never a promo); X = tier
        ldy #3
        lda (CURPTR),y
        sta MVFLAGS
        ldy PLY                 ; delta filter: one unsigned tier compare
        txa
        cmp THRT,y
        bcs sload
        ldy #0
        beq p2next              ; always
p2done: ldy PLY
        lda QSKIND,y
        beq :+
        jmp sdone               ; qs: captures only
:       lda FUTILE,y
        beq :+
        jmp sdone               ; futility: quiets can't raise alpha
:       lda FEATURES
        and #FT_KILLER
        beq p2tonk              ; killers off: single quiet pass
        lda #3                  ; killer pass: mirror this ply's killers
        sta PASSNO,y
        lda KILLER1F,y
        sta KF1
        lda KILLER1T,y
        sta KT1
        lda KILLER2F,y
        sta KF2
        lda KILLER2T,y
        sta KT2
        lda PLYBASELO,y
        sta CURPTR
        lda PLYBASEHI,y
        sta CURPTR+1
        jmp p3loop
p2tonk: lda #4
        sta PASSNO,y
        lda PLYBASELO,y
        sta CURPTR
        lda PLYBASEHI,y
        sta CURPTR+1
        jmp p4nk

; ---- pass 0: scan for the TT move (from/to match) ----
p0page: inc CURPTR+1
        jmp p0loop
p0next: lda CURPTR
        clc
        adc #4
        sta CURPTR
        bcs p0page
p0loop: lda CURPTR
        cmp SENDL
        bne p0go
        lda CURPTR+1
        cmp SENDH
        beq p0done
p0go:   ldy #1
        lda (CURPTR),y
        cmp TTF0
        bne p0next
        sta FROM
        iny
        lda (CURPTR),y
        cmp TTT0
        bne p0next
        sta TO
        ldy #3
        lda (CURPTR),y
        sta MVFLAGS
        lda #0
        tay
        sta (CURPTR),y          ; consume: passes 1-4 skip it by tier
        jmp sgo
p0done: ldy PLY                 ; TT pass done: captures next
        lda CLSPRES,y           ; class presence: skip an empty pass 1
        lsr                     ;  scan outright (bit 0 = heavy present)
        bcs p0hvy
        jmp p1done              ; no heavies: pass-2 presence decides
p0hvy:  lda #1
        sta PASSNO,y
        lda PLYBASELO,y
        sta CURPTR
        lda PLYBASEHI,y
        sta CURPTR+1
        ldy #0
        beq p1loopj             ; always
p1loopj:
        jmp p1loop

; ---- pass 3: killer quiets (tier $04 matching a killer) ----
p3page: inc CURPTR+1
        jmp p3loop
p3next: lda CURPTR
        clc
        adc #4
        sta CURPTR
        bcs p3page
p3loop: lda CURPTR
        cmp SENDL
        bne p3go
        lda CURPTR+1
        cmp SENDH
        beq p3done
p3go:   ldy #0
        lda (CURPTR),y
        cmp #$04
        bne p3next              ; captures/consumed: other passes
        iny
        lda (CURPTR),y          ; from
        cmp KF1
        beq p3f1
        cmp KF2
        bne p3next              ; matches neither killer's from
        iny                     ; killer-2 from: its to must match too
        lda (CURPTR),y
        cmp KT2
        bne p3next
        beq p3hit               ; always
p3f1:   iny
        lda (CURPTR),y          ; to
        cmp KT1
        beq p3hit
        dey                     ; killer-1 from, other to: killer 2?
        lda (CURPTR),y
        cmp KF2
        bne p3next
        iny
        lda (CURPTR),y
        cmp KT2
        bne p3next
p3hit:  ldy #1                  ; a killer: load the move and search it
        lda (CURPTR),y
        sta FROM
        iny
        lda (CURPTR),y
        sta TO
        iny
        lda (CURPTR),y
        sta MVFLAGS
        lda #0                  ; consume: zero the tier so pass 4 skips it
        tay                     ;  like the TT move — no killer compares (or
        sta (CURPTR),y          ;  killer ZP mirrors) needed there (r4)
        jmp sgo
p3done: ldy PLY                 ; -> final quiet pass
        lda #4
        sta PASSNO,y
        lda PLYBASELO,y
        sta CURPTR
        lda PLYBASEHI,y
        sta CURPTR+1
        jmp p4nk
sdonej: jmp sdone

; ---- pass 4: every remaining quiet (tier $04). Killers-on nodes reach
; here after pass 3 CONSUMED its killers (tier zeroed), so one loop
; serves both FT_KILLER settings — the old by-value killer-exclusion
; copy is gone (deep opt r4). ----
p4nkpg: inc CURPTR+1
        jmp p4nk
p4nknx: lda CURPTR
        clc
        adc #4
        sta CURPTR
        bcs p4nkpg
p4nk:   lda CURPTR
        cmp SENDL
        bne p4nkgo
        lda CURPTR+1
        cmp SENDH
        beq sdonej
p4nkgo: ldy #0
        lda (CURPTR),y
        cmp #$04
        bne p4nknx
        iny
        lda (CURPTR),y
        sta FROM
        iny
        lda (CURPTR),y
        sta TO
        ldy #3
        lda (CURPTR),y
        sta MVFLAGS
        jmp sgo

; sloopret: re-enter the move loop after a recursion (or an unmade
; illegal move): rebuild the ZP loop state from the per-ply arrays
; and dispatch on PASSNO.
sloopret:
        ldy PLY
        lda CURSORLO,y
        sta CURPTR
        lda CURSORHI,y
        sta CURPTR+1
        lda PLYENDLO,y
        sta SENDL
        lda PLYENDHI,y
        sta SENDH
        ldx PASSNO,y
        bpl :+                  ; PASS_TTDEF: the staged TT move did not cut,
        jmp srdefer             ;  so the real list still has to be built
:       beq srp0
        cpx #3
        bcs srp34
        ldy #0
        cpx #1
        bne srp2
        jmp p1loop
srp2:   jmp p2loop
srp0:   lda TTFROMA,y           ; pass 0: refresh the TT-move mirrors
        sta TTF0
        lda TTTOA,y
        sta TTT0
        jmp p0loop
srp34:  cpx #3
        bne srp4                ; pass 4 reads no killer state at all
        lda KILLER1F,y          ; pass 3: refresh the killer mirrors
        sta KF1
        lda KILLER1T,y
        sta KT1
        lda KILLER2F,y
        sta KF2
        lda KILLER2T,y
        sta KT2
        jmp p3loop
srp4:   jmp p4nk

; ---------------------------------------------------------------
; FT2_GENDEFER: deferred full-width generation at snode.
;
; sntry  - the TT offers a move for this ply. Validate it against the
;          board (ttmovevalid) and, if it is a move `generate` would
;          have emitted, STAGE it as a real 4-byte move-stack record
;          and search it with no generation at all. ~86% of these cut,
;          and that is the whole saving.
; srdefer- it did not cut (or was illegal): build the real list now,
;          consume the TT move from it exactly as pass 0 would have
;          (zero its tier byte), and join the normal passes at p0done.
;
; The staged record sits AT PLYBASE[PLY] with tier 0, so:
;   - setmove4, the sdemote scout refetch and the killer/BESTFLAGS
;     (T0),y reads all work unchanged (they address CURSOR-4);
;   - spop's MSP restore is unchanged (PLYBASE is still this node's
;     entry MSP);
;   - passes 1-4, which rescan from PLYBASE, skip it on the same
;     single tier compare that skips a consumed pass-0 move today.
; Generation, when it happens, therefore starts at PLYBASE+4 and the
; consume scan must start there too - the staged record carries the
; same from/to and would match first.
; ---------------------------------------------------------------
sntry:  jsr ttmovevalid         ; C set: pseudo-legal here, MVFLAGS = the
        bcs :+                  ;  flags generate would have emitted
        jmp snp0                ; declined: generate and hunt it in the list
:
; ---- GDVERIFY (debug, ASSEMBLY-TIME optional - same pattern as
; CKVERIFY): prove live that the validator is never MORE permissive
; than generate. Build the real list right here, where it would go
; anyway, require (TTF0, TTT0, MVFLAGS) to be in it, then throw the
; list away and stage as usual. Exit 102 on disagreement. Only this
; direction is checked: being too STRICT is a documented, tree-neutral
; outcome (promotions and castles), while being too permissive makes
; an illegal move. TestGenDeferVerify builds `ca65 -D GDVERIFY`.
.ifdef GDVERIFY
        lda MSP
        sta GDVMSP
        lda MSP+1
        sta GDVMSP+1
        lda #0
        sta CLSP
        jsr generate
        lda GDVMSP
        sta CURPTR
        lda GDVMSP+1
        sta CURPTR+1
gdvloop:lda CURPTR
        cmp MSP
        bne gdvgo
        lda CURPTR+1
        cmp MSP+1
        bne gdvgo
        lda #102                ; validated a move generate does not emit
        sta EXIT_TRAP
gdvgo:  ldy #1
        lda (CURPTR),y
        cmp TTF0
        bne gdvnext
        iny
        lda (CURPTR),y
        cmp TTT0
        bne gdvnext
        iny
        lda (CURPTR),y
        cmp MVFLAGS
        beq gdvfound
gdvnext:lda CURPTR
        clc
        adc #4
        sta CURPTR
        bcc gdvloop
        inc CURPTR+1
        jmp gdvloop
gdvfound:
        lda GDVMSP              ; discard the list and stage as usual
        sta MSP
        lda GDVMSP+1
        sta MSP+1
.endif
        ldy PLY
        lda MSP
        sta PLYBASELO,y
        sta CURPTR
        lda MSP+1
        sta PLYBASEHI,y
        sta CURPTR+1
        ldy #0
        tya
        sta (MSP),y             ; tier 0 = "consumed by pass 0"
        lda TTF0
        iny
        sta (MSP),y             ; from
        sta FROM
        lda TTT0
        iny
        sta (MSP),y             ; to
        sta TO
        lda MVFLAGS
        iny
        sta (MSP),y             ; flags
        lda MSP                 ; MSP past the record: children (and the
        clc                     ;  real list, if it is ever built) go above
        adc #4
        sta MSP
        bcc :+
        jsr flushpage           ; page cross + move-stack overflow trap
:       ldy PLY
        lda #PASS_TTDEF
        sta PASSNO,y
        jmp sgo                 ; (PLYEND/SEND are unset until srdefer
                                ;  builds the list; nothing reads them
                                ;  before then - sloopret reloads SEND
                                ;  from PLYEND, and srdefer rewrites both)

srdefer:
        lda #0
        sta CLSP                ; class-presence accumulator (as gennodef)
        jsr generate            ; MSP is back at PLYBASE+4: every child
        ldy PLY                 ;  restores it, so the list lands there
        lda MSP
        sta PLYENDLO,y
        sta SENDL
        lda MSP+1
        sta PLYENDHI,y
        sta SENDH
        lda CLSP
        sta CLSPRES,y
        lda TTFROMA,y           ; refresh the TT-move mirrors (a child's
        sta TTF0                ;  own snode clobbered the ZP copies)
        lda TTTOA,y
        sta TTT0
        lda PLYBASELO,y         ; scan from the first GENERATED move
        clc
        adc #4
        sta CURPTR
        lda PLYBASEHI,y
        adc #0
        sta CURPTR+1
        jmp sdcloop
sdcpage:
        inc CURPTR+1
        jmp sdcloop
sdcnext:
        lda CURPTR
        clc
        adc #4
        sta CURPTR
        bcs sdcpage
sdcloop:
        lda CURPTR
        cmp SENDL
        bne sdcgo
        lda CURPTR+1
        cmp SENDH
        beq sdcdone             ; not in the list: only reachable if
                                ;  ttmovevalid was MORE permissive than
                                ;  generate, which the exhaustive gate
                                ;  (and the GDVERIFY variant) forbid
sdcgo:  ldy #1
        lda (CURPTR),y
        cmp TTF0
        bne sdcnext
        iny
        lda (CURPTR),y
        cmp TTT0
        bne sdcnext
        lda #0                  ; consume it: passes 1-4 skip it by tier.
        tay                     ;  Exactly one entry can match - promotions
        sta (CURPTR),y          ;  (the only repeated from/to) are rejected
sdcdone:
        jmp p0done              ; (p0done reloads Y = PLY itself)

; sgo: search the move at CURPTR (FROM/TO/MVFLAGS loaded): advance
; the cursor past it and persist for sloopret/setmove4, then make.
sgo:    ldy PLY
        lda CURPTR
        clc
        adc #4
        sta CURPTR
        sta CURSORLO,y
        lda CURPTR+1
        adc #0
        sta CURPTR+1
        sta CURSORHI,y
        ; fall through to sdomove
sdomove:
        jsr make
        ; lazy legality (perf review F1): when the mover was not in
        ; check, only king moves, ep captures, and moves leaving a
        ; king-aligned square can possibly be illegal. Everything else
        ; skips the full attacked() scan. Conservative: alignment means
        ; "run the full check", not "illegal".
        ldx PLY
        lda INCHK-1,x           ; parent ply's in-check state
        bne slfull
        lda MVFLAGS
        and #FL_EP
        bne slfull
        lda MVPIECE
        and #TYPEMASK
        cmp #KING
        beq slfull
        lda SIDE
        eor #COLORMASK
        asl
        tay
        lda PIECESQ,y           ; mover's king square
        sec
        sbc FROM
        clc
        adc #$77
        tay
        lda ATTACKTAB,y
        and #ATK_DIAG|ATK_ORTHO
        bne slfull              ; from-square king-aligned: maybe pinned
        jmp slegal              ; provably legal
slfull: ; full check: mover must not leave their king attacked
        lda SIDE
        sta ATSIDE
        eor #COLORMASK
        asl
        tay
        lda PIECESQ,y
        sta ATSQ
        jsr attacked
        bcc slegal
        jsr unmake
sloopj: jmp sloopret
slegal: ldy PLY                 ; PLY = child here
        dey
        lda LEGALCNT,y
        clc
        adc #1
        sta LEGALCNT,y
        ; ---- child window mode (FT_LMR: PVS + late move reductions):
        ; 0 = full window, 1 = zero-window scout, 2/3 = scout reduced
        ; by 1/2. The first legal move always gets the full window;
        ; reductions only for late quiets (pass 4), never in check,
        ; never for checking moves, remaining >= 3.
        ; QS capture nodes take none of this: SMODE[ply] was preset to 0
        ; at qs node entry (qsdelta), so they skip straight to the child
        ; search (deep opt r4).
        lda QSKIND,y
        bne sqgo
        ldx #0
        lda FEATURES
        and #FT_LMR
        beq smset
        lda LEGALCNT,y
        cmp #2
        bcc smset               ; first legal move: the PV move
        ldx #1                  ; later moves: zero-window scout
        cpy #0
        beq smset               ; root moves: scout but never reduce
        lda PASSNO,y
        cmp #4
        bne smset
        lda INCHK,y
        bne smset
        lda INCHK+1,y
        bne smset               ; the move gives check: don't reduce
        lda LEGALCNT,y
        cmp #4
        bcc smset               ; late: >= 3 moves already searched
        lda MAXDEPTH            ; ZP PLY is the CHILD ply here (Y = PLY-1),
        sec                     ;  so MAXDEPTH-PLY = remaining-1: compare
        sbc PLY                 ;  one lower and skip the T2 staging (r4)
        cmp #2
        bcc smset               ; remaining < 3: too shallow to reduce
        ldx #2                  ; reduce by 1
        cmp #4
        bcc smset
        lda LEGALCNT,y
        cmp #7
        bcc smset               ; very late (>= 6 searched) and deep:
        ldx #3                  ;  reduce by 2
smset:  txa
        sta SMODE,y
sqgo:   iny
ssearch:                        ; (re)search entry; Y = child ply
        ; child window: BETA[c] = -ALPHA[p] always; ALPHA[c] is
        ; -BETA[p] (full window) or BETA[c] - 1 = NOT ALPHA[p] (scout:
        ; -x - 1 is the one's complement, so no carry chain and no
        ; T0/T1 staging — deep opt r4)
        sec
        lda #0
        sbc ALPHALO-1,y
        sta BETALO,y
        lda #0
        sbc ALPHAHI-1,y
        sta BETAHI,y
        lda SMODE-1,y
        beq swfull
        tax                     ; X = mode (>= 1: scout or reduced scout)
        lda ALPHALO-1,y
        eor #$FF
        sta ALPHALO,y
        lda ALPHAHI-1,y
        eor #$FF
        sta ALPHAHI,y
        cpx #2
        bcc snored
        lda MAXDEPTH            ; reduced scout: shrink the horizon
        pha                     ;  for the subtree by mode-1 plies
        sec
        sbc SMODE-1,y
        clc
        adc #1
        sta MAXDEPTH
        jsr search
        pla
        sta MAXDEPTH
        jmp spostsr
        ; ---- check extension (FT_CKEXT): the move just made gives check
        ; (INCHK[child], set by make), so search the child ONE PLY DEEPER -
        ; MAXDEPTH+1 for the subtree instead of the usual horizon - capped at
        ; ONE extension per root-to-leaf path (NUMEXT, incremented/decremented
        ; in lockstep so the budget is exact and a scout re-search through
        ; ssearch re-derives the same decision). Never in quiescence: a qs
        ; capture node's children take the plain path (QSKIND[parent]), while an
        ; in-check evasion node past the horizon is full-width in both engines
        ; and DOES extend - mirror.checkExt's qsKind test exactly. Reached only
        ; from the mode 0/1 path, so an extension can never coexist with an LMR
        ; reduction (a checking move is mode < 2 anyway: the smset reduction
        ; gate tests INCHK+1). Mirrors mirror.checkExt with MaxExt = 1.
sckext: lda FEATURES
        and #FT_CKEXT
        beq snoxt
        lda NUMEXT              ; per-path budget spent? (MaxExt = 1)
        bne snoxt
        lda QSKIND-1,y          ; parent is a qs capture node: never extend
        bne snoxt
ckexted:                        ; (labelled for the parity harness's probe)
        inc NUMEXT
        inc MAXDEPTH            ; one ply deeper for the whole child subtree
        jsr search
        dec MAXDEPTH
        dec NUMEXT
        jmp spostsr
snoxt:  jsr search
        jmp spostsr
swfull: sec                     ; full window: ALPHA[c] = -BETA[p]
        lda #0
        sbc BETALO-1,y
        sta ALPHALO,y
        lda #0
        sbc BETAHI-1,y
        sta ALPHAHI,y
snored: lda INCHK,y             ; Y = child ply: does the move give check?
        bne sckext              ;  yes: check-extension candidate (out of line)
        jsr search
spostsr:
        jsr unmake
        ; scout results: a fail-high zero-window score is not final
        ldy PLY
        lda SMODE,y
        beq snegf               ; full-window result: negate, then final
        ; scout: fail high iff -SCORE > ALPHA, i.e. ALPHA + SCORE < 0 —
        ; tested on the RAW child score, so a failed-low scout (the
        ; common outcome) skips the 16-bit negate entirely (deep opt r4)
        clc
        lda ALPHALO,y
        adc SCORE
        lda ALPHAHI,y
        adc SCORE+1
        bvc :+
        eor #$80
:       bmi :+                  ; sum < 0: the scout failed high
        jmp sloopret            ; failed low: SCORE <= ALPHA < BETA, so the
                                ;  beta-cutoff and alpha-raise tests below
                                ;  are provably no-ops — next move (r4)
:       sec                     ; fail high: now negate for the demote /
        lda #0                  ;  zero-window-final paths
        sbc SCORE
        sta SCORE
        lda #0
        sbc SCORE+1
        sta SCORE+1
        lda SMODE,y
        cmp #2
        bcs sdemote1            ; reduced: retry unreduced, zero window
        ; unreduced scout: full-window retry only if the window is
        ; open (BETA - ALPHA >= 2); at a zero-window node the fail-
        ; high result is final (the cutoff check below fires)
        sec
        lda BETALO,y
        sbc ALPHALO,y
        sta T0
        lda BETAHI,y
        sbc ALPHAHI,y
        bne sdemote0            ; difference >= 256: open
        lda T0
        cmp #2
        bcs sdemote0
        bcc scut                ; always (zero-window node: SCORE > ALPHA
                                ;  = BETA-1 means SCORE >= BETA, so the
                                ;  beta test is a proven cutoff — r4)
sdemote1:
        lda #1
        bne :+                  ; always
sdemote0:
        lda #0
:       sta SMODE,y
        ; refetch the move (cursor is 4 past it) and re-make it;
        ; legality is already proven, no attacked() re-check
        lda CURSORLO,y
        sec
        sbc #4
        sta T0
        lda CURSORHI,y
        sbc #0
        sta T1
        ldy #1
        lda (T0),y
        sta FROM
        iny
        lda (T0),y
        sta TO
        iny
        lda (T0),y
        sta MVFLAGS
        jsr make
        ldy PLY
        jmp ssearch
snegf:  sec                     ; SCORE = -SCORE (full-window result)
        lda #0
        sbc SCORE
        sta SCORE
        lda #0
        sbc SCORE+1
        sta SCORE+1
scheckbc:
        ; beta cutoff?
        sec
        lda SCORE
        sbc BETALO,y
        lda SCORE+1
        sbc BETAHI,y
        bvc :+
        eor #$80
:       bmi snocut              ; SCORE < BETA
scut:   lda BETALO,y            ; fail-hard: return BETA
        sta SCORE
        lda BETAHI,y
        sta SCORE+1
        lda QSKIND,y            ; TT: lower bound + the cutting move
        bne sbetapop
        jsr setmove4
        ; killers: remember quiet cutoff moves
        lda FEATURES
        and #FT_KILLER
        beq snokupd
        ldx TTENTRY+4
        lda BOARD,x             ; board is restored: nonzero = capture
        bne snokupd
        ldy #3
        lda (T0),y              ; setmove4 left T0 at the move
        and #FL_EP|FL_PROMO
        bne snokupd
        ldy PLY
        lda TTENTRY+3
        cmp KILLER1F,y
        bne skupd
        lda TTENTRY+4
        cmp KILLER1T,y
        beq snokupd             ; already killer 1
skupd:  lda KILLER1F,y
        sta KILLER2F,y
        lda KILLER1T,y
        sta KILLER2T,y
        lda TTENTRY+3
        sta KILLER1F,y
        lda TTENTRY+4
        sta KILLER1T,y
snokupd:
        lda SCORE
        sta TTENTRY+5
        lda SCORE+1
        sta TTENTRY+6
        lda #TT_LOWER
        jsr ttstore
sbetapop:
        jmp spop
snocut: ; alpha improvement? (strict >)
        sec
        lda ALPHALO,y
        sbc SCORE
        lda ALPHAHI,y
        sbc SCORE+1
        bvc :+
        eor #$80
:       bmi :+                  ; SCORE > ALPHA: improvement
        jmp sloopret
:       lda SCORE
        sta ALPHALO,y
        lda SCORE+1
        sta ALPHAHI,y
        ; record this move (cursor was already advanced by 4)
        lda #1
        sta RAISED,y
        jsr setmove4
        ldy PLY
        lda TTENTRY+3
        sta TTBF,y
        lda TTENTRY+4
        sta TTBT,y
        cpy #0
        beq :+
        jmp sloopret
:       lda TTENTRY+3           ; root: also for the driver
        sta BESTFROM
        lda TTENTRY+4
        sta BESTTO
        ldy #3
        lda (T0),y              ; setmove4 left T0 at the move
        sta BESTFLAGS
        jmp sloopret

sdone:  ; return alpha; full-width nodes with no legal moves: mate/stalemate
        ldy PLY
        lda QSKIND,y
        bne sretqs
        lda LEGALCNT,y
        bne sret
        lda FUTILE,y
        bne sretqs              ; quiets were pruned: can't claim mate
        lda INCHK,y             ; computed at node entry
        bne smated
        lda #0                  ; stalemate
        sta SCORE
        sta SCORE+1
        beq sterm               ; always
smated: lda PLY                 ; SCORE = PLY - MATE (mated here)
        sec
        sbc #<MATE
        sta SCORE
        lda #0
        sbc #>MATE
        sta SCORE+1
sterm:  lda #NOSQ               ; TT: exact, no move
        sta TTENTRY+3
        sta TTENTRY+4
        lda SCORE
        sta TTENTRY+5
        lda SCORE+1
        sta TTENTRY+6
        lda #TT_EXACT
        jsr ttstore
        jmp spop
sret:   lda ALPHALO,y           ; (Y = PLY from sdone)
        sta SCORE
        lda ALPHAHI,y
        sta SCORE+1
        ; TT: exact if alpha was raised here, else upper bound
        lda TTBF,y
        sta TTENTRY+3
        lda TTBT,y
        sta TTENTRY+4
        lda SCORE
        sta TTENTRY+5
        lda SCORE+1
        sta TTENTRY+6
        lda #TT_UPPER
        ldx RAISED,y
        beq :+
        lda #TT_EXACT
:       jsr ttstore
        jmp spop
sretqs: lda ALPHALO,y           ; (Y = PLY from sdone)
        sta SCORE
        lda ALPHAHI,y
        sta SCORE+1
spop:   ldy PLY
        lda PLYBASELO,y
        sta MSP
        lda PLYBASEHI,y
        sta MSP+1
        rts

; setmove4: TTENTRY+3/4 = the from/to of the move at cursor[PLY] - 4
; (the move just searched; the cursor advances before make). Leaves
; T0/T1 pointing at the move for the callers' flags reads; must not
; touch CURPTR (the live loop cursor, restored by sloopret).
setmove4:
        ldy PLY
        lda CURSORLO,y
        sec
        sbc #4
        sta T0
        lda CURSORHI,y
        sbc #0
        sta T1
        ldy #1
        lda (T0),y
        sta TTENTRY+3
        iny
        lda (T0),y
        sta TTENTRY+4
        rts

; ---------------------------------------------------------------
; ttmovevalid (FT2_GENDEFER): would `generate` emit (TTF0 -> TTT0) in
; THIS position?
;   Out: carry SET   = yes, and MVFLAGS holds the exact flags byte
;                      generate would have written for it;
;        carry CLEAR = no, or a case we decline to reconstruct - the
;                      caller then generates the list as usual.
; Clobbers A, X, Y and GTMP/GPIECE plus atgeo's scratch (ATSQ/ATTMP/
; ATBITS/DIFF/ATDELTA), all dead at snode entry.
;
; ** This routine must never be MORE PERMISSIVE than generate. **
; asm/tt.s indexes on 12 bits and ttfetch verifies 24 more, so a TT hit
; against a DIFFERENT position survives with p = 2^-20 - about one
; wrong-position TT move per 25 games at the shipped control. Today the
; eager generation IS that validator (pass 0 simply fails to find such a
; move); with generation deferred, this routine is the only thing
; standing between a stale TT entry and an illegal make() that corrupts
; the board and piece lists irrecoverably. Being too STRICT only costs
; cycles (the caller falls back to generation and pass 0 finds the move
; in the list, same tree). TestTTMoveValidExhaustive compares this
; routine against generate() over all 128x128 (from,to) pairs per corpus
; position; the `ca65 -D GDVERIFY` variant re-generates at every staged
; node and traps (exit 102) on a live disagreement.
;
; DELIBERATE REJECTIONS (fall through to generation; ~2.4% of TT moves):
;   - PROMOTIONS. The TT stores from/to only, and pass 0 today searches
;     ALL FOUR promo variants in list order N,B,R,Q - so the move the TT
;     cutter searches first today is the KNIGHT promo. Replaying a single
;     reconstructed promotion would change the tree.
;   - CASTLING. Validating it needs two attacked() scans (~656 cycles).
;     It falls out for free: ATTACKTAB has no ATK_KING bit for a
;     two-file king step, so the geometry test below rejects it.
; En passant and double pushes reconstruct exactly and ARE validated.
; ---------------------------------------------------------------
ttmovevalid:
        lda TTF0                ; both squares must be ON the 0x88 board:
        and #$88                ;  BOARD is a 128-byte window and a stale
        bne ttmvno              ;  TT entry can hold anything
        lda TTT0
        and #$88
        bne ttmvno
        ldx TTF0
        lda BOARD,x
        beq ttmvno              ; empty from-square
        sta GTMP                ; the mover's piece byte
        eor SIDE
        and #COLORMASK
        bne ttmvno              ; not a piece of the side to move
        ldx TTT0
        lda BOARD,x
        sta GPIECE              ; the target byte (0 = empty square)
        beq ttmvocc
        eor SIDE
        and #COLORMASK
        beq ttmvno              ; own piece on the target (also to == from)
ttmvocc:
        lda GTMP
        and #TYPEMASK
        cmp #PAWN
        beq ttmvpawn
        ; ---- knight / bishop / rook / queen / king: pure geometry.
        ; atgeo answers "does the piece on TTF0 attack TTT0 along this
        ; difference", walking the ray with the same blocker test
        ; generate's rays make; the target has already been proved empty
        ; or enemy, so attack == pseudo-legal move. No move of these
        ; types ever carries flags. A castle has no ATK_KING bit for its
        ; two-file difference and is rejected here (see the header).
        lda #0
        sta MVFLAGS
        lda TTT0
        sta ATSQ                ; atgeo walks its ray toward ATSQ
        clc
        adc #$77                ; TTT0 <= $77, so no carry out
        tax                     ; X = ATSQ + $77 (atgeo's diff base)
        sec
        sbc TTF0
        tay                     ; Y = TTT0 - TTF0 + $77
        lda ATTACKTAB,y
        beq ttmvno              ; no geometric relation at all
        ldy TTF0                ; atgeo: A = bits, Y = from, X = base
        jmp atgeo               ; carry = the verdict

ttmvno: clc
        rts

        ; ---- pawns: pushes, double pushes, captures and en passant,
        ; by colour. A move onto the last rank is a PROMOTION and is
        ; rejected before anything else (see the header).
ttmvpawn:
        lda TTT0
        sec
        sbc TTF0                ; d = to - from (both on board)
        ldx SIDE
        bne ttmvbp
        ldx TTT0                ; white promotes onto rank 8 ($7x)
        cpx #$70
        bcs ttmvno
        cmp #$10
        beq ttmvpush
        cmp #$20
        beq ttmvdblw
        cmp #$0F                ; +$0F / +$11: the two capture diagonals
        beq ttmvcap             ;  (no file wrap: both squares are on
        cmp #$11                ;  board, so d fixes the file shift)
        beq ttmvcap
        clc
        rts
ttmvbp: ldx TTT0                ; black promotes onto rank 1 ($0x)
        cpx #$08
        bcc ttmvno
        cmp #$F0
        beq ttmvpush
        cmp #$E0
        beq ttmvdblb
        cmp #$F1
        beq ttmvcap
        cmp #$EF
        beq ttmvcap
        clc
        rts

ttmvpush:
        lda GPIECE              ; single push: the target must be EMPTY
        bne ttmvno
        lda #0
        sta MVFLAGS
        sec
        rts
ttmvdblw:
        lda GPIECE              ; double push: target empty, home rank,
        bne ttmvno              ;  and the skipped square empty too
        lda TTF0
        and #$F0
        cmp #$10                ; white doubles from rank 2
        bne ttmvno
        lda TTF0
        clc
        adc #$10
        jmp ttmvdbl2
ttmvdblb:
        lda GPIECE
        bne ttmvno
        lda TTF0
        and #$F0
        cmp #$60                ; black doubles from rank 7
        bne ttmvno
        lda TTF0
        sec
        sbc #$10
ttmvdbl2:
        tax
        lda BOARD,x
        bne ttmvno2
        lda #FL_DOUBLE
        sta MVFLAGS
        sec
        rts
ttmvno2:clc                     ; (second reject exit: branch range)
        rts
ttmvcap:
        lda GPIECE              ; diagonal: a real capture, or en passant
        beq ttmvep              ;  onto the (empty) ep square
        lda #0                  ; enemy piece there - own pieces and
        sta MVFLAGS             ;  promotions were rejected above
        sec
        rts
ttmvep: lda TTT0
        cmp EPSQ                ; EPSQ = NOSQ when there is none, which
        bne ttmvno2             ;  never equals an on-board TTT0
        lda #FL_EP
        sta MVFLAGS
        sec
        rts

; ---------------------------------------------------------------
; ttmvsweep (GDVERIFY variant only): the exhaustive-equivalence
; harness entry. A 2^-20 failure mode cannot be gated by sampling, so
; TestTTMoveValidExhaustive checks EVERY (from,to) pair of the 128
; square codes - including the off-board 0x88 ones - against
; generate()'s output, for every corpus position. Doing that from Go
; one machine per pair is 16384 emulator boots per position, so the
; sweep runs on the 6502: 32 from-squares per call (4 calls cover the
; 128), 128 result bytes each.
;   In:  GDVBASE = the first from-square of this chunk.
;   Out: GDVBUF + (from - GDVBASE)*128 + to = (carry<<7) | MVFLAGS.
; ---------------------------------------------------------------
.ifdef GDVERIFY
ttmvsweep:
        lda #<GDVBUF
        sta CURPTR
        lda #>GDVBUF
        sta CURPTR+1
        lda GDVBASE
        sta TTF0
tmsf:   lda #0
        sta TTT0
tmst:   jsr ttmovevalid
        lda #0
        bcc :+
        lda #$80                ; bit 7 = validated
:       ora MVFLAGS
        ldy TTT0
        sta (CURPTR),y
        inc TTT0
        lda TTT0
        bpl tmst                ; to = 0..127
        lda CURPTR              ; next from: 128 bytes on
        clc
        adc #$80
        sta CURPTR
        bcc :+
        inc CURPTR+1
:       inc TTF0
        lda TTF0
        sec
        sbc GDVBASE
        cmp #32
        bcc tmsf
        rts
.endif

        .segment "TABLES"

; TIERTAB: victim piece byte -> move tier (victimtype<<4 | class).
; Only the low 3 type bits matter (0 = empty square -> quiet); heavy
; class 1 for victims >= rook, light class 2 below. A pseudo-legal
; king "capture" must always be searched: its victim type (6) tops the
; tier ordering, so it clears every reachable THRT threshold.
; (The old VV16L/VV16H victim-value rows are gone — the delta filter
; compares tier bytes directly against THRT, deep opt r4.)
        .align 256
TIERTAB:
.repeat 32
        .byte $04                       ; empty: quiet
        .byte (PAWN<<4)|2, (KNIGHT<<4)|2, (BISHOP<<4)|2
        .byte (ROOK<<4)|1, (QUEEN<<4)|1, (KING<<4)|1
        .byte $04                       ; type 7: never occurs
.endrepeat

        .segment "CODE"
