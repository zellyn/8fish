// Package ui drives the on-device 8fish user interface (asm/m8.s, asm/ui.s)
// under the harness emulator: it boots the REAL image the way the real
// machine will, types on its keyboard, and reads its screen back.
//
// The boot is the shipping boot. m8boot.bin is loaded at $0800 and executed;
// it latches $C08B, copies the UI payload from $0900 up to Language Card RAM
// at $E000, installs the engine's LC-resident aux primitives at $D000, and
// jumps to $E000. Nothing about the image is special-cased for the test
// except the keyboard: the HARNESSKBD build of asm/m8.s reads the harness
// input traps instead of $C000/$C010, which is exactly how internal/entropy
// already validates the same entkey code.
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
	"github.com/zellyn/chess6502/internal/chesstest"
)

// Fixed addresses of the boot contract (see asm/m8.cfg and asm/m8.s).
const (
	BootOrg    = 0x0800 // where m8boot.bin is BRUN
	PayloadOrg = 0x0900 // where m8.bin is BLOADed before the copier runs
	EngineOrg  = 0x4000
	LCOrg      = 0xE000 // where the payload runs (its first bytes are JMP m8main)
)

// Machine is a booted 8fish UI plus the state a driving test needs.
type Machine struct {
	M    *harness.Machine
	Defs chesstest.Defs
	Lbl  map[string]uint16 // asm/m8t.lbl: UI label -> address

	// StepLimit caps how many cycles the image may run WITHOUT blocking for
	// input before RunToInput gives up. A stuck image must fail the test,
	// not run for hours: the longest legitimate stretch is one move at the
	// slowest level (60 s = 6.1e7 cycles), so the default leaves a 30x
	// margin and still fails within about a minute of wall time.
	StepLimit uint64
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
// keystroke. book, if non-nil, is injected at $2000 (the resident opening
// book's home).
func Boot(root string, book []byte) (*Machine, error) {
	u, err := load(root, "m8tboot", "m8t", book, true)
	if err != nil {
		return nil, err
	}
	if err := u.RunToInput(); err != nil {
		return nil, err
	}
	return u, nil
}

// BootShipping loads the SHIPPING image — the build that reads the real
// $C000/$C010 keyboard rather than the harness input traps — and runs the
// copier for the given number of cycles. It cannot be typed at (the IIe
// memory model has no keyboard), so it will end up spinning in entkey's
// poll loop; what it proves is that the image that goes on the disk boots,
// installs itself in Language Card RAM, paints, and then waits.
func BootShipping(root string, cycles uint64) (*Machine, error) {
	u, err := load(root, "m8boot", "m8", nil, false)
	if err != nil {
		return nil, err
	}
	if _, _, err := u.M.Run(cycles); err != nil {
		return nil, err
	}
	return u, nil
}

// load builds the machine and lays the three images out in memory. traps
// selects whether the harness input traps are wired up at all: the shipping
// build reads $C000/$C010, so leaving them on would be a lie.
func load(root, bootName, payloadName string, book []byte, traps bool) (*Machine, error) {
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
	if traps {
		cfg.InAddr, cfg.InStatusAddr = 0xBFF1, 0xBFF2
	}
	m, err := harness.New(cfg)
	if err != nil {
		return nil, err
	}
	copy(m.Mem.Main[BootOrg:], boot)
	copy(m.Mem.Main[PayloadOrg:], payload)
	if book != nil {
		copy(m.Mem.Main[0x2000:], book)
	}
	return &Machine{M: m, Defs: defs, Lbl: lbl, StepLimit: DefaultStepLimit}, nil
}

// ErrExited reports that the image hit the exit trap (the Q command).
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
	u.M.SendInput([]byte{c})
	return u.RunToInput()
}

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
	UIHFROM  = 0xF800
	UIHTO    = 0xF900
	UIHFLAG  = 0xFA00
	UIHASH0  = 0xFB00
)

// Game-over codes (RES_* in asm/ui.s).
const (
	ResNone   = 0
	ResMate   = 1
	ResStale  = 2
	Res50     = 3
	ResRep    = 4
	ResResign = 5
	ResAgreed = 6
	ResLong   = 7
	ResErr    = 8
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
	case ResLong:
		return "draw: too long"
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
// command cycles WHITE -> BLACK -> TWO PLAYERS. Passing through BLACK hands
// the move to the engine, which replies immediately, so this finishes with a
// new game.
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
