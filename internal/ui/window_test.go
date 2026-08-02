package ui_test

// window_test.go gates the mixed-mode window as a PLAYER READS IT, and every
// test in it exists because a person booted asm/8fish.dsk on a real Apple IIe
// and hit the thing it now asserts (2026-08-01, the project's first session of
// real usage feedback):
//
//	"no gap between 'YOU ARE WHITE' and 'WHITE TO MOVE'"
//	"'L' only shows extra info in text mode"
//	"typing S when white immediately makes the computer move and switches to
//	 black; typing S again when black does nothing the first time, then
//	 switches the second"
//
// All three were invisible to the existing suite for the same reason: every
// window gate used strings.Contains, so it could not see WHERE a field landed,
// whether a field was painted at all on the screen being shown, or whether a
// state change said anything. These drive the SHIPPING DISK -- the artefact
// the player was holding -- and assert layout, reachability and feedback.

import (
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/ui"
)

// winKeyBudget is the per-keystroke cycle allowance. Everything here is a
// command or a book move, so this is slack, not a measurement.
const winKeyBudget = 1_500_000_000

// bootedDisk boots the shipping disk to its first keyboard poll, on the board
// with the window under it.
func bootedDisk(t *testing.T) *ui.DiskMachine {
	t.Helper()
	m, err := ui.NewDiskMachine(dskPath(t), ui.RomDir())
	if err != nil {
		t.Skipf("SKIP: no Apple II machine available: %v", err)
	}
	if ok, err := m.RunToKeyboard(600_000_000); err != nil || !ok {
		t.Fatalf("boot: ok=%v err=%v (PC $%04X)", ok, err, m.CPU.PC())
	}
	if !m.Mem.Mixed {
		t.Fatal("the disk did not boot into MIXED mode: there is no window to read")
	}
	return m
}

// TestWindowTitleBarHasAGapBeforeTheStatus.
//
// Window row 20 carries two fields side by side: the forty-column INVERSE
// title bar, whose right-hand end is "YOU ARE WHITE" followed by an inverse
// space, and then whose move it is. Butting the second field against column 40
// puts "WHITE TO MOVE" immediately after the end of a solid white bar, and the
// row reads as YOU ARE WHITEWHITE TO MOVE -- which is exactly how it was
// reported off the hardware.
//
// The fix is a one-cell gutter, and the assertion is written to reject BOTH
// ways of losing it: no gap at all, and a gap made of inverse spaces (which is
// not a gap on a screen, only in a string -- it is more white bar).
func TestWindowTitleBarHasAGapBeforeTheStatus(t *testing.T) {
	m := bootedDisk(t)
	win := m.Window()
	t.Logf("the window at boot:\n%s", win)

	row := win.Text(0)

	// The layout contract, stated as concrete columns: §14.1's title bar owns
	// 0-39, column 40 is the normal-video gutter, and the side-to-move field
	// begins at 41. Asserted first so a re-layout says which column moved.
	if a, inv := ui.Decode(win.Raw[0][40]); a != ' ' || inv {
		t.Errorf("window row %d column 40 is $%02X (%q, inverse=%v); it must be a "+
			"NORMAL-video space -- the one column of breathing room between the "+
			"inverse title bar and the side-to-move field. An INVERSE space is a "+
			"white block: on a IIe it extends the bar rather than separating it",
			ui.Win80Top, win.Raw[0][40], string(rune(a)), inv)
	}
	if !strings.HasPrefix(row[41:], "WHITE TO MOVE") {
		t.Errorf("window row %d does not start the side-to-move field at column 41: %q",
			ui.Win80Top, row[41:])
	}

	// Where does the inverse title bar end? Found from the video attributes,
	// not assumed, so re-wording a field cannot silently move the answer.
	lastInv := -1
	for col := range 80 {
		if _, inv := ui.Decode(win.Raw[0][col]); inv {
			lastInv = col
		}
	}
	if lastInv < 0 {
		t.Fatalf("window row %d has no inverse cells at all; the title bar is inverse "+
			"video:\n%q", ui.Win80Top, win.Text(0))
	}

	// ...and where does the next field start?
	firstText := -1
	for col := lastInv + 1; col < 80; col++ {
		if row[col] != ' ' {
			firstText = col
			break
		}
	}
	if firstText < 0 {
		t.Fatalf("window row %d has nothing after the title bar (ends at column %d):\n%q",
			ui.Win80Top, lastInv, row)
	}
	if firstText < lastInv+2 {
		t.Errorf("window row %d puts %q at column %d with the inverse title bar running "+
			"to column %d: there is NO blank cell between them, so on a IIe the white "+
			"bar runs straight into the text and the row reads as one word.\n%q",
			ui.Win80Top, strings.TrimSpace(row[firstText:min(firstText+13, 80)]), firstText,
			lastInv, row)
	}
	// The gutter has to be NORMAL video. An inverse space is a white block:
	// indistinguishable, on screen, from the title bar continuing.
	for col := lastInv + 1; col < firstText; col++ {
		if _, inv := ui.Decode(win.Raw[0][col]); inv {
			t.Errorf("window row %d column %d is an INVERSE space ($%02X): it is drawn as "+
				"a white block, so it extends the title bar instead of separating it "+
				"from %q", ui.Win80Top, col, win.Raw[0][col], strings.TrimSpace(row[firstText:]))
		}
	}
	t.Logf("title bar: inverse columns 0-%d; gutter columns %d-%d in normal video; "+
		"status field starts at column %d", lastInv, lastInv+1, firstText-1, firstText)

	// And the two fields are still the right two fields.
	if got := strings.TrimSpace(row[:lastInv+1]); !strings.Contains(got, "YOU ARE WHITE") {
		t.Errorf("the title bar reads %q, want it to end with which colour the player has", got)
	}
	if got := strings.TrimSpace(row[firstText:]); !strings.HasPrefix(got, "WHITE TO MOVE") {
		t.Errorf("the field after the title bar reads %q, want WHITE TO MOVE", got)
	}
}

