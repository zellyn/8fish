package chesstest

import (
	"math/rand"
	"testing"

	"github.com/zellyn/8fish/internal/refchess"
)

var passedBonus = [8]int{0, 15, 0, 21, 50, 52, 20, 0} // keep = cmd/gentables (Texel-tuned, diversified corpus)

// refPStruct computes the intended pawn-structure + king-shield term
// (white POV) from a position: the reference model for the asm
// pawnterm. Semantics: doubled = flat -12 for >= 2 own pawns on a
// file; isolated = -7 when no own pawns on adjacent files; passed =
// bonus by the most advanced own pawn's rank when no enemy pawn on
// files f-1..f+1 at rank >= it (white; <= for black, adjacent
// same-rank enemy pawns block); king shield (white king on rank 0 /
// black on rank 7 only) = +3 per own-pawn-occupied file of f-1..f+1,
// -4 when the king's own file has no own pawn; black mirrors signs.
func refPStruct(pos *Position) int16 {
	var wbits, bbits [8]byte
	var wk, bk byte
	for sq := 0; sq < 128; sq++ {
		if sq&0x88 != 0 {
			continue
		}
		p := pos.Board[sq]
		if p == 0 {
			continue
		}
		file, rank := sq&7, sq>>4
		switch {
		case p&0x07 == 1 && p&0x08 == 0:
			wbits[file] |= 1 << rank
		case p&0x07 == 1:
			bbits[file] |= 1 << rank
		case p&0x07 == 6 && p&0x08 == 0:
			wk = byte(sq)
		case p&0x07 == 6:
			bk = byte(sq)
		}
	}
	neighbors := func(bits *[8]byte, f int) byte {
		var n byte
		if f > 0 {
			n |= bits[f-1]
		}
		if f < 7 {
			n |= bits[f+1]
		}
		return n
	}
	or3 := func(bits *[8]byte, f int) byte { return neighbors(bits, f) | bits[f] }
	acc := 0
	for f := 0; f < 8; f++ {
		if wb := wbits[f]; wb != 0 {
			if wb&(wb-1) != 0 {
				acc -= 12
			}
			if neighbors(&wbits, f) == 0 {
				acc -= 7
			}
			hi := 7
			for wb&(1<<hi) == 0 {
				hi--
			}
			if or3(&bbits, f)&(0xFF<<hi) == 0 {
				acc += passedBonus[hi]
			}
		}
		if bb := bbits[f]; bb != 0 {
			if bb&(bb-1) != 0 {
				acc += 12
			}
			if neighbors(&bbits, f) == 0 {
				acc += 7
			}
			lo := 0
			for bb&(1<<lo) == 0 {
				lo++
			}
			if or3(&wbits, f)&byte(1<<(lo+1)-1) == 0 {
				acc -= passedBonus[7-lo]
			}
		}
	}
	shield := func(bits *[8]byte, f int) (n int, open bool) {
		for _, ff := range []int{f - 1, f, f + 1} {
			if ff >= 0 && ff <= 7 && bits[ff] != 0 {
				n++
			}
		}
		return n, bits[f] == 0
	}
	if wk>>4 == 0 {
		n, open := shield(&wbits, int(wk&7))
		acc += 3 * n
		if open {
			acc -= 4
		}
	}
	if bk>>4 == 7 {
		n, open := shield(&bbits, int(bk&7))
		acc -= 3 * n
		if open {
			acc += 4
		}
	}
	return int16(acc)
}

// TestPStructParity: the asm pawnterm must match the Go reference
// model exactly over thousands of positions from random legal games.
// (Originally an old-binary-vs-new comparison; that gate exposed two
// bugs in the old implementation — an h-file isolated-pawn flag bug
// and a king-shield scratch clobber — so the reference model is now
// the specification.)
func TestPStructParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: thousands of engine runs")
	}
	bin := loadEngine(t)
	pstruct := func(pos *Position) int16 {
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetBudget(m, defs, 0, 1)
		if exited, code, err := m.Run(30_000_000_000); err != nil || !exited || code > 2 {
			t.Fatalf("exited=%v code=%d err=%v", exited, code, err)
		}
		return int16(uint16(m.Mem.Main[defs["PSTRUCT"]]) |
			uint16(m.Mem.Main[defs["PSTRUCT"]+1])<<8)
	}

	rng := rand.New(rand.NewSource(65021))
	positions, games := 0, 0
	for games = 0; games < 120 && positions < 4000; games++ {
		ref, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for ply := 0; ply < 60; ply++ {
			legal := ref.LegalMoves()
			if len(legal) == 0 || ref.HalfmoveClock() >= 100 {
				break
			}
			if err := ref.Make(legal[rng.Intn(len(legal))]); err != nil {
				t.Fatal(err)
			}
			pos, err := ParseFEN(ref.FEN())
			if err != nil {
				t.Fatal(err)
			}
			positions++
			if want, got := refPStruct(pos), pstruct(pos); want != got {
				t.Fatalf("PSTRUCT mismatch at %q: model=%d asm=%d", ref.FEN(), want, got)
			}
		}
	}
	t.Logf("PSTRUCT parity vs reference model: %d positions over %d games, all exact", positions, games)
}
