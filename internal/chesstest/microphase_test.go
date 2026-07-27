package chesstest

import (
	"fmt"
	"testing"
)

// TestMicroABPhase is TestMicroAB extended with MATERIAL instrumentation, and
// it is the ground-truth generator for the mirror's phase-aware cycle-cost
// model (internal/mirror/cycles.go, cycles.md).
//
// TestMicroAB reports per-search operation-entry counts and the true emulated
// cycle total. That is enough to fit a model whose per-operation cost is
// CONSTANT — and such a model over-prices low-material positions by ~30-40%,
// because real per-op cost scales with the number of live pieces (movegen
// emits fewer moves, attacked() scans fewer live slots, eval walks a shorter
// piece list).
//
// So this test additionally accumulates, at every search/make/eval entry, the
// number of LIVE piece slots (PIECESQ entries != NOSQ) and the engine's own
// PHASE byte. Those sums are the regressors a phase-aware model needs:
//
//	sum_over_nodes(pieces)  ==  Nodes * mean_pieces_per_node
//
// so a per-node cost of the form (a + b*pieces) is a linear model in
// [search, sumPcSearch] and fits with the same machinery.
//
// Each (mask, fen) is a subtest so a long calibration run can be split up
// (-run 'TestMicroABPhase/m1f/f03'). Output lines are formatted as Go struct
// literals, ready to paste into internal/mirror/cycles_test.go: asmRow.
func TestMicroABPhase(t *testing.T) {
	if testing.Short() {
		t.Skip("calibration: run explicitly")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	psqA, phaseA := labels["PIECESQ"], labels["PHASE"]
	if psqA == 0 {
		psqA = defs["PIECESQ"]
	}
	if phaseA == 0 {
		phaseA = defs["PHASE"]
	}
	searchA, makeA, evalA := labels["search"], labels["make"], labels["eval"]
	attackedA, ttprobeA, generateA := labels["attacked"], labels["ttprobe"], labels["generate"]
	// A missing symbol would resolve to address 0 and silently report zero
	// counts for that probe, which would corrupt the calibration table.
	for name, addr := range map[string]uint16{
		"PIECESQ": psqA, "PHASE": phaseA, "search": searchA, "make": makeA,
		"eval": evalA, "attacked": attackedA, "ttprobe": ttprobeA, "generate": generateA,
	} {
		if addr == 0 {
			t.Fatalf("symbol %q not found in asm/engine.lbl or asm/defs.inc", name)
		}
	}

	for _, tier := range calibTiers {
		mask, base := tier.mask, tier.depth
		t.Run(fmt.Sprintf("m%02x", mask), func(t *testing.T) {
			for i, cf := range calibFens {
				i, cf := i, cf
				t.Run(fmt.Sprintf("f%02d", i), func(t *testing.T) {
					depth := base + cf.bonus[tierIdx(mask)]
					pos, err := ParseFEN(cf.fen)
					if err != nil {
						t.Fatal(err)
					}
					m, err := NewMachine(bin, defs, pos, 0, nil)
					if err != nil {
						t.Fatal(err)
					}
					SetFeatures(m, defs, mask)
					SetBudget(m, defs, 0, depth) // fixed depth
					m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove

					// livePieces counts non-tombstoned PIECESQ slots.
					mem := m.Mem.Main[:]
					livePieces := func() uint64 {
						var n uint64
						for k := uint16(0); k < 32; k++ {
							if mem[psqA+k] != 0xFF {
								n++
							}
						}
						return n
					}
					var nSearch, nMake, nEval, nAtt, nTT, nGen uint64
					var pcSearch, pcMake, pcEval, phSearch uint64
					exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
						switch pc {
						case searchA:
							nSearch++
							pcSearch += livePieces()
							phSearch += uint64(mem[phaseA])
						case makeA:
							nMake++
							pcMake += livePieces()
						case evalA:
							nEval++
							pcEval += livePieces()
						case attackedA:
							nAtt++
						case ttprobeA:
							nTT++
						case generateA:
							nGen++
						}
					})
					if err != nil || !exited || code > 2 {
						t.Fatalf("fen=%q mask=%#x exited=%v code=%d err=%v", cf.fen, mask, exited, code, err)
					}
					score := int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8)
					bf, bt, bfl := m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]]
					// Go-literal row for internal/mirror/cycles_test.go.
					t.Logf("ROW {%#02x, %d, %q, %d, %q, %d, %d, %d, %d, %d, %d, %d, %d, %d, %d, %d},",
						mask, depth, cf.fen, score, MoveUCI(bf, bt, bfl), m.Cycles,
						nSearch, nMake, nEval, nAtt, nTT, nGen, pcSearch, pcMake, pcEval, phSearch)
					t.Logf("     mean pieces/node %.2f  mean phase/node %.2f  cyc/node %.0f",
						float64(pcSearch)/float64(nSearch), float64(phSearch)/float64(nSearch),
						float64(m.Cycles)/float64(nSearch))
				})
			}
		})
	}
}

// calibTiers are TestMicroAB's feature-mask/depth tiers: 0x1F is the shipped
// configuration, 0x07 exercises null/futility/killer without the eval extras,
// 0x00 is the bare alpha-beta loop. Sparse positions get a per-FEN, per-tier
// depth BONUS on top (see calibFen.bonus).
var calibTiers = []struct {
	mask  byte
	depth byte
}{{0x1F, 6}, {0x07, 5}, {0x00, 4}}

