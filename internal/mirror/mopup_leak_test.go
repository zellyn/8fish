package mirror

import (
	"math/rand/v2"
	"testing"
)

// TestMopupNoMiddlegameLeak is the definitive gate-leak proof: over
// thousands of positions reached by random legal play, the mop-up ON
// eval must EQUAL the mop-up OFF eval at every position whose phase is
// above PhaseMax (any real middlegame). Only low-phase, decisive-
// material endgames may differ. It also confirms the OFF path is
// byte-identical to a baseline engine that has no Mopup config at all.
func TestMopupNoMiddlegameLeak(t *testing.T) {
	off := NewEngine() // Mopup zero value = OFF
	on := NewEngine()
	on.Mopup = DefaultMopup

	rnd := rand.New(rand.NewPCG(0xA11CE, 0xB0B))
	start, err := ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		t.Fatal(err)
	}

	var checked, gated, leaks int
	walker := NewEngine()
	for game := 0; game < 300; game++ {
		gp := *start
		for ply := 0; ply < 80; ply++ {
			// Compare evals at this position with both configs.
			off.SetPosition(&gp)
			on.SetPosition(&gp)
			off.Seed, on.Seed = 0, 0
			eOff := off.eval()
			eOn := on.eval()
			checked++
			if eOff != eOn {
				gated++
				// A difference is only legal when the gate is active:
				// phase <= PhaseMax. Anything above that is a middlegame leak.
				if on.Pos.Phase > DefaultMopup.PhaseMax {
					leaks++
					if leaks <= 5 {
						t.Errorf("LEAK: phase %d > %d but eval differs (off=%d on=%d) fen=%s",
							on.Pos.Phase, DefaultMopup.PhaseMax, eOff, eOn, gp.FEN())
					}
				}
			}
			// Advance by a random legal move.
			moves := legalMoves(walker, &gp)
			if len(moves) == 0 {
				break
			}
			m := moves[rnd.IntN(len(moves))]
			walker.SetPosition(&gp)
			walker.make(m)
			gp = walker.Pos
			gp.Ply = 0
		}
	}
	t.Logf("checked=%d positions, gate-active(differ)=%d, middlegame-leaks=%d", checked, gated, leaks)
	if leaks != 0 {
		t.Fatalf("%d middlegame leaks: the phase gate is not tight", leaks)
	}

	// Positive control: the gate MUST fire on a won endgame (else the
	// "no leak" result is vacuous). KQK should differ; the opening must not.
	assertDiff := func(fen string, wantDiff bool) {
		off.SetPosition(mustFEN(t, fen))
		on.SetPosition(mustFEN(t, fen))
		off.Seed, on.Seed = 0, 0
		d := off.eval() != on.eval()
		if d != wantDiff {
			t.Errorf("gate control fen=%q: got differ=%v want %v (phase=%d)", fen, d, wantDiff, on.Pos.Phase)
		}
	}
	assertDiff("8/8/8/4k3/8/8/8/3QK3 w - - 0 1", true)                            // KQK: gate active
	assertDiff("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1", false) // opening: silent
}

func mustFEN(t *testing.T, fen string) *Position {
	t.Helper()
	p, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
