package chesstest

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/refchess"
)

// The pawn-structure evaluation cache (PSTRUCT + PDIRTY dirty flag,
// asm/eval.s + asm/board.s) keeps the pawn-structure/king-shield term
// incrementally valid: pawnterm recomputes it only when a pawn or king
// moved, and unmake restores it wholesale. These tests build a second
// engine binary with -D PTNOCACHE, which disables the cache entirely
// (make never recomputes; eval recomputes the term from scratch every
// call), and prove the cache is a pure cycle optimization: bit-identical
// search trees, only cycles differ.

// ptcacheFENs: a suite exercising every pawn-structure-changing move
// kind in the search (pawn pushes/captures, promotions incl. under-
// promotion, en passant), plus king-shield-relevant king moves.
var ptcacheFENs = []struct {
	name  string
	fen   string
	depth byte
}{
	{"startpos", refchess.StartFEN, 6},
	{"profile-1", "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 5},
	{"profile-2", "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", 5},
	{"ep-avail", "rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3", 6},
	{"promo-race", "8/2P2k2/8/8/8/6K1/5p2/8 w - - 0 1", 8},
	{"passed-endgame", "8/pp3ppp/8/2ppP3/3P4/2P5/PP4PP/8 w - - 0 1", 8},
	{"kingside-storm", "r1bq1rk1/pp3ppp/2n1pn2/2bp4/3P4/2N1PN2/PPQ1BPPP/R1B2RK1 w - - 0 1", 5},
	{"heavy-pawns", "2r3k1/1p2ppbp/p1qp1np1/8/2PNP3/1P2BP2/P3Q1PP/3R2K1 w - - 0 1", 5},
}

func buildNoCache(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("..", "..")
	if err := asmbuild.BuildVariant(root, "engine_noptcache", "PTNOCACHE"); err != nil {
		if err == asmbuild.ErrCA65NotInstalled {
			t.Skip("ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine_noptcache.bin"))
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// runFixed runs a fixed-depth search (budget 0) and returns move, score,
// and total emulated cycles.
func runFixed(t *testing.T, bin []byte, pos *Position, depth byte) (string, int16, uint64) {
	t.Helper()
	var cout bytes.Buffer
	m, err := NewMachine(bin, defs, pos, 0, &cout)
	if err != nil {
		t.Fatal(err)
	}
	SetBudget(m, defs, 0, depth) // budget 0 = fixed depth
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	exited, code, err := m.Run(400_000_000_000)
	if err != nil || !exited {
		t.Fatalf("run failed: exited=%v err=%v", exited, err)
	}
	score := int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
	move := ""
	if code == 0 {
		move = strings.TrimSpace(cout.String())
	}
	return move, score, m.Cycles
}

// countNodes runs a fixed-depth search under the profiler and counts
// executions of the make/eval/unmake entry points (identical counts
// across two builds proves an identical search tree).
func countNodes(t *testing.T, bin []byte, lblFile string, pos *Position, depth byte) (make, eval, unmake uint64) {
	t.Helper()
	labels, err := ParseLabelFile(filepath.Join("..", "..", "asm", lblFile))
	if err != nil {
		t.Fatal(err)
	}
	mk, ev, um := labels["make"], labels["eval"], labels["unmake"]
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetBudget(m, defs, 0, depth)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	exited, _, err := m.RunProfile(400_000_000_000, func(pc uint16, _ uint8) {
		switch pc {
		case mk:
			make++
		case ev:
			eval++
		case um:
			unmake++
		}
	})
	if err != nil || !exited {
		t.Fatalf("profiled run failed: exited=%v err=%v", exited, err)
	}
	return make, eval, unmake
}

// TestPTCacheIdenticalTree: the cache must produce bit-identical search
// trees. For every FEN, cache-on and cache-off must return the same best
// move and score and execute make/eval/unmake the same number of times.
func TestPTCacheIdenticalTree(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: builds a second engine binary and profiles searches")
	}
	on := loadEngine(t)
	off := buildNoCache(t)

	var totOn, totOff uint64
	for _, tc := range ptcacheFENs {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		mvOn, scOn, cyOn := runFixed(t, on, pos, tc.depth)
		mvOff, scOff, cyOff := runFixed(t, off, pos, tc.depth)
		if mvOn != mvOff || scOn != scOff {
			t.Errorf("%s: cache-on (move=%q score=%d) != cache-off (move=%q score=%d)",
				tc.name, mvOn, scOn, mvOff, scOff)
			continue
		}
		mkOn, evOn, umOn := countNodes(t, on, "engine.lbl", pos, tc.depth)
		mkOff, evOff, umOff := countNodes(t, off, "engine_noptcache.lbl", pos, tc.depth)
		if mkOn != mkOff || evOn != evOff || umOn != umOff {
			t.Errorf("%s: node counts differ: on(make=%d eval=%d unmake=%d) off(make=%d eval=%d unmake=%d)",
				tc.name, mkOn, evOn, umOn, mkOff, evOff, umOff)
			continue
		}
		totOn += cyOn
		totOff += cyOff
		saved := int64(cyOff) - int64(cyOn)
		t.Logf("%-16s d%d  move=%-5s score=%-6d  makes=%d evals=%d  cyc: cache=%dM nocache=%dM  saved=%.1f%%",
			tc.name, tc.depth, mvOn, scOn, mkOn, evOn, cyOn/1e6, cyOff/1e6,
			100*float64(saved)/float64(cyOff))
	}
	if totOff > 0 {
		t.Logf("SUITE TOTAL: cache=%dM  nocache=%dM  saved=%dM (%.1f%%)",
			totOn/1e6, totOff/1e6, (totOff-totOn)/1e6,
			100*float64(int64(totOff)-int64(totOn))/float64(totOff))
	}
}

// TestPTCacheRandomWalk: over thousands of positions from random legal
// games — including promotions, under-promotions, and en passant — the
// cache-on and cache-off engines must return identical depth-2 scores
// (which fold in PSTRUCT). This exercises the incremental dirty-flag
// maintenance against a fresh recompute across make/unmake in the search.
func TestPTCacheRandomWalk(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: thousands of paired engine runs")
	}
	on := loadEngine(t)
	off := buildNoCache(t)

	score := func(bin []byte, pos *Position) int16 {
		var cout bytes.Buffer
		m, err := NewMachine(bin, defs, pos, 0, &cout)
		if err != nil {
			t.Fatal(err)
		}
		SetBudget(m, defs, 0, 2)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		exited, code, err := m.Run(30_000_000_000)
		if err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
		return int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
	}

	rng := rand.New(rand.NewSource(99001))
	positions := 0
	for games := 0; games < 150 && positions < 4000; games++ {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for ply := 0; ply < 70; ply++ {
			legal := ref.LegalMoves()
			if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
				break
			}
			mv := legal[rng.Intn(len(legal))]
			if err := ref.Make(mv); err != nil {
				t.Fatal(err)
			}
			fen := ref.FEN()
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			positions++
			if scOn, scOff := score(on, pos), score(off, pos); scOn != scOff {
				t.Fatalf("score mismatch at %q: cache=%d nocache=%d", fen, scOn, scOff)
			}
		}
	}
	t.Logf("pawn-cache random walk: %d positions, all cache/no-cache scores identical", positions)
}
