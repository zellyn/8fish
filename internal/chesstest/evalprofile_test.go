package chesstest

import (
	"fmt"
	"sort"
	"testing"
)

// TestEvalClusterProfile (deep optimization review round 4): measures the
// eval.s cluster's share of total cycles on the ADOPTED gameplay config
// (FEATURES 0x1f + FEATURES2 FT2_IMPROV) and on the baseline test config
// (FEATURES2 0), with a per-label breakdown inside the cluster. Also prints
// the exact-tree fingerprint (search/make/eval counts + score + move) per
// FEN so the same run doubles as the adopted-config identity gate.
//
// Diagnostic (skipped in -short): run explicitly and diff the log.
func TestEvalClusterProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	cstart, cend := labels["addpiece"], labels["TYPEATKTAB"]
	if cstart == 0 || cend == 0 || cend <= cstart {
		t.Fatalf("eval cluster range not found: addpiece=%#x TYPEATKTAB=%#x", cstart, cend)
	}
	searchAddr, makeAddr, evalAddr := labels["search"], labels["make"], labels["eval"]

	for _, ft2 := range []byte{0x01, 0x00} {
		var grandTot, grandClu uint64
		agg := map[string]uint64{}
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			m, err := NewMachine(bin, defs, pos, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			SetFeatures(m, defs, 0x1f)
			SetFeatures2(m, defs, ft2)
			SetBudget(m, defs, 0, 6)
			m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
			p := &Profile{PCCycles: make([]uint64, 65536)}
			var searches, makes, evals uint64
			exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
				c := uint64(cycles)
				p.PCCycles[pc] += c
				p.Total += c
				switch pc {
				case searchAddr:
					searches++
				case makeAddr:
					makes++
				case evalAddr:
					evals++
				}
			})
			if err != nil || !exited || code > 2 {
				t.Fatalf("fen=%q exited=%v code=%d err=%v", fen, exited, code, err)
			}
			var clu uint64
			for pc := int(cstart); pc < int(cend); pc++ {
				clu += p.PCCycles[pc]
			}
			grandTot += p.Total
			grandClu += clu
			for _, r := range p.ByRoutine(labels) {
				agg[r.Name] += r.Cycles
			}
			score := int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
			bf, bt, bfl := m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]]
			t.Logf("ft2=%02x %-24s sc=%6d mv=%s cyc=%10d s=%d mk=%d ev=%d cluster=%d (%.2f%%)",
				ft2, fen[:24], score, MoveUCI(bf, bt, bfl), m.Cycles,
				searches, makes, evals, clu, 100*float64(clu)/float64(p.Total))
		}
		fmt.Printf("FT2=%02x EVAL CLUSTER: %d / %d = %.2f%% of total\n",
			ft2, grandClu, grandTot, 100*float64(grandClu)/float64(grandTot))
		// per-label breakdown, cluster labels only
		type kv struct {
			name string
			c    uint64
		}
		var rows []kv
		for name, c := range agg {
			if a, ok := labels[name]; ok && a >= cstart && a < cend {
				rows = append(rows, kv{name, c})
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].c > rows[j].c })
		for _, r := range rows {
			if r.c*10000 < grandTot {
				continue // < 0.01%
			}
			fmt.Printf("  ft2=%02x %-14s %12d  %5.2f%%\n", ft2, r.name, r.c, 100*float64(r.c)/float64(grandTot))
		}
	}
}
