// Command gentiles slices the owner's hand-drawn DazzleDraw chessboard
// (assets/chess2-dazzledraw-save.bin, A2FC: 8 KB aux then 8 KB main) into
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
//	2  the artwork loses ink, or a geometric assumption the tile format
//	   rests on is broken (most such breaks stop the slice outright; a few,
//	   like a pawn drawn differently on different files, would ship a blob
//	   that no longer matches the art)
//
// Status 1 is retired; see the exit* constants for why.
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

	"github.com/zellyn/8fish/internal/tiles"
)

const (
	defaultArtPath = "assets/chess2-dazzledraw-save.bin"
	blobPath       = "internal/tiles/tileblob.bin"
	defsPath       = "asm/tiledefs.inc"
	incPath        = "asm/tiles.inc"
)

// Exit statuses.
//
// Status 1 is RETIRED, and deliberately left unallocated. It used to mean
// "the drawing slices, but the top trim clips ink" — a middle tier that
// existed only because the old 44x21 artwork was trimmed into 42x19 tiles.
// Since the CHESS2 redraw the tile is the whole source square, and the only
// way to lose a dot is to draw it outside the content window, which is a
// structural break (2) that also stops Build. Reusing 1 for something else
// would let an old script that special-cases it silently mis-read a new run;
// removing it says what happened.
const (
	exitOK     = 0
	exitBroken = 2
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
		pngPath  = fs.String("png", "", "write a PNG of the tiles AFTER slicing to this path")
		showDots = fs.Bool("dots", false, "in -check, list every lost dot's coordinates instead of summarising per row")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: gentiles [-check] [-art PATH] [-png PATH]\n\n"+
			"Slices the hand-drawn DazzleDraw board into the resident DHGR tile blob.\n"+
			"With -check, writes nothing and reports every broken assumption at once.\n"+
			"Exit: 0 clean, %d lost ink or a broken assumption (1 is retired).\n\n",
			exitBroken)
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
		fmt.Fprintf(stdout, "  wrote %s (the tiles as they will render, post-slice)\n", pngPath)
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
				"     post-slice: any lost ink is already gone.\n", pngPath)
		}
	}

	if !rep.Clean() {
		return exitBroken
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
	fmt.Fprintf(w, "  grid: 8x8 squares of %dx%d dots at origin (%d,%d); tile = %dx%d dots%s\n\n",
		tiles.SrcSquareW, tiles.SrcSquareH, tiles.SrcOriginX, tiles.SrcOriginY,
		tiles.TileW, tiles.TileH, trimPhrase())

	printLost(w, rep, showDots)
	printGridFit(w, rep)
	printViolations(w, rep)
	printExtents(w, rep)
	printSummary(w, rep)
}

// trimPhrase describes the source-to-tile relationship in one clause, so
// the header does not claim a trim that is not being taken.
func trimPhrase() string {
	if tiles.SrcTrimTop == 0 && tiles.TileW == tiles.SrcSquareW && tiles.TileH == tiles.SrcSquareH {
		return " = the WHOLE source square (no trim)"
	}
	return fmt.Sprintf(" after dropping the top %d source row(s)", tiles.SrcTrimTop)
}

// printLost is the headline: ink that never reaches a tile at all. It
// replaced a "CLIPPED INK" section that measured only the top trim; with
// SrcTrimTop at 0 that section could not fail, so the measurement was
// widened to the whole kept window rather than left in place as a
// tautology. The row/column split is printed because only ONE half of it
// can currently fail, and a reader is entitled to know which.
func printLost(w io.Writer, rep *tiles.Report, showDots bool) {
	ky0, ky1, kx0, kx1 := tiles.KeptWindow()
	fmt.Fprintf(w, "LOST INK (drawn outside the kept window: rows dy %d..%d, dots dx %d..%d)\n", ky0, ky1, kx0, kx1)
	if rep.LostTotal == 0 {
		fmt.Fprintf(w, "  NONE. Every drawn dot reaches a tile. This is the goal state.\n")
		fmt.Fprintf(w, "  Live half of this check: the DOT COLUMNS (dx outside %d..%d is dropped by the\n", kx0, kx1)
		fmt.Fprintf(w, "  4-byte tile row). The ROW half cannot fail while SrcTrimTop=%d and TileH=%d\n", tiles.SrcTrimTop, tiles.TileH)
		fmt.Fprintf(w, "  cover the whole %d-row square — that half is a tautology today, not a gate.\n\n", tiles.SrcSquareH)
		return
	}
	fmt.Fprintf(w, "  %d pixels of piece ink are LOST, across %d squares (%d outside the kept rows, %d outside the kept dot columns):\n\n",
		rep.LostTotal, len(rep.Lost), rep.LostRows, rep.LostCols)
	for _, sq := range rep.Lost {
		name := tiles.PieceAt(sq.R, sq.F)
		if name == "" {
			name = "no piece transcribed here"
		}
		fmt.Fprintf(w, "  %-3s %-34s %3d px\n", tiles.Square(sq.R, sq.F), name, sq.Count())
		for _, line := range dotLines(sq.Dots, showDots) {
			fmt.Fprintf(w, "        %s\n", line)
		}
	}
	fmt.Fprintf(w, "\n  Fix by moving that ink inside dy %d..%d, dx %d..%d.\n\n", ky0, ky1, kx0, kx1)
}

