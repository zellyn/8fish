package chesstest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// Structural-slack instrument (diagnostic only; no engine change).
//
// TestProfileR5 answers "where do the cycles go" per label. It does NOT
// answer the questions a STRUCTURAL hunt needs:
//
//   - How many TIMES is each routine entered? (a label's cycle share
//     divided by its entry count is the only way to price "avoid one
//     call of X".)
//   - How many list entries do the five move-loop passes SCAN versus
//     SEARCH? (scanned-but-rejected entries are pure discarded work.)
//   - How much of the whole search runs BELOW the horizon (quiescence)?
//
// Same phase-diverse workload and shipped configuration as
// TestProfileR5, so the numbers compose with it.
//
// Run explicitly: go test ./internal/chesstest -run TestStructHunt -v
// ---------------------------------------------------------------------------

type shProfile struct {
	cycles []uint64
	execs  []uint64
	total  uint64
	// cycle split by node region: qs = PLY >= MAXDEPTH at the moment the
	// instruction executed (the engine's own horizon test; MAXDEPTH is
	// mutated by null/LMR/ckext exactly as the engine sees it).
	qsCycles   uint64
	fullCycles uint64
	// slfull reason -> [legal, illegal] counts
	legal map[string]*[2]uint64
	// slfull reason -> cycles spent from sdomove entry to the legality
	// verdict, split [legalCycles, illegalCycles]. This is the exact
	// price of "make + gate + attacked (+ unmake)" for that class.
	legalCyc map[string]*[2]uint64
	// same, for moves that took the FAST gate (never called attacked)
	fastN, fastCyc uint64

	// ---- evasion-filter model -------------------------------------
	// [0]=filter rejects, [1]=filter passes, [2]=model undecided;
	// inner index 0=move was actually legal, 1=actually illegal.
	// A nonzero evasion[0][0] would be a CORRECTNESS bug in the filter.
	evasion    [3][2]uint64
	evasionCyc [3][2]uint64

	// ---- pin-ray model (category 4: from-square king-aligned) ------
	// [0]=ray test says LEGAL, [1]=says ILLEGAL; inner 0=really legal,
	// 1=really illegal. pin[0][1] and pin[1][0] must both be 0.
	pin     [2][2]uint64
	pinCyc  [2][2]uint64
	pinStep uint64 // total BOARD probes the ray walk performed
	pinN    uint64
	// evasion rejects split by rule, and double-check exposure
	evRejTarget, evRejDouble, evDoubleMoves uint64

	// ---- lazy-eval opportunity -------------------------------------
	// Per eval call, keyed by call region ("qs" = stand pat past the
	// horizon, "fw" = the null/RFP gates above it):
	//   n         calls
	//   dirty     calls whose PSTRUCT was stale (pawntermfull ran)
	//   fh        calls whose FINAL score failed high vs beta
	//   bail[M]   calls where the accumulator score ALONE, minus margin
	//             M, already fails high (so a lazy bail is provable) AND
	//             PSTRUCT was stale (so a bail would actually save work)
	ev map[string]*evStat
}

type evStat struct {
	n, dirty, fh uint64
	bail         [6]uint64 // margins shEvMargins
	bailAny      [6]uint64 // same but ignoring the dirty requirement
}

var shEvMargins = [6]int{0, 32, 64, 100, 160, 250}

// ---------------------------------------------------------------------------
// Evasion-filter model. At an in-check node, a non-king, non-ep move can
// only be legal if it captures the checker or interposes on the ray
// between king and checker (and single check, not double). That test is
// two table lookups on the 0x88 difference; the current engine instead
// makes the move, calls attacked(), and unmakes.
//
// shEvasionModel reproduces exactly that filter against the LIVE parent
// board in machine memory, so the measured reject rate is the real one
// rather than an estimate. Returns (rejected, ok): ok=false when the
// model declines to judge (no checker found / >2 checkers), which is
// counted separately rather than assumed favourable.
// ---------------------------------------------------------------------------

