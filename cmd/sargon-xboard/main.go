// Command sargon-xboard exposes headless Sargon III as an xboard (CECP) engine
// so cutechess-cli can pit it against another engine (e.g. our own).
//
// It plays the side that moves *second*: the opponent's moves arrive as
// "usermove" and are typed into Sargon as the keyboard player's move; Sargon's
// reply is emitted as the engine's move. Configure cutechess with this engine
// as Black (Sargon's default: the keyboard player is White).
//
// Usage (as a cutechess engine):
//
//	cutechess-cli \
//	  -engine name=us cmd="go run ./cmd/uci" proto=uci \
//	  -engine name=SargonIII cmd="go run ./cmd/sargon-xboard -level 3" proto=xboard \
//	  -each tc=inf -games 1 -pgnout smoke.pgn
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/zellyn/8fish/internal/refchess"
	"github.com/zellyn/8fish/internal/sargon"
)

func main() {
	dsk := flag.String("dsk", "assets/sargon-iii.dsk", "path to Sargon III .dsk image")
	level := flag.Int("level", 1, "Sargon level 1-9 (used only when -budget-cycles=0)")
	easy := flag.Bool("easy", true, "enable Easy Mode (ponder-free; reliable) in level mode")
	budgetCycles := flag.Uint64("budget-cycles", 0, "fair mode: Infinite level + CTRL-T after this many 6502 cycles/move (0 = use -level)")
	budgetMult := flag.Float64("budget-multiplier", 1.0, "scale -budget-cycles (approximate Sargon's pondering edge, disabled by Easy Mode; e.g. 2.0 gives Sargon 2x our per-move compute)")
	wait := flag.Uint64("wait", 40_000_000, "level mode: max CPU steps to wait for a Sargon reply")
	debug := flag.Bool("debug", false, "log protocol traffic to stderr")
	flag.Parse()

	effBudget := *budgetCycles
	if effBudget > 0 && *budgetMult != 1.0 {
		effBudget = uint64(float64(effBudget) * *budgetMult)
		log.Printf("Sargon budget %d x %.2f = %d cycles/move", *budgetCycles, *budgetMult, effBudget)
	}

	e := &engine{
		dsk: *dsk, level: *level, easy: *easy, budgetCycles: effBudget,
		wait: *wait, debug: *debug,
	}
	e.run()
}

type engine struct {
	dsk          string
	level        int
	easy         bool
	budgetCycles uint64
	wait         uint64
	debug        bool

	m       *sargon.Machine
	out     *bufio.Writer
	mu      sync.Mutex // guards out (main loop + think goroutine both write)
	booted  chan struct{}
	bootErr error

	// force/go handshake state (guarded by smu). In force mode Sargon's reply
	// is computed but held until "go".
	smu          sync.Mutex
	force        bool
	pendingReply string
	haveReply    bool
	movesSeen    int  // opponent usermoves since "new"
	startedWhite bool // Sargon-as-White opening move dispatched

	// setboard opening: the FEN cutechess sends to start a varied-opening game.
	// Applied once, after boot, before the first move (guarded by smu).
	pendingFEN string
	setupOnce  *sync.Once
	setupErr   error

	// Referee history for verified draw-by-repetition handling. Every move
	// (opponent usermoves and Sargon's own replies) is replayed through refchess
	// so that, when Sargon declares a repetition draw with its move still
	// pending, the adapter can play the actual drawing move and let cutechess
	// end the game as a draw. Guarded by tmu. trackerInit gates one-time
	// per-game initialization (from the setboard FEN or the standard start).
	tmu         sync.Mutex
	tracker     *repTracker
	trackerInit bool
}

func (e *engine) run() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	e.out = bufio.NewWriter(os.Stdout)
	defer e.out.Flush()

	// Boot Sargon in the background (~2-3s) so the protocol loop stays
	// responsive to ping and the handshake; usermoves wait for it.
	e.startBoot()

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if e.debug {
			fmt.Fprintf(os.Stderr, "< %s\n", line)
		}
		if e.handle(line) {
			return
		}
	}
}

