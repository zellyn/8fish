package chesstest

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zellyn/chess6502/harness"
)

// ---------------------------------------------------------------------------
// FT2_SOFTCLK — the ESTIMATED elapsed-cycle clock (asm/search.s checkclocks).
//
// WHY IT EXISTS. Every budget-mode number in docs/results.md was measured
// under a harness that supplies a cycle counter at CLOCK_TRAP ($BFF4). A real
// Apple IIe has no such thing: $BFF4 is plain RAM, and the machine has no
// readable clock at all. Without one the shipped engine cannot run budget
// mode, levels have to ship as raw plies, and FT2_ADAPT — validated and
// measured — cannot run at all. So the engine estimates elapsed cycles, the
// same concession Sargon III makes (docs/sargon.md).
//
// HOW IT IS TESTED. The harness has the clock the hardware does not, which is
// exactly what makes the estimator measurable: turn OFF the harness's $BFF4
// read trap (m.Mem.ClockAddr = 0) and $BFF4 becomes ordinary RAM — real
// hardware semantics — while m.Cycles still reports the TRUE emulated cycle
// count. Estimate and ground truth, on the same run.
//
// ★ WHICH OF THESE IS THE ACCEPTANCE GATE — READ THIS BEFORE TRUSTING A
// NUMBER FROM THIS FILE. Not TestSoftClockAccuracy. That test passed this
// feature at aggregate estimate/truth = 1.052 ("the engine over-reports
// elapsed time, so it stops early") while the SAME BUILD overran its clock by
// 17% in real games — the error was backwards in SIGN, and the +29 Elo the
// feature appeared to win was 26% more compute (docs/results.md 2026-07-27).
// The gate is now sprt.TestSoftClockAdherence: in-game adherence (own true
// cycles / own intended cycles) against an exact-clock control, measured the
// way cmd/sprt measures it. Everything here is a diagnostic.
//
//	TestSoftClockNoTreeEffect  GATE (still): fixed-depth trees are IDENTICAL
//	                           with the feature on and off, and the
//	                           estimator's own cycle cost as a percentage.
//	TestSoftClockCalibrate     DIAGNOSTIC ONLY, and the procedure that
//	                           produced the failure — see its own comment.
//	                           The shipped constants come from
//	                           cmd/softclkdiag -fit, over real games.
//	TestSoftClockAccuracy      DIAGNOSTIC: the error distribution over a
//	                           position pool. Useful for seeing HOW the error
//	                           is shaped; not evidence that the estimator
//	                           manages time correctly.
// ---------------------------------------------------------------------------

// shipFeatures / shipFeatures2 are the configuration ucibridge.runEngine
// plays with: FEATURES 0x5F (0x1F + FT_CKEXT) and FEATURES2 = FT2_GENDEFER.
// The estimator is calibrated and gated on THIS config, not on the
// 0x1F/0x07/0x00 calibration masks the mirror's cost model was fit at — the
// shipped mask's check extensions and deferred generation change the mix of
// work per node, and the estimator has to be right about the engine that
// actually ships.
func shipFeatures(t testing.TB) (byte, byte) {
	t.Helper()
	ck, gd := defs["FT_CKEXT"], defs["FT2_GENDEFER"]
	if ck == 0 || gd == 0 {
		t.Fatal("FT_CKEXT / FT2_GENDEFER missing from defs.inc")
	}
	return byte(ck) | 0x1F, byte(gd)
}

func softClockBit(t testing.TB) byte {
	t.Helper()
	b := defs["FT2_SOFTCLK"]
	if b == 0 {
		t.Fatal("FT2_SOFTCLK missing from defs.inc")
	}
	return byte(b)
}

// nPCost is the cost table's entry count (phase 0..24); checkclocks clamps
// PHASE into that range. Mirrors NPCOST in defs.inc, which ParseDefs cannot
// read because it is decimal.
const nPCost = 25

// costTable is the two label addresses of PCOSTLO/PCOSTHI in the loaded
// image, so a test can rewrite the per-phase cost table without rebuilding.
type costTable struct{ lo, hi uint16 }

func loadCostTable(t testing.TB, labels map[string]uint16) costTable {
	t.Helper()
	lo, hi := labels["PCOSTLO"], labels["PCOSTHI"]
	if lo == 0 || hi == 0 {
		t.Fatal("PCOSTLO/PCOSTHI missing from asm/engine.lbl")
	}
	if hi-lo != nPCost {
		t.Fatalf("cost table layout changed: PCOSTHI-PCOSTLO = %d, want %d", hi-lo, nPCost)
	}
	return costTable{lo, hi}
}

// poke writes a per-phase table of 128-node costs (in CLOCK_TRAP's 256-cycle
// units) over the image's built-in one.
func (c costTable) poke(m *harness.Machine, vals [nPCost]uint16) {
	for i, v := range vals {
		m.Mem.Main[c.lo+uint16(i)] = byte(v)
		m.Mem.Main[c.hi+uint16(i)] = byte(v >> 8)
	}
}

// read24 reads a 24-bit little-endian value from main RAM.
func read24(m *harness.Machine, addr uint16) uint64 {
	return uint64(m.Mem.Main[addr]) | uint64(m.Mem.Main[addr+1])<<8 |
		uint64(m.Mem.Main[addr+2])<<16
}

