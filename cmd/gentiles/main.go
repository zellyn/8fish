// Command gentiles slices the owner's hand-drawn DazzleDraw chessboard
// (assets/chess-dazzledraw-save.bin, A2FC: 8 KB aux then 8 KB main) into
// the resident per-square DHGR piece tiles:
//
//	internal/tiles/tileblob.bin  — the 24-tile blob (embedded by package tiles)
//	asm/tiledefs.inc             — the layout CONSTANTS, alone
//	asm/tiles.inc                — the TILEIDX/TILEOFF dispatch tables
//
// The constants are split out because asm/m8.s needs TILE_BLOB_SIZE in its
// $0D00 copier — to size stage 2's page table — but the dispatch tables belong
// in the Language Card payload, and ca65 emits into whichever segment is
// current at the point of the .include. One file could not be both.
//
// The artwork is the SINGLE source of truth: gentiles re-derives nothing
// by hand. Every geometric assumption the 76-byte tile format rests on
// (see package tiles' doc comment) is asserted against the actual pixels
// and the run FAILS if one is broken, so re-drawing the board can never
// silently produce a blob that renders garbage.
//
// Run: go run ./cmd/gentiles   (from the repo root, or via `make tiles`)
//
// # Checking mode
//
// While REDRAWING the board you want the whole list of problems, not the
// first assertion. `-check` writes nothing and prints every finding:
//
//	go run ./cmd/gentiles -check -art work-in-progress.bin -png /tmp/tiles.png
//
// Exit status:
//
//	0  the artwork is clean: it slices and loses nothing — the goal state
//	1  the artwork slices, but the top trim CLIPS ink (dots are lost)
//	2  a geometric assumption the tile format rests on is broken (most such
//	   breaks stop the slice outright; a few, like a pawn drawn differently
//	   on different files, would ship a blob that no longer matches the art)
//
// See assets/README.md for the drawing spec the checker enforces.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zellyn/chess6502/internal/tiles"
)

const (
	defaultArtPath = "assets/chess-dazzledraw-save.bin"
	blobPath       = "internal/tiles/tileblob.bin"
	defsPath       = "asm/tiledefs.inc"
	incPath        = "asm/tiles.inc"
)

// Exit statuses. Distinguishing clipped ink from a structural break is the
// point of the tool: one means "the drawing loses pixels to the trim", the
// other means "the drawing breaks an assumption the tile format rests on".
const (
	exitOK      = 0
	exitClipped = 1
	exitBroken  = 2
)

// maxPerKind caps how many findings of one kind a report prints. A single
// stray pixel can produce hundreds of related lines; the count is always
// reported in full even when the list is truncated.
const maxPerKind = 20

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is main's body with the process boundary factored out, so tests can
// drive the tool end to end — including its exit status — without a
// subprocess. It returns the process exit status.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gentiles", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		artPath  = fs.String("art", defaultArtPath, "artwork to read (A2FC DazzleDraw save); point this at a work-in-progress redraw")
		check    = fs.Bool("check", false, "check the artwork and report EVERY problem; write no outputs")
		pngPath  = fs.String("png", "", "write a PNG of the tiles AFTER slicing (post-trim) to this path")
		showDots = fs.Bool("dots", false, "in -check, list every clipped dot's coordinates instead of summarising per row")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: gentiles [-check] [-art PATH] [-png PATH]\n\n"+
			"Slices the hand-drawn DazzleDraw board into the resident DHGR tile blob.\n"+
			"With -check, writes nothing and reports every broken assumption at once.\n"+
			"Exit: 0 clean, %d ink clipped by the top trim, %d broken assumption.\n\n",
			exitClipped, exitBroken)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK // -h is a successful request for the usage text
		}
		return exitBroken
	}

	art, err := os.ReadFile(*artPath)
	if err != nil {
		fmt.Fprintln(stderr, "gentiles:", err)
		return exitBroken
	}

	if *check {
		return runCheck(art, *artPath, *pngPath, *showDots, stdout, stderr)
	}
	return runBuild(art, *artPath, *pngPath, stdout, stderr)
}

