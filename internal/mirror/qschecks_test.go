package mirror

import (
	"math/rand/v2"
	"testing"

	"github.com/zellyn/chess6502/internal/refchess"
)

// TestGivesCheckVsRefchess validates the exact "does this move give
// check" machinery the task #37 QS quiet-checks feature relies on
// (make() sets e.inChk[child] = curInCheck()) against the independent
// refchess referee, over random-walk games. For every legal move in
// every visited position, the mirror's post-make in-check flag must
// match refchess's InCheck() after the corresponding move.
func TestGivesCheckVsRefchess(t *testing.T) {
	rnd := rand.New(rand.NewPCG(37, 137))
	checks := 0
	for game := range 40 {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		pos, err := ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		eng := NewEngine()
		for ply := range 80 {
			eng.SetPosition(pos)
			// Map refchess legal moves by UCI so we can cross-check each.
			refCheck := map[string]bool{}
			for _, rm := range ref.LegalMoves() {
				rc := ref.Copy()
				if err := rc.Make(rm); err != nil {
					t.Fatalf("game %d ply %d: refchess make %s: %v", game, ply, rm.String(), err)
				}
				refCheck[rm.String()] = rc.InCheck()
			}
			// For each legal mirror move, compare its gives-check flag.
			for _, m := range eng.generate(false) {
				eng.make(m)
				king := eng.Pos.PieceSq[int(eng.Pos.Side^ColorMask)<<1]
				if eng.attacked(king, eng.Pos.Side) {
					eng.unmake() // illegal (left own king in check)
					continue
				}
				gives := eng.inChk[eng.Pos.Ply] // = curInCheck() for the child
				eng.unmake()
				uci := m.UCI()
				want, ok := refCheck[uci]
				if !ok {
					t.Fatalf("game %d ply %d (%s): mirror legal move %s not legal per refchess",
						game, ply, pos.FEN(), uci)
				}
				if gives != want {
					t.Fatalf("game %d ply %d (%s): move %s gives-check mirror=%v refchess=%v",
						game, ply, pos.FEN(), uci, gives, want)
				}
				if gives {
					checks++
				}
			}
			legal := legalMoves(eng, pos)
			if len(legal) == 0 {
				break
			}
			mv := legal[rnd.IntN(len(legal))]
			if err := applyUCI(eng, pos, mv.UCI()); err != nil {
				t.Fatalf("game %d ply %d: %v", game, ply, err)
			}
			if err := ref.Make(refMoveByUCI(t, ref, mv.UCI())); err != nil {
				t.Fatalf("game %d ply %d: refchess make: %v", game, ply, err)
			}
		}
	}
	t.Logf("cross-checked gives-check on random-walk games; %d checking moves seen", checks)
	if checks < 50 {
		t.Errorf("suspiciously few checking moves cross-checked (%d)", checks)
	}
}

// TestQSChecksNodeShape: depth-6 total/QS/QS-check node counts for the
// task #37 quiet-checks-in-QS variants, vs the shipped baseline (no
// quiet checks) and vs the recap2 QS shaping. The key numbers for the
// 6502 cost-benefit are the QS-node growth and the share of QS nodes
// that come from quiet checks.
func TestQSChecksNodeShape(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	variants := []struct {
		name string
		qs   QSParams
	}{
		{"base (caps only)", QSParams{}},
		{"checks1", QSParams{Checks: 1}},
		{"checks2", QSParams{Checks: 2}},
		{"checks1+safe", QSParams{Checks: 1, SafeChecks: true}},
		{"checks2+safe", QSParams{Checks: 2, SafeChecks: true}},
		{"recap2", QSParams{RecapAfter: 2}},
		{"recap2+checks1", QSParams{RecapAfter: 2, Checks: 1}},
		{"recap2+checks2", QSParams{RecapAfter: 2, Checks: 2}},
		{"recap2+checks1+safe", QSParams{RecapAfter: 2, Checks: 1, SafeChecks: true}},
	}
	fens := benchFENs(t)
	var baseTotal, baseQS uint64
	for vi, v := range variants {
		var total, qsn, qsc uint64
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			eng := NewEngine()
			eng.QS = v.qs
			eng.SetPosition(pos)
			eng.SearchFixed(6)
			total += eng.Nodes
			qsn += eng.QSNodes
			qsc += eng.QSCheckNodes
		}
		if vi == 0 {
			baseTotal, baseQS = total, qsn
		}
		t.Logf("%-20s total %9d (%+6.1f%%)  qs %9d (%+6.1f%%, %2.0f%% of nodes)  qschk %8d (%2.0f%% of qs)",
			v.name, total, 100*(float64(total)/float64(baseTotal)-1),
			qsn, 100*(float64(qsn)/float64(baseQS)-1), 100*float64(qsn)/float64(total),
			qsc, 100*float64(qsc)/float64(qsn))
	}
}

// TestQSChecksWAC is a secondary tactical sanity signal: the WAC subset
// solved at a SHALLOW fixed depth, where more of the tactic must be seen
// inside quiescence, comparing caps-only QS to the quiet-checks variants.
// A quiet-checks QS should not regress the solve count and may raise it.
func TestQSChecksWAC(t *testing.T) {
	cases := []struct{ name, fen, bm string }{
		{"WAC.001", "2rr3k/pp3pp1/1nnqbN1p/3pN3/2pP4/2P3Q1/PPB4P/R4RK1 w - - 0 1", "g3g6"},
		{"WAC.002", "8/7p/5k2/5p2/p1p2P2/Pr1pPK2/1P1R3P/8 b - - 0 1", "b3b2"},
		{"WAC.004", "r1bq2rk/pp3pbp/2p1p1pQ/7P/3P4/2PB1N2/PP3PPR/2KR4 w - - 0 1", "h6h7"},
		{"WAC.005", "5k2/6pp/p1qN4/1p1p4/3P4/2PKP2Q/PP3r2/3R4 b - - 0 1", "c6c4"},
		{"WAC.008", "r4q1k/p2bR1rp/2p2Q1N/5p2/5p2/2P5/PP3PPP/R5K1 w - - 0 1", "e7f7"},
		{"WAC.010", "2br2k1/2q3rn/p2NppQ1/2p1P3/Pp5R/4P3/1P3PPP/3R2K1 w - - 0 1", "h4h7"},
		{"WAC.012", "4k1r1/2p3r1/1pR1p3/3pP2p/3P2qP/P4N2/1PQ4P/5RK1 b - - 0 1", "g4f3"},
	}
	variants := []struct {
		name string
		qs   QSParams
	}{
		{"caps-only", QSParams{}},
		{"checks1", QSParams{Checks: 1}},
		{"checks2", QSParams{Checks: 2}},
	}
	const depth = 3
	for _, v := range variants {
		solved := 0
		for _, tc := range cases {
			pos, err := ParseFEN(tc.fen)
			if err != nil {
				t.Fatal(err)
			}
			eng := NewEngine()
			eng.QS = v.qs
			eng.SetPosition(pos)
			best, _ := eng.SearchFixed(depth)
			if best.UCI() == tc.bm {
				solved++
			}
		}
		t.Logf("depth %d, QS %-8s: %d/%d solved", depth, v.name, solved, len(cases))
	}
}

func refMoveByUCI(t *testing.T, p *refchess.Position, uci string) refchess.Move {
	t.Helper()
	for _, rm := range p.LegalMoves() {
		if rm.String() == uci {
			return rm
		}
	}
	t.Fatalf("no refchess move matching %s", uci)
	return refchess.Move{}
}
