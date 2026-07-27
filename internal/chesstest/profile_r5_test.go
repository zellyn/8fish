package chesstest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// r5Pos is a profiling position tagged with its game phase, so the
// round-5 review can see where the cycles go in each phase separately
// as well as in aggregate.
type r5Pos struct {
	phase string
	fen   string
}

// r5Positions spans the phases deliberately. Round 4 profiled only two
// midgame FENs, which is why its target list was midgame-shaped; task
// #32 (cost model over-prices endgame nodes ~30%) is direct evidence
// that endgame node shape differs enough to matter.
var r5Positions = []r5Pos{
	// Midgame — the two round-4 FENs, kept so the rounds are comparable.
	{"midgame", "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8"},
	{"midgame", "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8"},
	// Opening — near the book exit, high mobility, many pieces.
	{"opening", "rnbqkb1r/pp2pppp/3p1n2/2pP4/4P3/2N5/PPP2PPP/R1BQKBNR w KQkq - 0 5"},
	// Tactical midgame — open lines, checks available (exercises FT_CKEXT,
	// which did not exist when round 4 profiled).
	{"tactical", "r3k2r/pbpnqppp/1p2pn2/3p4/2PP4/1PN1PN2/PB2QPPP/R3K2R w KQkq - 0 11"},
	// Endgame — few pieces, deep trees, different attacked()/eval mix.
	{"endgame", "8/2p2pk1/1p1p2p1/p2P4/P1P1PP2/1P4K1/8/8 w - - 0 1"},
	{"endgame", "8/3k4/p1p5/P1P5/3r4/5R2/5PPP/6K1 w - - 0 1"},
}

