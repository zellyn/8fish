; Transposition table: 4096 entries x 8 bytes in aux RAM $0200-$81FF.
;
; Aux access discipline (D4): reads of $0200-$BFFF with RAMRD on switch
; instruction fetches too, so the read loop (ttfetch) lives in Language
; Card RAM, installed by the loader in engine.s. Writes need only RAMWRT,
; which doesn't affect fetches, so ttstore runs from ordinary code.
; Arguments/results stay in zero page, which neither switch affects.
;
; Entry: +0..2 verify = HASH1..3 (20 fresh bits beyond the 12 index
; bits), +3 from, +4 to (NOSQ if none), +5/6 score (node-relative for
; mates), +7 depth<<2 | bound; bound 0 marks an empty entry.

; ---------------------------------------------------------------
; ttaddr (as a macro, inlined into ttprobe and ttstore - deep opt r4):
; TTPTR = TTBASE + (12-bit index from HASH0/HASH1) * 8.
; Table-driven: TTHITAB fills bits 3-6, SHR5TAB bits 0-2 (disjoint,
; so ora); the final +>TTBASE must stay adc (base $02 overlaps).
; Clobbers A,X,Y.
; ---------------------------------------------------------------
.macro TTADDR
        ldx HASH0
        lda SHL3TAB,x
        sta TTPTR
        ldy HASH1
        lda TTHITAB,y
        ora SHR5TAB,x
        clc
        adc #>TTBASE
        sta TTPTR+1
.endmacro

; ---------------------------------------------------------------
; ttprobe: carry set = hit; TTENTRY+3..7 holds the entry, mate scores
; adjusted to be relative to this node (score -= PLY sense). The
; verify compare happens inside ttfetch, still in aux-read mode, so
; a miss bails without copying anything (TTENTRY+0..2 are never
; written on the probe path; ttstore rebuilds them from HASH1..3).
; ---------------------------------------------------------------
ttprobe:
        TTADDR
        jsr ttfetch             ; LC-resident: verify + copy, cc = miss
        bcs :+
        rts                     ; carry clear: miss
:       ; mate-score adjustment: stored scores are node-relative
        lda TTENTRY+6
        cmp #$74                ; >= +29696: winning mate
        bcc ttpneg
        lda TTENTRY+5
        sec
        sbc PLY
        sta TTENTRY+5
        bcs tthit
        dec TTENTRY+6
        sec
        rts
ttpneg: cmp #$8C                ; <= $8B..: losing mate
        bcs tthit
        cmp #$80
        bcc tthit               ; positive non-mate
        lda TTENTRY+5
        clc
        adc PLY
        sta TTENTRY+5
        bcc tthit
        inc TTENTRY+6
tthit:  sec
        rts

; ---------------------------------------------------------------
; ttstore: store TTENTRY+3..+6 (move, score) with bound in A for the
; current position; depth = MAXDEPTH - PLY clamped to 0..31. Mate
; scores converted to node-relative. Skipped entirely when aborting
; (garbage scores must not poison the table).
; ---------------------------------------------------------------
ttstore:
        ldx ABORT
        beq :+
        rts
:       sta T0                  ; bound
        ; depth
        lda MAXDEPTH
        sec
        sbc PLY
        bpl :+
        lda #0
:       cmp #32
        bcc :+
        lda #31
:       asl
        asl
        ora T0
        sta TTENTRY+7
        ; mate adjustment (inverse of probe)
        lda TTENTRY+6
        cmp #$74
        bcc tspneg
        lda TTENTRY+5
        clc
        adc PLY
        sta TTENTRY+5
        bcc tsgo
        inc TTENTRY+6
        bne tsgo                ; always
tspneg: cmp #$8C
        bcs tsgo
        cmp #$80
        bcc tsgo
        lda TTENTRY+5
        sec
        sbc PLY
        sta TTENTRY+5
        bcs tsgo
        dec TTENTRY+6
tsgo:   lda HASH1
        sta TTENTRY
        lda HASH2
        sta TTENTRY+1
        lda HASH3
        sta TTENTRY+2
        TTADDR
        sta $C005               ; RAMWRT on: stores land in aux
        ; unrolled: entries are 8-aligned, (TTPTR),y never crosses
        ldy #7
        lda TTENTRY+7
        sta (TTPTR),y
        dey
        lda TTENTRY+6
        sta (TTPTR),y
        dey
        lda TTENTRY+5
        sta (TTPTR),y
        dey
        lda TTENTRY+4
        sta (TTPTR),y
        dey
        lda TTENTRY+3
        sta (TTPTR),y
        dey
        lda TTENTRY+2
        sta (TTPTR),y
        dey
        lda TTENTRY+1
        sta (TTPTR),y
        dey
        lda TTENTRY
        sta (TTPTR),y
        sta $C004               ; RAMWRT off
        rts

        .segment "LCCODE"

; ttfetch: verify-and-read the entry at aux (TTPTR). Runs from LC RAM
; because RAMRD switches all $0200-$BFFF reads including fetches; zero
; page (HASH1..3, TTENTRY) is unaffected, so the verify compare runs
; against main ZP while aux is still switched in, and a miss bails
; before copying anything. Carry set = verified hit, TTENTRY+3..7
; filled; carry clear = empty or key mismatch.
; (Entries are 8-aligned: (TTPTR),y never crosses a page.)
ttfetch:
        sta $C003               ; RAMRD on
        ldy #7
        lda (TTPTR),y
        sta TTENTRY+7
        and #$03
        beq ttfmiss             ; bound 0: empty entry
        ldy #0
        lda (TTPTR),y
        cmp HASH1
        bne ttfmiss
        iny
        lda (TTPTR),y
        cmp HASH2
        bne ttfmiss
        iny
        lda (TTPTR),y
        cmp HASH3
        bne ttfmiss
        iny
        lda (TTPTR),y
        sta TTENTRY+3
        iny
        lda (TTPTR),y
        sta TTENTRY+4
        iny
        lda (TTPTR),y
        sta TTENTRY+5
        iny
        lda (TTPTR),y
        sta TTENTRY+6
        sta $C002               ; RAMRD off
        sec
        rts
ttfmiss:
        sta $C002               ; RAMRD off
        clc
        rts

        .segment "CODE"
