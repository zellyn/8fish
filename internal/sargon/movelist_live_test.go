package sargon

import (
	"strings"
	"testing"
)

// TestSetupResetsMoveList checks that CTRL-A Analysis-Mode setup leaves Sargon's
// move list EMPTY and renumbering from 1, which is what makes the ply-indexed
// cross-check (CrossCheckHistory) meaningful for setboard/pool games too.
func TestSetupResetsMoveList(t *testing.T) {
	skipUnlessSlow(t)
	m := bootPromoHard(t, "")
	// Play a couple of moves so the list is non-empty first.
	m.SargonWhite = true
	if _, err := m.StartAsWhite(30_000_000); err != nil {
		t.Fatalf("StartAsWhite: %v", err)
	}
	if before := m.MoveList(); len(before) == 0 {
		t.Fatalf("expected a non-empty move list after the opening move:\n%s", m.ScreenDump("pre-setup"))
	}
	if err := m.SetupPosition("8/8/4k3/8/8/4K3/4P3/8 w - -"); err != nil {
		t.Fatalf("SetupPosition: %v", err)
	}
	got := m.MoveList()
	for _, r := range got {
		if r.White != "" || r.Black != "" {
			t.Errorf("move list not cleared by setup: %+v\n%s", got, m.ScreenDump("post-setup"))
			break
		}
	}
	if no, _, ok := m.LastSargonEntry(); ok {
		t.Errorf("LastSargonEntry after setup = (%d, ok) want no entry\n%s", no, m.ScreenDump("post-setup"))
	}
	t.Logf("post-setup list rows: %+v", got)
}

// TestScreenDumpFraming pins the greppable framing of the periodic capture.
func TestScreenDumpFraming(t *testing.T) {
	skipUnlessSlow(t)
	m := bootPromoHard(t, "")
	d := m.ScreenDump("probe")
	if !strings.HasPrefix(d, "SCREEN-DUMP-BEGIN probe sargonWhite=false\n") ||
		!strings.HasSuffix(d, "\nSCREEN-DUMP-END") {
		t.Errorf("bad framing:\n%s", d)
	}
	if n := strings.Count(d, "\n"); n != 25 {
		t.Errorf("got %d newlines, want 25 (24 rows + 2 markers)", n)
	}
	if !strings.Contains(d, "SARGON III") {
		t.Errorf("dump missing the title row:\n%s", d)
	}
}

// castleFEN: bare kings and rooks on their home squares, all four castling
// rights available (Sargon's CTRL-A editor grants castling from K/R placement).
const castleFEN = "r3k2r/8/8/8/8/8/8/R3K2R w KQkq -"

// TestCastlingEntryAndDisplay drives BOTH castles from the keyboard and checks
// what Sargon records. The manual (Chess Notation) says a castle is ENTERED by
// naming the King's FROM and TO squares but RECORDED as "0-0"/"0-0-0" — this
// pins both halves, plus the piece placement in RAM.
func TestCastlingEntryAndDisplay(t *testing.T) {
	skipUnlessSlow(t)
	for _, tc := range []struct {
		name, move, wantTok string
		kingTo, rookTo      string
	}{
		{"kingside", "E1-G1", "O-O", "g1", "f1"},
		{"queenside", "E1-C1", "O-O-O", "c1", "d1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := bootPromo(t, castleFEN)
			if _, err := m.RequestMove(tc.move, promoBudget); err != nil {
				t.Fatalf("RequestMove(%s): %v\n%s", tc.move, err, m.ScreenDump("castle"))
			}
			tok := ourColumnToken(m)
			if tok != tc.wantTok && tok != "0-0" && tok != "0-0-0" {
				t.Errorf("our move-list token = %q, want %q\n%s", tok, tc.wantTok, m.ScreenDump("castle"))
			}
			// The token must decode back to the king move we typed.
			_, _, kind, hasSq := TokenSquares(tok)
			if hasSq || kind != tc.wantTok {
				t.Errorf("TokenSquares(%q) = kind %q hasSquares %v, want kind %q", tok, kind, hasSq, tc.wantTok)
			}
			// And the BOARD must show a real castle: king and rook both moved.
			kt, _ := ParseSquare(tc.kingTo)
			rt, _ := ParseSquare(tc.rookTo)
			if !m.anyPieceAt(kt, false) || !m.anyPieceAt(rt, false) {
				t.Errorf("after %s: white piece on %s=%v, on %s=%v (want both)\n%s",
					tc.move, tc.kingTo, m.anyPieceAt(kt, false), tc.rookTo, m.anyPieceAt(rt, false),
					m.ScreenDump("castle"))
			}
			t.Logf("%s: entered %q -> recorded %q, king %s rook %s", tc.name, tc.move, tok, tc.kingTo, tc.rookTo)
		})
	}
}
