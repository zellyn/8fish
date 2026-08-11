package book

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zellyn/chess6502/internal/refchess"
)

// buildbig.go compiles the BIG BOOK: the curated book, unchanged, plus the
// vendored lichess ECO dataset imported as pure breadth until the 3,639-entry
// window is full. It is a SEPARATE, permissive builder on purpose — Build
// stays strict, because it guards the hand-curated openings.txt, where an
// overlapping breadth line is an authoring mistake that must fail the build.
// Here overlap is the normal case (3,810 dataset lines re-tread the curated
// repertoire constantly) and the right answer is "curated wins, skip, move
// on", not an error.

// NamesStageMax is the name table's DELIVERY budget: the stage-2 window
// between the staged table at delivery.NamesOrg ($1600) and the staged
// ProRWTS2 blob at delivery.RwtsOrg ($1D00) — seven pages, 1,792 bytes.
//
// This, not NamesMaxSize, is the budget that binds. Language Card bank 2
// could hold 4,096 B, but the table is STAGED through main RAM on its way
// there, and asm/m8.s computes SD2RWTS = SD2NAMES + ceil(BOOK_NAMES_SIZE/256)
// pages: a table over 7 pages moves the driver's staging base, changes
// m8.bin/m8sdboot.bin byte-for-byte and adds stage-2 sectors — a delivery
// change with a boot-timing cost, not a book change. The big book's name
// budget is therefore whatever fits between the curated table's 1,702 bytes
// and this line; BuildBig spends it on the most-used lichess family names and
// falls back to per-letter "ECO A".."ECO E" buckets for the rest.
const NamesStageMax = 0x1D00 - 0x1600

// BigStats is BuildBig's accounting, printed by cmd/genbook so nothing is
// dropped silently.
type BigStats struct {
	CuratedEntries int // Build(curated)'s entries, unchanged at the front
	ECOEntries     int // breadth entries imported from the dataset
	DroppedByCap   int // distinct new positions the entry capacity dropped
	FamilyNames    int // lichess family names admitted into the name table
	BucketNames    int // "ECO A".."ECO E" fallback names emitted
	NameBytes      int // the full name table's encoded size
}

