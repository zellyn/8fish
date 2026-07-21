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
	default:
		usage()
	}
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
	cbudget := fs.Uint64("cbudget", 0, "per-move CYCLE budget (>0 selects cycle-budgeted mode, both sides; overrides -budget)")
	aCMCost := fs.Float64("acmcost", 0, "A per-node countermove cycle cost (cycle mode only)")
	bCMCost := fs.Float64("bcmcost", 0, "B per-node countermove cycle cost (cycle mode only)")
	aImp := fs.String("aimp", "", "A improving params: mode,rfp,lmr[,rni1,rni2,futni,lmrextra] mode=1(free)|2(full) (empty = off)")
	bImp := fs.String("bimp", "", "B improving params")
	fs.Parse(args)

	lines, err := mirror.GenOpenings(sprt.Openings, *pairs, *seed)
	check(err)
	a := mirror.PlayerCfg{Features: byte(*aMask), Weights: parseWeights(*aw), Depth: *depth,
		FixFutility: *aFix, LMR: parseLMR(*aLMR), QS: parseQS(*aQS), KB: loadKB(*aKB), Fut: parseFut(*aFut), Ord: parseOrd(*aOrd),
		LMP: parseLMP(*aLMP), Asp: parseAsp(*aAsp), CM: parseCM(*aCM), CMCost: *aCMCost, Improving: parseImp(*aImp),
		NodeBudget: *budget, CycleBudget: *cbudget, MaxIters: *maxiters}
	b := mirror.PlayerCfg{Features: byte(*bMask), Weights: parseWeights(*bw), Depth: *depth,
		FixFutility: *bFix, LMR: parseLMR(*bLMR), QS: parseQS(*bQS), KB: loadKB(*bKB), Fut: parseFut(*bFut), Ord: parseOrd(*bOrd),
		LMP: parseLMP(*bLMP), Asp: parseAsp(*bAsp), CM: parseCM(*bCM), CMCost: *bCMCost, Improving: parseImp(*bImp),
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
	fmt.Printf("A(%#02x %s fix=%v lmr=%q qs=%q ord=%q lmp=%q asp=%q cm=%q imp=%q) vs B(%#02x %s fix=%v lmr=%q qs=%q ord=%q lmp=%q asp=%q cm=%q imp=%q) %s: %s (%v)\n",
		byte(*aMask), *aw, *aFix, *aLMR, *aQS, *aOrd, *aLMP, *aAsp, *aCM, *aImp, byte(*bMask), *bw, *bFix, *bLMR, *bQS, *bOrd, *bLMP, *bAsp, *bCM, *bImp,
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
