package mirror

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
)

// egPos is one endgame the engine must convert (or hold). The HERO is the
// side to move in fen — every diagnosis FEN is 8fish-to-move from 8fish's
// POV, so this convention keeps the corpus honest.
//
// `want` is the OBJECTIVELY CORRECT hero result, taken from an external
// Stockfish 18 verification of every FEN (6 s/position; the `sf` field
// records its hero-POV verdict). This is NOT optional bookkeeping: the
// first version of this corpus was labelled from chess intuition plus the
// loss diagnosis' prose, and an adversarial review found EIGHT labels
// wrong — most embarrassingly `kpk-front` ("king in front of the pawn:
// won"), which with WHITE to move is the textbook mutual-zugzwang DRAW
// (1.Kc3 Kc5), and the three diagnosis FENs, which are all objectively
// LOST for 8fish at the harvested ply rather than even-or-better. Labels
// derived from our own shallow eval would have made this suite measure
// "the ON engine beats its handicapped twin" and call it conversion.
//
// want values:
//
//	"win"  hero is objectively winning: converting is the correct result
//	"draw" hero should hold exactly a draw
//	"lost" hero is objectively LOST — kept in the corpus because the points
//	       metric is a valid paired A/B on any position, but EXCLUDED from
//	       the correct-result rate, since "correct" there means losing.
type egPos struct {
	name string
	fen  string
	want string // "win" | "draw" | "lost"
	sf   string // external Stockfish 18 verdict, hero POV
	note string
}

