package mirror

import "testing"

// TestMopupSelfplayNeutral is the STEP-3 empirical middlegame-neutrality
// check: full self-play at the asm-matched feature mask (FtAll = 0x1f),
// mop-up ON (A) vs OFF (B). Because the phase gate makes every
// middlegame eval byte-identical (see TestMopupNoMiddlegameLeak), the
// result should be neutral-to-slightly-positive (ON only helps the rare
// decisive endgame); a large swing would mean the gate leaks.
//
//	GOWORK=off go test ./internal/mirror -run TestMopupSelfplayNeutral -v -timeout 40m
func TestMopupSelfplayNeutral(t *testing.T) {
	if testing.Short() {
		t.Skip("self-play match is slow")
	}
	const (
		budget  = 150000
		pairs   = 160
		workers = 4
	)
	bases := [][]string{{"e2e4"}, {"d2d4"}, {"c2c4"}, {"g1f3"}, {"e2e4", "c7c5"}, {"d2d4", "g8f6"}}
	openings, err := GenOpenings(bases, 48, 0xE10)
	if err != nil {
		t.Fatal(err)
	}
	on := PlayerCfg{Features: FtAll, Weights: DefaultWeights, NodeBudget: budget, Mopup: DefaultMopup}
	off := PlayerCfg{Features: FtAll, Weights: DefaultWeights, NodeBudget: budget}
	res, err := Match(on, off, openings, pairs, workers, 0x1f)
	if err != nil {
		t.Fatal(err)
	}
	elo, margin := res.EloDiff()
	t.Logf("mop-up ON vs OFF (mask 0x1f, budget %d): %s", budget, res.String())
	t.Logf("Elo %+.1f +/- %.1f over %d games", elo, margin, res.Games())
}
