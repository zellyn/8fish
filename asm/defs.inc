; chess6502 core definitions: memory layout and encodings.
; internal/chesstest parses this file directly for its symbol table
; (ParseDefs); the ld65 label file (-Ln) is consulted only for symbols
; not defined here, e.g. gentables-emitted table symbols like ZPLANE0/
; STMKEY/CASTKEYS/EPKEYS/KINDTAB (see hash_test.go). Keep symbols
; exported (no @local) for anything Go needs to poke or read.

; ---- Piece encoding (board byte) ----
; index<<4 | color<<3 | type    (0 = empty square)
; type 1..6 = P N B R Q K; color 0 = white, 8 = black (mask COLORMASK)
; slot in PIECESQ = index + (color ? 16 : 0); king is always index 0.
PAWN    = 1
KNIGHT  = 2
BISHOP  = 3
ROOK    = 4
QUEEN   = 5
KING    = 6
TYPEMASK  = $07
COLORMASK = $08
INDEXMASK = $F0

; ---- Attack table bits (must match cmd/gentables) ----
ATK_KNIGHT = $01
ATK_KING   = $02
ATK_DIAG   = $04
ATK_ORTHO  = $08
ATK_WPAWN  = $10
ATK_BPAWN  = $20

; ---- Move flags ----
; bits 0-2: promotion piece type (0 = none, else N/B/R/Q)
; bit 3: en-passant capture   bit 4: double pawn push   bit 5: castle
FL_PROMO  = $07
FL_EP     = $08
FL_DOUBLE = $10
FL_CASTLE = $20

; Move tier constants (see MOVESTACK): fixed tiers used by the emit
; fast paths instead of a TIERTAB lookup. TIER_QUIET must equal
; TIERTAB[0] (empty victim -> quiet class).
TIER_QUIET = $04                  ; quiet / double push / castle
TIER_EPCAP = (PAWN<<4)|2          ; en passant: pawn victim, light class
TIER_PROMO = $01                  ; promotion: heavy class, no delta filter

; ---- Castling rights bits (in CASTLE) ----
CR_WK = $01
CR_WQ = $02
CR_BK = $04
CR_BQ = $08

NOSQ   = $FF            ; "no square": ep-none, tombstoned piece slot
MAXPLY = 32

; Mate-zone tests on a score's HIGH byte (see MATE below): winning mates
; live in $74xx+ (>= +29696), losing mates in $80xx-$8Bxx (<= -29696).
MATEZONEHI  = $74
NMATEZONEHI = $8C       ; exclusive: hi < $8C && hi >= $80 is a losing mate

; ---- Zero page ----
SIDE      = $00         ; side to move: 0 = white, COLORMASK = black
EPSQ      = $01         ; 0x88 square behind a double push, or NOSQ
CASTLE    = $02         ; CR_* bits
PLY       = $03
ROOTDEPTH = $04         ; perft depth (poked by the test rig)
SENDL     = $05         ; search move loop: list-end lo (PLYENDLO[PLY] mirror)
SENDH     = $06         ; search move loop: list-end hi
HVALID    = $07         ; hash-validity watermark (deep opt r3): HASH0-3
                        ; equals the hash of the current-path position at
                        ; ply HVALID, and HASHSTK*[p] is correct for all
                        ; p < HVALID. make elides hash maintenance for
                        ; quiescence children that provably never consume
                        ; it (no TT probe/store, 50-move clock < 4 so no
                        ; repetition scan, not in check); hashcatchup in
                        ; board.s replays undo records to repair the
                        ; suffix when a consumer appears. unmake restores
                        ; HASH from HASHSTK only when PLY < HVALID, then
                        ; clamps HVALID to PLY. Reset per iteration by
                        ; the driver (engine.s). Exception to the HASH
                        ; invariant: while inside a null-move subtree
                        ; whose null ply v == HVALID (UNDOFROM[v] = NOSQ),
                        ; HASH = position v+1 (makenull applied stm/ep
                        ; itself); hashcatchup steps past the marker.
