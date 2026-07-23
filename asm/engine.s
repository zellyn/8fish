; Engine driver (M2): the rig pokes BOARD, PIECESQ, SIDE, EPSQ, CASTLE,
; HALFMOVE, and MAXDEPTH, then runs this binary. It searches to MAXDEPTH
; (plus quiescence), prints the best move in UCI form ("e2e4", "e7e8q")
; + LF via the COUT trap, and exits 0. With no legal moves it prints
; "none" and exits 2 (SCORE distinguishes mate from stalemate; the rig
; reads SCORE/BESTFROM/etc. from zero page).

        .include "defs.inc"

        .import __LCCODE_LOAD__, __LCCODE_RUN__, __LCCODE_SIZE__

        .segment "CODE"

entry:
        ldx #$FF
        txs
        ; install the LC-resident code (aux-read primitives): bank 1
        ; RAM, write-enabled by the double read
        lda $C08B
        lda $C08B
        lda #<__LCCODE_LOAD__
        sta ZPTR
        lda #>__LCCODE_LOAD__
        sta ZPTR+1
        lda #<__LCCODE_RUN__
        sta TTPTR
        lda #>__LCCODE_RUN__
        sta TTPTR+1
        ldx #>(__LCCODE_SIZE__ + 255)
        ldy #0
ldloop: lda (ZPTR),y
        sta (TTPTR),y
        iny
        bne ldloop
        inc ZPTR+1
        inc TTPTR+1
        dex
        bne ldloop
        lda #0
        sta PSP0
        sta PSP1
        sta ABORT
        sta NODECNT
        jsr evalinit
        lda #NOSQ
        sta BESTFROM
        ; depth cap; MAXDEPTH becomes the per-iteration depth
        lda MAXDEPTH
        sta MAXCAP
        ; hard-abort limit = 2x budget, saturating at 24 bits
        lda BUDGET0
        asl
        sta ABORTL0
        lda BUDGET1
        rol
        sta ABORTL1
        lda BUDGET2
        rol
        sta ABORTL2
        bcc :+
        lda #$FF
        sta ABORTL0
        sta ABORTL1
        sta ABORTL2
:
        ; fixed-depth mode (budget 0): one iteration at the cap
        lda BUDGET0
        ora BUDGET1
        ora BUDGET2
        bne idmode
        lda MAXCAP
        sta CURDEPTH
        jsr iterate
        jmp report
idmode: lda #1
        sta CURDEPTH
idloop: lda CLOCK_TRAP          ; iteration start time (latched 24-bit)
        sta ITSTART0
        lda CLOCK_TRAP+1
        sta ITSTART1
        lda CLOCK_TRAP+2
        sta ITSTART2
        jsr aspiterate          ; iterate CURDEPTH, aspiration window (FT_ASP)
        lda ABORT
        beq idok
        ; aborted mid-iteration: a partial iteration's "best" is just the
        ; first root move it happened to search (fail-hard alpha starts at
        ; -INF), so prefer the last COMPLETED iteration's move whenever
        ; one exists. (D9's improved-on-previous-score refinement needs
        ; score bookkeeping; this is the safe subset.) Also restore that
        ; iteration's score and depth, so the reported score is not the
        ; abort dummy and CURDEPTH is the depth actually completed.
        lda PREVFROM
        cmp #NOSQ
        bne :+
        jmp report              ; iteration 1 aborted: keep what we have
:       sta BESTFROM
        lda PREVTO
        sta BESTTO
        lda PREVFLAGS
        sta BESTFLAGS
        lda PREVSC0
        sta SCORE
        lda PREVSC1
        sta SCORE+1
        dec CURDEPTH            ; iteration 1 never aborts, so >= 1
        jmp report
reportj:
        jmp report
idok:   ; a winning mate is exact and final: deepening can't improve it
        lda SCORE+1
        bmi :+
        cmp #MATEZONEHI
        bcs reportj
