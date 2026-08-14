// Command softclkdiag is the FT2_SOFTCLK estimator's measurement rig under
// GAME conditions.
//
// It exists because the 284-position pool test (chesstest.TestSoftClockAccuracy)
// and the actual per-game A/B disagreed about the estimator's error IN SIGN:
// the pool said est/truth = 1.052 (the engine believes it has spent MORE than
// it has, so it stops early), the games said the engine overran its clock by
// 17% (so in games the estimate runs LOW). See docs/results.md 2026-07-27.
//
// This tool plays real self-play games through the same loop the SPRT match
// uses — warm TT carried in the aux bank, the same per-move allocation — and
// records, per move, the two regressors the on-device estimator actually
// forms (poll count and the PHASE sampled at each poll), the true cycle cost,
// the completed depth, and optional probe counts. From those it can:
//
//	-mode report  : the est/truth distribution under game conditions
//	-mode fit     : refit SOFTA/SOFTB against game-condition data
//
// -coldtt drops the TT between moves, which turns a game into a sequence of
// pool-like cold searches and so isolates TT warmth as a cause.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/zellyn/8fish/internal/chesstest"
	"github.com/zellyn/8fish/internal/sprt"
)

func main() {
	var (
		binfile  = flag.String("bin", "asm/engine.bin", "engine binary")
		defsfile = flag.String("defs", "asm/defs.inc", "memory-layout defs")
		lblfile  = flag.String("lbl", "asm/engine.lbl", "label file (probe addresses)")
		aBits    = flag.String("a", "0x5f", "FEATURES for both sides")
		aBits2   = flag.String("a2", "0x30", "FEATURES2 for both sides (0x20 = FT2_SOFTCLK)")
		budgetMs = flag.Uint64("budget", 4000, "emulated ms per move")
		pairs    = flag.Int("pairs", 8, "opening pairs (games = 2x)")
		parallel = flag.Int("parallel", runtime.NumCPU()-1, "concurrent games")
		openseed = flag.Uint64("openseed", 0, "opening-generation seed")
		coldtt   = flag.Bool("coldtt", false, "DIAGNOSTIC: drop the TT between moves (pool-like cold searches)")
		probe    = flag.Bool("probe", true, "profile the run to recover poll count / sampled phase sum (2-3x slower)")
		extra    = flag.Bool("extra", false, "also count search/qsearch/make/eval/ttprobe/generate entries")
		dump     = flag.String("dump", "", "write per-move TSV here")
		fens     = flag.String("fens", "", "instead of playing games, run one budgeted search per FEN in this file (pool protocol: cold TT, no game context)")
		hmClamp  = flag.Int("halfmove", -1, "override the halfmove clock for -fens runs (-1 = take it from the FEN)")
		fitTab   = flag.Bool("fit", false, "fit the 25-entry per-phase cost table against this run and print it as ca65 source")
		fitLam   = flag.Float64("lambda", 1.0, "smoothness weight for -fit")
		fitFloor = flag.Uint64("fitfloor", 1_000_000, "-fit ignores moves cheaper than this many true cycles")
		margin   = flag.Float64("margin", 0.0, "-fit: multiply the fitted table by 1+margin (deliberate OVERestimate bias)")
		refit    = flag.String("refit", "", "re-fit from an existing -dump TSV (comma-separated dumps allowed) instead of running the engine")
		idloop   = flag.Bool("idloop", false, "with -fens: trace the estimate against the truth at every ID-iteration boundary (where the predictive gate reads it)")
	)
	flag.Parse()

	bin, err := os.ReadFile(*binfile)
	must(err)
	defs, err := chesstest.ParseDefs(*defsfile)
	must(err)
	labels, err := chesstest.ParseLabelFile(*lblfile)
	must(err)
	a := mustU8(*aBits)
	a2 := mustU8(*aBits2)

	probes := map[string]uint16{}
	if *probe {
		probes["checkclocks"] = labels["checkclocks"]
		if probes["checkclocks"] == 0 {
			fatal("checkclocks missing from " + *lblfile)
		}
		if *extra {
			for _, n := range []string{"search", "qsearch", "make", "eval", "ttprobe", "generate"} {
				if ad := labels[n]; ad != 0 {
					probes[n] = ad
				}
			}
		}
	}

	var mu sync.Mutex
	var rows []sprt.MoveInfo

	if *refit != "" {
		rows = readDump(*refit)
		summary(rows, *budgetMs*chesstest.CyclesPerMs, false)
		fitTable(rows, *fitLam, *fitFloor, *margin)
		return
	}

	if *fens != "" && *idloop {
		traceIDLoop(bin, defs, labels, *fens, a, a2, *budgetMs*chesstest.CyclesPerMs, *parallel)
		return
	}

	if *fens != "" {
		rows = runFens(bin, defs, *fens, a, a2, *budgetMs*chesstest.CyclesPerMs,
			probes, *parallel, *hmClamp)
		writeDump(*dump, rows)
		summary(rows, *budgetMs*chesstest.CyclesPerMs, true)
		if *fitTab {
			fitTable(rows, *fitLam, *fitFloor, *margin)
		}
		return
	}

	cfg := sprt.Config{
		Bin: bin, Defs: defs,
		FeaturesA: a, FeaturesB: a, FeaturesA2: a2, FeaturesB2: a2,
		BudgetCycles: *budgetMs * chesstest.CyclesPerMs,
		Pairs:        *pairs, Parallel: *parallel, OpenSeed: *openseed,
		PerGame: true, ColdTT: *coldtt, ProbeAddrs: probes,
		MoveTrace: func(mi sprt.MoveInfo) {
			mu.Lock()
			rows = append(rows, mi)
			mu.Unlock()
		},
	}
	res := sprt.Run(cfg)
	for _, e := range res.Errors {
		fmt.Fprintln(os.Stderr, "ERROR:", e)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].FEN != rows[j].FEN {
			return rows[i].FEN < rows[j].FEN
		}
		return rows[i].Cycles < rows[j].Cycles
	})

	writeDump(*dump, rows)
	summary(rows, *budgetMs*chesstest.CyclesPerMs, *coldtt)
	if *fitTab {
		fitTable(rows, *fitLam, *fitFloor, *margin)
	}
}

