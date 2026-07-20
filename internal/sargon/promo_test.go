package sargon

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Promotion regression tests. Two gauntlet games died because the driver never
// answered Sargon's "ENTER PROMOTED PIECE" prompt when WE promoted: it typed
// FROM-TO+RETURN and then waited for the pawn to reach the last rank, but Sargon
// blocks on the prompt until a piece is chosen. These tests exercise our
// promotion entry (queen + under-promotion, both colours, plus a promotion that
// gives check) and Sargon's own promotion decode, proving each with RAM/screen
// evidence. Gated on SARGON_SLOW (drives the emulator).

const promoBudget = 4_000_000 // Infinite level + CTRL-T after this many cycles

// bootPromo boots a fresh machine, enables Easy Mode and Infinite level, sets up
// the position, and returns the ready machine.
func bootPromo(t *testing.T, fen string) *Machine {
	t.Helper()
	m, err := NewMachine(dskPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.BootToPrompt(); err != nil {
		t.Fatal(err)
	}
	if err := m.EasyMode(); err != nil {
		t.Fatal(err)
	}
	m.Run(1_000_000)
	if err := m.SetupPosition(fen); err != nil {
		t.Fatalf("setup %q: %v", fen, err)
	}
	if err := m.InfiniteLevel(); err != nil {
		t.Fatal(err)
	}
	return m
}

// ourColumnToken returns our (the keyboard opponent's) latest move-list token.
// The opponent plays Black iff Sargon is White; Black's moves are in the right
// column (cols 22-30), White's in the left (cols 10-18).
func ourColumnToken(m *Machine) string {
	return m.scrapeMoveColumn(m.SargonWhite)
}

func skipUnlessSlow(t *testing.T) {
	if os.Getenv("SARGON_SLOW") == "" {
		t.Skip("set SARGON_SLOW=1 (drives the emulator; ~seconds each)")
	}
}

// TestPromotionQueenWhite: keyboard White promotes to a Queen (Sargon Black).
func TestPromotionQueenWhite(t *testing.T) {
	skipUnlessSlow(t)
	// White pawn b7 promotes on b8; black king on e6 (b8=Q is not check); both
	// sides have replies.
	m := bootPromo(t, "8/1P6/4k3/8/8/8/8/6K1 w - -")
	res, err := m.RequestMove("B7-B8Q", promoBudget)
	if err != nil {
		t.Fatalf("queen promotion rejected: %v\nscreen:\n%s", err, m.Screen())
	}
	// RAM evidence: our pawn slot now sits on b8, and b7 is vacated.
	b7, _ := ParseSquare("b7")
	b8, _ := ParseSquare("b8")
	if m.anyPieceAt(b7, false) {
		t.Errorf("b7 still occupied after promotion; board:\n%s", res.Board.Board())
	}
	if !m.anyPieceAt(b8, false) {
		t.Errorf("no white piece on b8 after promotion; board:\n%s", res.Board.Board())
	}
	tok := ourColumnToken(m)
	if !strings.Contains(tok, "/Q") {
		t.Errorf("our move token = %q, want a /Q promotion", tok)
	}
	// Sargon must have replied.
	if !res.SargonMove.From.Valid() || res.SargonMove.From == res.SargonMove.To {
		t.Errorf("Sargon did not reply after our promotion (token=%q)", res.SargonText)
	}
	fmt.Printf("queen-white: ourTok=%q sargon=%q msg=%q\n", tok, res.SargonText, res.Message)
}

// TestPromotionKnightWhite: keyboard White UNDER-promotes to a Knight. Proving
// under-promotion is the sharpest evidence the prompt is answered correctly:
// the old code always got a Queen (the prompt default).
func TestPromotionKnightWhite(t *testing.T) {
	skipUnlessSlow(t)
	m := bootPromo(t, "8/1P6/4k3/8/8/8/8/6K1 w - -")
	res, err := m.RequestMove("B7-B8N", promoBudget)
	if err != nil {
		t.Fatalf("knight under-promotion rejected: %v\nscreen:\n%s", err, m.Screen())
	}
	b8, _ := ParseSquare("b8")
	if !m.anyPieceAt(b8, false) {
		t.Errorf("no white piece on b8 after under-promotion; board:\n%s", res.Board.Board())
	}
	tok := ourColumnToken(m)
	if !strings.Contains(tok, "/N") {
		t.Errorf("our move token = %q, want a /N under-promotion (not /Q)", tok)
	}
	fmt.Printf("knight-white: ourTok=%q sargon=%q\n", tok, res.SargonText)
}

// TestPromotionQueenCheckWhite: a queen promotion that delivers CHECK. This
// mirrors the f2f1n gauntlet failure, where the promotion put Sargon in check
// and the message line read "CHECK" while the move was (wrongly) reported not
// accepted.
func TestPromotionQueenCheckWhite(t *testing.T) {
	skipUnlessSlow(t)
	// b8=Q gives check to a black king on g8 along the 8th rank.
	m := bootPromo(t, "6k1/1P6/8/8/8/8/8/6K1 w - -")
	res, err := m.RequestMove("B7-B8Q", promoBudget)
	if err != nil {
		t.Fatalf("checking promotion rejected: %v\nscreen:\n%s", err, m.Screen())
	}
	b8, _ := ParseSquare("b8")
	if !m.anyPieceAt(b8, false) {
		t.Errorf("no white piece on b8 after checking promotion; board:\n%s", res.Board.Board())
	}
	// Sargon (in check) must have replied by moving its king out of check.
	if !res.SargonMove.From.Valid() || res.SargonMove.From == res.SargonMove.To {
		t.Errorf("Sargon did not reply to checking promotion (token=%q msg=%q)",
			res.SargonText, res.Message)
	}
	fmt.Printf("queen-check-white: ourTok=%q sargon=%q msg=%q\n",
		ourColumnToken(m), res.SargonText, res.Message)
}

// TestPromotionBlack: keyboard Black promotes (Sargon plays White). This is the
// exact configuration of the c2c1q / f2f1n failures (promotions on rank 1).
func TestPromotionBlack(t *testing.T) {
	skipUnlessSlow(t)
	// White to move; Sargon (White) makes a harmless g-side move, then Black
	// promotes on b1. White has nothing that can stop or capture the b-pawn.
	m := bootPromo(t, "1k6/8/8/8/6P1/6K1/1p6/8 w - -")
	if _, err := m.StartAsWhite(promoBudget); err != nil {
		t.Fatalf("StartAsWhite: %v\nscreen:\n%s", err, m.Screen())
	}
	if !m.SargonWhite {
		t.Fatal("SargonWhite not set after StartAsWhite")
	}
	res, err := m.RequestMove("B2-B1Q", promoBudget)
	if err != nil {
		t.Fatalf("black queen promotion rejected: %v\nscreen:\n%s", err, m.Screen())
	}
	// RAM evidence: a black piece now sits on b1, b2 vacated.
	b1, _ := ParseSquare("b1")
	b2, _ := ParseSquare("b2")
	if m.anyPieceAt(b2, true) {
		t.Errorf("b2 still occupied after black promotion; board:\n%s", res.Board.Board())
	}
	if !m.anyPieceAt(b1, true) {
		t.Errorf("no black piece on b1 after promotion; board:\n%s", res.Board.Board())
	}
	tok := ourColumnToken(m)
	if !strings.Contains(tok, "/Q") {
		t.Errorf("our (Black) move token = %q, want a /Q promotion", tok)
	}
	if !res.SargonMove.From.Valid() || res.SargonMove.From == res.SargonMove.To {
		t.Errorf("Sargon (White) did not reply after our promotion (token=%q)", res.SargonText)
	}
	fmt.Printf("queen-black: ourTok=%q sargon=%q\n", tok, res.SargonText)
}

// TestPromotionBlackUnderRook: keyboard Black UNDER-promotes to a Rook.
func TestPromotionBlackUnderRook(t *testing.T) {
	skipUnlessSlow(t)
	m := bootPromo(t, "1k6/8/8/8/6P1/6K1/1p6/8 w - -")
	if _, err := m.StartAsWhite(promoBudget); err != nil {
		t.Fatalf("StartAsWhite: %v\nscreen:\n%s", err, m.Screen())
	}
	res, err := m.RequestMove("B2-B1R", promoBudget)
	if err != nil {
		t.Fatalf("black rook under-promotion rejected: %v\nscreen:\n%s", err, m.Screen())
	}
	b1, _ := ParseSquare("b1")
	if !m.anyPieceAt(b1, true) {
		t.Errorf("no black piece on b1 after under-promotion; board:\n%s", res.Board.Board())
	}
	tok := ourColumnToken(m)
	if !strings.Contains(tok, "/R") {
		t.Errorf("our (Black) move token = %q, want a /R under-promotion (not /Q)", tok)
	}
	fmt.Printf("rook-black: ourTok=%q sargon=%q\n", tok, res.SargonText)
}

// TestSargonPromotionDecode: SARGON itself promotes; verify the driver decodes
// it into correct coordinate notation ("b1q") and the RAM board shows Sargon's
// pawn slot on the promotion square.
func TestSargonPromotionDecode(t *testing.T) {
	skipUnlessSlow(t)
	// White (keyboard) to move with only a quiet king shuffle available; Black
	// (Sargon) has a lone pawn on b2 that promotes for a decisive queen.
	m := bootPromo(t, "8/8/8/8/1k6/8/1p5P/7K w - -")
	// Keyboard White plays a harmless king move; then Sargon (Black) promotes.
	res, err := m.RequestMove("H1-G1", promoBudget)
	if err != nil {
		t.Fatalf("setup move rejected: %v\nscreen:\n%s", err, m.Screen())
	}
	// Sargon's reply should be a promotion to a queen on b1.
	coord := res.SargonMove.From.Algebraic() + res.SargonMove.To.Algebraic()
	if res.SargonMove.To.Algebraic() != "b1" || res.SargonMove.From.Algebraic() != "b2" {
		t.Fatalf("expected Sargon to promote b2-b1, got %s (token=%q)\nboard:\n%s",
			coord, res.SargonText, res.Board.Board())
	}
	// The on-screen token must carry the promotion marker so the adapter emits
	// coordinate "b1q".
	if !strings.Contains(res.SargonText, "/") {
		t.Errorf("Sargon promotion token %q lacks a '/piece' promotion marker", res.SargonText)
	}
	// RAM evidence: a black pawn slot occupies b1.
	b1, _ := ParseSquare("b1")
	if !m.anyPieceAt(b1, true) {
		t.Errorf("no black piece on b1 after Sargon promotion; board:\n%s", res.Board.Board())
	}
	fmt.Printf("sargon-decode: token=%q ram=%s board:\n%s", res.SargonText, coord, res.Board.Board())
}
