package delivery

// layout_test.go enforces the WHOLE 8fish memory map from the built
// artefacts, not from comments about them.
//
// This project has twice shipped a comment that contradicted reality
// ($2000-$207F "reserved" when the book had always been based there, and a
// stale $0E00 staging address). Both were caught by reading; neither was
// caught by a gate. Everything below is derived from asm/*.cfg, asm/defs.inc,
// asm/*.lbl and the actual .bin sizes, so a map claim that stops being true
// becomes a test failure.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/rwts"
)

const layoutRoot = "../.."

// ---- tiny readers -------------------------------------------------------

var defsRe = regexp.MustCompile(`(?m)^([A-Z0-9_]+)\s*=\s*(\$[0-9A-Fa-f]+|[0-9]+)\b`)

// readSyms pulls NAME = $HEX and NAME = DECIMAL definitions out of an asm
// include file.
func readSyms(t *testing.T, rel string) map[string]int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(layoutRoot, rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	out := map[string]int{}
	for _, m := range defsRe.FindAllStringSubmatch(string(b), -1) {
		var v int64
		var err error
		if m[2][0] == '$' {
			v, err = strconv.ParseInt(m[2][1:], 16, 32)
		} else {
			v, err = strconv.ParseInt(m[2], 10, 32)
		}
		if err != nil {
			continue
		}
		out[m[1]] = int(v)
	}
	return out
}

var lblRe = regexp.MustCompile(`(?m)^al\s+([0-9A-Fa-f]+)\s+\.?(\S+)`)

func readLbl(t *testing.T, rel string) map[string]int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(layoutRoot, rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	out := map[string]int{}
	for _, m := range lblRe.FindAllStringSubmatch(string(b), -1) {
		v, err := strconv.ParseInt(m[1], 16, 32)
		if err != nil {
			continue
		}
		out[m[2]] = int(v)
	}
	return out
}

func fileLen(t *testing.T, rel string) int {
	t.Helper()
	fi, err := os.Stat(filepath.Join(layoutRoot, rel))
	if err != nil {
		t.Fatalf("%s: %v (run `make engine m8` or the internal/ui suite)", rel, err)
	}
	return int(fi.Size())
}

