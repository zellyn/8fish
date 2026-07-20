// Command sprt runs a self-play feature-gate match between two feature
// configurations of the engine (see internal/sprt). Example:
//
//	go run ./cmd/sprt -a 0x07 -b 0x06 -budget 5000 -pairs 100
//
// tests all-features (A) against everything-but-null-move (B).
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/sprt"
)

func main() {
	var (
		binfile   = flag.String("bin", "asm/engine.bin", "engine binary")
		defsfile  = flag.String("defs", "asm/defs.inc", "memory-layout defs")
		binBfile  = flag.String("binB", "", "side-B engine binary (default: same as -bin)")
		defsBfile = flag.String("defsB", "", "side-B memory-layout defs (required with -binB)")
		aBits    = flag.String("a", "0x07", "feature bits for side A")
		bBits    = flag.String("b", "0x00", "feature bits for side B")
		budgetMs = flag.Uint64("budget", 5000, "emulated ms per move")
		pairs    = flag.Int("pairs", 50, "opening pairs (games = 2x)")
		parallel = flag.Int("parallel", runtime.NumCPU()-2, "concurrent games")
	)
	flag.Parse()

	bin, err := os.ReadFile(*binfile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defs, err := chesstest.ParseDefs(*defsfile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	a, err := strconv.ParseUint(*aBits, 0, 8)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b, err := strconv.ParseUint(*bBits, 0, 8)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var binB []byte
	var defsB chesstest.Defs
	if *binBfile != "" {
		if binB, err = os.ReadFile(*binBfile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if defsB, err = chesstest.ParseDefs(*defsBfile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	res := sprt.Run(sprt.Config{
		Bin:          bin,
		Defs:         defs,
		BinB:         binB,
		DefsB:        defsB,
		FeaturesA:    byte(a),
		FeaturesB:    byte(b),
		BudgetCycles: *budgetMs * chesstest.CyclesPerMs,
		Pairs:        *pairs,
		Parallel:     *parallel,
	})
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", e)
	}
	elo, margin := res.EloDiff()
	fmt.Printf("A(%#02x) vs B(%#02x) @ %dms: +%d =%d -%d  score %.1f%%  elo %+.0f +/- %.0f  llr(0,10) %.2f\n",
		a, b, *budgetMs, res.Wins, res.Draws, res.Losses,
		100*res.Score(), elo, margin, res.LLR(0, 10))
	if len(res.Errors) > 0 {
		os.Exit(1)
	}
}