// lookups returns the complete list of cost-table lookups one move made: one
// per clock poll at the phase sampled there, PLUS one at the root phase for
// the entry prime (engine.s charges the 128 nodes that run before the first
// poll). Fitting against exactly this vector reproduces the on-device
// estimate to the byte.
func lookups(r sprt.MoveInfo) [sprt.NPCost]float64 {
	var l [sprt.NPCost]float64
	for p, n := range r.PhaseHist {
		l[p] = float64(n)
	}
	rp := r.Phase
	if rp >= sprt.NPCost {
		rp = sprt.NPCost - 1
	}
	l[rp]++
	return l
}

// fitTable fits the 25-entry per-phase cost table by relative least squares —
// the loss a time budget actually cares about, since a budget is spent
// proportionally — with a second-difference smoothness penalty so the thin
// phase buckets borrow shape from their neighbours instead of chasing noise.
//
// The estimator's whole structure is preserved: this only refills PCOSTLO /
// PCOSTHI. The linear SOFTA + SOFTB*phase form is dropped because the
// game-condition cost curve is visibly concave (it climbs steeply from phase
// 0 to ~8, then flattens), and a straight line through it is 10-16% wrong at
// both ends. The table costs exactly the same at runtime — one indexed fetch.
func fitTable(rows []sprt.MoveInfo, lambda float64, floor uint64, margin float64) {
	const N = sprt.NPCost
	var a [N][N]float64
	var b [N]float64
	used := 0
	var seen [N]float64
	for _, r := range rows {
		if r.Cycles < floor || r.Polls == 0 {
			continue
		}
		used++
		l := lookups(r)
		var row [N]float64
		for p := range row {
			row[p] = 128 * l[p] / float64(r.Cycles)
			seen[p] += l[p]
		}
		for i := range row {
			if row[i] == 0 {
				continue
			}
			for j := range row {
				a[i][j] += row[i] * row[j]
			}
			b[i] += row[i]
		}
	}
	if used == 0 {
		fatal("fit: no usable moves")
	}
	// Smoothness: penalise the second difference, scaled so lambda is
	// dimensionless against a ~5000-cycle node and the sample size.
	const c0 = 5000.0
	lam := lambda * float64(used) / (c0 * c0)
	for p := 1; p < N-1; p++ {
		d := [N]float64{}
		d[p-1], d[p], d[p+1] = 1, -2, 1
		for i := range d {
			if d[i] == 0 {
				continue
			}
			for j := range d {
				a[i][j] += lam * d[i] * d[j]
			}
		}
	}
	// Tiny ridge so an entirely unobserved phase still resolves.
	for p := range a {
		a[p][p] += 1e-9
	}
	c := solve(a, b)

	// Monotone non-decreasing in phase: more material can only mean more
	// work per node, and it keeps an under-observed bucket from dipping.
	for p := 1; p < N; p++ {
		if c[p] < c[p-1] {
			c[p] = c[p-1]
		}
	}
	for p := range c {
		c[p] *= 1 + margin
	}

	fmt.Printf("--- FITTED COST TABLE (n=%d moves >= %d cycles, lambda=%.2f, margin=%+.1f%%) ---\n",
		used, floor, lambda, 100*margin)
	for p := 0; p < N; p++ {
		fmt.Printf("  phase %2d  cost/node %7.1f  (polls observed: %.0f)\n", p, c[p], seen[p])
	}
	// Residuals under the fitted table.
	var sq, sp, sy, worst float64
	n := 0
	for _, r := range rows {
		if r.Polls == 0 {
			continue
		}
		l := lookups(r)
		var pred float64
		for p := range l {
			pred += 128 * l[p] * c[p]
		}
		e := 100 * (pred - float64(r.Cycles)) / float64(r.Cycles)
		sq += (e / 100) * (e / 100)
		if math.Abs(e) > math.Abs(worst) {
			worst = e
		}
		sp += pred
		sy += float64(r.Cycles)
		n++
	}
	fmt.Printf("  predicted est/truth over all %d polled moves: %.4f   RMS %.1f%%  worst %+.1f%%\n",
		n, sp/sy, 100*math.Sqrt(sq/float64(n)), worst)

	// The parametric form defs.inc actually carries.
	pa, pb, pc, pk, prms, ppool := fitPiecewise(rows, floor, margin)
	fmt.Printf("--- PIECEWISE fit: cost/node = %.1f + %.2f*min(phase,%d) + %.2f*max(phase-%d,0)\n",
		pa, pb, pk, pc, pk)
	fmt.Printf("    over all polled moves: est/truth %.4f  RMS %.1f%%\n", ppool, prms)
	const scale = 16
	fmt.Printf("    SOFTSCALE = %d\n    SOFTA = %d\n    SOFTB = %d\n    SOFTC = %d\n    SOFTK = %d\n",
		scale, int(math.Round(pa*scale)), int(math.Round(pb*scale)), int(math.Round(pc*scale)), pk)
	for p := 0; p < N; p++ {
		v := pa + pb*float64(min(p, pk)) + pc*float64(max(p-pk, 0))
		fmt.Printf("      phase %2d: free %7.1f  piecewise %7.1f  (%+.1f%%)\n",
			p, c[p], v, 100*(v-c[p])/c[p])
	}
}

