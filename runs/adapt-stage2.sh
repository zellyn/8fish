#!/bin/zsh
# Stage 2: stack triages (500 g) then promotions (2000 g = 4 seeds x 250
# pairs) for the triage survivors. Same baseline as adapt-triage.sh.
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

# Stack triages at seed 6502 (comparable to the singles).
run qhcm-f1t8      6502 -aqh 1,8,0,0,0 -aqhcost $QC -acm 1,1 -acmcost 40
run qhttrepl-f1t8  6502 -aqh 1,8,0,0,0 -aqhcost $QC -attrepl 1 -attreplcost 15

# Promotions: 4 fresh seeds each.
for seed in 11111 22222 33333 44444; do
  run P-qhpart-f1-t8 $seed -aqh 1,8,0,0,0 -aqhcost $QC
done
for seed in 11111 22222 33333 44444; do
  run P-qhpart-f2-t16 $seed -aqh 2,16,0,0,0 -aqhcost $QC
done
for seed in 11111 22222 33333 44444; do
  run P-ttrepl $seed -attrepl 1 -attreplcost 15
done
for seed in 11111 22222 33333 44444; do
  run P-qhlmp-f2 $seed -aqh 2,8,0,0,8 -aqhcost $QC -almp 3,3,2,0,0,0
done
echo "=== STAGE2 DONE $(date +%H:%M:%S)" >> "$LOG"
