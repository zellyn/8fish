package mirror

// Weights are the pawn-structure/king-shield weights (asm pawnterm's
// immediates and PASSEDBONUS table). All values are applied as
// unsigned byte magnitudes in the asm, so tuned values must land in
// 0..255.
type Weights struct {
	Doubled  int    // penalty per file with 2+ own pawns (flat, not per pawn)
	Isolated int    // penalty per file whose neighbors have no own pawns
	Passed   [8]int // bonus by advancement rank (1..6 reachable)
	Shield   int    // per own-pawn-occupied file of the 3 around a home-rank king
	OpenFile int    // penalty for an open own file under a home-rank king
}

// DefaultWeights are the asm's current baked-in pawnterm values, and the
// mirror MUST hold them to reproduce the asm's search: the passed-pawn
// bonuses come from cmd/gentables (passedBonus {0,15,0,21,50,52,20,0},
// Texel-tuned on the diversified corpus); doubled 12 / isolated 7 are the
// eval.s PTFILE immediates; shield 3 / open 4 are encoded in SHLDW/SHLDB
// (value = 3*count - 4*open). This set matches refPStruct (the asm-verified
// spec in internal/chesstest) exactly. (Historically these were the
// pre-tuning values, which silently drifted from the asm when its passed/
// isolated immediates were retuned but this table was not — the bug that
// made every default-weights screen evaluate pawns unlike the shipped asm.)
var DefaultWeights = Weights{
	Doubled:  12,
	Isolated: 7,
	Passed:   [8]int{0, 15, 0, 21, 50, 52, 20, 0},
	Shield:   3,
	OpenFile: 4,
}

// TunedWeights is the Texel-tuned set on the DIVERSIFIED corpus:
// 101,202 self-play quiet positions (2026-07-18) + 7,706 non-self-play
// positions from the 210-game rating-pool gauntlet (2026-07-19,
// testdata/texel-rows-2026-07-19.gz, sigmoid K=0.85, loss
// 0.103935 -> 0.103650). Folding in real-opponent games pulled the
// advanced passed-pawn bonuses down ~20-25% (self-play overvalues
// passed pawns, both sides pushing symmetrically) and the isolated
// penalty from 10 to 7; bootstrap 95% CIs put passed4/5/6 and isolated
// clearly outside the old self-play values. A depth-6 self-play A/B vs
// the old self-play-tuned set was +21 +/- 39 (home-field bias favors
// the incumbent, so neutral-to-positive here is a real endorsement).
// SelfPlayTunedWeights keeps the prior set for reference.
var TunedWeights = Weights{
	Doubled:  14,
	Isolated: 7,
	Passed:   [8]int{0, 15, 0, 21, 50, 52, 20, 0},
	Shield:   2,
	OpenFile: 3,
}

// SelfPlayTunedWeights is the prior Texel set from self-play data only
// (2026-07-18, K=0.80). Retained as the reference point the diversified
// corpus moved away from.
var SelfPlayTunedWeights = Weights{
	Doubled:  12,
	Isolated: 10,
	Passed:   [8]int{0, 18, 0, 33, 62, 69, 28, 0},
	Shield:   3,
	OpenFile: 4,
}

// pawnterm recomputes PStruct (white POV), mirroring asm pawnterm:
// doubled/isolated/passed pawns and the minimal king shield.
func (e *Engine) pawnterm() {
	p := &e.Pos
	p.PDirty = false
	f := extractPawnFeatures(p)
	p.PStruct = f.dot(&e.Weights)
}

// pawnFeatures counts each pawnterm term once so PStruct is a dot
// product with Weights — this is what makes Texel tuning linear.
// All counts are white-minus-black is NOT applied here; both sides
// kept separate for clarity.
type pawnFeatures struct {
	doubledW, doubledB   int    // files with 2+ pawns
	isolatedW, isolatedB int    // files with pawns and no neighbors
	passedW, passedB     [8]int // passed pawns by advancement (bonus index)
	shieldW, shieldB     int    // shield files (0-3; 0 if king not home)
	openW, openB         int    // open own file under a home-rank king
}

func (f *pawnFeatures) dot(w *Weights) int {
	v := 0
	v -= w.Doubled * (f.doubledW - f.doubledB)
	v -= w.Isolated * (f.isolatedW - f.isolatedB)
	for r := range 8 {
		v += w.Passed[r] * (f.passedW[r] - f.passedB[r])
	}
	v += w.Shield * (f.shieldW - f.shieldB)
	v -= w.OpenFile * (f.openW - f.openB)
	return v
}