// softResult is one search run under the estimated clock.
type softResult struct {
	estimate uint64 // the engine's own elapsed-cycle estimate
	truth    uint64 // emulated 6502 cycles actually spent
	depth    int    // completed ID depth
	move     string
	score    int
	abort    bool
	polls    uint64 // raw CLOCK_TRAP total for probe runs
}

// runSoft runs one search with the estimated clock in HARDWARE mode: the
// harness's $BFF4 read trap is disabled, so the engine sees only what it
// wrote there itself. table, if non-nil, replaces the image's cost table
// (used by the calibration probes).
func runSoft(bin []byte, fen string, budget uint64, depth byte, feat, feat2 byte,
	softBit byte, ct *costTable, table *[nPCost]uint16) (softResult, error) {

	var r softResult
	pos, err := ParseFEN(fen)
	if err != nil {
		return r, err
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		return r, err
	}
	// Hardware semantics: $BFF4-$BFF6 is plain RAM. Without this the trap's
	// answer would win over the engine's accumulator on every read AND
	// checkclocks would accumulate onto the real counter instead of its own
	// running total, which is not a thing any IIe does.
	m.Mem.ClockAddr = 0
	SetFeatures(m, defs, feat)
	// Poke the budget with FT2_SOFTCLK still CLEAR, then arm it. SetBudget
	// divides by the soft-clock safety margin when the bit is set (that is
	// where the margin lives now — see chesstest.SoftClockMargin), and these
	// diagnostics want to control the engine's ACTUAL limit: "budget 4M" here
	// has to mean the estimate really stops at 4M, or the by-budget breakdown
	// is measuring a number nobody chose. The GAME path (sprt, ucibridge) goes
	// through the margin, which is what the adherence gate measures.
	SetFeatures2(m, defs, feat2)
	SetBudget(m, defs, budget, depth)
	SetFeatures2(m, defs, feat2|softBit)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	if table != nil {
		ct.poke(m, *table)
	}
	exited, code, err := m.Run(400_000_000_000)
	if err != nil {
		return r, err
	}
	if !exited {
		return r, fmt.Errorf("engine did not exit (fen %q)", fen)
	}
	if code > 2 {
		return r, fmt.Errorf("engine exit code %d (fen %q)", code, fen)
	}
	r.polls = read24(m, defs["CLOCK_TRAP"])
	r.estimate = r.polls << 8
	r.truth = m.Cycles
	r.depth = int(m.Mem.Main[defs["CURDEPTH"]])
	if budget != 0 && r.depth > int(depth) {
		r.depth = int(depth)
	}
	r.score = int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
	if m.Mem.Main[defs["BESTFROM"]] == byte(defs["NOSQ"]) {
		r.move = "none"
	} else {
		r.move = MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]],
			m.Mem.Main[defs["BESTFLAGS"]])
	}
	r.abort = m.Mem.Main[defs["ABORT"]] != 0
	return r, nil
}

// ---------------------------------------------------------------------------
// Gate 1: the estimator must not perturb the tree, and its cost must be small.
// ---------------------------------------------------------------------------

