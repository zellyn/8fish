// Package chesstest is the engine-embedding layer: it parses asm/defs.inc
// for the memory-layout contract, encodes FEN positions into the engine's
// in-memory representation, and drives the harness machine through perft
// runs and searches. Despite the name, it is not test-only — it is the
// shared foundation both the *_test.go files in this repo and the
// production internal packages (ucibridge, behind cmd/uci; sprt, behind
// cmd/sprt) build on to load the engine binary and talk to it.
package chesstest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/zellyn/chess6502/harness"
)

// CyclesPerMs is the engine's effective clock rate in cycles per emulated
// millisecond: harness.EffectiveHz (1.0205 MHz, rounded down to 1020484)
// divided by 1000. Used to convert wall/UCI time budgets into engine
// cycle budgets.
const CyclesPerMs = 1020

// Defs holds symbol values parsed from asm/defs.inc ("NAME = $HEX" lines).
type Defs map[string]uint16

func ParseDefs(path string) (Defs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defs := Defs{}
	for line := range strings.SplitSeq(string(data), "\n") {
		if i := strings.Index(line, ";"); i >= 0 {
			line = line[:i]
		}
		name, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "$") {
			continue
		}
		val, err := strconv.ParseUint(rest[1:], 16, 16)
		if err != nil {
			continue
		}
		defs[strings.TrimSpace(name)] = uint16(val)
	}
	return defs, nil
}

// Position is a chess position in the engine's encoding.
type Position struct {
	Board    [128]byte // 0x88; piece byte = index<<4 | color<<3 | type
	PieceSq  [32]byte  // slots 0-15 white (0=king), 16-31 black; $FF empty
	Side     byte      // 0 white, 8 black
	EpSq     byte      // 0x88 square or $FF
	Castle   byte      // 1=WK 2=WQ 4=BK 8=BQ
	Halfmove byte      // 50-move-rule clock
}

var pieceTypes = map[byte]byte{'p': 1, 'n': 2, 'b': 3, 'r': 4, 'q': 5, 'k': 6}

// ParseFEN encodes a FEN string into the engine's representation,
// assigning kings to slot 0 of each side as the engine requires.
func ParseFEN(fen string) (*Position, error) {
	fields := strings.Fields(fen)
	if len(fields) < 4 {
		return nil, fmt.Errorf("FEN needs at least 4 fields: %q", fen)
	}
	pos := &Position{EpSq: 0xFF}
	for i := range pos.PieceSq {
		pos.PieceSq[i] = 0xFF
	}

	next := [2]byte{1, 1} // next free index per color (0 reserved for king)
	rank := 7
	file := 0
	for _, c := range fields[0] {
		switch {
		case c == '/':
			rank--
			file = 0
		case c >= '1' && c <= '8':
			file += int(c - '0')
		default:
			typ, ok := pieceTypes[byte(c|0x20)]
			if !ok {
				return nil, fmt.Errorf("bad FEN piece %q", c)
			}
			var color byte // 0 white, 1 black
			if c >= 'a' {  // lowercase = black
				color = 1
			}
			var index byte
			if typ == 6 {
				index = 0
			} else {
				index = next[color]
				next[color]++
				if index > 15 {
					return nil, fmt.Errorf("more than 16 pieces for one side")
				}
			}
			sq := byte(rank*16 + file)
			pos.Board[sq] = index<<4 | color<<3 | typ
			pos.PieceSq[index+16*color] = sq
			file++
		}
	}

	switch fields[1] {
	case "w":
		pos.Side = 0
	case "b":
		pos.Side = 8
	default:
		return nil, fmt.Errorf("bad side %q", fields[1])
	}

	if fields[2] != "-" {
		for _, c := range fields[2] {
			switch c {
			case 'K':
				pos.Castle |= 1
			case 'Q':
				pos.Castle |= 2
			case 'k':
				pos.Castle |= 4
			case 'q':
				pos.Castle |= 8
			}
		}
	}

	if fields[3] != "-" {
		if len(fields[3]) != 2 {
			return nil, fmt.Errorf("bad ep square %q", fields[3])
		}
		pos.EpSq = byte(fields[3][1]-'1')<<4 | (fields[3][0] - 'a')
	}
	if len(fields) > 4 {
		hm, err := strconv.Atoi(fields[4])
		if err == nil && hm >= 0 && hm < 256 {
			pos.Halfmove = byte(hm)
		}
	}
	return pos, nil
}

