package mirror

import (
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"testing"
)

// TestMidBlindGap is SCREEN A at corpus scale. The 5 diagnosis FENs are the
// extreme tail of their buckets (blindGap 432..1860 vs a bucket median of
// 301), so a 5-position probe cannot say whether a term helps the bucket.
// This harvests MIDDLEGAME quiet positions from self-play and reproduces the
// diagnosis's instrument on each:
//
//	gap = shallow score at the operating budget (143M cycles)
//	    − oracle score at 5x that budget, TERMS OFF (the neutral reference)
//
// A positive gap is over-rating (we think we are better than a deeper search
// does). The headline is the HORIZON-BLIND subset — positions where the OFF
// gap is >= 200, the same threshold the diagnosis used to separate the
// king-safety bucket (median 301, 15/17 >= 200) from the positional one
// (median 59, 0/68 >= 200) — and whether a term shrinks the gap there.
//
// Comparing an ON shallow score against an OFF oracle is deliberate: the
// terms are exactly colour-antisymmetric (TestMidSanity), so they add no
// systematic offset across a balanced corpus, and any noise they add counts
// against them.
func TestMidBlindGap(t *testing.T) {
	if testing.Short() {
		t.Skip("blind-gap probe is a measurement, not a unit test")
	}
	const budget = 143_000_000
	games := envInt("MIDBLIND_GAMES", 40)
	maxPos := envInt("MIDBLIND_POS", 400)

	// Harvest quiet middlegame positions from asm-matched self-play.
	base := PlayerCfg{Features: FtAll, Weights: DefaultWeights, QS: DefaultQS, Depth: 6}
	rnd := rand.New(rand.NewPCG(0x8F15, 0xB11D))
	lines := comboOpenings(t, games, 0x5EED)
	var fens []string
	for i := 0; i < games && len(fens) < maxPos; i++ {
		rec, err := PlayGame(base, base, lines[i%len(lines)], rnd, true)
		if err != nil {
			t.Fatal(err)
		}
		fens = append(fens, rec.QuietFENs...)
	}
	if len(fens) > maxPos {
		fens = fens[:maxPos]
	}

	groups := []struct {
		name string
		p    MidParams
	}{
		{"OFF", MidParams{}},
		{"ks", MidKingSafety},
		{"pos", MidPositional},
		{"both", DefaultMid},
	}
	gaps := make([][]int, len(groups))
	var kept int
	var blindIdx []int // indices (into the kept sequence) of the OFF>=200 subset
	for _, fen := range fens {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		probe := NewEngine()
		probe.SetPosition(pos)
		if probe.Pos.Phase < DefaultMid.PhaseMin {
			continue // endgame: not this term set's business
		}
		or := NewEngine()
		or.SetPosition(pos)
		or.Seed = 0
		_, oracle := or.SearchCycleBudget(5*budget, MaxPly-1)
		for gi, g := range groups {
			e := NewEngine()
			e.Mid = g.p
			e.SetPosition(pos)
			e.Seed = 0
			_, sc := e.SearchCycleBudget(budget, MaxPly-1)
			gaps[gi] = append(gaps[gi], sc-oracle)
		}
		if abs(gaps[0][kept]) >= 200 {
			blindIdx = append(blindIdx, kept)
		}
		kept++
	}
	t.Logf("harvested %d quiet FENs, %d middlegame (phase >= %d); horizon-blind subset (|OFF gap| >= 200): %d",
		len(fens), kept, DefaultMid.PhaseMin, len(blindIdx))
	if kept == 0 {
		t.Fatal("no middlegame positions harvested")
	}

	t.Logf("%-5s | %8s %8s %8s | %8s %8s | %s", "group",
		"med|gap|", "mean|gap|", "meanGap", "blindMed", "blindMean", "better/worse vs OFF (all)")
	for gi, g := range groups {
		med, mean, signed := medAbs(gaps[gi]), meanAbs(gaps[gi]), meanSigned(gaps[gi])
		var bl []int
		for _, i := range blindIdx {
			bl = append(bl, gaps[gi][i])
		}
		better, worse := 0, 0
		for i := range gaps[gi] {
			switch {
			case abs(gaps[gi][i]) < abs(gaps[0][i]):
				better++
			case abs(gaps[gi][i]) > abs(gaps[0][i]):
				worse++
			}
		}
		bm, bmean := 0.0, 0.0
		if len(bl) > 0 {
			bm, bmean = medAbs(bl), meanAbs(bl)
		}
		t.Logf("%-5s | %8.1f %8.1f %8.1f | %8.1f %8.1f | %d/%d", g.name,
			med, mean, signed, bm, bmean, better, worse)
	}
	// Paired mean improvement in |gap| on the blind subset, with a paired
	// standard error, per group.
	for gi, g := range groups {
		if gi == 0 || len(blindIdx) < 2 {
			continue
		}
		var d []float64
		for _, i := range blindIdx {
			d = append(d, float64(abs(gaps[0][i])-abs(gaps[gi][i])))
		}
		m, se := meanSE(d)
		t.Logf("blind-subset |gap| reduction, %s: %+.1f +/- %.1f cp (n=%d, positive = the term sees the danger)",
			g.name, m, se, len(d))
	}
}

func envInt(key string, def int) int {
	if s := os.Getenv(key); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func medAbs(v []int) float64 {
	a := make([]int, len(v))
	for i, x := range v {
		a[i] = abs(x)
	}
	sort.Ints(a)
	n := len(a)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return float64(a[n/2])
	}
	return float64(a[n/2-1]+a[n/2]) / 2
}

func meanAbs(v []int) float64 {
	s := 0
	for _, x := range v {
		s += abs(x)
	}
	return float64(s) / float64(len(v))
}

func meanSigned(v []int) float64 {
	s := 0
	for _, x := range v {
		s += x
	}
	return float64(s) / float64(len(v))
}

func meanSE(d []float64) (float64, float64) {
	n := float64(len(d))
	m := 0.0
	for _, x := range d {
		m += x
	}
	m /= n
	v := 0.0
	for _, x := range d {
		v += (x - m) * (x - m)
	}
	if n > 1 {
		v /= n - 1
	}
	return m, isqrt(v / n)
}

func isqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton, plenty for a reported error bar.
	z := x
	for range 40 {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
