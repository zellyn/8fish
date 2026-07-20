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
	// SHIFT-<digit> on the Apple ][+ keyboard: 1..9 -> ! @ # $ % ^ & * (
	shifted := []byte("!@#$%^&*(")
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
	mid, prevReply, err := m.enterMove(move)
	if err != nil {
		return res, err
	}
	thinkStart := m.Cycles()
	replied := m.waitReply(prevReply, maxThinkSteps)
	res.ThinkCycles = m.Cycles() - thinkStart
	if !replied {
		res.Message, res.GameOver = m.scrapeMessage()
		if res.GameOver {
			return res, nil // mate/stalemate: our move ended the game
		}
		return res, fmt.Errorf("no reply from sargon within %d steps after move %q", maxThinkSteps, move)
	}
	return m.decodeReply(res, mid), nil
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
	mid, prevReply, err := m.enterMove(move)
	if err != nil {
		return res, err
	}
	_ = prevReply
	thinkStart := m.Cycles()

	// Commit detection uses Sargon's whole move-list column text, which changes
	// ONLY when Sargon commits a move — its internal search scribbles the
	// $60-$7F piece list but never the move-list text, so this avoids both
	// mid-search RAM garbage and repeated-token ambiguity.
	prevCol := m.sargonColumnText()

	// Spend the budget, watching for an early opening-book reply.
	book := false
	for m.Cycles()-thinkStart < budgetCycles {
		if err := m.Run(pollChunk); err != nil {
			return res, err
		}
		if m.sargonColumnText() != prevCol {
			book = true
			break
		}
	}
	if !book {
		m.ForceMove() // CTRL-T: play best move so far
		if !m.waitSargonCommit(prevCol, 12_000_000) {
			res.ThinkCycles = m.Cycles() - thinkStart
			res.Message, res.GameOver = m.scrapeMessage()
			if res.GameOver {
				return res, nil
			}
			return res, fmt.Errorf("no reply after CTRL-T for move %q", move)
		}
	}
	res.ThinkCycles = m.Cycles() - thinkStart
	return m.decodeReply(res, mid), nil
}

// sargonColumnText returns Sargon's entire move-list column (all rows,
// concatenated) — the left column when Sargon plays White, else the right. This
// text changes only when Sargon commits a move (its search is internal), so a
// change is a reliable commit signal, robust to repeated move tokens and to
// scrolling (a new row alters the multi-row string even if the token repeats).
func (m *Machine) sargonColumnText() string {
	lo, hi := 22, 30 // Sargon Black -> right column
	if m.SargonWhite {
		lo, hi = 10, 18
	}
	var sb strings.Builder
	for r := 10; r < 24; r++ {
		row := m.TextRow(r)
		if len(row) >= hi {
			sb.WriteString(row[lo:hi])
		}
		sb.WriteByte('|')
	}
	return sb.String()
}

// waitSargonCommit polls up to maxSteps until Sargon's move-list column changes
// from prev (a committed move), then settles briefly so the piece list is fully
// updated. Returns whether a commit was seen.
func (m *Machine) waitSargonCommit(prev string, maxSteps uint64) bool {
	var steps uint64
	for steps < maxSteps {
		if err := m.Run(pollChunk); err != nil {
			return false
		}
		steps += pollChunk
		if m.sargonColumnText() != prev {
			m.Run(1_000_000) // let the piece list finish updating
			return true
		}
	}
	return false
}

// sargonHalf returns the 16 piece-square bytes belonging to Sargon (white half
// when Sargon plays White, else the black half).
func (m *Machine) sargonHalf(pl PieceList) [16]Square {
	var h [16]Square
	lo := 16
	if m.SargonWhite {
		lo = 0
	}
	copy(h[:], pl[lo:lo+16])
	return h
}

