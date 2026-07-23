// Package ucibridge presents the emulated 6502 engine as a UCI engine.
// It keeps game state with the refchess referee, runs one harness
// machine per "go", and carries the aux bank (the transposition table)
// forward between moves so the TT persists across a game.
//
// The engine's clock is emulated time, not wall time: budgets are
// converted at 1020.5 cycles/ms. By default the bridge derives a budget
// from movetime (as emulated ms) or wtime/btime (remaining/30 + inc/2);
// the EmulatedMovetimeMs option pins a fixed per-move budget instead,
// which is how gauntlets give the 6502 its "real" control regardless of
// the wall clocks opponents run at.
package ucibridge

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

const (
	// cyclesPerMs mirrors chesstest.CyclesPerMs (the engine's effective
	// clock, 1.0205 MHz, rounded down to 1020 cycles/ms).
	cyclesPerMs = chesstest.CyclesPerMs
	maxDepthCap = 24
)

type Bridge struct {
	Bin  []byte
	Defs chesstest.Defs

	// FixedBudgetMs, if nonzero, is the per-move emulated-time budget in
	// ms, overriding anything in the go command.
	FixedBudgetMs uint64

	// Dither seeds the engine's eval-dither PRNG with a fresh random
	// byte each move, breaking deterministic move repetition (the
	// hardware build will seed this from input timing instead).
	Dither bool

	// Banked enables chess-clock banking: unused per-move cycles carry
	// forward, and each move spends base + bank/8 (chesstest.BankedClock).
	// Only meaningful with a per-move budget (FixedBudgetMs/movetime);
	// fixed-depth mode ignores it.
	Banked bool

	// Adaptive enables on-device adaptive time/effort management (FT2_ADAPT):
	// the bridge runs the BankedClock as with Banked (per-move income, /8
	// smoothing, carry), but each move ALSO installs the movable-ceiling
	// params (CEILMAX/UNSTCEIL/MINSPEND) so the engine spends MORE on
	// critical/unstable moves and LESS on easy ones at the same per-game
	// total. Only meaningful with a per-move budget; supersedes Banked when
	// both are set. Byte-identical to Banked when unset.
	Adaptive bool

	// Ponder enables pondering: searching during the opponent's turn on the
	// position we would face if the opponent plays our predicted reply,
	// letting that search warm the transposition table that already carries
	// across moves via b.aux. On a ponder hit the follow-up real search
	// begins with a TT warm for the exact position (a real head start); on a
	// miss the entries are simply less relevant (a TT is never wrong), so no
	// discard is needed. When Ponder is false the bridge is BYTE-IDENTICAL to
	// the non-pondering engine (every ponder code path is gated on this flag).
	Ponder bool
	// PonderBudgetMs, if nonzero, pins the ponder search's emulated-time
	// budget in ms. In a real gauntlet this stands in for however long the
	// opponent takes to move; zero derives it from the go arguments like a
	// normal search. Only meaningful when Ponder is true.
	PonderBudgetMs uint64

	// Book, if non-nil, is the resident opening book. Before searching, the
	// bridge loads the blob into the emulated machine's $2000 hole and runs
	// the engine's ON-DEVICE probe (the asm bookentry point): the 6502
	// engine computes the position's 32-bit Zobrist key (== asm HASH0-3),
	// binary-searches the resident array, weighted-picks among the equal-key
	// moves, and returns the move to play WITHOUT any node search. The bridge
	// merely supplies the random value and reads back the engine's choice;
	// the selection is byte-for-byte the resident engine's own (proven by
	// chesstest.TestBookProbeParityASMvsGo). When Book is nil the bridge
	// behaves byte-identically to the bookless engine (existing tests/
	// fingerprints unaffected).
	Book *book.Book
	// BookEntry is the address of the asm bookentry label (from engine.lbl);
	// required when Book is set. The bridge starts the machine here for the
	// probe pass.
	BookEntry uint16
	// BookSeed seeds the deterministic PRNG that drives the weighted book
	// pick, so A/B replays reproduce (same seed -> same book choices). The
	// engine consumes this value; only the PRNG lives host-side.
	BookSeed uint64

	pos   *refchess.Position
	aux   []byte // carried-over aux bank (TT state); nil until first move
	rnd   func() byte
	info  string // "info depth ... score cp ..." from the last think
	clock *chesstest.BankedClock

	// Ponder bookkeeping (all only touched when Ponder is true).
	ponderArgs      []string // go-command args (minus "ponder") for the pending ponder / its ponderhit budget
	ponderBest      string   // best move the last ponder search found (held for the UCI stop reply)
	ponderPredicted string   // the reply P last advertised in "bestmove M ponder P"

	bookRnd func() uint32
	// CurOpening is the name ID (+1) of the opening the last book move
	// belonged to; 0 means "not in book / out of book". CurOpeningName is
	// its text ("ECO Name"), surfaced in the info log ("in book: ...").
	CurOpening     byte
	CurOpeningName string

	// PonderSelf enables gauntlet self-pondering: when the driving GUI does NOT
	// speak the UCI go-ponder/ponderhit handshake (e.g. cutechess-cli), the
	// bridge emulates a pondering interval internally. On each real "go" it
	// first ponders the position it predicted last move (root+M+P) under
	// PonderBudgetMs — warming the carried TT exactly as a go-ponder would —
	// then runs the real search on the actual position (root+M+S), recording
	// whether the opponent's reply S matched the prediction P (the ponder-hit
	// rate). Requires Ponder. When PonderSelf is true the "bestmove M ponder P"
	// advisory and the Ponder UCI option are suppressed so a non-pondering GUI
	// never tries to drive pondering itself. When PonderSelf is false the bridge
	// is byte-identical to before (every self-ponder path is gated on it).
	PonderSelf bool
	// Log, if non-nil, receives out-of-band measurement lines (book openings,
	// ponder hit/miss, per-game ponder summaries). A gauntlet points this at the
	// engine's stderr so the ponder-hit rate is captured to a file regardless of
	// whether the GUI surfaces "info string" lines.
	Log io.Writer

	// Self-ponder bookkeeping (only touched when PonderSelf is true).
	posBase       []string // "startpos" / "fen ..." prefix of the last position command
	posMoves      []string // moves list of the last position command
	gameNo        int      // 1-based game counter (bumped on ucinewgame)
	ponderHits    int      // ponder hits this game
	ponderTotal   int      // ponder intervals this game
	ponderHitsAll int      // ponder hits over the whole session
	ponderTotAll  int      // ponder intervals over the whole session

	// Time-budget audit bookkeeping (only touched when b.Log != nil). Own-move
	// cycles are our REAL search cost (think -> runEngine, m.Cycles); ponder
	// cycles are opponent-time (ponder -> runEngine) and are kept SEPARATE so
	// the two are never conflated. The per-game own-move total is compared
	// against income*moves (the intended per-game budget) — the bank conserves
	// total, so the ratio should sit at ~1.0.
	gameIncome    uint64 // per-move income (operating budget) in force this game
	ownMoveCyc    uint64 // sum of own-move (real search) cycles this game
	ownMoveCount  int    // own moves searched this game
	ponderCyc     uint64 // sum of ponder-search cycles this game
	ownMoveCycAll uint64 // own-move cycles over the whole session
	ownMoveCntAll int    // own moves over the whole session
	ponderCycAll  uint64 // ponder cycles over the whole session
	intendedAll   uint64 // sum of income*moves across games (session intended budget)
	timeFlushed   bool   // guards TIME-SESSION-SUMMARY against the quit-then-EOF double flush
}

