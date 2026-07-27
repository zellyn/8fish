package chesstest

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/zellyn/chess6502/internal/mirror"
	"github.com/zellyn/chess6502/internal/refchess"
)

// ---------------------------------------------------------------------------
// FULL-GAME move-for-move asm<->mirror parity.
//
// TestSearchMirrorParity proves the two engines agree on a handful of ROOT
// positions. That is a weak invariant: the positions are hand-picked, they are
// all openings/middlegames of the same flavour, and nothing ever walks the
// engines into the endgames, the near-mate scores, the repetition-adjacent
// shuffles, or the pathological quiet positions a real game produces.
//
// The strong invariant is: play COMPLETE GAMES with the two engines configured
// identically and require the SAME MOVE, the SAME SCORE, and the SAME TREE
// (search-node / make / eval / makenull counts) at EVERY ply. Every ply of
// every game is an independent parity test on a position no human chose, and
// the game walk is a coverage generator that reaches score ranges (mates,
// 50-move draws, insufficient-material draws) the fixed FEN list never does.
//
// This is the gate that would have caught the asm/tt.s unsigned mate-zone
// compare on the day it shipped, instead of it being modelled into the mirror
// as `TTPlyQuirk` and living for weeks.
//
// Both engines are driven FIXED-DEPTH from the referee's FEN with fresh state
// every ply (fresh 6502 machine = fresh TT/killers/history; fresh mirror
// Engine = ditto), so the comparison has no hidden carry-over: it is a pure
// function of (position, halfmove clock, depth, feature mask) on both sides.
// Budget/cycle modes are deliberately NOT used - they are not comparable
// across a 6502 emulator and a Go model.
// ---------------------------------------------------------------------------

// parityConfig is one (FEATURES, FEATURES2) tier to run games under, with the
// mirror-side knobs that model the same tier.
type parityConfig struct {
	name     string
	features byte // asm FEATURES byte
}

// mirrorEngine builds the mirror configured to model this asm feature mask.
// Only the bits the mirror knows how to model are accepted.
func (c parityConfig) mirrorEngine() *mirror.Engine {
	me := mirror.NewEngine()
	me.Features = c.features & 0x1f
	if c.features&ftCkExt != 0 {
		me.CheckExt = mirror.CheckExtParams{MaxExt: 1}
	}
	// (No quirk flags: asm/tt.s' mate-zone classification is a signed test
	// now, so the mirror's own signed-correct ttstore/ttprobe is the
	// faithful model, and TTPlyQuirk is deleted.)
	me.CycleTrack = true // exposes Cyc.Nodes/Makes/Evals/MakeNull
	return me
}

// parityConfigs are the tiers the engine actually ships and screens against.
// 0x5f is the SHIPPED gameplay mask (0x1f | FT_CKEXT) with FEATURES2 = 0;
// 0x1f is the plain tier every mirror screen is calibrated on.
var parityConfigs = []parityConfig{
	{"ship-0x5f", 0x1f | ftCkExt},
	{"plain-0x1f", 0x1f},
}