// runBuild is the generator: slice the artwork and write the three
// committed outputs. It keeps Build's fail-on-first-problem behaviour, and
// points at -check when it trips.
func runBuild(art []byte, artPath, pngPath string, stdout, stderr io.Writer) int {
	blob, err := tiles.Build(art)
	if err != nil {
		fmt.Fprintln(stderr, "gentiles:", err)
		fmt.Fprintf(stderr, "gentiles: run `go run ./cmd/gentiles -check -art %s` for the full list of problems\n", artPath)
		return exitBroken
	}
	if len(blob) != tiles.BlobSize {
		fmt.Fprintf(stderr, "gentiles: blob is %d bytes, want %d\n", len(blob), tiles.BlobSize)
		return exitBroken
	}
	for _, w := range []struct {
		path string
		data []byte
	}{
		{blobPath, blob},
		{defsPath, []byte(asmDefs())},
		{incPath, []byte(asmInc())},
	} {
		if err := os.WriteFile(w.path, w.data, 0o644); err != nil {
			fmt.Fprintln(stderr, "gentiles:", err)
			return exitBroken
		}
	}

	fmt.Fprintf(stdout, "gentiles: %s -> %d tiles of %dx%d dots\n", artPath, tiles.NumTiles, tiles.TileW, tiles.TileH)
	fmt.Fprintf(stdout, "  %d rows x %d stored bytes = %d B/tile; blob = %d B -> %s\n",
		tiles.TileH, tiles.TileBytes, tiles.TileStride, len(blob), blobPath)
	fmt.Fprintf(stdout, "  dropped per tile row: byte columns 0 (aux0) and %d (main2), asserted pure background\n",
		tiles.TileCols-1)
	fmt.Fprintf(stdout, "  empty squares synthesised from aux=$%02X main=$%02X (light) / $00 (dark)\n",
		tiles.BGAux, tiles.BGMain)
	fmt.Fprintf(stdout, "  wrote %s (constants) and %s (TILEIDX mirrors PIECECH: dark<<4 | piece&$0F)\n",
		defsPath, incPath)
	if pngPath != "" {
		if err := writeTilePNG(pngPath, blob); err != nil {
			fmt.Fprintln(stderr, "gentiles:", err)
			return exitBroken
		}
		fmt.Fprintf(stdout, "  wrote %s (the tiles as they will render, post-trim)\n", pngPath)
	}
	return exitOK
}

// runCheck reports everything and writes nothing but the optional PNG.
func runCheck(art []byte, artPath, pngPath string, showDots bool, stdout, stderr io.Writer) int {
	rep, err := tiles.Check(art)
	if err != nil {
		fmt.Fprintln(stderr, "gentiles:", err)
		return exitBroken
	}
	printReport(stdout, rep, artPath, showDots)

	if pngPath != "" {
		if rep.Blob == nil {
			fmt.Fprintf(stdout, "\n  (no PNG: the artwork does not slice, so there are no tiles to render)\n")
		} else if err := writeTilePNG(pngPath, rep.Blob); err != nil {
			fmt.Fprintln(stderr, "gentiles:", err)
			return exitBroken
		} else {
			fmt.Fprintf(stdout, "\nPNG: %s — the start position drawn FROM THE TILES, so what you see is\n"+
				"     post-trim: any clipped ink is already gone.\n", pngPath)
		}
	}

	switch {
	case len(rep.Violations) > 0:
		return exitBroken
	case rep.ClippedTotal > 0:
		return exitClipped
	}
	return exitOK
}

// writeTilePNG renders the start position out of the blob, which is the
// only honest picture of a tile: it shows exactly the dots that survived
// slicing.
func writeTilePNG(path string, blob []byte) error {
	board := tiles.StartPosition()
	const originCol, originY = 1, 8 // inset a little so the PNG is readable
	return tiles.BlobPNG(path, blob, &board, originCol, originY)
}

// ---------------------------------------------------------------------
// Report formatting.
// ---------------------------------------------------------------------

func printReport(w io.Writer, rep *tiles.Report, artPath string, showDots bool) {
	fmt.Fprintf(w, "gentiles -check: %s\n", artPath)
	fmt.Fprintf(w, "  grid: 8x8 squares of %dx%d dots at origin (%d,%d); tile = %dx%d dots after dropping the top %d row(s)\n\n",
		tiles.SrcSquareW, tiles.SrcSquareH, tiles.SrcOriginX, tiles.SrcOriginY,
		tiles.TileW, tiles.TileH, tiles.SrcTrimTop)

	printClipped(w, rep, showDots)
	printViolations(w, rep)
	printExtents(w, rep)
	printSummary(w, rep)
}