var shSlideDirs = [8]int{-17, -16, -15, -1, 1, 15, 16, 17}
var shKnightDirs = [8]int{-33, -31, -18, -14, 14, 18, 31, 33}

func shOnBoard(sq int) bool { return sq >= 0 && sq < 128 && sq&0x88 == 0 }

// shCheckers finds every square from which side `by` attacks `ksq`.
func shCheckers(board []byte, ksq int, by byte) []int {
	var out []int
	for _, d := range shKnightDirs {
		s := ksq + d
		if shOnBoard(s) {
			p := board[s]
			if p != 0 && p&0x08 == by && p&0x07 == 2 {
				out = append(out, s)
			}
		}
	}
	for _, d := range shSlideDirs {
		diag := d != -16 && d != 16 && d != -1 && d != 1
		for s, n := ksq+d, 1; shOnBoard(s); s, n = s+d, n+1 {
			p := board[s]
			if p == 0 {
				continue
			}
			if p&0x08 == by {
				t := p & 0x07
				switch {
				case t == 5: // queen
					out = append(out, s)
				case t == 4 && !diag: // rook
					out = append(out, s)
				case t == 3 && diag: // bishop
					out = append(out, s)
				case t == 6 && n == 1: // king
					out = append(out, s)
				case t == 1 && n == 1 && diag:
					// pawn: white (by==0) attacks upward (+15/+17 from
					// its square means the king is at s+15/s+17)
					if by == 0 && (d == -15 || d == -17) {
						out = append(out, s)
					} else if by != 0 && (d == 15 || d == 17) {
						out = append(out, s)
					}
				}
			}
			break
		}
	}
	return out
}

// shRayDir returns the single-step 0x88 direction from a to b if they lie
// on a queen line, else 0. (The asm equivalent is one DELTATAB lookup.)
func shRayDir(a, b int) int {
	dr, df := b/16-a/16, b%16-a%16
	switch {
	case dr == 0 && df == 0:
		return 0
	case dr == 0:
		if df > 0 {
			return 1
		}
		return -1
	case df == 0:
		if dr > 0 {
			return 16
		}
		return -16
	case dr == df:
		if dr > 0 {
			return 17
		}
		return -17
	case dr == -df:
		if dr > 0 {
			return 15
		}
		return -15
	}
	return 0
}

// shPinLegal models the single-ray pin test that would replace the full
// attacked() scan for category-4 moves. Pre-make view of the board.
// Returns (legal, probes).
func shPinLegal(board []byte, ksq, from, to int, mover byte) (bool, uint64) {
	d := shRayDir(ksq, from)
	if d == 0 {
		return true, 0 // not aligned: caller would not have entered
	}
	var probes uint64
	// first occupied square from the king along d
	s := ksq + d
	for shOnBoard(s) {
		probes++
		if board[s] != 0 {
			break
		}
		s += d
	}
	if !shOnBoard(s) || s != from {
		return true, probes // the mover is not the blocker on this ray
	}
	// continue past FROM looking for an enemy slider on this line
	s += d
	for shOnBoard(s) {
		probes++
		if board[s] != 0 {
			break
		}
		s += d
	}
	if !shOnBoard(s) {
		return true, probes
	}
	p := board[s]
	if p&0x08 == mover&0x08 {
		return true, probes // own piece behind: no pin
	}
	diag := d != 1 && d != -1 && d != 16 && d != -16
	t := p & 0x07
	pins := t == 5 || (t == 4 && !diag) || (t == 3 && diag)
	if !pins {
		return true, probes
	}
	// pinned: legal only if the move stays on the same ray
	return shRayDir(ksq, to) == d, probes
}