PCOUNT    = $08         ; 32-bit little-endian perft leaf counter
MSP       = $10         ; 16-bit pointer: top of move stack
FROM      = $12
TO        = $13
MVPIECE   = $14
MVFLAGS   = $15
ATSQ      = $16         ; attacked(): target square
ATSIDE    = $17         ; attacked(): attacking side (0 or COLORMASK)
ATTMP     = $18         ; attacked(): scratch (candidate from-square)
PREVSC0   = $19         ; previous completed iteration's root score,
PREVSC1   = $20         ;  restored with the PREV move on hard abort
GTMP      = $1A         ; movegen scratch
GSLOT     = $1B         ; movegen: current slot
GFROM     = $1C         ; movegen: current from-square
GPIECE    = $1D         ; movegen: current piece byte
GLIMIT    = $1E         ; movegen: slot-walk bound (half base + $10);
                        ;  reuses the old GDIR slot (direction tables
                        ;  are gone since deep opt round 3)
GDELTA    = $1F         ; movegen: current step delta
PROMTYPE  = $21         ; movegen: promotion loop scratch
GTO       = $22         ; movegen scratch target square
ATBITS    = $23         ; attacked(): attack-table bits for current diff
CURPTR    = $24         ; 16-bit: move-iteration pointer. In search this is
                        ;  the LIVE move-loop cursor between recursions
                        ;  (persisted in CURSORLO/HI[PLY] across them);
                        ;  perft and movegen use it as scratch.
DIFF      = $26         ; attacked(): saved difference index
ATDELTA   = $27         ; attacked(): slider step delta
GCOUNT    = $28         ; movegen: offset-loop counter
CRTMP     = $29         ; castle helpers: rook piece byte
ATT78     = $2A         ; attacked(): ATSQ + $78 ($77 + 1 for the
                        ;  carry-clear complement-add diff trick)
VICTIM    = $2B         ; make: captured piece byte
ATP2      = $30         ; attacked() (deep opt r4): 16-bit ZP pointer to
                        ;  RATTACK + $77 - ATSQ, so lda (ATP2),y with
                        ;  y = a piece-list square reads the reversed
                        ;  attack table at diff = ATSQ - from + $77;
                        ;  tombstone squares ($FF) index the table's
                        ;  zero tail and drop out with no special test.
                        ;  $30/$31 were free; set on every attacked entry.
KF1       = $2C         ; search passes 3/4: killer mirrors for this ply
KT1       = $2D         ;  (KILLER1F/1T/2F/2T[PLY]; stable during a node's
KF2       = $2E         ;  move loop, rebuilt by sloopret after recursion)
KT2       = $2F
RECAPSQ   = $0C         ; recap2 qs gate: target square recaptures must hit
RECAPONLY = $0D         ; recap2 qs gate: nonzero -> generateq emits only
                        ;  captures landing on RECAPSQ (prev move's TO)
TTF0      = $0E         ; search pass 0: TT-move from (TTFROMA[PLY] mirror)
TTT0      = $0F         ; search pass 0: TT-move to (TTTOA[PLY] mirror)
FDIRTY    = $33         ; pawn-term per-file cache (deep opt r4 integration):
                        ;  bit f = file f's pawn placement changed since
                        ;  pawntermfull last consumed the mask. Written by
                        ;  make's tail (FT_PSTRUCT-gated: FILEBIT[FROM] |
                        ;  FILEBIT[TO] for pawn movers, FILEBIT[capsq] for a
                        ;  pawn victim under a non-pawn mover); unmake ORs
                        ;  back UNDOFD (this move's make-time value) on the
                        ;  same pawn/slow undo paths; ptbuild seeds $FF.
                        ;  pawntermfull recomputes exactly the files in
                        ;  SPREADTAB[FDIRTY] (neighbor smear: a file's terms
                        ;  read files f-1..f+1), sums the rest from PTFVAL,
                        ;  then clears FDIRTY.
PTFVAL    = $34         ; $34-$3B: per-file pawn-term value cache (signed
                        ;  byte each, white POV; |v| <= 71 = 12+7+52 by
                        ;  construction so 8 bits always suffice). Entry f
                        ;  is valid iff FDIRTY bit f is clear.
CLSP      = $3C         ; movegen class-presence accumulator (deep opt r4
                        ;  integration): every classic emit ORs its tier
                        ;  byte in - bit 0 = a heavy capture/promo was
                        ;  emitted, bit 1 = a light capture (higher bits
                        ;  meaningless; batched quiets skip the OR).
                        ;  Reset by gennodeq/gennodef, latched to
                        ;  CLSPRES[PLY] by gennd2 after generation.
; ---- Resident opening book (docs/book.md; layout in asm/book.inc) ----
; The probe (asm/book.s, entry bookentry) runs standalone at the root before
; any search. These are the only bytes it CLAIMS; its working scratch is
; borrowed search/eval ZP (see book.s), all dead before the first node.
CUROPENING = $3D        ; "which opening am I in": the NAMEID (name-table
                        ;  index) of the last book move the probe played.
                        ;  Free ZP: $3D-$3F were unallocated (CLSP $3C ..
                        ;  BOARD $40). Meaningful only after a book hit.
RUNPTR     = $3E         ; 16-bit ($3E-$3F): the probe's equal-key-run /
                        ;  weighted-pick pointer (ZP for (RUNPTR),y reads).

EVALVALID = $32         ; improving (FT2_IMPROV): nonzero = a static eval was
                        ;  recorded for the CURRENT node's visit (deep opt r4:
                        ;  was a 32-byte per-ply array at $0278; single ZP byte
                        ;  suffices because it is only written at node entry /
                        ;  eval-record and read at sprepj, all strictly before
                        ;  any recursion from that node — the one write-...-
                        ;  recurse-...-read path, the failed null move, now
                        ;  jumps straight to the move loop and never reads it).
                        ;  Reset unconditionally at full-width node init (the
                        ;  write is feature-blind; nothing reads it when
                        ;  FT2_IMPROV is clear, so the tree is unchanged).

