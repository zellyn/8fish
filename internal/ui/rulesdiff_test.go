package ui_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/ui"
)

// ---------------------------------------------------------------------------
// Differential testing of the UI's OWN copy of the rules against refchess.
//
// The on-device UI re-derives, at every turn, three things a referee decides:
//   - how many legal moves the side to move has (UINLEGAL),
//   - whether that side is in check (UICHK),
//   - whether the game is over and how (UIRESULT).
//
// refchess is an independent, perft-verified implementation of the same three
// judgements. Any disagreement is a bug in one of them.
// ---------------------------------------------------------------------------

// syncFEN installs a position and returns the UI's verdict on it.
func syncFEN(t *testing.T, u *ui.Machine, fen string) (nlegal, check, result byte) {
	t.Helper()
	if err := u.SetFEN(fen); err != nil {
		t.Fatalf("SetFEN(%q): %v", fen, err)
	}
	return u.Peek(ui.UINLEGAL), u.Peek(ui.UICHK), u.Peek(ui.UIRESULT)
}

// refVerdict is refchess's judgement on the same position.
func refVerdict(t *testing.T, fen string) (nlegal int, check bool) {
	t.Helper()
	p, err := refchess.ParseFEN(fen)
	if err != nil {
		t.Fatalf("refchess.ParseFEN(%q): %v", fen, err)
	}
	return len(p.LegalMoves()), p.InCheck()
}

// checkPosition compares the UI's three judgements against refchess's.
func checkPosition(t *testing.T, u *ui.Machine, fen string) bool {
	t.Helper()
	nUI, chkUI, res := syncFEN(t, u, fen)
	nRef, chkRef := refVerdict(t, fen)

	ok := true
	if int(nUI) != nRef {
		t.Errorf("%s: UI counts %d legal moves, refchess %d", fen, nUI, nRef)
		ok = false
	}
	if (chkUI != 0) != chkRef {
		t.Errorf("%s: UI in-check=%v, refchess in-check=%v", fen, chkUI != 0, chkRef)
		ok = false
	}
	switch {
	case nRef == 0 && chkRef && res != ui.ResMate:
		t.Errorf("%s: checkmate, but UIRESULT = %s", fen, ui.ResultName(res))
		ok = false
	case nRef == 0 && !chkRef && res != ui.ResStale:
		t.Errorf("%s: stalemate, but UIRESULT = %s", fen, ui.ResultName(res))
		ok = false
	case nRef > 0 && (res == ui.ResMate || res == ui.ResStale):
		t.Errorf("%s: %d legal moves, but UIRESULT = %s", fen, nRef, ui.ResultName(res))
		ok = false
	}
	return ok
}

// uiAccepts types a move at the given position and reports whether the UI
// played it.
func uiAccepts(t *testing.T, u *ui.Machine, fen, move string) bool {
	t.Helper()
	if err := u.SetFEN(fen); err != nil {
		t.Fatalf("SetFEN(%q): %v", fen, err)
	}
	before := u.Peek(ui.UIHCNT)
	if err := u.Enter(move); err != nil {
		t.Fatalf("typing %q: %v", move, err)
	}
	return u.Peek(ui.UIHCNT) != before
}

// diffMoveSets names exactly which moves the two implementations disagree on.
// Candidates are every square-to-square pair (plus the four promotions off
// each legal move's from-square), so an ACCEPTED illegal move is caught as
// surely as a REJECTED legal one.
func diffMoveSets(t *testing.T, u *ui.Machine, fen string) (missing, extra []string) {
	t.Helper()
	p, err := refchess.ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	legal := map[string]bool{}
	for _, m := range p.LegalMoves() {
		legal[m.String()] = true
	}
	cands := map[string]bool{}
	for m := range legal {
		cands[m] = true
	}
	for from := range 64 {
		fs := sqName(from)
		for to := range 64 {
			if from == to {
				continue
			}
			cands[fs+sqName(to)] = true
		}
	}
	list := make([]string, 0, len(cands))
	for m := range cands {
		list = append(list, m)
	}
	sort.Strings(list)
	for _, m := range list {
		acc := uiAccepts(t, u, fen, m)
		if acc && !legal[m] {
			extra = append(extra, m)
		}
		if !acc && legal[m] {
			missing = append(missing, m)
		}
	}
	return missing, extra
}

func sqName(sq int) string { return fmt.Sprintf("%c%c", 'a'+sq%8, '1'+sq/8) }

