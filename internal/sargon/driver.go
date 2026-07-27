package sargon

import (
	"fmt"
	"strings"
)

// Timing / step budgets. The emulator runs ~1 CPU cycle per Step, i.e. ~1e6
// steps per emulated second on a 1 MHz Apple II.
const (
	// MaxBootSteps bounds the wait for the program to reach its move-entry
	// state. Sargon III finishes loading from the (slow, emulated) Disk II in
	// roughly 100M cycles.
	MaxBootSteps = 250_000_000

	// pollChunk is how many CPU steps to run between checks while waiting.
	pollChunk = 500_000

	// BootSettleSteps is run after the board first appears, to let Sargon
	// finish loading before the first move is entered (~30M cycles margin).
	BootSettleSteps = 35_000_000
)

// initialWhitePawns is the piece-list signature that appears once Sargon has
// set up the board and is waiting for the first move: white pawns on a2-h2.
var initialWhitePawns = [8]Square{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17}

// BootToPrompt runs the CPU until Sargon III has loaded and set up its board
// (the piece list shows the initial position), i.e. it is ready for the first
// move. Returns an error if that state isn't reached within MaxBootSteps.
func (m *Machine) BootToPrompt() error {
	ready := func(m *Machine) bool {
		for i, want := range initialWhitePawns {
			if Square(m.A2.RamRead(uint16(WhiteListAddr+i))) != want {
				return false
			}
		}
		// Also require the title to be on screen, to avoid matching a
		// transient identical-looking table during load.
		return m.ScreenContains("SARGON III")
	}
	ok, err := m.RunUntil(MaxBootSteps, ready)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("sargon did not reach move-entry state within %d steps", MaxBootSteps)
	}
	// The board is set up well before Sargon finishes loading the rest of the
	// program (opening book / overlays) from the emulated Disk II. Entering a
	// move during that window corrupts later input, so settle first.
	return m.Run(BootSettleSteps)
}

// Level sets Sargon's playing level (1-9) by injecting the corresponding
// shifted digit (SHIFT-n). Must be called when it is the player's turn.
//
// Level -> avg response time (per the manual): 1=5s, 2=15s, 3=30s, 4=1m,
// 5=2m, 6=3m, 7=6m, 8=10m, 9=infinite. Use level 3 for ~30s/move.
func (m *Machine) Level(n int) error {
	if n < 1 || n > 9 {
		return fmt.Errorf("level %d out of range 1-9", n)
	}
	// SHIFT-<digit> on the Apple ][+ keyboard: 1..9 -> ! " # $ % & ' ( )
	// (the ][+ shifted number row; NOT the modern US-PC row ! @ # $ % ^ & * (,
	// on which SHIFT-9 is '(' = level 8 — so Level(9)/InfiniteLevel would fall a
	// level short of true Infinite). SHIFT-9 = ')' (0x29) selects level 9.
	shifted := []byte("!\"#$%&'()")
	m.Key(shifted[n-1])
	// Give the key time to be consumed.
	return m.Run(pollChunk)
}

// EasyMode enables Sargon's Easy Mode (CTRL-E), which stops it from thinking on
// the player's time (pondering). This is strongly recommended when driving
// Sargon programmatically: while pondering, Sargon continuously overwrites its
// $60-$7F piece list as search scratch, so the board can only be read reliably
// from RAM (and our own moves confirmed) once pondering is off. It also makes
// per-move timing ponder-free and reproducible, matching a non-pondering
// opponent. Per the manual it roughly halves Sargon's effective strength at a
// given level, so choose the level accordingly. Call once, on the player's turn.
func (m *Machine) EasyMode() error {
	m.Key(0x05) // CTRL-E
	return m.Run(pollChunk)
}

// ForceMove sends CTRL-T (Terminate Search), forcing Sargon to play its best
// move so far immediately. Needed to make Sargon move on the Infinite level
// (SHIFT-9); not used for timed matches.
func (m *Machine) ForceMove() {
	m.Key(0x14) // CTRL-T
}

// CyclesPerSecond is Sargon's effective 6502 clock (Apple II ~1.0205 MHz).
const CyclesPerSecond = 1_020_500

