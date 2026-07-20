package chesstest

import (
	"os"
	"testing"
)

// TestDebugRootMoves prints the move list generated at the root — a
// debugging aid for perft mismatches. Gated behind CHESS6502_DEBUG so it
// stays silent in ordinary test runs.
func TestDebugRootMoves(t *testing.T) {
	if os.Getenv("CHESS6502_DEBUG") != "1" {
		t.Skip("set CHESS6502_DEBUG=1 to run")
	}
	fen := "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1"
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(perftBin, defs, pos, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Run(1_000_000_000)
	base := defs["MOVESTACK"]
	endLo := m.Mem.Main[defs["PLYENDLO"]]
	endHi := m.Mem.Main[defs["PLYENDHI"]]
	end := uint16(endLo) | uint16(endHi)<<8
	t.Logf("generated %d moves:", (end-base)/4)
	for a := base; a < end; a += 4 { // 4 bytes: tier,from,to,flags
		from, to, flags := m.Mem.Main[a+1], m.Mem.Main[a+2], m.Mem.Main[a+3]
		t.Logf("  %s%s flags=%02x", SqName(from), SqName(to), flags)
	}
	// Also dump the piece list.
	psq := defs["PIECESQ"]
	for slot := range 32 {
		sq := m.Mem.Main[psq+uint16(slot)]
		if sq != 0xFF {
			t.Logf("slot %2d: %s (board=%02x)", slot, SqName(sq), m.Mem.Main[defs["BOARD"]+uint16(sq)])
		}
	}
}

// TestDebugKingSlot single-steps the engine and logs every write to the
// white king's piece-list slot, with the PC that did it.
func TestDebugKingSlot(t *testing.T) {
	if testing.Short() {
		t.Skip("debug helper")
	}
	fen := "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1"
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(perftBin, defs, pos, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	psq := defs["PIECESQ"]
	prev := m.Mem.Main[psq]
	n := 0
	for range 300000 {
		exited, _, err := m.Run(1)
		if err != nil {
			t.Fatal(err)
		}
		if cur := m.Mem.Main[psq]; cur != prev {
			t.Logf("cycle %7d PC=%04X: king slot %s -> %s", m.Cycles, m.CPU.PC(), SqName(prev), SqName(cur))
			prev = cur
			n++
			if n > 40 {
				t.Fatal("too many transitions")
			}
		}
		if exited {
			break
		}
	}
}