// shBetween reports whether `to` lies strictly between ksq and csq on a
// straight line (the interpose set).
func shBetween(ksq, csq, to int) bool {
	d := csq - ksq
	var step int
	for _, s := range shSlideDirs {
		if d%s == 0 && d/s > 0 && d/s < 8 {
			// candidate; confirm it is the right geometric line
			if (s == 1 || s == -1) && ksq/16 != csq/16 {
				continue
			}
			if (s == 16 || s == -16) && ksq%16 != csq%16 {
				continue
			}
			if s != 1 && s != -1 && s != 16 && s != -16 {
				// diagonal: |rank delta| must equal |file delta|
				dr, df := csq/16-ksq/16, csq%16-ksq%16
				if dr*dr != df*df {
					continue
				}
			}
			step = s
			break
		}
	}
	if step == 0 {
		return false
	}
	for s := ksq + step; s != csq; s += step {
		if s == to {
			return true
		}
	}
	return false
}

var shLabels map[string]uint16

func labelsFor(t *testing.T) map[string]uint16 {
	t.Helper()
	if shLabels == nil {
		var err error
		shLabels, err = ParseLabelFile("../../asm/engine.lbl")
		if err != nil {
			t.Fatal(err)
		}
	}
	return shLabels
}

func newSHProfile() *shProfile {
	return &shProfile{cycles: make([]uint64, 65536), execs: make([]uint64, 65536)}
}

func (p *shProfile) add(o *shProfile) {
	for i, c := range o.cycles {
		p.cycles[i] += c
	}
	for i, c := range o.execs {
		p.execs[i] += c
	}
	p.total += o.total
	p.qsCycles += o.qsCycles
	p.fullCycles += o.fullCycles
	if p.legal == nil {
		p.legal = map[string]*[2]uint64{}
	}
	for k, v := range o.legal {
		if p.legal[k] == nil {
			p.legal[k] = &[2]uint64{}
		}
		p.legal[k][0] += v[0]
		p.legal[k][1] += v[1]
	}
	if p.legalCyc == nil {
		p.legalCyc = map[string]*[2]uint64{}
	}
	for k, v := range o.legalCyc {
		if p.legalCyc[k] == nil {
			p.legalCyc[k] = &[2]uint64{}
		}
		p.legalCyc[k][0] += v[0]
		p.legalCyc[k][1] += v[1]
	}
	p.fastN += o.fastN
	p.fastCyc += o.fastCyc
	for i := range o.evasion {
		for j := range o.evasion[i] {
			p.evasion[i][j] += o.evasion[i][j]
			p.evasionCyc[i][j] += o.evasionCyc[i][j]
		}
	}
	for i := range o.pin {
		for j := range o.pin[i] {
			p.pin[i][j] += o.pin[i][j]
			p.pinCyc[i][j] += o.pinCyc[i][j]
		}
	}
	p.pinStep += o.pinStep
	p.pinN += o.pinN
	p.evRejTarget += o.evRejTarget
	p.evRejDouble += o.evRejDouble
	p.evDoubleMoves += o.evDoubleMoves
	if p.ev == nil {
		p.ev = map[string]*evStat{}
	}
	for k, v := range o.ev {
		d := p.ev[k]
		if d == nil {
			d = &evStat{}
			p.ev[k] = d
		}
		d.n += v.n
		d.dirty += v.dirty
		d.fh += v.fh
		for i := range v.bail {
			d.bail[i] += v.bail[i]
			d.bailAny[i] += v.bailAny[i]
		}
	}
}

