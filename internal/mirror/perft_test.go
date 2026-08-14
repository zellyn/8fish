package mirror

import (
	"math/rand/v2"
	"testing"

	"github.com/zellyn/8fish/internal/refchess"
)

// The same six positions internal/chesstest validates the asm engine
// against (chessprogramming.org/Perft_Results).
var perftCases = []struct {
	name   string
	fen    string
	counts []uint64
}{
	{
		"startpos",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		[]uint64{20, 400, 8902, 197281},
	},
	{
		"kiwipete",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		[]uint64{48, 2039, 97862},
	},
	{
		"pos3-ep",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		[]uint64{14, 191, 2812, 43238},
	},
	{
		"pos4-promo",
		"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1",
		[]uint64{6, 264, 9467},
	},
	{
		"pos5",
		"rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8",
		[]uint64{44, 1486, 62379},
	},
	{
		"pos6",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		[]uint64{46, 2079, 89890},
	},
}

func TestPerft(t *testing.T) {
	for _, tc := range perftCases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for d, want := range tc.counts {
			if got := Perft(pos, d+1); got != want {
				t.Errorf("%s perft(%d) = %d, want %d", tc.name, d+1, got, want)
			}
		}
	}
}

// TestPerftVsRefchess random-walks games, cross-checking perft(2) and
// the legal move sets against the independent referee at every step.
func TestPerftVsRefchess(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	for game := range 20 {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		pos, err := ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		eng := NewEngine()
		for ply := range 60 {
			refMoves := ref.LegalMoves()
			mirMoves := legalMoves(eng, pos)
			refSet := map[string]bool{}
			for _, m := range refMoves {
				refSet[m.String()] = true
			}
			if len(refMoves) != len(mirMoves) {
				t.Fatalf("game %d ply %d (%s): refchess %d legal moves, mirror %d",
					game, ply, pos.FEN(), len(refMoves), len(mirMoves))
			}
			for _, m := range mirMoves {
				if !refSet[m.UCI()] {
					t.Fatalf("game %d ply %d (%s): mirror move %s not legal per refchess",
						game, ply, pos.FEN(), m.UCI())
				}
			}
			if len(refMoves) == 0 {
				break
			}
			if want, got := refPerft(ref, 2), Perft(pos, 2); want != got {
				t.Fatalf("game %d ply %d (%s): perft(2) mirror %d, refchess %d",
					game, ply, pos.FEN(), got, want)
			}
			mv := refMoves[rnd.IntN(len(refMoves))]
			if err := ref.Make(mv); err != nil {
				t.Fatal(err)
			}
			if err := applyUCI(eng, pos, mv.String()); err != nil {
				t.Fatalf("game %d ply %d: %v", game, ply, err)
			}
		}
	}
}

func refPerft(p *refchess.Position, depth int) uint64 {
	return p.Perft(depth)
}
