// Command ponder-match measures the value of the DEVICE's real pondering by
// playing 8fish against 8fish with BOTH sides running the real m8.s UI under
// the harness emulator (ui.Boot, the HARNESSKBD build), relaying moves between
// them as typed keystrokes — the way cmd/sargon-symmatch drives the real
// Sargon disk. One side ponders (PONDERON=1, the shipped default); the other
// has pondering turned off (PONDERON=0, the same poke every move-by-move gate
// uses, i.e. exactly the pre-ponder shipped behaviour). The Elo between them
// is the device ponder's worth.
//
// WHY THIS EXISTS. Every published ponder Elo (~+116 vs Sargon) came from
// internal/ucibridge's SIMULATED ponder, whose predictor is the free-TT walk
// ("source a", ponderPrediction). The device (asm/m8.s m8ponder) predicts with
// a SHALLOW SEARCH ("source b", PPREDDEPTH=3). Nobody has measured what the
// device's actual mechanism is worth. This harness runs the actual mechanism:
// the real m8ponder, the real pkclk keyboard-poll interrupt, the real
// snapshot/restore, the real warm TT carried into the real reply search.
//
// THE PER-MOVE LOOP (P = the pondering machine, N = the non-pondering one):
//
//  1. P plays its move M (a normal typed-RETURN engine turn). Its input
//     loop's per-key wait (asm/m8.s urdkey) then opens a ponder burst —
//     m8ponder predicts N's reply, makes it, and parks at the first pkclk
//     keyboard poll (the harness breaks Run on a status read with no key
//     pending). On the device the wait re-opens a fresh burst after EVERY
//     keystroke of N's move as it is typed in; each of those micro-bursts
//     aborts on the next key and is booked as ponder overhead below.
//  2. M is typed into N; N's engine computes its reply S, spending T_N
//     emulated cycles under its own level budget.
//  3. P is granted a PONDER WINDOW of exactly T_N cycles: its emulator is run
//     with the keyboard trap disabled (a hardware $C000 read just returns "no
//     key"), so the deep ponder search runs the way it does on an Apple IIe
//     while the opponent thinks. This is the concurrency bookkeeping: the two
//     emulators are separate, so "P ponders while N thinks" is realised as
//     "run P for the cycles N just measurably spent".
//  4. S is typed into P. The first keystroke lands in pkclk, which aborts the
//     ponder (discarding the guess, keeping the warm TT — the round-6
//     recovery trap is on the device, already gated). P's reply search then
//     runs with whatever TT state the ponder left: warm for exactly root+M+S
//     on a HIT (S == prediction), stale-but-verified on a MISS.
//  5. Symmetrically the other way, except N never gets a window (it does not
//     ponder — that asymmetry IS the measurement).
//
// ACCOUNTING. Pondering is FREE time on real hardware — the machine is
// otherwise idle on the opponent's clock. So the ponderer legitimately spends
// its own budget PLUS a window per move, and the Elo is the value of that
// free compute given the device's real predictor and interrupt path. The
// asymmetry is NOT equalised away; both sides' cycle totals are logged so it
// is visible and auditable.
//
// SOUNDNESS.
//   - The no-ponder side is the shipped no-ponder behaviour: PONDERON=0 makes
//     m8ponder return immediately (one flag test off the search path).
//     -mode control plays noponder-vs-noponder and must come out ~0 Elo.
//   - Every injected move is read back from the receiving machine's own
//     history (and the full position is cross-checked against a refchess
//     referee every ply); a relay that drops or mangles a move aborts loudly.
//   - Determinism: one PRNG per game, seeded from -seed and the game number,
//     drives the entropy-collector pokes (the "when did the human press the
//     key" randomness). The same seed replays the same match.
//   - Warm-TT guard: the reply depth after a HIT is aggregated against the
//     reply depth after a MISS. If hits do not search deeper for the same
//     budget, the instrument (or the device) is broken and the summary says
//     so; the per-image proof is internal/ui's TestDevicePonderHitHeadStart.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/engsyms"
	"github.com/zellyn/chess6502/internal/refchess"
	"github.com/zellyn/chess6502/internal/sprt"
	"github.com/zellyn/chess6502/internal/ui"
)

