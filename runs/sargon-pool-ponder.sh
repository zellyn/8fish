#!/bin/zsh
# VARIED-OPENING gauntlet: our engine (opening BOOK + PONDERING on) vs Sargon
# III, starting each game from an unbalanced opening in tools/openings-pool.epd.
# This is runs/sargon-pool-match.sh's fair-gauntlet with two additions on OUR
# side, both bridge-side Go (no asm/engine.bin change):
#
#   -book         play the resident opening book before searching, and log the
#                 ECO opening name per game (BOOK ... in us-ponder.log).
#   -ponder       self-ponder during Sargon's turn and MEASURE the ponder-hit
#                 rate: after our move we predict Sargon's reply P, ponder the
#                 position root+M+P under an emulated budget equal to Sargon's
#                 per-move think, and on Sargon's actual reply S record HIT
#                 (S==P; the follow-up real search inherits the warm carried TT)
#                 or MISS. Per-move PONDER lines + per-game/session PONDER-*-
#                 SUMMARY lines land in us-ponder.log.
#
# PONDER BUDGET MAPPING: Sargon runs fair mode (Infinite level + CTRL-T at
# BUDGET*MULT 6502 cycles/move), so its think time IS BUDGET*MULT emulated
# cycles. We hand our ponder exactly that free time:
#     -ponder-budget-ms = BUDGET*MULT / 1020   (emulated cycles -> emulated ms).
#
# BOOK CAVEAT: the openings-pool positions start PAST our main-line book (0/40
# are in-book), so with this pool the book almost never fires — pondering is the
# feature actually exercised. To exercise the book in a live match, run from the
# standard start (drop -openings; see the STD block at the bottom, commented).
#
# SARGON PONDERING HANDICAP: Easy Mode (required for reliable headless reads)
# disables Sargon's own pondering; -budget-multiplier MULT approximates it by
# giving Sargon MULT x our per-move cycles. Gauntlet #2 used MULT=1.5.
#
# HONEST RESULT: SARGON-DECLARED-DRAW / SARGON-DRAW-MOVE-PLAYED games in
# sargon-debug.log are real draws — reclassify raw cutechess resign "wins" when
# tallying (grep the debug log). Never trust a raw cutechess win over the audit.
#
# Detached/logged batch (survives tmux window death), e.g.
#   tmux new -s sargonpp 'runs/sargon-pool-ponder.sh 20 30000000 1.5 /tmp/poolpp'
# then poll the log for the SARGON-POOL-PONDER-DONE marker.
#
# Args: [ROUNDS] [BUDGET_CYCLES] [SARGON_MULT] [OUTDIR] [BOOK_SEED]
#   defaults: 20 30000000 1.5 /tmp/sargon-pool-ponder 1
set -e
cd "$(dirname "$0")/.."
REPO="$PWD"
ROUNDS="${1:-20}"          # distinct openings used (<=40); x2 games each
BUDGET="${2:-30000000}"    # OUR Sargon-matched cycles/move
MULT="${3:-1.5}"           # Sargon gets MULT x BUDGET cycles (pondering proxy)
OUT="${4:-/tmp/sargon-pool-ponder}"
BOOK_SEED="${5:-1}"        # book pick seed (per-game variety is added automatically)
mkdir -p "$OUT"
US_BUDGET_MS=$(( BUDGET / 1020 ))                        # our matched emulated-ms budget
SARGON_CYC=$(awk "BEGIN{printf \"%d\", $BUDGET*$MULT}")  # Sargon's per-move cycles
PONDER_MS=$(( SARGON_CYC / 1020 ))                       # ponder = Sargon's think, in emulated ms

echo "=== sargon-pool-ponder: $((ROUNDS*2)) games, BOOK + PONDER on ==="
echo "    our budget=${BUDGET}cyc (${US_BUDGET_MS}ms), Sargon MULT=${MULT} (${SARGON_CYC}cyc)"
echo "    ponder budget=${PONDER_MS}ms (== Sargon's per-move think), book-seed=${BOOK_SEED}"
echo "building binaries + engine.lbl (for the on-device book probe)..."
go build -o "$OUT/us" ./cmd/uci
go build -o "$OUT/sargon-xb" ./cmd/sargon-xboard
# Assemble engine.bin + engine.lbl together from source into $OUT so the book
# probe's entry label matches the binary exactly (does not touch asm/engine.bin).
( cd asm && ca65 -g engine.s -o "$OUT/engine.o" \
    && ld65 -C engine.cfg "$OUT/engine.o" -o "$OUT/engine.bin" -Ln "$OUT/engine.lbl" )

cat > "$OUT/run-us.sh" <<EOF
#!/bin/zsh
exec "$OUT/us" -bin "$OUT/engine.bin" -defs "$REPO/asm/defs.inc" -lbl "$OUT/engine.lbl" \
  -budget ${US_BUDGET_MS} -book -book-seed ${BOOK_SEED} -ponder -ponder-budget-ms ${PONDER_MS} \
  2>>"$OUT/us-ponder.log"
EOF
cat > "$OUT/run-sargon.sh" <<EOF
#!/bin/zsh
exec "$OUT/sargon-xb" -dsk "$REPO/assets/sargon-iii.dsk" -budget-cycles ${BUDGET} -budget-multiplier ${MULT} -debug 2>>"$OUT/sargon-debug.log"
EOF
chmod +x "$OUT/run-us.sh" "$OUT/run-sargon.sh"
: > "$OUT/us-ponder.log"; : > "$OUT/sargon-debug.log"

echo "starting cutechess at $(date)"
"$REPO/tools/cutechess-cli" \
  -engine name=us cmd="$OUT/run-us.sh" proto=uci \
  -engine name=SargonIII cmd="$OUT/run-sargon.sh" proto=xboard restart=on \
  -openings file="$REPO/tools/openings-pool.epd" format=epd order=sequential \
  -repeat \
  -each tc=inf -rounds "$ROUNDS" -games 2 -maxmoves 160 -ratinginterval 1 \
  -resign movecount=5 score=900 -draw movenumber=40 movecount=10 score=20 \
  -pgnout "$OUT/sargon-pool.pgn" || true

echo "cutechess exited at $(date)"
echo "--- ponder session summary (measured hit rate) ---"
grep -E "PONDER-SESSION-SUMMARY|PONDER-GAME-SUMMARY" "$OUT/us-ponder.log" || true
echo "--- book openings played (usually none from this pool) ---"
grep -c "^BOOK " "$OUT/us-ponder.log" || true
echo "--- adapter draw/judgment audit (reclassify these as draws) ---"
grep -Ec "SARGON-DECLARED-DRAW|SARGON-DRAW-MOVE-PLAYED" "$OUT/sargon-debug.log" || true
echo "SARGON-POOL-PONDER-DONE"
