package chesstest

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/zellyn/8fish/harness"
)

// Profile accumulates per-PC cycle counts from a profiled run, plus
// cheap zero-page-bucketed views (cycles by PLY and by CURDEPTH at the
// time each instruction executed).
type Profile struct {
	PCCycles []uint64 // indexed by PC (65536 entries)
	ByPly    [64]uint64
	ByDepth  [64]uint64
	Total    uint64
}

// RunProfiled executes the machine like Machine.Run while building a
// Profile. defs supplies the PLY/CURDEPTH addresses for bucketing.
func RunProfiled(m *harness.Machine, defs Defs, maxCycles uint64) (bool, byte, *Profile, error) {
	p := &Profile{PCCycles: make([]uint64, 65536)}
	plyAddr := defs["PLY"]
	depthAddr := defs["CURDEPTH"]
	exited, code, err := m.RunProfile(maxCycles, func(pc uint16, cycles uint8) {
		c := uint64(cycles)
		p.PCCycles[pc] += c
		p.Total += c
		p.ByPly[m.Mem.Main[plyAddr]&63] += c
		p.ByDepth[m.Mem.Main[depthAddr]&63] += c
	})
	return exited, code, p, err
}

// ParseLabelFile reads an ld65 -Ln VICE label file ("al 00C000 .LABEL")
// into a symbol table (addresses above $FFFF are dropped).
func ParseLabelFile(path string) (map[string]uint16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	labels := map[string]uint16{}
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "al" && strings.HasPrefix(f[2], ".") {
			if v, err := strconv.ParseUint(f[1], 16, 32); err == nil && v <= 0xFFFF {
				labels[f[2][1:]] = uint16(v)
			}
		}
	}
	return labels, nil
}

// ByRoutine aggregates a Profile's per-PC cycles into labeled buckets:
// each PC is attributed to the nearest label at or below it. Returns
// (name, cycles) pairs sorted by descending cycles.
func (p *Profile) ByRoutine(labels map[string]uint16) []struct {
	Name   string
	Cycles uint64
} {
	type lab struct {
		addr uint16
		name string
	}
	labs := make([]lab, 0, len(labels))
	for name, addr := range labels {
		labs = append(labs, lab{addr, name})
	}
	sort.Slice(labs, func(i, j int) bool {
		if labs[i].addr != labs[j].addr {
			return labs[i].addr < labs[j].addr
		}
		return labs[i].name < labs[j].name // deterministic among aliases
	})
	agg := map[string]uint64{}
	li := 0
	cur := "(pre-code)"
	for pc := 0; pc < 65536; pc++ {
		for li < len(labs) && int(labs[li].addr) <= pc {
			cur = labs[li].name
			li++
		}
		if c := p.PCCycles[pc]; c != 0 {
			agg[cur] += c
		}
	}
	out := make([]struct {
		Name   string
		Cycles uint64
	}, 0, len(agg))
	for name, c := range agg {
		out = append(out, struct {
			Name   string
			Cycles uint64
		}{name, c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Cycles > out[j].Cycles })
	return out
}

// Report renders the top-n routine buckets plus the by-ply and
// by-depth cycle histograms as a printable string.
func (p *Profile) Report(labels map[string]uint16, n int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "total %dM cycles\n", p.Total/1_000_000)
	for i, r := range p.ByRoutine(labels) {
		if i >= n {
			break
		}
		fmt.Fprintf(&b, "  %-16s %10d  %4.1f%%\n", r.Name, r.Cycles,
			100*float64(r.Cycles)/float64(p.Total))
	}
	b.WriteString("  by CURDEPTH: ")
	for d, c := range p.ByDepth {
		if c > 0 {
			fmt.Fprintf(&b, "d%d:%d%% ", d, 100*c/p.Total)
		}
	}
	b.WriteString("\n  by PLY: ")
	for ply, c := range p.ByPly {
		if c > p.Total/100 {
			fmt.Fprintf(&b, "p%d:%d%% ", ply, 100*c/p.Total)
		}
	}
	b.WriteString("\n")
	return b.String()
}