// fitPiecewise fits the PARAMETRIC form the assembler can generate:
//
//	cost(p) = A + B*min(p,K) + C*max(p-K,0)
//
// a two-segment line in the taper phase. The knee exists because the
// game-condition cost curve is concave: per-node cost climbs steeply from
// phase 0 to about phase 8 and then almost flattens, so the single straight
// line the shipped estimator used is wrong at both ends however it is fit.
// Three constants and a knee keep asm/defs.inc parametric and reviewable —
// no 50 hand-maintained bytes — and cost exactly nothing at runtime, since
// the table is still built at assembly time and read with one indexed fetch.
//
// K is chosen by exhaustive search over the plausible range, minimising the
// same relative loss.
func fitPiecewise(rows []sprt.MoveInfo, floor uint64, margin float64) (a, b, c float64, k int, rms, pool float64) {
	best := math.Inf(1)
	for kk := 2; kk <= 20; kk++ {
		var m [3][3]float64
		var v [3]float64
		for _, r := range rows {
			if r.Cycles < floor || r.Polls == 0 {
				continue
			}
			l := lookups(r)
			var x [3]float64
			for p := range l {
				if l[p] == 0 {
					continue
				}
				n := 128 * l[p] / float64(r.Cycles)
				x[0] += n
				x[1] += n * float64(min(p, kk))
				x[2] += n * float64(max(p-kk, 0))
			}
			for i := range x {
				for j := range x {
					m[i][j] += x[i] * x[j]
				}
				v[i] += x[i]
			}
		}
		s := solve3(m, v)
		// Per-node cost must not FALL as material rises: above the knee the
		// data is flat, and an unconstrained fit reads a small negative slope
		// out of that noise. A falling tail would under-price exactly the
		// full-material middlegames where the searches are largest, so clamp
		// it to flat and refit the two remaining parameters.
		if s[2] < 0 {
			var m2 [3][3]float64
			var v2 [3]float64
			for _, r := range rows {
				if r.Cycles < floor || r.Polls == 0 {
					continue
				}
				l := lookups(r)
				var x [3]float64
				for p := range l {
					if l[p] == 0 {
						continue
					}
					n := 128 * l[p] / float64(r.Cycles)
					x[0] += n
					x[1] += n * float64(min(p, kk))
				}
				x[2] = 0
				for i := range x {
					for j := range x {
						m2[i][j] += x[i] * x[j]
					}
					v2[i] += x[i]
				}
			}
			m2[2][2] = 1 // pin the clamped parameter
			v2[2] = 0
			s = solve3(m2, v2)
			s[2] = 0
		}
		// Score: relative RMS over the same rows.
		var sq float64
		n := 0
		for _, r := range rows {
			if r.Cycles < floor || r.Polls == 0 {
				continue
			}
			l := lookups(r)
			var pred float64
			for p := range l {
				pred += 128 * l[p] * (s[0] + s[1]*float64(min(p, kk)) + s[2]*float64(max(p-kk, 0)))
			}
			e := (pred - float64(r.Cycles)) / float64(r.Cycles)
			sq += e * e
			n++
		}
		if r := math.Sqrt(sq / float64(n)); r < best {
			best, a, b, c, k = r, s[0], s[1], s[2], kk
		}
	}
	a *= 1 + margin
	b *= 1 + margin
	c *= 1 + margin
	// Report over ALL polled moves (not just the fit set).
	var sq, sp, sy float64
	n := 0
	for _, r := range rows {
		if r.Polls == 0 {
			continue
		}
		l := lookups(r)
		var pred float64
		for p := range l {
			pred += 128 * l[p] * (a + b*float64(min(p, k)) + c*float64(max(p-k, 0)))
		}
		e := (pred - float64(r.Cycles)) / float64(r.Cycles)
		sq += e * e
		sp += pred
		sy += float64(r.Cycles)
		n++
	}
	return a, b, c, k, 100 * math.Sqrt(sq/float64(n)), sp / sy
}

