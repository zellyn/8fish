package sargon

import "testing"

// Regression tests for the move-list parse and the repaint-immune commit
// signal. All screen rows below are VERBATIM captures from the emulator (logged
// SCREEN-DUMPs), padded to 40 columns.

func pad(s string) string {
	for len(s) < 40 {
		s += " "
	}
	return s
}

func rows(ss ...string) []string {
	out := make([]string, 0, 14)
	for _, s := range ss {
		out = append(out, pad(s))
	}
	for len(out) < 14 {
		out = append(out, pad(""))
	}
	return out
}

// Verbatim from the 2026-07 standard-start match, game 9's adjudication dump
// (Sargon White): the list Sargon showed when the harness read a stale "D1XD4".
var g9Rows = rows(
	"       1  E2-E4        E7-E5",
	"       2  F1-C4        G8-F6",
	"       3  D2-D4        E5XD4",
	"       4  G1-F3        F6XE4",
	"       5  D1XD4        D8-E7",
	"       6",
)

// Verbatim from a scrolled, late-game dump: promotions, under-promotion and
// check markers, plus the widest tokens Sargon prints.
var scrolledRows = rows(
	"      51  B2-C2        F2-F1/Q",
	"      52  B5XF1        H1XF1",
	"      53  C2-B3        E3-E2",
	"      54  B3-A4        D4-C4",
	"      55  B7-B8/N      F1-A1+",
	"      56",
)