// convEndgame is the conversion corpus: the loss-diagnosis failures
// (2026-07-25) plus unconverted wins mined from the 300-game match log plus
// textbook endgames. Every FEN is hero-to-move, legal, and inside the phase
// gate (TestEndgameCorpusValid).
var convEndgame = []egPos{
	// ---- The diagnosis positions (8fish to move; it lost these). NOTE all
	// three are objectively LOST at this ply, so the diagnosis' "even or
	// better" framing describes the material count, not the position: the
	// point of no return was EARLIER. They stay as regression ballast.
	{"diag-g137", "8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1", "lost", "cp -444 @d16",
		"8fish UP a pawn in material, but the K+P ending is lost; it played c7d6 and LOST"},
	{"diag-g246", "8/8/8/4N1pk/1K3p1p/1P3P2/P5n1/8 w - - 0 1", "lost", "cp -501 @d?",
		"minor-piece ending, material even but objectively lost; 8fish LOST"},
	{"diag-g61", "8/5p2/pk1P4/8/P5p1/3Pp1P1/2r1b1BP/4R1K1 b - - 0 1", "lost", "cp -362 @d?",
		"R+B each, 8fish thought it was even; objectively lost; 8fish LOST"},

	// ---- Unconverted wins HARVESTED from the same 300-game match log by
	// TestHarvestEndgames (the 8fish-to-move, phase-gated position where its
	// own depth-6 score peaked, in games it drew or lost). CAVEAT: these are
	// selected on the OFF engine's own inflated peak score, so several turn
	// out not to be wins at all — the Stockfish column is what decides.
	{"peak-g163", "8/2p5/2p3p1/8/7k/6p1/5r2/6K1 b - - 15 1", "win", "mate",
		"g163 DRAW; 8fish saw mate (+29997) and shuffled it away"},
	{"peak-g34", "1b2k3/1P4p1/4p2p/4P3/3Q4/6P1/5P2/6K1 w - - 5 1", "win", "decisive",
		"g34 DRAW; Q+2P vs B+2P"},
	{"peak-g194", "3r3k/R1R5/6P1/8/8/8/5PK1/8 w - - 5 1", "win", "decisive",
		"g194 DRAW; 2R+2P vs R"},
	{"peak-g196", "8/8/8/3K1k2/3B4/8/5P2/8 w - - 7 1", "win", "decisive",
		"g196 DRAW; K+B+P vs K"},
	{"peak-g91", "8/b7/8/5p2/1k6/1P1K3p/3N1p1P/8 b - - 1 1", "draw", "cp 0 @d58",
		"g91 LOSS; 8fish's eval said +390, Stockfish says DRAWN"},
	{"peak-g229", "8/8/8/1K6/P1Pk4/8/8/6r1 b - - 0 1", "win", "decisive",
		"g229 DRAW; R vs 2P, hero black"},
	{"peak-g234", "8/4k3/R3P3/2P3K1/8/2r5/8/8 w - - 7 1", "draw", "cp 0 @d76",
		"g234 DRAW; R+2P vs R — a CORRECT draw, our +351 was wrong"},
	{"peak-g132", "2R5/1P2p3/3k2pp/4p3/8/1r5P/3K2P1/8 w - - 3 1", "win", "decisive",
		"g132 DRAW; R+b7 passer vs R"},
	{"peak-g2", "8/P4k2/5p1p/3b4/6P1/3pK3/3B4/8 w - - 4 1", "draw", "cp 0 @d54",
		"g2 DRAW; bishops + a7 passer — a CORRECT draw"},
	{"peak-g8", "8/p3k2p/Ppr3pP/2P2p2/1B2p3/4P3/4KPP1/8 w - - 1 1", "lost", "cp -349 @d27",
		"g8 DRAW / g288 LOSS; our +229 was badly wrong — the hero is losing"},
	{"peak-g48", "3r4/pp5p/3bk1pP/P1pR1p2/2P1p3/1PB1P3/4KPP1/8 w - - 1 1", "draw", "cp -25 @d25",
		"g48 DRAW; our +120 was wrong, it is level-to-slightly-worse"},

	// ---- Textbook K+P.
	{"kpk-front", "8/8/8/3k4/8/3K4/3P4/8 w - - 0 1", "draw", "cp 0 @d67",
		"MUTUAL ZUGZWANG: with WHITE to move this is DRAWN (1.Kc3 Kc5). " +
			"NOTE mopup_conversion_test.go's KPK-win carries the same FEN " +
			"labelled a win — that label is wrong too"},
	{"kpk-file-e", "8/8/4k3/8/8/4K3/4P3/8 w - - 0 1", "win", "decisive",
		"king in front, pawn on the 2nd, reserve tempo: won"},
	{"kpk-rookdraw", "8/8/8/8/8/k7/P7/K7 w - - 0 1", "draw", "cp 0",
		"rook pawn, defender in the corner: drawn — must NOT be over-valued"},
	{"kpk-blocked", "8/8/8/3k4/3p4/3K4/8/8 w - - 0 1", "draw", "cp 0",
		"blocked pawn, hero is the DEFENDER: must hold"},

	// ---- Rule of the square.
	{"square-win", "8/6k1/8/8/8/8/P4K2/8 w - - 0 1", "win", "decisive",
		"black king OUTSIDE the square of the a-pawn: won by running it"},
	{"square-draw", "8/5k2/8/8/8/8/P4K2/8 w - - 0 1", "draw", "cp 0",
		"black king INSIDE the square by one file: drawn — the term must not lie"},

	// ---- Outside / distant passer.
	{"outside-passer", "8/8/8/5k2/1P4p1/8/6K1/8 w - - 0 1", "draw", "cp 0 @d64",
		"looks like a distant-passer win; Stockfish says DRAWN"},
	{"two-connected", "8/8/8/1k6/8/8/PP4K1/8 w - - 0 1", "win", "decisive",
		"K+2P connected vs bare king"},
	{"k2p-vs-kp", "8/8/3k4/4p3/P3P3/2K5/8/8 w - - 0 1", "win", "decisive",
		"K+2P vs K+P: outside a-pawn decoys, then the king eats e5"},

	// ---- Rook endings.
	{"lucena", "1K6/1P1k4/8/8/8/8/7r/2R5 w - - 0 1", "win", "decisive",
		"Lucena: R+P vs R, bridge-building win"},
	{"rook-behind", "7r/7p/8/1P6/8/6k1/1R6/6K1 w - - 0 1", "draw", "cp 0 @d34+",
		"R+P each, both rooks behind their own passer (Tarrasch); must not be thrown"},
}

// egCfg is the asm-matched player config for the conversion suite: the
// SHIPPED engine (mask 0x1f, recap2 QS, Default weights, mop-up ON) plus
// the endgame terms under test.
func egCfg(eg EndgameParams, cbudget uint64) PlayerCfg {
	return PlayerCfg{
		Features: FtAll, Weights: DefaultWeights, QS: DefaultQS,
		Mopup: DefaultMopup, EG: eg, EGCost: EvalTermsCost(2),
		CycleBudget: cbudget,
	}
}