func solve3(m [3][3]float64, v [3]float64) [3]float64 {
	var a [sprt.NPCost][sprt.NPCost]float64
	var b [sprt.NPCost]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			a[i][j] = m[i][j]
		}
		b[i] = v[i]
	}
	for i := 3; i < sprt.NPCost; i++ {
		a[i][i] = 1
	}
	x := solve(a, b)
	return [3]float64{x[0], x[1], x[2]}
}

// solve does Gaussian elimination with partial pivoting on a small dense
// system.
func solve(a [sprt.NPCost][sprt.NPCost]float64, b [sprt.NPCost]float64) [sprt.NPCost]float64 {
	const N = sprt.NPCost
	for col := 0; col < N; col++ {
		piv := col
		for r := col + 1; r < N; r++ {
			if math.Abs(a[r][col]) > math.Abs(a[piv][col]) {
				piv = r
			}
		}
		a[col], a[piv] = a[piv], a[col]
		b[col], b[piv] = b[piv], b[col]
		if a[col][col] == 0 {
			continue
		}
		for r := 0; r < N; r++ {
			if r == col {
				continue
			}
			f := a[r][col] / a[col][col]
			if f == 0 {
				continue
			}
			for k := col; k < N; k++ {
				a[r][k] -= f * a[col][k]
			}
			b[r] -= f * b[col]
		}
	}
	var x [N]float64
	for i := 0; i < N; i++ {
		if a[i][i] != 0 {
			x[i] = b[i] / a[i][i]
		}
	}
	return x
}