// buildArtefacts makes sure every .bin/.lbl this file reads exists and is
// current. Skipping when the assembler is absent is the only skip allowed:
// a layout gate that quietly skips because a file was missing is no gate.
func buildArtefacts(t *testing.T) {
	t.Helper()
	if err := asmbuild.Build(layoutRoot); err != nil {
		if err == asmbuild.ErrCA65NotInstalled {
			t.Skip("SKIP: ca65 not installed")
		}
		t.Fatal(err)
	}
	for _, v := range []struct {
		out     string
		defines []string
	}{{"m8", nil}, {"m8sd", []string{asmbuild.SDChain}}} {
		if err := asmbuild.BuildStandaloneAs(layoutRoot, "m8", v.out, v.defines...); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- the map ------------------------------------------------------------

type region struct {
	name       string
	start, end int // [start, end)
}

// disjoint reports every pair of regions that overlap.
func disjoint(t *testing.T, what string, rs []region) {
	t.Helper()
	sort.Slice(rs, func(i, j int) bool { return rs[i].start < rs[j].start })
	for i, r := range rs {
		t.Logf("  $%04X-$%04X  %6d B  %s", r.start, r.end-1, r.end-r.start, r.name)
		if i > 0 && r.start < rs[i-1].end {
			t.Errorf("%s: %q ($%04X-$%04X) OVERLAPS %q ($%04X-$%04X) by %d bytes",
				what, r.name, r.start, r.end-1, rs[i-1].name, rs[i-1].start, rs[i-1].end-1,
				rs[i-1].end-r.start)
		}
	}
}

// TestMainMemoryLayout: nothing in MAIN overlaps, in either delivery layout,
// and the engine image stops short of the harness trap page.
func TestMainMemoryLayout(t *testing.T) {
	buildArtefacts(t)
	defs := readSyms(t, "asm/defs.inc")
	bookinc := readSyms(t, "asm/book.inc")

	engine := fileLen(t, "asm/engine.bin")
	book := fileLen(t, "internal/book/bookblob.bin")
	booknames := fileLen(t, "internal/book/booknames.bin")
	tileblob := fileLen(t, "internal/tiles/tileblob.bin")
	m8 := fileLen(t, "asm/m8.bin")
	bloadBoot := fileLen(t, "asm/m8boot.bin")
	sdBoot := fileLen(t, "asm/m8sdboot.bin")

	// The engine's own live data structures, from defs.inc.
	perPlyLo := defs["PIECESQ"]
	perPlyHi := defs["MOVESTACK"] // per-ply arrays run right up to MOVESTACK
	if perPlyLo == 0 || perPlyHi == 0 {
		t.Fatal("PIECESQ / MOVESTACK missing from defs.inc")
	}

	// STAGE 1 is what the boot sector's own page table delivers. The book is
	// NOT in it any more: it arrives in stage 2, which is why the payload's
	// staging room stopped being a margin worth defending.
	t.Log("STANDARD DELIVERY stage 1 (asm/m8sd.cfg + internal/delivery):")
	disjoint(t, "SD stage 1", []region{
		{"copier + chain loader (m8sdboot.bin)", CopierOrg, CopierOrg + sdBoot},
		{"staged UI payload (m8.bin)", PayloadOrg, PayloadOrg + m8},
		{"engine image", EngineOrg, EngineOrg + engine},
	})
	// STAGE 2 lands after the copier has lifted the payload to $E000, so it
	// reuses the payload's staging area. Both blobs are then copied on -- the
	// tiles to LC bank 1, the names to LC bank 2, the entries to aux $0800 --
	// and main $2000-$3FFF is left free for the DHGR main half.
	rwtsblob := fileLen(t, "internal/rwts/rwtsblob.bin")
	t.Log("STANDARD DELIVERY stage 2 (landing zones in MAIN, then copied on):")
	disjoint(t, "SD stage 2", []region{
		{"copier + chain loader (m8sdboot.bin)", CopierOrg, CopierOrg + sdBoot},
		{"staged tile blob", TilesOrg, TilesOrg + tileblob},
		{"staged book name table", NamesOrg, NamesOrg + booknames},
		{"staged ProRWTS2 reader blob", RwtsOrg, RwtsOrg + rwtsblob},
		{"staged opening book", BookOrg, BookOrg + book},
		{"engine image", EngineOrg, EngineOrg + engine},
	})
	// The name table's landing zone is DERIVED on the asm side
	// (SD2NAMES = SD2TILES + SD2TILEN*256, so the two blobs are one span on
	// the disk). Recompute it here rather than trusting the constant: if
	// tileblob.bin grows past a page boundary, the copier's source address
	// moves and this one has to move with it or stage 2 reads the artwork's
	// tail as names.
	if want := TilesOrg + (tileblob+SectorBytes-1)/SectorBytes*SectorBytes; NamesOrg != want {
		t.Errorf("NamesOrg is $%04X but the tile blob (%d B at $%04X) ends at page "+
			"$%04X: asm/m8.s derives SD2NAMES from TILE_BLOB_SIZE, so the two now "+
			"disagree about where stage 2 puts the name table",
			NamesOrg, tileblob, TilesOrg, want)
	}
	// Same derivation discipline for the reader blob's landing zone: asm/m8.s
	// computes SD2RWTS as the page after the name table; RwtsOrg must be the
	// identical sum or the copier lifts the name table's tail as driver code.
	if want := NamesOrg + (booknames+SectorBytes-1)/SectorBytes*SectorBytes; RwtsOrg != want {
		t.Errorf("RwtsOrg is $%04X but the name table (%d B at $%04X) ends at page "+
			"$%04X: asm/m8.s's SD2RWTS and delivery.RwtsOrg disagree",
			RwtsOrg, booknames, NamesOrg, want)
	}
	if RwtsOrg+rwtsblob > BookOrg {
		t.Errorf("the staged reader blob ($%04X+%d) runs into the book's landing "+
			"zone at $%04X", RwtsOrg, rwtsblob, BookOrg)
	}

	// The BLOAD layout carries NO staged opening book any more (2026-08-08):
	// the arrow-key cursor entry pushed the payload past $2000, and the
	// deliberate resolution (internal/ui TestUIByteBudget, docs/ui-design.md
	// §18.3) was to retire BLOAD-with-a-book-preloaded — a dev convenience —
	// rather than hold the UI under a ceiling only that convenience needed.
	// The harness stages the book AFTER the copier runs (ui.Machine.stageBook),
	// mirroring the disk's stage-2 ordering; an on-device BLOAD simply has no
	// resident book, and the probe misses cleanly.
	t.Log("BLOAD layout (asm/m8.cfg; no staged book — see the comment):")
	bloadBase := readCfgSymbol(t, "asm/m8.cfg", "UIPAYLOAD")
	disjoint(t, "BLOAD layout", []region{
		{"copier (m8boot.bin)", 0x0800, 0x0800 + bloadBoot},
		{"staged UI payload (m8.bin)", bloadBase, bloadBase + m8},
		{"engine image", EngineOrg, EngineOrg + engine},
	})
	if end := bloadBase + m8; end > BookOrg {
		t.Logf("BLOAD layout: the staged payload ends at $%04X, %d B past the book "+
			"staging area at $%04X — which is WHY the BLOAD layout has no staged book",
			end, end-BookOrg, BookOrg)
	}

	// The book must fit its landing zone at $2000, and -- the constraint that
	// actually binds now -- its RESIDENT home in aux $0800, below the DHGR aux
	// half at $2000. Nothing else enforces either: the engine reads BOOK_COUNT
	// out of the blob at run time, so an oversized blob is simply written over
	// whatever follows it.
	if BookOrg+book > EngineOrg {
		t.Errorf("book blob is %d B at $%04X: it runs %d B into the engine at $%04X",
			book, BookOrg, BookOrg+book-EngineOrg, EngineOrg)
	}
	if BookAux+book > 0x2000 {
		t.Errorf("book blob is %d B: at its resident aux base $%04X it runs %d B into "+
			"the DHGR aux half at $2000", book, BookAux, BookAux+book-0x2000)
	}
	t.Logf("resident book entries: aux $%04X-$%04X, %d B of the %d B below the DHGR aux "+
		"half (%d B spare)", BookAux, BookAux+book-1, book, 0x2000-BookAux,
		0x2000-BookAux-book)
	// The tile blob's resident home is Language Card bank 1, above the
	// engine's LCCODE, and it must not run past $DFFF into the unbanked
	// $E000-$FFFF where the UI lives.
	if TilesLC+tileblob > 0xE000 {
		t.Errorf("tile blob is %d B at LC $%04X: it runs %d B past $DFFF into the UI",
			tileblob, TilesLC, TilesLC+tileblob-0xE000)
	}
	// And the name table's home is Language Card BANK 2. Same ceiling, same
	// reason: $E000-$FFFF is NOT banked, so an overrun would land on the UI's
	// own code in both banks.
	if NamesLC+booknames > 0xE000 {
		t.Errorf("the book's name table is %d B at LC bank 2 $%04X: it runs %d B past "+
			"$DFFF into the UI, which is unbanked", booknames, NamesLC,
			NamesLC+booknames-0xE000)
	}
	t.Logf("resident book names: LC bank 2 $%04X-$%04X, %d B of 4096 (%d B spare)",
		NamesLC, NamesLC+booknames-1, booknames, 0x1000-booknames)
	// asm/book.inc is generated alongside the blobs; if only one is
	// regenerated the asm probe walks the wrong shape.
	if got, want := bookinc["BOOK_BLOB_SIZE"], book; got != want {
		t.Errorf("asm/book.inc says BOOK_BLOB_SIZE = %d but internal/book/bookblob.bin is %d B: "+
			"re-run `go run ./cmd/genbook`", got, want)
	}
	if got, want := bookinc["BOOK_NAMES_SIZE"], booknames; got != want {
		t.Errorf("asm/book.inc says BOOK_NAMES_SIZE = %d but internal/book/booknames.bin "+
			"is %d B: re-run `go run ./cmd/genbook`", got, want)
	}
	if got := bookinc["BOOK_NAMES"]; got != NamesLC {
		t.Errorf("asm/book.inc BOOK_NAMES = $%04X, delivery.NamesLC = $%04X: the copier "+
			"and uibookname disagree about where the name table lives", got, NamesLC)
	}
	if got := bookinc["BOOK_BASE"]; got != BookAux {
		t.Errorf("asm/book.inc BOOK_BASE = $%04X, delivery.BookAux = $%04X: the probe and "+
			"the copier disagree about where the resident book lives", got, BookAux)
	}

	// The engine image must stop below the harness trap page: $BFF0-$BFFF is
	// COUT/IN/INSTAT/CLOCK/EXIT, and with FT2_SOFTCLK the engine WRITES
	// $BFF4-$BFF6 on real hardware.
	trap := defs["COUT_TRAP"]
	if EngineOrg+engine > trap {
		t.Errorf("engine image ends at $%04X, at or past the trap page at $%04X",
			EngineOrg+engine-1, trap)
	}
	t.Logf("engine ends $%04X, %d B below the trap page $%04X (linker cap is $BFEF)",
		EngineOrg+engine-1, trap-(EngineOrg+engine), trap)

	// The engine's per-ply arrays and move stack are what the staged payload
	// sits on top of. That is deliberate and safe ONLY because the copier
	// runs before the first search; assert the region really is the one the
	// comments name, so a per-ply array added above MOVESTACK is a failure.
	if perPlyHi != 0x0E00 || perPlyLo != 0x0800 {
		t.Errorf("per-ply block moved: PIECESQ $%04X, MOVESTACK $%04X (map comments in "+
			"asm/m8.s, asm/m8sd.cfg and internal/delivery all name $0800/$0E00)",
			perPlyLo, perPlyHi)
	}
	// MOVESTACKTOP is still $2000, but for a NEW reason: main $2000-$3FFF is
	// now the DHGR main half rather than the book, and a move stack that ran
	// into it would paint its overflow onto the board.
	if got := defs["MOVESTACKTOP"]; got != 0x2000 {
		t.Errorf("MOVESTACKTOP $%04X != $2000: the generator's overflow trap and the "+
			"DHGR main half no longer meet at the same address", got)
	}
}

// TestBookClearsTheAuxTextPage is the gate the whole 2026-08-01 change exists
// to satisfy, and it is stated where it FAILS rather than in a comment.
//
// The board ships in MIXED MODE. Mixed mode plus double hi-res means the
// four-row text window is EIGHTY-column text, and 80-column text is fetched
// two bytes per column: the AUX byte is the even column, the MAIN byte the
// odd one. So the aux half of the whole text page, $0400-$07FF, belongs to
// the video scanner -- rows 20-23 are $0650-$0677, $06D0-$06F7, $0750-$0777
// and $07D0-$07F7, four spans in the MIDDLE of it.
//
// The resident opening book is the only thing that has ever wanted that
// memory. It fitted at aux $0200 when it was 7,407 B including its name
// table; moving the name table to Language Card bank 2 left 5,705 B, which
// starts at $0800 and clears the page entirely.
//
// Two ways this could silently break: someone moves BOOK_BASE back down, or
// the entries grow DOWNWARD (they cannot today, but a future "start it lower
// to gain headroom" would). Both are the same assertion, and both are read
// from the built artefacts.
func TestBookClearsTheAuxTextPage(t *testing.T) {
	buildArtefacts(t)
	bookinc := readSyms(t, "asm/book.inc")
	book := fileLen(t, "internal/book/bookblob.bin")

	// $0400-$07FF: the 80-column text page's aux half, all 24 rows.
	const textLo, textHi = 0x0400, 0x0800
	base := bookinc["BOOK_BASE"]
	if base == 0 {
		t.Fatal("BOOK_BASE missing from asm/book.inc")
	}
	if base != BookAux {
		t.Errorf("asm/book.inc BOOK_BASE = $%04X but delivery.BookAux = $%04X", base, BookAux)
	}
	end := base + book // exclusive
	if base < textHi && end > textLo {
		lo, hi := max(base, textLo), min(end, textHi)
		t.Fatalf("THE RESIDENT BOOK OVERLAPS THE 80-COLUMN TEXT PAGE'S AUX HALF.\n"+
			"  book:      aux $%04X-$%04X (%d B)\n"+
			"  text page: aux $%04X-$%04X (the EVEN columns of all 24 rows)\n"+
			"  overlap:   $%04X-$%04X, %d bytes\n"+
			"Mixed mode's four-row window (text rows 20-23) is fetched from aux "+
			"$0650, $06D0, $0750 and $07D0. Whatever is under those spans is what "+
			"the player would read as the status line. Either the book goes back "+
			"above $0800, or the board goes back to full screen -- there is no "+
			"third option, and a book split across the window is not a book.",
			base, end-1, book, textLo, textHi-1, lo, hi-1, hi-lo)
	}
	t.Logf("the resident book is aux $%04X-$%04X; the 80-column text page's aux half "+
		"($%04X-$%04X) is clear by %d bytes", base, end-1, textLo, textHi-1, base-textHi)

	// The four window rows, spelled out, so the numbers in every comment about
	// this change are checked against the real interleave rather than retyped.
	for row := 20; row < 24; row++ {
		rb := 0x0400 + (row%8)*0x80 + (row/8)*0x28
		if rb >= base && rb < end {
			t.Errorf("window row %d's aux base $%04X is inside the book", row, rb)
		}
		t.Logf("  window row %d: aux $%04X-$%04X (even columns) + main $%04X-$%04X (odd)",
			row, rb, rb+39, rb, rb+39)
	}
}

// TestDirbufAuxReservation pins the driver's 512-byte directory buffer to aux
// $0200-$03FF and proves that window is disjoint from every live aux structure.
//
// The big-book loader reads each file DIRECTLY into aux (allow_aux), and the
// driver's aux read path switches BOTH RAMRD and RAMWRT on for the whole read
// loop, reading its sapling block-list back out of dirbuf mid-loop. So dirbuf
// MUST live in aux, and it must not sit on anything the load has to preserve:
// the 80-column text page (aux $0400-$07FF), the resident book (aux $0800+),
// the boot splash / DHGR page 1 (aux $2000-$3FFF), or the big-book / TT window
// (aux $4000-$BFFF). aux $0200-$03FF -- the low transposition-table region,
// dead before the first search, and the big book only loads at boot / New Game
// -- is the one clean home. This is the executable half of the memory-map
// decision in docs/loadcover-feasibility.md; the splash gate exercises it live
// (the 32 KB load runs with dirbuf there and the splash survives byte-perfect).
//
// HAZARD, gated by the comment above it in cmd/genrwts: a driver call DURING a
// game (a future save/load, not at a game boundary) would find live data here
// and need to re-home dirbuf or save/restore it.
func TestDirbufAuxReservation(t *testing.T) {
	buildArtefacts(t)
	rwtsinc := readSyms(t, "asm/rwts.inc")
	dirbuf := rwtsinc["RWTS_DIRBUF"]
	if dirbuf == 0 {
		t.Fatal("RWTS_DIRBUF missing from asm/rwts.inc")
	}
	if dirbuf != 0x0200 {
		t.Fatalf("RWTS_DIRBUF = $%04X, want aux $0200 (the reserved low-TT dirbuf window)", dirbuf)
	}
	const dirbufLen = 0x200            // the driver reads whole 512-byte blocks into it
	lo, hi := dirbuf, dirbuf+dirbufLen // [lo, hi) exclusive

	for _, r := range []struct {
		name   string
		lo, hi int
	}{
		{"80-column text page (aux half)", 0x0400, 0x0800},
		{"resident opening book", BookAux, BookAux + fileLen(t, "internal/book/bookblob.bin")},
		{"boot splash / DHGR page 1 (aux half)", 0x2000, 0x4000},
		{"big-book / transposition-table window", 0x4000, 0xC000},
	} {
		if lo < r.hi && hi > r.lo {
			t.Fatalf("dirbuf (aux $%04X-$%04X) OVERLAPS %s (aux $%04X-$%04X) -- the "+
				"aux-direct book load would corrupt it, or it would corrupt the load",
				lo, hi-1, r.name, r.lo, r.hi-1)
		}
	}
	t.Logf("driver dirbuf reserved at aux $%04X-$%04X, clear of the text page, the "+
		"resident book, the splash and the TT/big-book window", lo, hi-1)
}

// TestLanguageCardLayout: the UI's code, its static data and the 6502 vectors
// tile $E000-$FFFF without overlapping, measured from the linker.
func TestLanguageCardLayout(t *testing.T) {
	buildArtefacts(t)
	m8lbl := readLbl(t, "asm/m8.lbl")
	englbl := readLbl(t, "asm/engine.lbl")

	code := m8lbl["__UICODE_SIZE__"]
	if code == 0 {
		t.Fatal("__UICODE_SIZE__ missing from asm/m8.lbl")
	}
	const (
		lcBase  = 0xE000
		m8vars  = 0xF700
		vectors = 0xFFF0
	)
	disjoint(t, "language card", []region{
		{"UICODE (code + static data)", lcBase, lcBase + code},
		{"UI variables + screen buffers", m8vars, m8vars + 0x100},
		{"game history from/to/flags", 0xF800, 0xFB00},
		{"game hash history", 0xFB00, 0xFF00},
		{"UI80BUF (the mixed-mode window's 80-column staging line)", 0xFF00, 0xFF50},
		{"6502 vectors", vectors, 0x10000},
	})
	if lcBase+code > m8vars {
		t.Errorf("UICODE is %d B: it runs %d B into the UI's variables at $%04X",
			code, lcBase+code-m8vars, m8vars)
	}
	t.Logf("UICODE %d B of %d ($E000-$%04X); %d B free before $%04X",
		code, m8vars-lcBase, lcBase+code-1, m8vars-(lcBase+code), m8vars)

	// THE COPIER'S 128-BYTE CEILING. asm/m8.s installs the engine's LC-resident
	// aux primitives with `ldy #ENG_LCCODE_SIZE-1 / ... / bpl`. That loop copies
	// NOTHING at all the moment the size exceeds 128, because Y starts negative
	// — and the failure is silent: the UI still boots and still plays, it just
	// probes a transposition table through uninstalled code.
	lcSize := englbl["__LCCODE_SIZE__"]
	if lcSize == 0 {
		t.Fatal("__LCCODE_SIZE__ missing from asm/engine.lbl")
	}
	t.Logf("engine LCCODE (installed at $D000 by the copier's bpl loop): %d B", lcSize)
	if lcSize > 128 {
		t.Errorf("LCCODE is %d B: asm/m8.s's copier uses `ldy #ENG_LCCODE_SIZE-1 ... bpl`, "+
			"which copies zero bytes once the size exceeds 128", lcSize)
	}
	if lcSize > 0x1000 {
		t.Errorf("LCCODE is %d B: it does not fit LC bank 1's $D000-$DFFF", lcSize)
	}

	// The copier moves whole PAGES: ceil(UICODE/256). It must not walk past
	// the UI's variables at the destination end.
	pages := (code + 255) / 256
	if lcBase+pages*256 > m8vars {
		t.Errorf("the copier moves %d pages (%d B) to $E000, %d B past the UI's variables at $%04X",
			pages, pages*256, lcBase+pages*256-m8vars, m8vars)
	}
}

// TestDebugBufferPlacement: GDVBUF, the GDVERIFY-only 4 KB sweep buffer, sits
// in main RAM among things that have moved twice. Whatever it currently
// overlaps is a property of the memory map, not of which tests happen to run,
// so it has to be stated somewhere that FAILS rather than in a comment.
func TestDebugBufferPlacement(t *testing.T) {
	defs := readSyms(t, "asm/defs.inc")
	book := fileLen(t, "internal/book/bookblob.bin")
	buf := defs["GDVBUF"]
	if buf == 0 {
		t.Skip("GDVBUF not defined")
	}
	const gdvBufSize = 32 * 128
	t.Logf("GDVBUF $%04X-$%04X (%d B); book: resident at AUX $%04X-$%04X, staged "+
		"once at main $%04X-$%04X during boot (%d B)",
		buf, buf+gdvBufSize-1, gdvBufSize, BookAux, BookAux+book-1,
		BookOrg, BookOrg+book-1, book)
	if buf+gdvBufSize > EngineOrg {
		t.Errorf("GDVBUF $%04X+%d runs into the engine at $%04X", buf, gdvBufSize, EngineOrg)
	}
	// REWRITTEN 2026-08-01, because what the buffer overlaps CHANGED. It used
	// to overlap the resident book at main $2000-$3CEE, and the invariant was
	// "a GDVERIFY build and a loaded book are mutually exclusive", enforced in
	// chesstest.newStubMachine. The book now lives in AUX $0800-$1E48, so that
	// overlap — and the guard that enforced it — are gone.
	//
	// What GDVBUF overlaps NOW is the DHGR MAIN half. Double hi-res page 1 is
	// aux $2000-$3FFF + main $2000-$3FFF, and GDVBUF is main $3000-$3FFF: the
	// bottom half of the board's main bytes. So the live invariant is that a
	// GDVERIFY build and the on-device board are mutually exclusive. That is
	// fine — the sweep is a debug-only entry that renders nothing — but it is
	// an invariant, not an accident, and this is where it is stated so that
	// moving either one fails.
	//
	// It also still overlaps where stage 2 STAGES the book ($2000) before the
	// copier lifts it to aux. That is harmless in a way worth writing down:
	// staging happens once, during boot, before any engine code runs, and a
	// GDVERIFY sweep can only run long afterwards.
	const dhgrMainLo, dhgrMainHi = 0x2000, 0x3FFF
	if buf+gdvBufSize-1 < dhgrMainLo || buf > dhgrMainHi {
		t.Errorf("GDVBUF $%04X-$%04X no longer overlaps the DHGR main half "+
			"($%04X-$%04X). That is not necessarily wrong, but this test and "+
			"asm/defs.inc both document the overlap as deliberate, so one of "+
			"them is now lying — fix the comments, not this assertion",
			buf, buf+gdvBufSize-1, dhgrMainLo, dhgrMainHi)
	}
	if buf < BookAux+bookAuxMax {
		t.Errorf("GDVBUF $%04X starts below aux $%04X: the resident book lives at "+
			"aux $%04X and up, and main/aux addresses this low are engine scratch "+
			"that a GDVERIFY sweep would corrupt with nothing asserting it",
			buf, BookAux+bookAuxMax, BookAux)
	}
}

// bookAuxMax is the aux window the resident book may occupy ($0200-$1FFF).
const bookAuxMax = 0x2000 - BookAux

// readCfgSymbol pulls `NAME: type = export, value = $HEX;` out of a ld65
// config's SYMBOLS block.
func readCfgSymbol(t *testing.T, rel, name string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(layoutRoot, rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	re := regexp.MustCompile(fmt.Sprintf(`(?s)%s:.*?value\s*=\s*\$([0-9A-Fa-f]+)`, name))
	m := re.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("%s: no %s symbol", rel, name)
	}
	v, err := strconv.ParseInt(m[1], 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	return int(v)
}

// TestLanguageCardBank1Layout does for $D000-$DFFF what TestLanguageCardLayout
// does for $E000-$FFFF, and it exists because that region had NO region list
// at all -- which is how asm/m8.s's map comment came to be wrong twice at once
// (LCCODE's size, and the renderer's tables off by one 152-byte stride).
//
// Bank 1 is where the DHGR artwork and its init-built tables became resident
// on 2026-07-31. Bank 2 is still entirely unused, and the test says so with a
// number rather than a comment, so the day something wants 4 KB the answer is
// already measured.
func TestLanguageCardBank1Layout(t *testing.T) {
	buildArtefacts(t)
	m8lbl := readLbl(t, "asm/m8.lbl")
	englbl := readLbl(t, "asm/engine.lbl")

	lccode := int(englbl["__LCCODE_RUN__"])
	lcsize := int(englbl["__LCCODE_SIZE__"])
	if lccode == 0 || lcsize == 0 {
		t.Fatal("__LCCODE_RUN__/__LCCODE_SIZE__ missing from asm/engine.lbl")
	}
	tileblob := fileLen(t, "internal/tiles/tileblob.bin")

	// The renderer's addresses come from the LINKER, not from this file: they
	// are computed in asm/m8.s from DHTILES and the blob size.
	dhtiles := int(m8lbl["DHTILES"])
	rowl, rowh, blnk := int(m8lbl["DHROWL"]), int(m8lbl["DHROWH"]), int(m8lbl["dhblnk"])
	for name, v := range map[string]int{
		"DHTILES": dhtiles, "DHROWL": rowl, "DHROWH": rowh, "dhblnk": blnk,
	} {
		if v == 0 {
			t.Fatalf("%s missing from asm/m8.lbl", name)
		}
	}
	if dhtiles != TilesLC {
		t.Errorf("asm/m8.s DHTILES = $%04X but delivery.TilesLC = $%04X: the copier "+
			"and the renderer disagree about where the artwork lives", dhtiles, TilesLC)
	}
	const rowTab = 152 // one entry per board scanline
	const blankTab = 2 * 76

	rwtsblob := fileLen(t, "internal/rwts/rwtsblob.bin")
	if rwtsblob != rwts.BlobLen {
		t.Errorf("internal/rwts/rwtsblob.bin is %d B but gen.go says %d: re-run "+
			"`go run ./cmd/genrwts`", rwtsblob, rwts.BlobLen)
	}
	if RwtsLC != rwts.EntryAddr {
		t.Errorf("delivery.RwtsLC = $%04X but rwts.EntryAddr = $%04X", RwtsLC, rwts.EntryAddr)
	}
	t.Log("LANGUAGE CARD BANK 1 ($D000-$DFFF):")
	disjoint(t, "LC bank 1", []region{
		{"LCCODE (ttfetch + the book's bkfetch/bkhdr)", lccode, lccode + lcsize},
		{"DHTILES (the artwork, delivered by stage 2)", dhtiles, dhtiles + tileblob},
		{"DHROWL (scanline bases, low)", rowl, rowl + rowTab},
		{"DHROWH (scanline bases, high)", rowh, rowh + rowTab},
		{"dhblnk (the two synthesised empty tiles)", blnk, blnk + blankTab},
		{"ProRWTS2 read-only driver (m8rwtsinit installs it)", RwtsLC, RwtsLC + rwtsblob},
	})
	top := RwtsLC + rwtsblob
	if top > 0xE000 {
		t.Errorf("LC bank 1 use ends at $%04X, past $DFFF and into the UI's own "+
			"$E000-$FFEF", top-1)
	}
	t.Logf("  bank 1 used $D000-$%04X, %d B free below $E000", top-1, 0xE000-top)

	// BANK 2 is not empty any more: it holds the opening book's name table.
	// That is the whole reason mixed mode fits -- the 1,702 bytes moved OUT of
	// the aux blob, off the 80-column text page, and into the last contiguous
	// 4 KB in the machine.
	//
	// The bank-1 free run is what makes bank 2 the only answer, so measure it
	// rather than asserting it: if a future change frees enough of bank 1 to
	// hold the table, the bank switch in uibookname stops being necessary and
	// somebody should know.
	booknames := fileLen(t, "internal/book/booknames.bin")
	t.Log("LANGUAGE CARD BANK 2 ($D000-$DFFF):")
	disjoint(t, "LC bank 2", []region{
		{"the opening book's name table (uibookname reads it)", NamesLC, NamesLC + booknames},
	})
	if NamesLC+booknames > 0xE000 {
		t.Errorf("the name table runs past $DFFF: $E000-$FFFF is NOT banked, it is the "+
			"UI's own code in both banks (%d B over)", NamesLC+booknames-0xE000)
	}
	t.Logf("  bank 2 used $%04X-$%04X, %d B free", NamesLC, NamesLC+booknames-1,
		0xE000-(NamesLC+booknames))
	if free := 0xE000 - top; free >= booknames {
		t.Errorf("Language Card bank 1 now has %d B free, enough for the %d-byte name "+
			"table. uibookname's bank switch (and the copier's second copy) exist only "+
			"because it did not fit; if it does now, they are complexity with no reason",
			free, booknames)
	}
}

// TestPage3Layout gates the one part of the memory map that had nothing but a
// comment behind it: the book probe's main-RAM landing pads.
//
// asm/book.s copies each 9-byte book entry out of aux into BKENT and the
// 8-byte header into BKHDR, both in page 3, and page 3 is CROWDED in a way
// defs.inc does not say. Three occupants, in address order:
//
//	$0300-$0355  the Disk II boot ROM's 2-of-6 nibble buffer, rewritten on
//	             EVERY sector it reads -- and asm/m8.s re-enters that ROM to
//	             chain-load stage 2
//	$0356-$03D5  the same ROM's denibble table, built once at $C600 entry
//	$03F2-$03F4  the Autostart RESET vector and power-up byte, which m8main
//	             writes to force Ctrl-Reset to COLD boot
//
// So the pads have to live in $03D6-$03F1, and BKENT sits one byte above the
// ROM's table. One byte is not a margin you leave to a comment, and the bounds
// are not guesses: they are read out of the ROM image itself below.
func TestPage3Layout(t *testing.T) {
	buildArtefacts(t)
	englbl := readLbl(t, "asm/engine.lbl")
	m8lbl := readLbl(t, "asm/m8.lbl")

	bkent, bkhdr := int(englbl["BKENT"]), int(englbl["BKHDR"])
	if bkent == 0 || bkhdr == 0 {
		t.Skip("SKIP: BKENT/BKHDR not exported from asm/book.s")
	}

	// The Disk II boot ROM's page-3 ceiling, read from the ROM rather than
	// remembered: its denibble table is indexed `eor $02D6,y` with y a valid
	// 6-and-2 nibble ($96-$FF), so the highest byte it touches is $02D6+$FF.
	const romTableTop = 0x02D6 + 0xFF // $03D5
	romDir := os.Getenv("GOAPPLE2_ROMS")
	if romDir == "" {
		romDir = "/Users/zellyn/gh/goapple2/data/roms" // same convention as internal/ui.RomDir
	}
	romPath := filepath.Join(romDir,
		"Apple Disk II 16 Sector Interface Card ROM P5 - 341-0027.bin")
	if rom, err := os.ReadFile(romPath); err == nil && len(rom) == 256 {
		// $C6AF is `eor $02D6,y` -- confirm the base is really $02D6, so a
		// different ROM revision cannot silently move the ceiling.
		if rom[0xAF] != 0x59 || rom[0xB0] != 0xD6 || rom[0xB1] != 0x02 {
			t.Errorf("the Disk II ROM's denibble read at $C6AF is %02X %02X %02X, not "+
				"`eor $02D6,y`: its page-3 ceiling is not $%04X, and BKENT at $%04X "+
				"may be inside it", rom[0xAF], rom[0xB0], rom[0xB1], romTableTop, bkent)
		} else {
			t.Logf("Disk II ROM confirmed: `eor $02D6,y` at $C6AF, so its page-3 "+
				"denibble table tops out at $%04X", romTableTop)
		}
	} else {
		t.Logf("(Disk II ROM not readable at %s; using the documented $%04X ceiling)",
			romPath, romTableTop)
	}

	const bkentLen, bkhdrLen = 9, 8
	const autostart = 0x03F2 // RESET vector + power-up byte
	disjoint(t, "page 3", []region{
		{"Disk II ROM 2-of-6 nibble buffer", 0x0300, 0x0356},
		{"Disk II ROM denibble table", 0x0356, romTableTop + 1},
		{"BKENT (one book entry, copied from aux)", bkent, bkent + bkentLen},
		{"BKHDR (the blob header, copied from aux)", bkhdr, bkhdr + bkhdrLen},
		{"Autostart RESET vector + power-up byte", autostart, 0x03F5},
	})
	if bkent <= romTableTop {
		t.Errorf("BKENT is $%04X, at or below the Disk II ROM's page-3 ceiling $%04X: "+
			"the chain load would overwrite it on every sector of stage 2",
			bkent, romTableTop)
	}
	if bkhdr+bkhdrLen > autostart {
		t.Errorf("BKHDR ($%04X-$%04X) runs into the Autostart vector at $%04X",
			bkhdr, bkhdr+bkhdrLen-1, autostart)
	}
	t.Logf("page 3 pads: BKENT $%04X-$%04X, BKHDR $%04X-$%04X; they start %d byte(s) "+
		"past the Disk II ROM's last page-3 byte ($%04X) and end %d B below the "+
		"Autostart vector at $%04X",
		bkent, bkent+bkentLen-1, bkhdr, bkhdr+bkhdrLen-1,
		bkent-romTableTop, romTableTop, autostart-(bkhdr+bkhdrLen), autostart)

	// The engine's own page-3 scratch (FT2_ADAPT) must stay clear of both.
	defs := readSyms(t, "asm/defs.inc")
	if rt := defs["RTARGET2"]; rt != 0 && rt >= bkent {
		t.Errorf("the engine's page-3 scratch reaches $%04X, into BKENT at $%04X",
			rt, bkent)
	}
	_ = m8lbl
}