func main() {
	var (
		root     = flag.String("root", ".", "repository root (asm/ images are built here)")
		mode     = flag.String("mode", "measure", "measure: ponder-vs-noponder; control: noponder-vs-noponder (the ~0-Elo sanity gate)")
		pairs    = flag.Int("pairs", 100, "opening pairs; games = 2*pairs (colors swapped within a pair)")
		level    = flag.Int("level", 5, "UI level for BOTH sides (5 = 4 s/move on the estimated clock; 1-4 are fixed-depth)")
		seed     = flag.Uint64("seed", 1, "match seed: drives the per-game entropy-collector pokes (deterministic replay)")
		openSeed = flag.Uint64("openseed", 0, "opening-generation seed (0 = sprt's historical default)")
		parallel = flag.Int("parallel", 0, "concurrent games (0 = NumCPU, capped at 8)")
		outfile  = flag.String("out", "", "append the full per-move log to this file (stdout gets games + summaries)")
		maxPlies = flag.Int("max-plies", 220, "adjudicate a draw at this many plies (UI history is byte-capped at 255)")
		noBuild  = flag.Bool("nobuild", false, "skip rebuilding the asm images (trust what is on disk)")
		pairSeed = flag.Bool("pairseed", false, "INSTRUMENT SYMMETRY PROOF: both games of a pair share one game seed, so (in -mode control) game 2 is game 1 with the roles exactly swapped — every pair must sum to zero and the session Elo must be exactly 0.0, or the harness favors a side")
	)
	flag.Parse()

	if *mode != "measure" && *mode != "control" {
		log.Fatalf("-mode: want measure or control, got %q", *mode)
	}
	if *parallel <= 0 {
		*parallel = min(runtime.NumCPU(), 8)
	}

	// Build the images this command boots (engine.bin + the HARNESSKBD UI),
	// exactly as internal/ui's TestMain does — the stale-binary hazard rule.
	if !*noBuild {
		if err := asmbuild.Build(*root); err != nil {
			log.Fatalf("asm build: %v", err)
		}
		if err := engsyms.Generate(*root); err != nil {
			log.Fatalf("engsyms: %v", err)
		}
		if err := asmbuild.BuildStandaloneAs(*root, "m8", "m8t", "HARNESSKBD"); err != nil {
			log.Fatalf("m8t build: %v", err)
		}
	}

	bin, err := os.ReadFile(*root + "/asm/engine.bin")
	if err != nil {
		log.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(*root + "/asm/defs.inc")
	if err != nil {
		log.Fatal(err)
	}

	var logw io.Writer = os.Stdout
	var mvw io.Writer = io.Discard // per-move detail: file only (30K+ lines)
	if *outfile != "" {
		f, err := os.OpenFile(*outfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("open log: %v", err)
		}
		defer f.Close()
		logw = io.MultiWriter(os.Stdout, f)
		mvw = f
	}
	var logMu sync.Mutex
	lg := func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintf(logw, format+"\n", args...)
	}
	mv := func(format string, args ...any) {
		logMu.Lock()
		defer logMu.Unlock()
		fmt.Fprintf(mvw, format+"\n", args...)
	}

	aPonder := *mode == "measure"
	lg("=== ponder-match: mode=%s games=%d level=%d seed=%d openseed=%d parallel=%d maxplies=%d ===",
		*mode, 2**pairs, *level, *seed, *openSeed, *parallel, *maxPlies)
	lg("=== side A: PONDERON=%v (the device m8ponder)  side B: PONDERON=0 (shipped no-ponder behaviour) ===",
		aPonder)

	openings := sprt.GenOpenings(bin, defs, *pairs, *openSeed)
	lg("openings: %d generated (balanced, |eval|<=60cp at d2)", len(openings))

	type gameOut struct {
		outcome int // +1 A wins, 0 draw, -1 A loses; from side A's perspective
		a, b    sideStats
		plies   int
		reason  string
		err     error
	}
	res := &sprt.Result{}
	var sessA, sessB sideStats
	var totPlies, done int
	start := time.Now()

	sem := make(chan struct{}, *parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for p := 0; p < *pairs; p++ {
		for g := 0; g < 2; g++ {
			gameNo := 2*p + g
			aWhite := g == 0
			opening := openings[p%len(openings)]
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				gameSeed := *seed + uint64(gameNo)*0x9E3779B97F4A7C15
				if *pairSeed {
					gameSeed = *seed + uint64(p)*0x9E3779B97F4A7C15
				}
				out := gameOut{}
				out.outcome, out.a, out.b, out.plies, out.reason, out.err =
					playGame(*root, *level, opening, aPonder, aWhite, gameSeed, *maxPlies, gameNo, mv)
				mu.Lock()
				defer mu.Unlock()
				done++
				if out.err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("g%d: %v", gameNo, out.err))
					lg("GAME %d ERROR (%d/%d): %v", gameNo, done, 2**pairs, out.err)
					return
				}
				sessA.fold(out.a)
				sessB.fold(out.b)
				totPlies += out.plies
				switch out.outcome {
				case 1:
					res.Wins++
				case -1:
					res.Losses++
				default:
					res.Draws++
				}
				elo, margin := res.EloDiff()
				lg("GAME %d RESULT (%d/%d): %s reason=%s plies=%d aWhite=%v opening=%q | running A +%d -%d =%d elo=%+.0f±%.0f [%.0fs]",
					gameNo, done, 2**pairs, outName(out.outcome), out.reason, out.plies, aWhite,
					strings.Join(opening, " "),
					res.Wins, res.Losses, res.Draws, elo, margin, time.Since(start).Seconds())
			}()
		}
	}
	wg.Wait()

	// ----- Session summaries: the numbers the writeup needs. -----
	elo, margin := res.EloDiff()
	lg("PONDER-MATCH-SESSION mode=%s games=%d aWins=%d aLosses=%d draws=%d errors=%d elo=%+.1f margin=%.1f plies_mean=%.1f wall=%.0fs",
		*mode, res.Games(), res.Wins, res.Losses, res.Draws, len(res.Errors),
		elo, margin, mean(uint64(totPlies), res.Games()), time.Since(start).Seconds())
	// Both sides' cycle totals: the free-time asymmetry, visible on purpose.
	// A's total compute = own thinks + ponder windows (+ the cycles it took to
	// park inside each ponder); B's = own thinks only.
	lg("PONDER-SPEND a_own=%d a_moves=%d a_think_mean=%.0f a_window=%d a_windows=%d a_kick=%d a_overhead=%d a_total=%d | b_own=%d b_moves=%d b_think_mean=%.0f b_total=%d | total_ratio=%.3f own_ratio=%.3f",
		sessA.ownCyc, sessA.moves, mean(sessA.ownCyc, sessA.moves),
		sessA.windowCyc, sessA.windows, sessA.kickCyc, sessA.overheadCyc, sessA.total(),
		sessB.ownCyc, sessB.moves, mean(sessB.ownCyc, sessB.moves), sessB.total(),
		ratio(sessA.total(), sessB.total()), ratio(sessA.ownCyc, sessB.ownCyc))
	if aPonder {
		classified := sessA.hits + sessA.misses
		lg("PONDER-HITRATE predictions=%d hits=%d misses=%d none=%d hitrate=%.3f (of predicted) coverage=%.3f interrupted_deep=%d backstops=%d",
			classified, sessA.hits, sessA.misses, sessA.nones,
			ratio(uint64(sessA.hits), uint64(classified)),
			ratio(uint64(classified), uint64(classified+sessA.nones)),
			sessA.interrupted, sessA.backstops)
		// The warm-TT guard: a HIT must buy reply depth or the ponder is inert.
		hd := mean(sessA.hitDepthSum, int(sessA.hitDepthN))
		md := mean(sessA.missDepthSum, int(sessA.missDepthN))
		lg("PONDER-WARMTH hit_reply_depth=%.2f (n=%d) miss_reply_depth=%.2f (n=%d) delta=%+.2f",
			hd, sessA.hitDepthN, md, sessA.missDepthN, hd-md)
		if sessA.hitDepthN >= 20 && sessA.missDepthN >= 20 && hd <= md {
			lg("WARNING: hits did NOT search deeper than misses — the warm TT bought nothing; the measurement may be inert")
		}
	}
	lg("PONDER-DEPTH a_reply_depth_mean=%.2f b_reply_depth_mean=%.2f",
		mean(sessA.depthSum, sessA.moves), mean(sessB.depthSum, sessB.moves))
	lg("PONDER-MATCH-DONE mode=%s games=%d elo=%+.1f margin=%.1f", *mode, res.Games(), elo, margin)
}

