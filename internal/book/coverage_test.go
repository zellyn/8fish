package book

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
	"github.com/zellyn/chess6502/internal/refchess"
)

// ---------------------------------------------------------------------------
// Book coverage measurement.
//
// The book's job is BREADTH: not to drill lines deep, but to answer whatever
// the opponent actually plays for as many plies as possible. The number that
// matters is therefore not "how many lines" or "how many bytes" but:
//
//	at each position where the OPPONENT is to move and we are still in book,
//	what fraction of the moves the opponent might actually play leave us
//	still in book?
//
// A book move of ours is never the thing that ends the book — we always have
// a reply to our own line. The book ends when the opponent steps off it. So
// every measurement below is organised around opponent-to-move nodes.
//
// These tests only READ the compiled book, so they are cheap and are meant to
// be re-run before and after any change to openings.txt.
// ---------------------------------------------------------------------------

// keyIndex maps a book key to all entries sharing it.
func keyIndex(b *Book) map[uint32][]Entry {
	m := map[uint32][]Entry{}
	for _, e := range b.Entries() {
		m[e.Key] = append(m[e.Key], e)
	}
	return m
}

// entryUCI is the inverse of uciTo0x88: render a book entry as a UCI move.
func entryUCI(e Entry) string {
	sq := func(s byte) string {
		return fmt.Sprintf("%c%c", 'a'+(s&0x0F), '1'+(s>>4))
	}
	s := sq(e.From) + sq(e.To)
	switch e.Flags & 7 {
	case 2:
		s += "n"
	case 3:
		s += "b"
	case 4:
		s += "r"
	case 5:
		s += "q"
	}
	return s
}

// inBook reports whether we have a move for this position.
func inBook(idx map[uint32][]Entry, p *refchess.Position) bool {
	k, err := HashFEN(p.FEN())
	if err != nil {
		return false
	}
	_, ok := idx[k]
	return ok
}

func mustStart(t *testing.T) *refchess.Position {
	t.Helper()
	p, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// child returns a copy of p with m made.
func child(t *testing.T, p *refchess.Position, m refchess.Move) *refchess.Position {
	t.Helper()
	q := p.Copy()
	if err := q.Make(m); err != nil {
		t.Fatal(err)
	}
	return q
}

// ---------------------------------------------------------------------------
// A. Shape of the book: size, depth, and where the entries actually go.
// ---------------------------------------------------------------------------

func TestBookShape(t *testing.T) {
	b, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	idx := keyIndex(b)
	multi := 0
	for _, es := range idx {
		if len(es) > 1 {
			multi++
		}
	}
	t.Logf("blob %d B of %d capacity (%.1f%%), %d free",
		b.Size(), MaxSize, 100*float64(b.Size())/float64(MaxSize), MaxSize-b.Size())
	t.Logf("%d entries, %d distinct positions (%d with >1 move), %d names",
		len(b.Entries()), len(idx), multi, len(b.names))

	// Ply histogram: how deep does the book reach, and how much of it is
	// depth rather than breadth? Walk the book graph from the start.
	seen := map[uint32]int{} // key -> shallowest ply seen
	type node struct {
		p   *refchess.Position
		ply int
	}
	start := mustStart(t)
	k0, _ := HashFEN(start.FEN())
	queue := []node{{start, 0}}
	seen[k0] = 0
	perPly := map[int]int{}
	maxPly := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		perPly[n.ply]++
		if n.ply > maxPly {
			maxPly = n.ply
		}
		k, _ := HashFEN(n.p.FEN())
		for _, e := range idx[k] {
			mv, err := refchess.ParseMove(entryUCI(e))
			if err != nil {
				t.Fatalf("entry %+v: %v", e, err)
			}
			q := child(t, n.p, mv)
			qk, _ := HashFEN(q.FEN())
			if _, ok := idx[qk]; !ok {
				continue // leaf: we have no further move
			}
			if _, dup := seen[qk]; dup {
				continue
			}
			seen[qk] = n.ply + 1
			queue = append(queue, node{q, n.ply + 1})
		}
	}
	var sb strings.Builder
	for ply := 0; ply <= maxPly; ply++ {
		fmt.Fprintf(&sb, " %d:%d", ply, perPly[ply])
	}
	t.Logf("in-book positions by ply (following our own lines):%s", sb.String())
	t.Logf("deepest book ply: %d", maxPly)
}

