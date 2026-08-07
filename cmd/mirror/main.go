// Command mirror drives the Go mirror engine (internal/mirror) for
// fast, node-denominated experiments: fixed-depth node counts, Texel
// tuning of the pawn-structure weights, and self-play validation
// matches. See docs/plan.md task #20.
package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/zellyn/chess6502/internal/mirror"
	"github.com/zellyn/chess6502/internal/sprt"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "nodes":
		nodes(os.Args[2:])
	case "gen":
		gen(os.Args[2:])
	case "tune":
		tune(os.Args[2:])
	case "match":
		match(os.Args[2:])
	case "pgnrows":
		pgnrows(os.Args[2:])
	case "genfen":
		genfen(os.Args[2:])
	case "tunekb":
		tunekb(os.Args[2:])
	case "atime":
		atime(os.Args[2:])
	default:
		usage()
	}
}

// atime runs the adaptive time/effort-management screen: an adaptive
// allocation policy (A) vs a flat allocation (B) at the SAME per-game cycle
// bank, asm-matched config, self-play. Both arms earn Base cycles/move of
// income; the difference is only HOW the bank is spent. Reports Elo +/- err
// and per-side diagnostics (moves extended/shortened, cycles/game, avg
// depth) so the gain can be attributed to the panic vs easy-move side.
func atime(args []string) {
	fs := flag.NewFlagSet("atime", flag.ExitOnError)
	base := fs.Uint64("base", 40_000_000, "per-move cycle income (== the flat per-move budget)")
	pairs := fs.Int("pairs", 1000, "game pairs (2 games each)")
	workers := fs.Int("workers", 3, "parallel pairs")
	seed := fs.Uint64("seed", 6502, "RNG seed")
	variant := fs.String("variant", "adaptive", "A policy: flatbank|easy|panic|unstable|adaptive|adaptive-smooth|adaptive-aggr")
	bl := fs.String("baseline", "flatbank", "B policy (the flat baseline): flat|flatbank")
	maxiters := fs.Int("maxiters", 0, "ID depth ceiling (0 = MaxPly-1)")
	fs.Parse(args)

	// asm-matched config: mask 0x1f (FtAll), recap2 QS, shipped (Default)
	// weights, corrected-guard RFP 120/500 (NewEngine default via nil Fut).
	mk := func(tp *mirror.TimeParams) mirror.PlayerCfg {
		return mirror.PlayerCfg{
			Features: mirror.FtAll, Weights: mirror.DefaultWeights,
			QS: mirror.DefaultQS, Time: tp, MaxIters: *maxiters,
		}
	}
	aTP := atimeVariant(*variant, *base)
	bTP := atimeVariant(*bl, *base)
	a, b := mk(aTP), mk(bTP)

	lines, err := mirror.GenOpenings(sprt.Openings, *pairs, *seed)
	check(err)
	start := time.Now()
	res, aD, bD, err := mirror.TimeMatch(a, b, lines, *pairs, *workers, *seed)
	check(err)
	games := 2 * *pairs
	fmt.Printf("A=%s vs B=%s  base %dM  %d games (seed %d, %v)\n",
		*variant, *bl, *base/1_000_000, games, *seed, time.Since(start).Round(time.Second))
	fmt.Printf("  result (A POV): %s\n", res)
	fmt.Printf("  A %s\n", atimeDiagStr(aD, *base))
	fmt.Printf("  B %s\n", atimeDiagStr(bD, *base))
}

// atimeVariant maps a policy name to a TimeParams at the given base.
func atimeVariant(name string, base uint64) *mirror.TimeParams {
	switch name {
	case "flat":
		// Pure flat, no bank: byte-identical to a flat SearchCycleBudget(base).
		return &mirror.TimeParams{On: false, Base: base, MaxEighths: 8}
	case "flatbank":
		// BankedClock-style flat baseline: base ceiling Base + bank/8, no
		// signals. Recycles under-spend EVENLY (fair equal-bank flat).
		return &mirror.TimeParams{On: true, Base: base, Smooth: true, MaxEighths: 8}
	case "easy":
		// Easy-stop only, smoothed base (banks easy savings, spreads via /8).
		return &mirror.TimeParams{On: true, Base: base, Smooth: true,
			EasyStop: true, StableIters: 3, ScoreFlat: 20, MinDepth: 3, MinSpendEighths: 4,
			MaxEighths: 8}
	case "panic":
		// Panic (score-drop) extension only, no easy-stop.
		return &mirror.TimeParams{On: true, Base: base, Smooth: true,
			Panic: true, PanicDrop: 40, PanicEighths: 24, MaxEighths: 24}
	case "unstable":
		// Instability extension only, no easy-stop.
		return &mirror.TimeParams{On: true, Base: base, Smooth: true,
			Unstable: true, UnstableEighths: 24, MaxEighths: 24}
	case "adaptive":
		// Conservative: easy-stop banks, panic+instability extend to 2x.
		return &mirror.TimeParams{On: true, Base: base, Smooth: false,
			EasyStop: true, StableIters: 3, ScoreFlat: 20, MinDepth: 3, MinSpendEighths: 4,
			Panic: true, PanicDrop: 40, PanicEighths: 16,
			Unstable: true, UnstableEighths: 16, MaxEighths: 16}
	case "adaptive-smooth":
		// Same signals but on a smoothed base ceiling (recycles the bank fully
		// even on non-extended moves — should hold total compute closest).
		return &mirror.TimeParams{On: true, Base: base, Smooth: true,
			EasyStop: true, StableIters: 3, ScoreFlat: 20, MinDepth: 3, MinSpendEighths: 4,
			Panic: true, PanicDrop: 40, PanicEighths: 24,
			Unstable: true, UnstableEighths: 16, MaxEighths: 24}
	case "adaptive-aggr":
		// Aggressive: extend to 4x, earlier/looser easy-stop.
		return &mirror.TimeParams{On: true, Base: base, Smooth: false,
			EasyStop: true, StableIters: 2, ScoreFlat: 30, MinDepth: 2, MinSpendEighths: 2,
			Panic: true, PanicDrop: 25, PanicEighths: 32,
			Unstable: true, UnstableEighths: 24, MaxEighths: 32}
	default:
		fmt.Fprintf(os.Stderr, "unknown atime variant %q\n", name)
		os.Exit(2)
		return nil
	}
}