func outName(o int) string {
	switch o {
	case 1:
		return "A-wins"
	case -1:
		return "A-loses"
	}
	return "draw"
}

func mean(sum uint64, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(sum) / float64(n)
}

func ratio(a, b uint64) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// sideStats is one side's per-game (and, folded, per-session) accounting.
type sideStats struct {
	ownCyc   uint64 // emulated cycles spent on its own engine turns
	moves    int
	depthSum uint64 // sum of completed reply depths (CURDEPTH)

	// Ponder side only.
	windows     int    // ponder windows granted
	windowCyc   uint64 // cycles granted across them (== opponent thinks)
	kickCyc     uint64 // cycles spent entering a ponder park (incl. game-start kick)
	overheadCyc uint64 // ponder-machinery cycles on the own-turn path: the
	// interrupt tail (search-to-next-poll + snapshot restore while the human
	// types) and the next ponder's entry after the reply — real device
	// latency, booked as ponder spend, not own-move think
	hits        int    // prediction == the move actually injected
	misses      int    // prediction != the move injected
	nones       int    // no prediction existed (predictor bailed/never ran)
	interrupted int    // deep ponder search still live when the key landed
	backstops   int    // ponder hit its walk-away backstop inside the window
	hitDepthSum uint64 // reply CURDEPTH sums, split by hit/miss: the warm-TT guard
	hitDepthN   uint64
	missDepthSum uint64
	missDepthN   uint64
}