// NewMachine builds a harness machine with the engine binary loaded and
// the position poked into memory, ready to run.
func NewMachine(bin []byte, defs Defs, pos *Position, depth byte, cout io.Writer) (*harness.Machine, error) {
	m, err := harness.New(harness.Config{
		Bin:          bin,
		Org:          0x4000,
		Entry:        0x4000,
		CoutAddr:     0xBFF0,
		ExitAddr:     0xBFFF,
		InAddr:       0xBFF1,
		InStatusAddr: 0xBFF2,
		ClockAddr:    0xBFF4,
		Cout:         cout,
	})
	if err != nil {
		return nil, err
	}
	board := defs["BOARD"]
	// Write only the 64 real squares. The 0x88 board's 64 off-board bytes
	// (files 8-F of each rank) are deliberately left untouched so they stay
	// available as engine scratch - see the off-board dead-space contract at
	// BOARD in defs.inc. The engine never reads off-board, so this is exact.
	for rank := uint16(0); rank < 8; rank++ {
		base := rank * 16
		copy(m.Mem.Main[board+base:board+base+8], pos.Board[base:base+8])
	}
	psq := defs["PIECESQ"]
	copy(m.Mem.Main[psq:psq+32], pos.PieceSq[:])
	m.Mem.Main[defs["SIDE"]] = pos.Side
	m.Mem.Main[defs["EPSQ"]] = pos.EpSq
	m.Mem.Main[defs["CASTLE"]] = pos.Castle
	m.Mem.Main[defs["ROOTDEPTH"]] = depth
	m.Mem.Main[defs["FEATURES"]] = 0x1F  // all search+eval features on
	m.Mem.Main[defs["FEATURES2"]] = 0x00 // second feature byte off by default
	return m, nil
}

// SetFeatures overrides the engine's search-feature bits (FT_* in
// defs.inc) — the A/B lever for feature gating.
func SetFeatures(m *harness.Machine, defs Defs, bits byte) {
	m.Mem.Main[defs["FEATURES"]] = bits
}

// SetFeatures2 overrides the engine's second feature byte (FT2_* in
// defs.inc, e.g. FT2_IMPROV) — the A/B lever for features that outgrew
// the original 8-bit FEATURES byte.
func SetFeatures2(m *harness.Machine, defs Defs, bits byte) {
	m.Mem.Main[defs["FEATURES2"]] = bits
}

// SetBudget pokes the engine's soft time budget (in cycles, converted
// to its 24-bit 256-cycle units, rounding up so a tiny nonzero budget
// never degrades to fixed-depth mode) and the depth cap.
func SetBudget(m *harness.Machine, defs Defs, budgetCycles uint64, maxDepth byte) {
	b := (budgetCycles + 255) >> 8
	if b > 0xFFFFFF {
		b = 0xFFFFFF
	}
	m.Mem.Main[defs["BUDGET0"]] = byte(b)
	m.Mem.Main[defs["BUDGET1"]] = byte(b >> 8)
	m.Mem.Main[defs["BUDGET2"]] = byte(b >> 16)
	m.Mem.Main[defs["MAXDEPTH"]] = maxDepth
}

// poke24 writes a value (in cycles, converted to the engine's 24-bit
// 256-cycle units, rounding up) to a 3-byte little-endian MAIN triple whose
// low byte is at defs[sym].
func poke24(m *harness.Machine, defs Defs, sym string, cycles uint64) {
	v := (cycles + 255) >> 8
	if v > 0xFFFFFF {
		v = 0xFFFFFF
	}
	a := defs[sym]
	m.Mem.Main[a] = byte(v)
	m.Mem.Main[a+1] = byte(v >> 8)
	m.Mem.Main[a+2] = byte(v >> 16)
}

// SetAdaptive pokes the FT2_ADAPT (adaptive time/effort) engine params and
// turns the feature bit on. BUDGET (the movable ceiling's start = the base
// ceiling) must already be poked by SetBudget. All three ceilings are in
// CYCLES (converted to 256-cycle units on device, matching BUDGET):
//
//	ceilMax  = panic target / hard max ceiling = min(4*base, income+bank)
//	unstCeil = instability target             = min(3*base, ceilMax)
//	minSpend = easy-stop minimum spend        = base/4
//
// The host (which runs the per-game bank) computes these; on device the engine
// only compares and raises the movable ceiling. Leaves FEATURES2's other bits
// untouched (OR in FT2_ADAPT). ftAdapt must be the FT2_ADAPT defs value.
func SetAdaptive(m *harness.Machine, defs Defs, ceilMax, unstCeil, minSpend uint64) {
	poke24(m, defs, "CEILMAX0", ceilMax)
	poke24(m, defs, "UNSTCEIL0", unstCeil)
	poke24(m, defs, "MINSPEND0", minSpend)
	m.Mem.Main[defs["FEATURES2"]] |= byte(defs["FT2_ADAPT"])
}

