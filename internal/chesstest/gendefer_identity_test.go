package chesstest

import (
	"fmt"
	"hash/fnv"
	"testing"
)

// gdFingerprint is the MicroAB fingerprint of one search: the entry counts
// that pin down tree shape and code-path traversal exactly, plus the
// result. Everything here must be IDENTICAL with FT2_GENDEFER on and off —
// that is the acceptance test for the feature, and the reason it needs no
// SPRT (see docs/results.md 2026-07-27).
type gdFingerprint struct {
	score  int16
	move   string
	trace  uint64 // FNV of the (FROM,TO,MVFLAGS) stream at every make()
	counts map[string]uint64
	cycles uint64
}

func (f gdFingerprint) String() string {
	return fmt.Sprintf("sc=%d mv=%s trace=%016x search=%d make=%d eval=%d ttprobe=%d | attacked=%d generate=%d",
		f.score, f.move, f.trace, f.counts["search"], f.counts["make"], f.counts["eval"],
		f.counts["ttprobe"], f.counts["attacked"], f.counts["generate"])
}

// same is the tree-identity predicate. `trace` is the whole tree: the
// (FROM,TO,MVFLAGS) bytes at every entry to make(), folded in order, so an
// equal hash means both engines made the same moves in the same order
// everywhere. The counts are kept as independent corroboration.
//
// `generate` and `attacked` are deliberately EXCLUDED, and neither is a
// tree signal here: generate is exactly what the feature skips, and
// attacked drops with it because generate's castling emission calls
// gcsafe2, which is two attacked() scans per castle-eligible node. Both
// falling while the make-trace hash holds is the shape of a correct
// saving.
func (f gdFingerprint) same(g gdFingerprint) bool {
	if f.score != g.score || f.move != g.move || f.trace != g.trace {
		return false
	}
	for _, k := range []string{"search", "make", "eval", "ttprobe"} {
		if f.counts[k] != g.counts[k] {
			return false
		}
	}
	return true
}

// gdSaturated is a soft budget far larger than any run here needs. Passing
// it puts the engine in BUDGET (iterative-deepening) mode without ever
// letting the budget decide anything: the ID driver's predictive gate
// ("does now + 2*cost still fit?") always says yes, so both A and B run
// every iteration 1..maxDepth and the tree stays deterministic. That
// matters because fixed-depth mode is a SINGLE iteration against a COLD TT
// — the regime in which this feature has almost nothing to do (the
// opportunity is TT-warmth-driven; docs/results.md 2026-07-27 measured it
// halving at a 10x-smaller budget). 2e9 also keeps the engine's ABORTL =
// 2*BUDGET inside its 24-bit 256-cycle-unit field.
const gdSaturated = 2_000_000_000

// gdRun searches fen under the given feature bytes and returns its
// fingerprint. budget 0 selects fixed-depth mode (one iteration at depth);
// gdSaturated selects real iterative deepening to that depth.
//
// Never a REAL time budget: a budgeted search converts the cycles this
// feature saves into extra nodes, so the trees would diverge by design and
// nothing could be compared.
func gdRun(t *testing.T, bin []byte, labels map[string]uint16, fen string, features, features2 byte, depth byte, budget uint64) gdFingerprint {
	t.Helper()
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetFeatures(m, defs, features)
	SetFeatures2(m, defs, features2)
	SetBudget(m, defs, budget, depth)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove

	names := []string{"search", "make", "eval", "attacked", "ttprobe", "generate"}
	addr := make([]uint16, len(names))
	for i, n := range names {
		a, ok := labels[n]
		if !ok {
			t.Fatalf("label %q missing", n)
		}
		addr[i] = a
	}
	counts := make([]uint64, len(names))
	h := fnv.New64a()
	makeAddr := labels["make"]
	fromA, toA, flagsA := defs["FROM"], defs["TO"], defs["MVFLAGS"]
	exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
		if pc == makeAddr {
			h.Write([]byte{m.Mem.Main[fromA], m.Mem.Main[toA], m.Mem.Main[flagsA]})
		}
		for i := range addr {
			if pc == addr[i] {
				counts[i]++
			}
		}
	})
	if err != nil || !exited || code > 2 {
		t.Fatalf("%.30s: exited=%v code=%d err=%v", fen, exited, code, err)
	}
	fp := gdFingerprint{
		trace:  h.Sum64(),
		score:  int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8),
		move:   MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]]),
		counts: map[string]uint64{},
		cycles: m.Cycles,
	}
	for i, n := range names {
		fp.counts[n] = counts[i]
	}
	return fp
}

// microABFens / microABTiers mirror TestMicroAB's suite exactly, so this
// test's ON-vs-OFF comparison is the same workload the project's A/B
// fingerprint instrument uses.
var microABFens = []string{
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8",
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11",
}

// microABTiers: TestMicroAB's own (mask, depth) tiers, plus an
// ITERATIVE-DEEPENING tier. The ID tier is the load-bearing one for this
// feature: MicroAB's fixed-depth mode is a SINGLE iteration against a cold
// TT, where barely any node has a TT move to defer on, so a fixed-depth-only
// identity test would be near-vacuous.
var microABTiers = []struct {
	mask   byte
	depth  byte
	budget uint64 // 0 = fixed depth, gdSaturated = full ID
	label  string
}{
	{0x1F, 6, 0, "fixed"},
	{0x07, 5, 0, "fixed"},
	{0x00, 4, 0, "fixed"},
	{0x1F | 0x40, 6, gdSaturated, "ID"}, // shipped mask (0x1F | FT_CKEXT)
}

