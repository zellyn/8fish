#!/bin/bash
# Paired soft-clock control for cmd/sargon-symmatch.
#
# The SAME gauntlet twice, identical in every respect except the clock:
#   arm "off"  -- harness-exact cycle counter ($BFF4 read trap live). This is the
#                 configuration every Sargon number in docs/results.md was measured
#                 through, including the headline +110 Elo.
#   arm "soft" -- FT2_SOFTCLK, trap off: the SHIPPED device configuration
#                 (asm/m8.s sets FEATURES2 = FT2_GENDEFER|FT2_SOFTCLK), because an
#                 Apple IIe has no readable clock.
#
# Pairing: shard k of BOTH arms uses the same -dither-seed, the same budget, the
# same standard-start opening (both engines on their own books), the same game
# count and the same colour alternation. -dither prng (not the shipped entropy
# source) precisely so the seed CAN be pinned across arms; the dither source is
# not what is under test here.
set -u
cd "$(dirname "$0")/../.." || exit 1
ROOT=$(pwd)
OUT=$ROOT/runs/softclock-paired
GAMES=${GAMES:-42}
BUDGET=${BUDGET:-30000000}
SEEDS="101 102 103 104 105 106"

for seed in $SEEDS; do
  for arm in off soft; do
    d=$OUT/$arm-$seed
    mkdir -p "$d"
    flags=""
    [ "$arm" = soft ] && flags="-softclock"
    nohup go run ./cmd/sargon-symmatch \
      -games "$GAMES" -budget-cycles "$BUDGET" \
      -standard-start -book \
      -dither prng -dither-seed "$seed" \
      $flags -out "$d" > "$d/stdout.log" 2>&1 &
    echo "launched arm=$arm seed=$seed pid=$! -> $d"
  done
done
wait
echo "ALL-SHARDS-DONE"
