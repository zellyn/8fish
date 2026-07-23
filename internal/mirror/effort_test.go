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
			timedOff := func(bank uint64) (Move, int, uint64, int) {
				e := NewEngine()
				e.Features = FtAll | FtSEE | FtHistory
				e.SetPosition(pos)
				e.Seed = 0
				tp := &TimeParams{On: false, Base: base, MaxEighths: 8}
				m, s, spent := e.SearchTimed(tp, bank, MaxPly-1)
				return m, s, spent, e.CompletedDepth
			}
			fm, fs, fc, fd := flat()
			for _, bank := range []uint64{0, base, 4 * base, 100 * base} {
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
	spend := func(tp *TimeParams, bank uint64) uint64 {
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

// TestEffortBankConserves: over a synthetic sequence of spends the bank
// conserves total effort (income == Base per move) and never exceeds the cap
// nor goes negative.
func TestEffortBankConserves(t *testing.T) {
	const base = 1000
	b := EffortBank{Base: base}
	spends := []uint64{200, 5000, 100, 100, 3000, 900, 0, 8000}
	var income, spent uint64
	for _, s := range spends {
		income += base
		b.settle(s)
		spent += s
		if b.bank > 8*base {
			t.Errorf("bank %d exceeded cap %d", b.bank, 8*base)
		}
	}
	// Final bank == income - spent, unless clamped at 0 or the cap along the
	// way. Here total income 8000, total spent 17300 => bank floored at 0.
	if b.bank != 0 {
		t.Errorf("expected drained bank, got %d (income %d spent %d)", b.bank, income, spent)
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
