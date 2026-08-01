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

// paintListRow writes text straight into text page 1 for one move-list row, so
// a list state can be posed without playing the game that produces it.
func paintListRow(m *Machine, row int, text string) {
	base := textRowBase(row)
	for col := 0; col < 40; col++ {
		c := byte(' ')
		if col < len(text) {
			c = text[col]
		}
		m.Poke(base+uint16(col), c)
	}
}

// TestWrapNeedsTwoConsecutiveStrikes pins the ErrListWrapped guard against the
// mid-search repaint. Sargon transiently BLANKS its newest move-list rows, so a
// single poll that sees the number go backwards near the 127-move capacity is
// far more likely a blank frame than a real wrap — and a spurious wrap ends a
// game as a harness artifact, which is the class this driver exists to avoid.
// Polls are pollChunk (500K cycles) apart, longer than the ~167K repaint, so
// the guard requires the reading to REPEAT.
func TestWrapNeedsTwoConsecutiveStrikes(t *testing.T) {
	skipUnlessSlow(t) // needs the ROMs + disk image, though it steps no CPU
	m, err := NewMachine(dskPath)
	if err != nil {
		t.Fatal(err)
	}
	m.SargonWhite = true
	full := func() {
		paintListRow(m, 21, "      <5  A1-A2        H8-H7") // 125
		paintListRow(m, 22, "      <6  A2-A1        H7-H8") // 126
	}
	blanked := func() { // repaint in progress: the newest rows are gone
		paintListRow(m, 21, "")
		paintListRow(m, 22, "")
	}
	paintListRow(m, 20, "      <4  B1-A1        G8-H8") // 124
	full()
	if no, _, ok := m.LastSargonEntry(); !ok || no != 126 {
		t.Fatalf("posed list reads (%d,%v), want move 126", no, ok)
	}
	// A blank frame, once: NOT a wrap, and the caller keeps polling.
	blanked()
	if committed, err := m.sargonCommitted(126); committed || err != nil {
		t.Fatalf("single blank frame = (%v,%v), want (false,nil) — a repaint is not a wrap", committed, err)
	}
	// The repaint completes: the strike must be forgotten.
	full()
	if committed, err := m.sargonCommitted(126); committed || err != nil {
		t.Fatalf("repainted list = (%v,%v), want (false,nil)", committed, err)
	}
	blanked()
	if _, err := m.sargonCommitted(126); err != nil {
		t.Fatalf("first strike after a clean read = %v, want nil", err)
	}
	// Two consecutive backwards readings, half a million cycles apart in the
	// real loops: that is a wrap, and the game must be abandoned.
	if _, err := m.sargonCommitted(126); err != ErrListWrapped {
		t.Fatalf("second consecutive blank reading = %v, want ErrListWrapped", err)
	}
	// Below the capacity the guard is inert: a backwards number mid-game is a
	// repaint, always, however many times it is seen.
	blanked()
	paintListRow(m, 20, "")
	paintListRow(m, 19, "      50  B1-A1        G8-H8")
	for i := 0; i < 3; i++ {
		if committed, err := m.sargonCommitted(60); committed || err != nil {
			t.Fatalf("mid-game blank frame = (%v,%v), want (false,nil)", committed, err)
		}
	}
}
