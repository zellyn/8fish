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

// TestBookMovesBankTheirIncome is the standard-start invariant: an opening-book
// move costs ~0 cycles, so Settle must credit (almost) its whole income to the
// bank, and the FIRST real search out of book must then be allocated visibly
// more than the flat income B. This is the harness-side loop that cmd/sargon-
// symmatch runs per own move (Alloc -> search -> Settle) with the measured book
// costs from a real standard-start game (game 0 of the validation run).
func TestBookMovesBankTheirIncome(t *testing.T) {
	const budget = uint64(30_000_000)
	// Measured 8fish book-move costs (on-device book probe + any shallow
	// ponder-prediction probe) from a real standard-start game.
	bookSpends := []uint64{705_377, 136_416, 16_554, 153_163, 16_819}

	clock := &chesstest.BankedClock{Base: budget}
	var totalBook uint64
	for i, spent := range bookSpends {
		clock.Alloc() // a book move ignores its allocation (it runs no search)
		clock.Settle(spent)
		totalBook += spent
		wantBank := int64((uint64(i+1))*budget - totalBook)
		if got := clock.Bank(); got != wantBank {
			t.Fatalf("book move %d: bank=%d, want %d (income banked minus spend)", i+1, got, wantBank)
		}
	}
	// Five near-free moves must leave ~5B banked (well under the 8B cap).
	if bank := clock.Bank(); bank < int64(4.9*float64(budget)) {
		t.Errorf("bank after %d book moves = %d, want >= ~%d", len(bookSpends), bank, int64(4.9*float64(budget)))
	}
	// The first post-book search draws income + bank/8 — strictly more than B.
	alloc := clock.Alloc()
	if alloc <= budget {
		t.Errorf("first post-book alloc = %d, want > income %d (book time must be spendable)", alloc, budget)
	}
	if ratio := float64(alloc) / float64(budget); ratio < 1.5 || ratio > 2.0 {
		t.Errorf("first post-book alloc = %.3fx income, want ~1.6x (<= 2.0x by the 8B bank cap)", ratio)
	}
}

// TestMovetimeMsFloor guards the ~0-cycle-window case that standard-start makes
// reachable: a sub-millisecond budget must never round to "movetime 0", which
// the bridge reads as fixed-DEPTH mode (a 300-billion-cycle run cap = a hang).
func TestMovetimeMsFloor(t *testing.T) {
	for _, tc := range []struct{ cyc, want uint64 }{
		{0, 1}, {1, 1}, {1019, 1}, {1020, 1}, {2040, 2}, {30_000_000, 29411},
	} {
		if got := movetimeMs(tc.cyc); got != tc.want {
			t.Errorf("movetimeMs(%d) = %d, want %d", tc.cyc, got, tc.want)
		}
	}
}