BOARD     = $40         ; 128-byte 0x88 board, $40..$BF. The 64 off-board
                        ;  bytes (files 8-F of each rank: $48-4F,58-5F,68-6F,
                        ;  78-7F,88-8F,98-9F,A8-AF,B8-BF) are FREE for scratch,
                        ;  BUT only while every board read is (a) a piece/move-
                        ;  list square, (b) `and #$88`-masked before the read,
                        ;  or (c) a ray that terminates on-board (atwalk/ckwalk
                        ;  in board.s). Any NEW board-neighbor read (king
                        ;  zone/shield), 0..127 scan, or copy-make MUST mask
                        ;  file>=8 first or it will read scratch as board data.

; ---- Search/eval zero page ($C0-$FF) ----
SCORE     = $C0         ; 16-bit: search/eval return value
MGSCORE   = $C2         ; 16-bit signed: middlegame accumulator, white POV
EGSCORE   = $C4         ; 16-bit signed: endgame accumulator, white POV
PHASE     = $C6         ; game phase 0..24+ (N/B=1 R=2 Q=4, both sides)
HALFMOVE  = $C7         ; 50-move-rule halfmove clock
BESTFROM  = $C8         ; root best move
BESTTO    = $C9
BESTFLAGS = $CA
T0        = $CB         ; 16-bit scratch (scmp16 left operand)
T1        = $CC
T2        = $CD         ; 16-bit scratch (scmp16 right operand)
T3        = $CE
MUL0      = $CF         ; 24-bit multiply accumulator
MUL1      = $D0
MUL2      = $D1
PSP0      = $D2         ; 16-bit: PSQT mg table pointer
PSP1      = $D4         ; 16-bit: PSQT eg table pointer
PDIRTY    = $D6         ; nonzero: PSTRUCT is stale for the current
                        ; position. EVERY writer (movepiece/takepiece
                        ; prologues AND addpiece/rempiece) ORs in the
                        ; DIRTYTAB encoding — $81 pawn, $03 king — whose
                        ; bit 0 is a per-make transient "THIS move
                        ; dirtied" flag: make's tail tests it with one
                        ; lsr and strips it (r4 integration). King-only
                        ; fresh dirt (post-lsr == 1) takes the eager
                        ; ptkonly shield delta (reads FROM/TO/MVPIECE,
                        ; valid at make's tail); everything else DEFERS —
                        ; PDIRTY becomes the stale marker $40 (a bit
                        ; DIRTYTAB never sets, so make's tail recovers
                        ; the exact pre-make value for UNDOPD) riding
                        ; through the subtree until an eval's
                        ; FT_PSTRUCT path consumes it via pawntermfull
                        ; (lazy pawnterm: makes whose subtree never evals
                        ; — TT cutoffs, illegal moves — skip the
                        ; recompute entirely). Unmake restores the marker
                        ; from UNDOPD on exactly the paths that restore
                        ; PSTRUCT. Invariant: PDIRTY == 0 => PSTRUCT is
                        ; correct for the current position.
                        ; MAIN $0200-$020F (PWBITS/PBBITS, eval.s) is
                        ; PERSISTENT pawn-file bitmask state: built by
                        ; evalinit (ptbuild), kept current by pbtoggle
                        ; XOR toggles in make/unmake. Nothing else may
                        ; write it. (AUX $0200+ is the TT; unrelated.)
EVTMP     = $D7         ; eval scratch
PSQSQ     = $D8         ; psqt: square (possibly rank-flipped)
PSQPIECE  = $D9         ; psqt: piece byte
MULCNT    = $DA         ; multiply loop counter
MAXDEPTH  = $DB         ; full-width search depth; qs beyond (poked by rig)
; $DC free (was GENCAPS; qs uses the compile-time generateq copy)
HASH0     = $DD         ; 32-bit incremental Zobrist hash
HASH1     = $DE
HASH2     = $DF
HASH3     = $E0
ZPTR      = $E1         ; 16-bit: Zobrist plane pointer
BUDGET0   = $E3         ; 24-bit soft time budget in 256-cycle units,
BUDGET1   = $E4         ;  poked by the rig; 0 = fixed-depth mode
BUDGET2   = $E5
ABORTL0   = $E6         ; hard-abort limit (2x budget), computed at entry
ABORTL1   = $E7
ABORTL2   = $E8
ABORT     = $E9         ; nonzero: unwind the search immediately
NODECNT   = $EA         ; clock-poll divider (poll every 256 nodes)
CURDEPTH  = $EB         ; iterative-deepening iteration depth
MAXCAP    = $EC         ; depth cap (poked MAXDEPTH moves here at entry)
PREVFROM  = $ED         ; previous completed iteration's best move
PREVTO    = $EE
PREVFLAGS = $EF
TTPTR     = $F0         ; 16-bit: aux address of the current TT entry
TTENTRY   = $F2         ; 8-byte staging copy of a TT entry:
                        ;  +0..2 verify (HASH1-3), +3 from, +4 to,
                        ;  +5/6 score LE, +7 depth<<2 | bound
FEATURES  = $FA         ; search feature bits (poked by the rig)
FEATURES2 = $DC         ; second search feature byte (FT2_* bits; poked by
                        ;  the rig, default 0). $DC was the free ZP slot
                        ;  (ex-GENCAPS); nothing else reads or writes it.
REMDEPTH  = $FB         ; scratch: remaining depth at this node
FUTMARG   = $FC         ; scratch: futility margin low byte
FUTMARGH  = T3          ; futility margin high byte (rem2 margin 500 needs
                        ;  16 bits); reuses $CE scmp16 scratch - provably
                        ;  dead in the futility block (no scmp16 call there,
                        ;  and eval clobbers it just before we set it)
SEED      = $FD         ; eval-dither PRNG state; 0 (default) = no dither.
                        ; On hardware this gets seeded from keypress
                        ; timing; the bridge pokes a random byte.
PSTRUCT   = $FE         ; 16-bit signed, white POV: pawn-structure +
                        ; king-shield term, recomputed on pawn/king
                        ; events and added to the tapered eval

FT_NULL   = $01         ; null-move pruning (R=2)
FT_KILLER = $02         ; killer-move ordering
FT_FUTIL  = $04         ; futility + reverse-futility at depth <= 2
FT_PSTRUCT = $08        ; pawn-structure + king-shield eval term
FT_LMR    = $10         ; PVS zero-window scouts + late move reductions
; $20 free: was FT_ROOKX (experimental rook/blockade eval terms, task #51);
;  removed 2026-07-23 after its SPRT lost (-19 +/- 33) to reclaim CODE space
;  for the mop-up + adaptive ports. The extraterm subroutine + RKCNT/XSTRUCT
;  scratch are gone; PWBITS/PBBITS and WBLOCKM/BBLOCKM (shared with pawnterm)
;  are untouched.
FT_ASP    = $40         ; aspiration windows at the iterative-deepening
                        ;  root (budget mode only): after iteration d
                        ;  completes with score s (not a mate, d >= 1),
                        ;  open iteration d+1 with (s-ASPDELTA, s+ASPDELTA)
                        ;  and re-search on a fail-low/high, opening ONLY
                        ;  the failing bound (asymmetric) until the score
                        ;  lands inside the window, a mate appears, or the
                        ;  third fail forces the full window. Root-only
                        ;  cold code (aspiterate, engine.s); zero cost when
                        ;  the bit is clear. Mirrors mirror.aspIterate with
                        ;  AspirationParams{Delta:25, Policy:AspAsym}.
                        ;  (bit was reserved for the canceled LMP port.)
FT_CKVERIFY = $80       ; debug: verify make's gives-check against a
                        ; full scan at every node (exit 101 on mismatch)

; ---- Second feature byte (FEATURES2) ----
FT2_ADAPT  = $04        ; adaptive time/effort management (mirror.SearchTimed
                        ;  port). Budget mode only. The host pokes BUDGET =
                        ;  the per-move base ceiling and CEILMAX = the hard max
                        ;  (min(4*base, income+bank)); the engine RAISES the
                        ;  movable ceiling (mutating BUDGET in place) after each
                        ;  completed ID iteration on the panic (root score
                        ;  dropped >=25cp vs last completed iter -> 4*base) and
                        ;  instability (root best move changed -> 3*base)
                        ;  signals, and STOPS early (easy move) once the best
                        ;  move has been stable for 2 iters past depth 2 with a
                        ;  flat (|drop|<=30cp) score and >= base/4 cycles spent.
                        ;  All raises clamp to CEILMAX; multipliers are eighths
                        ;  (shift+add). adaptive-aggr params (mirror-validated:
                        ;  +54 vs flat / +21 vs banking). Zero tree effect when
                        ;  clear: BUDGET stays the poked constant, ABORTL =
                        ;  2*BUDGET, no policy runs -> byte-identical search.
FT2_IMPROV = $01        ; improving heuristic, full-signal LMR application
                        ;  (mirror ImprovingParams{Mode:full, LMR:true,
                        ;  LMRExtra:1}). "improving" = the side to move's
                        ;  static eval at this ply exceeds its static eval
                        ;  two plies ago. When NOT improving, late-quiet LMR
                        ;  reductions get +1 extra ply. Full-signal: a static
                        ;  eval is guaranteed at every full-width node (forced
                        ;  only where null/RFP computed none) so the ply-2
                        ;  comparison is always available. RFP application NOT
                        ;  ported (measured neutral). Zero tree effect when
                        ;  clear; only the LMR reduction depth changes when set.
FT2_MOPUP = $02         ; phase-gated endgame mop-up eval term (mirror
                        ;  DefaultMopup{Edge:10, Close:4, MatThreshold:450,
                        ;  PhaseMax:6}). White-POV bonus added to SCORE beside
                        ;  PSTRUCT, BEFORE the side-to-move negation.
                        ;  Fires only at PHASE <= 6 (endgame) AND a signed
                        ;  material edge >= 450 (a rook): drives the losing
                        ;  king to a corner (Edge*cornerDist(losingKing),
                        ;  computed inline as cornerdist(file)+cornerdist(rank)
                        ;  since the tight image left no room for a 64-byte
                        ;  table; reproduces the mirror mopupCMD values; max 60)
                        ;  and pulls the winning king in (Close*(14-manhattan
                        ;  (kings)), max 56); combined < a minor, so it breaks
                        ;  the won-endgame shuffling draw without ever trading
                        ;  material. Winning-side material is a single piece-
                        ;  list pass (endgames only, so cheaper than a
                        ;  maintained accumulator that would tax every make);
                        ;  values match mirror mopMatVal (MOPMATLO/HI). Zero
                        ;  cost / byte-identical eval instruction stream when
                        ;  clear (mopupterm never runs). Computed live in eval
                        ;  by mopupterm (CODE tail). A/B via FEATURES2 0x02.

; TT bound codes (in entry byte 7, low 2 bits; 0 = empty entry)
TT_UPPER = 1
TT_LOWER = 2
TT_EXACT = 3

INF       = 32000
MATE      = 30000
TEMPO     = 10          ; side-to-move eval bonus, centipawns
ASPDELTA  = 25          ; FT_ASP initial aspiration half-window (centipawns)

; ---- Absolute data ----
PIECESQ    = $0800      ; 32 bytes: slots 0-15 white (0=king), 16-31 black
PLYBASELO  = $0820      ; per-ply move-stack base pointer
PLYBASEHI  = $0840
PLYENDLO   = $0860      ; per-ply move-stack end (after generation)
PLYENDHI   = $0880
CURSORLO   = $08A0      ; per-ply iteration cursor
CURSORHI   = $08C0

UNDOCAP    = $08E0      ; captured piece byte (0 if none)
UNDOCAPSQ  = $0900      ; captured piece square (differs from TO for ep)
UNDOCASTLE = $0920
UNDOEP     = $0940
UNDOPIECE  = $0960      ; moving piece byte before promotion
UNDOFROM   = $0980
UNDOTO     = $09A0
UNDOFLAGS  = $09C0

; Search per-ply state (32 entries each)
ALPHALO    = $09E0
ALPHAHI    = $0A00
BETALO     = $0A20
BETAHI     = $0A40
UNDOMGLO   = $0A60      ; eval accumulators saved by make
UNDOMGHI   = $0A80
UNDOEGLO   = $0AA0
UNDOEGHI   = $0AC0
UNDOPHASE  = $0AE0
UNDOHALF   = $0B00
LEGALCNT   = $0B20      ; legal moves found at this ply
PASSNO     = $0B40      ; move-loop pass: 0 TT move, 1 heavy captures/
                        ;  promos, 2 light captures, 3 killers, 4 quiets
QSKIND     = $0B60      ; 0 = full-width (or evasion) node, 1 = qs captures-only
HASHSTK0   = $0B80      ; hash of the position at each ply (saved by make;
HASHSTK1   = $0BA0      ;  doubles as the unmake restore source and the
HASHSTK2   = $0BC0      ;  repetition-scan history)
HASHSTK3   = $0BE0
TTFROMA    = $0C00      ; TT move for this ply (NOSQ = none): searched first
TTTOA      = $0C20
TTBF       = $0C40      ; best move found at this ply (for the TT store)
TTBT       = $0C60
RAISED     = $0C80      ; nonzero: alpha was raised at this ply (EXACT bound)
KILLER1F   = $0CA0      ; killer moves: two quiet cutoff moves per ply
KILLER1T   = $0CC0
KILLER2F   = $0CE0
KILLER2T   = $0D00
INCHK      = $0D20      ; nonzero: side to move in check at this ply
FUTILE     = $0D40      ; nonzero: prune quiets at this ply (futility)
THRT       = $0D60      ; qs delta-pruning threshold as a TIER byte
                        ;  (deep opt r4: was the 16-bit DELTATL/DELTATH
                        ;  pair): minvictimtype<<4, where minvictimtype is
                        ;  the smallest victim type whose value clears the
                        ;  16-bit alpha-standpat-margin threshold. The move
                        ;  loop's delta filter is then a single unsigned
                        ;  compare of the move's tier byte against THRT
                        ;  (tier = victimtype<<4|class, class <= 2 here).
                        ;  $00 = everything passes (full-width nodes, low
                        ;  phase, raised stand pat); $70 = nothing passes.
UNDOFD     = $0D80      ; per-ply FDIRTY restore mask (deep opt r4
                        ;  integration; reuses the old DELTATH slot):
                        ;  written by make's FT_PSTRUCT-gated dirty tail
                        ;  (= FDIRTY after this move's file marks),
                        ;  OR-restored into FDIRTY by unmake's pawn-victim/
                        ;  pawn-mover/slow undo paths (the re-toggled files'
                        ;  cached PTFVAL entries go stale again).
UNDOPSL    = $0DA0      ; PSTRUCT saved by make, restored by unmake
UNDOPSH    = $0DC0
SMODE      = $0DE0      ; child window mode for the move being searched:
                        ;  0 full window, 1 zero-window scout (PVS),
                        ;  2/3 scout reduced by 1/2 (LMR)

; $0213-$0221 is free driver-scratch RAM (was FT_ROOKX's RKCNT[16] at
; $0210 and XSTRUCT at $0220, removed 2026-07-23 with the feature).

; ---- Entropy collector (asm/entropy.inc; M8 keyboard/text UI driver) ----
; The IIe has no clock and no RNG, so the eval dither's SEED is MANUFACTURED
; from human keystroke timing: ENTCNT spins in the driver's keyboard-wait
; loop (16 cycles/iteration, so its low byte turns over every ~4ms — far
; below any human's timing jitter) and its low byte is folded into ENTROPY at
; every keypress. ENTROPY's low byte becomes SEED at move time (entseed).
; ENGINE code never reads or writes these three bytes: the collector lives
; entirely in the driver, so the test harness (which pokes SEED directly)
; and the on-device collector cannot collide.
ENTCNT     = $0210      ; 16-bit free-running keyboard-wait loop counter
                        ;  (LE; wraps every ~1.03s of waiting)
ENTROPY    = $0212      ; accumulated entropy byte: ROL-then-EOR fold of
                        ;  ENTCNT's low byte at each keypress. DELIBERATELY
                        ;  never initialized — cold-boot RAM garbage and a
                        ;  warm-restart carryover of the previous session's
                        ;  value are both free entropy.

; Resident-book probe I/O (asm/book.s). Free driver-scratch RAM between
; PWBITS/PBBITS ($0200-$020F) and ITSTART ($0230). Touched only by the book
; probe, which the harness/bridge runs only when a book is loaded.
BOOKRND    = $0222      ; 4 bytes: 32-bit LE random r, poked by the harness
                        ;  (on hardware, derived from SEED / input timing).
                        ;  CONSUMED by the probe (shifted out by r % total).
BOOKHIT    = $0226      ; byte: 1 = a book move was played (BESTFROM/TO/FLAGS
                        ;  + CUROPENING valid), 0 = no book / out of book.

; Driver scratch (budget mode)
ITSTART0   = $0230      ; 24-bit CLOCK snapshot at iteration start
ITSTART1   = $0231      ;  (predictive gate: est next = 2x last cost)
ITSTART2   = $0232

; FT_ASP aspiration state (budget-mode ID root; aspiterate in engine.s).
; Cold once-per-(re)search code, so plain RAM: the live root window bounds
; (ALPHA[0]/BETA[0] get clobbered as alpha is raised during the search, so
; the window is held here across re-searches) and a fail counter.
ASPAL      = $0233      ; 16-bit window alpha (score space)
ASPAH      = $0234
ASPBL      = $0235      ; 16-bit window beta
ASPBH      = $0236
ASPFAIL    = $0237      ; fails this iteration (0..3; 3 forces full window)

; FT2_IMPROV improving-heuristic per-ply eval stack (improving.go's
; evalStack/evalValid). 16-bit static eval per ply, plus a per-ply
; "recorded this visit" flag. Extends the driver-scratch block above
; ($0238-$0297, 96 bytes, MAIN RAM below PIECESQ at $0800 — free, and
; contiguous with ITSTART/ASP). Written only when FT2_IMPROV is set; the
; OFF path never touches these, so the search tree is unchanged.
EVALSTKL   = $0238      ; 32 bytes: static eval low byte, indexed by ply
EVALSTKH   = $0258      ; 32 bytes: static eval high byte, indexed by ply
UNDOPD     = $0298      ; 32 bytes ($0298-$02B7, page-2 tail — free RAM
                        ;  like the rest of the repurposed input-buffer
                        ;  page): per-ply PDIRTY restore value for the
                        ;  lazy-pawnterm deferral (r4 integration).
                        ;  Written by make's dirty tail (0 on the eager
                        ;  ptkonly/feature-off paths, else the deferred
                        ;  stale marker); restored by unmake on exactly
                        ;  the paths that restore PSTRUCT.
CLSPRES    = $0278      ; 32 bytes: per-ply move-list class presence (CLSP
                        ;  latched by gennd2; was the old EVALVALID array
                        ;  slot): the move loop skips the pass 1/2 scans
                        ;  outright when no move of that class was emitted
                        ;  (bit 0 = heavy capture/promo, bit 1 = light
                        ;  capture; conservative - a consumed TT move may
                        ;  leave a set bit with nothing left to find).

; FT2_ADAPT adaptive-effort scratch (page 3, $0300-$030A — free RAM: nothing
; is defined between UNDOPD's tail ($02B7) and PIECESQ ($0800), and the 6502
; stack lives in page 1). CEILMAX/UNSTCEIL/MINSPEND are POKED by the host each
; move (the host already runs the per-game bank, so it does all the ceiling
; arithmetic — no multiply on-device): CEILMAX = panic target = the hard max
; = min(4*BASE, income+bank); UNSTCEIL = instability target = min(3*BASE,
; CEILMAX); MINSPEND = BASE/4 = easy-stop minimum spend. All 24-bit, 256-cycle
; units, matching BUDGET. STABLE is engine-only. Untouched unless FT2_ADAPT set.
CEILMAX0   = $0300      ; 24-bit panic target / hard max ceiling (poked)
CEILMAX1   = $0301
CEILMAX2   = $0302
UNSTCEIL0  = $0303      ; 24-bit instability target = min(3*BASE, CEILMAX) (poked)
UNSTCEIL1  = $0304
UNSTCEIL2  = $0305
MINSPEND0  = $0306      ; 24-bit easy-stop minimum spend = BASE/4 (poked)
MINSPEND1  = $0307
MINSPEND2  = $0308
STABLE     = $0309      ; best-move stability counter (iterations unchanged)
RTARGET0   = $030A      ; 24-bit staged raise target for the shared `raise`
RTARGET1   = $030B
RTARGET2   = $030C

MOVESTACK  = $0E00      ; 4 bytes/move (tier,from,to,flags); through $1FFF
                        ;  (1151-move capacity; emitmove traps overflow with
                        ;  exit 100). tier = victimtype<<4 | class: class
                        ;  1 = heavy capture/promo (pass 1), 2 = light
                        ;  capture (pass 2), 4 = quiet (passes 3/4);
                        ;  $00 = TT move already consumed by pass 0.
MOVESTACKTOP = $2000    ; exclusive end; generator overflow-checks this.
                        ;  Batched quiet emission (deep opt round 4)
                        ;  writes through a Y offset and flushes MSP at
                        ;  ray/handler boundaries, so up to ~124 bytes
                        ;  past this top can be written before the trap
                        ;  fires: $2000-$207F is reserved guard slack
                        ;  and must stay unallocated.

; Transposition table: 4096 x 8 bytes in AUX $0200-$81FF (see D6).
TTBASE     = $0200

; ---- Keyboard (real hardware; M8 driver + entropy collector) ----
; The M8 UI drives these directly: monitor ROM is unmapped while LC RAM is
; read-enabled, so there is no COUT/RDKEY to call.
KBD        = $C000      ; read: bit7 = key waiting, bits 0-6 = ASCII
KBDSTRB    = $C010      ; read: clear the keyboard strobe

; ---- Harness I/O (plain RAM on real hardware; see docs/testing.md) ----
COUT_TRAP   = $BFF0
IN_TRAP     = $BFF1     ; read: next input byte (0 if none)
INSTAT_TRAP = $BFF2     ; read: $80 if input waiting
CLOCK_TRAP  = $BFF4     ; read: cycles/256, 3 bytes LE, latched on low read
EXIT_TRAP   = $BFFF
