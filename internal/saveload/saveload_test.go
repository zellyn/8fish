package saveload

import (
	"bytes"
	"testing"
)

func TestEmptyRecordValidates(t *testing.T) {
	rec := Empty()
	if len(rec) != RecordBytes {
		t.Fatalf("Empty() is %d bytes, want %d", len(rec), RecordBytes)
	}
	plies, _, _, _, _, ok := Decode(rec)
	if !ok {
		t.Fatal("the shipped placeholder record does not validate")
	}
	if len(plies) != 0 {
		t.Fatalf("the placeholder holds %d plies, want 0", len(plies))
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	in := []Ply{{0x14, 0x34, 0}, {0x64, 0x44, 0}, {0x06, 0x25, 0}, {0x71, 0x52, 0x03}}
	rec := Encode(in, 0xFF, 4, 0, 0)
	plies, human, level, result, win, ok := Decode(rec)
	if !ok {
		t.Fatal("Encode's output does not Decode")
	}
	if len(plies) != len(in) {
		t.Fatalf("got %d plies, want %d", len(plies), len(in))
	}
	for i := range in {
		if plies[i] != in[i] {
			t.Errorf("ply %d: got %+v, want %+v", i, plies[i], in[i])
		}
	}
	if human != 0xFF || level != 4 || result != 0 || win != 0 {
		t.Errorf("scalars: human=%#x level=%d result=%d win=%d", human, level, result, win)
	}
}

func TestDecodeRefusesDamage(t *testing.T) {
	rec := Encode([]Ply{{0x14, 0x34, 0}}, 0, 4, 0, 0)
	for _, tc := range []struct {
		name string
		off  int
	}{
		{"magic", OffMagic},
		{"version", OffVersion},
		{"ply count", OffPlyCount},
		{"a from-square", OffFrom},
		{"a flag byte", OffFlag},
		{"the checksum itself", OffChecksum},
	} {
		bad := append([]byte(nil), rec...)
		bad[tc.off] ^= 0x5A
		if _, _, _, _, _, ok := Decode(bad); ok {
			t.Errorf("Decode accepted a record with %s damaged", tc.name)
		}
	}
	// ...and a truncated one.
	if _, _, _, _, _, ok := Decode(rec[:512]); ok {
		t.Error("Decode accepted a half-length record")
	}
}

func TestChecksumSkipsItsOwnBytes(t *testing.T) {
	rec := Empty()
	// Whatever is stored AT the checksum offsets must not affect the sum —
	// that is what lets the asm side sum all 1,024 bytes while they are zero.
	before := Checksum16(rec)
	rec[OffChecksum] ^= 0xFF
	rec[OffChecksum+1] ^= 0xFF
	if got := Checksum16(rec); got != before {
		t.Fatalf("Checksum16 covers its own bytes: %#x vs %#x", got, before)
	}
	// ...and every other byte must.
	rec2 := append([]byte(nil), rec...)
	rec2[RecordBytes-1] ^= 1
	if Checksum16(rec2) == before {
		t.Fatal("Checksum16 ignored the record's last byte")
	}
	if !bytes.Equal(Encode(nil, 0, 0, 0, 0)[OffFrom:], make([]byte, RecordBytes-OffFrom)) {
		t.Fatal("an empty Encode's array pages are not zero")
	}
}
