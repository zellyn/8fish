package mirror

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"sync"
)

// GenerateData plays a fixed number of fixed-depth self-play games
// (all features on, dither on, both sides using weights w) and
// collects quiet labeled positions.
func GenerateData(openings [][]string, w Weights, depth, games, workers int, seed uint64, progress func(games, samples int)) ([]Sample, error) {
	// QS must be the asm's shipped recap2 shape, not the zero-value
	// (unlimited) — otherwise the Texel corpus is generated from a
	// quiescence the shipped engine never uses (fix, 2026-07-23).
	cfg := PlayerCfg{Features: FtAll, Weights: w, Depth: depth, QS: DefaultQS}
	var mu sync.Mutex
	var samples []Sample
	next, done := 0, 0
	var firstErr error

	var wg sync.WaitGroup
	if workers <= 0 {
		workers = 1
	}
	for wk := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rnd := rand.New(rand.NewPCG(seed^0xda7a6e2, uint64(wk)*0x9e3779b97f4a7c15+1))
			for {
				mu.Lock()
				if next >= games || firstErr != nil {
					mu.Unlock()
					return
				}
				g := next
				next++
				mu.Unlock()

				opening := openings[g%len(openings)]
				rec, err := PlayGame(cfg, cfg, opening, rnd, true)

				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
				samples = append(samples, rec.Samples...)
				done++
				if progress != nil && done%50 == 0 {
					progress(done, len(samples))
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return samples, firstErr
}

// Row is one serialized training position: the fixed eval base, the
// 10-element pawn-structure feature vector (weightsVec layout), and
// the game outcome.
type Row struct {
	Base, R float64
	F       [10]float64
}

// SampleRows converts collected samples to tuner rows.
func SampleRows(samples []Sample) []Row {
	rows := make([]Row, len(samples))
	for i := range samples {
		rows[i] = Row{Base: float64(samples[i].Base), R: samples[i].R, F: featVec(&samples[i].F)}
	}
	return rows
}

// AppendRows appends rows to a data file (one "R base f0..f9" line
// each), so generation can run in restartable chunks.
func AppendRows(path string, rows []Row) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, r := range rows {
		fmt.Fprintf(w, "%g %g", r.R, r.Base)
		for _, x := range r.F {
			fmt.Fprintf(w, " %g", x)
		}
		fmt.Fprintln(w)
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// LoadRows reads a data file written by AppendRows. A gzip-compressed
// file (magic 0x1f 0x8b, e.g. the checked-in testdata corpus) is
// transparently decompressed.
func LoadRows(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var src io.Reader = bufio.NewReader(f)
	magic, err := src.(*bufio.Reader).Peek(2)
	if err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(src)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		src = gz
	}
	var rows []Row
	sc := bufio.NewScanner(src)
	for sc.Scan() {
		var r Row
		vals := []any{&r.R, &r.Base}
		for i := range r.F {
			vals = append(vals, &r.F[i])
		}
		if _, err := fmt.Sscan(sc.Text(), vals...); err != nil {
			return nil, fmt.Errorf("bad row %q: %w", sc.Text(), err)
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

// weightsVec flattens the tunable parameters: doubled, isolated,
// passed[1..6], shield, openfile (10 values).
func weightsVec(w Weights) [10]float64 {
	return [10]float64{
		float64(w.Doubled), float64(w.Isolated),
		float64(w.Passed[1]), float64(w.Passed[2]), float64(w.Passed[3]),
		float64(w.Passed[4]), float64(w.Passed[5]), float64(w.Passed[6]),
		float64(w.Shield), float64(w.OpenFile),
	}
}

// vecWeights rounds a parameter vector back to integer Weights,
// clamping to the asm's unsigned-byte range.
func vecWeights(v [10]float64) Weights {
	clamp := func(x float64) int {
		n := int(math.Round(x))
		if n < 0 {
			n = 0
		}
		if n > 255 {
			n = 255
		}
		return n
	}
	return Weights{
		Doubled:  clamp(v[0]),
		Isolated: clamp(v[1]),
		Passed:   [8]int{0, clamp(v[2]), clamp(v[3]), clamp(v[4]), clamp(v[5]), clamp(v[6]), clamp(v[7]), 0},
		Shield:   clamp(v[8]),
		OpenFile: clamp(v[9]),
	}
}

// featVec extracts the per-sample feature multipliers matching
// weightsVec's layout (white minus black, with the signs pawnFeatures.
// dot applies).
func featVec(f *pawnFeatures) [10]float64 {
	return [10]float64{
		-float64(f.doubledW - f.doubledB),
		-float64(f.isolatedW - f.isolatedB),
		float64(f.passedW[1] - f.passedB[1]),
		float64(f.passedW[2] - f.passedB[2]),
		float64(f.passedW[3] - f.passedB[3]),
		float64(f.passedW[4] - f.passedB[4]),
		float64(f.passedW[5] - f.passedB[5]),
		float64(f.passedW[6] - f.passedB[6]),
		float64(f.shieldW - f.shieldB),
		-float64(f.openW - f.openB),
	}
}

func sigmoid(evalCp, k float64) float64 {
	return 1 / (1 + math.Pow(10, -k*evalCp/400))
}

// texelLoss is the mean squared error of the logistic prediction over
// the rows for parameter vector v.
func texelLoss(rows []Row, v [10]float64, k float64) float64 {
	sum := 0.0
	for i := range rows {
		eval := rows[i].Base
		for j, fj := range rows[i].F {
			eval += fj * v[j]
		}
		d := rows[i].R - sigmoid(eval, k)
		sum += d * d
	}
	return sum / float64(len(rows))
}

// FitK finds the sigmoid scale minimizing the loss with the given
// weights fixed.
func FitK(rows []Row, w Weights) float64 {
	v := weightsVec(w)
	bestK, bestL := 1.0, math.Inf(1)
	for k := 0.2; k <= 2.4; k += 0.05 {
		if l := texelLoss(rows, v, k); l < bestL {
			bestK, bestL = k, l
		}
	}
	return bestK
}

// Tune runs multi-scale integer coordinate descent on the pawn-
// structure weights, PeSTO fixed. Returns the tuned weights and the
// loss before/after.
func Tune(rows []Row, start Weights, k float64, progress func(step string, loss float64)) (Weights, float64, float64) {
	v := weightsVec(start)
	lossBefore := texelLoss(rows, v, k)
	best := lossBefore
	for _, step := range []float64{32, 16, 8, 4, 2, 1} {
		improved := true
		for improved {
			improved = false
			for j := range v {
				for _, dir := range []float64{step, -step} {
					cand := v
					cand[j] += dir
					if cand[j] < 0 {
						cand[j] = 0
					}
					if cand[j] > 255 {
						cand[j] = 255
					}
					if cand[j] == v[j] {
						continue
					}
					if l := texelLoss(rows, cand, k); l < best {
						v, best = cand, l
						improved = true
					}
				}
			}
		}
		if progress != nil {
			progress(fmt.Sprintf("step %g", step), best)
		}
	}
	return vecWeights(v), lossBefore, best
}
