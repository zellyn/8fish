package ui_test

import (
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/ui"
)

// Device pondering (asm/m8.s m8ponder, docs/ponder-design.md §11.A). These
// gates prove the on-device Scheme-A ponder is correct where it is
// device-specific: it warms the aux TT for the predicted line, restores the
// board exactly (the make/unmake-around-search snapshot), never commits the
// pondered move, predicts like the Go reference, and gives a warm-TT head
// start on a hit — while leaving the own-move search byte-identical (that is
// TestMicroAB, on engine.bin, green by construction because engine.bin is
// untouched).
//
// THE HARNESS CANNOT RUN A NON-BLOCKING KEYBOARD POLL. TrapMemory breaks Run
// the instant the program reads the keyboard-status trap with no key pending
// (harness/trapmemory.go) — the signal that entkey is blocked. pkclk's ponder
// poll reads the same trap, so under RunToInput the ponder "parks" at its
// first poll. On real hardware the poll just reads $C000 and the search
// continues; that is exactly what pondering needs and what the disk does.
// runPonderToCap models the hardware by DISABLING the trap for the duration of
// one ponder, so the search runs to its walk-away backstop the way it will on
// an Apple IIe when the opponent is slow.

const ponderPredDepth = 3 // asm PPREDDEPTH

// ponderFENs: quiet positions with a stable shallow best reply. Side to move
// is the "human"; the engine is the opponent whose TT the ponder warms.
var devPonderFENs = []struct {
	name string
	fen  string
}{
	{"after 1.e4 e5 2.Nf3", "rnbqkbnr/pppp1ppp/8/4p3/4P3/5N2/PPPP1PPP/RNBQKB1R b KQkq - 1 2"},
	{"Ruy Lopez, black to move", "r1bqkbnr/pppp1ppp/2n5/1B2p3/4P3/5N2/PPPP1PPP/RNBQK2R b KQkq - 3 3"},
	{"Italian, white to move", "r1bqkb1r/pppp1ppp/2n2n2/4p3/2B1P3/5N2/PPPP1PPP/RNBQK2R w KQkq - 4 4"},
}

// enablePonder re-enables pondering (boot() disables it — see m8_test.go) and
// silences the eval dither so a search is deterministic.
func enablePonder(u *ui.Machine) {
	u.Poke(ui.PONDERON, 1)
	u.Poke(u.Defs["SEED"], 0)
}

// setupRootM installs fen with the side to move played by the HUMAN and the
// engine as the opponent, at UIHCNT>0 so m8ponder does not take its first-move
// skip. The board is left exactly at fen, machine parked at the input prompt.
func setupRootM(t *testing.T, u *ui.Machine, fen string) {
	t.Helper()
	u.Poke(ui.UIHUMAN, 0xFF) // referee mode during setup: no engine move fires
	if err := u.SetFEN(fen); err != nil {
		t.Fatalf("SetFEN %q: %v", fen, err)
	}
	side := u.Peek(u.Defs["SIDE"])
	u.Poke(ui.UIHUMAN, side) // the human plays the side to move -> human's turn
	u.Poke(ui.UIHCNT, 1)     // >0: ponder is not skipped as "first move"
	u.Poke(u.Defs["SEED"], 0)
}

// runPonderToCap triggers one m8ponder and lets it run to its walk-away
// backstop (~8 s of estimated clock) by disabling the keyboard trap for the
// duration, so the ponder's poll does not break Run. On return the ponder has
// completed: the board is restored to root+M, PPFROM/PPTO hold the predicted
// reply P, and the aux TT is warm for root+M+P.
func runPonderToCap(t *testing.T, u *ui.Machine) {
	t.Helper()
	// A RETURN re-enters mloop, which reaches m8ponder on the human's turn.
	u.M.SendInput([]byte{0x0D})
	if err := u.RunToInput(); err != nil { // parks at the predictor's first poll
		t.Fatalf("reaching the ponder: %v", err)
	}
	if u.Peek(ui.PONDERING) == 0 {
		t.Fatalf("machine did not enter the ponder (PONDERING=0); board=%s", u.FEN())
	}
	savedStat, savedIn := u.M.Mem.InStatusAddr, u.M.Mem.InAddr
	u.M.Mem.InStatusAddr, u.M.Mem.InAddr = 0, 0 // model the hardware poll
	if _, _, err := u.M.Run(80_000_000); err != nil {
		u.M.Mem.InStatusAddr, u.M.Mem.InAddr = savedStat, savedIn
		t.Fatalf("running the ponder: %v", err)
	}
	u.M.Mem.InStatusAddr, u.M.Mem.InAddr = savedStat, savedIn
	if u.Peek(ui.PONDERING) != 0 {
		t.Fatalf("ponder did not finish within the backstop (PONDERING still set)")
	}
}