// TestProfileR5 (diagnostic): per-routine cycle shares at the SHIPPED
// gameplay config (FEATURES 0x5F = 0x1F | FT_CKEXT, FEATURES2 0),
// budget mode, aggregated across phase-diverse positions.
//
// This is the target-selection instrument for deep optimization round
// 5. It deliberately differs from TestProfileR4 in two ways that make
// the old target list untrustworthy: the shipped config now includes
// check extensions, and the position set now spans opening through
// endgame instead of two midgames.
//
// Run explicitly and read the log.
func TestProfileR5(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	shipped := byte(defs["FT_CKEXT"]) | 0x1F

	// Aggregate PC cycles across every position, and per phase.
	total := &Profile{PCCycles: make([]uint64, 65536)}
	perPhase := map[string]*Profile{}

	for _, pos := range r5Positions {
		p, err := ParseFEN(pos.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, p, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, shipped)
		SetBudget(m, defs, 30_000_000, 24)
		m.Mem.Main[defs["HALFMOVE"]] = p.Halfmove
		exited, code, prof, err := RunProfiled(m, defs, 400_000_000_000)
		if err != nil || !exited || code > 2 {
			t.Fatalf("%s: exited=%v code=%d err=%v", pos.fen[:20], exited, code, err)
		}
		t.Logf("%-8s %-24s %dM cycles", pos.phase, pos.fen[:24], prof.Total/1_000_000)

		if perPhase[pos.phase] == nil {
			perPhase[pos.phase] = &Profile{PCCycles: make([]uint64, 65536)}
		}
		for _, dst := range []*Profile{total, perPhase[pos.phase]} {
			for pc, c := range prof.PCCycles {
				dst.PCCycles[pc] += c
			}
			dst.Total += prof.Total
			for i, c := range prof.ByPly {
				dst.ByPly[i] += c
			}
			for i, c := range prof.ByDepth {
				dst.ByDepth[i] += c
			}
		}
	}

	t.Logf("\n===== AGGREGATE (shipped FEATURES=%#02x, %d positions) =====\n%s",
		shipped, len(r5Positions), total.Report(labels, 45))

	// Per-phase shares for the same routines, so a routine that is cheap
	// in the midgame but dominant in endgames cannot hide in the average.
	phases := make([]string, 0, len(perPhase))
	for ph := range perPhase {
		phases = append(phases, ph)
	}
	sort.Strings(phases)

	topN := 25
	agg := total.ByRoutine(labels)
	if len(agg) < topN {
		topN = len(agg)
	}
	phaseShare := map[string]map[string]float64{}
	for _, ph := range phases {
		phaseShare[ph] = map[string]float64{}
		for _, r := range perPhase[ph].ByRoutine(labels) {
			phaseShare[ph][r.Name] = 100 * float64(r.Cycles) / float64(perPhase[ph].Total)
		}
	}

	hdr := fmt.Sprintf("%-20s %8s", "routine", "ALL%")
	for _, ph := range phases {
		hdr += fmt.Sprintf(" %8s", ph)
	}
	rows := hdr + "\n"
	for i := 0; i < topN; i++ {
		r := agg[i]
		row := fmt.Sprintf("%-20s %7.2f%%", r.Name, 100*float64(r.Cycles)/float64(total.Total))
		for _, ph := range phases {
			row += fmt.Sprintf(" %7.2f%%", phaseShare[ph][r.Name])
		}
		rows += row + "\n"
	}
	t.Logf("\n===== PER-PHASE SHARES (top %d by aggregate) =====\n%s", topN, rows)

	// ---- Per-FILE rollup -------------------------------------------------
	//
	// The per-label view above fragments every routine: `make` shows as
	// make/mkfast/mkfqon/mkfmvon/mkfmvoff, `unmake` as unmake/umfmover/
	// umnohash/umfpawn, and so on. Optimization targets are chosen at the
	// routine and file level, so roll the labels up by the source file that
	// defines them.
	// Attribution is per-PC, by nearest preceding label whose defining file
	// is known. Attributing by label name alone leaves ~25% unattributed,
	// because macros (ATSLOT, MVPBODY, ...) emit ca65 .local labels that
	// exist in the link map but in no source file. Those PCs still sit
	// inside the emitting file's contiguous link-order range, so walking
	// addresses recovers them.
	owner := labelOwners(t)
	type lab struct {
		addr uint16
		file string
	}
	var known []lab
	for name, addr := range labels {
		if f := owner[name]; f != "" {
			known = append(known, lab{addr, f})
		}
	}
	sort.Slice(known, func(i, j int) bool { return known[i].addr < known[j].addr })

	byFile := map[string]uint64{}
	var attributed uint64
	ki := 0
	cur := "(pre-code)"
	for pc := 0; pc < 65536; pc++ {
		for ki < len(known) && int(known[ki].addr) <= pc {
			cur = known[ki].file
			ki++
		}
		if c := total.PCCycles[pc]; c != 0 {
			byFile[cur] += c
			attributed += c
		}
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Slice(files, func(i, j int) bool { return byFile[files[i]] > byFile[files[j]] })
	fileRows := ""
	for _, f := range files {
		fileRows += fmt.Sprintf("  %-22s %10d %6.2f%%\n", f, byFile[f],
			100*float64(byFile[f])/float64(total.Total))
	}
	t.Logf("\n===== PER-FILE ROLLUP (attributed %.1f%% of %dM cycles) =====\n%s",
		100*float64(attributed)/float64(total.Total), total.Total/1_000_000, fileRows)

	// Cumulative concentration: how flat is the profile really?
	cum := 0.0
	marks := map[int]bool{1: true, 3: true, 5: true, 10: true, 20: true, 40: true}
	conc := ""
	for i, r := range agg {
		cum += 100 * float64(r.Cycles) / float64(total.Total)
		if marks[i+1] {
			conc += fmt.Sprintf("  top-%-3d %6.2f%%\n", i+1, cum)
		}
	}
	conc += fmt.Sprintf("  all-%-3d %6.2f%%\n", len(agg), cum)
	t.Logf("\n===== CONCENTRATION (cumulative share by label) =====\n%s", conc)
}

// labelOwners maps each asm label name to the source file that defines
// it, by scanning the .s/.inc sources for column-0 `name:` definitions.
func labelOwners(t *testing.T) map[string]string {
	t.Helper()
	owner := map[string]string{}
	srcs, err := filepath.Glob("../../asm/*.s")
	if err != nil {
		t.Fatal(err)
	}
	incs, err := filepath.Glob("../../asm/*.inc")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*):`)
	for _, path := range append(srcs, incs...) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(path)
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			if _, dup := owner[m[1]]; !dup {
				owner[m[1]] = base
			}
		}
	}
	return owner
}
