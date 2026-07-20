; Negamax alpha-beta search with quiescence.
;
; Node protocol: caller sets ALPHA/BETA at the child's ply, then calls
; search with PLY already advanced by make. SCORE returns the fail-hard
; result from the side-to-move's POV. Full-width until PLY >= MAXDEPTH,
; then quiescence: stand pat + captures + queen promotions; when in
; check, a full evasion node instead (no stand pat, all moves, mate
; detection) — which is exactly a full-width node, so it reuses one.
;
; Move loop runs two passes over the generated list: pass 0 processes
; captures/promotions, pass 1 the quiets. QS capture-only nodes stop
; after pass 0.

; ---------------------------------------------------------------
; gennode: set base/cursor from MSP, generate, set end. QS capture
; nodes use the specialized captures-only generator copy.
; ---------------------------------------------------------------
gennode:
        ldy PLY
        lda MSP
        sta PLYBASELO,y
        sta CURSORLO,y
        lda MSP+1
        sta PLYBASEHI,y
        sta CURSORHI,y
        lda QSKIND,y
        beq :+
        ; recap2 gate: past RecapAfter=2 qs plies, restrict generateq
        ; to recaptures onto the previous move's TO (UNDOTO[PLY-1]).
        ; qs depth = PLY - MAXDEPTH (>= 0 here; MAXDEPTH is constant
        ; through a qs subtree - qs does no null/LMR reductions).
        lda #0
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
        sta RECAPONLY
genq:   jsr generateq
        jmp gennd2
:       jsr generate
gennd2: ldy PLY
        lda MSP
        sta PLYENDLO,y
        lda MSP+1
        sta PLYENDHI,y
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
        inc NODECNT
        inc NODECNT             ; +2: poll the clock every 128 nodes
        bne :+
        jsr checkclock
:       lda ABORT
        beq :+
        lda #0                  ; aborting: unwind with a dummy score
        sta SCORE
        sta SCORE+1
        rts
:       lda PLY
        cmp #MAXPLY-1
        bcc :+
        jmp eval                ; hard ply cap: static eval
:       lda PLY
        beq sdrawend            ; root: no draw checks; a move is required
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
.ifndef NOEVAL
        ; debug (FT_CKVERIFY): cross-check the propagated in-check flag
        ; against a full scan; a mismatch kills the run with code 101
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
        ldy PLY
        lda #0                  ; (INCHK,y is NOT cleared here: it was
        sta LEGALCNT,y          ;  propagated by make / the root driver)
        sta QSKIND,y
        sta RAISED,y
        sta FUTILE,y
        sta DELTATL,y
        lda #$80                ; delta threshold -32768: no delta pruning
        sta DELTATH,y           ; (qs stand-pat raises it when applicable)
        lda #NOSQ
        sta TTFROMA,y
        sta TTBF,y
        lda PLY
        cmp MAXDEPTH
        bcs squiesce            ; quiescence entry below
        ; full-width node: probe the transposition table
        jsr ttprobe
        bcc snodej
        ldy PLY
        lda TTENTRY+3
        sta TTFROMA,y           ; TT move: searched first (pass 0)
        lda TTENTRY+4
        sta TTTOA,y
        ; cutoff allowed if stored depth >= remaining depth, not at root
        lda PLY
        beq snodej
        lda TTENTRY+7
        lsr
        lsr
        sta T0
        lda MAXDEPTH
        sec
        sbc PLY
        cmp T0
        bcc ttcut               ; remaining < stored depth
        beq ttcut               ; equal: cutoff ok
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
        ldy PLY
        lda INCHK,y             ; propagated by make
        beq :+
        jmp snode               ; in check: full evasion node
:
        ldy PLY
        lda #1
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
:       bmi :+
        jmp qsdelta             ; no improvement
:       lda SCORE
        sta ALPHALO,y
        lda SCORE+1
        sta ALPHAHI,y
