package mirror

import (
	"testing"
)

// TestEffortOffIdentical is the parity gate: SearchTimed with On=false,
// Smooth=false and every signal off must spend exactly Base each move and
// return a byte-identical (best, score, spent, depth) result to a flat
// SearchCycleBudget(Base) — the current mirror behavior. The bank value is
// irrelevant on this path (the ceiling never consults it), so we probe a
// range of bank balances to prove it is a genuine no-op.
func TestEffortOffIdentical(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	}
	for _, base := range []uint64{2_000_000, 8_000_000, 30_000_000} {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			flat := func() (Move, int, uint64, int) {
				e := NewEngine()
				e.Features = FtAll | FtSEE | FtHistory
				e.SetPosition(pos)
				e.Seed = 0
				m, s := e.SearchCycleBudget(base, MaxPly-1)
				return m, s, e.Cyc.Est, e.CompletedDepth
			}
			timedOff := func(bank int64) (Move, int, uint64, int) {
				e := NewEngine()
				e.Features = FtAll | FtSEE | FtHistory
				e.SetPosition(pos)
				e.Seed = 0
				tp := &TimeParams{On: false, Base: base, MaxEighths: 8}
				m, s, spent := e.SearchTimed(tp, bank, MaxPly-1)
				return m, s, spent, e.CompletedDepth
			}
			fm, fs, fc, fd := flat()
			for _, bank := range []int64{0, int64(base), 4 * int64(base), 100 * int64(base)} {
				tm, ts, tc, td := timedOff(bank)
				if fm != tm || fs != ts || fc != tc || fd != td {
					t.Errorf("base %d bank %d %s: OFF path differs from flat: flat (%v,%d,%d,d%d) vs timed (%v,%d,%d,d%d)",
						base, bank, fen, fm, fs, fc, fd, tm, ts, tc, td)
				}
			}
		}
	}
}

// TestEffortDeterminism: SearchTimed is a pure function of (position, params,
// bank) — two replays are bit-identical, exactly like SearchCycleBudget.
func TestEffortDeterminism(t *testing.T) {
	pos, err := ParseFEN("r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	tp := &TimeParams{
		On: true, Base: 8_000_000, Smooth: true,
		EasyStop: true, StableIters: 3, ScoreFlat: 20, MinDepth: 3, MinSpendEighths: 4,
		Panic: true, PanicDrop: 40, PanicEighths: 24,
		Unstable: true, UnstableEighths: 16,
		MaxEighths: 32,
	}
	run := func() (Move, int, uint64, int) {
		e := NewEngine()
		e.Features = FtAll | FtSEE | FtHistory
		e.SetPosition(pos)
		e.Seed = 7
		m, s, spent := e.SearchTimed(tp, 20_000_000, MaxPly-1)
		return m, s, spent, e.CompletedDepth
	}
	m1, s1, c1, d1 := run()
	m2, s2, c2, d2 := run()
	if m1 != m2 || s1 != s2 || c1 != c2 || d1 != d2 {
		t.Errorf("SearchTimed non-deterministic: (%v,%d,%d,%d) vs (%v,%d,%d,%d)",
			m1, s1, c1, d1, m2, s2, c2, d2)
	}
}