// TestRulesCorpus runs the UI's referee over positions chosen to hit the
// castling, en-passant and promotion corners that a random sweep reaches only
// by luck.
func TestRulesCorpus(t *testing.T) {
	u := twoPlayer(t)
	for _, tc := range []struct{ name, fen string }{
		// --- castling: transit squares, check, and the rook's own path ---
		{"quiet, all rights", "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1"},
		{"white in check from d-file", "r3k2r/8/8/8/8/8/8/R2rK2R w KQkq - 0 1"},
		{"e1 attacked by rook on e8", "r3k2r/4r3/8/8/8/8/8/R3K2R w KQ - 0 1"},
		{"f1 attacked", "r3k2r/8/8/5q2/8/8/8/R3K2R w KQkq - 0 1"},
		{"g1 attacked", "r3k2r/8/8/6q1/8/8/8/R3K2R w KQkq - 0 1"},
		{"d1 attacked", "r3k2r/8/8/3q4/8/8/8/R3K2R w KQkq - 0 1"},
		{"c1 attacked", "r3k2r/8/8/2q5/8/8/8/R3K2R w KQkq - 0 1"},
		{"b1 attacked (LEGAL: not a transit square)", "r3k2r/8/8/1q6/8/8/8/R3K2R w KQkq - 0 1"},
		{"b1 occupied blocks O-O-O", "r3k2r/8/8/8/8/8/8/RN2K2R w KQkq - 0 1"},
		{"black to move, f8 attacked", "r3k2r/8/8/5Q2/8/8/8/R3K2R b KQkq - 0 1"},
		{"black to move, d8 attacked", "r3k2r/8/8/3Q4/8/8/8/R3K2R b KQkq - 0 1"},
		{"black to move, b8 attacked (LEGAL)", "r3k2r/8/8/1Q6/8/8/8/R3K2R b KQkq - 0 1"},
		{"rights already gone", "r3k2r/8/8/8/8/8/8/R3K2R w - - 0 1"},
		{"only white kingside right", "r3k2r/8/8/8/8/8/8/R3K2R w K - 0 1"},
		{"only black queenside right", "r3k2r/8/8/8/8/8/8/R3K2R b q - 0 1"},

		// --- en passant ---
		{"ep horizontal pin (must be ILLEGAL)", "8/8/8/8/k2Pp2R/8/8/4K3 b - d3 0 1"},
		{"ep legal, no pin", "8/8/8/8/3Pp3/8/8/4K2k b - d3 0 1"},
		{"ep double, both sides", "8/8/8/8/2PpP3/8/8/4K2k b - c3 0 1"},
		{"ep diagonal pin", "8/8/8/8/1k1Pp3/8/8/4K1B1 b - d3 0 1"},
		{"ep resolving check", "8/8/8/8/3PpK2/8/8/7k b - d3 0 1"},
		{"white ep", "4k3/8/8/3pP3/8/8/8/4K3 w - d6 0 1"},

		// --- promotion ---
		{"promotion with capture", "1n2k3/P7/8/8/8/8/8/4K3 w - - 0 1"},
		{"promotion giving mate", "8/6P1/8/8/8/8/8/K5k1 w - - 0 1"},
		{"black promotion", "4k3/8/8/8/8/8/1p6/R3K3 b - - 0 1"},

		// --- mate / stalemate corners ---
		{"stalemate", "7k/5Q2/6K1/8/8/8/8/8 b - - 0 1"},
		{"back rank mate", "6k1/5ppp/8/8/8/8/8/R5K1 b - - 0 1"},
		{"smothered mate", "6rk/6pp/7N/8/8/8/8/K7 b - - 0 1"},
		{"double check, king must move", "4k3/8/8/8/8/4n3/8/R3K2b w - - 0 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !checkPosition(t, u, tc.fen) {
				missing, extra := diffMoveSets(t, u, tc.fen)
				t.Errorf("  UI REJECTS legal: %v\n  UI ACCEPTS illegal: %v",
					missing, extra)
			}
		})
	}
}

// TestRulesRandomSweep plays random games and compares the UI's referee with
// refchess at every position reached.
func TestRulesRandomSweep(t *testing.T) {
	if testing.Short() {
		t.Skip("boots the image and syncs hundreds of positions")
	}
	u := twoPlayer(t)
	rnd := rand.New(rand.NewSource(20260728))
	const games, plies = 30, 30
	bad := 0
	for g := range games {
		p, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for range plies {
			m, ok := refchess.RandomMove(p, rnd)
			if !ok {
				break
			}
			if err := p.Make(m); err != nil {
				t.Fatal(err)
			}
			if !checkPosition(t, u, p.FEN()) {
				bad++
				if bad > 5 {
					t.Fatalf("stopping after %d disagreements (game %d)", bad, g)
				}
			}
		}
	}
}

// TestRulesPlaythrough is the strongest differential available: random legal
// games TYPED INTO THE UI one move at a time, with the UI's whole position —
// board, side, castling rights, en-passant square, halfmove clock — compared
// against refchess after every single ply.
//
// SetFEN cannot catch a rights-update bug, because it INSTALLS the rights.
// Only playing the move can, which is what makes this the test that covers
// "the rook was captured on its home square" and "the halfmove clock resets
// on a capture".
func TestRulesPlaythrough(t *testing.T) {
	if testing.Short() {
		t.Skip("types thousands of keystrokes into the image")
	}
	u := twoPlayer(t)
	rnd := rand.New(rand.NewSource(0x8F1511))
	const games, plies = 12, 60
	for g := range games {
		mustEnter(t, u, "n")
		p, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for ply := range plies {
			m, ok := refchess.RandomMove(p, rnd)
			if !ok {
				break
			}
			before := u.Peek(ui.UIHCNT)
			if err := u.Enter(m.String()); err != nil {
				t.Fatalf("game %d ply %d typing %q: %v", g, ply, m, err)
			}
			if u.Peek(ui.UIHCNT) == before {
				t.Fatalf("game %d ply %d: UI REJECTED the legal move %s in %s\n%s",
					g, ply, m, p.FEN(), u.Screen())
			}
			if err := p.Make(m); err != nil {
				t.Fatal(err)
			}
			if got, want := normFEN(u.FEN()), normFEN(p.FEN()); got != want {
				t.Fatalf("game %d ply %d after %s:\n  UI       %s\n  refchess %s",
					g, ply, m, got, want)
			}
			if u.Peek(ui.UIRESULT) != ui.ResNone {
				break
			}
		}
	}
}

