package sargon

import (
	"fmt"
	"strconv"
	"strings"
)

// Sargon's on-screen MOVE LIST is its own authoritative record of the game, and
// the only ponder-immune place to read it (the $60-$7F piece list is search
// scratch in Hard Mode). It occupies text rows 10-23 as
//
//	<right-justified move no. in cols 0-7>  <WHITE token>        <BLACK token>
//	       1  E2-E4        E7-E5
//	      55  B7-B8/N      F1-A1+
//	      56
//
// White's token starts at col 10 and Black's at col 23 REGARDLESS of who Sargon
// is (only the SARGON/PLAYER header labels swap). A castling token is printed
// one column right of the others (" O-O "), and the widest token is 8 chars
// ("B7-B8/Q+"), so the scrape windows are cols [10,18) and [22,31).
//
// The list shows a 13-move window with the newest at the bottom and scrolls;
// row 23 usually holds the move number of the move currently being played.
//
// CRITICAL (the 2026-07 desync root cause): while Sargon searches, it BLANKS the
// bottom move-list rows and repaints them ~167K cycles later — a transient frame
// in which the list is missing its most recent entries. Detecting "Sargon has
// moved" by comparing the raw column TEXT therefore false-positives on that
// blank frame, and the caller then reads the bottom-most surviving token, which
// is Sargon's PREVIOUS move: a stale, illegal-on-our-board reply. Commit
// detection must instead use the displayed MOVE NUMBER, which only ever advances
// on a real commit and is unaffected by a repaint (see LastSargonEntry).
const (
	listTopRow    = 10
	listBotRow    = 24 // exclusive
	whiteColLo    = 10
	whiteColHi    = 18
	blackColLo    = 22
	blackColHi    = 31
	listNumberCol = 10 // move number lives in cols [0, listNumberCol)
)

// MoveRow is one displayed row of Sargon's move list. White/Black are the raw
// screen tokens ("" when that half-move is not on the list yet).
type MoveRow struct {
	No    int
	White string
	Black string
}

// MoveList parses the visible move-list window. Rows without a parseable move
// number (blank rows, or rows blanked by an in-progress repaint) are skipped.
func (m *Machine) MoveList() []MoveRow {
	rows := make([]string, 0, listBotRow-listTopRow)
	for r := listTopRow; r < listBotRow; r++ {
		rows = append(rows, m.TextRow(r))
	}
	return parseMoveList(rows)
}

// parseMoveList is MoveList's pure core: it takes the move-list window's screen
// rows (row 10 first) and returns the entries it can read.
func parseMoveList(rows []string) []MoveRow {
	var out []MoveRow
	for _, row := range rows {
		if len(row) < blackColHi {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(row[:listNumberCol]))
		if err != nil || n <= 0 {
			continue
		}
		out = append(out, MoveRow{
			No:    n,
			White: strings.TrimSpace(row[whiteColLo:whiteColHi]),
			Black: strings.TrimSpace(row[blackColLo:blackColHi]),
		})
	}
	return out
}

// entry returns the newest (highest move number) non-empty token in the
// requested column, with its move number. ok is false when the column is empty.
func lastEntry(rows []MoveRow, black bool) (no int, tok string, ok bool) {
	for _, r := range rows {
		t := r.White
		if black {
			t = r.Black
		}
		if t == "" {
			continue
		}
		if !ok || r.No > no {
			no, tok, ok = r.No, t, true
		}
	}
	return no, tok, ok
}

// LastSargonEntry returns the move number and token of the newest move in
// SARGON's column. The move number strictly increases by one per Sargon commit
// and is immune both to the list scrolling and to the mid-search repaint that
// transiently blanks rows, so it — not the column text — is the commit signal.
func (m *Machine) LastSargonEntry() (no int, tok string, ok bool) {
	return lastEntry(m.MoveList(), !m.SargonWhite)
}

// LastOwnEntry is LastSargonEntry for the KEYBOARD opponent's column: the
// ponder-immune signal that our typed move was accepted into the list.
func (m *Machine) LastOwnEntry() (no int, tok string, ok bool) {
	return lastEntry(m.MoveList(), m.SargonWhite)
}

// ErrListWrapped is returned when Sargon's displayed move number goes BACKWARDS.
// Sargon III cannot record a game longer than 127 full moves: at move 128 the
// move list restarts its numbering from 1 and its screen record no longer
// matches the game being played (observed in 2/2 games that reached move 128).
var ErrListWrapped = fmt.Errorf("sargon move list wrapped (game exceeded Sargon's ~127-move capacity)")

// MaxSargonMoves is the longest game Sargon III can record on its move list.
// Drive no further: past it the on-screen record — the only ponder-immune read
// path — is meaningless.
const MaxSargonMoves = 127

// ScreenDump renders the whole 24-row text screen framed by greppable markers,
// for periodic capture into a match log.
func (m *Machine) ScreenDump(label string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "SCREEN-DUMP-BEGIN %s sargonWhite=%v\n", label, m.SargonWhite)
	for r := 0; r < 24; r++ {
		sb.WriteString(strings.TrimRight(m.TextRow(r), " "))
		sb.WriteByte('\n')
	}
	sb.WriteString("SCREEN-DUMP-END")
	return sb.String()
}