// parityExtraStarts are non-opening starts: tactical middlegames, pawn
// endgames, piece endgames, and near-mate positions. Games from these walk
// the engines through score ranges (mate scores, 50-move draws, insufficient
// material) that opening starts at shallow depth never reach.
var parityExtraStarts = []string{
	// Tactical / sharp middlegames.
	"r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1", // Kiwipete
	"r4rk1/1pp1qppp/p1np1n2/2b1p1B1/2B1P1b1/P1NP1N2/1PP1QPPP/R4RK1 w - - 0 10",
	"3r1rk1/pp3ppp/2p1bn2/q7/2BP4/P1N1P3/1P2QPPP/R4RK1 w - - 0 15",
	"2kr3r/ppp1qppp/2n1bn2/2b5/4P3/2NP1N2/PPPBQPPP/2KR3R w - - 0 10",
	"r2q1rk1/1b1nbppp/p2ppn2/1p6/3NPP2/2N1B3/PPPQB1PP/2KR3R w - - 0 12",
	"1k1r4/pp1b1R2/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - - 0 1",
	"rnbq1rk1/pp2ppbp/6p1/2p5/3PP3/2P2N2/P3BPPP/R1BQK2R b KQ - 0 9",
	"2r3k1/1p1b1pp1/p2p3p/4n3/2P1P3/1P4PP/P2N1PB1/2R3K1 w - - 0 25",
	// In-check roots.
	"8/8/8/4k3/8/4r3/8/4K3 w - - 0 1",
	"rnb1kbnr/pppp1ppp/8/4p3/6Pq/5P2/PPPPP2P/RNBQKBNR w KQkq - 1 3",
	// Pawn endgames (deep, quiet, promotion-bound: the mop-up/passed-pawn
	// eval and the mate-score bookkeeping both get exercised here).
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	"4k3/8/4K3/8/8/8/4P3/8 w - - 0 1",
	"8/5ppp/8/8/8/8/5PPP/4K1k1 w - - 0 1",
	"8/p7/8/1P6/8/8/7p/K6k w - - 0 1",
	"8/8/1p6/p1p5/P1P5/1P6/8/K6k w - - 0 1",
	"8/1p3pp1/p6p/8/8/P6P/1P3PP1/4K1k1 w - - 0 1",
	// Piece endgames.
	"6k1/5ppp/8/8/8/8/5PPP/R5K1 w - - 0 1",
	"8/8/8/3k4/8/3K4/3Q4/8 w - - 0 1",
	"8/8/4k3/8/8/3K4/8/3R4 w - - 0 1",
	"8/8/3k4/8/8/3K4/8/3BN3 w - - 0 1",
	"8/2k5/8/8/8/3K4/8/1R3R2 w - - 0 1",
	"4k3/8/8/8/8/8/4P3/4K2R w K - 0 1",
	"8/8/2k5/8/2n5/2K5/8/6B1 w - - 0 1",
	"r5k1/5ppp/8/8/8/8/5PPP/R5K1 w - - 0 1",
	"8/3k4/8/8/8/8/3PK3/8 w - - 0 1",
	"2k5/8/2K5/8/8/8/8/2Q5 w - - 0 1",
	// Near-mate: forces mate scores through the TT at many plies.
	"6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1",
	"7k/6pp/8/8/8/8/8/Q5K1 w - - 0 1",
	"5rk1/5ppp/8/8/8/8/5PPP/4R1K1 w - - 0 1",
	// Fortress / shuffle candidates: drive the 50-move and repetition paths.
	"8/8/8/8/8/8/2k5/K7 w - - 0 1",
	"8/8/3k4/8/8/3K4/8/7R w - - 40 1",
}

