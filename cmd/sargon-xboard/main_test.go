package main

import "testing"

// TestCoordToSargonPromotion verifies the xboard coordinate -> Sargon FROM-TO
// translation, especially that a promotion letter is preserved (uppercased) so
// enterMove knows to answer Sargon's "ENTER PROMOTED PIECE" prompt. Dropping the
// letter (the old behaviour) left the pawn stuck at the prompt and hung the game.
func TestCoordToSargonPromotion(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"e2e4", "E2-E4", false},   // plain move, no promotion
		{"c2c1q", "C2-C1Q", false}, // queen promotion (Black, rank 1)
		{"f2f1n", "F2-F1N", false}, // knight under-promotion
		{"a7a8q", "A7-A8Q", false}, // queen promotion (White, rank 8)
		{"h7h8r", "H7-H8R", false}, // rook under-promotion
		{"b7b8b", "B7-B8B", false}, // bishop under-promotion
		{"e1g1", "E1-G1", false},   // castling coordinate (no promo letter)
		{"xyz", "", true},          // garbage
	}
	for _, c := range cases {
		got, err := coordToSargon(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("coordToSargon(%q) = %q, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("coordToSargon(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("coordToSargon(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestScreenTokenToCoordPromotion verifies Sargon's move-list tokens (with the
// "/piece" promotion marker, and an optional trailing "+" for check) decode to
// coordinate notation with the right promotion suffix, for both colours.
func TestScreenTokenToCoordPromotion(t *testing.T) {
	cases := []struct {
		tok         string
		sargonWhite bool
		want        string
	}{
		{"B7-B8/Q", true, "b7b8q"},   // Sargon (White) queens
		{"B7-B8/Q+", true, "b7b8q"},  // queen with check
		{"A7-A8/N", true, "a7a8n"},   // white knight under-promotion
		{"B2-B1/Q", false, "b2b1q"},  // Sargon (Black) queens on rank 1
		{"B2-B1/R+", false, "b2b1r"}, // black rook under-promotion, check
		{"E2-E4", true, "e2e4"},      // plain move
		{"E5XD4", true, "e5d4"},      // capture
		{"0-0", true, "e1g1"},        // castle unaffected
	}
	for _, c := range cases {
		if got := screenTokenToCoord(c.tok, c.sargonWhite); got != c.want {
			t.Errorf("screenTokenToCoord(%q, white=%v) = %q, want %q",
				c.tok, c.sargonWhite, got, c.want)
		}
	}
}