// MoveResult describes the outcome of submitting a player move.
type MoveResult struct {
	// SargonMove is Sargon's reply, decoded from the piece-list diff.
	SargonMove Move
	// SargonText is Sargon's reply as shown on the text move-list (e.g.
	// "E7-E6", "E4XD5", "0-0"), best-effort.
	SargonText string
	// Message is any message-line text detected (CHECK, CHECKMATE, etc.).
	Message string
	// GameOver is set if a terminal message (mate/stalemate/draw) was seen.
	GameOver bool
	// Board is the RAM piece list read once Sargon returned to idle.
	Board PieceList
	// ThinkCycles is the cycle-accurate CPU cost Sargon spent producing this
	// reply: from just after our move was accepted until its reply appeared.
	// At 1.0205 MHz, seconds = ThinkCycles / 1_020_500. Book moves are ~free;
	// out-of-book middlegame moves reflect the level's real per-move budget.
	ThinkCycles uint64
}

// SubmitMove types a player move (e.g. "E2-E4", or "E4-D5" for a capture; add
// a trailing promotion piece letter like "E7-E8N" to under-promote) followed
// by Return, waits for Sargon to accept it and reply, and returns Sargon's
// reply decoded from RAM plus the on-screen text.
//
// maxThinkSteps bounds how long to wait for Sargon's reply (e.g. ~10M for
// level 1, more for higher levels). It errors if the move is rejected as
// illegal or no reply appears in time.
func (m *Machine) SubmitMove(move string, maxThinkSteps uint64) (MoveResult, error) {
	var res MoveResult
	// Sargon's newest move-list entry BEFORE our move is typed (see RequestMove
	// for why the baseline must be taken here, not after enterMove).
	baseNo, _, _ := m.LastSargonEntry()
	mid, err := m.enterMove(move)
	if err != nil {
		return res, err
	}
	thinkStart := m.Cycles()
	replied := m.waitReply(baseNo, maxThinkSteps)
	res.ThinkCycles = m.Cycles() - thinkStart
	if !replied {
		res.Message, res.GameOver = m.scrapeMessage()
		if res.GameOver {
			return res, nil // mate/stalemate: our move ended the game
		}
		return res, fmt.Errorf("no reply from sargon within %d steps after move %q", maxThinkSteps, move)
	}
	return m.decodeReply(res, mid, baseNo), nil
}

// RequestMove is the fair-match primitive: it enters the opponent's move, gives
// Sargon exactly budgetCycles of thinking on the Infinite level (SHIFT-9, set
// via InfiniteLevel), then forces the move with CTRL-T and returns Sargon's
// reply. This puts per-move compute entirely under our control — symmetric and
// reproducible with our own cycle-budgeted engine — instead of trusting
// Sargon's self-estimated, time-banked level timer.
//
// If Sargon answers from its opening book before the budget elapses (an instant
// "free" move), that reply is returned unchanged with ThinkCycles ~= 0.
func (m *Machine) RequestMove(move string, budgetCycles uint64) (MoveResult, error) {
	var res MoveResult

	// Sargon's newest move-list entry BEFORE we enter ours. Captured now (not after
	// enterMove) so a FORCED reply is still seen as new: when our move leaves
	// Sargon a single legal response — common after a stalemate-avoiding
	// under-promotion, e.g. f7-f8=R leaving the enemy king one square — Sargon
	// plays it INSTANTLY the moment our move registers, on the Infinite level and
	// WITHOUT waiting for CTRL-T. That happens while enterMove is still running, so
	// a baseline taken after enterMove already contains the reply and the
	// change-based detection below would wait forever, wrongly reporting "no reply"
	// (the promotion-draw bug that contaminated ~10% of the 300-game run). Our move
	// never advances SARGON's move number, so this baseline is robust.
	baseNo, _, _ := m.LastSargonEntry()

	mid, err := m.enterMove(move)
	if err != nil {
		return res, err
	}
	thinkStart := m.Cycles()

	// Forced/instant reply already on the list: return it without waiting.
	if committed, err := m.sargonCommitted(baseNo); err != nil {
		return res, err
	} else if committed {
		res.ThinkCycles = m.Cycles() - thinkStart
		return m.decodeReply(res, mid, baseNo), nil
	}

	// Spend the budget, watching for an early opening-book reply.
	book := false
	for m.Cycles()-thinkStart < budgetCycles {
		if err := m.Run(pollChunk); err != nil {
			return res, err
		}
		committed, err := m.sargonCommitted(baseNo)
		if err != nil {
			return res, err
		}
		if committed {
			book = true
			break
		}
	}
	if !book {
		committed := false
		if m.HardMode {
			// Hard Mode: Sargon searches the Infinite level while pondering, so a
			// single CTRL-T is often missed — re-send until it commits.
			committed = m.forceCommitHard(baseNo, 80_000_000)
		} else {
			m.ForceMove() // CTRL-T: play best move so far
			committed = m.waitSargonCommit(baseNo, 12_000_000)
		}
		if !committed {
			res.ThinkCycles = m.Cycles() - thinkStart
			res.Message, res.GameOver = m.scrapeMessage()
			if res.GameOver {
				return res, nil
			}
			return res, fmt.Errorf("no reply after CTRL-T for move %q", move)
		}
	}
	res.ThinkCycles = m.Cycles() - thinkStart
	return m.decodeReply(res, mid, baseNo), nil
}