// loadPoolStarts reads the FENs from tools/openings-pool.epd.
func loadPoolStarts(t *testing.T) []string {
	t.Helper()
	f, err := os.Open("../../tools/openings-pool.epd")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// EPD: strip the opcode tail ("id ...;") and supply the missing
		// halfmove/fullmove fields so both FEN parsers agree.
		if i := strings.Index(line, " id "); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSuffix(strings.TrimSpace(line), ";")
		if n := len(strings.Fields(line)); n == 4 {
			line += " 0 1"
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// plyResult is one ply's comparison of the two engines.
type plyResult struct {
	cfg    string
	start  int
	ply    int
	fen    string
	aMove  string
	aScore int
	mMove  string
	mScore int
	// exact tree fingerprints: search()-entries, make()s, eval()s, makenull()s
	aNodes, aMakes, aEvals, aNull uint64
	mNodes, mMakes, mEvals, mNull uint64
	aQSNodes, mQSNodes            uint64
	aCycles                       uint64
}

func (r plyResult) moveDiverged() bool  { return r.aMove != r.mMove }
func (r plyResult) scoreDiverged() bool { return r.aScore != r.mScore }
func (r plyResult) treeDiverged() bool {
	return r.aNodes != r.mNodes || r.aMakes != r.mMakes ||
		r.aEvals != r.mEvals || r.aNull != r.mNull || r.aQSNodes != r.mQSNodes
}

func (r plyResult) String() string {
	return fmt.Sprintf(
		"[%s start=%d ply=%d] %s\n"+
			"    asm    move=%-6s score=%-7d nodes=%-8d makes=%-8d evals=%-8d null=%-6d qs=%-8d cyc=%d\n"+
			"    mirror move=%-6s score=%-7d nodes=%-8d makes=%-8d evals=%-8d null=%-6d qs=%-8d\n"+
			"    delta  nodes=%+d makes=%+d evals=%+d null=%+d qs=%+d",
		r.cfg, r.start, r.ply, r.fen,
		r.aMove, r.aScore, r.aNodes, r.aMakes, r.aEvals, r.aNull, r.aQSNodes, r.aCycles,
		r.mMove, r.mScore, r.mNodes, r.mMakes, r.mEvals, r.mNull, r.mQSNodes,
		int64(r.aNodes)-int64(r.mNodes), int64(r.aMakes)-int64(r.mMakes),
		int64(r.aEvals)-int64(r.mEvals), int64(r.aNull)-int64(r.mNull),
		int64(r.aQSNodes)-int64(r.mQSNodes))
}

type parityProbes struct {
	search, make_, eval, makenull uint16
}

// asmSearchFixed runs the asm engine over fen at fixed depth with the given
// FEATURES mask, returning the move, score and the four tree fingerprints.
func asmSearchFixed(bin []byte, probes parityProbes, fen string, depth byte, features byte) (mv string, score int, nodes, makes, evals, null, qs, cycles uint64, err error) {
	pos, err := ParseFEN(fen)
	if err != nil {
		return
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		return
	}
	SetFeatures(m, defs, features)
	SetFeatures2(m, defs, 0)
	SetBudget(m, defs, 0, depth)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove
	plyAddr, maxdAddr := defs["PLY"], defs["MAXDEPTH"]
	_, _, err = m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
		switch pc {
		case probes.search:
			nodes++
			// QS node: PLY >= MAXDEPTH at search ENTRY. The matching mirror
			// quantity is Cyc.NodesQS + Cyc.NodesEvasion, which chargeNode
			// books at the top of search() with the same predicate. It is
			// NOT Engine.QSNodes: that counter sits below the hard ply cap
			// and the 50-move / repetition / insufficient-material returns,
			// so it undercounts QS-ply nodes that bail out immediately (the
			// depth-5 sweep measured deltas of +1..+12 per move from exactly
			// that, on trees whose node/make/eval counts were identical).
			// MAXDEPTH is read live because check extensions mutate it.
			if m.Mem.Main[plyAddr] >= m.Mem.Main[maxdAddr] {
				qs++
			}
		case probes.make_:
			makes++
		case probes.eval:
			evals++
		case probes.makenull:
			null++
		}
	})
	if err != nil {
		return
	}
	score = int(int16(uint16(m.Mem.Main[defs["SCORE"]]) | uint16(m.Mem.Main[defs["SCORE"]+1])<<8))
	mv = MoveUCI(m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]], m.Mem.Main[defs["BESTFLAGS"]])
	cycles = m.Cycles
	return
}