// logf writes one out-of-band measurement line to b.Log (no-op if unset).
func (b *Bridge) logf(format string, args ...any) {
	if b.Log == nil {
		return
	}
	fmt.Fprintf(b.Log, format+"\n", args...)
}

// Run processes UCI commands until quit/EOF. Protocol errors are
// reported on w via "info string".
func (b *Bridge) Run(r io.Reader, w io.Writer) error {
	out := bufio.NewWriter(w)
	defer out.Flush()
	say := func(format string, args ...any) {
		fmt.Fprintf(out, format+"\n", args...)
		out.Flush()
	}

	b.pos, _ = refchess.ParseFEN(refchess.StartFEN)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "uci":
			say("id name chess6502")
			say("id author zellyn + Claude")
			say("option name EmulatedMovetimeMs type spin default 0 min 0 max 3600000")
			if b.Ponder && !b.PonderSelf {
				say("option name Ponder type check default true")
			}
			say("uciok")
		case "isready":
			say("readyok")
		case "ucinewgame":
			if b.PonderSelf && b.ponderTotal > 0 {
				b.logf("PONDER-GAME-SUMMARY game=%d hits=%d total=%d rate=%.3f",
					b.gameNo, b.ponderHits, b.ponderTotal,
					float64(b.ponderHits)/float64(b.ponderTotal))
			}
			b.flushGameTime() // TIME-GAME-SUMMARY for the game just finished
			b.gameNo++
			b.ponderHits, b.ponderTotal = 0, 0
			b.posBase, b.posMoves = nil, nil
			b.aux = nil // clear the TT
			b.pos, _ = refchess.ParseFEN(refchess.StartFEN)
			b.bookRnd = nil // restart the book-choice stream (re-derived per game)
			b.CurOpening, b.CurOpeningName = 0, ""
			b.ponderArgs, b.ponderBest, b.ponderPredicted = nil, "", ""
		case "setoption":
			// setoption name X value Y
			if len(fields) >= 5 && strings.EqualFold(fields[2], "EmulatedMovetimeMs") {
				if v, err := strconv.ParseUint(fields[4], 10, 64); err == nil {
					b.FixedBudgetMs = v
				}
			}
		case "position":
			if err := b.setPosition(fields[1:]); err != nil {
				say("info string position error: %v", err)
			}
		case "go":
			args := fields[1:]
			if b.Ponder && hasToken(args, "ponder") {
				// UCI ponder: the GUI has already set the position to
				// root+M+P (our move plus the predicted reply) and asks us to
				// think on it. Warm the carried TT for that position; hold the
				// bestmove until ponderhit/stop (do not emit one here).
				b.ponderArgs = stripToken(args, "ponder")
				if err := b.ponder(); err != nil {
					say("info string ponder error: %v", err)
				} else if b.info != "" {
					say("%s", b.info)
				}
				continue
			}
			if b.Ponder && b.PonderSelf {
				b.selfPonder(say)
			}
			b.emitMove(say, args)
		case "ponderhit":
			// The opponent played the predicted reply P; the position is still
			// root+M+P. Continue into the real follow-up search — it now begins
			// with a TT the ponder search already warmed. Budget comes from the
			// original "go ponder" line (UCI sends none on ponderhit).
			b.emitMove(say, b.ponderArgs)
		case "stop":
			// Ponder miss / abort: UCI requires a bestmove reply. We emit the
			// move the (completed) ponder search found; the GUI discards it on
			// a miss. The carried TT stays valid — the next real "go" benefits
			// regardless of whether this was a hit. (On real hardware a true
			// asynchronous stop would halt the running search here and read out
			// its best-so-far; the emulator runs each search to its budget, so
			// "stop" arrives after the ponder search has already finished.)
			if b.ponderBest != "" {
				say("bestmove %s", b.ponderBest)
			} else {
				say("bestmove 0000")
			}
		case "quit":
			b.flushSessionSummary()
			return nil
		}
	}
	b.flushSessionSummary()
	return sc.Err()
}

