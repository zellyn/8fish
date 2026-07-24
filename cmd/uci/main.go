// Command uci presents the emulated 6502 chess engine as a UCI engine
// (for cutechess-cli, lichess-bot, and friends). See internal/ucibridge.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/ucibridge"
)

func main() {
	var (
		binfile  = flag.String("bin", "asm/engine.bin", "engine binary")
		defsfile = flag.String("defs", "asm/defs.inc", "memory-layout defs")
		lblfile  = flag.String("lbl", "asm/engine.lbl", "engine label file (for the book probe entry point)")
		budget   = flag.Uint64("budget", 0, "fixed emulated ms per move (0: derive from go command)")
		dither   = flag.Bool("dither", false, "seed per-move eval dither (breaks deterministic repetition)")
		bank     = flag.Bool("bank", false, "chess-clock banking: unused budget carries to later moves")
		adaptive = flag.Bool("adaptive", false, "adaptive time management (FT2_ADAPT): per-game cycle bank + movable ceiling; supersedes -bank")
		usebook  = flag.Bool("book", false, "play the resident opening book before searching")
		bookSeed = flag.Uint64("book-seed", 0, "seed for the book's weighted-random pick (varied per game automatically)")
		ponder   = flag.Bool("ponder", false, "self-ponder during the opponent's turn and measure the ponder-hit rate (for gauntlets)")
		ponderMs = flag.Uint64("ponder-budget-ms", 0, "emulated-ms budget for each ponder search (set to the opponent's per-move think time)")
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
	b := &ucibridge.Bridge{Bin: bin, Defs: defs, FixedBudgetMs: *budget, Dither: *dither, Banked: *bank, Adaptive: *adaptive}
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