qsdelta:
        ; delta pruning threshold: search a capture only if its victim
        ; is worth at least alpha - standpat - margin. Disabled at low
        ; phase, where every pawn matters.
        lda PHASE
        cmp #6
        bcc qsnodelta
        sec
        lda ALPHALO,y
        sbc SCORE               ; SCORE still holds the stand-pat eval
        sta DELTATL,y
        lda ALPHAHI,y
        sbc SCORE+1
        sta DELTATH,y
        sec
        lda DELTATL,y
        sbc #<200               ; margin
        sta DELTATL,y
        lda DELTATH,y
        sbc #>200
        sta DELTATH,y
qsnodelta:
        jmp snode               ; qs nodes skip sprep (no null/futility)

; ---------------------------------------------------------------
; sprep: full-width-node pruning, before move generation.
; ---------------------------------------------------------------
sprep:  ldy PLY
        lda INCHK,y             ; propagated by make (root: the driver)
        beq :+
        jmp snode               ; in check: no null, no RFP, no futility
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
:       bmi snonull             ; below beta: search normally
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
sprepj: jmp snode

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

snode:  jsr gennode             ; picks generate/generateq via QSKIND
        ; initial pass: 0 (TT move first) when we have one and this is
        ; not a qs-capture node; else straight to pass 1
        ldy PLY
        ldx #1
        lda QSKIND,y
        bne :+
        lda TTFROMA,y
        cmp #NOSQ
        beq :+
        ldx #0
:       txa
        sta PASSNO,y
sloop:  ldy PLY
        lda CURSORLO,y
        cmp PLYENDLO,y
        bne sfetch              ; common: A = cursor lo, Y = PLY
        lda CURSORHI,y
        cmp PLYENDHI,y
        beq slpass              ; both bytes equal: end of list
        lda CURSORLO,y          ; rare (move stack crossed a page): reload lo
        jmp sfetch
slpass:
        ; end of list: 0 (TT move) -> 1 (heavy captures: promotions and
        ; victims >= rook) -> 2 (light captures) -> 3 (killers) ->
        ; 4 (quiets) -> done; qs and futility nodes stop after pass 2
        lda PASSNO,y
        cmp #4
        bcc :+
        jmp sdone
:       cmp #0
        beq spass1
        cmp #1
        beq spass2
        cmp #3
        beq spass4
        ; pass 2 (light captures) finished
        lda QSKIND,y
        beq :+
        jmp sdone               ; qs: captures only
:       lda FUTILE,y
        beq :+
        jmp sdone               ; futility: quiets can't raise alpha
:       lda FEATURES
        and #FT_KILLER
        bne spass3
        beq spass4              ; killers off: skip their pass
spass1: lda #1
        sta PASSNO,y
        bne spassgo             ; always
spass2: lda #2
        sta PASSNO,y
        bne spassgo             ; always
spass3: lda #3
        sta PASSNO,y
        bne spassgo             ; always
spass4: lda #4
        sta PASSNO,y
spassgo:
        lda PLYBASELO,y
        sta CURSORLO,y
        lda PLYBASEHI,y
        sta CURSORHI,y
        jmp sloop
sfetch: sta CURPTR              ; A = cursor lo (from sloop), Y = PLY
        ; advance cursor now, so skipping a move is just "jmp sloop"
        clc
        adc #3
        sta CURSORLO,y
        lda CURSORHI,y
        sta CURPTR+1            ; pointer high = unadvanced cursor high
        bcc :+
        adc #0                  ; carry from adc #3: bump the stored high
        sta CURSORHI,y
:       ldy #0
        lda (CURPTR),y
        sta FROM
        iny
        lda (CURPTR),y
        sta TO
        iny
        lda (CURPTR),y
        sta MVFLAGS
        ; pass 0: search only the TT move; passes 1/2 skip it
        ldy PLY
        lda PASSNO,y
        bne snotp0
        lda FROM
        cmp TTFROMA,y
        beq :+
        jmp sloop
:       lda TO
        cmp TTTOA,y
        beq :+
        jmp sloop
:       jmp sdomove
snotp0: ldx TTFROMA,y
        cpx #NOSQ
        beq snotttm
        cpx FROM
        bne snotttm
        ldx TTTOA,y
        cpx TO
        bne snotttm
        jmp sloop               ; the TT move: already searched in pass 0
