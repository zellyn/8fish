; ---------------------------------------------------------------------------
; 8fish on-device user interface (docs/ui-design.md).
;
; DRIVER CODE, top to bottom. Nothing here is reachable from search / eval /
; movegen / board: the UI calls INTO the engine at its published entry points
; (asm/engsyms.inc, generated from asm/engine.lbl by cmd/genengsyms) and the
; engine never calls back. engine.bin is bit-identical whether or not this
; file exists, so the engine plays the same chess driven by a human as it
; does driven by the UCI bridge.
;
; WHERE IT LIVES. Language Card RAM, $E000-$FFEF. The engine already latches
; $C08B at entry and never switches back, so that is ordinary, directly
; executable, directly addressable RAM in the normal address space — and this
; project had never used a byte of it. The UI therefore costs the engine
; ZERO MAIN bytes, zero transposition-table entries, and does not move the
; opening book.
;
; TWO OUTPUT FILES (see m8.cfg):
;   m8boot.bin  $0800  the run-once copier: latch $C08B, copy the payload
;                      from $0900 to $E000, install the engine's LC-resident
;                      aux primitives at $D000, jump to $E000. Both $0800
;                      (PIECESQ) and $0900-$1FFF (the per-ply arrays and
;                      MOVESTACK) are engine RAM that is garbage until the
;                      first search, so this costs nothing permanent either.
;   m8.bin      $E000  the UI itself.
;
; Assemble with -D HARNESSKBD to read the harness input traps instead of the
; real $C000/$C010 — that is how internal/ui drives a whole game from Go.
; ---------------------------------------------------------------------------

        .include "defs.inc"
        .include "book.inc"
        .include "tiledefs.inc"
        .include "engsyms.inc"

; ---------------------------------------------------------------------------
; LANGUAGE CARD BANK 1, above the engine's 65-byte LCCODE at $D000. Until now
; the whole of $D041-$DFFF was unused; the DHGR board renderer's read-only
; artwork and its two init-built tables live there, which is why the renderer
; costs the UI payload only its 347 bytes of code and 96 bytes of index table.
;
; Bank 1, not bank 2, ON PURPOSE for the ARTWORK: the engine's aux primitives
; are in bank 1 at $D000, the renderer would otherwise have to switch banks
; around every repaint, and a bank switch that gets left in the wrong state
; hides LCCODE from the next transposition-table probe.
;
;   $D000-$D063   LCCODE      the engine's aux primitives (ttfetch, and now
;                             bkfetch/bkhdr for the book: 100 B, from the
;                             LINKER -- internal/delivery's
;                             TestLanguageCardBank1Layout reads the real
;                             __LCCODE_SIZE__ rather than trusting this line,
;                             which said 65 B for one commit after it stopped
;                             being true)
;   $D300-$DA1F   DHTILES     the 24 tiles, 1,824 B, delivered by stage 2
;   $DA20-$DAB7   DHROWL      152 scanline base addresses (low), built by dhinit
;   $DAB8-$DB4F   DHROWH      152 scanline base addresses (high)
;   $DB50-$DBE7   dhblnk      the two synthesised empty-square tiles (2 x 76)
;   $DBE8-$DFFF   free        1,048 B
;
; LANGUAGE CARD BANK 2 is no longer free, and what is in it is what made MIXED
; MODE fit. The opening book's 1,702-byte NAME TABLE moved here from the aux
; blob, so the book's entries could start at aux $0800 -- above the 80-column
; text page, whose aux half ($0400-$07FF) is the EVEN columns of the four-row
; text window under the board.
;
;   $D000-$D6A5   BOOK_NAMES  the opening book's name table (asm/book.inc)
;   $D6A6-$DFFF   free        2,394 B
;
; The bank-1 objection above still stands, and is exactly why only ONE routine
; may select bank 2: uibookname, which runs at $E000 (unbanked), reads the
; table, and restores bank 1 before it returns. internal/ui's
; TestBookNameRestoresBank1 fails if it stops doing either half.
; ---------------------------------------------------------------------------
; Exported so the LINKER writes them into asm/m8.lbl: internal/delivery's
; TestLanguageCardBank1Layout reads the real addresses out of the label file
; rather than recomputing them, which is the difference between a gate and a
; second copy of the arithmetic.
        .export DHTILES, DHROWL, DHROWH, dhblnk

DHTILES   = $D300

; BOOKSTAGE is where the opening book LANDS -- from stage 2 of the chain load,
; or from a BLOAD -- before m8bookaux lifts it to its resident home in aux at
; BOOK_BASE. It is main hi-res page 1, i.e. the DHGR MAIN half, which is
; exactly the 8 KB the book had to stop occupying.
BOOKSTAGE = $2000
DHROWL    = DHTILES + TILE_BLOB_SIZE            ; $DA20
DHROWH    = DHROWL + 152
dhblnk    = DHROWH + 152

; Where the payload is staged before the copier runs. It is a LINKER symbol,
; not a constant here, because it is a fact about the DELIVERY LAYOUT and the
; layout lives in the .cfg alongside the copier's own load address:
;
;   m8.cfg / m8t.cfg   copier $0800, payload $0900   (BLOAD/BRUN delivery)
;   m8sd.cfg           copier $0C00, payload $0D00   (Standard Delivery disk;
;                                                     see cmd/mkdsk)
;
; For the BLOAD layout, $0900-$1FFF is 5,888 bytes of engine RAM (the per-ply
; undo/search arrays, then MOVESTACK) that holds nothing but garbage until the
; first search — and 5,888 is exactly the code budget m8.cfg caps the UI at,
; so the payload can never grow into the resident opening book at $2000. The
; disk layout has less staging room than code budget, which is why
; internal/ui's TestDiskLedger asserts the payload still ends below $2000.
        .import UIPAYLOAD

; Levels 1-4 are fixed depth (BUDGET = 0, the period-honest "search N plies"
; control that needs no clock at all); 5-9 are timed and run on
; FT2_SOFTCLK's estimated clock. The split lives in the LVB* table, not in
; code: a zero budget IS fixed-depth mode to the engine.
NLEVELS = 9

; ---------------------------------------------------------------------------
; The copier, and (on the disk build) the CHAIN LOADER. This is the only code
; that ever runs from MAIN.
;
; It does four things, in an order forced by what is alive when:
;
;   1. lift the UI payload from its staging address to $E000, and install the
;      engine's aux-bank primitives at $D000. Both must happen FIRST, because
;      the payload's staging area is where stage 2 lands.
;   2. (disk only) re-enter the surviving Standard Delivery loader with a fresh
;      page table to read STAGE 2: the DHGR tile blob, the opening book's name
;      table, and the opening book's entries.
;   3. copy the tile blob into Language Card bank 1 at DHTILES, and the book's
;      name table into Language Card BANK 2 at BOOK_NAMES.
;   4. copy the opening book's entries from main $2000 to AUX $0800, vacating
;      main $2000-$3FFF — which is the DHGR main half, and therefore not
;      somewhere the book can stay.
;
; Step 4 is UNCONDITIONAL: the BLOAD/harness path has the book at main $2000
; too (the loader or the test poked it there), and the engine's probe reads
; the aux copy in both. Step 2 is `-D SDCHAIN`, i.e. the disk link only,
; because it depends on boot-loader state that a BRUN from BASIC does not have.
;
; ★ CHAIN-LOAD PRECONDITIONS. On entry from the boot loader's terminator the
; following are live, and steps 1's code is written not to disturb any of them:
;
;   Y      the loader's NEXT sector index — the value its `$0801: TAY` produced
;   $2B    slot<<4 (X is a copy; we reload from $2B rather than preserve it)
;   $26    $00, the read buffer's low byte
;   $41    the track the head is on
;   $0800  $01, the boot ROM's "one sector per call" count
;
; ZPTR ($E1) and TTPTR ($F0) are the only zero page this code writes, and both
; are far above $41, so the copy in step 1 cannot corrupt the loader's state.
; Nothing here moves the drive head, and the motor is still on.
; ---------------------------------------------------------------------------
        .segment "BOOT"

        .import __UICODE_SIZE__

boot:
.ifdef SDCHAIN
        sty sdsect              ; the loader's next sector. FIRST: everything
                                ;  else is free to clobber Y.
.endif
        ldx #$FF
        txs
        lda $C08B               ; LC bank 1, read RAM + write RAM — the same
        lda $C08B               ;  double read engine.s's entry uses
        lda #<UIPAYLOAD
        sta ZPTR
        lda #>UIPAYLOAD
        sta ZPTR+1
        lda #<$E000
        sta TTPTR
        lda #>$E000
        sta TTPTR+1
        ldx #>(__UICODE_SIZE__ + 255)
        jsr copyp
        ; Install the engine's LC-resident aux-bank primitives at $D000. The
        ; engine's own $4000 entry does this, and the UI never uses that
        ; entry — it drives `iterate` directly — so the UI must do it here or
        ; the first transposition-table probe runs into garbage.
        ldy #ENG_LCCODE_SIZE-1
btlc:   lda ENG_LCCODE_LOAD,y
        sta ENG_LCCODE_RUN,y
        dey
        bpl btlc

.ifdef SDCHAIN
        ; ---- STAGE 2: re-enter the loader with a fresh page table ----------
        ; ROM must be readable again: the loader's track step ends in
        ; `jmp $FCA8` (the monitor's WAIT). We do not need to WRITE the
        ; Language Card until stage 2 has landed, so read-ROM/write-protect
        ; is the state to be in — and it is one read, not two.
        lda $C08A
        ldx #SD2TABLEN          ; write the table at $084F upward
sdt:    lda sd2tab-1,x
        sta $084E,x
        dex
        bne sdt
        lda #$4E                ; re-arm the pointer the loader self-modifies:
        sta $0806               ;  its INC makes the first read $084F
        lda #<sd2done           ; and repoint the terminator's JMP at $084C
        sta $084D
        lda #>sd2done
        sta $084E
        ldx $2B                 ; slot<<4, exactly as the boot ROM left it
sdsecl: ldy #$00                ; patched at entry: the next sector
        jmp $0802               ; ...and the load resumes, mid-track.
sdsect  = sdsecl+1

sd2done:
        ; ---- the tile blob: main SD2TILES -> Language Card bank 1 ----------
        lda $C089               ; read ROM, WRITE LC RAM, bank 1 (double read)
        lda $C089
        lda #<SD2TILES
        sta ZPTR
        lda #>SD2TILES
        sta ZPTR+1
        lda #<DHTILES
        sta TTPTR
        lda #>DHTILES
        sta TTPTR+1
        ldx #SD2TILEN
        jsr copyp

        ; ---- the book's NAME TABLE: main SD2NAMES -> Language Card BANK 2 --
        ; Bank 2 because bank 1 is full: LCCODE at $D000, the artwork at
        ; $D300, the scanline tables and the synthesised blank tiles above it,
        ; leaving 1,048 contiguous bytes for a 1,702-byte table. Bank 2 is
        ; 4,096 bytes and had never been touched.
        ;
        ; $C081 twice: read ROM, WRITE LC RAM, BANK 2. The double read is what
        ; enables writing, exactly as with $C089 above; the ROM stays readable
        ; because nothing here needs LC RAM to read.
        lda $C081
        lda $C081
        lda #<SD2NAMES
        sta ZPTR
        lda #>SD2NAMES
        sta ZPTR+1
        lda #<BOOK_NAMES
        sta TTPTR
        lda #>BOOK_NAMES
        sta TTPTR+1
        ldx #SD2NAMEN
        jsr copyp

        lda $C08B               ; back to LC RAM read + write, BANK 1 -- and
        lda $C08B               ;  bank 1 is the state EVERYTHING else assumes:
                                ;  LCCODE ($D000, the TT and book primitives)
                                ;  and DHTILES ($D300, the artwork) are both
                                ;  hidden while bank 2 is in. Only uibookname
                                ;  ever switches away again, and it switches
                                ;  back before it returns.
        lda #1                  ; the artwork is resident: boot to the BOARD
        sta UIDHGRDEF           ;  (a RAM patch of the payload, not a build
                                ;   difference -- see m8main)
.endif

        jmp $E000               ; the payload's first three bytes are a jump
                                ;  to m8main, so the copier is byte-identical
                                ;  for every build variant of the UI

; THE BOOK'S MOVE TO AUX IS NOT DONE HERE, and the reason is a real machine.
; It used to be: `sta RAMWRTON` and copy 29 pages from main $2000 to "aux"
; $0200. On an Apple ][+ $C005 is not a soft switch at all, so those 29 pages
; land in MAIN $0200-$1F3F -- straight over the BLOAD copier running at $0800.
; The copier overwrote itself mid-loop and the machine check that exists to
; print NEEDS A 128K APPLE IIE never ran; internal/ui's
; TestRefusesMachinesWithoutAuxRAM caught it as a screen full of garbage.
;
; So the copy is m8bookaux, called from m8main AFTER m8machine has PROVED the
; aux switches are real. Nothing may write 7 KB through RAMWRT before that
; proof, because on the machines the proof exists to reject, RAMWRT-on writes
; are ordinary main-memory writes over whatever is there.

; copyp: copy X pages from (ZPTR) to (TTPTR). Clobbers A,X,Y and both
; pointers; touches no other zero page (see the chain-load preconditions).
copyp:  ldy #0
cpl:    lda (ZPTR),y
        sta (TTPTR),y
        iny
        bne cpl
        inc ZPTR+1
        inc TTPTR+1
        dex
        bne copyp
        rts

; Where stage 2 lands in MAIN, and how many pages of it there are. Both blobs
; are staged in main and copied on rather than read straight to their homes,
; and that is the boot ROM's doing, not caution: its denibblise pass READS THE
; BUFFER BACK (`lda ($26),y` at $C6DC). A Language Card destination reads back
; as ROM while ROM is banked in for $FCA8, and an aux destination reads back as
; main with RAMRD off; either way the second pass would write garbage over the
; first. See internal/delivery's package doc.
SD2TILES  = UIPAYLOAD           ; the UI payload's staging area, now dead. Not
                                ;  a second copy of the address: it is the
                                ;  linker symbol the copier already reads the
                                ;  payload from, so the two cannot desync.
SD2TILEN  = (TILE_BLOB_SIZE + 255) / 256
SD2NAMES  = SD2TILES + SD2TILEN * 256   ; the name table abuts the artwork, so
                                        ;  the two are ONE span on the disk.
                                        ;  DERIVED, not written down twice:
                                        ;  internal/delivery.NamesOrg is the
                                        ;  same address computed the same way,
                                        ;  and TestStage2PageTable fails if the
                                        ;  two ever disagree.
SD2NAMEN  = (BOOK_NAMES_SIZE + 255) / 256
SD2BOOK   = BOOKSTAGE           ; main hi-res page 1: the DHGR main half
SD2BOOKN  = (BOOK_BLOB_SIZE + 255) / 256

; The name table's whole-page copy must stay inside Language Card bank 2. It
; lands at BOOK_NAMES ($D000) and $E000-$FFFF is NOT banked -- it is the UI's
; own code -- so an overrun would write the copier's payload over itself.
.assert BOOK_NAMES + SD2NAMEN * 256 <= $E000, error, "the book's name table does not fit Language Card bank 2 ($D000-$DFFF); see cmd/genbook's budget check"

.ifdef SDCHAIN
; Stage 2's page table, generated from the three blob sizes so that it cannot
; drift from what internal/delivery writes on the disk (TestStage2PageTable
; reads these very bytes back out of m8sdboot.bin and compares).
sd2tab:
        .repeat SD2TILEN, i
        .byte >SD2TILES + i
        .endrepeat
        .repeat SD2NAMEN, i
        .byte >SD2NAMES + i
        .endrepeat
        .repeat SD2BOOKN, i
        .byte >SD2BOOK + i
        .endrepeat
        .byte $C0
SD2TABLEN = * - sd2tab

; The store loop above runs X from SD2TABLEN down to 1 writing `sta $084E,x`,
; so the highest byte it touches is $084E+SD2TABLEN. Past $08FF it would walk
; out of the boot sector's page into $0900+ (the engine's per-ply arrays), and
; the loader would then read its table off the end of page $08 and back into
; its own code at $0800. delivery.Stage.Check() enforces the 176-sector
; ceiling on the GO side; nothing stopped ca65 from emitting a longer table
; until this line. It is the same ceiling seen from the assembler.
.assert SD2TABLEN <= 177, error, "stage 2's page table would overrun $08FF: SD2TABLEN > 177 sectors (the blobs grew; see internal/delivery)"
.endif