// BuildBig compiles the big book: Build(curated) verbatim, then the eco lines
// (the vendored lichess dataset, see ECOLines) imported as BREADTH for BOTH
// colors, under these rules:
//
//   - CURATED WINS. The curated lines run through the exact same Build first;
//     they alone carry weight and definition of the repertoire, and their
//     entries are byte-identical to the resident book's (the big-book gate
//     holds Encode to that). An eco move at any position the curated book
//     already answers is SKIPPED, not an error — a second move there would
//     re-roll the repertoire, never add coverage (see Side and Build).
//     That covers curated MAIN positions and curated breadth positions alike:
//     both already have an answer, so an alternative buys nothing.
//   - BOTH COLORS. Every ply of every line is a candidate entry (the even
//     plies answer as White, the odd plies as Black), so the dataset teaches
//     the engine to ANSWER each line from either side of the board. Like all
//     breadth, the entries are weightless advocacy-wise: weight 1, never
//     summed, never biasing a curated choice (curated keys carry no eco
//     entries at all).
//   - ONE MOVE PER POSITION. The first candidate at a position claims it and
//     later ones are skipped: distinct positions covered (breadth) beat
//     alternative moves at few positions. "First" is deterministic: candidates
//     are taken by PLY ASCENDING — the coverage measurements put the holes at
//     plies 3-7, and a shallow answer is reachable every game while a deep one
//     needs the whole line replayed — then by dataset order (file a..e, row
//     order; the dataset lists canonical lines early, so the more canonical
//     move wins a conflict).
//   - THE CAP IS LOUD. Filling stops at BigMaxEntries; every distinct position
//     dropped past the cap is counted in stats.DroppedByCap and reported by
//     cmd/genbook, never silently truncated.
//
// NAMES. NameID is one byte and the shipped table must stay within
// NamesStageMax (see above), so eco entries get the most informative name the
// leftover budget affords: the lichess FAMILY (the text before the first ':'),
// admitted greedily by how many selected entries would carry it, with
// per-ECO-letter buckets ("ECO A".."ECO E") as the fallback for everything
// else. The curated names keep their IDs and bytes exactly.
func BuildBig(curated, eco []Line) ([]Entry, []string, BigStats, error) {
	var stats BigStats
	entries, names, err := Build(curated)
	if err != nil {
		return nil, nil, stats, fmt.Errorf("book: BuildBig: curated: %w", err)
	}
	stats.CuratedEntries = len(entries)

	// Every curated key — main or breadth — is already answered; eco never
	// adds a move there.
	answered := make(map[uint32]bool, len(entries))
	for _, e := range entries {
		answered[e.Key] = true
	}

	// Walk every eco line through refchess (the same legality oracle Build
	// uses; any illegal move is a loud failure) and record every ply as a
	// candidate. row is the line's index across the whole dataset — the
	// deterministic low half of the priority order.
	type cand struct {
		ply, row        int
		key             uint32
		from, to, flags byte
		family, letter  string
	}
	var cands []cand
	for row, ln := range eco {
		pos, e := refchess.ParseFEN(refchess.StartFEN)
		if e != nil {
			return nil, nil, stats, fmt.Errorf("book: BuildBig: start FEN: %w", e)
		}
		family := ln.Name
		if i := strings.IndexByte(family, ':'); i >= 0 {
			family = family[:i]
		}
		family = strings.TrimSpace(family)
		for pi, uci := range ln.Moves {
			key, e := HashFEN(pos.FEN())
			if e != nil {
				return nil, nil, stats, fmt.Errorf("book: BuildBig: %s %s ply %d: hash: %w", ln.ECO, ln.Name, pi+1, e)
			}
			from, to, flags, e := uciTo0x88(uci)
			if e != nil {
				return nil, nil, stats, fmt.Errorf("book: BuildBig: %s %s ply %d %q: %w", ln.ECO, ln.Name, pi+1, uci, e)
			}
			mv, e := refchess.ParseMove(uci)
			if e != nil {
				return nil, nil, stats, fmt.Errorf("book: BuildBig: %s %s ply %d %q: parse: %w", ln.ECO, ln.Name, pi+1, uci, e)
			}
			if e := pos.Make(mv); e != nil {
				return nil, nil, stats, fmt.Errorf("book: BuildBig: %s %s ply %d %q: ILLEGAL: %w", ln.ECO, ln.Name, pi+1, uci, e)
			}
			cands = append(cands, cand{ply: pi, row: row, key: key,
				from: from, to: to, flags: flags, family: family, letter: ln.ECO[:1]})
		}
	}

	// Priority: ply ascending, then dataset order. The slice is already in
	// (row, ply) order and each row contributes one candidate per ply, so a
	// stable sort on ply alone leaves rows ascending within a ply.
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].ply < cands[j].ply })

	// Select: first candidate per position wins, capacity capped and counted.
	room := BigMaxEntries - len(entries)
	var selected []cand
	for _, c := range cands {
		if answered[c.key] {
			continue
		}
		answered[c.key] = true
		if len(selected) >= room {
			stats.DroppedByCap++
			continue
		}
		selected = append(selected, c)
	}

	// The name table: curated names verbatim, then admitted families (ranked
	// by how many selected entries carry them; ties by name), then the
	// per-letter buckets actually needed. All map iteration is sorted before
	// it can affect output.
	famCount := map[string]int{}
	for _, c := range selected {
		famCount[c.family]++
	}
	families := make([]string, 0, len(famCount))
	for f := range famCount {
		families = append(families, f)
	}
	sort.Slice(families, func(i, j int) bool {
		if famCount[families[i]] != famCount[families[j]] {
			return famCount[families[i]] > famCount[families[j]]
		}
		return families[i] < families[j]
	})
	nameBytes := 0
	for _, n := range names {
		nameBytes += 1 + len(n)
	}
	// Reserve room for all five buckets up front: with 3,000+ selected
	// entries and a two-digit byte budget the buckets are certain to be
	// needed, and reserving first keeps the family admission independent of
	// its own outcome.
	const bucketPrefix = "ECO "
	letters := []string{"A", "B", "C", "D", "E"}
	reserved := len(letters) * (1 + len(bucketPrefix) + 1)
	famID := map[string]byte{}
	for _, f := range families {
		cost := 1 + len(f)
		if nameBytes+reserved+cost > NamesStageMax || len(names)+len(letters) >= 255 {
			break
		}
		famID[f] = byte(len(names))
		names = append(names, f)
		nameBytes += cost
		stats.FamilyNames++
	}
	bucketID := map[string]byte{}
	for _, l := range letters {
		needed := false
		for _, c := range selected {
			if c.letter == l && !hasFam(famID, c.family) {
				needed = true
				break
			}
		}
		if !needed {
			continue
		}
		bucketID[l] = byte(len(names))
		names = append(names, bucketPrefix+l)
		nameBytes += 1 + len(bucketPrefix) + 1
		stats.BucketNames++
	}
	if len(names) > 255 {
		return nil, nil, stats, fmt.Errorf("book: BuildBig: %d distinct names; NameIDs must fit a byte and the count its header byte (max 255)", len(names))
	}
	if nameBytes > NamesStageMax {
		return nil, nil, stats, fmt.Errorf("book: BuildBig: name table is %d bytes, %d over the %d-byte staging window (NamesStageMax)",
			nameBytes, nameBytes-NamesStageMax, NamesStageMax)
	}
	stats.NameBytes = nameBytes

	for _, c := range selected {
		id, ok := famID[c.family]
		if !ok {
			id, ok = bucketID[c.letter]
			if !ok {
				return nil, nil, stats, fmt.Errorf("book: BuildBig: no name for family %q / letter %q", c.family, c.letter)
			}
		}
		entries = append(entries, Entry{Key: c.key, From: c.from, To: c.to,
			Flags: c.flags, Weight: 1, NameID: id})
	}
	stats.ECOEntries = len(entries) - stats.CuratedEntries
	if len(entries) > BigMaxEntries {
		return nil, nil, stats, fmt.Errorf("book: BuildBig: %d entries over the %d capacity", len(entries), BigMaxEntries)
	}
	return entries, names, stats, nil
}

// hasFam reports whether family f was admitted (map lookup that distinguishes
// "admitted with ID 0" from "absent" — ID 0 is a curated name, so an admitted
// family can never actually have it, but keep the check honest).
func hasFam(famID map[string]byte, f string) bool {
	_, ok := famID[f]
	return ok
}