snotttm:
        ; heavy captures in pass 1, light in pass 2, killer quiets in
        ; pass 3, remaining quiets in pass 4
        ldx TO
        lda BOARD,x
        bne siscap
        lda MVFLAGS
        and #FL_EP|FL_PROMO
        bne siscap
        lda PASSNO,y
        cmp #3
        beq squietk
        cmp #4
        beq squietr
        jmp sloop               ; capture passes: no quiets
squietk:                        ; killer pass: only killer matches
        lda KILLER1F,y
        cmp FROM
        bne :+
        lda KILLER1T,y
        cmp TO
        beq sdomovej
:       lda KILLER2F,y
        cmp FROM
        bne :+
        lda KILLER2T,y
        cmp TO
        beq sdomovej
:       jmp sloop
squietr:                        ; final pass: skip killers (already done)
        lda FEATURES
        and #FT_KILLER
        beq sdomovej
        lda KILLER1F,y
        cmp FROM
        bne :+
        lda KILLER1T,y
        cmp TO
        beq skskip
:       lda KILLER2F,y
        cmp FROM
        bne sdomovej
        lda KILLER2T,y
        cmp TO
        bne sdomovej
skskip: jmp sloop
sdomovej:
        jmp sdomove
siscap: lda PASSNO,y
        cmp #3
        bcc :+
        jmp sloop               ; captures were passes 1/2
:       ; promotions: always heavy; qs nodes take queen promos only
        lda MVFLAGS
        and #FL_PROMO
        beq scapvic
        lda PASSNO,y
        cmp #1
        beq :+
        jmp sloop               ; promos belong to the heavy pass
:       lda QSKIND,y
        beq sdomovej
        lda MVFLAGS
        and #FL_PROMO
        cmp #QUEEN
        beq sdomovej
        jmp sloop
scapvic:
        ; victim type: ep captures take a pawn
        lda MVFLAGS
        and #FL_EP
        beq :+
        ldx #PAWN
        bne scaptier            ; always
:       ldx TO
        lda BOARD,x
        and #TYPEMASK
        tax
scaptier:
        ; tier: victims >= rook are heavy (pass 1), others light (pass 2)
        lda PASSNO,y
        cpx #ROOK
        bcs :+
        cmp #2                  ; light capture: pass 2 only
        beq sdelta
        jmp sloop
:       cmp #1                  ; heavy capture: pass 1 only
        beq sdelta
        jmp sloop
sdelta: ; delta pruning: skip if the victim can't lift standpat to alpha
        sec
        lda VICVALL,x
        sbc DELTATL,y
        lda VICVALH,x
        sbc DELTATH,y
        bvc :+
        eor #$80
:       bpl sdomove             ; victim value >= threshold: search it
        jmp sloop
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
sloopj: jmp sloop
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
        ldx #0
        lda FEATURES
        and #FT_LMR
        beq smset
        lda QSKIND,y
        bne smset               ; qs capture nodes: full window
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
        sty T2
        lda MAXDEPTH
        sec
        sbc T2                  ; remaining depth at this node
        cmp #3
        bcc smset               ; too shallow to reduce
        ldx #2                  ; reduce by 1
        cmp #5
        bcc smset
        lda LEGALCNT,y
        cmp #7
        bcc smset               ; very late (>= 6 searched) and deep:
        ldx #3                  ;  reduce by 2
smset:  txa
        sta SMODE,y
        iny
ssearch:                        ; (re)search entry; Y = child ply
        ; child window: BETA[c] = -ALPHA[p] always; ALPHA[c] is
        ; -BETA[p] (full window) or BETA[c] - 1 (zero-window scout)
        sec
        lda #0
        sbc ALPHALO-1,y
        sta BETALO,y
        sta T0
        lda #0
        sbc ALPHAHI-1,y
        sta BETAHI,y
        sta T1
        lda SMODE-1,y
        beq swfull
        sec
        lda T0
        sbc #1
        sta ALPHALO,y
        lda T1
        sbc #0
        sta ALPHAHI,y
        jmp swgo