// ttWarmMove returns the move stored in the device's aux transposition table
// for fen, reproducing the asm TTADDR indexing (== Go PonderMove): index =
// (HASH1&0x0F)<<8 | HASH0, entry at $4000+index*8, +0..2 verify HASH1..3,
// +3/+4 = from/to, +7 = depth<<2|bound. ok=false = empty/mismatched/no move.
func ttWarmMove(t *testing.T, aux *[0x10000]byte, fen string) (from, to byte, ok bool) {
	t.Helper()
	key, err := book.HashFEN(fen)
	if err != nil {
		t.Fatalf("HashFEN %q: %v", fen, err)
	}
	h0, h1, h2, h3 := byte(key), byte(key>>8), byte(key>>16), byte(key>>24)
	addr := 0x4000 + (int(h0)|int(h1&0x0F)<<8)*8
	e := aux[addr : addr+8]
	if e[7]&0x03 == 0 || e[0] != h1 || e[1] != h2 || e[2] != h3 || e[4] == 0xFF {
		return 0, 0, false
	}
	return e[3], e[4], true
}

// TestPondersByDefault: the shipped image really ponders — m8main leaves
// PONDERON set. ui.Boot captures that shipped default before turning pondering
// off for the harness (which cannot run a non-blocking poll — see
// Machine.PonderDefault).
func TestPondersByDefault(t *testing.T) {
	u, err := ui.Boot(root, nil)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if u.PonderDefault != 1 {
		t.Fatalf("the shipped image booted with PONDERON=%d, want 1 — the disk must ponder", u.PonderDefault)
	}
	s, err := ui.BootShipping(root, nil)
	if err != nil {
		t.Fatalf("boot shipping: %v", err)
	}
	if s.PonderDefault != 1 {
		t.Fatalf("the SHIPPING image booted with PONDERON=%d, want 1", s.PonderDefault)
	}
}

// TestDevicePonderRestoresAndPredicts is the core device-ponder gate. For each
// position it runs one full ponder and asserts the four device-specific
// invariants:
//
//	restore  — the board is byte-identical to root+M afterward (the snapshot,
//	           since the ponder search overwrites P's undo slot and P cannot be
//	           unmade — asm m8ponder ppsave/pprestore).
//	no-commit — the game did not advance (UIHCNT, history unchanged): a ponder
//	           never plays a move.
//	predict  — the device's predicted reply P equals the Go reference's shallow
//	           (source-b) search of root+M at the same depth.
//	warm TT  — the aux TT holds a verified entry for root+M+P: the ponder
//	           warmed exactly the line a hit reaches, and it was never wiped.
func TestDevicePonderRestoresAndPredicts(t *testing.T) {
	bin, defs := engineBin(t)
	for _, tc := range devPonderFENs {
		t.Run(tc.name, func(t *testing.T) {
			u := boot(t)
			enablePonder(u)
			setupRootM(t, u, tc.fen)

			beforeFEN := u.FEN()
			beforePos := u.Position()
			beforeHist := len(u.History())
			beforeHCNT := u.Peek(ui.UIHCNT)

			runPonderToCap(t, u)

			// restore
			if got := u.FEN(); got != beforeFEN {
				t.Errorf("board not restored after ponder:\n got %q\nwant %q", got, beforeFEN)
			}
			afterPos := u.Position()
			if *afterPos != *beforePos {
				t.Errorf("piece-list/board bytes not restored after ponder")
			}
			// no-commit
			if got := u.Peek(ui.UIHCNT); got != beforeHCNT {
				t.Errorf("ponder advanced the game: UIHCNT %d -> %d", beforeHCNT, got)
			}
			if got := len(u.History()); got != beforeHist {
				t.Errorf("ponder committed a move: history len %d -> %d", beforeHist, got)
			}

			// predict: device P vs Go source-(b) shallow search of root+M.
			pFrom, pTo, pFlags := u.Peek(ui.PPFROM), u.Peek(ui.PPTO), u.Peek(ui.PPFLAGS)
			deviceP := chesstest.MoveUCI(pFrom, pTo, pFlags)
			goP := refSearch(t, bin, defs, beforePos, ponderPredDepth, 0)
			if deviceP != goP {
				t.Errorf("predicted reply P: device=%s, Go shallow(d%d)=%s", deviceP, ponderPredDepth, goP)
			}

			// warm TT: an entry for root+M+P must exist (the pondered position).
			childFEN := childFENAfter(t, tc.fen, deviceP)
			wf, wt, ok := ttWarmMove(t, &u.M.Mem.Aux, childFEN)
			if !ok {
				t.Errorf("aux TT has no verified entry for the pondered position root+M+P (%s) — the ponder did not warm it", childFEN)
			} else {
				t.Logf("%s: P=%s, TT warm at root+M+P with move %s",
					tc.name, deviceP, chesstest.MoveUCI(wf, wt, 0))
			}
		})
	}
}