// TestWindowShowsTheLevelPrompt.
//
// `L` sets a message and paints it WITHOUT a full repaint, because it then
// blocks in entkey waiting for the digit. The 40-column message row is row 17,
// and mixed mode shows only rows 20-23 -- so on the board screen the prompt was
// painted onto a screen the player cannot see, and `L` looked like it did
// nothing. (`uiaskpromo`, the promotion prompt, is the other caller and shares
// the fix; it is the same three instructions in uipaintmsg.)
//
// The assertion is EQUALITY between the window's message field and the
// 40-column row that renders it, not a substring: that is the §14.1 invariant
// -- one piece of code per string, the two screens cannot disagree -- and it
// fails if the window ever grows its own copy of the text.
func TestWindowShowsTheLevelPrompt(t *testing.T) {
	m := bootedDisk(t)
	const msgWinRow = 2 // window row 22 carries the 40-column message row

	if got := m.Window().Text(msgWinRow); strings.Contains(got, "LEVEL?") {
		t.Fatalf("the level prompt is up before L was pressed: %q", got)
	}
	if err := m.Enter("l", winKeyBudget); err != nil {
		t.Fatalf("typing L: %v", err)
	}
	win, scr := m.Window(), m.Screen()
	t.Logf("the window while L waits for a digit:\n%s", win)

	if got := scr.Text(17); !strings.Contains(got, "LEVEL?") {
		t.Fatalf("40-column row 17 = %q, want the level prompt: L did not run at all",
			strings.TrimSpace(got))
	}
	got, want := win.Text(msgWinRow)[:40], scr.Text(17)
	if got != want {
		t.Errorf("L prompts on the 40-column screen but NOT in the window under the "+
			"board:\n window row %d, columns 0-39: %q\n 40-column row 17:         %q\n"+
			"mixed mode shows only rows 20-23, so a prompt painted on row 17 alone is "+
			"a prompt the player cannot see", ui.Win80Top+msgWinRow, got, want)
	}
	// The right-hand half of that row must survive the message: the help line
	// is what tells the player L exists in the first place.
	if h := win.Text(msgWinRow)[40:]; !strings.Contains(h, "N-NEW T-TAKEBACK") {
		t.Errorf("the message overwrote the window's first help line: %q", h)
	}

	// And the prompt was LIVE, not just painted: the digit takes effect and
	// the prompt goes away.
	if err := m.Key('1', winKeyBudget); err != nil {
		t.Fatalf("typing the level digit: %v", err)
	}
	win = m.Window()
	if got := win.Text(msgWinRow); strings.Contains(got, "LEVEL?") {
		t.Errorf("the level prompt is still up after the digit: %q", got)
	}
	if got := win.Text(0); !strings.Contains(got, "LEVEL 1") {
		t.Errorf("the window's title bar says %q, want LEVEL 1", strings.TrimSpace(got))
	}
}

