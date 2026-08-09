package splash

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// asset is the committed source of truth for the splash: the 16 KB DHGR save.
func asset(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "assets", "fish8-splash-dazzledraw-save.bin"))
	if err != nil {
		t.Fatalf("%v (the hand-drawn splash asset)", err)
	}
	if len(raw) != RawBytes {
		t.Fatalf("asset is %d B, want %d", len(raw), RawBytes)
	}
	return raw
}

// TestRoundTripAsset is the core gate: the committed blob decodes back to the
// asset byte-for-byte, and Encode/Decode are inverses on it. If the encoder,
// the decoder, or the committed blob drift apart, the disk would show a
// corrupt logo and this fails with the first bad byte.
func TestRoundTripAsset(t *testing.T) {
	raw := asset(t)

	got, err := Decode(Blob())
	if err != nil {
		t.Fatalf("Decode(committed blob): %v", err)
	}
	if !bytes.Equal(got, raw) {
		for i := range raw {
			if got[i] != raw[i] {
				t.Fatalf("committed blob decodes wrong at byte %d ($%04X): got $%02X want $%02X "+
					"(run `go run ./cmd/gensplash`)", i, i, got[i], raw[i])
			}
		}
	}

	// Encode(raw) must reproduce the committed blob exactly, so the generator
	// is deterministic and the checked-in blob is the encoder's real output.
	if enc := Encode(raw); !bytes.Equal(enc, Blob()) {
		t.Fatalf("Encode(asset) = %d B, committed blob = %d B: they differ (stale splashblob.bin?)",
			len(enc), len(Blob()))
	}

	// ...and Decode(Encode(raw)) == raw, the property the task names.
	back, err := Decode(Encode(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, raw) {
		t.Fatal("Decode(Encode(asset)) != asset")
	}
}

// TestFitsBlockBudget: the blob (with magic) must fit the on-disk SPLASH file,
// and each bank must decode to exactly BankBytes so the 6502 decoder's
// bank-switch-at-8192 is honoured.
func TestFitsBlockBudget(t *testing.T) {
	blob := Blob()
	if len(blob) > DiskBytes {
		t.Fatalf("blob is %d B, over the %d-byte SPLASH file", len(blob), DiskBytes)
	}
	if DiskBytes%512 != 0 {
		t.Fatalf("DiskBytes = %d is not a whole number of 512-byte blocks", DiskBytes)
	}
	if got := Magic; blob[0] != got[0] || blob[1] != got[1] {
		t.Fatalf("blob does not start with the magic %q", Magic)
	}
	// The two streams each decode to exactly one bank.
	raw, err := Decode(blob)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != RawBytes {
		t.Fatalf("decoded %d B, want %d", len(raw), RawBytes)
	}
	// Disk() is the padded form the disk layout writes.
	if d := Disk(); len(d) != DiskBytes || !bytes.Equal(d[:len(blob)], blob) {
		t.Fatalf("Disk() is %d B or does not begin with the blob", len(d))
	}
}

// TestDecodeRejectsBadMagic proves the magic guard is real: a blob whose first
// two bytes are not '8','F' is refused, which is what stops a gross bad disk
// read from being decoded as pixels.
func TestDecodeRejectsBadMagic(t *testing.T) {
	blob := append([]byte(nil), Blob()...)
	blob[0] ^= 0xFF
	if _, err := Decode(blob); err == nil {
		t.Fatal("Decode accepted a blob with a corrupt magic")
	}
}

// TestPackbitsSyntheticShapes exercises the op boundaries directly (long runs,
// isolated pairs kept as literals, the 128-byte caps) so a change to the
// encoder that still happens to round-trip the asset is still caught.
func TestPackbitsSyntheticShapes(t *testing.T) {
	cases := [][]byte{
		bytes.Repeat([]byte{0x00}, BankBytes),                 // one giant run
		bytes.Repeat([]byte{0xAA, 0x55}, BankBytes/2),         // no run ever (all literal)
		append(bytes.Repeat([]byte{0x11}, 200),                // run > 128, then pair, then literals
			append([]byte{0x22, 0x22, 0x33}, bytes.Repeat([]byte{0x44, 0x55}, 50)...)...),
	}
	for i, c := range cases {
		if len(c) != BankBytes {
			// pad to a bank so Decode's bank sizing works
			c = append(c, make([]byte, BankBytes-len(c))...)
		}
		enc := packbits(c)
		dec := make([]byte, len(c))
		n, err := unpackbits(enc, dec)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if n != len(enc) {
			t.Fatalf("case %d: consumed %d of %d encoded bytes", i, n, len(enc))
		}
		if !bytes.Equal(dec, c) {
			t.Fatalf("case %d: round-trip mismatch", i)
		}
	}
}
