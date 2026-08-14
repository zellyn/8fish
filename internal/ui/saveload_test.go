package ui_test

// saveload_test.go gates FEATURE 2 of docs/prorwts2-design.md end to end, on
// the SHIPPING ARTEFACT: W stages the game record, pulls the transient
// SAVELOAD orchestrator and then the transient WRITE-CAPABLE ProRWTS2 build
// off the disk, and writes the record IN PLACE over the SAVE file's two
// blocks — through the Disk II card's real nibble write path onto a writable
// medium. O reads it back and REPLAYS it through the same referee the live
// game uses. Nothing is poked: every byte travels the boot ROM -> chain load
// -> resident reader -> transient writer pipeline a IIe would run.
//
// THE INTEGRITY GATE IS THE POINT. A write reaches the physical medium, so
// the worst possible failure is a stray sector. TestDiskSaveLoadRoundTrip
// denibblises all 560 sectors before and after the save and requires that
// ONLY the SAVE file's data sectors changed (delivery.SaveFileOffsets); the
// extractor still enforces every DATA checksum, so the tolerance it needs
// for write-splice-damaged ADDRESS checksums (internal/ui/diskwrite.go)
// cannot hide corruption.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/delivery"
	"github.com/zellyn/8fish/internal/saveload"
	"github.com/zellyn/8fish/internal/ui"
)

// uiWin mirrors asm/ui.s UIWIN (M8VARS+$19); not exported by package ui.
const uiWin = 0xF719

const (
	slBootBudget = 2_000_000_000 // boot + splash + big-book load
	slKeyBudget  = 1_500_000_000 // a key, incl. a 4-disk-call save behind W
)

// slMachine boots a DiskMachine on a WRITABLE copy of the shipping disk and
// leaves it at the game's first prompt, in TWO-PLAYER mode (no searches: the
// gate is about the disk, not the engine).
func slMachine(t *testing.T, dsk string) (*ui.DiskMachine, *ui.WritableDisk) {
	t.Helper()
	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	w, err := m.MakeWritable(dsk)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := m.BootToPrompt(slBootBudget); err != nil || !ok {
		t.Fatalf("boot: ok=%v err=%v (PC $%04X)", ok, err, m.CPU.PC())
	}
	if err := m.Enter("s", slKeyBudget); err != nil { // WHITE -> TWO PLAYERS
		t.Fatalf("s: %v", err)
	}
	if got := m.Mem.Main[ui.UIHUMAN]; got != 0xFF {
		t.Fatalf("UIHUMAN = $%02X after S, want $FF (two players)", got)
	}
	return m, w
}

// gameRows are the screen rows that fully describe the visible game state
// (title/who, board+coords, move panel, status, check, think, book) and
// exclude the transient message and prompt rows.
func gameRows(s *ui.Screen) string {
	var b strings.Builder
	for row := 0; row <= 16; row++ {
		b.WriteString(s.Text(row))
		b.WriteByte('\n')
	}
	return b.String()
}

// memPlies reads the live history arrays out of the machine.
func memPlies(m *ui.DiskMachine) []saveload.Ply {
	n := int(m.Mem.Main[ui.UIHCNT])
	plies := make([]saveload.Ply, n)
	for i := range n {
		plies[i] = saveload.Ply{
			From: m.Mem.Main[ui.UIHFROM+i],
			To:   m.Mem.Main[ui.UIHTO+i],
			Flag: m.Mem.Main[ui.UIHFLAG+i],
		}
	}
	return plies
}

func TestDiskSaveLoadRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots the disk twice, saves and loads through the Disk II card")
	}
	dsk := dskPath(t)
	m, w := slMachine(t, dsk)

	// The extractor round-trips the UNTOUCHED nibblised disk back to the
	// exact .dsk bytes — the baseline every diff below is measured against.
	before, err := w.ExtractSectors()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dsk)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, raw) {
		t.Fatal("ExtractSectors on the pristine disk does not reproduce the .dsk bytes")
	}

	// ---- play a short two-player game and SAVE it --------------------------
	for _, mv := range []string{"e2e4", "e7e5", "g1f3", "b8c6"} {
		if err := m.Enter(mv, slKeyBudget); err != nil {
			t.Fatalf("%s: %v\n%v", mv, err, m.Screen())
		}
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 4 {
		t.Fatalf("UIHCNT = %d after four plies, want 4", got)
	}
	preRows := gameRows(m.Screen())
	prePlies := memPlies(m)

	if err := m.Enter("w", slKeyBudget); err != nil {
		t.Fatalf("w: %v\n%v", err, m.Screen())
	}

	// ---- THE INTEGRITY GATE, FIRST: only the SAVE file's sectors changed ---
	// (Before the softer assertions, so a stray write is always named as a
	// stray write, whatever else the save did or did not manage.)
	after, err := w.ExtractSectors()
	if err != nil {
		t.Fatalf("extracting the written disk: %v", err)
	}
	allowed := map[int]bool{}
	for _, off := range delivery.SaveFileOffsets() {
		allowed[off] = true
	}
	changed := 0
	for off := 0; off < len(before); off += delivery.SectorBytes {
		if bytes.Equal(before[off:off+delivery.SectorBytes], after[off:off+delivery.SectorBytes]) {
			continue
		}
		changed++
		if !allowed[off] {
			t.Errorf("STRAY WRITE: sector at .dsk offset %#x (track %d) changed and is "+
				"not part of the SAVE file", off, off/delivery.SectorBytes/delivery.SectorsPerTrk)
		}
	}
	if changed == 0 {
		t.Fatal("no sector changed: the save never reached the medium")
	}
	t.Logf("integrity: %d sector(s) changed, all inside the SAVE file's %d",
		changed, len(delivery.SaveFileOffsets()))

	if msg := m.Screen().Text(17); !strings.Contains(msg, "GAME SAVED") {
		t.Fatalf("no save confirmation on the message row: %q\n%v",
			strings.TrimSpace(msg), m.Screen())
	}
	// The save must not have disturbed the game in memory.
	if got := memPlies(m); len(got) != 4 || got[0] != prePlies[0] || got[3] != prePlies[3] {
		t.Fatalf("the save changed the in-memory history: %v -> %v", prePlies, got)
	}

	// ---- the record on disk is EXACTLY the Go encoder's --------------------
	rec, err := delivery.ExtractSaveFile(after)
	if err != nil {
		t.Fatal(err)
	}
	want := saveload.Encode(prePlies,
		m.Mem.Main[ui.UIHUMAN], m.Mem.Main[ui.UILEVEL],
		m.Mem.Main[ui.UIRESULT], m.Mem.Main[uiWin])
	if !bytes.Equal(rec, want) {
		first := -1
		for i := range rec {
			if rec[i] != want[i] {
				first = i
				break
			}
		}
		t.Fatalf("the written record differs from internal/saveload.Encode at offset "+
			"%#x: got $%02X want $%02X (the asm and Go record builders diverged)",
			first, rec[first], want[first])
	}
	if plies, human, _, _, _, ok := saveload.Decode(rec); !ok || len(plies) != 4 || human != 0xFF {
		t.Fatalf("the written record does not decode: ok=%v plies=%d human=$%02X",
			ok, len(plies), human)
	}
	t.Logf("record: %d plies, byte-identical to the Go encoder (%d B)", len(prePlies), len(rec))

	// ---- wreck the game, LOAD, and get the position back -------------------
	if err := m.Enter("n", slKeyBudget); err != nil {
		t.Fatalf("n: %v", err)
	}
	if err := m.Enter("d2d4", slKeyBudget); err != nil {
		t.Fatalf("d2d4: %v", err)
	}
	if rows := gameRows(m.Screen()); rows == preRows {
		t.Fatal("the new game's screen matches the saved one: the wreck step did nothing")
	}
	if err := m.Enter("o", slKeyBudget); err != nil {
		t.Fatalf("o: %v\n%v", err, m.Screen())
	}
	if msg := m.Screen().Text(17); !strings.Contains(msg, "GAME LOADED") {
		t.Fatalf("no load confirmation: %q\n%v", strings.TrimSpace(msg), m.Screen())
	}
	if rows := gameRows(m.Screen()); rows != preRows {
		t.Fatalf("the loaded game does not match the saved one.\nSAVED:\n%s\nLOADED:\n%s",
			preRows, rows)
	}
	if got := memPlies(m); len(got) != 4 || got[0] != prePlies[0] || got[3] != prePlies[3] {
		t.Fatalf("the loaded history differs: %v -> %v", prePlies, got)
	}
	t.Logf("load (same session): board, panel and history restored")

	// ---- COLD REBOOT from the written medium: the save is durable ----------
	dsk2 := filepath.Join(t.TempDir(), "8fish-saved.dsk")
	if err := os.WriteFile(dsk2, after, 0o644); err != nil {
		t.Fatal(err)
	}
	m2, err := ui.NewDiskMachine(dsk2, ui.RomDir())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := m2.BootToPrompt(slBootBudget); err != nil || !ok {
		t.Fatalf("reboot: ok=%v err=%v", ok, err)
	}
	if err := m2.Enter("o", slKeyBudget); err != nil {
		t.Fatalf("o after reboot: %v\n%v", err, m2.Screen())
	}
	if msg := m2.Screen().Text(17); !strings.Contains(msg, "GAME LOADED") {
		t.Fatalf("no load confirmation after a cold reboot: %q\n%v",
			strings.TrimSpace(msg), m2.Screen())
	}
	if rows := gameRows(m2.Screen()); rows != preRows {
		t.Fatalf("after a cold reboot the loaded game differs.\nSAVED:\n%s\nLOADED:\n%s",
			preRows, rows)
	}
	if got := m2.Mem.Main[ui.UIHUMAN]; got != 0xFF {
		t.Fatalf("UIHUMAN = $%02X after a cold-reboot load, want $FF (restored)", got)
	}
	t.Logf("load (cold reboot from the written medium): identical position")
}