// TestEnPassantExpiry: the ep square must survive exactly one ply.
func TestEnPassantExpiry(t *testing.T) {
	u := twoPlayer(t)
	for _, mv := range []string{"e2e4", "a7a6", "e4e5", "d7d5"} {
		mustEnter(t, u, mv)
	}
	if got := u.FEN(); !strings.Contains(got, " d6 ") {
		t.Fatalf("after 1.e4 a6 2.e5 d5 the ep square should be d6: %s", got)
	}
	mustEnter(t, u, "a2a3")
	if got := u.FEN(); !strings.Contains(got, " - ") {
		t.Errorf("ep square survived a ply: %s", got)
	}
	mustEnter(t, u, "h7h6")
	before := u.Peek(ui.UIHCNT)
	mustEnter(t, u, "e5d6")
	if u.Peek(ui.UIHCNT) != before {
		t.Errorf("UI accepted a STALE en-passant capture e5d6:\n%s", u.Screen())
	}
}

// TestTakebackRestoresState replays a game and takes it back one ply at a
// time, requiring the FULL position — castling rights, ep square, halfmove
// clock — to match what refchess says it was.
func TestTakebackRestoresState(t *testing.T) {
	u := twoPlayer(t)
	moves := []string{
		"e2e4", "c7c5", "g1f3", "d7d6", "d2d4", "c5d4", "f3d4", "g8f6",
		"b1c3", "a7a6", "f1e2", "e7e5", "d4b3", "f8e7", "e1g1", "e8g8",
	}
	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{normFEN(p.FEN())}
	for _, mv := range moves {
		mustEnter(t, u, mv)
		m, err := refchess.ParseMove(mv)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Make(m); err != nil {
			t.Fatal(err)
		}
		want = append(want, normFEN(p.FEN()))
	}
	if got := normFEN(u.FEN()); got != want[len(want)-1] {
		t.Fatalf("after the opening: %q, want %q", got, want[len(want)-1])
	}
	for n := len(moves) - 1; n >= 0; n-- {
		mustEnter(t, u, "t")
		if got, w := normFEN(u.FEN()), want[n]; got != w {
			t.Fatalf("after taking back to ply %d: %q, want %q\n%s", n, got, w, u.Screen())
		}
		if got := int(u.Peek(ui.UIHCNT)); got != n {
			t.Fatalf("UIHCNT = %d after taking back to ply %d", got, n)
		}
	}
	mustEnter(t, u, "t")
	if got := u.Peek(ui.UIHCNT); got != 0 {
		t.Errorf("takeback at ply 0 changed UIHCNT to %d", got)
	}
}

// ---------------------------------------------------------------------------
// Threefold repetition: is it POSITION-based (side to move, castling rights,
// en-passant square) or just a loose piece-placement hash?
// ---------------------------------------------------------------------------

// repState reports the UI's repetition bookkeeping after each ply of a line.
func repLine(t *testing.T, u *ui.Machine, fen string, moves []string) []byte {
	t.Helper()
	if err := u.SetFEN(fen); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 0, len(moves))
	for i, mv := range moves {
		before := u.Peek(ui.UIHCNT)
		if err := u.Enter(mv); err != nil {
			t.Fatalf("ply %d %q: %v", i, mv, err)
		}
		if u.Peek(ui.UIHCNT) == before && u.Peek(ui.UIRESULT) == ui.ResNone {
			t.Fatalf("ply %d: UI rejected %q\n%s", i, mv, u.Screen())
		}
		out = append(out, u.Peek(ui.UIRESULT))
	}
	return out
}

// TestThreefoldExactPly: a knight shuffle repeats the START position, and the
// draw must be declared on the ply that makes it the THIRD occurrence — not
// the second, and not one ply late.
func TestThreefoldExactPly(t *testing.T) {
	u := twoPlayer(t)
	// Occurrence 1 = the start position itself; 2 after ply 4; 3 after ply 8.
	moves := []string{
		"g1f3", "g8f6", "f3g1", "f6g8", // -> occurrence 2
		"g1f3", "g8f6", "f3g1", "f6g8", // -> occurrence 3: DRAW
	}
	res := repLine(t, u, refchess.StartFEN, moves)
	for i, r := range res {
		want := byte(ui.ResNone)
		if i == 7 {
			want = ui.ResRep
		}
		if r != want {
			t.Errorf("after ply %d (%s): UIRESULT = %s, want %s",
				i+1, moves[i], ui.ResultName(r), ui.ResultName(want))
		}
	}
	if !contains(u.Screen().Text(12), "DRAW: REPETITION") {
		t.Errorf("row 12 = %q", u.Screen().Text(12))
	}
}

