package ui_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/ui"
)

// normFEN drops a FEN's fullmove number, which the UI does not track.
func normFEN(fen string) string {
	f := strings.Fields(fen)
	if len(f) > 5 {
		f = f[:5]
	}
	return strings.Join(f, " ")
}

// twoPlayer boots the UI into referee mode, where it validates and displays
// but never searches — so a test can drive both sides.
func twoPlayer(t *testing.T) *ui.Machine {
	t.Helper()
	u := boot(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	return u
}

// TestMoveEntry plays a normal opening through the UI's typed-coordinate
// entry and checks that the position, the move panel and the status line all
// follow.
func TestMoveEntry(t *testing.T) {
	u := twoPlayer(t)
	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	moves := []string{"e2e4", "e7e5", "g1f3", "b8c6", "f1b5", "a7a6"}
	for _, mv := range moves {
		mustEnter(t, u, mv)
		m, err := refchess.ParseMove(mv)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Make(m); err != nil {
			t.Fatal(err)
		}
		if got, want := normFEN(u.FEN()), normFEN(p.FEN()); got != want {
			t.Fatalf("after %s: UI position %q, refchess %q\n%s", mv, got, want, u.Screen())
		}
	}
	s := u.Screen()
	t.Logf("after 1.e4 e5 2.Nf3 Nc6 3.Bb5 a6, typed into the UI:\n%s", s)

	if got := u.History(); strings.Join(got, " ") != strings.Join(moves, " ") {
		t.Errorf("history = %v, want %v", got, moves)
	}
	for _, tc := range []struct {
		row  int
		want string
	}{
		{3, " 1 e2e4 e7e5"},
		{4, " 2 g1f3 b8c6"},
		{5, " 3 f1b5 a7a6"},
		{12, "WHITE TO MOVE"},
		{0, "TWO PLAYERS"},
	} {
		if got := s.Text(tc.row); !contains(got, tc.want) {
			t.Errorf("row %d = %q, want it to contain %q", tc.row, got, tc.want)
		}
	}
}

// TestSpecialMoves is the "the UI contains no chess rules" proof: castling,
// en passant and both promotion spellings are typed as ordinary coordinates
// and come out right because the ENGINE's generator emitted them.
func TestSpecialMoves(t *testing.T) {
	for _, tc := range []struct {
		name string
		fen  string
		move string
		want string // resulting FEN (first five fields)
	}{
		{
			"white kingside castling",
			"r1bqk2r/pppp1ppp/2n2n2/2b1p3/2B1P3/2N2N2/PPPP1PPP/R1BQK2R w KQkq - 4 5",
			"e1g1",
			"r1bqk2r/pppp1ppp/2n2n2/2b1p3/2B1P3/2N2N2/PPPP1PPP/R1BQ1RK1 b kq - 5",
		},
		{
			"black queenside castling",
			"r3kbnr/pppqpppp/2np4/8/8/2NP4/PPPQPPPP/R3KBNR b KQkq - 6 5",
			"e8c8",
			"2kr1bnr/pppqpppp/2np4/8/8/2NP4/PPPQPPPP/R3KBNR w KQ - 7",
		},
		{
			"en passant capture",
			"rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3",
			"e5f6",
			"rnbqkbnr/ppp1p1pp/5P2/3p4/8/8/PPPP1PPP/RNBQKBNR b KQkq - 0",
		},
		{
			"promotion to queen, spelled out",
			"8/4P3/8/8/8/8/8/K6k w - - 0 1",
			"e7e8q",
			"4Q3/8/8/8/8/8/8/K6k b - - 0",
		},
		{
			"underpromotion to knight",
			"8/4P3/8/8/8/8/8/K6k w - - 0 1",
			"e7e8n",
			"4N3/8/8/8/8/8/8/K6k b - - 0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := twoPlayer(t)
			if err := u.SetFEN(tc.fen); err != nil {
				t.Fatal(err)
			}
			mustEnter(t, u, tc.move)
			if got := normFEN(u.FEN()); got != tc.want {
				t.Errorf("after %s: %q, want %q\n%s", tc.move, got, tc.want, u.Screen())
			}
			if n := u.Peek(ui.UIHCNT); n != 1 {
				t.Errorf("UIHCNT = %d, want 1 (the move was rejected)", n)
			}
		})
	}
}

