package refchess

import "testing"

// TestSANParseAndRoundTrip walks a real opening in SAN, checking each move
// parses to the expected UCI, and that SAN(ParseSAN(x)) == x (rendering is
// the inverse of parsing for well-formed input).
func TestSANParseAndRoundTrip(t *testing.T) {
	// Scotch: exercises captures (exd4, Nxd4) and a check-free line.
	sans := []string{"e4", "e5", "Nf3", "Nc6", "d4", "exd4", "Nxd4", "Nf6", "Nc3", "Bb4"}
	wantUCI := []string{"e2e4", "e7e5", "g1f3", "b8c6", "d2d4", "e5d4", "f3d4", "g8f6", "b1c3", "f8b4"}
	p, err := ParseFEN(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	for i, san := range sans {
		mv, err := p.ParseSAN(san)
		if err != nil {
			t.Fatalf("ply %d ParseSAN(%q): %v", i+1, san, err)
		}
		if mv.String() != wantUCI[i] {
			t.Fatalf("ply %d ParseSAN(%q) = %s, want %s", i+1, san, mv.String(), wantUCI[i])
		}
		if got := p.SAN(mv); got != san {
			t.Errorf("ply %d SAN round-trip = %q, want %q", i+1, got, san)
		}
		if err := p.Make(mv); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSANDisambiguation checks minimal file/rank disambiguation both ways.
func TestSANDisambiguation(t *testing.T) {
	// Both knights (b8, f6) can reach d7 -> "Nbd7".
	p, _ := ParseFEN("rnbqkb1r/ppp1pppp/5n2/3p4/3P4/2N2N2/PPP1PPPP/R1BQKB1R b KQkq - 0 1")
	mv, err := p.ParseSAN("Nbd7")
	if err != nil {
		t.Fatalf("ParseSAN(Nbd7): %v", err)
	}
	if mv.String() != "b8d7" {
		t.Fatalf("Nbd7 = %s, want b8d7", mv.String())
	}
	if got := p.SAN(mv); got != "Nbd7" {
		t.Errorf("SAN(b8d7) = %q, want Nbd7", got)
	}
	// The undertermined "Nd7" must be rejected as ambiguous.
	if _, err := p.ParseSAN("Nd7"); err == nil {
		t.Error("ParseSAN accepted ambiguous Nd7")
	}
}

// TestSANCheckAndMate checks the +/# suffixes and castling render/parse.
func TestSANCheckAndMate(t *testing.T) {
	// Fool's mate: 1. f3 e5 2. g4 Qh4#.
	p, _ := ParseFEN(StartFEN)
	for _, san := range []string{"f3", "e5", "g4"} {
		mv, err := p.ParseSAN(san)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Make(mv); err != nil {
			t.Fatal(err)
		}
	}
	mv, err := p.ParseSAN("Qh4")
	if err != nil {
		t.Fatal(err)
	}
	if got := p.SAN(mv); got != "Qh4#" {
		t.Errorf("SAN = %q, want Qh4#", got)
	}
	// Castling parses even with trailing check glyphs stripped.
	cp, _ := ParseFEN("rnbqk2r/pppp1ppp/5n2/2b1p3/2B1P3/5N2/PPPP1PPP/RNBQK2R w KQkq - 0 1")
	mv, err = cp.ParseSAN("O-O")
	if err != nil {
		t.Fatalf("ParseSAN(O-O): %v", err)
	}
	if mv.String() != "e1g1" {
		t.Errorf("O-O = %s, want e1g1", mv.String())
	}
	if got := cp.SAN(mv); got != "O-O" {
		t.Errorf("SAN(e1g1) = %q, want O-O", got)
	}
}

// TestSANIllegal confirms a syntactically-fine but illegal move is refused.
func TestSANIllegal(t *testing.T) {
	p, _ := ParseFEN(StartFEN)
	if _, err := p.ParseSAN("Ke2"); err == nil {
		t.Error("ParseSAN accepted illegal Ke2 from the start position")
	}
}