// sargonCommitted reports whether Sargon's move list has advanced past move
// number baseNo, i.e. Sargon has committed a new move.
//
// This is THE commit signal. It replaces the older "Sargon's move-list column
// TEXT changed" test, which was wrong: mid-search Sargon transiently BLANKS the
// bottom rows of the list and repaints them ~167K cycles later (measured), and
// a poll landing inside that window saw a "change", broke out of the budget
// loop without ever sending CTRL-T, and then read the bottom-most SURVIVING
// token — Sargon's PREVIOUS move. That stale reply is illegal on our board and
// killed 15 of 300 games in the 2026-07 standard-start match (the other 2 were
// the 127-move list wrap below). The repaint fires once per game, on the first
// move Sargon has to search after leaving its opening book, which is why 14 of
// the 15 were Sargon-as-White games with a long book line. The displayed move
// NUMBER only ever advances on a real commit, so it is immune to the repaint and
// to the list scrolling.
//
// It returns ErrListWrapped if the number goes BACKWARDS *near Sargon's 127-move
// move-list capacity*, where a backwards number means the list has restarted its
// numbering and Sargon's screen record is fiction. Below that, a backwards number
// is just the repaint blank frame in progress (the newest rows are momentarily
// missing) and is reported as "not committed yet" — the caller keeps polling.
func (m *Machine) sargonCommitted(baseNo int) (bool, error) {
	no, _, ok := m.LastSargonEntry()
	if !ok {
		return false, nil
	}
	if no < baseNo && baseNo >= MaxSargonMoves-1 {
		return false, ErrListWrapped
	}
	return no > baseNo, nil
}

// confirmMoveScreen polls up to maxSteps for Hard-mode move acceptance: OUR
// move-list entry advances past baseOwnNo (our move landed in the list) with no
// ILLEGAL/INVALID message on the message row. Returns false (caller retries) if
// an ILLEGAL/INVALID message appears or the window elapses with no new entry.
//
// It keys on the displayed move NUMBER, not the column text, for the same reason
// as sargonCommitted: Sargon transiently blanks and repaints the list rows, and
// a text-change test false-positives on the blank frame — here that would report
// a move accepted that Sargon never saw.
func (m *Machine) confirmMoveScreen(baseOwnNo int, maxSteps uint64) bool {
	var s uint64
	for s < maxSteps {
		if err := m.Run(pollChunk); err != nil {
			return false
		}
		s += pollChunk
		if msg := m.messageLine(); strings.Contains(msg, "ILLEGAL") || strings.Contains(msg, "INVALID") {
			return false
		}
		if no, _, ok := m.LastOwnEntry(); ok && no > baseOwnNo {
			return true
		}
	}
	return false
}

// forceCommitHard drives a commit in Hard Mode, where a single CTRL-T is
// unreliable: while Sargon searches the Infinite level with pondering enabled it
// polls (and clears) the keyboard latch only occasionally, so one strobe is
// often missed. It therefore RE-SENDS CTRL-T, spaced ~2M steps apart (long
// enough for Sargon to read and clear the previous strobe), checking after each
// send for a move-list column change (a committed move). Returns whether a
// commit was seen within maxSteps. The Easy-mode single-CTRL-T path is unchanged.
func (m *Machine) forceCommitHard(baseNo int, maxSteps uint64) bool {
	const strobeGap = 2_000_000 // steps between CTRL-T re-sends
	var steps uint64
	for steps < maxSteps {
		m.ForceMove() // CTRL-T
		var w uint64
		for w < strobeGap && steps < maxSteps {
			if err := m.Run(pollChunk); err != nil {
				return false
			}
			steps += pollChunk
			w += pollChunk
			if committed, err := m.sargonCommitted(baseNo); err != nil {
				return false
			} else if committed {
				m.Run(1_000_000) // let the piece list / display finish updating
				return true
			}
		}
	}
	return false
}