// TestPromotionPrompt: typing a promotion without its fifth character makes
// the UI ask, and the answer completes the move.
func TestPromotionPrompt(t *testing.T) {
	u := twoPlayer(t)
	if err := u.SetFEN("8/4P3/8/8/8/8/8/K6k w - - 0 1"); err != nil {
		t.Fatal(err)
	}
	// Type the move but do NOT press return yet, so we can see the prompt
	// appear after RETURN and before the piece key.
	if err := u.Enter("e7e8"); err != nil {
		t.Fatal(err)
	}
	if got := u.Screen().Text(17); !contains(got, "PROMOTE TO") {
		t.Fatalf("row 17 = %q, want the promotion prompt\n%s", got, u.Screen())
	}
	if err := u.Key('r'); err != nil {
		t.Fatal(err)
	}
	if got, want := normFEN(u.FEN()), "4R3/8/8/8/8/8/8/K6k b - - 0"; got != want {
		t.Errorf("after e7e8 + R: %q, want %q\n%s", got, want, u.Screen())
	}
}

// TestLegalityAgainstRefchess is the validation gate: for each position,
// EVERY move refchess calls legal must be accepted and produce refchess's
// resulting position, and a sample of illegal coordinate strings must be
// rejected with a clear message and NO state change.
func TestLegalityAgainstRefchess(t *testing.T) {
	fens := []string{
		refchess.StartFEN,
		// Ruy Lopez middlegame: castling both ways, pins, everything.
		"r1bqk2r/pppp1ppp/2n2n2/2b1p3/2B1P3/2N2N2/PPPP1PPP/R1BQK2R w KQkq - 4 5",
		// En-passant window.
		"rnbqkbnr/ppp1p1pp/8/3pPp2/8/8/PPPP1PPP/RNBQKBNR w KQkq f6 0 3",
		// A pinned knight: b8 cannot move without exposing the king.
		"rnbqkbnr/pppp1ppp/8/4p3/6P1/5P2/PPPPP2P/RNBQKBNR b KQkq g3 0 2",
		// In check (a bishop pin on the king): only evasions are legal.
		"rnbqkbnr/ppp2ppp/8/1B1pp3/4P3/8/PPPP1PPP/RNBQK1NR b KQkq - 1 3",
		// Promotion race.
		"8/4P3/8/8/8/8/8/K6k w - - 0 1",
	}
	u := twoPlayer(t)
	rnd := rand.New(rand.NewSource(20260728))
	totalLegal, totalIllegal := 0, 0

	for _, fen := range fens {
		p, err := refchess.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		legal := p.LegalMoves()
		legalSet := map[string]bool{}
		for _, m := range legal {
			legalSet[m.String()] = true
		}

		for _, m := range legal {
			if err := u.SetFEN(fen); err != nil {
				t.Fatal(err)
			}
			mv := m.String()
			mustEnter(t, u, mv)
			if n := u.Peek(ui.UIHCNT); n != 1 {
				t.Errorf("%s: legal move %s was REJECTED (%q)", fen, mv,
					strings.TrimSpace(u.Screen().Text(17)))
				continue
			}
			after := p.Copy()
			if err := after.Make(m); err != nil {
				t.Fatal(err)
			}
			if got, want := normFEN(u.FEN()), normFEN(after.FEN()); got != want {
				t.Errorf("%s + %s: UI %q, refchess %q", fen, mv, got, want)
			}
			totalLegal++
		}

		// Illegal: random from/to pairs that refchess does not list.
		for tries, n := 0, 0; n < 40 && tries < 4000; tries++ {
			mv := fmt.Sprintf("%c%c%c%c",
				'a'+rnd.Intn(8), '1'+rnd.Intn(8), 'a'+rnd.Intn(8), '1'+rnd.Intn(8))
			if legalSet[mv] || mv[0:2] == mv[2:4] {
				continue
			}
			n++
			if err := u.SetFEN(fen); err != nil {
				t.Fatal(err)
			}
			mustEnter(t, u, mv)
			if cnt := u.Peek(ui.UIHCNT); cnt != 0 {
				t.Errorf("%s: ILLEGAL move %s was accepted", fen, mv)
				continue
			}
			if got, want := normFEN(u.FEN()), normFEN(fen); got != want {
				t.Errorf("%s: rejected move %s changed the position to %q", fen, mv, got)
			}
			if got := u.Screen().Text(17); !contains(got, "ILLEGAL MOVE") {
				t.Errorf("%s: rejecting %s said %q, want ILLEGAL MOVE", fen, mv, strings.TrimSpace(got))
			}
			totalIllegal++
		}
	}
	t.Logf("accepted %d legal moves, rejected %d illegal ones, all cross-checked against refchess",
		totalLegal, totalIllegal)
}