// ---------------------------------------------------------------------------
// B. THE HEADLINE MEASUREMENT: opponent-reply coverage.
//
// Walk every position reachable by following our own book moves. At each node
// where the opponent is to move, enumerate ALL its legal moves and count how
// many leave us in book. Report the shallow nodes explicitly (those are the
// ones a real opponent reaches every game) and aggregate the rest.
// ---------------------------------------------------------------------------

type oppNode struct {
	ply      int
	fen      string
	line     string   // SAN path to here
	covered  []string // opponent replies that keep us in book
	total    int      // legal replies
	pathProb float64  // product of our book weights along the path
}

// collectOppNodes walks our book lines and returns every opponent-to-move node.
// ourColor is refchess white (0) or black (1) — the colour the BOOK plays.
func collectOppNodes(t *testing.T, b *Book, idx map[uint32][]Entry, ourColor byte) []oppNode {
	t.Helper()
	var out []oppNode
	seen := map[uint32]bool{}

	type frame struct {
		p    *refchess.Position
		ply  int
		line string
		prob float64
	}
	start := mustStart(t)
	queue := []frame{{start, 0, "", 1}}
	// If we are black, white moves first from an out-of-book-for-us root:
	// the root IS an opponent node.
	for len(queue) > 0 {
		f := queue[0]
		queue = queue[1:]
		k, _ := HashFEN(f.p.FEN())
		if seen[k] {
			continue
		}
		seen[k] = true

		if f.p.SideToMove() == ourColor {
			// Our move: follow every book move (that is the variety we own).
			es := idx[k]
			var total uint32
			for _, e := range es {
				total += uint32(e.Weight)
			}
			for _, e := range es {
				mv, err := refchess.ParseMove(entryUCI(e))
				if err != nil {
					t.Fatal(err)
				}
				san := f.p.SAN(mv)
				q := child(t, f.p, mv)
				queue = append(queue, frame{q, f.ply + 1, joinLine(f.line, f.ply, san),
					f.prob * float64(e.Weight) / float64(total)})
			}
			continue
		}

		// Opponent to move: which of its legal moves keep us in book?
		legal := f.p.LegalMoves()
		n := oppNode{ply: f.ply, fen: f.p.FEN(), line: f.line, total: len(legal), pathProb: f.prob}
		for _, mv := range legal {
			q := child(t, f.p, mv)
			if !inBook(idx, q) {
				continue
			}
			n.covered = append(n.covered, f.p.SAN(mv))
			queue = append(queue, frame{q, f.ply + 1, joinLine(f.line, f.ply, f.p.SAN(mv)), f.prob})
		}
		sort.Strings(n.covered)
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ply != out[j].ply {
			return out[i].ply < out[j].ply
		}
		return out[i].line < out[j].line
	})
	return out
}

func joinLine(line string, ply int, san string) string {
	if line == "" {
		if ply%2 == 0 {
			return fmt.Sprintf("1.%s", san)
		}
		return fmt.Sprintf("1...%s", san)
	}
	if ply%2 == 0 {
		return fmt.Sprintf("%s %d.%s", line, ply/2+1, san)
	}
	return line + " " + san
}