// TestThreefoldRespectsCastlingRights is the discriminating case. The four
// moves Ra1b1 Ra8b8 Rb1a1 Rb8a8 restore the PIECE PLACEMENT exactly while
// destroying two castling rights, so the position after ply 4 is NOT the same
// position as the one before ply 1 — FIDE says a position repeats only when
// the castling and en-passant possibilities are the same too.
//
// A placement-only hash would see three occurrences after ply 8 and declare a
// draw four plies early. A correct one waits until ply 12.
func TestThreefoldRespectsCastlingRights(t *testing.T) {
	u := twoPlayer(t)
	const fen = "r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1"
	moves := []string{
		"a1b1", "a8b8", "b1a1", "b8a8", // rights KQkq -> Kk; occurrence 1 of "Kk"
		"a1b1", "a8b8", "b1a1", "b8a8", // occurrence 2 of "Kk"
		"a1b1", "a8b8", "b1a1", "b8a8", // occurrence 3 of "Kk": DRAW here
	}
	res := repLine(t, u, fen, moves)
	if res[7] != ui.ResNone {
		t.Errorf("draw declared after ply 8 (%s): the position before ply 1 had "+
			"castling rights KQkq and the ones after plies 4 and 8 have Kk, so "+
			"ply 8 is only the SECOND occurrence. The repetition hash is ignoring "+
			"castling rights.", ui.ResultName(res[7]))
	}
	if res[11] != ui.ResRep {
		t.Errorf("after ply 12 UIRESULT = %s, want draw: repetition",
			ui.ResultName(res[11]))
	}
}

// TestNoMoveAfterGameOver: once the UI has declared a result, a legal move
// typed at the board must be refused and the position must not change.
func TestNoMoveAfterGameOver(t *testing.T) {
	u := twoPlayer(t)
	// Fool's mate.
	for _, mv := range []string{"f2f3", "e7e5", "g2g4", "d8h4"} {
		mustEnter(t, u, mv)
	}
	if got := u.Peek(ui.UIRESULT); got != ui.ResMate {
		t.Fatalf("after 1.f3 e5 2.g4 Qh4# UIRESULT = %s\n%s", ui.ResultName(got), u.Screen())
	}
	fen, n := u.FEN(), u.Peek(ui.UIHCNT)
	for _, mv := range []string{"e1f2", "h4h2", "a2a3"} {
		mustEnter(t, u, mv)
		if u.FEN() != fen || u.Peek(ui.UIHCNT) != n {
			t.Fatalf("the UI played %q after checkmate", mv)
		}
		if !contains(u.Screen().Text(17), "GAME OVER") {
			t.Errorf("typing %q after mate: row 17 = %q", mv, u.Screen().Text(17))
		}
	}
}

// TestFiftyMoveBoundary pins the rule to HALFMOVES, not fullmoves, and checks
// that CHECKMATE on the hundredth halfmove is a mate, not a draw.
func TestFiftyMoveBoundary(t *testing.T) {
	u := twoPlayer(t)
	for _, tc := range []struct {
		name, fen, move string
		want            byte
	}{
		{"99 -> 100 on a quiet move is a draw",
			"8/8/4k3/8/8/4K3/8/R7 w - - 99 60", "a1a2", ui.Res50},
		{"98 -> 99 is not",
			"8/8/4k3/8/8/4K3/8/R7 w - - 98 60", "a1a2", ui.ResNone},
		{"a capture resets the clock",
			"8/8/4k3/8/8/4K3/r7/R7 w - - 99 60", "a1a2", ui.ResNone},
		{"a pawn move resets the clock",
			"8/8/4k3/8/8/4K3/P7/R7 w - - 99 60", "a2a3", ui.ResNone},
		{"mate on the hundredth halfmove is MATE, not a draw",
			"6k1/5ppp/8/8/8/8/8/R5K1 w - - 99 60", "a1a8", ui.ResMate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := u.SetFEN(tc.fen); err != nil {
				t.Fatal(err)
			}
			mustEnter(t, u, tc.move)
			if got := u.Peek(ui.UIRESULT); got != tc.want {
				t.Errorf("after %s from %s: UIRESULT = %s, want %s\n%s",
					tc.move, tc.fen, ui.ResultName(got), ui.ResultName(tc.want), u.Screen())
			}
		})
	}
}

// TestResignAwardsTheRightSide. The winner is derived from UIHUMAN, which in
// TWO-PLAYER mode is the sentinel $FF rather than a colour.
func TestResignAwardsTheRightSide(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, u *ui.Machine)
		want  string
	}{
		{"human is White", func(t *testing.T, u *ui.Machine) {}, "BLACK WINS"},
		// TWO presses: the cycle is WHITE -> TWO PLAYERS -> BLACK, so reaching
		// Black is deliberate. The engine answers on the way, which leaves
		// Black -- the human -- to move, and therefore to resign.
		{"human is Black", func(t *testing.T, u *ui.Machine) {
			mustEnter(t, u, "s")
			mustEnter(t, u, "s")
		}, "WHITE WINS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := boot(t)
			tc.setup(t, u)
			mustEnter(t, u, "r")
			if got := u.Screen().Text(17); !contains(got, tc.want) {
				t.Errorf("row 17 = %q, want %q", got, tc.want)
			}
		})
	}
	// Two-player mode: whoever resigns, the OTHER side must win.
	for _, tc := range []struct {
		name string
		pre  []string
		want string
	}{
		{"White resigns on move 1", nil, "BLACK WINS"},
		{"Black resigns on move 1", []string{"e2e4"}, "WHITE WINS"},
	} {
		t.Run("two players: "+tc.name, func(t *testing.T) {
			u := twoPlayer(t)
			for _, mv := range tc.pre {
				mustEnter(t, u, mv)
			}
			mustEnter(t, u, "r")
			if got := u.Screen().Text(17); !contains(got, tc.want) {
				t.Errorf("side to move resigned; row 17 = %q, want %q\n%s",
					got, tc.want, u.Screen())
			}
		})
	}
}