// playParityGame plays one complete game from start, comparing both engines at
// every ply. It returns every ply's result. The game is driven by the ASM
// engine's move (the shipped engine is the referee of the game line), so a
// divergence does not end the game: later plies keep testing.
func playParityGame(bin []byte, probes parityProbes, cfg parityConfig, start string, startIdx int, depth byte, maxPlies int) ([]plyResult, error) {
	ref, err := refchess.ParseFEN(start)
	if err != nil {
		return nil, fmt.Errorf("start %q: %w", start, err)
	}
	// A start where the side NOT to move is in check is an ILLEGAL position:
	// the root has a king capture available, both engines' behavior there is
	// undefined (pseudo-legal generation happily "captures" the king), and
	// they do in fact diverge. Reject it loudly instead of scoring it as an
	// engine divergence. (Three of this file's own hand-written endgame
	// starts were illegal this way when the test was first run — that is
	// exactly the failure this guard turns into a clear message.)
	if sideNotToMoveInCheck(ref) {
		return nil, fmt.Errorf("ILLEGAL start %q: the side not to move is in check", start)
	}
	seen := map[uint64]int{}
	var out []plyResult
	for ply := 0; ply < maxPlies; ply++ {
		if ref.HalfmoveClock() >= 100 || ref.InsufficientMaterial() {
			break
		}
		seen[ref.ZobristKey()]++
		if seen[ref.ZobristKey()] >= 3 {
			break
		}
		if len(ref.LegalMoves()) == 0 {
			break // mate or stalemate
		}
		fen := ref.FEN()

		amv, asc, an, amk, aev, anull, aqs, acyc, err := asmSearchFixed(bin, probes, fen, depth, cfg.features)
		if err != nil {
			return out, fmt.Errorf("asm search %q: %w", fen, err)
		}

		mp, err := mirror.ParseFEN(fen)
		if err != nil {
			return out, fmt.Errorf("mirror ParseFEN %q: %w", fen, err)
		}
		me := cfg.mirrorEngine()
		me.SetPosition(mp)
		mb, msc := me.SearchFixed(int(depth))

		out = append(out, plyResult{
			cfg: cfg.name, start: startIdx, ply: ply, fen: fen,
			aMove: amv, aScore: asc, mMove: mb.UCI(), mScore: msc,
			aNodes: an, aMakes: amk, aEvals: aev, aNull: anull,
			mNodes: me.Cyc.Nodes, mMakes: me.Cyc.Makes,
			mEvals: me.Cyc.Evals, mNull: me.Cyc.MakeNull,
			aQSNodes: aqs, mQSNodes: me.Cyc.NodesQS + me.Cyc.NodesEvasion,
			aCycles: acyc,
		})

		mv, err := refchess.ParseMove(amv)
		if err != nil {
			return out, fmt.Errorf("asm move %q at %q: %w", amv, fen, err)
		}
		if err := ref.Make(mv); err != nil {
			return out, fmt.Errorf("ILLEGAL asm move %q at %q: %w", amv, fen, err)
		}
	}
	return out, nil
}

func envInt(name string, def int) int {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return def
}

