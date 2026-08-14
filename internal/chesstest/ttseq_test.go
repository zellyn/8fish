package chesstest

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/mirror"
)

// ---------------------------------------------------------------------------
// TT OPERATION-SEQUENCE parity.
//
// The node/make/eval counters that TestIDIterationParity compares are an
// aggregate fingerprint: they prove the two engines walked the same tree, but
// they say nothing DIRECTLY about the transposition table, which is the state
// that carries between ID iterations. This file compares the TT operation
// SEQUENCE itself — every probe (hit or miss) and every store, in order, with
// its hash, ply, move, ply-adjusted score, packed depth and bound — so a
// disagreement names the exact operation, not just a count.
//
// asm side: probes at tt.s' `ttprobe` (attempt), `tthit` (verified hit, after
// the mate-zone adjustment), `ttfmiss` (empty entry or key mismatch), and
// `tsgo` (a store that actually happens: past the ABORT bail, with the packed
// depth|bound byte and the node-relative score already computed).
// mirror side: mirror.Engine.TTHook.
//
// This is the instrument the tt.s unsigned-mate-zone bug (54% of stores
// corrupted, invisible to every gate until it was found by accident) would
// have tripped on its first store.
// ---------------------------------------------------------------------------

// ttOp is one TT operation on either side, in a comparable form.
type ttOp struct {
	op         byte // mirror.TTProbeMiss / TTProbeHit / TTStore
	hash       uint32
	ply        int
	from, to   byte
	score      int16
	depthBound byte
}

func (o ttOp) String() string {
	name := [...]string{"miss ", "hit  ", "store"}[o.op]
	s := fmt.Sprintf("%s hash=%08x ply=%d", name, o.hash, o.ply)
	if o.op != mirror.TTProbeMiss {
		s += fmt.Sprintf(" from=%02x to=%02x score=%d depth=%d bound=%d",
			o.from, o.to, o.score, o.depthBound>>2, o.depthBound&3)
	}
	return s
}

// asmTTSequence runs the asm engine in ID mode and records every TT operation.
func asmTTSequence(bin []byte, labels map[string]uint16, fen string, cap byte,
	features byte, limit int) ([]ttOp, error) {

	pos, err := ParseFEN(fen)
	if err != nil {
		return nil, err
	}
	m, err := NewMachine(bin, defs, pos, 0, nil)
	if err != nil {
		return nil, err
	}
	SetFeatures(m, defs, features)
	SetFeatures2(m, defs, 0)
	SetBudget(m, defs, idHugeBudget, cap)
	m.Mem.Main[defs["HALFMOVE"]] = pos.Halfmove

	hash0 := defs["HASH0"]
	plyA := defs["PLY"]
	ttentry := defs["TTENTRY"]
	tthit, ttfmiss, tsgo := labels["tthit"], labels["ttfmiss"], labels["tsgo"]
	if tthit == 0 || ttfmiss == 0 || tsgo == 0 {
		return nil, fmt.Errorf("tthit/ttfmiss/tsgo labels missing")
	}
	var ops []ttOp
	readHash := func() uint32 {
		return uint32(m.Mem.Main[hash0]) | uint32(m.Mem.Main[hash0+1])<<8 |
			uint32(m.Mem.Main[hash0+2])<<16 | uint32(m.Mem.Main[hash0+3])<<24
	}
	// The entry staging area is only meaningful for hits and stores.
	record := func(op byte) {
		if len(ops) >= limit {
			return
		}
		o := ttOp{op: op, hash: readHash(), ply: int(m.Mem.Main[plyA])}
		if op != mirror.TTProbeMiss {
			o.from = m.Mem.Main[ttentry+3]
			o.to = m.Mem.Main[ttentry+4]
			o.score = int16(uint16(m.Mem.Main[ttentry+5]) | uint16(m.Mem.Main[ttentry+6])<<8)
			o.depthBound = m.Mem.Main[ttentry+7]
		}
		ops = append(ops, o)
	}
	exited, _, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
		switch pc {
		case tthit:
			record(mirror.TTProbeHit)
		case ttfmiss:
			record(mirror.TTProbeMiss)
		case tsgo:
			record(mirror.TTStore)
		}
	})
	if err != nil {
		return ops, err
	}
	if !exited {
		return ops, fmt.Errorf("engine did not exit")
	}
	return ops, nil
}

// mirrorTTSequence is the mirror-side twin.
func mirrorTTSequence(cfg parityConfig, fen string, cap int, limit int) ([]ttOp, error) {
	mp, err := mirror.ParseFEN(fen)
	if err != nil {
		return nil, err
	}
	me := cfg.mirrorEngine()
	me.CycleTrack = false
	me.SetPosition(mp)
	var ops []ttOp
	me.TTHook = func(o mirror.TTOp) {
		if len(ops) >= limit {
			return
		}
		ops = append(ops, ttOp{op: o.Op, hash: o.Hash, ply: o.Ply, from: o.From,
			to: o.To, score: o.Score, depthBound: o.DepthBound})
	}
	me.SearchCycleBudget(idHugeBudget, cap)
	return ops, nil
}