func writeDump(path string, rows []sprt.MoveInfo) {
	if path == "" {
		return
	}
	f, err := os.Create(path)
	must(err)
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "ply\tphase\tbudget\tcycles\testimate\tdepth\tpolls\tphasesum\tsearch\tmake\teval\tttprobe\tgenerate\thist\tfen")
	for _, r := range rows {
		c := func(n string) uint64 { return r.Counts[n] }
		hs := make([]string, len(r.PhaseHist))
		for i, v := range r.PhaseHist {
			hs[i] = strconv.FormatUint(uint64(v), 10)
		}
		fmt.Fprintf(w, "%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\n",
			r.Ply, r.Phase, r.Budget, r.Cycles, r.Estimate, r.Depth, r.Polls, r.PhaseSum,
			c("search"), c("make"), c("eval"), c("ttprobe"), c("generate"),
			strings.Join(hs, ","), r.FEN)
	}
	w.Flush()
	f.Close()
}

// traceIDLoop answers the question the aggregate est/truth ratio cannot: the
// estimator can be right about a WHOLE move and still be wrong at the moment
// the predictive gate in idloop reads it, and it is that reading — not the
// final total — that decides whether one more iteration is started. The gate
// compares now + 2*(last iteration's cost) against BUDGET, so a low reading
// buys a whole extra iteration, which is 2-6x the last one.
//
// It samples (estimate, true cycles) at every entry to idloop, i.e. exactly
// where ITSTART is latched and one instruction before each gate decision.
func traceIDLoop(bin []byte, defs chesstest.Defs, labels map[string]uint16,
	path string, feat, feat2 byte, budget uint64, parallel int) {

	addr := labels["idloop"]
	if addr == 0 {
		fatal("idloop missing from the label file")
	}
	list := readFenList(path)
	if parallel <= 0 {
		parallel = 1
	}
	type sample struct{ est, truth float64 }
	var mu sync.Mutex
	byIter := map[int][]sample{}
	var endEst, endTruth float64
	ch := make(chan int)
	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				pos, err := chesstest.ParseFEN(list[i])
				if err != nil {
					continue
				}
				m, err := chesstest.NewMachine(bin, defs, pos, 0, nil)
				if err != nil {
					continue
				}
				m.Mem.ClockAddr = 0
				chesstest.SetFeatures(m, defs, feat)
				chesstest.SetFeatures2(m, defs, feat2)
				chesstest.SetBudget(m, defs, budget, 24)
				m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
				ca := defs["CLOCK_TRAP"]
				var local []sample
				exited, _, err := m.RunProfile(budget*8+2_000_000_000, func(pc uint16, _ uint8) {
					if pc != addr {
						return
					}
					est := (uint64(m.Mem.Main[ca]) | uint64(m.Mem.Main[ca+1])<<8 |
						uint64(m.Mem.Main[ca+2])<<16) << 8
					local = append(local, sample{float64(est), float64(m.Cycles)})
				})
				if err != nil || !exited {
					continue
				}
				est := (uint64(m.Mem.Main[ca]) | uint64(m.Mem.Main[ca+1])<<8 |
					uint64(m.Mem.Main[ca+2])<<16) << 8
				mu.Lock()
				// Index from the END: iteration boundary -1 is the last gate
				// decision, the one that actually matters.
				for k, s := range local {
					byIter[k-len(local)] = append(byIter[k-len(local)], s)
				}
				endEst += float64(est)
				endTruth += float64(m.Cycles)
				mu.Unlock()
			}
		}()
	}
	for i := range list {
		ch <- i
	}
	close(ch)
	wg.Wait()

	fmt.Printf("ID-loop trace over %d positions, budget %d cycles\n", len(list), budget)
	fmt.Printf("  at MOVE END:            est/truth %.4f\n", endEst/endTruth)
	keys := make([]int, 0, len(byIter))
	for k := range byIter {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		var e, t float64
		n := 0
		for _, s := range byIter[k] {
			if s.truth == 0 {
				continue
			}
			e += s.est
			t += s.truth
			n++
		}
		if n == 0 || t == 0 {
			continue
		}
		fmt.Printf("  gate reading %+d from end: n=%-4d est/truth %.4f\n", k, n, e/t)
	}
}

func readFenList(path string) []string {
	data, err := os.ReadFile(path)
	must(err)
	var list []string
	seen := map[string]bool{}
	for _, line := range splitLines(string(data)) {
		if line == "" || line[0] == '#' {
			continue
		}
		if i := lastTab(line); i >= 0 {
			line = line[i+1:]
		}
		if i := indexOf(line, " id "); i >= 0 {
			line = line[:i]
		}
		line = trimSemi(line)
		if nf := countFields(line); nf == 4 {
			line += " 0 1"
		} else if nf < 4 {
			continue
		}
		if !seen[line] {
			seen[line] = true
			list = append(list, line)
		}
	}
	return list
}