func (e *engine) send(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.debug {
		fmt.Fprintf(os.Stderr, "> %s\n", s)
	}
	fmt.Fprintln(e.out, s)
	e.out.Flush()
}

// handle processes one command; returns true to quit.
func (e *engine) handle(line string) bool {
	cmd := line
	arg := ""
	if i := strings.IndexByte(line, ' '); i >= 0 {
		cmd, arg = line[:i], strings.TrimSpace(line[i+1:])
	}

	switch cmd {
	case "xboard":
		// no-op
	case "protover":
		e.send(`feature myname="SargonIII" usermove=1 setboard=1 ping=1 sigint=0 sigterm=0 colors=0 done=1`)
	case "new":
		// Boot already started at process launch; only reboot if a game was
		// already played on this machine.
		if e.m != nil {
			e.startBoot()
		}
		e.smu.Lock()
		e.force, e.pendingReply, e.haveReply = false, "", false
		e.movesSeen, e.startedWhite = 0, false
		e.pendingFEN, e.setupOnce, e.setupErr = "", &sync.Once{}, nil
		e.smu.Unlock()
		e.tmu.Lock()
		e.tracker, e.trackerInit = nil, false
		e.tmu.Unlock()
	case "setboard":
		// Varied-opening start position. Stored now; applied by ensureReady
		// (after boot, before the first move) via SetupPosition. cutechess sends
		// this for each game when run with -openings ... format=epd.
		e.smu.Lock()
		e.pendingFEN = arg
		e.smu.Unlock()
	case "force":
		e.smu.Lock()
		e.force = true
		e.smu.Unlock()
	case "ping":
		e.send("pong %s", arg)
	case "usermove":
		e.smu.Lock()
		e.movesSeen++
		e.smu.Unlock()
		e.doUserMove(arg)
	case "go":
		// "go" with no prior usermove means the engine is White: make Sargon
		// take White (CTRL-S) and play the opening move. Otherwise leave force
		// mode and emit Sargon's reply to the last usermove (now, if ready, or
		// the in-flight think goroutine emits it when it sees force cleared).
		e.smu.Lock()
		e.force = false
		firstWhite := e.movesSeen == 0 && !e.startedWhite
		if firstWhite {
			e.startedWhite = true
		}
		r := ""
		if !firstWhite && e.haveReply {
			r, e.pendingReply, e.haveReply = e.pendingReply, "", false
		}
		e.smu.Unlock()
		if firstWhite {
			go e.thinkFirstWhite()
		} else if r != "" {
			e.send("move %s", r)
		}
	case "result", "?", "draw", "computer", "hard", "easy", "post", "nopost",
		"random", "level", "st", "sd", "time", "otim", "accepted", "rejected",
		"name", "rating", "ics", "cores", "memory":
		// ignored / not applicable
	case "quit":
		return true
	default:
		// ignore unknown commands per CECP
	}
	return false
}

// startBoot boots a fresh machine in a goroutine and publishes it on e.booted.
func (e *engine) startBoot() {
	e.booted = make(chan struct{})
	e.m = nil
	e.bootErr = nil
	go func(done chan struct{}) {
		m, err := sargon.NewMachine(e.dsk)
		if err == nil {
			err = m.BootToPrompt()
		}
		if err == nil && e.budgetCycles > 0 {
			// Fair mode: Infinite level + CTRL-T at our cycle budget. Also
			// enable Easy Mode so Sargon doesn't ponder after moving — keeps
			// the piece list stable (reliable reads) and play reproducible.
			if err = m.InfiniteLevel(); err == nil {
				err = m.EasyMode()
			}
		} else {
			if err == nil && e.level != 1 {
				err = m.Level(e.level)
			}
			if err == nil && e.easy {
				err = m.EasyMode()
			}
		}
		if err != nil {
			e.bootErr = err
		} else {
			e.m = m
		}
		close(done)
	}(e.booted)
}