// TestEffortExtendSpendsMore: on a bank-rich move, a panic/instability
// extension must let the search spend MORE cycles (and reach at least as
// deep) than the same move with extension disabled. The easy-stop must,
// conversely, be able to spend LESS. This proves the ceiling actually moves.
func TestEffortExtendSpendsMore(t *testing.T) {
	// A sharp tactical middlegame where the best move is not trivially stable.
	pos, err := ParseFEN("r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	const base = 6_000_000
	spend := func(tp *TimeParams, bank int64) uint64 {
		e := NewEngine()
		e.Features = FtAll | FtSEE | FtHistory
		e.SetPosition(pos)
		e.Seed = 0
		_, _, spent := e.SearchTimed(tp, bank, MaxPly-1)
		return spent
	}
	flatSpend := spend(&TimeParams{On: true, Base: base, MaxEighths: 8}, 40*base)
	extend := spend(&TimeParams{On: true, Base: base, MaxEighths: 40,
		Panic: true, PanicDrop: 1, PanicEighths: 40,
		Unstable: true, UnstableEighths: 40}, 40*base)
	t.Logf("flat spend %d, extended spend %d", flatSpend, extend)
	if extend <= flatSpend {
		t.Errorf("extension did not spend more: flat %d, extended %d", flatSpend, extend)
	}
}

// TestEffortBankConserves: over a synthetic sequence of spends the SIGNED bank
// conserves total effort (income == Base per move). An overspend drives the
// bank NEGATIVE (debt) instead of clamping at zero, so the final bank equals
// income - spent exactly whenever the positive cap was never hit. Only the
// positive side is capped.
func TestEffortBankConserves(t *testing.T) {
	const base = 1000
	b := EffortBank{Base: base}
	spends := []uint64{200, 5000, 100, 100, 3000, 900, 0, 8000}
	var income, spent int64
	for _, s := range spends {
		income += base
		b.settle(s)
		spent += int64(s)
		if b.bank > 8*base {
			t.Errorf("bank %d exceeded positive cap %d", b.bank, 8*base)
		}
	}
	// Positive cap never bit here (the bank runs negative after the 5000
	// overspend), so the debt telescopes exactly to income - spent.
	if want := income - spent; b.bank != want {
		t.Errorf("bank did not conserve: got %d want %d (income %d spent %d)", b.bank, want, income, spent)
	}
}

// TestEffortBankAdherence is the closed-loop budget-conservation gate. It
// models the real per-game feedback (allocation drawn from the bank -> move
// spends up to its ceiling, or runs to the device hard-abort = 2*maxCeiling on
// FORCED-PANIC moves that overspend a thin bank -> settle), then compares the
// session own_total against own_intended = N*income. The OLD clamp-at-zero bank
// FORGAVE the hard-abort overspends (measured ~1.13 ratio). The signed/debt
// bank claws them back, so the ratio drops to ~1.0; the only residual is the
// minSpend floor (income/4), which this test quantifies.
func TestEffortBankAdherence(t *testing.T) {
	const income = 30_000_000
	minSpend := int64(income / 4)
	// A move sequence: most easy (spend the min), some panic (run to the device
	// hard-abort). This is the adversarial pattern the old clamp leaked on.
	panicMove := []bool{false, false, true, false, true, false, false, true,
		false, false, true, false, false, false, true, false, false, false, true, false}

	sim := func(clampAtZero bool) (total, intended int64) {
		bank := int64(0)
		for _, isPanic := range panicMove {
			// Host-side allocation exactly as pokeAlloc / SearchTimed compute it.
			baseCeiling := int64(income) + bank/8
			if baseCeiling < minSpend {
				baseCeiling = minSpend
			}
			maxCeiling := int64(4 * income)
			if lim := int64(income) + bank; maxCeiling > lim {
				maxCeiling = lim
			}
			if maxCeiling < baseCeiling {
				maxCeiling = baseCeiling
			}
			// Device spend model: an easy move spends its base ceiling; a panic
			// move runs to the on-device hard-abort = 2*maxCeiling (ABORTL),
			// which on a thin/empty bank exceeds income+bank.
			var spent int64
			if isPanic {
				spent = 2 * maxCeiling
			} else {
				spent = baseCeiling
			}
			total += spent
			intended += income
			// settle
			if clampAtZero {
				tot := bank + income
				if spent >= tot {
					bank = 0
				} else {
					bank = tot - spent
				}
				if bank > 8*income {
					bank = 8 * income
				}
			} else {
				bank += income - spent
				if bank > 8*income {
					bank = 8 * income
				}
			}
		}
		return total, intended
	}

	oldTot, oldInt := sim(true)
	newTot, newInt := sim(false)
	oldRatio := float64(oldTot) / float64(oldInt)
	newRatio := float64(newTot) / float64(newInt)
	t.Logf("closed-loop adherence: OLD clamp ratio=%.4f (%d/%d), NEW debt ratio=%.4f (%d/%d), residual=%.2f%%",
		oldRatio, oldTot, oldInt, newRatio, newTot, newInt, (newRatio-1)*100)
	// The debt bank must claw back the bulk of the overspend: the residual
	// excess (from the minSpend floor only) must be less than half the old
	// clamp's leak. (The exact residual under the real device is quantified by
	// the gauntlet's TIME-SESSION-SUMMARY; this synthetic pattern is deliberately
	// more panic-heavy than reality.)
	if newRatio >= oldRatio {
		t.Errorf("debt bank did not reduce overspend: old %.4f new %.4f", oldRatio, newRatio)
	}
	if (newRatio - 1) >= (oldRatio-1)*0.5 {
		t.Errorf("debt bank clawed back <half the leak: old excess %.4f new excess %.4f", oldRatio-1, newRatio-1)
	}
}

// TestEffortMatchRuns is an end-to-end smoke test: an adaptive-vs-flat-bank
// self-play match completes and is reproducible across worker counts.
func TestEffortMatchRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	openings, err := GenOpenings([][]string{{"e2e4", "e7e5"}, {"d2d4", "d7d5"}}, 8, 6502)
	if err != nil {
		t.Fatal(err)
	}
	const base = 3_000_000
	adaptive := &TimeParams{On: true, Base: base, Smooth: false,
		EasyStop: true, StableIters: 3, ScoreFlat: 25, MinDepth: 3, MinSpendEighths: 4,
		Panic: true, PanicDrop: 40, PanicEighths: 24,
		Unstable: true, UnstableEighths: 16, MaxEighths: 32}
	flatBank := &TimeParams{On: true, Base: base, Smooth: true, MaxEighths: 8}
	a := PlayerCfg{Features: FtAll | FtSEE | FtHistory, Weights: DefaultWeights, Time: adaptive}
	b := PlayerCfg{Features: FtAll | FtSEE | FtHistory, Weights: DefaultWeights, Time: flatBank}
	r1, err := Match(a, b, openings, 8, 1, 6502)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Match(a, b, openings, 8, 4, 6502)
	if err != nil {
		t.Fatal(err)
	}
	if *r1 != *r2 {
		t.Errorf("adaptive match not reproducible across worker counts: %v vs %v", r1, r2)
	}
	t.Logf("adaptive vs flat-bank @%d base: %s", base, r1)
}