// readDump reloads one or more -dump TSVs so a table can be refit (or a
// margin re-chosen) without spending another hour of emulator time.
func readDump(paths string) []sprt.MoveInfo {
	var out []sprt.MoveInfo
	for _, path := range strings.Split(paths, ",") {
		data, err := os.ReadFile(path)
		must(err)
		for i, line := range splitLines(string(data)) {
			if i == 0 || line == "" {
				continue
			}
			f := strings.Split(line, "\t")
			if len(f) < 15 {
				continue
			}
			u := func(k int) uint64 { v, _ := strconv.ParseUint(f[k], 10, 64); return v }
			r := sprt.MoveInfo{
				Ply: int(u(0)), Phase: int(u(1)), Budget: u(2), Cycles: u(3),
				Estimate: u(4), Depth: int(u(5)), Polls: u(6), PhaseSum: u(7),
				Counts: map[string]uint64{"search": u(8), "make": u(9), "eval": u(10),
					"ttprobe": u(11), "generate": u(12)},
				FEN: f[14],
			}
			for j, s := range strings.Split(f[13], ",") {
				if j < sprt.NPCost {
					v, _ := strconv.ParseUint(s, 10, 32)
					r.PhaseHist[j] = uint32(v)
				}
			}
			out = append(out, r)
		}
	}
	return out
}

// runFens is the POOL protocol: one budgeted search per FEN, cold TT, no game
// context — exactly what TestSoftClockAccuracy does. Running the same
// measurement over game-visited FENs and over the pool's own FENs is what
// separates "the pool's positions are different" from "the game loop is
// different".
func runFens(bin []byte, defs chesstest.Defs, path string, feat, feat2 byte,
	budget uint64, probes map[string]uint16, parallel, hm int) []sprt.MoveInfo {

	data, err := os.ReadFile(path)
	must(err)
	var list []string
	seen := map[string]bool{}
	for _, line := range splitLines(string(data)) {
		if line == "" || line[0] == '#' {
			continue
		}
		// Accept both plain FEN files and the diag's own TSV dump (FEN last).
		if i := lastTab(line); i >= 0 {
			line = line[i+1:]
		}
		if i := indexOf(line, " id "); i >= 0 {
			line = line[:i]
		}
		line = trimSemi(line)
		if nf := countFields(line); nf == 4 {
			line += " 0 1"
		} else if nf < 4 {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		list = append(list, line)
	}
	if parallel <= 0 {
		parallel = 1
	}
	out := make([]sprt.MoveInfo, len(list))
	ch := make(chan int)
	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				fen := list[i]
				pos, err := chesstest.ParseFEN(fen)
				if err != nil {
					continue
				}
				m, err := chesstest.NewMachine(bin, defs, pos, 0, nil)
				if err != nil {
					continue
				}
				// Hardware semantics ONLY when the estimator is on: with
				// FT2_SOFTCLK clear the engine needs the harness's live
				// counter, and killing the trap would leave it with a clock
				// stuck at zero that never aborts.
				if ft2 := defs["FT2_SOFTCLK"]; ft2 != 0 && feat2&byte(ft2) != 0 {
					m.Mem.ClockAddr = 0
				}
				chesstest.SetFeatures(m, defs, feat)
				chesstest.SetFeatures2(m, defs, feat2)
				chesstest.SetBudget(m, defs, budget, 24)
				h := pos.Halfmove
				if hm >= 0 {
					h = byte(min(hm, 255))
				}
				m.Mem.Main[defs["HALFMOVE"]] = h
				var polls, phaseSum uint64
				var hist [sprt.NPCost]uint32
				counts := map[string]uint64{}
				var exited bool
				if len(probes) > 0 {
					cc, ph := probes["checkclocks"], defs["PHASE"]
					names := make([]string, 0, len(probes))
					addrs := make([]uint16, 0, len(probes))
					for n, a := range probes {
						names, addrs = append(names, n), append(addrs, a)
					}
					hits := make([]uint64, len(addrs))
					exited, _, err = m.RunProfile(budget*8+2_000_000_000, func(pc uint16, _ uint8) {
						for k, a := range addrs {
							if pc == a {
								hits[k]++
							}
						}
						if pc == cc {
							polls++
							p := int(m.Mem.Main[ph])
							phaseSum += uint64(p)
							if p >= sprt.NPCost {
								p = sprt.NPCost - 1
							}
							hist[p]++
						}
					})
					for k, n := range names {
						counts[n] = hits[k]
					}
				} else {
					exited, _, err = m.Run(budget*8 + 2_000_000_000)
				}
				if err != nil || !exited {
					continue
				}
				ca := defs["CLOCK_TRAP"]
				out[i] = sprt.MoveInfo{
					FEN: fen, Phase: int(m.Mem.Main[defs["PHASE"]]), Budget: budget,
					Cycles: m.Cycles,
					Estimate: (uint64(m.Mem.Main[ca]) | uint64(m.Mem.Main[ca+1])<<8 |
						uint64(m.Mem.Main[ca+2])<<16) << 8,
					Depth: int(m.Mem.Main[defs["CURDEPTH"]]),
					Polls: polls, PhaseSum: phaseSum, PhaseHist: hist, Counts: counts,
				}
			}
		}()
	}
	for i := range list {
		ch <- i
	}
	close(ch)
	wg.Wait()
	var ok []sprt.MoveInfo
	for _, r := range out {
		if r.Cycles > 0 {
			ok = append(ok, r)
		}
	}
	return ok
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, trimSpace(s[start:i]))
			start = i + 1
		}
	}
	out = append(out, trimSpace(s[start:]))
	return out
}
func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
func trimSemi(s string) string {
	s = trimSpace(s)
	if len(s) > 0 && s[len(s)-1] == ';' {
		s = trimSpace(s[:len(s)-1])
	}
	return s
}
func lastTab(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\t' {
			return i
		}
	}
	return -1
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func countFields(s string) int {
	n, in := 0, false
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			in = false
		} else if !in {
			in = true
			n++
		}
	}
	return n
}

