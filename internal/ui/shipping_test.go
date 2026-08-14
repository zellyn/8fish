package ui_test

// The SHIPPING build, driven through the real keyboard.
//
// Every other gate in this package drives asm/m8.s built with -D HARNESSKBD,
// which reads the harness input traps at $BFF1/$BFF2. That is not the image
// that goes on the disk. The image that goes on the disk polls $C000 for a
// key with bit 7 set and clears the strobe with $C010, and until goapple2's
// IIe model grew a keyboard (2026-07-28) nothing had ever pressed a key on
// it: it was proven to boot, paint and block, and nothing more.
//
// These tests drive asm/m8.bin — no -D HARNESSKBD — through goapple2/iie's
// modelled keyboard, and hold it to the same standard: type a move, play a
// whole game to a real termination, and (the strongest form) require the two
// builds to produce BYTE-IDENTICAL screens for identical input, keystroke by
// keystroke. Any divergence is a finding about the shipped artefact.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/refchess"
	"github.com/zellyn/8fish/internal/ui"
	"github.com/zellyn/goapple2/chargen"
)

func bootShipping(t *testing.T) *ui.Machine {
	t.Helper()
	u, err := ui.BootShipping(root, nil)
	if err != nil {
		t.Fatalf("boot shipping image: %v", err)
	}
	return u
}

// TestShippingKeyboard is the smallest thing that was never true before: a
// key pressed on the modelled IIe keyboard reaches the shipping image, is
// echoed on the input line, and moves a piece.
func TestShippingKeyboard(t *testing.T) {
	u := bootShipping(t)
	if err := u.TwoPlayer(); err != nil {
		t.Fatal(err)
	}
	// Echo: the line editor must show the move as it is typed.
	if err := u.Type("e2e4"); err != nil {
		t.Fatal(err)
	}
	if got := u.Screen().Text(23); !contains(got, "YOUR MOVE? e2e4") {
		t.Errorf("row 23 = %q, want the typed move echoed", strings.TrimSpace(got))
	}
	// Backspace is part of the keyboard path too ($08, left arrow on a IIe).
	if err := u.Key(0x08); err != nil {
		t.Fatal(err)
	}
	if got := u.Screen().Text(23); !contains(got, "YOUR MOVE? e2e") || contains(got, "e2e4") {
		t.Errorf("after backspace row 23 = %q, want e2e", strings.TrimSpace(got))
	}
	if err := u.Enter("4"); err != nil {
		t.Fatal(err)
	}
	if got, want := normFEN(u.FEN()),
		"rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0"; got != want {
		t.Fatalf("after typing e2e4 on the real keyboard: %q, want %q\n%s", got, want, u.Screen())
	}
	// The entropy collector must be fed by the REAL keyboard as well: on
	// hardware it is the engine's only source of randomness.
	if u.Peek(u.Defs["ENTROPY"]) == 0 {
		t.Error("ENTROPY is still 0 after five keystrokes through $C000")
	}
	t.Logf("shipping image after e2e4 typed on the modelled IIe keyboard:\n%s", u.Screen())
}

// pair drives the HARNESSKBD build and the SHIPPING build in lockstep and
// fails the moment their screens differ.
type pair struct {
	t    *testing.T
	h, s *ui.Machine
	keys int
}

func newPair(t *testing.T) *pair {
	t.Helper()
	return &pair{t: t, h: boot(t), s: bootShipping(t)}
}

// diff renders the first differing row of the two screens.
func diff(h, s *ui.Screen) string {
	for row := range 24 {
		if h.Raw[row] != s.Raw[row] {
			return fmt.Sprintf("row %d\n  harness build: %q\n  shipping build: %q\n"+
				"  harness bytes:  % 02x\n  shipping bytes: % 02x",
				row, h.Text(row), s.Text(row), h.Raw[row], s.Raw[row])
		}
	}
	return ""
}

// same asserts the two builds are showing byte-identical screens.
func (p *pair) same(what string) {
	p.t.Helper()
	if d := diff(p.h.Screen(), p.s.Screen()); d != "" {
		p.t.Fatalf("%s: the two builds diverged after %d keystrokes:\n%s\nHARNESS BUILD:\n%s\nSHIPPING BUILD:\n%s",
			what, p.keys, d, p.h.Screen(), p.s.Screen())
	}
}

func (p *pair) key(c byte) {
	p.t.Helper()
	if err := p.h.Key(c); err != nil {
		p.t.Fatalf("harness build, key %q: %v", c, err)
	}
	if err := p.s.Key(c); err != nil {
		p.t.Fatalf("shipping build, key %q: %v", c, err)
	}
	p.keys++
	p.same(fmt.Sprintf("key %q", c))
}

