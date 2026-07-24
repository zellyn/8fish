package sargon

import (
	"fmt"
	"strings"
	"testing"
)

// g10PreF7F8R is the exact position from the 300-game symmetric run's game 10,
// just before 8fish plays the F7-F8R that hung the harness: White Kf6 + Pf7,
// Black Kh7, White to move. f7-f8=Q would STALEMATE Black, so 8fish
// under-promotes to a Rook (f8=R), after which Black has EXACTLY ONE legal move
// (H7-H6). Because the reply is forced, Sargon plays it the instant the promotion
// registers — before RequestMove's old commit baseline was taken — so the harness
// waited forever and adjudicated a draw. This is the regression test for that fix.
const g10PreF7F8R = "8/5P1k/5K2/8/8/8/8/8 w - -"

// TestHardPromotionForcedReply reproduces the exact game-10 failure: RequestMove
// on the stalemate-avoiding under-promotion F7-F8R, whose only legal reply
// (H7-H6) Sargon commits instantly. Before the RequestMove baseline fix this
// returned "no reply after CTRL-T"; now Sargon's forced reply is detected.
func TestHardPromotionForcedReply(t *testing.T) {
	skipUnlessSlow(t)
	m := bootPromoHard(t, g10PreF7F8R)
	ponderWindow(m, hardPonderWindow) // Sargon ponders (Hard-mode state)
	res, err := m.RequestMove("F7-F8R", hardPromoBudget)
	if err != nil {
		t.Fatalf("g10 F7-F8R still hangs: %v\nrow5=%q\nscreen:\n%s", err, strings.TrimSpace(m.TextRow(5)), m.Screen())
	}
	ourTok := ourColumnToken(m)
	if !strings.Contains(ourTok, "/R") {
		t.Errorf("our move token = %q, want the /R under-promotion", ourTok)
	}
	// The only legal Black reply is H7-H6.
	if got := strings.ToUpper(strings.TrimSpace(res.SargonText)); got != "H7-H6" {
		t.Errorf("Sargon reply = %q, want the forced H7-H6", got)
	}
	if !isMoveToken(res.SargonText) {
		t.Errorf("Sargon did not reply with a real move (token=%q)", res.SargonText)
	}
	fmt.Printf("g10 forced-reply: ourTok=%q sargon=%q think=%d msg=%q\n", ourTok, res.SargonText, res.ThinkCycles, res.Message)
}
