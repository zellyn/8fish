// Command sargon-symmatch runs a FUDGE-FREE, fully SYMMETRIC match between our
// engine ("8fish", the emulated 6502 engine via internal/ucibridge) and Sargon
// III (driven in the goapple2 emulator via internal/sargon), in a SINGLE process
// so the two engines' emulated-cycle clocks can be interleaved exactly.
//
// SYMMETRY (no budget multiplier). Per full move:
//
//  1. 8fish searches its move, consuming T_us emulated cycles (its flat budget
//     B). CONCURRENTLY (emulated-time-equivalent: run right after, since the two
//     engines are separate emulators) Sargon is advanced by exactly T_us cycles.
//     In Hard Mode Sargon natively PONDERS our position during that window.
//  2. 8fish's move is injected into Sargon and confirmed via the on-screen
//     move-list column (Hard-mode, ponder-immune; RAM is search scratch).
//  3. Sargon commits its reply under the SAME budget B (Infinite level + CTRL-T),
//     costing T_sargon cycles, read from the move-list column.
//  4. Sargon is paused (we stop advancing it).
//  5. 8fish ponders for EXACTLY T_sargon cycles on Sargon's predicted reply,
//     warming its carried transposition table (the shipped go-ponder path).
//  6. Sargon's move is injected into 8fish; loop.
//
// Each side's ponder length equals the OTHER side's measured think, by
// construction — logged per move so the mirror is auditable. Base think is B for
// both (Sargon: CTRL-T at B; 8fish: flat movetime B), so the match is symmetric
// with no multiplier and no fudge.
//
// This is deliberately NOT cutechess-orchestrated: cutechess runs the two
// engines as independent OS processes and cannot interleave their emulated
// clocks cycle-accurately (which is the whole point here).
//
// ★ WHAT THIS COMMAND'S Elo DOES NOT DESCRIBE (2026-07-29). `-softclock` makes
// the FEATURES/FEATURES2 bytes byte-identical to the shipped disk, but the
// TIME MANAGEMENT around them is still not the disk's, in two ways that are
// worth stating before anyone attaches one of these numbers to the artifact:
//
//   - PONDERING. Step 5 above is roughly HALF of 8fish's total compute in a
//     match (measured: 316 of 648 Gcyc on the exact clock, 401 of 824 on the
//     soft one). `asm/m8.s` NEVER PONDERS — its main loop blocks in `uiread` ->
//     `entkey` on the opponent's turn — and docs/plan.md M8 keeps it out of
//     scope on purpose. So the disk plays with about half the compute per move
//     pair that these games give 8fish, and a change that only moves the ponder
//     path (e.g. a ponder-specific clock margin) moves nothing a user can boot.
//   - THE BANK. `newEightfish` leaves the bridge on `Banked=false,
//     Adaptive=false` and this file runs its own host-side
//     `chesstest.BankedClock`, settled on TRUE cycles. That refunds every
//     under-spend into the next move, so own-move total compute telescopes to
//     income x moves BY CONSTRUCTION and the own-move adherence line cannot
//     show a clock error however large. The disk runs on-device FT2_ADAPT with
//     NO bank at all (`uilimits`: CEILMAX 4x, UNSTCEIL 3x, MINSPEND base/4,
//     straight off the level's budget).
//
// The spend-symmetry audit below is still exact for what it measures; this is a
// note about what the measured configuration IS.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/sargon"
	"github.com/zellyn/chess6502/internal/ucibridge"
)

// cyclesPerMs matches the engine bridge's clock (chesstest.CyclesPerMs, 1020).
// Sargon's own clock is 1_020_500/s; the <0.05% difference only affects how a
// measured cycle count is rounded to a movetime budget, never the logged
// symmetry (which compares exact cycle counts).
const cyclesPerMs = chesstest.CyclesPerMs