// MoveListPlies flattens the visible move list into (ply, token) pairs, where
// ply 2n-1 is White's nth move and ply 2n is Black's. Only plies actually shown
// are returned; the window scrolls, so the first ply is usually > 1.
func (m *Machine) MoveListPlies() map[int]string {
	out := map[int]string{}
	for _, r := range m.MoveList() {
		if r.White != "" {
			out[2*r.No-1] = r.White
		}
		if r.Black != "" {
			out[2*r.No] = r.Black
		}
	}
	return out
}

// HistMove is one half-move of OUR record of the game, in the minimal form
// needed to check it against the token Sargon displays for the same ply.
// From/To are lowercase algebraic ("e2", "e4"). Castle is "O-O"/"O-O-O" for a
// castling move (Sargon prints those with no squares), and EnPassant marks an
// en-passant capture (Sargon prints "PXPEP", also with no squares).
type HistMove struct {
	From, To  string
	Castle    string
	EnPassant bool
}

// CrossCheckHistory compares SARGON's own on-screen move list against our
// history, ply for ply. This is the harness's desync alarm: our board and
// Sargon's must record the same game, and if they ever diverge every later read
// is fiction. The list only shows a scrolling window of recent moves, so only
// the plies actually displayed are compared (the overlapping suffix).
//
// firstPly is the move-list ply number that hist[0] occupies: 1 when the game's
// first recorded move is White's, 2 when it is Black's (a setboard position with
// Black to move — Sargon's list still puts Black's move in the right column of
// row 1). CTRL-A setup clears the list and renumbers from 1 (verified in
// TestSetupResetsMoveList), so setboard games are checkable too.
//
// It returns ok=false with the FIRST differing ply (1-based) and a description
// naming both sides' versions of that ply.
func (m *Machine) CrossCheckHistory(hist []HistMove, firstPly int) (ok bool, badPly int, detail string) {
	return crossCheck(m.MoveListPlies(), hist, firstPly)
}

// crossCheck is CrossCheckHistory's pure core over an already-scraped list.
func crossCheck(shown map[int]string, hist []HistMove, firstPly int) (ok bool, badPly int, detail string) {
	if firstPly < 1 {
		firstPly = 1
	}
	last := len(hist) + firstPly - 1 // list-ply number of our newest recorded move
	plies := make([]int, 0, len(shown))
	for p := range shown {
		plies = append(plies, p)
	}
	sortInts(plies)
	for _, p := range plies {
		tok := shown[p]
		if p < firstPly {
			continue // pre-setup residue; cannot happen after a clean setup
		}
		if p == last+1 {
			// Sargon has just committed a reply we have not recorded yet: normal
			// between RequestMove returning and the caller applying the move.
			continue
		}
		if p > last {
			return false, p, fmt.Sprintf("sargon shows ply %d = %q but our history ends at ply %d", p, tok, last)
		}
		h := hist[p-firstPly]
		from, to, kind, hasSquares := TokenSquares(tok)
		switch {
		case hasSquares:
			if from.Algebraic() != h.From || to.Algebraic() != h.To {
				return false, p, fmt.Sprintf("ply %d: sargon %q (%s-%s) vs ours %s-%s",
					p, tok, from.Algebraic(), to.Algebraic(), h.From, h.To)
			}
		case kind == "O-O" || kind == "O-O-O":
			if h.Castle != kind {
				return false, p, fmt.Sprintf("ply %d: sargon %q (%s) vs ours %s-%s (castle=%q)", p, tok, kind, h.From, h.To, h.Castle)
			}
		case kind == "EP":
			if !h.EnPassant {
				return false, p, fmt.Sprintf("ply %d: sargon %q (en passant) vs ours %s-%s (not e.p.)", p, tok, h.From, h.To)
			}
		default:
			return false, p, fmt.Sprintf("ply %d: sargon token %q is unparseable (ours %s-%s)", p, tok, h.From, h.To)
		}
	}
	return true, 0, ""
}

// HistoryString renders our history as a numbered move list for logging beside
// Sargon's screen dump.
func HistoryString(hist []HistMove) string {
	var sb strings.Builder
	for i, h := range hist {
		if i%2 == 0 {
			fmt.Fprintf(&sb, " %d.", i/2+1)
		}
		if h.Castle != "" {
			fmt.Fprintf(&sb, " %s", h.Castle)
			continue
		}
		fmt.Fprintf(&sb, " %s%s", h.From, h.To)
		if h.EnPassant {
			sb.WriteString("ep")
		}
	}
	return strings.TrimSpace(sb.String())
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// TokenSquares extracts the from/to squares a Sargon move-list token names, if
// it names any. Castling ("0-0", "O-O-O") and en passant ("PXPEP") carry no
// squares and return ok=false with the kind set, so callers can check them
// against the position instead.
func TokenSquares(tok string) (from, to Square, kind string, ok bool) {
	t := strings.ToUpper(strings.TrimSpace(tok))
	t = strings.TrimSuffix(t, "+")
	switch t {
	case "0-0", "O-O":
		return 0, 0, "O-O", false
	case "0-0-0", "O-O-O":
		return 0, 0, "O-O-O", false
	}
	if strings.HasSuffix(t, "EP") {
		return 0, 0, "EP", false
	}
	if i := strings.IndexByte(t, '/'); i >= 0 {
		t = t[:i]
	}
	if len(t) < 5 || (t[2] != '-' && t[2] != 'X') {
		return 0, 0, "", false
	}
	f, ok1 := ParseSquare(t[0:2])
	tt, ok2 := ParseSquare(t[3:5])
	if !ok1 || !ok2 {
		return 0, 0, "", false
	}
	return f, tt, "squares", true
}