// TestTakebackAgainstTheEngine. The two-player takeback gate cannot see the
// two-plies-at-a-time path, because in referee mode UIHUMAN is $FF and
// cmd_take always steps back one. Playing a real opponent exercises the other
// branch, and repeatedly: each `T` must undo the human's move AND the
// engine's reply, hand the move back to the human, and leave a position
// refchess agrees with — including castling rights and the halfmove clock.
func TestTakebackAgainstTheEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real searches")
	}
	u := boot(t)
	mustEnter(t, u, "l") // level 1: fixed depth 2, fast
	mustEnter(t, u, "1")
	if got := u.Peek(ui.UILEVEL); got != 1 {
		t.Fatalf("UILEVEL = %d after L 1", got)
	}
	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	// Each human move is followed by the engine's reply, so the history
	// grows by two and the board state after each pair is replayable.
	var snapshots []string
	snapshots = append(snapshots, normFEN(u.FEN()))
	for _, mv := range []string{"e2e4", "g1f3", "f1c4", "e1g1"} {
		if err := u.Enter(mv); err != nil {
			t.Fatalf("typing %q: %v\n%s", mv, err, u.Screen())
		}
		if got := u.Peek(ui.UIRESULT); got != ui.ResNone {
			t.Fatalf("game ended (%s) during the opening", ui.ResultName(got))
		}
		snapshots = append(snapshots, normFEN(u.FEN()))
	}
	hist := u.History()
	if len(hist) != 8 {
		t.Fatalf("history = %v, want 8 plies (4 human + 4 engine)", hist)
	}
	// Rebuild refchess's view of the same game so the takebacks have an
	// independent reference at every step.
	want := []string{normFEN(p.FEN())}
	for _, mv := range hist {
		m, err := refchess.ParseMove(mv)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Make(m); err != nil {
			t.Fatalf("engine played %q, refchess says: %v", mv, err)
		}
		want = append(want, normFEN(p.FEN()))
	}
	for i := 4; i >= 1; i-- {
		mustEnter(t, u, "t")
		if got, w := normFEN(u.FEN()), want[2*(i-1)]; got != w {
			t.Fatalf("takeback %d:\n  UI       %s\n  refchess %s\n%s",
				5-i, got, w, u.Screen())
		}
		if got := int(u.Peek(ui.UIHCNT)); got != 2*(i-1) {
			t.Errorf("takeback %d: UIHCNT = %d, want %d", 5-i, got, 2*(i-1))
		}
		if got := u.Peek(ui.UIRESULT); got != ui.ResNone {
			t.Errorf("takeback %d left UIRESULT = %s", 5-i, ui.ResultName(got))
		}
		if !contains(u.Screen().Text(12), "WHITE TO MOVE") {
			t.Errorf("takeback %d: status row = %q", 5-i, u.Screen().Text(12))
		}
	}
	if got := normFEN(u.FEN()); got != snapshots[0] {
		t.Errorf("back at the start: %q, want %q", got, snapshots[0])
	}
}

// TestUnderpromotionThroughThePrompt: all four pieces must be reachable when
// the fifth character is omitted, including on a CAPTURE, and the prompt must
// be escapable without touching the game.
func TestUnderpromotionThroughThePrompt(t *testing.T) {
	const fen = "1n2k3/P7/8/8/8/8/8/4K3 w - - 0 1" // a7xb8 and a7a8 both promote
	for _, tc := range []struct{ move, key, want string }{
		{"a7a8", "q", "Qn2k3/8/8/8/8/8/8/4K3 b - - 0"},
		{"a7a8", "r", "Rn2k3/8/8/8/8/8/8/4K3 b - - 0"},
		{"a7a8", "b", "Bn2k3/8/8/8/8/8/8/4K3 b - - 0"},
		{"a7a8", "n", "Nn2k3/8/8/8/8/8/8/4K3 b - - 0"},
		{"a7b8", "q", "1Q2k3/8/8/8/8/8/8/4K3 b - - 0"},
		{"a7b8", "n", "1N2k3/8/8/8/8/8/8/4K3 b - - 0"},
	} {
		t.Run(tc.move+"="+tc.key, func(t *testing.T) {
			u := twoPlayer(t)
			if err := u.SetFEN(fen); err != nil {
				t.Fatal(err)
			}
			mustEnter(t, u, tc.move)
			if got := u.Screen().Text(17); !contains(got, "PROMOTE TO") {
				t.Fatalf("row 17 = %q, want the promotion prompt", got)
			}
			if err := u.Key(tc.key[0]); err != nil {
				t.Fatal(err)
			}
			if got := normFEN(u.FEN()); got != tc.want {
				t.Errorf("%s + %s: %q, want %q\n%s", tc.move, tc.key, got, tc.want, u.Screen())
			}
		})
	}
	t.Run("escape leaves the game alone", func(t *testing.T) {
		u := twoPlayer(t)
		if err := u.SetFEN(fen); err != nil {
			t.Fatal(err)
		}
		before, n := u.FEN(), u.Peek(ui.UIHCNT)
		mustEnter(t, u, "a7a8")
		if err := u.Key(0x1B); err != nil { // ESC
			t.Fatal(err)
		}
		if u.FEN() != before || u.Peek(ui.UIHCNT) != n {
			t.Errorf("ESC at the promotion prompt changed the game: %s", u.FEN())
		}
	})
}

