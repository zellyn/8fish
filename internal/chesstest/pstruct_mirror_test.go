package chesstest

import (
	"math/rand"
	"testing"

	"github.com/zellyn/8fish/internal/mirror"
	"github.com/zellyn/8fish/internal/refchess"
)

// TestPStructMirrorParity guards the mirror's DEFAULT pawn-structure
// weights against the shipped asm. The mirror is our node-budget
// feature-selection instrument, so its default eval must reproduce the
// asm's exactly: if mirror.DefaultWeights drifts from the asm's baked-in
// pawnterm immediates (eval.s) and passed-pawn table (cmd/gentables),
// every default-weights screen silently evaluates pawns unlike the engine
// it is meant to model. (That drift did happen once: the asm's passed/
// isolated/shield/open constants were retuned and DefaultWeights was left
// on the pre-tuning values, biasing every pstruct-on screen.)
//
// The check is direct: the mirror's PStruct term, as computed by a stock
// NewEngine() (which uses DefaultWeights), must equal the asm engine's
// PSTRUCT word over a corpus of random legal positions. TestPStructParity
// separately pins the asm's PSTRUCT to refPStruct, so this closes the
// asm <-> mirror loop.
func TestPStructMirrorParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: hundreds of engine runs")
	}
	bin := loadEngine(t)
	asmPStruct := func(pos *Position) int16 {
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetBudget(m, defs, 0, 1) // depth 1: static eval only, PSTRUCT settled
		if exited, code, err := m.Run(30_000_000_000); err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
		return int16(uint16(m.Mem.Main[defs["PSTRUCT"]]) |
			uint16(m.Mem.Main[defs["PSTRUCT"]+1])<<8)
	}

	rng := rand.New(rand.NewSource(0x9157))
	positions := 0
	for games := 0; games < 40 && positions < 600; games++ {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for ply := 0; ply < 60; ply++ {
			legal := ref.LegalMoves()
			if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
				break
			}
			if err := ref.Make(legal[rng.Intn(len(legal))]); err != nil {
				t.Fatal(err)
			}
			fen := ref.FEN()
			positions++

			cp, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			want := asmPStruct(cp)

			mp, err := mirror.ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			me := mirror.NewEngine() // stock config: DefaultWeights
			me.SetPosition(mp)
			if got := int16(me.Pos.PStruct); got != want {
				t.Fatalf("PSTRUCT mismatch at %q: asm=%d mirror(default)=%d", fen, want, got)
			}
		}
	}
	t.Logf("mirror default-weights PSTRUCT == asm over %d positions", positions)
}
