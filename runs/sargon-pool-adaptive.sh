#!/bin/zsh
# FULL-STACK gauntlet: our engine (opening BOOK + PONDERING + ADAPTIVE time
# management + endgame mop-up) vs Sargon III, each game from an unbalanced
# opening in tools/openings-pool.epd. This is runs/sargon-pool-ponder.sh plus
# ONE more bridge-side switch (no asm/engine.bin change):
#
#   -adaptive     run OUR own-move search under a PER-GAME cycle bank + FT2_ADAPT
#                 movable ceiling (the bridge-side twin of cmd/sprt -pergame
#                 -adaptA): each move's income == the operating budget (-budget),
#                 the bank carries forward (cap 8x), and each move pokes the base
#                 ceiling (income + bank/8) plus the adaptive ceilings
#                 (maxCeiling=min(4*income, income+bank), unstTarget=min(3*income,
#                 maxCeiling), minSpend=income/4). The engine spends MORE on hard/
#                 unstable positions and LESS on easy ones at the SAME per-game
#                 total (the bank conserves), so on average own-move total ~=
#                 income*moves. Endgame mop-up rides along inside the search.
#
# Both -book (resident opening book, ECO name per game) and -ponder (self-ponder
# on the predicted position during Sargon's FREE turn, hit-rate measured) stay
# on alongside -adaptive.
#
# TWO SEPARATE BUDGET MODELS (never conflated in the audit):
#   * OUR OWN-MOVE search is ADAPTIVE, drawn from the per-game bank (income ==
#     -budget). This is the budget we audit for adherence.
#   * PONDERING is opponent-time: it spends on the predicted position during
#     Sargon's think, budget = Sargon's per-move think (-ponder-budget-ms). Free
#     time, NOT part of our own-move budget. Logged separately.
#
# TIME-BUDGET AUDIT (the reason this variant exists). The engine logs to stderr
# (captured to us-adaptive.log):
#   TIME-MOVE            game/move/income/alloc/spent/ratio/bank — per own move.
#   TIME-PONDER          game/spent — per ponder interval (opponent-time).
#   TIME-GAME-SUMMARY    total (sum own-move cycles) vs intended (income*moves)
#                        + ratio (~1.0 == bank conserved the budget) + ponder.
#   TIME-SESSION-SUMMARY session own_total vs own_intended + ratio, ponder_total.
# A per-game/session ratio persistently > ~1.05 means we are OVERSPENDING the
# budget we set and is a finding to flag.
#
# BOOK CAVEAT: the openings-pool positions start PAST our main-line book, so with
# this pool the book almost never fires — pondering + adaptive are what get
# exercised. Run from the standard start (drop -openings) to exercise the book.
#
# SARGON HANDICAP: Easy Mode disables Sargon's own pondering; -budget-multiplier
# MULT approximates it by giving Sargon MULT x our per-move cycles (default 1.5).
#
# HONEST RESULT: SARGON-DECLARED-DRAW / SARGON-DRAW-MOVE-PLAYED games in
# sargon-debug.log are real draws — reclassify raw cutechess resign "wins" when
# tallying (grep the debug log). Never trust a raw cutechess win over the audit.
#
# Detached/logged batch (survives tmux window death), e.g.
#   tmux new -s sargonpa 'runs/sargon-pool-adaptive.sh 20 30000000 1.5 /tmp/poolpa'
# then poll the log for the SARGON-POOL-ADAPTIVE-DONE marker.
#
# Optional env overrides (defaults keep the standard main-worktree run intact):
#   CUTECHESS   path to cutechess-cli  (default $REPO/tools/cutechess-cli)
#   SARGON_DSK  path to sargon-iii.dsk (default $REPO/assets/sargon-iii.dsk)
#
# Args: [ROUNDS] [BUDGET_CYCLES] [SARGON_MULT] [OUTDIR] [BOOK_SEED]
#   defaults: 20 30000000 1.5 /tmp/sargon-pool-adaptive 1   (ROUNDS*2 games)
set -e
cd "$(dirname "$0")/.."
REPO="$PWD"
ROUNDS="${1:-20}"          # distinct openings used (<=40); x2 games each
BUDGET="${2:-30000000}"    # OUR per-move income (Sargon-matched cycles/move)
MULT="${3:-1.5}"           # Sargon gets MULT x BUDGET cycles (pondering proxy)
OUT="${4:-/tmp/sargon-pool-adaptive}"
BOOK_SEED="${5:-1}"        # book pick seed (per-game variety added automatically)
CUTECHESS="${CUTECHESS:-$REPO/tools/cutechess-cli}"
SARGON_DSK="${SARGON_DSK:-$REPO/assets/sargon-iii.dsk}"
mkdir -p "$OUT"
US_BUDGET_MS=$(( BUDGET / 1020 ))                        # our income as emulated-ms
SARGON_CYC=$(awk "BEGIN{printf \"%d\", $BUDGET*$MULT}")  # Sargon's per-move cycles
PONDER_MS=$(( SARGON_CYC / 1020 ))                       # ponder = Sargon's think, in emulated ms