// flushGameTime emits the per-game time-budget audit line (own-move total vs the
// intended income*moves, plus the ratio and the separate ponder total) and rolls
// the per-game figures into the session accumulators, then zeroes the per-game
// counters. No-op when time auditing is off or the game had no timed own move
// (guards the quit-then-EOF double flush).
func (b *Bridge) flushGameTime() {
	if b.Log == nil || b.ownMoveCount == 0 {
		return
	}
	intended := b.gameIncome * uint64(b.ownMoveCount)
	ratio := 0.0
	if intended > 0 {
		ratio = float64(b.ownMoveCyc) / float64(intended)
	}
	b.logf("TIME-GAME-SUMMARY game=%d moves=%d income=%d total=%d intended=%d ratio=%.4f ponder=%d",
		b.gameNo, b.ownMoveCount, b.gameIncome, b.ownMoveCyc, intended, ratio, b.ponderCyc)
	b.ownMoveCycAll += b.ownMoveCyc
	b.ownMoveCntAll += b.ownMoveCount
	b.ponderCycAll += b.ponderCyc
	b.intendedAll += intended
	b.ownMoveCyc, b.ownMoveCount, b.ponderCyc = 0, 0, 0
}

// flushSessionSummary logs the final game's per-game summaries (if unflushed) and
// the whole-session ponder-hit rate and time-budget adherence. Called on quit and
// at EOF; each section self-guards against the resulting double invocation.
func (b *Bridge) flushSessionSummary() {
	if b.PonderSelf && b.ponderTotal > 0 {
		b.logf("PONDER-GAME-SUMMARY game=%d hits=%d total=%d rate=%.3f",
			b.gameNo, b.ponderHits, b.ponderTotal,
			float64(b.ponderHits)/float64(b.ponderTotal))
		b.ponderTotal = 0 // avoid double-flush (quit then EOF)
	}
	b.flushGameTime() // TIME-GAME-SUMMARY for the final game
	if b.PonderSelf && b.ponderTotAll > 0 {
		b.logf("PONDER-SESSION-SUMMARY games=%d hits=%d total=%d rate=%.3f",
			b.gameNo, b.ponderHitsAll, b.ponderTotAll,
			float64(b.ponderHitsAll)/float64(b.ponderTotAll))
		b.ponderTotAll = 0
	}
	if b.Log != nil && !b.timeFlushed && b.ownMoveCntAll > 0 {
		ratio := 0.0
		if b.intendedAll > 0 {
			ratio = float64(b.ownMoveCycAll) / float64(b.intendedAll)
		}
		b.logf("TIME-SESSION-SUMMARY games=%d moves=%d own_total=%d own_intended=%d ratio=%.4f ponder_total=%d",
			b.gameNo, b.ownMoveCntAll, b.ownMoveCycAll, b.intendedAll, ratio, b.ponderCycAll)
		b.timeFlushed = true
	}
}

