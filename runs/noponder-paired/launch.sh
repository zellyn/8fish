#!/bin/bash
# Paired soft-clock control with PONDERING REMOVED FROM BOTH SIDES.
#
# Why: runs/softclock-paired measured the clock with both engines pondering,
# but the SHIPPED DISK NEVER PONDERS (asm/m8.s blocks in uiread/entkey on the
# opponent's turn; docs/plan.md keeps ponder out of M8 on purpose). Ponder is
# ~49% of 8fish's compute in a symmatch game, so NEITHER arm of that gauntlet
# was the artifact a user boots. This run removes ponder as a confound.
#
#   arm "soft" -- FT2_SOFTCLK on. With FT2_ADAPT (set at runtime by uilimits)
#                 this is the DEVICE configuration: what the disk actually runs.
#   arm "off"  -- harness-exact cycle counter ($BFF4 read trap live). Identical
#                 in every other respect. The arm-to-arm delta IS the clock cost.
#
# -noponder is symmetric: 8fish runs no ponder search AND Sargon is not advanced
# during 8fish's think, so Hard Mode gets no ponder window either. Dropping only
# 8fish's ponder would hand Sargon a ~2:1 compute edge and the Elo would measure
# that, not chess.
#
# Pairing: shard k of BOTH arms uses the same -dither-seed, the same budget, the
# same standard-start opening (both engines on their own books), the same game
# count and the same colour alternation.
set -u
cd "$(dirname "$0")/../.." || exit 1
ROOT=$(pwd)
OUT=$ROOT/runs/noponder-paired
GAMES=${GAMES:-42}
BUDGET=${BUDGET:-30000000}
SEEDS=${SEEDS:-"101 102 103 104 105 106"}
JOBS=${JOBS:-8}          # concurrency cap: this box has 8 cores

# Build the engine ONCE up front. Shards racing to assemble it would collide.
make engine >/dev/null || exit 1

running=0
for seed in $SEEDS; do
  for arm in soft off; do
    d=$OUT/$arm-$seed
    mkdir -p "$d"
    flags=""
    [ "$arm" = soft ] && flags="-softclock"
    nohup go run ./cmd/sargon-symmatch \
      -games "$GAMES" -budget-cycles "$BUDGET" \
      -standard-start -book \
      -dither prng -dither-seed "$seed" \
      -noponder $flags -out "$d" > "$d/stdout.log" 2>&1 &
    echo "launched arm=$arm seed=$seed pid=$! -> $d"
    running=$((running + 1))
    if [ "$running" -ge "$JOBS" ]; then
      wait -n 2>/dev/null || wait
      running=$((running - 1))
    fi
  done
done
wait
echo "ALL-SHARDS-DONE"
