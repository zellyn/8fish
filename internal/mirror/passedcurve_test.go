package mirror

import (
	"math"
	"strings"
	"testing"
)

// The shipped passed-pawn bonus table {0,15,0,21,50,52,20,0} LOOKS
// broken: indexed by advancement, a passer one square from promoting
// (index 6) scores 20 while one on the 6th rank (index 5) scores 52.
// It is not broken, and these tests pin down why, so the "obvious bug"
// is not re-opened:
//
//  1. TestPassedBonusIndexing pins the indexing: index N is the pawn's
//     0x88 rank for white (chess rank N+1) and 7-rank for black, so
//     index 6 really is a 7th-rank passer and index 7 (the promotion
//     square, where no pawn can stand) is correctly 0.
//  2. TestPassedEffectiveCurve shows the bonus is only a TOP-UP on the
//     PeSTO pawn PSQT, which already pays a huge advancement curve
//     (file-averaged EG +77.6 on the 6th, +159.2 on the 7th). The
//     EFFECTIVE value of a passer — PSQT + bonus — is strictly
//     increasing from the 4th rank up; the tuner correctly spent less
//     top-up where PeSTO already pays.
//
// Measured 2026-07-25: monotone replacements screened neutral or worse
// (cycle-budget self-play, 143M cyc/move, 2000 games each — see
// docs/results.md), and a free Texel re-tune of the diversified corpus
// reproduces the shipped table exactly.

// TestPassedBonusIndexing pins which Weights.Passed bucket a passer of
// a given chess rank lands in, for both colors.
func TestPassedBonusIndexing(t *testing.T) {
	probe := func(piece string, rank int) int {
		rows := []string{"6k1", "8", "8", "8", "8", "8", "8", "6K1"}
		rows[8-rank] = piece + "7" // FEN row 0 is chess rank 8
		p, err := ParseFEN(strings.Join(rows, "/") + " w - - 0 1")
		if err != nil {
			t.Fatalf("%s rank %d: %v", piece, rank, err)
		}
		f := extractPawnFeatures(p)
		got := -1
		for i := range 8 {
			if piece == "P" && f.passedW[i] > 0 {
				got = i
			}
			if piece == "p" && f.passedB[i] > 0 {
				got = i
			}
		}
		return got
	}
	for rank := 2; rank <= 7; rank++ {
		if got, want := probe("P", rank), rank-1; got != want {
			t.Errorf("white pawn on rank %d: bucket %d, want %d", rank, got, want)
		}
		if got, want := probe("p", 9-rank), rank-1; got != want {
			t.Errorf("black pawn on rank %d: bucket %d, want %d", 9-rank, got, want)
		}
	}
	// A pawn can never stand on rank 1 or 8, so buckets 0 and 7 are
	// unreachable and their zero weights are correct.
	if DefaultWeights.Passed[0] != 0 || DefaultWeights.Passed[7] != 0 {
		t.Errorf("unreachable buckets must be 0: %v", DefaultWeights.Passed)
	}
}

// passedPSQT returns the file-averaged PeSTO pawn placement value (MG,
// EG, base piece value removed) for white on the given 0x88 rank.
func passedPSQT(rank int) (mg, eg float64) {
	var m, e int
	for f := range 8 {
		sq := rank*16 + f
		m += psqtMG[Pawn][sq] - pestoPieceMG[0]
		e += psqtEG[Pawn][sq] - pestoPieceEG[0]
	}
	return float64(m) / 8, float64(e) / 8
}

// TestPassedEffectiveCurve asserts that what a passed pawn is actually
// worth — PeSTO PSQT + pawnterm bonus — rises with advancement from the
// 4th rank up, in both game phases. This is the invariant the raw bonus
// table only APPEARS to violate.
func TestPassedEffectiveCurve(t *testing.T) {
	var lastMG, lastEG float64
	t.Logf("rank | psqtMG | psqtEG | bonus | effMG  | effEG")
	for rank := 1; rank <= 6; rank++ { // 0x88 rank 1..6 = chess rank 2..7
		mg, eg := passedPSQT(rank)
		b := float64(DefaultWeights.Passed[rank])
		t.Logf("  %d  | %6.1f | %6.1f | %5.0f | %6.1f | %6.1f", rank+1, mg, eg, b, mg+b, eg+b)
		if rank >= 3 { // chess rank 4 and up
			if mg+b <= lastMG || eg+b <= lastEG {
				t.Errorf("effective passer value not increasing at chess rank %d: "+
					"MG %.1f (prev %.1f), EG %.1f (prev %.1f)", rank+1, mg+b, lastMG, eg+b, lastEG)
			}
		}
		lastMG, lastEG = mg+b, eg+b
	}
}

// TestPassedCorpusConstraint documents that the Texel corpus is NOT
// sparse at the extreme buckets (the "few 7th-rank positions" theory
// for the non-monotone fit) and that the shipped values sit at the
// bottom of a real, curved loss basin. Slow: loads and scores 108,908
// rows several dozen times.
func TestPassedCorpusConstraint(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: scores the whole Texel corpus dozens of times")
	}
	rows, err := LoadRows("testdata/texel-rows-2026-07-19.gz")
	if err != nil {
		t.Fatal(err)
	}
	k := FitK(rows, DefaultWeights)
	t.Logf("corpus %d rows (diversified: 101,202 self-play + 7,706 pool), K %.2f", len(rows), k)

	for j := 2; j <= 7; j++ { // weightsVec slots for Passed[1..6]
		nz := 0
		for i := range rows {
			if rows[i].F[j] != 0 {
				nz++
			}
		}
		if nz < len(rows)/40 { // every bucket is constrained by >2.5% of rows
			t.Errorf("passed[%d] constrained by only %d rows", j-1, nz)
		}
		v := weightsVec(DefaultWeights)
		best, bestL := 0.0, math.Inf(1)
		for x := 0.0; x <= 140; x += 5 {
			c := v
			c[j] = x
			if l := texelLoss(rows, c, k); l < bestL {
				best, bestL = x, l
			}
		}
		t.Logf("passed[%d] (chess rank %d): nonzero rows %6d (%5.2f%%)  1-D argmin %3.0f  shipped %3d",
			j-1, j, nz, 100*float64(nz)/float64(len(rows)), best, DefaultWeights.Passed[j-1])
		if math.Abs(best-float64(DefaultWeights.Passed[j-1])) > 10 {
			t.Errorf("passed[%d]: shipped %d is far from the corpus argmin %.0f",
				j-1, DefaultWeights.Passed[j-1], best)
		}
	}
}
