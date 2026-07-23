package book

import (
	"fmt"

	"github.com/zellyn/chess6502/internal/refchess"
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

// Build compiles the curated lines into book entries and a name table.
// It validates EVERY move legal move-by-move through refchess (the
// legality oracle) and returns a loud error on the first illegal move —
// the generator must refuse to emit an illegal line. Each position hash
// is keyed to the move played from it; a (key, move) pair appearing in
// several lines is merged, its weight summed (the more a move recurs
// across sound lines, the more it is played). Distinct moves from the
// same position stay as separate entries sharing the key — the source of
// opening variety.
func Build(lines []Line) (entries []Entry, names []string, err error) {
	names = make([]string, len(lines))
	// merge key: (key,from,to,flags) -> index into entries
	index := map[[7]byte]int{}
	mkKey := func(e Entry) [7]byte {
		return [7]byte{
			byte(e.Key), byte(e.Key >> 8), byte(e.Key >> 16), byte(e.Key >> 24),
			e.From, e.To, e.Flags,
		}
	}

	for li, ln := range lines {
		if int(byte(li)) != li {
			return nil, nil, fmt.Errorf("too many lines: name IDs must fit in a byte (%d)", li)
		}
		names[li] = ln.ECO + " " + ln.Name
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
			ent := Entry{Key: key, From: from, To: to, Flags: flags, Weight: 1, NameID: byte(li)}
			mk := mkKey(ent)
			if idx, ok := index[mk]; ok {
				if int(entries[idx].Weight) < 255 {
					entries[idx].Weight++
				}
				continue
			}
			index[mk] = len(entries)
			entries = append(entries, ent)
		}
	}
	return entries, names, nil
}