// TestBadInput: garbage must produce a clear message and never touch the
// position.
func TestBadInput(t *testing.T) {
	u := twoPlayer(t)
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"e2e", "TYPE A MOVE"},
		{"e2e44", "TYPE A MOVE"},
		{"z9z9", "TYPE A MOVE"},
		{"a1a2", "ILLEGAL MOVE"},
		{"e2e4x", "TYPE A MOVE"},
		{"x", "UNKNOWN COMMAND"},
	} {
		mustEnter(t, u, tc.in)
		if got := u.Screen().Text(17); !contains(got, tc.want) {
			t.Errorf("input %q -> row 17 %q, want it to contain %q", tc.in, strings.TrimSpace(got), tc.want)
		}
		if n := u.Peek(ui.UIHCNT); n != 0 {
			t.Errorf("input %q changed the game (UIHCNT = %d)", tc.in, n)
		}
	}
}

// TestTerminations drives each way a game can really end and asserts what the
// screen says.
func TestTerminations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fen    string
		moves  []string
		result byte
		rows   map[int]string
	}{
		{
			name:   "fool's mate from the opening",
			moves:  []string{"f2f3", "e7e5", "g2g4", "d8h4"},
			result: ui.ResMate,
			rows:   map[int]string{12: "CHECKMATE", 17: "BLACK WINS"},
		},
		{
			name:   "back-rank mate",
			fen:    "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1",
			moves:  []string{"a1a8"},
			result: ui.ResMate,
			rows:   map[int]string{12: "CHECKMATE", 17: "WHITE WINS"},
		},
		{
			name:   "stalemate",
			fen:    "7k/3Q4/8/8/8/8/8/6K1 w - - 0 1",
			moves:  []string{"d7f7"},
			result: ui.ResStale,
			rows:   map[int]string{12: "STALEMATE", 17: "GAME DRAWN"},
		},
		{
			name:   "50-move rule",
			fen:    "8/8/4k3/8/8/4K3/8/6R1 w - - 99 60",
			moves:  []string{"g1g2"},
			result: ui.Res50,
			rows:   map[int]string{12: "DRAW: 50 MOVES", 17: "GAME DRAWN"},
		},
		{
			name:  "threefold repetition",
			fen:   "4k3/8/8/8/8/8/8/R3K2R w - - 0 1",
			moves: []string{"a1a2", "e8e7", "a2a1", "e7e8", "a1a2", "e8e7", "a2a1", "e7e8"},
			// The start position recurs after ply 4 and again after ply 8.
			result: ui.ResRep,
			rows:   map[int]string{12: "DRAW: REPETITION", 17: "GAME DRAWN"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := twoPlayer(t)
			if tc.fen != "" {
				if err := u.SetFEN(tc.fen); err != nil {
					t.Fatal(err)
				}
			}
			for _, mv := range tc.moves {
				mustEnter(t, u, mv)
			}
			s := u.Screen()
			if got := u.Peek(ui.UIRESULT); got != tc.result {
				t.Errorf("UIRESULT = %s, want %s\n%s",
					ui.ResultName(got), ui.ResultName(tc.result), s)
			}
			for row, want := range tc.rows {
				if got := s.Text(row); !contains(got, want) {
					t.Errorf("row %d = %q, want it to contain %q", row, strings.TrimSpace(got), want)
				}
			}
			t.Logf("%s:\n%s", tc.name, s)
			// A finished game accepts no more moves.
			mustEnter(t, u, "e2e4")
			if got := u.Screen().Text(17); !contains(got, "GAME OVER") {
				t.Errorf("a move after the end said %q, want GAME OVER", strings.TrimSpace(got))
			}
		})
	}
}

// TestTakeback: the takeback replays the game from the start position rather
// than keeping an undo journal, so the restored position must be
// byte-identical to the one before the move.
func TestTakeback(t *testing.T) {
	u := twoPlayer(t)
	moves := []string{"e2e4", "e7e5", "g1f3", "b8c6"}
	var fens []string
	for _, mv := range moves {
		fens = append(fens, u.FEN())
		mustEnter(t, u, mv)
	}
	for i := len(moves) - 1; i >= 0; i-- {
		mustEnter(t, u, "t")
		if got, want := u.FEN(), fens[i]; got != want {
			t.Fatalf("takeback to ply %d: %q, want %q\n%s", i, got, want, u.Screen())
		}
		if got := int(u.Peek(ui.UIHCNT)); got != i {
			t.Fatalf("takeback to ply %d: UIHCNT = %d", i, got)
		}
	}
	mustEnter(t, u, "t")
	if got := u.Screen().Text(17); !contains(got, "NOTHING TO TAKE BACK") {
		t.Errorf("takeback at the start said %q", strings.TrimSpace(got))
	}
}

