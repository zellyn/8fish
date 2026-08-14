package chesstest

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/8fish/internal/asmbuild"
)

var (
	perftBin []byte
	defs     Defs
)

func TestMain(m *testing.M) {
	root, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}
	if err := asmbuild.Build(root); err != nil {
		if errors.Is(err, asmbuild.ErrCA65NotInstalled) {
			os.Stderr.WriteString("SKIP: ca65 not installed\n")
			os.Exit(0)
		}
		panic(err)
	}
	asm := filepath.Join(root, "asm")
	perftBin, err = os.ReadFile(filepath.Join(asm, "perft.bin"))
	if err != nil {
		panic(err)
	}
	defs, err = ParseDefs(filepath.Join(asm, "defs.inc"))
	if err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// Published perft values: chessprogramming.org/Perft_Results.
var perftCases = []struct {
	name   string
	fen    string
	counts []uint32 // counts[d-1] = perft(d)
}{
	{
		"startpos",
		"rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1",
		[]uint32{20, 400, 8902, 197281},
	},
	{
		"kiwipete",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
		[]uint32{48, 2039, 97862},
	},
	{
		"pos3-ep",
		"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
		[]uint32{14, 191, 2812, 43238},
	},
	{
		"pos4-promo",
		"r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1",
		[]uint32{6, 264, 9467},
	},
	{
		"pos5",
		"rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8",
		[]uint32{44, 1486, 62379},
	},
	{
		"pos6",
		"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
		[]uint32{46, 2079, 89890},
	},
}

func TestPerft(t *testing.T) {
	for _, tc := range perftCases {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for d, want := range tc.counts {
			depth := byte(d + 1)
			count, cycles, err := Perft(perftBin, defs, pos, depth, 4_000_000_000)
			if err != nil {
				t.Fatalf("%s depth %d: %v", tc.name, depth, err)
			}
			if count != want {
				t.Errorf("%s perft(%d) = %d, want %d", tc.name, depth, count, want)
			} else {
				t.Logf("%s perft(%d) = %d ok (%d cycles, %.0f cycles/leaf)",
					tc.name, depth, count, cycles, float64(cycles)/float64(count))
			}
		}
	}
}