// ensureBooted blocks until the background boot finishes.
func (e *engine) ensureBooted() error {
	<-e.booted
	return e.bootErr
}

// ensureReady blocks until boot completes and, if a setboard opening is pending,
// applies it exactly once via SetupPosition (which sets the board and
// side-to-move and validates the result). Called at the top of every think
// goroutine so the opening is in place before Sargon's first move. Level/Easy
// Mode set at boot are preserved across Analysis Mode.
func (e *engine) ensureReady() error {
	if err := e.ensureBooted(); err != nil {
		return err
	}
	e.smu.Lock()
	fen, once := e.pendingFEN, e.setupOnce
	e.smu.Unlock()
	// Initialize the referee history once per game — from the setboard FEN if
	// present, else the standard start. Done here (not in "new") because the
	// setboard FEN only arrives after "new" and before the first move, and
	// ensureReady runs at the top of every think goroutine.
	e.initTrackerOnce(fen)
	if fen == "" || once == nil {
		return nil
	}
	once.Do(func() {
		if err := e.m.SetupPosition(fen); err != nil {
			e.setupErr = fmt.Errorf("setboard %q: %w", fen, err)
			log.Printf("SETBOARD FAILED (resigning): %v", e.setupErr)
			return
		}
		log.Printf("SETBOARD ok: %s", fen)
	})
	return e.setupErr
}

// initTrackerOnce builds the referee history exactly once per game, from the
// setboard FEN (if any) or the standard start. Safe to call from every think
// goroutine; only the first call does work.
func (e *engine) initTrackerOnce(fen string) {
	e.tmu.Lock()
	defer e.tmu.Unlock()
	if e.trackerInit {
		return
	}
	e.trackerInit = true
	start := fen
	if start == "" {
		start = refchess.StartFEN
	}
	e.tracker = newRepTracker(start)
	if e.tracker == nil || !e.tracker.valid {
		log.Printf("TRACKER-INVALID init from FEN %q (draw-move verification disabled)", start)
	}
}

// trackApply records a move (opponent's or Sargon's, UCI coordinate notation)
// in the referee history. Any parse/apply failure disables verification (the
// tracker marks itself invalid) so the adapter falls back to today's behavior;
// it is logged but never fatal.
func (e *engine) trackApply(coord string) {
	e.tmu.Lock()
	defer e.tmu.Unlock()
	if err := e.tracker.apply(coord); err != nil {
		log.Printf("TRACKER-INVALID %v (draw-move verification disabled)", err)
	}
}

// uniqueEnPassant returns the tracked position's only legal en-passant capture
// in UCI notation, or "" if the tracker is invalid or the capture is ambiguous.
func (e *engine) uniqueEnPassant() string {
	e.tmu.Lock()
	defer e.tmu.Unlock()
	return e.tracker.uniqueEnPassant()
}

// findDrawingMove asks the referee history for a legal move (for the side to
// move) that forces a draw cutechess will recognize. Returns "" if none.
func (e *engine) findDrawingMove() (coord string, count int, reason string) {
	e.tmu.Lock()
	defer e.tmu.Unlock()
	return e.tracker.findDrawingMove()
}

// doUserMove processes an opponent move. The actual thinking runs in a
// goroutine so the I/O loop keeps answering ping (liveness) while Sargon boots
// and searches; otherwise cutechess declares a stalled connection.
func (e *engine) doUserMove(coord string) {
	go e.think(coord)
}

// thinkFirstWhite handles the engine-plays-White case: Sargon takes White via
// CTRL-S and plays the opening move, which is emitted as our move.
func (e *engine) thinkFirstWhite() {
	defer e.recoverResign("thinkFirstWhite")
	if err := e.ensureReady(); err != nil {
		log.Printf("boot failed, resigning: %v", err)
		e.resign()
		return
	}
	res, err := e.m.StartAsWhite(e.budgetCycles)
	if err != nil {
		log.Printf("sargon-as-white failed, resigning: %v", err)
		e.resign()
		return
	}
	reply := e.replyCoord(res)
	if reply == "" {
		log.Printf("sargon-as-white: no decodable opening move (msg=%q)", res.Message)
		e.claimGameOver(res)
		return
	}
	e.trackApply(reply)
	e.send("move %s", reply)
}

