package mirror

import (
	"bufio"
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
)

// King-bucketed PSQT (task #30): an NNUE/HalfKP-inspired but network-
// free eval extension. Non-king pieces get a per-square value DELTA on
// top of the base PeSTO tables, selected by the bucket of their own
// king's square. This lets a piece be worth more or less depending on
// where its king lives (e.g. a castled king changes the value of the
// pawns and pieces around it) without any runtime multiply.
//
// Bucketing is by king file zone only (4 buckets: a-b, c-d, e-f, g-h),
// which is castling-aligned, cheap (kingfile>>1), and always well
// populated. Black mirrors vertically (sq^0x70), so the file — and thus
// the bucket — is shared with White; the tables are color-symmetric.

// NumKB is the number of king buckets.
const NumKB = 4

// kingBucket maps a king's 0x88 square to its file zone (0..3).
func kingBucket(kingSq byte) int { return int(kingSq&7) >> 1 }

// KBTables holds the per-bucket PSQT deltas, indexed [bucket][type]
// [0x88 square] in the same white-POV convention as psqtMG/psqtEG.
// Kings (type 6) always carry zero delta (they define the bucket).
type KBTables struct {
	MG, EG [NumKB][7][128]int
}

// kbDelta computes the tapered, white-POV king-bucket delta for the
// current position (recomputed per eval, like pawnterm — the asm would
// carry it in an accumulator refreshed on king moves).
func (e *Engine) kbDelta() int {
	p := &e.Pos
	wb := kingBucket(p.PieceSq[0])
	bb := kingBucket(p.PieceSq[16])
	var dmg, deg int
	for slot := 0; slot < 32; slot++ {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		piece := p.Board[sq]
		typ := piece & TypeMask
		if typ == King {
			continue
		}
		if piece&ColorMask == 0 {
			dmg += e.KB.MG[wb][typ][sq]
			deg += e.KB.EG[wb][typ][sq]
		} else {
			dmg -= e.KB.MG[bb][typ][sq^0x70]
			deg -= e.KB.EG[bb][typ][sq^0x70]
		}
	}
	return taper(dmg, deg, p.Phase)
}

// sq88to64 maps a 0x88 square to the PeSTO 0..63 index (a8=0, h1=63).
func sq88to64(sq byte) int {
	rank := int(sq >> 4)
	file := int(sq & 7)
	return (7-rank)*8 + file
}

// --- FEN-labeled corpus (bucketed PSQT needs board placement, which
// the pawn-feature corpus discarded) ---

// FenRow is one labeled position for king-bucket tuning.
type FenRow struct {
	FEN string
	R   float64 // game result, white POV
}

// GenerateFENData self-plays fixed-depth games and returns the quiet
// sampled positions as FEN + white-POV result (same gates as the pawn-
// feature collector in PlayGame).
func GenerateFENData(openings [][]string, w Weights, depth, games, workers int, seed uint64, progress func(games, rows int)) ([]FenRow, error) {
	cfg := PlayerCfg{Features: FtAll, Weights: w, Depth: depth, QS: DefaultQS}
	var mu sync.Mutex
	var rows []FenRow
	next, done := 0, 0
	var firstErr error
	if workers <= 0 {
		workers = 1
	}
	var wg sync.WaitGroup
	for wk := range workers {
		wg.Add(1)
		go func(wk int) {
			defer wg.Done()
			rnd := rand.New(rand.NewPCG(seed^0x4b1f, uint64(wk)*0x9e3779b97f4a7c15+1))
			for {
				mu.Lock()
				if next >= games || firstErr != nil {
					mu.Unlock()
					return
				}
				g := next
				next++
				mu.Unlock()

				rec, err := PlayGame(cfg, cfg, openings[g%len(openings)], rnd, true)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				for _, f := range rec.QuietFENs {
					rows = append(rows, FenRow{FEN: f, R: rec.Result})
				}
				done++
				if progress != nil && done%50 == 0 {
					progress(done, len(rows))
				}
				mu.Unlock()
			}
		}(wk)
	}
	wg.Wait()
	return rows, firstErr
}

// WriteFenRows writes rows as gzip "R fen" lines.
func WriteFenRows(path string, rows []FenRow) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	w := bufio.NewWriter(gz)
	for _, r := range rows {
		fmt.Fprintf(w, "%g %s\n", r.R, r.FEN)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadFenRows reads a file written by WriteFenRows (gzip-aware).
func ReadFenRows(path string) ([]FenRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	var sc *bufio.Scanner
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		sc = bufio.NewScanner(gz)
	} else {
		sc = bufio.NewScanner(br)
	}
	sc.Buffer(make([]byte, 0, 256), 1<<20)
	var rows []FenRow
	for sc.Scan() {
		line := sc.Text()
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		var r float64
		if _, err := fmt.Sscan(line[:sp], &r); err != nil {
			return nil, fmt.Errorf("bad row %q: %w", line, err)
		}
		rows = append(rows, FenRow{FEN: line[sp+1:], R: r})
	}
	return rows, sc.Err()
}