// TestSoftClockNoTreeEffect is the "it must not change the engine" gate.
//
// In FIXED-DEPTH mode the estimated clock is written and never read, so an
// A/B is a clean two-sided assertion:
//
//   - the SEARCH TREE must be bit-identical — same search/make/eval/attacked/
//     ttprobe/generate entry counts, same score, same move. If checkclocks
//     clobbered a live register (it takes X, which is dead at search's entry
//     — the only caller) this is what would catch it, and it would catch it
//     even though the clobber only happens on 1 node in 128.
//   - the CYCLE DELTA is the estimator's whole price, measured rather than
//     derived, and reported as a percentage.
//
// Feature OFF is not merely "equivalent" but the identical instruction
// stream: engine.s patches search's `ccsite: jsr checkclock` operand to
// `checkclocks` only when the bit is set. So the OFF arm of this test is also
// the assertion that shipping the feature dark costs exactly nothing.
func TestSoftClockNoTreeEffect(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: fixed-depth 6502 searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	softBit := softClockBit(t)
	feat, feat2 := shipFeatures(t)
	probes := []struct {
		name string
		addr uint16
	}{
		{"search", labels["search"]}, {"make", labels["make"]},
		{"eval", labels["eval"]}, {"attacked", labels["attacked"]},
		{"ttprobe", labels["ttprobe"]}, {"generate", labels["generate"]},
	}
	for _, p := range probes {
		if p.addr == 0 {
			t.Fatalf("probe %q missing from asm/engine.lbl", p.name)
		}
	}

	// A spread of phases: full middlegame down to a pawn ending (phase 0),
	// because the whole point of the phase term is that the per-node cost is
	// not constant across them.
	cases := []struct {
		fen   string
		depth byte
	}{
		{"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 6},
		{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 6},
		{"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 7},
		{"2r3k1/5pp1/p6p/1p2p3/4P3/1P3P2/P5PP/2R3K1 w - - 0 1", 8},
		{"4k3/pp3ppp/8/8/8/8/PP3PPP/4K3 w - - 0 1", 8},
		{"8/8/4k3/8/8/4K3/4P3/6r1 w - - 0 1", 8},
	}

	run := func(fen string, depth byte, on bool) (counts []uint64, cycles uint64, score int, move string) {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		bits := feat2
		if on {
			bits |= softBit
			m.Mem.ClockAddr = 0 // hardware semantics
		}
		SetFeatures(m, defs, feat)
		SetFeatures2(m, defs, bits)
		SetBudget(m, defs, 0, depth) // fixed depth: the estimate is inert
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		counts = make([]uint64, len(probes))
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, _ uint8) {
			for i := range probes {
				if pc == probes[i].addr {
					counts[i]++
				}
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("fen=%q on=%v exited=%v code=%d err=%v", fen, on, exited, code, err)
		}
		score = int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
		move = MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]],
			m.Mem.Main[defs["BESTFLAGS"]])
		return counts, m.Cycles, score, move
	}

	var totOff, totOn uint64
	for _, c := range cases {
		offC, offCyc, offSc, offMv := run(c.fen, c.depth, false)
		onC, onCyc, onSc, onMv := run(c.fen, c.depth, true)
		for i := range probes {
			if offC[i] != onC[i] {
				t.Errorf("%s d%d: %s count %d (off) != %d (on) — the estimator PERTURBED THE TREE",
					c.fen, c.depth, probes[i].name, offC[i], onC[i])
			}
		}
		if offSc != onSc || offMv != onMv {
			t.Errorf("%s d%d: score/move %d/%s (off) != %d/%s (on)",
				c.fen, c.depth, offSc, offMv, onSc, onMv)
		}
		totOff += offCyc
		totOn += onCyc
		t.Logf("%-42s d%-2d nodes=%-9d off=%-11d on=%-11d cost=%+.4f%%",
			c.fen[:min(42, len(c.fen))], c.depth, offC[0], offCyc, onCyc,
			100*float64(int64(onCyc)-int64(offCyc))/float64(offCyc))
	}
	cost := 100 * float64(int64(totOn)-int64(totOff)) / float64(totOff)
	t.Logf("ESTIMATOR COST: %d -> %d cycles = %+.4f%% overall", totOff, totOn, cost)
	// 32 cycles per 128 nodes against ~3.3k cycles/node is 0.0076%; a
	// tenfold margin catches an accidental per-NODE (rather than per-poll)
	// cost without failing on emulator noise (there is none: the run is
	// deterministic).
	if cost > 0.1 {
		t.Errorf("estimator costs %.4f%%, want <= 0.1%%", cost)
	}
	if cost < 0 {
		t.Errorf("estimator cost is negative (%.4f%%): the A/B is not measuring what it thinks", cost)
	}
}

// TestSoftClockMarginEquivalence is the proof that the safety margin can live
// on the BUDGET instead of in the cost table — the change that let the margin
// become a function of the level length at zero engine cost.
//
// The claim is algebraic: every use of the estimate is `estimate >= limit`,
// the estimate is a plain sum of cost-table entries, so
//
//	m * raw_estimate >= limit    <=>    raw_estimate >= limit / m
//
// and the two must produce not merely similar timing but the IDENTICAL SEARCH.
// This runs both arms and demands exactly that: same nodes, same makes, same
// evals, same completed depth, same move, same score.
//
// m = 2 is used because it makes both sides exact in integer arithmetic (the
// doubled table entries and the halved budget are both representable), so a
// failure here is a real disagreement and not a rounding artifact. The shipped
// margins (127/113/100%) are not exact in that sense and are worth a fraction
// of a percent of drift; that is priced and accepted, and it is why this test
// pins the PRINCIPLE rather than the shipped constants.
func TestSoftClockMarginEquivalence(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: budgeted 6502 searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	ct := loadCostTable(t, labels)
	softBit := softClockBit(t)
	feat, feat2 := shipFeatures(t)

	// The image's own table, read back so the doubled arm is exactly 2x it.
	var raw, doubled [nPCost]uint16
	m0, err := NewMachine(bin, defs, mustFEN(t, "8/8/8/8/8/8/8/K6k w - - 0 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		raw[i] = uint16(m0.Mem.Main[ct.lo+uint16(i)]) | uint16(m0.Mem.Main[ct.hi+uint16(i)])<<8
		doubled[i] = 2 * raw[i]
	}

	cases := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"2r3k1/5pp1/p6p/1p2p3/4P3/1P3P2/P5PP/2R3K1 w - - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	}
	const budget = 8_000_000 // even, so budget/2 is exact

	for _, fen := range cases {
		// Arm A: raw table, HALVED budget — what SetBudget now does.
		a, err := runSoft(bin, fen, budget/2, 24, feat, feat2, softBit, &ct, &raw)
		if err != nil {
			t.Fatal(err)
		}
		// Arm B: DOUBLED table, full budget — what folding the margin in did.
		b, err := runSoft(bin, fen, budget, 24, feat, feat2, softBit, &ct, &doubled)
		if err != nil {
			t.Fatal(err)
		}
		if a.depth != b.depth || a.move != b.move || a.score != b.score {
			t.Errorf("%s: budget/2 gave d%d %s %d, table*2 gave d%d %s %d — "+
				"scaling the budget is NOT equivalent to scaling the table",
				fen, a.depth, a.move, a.score, b.depth, b.move, b.score)
		}
		// The estimate itself must come out exactly doubled: same tree, same
		// polls, entries twice as big.
		if b.estimate != 2*a.estimate {
			t.Errorf("%s: estimate %d (raw) vs %d (doubled), want exactly 2x — "+
				"the two arms searched different trees",
				fen, a.estimate, b.estimate)
		}
		if a.truth != b.truth {
			t.Errorf("%s: true cycles %d vs %d — the trees differ", fen, a.truth, b.truth)
		}
	}
}

// TestSoftClockMarginRule pins the margin rule itself: the octave breakpoints
// the on-device UI has to reimplement. If these move, docs/ui-design.md and
// the comment in chesstest.go move with them.
func TestSoftClockMarginRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cycles uint64
		want   uint64
	}{
		{"1 s", 1_020_000, 127},
		{"4 s", 4_080_000, 127},
		{"8 s", 8_160_000, 113},
		{"15 s", 15_300_000, 100},
		{"30 s", 30_600_000, 100},
		{"60 s", 61_200_000, 100},
	} {
		if got := SoftClockMargin(tc.cycles); got != tc.want {
			t.Errorf("SoftClockMargin(%s = %d) = %d, want %d", tc.name, tc.cycles, got, tc.want)
		}
	}
	// Monotone non-increasing: a longer level must never be charged a LARGER
	// margin than a shorter one, or the adaptive ceilings stop nesting.
	prev := uint64(1000)
	for c := uint64(1 << 16); c < 1<<32; c <<= 1 {
		got := SoftClockMargin(c)
		if got > prev {
			t.Errorf("margin rose from %d to %d at %d cycles: not monotone", prev, got, c)
		}
		prev = got
	}
}