func TestBookOpponentCoverage(t *testing.T) {
	small, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	big, err := DefaultBigBook()
	if err != nil {
		t.Fatal(err)
	}
	// The BIG book's coverage floors, per color and ply — the GATE on the
	// lichess ECO import (book.BuildBig). The 633-entry curated book measured
	// (playing White) 6.5% at ply 3, 3.4% at ply 5, 2.4% at ply 7; with the
	// import the same walk measures 11.3% / 5.0% / 4.1%, and playing Black
	// (even plies) 18.5% / 6.0% / 3.9% at plies 2 / 4 / 6.
	//
	// Those post-fill numbers are the DATASET'S CEILING, not a tuning choice:
	// the fill is shallow-first and the 3,639-entry cap only starts dropping
	// positions at ply 11, so every position the lichess dataset knows at
	// plies <= 10 is in the book — coverage at these plies depends only on
	// which positions carry entries, and all of them do. The rest of the
	// opponent's legal replies are moves no named opening plays, which a
	// popularity dataset cannot answer (curated /w and /b lines are how those
	// get covered, one junk move at a time).
	//
	// Floors sit between the curated-only baseline and the measured ceiling:
	// under the ceiling so openings.txt edits (which shift the reachable-node
	// mix) don't trip them, far enough above the baseline that "the import
	// quietly stopped working" cannot pass.
	floors := map[byte]map[int]float64{
		0: {3: 9, 5: 4, 7: 3},
		1: {2: 15, 4: 5, 6: 3},
	}
	for _, bk := range []struct {
		label  string
		b      *Book
		assert bool
	}{{"SMALL (resident) book", small, false}, {"BIG (disk) book", big, true}} {
		idx := keyIndex(bk.b)
		for _, side := range []struct {
			name  string
			color byte
		}{{"BOOK PLAYS WHITE", 0}, {"BOOK PLAYS BLACK", 1}} {
			nodes := collectOppNodes(t, bk.b, idx, side.color)
			t.Logf("=== %s, %s: %d opponent-to-move nodes reachable in book ===",
				bk.label, side.name, len(nodes))

			// Per-ply aggregate.
			byPly := map[int][]oppNode{}
			for _, n := range nodes {
				byPly[n.ply] = append(byPly[n.ply], n)
			}
			plies := make([]int, 0, len(byPly))
			for p := range byPly {
				plies = append(plies, p)
			}
			sort.Ints(plies)
			pct := map[int]float64{}
			for _, p := range plies {
				ns := byPly[p]
				var cov, tot int
				dead := 0
				for _, n := range ns {
					cov += len(n.covered)
					tot += n.total
					if len(n.covered) == 0 {
						dead++
					}
				}
				pct[p] = 100 * float64(cov) / float64(tot)
				t.Logf("  ply %2d: %3d nodes, %4d/%4d legal replies covered (%.1f%%), %d nodes with ZERO covered replies",
					p, len(ns), cov, tot, pct[p], dead)
			}
			if bk.assert {
				for _, p := range sortedKeys(floors[side.color]) {
					if got, want := pct[p], floors[side.color][p]; got < want {
						t.Errorf("%s, %s: ply %d opponent-reply coverage %.1f%% is under the "+
							"%.0f%% floor — the ECO breadth import has regressed "+
							"(curated-only baseline was ~6.5%% at ply 3)",
							bk.label, side.name, p, got, want)
					}
				}
			}
			if !bk.assert {
				// The shallow nodes verbatim — the ones every game hits. Small
				// book only: the big book has hundreds of shallow nodes.
				for _, n := range nodes {
					if n.ply > 3 {
						continue
					}
					t.Logf("  [ply %d] after %-24s : %2d/%2d covered: %s",
						n.ply, orDash(n.line), len(n.covered), n.total, strings.Join(n.covered, " "))
				}
			}
		}
	}
}

