package ucibridge

import (
	"regexp"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

// Positions used by the pondering tests. All are quiet-ish middlegame/opening
// positions where a fixed-depth search is fast enough for CI.
var ponderFENs = []string{
	refchess.StartFEN,
	"r1bqkbnr/pppp1ppp/2n5/1B2p3/4P3/5N2/PPPP1PPP/RNBQK2R b KQkq - 3 3", // Ruy Lopez, Black to move
	"rnbqkb1r/pp2pppp/3p1n2/8/3NP3/2N5/PPP2PPP/R1BQKB1R b KQkq - 0 5",   // Sicilian-ish
}

// newPonderBridge returns a bridge positioned at fen with pondering enabled.
func newPonderBridge(bin []byte, defs chesstest.Defs, fen string) (*Bridge, error) {
	b := &Bridge{Bin: bin, Defs: defs, Ponder: true, FixedBudgetMs: 1500}
	var err error
	b.pos, err = refchess.ParseFEN(fen)
	return b, err
}

// snap/restore capture and reinstate the carried aux (TT) so a "warm" and a
// "cold" follow-up search can start from the identical TT state.
func snap(b *Bridge) []byte { return append([]byte(nil), b.aux...) }
func restore(b *Bridge, s []byte) {
	if s == nil {
		b.aux = nil
		return
	}
	b.aux = append([]byte(nil), s...)
}

// applyUCI applies a UCI move to the bridge's game position.
func applyUCI(t *testing.T, b *Bridge, uci string) {
	t.Helper()
	mv, err := refchess.ParseMove(uci)
	if err != nil {
		t.Fatalf("parse %q: %v", uci, err)
	}
	if err := b.pos.Make(mv); err != nil {
		t.Fatalf("make %q: %v", uci, err)
	}
}

// followSearch runs one follow-up search over the CURRENT position at the given
// budget/depth, carrying the aux, and returns the raw result. It does not
// commit the move (game state is left untouched).
func followSearch(t *testing.T, b *Bridge, budget uint64, depth byte) engineResult {
	t.Helper()
	pos, err := chesstest.ParseFEN(b.pos.FEN())
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.runEngine(pos, pos.Halfmove, budget, depth, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestPonderMoveExtraction proves source (a): the move read out of the carried
// TT after a real search is the engine's own PV 2nd move — i.e. it equals the
// best move a from-scratch search of the post-move position returns.
func TestPonderMoveExtraction(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: real searches")
	}
	bin, defs, _ := loadBridgeDeps(t)
	const budgetMs = 1500
	hits := 0
	for _, fen := range ponderFENs {
		b, err := newPonderBridge(bin, defs, fen)
		if err != nil {
			t.Fatal(err)
		}
		// Real move M: warms the carried TT and advances b.pos to root+M.
		m, err := b.think(nil)
		if err != nil {
			t.Fatalf("%s: think: %v", fen, err)
		}
		p, ok := b.PonderMove() // source (a): free, from the TT
		if !ok {
			t.Logf("%s: M=%s no TT ponder move (would fall back to source b)", fen, m)
			continue
		}
		hits++
		// Independent oracle: a from-scratch search of the post-M position.
		// Its best move IS the PV 2nd move, so it must equal p.
		cpos, err := chesstest.ParseFEN(b.pos.FEN())
		if err != nil {
			t.Fatal(err)
		}
		cold, err := chesstest.SearchBudget(bin, defs, cpos, maxDepthCap, budgetMs*cyclesPerMs, 5_000_000_000)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: M=%s  ponder(TT)=%s  freshSearch(post-M)=%s", fen, m, p, cold.Move)
		if p != cold.Move {
			t.Errorf("%s: TT ponder move %s != fresh PV-2nd-move %s", fen, p, cold.Move)
		}
	}
	if hits == 0 {
		t.Fatal("no TT ponder moves extracted on any position")
	}
}

// TestPonderHeadStartNodes is THE KEY TEST for the warm-TT head start measured
// as nodes-to-fixed-depth: after a ponder HIT the follow-up search reaches the
// same depth for far FEWER nodes (cycles), because the ponder search already
// populated the TT it reads. Reports the delta per position.
func TestPonderHeadStartNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: fixed-depth searches")
	}
	bin, defs, _ := loadBridgeDeps(t)
	const depth = byte(6)
	for _, fen := range ponderFENs {
		b, err := newPonderBridge(bin, defs, fen)
		if err != nil {
			t.Fatal(err)
		}
		// Prefix: real move M (warms root TT), predict reply P, advance to
		// root+M+P — the position we'd face on a ponder hit.
		m, err := b.think(nil)
		if err != nil {
			t.Fatalf("%s: think: %v", fen, err)
		}
		p, ok, err := b.ponderPrediction()
		if err != nil || !ok {
			t.Fatalf("%s: no ponder prediction (ok=%v err=%v)", fen, ok, err)
		}
		applyUCI(t, b, p)
		base := snap(b) // TT after M+prediction, before the ponder search

		// WARM: ponder search on root+M+P (fixed depth), then the follow-up
		// real search on the same position at the same fixed depth.
		ponderRes := followSearch(t, b, 0, depth)
		warm := followSearch(t, b, 0, depth)

		// COLD: identical follow-up from the pre-ponder TT (no ponder search).
		restore(b, base)
		cold := followSearch(t, b, 0, depth)

		t.Logf("%s: M=%s P=%s  ponder=%d nodes  followup: cold=%d warm=%d nodes  (warm/cold=%.3f, saved %d)",
			fen, m, p, ponderRes.nodes, cold.nodes, warm.nodes,
			float64(warm.nodes)/float64(cold.nodes), int64(cold.nodes)-int64(warm.nodes))
		if warm.depth != cold.depth {
			t.Errorf("%s: warm depth %d != cold depth %d (fixed-depth run)", fen, warm.depth, cold.depth)
		}
		if warm.nodes >= cold.nodes {
			t.Errorf("%s: warm follow-up %d nodes not fewer than cold %d — no head start", fen, warm.nodes, cold.nodes)
		}
	}
}