// waitSargonMove polls up to maxSteps until Sargon's half of the piece list
// differs from mid and is stable for two consecutive polls — i.e. Sargon has
// committed a move and (Easy Mode) returned to idle. Immune to repeated
// move-list text. Returns whether a completed move was detected.
func (m *Machine) waitSargonMove(mid PieceList, maxSteps uint64) bool {
	base := m.sargonHalf(mid)
	last := base
	changed := false
	stable := 0
	var steps uint64
	for steps < maxSteps {
		if err := m.Run(pollChunk); err != nil {
			return false
		}
		steps += pollChunk
		cur := m.sargonHalf(m.ReadPieceList())
		if !changed && cur != base {
			changed = true
		}
		if changed {
			if cur == last {
				stable++
			} else {
				stable = 0
			}
			if stable >= 2 {
				return true
			}
		}
		last = cur
	}
	return changed
}

// enterMove settles, types the player move, and confirms it registered (the
// mover's white piece reaches the destination square), retrying a dropped or
// garbled entry. It returns the piece list just after our move (mid) and the
// SARGON move-list token before Sargon replies (prevReply).
//
// move is "E2-E4" or "E4XD5" (separator '-' or 'X'); a trailing promotion
// letter (e.g. "E7-E8N") is allowed and ignored for the square parse.
func (m *Machine) enterMove(move string) (mid PieceList, prevReply string, err error) {
	move = strings.ToUpper(strings.TrimSpace(move))
	from, to, ok := parseFromTo(move)
	if !ok {
		return mid, "", fmt.Errorf("cannot parse move %q", move)
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
	// loop; typing during that window drops characters.
	if err := m.Run(3_000_000); err != nil {
		return mid, "", err
	}

	before := m.ReadPieceList()
	// The keyboard opponent plays Black iff Sargon is White.
	oppBlack := m.SargonWhite
	fromIdx := pieceIndexAt(before, from, oppBlack)
	if fromIdx < 0 {
		return mid, "", fmt.Errorf("no %s piece on %s for move %q", colorName(oppBlack), from.Algebraic(), move)
	}

	const stepsPerKey = 200_000
	accepted := false
	for attempt := 0; attempt < 4 && !accepted; attempt++ {
		if attempt > 0 {
			// Clear a partial/garbled entry: Return submits & rejects it.
			m.Key(0x0D)
			if err := m.Run(1_500_000); err != nil {
				return mid, "", err
			}
		}
		if err := m.TypePaced(fromTo+"\r", stepsPerKey); err != nil {
			return mid, "", err
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
				return mid, "", err
			}
			if done {
				accepted = true
				break
			}
			// Prompt never appeared (dropped keys): fall through to retry.
			continue
		}
		var s uint64
		for s < 4_000_000 {
			if err := m.Run(pollChunk); err != nil {
				return mid, "", err
			}
			s += pollChunk
			// Accept when any of our pieces has reached the destination.
			// (Promotions are handled above via the prompt and never reach here.)
			if m.anyPieceAt(to, oppBlack) {
				accepted = true
				break
			}
		}
		_ = fromIdx
	}
	if !accepted {
		return mid, "", fmt.Errorf("move %q not accepted after retries (screen: %q)", move, m.messageLine())
	}
	return m.ReadPieceList(), m.scrapeReplyText(), nil
}

