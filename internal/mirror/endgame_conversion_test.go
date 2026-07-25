package mirror

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"sync"
	"testing"
)

// egPos is one endgame the engine must convert (or hold). The HERO is the
// side to move in fen — every diagnosis FEN is 8fish-to-move from 8fish's
// POV, so this convention keeps the corpus honest. want is the correct
// result for the hero.
type egPos struct {
	name string
	fen  string
	want string // "win" | "draw"
	note string
}

// convEndgame is the conversion corpus: the loss-diagnosis failures
// (2026-07-25) plus textbook should-win / should-hold endgames. Every FEN
// is hero-to-move and inside the phase gate (TestEndgameCorpusValid).
var convEndgame = []egPos{
	// ---- The diagnosis positions (8fish to move; it lost or drew these).
	{"diag-g137", "8/2p2p2/3P4/1Pp2kp1/6p1/3K4/6PP/8 b - - 0 1", "win",
		"8fish UP a pawn in a pure K+P ending; played c7d6 and LOST"},
	{"diag-g246", "8/8/8/4N1pk/1K3p1p/1P3P2/P5n1/8 w - - 0 1", "draw",
		"minor-piece ending, material even; 8fish LOST"},
	{"diag-g61", "8/5p2/pk1P4/8/P5p1/3Pp1P1/2r1b1BP/4R1K1 b - - 0 1", "draw",
		"R+B each, even and 8fish knew it; 8fish LOST"},

	// ---- Unconverted wins HARVESTED from the same 300-game match log by
	// TestHarvestEndgames: the 8fish-to-move, phase-gated endgame position
	// where 8fish's own depth-6 score peaked, in games it then drew or lost.
	// These are literally the half-points the diagnosis says we leak.
	{"peak-g163", "8/2p5/2p3p1/8/7k/6p1/5r2/6K1 b - - 15 1", "win",
		"g163 DRAW; peak +29997 (a seen mate, shuffled away)"},
	{"peak-g34", "1b2k3/1P4p1/4p2p/4P3/3Q4/6P1/5P2/6K1 w - - 5 1", "win",
		"g34 DRAW; peak +1029 (Q+2P vs B+2P)"},
	{"peak-g194", "3r3k/R1R5/6P1/8/8/8/5PK1/8 w - - 5 1", "win",
		"g194 DRAW; peak +914 (2R+2P vs R)"},
	{"peak-g196", "8/8/8/3K1k2/3B4/8/5P2/8 w - - 7 1", "win",
		"g196 DRAW; peak +434 (K+B+P vs K)"},
	{"peak-g91", "8/b7/8/5p2/1k6/1P1K3p/3N1p1P/8 b - - 1 1", "win",
		"g91 LOSS; peak +390 (B+3P vs N+2P, hero black)"},
	{"peak-g229", "8/8/8/1K6/P1Pk4/8/8/6r1 b - - 0 1", "win",
		"g229 DRAW; peak +373 (R vs 2P, hero black)"},
	{"peak-g234", "8/4k3/R3P3/2P3K1/8/2r5/8/8 w - - 7 1", "win",
		"g234 DRAW; peak +351 (R+2P vs R — rook technique)"},
	{"peak-g132", "2R5/1P2p3/3k2pp/4p3/8/1r5P/3K2P1/8 w - - 3 1", "win",
		"g132 DRAW; peak +335 (R+b7 passer vs R)"},
	{"peak-g2", "8/P4k2/5p1p/3b4/6P1/3pK3/3B4/8 w - - 4 1", "win",
		"g2 DRAW; peak +233 (bishops, a7 passer)"},
	{"peak-g8", "8/p3k2p/Ppr3pP/2P2p2/1B2p3/4P3/4KPP1/8 w - - 1 1", "win",
		"g8 DRAW / g288 LOSS from the same position; peak +229"},
	{"peak-g48", "3r4/pp5p/3bk1pP/P1pR1p2/2P1p3/1PB1P3/4KPP1/8 w - - 1 1", "win",
		"g48 DRAW; peak +120 (R+B each, h6 passer)"},

	// ---- Textbook K+P: the class g137 belongs to.
	{"kpk-front", "8/8/8/3k4/8/3K4/3P4/8 w - - 0 1", "win",
		"king in front of the pawn: won"},
	{"kpk-file-e", "8/8/4k3/8/8/4K3/4P3/8 w - - 0 1", "win",
		"king in front, pawn on the 2nd, reserve tempo: won"},
	{"kpk-rookdraw", "8/8/8/8/8/k7/P7/K7 w - - 0 1", "draw",
		"rook pawn, defender in the corner: drawn — must NOT be over-valued"},
	{"kpk-blocked", "8/8/8/3k4/3p4/3K4/8/8 w - - 0 1", "draw",
		"blocked pawn, hero is the DEFENDER: must hold"},

	// ---- Rule of the square (the cheap unstoppable-passer knowledge).
	{"square-win", "8/6k1/8/8/8/8/P4K2/8 w - - 0 1", "win",
		"black king OUTSIDE the square of the a-pawn: won by running it"},
	{"square-draw", "8/5k2/8/8/8/8/P4K2/8 w - - 0 1", "draw",
		"black king INSIDE the square by one file: drawn — the term must not lie"},

	// ---- Outside / distant passer.
	{"outside-passer", "8/8/8/5k2/1P4p1/8/6K1/8 w - - 0 1", "win",
		"distant b-pawn decoys the king off the g-pawn"},
	{"two-connected", "8/8/8/1k6/8/8/PP4K1/8 w - - 0 1", "win",
		"K+2P connected vs bare king"},
	{"k2p-vs-kp", "8/8/3k4/4p3/P3P3/2K5/8/8 w - - 0 1", "win",
		"K+2P vs K+P: outside a-pawn decoys, then the king eats e5"},

	// ---- Rook endings.
	{"lucena", "1K6/1P1k4/8/8/8/8/7r/2R5 w - - 0 1", "win",
		"Lucena: R+P vs R, bridge-building win"},
	{"rook-behind", "7r/7p/8/1P6/8/6k1/1R6/6K1 w - - 0 1", "draw",
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
		t.Fatalf("fen %q: %v", fen, err)
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

// egScoreOf turns a tally into the hero's points and the count of results
// matching `want`.
func egScoreOf(o *egOutcome, want string) (pts float64, correct int) {
	pts = float64(o.wins) + 0.5*float64(o.draws)
	if want == "win" {
		correct = o.wins
	} else {
		correct = o.wins + o.draws
	}
	return
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

	var ptsOff, ptsOn float64
	var okOff, okOn int
	t.Logf("%-15s %-5s | %-9s %-6s | %-9s %-6s | delta", "position", "want", "OFF W/D/L", "moves", "ON W/D/L", "moves")
	for _, pos := range convEndgame {
		a, b := resOff[pos.name], resOn[pos.name]
		if a == nil || b == nil {
			continue
		}
		pa, ca := egScoreOf(a, pos.want)
		pb, cb := egScoreOf(b, pos.want)
		ptsOff += pa
		ptsOn += pb
		okOff += ca
		okOn += cb
		t.Logf("%-15s %-5s | %d/%d/%d       %-6s | %d/%d/%d       %-6s | %+.1f pts %+d correct",
			pos.name, pos.want, a.wins, a.draws, a.losses, a.avgMoves(),
			b.wins, b.draws, b.losses, b.avgMoves(), pb-pa, cb-ca)
	}
	n := len(convEndgame) * egConvRuns
	t.Logf("TOTAL: OFF %.1f/%d pts, %d/%d correct  ->  ON %.1f/%d pts, %d/%d correct  (delta %+.1f pts, %+d correct)",
		ptsOff, n, okOff, n, ptsOn, n, okOn, n, ptsOn-ptsOff, okOn-okOff)
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
	var ptsOff, ptsOn float64
	var okOff, okOn int
	for _, pos := range convEndgame {
		a, b := resOff[pos.name], resOn[pos.name]
		pa, ca := egScoreOf(a, pos.want)
		pb, cb := egScoreOf(b, pos.want)
		ptsOff, ptsOn = ptsOff+pa, ptsOn+pb
		okOff, okOn = okOff+ca, okOn+cb
		t.Logf("%-15s want=%-5s bothOFF %d/%d/%d (%s)  bothON %d/%d/%d (%s)",
			pos.name, pos.want, a.wins, a.draws, a.losses, a.avgMoves(),
			b.wins, b.draws, b.losses, b.avgMoves())
	}
	n := len(convEndgame) * egConvRuns
	t.Logf("TOTAL both-sides: OFF %.1f/%d (%d correct) -> ON %.1f/%d (%d correct)  delta %+.1f pts %+d correct",
		ptsOff, n, okOff, ptsOn, n, okOn, ptsOn-ptsOff, okOn-okOff)
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
	n := len(convEndgame) * egConvRuns
	for _, arm := range arms {
		res := runEGSuite(t, arm.eg, EndgameParams{}, egConvBudget, egConvRuns, egConvPlies, egConvWork)
		var pts float64
		var ok, w, d, l int
		for _, pos := range convEndgame {
			o := res[pos.name]
			p, c := egScoreOf(o, pos.want)
			pts += p
			ok += c
			w, d, l = w+o.wins, d+o.draws, l+o.losses
		}
		t.Logf("%-10s  %.1f/%d pts   %d/%d correct   W/D/L %d/%d/%d", arm.name, pts, n, ok, n, w, d, l)
	}
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
	n := len(convEndgame) * egConvRuns
	for _, arm := range arms {
		res := runEGSuite(t, arm.eg, EndgameParams{}, egConvBudget, egConvRuns, egConvPlies, egConvWork)
		var pts float64
		var ok, w, d, l int
		for _, pos := range convEndgame {
			o := res[pos.name]
			p, c := egScoreOf(o, pos.want)
			pts += p
			ok += c
			w, d, l = w+o.wins, d+o.draws, l+o.losses
		}
		t.Logf("%-10s  %.1f/%d pts   %d/%d correct   W/D/L %d/%d/%d", arm.name, pts, n, ok, n, w, d, l)
	}
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
	n := len(convEndgame) * egConvRuns
	for _, arm := range arms {
		res := runEGSuite(t, arm.eg, EndgameParams{}, egConvBudget, egConvRuns, egConvPlies, egConvWork)
		var pts float64
		var ok, w, d, l int
		for _, pos := range convEndgame {
			o := res[pos.name]
			p, c := egScoreOf(o, pos.want)
			pts += p
			ok += c
			w, d, l = w+o.wins, d+o.draws, l+o.losses
		}
		t.Logf("%-10s  %.1f/%d pts   %d/%d correct   W/D/L %d/%d/%d", arm.name, pts, n, ok, n, w, d, l)
	}
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
