package ui_test

import (
	"testing"

	"github.com/zellyn/8fish/internal/book"
	"github.com/zellyn/8fish/internal/chesstest"
	"github.com/zellyn/8fish/internal/refchess"
	"github.com/zellyn/8fish/internal/ui"
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
	// A RETURN re-enters mloop; on the human's turn uiread's per-key wait
	// (urdkey) runs the ponder burst.
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
			// deep ponder search through pkclk. PONDERON goes off FIRST so the
			// per-key wait does not start the NEXT burst after the interrupt —
			// the running ponder is unaffected (PONDERON only gates entry), and
			// the machine parks in entkey where the restored board is
			// observable. (Between-burst pondering has its own gate:
			// TestPonderBetweenKeystrokes.)
			u.Poke(ui.PONDERON, 0)
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
			if err := u.Enter(""); err != nil { // PONDERON already off above
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

// ---------------------------------------------------------------------------
// Ponder-in-the-gaps (asm/m8.s urdkey): while the human is on move, the
// engine ponders in BURSTS between his keystrokes instead of once-then-idle.
// Before this change the FIRST keystroke of the turn aborted the (single)
// mloop ponder and the engine then sat idle for the whole move entry; now
// every gap — cursor navigation, mid-line typing, plain thinking — is a live
// ponder search, aborted sub-quantum by the next key through the existing
// pkclk poll.
//
// The park-point is the sensor: with the harness input trap enabled, Run
// breaks at the first EMPTY keyboard poll after a key is handled. If the
// per-key wait started a burst, that poll is pkclk's — INSIDE m8ponder, with
// PONDERING=1. If the wait just blocked (the old behaviour, or a gated
// burst), the poll is entkey's, with PONDERING=0. So "PONDERING after Key()"
// distinguishes pondering-between-keys from idle-between-keys directly.
// ---------------------------------------------------------------------------

// IIe key codes (asm/m8.s uiread), local to this file's gates.
const (
	pkeyUp   = 0x0B
	pkeyDown = 0x0A
	pkeyEsc  = 0x1B
	psqE4    = 0x34 // 0x88 squares
	psqE5    = 0x44
)

// keyParked sends one key and reports PONDERING at the resulting park.
func keyParked(t *testing.T, u *ui.Machine, c byte, what string) byte {
	t.Helper()
	if err := u.Key(c); err != nil {
		t.Fatalf("%s (key %#02x): %v", what, c, err)
	}
	return u.Peek(ui.PONDERING)
}

// finishBurst models the hardware keyboard for one interval, exactly like
// runPonderToCap: the live burst runs to its walk-away backstop and the
// machine falls into the blocking entkey wait.
func finishBurst(t *testing.T, u *ui.Machine) {
	t.Helper()
	savedStat, savedIn := u.M.Mem.InStatusAddr, u.M.Mem.InAddr
	u.M.Mem.InStatusAddr, u.M.Mem.InAddr = 0, 0
	_, _, err := u.M.Run(80_000_000)
	u.M.Mem.InStatusAddr, u.M.Mem.InAddr = savedStat, savedIn
	if err != nil {
		t.Fatalf("running the burst out: %v", err)
	}
	if got := u.Peek(ui.PONDERING); got != 0 {
		t.Fatalf("burst did not finish within the backstop (PONDERING=%d)", got)
	}
}

// TestPonderBetweenKeystrokes is the ponder-in-the-gaps gate. It drives the
// exact scenario the change exists for — the human pops the arrow cursor and
// navigates, with think-gaps between the keys — and asserts the engine is
// PONDERING at every between-key park, that the bursts do real TT work, that
// the board/cursor/entropy invariants all hold across them, that a typed
// move still composes and commits mid-bursts, and that a real ESC screen
// swap ends the bursts for the turn.
//
// Mutation sensors (each verified by reverting the corresponding change):
//   - restoring the old `jsr entkey` wait (no urdkey) fails every
//     PONDERING=1 assertion — that IS the old idle-between-keys behaviour;
//   - restoring the old standalone mloop `jsr m8ponder` on top of it still
//     fails them (the first key aborts the one ponder and the rest is idle);
//   - dropping urdkey's PONDEROK gate fails TestPonderBurstsGated's
//     game-over case; dropping uiswap's PONDEROK clear fails the ESC leg.
func TestPonderBetweenKeystrokes(t *testing.T) {
	tc := devPonderFENs[0]
	u := boot(t)
	enablePonder(u)
	setupRootM(t, u, tc.fen)
	u.Poke(ui.UIHTO, psqE4) // ply-0 destination: where the cursor pops
	rootMFEN := u.FEN()     // the UI's own rendering (fullmove always 1)
	entAddr := u.Defs["ENTROPY"]
	entBefore := uint16(u.Peek(entAddr)) | uint16(u.Peek(entAddr+1))<<8

	// (1) The first arrow pops the cursor AND parks inside a live burst.
	if got := keyParked(t, u, pkeyUp, "pop the cursor"); got != 1 {
		t.Fatalf("after the cursor-pop arrow the machine is not pondering "+
			"(PONDERING=%d, want 1): the engine is idle between keystrokes", got)
	}
	if got := u.Peek(ui.CURACT); got != 1 {
		t.Fatalf("CURACT = %d after the first arrow, want 1", got)
	}
	if got := u.Peek(ui.CURSQ); got != psqE4 {
		t.Fatalf("cursor popped on $%02X, want the last destination e4 ($%02X)", got, psqE4)
	}

	// (2) The next arrow aborts that burst, moves the cursor, and a FRESH
	// burst is live at the park — pondering between EVERY pair of keys.
	if got := keyParked(t, u, pkeyUp, "cursor to e5"); got != 1 {
		t.Fatalf("no fresh burst after the second arrow (PONDERING=%d, want 1)", got)
	}
	if got := u.Peek(ui.CURSQ); got != psqE5 {
		t.Fatalf("CURSQ = $%02X after up, want e5 ($%02X): the key was not handled", got, psqE5)
	}
	entAfter := uint16(u.Peek(entAddr)) | uint16(u.Peek(entAddr+1))<<8
	if entAfter == entBefore {
		t.Errorf("ENTROPY did not advance across ponder-interrupted keystrokes (%#04x)", entBefore)
	}

	// (3) Let the live burst run out on the modelled hardware keyboard: it
	// predicts P, deep-ponders root+M+P to the backstop, and restores.
	finishBurst(t, u)
	if got := u.FEN(); got != rootMFEN {
		t.Fatalf("board not restored to root+M after a between-keys burst:\n got %q\nwant %q", got, rootMFEN)
	}
	if got := u.Peek(ui.CURACT); got != 1 {
		t.Errorf("CURACT = %d after the burst, want 1: the cursor overlay must survive", got)
	}
	if got := u.Peek(ui.CURSQ); got != psqE5 {
		t.Errorf("CURSQ = $%02X after the burst, want e5 ($%02X)", got, psqE5)
	}
	if got := u.Peek(ui.UITHINK); got != 0 {
		t.Errorf("a ponder burst painted the think line (UITHINK=%#02x)", got)
	}
	// ...and it did real work: the aux TT holds a verified entry for the
	// pondered position root+M+P. THIS is the compute the old code threw
	// away while the player navigated.
	p := chesstest.MoveUCI(u.Peek(ui.PPFROM), u.Peek(ui.PPTO), u.Peek(ui.PPFLAGS))
	childFEN := childFENAfter(t, tc.fen, p)
	if _, _, ok := ttWarmMove(t, &u.M.Mem.Aux, childFEN); !ok {
		t.Errorf("no verified aux-TT entry for root+M+P (%s): the between-keys burst warmed nothing", childFEN)
	}

	// (4) Bursts continue after a backstopped one: the next key gets a new one.
	if got := keyParked(t, u, pkeyDown, "cursor back to e4"); got != 1 {
		t.Fatalf("no burst after a backstopped interval (PONDERING=%d, want 1)", got)
	}
	if got := u.Peek(ui.CURSQ); got != psqE4 {
		t.Fatalf("CURSQ = $%02X after down, want e4 ($%02X)", got, psqE4)
	}

	// (5) ESC with the cursor up CANCELS the cursor — and pondering carries
	// on: only a commit or a real screen swap ends it.
	if got := keyParked(t, u, pkeyEsc, "cancel the cursor"); got != 1 {
		t.Fatalf("ESC cursor-cancel stopped the bursts (PONDERING=%d, want 1)", got)
	}
	if got := u.Peek(ui.CURACT); got != 0 {
		t.Fatalf("CURACT = %d after ESC, want 0 (cancelled)", got)
	}

	// (6) Typed entry composes mid-bursts: each character of the human's
	// real move is handled between live bursts, and RETURN commits it
	// through uidispatch exactly as before.
	for _, c := range []byte("b8c6") {
		if got := keyParked(t, u, c, "typing b8c6"); got != 1 {
			t.Fatalf("no burst while typing %q (PONDERING=%d, want 1)", c, got)
		}
	}
	u.Poke(ui.UILEVEL, 2) // fixed depth: a fast deterministic reply
	if err := u.Key(0x0D); err != nil {
		t.Fatalf("RETURN: %v", err)
	}
	if got := u.Peek(ui.UIHCNT); got != 3 {
		t.Fatalf("UIHCNT = %d after b8c6 + reply, want 3: the move did not commit "+
			"and play through the engine", got)
	}
	// The reply's turn ended in the next human interval: parked pondering.
	if got := u.Peek(ui.PONDERING); got != 1 {
		t.Errorf("after the engine's reply the new turn is not pondering (PONDERING=%d)", got)
	}

	// (7) A REAL ESC screen swap ends pondering for the turn. The harness
	// image boots with UIDHGRDEF=0 (BLOAD path: ESC is a no-op and bursts
	// must survive it); poking it on arms the swap the disk build has.
	dhgrdef, ok := u.Lbl["UIDHGRDEF"]
	if !ok {
		t.Fatal("no UIDHGRDEF label in m8t.lbl")
	}
	if got := keyParked(t, u, pkeyEsc, "no-op ESC (BLOAD build)"); got != 1 {
		t.Fatalf("a NO-OP ESC (no artwork) ended the bursts (PONDERING=%d, want 1)", got)
	}
	u.Poke(dhgrdef, 1)
	if got := keyParked(t, u, pkeyEsc, "real ESC swap"); got != 0 {
		t.Fatalf("a real ESC screen swap did not end the bursts (PONDERING=%d, want 0)", got)
	}
	if got := u.Peek(ui.PONDEROK); got != 0 {
		t.Errorf("PONDEROK = %d after the swap, want 0", got)
	}
	if got := keyParked(t, u, pkeyUp, "arrow after the swap"); got != 0 {
		t.Fatalf("bursts resumed within the turn after an ESC swap (PONDERING=%d, want 0)", got)
	}
	t.Log("the engine ponders in every between-keystroke gap: cursor pops/moves, " +
		"ESC-cancel and typing all park mid-burst; the burst warms the TT and " +
		"restores; commit and a real ESC swap end it")
}

// TestPonderKeyResponse gates the RESPONSIVENESS of pondering-between-keys:
// a keystroke that lands while a burst is live must be read within
// keyRespBudget cycles. The per-node cost model (asm/defs.inc SOFTA/SOFTB:
// ~3,084+71*phase cycles per counted node) makes checkclock's native
// 128-node poll quantum worth ~0.5 s and a fresh burst's 256-node lead-in
// ~1 s — measured 0.53-1.16 s key-to-read before the pkclk cadence fix
// (PKQUANT + the uidrive first-poll arming). With bursts in EVERY
// between-keystroke gap that lag would be on every arrow of a cursor walk,
// so it is gated, at both ends of a burst:
//
//	r=0     the key lands immediately after a park early in a fresh burst
//	        (rapid navigation) — this leg fails if uidrive's first-poll
//	        arming is dropped (the 256-node lead-in returns);
//	r>=1M   the key lands deep in the ponder search — these legs fail if
//	        pkclk stops re-arming NODECNT to PKQUANT (128-node cadence).
func TestPonderKeyResponse(t *testing.T) {
	const keyRespBudget = 200_000 // cycles ~= 0.2 s; measured ~40-120K fixed
	tc := devPonderFENs[0]
	for _, r := range []uint64{0, 1_000_000, 5_000_000} {
		u := boot(t)
		enablePonder(u)
		setupRootM(t, u, tc.fen)
		u.Poke(ui.UIHTO, psqE4)
		if got := keyParked(t, u, pkeyUp, "pop the cursor"); got != 1 {
			t.Fatalf("r=%d: not pondering after the arrow (PONDERING=%d)", r, got)
		}
		// Advance r cycles deeper into the burst on the modelled hardware
		// keyboard, then land the next key and time until the image READS it
		// (the input queue drains at entkey's data read — by then the abort
		// has unwound and the key handler is about to run).
		savedStat, savedIn := u.M.Mem.InStatusAddr, u.M.Mem.InAddr
		u.M.Mem.InStatusAddr, u.M.Mem.InAddr = 0, 0
		if _, _, err := u.M.Run(r); err != nil {
			t.Fatal(err)
		}
		u.M.Mem.InStatusAddr, u.M.Mem.InAddr = savedStat, savedIn
		live := u.Peek(ui.PONDERING)
		u.M.SendInput([]byte{pkeyDown})
		c0 := u.M.Cycles
		for len(u.M.Mem.Input) > 0 {
			if _, _, err := u.M.Run(4096); err != nil {
				t.Fatal(err)
			}
			if u.M.Cycles-c0 > 5_000_000 {
				break
			}
		}
		lat := u.M.Cycles - c0
		t.Logf("r=%8d into the burst (PONDERING=%d): key read after %6d cycles = %5.1f ms",
			r, live, lat, float64(lat)/1020.484)
		if lat > keyRespBudget {
			t.Errorf("r=%d: the keystroke waited %d cycles (%.0f ms) for the ponder "+
				"burst — over the %d-cycle response budget",
				r, lat, float64(lat)/1020.484, keyRespBudget)
		}
	}
}

// TestPonderBurstsGated proves the bursts stay OFF everywhere they must:
// the game-over command prompt (the PONDEROK gate itself — m8ponder's own
// guards would all pass there), two-player mode, in the big book, and before
// the first engine move. Each leg ends with a control that flips the gate
// back and sees a burst, so a vacuous sensor cannot pass.
func TestPonderBurstsGated(t *testing.T) {
	tc := devPonderFENs[0]

	t.Run("game over", func(t *testing.T) {
		u := boot(t)
		enablePonder(u)
		setupRootM(t, u, tc.fen)
		// Resign: UIRESULT latches and the main loop re-enters uiread on the
		// game-over path with PONDEROK off. Every m8ponder guard still
		// passes here (PONDERON=1, UIHCNT=1, UIHUMAN=side, no big book), so
		// only urdkey's PONDEROK gate keeps the burst from firing.
		if err := u.Enter("r"); err != nil {
			t.Fatalf("resign: %v", err)
		}
		if got := u.Peek(ui.UIRESULT); got != ui.ResResign {
			t.Fatalf("UIRESULT = %d after resign, want %d", got, ui.ResResign)
		}
		if got := keyParked(t, u, pkeyUp, "arrow at the game-over prompt"); got != 0 {
			t.Fatalf("the engine ponders at the GAME-OVER prompt (PONDERING=%d, want 0)", got)
		}
	})

	t.Run("two player", func(t *testing.T) {
		u := boot(t)
		enablePonder(u)
		setupRootM(t, u, tc.fen)
		u.Poke(ui.UIHUMAN, 0xFF)
		if got := keyParked(t, u, pkeyUp, "arrow in two-player mode"); got != 0 {
			t.Fatalf("the engine ponders in TWO-PLAYER mode (PONDERING=%d, want 0)", got)
		}
	})

	t.Run("in book", func(t *testing.T) {
		u := boot(t)
		enablePonder(u)
		setupRootM(t, u, tc.fen)
		u.Poke(ui.BIGBOOKOK, 1) // in the big book: a ponder would write the
		if got := keyParked(t, u, pkeyUp, "arrow while in book"); got != 0 {
			t.Fatalf("the engine ponders IN BOOK (PONDERING=%d, want 0) — "+
				"the TT it writes IS the book", got)
		}
		u.Poke(ui.BIGBOOKOK, 0) // control: out of book the same key ponders
		if got := keyParked(t, u, pkeyUp, "arrow out of book"); got != 1 {
			t.Fatalf("control failed: out of book the arrow should ponder "+
				"(PONDERING=%d, want 1) — the sensor is vacuous", got)
		}
	})

	t.Run("first move", func(t *testing.T) {
		u := boot(t) // start position, human White on move, UIHCNT=0
		enablePonder(u)
		if got := keyParked(t, u, pkeyUp, "arrow before any move"); got != 0 {
			t.Fatalf("the engine ponders before its first move (PONDERING=%d, want 0)", got)
		}
		u.Poke(ui.UIHCNT, 1) // control: with a move on the books it ponders
		if got := keyParked(t, u, pkeyUp, "arrow with UIHCNT=1"); got != 1 {
			t.Fatalf("control failed: with UIHCNT=1 the arrow should ponder "+
				"(PONDERING=%d, want 1) — the sensor is vacuous", got)
		}
	})
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