func (p *pair) enter(s string) {
	p.t.Helper()
	for i := range len(s) {
		p.key(s[i])
	}
	p.key(0x0D)
}

// TestShippingScreenParity is the headline: for identical input, the build
// that ships and the build the rest of the suite tests must paint identical
// screens — checked after EVERY keystroke, not just at the end.
func TestShippingScreenParity(t *testing.T) {
	p := newPair(t)
	p.same("cold boot")

	// Referee mode, then a real opening: normal moves, a capture, castling,
	// a rejected illegal move, a takeback and a level change all go through
	// the same keyboard.
	p.enter("s")
	p.enter("s")
	p.enter("n")
	for _, mv := range []string{
		"e2e4", "e7e5", "g1f3", "b8c6", "f1b5", "a7a6",
		"b5c6", "d7c6", "e1g1", "c8g4",
	} {
		p.enter(mv)
	}
	p.enter("a1a3") // illegal: must say so, identically, in both
	if got := p.s.Screen().Text(17); !contains(got, "ILLEGAL MOVE") {
		t.Errorf("row 17 = %q, want ILLEGAL MOVE", strings.TrimSpace(got))
	}
	p.enter("t") // takeback
	p.enter("l")
	p.key('3') // level 3
	p.enter("?")
	t.Logf("after %d identical keystrokes both builds show:\n%s", p.keys, p.s.Screen())

	if got, want := p.h.FEN(), p.s.FEN(); got != want {
		t.Errorf("positions diverged: harness %q, shipping %q", got, want)
	}
	if got, want := strings.Join(p.h.History(), " "), strings.Join(p.s.History(), " "); got != want {
		t.Errorf("histories diverged:\n  harness  %s\n  shipping %s", got, want)
	}
}

// TestShippingEngineParity: the engine's own move must be the same in both
// builds. It is chosen from a search whose eval dither is seeded by the
// ENTROPY accumulator, which the keyboard feeds — so this is really "the two
// keyboard paths collect the same entropy", which is the one place a
// keyboard difference could reach the chess.
func TestShippingEngineParity(t *testing.T) {
	p := newPair(t)
	p.enter("l")
	p.key('1') // fixed depth 2: fast, and still a real search
	p.enter("e2e4")
	if n := p.s.Peek(ui.UIHCNT); n != 2 {
		t.Fatalf("the engine did not reply (UIHCNT = %d)\n%s", n, p.s.Screen())
	}
	p.enter("g1f3")
	p.enter("f1c4")
	h, s := p.h.History(), p.s.History()
	if strings.Join(h, " ") != strings.Join(s, " ") {
		t.Fatalf("the two builds played different games:\n  harness  %v\n  shipping %v", h, s)
	}
	t.Logf("both builds played %v", h)
	for _, u := range []*ui.Machine{p.h, p.s} {
		if u.Peek(u.Defs["SEED"]) == 0 {
			t.Error("SEED is 0: the search ran with the eval dither disabled")
		}
	}
	if a, b := p.h.Peek(p.h.Defs["ENTROPY"]), p.s.Peek(p.s.Defs["ENTROPY"]); a != b {
		t.Errorf("ENTROPY diverged: harness %#02x, shipping %#02x", a, b)
	}
}

