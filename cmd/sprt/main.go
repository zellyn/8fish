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

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/sprt"
)

func main() {
	var (
		binfile   = flag.String("bin", "asm/engine.bin", "engine binary")
		defsfile  = flag.String("defs", "asm/defs.inc", "memory-layout defs")
		binBfile  = flag.String("binB", "", "side-B engine binary (default: same as -bin)")
		defsBfile = flag.String("defsB", "", "side-B memory-layout defs (required with -binB)")
		aBits     = flag.String("a", "0x07", "feature bits for side A")
		bBits     = flag.String("b", "0x00", "feature bits for side B")
		aBits2    = flag.String("a2", "0x00", "second feature byte (FT2_*) for side A")
		bBits2    = flag.String("b2", "0x00", "second feature byte (FT2_*) for side B")
		budgetMs  = flag.Uint64("budget", 5000, "emulated ms per move")
		pairs     = flag.Int("pairs", 50, "opening pairs (games = 2x)")
		parallel  = flag.Int("parallel", runtime.NumCPU()-2, "concurrent games")
		openseed  = flag.Uint64("openseed", 0, "opening-generation seed (0 = historical default 42)")
		pergame   = flag.Bool("pergame", false, "per-GAME cycle-bank mode: -budget is each side's per-move income into a per-game bank (needed to exercise time management)")
		adaptA    = flag.Bool("adaptA", false, "side A runs on-device FT2_ADAPT adaptive time management (per-game mode)")
		adaptB    = flag.Bool("adaptB", false, "side B runs on-device FT2_ADAPT adaptive time management (per-game mode)")
		lblfile   = flag.String("lbl", "asm/engine.lbl", "label file (supplies the asm `bookentry` address; needed with -bookA/-bookB)")
		bookA     = flag.String("bookA", "", "side-A resident book blob (\"default\" = the embedded one; empty = no book)")
		bookB     = flag.String("bookB", "", "side-B resident book blob (\"default\" = the embedded one; empty = no book)")
		bookSeed  = flag.Uint64("book-seed", 1, "seed for the per-game weighted book pick")
		dither    = flag.Bool("dither", false, "poke a fresh eval-dither SEED before every search (as the UCI bridge does); implied by -bookA/-bookB")
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
	a2, err := strconv.ParseUint(*aBits2, 0, 8)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b2, err := strconv.ParseUint(*bBits2, 0, 8)
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

	// Book A/B. An ordinary SPRT cannot see a book change (both sides share
	// one book and cancel), so the two sides load DIFFERENT blobs and the
	// forced opening prefix is switched off — otherwise the prefix plays
	// straight through the region the book is choosing.
	loadBook := func(path string) *book.Book {
		switch path {
		case "":
			return nil
		case "default":
			bk, err := book.Default()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return bk
		}
		blob, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		bk, err := book.Load(blob)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		return bk
	}
	bkA, bkB := loadBook(*bookA), loadBook(*bookB)
	var bookEntry uint16
	if bkA != nil || bkB != nil {
		labels, err := chesstest.ParseLabelFile(*lblfile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if bookEntry = labels["bookentry"]; bookEntry == 0 {
			fmt.Fprintf(os.Stderr, "%s: no `bookentry` label\n", *lblfile)
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
		FeaturesA2:   byte(a2),
		FeaturesB2:   byte(b2),
		BudgetCycles: *budgetMs * chesstest.CyclesPerMs,
		Pairs:        *pairs,
		Parallel:     *parallel,
		OpenSeed:     *openseed,
		PerGame:      *pergame,
		AdaptiveA:    *adaptA,
		AdaptiveB:    *adaptB,
		BookA:        bkA,
		BookB:        bkB,
		BookEntry:    bookEntry,
		BookSeed:     *bookSeed,
		NoOpening:    bkA != nil || bkB != nil,
		Dither:       *dither || bkA != nil || bkB != nil,
	})
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", e)
	}
	if bkA != nil || bkB != nil {
		// Instrument check, printed BEFORE the result: if these are equal or
		// zero the match measured nothing and the Elo below is meaningless.
		fmt.Printf("book moves played: A %d (%d entries) | B %d (%d entries)\n",
			res.ABookMoves, bookEntryCount(bkA), res.BBookMoves, bookEntryCount(bkB))
	}
	elo, margin := res.EloDiff()
	fmt.Printf("A(%#02x) vs B(%#02x) @ %dms: +%d =%d -%d  score %.1f%%  elo %+.0f +/- %.0f  llr(0,10) %.2f\n",
		a, b, *budgetMs, res.Wins, res.Draws, res.Losses,
		100*res.Score(), elo, margin, res.LLR(0, 10))
	// Equal-total-spend check: in per-game bank mode the conserving (signed)
	// bank must make the adaptive and flat sides spend the same grand total
	// compute (within the minSpend-floor residual) — otherwise the Elo is
	// confounded by a compute advantage rather than a pure time-allocation one.
	if res.ACycles > 0 && res.BCycles > 0 {
		income := *budgetMs * chesstest.CyclesPerMs
		aInt := income * uint64(res.AMoves)
		bInt := income * uint64(res.BMoves)
		aAdh := float64(res.ACycles) / float64(aInt) // adaptive own_total/own_intended
		bAdh := float64(res.BCycles) / float64(bInt) // flat own_total/own_intended
		ab := float64(res.ACycles) / float64(res.BCycles)
		fmt.Printf("  spend: A_total=%d (moves=%d intended=%d adherence=%.4f)\n",
			res.ACycles, res.AMoves, aInt, aAdh)
		fmt.Printf("         B_total=%d (moves=%d intended=%d adherence=%.4f)\n",
			res.BCycles, res.BMoves, bInt, bAdh)
		fmt.Printf("         equal-total-spend A/B=%.4f (%.2f%%)\n", ab, (ab-1)*100)
	}
	if len(res.Errors) > 0 {
		os.Exit(1)
	}
}

// bookEntryCount is 0 for a nil book, for the instrument line above.
func bookEntryCount(b *book.Book) int {
	if b == nil {
		return 0
	}
	return len(b.Entries())
}