// egPlayOut plays fen out to a referee result. white/black are the two
// configs; the return is (reason, whitePOV result, full moves played).
func egPlayOut(t *testing.T, fen string, white, black PlayerCfg, maxPlies int, seed uint64) (string, float64, int) {
	t.Helper()
	start, err := ParseFEN(fen)
	if err != nil {
		// Errorf, NOT Fatalf: runEGSuite calls this from worker goroutines,
		// where FailNow is a testing misuse (it would leak the run).
		t.Errorf("fen %q: %v", fen, err)
		return "parse-error", 0.5, 0
	}
	we, be := white.engine(), black.engine()
	rnd := rand.New(rand.NewPCG(seed, 0x5eed))
	gp := *start
	seen := map[uint32]int{}
	for ply := 0; ply < maxPlies; ply++ {
		eng, cfg := we, white
		if gp.Side != 0 {
			eng, cfg = be, black
		}
		eng.SetPosition(&gp)
		seen[eng.Pos.Hash]++
		inChk := eng.curInCheck()
		switch {
		case eng.Pos.Halfmove >= 100:
			return "50-move", 0.5, ply / 2
		case seen[eng.Pos.Hash] >= 3:
			return "threefold", 0.5, ply / 2
		case eng.Pos.Phase < 2 && !eng.anyPawn():
			return "material", 0.5, ply / 2
		}
		eng.Seed = byte(rnd.IntN(255)) + 1
		maxIters := cfg.MaxIters
		if maxIters <= 0 {
			maxIters = MaxPly - 1
		}
		var best Move
		switch {
		case cfg.CycleBudget > 0:
			best, _ = eng.SearchCycleBudget(cfg.CycleBudget, maxIters)
		case cfg.NodeBudget > 0:
			best, _ = eng.SearchBudget(cfg.NodeBudget, maxIters)
		default:
			best, _ = eng.SearchFixed(cfg.Depth)
		}
		if best.From == NoSq {
			if !inChk {
				return "stalemate", 0.5, ply / 2
			}
			if gp.Side == 0 { // white is mated
				return "mate", 0, ply / 2
			}
			return "mate", 1, ply / 2
		}
		eng.SetPosition(&gp)
		eng.make(best)
		gp = eng.Pos
		gp.Ply = 0
	}
	return "move-cap", 0.5, maxPlies / 2
}

// egOutcome is one position's tally over N dither seeds, hero POV.
type egOutcome struct {
	wins, draws, losses int
	movesToWin          []int
	reasons             map[string]int
}

func (o *egOutcome) avgMoves() string {
	if len(o.movesToWin) == 0 {
		return "-"
	}
	s := 0
	for _, m := range o.movesToWin {
		s += m
	}
	return fmt.Sprintf("%.0f", float64(s)/float64(len(o.movesToWin)))
}

func (o *egOutcome) String() string {
	return fmt.Sprintf("W/D/L %d/%d/%d  avgMovesToWin %-4s %s",
		o.wins, o.draws, o.losses, o.avgMoves(), egReasons(o.reasons))
}

func egReasons(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	s := ""
	for _, k := range keys {
		s += fmt.Sprintf("%s:%d ", k, m[k])
	}
	return s
}

