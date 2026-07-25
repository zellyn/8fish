package mirror

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestHarvestEndgames mines the 300-game symmetric-match log for the
// positions the loss diagnosis pointed at: games 8fish DREW or LOST after
// reaching a clearly better ENDGAME. For each requested game it replays
// the move list and reports the 8fish-to-move, phase-gated (endgame)
// position where a depth-6 search from 8fish's POV was highest — i.e. the
// exact moment 8fish believed it was winning an endgame it then failed to
// win. Those FENs seed the conversion corpus (convEndgame in
// endgame_conversion_test.go).
//
// Run (the log is a job artifact, not in the repo):
//
//	EGHARVEST=/path/to/symmatch.log GOWORK=off go test ./internal/mirror \
//	  -run TestHarvestEndgames -v -timeout 30m
func TestHarvestEndgames(t *testing.T) {
	path := os.Getenv("EGHARVEST")
	if path == "" {
		t.Skip("set EGHARVEST=<symmatch.log> to harvest endgame positions")
	}
	games, err := parseSymmatchLog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("parsed %d games from %s", len(games), path)

	want := os.Getenv("EGHARVEST_GAMES")
	var ids []int
	if want == "" {
		for id, g := range games {
			if g.result != "8fish-wins" {
				ids = append(ids, id)
			}
		}
	} else {
		for _, s := range strings.Split(want, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				t.Fatalf("bad EGHARVEST_GAMES %q", want)
			}
			ids = append(ids, n)
		}
	}
	for _, id := range ids {
		g, ok := games[id]
		if !ok {
			t.Errorf("game %d not in log", id)
			continue
		}
		fen, score, ply := bestEndgamePeak(t, g)
		if fen == "" {
			continue
		}
		t.Logf("g%-3d %-12s peak %+5d at ply %3d  %q", id, g.result, score, ply, fen)
	}
}

type symGame struct {
	id          int
	opening     string
	sargonWhite bool
	moves       []string
	result      string
}

var (
	reStart  = regexp.MustCompile(`^GAME (\d+) START: sargonWhite=(true|false).*opening="([^"]+)"`)
	reResult = regexp.MustCompile(`^GAME (\d+) RESULT: (\S+)`)
	reMove   = regexp.MustCompile(`move=([a-h][1-8][a-h][1-8][nbrq]?)`)
	reMoveG  = regexp.MustCompile(`^MOVE g(\d+) `)
)

// parseSymmatchLog reconstructs each game's opening FEN + move list from a
// sargon-symmatch log (GAME START / MOVE / GAME RESULT lines).
func parseSymmatchLog(path string) (map[int]*symGame, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[int]*symGame{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := reStart.FindStringSubmatch(line); m != nil {
			id, _ := strconv.Atoi(m[1])
			out[id] = &symGame{id: id, sargonWhite: m[2] == "true", opening: m[3] + " 0 1"}
			continue
		}
		if m := reResult.FindStringSubmatch(line); m != nil {
			id, _ := strconv.Atoi(m[1])
			if g := out[id]; g != nil {
				g.result = m[2]
			}
			continue
		}
		if m := reMoveG.FindStringSubmatch(line); m != nil {
			id, _ := strconv.Atoi(m[1])
			g := out[id]
			if g == nil {
				continue
			}
			// A MOVE line carries one or two half-moves in play order.
			for _, mv := range reMove.FindAllStringSubmatch(line, -1) {
				g.moves = append(g.moves, mv[1])
			}
		}
	}
	return out, sc.Err()
}

// bestEndgamePeak replays a game and returns the FEN of the 8fish-to-move
// ENDGAME position (Phase <= DefaultEndgame.PhaseMax) whose depth-6 score
// from 8fish's POV is highest, plus that score and the ply.
func bestEndgamePeak(t *testing.T, g *symGame) (string, int, int) {
	t.Helper()
	start, err := ParseFEN(g.opening)
	if err != nil {
		t.Errorf("g%d opening %q: %v", g.id, g.opening, err)
		return "", 0, 0
	}
	eng := NewEngine()
	eng.Seed = 0
	gp := *start
	fishWhite := !g.sargonWhite
	bestFEN, bestScore, bestPly := "", -Inf, 0
	for i, ms := range g.moves {
		fishToMove := (gp.Side == 0) == fishWhite
		eng.SetPosition(&gp)
		if fishToMove && eng.Pos.Phase <= DefaultEndgame.PhaseMax && i > 20 {
			eng.ClearTT()
			_, sc := eng.SearchFixed(6)
			if sc > bestScore {
				bestFEN, bestScore, bestPly = gp.FEN(), sc, i+1
			}
		}
		if err := applyUCI(eng, &gp, ms); err != nil {
			// The log's move list can end mid-line at adjudication; stop.
			break
		}
	}
	if bestFEN == "" {
		return "", 0, 0
	}
	return bestFEN, bestScore, bestPly
}

// TestEndgameCorpusValid parses every FEN in the conversion corpus (a
// typo in a harvested FEN would silently skew the whole screen) and prints
// its phase / material so the corpus composition is auditable.
func TestEndgameCorpusValid(t *testing.T) {
	e := NewEngine()
	for _, c := range convEndgame {
		p, err := ParseFEN(c.fen)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		e.SetPosition(p)
		e.Seed = 0
		// Legality: kings not adjacent, and the side NOT to move is not in
		// check (a hand-written FEN can easily be an impossible position,
		// which would poison the whole screen).
		if cheb(e.Pos.PieceSq[0], e.Pos.PieceSq[16]) <= 1 {
			t.Errorf("%s: ILLEGAL, kings adjacent", c.name)
		}
		idle := e.Pos.PieceSq[0]
		if e.Pos.Side == 0 {
			idle = e.Pos.PieceSq[16]
		}
		if e.attacked(idle, e.Pos.Side) {
			t.Errorf("%s: ILLEGAL, the side not to move is in check", c.name)
		}
		if e.Pos.Phase > DefaultEndgame.PhaseMax {
			t.Errorf("%s: phase %d is ABOVE the gate %d — the term is silent here",
				c.name, e.Pos.Phase, DefaultEndgame.PhaseMax)
		}
		t.Logf("%-14s phase %2d  staticEval(stm) %+5d  want=%s", c.name, e.Pos.Phase, e.eval(), c.want)
	}
	fmt.Fprintln(os.Stderr)
}
