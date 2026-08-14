package sprt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/8fish/internal/asmbuild"
	"github.com/zellyn/8fish/internal/chesstest"
	"github.com/zellyn/8fish/internal/refchess"
)

// playDiagGame: one carryover game, 0x1F (A, White) vs 0x0F (B),
// logging realized depth, abort state, cycles, and (when banked) the
// clock bank per move — the realized-depth diagnostic behind the
// time-management campaign. Self-play SPRT hides absolute regressions
// that hit both sides equally; this makes them visible.
func playDiagGame(t *testing.T, banked bool) {
	asmbuild.BuildT(t, "../..")
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("..", "..", "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	const budget = 30_612_000 // 30s emulated
	auxes := map[bool][]byte{}
	clocks := map[bool]*chesstest.BankedClock{
		true:  {Base: budget},
		false: {Base: budget},
	}
	depths := map[byte]int{}
	for ply := 0; ply < 60; ply++ {
		legal := ref.LegalMoves()
		if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
			break
		}
		aTurn := ref.SideToMove() == 0
		features := byte(0x1F)
		if !aTurn {
			features = 0x0F
		}
		pos, err := chesstest.ParseFEN(ref.FEN())
		if err != nil {
			t.Fatal(err)
		}
		m, err := chesstest.NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		chesstest.SetFeatures(m, defs, features)
		alloc := uint64(budget)
		if banked {
			alloc = clocks[aTurn].Alloc()
		}
		chesstest.SetBudget(m, defs, alloc, 24)
		m.Mem.Main[defs["HALFMOVE"]] = byte(min(ref.HalfmoveClock(), 255))
		m.Mem.Main[defs["SEED"]] = byte(ply*37 + 1) // dither like the match
		if aux := auxes[aTurn]; aux != nil {
			copy(m.Mem.Aux[:], aux)
		}
		exited, code, err := m.Run(alloc*30 + 2_000_000_000)
		if err != nil || !exited || code > 2 {
			t.Fatalf("ply %d: exited=%v code=%d err=%v", ply, exited, code, err)
		}
		if auxes[aTurn] == nil {
			auxes[aTurn] = make([]byte, len(m.Mem.Aux))
		}
		copy(auxes[aTurn], m.Mem.Aux[:])
		if banked {
			clocks[aTurn].Settle(m.Cycles)
		}
		if code == 2 {
			break
		}
		ms := chesstest.MoveUCI(m.Mem.Main[defs["BESTFROM"]],
			m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
		score := int16(uint16(m.Mem.Main[defs["SCORE"]]) |
			uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
		d := m.Mem.Main[defs["CURDEPTH"]]
		depths[d]++
		t.Logf("ply %2d %s(%#02x): %-6s depth=%d abort=%d score=%6d cycles=%3dM alloc=%3dM bank=%3dM",
			ply, map[bool]string{true: "A", false: "B"}[aTurn], features, ms,
			d, m.Mem.Main[defs["ABORT"]], score, m.Cycles/1_000_000,
			alloc/1_000_000, clocks[aTurn].Bank()/1_000_000)
		mv, err := refchess.ParseMove(ms)
		if err != nil {
			t.Fatal(err)
		}
		if err := ref.Make(mv); err != nil {
			t.Fatalf("ply %d: illegal %q: %v", ply, ms, err)
		}
	}
	t.Logf("banked=%v depth distribution: %v", banked, depths)
}

func TestDebugDepthGame(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	playDiagGame(t, false)
}

func TestDebugDepthGameBanked(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	playDiagGame(t, true)
}
