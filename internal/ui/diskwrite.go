package ui

// diskwrite.go is the WRITE half of the disk harness (docs/prorwts2-design.md
// Feature 2, docs/saveload-feasibility.md §7): a writable drive for the
// booted DiskMachine, and a sector extractor for what the machine wrote.
//
// Two properties of goapple2's stock pieces force local code, and both are
// documented in the feasibility doc:
//
//   - disk.Nybble's writeable flag is private and always false, so a loaded
//     .dsk is write-protected (ProRWTS2's detect_wp would dutifully report
//     it). WritableDisk is a ~30-line cards.Disk over the same nibblised
//     tracks with writes enabled. (A SetWriteable upstream in goapple2 is
//     the better long-term home for this.)
//
//   - the emulated medium only advances on data-register access, so the
//     driver's timed post-address delay does not move the disk the way real
//     rotation does: the write splice lands ~5 nibbles EARLY, overwriting
//     the address field's checksum pair and epilogue with the leading sync
//     bytes. Harmless on hardware and to the driver itself (its readadr
//     never reads address checksums), but goapple2's readOneSector VERIFIES
//     the address checksum and would refuse every sector the machine wrote.
//     ExtractSectors is the tolerant twin: address checksums are not
//     enforced; DATA checksums still are.
//
// The disk-integrity gate is built ON these: extract every sector before
// and after a save and require that only the SAVE file's data sectors
// changed. The tolerance cannot mask corruption — sector DATA is still
// checksum-verified and byte-compared.

import (
	"fmt"

	"github.com/zellyn/goapple2/disk"
)

// WritableDisk is a cards.Disk over nibblised tracks with writes ENABLED —
// the same read semantics as disk.Nybble, plus actually storing what the
// Disk II card shifts out.
type WritableDisk struct {
	Tracks    [][]byte
	halfTrack byte
	position  int
}

func (d *WritableDisk) track() []byte { return d.Tracks[d.halfTrack/2] }

func (d *WritableDisk) Read() byte {
	t := d.track()
	d.position = (d.position + 1) % len(t)
	return t[d.position]
}

func (d *WritableDisk) Skip(amount int) {
	d.position = (d.position + amount) % len(d.track())
}

func (d *WritableDisk) Write(b byte) {
	t := d.track()
	d.position = (d.position + 1) % len(t)
	t[d.position] = b
}

func (d *WritableDisk) SetHalfTrack(halfTrack byte) { d.halfTrack = halfTrack }
func (d *WritableDisk) HalfTrack() byte             { return d.halfTrack }
func (d *WritableDisk) Writeable() bool             { return true }

// MakeWritable replaces drive 1's write-protected disk with a WritableDisk
// over a fresh nibblisation of the same .dsk, and returns it so the caller
// can extract what the machine wrote. Call before (or after) boot; the
// drive's head state restarts at track 0, exactly like swapping a disk.
func (m *DiskMachine) MakeWritable(dskPath string) (*WritableDisk, error) {
	nyb, err := disk.DiskFromFile(dskPath, 0)
	if err != nil {
		return nil, err
	}
	w := &WritableDisk{Tracks: nyb.Tracks}
	m.Mem.Card.LoadDisk(w, 0)
	return w, nil
}

// ExtractSectors denibblises the disk back into .dsk byte order (the exact
// inverse of the loader's view), TOLERANTLY: the address field's checksum
// and epilogue are not required intact — an on-device write destroys them
// in this emulation (see the file comment) — but the address PROLOGUE and
// the full DATA checksum are. Every one of the 560 sectors must be found,
// so a write that damaged a neighbouring sector cannot hide.
func (d *WritableDisk) ExtractSectors() ([]byte, error) {
	out := make([]byte, disk.DOS_DISK_BYTES)
	for track := range len(d.Tracks) {
		t := d.Tracks[track]
		seen := 0
		var seenMask uint16
		// One linear scan; the track is circular, so scan it twice to catch
		// a sector whose address and data straddle the wrap point.
		buf := append(append([]byte{}, t...), t...)
		for i := 0; i+3 < len(buf) && seen < 16; i++ {
			if buf[i] != 0xD5 || buf[i+1] != 0xAA || buf[i+2] != 0x96 {
				continue
			}
			// The 4-4 encoded address field: volume, track, sector,
			// checksum. The checksum pair may be sync garbage (a write
			// splice), so it is read but NOT enforced.
			rd44 := func(off int) byte { return (buf[off]<<1 | 1) & buf[off+1] }
			sec := rd44(i + 7)
			if sec > 15 || seenMask&(1<<sec) != 0 {
				continue
			}
			// Find the data prologue within the gap.
			j := i + 11
			for ; j+2 < len(buf) && j < i+60; j++ {
				if buf[j] == 0xD5 && buf[j+1] == 0xAA && buf[j+2] == 0xAD {
					break
				}
			}
			if j+2 >= len(buf) || buf[j] != 0xD5 || buf[j+1] != 0xAA || buf[j+2] != 0xAD {
				continue
			}
			j += 3
			if j+343 > len(buf) {
				continue
			}
			var raw [342]byte
			xor := byte(0)
			bad := false
			for k := range 342 {
				v := disk.Dos33readTable[buf[j+k]]
				if v < 0 {
					bad = true
					break
				}
				b := byte(v) ^ xor
				// The on-disk order is the 86 low-bit bytes (stored at
				// raw[341..256]) then the 256 six-bit bytes (raw[0..255]).
				if k < 86 {
					raw[341-k] = b
				} else {
					raw[k-86] = b
				}
				xor = b
			}
			if bad {
				continue
			}
			if v := disk.Dos33readTable[buf[j+342]]; v < 0 || byte(v) != xor {
				continue // data checksum mismatch: not a healthy sector
			}
			data := disk.PostNybble(raw[:])
			logical := disk.Dos33PhysicalToLogicalSectorMap[sec]
			copy(out[(track*16+int(logical))*256:], data[:])
			seenMask |= 1 << sec
			seen++
			i = j + 342
		}
		if seen != 16 {
			return nil, fmt.Errorf("track %d: found %d of 16 sectors", track, seen)
		}
	}
	return out, nil
}
