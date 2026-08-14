// Command uci presents the emulated 6502 chess engine as a UCI engine
// (for cutechess-cli, lichess-bot, and friends). See internal/ucibridge.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zellyn/8fish/internal/book"
	"github.com/zellyn/8fish/internal/chesstest"
	"github.com/zellyn/8fish/internal/ucibridge"
)

func main() {
	var (
		binfile    = flag.String("bin", "asm/engine.bin", "engine binary")
		defsfile   = flag.String("defs", "asm/defs.inc", "memory-layout defs")
		lblfile    = flag.String("lbl", "asm/engine.lbl", "engine label file (for the book probe entry point)")
		budget     = flag.Uint64("budget", 0, "fixed emulated ms per move (0: derive from go command)")
		dither     = flag.Bool("dither", false, "seed per-move eval dither (breaks deterministic repetition)")
		ditherSrc  = flag.String("dither-source", ucibridge.DitherEntropy, "where the dither seed comes from: entropy (hardware-faithful keystroke-timing collector), prng (host PCG)")
		ditherSeed = flag.Uint64("dither-seed", 0, "with -dither-source prng: pin the stream for a reproducible run")
		bank       = flag.Bool("bank", false, "chess-clock banking: unused budget carries to later moves")
		adaptive   = flag.Bool("adaptive", false, "adaptive time management (FT2_ADAPT): per-game cycle bank + movable ceiling; supersedes -bank")
		usebook    = flag.Bool("book", false, "play the resident opening book before searching")
		bookSeed   = flag.Uint64("book-seed", 0, "seed for the book's weighted-random pick (varied per game automatically)")
		ponder     = flag.Bool("ponder", false, "self-ponder during the opponent's turn and measure the ponder-hit rate (for gauntlets)")
		ponderMs   = flag.Uint64("ponder-budget-ms", 0, "emulated-ms budget for each ponder search (set to the opponent's per-move think time)")
	)
	flag.Parse()

	switch *ditherSrc {
	case ucibridge.DitherEntropy, ucibridge.DitherPRNG, ucibridge.DitherOff:
	default:
		fmt.Fprintf(os.Stderr, "-dither-source: want %q, %q or %q, got %q\n",
			ucibridge.DitherEntropy, ucibridge.DitherPRNG, ucibridge.DitherOff, *ditherSrc)
		os.Exit(1)
	}

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
	b := &ucibridge.Bridge{
		Bin: bin, Defs: defs, FixedBudgetMs: *budget, Banked: *bank, Adaptive: *adaptive,
		Dither: *dither, DitherSource: *ditherSrc, DitherSeed: *ditherSeed,
	}
	if *usebook {
		bk, err := book.Default()
		if err != nil {
			fmt.Fprintln(os.Stderr, "book:", err)
			os.Exit(1)
		}
		labels, err := chesstest.ParseLabelFile(*lblfile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "labels:", err)
			os.Exit(1)
		}
		be, ok := labels["bookentry"]
		if !ok {
			fmt.Fprintln(os.Stderr, "bookentry label not found in", *lblfile)
			os.Exit(1)
		}
		b.Book, b.BookEntry, b.BookSeed = bk, be, *bookSeed
	}
	if *ponder {
		b.Ponder, b.PonderSelf, b.PonderBudgetMs = true, true, *ponderMs
	}
	if *usebook || *ponder || *adaptive {
		b.Log = os.Stderr // capture book openings / ponder measurements / time-budget audit to stderr
	}
	if err := b.Run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
