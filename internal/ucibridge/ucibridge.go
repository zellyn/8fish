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

	bookRnd func() uint32
	// CurOpening is the name ID (+1) of the opening the last book move
	// belonged to; 0 means "not in book / out of book". CurOpeningName is
	// its text ("ECO Name"), surfaced in the info log ("in book: ...").
	CurOpening     byte
	CurOpeningName string
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
			say("uciok")
		case "isready":
			say("readyok")
		case "ucinewgame":
			b.aux = nil // clear the TT
			b.pos, _ = refchess.ParseFEN(refchess.StartFEN)
			b.bookRnd = nil // restart the deterministic book-choice stream
			b.CurOpening, b.CurOpeningName = 0, ""
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
			move, err := b.think(fields[1:])
			if err != nil {
				say("info string engine error: %v", err)
				say("bestmove 0000")
				continue
			}
			if b.info != "" {
				say("%s", b.info)
			}
			say("bestmove %s", move)
		case "quit":
			return nil
		}
	}
	return sc.Err()
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
	if i < len(args) && args[i] == "moves" {
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
		seed := b.BookSeed
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
	return move, true, nil
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
	m, err := chesstest.NewMachine(b.Bin, b.Defs, pos, 0, io.Discard)
	if err != nil {
		return "", err
	}
	// Gameplay config: FEATURES stays the NewMachine 0x1F default.
	// FT2_IMPROV (improving-LMR) was adopted 2026-07-21 on a +13 ± 9 cycle
	// screen, then RETRACTED 2026-07-22: a 4200-game confirmation SPRT
	// landed at −1.8 ± 8.6 (no demonstrable benefit; screen's +13 outside
	// the CI). FEATURES2 now stays 0 in gameplay. The asm implementation
	// remains gated behind FT2_IMPROV for re-test; excising it (to reclaim
	// its ~0.19% feature-off gate-check tax) is a deferred search.s cleanup.
	depth := byte(maxDepthCap)
	budget := b.budgetCycles(args)
	if d, ok := goDepth(args); ok {
		depth, budget = d, 0 // fixed-depth mode
	}
	if b.Banked && budget != 0 {
		if b.clock == nil || b.clock.Base != budget {
			b.clock = &chesstest.BankedClock{Base: budget}
		}
		budget = b.clock.Alloc()
	}
	chesstest.SetBudget(m, b.Defs, budget, depth)
	m.Mem.Main[b.Defs["HALFMOVE"]] = byte(min(b.pos.HalfmoveClock(), 255))
	if b.Dither {
		if b.rnd == nil {
			r := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			b.rnd = func() byte { return byte(r.IntN(255) + 1) }
		}
		m.Mem.Main[b.Defs["SEED"]] = b.rnd()
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
		return "", err
	}
	if !exited {
		return "", fmt.Errorf("engine did not finish")
	}
	if b.aux == nil {
		b.aux = make([]byte, len(m.Mem.Aux))
	}
	copy(b.aux, m.Mem.Aux[:]) // carry the TT forward
	if b.clock != nil && budget != 0 {
		b.clock.Settle(m.Cycles)
	}

	if code == 2 {
		return "0000", nil // no legal move; cutechess shouldn't ask
	}
	if code != 0 {
		return "", fmt.Errorf("engine exit code %d", code)
	}
	from := m.Mem.Main[b.Defs["BESTFROM"]]
	to := m.Mem.Main[b.Defs["BESTTO"]]
	flags := m.Mem.Main[b.Defs["BESTFLAGS"]]
	score := int16(uint16(m.Mem.Main[b.Defs["SCORE"]]) |
		uint16(m.Mem.Main[b.Defs["SCORE"]+1])<<8)
	b.info = fmt.Sprintf("info depth %d score cp %d nodes %d",
		m.Mem.Main[b.Defs["CURDEPTH"]], score, m.Cycles)
	move := chesstest.MoveUCI(from, to, flags)
	mv, err := refchess.ParseMove(move)
	if err != nil {
		return "", fmt.Errorf("engine move %q: %w", move, err)
	}
	if err := b.pos.Make(mv); err != nil {
		return "", fmt.Errorf("engine played illegal %q: %w", move, err)
	}
	return move, nil
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