func (s *sideStats) total() uint64 { return s.ownCyc + s.windowCyc + s.kickCyc + s.overheadCyc }

func (s *sideStats) fold(o sideStats) {
	s.ownCyc += o.ownCyc
	s.moves += o.moves
	s.depthSum += o.depthSum
	s.windows += o.windows
	s.windowCyc += o.windowCyc
	s.kickCyc += o.kickCyc
	s.overheadCyc += o.overheadCyc
	s.hits += o.hits
	s.misses += o.misses
	s.nones += o.nones
	s.interrupted += o.interrupted
	s.backstops += o.backstops
	s.hitDepthSum += o.hitDepthSum
	s.hitDepthN += o.hitDepthN
	s.missDepthSum += o.missDepthSum
	s.missDepthN += o.missDepthN
}

// side is one booted 8fish UI in the relay.
type side struct {
	u      *ui.Machine
	ponder bool
	label  string
	st     sideStats
	// lastEstimate is the engine's own soft-clock estimate (CLOCK_TRAP<<8)
	// sampled at the end of its last reply search — the device's belief about
	// what it spent, next to the true emulated cycles in the MOVE log line.
	lastEstimate uint64
	// dbgTyping/dbgSearch split the last turn's cycles: keystroke processing
	// (including a ponder-interrupt tail) vs dispatch+search+apply.
	dbgTyping, dbgSearch uint64
}

// ppSentinel marks "no prediction yet" in PPFROM. m8ponder only ever writes a
// generator from-square there (never NOSQ=$FF), so $FF is unreachable except
// by our poke. (0 would be square a1 — a legal from-square.)
const ppSentinel = 0xFF