func main() {
	var (
		dsk         = flag.String("dsk", "assets/sargon-iii.dsk", "Sargon III .dsk image")
		binfile     = flag.String("bin", "asm/engine.bin", "8fish engine binary")
		defsfile    = flag.String("defs", "asm/defs.inc", "8fish memory-layout defs")
		lblfile     = flag.String("lbl", "asm/engine.lbl", "8fish label file (book probe entry)")
		budget      = flag.Uint64("budget-cycles", 30_000_000, "symmetric per-move budget B in 6502 cycles (both sides)")
		usebook     = flag.Bool("book", false, "8fish plays its resident opening book before searching")
		bookSeed    = flag.Uint64("book-seed", 1, "8fish book weighted-pick seed")
		bookFile    = flag.String("bookfile", "", "load 8fish's resident book from this blob instead of the embedded one (for book A/B against the same Sargon)")
		dither      = flag.String("dither", ucibridge.DitherEntropy, "8fish eval-dither seed source: entropy (hardware-faithful keystroke-timing collector), prng (host PCG), off")
		ditherSeed  = flag.Uint64("dither-seed", 0, "with -dither prng: pin the stream for a reproducible run (0 = random)")
		softclock   = flag.Bool("softclock", false, "8fish runs on its own ESTIMATED elapsed-cycle clock (FT2_SOFTCLK, $BFF4 read trap off) — the SHIPPED device configuration (asm/m8.s), which an Apple IIe requires because it has no readable clock")
		noPonder    = flag.Bool("noponder", false, "remove pondering from BOTH sides: 8fish runs no ponder search (asm/m8.s never ponders — it blocks in uiread/entkey on the opponent's turn) and Sargon is NOT advanced during 8fish's think, so Hard Mode gets no ponder window either. Symmetry is preserved (each side spends only its own-move budget B); dropping only 8fish's ponder would hand Sargon a ~2:1 compute edge and the Elo would measure that, not chess")
		openings    = flag.String("openings", "", "EPD file of opening positions (one per line); games cycle through it")
		stdStart    = flag.Bool("standard-start", false, "every game starts from the STANDARD chess start position with no setboard, so BOTH engines use their opening books (8fish's resident book, Sargon III's built-in book); overrides -openings")
		games       = flag.Int("games", 2, "number of games to play")
		maxMoves    = flag.Int("max-moves", 160, "adjudicate a draw after this many full moves")
		sargonFirst = flag.Bool("sargon-white-first", false, "game 0: Sargon plays White (else 8fish White); colors alternate each game")
		outdir      = flag.String("out", "", "if set, also append the log to <out>/symmatch.log")
		confirm     = flag.Bool("confirm-deep-ponder", false, "validation: inject a move mid-deep-ponder and report acceptance, then exit")
		confirmBud  = flag.Uint64("confirm-ponder-cycles", 35_000_000, "deep-ponder window for -confirm-deep-ponder")
		diag        = flag.Bool("diag", false, "diagnostic: type a move mid-ponder then repeatedly CTRL-T, dumping the commit behavior")
		diagMove    = flag.String("diag-move", "g1f3", "move (UCI) for -diag")
	)
	flag.Parse()

	switch *dither {
	case ucibridge.DitherEntropy, ucibridge.DitherPRNG, ucibridge.DitherOff:
	default:
		log.Fatalf("-dither: want %q, %q or %q, got %q",
			ucibridge.DitherEntropy, ucibridge.DitherPRNG, ucibridge.DitherOff, *dither)
	}

	if *diag {
		runDiag(func(f string, a ...any) { fmt.Printf(f+"\n", a...) }, *dsk, *confirmBud, *diagMove)
		return
	}

	// Log to stdout and (optionally) a file.
	var logw io.Writer = os.Stdout
	if *outdir != "" {
		if err := os.MkdirAll(*outdir, 0755); err != nil {
			log.Fatalf("mkdir %s: %v", *outdir, err)
		}
		f, err := os.OpenFile(*outdir+"/symmatch.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("open log: %v", err)
		}
		defer f.Close()
		logw = io.MultiWriter(os.Stdout, f)
	}
	lg := func(format string, args ...any) {
		fmt.Fprintf(logw, format+"\n", args...)
	}

	if *confirm {
		runConfirm(lg, *dsk, *budget, *confirmBud, *diagMove)
		return
	}

	// STANDARD-START mode: no opening pool, no setboard. Every game begins at the
	// initial position, so 8fish plays its resident book (b.Book/BookEntry/BookSeed
	// -> the asm book probe) and Sargon III plays its OWN built-in book. Sargon's
	// book is left intact deliberately: nothing here sends CTRL-Y (which cancels
	// it), and the boot sequence only sets Hard Mode + Infinite level. The pool
	// mode (-openings) is untouched and remains the history-comparable measurement.
	openingList, err := loadOpenings(*openings)
	if err != nil {
		log.Fatalf("openings: %v", err)
	}
	if *stdStart {
		if len(openingList) > 0 {
			lg("NOTE: -standard-start overrides -openings %q (%d positions ignored)", *openings, len(openingList))
		}
		openingList = nil
	}

	eng, err := newEightfish(*binfile, *defsfile, *lblfile, *usebook, *bookFile, *bookSeed, *dither, *ditherSeed, *softclock, logw)
	if err != nil {
		log.Fatalf("8fish init: %v", err)
	}
	defer eng.close()

	mode := "pool"
	if len(openingList) == 0 {
		mode = "standard-start"
	}
	clockMode := "harness-exact ($BFF4 read trap live)"
	if *softclock {
		clockMode = "SOFTCLOCK (FT2_SOFTCLK, trap off) — the SHIPPED device config"
	}
	ponderMode := "symmetric ponder"
	if *noPonder {
		ponderMode = "NO PONDER on either side (device-faithful: asm/m8.s never ponders; Sargon not advanced during 8fish's think)"
	}
	lg("=== sargon-symmatch: %d game(s), budget B=%d cyc (%d ms), book=%v, mode=%s, clock=%s, %s ===",
		*games, *budget, *budget/cyclesPerMs, *usebook, mode, clockMode, ponderMode)

	// Sargon III cannot record a game longer than 127 full moves: at move 128 its
	// move-list numbering restarts at 1 and its screen record — the only
	// ponder-immune way to read its replies — becomes fiction. Both games in the
	// 300-game 2026-07 run that reached move 128 broke there. Adjudicate first.
	if *maxMoves > sargon.MaxSargonMoves {
		lg("NOTE: -max-moves %d exceeds Sargon III's %d-move move-list capacity; clamping",
			*maxMoves, sargon.MaxSargonMoves)
		*maxMoves = sargon.MaxSargonMoves
	}

	var wins, losses, draws int
	var sess timeAcct // whole-session 8fish own-move spend vs intended budget
	for g := 0; g < *games; g++ {
		sargonWhite := *sargonFirst
		if g%2 == 1 {
			sargonWhite = !sargonWhite
		}
		fen := ""
		if len(openingList) > 0 {
			fen = openingList[g%len(openingList)]
		}
		res := playGame(lg, eng, *dsk, *budget, sargonWhite, fen, *maxMoves, g, &sess, *noPonder)
		switch res {
		case resWin:
			wins++
		case resLoss:
			losses++
		default:
			draws++
		}
		lg("GAME %d RESULT: %s (running: 8fish +%d -%d =%d)", g, res, wins, losses, draws)
	}
	// Harness-level adherence (own_total = actual 8fish own-move cycles; own_intended
	// = flat budget B x moves). Distinct name from the ucibridge's own TIME-*
	// lines, whose internal "income" is this harness's varying per-move alloc.
	lg("SYMMATCH-TIME-SESSION-SUMMARY games=%d moves=%d own_total=%d own_intended=%d ratio=%.4f",
		*games, sess.moves, sess.ownCyc, sess.intended, sess.ratio())
	// THE gate. Read this before the result: a spend_ratio away from 1.0 means
	// one engine got more compute than the other and the Elo below is not a
	// measurement of chess strength.
	lg("SYMMATCH-SPEND-SESSION-SUMMARY games=%d 8fish_total=%d sargon_total=%d spend_ratio=%.4f "+
		"| 8fish_own=%d 8fish_ponder=%d ponder_asked=%d ponder_ratio=%.4f ponders=%d sargon_own=%d sargon_ponder=%d",
		*games, sess.eightfishTotal(), sess.sargonTotal(), sess.spendRatio(),
		sess.ownCyc, sess.ponderCyc, sess.ponderAsked, sess.ponderRatio(), sess.ponders,
		sess.sargonThink, sess.sargonPonder)
	lg("SYMMATCH-BOOK-SESSION-SUMMARY games=%d 8fish_book_moves=%d/%d 8fish_book_cyc=%d sargon_instant_replies=%d/%d",
		*games, sess.bookMoves, sess.moves, sess.bookCyc, sess.sargonBook, sess.sargonMoves)
	lg("SYMMATCH-DONE games=%d 8fish_wins=%d 8fish_losses=%d draws=%d", *games, wins, losses, draws)
}

// timeAcct accumulates 8fish's own-move cycle spend against its intended budget
// (income x moves). The debt-bank makes own_total telescope to own_intended, so
// ratio() reports how close per-game/session compute conserved to 1.0x.
type timeAcct struct {
	moves       int
	ownCyc      uint64 // actual emulated cycles spent on 8fish's own-move searches
	intended    uint64 // sum of per-move income (== budget B x moves)
	bookMoves   int    // 8fish own moves answered from its resident book (spend ~0)
	bookCyc     uint64 // cycles those book moves actually cost (probe + pred probe)
	sargonBook  int    // Sargon replies that came near-instantly (its own book)
	sargonMoves int    // Sargon replies counted

	// --- Per-side TOTAL compute, for the spend-symmetry audit. ---
	//
	// sargon-symmatch is symmetric BY CONSTRUCTION only as long as each side's
	// two components mirror the other's:
	//
	//	8fish  = ownCyc (its own searches)      + ponderCyc   (its ponders)
	//	sargon = ownCyc (the mirrored ponder window) + sargonThink (its own thinks)
	//
	// The first components are EQUAL by construction: runSargonCycles advances
	// Sargon by exactly the true cycles 8fish's search spent. The second pair is
	// NOT: 8fish is merely ASKED to ponder for reply.ThinkCycles, and under
	// FT2_SOFTCLK it decides for itself when that window is up. So a soft-clock
	// estimator error shows up here, as ponderCyc drifting off sargonThink —
	// free compute that would inflate Elo meaninglessly. ponderAsked records the
	// windows actually requested (ponders are skipped when there is no predicted
	// move or Sargon replied instantly), so the comparison is like-for-like.
	ponderCyc   uint64 // true cycles 8fish's ponders actually burned
	ponderAsked uint64 // window 8fish's ponders were asked to burn
	sargonThink uint64 // cycles Sargon spent on its own moves (all of them)
	ponders     int    // ponder searches actually run
	// sargonPonder is the ponder window Sargon was actually advanced through
	// (runSargonCycles). Under the default symmetric mode this equals ownCyc by
	// construction; under -noponder it stays 0, because Sargon is not advanced
	// during 8fish's think at all. Tracking it explicitly — instead of assuming
	// it equals ownCyc — is what keeps spendRatio honest in BOTH modes.
	sargonPonder uint64
}

