package chesstest

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// allLabels reads the ld65 label file preserving duplicate names (the
// generate/generateq body copies reuse local label names; the map in
// ParseLabelFile collapses them, which mis-buckets a per-label profile).
func allLabels(t *testing.T, path string) []struct {
	addr uint16
	name string
} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var labs []struct {
		addr uint16
		name string
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "al" && strings.HasPrefix(f[2], ".") {
			if v, err := strconv.ParseUint(f[1], 16, 32); err == nil && v <= 0xFFFF {
				labs = append(labs, struct {
					addr uint16
					name string
				}{uint16(v), f[2][1:]})
			}
		}
	}
	sort.Slice(labs, func(i, j int) bool { return labs[i].addr < labs[j].addr })
	return labs
}

// TestMovegenClusterProfile: deep optimization review instrument for the
// movegen cluster (generate/generateq/genrecap/emitmove/emitmovef/
// promoloop). Profiles the adopted config (FEATURES 0x1F + FEATURES2
// 0x01) on the standard 2-FEN budget workload and reports the cluster's
// share of total cycles plus a per-label breakdown inside the cluster.
// The cluster is the contiguous code region [emitmove, end-of-movegen),
// bounded below by the first label after the movegen code (aspiterate
// at present; computed from the label file, not hardcoded).
func TestMovegenClusterProfile(t *testing.T) {
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
	lo := labels["emitmove"]
	// upper bound: the movegen cluster is contiguous from emitmove through
	// the end of the generateq proc; find the highest label belonging to
	// the cluster by name prefix, then the smallest label above it.
	isMovegen := func(name string) bool {
		for _, p := range []string{"emit", "emtier", "emfq", "empage", "flushpage", "promoloop",
			"generate", "genloop", "gennext", "gendone", "genpawn", "genbpawn", "genknight",
			"genking", "genbishop", "genrook", "genqueen", "gencastle", "genrecap",
			"gw", "gb", "gc", "gr"} {
			if strings.HasPrefix(name, p) {
				return true
			}
		}
		return false
	}
	last := lo
	for name, addr := range labels {
		if isMovegen(name) && addr > last && addr < 0x8000 {
			last = addr
		}
	}
	hi := uint16(0xFFFF)
	for _, addr := range labels {
		if addr > last && addr < hi {
			hi = addr
		}
	}
	t.Logf("cluster PC range: $%04X..$%04X", lo, hi)
	var grandTotal, grandCluster uint64
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures2(m, defs, 0x01)
		SetBudget(m, defs, 30_000_000, 24)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		p := &Profile{PCCycles: make([]uint64, 65536)}
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
			c := uint64(cycles)
			p.PCCycles[pc] += c
			p.Total += c
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
		var cluster uint64
		for pc := lo; pc < hi; pc++ {
			cluster += p.PCCycles[pc]
		}
		grandTotal += p.Total
		grandCluster += cluster
		t.Logf("%s\n  total=%dM cluster=%dM (%.2f%%)", fen[:20],
			p.Total/1e6, cluster/1e6, 100*float64(cluster)/float64(p.Total))
		// per-label breakdown inside the cluster (duplicates preserved;
		// macro locals folded into the nearest named label above them,
		// with the two body copies distinguished by a /q suffix)
		all := allLabels(t, "../../asm/engine.lbl")
		qbase := labels["generateq"]
		agg := map[string]uint64{}
		var order []string
		cur := ""
		li := 0
		for pc := int(lo); pc < int(hi); pc++ {
			for li < len(all) && int(all[li].addr) <= pc {
				if !strings.HasPrefix(all[li].name, "LOCAL-MACRO") {
					cur = all[li].name
					if all[li].addr >= qbase && all[li].addr < labels["genrecap"] {
						cur += "/q"
					}
				}
				li++
			}
			if c := p.PCCycles[pc]; c != 0 {
				if _, seen := agg[cur]; !seen {
					order = append(order, cur)
				}
				agg[cur] += c
			}
		}
		out := ""
		for _, name := range order {
			if c := agg[name]; c*2000 >= p.Total { // >= 0.05%
				out += fmt.Sprintf(" %s=%.2f%%", name, 100*float64(c)/float64(p.Total))
			}
		}
		t.Log(out)
	}
	t.Logf("CLUSTER SHARE: %.2f%% (%d / %d)", 100*float64(grandCluster)/float64(grandTotal), grandCluster, grandTotal)
}

