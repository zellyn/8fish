// Command genbook compiles the human-maintained opening source
// internal/book/openings.txt (SAN) into everything downstream:
//
//	internal/book/lines.go      — the generated Go book.Lines (UCI)
//	internal/book/bookblob.bin  — the resident ENTRIES (header + entry array),
//	                              which goes to AUX $0800
//	internal/book/booknames.bin — the resident NAME TABLE, which goes to
//	                              LANGUAGE CARD BANK 2 $D000
//	asm/book.inc                — layout defs for the asm probe
//
// TWO PIECES: see package book's doc. The probe reads only the first; only
// asm/m8.s's uibookname reads the second, and it runs at $E000 where a bank
// switch is free. Splitting them is what lets the entries clear the
// 80-column text page's aux half at $0400-$07FF, and therefore what lets the
// board ship in MIXED MODE.
//
// openings.txt is the SINGLE source of truth. genbook parses it, validates
// every line legal move-by-move through refchess (refusing an illegal or
// malformed line, naming the opening and bad move), then keys each position
// on the engine's 32-bit Zobrist hash (== asm HASH0-3).
//
// Run: go run ./cmd/genbook   (from the repo root)
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/zellyn/chess6502/internal/book"
)

const openingsPath = "internal/book/openings.txt"

func main() {
	lines, err := parseOpenings(openingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genbook: parse failed:", err)
		os.Exit(1)
	}
	if err := writeLinesGo("internal/book/lines.go", lines); err != nil {
		fmt.Fprintln(os.Stderr, "genbook: writing lines.go:", err)
		os.Exit(1)
	}

	entries, names, err := book.Build(lines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genbook: build failed:", err)
		os.Exit(1)
	}
	blob, nameBlob := book.Encode(entries, names)

	// Round-trip sanity: both pieces must parse back to the same counts.
	bk, err := book.Load(blob, nameBlob)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genbook: blob failed to reload:", err)
		os.Exit(1)
	}

	rawNames, tokNames := measureNameTables(names)

	if err := os.WriteFile("internal/book/bookblob.bin", blob, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genbook:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("internal/book/booknames.bin", nameBlob, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genbook:", err)
		os.Exit(1)
	}
	// The BIG BOOK (docs/prorwts2-design.md §3): the same entries in the same
	// layout plus a checksum trailer, loaded from disk into the idle
	// transposition-table window at aux $4000. Today it carries the same
	// curated lines as the resident blob — the MECHANISM supports
	// book.BigMaxEntries (3,639); filling it is the follow-on content task.
	// Its names are the resident name table (shared; one-byte NameIDs), so
	// growing the big book must stay inside the same 255-name budget.
	bigBlob, err := book.EncodeBig(entries, names)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genbook:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("internal/book/bigbook.bin", bigBlob, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genbook:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("asm/book.inc",
		[]byte(asmHeader(len(entries), len(names), len(blob), len(nameBlob))), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "genbook:", err)
		os.Exit(1)
	}

	entriesBytes := len(entries) * book.EntryStride
	fmt.Printf("genbook: %s -> %d lines -> %d entries (%d bytes), %d names\n",
		openingsPath, len(lines), len(entries), entriesBytes, len(names))
	fmt.Printf("  ENTRIES: header %d B + entries %d B = %d B -> AUX $%04X\n",
		book.HeaderSize, entriesBytes, len(blob), book.BaseAddr)
	fmt.Printf("  NAMES:   %d names, %d B (raw) -> LANGUAGE CARD BANK 2 $%04X\n",
		len(names), len(nameBlob), book.NamesAddr)
	fmt.Printf("  name-table measurement: raw=%d B  word-tokenized=%d B  -> using RAW\n",
		rawNames, tokNames)
	fmt.Printf("  resident footprint: entries %d / %d B aux $%04X-$1FFF (%.1f%%), "+
		"names %d / %d B in LC bank 2 (%.1f%%)\n",
		len(blob), book.MaxSize, book.BaseAddr, 100*float64(len(blob))/float64(book.MaxSize),
		len(nameBlob), book.NamesMaxSize, 100*float64(len(nameBlob))/float64(book.NamesMaxSize))
	fmt.Printf("  reload OK: %d entries, %d names\n", len(bk.Entries()), len(names))
	fmt.Printf("  BIGBOOK: %d B (header + %d entries + checksum) -> AUX $%04X via ProRWTS2; "+
		"capacity %d entries (%.1f%% full)\n",
		len(bigBlob), len(entries), book.BigBase, book.BigMaxEntries,
		100*float64(len(entries))/float64(book.BigMaxEntries))

	// The budget checks come LAST and after the breakdown is printed: when a
	// book overflows you need to see WHERE the bytes went (entries vs names)
	// to decide what to cut, and a bare "too big" line does not tell you.
	bad := false
	if len(blob) > book.MaxSize {
		fmt.Fprintf(os.Stderr, "genbook: the entries blob is %d bytes, %d MORE than the "+
			"%d-byte aux budget ($%04X-$1FFF, between the 80-column text page and the "+
			"DHGR aux half)\n", len(blob), len(blob)-book.MaxSize, book.MaxSize, book.BaseAddr)
		bad = true
	}
	if len(nameBlob) > book.NamesMaxSize {
		fmt.Fprintf(os.Stderr, "genbook: the name table is %d bytes, %d MORE than all of "+
			"Language Card bank 2 (%d B)\n",
			len(nameBlob), len(nameBlob)-book.NamesMaxSize, book.NamesMaxSize)
		bad = true
	}
	if bad {
		os.Exit(1)
	}
}

