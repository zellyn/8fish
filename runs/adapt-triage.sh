#!/bin/zsh
# Modern-technique adaptation round: 500-game cycle-budget triage screens.
# Baseline BOTH SIDES = shipped config: mask 0x1f, check extensions
# (maxext 1), recap2 QS, shipped weights/futility, cbudget 143M (the 30
# s-equivalent operating point). A-side adds one candidate + its asm-
# realistic cycle tax. Charges: qhist probe 23 (rescan item + indexed
# load/cmp), update 30, aging 8/byte; IIR 10/full-width node; razor
# 10/test; ttrepl 15/store; nullr unpriced (~5 cyc/null attempt, below
# model resolution).
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

run iir4        -aiir 4 -aiircost 10
run iir4any     -aiir 4,1 -aiircost 10
run iir3        -aiir 3 -aiircost 10
run razor300    -arazor 0,300 -arazorcost 10
run razor400    -arazor 0,400 -arazorcost 10
run razor12     -arazor 200,400 -arazorcost 10
run nullr36     -anullr 3,6
run nullr38     -anullr 3,8
run ttrepl      -attrepl 1 -attreplcost 15
run qhpart-f2-t8   -aqh 2,8,0,0,0 -aqhcost $QC
run qhpart-f2-t16  -aqh 2,16,0,0,0 -aqhcost $QC
run qhpart-f1-t8   -aqh 1,8,0,0,0 -aqhcost $QC
run qhlmr-f2       -aqh 2,0,32,1,0 -aqhcost $QC
run qhpartlmr-f2   -aqh 2,8,32,1,0 -aqhcost $QC
run lmp-ctrl       -almp 3,3,2,0,0,0
run qhlmp-f2       -aqh 2,8,0,0,8 -aqhcost $QC -almp 3,3,2,0,0,0
run qhpart-lmp-noex -aqh 2,8,0,0,0 -aqhcost $QC -almp 3,3,2,0,0,0
echo "=== TRIAGE DONE $(date +%H:%M:%S)" >> "$LOG"