func TestParseMoveList(t *testing.T) {
	got := parseMoveList(g9Rows)
	want := []MoveRow{
		{1, "E2-E4", "E7-E5"},
		{2, "F1-C4", "G8-F6"},
		{3, "D2-D4", "E5XD4"},
		{4, "G1-F3", "F6XE4"},
		{5, "D1XD4", "D8-E7"},
		{6, "", ""},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The widest tokens Sargon prints must survive the scrape windows: an
// under-promotion in the White column ("B7-B8/N") and a promotion plus check in
// the Black column ("F2-F1/Q", "F1-A1+"). The Black window was [22,30) before
// the 2026-07 audit, which truncated an 8-character Black token.
func TestParseMoveListWideTokens(t *testing.T) {
	got := parseMoveList(scrolledRows)
	want := map[int][2]string{
		51: {"B2-C2", "F2-F1/Q"},
		55: {"B7-B8/N", "F1-A1+"},
	}
	for _, r := range got {
		if w, ok := want[r.No]; ok {
			if r.White != w[0] || r.Black != w[1] {
				t.Errorf("move %d = %q/%q, want %q/%q", r.No, r.White, r.Black, w[0], w[1])
			}
			delete(want, r.No)
		}
	}
	if len(want) != 0 {
		t.Errorf("rows not parsed: %+v", want)
	}
	// An 8-character Black token (promotion + check) must not be truncated.
	wide := parseMoveList(rows("      55  B7-B8/N+     F2-F1/Q+"))
	if len(wide) != 1 || wide[0].Black != "F2-F1/Q+" || wide[0].White != "B7-B8/N+" {
		t.Errorf("wide row = %+v, want white=%q black=%q", wide, "B7-B8/N+", "F2-F1/Q+")
	}
}

// Castling is printed one column right of the other tokens (" O-O "), verbatim
// from game 266's dump.
func TestParseMoveListCastling(t *testing.T) {
	got := parseMoveList(rows("       4   O-O         E7-E6"))
	if len(got) != 1 || got[0].White != "O-O" || got[0].Black != "E7-E6" {
		t.Fatalf("got %+v, want {4 O-O E7-E6}", got)
	}
}

// THE DESYNC REGRESSION. While searching, Sargon transiently BLANKS the bottom
// move-list rows and repaints them ~167K cycles later. The old commit detector
// compared the column TEXT, so the blank frame read as "Sargon moved" and the
// caller then took the bottom-most SURVIVING token — Sargon's PREVIOUS move.
// The move NUMBER must not advance across a repaint.
func TestRepaintBlankIsNotACommit(t *testing.T) {
	// Verbatim before/after from TestRepaintWatch (seed 7, Sargon White):
	// rows 11 and 12 blank, then come back identical.
	full := rows(
		"       1  E2-E4        D7-D6",
		"       2  D2-D4        C8-D7",
		"       3",
	)
	blanked := rows(
		"       1  E2-E4        D7-D6",
	)

	noFull, tokFull, okFull := lastEntry(parseMoveList(full), false /* White column */)
	if !okFull || noFull != 2 || tokFull != "D2-D4" {
		t.Fatalf("full list: got (%d,%q,%v), want (2,\"D2-D4\",true)", noFull, tokFull, okFull)
	}
	noBlank, _, okBlank := lastEntry(parseMoveList(blanked), false)
	if !okBlank || noBlank != 1 {
		t.Fatalf("blanked list: got (%d,%v), want move number 1", noBlank, okBlank)
	}
	// The old text-based detector saw a CHANGE here and called it a commit; the
	// number-based one must see no advance past the baseline of 2.
	if noBlank > noFull {
		t.Errorf("blank frame advanced the move number: %d > %d", noBlank, noFull)
	}
}

func TestTokenSquares(t *testing.T) {
	cases := []struct {
		tok        string
		from, to   string
		kind       string
		hasSquares bool
	}{
		{"E2-E4", "e2", "e4", "squares", true},
		{"E4XD5", "e4", "d5", "squares", true},
		{"F5-F4+", "f5", "f4", "squares", true},
		{"B7-B8/N", "b7", "b8", "squares", true},
		{"F2-F1/Q+", "f2", "f1", "squares", true},
		{"O-O", "", "", "O-O", false},
		{"0-0-0", "", "", "O-O-O", false},
		{"O-O-O+", "", "", "O-O-O", false},
		{"PXPEP", "", "", "EP", false},
		{"", "", "", "", false},
		{"GARBAGE", "", "", "", false},
	}
	for _, c := range cases {
		from, to, kind, ok := TokenSquares(c.tok)
		if ok != c.hasSquares || kind != c.kind {
			t.Errorf("TokenSquares(%q) = kind %q ok %v, want %q %v", c.tok, kind, ok, c.kind, c.hasSquares)
			continue
		}
		if ok && (from.Algebraic() != c.from || to.Algebraic() != c.to) {
			t.Errorf("TokenSquares(%q) = %s-%s, want %s-%s", c.tok, from.Algebraic(), to.Algebraic(), c.from, c.to)
		}
	}
}

func TestCrossCheckHistory(t *testing.T) {
	hist := []HistMove{
		{From: "e2", To: "e4"}, {From: "e7", To: "e5"},
		{From: "f1", To: "c4"}, {From: "g8", To: "f6"},
	}
	shown := map[int]string{1: "E2-E4", 2: "E7-E5", 3: "F1-C4", 4: "G8-F6"}
	if ok, bad, detail := crossCheck(shown, hist, 1); !ok {
		t.Errorf("matching list reported bad ply %d: %s", bad, detail)
	}
	// A stale/incorrect token must be caught at the exact ply.
	shown[3] = "D1XD4"
	if ok, bad, _ := crossCheck(shown, hist, 1); ok || bad != 3 {
		t.Errorf("mismatch at ply 3 not caught: ok=%v bad=%d", ok, bad)
	}
	// Sargon one ply ahead of us (it has just replied) is NOT a desync.
	if ok, bad, detail := crossCheck(map[int]string{1: "E2-E4", 2: "E7-E5", 3: "F1-C4"}, hist[:2], 1); !ok {
		t.Errorf("sargon one ply ahead reported bad ply %d: %s", bad, detail)
	}
	// Two plies ahead IS a desync (we lost track of a move).
	if ok, _, _ := crossCheck(map[int]string{1: "E2-E4", 2: "E7-E5", 3: "F1-C4"}, hist[:1], 1); ok {
		t.Error("sargon two plies ahead was not flagged")
	}
	// Castling and en passant carry no squares and check against the flags.
	castle := []HistMove{{From: "e1", To: "g1", Castle: "O-O"}}
	if ok, _, d := crossCheck(map[int]string{1: "O-O"}, castle, 1); !ok {
		t.Errorf("castle mismatch: %s", d)
	}
	if ok, _, _ := crossCheck(map[int]string{1: "O-O-O"}, castle, 1); ok {
		t.Error("wrong-side castle was not flagged")
	}
	ep := []HistMove{{From: "e5", To: "d6", EnPassant: true}}
	if ok, _, d := crossCheck(map[int]string{1: "PXPEP"}, ep, 1); !ok {
		t.Errorf("en passant mismatch: %s", d)
	}
	if ok, _, _ := crossCheck(map[int]string{1: "PXPEP"}, []HistMove{{From: "e5", To: "d6"}}, 1); ok {
		t.Error("PXPEP against a non-e.p. move was not flagged")
	}
}