:       ; predictive gate: the next iteration is estimated at 2x the
        ; one just finished (QS-dominated growth ratios run 2-6, and
        ; the 2x-budget hard abort still backstops underestimates).
        ; Start it only if now + 2*cost fits inside the full budget -
        ; otherwise stop HERE with the completed move, instead of
        ; burning up to 1.5x budget on a doomed iteration (measured:
        ; ~25% of middlegame moves were hard-aborting).
        lda CLOCK_TRAP          ; latch now; cost = now - ITSTART
        sec
        sbc ITSTART0
        sta T0
        lda CLOCK_TRAP+1
        sbc ITSTART1
        sta T1
        lda CLOCK_TRAP+2
        sbc ITSTART2
        asl T0                  ; est = 2*cost, saturating
        rol T1
        rol
        bcs reportj             ; overflow: nowhere near fitting
        sta T2
        lda CLOCK_TRAP          ; now + est (CLOCK latch: low read
        clc                     ;  relatches; same tick as above or
        adc T0                  ;  a hair later - both fine)
        sta T0
        lda CLOCK_TRAP+1
        adc T1
        sta T1
        lda CLOCK_TRAP+2
        adc T2
        bcs report              ; overflow: can't fit
        sta T2
        lda T0                  ; fits iff now + est <= BUDGET
        cmp BUDGET0
        lda T1
        sbc BUDGET1
        lda T2
        sbc BUDGET2
        bcs report              ; projected past the budget: stop now
        inc CURDEPTH
        lda CURDEPTH
        cmp MAXCAP
        bcc idloopj
        beq idloopj
        jmp report
idloopj:
        jmp idloop

; iterate: run one full-window search at CURDEPTH, saving the previous
; iteration's best move first. Fixed-depth mode uses this directly; the
; budget-mode ID driver goes through aspiterate (below), which reuses the
; same per-iteration setup via itsearch.
iterate:
        lda BESTFROM
        sta PREVFROM
        lda BESTTO
        sta PREVTO
        lda BESTFLAGS
        sta PREVFLAGS
        lda SCORE               ; previous iteration's root score
        sta PREVSC0
        lda SCORE+1
        sta PREVSC1
        lda #NOSQ
        sta BESTFROM
        lda CURDEPTH
        sta MAXDEPTH
        lda #0
        sta PLY
        sta HVALID              ; hash watermark: HASH = root position
        jsr curincheck          ; root in-check state (make propagates
        lda #0                  ; it for every deeper ply)
        rol
        sta INCHK
        lda #<MOVESTACK
        sta MSP
        lda #>MOVESTACK
        sta MSP+1
        lda #<(-INF)
        sta ALPHALO
        lda #>(-INF)
        sta ALPHAHI
        lda #<INF
        sta BETALO
        lda #>INF
        sta BETAHI
        jmp search              ; rts returns to iterate's caller

report: lda BESTFROM
        cmp #NOSQ
        beq nomove
        jsr printsq
        lda BESTTO
        jsr printsq2
        lda BESTFLAGS
        and #FL_PROMO
        beq endline
        tax
        lda promochar,x
        sta COUT_TRAP
endline:
        lda #$0A
        sta COUT_TRAP
        lda #0
        sta EXIT_TRAP
        brk

nomove: ldx #0
:       lda nonetxt,x
        beq :+
        sta COUT_TRAP
        inx
        bne :-
:       lda #2
        sta EXIT_TRAP
        brk

; printsq: A = 0x88 square of BESTFROM; printsq2 same for any square.
printsq:
        lda BESTFROM
printsq2:
        pha
        and #$0F
        clc
        adc #'a'
        sta COUT_TRAP
        pla
        lsr
        lsr
        lsr
        lsr
        clc
        adc #'1'
        sta COUT_TRAP
        rts

promochar:
        .byte 0, 0, 'n', 'b', 'r', 'q', 0, 0
nonetxt:
        .byte "none", $0A, 0

        .include "search.s"
        .include "tt.s"
        .include "eval.s"
        .include "board.s"
        .include "movegen.s"
        .include "tables.s"

        ; Aspiration code lives in CODE (not the TABLES segment tables.s
        ; leaves active): appended at CODE's end it shifts the page-aligned
        ; TABLES/LCCODE only by whole pages, so no hot-path instruction's
        ; low byte — and no page-crossing cycle count — changes. Placing it
        ; in TABLES instead grows that segment by a non-page amount and
        ; perturbs the LC-install loop's cost (FT_ASP-off cycles must be
        ; bit-identical to the pre-aspiration engine).
        .segment "CODE"

