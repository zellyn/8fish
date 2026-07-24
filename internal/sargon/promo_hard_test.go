package sargon

import (
	"fmt"
	"strings"
	"testing"
)

// Hard-mode promotion regression tests. The symmetric match harness
// (cmd/sargon-symmatch) drives Sargon in HARD mode (no Easy Mode, native
// pondering). A 300-game run showed that when the keyboard side plays a pawn
// PROMOTION (e.g. F7-F8Q / F7-F8R) Sargon never replied — RequestMove returned
// "no reply after CTRL-T" and the harness adjudicated a draw. These tests
// reproduce that path faithfully: a HEAVY midgame (so Sargon's ponder search is
// deep and it polls the keyboard only rarely), boot Hard mode + Infinite level,
// run a ponder window (Sargon mid-search), then inject a promotion via
// RequestMove and require a real on-screen reply (RAM is ponder scratch in Hard
// mode, so evidence is the ponder-immune move-list column, not anyPieceAt).

// heavyPromoFEN: full-army midgame, White to move, white passed pawn on f7 with
// f8 empty. f7-f8=Q is NOT check (black king on e6), so Sargon must find a normal
// reply — but with a full black army the ponder is deep, reproducing the rare-
// keyboard-poll hang. Colors match the log (8fish White promotes, Sargon Black).
const heavyPromoFEN = "rnbq2nr/pppp1Ppp/4k3/8/8/8/PPPPP1PP/RNBQKBNR w - -"

// bootPromoHard boots a fresh machine in HARD mode (no Easy Mode) + Infinite
// level, sets up the position, and returns the ready machine — matching how
// cmd/sargon-symmatch drives Sargon.
func bootPromoHard(t *testing.T, fen string) *Machine {
	t.Helper()
	m, err := NewMachine(dskPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.BootToPrompt(); err != nil {
		t.Fatal(err)
	}
	m.HardMode = true
	if err := m.InfiniteLevel(); err != nil {
		t.Fatal(err)
	}
	m.Run(1_000_000)
	if fen != "" {
		if err := m.SetupPosition(fen); err != nil {
			t.Fatalf("setup %q: %v", fen, err)
		}
	}
	return m
}

// ponderWindow advances Sargon by cycles, matching runSargonCycles in the
// harness (Sargon natively ponders during this window).
func ponderWindow(m *Machine, cycles uint64) {
	const chunk = 200_000
	start := m.Cycles()
	for m.Cycles()-start < cycles {
		if err := m.Run(chunk); err != nil {
			return
		}
	}
}

// isMoveToken reports whether a Sargon move-list token looks like a committed
// move (FROM-TO / capture / castle / en-passant) with a real destination.
func isMoveToken(tok string) bool {
	t := strings.ToUpper(strings.TrimSpace(tok))
	switch t {
	case "0-0", "O-O", "0-0-0", "O-O-O":
		return true
	}
	if strings.HasSuffix(t, "EP") {
		return true
	}
	if i := strings.IndexByte(t, '/'); i >= 0 {
		t = t[:i]
	}
	if len(t) < 5 || (t[2] != '-' && t[2] != 'X') {
		return false
	}
	return t[0:2] != t[3:5]
}

const hardPromoBudget = 30_000_000 // matches the real symmatch B=30M
const hardPonderWindow = 25_000_000

// TestHardPromotionQueenWhite: Hard-mode keyboard White queen-promotes on f8 in
// a heavy midgame. Before the fix Sargon never replied (RequestMove errored).
func TestHardPromotionQueenWhite(t *testing.T) {
	skipUnlessSlow(t)
	m := bootPromoHard(t, heavyPromoFEN)
	ponderWindow(m, hardPonderWindow) // Sargon ponders our move (Hard-mode state)
	res, err := m.RequestMove("F7-F8Q", hardPromoBudget)
	if err != nil {
		t.Fatalf("HARD queen promotion — no reply: %v\nrow5=%q\nscreen:\n%s", err, strings.TrimSpace(m.TextRow(5)), m.Screen())
	}
	tok := ourColumnToken(m)
	if !strings.Contains(tok, "/Q") {
		t.Errorf("our move token = %q, want a /Q promotion", tok)
	}
	if !isMoveToken(res.SargonText) {
		t.Errorf("Sargon did not reply with a real move after our HARD promotion (token=%q msg=%q)", res.SargonText, res.Message)
	}
	fmt.Printf("HARD queen-white: ourTok=%q sargon=%q think=%d msg=%q\n", tok, res.SargonText, res.ThinkCycles, res.Message)
}

// TestHardPromotionRookWhite: Hard-mode keyboard White UNDER-promotes to a Rook
// (the log showed both f7f8q and f7f8r).
func TestHardPromotionRookWhite(t *testing.T) {
	skipUnlessSlow(t)
	m := bootPromoHard(t, heavyPromoFEN)
	ponderWindow(m, hardPonderWindow)
	res, err := m.RequestMove("F7-F8R", hardPromoBudget)
	if err != nil {
		t.Fatalf("HARD rook under-promotion — no reply: %v\nrow5=%q\nscreen:\n%s", err, strings.TrimSpace(m.TextRow(5)), m.Screen())
	}
	tok := ourColumnToken(m)
	if !strings.Contains(tok, "/R") {
		t.Errorf("our move token = %q, want a /R under-promotion (not /Q)", tok)
	}
	if !isMoveToken(res.SargonText) {
		t.Errorf("Sargon did not reply with a real move after our HARD under-promotion (token=%q)", res.SargonText)
	}
	fmt.Printf("HARD rook-white: ourTok=%q sargon=%q think=%d\n", tok, res.SargonText, res.ThinkCycles)
}