// waitSargonCommit polls up to maxSteps until Sargon's move list advances past
// baseNo (a committed move), then settles briefly so the piece list is fully
// updated. Returns whether a commit was seen.
func (m *Machine) waitSargonCommit(baseNo int, maxSteps uint64) bool {
	var steps uint64
	for steps < maxSteps {
		if err := m.Run(pollChunk); err != nil {
			return false
		}
		steps += pollChunk
		if committed, err := m.sargonCommitted(baseNo); err != nil {
			return false
		} else if committed {
			m.Run(1_000_000) // let the piece list finish updating
			return true
		}
	}
	return false
}

// enterMove settles, types the player move, and confirms it registered (the
// mover's white piece reaches the destination square), retrying a dropped or
// garbled entry. It returns the piece list just after our move (mid).
//
// move is "E2-E4" or "E4XD5" (separator '-' or 'X'); a trailing promotion
// letter (e.g. "E7-E8N") is allowed and ignored for the square parse.
func (m *Machine) enterMove(move string) (mid PieceList, err error) {
	move = strings.ToUpper(strings.TrimSpace(move))
	from, to, ok := parseFromTo(move)
	if !ok {
		return mid, fmt.Errorf("cannot parse move %q", move)
	}
	// A promotion carries a trailing piece letter after the 5-char FROM-TO
	// (e.g. "C2-C1Q" or "C2-C1N"). Sargon does not promote on the FROM-TO
	// RETURN; it prompts "ENTER PROMOTED PIECE" and waits for the choice. We
	// type only the FROM-TO field, then answer the prompt (see completePromotion)
	// — typing the letter inside the field is rejected as INVALID MOVE.
	fromTo := move
	promo := byte(0)
	if len(move) >= 6 {
		switch move[5] {
		case 'Q', 'N', 'R', 'B':
			promo = move[5]
			fromTo = move[:5]
		}
	}

	// Settle: after moving, Sargon redraws before returning to its keyboard
	// loop; typing during that window drops characters. In Hard Mode the caller
	// has just run Sargon for a ponder window (the display is already stable and
	// the keyboard loop is live), and the RAM piece list is being churned by the
	// ponder search, so we skip both the settle and the RAM-based source-square
	// guard — the move is confirmed from the ponder-immune screen column below.
	if !m.HardMode {
		if err := m.Run(3_000_000); err != nil {
			return mid, err
		}
	}

	before := m.ReadPieceList()
	// The keyboard opponent plays Black iff Sargon is White.
	oppBlack := m.SargonWhite
	fromIdx := pieceIndexAt(before, from, oppBlack)
	if fromIdx < 0 && !m.HardMode {
		return mid, fmt.Errorf("no %s piece on %s for move %q", colorName(oppBlack), from.Algebraic(), move)
	}

	// Hard-mode confirmation baseline: our newest move-list entry BEFORE typing.
	// Our move number advances only when our move is accepted into the list; a
	// rejected (ILLEGAL/INVALID) move never appears, so an advance is a reliable,
	// ponder-immune AND repaint-immune accept signal.
	baseOwnNo, _, _ := m.LastOwnEntry()

	const stepsPerKey = 200_000
	accepted := false
	for attempt := 0; attempt < 4 && !accepted; attempt++ {
		if attempt > 0 {
			// Clear a partial/garbled entry: Return submits & rejects it.
			m.Key(0x0D)
			if err := m.Run(1_500_000); err != nil {
				return mid, err
			}
		}
		if err := m.TypePaced(fromTo+"\r", stepsPerKey); err != nil {
			return mid, err
		}
		// Promotion: answer the "ENTER PROMOTED PIECE" prompt so the pawn
		// actually reaches the last rank (without this the pawn stays put and
		// Sargon never replies — it is blocked on the prompt). Seeing the prompt
		// is itself proof the move was accepted (Sargon only prompts for a legal
		// pawn-to-last-rank move); use that as the accept signal, because right
		// after the promotion it is Sargon's turn and its search scribbles the
		// $60-$7F piece list, so anyPieceAt(to) would read garbage.
		if promo != 0 {
			done, err := m.completePromotion(promo)
			if err != nil {
				return mid, err
			}
			if done {
				accepted = true
				break
			}
			// Prompt never appeared (dropped keys): fall through to retry.
			continue
		}
		if m.HardMode {
			// Screen confirmation: our move appears in our move-list column, and
			// no ILLEGAL/INVALID message shows. Ponder-immune (the RAM piece list
			// is search scratch while Sargon ponders).
			accepted = m.confirmMoveScreen(baseOwnNo, 5_000_000)
		} else {
			var s uint64
			for s < 4_000_000 {
				if err := m.Run(pollChunk); err != nil {
					return mid, err
				}
				s += pollChunk
				// Accept when any of our pieces has reached the destination.
				// (Promotions are handled above via the prompt and never reach here.)
				if m.anyPieceAt(to, oppBlack) {
					accepted = true
					break
				}
			}
		}
		_ = fromIdx
	}
	if !accepted {
		return mid, fmt.Errorf("move %q not accepted after retries (screen: %q)", move, m.messageLine())
	}
	return m.ReadPieceList(), nil
}

