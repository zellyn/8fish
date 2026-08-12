// Package saveload is the single account of the 8fish SAVED-GAME RECORD:
// the 1,024-byte (2-block) image the on-device save command writes IN PLACE
// over the SAVE file's blocks, and the load command replays from
// (docs/prorwts2-design.md §4, docs/saveload-feasibility.md).
//
// A saved game is NOT a serialized position. It is the game's move history —
// the same three parallel arrays takeback already replays through — plus the
// handful of scalars needed to restore whose game it was. Load = validate,
// copy the arrays back, and REPLAY every ply through the same
// uifind/uitrylegal/uiapply machinery the live game uses, so a loaded
// position is by construction one the referee reached; a corrupted record
// fails the checksum, and a hand-crafted one fails per-ply legality.
//
// The layout is page-aligned so the 6502 side copies each array with one
// absolute-indexed loop (asm/saveload.s must stay byte-compatible with this
// file; the round-trip gate compares the two encoders' output):
//
//	$000-$00F  header: '8','F', version, plyCount, human, level, result,
//	           win, 6 reserved bytes, checksum16 (LE, at $00E)
//	$010-$0FF  reserved (0)
//	$100-$1FE  from-square per ply (255 entries; $1FF reserved 0)
//	$200-$2FE  to-square per ply
//	$300-$3FE  generator flags per ply
//
// The checksum is the 16-bit byte sum (mod 65536) over every byte EXCEPT its
// own two — the same primitive as the big book's trailer (asm/m8.s mbbsum).
package saveload

// Record geometry. MaxPlies is 255, matching the UI's one-page history
// arrays: past ply 255 the live game stops RECORDING (asm/m8.s uiapply,
// UIHFULL), and a save records what the history holds.
const (
	RecordBytes = 1024 // 2 ProDOS blocks: the SAVE file, overwritten in place
	MaxPlies    = 255

	OffMagic    = 0x000 // '8','F'
	OffVersion  = 0x002 // Version
	OffPlyCount = 0x003 // plies recorded (0 = empty slot)
	OffHuman    = 0x004 // UIHUMAN: 0 white, $80 black, $FF two players
	OffLevel    = 0x005 // UILEVEL: 1..9
	OffResult   = 0x006 // UIRESULT (only RES_RESIGN/RES_AGREED are restored;
	// the rest are re-derived by uisync after replay)
	OffWin      = 0x007 // UIWIN: the winning side for a latched result
	OffChecksum = 0x00E // 16-bit LE byte sum over all bytes but these two
	OffFrom     = 0x100
	OffTo       = 0x200
	OffFlag     = 0x300

	Magic0  = '8'
	Magic1  = 'F'
	Version = 1
)

// DiskFileName is the SAVE file's name in the ProDOS-shaped directory (and
// in asm/saveload.s's request).
const DiskFileName = "SAVE"

// WriterFileName is the transient write-capable driver's file (the blob the
// resident reader pulls to main $0E00 when a save is requested).
const WriterFileName = "RWTSW"

// CodeFileName is the save/load ORCHESTRATOR's file: the on-demand payload
// carrying the save/load logic itself, loaded to main $1A00 by the resident
// reader (docs/saveload-feasibility.md §6 — UICODE is full, so the logic is
// transient too).
const CodeFileName = "SAVELOAD"

// Checksum16 is the record's checksum primitive: the 16-bit byte sum over
// everything except the checksum's own two bytes. Byte-identical to the
// asm/saveload.s loop (and the same primitive as the big book's mbbsum).
func Checksum16(rec []byte) uint16 {
	var sum uint16
	for i, b := range rec {
		if i == OffChecksum || i == OffChecksum+1 {
			continue
		}
		sum += uint16(b)
	}
	return sum
}

// seal writes the checksum into a record.
func seal(rec []byte) []byte {
	sum := Checksum16(rec)
	rec[OffChecksum] = byte(sum)
	rec[OffChecksum+1] = byte(sum >> 8)
	return rec
}

// Empty returns the placeholder record the disk ships in the SAVE file: a
// valid-checksum record with plyCount 0, which the load command reports as
// "no saved game" rather than an error.
func Empty() []byte {
	rec := make([]byte, RecordBytes)
	rec[OffMagic] = Magic0
	rec[OffMagic+1] = Magic1
	rec[OffVersion] = Version
	return seal(rec)
}

// Ply is one recorded half-move: the from/to squares (0x88) and the
// generator's flags, exactly the bytes UIHFROM/UIHTO/UIHFLAG hold.
type Ply struct{ From, To, Flag byte }

// Encode builds the record the on-device save command would write for this
// game state. It is the Go twin of asm/saveload.s's record builder; the
// round-trip gate compares the machine's written blocks against this.
func Encode(plies []Ply, human, level, result, win byte) []byte {
	if len(plies) > MaxPlies {
		plies = plies[:MaxPlies]
	}
	rec := make([]byte, RecordBytes)
	rec[OffMagic] = Magic0
	rec[OffMagic+1] = Magic1
	rec[OffVersion] = Version
	rec[OffPlyCount] = byte(len(plies))
	rec[OffHuman] = human
	rec[OffLevel] = level
	rec[OffResult] = result
	rec[OffWin] = win
	for i, p := range plies {
		rec[OffFrom+i] = p.From
		rec[OffTo+i] = p.To
		rec[OffFlag+i] = p.Flag
	}
	return seal(rec)
}

// Decode validates a record and returns its plies and scalars. It mirrors
// the checks the on-device load command performs (magic, version, checksum,
// ply count) — the harness's failure-path gates use the DISAGREEMENTS
// (corrupt a record, expect the same refusal on-device).
func Decode(rec []byte) (plies []Ply, human, level, result, win byte, ok bool) {
	if len(rec) != RecordBytes ||
		rec[OffMagic] != Magic0 || rec[OffMagic+1] != Magic1 ||
		rec[OffVersion] != Version {
		return nil, 0, 0, 0, 0, false
	}
	sum := Checksum16(rec)
	if rec[OffChecksum] != byte(sum) || rec[OffChecksum+1] != byte(sum>>8) {
		return nil, 0, 0, 0, 0, false
	}
	n := int(rec[OffPlyCount])
	for i := range n {
		plies = append(plies, Ply{rec[OffFrom+i], rec[OffTo+i], rec[OffFlag+i]})
	}
	return plies, rec[OffHuman], rec[OffLevel], rec[OffResult], rec[OffWin], true
}
