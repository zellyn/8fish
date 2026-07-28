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
	for _, out := range []string{"m8", "m8sd"} {
		if err := asmbuild.BuildStandaloneAs(layoutRoot, "m8", out); err != nil {
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
	m8 := fileLen(t, "asm/m8.bin")
	bloadBoot := fileLen(t, "asm/m8boot.bin")
	sdBoot := fileLen(t, "asm/m8sdboot.bin")

	// The engine's own live data structures, from defs.inc.
	perPlyLo := defs["PIECESQ"]
	perPlyHi := defs["MOVESTACK"] // per-ply arrays run right up to MOVESTACK
	if perPlyLo == 0 || perPlyHi == 0 {
		t.Fatal("PIECESQ / MOVESTACK missing from defs.inc")
	}

	t.Log("STANDARD DELIVERY layout (asm/m8sd.cfg + internal/delivery):")
	disjoint(t, "SD layout", []region{
		{"copier (m8sdboot.bin)", CopierOrg, CopierOrg + sdBoot},
		{"staged UI payload (m8.bin)", PayloadOrg, PayloadOrg + m8},
		{"resident book", BookOrg, BookOrg + book},
		{"engine image", EngineOrg, EngineOrg + engine},
	})

	t.Log("BLOAD layout (asm/m8.cfg):")
	bloadBase := readCfgSymbol(t, "asm/m8.cfg", "UIPAYLOAD")
	disjoint(t, "BLOAD layout", []region{
		{"copier (m8boot.bin)", 0x0800, 0x0800 + bloadBoot},
		{"staged UI payload (m8.bin)", bloadBase, bloadBase + m8},
		{"resident book", BookOrg, BookOrg + book},
		{"engine image", EngineOrg, EngineOrg + engine},
	})

	// The book must fit its hole. Nothing else enforces this: the engine
	// reads BOOK_COUNT out of the blob at run time, so an oversized blob
	// would simply be written over the engine's first bytes.
	if BookOrg+book > EngineOrg {
		t.Errorf("book blob is %d B at $%04X: it runs %d B into the engine at $%04X",
			book, BookOrg, BookOrg+book-EngineOrg, EngineOrg)
	}
	// asm/book.inc is generated alongside the blob; if only one is
	// regenerated the asm probe walks the wrong shape.
	if got, want := bookinc["BOOK_BLOB_SIZE"], book; got != want {
		t.Errorf("asm/book.inc says BOOK_BLOB_SIZE = %d but internal/book/bookblob.bin is %d B: "+
			"re-run `go run ./cmd/genbook`", got, want)
	}
	if got := bookinc["BOOK_BASE"]; got != BookOrg {
		t.Errorf("asm/book.inc BOOK_BASE = $%04X, delivery.BookOrg = $%04X", got, BookOrg)
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
	if got := defs["MOVESTACKTOP"]; got != BookOrg {
		t.Errorf("MOVESTACKTOP $%04X != the book base $%04X: the generator's overflow "+
			"trap and the book no longer meet at the same address", got, BookOrg)
	}
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

// TestDebugBufferPlacement: GDVBUF, the GDVERIFY-only 4 KB sweep buffer, is
// documented in asm/defs.inc as living "above the resident book blob". The
// book has since grown past it. It is harmless today only because no test
// loads a book into a GDVERIFY machine — which is a property of the tests,
// not of the map, and therefore has to be stated somewhere that fails.
func TestDebugBufferPlacement(t *testing.T) {
	defs := readSyms(t, "asm/defs.inc")
	book := fileLen(t, "internal/book/bookblob.bin")
	buf := defs["GDVBUF"]
	if buf == 0 {
		t.Skip("GDVBUF not defined")
	}
	const gdvBufSize = 32 * 128
	t.Logf("GDVBUF $%04X-$%04X (%d B); resident book $%04X-$%04X (%d B)",
		buf, buf+gdvBufSize-1, gdvBufSize, BookOrg, BookOrg+book-1, book)
	if buf+gdvBufSize > EngineOrg {
		t.Errorf("GDVBUF $%04X+%d runs into the engine at $%04X", buf, gdvBufSize, EngineOrg)
	}
	if buf < BookOrg+book && buf+gdvBufSize > BookOrg {
		t.Errorf("GDVBUF ($%04X-$%04X) overlaps the resident book ($%04X-$%04X) by %d bytes. "+
			"asm/defs.inc still justifies $%04X as being ABOVE the book, quoting $2000-$2F19 "+
			"— the size the book had before it was widened to %d B. Either move the buffer, "+
			"or replace that justification with the real invariant (no GDVERIFY run may load "+
			"a book) and assert it where the GDVERIFY machine is built.",
			buf, buf+gdvBufSize-1, BookOrg, BookOrg+book-1,
			min(buf+gdvBufSize, BookOrg+book)-max(buf, BookOrg), buf, book)
	}
}

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