// posKey is a FIDE "same position" key: placement, side to move, castling
// rights and the en-passant square — everything but the clocks.
func posKey(p *refchess.Position) string {
	f := strings.Fields(p.FEN())
	return strings.Join(f[:4], " ")
}

// longGame builds a LEGAL game that avoids every draw rule the UI knows: it
// never lets the halfmove clock reach 100 and never plays into a third
// occurrence of a position. Anything it produces is a game the UI must not
// call a draw.
func longGame(t *testing.T, plies int) []string {
	t.Helper()
	rnd := rand.New(rand.NewSource(31337))
	for attempt := 0; attempt < 400; attempt++ {
		p, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]int{posKey(p): 1}
		var moves []string
	ply:
		for len(moves) < plies {
			legal := p.LegalMoves()
			rnd.Shuffle(len(legal), func(i, j int) { legal[i], legal[j] = legal[j], legal[i] })
			// When the 50-move clock is getting close, a pawn move or a
			// capture is the only thing that keeps the game alive.
			resetting := p.HalfmoveClock() >= 80
			for pass := 0; pass < 2; pass++ {
				for _, m := range legal {
					q := p.Copy()
					if err := q.Make(m); err != nil {
						continue
					}
					if q.HalfmoveClock() >= 100 || seen[posKey(q)] >= 2 {
						continue
					}
					if pass == 0 && resetting && q.HalfmoveClock() != 0 {
						continue
					}
					if len(q.LegalMoves()) == 0 {
						continue // mate or stalemate ends the game
					}
					p = q
					seen[posKey(p)]++
					moves = append(moves, m.String())
					continue ply
				}
			}
			break // painted into a corner; try another seed
		}
		if len(moves) >= plies {
			return moves
		}
	}
	t.Skipf("could not build a %d-ply draw-free game", plies)
	return nil
}

// TestLongGameIsNotDrawn is the gate on the fix for the RES_LONG defect: a
// legal game is never over because of its LENGTH.
//
// The UI used to declare "DRAW: TOO LONG" the moment UIHCNT reached 250 — in
// ANY position, including a completely winning one — because the history and
// hash arrays are one page each. 125 moves is not a draw under any rule (the
// FIDE ceiling is the 75-MOVE rule, a counter, not a ply count), so that was
// a wrong RESULT, not a cosmetic limit.
//
// Now the game goes on and the RECORDING degrades instead. This test plays a
// draw-free game PAST the old cap and past the array limit and requires:
//
//   - no result is ever declared, at ply 250 or anywhere else;
//   - the position keeps tracking refchess after the arrays are full, so the
//     UI is still refereeing rather than freewheeling;
//   - UIHCNT stops at 255 and UIHFULL comes up, so nothing wraps to index 0;
//   - nothing is written past the last byte of any array (canaries);
//   - takeback is REFUSED, in words, rather than replaying to a position
//     that is not the one before the last move.
// canaryLo/canaryHi bracket the START of the ponder position snapshot
// (PPBOARD, $FF50-$FFCF; see asm/ui.s). This was "free LC RAM" when the
// canary moved here after MIXED MODE landed, but pondering (2026-08-07) took
// the LAST free run below the vectors -- $FF00-$FFEF is now UI80BUF +
// PPBOARD/PPPIECE with NOTHING free above the history arrays. So the canary
// no longer watches free space; it watches the ponder snapshot, and the
// invariant it now enforces is real and worth keeping: a maximal-length
// TWO-PLAYER game (which never ponders) must not spill the history arrays or
// UI80BUF into the ponder region. If it fires, either the $FB00-$FEFF arrays
// overran or UI80BUF wrote past its 80 bytes. (Earlier this comment claimed
// $FF50 was "GENUINELY free" -- the third memory-map comment in this repo to
// go stale; it is corrected here rather than left to rot.)
const (
	canaryLo = uint16(0xFF50)
	canaryHi = uint16(0xFF60)
)