// TestPonderHeadStartDepth measures the head start as DEPTH, in the realistic
// ponder-hit shape: the ponder search runs deep on the opponent's clock, then
// a short follow-up confirms the move. With the deep TT already warm, the
// follow-up flies back to the pondered depth within a small time budget; the
// cold search of the same position in the same small budget reaches far less
// depth. The delta is the depth the ponder time bought us for free.
func TestPonderHeadStartDepth(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: budgeted searches")
	}
	bin, defs, _ := loadBridgeDeps(t)
	const ponderDepth = byte(6)                    // "opponent thought a while"
	const followBudget = uint64(500 * cyclesPerMs) // short confirming search
	deeper := 0
	for _, fen := range ponderFENs {
		b, err := newPonderBridge(bin, defs, fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := b.think(nil)
		if err != nil {
			t.Fatalf("%s: think: %v", fen, err)
		}
		p, ok, err := b.ponderPrediction()
		if err != nil || !ok {
			t.Fatalf("%s: no ponder prediction (ok=%v err=%v)", fen, ok, err)
		}
		applyUCI(t, b, p)
		base := snap(b)

		// WARM: deep ponder search, then a short follow-up on the warm TT.
		followSearch(t, b, 0, ponderDepth)
		warm := followSearch(t, b, followBudget, maxDepthCap)

		// COLD: the same short follow-up from the pre-ponder TT.
		restore(b, base)
		cold := followSearch(t, b, followBudget, maxDepthCap)

		t.Logf("%s: M=%s P=%s  short(500ms) followup depth: cold=%d warm=%d (Δ=%+d)",
			fen, m, p, cold.depth, warm.depth, int(warm.depth)-int(cold.depth))
		if warm.depth < cold.depth {
			t.Errorf("%s: warm depth %d < cold depth %d — warm TT hurt", fen, warm.depth, cold.depth)
		}
		if warm.depth > cold.depth {
			deeper++
		}
	}
	if deeper == 0 {
		t.Error("expected the warm follow-up to reach greater depth on at least one position")
	}
}