// playHumanMoveUndithered types the human's move and lets the engine reply
// with the eval dither held OFF (SEED pinned 0 the instant entseed installs
// it — the engineMoveUndithered trick), so a fixed-depth reply is
// deterministic. On return the machine is parked at the next prompt.
func playHumanMoveUndithered(t *testing.T, u *ui.Machine, move string) {
	t.Helper()
	seedAddr := u.Defs["SEED"]
	for i := range len(move) { // type the move; no engine move until RETURN
		if err := u.Key(move[i]); err != nil {
			t.Fatalf("typing %q: %v", move, err)
		}
	}
	u.Poke(seedAddr, 0)
	u.M.SendInput([]byte{0x0D})
	for {
		exited, _, err := u.M.RunProfile(1<<16, func(uint16, uint8) {
			if u.M.Mem.Main[seedAddr] != 0 {
				u.M.Mem.Main[seedAddr] = 0
			}
		})
		if err != nil {
			t.Fatalf("engine reply: %v", err)
		}
		if exited || u.M.WaitingForInput() {
			break
		}
	}
	if err := u.RunToInput(); err != nil {
		t.Fatalf("engine reply: %v", err)
	}
}

// legalMoveOtherThan returns a legal move in fen whose UCI is not p.
func legalMoveOtherThan(t *testing.T, fen, p string) string {
	t.Helper()
	pos, err := refchess.ParseFEN(fen)
	if err != nil {
		t.Fatalf("parse %q: %v", fen, err)
	}
	for _, mv := range pos.LegalMoves() {
		if mv.String() != p {
			return mv.String()
		}
	}
	t.Fatalf("no legal move other than %s in %s", p, fen)
	return ""
}

// TestDevicePonderMissClean proves the brief's miss invariant: after pondering
// the predicted reply P, the human actually plays a DIFFERENT move S, and the
// engine's reply to root+M+S is bit-identical to a run that never pondered.
// The warm-but-stale TT (keyed to root+M+P) self-verifies and is ignored.
func TestDevicePonderMissClean(t *testing.T) {
	for _, tc := range devPonderFENs {
		t.Run(tc.name, func(t *testing.T) {
			// --- ponder (miss): warm the TT for root+M+P, then play S != P ---
			u := boot(t)
			enablePonder(u)
			setupRootM(t, u, tc.fen)
			runPonderToCap(t, u)
			p := chesstest.MoveUCI(u.Peek(ui.PPFROM), u.Peek(ui.PPTO), u.Peek(ui.PPFLAGS))
			s := legalMoveOtherThan(t, tc.fen, p)
			u.Poke(ui.PONDERON, 0) // stop pondering; the warm TT stays
			u.Poke(ui.UILEVEL, 2)  // fixed depth 3: a deterministic reply
			playHumanMoveUndithered(t, u, s)
			ponderHist := u.History()

			// --- cold (no ponder): the same S from a fresh, unwarmed TT ---
			c := boot(t) // boot() leaves PONDERON=0
			c.Poke(c.Defs["SEED"], 0)
			c.Poke(ui.UIHUMAN, 0xFF)
			if err := c.SetFEN(tc.fen); err != nil {
				t.Fatal(err)
			}
			c.Poke(ui.UIHUMAN, c.Peek(c.Defs["SIDE"]))
			c.Poke(ui.UILEVEL, 2)
			playHumanMoveUndithered(t, c, s)
			coldHist := c.History()

			if len(ponderHist) < 2 || len(coldHist) < 2 {
				t.Fatalf("expected S + an engine reply; ponder=%v cold=%v", ponderHist, coldHist)
			}
			pReply := ponderHist[len(ponderHist)-1]
			cReply := coldHist[len(coldHist)-1]
			t.Logf("%s: P=%s S=%s  engine reply: pondered=%s cold=%s", tc.name, p, s, pReply, cReply)
			if pReply != cReply {
				t.Errorf("a ponder MISS changed the engine's reply to %s: pondered=%s, no-ponder=%s", s, pReply, cReply)
			}
		})
	}
}

