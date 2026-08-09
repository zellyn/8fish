package ui_test

// splash_test.go gates the BOOT TITLE CARD (asm/m8.s m8splash) on the SHIPPING
// DISK: it boots asm/8fish.dsk from the Disk II boot ROM, the same path a real
// machine takes, and proves the hand-drawn splash is read off the disk,
// decoded by the 6502 PackBits decoder straight into DHGR page 1, and that a
// keypress advances past it to a rendered board.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/zellyn/chess6502/internal/splash"
	"github.com/zellyn/chess6502/internal/ui"
)

const splashBudget = 600_000_000

// TestDiskSplashShowsThenAdvances is the end-to-end splash gate.
func TestDiskSplashShowsThenAdvances(t *testing.T) {
	dsk := dskPath(t)
	m, err := ui.NewDiskMachine(dsk, ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}

	// The splash's key-wait (asm/m8.s splhold) is the FIRST keyboard poll on a
	// disk boot: the machine runs there before the board is ever painted.
	ok, err := m.RunToKeyboard(splashBudget)
	if err != nil || !ok {
		t.Fatalf("boot never reached the splash key-wait (ok=%v err=%v PC $%04X)",
			ok, err, m.CPU.PC())
	}

	// ---- 0. the splash is shown FULL-SCREEN, not MIXED ----------------------
	// It is a full 192-line image, so unlike the board it must NOT carry the
	// four-row text window (that window shows the uninitialised text page as
	// garbage). Regress asm/m8.s m8splash's `sta MIXCLR` and the bottom of the
	// logo — "Seagull Sisters Software" — is replaced by text-page noise.
	if m.Mem.Mixed || m.Mem.Text || !m.Mem.DHires() {
		t.Fatalf("splash not full-screen DHGR: Mixed=%v Text=%v DHires=%v "+
			"(want false/false/true)", m.Mem.Mixed, m.Mem.Text, m.Mem.DHires())
	}

	// ---- 1. the splash bytes landed in DHGR page 1, decoded correctly -------
	// dhgrScreen reads page 1 in A2FC order (aux half then main half), which is
	// exactly the raw splash layout. It must equal the hand-drawn asset.
	asset, err := os.ReadFile(filepath.Join(root, "assets", "fish8-splash-dazzledraw-save.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got := dhgrScreen(m)
	if !bytes.Equal(got, asset) {
		n, first := 0, -1
		for i := range asset {
			if got[i] != asset[i] {
				if first < 0 {
					first = i
				}
				n++
			}
		}
		t.Fatalf("the splash in DHGR page 1 differs from the asset in %d of %d bytes; "+
			"first at $%04X (bank/off): got $%02X want $%02X — the disk read, the aux "+
			"lift, or the 6502 PackBits decoder is wrong",
			n, len(asset), first, got[first], asset[first])
	}
	// ...and it is what the Go codec produces, both ends of the same claim.
	want, err := splash.Decode(splash.Blob())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the on-screen splash is not splash.Decode(splash.Blob())")
	}
	t.Logf("splash decoded into DHGR page 1 (%d B) matches the hand-drawn asset", len(got))

	// ---- 2. a keypress advances to the board --------------------------------
	m.SendKey(' ')
	ok, err = m.RunToKeyboard(splashBudget)
	if err != nil || !ok {
		t.Fatalf("a keypress did not advance the splash to the game prompt (ok=%v err=%v)", ok, err)
	}

	// The board is now painted: DHGR page 1 is no longer the splash (the board
	// paint + the big-book load's dhclear overwrote it), and the game's text
	// screen shows the prompt row's help.
	if bytes.Equal(dhgrScreen(m), asset) {
		t.Fatal("after a keypress the screen is still the splash: the boot did not " +
			"advance to the board")
	}
	if got := m.Screen().Text(17); got == "" {
		t.Fatal("no prompt row after advancing past the splash")
	}
	t.Logf("keypress advanced past the splash; board up, prompt row: %q",
		bytesTrim(m.Screen().Text(17)))
}

func bytesTrim(s string) string {
	return string(bytes.TrimSpace([]byte(s)))
}