// summary prints the game-condition error distribution and the refit.
func summary(rows []sprt.MoveInfo, income uint64, cold bool) {
	if len(rows) == 0 {
		fatal("no moves")
	}
	var sumEst, sumTrue, sumBudget float64
	var haveEst int
	for _, r := range rows {
		sumEst += float64(r.Estimate)
		sumTrue += float64(r.Cycles)
		sumBudget += float64(income)
		if r.Estimate > 0 {
			haveEst++
		}
	}
	fmt.Printf("moves=%d  coldtt=%v  income=%d\n", len(rows), cold, income)
	fmt.Printf("ADHERENCE (sum true cycles / sum income) = %.4f\n", sumTrue/sumBudget)
	if haveEst > 0 {
		fmt.Printf("GAME pool estimate/truth                = %.4f  (%d moves carry an estimate)\n",
			sumEst/sumTrue, haveEst)
	}

	// Per-move relative error of the estimate.
	if haveEst == len(rows) {
		errs := make([]float64, 0, len(rows))
		for _, r := range rows {
			errs = append(errs, 100*(float64(r.Estimate)-float64(r.Cycles))/float64(r.Cycles))
		}
		sort.Float64s(errs)
		var sum, sq float64
		for _, e := range errs {
			sum += e
			sq += (e / 100) * (e / 100)
		}
		q := func(p float64) float64 { return errs[int(p*float64(len(errs)-1))] }
		fmt.Printf("per-move rel err: bias %+.1f%%  RMS %.1f%%  p10 %+.1f%%  p50 %+.1f%%  p90 %+.1f%%\n",
			sum/float64(len(errs)), 100*math.Sqrt(sq/float64(len(errs))), q(0.10), q(0.50), q(0.90))
	}

	// Cycles per counted node, which is what the cost model prices.
	var polls, phasesum, cyc float64
	for _, r := range rows {
		polls += float64(r.Polls)
		phasesum += float64(r.PhaseSum)
		cyc += float64(r.Cycles)
	}
	if polls > 0 {
		fmt.Printf("polls=%.0f  mean sampled phase=%.2f  MEAN CYCLES/NODE=%.1f\n",
			polls, phasesum/polls, cyc/(polls*128))
		f := fitSoft(rows)
		fmt.Printf("GAME-CONDITION FIT: cost/node = %.1f + %.2f*phase   ->  SOFTA=%d SOFTB=%d (SOFTSCALE 16)\n",
			f.a, f.b, int(math.Round(f.a*16)), int(math.Round(f.b*16)))
		// residuals
		var sq2, worst, sp, sa float64
		var n int
		for _, r := range rows {
			if r.Polls == 0 {
				continue // sub-quantum: the prime alone, nothing to fit
			}
			n++
			p := f.a*float64(r.Polls)*128 + f.b*float64(r.PhaseSum)*128
			e := 100 * (p - float64(r.Cycles)) / float64(r.Cycles)
			sq2 += (e / 100) * (e / 100)
			if math.Abs(e) > math.Abs(worst) {
				worst = e
			}
			sp += p
			sa += float64(r.Cycles)
		}
		fmt.Printf("  in-sample (n=%d, polls>0) RMS %.1f%%  worst %+.1f%%  pool pred/actual %.4f\n",
			n, 100*math.Sqrt(sq2/float64(n)), worst, sp/sa)
	}

	// Breakdowns.
	byBucket := func(name string, key func(sprt.MoveInfo) int, order []int, label func(int) string) {
		g := map[int][]sprt.MoveInfo{}
		for _, r := range rows {
			g[key(r)] = append(g[key(r)], r)
		}
		fmt.Printf("--- by %s ---\n", name)
		ks := order
		if ks == nil {
			for k := range g {
				ks = append(ks, k)
			}
			sort.Ints(ks)
		}
		for _, k := range ks {
			rs := g[k]
			if len(rs) == 0 {
				continue
			}
			var e, t, p, ps float64
			for _, r := range rs {
				e += float64(r.Estimate)
				t += float64(r.Cycles)
				p += float64(r.Polls)
				ps += float64(r.PhaseSum)
			}
			cpn := 0.0
			mph := 0.0
			if p > 0 {
				cpn = t / (p * 128)
				mph = ps / p
			}
			fmt.Printf("  %-14s n=%-5d est/true %.4f  adherence %.4f  cyc/node %7.1f  meanphase %5.2f\n",
				label(k), len(rs), e/t, t/(float64(len(rs))*float64(income)), cpn, mph)
		}
	}
	byBucket("completed depth", func(r sprt.MoveInfo) int { return r.Depth }, nil,
		func(k int) string { return fmt.Sprintf("depth %d", k) })
	byBucket("root phase", func(r sprt.MoveInfo) int {
		switch {
		case r.Phase <= 3:
			return 0
		case r.Phase <= 7:
			return 1
		case r.Phase <= 13:
			return 2
		case r.Phase <= 19:
			return 3
		default:
			return 4
		}
	}, []int{0, 1, 2, 3, 4}, func(k int) string {
		return []string{"phase 0-3", "phase 4-7", "phase 8-13", "phase 14-19", "phase 20-24"}[k]
	})
	byBucket("game ply", func(r sprt.MoveInfo) int { return r.Ply / 20 }, nil,
		func(k int) string { return fmt.Sprintf("ply %d-%d", k*20, k*20+19) })
}

type fit struct{ a, b float64 }

// fitSoft is the same relative-least-squares fit chesstest.fitSoft does, on
// game-condition rows: minimise sum ((a*polls + b*phasesum)/cyc - 1)^2, then
// divide by the 128 nodes a poll covers to get a per-NODE cost.
func fitSoft(rows []sprt.MoveInfo) fit {
	var saa, sab, sbb, sa, sb float64
	for _, r := range rows {
		if r.Cycles == 0 || r.Polls == 0 {
			continue
		}
		p := float64(r.Polls) / float64(r.Cycles)
		f := float64(r.PhaseSum) / float64(r.Cycles)
		saa += p * p
		sab += p * f
		sbb += f * f
		sa += p
		sb += f
	}
	det := saa*sbb - sab*sab
	if det == 0 {
		return fit{}
	}
	return fit{
		a: (sa*sbb - sb*sab) / det / 128,
		b: (sb*saa - sa*sab) / det / 128,
	}
}

func mustU8(s string) byte {
	v, err := strconv.ParseUint(s, 0, 8)
	must(err)
	return byte(v)
}
func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}
func fatal(s string) {
	fmt.Fprintln(os.Stderr, s)
	os.Exit(1)
}