echo "=== sargon-pool-adaptive: $((ROUNDS*2)) games, BOOK + PONDER + ADAPTIVE on ==="
echo "    our income=${BUDGET}cyc (${US_BUDGET_MS}ms/move), Sargon MULT=${MULT} (${SARGON_CYC}cyc)"
echo "    ponder budget=${PONDER_MS}ms (== Sargon's per-move think), book-seed=${BOOK_SEED}"
echo "building binaries + engine.lbl (for the on-device book probe)..."
# Our engine (cmd/uci) builds GOWORK=off (module mode; always compiles this
# checkout's bridge). The Sargon adapter (cmd/sargon-xboard) needs the local
# go.work goapple2, so it builds under the ambient go.work.
GOWORK=off go build -o "$OUT/us" ./cmd/uci
go build -o "$OUT/sargon-xb" ./cmd/sargon-xboard
# Assemble engine.bin + engine.lbl together from source into $OUT so the book
# probe's entry label matches the binary exactly (does not touch asm/engine.bin).
( cd asm && ca65 -g engine.s -o "$OUT/engine.o" \
    && ld65 -C engine.cfg "$OUT/engine.o" -o "$OUT/engine.bin" -Ln "$OUT/engine.lbl" )

cat > "$OUT/run-us.sh" <<EOF
#!/bin/zsh
exec "$OUT/us" -bin "$OUT/engine.bin" -defs "$REPO/asm/defs.inc" -lbl "$OUT/engine.lbl" \
  -budget ${US_BUDGET_MS} -adaptive -book -book-seed ${BOOK_SEED} -ponder -ponder-budget-ms ${PONDER_MS} \
  2>>"$OUT/us-adaptive.log"
EOF
cat > "$OUT/run-sargon.sh" <<EOF
#!/bin/zsh
exec "$OUT/sargon-xb" -dsk "$SARGON_DSK" -budget-cycles ${BUDGET} -budget-multiplier ${MULT} -debug 2>>"$OUT/sargon-debug.log"
EOF
chmod +x "$OUT/run-us.sh" "$OUT/run-sargon.sh"
: > "$OUT/us-adaptive.log"; : > "$OUT/sargon-debug.log"

echo "starting cutechess at $(date)"
"$CUTECHESS" \
  -engine name=us cmd="$OUT/run-us.sh" proto=uci \
  -engine name=SargonIII cmd="$OUT/run-sargon.sh" proto=xboard restart=on \
  -openings file="$REPO/tools/openings-pool.epd" format=epd order=sequential \
  -repeat \
  -each tc=inf -rounds "$ROUNDS" -games 2 -maxmoves 160 -ratinginterval 1 \
  -resign movecount=5 score=900 -draw movenumber=40 movecount=10 score=20 \
  -pgnout "$OUT/sargon-pool.pgn" || true

echo "cutechess exited at $(date)"
echo "--- ponder session summary (measured hit rate) ---"
grep -E "PONDER-SESSION-SUMMARY|PONDER-GAME-SUMMARY" "$OUT/us-adaptive.log" || true
echo "--- TIME-BUDGET adherence (own-move total vs intended; ratio ~1.0 == on budget) ---"
grep -E "TIME-SESSION-SUMMARY|TIME-GAME-SUMMARY" "$OUT/us-adaptive.log" || true
echo "--- book openings played (usually none from this pool) ---"
grep -c "^BOOK " "$OUT/us-adaptive.log" || true
echo "--- adapter draw/judgment audit (reclassify these as draws) ---"
grep -Ec "SARGON-DECLARED-DRAW|SARGON-DRAW-MOVE-PLAYED" "$OUT/sargon-debug.log" || true
echo "SARGON-POOL-ADAPTIVE-DONE"
