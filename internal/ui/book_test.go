package ui_test

import (
	"strings"
	"testing"

	"github.com/zellyn/chess6502/internal/book"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/ui"
)

// TestBookIntegration: with the resident book at $2000, the engine's turn
// goes through the probe instead of a search, and the UI streams the
// opening's name out of the blob's own name table onto row 16.
//
// The cross-check is against internal/book's parsed entries, keyed on the
// engine's own HASH0-3 read straight out of the machine — so it proves both
// that the played move really came from the book and that the UI's
// Pascal-string walk lands on the name the book agrees with.
func TestBookIntegration(t *testing.T) {
	blob := book.DefaultBlob()
	b, err := book.Load(blob)
	if err != nil {
		t.Fatal(err)
	}
	u, err := ui.Boot(root, blob)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	// Two-player mode so we can look at the position after 1.e4 before the
	// engine answers it.
	twoPlayerPoke(t, u)
	mustEnter(t, u, "e2e4")

	h := u.Defs["HASH0"]
	key := uint32(u.Peek(h)) | uint32(u.Peek(h+1))<<8 |
		uint32(u.Peek(h+2))<<16 | uint32(u.Peek(h+3))<<24
	var candidates []book.Entry
	for _, e := range b.Entries() {
		if e.Key == key {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		t.Fatalf("the book has no entry for the position after 1.e4 (key %#08x)", key)
	}

	const sentinel = 0xEE
	u.Poke(u.Defs["CUROPENING"], sentinel)
	u.Poke(ui.UIHUMAN, 0) // human is White, so the engine answers as Black
	if err := u.Enter(""); err != nil {
		t.Fatal(err)
	}

	hist := u.History()
	if len(hist) != 2 {
		t.Fatalf("the engine did not reply: %v\n%s", hist, u.Screen())
	}
	s := u.Screen()
	t.Logf("after 1.e4 with the resident book loaded:\n%s", s)

	nameID := u.Peek(u.Defs["CUROPENING"])
	if nameID == sentinel {
		t.Fatalf("the probe never ran: CUROPENING is still the sentinel")
	}
	var picked *book.Entry
	for i, e := range candidates {
		if chesstest.MoveUCI(e.From, e.To, e.Flags) == hist[1] {
			picked = &candidates[i]
		}
	}
	if picked == nil {
		var opts []string
		for _, e := range candidates {
			opts = append(opts, chesstest.MoveUCI(e.From, e.To, e.Flags))
		}
		t.Fatalf("the engine played %s, which is not a book move for this key (%v)",
			hist[1], opts)
	}
	if nameID != picked.NameID {
		t.Errorf("CUROPENING = %d, want %d (the chosen entry's NameID)", nameID, picked.NameID)
	}
	row := strings.TrimSpace(s.Text(16))
	if want := "BOOK: " + b.Name(nameID); row != want {
		t.Errorf("row 16 = %q, want %q", row, want)
	}
	t.Logf("book reply %s from %d candidates; CUROPENING %d -> row 16 %q",
		hist[1], len(candidates), nameID, row)

	// Playing out of book must drop the line: a position the book does not
	// know leaves row 16 blank again.
	twoPlayerPoke(t, u)
	mustEnter(t, u, "a2a3")
	mustEnter(t, u, "h7h6")
	mustEnter(t, u, "h2h3")
	u.Poke(ui.UIHUMAN, 0)
	if err := u.Enter(""); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(u.Screen().Text(16)); got != "" {
		t.Errorf("out of book, row 16 still says %q", got)
	}
}

// TestBookRandomIsFullWidth gates asm/m8.s uibookrnd: the 32 bits of
// collected entropy (ENTROPY:2 + ENTCNT:2) must reach BOOKRND as 32 bits.
//
// ★ WHY THIS NEEDS A GATE AT ALL. uibookrnd is a fixed pattern of EORs, so
// it is AFFINE over GF(2) and its whole behaviour is a 32x32 bit matrix. A
// matrix that is not full rank silently throws entropy away and nothing
// downstream can tell: the book still picks a legal move, the game still
// looks random, and the loss shows up only as openings that recur more often
// than they should. It shipped at rank 24 until 2026-07-29, because the last
// byte read ENTROPY's LOW half and so was identically
// BOOKRND+0 EOR BOOKRND+2: ENTROPY's HIGH byte reached BOOKRND nowhere at
// all, capping the book's weighted pick at 2^24 values instead of 2^32.
//
// Affine means 33 evaluations settle it exactly: f(0) gives the constant and
// f(e_i) EOR f(0) gives column i. Full rank <=> a bijection <=> no input
// combination is ever lost.
func TestBookRandomIsFullWidth(t *testing.T) {
	u := boot(t)
	addr, ok := u.Lbl["uibookrnd"]
	if !ok {
		t.Fatal("uibookrnd not in asm/m8t.lbl")
	}
	// Trampoline in the driver-scratch RAM at $03A0 (defs.inc documents the
	// page as free), the same lever TestSoftClockLimits uses for uilimits.
	const tramp = 0x03A0
	const spin = tramp + 3 // the JMP * the trampoline parks on
	entropy, entcnt, bookrnd := u.Defs["ENTROPY"], u.Defs["ENTCNT"], u.Defs["BOOKRND"]

	// eval runs uibookrnd over one 32-bit input (ENTROPY low, ENTROPY high,
	// ENTCNT low, ENTCNT high) and returns BOOKRND as a uint32.
	//
	// The trampoline ends in a two-byte self-jump rather than the harness
	// exit trap: the trap latches, so a second Run would return instantly and
	// every call after the first would silently report the FIRST call's
	// BOOKRND — a rank-0 matrix, and a gate that fails for the wrong reason.
	eval := func(in uint32) uint32 {
		u.Poke(entropy, byte(in))
		u.Poke(entropy+1, byte(in>>8))
		u.Poke(entcnt, byte(in>>16))
		u.Poke(entcnt+1, byte(in>>24))
		u.Poke(tramp+0, 0x20) // JSR uibookrnd
		u.Poke(tramp+1, byte(addr))
		u.Poke(tramp+2, byte(addr>>8))
		u.Poke(tramp+3, 0x4C) // JMP *
		u.Poke(tramp+4, byte(spin&0xFF))
		u.Poke(tramp+5, byte(spin>>8))
		for i := uint16(0); i < 4; i++ {
			u.Poke(bookrnd+i, 0xCC) // poison, so a no-op is not read as 0
		}
		u.M.CPU.SetPC(tramp)
		if exited, _, err := u.M.Run(4096); err != nil || exited {
			t.Fatalf("running uibookrnd: exited=%v err=%v", exited, err)
		}
		if pc := u.M.CPU.PC(); pc != spin {
			t.Fatalf("uibookrnd did not return to the trampoline: PC = $%04X", pc)
		}
		return uint32(u.Peek(bookrnd)) | uint32(u.Peek(bookrnd+1))<<8 |
			uint32(u.Peek(bookrnd+2))<<16 | uint32(u.Peek(bookrnd+3))<<24
	}

	base := eval(0)
	var cols [32]uint32
	for i := range 32 {
		cols[i] = eval(1<<i) ^ base
	}
	// Gaussian elimination over GF(2): reduce the columns to a basis.
	var basis []uint32
	for _, c := range cols {
		for _, b := range basis {
			if v := c ^ b; v < c {
				c = v
			}
		}
		if c != 0 {
			basis = append(basis, c)
			// Reduction gives every basis element a distinct leading bit,
			// so keeping the slice sorted descending makes the greedy pass
			// above see them in leading-bit order — which is what makes it
			// an exact elimination rather than an approximation.
			for i := len(basis) - 1; i > 0 && basis[i] > basis[i-1]; i-- {
				basis[i], basis[i-1] = basis[i-1], basis[i]
			}
		}
	}
	if len(basis) != 32 {
		t.Errorf("uibookrnd has GF(2) rank %d, want 32: %d of the collector's "+
			"32 bits never reach BOOKRND, so the book's weighted pick draws from "+
			"2^%d values instead of 2^32", len(basis), 32-len(basis), len(basis))
	}
	// Affine + full rank is a bijection; spot-check it directly too.
	seen := map[uint32]uint32{}
	for _, in := range []uint32{0, 1, 0x0000FFFF, 0xFFFF0000, 0x12345678,
		0xDEADBEEF, 0xA5A5A5A5, 0x5A5A5A5A, 0xFFFFFFFF, 0x01020304} {
		out := eval(in)
		if prev, dup := seen[out]; dup {
			t.Errorf("uibookrnd maps both %#08x and %#08x to %#08x", prev, in, out)
		}
		seen[out] = in
	}
	t.Logf("uibookrnd: GF(2) rank %d/32 over (ENTROPY, ENTCNT) -> BOOKRND", len(basis))
}