// printGridFit shows the two measurements the grid rests on: the frame the
// artist drew, and where the ink actually sits inside its squares. Both
// are printed with their slack, because a redraw's real question is "how
// much room have I got left".
func printGridFit(w io.Writer, rep *tiles.Report) {
	fmt.Fprintf(w, "GRID FIT (the declared grid must exactly fill the drawn frame)\n")
	fmt.Fprintf(w, "  frame:  bars x %d..%d and x %d..%d (%d dots each); rules y=%d and y=%d\n",
		tiles.BoardMinX, tiles.BorderLeft, tiles.BorderRight, tiles.BoardMaxX,
		tiles.BorderW, tiles.BorderTop, tiles.BorderBot)
	fmt.Fprintf(w, "  gap:    %d dots either side, %d row(s) top and bottom — asserted DARK\n",
		tiles.FrameGapX, tiles.FrameGapY)
	fmt.Fprintf(w, "  grid:   x %d..%d (8 x %d), y %d..%d (8 x %d)\n",
		tiles.SrcOriginX, tiles.SrcOriginX+8*tiles.SrcSquareW-1, tiles.SrcSquareW,
		tiles.SrcOriginY, tiles.SrcOriginY+8*tiles.SrcSquareH-1, tiles.SrcSquareH)
	lo, hi, _ := tiles.StoredDotSpan()
	fmt.Fprintf(w, "  stored: byte columns %d..%d cover dx %d..%d; declared content window dx %d..%d\n",
		tiles.FirstCol, tiles.LastCol, lo, hi, tiles.ContentMinDX, tiles.ContentMaxDX)
	fmt.Fprintf(w, "  ink:    board-wide dx %d..%d (slack %+d/%+d), dy %d..%d (slack %+d/%+d)\n\n",
		rep.InkMinDX, rep.InkMaxDX, rep.InkMinDX-tiles.ContentMinDX, tiles.ContentMaxDX-rep.InkMaxDX,
		rep.InkMinDY, rep.InkMaxDY, rep.InkMinDY, tiles.SrcSquareH-1-rep.InkMaxDY)
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
	ky0, ky1, kx0, kx1 := tiles.KeptWindow()
	fmt.Fprintf(w, "INK EXTENTS (per transcribed square; the drawing spec is dy %d..%d, dx %d..%d)\n",
		tiles.SrcTrimTop, tiles.SrcSquareH-1, tiles.ContentMinDX, tiles.ContentMaxDX)
	fmt.Fprintf(w, "  %-3s %-34s %-10s %-10s %6s  %s\n", "sq", "piece", "dy", "dx", "px", "kept?")
	for _, e := range rep.Extents {
		if e.Empty {
			fmt.Fprintf(w, "  %-3s %-34s %-10s %-10s %6d  %s\n",
				tiles.Square(e.R, e.F), tiles.PieceAt(e.R, e.F), "-", "-", 0, "-")
			continue
		}
		kept := "all"
		if e.MinDY < ky0 || e.MaxDY > ky1 || e.MinDX < kx0 || e.MaxDX > kx1 {
			kept = "LOSES INK"
		}
		fmt.Fprintf(w, "  %-3s %-34s %-10s %-10s %6d  %s\n",
			tiles.Square(e.R, e.F), tiles.PieceAt(e.R, e.F),
			fmt.Sprintf("%d..%d", e.MinDY, e.MaxDY), fmt.Sprintf("%d..%d", e.MinDX, e.MaxDX),
			e.Count, kept)
	}
	fmt.Fprintln(w)
}

func printSummary(w io.Writer, rep *tiles.Report) {
	fmt.Fprintf(w, "SUMMARY\n")
	fmt.Fprintf(w, "  what-if trim curve (board-wide ink a top trim WOULD cost; SrcTrimTop is %d, so\n", tiles.SrcTrimTop)
	fmt.Fprintf(w, "  none of it is being paid): top 1 row = %d, top 2 = %d, top 3 = %d; bottom 1 row = %d\n",
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
	default:
		fmt.Fprintf(w, "  RESULT: BROKEN — %d geometry finding(s), %d lost pixel(s) (exit %d)\n",
			len(rep.Violations), rep.LostTotal, exitBroken)
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
