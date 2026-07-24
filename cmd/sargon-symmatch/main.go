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
		openings    = flag.String("openings", "", "EPD file of opening positions (one per line); games cycle through it")
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

	openingList, err := loadOpenings(*openings)
	if err != nil {
		log.Fatalf("openings: %v", err)
	}

	eng, err := newEightfish(*binfile, *defsfile, *lblfile, *usebook, *bookSeed, logw)
	if err != nil {
		log.Fatalf("8fish init: %v", err)
	}
	defer eng.close()

	lg("=== sargon-symmatch: %d game(s), budget B=%d cyc (%d ms), book=%v, symmetric ponder ===",
		*games, *budget, *budget/cyclesPerMs, *usebook)

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
		res := playGame(lg, eng, *dsk, *budget, sargonWhite, fen, *maxMoves, g, &sess)
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
	lg("SYMMATCH-DONE games=%d 8fish_wins=%d 8fish_losses=%d draws=%d", *games, wins, losses, draws)
}

// timeAcct accumulates 8fish's own-move cycle spend against its intended budget
// (income x moves). The debt-bank makes own_total telescope to own_intended, so
// ratio() reports how close per-game/session compute conserved to 1.0x.
type timeAcct struct {
	moves    int
	ownCyc   uint64 // actual emulated cycles spent on 8fish's own-move searches
	intended uint64 // sum of per-move income (== budget B x moves)
}

func (t *timeAcct) add(spent, income uint64) {
	t.moves++
	t.ownCyc += spent
	t.intended += income
}

