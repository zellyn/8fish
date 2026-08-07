#!/bin/zsh
# Remainder of the triage (the first runner was killed mid qhpart-f1-t8).
set -e
cd "$(dirname "$0")/.."
LOG="${LOG:-runs/adapt-triage.log}"
BASE=(-workers 7 -cbudget 143000000 -ackext 1 -bckext 1 -pairs 250 -seed 6502)
QC="23,30,8"

run() {
  name="$1"; shift
  echo "=== $name : $* $(date +%H:%M:%S)" >> "$LOG"
  go run ./cmd/mirror match "${BASE[@]}" "$@" >> "$LOG" 2>&1
}

run qhpart-f1-t8    -aqh 1,8,0,0,0 -aqhcost $QC
run qhlmr-f2        -aqh 2,0,32,1,0 -aqhcost $QC
run qhpartlmr-f2    -aqh 2,8,32,1,0 -aqhcost $QC
run lmp-ctrl        -almp 3,3,2,0,0,0
run qhlmp-f2        -aqh 2,8,0,0,8 -aqhcost $QC -almp 3,3,2,0,0,0
run qhpart-lmp-noex -aqh 2,8,0,0,0 -aqhcost $QC -almp 3,3,2,0,0,0
echo "=== TRIAGE DONE $(date +%H:%M:%S)" >> "$LOG"