// printClipped is the headline: ink the top trim throws away.
func printClipped(w io.Writer, rep *tiles.Report, showDots bool) {
	fmt.Fprintf(w, "CLIPPED INK (source rows dy 0..%d, dropped by the tile)\n", tiles.SrcTrimTop-1)
	if rep.ClippedTotal == 0 {
		fmt.Fprintf(w, "  NONE. Every drawn dot survives the trim. This is the goal state.\n\n")
		return
	}
	fmt.Fprintf(w, "  %d pixels of piece ink are LOST, across %d squares:\n\n", rep.ClippedTotal, len(rep.Clipped))
	for _, sq := range rep.Clipped {
		name := tiles.PieceAt(sq.R, sq.F)
		if name == "" {
			name = "no piece transcribed here"
		}
		fmt.Fprintf(w, "  %-3s %-34s %3d px\n", tiles.Square(sq.R, sq.F), name, sq.Count())
		for _, line := range dotLines(sq.Dots, showDots) {
			fmt.Fprintf(w, "        %s\n", line)
		}
	}
	fmt.Fprintf(w, "\n  Fix by moving that ink down: nothing may be drawn above dy %d.\n\n", tiles.SrcTrimTop)
}

// dotLines renders a square's clipped dots one line per source row: the
// row, then either the runs of dx or every dx.
func dotLines(dots []tiles.Dot, showDots bool) []string {
	byRow := map[int][]int{}
	var rows []int
	for _, d := range dots {
		if _, ok := byRow[d.DY]; !ok {
			rows = append(rows, d.DY)
		}
		byRow[d.DY] = append(byRow[d.DY], d.DX)
	}
	var out []string
	for _, dy := range rows {
		xs := byRow[dy]
		if showDots {
			out = append(out, fmt.Sprintf("dy=%d: dx %s", dy, joinInts(xs)))
			continue
		}
		out = append(out, fmt.Sprintf("dy=%d: %2d px at dx %s", dy, len(xs), runs(xs)))
	}
	return out
}

