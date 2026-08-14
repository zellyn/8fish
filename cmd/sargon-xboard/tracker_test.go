package main

import (
	"testing"

	"github.com/zellyn/8fish/internal/refchess"
)

// apply a sequence of UCI moves, failing the test on any tracker error.
func mustApply(t *testing.T, tr *repTracker, moves ...string) {
	t.Helper()
	for _, mv := range moves {
		if err := tr.apply(mv); err != nil {
			t.Fatalf("apply(%q): %v", mv, err)
		}
	}
}

// knightShuttle returns the move sequence that, from the standard start,
// shuttles both knights out and back, leaving Black to move at a position from
// which f6g8 completes the third occurrence of the starting position (a
// threefold repetition — the pure-Go analogue of Sargon's perpetual). After
// these 7 moves seen[startpos]==2 and it is Black's turn.
func knightShuttle() []string {
	return []string{
		"g1f3", "g8f6", // 1. Nf3 Nf6
		"f3g1", "f6g8", // 2. Ng1 Ng8   -> start position, 2nd occurrence
		"g1f3", "g8f6", // 3. Nf3 Nf6
		"f3g1", // 4. Ng1        -> Black to move; f6g8 makes the 3rd occurrence
	}
}

// TestFindsThreefoldRepetition: a shuttle brings the position back for a third
// time; the finder must return the exact move that completes the threefold.
func TestFindsThreefoldRepetition(t *testing.T) {
	tr := newRepTracker(refchess.StartFEN)
	if !tr.valid {
		t.Fatal("tracker should be valid")
	}
	mustApply(t, tr, knightShuttle()...)

	coord, count, reason := tr.findDrawingMove()
	if coord != "f6g8" {
		t.Errorf("findDrawingMove coord = %q, want %q", coord, "f6g8")
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	if reason != "repetition" {
		t.Errorf("reason = %q, want %q", reason, "repetition")
	}
	// The returned move must actually be legal and reach a thrice-seen position.
	cp := tr.pos.Copy()
	mv, err := refchess.ParseMove(coord)
	if err != nil || cp.Make(mv) != nil {
		t.Fatalf("returned move %q is not legal", coord)
	}
	if got := tr.seen[cp.ZobristKey()] + 1; got != 3 {
		t.Errorf("resulting occurrence count = %d, want 3", got)
	}
}

// TestNoDrawWhenNoRepetition: with no position seen twice, the finder must
// return no move (so the adapter resigns rather than play an unverified move).
func TestNoDrawWhenNoRepetition(t *testing.T) {
	tr := newRepTracker(refchess.StartFEN)
	// Two plies out; nothing has repeated.
	mustApply(t, tr, "g1f3", "g8f6")
	if coord, _, _ := tr.findDrawingMove(); coord != "" {
		t.Errorf("findDrawingMove = %q, want no move", coord)
	}
	// Even one ply before the threefold (start seen twice, but no single move
	// from here returns to it) there must be no verified drawing move.
	tr2 := newRepTracker(refchess.StartFEN)
	mustApply(t, tr2, "g1f3", "g8f6", "f3g1", "f6g8", "g1f3", "g8f6")
	if coord, _, _ := tr2.findDrawingMove(); coord != "" {
		t.Errorf("premature findDrawingMove = %q, want no move", coord)
	}
}

// TestFiftyMoveRule: a position at halfmove clock 99 has a quiet move that
// reaches 100; the finder must surface it with the fiftymove reason.
func TestFiftyMoveRule(t *testing.T) {
	// White K+R vs lone Black K, halfmove clock already 99.
	tr := newRepTracker("7k/8/8/8/8/8/8/K6R w - - 99 1")
	if !tr.valid {
		t.Fatal("tracker should be valid")
	}
	coord, count, reason := tr.findDrawingMove()
	if reason != "fiftymove" {
		t.Errorf("reason = %q, want %q (coord=%q)", reason, "fiftymove", coord)
	}
	if count != 100 {
		t.Errorf("count = %d, want 100", count)
	}
	if coord == "" {
		t.Fatal("expected a fifty-move drawing move, got none")
	}
	// Playing it must actually reach a halfmove clock of 100.
	cp := tr.pos.Copy()
	mv, err := refchess.ParseMove(coord)
	if err != nil || cp.Make(mv) != nil {
		t.Fatalf("returned move %q not legal", coord)
	}
	if cp.HalfmoveClock() != 100 {
		t.Errorf("halfmove clock after %q = %d, want 100", coord, cp.HalfmoveClock())
	}
}

// TestSetboardStartRepetition: the referee history rooted at a non-standard
// (setboard) FEN must detect a threefold relative to THAT root position.
func TestSetboardStartRepetition(t *testing.T) {
	// Rooks and kings; a rook shuttle on each side returns to the root FEN.
	root := "1r5k/8/8/8/8/8/8/1R5K w - - 0 1"
	tr := newRepTracker(root)
	if !tr.valid {
		t.Fatal("tracker should be valid")
	}
	mustApply(t, tr,
		"b1a1", "b8a8", // root -> ...
		"a1b1", "a8b8", // -> root, 2nd occurrence
		"b1a1", "b8a8", //
		"a1b1", // Black to move; a8b8 completes the threefold
	)
	coord, count, reason := tr.findDrawingMove()
	if coord != "a8b8" || count != 3 || reason != "repetition" {
		t.Errorf("findDrawingMove = (%q,%d,%q), want (a8b8,3,repetition)", coord, count, reason)
	}
}

// TestInvalidMoveDisablesTracker: an illegal/unparseable move must mark the
// tracker invalid and suppress all draw-move verification (non-invasive
// fallback to the resign path), never fabricate a move.
func TestInvalidMoveDisablesTracker(t *testing.T) {
	tr := newRepTracker(refchess.StartFEN)
	if err := tr.apply("e2e5"); err == nil { // illegal pawn move
		t.Fatal("expected error applying illegal move")
	}
	if tr.valid {
		t.Error("tracker should be invalid after an illegal move")
	}
	if coord, _, _ := tr.findDrawingMove(); coord != "" {
		t.Errorf("invalid tracker findDrawingMove = %q, want no move", coord)
	}
	// Parse failure likewise invalidates.
	tr2 := newRepTracker(refchess.StartFEN)
	if err := tr2.apply("garbage"); err == nil || tr2.valid {
		t.Error("expected parse error to invalidate tracker")
	}
}

// TestEngineTrackingReplaysFullGame drives the ENGINE-level tracking wrappers
// (initTrackerOnce/trackApply/findDrawingMove) exactly as the think goroutines
// do — no emulator/machine needed, since these touch only the tracker fields.
// It replays an opening then the perpetual shuttle, proving every move reaches
// the tracker once and the drawing move is found through the real wiring.
func TestEngineTrackingReplaysFullGame(t *testing.T) {
	e := &engine{}
	e.initTrackerOnce("") // no setboard -> standard start
	// A short book line, then the knight shuttle back to the start position.
	// (Not a real Sargon game, but the same call sequence: alternating moves
	// fed through trackApply the way usermove/reply are.)
	for _, mv := range []string{"g1f3", "g8f6", "f3g1", "f6g8"} { // -> start, 2nd
		e.trackApply(mv)
	}
	// No repetition-completing move available mid-shuttle.
	if coord, _, _ := e.findDrawingMove(); coord != "" {
		t.Fatalf("unexpected drawing move mid-game: %q", coord)
	}
	for _, mv := range []string{"g1f3", "g8f6", "f3g1"} { // Black to move; f6g8 draws
		e.trackApply(mv)
	}
	coord, count, reason := e.findDrawingMove()
	if coord != "f6g8" || count != 3 || reason != "repetition" {
		t.Errorf("engine findDrawingMove = (%q,%d,%q), want (f6g8,3,repetition)", coord, count, reason)
	}
	// The tracker must still be valid: a doubled or dropped move would corrupt
	// the occurrence counts and typically surface as an illegal Make.
	if !e.tracker.valid {
		t.Error("tracker unexpectedly invalid after a clean replay")
	}
}

// TestInitTrackerOnceIsIdempotent: initTrackerOnce must build the history once
// and ignore later calls (every think goroutine calls it), so occurrence counts
// accumulated by trackApply are never reset mid-game.
func TestInitTrackerOnceIsIdempotent(t *testing.T) {
	e := &engine{}
	e.initTrackerOnce("")
	e.trackApply("e2e4")
	e.initTrackerOnce("") // second call must be a no-op
	// If it re-initialized, the e2e4 position count would be lost; verify the
	// tracker still reflects the applied move (side to move is Black).
	if e.tracker.pos.SideToMove() != 1 {
		t.Error("initTrackerOnce reset the tracker on a second call")
	}
}
