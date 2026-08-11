// Package ui drives the on-device 8fish user interface (asm/m8.s, asm/ui.s)
// under the harness emulator: it boots the REAL image the way the real
// machine will, types on its keyboard, and reads its screen back.
//
// The boot is the shipping boot. m8boot.bin is loaded at $0800 and executed;
// it latches $C08B, copies the UI payload from $0900 up to Language Card RAM
// at $E000, installs the engine's LC-resident aux primitives at $D000, and
// jumps to $E000.
//
// KEYBOARD. Two images, two ways in, and both are driven from here:
//
//	Boot          the HARNESSKBD build (asm/m8t.bin), which reads the
//	              harness input traps at $BFF1/$BFF2 — the same way
//	              internal/entropy validates the same entkey code.
//	BootShipping  the SHIPPING build (asm/m8.bin), which polls $C000 for a
//	              key with bit 7 set and clears the strobe at $C010,
//	              through goapple2/iie's modelled IIe keyboard. This is the
//	              image that goes on the disk.
//
// Machine.Key/Type/Enter work identically on both, so every gate can be run
// against either; internal/ui/shipping_test.go runs the two in lockstep and
// requires byte-identical screens.
//
// CLOCK. The machine is built with the harness clock trap DISABLED, because
// that is the hardware truth: on an Apple IIe $BFF4 is plain RAM, and with
// FT2_SOFTCLK set it holds the engine's own ESTIMATED elapsed-cycle
// accumulator. Leaving the trap on would let the harness's real counter win
// every read and the tests would measure something the shipped machine can
// never do.
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zellyn/chess6502/harness"
	bookpkg "github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/tiles"
)

// Fixed addresses of the boot contract (see asm/m8.cfg and asm/m8.s).
const (
	BootOrg    = 0x0800 // where m8boot.bin is BRUN
	PayloadOrg = 0x0900 // where m8.bin is BLOADed before the copier runs
	EngineOrg  = 0x4000
	LCOrg      = 0xE000 // where the payload runs (its first bytes are JMP m8main)
	// TilesLC is where the DHGR artwork is resident: Language Card bank 1,
	// above the engine's LCCODE at $D000. Keep equal to delivery.TilesLC and
	// asm/m8.s's DHTILES.
	TilesLC = 0xD300
	// NamesLC is where the book's NAME TABLE is resident: Language Card
	// BANK 2, $D000. Keep equal to delivery.NamesLC, book.NamesAddr and
	// asm/book.inc's BOOK_NAMES.
	NamesLC = 0xD000
)

// Machine is a booted 8fish UI plus the state a driving test needs.
type Machine struct {
	M    *harness.Machine
	Defs chesstest.Defs
	Lbl  map[string]uint16 // asm/m8t.lbl: UI label -> address

	// RealKbd is set for the SHIPPING image: keystrokes go into the IIe
	// keyboard latch at $C000 and the image clears the strobe at $C010,
	// instead of the harness input traps the HARNESSKBD build reads.
	RealKbd bool

	// StepLimit caps how many cycles the image may run WITHOUT blocking for
	// input before RunToInput gives up. A stuck image must fail the test,
	// not run for hours: the longest legitimate stretch is one move at the
	// slowest level (60 s = 6.1e7 cycles), so the default leaves a 30x
	// margin and still fails within about a minute of wall time.
	StepLimit uint64

	// PonderDefault is the value m8main left in PONDERON at boot — 1 on the
	// shipped image, which PONDERS. Boot captures it and then pokes PONDERON
	// off, because the harness CANNOT run pondering: it breaks Run the instant
	// the program reads the keyboard-status trap with no key pending
	// (harness/trapmemory.go), which is exactly what pkclk's non-blocking
	// ponder poll does, so a ponder would park the machine mid-search on every
	// human turn. Real hardware just reads $C000 and keeps searching. The
	// pondering feature is exercised by internal/ui/ponder_test.go, which
	// re-enables PONDERON and drives it with the poll trap disabled.
	PonderDefault byte
}