// TestDevicePonderHitHeadStart demonstrates a ponder HIT: after pondering the
// predicted reply P, the human actually plays P, and the engine's timed reply
// to root+M+P reaches a GREATER (never lesser) iterative-deepening depth than
// the same reply searched from a cold TT — the warm TT was reused. This is the
// device analog of the Go bridge's TestPonderHeadStartDepth.
func TestDevicePonderHitHeadStart(t *testing.T) {
	const timedLevel = 5 // 4 s/move on the estimated clock
	deeper := 0
	for _, tc := range devPonderFENs {
		t.Run(tc.name, func(t *testing.T) {
			// warm (hit): ponder root+M+P, then the human plays P.
			u := boot(t)
			enablePonder(u)
			setupRootM(t, u, tc.fen)
			runPonderToCap(t, u)
			p := chesstest.MoveUCI(u.Peek(ui.PPFROM), u.Peek(ui.PPTO), u.Peek(ui.PPFLAGS))
			u.Poke(ui.PONDERON, 0)
			u.Poke(ui.UILEVEL, timedLevel)
			playHumanMoveUndithered(t, u, p)
			warm := u.Peek(u.Defs["CURDEPTH"])

			// cold: same reply, no ponder, unwarmed TT.
			c := boot(t)
			c.Poke(c.Defs["SEED"], 0)
			c.Poke(ui.UIHUMAN, 0xFF)
			if err := c.SetFEN(tc.fen); err != nil {
				t.Fatal(err)
			}
			c.Poke(ui.UIHUMAN, c.Peek(c.Defs["SIDE"]))
			c.Poke(ui.UILEVEL, timedLevel)
			playHumanMoveUndithered(t, c, p)
			cold := c.Peek(c.Defs["CURDEPTH"])

			t.Logf("%s: HIT P=%s  reply depth: cold=%d warm=%d (Δ=%+d)", tc.name, p, cold, warm, int(warm)-int(cold))
			if warm < cold {
				t.Errorf("%s: the warm TT REDUCED the reply depth (%d < %d)", tc.name, warm, cold)
			}
			if warm > cold {
				deeper++
			}
		})
	}
	if deeper == 0 {
		t.Error("no position reached a greater depth on a hit — the warm TT bought no head start")
	}
}