// claimGameOver ends the game when Sargon has no move to give. If Sargon
// reports a draw (3-fold/50-move/stalemate) we claim a draw; otherwise (Sargon
// is mated, or a rare unrecoverable read failure) we resign for Sargon's side.
// Draw/resign claims are safe (unlike a mate claim, which cutechess rejects if
// the position isn't actually mate).
func (e *engine) claimGameOver(res sargon.MoveResult) {
	msg := strings.ToUpper(res.Message)
	if strings.Contains(msg, "DRAW") || strings.Contains(msg, "STALEMATE") {
		// Sargon declares a draw (usually 3-fold repetition) with its own
		// drawing move still pending — one ply before cutechess would count the
		// threefold on ITS board — so a raw "1/2-1/2" result claim is rejected
		// and deadlocks the match.
		//
		// If our independent referee (fed every move as it happened) can verify
		// a specific Sargon move that forces a draw cutechess DOES recognize —
		// completing a threefold, or tripping the fifty-move rule — play THAT
		// move: cutechess then ends the game as a draw itself. Dump Sargon's
		// screen first (as the resign path does) so every adapter-judgment
		// ending is still auditable against Sargon's display.
		//
		// Only a VERIFIED move is ever played: if the referee finds none (a
		// stalemate message, an invalid tracker, or a claim that doesn't check
		// out) we keep today's behavior exactly — screen dump + resign — because
		// a wrong move that fails to end the game would leave Sargon wedged (it
		// believes the game is over) and corrupt the match.
		if coord, count, reason := e.findDrawingMove(); coord != "" {
			e.dumpScreen("draw move")
			log.Printf("SARGON-DRAW-MOVE-PLAYED move=%s reps=%d reason=%s", coord, count, reason)
			e.send("move %s", coord)
			return
		}
		bal := res.Board.MaterialBalance()
		if e.m != nil && !e.m.SargonWhite {
			bal = -bal
		}
		log.Printf("SARGON-DECLARED-DRAW material=%+d (no verified drawing move; resigning to end game; reclassify as draw)", bal)
	}
	e.resign()
}

// recoverResign turns a panic in a think goroutine into a resignation, so a
// bug can't crash the process mid-match (which cutechess sees as a disconnect
// and forfeits). Deferred at the top of each think goroutine.
func (e *engine) recoverResign(where string) {
	if r := recover(); r != nil {
		log.Printf("panic in %s: %v; resigning", where, r)
		e.resign()
	}
}

// resign resigns for Sargon's side via the CECP "resign" command (cutechess
// attributes it to this engine). A result string like "1-0 {...}" is a *claim*
// that cutechess validates against the board and rejects ("invalid result
// claim") when the game isn't actually over, so it must not be used to resign.
// Every adapter-initiated resignation dumps Sargon's full text screen to the
// log first: the dump lands immediately before cutechess's "result" line in
// the debug stream, so each game-ending decision can be audited afterward
// against what Sargon was actually displaying.
func (e *engine) resign() {
	e.dumpScreen("at resign")
	e.send("resign")
}

// dumpScreen logs Sargon's 24-row text screen with framing markers so
// post-match tooling (and humans) can correlate screens with outcomes.
func (e *engine) dumpScreen(why string) {
	if e.m == nil {
		log.Printf("SCREEN-DUMP (%s): no machine", why)
		return
	}
	log.Printf("SCREEN-DUMP-BEGIN (%s) sargonWhite=%v\n%s\nSCREEN-DUMP-END",
		why, e.m.SargonWhite, e.m.Screen())
}

