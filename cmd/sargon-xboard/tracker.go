package main

import (
	"fmt"

	"github.com/zellyn/chess6502/internal/refchess"
)

// repTracker maintains an independent referee history of the game (via
// internal/refchess) so the adapter can verify a Sargon "draw by repetition"
// claim and play the actual drawing move — letting cutechess end the game as a
// draw natively — instead of resigning Sargon's side.
//
// Sargon declares a repetition draw with its own drawing move still pending,
// one ply before cutechess would count the threefold on ITS board. By replaying
// every move through refchess we know the exact position Sargon is to move from,
// can enumerate its legal moves, and can pick the one whose resulting position
// already occurred twice (so playing it is the third occurrence cutechess will
// recognize) or which trips the fifty-move rule.
//
// It is deliberately non-invasive: if any move fails to parse or apply (which
// should never happen — every move here already passed through Sargon and/or
// cutechess), the tracker marks itself invalid and every downstream check
// returns "no drawing move", so the caller falls back to today's resign path.
type repTracker struct {
	pos   *refchess.Position
	seen  map[uint64]int // Zobrist key -> occurrence count (initial position included)
	valid bool
}

// newRepTracker starts a tracker from a FEN (the setboard opening, or
// refchess.StartFEN for a normal game). The initial position is counted, so a
// later return to it is correctly the second occurrence.
func newRepTracker(fen string) *repTracker {
	pos, err := refchess.ParseFEN(fen)
	if err != nil {
		return &repTracker{valid: false}
	}
	t := &repTracker{pos: pos, seen: map[uint64]int{}, valid: true}
	t.seen[pos.ZobristKey()]++
	return t
}

// apply plays one move in UCI coordinate notation ("e2e4", "g2g3", "e7e8q") and
// records the resulting position's occurrence count. It marks the tracker
// invalid and returns an error if the move can't be parsed or is illegal in the
// tracked position — the tracker must never silently diverge from the real
// game, since a wrong occurrence count could fabricate a bogus drawing move.
// A nil or already-invalid tracker is a no-op.
func (t *repTracker) apply(coord string) error {
	if t == nil || !t.valid {
		return nil
	}
	mv, err := refchess.ParseMove(coord)
	if err != nil {
		t.valid = false
		return fmt.Errorf("tracker parse %q: %w", coord, err)
	}
	if err := t.pos.Make(mv); err != nil {
		t.valid = false
		return fmt.Errorf("tracker make %q: %w", coord, err)
	}
	t.seen[t.pos.ZobristKey()]++
	return nil
}

// findDrawingMove returns a legal move for the side to move that forces a draw
// cutechess will recognize on its own board: first choice a move completing a
// threefold repetition (the resulting position already occurred twice), else a
// move that reaches a halfmove clock of 100 (fifty-move rule). It returns
// ("", 0, "") when the tracker is invalid or no such move exists — in which
// case the caller must NOT play an unverified move (that would wedge Sargon,
// which believes the game is over) and should resign instead.
//
// count is the resulting occurrence count (3 for a completed threefold) or the
// halfmove clock (>=100) for the fifty-move case; reason is "repetition" or
// "fiftymove" for logging.
func (t *repTracker) findDrawingMove() (coord string, count int, reason string) {
	if t == nil || !t.valid || t.pos == nil {
		return "", 0, ""
	}
	legal := t.pos.LegalMoves()
	// Prefer completing a threefold repetition.
	for _, mv := range legal {
		cp := t.pos.Copy()
		if err := cp.Make(mv); err != nil {
			continue
		}
		if n := t.seen[cp.ZobristKey()] + 1; n >= 3 {
			return mv.String(), n, "repetition"
		}
	}
	// Otherwise trip the fifty-move rule.
	for _, mv := range legal {
		cp := t.pos.Copy()
		if err := cp.Make(mv); err != nil {
			continue
		}
		if hc := cp.HalfmoveClock(); hc >= 100 {
			return mv.String(), hc, "fiftymove"
		}
	}
	return "", 0, ""
}