// runEGSuite plays every corpus position `runs` times. The HERO (side to
// move in the FEN) gets heroCfg; the opponent gets oppCfg. Returns per-name
// hero-POV tallies.
func runEGSuite(t *testing.T, heroEG, oppEG EndgameParams, cbudget uint64, runs, maxPlies, workers int) map[string]*egOutcome {
	out := map[string]*egOutcome{}
	var mu sync.Mutex
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, pos := range convEndgame {
		wg.Add(1)
		sem <- struct{}{}
		go func(pos egPos) {
			defer wg.Done()
			defer func() { <-sem }()
			p, err := ParseFEN(pos.fen)
			if err != nil {
				t.Errorf("%s: %v", pos.name, err)
				return
			}
			// NOTE egPlayOut must not FailNow from this goroutine; it takes
			// the same t only to Errorf. See egPlayOut.
			heroWhite := p.Side == 0
			hero, opp := egCfg(heroEG, cbudget), egCfg(oppEG, cbudget)
			w, b := hero, opp
			if !heroWhite {
				w, b = opp, hero
			}
			o := &egOutcome{reasons: map[string]int{}}
			for r := 0; r < runs; r++ {
				reason, resW, moves := egPlayOut(t, pos.fen, w, b, maxPlies, uint64(r)*2654435761+1)
				res := resW
				if !heroWhite {
					res = 1 - resW
				}
				o.reasons[reason]++
				switch res {
				case 1:
					o.wins++
					o.movesToWin = append(o.movesToWin, moves)
				case 0:
					o.losses++
				default:
					o.draws++
				}
			}
			mu.Lock()
			out[pos.name] = o
			mu.Unlock()
		}(pos)
	}
	wg.Wait()
	return out
}

// egScoreOf turns a tally into three DISTINCT numbers, kept separate on
// purpose because the review of the first version showed they were being
// conflated:
//
//	pts       the hero's MATCH SCORE (win 1, draw 0.5) against the fixed
//	          defender. Needs no ground truth, so it is the honest A/B
//	          metric — but it REWARDS beating a weaker twin in an
//	          objectively drawn position, so it is not a "conversion rate".
//	correct   results that reach at least the objectively correct outcome.
//	          Only counted for want "win"/"draw"; positions where the hero is
//	          objectively LOST are excluded (counted==false), because there
//	          "correct" would mean losing.
//	over      results BETTER than the objective outcome — wins in a drawn
//	          position, or anything but a loss in a lost one. This is the
//	          part of a pts gain that is "outplayed the handicapped twin",
//	          NOT endgame technique, and it must be reported separately or
//	          the headline overstates the feature.
func egScoreOf(o *egOutcome, want string) (pts float64, correct int, over int, counted bool) {
	pts = float64(o.wins) + 0.5*float64(o.draws)
	switch want {
	case "win":
		return pts, o.wins, 0, true
	case "draw":
		return pts, o.wins + o.draws, o.wins, true
	default: // "lost": no defensible "correct" target
		return pts, 0, o.wins + o.draws, false
	}
}

// egPaired is a PAIRED comparison of two arms over the corpus. The unit of
// independence is the POSITION, not the game: the runs within a position
// differ only in the dither seed and are strongly correlated, so an
// error bar computed over 500 games would be fraudulently tight (the review
// caught exactly this). N here is 25.
type egPaired struct {
	n             int
	meanDelta     float64 // mean per-position pts delta (out of egConvRuns)
	stderr        float64
	totalDelta    float64
	better, worse int
	correctDelta  int
	overA, overB  int
	// ptsByClass splits the pts delta by the position's OBJECTIVE class, so
	// the share of the gain that comes from actually converting WON endings is
	// visible next to the share that is merely beating a weaker twin in a
	// drawn or lost one.
	ptsByClass map[string]float64
	nByClass   map[string]int
	ptsA, ptsB float64
	okA, okB   int
	okDenom    int
	wA, dA, lA int
	wB, dB, lB int
}

// lo/hi are the 95% paired CI on the mean per-position delta.
func (p *egPaired) lo() float64 { return p.meanDelta - 1.96*p.stderr }
func (p *egPaired) hi() float64 { return p.meanDelta + 1.96*p.stderr }

// sig reports whether the paired CI excludes zero.
func (p *egPaired) sig() string {
	switch {
	case p.lo() > 0:
		return "SIG+"
	case p.hi() < 0:
		return "SIG-"
	}
	return "ns"
}

func (p *egPaired) String() string {
	return fmt.Sprintf("pts %.1f -> %.1f (total %+.1f); per-position delta %+.2f +/- %.2f [%+.2f,%+.2f] %s (%d better / %d worse of %d); "+
		"pts delta BY OBJECTIVE CLASS: won %+.1f (n=%d) / drawn %+.1f (n=%d) / lost %+.1f (n=%d); "+
		"correct %d -> %d of %d (%+d); better-than-objective %d -> %d; W/D/L %d/%d/%d -> %d/%d/%d",
		p.ptsA, p.ptsB, p.totalDelta, p.meanDelta, p.stderr, p.lo(), p.hi(), p.sig(),
		p.better, p.worse, p.n,
		p.ptsByClass["win"], p.nByClass["win"], p.ptsByClass["draw"], p.nByClass["draw"],
		p.ptsByClass["lost"], p.nByClass["lost"],
		p.okA, p.okB, p.okDenom, p.correctDelta,
		p.overA, p.overB, p.wA, p.dA, p.lA, p.wB, p.dB, p.lB)
}

