package chesstest

import (
	"fmt"
	"testing"
)

// TestTTScoreRoundTrip is the regression gate for the TT's node-relative
// mate bookkeeping (asm/tt.s ttstore/ttprobe).
//
// The invariant: a score handed to ttstore and later read back by ttprobe
// must come out UNCHANGED for every ordinary (non-mate) score, and for a
// mate score must come back shifted by exactly the difference in ply so
// that "mate in N from HERE" stays true at whatever node reads it.
//
// The bug this catches: ttstore/ttprobe used to classify mate scores with
// an UNSIGNED `cmp #$74` on the score's high byte. Winning mates live in
// $74xx+ and losing mates in $80xx-$8Bxx, but $80-$FF is ALSO ">= $74"
// unsigned, so EVERY negative score took the winning-mate path (+Ply on
// store, -Ply on probe) and the losing-mate arm was dead code. Every
// negative score stored at ply s and read at ply p came back off by s-p,
// and losing mates were shifted the WRONG WAY. With the search's TT
// cutoffs comparing that score against alpha/beta, the corruption is a
// direct source of wrong cutoffs and wrong reported mate distances.
//
// Method: hook tsadj (ttstore's adjustment point, where TTENTRY+5/6 still
// holds the caller's score) to shadow what was stored at each of the 4096
// indices, and ttpdone (the instruction after `jsr ttprobe`, where
// TTENTRY+5/6 holds the fully adjusted score and carry = hit) to see what
// came back. The TT is always-replace and the probe's 20-bit verify means
// a hit is the same position, so a hit at an index the shadow has seen is
// exactly the round trip of that store.
func TestTTScoreRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	fens := []struct {
		fen   string
		depth byte
	}{
		{"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", 5},
		{"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", 5},
		{"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 6},
		// mate-heavy: both sides' mate scores flow through the table
		{"6k1/5ppp/8/8/8/8/1q6/R6K w - - 0 1", 6},
		{"r1bqkb1r/pppp1ppp/2n2n2/4p2Q/2B1P3/8/PPPP1PPP/RNB1K1NR w KQkq - 4 4", 5},
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	tsadj, ttpdone := labels["tsadj"], labels["ttpdone"]
	if tsadj == 0 || ttpdone == 0 {
		t.Fatalf("missing hook labels: tsadj=%#x ttpdone=%#x", tsadj, ttpdone)
	}
	const (
		ttentry     = 0xF2 // asm/defs.inc TTENTRY
		plyAddr     = 0x03 // asm/defs.inc PLY
		hash0       = 0xDD // asm/defs.inc HASH0
		mateZoneLo  = 0x7400
		nmateZoneHi = -0x7401
	)
	type rec struct {
		score int // what the caller handed ttstore
		ply   int
		ok    bool
	}
	var shadow [4096]rec
	var trips, bad int
	var examples []string

	for _, tc := range fens {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1f)
		SetBudget(m, defs, 0, tc.depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		for i := range shadow {
			shadow[i] = rec{}
		}
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
			if pc != tsadj && pc != ttpdone {
				return
			}
			sc := int(int16(uint16(m.Mem.Main[ttentry+5]) | uint16(m.Mem.Main[ttentry+6])<<8))
			ply := int(m.Mem.Main[plyAddr])
			idx := int(m.Mem.Main[hash0]) | int(m.Mem.Main[hash0+1]&0x0F)<<8
			if pc == tsadj {
				shadow[idx] = rec{score: sc, ply: ply, ok: true}
				return
			}
			if m.CPU.P()&1 == 0 { // carry clear: probe missed
				return
			}
			r := shadow[idx]
			if !r.ok {
				return
			}
			trips++
			// Expected: an ordinary score is invariant; a mate score is
			// re-based from the storing node's ply to this node's ply.
			want := r.score
			switch {
			case r.score >= mateZoneLo:
				want = r.score + r.ply - ply
			case r.score <= nmateZoneHi:
				want = r.score - r.ply + ply
			}
			if int16(want) != int16(sc) {
				bad++
				if len(examples) < 6 {
					examples = append(examples, fmt.Sprintf(
						"idx=%04x stored %d at ply %d -> probed at ply %d: got %d, want %d (off by %+d)",
						idx, r.score, r.ply, ply, sc, want, sc-want))
				}
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("fen=%q exited=%v code=%d err=%v", tc.fen, exited, code, err)
		}
	}
	t.Logf("TT store->probe round trips traced: %d", trips)
	if trips < 1000 {
		t.Fatalf("only %d round trips traced; the hooks are not firing", trips)
	}
	if bad != 0 {
		for _, e := range examples {
			t.Errorf("TT round trip corrupted: %s", e)
		}
		t.Fatalf("%d/%d (%.1f%%) TT store->probe round trips returned a corrupted score",
			bad, trips, 100*float64(bad)/float64(trips))
	}
}

// TestTTMateStoreAdjust checks ttstore's ply conversion DIRECTLY — the raw
// value it is about to write vs the value the caller handed it — for all
// three score classes. It pins the arm the unsigned compare left as dead
// code: a LOSING mate (<= -29697, high byte $80-$8B) must be re-based
// DOWNWARD (score - Ply) on store, where the old code re-based it UPWARD
// like a winning mate; and an ordinary negative score must not move at all.
// A same-ply round trip hides both of those (shift and unshift cancel), so
// this test looks at the stored bytes rather than the round trip.
func TestTTMateStoreAdjust(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator searches")
	}
	// Black to move, white mates shortly: the losing-mate scores are the
	// ones black stores at its own nodes.
	fens := []struct {
		fen   string
		depth byte
	}{
		{"6k1/5ppp/8/8/8/8/8/R5K1 b - - 0 1", 5},
		{"7k/5ppp/8/8/8/8/5PPP/R5K1 b - - 0 1", 6},
		{"k7/8/2K5/8/8/8/8/6R1 b - - 0 1", 5},
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	tsadj, tsgo := labels["tsadj"], labels["tsgo"]
	if tsadj == 0 || tsgo == 0 {
		t.Fatalf("missing hook labels: tsadj=%#x tsgo=%#x", tsadj, tsgo)
	}
	const (
		ttentry     = 0xF2
		plyAddr     = 0x03
		mateZoneLo  = 0x7400
		nmateZoneHi = -0x7401
	)
	var pre, preP int
	var have bool
	var stores, loseStores, winStores, negStores, bad int
	var examples []string
	for _, tc := range fens {
		pos, err := ParseFEN(tc.fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, 0x1f)
		SetBudget(m, defs, 0, tc.depth)
		m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
		have = false
		if _, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
			if pc != tsadj && pc != tsgo {
				return
			}
			sc := int(int16(uint16(m.Mem.Main[ttentry+5]) | uint16(m.Mem.Main[ttentry+6])<<8))
			ply := int(m.Mem.Main[plyAddr])
			if pc == tsadj {
				pre, preP, have = sc, ply, true
				return
			}
			if !have {
				return
			}
			have = false
			stores++
			class := "ordinary"
			want := pre
			switch {
			case pre >= mateZoneLo:
				winStores++
				class = "winning mate"
				want = pre + preP
			case pre <= nmateZoneHi:
				loseStores++
				class = "losing mate"
				want = pre - preP
			case pre < 0:
				negStores++
			}
			if int16(want) != int16(sc) {
				bad++
				if len(examples) < 8 {
					examples = append(examples, fmt.Sprintf(
						"%s %d at ply %d: ttstore wrote %d, want %d (off by %+d)",
						class, pre, preP, sc, int(int16(want)), sc-int(int16(want))))
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("ttstore adjustments checked: %d (winning mates %d, losing mates %d, ordinary negatives %d)",
		stores, winStores, loseStores, negStores)
	if loseStores == 0 {
		t.Fatal("no losing-mate scores reached the TT; the test positions no longer exercise the dead arm")
	}
	if negStores == 0 {
		t.Fatal("no ordinary negative scores reached the TT")
	}
	for _, e := range examples {
		t.Errorf("ttstore ply adjustment wrong: %s", e)
	}
	if bad != 0 {
		t.Fatalf("%d/%d ttstore ply adjustments wrong", bad, stores)
	}
}
