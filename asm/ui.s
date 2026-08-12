; ---------------------------------------------------------------------------
; 8fish on-device UI: 40-column text screen renderer (docs/ui-design.md).
;
; DRIVER CODE. Nothing in this file is reachable from search/eval/movegen/
; board; it only READS BOARD/PIECESQ/SIDE/SCORE/CURDEPTH/BEST* and writes the
; text screen. The engine plays byte-identical chess whether it is driven by
; the UCI bridge or by this file.
;
; This module is assembled to run in LANGUAGE-CARD RAM ($E000-$FFEF), not in
; the $4000-$BEFF engine image: the engine already runs with LC bank-1 RAM
; read+write enabled ($C08B, see engine.s), and it uses only 65 bytes of it
; (LCCODE at $D000), so $E000-$FFFF is 8 KB of directly-addressable RAM that
; costs ZERO of the image's 1622-byte MAIN headroom. See docs/ui-design.md.
;
; Video encoding (Apple IIe primary character set):
;   normal video  = ASCII | $80          ($80-$FF; lowercase at $E0-$FF)
;   inverse upper = ASCII & $3F          ($00-$3F)
;   inverse lower = ASCII                ($60-$7F, IIe only)
; The board's DARK squares are drawn in inverse video and its piece colour is
; carried by CASE (uppercase = white, lowercase = black — the FEN convention),
; so both the checker pattern and the piece colour survive under a piece.
; ---------------------------------------------------------------------------

; ---- Language Card bank selection ----
; READ these (never store): two consecutive reads of an odd $C08x enable
; writing to LC RAM, which everything here needs — $E000-$FFFF is where the
; UI's own variables live. Bank 1 is the RESTING STATE (LCCODE at $D000 and
; the DHTILES artwork at $D300 are bank 1); bank 2 holds the opening book's
; name table and, above it at $D700, the UI's own read-only strings and cold
; tables (the UIDATA2 segment). Every routine that selects bank 2 restores
; bank 1 before returning — internal/ui's bank gates fail if one stops.
; Defined here, not in asm/m8.s, because uiputs below needs them and ui.s is
; also linked standalone by asm/uitest.s.
LCBANK1RW = $C08B       ; r x2: LC bank 1, read RAM + write RAM
LCBANK2RW = $C083       ; r x2: LC bank 2, read RAM + write RAM

; ---- Screen geometry (40x24 text page 1) ----
BRDROW0   = 2           ; screen row holding rank 8
BRDCOL    = 3           ; screen column of the a-file cell's left character
PANCOL    = 21          ; move-list panel column
STATROW   = 12          ; side-to-move / status
CHKROW    = 13          ; check / mate / draw-claim status
THINKROW  = 14          ; depth / score / current best move (left column only:
                        ;  rows 3-15 of columns 21-39 are the move panel)
BOOKROW   = 16          ; opening name (full width, below the panel)
MSGROW    = 17          ; errors and prompts (full width)
HELPROW   = 20          ; command reminder (full width)
PROMPTROW = 23          ; input line

; ---- Borrowed zero page ----
; Every byte below is search/eval scratch that is provably dead at driver
; time (no search is running when the UI paints). The UI CLAIMS nothing.
SCRPTR    = T0          ; $CB-$CC  16-bit screen pointer
UISQ      = T2          ; $CD      current 0x88 square
UIDARK    = T3          ; $CE      $00 = light square, $10 = dark square
STRPTR    = MUL0        ; $CF-$D0  16-bit string pointer
UIROW     = EVTMP       ; $D7      loop scratch
UITMP     = PSQSQ       ; $D8      loop scratch
UIPLY     = PSQPIECE    ; $D9      loop scratch
UIMFIRST  = MULCNT      ; $DA      loop scratch
LAYPTR    = PSP0        ; $D2-$D3  16-bit layout-table pointer

