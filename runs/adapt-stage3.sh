#!/bin/zsh
# Stage 3: finish the promotions the killed stage-2 runner didn't reach —
# f2-t16 seed 44444, then ttrepl and history-gated LMP (4 seeds each).
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

run P-qhpart-f2-t16 44444 -aqh 2,16,0,0,0 -aqhcost $QC
for seed in 11111 22222 33333 44444; do
  run P-ttrepl $seed -attrepl 1 -attreplcost 15
done
for seed in 11111 22222 33333 44444; do
  run P-qhlmp-f2 $seed -aqh 2,8,0,0,8 -aqhcost $QC -almp 3,3,2,0,0,0
done
echo "=== STAGE3 DONE $(date +%H:%M:%S)" >> "$LOG"
