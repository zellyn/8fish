package book

import (
	"fmt"

	"github.com/zellyn/8fish/internal/refchess"
)

// uciTo0x88 converts a UCI move ("e2e4", "e7e8q") to the engine's 0x88
// from/to squares and move flags (bits 0-2 = promotion, per "..nbrq").
func uciTo0x88(uci string) (from, to, flags byte, err error) {
	if len(uci) != 4 && len(uci) != 5 {
		return 0, 0, 0, fmt.Errorf("bad UCI move %q", uci)
	}
	sq := func(f, r byte) (byte, error) {
		if f < 'a' || f > 'h' || r < '1' || r > '8' {
			return 0, fmt.Errorf("bad square in %q", uci)
		}
		return (r-'1')<<4 | (f - 'a'), nil // 0x88: rank*16 + file
	}
	if from, err = sq(uci[0], uci[1]); err != nil {
		return
	}
	if to, err = sq(uci[2], uci[3]); err != nil {
		return
	}
	if len(uci) == 5 {
		switch uci[4] {
		case 'n':
			flags = 2
		case 'b':
			flags = 3
		case 'r':
			flags = 4
		case 'q':
			flags = 5
		default:
			return 0, 0, 0, fmt.Errorf("bad promotion in %q", uci)
		}
	}
	return
}

// Side selects whose moves in a curated line become book entries.
//
// The book is a set of positions, so a line written out in full teaches the
// engine BOTH to play the moves and to answer them. That is what you want for
// a main line ("1.e4 e5 2.Nf3 Nc6"): the engine is happy on either side of a
// Ruy Lopez. It is emphatically NOT what you want for a breadth line whose
// only purpose is to have a sane reply to a bad move — writing "1.g4 d5" as a
// SideBoth line would hand the engine 1.g4 as a first move it might roll.
//
// SideBlack keeps only Black's moves, SideWhite only White's. In openings.txt
// these are written as a "/b" or "/w" suffix on the ECO code.
//
// A marked line is also WEIGHTLESS: it creates entries the book was missing,
// but it never increments the weight of an entry an earlier line already
// established. This is not a nicety — it was measured. Weight is just "how
// many curated lines played this move here", so a short line added only to
// have an ANSWER ready for 1...h5 still bumped the weight of its own first
// move, `1.e4`. Thirty such lines moved the engine's own opening repertoire
// from 65% 1.e4 to 44% without anyone choosing that, and the 2,000-game
// self-play cost was real (docs/results.md 2026-07-28). Breadth lines answer;
// they do not advocate. Only an unmarked main line expresses a preference.
type Side byte

const (
	SideBoth  Side = 0
	SideWhite Side = 1
	SideBlack Side = 2
)

// String renders the Go constant name, so cmd/genbook can emit Side values
// straight into the generated lines.go.
func (s Side) String() string {
	switch s {
	case SideWhite:
		return "SideWhite"
	case SideBlack:
		return "SideBlack"
	default:
		return "SideBoth"
	}
}

// wants reports whether a move played at zero-based ply pi (even = White to
// move) belongs to this line's perspective.
func (s Side) wants(pi int) bool {
	switch s {
	case SideWhite:
		return pi%2 == 0
	case SideBlack:
		return pi%2 == 1
	default:
		return true
	}
}