// TestSidesKeyAnnouncesAllThreeModes.
//
// `S` is a THREE-way cycle, and two-player mode is the referee mode, where the
// UI validates and displays but never searches. Two things were wrong with it
// on the disk zellyn played, and they compounded:
//
//   - the cycle ran WHITE -> BLACK -> TWO PLAYERS, so the very first press of
//     an unlabelled key was the DESTRUCTIVE one: it handed White to the engine
//     and a move was committed before anything could be reconsidered;
//   - landing in a mode ended in `jmp uiclrmsg`, so it announced nothing. The
//     only evidence of the third state was thirteen characters inside a
//     forty-column inverse title bar, and from the player's chair the press
//     that reached it "did nothing".
//
// The cycle is now WHITE -> TWO PLAYERS -> BLACK -> WHITE. This asserts both
// halves of the fix: the FIRST press is the harmless one (referee mode,
// nothing searches, not a ply added), and every press names the mode it landed
// in AND what the next press does -- which is the property that makes a
// one-key cycle usable at all -- on BOTH screens.
func TestSidesKeyAnnouncesAllThreeModes(t *testing.T) {
	m := bootedDisk(t)
	const msgWinRow = 2

	plies := func() byte { return m.Mem.Main[ui.UIHCNT] }
	if n := plies(); n != 0 {
		t.Fatalf("the game is %d plies old before anything was typed", n)
	}

	for _, step := range []struct {
		press    int
		who      string // the title bar's who-field
		mode     string // what the message must call the mode just entered
		next     string // ...and what it must say the NEXT S does
		searches bool   // does this press hand the move to the engine?
	}{
		{1, "TWO PLAYERS", "TWO PLAYERS", "YOU PLAY BLACK", false},
		{2, "YOU ARE BLACK", "YOU PLAY BLACK", "YOU PLAY WHITE", true},
		{3, "YOU ARE WHITE", "YOU PLAY WHITE", "TWO PLAYERS", true},
	} {
		before := plies()
		if err := m.Enter("s", winKeyBudget); err != nil {
			t.Fatalf("S #%d: %v", step.press, err)
		}
		win, scr := m.Window(), m.Screen()
		t.Logf("after S #%d:\n%s", step.press, win)

		if got := win.Text(0); !strings.Contains(got, step.who) {
			t.Errorf("after S #%d the window's title bar says %q, want %q",
				step.press, strings.TrimSpace(got), step.who)
		}
		msg := strings.TrimSpace(win.Text(msgWinRow)[:40])
		if !strings.Contains(msg, step.mode) {
			t.Errorf("after S #%d the window's message row says %q; it must NAME the "+
				"mode S just entered (%q), or the only thing that changed is thirteen "+
				"characters inside the inverse title bar",
				step.press, msg, step.mode)
		}
		if !strings.Contains(msg, step.next) {
			t.Errorf("after S #%d the message %q does not say what the NEXT S does "+
				"(%q): a one-key three-way cycle whose states do not name their "+
				"successor cannot be discovered from the keyboard",
				step.press, msg, step.next)
		}
		// BOTH screens. The 40-column message row is painted by uipaint40
		// whether or not it is the screen on show, so ESC must not lose the
		// announcement.
		if got := strings.TrimSpace(scr.Text(17)); got != msg {
			t.Errorf("after S #%d the 40-column message row says %q but the window "+
				"says %q: the announcement must be on both screens, from one string",
				step.press, got, msg)
		}

		switch after := plies(); {
		case step.searches && after == before:
			t.Errorf("after S #%d the engine did not move: %d plies before and after",
				step.press, before)
		case !step.searches && after != before:
			t.Errorf("S #%d must be the HARMLESS press -- from the default it selects "+
				"referee mode, where nothing searches -- but the game went from %d "+
				"plies to %d. The first press of an unlabelled key must not commit "+
				"a move", step.press, before, after)
		}
	}

	// Back where we started, and the cycle really was a cycle.
	if got := m.Mem.Main[ui.UIHUMAN]; got != 0 {
		t.Errorf("three presses of S left UIHUMAN = $%02X, want $00 (the human plays "+
			"White): the cycle is not closed", got)
	}
}