; aspiterate: budget-mode ID wrapper that applies aspiration windows
; (FT_ASP) around iteration CURDEPTH. It first saves the previous
; completed iteration's result (BEST* -> PREV*, SCORE -> PREVSC) — the
; abort-recovery snapshot AND the aspiration seed — then runs the
; iteration. With FT_ASP off, CURDEPTH == 1, or the seed in a mate zone
; it is a plain full-window iteration (identical tree to iterate).
; Otherwise it opens (prev-ASPDELTA, prev+ASPDELTA) and re-searches on a
; fail-low/high, opening ONLY the failing bound (asymmetric) until the
; score lands inside the window, a mate appears, or the third fail forces
; the full window. On a budget abort mid-(re)search ABORT stays set and
; control returns to idloop, which discards the whole iteration (restores
; PREV*), exactly as for iterate. Mirrors mirror.aspIterate with
; AspirationParams{Delta:25, Policy:AspAsym}, including its fail-low/high
; definitions (s <= alpha with alpha > -INF, s >= beta with beta < INF)
; and widening order.
aspiterate:
        lda BESTFROM            ; save previous completed iteration (once)
        sta PREVFROM
        lda BESTTO
        sta PREVTO
        lda BESTFLAGS
        sta PREVFLAGS
        lda SCORE               ; PREVSC = seed for the aspiration window
        sta PREVSC0
        lda SCORE+1
        sta PREVSC1
        lda FEATURES            ; aspiration disabled? -> full window
        and #FT_ASP
        beq aspfull
        lda CURDEPTH            ; iteration 1 -> full window
        cmp #2
        bcc aspfull
        lda PREVSC1             ; seed in a mate zone? -> full window
        bpl aspspos             ; hi $00-$7F: test winning-mate zone
        cmp #NMATEZONEHI        ; hi $80-$8B (< $8C): losing mate
        bcc aspfull
        bcs aspnarrow           ; hi $8C-$FF: negative, not a mate
aspspos:
        cmp #MATEZONEHI         ; hi $74-$7F: winning mate
        bcs aspfull
        bcc aspnarrow           ; not a mate seed: use a narrow window
aspfull:
        lda #<(-INF)            ; full-window single iteration
        sta ASPAL
        lda #>(-INF)
        sta ASPAH
        lda #<INF
        sta ASPBL
        lda #>INF
        sta ASPBH
        jmp itsearch            ; tail: search's rts returns to idloop
aspnarrow:
        ; window = (prev - ASPDELTA, prev + ASPDELTA). A non-mate seed is
        ; well inside +/-INF, so prev+/-ASPDELTA never needs clamping.
        sec
        lda PREVSC0
        sbc #ASPDELTA
        sta ASPAL
        lda PREVSC1
        sbc #0
        sta ASPAH
        clc
        lda PREVSC0
        adc #ASPDELTA
        sta ASPBL
        lda PREVSC1
        adc #0
        sta ASPBH
        lda #0
        sta ASPFAIL
asploop:
        jsr itsearch            ; run CURDEPTH with the [ASPA,ASPB] window
        lda ABORT
        beq :+
        rts                     ; budget hit mid-(re)search: discard iteration
:       ; fail-low? SCORE <= ASPA and ASPA > -INF.
        lda ASPAH               ; ASPA == -INF ($8300)? then no fail-low
        cmp #>(-INF)
        bne aspchklo
        lda ASPAL
        cmp #<(-INF)
        beq aspchkhi            ; ASPA == -INF: skip the fail-low test
