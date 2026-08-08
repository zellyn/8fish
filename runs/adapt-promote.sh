#!/bin/zsh
# Promotion runs: 2000 games (4 seeds x 250 pairs) for the triage
# survivors. Usage: adapt-promote.sh <name> <extra mirror-match args...>
# Appends per-seed results to runs/adapt-promote.log.
set -e
cd "$(dirname "$0")/.."
LOG="${LOG:-runs/adapt-promote.log}"
name="$1"; shift
for seed in 11111 22222 33333 44444; do
  echo "=== $name seed $seed : $* $(date +%H:%M:%S)" >> "$LOG"
  go run ./cmd/mirror match -workers 7 -cbudget 143000000 \
    -ackext 1 -bckext 1 -pairs 250 -seed $seed "$@" >> "$LOG" 2>&1
done
echo "=== $name DONE $(date +%H:%M:%S)" >> "$LOG"