func (b *Bridge) setPosition(args []string) error {
	var err error
	i := 0
	switch {
	case len(args) > 0 && args[0] == "startpos":
		b.pos, err = refchess.ParseFEN(refchess.StartFEN)
		i = 1
	case len(args) > 0 && args[0] == "fen":
		// FEN is the next up-to-6 fields, until "moves".
		j := 1
		for j < len(args) && args[j] != "moves" {
			j++
		}
		b.pos, err = refchess.ParseFEN(strings.Join(args[1:j], " "))
		i = j
	default:
		return fmt.Errorf("bad position command")
	}
	if err != nil {
		return err
	}
	// Record the base spec and full move list so self-pondering can reconstruct
	// the predicted position (root+M+P) — args[:i] is "startpos"/"fen f1..f6".
	b.posBase = append(b.posBase[:0:0], args[:i]...)
	b.posMoves = nil
	if i < len(args) && args[i] == "moves" {
		b.posMoves = append(b.posMoves[:0:0], args[i+1:]...)
		for _, ms := range args[i+1:] {
			mv, err := refchess.ParseMove(ms)
			if err != nil {
				return fmt.Errorf("move %q: %w", ms, err)
			}
			if err := b.pos.Make(mv); err != nil {
				return fmt.Errorf("move %q: %w", ms, err)
			}
		}
	}
	return nil
}

// buildPosition constructs a fresh position from a position-command base spec
// ("startpos" or "fen f1..f6") plus a move list. Used to reconstruct the
// position we predicted (root+M+P) for self-pondering.
func buildPosition(base, moves []string) (*refchess.Position, error) {
	var pos *refchess.Position
	var err error
	switch {
	case len(base) > 0 && base[0] == "startpos":
		pos, err = refchess.ParseFEN(refchess.StartFEN)
	case len(base) > 0 && base[0] == "fen":
		pos, err = refchess.ParseFEN(strings.Join(base[1:], " "))
	default:
		return nil, fmt.Errorf("bad position base %v", base)
	}
	if err != nil {
		return nil, err
	}
	for _, ms := range moves {
		mv, err := refchess.ParseMove(ms)
		if err != nil {
			return nil, err
		}
		if err := pos.Make(mv); err != nil {
			return nil, err
		}
	}
	return pos, nil
}

// budgetCycles derives the emulated budget from the go arguments.
func (b *Bridge) budgetCycles(args []string) uint64 {
	if b.FixedBudgetMs != 0 {
		return b.FixedBudgetMs * cyclesPerMs
	}
	get := func(name string) (uint64, bool) {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				v, err := strconv.ParseUint(args[i+1], 10, 64)
				if err == nil {
					return v, true
				}
			}
		}
		return 0, false
	}
	if mt, ok := get("movetime"); ok {
		return mt * cyclesPerMs
	}
	timeName, incName := "wtime", "winc"
	if b.pos.SideToMove() != 0 {
		timeName, incName = "btime", "binc"
	}
	if remaining, ok := get(timeName); ok {
		inc, _ := get(incName)
		return (remaining/30 + inc/2) * cyclesPerMs
	}
	return 30_000 * cyclesPerMs // default: 30 emulated seconds
}