// newSide boots one machine, types the opening in referee mode (validated
// against the machine's own move acceptance), and assigns the engine its
// color. The machine is left parked at the input prompt, human to move next
// in the UI's eyes iff the opening leaves the relay's color on move.
func newSide(root string, level int, ponder bool, opening []string, engineWhite bool, label string) (*side, error) {
	u, err := ui.Boot(root, nil)
	if err != nil {
		return nil, fmt.Errorf("%s boot: %w", label, err)
	}
	u.Poke(ui.UILEVEL, byte(level))
	u.Poke(ui.UIHUMAN, 0xFF) // referee mode: the opening is typed, not searched
	for i, ms := range opening {
		if err := u.Enter(ms); err != nil {
			return nil, fmt.Errorf("%s opening %q: %w", label, ms, err)
		}
		if got := int(u.Peek(ui.UIHCNT)); got != i+1 {
			return nil, fmt.Errorf("%s opening move %q rejected (UIHCNT=%d, want %d)", label, ms, got, i+1)
		}
	}
	human := byte(8) // engine White -> the relay types Black's moves
	if !engineWhite {
		human = 0
	}
	u.Poke(ui.UIHUMAN, human)
	if ponder {
		u.Poke(ui.PONDERON, 1) // ui.Boot turned the shipped default off; restore it
		u.Poke(ui.PPFROM, ppSentinel)
	}
	return &side{u: u, ponder: ponder, label: label}, nil
}

// jitterEntropy pokes the entropy collector with this turn's "human timing"
// randomness from the game PRNG. On hardware the collector's seed material is
// when the human presses keys; the relay's keystrokes arrive at deterministic
// cycle counts, so the unpredictability is reintroduced here — deterministically
// per -seed, which is what makes a run reproducible. entseed (m8engine) then
// derives the eval-dither SEED from it, exactly as shipped.
func jitterEntropy(u *ui.Machine, rnd *rand.Rand) {
	e, c := u.Defs["ENTROPY"], u.Defs["ENTCNT"]
	r := rnd.Uint64()
	u.Poke(e, byte(r))
	u.Poke(e+1, byte(r>>8))
	u.Poke(c, byte(r>>16))
	u.Poke(c+1, byte(r>>24))
}