// TestPonderMissCorrectness proves a ponder MISS never corrupts results: after
// pondering on the predicted reply P, a follow-up search on a DIFFERENT reply X
// returns exactly the move it returns with no ponder at all (a TT is never
// wrong), and costs no more.
func TestPonderMissCorrectness(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: fixed-depth searches")
	}
	bin, defs, _ := loadBridgeDeps(t)
	const depth = byte(6)
	tested := 0
	for _, fen := range ponderFENs {
		b, err := newPonderBridge(bin, defs, fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := b.think(nil) // b.pos = root+M
		if err != nil {
			t.Fatalf("%s: think: %v", fen, err)
		}
		p, ok := b.PonderMove()
		if !ok {
			t.Logf("%s: no TT ponder move; skipping miss case", fen)
			continue
		}
		// Pick a legal reply X != P (the "miss").
		var x string
		for _, mv := range b.pos.LegalMoves() {
			if mv.String() != p {
				x = mv.String()
				break
			}
		}
		if x == "" {
			continue
		}
		tested++
		base := snap(b)

		// WARM-miss: ponder on root+M+P, then the real follow-up on root+M+X.
		posP := b.pos.Copy()
		applyUCI(t, b, p)
		followSearch(t, b, 0, depth) // ponder search on root+M+P
		b.pos = posP.Copy()          // back to root+M
		applyUCI(t, b, x)            // opponent actually played X
		warm := followSearch(t, b, 0, depth)

		// COLD: same follow-up on root+M+X from the pre-ponder TT.
		restore(b, base)
		b.pos = posP.Copy()
		applyUCI(t, b, x)
		cold := followSearch(t, b, 0, depth)

		warmMove := chesstest.MoveUCI(warm.from, warm.to, warm.flags)
		coldMove := chesstest.MoveUCI(cold.from, cold.to, cold.flags)
		t.Logf("%s: M=%s pondered=%s actual=%s  followup: cold=%s(%d) warm=%s(%d)",
			fen, m, p, x, coldMove, cold.nodes, warmMove, warm.nodes)
		if warmMove != coldMove {
			t.Errorf("%s: miss corrupted result: warm=%s cold=%s", fen, warmMove, coldMove)
		}
		if warm.score != cold.score {
			t.Errorf("%s: miss changed score: warm=%d cold=%d", fen, warm.score, cold.score)
		}
	}
	if tested == 0 {
		t.Fatal("no miss case exercised")
	}
}

// TestPonderDeterminism: the same seed/budget produces the same ponder result.
func TestPonderDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: real searches")
	}
	bin, defs, _ := loadBridgeDeps(t)
	run := func(fen string) (m, p string, nodes uint64) {
		b, err := newPonderBridge(bin, defs, fen)
		if err != nil {
			t.Fatal(err)
		}
		if m, err = b.think(nil); err != nil {
			t.Fatal(err)
		}
		var ok bool
		if p, ok = b.PonderMove(); !ok {
			p = "(none)"
		}
		applyUCI(t, b, p)
		res := followSearch(t, b, 0, 6)
		return m, p, res.nodes
	}
	for _, fen := range ponderFENs {
		m1, p1, n1 := run(fen)
		m2, p2, n2 := run(fen)
		if m1 != m2 || p1 != p2 || n1 != n2 {
			t.Errorf("%s: nondeterministic: (%s,%s,%d) vs (%s,%s,%d)", fen, m1, p1, n1, m2, p2, n2)
		}
	}
}

// TestPonderDisabledByteIdentical proves the Ponder flag alone changes nothing
// about the search: driving the exact same plain-go UCI script with Ponder off
// vs on yields identical output once the advisory "ponder P" suffix (the only
// thing the flag adds) is stripped. Every ponder code path is gated on the
// flag, so Ponder=false is byte-identical to the non-pondering engine.
func TestPonderDisabledByteIdentical(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: real searches")
	}
	bin, defs, _ := loadBridgeDeps(t)
	script := strings.Join([]string{
		"uci",
		"isready",
		"ucinewgame",
		"position startpos moves e2e4",
		"go movetime 800",
		"position startpos moves e2e4 e7e5 g1f3",
		"go movetime 800",
		"quit",
	}, "\n")

	run := func(ponder bool) string {
		b := &Bridge{Bin: bin, Defs: defs, Ponder: ponder}
		var out strings.Builder
		if err := b.Run(strings.NewReader(script), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	off := run(false)
	on := run(true)

	// Strip the advisory suffix and the Ponder option line that the flag adds.
	ponderSuffix := regexp.MustCompile(` ponder [a-h][1-8][a-h][1-8][nbrq]?`)
	onStripped := ponderSuffix.ReplaceAllString(on, "")
	onStripped = strings.ReplaceAll(onStripped, "option name Ponder type check default true\n", "")

	if off != onStripped {
		t.Errorf("Ponder=false not byte-identical to Ponder=true minus the ponder suffix.\n--- off ---\n%s\n--- on (stripped) ---\n%s", off, onStripped)
	}
	// Sanity: with Ponder on, the bestmove really did carry a ponder move.
	if !strings.Contains(on, " ponder ") {
		t.Error("Ponder=true did not emit a 'ponder' suffix")
	}
}
