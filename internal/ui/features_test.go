package ui

import "testing"

// TestShippedFeatureMask reads the FEATURES/FEATURES2 bytes the ON-DEVICE
// driver installs, and compares them with the mask every measurement rig
// uses (ucibridge: 0x1F|FT_CKEXT, FEATURES2 = FT2_GENDEFER).
func TestShippedFeatureMask(t *testing.T) {
	for _, tc := range []struct {
		name string
		boot func(string, []byte) (*Machine, error)
	}{{"harnesskbd", Boot}, {"shipping", BootShipping}} {
		u, err := tc.boot("../..", nil)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if err := u.Key(0x0D); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		f1 := u.Peek(u.Defs["FEATURES"])
		f2 := u.Peek(u.Defs["FEATURES2"])
		wantF1 := byte(0x1F) | byte(u.Defs["FT_CKEXT"])
		t.Logf("%s: FEATURES=%#02x FEATURES2=%#02x (measurement rigs use FEATURES=%#02x)",
			tc.name, f1, f2, wantF1)
		if f1 != wantF1 {
			t.Errorf("%s: on-device FEATURES=%#02x, want the shipped gameplay mask %#02x (FT_CKEXT %#02x missing)",
				tc.name, f1, wantF1, u.Defs["FT_CKEXT"])
		}
	}
}