func (t *timeAcct) add(spent, income uint64) {
	t.moves++
	t.ownCyc += spent
	t.intended += income
}

// addPonder records one ponder search: the window it was asked for and what it
// truly cost. asked == 0 means no ponder ran, and nothing is recorded.
func (t *timeAcct) addPonder(asked, spent uint64) {
	if asked == 0 {
		return
	}
	t.ponders++
	t.ponderAsked += asked
	t.ponderCyc += spent
}

// eightfishTotal / sargonTotal are the two sides' whole-match compute.
func (t timeAcct) eightfishTotal() uint64 { return t.ownCyc + t.ponderCyc }
func (t timeAcct) sargonTotal() uint64    { return t.sargonPonder + t.sargonThink }

// spendRatio is THE number to read before any Elo: 8fish's total compute over
// Sargon's. 1.0 means the mirror held. Anything past a few percent means the
// Elo is measuring a compute edge, not chess.
func (t timeAcct) spendRatio() float64 {
	if s := t.sargonTotal(); s != 0 {
		return float64(t.eightfishTotal()) / float64(s)
	}
	return 0
}

// ponderRatio isolates the only component that CAN break symmetry.
func (t timeAcct) ponderRatio() float64 {
	if t.ponderAsked == 0 {
		return 0
	}
	return float64(t.ponderCyc) / float64(t.ponderAsked)
}

func (t *timeAcct) fold(o timeAcct) {
	t.moves += o.moves
	t.ownCyc += o.ownCyc
	t.intended += o.intended
	t.bookMoves += o.bookMoves
	t.bookCyc += o.bookCyc
	t.sargonBook += o.sargonBook
	t.sargonMoves += o.sargonMoves
	t.ponderCyc += o.ponderCyc
	t.ponderAsked += o.ponderAsked
	t.sargonThink += o.sargonThink
	t.sargonPonder += o.sargonPonder
	t.ponders += o.ponders
}

func (t timeAcct) ratio() float64 {
	if t.intended == 0 {
		return 0
	}
	return float64(t.ownCyc) / float64(t.intended)
}

// ---------------------------------------------------------------------------
// Game result
// ---------------------------------------------------------------------------

type gameResult string

const (
	resWin  gameResult = "8fish-wins"
	resLoss gameResult = "8fish-loses"
	resDraw gameResult = "draw"
)

// ---------------------------------------------------------------------------
// 8fish: the ucibridge.Bridge driven in-process over pipes (real UCI protocol,
// including the go-ponder/ponder-warm handshake).
// ---------------------------------------------------------------------------

type eightfish struct {
	in  *bufio.Writer // -> bridge stdin
	out *bufio.Scanner
	cw  io.WriteCloser
}

func newEightfish(binfile, defsfile, lblfile string, usebook bool, bookFile string, bookSeed uint64, dither string, ditherSeed uint64, softclock bool, logw io.Writer) (*eightfish, error) {
	bin, err := os.ReadFile(binfile)
	if err != nil {
		return nil, err
	}
	defs, err := chesstest.ParseDefs(defsfile)
	if err != nil {
		return nil, err
	}
	// Ponder ON, PonderSelf OFF: we drive the real UCI go-ponder handshake so
	// each ponder uses a per-move movetime budget (== Sargon's measured think).
	// FixedBudgetMs=0 and Banked/Adaptive=false so each own-move `go movetime B`
	// is a FLAT budget-B search (the symmetric base). b.aux (the TT) carries
	// across moves and is warmed by the ponder exactly as in a real gauntlet.
	// Dither ON: per-move eval-noise so 8fish plays DIFFERENT games from a
	// repeated opening (the 40-position pool cycles ~7-8x over 300 games; both
	// engines are otherwise deterministic, so without dither "300 games" is
	// really ~40-80 distinct games replayed — a false-tight error bar). Dither
	// makes each game an independent sample; symmetry is unaffected (Sargon
	// still ponders 8fish's ACTUAL measured cycles, whatever dither makes them).
	//
	// The seed comes from the HARDWARE-FAITHFUL source by default (-dither
	// entropy): internal/entropy's byte-for-byte model of the on-device
	// keystroke-timing collector (asm/entropy.inc), fed by the real arrival
	// timing of Sargon's moves and by our own searches' emulated cycle counts.
	// The match therefore exercises the entropy plan that will ship instead of
	// a host PRNG the IIe could never have. -dither prng -dither-seed N gets
	// the old, reproducible stream back.
	//
	// SoftClock (-softclock) selects the SHIPPED device configuration: the
	// engine runs on its own estimated elapsed-cycle clock (FT2_SOFTCLK)
	// because an Apple IIe has no readable clock. One bool drives BOTH the
	// feature bit and the $BFF4 read-trap disable (ucibridge.runEngine derives
	// them together, the same single-source-of-truth rule internal/sprt uses
	// in the other direction), so the two can never be set inconsistently.
	//
	// It does NOT weaken the mirror: the `info nodes` count the bridge
	// publishes — and therefore the window this harness hands Sargon — is
	// always m.Cycles, the emulator's TRUE cycle count, never the engine's
	// estimate. What the soft clock CAN do is make 8fish's own searches and
	// (crucially) its ponders overrun their windows in true cycles; the
	// own-move overrun is mirrored to Sargon and stays symmetric, but a ponder
	// overrun is NOT mirrored and is free compute. Audit SYMMATCH-SPEND-*.
	b := &ucibridge.Bridge{
		Bin: bin, Defs: defs, Ponder: true, Log: logw,
		Dither:       dither != ucibridge.DitherOff,
		DitherSource: dither,
		DitherSeed:   ditherSeed,
		SoftClock:    softclock,
	}
	if usebook {
		bk, err := loadBookBlob(bookFile)
		if err != nil {
			return nil, fmt.Errorf("book: %w", err)
		}
		labels, err := chesstest.ParseLabelFile(lblfile)
		if err != nil {
			return nil, fmt.Errorf("labels: %w", err)
		}
		be, ok := labels["bookentry"]
		if !ok {
			return nil, fmt.Errorf("bookentry label not in %s", lblfile)
		}
		b.Book, b.BookEntry, b.BookSeed = bk, be, bookSeed
	}
	cr, cw := io.Pipe() // harness -> bridge
	rr, rw := io.Pipe() // bridge -> harness
	go func() {
		_ = b.Run(cr, rw)
		rw.Close()
	}()
	e := &eightfish{in: bufio.NewWriter(cw), out: bufio.NewScanner(rr), cw: cw}
	e.out.Buffer(make([]byte, 0, 64*1024), 1<<20)
	e.send("uci")
	e.expect("uciok")
	e.send("isready")
	e.expect("readyok")
	return e, nil
}

func (e *eightfish) close() { e.cw.Close() }

func (e *eightfish) send(format string, args ...any) {
	fmt.Fprintf(e.in, format+"\n", args...)
	e.in.Flush()
}