// engineTurn drives one engine move on s: type inject (the opponent's move;
// "" for the bare RETURN that opens the game) and run until the machine parks
// again. For a pondering side the first keystroke interrupts the live ponder
// through pkclk, and the classification of the interrupted ponder's prediction
// against inject (HIT/MISS/NONE) is computed here, before the keys go in.
//
// Returns the engine's reply ("" if the game ended on the injected move — the
// caller's referee must agree), the emulated cycles the turn consumed, the
// completed reply depth, and the hit classification.
func (s *side) engineTurn(inject string, rnd *rand.Rand) (reply string, think uint64, depth byte, hitClass string, err error) {
	u := s.u
	h0 := int(u.Peek(ui.UIHCNT))
	hitClass = "n/a"
	if s.ponder && inject != "" {
		if u.Peek(ui.PONDERING) != 0 {
			s.st.interrupted++
		}
		if ppf := u.Peek(ui.PPFROM); ppf != ppSentinel {
			pred := chesstest.MoveUCI(ppf, u.Peek(ui.PPTO), u.Peek(ui.PPFLAGS))
			if pred == inject {
				hitClass = "HIT"
				s.st.hits++
			} else {
				hitClass = "MISS"
				s.st.misses++
			}
		} else {
			hitClass = "NONE"
			s.st.nones++
		}
	}
	if s.ponder {
		u.Poke(ui.PPFROM, ppSentinel) // armed for the ponder this turn will start
	}
	jitterEntropy(u, rnd)

	c0 := u.M.Cycles
	for i := 0; i < len(inject); i++ {
		if err := u.Key(inject[i]); err != nil {
			return "", 0, 0, hitClass, fmt.Errorf("%s typing %q[%d]: %w", s.label, inject, i, err)
		}
	}
	cTyped := u.M.Cycles
	// The final RETURN dispatches the injected move and runs the engine's
	// reply — and, on a pondering side, flows straight into the NEXT ponder,
	// whose predictor overwrites CURDEPTH and whose search scrambles the board
	// until it is restored. So instead of ui.Key (which only stops at the next
	// park), step in small slices and sample CURDEPTH in the window between
	// the reply being applied (UIHCNT reaches its target) and m8ponder
	// starting (PONDERING still 0) — that window is a full uisync+uipaint,
	// ~25K cycles, far wider than the 4K slice.
	u.M.SendInput([]byte{0x0D})
	targetH := h0 + 1 // bare kick: reply is ply h0+1
	if inject != "" {
		targetH = h0 + 2 // inject is applied first, then the reply
	}
	deadline := u.M.Cycles + u.StepLimit
	sampled := false
	for {
		exited, _, rerr := u.M.Run(4096)
		if rerr != nil {
			return "", 0, 0, hitClass, fmt.Errorf("%s reply run: %w", s.label, rerr)
		}
		if exited {
			return "", 0, 0, hitClass, fmt.Errorf("%s image exited during reply", s.label)
		}
		if !sampled && int(u.Peek(ui.UIHCNT)) >= targetH && u.Peek(ui.PONDERING) == 0 {
			depth = u.Peek(u.Defs["CURDEPTH"])
			ct := u.Defs["CLOCK_TRAP"]
			s.lastEstimate = (uint64(u.Peek(ct)) | uint64(u.Peek(ct+1))<<8 |
				uint64(u.Peek(ct+2))<<16) << 8
			s.dbgTyping = cTyped - c0
			s.dbgSearch = u.M.Cycles - cTyped
			sampled = true
		}
		if u.M.WaitingForInput() {
			break
		}
		if u.M.Cycles > deadline {
			return "", 0, 0, hitClass, fmt.Errorf("%s never parked after %q", s.label, inject)
		}
	}
	think = u.M.Cycles - c0

	// Faithful-relay assertion: read the machine's own record back.
	hist := u.History()
	base := h0
	if inject != "" {
		if len(hist) <= h0 || hist[h0] != inject {
			return "", think, 0, hitClass,
				fmt.Errorf("%s did not accept injected %q (hist=%v, screen row17=%q)",
					s.label, inject, hist, strings.TrimSpace(u.Screen().Text(17)))
		}
		base++
	}
	switch len(hist) {
	case base: // no reply: only legitimate if the game just ended
		if u.Peek(ui.UIRESULT) == 0 {
			return "", think, 0, hitClass,
				fmt.Errorf("%s produced no reply and no game-over (hist=%v)", s.label, hist)
		}
		return "", think, 0, hitClass, nil
	case base + 1:
		reply = hist[base]
	default:
		return "", think, 0, hitClass,
			fmt.Errorf("%s history advanced unexpectedly: %d -> %d", s.label, h0, len(hist))
	}
	if !sampled { // parked without a mid-flight sample (never for a real reply)
		depth = u.Peek(u.Defs["CURDEPTH"])
		s.dbgTyping = cTyped - c0
		s.dbgSearch = think - s.dbgTyping
	}
	// Own-move think is the dispatch+search+apply phase. On a pondering side
	// the rest of the turn — the interrupt tail (the ponder search running to
	// its next 128-node poll plus the snapshot restore, all while the human
	// is still typing) and the next ponder's entry after the reply — is
	// ponder machinery, booked as ponder spend so the own-move comparison
	// against the no-ponder side stays like-for-like.
	if s.ponder {
		s.st.ownCyc += s.dbgSearch
		s.st.overheadCyc += think - s.dbgSearch
	} else {
		s.st.ownCyc += think
	}
	s.st.moves++
	s.st.depthSum += uint64(depth)
	switch hitClass {
	case "HIT":
		s.st.hitDepthSum += uint64(depth)
		s.st.hitDepthN++
	case "MISS":
		s.st.missDepthSum += uint64(depth)
		s.st.missDepthN++
	}
	return reply, think, depth, hitClass, nil
}

