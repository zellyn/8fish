package main

import (
	"testing"

	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

// TestScreenTokenToCoord audits every Sargon move-list token form the parser must
// handle (manual notation section: FROM-TO, capture X, promotion /Q, castling
// 0-0/0-0-0, and the square-less en passant "PXPEP"). The square-less forms are
// disambiguated against the referee's current position.
func TestScreenTokenToCoord(t *testing.T) {
	// Position after 1.e4 Nf6 2.e5 d5 — black just double-pushed d7-d5, so the
	// white e5 pawn has a unique en passant capture e5xd6 (epSquare d6).
	epFEN := "rnbqkb1r/ppp1pppp/5n2/3pP3/8/8/PPPP1PPP/RNBQKBNR w KQkq d6 0 3"
	epPos, err := refchess.ParseFEN(epFEN)
	if err != nil {
		t.Fatalf("parse ep FEN: %v", err)
	}

	tests := []struct {
		name        string
		tok         string
		sargonWhite bool
		ref         *refchess.Position
		want        string
	}{
		{"normal", "E2-E4", true, nil, "e2e4"},
		{"capture", "E4XD5", true, nil, "e4d5"},
		{"promotion-queen", "E7-E8/Q", true, nil, "e7e8q"},
		{"promotion-knight", "G2-G1/N", false, nil, "g2g1n"},
		{"castle-kingside-white", "0-0", true, nil, "e1g1"},
		{"castle-queenside-white", "0-0-0", true, nil, "e1c1"},
		{"castle-kingside-black", "0-0", false, nil, "e8g8"},
		{"castle-queenside-black", "0-0-0", false, nil, "e8c8"},
		{"en-passant-PXPEP", "PXPEP", true, epPos, "e5d6"},
		{"en-passant-lowercase", "pxpep", true, epPos, "e5d6"},
		{"en-passant-no-ref", "PXPEP", true, nil, ""}, // unreadable without position
		{"garbage", "ZZZZ", true, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := screenTokenToCoord(tc.tok, tc.sargonWhite, tc.ref)
			if got != tc.want {
				t.Errorf("screenTokenToCoord(%q, %v, ref=%v) = %q, want %q",
					tc.tok, tc.sargonWhite, tc.ref != nil, got, tc.want)
			}
		})
	}
}

// TestEnPassantAmbiguous confirms that when two pawns flank the double-pushed
// pawn (two legal en passant captures to the same square), the square-less
// "PXPEP" token cannot be resolved and returns "" (the caller then adjudicates
// from Sargon's screen rather than guessing).
func TestEnPassantAmbiguous(t *testing.T) {
	// Black just played c7-c5; white pawns on b5 and d5 can BOTH capture ep to c6.
	pos, err := refchess.ParseFEN("4k3/8/8/1PpP4/8/8/8/4K3 w - c6 0 2")
	if err != nil {
		t.Fatalf("parse FEN: %v", err)
	}
	if eps := pos.EnPassantCaptures(); len(eps) != 2 {
		t.Fatalf("want 2 en passant captures, got %d: %v", len(eps), eps)
	}
	if got := screenTokenToCoord("PXPEP", true, pos); got != "" {
		t.Errorf("ambiguous PXPEP = %q, want \"\"", got)
	}
}

// TestBankConserves is a closed-loop check that the per-game debt-bank makes
// 8fish's TOTAL own-move spend telescope to ~1.0x its intended budget (income x
// moves) despite a constant per-move iteration-boundary overshoot — the fix for
// the observed 1.079 leak. It mirrors the BankedClock the cutechess path uses.
func TestBankConserves(t *testing.T) {
	const (
		budget    = uint64(30_000_000)
		overshoot = 1.079 // the leak measured in the 40-game run
		moves     = 80
	)
	clock := &chesstest.BankedClock{Base: budget}
	var acct timeAcct
	for i := 0; i < moves; i++ {
		alloc := clock.Alloc()
		spent := uint64(float64(alloc) * overshoot) // engine overshoots its alloc
		clock.Settle(spent)
		acct.add(spent, budget)
	}
	if r := acct.ratio(); r < 0.99 || r > 1.03 {
		t.Errorf("bank did not conserve: ratio=%.4f (want ~1.0, was %.3f unbanked)", r, overshoot)
	}
}