// TestEmitSites: counts cycles spent at every `jsr emitmove` /
// `jsr emitmovef` call site (the jsr's 6 cycles are attributed to the
// site PC), split by region (full copy / qs copy / genrecap), plus the
// emitmove entry count. Divides by 6 to give call counts per site.
func TestEmitSites(t *testing.T) {
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
	em, emf := labels["emitmove"], labels["emitmovef"]
	gen, genq, grec := labels["generate"], labels["generateq"], labels["genrecap"]
	// locate all jsr sites in the binary image (org $4000)
	org := 0x4000
	type site struct {
		pc     int
		target string
	}
	var sites []site
	for i := 0; i+2 < len(bin); i++ {
		if bin[i] == 0x20 {
			addr := uint16(bin[i+1]) | uint16(bin[i+2])<<8
			if addr == em || addr == emf {
				tgt := "emitmove"
				if addr == emf {
					tgt = "emitmovef"
				}
				sites = append(sites, site{org + i, tgt})
			}
		}
	}
	agg := make([]uint64, len(sites))
	var emCount, emfCount uint64
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures2(m, defs, 0x01)
		SetBudget(m, defs, 30_000_000, 24)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		p := make([]uint64, 65536)
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
			p[pc] += uint64(cycles)
			if pc == em {
				emCount++
			}
			if pc == emf {
				emfCount++
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
		for i, s := range sites {
			agg[i] += p[s.pc]
		}
	}
	region := func(pc int) string {
		switch {
		case pc >= int(grec):
			return "genrecap"
		case pc >= int(genq):
			return "qscopy"
		case pc >= int(gen):
			return "fullcopy"
		default:
			return "emit-internal"
		}
	}
	byRegion := map[string]uint64{}
	for i, s := range sites {
		calls := agg[i] / 6
		byRegion[region(s.pc)+"/"+s.target] += calls
		if calls > 500 {
			t.Logf("site $%04X (%s, %s): %d calls", s.pc, s.target, region(s.pc), calls)
		}
	}
	t.Logf("emitmove entries: %d, emitmovef entries: %d", emCount, emfCount)
	for k, v := range byRegion {
		t.Logf("region %-20s %d calls", k, v)
	}
}

// TestMicroABAdopted: the MicroAB exact-tree fingerprint at the adopted
// gameplay config (FEATURES 0x1F + FEATURES2 0x01), depth 6. Same
// probes and output shape as TestMicroAB; run before/after a movegen
// change and diff — counts+score+move must be identical, cycles drop.
func TestMicroABAdopted(t *testing.T) {
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
	type probe struct {
		name string
		addr uint16
	}
	probes := []probe{
		{"search", labels["search"]},
		{"make", labels["make"]},
		{"eval", labels["eval"]},
		{"attacked", labels["attacked"]},
		{"ttprobe", labels["ttprobe"]},
		{"generate", labels["generate"]},
	}
	var grand uint64
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
		SetBudget(m, defs, 0, 6) // fixed depth
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		counts := make([]uint64, len(probes))
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
			for i := range probes {
				if pc == probes[i].addr {
					counts[i]++
				}
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("fen=%q exited=%v code=%d err=%v", fen, exited, code, err)
		}
		score := int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
		bf, bt, bfl := m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]]
		grand += m.Cycles
		line := fmt.Sprintf("adopted %-24s sc=%6d mv=%s cyc=%10d |", fen[:24], score, MoveUCI(bf, bt, bfl), m.Cycles)
		for i := range probes {
			line += fmt.Sprintf(" %s=%d", probes[i].name, counts[i])
		}
		t.Log(line)
	}
	t.Logf("ADOPTED GRAND TOTAL CYCLES: %d", grand)
}