// replyCoord converts Sargon's reply to xboard coordinate notation, preferring
// the authoritative on-screen move token (the RAM piece-list decode can be
// unreliable while Sargon ponders). Falls back to the RAM from/to squares.
func (e *engine) replyCoord(res sargon.MoveResult) string {
	sw := e.m != nil && e.m.SargonWhite
	// The on-screen move-list token is authoritative — it is exactly the move
	// Sargon displays (with promotion "/Q" and capture "X"), read at commit
	// time. The 8-char scrape window captures promotions in full. Use it first;
	// fall back to the RAM from/to decode only if the token can't be parsed.
	// En passant is the one capture Sargon records with NO squares ("PXPEP"), so
	// the token alone cannot name it. Resolve it against the referee history —
	// there is almost always exactly one legal e.p. capture — rather than falling
	// through to the RAM piece-list decode, which is search scratch in Hard Mode.
	if _, _, kind, ok := sargon.TokenSquares(res.SargonText); !ok && kind == "EP" {
		if c := e.uniqueEnPassant(); c != "" {
			return c
		}
	}
	if c := screenTokenToCoord(res.SargonText, sw); c != "" {
		return c
	}
	mv := res.SargonMove
	if mv.From.Valid() && mv.To.Valid() && mv.From != mv.To {
		coord := mv.From.Algebraic() + mv.To.Algebraic()
		if mv.MovedIndex >= 0 && sargon.PieceIndexType(mv.MovedIndex%16) == sargon.Pawn &&
			(mv.To.Rank() == 0 || mv.To.Rank() == 7) {
			coord += "q"
		}
		return coord
	}
	return ""
}

// screenTokenToCoord parses a Sargon move-list token ("E2-E4", "E5XD4",
// "H7-H8/Q", "0-0", "0-0-0") into xboard coordinate notation ("e2e4", "e5d4",
// "h7h8q", "e1g1", ...). Returns "" if it can't parse.
func screenTokenToCoord(tok string, sargonWhite bool) string {
	t := strings.ToUpper(strings.TrimSpace(tok))
	// Strip Sargon's check marker before matching the square-less castling
	// tokens whole: "O-O-O+" would otherwise fall through as unparseable.
	t = strings.TrimSuffix(t, "+")
	switch t {
	case "0-0", "O-O": // kingside castle
		if sargonWhite {
			return "e1g1"
		}
		return "e8g8"
	case "0-0-0", "O-O-O": // queenside castle
		if sargonWhite {
			return "e1c1"
		}
		return "e8c8"
	}
	promo := ""
	if i := strings.IndexByte(t, '/'); i >= 0 {
		if i+1 < len(t) {
			promo = strings.ToLower(string(t[i+1]))
		}
		t = t[:i]
	}
	// Expect FROM sep TO, sep is '-' or 'X'; ignore a trailing "EP".
	t = strings.TrimSuffix(t, "EP")
	if len(t) < 5 || (t[2] != '-' && t[2] != 'X') {
		return ""
	}
	from, ok1 := sargon.ParseSquare(t[0:2])
	to, ok2 := sargon.ParseSquare(t[3:5])
	if !ok1 || !ok2 {
		return ""
	}
	return from.Algebraic() + to.Algebraic() + promo
}

