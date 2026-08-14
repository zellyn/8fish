package book

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"strings"

	"github.com/zellyn/8fish/internal/refchess"
)

// eco.go parses the vendored lichess chess-openings dataset
// (internal/book/lichess, CC0 — see its README.md) into book.Lines. This is
// the BIG BOOK's breadth source and nothing else's: the small resident book is
// compiled from the hand-curated openings.txt alone, and BuildBig keeps the
// curated entries byte-identical while importing these lines around them.
//
// Every move is validated legal move-by-move through refchess, exactly like
// the curated compiler in cmd/genbook: an illegal or malformed row fails the
// whole parse loudly, naming the file, row and move. The returned order is
// the DETERMINISTIC priority order BuildBig ties on: a.tsv rows first (in file
// order), then b..e — the dataset lists canonical lines early, so when two
// rows offer different moves at the same position the earlier row wins.

//go:embed lichess/a.tsv lichess/b.tsv lichess/c.tsv lichess/d.tsv lichess/e.tsv
var ecoTSV embed.FS

// ecoFiles is the fixed read order — the low half of BuildBig's tiebreak.
var ecoFiles = []string{"a.tsv", "b.tsv", "c.tsv", "d.tsv", "e.tsv"}

// ECOLines parses the embedded lichess dataset into lines (Side is left
// SideBoth; BuildBig imports each line for both colors itself). ECO is the
// row's ECO code, Name its full lichess name ("Sicilian Defense: Najdorf
// Variation"), Moves the SAN column compiled to UCI through refchess.
func ECOLines() ([]Line, error) {
	var lines []Line
	for _, fn := range ecoFiles {
		data, err := ecoTSV.ReadFile("lichess/" + fn)
		if err != nil {
			return nil, fmt.Errorf("book: embedded lichess/%s: %w", fn, err)
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		row := 0
		for sc.Scan() {
			row++
			t := sc.Text()
			if row == 1 && strings.HasPrefix(t, "eco\t") {
				continue // the header row
			}
			if strings.TrimSpace(t) == "" {
				continue
			}
			f := strings.Split(t, "\t")
			if len(f) != 3 {
				return nil, fmt.Errorf("book: lichess/%s row %d: %d columns, want 3 (eco, name, pgn)",
					fn, row, len(f))
			}
			eco, name, pgn := strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), f[2]
			if len(eco) != 3 || eco[0] < 'A' || eco[0] > 'E' {
				return nil, fmt.Errorf("book: lichess/%s row %d: bad ECO code %q", fn, row, eco)
			}
			if name == "" {
				return nil, fmt.Errorf("book: lichess/%s row %d: empty name", fn, row)
			}
			moves, err := compileECOSAN(pgn)
			if err != nil {
				return nil, fmt.Errorf("book: lichess/%s row %d (%s %s): %w", fn, row, eco, name, err)
			}
			lines = append(lines, Line{ECO: eco, Name: name, Side: SideBoth, Moves: moves})
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("book: lichess/%s: %w", fn, err)
		}
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("book: the embedded lichess dataset is empty")
	}
	return lines, nil
}

// compileECOSAN walks a PGN movetext fragment ("1. e4 c5 2. Nf3") from the
// start position, resolving each SAN token against refchess's legal moves
// (which validates legality and rejects ambiguity), and returns the UCI move
// list. Move-number tokens are stripped; refchess.ParseSAN itself ignores
// trailing check/mate/annotation glyphs.
func compileECOSAN(pgn string) ([]string, error) {
	pos, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		return nil, err
	}
	var uci []string
	ply := 0
	for _, tok := range strings.Fields(pgn) {
		san := stripECOMoveNumber(tok)
		if san == "" {
			continue
		}
		ply++
		mv, err := pos.ParseSAN(san)
		if err != nil {
			return nil, fmt.Errorf("ply %d %q: %w", ply, san, err)
		}
		uci = append(uci, mv.String())
		if err := pos.Make(mv); err != nil {
			return nil, fmt.Errorf("ply %d %q: illegal: %w", ply, san, err)
		}
	}
	if len(uci) == 0 {
		return nil, fmt.Errorf("no moves")
	}
	return uci, nil
}

// stripECOMoveNumber removes a leading move number ("1.", "12...", or glued
// "1.e4" -> "e4") from a PGN token; a bare number collapses to "". Same
// tolerant tokenizer as cmd/genbook's, restated here because the dataset
// parser must not depend on a main package.
func stripECOMoveNumber(tok string) string {
	i := 0
	for i < len(tok) && tok[i] >= '0' && tok[i] <= '9' {
		i++
	}
	if i == 0 {
		return tok
	}
	if i < len(tok) && tok[i] == '.' {
		for i < len(tok) && tok[i] == '.' {
			i++
		}
		return tok[i:]
	}
	if i == len(tok) {
		return ""
	}
	return tok
}