// expect reads lines until one has the given prefix, returning it.
func (e *eightfish) expect(prefix string) string {
	for e.out.Scan() {
		line := e.out.Text()
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// newGame resets per-game state (clears TT) in the bridge.
func (e *eightfish) newGame() { e.send("ucinewgame") }

// bookInfoPrefix is the ucibridge's book-hit marker ("info string in book: <ECO Name>").
const bookInfoPrefix = "in book: "

// searchResult is one 8fish own-move outcome.
type searchResult struct {
	move    string // UCI best move ("0000" if none)
	ponder  string // predicted opponent reply, or "" if none advertised
	cycles  uint64 // T_us: emulated cycles the search actually spent (info nodes)
	book    bool   // the move came from 8fish's resident opening book (no search)
	opening string // "ECO Name" of the book line, when book
}

// search runs a flat budget-B search on the position given by (base, moves) and
// returns the move, the predicted reply, and the cycles spent.
func (e *eightfish) search(base string, moves []string, budgetCyc uint64) searchResult {
	e.send("position %s", posSpec(base, moves))
	e.send("go movetime %d", movetimeMs(budgetCyc))
	var res searchResult
	for e.out.Scan() {
		line := e.out.Text()
		// Book hit: the bridge emits "info string in book: <ECO Name>" and then a
		// separate "info nodes N" carrying the turn's real (near-zero) cost.
		if i := strings.Index(line, bookInfoPrefix); i >= 0 {
			res.book = true
			res.opening = strings.TrimSpace(line[i+len(bookInfoPrefix):])
		}
		if n, ok := parseNodes(line); ok {
			res.cycles = n
		}
		if strings.HasPrefix(line, "bestmove") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				res.move = f[1]
			}
			if len(f) >= 4 && f[2] == "ponder" {
				res.ponder = f[3]
			}
			return res
		}
	}
	return res
}

// ponder warms 8fish's carried TT by searching the position it WOULD face if the
// opponent plays `predicted` (base + moves + our move + predicted), under a
// budget equal to Sargon's measured think. Returns the cycles it actually spent.
func (e *eightfish) ponder(base string, moves []string, predicted string, budgetCyc uint64) uint64 {
	e.send("position %s", posSpec(base, append(append([]string(nil), moves...), predicted)))
	e.send("go ponder movetime %d", movetimeMs(budgetCyc))
	// The go-ponder path emits exactly one "info string pondering ..." line and
	// then holds its bestmove (no further output) until the next command.
	for e.out.Scan() {
		line := e.out.Text()
		if strings.HasPrefix(line, "info string pondering") {
			n, _ := parseNodes(line)
			return n
		}
		if strings.HasPrefix(line, "info string ponder error") {
			return 0
		}
	}
	return 0
}

// movetimeMs converts a cycle budget to the UCI `movetime` (ms) the bridge
// parses, FLOORED AT 1. A sub-millisecond window would round to `movetime 0`,
// which the bridge reads as "no time budget" = fixed-DEPTH mode with a 300-
// billion-cycle run cap — i.e. an apparent hang. That can happen legitimately
// in standard-start mode, where an opening-book move costs only tens of
// thousands of cycles and the opponent's mirrored window is correspondingly
// tiny. 1 ms (1020 cycles) is the smallest honest non-degenerate budget.
func movetimeMs(cyc uint64) uint64 {
	if ms := cyc / cyclesPerMs; ms > 0 {
		return ms
	}
	return 1
}

func posSpec(base string, moves []string) string {
	if len(moves) == 0 {
		return base
	}
	return base + " moves " + strings.Join(moves, " ")
}