; ---- UI RAM (Language Card, above the UI's own code) ----
;
; LC map ($E000-$FFFF; see docs/ui-design.md §2 — the engine runs with
; $C08B latched, so this is ordinary directly-executable RAM costing the
; engine ZERO MAIN bytes):
;
;   $E000-$F6FF  UICODE segment: code + the static data a bank window
;                cannot afford (5,888 B; the link config caps it here, so
;                an overflow is a link error). The read-only strings and
;                cold tables are NOT here: they are the UIDATA2 segment in
;                LANGUAGE CARD BANK 2 at $D700, read through uiputs's
;                bank-2 window and its siblings.
;   $F700-$F7FF  UI variables and screen-string buffers (this block)
;   $F800-$FAFF  game history: from / to / flags, one page each
;   $FB00-$FEFF  game hash history: HASH0-3, one page each
;   $FF00-$FF4F  UI80BUF: the mixed-mode window's 80-column staging line
;   $FF50-$FFEF  PPBOARD/PPPIECE: the ponder position snapshot (160 B, live
;                only while pondering — see below and asm/m8.s m8ponder)
;   $FFF0-$FFFF  6502 vectors (RAM once LC read is enabled; m8.s writes them)
M8VARS    = $F700
UIHCNT    = M8VARS+$00  ; plies played so far
UILEVEL   = M8VARS+$01  ; 1..9
UIHUMAN   = M8VARS+$02  ; which colour the human has (0 / COLORMASK)
UIRESULT  = M8VARS+$03  ; RES_* game-over code, 0 = still playing
UINLEGAL  = M8VARS+$04  ; legal moves for the side to move (capped at 255)
UICHK     = M8VARS+$05  ; nonzero: side to move is in check
UIBLEN    = M8VARS+$06  ; characters in the input line
UIMFROM   = M8VARS+$07  ; parsed / matched move
UIMTO     = M8VARS+$08
UIMPROM   = M8VARS+$09  ; promotion piece type (0 = none, else N/B/R/Q = 2..5)
UIFFLAGS  = M8VARS+$0A  ; the generator's flags for the matched move
UIPTRL    = M8VARS+$0B  ; move-list walk cursor (kept OUT of zero page:
UIPTRH    = M8VARS+$0C  ;  make/unmake clobber most of the engine's ZP)
UITMPB    = M8VARS+$0D
UICNT2    = M8VARS+$0E
UIFOUND   = M8VARS+$0F  ; nonzero: uifind matched
UISCR0    = M8VARS+$10  ; 16-bit scratch (score formatting, scan bounds)
UISCR1    = M8VARS+$11
UIQ0      = M8VARS+$12  ; 16-bit quotient scratch
UIQ1      = M8VARS+$13
UIK       = M8VARS+$14  ; soft-clock margin multiplier
UIACC0    = M8VARS+$15  ; 32-bit shift-and-add multiply accumulator
UIACC1    = M8VARS+$16
UIACC2    = M8VARS+$17
UIACC3    = M8VARS+$18
UIWIN     = M8VARS+$19  ; winning side for a decisive result (0 / COLORMASK)
UILSC0    = M8VARS+$1A  ; last completed engine search score (engine POV)
UILSC1    = M8VARS+$1B
UIT0      = M8VARS+$1C  ; 24-bit temp for the per-move limit arithmetic
UIT1      = M8VARS+$1D
UIT2      = M8VARS+$1E
UISEEN    = M8VARS+$1F  ; repetition count of the current position
UIHFULL   = M8VARS+$AA  ; nonzero: the 256-ply history arrays filled up and
                        ;  plies are no longer being RECORDED (the game goes
                        ;  on; see uiapply and cmd_take)
UIDHGR    = M8VARS+$AB  ; nonzero: the DOUBLE HI-RES board is the screen being
                        ;  shown (ESC toggles; the 40-column text screen is
                        ;  painted either way, so the swap is instantaneous)
; ---- Pondering state (docs/ponder-design.md §11.A). Driver-only, all in LC
; RAM; the engine never reads any of it. PONDERON is the UI-only enable flag
; (design §9.1); PONDERING selects the keyboard-poll ccsite target inside
; uidrive and suppresses the think-line paint; PONDERKEY says "a keypress
; ended the ponder search, DISCARD it" and gates uidrive's abort recovery off
; (design §4.3, the round-6 recovery trap). PP* hold the predicted reply P and
; the root+M snapshot's scalars (the board/piece-list snapshot lives in the
; $FF50 block below).
PONDERON  = M8VARS+$AC  ; nonzero: pondering enabled (m8main defaults it on)
PONDERING = M8VARS+$AD  ; nonzero while a ponder search runs (uidrive gates)
PONDERKEY = M8VARS+$AE  ; nonzero: a key ended the ponder search -> discard
PPFROM    = M8VARS+$AF  ; predicted reply P: from-square
PPTO      = M8VARS+$B0  ; predicted reply P: to-square
PPFLAGS   = M8VARS+$B1  ; predicted reply P: generator flags
PPSIDE    = M8VARS+$B2  ; root+M snapshot: SIDE
PPCASTLE  = M8VARS+$B3  ; root+M snapshot: CASTLE
PPEPSQ    = M8VARS+$B4  ; root+M snapshot: EPSQ
PPHALF    = M8VARS+$B5  ; root+M snapshot: HALFMOVE
; ---- Arrow-key cursor entry (asm/m8.s curpop and friends). $F7B9 is
; DHFLIP, defined as a raw address in asm/dhgr.s because that file also
; links standalone (asm/dhgrtest.s) without this one — named here so the
; map stays the single account of the page.
CURACT    = M8VARS+$B6  ; nonzero: the cursor is up on the board
CURSQ     = M8VARS+$B7  ; the cursor's 0x88 square
CURFROM   = M8VARS+$B8  ; latched FROM square (NOSQ = none)
CURTSQ    = M8VARS+$BA  ; cursqdraw scratch: the square being repainted
; ---- The BIG BOOK and its ProRWTS2 reader (asm/m8.s m8bigbook and friends;
; docs/prorwts2-design.md). BIGBOOKOK is the ONE-WAY latch: set only by a
; verified load, cleared at mesearch the instant "out of book" becomes true
; (the first real search overwrites the borrowed TT window), re-opened only
; by a New Game reload. RWTSHOLD is the zp-swap image rwtszp exchanges with
; the driver's $3C-$67 window around every call.
BIGBOOKOK = M8VARS+$BB  ; nonzero: the big book is verified in aux $4000 and
                        ;  the game has not yet left it
BBIDX     = M8VARS+$BC  ; big-book load: which of BOOK0-BOOK3
BBST      = M8VARS+$BD  ; big-book load: driver status, saved across rwtszp
                        ;  (M8VARS+$BE freed: the aux-direct read needs no held
                        ;   display byte -- the splash/board stays up untouched)
RWTSHOLD  = M8VARS+$BF  ; $F7BF-$F7EA: the zp window image (RWTS_ZPN = 44 B)
SPLST     = M8VARS+$EB  ; boot splash: driver status, saved across rwtszp
                        ;  (asm/m8.s m8splash); one byte, disk build only
PKDIV     = M8VARS+$ED  ; pkclk's charge divider: the ponder polls the keyboard
                        ;  every PKQUANT nodes for latency, but charges the
                        ;  soft clock only every 16th poll — 16*8 = the same
                        ;  128-node quantum ENG_checkclocks' PCOST prices
SLCMD     = M8VARS+$EE  ; save/load: which command the stub loaded the
                        ;  transient orchestrator FOR (0 = save, 1 = load);
                        ;  read by asm/saveload.s slentry
SLSTAT    = M8VARS+$EF  ; save/load: driver status, saved across rwtszp
                        ;  (the stub's orchestrator read and every call the
                        ;  orchestrator itself makes)
PONDEROK  = M8VARS+$EC  ; nonzero: uiread's per-key wait may run ponder BURSTS
                        ;  between keystrokes (asm/m8.s urdkey). Set by mloop
                        ;  only when the HUMAN is on move in a live game;
                        ;  cleared for the game-over command prompt and by the
                        ;  ESC screen-swap (leaving the input ends pondering
                        ;  for the turn). Distinct from PONDERON (the player-
                        ;  visible enable) and PONDERING (a search is live).
UIBUF     = M8VARS+$20  ; input line (UIBUFMAX bytes)
UITHINK   = M8VARS+$30  ; think line: depth / score / best move
UIMSGB    = M8VARS+$50  ; message row (40 B + terminator)
UIBOOKB   = M8VARS+$80  ; opening-name row (40 B + terminator)
                        ; UIHFULL is $F7AA, UIDHGR $F7AB; PP* run $F7AC-$F7B5;
                        ; CUR*/DHFLIP $F7B6-$F7BA; BIGBOOKOK/BB*/RWTSHOLD
                        ; $F7BB-$F7EA; SPLST $F7EB; PONDEROK $F7EC;
                        ; PKDIV $F7ED; SLCMD/SLSTAT $F7EE-$F7EF;
                        ; $F7F0-$F7FF free

; The game history is three PARALLEL 256-byte arrays so a ply index fits in
; X with no multiply: 256 plies = 128 full moves (Sargon III's own move list
; caps at 127). Takeback replays the game from the start position through
; these arrays, so no per-ply undo record is stored.
UIHFROM   = $F800       ; 256 bytes: from-square per ply
UIHTO     = $F900       ; 256 bytes: to-square per ply
UIHFLAG   = $FA00       ; 256 bytes: move flags per ply

; Game hash history: UIHASHn[i] is the position's 32-bit Zobrist hash
; BEFORE ply i was played, so the CURRENT position is always at index
; UIHCNT. The engine's own repetition scan works off HASHSTK, which at the
; root holds nothing — the game history has to live here (design §5.3).
;
; 256 entries is a HARD limit — the index is one byte, which is what makes
; every array above page-indexable — but it is NOT a game limit: at ply 255
; the UI stops RECORDING and keeps refereeing (uiapply). Nothing is ever
; written past index 255, and no game is ever drawn merely for being long.
UIHASH0   = $FB00
UIHASH1   = $FC00
UIHASH2   = $FD00
UIHASH3   = $FE00

; UI80BUF: one EIGHTY-column text line, staged in Language Card RAM before it
; is de-interleaved into the aux and main halves of the mixed-mode window
; (asm/m8.s's ui80row). It is not in the $F700 block because that page is
; full; $FF00-$FFEF was the last free run below the 6502 vectors.
;
; It is also, deliberately, NOT on the screen. The window's main half is rows
; 20-23 of the 40-column text page, so a row composed in place would be
; overwriting its own source. See the window's comment block in asm/m8.s.
UI80BUF   = $FF00       ; 80 bytes, $FF00-$FF4F

; PPBOARD/PPPIECE: the root+M position snapshot m8ponder takes before it makes
; the predicted reply P onto the board. The ponder search roots at ply 0, whose
; undo slots it overwrites, so P cannot be unmade afterward (engine.s iterate
; forces PLY=0); m8ponder restores root+M by copying this snapshot back and
; re-running ENG_evalinit. BOARD is 128 bytes ($40-$BF), PIECESQ 32, so the two
; fill the last free run below the 6502 vectors exactly ($FF50-$FFEF, 160 B).
; Only live during a ponder; nothing else touches this region.
PPBOARD   = $FF50       ; 128-byte snapshot of BOARD, $FF50-$FFCF
PPPIECE   = $FFD0       ; 32-byte snapshot of PIECESQ, $FFD0-$FFEF

UIBUFMAX  = 8           ; longest accepted input line ("e7e8q" + slack)

; Game-over codes (UIRESULT). There is deliberately NO "too long" code:
; a game is never over because of its length (docs/results.md 2026-07-28).
RES_MATE   = 1
RES_STALE  = 2
RES_50     = 3
RES_REP    = 4
RES_RESIGN = 5
RES_AGREED = 6
RES_ERR    = 7

; ---------------------------------------------------------------
; uigotorc: SCRPTR = the text-page-1 address of row A (0-23),
; column X (0-39). Clobbers A, Y.
;
; No carry propagation is needed: the largest row-base low byte is
; $D0 (row 23) and $D0 + 39 = $F7, so base+col never leaves the
; base's page. Callers MUST keep X <= 47.
;
; uigoto0 is uigotorc at column 0 — much the most common call, so
; the `ldx #0` lives here once instead of at fifteen call sites.
; The UI is COLD code (it runs between moves, at human speed), so
; a jsr that saves bytes is a win even where it costs cycles.
; ---------------------------------------------------------------
uigoto0:
        ldx #0
        ; fall through
uigotorc:
        tay
        txa
        clc
        adc ROWLO,y
        sta SCRPTR
        lda ROWHI,y
        sta SCRPTR+1
        rts

; ---------------------------------------------------------------
; uiputs: write the $00-terminated string at A/X (lo/hi) starting at
; SCRPTR. String bytes are stored RAW, so the data carries its own
; video attribute (see the encoding note above). Clobbers A, Y.
;
; It reads through a LANGUAGE CARD BANK 2 window, because that is
; where the static strings live (the UIDATA2 segment at $D700 — see
; asm/m8.cfg). The window is harmless for every other source uiputs
; is handed: the screen-string buffers are at $F7xx and bank
; switching only re-maps $D000-$DFFF. Its own code, SCRPTR's target
; (the main-RAM text page) and the stack are all unaffected too, and
; bank 1 — LCCODE, the artwork — is restored before returning, on
; the same discipline uibookname established.
; ---------------------------------------------------------------
uiputs:
        sta STRPTR
        stx STRPTR+1
        lda LCBANK2RW
        lda LCBANK2RW
        ldy #0
upsl:   lda (STRPTR),y
        beq upsx
        sta (SCRPTR),y
        iny
        bne upsl
upsx:   lda LCBANK1RW
        lda LCBANK1RW
        rts

; ---------------------------------------------------------------
; SCRSTR/INVSTR: assemble a $00-terminated screen string in normal
; (ASCII|$80) or inverse-uppercase (ASCII&$3F) video. INVSTR must not
; contain '@' (which would encode as the $00 terminator).
; ---------------------------------------------------------------
.macro SCRSTR str
        .repeat .strlen(str), I
        .byte .strat(str, I) | $80
        .endrepeat
        .byte 0
.endmacro

.macro INVSTR str
        .repeat .strlen(str), I
        .byte .strat(str, I) & $3F
        .endrepeat
        .byte 0
.endmacro

; ---------------------------------------------------------------
; uistatic: paint a layout table at A/X (lo/hi). Each entry is four
; bytes — row, column, string lo, string hi — and the table ends with
; row = $FF. Clobbers A, X, Y and the borrowed ZP.
;
; UITESTBUILD only: the shipping UI (asm/m8.s) paints every row with
; direct uigotorc/uiputs calls and never references this routine, so
; carrying it in the payload was 50 dead bytes of the $E000-$F6FF
; code budget. asm/uitest.s still drives it.
; ---------------------------------------------------------------
.ifdef UITESTBUILD
uistatic:
        sta LAYPTR
        stx LAYPTR+1
        ldy #0
        sty UITMP               ; byte cursor into the table
ustl:   ldy UITMP
        lda (LAYPTR),y
        cmp #$FF
        beq ustx
        pha                     ; row
        iny
        lda (LAYPTR),y
        tax                     ; column
        pla
        jsr uigotorc
        ldy UITMP
        iny
        iny
        lda (LAYPTR),y
        pha
        iny
        lda (LAYPTR),y
        tax
        pla
        jsr uiputs
        lda UITMP
        clc
        adc #4
        sta UITMP
        bne ustl
ustx:   rts
.endif


; ---------------------------------------------------------------
; uicls: fill the 40 visible columns of all 24 rows with normal
; spaces. Deliberately does NOT touch the eight per-page "screen
; holes" ($x478-$x47F etc.), which belong to peripheral firmware.
; Clobbers A, X, Y, SCRPTR.
; ---------------------------------------------------------------
uicls:  ldx #23
        stx UIROW
ucls1:  lda UIROW
        jsr uigoto0
        ldy #39
        lda #$A0                ; normal space
ucls2:  sta (SCRPTR),y
        dey
        bpl ucls2
        dec UIROW
        bpl ucls1
        rts

; ---------------------------------------------------------------
; uiboard: paint the 8x8 board from BOARD into the text page.
;
; Two characters per square (piece letter + blank, both carrying the
; square's video attribute), so the board is a 16x8 block of 14x8-pixel
; cells at rows BRDROW0..BRDROW0+7, columns BRDCOL..BRDCOL+15.
;
; Reads BOARD only. Clobbers A, X, Y and the borrowed ZP above.
; ---------------------------------------------------------------
uiboard:
        lda #$70                ; a8 (rank 7, file 0)
        sta UISQ
        lda #0
        sta UIDARK              ; a8 is a LIGHT square
        lda #0
        sta UITMP               ; board row 0..7 (rank 8 down to rank 1)
ubrow:  lda UITMP
        clc
        adc #BRDROW0
        ldx #BRDCOL
        jsr uigotorc
        ldy #0
ubcol:  ldx UISQ
        lda BOARD,x             ; piece byte (zp,x: BOARD=$40, sq <= $77)
        and #$0F                ; colour<<3 | type; 0 = empty
        ora UIDARK
        tax
        lda PIECECH,x
        sta (SCRPTR),y
        iny
        ldx UIDARK              ; blank half-cell, same video attribute
        lda PIECECH,x           ;  (PIECECH[$00]=space, PIECECH[$10]=inv space)
        sta (SCRPTR),y
        iny
        lda UIDARK
        eor #$10                ; next file: flip the square colour
        sta UIDARK
        inc UISQ
        cpy #16
        bcc ubcol
        lda UISQ                ; step down a rank: we advanced 8 files, so
        sec                     ;  the next rank's a-file is UISQ - $18
        sbc #$18
        sta UISQ
        lda UIDARK              ; 8 flips = no net change; flip once more so
        eor #$10                ;  the new rank starts on the other colour
        sta UIDARK
        inc UITMP
        lda UITMP
        cmp #8
        bcc ubrow
        rts

; ---------------------------------------------------------------
; uicoords: rank digits down the left of the board and file letters
; underneath it. Clobbers A, X, Y, SCRPTR, UIROW.
; ---------------------------------------------------------------
uicoords:
        lda #0
        sta UIROW
ucrk:   lda UIROW
        clc
        adc #BRDROW0
        ldx #BRDCOL-2
        jsr uigotorc
        lda #('8'|$80)
        sec
        sbc UIROW               ; '8' down to '1'
        ldy #0
        sta (SCRPTR),y
        inc UIROW
        lda UIROW
        cmp #8
        bcc ucrk
        lda #BRDROW0+8
        ldx #BRDCOL
        jsr uigotorc
        ldy #0
        lda #('a'|$80)
ucfl:   sta (SCRPTR),y
        iny
        iny
        clc
        adc #1
        cpy #16
        bcc ucfl
        rts

; ---------------------------------------------------------------
; uisqout: A = 0x88 square -> two characters ("e2") at (SCRPTR),y,
; advancing Y by 2. Clobbers A.
; ---------------------------------------------------------------
uisqout:
        pha
        and #$0F
        clc
        adc #('a'|$80)
        sta (SCRPTR),y
        iny
        pla
        lsr
        lsr
        lsr
        lsr
        clc
        adc #('1'|$80)
        sta (SCRPTR),y
        iny
        rts

; ---------------------------------------------------------------
; uidec2: A = 0..99 -> two right-aligned digits at (SCRPTR),y,
; advancing Y by 2. A leading zero is printed as a space.
; Clobbers A, X.
; ---------------------------------------------------------------
uidec2:
        ldx #0
ud2l:   cmp #10
        bcc ud2d
        sbc #10
        inx
        bne ud2l
ud2d:   pha
        txa
        beq ud2sp
        clc
        adc #('0'|$80)
        bne ud2put
ud2sp:  lda #$A0
ud2put: sta (SCRPTR),y
        iny
        pla
        clc
        adc #('0'|$80)
        sta (SCRPTR),y
        iny
        rts

; ---------------------------------------------------------------
; uid2z: A = 0..99 -> two digits at (SCRPTR),y with the leading zero
; PRINTED, advancing Y by 2. Clobbers A, X.
;
; It lives here rather than in m8.s because uidec3 (below) needs it
; and asm/ui.s is also linked STANDALONE by asm/uitest.s, so a
; renderer routine may not call into the m8.s half of the payload.
; m8.s's hundredths field (uiscore) calls it too.
; ---------------------------------------------------------------
uid2z:  ldx #0
ud2zl:  cmp #10
        bcc ud2zd
        sbc #10
        inx
        bne ud2zl
ud2zd:  pha
        txa
        clc
        adc #('0'|$80)
        sta (SCRPTR),y
        iny
        pla
        clc
        adc #('0'|$80)
        sta (SCRPTR),y
        iny
        rts

; ---------------------------------------------------------------
; uidec3: A = 0..255 -> THREE right-aligned digits at (SCRPTR),y,
; advancing Y by 3. Leading zeros are printed as spaces, but a
; nonzero hundreds digit forces the tens digit out, so 100 is "100"
; and not "1 0". Clobbers A, X.
;
; The move panel needs three columns because a game can legally run
; past move 99: with the ply cap gone (docs/results.md 2026-07-28)
; the only ceiling on the move number is the 256-entry history array,
; i.e. move 128. Two digits used to render 100 as ":0" — uidec2's
; tens digit is a raw counter, and 10 + '0' is ':'.
; ---------------------------------------------------------------
uidec3: ldx #0
ud3l:   cmp #100
        bcc ud3d
        sbc #100
        inx
        bne ud3l
ud3d:   pha
        txa
        beq ud3sp
        clc
        adc #('0'|$80)
        sta (SCRPTR),y
        iny
        pla
        jmp uid2z               ; hundreds present: print the tens zero
ud3sp:  lda #$A0                ; under 100: a blank hundreds column, and
        sta (SCRPTR),y          ;  uidec2 blanks a leading tens zero too
        iny
        pla
        jmp uidec2

; ---------------------------------------------------------------
; uimoves: render the game move list into the side panel.
;
; UIHIST holds three bytes per ply (from, to, flags) and UIHCNT the
; number of plies played. The panel shows the last PANROWS full moves
; as "nnn ffff tttt" — a THREE-column right-aligned move number, so a
; game past move 99 reads "100 e2e4 e7e5" rather than ":0 ...". The
; panel is 19 columns (21-39) and the line is 13, so it fits.
; Clobbers A, X, Y and the borrowed ZP.
; ---------------------------------------------------------------
PANROWS = 13

uimoves:
        lda #BRDROW0
        ldx #PANCOL
        jsr uigotorc
        lda #<pantxt
        ldx #>pantxt
        jsr uiputs
        ; first full move to show: max(0, ((UIHCNT+1)/2) - PANROWS)
        lda UIHCNT
        clc
        adc #1
        ror                     ; number of full moves touched. ROR, not
                                ;  LSR: UIHCNT can be 255 (the history is
                                ;  full and the game continues), and then
                                ;  the +1 carries out — an LSR would drop it
                                ;  and show move 1 instead of move 128
        sec
        sbc #PANROWS
        bcs :+
        lda #0
:       sta UIMFIRST
        lda #0
        sta UIROW               ; panel line 0..PANROWS-1
umrow:  lda UIMFIRST
        clc
        adc UIROW
        sta UITMP               ; full-move index (0-based)
        asl
        cmp UIHCNT
        bcs umdone              ; no ply for this line: stop
        lda UIROW
        clc
        adc #BRDROW0+1
        ldx #PANCOL
        jsr uigotorc
        ldy #0
        lda UITMP
        clc
        adc #1                  ; move NUMBER is 1-based
        jsr uidec3              ; umply supplies the separating space
        lda UITMP               ; white's ply index = 2*move
        asl
        jsr umply
        lda UITMP               ; black's ply index = 2*move + 1
        asl
        clc
        adc #1
        cmp UIHCNT
        bcs umnext              ; game ended on white's move
        jsr umply
umnext: inc UIROW
        lda UIROW
        cmp #PANROWS
        bcc umrow
umdone: rts

; umply: A = ply index -> " e2e4" at (SCRPTR),y. Clobbers A, X.
umply:  tax
        lda #$A0
        sta (SCRPTR),y
        iny
        lda UIHFROM,x
        jsr uisqout
        lda UIHTO,x
        jmp uisqout

; ---------------------------------------------------------------
; Tables
; ---------------------------------------------------------------

; Text page 1 row bases: $0400 + (row .mod 8)*$80 + (row/8)*$28.
ROWLO:
        .repeat 24, I
        .byte <($0400 + (I .mod 8) * $80 + (I / 8) * $28)
        .endrepeat
ROWHI:
        .repeat 24, I
        .byte >($0400 + (I .mod 8) * $80 + (I / 8) * $28)
        .endrepeat

; Screen byte for a board square, indexed by  dark<<4 | (piece & $0F).
; piece & $0F = colour<<3 | type, so $01-$06 are white P N B R Q K and
; $09-$0E are black. $00 is an empty square; $07/$08/$0F cannot occur.
PIECECH:
        ; --- light squares: normal video (ASCII | $80) ---
        .byte $A0               ; $00 empty          ' '
        .byte $D0, $CE, $C2     ; $01-$03 wP wN wB   P N B
        .byte $D2, $D1, $CB     ; $04-$06 wR wQ wK   R Q K
        .byte $BF, $BF          ; $07-$08 impossible '?'
        .byte $F0, $EE, $E2     ; $09-$0B bP bN bB   p n b
        .byte $F2, $F1, $EB     ; $0C-$0E bR bQ bK   r q k
        .byte $BF               ; $0F impossible     '?'
        ; --- dark squares: inverse video ---
        ; uppercase/punct inverse = ASCII & $3F; lowercase inverse = ASCII
        .byte $20               ; $10 empty          inverse ' '
        .byte $10, $0E, $02     ; $11-$13 wP wN wB
        .byte $12, $11, $0B     ; $14-$16 wR wQ wK
        .byte $3F, $3F          ; $17-$18 impossible
        .byte $70, $6E, $62     ; $19-$1B bP bN bB
        .byte $72, $71, $6B     ; $1C-$1E bR bQ bK
        .byte $3F               ; $1F impossible

; Panel header, inverse video: "MOVES" padded to the panel width.
pantxt: .byte $20, $0D, $0F, $16, $05, $13
        .byte $20, $20, $20, $20, $20, $20, $20, $20, $20, $20, $20, $20, $20
        .byte 0