aspchklo:
        sec                     ; ASPA - SCORE; result >= 0 => SCORE <= ASPA
        lda ASPAL
        sbc SCORE
        lda ASPAH
        sbc SCORE+1
        bvc :+
        eor #$80
:       bmi aspchkhi            ; ASPA < SCORE: not a fail-low
        jsr aspbump             ; fail-low; C=1 if mate/3rd fail forces full
        bcs aspwidefull
        lda #<(-INF)            ; asymmetric: open only alpha
        sta ASPAL
        lda #>(-INF)
        sta ASPAH
        jmp asploop
aspchkhi:
        ; fail-high? SCORE >= ASPB and ASPB < INF.
        lda ASPBH               ; ASPB == INF ($7D00)? then no fail-high
        cmp #>INF
        bne aspchkhi2
        lda ASPBL
        cmp #<INF
        beq aspacc              ; ASPB == INF: score inside window -> accept
aspchkhi2:
        sec                     ; SCORE - ASPB; result >= 0 => SCORE >= ASPB
        lda SCORE
        sbc ASPBL
        lda SCORE+1
        sbc ASPBH
        bvc :+
        eor #$80
:       bpl aspfh               ; SCORE >= ASPB: fail-high
aspacc:
        rts                     ; score inside window: accept the iteration
aspfh:
        jsr aspbump             ; fail-high
        bcs aspwidefull
        lda #<INF               ; asymmetric: open only beta
        sta ASPBL
        lda #>INF
        sta ASPBH
        jmp asploop
aspwidefull:
        lda #<(-INF)            ; mate/3rd fail: full window, guaranteed
        sta ASPAL               ;  terminal (no clip, no further fail)
        lda #>(-INF)
        sta ASPAH
        lda #<INF
        sta ASPBL
        lda #>INF
        sta ASPBH
        jmp asploop

; aspbump: register a fail. Increments ASPFAIL and returns C=1 when the
; full window must be forced now — the failing score (in SCORE) is itself
; in a mate zone (a narrow window must never clip a mate) or this is the
; third fail — else C=0 (open only the failing bound).
aspbump:
        inc ASPFAIL
        lda SCORE+1             ; inMateZone(SCORE)?
        bpl aspbpos
        cmp #NMATEZONEHI
        bcc aspbforce           ; losing-mate score
        bcs aspbcnt             ; negative, not a mate
aspbpos:
        cmp #MATEZONEHI
        bcs aspbforce           ; winning-mate score
aspbcnt:
        lda ASPFAIL
        cmp #3                  ; the third fail forces the full window
        bcs aspbforce
        clc                     ; widen the failing bound only
        rts
aspbforce:
        sec
        rts

