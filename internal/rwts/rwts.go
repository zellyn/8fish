// Package rwts is the resident ProRWTS2 read-only floppy driver: the one
// piece of 8fish that can read the disk after boot (docs/prorwts2-design.md).
//
// The driver is peterferrie's ProRWTS2 (BSD-3-Clause), vendored pristine at
// asm/third_party/PRORWTS2.S and assembled with ACME by cmd/genrwts into
// rwtsblob.bin — a fully-relocated image of the FLOPPY driver configured for
// exactly one job: read a named file's blocks off the 8fish disk into main
// RAM. It is resident in LANGUAGE CARD BANK 1 at EntryAddr ($DC00), the
// resting bank, so calling it needs no bank switch at all.
//
// WHAT IS NOT HERE. ProRWTS2's own `init` requires a running ProDOS (it calls
// the MLI and reads DEVNUM), and 8fish has none — so the init is never
// assembled into the blob. Its three real products are reproduced elsewhere:
//
//   - the 6-and-2 denibble table (nibtbl): PREBUILT by cmd/genrwts into the
//     blob, byte-for-byte the table init's loop would have built;
//   - the slot<<4 pokes into the driver's soft-switch operands: done at boot
//     by asm/m8.s's m8rwtsinit, from the boot ROM's slot byte ($2B), over the
//     operand addresses listed in SlotSites (generated into gen.go);
//   - the current-track seed (the `trackd1` self-modifying operand): seeded
//     from the Standard Delivery loader's $41 while the head has not moved.
//
// The directory's key block number is baked into the blob at build time
// (DirBlock below): with a fixed disk layout there is nothing to discover at
// run time.
package rwts

import _ "embed"

// DirBlock is the ProDOS block number of the 8fish book region's directory
// key block. Track 26 (block 208 = 26*8): comfortably above the Standard
// Delivery region (tracks 0-13ish, gated by delivery's overlap check) and low
// enough that the whole region — directory + 4 index blocks + 64 data blocks
// — fits the remaining tracks. cmd/genrwts bakes this number into the
// driver blob's `unrblocklo/hi` operands; internal/delivery's region builder
// places the directory at the same number. One constant, two consumers.
const DirBlock = 208

// Blob is the relocated driver image, delivered by stage 2 of the chain load
// and copied to Language Card bank 1 at EntryAddr by asm/m8.s (m8rwtsinit).
//
//go:embed rwtsblob.bin
var Blob []byte
