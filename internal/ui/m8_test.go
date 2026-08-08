package ui_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/engsyms"
	"github.com/zellyn/chess6502/internal/ui"
)

const root = "../.."

// TestMain builds the whole chain the UI depends on, in order: the engine
// (which also emits asm/engine.lbl), the symbol bridge that turns those
// labels into asm/engsyms.inc, and then both builds of the UI — the shipping
// one that reads $C000/$C010 and the HARNESSKBD one these tests drive.
//
// Making the bridge a build step rather than a manual one is the whole point
// of docs/ui-design.md §11 risk 4: the failure mode of an engine refactor
// must be a broken build, never a UI silently linked against stale addresses.
func TestMain(m *testing.M) {
	if err := asmbuild.Build(root); err != nil {
		if errors.Is(err, asmbuild.ErrCA65NotInstalled) {
			os.Exit(0) // nothing to test without the assembler
		}
		panic(err)
	}
	if err := engsyms.Generate(root); err != nil {
		panic(err)
	}
	if err := asmbuild.BuildStandaloneAs(root, "m8", "m8t", "HARNESSKBD"); err != nil {
		panic(err)
	}
	if err := asmbuild.BuildStandaloneAs(root, "m8", "m8"); err != nil {
		panic(err)
	}
	// The Standard Delivery layout of the same source: the copier and the
	// staged payload at $0D00/$0E00, plus the CHAIN LOADER that re-enters the
	// boot loader for stage 2 (internal/delivery). It emits asm/m8sdboot.bin,
	// which cmd/mkdsk ships.
	if err := asmbuild.BuildStandaloneAs(root, "m8", "m8sd", asmbuild.SDChain); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func boot(t *testing.T) *ui.Machine {
	t.Helper()
	u, err := ui.Boot(root, nil)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	// ui.Boot already turned pondering off for the harness (Machine.PonderDefault
	// records the shipped default). The ponder gate re-enables it explicitly.
	return u
}

func mustEnter(t *testing.T, u *ui.Machine, s string) {
	t.Helper()
	if err := u.Enter(s); err != nil {
		t.Fatalf("typing %q: %v\n%s", s, err, u.Screen())
	}
}

// TestM8Boot is the smoke test: the shipping boot path really runs, the UI
// really ends up executing from Language Card RAM, and it paints a start
// position and waits for a key.
func TestM8Boot(t *testing.T) {
	u := boot(t)
	s := u.Screen()
	t.Logf("cold boot (%d cycles = %.1f ms):\n%s",
		u.M.Cycles, float64(u.M.Cycles)/1020.484, s)

	for _, tc := range []struct {
		row  int
		want string
	}{
		{0, "8FISH 1.0"},
		{0, "LEVEL 4"},
		{0, "YOU ARE WHITE"},
		{2, "MOVES"},
		{12, "WHITE TO MOVE"},
		{20, "N-NEW"},
		{23, "YOUR MOVE?"},
	} {
		if got := s.Text(tc.row); !contains(got, tc.want) {
			t.Errorf("row %d = %q, want it to contain %q", tc.row, got, tc.want)
		}
	}
	// The board itself, from the UI's own start-position image.
	wantBoard := []string{
		"r n b q k b n r ",
		"p p p p p p p p ",
		"                ",
		"                ",
		"                ",
		"                ",
		"P P P P P P P P ",
		"R N B Q K B N R ",
	}
	for i, want := range wantBoard {
		if got := s.Text(2 + i)[3 : 3+16]; got != want {
			t.Errorf("board rank %d: got %q, want %q", 8-i, got, want)
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// TestStartPosition gates the UI's hand-written 64-byte start-position image
// (STSQ/STPC in asm/m8.s) against chesstest.ParseFEN — the encoder every
// other test and the whole opening book agree on. It has to match EXACTLY,
// piece-list indices included, because the Zobrist hash is computed from
// those bytes and the book is keyed on the hash.
func TestStartPosition(t *testing.T) {
	u := boot(t)
	want, err := chesstest.ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	board := u.Defs["BOARD"]
	for rank := range uint16(8) {
		for file := range uint16(8) {
			sq := rank*16 + file
			got := u.Peek(board + sq)
			if got != want.Board[sq] {
				t.Errorf("BOARD[%#02x] = %#02x, want %#02x", sq, got, want.Board[sq])
			}
		}
	}
	psq := u.Defs["PIECESQ"]
	for i := range uint16(32) {
		if got := u.Peek(psq + i); got != want.PieceSq[i] {
			t.Errorf("PIECESQ[%d] = %#02x, want %#02x", i, got, want.PieceSq[i])
		}
	}
	for _, tc := range []struct {
		name string
		got  byte
		want byte
	}{
		{"SIDE", u.Peek(u.Defs["SIDE"]), want.Side},
		{"CASTLE", u.Peek(u.Defs["CASTLE"]), want.Castle},
		{"EPSQ", u.Peek(u.Defs["EPSQ"]), want.EpSq},
		{"HALFMOVE", u.Peek(u.Defs["HALFMOVE"]), 0},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %#02x, want %#02x", tc.name, tc.got, tc.want)
		}
	}
}

// TestUIByteBudget reports the MEASURED byte budget of the shipping image
// against the 8,176 bytes of $E000-$FFEF, from the linker's own segment
// sizes and label deltas — no estimates.
func TestUIByteBudget(t *testing.T) {
	lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	code := int(lbl["__UICODE_SIZE__"])
	bootBin, err := os.ReadFile(filepath.Join(root, "asm", "m8boot.bin"))
	if err != nil {
		t.Fatal(err)
	}
	const (
		lcTotal  = 0xFFF0 - 0xE000 // 8,176 B of directly executable LC RAM
		dataVars = 0x100           // $F700 page: variables + screen buffers
		dataHist = 3 * 0x100       // UIHFROM / UIHTO / UIHFLAG
		dataHash = 4 * 0x100       // UIHASH0-3, the game's repetition history
	)
	total := code + dataVars + dataHist + dataHash
	t.Logf("LANGUAGE CARD BUDGET ($E000-$FFEF, %d B)", lcTotal)
	t.Logf("  UICODE (code + static data, measured):        %5d B", code)
	t.Logf("  UI variables + screen buffers ($F700):        %5d B", dataVars)
	t.Logf("  game history from/to/flags ($F800-$FAFF):     %5d B", dataHist)
	t.Logf("  game hash history ($FB00-$FEFF):              %5d B", dataHash)
	t.Logf("  TOTAL:                                        %5d B (%.0f%% of %d)",
		total, 100*float64(total)/lcTotal, lcTotal)
	t.Logf("  FREE:                                         %5d B", lcTotal-total)
	t.Logf("MAIN cost: %d B, transient ($0800 copier, overwritten by PIECESQ)", len(bootBin))
	if total > lcTotal {
		t.Errorf("UI does not fit: %d B of %d B", total, lcTotal)
	}

	// Component sizes, from label deltas between the top-level section
	// boundaries. Every byte of UICODE falls into exactly one row, so the
	// rows sum to the segment size the linker reports.
	bounds := []struct{ label, what string }{
		{"m8entry", "entry vector (fixed at $E000 so m8boot.bin never changes)"},
		{"uigotorc", "asm/ui.s renderer: board, panel, coords, screen primitives"},
		{"entkey", "asm/entropy.inc: keyboard wait + entropy fold + seed"},
		{"m8main", "cold start, machine check, main loop, whose-turn-is-it"},
		{"uistartpos", "position bookkeeping: new game, apply, hash history"},
		{"uigen", "move generation/validation/legality (all borrowed from the engine)"},
		{"uisync", "game state: legal-move count, mate/stalemate/50/repetition"},
		{"m8engine", "the engine's turn: entropy seed, book probe, apply"},
		{"uilimits", "level table + the FT2_SOFTCLK safety-margin rule"},
		{"uidrive", "the UI's own iterative-deepening driver"},
		{"uiread", "line editor, move parsing, promotion prompt"},
		{"cmd_new", "commands: new/takeback/resign/draw/level/sides/quit/help"},
		{"uipaint", "painting: title, status, prompt, messages, opening name"},
		{"uithinkln", "think line + signed centipawn formatting"},
		{"STSQ", "tables and strings (start position, levels, KTAB, all text)"},
		{"dhclear", "asm/dhgr.s: the double-hi-res board renderer"},
		{"TILEIDX", "generated tile dispatch tables (cmd/gentiles)"},
	}
	type row struct {
		addr int
		what string
	}
	var rows []row
	for _, b := range bounds {
		a, ok := lbl[b.label]
		if !ok {
			t.Fatalf("boundary label %q not in asm/m8.lbl", b.label)
		}
		rows = append(rows, row{int(a), b.what})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].addr < rows[j].addr })
	end := 0xE000 + code
	t.Logf("UICODE components (label deltas, measured; they sum to %d B):", code)
	sum := 0
	for i, r := range rows {
		next := end
		if i+1 < len(rows) {
			next = rows[i+1].addr
		}
		t.Logf("  %5d B  %s", next-r.addr, r.what)
		sum += next - r.addr
	}
	if sum != code {
		t.Errorf("component sizes sum to %d B, but UICODE is %d B", sum, code)
	}
}

// TestShippingImageBoots runs the image that actually goes on the disk — the
// build that reads the real $C000/$C010 keyboard, not the harness input
// traps. m8boot.bin copies the shipping payload into Language Card RAM, the
// UI runs there, paints a start position, and then blocks in entkey's poll
// loop with the entropy counter spinning.
func TestShippingImageBoots(t *testing.T) {
	u, err := ui.BootShipping(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Spin(4_000_000); err != nil { // ~4 s of IIe time, no key
		t.Fatal(err)
	}
	s := u.Screen()
	t.Logf("shipping image (real-keyboard build) after boot:\n%s", s)
	for _, want := range []struct {
		row  int
		text string
	}{
		{0, "8FISH 1.0"},
		{2, "r n b q k b n r"},
		{9, "R N B Q K B N R"},
		{12, "WHITE TO MOVE"},
		{23, "YOUR MOVE?"},
	} {
		if got := s.Text(want.row); !contains(got, want.text) {
			t.Errorf("row %d = %q, want it to contain %q", want.row, got, want.text)
		}
	}
	// entkey's 16-cycle poll loop is what manufactures the engine's only
	// source of randomness on a machine with no clock. If it is not
	// spinning, the shipped engine has no entropy at all.
	entcnt := uint16(u.Peek(u.Defs["ENTCNT"])) | uint16(u.Peek(u.Defs["ENTCNT"]+1))<<8
	if entcnt == 0 {
		t.Error("ENTCNT is 0: the image is not in the entropy-collecting keyboard wait")
	}
	t.Logf("ENTCNT = %d after %d cycles: the entropy collector is spinning in the keyboard wait",
		entcnt, u.M.Cycles)
	// And it must have taken the display: without ALTCHARSET the board's
	// inverse lowercase is flashing punctuation on a real IIe.
	if !u.AltCharset() {
		t.Error("the shipping image did not select the ALTERNATE character set")
	}
}
