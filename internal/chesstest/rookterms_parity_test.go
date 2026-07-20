package chesstest

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
	"github.com/zellyn/chess6502/internal/refchess"
)

// ftRookX is FT_ROOKX (asm defs.inc): the experimental rook/blockade
// eval-term gate ported in task #51.
const ftRookX = 0x20

// flipFEN color-mirrors a FEN's board (vertical flip + swap piece
// colors + flip side to move), dropping castling/ep since the extra
// terms ignore both. The white-POV extra-term value of the flipped
// position must be the exact negation of the original's.
func flipFEN(fen string) string {
	fields := strings.Fields(fen)
	ranks := strings.Split(fields[0], "/")
	for i, j := 0, len(ranks)-1; i < j; i, j = i+1, j-1 {
		ranks[i], ranks[j] = ranks[j], ranks[i]
	}
	swap := func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 32
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return r
	}
	for i := range ranks {
		ranks[i] = strings.Map(swap, ranks[i])
	}
	side := "w"
	if fields[1] == "w" {
		side = "b"
	}
	return strings.Join(ranks, "/") + " " + side + " - - 0 1"
}

// TestRookTermsParity: the asm extraterm (XSTRUCT, FT_ROOKX) must equal
// the mirror's ExtraEval with the fixed RookTermsAsm weight set, exactly,
// over thousands of positions from random legal games — AND the term must
// be perfectly color-antisymmetric (flipFEN negates it) in both engines.
// This is the analogue of TestPStructParity for the new term set.
func TestRookTermsParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: thousands of engine runs")
	}
	bin := loadEngine(t)

	asmExtra := func(fen string) int {
		pos, err := ParseFEN(fen)
		if err != nil {
			t.Fatalf("chesstest ParseFEN %q: %v", fen, err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1F|ftRookX) // FtAll + FT_ROOKX
		SetBudget(m, defs, 0, 1)
		if exited, code, err := m.Run(30_000_000_000); err != nil || !exited || code > 2 {
			t.Fatalf("%s: exited=%v code=%d err=%v", fen, exited, code, err)
		}
		return int(int16(uint16(m.Mem.Main[defs["XSTRUCT"]]) |
			uint16(m.Mem.Main[defs["XSTRUCT"]+1])<<8))
	}
	refExtra := func(fen string) int {
		mp, err := mirror.ParseFEN(fen)
		if err != nil {
			t.Fatalf("mirror ParseFEN %q: %v", fen, err)
		}
		me := mirror.NewEngine()
		me.SetPosition(mp)
		me.Extra = mirror.RookTermsAsm
		return me.ExtraEval()
	}

	rng := rand.New(rand.NewSource(0x51ab))
	positions, games := 0, 0
	for games = 0; games < 200 && positions < 3000; games++ {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for ply := 0; ply < 80; ply++ {
			legal := ref.LegalMoves()
			if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
				break
			}
			if err := ref.Make(legal[rng.Intn(len(legal))]); err != nil {
				t.Fatal(err)
			}
			fen := ref.FEN()
			positions++

			want := refExtra(fen)
			got := asmExtra(fen)
			if want != got {
				t.Fatalf("XSTRUCT mismatch at %q: mirror=%d asm=%d", fen, want, got)
			}
			// Color antisymmetry: the flipped position negates the term,
			// in both the reference and the asm.
			flip := flipFEN(fen)
			if rw := refExtra(flip); rw != -want {
				t.Fatalf("mirror asymmetry at %q: got %d, flip %d", fen, want, rw)
			}
			if aw := asmExtra(flip); aw != -got {
				t.Fatalf("asm asymmetry at %q: got %d, flip %d", fen, got, aw)
			}
		}
	}
	t.Logf("rook-terms parity vs mirror + color symmetry: %d positions over %d games, all exact", positions, games)
}