func TestLongGameIsNotDrawn(t *testing.T) {
	if testing.Short() {
		t.Skip("types 262 moves into the image")
	}
	const plies = 262 // 255 recorded + 7 played with the arrays full
	moves := longGame(t, plies)
	u := twoPlayer(t)

	// Canaries: the byte after each array's last, and the start position's
	// own hash entry at index 0, which nothing may ever rewrite.
	hash0 := [4]byte{}
	for i := range hash0 {
		hash0[i] = u.Peek(ui.UIHASH0 + uint16(0x100*i))
	}
	// canaryLo/canaryHi bracket the ponder snapshot region (PPBOARD). A
	// two-player game never ponders, so these bytes must stay untouched; a
	// change means the history arrays or UI80BUF spilled into them (see the
	// const's comment -- the LC is full, there is no free RAM left to canary).
	for a := canaryLo; a < canaryHi; a++ {
		u.Poke(a, 0xC5)
	}

	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	for i, mv := range moves {
		if err := u.Enter(mv); err != nil {
			t.Fatalf("ply %d %q: %v", i+1, mv, err)
		}
		m, err := refchess.ParseMove(mv)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Make(m); err != nil {
			t.Fatalf("ply %d %q: refchess says %v", i+1, mv, err)
		}
		if res := u.Peek(ui.UIRESULT); res != ui.ResNone {
			t.Fatalf("ply %d (%s): UIRESULT = %s in a game that meets NO draw rule "+
				"(the halfmove clock and repetitions were both kept clear)\n%s",
				i+1, mv, ui.ResultName(res), u.Screen())
		}
		if got, want := normFEN(u.FEN()), normFEN(p.FEN()); got != want {
			t.Fatalf("ply %d (%s): UI position %q, refchess %q\n%s", i+1, mv, got, want, u.Screen())
		}
	}
	t.Logf("after %d legal plies (move %d) the game is still live:\n%s",
		plies, (plies+1)/2, u.Screen())

	if got := u.Peek(ui.UIHCNT); got != 255 {
		t.Errorf("UIHCNT = %d after %d plies, want it pinned at 255 (0 would mean it WRAPPED)", got, plies)
	}
	if u.Peek(ui.UIHFULL) == 0 {
		t.Errorf("UIHFULL = 0 after %d plies: the UI does not know its history is full", plies)
	}
	for i := range hash0 {
		if got := u.Peek(ui.UIHASH0 + uint16(0x100*i)); got != hash0[i] {
			t.Errorf("UIHASH%d[0] changed from $%02X to $%02X: something wrote past "+
				"the end of the array below it", i, hash0[i], got)
		}
	}
	// The canary sits ABOVE UI80BUF. $FF00-$FF4F is the mixed-mode window's
	// 80-column staging line, which uiprompt rewrites on EVERY keystroke in
	// both display modes -- so a canary at $FF00 is not watching free RAM, it
	// is watching a live buffer, and this test failed for every keystroke
	// rather than for an overrun. $FF50-$FFEF is what is actually free below
	// the 6502 vectors; see the UI80BUF comment in asm/ui.s.
	for a := canaryLo; a < canaryHi; a++ {
		if got := u.Peek(a); got != 0xC5 {
			t.Errorf("$%04X = $%02X: written past the end of UIHASH3", a, got)
		}
	}

	// Takeback is refused, and says so, and leaves the game exactly alone.
	before, cnt := u.FEN(), u.Peek(ui.UIHCNT)
	mustEnter(t, u, "t")
	if got := u.Screen().Text(17); !contains(got, "MOVE LIST FULL") {
		t.Errorf("row 17 = %q, want the takeback refusal", strings.TrimSpace(got))
	}
	if u.FEN() != before || u.Peek(ui.UIHCNT) != cnt {
		t.Errorf("takeback with a full history changed the game: %s", u.FEN())
	}
	// ...and the game is still playable after the refusal.
	legal := p.LegalMoves()
	if len(legal) == 0 {
		t.Fatal("the constructed game ended in mate or stalemate")
	}
	mustEnter(t, u, legal[0].String())
	if err := p.Make(legal[0]); err != nil {
		t.Fatal(err)
	}
	if got, want := normFEN(u.FEN()), normFEN(p.FEN()); got != want {
		t.Errorf("a move played after the takeback refusal: UI %q, refchess %q", got, want)
	}
}

// TestTakebackAcrossABookMove. The engine's book moves go through the same
// history arrays as its searched ones, so a takeback has to replay them the
// same way — with the resident book present, which is the configuration a
// player actually boots.
func TestTakebackAcrossABookMove(t *testing.T) {
	if testing.Short() {
		t.Skip("boots with the resident book")
	}
	blob := book.DefaultEntries()
	u, err := ui.Boot(root, blob)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	start := normFEN(u.FEN())
	mustEnter(t, u, "e2e4")
	hist := u.History()
	if len(hist) != 2 {
		t.Fatalf("the engine did not reply to 1.e4: %v\n%s", hist, u.Screen())
	}
	if !contains(u.Screen().Text(16), "BOOK:") {
		t.Logf("note: the reply was not a book move (row 16 = %q)", u.Screen().Text(16))
	}
	// Independently replay the same two plies.
	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	for _, mv := range hist {
		m, err := refchess.ParseMove(mv)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Make(m); err != nil {
			t.Fatalf("the engine played %q, refchess says: %v", mv, err)
		}
	}
	if got, want := normFEN(u.FEN()), normFEN(p.FEN()); got != want {
		t.Fatalf("after 1.e4 + the book reply:\n  UI       %s\n  refchess %s", got, want)
	}
	mustEnter(t, u, "t")
	if got := normFEN(u.FEN()); got != start {
		t.Errorf("takeback across a book move: %q, want the start position %q\n%s",
			got, start, u.Screen())
	}
	if got := u.Peek(ui.UIHCNT); got != 0 {
		t.Errorf("UIHCNT = %d after taking back both plies", got)
	}
}