// Build compiles the curated lines into book entries and a name table.
// It validates EVERY move legal move-by-move through refchess (the
// legality oracle) and returns a loud error on the first illegal move —
// the generator must refuse to emit an illegal line. Each position hash
// is keyed to the move played from it; a (key, move) pair appearing in
// several MAIN lines is merged, its weight summed (the more a move recurs
// across sound lines, the more it is played). Distinct moves from the
// same position stay as separate entries sharing the key — the source of
// opening variety.
//
// Perspective-marked (breadth) lines are handled in a second pass and obey
// two extra rules, both of which were bought with measured Elo: they never
// add weight to an existing entry, and they are REFUSED if they would offer
// an alternative move at a position the main lines already answer. See Side.
func Build(lines []Line) (entries []Entry, names []string, err error) {
	// The name table is DEDUPLICATED: two lines whose "ECO Name" text is
	// identical share one NameID and one stored string. That is what makes
	// breadth affordable — a dozen four-ply lines answering a dozen junk
	// first moves are all honestly called "A00 Irregular Opening", and the
	// name costs ~22 bytes once instead of a dozen times. The asm walks the
	// table positionally by NameID (uibookname in asm/m8.s), so sharing an
	// ID is invisible to it.
	nameID := map[string]byte{}
	// merge key: (key,from,to,flags) -> index into entries
	index := map[[7]byte]int{}
	mkKey := func(e Entry) [7]byte {
		return [7]byte{
			byte(e.Key), byte(e.Key >> 8), byte(e.Key >> 16), byte(e.Key >> 24),
			e.From, e.To, e.Flags,
		}
	}

	// TWO PASSES, and the order is load-bearing. Pass 0 takes the unmarked
	// MAIN lines, which alone define the engine's repertoire and its weights.
	// Pass 1 takes the perspective-marked BREADTH lines, which may only fill
	// positions the main lines left empty (see the refusal below).
	mainKeys := map[uint32]bool{}
	for pass := range 2 {
		for _, ln := range lines {
			if (ln.Side == SideBoth) != (pass == 0) {
				continue
			}
			full := ln.ECO + " " + ln.Name
			id, ok := nameID[full]
			if !ok {
				if len(names) > 255 {
					return nil, nil, fmt.Errorf("too many distinct opening names: NameIDs must fit in a byte (%d)", len(names))
				}
				id = byte(len(names))
				nameID[full] = id
				names = append(names, full)
			}
			pos, e := refchess.ParseFEN(refchess.StartFEN)
			if e != nil {
				return nil, nil, fmt.Errorf("start FEN: %w", e)
			}
			for pi, uci := range ln.Moves {
				// Hash of the position BEFORE this move is the entry key.
				key, e := HashFEN(pos.FEN())
				if e != nil {
					return nil, nil, fmt.Errorf("%s %s ply %d: hash: %w", ln.ECO, ln.Name, pi+1, e)
				}
				from, to, flags, e := uciTo0x88(uci)
				if e != nil {
					return nil, nil, fmt.Errorf("%s %s ply %d %q: %w", ln.ECO, ln.Name, pi+1, uci, e)
				}
				// Legality oracle: parse and make through refchess. Any illegal
				// move fails the whole build loudly.
				mv, e := refchess.ParseMove(uci)
				if e != nil {
					return nil, nil, fmt.Errorf("%s %s ply %d %q: parse: %w", ln.ECO, ln.Name, pi+1, uci, e)
				}
				if e := pos.Make(mv); e != nil {
					return nil, nil, fmt.Errorf("%s %s ply %d %q: ILLEGAL: %w", ln.ECO, ln.Name, pi+1, uci, e)
				}
				// Every move is still parsed, validated and MADE — the line has to
				// be walked to reach the later positions — but only the moves this
				// line speaks for become entries. See Side.
				if !ln.Side.wants(pi) {
					continue
				}
				ent := Entry{Key: key, From: from, To: to, Flags: flags, Weight: 1, NameID: id}
				mk := mkKey(ent)
				if idx, ok := index[mk]; ok {
					// Weightless: a perspective-marked (breadth) line adds
					// coverage, never preference. See Side.
					if ln.Side == SideBoth && int(entries[idx].Weight) < 255 {
						entries[idx].Weight++
					}
					continue
				}
				if ln.Side == SideBoth {
					mainKeys[key] = true
				} else if mainKeys[key] {
					// REFUSED, and this refusal is the whole point of the split.
					// A breadth line offering a SECOND move from a position a main
					// line already answers does not add coverage — it silently
					// re-rolls the engine's repertoire, which cost measurable Elo
					// once already (docs/results.md 2026-07-28). If the move really
					// is one we want to play, promote it to a main line by dropping
					// the /w or /b suffix, deliberately and visibly.
					return nil, nil, fmt.Errorf(
						"%s %s ply %d %q: a breadth line (/w or /b) may not add an "+
							"alternative move to a position the main lines already "+
							"cover — that changes the repertoire, not the coverage. "+
							"Drop the suffix to adopt it as a main line, or drop the ply",
						ln.ECO, ln.Name, pi+1, uci)
				}
				index[mk] = len(entries)
				entries = append(entries, ent)
			}
		}
	}
	return entries, names, nil
}