// TestFullGameMirrorParity is the strong asm<->mirror gate: complete games,
// move for move, at fixed depth, under the shipped mask and the plain mask.
//
// Knobs (env): PARITY_DEPTH (default 4), PARITY_PLIES (max plies per game,
// default 6), PARITY_STARTS (cap on the number of starting positions, 0 =
// all), PARITY_CFG (substring filter on the config name).
//
// The default is sized for `make test` (~3 min on 8 cores: 71 starts x 2
// configs x 6 plies). The deep sweep the audit ran is
//
//	PARITY_DEPTH=4 PARITY_PLIES=30 go test ./internal/chesstest -run FullGame
//
// (~4,000 plies, ~25 min) plus a depth-6 subset; see docs/results.md.
func TestFullGameMirrorParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: asm emulator plays whole games")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	probes := parityProbes{
		search: labels["search"], make_: labels["make"],
		eval: labels["eval"], makenull: labels["makenull"],
	}
	for name, v := range map[string]uint16{"search": probes.search, "make": probes.make_,
		"eval": probes.eval, "makenull": probes.makenull} {
		if v == 0 {
			t.Fatalf("label %q missing from asm/engine.lbl", name)
		}
	}

	depth := byte(envInt("PARITY_DEPTH", 4))
	maxPlies := envInt("PARITY_PLIES", 6)
	starts := append(loadPoolStarts(t), parityExtraStarts...)
	if n := envInt("PARITY_STARTS", 0); n > 0 && n < len(starts) {
		starts = starts[:n]
	}
	cfgFilter := os.Getenv("PARITY_CFG")

	type job struct {
		cfg parityConfig
		idx int
	}
	var jobs []job
	for _, cfg := range parityConfigs {
		if cfgFilter != "" && !strings.Contains(cfg.name, cfgFilter) {
			continue
		}
		for i := range starts {
			jobs = append(jobs, job{cfg, i})
		}
	}

	var mu sync.Mutex
	var all []plyResult
	var errs []string
	var wg sync.WaitGroup
	ch := make(chan job)
	workers := runtime.NumCPU()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				res, err := playParityGame(bin, probes, j.cfg, starts[j.idx], j.idx, depth, maxPlies)
				mu.Lock()
				all = append(all, res...)
				if err != nil {
					errs = append(errs, fmt.Sprintf("[%s start=%d] %v", j.cfg.name, j.idx, err))
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()

	for _, e := range errs {
		t.Error(e)
	}

	sort.Slice(all, func(i, k int) bool {
		if all[i].cfg != all[k].cfg {
			return all[i].cfg < all[k].cfg
		}
		if all[i].start != all[k].start {
			return all[i].start < all[k].start
		}
		return all[i].ply < all[k].ply
	})

	var moveDiv, scoreDiv, treeDiv int
	var nodeExact, makeExact, evalExact, nullExact, qsExact int
	var reported int
	// Distribution of the make-count delta, for the QS-shape exactness check.
	makeDelta := map[int64]int{}
	nodeDelta := map[int64]int{}
	for _, r := range all {
		if r.aNodes == r.mNodes {
			nodeExact++
		}
		if r.aMakes == r.mMakes {
			makeExact++
		}
		if r.aEvals == r.mEvals {
			evalExact++
		}
		if r.aNull == r.mNull {
			nullExact++
		}
		if r.aQSNodes == r.mQSNodes {
			qsExact++
		}
		makeDelta[int64(r.aMakes)-int64(r.mMakes)]++
		nodeDelta[int64(r.aNodes)-int64(r.mNodes)]++
		md, sd, td := r.moveDiverged(), r.scoreDiverged(), r.treeDiverged()
		if md {
			moveDiv++
		}
		if sd {
			scoreDiv++
		}
		if td {
			treeDiv++
		}
		if (md || sd || td) && reported < 40 {
			reported++
			t.Errorf("DIVERGENCE #%d\n%s", reported, r)
		}
	}

	// Coverage numbers: a clean result is only as strong as what it touched.
	uniq := map[string]bool{}
	var mateScores, totalNodes uint64
	for _, r := range all {
		uniq[strings.Join(strings.Fields(r.fen)[:4], " ")] = true
		if r.aScore >= 29696 || r.aScore <= -29697 {
			mateScores++
		}
		totalNodes += r.aNodes
	}

	games := len(jobs)
	fmt.Printf("full-game parity: depth %d, %d games (%d starts x %d configs), %d plies compared\n",
		depth, games, len(starts), len(parityConfigs), len(all))
	fmt.Printf("  best move identical:      %d/%d\n", len(all)-moveDiv, len(all))
	fmt.Printf("  root score identical:     %d/%d\n", len(all)-scoreDiv, len(all))
	fmt.Printf("  search-node count exact:  %d/%d\n", nodeExact, len(all))
	fmt.Printf("  make count exact:         %d/%d\n", makeExact, len(all))
	fmt.Printf("  eval count exact:         %d/%d\n", evalExact, len(all))
	fmt.Printf("  makenull count exact:     %d/%d\n", nullExact, len(all))
	fmt.Printf("  QS-node count exact:      %d/%d\n", qsExact, len(all))
	fmt.Printf("  make-count delta histogram: %s\n", histString(makeDelta))
	fmt.Printf("  node-count delta histogram: %s\n", histString(nodeDelta))
	fmt.Printf("  coverage: %d distinct positions, %d plies scored in the mate zone, %d asm search nodes\n",
		len(uniq), mateScores, totalNodes)
	if len(all) == 0 {
		t.Fatal("no plies compared")
	}
}

func histString(h map[int64]int) string {
	keys := make([]int64, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%+d:%d", k, h[k])
	}
	return b.String()
}
