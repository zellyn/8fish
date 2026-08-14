package ui_test

// bigbook_test.go gates FEATURE 1 of docs/prorwts2-design.md end to end, on
// the SHIPPING ARTEFACT: the resident ProRWTS2 reader boots off the disk,
// loads the BIG BOOK into the idle transposition-table window at aux
// $4000-$BFFF, the engine plays the opening OUT OF THAT WINDOW, the first
// real search closes the one-way BIGBOOKOK latch, and a New Game reloads.
//
// Nothing here is poked into place: every byte the assertions read arrived
// through the Disk II boot ROM, the Standard Delivery chain load, and then —
// the part under test — the ProRWTS2 driver's own motor/seek/readadr/denibble
// path against the real nibblised .dsk. A wrong slot poke, a wrong track
// seed, a misplaced ProDOS block or a broken denibble table all end here as
// byte diffs, not as mysteries on a IIe.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/book"
	"github.com/zellyn/8fish/internal/delivery"
	"github.com/zellyn/8fish/internal/ui"
)

func readAsm(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "asm", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readDisk(path string) ([]byte, error) { return os.ReadFile(path) }

// bigBookLabels reads the two addresses this file drives to and asserts on.
func bigBookLabels(t *testing.T) (m8bigbook, mloop, bookpg uint16) {
	t.Helper()
	m8lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	englbl, err := ui.ParseLbl(filepath.Join(root, "asm", "engine.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	for name, m := range map[string]map[string]uint16{
		"m8bigbook": m8lbl, "mloop": m8lbl, "bookpg": englbl,
	} {
		if _, ok := m[name]; !ok {
			t.Fatalf("label %q missing", name)
		}
	}
	return m8lbl["m8bigbook"], m8lbl["mloop"], englbl["bookpg"]
}

// TestDiskBigBook is the disk-end-to-end gate (the TestDiskPlays discipline,
// pointed at the new machinery). One boot, then the whole feature in order:
//
//  1. ZP PROTOCOL: all 256 zero-page bytes are snapshot around the boot-time
//     m8bigbook call and must come back equal (the driver claims $3C-$67 —
//     the live BOARD rows included — and rwtszp must give every byte back).
//     The documented UI scratch is excluded; the board is not scratch.
//  2. THE LOAD IS REAL: aux $4000-$BFFF equals bigbook.bin byte for byte —
//     and equals what delivery.ExtractBookFile reads back OUT OF THE .DSK,
//     so "the driver read the disk" and "the builder wrote the disk" are the
//     same claim checked from both ends. The latch is open, bookpg = $40.
//  3. THE OPENING COMES FROM $4000: with the RESIDENT book's magic zeroed
//     (so any fallback probe would miss), e2e4 still gets an instant book
//     reply with the opening named — only the big book can have served it.
//  4. THE LATCH CLOSES: an off-book move forces a real search; BIGBOOKOK
//     drops to 0 and bookpg back to $08 (mesearch's one-way close). This is
//     the mutation gate for the latch: delete the close and this fails.
//  5. NEW GAME RELOADS: N reopens the latch and aux $4000 matches the blob
//     again — which the search had just overwritten, so a pass proves a
//     genuine second read, not a survivor.
func TestDiskBigBook(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots a disk, loads 32 KB twice, searches")
	}
	dsk := dskPath(t)
	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	m8bigbook, mloop, bookpg := bigBookLabels(t)
	big := book.DefaultBig()

	// ---- 1. the zp protocol, around the boot-time load ---------------------
	const budget = 2_000_000_000
	// The splash no longer waits for a key: it shows FULL-SCREEN and the big
	// book loads straight into aux behind it (asm/m8.s m8main), so m8bigbook is
	// reached on the boot path with no keypress. Run straight to it.
	ok, err := m.RunUntilPC(m8bigbook, budget)
	if err != nil || !ok {
		t.Fatalf("boot never reached m8bigbook (ok=%v err=%v PC $%04X)", ok, err, m.CPU.PC())
	}
	bootToLoad := m.Cycles // the splash is up; the aux-direct load is about to begin
	var zpBefore [256]byte
	copy(zpBefore[:], m.Mem.Main[:256])
	// ...and the two regions the reader must never touch: the ENGINE IMAGE
	// (main $4000-$BBFF) and the UI's own code ($E000-$F6FF; one byte is the
	// documented exception, the poked digit of f_book). The aux-direct read's
	// only writes are AUX (dirbuf at aux $0200-$03FF and the $4000+ book
	// window) and its zp window: main $2000-$3FFF (the splash's DHGR half) and
	// everything else in these ranges must be BYTE-IDENTICAL across the load.
	engBefore := append([]byte(nil), m.Mem.Main[0x4000:0xBC00]...)
	uiBefore := append([]byte(nil), m.Mem.Main[0xE000:0xF700]...)
	ok, err = m.RunUntilPC(mloop, budget)
	if err != nil || !ok {
		t.Fatalf("m8bigbook never returned to mloop (ok=%v err=%v PC $%04X)", ok, err, m.CPU.PC())
	}
	loadDone := m.Cycles
	// The UI's documented borrowed scratch may differ; nothing else may. The
	// board ($40-$BF) is the range the driver's own window overlaps, so a
	// dropped rwtszp swap-back fails here with a square's address in hand.
	scratch := func(i int) bool {
		return (i >= 0xCB && i <= 0xDA) || i == 0xE1 || i == 0xE2 || i == 0xF0 || i == 0xF1
	}
	for i := range 256 {
		if scratch(i) {
			continue
		}
		if m.Mem.Main[i] != zpBefore[i] {
			t.Errorf("zp $%02X changed across m8bigbook: $%02X -> $%02X (the driver's "+
				"window is $3C-$67; rwtszp must restore every byte)",
				i, zpBefore[i], m.Mem.Main[i])
		}
	}
	for i, b := range m.Mem.Main[0x4000:0xBC00] {
		if b == engBefore[i] {
			continue
		}
		if 0x4000+i == int(bookpg) {
			continue // the ONE byte the latch writes: the probe's base page
		}
		t.Fatalf("the load MODIFIED THE ENGINE IMAGE at main $%04X: $%02X -> $%02X",
			0x4000+i, engBefore[i], b)
	}
	if got := m.Mem.Main[bookpg]; got != byte(book.BigBase>>8) {
		t.Fatalf("bookpg = $%02X right after the load, want $%02X", got, book.BigBase>>8)
	}
	lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	fbookDigit := int(lbl["f_book"]) + 5 - 0xE000
	for i, b := range m.Mem.Main[0xE000:0xF700] {
		if b != uiBefore[i] && i != fbookDigit {
			t.Fatalf("the load MODIFIED UICODE at $%04X: $%02X -> $%02X", 0xE000+i, uiBefore[i], b)
		}
	}
	t.Logf("zp protocol: all non-scratch zero page equal across the load "+
		"(board rows $40-$67 included); engine image and UICODE untouched; "+
		"splash up at %.2f s, big book loaded under it in +%.2f s (board+first "+
		"input at ~%.2f s)",
		float64(bootToLoad)/1_020_484, float64(loadDone-bootToLoad)/1_020_484,
		float64(loadDone)/1_020_484)

	// ---- 2. the load is real, verified, and latched ------------------------
	if ok, err := m.RunToKeyboard(budget); err != nil || !ok {
		t.Fatalf("boot: ok=%v err=%v (PC $%04X)", ok, err, m.CPU.PC())
	}
	if got := m.Mem.Aux[book.BigBase : book.BigBase+book.BigWindow]; !bytes.Equal(got, big) {
		n, first := 0, -1
		for i := range big {
			if got[i] != big[i] {
				if first < 0 {
					first = i
				}
				n++
			}
		}
		t.Fatalf("the big book in aux differs from bigbook.bin in %d of %d bytes; "+
			"first at aux $%04X: got $%02X want $%02X — the ProRWTS2 read or the "+
			"aux lift is broken", n, len(big), book.BigBase+first, got[first], big[first])
	}
	// ...and the .dsk itself carries those bytes where the driver's block
	// arithmetic looks for them (both ends of the same claim).
	raw, err := readDisk(dsk)
	if err != nil {
		t.Fatal(err)
	}
	for i := range delivery.BookFiles {
		fromDisk, err := delivery.ExtractBookFile(raw, i)
		if err != nil {
			t.Fatal(err)
		}
		want := big[i*delivery.BookFileBytes : (i+1)*delivery.BookFileBytes]
		if !bytes.Equal(fromDisk, want) {
			t.Fatalf("%s on the .dsk does not match bigbook.bin", delivery.BookFileName(i))
		}
	}
	if got := m.Mem.Main[ui.BIGBOOKOK]; got != 1 {
		t.Fatalf("BIGBOOKOK = %d after a verified load, want 1", got)
	}
	if got := m.Mem.Main[bookpg]; got != byte(book.BigBase>>8) {
		t.Fatalf("bookpg = $%02X after the load, want $%02X (the probe must read "+
			"the big book)", got, book.BigBase>>8)
	}
	t.Logf("BIG BOOK: %d B verified in aux $%04X-$%04X, latch open, bookpg = $40",
		len(big), book.BigBase, book.BigBase+len(big)-1)

	// ---- 3. the opening is served FROM the big book ------------------------
	// Neuter the resident book's ENTRIES (the header stays: uibookname reads
	// the name table's address from it): a probe that fell back to $0800 now
	// misses every real key, so a NAMED book reply can only have come from
	// aux $4000.
	resident := book.DefaultEntries()
	for i := book.HeaderSize; i < len(resident); i++ {
		m.Mem.Aux[delivery.BookAux+i] = 0
	}
	const keyBudget = 1_500_000_000
	if err := m.Enter("l", keyBudget); err != nil {
		t.Fatalf("l: %v", err)
	}
	if err := m.Key('1', keyBudget); err != nil {
		t.Fatalf("level 1: %v", err)
	}
	if err := m.Enter("e2e4", keyBudget); err != nil {
		t.Fatalf("e2e4: %v\n%v", err, m.Screen())
	}
	if got := m.Screen().Text(16); !strings.Contains(got, "BOOK:") {
		t.Fatalf("no opening name after e2e4 with the resident book neutered: the "+
			"reply did not come from the big book at aux $4000\nrow 16: %q\n%v",
			strings.TrimSpace(got), m.Screen())
	}
	if got := m.Mem.Main[ui.BIGBOOKOK]; got != 1 {
		t.Fatalf("BIGBOOKOK = %d after a book reply, want 1 (a book move must not "+
			"close the latch)", got)
	}
	t.Logf("book reply out of aux $4000 (resident book neutered): %q",
		strings.TrimSpace(m.Screen().Text(16)))

	// ---- 4. out of book: the one-way latch closes --------------------------
	if err := m.Enter("e1e2", keyBudget); err != nil {
		t.Fatalf("e1e2: %v\n%v", err, m.Screen())
	}
	if got := m.Screen().Text(16); strings.Contains(got, "BOOK:") {
		t.Fatalf("still in book after Ke2? %q", strings.TrimSpace(got))
	}
	if got := m.Mem.Main[ui.BIGBOOKOK]; got != 0 {
		t.Errorf("BIGBOOKOK = %d after the first real search, want 0: mesearch's "+
			"latch close is missing, and the probe would read TT-mangled bytes as "+
			"book entries next move", got)
	}
	if got := m.Mem.Main[bookpg]; got != byte(delivery.BookAux>>8) {
		t.Errorf("bookpg = $%02X after leaving book, want $%02X (back to the "+
			"resident blob)", got, delivery.BookAux>>8)
	}
	t.Log("first real search: latch closed, probe re-pointed at the resident book")

	// ---- 5. New Game reloads what the search overwrote ---------------------
	if err := m.Enter("n", keyBudget); err != nil {
		t.Fatalf("n: %v\n%v", err, m.Screen())
	}
	if got := m.Mem.Main[ui.BIGBOOKOK]; got != 1 {
		t.Fatalf("BIGBOOKOK = %d after New Game, want 1: the reload did not happen "+
			"or did not verify", got)
	}
	if got := m.Mem.Main[bookpg]; got != byte(book.BigBase>>8) {
		t.Fatalf("bookpg = $%02X after the New Game reload, want $%02X", got, book.BigBase>>8)
	}
	if got := m.Mem.Aux[book.BigBase : book.BigBase+book.BigWindow]; !bytes.Equal(got, big) {
		t.Fatal("aux $4000-$BFFF does not match bigbook.bin after the New Game " +
			"reload — the second read never happened (the search had overwritten " +
			"the window, so this cannot be a leftover)")
	}
	t.Log("New Game: the big book re-read from disk over the searched-over window")
}

// TestBigBookLoadFailureDegradesHonestly: a disk whose book region is
// corrupt (one flipped byte in BOOK3's data) must fail the CHECKSUM, leave
// the latch closed and the probe on the resident book, say so once — and the
// game must still play its opening from the resident book. The feature can
// only ever degrade to today's shipped behaviour; this is where that claim
// is executed rather than asserted.
func TestBigBookLoadFailureDegradesHonestly(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots a disk")
	}
	dsk := dskPath(t)
	raw, err := readDisk(dsk)
	if err != nil {
		t.Fatal(err)
	}
	// Flip one byte of the last book file's first data block, on a copy.
	offs, err := delivery.BookRegionOffsets(book.DefaultBig(), []byte("SAVELOAD STUB"))
	if err != nil {
		t.Fatal(err)
	}
	last := 0
	for _, o := range offs {
		last = max(last, o)
	}
	raw[last+7] ^= 0xFF
	bad := filepath.Join(t.TempDir(), "8fish-corrupt.dsk")
	if err := os.WriteFile(bad, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := ui.NewDiskMachine(bad, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	_, _, bookpg := bigBookLabels(t)
	if ok, err := m.BootToPrompt(2_000_000_000); err != nil || !ok {
		t.Fatalf("boot: ok=%v err=%v (PC $%04X)", ok, err, m.CPU.PC())
	}
	if got := m.Mem.Main[ui.BIGBOOKOK]; got != 0 {
		t.Fatalf("BIGBOOKOK = %d after a corrupt load, want 0: the checksum verify "+
			"opened the latch on garbage", got)
	}
	if got := m.Mem.Main[bookpg]; got != byte(delivery.BookAux>>8) {
		t.Fatalf("bookpg = $%02X after a corrupt load, want $%02X (the resident book)",
			got, delivery.BookAux>>8)
	}
	if got := m.Screen().Text(17); !strings.Contains(got, "SMALL BOOK") {
		t.Errorf("the failure is silent: message row = %q, want the SMALL BOOK notice",
			strings.TrimSpace(got))
	}
	// ...and the game still opens from the resident book.
	const keyBudget = 1_500_000_000
	if err := m.Enter("l", keyBudget); err != nil {
		t.Fatalf("l: %v", err)
	}
	if err := m.Key('1', keyBudget); err != nil {
		t.Fatalf("level 1: %v", err)
	}
	if err := m.Enter("e2e4", keyBudget); err != nil {
		t.Fatalf("e2e4: %v\n%v", err, m.Screen())
	}
	if got := m.Screen().Text(16); !strings.Contains(got, "BOOK:") {
		t.Fatalf("no book reply from the RESIDENT book after the big book failed: %q\n%v",
			strings.TrimSpace(got), m.Screen())
	}
	t.Logf("corrupt disk: latch closed, notice shown, resident book played %q",
		strings.TrimSpace(m.Screen().Text(16)))
}

// TestBigBookNeedsTheChainLoad is TestBoardNeedsTheChainLoad's twin for the
// reader: RWTSDEF is $00 in BOTH payloads (a BRUN has no staged driver and no
// boot-loader state), and only the DISK copier stores the three boot facts.
func TestBigBookNeedsTheChainLoad(t *testing.T) {
	lbl, err := ui.ParseLbl(filepath.Join(root, "asm", "m8.lbl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"RWTSDEF", "RWTSSLOT", "RWTSTRK"} {
		def, ok := lbl[name]
		if !ok {
			t.Fatalf("asm/m8.lbl has no %s", name)
		}
		off := int(def) - 0xE000
		for _, bin := range []string{"m8.bin", "m8sd.bin"} {
			b := readAsm(t, bin)
			if off < 0 || off >= len(b) {
				t.Fatalf("%s at $%04X is outside %s", name, def, bin)
			}
			if b[off] != 0 {
				t.Errorf("%s has %s = $%02X, want $00: only the chain loader may set it",
					bin, name, b[off])
			}
		}
	}
	// The disk copier stores $01 to RWTSDEF; the BLOAD copier must not.
	def := lbl["RWTSDEF"]
	patch := []byte{0xA9, 0x01, 0x8D, byte(def), byte(def >> 8)}
	if !bytes.Contains(readAsm(t, "m8sdboot.bin"), patch) {
		t.Error("m8sdboot.bin never stores $01 to RWTSDEF: the disk would boot with " +
			"a resident reader nothing ever calls")
	}
	if bytes.Contains(readAsm(t, "m8boot.bin"), patch) {
		t.Error("m8boot.bin stores $01 to RWTSDEF, but a BRUN has no staged driver: " +
			"m8rwtsinit would copy garbage into LC bank 1 and m8bigbook would call it")
	}
}

// TestPonderRespectsTheLatch: while the BIGBOOKOK latch is open the TT *is*
// the big book, so m8ponder must be a no-op — one ponder search would
// overwrite thousands of entries. Driven on the harness image (the only
// build with a controllable keyboard trap): with the latch poked open and
// the big book poked into aux, a human turn passes through m8ponder and the
// whole window must come back untouched. The control run (latch closed)
// proves the instrument: the same turn then DOES write the window.
func TestPonderRespectsTheLatch(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: two ponder searches")
	}
	big := book.DefaultBig()
	run := func(latch byte) (diff int) {
		u := boot(t)
		enablePonder(u)
		setupRootM(t, u, "rnbqkbnr/pppp1ppp/8/4p3/4P3/5N2/PPPP1PPP/RNBQKB1R b KQkq - 1 2")
		copy(u.M.Mem.Aux[book.BigBase:], big)
		u.Poke(ui.BIGBOOKOK, latch)
		// One pass through mloop reaches m8ponder on the human's turn. With
		// the latch OPEN the gate returns immediately and the machine parks
		// at the prompt; with it CLOSED the ponder parks at its first
		// keyboard poll, and the trap is then disabled so the search runs to
		// its walk-away backstop (runPonderToCap's hardware model).
		u.M.SendInput([]byte{0x0D})
		if err := u.RunToInput(); err != nil {
			t.Fatalf("reaching m8ponder: %v", err)
		}
		if u.Peek(ui.PONDERING) != 0 {
			savedStat, savedIn := u.M.Mem.InStatusAddr, u.M.Mem.InAddr
			u.M.Mem.InStatusAddr, u.M.Mem.InAddr = 0, 0
			if _, _, err := u.M.Run(80_000_000); err != nil {
				t.Fatalf("running the ponder: %v", err)
			}
			u.M.Mem.InStatusAddr, u.M.Mem.InAddr = savedStat, savedIn
		} else if latch == 0 {
			t.Fatal("control: the ponder never started with the latch closed")
		}
		for i, b := range u.M.Mem.Aux[book.BigBase : book.BigBase+book.BigWindow] {
			if b != big[i] {
				diff++
			}
		}
		return diff
	}
	if diff := run(0); diff == 0 {
		t.Fatal("control: with the latch CLOSED the ponder wrote nothing to aux " +
			"$4000-$BFFF — the instrument cannot see a ponder, so the gate is vacuous")
	} else {
		t.Logf("control: latch closed, ponder wrote %d bytes of the window (as a "+
			"ponder should)", diff)
	}
	if diff := run(1); diff != 0 {
		t.Fatalf("with the latch OPEN the ponder modified %d bytes of the big book: "+
			"m8ponder's BIGBOOKOK gate is missing", diff)
	}
	t.Log("latch open: m8ponder no-ops; the big book is byte-identical after the turn")
}
