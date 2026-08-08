#!/bin/zsh
# Confirm the surprising history-gated-LMP promotion (+35): re-run seed 6502
# for the form-2 combo (the field outlier), and screen the cheaper
# form-1 + LMP combo across all seeds (the real port candidate).
set -e
cd "$(dirname "$0")/.."
LOG="${LOG:-runs/adapt-stage2.log}"
BASE=(-workers 7 -cbudget 143000000 -ackext 1 -bckext 1 -pairs 250)
QC="23,30,8"

run() {
  name="$1"; seed="$2"; shift 2
  echo "=== $name seed $seed : $* $(date +%H:%M:%S)" >> "$LOG"
  go run ./cmd/mirror match "${BASE[@]}" -seed $seed "$@" >> "$LOG" 2>&1
}

run C-qhlmp-f2-6502 6502 -aqh 2,8,0,0,8 -aqhcost $QC -almp 3,3,2,0,0,0
for seed in 6502 11111 22222 33333 44444; do
  run C-qhlmp-f1 $seed -aqh 1,8,0,0,8 -aqhcost $QC -almp 3,3,2,0,0,0
done
echo "=== CONFIRM DONE $(date +%H:%M:%S)" >> "$LOG"