// probeBook runs the engine's ON-DEVICE book probe over the current
// position. On a hit it applies the engine-chosen book move to the game
// state and returns its UCI form; ok=false means Book is disabled or the
// position is out of book (fall through to search). The move choice is the
// resident engine's own, deterministic given BookSeed.
func (b *Bridge) probeBook() (move string, ok bool, err error) {
	if b.Book == nil {
		return "", false, nil
	}
	if b.bookRnd == nil {
		// Vary the book stream per game (gameNo folded in) so a gauntlet driven
		// by one long-lived engine process gets opening variety across games,
		// while gameNo==0 (direct think() use in tests) reproduces the original
		// BookSeed stream exactly.
		seed := b.BookSeed + uint64(b.gameNo)*0x9E3779B97F4A7C15
		r := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
		b.bookRnd = func() uint32 { return r.Uint32() }
	}
	pos, err := chesstest.ParseFEN(b.pos.FEN())
	if err != nil {
		return "", false, err
	}
	// Draw one random value regardless of hit/miss so the PRNG stream —
	// hence the whole game's book choices — depends only on BookSeed and
	// the move number, not on where the game left the book. The engine does
	// the key/search/pick; we only feed it r.
	r := b.bookRnd()
	res, err := chesstest.AsmBookProbe(b.Bin, b.Defs, b.BookEntry, b.Book.Blob(), pos, r)
	if err != nil {
		return "", false, err
	}
	if !res.Hit {
		return "", false, nil
	}
	move = res.Move
	mv, err := refchess.ParseMove(move)
	if err != nil {
		return "", false, fmt.Errorf("book move %q: %w", move, err)
	}
	if err := b.pos.Make(mv); err != nil {
		return "", false, fmt.Errorf("book move %q illegal: %w", move, err)
	}
	name := b.Book.Name(res.NameID) // name text is host-side (log) only
	b.CurOpening = res.NameID + 1
	b.CurOpeningName = name
	b.info = fmt.Sprintf("info string in book: %s", name)
	b.logf("BOOK game=%d move=%s name=%q", b.gameNo, move, name)
	return move, true, nil
}

// engineResult is one raw harness search outcome (before it is committed to
// game state), as read out of the machine after runEngine.
type engineResult struct {
	code            int    // engine exit code: 0 = move found, 2 = no legal move
	from, to, flags byte   // BESTFROM/BESTTO/BESTFLAGS (engine 0x88 + flag encoding)
	score           int16  // SCORE, side-to-move POV
	depth           byte   // CURDEPTH, the deepest iterative-deepening iteration reached
	nodes           uint64 // machine cycles spent (the engine's node/cost proxy)
}

// runEngine builds a fresh harness machine over pos, restores the carried aux
// (TT), runs one search under the given cycle budget (0 = fixed-depth mode)
// and depth cap, then carries the resulting aux forward in b.aux. halfmove is
// poked into HALFMOVE; seed >= 0 is poked into SEED (eval dither), seed < 0
// leaves SEED at its default. It never touches game state (b.pos). This is the
// exact machine-setup sequence that think() and the ponder search share, so
// the carried TT is populated identically whichever drives it.
// adaptiveAlloc holds the FT2_ADAPT movable-ceiling params for a move (all in
// cycles; the engine converts to its 256-cycle units). Nil => flat search.
type adaptiveAlloc struct {
	maxCeiling uint64 // panic target / hard max = min(4*income, income+bank)
	unstTarget uint64 // instability target = min(3*income, maxCeiling)
	minSpend   uint64 // easy-stop minimum spend = income/4
}

func (b *Bridge) runEngine(pos *chesstest.Position, halfmove byte, budget uint64, depth byte, seed int, adapt *adaptiveAlloc) (engineResult, error) {
	// Gameplay config: FEATURES stays the NewMachine 0x1F default.
	// FT2_IMPROV (improving-LMR) was adopted 2026-07-21 on a +13 ± 9 cycle
	// screen, then RETRACTED 2026-07-22: a 4200-game confirmation SPRT
	// landed at −1.8 ± 8.6 (no demonstrable benefit; screen's +13 outside
	// the CI). FEATURES2 now stays 0 in gameplay. The asm implementation
	// remains gated behind FT2_IMPROV for re-test; excising it (to reclaim
	// its ~0.19% feature-off gate-check tax) is a deferred search.s cleanup.
	m, err := chesstest.NewMachine(b.Bin, b.Defs, pos, 0, io.Discard)
	if err != nil {
		return engineResult{}, err
	}
	chesstest.SetBudget(m, b.Defs, budget, depth)
	if adapt != nil {
		chesstest.SetAdaptive(m, b.Defs, adapt.maxCeiling, adapt.unstTarget, adapt.minSpend)
	}
	m.Mem.Main[b.Defs["HALFMOVE"]] = halfmove
	if seed >= 0 {
		m.Mem.Main[b.Defs["SEED"]] = byte(seed)
	}
	if b.aux != nil {
		copy(m.Mem.Aux[:], b.aux) // restore the TT
	}

	runCap := budget*3 + 4_000_000_000
	if budget == 0 {
		runCap = 300_000_000_000 // fixed-depth mode: deep searches take what they take
	}
	exited, code, err := m.Run(runCap)
	if err != nil {
		return engineResult{}, err
	}
	if !exited {
		return engineResult{}, fmt.Errorf("engine did not finish")
	}
	if b.aux == nil {
		b.aux = make([]byte, len(m.Mem.Aux))
	}
	copy(b.aux, m.Mem.Aux[:]) // carry the TT forward
	return engineResult{
		code:  int(code),
		from:  m.Mem.Main[b.Defs["BESTFROM"]],
		to:    m.Mem.Main[b.Defs["BESTTO"]],
		flags: m.Mem.Main[b.Defs["BESTFLAGS"]],
		score: int16(uint16(m.Mem.Main[b.Defs["SCORE"]]) |
			uint16(m.Mem.Main[b.Defs["SCORE"]+1])<<8),
		depth: m.Mem.Main[b.Defs["CURDEPTH"]],
		nodes: m.Cycles,
	}, nil
}