// extractPawnFeatures mirrors asm pawnterm's per-file scans exactly,
// including its quirks: the doubled penalty is flat per file (not per
// extra pawn), only the most advanced pawn per file is tested for
// passed status, an enemy pawn at the SAME rank on an adjacent file
// blocks (conservative), and the king shield applies only on the
// king's own back rank.
func extractPawnFeatures(p *Position) *pawnFeatures {
	var pwcnt, pbcnt [8]int
	var pwmax, pbmax [8]int // 0 if none
	var pwmin, pbmin [8]int // 15 if none
	for i := range 8 {
		pwmin[i], pbmin[i] = 15, 15
	}
	for slot := range 32 {
		sq := p.PieceSq[slot]
		if sq == NoSq {
			continue
		}
		if p.Board[sq]&TypeMask != Pawn {
			continue
		}
		file := int(sq & 0x07)
		rank := int(sq >> 4)
		if slot < 16 {
			pwcnt[file]++
			if rank > pwmax[file] {
				pwmax[file] = rank
			}
			if rank < pwmin[file] {
				pwmin[file] = rank
			}
		} else {
			pbcnt[file]++
			if rank > pbmax[file] {
				pbmax[file] = rank
			}
			if rank < pbmin[file] {
				pbmin[file] = rank
			}
		}
	}

	f := &pawnFeatures{}
	neigh := func(cnt *[8]int, x int) int {
		n := 0
		if x > 0 {
			n |= cnt[x-1]
		}
		if x < 7 {
			n |= cnt[x+1]
		}
		return n
	}
	max3 := func(arr *[8]int, x int) int {
		m := arr[x]
		if x > 0 && arr[x-1] > m {
			m = arr[x-1]
		}
		if x < 7 && arr[x+1] > m {
			m = arr[x+1]
		}
		return m
	}
	min3 := func(arr *[8]int, x int) int {
		m := arr[x]
		if x > 0 && arr[x-1] < m {
			m = arr[x-1]
		}
		if x < 7 && arr[x+1] < m {
			m = arr[x+1]
		}
		return m
	}

	for x := range 8 {
		if pwcnt[x] > 0 {
			if pwcnt[x] > 1 {
				f.doubledW++
			}
			if neigh(&pwcnt, x) == 0 {
				f.isolatedW++
			}
			// White passed: most advanced pawn, no black pawn at
			// rank >= r on files x-1..x+1.
			r := pwmax[x]
			if max3(&pbmax, x) < r {
				f.passedW[r]++
			}
		}
		if pbcnt[x] > 0 {
			if pbcnt[x] > 1 {
				f.doubledB++
			}
			if neigh(&pbcnt, x) == 0 {
				f.isolatedB++
			}
			// Black passed: most advanced = lowest rank; blocked by a
			// white pawn at rank <= r on files x-1..x+1.
			r := pbmin[x]
			if min3(&pwmin, x) > r {
				f.passedB[7-r]++
			}
		}
	}

	// King shield: only for kings on their own back rank.
	wk := p.PieceSq[0]
	if wk != NoSq && wk&0x70 == 0 {
		file := int(wk & 0x07)
		if pwcnt[file] > 0 {
			f.shieldW++
		}
		if file > 0 && pwcnt[file-1] > 0 {
			f.shieldW++
		}
		if file < 7 && pwcnt[file+1] > 0 {
			f.shieldW++
		}
		if pwcnt[file] == 0 {
			f.openW = 1
		}
	}
	bk := p.PieceSq[16]
	if bk != NoSq && bk&0x70 == 0x70 {
		file := int(bk & 0x07)
		if pbcnt[file] > 0 {
			f.shieldB++
		}
		if file > 0 && pbcnt[file-1] > 0 {
			f.shieldB++
		}
		if file < 7 && pbcnt[file+1] > 0 {
			f.shieldB++
		}
		if pbcnt[file] == 0 {
			f.openB = 1
		}
	}
	return f
}

// taperedWhite computes the taper exactly as asm eval: w = PHASEW[min
// (phase,24)]; w=32 -> MG, w=0 -> EG, else EG + sign(D)*((|D|*w)>>5)
// with D = MG-EG (magnitude multiply, so the shift truncates toward
// zero). White POV, no pstruct/tempo/pov/dither.
func (p *Position) taperedWhite() int {
	return taper(p.MG, p.EG, p.Phase)
}

// taper blends an (MG, EG) score pair by game phase, exactly as asm
// eval (magnitude multiply, shift truncates toward zero).
func taper(mg, eg, phase int) int {
	if phase >= 25 {
		phase = 24
	}
	w := phaseW[phase]
	switch w {
	case 32:
		return mg
	case 0:
		return eg
	}
	d := mg - eg
	neg := d < 0
	if neg {
		d = -d
	}
	prod := (d * w) >> 5
	if neg {
		prod = -prod
	}
	return eg + prod
}

// eval returns the score from the side to move's POV including the
// pstruct term, tempo, and dither, mirroring asm eval.
func (e *Engine) eval() int {
	if e.cyc {
		e.chargeEval(e.Extra.anyOn())
	}
	p := &e.Pos
	score := p.taperedWhite()
	if e.KB != nil {
		score += e.kbDelta()
	}
	if e.Features&FtPstruct != 0 {
		score += p.PStruct
	}
	if e.Extra.anyOn() {
		score += e.extraEval()
	}
	if p.Side != 0 {
		score = -score
	}
	score += Tempo
	if e.Seed != 0 {
		e.Seed = e.Seed*3 + 29
		score += int(e.Seed & 3)
	}
	// Record the static eval for the improving heuristic. Gated on the
	// heuristic being active so the OFF path never touches evalStack (the
	// parity/no-op guarantee). Records at the CURRENT node's ply: eval() is
	// only ever called for the position at p.Ply during search.
	if e.Improving.on() {
		ply := e.Pos.Ply
		e.evalStack[ply] = score
		e.evalValid[ply] = true
	}
	return score
}