func mustFEN(t testing.TB, fen string) *Position {
	t.Helper()
	p, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ---------------------------------------------------------------------------
// Calibration.
// ---------------------------------------------------------------------------

// softFit is a relative-least-squares fit of true cycles onto the two things
// the on-device estimator can actually observe: the number of clock POLLS
// (one per 128 nodes) and the sum of the node PHASE sampled at those polls.
//
// Relative rather than absolute error, for the same reason the mirror's cost
// model is fit that way (internal/mirror/cycles.go): the searches span two
// orders of magnitude in size, so an unweighted fit would let the largest
// searches set both coefficients and would happily be 40% wrong on every
// small one. A budget is spent proportionally, so proportional error is the
// loss that matters.
type softFit struct{ perPoll, perPhase float64 }

func fitSoft(polls, phase, cyc []float64) softFit {
	// Normal equations for min sum_i ((a*p_i + b*f_i)/y_i - 1)^2.
	var saa, sab, sbb, sa, sb float64
	for i := range cyc {
		p, f, y := polls[i]/cyc[i], phase[i]/cyc[i], 1.0
		saa += p * p
		sab += p * f
		sbb += f * f
		sa += p * y
		sb += f * y
	}
	det := saa*sbb - sab*sab
	if det == 0 {
		return softFit{}
	}
	return softFit{
		perPoll:  (sa*sbb - sb*sab) / det,
		perPhase: (sb*saa - sa*sab) / det,
	}
}

// TestSoftClockCalibrate is a FIXED-DEPTH, POOL-POSITION diagnostic, and it
// is retained as the record of a procedure that failed — do NOT paste its
// output into defs.inc.
//
// It fit SOFTA/SOFTB over 23 hand-built calibration positions, each searched
// once at a fixed depth from a cold TT. Those positions are quiet,
// near-balanced and mostly full-material, and they make ~1.21 moves per
// search node. Positions the engine actually reaches in play make ~1.58 and
// cost ~18% more per node AT THE SAME TAPER PHASE — which is the one thing
// the estimator's single regressor cannot see. The result was a cost model
// ~24% too cheap in games, an engine that overran its clock by 17%, and a
// pool gate that reported the bias with the wrong sign.
//
// The shipped constants are now fit by cmd/softclkdiag -fit, over moves from
// real self-play games (warm TT, real per-move allocation), against the exact
// vector of cost-table lookups each move made. This test is still useful for
// one thing: it is the cleanest way to see the per-node cost of a SINGLE
// iteration at a known depth, with no iterative-deepening or budget-abort
// structure on top.
//
// It does NOT reuse the mirror's fitted NodePhase coefficient directly. That
// coefficient is one term of a five-regressor model (node, node*phase, make,
// eval, ttprobe) fit at masks 0x1F/0x07/0x00; the on-device estimator has
// only two of those columns and runs the shipped 0x5F + FT2_GENDEFER. What
// transfers is the FORM (per-node cost linear in taper phase) and the choice
// of phase as the regressor — the values have to be refit against what the
// engine can see.
//
// The measurement trick: the engine's own accumulator is the instrument. Run
// each position twice with the cost table overwritten —
//
//	all entries 1 : CLOCK_TRAP ends at the POLL COUNT
//	entry[p] = p  : CLOCK_TRAP ends at the SAMPLED PHASE SUM
//
// — so the regressors are measured exactly as the runtime forms them,
// sampling error and 128-node quantization included, with no probe overhead
// and no second model to keep in sync.
//
// Fixed depth, so the tree cannot depend on the table being poked.
func TestSoftClockCalibrate(t *testing.T) {
	if testing.Short() {
		t.Skip("calibration: run explicitly")
	}
	if os.Getenv("SOFTCLK_CALIB") == "" {
		t.Skip("calibration: set SOFTCLK_CALIB=1 to run (slow)")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	ct := loadCostTable(t, labels)
	softBit := softClockBit(t)
	feat, feat2 := shipFeatures(t)

	var ones, ramp [nPCost]uint16
	for i := range ones {
		ones[i] = 1
		ramp[i] = uint16(i)
	}

	rows := make([]calibRow, len(calibFens))
	var wg sync.WaitGroup
	ch := make(chan int)
	var mu sync.Mutex
	var errs []string
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range ch {
				cf := calibFens[i]
				// The 0x1F-tier depth; the shipped mask is 0x1F plus check
				// extensions, so the tree sizes land in the same range.
				d := calibTiers[0].depth + cf.bonus[0]
				a, err := runSoft(bin, cf.fen, 0, d, feat, feat2, softBit, &ct, &ones)
				if err != nil {
					mu.Lock()
					errs = append(errs, err.Error())
					mu.Unlock()
					continue
				}
				b, err := runSoft(bin, cf.fen, 0, d, feat, feat2, softBit, &ct, &ramp)
				if err != nil {
					mu.Lock()
					errs = append(errs, err.Error())
					mu.Unlock()
					continue
				}
				rows[i] = calibRow{cf.fen, d, float64(a.polls), float64(b.polls), float64(a.truth)}
			}
		}()
	}
	for i := range calibFens {
		ch <- i
	}
	close(ch)
	wg.Wait()
	for _, e := range errs {
		t.Error(e)
	}
	if t.Failed() {
		return
	}

	var polls, phase, cyc []float64
	for _, r := range rows {
		if r.cyc == 0 {
			continue
		}
		polls = append(polls, r.polls)
		phase = append(phase, r.phase)
		cyc = append(cyc, r.cyc)
	}
	f := fitSoft(polls, phase, cyc)
	// Per-NODE form: a poll covers 128 nodes.
	a, b := f.perPoll/128, f.perPhase/128
	const softScale = 16
	t.Logf("FIT: %.1f + %.2f*phase cycles/node  ->  SOFTA = %d, SOFTB = %d (SOFTSCALE %d)",
		a, b, int(math.Round(a*softScale)), int(math.Round(b*softScale)), softScale)

	// In-sample residuals, worst first.
	type res struct {
		err float64
		r   calibRow
	}
	var rs []res
	var sq, sumA, sumP float64
	for _, r := range rows {
		if r.cyc == 0 {
			continue
		}
		p := f.perPoll*r.polls + f.perPhase*r.phase
		e := 100 * (p - r.cyc) / r.cyc
		rs = append(rs, res{e, r})
		sq += (e / 100) * (e / 100)
		sumA += r.cyc
		sumP += p
	}
	sort.Slice(rs, func(i, j int) bool { return math.Abs(rs[i].err) > math.Abs(rs[j].err) })
	for _, x := range rs {
		t.Logf("  %+6.1f%%  d%-2d %-44s meanphase=%4.1f cyc=%12.0f",
			x.err, x.r.depth, x.r.fen[:min(44, len(x.r.fen))],
			x.r.phase/math.Max(x.r.polls, 1), x.r.cyc)
	}
	t.Logf("in-sample: RMS %.1f%%  worst %.1f%%  pool actual/pred %.3f",
		100*math.Sqrt(sq/float64(len(rs))), math.Abs(rs[0].err), sumA/sumP)
}