// TestDiskLoadDegrades drives the failure paths: O with no game ever saved
// (the shipped placeholder), and O against a corrupted record. Both must
// message and leave the game playable; neither may crash or half-load.
func TestDiskLoadDegrades(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: boots the disk twice")
	}
	dsk := dskPath(t)

	// ---- the shipped placeholder: not an error, just nothing to load -------
	m, _ := slMachine(t, dsk)
	if err := m.Enter("o", slKeyBudget); err != nil {
		t.Fatalf("o: %v\n%v", err, m.Screen())
	}
	emptyMsg := strings.TrimSpace(m.Screen().Text(17))
	if !strings.Contains(emptyMsg, "NO SAVED GAME") {
		t.Fatalf("empty slot: message %q, want NO SAVED GAME\n%v", emptyMsg, m.Screen())
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 0 {
		t.Fatalf("UIHCNT = %d after an empty-slot load, want 0 (untouched)", got)
	}
	if err := m.Enter("e2e4", slKeyBudget); err != nil {
		t.Fatalf("e2e4 after empty-slot load: %v", err)
	}
	if got := m.Mem.Main[ui.UIHCNT]; got != 1 {
		t.Fatalf("UIHCNT = %d after e2e4, want 1 (the game must remain playable)", got)
	}
	t.Logf("empty slot: %q, game untouched and playable", emptyMsg)

	// ---- a corrupted record: refused, game intact ---------------------------
	raw, err := os.ReadFile(dsk)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), raw...)
	for _, off := range delivery.SaveFileOffsets() {
		for i := 0x20; i < 0x40; i++ { // mid-sector garbage: checksum must catch it
			bad[off+i] ^= 0xA5
		}
	}
	dskBad := filepath.Join(t.TempDir(), "8fish-corrupt-save.dsk")
	if err := os.WriteFile(dskBad, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	m2, _ := slMachine(t, dskBad)
	if err := m2.Enter("e2e4", slKeyBudget); err != nil { // a live game to protect
		t.Fatalf("e2e4: %v", err)
	}
	preRows := gameRows(m2.Screen())
	if err := m2.Enter("o", slKeyBudget); err != nil {
		t.Fatalf("o on the corrupt disk: %v\n%v", err, m2.Screen())
	}
	if msg := m2.Screen().Text(17); !strings.Contains(msg, "UNREADABLE") {
		t.Fatalf("corrupt record: message %q, want SAVED GAME UNREADABLE\n%v",
			strings.TrimSpace(msg), m2.Screen())
	}
	if rows := gameRows(m2.Screen()); rows != preRows {
		t.Fatalf("a REFUSED load changed the game.\nBEFORE:\n%s\nAFTER:\n%s", preRows, rows)
	}
	if got := m2.Mem.Main[ui.UIHCNT]; got != 1 {
		t.Fatalf("UIHCNT = %d after a refused load, want 1 (game intact)", got)
	}
	t.Logf("corrupt record: %q, game intact", strings.TrimSpace(m2.Screen().Text(17)))

	// ---- a WELL-FORMED record with an ILLEGAL move: the replay guard --------
	// The checksum passes, so only the per-ply uifind/uitrylegal validation
	// stands between a hand-crafted record and a corrupted board. e2-e5 is no
	// pawn move; the load must refuse it and leave a CLEAN NEW GAME, never a
	// half-replayed position.
	forged := saveload.Encode([]saveload.Ply{{From: 0x14, To: 0x44}}, 0xFF, 4, 0, 0)
	evil := append([]byte(nil), raw...)
	for i, off := range delivery.SaveFileOffsets() {
		copy(evil[off:off+delivery.SectorBytes], forged[i*delivery.SectorBytes:(i+1)*delivery.SectorBytes])
	}
	dskEvil := filepath.Join(t.TempDir(), "8fish-forged-save.dsk")
	if err := os.WriteFile(dskEvil, evil, 0o644); err != nil {
		t.Fatal(err)
	}
	m3, _ := slMachine(t, dskEvil)
	if err := m3.Enter("o", slKeyBudget); err != nil {
		t.Fatalf("o on the forged disk: %v\n%v", err, m3.Screen())
	}
	if msg := m3.Screen().Text(17); !strings.Contains(msg, "UNREADABLE") {
		t.Fatalf("forged record: message %q, want SAVED GAME UNREADABLE\n%v",
			strings.TrimSpace(msg), m3.Screen())
	}
	if got := m3.Mem.Main[ui.UIHCNT]; got != 0 {
		t.Fatalf("UIHCNT = %d after a refused forged load, want 0 (a clean new game)", got)
	}
	if err := m3.Enter("e2e4", slKeyBudget); err != nil {
		t.Fatalf("e2e4 after the refused forged load: %v", err)
	}
	if got := m3.Mem.Main[ui.UIHCNT]; got != 1 {
		t.Fatalf("UIHCNT = %d after e2e4, want 1 (the machine must still referee)", got)
	}
	t.Logf("forged record (valid checksum, illegal ply): refused, clean new game")
}