// ttSeqFENs is the sequence gate's position set. It is deliberately small (the
// trace is per-operation, so the runs must stay short) but covers the shapes
// that stress the TT hardest: a transposition-rich middlegame, a pawn endgame,
// and two near-mate positions where MATE SCORES are written to and read back
// from the table at many different plies — the exact traffic the mate-zone
// adjustment (tt.s tsadj/ttadj) handles.
var ttSeqFENs = []string{
	"r1b1k2r/ppp2ppp/2nqpn2/3p4/3P4/P1P1BN2/2P1PPPP/2RQKB1R w Kkq - 2 8",
	"8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1",
	"5rk1/5ppp/8/8/8/8/5PPP/4R1K1 w - - 0 1",
	"8/8/4k3/8/8/3K4/8/3R4 w - - 0 1",
	"4k3/8/4K3/8/8/8/4P3/8 w - - 0 1",
	"1k1r4/pp1b1R2/3q2pp/4p3/2B5/4Q3/PPP2B2/2K5 b - - 0 1",
}

// TestTTSequenceParity requires the asm and the mirror to perform the SAME
// transposition-table operations, in the same order, with the same operands,
// through a real multi-iteration ID search.
func TestTTSequenceParity(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: traced 6502 searches")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"HASH0", "PLY", "TTENTRY"} {
		if defs[s] == 0 {
			t.Fatalf("defs symbol %q missing", s)
		}
	}
	const limit = 4_000_000
	depth := envInt("TTSEQ_DEPTH", 5)
	fens := ttSeqFENs
	if f := os.Getenv("TTSEQ_FEN"); f != "" {
		fens = []string{f}
	}
	cfgs := idParityConfigs
	if s := os.Getenv("TTSEQ_CFG"); s != "" {
		var sel []parityConfig
		for _, c := range idParityConfigs {
			if strings.Contains(c.name, s) {
				sel = append(sel, c)
			}
		}
		cfgs = sel
	}
	var totalOps, stores, hits, rootHits int
	for _, cfg := range cfgs {
		for _, fen := range fens {
			a, err := asmTTSequence(bin, labels, fen, byte(depth), cfg.features, limit)
			if err != nil {
				t.Fatalf("asm %q: %v", fen, err)
			}
			m, err := mirrorTTSequence(cfg, fen, depth, limit)
			if err != nil {
				t.Fatalf("mirror %q: %v", fen, err)
			}
			totalOps += len(a)
			for _, o := range a {
				switch o.op {
				case mirror.TTStore:
					stores++
				case mirror.TTProbeHit:
					hits++
					if o.ply == 0 {
						// A hit at the ROOT can only come from an EARLIER ID
						// iteration: within one iteration the root is visited
						// once, against an empty (or foreign) entry. It is the
						// proof that cross-iteration TT reuse is exercised.
						rootHits++
					}
				}
			}
			n := min(len(a), len(m))
			bad := -1
			for i := range n {
				if a[i] != m[i] {
					bad = i
					break
				}
			}
			if bad < 0 && len(a) != len(m) {
				bad = n
			}
			if bad >= 0 {
				var ctx strings.Builder
				lo := max(bad-6, 0)
				for i := lo; i < bad; i++ {
					fmt.Fprintf(&ctx, "      [%d] both: %s\n", i, a[i])
				}
				got, want := "<end of sequence>", "<end of sequence>"
				if bad < len(a) {
					got = a[bad].String()
				}
				if bad < len(m) {
					want = m[bad].String()
				}
				t.Errorf("TT SEQUENCE DIVERGENCE [%s] %s\n"+
					"    first disagreeing operation: #%d of %d (asm) / %d (mirror)\n%s"+
					"      asm    %s\n      mirror %s",
					cfg.name, fen, bad, len(a), len(m), ctx.String(), got, want)
			}
		}
	}
	t.Logf("TT sequence parity: %d operations compared (%d stores, %d probe hits, "+
		"%d of them at the ROOT = cross-iteration reuse) over %d configs x %d positions at ID depth %d",
		totalOps, stores, hits, rootHits, len(cfgs), len(fens), depth)
	if stores == 0 || hits == 0 {
		t.Errorf("traced %d stores and %d hits: the trace is not exercising the TT", stores, hits)
	}
	if rootHits == 0 {
		t.Error("no ROOT (ply 0) TT hit was traced: nothing carried an entry from one ID " +
			"iteration into the next, so this run proves nothing the fixed-depth gate does not")
	}
}