// egCompare builds the paired summary of arm B against arm A.
func egCompare(t *testing.T, resA, resB map[string]*egOutcome) *egPaired {
	t.Helper()
	p := &egPaired{ptsByClass: map[string]float64{}, nByClass: map[string]int{}}
	var deltas []float64
	for _, pos := range convEndgame {
		a, b := resA[pos.name], resB[pos.name]
		if a == nil || b == nil {
			t.Errorf("%s: missing from an arm — the corpus entry failed to run", pos.name)
			continue
		}
		pa, ca, oa, counted := egScoreOf(a, pos.want)
		pb, cb, ob, _ := egScoreOf(b, pos.want)
		d := pb - pa
		deltas = append(deltas, d)
		p.totalDelta += d
		switch {
		case d > 0:
			p.better++
		case d < 0:
			p.worse++
		}
		p.ptsA, p.ptsB = p.ptsA+pa, p.ptsB+pb
		p.ptsByClass[pos.want] += d
		p.nByClass[pos.want]++
		p.overA, p.overB = p.overA+oa, p.overB+ob
		if counted {
			p.okA, p.okB = p.okA+ca, p.okB+cb
			p.correctDelta += cb - ca
			p.okDenom += egConvRuns
		}
		p.wA, p.dA, p.lA = p.wA+a.wins, p.dA+a.draws, p.lA+a.losses
		p.wB, p.dB, p.lB = p.wB+b.wins, p.dB+b.draws, p.lB+b.losses
	}
	p.n = len(deltas)
	if p.n == 0 {
		return p
	}
	p.meanDelta = p.totalDelta / float64(p.n)
	if p.n > 1 {
		var ss float64
		for _, d := range deltas {
			ss += (d - p.meanDelta) * (d - p.meanDelta)
		}
		p.stderr = math.Sqrt(ss/float64(p.n-1)) / math.Sqrt(float64(p.n))
	}
	return p
}

const (
	egConvBudget = 30_000_000 // cycles/move: the Sargon-match operating point
	egConvRuns   = 20
	egConvPlies  = 250
	egConvWork   = 4
)

// TestEndgameConversion is the HEADLINE measurement (screen A): the
// conversion rate of the diagnosis + textbook endgames with the endgame
// terms ON vs OFF for the hero, against a FIXED reference defender that
// always has them OFF. That isolates the term's contribution (a both-sides
// screen cancels it, which is exactly why self-play reads neutral).
//
//	GOWORK=off go test ./internal/mirror -run TestEndgameConversion -v -timeout 90m
func TestEndgameConversion(t *testing.T) {
	if testing.Short() {
		t.Skip("conversion suite is slow")
	}
	off := EndgameParams{}
	on := DefaultEndgame

	t.Logf("=== ARM A: hero EG OFF vs defender OFF (the shipped engine) ===")
	resOff := runEGSuite(t, off, off, egConvBudget, egConvRuns, egConvPlies, egConvWork)
	t.Logf("=== ARM B: hero EG ON  vs defender OFF (the term under test) ===")
	resOn := runEGSuite(t, on, off, egConvBudget, egConvRuns, egConvPlies, egConvWork)

	t.Logf("%-15s %-5s %-14s | %-9s %-6s | %-9s %-6s | delta",
		"position", "want", "stockfish", "OFF W/D/L", "moves", "ON W/D/L", "moves")
	for _, pos := range convEndgame {
		a, b := resOff[pos.name], resOn[pos.name]
		if a == nil || b == nil {
			continue
		}
		pa, ca, _, counted := egScoreOf(a, pos.want)
		pb, cb, _, _ := egScoreOf(b, pos.want)
		corr := fmt.Sprintf("%+d correct", cb-ca)
		if !counted {
			corr = "(lost: excluded)"
		}
		t.Logf("%-15s %-5s %-14s | %d/%d/%d       %-6s | %d/%d/%d       %-6s | %+.1f pts %s",
			pos.name, pos.want, pos.sf, a.wins, a.draws, a.losses, a.avgMoves(),
			b.wins, b.draws, b.losses, b.avgMoves(), pb-pa, corr)
	}
	p := egCompare(t, resOff, resOn)
	t.Logf("HEADLINE (hero ON vs hero OFF, common fixed OFF defender):")
	t.Logf("  %s", p)
	t.Logf("  NOTE the error bar is paired over the %d POSITIONS (runs within a "+
		"position share everything but the dither seed, so a per-GAME error bar "+
		"would be fraudulently tight).", p.n)
	for _, pos := range convEndgame {
		t.Logf("  %-15s %s", pos.name, pos.note)
	}
}