; itsearch: run one search at CURDEPTH with the root window held in
; ASPAL/H (alpha) and ASPBL/H (beta). Resets the per-iteration root state
; exactly like iterate (best move, ply, hash watermark, in-check flag,
; move-stack pointer), then enters search (whose rts returns to
; itsearch's caller). aspiterate re-runs this per aspiration re-search.
itsearch:
        lda #NOSQ
        sta BESTFROM
        lda CURDEPTH
        sta MAXDEPTH
        lda #0
        sta PLY
        sta HVALID
        jsr curincheck
        lda #0
        rol
        sta INCHK
        lda #<MOVESTACK
        sta MSP
        lda #>MOVESTACK
        sta MSP+1
        lda ASPAL
        sta ALPHALO
        lda ASPAH
        sta ALPHAHI
        lda ASPBL
        sta BETALO
        lda ASPBH
        sta BETAHI
        jmp search              ; rts returns to itsearch's caller

        ; Resident opening-book probe (bookentry/bookprobe). Appended at the
        ; very end of CODE — after the aspiration block — so it moves nothing
        ; before it and shifts the page-aligned TABLES/LCCODE only by whole
        ; pages: the bookless search path stays byte-identical (see book.s).
        ; Reached only via bookentry, which the harness/bridge invokes when a
        ; book is loaded; the normal $4000 entry never touches it.
        .segment "CODE"
        .include "book.s"

        ; ---------------------------------------------------------------
        ; mopfin / mopcd: the mop-up term's tail (close term + signed apply)
        ; and its corner-distance helper. Deliberately placed HERE, at the
        ; end of the CODE segment, to fill the page-alignment padding between
        ; CODE's end and the page-aligned TABLES base (~48 bytes that would
        ; otherwise be wasted fill). The image is nearly full, so relocating
        ; this ~48-byte tail out of TABLES (mopupterm's home) into that free
        ; CODE slack is what lets the feature fit MAIN at all. mopupterm
        ; reaches mopfin by `jmp mopfin` with A = manhattan(kings); mopcd is
        ; reached by `jsr mopcd` (absolute, cross-segment — fine). Neither
        ; grows CODE past the page boundary, so the hot code and the
        ; page-aligned TABLES base do not move (FT2_MOPUP-off byte-identical).
        ; ---------------------------------------------------------------
        .segment "CODE"
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

        ; ---------------------------------------------------------------
        ; mopupterm (FT2_MOPUP): phase-gated endgame mop-up eval term.
        ; Appended at CODE's end (like the aspiration/book blocks) so the
        ; hot search/eval/board/movegen code before it does not move and
        ; TABLES/LCCODE shift only by whole pages; it is cold in the 0x1f
        ; default (never called unless FT2_MOPUP is set). eval jsr's here
        ; after the pstruct/extraterm adds and BEFORE the side-to-move
        ; negation, so the WHITE-POV bonus carries the right sign.
        ;
        ; Fires only when PHASE <= 6 (endgame) AND the signed material edge
        ; (winning minus losing side, mirror mopMatVal values) is >= 450
        ; (a rook). Then it drives the LOSING king toward a corner
        ; (Edge*MOPCMD[losingKing], Edge=10) and pulls the WINNING king in
        ; (Close*(14 - manhattan(kings)), Close=4), max ~116cp < a minor —
        ; so it breaks the won-endgame shuffling draw without ever trading
        ; material. Mirrors internal/mirror mopupEval (DefaultMopup) exactly.
        ;
        ; Material is one piece-list pass, reached only in low-phase
        ; endgames — cheaper and simpler than a maintained signed-material
        ; accumulator that would tax every make/unmake on the hot path for
        ; a signal used only when this rare gate opens.
        ;
        ; Reads PHASE/PIECESQ/BOARD, writes SCORE (16-bit ZP). Clobbers
        ; A,X,Y,T0,T1,T2,EVTMP,MULCNT,PSQPIECE — all dead in eval's POV/
        ; tempo/seed tail (its only caller). No multiply, no tables beyond
        ; MOPCMD (packed-square corner distance) and MOPMATLO/HI.
        ;
        ; Placed in the TABLES segment (the page-aligned MAIN tail, beside
        ; its MOPCMD/MOPMAT data) rather than CODE: this image has only a
        ; few bytes of CODE slack before its length rolls to a new page and
        ; shoves the page-aligned TABLES base up a whole 256 bytes. Keeping
        ; this cold routine in TABLES avoids that tax; it is still ordinary
        ; MAIN RAM ($4000-$BEFF), so eval's `jsr mopupterm` runs it normally.
        ; ---------------------------------------------------------------
        .segment "TABLES"
mopupterm:
        lda PHASE
        cmp #7                  ; PHASE <= 6 ?  (unsigned: PHASE < 7)
        bcc mopg
        rts                     ; middlegame: term 0, SCORE unchanged
mopg:   ; signed material diff (white - black) -> T1:T0
        lda #0
        sta T0
        sta T1
        ldx #31
mopms:  ldy PIECESQ,x           ; Y = square (NOSQ = $FF, bit 7 set)
        bmi mopmx               ; empty slot: skip
        lda a:BOARD,y
        and #TYPEMASK
        tay                     ; Y = piece type 0..6 (king -> 0 value)
        cpx #16
        bcs mopmb               ; slot >= 16 -> black: subtract
        clc                     ; white: add value
        lda T0
        adc MOPMATLO,y
        sta T0
        lda T1
        adc MOPMATHI,y
        sta T1
        jmp mopmx
mopmb:  sec                     ; black: subtract value
        lda T0
        sbc MOPMATLO,y
        sta T0
        lda T1
        sbc MOPMATHI,y
        sta T1
mopmx:  dex
        bpl mopms
        ; winner index X and |diff|: X=0 white leads, X=1 black leads.
        ; diff sign is T1's bit 7; negate in place for the black case so a
        ; single UNSIGNED compare decides the >= 450 (a rook) gate.
        ldx #0                  ; assume white leads
        lda T1
        bpl mopc                ; diff >= 0
        inx                     ; black leads
        sec                     ; T1:T0 = -diff (= |diff|)
        lda #0
        sbc T0
        sta T0
        lda #0
        sbc T1
        sta T1
mopc:   lda T0                  ; |diff| >= 450 ?  (unsigned 16-bit)
        cmp #<450
        lda T1
        sbc #>450
        bcs mopw                ; carry set: |diff| >= 450, term fires
        rts                     ; edge < a rook: term 0
mopw:   stx MULCNT              ; 0 = white winner (add), 1 = black (subtract)
        ; cache the losing king (-> EVTMP) and the winning king (-> PSQSQ);
        ; slots differ by 16, so eor #16 swaps white<->black.
        ldy #16                 ; white winner -> losing king = black (+16)
        cpx #0
        beq :+
        ldy #0                  ; black winner -> losing king = white (+0)
:       lda PIECESQ,y
        sta EVTMP
        tya
        eor #16
        tay
        lda PIECESQ,y
        sta PSQSQ
        ; king-capture guard: a pseudo-legal node may have captured a king
        ; (square NOSQ); the score is irrelevant there, add nothing.
        lda EVTMP
        cmp #NOSQ
        bne :+
        rts
:       lda PSQSQ
        cmp #NOSQ
        bne :+
        rts
:       ; Edge * cornerDist(losingKing): cornerdist(file) + cornerdist(rank)
        ; via mopcd (no table); reproduces the mirror's mopupCMD values.
        lda EVTMP
        and #$07                ; losing king file
        jsr mopcd
        sta PSQPIECE            ; cornerdist(file)
        lda EVTMP
        lsr
        lsr
        lsr
        lsr                     ; losing king rank
        jsr mopcd
        clc
        adc PSQPIECE            ; CMD 0..6
        sta PSQPIECE
        asl                     ; CMD*2
        sta T2
        lda PSQPIECE
        asl
        asl
        asl                     ; CMD*8
        clc
        adc T2                  ; CMD*10 = Edge*CMD (<= 60)
        sta T2                  ; running bonus B
        ; manhattan(losing,winning king) = |lr-wr| + |lf-wf|
        lda EVTMP
        lsr
        lsr
        lsr
        lsr                     ; lr
        sta T0
        lda PSQSQ
        lsr
        lsr
        lsr
        lsr                     ; wr
        sec
        sbc T0
        bcs :+
        eor #$FF
        adc #1                  ; C=0 -> |lr - wr|
:       sta T1                  ; |rank diff|
        lda EVTMP
        and #$07                ; lf
        sta T0
        lda PSQSQ
        and #$07                ; wf
        sec
        sbc T0
        bcs :+
        eor #$FF
        adc #1                  ; |lf - wf|
:       clc
        adc T1                  ; manhattan (in A)
        ; close term + apply live in the CODE segment's page-tail slack
        ; (mopfin, engine.s) so the whole feature fits the near-full image.
        jmp mopfin

