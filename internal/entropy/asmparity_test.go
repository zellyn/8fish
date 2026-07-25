package entropy_test

import (
	"bytes"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zellyn/chess6502/harness"
	"github.com/zellyn/chess6502/internal/asmbuild"
	"github.com/zellyn/chess6502/internal/chesstest"
	"github.com/zellyn/chess6502/internal/entropy"
)

// The REAL 6502 collector, driven under emulation.
//
// asm/entropytest.s assembles asm/entropy.inc with HARNESSKBD, so the harness
// input traps stand in for $C000/$C010: the collector spins its counter in the
// keyboard-wait loop exactly as it will on hardware, and THIS test plays the
// human — it decides at which emulated cycle each key becomes available. The
// 6502 side emits one SEED byte per simulated move through the COUT trap.

const (
	nmovesAddr = 0x0300 // entropytest.s NMOVES (0 = run forever)
	keyspmAddr = 0x0301 // entropytest.s KEYSPM
	entOrg     = 0x4000
)

type sim struct {
	t      *testing.T
	m      *harness.Machine
	out    *bytes.Buffer
	entcnt uint16 // ENTCNT address
	entrop uint16 // ENTROPY address
}

// newSim builds the emulated collector with the given initial ENTCNT/ENTROPY
// (arbitrary on purpose: hardware leaves them as RAM garbage).
func newSim(t *testing.T, cnt uint16, ent byte, moves, keysPerMove byte) *sim {
	t.Helper()
	root := filepath.Join("..", "..")
	if err := asmbuild.BuildStandalone(root, "entropytest"); err != nil {
		if errors.Is(err, asmbuild.ErrCA65NotInstalled) {
			t.Skip("SKIP: ca65 not installed")
		}
		t.Fatal(err)
	}
	bin, err := os.ReadFile(filepath.Join(root, "asm", "entropytest.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := chesstest.ParseDefs(filepath.Join(root, "asm", "defs.inc"))
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	m, err := harness.New(harness.Config{
		Bin: bin, Org: entOrg, Entry: entOrg,
		CoutAddr: 0xBFF0, ExitAddr: 0xBFFF,
		InAddr: 0xBFF1, InStatusAddr: 0xBFF2, ClockAddr: 0xBFF4,
		Cout: out,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Mem.Main[nmovesAddr] = moves
	m.Mem.Main[keyspmAddr] = keysPerMove
	m.Mem.Main[defs["ENTCNT"]] = byte(cnt)
	m.Mem.Main[defs["ENTCNT"]+1] = byte(cnt >> 8)
	m.Mem.Main[defs["ENTROPY"]] = ent
	return &sim{t: t, m: m, out: out, entcnt: defs["ENTCNT"], entrop: defs["ENTROPY"]}
}

// step runs the collector until it next polls the keyboard with nothing
// pending (i.e. one poll-loop iteration per call) or the program exits.
func (s *sim) step() (exited bool) {
	exited, _, err := s.m.Run(1 << 30)
	if err != nil {
		s.t.Fatalf("run: %v", err)
	}
	if !exited && !s.m.WaitingForInput() {
		s.t.Fatal("run stopped without exiting or waiting for input")
	}
	return exited
}

func (s *sim) state() (cnt uint16, ent byte) {
	return uint16(s.m.Mem.Main[s.entcnt]) | uint16(s.m.Mem.Main[s.entcnt+1])<<8,
		s.m.Mem.Main[s.entrop]
}

// press plays one human keystroke that arrives after at least `wait` more
// emulated cycles: spin the poll loop until then, then make the key available.
// It returns ENTCNT as the collector will see it at the fold (the +1 is the
// successful poll's own increment, which happens before the fold).
func (s *sim) press(wait uint64, key byte) uint16 {
	target := s.m.Cycles + wait
	for s.m.Cycles < target {
		if s.step() {
			s.t.Fatal("collector exited early")
		}
	}
	cnt, _ := s.state()
	s.m.SendInput([]byte{key})
	return cnt + 1
}

// awaitSeed steps until the collector has emitted its next seed byte.
func (s *sim) awaitSeed() byte {
	n := s.out.Len()
	for s.out.Len() == n {
		if s.step() && s.out.Len() == n {
			s.t.Fatal("collector exited without emitting a seed")
		}
	}
	return s.out.Bytes()[n]
}

// TestASMLoopCycles pins entropy.TickCycles against the emulated loop: one
// keyboard-poll iteration costs exactly 16 cycles and bumps ENTCNT by one.
func TestASMLoopCycles(t *testing.T) {
	s := newSim(t, 0, 0, 1, 1)
	s.step() // reach the first poll
	before, cntBefore := s.m.Cycles, s.m.Mem.Main[s.entcnt]
	const iters = 200 // stay inside one low-byte run (no high-byte carry)
	for i := 0; i < iters; i++ {
		s.step()
	}
	gotCycles := (s.m.Cycles - before) / iters
	gotCnt := s.m.Mem.Main[s.entcnt] - cntBefore
	if gotCycles != entropy.TickCycles {
		t.Errorf("poll loop = %d cycles/iteration, entropy.TickCycles = %d", gotCycles, entropy.TickCycles)
	}
	if int(gotCnt) != iters {
		t.Errorf("ENTCNT advanced %d over %d polls, want %d", gotCnt, iters, iters)
	}
	t.Logf("poll loop: %d cycles/iteration, ENTCNT +%d over %d polls; low byte turns over every %d cycles = %.2fms",
		gotCycles, gotCnt, iters, 256*entropy.TickCycles, 256*entropy.TickCycles*1000.0/entropy.CPUHz)
}

// TestASMParity: the Go model in this package produces the SAME per-move seed
// stream as the real 6502 collector for the same keystroke arrivals. That is
// what lets the harness derive its seeds through the shipping algorithm rather
// than bypassing it with a host PRNG.
func TestASMParity(t *testing.T) {
	const (
		moves       = 40
		keysPerMove = 4 // "e2e4"
		initCnt     = uint16(0x9F3C)
		initEnt     = byte(0xC7)
	)
	s := newSim(t, initCnt, initEnt, moves, keysPerMove)
	model := entropy.New(initCnt, initEnt)

	rnd := rand.New(rand.NewPCG(1, 2))
	var want []byte
	for mv := 0; mv < moves; mv++ {
		for k := 0; k < keysPerMove; k++ {
			// 3-60ms of keystroke jitter (small keeps the test quick: every
			// extra ms is ~64 emulated poll iterations; TestSeedQuality does
			// the realistic distribution).
			wait := entropy.CyclesForDuration(time.Duration(3+rnd.Int64N(57)) * time.Millisecond)
			arrival := s.press(wait, byte('a'+k))
			mc, _ := model.State()
			model.Tick(uint64(arrival - mc)) // exactly the polls the 6502 ran
			model.Key()
		}
		want = append(want, model.Seed())
	}
	got := s.finish()
	if !bytes.Equal(got, want) {
		t.Fatalf("seed stream mismatch\nasm: %v\ngo:  %v", got, want)
	}
	acnt, aent := s.state()
	mcnt, ment := model.State()
	if acnt != mcnt || aent != ment {
		t.Errorf("final state: asm ENTCNT=%#04x ENTROPY=%#02x, model ENTCNT=%#04x ENTROPY=%#02x",
			acnt, aent, mcnt, ment)
	}
	t.Logf("%d moves x %d keystrokes: identical seed streams; final ENTCNT=%#04x ENTROPY=%#02x",
		moves, keysPerMove, acnt, aent)
}

// finish runs to the program's exit and returns the whole emitted seed stream.
func (s *sim) finish() []byte {
	for !s.step() {
	}
	return s.out.Bytes()
}

// TestSeedQuality runs the real 6502 collector over a simulated session of
// human move entry and reports the seed distribution: spread over 1..255, no
// clustering, and — the point of the exercise — a different seed stream for
// every game, so the engine cannot replay a game.
func TestSeedQuality(t *testing.T) {
	games, movesPerGame := 24, 30
	if testing.Short() {
		games, movesPerGame = 6, 12
	}
	const keysPerMove = 4
	s := newSim(t, 0, 0, 0 /* run forever */, keysPerMove)

	rnd := rand.New(rand.NewPCG(0x5EED, 0xF00D))
	// Human timing: a per-move think time then per-keystroke intervals,
	// lognormal. Scaled to ~1/10 real time so the test runs in seconds; the
	// JITTER — which is where the entropy comes from — still spans many
	// multiples of the counter's 4.1ms low-byte period.
	delay := func(meanMs, sigma float64) uint64 {
		ms := math.Exp(rnd.NormFloat64()*sigma) * meanMs
		return entropy.CyclesForDuration(time.Duration(ms * float64(time.Millisecond)))
	}

	var seeds []byte
	for g := 0; g < games; g++ {
		for mv := 0; mv < movesPerGame; mv++ {
			s.press(delay(120, 0.9), 'e') // think time before typing starts
			for k := 1; k < keysPerMove; k++ {
				s.press(delay(25, 0.5), byte('a'+k)) // inter-keystroke
			}
			seeds = append(seeds, s.awaitSeed())
		}
	}

	var counts [256]int
	for _, b := range seeds {
		counts[b]++
	}
	distinct := 0
	for _, c := range counts {
		if c > 0 {
			distinct++
		}
	}
	if counts[0] != 0 {
		t.Errorf("%d seeds were 0 (that would switch dither off)", counts[0])
	}
	adjacentRepeat := 0
	for i := 1; i < len(seeds); i++ {
		if seeds[i] == seeds[i-1] {
			adjacentRepeat++
		}
	}
	var buckets [16]int
	for _, b := range seeds {
		buckets[b>>4]++
	}
	exp := float64(len(seeds)) / 16
	chi2 := 0.0
	for _, c := range buckets {
		d := float64(c) - exp
		chi2 += d * d / exp
	}
	minS, maxS := byte(255), byte(0)
	for _, b := range seeds {
		minS, maxS = min(minS, b), max(maxS, b)
	}
	streams := map[string]int{}
	firstSeeds := map[byte]int{}
	for g := 0; g < games; g++ {
		gs := seeds[g*movesPerGame : (g+1)*movesPerGame]
		streams[string(gs)]++
		firstSeeds[gs[0]]++
	}

	t.Logf("seeds=%d distinct=%d min=%d max=%d adjacent-repeats=%d chi2(15df)=%.1f",
		len(seeds), distinct, minS, maxS, adjacentRepeat, chi2)
	t.Logf("games=%d distinct-seed-streams=%d distinct-first-move-seeds=%d",
		games, len(streams), len(firstSeeds))

	if len(streams) != games {
		t.Errorf("only %d/%d games got a distinct seed stream — games could repeat", len(streams), games)
	}
	if want := min(len(seeds), 200) * 2 / 3; distinct < want {
		t.Errorf("seed spread too narrow: %d distinct values over %d seeds (want >= %d)", distinct, len(seeds), want)
	}
	if chi2 > 45 { // 15 df: p<0.001 is ~37.7 — a loose clustering alarm only
		t.Errorf("seed distribution clusters badly: chi2 = %.1f over 16 buckets", chi2)
	}
}
