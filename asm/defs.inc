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

; $32 is free ZP again (was EVALVALID, the FT2_IMPROV "a static eval was
; recorded for this node visit" flag; removed with the feature 2026-07-26).

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
FT_CKEXT  = $40         ; check extensions in the MAIN search (mirror
                        ;  CheckExtParams{MaxExt: 1}: +12.6 +/- 9.0 on the
                        ;  4000-game cycle-budget screen). When the move just
                        ;  made GIVES CHECK (INCHK[child], already computed by
                        ;  make) the child subtree is searched one ply DEEPER
                        ;  (MAXDEPTH+1, balanced restore) instead of one ply
                        ;  shallower - capped at ONE extension per root-to-leaf
                        ;  path by NUMEXT ($0233), incremented/decremented in
                        ;  lockstep with the extension so the path budget is
                        ;  exact (and a scout re-search re-derives the same
                        ;  decision). Never in quiescence: QSKIND[parent] gates
                        ;  it, so a qs capture node's children never extend,
                        ;  while an in-check EVASION node past the horizon is a
                        ;  full-width node in both engines and DOES extend -
                        ;  exactly mirror.checkExt's qsKind test. Never together
                        ;  with an LMR reduction: a checking move is already
                        ;  excluded from reduction by the smset gate's INCHK+1
                        ;  test, so extension and reduction are mutually
                        ;  exclusive by construction. Zero tree effect when
                        ;  clear (the gate costs one INCHK load per child).
; ($40 was FT_ASP, aspiration windows at the ID root. REJECTED by
;  its own asm SPRT (-21 +/- 32, 2026-07-21) and gated off ever since;
;  removed 2026-07-25 in the space-optimization pass (SPACE round 1) to
;  reclaim CODE for the pending check-extension/endgame-technique ports.
;  The wrapper (aspiterate/aspbump/aspfull/itsearch) and its RAM state
;  (ASPAL/AH/BL/BH/ASPFAIL at $0233-$0237) are gone; the budget-mode ID
;  driver calls the plain `iterate` path again, which is byte-for-byte the
;  FT_ASP-off tree. Re-porting is a mirror-verified exercise if the
;  verdict ever flips (mirror.aspIterate is untouched).
FT_CKVERIFY = $80       ; debug: verify make's gives-check against a full
                        ; scan at every node (exit 101 on mismatch). The
                        ; verify code itself is ASSEMBLY-TIME optional: the
                        ; shipped image omits it entirely and only a
                        ; `ca65 -D CKVERIFY` variant build (see
                        ; internal/asmbuild.BuildVariant, same pattern as
                        ; PTNOCACHE) contains it. The bit is still reserved
                        ; so the variant's FEATURES encoding is unchanged.

; ---- Second feature byte (FEATURES2) ----
FT2_GENDEFER = $10      ; DEFERRED full-width move generation at snode: when
                        ;  the TT offers a move for this ply, validate it with
                        ;  ttmovevalid (search.s) and search it as a staged
                        ;  one-move list WITHOUT generating; generate the real
                        ;  list only if it fails to cut (~86% of TT-available
                        ;  nodes cut on it, ~19% of all full-width nodes skip
                        ;  generation entirely). TREE-IDENTICAL by construction:
                        ;  ttmovevalid rejects promotions and castles (pass 0
                        ;  searches all four promo variants in list order
                        ;  N,B,R,Q, so replaying one would change the tree), and
                        ;  the deferred node re-enters the normal passes through
                        ;  the same p0done with the TT move's tier zeroed - the
                        ;  same "consumed" marking pass 0 writes today. The
                        ;  staged record lives at PLYBASE[PLY] so setmove4, the
                        ;  scout refetch and the killer flag read are unchanged.
                        ;  PASSNO[PLY] = PASS_TTDEF while it is live.
                        ;  ttmovevalid must never be MORE permissive than
                        ;  generate: tt.s indexes on 12 bits and verifies 24, so
                        ;  a hit against a different position survives with
                        ;  p = 2^-20 and today's eager generation is the only
                        ;  thing that catches it (see docs/results.md
                        ;  2026-07-27). TestTTMoveValidExhaustive gates that
                        ;  over all 128x128 (from,to) pairs per corpus position;
                        ;  the `ca65 -D GDVERIFY` variant traps (exit 102) on a
                        ;  live disagreement.
FT2_SOFTCLK = $20       ; ESTIMATED elapsed-cycle clock — the feature that
                        ;  lets budget mode (and therefore FT2_ADAPT and
                        ;  timed levels) run on real hardware, where
                        ;  CLOCK_TRAP is plain RAM and nothing on an Apple
                        ;  IIe reads back a clock. checkclock's poll (once
                        ;  per 128 nodes) accumulates 128 x cost(PHASE) into
                        ;  CLOCK_TRAP itself, so every existing clock READER
                        ;  is unchanged; see the checkclocks comment in
                        ;  search.s and docs/results.md for the measured
                        ;  error distribution. Zero cost and zero tree effect
                        ;  when clear: engine.s patches search's `ccsite: jsr
                        ;  checkclock` operand to `checkclocks` only when the
                        ;  bit is set, so an OFF run executes the identical
                        ;  instruction stream. ON, the estimate REPLACES the
                        ;  harness clock, so a harness run that wants the real
                        ;  counter must leave this bit clear (and a run that
                        ;  wants hardware semantics must also disable the
                        ;  harness's own $BFF4 read trap — TrapMemory
                        ;  ClockAddr = 0 — or the trap's answer wins).
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
; $01 is RESERVED, not implemented: it was FT2_IMPROV, the improving
; heuristic (full-signal LMR application, mirror ImprovingParams{Mode:full,
; LMR:true, LMRExtra:1}). Adopted 2026-07-21, RETRACTED 2026-07-22 at
; -1.8 +/- 8.6 over 4200 games, then removed from the image entirely
; (feature audit, 2026-07-26): the asm cost 101 B plus a per-full-width-node
; gate walk even when clear. mirror.ImprovingParams and mirror's own tests
; are untouched, so a re-port stays a mirror-verified exercise.

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

; $08 is RESERVED, not implemented: it was FT2_EGTECH, the endgame TECHNIQUE
; eval terms (mirror endgame.go DefaultEndgame: KingCent 8, KingPawn 6,
; Pass{0,0,10,20,40,60,100,0}, PassKingOur 6, PassKingThem 4, KingAhead 15,
; PHASE <= 6). Ported 2026-07-25; its own asm SPRT measured -9 +/- 24 over
; 600 games (neutral) so it shipped gated OFF, and it was removed from the
; image in the feature audit (2026-07-26): 727 B of TABLES for a term that
; never ran. internal/mirror/endgame.go and its mirror-side tests are
; untouched (they document the real conversion finding: draws 96 -> 57 in
; mirror self-play, which asm self-play cannot price), so a re-port stays a
; mirror-verified exercise. Removed symbols: endterms/egterm/egpass/egadd/
; egadds/egcheb/EGKPDF + EGPASS/EGCD8/RWIN tables + $0213-$0218 state.

; TT bound codes (in entry byte 7, low 2 bits; 0 = empty entry)
TT_UPPER = 1
TT_LOWER = 2
TT_EXACT = 3

INF       = 32000
MATE      = 30000
TEMPO     = 10          ; side-to-move eval bonus, centipawns

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
                        ;  promos, 2 light captures, 3 killers, 4 quiets,
                        ;  PASS_TTDEF = the FT2_GENDEFER staged TT move
PASS_TTDEF = $80        ; PASSNO value while a deferred TT move is live: the
                        ;  list has NOT been generated yet, so sloopret must
                        ;  generate it before continuing (srdefer). Bit 7 so
                        ;  sloopret's dispatch costs one `bmi` (the pass-4 scan
                        ;  is the hottest, and must not grow a compare).
                        ;  Never 4, so the LMR gate in slegal declines to
                        ;  reduce it - exactly as it declines pass 0 today.
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
; $0210 and XSTRUCT at $0220, removed 2026-07-23 with the feature), less
; $0216-$0217, which the entropy accumulator took 2026-07-29 (see ENTROPY
; below).
; The `ca65 -D GDVERIFY` debug variant (FT2_GENDEFER's live equivalence
; assertion + its exhaustive-sweep entry) borrows the first three bytes;
; the shipped image assembles none of that code and leaves them free.
GDVMSP     = $0213      ; GDVERIFY: MSP saved across the throwaway generate
GDVBASE    = $0215      ; GDVERIFY: ttmvsweep's first from-square
GDVBUF     = $3000      ; GDVERIFY: ttmvsweep's 32x128 result buffer.
                        ;  DELIBERATELY OVERLAPS THE RESIDENT BOOK. It must:
                        ;  the book now spans $2000-$3CEE (7407 B after the
                        ;  2026-07-28 widening) and there is no other 4 KB
                        ;  hole in MAIN, so a GDVERIFY build and a loaded
                        ;  book are MUTUALLY EXCLUSIVE. That is fine -- the
                        ;  sweep is a debug-only entry that probes no book --
                        ;  but it is an invariant, not an accident, and
                        ;  chesstest.newStubMachine asserts it.
                        ;
                        ;  This comment used to justify $3000 as being ABOVE
                        ;  the blob, quoting $2000-$2F19: the size the book
                        ;  had BEFORE it was widened. That made it the third
                        ;  comment in this file to assert a memory-map fact
                        ;  that had quietly stopped being true (see also the
                        ;  deleted $2000-$207F reservation, and the stale
                        ;  $0E00 staging address in docs/ui-design.md). Prefer
                        ;  a test that enforces the invariant over a comment
                        ;  that asserts it.

; ---- Entropy collector (asm/entropy.inc; M8 keyboard/text UI driver) ----
; The IIe has no clock and no RNG, so the eval dither's SEED is MANUFACTURED
; from human keystroke timing: ENTCNT spins in the driver's keyboard-wait
; loop (16 cycles/iteration, so its low byte turns over every ~4ms — far
; below any human's timing jitter) and its low byte is folded into ENTROPY at
; every keypress. ENTROPY's low byte becomes SEED at move time (entseed).
; ENGINE code never reads or writes these four bytes: the collector lives
; entirely in the driver, so the test harness (which pokes SEED directly)
; and the on-device collector cannot collide.
ENTCNT     = $0210      ; 16-bit free-running keyboard-wait loop counter
                        ;  (LE; wraps every ~1.03s of waiting)
                        ; $0212 is free (was the 1-byte ENTROPY). The
                        ;  accumulator needs two CONTIGUOUS bytes for its
                        ;  lsr/ror step and $0213-$0215 are borrowed by the
                        ;  GDVERIFY debug build, so it lives at $0216.
ENTROPY    = $0216      ; 2 bytes (LE): the accumulator. A maximal-period
                        ;  16-bit Galois LFSR state, stepped once per fold
                        ;  with the folded byte EORed into the low half; the
                        ;  low byte becomes SEED (entseed). WIDENED from one
                        ;  byte 2026-07-29: the old 8-bit ROL-then-EOR fold
                        ;  had a state space of only 256, so a QUANTIZED
                        ;  (constant-delta) arrival stream drove it round a
                        ;  cycle of at most 16 states and the engine replayed
                        ;  the same games. See asm/entropy.inc for the
                        ;  measurement. DELIBERATELY never initialized —
                        ;  cold-boot RAM garbage and a warm-restart carryover
                        ;  of the previous session's value are both free
                        ;  entropy.

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

NUMEXT     = $0233      ; FT_CKEXT: check extensions applied along the CURRENT
                        ;  root-to-leaf path (mirror Engine.numExt). Reset to 0
                        ;  per ID iteration (iterate); incremented before an
                        ;  extended child search and decremented after, so it
                        ;  always equals the live path's extension count and the
                        ;  MaxExt=1 cap is exactly "NUMEXT != 0 -> no extension".
; $0234-$0237 is free driver-scratch RAM (was FT_ASP's ASPAL/AH/BL/BH +
; ASPFAIL aspiration-window state, removed 2026-07-25 with the feature).

; $0238-$0277 is free RAM again (was FT2_IMPROV's EVALSTKL/EVALSTKH, the
; 2x32-byte per-ply static-eval stack; removed with the feature 2026-07-26).

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
                        ;  fires.
                        ;
                        ;  $2000-$207F IS NOT RESERVED. It used to be
                        ;  described here as guard slack that "must stay
                        ;  unallocated", which was never true (the
                        ;  resident opening book has always been based at
                        ;  $2000 — BOOK_BASE in asm/book.inc) and would
                        ;  not have helped if it were:
                        ;
                        ;   - Under the harness, the store to EXIT_TRAP
                        ;     ends the run on that very instruction, so
                        ;     the <=124 bytes already written ARE the
                        ;     whole blast radius and nothing afterwards
                        ;     can observe them. Slack protects nothing.
                        ;   - On real hardware $BFFF is plain RAM, so the
                        ;     trap is a no-op: the generator RETURNS and
                        ;     keeps emitting past $2000 for the rest of
                        ;     that ply and every deeper one. The overrun
                        ;     is unbounded and 128 bytes cannot contain
                        ;     it. Slack protects nothing here either.
                        ;
                        ;  What actually keeps the book safe is distance:
                        ;  overflow needs ~1151 moves stacked across the
                        ;  live recursion, and the measured worst case
                        ;  (TestMoveStackWatermark, per-instruction
                        ;  sampling of MSP over tactical, max-mobility
                        ;  and deep-endgame searches) is 487 slots —
                        ;  under half. That test is the enforcement
                        ;  mechanism and FAILS if a future change drives
                        ;  the peak past 50% of capacity; do not replace
                        ;  it with a comment. Note also that the exposed
                        ;  window is fixed at $2000-$207F by this
                        ;  constant, so it does NOT grow when the book
                        ;  grows.

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
CLOCK_TRAP  = $BFF4     ; read: cycles/256, 3 bytes LE, latched on low read.
                        ;  On real hardware this is ordinary RAM and, with
                        ;  FT2_SOFTCLK set, it is the 24-bit ESTIMATED-cycle
                        ;  accumulator checkclocks maintains (search.s).
                        ;  engine.s zeroes it at entry in that mode; every
                        ;  reader is unchanged either way.
EXIT_TRAP   = $BFFF

; ---- FT2_SOFTCLK per-node cost model (search.s checkclocks) ----
; Estimated cost of ONE search node, in 6502 cycles: a two-segment function
; of the taper phase, times a deliberate safety margin.
;
;   raw(phase) = (SOFTA + SOFTB*min(phase,SOFTK) + SOFTC*max(phase-SOFTK,0))
;                / SOFTSCALE
;   cost(phase) = raw(phase) * SOFTMARGIN / 100
;
; SOFTSCALE exists only so the slopes can carry a fraction with
; assembly-time integer arithmetic; it is 8 rather than 16 so SOFTA stays
; inside the 16 bits chesstest.ParseDefs reads. The regressor is still the
; taper phase — the only cost signal the engine maintains for free — and the
; table is still built at assembly time and read with one indexed fetch, so
; neither the knee nor the margin costs anything at runtime.
;
; ★ REFIT 2026-07-27 UNDER GAME CONDITIONS. The first fit was taken over 23
; hand-built calibration positions at FIXED DEPTH and gated by a 284-search
; pool of opening starts. Both are quiet, near-balanced, mostly
; full-material positions; both said the estimator ran 5% HIGH. In actual
; self-play the same estimator ran 12% LOW and the engine overran its clock
; by 17-19% — the two measurements disagreed in SIGN (docs/results.md
; 2026-07-27). The cause is neither the transposition table (a cold-TT
; control moves the ratio 1.5%) nor the halfmove clock (1%) nor the phase
; MIX: at MATCHED phase, a position the engine actually reaches in play
; costs ~18% more per node than a pool opening, because it makes ~1.58 moves
; per node against the pool's ~1.21 — check evasions and illegal-move
; rejections that the phase term cannot see.
;
; So SOFTA/SOFTB/SOFTK are fit against MOVES FROM REAL GAMES, played through
; the SPRT match loop with a warm TT and the real per-move allocation,
; against the exact vector of cost-table lookups each move made, entry prime
; included (cmd/softclkdiag -fit). The straight line was dropped because the
; game-condition cost curve is concave — it climbs steeply to about phase 15
; and is then flat — so one line is ~10% wrong at both ends however drawn.
; Raw fit: 3437.3 + 79.7*min(phase,14) cycles/node, relative RMS 16.5% over
; 3150 game moves drawn from BOTH a 4 s/move and a 15 s/move control, because
; the true per-node cost is ~20% lower in the longer searches (deeper trees
; take far more TT cutoffs, and a TT-cutoff node is counted but nearly free)
; and the phase regressor cannot see which it is looking at.
SOFTSCALE = 8
SOFTA     = 27498       ; 3437.3 cycles/node at phase 0  (raw game fit)
SOFTB     = 637         ;  +79.7 cycles/node per unit of phase, up to SOFTK
SOFTC     = 0           ;  flat above SOFTK: past ~15 the measured curve is
                        ;  level, and an unconstrained fit only reads noise
                        ;  out of it (a falling tail would under-price the
                        ;  full-material middlegames with the biggest trees)
SOFTK     = 14          ; the knee, in taper phase
NPCOST    = 25          ; cost-table entries: phase 0..24 (checkclocks clamps)

; SOFTMARGIN is the ASSEMBLY-TIME scale on the cost table, and it is now 100:
; the table holds the RAW measured cost per node and the safety bias lives on
; the other side of the comparison, in the BUDGET.
;
; WHY THERE IS A BIAS AT ALL. An unbiased clock does not produce unbiased time
; management here, because the estimate is read by a THRESHOLD: idloop starts
; iteration d+1 only if now + 2*cost(d) fits in the budget. Getting that
; comparison wrong in the permissive direction buys a whole extra iteration
; (2-6x the last one); getting it wrong the other way costs only the iteration
; not taken. Measured: the raw fit (in-game estimate/truth 0.99) still overran
; its budget by 12%, taking an extra iteration on 86 of 525 paired positions
; against 16 that took one fewer.
;
; WHY IT IS NOT HERE. Every use of the estimate is `estimate >= limit`, the
; estimate is a plain sum of table entries, and all five limits (BUDGET,
; ABORTL, CEILMAX, UNSTCEIL, MINSPEND) are host-poked once per move. So
;
;       m * raw_estimate >= limit    <=>    raw_estimate >= limit / m
;
; is the same comparison, and dividing the limits costs ZERO engine cycles and
; ZERO engine bytes because it happens at move setup. Folded in here, the
; margin had to be ONE constant for every level length — and it cannot be,
; because the true cost per node is ~20% lower in a 15 s search than in a 4 s
; one (deeper trees take far more TT cutoffs; a cutoff node is counted but
; nearly free). One constant left a 15 s level spending 0.68 of its allocation
; against the exact clock's 0.86. On the budget side the margin can vary with
; the budget, which is the degree of freedom that fixes it.
;
; Keeping the table raw also makes the engine's own `spent` figure an honest
; estimate of true elapsed cycles, so a banked clock on hardware settles in
; real units rather than inflated ones.
;
; THE MARGIN RULE now lives with the code that pokes the limits, and is stated
; for the on-device UI to reimplement, in internal/chesstest SoftClockMargin.
; The margin's SIZE is a function of the estimator's VARIANCE, not its bias: a
; future regressor that cuts the 17% relative RMS (a make counter was priced at
; ~0.29% of runtime and refused) should be able to cut it.
;
; Leave this at 100 unless you want a scale that is genuinely constant across
; every level length; SOFTC/SOFTK above are still the shape of the cost curve.
SOFTMARGIN = 100        ; percent; see above — the bias lives on the BUDGET now