// calibRow is one calibration position: the two probe-measured regressors
// (poll count and sampled phase sum) against the true cycle total.
type calibRow struct {
	fen               string
	depth             byte
	polls, phase, cyc float64
}

// ---------------------------------------------------------------------------
// Gate 2: the error distribution, which is the whole point.
// ---------------------------------------------------------------------------

// softAcc is one budget-mode search's estimate-vs-truth comparison.
type softAcc struct {
	fen      string
	budget   uint64
	phase    int
	estimate uint64
	truth    uint64
	depth    int
	abort    bool
}

func (s softAcc) relErr() float64 {
	return 100 * (float64(s.estimate) - float64(s.truth)) / float64(s.truth)
}

// absErr is the signed error in cycles.
func (s softAcc) absErr() float64 { return float64(s.estimate) - float64(s.truth) }

// softQuantum is the estimator's RESOLUTION, in cycles: one poll charges 128
// nodes at once, so no search can be estimated to better than a single table
// entry. This is not model error, it is the sampling period, and it is what
// dominates every short search: a mate-in-1 that really costs 0.07 s is
// charged one whole quantum and reads "+547%", which is a true statement about
// a meaningless quantity.
//
// So the relative-error distribution is reported over the searches a LEVEL is
// actually made of — those costing at least softResolvable — while everything
// below it is scored in ABSOLUTE terms, where the honest claim (and the
// assertion) is that it is bounded by about one quantum.
//
// The quantum is DERIVED from defs.inc rather than hardcoded, because it moves
// with every recalibration and a hardcoded copy silently went stale once
// already. chesstest.ParseDefs cannot supply it: the estimator's constants are
// decimal magnitudes, not $hex addresses, so it skips them.
func softQuanta(t testing.TB) (lo, hi uint64) {
	t.Helper()
	c := softCostConsts(t)
	return uint64(128 * c(0)), uint64(128 * c(nPCost-1))
}

