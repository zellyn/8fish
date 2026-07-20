package chesstest

import (
	"strings"
	"testing"
)

// TestRecapSplit: how much of generateq (and QS overall) runs under
// RECAPONLY=1 (recapture-filtered plies) vs full-QS plies? Decides
// whether an attacker-driven recapture generator is worth building.
func TestRecapSplit(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetBudget(m, defs, 30_000_000, 24)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		fullQS := &Profile{PCCycles: make([]uint64, 65536)}
		recapQS := &Profile{PCCycles: make([]uint64, 65536)}
		var fullWidth uint64
		plyAddr, mdAddr := defs["PLY"], defs["MAXDEPTH"]
		recapAddr := defs["RECAPONLY"]
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
			c := uint64(cycles)
			if m.Mem.Main[plyAddr] < m.Mem.Main[mdAddr] {
				fullWidth += c
				return
			}
			p := fullQS
			if m.Mem.Main[recapAddr] != 0 {
				p = recapQS
			}
			p.PCCycles[pc] += c
			p.Total += c
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
		tot := fullWidth + fullQS.Total + recapQS.Total
		t.Logf("%s\n  full-width: %d%%  fullQS: %d%%  recapQS: %d%%", fen[:20],
			100*fullWidth/tot, 100*fullQS.Total/tot, 100*recapQS.Total/tot)
		for name, p := range map[string]*Profile{"FULLQS": fullQS, "RECAPQS": recapQS} {
			var out strings.Builder
			out.WriteString(name + ":")
			for i, r := range p.ByRoutine(labels) {
				if i >= 8 {
					break
				}
				out.WriteString(" " + r.Name + "=" + itoaPct(r.Cycles, tot))
			}
			t.Log(out.String())
		}
	}
}