// think runs the engine over the current position and returns the move
// in UCI form, also applying it to the bridge's game state.
func (b *Bridge) think(args []string) (string, error) {
	if move, ok, err := b.probeBook(); err != nil {
		return "", err
	} else if ok {
		return move, nil
	}
	pos, err := chesstest.ParseFEN(b.pos.FEN())
	if err != nil {
		return "", err
	}
	depth := byte(maxDepthCap)
	budget := b.budgetCycles(args)
	if d, ok := goDepth(args); ok {
		depth, budget = d, 0 // fixed-depth mode
	}
	// income is this move's operating budget (== the per-game bank's per-move
	// income) — captured for the time-budget audit before the bank/adaptive
	// logic reassigns budget to the actual allocation. 0 in fixed-depth mode.
	income := budget
	var adapt *adaptiveAlloc
	if (b.Banked || b.Adaptive) && budget != 0 {
		if b.clock == nil || b.clock.Base != income {
			b.clock = &chesstest.BankedClock{Base: income}
		}
		budget = b.clock.Alloc() // base ceiling = income + bank/8
		if b.Adaptive {
			// Movable-ceiling params, computed exactly as mirror.SearchTimed:
			// hard max = min(4*income, income+bank), clamped >= base ceiling;
			// instability target = min(3*income, hard max); min spend =
			// income/4. The engine raises the ceiling on-device from these.
			maxCeiling := 4 * income
			if lim := income + b.clock.Bank(); maxCeiling > lim {
				maxCeiling = lim
			}
			if maxCeiling < budget {
				maxCeiling = budget
			}
			unstTarget := 3 * income
			if unstTarget > maxCeiling {
				unstTarget = maxCeiling
			}
			adapt = &adaptiveAlloc{maxCeiling: maxCeiling, unstTarget: unstTarget, minSpend: income / 4}
		}
	}
	seed := -1
	if b.Dither {
		if b.rnd == nil {
			r := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			b.rnd = func() byte { return byte(r.IntN(255) + 1) }
		}
		seed = int(b.rnd())
	}
	res, err := b.runEngine(pos, byte(min(b.pos.HalfmoveClock(), 255)), budget, depth, seed, adapt)
	if err != nil {
		return "", err
	}
	if b.clock != nil && budget != 0 {
		b.clock.Settle(res.nodes)
	}
	// Time-budget audit: log this move's ACTUAL own-move cycles (res.nodes, the
	// real search — never the ponder search) against its income, the allocation
	// it drew from the bank, and the per-move spent/income ratio (which visibly
	// extends on hard positions and shortens on easy ones under -adaptive). The
	// bank carries the residual forward so the per-game total settles near
	// income*moves. Accumulated for the per-game/session adherence lines.
	if b.Log != nil && income != 0 {
		bank := uint64(0)
		if b.clock != nil {
			bank = b.clock.Bank()
		}
		b.gameIncome = income
		b.ownMoveCyc += res.nodes
		b.ownMoveCount++
		b.logf("TIME-MOVE game=%d move=%d income=%d alloc=%d spent=%d ratio=%.3f bank=%d",
			b.gameNo, b.ownMoveCount, income, budget, res.nodes, float64(res.nodes)/float64(income), bank)
	}

	if res.code == 2 {
		return "0000", nil // no legal move; cutechess shouldn't ask
	}
	if res.code != 0 {
		return "", fmt.Errorf("engine exit code %d", res.code)
	}
	b.info = fmt.Sprintf("info depth %d score cp %d nodes %d", res.depth, res.score, res.nodes)
	move := chesstest.MoveUCI(res.from, res.to, res.flags)
	mv, err := refchess.ParseMove(move)
	if err != nil {
		return "", fmt.Errorf("engine move %q: %w", move, err)
	}
	if err := b.pos.Make(mv); err != nil {
		return "", fmt.Errorf("engine played illegal %q: %w", move, err)
	}
	return move, nil
}