// TestEndgameConversionBoth is the mop-up-precedent view: both sides ON vs
// both sides OFF. It should show a SMALLER delta than the isolated screen
// (the knowledge is symmetric), which is the mechanism behind a neutral
// self-play result.
func TestEndgameConversionBoth(t *testing.T) {
	if testing.Short() {
		t.Skip("conversion suite is slow")
	}
	off, on := EndgameParams{}, DefaultEndgame
	resOff := runEGSuite(t, off, off, egConvBudget, egConvRuns, egConvPlies, egConvWork)
	resOn := runEGSuite(t, on, on, egConvBudget, egConvRuns, egConvPlies, egConvWork)
	for _, pos := range convEndgame {
		a, b := resOff[pos.name], resOn[pos.name]
		if a == nil || b == nil {
			continue
		}
		t.Logf("%-15s want=%-5s bothOFF %d/%d/%d (%s)  bothON %d/%d/%d (%s)",
			pos.name, pos.want, a.wins, a.draws, a.losses, a.avgMoves(),
			b.wins, b.draws, b.losses, b.avgMoves())
	}
	t.Logf("BOTH-SIDES: %s", egCompare(t, resOff, resOn))
}

// egRunArms runs each arm's hero against the fixed OFF defender and reports
// every arm PAIRED against the first arm (which must be the OFF baseline),
// with a per-position error bar. Without the error bar these tables invite
// exactly the mistake the review caught: reading a 2-3 pts difference on a
// 25-position screen as a verdict.
func egRunArms(t *testing.T, arms []struct {
	name string
	eg   EndgameParams
}) {
	t.Helper()
	var baseRes map[string]*egOutcome
	for i, arm := range arms {
		res := runEGSuite(t, arm.eg, EndgameParams{}, egConvBudget, egConvRuns, egConvPlies, egConvWork)
		if i == 0 {
			baseRes = res
			p := egCompare(t, res, res)
			t.Logf("%-10s  pts %.1f  correct %d/%d  W/D/L %d/%d/%d  (baseline)",
				arm.name, p.ptsA, p.okA, p.okDenom, p.wA, p.dA, p.lA)
			continue
		}
		p := egCompare(t, baseRes, res)
		t.Logf("%-10s  pts %.1f (%+.1f)  per-pos %+.2f +/- %.2f %-4s  correct %d/%d (%+d)  W/D/L %d/%d/%d",
			arm.name, p.ptsB, p.totalDelta, p.meanDelta, p.stderr, p.sig(),
			p.okB, p.okDenom, p.correctDelta, p.wB, p.dB, p.lB)
	}
}

// TestEndgameConversionAblate runs the isolated (hero-only) conversion
// screen for each term ALONE, so a term that contributes nothing can be
// dropped rather than shipped on the strength of the bundle.
func TestEndgameConversionAblate(t *testing.T) {
	if testing.Short() {
		t.Skip("ablation is slow")
	}
	base := EndgameDesigned
	only := func(f func(*EndgameParams)) EndgameParams {
		p := EndgameParams{Enable: true, PhaseMax: base.PhaseMax}
		f(&p)
		return p
	}
	arms := []struct {
		name string
		eg   EndgameParams
	}{
		{"none(OFF)", EndgameParams{}},
		{"kcent", only(func(p *EndgameParams) { p.KingCent = base.KingCent })},
		{"kpawn", only(func(p *EndgameParams) { p.KingPawn = base.KingPawn })},
		{"pass", only(func(p *EndgameParams) { p.Pass = base.Pass })},
		{"pking", only(func(p *EndgameParams) { p.PassKingOur, p.PassKingThem = base.PassKingOur, base.PassKingThem })},
		{"kahead", only(func(p *EndgameParams) { p.KingAhead = base.KingAhead })},
		{"unstop", only(func(p *EndgameParams) { p.Unstoppable = base.Unstoppable })},
		{"rbehind", only(func(p *EndgameParams) { p.RookBehind = base.RookBehind })},
		{"all", base},
	}
	egRunArms(t, arms)
}