// completePromotion answers Sargon's "ENTER PROMOTED PIECE" prompt, which
// appears after a pawn's FROM-TO move to the last rank is entered. Sargon shows
// the prompt with a default of Queen (".../Q" in the move list) and blocks until
// a single keystroke confirms a choice:
//
//	Queen:         RETURN (accept the shown default)
//	Under-promote: the piece letter N/R/B (selects AND confirms directly —
//	               verified on-screen: pressing the letter clears the prompt)
//
// It waits for the prompt (which lags the FROM-TO RETURN by ~1M cycles) before
// sending the choice, and reports whether the prompt was seen and answered. A
// false return means the prompt never appeared (e.g. dropped FROM-TO keys) and
// the caller should retry entering the move.
//
// In HARD mode Sargon ponders (Infinite level) while the prompt is up and polls
// (and clears) the keyboard latch only occasionally, so a SINGLE confirming
// keystroke is easily missed — and a missed confirm parks Sargon at the prompt
// forever (never searching its reply), which is the "no reply after CTRL-T"
// promotion hang that drew ~10% of the 300-game symmetric run. The Hard path
// therefore waits longer for the prompt (a premature give-up would make the
// caller re-type FROM-TO and corrupt the prompt state) and RE-STROBES the
// confirm key until the prompt clears — mirroring forceCommitHard's repeated
// CTRL-T. The Easy path (Sargon idle at the prompt) is a single keystroke, as
// before.
func (m *Machine) completePromotion(promo byte) (bool, error) {
	const stepsPerKey = 200_000
	// Prompt-appearance window. Easy mode reads the FROM-TO RETURN promptly;
	// Hard mode (deep ponder, rare keyboard poll) can lag, so wait much longer
	// before concluding the FROM-TO keys were dropped.
	promptWait := uint64(6_000_000)
	if m.HardMode {
		promptWait = 30_000_000
	}
	sawPrompt := false
	var s uint64
	for s < promptWait {
		if err := m.Run(pollChunk); err != nil {
			return false, err
		}
		s += pollChunk
		if m.ScreenContains("PROMOTED") {
			sawPrompt = true
			break
		}
	}
	if !sawPrompt {
		return false, nil
	}
	if !m.HardMode {
		// Easy mode: Sargon is idle at the prompt and reads the key immediately.
		// (Unchanged: type the under-promotion piece letter, then RETURN.)
		if promo != 'Q' {
			if err := m.TypePaced(string(promo), stepsPerKey); err != nil {
				return false, err
			}
		}
		m.Key(0x0D) // RETURN confirms the shown promotion piece
		return true, m.Run(1_500_000)
	}
	// Hard mode: re-strobe a single confirming keystroke until the prompt clears.
	// RETURN accepts the default Queen; an under-promotion piece letter (N/R/B)
	// selects AND confirms in one key (verified on-screen).
	confirm := byte(0x0D)
	if promo != 'Q' {
		confirm = promo
	}
	return m.confirmPromotionHard(confirm), nil
}