// kickPonder re-enters the main loop with an empty line so the input loop's
// first ponder burst (urdkey -> m8ponder) starts — needed once at game start
// when the pondering side moves second (mid-game the ponder starts by itself
// on the tail of the side's own engine turn). The cycles it takes to park
// inside the ponder are ponder spend, booked as such.
func (s *side) kickPonder() error {
	s.u.Poke(ui.PPFROM, ppSentinel)
	c0 := s.u.M.Cycles
	if err := s.u.Key(0x0D); err != nil {
		return fmt.Errorf("%s ponder kick: %w", s.label, err)
	}
	s.st.kickCyc += s.u.M.Cycles - c0
	return nil
}

// grantWindow models the opponent's think happening on the ponderer's
// otherwise-idle clock: run s for exactly w cycles with the keyboard trap
// disabled, so the parked ponder search proceeds the way it does on hardware
// (a $C000 status read returns "no key" and the search continues; the harness
// trap would break Run instead — the same modelling ponder_test.go uses).
// If the ponder hits its on-device walk-away backstop inside the window, the
// machine falls through to the keyboard wait and idles there — also exactly
// the hardware behaviour.
func (s *side) grantWindow(w uint64) error {
	m := s.u.M
	savedStat, savedIn := m.Mem.InStatusAddr, m.Mem.InAddr
	m.Mem.InStatusAddr, m.Mem.InAddr = 0, 0
	start := m.Cycles
	var err error
	for m.Cycles-start < w && err == nil {
		var exited bool
		exited, _, err = m.Run(w - (m.Cycles - start))
		if exited {
			err = fmt.Errorf("%s exited during ponder window", s.label)
		}
	}
	m.Mem.InStatusAddr, m.Mem.InAddr = savedStat, savedIn
	s.st.windows++
	s.st.windowCyc += m.Cycles - start
	if err == nil && s.u.Peek(ui.PONDERING) == 0 && s.u.Peek(ui.PPFROM) != ppSentinel {
		s.st.backstops++ // deep ponder ran to its backstop before the window closed
	}
	return err
}