// softCostConsts returns cost(phase) in cycles/node, exactly as search.s builds
// PCOSTLO/PCOSTHI at assembly time from defs.inc.
func softCostConsts(t testing.TB) func(p int) int {
	t.Helper()
	src, err := os.ReadFile("../../asm/defs.inc")
	if err != nil {
		t.Fatal(err)
	}
	v := map[string]int{}
	re := regexp.MustCompile(`(?m)^(SOFT[A-Z]+)\s*=\s*(-?\d+)`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("bad %s in defs.inc: %v", m[1], err)
		}
		v[m[1]] = n
	}
	for _, k := range []string{"SOFTA", "SOFTB", "SOFTC", "SOFTK", "SOFTSCALE", "SOFTMARGIN"} {
		if _, ok := v[k]; !ok {
			t.Fatalf("%s missing from asm/defs.inc", k)
		}
	}
	return func(p int) int {
		raw := v["SOFTA"] + v["SOFTB"]*min(p, v["SOFTK"]) + v["SOFTC"]*max(p-v["SOFTK"], 0)
		return raw * v["SOFTMARGIN"] / (100 * v["SOFTSCALE"])
	}
}

// softResolvable is the true-cost floor for relative-error reporting: 2M
// cycles, ~2 seconds, ~5 quanta. Below this the ±1-quantum resolution alone
// permits ±20% no matter how good the cost model is.
const softResolvable = 2_000_000