func tierIdx(mask byte) int {
	for i, t := range calibTiers {
		if t.mask == mask {
			return i
		}
	}
	panic("bad mask")
}

// calibFen is one calibration position. bonus[tier] is added to the tier
// depth: a 4-piece endgame at depth 6 searches a trivially small tree, which
// would contribute almost no information to the fit, so sparse positions are
// searched deeper until their trees are comparable in size to the midgame
// ones. The bonus is per TIER because the unpruned 0x00 mask explodes far
// faster with depth than the fully-pruned 0x1F one. Depth is not a regressor
// in the cost model (the model is per OPERATION, and both the operation
// counts and the cycle total are measured over the whole search), so mixing
// depths across positions is sound; the bonuses were tuned empirically to put
// every search in the ~10M-700M cycle range.
type calibFen struct {
	fen   string
	bonus [3]byte // per calibTiers entry: 0x1F, 0x07, 0x00
}

// calibFens: the original 6 TestMicroAB positions (indices 0-5: five 30-32
// piece middlegames plus one 10-piece endgame) followed by 17 positions
// filling in the material range 3..28 pieces, which the original set left
// almost empty. Fitting a material-dependent per-node cost from the single
// original endgame would be fitting one position; these 17 make the phase
// term identifiable from a spread of 23 positions.
var calibFens = []calibFen{
	// --- the original TestMicroAB six (30, 31, 32, 32, 10, 30 pieces) ---
	{"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", [3]byte{0, 0, 0}},
	{"r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", [3]byte{0, 0, 0}},
	{"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", [3]byte{0, 0, 0}},
	{"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", [3]byte{0, 0, 0}},
	{"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", [3]byte{0, 0, 0}},
	{"2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", [3]byte{0, 0, 0}},
	// --- added for the phase fit: the material range 3..28 pieces ---
	// The comment gives (piece count, phase). Phase is the engine's own
	// N=B=1,R=2,Q=4 sum, deliberately NOT collinear with the piece count here
	// (12 pieces at phase 0 vs 12 at phase 6; 8 at phase 0 vs 8 at phase 4),
	// so the fit can tell the two candidate material regressors apart.
	{"8/8/8/4k3/8/8/4KP2/8 w - - 0 1", [3]byte{6, 5, 4}},                                 // 3, ph 0
	{"8/8/4k3/8/8/4K3/4P3/6r1 w - - 0 1", [3]byte{5, 4, 2}},                              // 4, ph 2
	{"8/6k1/8/8/8/8/1r6/1K1R4 w - - 0 1", [3]byte{5, 4, 2}},                              // 4, ph 4
	{"8/8/3k4/8/8/3KB3/8/5n2 w - - 0 1", [3]byte{5, 4, 3}},                               // 4, ph 2
	{"8/8/4kp2/8/4K3/8/5P2/8 w - - 0 1", [3]byte{6, 5, 4}},                               // 4, ph 0
	{"6k1/5p1p/6p1/8/8/6P1/5P1P/6K1 w - - 0 1", [3]byte{4, 3, 2}},                        // 8, ph 0
	{"8/5ppk/8/8/8/8/5PPK/1R4r1 w - - 0 1", [3]byte{4, 3, 2}},                            // 8, ph 4
	{"8/4kp2/6p1/7p/3R3P/6P1/5PK1/4r3 w - - 0 1", [3]byte{3, 2, 1}},                      // 10, ph 4
	{"4k3/pp3ppp/8/8/8/8/PP3PPP/4K3 w - - 0 1", [3]byte{4, 3, 2}},                        // 12, ph 0
	{"4r1k1/5ppp/8/3n4/8/4B3/5PPP/4R1K1 w - - 0 1", [3]byte{2, 2, 1}},                    // 12, ph 6
	{"8/2b2pk1/1p4p1/p6p/P1n4P/1PN3P1/5PK1/2B5 w - - 0 1", [3]byte{2, 2, 1}},             // 16, ph 4
	{"2r3k1/5pp1/p6p/1p2p3/4P3/1P3P2/P5PP/2R3K1 w - - 0 1", [3]byte{2, 2, 1}},            // 16, ph 4
	{"r3k2r/pp3ppp/2n5/3p4/3P4/2N5/PP3PPP/R3K2R w KQkq - 0 1", [3]byte{1, 1, 0}},         // 20, ph 10
	{"2r2rk1/1p3pp1/p2p3p/4p3/4P3/1P1P1N1P/P4PP1/2R2RK1 w - - 0 1", [3]byte{1, 1, 0}},    // 21, ph 9
	{"r4rk1/pp2ppbp/2n3p1/8/3PP3/2N1B3/PP3PPP/R4RK1 w - - 0 1", [3]byte{0, 0, 0}},        // 23, ph 12
	{"r4rk1/pp2bppp/2n1pn2/8/3P4/2N1BN2/PP3PPP/R4RK1 w - - 0 1", [3]byte{0, 0, 0}},       // 24, ph 14
	{"r1b2rk1/pp2qppp/2n1pn2/3p4/3P4/2NBP3/PP3PPP/R1BQ1RK1 w - - 0 1", [3]byte{0, 0, 0}}, // 28, ph 22
}
