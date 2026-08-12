; ---------------------------------------------------------------------------
; 8fish SAVE/LOAD orchestrator (docs/prorwts2-design.md Feature 2,
; docs/saveload-feasibility.md §6).
;
; TRANSIENT CODE. This file is the SAVELOAD segment: linked alongside the UI
; (so it names UI routines and strings directly) but emitted to its OWN file
; (asm/m8saveload.bin), which ships as the SAVELOAD file in the disk's
; ProDOS-shaped region — NOT in the resident payload. The W/O command stubs
; (asm/m8.s cmd_save/cmd_load) pull it to $1A00 with the resident reader and
; jump in with A = 0 (save) / 1 (load); everything here is forgotten the
; moment it returns. The $E000-$F6FF UICODE budget was FULL (15 B free), so
; the logic lives where the write driver already lives: on the disk, loaded
; on demand into scratch that is dead between searches.
;
; THE TRANSIENT WINDOW ($0E00-$1FFF = MOVESTACK, "garbage until the first
; search", rebuilt by every search; PIECESQ at $0800 and the per-ply undo
; arrays at $0900-$0DFF are LIVE and untouched):
;
;   $0E00-$11FF  the write-capable ProRWTS2 blob (SAVE only), and afterwards
;                the read-back VERIFY buffer — the read-back lands over the
;                spent driver, so the verify costs no extra RAM
;   $1200-$13FF  the write driver's dirbuf     (baked in by cmd/genrwts)
;   $1400-$15FF  the write driver's encbuf     (its own page, NOT dirbuf)
;   $1600-$19FF  SLREC: the 1,024-byte save record (internal/saveload's
;                layout, byte-compatible by the round-trip gate)
;   $1A00-$1FFF  this code (SLSIZE caps it; the link fails past $1FFF)
;
; LOAD's replay generates ply-0 move lists from $0E00 up (a few hundred
; bytes), far below this code — and by then the record has been committed to
; the LC history arrays, so nothing down there is still needed.
;
; EVERY disk access runs in MAIN memory: auxreq stays 0 (the resident
; driver's aux path is never taken; the write build has no aux path at all),
; so the live TT / big book in aux and the DHGR screen are untouched. The
; resident driver's dirbuf ($0200) follows RAMRD/RAMWRT and therefore lands
; in MAIN $0200-$03FF here — enumerated as tolerable-to-clobber in the
; feasibility doc §2 (entropy pool, per-search scratch, the invalidated
; power-up bytes).
;
; FAILURE RULES. A failed SAVE touches nothing but the transient window and
; the (already sacrificial) SAVE file — the game in memory is never altered.
; A failed LOAD degrades by STAGE: an unreadable/invalid/empty record changes
; nothing; a record that validates but contains an illegal ply (hand-crafted:
; the checksum already passed) aborts to a clean NEW GAME rather than leave a
; half-replayed position on the board.
; ---------------------------------------------------------------------------

        .segment "SAVELOAD"

SLREC  = $1600                  ; the staged record (2 blocks)
SLVBUF = RWTSW_ENTRY            ; read-back verify buffer = the spent driver

; Record header offsets — keep byte-identical to internal/saveload.
SLR_MAGIC0 = SLREC+$000         ; '8'
SLR_MAGIC1 = SLREC+$001         ; 'F'
SLR_VER    = SLREC+$002         ; 1
SLR_NPLY   = SLREC+$003         ; plies recorded (0 = empty slot)
SLR_HUMAN  = SLREC+$004
SLR_LEVEL  = SLREC+$005
SLR_RESULT = SLREC+$006
SLR_WIN    = SLREC+$007
SLR_CK     = SLREC+$00E         ; 16-bit LE byte sum over all bytes but these
SLR_FROM   = SLREC+$100         ; 255 plies each, page-aligned for these loops
SLR_TO     = SLREC+$200
SLR_FLAG   = SLREC+$300

slentry:
        bne slldj               ; A = SLCMD: 0 save, 1 load
        beq slsave              ; (always)
slldj:  jmp slload

; ===========================================================================
; SAVE
; ===========================================================================
slsave:
        jsr slbuild             ; 1. the record, checksummed, at SLREC
        lda #<f_rwtsw           ; 2. the WRITE DRIVER: resident reader pulls
        ldx #>f_rwtsw           ;    the RWTSW file over dead scratch at $0E00
        ldy #>RWTSW_ENTRY
        jsr slrd4
        bne slwfail
        jsr slmat               ; 3. materialize it (slot pokes, track seed)
        lda #<f_save            ; 4. WRITE the record in place over the SAVE
        sta slfnam              ;    file's two blocks — the transient
        lda #>f_save            ;    driver's one and only run
        sta slfnam+1
        lda #>SLREC
        sta sldsth
        lda #1
        sta slwr
        jsr slio
        bne slwfail
        lda #<f_save            ; 5. VERIFY: resident read-back over the spent
        ldx #>f_save            ;    driver, then a 1,024-byte compare
        ldy #>SLVBUF
        jsr slrd4
        bne slwfail
        ldx #0
slv1:   lda SLVBUF,x
        cmp SLREC,x
        bne slwfail
        lda SLVBUF+$100,x
        cmp SLREC+$100,x
        bne slwfail
        lda SLVBUF+$200,x
        cmp SLREC+$200,x
        bne slwfail
        lda SLVBUF+$300,x
        cmp SLREC+$300,x
        bne slwfail
        inx
        bne slv1
        lda UIHFULL             ; saved — but a full history means only the
        bne slwtrunc            ;  first 255 plies were ever recorded: say so
        lda #<m_saved
        ldx #>m_saved
        jmp uisetmsg
slwtrunc:
        lda #<m_savefull
        ldx #>m_savefull
        jmp uisetmsg
slwfail:
        lda #<m_savefail        ; nothing in the game was touched: the record
        ldx #>m_savefail        ;  was STAGED, the write went to the one
        jmp uisetmsg            ;  sacrificial file, and the verify failed it

; slbuild: stage the record at SLREC. Zero it all first, so the checksum sum
; (over everything, with its own two bytes still zero) equals the Go side's
; skip-the-checksum-bytes definition.
slbuild:
        ldx #0
        txa
sbz:    sta SLREC,x
        sta SLREC+$100,x
        sta SLREC+$200,x
        sta SLREC+$300,x
        inx
        bne sbz
        lda #'8'
        sta SLR_MAGIC0
        lda #'F'
        sta SLR_MAGIC1
        lda #1
        sta SLR_VER
        lda UIHCNT
        sta SLR_NPLY
        lda UIHUMAN
        sta SLR_HUMAN
        lda UILEVEL
        sta SLR_LEVEL
        lda UIRESULT
        sta SLR_RESULT
        lda UIWIN
        sta SLR_WIN
        ldx #0
sbc1:   cpx UIHCNT              ; ONLY the recorded plies: past UIHCNT the
        bcs sbdone              ;  arrays hold garbage (at $F800 literally
        lda UIHFROM,x           ;  the spent boot-splash code), and a record
        sta SLR_FROM,x          ;  must be deterministic — the zeroing above
        lda UIHTO,x             ;  already blanked the tail. Keeps the asm
        sta SLR_TO,x            ;  builder byte-identical to saveload.Encode.
        lda UIHFLAG,x
        sta SLR_FLAG,x
        inx
        bne sbc1                ; always (x <= 255 = UIHCNT's ceiling)
sbdone:
        jsr slsum
        lda UISCR0
        sta SLR_CK
        lda UISCR1
        sta SLR_CK+1
        rts

; ===========================================================================
; LOAD
; ===========================================================================
slload:
        lda #<f_save            ; 1. read the SAVE file to SLREC
        ldx #>f_save
        ldy #>SLREC
        jsr slrd4
        beq slval
        lda #<m_slnoread        ; unreadable: not a valid record, game intact
        ldx #>m_slnoread
        jmp uisetmsg
slval:  lda SLR_MAGIC0          ; 2. validate: magic, version, checksum
        cmp #'8'
        bne slbadf
        lda SLR_MAGIC1
        cmp #'F'
        bne slbadf
        lda SLR_VER
        cmp #1
        bne slbadf
        lda SLR_CK              ; stash the stored sum, zero its bytes, and
        sta slck                ;  re-sum with the SAME loop the save used
        lda SLR_CK+1
        sta slck+1
        lda #0
        sta SLR_CK
        sta SLR_CK+1
        jsr slsum
        lda UISCR0
        cmp slck
        bne slbadf
        lda UISCR1
        cmp slck+1
        bne slbadf
        lda SLR_NPLY            ; 3. an empty slot is a message, not an error
        bne slcommit
        lda #<m_slempty
        ldx #>m_slempty
        jmp uisetmsg
slbadf: lda #<m_slbad           ; magic/version/checksum wrong: say so, touch
        ldx #>m_slbad           ;  nothing
        jmp uisetmsg

slcommit:
        ; 4. the point of no return: replace the game. History arrays and
        ; scalars first, then a clean new-game state, then REPLAY through the
        ; same find/legal/apply path a typed move takes — so the loaded
        ; position is by construction one the referee reached.
        sta sltgt
        lda SLR_HUMAN
        sta UIHUMAN
        lda SLR_LEVEL
        sta UILEVEL
        ldx #0
slc2:   lda SLR_FROM,x
        sta UIHFROM,x
        lda SLR_TO,x
        sta UIHTO,x
        lda SLR_FLAG,x
        sta UIHFLAG,x
        inx
        cpx #255
        bcc slc2
        jsr m8new               ; start position, UIHCNT=0, results/rows clear
slrl:   lda UIHCNT              ; the same replay loop shape as cmd_take
        cmp sltgt
        bcs slrdone
        tax
        lda UIHFROM,x
        sta UIMFROM
        lda UIHTO,x
        sta UIMTO
        lda UIHFLAG,x
        and #FL_PROMO           ; the promo piece rides in the flags' low
        sta UIMPROM             ;  bits, exactly what uifind matches against
        jsr uifind              ; re-derive UIFFLAGS from the GENERATOR: the
        lda UIFOUND             ;  stored flags select the move, the
        beq slrbad              ;  generator's own flags get played
        jsr uiplay
        jsr uitrylegal
        bcc slrbad
        jsr uiapply
        jmp slrl
slrdone:
        lda SLR_RESULT          ; 5. only DECISION results survive a load
        cmp #RES_RESIGN         ;  (uisync re-derives mate/stalemate/50/rep,
        beq slres               ;  and would otherwise clear a latched one)
        cmp #RES_AGREED
        bne slnores
slres:  sta UIRESULT
        lda SLR_WIN
        sta UIWIN
slnores:
        lda #<m_loaded
        ldx #>m_loaded
        jmp uisetmsg
slrbad:
        ; a ply the checksummed record swears by is not a legal move here:
        ; hand-crafted file or version drift. Never leave a half-replayed
        ; position — reset to a clean new game and say why.
        jsr m8new
        lda #<m_slbad
        ldx #>m_slbad
        jmp uisetmsg

; ===========================================================================
; The disk plumbing
; ===========================================================================

; slrd4: resident-reader convenience — read the 1,024-byte file named at A/X
; to page Y. Z on return = success (from slio).
slrd4:  sta slfnam
        stx slfnam+1
        sty sldsth
        lda #0
        sta slwr
        ; fall through

; slio: ONE driver call, resident (slwr=0) or transient-write (slwr=1),
; with the same zp-window discipline every driver call in asm/m8.s uses:
; rwtszp swaps the engine's $3C-$67 out and the driver image in, parameters
; are written into the LIVE window, and rwtszp swaps back after. All calls
; are 1,024 bytes ($0400) to/from page sldsth, name at slfnam. Z = success.
slio:   jsr rwtszp
        lda slfnam
        sta RWTS_NAMLO
        lda slfnam+1
        sta RWTS_NAMLO+1
        lda #0
        sta RWTS_AUXREQ         ; MAIN, always: the held image may say aux
                                ;  (harmlessly stores to unused $51 when the
                                ;  call is the write driver's)
        sta RWTS_SIZELO
        sta RWTS_LDRLO
        lda sldsth
        sta RWTS_LDRLO+1
        lda #4                  ; $0400 = 1,024 B = 2 blocks, every call here
        sta RWTS_SIZELO+1
        lda slwr
        beq siord
        lda #RWTSW_CMDWRITE     ; the write driver wants its command byte set
        sta RWTSW_REQCMD        ;  on every call
        jsr RWTSW_ENTRY
        jmp siodn
siord:  jsr RWTS_ENTRY
siodn:  lda RWTS_STATUS
        sta SLSTAT
        jsr rwtszp
        lda SLSTAT              ; Z = success, for the caller's branch
        rts

; slmat: materialize the just-loaded write driver, finishing what ProRWTS2's
; init would have done (the same two jobs m8rwtsinit did for the resident
; reader at boot, plus the write path's four raw-immediate slot sites):
; poke the boot slot into the operand sites listed in the blob's own tables,
; and seed its current-track operand from the RESIDENT driver's — which is
; live-accurate, because the resident reader's RWTSW read just moved the arm.
slmat:  ldx #RWTSW_NSLOT-1
smp1:   lda RWTSW_SLOTLO,x
        sta sm1+1
        sta sm2+1
        lda RWTSW_SLOTHI,x
        sta sm1+2
        sta sm2+2
        lda RWTSSLOT
sm1:    ora $FFFF               ; $C08x soft-switch operands: OR the slot in
sm2:    sta $FFFF
        dex
        bpl smp1
        ldx #RWTSW_NIMM-1
smp2:   lda RWTSW_IMMLO,x
        sta sm3+1
        lda RWTSW_IMMHI,x
        sta sm3+2
        lda RWTSSLOT
sm3:    sta $FFFF               ; raw slot<<4 immediates: STORE the slot
        dex
        bpl smp2
        lda RWTS_TRACKD1        ; bank 1 is the resting bank: readable here
        sta RWTSW_TRACKD1
        rts

; slsum: UISCR1:UISCR0 = the 16-bit byte sum over all 1,024 record bytes.
; The checksum convention skips the checksum's own two bytes; callers make
; that true by summing while they are ZERO (slbuild zeroes the record before
; filling it; slval stashes and zeroes them first). Same primitive as the
; big book's trailer (asm/m8.s mbbsum) and internal/saveload.Checksum16.
slsum:  lda #0
        sta UISCR0
        sta UISCR1
        sta T0
        lda #>SLREC
        sta T0+1
        ldy #0
ssl:    lda (T0),y
        clc
        adc UISCR0
        sta UISCR0
        bcc :+
        inc UISCR1
:       iny
        bne ssl
        inc T0+1
        lda T0+1
        cmp #>(SLREC+$400)
        bne ssl
        rts

; File names, length-prefixed plain ASCII, read by the drivers through
; (namlo). Main RAM below $C000 is readable from every bank state, so —
; unlike f_book/f_splash — these need no bank-independence care.
f_rwtsw: .byte 5, "RWTSW"
f_save:  .byte 4, "SAVE"

; Parameters for slio, and the load's scratch. In the segment itself — the
; file is re-read from disk on every W/O, so they start fresh each time.
slfnam: .word 0                 ; length-prefixed name pointer
sldsth: .byte 0                 ; destination page
slwr:   .byte 0                 ; 0 = resident read, 1 = transient write
sltgt:  .byte 0                 ; load: plies to replay
slck:   .word 0                 ; load: the record's stored checksum

; The stub read the whole SLSIZE-byte file to SLCODE; this code must fit it.
        .assert * <= SLCODE + SLSIZE, error, "asm/saveload.s overflows the $1A00-$1FFF transient window (SLSIZE)"