func shRun(t *testing.T, bin []byte, defs Defs, fen string) *shProfile {
	t.Helper()
	pos, err := ParseFEN(fen)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	SetFeatures(m, defs, byte(defs["FT_CKEXT"])|0x1F)
	SetFeatures2(m, defs, byte(defs["FT2_GENDEFER"]))
	SetBudget(m, defs, 30_000_000, 24)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove

	p := newSHProfile()
	p.legal = map[string]*[2]uint64{}
	p.legalCyc = map[string]*[2]uint64{}
	plyAddr := defs["PLY"]
	mdAddr := defs["MAXDEPTH"]
	inchk := defs["INCHK"]
	L := labelsFor(t)
	slfull, slegal, sloopj, sdomove := L["slfull"], L["slegal"], L["sloopj"], L["sdomove"]
	pending := ""
	var moveCyc uint64
	inMove := false

	p.ev = map[string]*evStat{"qs": {}, "fw": {}}
	evalAddr, evdone := L["eval"], L["evdone"]
	rd16 := func(a uint16) int {
		return int(int16(uint16(m.Mem.Main[a]) | uint16(m.Mem.Main[a+1])<<8))
	}
	var evKey string
	var evBeta int
	var evDirty bool
	evPending := false
	board, psq := defs["BOARD"], defs["PIECESQ"]
	evVerdict, pinVerdict := -1, -1
	exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cycles uint8) {
		c := uint64(cycles)
		p.cycles[pc] += c
		p.execs[pc]++
		p.total += c
		if m.Mem.Main[plyAddr] >= m.Mem.Main[mdAddr] {
			p.qsCycles += c
		} else {
			p.fullCycles += c
		}
		// Why did this move need the FULL legality scan? Classify in the
		// same priority order as sdomove's gate (search.s:1349-1372).
		if inMove {
			moveCyc += c
		}
		switch pc {
		case evalAddr:
			ply := uint16(m.Mem.Main[plyAddr])
			evKey = "fw"
			if m.Mem.Main[plyAddr] >= m.Mem.Main[mdAddr] {
				evKey = "qs"
			}
			evBeta = int(int16(uint16(m.Mem.Main[defs["BETALO"]+ply]) |
				uint16(m.Mem.Main[defs["BETAHI"]+ply])<<8))
			evDirty = m.Mem.Main[defs["PDIRTY"]] != 0
			evPending = true
		case evdone:
			if !evPending {
				break
			}
			evPending = false
			st := p.ev[evKey]
			st.n++
			if evDirty {
				st.dirty++
			}
			score := rd16(defs["SCORE"])
			if score >= evBeta {
				st.fh++
			}
			// S0 = the score eval would have produced WITHOUT the
			// PSTRUCT term (white POV, so the sign follows the STM
			// negation that eval applies at the end).
			ps := rd16(defs["PSTRUCT"])
			if m.Mem.Main[defs["SIDE"]] != 0 {
				ps = -ps
			}
			s0 := score - ps
			for i, mg := range shEvMargins {
				if s0-mg >= evBeta {
					st.bailAny[i]++
					if evDirty {
						st.bail[i]++
					}
				}
			}
		}
		switch {
		case pc == sdomove:
			inMove, moveCyc, pending = true, 0, ""
			// Evasion-filter model, evaluated PRE-make on the parent
			// position (which is what a real pre-make filter would see).
			evVerdict, pinVerdict = -1, -1
			ply := uint16(m.Mem.Main[plyAddr])
			from := int(m.Mem.Main[defs["FROM"]])
			to := int(m.Mem.Main[defs["TO"]])
			side := m.Mem.Main[defs["SIDE"]]
			mover := m.Mem.Main[board+uint16(from)]
			ksq := int(m.Mem.Main[psq+uint16(side)*2])
			notKingNotEP := mover&0x07 != 6 && m.Mem.Main[defs["MVFLAGS"]]&0x08 == 0
			if m.Mem.Main[inchk+ply] != 0 {
				if notKingNotEP {
					ch := shCheckers(m.Mem.Main[board:board+128], ksq, side^0x08)
					switch {
					case len(ch) == 0 || len(ch) > 2:
						evVerdict = 2 // model declines
					case len(ch) == 2:
						evVerdict = 0 // double check: no non-king move helps
						p.evRejDouble++
						p.evDoubleMoves++
					case to == ch[0] || shBetween(ksq, ch[0], to):
						evVerdict = 1 // addresses the check: must still verify
					default:
						evVerdict = 0 // provably illegal without making it
						p.evRejTarget++
					}
				}
			} else if notKingNotEP && shRayDir(ksq, from) != 0 {
				// category 4: the engine's gate sends this to slfull.
				ok, probes := shPinLegal(m.Mem.Main[board:board+128], ksq, from, to, mover)
				p.pinStep += probes
				p.pinN++
				pinVerdict = 1
				if ok {
					pinVerdict = 0
				}
			}
		case pc == slfull:
			ply := m.Mem.Main[plyAddr] // child ply; parent = ply-1
			king := m.Mem.Main[defs["MVPIECE"]]&0x07 == 6
			switch {
			case m.Mem.Main[inchk+uint16(ply)-1] != 0:
				if king {
					pending = "1a-evasion-KINGmove"
				} else {
					pending = "1b-evasion-other"
				}
			case m.Mem.Main[defs["MVFLAGS"]]&0x08 != 0:
				pending = "2-ep-capture"
			case king:
				pending = "3-king-move"
			default:
				pending = "4-from-king-aligned"
			}
			if p.legal[pending] == nil {
				p.legal[pending] = &[2]uint64{}
				p.legalCyc[pending] = &[2]uint64{}
			}
		case pc == slegal || pc == sloopj:
			if !inMove {
				break
			}
			inMove = false
			j := 0
			if pc == sloopj {
				j = 1
			}
			if evVerdict >= 0 {
				p.evasion[evVerdict][j]++
				p.evasionCyc[evVerdict][j] += moveCyc
				evVerdict = -1
			}
			if pinVerdict >= 0 {
				p.pin[pinVerdict][j]++
				p.pinCyc[pinVerdict][j] += moveCyc
				pinVerdict = -1
			}
			if pending == "" {
				p.fastN++
				p.fastCyc += moveCyc
				break
			}
			i := 0
			if pc == sloopj {
				i = 1
			}
			p.legal[pending][i]++
			p.legalCyc[pending][i] += moveCyc
			pending = ""
		}
	})
	if err != nil || !exited || code > 2 {
		t.Fatalf("%s: exited=%v code=%d err=%v", fen, exited, code, err)
	}
	return p
}

