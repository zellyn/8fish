// Package splash is the boot TITLE CARD: the owner's hand-drawn 16 KB
// double-hi-res logo (assets/fish8-splash-dazzledraw-save.bin), PackBits-
// compressed for the disk and decoded straight into DHGR page 1 at boot by
// asm/m8.s (m8splash) before the chessboard appears.
//
// FORMAT. The raw asset is 16,384 B: 8 KB AUX half then 8 KB MAIN half (the
// A2FC save order). Each half is compressed as its OWN PackBits stream so no
// copy/run op ever crosses the 8,192-byte bank boundary and each stream
// decodes to EXACTLY 8,192 bytes; the 6502 decoder switches destination banks
// after 8,192 output bytes with no length field to read. The blob is
//
//	'8' 'F'  [aux PackBits stream]  [main PackBits stream]
//
// a two-byte magic ('8','F' = $38,$46) so a gross bad disk read is caught
// before the decoder scribbles garbage over the screen. On disk the blob is
// padded with zeros to a whole number of 512-byte ProDOS blocks (DiskBytes);
// the decoder ignores the padding (it stops at 8,192 bytes per bank).
//
// PACKBITS. A control byte c drives the stream:
//
//	c < 128 : copy the next c+1 bytes verbatim   (a literal run of 1..128)
//	c >= 128: repeat the next single byte 257-c times (a run of 2..128)
//
// c == 128 is unused (the encoder never emits it). This is the classic
// Apple/TIFF PackBits, per bank.
package splash

import _ "embed"

const (
	// RawBytes is the uncompressed screen: 8 KB aux + 8 KB main.
	RawBytes = 16384
	// BankBytes is one DHGR bank half — the point at which the decoder
	// switches destination banks.
	BankBytes = 8192
	// DiskBytes is the on-disk size of the compressed blob: the encoded blob
	// zero-padded up to a whole number of 512-byte blocks. asm/m8.s requests
	// exactly this many bytes and internal/delivery lays it out in this many
	// bytes' worth of blocks.
	DiskBytes = 5632 // 11 blocks
)

// Magic is the two-byte header the decoder checks before trusting the stream.
var Magic = [2]byte{'8', 'F'}

//go:embed splashblob.bin
var blob []byte

// Blob returns the committed compressed blob (magic + the two PackBits
// streams), regenerated from the asset by cmd/gensplash.
func Blob() []byte { return blob }

// Disk returns the blob zero-padded to DiskBytes, i.e. exactly what
// internal/delivery writes into the SPLASH file's data blocks and what
// asm/m8.s reads back off the disk.
func Disk() []byte {
	out := make([]byte, DiskBytes)
	copy(out, blob)
	return out
}

// Encode compresses a 16,384-byte raw DHGR screen (8 KB aux then 8 KB main)
// into the magic-prefixed two-stream blob. It panics if raw is not RawBytes
// long, because every caller (the generator, the round-trip test) hands it the
// fixed-size asset and a wrong size is a build error, not a runtime condition.
func Encode(raw []byte) []byte {
	if len(raw) != RawBytes {
		panic("splash: raw screen must be exactly 16384 bytes")
	}
	out := []byte{Magic[0], Magic[1]}
	out = append(out, packbits(raw[:BankBytes])...)
	out = append(out, packbits(raw[BankBytes:])...)
	return out
}

// Decode reverses Encode: it checks the magic, then decodes the aux stream to
// the first 8,192 output bytes and the main stream (continuing in the same
// blob) to the next 8,192, returning the 16,384-byte screen. Padding after the
// second stream is ignored. It returns an error on a bad magic or a truncated
// stream — the same failure the 6502 decoder guards against with its magic
// check.
func Decode(blob []byte) ([]byte, error) {
	if len(blob) < 2 || blob[0] != Magic[0] || blob[1] != Magic[1] {
		return nil, errBadMagic
	}
	out := make([]byte, RawBytes)
	src := blob[2:]
	n, err := unpackbits(src, out[:BankBytes])
	if err != nil {
		return nil, err
	}
	if _, err := unpackbits(src[n:], out[BankBytes:]); err != nil {
		return nil, err
	}
	return out, nil
}

var (
	errBadMagic  = errString("splash: bad magic (want '8','F')")
	errTruncated = errString("splash: stream ran out before the bank was full")
)

type errString string

func (e errString) Error() string { return string(e) }

// packbits compresses one bank (any length) with PackBits, using the classic
// TIFF/Apple encoder: a run op is emitted only for THREE or more identical
// bytes, so an isolated pair stays inside a literal (2 bytes either way, but
// without the extra control byte that breaking the literal would cost). Each
// op is capped at 128 bytes. This is what keeps the blob inside its block
// budget; the decoder accepts any valid PackBits regardless.
func packbits(src []byte) []byte {
	var out []byte
	n := len(src)
	for i := 0; i < n; {
		// Length of the identical-byte run starting at i (capped at 128).
		run := 1
		for i+run < n && src[i+run] == src[i] && run < 128 {
			run++
		}
		if run >= 3 {
			out = append(out, byte(257-run), src[i])
			i += run
			continue
		}
		// A literal run: accumulate until a run of >=3 begins or 128 bytes.
		start := i
		for i < n && i-start < 128 {
			if i+2 < n && src[i] == src[i+1] && src[i] == src[i+2] {
				break // a 3+ run begins at i; let the next iteration emit it
			}
			i++
		}
		out = append(out, byte(i-start-1))
		out = append(out, src[start:i]...)
	}
	return out
}

// unpackbits decodes PackBits ops from src into out until out is exactly full,
// returning how many src bytes it consumed. The bank size is out's length, not
// a field in the stream — the mirror of the 6502 decoder, which counts output
// bytes to know when a bank is done.
func unpackbits(src, out []byte) (int, error) {
	o, i := 0, 0
	for o < len(out) {
		if i >= len(src) {
			return 0, errTruncated
		}
		c := src[i]
		i++
		if c < 128 {
			cnt := int(c) + 1
			if i+cnt > len(src) || o+cnt > len(out) {
				return 0, errTruncated
			}
			copy(out[o:o+cnt], src[i:i+cnt])
			i += cnt
			o += cnt
		} else {
			cnt := 257 - int(c)
			if i >= len(src) || o+cnt > len(out) {
				return 0, errTruncated
			}
			b := src[i]
			i++
			for k := 0; k < cnt; k++ {
				out[o+k] = b
			}
			o += cnt
		}
	}
	return i, nil
}
