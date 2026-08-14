package chesstest

import (
	"fmt"
	"math/rand"
	"os"
	"testing"

	"github.com/zellyn/8fish/internal/mirror"
	"github.com/zellyn/8fish/internal/refchess"
)

// TestParityDivergenceProbe is the root-cause harness for a
// TestFullGameMirrorParity divergence: a depth ladder on the offending FEN
// plus a static-eval comparison of the root and every root child, so the
// question "search or eval?" is answered in one run. Point it at a FEN with
//
//	PARITY_PROBE_FEN='<fen>' go test ./internal/chesstest -run DivergenceProbe -v
//
// It is a diagnostic, not a gate: with no FEN set it skips.
func TestParityDivergenceProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	fen := os.Getenv("PARITY_PROBE_FEN")
	if fen == "" {
		t.Skip("set PARITY_PROBE_FEN to a FEN to probe")
	}
	fens := []string{fen}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	probes := parityProbes{search: labels["search"], make_: labels["make"],
		eval: labels["eval"], makenull: labels["makenull"]}
	evalinit, evalAddr := labels["evalinit"], labels["eval"]

	for _, fen := range fens {
		// (a) static eval of the root and every root child.
		ref, err := refchess.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		check := func(f string, tag string) {
			a := asmStaticEval(t, bin, evalinit, evalAddr, f, 0)
			mp, err := mirror.ParseFEN(f)
			if err != nil {
				t.Fatal(err)
			}
			me := mirror.NewEngine()
			me.SetPosition(mp)
			if m := me.Eval(); m != a {
				t.Errorf("STATIC EVAL MISMATCH %s %s: asm=%d mirror=%d", tag, f, a, m)
			}
		}
		check(fen, "root")
		for _, mv := range ref.LegalMoves() {
			c := ref
			if err := c.Make(mv); err != nil {
				continue
			}
			check(c.FEN(), "child "+mv.String())
		}

		// (b) depth ladder, both masks.
		for _, cfg := range parityConfigs {
			for d := byte(1); d <= 4; d++ {
				amv, asc, an, amk, aev, anull, aqs, _, err := asmSearchFixed(bin, probes, fen, d, cfg.features)
				if err != nil {
					t.Fatal(err)
				}
				mp, err := mirror.ParseFEN(fen)
				if err != nil {
					t.Fatal(err)
				}
				me := cfg.mirrorEngine()
				me.SetPosition(mp)
				mb, msc := me.SearchFixed(int(d))
				flag := ""
				if amv != mb.UCI() || asc != msc || an != me.Cyc.Nodes || amk != me.Cyc.Makes {
					flag = "   <<< DIVERGES"
				}
				fmt.Printf("%-34s %-11s d%d asm=%s/%d n=%d mk=%d ev=%d null=%d qs=%d | mirror=%s/%d n=%d mk=%d ev=%d null=%d qs=%d%s\n",
					fen, cfg.name, d, amv, asc, an, amk, aev, anull, aqs,
					mb.UCI(), msc, me.Cyc.Nodes, me.Cyc.Makes, me.Cyc.Evals, me.Cyc.MakeNull, me.QSNodes, flag)
			}
		}
	}
}

// TestPawnlessEvalParity sweeps randomly generated PAWNLESS endgame
// positions and requires the asm's static eval to equal the mirror's
// exactly. Pawnless endgames are where TestFullGameMirrorParity's first
// divergences appeared, and they are the eval shape the fixed FEN suites
// cover least (no pawn-structure terms fire, kings roam onto their back
// ranks, PHASE sits at the insufficient-material boundary).
func TestPawnlessEvalParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: thousands of engine runs")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	evalinit, evalAddr := labels["evalinit"], labels["eval"]

	// Piece sets, white-winning; each is also tried color-flipped.
	sets := [][]byte{
		{'R'}, {'Q'}, {'R', 'R'}, {'B', 'N'}, {'B', 'B'}, {'N', 'N'},
		{'R', 'B'}, {'Q', 'R'}, {'N'}, {'B'},
	}
	rnd := rand.New(rand.NewSource(20260726))
	tested, mismatches := 0, 0
	for i := 0; i < 600; i++ {
		set := sets[rnd.Intn(len(sets))]
		flip := rnd.Intn(2) == 1
		board := map[int]byte{}
		place := func(c byte) bool {
			for try := 0; try < 64; try++ {
				sq := rnd.Intn(64)
				if _, used := board[sq]; used {
					continue
				}
				board[sq] = c
				return true
			}
			return false
		}
		// Kings never adjacent.
		var wk, bk int
		for {
			wk, bk = rnd.Intn(64), rnd.Intn(64)
			df, dr := abs8(wk%8-bk%8), abs8(wk/8-bk/8)
			if wk != bk && (df > 1 || dr > 1) {
				break
			}
		}
		board[wk], board[bk] = 'K', 'k'
		ok := true
		for _, c := range set {
			if flip {
				c |= 0x20 // lowercase = black
			}
			if !place(c) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		side := "w"
		if rnd.Intn(2) == 1 {
			side = "b"
		}
		fen := boardToFEN(board, side)
		// Skip positions where the side NOT to move is in check (illegal).
		p, err := refchess.ParseFEN(fen)
		if err != nil {
			continue
		}
		if sideNotToMoveInCheck(p) {
			continue
		}
		a := asmStaticEval(t, bin, evalinit, evalAddr, fen, 0)
		mp, err := mirror.ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		me := mirror.NewEngine()
		me.SetPosition(mp)
		m := me.Eval()
		tested++
		if a != m {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("pawnless eval mismatch %q: asm=%d mirror=%d (delta %+d)", fen, a, m, a-m)
			}
		}
	}
	fmt.Printf("pawnless static-eval parity: %d positions, %d mismatches\n", tested, mismatches)
}

func abs8(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// sideNotToMoveInCheck reports whether the side that is NOT to move is in
// check — i.e. the position is ILLEGAL and the root has a king capture
// available. refchess's InCheck answers only for the side to move, so this
// flips the side-to-move field of the FEN and asks again.
func sideNotToMoveInCheck(p *refchess.Position) bool {
	// Build the mirrored-side FEN and ask refchess.
	f := p.FEN()
	var flipped string
	for i := 0; i < len(f); i++ {
		if f[i] == ' ' {
			side := f[i+1]
			ns := byte('w')
			if side == 'w' {
				ns = 'b'
			}
			flipped = f[:i+1] + string(ns) + f[i+2:]
			break
		}
	}
	q, err := refchess.ParseFEN(flipped)
	if err != nil {
		return true
	}
	return q.InCheck()
}

// boardToFEN renders a square->piece map (square = rank*8+file, rank 0 =
// rank 1) plus a side to move as a FEN with no castling/ep.
func boardToFEN(board map[int]byte, side string) string {
	out := ""
	for rank := 7; rank >= 0; rank-- {
		empty := 0
		for file := 0; file < 8; file++ {
			if c, ok := board[rank*8+file]; ok {
				if empty > 0 {
					out += fmt.Sprintf("%d", empty)
					empty = 0
				}
				out += string(c)
			} else {
				empty++
			}
		}
		if empty > 0 {
			out += fmt.Sprintf("%d", empty)
		}
		if rank > 0 {
			out += "/"
		}
	}
	return out + " " + side + " - - 0 1"
}