// --- gradient-descent tuner for the bucket deltas ---

// kbParams flattens the deltas: [phase(0=MG,1=EG)][bucket][type 0..4 =
// P,N,B,R,Q][sq 0..63]. Kings excluded.
const kbNTypes = 5
const kbNParams = 2 * NumKB * kbNTypes * 64

func kbIndex(phase, bucket, typ0, sq64 int) int {
	return ((phase*NumKB+bucket)*kbNTypes+typ0)*64 + sq64
}

// kbFeat is one sparse (param, coefficient) contribution.
type kbFeat struct {
	idx int32
	c   float64
}

// kbExample is a tuning position: fixed white-POV base score, result,
// and the sparse feature coefficients for the bucket params.
type kbExample struct {
	base  float64
	r     float64
	feats []kbFeat
}

// buildKBExample decomposes a position into its base eval (taperedWhite
// + pstruct + tempo, white POV, all with weights w) and the bucket
// feature coefficients (piece contributions, taper folded in linearly).
func buildKBExample(eng *Engine, pos *Position, w Weights, r float64) kbExample {
	eng.Features = FtAll
	eng.Weights = w
	eng.KB = nil
	eng.SetPosition(pos)
	p := &eng.Pos
	base := float64(p.taperedWhite() + p.PStruct)
	if p.Side == 0 {
		base += Tempo
	} else {
		base -= Tempo
	}
	phase := p.Phase
	if phase >= 25 {
		phase = 24
	}
	frac := float64(phaseW[phase]) / 32 // MG weight; EG weight = 1-frac
	wb := kingBucket(p.PieceSq[0])
	bb := kingBucket(p.PieceSq[16])
	var feats []kbFeat
	for slot := 0; slot < 32; slot++ {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		piece := p.Board[sq]
		typ := int(piece & TypeMask)
		if typ == King {
			continue
		}
		typ0 := typ - 1 // P=0..Q=4
		var bucket, sq64 int
		var sign float64
		if piece&ColorMask == 0 {
			bucket, sq64, sign = wb, sq88to64(sq), 1
		} else {
			bucket, sq64, sign = bb, sq88to64(sq^0x70), -1
		}
		feats = append(feats,
			kbFeat{int32(kbIndex(0, bucket, typ0, sq64)), sign * frac},
			kbFeat{int32(kbIndex(1, bucket, typ0, sq64)), sign * (1 - frac)})
	}
	return kbExample{base: base, r: r, feats: feats}
}

