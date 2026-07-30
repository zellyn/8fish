package mirror

import (
	"math"
	"testing"
)

// asmRow is one ground-truth search from the asm differential harness
// (internal/chesstest TestMicroABPhase, engine.bin built from asm/ at main
// 5652dc1). cyc is the TRUE emulated-6502 cycle total; search/make/eval/
// attacked/ttprobe/gen are the asm operation-entry probe counts; and
// pcSearch/pcMake/pcEval/phSearch are the MATERIAL sums the harness
// accumulates at those same probes — the live piece count (PIECESQ slots not
// tombstoned) summed over every search/make/eval entry, and the engine's own
// taper PHASE summed over every search entry.
//
// The material sums are what make a phase-aware cost model fittable: a
// per-node cost of (Node + NodePhase*phase) predicts
// Node*search + NodePhase*phSearch cycles, which is linear in exactly these
// columns. See cycles.md.
type asmRow struct {
	mask, depth            byte
	fen                    string
	score                  int
	move                   string
	cyc                    uint64
	search, make_, eval    uint64
	attacked, ttprobe, gen uint64
	// Material sums over the operation's entries (see above).
	pcSearch, pcMake, pcEval uint64
	phSearch                 uint64
}

// microAB is the captured TestMicroABPhase ground truth: 69 fixed-depth
// searches = 23 positions x 3 feature masks (0x1f, 0x07, 0x00).
//
// The 23 positions span 3..32 root pieces. That spread is the point: the
// original calibration set was six positions, five of them 30-32 pieces plus
// a single 10-piece endgame, so the per-node cost's dependence on material
// was not identifiable at all — the one endgame was simply an outlier the
// frozen test carved out. Seventeen positions covering 3, 4, 4, 4, 4, 8, 8,
// 10, 12, 12, 16, 16, 20, 21, 23, 24 and 28 pieces were added so the
// material term is fit from a POPULATION, and so leave-one-position-out
// cross-validation can check it predicts material levels it never saw.
//
// Sparse positions are searched deeper than dense ones (the per-mask depth
// is in the row) so every search lands in the ~5M-950M cycle range; the
// model is per-operation, so depth is not a regressor and mixing depths is
// sound. Regenerate with:
//
//	go test ./internal/chesstest/ -run TestMicroABPhase -v
var microAB = []asmRow{
	// mask 0x1f.
	{0x1f, 6, "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", -40, "a3a4", 246454621, 53273, 69839, 49821, 48950, 8495, 8384, 1499756, 1994765, 1403478, 1103129},
	{0x1f, 6, "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", 354, "e1c1", 378687960, 89096, 101600, 83136, 63087, 13262, 13935, 2526904, 2915957, 2359845, 1925109},
	{0x1f, 6, "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", -38, "e2a6", 595751400, 105654, 207167, 97883, 212352, 24985, 16827, 2854648, 5584930, 2647469, 2148372},
	{0x1f, 6, "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 84, "c3d5", 451466864, 74627, 166801, 66420, 127526, 18723, 14615, 2113665, 4815902, 1880223, 1470759},
	{0x1f, 6, "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 96, "b4f4", 18194456, 4807, 6057, 4141, 3929, 1366, 1262, 39309, 51872, 33826, 17004},
	{0x1f, 6, "2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", -18, "d4c6", 106689506, 25474, 28790, 24065, 17315, 4039, 3734, 679927, 781589, 642589, 541007},
	{0x1f, 12, "8/8/8/4k3/8/8/4KP2/8 w - - 0 1", 163, "e2e3", 381756367, 111148, 123383, 72986, 116668, 59851, 34521, 330583, 370149, 218958, 2830},
	{0x1f, 11, "8/8/4k3/8/8/4K3/4P3/6r1 w - - 0 1", -446, "e3f4", 2483444072, 823121, 907417, 650196, 660786, 286978, 205373, 3138145, 3521446, 2497502, 1490204},
	{0x1f, 11, "8/6k1/8/8/8/8/1r6/1K1R4 w - - 0 1", 514, "b1b2", 185662853, 63754, 70188, 47742, 51876, 20477, 15448, 189681, 211127, 143549, 124346},
	{0x1f, 11, "8/8/3k4/8/8/3KB3/8/5n2 w - - 0 1", 0, "d3c2", 263232629, 78495, 99533, 58392, 76546, 24149, 19176, 305946, 398132, 233568, 148956},
	{0x1f, 12, "8/8/4kp2/8/4K3/8/5P2/8 w - - 0 1", 0, "e4d4", 661517695, 196929, 227488, 141168, 216900, 100095, 61606, 747382, 874367, 534524, 0},
	{0x1f, 10, "6k1/5p1p/6p1/8/8/6P1/5P1P/6K1 w - - 0 1", 7, "g1f1", 110327974, 32999, 35883, 27618, 29861, 12093, 9264, 259015, 283827, 216428, 0},
	{0x1f, 10, "8/5ppk/8/8/8/8/5PPK/1R4r1 w - - 0 1", 530, "h2g1", 387533865, 121255, 134678, 102503, 94595, 37496, 31274, 817692, 921861, 689543, 233398},
	{0x1f, 9, "8/4kp2/6p1/7p/3R3P/6P1/5PK1/4r3 w - - 0 1", -3, "d4c4", 608941128, 152522, 235557, 132031, 184165, 37681, 37200, 1471808, 2306937, 1271815, 564568},
	{0x1f, 10, "4k3/pp3ppp/8/8/8/8/PP3PPP/4K3 w - - 0 1", 10, "e1d2", 1625305884, 485357, 509269, 445563, 369281, 137703, 113376, 5770348, 6076964, 5294495, 0},
	{0x1f, 8, "4r1k1/5ppp/8/3n4/8/4B3/5PPP/4R1K1 w - - 0 1", -3, "g1f1", 165076285, 41450, 58779, 37468, 49176, 11056, 9838, 429039, 624411, 388031, 178463},
	{0x1f, 8, "8/2b2pk1/1p4p1/p6p/P1n4P/1PN3P1/5PK1/2B5 w - - 0 1", 312, "b3c4", 414515143, 116443, 130663, 108985, 85128, 34732, 22109, 1631215, 1875087, 1523392, 312535},
	{0x1f, 8, "2r3k1/5pp1/p6p/1p2p3/4P3/1P3P2/P5PP/2R3K1 w - - 0 1", 656, "c1c8", 40123233, 11288, 12573, 10257, 7031, 3880, 2392, 163622, 184849, 148566, 23094},
	{0x1f, 7, "r3k2r/pp3ppp/2n5/3p4/3P4/2N5/PP3PPP/R3K2R w KQkq - 0 1", 0, "a1d1", 560695158, 132655, 186801, 123687, 152798, 35010, 25568, 2432137, 3442274, 2268648, 1280550},
	{0x1f, 7, "2r2rk1/1p3pp1/p2p3p/4p3/4P3/1P1P1N1P/P4PP1/2R2RK1 w - - 0 1", 317, "d3d4", 152638473, 40964, 43855, 39319, 23962, 9929, 7150, 753339, 822406, 721920, 224489},
	{0x1f, 6, "r4rk1/pp2ppbp/2n3p1/8/3PP3/2N1B3/PP3PPP/R4RK1 w - - 0 1", 119, "d4d5", 110914093, 28585, 30777, 27778, 18666, 5407, 4005, 613418, 674818, 595813, 316720},
	{0x1f, 6, "r4rk1/pp2bppp/2n1pn2/8/3P4/2N1BN2/PP3PPP/R4RK1 w - - 0 1", 11, "h2h3", 512156075, 140541, 148331, 136912, 78940, 20892, 17216, 3170794, 3409773, 3087998, 1850293},
	{0x1f, 6, "r1b2rk1/pp2qppp/2n1pn2/3p4/3P4/2NBP3/PP3PPP/R1BQ1RK1 w - - 0 1", 20, "c1d2", 818540344, 188549, 236705, 173266, 127989, 28859, 30745, 4948896, 6262013, 4554253, 3959216},
	// mask 0x07.
	{0x07, 5, "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", -24, "a3a4", 230176239, 55781, 78831, 50855, 55005, 9593, 8679, 1563419, 2239026, 1422397, 1159997},
	{0x07, 5, "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", 349, "e1c1", 246506807, 81098, 84194, 75289, 48767, 8006, 8165, 2325312, 2439301, 2161455, 1763524},
	{0x07, 5, "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 7, "d5e6", 358628430, 106475, 149134, 99233, 131209, 5490, 7578, 3034128, 4214935, 2826438, 2209129},
	{0x07, 5, "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 77, "c3d5", 245247586, 54679, 97208, 48443, 66614, 5634, 7681, 1557763, 2796539, 1377864, 1115175},
	{0x07, 5, "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 102, "b4f4", 8053483, 2247, 3190, 1890, 2367, 432, 548, 18463, 27243, 15464, 7630},
	{0x07, 5, "2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", -29, "f1e2", 129759812, 37634, 45758, 34672, 29927, 4745, 4274, 1032249, 1264065, 950472, 809952},
	{0x07, 10, "8/8/8/4k3/8/8/4KP2/8 w - - 0 1", 108, "e2e3", 152432418, 45768, 51743, 29287, 50447, 23162, 13708, 136176, 155229, 87861, 3076},
	{0x07, 9, "8/8/4k3/8/8/4K3/4P3/6r1 w - - 0 1", -442, "e3f4", 2004865262, 645854, 706564, 362209, 541414, 375150, 200538, 2435853, 2732588, 1365258, 1179368},
	{0x07, 9, "8/6k1/8/8/8/8/1r6/1K1R4 w - - 0 1", 513, "b1b2", 184360076, 71191, 76706, 52834, 51169, 15156, 13447, 211817, 230273, 158594, 138870},
	{0x07, 9, "8/8/3k4/8/8/3KB3/8/5n2 w - - 0 1", 0, "d3c2", 222984438, 73341, 88292, 50861, 65364, 22585, 15836, 286396, 353168, 203444, 139714},
	{0x07, 10, "8/8/4kp2/8/4K3/8/5P2/8 w - - 0 1", 11, "f2f3", 372494367, 117685, 133895, 81086, 129422, 57651, 35014, 450629, 519718, 309126, 0},
	{0x07, 8, "6k1/5p1p/6p1/8/8/6P1/5P1P/6K1 w - - 0 1", 6, "g1f1", 32534552, 10643, 11353, 7118, 9710, 4394, 3153, 83045, 89488, 55277, 0},
	{0x07, 8, "8/5ppk/8/8/8/8/5PPK/1R4r1 w - - 0 1", 524, "b1g1", 100261978, 29093, 36048, 18257, 28613, 14228, 9322, 194303, 244703, 120244, 57884},
	{0x07, 7, "8/4kp2/6p1/7p/3R3P/6P1/5PK1/4r3 w - - 0 1", -8, "g2f3", 207479055, 53581, 84174, 43112, 66494, 13199, 13483, 513200, 820716, 412220, 194662},
	{0x07, 8, "4k3/pp3ppp/8/8/8/8/PP3PPP/4K3 w - - 0 1", 10, "e1d2", 1038924594, 346837, 355863, 291910, 262536, 111252, 85383, 4121708, 4243388, 3465066, 0},
	{0x07, 7, "4r1k1/5ppp/8/3n4/8/4B3/5PPP/4R1K1 w - - 0 1", 3, "g1f1", 537111373, 133763, 190934, 109854, 159899, 36651, 34637, 1429698, 2106858, 1165496, 627307},
	{0x07, 7, "8/2b2pk1/1p4p1/p6p/P1n4P/1PN3P1/5PK1/2B5 w - - 0 1", 308, "b3c4", 136758534, 42482, 49354, 36811, 32440, 9314, 9233, 591381, 700600, 511173, 114021},
	{0x07, 7, "2r3k1/5pp1/p6p/1p2p3/4P3/1P3P2/P5PP/2R3K1 w - - 0 1", 593, "c1c8", 38892179, 15285, 16138, 13216, 8217, 2233, 2291, 223277, 237419, 192969, 29738},
	{0x07, 6, "r3k2r/pp3ppp/2n5/3p4/3P4/2N5/PP3PPP/R3K2R w KQkq - 0 1", 11, "e1c1", 939034679, 221787, 338028, 196848, 297425, 50021, 42104, 4086268, 6283756, 3623875, 2135034},
	{0x07, 6, "2r2rk1/1p3pp1/p2p3p/4p3/4P3/1P1P1N1P/P4PP1/2R2RK1 w - - 0 1", 314, "d3d4", 164205765, 46786, 52969, 42238, 32007, 14439, 10214, 901927, 1033014, 813740, 322161},
	{0x07, 5, "r4rk1/pp2ppbp/2n3p1/8/3PP3/2N1B3/PP3PPP/R4RK1 w - - 0 1", 114, "a1d1", 189328194, 59761, 66419, 56291, 44722, 7327, 6983, 1279839, 1442028, 1204369, 672608},
	{0x07, 5, "r4rk1/pp2bppp/2n1pn2/8/3P4/2N1BN2/PP3PPP/R4RK1 w - - 0 1", 6, "f3e5", 492148420, 149399, 167348, 143274, 97944, 17268, 15729, 3375763, 3833430, 3235360, 1986244},
	{0x07, 5, "r1b2rk1/pp2qppp/2n1pn2/3p4/3P4/2NBP3/PP3PPP/R1BQ1RK1 w - - 0 1", 7, "d3c2", 1018722093, 276053, 362890, 254409, 206739, 21405, 35032, 7239380, 9547949, 6682325, 5809487},
	// mask 0x00.
	{0x00, 4, "r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8", -16, "a3a4", 80254104, 18616, 31979, 16121, 25720, 1434, 2342, 524502, 905856, 453493, 385134},
	{0x00, 4, "r2q1rk1/pp1nbppp/2p1pn2/3p2B1/2PP4/2N1PN2/PPQ2PPP/R3KB1R w KQ - 6 8", 343, "e1c1", 246003032, 63364, 67616, 53890, 33978, 5452, 9207, 1830557, 1998563, 1549004, 1386308},
	{0x00, 4, "r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", 14, "d5e6", 82705325, 14888, 27147, 11900, 30435, 2400, 2798, 416971, 758320, 328122, 313815},
	{0x00, 4, "r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10", 64, "c3d5", 270168845, 51639, 105634, 43276, 76332, 5121, 7859, 1490241, 3094057, 1239561, 1077031},
	{0x00, 4, "8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1", 93, "b4f4", 4838406, 1229, 2022, 854, 1618, 297, 318, 10454, 17954, 7010, 4196},
	{0x00, 4, "2rq1rk1/pp1bppbp/2np1np1/8/3NP3/2N1BP2/PPPQ2PP/2KR1B1R w - - 0 11", -42, "f1e2", 119078679, 29072, 42451, 25387, 28629, 2649, 3398, 804557, 1194113, 699308, 635174},
	{0x00, 8, "8/8/8/4k3/8/8/4KP2/8 w - - 0 1", 97, "e2e3", 72319181, 23180, 25803, 11873, 25476, 10557, 6066, 69218, 77409, 35619, 0},
	{0x00, 6, "8/8/4k3/8/8/4K3/4P3/6r1 w - - 0 1", -424, "e3e4", 118174971, 47311, 52219, 38830, 36781, 5416, 6535, 184783, 206410, 151999, 88204},
	{0x00, 6, "8/6k1/8/8/8/8/1r6/1K1R4 w - - 0 1", 517, "b1b2", 12783012, 4355, 4900, 2043, 4370, 2167, 1433, 13400, 15493, 6241, 9380},
	{0x00, 7, "8/8/3k4/8/8/3KB3/8/5n2 w - - 0 1", 0, "d3c2", 372756249, 121399, 159042, 80193, 128196, 25187, 21105, 472869, 636168, 320772, 230071},
	{0x00, 8, "8/8/4kp2/8/4K3/8/5P2/8 w - - 0 1", 26, "e4f4", 105765801, 36162, 40390, 19455, 39197, 16228, 9728, 141179, 158846, 75281, 0},
	{0x00, 6, "6k1/5p1p/6p1/8/8/6P1/5P1P/6K1 w - - 0 1", 11, "g1g2", 9389838, 3354, 3453, 1930, 3077, 1414, 1000, 26674, 27534, 15308, 0},
	{0x00, 6, "8/5ppk/8/8/8/8/5PPK/1R4r1 w - - 0 1", 511, "b1g1", 14706468, 4603, 5471, 2497, 4640, 2022, 1541, 30653, 36908, 16427, 9838},
	{0x00, 5, "8/4kp2/6p1/7p/3R3P/6P1/5PK1/4r3 w - - 0 1", -9, "g2f3", 39976847, 11708, 16946, 8798, 13146, 2027, 2590, 112593, 165254, 84208, 42628},
	{0x00, 6, "4k3/pp3ppp/8/8/8/8/PP3PPP/4K3 w - - 0 1", 10, "e1d2", 117613115, 43303, 43882, 30456, 33453, 12753, 9008, 518527, 525887, 364491, 0},
	{0x00, 5, "4r1k1/5ppp/8/3n4/8/4B3/5PPP/4R1K1 w - - 0 1", 4, "e1d1", 142073991, 38680, 57877, 30988, 51339, 5419, 7201, 414753, 637155, 329133, 181823},
	{0x00, 5, "8/2b2pk1/1p4p1/p6p/P1n4P/1PN3P1/5PK1/2B5 w - - 0 1", 291, "b3c4", 80518047, 28251, 33087, 24082, 22017, 2776, 3798, 397754, 474498, 337542, 83615},
	{0x00, 5, "2r3k1/5pp1/p6p/1p2p3/4P3/1P3P2/P5PP/2R3K1 w - - 0 1", 578, "c1c8", 10855979, 4459, 4797, 3869, 2913, 502, 556, 67330, 72708, 58342, 11626},
	{0x00, 4, "r3k2r/pp3ppp/2n5/3p4/3P4/2N5/PP3PPP/R3K2R w KQkq - 0 1", 14, "e1c1", 71236418, 17803, 27915, 14887, 26713, 2375, 2682, 335294, 530145, 278841, 175205},
	{0x00, 4, "2r2rk1/1p3pp1/p2p3p/4p3/4P3/1P1P1N1P/P4PP1/2R2RK1 w - - 0 1", 311, "d3d4", 22387254, 5825, 8707, 4500, 6177, 1083, 1194, 114823, 171357, 88195, 41831},
	{0x00, 4, "r4rk1/pp2ppbp/2n3p1/8/3PP3/2N1B3/PP3PPP/R4RK1 w - - 0 1", 117, "a1d1", 73383346, 20537, 24460, 17817, 16523, 2320, 2459, 440543, 531155, 380220, 227765},
	{0x00, 4, "r4rk1/pp2bppp/2n1pn2/8/3P4/2N1BN2/PP3PPP/R4RK1 w - - 0 1", 17, "h2h3", 291442649, 80726, 97502, 70581, 60221, 8825, 9405, 1850039, 2257447, 1610739, 1084124},
	{0x00, 4, "r1b2rk1/pp2qppp/2n1pn2/3p4/3P4/2NBP3/PP3PPP/R1BQ1RK1 w - - 0 1", 22, "c1d2", 303456470, 78567, 90954, 66832, 45470, 6339, 11346, 2076764, 2447836, 1760172, 1669892},
}

// replay runs one microAB row in the mirror with the asm-matching config
// and cycle accounting on, returning the engine and its cycle account.
func replay(t *testing.T, r asmRow) (*Engine, CycleAccount) {
	t.Helper()
	pos, err := ParseFEN(r.fen)
	if err != nil {
		t.Fatalf("ParseFEN %q: %v", r.fen, err)
	}
	e := NewEngine()
	e.Features = r.mask
	e.CycleTrack = true // accounting without a budget
	e.Seed = 0          // no dither (the harness leaves SEED = 0)
	e.SetPosition(pos)
	e.SearchFixed(int(r.depth))
	return e, e.Cyc
}

// TestCycleAccountingConsistency hand-checks the accounting invariants on a
// fixed search: the node-type counts partition the node total, every make
// has a matching unmake, the TT is probed exactly once per full-width node,
// and Est is EXACTLY the cost table dotted with the counters (the four
// priced ops plus the zero-priced ones). It also confirms accounting is
// fully inert when neither CycleBudget nor CycleTrack is set.
func TestCycleAccountingConsistency(t *testing.T) {
	pos, err := ParseFEN("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8")
	if err != nil {
		t.Fatal(err)
	}

	// Inert by default: no budget, no track => zero accounting.
	off := NewEngine()
	off.SetPosition(pos)
	off.SearchFixed(4)
	if off.Cyc != (CycleAccount{}) {
		t.Errorf("accounting ran with CycleBudget=0 && CycleTrack=false: %+v", off.Cyc)
	}

	e := NewEngine()
	e.CycleTrack = true
	e.SetPosition(pos)
	e.SearchFixed(4)
	c := e.Cyc

	if got := c.NodesFull + c.NodesQS + c.NodesEvasion; got != c.Nodes {
		t.Errorf("node partition %d != Nodes %d", got, c.Nodes)
	}
	if c.Makes != c.Unmakes {
		t.Errorf("makes %d != unmakes %d", c.Makes, c.Unmakes)
	}
	if mc := c.MakeQuiet + c.MakeCap + c.MakePromo + c.MakeCastle + c.MakeEP; mc != c.Makes {
		t.Errorf("make-class partition %d != Makes %d", mc, c.Makes)
	}
	if c.TTProbes != c.NodesFull {
		t.Errorf("ttprobes %d != full-width nodes %d (mirror probes once per full node)", c.TTProbes, c.NodesFull)
	}
	if c.EvalsExtra != 0 {
		t.Errorf("EvalsExtra %d without Extra terms", c.EvalsExtra)
	}
	if c.PhaseSum == 0 {
		t.Errorf("PhaseSum 0: the material term is not being accumulated")
	}
	// Est is exactly the priced dot product (generate/attacked/etc. priced
	// 0). The material term contributes NodePhase * (sum of phase over
	// nodes), which is why PhaseSum is a counter and not just a running
	// cost: it keeps Est reconstructible from the counters.
	k := e.Costs
	want := uint64(k.Node)*c.Nodes + uint64(k.NodePhase)*c.PhaseSum +
		uint64(k.Make)*c.Makes + uint64(k.Eval)*c.Evals +
		uint64(k.TTProbe)*c.TTProbes + uint64(k.Attacked)*c.Attacked +
		uint64(k.Generate)*c.Generates + uint64(k.MovePerGen)*c.MovesGen +
		uint64(k.MakeNull)*c.MakeNull + uint64(k.TTStore)*c.TTStores
	if want != c.Est {
		t.Errorf("Est %d != cost-dot-counts %d", c.Est, want)
	}
	t.Logf("d4 counts: nodes=%d (full=%d qs=%d eva=%d) makes=%d evals=%d ttp=%d phasesum=%d (mean phase %.1f) Est=%d",
		c.Nodes, c.NodesFull, c.NodesQS, c.NodesEvasion, c.Makes, c.Evals, c.TTProbes,
		c.PhaseSum, float64(c.PhaseSum)/float64(c.Nodes), c.Est)
}

// TestCycleBudgetDeterminism: SearchCycleBudget is a pure function of
// (position, budget, features) — the estimate never consults wall time —
// so two runs are bit-identical, the A/B soundness gate (same as
// TestBudgetDeterminism for node budgets).
func TestCycleBudgetDeterminism(t *testing.T) {
	fens := []string{
		"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
		"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1",
	}
	for _, budget := range []uint64{5_000_000, 50_000_000, 300_000_000} {
		for _, fen := range fens {
			pos, err := ParseFEN(fen)
			if err != nil {
				t.Fatal(err)
			}
			run := func() (Move, int, uint64, int) {
				e := NewEngine()
				e.SetPosition(pos)
				m, s := e.SearchCycleBudget(budget, MaxPly-1)
				return m, s, e.Cyc.Est, e.MaxDepth
			}
			m1, s1, est1, d1 := run()
			m2, s2, est2, _ := run()
			if m1 != m2 || s1 != s2 || est1 != est2 {
				t.Errorf("budget %d %s: non-deterministic: (%v,%d,%d) vs (%v,%d,%d)",
					budget, fen[:16], m1, s1, est1, m2, s2, est2)
			}
			if m1.From == NoSq {
				t.Errorf("budget %d %s: no move produced", budget, fen[:16])
			}
			t.Logf("cyc-budget %10d  %-16s best %s score %5d  est %10d  depth %d",
				budget, fen[:16], m1.UCI(), s1, est1, d1)
		}
	}
}

// TestCycleBudgetDiscountsFeature is the end-to-end proof of the whole
// exercise: under a FIXED cycle budget, taxing an eval term (the rook set,
// 219 cyc/call) makes the engine spend more of its budget per node and so
// search FEWER nodes / reach no deeper than the untaxed engine — i.e. the
// term's cost is automatically charged against it, which a node budget can
// never do. Compare that to the same term under a NODE budget, which grants
// it identical work for free.
func TestCycleBudgetDiscountsFeature(t *testing.T) {
	pos, err := ParseFEN("r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8")
	if err != nil {
		t.Fatal(err)
	}
	// Both engines run the rook term (identical eval => identical tree). The
	// ONLY difference is whether the term's 219-cyc/call cost is charged, so
	// any node-count difference is purely the tax biting into the budget.
	// Swept over budgets: the tax never lets the taxed engine out-search the
	// untaxed one, and at some budgets it costs it a whole iteration.
	//
	// The sweep is a dense grid rather than a handful of hand-picked
	// budgets. Budgeted iterative deepening decides up front whether to
	// START the next iteration, so node counts land on iteration boundaries
	// and the tax only shows as a node difference near one of those
	// boundaries; a sparse sweep can miss every boundary purely because the
	// cost table was recalibrated (which is what happened when the phase
	// term rescaled Est, and again when the pre-make evasion filter took
	// makes out of Est on 2026-07-30).
	//
	// DENSITY RULE, so the next recalibration does not silently reduce this
	// to a coincidence: the tax is worth roughly 4% of Est here (219 cycles
	// x ~1 eval/node against ~5000 cycles/node), so a boundary is only
	// straddled by budgets within ~4% above it. The STEP therefore has to
	// stay a few percent of the budget, not a fixed 20M that is 50% of the
	// first boundary and 3% of the last. The range stops at 320M because
	// the d7 boundary is the last one below it and bigger budgets only cost
	// time. bites is logged: if it ever drops toward 1, the grid has drifted
	// off the boundaries again and wants re-tuning, not deleting.
	var totUn, totTx uint64
	bit := false
	bites := 0
	var budgets []uint64
	for b := uint64(40_000_000); b <= 320_000_000; b += 4_000_000 {
		budgets = append(budgets, b)
	}
	for _, budget := range budgets {
		untaxed := NewEngine()
		untaxed.Extra, untaxed.Costs.EvalTerm = RookTermsAsm, 0
		untaxed.SetPosition(pos)
		untaxed.SearchCycleBudget(budget, MaxPly-1)
		taxed := NewEngine()
		taxed.Extra, taxed.Costs.EvalTerm = RookTermsAsm, RookTermCost
		taxed.SetPosition(pos)
		taxed.SearchCycleBudget(budget, MaxPly-1)
		if taxed.Nodes > untaxed.Nodes {
			t.Errorf("budget %d: taxed searched MORE nodes (%d) than untaxed (%d)", budget, taxed.Nodes, untaxed.Nodes)
		}
		if taxed.Nodes < untaxed.Nodes {
			bit = true
			bites++
		}
		totUn += untaxed.Nodes
		totTx += taxed.Nodes
		t.Logf("cyc-budget %10d: untaxed nodes=%7d d%d | taxed nodes=%7d d%d",
			budget, untaxed.Nodes, untaxed.MaxDepth, taxed.Nodes, taxed.MaxDepth)
	}
	if !bit {
		t.Errorf("the rook-term tax never reduced node count across the budget sweep")
	}
	t.Logf("swept totals: untaxed %d nodes vs taxed %d nodes (%.1f%% fewer under the tax); "+
		"the tax bit at %d of %d budgets (see the density rule above)",
		totUn, totTx, 100*(1-float64(totTx)/float64(totUn)), bites, len(budgets))

	// A NODE budget cannot see the tax at all: identical tree => identical
	// nodes/depth regardless of EvalTermsCost. That is exactly the blind
	// spot the cycle budget fixes.
	un := NewEngine()
	un.Extra, un.Costs.EvalTerm = RookTermsAsm, 0
	un.SetPosition(pos)
	un.SearchBudget(200000, MaxPly-1)
	tn := NewEngine()
	tn.Extra, tn.Costs.EvalTerm = RookTermsAsm, RookTermCost
	tn.SetPosition(pos)
	tn.SearchBudget(200000, MaxPly-1)
	if un.Nodes != tn.Nodes {
		t.Errorf("node budget: EvalTermsCost changed the search (%d vs %d nodes) — it must not", un.Nodes, tn.Nodes)
	}
	t.Logf("node budget 200000, rook term on both: nodes=%d depth=%d regardless of EvalTermsCost (term untaxed)",
		un.Nodes, un.MaxDepth)
}

// nnls solves min||X c - y|| subject to c >= 0 (no intercept: zero ops =
// zero cycles) by clamped coordinate descent. The unconstrained OLS on
// these collinear operation counts produces physically meaningless
// negative costs; non-negativity yields an interpretable, transferable
// per-operation cost vector.
func nnls(X [][]float64, y []float64) []float64 {
	n, k := len(X), len(X[0])
	c := make([]float64, k)
	// Precompute column norms.
	norm := make([]float64, k)
	for j := 0; j < k; j++ {
		var s float64
		for r := 0; r < n; r++ {
			s += X[r][j] * X[r][j]
		}
		norm[j] = s
	}
	resid := make([]float64, n)
	copy(resid, y) // c starts at 0 so residual = y
	for iter := 0; iter < 5000; iter++ {
		var maxDelta float64
		for j := 0; j < k; j++ {
			if norm[j] == 0 {
				continue
			}
			// Partial residual excluding column j.
			var dot float64
			for r := 0; r < n; r++ {
				dot += X[r][j] * (resid[r] + X[r][j]*c[j])
			}
			nc := dot / norm[j]
			if nc < 0 {
				nc = 0
			}
			d := nc - c[j]
			if d != 0 {
				for r := 0; r < n; r++ {
					resid[r] -= X[r][j] * d
				}
				c[j] = nc
				if math.Abs(d) > maxDelta {
					maxDelta = math.Abs(d)
				}
			}
		}
		if maxDelta < 1e-6 {
			break
		}
	}
	return c
}

// TestFullWidthParity documents where the mirror tree tracks the asm and
// where it diverges. It is INFORMATIONAL (never fails): the mirror's
// full-width TT-probe count matches the asm's exactly at mask 0x00
// (identical full-width tree), but at masks with search features
// (null/futility/LMR) the full-width trees drift, and at ALL masks the
// mirror's quiescence tree is larger than the asm's (the asm QS prunes /
// inlines more). This is the reason the cycle model is calibrated on the
// asm's OWN operation counts (TestCycleModelFit), not on mirror counts.
func TestFullWidthParity(t *testing.T) {
	for _, r := range microAB {
		_, c := replay(t, r)
		ttRatio := float64(c.TTProbes) / float64(r.ttprobe)
		// asm QS ~= asm search - asm full-width (ttprobe ~= full-width).
		asmQS := int(r.search) - int(r.ttprobe)
		qsRatio := float64(c.NodesQS) / float64(asmQS)
		t.Logf("m%02x %-14s full: mirror-ttp %d vs asm-ttp %d (%.2f) | QS: mirror %d vs asm~%d (%.2f)",
			r.mask, r.fen[:14], c.TTProbes, r.ttprobe, ttRatio, c.NodesQS, asmQS, qsRatio)
	}
}

// TestPhaseTransfers is the transfer guard for the model's new regressor.
//
// The reduced regressor set exists so the asm-fitted costs can be applied to
// the MIRROR's operation counts without distortion. That argument was made
// for counts (node/make/eval/ttprobe fire at similar per-node rates in both
// engines); the phase term needs its own version of it, because it is
// charged per node MULTIPLIED by the node's material. What has to transfer
// is the mirror's MEAN PHASE PER NODE: if the mirror's tree sat at
// systematically different material from the asm's — e.g. if its quiescence
// search chased captures further, landing on emptier boards — the same
// coefficient would mis-charge the mirror.
//
// It does not, and by a wider margin than the old QS-inflation caveat in
// cycles.md would suggest: on these 69 fixed-depth searches the mirror
// reproduces the asm's node count EXACTLY, and its mean phase per node
// matches to 0.00%. Bound: 10%, i.e. this test is a drift alarm, not a
// statement that 0% is required.
func TestPhaseTransfers(t *testing.T) {
	if testing.Short() {
		t.Skip("replays 69 searches in the mirror")
	}
	var worst float64
	var worstRow asmRow
	sameNodes := 0
	for _, r := range microAB {
		_, c := replay(t, r)
		if c.Nodes == r.search {
			sameNodes++
		}
		mirror := float64(c.PhaseSum) / float64(c.Nodes)
		asm := float64(r.phSearch) / float64(r.search)
		var d float64
		if asm > 0 {
			d = 100 * (mirror - asm) / asm
		} else if mirror != 0 { // asm mean phase 0 (pawn endgame): so must the mirror's be
			t.Errorf("m%02x %s: asm mean phase 0 but mirror %.2f", r.mask, r.fen[:16], mirror)
		}
		if math.Abs(d) >= math.Abs(worst) {
			worst, worstRow = d, r
		}
		t.Logf("m%02x %2dpc %-16s mean phase/node: mirror %6.3f (%d nodes) vs asm %6.3f (%d nodes) (%+.2f%%)",
			r.mask, rootPieces(r.fen), r.fen[:16], mirror, c.Nodes, asm, r.search, d)
		if math.Abs(d) > 10 {
			t.Errorf("m%02x %s: mirror mean phase/node %.2f vs asm %.2f (%+.1f%%, >10%%): the phase term does not transfer",
				r.mask, r.fen[:16], mirror, asm, d)
		}
	}
	t.Logf("worst mean-phase-per-node divergence: %+.1f%% (%s m%02x)",
		worst, worstRow.fen[:16], worstRow.mask)
	// Reported, not asserted: the mirror is free to diverge in tree shape (it
	// is a research engine), and the phase bound above is the thing the cost
	// model actually needs. But today it does not diverge at all, which is
	// worth knowing when reading any mirror-vs-asm claim.
	t.Logf("mirror node count identical to the asm on %d of %d searches", sameNodes, len(microAB))
}

// rootPieces counts the pieces in a FEN's board field — the "how much
// material is on the board" axis the calibration set was built to span.
func rootPieces(fen string) int {
	n := 0
	for _, c := range fen {
		switch {
		case c == ' ':
			return n
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			n++
		}
	}
	return n
}

// colVal projects one ground-truth row onto a named regressor.
func colVal(r asmRow, name string) float64 {
	switch name {
	case "search":
		return float64(r.search)
	case "phase":
		return float64(r.phSearch)
	case "pieces":
		return float64(r.pcSearch)
	case "make":
		return float64(r.make_)
	case "pcmake":
		return float64(r.pcMake)
	case "eval":
		return float64(r.eval)
	case "pceval":
		return float64(r.pcEval)
	case "attacked":
		return float64(r.attacked)
	case "ttprobe":
		return float64(r.ttprobe)
	case "generate":
		return float64(r.gen)
	}
	panic("bad col " + name)
}

// fitRel fits the asm cycle total onto the named asm-count columns, through
// the origin (zero ops = zero cycles) and constrained non-negative.
//
// It minimizes RELATIVE error: every row is divided by its own cycle total
// before the least-squares solve. That is not cosmetic. The calibration set
// spans 4.8M to 2.5G cycles per search, so plain least squares is a
// cycle-weighted average that lets the handful of huge midgame searches set
// every coefficient and leaves the cheap low-material ones with almost no
// leverage — which is exactly how the old constant-cost model came to
// over-price endgame nodes by ~30% while still "fitting". The budget is used
// proportionally (a screen cares that a node costs the right FRACTION of the
// budget), so proportional error is the right loss.
func fitRel(data []asmRow, cols []string) []float64 {
	X := make([][]float64, len(data))
	y := make([]float64, len(data))
	for i, r := range data {
		row := make([]float64, len(cols))
		for j, name := range cols {
			row[j] = colVal(r, name) / float64(r.cyc)
		}
		X[i] = row
		y[i] = 1 // = cyc/cyc
	}
	return nnls(X, y)
}

// fitStats summarizes a model's predicted-vs-actual behavior.
type fitStats struct {
	worst     float64 // worst |relative error|, percent
	worstRow  asmRow
	rms       float64 // RMS relative error, percent
	over15    int     // rows worse than 15%
	maskErr   map[byte]float64
	poolRatio float64 // sum(actual)/sum(predicted) over all rows
	lowRatio  float64 // ... over rows with <= lowMaterial root pieces
	highRatio float64 // ... over the rest
}

// lowMaterial is the root piece count at or below which a calibration
// position counts as "low material" for the bias check. 12 pieces is two
// kings plus ten men: rook-and-pawn endings and below.
const lowMaterial = 12

func evalModel(t *testing.T, data []asmRow, cols []string, coef []float64, verbose bool) fitStats {
	s := fitStats{maskErr: map[byte]float64{}}
	maskAct, maskPred := map[byte]float64{}, map[byte]float64{}
	var poolA, poolP, lowA, lowP float64
	var sq float64
	for _, r := range data {
		var p float64
		for j, name := range cols {
			p += coef[j] * colVal(r, name)
		}
		act := float64(r.cyc)
		e := 100 * (p - act) / act
		sq += (e / 100) * (e / 100)
		if math.Abs(e) > s.worst {
			s.worst, s.worstRow = math.Abs(e), r
		}
		if math.Abs(e) > 15 {
			s.over15++
		}
		maskAct[r.mask] += act
		maskPred[r.mask] += p
		poolA, poolP = poolA+act, poolP+p
		if rootPieces(r.fen) <= lowMaterial {
			lowA, lowP = lowA+act, lowP+p
		}
		if verbose {
			t.Logf("  m%02x %2dpc d%d %-24s act=%11.0f pred=%11.0f err=%+6.1f%%",
				r.mask, rootPieces(r.fen), r.depth, r.fen[:24], act, p, e)
		}
	}
	s.rms = 100 * math.Sqrt(sq/float64(len(data)))
	for m, a := range maskAct {
		s.maskErr[m] = 100 * (maskPred[m] - a) / a
	}
	s.poolRatio = poolA / poolP
	s.lowRatio = lowA / lowP
	s.highRatio = (poolA - lowA) / (poolP - lowP)
	return s
}

// cvRMS is leave-one-POSITION-out cross-validation: hold out all three
// mask-rows of one FEN, refit, and score the held-out rows. Holding out a
// whole position (not a row) is what makes this a real test of the material
// term — a model that has merely memorized one endgame cannot predict an
// endgame it has never seen.
func cvRMS(data []asmRow, cols []string) (rms, worst float64) {
	seen := map[string]bool{}
	var sq float64
	var n int
	for _, r := range data {
		if seen[r.fen] {
			continue
		}
		seen[r.fen] = true
		var train, test []asmRow
		for _, o := range data {
			if o.fen == r.fen {
				test = append(test, o)
			} else {
				train = append(train, o)
			}
		}
		coef := fitRel(train, cols)
		for _, o := range test {
			var p float64
			for j, name := range cols {
				p += coef[j] * colVal(o, name)
			}
			e := (p - float64(o.cyc)) / float64(o.cyc)
			sq += e * e
			n++
			if math.Abs(e)*100 > worst {
				worst = math.Abs(e) * 100
			}
		}
	}
	return 100 * math.Sqrt(sq/float64(n)), worst
}

// TestCycleModelFit derives the per-operation 6502 cycle costs from the
// microAB ground truth by regressing the asm cycle total onto the asm
// operation counts, and compares the candidate model forms. Run with -v.
//
// The headline comparison is CONST vs PHASE:
//
//   - CONST is the old shipped form [search, make, eval, ttprobe]. Its
//     per-node cost cannot depend on how much material is on the board, so
//     it must over-charge low-material nodes — the defect this test now
//     measures directly (lowRatio, the actual/predicted ratio over the
//     positions with <= 12 root pieces).
//
//   - PHASE adds ONE regressor: the sum of the node's taper phase over all
//     search entries, i.e. it lets the per-node cost be Node +
//     NodePhase*phase instead of a constant. This is the shipped form.
//
// PIECES is the same idea with live piece count instead of phase, and is
// reported because it is the other obvious material regressor; phase wins
// (see cycles.md) and is free at runtime, since both engines already
// maintain it incrementally.
func TestCycleModelFit(t *testing.T) {
	models := []struct {
		name string
		cols []string
	}{
		{"CONST  (old shipped)", []string{"search", "make", "eval", "ttprobe"}},
		{"PHASE  (new shipped)", []string{"search", "phase", "make", "eval", "ttprobe"}},
		{"PIECES", []string{"search", "pieces", "make", "eval", "ttprobe"}},
		{"PHASE+per-eval pieces", []string{"search", "phase", "make", "eval", "pceval", "ttprobe"}},
		{"PHASE+generate", []string{"search", "phase", "make", "eval", "ttprobe", "generate"}},
	}
	for _, m := range models {
		coef := fitRel(microAB, m.cols)
		t.Logf("== %s ==", m.name)
		for i, c := range m.cols {
			t.Logf("   %-9s %10.1f", c, coef[i])
		}
		s := evalModel(t, microAB, m.cols, coef, false)
		cv, cvWorst := cvRMS(microAB, m.cols)
		for _, mask := range []byte{0x1f, 0x07, 0x00} {
			t.Logf("   mask %#02x grand-total err %+.2f%%", mask, s.maskErr[mask])
		}
		t.Logf("   worst per-search %.1f%% (%s m%02x) | RMS %.1f%% | rows>15%% %d",
			s.worst, s.worstRow.fen[:20], s.worstRow.mask, s.rms, s.over15)
		t.Logf("   ratio act/pred: pool %.4f | <=%dpc %.4f | rest %.4f",
			s.poolRatio, lowMaterial, s.lowRatio, s.highRatio)
		t.Logf("   leave-one-position-out CV: RMS %.1f%% worst %.1f%%", cv, cvWorst)
	}
}

// TestCycleModelFrozen checks the FROZEN DefaultCycleCosts (the reduced
// [node, node*phase, make, eval, ttprobe] model shipped in cycles.go)
// applied to the asm operation counts (the same tree that produced the cycle
// total). The reduced model deliberately drops the generate/attacked
// regressors (whose mirror/asm per-node frequency diverges) so the costs
// transfer cleanly to the mirror's counts; it trades a little asm-side
// accuracy for that.
//
// Guards, all applied to ALL 69 searches — there is no longer an endgame
// carve-out. The old model's per-node cost could not depend on material, so
// it over-charged the single low-material position by 30-47% and the frozen
// test simply excluded any FEN starting "8/" from its per-search check. With
// the phase term and a calibration set spanning 3-32 pieces, endgames are
// held to the same bound as everything else, and two further guards below
// assert the absence of the material BIAS itself.
//
//  1. every search within 21% (worst is 20.4%, and it is a 32-piece
//     MIDGAME position, not a low-material one — the residual is no longer
//     material-structured);
//  2. per-mask grand total within 8% (was 12%; the fit now lands inside
//     4.2%);
//  3. the low-material and high-material actual/predicted ratios both
//     within 5% of 1.0 — the direct statement of "the model does not
//     systematically over- or under-price a phase of the game". This is the
//     guard that would have caught the original defect (its low-material
//     ratio was 0.71).
func TestCycleModelFrozen(t *testing.T) {
	k := DefaultCycleCosts
	predict := func(r asmRow) float64 {
		return fittedConst +
			k.Node*float64(r.search) +
			k.NodePhase*float64(r.phSearch) +
			k.Make*float64(r.make_) +
			k.Eval*float64(r.eval) +
			k.TTProbe*float64(r.ttprobe)
	}
	maskAct := map[byte]float64{}
	maskPred := map[byte]float64{}
	var worst float64
	var worstRow asmRow
	var lowAct, lowPred, hiAct, hiPred float64
	for _, r := range microAB {
		p := predict(r)
		act := float64(r.cyc)
		e := 100 * (p - act) / act
		if math.Abs(e) > worst {
			worst, worstRow = math.Abs(e), r
		}
		maskAct[r.mask] += act
		maskPred[r.mask] += p
		if rootPieces(r.fen) <= lowMaterial {
			lowAct, lowPred = lowAct+act, lowPred+p
		} else {
			hiAct, hiPred = hiAct+act, hiPred+p
		}
		if math.Abs(e) > 21 {
			t.Errorf("m%02x %2dpc %s: frozen err %+.1f%% (>21%%) act=%.0f pred=%.0f",
				r.mask, rootPieces(r.fen), r.fen[:16], e, act, p)
		}
	}
	for _, m := range []byte{0x1f, 0x07, 0x00} {
		e := 100 * (maskPred[m] - maskAct[m]) / maskAct[m]
		t.Logf("mask %#02x grand-total frozen err %+.2f%%", m, e)
		if math.Abs(e) > 8 {
			t.Errorf("mask %#02x grand-total frozen err %+.2f%% exceeds 8%%", m, e)
		}
	}
	low, hi := lowAct/lowPred, hiAct/hiPred
	t.Logf("actual/predicted ratio: <=%dpc %.4f (%d searches) | >%dpc %.4f",
		lowMaterial, low, countLow(microAB), lowMaterial, hi)
	for _, c := range []struct {
		name  string
		ratio float64
	}{{"low-material", low}, {"high-material", hi}} {
		if math.Abs(ratio1(c.ratio)) > 5 {
			t.Errorf("%s actual/predicted ratio %.4f is %+.1f%% off 1.0 (>5%%): the model is material-biased",
				c.name, c.ratio, ratio1(c.ratio))
		}
	}
	t.Logf("worst per-search frozen err: %.1f%% (%s, %d pieces, m%02x)",
		worst, worstRow.fen[:20], rootPieces(worstRow.fen), worstRow.mask)
}

// ratio1 returns how far a ratio is from 1.0, in percent.
func ratio1(r float64) float64 { return 100 * (r - 1) }

func countLow(rows []asmRow) int {
	n := 0
	for _, r := range rows {
		if rootPieces(r.fen) <= lowMaterial {
			n++
		}
	}
	return n
}

// TestEvalTermTax validates deliverable #4: an eval term costing 219
// cycles/call (the asm FT_ROOKX extraterm) is ~4-6% of all cycles, so
// under a fixed cycle budget it discounts effective search by that much.
// It computes the fraction directly from the asm ground truth
// (219*eval_calls / total_cycles) per mask, and confirms the mirror's
// EvalTerm accounting reproduces the same per-eval surcharge.
func TestEvalTermTax(t *testing.T) {
	maskEval := map[byte]uint64{}
	maskCyc := map[byte]uint64{}
	for _, r := range microAB {
		maskEval[r.mask] += r.eval
		maskCyc[r.mask] += r.cyc
	}
	for _, m := range []byte{0x1f, 0x07, 0x00} {
		frac := 100 * RookTermCost * float64(maskEval[m]) / float64(maskCyc[m])
		t.Logf("mask %#02x: rook term %g cyc/eval-call over %d eval calls = %.2f%% of %d cycles",
			m, RookTermCost, maskEval[m], frac, maskCyc[m])
	}

	// Mirror side: run WITH the rook term and its 219-cyc/call tax, then
	// read the term's share of the engine's own estimated cycles. (Enabling
	// the term changes the eval and thus the tree, so this is measured on
	// the taxed engine directly, not by differencing against an untaxed
	// run.)
	r := microAB[0] // m1f fen0
	pos, _ := ParseFEN(r.fen)
	taxed := NewEngine()
	taxed.Features, taxed.CycleTrack, taxed.Seed = r.mask, true, 0
	taxed.Extra = RookTermsAsm
	taxed.Costs.EvalTerm = RookTermCost
	taxed.SetPosition(pos)
	taxed.SearchFixed(int(r.depth))

	if taxed.Cyc.EvalsExtra != taxed.Cyc.Evals {
		t.Errorf("EvalsExtra %d != Evals %d (term should fire on every eval)", taxed.Cyc.EvalsExtra, taxed.Cyc.Evals)
	}
	added := uint64(RookTermCost) * taxed.Cyc.EvalsExtra
	frac := 100 * float64(added) / float64(taxed.Cyc.Est)
	t.Logf("mirror m1f fen0 (rook term on): term %d cyc over %d eval calls = %.2f%% of Est %d",
		added, taxed.Cyc.EvalsExtra, frac, taxed.Cyc.Est)
	// The whole point (deliverable #4): the rook term's mirror-side cycle
	// fraction lands in the same 4-6% band the asm ground truth shows, so a
	// cycle budget discounts its Elo by that much. If this drifts out of
	// band, the runtime cost coefficients need re-tuning.
	if frac < 3.5 || frac > 6.5 {
		t.Errorf("mirror rook-term fraction %.2f%% outside the 3.5-6.5%% band (asm reality ~4-6%%)", frac)
	}
}