// TestDevicePonderInterruptDiscardsAndTimesKey drives the realistic ponder
// interruption: the deep search is running when the human's key lands. It
// asserts the three device-specific correctness points the brief calls out:
//
//	no-commit   — the move played is the human's S, never the pondered P (the
//	              PONDERKEY guard discards; the round-6 abort-recovery landmine).
//	entropy     — the interrupting key is not consumed by the poll (the strobe
//	              is left set), so entkey still reads AND times it: S is read
//	              correctly and ENTROPY advances.
//	clean think — a ponder abort paints nothing; UITHINK stays blank. Removing
//	              the PONDERKEY guard makes the abort fall into the own-move
//	              recovery (which paints), so this is the mutation-(b) sensor.
func TestDevicePonderInterruptDiscardsAndTimesKey(t *testing.T) {
	for _, tc := range devPonderFENs {
		t.Run(tc.name, func(t *testing.T) {
			u := boot(t)
			enablePonder(u)
			setupRootM(t, u, tc.fen)
			entAddr := uint16(u.Defs["ENTROPY"])
			if got := u.Peek(ui.UITHINK); got != 0 {
				t.Fatalf("UITHINK not blank at setup: %#02x", got)
			}
			setupFEN := u.FEN()
			beforeHCNT := u.Peek(ui.UIHCNT)
			entBefore := uint16(u.Peek(entAddr)) | uint16(u.Peek(entAddr+1))<<8

			// Enter the ponder and run into the DEEP search (past the shallow
			// predictor), then interrupt it with S. The predictor sets PPFROM
			// when it finishes; the deep search of root+M+P follows. Only a
			// deep-search abort exercises the PONDERKEY discard guard.
			u.M.SendInput([]byte{0x0D})
			if err := u.RunToInput(); err != nil { // parks at the predictor's poll
				t.Fatal(err)
			}
			savedStat, savedIn := u.M.Mem.InStatusAddr, u.M.Mem.InAddr
			u.M.Mem.InStatusAddr, u.M.Mem.InAddr = 0, 0 // model the hardware poll
			u.Poke(ui.PPFROM, 0)                        // detect the predictor finishing
			for i := 0; i < 60 && u.Peek(ui.PPFROM) == 0 && u.Peek(ui.PONDERING) != 0; i++ {
				u.M.Run(500_000)
			}
			if u.Peek(ui.PPFROM) == 0 {
				u.M.Mem.InStatusAddr, u.M.Mem.InAddr = savedStat, savedIn
				t.Fatal("the predictor never produced a move")
			}
			u.M.Run(1_000_000) // now inside the deep search of root+M+P
			u.M.Mem.InStatusAddr, u.M.Mem.InAddr = savedStat, savedIn
			if u.Peek(ui.PONDERING) == 0 {
				t.Fatal("deep search ended before the interrupt (nothing to interrupt)")
			}

			// The human plays a real move S. Its first keystroke interrupts the
			// deep ponder search through pkclk.
			s := legalMoveOtherThan(t, tc.fen, "")
			if err := u.Key(s[0]); err != nil { // interrupts the ponder
				t.Fatal(err)
			}
			// no-commit: the ponder discarded and restored root+M; nothing was
			// played (the human has only typed one character so far).
			if got := u.FEN(); got != setupFEN {
				t.Errorf("board changed by the ponder interrupt:\n got %q\nwant %q", got, setupFEN)
			}
			if got := u.Peek(ui.UIHCNT); got != beforeHCNT {
				t.Errorf("ponder interrupt committed a move: UIHCNT %d -> %d", beforeHCNT, got)
			}
			// mutation-(b) sensor: the ponder abort must have painted NOTHING.
			if got := u.Peek(ui.UITHINK); got != 0 {
				t.Errorf("the ponder abort painted the think line (UITHINK=%#02x) — the PONDERKEY discard guard is not holding", got)
			}
			for i := 1; i < len(s); i++ {
				if err := u.Key(s[i]); err != nil {
					t.Fatal(err)
				}
			}
			u.Poke(ui.PONDERON, 0) // no further ponders while we finish the move
			if err := u.Enter(""); err != nil {
				t.Fatal(err)
			}

			// entropy: the key was NOT eaten by the poll (strobe left set), so
			// entkey read AND timed it — S is recorded at its ply and ENTROPY
			// advanced.
			hist := u.History()
			if int(beforeHCNT) >= len(hist) || hist[beforeHCNT] != s {
				t.Errorf("the human's move %s was not read after the interrupt (history=%v) — the poll consumed the key", s, hist)
			}
			entAfter := uint16(u.Peek(entAddr)) | uint16(u.Peek(entAddr+1))<<8
			if entAfter == entBefore {
				t.Errorf("ENTROPY did not advance across the pondered keystroke (%#04x): the poll ate the key's timing", entBefore)
			}
			t.Logf("%s: interrupted deep ponder with S=%s (played, board restored); ENTROPY %#04x -> %#04x",
				tc.name, s, entBefore, entAfter)
		})
	}
}

// childFENAfter returns the FEN after playing uciMove in fen, using the same
// refchess the rest of internal/ui uses as an oracle.
func childFENAfter(t *testing.T, fen, uciMove string) string {
	t.Helper()
	p, err := refchess.ParseFEN(fen)
	if err != nil {
		t.Fatalf("parse %q: %v", fen, err)
	}
	mv, err := refchess.ParseMove(uciMove)
	if err != nil {
		t.Fatalf("parse move %q: %v", uciMove, err)
	}
	if err := p.Make(mv); err != nil {
		t.Fatalf("make %q on %q: %v", uciMove, fen, err)
	}
	return p.FEN()
}