// TestGenDeferTreeIdentity is the ACCEPTANCE TEST for FT2_GENDEFER: over
// the MicroAB suite and feature tiers, the make-move trace hash, score,
// best move and probe counts must be identical with the feature on and off.
// The feature is a pure cycle saving only if that holds, which is why it is
// MicroAB-gated rather than SPRT-gated.
//
// It also prints the honest cycle delta on the same tree.
func TestGenDeferTreeIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs the MicroAB suite twice")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	defer2 := byte(defs["FT2_GENDEFER"])
	var offTotal, onTotal uint64
	for _, tier := range microABTiers {
		var tOff, tOn, gOff, gOn uint64
		for _, fen := range microABFens {
			off := gdRun(t, bin, labels, fen, tier.mask, 0, tier.depth, tier.budget)
			on := gdRun(t, bin, labels, fen, tier.mask, defer2, tier.depth, tier.budget)
			if !off.same(on) {
				t.Errorf("TREE DIVERGED %s m%02x d%d %.24s\n  OFF %s\n  ON  %s",
					tier.label, tier.mask, tier.depth, fen, off, on)
				continue
			}
			tOff, tOn = tOff+off.cycles, tOn+on.cycles
			gOff, gOn = gOff+off.counts["generate"], gOn+on.counts["generate"]
			t.Logf("%-5s m%02x d%d %-24s identical; generate %d -> %d (-%.1f%%), cycles %d -> %d (%+.2f%%)",
				tier.label, tier.mask, tier.depth, fen[:24], off.counts["generate"], on.counts["generate"],
				100*float64(off.counts["generate"]-on.counts["generate"])/float64(off.counts["generate"]),
				off.cycles, on.cycles,
				-100*(float64(off.cycles)-float64(on.cycles))/float64(off.cycles))
		}
		if tOff > 0 {
			t.Logf("TIER %-5s m%02x d%d: generate -%.1f%%, cycles %.2f%% saved",
				tier.label, tier.mask, tier.depth,
				100*float64(gOff-gOn)/float64(gOff),
				100*(float64(tOff)-float64(tOn))/float64(tOff))
		}
		offTotal += tOff
		onTotal += tOn
	}
	if offTotal == 0 {
		return
	}
	t.Logf("MICROAB TOTAL: off=%d on=%d  saving=%.2f%%",
		offTotal, onTotal, 100*(float64(offTotal)-float64(onTotal))/float64(offTotal))
}

// TestGenDeferCycleDelta measures the feature's actual cycle saving on the
// round-5 PROFILE workload (the phase-diverse position set the +2.0%
// prediction was computed over), at the SHIPPED gameplay mask.
//
// Real iterative deepening under a saturated budget, NOT a real time
// budget: a genuinely budgeted search spends the saved cycles on extra
// nodes, so the two trees would differ by construction and there would be
// nothing to compare. Saturating the budget keeps ID's TT warmth — which is
// what the opportunity is made of — while leaving the tree deterministic.
//
// The depth cap is chosen PER POSITION by deepening the feature-off run
// until it passes gdControlCycles, so every phase is measured at roughly
// the ~30M-cycle control the prediction was made at rather than at whatever
// a fixed depth happens to cost (the phases differ by two orders of
// magnitude at equal depth).
const gdControlCycles = 30_000_000

func TestGenDeferCycleDelta(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	shipped := byte(defs["FT_CKEXT"]) | 0x1F
	defer2 := byte(defs["FT2_GENDEFER"])
	type acc struct{ off, on uint64 }
	total := acc{}
	perPhase := map[string]*acc{}
	for _, pos := range r5Positions {
		var off gdFingerprint
		var d byte
		for d = 4; d < 20; d++ {
			off = gdRun(t, bin, labels, pos.fen, shipped, 0, d, gdSaturated)
			if off.cycles >= gdControlCycles {
				break
			}
		}
		on := gdRun(t, bin, labels, pos.fen, shipped, defer2, d, gdSaturated)
		if !off.same(on) {
			t.Errorf("TREE DIVERGED %.24s\n  OFF %s\n  ON  %s", pos.fen, off, on)
			continue
		}
		if perPhase[pos.phase] == nil {
			perPhase[pos.phase] = &acc{}
		}
		perPhase[pos.phase].off += off.cycles
		perPhase[pos.phase].on += on.cycles
		total.off += off.cycles
		total.on += on.cycles
		t.Logf("%-8s d%-2d %-24s %11d -> %11d  %+.2f%%  (generate %d -> %d)",
			pos.phase, d, pos.fen[:24], off.cycles, on.cycles,
			-100*(float64(off.cycles)-float64(on.cycles))/float64(off.cycles),
			off.counts["generate"], on.counts["generate"])
	}
	for ph, a := range perPhase {
		t.Logf("PHASE %-8s saving %.2f%%", ph, 100*(float64(a.off)-float64(a.on))/float64(a.off))
	}
	if total.off > 0 {
		t.Logf("R5 PROFILE WORKLOAD TOTAL: off=%d on=%d  SAVING %.2f%% (prediction: +1.9..+2.5%%)",
			total.off, total.on, 100*(float64(total.off)-float64(total.on))/float64(total.off))
	}
}