// emitMove runs the real search for the current position, prints its info and
// bestmove, and — when Ponder is on and a reply can be predicted — appends the
// "ponder P" advisory that tells the GUI which move to have us ponder next.
func (b *Bridge) emitMove(say func(string, ...any), args []string) {
	move, err := b.think(args)
	if err != nil {
		say("info string engine error: %v", err)
		say("bestmove 0000")
		return
	}
	if b.info != "" {
		say("%s", b.info)
	}
	if b.Ponder {
		if p, ok, perr := b.ponderPrediction(); perr == nil && ok {
			b.ponderPredicted = p
			if !b.PonderSelf {
				// Advise the GUI which reply to have us ponder next. Suppressed
				// under PonderSelf, where the bridge ponders internally instead
				// (see selfPonder) and a plain bestmove keeps a non-pondering GUI
				// from trying to drive the handshake.
				say("bestmove %s ponder %s", move, p)
				return
			}
		}
	}
	say("bestmove %s", move)
}

// selfPonder emulates one pondering interval for gauntlet drivers that do not
// speak the UCI ponder handshake. Using the reply P predicted after our last
// move, it ponders the position we WOULD have faced (root+M+P) under
// PonderBudgetMs — warming the carried TT (b.aux) exactly as a go-ponder would —
// then measures whether the opponent's actual reply S (the last move in the
// current position) matched P. On a hit the follow-up real search (emitMove,
// run next) inherits the warm TT for the exact position it now faces; on a miss
// the TT is merely less relevant (never wrong). Game state is restored before
// returning, so the ensuing real search sees the true position root+M+S.
func (b *Bridge) selfPonder(say func(string, ...any)) {
	if b.ponderPredicted == "" || len(b.posMoves) == 0 {
		return // first move of the game, or nothing predicted yet
	}
	p := b.ponderPredicted
	s := b.posMoves[len(b.posMoves)-1] // opponent's actual reply
	b.ponderPredicted = ""             // consumed

	// Reconstruct root+M+P: the current move list with the opponent's actual
	// reply replaced by our prediction P.
	predMoves := append(append([]string(nil), b.posMoves[:len(b.posMoves)-1]...), p)
	if predPos, err := buildPosition(b.posBase, predMoves); err == nil {
		saved := b.pos
		b.pos = predPos
		b.ponderArgs = nil // budget comes from PonderBudgetMs
		if perr := b.ponder(); perr != nil {
			say("info string self-ponder error: %v", perr)
		}
		b.pos = saved
	} else {
		say("info string self-ponder rebuild error: %v", err)
	}

	hit := s == p
	b.ponderTotal++
	b.ponderTotAll++
	if hit {
		b.ponderHits++
		b.ponderHitsAll++
	}
	label := "MISS"
	if hit {
		label = "HIT"
	}
	say("info string ponder %s predicted %s actual %s (game %d/%d, all %d/%d)",
		label, p, s, b.ponderHits, b.ponderTotal, b.ponderHitsAll, b.ponderTotAll)
	b.logf("PONDER %s game=%d predicted=%s actual=%s game_hits=%d/%d all_hits=%d/%d",
		label, b.gameNo, p, s, b.ponderHits, b.ponderTotal, b.ponderHitsAll, b.ponderTotAll)
}

// ponder runs the pondering search over the current position (root+M+P, the
// one we'd face if the opponent plays the predicted reply). Its only job is to
// warm the carried TT (b.aux) for that position so the follow-up real search
// on a ponder hit gets a head start; it does not touch game state and its
// bestmove is merely held for the UCI "stop" reply. Not book-probed and not
// dithered — a ponder result is never committed.
func (b *Bridge) ponder() error {
	pos, err := chesstest.ParseFEN(b.pos.FEN())
	if err != nil {
		return err
	}
	budget := b.ponderBudgetCycles(b.ponderArgs)
	res, err := b.runEngine(pos, byte(min(b.pos.HalfmoveClock(), 255)), budget, maxDepthCap, -1, nil)
	if err != nil {
		return err
	}
	if res.code == 0 && res.to != 0xFF {
		b.ponderBest = chesstest.MoveUCI(res.from, res.to, res.flags)
	} else {
		b.ponderBest = ""
	}
	b.info = fmt.Sprintf("info string pondering depth %d nodes %d", res.depth, res.nodes)
	// Time-budget audit: ponder cycles are OPPONENT-time (spent during Sargon's
	// turn on the predicted position), not part of our own-move budget — record
	// them SEPARATELY so the two are never conflated in the adherence numbers.
	if b.Log != nil {
		b.ponderCyc += res.nodes
		b.logf("TIME-PONDER game=%d spent=%d", b.gameNo, res.nodes)
	}
	return nil
}