// runs collapses a sorted dx list into "8-11,14,20-23".
func runs(xs []int) string {
	var parts []string
	for i := 0; i < len(xs); {
		j := i
		for j+1 < len(xs) && xs[j+1] == xs[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, fmt.Sprintf("%d", xs[i]))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", xs[i], xs[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

func joinInts(xs []int) string {
	var parts []string
	for _, x := range xs {
		parts = append(parts, fmt.Sprintf("%d", x))
	}
	return strings.Join(parts, ",")
}

// printViolations lists every OTHER broken assumption, grouped by kind so
// one stray pixel does not bury the rest.
func printViolations(w io.Writer, rep *tiles.Report) {
	fmt.Fprintf(w, "GEOMETRY CHECKS\n")
	if len(rep.Violations) == 0 {
		fmt.Fprintf(w, "  all %d assumptions hold: %s.\n\n",
			len(tiles.CheckKinds), strings.Join(tiles.CheckKinds, ", "))
		return
	}
	counts := map[string]int{}
	for _, v := range rep.Violations {
		counts[v.Kind]++
	}
	fmt.Fprintf(w, "  %d findings:\n", len(rep.Violations))
	shown := map[string]int{}
	for _, v := range rep.Violations {
		shown[v.Kind]++
		switch {
		case shown[v.Kind] <= maxPerKind:
			fmt.Fprintf(w, "    %s\n", v)
		case shown[v.Kind] == maxPerKind+1:
			fmt.Fprintf(w, "    [%s] ... and %d more of this kind\n", v.Kind, counts[v.Kind]-maxPerKind)
		}
	}
	fmt.Fprintln(w)
}

// printExtents is the redraw's working table: where each piece's ink
// actually sits, and how much slack it has against the trim.
func printExtents(w io.Writer, rep *tiles.Report) {
	fmt.Fprintf(w, "INK EXTENTS (per transcribed square; the drawing spec is dy %d..%d, dx %d..%d)\n",
		tiles.SrcTrimTop, tiles.SrcSquareH-1, tiles.ContentMinDX, tiles.ContentMaxDX)
	fmt.Fprintf(w, "  %-3s %-34s %-10s %-10s %6s  %s\n", "sq", "piece", "dy", "dx", "px", "top slack")
	for _, e := range rep.Extents {
		if e.Empty {
			fmt.Fprintf(w, "  %-3s %-34s %-10s %-10s %6d  %s\n",
				tiles.Square(e.R, e.F), tiles.PieceAt(e.R, e.F), "-", "-", 0, "-")
			continue
		}
		slack := fmt.Sprintf("%+d", e.MinDY-tiles.SrcTrimTop)
		if e.MinDY < tiles.SrcTrimTop {
			slack += " CLIPPED"
		}
		fmt.Fprintf(w, "  %-3s %-34s %-10s %-10s %6d  %s\n",
			tiles.Square(e.R, e.F), tiles.PieceAt(e.R, e.F),
			fmt.Sprintf("%d..%d", e.MinDY, e.MaxDY), fmt.Sprintf("%d..%d", e.MinDX, e.MaxDX),
			e.Count, slack)
	}
	fmt.Fprintln(w)
}

func printSummary(w io.Writer, rep *tiles.Report) {
	fmt.Fprintf(w, "SUMMARY\n")
	fmt.Fprintf(w, "  trim cost curve (board-wide ink lost): top 1 row = %d, top 2 = %d, top 3 = %d; bottom 1 row = %d\n",
		rep.TrimCosts[1], rep.TrimCosts[2], rep.TrimCosts[3], rep.BottomCost)
	if rep.Blob == nil {
		fmt.Fprintf(w, "  blob: WOULD NOT SLICE — fix the geometry findings above\n")
	} else if len(rep.Changed) == 0 {
		fmt.Fprintf(w, "  blob: %d bytes (%d tiles x %d B), byte-identical to the committed %s\n",
			len(rep.Blob), tiles.NumTiles, tiles.TileStride, blobPath)
	} else {
		fmt.Fprintf(w, "  blob: %d bytes (%d tiles x %d B); %d tile(s) DIFFER from the committed %s:\n",
			len(rep.Blob), tiles.NumTiles, tiles.TileStride, len(rep.Changed), blobPath)
		for _, name := range rep.Changed {
			fmt.Fprintf(w, "      tile %s\n", name)
		}
		fmt.Fprintf(w, "    run `go run ./cmd/gentiles` to regenerate the blob and the .inc files\n")
	}
	switch {
	case rep.Clean():
		fmt.Fprintf(w, "  RESULT: clean — the artwork slices and loses nothing (exit %d)\n", exitOK)
	case len(rep.Violations) > 0:
		fmt.Fprintf(w, "  RESULT: BROKEN — %d geometry finding(s) (exit %d)\n",
			len(rep.Violations), exitBroken)
	default:
		fmt.Fprintf(w, "  RESULT: %d pixels clipped by the top trim; the artwork slices but loses ink (exit %d)\n",
			rep.ClippedTotal, exitClipped)
	}
}

// ---------------------------------------------------------------------
// Generated asm.
// ---------------------------------------------------------------------

// asmDefs emits the generated layout constants, and nothing else, so that
// they can be included from any segment.
func asmDefs() string {
	var b strings.Builder
	b.WriteString("; Generated by cmd/gentiles. DO NOT EDIT.\n")
	b.WriteString("; DHGR board-tile layout. The blob (internal/tiles/tileblob.bin) holds\n")
	b.WriteString("; 24 piece tiles; the two EMPTY-square tiles are synthesised from the\n")
	b.WriteString("; background constants below, so no bytes are spent on them.\n")
	b.WriteString(";\n")
	b.WriteString("; A rendered square is TILE_PITCH dots = TILE_SQCOLS byte columns per\n")
	b.WriteString("; plane. TILE_PITCH is a multiple of 14, so every file lands on a byte\n")
	b.WriteString("; boundary AND keeps the light-square dither phase: no shifting, ever.\n")
	b.WriteString(";\n")
	b.WriteString("; Per tile row the six byte columns are [aux0 aux1 aux2 main0 main1\n")
	b.WriteString("; main2]; columns 0 and 5 are pure background in ALL 24 tiles (asserted\n")
	b.WriteString("; by the generator), so only columns 1..4 are stored. Draw one row as:\n")
	b.WriteString(";   aux+0 = BG    aux+1,+2 = tile[0],tile[1]\n")
	b.WriteString(";   main+0,+1 = tile[2],tile[3]    main+2 = BG\n\n")

	fmt.Fprintf(&b, "TILE_PITCH     = %d      ; dots per square (destination), = 6 DHGR bytes\n", tiles.TileW)
	fmt.Fprintf(&b, "TILE_ROWS      = %d      ; scanlines per square\n", tiles.TileH)
	fmt.Fprintf(&b, "TILE_SQCOLS    = %d       ; byte columns per square PER PLANE (aux, main)\n", tiles.TileCols/2)
	fmt.Fprintf(&b, "TILE_BYTES     = %d       ; stored bytes per tile row (aux1,aux2,main0,main1)\n", tiles.TileBytes)
	fmt.Fprintf(&b, "TILE_STRIDE    = %d      ; bytes per tile (TILE_BYTES*TILE_ROWS)\n", tiles.TileStride)
	fmt.Fprintf(&b, "TILE_COUNT     = %d      ; piece tiles in the blob\n", tiles.NumTiles)
	fmt.Fprintf(&b, "TILE_BLOB_SIZE = %d    ; TILE_COUNT*TILE_STRIDE\n", tiles.BlobSize)
	fmt.Fprintf(&b, "TILE_BG_AUX    = $%02X     ; light-square dither, aux plane\n", tiles.BGAux)
	fmt.Fprintf(&b, "TILE_BG_MAIN   = $%02X     ; light-square dither, main plane\n", tiles.BGMain)
	fmt.Fprintf(&b, "TILE_BG_DARK   = $00     ; dark squares are simply blank\n")
	fmt.Fprintf(&b, "TILE_NONE      = $%02X     ; TILEIDX entry: empty square, draw background only\n", noTile)
	return b.String()
}

// asmInc emits the dispatch tables. It emits DATA, so it must be included
// with the destination segment already selected.
func asmInc() string {
	var b strings.Builder
	b.WriteString("; Generated by cmd/gentiles. DO NOT EDIT.\n")
	b.WriteString("; DHGR board-tile dispatch tables. The constants they go with are in\n")
	b.WriteString("; asm/tiledefs.inc; this file is pure DATA and lands in whichever\n")
	b.WriteString("; segment is current where it is included.\n\n")
	b.WriteString("; ---------------------------------------------------------------\n")
	b.WriteString("; TILEIDX / TILEOFFL / TILEOFFH: indexed EXACTLY like PIECECH, by\n")
	b.WriteString(";   dark<<4 | (piece & $0F)\n")
	b.WriteString("; TILEOFFL/H give the tile's byte offset from the blob base, so the\n")
	b.WriteString("; renderer never multiplies by TILE_STRIDE (=76, not a power of two).\n")
	b.WriteString("; ---------------------------------------------------------------\n")
	emitTable(&b, "TILEIDX", func(idx int) int {
		if idx < 0 {
			return noTile
		}
		return idx
	})
	emitTable(&b, "TILEOFFL", func(idx int) int {
		if idx < 0 {
			return 0
		}
		return (idx * tiles.TileStride) & 0xFF
	})
	emitTable(&b, "TILEOFFH", func(idx int) int {
		if idx < 0 {
			return 0
		}
		return (idx * tiles.TileStride) >> 8
	})
	return b.String()
}

// noTile marks an empty (or impossible) board nibble: draw background.
const noTile = 0xFF

// rowNames labels the six .byte lines, mirroring PIECECH's commentary.
var rowNames = []struct {
	first, last byte
	comment     string
}{
	{0x00, 0x00, "empty"},
	{0x01, 0x06, "wP wN wB wR wQ wK"},
	{0x07, 0x08, "impossible"},
	{0x09, 0x0E, "bP bN bB bR bQ bK"},
	{0x0F, 0x0F, "impossible"},
}

// emitTable writes a 32-entry table indexed by dark<<4|nibble, calling
// val with the blob tile index (-1 for empty/impossible).
func emitTable(b *strings.Builder, name string, val func(idx int) int) {
	fmt.Fprintf(b, "%s:\n", name)
	for _, dark := range []bool{false, true} {
		if dark {
			b.WriteString("        ; --- dark squares ---\n")
		} else {
			b.WriteString("        ; --- light squares ---\n")
		}
		hi := byte(0)
		if dark {
			hi = 0x10
		}
		for _, rn := range rowNames {
			var parts []string
			for n := rn.first; n <= rn.last; n++ {
				parts = append(parts, fmt.Sprintf("$%02X", val(tiles.TileIndex(n, dark))))
			}
			fmt.Fprintf(b, "        .byte %-23s ; $%02X-$%02X %s\n",
				strings.Join(parts, ","), hi|rn.first, hi|rn.last, rn.comment)
		}
	}
	b.WriteString("\n")
}