// TestCommands exercises the rest of the single-key command set.
func TestCommands(t *testing.T) {
	u := boot(t)
	// L sets the level, and the title bar shows it.
	mustEnter(t, u, "l")
	if got := u.Screen().Text(17); !contains(got, "LEVEL?") {
		t.Fatalf("row 17 = %q, want the level prompt", strings.TrimSpace(got))
	}
	if err := u.Key('2'); err != nil {
		t.Fatal(err)
	}
	if got := u.Screen().Text(0); !contains(got, "LEVEL 2") {
		t.Errorf("title = %q, want LEVEL 2", got)
	}
	// S cycles WHITE -> TWO PLAYERS -> BLACK -> WHITE, and the order is the
	// point: the FIRST press is the harmless one -- referee mode, where
	// nothing searches -- so one press of an unlabelled key can never commit
	// a move. Handing a colour to the engine takes a second, deliberate press.
	mustEnter(t, u, "s")
	if got := u.Screen().Text(0); !contains(got, "TWO PLAYERS") {
		t.Errorf("title = %q, want TWO PLAYERS on the first S", got)
	}
	if n := u.Peek(ui.UIHCNT); n != 0 {
		t.Errorf("the first S committed a move (UIHCNT = %d); it selects referee mode, "+
			"where nothing searches", n)
	}
	if got := u.Screen().Text(17); !contains(got, "TWO PLAYERS") {
		t.Errorf("row 17 = %q; landing in a mode must announce it, or the state change "+
			"is thirteen characters inside an inverse title bar", strings.TrimSpace(got))
	}
	// The second press goes to Black, which DOES hand the move to the engine;
	// it replies at once out of the book.
	mustEnter(t, u, "s")
	if got := u.Screen().Text(0); !contains(got, "YOU ARE BLACK") {
		t.Errorf("title = %q, want YOU ARE BLACK", got)
	}
	if n := u.Peek(ui.UIHCNT); n != 1 {
		t.Errorf("after swapping to Black the engine should have moved; UIHCNT = %d", n)
	}
	t.Logf("engine opened with %v; screen:\n%s", u.History(), u.Screen())
	// R resigns.
	mustEnter(t, u, "r")
	if got := u.Peek(ui.UIRESULT); got != ui.ResResign {
		t.Errorf("after R: %s, want resigned", ui.ResultName(got))
	}
	if got := u.Screen().Text(12); !contains(got, "RESIGNED") {
		t.Errorf("row 12 = %q, want RESIGNED", strings.TrimSpace(got))
	}
	// N starts over. The human is still Black here, so the engine takes the
	// new game's first move immediately — exactly one ply.
	mustEnter(t, u, "n")
	if got := u.Peek(ui.UIRESULT); got != ui.ResNone {
		t.Errorf("after N: %s, want playing", ui.ResultName(got))
	}
	if got := u.History(); len(got) != 1 {
		t.Errorf("after N the engine should have opened; history = %v", got)
	}
	// ? explains the move syntax.
	mustEnter(t, u, "?")
	if got := u.Screen().Text(17); !contains(got, "e2e4") {
		t.Errorf("? said %q", strings.TrimSpace(got))
	}
}

// TestDrawOffer: "D" is a genuine offer, not a claim (a threefold repetition
// or the 50-move rule is adjudicated automatically). The engine accepts only
// when its last completed search said it was materially worse off.
func TestDrawOffer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		score int16
		want  byte
		row   string
	}{
		{"engine is fine, declines", 0, ui.ResNone, "DRAW DECLINED"},
		{"engine is a pawn down, declines", -100, ui.ResNone, "DRAW DECLINED"},
		{"engine is losing, accepts", -300, ui.ResAgreed, "GAME DRAWN"},
		{"engine is winning, declines", +900, ui.ResNone, "DRAW DECLINED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := boot(t)
			const uilsc = 0xF71A // UILSC0/1 in the M8VARS block
			u.Poke(uilsc, byte(uint16(tc.score)))
			u.Poke(uilsc+1, byte(uint16(tc.score)>>8))
			mustEnter(t, u, "d")
			if got := u.Peek(ui.UIRESULT); got != tc.want {
				t.Errorf("score %d: %s, want %s", tc.score,
					ui.ResultName(got), ui.ResultName(tc.want))
			}
			if got := u.Screen().Text(17); !contains(got, tc.row) {
				t.Errorf("score %d: row 17 = %q, want %q", tc.score, strings.TrimSpace(got), tc.row)
			}
		})
	}
}