// ponderBudgetCycles is the emulated cycle budget for a ponder search:
// PonderBudgetMs if pinned, else derived from the go args like a real search.
func (b *Bridge) ponderBudgetCycles(args []string) uint64 {
	if b.PonderBudgetMs != 0 {
		return b.PonderBudgetMs * cyclesPerMs
	}
	return b.budgetCycles(args)
}

// ttBase is the aux-bank byte offset of the transposition table (asm TTBASE,
// $0200). Entries are 8 bytes; see PonderMove for the entry layout.
const ttBase = 0x0200

// ponderProbeMs is the emulated-time budget of the source-(b) fallback search
// used to predict the opponent's reply when the TT slot is empty/mismatched.
const ponderProbeMs = 300

// PonderMove reads the engine's own predicted reply for the CURRENT position
// (typically root+M, right after our real move) straight out of the carried
// transposition table — source (a), the free PV-2nd-move continuation. It
// reproduces the asm TTADDR indexing exactly: compute the position's 32-bit
// Zobrist key (== asm HASH0-3, via book.HashFEN — proven equal to the on-
// device hash by TestBookKeyMatchesASM), form index = (HASH1&0x0F)<<8 | HASH0,
// read the 8-byte entry at ttBase+index*8 (+0..2 verify = HASH1..3, +3 from,
// +4 to, +5..6 score, +7 depth<<2|bound), verify the stored key and a non-zero
// bound, and return the stored from/to as a legal UCI move. ok=false when the
// slot is empty, keyed to a different position, holds NOSQ, or the move is not
// legal here (caller should fall back to a shallow search — source (b)).
func (b *Bridge) PonderMove() (string, bool) {
	if b.aux == nil {
		return "", false
	}
	key, err := book.HashFEN(b.pos.FEN())
	if err != nil {
		return "", false
	}
	h0, h1, h2, h3 := byte(key), byte(key>>8), byte(key>>16), byte(key>>24)
	index := int(h0) | int(h1&0x0F)<<8
	addr := ttBase + index*8
	if addr+8 > len(b.aux) {
		return "", false
	}
	e := b.aux[addr : addr+8]
	if e[7]&0x03 == 0 { // bound 0: empty entry
		return "", false
	}
	if e[0] != h1 || e[1] != h2 || e[2] != h3 { // key mismatch (collision)
		return "", false
	}
	if e[4] == 0xFF { // NOSQ: no move stored
		return "", false
	}
	return b.legalFromTo(e[3], e[4])
}

// legalFromTo turns a TT from/to pair (engine 0x88 squares; the TT stores no
// promotion piece) into a legal UCI move in the current position, matching on
// from+to. ok=false if no legal move matches (a stale/colliding entry).
func (b *Bridge) legalFromTo(from88, to88 byte) (string, bool) {
	sq := func(s byte) byte { return (s>>4)*8 + (s & 0x0F) } // 0x88 -> 0..63
	from, to := sq(from88), sq(to88)
	for _, mv := range b.pos.LegalMoves() {
		if mv.From == from && mv.To == to {
			return mv.String(), true
		}
	}
	return "", false
}

// ponderPrediction picks the move to ponder on for the current position: the
// free TT PV-2nd-move (source a) when present and legal, else a short shallow
// search (source b). ok=false only when there is genuinely no legal move
// (checkmate/stalemate). Source (b) runs through runEngine so it too warms the
// carried TT — a harmless bonus (a TT is never wrong).
func (b *Bridge) ponderPrediction() (string, bool, error) {
	if p, ok := b.PonderMove(); ok {
		return p, true, nil // source (a): free PV continuation
	}
	pos, err := chesstest.ParseFEN(b.pos.FEN())
	if err != nil {
		return "", false, err
	}
	res, err := b.runEngine(pos, byte(min(b.pos.HalfmoveClock(), 255)), ponderProbeMs*cyclesPerMs, maxDepthCap, -1, nil)
	if err != nil {
		return "", false, err
	}
	if res.code == 2 || res.to == 0xFF {
		return "", false, nil // no legal move
	}
	if res.code != 0 {
		return "", false, fmt.Errorf("ponder-probe exit code %d", res.code)
	}
	return chesstest.MoveUCI(res.from, res.to, res.flags), true, nil
}

// hasToken reports whether tok appears among args.
func hasToken(args []string, tok string) bool {
	for _, a := range args {
		if a == tok {
			return true
		}
	}
	return false
}

// stripToken returns args with every occurrence of tok removed.
func stripToken(args []string, tok string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != tok {
			out = append(out, a)
		}
	}
	return out
}

func goDepth(args []string) (byte, bool) {
	for i, a := range args {
		if a == "depth" && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
				return byte(min(v, maxDepthCap)), true
			}
		}
	}
	return 0, false
}