func (e *engine) think(coord string) {
	defer e.recoverResign("think")
	if err := e.ensureReady(); err != nil {
		// Must not return without a move/resign, or cutechess deadlocks.
		log.Printf("boot failed, resigning: %v", err)
		e.resign()
		return
	}
	sargonText, err := coordToSargon(coord)
	if err != nil {
		e.send("Illegal move: %s", coord)
		return
	}
	var res sargon.MoveResult
	if e.budgetCycles > 0 {
		res, err = e.m.RequestMove(sargonText, e.budgetCycles)
	} else {
		res, err = e.m.SubmitMove(sargonText, e.wait)
	}
	if err != nil {
		// If our move ended the game (mate/stalemate), cutechess already
		// adjudicated from the move; nothing to send. Otherwise a move-read
		// failure would deadlock the match (cutechess waits forever for a
		// move), so resign to end the game cleanly rather than hang.
		if res.GameOver {
			return
		}
		log.Printf("submit %q failed, resigning: %v", coord, err)
		e.resign()
		return
	}
	// Sargon accepted our move (enterMove confirmed it registered). Record it in
	// the referee history now, so that even if Sargon then declines to move and
	// declares a draw, the tracker sits at Sargon's to-move position and can
	// verify its drawing move. Sargon's reply (if any) is recorded below.
	e.trackApply(coord)
	reply := e.replyCoord(res)
	if e.debug {
		mv := res.SargonMove
		log.Printf("DECODE token=%q ram=%s-%s idx=%d think=%d -> %q gameover=%v msg=%q",
			res.SargonText, mv.From.Algebraic(), mv.To.Algebraic(), mv.MovedIndex,
			res.ThinkCycles, reply, res.GameOver, res.Message)
	}
	if reply == "" {
		// No decodable move: Sargon reports the game over (draw/mate) or a rare
		// read failure. Claim the correct result rather than emit a bad move.
		log.Printf("no move for %q (gameover=%v msg=%q)", coord, res.GameOver, res.Message)
		e.claimGameOver(res)
		return
	}
	// Record Sargon's reply in the referee history before emitting it (force
	// mode only delays emission; the board position is already updated).
	e.trackApply(reply)

	// Emit now unless we're in force mode, in which case hold the reply until
	// "go" arrives (which will emit it, or clear force so we emit here).
	e.smu.Lock()
	if e.force {
		e.pendingReply, e.haveReply = reply, true
		e.smu.Unlock()
		return
	}
	e.smu.Unlock()

	// Report a material score (CECP thinking line "ply score time nodes pv")
	// so cutechess can adjudicate draws (dead-equal shuffles) and resignations
	// (lopsided games) itself — this ends repetition draws cleanly (Sargon's
	// own draw claim can hang cutechess on an off-by-one 3-fold count) and
	// speeds up decided games.
	bal := res.Board.MaterialBalance() // white - black
	if e.m != nil && !e.m.SargonWhite {
		bal = -bal // from Sargon's (Black) perspective
	}
	e.send("1 %d 0 1 %s", bal, reply)
	e.send("move %s", reply)
}

// coordToSargon converts xboard coordinate notation ("e2e4", "e7e8q",
// "e1g1") to Sargon's "FROM-TO" text (with an under-promotion suffix).
func coordToSargon(coord string) (string, error) {
	coord = strings.ToLower(strings.TrimSpace(coord))
	if len(coord) < 4 {
		return "", fmt.Errorf("short move %q", coord)
	}
	from := strings.ToUpper(coord[0:2])
	to := strings.ToUpper(coord[2:4])
	if _, ok := sargon.ParseSquare(from); !ok {
		return "", fmt.Errorf("bad from %q", coord)
	}
	if _, ok := sargon.ParseSquare(to); !ok {
		return "", fmt.Errorf("bad to %q", coord)
	}
	move := from + "-" + to
	// Promotion: xboard coordinate carries a 5th char (q/r/b/n). Sargon does NOT
	// promote on the FROM-TO RETURN alone; it then prompts "ENTER PROMOTED PIECE"
	// and waits for the choice (RETURN = queen default, or N/R/B + RETURN to
	// under-promote). enterMove answers that prompt, so pass the piece letter
	// through by appending it (uppercased) to the FROM-TO text. Earlier attempts
	// that typed the letter INSIDE the FROM-TO field ("D7-D8B") were rejected as
	// INVALID MOVE — the letter must be sent only after the prompt appears.
	if len(coord) >= 5 {
		switch coord[4] {
		case 'q', 'r', 'b', 'n':
			move += strings.ToUpper(coord[4:5])
		}
	}
	return move, nil
}

// promoSuffix extracts a promotion letter from Sargon's move text (e.g.
// "H7-H8/Q") and returns it lowercased for coordinate notation, or "".
func promoSuffix(text string) string {
	if i := strings.IndexByte(text, '/'); i >= 0 && i+1 < len(text) {
		return strings.ToLower(string(text[i+1]))
	}
	return ""
}