// DefaultStepLimit is Machine.StepLimit's default: ~2,000 emulated seconds.
const DefaultStepLimit = 2_000_000_000

// Screen is a snapshot of text page 1.
type Screen struct {
	Raw [24][40]byte
}

// RowBase is the Apple II interleaved text-page-1 row base.
func RowBase(row int) uint16 {
	return 0x400 + uint16(row%8)*0x80 + uint16(row/8)*0x28
}

// Decode converts an Apple IIe text-screen byte to its ASCII character plus
// an "inverse video" flag.
//
//	$80-$FF  normal video, ASCII = b & $7F (lowercase lives at $E0-$FF)
//	$60-$7F  inverse lowercase (IIe alternate character set), ASCII = b
//	$40-$5F  flashing uppercase — unused by this UI
//	$20-$3F  inverse space/punctuation/digits, ASCII = b
//	$00-$1F  inverse @ A-Z [ \ ] ^ _, ASCII = b | $40
func Decode(b byte) (ascii byte, inverse bool) {
	switch {
	case b >= 0x80:
		return b & 0x7F, false
	case b >= 0x60:
		return b, true
	case b >= 0x40:
		return b & 0x3F, false
	case b >= 0x20:
		return b, true
	default:
		return b | 0x40, true
	}
}

// Text returns one screen row as ASCII.
func (s *Screen) Text(row int) string {
	var sb strings.Builder
	for col := range 40 {
		a, _ := Decode(s.Raw[row][col])
		sb.WriteByte(a)
	}
	return sb.String()
}

// Win80Top is the first text row of the mixed-mode window: with MIXED set,
// the IIe shows graphics on scanlines 0-159 and text rows 20-23 below them.
// Mirrors asm/m8.s's DHTXTTOP.
const Win80Top = 20

// Win80Rows is how many rows that window has.
const Win80Rows = 4

// Window80 is a snapshot of the mixed-mode text window: four rows of EIGHTY
// columns, de-interleaved from the two banks.
//
// With 80COL on the video scanner fetches two bytes per column position, aux
// first: for row base B and 0 <= i < 40, aux B+i is screen column 2i and main
// B+i is column 2i+1. Reading it back therefore needs BOTH banks, which is
// why this is not just Screen() with a wider loop -- a test that read only
// main would see every other character and call it corruption.
type Window80 struct {
	Raw [Win80Rows][80]byte
}

func window80(main, aux *[0x10000]byte) *Window80 {
	var w Window80
	for r := range Win80Rows {
		base := RowBase(Win80Top + r)
		for i := range 40 {
			w.Raw[r][2*i] = aux[base+uint16(i)]
			w.Raw[r][2*i+1] = main[base+uint16(i)]
		}
	}
	return &w
}

// Text returns one window row as ASCII.
func (w *Window80) Text(row int) string {
	var sb strings.Builder
	for col := range 80 {
		a, _ := Decode(w.Raw[row][col])
		sb.WriteByte(a)
	}
	return sb.String()
}

// String renders the window the way a IIe would show it under the board.
func (w *Window80) String() string {
	var sb strings.Builder
	sb.WriteString("    +" + strings.Repeat("-", 80) + "+\n")
	for row := range Win80Rows {
		fmt.Fprintf(&sb, "%2d  |%s|\n", Win80Top+row, w.Text(row))
	}
	sb.WriteString("    +" + strings.Repeat("-", 80) + "+")
	return sb.String()
}

