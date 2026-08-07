; Double-hi-res board renderer for 8fish.
;
; Paints the owner's hand-drawn chessboard (assets/chess2-dazzledraw-save.bin,
; sliced into tiles by cmd/gentiles) onto DHGR page 1 at FULL 560x192
; resolution. See docs/ui-design.md 3.1.
;
; GEOMETRY (all measured from the artwork; cmd/gentiles asserts every number)
;
;   The artwork's squares are 42 x 19 at origin (8,2), which is EXACTLY the
;   rendered tile -- the board is drawn at the size it is displayed, so
;   nothing the artist draws is thrown away. (It was not always so: the first
;   artwork was 44 x 21 and the slicer dropped the top two source rows,
;   silently clipping 28 pixels of bishop and king finial. The board was
;   redrawn at 42 x 19 on 2026-08-06; see assets/README.md's History.)
;
;     42 px = 6 DHGR bytes = 3 aux + 3 main, and 42 is a multiple of 14.
;
;   That last fact is what makes this renderer cheap. A 14-px multiple keeps
;   every file at the SAME bit phase, so a tile is horizontally position
;   independent: no pre-shifting, no 8 bit-phase sprite variants, no masking.
;   42 is also EVEN, so the light squares' 1-on-1-off dither keeps its phase
;   (the artwork's dither is globally locked to "even absolute x is lit",
;   which in DHGR bytes is the constant pair aux=$55, main=$2A).
;
;   Columns 0-7 and 35-41 of every square are pure background in ALL 64
;   squares (measured: zero deviating pixels), which is what lets a tile row
;   store four of its six byte columns.
;
;   Board: 8 x 42 = 336 px at x=112 (byte column 8: 14-aligned AND even, so
;   the dither phase carries over unchanged), and 8 x 19 = 152 scanlines from
;   y=4. Mixed mode ($C053) shows scanlines 0-159, so 152 plus a 4-line border
;   top and bottom fits exactly -- and as of 2026-08-01 that is not a spare
;   property, it is the shipping arrangement: the board runs 4-155 with four
;   rows of 80-column text on scanlines 160-191. Nothing in this file changed
;   to make that happen (see docs/ui-design.md section 14); the geometry was
;   already right, and what had to move was the opening book.
;
; TILES
;
;   Byte columns 0 (aux0) and 5 (main2) of every tile are pure background in
;   all 24 piece tiles, so only the middle FOUR are stored:
;   [aux1, aux2, main0, main1], 4 B x 19 rows = 76 B per tile, 24 tiles =
;   1,824 B. The two empty-square tiles are synthesised at init from the
;   background constants (dhinit), so the blob carries 24 tiles, not 26.
;
; The caller must have selected LC BANK 1 -- the bank holding the tile blob;
; uibookname is the one routine that ever selects bank 2, and it restores
; bank 1 before it returns -- and must have put the machine in DHGR mode
; (uidhon: 80COL on, AN3 low, HIRES, TEXT off, MIXED on, 80STORE off).

; ---- geometry ----
DHCOL0    = 8           ; first DHGR byte column of the board (x = 112)
DHTOP     = 4           ; first scanline of the board
DHROWS    = 19          ; scanlines per square
DHBOARDH  = 8*DHROWS    ; 152 board scanlines
DHTILESZ  = 4*DHROWS    ; 76 stored bytes per tile
DHNTILE   = 24          ; piece tiles in the blob

; ---- background constants ----
; "even absolute x is lit": in an aux byte x = 14c+bit, so even x = even bit
; => $55; in a main byte x = 14c+7+bit, so even x = odd bit => $2A.
DHBGL_A   = $55
DHBGL_M   = $2A
DHBGD_A   = $00
DHBGD_M   = $00

; ---- soft switches ----
DHRAMWRTON  = $C005
DHRAMWRTOFF = $C004

; ---- borrowed zero page (the same provably-dead engine scratch asm/ui.s
; borrows; the text renderer and this one never run at the same time) ----
DHPTR     = T0          ; $CB-$CC screen row pointer
DHSQ      = T2          ; $CD     current 0x88 square
DHDARK    = T3          ; $CE     $10 if the square is dark, else $00
DHTPTR    = MUL0        ; $CF-$D0 tile pointer
DHTMP     = MUL2        ; $D1     scratch
DHCOL     = EVTMP       ; screen byte column of the current file
DHROWI    = PSQSQ       ; board scanline index of the current rank
DHBGA     = PSQPIECE    ; this square's aux background byte
DHBGM     = MULCNT      ; this square's main background byte
DHA1      = PSP0        ; the four stored tile bytes, held across the
DHA2      = PSP0+1      ;  RAMWRT flip
DHM0      = PSP1
DHM1      = PSP1+1

; --------------------------------------------------------------------------
; dhclear: blank DHGR page 1 in BOTH banks -- 16,384 bytes, once, before the
; screen is ever shown.
;
; It is not decoration. dhboard paints only the 8x8 board (byte columns 8-31,
; scanlines 4-155); the border around it is never written again. And on the
; shipping disk what the loader left in main $2000-$3FFF is the DEAD OPENING
; BOOK, which as pixels is 8 KB of noise around the artwork.
;
; $00 is not a choice of colour, either: internal/tiles' Go model renders onto
; a zeroed screen, so black is the value TestDHGRRenderParity asserts for every
; byte outside the board.
;
; Cost: 16,384 stores, ~115,000 cycles, paid once at init.
; --------------------------------------------------------------------------
dhclear:
        sta DHRAMWRTON          ; aux half first...
        jsr dhcl1
        sta DHRAMWRTOFF         ; ...then fall through for the main half, whose
                                ;  rts returns to dhclear's caller
dhcl1:  lda #0
        sta DHPTR
        lda #$20
        sta DHPTR+1
        ldx #$20                ; $2000-$3FFF is 32 pages
        ldy #0
        tya                     ; A = 0
dhcl2:  sta (DHPTR),y
        iny
        bne dhcl2
        inc DHPTR+1
        dex
        bne dhcl2
        rts

; --------------------------------------------------------------------------
; dhinit: build the init-time tables — 152 board scanline base addresses, 26
; tile pointers, and the two synthesised blank tiles. Call once, after the
; tile blob is resident. Clobbers A,X,Y.
;
; DHGR scanline base (per bank, page 1):
;   off(y) = (y&7)*1024 + ((y>>3)&7)*128 + (y>>6)*40
; splits into a carry-free lo/hi pair:
;   hi = $20 + (y&7)*4 + ((y>>3)&7)>>1
;   lo = (((y>>3)&7)&1)*128 + (y>>6)*40     ; max 128+80 = 208, never carries
; --------------------------------------------------------------------------
dhinit:
        ldx #0
dhi1:   txa
        clc
        adc #DHTOP              ; y = DHTOP + row index
        pha
        and #7                  ; s = y&7
        asl
        asl                     ; s*4
        sta DHTMP
        pla
        pha
        lsr
        lsr
        lsr
        and #7                  ; p = (y>>3)&7
        pha
        lsr                     ; p>>1
        clc
        adc DHTMP
        adc #$20
        sta DHROWH,x            ; hi = $20 + s*4 + p>>1
        pla
        and #1
        beq dhi2
        lda #128
dhi2:   sta DHTMP               ; (p&1)*128
        pla                     ; y
        lsr
        lsr
        lsr
        lsr
        lsr
        lsr                     ; q = y>>6 (0..2)
        tay
        lda dhq40,y
        clc
        adc DHTMP
        sta DHROWL,x            ; lo = (p&1)*128 + q*40
        inx
        cpx #DHBOARDH
        bne dhi1

        ; --- the two blank tiles (an empty square's four stored bytes are
        ; background too, so it needs a tile like any other; synthesising
        ; them here is why the blob carries 24 tiles and not 26) ---
        ldx #0
dhi5:   lda #DHBGL_A            ; light: aux1, aux2, main0, main1
        sta dhblnk,x
        sta dhblnk+1,x
        lda #DHBGL_M
        sta dhblnk+2,x
        sta dhblnk+3,x
        lda #0                  ; dark: all four bytes are background 0
        sta dhblnk+DHTILESZ,x
        sta dhblnk+DHTILESZ+1,x
        sta dhblnk+DHTILESZ+2,x
        sta dhblnk+DHTILESZ+3,x
        inx
        inx
        inx
        inx
        cpx #DHTILESZ
        bne dhi5
        rts

dhq40:  .byte 0, 40, 80

; --------------------------------------------------------------------------
; dhboard: repaint all 64 squares from BOARD — 64 x 19 x 6 = 7,296 stores.
;
; A whole-board repaint every move (rather than incremental updates) is the
; discipline the 40-column renderer already uses, and for the same reason: at
; a fraction of a percent of one move's thinking time it is far too cheap to
; be worth a bug class.
; --------------------------------------------------------------------------
dhboard:
        lda #$70                ; 0x88 square of a8 — rank 8 is the top row
        sta DHSQ
        lda #0
        sta DHROWI
dhrank:
        lda #DHCOL0
        sta DHCOL
dhfile:
        ; --- square colour: dark iff (rank88 + file) is even ---
        lda DHSQ
        lsr
        lsr
        lsr
        lsr                     ; rank88
        eor DHSQ                ; bit0 = parity(rank88) ^ parity(file)
        and #1
        beq dhdk
        lda #$00                ; odd sum => LIGHT
        sta DHDARK
        lda #DHBGL_A
        sta DHBGA
        lda #DHBGL_M
        sta DHBGM
        bne dhpc                ; always taken: DHBGL_M ($2A) is nonzero
dhdk:   lda #$10                ; even sum => DARK
        sta DHDARK
        lda #DHBGD_A
        sta DHBGA
        lda #DHBGD_M
        sta DHBGM
dhpc:
        ; --- tile for this square: the PIECECH index, dark<<4 | piece&$0F.
        ; TILEOFFL/H (cmd/gentiles) give the blob offset directly, so there is
        ; no multiply by the 76-byte stride here. ---
        ldy DHSQ
        lda BOARD,y
        and #$0F
        ora DHDARK
        tay
        lda TILEIDX,y
        cmp #TILE_NONE
        beq dhmt
        clc
        lda TILEOFFL,y
        adc #<DHTILES
        sta DHTPTR
        lda TILEOFFH,y
        adc #>DHTILES
        sta DHTPTR+1
        jmp dhblit
dhmt:   lda DHDARK              ; empty square: the synthesised blank tile
        bne dhmtd
        lda #<dhblnk
        sta DHTPTR
        lda #>dhblnk
        sta DHTPTR+1
        jmp dhblit
dhmtd:  lda #<(dhblnk+DHTILESZ)
        sta DHTPTR
        lda #>(dhblnk+DHTILESZ)
        sta DHTPTR+1
dhblit:

        ; --- blit DHROWS scanlines ---
        ldx DHROWI
        lda DHROWI
        clc
        adc #DHROWS
        sta DHTMP               ; stop when the row index reaches this
dhrow:
        lda DHROWL,x
        sta DHPTR
        lda DHROWH,x
        sta DHPTR+1
        ; Read all four tile bytes BEFORE touching RAMWRT. A read follows
        ; RAMRD, not RAMWRT, so this is not strictly required — but keeping
        ; the switched window as small and as store-only as possible is what
        ; makes this routine easy to reason about (and see docs/decisions.md
        ; D8: a `sta (zp),y` dummy read follows RAMRD, which is exactly the
        ; kind of surprise this discipline avoids).
        ldy #0
        lda (DHTPTR),y
        sta DHA1
        iny
        lda (DHTPTR),y
        sta DHA2
        iny
        lda (DHTPTR),y
        sta DHM0
        iny
        lda (DHTPTR),y
        sta DHM1

        sta DHRAMWRTON          ; --- aux half: aux0(bg), aux1, aux2 ---
        ldy DHCOL
        lda DHBGA
        sta (DHPTR),y
        iny
        lda DHA1
        sta (DHPTR),y
        iny
        lda DHA2
        sta (DHPTR),y

        sta DHRAMWRTOFF         ; --- main half: main0, main1, main2(bg) ---
        ldy DHCOL
        lda DHM0
        sta (DHPTR),y
        iny
        lda DHM1
        sta (DHPTR),y
        iny
        lda DHBGM
        sta (DHPTR),y

        clc                     ; next stored tile row
        lda DHTPTR
        adc #4
        sta DHTPTR
        bcc dhr2
        inc DHTPTR+1
dhr2:   inx
        cpx DHTMP
        bne dhrow

        ; --- next file ---
        clc
        lda DHCOL
        adc #3
        sta DHCOL
        inc DHSQ
        lda DHSQ
        and #$07
        beq dhnrank             ; 8 files done
        jmp dhfile              ; (out of branch range)
dhnrank:

        ; --- next rank (0x88: down one rank is -$10; we are +8 into this
        ; one, so step back $18) ---
        clc
        lda DHROWI
        adc #DHROWS
        sta DHROWI
        cmp #DHBOARDH
        bcs dhdone
        lda DHSQ
        sec
        sbc #$18
        sta DHSQ
        jmp dhrank
dhdone:
        rts