// TestSideSwapBookkeeping: `S` cycles WHITE -> TWO PLAYERS -> BLACK -> WHITE
// without disturbing the position, and `N` resets the game. Swapping to a
// colour whose opponent is to move legitimately hands that move to the engine,
// so the board-unchanged assertion is made on a swap that does not: here the
// game is left with BLACK to move, and TWO PLAYERS -> BLACK is therefore the
// human's own turn either way.
func TestSideSwapBookkeeping(t *testing.T) {
	u := twoPlayer(t)
	for _, mv := range []string{"e2e4", "e7e5", "g1f3"} {
		mustEnter(t, u, mv)
	}
	if got := u.Screen().Text(12); !contains(got, "BLACK TO MOVE") {
		t.Fatalf("row 12 = %q", got)
	}
	fen, n := normFEN(u.FEN()), u.Peek(ui.UIHCNT)

	// TWO PLAYERS -> BLACK, with Black to move: still the human's turn, so
	// nothing may happen to the game at all.
	mustEnter(t, u, "s")
	if got := u.Screen().Text(0); !contains(got, "YOU ARE BLACK") {
		t.Errorf("row 0 = %q, want YOU ARE BLACK", got)
	}
	if got := normFEN(u.FEN()); got != fen {
		t.Errorf("S changed the position: %q, want %q", got, fen)
	}
	if got := u.Peek(ui.UIHCNT); got != n {
		t.Errorf("S changed UIHCNT to %d, want %d", got, n)
	}
	if got := u.Screen().Text(12); !contains(got, "BLACK TO MOVE") {
		t.Errorf("S changed the side to move: row 12 = %q", got)
	}

	// BLACK -> WHITE, with Black to move: this one DOES hand the move to the
	// engine, and exactly one ply may come of it.
	mustEnter(t, u, "s")
	if got := u.Screen().Text(0); !contains(got, "YOU ARE WHITE") {
		t.Errorf("row 0 = %q, want YOU ARE WHITE", got)
	}
	if got := u.Peek(ui.UIHCNT); got != n+1 {
		t.Errorf("swapping to White with Black to move added %d plies, want exactly 1",
			int(got)-int(n))
	}

	// N with the human on White: a fresh start position, human to move.
	mustEnter(t, u, "n")
	if got := u.Peek(ui.UIHCNT); got != 0 {
		t.Errorf("N left UIHCNT = %d", got)
	}
	if got := u.Peek(ui.UIRESULT); got != ui.ResNone {
		t.Errorf("N left UIRESULT = %s", ui.ResultName(got))
	}
	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := normFEN(u.FEN()), normFEN(p.FEN()); got != want {
		t.Errorf("N left %q, want the start position %q", got, want)
	}
	if got := u.Screen().Text(3); contains(got, "e2e4") {
		t.Errorf("N left the previous game in the move panel: %q", got)
	}
}

// UILSC0/UILSC1 (asm/ui.s M8VARS+$1A): the engine's last completed search
// score, and the only thing cmd_draw consults.
const uiLSC0 = 0xF71A

// TestDrawOfferIsNotAnsweredFromARetractedSearch. `D` is answered from
// UILSC, which is written by the engine's search. After a TAKEBACK the
// position that score describes is no longer on the board — and takebacks
// stack, so the score can be arbitrarily far from the position being offered
// a draw in. A takeback must therefore invalidate it, exactly as a book move
// already invalidates the think line ("a book move did no searching: the
// previous move's depth/score readout would be a lie").
func TestDrawOfferIsNotAnsweredFromARetractedSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real search")
	}
	u := boot(t)
	mustEnter(t, u, "l")
	mustEnter(t, u, "1")
	// The engine plays Black and is down a queen and a rook: its search will
	// score the position far below the -150 cp draw-acceptance threshold.
	if err := u.SetFEN("rnb1kbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"); err != nil {
		t.Fatal(err)
	}
	u.Poke(ui.UIHUMAN, 0)
	mustEnter(t, u, "e2e4")
	score := int16(uint16(u.Peek(uiLSC0)) | uint16(u.Peek(uiLSC0+1))<<8)
	if score >= -150 {
		t.Skipf("the engine scored the position %d cp; need < -150 to exercise the offer", score)
	}
	t.Logf("engine's last completed search: %d cp", score)

	mustEnter(t, u, "t")
	after := int16(uint16(u.Peek(uiLSC0)) | uint16(u.Peek(uiLSC0+1))<<8)
	if after < -150 {
		t.Errorf("takeback left UILSC = %d cp — the score of a position that is "+
			"no longer on the board. `D` will be answered from a retracted search.",
			after)
	}
	// With no completed search for the position now on the board, the offer
	// must be declined rather than answered from the withdrawn line.
	mustEnter(t, u, "d")
	if got := u.Peek(ui.UIRESULT); got == ui.ResAgreed {
		t.Errorf("the engine agreed a draw on the strength of a RETRACTED search:\n%s",
			u.Screen())
	}
	if got := u.Screen().Text(17); !contains(got, "DRAW DECLINED") {
		t.Errorf("row 17 = %q, want DRAW DECLINED", got)
	}
	// And a genuinely fresh search must still be able to accept.
	mustEnter(t, u, "n")
	if got := int16(uint16(u.Peek(uiLSC0)) | uint16(u.Peek(uiLSC0+1))<<8); got != 0 {
		t.Errorf("N left UILSC = %d, want 0", got)
	}
}
