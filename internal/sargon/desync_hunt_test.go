package sargon

import (
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/refchess"
)

// Desync regression / self-play soak. In the 2026-07 standard-start match, 17 of
// 300 games (5.7%) ended with Sargon's "reply" being a STALE token — its own
// PREVIOUS move, already on our board. 14 of the 17 had Sargon as White and hit
// it on the FIRST move Sargon had to actually search (every earlier reply came
// straight from its opening book).
//
// Root cause (measured in TestRepaintWatch): while searching, Sargon BLANKS the
// bottom move-list rows and repaints them ~167K cycles later. The old commit
// detector compared the raw move-list column TEXT, so a poll landing inside that
// blank frame looked like a commit; RequestMove then returned without ever
// sending CTRL-T and read the bottom-most surviving token. Commit detection now
// keys on the displayed move NUMBER (sargonCommitted), which never goes
// backwards on a repaint.
//
// This test plays real games through the real RequestMove and cross-checks
// Sargon's own displayed move list against our referee history after EVERY move.

// tokToUCI mirrors the harness's screen-token decoder (cmd/sargon-symmatch's
// screenTokenToCoord) so the test reads Sargon's replies exactly as the match does.
func tokToUCI(tok string, sargonWhite bool, ref *refchess.Position) string {
	from, to, kind, ok := TokenSquares(tok)
	switch {
	case ok:
		promo := ""
		t := strings.ToUpper(strings.TrimSpace(tok))
		if i := strings.IndexByte(t, '/'); i >= 0 && i+1 < len(t) {
			promo = strings.ToLower(string(t[i+1]))
			if promo == "+" {
				promo = ""
			}
		}
		return from.Algebraic() + to.Algebraic() + promo
	case kind == "O-O":
		if sargonWhite {
			return "e1g1"
		}
		return "e8g8"
	case kind == "O-O-O":
		if sargonWhite {
			return "e1c1"
		}
		return "e8c8"
	case kind == "EP":
		if eps := ref.EnPassantCaptures(); len(eps) == 1 {
			return eps[0].String()
		}
	}
	return ""
}

func uciToSargonTok(uci string) string {
	s := strings.ToUpper(uci[0:2]) + "-" + strings.ToUpper(uci[2:4])
	if len(uci) >= 5 {
		s += strings.ToUpper(uci[4:5])
	}
	return s
}

// histMove builds the cross-check record for mv in position p (BEFORE Make).
func histMove(p *refchess.Position, mv refchess.Move) HistMove {
	h := HistMove{From: mv.String()[0:2], To: mv.String()[2:4]}
	switch p.SAN(mv) {
	case "O-O", "O-O+", "O-O#":
		h.Castle = "O-O"
	case "O-O-O", "O-O-O+", "O-O-O#":
		h.Castle = "O-O-O"
	}
	for _, ep := range p.EnPassantCaptures() {
		if ep.From == mv.From && ep.To == mv.To {
			h.EnPassant = true
		}
	}
	return h
}

// TestSelfPlaySoak plays one game against Sargon with random legal replies,
// verifying after every ply that (a) Sargon's reply is a NEW, legal move and
// (b) Sargon's own displayed move list agrees with our history move for move.
//
// Env: SARGON_SEED picks the reply stream, SARGON_PLIES caps the length,
// SARGON_BLACK=1 makes Sargon play Black (we open).
func TestSelfPlaySoak(t *testing.T) {
	skipUnlessSlow(t)
	seed := envInt("SARGON_SEED", 1)
	maxPly := int(envInt("SARGON_PLIES", 24))
	sargonWhite := os.Getenv("SARGON_BLACK") == ""
	rnd := rand.New(rand.NewSource(seed))
	const budget = 30_000_000

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

	ref, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	var hist []HistMove

	// applySargon decodes Sargon's token, checks legality, records history.
	applySargon := func(ply int, tok string) bool {
		uci := tokToUCI(tok, sargonWhite, ref)
		mv, err := refchess.ParseMove(uci)
		if err != nil {
			t.Errorf("seed=%d ply%d UNREADABLE token=%q\n%s\nour history: %s",
				seed, ply, tok, m.ScreenDump("unreadable"), HistoryString(hist))
			return false
		}
		h := histMove(ref, mv)
		if err := ref.Make(mv); err != nil {
			t.Errorf("seed=%d ply%d ILLEGAL token=%q uci=%q: %v\n%s\nour history: %s",
				seed, ply, tok, uci, err, m.ScreenDump("illegal"), HistoryString(hist))
			return false
		}
		hist = append(hist, h)
		return true
	}

	crossCheck := func(ply int) bool {
		ok, bad, detail := m.CrossCheckHistory(hist, 1)
		if !ok {
			t.Errorf("seed=%d ply%d *** MOVE-LIST DESYNC *** first bad ply %d: %s\n%s\nour history: %s",
				seed, ply, bad, detail, m.ScreenDump("desync"), HistoryString(hist))
		}
		return ok
	}

	ply := 0
	if sargonWhite {
		m.SargonWhite = true
		res, err := m.StartAsWhite(budget)
		if err != nil {
			t.Fatalf("StartAsWhite: %v\n%s", err, m.ScreenDump("open-fail"))
		}
		ply++
		if !applySargon(ply, res.SargonText) || !crossCheck(ply) {
			return
		}
		t.Logf("seed=%d ply%d SARGON %q think=%d", seed, ply, res.SargonText, res.ThinkCycles)
	}

	for ply < maxPly {
		legal := ref.LegalMoves()
		if len(legal) == 0 {
			t.Logf("seed=%d game over at ply %d", seed, ply)
			return
		}
		our := legal[rnd.Intn(len(legal))]
		oh := histMove(ref, our)
		if err := ref.Make(our); err != nil {
			t.Fatal(err)
		}
		hist = append(hist, oh)
		ply++

		ponderWindow(m, 15_000_000+uint64(rnd.Intn(45_000_000)))
		res, err := m.RequestMove(uciToSargonTok(our.String()), budget)
		if err != nil {
			t.Logf("seed=%d ply%d RequestMove(%s): %v (msg=%q gameOver=%v)\n%s",
				seed, ply, our.String(), err, res.Message, res.GameOver, m.ScreenDump("no-reply"))
			return
		}
		if !crossCheck(ply) { // our own move must be recorded correctly too
			return
		}
		if res.GameOver && res.SargonText == "" {
			t.Logf("seed=%d ply%d our move ended the game (msg=%q)", seed, ply, res.Message)
			return
		}
		ply++
		if !applySargon(ply, res.SargonText) || !crossCheck(ply) {
			return
		}
		t.Logf("seed=%d ply%d our=%s | ply%d SARGON %q think=%d msg=%q",
			seed, ply-1, our.String(), ply, res.SargonText, res.ThinkCycles, res.Message)
	}
	t.Logf("seed=%d completed %d plies, move list matched throughout", seed, ply)
}

func envInt(name string, def int64) int64 {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return def
}