func atimeDiagStr(d mirror.TimeDiag, base uint64) string {
	if d.Moves == 0 {
		return "(no timed moves)"
	}
	avgDepth := float64(d.DepthSum) / float64(d.Moves)
	mcycPerMove := float64(d.Cycles) / float64(d.Moves) / 1e6
	pctExt := 100 * float64(d.Extended) / float64(d.Moves)
	pctShort := 100 * float64(d.Shortened) / float64(d.Moves)
	return fmt.Sprintf("moves %d  ext %.1f%% (panic %d, unstable %d)  short %.1f%%  avgdepth %.2f  %.1f Mcyc/move (base %dM)",
		d.Moves, pctExt, d.Panics, d.Unstables, pctShort, avgDepth, mcycPerMove, base/1_000_000)
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: mirror <command> [flags]
  nodes    fixed-depth node counts by feature mask
  gen      self-play a chunk of games, appending labeled rows to -data
  tune     Texel-tune pstruct weights from a -data file
  match    self-play match between weight/feature configurations
  pgnrows  extract quiet labeled rows from PGN files into -data`)
	os.Exit(2)
}

// pgnrows extracts quiet, labeled training rows from external-opponent
// PGNs (the rating-pool gauntlet games) and appends them to -data, so
// the Texel corpus gains non-self-play material.
func pgnrows(args []string) {
	fs := flag.NewFlagSet("pgnrows", flag.ExitOnError)
	data := fs.String("data", "texel.rows", "row file to append to")
	everyN := fs.Int("every", 1, "keep every Nth qualifying quiet position")
	fs.Parse(args)
	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "pgnrows: no PGN files given")
		os.Exit(2)
	}
	var total mirror.PGNStats
	for _, p := range paths {
		samples, st, err := mirror.PGNSamples(p, *everyN)
		check(err)
		check(mirror.AppendRows(*data, mirror.SampleRows(samples)))
		fmt.Printf("  %-40s games %3d  skipped %3d  samples %5d\n",
			p, st.Games, st.Skipped, st.Samples)
		total.Games += st.Games
		total.Skipped += st.Skipped
		total.Samples += st.Samples
	}
	fmt.Printf("total: games %d, skipped %d, samples %d appended to %s\n",
		total.Games, total.Skipped, total.Samples, *data)
}

func nodes(args []string) {
	fs := flag.NewFlagSet("nodes", flag.ExitOnError)
	fen := fs.String("fen", "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", "position")
	depth := fs.Int("depth", 6, "fixed depth")
	fs.Parse(args)
	for _, mask := range []byte{0x00, 0x01, 0x02, 0x04, 0x07, 0x0F} {
		pos, err := mirror.ParseFEN(*fen)
		check(err)
		eng := mirror.NewEngine()
		eng.Features = mask
		eng.SetPosition(pos)
		start := time.Now()
		best, score := eng.SearchFixed(*depth)
		fmt.Printf("features %#02x: %10d nodes, depth %d, best %s score %d (%v)\n",
			mask, eng.Nodes, *depth, best.UCI(), score, time.Since(start).Round(time.Millisecond))
	}
}

func gen(args []string) {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	depth := fs.Int("depth", 5, "self-play depth")
	games := fs.Int("games", 300, "games this chunk")
	workers := fs.Int("workers", runtime.NumCPU()-2, "parallel games")
	seed := fs.Uint64("seed", 6502, "RNG seed (vary per chunk)")
	openings := fs.Int("openings", 300, "generated opening lines")
	data := fs.String("data", "texel.rows", "row file to append to")
	fs.Parse(args)

	lines, err := mirror.GenOpenings(sprt.Openings, *openings, *seed)
	check(err)
	start := time.Now()
	samples, err := mirror.GenerateData(lines, mirror.DefaultWeights, *depth,
		*games, *workers, *seed, func(games, n int) {
			fmt.Printf("  %d games, %d samples (%v)\n", games, n, time.Since(start).Round(time.Second))
		})
	check(err)
	check(mirror.AppendRows(*data, mirror.SampleRows(samples)))
	fmt.Printf("chunk done: %d games, %d samples appended to %s in %v\n",
		*games, len(samples), *data, time.Since(start).Round(time.Second))
}

func tune(args []string) {
	fs := flag.NewFlagSet("tune", flag.ExitOnError)
	data := fs.String("data", "texel.rows", "row file from gen")
	fs.Parse(args)

	rows, err := mirror.LoadRows(*data)
	check(err)
	fmt.Printf("loaded %d rows from %s\n", len(rows), *data)

	k := mirror.FitK(rows, mirror.DefaultWeights)
	fmt.Printf("fitted sigmoid K = %.2f\n", k)

	tuned, before, after := mirror.Tune(rows, mirror.DefaultWeights, k,
		func(step string, loss float64) { fmt.Printf("  %s: loss %.6f\n", step, loss) })
	fmt.Printf("loss: %.6f -> %.6f\n", before, after)
	fmt.Printf("tuned weights: %+v\n", tuned)
	fmt.Printf("asm PASSEDBONUS: .byte %d, %d, %d, %d, %d, %d, %d, %d\n",
		tuned.Passed[0], tuned.Passed[1], tuned.Passed[2], tuned.Passed[3],
		tuned.Passed[4], tuned.Passed[5], tuned.Passed[6], tuned.Passed[7])
}

func match(args []string) {
	fs := flag.NewFlagSet("match", flag.ExitOnError)
	depth := fs.Int("depth", 5, "fixed depth (fixed-depth mode)")
	budget := fs.Uint64("budget", 0, "per-move NODE budget (>0 selects node-budgeted iterative-deepening mode; both sides equal)")
	maxiters := fs.Int("maxiters", 0, "ID depth ceiling in budget mode (0 = MaxPly-1)")
	pairs := fs.Int("pairs", 150, "game pairs (2 games each)")
	workers := fs.Int("workers", runtime.NumCPU()-2, "parallel pairs")
	seed := fs.Uint64("seed", 6502, "RNG seed")
	aMask := fs.Uint("afeat", uint(mirror.FtAll), "A feature mask")
	bMask := fs.Uint("bfeat", uint(mirror.FtAll), "B feature mask")
	aw := fs.String("aweights", "default", "A pstruct weights: default|tuned|w:d,i,p1..p6,s,o")
	bw := fs.String("bweights", "default", "B pstruct weights")
	aFix := fs.Bool("afix", false, "A uses the fixed futility mate-zone guard")
	bFix := fs.Bool("bfix", false, "B uses the fixed futility mate-zone guard")
	aLMR := fs.String("almr", "", "A LMR params late1,late2,rem1,rem2,killers,evasion (empty = asm current)")
	bLMR := fs.String("blmr", "", "B LMR params")
	// Default recap2 = the asm's shipped QS shape (gennode RecapAfter=2), so
	// a bare `mirror match` models the asm. Pass "0,0" for the unlimited-QS
	// experiment baseline.
	aQS := fs.String("aqs", "0,2", "A QS shape: plycap,recapafter (0,0 = unlimited)")
	bQS := fs.String("bqs", "0,2", "B QS shape")
	aKB := fs.String("akb", "", "A king-bucket table file (empty = off)")
	bKB := fs.String("bkb", "", "B king-bucket table file")
	aFut := fs.String("afut", "", "A futility params: guard,rfp1,rfp2,rfp3,rfp4,fut,maxrem (empty = shipped)")
	bFut := fs.String("bfut", "", "B futility params")
	aOrd := fs.String("aord", "", "A ordering knobs: losinglast,histmalus (e.g. 1,1)")
	bOrd := fs.String("bord", "", "B ordering knobs")
	aLMP := fs.String("almp", "", "A LMP params: dmax,base,mult,quad,exemptkillers,keepchecks (empty = off)")
	bLMP := fs.String("blmp", "", "B LMP params")
	aAsp := fs.String("aasp", "", "A aspiration params: delta[,policy] policy=full|prog|asym (empty = off)")
	bAsp := fs.String("basp", "", "B aspiration params")
	aCM := fs.String("acm", "", "A countermove params: indexing[,beforekillers[,persist]] indexing=1(to)|2(piece,to) (empty = off)")
	bCM := fs.String("bcm", "", "B countermove params")
	aExtra := fs.String("aextra", "", "A experimental eval terms: key:val,... keys ropen,rsemi,bishop,rook7,drook,block,tropism (empty = off)")
	bExtra := fs.String("bextra", "", "B experimental eval terms")
	aExtraCost := fs.Float64("aextracost", 0, "A per-eval-call cycle cost of the extra terms (cycle mode only)")
	bExtraCost := fs.Float64("bextracost", 0, "B per-eval-call cycle cost of the extra terms (cycle mode only)")
	aMop := fs.String("amop", "", "A endgame mop-up: \"default\"|\"off\"|edge,close,matthresh,phasemax")
	bMop := fs.String("bmop", "", "B endgame mop-up: \"default\"|\"off\"|edge,close,matthresh,phasemax")
	aEG := fs.String("aeg", "", "A endgame-technique terms: \"default\" or key:val,... keys phase,kcent,kpawn,pass,pking,pkingt,kahead,unstop,rbehind (empty = off)")
	bEG := fs.String("beg", "", "B endgame-technique terms")
	aEGCost := fs.Float64("aegcost", 0, "A per-gated-eval-call cycle cost of the endgame terms (cycle mode only)")
	bEGCost := fs.Float64("begcost", 0, "B per-gated-eval-call cycle cost of the endgame terms (cycle mode only)")
	aMid := fs.String("amid", "", "A middlegame terms: \"default\"|\"ks\"|\"pos\"|key:val,... (empty = off)")
	bMid := fs.String("bmid", "", "B middlegame terms")
	aMidCost := fs.Float64("amidcost", 0, "A per-gated-eval-call cycle cost of the middlegame terms (cycle mode only)")
	bMidCost := fs.Float64("bmidcost", 0, "B per-gated-eval-call cycle cost of the middlegame terms (cycle mode only)")
	cbudget := fs.Uint64("cbudget", 0, "per-move CYCLE budget (>0 selects cycle-budgeted mode, both sides; overrides -budget)")
	aCMCost := fs.Float64("acmcost", 0, "A per-node countermove cycle cost (cycle mode only)")
	bCMCost := fs.Float64("bcmcost", 0, "B per-node countermove cycle cost (cycle mode only)")
	aImp := fs.String("aimp", "", "A improving params: mode,rfp,lmr[,rni1,rni2,futni,lmrextra] mode=1(free)|2(full) (empty = off)")
	bImp := fs.String("bimp", "", "B improving params")
	aSEE := fs.String("asee", "", "A SEE params: mode,margin,deferfw,pruneqs,deferqs mode=1(pawn)|2(attacked)|3(full-bool) (empty = off)")
	bSEE := fs.String("bsee", "", "B SEE params")
	aSEECost := fs.String("aseecost", "", "A SEE cycle costs: gate,call,rescanitem (cycle mode only)")
	bSEECost := fs.String("bseecost", "", "B SEE cycle costs")
	aCkExt := fs.String("ackext", "", "A check-extension params: maxext[,capturesonly] (empty = off)")
	bCkExt := fs.String("bckext", "", "B check-extension params")
	aQH := fs.String("aqh", "", "A quiet-history params: form,part,lmrhigh,lmrlow,lmpthresh form=1(side-to)|2(ptype-to)|3(side-ptype-to) (empty = off)")
	bQH := fs.String("bqh", "", "B quiet-history params")
	aQHCost := fs.String("aqhcost", "", "A quiet-history cycle costs: probe,update,ageperbyte (cycle mode only)")
	bQHCost := fs.String("bqhcost", "", "B quiet-history cycle costs")
	aIIR := fs.String("aiir", "", "A IIR params: minrem[,anywindow] (empty = off)")
	bIIR := fs.String("biir", "", "B IIR params")
	aIIRCost := fs.Float64("aiircost", 0, "A per-full-width-node IIR test cost (cycle mode only)")
	bIIRCost := fs.Float64("biircost", 0, "B per-full-width-node IIR test cost (cycle mode only)")
	aRazor := fs.String("arazor", "", "A razoring margins: m1,m2 centipawns at remaining 1/2, 0 = off at that rem (empty = off)")
	bRazor := fs.String("brazor", "", "B razoring margins")
	aRazorCost := fs.Float64("arazorcost", 0, "A per-test razoring cost (cycle mode only)")
	bRazorCost := fs.Float64("brazorcost", 0, "B per-test razoring cost (cycle mode only)")
	aNullR := fs.String("anullr", "", "A adaptive null-move reduction: deepr,minrem (empty = shipped R=2)")
	bNullR := fs.String("bnullr", "", "B adaptive null-move reduction")
	aTTR := fs.Int("attrepl", 0, "A TT replacement: 1 = depth-preferred + age (0 = shipped always-replace)")
	bTTR := fs.Int("bttrepl", 0, "B TT replacement")
	aTTRCost := fs.Float64("attreplcost", 0, "A per-store TT replacement-policy cost (cycle mode only)")
	bTTRCost := fs.Float64("bttreplcost", 0, "B per-store TT replacement-policy cost (cycle mode only)")
	fs.Parse(args)

	lines, err := mirror.GenOpenings(sprt.Openings, *pairs, *seed)
	check(err)
	a := mirror.PlayerCfg{Features: byte(*aMask), Weights: parseWeights(*aw), Depth: *depth,
		FixFutility: *aFix, LMR: parseLMR(*aLMR), QS: parseQS(*aQS), KB: loadKB(*aKB), Fut: parseFut(*aFut), Ord: parseOrd(*aOrd),
		LMP: parseLMP(*aLMP), Asp: parseAsp(*aAsp), CM: parseCM(*aCM), CMCost: *aCMCost, Improving: parseImp(*aImp),
		SEE: parseSEE(*aSEE), SEECosts: parseSEECost(*aSEECost),
		CheckExt: parseCheckExt(*aCkExt),
		QHist:    parseQHist(*aQH), QHistCosts: parseTriple(*aQHCost, "quiet-history costs"),
		IIR: parseIIR(*aIIR), IIRCost: *aIIRCost,
		Razor: parseRazor(*aRazor), RazorCost: *aRazorCost,
		NullR:  parseNullR(*aNullR),
		TTRepl: mirror.TTReplParams{DepthPref: *aTTR != 0}, TTReplCost: *aTTRCost,
		Extra: parseExtra(*aExtra), EvalTermsCost: *aExtraCost,
		Mopup: parseMopup(*aMop), EG: parseEG(*aEG), EGCost: *aEGCost,
		Mid: parseMid(*aMid), MidCost: *aMidCost,
		NodeBudget: *budget, CycleBudget: *cbudget, MaxIters: *maxiters}
	b := mirror.PlayerCfg{Features: byte(*bMask), Weights: parseWeights(*bw), Depth: *depth,
		FixFutility: *bFix, LMR: parseLMR(*bLMR), QS: parseQS(*bQS), KB: loadKB(*bKB), Fut: parseFut(*bFut), Ord: parseOrd(*bOrd),
		LMP: parseLMP(*bLMP), Asp: parseAsp(*bAsp), CM: parseCM(*bCM), CMCost: *bCMCost, Improving: parseImp(*bImp),
		SEE: parseSEE(*bSEE), SEECosts: parseSEECost(*bSEECost),
		CheckExt: parseCheckExt(*bCkExt),
		QHist:    parseQHist(*bQH), QHistCosts: parseTriple(*bQHCost, "quiet-history costs"),
		IIR: parseIIR(*bIIR), IIRCost: *bIIRCost,
		Razor: parseRazor(*bRazor), RazorCost: *bRazorCost,
		NullR:  parseNullR(*bNullR),
		TTRepl: mirror.TTReplParams{DepthPref: *bTTR != 0}, TTReplCost: *bTTRCost,
		Extra: parseExtra(*bExtra), EvalTermsCost: *bExtraCost,
		Mopup: parseMopup(*bMop), EG: parseEG(*bEG), EGCost: *bEGCost,
		Mid: parseMid(*bMid), MidCost: *bMidCost,
		NodeBudget: *budget, CycleBudget: *cbudget, MaxIters: *maxiters}
	start := time.Now()
	res, err := mirror.Match(a, b, lines, *pairs, *workers, *seed)
	check(err)
	mode := fmt.Sprintf("depth %d", *depth)
	if *budget > 0 {
		mode = fmt.Sprintf("budget %d nodes/move (maxiters %d)", *budget, *maxiters)
	}
	if *cbudget > 0 {
		mode = fmt.Sprintf("budget %d cycles/move (maxiters %d)", *cbudget, *maxiters)
	}
	fmt.Printf("A(%#02x %s fix=%v lmr=%q qs=%q ord=%q lmp=%q asp=%q cm=%q imp=%q see=%q ckext=%q qh=%q iir=%q razor=%q nullr=%q ttrepl=%d extra=%q mop=%q eg=%q mid=%q) vs B(%#02x %s fix=%v lmr=%q qs=%q ord=%q lmp=%q asp=%q cm=%q imp=%q see=%q ckext=%q qh=%q iir=%q razor=%q nullr=%q ttrepl=%d extra=%q mop=%q eg=%q mid=%q) %s: %s (%v)\n",
		byte(*aMask), *aw, *aFix, *aLMR, *aQS, *aOrd, *aLMP, *aAsp, *aCM, *aImp, *aSEE, *aCkExt, *aQH, *aIIR, *aRazor, *aNullR, *aTTR, *aExtra, *aMop, *aEG, *aMid,
		byte(*bMask), *bw, *bFix, *bLMR, *bQS, *bOrd, *bLMP, *bAsp, *bCM, *bImp, *bSEE, *bCkExt, *bQH, *bIIR, *bRazor, *bNullR, *bTTR, *bExtra, *bMop, *bEG, *bMid,
		mode, res, time.Since(start).Round(time.Second))
}

// loadKB loads a king-bucket table file, or nil when path is empty.
func loadKB(path string) *mirror.KBTables {
	if path == "" {
		return nil
	}
	t, err := mirror.LoadKB(path)
	check(err)
	return t
}

// genfen self-plays a chunk of games and writes quiet FEN+result rows
// (the corpus for king-bucketed PSQT tuning, which needs board
// placement the pawn-feature rows discarded).
func genfen(args []string) {
	fs := flag.NewFlagSet("genfen", flag.ExitOnError)
	depth := fs.Int("depth", 5, "self-play depth")
	games := fs.Int("games", 2000, "games this chunk")
	workers := fs.Int("workers", runtime.NumCPU()-1, "parallel games")
	seed := fs.Uint64("seed", 6502, "RNG seed")
	openings := fs.Int("openings", 400, "generated opening lines")
	out := fs.String("out", "fenrows.gz", "output file (gzip R fen lines)")
	fs.Parse(args)

	lines, err := mirror.GenOpenings(sprt.Openings, *openings, *seed)
	check(err)
	start := time.Now()
	rows, err := mirror.GenerateFENData(lines, mirror.DefaultWeights, *depth,
		*games, *workers, *seed, func(g, n int) {
			fmt.Printf("  %d games, %d rows (%v)\n", g, n, time.Since(start).Round(time.Second))
		})
	check(err)
	check(mirror.WriteFenRows(*out, rows))
	fmt.Printf("genfen done: %d games, %d rows to %s in %v\n",
		*games, len(rows), *out, time.Since(start).Round(time.Second))
}

// tunekb tunes the king-bucketed PSQT deltas from a FEN corpus (self-
// play + optional pool PGNs), with a train/val split to watch overfit,
// and saves the tuned tables for the match driver.
func tunekb(args []string) {
	fs := flag.NewFlagSet("tunekb", flag.ExitOnError)
	fen := fs.String("fen", "", "self-play FEN corpus (gzip R fen)")
	pool := fs.String("pool", "", "glob of pool PGNs to fold in (optional)")
	lambda := fs.Float64("lambda", 0.002, "L2 regularization")
	lr := fs.Float64("lr", 0.05, "Adam learning rate")
	iters := fs.Int("iters", 400, "gradient-descent iterations")
	valFrac := fs.Float64("val", 0.1, "holdout fraction for validation")
	workers := fs.Int("workers", runtime.NumCPU()-1, "parallel example build")
	out := fs.String("out", "", "output KB table file (gob); empty = don't save")
	fs.Parse(args)

	var rows []mirror.FenRow
	if *fen != "" {
		r, err := mirror.ReadFenRows(*fen)
		check(err)
		rows = append(rows, r...)
		fmt.Printf("self-play rows: %d\n", len(r))
	}
	if *pool != "" {
		paths, _ := filepath.Glob(*pool)
		var pn int
		for _, p := range paths {
			r, _, err := mirror.PGNFenRows(p)
			check(err)
			rows = append(rows, r...)
			pn += len(r)
		}
		fmt.Printf("pool rows: %d from %d files\n", pn, len(paths))
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stderr, "tunekb: no rows")
		os.Exit(2)
	}

	// Deterministic shuffle + split.
	rnd := rand.New(rand.NewPCG(0x30, 0xbadc0de))
	rnd.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	nVal := int(*valFrac * float64(len(rows)))
	valRows, trRows := rows[:nVal], rows[nVal:]

	start := time.Now()
	trExs, err := mirror.BuildKBExamples(trRows, mirror.TunedWeights, *workers)
	check(err)
	valExs, err := mirror.BuildKBExamples(valRows, mirror.TunedWeights, *workers)
	check(err)
	fmt.Printf("built %d train + %d val examples (%v)\n", len(trExs), len(valExs), time.Since(start).Round(time.Second))

	k := mirror.FitKBK(trExs)
	fmt.Printf("fitted K = %.2f, lambda = %g, lr = %g, iters = %d\n", k, *lambda, *lr, *iters)

	valBefore := mirror.KBLoss(valExs, nil, k)
	params, kb, trBefore, trAfter := mirror.TuneKB(trExs, k, *lambda, *lr, *iters,
		func(it int, loss float64, cur []float64) {
			fmt.Printf("  iter %4d: train %.6f  val %.6f\n", it, loss, mirror.KBLoss(valExs, cur, k))
		})
	valAfter := mirror.KBLoss(valExs, params, k)
	maxAbs, nz := mirror.KBStats(kb)
	fmt.Printf("train loss: %.6f -> %.6f\n", trBefore, trAfter)
	fmt.Printf("val   loss: %.6f -> %.6f\n", valBefore, valAfter)
	fmt.Printf("KB tables: %d nonzero entries, max |delta| = %d\n", nz, maxAbs)
	if *out != "" {
		check(mirror.SaveKB(*out, kb))
		fmt.Printf("saved KB tables to %s\n", *out)
	}
}

// parseFut parses "guard,rfp1,rfp2,rfp3,rfp4,fut,maxrem" into a
// FutilityParams; empty means the shipped default (nil).
func parseFut(s string) *mirror.FutilityParams {
	if s == "" {
		return nil
	}
	var guard, r1, r2, r3, r4, fut, maxRem int
	n, err := fmt.Sscanf(s, "%d,%d,%d,%d,%d,%d,%d", &guard, &r1, &r2, &r3, &r4, &fut, &maxRem)
	if err != nil || n != 7 {
		fmt.Fprintf(os.Stderr, "bad futility params %q (want guard,rfp1,rfp2,rfp3,rfp4,fut,maxrem)\n", s)
		os.Exit(2)
	}
	return &mirror.FutilityParams{
		CorrectGuard: guard != 0,
		RFP:          [8]int{0, r1, r2, r3, r4},
		MaxRem:       maxRem,
		Fut:          fut,
	}
}

// parseOrd parses "losinglast,histmalus" (e.g. "1,1"); empty = zero.
func parseOrd(s string) mirror.OrderParams {
	if s == "" {
		return mirror.OrderParams{}
	}
	var ll, hm int
	n, err := fmt.Sscanf(s, "%d,%d", &ll, &hm)
	if err != nil || n != 2 {
		fmt.Fprintf(os.Stderr, "bad ordering params %q (want losinglast,histmalus)\n", s)
		os.Exit(2)
	}
	return mirror.OrderParams{LosingLast: ll != 0, HistMalus: hm != 0}
}

// parseLMP parses "dmax,base,mult,quad,exemptkillers,keepchecks" into an
// LMPParams; empty means off (zero value).
func parseLMP(s string) mirror.LMPParams {
	if s == "" {
		return mirror.LMPParams{}
	}
	var dmax, base, mult, quad, ek, kc int
	n, err := fmt.Sscanf(s, "%d,%d,%d,%d,%d,%d", &dmax, &base, &mult, &quad, &ek, &kc)
	if err != nil || n != 6 {
		fmt.Fprintf(os.Stderr, "bad LMP params %q (want dmax,base,mult,quad,exemptkillers,keepchecks)\n", s)
		os.Exit(2)
	}
	return mirror.LMPParams{Dmax: dmax, Base: base, Mult: mult, Quad: quad,
		ExemptKillers: ek != 0, KeepChecks: kc != 0}
}

// parseCM parses "indexing[,beforekillers[,persist]]" into a
// CountermoveParams; empty means off (zero value). indexing: 1 = [to],
// 2 = [piece-type][to].
func parseCM(s string) mirror.CountermoveParams {
	if s == "" {
		return mirror.CountermoveParams{}
	}
	var indexing, bk, persist int
	n, err := fmt.Sscanf(s, "%d,%d,%d", &indexing, &bk, &persist)
	if (err != nil && n < 1) || n < 1 {
		fmt.Fprintf(os.Stderr, "bad countermove params %q (want indexing[,beforekillers[,persist]])\n", s)
		os.Exit(2)
	}
	return mirror.CountermoveParams{Indexing: indexing, BeforeKillers: bk != 0, Persist: persist != 0}
}

// parseImp parses the improving heuristic params:
//
//	mode,rfp,lmr[,rni1,rni2,futni,lmrextra]
//
// mode = 1 (free-signal) | 2 (full-signal); rfp/lmr are 0/1 flags selecting
// the RFP/futility and LMR applications. The optional tail sets the
// not-improving RFP margins (rni1 @ remaining 1, rni2 @ remaining 2), the
// not-improving leaf-futility margin (futni), and the extra LMR reduction
// plies (lmrextra, default 1). Empty means off (zero value).
func parseImp(s string) mirror.ImprovingParams {
	if s == "" {
		return mirror.ImprovingParams{}
	}
	var mode, rfp, lmr, rni1, rni2, futni, lmrextra int
	n, err := fmt.Sscanf(s, "%d,%d,%d,%d,%d,%d,%d",
		&mode, &rfp, &lmr, &rni1, &rni2, &futni, &lmrextra)
	if err != nil && n < 3 {
		fmt.Fprintf(os.Stderr, "bad improving params %q (want mode,rfp,lmr[,rni1,rni2,futni,lmrextra])\n", s)
		os.Exit(2)
	}
	return mirror.ImprovingParams{
		Mode:     mode,
		RFP:      rfp != 0,
		RFPNI:    [8]int{0, rni1, rni2},
		FutNI:    futni,
		LMR:      lmr != 0,
		LMRExtra: lmrextra,
	}
}

// parseCheckExt parses "maxext[,capturesonly]" into a CheckExtParams;
// empty means off (zero value). maxext caps check extensions per path;
// capturesonly (0/1) restricts extensions to checking captures.
func parseCheckExt(s string) mirror.CheckExtParams {
	if s == "" {
		return mirror.CheckExtParams{}
	}
	var maxext, capsOnly int
	n, err := fmt.Sscanf(s, "%d,%d", &maxext, &capsOnly)
	if (err != nil && n < 1) || n < 1 {
		fmt.Fprintf(os.Stderr, "bad check-extension params %q (want maxext[,capturesonly])\n", s)
		os.Exit(2)
	}
	return mirror.CheckExtParams{MaxExt: maxext, CapturesOnly: capsOnly != 0}
}

// parseSEE parses "mode,margin,deferfw,pruneqs,deferqs" into an
// SEEParams; empty means off (zero value).
func parseSEE(s string) mirror.SEEParams {
	if s == "" {
		return mirror.SEEParams{}
	}
	var mode, margin, dfw, pqs, dqs int
	n, err := fmt.Sscanf(s, "%d,%d,%d,%d,%d", &mode, &margin, &dfw, &pqs, &dqs)
	if err != nil || n != 5 {
		fmt.Fprintf(os.Stderr, "bad SEE params %q (want mode,margin,deferfw,pruneqs,deferqs)\n", s)
		os.Exit(2)
	}
	return mirror.SEEParams{Mode: mode, Margin: margin,
		DeferFW: dfw != 0, PruneQS: pqs != 0, DeferQS: dqs != 0}
}

// parseSEECost parses "gate,call,rescanitem" into the SEE cost triple;
// empty means untaxed (zeros).
func parseSEECost(s string) [3]float64 {
	if s == "" {
		return [3]float64{}
	}
	var g, c, r float64
	n, err := fmt.Sscanf(s, "%g,%g,%g", &g, &c, &r)
	if err != nil || n != 3 {
		fmt.Fprintf(os.Stderr, "bad SEE costs %q (want gate,call,rescanitem)\n", s)
		os.Exit(2)
	}
	return [3]float64{g, c, r}
}

// parseQHist parses "form,part,lmrhigh,lmrlow,lmpthresh" into a
// QHistParams; empty means off (zero value). Trailing fields default to 0
// (that use disabled).
func parseQHist(s string) mirror.QHistParams {
	if s == "" {
		return mirror.QHistParams{}
	}
	var form, part, lmrhigh, lmrlow, lmpthresh int
	n, err := fmt.Sscanf(s, "%d,%d,%d,%d,%d", &form, &part, &lmrhigh, &lmrlow, &lmpthresh)
	if n < 1 || (err != nil && n < 1) || form < 1 || form > 3 {
		fmt.Fprintf(os.Stderr, "bad quiet-history params %q (want form,part,lmrhigh,lmrlow,lmpthresh)\n", s)
		os.Exit(2)
	}
	return mirror.QHistParams{Form: form, PartThresh: part, LMRHigh: lmrhigh,
		LMRLowThresh: lmrlow, LMPThresh: lmpthresh}
}

// parseTriple parses "a,b,c" into a [3]float64; empty means all zero.
func parseTriple(s, what string) [3]float64 {
	if s == "" {
		return [3]float64{}
	}
	var a, b, c float64
	n, err := fmt.Sscanf(s, "%g,%g,%g", &a, &b, &c)
	if err != nil || n != 3 {
		fmt.Fprintf(os.Stderr, "bad %s %q (want three comma-separated numbers)\n", what, s)
		os.Exit(2)
	}
	return [3]float64{a, b, c}
}

// parseIIR parses "minrem[,anywindow]" into an IIRParams; empty means off.
func parseIIR(s string) mirror.IIRParams {
	if s == "" {
		return mirror.IIRParams{}
	}
	var minrem, anyw int
	n, err := fmt.Sscanf(s, "%d,%d", &minrem, &anyw)
	if (err != nil && n < 1) || n < 1 {
		fmt.Fprintf(os.Stderr, "bad IIR params %q (want minrem[,anywindow])\n", s)
		os.Exit(2)
	}
	return mirror.IIRParams{MinRem: minrem, AnyWindow: anyw != 0}
}

// parseRazor parses "m1,m2" into a RazorParams; empty means off.
func parseRazor(s string) mirror.RazorParams {
	if s == "" {
		return mirror.RazorParams{}
	}
	var m1, m2 int
	n, err := fmt.Sscanf(s, "%d,%d", &m1, &m2)
	if err != nil || n != 2 {
		fmt.Fprintf(os.Stderr, "bad razoring margins %q (want m1,m2)\n", s)
		os.Exit(2)
	}
	return mirror.RazorParams{M1: m1, M2: m2}
}

// parseNullR parses "deepr,minrem" into a NullRParams; empty means the
// shipped fixed R=2.
func parseNullR(s string) mirror.NullRParams {
	if s == "" {
		return mirror.NullRParams{}
	}
	var deepr, minrem int
	n, err := fmt.Sscanf(s, "%d,%d", &deepr, &minrem)
	if err != nil || n != 2 {
		fmt.Fprintf(os.Stderr, "bad null-R params %q (want deepr,minrem)\n", s)
		os.Exit(2)
	}
	return mirror.NullRParams{DeepR: deepr, MinRem: minrem}
}

// parseAsp parses "delta[,policy]" into an AspirationParams; empty means
// off (zero value). policy is full (default), prog, or asym.
func parseAsp(s string) mirror.AspirationParams {
	if s == "" {
		return mirror.AspirationParams{}
	}
	var delta int
	var pol string
	n, err := fmt.Sscanf(s, "%d,%s", &delta, &pol)
	if err != nil && n < 1 {
		fmt.Fprintf(os.Stderr, "bad aspiration params %q (want delta[,policy])\n", s)
		os.Exit(2)
	}
	policy := mirror.AspFull
	switch pol {
	case "", "full":
		policy = mirror.AspFull
	case "prog":
		policy = mirror.AspProgressive
	case "asym":
		policy = mirror.AspAsym
	default:
		fmt.Fprintf(os.Stderr, "bad aspiration policy %q (want full|prog|asym)\n", pol)
		os.Exit(2)
	}
	return mirror.AspirationParams{Delta: delta, Policy: policy}
}

// parseExtra parses the experimental cheap eval terms (task #18/#51) as a
// comma-separated list of key:value pairs, e.g. "bishop:30" or
// "ropen:25,rsemi:12,drook:15,block:18". An empty string leaves every term
// zero (off), a byte-identical no-op vs the shipped eval. Weights are in
// centipawns (pawn ~= 100).
func parseExtra(s string) mirror.EvalTerms {
	var t mirror.EvalTerms
	if s == "" {
		return t
	}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, ":")
		var val int
		if !ok || func() bool { _, e := fmt.Sscanf(v, "%d", &val); return e != nil }() {
			fmt.Fprintf(os.Stderr, "bad extra term %q (want key:val)\n", kv)
			os.Exit(2)
		}
		switch k {
		case "ropen":
			t.RookOpen = val
		case "rsemi":
			t.RookSemi = val
		case "bishop", "bishoppair":
			t.BishopPair = val
		case "rook7":
			t.Rook7th = val
		case "drook":
			t.DoubledRk = val
		case "block":
			t.Blockade = val
		case "tropism":
			t.Tropism = val
		default:
			fmt.Fprintf(os.Stderr, "unknown extra term key %q (want ropen,rsemi,bishop,rook7,drook,block,tropism)\n", k)
			os.Exit(2)
		}
	}
	return t
}

// parseMopup parses the endgame mop-up config: "" or "off" = OFF (the
// zero value), "default" = DefaultMopup (the SHIPPED asm term — use it on
// BOTH sides of any endgame screen so the baseline is the real engine),
// or an explicit "edge,close,matthresh,phasemax".
func parseMopup(s string) mirror.MopupParams {
	switch s {
	case "", "off":
		return mirror.MopupParams{}
	case "default", "on":
		return mirror.DefaultMopup
	}
	m := mirror.MopupParams{Enable: true}
	n, err := fmt.Sscanf(s, "%d,%d,%d,%d", &m.Edge, &m.Close, &m.MatThreshold, &m.PhaseMax)
	if err != nil || n != 4 {
		fmt.Fprintf(os.Stderr, "bad mopup params %q (want off|default|edge,close,matthresh,phasemax)\n", s)
		os.Exit(2)
	}
	return m
}

// parseEG parses the endgame-technique terms: "" = off, "default" =
// DefaultEndgame, else a comma-separated key:val list starting from the
// default gate (phase 6) with every WEIGHT zero, so a single term can be
// screened in isolation (e.g. "kcent:8" or "unstop:250,pass:1").
// "pass:N" scales the built-in monotone advancement table by N/1 (N=1 is
// the default table; N=0 disables it).
func parseEG(s string) mirror.EndgameParams {
	switch s {
	case "", "off":
		return mirror.EndgameParams{}
	case "default", "on", "all":
		return mirror.DefaultEndgame
	case "designed":
		return mirror.EndgameDesigned
	}
	t := mirror.EndgameParams{Enable: true, PhaseMax: mirror.DefaultEndgame.PhaseMax}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, ":")
		var val int
		if !ok || func() bool { _, e := fmt.Sscanf(v, "%d", &val); return e != nil }() {
			fmt.Fprintf(os.Stderr, "bad endgame term %q (want key:val)\n", kv)
			os.Exit(2)
		}
		switch k {
		case "phase":
			t.PhaseMax = val
		case "kcent":
			t.KingCent = val
		case "kpawn":
			t.KingPawn = val
		case "pass":
			for i, p := range mirror.EndgameDesigned.Pass {
				t.Pass[i] = p * val
			}
		case "pking":
			t.PassKingOur = val
		case "pkingt":
			t.PassKingThem = val
		case "kahead":
			t.KingAhead = val
		case "unstop":
			t.Unstoppable = val
		case "rbehind":
			t.RookBehind = val
		default:
			fmt.Fprintf(os.Stderr, "unknown endgame term key %q (want phase,kcent,kpawn,pass,pking,pkingt,kahead,unstop,rbehind)\n", k)
			os.Exit(2)
		}
	}
	return t
}

// parseMid parses the MIDDLEGAME term set (midgame.go): "default"/"all",
// "ks" (king-safety group only), "pos" (positional group only), "off", or
// key:val pairs to screen individual terms. Keys: phase, ksatk (scales the
// built-in attack-unit table by val/8, val=8 = the default curve), ksdef,
// ksopen, ksfull, ksgap, ksexp, outn, outb, back, phal, badb, blkctr.
func parseMid(s string) mirror.MidParams {
	switch s {
	case "", "off":
		return mirror.MidParams{}
	case "default", "on", "all":
		return mirror.DefaultMid
	case "ks":
		return mirror.MidKingSafety
	case "pos":
		return mirror.MidPositional
	}
	t := mirror.MidParams{Enable: true, PhaseMin: mirror.DefaultMid.PhaseMin}
	for _, kv := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(kv, ":")
		var val int
		if !ok || func() bool { _, e := fmt.Sscanf(v, "%d", &val); return e != nil }() {
			fmt.Fprintf(os.Stderr, "bad midgame term %q (want key:val)\n", kv)
			os.Exit(2)
		}
		switch k {
		case "phase":
			t.PhaseMin = val
		case "ksatk":
			for i, p := range mirror.DefaultMid.KSAtk {
				t.KSAtk[i] = p * val / 8
			}
		case "ksdef":
			t.KSDefend = val != 0
		case "ksopen":
			t.KSOpen = val
		case "ksfull":
			t.KSFullOpen = val
		case "ksgap":
			t.KSGap = val
		case "kspawn":
			t.KSPawn = val
		case "ksexp":
			t.KSExposed = val
		case "outn":
			t.OutpostN = val
		case "outb":
			t.OutpostB = val
		case "back":
			t.Backward = val
		case "phal":
			t.Phalanx = val
		case "badb":
			t.BadBishop = val
		case "blkctr":
			t.BlockedCtr = val
		default:
			fmt.Fprintf(os.Stderr, "unknown midgame term key %q\n", k)
			os.Exit(2)
		}
	}
	return t
}

// parseQS parses "plycap,recapafter[,checks[,safechecks]]". The last two
// fields (task #37 quiet-checks-in-QS) are optional and default to off.
func parseQS(s string) mirror.QSParams {
	var q mirror.QSParams
	var safe int
	n, _ := fmt.Sscanf(s, "%d,%d,%d,%d", &q.PlyCap, &q.RecapAfter, &q.Checks, &safe)
	if n < 2 {
		fmt.Fprintf(os.Stderr, "bad QS params %q (want plycap,recapafter[,checks[,safechecks]])\n", s)
		os.Exit(2)
	}
	q.SafeChecks = safe != 0
	// "0,0" is the documented UNLIMITED-QS experiment baseline. Since
	// PlayerCfg's zero value now means the SHIPPED shape (DefaultQS/recap2 —
	// the old zero-is-unlimited semantics silently screened a fictional
	// engine), map it to the explicit sentinel so the CLI contract holds.
	if q == (mirror.QSParams{}) {
		return mirror.QSUnlimited
	}
	return q
}

// parseLMR parses "late1,late2,rem1,rem2,killers,evasion" (e.g.
// "3,6,3,5,0,1"); empty means the asm's current rules.
func parseLMR(s string) *mirror.LMRParams {
	if s == "" {
		return nil
	}
	var p mirror.LMRParams
	var k, e int
	n, err := fmt.Sscanf(s, "%d,%d,%d,%d,%d,%d",
		&p.LateR1, &p.LateR2, &p.MinRemR1, &p.MinRemR2, &k, &e)
	if err != nil || n != 6 {
		fmt.Fprintf(os.Stderr, "bad LMR params %q\n", s)
		os.Exit(2)
	}
	p.ReduceKillers = k != 0
	p.EvasionPVS = e != 0
	return &p
}

func parseWeights(s string) mirror.Weights {
	switch s {
	case "default":
		return mirror.DefaultWeights
	case "tuned":
		return mirror.TunedWeights
	}
	var w mirror.Weights
	n, err := fmt.Sscanf(s, "w:%d,%d,%d,%d,%d,%d,%d,%d,%d,%d",
		&w.Doubled, &w.Isolated, &w.Passed[1], &w.Passed[2], &w.Passed[3],
		&w.Passed[4], &w.Passed[5], &w.Passed[6], &w.Shield, &w.OpenFile)
	if err != nil || n != 10 {
		fmt.Fprintf(os.Stderr, "bad weights %q (want w:d,i,p1,p2,p3,p4,p5,p6,s,o)\n", s)
		os.Exit(2)
	}
	return w
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