// playGame relays one full game. Side A ponders iff aPonder; side A's engine
// is White iff aWhite. Returns the outcome from A's perspective (+1/0/-1).
func playGame(root string, level int, opening []string, aPonder, aWhite bool, gameSeed uint64, maxPlies, gameNo int, mv func(string, ...any)) (outcome int, aSt, bSt sideStats, plies int, reason string, err error) {
	rnd := rand.New(rand.NewPCG(gameSeed, gameSeed^0x5DEECE66D))

	// The referee: authoritative game state, terminal detection, and the
	// per-ply cross-check that keeps the relay honest.
	ref, err := refchess.ParseFEN(refchess.StartFEN)
	if err != nil {
		return 0, aSt, bSt, 0, "", err
	}
	seen := map[uint64]int{ref.ZobristKey(): 1}
	for _, ms := range opening {
		m, perr := refchess.ParseMove(ms)
		if perr != nil {
			return 0, aSt, bSt, 0, "", perr
		}
		if perr := ref.Make(m); perr != nil {
			return 0, aSt, bSt, 0, "", fmt.Errorf("opening %q: %w", ms, perr)
		}
		seen[ref.ZobristKey()]++
	}
	plies = len(opening)

	a, err := newSide(root, level, aPonder, opening, aWhite, fmt.Sprintf("g%d-A", gameNo))
	if err != nil {
		return 0, aSt, bSt, plies, "", err
	}
	b, err := newSide(root, level, false, opening, !aWhite, fmt.Sprintf("g%d-B", gameNo))
	if err != nil {
		return 0, aSt, bSt, plies, "", err
	}
	defer func() { aSt, bSt = a.st, b.st }()

	// Who moves first from the opening position, and who is on the other bench.
	stmWhite := ref.SideToMove() == 0
	mover, other := a, b
	if stmWhite != aWhite {
		mover, other = b, a
	}
	// If the ponderer opens on the bench, it ponders from the start (on
	// hardware m8ponder runs whenever it is the human's turn and UIHCNT>0).
	if other.ponder {
		if err := other.kickPonder(); err != nil {
			return 0, aSt, bSt, plies, "", err
		}
	}

	// terminal mirrors sargon-symmatch's referee adjudication. Outcome is
	// from A's perspective.
	terminal := func() (bool, int, string) {
		if len(ref.LegalMoves()) == 0 {
			if ref.InCheck() {
				stmIsA := (ref.SideToMove() == 0) == aWhite
				if stmIsA {
					return true, -1, "checkmate"
				}
				return true, 1, "checkmate"
			}
			return true, 0, "stalemate"
		}
		if seen[ref.ZobristKey()] >= 3 {
			return true, 0, "threefold"
		}
		if ref.HalfmoveClock() >= 100 {
			return true, 0, "fifty-move"
		}
		if ref.InsufficientMaterial() {
			return true, 0, "insufficient-material"
		}
		if plies >= maxPlies {
			return true, 0, "move-cap"
		}
		return false, 0, ""
	}

	inject := ""
	for {
		reply, think, depth, hitClass, terr := mover.engineTurn(inject, rnd)
		if terr != nil {
			return 0, aSt, bSt, plies, "", terr
		}
		if reply == "" {
			// The machine says the injected move ended the game. The referee
			// (already holding that move) must agree.
			term, out, why := terminal()
			if !term {
				return 0, aSt, bSt, plies, "",
					fmt.Errorf("%s declared game over (%s) but referee disagrees at %q",
						mover.label, ui.ResultName(mover.u.Peek(ui.UIRESULT)), ref.FEN())
			}
			return out, aSt, bSt, plies, why, nil
		}

		// Referee: the reply must be legal, and the machine's whole position
		// must match the referee's after it.
		m, perr := refchess.ParseMove(reply)
		if perr != nil {
			return 0, aSt, bSt, plies, "", fmt.Errorf("%s reply %q: %w", mover.label, reply, perr)
		}
		if perr := ref.Make(m); perr != nil {
			return 0, aSt, bSt, plies, "",
				fmt.Errorf("%s played illegal-to-referee %q at %q: %w", mover.label, reply, ref.FEN(), perr)
		}
		seen[ref.ZobristKey()]++
		plies++
		// Whole-position cross-check — but not while the machine is parked
		// inside a ponder search, where the board legitimately holds the
		// ponder line (it is restored when the ponder ends; the exactness of
		// that restore is the device gate TestDevicePonderRestoresAndPredicts).
		if mover.u.Peek(ui.PONDERING) == 0 {
			if got, want := norm5(mover.u.FEN()), norm5(ref.FEN()); got != want {
				return 0, aSt, bSt, plies, "",
					fmt.Errorf("%s position diverged after %q:\n machine %q\n referee %q", mover.label, reply, got, want)
			}
		}

		mv("MOVE g%d ply%d %s move=%s think=%d typing=%d search=%d est=%d depth=%d hit=%s pondering=%d",
			gameNo, plies, mover.label, reply, think, mover.dbgTyping, mover.dbgSearch,
			mover.lastEstimate, depth, hitClass, mover.u.Peek(ui.PONDERING))

		if term, out, why := terminal(); term {
			return out, aSt, bSt, plies, why, nil
		}

		// The other side pondered while this think ran: grant it that window.
		if other.ponder {
			if werr := other.grantWindow(think); werr != nil {
				return 0, aSt, bSt, plies, "", werr
			}
		}
		inject = reply
		mover, other = other, mover
	}
}

// norm5 keeps a FEN's first five fields (the UI does not track the fullmove
// number, and its FEN() always reports fullmove 1).
func norm5(fen string) string {
	f := strings.Fields(fen)
	if len(f) > 5 {
		f = f[:5]
	}
	return strings.Join(f, " ")
}
