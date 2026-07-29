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
; BOTH its bytes are folded into the 16-bit ENTROPY accumulator. One poll
; iteration is 16 cycles, so ENTCNT's low byte turns over every 4096 cycles
; (~4.0ms) and the full 16 bits every ~1.03s of waiting - both far below the
; millisecond-to-second jitter of human timing, which makes the low byte
; uniform over 0..255 for any real keypress. Every keystroke folds (a
; UCI-style move entry is 4-5 of them), so a move's seed mixes several
; independent inter-keystroke intervals, and the accumulator is never
; cleared: entropy only accumulates over the session.
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
; fold both ENTCNT bytes into ENTROPY and return the key in A (bit 7 cleared,
; strobe cleared). Clobbers A only - X and Y survive, so the UI can call it
; from inside its own indexed loops.
entkey: inc ENTCNT              ; 6 |
        bne :+                  ; 3 | 16 cycles per poll iteration
        inc ENTCNT+1            ;   | (21 on the 1-in-256 carry)
:       lda ENTKSTAT            ; 4 |
        bpl entkey              ; 3 |
        lda ENTCNT              ; arrival time: the unpredictable part
        jsr entfold
        lda ENTCNT+1            ; ...and the COARSE arrival time. It carries
        jsr entfold             ;  almost no entropy of its own (it only moves
                                ;  every ~4ms), but folding it means a REPEATED
                                ;  input needs the full 16-bit counter to
                                ;  repeat, not just its low byte: the "same
                                ;  arrival every time" degenerate case now
                                ;  needs waits quantized to a 1.03s grid
                                ;  instead of a 4.01ms one. Measured: on the
                                ;  dangerous quantization class it drops the
                                ;  collapse rate 130x (see the note below).
        lda ENTKDATA            ; the key itself
.ifndef HARNESSKBD
        bit KBDSTRB             ; clear the strobe (hardware only)
.endif
        and #$7F
        rts

; entfold: fold the byte in A into the 16-bit accumulator:
;       ENTROPY = LFSR(ENTROPY) EOR A          (A into the LOW half)
; where LFSR is one step of a maximal-period 16-bit Galois LFSR, polynomial
; $B400 = x^16 + x^14 + x^13 + x^11 + 1 (primitive: its orbit is all 65535
; nonzero states). Clobbers A; X and Y survive.
;
; WHY NOT THE OLD ONE-BYTE FOLD (ENTROPY = ROL(ENTROPY) EOR A):
; it worked fine on real keystroke jitter and failed completely on a
; QUANTIZED arrival stream. With a constant arrival delta the fold is a
; deterministic map on 256 states, and because ROL-then-EOR is AFFINE over
; GF(2) the orbits are not merely finite, they are tiny: an exhaustive walk
; of all 256 inputs x all 256 states found NO cycle longer than 16. The
; harness hit this for real -- a machine under gauntlet load quantized its
; waits to a constant 1.254ms, the collector locked onto a 32-long orbit of
; 28 distinct seeds, and the engine started replaying games. Over every
; arrival delta that is a multiple of 8 (one in eight of them) the old fold
; collapsed below 40 distinct seeds per 60 moves 100% of the time.
;
; WHY THIS ONE CANNOT: the state is 16 bits and the step is a primitive
; LFSR, so for a CONSTANT input the map s -> L(s) EOR a is affine with a
; single fixed point and one orbit covering the other 65535 states -- an
; exhaustive walk of all 256 inputs x all 65536 states finds exactly that:
; every cycle is either that lone fixed point or 65535 long. Measured on the
; dangerous quantization class (60 moves, arrival delta a multiple of 8, all
; 8192 deltas x 64 start states): the old fold collapses 100% of the time,
; this one 0.00019% -- a 526000x reduction, and the residue needs the
; accumulator to land exactly on the one degenerate orbit of a 65536-state
; space. It costs 7 bytes over the old fold and one extra byte of RAM.
entfold:
        pha                     ; the byte to fold
        lsr ENTROPY+1           ; --- one Galois LFSR step: shift right,
        ror ENTROPY             ;      C = the bit shifted out...
        bcc :+                  ;      ...and if it was set, apply the taps
        lda ENTROPY+1
        eor #$B4
        sta ENTROPY+1
:       pla                     ; --- then mix the byte into the low half
        eor ENTROPY
        sta ENTROPY
        rts

; entseed: install the collected entropy as the engine's eval-dither seed.
; SEED = ENTROPY's low byte, forced nonzero because SEED = 0 means "dither
; off". Over the LFSR's 65535-state orbit the low byte is uniform over 0..255,
; so no extra mixing is needed here. ENTROPY itself is left untouched, so
; later keypresses keep folding into the same accumulator. Clobbers A.
entseed:
        lda ENTROPY
        bne :+
        lda #$5A                ; 1-in-256 fallback (0 would disable dither)
:       sta SEED
        rts
