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
GDIR      = $1E         ; movegen: current direction index
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
PDIRTY    = $D6         ; nonzero: a pawn or king moved; PSTRUCT is stale.
                        ; Deep opt r4: EVERY writer (movepiece/takepiece
                        ; prologues AND addpiece/rempiece) now ORs in the
                        ; DIRTYTAB encoding — $81 pawn, $01 king — so
                        ; PDIRTY==1 means exactly "a king moved, no pawn
                        ; placement changed" (promotions mark bit 7 via
                        ; the pawn's rempiece). pawnterm's king-only fast
                        ; path keys on that; it also reads FROM/TO/
                        ; MVPIECE, valid at make's tail where pawnterm is
                        ; called (evalinit and the PTNOCACHE oracle enter
                        ; at pawntermfull, which skips the dispatch).
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
FT_ROOKX  = $20         ; experimental rook/blockade eval terms (task #51):
                        ;  rook open-file +25 / semi-open +12 (per rook),
                        ;  doubled rooks on a file +15, passed-pawn
                        ;  blockade -18. White-POV, flat (not tapered),
                        ;  added to SCORE beside PSTRUCT. Computed live in
                        ;  eval by extraterm (a piece-list scan + per-file
                        ;  blockade pass); zero cost when the bit is clear
                        ;  (arm A == the current engine byte-for-byte).
                        ;  A/B via sprt -a 0x3F -b 0x1F.
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
                        ;  ($0D80 FREE: the old DELTATH slot.)
UNDOPSL    = $0DA0      ; PSTRUCT saved by make, restored by unmake
UNDOPSH    = $0DC0
SMODE      = $0DE0      ; child window mode for the move being searched:
                        ;  0 full window, 1 zero-window scout (PVS),
                        ;  2/3 scout reduced by 1/2 (LMR)

; FT_ROOKX eval-term state (task #51), MAIN just above PWBITS/PBBITS.
; RKCNT: per-file rook counts, file|colorbit indexed (colorbit = $08,
; like PWBITS/PBBITS), scratch rebuilt on every extraterm call from the
; piece-list scan; drives the doubled-rook bonus. XSTRUCT: the last
; white-POV extra-term value; evalinit writes the ROOT position's value
; here for the parity harness (eval recomputes live into T2/T3 and never
; touches XSTRUCT, so it stays valid at the root after a search).
RKCNT      = $0210      ; 16 bytes ($0210-$021F)
XSTRUCT    = $0220      ; 16-bit signed, white POV

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
                        ; ($0278-$0297 FREE: the old EVALVALID per-ply array
                        ;  moved to a single ZP byte, deep opt r4 — see $30.)

MOVESTACK  = $0E00      ; 4 bytes/move (tier,from,to,flags); through $1FFF
                        ;  (1151-move capacity; emitmove traps overflow with
                        ;  exit 100). tier = victimtype<<4 | class: class
                        ;  1 = heavy capture/promo (pass 1), 2 = light
                        ;  capture (pass 2), 4 = quiet (passes 3/4);
                        ;  $00 = TT move already consumed by pass 0.
MOVESTACKTOP = $2000    ; exclusive end; generator overflow-checks this

; Transposition table: 4096 x 8 bytes in AUX $0200-$81FF (see D6).
TTBASE     = $0200

; ---- Harness I/O (plain RAM on real hardware; see docs/testing.md) ----
COUT_TRAP   = $BFF0
IN_TRAP     = $BFF1     ; read: next input byte (0 if none)
INSTAT_TRAP = $BFF2     ; read: $80 if input waiting
CLOCK_TRAP  = $BFF4     ; read: cycles/256, 3 bytes LE, latched on low read
EXIT_TRAP   = $BFFF