// sortedKeys returns m's keys ascending (deterministic assertion order).
func sortedKeys(m map[int]float64) []int {
	var ks []int
	for k := range m {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	return ks
}

func orDash(s string) string {
	if s == "" {
		return "(start)"
	}
	return s
}

// ---------------------------------------------------------------------------
// C. Realistic-opponent walk: how deep does the book actually survive against
// an engine that is choosing moves on merit rather than enumerating all of
// them? The mirror engine in its asm-matched configuration is the stand-in for
// 8fish; it is the opponent an ordinary self-play match would put across the
// board, and it is deterministic at fixed depth so the walk is reproducible.
// ---------------------------------------------------------------------------

// mirrorMove returns the asm-matched mirror engine's choice at fixed depth.
func mirrorMove(t *testing.T, fen string, depth int) (string, int) {
	t.Helper()
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	e := mirror.NewEngine()
	e.Features = mirror.FtAll
	e.Weights = mirror.DefaultWeights
	e.QS = mirror.DefaultQS
	e.SetPosition(mp)
	mv, score := e.SearchFixed(depth)
	return mv.UCI(), score
}

func TestBookExitAgainstEngineOpponent(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs real searches")
	}
	b, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	idx := keyIndex(b)
	const depth = 6

	// For every book line we might steer into, play it out: we take book
	// moves (all of them, to cover the whole book), the opponent takes the
	// engine's choice. Record the ply at which the book runs out and the
	// engine's own evaluation of the position we are left in.
	for _, side := range []struct {
		name  string
		color byte
	}{{"BOOK WHITE vs engine BLACK", 0}, {"BOOK BLACK vs engine WHITE", 1}} {
		type exit struct {
			ply   int
			line  string
			score int
			why   string
		}
		var exits []exit
		seen := map[uint32]bool{}
		type frame struct {
			p    *refchess.Position
			ply  int
			line string
		}
		queue := []frame{{mustStart(t), 0, ""}}
		for len(queue) > 0 {
			f := queue[0]
			queue = queue[1:]
			k, _ := HashFEN(f.p.FEN())
			if seen[k] {
				continue
			}
			seen[k] = true

			if f.p.SideToMove() == side.color {
				es := idx[k]
				if len(es) == 0 {
					// We are out of book on our own move.
					_, sc := mirrorMove(t, f.p.FEN(), depth)
					exits = append(exits, exit{f.ply, f.line, sc, "our move, no book entry"})
					continue
				}
				for _, e := range es {
					mv, err := refchess.ParseMove(entryUCI(e))
					if err != nil {
						t.Fatal(err)
					}
					san := f.p.SAN(mv)
					queue = append(queue, frame{child(t, f.p, mv), f.ply + 1, joinLine(f.line, f.ply, san)})
				}
				continue
			}

			// Opponent to move: the engine picks ONE move.
			uci, _ := mirrorMove(t, f.p.FEN(), depth)
			mv, err := refchess.ParseMove(uci)
			if err != nil {
				t.Fatal(err)
			}
			san := f.p.SAN(mv)
			q := child(t, f.p, mv)
			line := joinLine(f.line, f.ply, san)
			if !inBook(idx, q) {
				_, sc := mirrorMove(t, q.FEN(), depth)
				// sc is from the side-to-move's POV; the side to move is US.
				exits = append(exits, exit{f.ply + 1, line, sc, "opponent played " + san})
				continue
			}
			queue = append(queue, frame{q, f.ply + 1, line})
		}
		sort.Slice(exits, func(i, j int) bool { return exits[i].ply < exits[j].ply })
		var sum, n int
		hist := map[int]int{}
		var scoreSum int
		for _, e := range exits {
			sum += e.ply
			n++
			hist[e.ply]++
			scoreSum += e.score
		}
		t.Logf("=== %s (opponent = mirror @ depth %d) ===", side.name, depth)
		t.Logf("  %d distinct book exits; mean exit ply %.2f; mean eval at exit %+d cp (our POV)",
			n, float64(sum)/float64(max(n, 1)), scoreSum/max(n, 1))
		var keys []int
		for k := range hist {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			t.Logf("    exit at ply %2d: %d", k, hist[k])
		}
		for i, e := range exits {
			if i >= 25 {
				t.Logf("    ... (%d more)", len(exits)-25)
				break
			}
			t.Logf("    ply %2d  %+5d cp  %-46s  [%s]", e.ply, e.score, e.line, e.why)
		}
	}
}