// TestSoftClockAccuracy asks how wrong the engine is about how long it has
// been thinking, over a pool of positions.
//
// ★ IT IS NOT THE GATE, AND IT MUST NOT BE USED AS ONE. It was, and it
// certified a build whose error ran the other way. Two independent reasons:
//
//  1. WRONG POSITIONS. `starts` is 40 opening-pool starts plus 31 curated
//     endgames/tacticals, all with the halfmove clock forced to 0. 47 of the
//     71 sit at taper phase 20-24 — exactly the full-material shape the cost
//     model was fit on, and the one place it is accurate. Games are played
//     somewhere else: the mean SAMPLED phase over real game moves is ~10, and
//     at any given phase a game position costs ~18% more per node than a pool
//     one (makes per node 1.58 vs 1.21).
//
//  2. WRONG QUANTITY. Even over the right positions, aggregate estimate/truth
//     does not predict time management, because the estimate is consumed by a
//     THRESHOLD: idloop starts another iteration only if now + 2*cost(last)
//     fits the budget. Symmetric clock noise there produces ASYMMETRIC spend
//     — an extra iteration costs 2-6x what stopping early saves. Measured: a
//     table with in-game estimate/truth 0.99 still overran its budget by 12%,
//     taking an extra iteration on 86 of 525 paired positions against 16 that
//     took one fewer.
//
// The gate is sprt.TestSoftClockAdherence. What this test is still good for
// is SHAPE: which budgets, phases and depths the estimate is worst at, which
// is how the game-condition refit found its knee.
//
// The cost table is the RAW measured cost per node — the deliberate safety
// bias lives on the BUDGET now (chesstest.SoftClockMargin), not in the table —
// so the pool ratio here should sit near 1. It is a check on the cost MODEL,
// not on time management.
//
// BUDGET mode, shipped configuration, harness clock trap OFF — so the engine
// runs exactly as it would on a IIe, stopping itself on its own estimate —
// while m.Cycles reports what it really spent. The comparison is therefore
// CLOSED-LOOP: the estimate is not scored against a tree someone else chose,
// it is scored against the tree the estimate itself produced.
//
// Reported (and asserted) as a DISTRIBUTION, not a mean. A mean-accurate
// estimator that is 60% wrong on endgames would make one level mean two
// different things, so the breakdowns by phase bucket and by budget (= level
// length) carry the verdict, and the worst single search is always printed.
//
// The pool-total ratio matters separately and is the number that governs GAME
// length: the banked clock (BankedClock) settles every move in ESTIMATED
// units, so total game time telescopes to sum(income) in estimated cycles and
// the real-time drift over a game is the estimator's mean bias, not its
// worst-case single-search error.
//
// Knobs (env): SOFTCLK_STARTS caps the position count, SOFTCLK_DUMP logs
// every row.
func TestSoftClockAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: budgeted 6502 searches")
	}
	bin := loadEngine(t)
	softBit := softClockBit(t)
	feat, feat2 := shipFeatures(t)

	// Level lengths, in emulated cycles. The IIe runs ~1.02M cycles/second,
	// so these are roughly 1s / 4s / 15s / 60s per move — the span a level
	// menu would offer.
	budgets := []uint64{1_000_000, 4_000_000, 15_000_000, 60_000_000}
	const depthCap = 20

	starts := append(loadPoolStarts(t), parityExtraStarts...)
	if n := envInt("SOFTCLK_STARTS", 0); n > 0 && n < len(starts) {
		starts = starts[:n]
	}

	type job struct {
		fen    string
		budget uint64
	}
	var jobs []job
	for _, b := range budgets {
		for _, f := range starts {
			jobs = append(jobs, job{f, b})
		}
	}

	var mu sync.Mutex
	var all []softAcc
	var errs []string
	var wg sync.WaitGroup
	ch := make(chan job)
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				r, err := runSoft(bin, j.fen, j.budget, depthCap, feat, feat2, softBit, nil, nil)
				if err != nil {
					mu.Lock()
					errs = append(errs, err.Error())
					mu.Unlock()
					continue
				}
				mu.Lock()
				all = append(all, softAcc{
					fen: j.fen, budget: j.budget, phase: fenPhase(j.fen),
					estimate: r.estimate, truth: r.truth, depth: r.depth, abort: r.abort,
				})
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()
	for _, e := range errs {
		t.Error(e)
	}
	if len(all) == 0 {
		t.Fatal("no results")
	}

	sort.Slice(all, func(i, k int) bool {
		if all[i].budget != all[k].budget {
			return all[i].budget < all[k].budget
		}
		return all[i].fen < all[k].fen
	})
	if os.Getenv("SOFTCLK_DUMP") != "" {
		for _, a := range all {
			t.Logf("DUMP\t%d\t%d\t%d\t%d\t%+.1f\t%d\t%v\t%s",
				a.budget, a.phase, a.estimate, a.truth, a.relErr(), a.depth, a.abort, a.fen)
		}
	}

	// Split at the estimator's resolution. Everything at or above
	// softResolvable is scored in relative terms; everything below it is
	// scored in absolute ones, because down there the ±1-quantum sampling
	// period IS the error and a percentage says nothing.
	var res, sub []softAcc
	for _, a := range all {
		if a.truth >= softResolvable {
			res = append(res, a)
		} else {
			sub = append(sub, a)
		}
	}

	report := func(label string, rows []softAcc) softStats {
		s := summarize(rows)
		t.Logf("%-26s n=%-4d bias %+6.1f%%  RMS %5.1f%%  p10 %+6.1f%%  p50 %+6.1f%%  p90 %+6.1f%%  WORST %+6.1f%%  pool est/true %.3f",
			label, s.n, s.mean, s.rms, s.p10, s.p50, s.p90, s.worst, s.poolRatio)
		return s
	}
	pick := func(f func(softAcc) bool) []softAcc {
		var out []softAcc
		for _, a := range res {
			if f(a) {
				out = append(out, a)
			}
		}
		return out
	}

	quantumMin, quantumMax := softQuanta(t)
	t.Logf("resolution: one poll = 128 nodes = %d..%d cycles (%.2f..%.2f s, phase 0..24); "+
		"%d of %d searches cost < %d cycles and are scored in absolute terms below",
		quantumMin, quantumMax, float64(quantumMin)/1_020_500,
		float64(quantumMax)/1_020_500, len(sub), len(all), softResolvable)

	t.Log("--- resolvable searches (true cost >= 2M cycles ~ 2 s) ---")
	report("all resolvable", res)
	t.Log("--- by level length (budget) ---")
	for _, b := range budgets {
		rows := pick(func(a softAcc) bool { return a.budget == b })
		if len(rows) == 0 {
			continue
		}
		report(fmt.Sprintf("budget %d (~%.0fs)", b, float64(b)/1_020_500), rows)
	}
	t.Log("--- by root phase (0 = pawn ending, 24 = full middlegame) ---")
	buckets := []struct {
		name   string
		lo, hi int
	}{
		{"phase 0-3 (pawn/minor)", 0, 3},
		{"phase 4-7", 4, 7},
		{"phase 8-13", 8, 13},
		{"phase 14-19", 14, 19},
		{"phase 20-24 (middlegame)", 20, 24},
	}
	for _, bk := range buckets {
		rows := pick(func(a softAcc) bool { return a.phase >= bk.lo && a.phase <= bk.hi })
		if len(rows) == 0 {
			continue
		}
		report(bk.name, rows)
	}

	// Worst offenders among the resolvable searches.
	byErr := append([]softAcc(nil), res...)
	sort.Slice(byErr, func(i, k int) bool { return math.Abs(byErr[i].relErr()) > math.Abs(byErr[k].relErr()) })
	t.Log("--- worst 12 resolvable searches ---")
	for _, a := range byErr[:min(12, len(byErr))] {
		t.Logf("  %+6.1f%%  ph%-2d budget=%-9d est=%-10d true=%-10d d%-2d %s",
			a.relErr(), a.phase, a.budget, a.estimate, a.truth, a.depth, a.fen)
	}

	// Sub-resolution searches, in quanta.
	var worstSub float64
	var worstSubRow softAcc
	for _, a := range sub {
		if math.Abs(a.absErr()) > math.Abs(worstSub) {
			worstSub, worstSubRow = a.absErr(), a
		}
	}
	if len(sub) > 0 {
		t.Logf("--- %d sub-resolution searches: worst absolute error %+.0f cycles (%+.2f max-quanta, %+.2f s) on %s",
			len(sub), worstSub, worstSub/float64(quantumMax), worstSub/1_020_500, worstSubRow.fen)
	}

	// Whole-pool ratio over EVERY search including the sub-resolution ones:
	// this is the number that governs GAME length, because the bank settles
	// each move in estimated units.
	poolAll := summarize(all).poolRatio
	t.Logf("POOL estimate/truth over all %d searches: %.3f", len(all), poolAll)

	// ---- assertions ----
	//
	// TWO-SIDED AND LOOSE. The safety margin no longer lives in the cost table
	// — it is applied to the BUDGET at move setup (chesstest.SoftClockMargin),
	// so the table is the raw measured cost and the estimator is supposed to
	// be HONEST here, not biased. This checks that it is roughly so on the
	// easiest position set it will ever see, and nothing more: whether the
	// engine manages a clock safely is asserted by sprt.TestSoftClockAdherence
	// over real games, which is the only place it can be asserted.
	//
	// Read a failure here as "the cost model drifted", not "time management
	// broke" — and note the band is wide because the pool is unrepresentative
	// by construction (see the header): it is 47/71 full-material openings,
	// which is where the model is most accurate, so this is a floor on the
	// error rather than an estimate of it.
	if poolAll < 0.85 || poolAll > 1.25 {
		t.Errorf("pool estimate/truth %.3f outside [0.85, 1.25]: the raw cost model "+
			"has drifted — refit with cmd/softclkdiag -fit over real games "+
			"(the safety margin is NOT in this number; it is on the budget)", poolAll)
	}
	// Sub-resolution searches cannot be estimated at all; the claim is only
	// that they land within one sampling period of what the calibration says
	// they should. This is what would catch the entry pre-charge being missing
	// (the estimate falls a whole quantum short and reads a flat zero on the
	// shortest searches) or doubled.
	//
	// It is measured against poolAll * truth rather than truth so the bound
	// survives whatever aggregate bias the cost model happens to carry; a
	// bound written around "estimate == truth" failed on a correct build once
	// already, when the safety margin was still folded into the table.
	var worstDev float64
	var worstDevRow softAcc
	for _, a := range sub {
		d := float64(a.estimate) - poolAll*float64(a.truth)
		if math.Abs(d) > math.Abs(worstDev) {
			worstDev, worstDevRow = d, a
		}
	}
	devBound := float64(quantumMax) + 0.30*poolAll*float64(worstDevRow.truth)
	if math.Abs(worstDev) > devBound {
		t.Errorf("sub-resolution search deviates %+.0f cycles from the calibration's own "+
			"expectation (%s): more than one sampling period (%d) plus 30%% model error — "+
			"suspect the entry prime",
			worstDev, worstDevRow.fen, quantumMax)
	}
	// Per-phase bias is the failure mode that would make a level mean
	// different things in different parts of a game — exactly the defect the
	// mirror's constant-per-node cost had before the phase term went in. It is
	// checked RELATIVE to the overall pool ratio, not against 1.0, so the
	// deliberate global margin neither masks it nor trips it.
	for _, bk := range buckets {
		rows := pick(func(a softAcc) bool { return a.phase >= bk.lo && a.phase <= bk.hi })
		if len(rows) < 10 {
			continue
		}
		s := summarize(rows)
		if r := s.poolRatio / poolAll; r < 0.75 || r > 1.30 {
			t.Errorf("%s: estimate/truth %.3f is %.2fx the pool's %.3f — the estimate is phase-biased",
				bk.name, s.poolRatio, r, poolAll)
		}
	}
}

