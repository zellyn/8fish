#!/bin/zsh
# FUDGE-FREE SYMMETRIC gauntlet: our engine ("8fish") vs Sargon III, run by the
# in-process cmd/sargon-symmatch harness (NOT cutechess). Both engines ponder for
# EXACTLY the emulated-cycle time the other consumed — no budget multiplier:
#
#   * 8fish's own move: flat budget B (movetime B).
#   * Sargon's own move: Infinite level + CTRL-T at the SAME budget B (Hard Mode,
#     so Sargon natively ponders on our time). Its measured think = B + a small
#     forced-commit latency.
#   * Sargon's ponder window == 8fish's exact measured think (run cycle-for-cycle).
#   * 8fish's ponder budget == Sargon's exact measured think (the shipped
#     go-ponder path warms the carried TT).
#
# Every full move logs a MOVE line with both pairings so the symmetry is
# auditable (grep '^MOVE' in symmatch.log): 8fish_think == sargon_ponder_window
# by construction, and 8fish_ponder budget == sargon_think.
#
# HARD MODE: Sargon is driven WITHOUT Easy Mode (it ponders), Infinite level
# (true level 9 after the Level() shifted-digit fix). Move acceptance is confirmed
# from the on-screen move-list column (ponder-immune), not the churned RAM piece
# list. Game end (mate/stalemate/threefold/50-move/insufficient) is judged by an
# independent refchess referee, cross-checked against Sargon's screen; adapter-
# style draw reclassification is unnecessary here (we ARE the referee).
#
# Build+run from the MAIN checkout (go.work must see ../goapple2): the adapter and
# the harness need the local goapple2 (WithLazyVideoScan). engine.bin + engine.lbl
# are assembled together into $OUT so the book probe's entry label matches the
# binary exactly (does not touch asm/engine.bin).
#
# Detached/logged batch (survives window death), e.g.
#   nohup runs/sargon-symmatch.sh 40 30000000 /tmp/symmatch >/tmp/symmatch.boot 2>&1 &
# then poll "$OUT/symmatch.log" for SYMMATCH-DONE.
#
# OPENINGS MODE (5th arg):
#   pool     (default) — games cycle through tools/openings-pool.epd, setboard'd
#                        into Sargon. Comparable to the historical 300-game runs,
#                        but those positions start PAST both engines' books, so
#                        neither opening book ever fires.
#   standard          — every game starts from the STANDARD chess start position
#                        with no setboard: 8fish plays its resident 48-line book
#                        and Sargon III plays its own built-in book (nothing
#                        sends CTRL-Y, which would cancel Sargon's book). Book
#                        moves cost ~0 cycles, so the debt-bank banks their
#                        income for the first real search out of book.
#
# Args: [GAMES] [BUDGET_CYCLES] [OUTDIR] [BOOK_SEED] [MODE]
#   defaults: 40 30000000 /tmp/sargon-symmatch 1 pool
set -e
cd "$(dirname "$0")/.."
REPO="$PWD"
GAMES="${1:-40}"
BUDGET="${2:-30000000}"
OUT="${3:-/tmp/sargon-symmatch}"
BOOK_SEED="${4:-1}"
MODE="${5:-pool}"
case "$MODE" in
  pool)     OPENING_ARGS=(-openings "$REPO/tools/openings-pool.epd") ;;
  standard) OPENING_ARGS=(-standard-start) ;;
  *) echo "unknown MODE $MODE (want pool|standard)" >&2; exit 2 ;;
esac
mkdir -p "$OUT"

echo "=== sargon-symmatch: $GAMES games, budget B=${BUDGET}cyc (~$((BUDGET/1020))ms), Hard Mode, symmetric ponder, openings=$MODE ==="
echo "building harness + engine.bin/engine.lbl ..."
# Harness builds under the ambient go.work (needs local goapple2).
go build -o "$OUT/sargon-symmatch" ./cmd/sargon-symmatch
# Assemble engine.bin + engine.lbl together so the book probe's bookentry label
# matches the binary exactly.
( cd asm && ca65 -g engine.s -o "$OUT/engine.o" \
    && ld65 -C engine.cfg "$OUT/engine.o" -o "$OUT/engine.bin" -Ln "$OUT/engine.lbl" )

echo "starting match at $(date)"
# Openings per $MODE (colors alternate each game either way).
"$OUT/sargon-symmatch" \
  -dsk "$REPO/assets/sargon-iii.dsk" \
  -bin "$OUT/engine.bin" -defs "$REPO/asm/defs.inc" -lbl "$OUT/engine.lbl" \
  -budget-cycles "$BUDGET" \
  -book -book-seed "$BOOK_SEED" \
  "${OPENING_ARGS[@]}" \
  -games "$GAMES" -max-moves 160 \
  -out "$OUT" 2>&1 | tee -a "$OUT/symmatch.console"

echo "match exited at $(date)"
echo "--- result ---"
grep -E "SYMMATCH-DONE" "$OUT/symmatch.log" || true
echo "--- own-move budget adherence (own_total vs own_intended, target ~1.0) ---"
grep -E "SYMMATCH-TIME-SESSION-SUMMARY" "$OUT/symmatch.log" || true
echo "--- per-game results ---"
grep -E "GAME [0-9]+ RESULT" "$OUT/symmatch.log" || true
echo "--- opening-book usage (8fish book moves / Sargon instant replies) ---"
grep -E "SYMMATCH-BOOK-SESSION-SUMMARY" "$OUT/symmatch.log" || true
echo "--- distinct 8fish book openings played ---"
grep -E '^BANK .*book=true' "$OUT/symmatch.log" | grep -oE 'opening="[^"]+"' | sort | uniq -c | sort -rn | head -20 || true
echo "--- ponder-hit summary (predicted == actual Sargon reply) ---"
awk '/^MOVE/ && /pred=/{n++; if (/\(HIT\)/) h++} END{if(n>0) printf "ponder hits: %d/%d = %.1f%%\n", h, n, 100.0*h/n}' "$OUT/symmatch.log" || true
echo "SARGON-SYMMATCH-DONE"