// shLabelOwners maps label name -> defining source file.
func shLabelOwners(t *testing.T) map[string]string {
	t.Helper()
	owner := map[string]string{}
	srcs, _ := filepath.Glob("../../asm/*.s")
	incs, _ := filepath.Glob("../../asm/*.inc")
	re := regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*):`)
	for _, path := range append(srcs, incs...) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		base := filepath.Base(path)
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			if _, dup := owner[m[1]]; !dup {
				owner[m[1]] = base
			}
		}
	}
	return owner
}

type shRow struct {
	name   string
	file   string
	addr   uint16
	cycles uint64
	enters uint64
}

func shRows(t *testing.T, p *shProfile, labels map[string]uint16, owner map[string]string) []shRow {
	type lab struct {
		addr uint16
		name string
	}
	var labs []lab
	for name, addr := range labels {
		labs = append(labs, lab{addr, name})
	}
	sort.Slice(labs, func(i, j int) bool {
		if labs[i].addr != labs[j].addr {
			return labs[i].addr < labs[j].addr
		}
		return labs[i].name < labs[j].name
	})
	var rows []shRow
	for i, l := range labs {
		end := 0x10000
		if i+1 < len(labs) {
			end = int(labs[i+1].addr)
		}
		var cyc uint64
		for pc := int(l.addr); pc < end; pc++ {
			cyc += p.cycles[pc]
		}
		if cyc == 0 {
			continue
		}
		rows = append(rows, shRow{l.name, owner[l.name], l.addr, cyc, p.execs[l.addr]})
	}
	return rows
}

func shTable(rows []shRow, total uint64) string {
	out := fmt.Sprintf("%-26s %-18s %5s %12s %8s %11s %10s\n",
		"label", "file", "addr", "cycles", "share%", "enters", "cyc/entry")
	for _, r := range rows {
		cpe := 0.0
		if r.enters > 0 {
			cpe = float64(r.cycles) / float64(r.enters)
		}
		out += fmt.Sprintf("%-26s %-18s  %04X %12d %7.3f%% %11d %10.1f\n",
			r.name, r.file, r.addr, r.cycles,
			100*float64(r.cycles)/float64(total), r.enters, cpe)
	}
	return out
}

func TestStructHunt(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	owner := shLabelOwners(t)

	total := newSHProfile()
	perPhase := map[string]*shProfile{}
	for _, pos := range r5Positions {
		p := shRun(t, bin, defs, pos.fen)
		total.add(p)
		if perPhase[pos.phase] == nil {
			perPhase[pos.phase] = newSHProfile()
		}
		perPhase[pos.phase].add(p)
	}

	t.Logf("total %d cycles; qs (PLY>=MAXDEPTH) %.2f%%, full-width %.2f%%",
		total.total,
		100*float64(total.qsCycles)/float64(total.total),
		100*float64(total.fullCycles)/float64(total.total))
	phases := []string{"opening", "midgame", "tactical", "endgame"}
	for _, ph := range phases {
		p := perPhase[ph]
		if p == nil {
			continue
		}
		t.Logf("  %-9s total %11d  qs %.2f%%  full %.2f%%", ph, p.total,
			100*float64(p.qsCycles)/float64(p.total),
			100*float64(p.fullCycles)/float64(p.total))
	}

	rows := shRows(t, total, labels, owner)
	byCyc := append([]shRow(nil), rows...)
	sort.Slice(byCyc, func(i, j int) bool { return byCyc[i].cycles > byCyc[j].cycles })
	if err := os.WriteFile("/tmp/structhunt-bycycles.txt",
		[]byte(shTable(byCyc, total.total)), 0644); err != nil {
		t.Fatal(err)
	}
	byAddr := append([]shRow(nil), rows...)
	sort.Slice(byAddr, func(i, j int) bool { return byAddr[i].addr < byAddr[j].addr })
	if err := os.WriteFile("/tmp/structhunt-byaddr.txt",
		[]byte(shTable(byAddr, total.total)), 0644); err != nil {
		t.Fatal(err)
	}
	// Per-phase label tables, so an endgame-only cost cannot hide.
	for _, ph := range phases {
		p := perPhase[ph]
		if p == nil {
			continue
		}
		pr := shRows(t, p, labels, owner)
		sort.Slice(pr, func(i, j int) bool { return pr[i].cycles > pr[j].cycles })
		os.WriteFile("/tmp/structhunt-"+ph+".txt", []byte(shTable(pr, p.total)), 0644)
	}
	t.Logf("wrote /tmp/structhunt-{bycycles,byaddr,<phase>}.txt (%d live labels)", len(rows))

	// ---- event counts at named labels ---------------------------------
	events := []string{
		"search", "squiesce", "snode", "snodeq", "sdone", "sret", "sretqs",
		"scut", "smated", "sdraw", "sdrawend", "snorep", "sreploop", "smdloop",
		"generate", "generateq", "genrecapent", "gennodef", "gennodeq",
		"emitmove", "sntry", "srdefer", "snp0", "sngen",
		"p0go", "p1go", "p2go", "p3go", "p4nkgo",
		"p0done", "p1done", "p2done", "p3done", "p1loop", "p2loop", "p4nk",
		"sgo", "sdomove", "make", "unmake", "spostsr", "sloopj",
		"slfull", "slegal", "sloopret", "mkfast", "mkslow",
		"mkfmvon", "mkfmvoff", "mkfqon", "mkfcapoff", "mkfquiet",
		"umnohash", "umtramp", "umfquiet", "umfmover", "umfpawn", "umslow2",
		"hashcatchup",
		"sdemote0", "sdemote1", "ssearch", "sckext", "ckexted",
		"eval", "evtaper", "evmgraw", "evegraw", "evnotmg", "evrookx",
		"pawntermfull", "ptkonly", "ptkings", "mkpdone", "mkkonly", "mkfd",
		"movepiece", "takepiece", "movepieceq", "takepieceq",
		"addpiece", "rempiece", "hashpiece", "mvppawn", "tkppawn",
		"attacked", "curincheck", "atgeo", "atwalk", "atghit", "atgmiss",
		"ckfast", "cknodir", "ckhit", "cknone", "ckdone", "ckwalk", "ckdwalk",
		"ttprobe", "ttstore", "ttmovevalid",
		"makenull", "nullcut", "gstart", "rfpbok", "srfpno", "qsdelta",
		"qsraise", "qsnothr", "qsthr",
	}
	ev := "event counts (entries at label):\n"
	for _, name := range events {
		addr, ok := labels[name]
		if !ok {
			ev += fmt.Sprintf("  %-16s  (no such label)\n", name)
			continue
		}
		ev += fmt.Sprintf("  %-16s %12d\n", name, total.execs[addr])
	}
	t.Logf("\n%s", ev)

	// Per-phase event counts for the phase-sensitive ones.
	pev := fmt.Sprintf("%-16s %12s", "event", "ALL")
	for _, ph := range phases {
		pev += fmt.Sprintf(" %12s", ph)
	}
	pev += "\n"
	for _, name := range []string{
		"search", "squiesce", "snodeq", "snode", "smdloop", "sreploop",
		"make", "unmake", "sgo", "slfull", "sloopj", "attacked", "atgeo",
		"generate", "generateq", "genrecapent", "emitmove",
		"p1go", "p2go", "p3go", "p4nkgo", "eval", "pawntermfull",
		"sntry", "srdefer", "scut", "hashcatchup",
	} {
		addr, ok := labels[name]
		if !ok {
			continue
		}
		pev += fmt.Sprintf("%-16s %12d", name, total.execs[addr])
		for _, ph := range phases {
			p := perPhase[ph]
			if p == nil {
				pev += fmt.Sprintf(" %12s", "-")
				continue
			}
			pev += fmt.Sprintf(" %12d", p.execs[addr])
		}
		pev += "\n"
	}
	t.Logf("\nper-phase event counts:\n%s", pev)

	// ---- why did each move need the full legality scan, and did it pay?
	lr := fmt.Sprintf("%-22s %9s %9s %8s %10s %10s %9s %9s\n",
		"slfull reason", "legal", "illegal", "illeg%",
		"cyc(legal)", "cyc(illeg)", "cyc/leg", "cyc/ill")
	keys := make([]string, 0, len(total.legal))
	for k := range total.legal {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var tl, ti, tcl, tci uint64
	for _, k := range keys {
		v, cv := total.legal[k], total.legalCyc[k]
		tl += v[0]
		ti += v[1]
		tcl += cv[0]
		tci += cv[1]
		lr += fmt.Sprintf("%-22s %9d %9d %7.1f%% %10d %10d %9.0f %9.0f  (%.2f%% / %.2f%% of all)\n",
			k, v[0], v[1], 100*float64(v[1])/float64(v[0]+v[1]), cv[0], cv[1],
			float64(cv[0])/float64(max(v[0], 1)), float64(cv[1])/float64(max(v[1], 1)),
			100*float64(cv[0])/float64(total.total), 100*float64(cv[1])/float64(total.total))
	}
	lr += fmt.Sprintf("%-22s %9d %9d %7.1f%% %10d %10d %9.0f %9.0f  (%.2f%% / %.2f%% of all)\n",
		"TOTAL(slfull)", tl, ti, 100*float64(ti)/float64(tl+ti), tcl, tci,
		float64(tcl)/float64(max(tl, 1)), float64(tci)/float64(max(ti, 1)),
		100*float64(tcl)/float64(total.total), 100*float64(tci)/float64(total.total))
	lr += fmt.Sprintf("%-22s %9d %9s %8s %10d %10s %9.0f            (%.2f%% of all)\n",
		"FAST gate (no attacked)", total.fastN, "-", "-", total.fastCyc, "-",
		float64(total.fastCyc)/float64(max(total.fastN, 1)),
		100*float64(total.fastCyc)/float64(total.total))
	t.Logf("\n%s", lr)
	// ---- evasion filter model -------------------------------------------
	names := [3]string{"filter REJECTS (skip make)", "filter passes (verify)", "model undecided"}
	em := "evasion-node pre-make filter model (non-king, non-ep moves at in-check nodes):\n"
	em += fmt.Sprintf("  %-28s %10s %10s %12s %12s\n", "verdict", "wasLegal", "wasIllegal",
		"cyc(legal)", "cyc(illegal)")
	for i, nm := range names {
		em += fmt.Sprintf("  %-28s %10d %10d %12d %12d  (%.2f%% of all cycles)\n",
			nm, total.evasion[i][0], total.evasion[i][1],
			total.evasionCyc[i][0], total.evasionCyc[i][1],
			100*float64(total.evasionCyc[i][0]+total.evasionCyc[i][1])/float64(total.total))
	}
	em += fmt.Sprintf("  ** evasion[REJECTS][wasLegal] MUST be 0 for soundness: %d\n",
		total.evasion[0][0])
	t.Logf("\n%s", em)

	pm := "pin-ray model (category 4: parent not in check, non-king, non-ep, FROM king-aligned):\n"
	pm += fmt.Sprintf("  %-26s %10s %10s %12s %12s\n", "ray test says", "wasLegal",
		"wasIllegal", "cyc(legal)", "cyc(illegal)")
	for i, nm := range [2]string{"LEGAL (skip attacked)", "ILLEGAL (skip make too)"} {
		pm += fmt.Sprintf("  %-26s %10d %10d %12d %12d  (%.2f%% of all cycles)\n",
			nm, total.pin[i][0], total.pin[i][1], total.pinCyc[i][0], total.pinCyc[i][1],
			100*float64(total.pinCyc[i][0]+total.pinCyc[i][1])/float64(total.total))
	}
	pm += fmt.Sprintf("  ** disagreements with truth (must both be 0): saysLegal/wasIllegal=%d saysIllegal/wasLegal=%d\n",
		total.pin[0][1], total.pin[1][0])
	em2 := fmt.Sprintf("  reject rule split: target-rule %d, double-check-rule %d (moves at double-check nodes: %d)\n",
		total.evRejTarget, total.evRejDouble, total.evDoubleMoves)
	t.Logf("\n%s", em2)
	pm += fmt.Sprintf("  ray walk: %d calls, %d BOARD probes, %.2f probes/call\n",
		total.pinN, total.pinStep, float64(total.pinStep)/float64(max(total.pinN, 1)))
	t.Logf("\n%s", pm)

	// ---- lazy-eval opportunity -----------------------------------------
	le := "lazy-eval opportunity (bail before pawntermfull on the fail-high side):\n"
	le += fmt.Sprintf("  %-4s %9s %9s %9s   %s\n", "site", "evals", "PSTRUCT-stale",
		"failhigh", "bailable-at-margin (stale only) | (all calls)")
	for _, k := range []string{"qs", "fw"} {
		st := total.ev[k]
		if st == nil {
			continue
		}
		row := fmt.Sprintf("  %-4s %9d %9d(%4.1f%%) %9d   ", k, st.n, st.dirty,
			100*float64(st.dirty)/float64(max(st.n, 1)), st.fh)
		for i, mg := range shEvMargins {
			row += fmt.Sprintf("m%d:%d(%.1f%%) ", mg, st.bail[i],
				100*float64(st.bail[i])/float64(max(st.dirty, 1)))
		}
		row += "| "
		for i, mg := range shEvMargins {
			row += fmt.Sprintf("m%d:%d ", mg, st.bailAny[i])
		}
		le += row + "\n"
	}
	t.Logf("\n%s", le)

	for _, ph := range phases {
		p := perPhase[ph]
		if p == nil {
			continue
		}
		row := fmt.Sprintf("  %-9s", ph)
		for _, k := range keys {
			v := p.legal[k]
			if v == nil {
				v = &[2]uint64{}
			}
			row += fmt.Sprintf("  %s=%d/%d", k[:1], v[0]+v[1], v[1])
		}
		t.Logf("%s   (reason=total/illegal)", row)
	}
}