// Video renders one row's inverse/normal attributes as a bar.
func (s *Screen) Video(row int) string {
	var sb strings.Builder
	for col := range 40 {
		if _, inv := Decode(s.Raw[row][col]); inv {
			sb.WriteByte('#')
		} else {
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

// String renders the whole screen the way a IIe would show it.
func (s *Screen) String() string {
	var sb strings.Builder
	sb.WriteString("    +----------------------------------------+\n")
	for row := range 24 {
		fmt.Fprintf(&sb, "%2d  |%s|\n", row, s.Text(row))
	}
	sb.WriteString("    +----------------------------------------+")
	return sb.String()
}

// ParseLbl reads an ld65 -Ln label file into a name -> address map.
func ParseLbl(path string) (map[string]uint16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]uint16{}
	for line := range strings.SplitSeq(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[0] != "al" || !strings.HasPrefix(f[2], ".") {
			continue
		}
		var v uint32
		if _, err := fmt.Sscanf(f[1], "%x", &v); err != nil {
			continue
		}
		out[f[2][1:]] = uint16(v)
	}
	return out, nil
}

// Boot loads engine.bin, m8tboot.bin and m8t.bin and runs the copier, so the
// returned Machine is sitting in the UI's main loop waiting for its first
// keystroke. book, if non-nil, is the ENTRIES blob, injected at main $2000 --
// where stage 2 lands it, not where it ends up. The book's NAME TABLE is
// always installed (in Language Card bank 2), because it is embedded and
// costs nothing; see load.
func Boot(root string, book []byte) (*Machine, error) {
	u, err := load(root, "m8tboot", "m8t", false)
	if err != nil {
		return nil, err
	}
	if err := u.stageBook(book); err != nil {
		return nil, err
	}
	if err := u.RunToInput(); err != nil {
		return nil, err
	}
	u.disableHarnessPonder()
	return u, nil
}

// stageBook lands the opening book's ENTRIES blob at main $2000 — where
// stage 2 of the disk's chain load puts it — AFTER running the boot far
// enough for the copier to have lifted the UI payload to $E000 (the
// payload's entry point, the copier's final jump target).
//
// It used to be a load-time copy, back when the staged payload was
// guaranteed to end below $2000. The arrow-key cursor entry (2026-08-08)
// pushed m8.bin past that line, and the DELIBERATE resolution (see
// TestUIByteBudget) was to let it: the shipping DISK was never exposed —
// its chain load has always lifted the payload BEFORE stage 2 lands the
// book at $2000 — and this method makes the harness reproduce that exact
// ordering instead of an ordering no delivery uses any more. What was
// retired is only the on-device convenience of BLOADing a book at $2000
// and THEN BRUNning the copier over a payload that overlaps it.
func (u *Machine) stageBook(book []byte) error {
	if book == nil {
		return nil
	}
	const budget = 10_000_000 // the copier costs ~100k cycles; 10 s of margin
	start := u.M.Cycles
	for u.M.CPU.PC() != LCOrg {
		if err := u.M.CPU.Step(); err != nil {
			return err
		}
		if u.M.Cycles-start > budget {
			return fmt.Errorf("the copier never reached the payload entry at $%04X "+
				"(PC $%04X after %d cycles)", LCOrg, u.M.CPU.PC(), u.M.Cycles-start)
		}
	}
	copy(u.M.Mem.Main[0x2000:], book)
	return nil
}

// disableHarnessPonder records the shipped PONDERON default and turns
// pondering off for the harness (see the PonderDefault field). At boot the
// game has not started (UIHCNT=0, so m8ponder took its first-move skip and the
// machine is parked at the prompt), which is the one clean point to read the
// default before it matters.
func (u *Machine) disableHarnessPonder() {
	u.PonderDefault = u.Peek(PONDERON)
	u.Poke(PONDERON, 0)
}

// BootShipping loads the SHIPPING image — the build that reads the real
// $C000/$C010 keyboard rather than the harness input traps — and runs it to
// its first keyboard poll. The returned Machine types exactly like the
// HARNESSKBD one: Key/Type/Enter put the keystroke in the modelled IIe
// keyboard latch, and the image reads it with the same $C000 poll and
// $C010 strobe clear it will execute on hardware.
//
// This is the build that goes on the disk. Everything the HARNESSKBD gates
// prove about the UI, this one has to prove about the artefact.
func BootShipping(root string, book []byte) (*Machine, error) {
	u, err := load(root, "m8boot", "m8", true)
	if err != nil {
		return nil, err
	}
	if err := u.stageBook(book); err != nil {
		return nil, err
	}
	if err := u.RunToInput(); err != nil {
		return nil, err
	}
	u.disableHarnessPonder()
	return u, nil
}

// load builds the machine and lays the three images out in memory.
// realKbd selects the keyboard the image will read: the shipping build goes
// through the modelled IIe keyboard at $C000/$C010 and the harness input
// traps are left unwired, because wiring them for an image that never reads
// them would be a lie. The opening book is NOT placed here — the staged
// payload may legally overlap $2000 now — see stageBook.
func load(root, bootName, payloadName string, realKbd bool) (*Machine, error) {
	engine, err := os.ReadFile(filepath.Join(root, "asm", "engine.bin"))
	if err != nil {
		return nil, err
	}
	boot, err := os.ReadFile(filepath.Join(root, "asm", bootName+".bin"))
	if err != nil {
		return nil, err
	}
	payload, err := os.ReadFile(filepath.Join(root, "asm", payloadName+".bin"))
	if err != nil {
		return nil, err
	}
	defs, err := chesstest.ParseDefs(filepath.Join(root, "asm", "defs.inc"))
	if err != nil {
		return nil, err
	}
	lbl, err := ParseLbl(filepath.Join(root, "asm", payloadName+".lbl"))
	if err != nil {
		return nil, err
	}

	cfg := harness.Config{
		Bin: engine, Org: EngineOrg, Entry: BootOrg,
		CoutAddr: 0xBFF0,
		ExitAddr: 0xBFFF,
		// ClockAddr deliberately 0: on hardware $BFF4 is plain RAM and,
		// with FT2_SOFTCLK, it IS the engine's estimated-cycle
		// accumulator. See the package comment.
		ClockAddr: 0,
	}
	if realKbd {
		cfg.RealKeyboard = true
	} else {
		cfg.InAddr, cfg.InStatusAddr = 0xBFF1, 0xBFF2
	}
	m, err := harness.New(cfg)
	if err != nil {
		return nil, err
	}
	copy(m.Mem.Main[BootOrg:], boot)
	copy(m.Mem.Main[PayloadOrg:], payload)
	// The DHGR artwork, where the disk's copier leaves it: Language Card
	// bank 1, which in goapple2's model is Main[$D000-$DFFF]. Without this
	// the BLOAD path would paint the board out of whatever happened to be
	// there, which is a difference between this rehearsal and the disk for
	// no reason -- the blob is embedded and costs nothing to install.
	copy(m.Mem.Main[TilesLC:], tiles.DefaultBlob())
	// The book's NAME TABLE, where the disk's copier leaves it: Language Card
	// BANK 2, which in goapple2's model is the separate MainD000Bank2 array
	// (ALTZP is off, so it is the MAIN bank-2 image). Same reasoning as the
	// artwork: stage 2 delivers it on the disk, only the SDCHAIN copier does
	// that copy, and installing the embedded bytes here keeps the BLOAD
	// rehearsal reading the same names the disk will.
	copy(m.Mem.MainD000Bank2[NamesLC-0xD000:], bookpkg.DefaultNames())
	return &Machine{M: m, Defs: defs, Lbl: lbl, StepLimit: DefaultStepLimit, RealKbd: realKbd}, nil
}

// ErrExited reports that the image hit the harness exit trap at $BFFF.
// Nothing in the shipping UI stores there any more — Q cold boots through
// the ROM's RESET vector (asm/m8.s cmd_quit), because on hardware $BFFF is
// plain RAM — so this now means the image went somewhere it should not have.
type ErrExited struct{ Code byte }

func (e *ErrExited) Error() string { return fmt.Sprintf("image exited with code %d", e.Code) }

// RunToInput runs until the UI blocks polling the keyboard with nothing
// pending, which is the only point at which its state is quiescent.
func (u *Machine) RunToInput() error {
	const slice = 1 << 26
	deadline := u.M.Cycles + u.StepLimit
	for {
		exited, code, err := u.M.Run(slice)
		if err != nil {
			return err
		}
		if exited {
			return &ErrExited{Code: code}
		}
		if u.M.WaitingForInput() {
			return nil
		}
		if u.M.Cycles > deadline {
			return fmt.Errorf("ran %d cycles (%.0f emulated seconds) without blocking for input",
				u.StepLimit, float64(u.StepLimit)/1020484)
		}
	}
}

// Key sends one keystroke and runs until the UI blocks for the next one.
func (u *Machine) Key(c byte) error {
	if u.RealKbd {
		u.M.SendKey(c)
	} else {
		u.M.SendInput([]byte{c})
	}
	return u.RunToInput()
}

// Spin runs the image for a while with NO key arriving, so a test can watch
// what the keyboard wait does while it waits. The "blocked for input" break
// is suppressed for the duration; nothing else changes.
func (u *Machine) Spin(cycles uint64) error {
	saved := u.M.Mem.RealKeyboard
	u.M.Mem.RealKeyboard = false
	defer func() { u.M.Mem.RealKeyboard = saved }()
	_, _, err := u.M.Run(cycles)
	return err
}

// AltCharset reports whether the image has selected the IIe's ALTERNATE
// character set. It has to be on, or the $60-$7F bytes the board uses for
// black pieces on dark squares are flashing punctuation rather than inverse
// lowercase (goapple2/chargen documents both sets).
func (u *Machine) AltCharset() bool { return u.M.Mem.AltCharset }

// Type sends a string of keystrokes one at a time, letting the machine run
// between each — the same way a human types, and the same way the entropy
// collector expects to see them (asm/entropy.inc: the arrival time of each
// key is the seed material).
func (u *Machine) Type(s string) error {
	for i := range len(s) {
		if err := u.Key(s[i]); err != nil {
			return err
		}
	}
	return nil
}

// Enter types a line and the RETURN that submits it.
func (u *Machine) Enter(s string) error {
	if err := u.Type(s); err != nil {
		return err
	}
	return u.Key(0x0D)
}

// Screen snapshots text page 1.
func (u *Machine) Screen() *Screen {
	var s Screen
	for row := range 24 {
		base := RowBase(row)
		for col := range 40 {
			s.Raw[row][col] = u.M.Mem.Main[base+uint16(col)]
		}
	}
	return &s
}

// Window snapshots the mixed-mode 80-column text window out of both banks.
func (u *Machine) Window() *Window80 {
	return window80(&u.M.Mem.Main, &u.M.Mem.Aux)
}

// Peek reads a MAIN byte (including Language Card RAM at $D000-$FFFF, which
// is plain Main[] in the IIe model with ALTZP off).
func (u *Machine) Peek(addr uint16) byte { return u.M.Mem.Main[addr] }

// Poke writes a MAIN byte.
func (u *Machine) Poke(addr uint16, v byte) { u.M.Mem.Main[addr] = v }

// UI variable addresses (asm/ui.s M8VARS block). Mirrored here rather than
// parsed, because they are equates, not labels: ca65 does not emit them.
const (
	UIHCNT   = 0xF700
	UILEVEL  = 0xF701
	UIHUMAN  = 0xF702
	UIRESULT = 0xF703
	UINLEGAL = 0xF704
	UICHK    = 0xF705
	UIHFULL  = 0xF7AA
	// Pondering state (asm/ui.s). PONDERON is the shipped enable flag
	// (m8main defaults it to 1); the move-by-move test harness pokes it to 0
	// because it does not model opponent think-time, so a ponder would run to
	// its walk-away backstop every turn. The dedicated ponder gate re-enables
	// it. PPFROM/PPTO are the device's predicted reply P (for cross-checking
	// against the Go predictor).
	PONDERON  = 0xF7AC
	PONDERING = 0xF7AD
	PONDERKEY = 0xF7AE
	PPFROM    = 0xF7AF
	PPTO      = 0xF7B0
	PPFLAGS   = 0xF7B1
	// PONDEROK gates uiread's between-keystroke ponder bursts (asm/m8.s
	// urdkey): mloop sets it only when the human is on move in a live game,
	// and the ESC screen-swap clears it for the rest of the turn.
	PONDEROK = 0xF7EC
	// Arrow-key cursor entry (asm/m8.s curpop and friends).
	CURACT  = 0xF7B6 // nonzero: the cursor is up on the board
	CURSQ   = 0xF7B7 // the cursor's 0x88 square
	CURFROM = 0xF7B8 // latched FROM square ($FF = none)
	// The big book and its ProRWTS2 reader (asm/m8.s m8bigbook,
	// docs/prorwts2-design.md). BIGBOOKOK is the one-way in-book latch;
	// RWTSHOLD is the zp-swap image (44 bytes, $F7BF-$F7EA).
	BIGBOOKOK = 0xF7BB
	RWTSHOLD  = 0xF7BF
	UITHINK   = 0xF730 // think line ($00 = blank; a ponder must never paint it)
	UIHFROM   = 0xF800
	UIHTO     = 0xF900
	UIHFLAG   = 0xFA00
	UIHASH0   = 0xFB00
)

// Game-over codes (RES_* in asm/ui.s). There is deliberately no "too long"
// code: a game is never over because of its length (docs/results.md
// 2026-07-28). Past ply 255 the UI stops RECORDING and keeps refereeing —
// UIHFULL is the flag that says so.
const (
	ResNone   = 0
	ResMate   = 1
	ResStale  = 2
	Res50     = 3
	ResRep    = 4
	ResResign = 5
	ResAgreed = 6
	ResErr    = 7
)

// ResultName renders a UIRESULT code.
func ResultName(r byte) string {
	switch r {
	case ResNone:
		return "playing"
	case ResMate:
		return "checkmate"
	case ResStale:
		return "stalemate"
	case Res50:
		return "draw: 50 moves"
	case ResRep:
		return "draw: repetition"
	case ResResign:
		return "resigned"
	case ResAgreed:
		return "draw agreed"
	case ResErr:
		return "internal error"
	}
	return fmt.Sprintf("result %d", r)
}

// History returns the game's moves in UCI form, straight out of the UI's own
// three parallel history arrays.
func (u *Machine) History() []string {
	n := int(u.Peek(UIHCNT))
	out := make([]string, n)
	for i := range n {
		out[i] = chesstest.MoveUCI(
			u.Peek(UIHFROM+uint16(i)),
			u.Peek(UIHTO+uint16(i)),
			u.Peek(UIHFLAG+uint16(i)))
	}
	return out
}

// TwoPlayer puts the UI in referee mode, where it never searches: the "S"
// command cycles WHITE -> TWO PLAYERS -> BLACK, so from the default it is ONE
// press and nothing has moved -- that ordering is the point of the cycle (the
// harmless state first; see asm/m8.s cmd_swap). The loop and the closing new
// game are kept because the caller may not be starting from the default, and
// reaching TWO PLAYERS from BLACK does pass through a press that hands the
// move to the engine.
func (u *Machine) TwoPlayer() error {
	for range 3 {
		if u.Peek(UIHUMAN) == 0xFF {
			return u.Enter("n")
		}
		if err := u.Enter("s"); err != nil {
			return err
		}
	}
	return fmt.Errorf("could not reach two-player mode (UIHUMAN = %#02x)", u.Peek(UIHUMAN))
}

// ---------------------------------------------------------------------------
// Position access: read the position the UI is showing, and (for tests that
// need to reach a position the opening moves cannot) install one.
// ---------------------------------------------------------------------------

// FEN renders the position currently on the UI's board as a five-field FEN
// (the fullmove number is always 1: the UI does not track it). Reading it
// straight out of the engine's own BOARD / SIDE / CASTLE / EPSQ / HALFMOVE
// is what makes a ply-by-ply cross-check against refchess possible.
func (u *Machine) FEN() string {
	board := u.Defs["BOARD"]
	var sb strings.Builder
	for rank := 7; rank >= 0; rank-- {
		empty := 0
		for file := range 8 {
			pc := u.Peek(board+uint16(rank*16+file)) & 0x0F
			if pc == 0 {
				empty++
				continue
			}
			if empty > 0 {
				sb.WriteByte(byte('0' + empty))
				empty = 0
			}
			c := " pnbrqk"[pc&7]
			if pc&8 == 0 {
				c -= 'a' - 'A'
			}
			sb.WriteByte(c)
		}
		if empty > 0 {
			sb.WriteByte(byte('0' + empty))
		}
		if rank > 0 {
			sb.WriteByte('/')
		}
	}
	if u.Peek(u.Defs["SIDE"]) == 0 {
		sb.WriteString(" w ")
	} else {
		sb.WriteString(" b ")
	}
	castle := u.Peek(u.Defs["CASTLE"])
	if castle == 0 {
		sb.WriteByte('-')
	}
	for i, c := range []byte{'K', 'Q', 'k', 'q'} {
		if castle&(1<<uint(i)) != 0 {
			sb.WriteByte(c)
		}
	}
	sb.WriteByte(' ')
	if ep := u.Peek(u.Defs["EPSQ"]); ep == 0xFF {
		sb.WriteByte('-')
	} else {
		sb.WriteByte('a' + ep&0x0F)
		sb.WriteByte('1' + ep>>4)
	}
	fmt.Fprintf(&sb, " %d 1", u.Peek(u.Defs["HALFMOVE"]))
	return sb.String()
}

// SetFEN installs a position under the UI, resetting the game history, and
// presses RETURN (an empty line, which the UI treats as "nothing typed") so
// the main loop re-derives everything — legality, check, game-over state —
// and repaints. This is the test hook for positions no legal opening reaches:
// stalemates, en-passant windows, promotion races, 50-move endgames.
func (u *Machine) SetFEN(fen string) error {
	pos, err := chesstest.ParseFEN(fen)
	if err != nil {
		return err
	}
	board := u.Defs["BOARD"]
	for rank := range uint16(8) {
		base := rank * 16
		// Only the 64 real squares: the 0x88 board's off-board bytes are
		// engine scratch (the contract at BOARD in defs.inc).
		for file := range uint16(8) {
			u.Poke(board+base+file, pos.Board[base+file])
		}
	}
	psq := u.Defs["PIECESQ"]
	for i := range uint16(32) {
		u.Poke(psq+i, pos.PieceSq[i])
	}
	u.Poke(u.Defs["SIDE"], pos.Side)
	u.Poke(u.Defs["EPSQ"], pos.EpSq)
	u.Poke(u.Defs["CASTLE"], pos.Castle)
	u.Poke(u.Defs["HALFMOVE"], pos.Halfmove)
	u.Poke(UIHCNT, 0)
	u.Poke(UIRESULT, 0)
	if err := u.Key(0x0D); err != nil { // empty line: re-derive and repaint
		return err
	}
	// The main loop's evalinit has now built HASH0-3 for the poked position;
	// seed the game's hash history with it, exactly as the UI's own new-game
	// path does. Without this the repetition scan would compare against the
	// PREVIOUS game's entry 0.
	for i := range uint16(4) {
		u.Poke(UIHASH0+i*0x100, u.Peek(u.Defs["HASH0"]+i))
	}
	return nil
}

// Position reads the UI's position out of the machine as the exact bytes the
// engine sees — board, piece list, side, ep, castling, halfmove clock.
//
// This is stronger than round-tripping through a FEN. The engine's move
// generator walks the PIECE LIST, so two encodings of the same position that
// assign piece-list slots differently produce different (equally correct)
// move orders and therefore different search trees. A parity test that wants
// to isolate the DRIVER has to hand the reference engine the same bytes, not
// the same FEN.
func (u *Machine) Position() *chesstest.Position {
	pos := &chesstest.Position{}
	board := u.Defs["BOARD"]
	for rank := range uint16(8) {
		base := rank * 16
		for file := range uint16(8) {
			pos.Board[base+file] = u.Peek(board + base + file)
		}
	}
	psq := u.Defs["PIECESQ"]
	for i := range uint16(32) {
		pos.PieceSq[i] = u.Peek(psq + i)
	}
	pos.Side = u.Peek(u.Defs["SIDE"])
	pos.EpSq = u.Peek(u.Defs["EPSQ"])
	pos.Castle = u.Peek(u.Defs["CASTLE"])
	pos.Halfmove = u.Peek(u.Defs["HALFMOVE"])
	return pos
}