type softStats struct {
	n                     int
	mean, rms, worst      float64
	p10, p50, p90         float64
	poolRatio             float64
	sumEstimate, sumTruth uint64
}

func summarize(rows []softAcc) softStats {
	s := softStats{n: len(rows)}
	if s.n == 0 {
		return s
	}
	errs := make([]float64, 0, len(rows))
	var sum, sq float64
	for _, r := range rows {
		e := r.relErr()
		errs = append(errs, e)
		sum += e
		sq += (e / 100) * (e / 100)
		if math.Abs(e) > math.Abs(s.worst) {
			s.worst = e
		}
		s.sumEstimate += r.estimate
		s.sumTruth += r.truth
	}
	sort.Float64s(errs)
	s.mean = sum / float64(s.n)
	s.rms = 100 * math.Sqrt(sq/float64(s.n))
	pick := func(q float64) float64 {
		i := int(q * float64(s.n-1))
		return errs[i]
	}
	s.p10, s.p50, s.p90 = pick(0.10), pick(0.50), pick(0.90)
	s.poolRatio = float64(s.sumEstimate) / float64(s.sumTruth)
	return s
}

// fenPhase computes the engine's taper phase (N=B=1, R=2, Q=4, both sides)
// from a FEN's board field — the same quantity the engine keeps in PHASE.
func fenPhase(fen string) int {
	board, _, _ := strings.Cut(fen, " ")
	ph := 0
	for _, c := range board {
		switch c {
		case 'n', 'N', 'b', 'B':
			ph++
		case 'r', 'R':
			ph += 2
		case 'q', 'Q':
			ph += 4
		}
	}
	return ph
}