// TestEndgameConversionDropOne is the leave-one-out screen: the full set
// minus each term. A term whose removal does NOT hurt (or helps) is dead
// weight and gets dropped from DefaultEndgame rather than shipped on the
// bundle's strength. Also screens a couple of weight variants.
func TestEndgameConversionDropOne(t *testing.T) {
	if testing.Short() {
		t.Skip("ablation is slow")
	}
	base := EndgameDesigned
	drop := func(f func(*EndgameParams)) EndgameParams {
		p := base
		f(&p)
		return p
	}
	arms := []struct {
		name string
		eg   EndgameParams
	}{
		{"none(OFF)", EndgameParams{}},
		{"all", base},
		{"-kcent", drop(func(p *EndgameParams) { p.KingCent = 0 })},
		{"-kpawn", drop(func(p *EndgameParams) { p.KingPawn = 0 })},
		{"-pass", drop(func(p *EndgameParams) { p.Pass = [8]int{} })},
		{"-pking", drop(func(p *EndgameParams) { p.PassKingOur, p.PassKingThem = 0, 0 })},
		{"-kahead", drop(func(p *EndgameParams) { p.KingAhead = 0 })},
		{"-unstop", drop(func(p *EndgameParams) { p.Unstoppable = 0 })},
		{"-rbehind", drop(func(p *EndgameParams) { p.RookBehind = 0 })},
		{"kcent4", drop(func(p *EndgameParams) { p.KingCent = 4 })},
		{"pking-x2", drop(func(p *EndgameParams) { p.PassKingOur, p.PassKingThem = 12, 8 })},
		{"phase8", drop(func(p *EndgameParams) { p.PhaseMax = 8 })},
	}
	egRunArms(t, arms)
}

// TestEndgameConversionRound2 refines on top of round 1's verdict (the
// rule-of-the-square bonus HURT: dropping it was the single best arm), by
// leaving one more term out of the reduced set and re-testing a small,
// tempo-independent square-rule weight.
func TestEndgameConversionRound2(t *testing.T) {
	if testing.Short() {
		t.Skip("ablation is slow")
	}
	base := EndgameDesigned
	base.Unstoppable = 0
	mod := func(f func(*EndgameParams)) EndgameParams {
		p := base
		f(&p)
		return p
	}
	arms := []struct {
		name string
		eg   EndgameParams
	}{
		{"none(OFF)", EndgameParams{}},
		{"nounstop", base},
		{"--kcent", mod(func(p *EndgameParams) { p.KingCent = 0 })},
		{"--kpawn", mod(func(p *EndgameParams) { p.KingPawn = 0 })},
		{"--both-k", mod(func(p *EndgameParams) { p.KingCent, p.KingPawn = 0, 0 })},
		{"--rbehind", mod(func(p *EndgameParams) { p.RookBehind = 0 })},
		{"--kahead", mod(func(p *EndgameParams) { p.KingAhead = 0 })},
		{"--pass", mod(func(p *EndgameParams) { p.Pass = [8]int{} })},
		{"--pking", mod(func(p *EndgameParams) { p.PassKingOur, p.PassKingThem = 0, 0 })},
		{"unstop80", mod(func(p *EndgameParams) { p.Unstoppable = 80 })},
		{"pass-x2", mod(func(p *EndgameParams) {
			for i := range p.Pass {
				p.Pass[i] *= 2
			}
		})},
	}
	egRunArms(t, arms)
}

