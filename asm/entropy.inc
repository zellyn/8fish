; ---------------------------------------------------------------------------
; Entropy collector: manufacturing a random SEED on a machine with no clock
; and no RNG (design: zellyn).
;
; The eval dither (eval.s evseed) needs a fresh per-move SEED byte, otherwise
; the engine is fully deterministic and replays identical games from the same
; opening. Under the test harness the HOST pokes SEED. On real hardware there
; is nothing to poke it from: the IIe has no real-time clock, no timer, and no
; hardware RNG. The only genuinely unpredictable quantity available is WHEN
; THE HUMAN PRESSES A KEY - nobody releases a key on a cycle boundary.
;
; So: ENTCNT spins in the driver's keyboard-wait loop, and at every keypress
; its low byte is folded into the ENTROPY accumulator. One poll iteration is
; 16 cycles, so ENTCNT's low byte turns over every 4096 cycles (~4.0ms) and
; the full 16 bits every ~1.03s of waiting - both far below the millisecond-
; to-second jitter of human timing, which makes the low byte uniform over
; 0..255 for any real keypress. Every keystroke folds (a UCI-style move entry
; is 4-5 of them), so a move's seed mixes several independent inter-keystroke
; intervals, and the accumulator is never cleared: entropy only accumulates
; over the session.
;
; USAGE (driver / M8 UI):
;       jsr entkey      ; blocking "read one key", collecting entropy
;       ...             ; (call it for EVERY key the UI reads: menus, move
;                       ;  entry, confirmations - all of it is timing entropy)
;       jsr entseed     ; just before jsr'ing the search: SEED = ENTROPY|nonzero
;   optional, if the driver ever polls the keyboard from a non-blocking
;   context (e.g. a ponder search interrupted by the human's first keystroke):
;       lda NODECNT     ; or CLOCK_TRAP: how far the search got before the
;       jsr entfold     ;  interruption is as unpredictable as the keypress
;
; NOTHING in the engine (search/eval/movegen) touches ENTCNT, ENTROPY, or
; calls into this module: it is driver-only, cold code. The harness's direct
; SEED poke therefore keeps working byte-for-byte unchanged, and a given SEED
; still produces exactly the same search tree - this module only changes where
; the seed COMES FROM.
;
; Assemble with HARNESSKBD defined to read the harness input traps instead of
; the real $C000/$C010 (that is how internal/entropy's tests drive this exact
; code under emulation - see asm/entropytest.s).
; ---------------------------------------------------------------------------

.ifdef HARNESSKBD
ENTKSTAT = INSTAT_TRAP  ; read: bit7 set = key waiting
ENTKDATA = IN_TRAP      ; read: the key (consumes it; no strobe to clear)
.else
ENTKSTAT = KBD
ENTKDATA = KBD
.endif

; entkey: block until a key is available, spinning ENTCNT while waiting, then
; fold ENTCNT's low byte into ENTROPY and return the key in A (bit 7 cleared,
; strobe cleared). Clobbers A only - X and Y survive, so the UI can call it
; from inside its own indexed loops.
entkey: inc ENTCNT              ; 6 |
        bne :+                  ; 3 | 16 cycles per poll iteration
        inc ENTCNT+1            ;   | (21 on the 1-in-256 carry)
:       lda ENTKSTAT            ; 4 |
        bpl entkey              ; 3 |
        lda ENTCNT              ; arrival time: the unpredictable part
        jsr entfold
        lda ENTKDATA            ; the key itself
.ifndef HARNESSKBD
        bit KBDSTRB             ; clear the strobe (hardware only)
.endif
        and #$7F
        rts

; entfold: fold the byte in A into ENTROPY:
;       ENTROPY = ROL(ENTROPY) EOR A
; The rotate makes repeated folds accumulate instead of cancelling (a plain
; EOR of two equal arrival bytes would zero the accumulator), and keeps every
; earlier keystroke's contribution alive in the byte. Clobbers A.
entfold:
        pha
        lda ENTROPY
        asl                     ; C = old bit 7
        adc #0                  ; ...back into bit 0 (ROL through carry)
        sta ENTROPY
        pla
        eor ENTROPY
        sta ENTROPY
        rts

; entseed: install the collected entropy as the engine's eval-dither seed.
; SEED = ENTROPY, forced nonzero because SEED = 0 means "dither off". ENTROPY
; itself is left untouched, so later keypresses keep folding into the same
; accumulator. Clobbers A.
entseed:
        lda ENTROPY
        bne :+
        lda #$5A                ; 1-in-256 fallback (0 would disable dither)
:       sta SEED
        rts