// TestShippingFullGame is the deliverable: a complete game to a real
// termination, played on the SHIPPING image through $C000/$C010, refereed
// ply by ply by refchess — and, at every keystroke, screen-identical to the
// HARNESSKBD build the rest of the suite trusts.
func TestShippingFullGame(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the full game in -short")
	}
	const (
		uiLevel    = 1 // the UI's engine: fixed depth 2
		humanDepth = 4 // the scripted opponent
		maxPlies   = 250
	)
	bin, defs := engineBin(t)
	p := newPair(t)
	p.enter("l")
	p.key('0' + uiLevel)

	ref, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	syncRef := func() {
		t.Helper()
		hist := p.s.History()
		for ; seen < len(hist); seen++ {
			m, err := refchess.ParseMove(hist[seen])
			if err != nil {
				t.Fatalf("ply %d: %v", seen, err)
			}
			if err := ref.Make(m); err != nil {
				t.Fatalf("ply %d: the shipping image played %s, which refchess calls illegal: %v\n%s",
					seen, hist[seen], err, p.s.Screen())
			}
		}
		if got, want := normFEN(p.s.FEN()), normFEN(ref.FEN()); got != want {
			t.Fatalf("after ply %d the shipping image has %q and refchess has %q\n%s",
				seen, got, want, p.s.Screen())
		}
	}

	for range maxPlies {
		syncRef()
		if r := p.s.Peek(ui.UIRESULT); r != ui.ResNone {
			break
		}
		if len(ref.LegalMoves()) == 0 {
			t.Fatalf("refchess says there are no legal moves but the UI is still playing\n%s", p.s.Screen())
		}
		mv := refSearchFEN(t, bin, defs, ref.FEN(), humanDepth, byte(2*seen+7))
		if mv == "" {
			t.Fatalf("the scripted opponent found no move in %s", ref.FEN())
		}
		p.enter(mv)
		if int(p.s.Peek(ui.UIHCNT)) == seen {
			t.Fatalf("the shipping image rejected the opponent's legal move %s: %q\n%s",
				mv, strings.TrimSpace(p.s.Screen().Text(17)), p.s.Screen())
		}
	}
	syncRef()

	result := p.s.Peek(ui.UIRESULT)
	s := p.s.Screen()
	t.Logf("SHIPPING IMAGE, FINAL SCREEN after %d plies and %d keystrokes (%s):\n%s",
		p.s.Peek(ui.UIHCNT), p.keys, ui.ResultName(result), s)
	t.Logf("game: %s", strings.Join(p.s.History(), " "))
	t.Logf("emulated IIe time: %.1f s", float64(p.s.M.Cycles)/1020484)

	if result == ui.ResNone {
		t.Fatalf("the game did not terminate within %d plies", maxPlies)
	}
	switch result {
	case ui.ResMate:
		if len(ref.LegalMoves()) != 0 || !ref.InCheck() {
			t.Errorf("the UI says checkmate; refchess says %d legal moves, in check %v",
				len(ref.LegalMoves()), ref.InCheck())
		}
	case ui.ResStale:
		if len(ref.LegalMoves()) != 0 || ref.InCheck() {
			t.Errorf("the UI says stalemate; refchess says %d legal moves, in check %v",
				len(ref.LegalMoves()), ref.InCheck())
		}
	case ui.Res50:
		if ref.HalfmoveClock() < 100 {
			t.Errorf("the UI claims the 50-move rule at halfmove clock %d", ref.HalfmoveClock())
		}
	case ui.ResRep:
		// A real termination, checked by construction against the UI's own
		// hash history.
	default:
		t.Errorf("unexpected termination %s", ui.ResultName(result))
	}
	if got := s.Text(23); !contains(got, "COMMAND?") {
		t.Errorf("a finished game should prompt for a COMMAND, not a move: %q", got)
	}
	// And the two builds agreed the whole way down.
	p.same("end of game")
}

// ---------------------------------------------------------------------------
// Pixels, not bytes.
// ---------------------------------------------------------------------------

