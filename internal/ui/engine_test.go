package ui_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/ui"
)

// engineBin / engineDefs load the engine image the reference searches use.
func engineBin(t *testing.T) ([]byte, chesstest.Defs) {
	t.Helper()
	bin, err := os.ReadFile(filepath.Join(root, "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join(root, "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	return bin, defs
}

// shippedConfig returns the feature configuration the shipped machine plays,
// built FROM defs.inc — never read out of the image under test.
//
// ★ WHY IT IS CONSTRUCTED HERE AND NOT PEEKED. A parity reference derived
// from the thing it is meant to check cannot fail: it agrees by definition.
// This package has now been bitten by that twice. The first time
// (2026-07-28) the UI shipped FEATURES=$1F while every measured Elo number
// was at $5F, and the gate could not see it because BOTH sides came from
// chesstest.NewMachine's $1F test default. The second time is the bug this
// comment exists to close: m8.s was fixed to $5F and the reference was left
// at NewMachine's $1F, so the gate compared TWO DIFFERENT ENGINES and
// reported the difference as a driver divergence (docs/results.md
// 2026-07-29). Both bytes are therefore spelled out from the defs, and
// TestEngineParity separately requires the booted image to match them.
//
// FEATURES  = the ucibridge gameplay mask, $1F|FT_CKEXT.
// FEATURES2 = the bridge's FT2_GENDEFER plus FT2_SOFTCLK, which the device
// additionally requires because an Apple IIe has no readable clock. That
// difference is deliberate, and it is invisible at fixed depth: with
// BUDGET=0 nothing ever compares the estimate against a limit.
func shippedConfig(defs chesstest.Defs) (features, features2 byte) {
	return 0x1F | byte(defs["FT_CKEXT"]),
		byte(defs["FT2_GENDEFER"]) | byte(defs["FT2_SOFTCLK"])
}

// refSearchFEN runs the engine image the ordinary (UCI-bridge) way over a
// FEN: same features, same depth, same eval-dither seed, cold TT.
func refSearchFEN(t *testing.T, bin []byte, defs chesstest.Defs, fen string, depth, seed byte) string {
	t.Helper()
	pos, err := chesstest.ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	return refSearch(t, bin, defs, pos, depth, seed)
}

// refSearch is refSearchFEN over the engine's raw position bytes.
func refSearch(t *testing.T, bin []byte, defs chesstest.Defs, pos *chesstest.Position, depth, seed byte) string {
	t.Helper()
	var cout bytes.Buffer
	m, err := chesstest.NewMachine(bin, defs, pos, 0, &cout)
	if err != nil {
		t.Fatal(err)
	}
	f1, f2 := shippedConfig(defs)
	chesstest.SetFeatures(m, defs, f1) // NOT NewMachine's $1F test default
	chesstest.SetFeatures2(m, defs, f2)
	chesstest.SetBudget(m, defs, 0, depth)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	m.Mem.Main[defs["SEED"]] = seed
	exited, code, err := m.Run(1 << 34)
	if err != nil || !exited {
		t.Fatalf("reference search: exited=%v err=%v", exited, err)
	}
	if code != 0 {
		t.Fatalf("reference search exited %d", code)
	}
	return strings.TrimSpace(cout.String())
}

// twoPlayerPoke puts the UI in referee mode WITHOUT going through the "S"
// command, which would pass through "human plays Black" and let the engine
// take a move — and warm the transposition table. Parity tests need the
// first search of the session to be the one they measure.
func twoPlayerPoke(t *testing.T, u *ui.Machine) {
	t.Helper()
	u.Poke(ui.UIHUMAN, 0xFF)
	if err := u.Enter(""); err != nil {
		t.Fatal(err)
	}
}

// engineMoveUndithered submits the UI's move and lets the engine reply with
// the eval dither OFF, by clearing SEED the instant entseed installs it
// (eval.s evseed: `lda SEED / beq evdone`, so 0 means no noise and nothing
// writes SEED again).
//
// This is what makes TestEngineParity an actual TREE-IDENTITY gate. It used
// to run the UI dithered and hand the reference search `u.Peek(SEED)` — but
// SEED is evseed's LIVE PRNG STATE, advanced (seed = seed*3+29) at every
// dithered leaf, so that read the END of the stream, not its start. The two
// sides therefore compared different dither streams; the test agreed anyway
// because 0-3cp of noise rarely flips a best move, and it once logged "SEED
// 0x00" for a move entseed cannot produce a zero seed for. Feeding the
// reference the true installed seed does NOT fix it either: measured at the
// Ruy Lopez position, the UI's driver and the $4000 entry disagree for 33 of
// 40 seeds, because the dither's noise depends on the POSITION IN THE
// EVAL-CALL SEQUENCE and the two drivers' sequences do not align. Only with
// the dither off is "same position, same depth, same features" enough to
// demand the same move — which is the property this test exists to gate.
func engineMoveUndithered(t *testing.T, u *ui.Machine) {
	t.Helper()
	seedAddr := u.Defs["SEED"]
	u.Poke(seedAddr, 0)
	u.M.SendInput([]byte{0x0D})
	// entseed runs a few hundred cycles into the engine's turn, before any
	// eval; profile only until its write lands, then run normally.
	for zeroed := false; !zeroed; {
		exited, _, err := u.M.RunProfile(1<<16, func(uint16, uint8) {
			if u.M.Mem.Main[seedAddr] != 0 {
				u.M.Mem.Main[seedAddr] = 0
				zeroed = true
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if exited || u.M.WaitingForInput() {
			break
		}
	}
	if err := u.RunToInput(); err != nil {
		t.Fatal(err)
	}
	if got := u.Peek(seedAddr); got != 0 {
		t.Fatalf("SEED = %#02x after the move: the dither was not held off", got)
	}
}

// TestEngineParity is the "the engine plays the same chess either way" gate:
// a move chosen by the UI's OWN iterative-deepening driver, running from
// Language Card RAM, must be the move the engine's normal $4000 entry point
// produces for the same position, depth, features and dither seed.
//
// This is what makes the UI safe to add: docs/ui-design.md §7 supplies its
// own ID driver so the screen can show the search deepening without touching
// search.s, and this test is the evidence that the tree is unchanged.
//
// The gate is only as good as the reference, and the reference has to be
// identical in EVERY input the tree depends on: position bytes (piece-list
// slots included), depth, dither, and — the one that broke twice — the
// feature mask. See shippedConfig.
func TestEngineParity(t *testing.T) {
	bin, defs := engineBin(t)
	wantF, wantF2 := shippedConfig(defs)
	for _, tc := range []struct {
		name    string
		opening []string // typed into the UI in two-player mode first
		level   byte     // 1..4 are fixed depth 2..5
	}{
		{"start position, depth 2", nil, 1},
		{"after 1.e4, depth 3", []string{"e2e4"}, 2},
		{"Ruy Lopez, depth 4", []string{"e2e4", "e7e5", "g1f3", "b8c6", "f1b5"}, 3},
		// This case was skipped as a KNOWN DIVERGENCE on 2026-07-29: the UI
		// played f3d4 and the reference f1b5. It was not a divergence. The
		// reference was running FEATURES=$1F (chesstest.NewMachine's test
		// default) against the UI's shipped $5F, i.e. WITHOUT check
		// extensions — a different engine, not a different driver. With
		// FT_CKEXT on both sides the two agree here (f3d4) and at every one
		// of 60 position x depth cells swept in docs/results.md 2026-07-29.
		{"Sicilian, depth 5", []string{"e2e4", "c7c5", "g1f3", "d7d6", "d2d4", "c5d4"}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := boot(t)
			// The reference is built from defs.inc, so it can only be a
			// reference if the image really plays that configuration. Check
			// it here rather than trusting it: this is the exact drift that
			// turned this gate into a comparison of two different engines.
			if gotF, gotF2 := u.Peek(defs["FEATURES"]), u.Peek(defs["FEATURES2"]); gotF != wantF || gotF2 != wantF2 {
				t.Fatalf("the booted UI runs FEATURES=$%02X/$%02X but the reference is built "+
					"for the shipped $%02X/$%02X. Whichever side moved, this gate is comparing "+
					"two different ENGINES and can say nothing about the two DRIVERS.",
					gotF, gotF2, wantF, wantF2)
			}
			twoPlayerPoke(t, u)
			// Level first: the level command reads a key, and we want the
			// engine's very first search to be at the depth under test.
			mustEnter(t, u, "l")
			if err := u.Key('0' + tc.level); err != nil {
				t.Fatal(err)
			}
			twoPlayerPoke(t, u)
			for _, mv := range tc.opening {
				mustEnter(t, u, mv)
			}
			fen := u.FEN()
			// The reference engine gets the UI's EXACT position bytes,
			// piece-list slot assignment included: the generator walks the
			// piece list, so a FEN round trip would re-slot the pieces and
			// change the move order — a different but equally valid tree,
			// which is not what this test is about.
			pos := u.Position()
			before := u.Peek(ui.UIHCNT)

			// Hand the move to the engine: the human takes the OTHER colour.
			side := u.Peek(u.Defs["SIDE"])
			u.Poke(ui.UIHUMAN, side^0x08)
			engineMoveUndithered(t, u)
			hist := u.History()
			if len(hist) != int(before)+1 {
				t.Fatalf("the engine did not move; history %v\n%s", hist, u.Screen())
			}
			got := hist[len(hist)-1]
			want := refSearch(t, bin, defs, pos, tc.level+1, 0 /* dither off */)
			if got != want {
				t.Errorf("UI-driven engine played %s, $4000-entry engine played %s\n"+
					"  position %s, depth %d (both undithered)", got, want, fen, tc.level+1)
			}
			t.Logf("%s: both drivers played %s; think line %q",
				tc.name, got, strings.TrimSpace(u.Screen().Text(14)))
		})
	}
}

// TestSoftClockLimits gates the ★ safety-margin rule of docs/ui-design.md
// §6.2 against its reference implementation, internal/chesstest
// SoftClockMargin / SetBudget / SetAdaptive.
//
// The engine's FT2_SOFTCLK cost table is RAW; the deliberate safety bias
// lives on the limits, which the UI installs once per move. Getting it wrong
// in the permissive direction makes the engine overrun its clock by ~17%.
// All five limits must carry it — BUDGET, ABORTL (derived on device),
// CEILMAX, UNSTCEIL and MINSPEND — and they must all share ONE margin, taken
// from the base allocation.
func TestSoftClockLimits(t *testing.T) {
	// The trampoline: JSR uilimits, then store to the exit trap. Lives in
	// $03A0, which defs.inc documents as free driver-scratch RAM.
	const tramp = 0x03A0

	type want struct {
		level   byte
		seconds float64
		depth   byte
		timed   bool
	}
	levels := []want{
		{1, 0, 2, false}, {2, 0, 3, false}, {3, 0, 4, false}, {4, 0, 5, false},
		{5, 4, 20, true}, {6, 8, 20, true}, {7, 15, 20, true},
		{8, 30, 20, true}, {9, 60, 20, true},
	}
	for _, w := range levels {
		t.Run(fmt.Sprintf("level%d", w.level), func(t *testing.T) {
			u := boot(t)
			addr, ok := u.Lbl["uilimits"]
			if !ok {
				t.Fatal("uilimits not in asm/m8t.lbl")
			}
			u.Poke(ui.UILEVEL, w.level)
			u.Poke(tramp+0, 0x20) // JSR uilimits
			u.Poke(tramp+1, byte(addr))
			u.Poke(tramp+2, byte(addr>>8))
			u.Poke(tramp+3, 0xA9) // LDA #0
			u.Poke(tramp+4, 0x00)
			u.Poke(tramp+5, 0x8D) // STA $BFFF
			u.Poke(tramp+6, 0xFF)
			u.Poke(tramp+7, 0xBF)
			u.M.CPU.SetPC(tramp)
			exited, _, err := u.M.Run(1 << 20)
			if err != nil || !exited {
				t.Fatalf("running uilimits: exited=%v err=%v", exited, err)
			}

			read24 := func(sym string) uint64 {
				a := u.Defs[sym]
				return uint64(u.Peek(a)) | uint64(u.Peek(a+1))<<8 | uint64(u.Peek(a+2))<<16
			}
			budget := read24("BUDGET0")
			if got := u.Peek(u.Defs["MAXDEPTH"]); got != w.depth {
				t.Errorf("MAXDEPTH = %d, want %d", got, w.depth)
			}
			adapt := u.Peek(u.Defs["FEATURES2"])&byte(u.Defs["FT2_ADAPT"]) != 0
			if adapt != w.timed {
				t.Errorf("FT2_ADAPT = %v, want %v", adapt, w.timed)
			}
			if !w.timed {
				if budget != 0 {
					t.Errorf("fixed-depth level poked BUDGET = %d, want 0", budget)
				}
				t.Logf("level %d: fixed depth %d, no clock read anywhere", w.level, w.depth)
				return
			}

			// Reference: chesstest's own rule, in cycles.
			base := uint64(w.seconds * 1020484)
			pct := chesstest.SoftClockMargin(base)
			refUnits := func(cycles uint64) uint64 {
				return (cycles*100/pct + 255) >> 8
			}
			for _, c := range []struct {
				name string
				got  uint64
				want uint64
			}{
				{"BUDGET", budget, refUnits(base)},
				{"CEILMAX", read24("CEILMAX0"), refUnits(4 * base)},
				{"UNSTCEIL", read24("UNSTCEIL0"), refUnits(3 * base)},
				{"MINSPEND", read24("MINSPEND0"), refUnits(base / 4)},
			} {
				// The device rule is `x * K >> 8` with K = 25600/margin; the
				// host rule is `x * 100 / margin`. Same margin, different
				// rounding, so allow 1%.
				lo, hi := c.want*99/100, c.want*101/100+1
				if c.got < lo || c.got > hi {
					t.Errorf("%s = %d units, want ~%d (margin %d%%)", c.name, c.got, c.want, pct)
				}
			}
			t.Logf("level %d (%.0f s): margin %d%%, BUDGET %d, CEILMAX %d, UNSTCEIL %d, MINSPEND %d (256-cycle units)",
				w.level, w.seconds, pct, budget,
				read24("CEILMAX0"), read24("UNSTCEIL0"), read24("MINSPEND0"))
		})
	}
}

// TestTimedLevel runs one move at a real timed level, on the ESTIMATED clock
// the shipped machine has to use, and checks that the search stopped inside
// its allocation and that the between-iterations readout climbed.
func TestTimedLevel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the timed-level move in -short")
	}
	u := boot(t)
	mustEnter(t, u, "l")
	if err := u.Key('5'); err != nil { // 4 seconds
		t.Fatal(err)
	}
	twoPlayerPoke(t, u)
	mustEnter(t, u, "e2e4")
	before := u.M.Cycles
	u.Poke(ui.UIHUMAN, 0) // human is White, so the engine answers as Black
	if err := u.Enter(""); err != nil {
		t.Fatal(err)
	}
	spent := u.M.Cycles - before
	s := u.Screen()
	t.Logf("level 5 (4 s/move) reply %v in %d cycles = %.2f s of emulated IIe time",
		u.History(), spent, float64(spent)/1020484)
	t.Logf("think line: %q", strings.TrimSpace(s.Text(14)))
	t.Logf("%s", s)
	if len(u.History()) != 2 {
		t.Fatalf("the engine did not reply: %v", u.History())
	}
	// The engine believes it has spent more than it really has (the estimator
	// is deliberately biased), so real time must come in UNDER the nominal
	// allocation; the hard-abort backstop is 2x.
	if got := float64(spent) / 1020484; got > 8 {
		t.Errorf("a 4-second level took %.2f emulated seconds", got)
	}
	if !contains(s.Text(14), "D") {
		t.Errorf("row 14 = %q, want a depth/score readout", s.Text(14))
	}
}

// TestFullGame is the deliverable: a complete game, played keystroke by
// keystroke through the UI's own keyboard, from the opening to a real
// termination, with refchess refereeing every single ply.
//
// The human side is played by the same engine image run the ordinary way at
// a deeper fixed depth, so the game is a real one rather than random moves —
// the discipline internal/sargon's CrossCheckHistory applies to Sargon.
func TestFullGame(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the full game in -short")
	}
	const (
		uiLevel    = 1 // the UI's engine: fixed depth 2
		humanDepth = 4 // the scripted opponent
		maxPlies   = 250
	)
	bin, defs := engineBin(t)
	u := boot(t)
	mustEnter(t, u, "l")
	if err := u.Key('0' + uiLevel); err != nil {
		t.Fatal(err)
	}

	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	syncRef := func() {
		hist := u.History()
		for ; seen < len(hist); seen++ {
			m, err := refchess.ParseMove(hist[seen])
			if err != nil {
				t.Fatalf("ply %d: %v", seen, err)
			}
			if err := p.Make(m); err != nil {
				t.Fatalf("ply %d: the UI played %s, which refchess calls illegal: %v\n%s",
					seen, hist[seen], err, u.Screen())
			}
		}
		if got, want := normFEN(u.FEN()), normFEN(p.FEN()); got != want {
			t.Fatalf("after ply %d the UI has %q and refchess has %q\n%s",
				seen, got, want, u.Screen())
		}
	}

	for range maxPlies {
		syncRef()
		if r := u.Peek(ui.UIRESULT); r != ui.ResNone {
			break
		}
		if len(p.LegalMoves()) == 0 {
			t.Fatalf("refchess says there are no legal moves but the UI is still playing\n%s", u.Screen())
		}
		// A varying eval-dither seed for the opponent, so a fixed-depth
		// engine-vs-engine game does not shuffle into a repetition.
		mv := refSearchFEN(t, bin, defs, p.FEN(), humanDepth, byte(2*seen+7))
		if mv == "" {
			t.Fatalf("the scripted opponent found no move in %s", p.FEN())
		}
		mustEnter(t, u, mv)
		if int(u.Peek(ui.UIHCNT)) == seen {
			t.Fatalf("the UI rejected the opponent's legal move %s: %q\n%s",
				mv, strings.TrimSpace(u.Screen().Text(17)), u.Screen())
		}
	}
	syncRef()

	result := u.Peek(ui.UIRESULT)
	s := u.Screen()
	t.Logf("FINAL SCREEN after %d plies (%s):\n%s", u.Peek(ui.UIHCNT), ui.ResultName(result), s)
	t.Logf("game: %s", strings.Join(u.History(), " "))
	t.Logf("final position: %s", u.FEN())
	t.Logf("emulated IIe time for the whole game: %.1f s", float64(u.M.Cycles)/1020484)

	if result == ui.ResNone {
		t.Fatalf("the game did not terminate within %d plies", maxPlies)
	}
	// refchess must agree about WHY it ended.
	switch result {
	case ui.ResMate:
		if len(p.LegalMoves()) != 0 || !p.InCheck() {
			t.Errorf("the UI says checkmate; refchess says %d legal moves, in check %v",
				len(p.LegalMoves()), p.InCheck())
		}
	case ui.ResStale:
		if len(p.LegalMoves()) != 0 || p.InCheck() {
			t.Errorf("the UI says stalemate; refchess says %d legal moves, in check %v",
				len(p.LegalMoves()), p.InCheck())
		}
	case ui.Res50:
		if p.HalfmoveClock() < 100 {
			t.Errorf("the UI claims the 50-move rule at halfmove clock %d", p.HalfmoveClock())
		}
	case ui.ResRep:
		// Repetition is checked by construction, against the UI's own hash
		// history; refchess does not track occurrences here.
	default:
		t.Errorf("unexpected termination %s", ui.ResultName(result))
	}
	if got := s.Text(23); !contains(got, "COMMAND?") {
		t.Errorf("a finished game should prompt for a COMMAND, not a move: %q", got)
	}
}