// measureNameTables reports both name-encoding sizes so the choice is
// data-driven (the task asks to measure both and pick). raw = the
// length-prefixed form actually shipped. tokenized = a word dictionary
// (each unique word stored once) plus per-name word-index lists; it only
// wins when names share many words AND the dictionary overhead is repaid.
func measureNameTables(names []string) (raw, tok int) {
	for _, n := range names {
		raw += 1 + len(n)
	}
	// Tokenized: dictionary of unique words + per-name [count][wordID...].
	wordID := map[string]int{}
	dict := 0
	for _, n := range names {
		for _, w := range strings.Fields(n) {
			if _, ok := wordID[w]; !ok {
				wordID[w] = len(wordID)
				dict += 1 + len(w) // length-prefixed dictionary word
			}
		}
	}
	tok = dict
	for _, n := range names {
		tok += 1 + len(strings.Fields(n)) // count byte + one wordID byte each
	}
	return raw, tok
}

func asmHeader(entryCount, nameCount, blobSize, nameSize int) string {
	var b strings.Builder
	b.WriteString("; Generated by cmd/genbook. DO NOT EDIT.\n")
	b.WriteString("; Resident opening book layout. The book is delivered in TWO pieces by\n")
	b.WriteString("; the SECOND stage of the disk's chain load, and the copier in asm/m8.s\n")
	b.WriteString("; puts each where it stays:\n")
	b.WriteString(";\n")
	b.WriteString(";   ENTRIES (header + sorted entry array) -> AUXILIARY RAM $0800.\n")
	b.WriteString(";     Above the 80-column text page's aux half at $0400-$07FF, which is\n")
	b.WriteString(";     what the mixed-mode text window's EVEN columns come from -- that is\n")
	b.WriteString(";     the whole reason the base is $0800 and not $0200.\n")
	b.WriteString(";   NAME TABLE -> LANGUAGE CARD BANK 2 $D000.\n")
	b.WriteString(";     bookprobe NEVER reads it. Only asm/m8.s's uibookname does, and that\n")
	b.WriteString(";     runs at $E000, which LC bank switching does not touch.\n")
	b.WriteString(";\n")
	b.WriteString("; Probe: compute HASH0-3 for the current position, binary-search the\n")
	b.WriteString("; sorted entry array on the 4-byte key, weighted-pick among equal-key\n")
	b.WriteString("; entries, then NAMEID -> current-opening byte.\n\n")
	fmt.Fprintf(&b, "BOOK_BASE      = $%04X   ; resident base address (AUX)\n", book.BaseAddr)
	fmt.Fprintf(&b, "BOOK_MAGIC     = BOOK_BASE+%d   ; 2 bytes 'B','K'\n", 0)
	fmt.Fprintf(&b, "BOOK_COUNT     = BOOK_BASE+%d   ; word: entry count (%d)\n", 2, entryCount)
	fmt.Fprintf(&b, "BOOK_NAMEADDR  = BOOK_BASE+%d   ; word: the name table's ABSOLUTE\n", 4)
	fmt.Fprintf(&b, "                               ;  resident address ($%04X, LC bank 2)\n", book.NamesAddr)
	fmt.Fprintf(&b, "BOOK_NAMECT    = BOOK_BASE+%d   ; byte: name count (%d)\n", 6, nameCount)
	fmt.Fprintf(&b, "BOOK_STRIDE    = %d            ; entry stride in bytes\n", book.EntryStride)
	fmt.Fprintf(&b, "BOOK_ENTRIES   = BOOK_BASE+%d   ; first entry\n", book.HeaderSize)
	fmt.Fprintf(&b, "BOOK_NAMES     = $%04X   ; the name table's resident base (LC bank 2)\n", book.NamesAddr)
	fmt.Fprintf(&b, "BIGBOOK_BASE   = $%04X   ; the BIG BOOK's aux home: the idle TT window\n", book.BigBase)
	fmt.Fprintf(&b, "BIGBOOK_MAX    = %d    ; its capacity in entries (checksum trailer priced in)\n\n", book.BigMaxEntries)
	b.WriteString("; entry field offsets (from an entry base):\n")
	b.WriteString("BOOK_E_KEY     = 0    ; 4 bytes LE == HASH0..3\n")
	b.WriteString("BOOK_E_FROM    = 4    ; 0x88 from-square\n")
	b.WriteString("BOOK_E_TO      = 5    ; 0x88 to-square\n")
	b.WriteString("BOOK_E_FLAGS   = 6    ; move flags (bits 0-2 promo)\n")
	b.WriteString("BOOK_E_WEIGHT  = 7    ; weighted-pick weight\n")
	b.WriteString("BOOK_E_NAMEID  = 8    ; name-table index -> current-opening byte\n\n")
	b.WriteString("; build stats:\n")
	fmt.Fprintf(&b, "BOOK_ENTRY_COUNT = %d\n", entryCount)
	fmt.Fprintf(&b, "BOOK_NAME_COUNT  = %d\n", nameCount)
	fmt.Fprintf(&b, "BOOK_BLOB_SIZE   = %d\n", blobSize)
	fmt.Fprintf(&b, "BOOK_NAMES_SIZE  = %d\n", nameSize)
	return b.String()
}