// ---------------------------------------------------------------------------
// D. Coverage against REAL recorded games. Point BOOKPGN at a PGN archive
// (e.g. a Sargon III match) and this replays every game, reporting the ply at
// which the book would have run out and which opponent move ended it. This is
// the honest external check on the structural numbers above: it measures the
// book against moves a real opponent really played, not against a model.
// ---------------------------------------------------------------------------

func TestBookCoverageAgainstPGN(t *testing.T) {
	path := os.Getenv("BOOKPGN")
	if path == "" {
		t.Skip("set BOOKPGN=<file.pgn> to measure coverage against recorded games")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	idx := keyIndex(b)

	games := splitPGNGames(string(raw))
	t.Logf("%s: %d games", path, len(games))

	exitHist := map[int]int{}
	killers := map[string]int{} // "ply N: SAN" -> count
	firstMoves := map[string]int{}
	replies := map[string]map[string]int{}
	var total, sum int
	for _, g := range games {
		sans := pgnMoves(g)
		if len(sans) == 0 {
			continue
		}
		p, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		firstMoves[sans[0]]++
		exited := -1
		for i, san := range sans {
			if i >= 24 {
				break
			}
			mv, err := p.ParseSAN(san)
			if err != nil {
				break // parser limits (e.g. game text oddities); stop this game
			}
			if i >= 1 && i <= 3 {
				key := fmt.Sprintf("after %s", strings.Join(sans[:i], " "))
				if replies[key] == nil {
					replies[key] = map[string]int{}
				}
				replies[key][san]++
			}
			if err := p.Make(mv); err != nil {
				break
			}
			if !inBook(idx, p) {
				exited = i + 1
				killers[fmt.Sprintf("ply %2d after %-22s: %s", i+1, strings.Join(sans[:i], " "), san)]++
				break
			}
		}
		if exited < 0 {
			exited = 24
		}
		exitHist[exited]++
		total++
		sum += exited
	}
	t.Logf("mean ply at which the game leaves the book: %.2f (over %d games)",
		float64(sum)/float64(max(total, 1)), total)
	var ks []int
	for k := range exitHist {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	for _, k := range ks {
		t.Logf("  left book at ply %2d: %3d games", k, exitHist[k])
	}
	t.Logf("first moves played (both engines, game start):")
	for _, kv := range topN(firstMoves, 12) {
		t.Logf("   %-6s %d", kv.k, kv.v)
	}
	t.Logf("most common book-ending moves:")
	for _, kv := range topN(killers, 20) {
		t.Logf("   %-52s %d", kv.k, kv.v)
	}
	var rk []string
	for k := range replies {
		rk = append(rk, k)
	}
	sort.Strings(rk)
	for _, k := range rk {
		if len(replies[k]) == 0 {
			continue
		}
		var parts []string
		for _, kv := range topN(replies[k], 8) {
			mark := ""
			parts = append(parts, fmt.Sprintf("%s%s:%d", kv.k, mark, kv.v))
		}
		t.Logf("   %-30s -> %s", k, strings.Join(parts, " "))
	}
}

type kv struct {
	k string
	v int
}

func topN(m map[string]int, n int) []kv {
	var out []kv
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].v != out[j].v {
			return out[i].v > out[j].v
		}
		return out[i].k < out[j].k
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

var pgnCommentRe = regexp.MustCompile(`\{[^}]*\}`)
var pgnNumRe = regexp.MustCompile(`\b\d+\.(\.\.)?`)

func splitPGNGames(s string) []string {
	parts := strings.Split(s, "[Event ")
	var out []string
	for _, p := range parts[1:] {
		out = append(out, "[Event "+p)
	}
	return out
}

// pgnMoves extracts the SAN move list from one PGN game's movetext.
func pgnMoves(game string) []string {
	i := strings.Index(game, "\n\n")
	if i < 0 {
		return nil
	}
	text := game[i+2:]
	text = pgnCommentRe.ReplaceAllString(text, " ")
	text = pgnNumRe.ReplaceAllString(text, " ")
	var out []string
	for _, f := range strings.Fields(text) {
		switch f {
		case "1-0", "0-1", "1/2-1/2", "*":
			return out
		}
		if strings.HasPrefix(f, "$") || strings.HasPrefix(f, "(") {
			continue
		}
		out = append(out, strings.TrimRight(f, "!?"))
	}
	return out
}

// ---------------------------------------------------------------------------
// E. Transpositions. A breadth line that reaches a position an EARLIER line
// already established costs only the plies up to the join and then inherits
// that line's whole remaining depth, because the book is keyed on the position
// and not on the move order. This is the cheapest breadth in the file, so it
// is worth knowing which lines are doing it — and a test is better than a
// comment, because it cannot go stale when openings.txt is edited.
// ---------------------------------------------------------------------------

func TestBookTranspositions(t *testing.T) {
	established := map[uint32]string{} // key -> the line that first reached it
	var trans []string
	for _, ln := range Lines {
		pos, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		var keys []uint32
		for _, uci := range ln.Moves {
			k, err := HashFEN(pos.FEN())
			if err != nil {
				t.Fatal(err)
			}
			keys = append(keys, k)
			mv, err := refchess.ParseMove(uci)
			if err != nil {
				t.Fatal(err)
			}
			if err := pos.Make(mv); err != nil {
				t.Fatal(err)
			}
		}
		final, err := HashFEN(pos.FEN())
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, final)
		if prev, ok := established[final]; ok {
			trans = append(trans, fmt.Sprintf("%-4s %-26s -> %s", ln.ECO, ln.Name, prev))
		}
		for _, k := range keys {
			if _, ok := established[k]; !ok {
				established[k] = ln.ECO + " " + ln.Name
			}
		}
	}
	t.Logf("%d of %d lines transpose into a line declared earlier in openings.txt:",
		len(trans), len(Lines))
	for _, s := range trans {
		t.Logf("   %s", s)
	}
}

// ---------------------------------------------------------------------------
// F. OUR OWN REPERTOIRE. Every entry carries a weight, and the weight is just
// "how many curated lines played this move from this position". That makes the
// weight a side effect of how the file is written, not a judgement — and it is
// the one way a breadth line can hurt: a short line added only to have an
// ANSWER ready still bumps the weight of its own first move, quietly shifting
// how often the engine opens 1.e4 versus 1.d4. This prints the distribution so
// that shift is visible instead of implicit.
// ---------------------------------------------------------------------------

func TestBookRepertoire(t *testing.T) {
	b, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	idx := keyIndex(b)
	report := func(label string, moves []string) {
		p, err := refchess.ParseFEN(refchess.StartFEN)
		if err != nil {
			t.Fatal(err)
		}
		for _, san := range moves {
			mv, err := p.ParseSAN(san)
			if err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if err := p.Make(mv); err != nil {
				t.Fatal(err)
			}
		}
		k, _ := HashFEN(p.FEN())
		es := idx[k]
		if len(es) == 0 {
			t.Logf("%-22s (not in book)", label)
			return
		}
		var total int
		for _, e := range es {
			total += int(e.Weight)
		}
		sort.Slice(es, func(i, j int) bool { return es[i].Weight > es[j].Weight })
		var parts []string
		for _, e := range es {
			mv, err := refchess.ParseMove(entryUCI(e))
			if err != nil {
				t.Fatal(err)
			}
			parts = append(parts, fmt.Sprintf("%s %d (%.0f%%)",
				p.SAN(mv), e.Weight, 100*float64(e.Weight)/float64(total)))
		}
		t.Logf("%-22s %s", label, strings.Join(parts, "  "))
	}
	report("our 1st move:", nil)
	report("vs 1.e4:", []string{"e4"})
	report("vs 1.d4:", []string{"d4"})
	report("vs 1.e4 e5 2.Nf3:", []string{"e4", "e5", "Nf3"})
	report("vs 1.d4 Nf6 2.c4:", []string{"d4", "Nf6", "c4"})
}