; ---------------------------------------------------------------------------
        .segment "UICODE"

        ; $E000 exactly: the payload's entry vector. Keeping it at a fixed
        ; address (rather than letting the copier jump to a linked symbol)
        ; means m8boot.bin does not change when the UI does.
m8entry: jmp m8main

        ; ui.s next: it defines the SCRSTR/INVSTR macros used below, and
        ; ca65 macros must be defined before they are invoked.
        .include "ui.s"
        .include "entropy.inc"

; ===========================================================================
; Entry and the main loop
; ===========================================================================

; IIe display soft switches. These are WRITE-only on a IIe (all of
; $C000-$C00F READS as the keyboard), so each is reached with a store.
; Deliberately declared here and not in defs.inc: that file is engine
; source, and the UI does not touch engine source.
CLR80STORE  = $C000     ; w: 80STORE off
KBDSTROBE   = $C010     ; r: clear the keyboard strobe
RAMRDOFF    = $C002     ; w: read MAIN $0200-$BFFF
RAMRDON     = $C003     ; w: read AUX
RAMWRTOFF   = $C004     ; w: write MAIN $0200-$BFFF
RAMWRTON    = $C005     ; w: write AUX
SET80COLOFF = $C00C     ; w: 40-column display
SETALTCHAR  = $C00F     ; w: ALTERNATE character set
TXTSET      = $C051     ; text, not graphics
MIXCLR      = $C052     ; full screen, no four-line text window
TXTPAGE1    = $C054     ; display page 1 (and, under 80STORE, map it to MAIN)
SET80COLON  = $C00D     ; w: 80-column video fetch (BOTH banks per scanline)
GRSET       = $C050     ; graphics, not text
MIXSET      = $C053     ; MIXED: graphics on scanlines 0-159, four text rows
                        ;  (20-23) below them
SETDHIRES   = $C05E     ; AN3 LOW: double resolution, with 80COL on
CLRDHIRES   = $C05F     ; AN3 HIGH: single resolution
HIRESSET    = $C057     ; hi-res, not lo-res

; Language Card bank selection. READ these (never store): two consecutive
; reads of an odd $C08x is what enables writing to LC RAM, and every use here
; needs LC RAM writable -- $E000-$FFFF is where the UI's own variables live,
; and write-protecting the card protects all of $D000-$FFFF, not just the
; banked $D000-$DFFF window.
;
; Bank 1 is the RESTING STATE and everything assumes it: LCCODE ($D000: the
; engine's ttfetch and the book's bkfetch/bkhdr) and DHTILES ($D300: the board
; artwork) are both bank 1. Bank 2 holds exactly one thing -- the opening
; book's name table -- and exactly one routine ever selects it (uibookname).
LCBANK1RW   = $C08B     ; r x2: LC bank 1, read RAM + write RAM
LCBANK2RW   = $C083     ; r x2: LC bank 2, read RAM + write RAM

; Autostart's power-up byte (docs/ui-design.md §11 risk 3). The reset
; handler takes the WARM path — jmp ($03F2) — only when $03F4 equals
; $03F3 EOR $A5; anything else is a cold boot.
PWRUPLO     = $03F3
PWRUPBYTE   = $03F4