func (t *timeAcct) fold(o timeAcct) {
	t.moves += o.moves
	t.ownCyc += o.ownCyc
	t.intended += o.intended
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

func newEightfish(binfile, defsfile, lblfile string, usebook bool, bookSeed uint64, logw io.Writer) (*eightfish, error) {
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
	b := &ucibridge.Bridge{Bin: bin, Defs: defs, Ponder: true, Log: logw}
	if usebook {
		bk, err := book.Default()
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

// searchResult is one 8fish own-move outcome.
type searchResult struct {
	move   string // UCI best move ("0000" if none)
	ponder string // predicted opponent reply, or "" if none advertised
	cycles uint64 // T_us: emulated cycles the search actually spent (info nodes)
}

// search runs a flat budget-B search on the position given by (base, moves) and
// returns the move, the predicted reply, and the cycles spent.
func (e *eightfish) search(base string, moves []string, budgetCyc uint64) searchResult {
	e.send("position %s", posSpec(base, moves))
	e.send("go movetime %d", budgetCyc/cyclesPerMs)
	var res searchResult
	for e.out.Scan() {
		line := e.out.Text()
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
	e.send("go ponder movetime %d", budgetCyc/cyclesPerMs)
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

func playGame(lg func(string, ...any), eng *eightfish, dsk string, budget uint64, sargonWhite bool, fen string, maxMoves, gameNo int, sess *timeAcct) gameResult {
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
		sess.fold(gt)
	}()

	// Referee position (authoritative game state + terminal detection).
	startFEN := refchess.StartFEN
	base := "startpos"
	if fen != "" {
		startFEN = normalizeFEN(fen)
		base = "fen " + startFEN
	}
	ref, err := refchess.ParseFEN(startFEN)
	if err != nil {
		lg("GAME %d: bad opening FEN %q: %v", gameNo, fen, err)
		return resDraw
	}
	seen := map[uint64]int{ref.ZobristKey(): 1}

	// Boot Sargon fresh for this game, Hard Mode (NO Easy Mode), Infinite level.
	m, err := sargon.NewMachine(dsk)
	if err != nil {
		lg("GAME %d: sargon new: %v", gameNo, err)
		return resDraw
	}
	if err := m.BootToPrompt(); err != nil {
		lg("GAME %d: sargon boot: %v", gameNo, err)
		return resDraw
	}
	m.HardMode = true
	if err := m.InfiniteLevel(); err != nil {
		lg("GAME %d: sargon infinite level: %v", gameNo, err)
		return resDraw
	}
	m.Run(1_000_000)
	hard := verifyHardMode(m)
	if fen != "" {
		if err := m.SetupPosition(fen); err != nil {
			lg("GAME %d: sargon setup %q: %v", gameNo, fen, err)
			return resDraw
		}
	}
	lg("GAME %d START: sargonWhite=%v hardMode=%v(row3=%q) opening=%q budget=%d",
		gameNo, sargonWhite, hard, strings.TrimSpace(m.TextRow(3)), fen, budget)

	eng.newGame()
	var moves []string // UCI move list from the opening position

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
		if s == "" || !applyMove(ref, seen, s) {
			lg("GAME %d: sargon opening move unreadable/illegal token=%q", gameNo, res.SargonText)
			return resDraw
		}
		moves = append(moves, s)
		lg("MOVE g%d ply1 SARGON(open) move=%s think=%d (no 8fish ponder: opening)", gameNo, s, res.ThinkCycles)
		if term, r := terminal(ref, seen, moves, maxMoves, sargonWhite); term {
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
		if !applyMove(ref, seen, sr.move) {
			lg("MOVE g%d 8fish played illegal-to-referee move %q — aborting", gameNo, sr.move)
			return resDraw
		}
		moves = append(moves, sr.move)
		usPly := len(moves)

		// Sargon ponder window == 8fish's exact think (T_us).
		runSargonCycles(m, sr.cycles)

		if term, r := terminal(ref, seen, moves, maxMoves, sargonWhite); term {
			lg("MOVE g%d ply%d 8fish move=%s think=%d -> %s", gameNo, usPly, sr.move, sr.cycles, r)
			return r
		}

		// --- Steps 2+3: inject 8fish's move; Sargon commits its reply (T_sargon). ---
		sargonTxt, err := coordToSargon(sr.move)
		if err != nil {
			lg("MOVE g%d bad move->sargon %q: %v", gameNo, sr.move, err)
			return resDraw
		}
		reply, err := m.RequestMove(sargonTxt, budget)
		if err != nil {
			// No reply: Sargon may report game over (mate/stalemate/draw) on screen.
			lg("MOVE g%d sargon no reply to %s: %v (msg=%q)", gameNo, sr.move, err, reply.Message)
			return adjudicate(lg, gameNo, ref, m, false)
		}
		if reply.GameOver && reply.SargonText == "" {
			lg("MOVE g%d 8fish move %s ended game (sargon msg=%q)", gameNo, sr.move, reply.Message)
			return adjudicate(lg, gameNo, ref, m, false)
		}
		s := screenTokenToCoord(reply.SargonText, sargonWhite, ref)
		if s == "" || !applyMove(ref, seen, s) {
			lg("MOVE g%d sargon reply unreadable/illegal token=%q msg=%q — adjudicating",
				gameNo, reply.SargonText, reply.Message)
			return adjudicate(lg, gameNo, ref, m, false)
		}

		// --- Step 5: 8fish ponders EXACTLY T_sargon on the predicted reply. ---
		var ponderCyc uint64
		hit := "n/a"
		if sr.ponder != "" && reply.ThinkCycles > 0 {
			ponderCyc = eng.ponder(base, moves, sr.ponder, reply.ThinkCycles)
			if sr.ponder == s {
				hit = "HIT"
			} else {
				hit = "MISS"
			}
		}

		// --- Step 6: inject Sargon's move into 8fish (append to the line). ---
		moves = append(moves, s)
		sargonPly := len(moves)

		// Auditable symmetry line: 8fish think == Sargon ponder-window (exact),
		// and Sargon think ~= 8fish ponder (budget-matched).
		lg("MOVE g%d ply%d 8fish move=%s think=%d | sargon_ponder_window=%d || ply%d SARGON move=%s think=%d | 8fish_ponder=%d pred=%s(%s)",
			gameNo, usPly, sr.move, sr.cycles, sr.cycles,
			sargonPly, s, reply.ThinkCycles, ponderCyc, sr.ponder, hit)

		if term, r := terminal(ref, seen, moves, maxMoves, sargonWhite); term {
			lg("MOVE g%d after sargon %s -> %s (msg=%q)", gameNo, s, r, reply.Message)
			return r
		}
	}
}

// runSargonCycles advances the Sargon emulator by exactly `cycles` 6502 cycles,
// during which (Hard Mode) Sargon ponders the opponent's position. Small chunks
// keep the overshoot below one chunk.
func runSargonCycles(m *sargon.Machine, cycles uint64) {
	if cycles == 0 {
		return
	}
	const chunk = 200_000
	start := m.Cycles()
	for m.Cycles()-start < cycles {
		if err := m.Run(chunk); err != nil {
			return
		}
	}
}

// applyMove applies a UCI move to the referee and bumps the repetition count.
// Returns false if the move is illegal in the referee position.
func applyMove(ref *refchess.Position, seen map[uint64]int, uci string) bool {
	mv, err := refchess.ParseMove(uci)
	if err != nil {
		return false
	}
	if err := ref.Make(mv); err != nil {
		return false
	}
	seen[ref.ZobristKey()]++
	return true
}

// terminal reports whether the game is over from the referee's view (the
// authoritative referee, cross-checked against Sargon's screen at adjudication).
func terminal(ref *refchess.Position, seen map[uint64]int, moves []string, maxMoves int, sargonWhite bool) (bool, gameResult) {
	// Side to move has no legal move: checkmate (loss for stm) or stalemate.
	if len(ref.LegalMoves()) == 0 {
		if ref.InCheck() {
			// The side to move is checkmated. 8fish is White iff !sargonWhite.
			stmWhite := ref.SideToMove() == 0
			eightfishMated := stmWhite == (!sargonWhite)
			if eightfishMated {
				return true, resLoss
			}
			return true, resWin
		}
		return true, resDraw // stalemate
	}
	if seen[ref.ZobristKey()] >= 3 {
		return true, resDraw // threefold repetition
	}
	if ref.HalfmoveClock() >= 100 {
		return true, resDraw // fifty-move rule
	}
	if ref.InsufficientMaterial() {
		return true, resDraw
	}
	if len(moves)/2 >= maxMoves {
		return true, resDraw // move-cap adjudication
	}
	return false, resDraw
}

// adjudicate resolves a game that ended without a clean referee-terminal (e.g.
// Sargon declined to move and showed a terminal message). It trusts the
// referee's terminal verdict first, then Sargon's on-screen message, and dumps
// the screen for audit. eightfishStuck marks the case where 8fish had no move.
func adjudicate(lg func(string, ...any), gameNo int, ref *refchess.Position, m *sargon.Machine, eightfishStuck bool) gameResult {
	// Referee terminal?
	if len(ref.LegalMoves()) == 0 {
		if ref.InCheck() {
			// The side to move is mated. If it's 8fish's move it lost, else won.
			// eightfishStuck implies the side to move IS 8fish.
			if eightfishStuck {
				lg("GAME %d ADJUDICATE: 8fish checkmated (referee)", gameNo)
				return resLoss
			}
			// Sargon to move and mated -> 8fish wins.
			lg("GAME %d ADJUDICATE: checkmate on the board (referee)", gameNo)
			// Determine which side is mated by side-to-move.
			return resWin
		}
		lg("GAME %d ADJUDICATE: stalemate (referee) -> draw", gameNo)
		return resDraw
	}
	msg := strings.ToUpper(m.Screen())
	dump := func() { lg("GAME %d SCREEN-DUMP:\n%s", gameNo, m.Screen()) }
	switch {
	case strings.Contains(msg, "STALEMATE"), strings.Contains(msg, "DRAW"):
		dump()
		lg("GAME %d ADJUDICATE: Sargon declares draw/stalemate (reclassify) -> draw", gameNo)
		return resDraw
	case strings.Contains(msg, "MATE"): // CHECKMATE / MATE
		dump()
		// Whoever is to move on the board is mated.
		stmWhite := ref.SideToMove() == 0
		_ = stmWhite
		lg("GAME %d ADJUDICATE: Sargon shows mate", gameNo)
		if eightfishStuck {
			return resLoss
		}
		return resWin
	default:
		dump()
		lg("GAME %d ADJUDICATE: unresolved (msg unclear) -> draw", gameNo)
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
