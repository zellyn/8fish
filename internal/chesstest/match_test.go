package chesstest

import (
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/zellyn/8fish/internal/refchess"
)

// playGame plays the engine (as white if engineWhite) against a random
// mover, refereeing every engine move with refchess. Returns the result
// from the engine's perspective: +1 win, 0 draw, -1 loss.
func playGame(t *testing.T, depth byte, engineWhite bool, seed int64) int {
	t.Helper()
	ref, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	rnd := rand.New(rand.NewSource(seed))
	seen := map[uint64]int{}
	for ply := 0; ply < 400; ply++ {
		if ref.HalfmoveClock() >= 100 || ref.InsufficientMaterial() {
			return 0
		}
		seen[ref.ZobristKey()]++
		if seen[ref.ZobristKey()] >= 3 {
			return 0
		}
		engineTurn := (ref.SideToMove() == 0) == engineWhite
		legal := ref.LegalMoves()
		if len(legal) == 0 {
			mated := ref.InCheck()
			switch {
			case !mated:
				return 0
			case engineTurn:
				return -1
			default:
				return 1
			}
		}
		var mv refchess.Move
		if engineTurn {
			pos, err := ParseFEN(ref.FEN())
			if err != nil {
				t.Fatalf("ply %d: %v", ply, err)
			}
			res, err := SearchMove(loadEngine(t), defs, pos, depth, 20_000_000_000)
			if err != nil {
				t.Fatalf("ply %d (fen %q): %v", ply, ref.FEN(), err)
			}
			if res.Move == "" {
				t.Fatalf("ply %d: engine says no move but referee finds %d (fen %q)",
					ply, len(legal), ref.FEN())
			}
			mv, err = refchess.ParseMove(res.Move)
			if err != nil {
				t.Fatalf("ply %d: engine emitted %q: %v", ply, res.Move, err)
			}
			ok := false
			for _, lm := range legal {
				if lm == mv {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("ILLEGAL MOVE %q at ply %d (fen %q)", res.Move, ply, ref.FEN())
			}
		} else {
			var ok bool
			mv, ok = refchess.RandomMove(ref, rnd)
			if !ok {
				t.Fatalf("random mover found no move at ply %d", ply)
			}
		}
		if err := ref.Make(mv); err != nil {
			t.Fatalf("ply %d: referee rejected %v: %v", ply, mv, err)
		}
	}
	return 0
}

// TestLegalityTorture plays refereed games engine-vs-random. Every engine
// move is validated by the independent refchess implementation. Game
// count via TORTURE_GAMES (default 10; the M2 gate run uses 100).
func TestLegalityTorture(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: refereed games")
	}
	games := 10
	if s := os.Getenv("TORTURE_GAMES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			games = n
		}
	}
	wins, draws, losses := 0, 0, 0
	for i := range games {
		r := playGame(t, 3, i%2 == 0, int64(1000+i))
		switch r {
		case 1:
			wins++
		case 0:
			draws++
		case -1:
			losses++
		}
	}
	t.Logf("engine d3 vs random: +%d =%d -%d over %d games", wins, draws, losses, games)
	if losses > 0 {
		t.Errorf("engine lost %d games to a random mover", losses)
	}
}