// completePromotion answers Sargon's "ENTER PROMOTED PIECE" prompt, which
// appears after a pawn's FROM-TO move to the last rank is entered. Sargon shows
// the prompt with a default of Queen (".../Q" in the move list) and blocks until
// a choice is confirmed:
//
//	Queen ('Q'):        just RETURN (accept the default)
//	Under-promote:      the piece letter N/R/B, then RETURN
//
// It waits for the prompt (which lags the RETURN by ~1M cycles) before sending
// the choice, and reports whether the prompt was seen and answered. A false
// return means the prompt never appeared (e.g. dropped keystrokes) and the
// caller should retry.
func (m *Machine) completePromotion(promo byte) (bool, error) {
	const stepsPerKey = 200_000
	sawPrompt := false
	var s uint64
	for s < 6_000_000 {
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
	// Queen is the default; only under-promotions need the piece letter typed.
	if promo != 'Q' {
		if err := m.TypePaced(string(promo), stepsPerKey); err != nil {
			return false, err
		}
	}
	m.Key(0x0D) // RETURN confirms the shown promotion piece
	return true, m.Run(1_500_000)
}

// waitReply polls up to maxThinkSteps CPU steps for a new SARGON move token to
// appear (different from prevReply), returning whether one did. The text
// move-list is authoritative; the $60-$7F piece list is search scratch while
// Sargon thinks and is only trusted once idle (see decodeReply).
func (m *Machine) waitReply(prevReply string, maxThinkSteps uint64) bool {
	var steps uint64
	for steps < maxThinkSteps {
		if err := m.Run(pollChunk); err != nil {
			return false
		}
		steps += pollChunk
		if tok := m.scrapeReplyText(); tok != "" && tok != prevReply {
			return true
		}
	}
	return false
}

// decodeReply, called once a reply token is on screen, settles to idle then
// fills the move text, RAM-decoded move, board, and any status message.
func (m *Machine) decodeReply(res MoveResult, mid PieceList) MoveResult {
	// Let Sargon fully return to the idle input loop before trusting the list.
	m.Run(2_000_000)
	res.SargonText = m.scrapeReplyText()
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
	prevCol := m.sargonColumnText() // Sargon's (White, left) column; empty at start
	m.Key(0x13)                     // CTRL-S: Sargon plays the side to move (White)
	thinkStart := m.Cycles()

	if budgetCycles == 0 {
		// Level mode: wait for Sargon's natural (timed-level) opening move.
		if !m.waitSargonCommit(prevCol, 80_000_000) {
			return res, fmt.Errorf("no opening move from sargon-as-white (level mode)")
		}
	} else {
		book := false
		for m.Cycles()-thinkStart < budgetCycles {
			if err := m.Run(pollChunk); err != nil {
				return res, err
			}
			if m.sargonColumnText() != prevCol {
				book = true
				break
			}
		}
		if !book {
			m.ForceMove() // CTRL-T
			if !m.waitSargonCommit(prevCol, 12_000_000) {
				res.ThinkCycles = m.Cycles() - thinkStart
				res.Message, res.GameOver = m.scrapeMessage()
				return res, fmt.Errorf("no opening move from sargon-as-white")
			}
		}
	}
	res.ThinkCycles = m.Cycles() - thinkStart
	return m.decodeReply(res, mid), nil
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

// blackChanged reports whether any black piece-list entry (16-31) differs.
func blackChanged(a, b PieceList) bool {
	for i := 16; i < 32; i++ {
		if a[i] != b[i] {
			return true
		}
	}
	return false
}

// scrapeReplyText returns Sargon's most-recent move token from the on-screen
// move list. The list is chronological with White's move in the LEFT column
// (cols 10-15) and Black's in the RIGHT column (cols 22-27), regardless of who
// Sargon is; the header label swaps but the columns do not. So Sargon's column
// is the left one when it plays White, else the right one.
//
// It returns the LAST (bottom-most) non-empty token in that column, which is
// always the newest move even after the list scrolls (Sargon shows ~13 moves,
// newest at the bottom). Verified across a full 78-ply game.
func (m *Machine) scrapeReplyText() string {
	return m.scrapeMoveColumn(!m.SargonWhite /* right column iff Sargon is Black */)
}

// scrapeMoveColumn returns the bottom-most non-empty move token in the left
// (rightCol=false) or right (rightCol=true) column. Windows are 8 chars wide so
// a promotion token like "G2-G1/Q" (7 chars) is not truncated (which would drop
// the promotion piece and emit an illegal non-promoting move).
func (m *Machine) scrapeMoveColumn(rightCol bool) string {
	lo, hi := 10, 18
	if rightCol {
		lo, hi = 22, 30
	}
	last := ""
	for r := 10; r < 24; r++ {
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
func (m *Machine) scrapeMessage() (msg string, gameOver bool) {
	terminal := []string{"CHECKMATE", "STALEMATE", "DRAW", "MATE", "RESIGN"}
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