; m8main: cold start. Runs at $E000 with LC RAM read+write enabled and the
; engine image already resident at $4000.
m8main:
        ldx #$FF
        txs
        cld                     ; D is undefined after a 6502 reset. The
                                ;  Autostart ROM clears it in practice, so
                                ;  this is hardening, not a fix — but every
                                ;  ADC/SBC in the UI's arithmetic (and in the
                                ;  engine) is binary, and one byte is cheap
                                ;  next to debugging decimal-mode scores.
        ; TAKE THE DISPLAY. Six stores, and the first three are not optional.
        ;
        ; 80STORE is the one that corrupts memory rather than pixels. With
        ; 80STORE ON, $0400-$07FF (and, if HIRES is also on, $2000-$3FFF)
        ; follows PAGE2 and IGNORES RAMRD/RAMWRT — so the engine's aux
        ; transposition table, which spans $0200-$81FF in AUX, would have
        ; its $0400-$07FF slots land on the MAIN text page instead: garbage
        ; characters spraying across the board on every search, and a TT
        ; reading the screen back as entries. A IIe powers up with 80STORE
        ; off, so the disk boot is safe, but the 80-column FIRMWARE turns it
        ; ON — which is exactly the state a BLOAD/BRUN from BASIC.SYSTEM in
        ; 80-column mode (the default on a IIc, and on any IIe booted with
        ; PR#3) inherits. This USED to be a hardware-only failure, because
        ; goapple2's iie model did not implement 80STORE; it does now, and
        ; TestDiskBoots boots from the hostile state (80STORE + PAGE2 on)
        ; and fails if this store goes missing.
        ;
        ; ALTCHARSET is the load-bearing one. A IIe powers up on the PRIMARY
        ; character set, in which $60-$7F is FLASHING PUNCTUATION — so every
        ; black piece standing on a dark square (PIECECH's inverse lowercase)
        ; would come out as a blinking digit or bracket. $60-$7F is inverse
        ; lowercase only once the ALTERNATE set is selected, and only the
        ; program can select it. The byte encoding in docs/ui-design.md §3.2
        ; is correct; it just silently assumed a switch nobody threw.
        ;
        ; 80COL-off matters because ProDOS on a IIe boots into 80 columns:
        ; with the 80-column hardware live, this 40-column screen is shown
        ; one column out of two. TEXT / NOMIX / PAGE1 are three bytes each
        ; and remove the remaining assumptions about the state a BRUN
        ; inherits. PAGE1 is what makes the 80STORE store above complete:
        ; 80STORE off puts $0400-$07FF back under RAMRD/RAMWRT, PAGE1 makes
        ; the DISPLAY read main page 1.
        sta CLR80STORE
        sta SET80COLOFF
        sta SETALTCHAR
        sta TXTSET
        sta MIXCLR
        sta TXTPAGE1
        bit KBDSTROBE           ; a key held down while the disk loads is
                                ;  latched before the UI ever polls, and
                                ;  would arrive as the first character typed
        jsr m8machine           ; refuse to run where the engine cannot
        jsr m8bookaux           ; ...and only then move the book into aux
        ; CTRL-RESET. On a IIe (unlike a ][+ with a Language Card) a hardware
        ; reset DISABLES the built-in language card — that is exactly why
        ; Apple Pascal could use Ctrl-Reset as a warm start — so $FFFC is
        ; fetched from ROM and any copy written here is dead. The Autostart
        ; handler then makes its own warm/cold decision on the power-up byte:
        ; it takes the WARM path, jmp ($03F2) into Applesoft, iff
        ; $03F4 == $03F3 EOR $A5. Nothing 8fish or the boot ROM puts in page 3
        ; reaches $03F3/$03F4 -- the engine's scratch stops at $030C, the book
        ; probe's pads run $03D6-$03E6, and the Disk II ROM's own buffers stop
        ; at $03D5 (all gated by internal/delivery's TestPage3Layout) -- so
        ; that test would PASS on a booted disk and hand the machine to a BASIC
        ; whose entire zero page the engine has trampled: neither 8fish nor a
        ; reboot.
        ;
        ; So invalidate it. Ctrl-Reset then COLD boots, which restarts 8fish
        ; off the disk still in the drive. On the BLOAD path it is strictly
        ; better still: ProDOS's own language-card image is long gone by
        ; then, so a warm start there is a guaranteed crash.
        lda #0
        sta PWRUPLO
        lda #$FF                ; != $00 EOR $A5, so: cold start
        sta PWRUPBYTE
        ; The other 6502 vectors ARE ours: $FFFA-$FFFF is RAM once LC read is
        ; enabled, so with LC RAM in, a BRK or a stray interrupt would vector
        ; through whatever garbage powered up there. NMI and IRQ/BRK are the
        ; two that can actually fire here; RESET, per the note above, is
        ; deliberately NOT written — it is unreachable, and writing it
        ; invited the belief that Ctrl-Reset came back to m8main.
        lda #<m8main
        sta $FFFA               ; NMI
        lda #>m8main
        sta $FFFB
        lda #<m8irq
        sta $FFFE               ; IRQ / BRK
        lda #>m8irq
        sta $FFFF
        ; ON-DEVICE FEATURE CONFIGURATION (docs/results.md 2026-07-27).
        ; FT2_SOFTCLK is what makes budget mode run at all on a machine with
        ; no readable clock; the harness deliberately leaves it OFF because
        ; it has a real counter, so this must NOT be copied from
        ; ucibridge.runEngine.
        ; FEATURES must be the SHIPPED GAMEPLAY MASK, $1F|FT_CKEXT ($5F) --
        ; the mask ucibridge.runEngine plays, and the one every Elo number in
        ; docs/results.md was measured at, including both Sargon gauntlets
        ; (+89 and +110). It shipped as plain $1F until 2026-07-28, under a
        ; comment reading "all search + eval features" that was true only
        ; until FT_CKEXT was adopted on 07-25 at +24 +/- 23 over 600 games.
        ; So the bootable disk -- the artifact a user is handed -- played
        ; about 24 Elo below the artifact the results log describes.
        ;
        ; TWO independent adversarial reviewers found this on the same day,
        ; and the reason every gate missed it is worth keeping: internal/ui's
        ; reference searches go through chesstest.NewMachine, whose TEST
        ; default is also $1F, so TestEngineParity compared $1F against $1F.
        ; The reference had been built to match the UI instead of to match
        ; what ships -- the same shape as the harness clock the hardware does
        ; not have, and the position pool that was not game conditions.
        ;
        ; Gated now from both directions: internal/ui TestShippedFeatureMask
        ; and TestShippedFeatureConfig both read these bytes out of the BOOTED
        ; image and compare against the bridge's mask.
        lda #$1F|FT_CKEXT
        sta FEATURES
        lda #FT2_GENDEFER|FT2_SOFTCLK
        sta FEATURES2
        lda #4
        sta UILEVEL
        lda #0
        sta UIHUMAN             ; the human plays White by default
        ; The board screen. dhclear/dhinit are run ONCE -- the border never
        ; changes and the scanline tables never change.
        ;
        ; WHICH SCREEN COMES UP is NOT a constant here, and that is the point.
        ; The renderer blits from DHTILES in Language Card bank 1, and only the
        ; chain loader can put the artwork there: a BRUN of m8boot.bin has no
        ; boot loader to re-enter, so on that path $D300 holds whatever the
        ; last program left behind. Defaulting to the board would paint 1,824
        ; bytes of garbage as a chessboard until the user pressed ESC.
        ;
        ; So the default lives in UIDHGRDEF, a byte of the payload that is $00
        ; in both links -- m8.bin and m8sd.bin stay byte-identical, which
        ; TestDiskLayout requires -- and the chain loader stores $01 into it in
        ; RAM after lifting the payload. The disk boots to the board; a BRUN
        ; boots to the text screen and ESC is a no-op it can still take.
        jsr dhclear
        jsr dhinit
        lda UIDHGRDEF
        sta UIDHGR
        beq :+
        jsr uidhon
:
        jsr m8new

; The main loop. Every position change goes through uisync, and every
; iteration repaints the WHOLE screen: a full 40x24 repaint is 23,659 cycles
; (measured, internal/ui TestRenderCost) = 0.1% of a 30-second move, so
; partial repaints — and the entire class of incremental-update bugs — are
; designed out of existence.
mloop:  jsr uisync
        jsr uipaint
        lda UIRESULT
        bne mcmd                ; game over: commands only
        jsr uimyturn
        bcc mengine
mcmd:   jsr uiread
        jsr uidispatch
        jmp mloop
mengine:
        jsr m8engine
        jmp mloop

m8irq:  rti

; ---------------------------------------------------------------------------
; m8bookaux: lift the resident opening book's ENTRIES from main $2000 to AUX
; $0800, vacating main $2000-$3FFF -- the DHGR MAIN half -- for the board
; renderer. (The book's NAME TABLE is not here: it went to Language Card
; bank 2 in the copier, and it is the only part of the book that can, because
; the probe never reads it.)
;
; The blob got to main $2000 from the disk (stage 2 of the chain load) or from
; a BLOAD; either way it is there and dead the moment this returns. Stores
; follow RAMWRT and reads follow RAMRD, so ONE RAMWRT-on window copies main to
; aux with no switching inside the loop. This code runs from the Language Card
; at $E000, which neither switch touches.
;
; CALLED ONLY AFTER m8machine. See the note in the copier: on a machine whose
; aux switches are not switches, this loop is a 6 KB write over main $0800 --
; i.e. over this copier's own BLOAD-path staging area.
;
; ★ WHY $0800 AND NOT $0200. Aux $0400-$07FF is the 80-column text page's
; AUXILIARY half: with 80COL on, the scanner fetches an aux byte and a main
; byte per column, and the aux bytes are the EVEN columns. Mixed mode's
; four-line window is rows 20-23 of that page, so the book cannot be under it.
; $0800 is the first page above it.
;
; Byte count: BOOK_BLOB_SIZE rounded up to whole pages -- 23 pages for 5,705 B,
; so it copies 183 bytes past the blob into aux $1E49-$1EFF. Still well below
; the DHGR aux half at $2000, which internal/delivery's TestMainMemoryLayout
; asserts against the real blob size rather than against this sentence, and
; TestBookClearsTheAuxTextPage asserts the bottom end the same way.
; ---------------------------------------------------------------------------
m8bookaux:
        lda #<BOOKSTAGE
        sta ZPTR
        lda #>BOOKSTAGE
        sta ZPTR+1
        lda #<BOOK_BASE
        sta TTPTR
        lda #>BOOK_BASE
        sta TTPTR+1
        ldx #(BOOK_BLOB_SIZE + 255) / 256
        sta RAMWRTON
        ldy #0
m8bal:  lda (ZPTR),y
        sta (TTPTR),y
        iny
        bne m8bal
        inc ZPTR+1
        inc TTPTR+1
        dex
        bne m8bal
        sta RAMWRTOFF
        rts

; ---------------------------------------------------------------------------
; m8machine: this program needs a 128K Apple IIe. Prove it, or refuse.
;
; The transposition table lives in AUXILIARY RAM at $0200-$81FF, and the two
; machines that cannot provide it fail in ways a player would never diagnose:
;
;   Apple ][+ (or a IIe with the aux switches absent). $C002-$C005 are not
;   soft switches there, so every "aux" access goes to MAIN — straight over
;   the resident book at $2000 and the engine image at $4000. THE ENGINE
;   OVERWRITES ITSELF MID-SEARCH.
;
;   64K IIe (no auxiliary card). The switches exist but there is no RAM
;   behind them: writes vanish, reads float. At ~10^7 probes a game, a
;   24-bit verify lets roughly ONE false hit through per game — a silent
;   blunder, not a crash.
;
; The probe is two-sided, and that is the whole trick: write $A5 to MAIN
; $0300 and $5A to AUX $0300, then require BOTH that aux reads back $5A and
; that MAIN still reads $A5. A ][+ passes the first half (the switch was a
; no-op, so the $5A landed in main) and fails the second; a 64K IIe fails
; the first, because a floating read is not $5A.
;
; WHERE IT PROBES, and why it moved. It used to use $0300 (CEILMAX0, engine
; scratch) on the reasoning that aux $0300 was "inside the transposition
; table" and would be rewritten anyway. Both halves of that stopped being
; true: the TT starts at aux $4000 now, and aux $0300 was, at the time, INSIDE
; THE RESIDENT OPENING BOOK (based at aux $0200 then), which the copier had
; already put there when m8main ran. The probe wrote $5A and then $00 over one
; of its bytes; internal/ui's TestDiskBoots caught it as a book that did not
; read back.
;
; The book has since moved up to aux $0800, so aux $0300 would be harmless
; again -- and that is exactly the trap this comment exists to refuse. An
; address that is scratch because of where something ELSE happens to live is a
; bug waiting for the next move. $3F00 is scratch in BOTH banks BY
; CONSTRUCTION: main $3F00 and aux $3F00 are both inside DHGR page 1, which
; dhclear paints end to end before anything is displayed, and $3F00 is above
; the book's stage-2 landing zone in main ($2000-$3648) so probing it cannot
; damage the blob before m8bookaux moves it.
DHPROBE     = $3F00
; ---------------------------------------------------------------------------
m8machine:
        sta RAMRDOFF            ; start from MAIN whatever the firmware that
        sta RAMWRTOFF           ;  ran before us left the switches at
        lda #$A5
        sta DHPROBE             ; MAIN
        sta RAMRDON
        sta RAMWRTON
        lda #$5A
        sta DHPROBE             ; AUX
        lda DHPROBE             ; read AUX back before switching away
        sta RAMRDOFF            ; (the switches ignore the value stored)
        sta RAMWRTOFF
        cmp #$5A
        bne m8nope              ; no aux RAM behind the switches
        lda DHPROBE
        cmp #$A5
        beq m8ok                ; main survived: the switches are real
m8nope: jsr uicls
        lda #10
        ldx #6
        jsr uigotorc
        lda #<m_needsiie
        ldx #>m_needsiie
        jsr uiputs
m8halt: jmp m8halt              ; nothing safe is left to do
m8ok:   rts

; uimyturn: C=1 if the human is to move — either the side to move is his, or
; the UI is in two-player mode (UIHUMAN = $FF), where it referees a game
; between two people and never searches.
uimyturn:
        lda UIHUMAN
        cmp #$FF
        beq umtyes
        cmp SIDE
        bne umtno
umtyes: sec
        rts
umtno:  clc
        rts

; uiwhoidx: A = 0 (human is White), 1 (Black) or 2 (two players).
uiwhoidx:
        lda UIHUMAN
        cmp #$FF
        beq uwi2
        lsr
        lsr
        lsr                     ; 0 -> 0, COLORMASK -> 1
        rts
uwi2:   lda #2
        rts

; ===========================================================================
; Position bookkeeping
; ===========================================================================

; uistartpos: install the standard starting position. Board, piece lists,
; side, castling rights, ep square and the 50-move clock; then evalinit
; rebuilds the accumulators, PHASE, the pawn-file bitmasks and HASH0-3.
; Does NOT touch the game history (takeback replays through it).
uistartpos:
        lda #0
        ldx #127
uspc:   sta BOARD,x             ; zp,x: BOARD = $40, so $40..$BF
        dex
        bpl uspc
        ldx #31
uspp:   lda STSQ,x
        sta PIECESQ,x
        tay
        lda STPC,x
        sta a:BOARD,y
        dex
        bpl uspp
        lda #0
        sta SIDE
        sta HALFMOVE
        sta PLY
        sta HVALID
        sta ABORT
        lda #CR_WK|CR_WQ|CR_BK|CR_BQ
        sta CASTLE
        lda #NOSQ
        sta EPSQ
        jmp ENG_evalinit

; m8new: brand new game.
m8new:  jsr uistartpos
        lda #0
        sta UIHCNT
        sta UIHFULL
        sta UIRESULT
        sta CUROPENING
        sta UIBOOKB
        sta UIMSGB
        sta UITHINK
        sta UILSC0
        sta UILSC1
        jmp uipushhash          ; the start position is hash-history entry 0

; uipushhash: record HASH0-3 as the hash of the position before ply UIHCNT.
uipushhash:
        ldx UIHCNT
        lda HASH0
        sta UIHASH0,x
        lda HASH1
        sta UIHASH1,x
        lda HASH2
        sta UIHASH2,x
        lda HASH3
        sta UIHASH3,x
        rts

; uiplay: load the staged move (UIMFROM/UIMTO/UIFFLAGS) into the engine's
; make inputs. Staged in UI RAM rather than left in FROM/TO/MVFLAGS because
; make and unmake own those.
uiplay: lda UIMFROM
        sta FROM
        lda UIMTO
        sta TO
        lda UIFFLAGS
        sta MVFLAGS
        rts

; uiapply: commit the staged move to the game. Appends it to the history,
; makes it at PLY 0, re-derives the position state, and pushes the new hash.
;
; THE FULL-HISTORY CASE. The history and hash arrays are 256 entries — one
; page each, which is what keeps the index in X with no multiply — and a
; legal game CAN outrun them: 255 plies is 128 moves, and the 50-move rule
; permits far longer games than that. The UI used to declare "DRAW: TOO
; LONG" at ply 250, which is a result the rules do not license and one a
; player could reach while a piece up (internal/ui TestLongGameIsNotDrawn).
;
; So instead the game GOES ON and the RECORDING degrades. At ply 255 the
; move is played, the last slot is reused for it and UIHCNT stays at 255 —
; nothing is ever written past index 255 and nothing wraps to index 0. The
; current position's hash still lands at index UIHCNT, so uireps still
; compares like with like: it can only see the game's first 255 plies, and
; therefore only ever UNDER-counts repetitions. The 50-move rule is a
; counter, not a history, so it is untouched — as are mate and stalemate,
; which is everything needed to finish the game.
;
; What is lost is exact: the move panel stops growing, and takeback is
; refused for the rest of the game (cmd_take) rather than replaying to a
; position that is not the one before the last move. UIHFULL says which.
uiapply:
        jsr uiplay
        ldx UIHCNT
        lda FROM
        sta UIHFROM,x
        lda TO
        sta UIHTO,x
        lda MVFLAGS
        sta UIHFLAG,x
        lda #0
        sta PLY
        sta HVALID
        jsr ENG_make            ; PLY -> 1
        lda #0
        sta PLY
        lda UIHCNT
        cmp #255                ; the arrays are full: stop recording, but
        beq uafull              ;  keep playing
        inc UIHCNT
        bne uapush              ; always (UIHCNT is now 1..255)
uafull: lda #1
        sta UIHFULL
uapush: jsr ENG_evalinit
        jmp uipushhash

; ===========================================================================
; Move generation, validation and legality — all borrowed from the engine.
;
; The UI contains NO chess rules. Castling, en passant and promotion are
; free because the generator already emits them as ordinary list entries,
; and "is this move legal" is the engine's own test, lifted verbatim from
; search.s sdomove.
; ===========================================================================

; uigen: generate the side to move's pseudo-legal moves at ply 0 and point
; the walk cursor at the list base.
uigen:  lda #0
        sta PLY
        sta HVALID
        lda #<MOVESTACK
        sta MSP
        lda #>MOVESTACK
        sta MSP+1
        jsr ENG_gennodef
        lda PLYBASELO
        sta UIPTRL
        lda PLYBASEHI
        sta UIPTRH
        rts

; uinext: fetch the next list entry into FROM/TO/MVFLAGS and advance the
; cursor. C=1 = a move was fetched, C=0 = the list is exhausted.
; A list entry is four bytes: tier, from, to, flags.
uinext: lda UIPTRL
        cmp PLYENDLO
        bne unget
        lda UIPTRH
        cmp PLYENDHI
        bne unget
        clc
        rts
unget:  lda UIPTRL
        sta T0
        lda UIPTRH
        sta T0+1
        ldy #1
        lda (T0),y
        sta FROM
        iny
        lda (T0),y
        sta TO
        iny
        lda (T0),y
        sta MVFLAGS
        lda UIPTRL
        clc
        adc #4
        sta UIPTRL
        bcc :+
        inc UIPTRH
:       sec
        rts

; uitrylegal: make FROM/TO/MVFLAGS, run the engine's own legality test, then
; unmake unconditionally. C=1 = legal. The test is search.s slfull: after
; make, the side that just moved must not have left its king attacked.
;
; Deliberately NOT sdomove's whole path: the search's pre-make evasion filter
; (sdevade) is an optimization that needs CHECKERSQ[PLY], which only make's
; own gives-check propagation fills in, and the UI validates a move typed at
; ply 0 where no make has run. slfull alone is the complete test - the filter
; only ever removes work from in front of it - so the UI stays rule-free and
; correct for every move shape, at a cost nobody can measure.
uitrylegal:
        lda #0
        sta PLY
        jsr ENG_make            ; PLY -> 1; SIDE is now the opponent's
        lda SIDE
        sta ATSIDE
        eor #COLORMASK
        asl
        tay
        lda PIECESQ,y           ; the mover's king
        sta ATSQ
        jsr ENG_attacked        ; C=1 -> the mover left his king attacked
        lda #0
        rol                     ; A = 1 if illegal
        sta UITMPB
        jsr ENG_unmake
        lda UITMPB
        eor #1
        lsr                     ; C = 1 if legal
        rts

; uilegals: UINLEGAL = how many legal moves the side to move has, saturating
; at 255. Zero plus in check is checkmate; zero and not in check is
; stalemate. Costs one generate plus one make/unmake per pseudo-legal move —
; of order 20,000 cycles, against a move that takes millions.
uilegals:
        lda #0
        sta UINLEGAL
        jsr uigen
ulgl:   jsr uinext
        bcc ulgx
        jsr uitrylegal
        bcc ulgl
        ldx UINLEGAL
        inx
        beq ulgl                ; saturate rather than wrap
        stx UINLEGAL
        bne ulgl
ulgx:   rts

; uifind: look for UIMFROM/UIMTO (and, for a promotion, UIMPROM) in the
; generated list. On a match UIFOUND is nonzero and UIFFLAGS holds the
; GENERATOR's flags, so castling and en passant carry FL_CASTLE / FL_EP
; without the UI knowing what those are.
uifind: lda #0
        sta UIFOUND
        jsr uigen
uffl:   jsr uinext
        bcc uffx
        lda FROM
        cmp UIMFROM
        bne uffl
        lda TO
        cmp UIMTO
        bne uffl
        lda MVFLAGS
        and #FL_PROMO
        beq uffhit              ; not a promotion: from/to is unique
        cmp UIMPROM
        bne uffl                ; a different promotion piece
uffhit: lda MVFLAGS
        sta UIFFLAGS
        lda #1
        sta UIFOUND
uffx:   rts

; uipromoq: C=1 if UIMFROM/UIMTO names a promotion (so the UI must ask which
; piece). Only called when the typed move had no fifth character.
uipromoq:
        jsr uigen
upql:   jsr uinext
        bcc upqx
        lda FROM
        cmp UIMFROM
        bne upql
        lda TO
        cmp UIMTO
        bne upql
        lda MVFLAGS
        and #FL_PROMO
        beq upql
        sec
        rts
upqx:   clc
        rts

; uireps: UISEEN = how many times the CURRENT position has occurred earlier
; in the game. Two prior occurrences is a threefold repetition. Only
; positions within the last HALFMOVE plies can repeat, and only every second
; ply has the same side to move.
uireps: lda #0
        sta UISEEN
        lda UIHCNT
        sec
        sbc #2
        bcc urpx
        sta UITMPB              ; scan index
        lda UIHCNT
        sec
        sbc HALFMOVE
        bcs :+
        lda #0
:       sta UISCR0              ; lower bound
urpl:   lda UITMPB
        cmp UISCR0
        bcc urpx
        ldx UITMPB
        ldy UIHCNT
        lda UIHASH0,x
        cmp UIHASH0,y
        bne urpn
        lda UIHASH1,x
        cmp UIHASH1,y
        bne urpn
        lda UIHASH2,x
        cmp UIHASH2,y
        bne urpn
        lda UIHASH3,x
        cmp UIHASH3,y
        bne urpn
        inc UISEEN
urpn:   lda UITMPB
        sec
        sbc #2
        bcc urpx
        sta UITMPB
        jmp urpl
urpx:   rts

; uisync: re-derive everything that depends on the position, and decide
; whether the game is over. Called once per turn, before painting.
uisync: lda #0
        sta PLY
        sta HVALID
        sta ABORT
        jsr ENG_evalinit
        jsr ENG_curincheck
        lda #0
        rol
        sta UICHK
        jsr uilegals
        lda UIRESULT
        cmp #RES_RESIGN         ; a resignation or an agreed draw is a
        bcs usyx                ;  DECISION, not a position property
        lda #0
        sta UIRESULT
        lda UINLEGAL
        bne usymv
        lda SIDE                ; mated / stalemated: the side to move loses
        eor #COLORMASK
        sta UIWIN
        lda UICHK
        beq usysm
        lda #RES_MATE
        sta UIRESULT
        rts
usysm:  lda #RES_STALE
        sta UIRESULT
        rts
usymv:  lda HALFMOVE
        cmp #100
        bcc :+
        lda #RES_50
        sta UIRESULT
        rts
:       jsr uireps
        lda UISEEN
        cmp #2
        bcc usyx
        lda #RES_REP
        sta UIRESULT
usyx:   rts

; ===========================================================================
; The engine's turn
; ===========================================================================

m8engine:
        lda #CHKROW             ; a static "thinking" marker; the live one is
        ldx #0                  ;  the between-iterations readout on row 14
        jsr uigotorc
        lda #<s_thinking
        ldx #>s_thinking
        jsr uiputs
        lda UIDHGR              ; on the board screen the ONLY place it can
        beq :+                  ;  appear is the window's first row, and
        jsr ui80stat            ;  nothing repaints that until the search's
:                              ;  first completed iteration
        jsr entseed             ; SEED = the collected keystroke entropy
        jsr uibookrnd           ; BOOKRND likewise (book weighted pick)
        lda #0
        sta PLY
        sta HVALID
        sta ABORT
        jsr ENG_evalinit        ; root HASH0-3 for the probe
        jsr ENG_bookprobe       ; C=1 -> BESTFROM/TO/FLAGS + CUROPENING
        bcc mesearch
        jsr uibookname
        lda #0
        sta UITHINK             ; a book move did no searching: the previous
        jmp meapply             ;  move's depth/score readout would be a lie
mesearch:
        lda #0
        sta UIBOOKB             ; out of book: drop the opening line
        jsr uilimits
        jsr uidrive
        lda SCORE
        sta UILSC0
        lda SCORE+1
        sta UILSC1
meapply:
        ; Validate the engine's own move against the generator before playing
        ; it. The search always returns a legal move and the book is keyed on
        ; a 32-bit hash, so this never fires — but "never corrupt the
        ; position" is cheaper to guarantee than to debug, and one generate
        ; plus one make/unmake is invisible next to a search.
        lda BESTFROM
        sta UIMFROM
        lda BESTTO
        sta UIMTO
        lda BESTFLAGS
        and #FL_PROMO
        sta UIMPROM
        jsr uifind
        lda UIFOUND
        beq mebad
        jsr uiplay
        jsr uitrylegal
        bcc mebad
        jmp uiapply
mebad:  lda #RES_ERR
        sta UIRESULT
        lda #<m_enginebad
        ldx #>m_enginebad
        jmp uisetmsg

; uibookrnd: manufacture the book's 32-bit weighted-pick random from the
; keystroke entropy collector — the only unpredictable quantity an Apple IIe
; offers (asm/entropy.inc).
;
; The four output bytes must be a BIJECTION of the four input bytes, or the
; book's 32-bit weighted pick is not really 32 bits. It was not: the last
; byte read ENTROPY's LOW half, making
;
;       BOOKRND+3 = ENTROPY0 EOR ENTCNT1 = BOOKRND+0 EOR BOOKRND+2
;
; identically — a whole byte of linear dependency. Equivalently: ENTROPY's
; HIGH byte reached BOOKRND nowhere at all, so the map had GF(2) rank 24 and
; the reachable set was 2^24, not 2^32, no matter how good the collector got.
; Reading ENTROPY's high half here removes it for ZERO bytes (both operands
; are absolute), and is only possible because ENTROPY widened to 16 bits on
; 2026-07-29. With E = ENTROPY, C = ENTCNT the map is now invertible:
;
;       B0 = E0            ->  E0 = B0
;       B1 = C0 EOR E0     ->  C0 = B1 EOR B0
;       B2 = C1            ->  C1 = B2
;       B3 = E1 EOR C1     ->  E1 = B3 EOR B2
uibookrnd:
        lda ENTROPY
        sta BOOKRND
        lda ENTCNT
        eor ENTROPY
        sta BOOKRND+1
        lda ENTCNT+1
        sta BOOKRND+2
        lda ENTROPY+1
        eor ENTCNT+1
        sta BOOKRND+3
        rts

; ---------------------------------------------------------------------------
; uilimits: install this move's search limits from the level.
;
; ★ THE SAFETY-MARGIN RULE (docs/ui-design.md §6.2, defs.inc SOFTMARGIN,
; internal/chesstest SoftClockMargin). The engine's FT2_SOFTCLK cost table
; holds the RAW measured per-node cost; the deliberate safety bias lives on
; the BUDGET side, because every use of the estimate is `estimate >= limit`
; and scaling the limits is free — they are written once per move, off the
; hot path. Indexed by the OCTAVE of the budget (the position of its top set
; bit in 256-cycle units):
;
;       <= ~8 s   margin 127%   ->  BUDGET * 202 >> 8
;       ~8-16 s   margin 113%   ->  BUDGET * 227 >> 8
;       >  ~16 s  margin 100%   ->  unchanged
;
; A shift loop for the top set bit, a table read, and one 24x8
; shift-and-add — once per move, no division anywhere.
;
; It must scale ALL FIVE limits, not just BUDGET: the engine compares its
; estimate against BUDGET, ABORTL, CEILMAX, UNSTCEIL and MINSPEND. ABORTL is
; derived on-device from BUDGET (or from CEILMAX under FT2_ADAPT) so it
; inherits the scaling; CEILMAX / UNSTCEIL / MINSPEND are derived here from
; the ALREADY-SCALED base, which is what makes all five share ONE margin.
; Deriving each from its own octave would let CEILMAX — up to 4x base — pick
; a looser margin and quietly widen the adaptive ceiling.
; ---------------------------------------------------------------------------
uilimits:
        ldx UILEVEL
        dex
        cpx #NLEVELS
        bcc :+
        ldx #3                  ; defensive: an out-of-range level
:       lda LVDEPTH,x
        sta MAXDEPTH
        lda LVB0,x
        sta BUDGET0
        lda LVB1,x
        sta BUDGET1
        lda LVB2,x
        sta BUDGET2
        jsr uimargin
        lda BUDGET0
        ora BUDGET1
        ora BUDGET2
        bne ultimed
        lda FEATURES2           ; fixed-depth level: no clock is read at all
        and #255-FT2_ADAPT      ; single bit, so this is ~FT2_ADAPT
        sta FEATURES2
        rts
ultimed:
        ; CEILMAX = 4*base, UNSTCEIL = 3*base, MINSPEND = base/4, all from
        ; the scaled base (mirroring chesstest.SetAdaptive with no bank).
        lda BUDGET0
        sta UIT0
        lda BUDGET1
        sta UIT1
        lda BUDGET2
        sta UIT2
        asl UIT0                ; 2*base
        rol UIT1
        rol UIT2
        lda UIT0                ; 3*base -> UNSTCEIL
        clc
        adc BUDGET0
        sta UNSTCEIL0
        lda UIT1
        adc BUDGET1
        sta UNSTCEIL1
        lda UIT2
        adc BUDGET2
        sta UNSTCEIL2
        asl UIT0                ; 4*base -> CEILMAX
        rol UIT1
        rol UIT2
        lda UIT0
        sta CEILMAX0
        lda UIT1
        sta CEILMAX1
        lda UIT2
        sta CEILMAX2
        lda BUDGET2             ; base/4 -> MINSPEND
        lsr
        sta MINSPEND2
        lda BUDGET1
        ror
        sta MINSPEND1
        lda BUDGET0
        ror
        sta MINSPEND0
        lsr MINSPEND2
        ror MINSPEND1
        ror MINSPEND0
        lda FEATURES2
        ora #FT2_ADAPT
        sta FEATURES2
        rts

; uimargin: BUDGET *= KTAB[octave(BUDGET)] / 128, in place.
;
; ★ THE DIVISOR IS 128, NOT 256, SINCE 2026-07-30, and that is the whole
; point. A margin BELOW 100% — poking a budget LARGER than the level's
; nominal allocation — is not a rounding curiosity, it is what octaves 15 and
; up measured out at: the estimator reads ~3-5% HIGH there and the predictive
; gate doubles that into a ~10% SPEND deficit (docs/results.md 2026-07-30).
; Over 256 that needs K = 25600/90 = 284, which does not fit in a byte, so the
; old encoding could express safety margins and nothing else. Over 128 the
; same byte covers 50-255% and the shipped ladder is 101 / 113 / 139.
;
; It costs SIX BYTES: the accumulator is unchanged and still >>8, but the
; ADDEND is pre-doubled (asl/rol/rol below), so the loop forms 2*BUDGET*K and
; the >>8 delivers BUDGET*K/128. The `KTAB entry 0 means leave it alone`
; early-out is gone with it — identity is now K=128, every timed level
; multiplies, and (BUDGET*128*2)>>8 == BUDGET exactly.
uimargin:
        lda BUDGET0
        ora BUDGET1
        ora BUDGET2
        bne :+
        rts                     ; fixed depth: nothing to scale
:       lda BUDGET0             ; find the top set bit by shifting a copy down
        sta MUL0
        lda BUDGET1
        sta MUL1
        lda BUDGET2
        sta MUL2
        ldx #$FF
umsh:   inx
        lsr MUL2
        ror MUL1
        ror MUL0
        lda MUL0
        ora MUL1
        ora MUL2
        bne umsh
        cpx #24
        bcc :+
        ldx #23
:       lda KTAB,x
        sta UIK
        asl BUDGET0             ; the loop's ADDEND is 2*BUDGET, so the >>8
        rol BUDGET1             ;  below is really a >>7 on BUDGET*K. Safe in
        rol BUDGET2             ;  24 bits: the largest level is 60 s =
                                ;  239,176 units and 2x that is 478,352.
        lda #0
        sta UIACC0
        sta UIACC1
        sta UIACC2
        sta UIACC3
        ldy #8                  ; 24 x 8 shift-and-add, MSB of K first
umml:   asl UIACC0
        rol UIACC1
        rol UIACC2
        rol UIACC3
        asl UIK
        bcc ummn
        clc
        lda UIACC0
        adc BUDGET0
        sta UIACC0
        lda UIACC1
        adc BUDGET1
        sta UIACC1
        lda UIACC2
        adc BUDGET2
        sta UIACC2
        lda UIACC3
        adc #0
        sta UIACC3
ummn:   dey
        bne umml
        lda UIACC1              ; >> 8 of 2*BUDGET*K, i.e. BUDGET*K/128; K <=
        sta BUDGET0             ;  255 keeps the result inside 24 bits
        lda UIACC2
        sta BUDGET1
        lda UIACC3
        sta BUDGET2
        rts

; ---------------------------------------------------------------------------
; uidrive: the UI's own iterative-deepening driver — a port of engine.s's
; entry setup plus idloop, living entirely in the Language Card.
;
; It exists so the screen can show the search getting deeper WITHOUT touching
; the hot path: search.s's only periodic hook is checkclock, which is on the
; hot path and is not touched. Painting between completed ID iterations costs
; ~2,000 cycles per iteration against a search of order 10^7, and the tree
; the engine explores is byte-identical to the one the UCI bridge gets.
; ---------------------------------------------------------------------------
uidrive:
        lda #0
        sta PSP0
        sta PSP1
        sta ABORT
        sta NODECNT
        sta PLY
        sta HVALID
        jsr ENG_evalinit
        lda #NOSQ
        sta BESTFROM
        ; FT2_SOFTCLK arming. engine.s's ENTRY block does this, and a driver
        ; that jumps straight to `iterate` skips it (design §6.2's integration
        ; note): patch ccsite's operand over to the accumulating entry point
        ; and prime the estimate with the 128 nodes that run before the first
        ; poll. PHASE is live here — evalinit just built it.
        lda FEATURES2
        and #FT2_SOFTCLK
        beq udnosc
        lda #<ENG_checkclocks
        sta ENG_ccsite+1
        lda #>ENG_checkclocks
        sta ENG_ccsite+2
        lda #0
        sta CLOCK_TRAP+2
        lda PHASE
        cmp #NPCOST
        bcc :+
        lda #NPCOST-1
:       tax
        lda ENG_PCOSTLO,x
        sta CLOCK_TRAP
        lda ENG_PCOSTHI,x
        sta CLOCK_TRAP+1
udnosc:
        lda MAXDEPTH
        sta MAXCAP
        lda BUDGET0             ; hard-abort limit = 2x budget
        asl
        sta ABORTL0
        lda BUDGET1
        rol
        sta ABORTL1
        lda BUDGET2
        rol
        sta ABORTL2
        lda FEATURES2
        and #FT2_ADAPT
        beq :+
        jsr ENG_adaptaborthi    ; ABORTL = 2*CEILMAX instead
:       lda BUDGET0
        ora BUDGET1
        ora BUDGET2
        bne udid
        lda MAXCAP              ; fixed-depth mode: one iteration at the cap
        sta CURDEPTH
        jsr ENG_iterate
        jmp uithinkln
udid:   lda #1
        sta CURDEPTH
udloop: lda CLOCK_TRAP          ; iteration start (latched 24-bit)
        sta ITSTART0
        lda CLOCK_TRAP+1
        sta ITSTART1
        lda CLOCK_TRAP+2
        sta ITSTART2
        jsr ENG_iterate
        lda ABORT
        beq udok
        ; Aborted mid-iteration: a partial iteration's "best" is only the
        ; first root move it happened to search, so prefer the last COMPLETED
        ; iteration's move, score and depth.
        lda PREVFROM
        cmp #NOSQ
        beq udrep
        sta BESTFROM
        lda PREVTO
        sta BESTTO
        lda PREVFLAGS
        sta BESTFLAGS
        lda PREVSC0
        sta SCORE
        lda PREVSC1
        sta SCORE+1
        dec CURDEPTH            ; iteration 1 never aborts, so >= 1
udrep:  jmp uithinkln
udok:   jsr uithinkln           ; paint the completed iteration
        lda SCORE+1             ; a winning mate is exact: deepening can't
        bmi :+                  ;  improve it
        cmp #MATEZONEHI
        bcs udx
:       jsr ENG_adaptmaybe      ; FT2_ADAPT: raise the ceiling or easy-stop
        bne udx
        lda CLOCK_TRAP          ; predictive gate: cost = now - ITSTART,
        sec                     ;  est next = 2x cost
        sbc ITSTART0
        sta T0
        lda CLOCK_TRAP+1
        sbc ITSTART1
        sta T1
        lda CLOCK_TRAP+2
        sbc ITSTART2
        asl T0
        rol T1
        rol
        bcs udx                 ; overflow: nowhere near fitting
        sta T2
        lda CLOCK_TRAP
        clc
        adc T0
        sta T0
        lda CLOCK_TRAP+1
        adc T1
        sta T1
        lda CLOCK_TRAP+2
        adc T2
        bcs udx
        sta T2
        lda T0                  ; start it only if now + est fits the budget
        cmp BUDGET0
        lda T1
        sbc BUDGET1
        lda T2
        sbc BUDGET2
        bcs udx
        inc CURDEPTH
        lda CURDEPTH
        cmp MAXCAP
        beq udlj                ; continue while CURDEPTH <= MAXCAP
        bcs udx
udlj:   jmp udloop
udx:    rts

; ===========================================================================
; Input
; ===========================================================================

; uiread: read one line into UIBUF. EVERY key the UI reads goes through
; entkey, so every keystroke folds its arrival time into the entropy
; accumulator — that collector is the shipped engine's only source of
; randomness on a machine with no clock, and a plain keyboard poll here
; would silently destroy it.
uiread: lda #0
        sta UIBLEN
urdl:   jsr uiprompt
        jsr entkey
        cmp #$0D                ; RETURN
        beq urdx
        cmp #$08                ; left arrow
        beq urdbs
        cmp #$7F                ; DELETE
        beq urdbs
        cmp #$1B                ; ESC: swap board screen <-> text screen
        beq urdesc
        cmp #$20
        bcc urdl                ; other control keys: ignore
        cmp #$7B
        bcs urdl
        jsr uilower
        ldx UIBLEN
        cpx #UIBUFMAX
        bcs urdl
        sta UIBUF,x
        inc UIBLEN
        bne urdl
urdbs:  lda UIBLEN
        beq urdl
        dec UIBLEN
        jmp urdl
urdesc: jsr uiswap
        jmp urdl
urdx:   rts

; ---------------------------------------------------------------------------
; uiswap / uidhon / uidhoff: the board ships in MIXED MODE -- the double-hi-res
; board on scanlines 0-159 with a FOUR-ROW, EIGHTY-COLUMN text window under it
; -- and ESC swaps that whole screen for the 40-column text screen, which
; carries the move list, the full help and everything else that does not fit
; in four rows.
;
; ★ WHAT MIXED MODE COST, AND WHY IT IS PAID (2026-08-01). It is arithmetic,
; and the arithmetic is in AUX RAM, not on the screen. Double hi-res needs
; 80COL on, and with 80COL on the text window is 80-COLUMN text: its EVEN
; columns are fetched from AUX $0400-$07FF. Rows 20-23 are aux $0650-$0677,
; $06D0-$06F7, $0750-$0777 and $07D0-$07F7 -- 160 bytes, in four pieces, in
; the MIDDLE of the aux hole the opening book used to fill from $0200.
;
; The book did not get chopped into four pieces. Its 1,702-byte NAME TABLE
; moved out of the blob into LANGUAGE CARD BANK 2 (the probe never reads it;
; only uibookname does, and that runs at $E000 where a bank switch is free),
; leaving 5,705 B of header + entries that sit at aux $0800-$1E48 -- entirely
; ABOVE the text page. See cmd/genbook and docs/ui-design.md §14.
;
; ★ THE TWO SCREENS SHARE MAIN $0400-$07FF, and rows 20-23 are where they
; collide: the window's ODD columns are the very bytes the 40-column screen
; uses for its help and prompt rows. Neither renderer was changed to avoid
; that. Instead the window is composed in an 80-byte staging buffer (UI80BUF)
; and blitted, so it always READS from LC RAM and never from the screen it is
; about to overwrite -- and uiswap repaints the 40-column screen on the way
; back. Cost: one more 23,000-cycle 40-column repaint per ESC, against a
; whole class of "which renderer ran last" bugs.
;
; ★ SWITCHES THAT WERE UNVERIFIABLE AND ARE NOT ANY MORE. goapple2's IIe model
; used to implement neither 80STORE nor AN3, so this whole routine would have
; been hardware-only. It implements both now, with the 80STORE precedence that
; makes it dangerous, and exposes DHires() -- so internal/ui asserts the exact
; switch state below rather than trusting this comment.
; ---------------------------------------------------------------------------
uiswap:
        lda UIDHGRDEF           ; no artwork resident => there is no board to
        beq uiswx               ;  swap to (see m8main); ESC does nothing
        lda UIDHGR
        eor #1
        sta UIDHGR
        beq usw40
        jsr uidhon              ; to the board: the board screen AND the text
        jsr dhboard             ;  window under it are both stale
        jmp uidhtext
usw40:  jsr uidhoff             ; back to 40 columns: rows 20-23 of the text
        jmp uipaint40           ;  page have been carrying the window's ODD
                                ;  columns, so they are not text any more.
                                ;  uiread's loop repaints the prompt row next.
uiswx:  rts

; uidhon: hand the display to double hi-res, MIXED with a four-row text window.
uidhon:
        sta CLR80STORE          ; ★ LOAD-BEARING, and now doubly so. The board
                                ;  renderer reaches the aux half through
                                ;  RAMWRT, and the text window reaches the aux
                                ;  text page the same way; with 80STORE ON,
                                ;  BOTH $0400-$07FF and $2000-$3FFF follow
                                ;  PAGE2 and IGNORE RAMWRT, so every aux byte
                                ;  would land in MAIN -- an interleaved-garbage
                                ;  board over a half-written window.
        sta SET80COLON          ; the scanner fetches aux+main per byte column
        sta SETDHIRES           ; AN3 low: 560 dots, not 280
        sta GRSET               ; graphics
        sta HIRESSET            ; hi-res
        sta MIXSET              ; ★ MIXED: graphics on scanlines 0-159, four
                                ;  text rows below. The board is 8 x DHROWS =
                                ;  152 scanlines from DHTOP = 4, i.e. 4-155,
                                ;  so it fits with a four-line border top and
                                ;  bottom and the artwork is untouched.
        sta TXTPAGE1            ; page 1 = $2000-$3FFF in both banks, and
                                ;  $0400-$07FF for the window
        rts

; uidhoff: hand it back to the 40-column text screen, exactly as m8main took
; it at boot. AN3 goes back high too: leaving DHIRES selected under 40-column
; text is harmless on a IIe, but "put every switch back" is one fewer state to
; reason about.
uidhoff:
        sta CLR80STORE
        sta SET80COLOFF
        sta CLRDHIRES
        sta SETALTCHAR
        sta TXTSET
        sta MIXCLR
        sta TXTPAGE1
        rts

; uilower: fold an ASCII letter to lower case, so an Apple IIe with CAPS
; LOCK down types the same moves as one without it.
uilower:
        cmp #'A'
        bcc ulwx
        cmp #'Z'+1
        bcs ulwx
        ora #$20
ulwx:   rts

; uidispatch: act on the line just read. Files are a-h, so a bare letter is
; unambiguously a command: a ONE-character line is a command, a four- or
; five-character line is a move, anything else is a typo.
uidispatch:
        lda UIBLEN
        beq udpx
        cmp #1
        beq udpcmd
        cmp #4
        beq udpmove
        cmp #5
        beq udpmove
        lda #<m_badin
        ldx #>m_badin
        jmp uisetmsg
udpx:   rts

udpcmd: lda UIBUF
        ldx #0
udpcl:  cmp cmdkeys,x
        beq udpgo
        inx
        cpx #NCMDS
        bcc udpcl
        lda #<m_badcmd
        ldx #>m_badcmd
        jmp uisetmsg
udpgo:  lda cmdhi,x
        pha
        lda cmdlo,x
        pha
        rts                     ; rts-dispatch (addresses stored minus one)

; ---- move entry ----
udpmove:
        lda UIRESULT
        beq :+
        lda #<m_over
        ldx #>m_over
        jmp uisetmsg
:       jsr uimyturn
        bcs :+
        lda #<m_notyours
        ldx #>m_notyours
        jmp uisetmsg
:       jsr uiparse
        bcs :+
        lda #<m_badin
        ldx #>m_badin
        jmp uisetmsg
:       lda UIMPROM
        bne :+
        jsr uipromoq            ; a promotion typed without its piece
        bcc :+
        jsr uiaskpromo
        bcs :+
        rts                     ; cancelled: nothing typed, nothing played
:       jsr uifind
        lda UIFOUND
        beq udpill
        jsr uiplay
        jsr uitrylegal
        bcc udpill
        jsr uiapply
        jmp uiclrmsg
udpill: lda #<m_illegal
        ldx #>m_illegal
        jmp uisetmsg

; uiparse: UIBUF -> UIMFROM / UIMTO / UIMPROM. C=1 on success.
uiparse:
        lda #0
        sta UIMPROM
        ldx #0
        jsr upsq
        bcc upfail
        sta UIMFROM
        ldx #2
        jsr upsq
        bcc upfail
        sta UIMTO
        lda UIBLEN
        cmp #5
        bne upok
        lda UIBUF+4
        jsr upromo
        bcc upfail
upok:   sec
        rts
upfail: clc
        rts

; upsq: two characters at UIBUF+X -> a 0x88 square in A. C=1 on success.
upsq:   lda UIBUF,x
        sec
        sbc #'a'
        cmp #8
        bcs upsf
        sta UITMPB
        lda UIBUF+1,x
        sec
        sbc #'1'
        cmp #8
        bcs upsf
        asl
        asl
        asl
        asl
        ora UITMPB
        sec
        rts
upsf:   clc
        rts

; upromo: A = a promotion letter -> UIMPROM (piece type 2..5). C=1 on success.
upromo: ldy #0
uprl:   cmp promoltr,y
        beq uprh
        iny
        cpy #4
        bcc uprl
        clc
        rts
uprh:   tya
        clc
        adc #KNIGHT
        sta UIMPROM
        sec
        rts

; uiaskpromo: the fifth character was omitted on a promotion. Ask, and let
; RETURN or ESC back out — a prompt with no way out is a trap. C=1 = a piece
; was chosen (in UIMPROM), C=0 = cancelled.
uiaskpromo:
        lda #<m_promo
        ldx #>m_promo
        jsr uisetmsg
        jsr uipaintmsg
uapl:   jsr entkey
        cmp #$0D
        beq uapcan
        cmp #$1B
        beq uapcan
        jsr uilower
        jsr upromo
        bcc uapl
        jsr uiclrmsg
        sec
        rts
uapcan: jsr uiclrmsg
        clc
        rts

; ===========================================================================
; Commands
; ===========================================================================

cmd_new:
        jsr m8new
        jmp uiclrmsg

cmd_take:
        lda UIHFULL             ; past ply 255 the plies are no longer
        bne ctfull              ;  recorded, so there is nothing to replay TO
        lda UIHCNT
        bne :+
        lda #<m_noback
        ldx #>m_noback
        jmp uisetmsg
ctfull: lda #<m_histfull
        ldx #>m_histfull
        jmp uisetmsg
:       lda #0                  ; a takeback un-ends a finished game
        sta UIRESULT
        ; ...and RETRACTS the last search, which described a position that is
        ; about to leave the board. UILSC is the ONLY thing cmd_draw consults,
        ; and takebacks stack, so leaving it behind lets a draw be agreed on
        ; the strength of a search of a line the player has just withdrawn.
        ; Same rule m8engine already applies to the think line after a book
        ; move: no completed search for THIS position, no readout. (A is 0.)
        sta UILSC0
        sta UILSC1
        sta UITHINK
        lda SIDE                ; back to the human's own last decision:
        cmp UIHUMAN             ;  two plies if the engine has already
        bne ctone               ;  replied, one if it is still thinking
        lda UIHCNT
        cmp #2
        bcc ctone
        sec
        sbc #2
        jmp ctgo
ctone:  lda UIHCNT
        sec
        sbc #1
ctgo:   sta UITMPB
        jsr uistartpos
        lda #0
        sta UIHCNT
        jsr uipushhash
ctrl:   lda UIHCNT              ; replay, rather than keeping an undo record:
        cmp UITMPB              ;  80 bytes of code instead of a per-ply
        bcs ctdone              ;  journal, and the same array the move panel
        tax                     ;  already draws
        lda UIHFROM,x
        sta UIMFROM
        lda UIHTO,x
        sta UIMTO
        lda UIHFLAG,x
        sta UIFFLAGS
        jsr uiapply
        jmp ctrl
ctdone: jmp uiclrmsg

cmd_resign:
        lda UIRESULT
        bne :+
        ; The side TO MOVE is the one resigning, and the other one wins.
        ; Deriving that from SIDE rather than UIHUMAN is what makes it right
        ; in TWO-PLAYER mode, where UIHUMAN is the sentinel $FF and not a
        ; colour at all: `$FF EOR COLORMASK` is nonzero, so a resignation by
        ; either player used to be announced as "BLACK WINS". `R` is only
        ; reachable while UIRESULT is 0, which is only ever the side to
        ; move's own turn, so SIDE is the resigning side in every mode —
        ; and it is the same expression uisync already uses for a mate.
        lda SIDE
        eor #COLORMASK
        sta UIWIN
        lda #RES_RESIGN
        sta UIRESULT
        ; ...and clear whatever was on the message row, because uistatrow
        ; only fills in "WHITE WINS" / "BLACK WINS" when the row is EMPTY
        ; (so that "GAME OVER - N STARTS A NEW ONE" survives a repaint).
        ; Without this, resigning straight after any command that leaves a
        ; message -- S, or a declined draw -- would announce the winner
        ; nowhere. This and cmd_draw's acceptance are the only two places a
        ; result is LATCHED outside uisync, which re-derives its own.
        jmp uiclrmsg
:       rts

; cmd_draw: offer a draw. The engine accepts if its last completed search
; said it was materially worse off; otherwise it declines. (A threefold
; repetition or the 50-move rule is adjudicated automatically in uisync, so
; this is a genuine offer, not a claim.)
cmd_draw:
        lda UIRESULT
        beq :+
        rts                     ; already over
:       lda UILSC0              ; accept iff the engine's last completed
        cmp #$6A                ;  search said it was worse than -150 cp
        lda UILSC1              ;  (16-bit signed compare against $FF6A)
        sbc #$FF
        bvc :+
        eor #$80
:       bmi cdyes
cdno:   lda #<m_drawno
        ldx #>m_drawno
        jmp uisetmsg
cdyes:  lda #RES_AGREED
        sta UIRESULT
        jmp uiclrmsg            ; ...so uistatrow can say GAME DRAWN; see
                                ;  cmd_resign for why the row must be empty

cmd_level:
        lda #<m_level
        ldx #>m_level
        jsr uisetmsg
        jsr uipaintmsg
cllk:   jsr entkey
        cmp #$0D                ; RETURN or ESC backs out
        beq cllx
        cmp #$1B
        beq cllx
        sec
        sbc #'1'
        cmp #NLEVELS
        bcs cllk
        clc
        adc #1
        sta UILEVEL
cllx:   jmp uiclrmsg

; cmd_swap: cycle WHITE -> TWO PLAYERS -> BLACK -> WHITE. Two-player mode is
; the referee mode: the UI validates and displays but never searches, which
; is what you want for playing a friend, replaying a game, or setting up a
; position by hand.
;
; ★ WHAT A REAL PLAYER HIT, 2026-08-01, and both halves were this routine
; "working": "S when white immediately makes the computer move and switches to
; black; S again when black does nothing the first time, then switches the
; second." The first press handed White to the engine, which searched and
; moved before anything could be reconsidered; the second selected TWO
; PLAYERS, where nothing searches because that IS the mode -- and the only
; thing that said so was thirteen characters inside a forty-column inverse
; title bar. Two separate faults, and they compounded:
;
;   1. THE CYCLE WAS ORDERED WRONG. The very first press of an unlabelled key
;      was the DESTRUCTIVE one: it committed a move. The reordering puts the
;      HARMLESS state first -- one press gets you referee mode with nothing
;      moving, and handing a colour to the engine now takes a second,
;      deliberate press. Cost: zero bytes; it is the same three-way cycle
;      walked the other way round.
;
;   2. NOTHING WAS ANNOUNCED. It ended in `jmp uiclrmsg`, so landing in a mode
;      actively BLANKED the one row that could have named it. It now names the
;      mode it landed in AND what the next press does, on both screens (the
;      message row is painted by uipaint40 and, with the board up, by
;      uipaintmsg's window half). A one-key three-way cycle whose states do
;      not name their successor cannot be discovered from the keyboard.
cmd_swap:
        lda UIHUMAN
        beq cswt                ; White -> TWO PLAYERS: the harmless press
        cmp #COLORMASK
        beq csww                ; Black -> White
        lda #COLORMASK          ; two players -> Black
        bne cswset
cswt:   lda #$FF
        bne cswset
csww:   lda #0
cswset: sta UIHUMAN
        jsr uiwhoidx            ; 0 = White, 1 = Black, 2 = two players --
        tay                     ;  the same index the title bar's who-field
        lda sidelo,y            ;  uses, so the two can never disagree
        ldx sidehi,y
        jmp uisetmsg

cmd_help:
        lda #<m_help2
        ldx #>m_help2
        jmp uisetmsg

; cmd_quit: Q QUITS. On an Apple II with no resident operating system there
; is nowhere to quit TO, so quitting is a cold boot — which, with a disk
; still in the drive, is how you start a fresh 8fish, and otherwise is the
; machine's own start-up state. (It used to store a byte to $BFFF and fall
; into m8new: under the harness that was the exit trap, but on HARDWARE
; $BFFF is plain RAM, so Q silently DISCARDED the game under a label
; promising the opposite.)
;
; The switch to ROM cannot be made from here: this code is executing IN the
; language card at $E000, so the instruction after `lda $C082` would be
; fetched from ROM. So the last three instructions run from MAIN — six
; bytes copied to $0300, which is engine scratch (CEILMAX0) and is exactly
; the region m8machine already borrows.
cmd_quit:
        ldx #qstubend-qstub-1
cqcl:   lda qstub,x
        sta $0300,x
        dex
        bpl cqcl
        jmp $0300
qstub:  lda $C082               ; bank the ROM back in over $D000-$FFFF
        jmp ($FFFC)             ; ...and take its RESET vector: a cold boot,
qstubend:                       ;  since m8main invalidated the power-up byte

; ===========================================================================
; Painting
; ===========================================================================

; uipaint repaints EVERYTHING: the 40-column text screen every time, then --
; when the board is the screen being shown -- the double-hi-res board and the
; four-row 80-column window under it.
;
; The text screen is painted even when it is not displayed, and that is
; deliberate: it costs 23,659 cycles against the 193,667 the board costs, it
; makes ESC nearly instantaneous, and it keeps ONE repaint path -- every
; screen gate in internal/ui is a gate on bytes this always wrote. It is now
; load-bearing for a second reason: the MIXED-MODE WINDOW IS COMPOSED FROM IT.
; uidhtext copies already-rendered 40-column rows (title, status, check, think
; line, opening name, message) into its 80-byte staging buffer, so there is
; exactly one place where each of those strings is built.
uipaint:
        jsr uipaint40
        lda UIDHGR
        beq uipx
        jsr dhboard
        jmp uidhtext            ; ...and the text window, whose MAIN half
                                ;  uipaint40 has just overwritten with rows
                                ;  20-23 of the 40-column screen
uipx:   rts

uipaint40:
        jsr uicls
        jsr uititle
        jsr uicoords
        jsr uimoves
        jsr uiboard
        jsr uistatrow
        jsr uichkrow
        lda #THINKROW
        ldx #0
        jsr uigotorc
        lda #<UITHINK
        ldx #>UITHINK
        jsr uiputs
        jsr uipaintbook
        jsr uipaintmsg
        lda #HELPROW
        ldx #0
        jsr uigotorc
        lda #<s_help1
        ldx #>s_help1
        jsr uiputs
        lda #HELPROW+1
        ldx #0
        jsr uigotorc
        lda #<s_help2
        ldx #>s_help2
        jmp uiputs

; uipaintmsg: the message row, on BOTH screens.
;
; ★ THE WINDOW HALF IS NOT OPTIONAL, and leaving it out shipped a bug. The
; 40-column message row is row 17, and MIXED MODE SHOWS ONLY ROWS 20-23 -- so
; a caller that set a message and painted it without a full repaint (cmd_level
; and uiaskpromo: both put up a prompt and then block in entkey) wrote it to a
; row the player cannot see while the board is up. "L only shows extra info in
; text mode", reported from a real IIe, 2026-08-01: the level prompt was
; painted, correctly, onto an invisible screen.
;
; Fixing it HERE rather than at the two call sites is what keeps the two
; screens from forking: there is one routine that paints a message, and it
; paints it wherever the message row currently is. uipaint40 also comes
; through here, which costs one redundant window-row blit per full repaint (a
; few thousand cycles against the 23,659 + 193,667 a repaint already spends)
; and buys the guarantee that no future caller can forget.
uipaintmsg:
        lda #MSGROW
        ldx #0
        jsr uigotorc
        lda #<UIMSGB
        ldx #>UIMSGB
        jsr uiputs
        lda UIDHGR              ; ...and the window's third row, if the board
        beq uipmx               ;  is the screen being shown
        jmp ui80msg
uipmx:  rts

uipaintbook:
        lda #BOOKROW
        ldx #0
        jsr uigotorc
        lda #<UIBOOKB
        ldx #>UIBOOKB
        jmp uiputs

; uititle: the inverse status bar, with the level digit and the human's
; colour poked into the fixed template.
uititle:
        lda #0
        ldx #0
        jsr uigotorc
        lda #<s_title
        ldx #>s_title
        jsr uiputs
        lda UILEVEL
        clc
        adc #'0'
        and #$3F                ; inverse video
        ldy #TITLELVL
        sta (SCRPTR),y
        jsr uiwhoidx
        tax
        lda WHOOFF,x
        tax
        ldy #TITLEWHO
utcl:   lda s_who,x
        sta (SCRPTR),y
        inx
        iny
        cpy #TITLEWHO+13
        bcc utcl
        rts

; uistatrow: row 12, left column — side to move, or how the game ended.
uistatrow:
        ldx UIRESULT
        bne usrres
        ldx #0
        lda SIDE
        beq :+
        ldx #1
:       lda stmlo,x
        sta UITMPB
        lda stmhi,x
        jmp usrput
usrres: lda reslo,x
        sta UITMPB
        lda reshi,x
usrput: sta UICNT2
        lda #STATROW
        ldx #0
        jsr uigotorc
        lda UITMPB
        ldx UICNT2
        jsr uiputs
        ; A finished game explains itself in words on the message row — but
        ; only when nothing more recent is there, so "GAME OVER - N STARTS A
        ; NEW ONE" (the reply to a move typed after the end) is not wiped out
        ; by the next repaint.
        lda UIRESULT
        beq :+
        lda UIMSGB
        bne :+
        jsr uiresultmsg
:       rts

; uiresultmsg: fill the message row with the outcome in words.
uiresultmsg:
        ldx UIRESULT
        cpx #RES_ERR
        beq urmx                ; not a draw: m8engine's own message stands
        cpx #RES_MATE
        beq urmwin
        cpx #RES_RESIGN
        beq urmwin
        lda #<m_drawn
        ldx #>m_drawn
        jmp uisetmsg
urmwin: lda UIWIN
        beq :+
        lda #<m_blackwins
        ldx #>m_blackwins
        jmp uisetmsg
:       lda #<m_whitewins
        ldx #>m_whitewins
        jmp uisetmsg
urmx:   rts

; uichkrow: row 13 — check, while the game is live.
uichkrow:
        lda UIRESULT
        bne ucrx
        lda UICHK
        beq ucrx
        lda #CHKROW
        ldx #0
        jsr uigotorc
        lda #<s_check
        ldx #>s_check
        jmp uiputs
ucrx:   rts

; ---------------------------------------------------------------------------
; uiprompt: the input line with everything typed so far and a cursor block.
;
; It is the ONE row that is repainted on every keystroke rather than once per
; move, and it is also the one row the two screens fight over: 40-column row 23
; and the mixed-mode window's bottom row are the same 40 bytes of MAIN. So it
; composes into UI80BUF first and then writes outwards -- the left 40 columns
; to the text page, and (in mixed mode) the whole 80 to the window. Nothing
; reads back a screen it is about to overwrite.
; ---------------------------------------------------------------------------
uiprompt:
        jsr ui80clr             ; blank the whole line first: a backspace has
                                ;  to visibly remove the character
        lda UIRESULT
        bne upcmd
        jsr uimyturn
        bcs upmv
upcmd:  lda #<s_cmdq
        ldx #>s_cmdq
        jmp upput
upmv:   lda #<s_movq
        ldx #>s_movq
upput:  ldy #0
        jsr ui80puts
        ldx #PROMPTLEN
        ldy #0
uprpl:  cpy UIBLEN
        bcs uprcur
        lda UIBUF,y
        ora #$80
        sta UI80BUF,x
        inx
        iny
        bne uprpl
uprcur: lda #$20                ; inverse space: the cursor
        sta UI80BUF,x
        ; --- the 40-column screen's row 23 ---
        lda #PROMPTROW
        ldx #0
        jsr uigotorc
        ldy #39
uprc:   lda UI80BUF,y
        sta (SCRPTR),y
        dey
        bpl uprc
        ; --- and, in mixed mode, the window's bottom row, with the second
        ; help line filling the 40 columns the prompt does not use ---
        lda UIDHGR
        beq uprx
        lda #<s_help2
        ldx #>s_help2
        ldy #WIN80R
        jsr ui80puts
        lda #DHTXTTOP+3
        jmp ui80row
uprx:   rts

; ===========================================================================
; The mixed-mode text window: four rows of EIGHTY columns under the board.
;
; ★ HOW 80-COLUMN TEXT IS STORED. With 80COL on, the scanner fetches TWO bytes
; per column position: the AUX byte first, then the MAIN byte. So for row base
; B and 0 <= i < 40, aux B+i is screen column 2i and main B+i is column 2i+1.
; Every row here is therefore built as ONE 80-byte line in UI80BUF and then
; de-interleaved into the two banks by ui80row.
;
; ★ WHY A STAGING BUFFER AND NOT DIRECT STORES. Two reasons, and the second is
; the one that matters. First, it halves the soft-switch traffic: one RAMWRT-on
; window per row instead of one per character. Second, the window's MAIN half
; IS rows 20-23 of the 40-column text page -- the help rows and the prompt row
; -- so a renderer that read the screen while writing it would consume its own
; output. Composing in LC RAM makes the overlap a non-event.
;
; ★ WHAT THE FOUR ROWS SHOW, and why. Four rows and 320 characters have to
; carry what a player needs WITHOUT leaving the board, and everything else has
; to be reachable with one ESC:
;
;   20  the inverse title bar (version, level, which colour you have)  cols 0-39
;       whose move it is, or how the game ended                       cols 41-59
;       CHECK / THINKING...                                           cols 60-79
;   21  the think line: depth, score, the engine's best move          cols 0-39
;       BOOK: <opening name>                                          cols 41-79
;   22  the message row (illegal move, draw offers, game over)        cols 0-39
;       the first help line                                           cols 41-79
;   23  the input prompt, what you have typed, and the cursor         cols 0-39
;       the second help line                                          cols 41-79
;
; ★ COLUMN 40 IS A GUTTER ON EVERY ROW, and on row 20 it is load-bearing. The
; title bar is INVERSE VIDEO for exactly its first 40 columns -- a solid white
; block whose last character is an inverse space -- so a status field starting
; at column 40 put "WHITE TO MOVE" hard against that edge, and on a real IIe
; the row read as YOU ARE WHITEWHITE TO MOVE (reported 2026-08-01). The gutter
; must be NORMAL video: an inverse space there is another white cell, which is
; not a separator but more bar. See WIN80R.
;
; The MOVE LIST, the rank/file coordinates and the long-form help are what got
; left out; all three are on the 40-column screen, one ESC away. Every one of
; these fields is COPIED from the 40-column row that already renders it, so
; there is exactly one piece of code per string and the two screens cannot
; disagree.
; ===========================================================================

DHTXTTOP  = 20          ; first screen row of the four-row window (mixed mode
                        ;  shows text rows 20-23 under scanlines 0-159)
WIN80R    = 41          ; the window's RIGHT-HAND COLUMN: every row's second
                        ;  field starts here. Column 40 is deliberately left
                        ;  blank as the gutter -- on row 20 it is the one cell
                        ;  of black that separates the end of the inverse
                        ;  title bar from "WHITE TO MOVE".

; ui80clr: blank all 80 columns of the staging line. Clobbers A,Y.
ui80clr:
        ldy #79
        lda #$A0                ; normal space
u80c:   sta UI80BUF,y
        dey
        bpl u80c
        rts

; ui80get: copy the 40 characters of 40-column text row A into the staging
; line starting at column X, stopping at column 80. Clobbers A,X,Y, SCRPTR
; and UITMPB.
;
; It reads the TEXT PAGE, so its source row must be one the window does not
; overwrite -- i.e. row 0-19, never 20-23. That is not a comment: rows 20-23
; hold the window's own odd columns once it has been painted once, so a
; source row of 21 would render the previous frame's interleave as text.
ui80get:
        stx UITMPB
        ldx #0
        jsr uigotorc            ; SCRPTR = the row base, column 0
        ldy #0
        ldx UITMPB
u80g:   lda (SCRPTR),y
        sta UI80BUF,x
        inx
        cpx #80
        bcs u80gx
        iny
        cpy #40
        bcc u80g
u80gx:  rts

; ui80puts: copy the $00-terminated string at A/X into the staging line
; starting at column Y, stopping at column 80. Clobbers A,X,Y, STRPTR and
; UITMPB.
ui80puts:
        sta STRPTR
        stx STRPTR+1
        sty UITMPB
        ldy #0
        ldx UITMPB
u80p:   lda (STRPTR),y
        beq u80px
        sta UI80BUF,x
        inx
        cpx #80
        bcs u80px
        iny
        bne u80p
u80px:  rts

; ui80row: blit the staging line to 80-column text row A -- even columns to
; AUX, odd columns to MAIN. Clobbers A,X,Y and SCRPTR.
;
; RAMWRT is the only switch involved, and it works here for exactly the reason
; it works in dhboard: uidhon cleared 80STORE, so $0400-$07FF follows
; RAMRD/RAMWRT like any other address. With 80STORE on it would follow PAGE2
; instead and every "aux" byte would land in main, on top of the odd columns.
ui80row:
        ldx #0
        jsr uigotorc            ; SCRPTR = the row base; both banks share it
        sta RAMWRTON            ; --- even columns: aux ---
        ldx #0
        ldy #0
u80ra:  lda UI80BUF,x
        sta (SCRPTR),y
        inx
        inx
        iny
        cpy #40
        bcc u80ra
        sta RAMWRTOFF           ; --- odd columns: main ---
        ldx #1
        ldy #0
u80rm:  lda UI80BUF,x
        sta (SCRPTR),y
        inx
        inx
        iny
        cpy #40
        bcc u80rm
        rts

; ui80stat: rebuild the window's FIRST row -- the title bar, whose move it is,
; and CHECK. Split out because m8engine paints THINKING... onto CHKROW at the
; start of the engine's turn, and on the board screen that row is the only
; place it can appear.
ui80stat:
        jsr ui80clr
        lda #0                  ; the inverse title bar, level digit and all
        ldx #0
        jsr ui80get
        lda #STATROW            ; ...whose move it is, or the result, at the
        ldx #WIN80R             ;  window's RIGHT-HAND COLUMN. ★ NOT column 40:
                                ;  the title bar is 40 columns of INVERSE
                                ;  video whose last character is an inverse
                                ;  SPACE, so a status field at 40 puts
                                ;  "WHITE TO MOVE" hard against the end of a
                                ;  white bar and the row reads as
                                ;  "YOU ARE WHITEWHITE TO MOVE" -- reported
                                ;  from a real IIe, 2026-08-01. Column 41
                                ;  leaves one NORMAL-video cell of black
                                ;  between them, and it is the same column
                                ;  the two help lines start at, so all four
                                ;  window rows share one right-column origin.
        jsr ui80get
        lda #CHKROW             ; ...and CHECK / THINKING..., over its spaces
        ldx #60
        jsr ui80get
        lda #DHTXTTOP
        jmp ui80row

; ui80think: rebuild the window's SECOND row -- the think line and the opening
; name. Split out because the search calls it between iterations (uithinkln),
; which is the only thing that repaints while the engine is on move.
ui80think:
        jsr ui80clr
        lda #THINKROW
        ldx #0
        jsr ui80get
        lda #BOOKROW
        ldx #WIN80R             ; the right-hand column, like every other row
        jsr ui80get
        lda #DHTXTTOP+1
        jmp ui80row

; uidhtext: repaint all four rows of the window. Called after every full
; repaint (uipaint) and after every swap back to the board (uiswap).
uidhtext:
        jsr ui80stat
        jsr ui80think
        jsr ui80msg
        jmp uiprompt            ; row 23 paints itself into both screens

; ui80msg: rebuild the window's THIRD row -- the message row and the first
; help line. Split out for the same reason ui80stat and ui80think are: a
; caller that changes only the message (cmd_level, uiaskpromo, both through
; uipaintmsg) must be able to repaint it without a 217,000-cycle full repaint.
ui80msg:
        jsr ui80clr
        lda #MSGROW
        ldx #0
        jsr ui80get
        lda #<s_help1
        ldx #>s_help1
        ldy #WIN80R
        jsr ui80puts
        lda #DHTXTTOP+2
        jmp ui80row

; uisetmsg / uiclrmsg: the message row's contents live in LC RAM so a full
; repaint restores them.
uisetmsg:
        sta STRPTR
        stx STRPTR+1
        ldy #0
usml:   lda (STRPTR),y
        sta UIMSGB,y
        beq usmx
        iny
        cpy #39
        bcc usml
        lda #0
        sta UIMSGB,y
usmx:   rts

uiclrmsg:
        lda #0
        sta UIMSGB
        rts

; ---------------------------------------------------------------------------
; uibookname: CUROPENING -> "BOOK: <name>" in the opening row's buffer. The
; name table is a run of length-prefixed strings; NAMEID indexes it by
; position, so the walk is the decode.
;
; ★ IT READS TWO DIFFERENT MEMORIES, and that is the whole point of the split.
; The HEADER (magic, and the name table's address) is in AUXILIARY RAM with the
; entries; the NAME TABLE itself is in LANGUAGE CARD BANK 2. So this routine
; opens an aux window, reads six bytes, closes it, opens a bank-2 window,
; walks the table, and closes that.
;
; ★ WHY THIS ROUTINE MAY BANK, AND NOTHING ELSE MAY. It runs at $E000. Language
; Card bank switching only re-maps $D000-$DFFF -- $E000-$FFFF is common to both
; banks -- so its own instruction fetches, its zero page (which follows ALTZP,
; not the LC bank), its stack and every byte it writes (UIBOOKB and UITMPB, at
; $F7xx) are unaffected. asm/book.s cannot do the equivalent trick with RAMRD,
; because it is fetched from main $4000; that asymmetry is why the probe pays
; for a $D000 primitive and this does not.
;
; ★ WHAT IS HIDDEN WHILE BANK 2 IS IN, and therefore what must not happen:
; LCCODE at $D000 (ttfetch, bkfetch, bkhdr -- so no transposition-table probe
; and no book probe) and DHTILES at $D300 (so no board repaint). The window
; below is straight-line code with no jsr in it, and it restores bank 1 on
; BOTH exits. internal/ui's TestBookNameRestoresBank1 fails if either path
; stops doing so.
;
; ★ $C083 AND NOT $C080. Bank 2 with WRITE ENABLED, because write-protecting
; the Language Card protects ALL of $D000-$FFFF -- including $F780, where this
; routine's output goes. Write-protecting here would not read the wrong name;
; it would silently write no name at all.
; ---------------------------------------------------------------------------
uibookname:
        sta RAMRDON             ; ---- the HEADER, from AUX ----
        lda BOOK_MAGIC
        cmp #'B'
        bne ubnnone
        lda BOOK_MAGIC+1
        cmp #'K'
        bne ubnnone
        lda BOOK_NAMEADDR       ; the name table's ABSOLUTE resident address.
        sta T0                  ;  Read from the header rather than assembled
        lda BOOK_NAMEADDR+1     ;  in, so moving the table is a cmd/genbook
        sta T0+1                ;  change and nothing else.
        sta RAMRDOFF            ; ---- and out of AUX before anything else ----
        lda LCBANK2RW           ; ---- the NAME TABLE, from LC BANK 2 ----
        lda LCBANK2RW           ;  (two reads: that is what enables writing)
        ldx CUROPENING
        beq ubngot
ubnskip:
        ldy #0
        lda (T0),y
        sec                     ; += length + 1
        adc T0
        sta T0
        bcc :+
        inc T0+1
:       dex
        bne ubnskip
ubngot: ldx #0
ubnpfx: lda s_bookpfx,x
        beq ubnbody
        sta UIBOOKB,x
        inx
        bne ubnpfx
ubnbody:
        ldy #0
        lda (T0),y
        sta UITMPB
ubncl:  iny
        cpy UITMPB
        beq :+
        bcs ubnterm
:       lda (T0),y
        ora #$80
        sta UIBOOKB,x
        inx
        cpx #39
        bcc ubncl
ubnterm:
        lda LCBANK1RW           ; bank 1 back, BEFORE anything can want LCCODE
        lda LCBANK1RW           ;  or DHTILES again (X survives: it is the
                                ;  write cursor for the terminator below)
        lda #0
        sta UIBOOKB,x
        rts
ubnnone:                        ; no book: aux is still switched in, bank 1 was
        sta RAMRDOFF            ;  never switched away from
        lda #0
        sta UIBOOKB
        rts

; ---------------------------------------------------------------------------
; uithinkln: rebuild the think line and paint it. Called between completed
; iterative-deepening iterations, from the UI's own driver — zero lines of
; search.s change, and the numbers shown (depth, score, best move) are EXACT
; rather than the estimator's ~27%-RMS guess at a per-move time.
; ---------------------------------------------------------------------------
uithinkln:
        lda #<UITHINK
        sta SCRPTR
        lda #>UITHINK
        sta SCRPTR+1
        ldy #0
        lda #'D'|$80
        sta (SCRPTR),y
        iny
        lda CURDEPTH
        jsr uidec2
        lda #$A0
        sta (SCRPTR),y
        iny
        jsr uiscore
        lda #$A0
        sta (SCRPTR),y
        iny
        lda BESTFROM
        cmp #NOSQ
        beq utlterm
        jsr uisqout
        lda BESTTO
        jsr uisqout
utlterm:
        lda #0
        sta (SCRPTR),y
        lda #THINKROW
        ldx #0
        jsr uigotorc
        lda #<UITHINK
        ldx #>UITHINK
        jsr uiputs
        lda UIDHGR              ; in mixed mode the same line is the LEFT half
        beq utlx                ;  of the window's second row, and a search is
        jmp ui80think           ;  the only thing that repaints between moves
utlx:   rts

; uiscore: SCORE (16-bit signed centipawns, side-to-move POV) -> a signed
; pawn figure at (SCRPTR),y.
uiscore:
        lda SCORE+1
        bpl uscpos
        sec
        lda #0
        sbc SCORE
        sta UISCR0
        lda #0
        sbc SCORE+1
        sta UISCR1
        lda #'-'|$80
        bne uscsgn
uscpos: lda SCORE
        sta UISCR0
        lda SCORE+1
        sta UISCR1
        lda #'+'|$80
uscsgn: sta (SCRPTR),y
        iny
        lda UISCR1
        cmp #MATEZONEHI
        bcc uscnum
        ldx #0
uscml:  lda s_mate,x
        beq uscmx
        sta (SCRPTR),y
        iny
        inx
        bne uscml
uscmx:  rts

; uscnum: UISCR1:UISCR0 (0..32767 centipawns) -> "d.dd" / "dd.dd" / "ddd.dd".
uscnum: lda #0
        sta UIQ0
        sta UIQ1
usd100: lda UISCR0              ; divide by 100 by repeated subtraction: at
        cmp #100                ;  most ~320 iterations, once per iteration
        lda UISCR1              ;  of a search that costs millions of cycles
        sbc #0
        bcc usdd
        lda UISCR0
        sec
        sbc #100
        sta UISCR0
        lda UISCR1
        sbc #0
        sta UISCR1
        inc UIQ0
        bne usd100
        inc UIQ1
        jmp usd100
usdd:   ldx #0                  ; hundreds of the integer part
usdh:   lda UIQ0
        cmp #100
        lda UIQ1
        sbc #0
        bcc usdhd
        lda UIQ0
        sec
        sbc #100
        sta UIQ0
        lda UIQ1
        sbc #0
        sta UIQ1
        inx
        bne usdh
usdhd:  cpx #0
        beq usdt
        txa
        clc
        adc #'0'|$80
        sta (SCRPTR),y
        iny
        lda UIQ0
        jsr uid2z
        jmp usddot
usdt:   lda UIQ0
        cmp #10
        bcc usdu
        jsr uid2z
        jmp usddot
usdu:   clc
        adc #'0'|$80
        sta (SCRPTR),y
        iny
usddot: lda #'.'|$80
        sta (SCRPTR),y
        iny
        lda UISCR0
        ; uid2z (two digits, leading zero PRINTED — a hundredths field needs
        ; it) lives in ui.s, which the three-column move-number field also
        ; needs it for and which is linked standalone by asm/uitest.s.
        jmp uid2z

; ===========================================================================
; Tables and strings
; ===========================================================================

; The standard starting position as 32 (square, piece byte) pairs, in the
; engine's slot order: slot 0 is each side's king, white slots 0-15 and
; black 16-31, piece byte = index<<4 | colour<<3 | type. The indices match
; chesstest.ParseFEN's FEN-scan assignment exactly, which matters because
; the Zobrist hash — and therefore every opening-book key — is computed from
; these bytes. internal/ui's TestStartPosition asserts the whole image.
STSQ:   .byte $04, $10, $11, $12, $13, $14, $15, $16     ; wK, wPa2..wPh2(1-7)
        .byte $17, $00, $01, $02, $03, $05, $06, $07     ; wPh2, wRa1..wRh1
        .byte $74, $70, $71, $72, $73, $75, $76, $77     ; bK, bRa8..bRh8
        .byte $60, $61, $62, $63, $64, $65, $66, $67     ; bPa7..bPh7
STPC:   .byte $06, $11, $21, $31, $41, $51, $61, $71
        .byte $81, $94, $A2, $B3, $C5, $D3, $E2, $F4
        .byte $0E, $1C, $2A, $3B, $4D, $5B, $6A, $7C
        .byte $89, $99, $A9, $B9, $C9, $D9, $E9, $F9

; Levels. 1-4 are fixed depth (no clock read anywhere — the period-honest
; control, and the only kind an Apple IIe could offer before FT2_SOFTCLK);
; 5-9 are timed, in 256-cycle units at 1.0205 MHz. Nothing below ~4 s is
; offered: one soft-clock poll is 0.45-0.57 s of resolution, so a shorter
; level would be quantisation noise.
;             lvl   1    2    3    4    5      6      7      8       9
;             sec   -    -    -    -    4      8      15     30      60
LVDEPTH: .byte       2,   3,   4,   5,   20,    20,    20,    20,     20
LVB0:    .byte     $00, $00, $00, $00, $49,   $93,   $92,   $24,    $48
LVB1:    .byte     $00, $00, $00, $00, $3E,   $7C,   $E9,   $D3,    $A6
LVB2:    .byte     $00, $00, $00, $00, $00,   $00,   $00,   $01,    $03

; KTAB: the soft-clock margin as a multiplier over 128, indexed by the
; budget's octave (see uimargin for why 128). 101 = 12800/127, 113 =
; 12800/113, 139 = 12800/92; 128 would be "leave it alone".
;
; ★ OCTAVES 15+ WENT 100% -> 92% ON 2026-07-30, and that entry is now
; MEASURED rather than extrapolated. It had been the flat 100% tail no test
; looked at, and on all twenty curated openings the paired probe reads
; soft/exact 0.9206 at 15 s and 0.9035 at 30 s — the shipped LEVEL 7 and
; LEVEL 8 throwing away 8-10% of their allocation. At 92% they read ~0.99,
; which is the same 1-2%-under-parity the 4 s anchor was fitted to.
; See internal/chesstest softMarginPct, the reference this must agree with.
KTAB:   .byte 101, 101, 101, 101, 101, 101, 101, 101
        .byte 101, 101, 101, 101, 101, 101, 113, 139
        .byte 139, 139, 139, 139, 139, 139, 139, 139

promoltr: .byte 'n', 'b', 'r', 'q'

NCMDS = 8
cmdkeys: .byte 'n', 't', 'r', 'd', 'l', 's', 'q', '?'
cmdlo:  .byte <(cmd_new-1),  <(cmd_take-1),  <(cmd_resign-1), <(cmd_draw-1)
        .byte <(cmd_level-1), <(cmd_swap-1), <(cmd_quit-1),   <(cmd_help-1)
cmdhi:  .byte >(cmd_new-1),  >(cmd_take-1),  >(cmd_resign-1), >(cmd_draw-1)
        .byte >(cmd_level-1), >(cmd_swap-1), >(cmd_quit-1),   >(cmd_help-1)

stmlo:  .byte <s_white, <s_black
stmhi:  .byte >s_white, >s_black
reslo:  .byte <s_white, <s_mate2, <s_stale, <s_d50, <s_drep, <s_resign
        .byte <s_agreed, <s_err
reshi:  .byte >s_white, >s_mate2, >s_stale, >s_d50, >s_drep, >s_resign
        .byte >s_agreed, >s_err

; cmd_swap's announcement, indexed by uiwhoidx (0 White, 1 Black, 2 two
; players) -- the same index the title bar's who-field uses.
sidelo: .byte <m_sidew, <m_sideb, <m_side2
sidehi: .byte >m_sidew, >m_sideb, >m_side2

TITLELVL  = 20          ; column of the level digit in s_title
TITLEWHO  = 26          ; column of the 13-character "who plays what" field
PROMPTLEN = 11          ; length of s_movq / s_cmdq

s_title:   INVSTR " 8FISH 1.0    LEVEL 4     YOU ARE WHITE "
s_who:     INVSTR "YOU ARE WHITE"
           INVSTR "YOU ARE BLACK"
           INVSTR " TWO PLAYERS "
WHOOFF:    .byte 0, 14, 28     ; INVSTR's $00 terminator makes the stride 14
s_white:   SCRSTR "WHITE TO MOVE"
s_black:   SCRSTR "BLACK TO MOVE"
s_check:   SCRSTR "CHECK"
s_thinking: SCRSTR "THINKING..."
s_mate:    SCRSTR "MATE"
s_mate2:   SCRSTR "CHECKMATE"
s_stale:   SCRSTR "STALEMATE"
s_d50:     SCRSTR "DRAW: 50 MOVES"
s_drep:    SCRSTR "DRAW: REPETITION"
s_resign:  SCRSTR "RESIGNED"
s_agreed:  SCRSTR "DRAW AGREED"
s_err:     SCRSTR "INTERNAL ERROR"
s_movq:    SCRSTR "YOUR MOVE? "
s_cmdq:    SCRSTR "COMMAND?   "
s_help1:   SCRSTR "N-NEW T-TAKEBACK R-RESIGN D-DRAW"
s_help2:   SCRSTR "L-LEVEL S-SIDES Q-QUIT ?-HELP"
s_bookpfx: SCRSTR "BOOK: "

m_illegal:  SCRSTR "ILLEGAL MOVE - TRY AGAIN"
m_badin:    SCRSTR "TYPE A MOVE LIKE e2e4, OR ONE COMMAND"
m_badcmd:   SCRSTR "UNKNOWN COMMAND - ? FOR HELP"
m_notyours: SCRSTR "NOT YOUR MOVE"
m_over:     SCRSTR "GAME OVER - N STARTS A NEW ONE"
m_promo:    SCRSTR "PROMOTE TO (Q R B N)?"
m_noback:   SCRSTR "NOTHING TO TAKE BACK"
m_level:    SCRSTR "LEVEL? 1-4 PLIES, 5-9 TIMED (4-60 SEC)"
m_drawno:   SCRSTR "DRAW DECLINED"
m_drawn:    SCRSTR "GAME DRAWN"
m_whitewins: SCRSTR "WHITE WINS - N STARTS A NEW GAME"
m_blackwins: SCRSTR "BLACK WINS - N STARTS A NEW GAME"
m_help2:    SCRSTR "MOVES ARE e2e4 / e7e8q. RETURN SENDS."
m_enginebad: SCRSTR "ENGINE RETURNED AN ILLEGAL MOVE"
m_histfull: SCRSTR "MOVE LIST FULL - CANNOT TAKE BACK"
m_needsiie: SCRSTR "8FISH NEEDS A 128K APPLE IIE"
; cmd_swap's three announcements. Each names the mode S just entered AND what
; the next S does, so the three-way cycle is discoverable from any one of its
; states. 39 characters is the message row's limit (uisetmsg).
m_sidew:    SCRSTR "YOU PLAY WHITE. S NEXT: TWO PLAYERS"
m_sideb:    SCRSTR "YOU PLAY BLACK. S NEXT: YOU PLAY WHITE"
m_side2:    SCRSTR "TWO PLAYERS. S NEXT: YOU PLAY BLACK"

; ---------------------------------------------------------------------------
; The double-hi-res board renderer, and the generated tile dispatch tables it
; indexes. LAST in UICODE on purpose: internal/ui's TestUIByteBudget prices
; every section by label deltas between consecutive boundaries, so an include
; dropped into the middle of an existing section silently reassigns that
; section's bytes to it.
;
; The ARTWORK is not here. It is 1,824 B in Language Card bank 1 at DHTILES,
; delivered by stage 2 of the chain load, so the renderer costs this payload
; only its code and its 96-byte index tables.
; ---------------------------------------------------------------------------
        .include "dhgr.s"
        .include "tiles.inc"

; UIDHGRDEF: which screen comes up, and whether ESC has a second one to offer.
; $00 in BOTH links, so m8.bin and m8sd.bin stay byte-identical; the chain
; loader stores $01 here in RAM once it has put the artwork in Language Card
; bank 1. See m8main.
UIDHGRDEF: .byte 0