// TestInverseVideoPixels verifies the checkerboard the way a player sees it:
// through the IIe character generator, with the character set the image
// actually selected. Until goapple2 modelled ALTCHARSET this could only be
// asserted about BYTES against the documented encoding — which is exactly
// how the missing ALTCHARSET store survived: every byte was right, and every
// byte would have been displayed as something else.
func TestInverseVideoPixels(t *testing.T) {
	u := bootShipping(t)
	if !u.AltCharset() {
		t.Fatal("the image is on the PRIMARY character set: $60-$7F is flashing punctuation there, not inverse lowercase")
	}
	g := chargen.IIe()
	alt := u.AltCharset()
	s := u.Screen()

	// The board is rows 2-9, columns 3-18: 8 ranks x 16 cells, two cells per
	// square. A dark square is inverse video in BOTH its halves.
	for rank := range 8 {
		row := 2 + rank
		for file := range 8 {
			for half := range 2 {
				col := 3 + file*2 + half
				b := s.Raw[row][col]
				// a1 is a dark square, so a8 (rank index 0, file index 0)
				// is a LIGHT one: dark iff rank+file is odd.
				dark := (rank+file)%2 == 1
				lit := g.Lit(b, alt, false)
				class := chargen.Class(b, alt)
				if class == chargen.MouseText || class == chargen.Flashing {
					t.Fatalf("square %c%d half %d: byte %#02x displays as %v on a IIe",
						'a'+file, 8-rank, half, b, class)
				}
				// An inverse cell has a lit background: at least half its 56
				// dots are on. A normal cell has a dark background: fewer
				// than half. No glyph in the set is dense enough to confuse
				// the two.
				if dark != (lit > 28) {
					t.Errorf("square %c%d half %d (byte %#02x, %v): %d of 56 dots lit, want %s square",
						'a'+file, 8-rank, half, b, class, lit,
						map[bool]string{true: "dark (inverse)", false: "light (normal)"}[dark])
				}
			}
		}
	}

	// Now the two channels together, at dot resolution: a black knight on a
	// dark square must be a lowercase 'n' cut out of a lit background, and a
	// white knight on a light square a lit uppercase 'N' on black. Compare
	// the actual dots against the character generator's own glyphs.
	for _, tc := range []struct {
		name        string
		row, col    int
		want        byte // the ASCII the cell should be showing
		wantInverse bool
	}{
		{"black rook a8 (light square)", 2, 3, 'r', false},
		{"black knight b8 (dark square)", 2, 5, 'n', true},
		{"white rook a1 (dark square)", 9, 3, 'R', true},
		{"white knight b1 (light square)", 9, 5, 'N', false},
	} {
		b := s.Raw[tc.row][tc.col]
		gotASCII, ok := chargen.ASCII(b, alt)
		if !ok {
			t.Errorf("%s: byte %#02x has no character meaning", tc.name, b)
			continue
		}
		if gotASCII != tc.want {
			t.Errorf("%s: byte %#02x shows %q, want %q", tc.name, b, gotASCII, tc.want)
		}
		if inv := chargen.Class(b, alt) == chargen.Inverse; inv != tc.wantInverse {
			t.Errorf("%s: inverse = %v, want %v", tc.name, inv, tc.wantInverse)
		}
		// The dots themselves: the cell must equal the glyph for that
		// character in that video sense, with no appeal to the byte value.
		wantByte := tc.want | 0x80 // normal video
		if tc.wantInverse {
			if tc.want >= 'a' {
				wantByte = tc.want // inverse lowercase, alternate set
			} else {
				wantByte = tc.want & 0x3F // inverse uppercase
			}
		}
		got := g.Render(b, alt, false)
		want := g.Render(wantByte, alt, false)
		if got != want {
			t.Errorf("%s: dots\n%s\nwant\n%s", tc.name,
				strings.Join(got[:], "\n"), strings.Join(want[:], "\n"))
		}
	}

	// And a picture of the top-left corner of the board at dot resolution,
	// for the record: rank 8's first four squares.
	t.Logf("rank 8, squares a8-d8, as an Apple IIe would light them:")
	for _, line := range g.RenderLine(s.Raw[2][3:11], alt, false) {
		t.Logf("  %s", line)
	}

	// Nothing anywhere on the screen may flash or come out as MouseText —
	// this is the assertion that fails if the $C00F store is ever deleted.
	// And every byte's meaning under ui.Decode (which is what every OTHER
	// gate in this package reads the screen with) must be the meaning the
	// character generator gives it, so the byte-level assertions elsewhere
	// are anchored to the dots here.
	for row := range 24 {
		for col := range 40 {
			b := s.Raw[row][col]
			c := chargen.Class(b, alt)
			if c == chargen.Flashing || c == chargen.MouseText {
				t.Errorf("row %d col %d: byte %#02x displays as %v", row, col, b, c)
				continue
			}
			wantASCII, ok := chargen.ASCII(b, alt)
			gotASCII, gotInv := ui.Decode(b)
			if !ok || gotASCII != wantASCII || gotInv != (c == chargen.Inverse) {
				t.Errorf("row %d col %d byte %#02x: ui.Decode says (%q, inverse=%v), the IIe shows %q %v",
					row, col, b, gotASCII, gotInv, wantASCII, c)
			}
		}
	}
}

// TestPrimaryCharsetWouldBeWrong records WHY the ALTCHARSET store in
// asm/m8.s is load-bearing: with the character set a IIe powers up on, the
// board's black pieces are flashing punctuation. If someone deletes the
// store, TestInverseVideoPixels fails — and this test explains the failure.
func TestPrimaryCharsetWouldBeWrong(t *testing.T) {
	u := bootShipping(t)
	g := chargen.IIe()
	s := u.Screen()
	b := s.Raw[2][5] // b8: a black knight on a dark square, i.e. $60-$7F
	if b < 0x60 || b >= 0x80 {
		t.Fatalf("b8 byte is %#02x; this test assumes the inverse-lowercase range", b)
	}
	if got := chargen.Class(b, false); got != chargen.Flashing {
		t.Fatalf("on the primary character set %#02x is %v, want flashing", b, got)
	}
	wrong, _ := chargen.ASCII(b, false)
	right, _ := chargen.ASCII(b, true)
	t.Logf("byte %#02x is %q (inverse lowercase) on the ALTERNATE set and a FLASHING %q on the PRIMARY set",
		b, right, wrong)
	if g.Render(b, false, false) == g.Render(b, true, false) {
		t.Error("the two character sets render this byte identically; the model is not distinguishing them")
	}
}
