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
        ; FT2_SOFTCLK: run on the ESTIMATED clock. CLOCK_TRAP is a live
        ; counter only under the harness; on a IIe it is plain RAM, so the
        ; engine estimates elapsed cycles into it (search.s checkclocks) and
        ; every existing reader is untouched. Two stores patch the operand of
        ; the ONE `jsr checkclock` in search over to the accumulating entry
        ; point `checkclocks` — which is what makes the feature-off path
        ; not merely equivalent but the identical instruction stream, so an
        ; A/B against the harness clock measures the estimator and nothing
        ; else. (CODE is ordinary RAM at $4000; the entry already
        ; block-copies LCCODE, so the image is not write-protected.)
        lda FEATURES2
        and #FT2_SOFTCLK
        beq scdone
        lda #<checkclocks
        sta ccsite+1
        lda #>checkclocks
        sta ccsite+2
        ; Prime the estimate (it is elapsed-since-entry, and on hardware this
        ; RAM powers up with garbage) with ONE table entry — the 128 nodes
        ; that run before the FIRST poll. NODECNT starts at 0, so the first
        ; poll lands on node 256 and charges nodes 129-256; without this,
        ; nodes 1-128 are never charged and every search reads ~0.4 s short,
        ; which measured as a −41% bias at a 1 s/move budget (a whole level's
        ; worth) and left sub-256-node searches estimating a flat ZERO. PHASE
        ; is live here: evalinit built it from the poked board.
        lda #0
        sta CLOCK_TRAP+2
        lda PHASE
        cmp #NPCOST
        bcc scph
        lda #NPCOST-1
scph:   tax
        lda PCOSTLO,x
        sta CLOCK_TRAP
        lda PCOSTHI,x
        sta CLOCK_TRAP+1
