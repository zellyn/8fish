package chesstest

import (
	"testing"
)

// TestProfileR4 (diagnostic): per-routine cycle shares on the ADOPTED
// gameplay config (FEATURES 0x1F + FT2_IMPROV), budget mode, for the
// round-4 deep optimization review. Run explicitly and read the log.
func TestProfileR4(t *testing.T) {
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
		SetFeatures(m, defs, 0x1F)
		SetFeatures2(m, defs, 0x01)
		SetBudget(m, defs, 30_000_000, 24)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		exited, code, p, err := RunProfiled(m, defs, 400_000_000_000)
		if err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
		// Cluster share: search.s occupies [gennode, ttprobe) in link order.
		lo, hi := labels["gennodeq"], labels["ttprobe"]
		var cluster uint64
		for pc := int(lo); pc < int(hi); pc++ {
			cluster += p.PCCycles[pc]
		}
		t.Logf("%s\n  SEARCH.S CLUSTER [%04x,%04x): %d cycles = %.2f%%\n%s",
			fen[:20], lo, hi, cluster, 100*float64(cluster)/float64(p.Total), p.Report(labels, 60))
		// In-cluster per-label breakdown (named labels only, address-ordered).
		type lab struct {
			addr uint16
			name string
		}
		var labs []lab
		for name, addr := range labels {
			if addr >= lo && addr < hi {
				labs = append(labs, lab{addr, name})
			}
		}
		sortLabs(labs, func(a, b lab) bool { return a.addr < b.addr })
		for i, l := range labs {
			end := int(hi)
			if i+1 < len(labs) {
				end = int(labs[i+1].addr)
			}
			var c uint64
			for pc := int(l.addr); pc < end; pc++ {
				c += p.PCCycles[pc]
			}
			if c > 0 {
				t.Logf("    %04x %-24s %9d %5.2f%%", l.addr, l.name, c, 100*float64(c)/float64(p.Total))
			}
		}
	}
}

func sortLabs[T any](labs []T, less func(a, b T) bool) {
	for i := 1; i < len(labs); i++ {
		for j := i; j > 0 && less(labs[j], labs[j-1]); j-- {
			labs[j], labs[j-1] = labs[j-1], labs[j]
		}
	}
}