swfull: sec
        lda #0
        sbc BETALO-1,y
        sta ALPHALO,y
        lda #0
        sbc BETAHI-1,y
        sta ALPHAHI,y
swgo:   lda SMODE-1,y
        cmp #2
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
snored: jsr search
spostsr:
        sec                     ; SCORE = -SCORE
        lda #0
        sbc SCORE
        sta SCORE
        lda #0
        sbc SCORE+1
        sta SCORE+1
        jsr unmake
        ; scout results: a fail-high zero-window score is not final
        ldy PLY
        lda SMODE,y
        beq scheckbc            ; full-window result: final
        sec                     ; fail high iff SCORE > ALPHA (strict)
        lda ALPHALO,y
        sbc SCORE
        lda ALPHAHI,y
        sbc SCORE+1
        bvc :+
        eor #$80
:       bpl scheckbc            ; ALPHA >= SCORE: scout failed low
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
        bcc scheckbc            ; always (zero-window node)
sdemote1:
        lda #1
        bne :+                  ; always
sdemote0:
        lda #0
:       sta SMODE,y
        ; refetch the move (cursor is 3 past it) and re-make it;
        ; legality is already proven, no attacked() re-check
        lda CURSORLO,y
        sec
        sbc #3
        sta CURPTR
        lda CURSORHI,y
        sbc #0
        sta CURPTR+1
        ldy #0
        lda (CURPTR),y
        sta FROM
        iny
        lda (CURPTR),y
        sta TO
        iny
        lda (CURPTR),y
        sta MVFLAGS
        jsr make
        ldy PLY
        jmp ssearch
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
        lda BETALO,y            ; fail-hard: return BETA
        sta SCORE
        lda BETAHI,y
        sta SCORE+1
        lda QSKIND,y            ; TT: lower bound + the cutting move
        bne sbetapop
        jsr setmove3
        ; killers: remember quiet cutoff moves
        lda FEATURES
        and #FT_KILLER
        beq snokupd
        ldx TTENTRY+4
        lda BOARD,x             ; board is restored: nonzero = capture
        bne snokupd
        ldy #2
        lda (CURPTR),y          ; setmove3 left CURPTR at the move
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
        jmp sloop
:       lda SCORE
        sta ALPHALO,y
        lda SCORE+1
        sta ALPHAHI,y
        ; record this move (cursor was already advanced by 3)
        lda #1
        sta RAISED,y
        jsr setmove3
        ldy PLY
        lda TTENTRY+3
        sta TTBF,y
        lda TTENTRY+4
        sta TTBT,y
        cpy #0
        beq :+
        jmp sloop
:       lda TTENTRY+3           ; root: also for the driver
        sta BESTFROM
        lda TTENTRY+4
        sta BESTTO
        ldy #2
        lda (CURPTR),y          ; setmove3 left CURPTR at the move
        sta BESTFLAGS
        jmp sloop

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
sret:   ldy PLY
        lda ALPHALO,y
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
sretqs: ldy PLY
        lda ALPHALO,y
        sta SCORE
        lda ALPHAHI,y
        sta SCORE+1
spop:   ldy PLY
        lda PLYBASELO,y
        sta MSP
        lda PLYBASEHI,y
        sta MSP+1
        rts

; Victim values for delta pruning, by piece type. A pseudo-legal king
; "capture" must always be searched, hence the huge value.
VICVALL:
        .byte 0, <100, <320, <330, <500, <975, <20000, 0
VICVALH:
        .byte 0, >100, >320, >330, >500, >975, >20000, 0

; setmove3: TTENTRY+3/4 = the from/to of the move at cursor[PLY] - 3
; (the move just searched; the cursor advances before make).
setmove3:
        ldy PLY
        lda CURSORLO,y
        sec
        sbc #3
        sta CURPTR
        lda CURSORHI,y
        sbc #0
        sta CURPTR+1
        ldy #0
        lda (CURPTR),y
        sta TTENTRY+3
        iny
        lda (CURPTR),y
        sta TTENTRY+4
        rts
