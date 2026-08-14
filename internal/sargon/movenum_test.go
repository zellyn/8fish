package sargon

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/refchess"
)

// THE MOVE-100 GATE (2026-08-01).
//
// Sargon III renders its move-list number with a two-digit routine that adds
// '0' to each column and never carries out of the tens, so the tens column runs
// off the end of the digits: 100 prints ":0", 110 ";0", 128 "<8". The move
// NUMBER — not the column text — is the driver's commit and move-accepted
// signal, so when the parse rejected those rows the list froze at 99: Sargon's
// replies were on screen and invisible, every injected move looked unaccepted,
// and any game reaching move 100 ended as a "quirk-unresolved" draw. That was
// the whole residual quirk class in the standard-start gauntlets (10/504 =
// 2.0% on 2026-07-29; reproduced here at 30M cycles/move).
//
// The tests below measure the rendering on the real machine, by forcing
// Sargon's own move counter to a value just below a boundary and playing on.
// They are the live half of the gate; parseListNumber's table test is the fast
// half (movelist_test.go).

// moveCounterAddr is the RAM byte holding Sargon III's move number. Found by
// diffing all of main RAM across consecutive moves: it is the only byte that
// increments by exactly one per full move AND equals the number on screen.
// Poking it is a TEST-ONLY device for reaching the boundary in seconds instead
// of playing a hundred real moves; nothing outside these tests writes it.
const moveCounterAddr = 0x1388

// numberedMove is one watched full move: the number Sargon should now display
// and the exact characters it should display it with.
type numberedMove struct {
	no    int
	field string
}

// driveNumbers plays two warm-up moves, forces the move counter to poke, then
// plays one full move per entry in want, requiring at each step that (a) the
// driver still gets a reply, (b) the reply is legal on an independent referee,
// (c) the move-list number column reads back exactly as recorded, and (d) the
// commit signal (LastSargonEntry) reports the right move number.
func driveNumbers(t *testing.T, seed int64, poke byte, want []numberedMove) {
	t.Helper()
	m, err := NewMachine(dskPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.BootToPrompt(); err != nil {
		t.Fatal(err)
	}
	m.HardMode = true
	if err := m.InfiniteLevel(); err != nil {
		t.Fatal(err)
	}
	m.Run(1_000_000)
	m.SargonWhite = true
	res, err := m.StartAsWhite(2_000_000)
	if err != nil {
		t.Fatalf("StartAsWhite: %v\n%s", err, m.Screen())
	}
	ref, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	applySargon := func(tok string) error {
		uci := tokToUCI(tok, true, ref)
		if uci == "" {
			return fmt.Errorf("unreadable token %q", tok)
		}
		mv, err := refchess.ParseMove(uci)
		if err != nil {
			return err
		}
		return ref.Make(mv)
	}
	if err := applySargon(res.SargonText); err != nil {
		t.Fatalf("sargon opening %q: %v", res.SargonText, err)
	}

	rnd := rand.New(rand.NewSource(seed))
	// playFull makes a random legal reply, submits it, and applies Sargon's
	// answer. The seed is fixed and the emulator is cycle-deterministic, so the
	// whole game is reproducible: an early finish is a real failure, not flake.
	playFull := func() error {
		legal := ref.LegalMoves()
		if len(legal) == 0 {
			return fmt.Errorf("our side has no legal move")
		}
		our := legal[rnd.Intn(len(legal))]
		if err := ref.Make(our); err != nil {
			return err
		}
		ponderWindow(m, 1_000_000)
		r, err := m.RequestMove(uciToSargonTok(our.String()), 2_000_000)
		if err != nil {
			return fmt.Errorf("RequestMove(%s): %w (msg=%q)", our.String(), err, r.Message)
		}
		return applySargon(r.SargonText)
	}

	for i := 0; i < 2; i++ {
		if err := playFull(); err != nil {
			t.Fatalf("warm-up move %d: %v\n%s", i, err, m.ScreenDump("warmup"))
		}
	}
	m.Poke(moveCounterAddr, poke)

	for _, w := range want {
		if err := playFull(); err != nil {
			t.Fatalf("move %d: %v\n%s", w.no, err, m.ScreenDump(fmt.Sprintf("move-%d", w.no)))
		}
		if got := int(m.Peek(moveCounterAddr)); got != w.no {
			t.Fatalf("move counter = %d, want %d (test's own premise broke)", got, w.no)
		}
		// The raw characters Sargon painted in the number column of the newest row.
		field := ""
		for r := listTopRow; r < listBotRow; r++ {
			row := m.TextRow(r)
			if strings.TrimSpace(row[:blackColHi]) == "" {
				continue
			}
			field = strings.TrimSpace(row[:listNumberCol])
		}
		if field != w.field {
			t.Errorf("move %d: number column = %q, want %q\n%s", w.no, field, w.field, m.ScreenDump("field"))
		}
		no, tok, ok := m.LastSargonEntry()
		if !ok || no != w.no {
			t.Fatalf("move %d: LastSargonEntry = (%d,%q,%v), want number %d — the commit signal is blind here\n%s",
				w.no, no, tok, ok, w.no, m.ScreenDump("commit-signal"))
		}
	}
}

// TestMoveNumberRendering crosses the 99->100 boundary (tens '0'+10 == ':') and
// keeps playing. Before the parse fix this failed on the FIRST move past 99,
// with "no reply after CTRL-T" — Sargon's reply sitting on the ":0" row.
func TestMoveNumberRendering(t *testing.T) {
	skipUnlessSlow(t)
	driveNumbers(t, 7, 97, []numberedMove{
		{98, "98"}, {99, "99"}, {100, ":0"}, {101, ":1"},
	})
}

// TestMoveNumberRenderingTensRollover crosses 119->120, where the tens
// character steps again (';' -> '<'), up to the last numbers a game can reach
// under the MaxSargonMoves cap.
func TestMoveNumberRenderingTensRollover(t *testing.T) {
	skipUnlessSlow(t)
	driveNumbers(t, 7, 118, []numberedMove{
		{119, ";9"}, {120, "<0"}, {121, "<1"},
	})
}