scdone:
        ; depth cap; MAXDEPTH becomes the per-iteration depth
        lda MAXDEPTH
        sta MAXCAP
        ; hard-abort limit = 2x budget. (The former 24-bit saturation clamp
        ; here was dead: a real per-move budget is ~10^5 256-cycle units, so
        ; 2*BUDGET never approaches 2^23. Dropped to reclaim CODE space; fixed-
        ; depth mode uses budget 0 and never reaches this in any case.)
        lda BUDGET0
        asl
        sta ABORTL0
        lda BUDGET1
        rol
        sta ABORTL1
        lda BUDGET2
        rol
        sta ABORTL2
        ; FT2_ADAPT: override ABORTL with 2*CEILMAX so a raised ceiling is not
        ; hard-aborted at 2*BASE. Zero effect (and byte-identical tree) when the
        ; bit is clear; adaptaborthi is appended at CODE's end.
        lda FEATURES2
        and #FT2_ADAPT
        beq :+
        jsr adaptaborthi
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
        jsr iterate             ; run iteration CURDEPTH
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
:       ; FT2_ADAPT: after this completed iteration, raise the movable ceiling
        ; (panic/instability) or request an early easy-stop. adaptmaybe is
        ; appended; it returns A=0 (Z=1) with the ceiling untouched when the bit
        ; is clear, so the flat predictive gate below is byte-identical.
        jsr adaptmaybe
        bne reportj             ; easy-stop: report the completed move now
        ; predictive gate: the next iteration is estimated at 2x the
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
; iteration's best move first (BEST* -> PREV*, SCORE -> PREVSC: the
; abort-recovery snapshot). Both the fixed-depth and the budget-mode ID
; drivers call this directly. (The FT_ASP aspiration wrapper that used to
; sit between idloop and this path was removed 2026-07-25 with the
; feature; see docs/results.md.)
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
        sta NUMEXT              ; FT_CKEXT: per-iterate reset of the check-
                                ;  extension path budget (the balanced
                                ;  increment/decrement keeps it 0 anyway)
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

        ; Resident opening-book probe (bookentry/bookprobe). Appended at the
        ; very end of CODE — so it moves nothing before it and shifts the
        ; page-aligned TABLES/LCCODE only by whole pages: the bookless search
        ; path stays byte-identical (see book.s).
        ; Reached only via bookentry, which the harness/bridge invokes when a
        ; book is loaded; the normal $4000 entry never touches it.
        .segment "CODE"
        .include "book.s"

        ; ---------------------------------------------------------------
        ; mopupterm (FT2_MOPUP): phase-gated endgame mop-up eval term.
        ; Appended at CODE's end (like the aspiration/book blocks) so the
        ; hot search/eval/board/movegen code before it does not move and
        ; TABLES/LCCODE shift only by whole pages; it is cold in the 0x1f
        ; default (never called unless FT2_MOPUP is set). eval jsr's here
        ; after the pstruct adds and BEFORE the side-to-move
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
        ; (mopfin, board.s: it fills RATTACK's alignment hole) so the whole
        ; feature fits the near-full image.
        jmp mopfin



; ===================================================================
; FT2_ADAPT adaptive time/effort management (mirror.SearchTimed port).
; Appended at CODE's end (after book.s), reached from the driver via the
; jsr adaptaborthi (entry) and jsr adaptmaybe (idok) hooks. All ceiling
; arithmetic is done HOST-SIDE (the host runs the per-game bank); it pokes
; BUDGET=base, CEILMAX=panic target = hard max = min(4*base,income+bank),
; UNSTCEIL=min(3*base,CEILMAX), MINSPEND=base/4. On-device the engine only
; compares and RAISES the movable ceiling, which IS the BUDGET word (mutated
; in place); the flat predictive gate still reads BUDGET, so with FT2_ADAPT
; clear BUDGET stays constant and the gate/tree are unchanged. Signals
; (adaptive-aggr): panic (root score dropped >=25cp vs last completed iter)
; -> BUDGET=max(BUDGET,CEILMAX); instability (root best move changed) ->
; BUDGET=max(BUDGET,UNSTCEIL); easy-stop once the best move has been stable
; for StableIters=2 past MinDepth=2 with |drop|<=30cp (ScoreFlat) and
; >= MINSPEND cycles spent.
; ===================================================================

; adaptaborthi: override the hard-abort limit with ABORTL = 2*CEILMAX (no
; saturation: 2*CEILMAX <= 8*base is far under 24 bits for any real budget).
; Called from entry only when FT2_ADAPT is set.
adaptaborthi:
        lda CEILMAX0
        asl
        sta ABORTL0
        lda CEILMAX1
        rol
        sta ABORTL1
        lda CEILMAX2
        rol
        sta ABORTL2
        rts

; adaptmaybe: post-completed-iteration policy. Returns A=0 (Z=1) to continue,
; A=1 (Z=0) to easy-stop. When FT2_ADAPT is clear it returns 0 immediately
; with the ceiling untouched (the flat search path is unchanged). Otherwise it
; maintains STABLE and raises the ceiling (BUDGET) on the panic/instability
; signals, mirroring mirror.SearchTimed's per-iteration block (adaptive-aggr).
; BEST*/SCORE = this iteration, PREV*/PREVSC = previous completed iteration
; (iterate snapshots them at each iteration start).
adaptmaybe:
        lda FEATURES2
        and #FT2_ADAPT
        bne @on
        lda #0                  ; flat: continue, ceiling untouched
        rts
@on:
        lda CURDEPTH
        cmp #2
        bcs @dgt1
        lda #1                  ; d==1: stable=1, no panic/unstable/stop
        sta STABLE
        lda #0
        rts
@dgt1:
        sec                     ; scoreDrop = PREVSC - SCORE (16-bit signed) -> T1:T0
        lda PREVSC0
        sbc SCORE
        sta T0
        lda PREVSC1
        sbc SCORE+1
        sta T1
        ; The subtract can OVERFLOW: root scores span [-MATE, MATEZONEHI*256)
        ; = [-30000, +29695] (idok reports a WINNING mate before we are called,
        ; but a LOSING mate falls straight through to here), so the true drop
        ; reaches +-59695 and does not fit in 16 bits. The sign of the true
        ; difference is N eor V, never N alone: "up 3 pawns, now getting mated"
        ; (drop >= +32768) wraps NEGATIVE and would suppress the panic exactly
        ; when it is most wanted, and the mirror image, a losing mate that
        ; evaporates, wraps POSITIVE into a spurious one. Matches the signed
        ; window compares in search.s, and mirror.SearchTimed, which computes
        ; the drop full-width. (T1:T0 keep the WRAPPED value for the @easy
        ; |drop| <= 30 test below: harmless, since a wrap needs |true drop| >
        ; 32767 and the widest reachable one is 59695, so the wrapped magnitude
        ; is always >= 65536-59695 = 5841 and can never read as flat.)
        bvc @nov                ; V=0: A is the true high byte
        eor #$80                ; V=1: N eor V is the true sign, and the true
        bmi @nopanic            ;  |drop| is >= 32768, so a positive one is a
        bpl @dopanic            ;  panic outright (one of these two is taken)
@nov:   bmi @nopanic            ; scoreDrop < 0 -> no panic
        bne @dopanic            ; high byte > 0 -> drop >= 256 >= 25 -> panic
        lda T0
        cmp #25
        bcc @nopanic            ; low byte < 25 -> no panic
@dopanic:
        lda CEILMAX0           ; panic target = CEILMAX (the hard max)
        sta RTARGET0
        lda CEILMAX1
        sta RTARGET1
        lda CEILMAX2
        sta RTARGET2
        jsr raise
@nopanic:
        lda BESTFROM            ; unstable if best move changed vs previous iter
        cmp PREVFROM
        bne @changed
        lda BESTTO
        cmp PREVTO
        bne @changed
        inc STABLE
        jmp @easy
@changed:
        lda #1
        sta STABLE
        lda UNSTCEIL0          ; instability target = UNSTCEIL
        sta RTARGET0
        lda UNSTCEIL1
        sta RTARGET1
        lda UNSTCEIL2
        sta RTARGET2
        jsr raise
@easy:
        lda STABLE              ; easy-stop: stable>=2, spent>=MINSPEND, |drop|<=30
        cmp #2
        bcc @nostop
        lda CLOCK_TRAP
        cmp MINSPEND0
        lda CLOCK_TRAP+1
        sbc MINSPEND1
        lda CLOCK_TRAP+2
        sbc MINSPEND2
        bcc @nostop
        lda T1                  ; |scoreDrop| <= 30 ?
        bpl @abs
        sec
        lda #0
        sbc T0
        sta T0
        lda #0
        sbc T1
        sta T1
@abs:
        lda T0
        cmp #31
        lda T1
        sbc #0
        bcs @nostop             ; |drop| >= 31 -> not flat -> keep searching
        lda #1
        rts
@nostop:
        lda #0
        rts

; raise: BUDGET = max(BUDGET, RTARGET). The caller stages the target (CEILMAX
; for panic, UNSTCEIL for instability; both host-poked and already clamped to
; CEILMAX) in RTARGET0-2. Only ever raises the movable ceiling. BUDGET0-2 ($E3)
; are contiguous ZP.
raise:
        lda BUDGET0
        cmp RTARGET0
        lda BUDGET1
        sbc RTARGET1
        lda BUDGET2
        sbc RTARGET2
        bcs @done              ; BUDGET >= RTARGET: no raise
        lda RTARGET0
        sta BUDGET0
        lda RTARGET1
        sta BUDGET1
        lda RTARGET2
        sta BUDGET2
@done:  rts