// confirmPromotionHard re-sends the promotion-confirm keystroke until Sargon's
// "ENTER PROMOTED PIECE" prompt clears (the promotion committed). A single
// strobe is unreliable while Sargon ponders — the keyboard latch is polled and
// cleared only occasionally, so one keystroke is often missed (the same reason a
// single CTRL-T is missed; see forceCommitHard). It re-sends every strobeGap
// steps, checking after each poll for the prompt to vanish, and NEVER sends
// another keystroke once the prompt has cleared, so no stray key leaks into
// Sargon's ensuing search. Returns whether the prompt cleared within maxSteps.
func (m *Machine) confirmPromotionHard(confirm byte) bool {
	const (
		strobeGap = 2_000_000 // steps between re-sends (Sargon reads+clears)
		maxSteps  = 80_000_000
	)
	var steps uint64
	for steps < maxSteps {
		if !m.ScreenContains("PROMOTED") {
			m.Run(1_000_000) // let the board / move list finish updating
			return true
		}
		m.Key(confirm)
		var w uint64
		for w < strobeGap && steps < maxSteps {
			if err := m.Run(pollChunk); err != nil {
				return false
			}
			w += pollChunk
			steps += pollChunk
			if !m.ScreenContains("PROMOTED") {
				m.Run(1_000_000)
				return true
			}
		}
	}
	return false
}

// waitReply polls up to maxThinkSteps CPU steps for Sargon's move list to
// advance past baseNo (a new committed move), returning whether it did. The text
// move-list is authoritative; the $60-$7F piece list is search scratch while
// Sargon thinks and is only trusted once idle (see decodeReply). Keying on the
// move NUMBER rather than the token also avoids the mid-search repaint
// false-positive described in sargonCommitted.
func (m *Machine) waitReply(baseNo int, maxThinkSteps uint64) bool {
	var steps uint64
	for steps < maxThinkSteps {
		if err := m.Run(pollChunk); err != nil {
			return false
		}
		steps += pollChunk
		if committed, err := m.sargonCommitted(baseNo); err != nil {
			return false
		} else if committed {
			return true
		}
	}
	return false
}

// decodeReply, called once a reply has been committed, settles to idle then
// fills the move text, RAM-decoded move, board, and any status message. It
// re-reads the list until an entry NEWER than baseNo is visible, so a settle
// that lands inside Sargon's transient move-list repaint cannot hand back the
// previous move's token.
func (m *Machine) decodeReply(res MoveResult, mid PieceList, baseNo int) MoveResult {
	// Let Sargon fully return to the idle input loop before trusting the list.
	m.Run(2_000_000)
	res.SargonText = ""
	for tries := 0; tries < 8; tries++ {
		if no, tok, ok := m.LastSargonEntry(); ok && no > baseNo {
			res.SargonText = tok
			break
		}
		m.Run(500_000)
	}
	after := m.ReadPieceList()
	// Diff Sargon's own half: black (16-31) when Sargon is Black, else white.
	if mv, ok := DiffMove(mid, after, !m.SargonWhite); ok {
		res.SargonMove = mv
	}
	res.Board = after
	res.Message, res.GameOver = m.scrapeMessage()
	return res
}

// anyPieceAt reports whether any piece in the given color half currently sits
// on square sq (read live from the piece list in RAM).
func (m *Machine) anyPieceAt(sq Square, black bool) bool {
	lo, hi := 0, 16
	if black {
		lo, hi = 16, 32
	}
	for i := lo; i < hi; i++ {
		if Square(m.A2.RamRead(uint16(PieceListAddr+i))) == sq {
			return true
		}
	}
	return false
}

// pieceIndexAt returns the global piece-list index (0-15 white, 16-31 black)
// occupying square sq within the requested color half, or -1.
func pieceIndexAt(pl PieceList, sq Square, black bool) int {
	lo, hi := 0, 16
	if black {
		lo, hi = 16, 32
	}
	for i := lo; i < hi; i++ {
		if pl[i] == sq {
			return i
		}
	}
	return -1
}

func colorName(black bool) string {
	if black {
		return "black"
	}
	return "white"
}

// InfiniteLevel puts Sargon on the Infinite analysis level (SHIFT-9). Combined
// with RequestMove (CTRL-T after a chosen cycle budget) it gives fully
// controlled, reproducible per-move compute. Call on the player's turn.
func (m *Machine) InfiniteLevel() error {
	return m.Level(9)
}

