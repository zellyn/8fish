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
        bmi @nopanic            ; scoreDrop < 0 -> no panic
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

; ===================================================================
; FT2_EGTECH endgame TECHNIQUE eval term (port of internal/mirror/
; endgame.go, DefaultEndgame: KingCent 8, KingPawn 6, Pass
; {0,0,10,20,40,60,100,0}, PassKingOur 6, PassKingThem 4, KingAhead 15,
; gate PHASE <= 6; mirror screen +10 +/- 9 over 4000 cycle-budgeted
; games, +2.08 +/- 0.71 match points/position on the conversion suite).
; White-POV bonus added straight into SCORE beside the mop-up, BEFORE
; eval's side-to-move negation, so the signs line up with every other
; term. The two REJECTED designed terms (Unstoppable = harmful,
; RookBehind = null) are deliberately NOT ported - dropping them also
; removes the only board-walking loop.
;
; Appended at the TABLES tail like mopupterm/adaptmaybe: it is cold in
; the middlegame (never called above the phase gate) and putting it here
; keeps CODE's page count - and every hot-path address - unchanged.
;
; DATA IT READS, all already maintained, so the term needs NO piece-list
; pass (the mirror's egScan is replaced entirely):
;   - PWBITS/PBBITS ($0200-$020F): the per-file pawn rank-occupancy
;     bitmaps pawnterm uses. They are maintained by pbtoggle XOR toggles
;     in make/unmake on EVERY pawn placement change, INDEPENDENT of
;     FT_PSTRUCT and of pawnterm's lazy PDIRTY deferral, so they are
;     always current here. This is the decision that makes the port
;     cheap: the per-file most-advanced rank is the index of the highest
;     (white) / lowest (black) set bit of one byte - a 5-byte shift loop,
;     run only for an actual passer - so the sketched PWMAX/PBMIN stash
;     in pawnterm is not needed at all, and (unlike that stash) the
;     nearest-pawn distance below stays EXACT on doubled files.
;   - WBLOCKM/BBLOCKM: pawnterm's own passed-pawn masks, so the passer
;     test here is bit-identical to pawnterm's (and term-exact with the
;     mirror's egMax3(pbmax) < r / egMin3(pwmin) > r).
;   - RANKBIT, PIECESQ, PHASE (the gate).
;
; ONE loop over the 8 files does everything: the nearest-pawn distance
; for BOTH kings and both colors' passer terms, so the per-file overhead
; is paid once (~19 cycles for a pawnless file).
;
; Scratch: A,X,Y and the eval-tail-dead bytes T0-T3, EVTMP, MULCNT,
; MUL0-MUL2, PSQSQ, PSQPIECE, PSP0+1, PSP1+1 (the PSQT pointer HIGH
; bytes; the lo bytes must stay 0 and are untouched), plus 6 bytes of
; free MAIN scratch at $0213 for the two kings' nearest-pawn state.
; Same contract as mopupterm plus T3/MUL*/PSP*+1 - all provably dead in
; eval's POV/tempo/seed tail, and every psqt user re-stores PSP0+1/
; PSP1+1 before use.
; ===================================================================
EGWK   = PSQSQ                  ; white king square
EGBK   = PSQPIECE               ; black king square
EGFILE = T0                     ; the passer's file (X mirror)
EGBITS = T1                     ; this file's own-color rank bits
EGR    = T2                     ; the passer's absolute rank
EGOK   = T3                     ; the passer owner's king square
EGTK   = EVTMP                  ; the defender's king square
EGSIGN = PSP1+1                 ; 0 = white passer (add), 1 = black (negate)
EGTMP  = PSP0+1                 ; egpass byte temp
EGT    = MUL0                   ; egcheb temps
EGT2   = MUL1
EGSQB  = MUL2                   ; egcheb: the second square
EGU    = MULCNT                 ; this file's pawns of EITHER color
EGDF   = T2                     ; nearest-pawn scan: |file - kingfile|
EGWLIM = T3                     ;  ... and the rank-window index limit
                                ; (both alias passer bytes that are dead
                                ;  while the per-file scan runs)
EGWKF  = $0213                  ; white king file / rank*8 / best distance
EGWKR8 = $0214
EGWBST = $0215
EGBKF  = $0216                  ; black king file / rank*8 / best distance
EGBKR8 = $0217
EGBBST = $0218

; EGPASS: the endgame-only passed-pawn top-up by ADVANCEMENT (white: the
; pawn's rank 1..6, black: 7-rank), mirror DefaultEndgame.Pass - the
; monotone replacement for pawnterm's Texel-tuned PASSEDBONUS
; {0,15,0,21,50,52,20,0}, whose non-monotone shape (7th rank worth less
; than the 5th) is exactly what failed to convert.
; CAVEAT (2026-07-25, docs/results.md): PASSEDBONUS is a top-up on the
; PSQT, and PSQT+bonus IS monotone from the 4th rank up, so "the base
; table is broken" is not a valid justification for this table; if
; FT2_EGTECH is ever revisited, EGPASS needs its own screen.
EGPASS: .byte 0, 0, 10, 20, 40, 60, 100, 0

; EGCD8: per-coordinate centre distance {3,2,1,0,0,1,2,3}; CMD(square) =
; EGCD8[file] + EGCD8[rank], the mop-up's mopcd computed by table instead
; of by branch (4 lookups per eval beat 4 jsr round trips).
EGCD8:  .byte 3, 2, 1, 0, 0, 1, 2, 3

; RWIN: rank-window masks for the nearest-pawn scan. RWIN[kr*8+d] = the
; ranks within d of rank kr, clamped to the board - so "is any pawn on
; this file within d ranks of the king?" is one AND. d = 7 always covers
; the whole file, which is why a pawn on the board is always found.
RWIN:
        .byte $01, $03, $07, $0F, $1F, $3F, $7F, $FF   ; king rank 0
        .byte $02, $07, $0F, $1F, $3F, $7F, $FF, $FF   ; king rank 1
        .byte $04, $0E, $1F, $3F, $7F, $FF, $FF, $FF   ; king rank 2
        .byte $08, $1C, $3E, $7F, $FF, $FF, $FF, $FF   ; king rank 3
        .byte $10, $38, $7C, $FE, $FF, $FF, $FF, $FF   ; king rank 4
        .byte $20, $70, $F8, $FC, $FE, $FF, $FF, $FF   ; king rank 5
        .byte $40, $E0, $F0, $F8, $FC, $FE, $FF, $FF   ; king rank 6
        .byte $80, $C0, $E0, $F0, $F8, $FC, $FE, $FF   ; king rank 7

; -------------------------------------------------------------------
; endterms (FT2_MOPUP / FT2_EGTECH): both endgame eval terms share ONE
; phase-gate compare, so a middlegame eval pays exactly the 7 cycles it
; already paid for the mop-up alone.
; -------------------------------------------------------------------
endterms:
        lda PHASE
        cmp #7                  ; PHASE <= 6 ? (the shared endgame gate)
        bcs endout
        lda FEATURES2
        and #FT2_MOPUP
        beq endeg
        jsr mopupterm           ; (re-checks the same gate: 7 cycles, and it
                                ;  keeps mopupterm a self-contained entry)
endeg:  lda FEATURES2
        and #FT2_EGTECH
        beq endout
        jmp egterm
endout: rts

; -------------------------------------------------------------------
; EGKPDF: one file's contribution to ONE king's nearest-pawn distance,
; expanded twice (once per king) inside the file loop. EGU = this file's
; pawns of either color (nonzero), X = the file.
;   cand = max(|file - kingfile|, min over pawn ranks r of |r - kingrank|)
;   best = min(best, cand)
; Two prunes make this cheap and bound the work: a file whose |df| already
; reaches `best` cannot improve it (skip before any rank work), and the
; rank window only grows while d < best. The window masks come from RWIN
; (ranks within d of the king's rank), so a step is 17 cycles.
; Clobbers A,Y,EGDF,EGWLIM; preserves X.
; -------------------------------------------------------------------
.macro EGKPDF kf, kr8, best
        .local skip, walk, hit
        txa                     ; |file - kingfile|
        sec
        sbc kf
        bcs :+
        eor #$FF
        adc #1
:       cmp best
        bcs skip                ; already >= best: this file cannot improve
        sta EGDF
        clc
        lda kr8
        adc best
        sta EGWLIM              ; stop the window at d = best
        ldy kr8                 ; Y = kingrank*8 + d, d = 0
walk:   lda EGU
        and RWIN,y              ; any pawn within d ranks of the king?
        bne hit
        iny
        cpy EGWLIM
        bcc walk
        bcs skip                ; always: nothing within best-1 ranks
hit:    tya
        sec
        sbc kr8                 ; d
        cmp EGDF
        bcs :+
        lda EGDF                ; cand = max(|df|, d)
:       sta best                ; (< best by construction of both prunes)
skip:
.endmacro

egterm:
        lda PIECESQ+0           ; king-capture guard: a pseudo-legal node can
        cmp #NOSQ               ;  have captured a king (the illegal-move
        bne :+                  ;  refutation); the score is irrelevant there,
egret:  rts                     ;  and the mirror's egEval bails the same way
:       sta EGWK
        lda PIECESQ+16
        cmp #NOSQ
        beq egret
        sta EGBK
        ; ---- KingCent: 8 * (CMD[theirK] - CMD[ourK]), white POV
        ; = 8 * (CMD[bk] - CMD[wk]). CMD is the mop-up's centre-manhattan
        ; distance (0 centre .. 6 corner) = EGCD8[file] + EGCD8[rank].
        lda EGBK
        and #$07
        tay
        lda EGCD8,y
        sta EGT
        lda EGBK
        lsr
        lsr
        lsr
        lsr
        tay
        lda EGCD8,y
        clc
        adc EGT
        sta EGT2                ; CMD[bk]
        lda EGWK
        and #$07
        tay
        lda EGCD8,y
        sta EGT
        lda EGWK
        lsr
        lsr
        lsr
        lsr
        tay
        lda EGCD8,y
        clc
        adc EGT                 ; CMD[wk]
        sta EGT
        lda EGT2
        sec
        sbc EGT                 ; -6..6
        beq egkc0
        asl
        asl
        asl                     ; *8 = -48..48 (fits a signed byte)
        jsr egadd
egkc0:  ; ---- per-king nearest-pawn state (file, rank*8, best-so-far).
        ; best = 8 means "no pawn on the board"; the two 8s then cancel in
        ; the KingPawn difference below, which is exactly the mirror's
        ; nPawns == 0 silence.
        lda EGWK
        and #$07
        sta EGWKF
        lda EGWK
        and #$70
        lsr                     ; rank*8 (RWIN row base)
        sta EGWKR8
        lda EGBK
        and #$07
        sta EGBKF
        lda EGBK
        and #$70
        lsr
        sta EGBKR8
        lda #8
        sta EGWBST
        sta EGBBST
        ; ---- the single file pass: nearest-pawn distances for both kings
        ; and both colors' passer terms.
        ldx #7
egfl:   lda PWBITS,x
        sta EGBITS              ; white pawns on this file
        ora PBBITS,x
        bne egfgo
egflnx: dex                     ; (the pawnless-file path is this tight loop:
        bpl egfl                ;  18 cycles, no work at all)
        jmp egkp
egfgo:  sta EGU
        EGKPDF EGWKF, EGWKR8, EGWBST
        EGKPDF EGBKF, EGBKR8, EGBBST
        lda EGBITS
        beq egflb               ; no white pawn here
        lda PBBITS,x            ; black pawns on files f-1..f+1
        cpx #0
        beq :+
        ora PBBITS-1,x
:       cpx #7
        beq :+
        ora PBBITS+1,x
:       ldy EGBITS
        and WBLOCKM,y           ; any black pawn at rank >= our best?
        bne egflb               ; blocked: not a passer
        ldy #$FF                ; r = the most advanced white pawn's rank
        lda EGBITS              ;  = index of the HIGHEST set bit
:       iny
        lsr
        bne :-
        sty EGR
        stx EGFILE
        lda EGWK
        sta EGOK
        lda EGBK
        sta EGTK
        lda #0
        sta EGSIGN              ; white owner: owner POV == white POV
        jsr egpass
        ldx EGFILE              ; egadd/egadds clobber X
egflb:  lda PBBITS,x
        bne :+
        jmp egflnx
:       sta EGBITS
        lda PWBITS,x            ; white pawns on files f-1..f+1
        cpx #0
        beq :+
        ora PWBITS-1,x
:       cpx #7
        beq :+
        ora PWBITS+1,x
:       ldy EGBITS
        and BBLOCKM,y           ; any white pawn at rank <= their best?
        beq :+
        jmp egflnx
:
        ldy #$FF                ; r = the most advanced black pawn's rank
        lda EGBITS              ;  = index of the LOWEST set bit
:       iny
        lsr
        bcc :-
        sty EGR
        stx EGFILE
        lda EGBK
        sta EGOK
        lda EGWK
        sta EGTK
        lda #1
        sta EGSIGN              ; black owner: negate each owner-POV term
        jsr egpass
        ldx EGFILE
        jmp egflnx
        ; ---- KingPawn: 6 * (dist(theirK) - dist(ourK)) to the nearest pawn
        ; of EITHER color, white POV = 6 * (bestB - bestW).
egkp:   lda EGBBST
        sec
        sbc EGWBST              ; -7..7 (0 when the board has no pawns)
        beq egout
        asl                     ; *2
        sta EGT
        asl                     ; *4
        clc
        adc EGT                 ; *6 = -42..42
        jsr egadd
egout:  rts

; -------------------------------------------------------------------
; egpass: score ONE passer from its OWNER's POV, adding each sub-term
; into SCORE via egadds (which flips the sign for a black owner). EGR =
; the pawn's absolute rank, EGFILE = its file, EGOK/EGTK = owner/
; defender king squares, EGSIGN = 0 white / 1 black. Each sub-term fits
; a signed byte but their SUM does not (100+24+12+15 = 151), so they are
; added one at a time; two's-complement addition is order-independent,
; so the total is exactly the mirror's egPasser sum.
; -------------------------------------------------------------------
egpass:
        ldy EGR                 ; advancement index: white r, black 7-r
        lda EGSIGN
        beq :+
        tya
        eor #$07
        tay
:       lda EGPASS,y
        beq :+                  ; zero top-up: skip the add
        jsr egadds
:       ldy EGSIGN              ; front square: the pawn's next step
        bne :+
        lda EGR
        clc
        adc #1                  ; white: r+1
        jmp :++
:       lda EGR
        sec
        sbc #1                  ; black: r-1
:       cmp #8
        bcs egpka               ; off board: no front square, no king terms
        asl
        asl
        asl
        asl
        ora EGFILE
        sta EGSQB               ; front square
        lda EGOK                ; PassKingOur: 6 * (4 - cheb(ourK, front))
        jsr egcheb
        sta EGTMP
        lda #4
        sec
        sbc EGTMP               ; -3..4
        beq :+
        asl                     ; *2
        sta EGTMP
        asl                     ; *4
        clc
        adc EGTMP               ; *6 = -18..24
        jsr egadds
:       lda EGTK                ; PassKingThem: 4 * (cheb(theirK, front) - 4)
        jsr egcheb
        sec
        sbc #4                  ; -4..3
        beq egpka
        asl
        asl                     ; *4 = -16..12
        jsr egadds
egpka:  ; KingAhead: our king strictly AHEAD of the pawn within one file
        ; (+15: the cheap key-square proxy); our king directly BEHIND it on
        ; the same file (-15: blocking its own pawn).
        lda EGOK
        and #$07
        sec
        sbc EGFILE
        bcs :+
        eor #$FF
        adc #1                  ; |kingfile - pawnfile| (C=0: adds exactly 1)
:       sta EGTMP
        lda EGOK
        lsr
        lsr
        lsr
        lsr                     ; king rank
        cmp EGR
        beq egpkb               ; same rank: not ahead
        ldy EGSIGN              ; (ldy/bne leave C from the cmp intact)
        bne :+
        bcs egpka1              ; white: ahead iff kr > r
        bcc egpkb
:       bcc egpka1              ; black: ahead iff kr < r
egpkb:  lda EGTMP               ; behind: penalty only on the pawn's own file
        bne egpkd
        lda #<-15
        jmp egadds
egpka1: lda EGTMP               ; ahead: bonus within one file
        cmp #2
        bcs egpkd
        lda #15
        jmp egadds
egpkd:  rts

; egadds: add the signed byte in A into SCORE, negated first when the
; passer belongs to BLACK (EGSIGN != 0) - the mirror's score -= egPasser.
egadds: ldy EGSIGN
        beq egadd
        eor #$FF
        clc
        adc #1                  ; -A
; egadd: SCORE += the signed byte in A, sign-extended. Clobbers A,X,(Y).
egadd:  ldx #0
        cmp #$80
        bcc :+
        dex                     ; negative: high byte $FF
:       clc
        adc SCORE
        sta SCORE
        txa
        adc SCORE+1
        sta SCORE+1
        rts

; egcheb: A = one 0x88 square, EGSQB = the other -> A = their Chebyshev
; distance max(|dr|,|df|), 0..7. Clobbers A, EGT, EGT2; preserves X,Y.
egcheb: sta EGT
        and #$07
        sta EGT2
        lda EGSQB
        and #$07
        sec
        sbc EGT2
        bcs :+
        eor #$FF
        adc #1                  ; |df| (C=0 here, so this adds exactly 1)
:       sta EGT2
        lda EGT
        and #$70
        sta EGT
        lda EGSQB
        and #$70
        sec
        sbc EGT                 ; rank difference, still in <<4 units
        bcs :+
        eor #$FF
        adc #1
:       lsr
        lsr
        lsr
        lsr                     ; |dr|
        cmp EGT2
        bcs :+
        lda EGT2                ; max(|dr|,|df|)
:       rts