// BuildKBExamples decomposes FEN rows into tuning examples in parallel.
func BuildKBExamples(rows []FenRow, w Weights, workers int) ([]kbExample, error) {
	if workers <= 0 {
		workers = 1
	}
	exs := make([]kbExample, len(rows))
	errs := make([]error, workers)
	var wg sync.WaitGroup
	chunk := (len(rows) + workers - 1) / workers
	for wk := 0; wk < workers; wk++ {
		lo := wk * chunk
		hi := lo + chunk
		if hi > len(rows) {
			hi = len(rows)
		}
		if lo >= hi {
			continue
		}
		wg.Add(1)
		go func(wk, lo, hi int) {
			defer wg.Done()
			eng := NewEngine()
			for i := lo; i < hi; i++ {
				pos, err := ParseFEN(rows[i].FEN)
				if err != nil {
					errs[wk] = err
					return
				}
				exs[i] = buildKBExample(eng, pos, w, rows[i].R)
			}
		}(wk, lo, hi)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return exs, nil
}

// FitKBK grids the sigmoid scale that minimizes the base-only loss
// (params all zero), matching FitK for the pawn tuner.
func FitKBK(exs []kbExample) float64 {
	zero := make([]float64, kbNParams)
	bestK, bestL := 1.0, math.Inf(1)
	for k := 0.2; k <= 2.4; k += 0.05 {
		if l := kbLoss(exs, zero, k); l < bestL {
			bestK, bestL = k, l
		}
	}
	return bestK
}

// kbEval returns base + Σ coeff*param for an example.
func kbEval(ex *kbExample, params []float64) float64 {
	e := ex.base
	for _, f := range ex.feats {
		e += f.c * params[f.idx]
	}
	return e
}

// KBLoss is the exported mean-squared logistic error over examples for
// a parameter vector (nil = base-only). Used for validation reporting.
func KBLoss(exs []kbExample, params []float64, k float64) float64 {
	if params == nil {
		params = make([]float64, kbNParams)
	}
	return kbLoss(exs, params, k)
}

// kbLoss is the mean squared logistic error over examples.
func kbLoss(exs []kbExample, params []float64, k float64) float64 {
	sum := 0.0
	for i := range exs {
		d := exs[i].r - sigmoid(kbEval(&exs[i], params), k)
		sum += d * d
	}
	return sum / float64(len(exs))
}

// TuneKB fits the bucket deltas by full-batch Adam with L2 weight
// decay. Adam's momentum + per-parameter second-moment scaling suits
// this sparse problem (each delta appears in only a few percent of
// positions) far better than a flat learning rate or plain AdaGrad,
// which bang-bangs on the full-batch gradient. k is the sigmoid scale;
// lambda the L2 strength; iters the step count. Returns the tuned
// params and eval-ready KBTables, plus train loss before/after.
// progress (optional) fires every 50 iters.
func TuneKB(exs []kbExample, k, lambda, lr float64, iters int, progress func(it int, loss float64, params []float64)) ([]float64, *KBTables, float64, float64) {
	params := make([]float64, kbNParams)
	grad := make([]float64, kbNParams)
	m := make([]float64, kbNParams) // Adam 1st moment
	v := make([]float64, kbNParams) // Adam 2nd moment
	before := kbLoss(exs, params, k)
	kc := k * math.Ln10 / 400
	n := float64(len(exs))
	const b1, b2, eps = 0.9, 0.999, 1e-8
	for it := 0; it < iters; it++ {
		for i := range grad {
			grad[i] = 0
		}
		for i := range exs {
			ex := &exs[i]
			s := sigmoid(kbEval(ex, params), k)
			// d/dparam of (r-s)^2 = 2(s-r)*s(1-s)*kc*coeff
			g := 2 * (s - ex.r) * s * (1 - s) * kc
			for _, f := range ex.feats {
				grad[f.idx] += g * f.c
			}
		}
		bc1 := 1 - math.Pow(b1, float64(it+1))
		bc2 := 1 - math.Pow(b2, float64(it+1))
		for j := range params {
			gj := grad[j] / n
			m[j] = b1*m[j] + (1-b1)*gj
			v[j] = b2*v[j] + (1-b2)*gj*gj
			mh := m[j] / bc1
			vh := v[j] / bc2
			// AdamW: decoupled weight decay. Folding L2 into gj instead
			// would be amplified by Adam's per-parameter normalization
			// into a full-step restoring force that pins every param to
			// zero; decoupling keeps it a gentle pull.
			params[j] -= lr * (mh/(math.Sqrt(vh)+eps) + lambda*params[j])
		}
		if progress != nil && (it+1)%50 == 0 {
			progress(it+1, kbLoss(exs, params, k), params)
		}
	}
	after := kbLoss(exs, params, k)
	return params, kbTablesFromParams(params), before, after
}

// kbTablesFromParams materializes eval-ready KBTables (0x88 indexed,
// rounded to int) from a tuned parameter vector.
func kbTablesFromParams(params []float64) *KBTables {
	t := &KBTables{}
	for b := 0; b < NumKB; b++ {
		for typ0 := 0; typ0 < kbNTypes; typ0++ {
			for sq := 0; sq < 128; sq++ {
				if sq&0x88 != 0 {
					continue
				}
				sq64 := sq88to64(byte(sq))
				t.MG[b][typ0+1][sq] = int(math.Round(params[kbIndex(0, b, typ0, sq64)]))
				t.EG[b][typ0+1][sq] = int(math.Round(params[kbIndex(1, b, typ0, sq64)]))
			}
		}
	}
	return t
}

// SaveKB / LoadKB serialize KBTables (gob) for the match driver.
func SaveKB(path string, t *KBTables) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(t); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func LoadKB(path string) (*KBTables, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t := &KBTables{}
	if err := gob.NewDecoder(f).Decode(t); err != nil {
		return nil, err
	}
	return t, nil
}

// KBStats reports the magnitude/range of tuned deltas (for the storage
// verdict — how big do the extra tables need to be).
func KBStats(t *KBTables) (maxAbs, nonzero int) {
	for b := 0; b < NumKB; b++ {
		for typ := 1; typ <= kbNTypes; typ++ {
			for sq := 0; sq < 128; sq++ {
				if sq&0x88 != 0 {
					continue
				}
				for _, v := range []int{t.MG[b][typ][sq], t.EG[b][typ][sq]} {
					if v != 0 {
						nonzero++
					}
					if a := abs(v); a > maxAbs {
						maxAbs = a
					}
				}
			}
		}
	}
	return
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