// TestEndgameSmoke is the quick wiring/timing check.
func TestEndgameSmoke(t *testing.T) {
	for _, name := range []string{"diag-g137", "kpk-front", "square-win"} {
		for _, arm := range []struct {
			label string
			eg    EndgameParams
		}{{"OFF", EndgameParams{}}, {"ON", DefaultEndgame}} {
			var pos egPos
			for _, p := range convEndgame {
				if p.name == name {
					pos = p
				}
			}
			p, err := ParseFEN(pos.fen)
			if err != nil {
				t.Fatal(err)
			}
			hero, opp := egCfg(arm.eg, 5_000_000), egCfg(EndgameParams{}, 5_000_000)
			w, b := hero, opp
			if p.Side != 0 {
				w, b = opp, hero
			}
			reason, resW, moves := egPlayOut(t, pos.fen, w, b, 120, 1)
			res := resW
			if p.Side != 0 {
				res = 1 - resW
			}
			t.Logf("%-12s %-3s -> %-10s hero=%.1f in %d moves", name, arm.label, reason, res, moves)
		}
	}
}

// TestEndgameHarnessPOV is the harness self-check: the conversion suite's
// hero-POV mapping, on positions with a FORCED known outcome, for both hero
// colors. A sign error here would invert the entire measurement.
func TestEndgameHarnessPOV(t *testing.T) {
	cfg := egCfg(EndgameParams{}, 2_000_000)
	cases := []struct {
		name, fen string
		wantRes   float64
	}{
		// Hero = white (to move), mates in 1: hero must score 1.
		{"white-mates", "7k/5Q2/6K1/8/8/8/8/8 w - - 0 1", 1},
		// Hero = black (to move), gets mated: hero must score 0.
		{"black-mated", "5k2/5Q2/5K2/8/8/8/8/8 b - - 0 1", 0},
		// Hero = black (to move), mates in 1: hero must score 1.
		{"black-mates", "8/8/8/8/8/1k6/5q2/K7 b - - 0 1", 1},
	}
	for _, c := range cases {
		p, err := ParseFEN(c.fen)
		if err != nil {
			t.Fatal(err)
		}
		reason, resW, moves := egPlayOut(t, c.fen, cfg, cfg, 20, 1)
		res := resW
		if p.Side != 0 {
			res = 1 - resW
		}
		if res != c.wantRes {
			t.Errorf("%s: hero scored %.1f want %.1f (reason %s, %d moves)", c.name, res, c.wantRes, reason, moves)
		} else {
			t.Logf("%-12s hero=%.1f reason=%s moves=%d", c.name, res, reason, moves)
		}
	}
}

// TestEndgameConversionDecide pairs the SHIPPED set directly against the
// variants it was chosen over, so the two ship/kill decisions get a CI on
// the decision-relevant DIFFERENCE rather than on each arm's gap to OFF.
// (The review's point: on a 25-position screen a 2-3 pts table difference is
// not a verdict.)
func TestEndgameConversionDecide(t *testing.T) {
	if testing.Short() {
		t.Skip("decision screen is slow")
	}
	ship := DefaultEndgame
	withUnstop := ship
	withUnstop.Unstoppable = EndgameDesigned.Unstoppable
	withUnstop80 := ship
	withUnstop80.Unstoppable = 80
	withRB := ship
	withRB.RookBehind = EndgameDesigned.RookBehind

	shipRes := runEGSuite(t, ship, EndgameParams{}, egConvBudget, egConvRuns, egConvPlies, egConvWork)
	for _, c := range []struct {
		name string
		eg   EndgameParams
	}{
		{"+unstop250", withUnstop},
		{"+unstop80", withUnstop80},
		{"+rbehind20", withRB},
		{"=designed(both)", EndgameDesigned},
	} {
		res := runEGSuite(t, c.eg, EndgameParams{}, egConvBudget, egConvRuns, egConvPlies, egConvWork)
		p := egCompare(t, shipRes, res)
		t.Logf("shipped -> shipped%-16s  per-position delta %+.2f +/- %.2f [%+.2f,%+.2f] %-4s  pts %+.1f  correct %+d",
			c.name, p.meanDelta, p.stderr, p.lo(), p.hi(), p.sig(), p.totalDelta, p.correctDelta)
	}
	t.Log("A NEGATIVE delta here means the extra term makes the shipped set WORSE.")
}