// SearchResult is the outcome of running the engine binary once.
type SearchResult struct {
	Move   string // UCI ("e2e4", "e7e8q"), or "" if no legal move
	Score  int16  // from the side to move's POV
	Cycles uint64
}

// SearchMove runs the engine binary over the position at fixed depth
// MAXDEPTH and returns the chosen move and score.
func SearchMove(bin []byte, defs Defs, pos *Position, depth byte, maxCycles uint64) (*SearchResult, error) {
	return SearchBudget(bin, defs, pos, depth, 0, maxCycles)
}

// SearchBudget runs the engine with iterative deepening under a cycle
// budget (in cycles; converted to the engine's 256-cycle units), capped
// at depth maxDepth. budget 0 = fixed-depth mode.
func SearchBudget(bin []byte, defs Defs, pos *Position, maxDepth byte, budget uint64, maxCycles uint64) (*SearchResult, error) {
	var cout bytes.Buffer
	m, err := NewMachine(bin, defs, pos, 0, &cout)
	if err != nil {
		return nil, err
	}
	SetBudget(m, defs, budget, maxDepth)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove

	exited, code, err := m.Run(maxCycles)
	if err != nil {
		return nil, err
	}
	if !exited {
		return nil, fmt.Errorf("cycle limit (%d) reached", maxCycles)
	}
	res := &SearchResult{
		Score: int16(uint16(m.Mem.Main[defs["SCORE"]]) |
			uint16(m.Mem.Main[defs["SCORE"]+1])<<8),
		Cycles: m.Cycles,
	}
	switch code {
	case 0:
		out := strings.TrimSpace(cout.String())
		if len(out) < 4 || len(out) > 5 {
			return nil, fmt.Errorf("engine printed %q", out)
		}
		res.Move = out
	case 2: // no legal move: mate or stalemate
	default:
		return nil, fmt.Errorf("engine exited with code %d (cout %q)", code, cout.String())
	}
	return res, nil
}

// Perft runs the perft binary over the position at the given depth and
// returns the leaf count, both as read from the PCOUNT memory location
// and as printed via COUT (they must agree).
func Perft(bin []byte, defs Defs, pos *Position, depth byte, maxCycles uint64) (uint32, uint64, error) {
	var cout bytes.Buffer
	m, err := NewMachine(bin, defs, pos, depth, &cout)
	if err != nil {
		return 0, 0, err
	}
	exited, code, err := m.Run(maxCycles)
	if err != nil {
		return 0, m.Cycles, err
	}
	if !exited {
		return 0, m.Cycles, fmt.Errorf("cycle limit (%d) reached", maxCycles)
	}
	if code != 0 {
		return 0, m.Cycles, fmt.Errorf("engine exited with code %d (cout %q)", code, cout.String())
	}

	pc := defs["PCOUNT"]
	count := uint32(m.Mem.Main[pc]) | uint32(m.Mem.Main[pc+1])<<8 |
		uint32(m.Mem.Main[pc+2])<<16 | uint32(m.Mem.Main[pc+3])<<24
	printed := strings.TrimSpace(cout.String())
	if want := fmt.Sprintf("%08X", count); printed != want {
		return count, m.Cycles, fmt.Errorf("COUT printed %q, memory says %s", printed, want)
	}
	return count, m.Cycles, nil
}

// SqName renders a 0x88 square as algebraic notation, e.g. "e4".
func SqName(sq byte) string {
	return fmt.Sprintf("%c%c", 'a'+sq&0x0F, '1'+sq>>4)
}

// MoveUCI renders a 0x88 from/to pair plus engine move flags (bits 0-2 =
// FL_PROMO in asm/defs.inc) as a UCI move string, e.g. "e2e4" or "e7e8q".
func MoveUCI(from, to, flags byte) string {
	move := SqName(from) + SqName(to)
	if p := flags & 0x07; p != 0 {
		move += string("..nbrq"[p])
	}
	return move
}