// parseNodes returns the value after a "nodes" token in a UCI info line.
func parseNodes(line string) (uint64, bool) {
	f := strings.Fields(line)
	for i, t := range f {
		if t == "nodes" && i+1 < len(f) {
			if v, err := strconv.ParseUint(f[i+1], 10, 64); err == nil {
				return v, true
			}
		}
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// The game loop
// ---------------------------------------------------------------------------

func playGame(lg func(string, ...any), eng *eightfish, dsk string, budget uint64, sargonWhite bool, fen string, maxMoves, gameNo int, sess *timeAcct, noPonder bool) gameResult {
	// Per-game debt-bank governing 8fish's own-move budget. Income per move is B;
	// each move spends B + bank/8, and an iteration-boundary overshoot drives the
	// bank NEGATIVE so later moves' shrunken allocations repay it — the same
	// conserving BankedClock the cutechess/sprt path uses. This does NOT touch the
	// symmetry: Sargon still ponders 8fish's ACTUAL measured think (sr.cycles),
	// whatever the bank let it spend.
	clock := &chesstest.BankedClock{Base: budget}
	var gt timeAcct // this game's 8fish own-move spend, folded into sess + logged on return
	defer func() {
		lg("SYMMATCH-TIME-GAME-SUMMARY game=%d moves=%d own_total=%d own_intended=%d ratio=%.4f",
			gameNo, gt.moves, gt.ownCyc, gt.intended, gt.ratio())
		lg("SYMMATCH-BOOK-GAME-SUMMARY game=%d 8fish_book_moves=%d/%d 8fish_book_cyc=%d sargon_instant_replies=%d/%d",
			gameNo, gt.bookMoves, gt.moves, gt.bookCyc, gt.sargonBook, gt.sargonMoves)
		lg("SYMMATCH-SPEND-GAME-SUMMARY game=%d 8fish_total=%d sargon_total=%d spend_ratio=%.4f ponder_ratio=%.4f",
			gameNo, gt.eightfishTotal(), gt.sargonTotal(), gt.spendRatio(), gt.ponderRatio())
		sess.fold(gt)
	}()

	// Referee position (authoritative game state + terminal detection).
	startFEN := refchess.StartFEN
	base := "startpos"
	if fen != "" {
		startFEN = normalizeFEN(fen)
		base = "fen " + startFEN
	}
	// abandon ends a game that never became a game (boot, mode or setup failure).
	// It logs a TERMINATION line with the same grep key every other exit uses:
	// a game the harness could not even start is a HARNESS ARTIFACT scored as a
	// draw, and an audit that cannot see it reports it as absent rather than as
	// unknown (the 2026-07-31 `plies=-1` lesson, one level further up — these
	// paths had no TERMINATION line at all, so they were invisible even to a
	// fixed analyser).
	abandon := func(reason, format string, args ...any) gameResult {
		lg(format, args...)
		lg("TERMINATION g%d result=%s reason=quirk-%s plies=-1", gameNo, resDraw, reason)
		return resDraw
	}

	ref, err := refchess.ParseFEN(startFEN)
	if err != nil {
		return abandon("bad-opening-fen", "GAME %d: bad opening FEN %q: %v", gameNo, fen, err)
	}
	seen := map[uint64]int{ref.ZobristKey(): 1}

	// Boot Sargon fresh for this game, Hard Mode (NO Easy Mode), Infinite level.
	m, err := sargon.NewMachine(dsk)
	if err != nil {
		return abandon("sargon-new", "GAME %d: sargon new: %v", gameNo, err)
	}
	if err := m.BootToPrompt(); err != nil {
		return abandon("sargon-boot", "GAME %d: sargon boot: %v", gameNo, err)
	}
	m.HardMode = true
	if err := m.InfiniteLevel(); err != nil {
		return abandon("sargon-level", "GAME %d: sargon infinite level: %v", gameNo, err)
	}
	m.Run(1_000_000)
	// Assert, don't just log: the whole measurement assumes Sargon is on the
	// INFINITE level (so CTRL-T at our budget is what bounds its search) in HARD
	// mode (so it ponders like a real opponent). Easy mode roughly halves its
	// strength and a lower level would make it move on its own timer — either
	// would INFLATE our score silently. Row 3 shows "LEVEL <n>" with a trailing
	// "E" in Easy mode.
	row3 := strings.ToUpper(strings.TrimSpace(m.TextRow(3)))
	hard := verifyHardMode(m)
	if !hard || !strings.Contains(row3, "LEVEL 9") {
		lg("%s", m.ScreenDump(fmt.Sprintf("g%d bad-mode", gameNo)))
		return abandon("sargon-mode",
			"WARNING g%d: SARGON MODE WRONG — want Hard + LEVEL 9, row3=%q hard=%v; ABANDONING GAME",
			gameNo, row3, hard)
	}
	if fen != "" {
		if err := m.SetupPosition(fen); err != nil {
			return abandon("sargon-setup", "GAME %d: sargon setup %q: %v", gameNo, fen, err)
		}
	}
	lg("GAME %d START: sargonWhite=%v hardMode=%v(row3=%q) opening=%q budget=%d",
		gameNo, sargonWhite, hard, strings.TrimSpace(m.TextRow(3)), fen, budget)

	eng.newGame()
	var moves []string              // UCI move list from the opening position
	var hist []sargon.HistMove      // same plies, in move-list cross-check form
	nextDump := 2 * screenDumpEvery // ply at which the next periodic capture is due
	// Move-list ply number our hist[0] occupies. CTRL-A setup clears Sargon's list
	// and renumbers from 1 (verified: internal/sargon TestSetupResetsMoveList), so
	// POOL games are cross-checkable too — but a Black-to-move setboard position
	// puts our first recorded move in the BLACK column of row 1, i.e. list ply 2.
	firstPly := 1
	if ref.SideToMove() != 0 {
		firstPly = 2
	}

	// If Sargon is White it moves first (CTRL-S). SetupPosition-based games in the
	// pool are all white-to-move, so a Sargon-White game means Sargon opens.
	if sargonWhite {
		m.SargonWhite = true
		res, err := m.StartAsWhite(budget)
		if err != nil {
			lg("GAME %d: sargon-as-white open: %v", gameNo, err)
			return adjudicate(lg, gameNo, ref, m, false)
		}
		s := screenTokenToCoord(res.SargonText, true, ref)
		if s == "" || !applyMove(ref, seen, &hist, s) {
			lg("GAME %d: sargon opening move unreadable/illegal token=%q", gameNo, res.SargonText)
			lg("%s", m.ScreenDump(fmt.Sprintf("g%d opening unreadable", gameNo)))
			lg("TERMINATION g%d result=%s reason=quirk-opening-unreadable plies=0", gameNo, resDraw)
			return resDraw
		}
		moves = append(moves, s)
		gt.sargonMoves++
		gt.sargonThink += res.ThinkCycles // Sargon's opening move: no 8fish ponder mirrors it
		if sargonInstant(res.ThinkCycles, budget) {
			gt.sargonBook++ // instant first move => straight out of Sargon's book
		}
		lg("MOVE g%d ply1 SARGON(open) move=%s think=%d sargon_instant=%v (no 8fish ponder: opening)",
			gameNo, s, res.ThinkCycles, sargonInstant(res.ThinkCycles, budget))
		if !crossCheck(lg, gameNo, 1, m, hist, firstPly) {
			lg("TERMINATION g%d result=%s reason=quirk-desync plies=1", gameNo, resDraw)
			return resDraw
		}
		if term, r, why := terminal(ref, seen, moves, maxMoves, sargonWhite); term {
			lg("TERMINATION g%d result=%s reason=%s plies=%d", gameNo, r, why, len(moves))
			return r
		}
	}

	// Main symmetric loop: 8fish to move at the top.
	for {
		// --- Step 1: 8fish searches (T_us); Sargon ponders that same window. ---
		// The bank sets THIS move's spendable budget (income B + bank/8); the
		// actual measured think (sr.cycles) is what Sargon then mirrors.
		alloc := clock.Alloc()
		sr := eng.search(base, moves, alloc)
		if sr.move == "" || sr.move == "0000" {
			lg("MOVE g%d 8fish has no move (%q) — adjudicating", gameNo, sr.move)
			return adjudicate(lg, gameNo, ref, m, true)
		}
		clock.Settle(sr.cycles)
		gt.add(sr.cycles, budget)
		if sr.book {
			gt.bookMoves++
			gt.bookCyc += sr.cycles
		}
		// Per-own-move time-bank audit. A book move spends ~0, so Settle credits
		// almost the whole income to the bank and the NEXT alloc is visibly larger
		// than the flat income B — the first real search out of book inherits the
		// time the book moves did not use (capped at 8*B of bank => alloc <= 2*B).
		lg("BANK g%d ply%d move=%s book=%v opening=%q income=%d alloc=%d spent=%d spent_over_income=%.4f bank_after=%d next_alloc=%d",
			gameNo, len(moves)+1, sr.move, sr.book, sr.opening, budget, alloc, sr.cycles,
			float64(sr.cycles)/float64(budget), clock.Bank(), clock.Alloc())
		if !applyMove(ref, seen, &hist, sr.move) {
			lg("MOVE g%d 8fish played illegal-to-referee move %q — aborting", gameNo, sr.move)
			lg("TERMINATION g%d result=%s reason=quirk-8fish-illegal plies=%d", gameNo, resDraw, len(moves))
			return resDraw
		}
		moves = append(moves, sr.move)
		usPly := len(moves)

		// Sargon ponder window == 8fish's exact think (T_us). Under -noponder the
		// window is zero on BOTH sides, so Sargon is simply not advanced here.
		var sargonWindow uint64
		if !noPonder {
			sargonWindow = sr.cycles
			runSargonCycles(m, sargonWindow)
			gt.sargonPonder += sargonWindow
		}

		if term, r, why := terminal(ref, seen, moves, maxMoves, sargonWhite); term {
			lg("MOVE g%d ply%d 8fish move=%s think=%d -> %s", gameNo, usPly, sr.move, sr.cycles, r)
			lg("TERMINATION g%d result=%s reason=%s plies=%d", gameNo, r, why, len(moves))
			return r
		}

		// --- Steps 2+3: inject 8fish's move; Sargon commits its reply (T_sargon). ---
		sargonTxt, err := coordToSargon(sr.move)
		if err != nil {
			lg("MOVE g%d bad move->sargon %q: %v", gameNo, sr.move, err)
			lg("TERMINATION g%d result=%s reason=quirk-move-encoding plies=%d", gameNo, resDraw, len(moves))
			return resDraw
		}
		reply, err := m.RequestMove(sargonTxt, budget)
		if err != nil {
			// No reply: Sargon may report game over (mate/stalemate/draw) on screen.
			lg("MOVE g%d sargon no reply to %s: %v (msg=%q)", gameNo, sr.move, err, reply.Message)
			lg("%s", m.ScreenDump(fmt.Sprintf("g%d no-reply ply%d", gameNo, usPly)))
			return adjudicate(lg, gameNo, ref, m, false)
		}
		// Our move must have landed in Sargon's own record exactly as we played it.
		if !crossCheck(lg, gameNo, usPly, m, hist, firstPly) {
			return adjudicate(lg, gameNo, ref, m, false)
		}
		if reply.GameOver && reply.SargonText == "" {
			lg("MOVE g%d 8fish move %s ended game (sargon msg=%q)", gameNo, sr.move, reply.Message)
			return adjudicate(lg, gameNo, ref, m, false)
		}
		s := screenTokenToCoord(reply.SargonText, sargonWhite, ref)
		if s == "" || !applyMove(ref, seen, &hist, s) {
			lg("MOVE g%d sargon reply unreadable/illegal token=%q msg=%q — adjudicating",
				gameNo, reply.SargonText, reply.Message)
			lg("DESYNC g%d our history: %s", gameNo, sargon.HistoryString(hist))
			lg("%s", m.ScreenDump(fmt.Sprintf("g%d unreadable-reply ply%d", gameNo, usPly+1)))
			return adjudicate(lg, gameNo, ref, m, false)
		}
		if !crossCheck(lg, gameNo, usPly+1, m, hist, firstPly) {
			return adjudicate(lg, gameNo, ref, m, false)
		}

		// --- Step 5: 8fish ponders EXACTLY T_sargon on the predicted reply. ---
		var ponderCyc uint64
		hit := "n/a"
		if !noPonder && sr.ponder != "" && reply.ThinkCycles > 0 {
			ponderCyc = eng.ponder(base, moves, sr.ponder, reply.ThinkCycles)
			gt.addPonder(reply.ThinkCycles, ponderCyc)
			if sr.ponder == s {
				hit = "HIT"
			} else {
				hit = "MISS"
			}
		}
		// Sargon's own think counts toward ITS side of the spend ledger whether
		// or not we pondered over it (an instant book reply costs it ~nothing and
		// buys us no ponder either, so both sides stay honest).
		gt.sargonThink += reply.ThinkCycles

		// --- Step 6: inject Sargon's move into 8fish (append to the line). ---
		moves = append(moves, s)
		sargonPly := len(moves)

		gt.sargonMoves++
		instant := sargonInstant(reply.ThinkCycles, budget)
		if instant {
			gt.sargonBook++
		}

		// Auditable symmetry line: 8fish think == Sargon ponder-window (exact),
		// and Sargon think ~= 8fish ponder (budget-matched).
		lg("MOVE g%d ply%d 8fish move=%s think=%d | sargon_ponder_window=%d || ply%d SARGON move=%s think=%d | 8fish_ponder=%d pred=%s(%s) 8fish_book=%v sargon_instant=%v",
			gameNo, usPly, sr.move, sr.cycles, sargonWindow,
			sargonPly, s, reply.ThinkCycles, ponderCyc, sr.ponder, hit, sr.book, instant)

		// Periodic full-screen capture: cheap (~1 KB of emulator RAM) and the only
		// record of what SARGON thought the game was. Framed for grepping. Keyed on
		// a ply counter, not len(moves)%2: when Sargon is White the move list is an
		// ODD number of plies long after its reply, so a parity test would never
		// fire for half the games.
		if len(moves) >= nextDump {
			lg("%s", m.ScreenDump(fmt.Sprintf("g%d periodic ply%d", gameNo, len(moves))))
			nextDump = len(moves) + 2*screenDumpEvery
		}

		if term, r, why := terminal(ref, seen, moves, maxMoves, sargonWhite); term {
			lg("MOVE g%d after sargon %s -> %s (msg=%q)", gameNo, s, r, reply.Message)
			lg("TERMINATION g%d result=%s reason=%s plies=%d", gameNo, r, why, len(moves))
			return r
		}
	}
}

// screenDumpEvery is how often (in full moves) the whole Sargon text screen is
// captured into the match log.
const screenDumpEvery = 10

// sargonInstant reports whether a Sargon reply came WITHOUT spending its budget:
// its own opening book (or a single forced legal reply) commits the move as soon
// as it appears in the move-list column, which sargon.RequestMove detects inside
// the budget loop and returns immediately. An out-of-book Infinite-level reply
// always runs the loop to exhaustion and then needs the forced-commit strobes, so
// it costs budget + latency (~35.5M at B=30M). "think < budget" therefore
// separates the two exactly, at any budget, with no magic fraction.
func sargonInstant(think, budget uint64) bool { return think < budget }

// runSargonCycles advances the Sargon emulator by exactly `cycles` 6502 cycles,
// during which (Hard Mode) Sargon ponders the opponent's position. Small chunks
// keep the overshoot below one chunk.
func runSargonCycles(m *sargon.Machine, cycles uint64) {
	if cycles == 0 {
		return
	}
	const chunk = 200_000
	start := m.Cycles()
	for {
		done := m.Cycles() - start
		if done >= cycles {
			return
		}
		// Trim the final chunk to what is left, so a SHORT window (8fish answered
		// from its opening book: think ~ 40K cycles) does not hand Sargon a whole
		// 200K-cycle chunk of unearned ponder time. Large windows are unchanged.
		step := cycles - done
		if step > chunk {
			step = chunk
		}
		if err := m.Run(step); err != nil {
			return
		}
	}
}

// applyMove applies a UCI move to the referee, bumps the repetition count, and
// appends the ply to hist — our record of the game, which is cross-checked
// against SARGON's own on-screen move list after every ply (see crossCheck).
// Returns false if the move is illegal in the referee position.
func applyMove(ref *refchess.Position, seen map[uint64]int, hist *[]sargon.HistMove, uci string) bool {
	mv, err := refchess.ParseMove(uci)
	if err != nil {
		return false
	}
	h := histMove(ref, mv)
	if err := ref.Make(mv); err != nil {
		return false
	}
	seen[ref.ZobristKey()]++
	*hist = append(*hist, h)
	return true
}

// histMove records mv (legal in p, BEFORE it is made) in the minimal form the
// move-list cross-check needs: from/to squares, plus the two forms Sargon prints
// WITHOUT squares — castling ("0-0"/"0-0-0") and en passant ("PXPEP").
func histMove(p *refchess.Position, mv refchess.Move) sargon.HistMove {
	uci := mv.String()
	h := sargon.HistMove{From: uci[0:2], To: uci[2:4]}
	switch strings.TrimRight(p.SAN(mv), "+#") {
	case "O-O":
		h.Castle = "O-O"
	case "O-O-O":
		h.Castle = "O-O-O"
	}
	for _, ep := range p.EnPassantCaptures() {
		if ep.From == mv.From && ep.To == mv.To {
			h.EnPassant = true
		}
	}
	return h
}

// crossCheck compares SARGON's own displayed move list with our history. They
// must agree move for move; if they ever diverge, every later read of Sargon's
// screen is fiction and the game is unusable. Logs LOUDLY (both histories, the
// exact first differing ply, and a full screen dump) and reports whether the
// game may continue.
//
// This is the alarm that would have caught the 2026-07 stale-token desync at the
// ply it happened rather than as a mystery "illegal token" moves later.
func crossCheck(lg func(string, ...any), gameNo, ply int, m *sargon.Machine, hist []sargon.HistMove, firstPly int) bool {
	ok, bad, detail := m.CrossCheckHistory(hist, firstPly)
	if ok {
		return true
	}
	lg("DESYNC g%d ply%d *** SARGON MOVE LIST DISAGREES WITH OURS *** first bad ply %d: %s",
		gameNo, ply, bad, detail)
	lg("DESYNC g%d our history: %s", gameNo, sargon.HistoryString(hist))
	lg("%s", m.ScreenDump(fmt.Sprintf("desync g%d ply%d", gameNo, ply)))
	return false
}

// terminal reports whether the game is over from the referee's view (the
// authoritative referee, cross-checked against Sargon's screen at adjudication).
// It also returns the REASON, so the match log records how every game actually
// ended and the audit does not have to re-derive draw classifications by
// replaying move lists. Reasons are the grep keys in the audit table:
// checkmate / stalemate / threefold / fifty-move / insufficient-material /
// move-cap (plus the "quirk-*" reasons adjudicate() logs).
func terminal(ref *refchess.Position, seen map[uint64]int, moves []string, maxMoves int, sargonWhite bool) (bool, gameResult, string) {
	// Side to move has no legal move: checkmate (loss for stm) or stalemate.
	if len(ref.LegalMoves()) == 0 {
		if ref.InCheck() {
			// The side to move is checkmated. 8fish is White iff !sargonWhite.
			stmWhite := ref.SideToMove() == 0
			eightfishMated := stmWhite == (!sargonWhite)
			if eightfishMated {
				return true, resLoss, "checkmate"
			}
			return true, resWin, "checkmate"
		}
		return true, resDraw, "stalemate"
	}
	if seen[ref.ZobristKey()] >= 3 {
		return true, resDraw, "threefold"
	}
	if ref.HalfmoveClock() >= 100 {
		return true, resDraw, "fifty-move"
	}
	if ref.InsufficientMaterial() {
		return true, resDraw, "insufficient-material"
	}
	if len(moves)/2 >= maxMoves {
		return true, resDraw, "move-cap"
	}
	return false, resDraw, ""
}

// adjudicate resolves a game that ended without a clean referee-terminal (e.g.
// Sargon declined to move and showed a terminal message). It trusts the
// referee's terminal verdict first, then Sargon's on-screen message, and dumps
// the screen for audit. eightfishStuck marks the case where 8fish had no move.
func adjudicate(lg func(string, ...any), gameNo int, ref *refchess.Position, m *sargon.Machine, eightfishStuck bool) gameResult {
	// Every exit logs a TERMINATION line with the same grep key the clean
	// referee-terminal path uses, so one pass over the log classifies all games.
	// The "quirk-" prefixed reasons are the HARNESS-ARTIFACT class (the
	// 2026-07-26 audit's 5.7%): the game did NOT end by a rule of chess, it
	// ended because Sargon's screen could not be read. They are counted
	// separately and never silently folded in with real draws.
	// Referee terminal?
	if len(ref.LegalMoves()) == 0 {
		if ref.InCheck() {
			// The side to move is mated. If it's 8fish's move it lost, else won.
			// eightfishStuck implies the side to move IS 8fish.
			if eightfishStuck {
				lg("GAME %d ADJUDICATE: 8fish checkmated (referee)", gameNo)
				lg("TERMINATION g%d result=%s reason=checkmate-referee plies=-1", gameNo, resLoss)
				return resLoss
			}
			// Sargon to move and mated -> 8fish wins.
			lg("GAME %d ADJUDICATE: checkmate on the board (referee)", gameNo)
			lg("TERMINATION g%d result=%s reason=checkmate-referee plies=-1", gameNo, resWin)
			// Determine which side is mated by side-to-move.
			return resWin
		}
		lg("GAME %d ADJUDICATE: stalemate (referee) -> draw", gameNo)
		lg("TERMINATION g%d result=%s reason=stalemate-referee plies=-1", gameNo, resDraw)
		return resDraw
	}
	msg := strings.ToUpper(m.Screen())
	dump := func() { lg("GAME %d SCREEN-DUMP:\n%s", gameNo, m.Screen()) }
	switch {
	case strings.Contains(msg, "STALEMATE"), strings.Contains(msg, "DRAW"):
		dump()
		lg("GAME %d ADJUDICATE: Sargon declares draw/stalemate (reclassify) -> draw", gameNo)
		lg("TERMINATION g%d result=%s reason=sargon-declares-draw plies=-1", gameNo, resDraw)
		return resDraw
	case strings.Contains(msg, "MATE"): // CHECKMATE / MATE
		dump()
		// Whoever is to move on the board is mated.
		stmWhite := ref.SideToMove() == 0
		_ = stmWhite
		lg("GAME %d ADJUDICATE: Sargon shows mate", gameNo)
		if eightfishStuck {
			lg("TERMINATION g%d result=%s reason=sargon-shows-mate plies=-1", gameNo, resLoss)
			return resLoss
		}
		lg("TERMINATION g%d result=%s reason=sargon-shows-mate plies=-1", gameNo, resWin)
		return resWin
	default:
		dump()
		lg("GAME %d ADJUDICATE: unresolved (msg unclear) -> draw", gameNo)
		lg("TERMINATION g%d result=%s reason=quirk-unresolved plies=-1", gameNo, resDraw)
		return resDraw
	}
}

// ---------------------------------------------------------------------------
// Deep-ponder-injection confirmation (validation)
// ---------------------------------------------------------------------------

func runConfirm(lg func(string, ...any), dsk string, budget, ponderCyc uint64, moveUCI string) {
	lg("=== confirm-deep-ponder: inject %q into a %d-cycle deep ponder ===", moveUCI, ponderCyc)
	t0 := time.Now()
	m, err := sargon.NewMachine(dsk)
	if err != nil {
		lg("CONFIRM FAIL: new machine: %v", err)
		return
	}
	if err := m.BootToPrompt(); err != nil {
		lg("CONFIRM FAIL: boot: %v", err)
		return
	}
	m.HardMode = true
	if err := m.InfiniteLevel(); err != nil {
		lg("CONFIRM FAIL: infinite level: %v", err)
		return
	}
	m.Run(1_000_000)
	hard := verifyHardMode(m)
	lg("CONFIRM: booted; row3=%q hardMode=%v (boot %.1fs)", strings.TrimSpace(m.TextRow(3)), hard, time.Since(t0).Seconds())

	// Drive Sargon deep into a native ponder BEFORE injecting our move: it is
	// Black-to-reply and pondering White's move for the whole window.
	c0 := m.Cycles()
	runSargonCycles(m, ponderCyc)
	lg("CONFIRM: ran %d ponder cycles (Sargon mid-deep-search)", m.Cycles()-c0)

	// Inject a legal opening move mid-deep-ponder; RequestMove's Hard-mode
	// enterMove confirms acceptance from the SCREEN column, then the Hard-mode
	// forced-commit (repeated CTRL-T) drives Sargon's reply.
	sargonTxt, _ := coordToSargon(moveUCI)
	inj := m.Cycles()
	reply, err := m.RequestMove(sargonTxt, budget)
	if err != nil {
		lg("CONFIRM RESULT: injection FAILED: %v (screen row5=%q)", err, strings.TrimSpace(m.TextRow(5)))
		lg("SCREEN:\n%s", m.Screen())
		return
	}
	s := screenTokenToCoord(reply.SargonText, false, nil)
	lg("CONFIRM RESULT: injection ACCEPTED mid-deep-ponder. Sargon reply token=%q coord=%q think=%d cyc (commit %d cyc, %.1fs total)",
		reply.SargonText, s, reply.ThinkCycles, m.Cycles()-inj, time.Since(t0).Seconds())
	lg("CONFIRM-DEEP-PONDER-DONE accepted=true reply=%s", s)
}

// runDiag reproduces the Hard-mode mid-ponder move entry and then observes what
// happens as we repeatedly force a commit, to characterize the CTRL-T behavior.
func runDiag(lg func(string, ...any), dsk string, ponderCyc uint64, moveUCI string) {
	m, err := sargon.NewMachine(dsk)
	if err != nil {
		lg("DIAG new: %v", err)
		return
	}
	if err := m.BootToPrompt(); err != nil {
		lg("DIAG boot: %v", err)
		return
	}
	m.HardMode = true
	m.InfiniteLevel()
	m.Run(1_000_000)
	lg("DIAG booted row3=%q hard=%v", strings.TrimSpace(m.TextRow(3)), verifyHardMode(m))

	// Ponder window (Sargon Black, we play White).
	runSargonCycles(m, ponderCyc)
	txt, _ := coordToSargon(moveUCI)
	lg("DIAG ponder window done (%d cyc); typing %q mid-ponder", ponderCyc, txt)

	// Type the move directly (paced), like enterMove does.
	prevOurCol := colText(m, false) // our (left) column before typing
	if err := m.TypePaced(txt+"\r", 200_000); err != nil {
		lg("DIAG type err: %v", err)
		return
	}
	// Poll for our-column acceptance.
	accepted := false
	for i := 0; i < 20; i++ {
		m.Run(500_000)
		if colText(m, false) != prevOurCol {
			accepted = true
			break
		}
		if r5 := strings.ToUpper(m.TextRow(5)); strings.Contains(r5, "ILLEGAL") || strings.Contains(r5, "INVALID") {
			break
		}
	}
	lg("DIAG move accepted(screen)=%v; our-col=%q sargon-col=%q msg=%q",
		accepted, strings.TrimSpace(colText(m, false)), strings.TrimSpace(colText(m, true)), strings.TrimSpace(m.TextRow(5)))

	// Simulate a budget window (Sargon searches its reply), THEN spam CTRL-T
	// every 500K cycles and measure how many cycles until it commits.
	m.Run(10_000_000) // budget window B
	sargonColBefore := colText(m, true)
	forceStart := m.Cycles()
	strobes := 0
	for m.Cycles()-forceStart < 40_000_000 {
		m.ForceMove() // CTRL-T
		strobes++
		m.Run(500_000)
		if colText(m, true) != sargonColBefore {
			lg("DIAG COMMIT after %d CTRL-T strobes, %d cyc latency; move row: %q",
				strobes, m.Cycles()-forceStart, strings.TrimSpace(m.TextRow(10)))
			return
		}
	}
	lg("DIAG NO COMMIT after %d strobes / 40M cyc. Screen:\n%s", strobes, m.Screen())
}

// colText returns a move-list column's concatenated text. rightCol picks
// Sargon's column when Sargon is Black (right); left is the White/player column.
func colText(m *sargon.Machine, rightCol bool) string {
	lo, hi := 10, 18
	if rightCol {
		lo, hi = 22, 30
	}
	var sb strings.Builder
	for r := 10; r < 24; r++ {
		row := m.TextRow(r)
		if len(row) >= hi {
			sb.WriteString(row[lo:hi])
		}
		sb.WriteByte('|')
	}
	return sb.String()
}

// verifyHardMode returns true if Sargon is in Hard Mode. Row 3 ends with
// "LEVEL <n>"; Easy Mode (CTRL-E) appends an "E" trailing the level number
// ("LEVEL <n>E"/"LEVEL <n> E"). Hard Mode (the boot default) shows none.
func verifyHardMode(m *sargon.Machine) bool {
	row3 := strings.ToUpper(m.TextRow(3))
	i := strings.Index(row3, "LEVEL")
	if i < 0 {
		return true // no level line found; boot default is Hard
	}
	rest := strings.TrimSpace(row3[i+len("LEVEL"):])
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	for j < len(rest) && rest[j] == ' ' {
		j++
	}
	return !(j < len(rest) && rest[j] == 'E') // trailing 'E' => Easy Mode
}

// ---------------------------------------------------------------------------
// Helpers (mirrors of the sargon-xboard adapter's coord<->Sargon converters)
// ---------------------------------------------------------------------------

// coordToSargon converts UCI ("e2e4", "e7e8q", "e1g1") to Sargon "FROM-TO" text.
func coordToSargon(coord string) (string, error) {
	coord = strings.ToLower(strings.TrimSpace(coord))
	if len(coord) < 4 {
		return "", fmt.Errorf("short move %q", coord)
	}
	from := strings.ToUpper(coord[0:2])
	to := strings.ToUpper(coord[2:4])
	if _, ok := sargon.ParseSquare(from); !ok {
		return "", fmt.Errorf("bad from %q", coord)
	}
	if _, ok := sargon.ParseSquare(to); !ok {
		return "", fmt.Errorf("bad to %q", coord)
	}
	move := from + "-" + to
	if len(coord) >= 5 {
		switch coord[4] {
		case 'q', 'r', 'b', 'n':
			move += strings.ToUpper(coord[4:5])
		}
	}
	return move, nil
}

// screenTokenToCoord parses a Sargon move-list token into UCI notation. Sargon
// records most moves as FROM-TO squares ("E2-E4", "E4XD5" for a capture,
// "E7-E8/Q" for a promotion), but a few forms carry no squares — castling
// ("0-0"/"0-0-0") and en passant ("PXPEP") — and are resolved against `ref`,
// the referee's CURRENT position (the position from which Sargon just moved).
// ref may be nil only for the square-bearing forms (e.g. the runConfirm probe).
func screenTokenToCoord(tok string, sargonWhite bool, ref *refchess.Position) string {
	t := strings.ToUpper(strings.TrimSpace(tok))
	// Sargon appends "+" for check (observed: "F5-F4+", "H4-H1+"). The FROM-TO
	// forms below index fixed offsets and are unaffected, but the square-less
	// castling tokens are matched WHOLE — so a castle that gives check ("O-O-O+")
	// must have the marker stripped first or it reads as unparseable.
	t = strings.TrimSuffix(t, "+")
	switch t {
	case "0-0", "O-O":
		if sargonWhite {
			return "e1g1"
		}
		return "e8g8"
	case "0-0-0", "O-O-O":
		if sargonWhite {
			return "e1c1"
		}
		return "e8c8"
	}
	// En passant: Sargon shows the square-less token "PXPEP" (Pawn takes Pawn
	// En Passant). Disambiguate against the referee's unique legal en passant
	// capture. A square-bearing EP variant (were Sargon ever to emit one) still
	// falls through to the FROM-TO parse below after the EP suffix is trimmed.
	if strings.HasSuffix(t, "EP") {
		if ref != nil {
			if eps := ref.EnPassantCaptures(); len(eps) == 1 {
				return eps[0].String()
			}
		}
		t = strings.TrimSuffix(t, "EP")
	}
	promo := ""
	if i := strings.IndexByte(t, '/'); i >= 0 {
		if i+1 < len(t) {
			promo = strings.ToLower(string(t[i+1]))
		}
		t = t[:i]
	}
	if len(t) < 5 || (t[2] != '-' && t[2] != 'X') {
		return ""
	}
	from, ok1 := sargon.ParseSquare(t[0:2])
	to, ok2 := sargon.ParseSquare(t[3:5])
	if !ok1 || !ok2 {
		return ""
	}
	return from.Algebraic() + to.Algebraic() + promo
}

// loadOpenings reads an EPD file (one position per line, optional `id "..."`).
func loadOpenings(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Keep only the board+color+castling+ep fields (drop the id/opcodes).
		if i := strings.Index(line, " id "); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimRight(strings.TrimSpace(line), ";")
		out = append(out, line)
	}
	return out, nil
}

// normalizeFEN ensures a 4-field EPD becomes a 6-field FEN for refchess.
func normalizeFEN(fen string) string {
	f := strings.Fields(fen)
	for len(f) < 6 {
		if len(f) == 4 {
			f = append(f, "0")
		} else {
			f = append(f, "1")
		}
	}
	return strings.Join(f[:6], " ")
}

// loadBookBlob returns the embedded resident book, or the blob at path when
// one is named. Naming a blob is how a book A/B holds everything else fixed —
// same engine image, same Sargon, same budget — and varies only the book.
func loadBookBlob(path string) (*book.Book, error) {
	if path == "" {
		return book.Default()
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// nil names: a named blob file is an ENTRIES blob, and the name text is
	// logging-only -- nothing about move selection reads it.
	return book.Load(blob, nil)
}