// StartAsWhite makes Sargon take White (CTRL-S) and play the opening move,
// giving it budgetCycles of thinking then forcing with CTRL-T (as RequestMove).
// Call once right after boot and InfiniteLevel, before any opponent move; the
// keyboard opponent is then Black. Returns Sargon's first move. An opening-book
// first move (the common case) is returned instantly without CTRL-T.
func (m *Machine) StartAsWhite(budgetCycles uint64) (MoveResult, error) {
	var res MoveResult
	mid := m.ReadPieceList() // initial position
	m.SargonWhite = true
	baseNo, _, _ := m.LastSargonEntry() // 0: Sargon's column is empty at the start
	m.Key(0x13)                         // CTRL-S: Sargon plays the side to move (White)
	thinkStart := m.Cycles()

	if budgetCycles == 0 {
		// Level mode: wait for Sargon's natural (timed-level) opening move.
		if !m.waitSargonCommit(baseNo, 80_000_000) {
			return res, fmt.Errorf("no opening move from sargon-as-white (level mode)")
		}
	} else {
		book := false
		for m.Cycles()-thinkStart < budgetCycles {
			if err := m.Run(pollChunk); err != nil {
				return res, err
			}
			committed, err := m.sargonCommitted(baseNo)
			if err != nil {
				return res, err
			}
			if committed {
				book = true
				break
			}
		}
		if !book {
			committed := false
			if m.HardMode {
				committed = m.forceCommitHard(baseNo, 80_000_000)
			} else {
				m.ForceMove() // CTRL-T
				committed = m.waitSargonCommit(baseNo, 12_000_000)
			}
			if !committed {
				res.ThinkCycles = m.Cycles() - thinkStart
				res.Message, res.GameOver = m.scrapeMessage()
				return res, fmt.Errorf("no opening move from sargon-as-white")
			}
		}
	}
	res.ThinkCycles = m.Cycles() - thinkStart
	return m.decodeReply(res, mid, baseNo), nil
}

// parseFromTo extracts the from/to squares from a move like "E2-E4", "E4XD5",
// or "E7-E8Q". Separator may be '-' or 'X'.
func parseFromTo(move string) (from, to Square, ok bool) {
	if len(move) < 5 {
		return 0, 0, false
	}
	from, ok1 := ParseSquare(move[0:2])
	if move[2] != '-' && move[2] != 'X' {
		return 0, 0, false
	}
	to, ok2 := ParseSquare(move[3:5])
	return from, to, ok1 && ok2
}

// messageLine returns the trimmed contents of the message-line row (row 5).
func (m *Machine) messageLine() string {
	return strings.TrimSpace(m.TextRow(5))
}

// scrapeMoveColumn returns the bottom-most non-empty move token in the left
// (rightCol=false) or right (rightCol=true) column. Windows are 8 chars wide so
// a promotion token like "G2-G1/Q" (7 chars) is not truncated (which would drop
// the promotion piece and emit an illegal non-promoting move).
func (m *Machine) scrapeMoveColumn(rightCol bool) string {
	lo, hi := whiteColLo, whiteColHi
	if rightCol {
		lo, hi = blackColLo, blackColHi
	}
	last := ""
	for r := listTopRow; r < listBotRow; r++ {
		row := m.TextRow(r)
		if len(row) < hi {
			continue
		}
		if tok := strings.TrimSpace(row[lo:hi]); tok != "" {
			last = tok
		}
	}
	return last
}

// scrapeMessage scans the screen for status keywords and reports the first one
// found plus whether it is terminal.
// The manual (Feature 12, Draw Offer) lists the draws Sargon announces by
// itself: STALEMATE, REPETITION OF POSITION and the 50-MOVE RULE. The latter two
// contain neither "DRAW" nor "MATE", so they must be matched by name or a
// Sargon-declared draw reads as "no message".
func (m *Machine) scrapeMessage() (msg string, gameOver bool) {
	terminal := []string{"CHECKMATE", "STALEMATE", "REPETITION", "MOVE RULE", "DRAW", "MATE", "RESIGN"}
	for _, k := range terminal {
		if m.ScreenContains(k) {
			return k, true
		}
	}
	if m.ScreenContains("CHECK") {
		return "CHECK", false
	}
	return "", false
}
