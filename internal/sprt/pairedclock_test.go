package sprt

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/refchess"
)

// ---------------------------------------------------------------------------
// TestPairedClockProbe — the POSITION-PAIRED soft-clock instrument.
//
// WHY IT EXISTS. TestSoftClockAdherence compares two INDEPENDENT self-play
// runs, one per clock, and divides their total spend. That is the right
// acceptance criterion (it is what a game actually does) but it is a very
// blunt measuring device: the two arms play DIFFERENT games, so the ratio
// carries the whole variance of game composition. Measured at 3 pairs and a
// 29 s budget, the two arms produced 654 and 444 moves and a soft/exact of
// 1.01 — a number with no resolving power at all, and one that disagreed by
// 10 points with a 6-pair run.
//
// This probe removes composition entirely. At every ply of a self-play game
// the SAME machine state — same position, same halfmove clock, same carried
// transposition table, same poked limits — is searched TWICE, once on the
// exact harness clock and once on FT2_SOFTCLK, and both TRUE cycle costs,
// completed depths and chosen moves are recorded. The exact run decides the
// move, so both arms walk an identical trajectory. What comes out is a paired
// ratio with a bootstrap interval roughly 20x tighter than the unpaired gate's
// for the same wall time, plus two things the gate cannot report at all: the
// completed-depth difference and the move-agreement rate, which are what
// actually matter for strength.
//
// ★ THE TRAP THIS TEST FELL INTO FIRST, AND WHY THE TT CARRY IS LOAD-BEARING.
// The obvious implementation carries ONE transposition table forward across
// plies, handing each search the table its immediate predecessor produced.
// That is not what any real game does — and it is not what the SHIPPED disk
// does either: asm/m8.s never ponders (its main loop blocks in `uiread` ->
// `entkey` on the human's turn), so the engine's TT is always TWO plies old
// when a search starts. A one-ply-old table already covers the tree the
// search is about to build, which breaks idloop's "the next iteration costs
// 2x the last" assumption and inflates BOTH arms' adherence — measured exact
// adherence 1.26 at 4 s, against the 0.92 the same build shows in real games.
// It also moved the paired ratio by 11 points (0.913 -> 1.027) and would have
// reported a soft-clock defect that does not exist. Two auxes, alternating by
// side, exactly as sprt.Run and the disk both do it.
//
// Environment knobs (all optional): PROBE_MS (comma-separated budgets in ms),
// PROBE_OPENINGS, PROBE_PLIES, PROBE_SKIP, PROBE_ADAPT (install the disk's
// FT2_ADAPT ceilings — asm/m8.s uilimits, no bank), PROBE_MARGIN (override
// the margin percent applied to the SOFT arm's limits, so an octave's
// margin-table entry can be fitted directly rather than guessed).
// ---------------------------------------------------------------------------
func TestPairedClockProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: paired budgeted self-play games")
	}
	asmbuild.BuildT(t, "../..")
	bin, err := os.ReadFile(filepath.Join("..", "..", "asm", "engine.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join("..", "..", "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	softBit := byte(defs["FT2_SOFTCLK"])
	genDefer := byte(defs["FT2_GENDEFER"])
	feat := byte(0x1F) | byte(defs["FT_CKEXT"])
	adapt := os.Getenv("PROBE_ADAPT") != ""

	// ★ WHY THE DEFAULT IS THE WHOLE LIST, changed 2026-07-30 from 8.
	// This is the instrument that actually resolves the spend ratio, and its
	// [0.90, 1.10] assertion below is a real acceptance gate — so running it
	// on openings 0-7 certified 8 of 20. The per-opening soft/exact spread at
	// the 30 s octave is 0.75 to 1.01, sd 0.062, which makes an 8-game slice
	// worth +/- 0.022 and a 4-game one +/- 0.031: slices disagree with each
	// other by more than the width of the band's own margin. Measured on all
	// twenty, the 30 s octave reads 0.9035 [0.8731, 0.9301] — a population
	// fact, and the one the shipped LEVEL 8 actually experiences. Set
	// PROBE_OPENINGS/PROBE_FIRSTOPEN to slice it deliberately, but know that a
	// slice is a 4-8 sample of a population with that spread.
	nOpen := len(Openings)
	if v := os.Getenv("PROBE_OPENINGS"); v != "" {
		nOpen, _ = strconv.Atoi(v)
	}
	// PROBE_FIRSTOPEN selects a DISJOINT slice of the opening list, so a second
	// run is an independent replicate rather than a rerun of a deterministic
	// one (nothing here is dithered).
	openFirst := 0
	if v := os.Getenv("PROBE_FIRSTOPEN"); v != "" {
		openFirst, _ = strconv.Atoi(v)
	}
	maxPly := 40
	if v := os.Getenv("PROBE_PLIES"); v != "" {
		maxPly, _ = strconv.Atoi(v)
	}
	firstPly := 0
	if v := os.Getenv("PROBE_SKIP"); v != "" {
		firstPly, _ = strconv.Atoi(v)
	}
	// The SHIPPED octaves, one per distinct margin-table entry, and BOTH ENDS
	// of the entry that covers more than one level:
	//
	//	 4000 — octave 13, margin 127%. asm/m8.s LEVEL 5.
	//	15000 — octave 15, margin 92%. LEVEL 7, and the LOW end of the 15+
	//	        entry. Added 2026-07-30: this octave was the "measured anchor"
	//	        that no longer got measured, and on all twenty openings it was
	//	        spending 0.9206 of what an exact clock spends.
	//	30000 — octave 16, margin 92%. LEVEL 8, the gauntlet budget, and the
	//	        level a user picks for a serious game.
	//	60000 — octave 17, margin 92%. LEVEL 9, the longest level that ships.
	//
	// ★ 60000 IS HERE BECAUSE OCTAVE 17 IS NOT OCTAVE 16, and assuming it was
	// is exactly the mistake the flat tail made. One margin entry covers
	// octaves 15-23, so it is tempting to gate one of them and call the rest
	// covered — but measured at margin 100 the three shipped octaves read
	// 0.9206 (15 s), 0.9035 (30 s) and 0.9700 (60 s). A 60 s search completes
	// depth 4.4 and its exact-clock control only spends 0.8502, so both arms
	// sit far from the budget and the predictive gate is far less sensitive
	// there. An entry fitted at 30 s therefore lands 60 s ABOVE parity, and
	// nothing would have said so. This run is ~27 minutes; that is the price
	// of gating every level the disk offers instead of the cheap ones.
	budgets := []uint64{4000, 15000, 30000, 60000}
	if v := os.Getenv("PROBE_MS"); v != "" {
		budgets = nil
		for _, s := range strings.Split(v, ",") {
			n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
			budgets = append(budgets, n)
		}
	}
	margins := []uint64{0} // 0 = use the shipped table
	if v := os.Getenv("PROBE_MARGIN"); v != "" {
		margins = nil
		for _, s := range strings.Split(v, ",") {
			n, _ := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
			margins = append(margins, n)
		}
	}

	type pt struct {
		open             int
		ply              int
		soft, exact, est uint64
		dsoft, dexact    byte
		agree            bool
	}
	type outc struct {
		cyc, est  uint64
		from, to  byte
		fl, depth byte
		aux       []byte
	}

	// runOne installs the shipped gameplay config and one move's limits, then
	// runs. marginPct != 0 replaces the table margin for the soft arm: the
	// SOFTCLK bit is set AFTER the limits are poked so SetBudget/SetAdaptive do
	// not apply the table themselves.
	runOne := func(pos *chesstest.Position, aux []byte, hm byte, budget uint64, soft bool, marginPct uint64) outc {
		m, err := chesstest.NewMachine(bin, defs, pos, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		chesstest.SetFeatures(m, defs, feat)
		lim := budget
		if soft {
			m.Mem.ClockAddr = 0
			if marginPct != 0 {
				chesstest.SetFeatures2(m, defs, genDefer) // limits poked RAW
				lim = budget * 100 / marginPct
			} else {
				chesstest.SetFeatures2(m, defs, genDefer|softBit)
			}
		} else {
			chesstest.SetFeatures2(m, defs, genDefer)
		}
		chesstest.SetBudget(m, defs, lim, 24)
		if adapt {
			// exactly asm/m8.s uilimits: no bank, ceilings from the level budget
			chesstest.SetAdaptive(m, defs, lim, 4*lim, 3*lim, lim/4)
		}
		if soft && marginPct != 0 {
			f2 := genDefer | softBit
			if adapt {
				f2 |= byte(defs["FT2_ADAPT"])
			}
			m.Mem.Main[defs["FEATURES2"]] = f2
		}
		m.Mem.Main[defs["HALFMOVE"]] = hm
		if aux != nil {
			copy(m.Mem.Aux[:], aux)
		}
		exited, code, err := m.Run(budget*12 + 2_000_000_000)
		if err != nil || !exited || code != 0 {
			return outc{to: 0xFF}
		}
		c := defs["CLOCK_TRAP"]
		est := (uint64(m.Mem.Main[c]) | uint64(m.Mem.Main[c+1])<<8 | uint64(m.Mem.Main[c+2])<<16) << 8
		na := make([]byte, len(m.Mem.Aux))
		copy(na, m.Mem.Aux[:])
		return outc{m.Cycles, est, m.Mem.Main[defs["BESTFROM"]], m.Mem.Main[defs["BESTTO"]],
			m.Mem.Main[defs["BESTFLAGS"]], m.Mem.Main[defs["CURDEPTH"]], na}
	}

	for _, ms := range budgets {
		budget := ms * chesstest.CyclesPerMs
		for _, mg := range margins {
			t0 := time.Now()
			var mu sync.Mutex
			var pts []pt
			var wg sync.WaitGroup
			sem := make(chan struct{}, max(1, parallelDefault()))
			for oi := openFirst; oi < openFirst+nOpen && oi < len(Openings); oi++ {
				wg.Add(1)
				go func(oi int) {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					ref, err := refchess.ParseFEN("rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1")
					if err != nil {
						return
					}
					for _, mvs := range Openings[oi] {
						mv, err := refchess.ParseMove(mvs)
						if err != nil {
							return
						}
						if err := ref.Make(mv); err != nil {
							return
						}
					}
					// TT carry MUST match the shipped arrangement: the disk
					// (asm/m8.s) never ponders, so the engine's transposition
					// table is always TWO plies old when a search starts —
					// exactly what sprt.Run models with its per-side auxes.
					// Carrying the immediately-previous ply's TT instead makes
					// every search start on a table that already covers its own
					// tree, which breaks the predictive gate's "next iteration
					// is 2x the last" assumption and inflates BOTH arms'
					// adherence (measured: exact 1.26 vs sprt's 0.92 at 4 s).
					auxes := [2][]byte{}
					var local []pt
					for ply := 0; ply < maxPly; ply++ {
						if ref.HalfmoveClock() >= 100 || ref.InsufficientMaterial() || len(ref.LegalMoves()) == 0 {
							break
						}
						pos, err := chesstest.ParseFEN(ref.FEN())
						if err != nil {
							break
						}
						hm := byte(min(ref.HalfmoveClock(), 255))
						side := ply & 1
						aux := auxes[side]
						s := runOne(pos, aux, hm, budget, true, mg)
						e := runOne(pos, aux, hm, budget, false, 0)
						if s.to == 0xFF || e.to == 0xFF {
							break
						}
						if ply >= firstPly {
							local = append(local, pt{oi, ply, s.cyc, e.cyc, s.est, s.depth, e.depth,
								s.from == e.from && s.to == e.to && s.fl == e.fl})
						}
						auxes[side] = e.aux
						mv, err := refchess.ParseMove(chesstest.MoveUCI(e.from, e.to, e.fl))
						if err != nil {
							break
						}
						if err := ref.Make(mv); err != nil {
							break
						}
					}
					mu.Lock()
					pts = append(pts, local...)
					mu.Unlock()
				}(oi)
			}
			wg.Wait()
			// Deterministic order: the goroutines append in completion order,
			// and both the bootstrap resample and the per-opening breakdown
			// below must not depend on which game finished first.
			sort.Slice(pts, func(i, j int) bool {
				if pts[i].open != pts[j].open {
					return pts[i].open < pts[j].open
				}
				return pts[i].ply < pts[j].ply
			})
			var ss, se, ds, de, sest float64
			agree := 0
			rat := make([]float64, 0, len(pts))
			for _, p := range pts {
				ss += float64(p.soft)
				se += float64(p.exact)
				sest += float64(p.est)
				ds += float64(p.dsoft)
				de += float64(p.dexact)
				if p.agree {
					agree++
				}
				rat = append(rat, float64(p.soft)/float64(p.exact))
			}
			sort.Float64s(rat)
			q := func(f float64) float64 {
				if len(rat) == 0 {
					return 0
				}
				return rat[int(f*float64(len(rat)-1))]
			}
			// ★ CLUSTER bootstrap: resample GAMES, not positions.
			//
			// The plies of one game are NOT independent draws. The exact arm
			// decides every move, so a game is one deterministic trajectory
			// through one opening's middlegame; consecutive plies share a
			// position, a material balance, a phase and a carried TT, and the
			// thing being measured — whether the predictive gate declines the
			// last iteration — is a property of the whole trajectory, not of
			// each ply separately. Resampling PLIES therefore treats ~40
			// correlated observations as 40 independent ones and reports an
			// interval far tighter than the data support. That is not
			// hypothetical: it is how the 4-game PROBE_FIRSTOPEN=16 slice
			// produced a confident-looking [0.8048, 0.9338] on an n of FOUR.
			// Resampling whole openings keeps the within-game correlation
			// inside the resampled unit, which is the standard correction.
			games := map[int][]pt{}
			var gameIDs []int
			for _, p := range pts {
				if _, ok := games[p.open]; !ok {
					gameIDs = append(gameIDs, p.open)
				}
				games[p.open] = append(games[p.open], p)
			}
			rng := rand.New(rand.NewPCG(1, 2))
			boots := make([]float64, 2000)
			for b := range boots {
				var a, c float64
				for range gameIDs {
					for _, p := range games[gameIDs[rng.IntN(len(gameIDs))]] {
						a += float64(p.soft)
						c += float64(p.exact)
					}
				}
				boots[b] = a / c
			}
			sort.Float64s(boots)
			lo, hi := boots[int(0.025*float64(len(boots)))], boots[int(0.975*float64(len(boots)))]
			n := float64(len(pts))
			mgLabel := chesstest.SoftClockMargin(budget)
			if mg != 0 {
				mgLabel = mg
			}
			line := fmt.Sprintf("budget=%6d ms adapt=%v margin=%3d%%  n=%d  soft_adh=%.4f exact_adh=%.4f  SOFT/EXACT=%.4f [%.4f,%.4f]  est/truth=%.4f  depth soft=%.3f exact=%.3f  agree=%.3f  p10=%.3f p50=%.3f p90=%.3f  [%v]",
				ms, adapt, mgLabel, len(pts), ss/(n*float64(budget)), se/(n*float64(budget)), ss/se, lo, hi,
				sest/ss, ds/n, de/n, float64(agree)/n, q(0.10), q(0.50), q(0.90), time.Since(t0).Round(time.Second))
			t.Log(line)
			fmt.Println("PROBE " + line)

			// ── BIAS AND VARIANCE ARE DIFFERENT DISEASES ────────────────
			// The aggregate est/truth on the line above is a cycle-weighted
			// BIAS. It cannot distinguish "the cost table is mispriced for
			// this population" from "the per-move estimate is unbiased but
			// noisy, and a THRESHOLD turns symmetric noise into asymmetric
			// spend". Report the per-move mean and the relative RMS about
			// that mean separately, plus the depth ledger — a spend deficit
			// delivered as whole DECLINED ITERATIONS shows up as dexact >
			// dsoft, while one delivered inside a single iteration does not.
			var em, ev float64
			var dLess, dMore int
			for _, p := range pts {
				em += float64(p.est) / float64(p.soft)
				if p.dexact > p.dsoft {
					dLess++
				} else if p.dexact < p.dsoft {
					dMore++
				}
			}
			em /= n
			for _, p := range pts {
				d := float64(p.est)/float64(p.soft) - em
				ev += d * d
			}
			ev = math.Sqrt(ev/n) / em
			t.Logf("  est/truth per-move: mean=%.4f relRMS=%.4f   iterations: soft SHORT on %d/%d moves, soft LONGER on %d",
				em, ev, dLess, len(pts), dMore)

			// ── PER-OPENING BREAKDOWN ───────────────────────────────────
			// A slice ratio is an average over a handful of GAMES. Whether
			// it reflects the population or one outlier trajectory is not
			// visible from the aggregate, and with 4-8 games it is the first
			// thing to check before modelling anything.
			for _, g := range gameIDs {
				var a, c, e2 float64
				var dl int
				for _, p := range games[g] {
					a += float64(p.soft)
					c += float64(p.exact)
					e2 += float64(p.est)
					if p.dexact > p.dsoft {
						dl++
					}
				}
				t.Logf("  opening %2d (n=%3d): soft/exact=%.4f est/truth=%.4f short-iters=%d  [%s]",
					g, len(games[g]), a/c, e2/a, dl, strings.Join(Openings[g], " "))
			}

			// The same band TestSoftClockAdherence applies to the unpaired
			// ratio, on the instrument that can actually resolve it. Only
			// meaningful when the shipped margin table is in force.
			if mg == 0 {
				if r := ss / se; r < 0.90 || r > 1.10 {
					t.Errorf("%d ms: paired soft/exact spend %.4f outside [0.90, 1.10] "+
						"(bootstrap 95%% [%.4f, %.4f]): the estimator is not delivering the "+
						"compute an exact clock would at this octave", ms, r, lo, hi)
				}
				// ★ AND THE INTERVAL HAS TO BE INSIDE IT TOO, added 2026-07-30.
				// The band is NOT widened by this — it is the same [0.90,
				// 1.10]; what changes is that the MEASUREMENT, not just its
				// midpoint, has to establish the claim.
				//
				// This is the assertion that would have caught the defect this
				// was written for. On all twenty openings the pre-fix build
				// read 0.9035 at 30 s and 0.9206 at 15 s: both midpoints are
				// inside the band, both PASSED, and both were leaving 8-10% of
				// the level's allocation unspent — with game-clustered 95%
				// intervals of [0.8731, 0.9301] and [0.8844, 0.9619], i.e.
				// a fifth and a third of the interval sitting under the floor.
				// A point estimate parked on the edge of a band is not a
				// passing measurement, it is an unresolved one, and the whole
				// investigation began with someone noticing a slice of it.
				if lo < 0.90 || hi > 1.10 {
					t.Errorf("%d ms: paired soft/exact spend %.4f has 95%% interval "+
						"[%.4f, %.4f], which is not contained in [0.90, 1.10]: the point "+
						"estimate passes but the measurement does not RESOLVE the spend as "+
						"equal — this is the shape the 30 s octave hid in", ms, ss/se, lo, hi)
				}
			}
		}
	}
}
