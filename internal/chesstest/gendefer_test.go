package chesstest

import (
	"fmt"
	"sort"
	"testing"
)

// TestGenDeferAsmCost (diagnostic, deferred-generation study): measures
// what the FULL-WIDTH move generator actually costs, separated from the
// quiescence copy.
//
// Why a bespoke instrument: movegenbody.inc is a macro body assembled
// TWICE (`.proc generate` for full-width, `.proc generateq` for QS), and
// the shared emitters (emitmove/emitmovep/emitmovef/promoloop/empage/
// flushpage) are called from both copies. A per-FILE rollup therefore
// reports one "movegenbody.inc" number that is the SUM of the two
// generators, which cannot decide whether deferring full-width
// generation is worth it.
//
// Method (exact, not sampled):
//   - The link map puts the three pieces in disjoint contiguous ranges:
//     emitters [emitmove, generate), the full-width body [generate,
//     generateq), and the QS body + genrecap [generateq, bookentry).
//   - A per-PC callback classifies each executed instruction by range.
//     Cycles in the SHARED emitter range are attributed to whichever
//     body was executing most recently ("mode"), which is exact: the
//     emitters are only ever reached by jsr from inside a body copy,
//     and neither copy calls across into the other.
//   - gennodef's own prologue and the shared gennd2 tail are attributed
//     the same way (mode is set at the gennodef/gennodeq entry labels).
//   - Call counts are exact instruction-execution counts at entry PCs.
//
// ---------------------------------------------------------------------
// COST SIDE (instruction-level accounting, not measured — no code was
// added to the engine). Deferring generation past the TT move means the
// move is no longer guaranteed to be in a generated list, so it must be
// validated as pseudo-legal in THIS position before make(). Today the
// eager generate() IS the validator: pass 0 simply fails to find a
// wrong-position TT move.
//
// Why validation is mandatory: asm/tt.s indexes with 12 bits (HASH0 +
// HASH1's low nibble) and verifies HASH1..3 (24 bits) — union = the FULL
// 32-bit Zobrist. A hit against an entry holding a DIFFERENT position
// therefore survives with probability 2^-20 (the fresh verify bits). At
// the measured ~800-1200 ttprobe calls per move at the shipped ~30s
// control, that is roughly one wrong-position TT move per ~2000 moves —
// order 1 per 25 games. Unvalidated, each one makes an illegal move and
// corrupts the board/piece lists irrecoverably.
//
// Proposed ttmovevalid (no illegal/undocumented opcodes; reuses the
// existing 0x88 tables and board.s' atgeo ray walk):
//
//	prologue: BOARD[FROM] nonzero and our color; BOARD[TO] empty or
//	  enemy .......................................... 32 (TO empty) /
//	                                                    38 (TO occupied)
//	type dispatch (MVPIECE & TYPEMASK) ............ 10 pawn / 13 other /
//	                                                             14 king
//	knight+slider: diff = TO-FROM+$77 -> ATTACKTAB, stage ATSQ/X/Y,
//	  jmp atgeo ............................................... 50
//	  atgeo knight (no ray) ................................... 46
//	  atgeo slider (setup 54 + 23/intermediate square + 11 + 8);
//	  measured mean ray length 2.18 => 1.18 intermediates ...... 100
//	pawn: color branch, d in {$10,$20,$0F,$11}, empty/enemy or EPSQ
//	  test, promotion-rank test, flags ......................... ~55
//	king (non-castle): ATTACKTAB & ATK_KING, flags ............. ~35
//	castling: REJECT (fall through to normal generation) — the two
//	  attacked() scans it needs cost ~656 cycles, and it is only
//	  2.3% of TT moves.
//
// Per-case totals: knight 144, slider ~199, pawn ~100, king ~84.
// Weighted by the mover mix measured over 432 shipped-control self-play
// moves (mirror TestGenDeferSelfPlay: pawn 19.0%, knight 10.7%, bishop
// 18.1%, rook 11.5%, queen 4.8%, king 35.9%) => ~133 cycles. Add ~25 for
// staging the TT move as a real 4-byte move-stack record at MSP (so
// setmove4 / the scout refetch / the killer flag read keep working
// unchanged) and slack for the extra PASSNO state: budget 150-250
// cycles per TT-available node.
// ---------------------------------------------------------------------
func TestGenDeferAsmCost(t *testing.T) {
	if testing.Short() {
		t.Skip("diagnostic: run explicitly")
	}
	bin := loadEngine(t)
	labels, err := ParseLabelFile("../../asm/engine.lbl")
	if err != nil {
		t.Fatal(err)
	}
	shipped := byte(defs["FT_CKEXT"]) | 0x1F

	need := []string{"emitmove", "generate", "generateq", "bookentry",
		"gennodef", "gennodeq", "gennd2", "curincheck", "search",
		"p0page", "p0done", "snode", "snodeq", "make", "attacked",
		"stop0", "scut"}
	L := map[string]uint16{}
	for _, n := range need {
		a, ok := labels[n]
		if !ok {
			t.Fatalf("label %q missing", n)
		}
		L[n] = a
	}

	type counters struct {
		total uint64

		genFull  uint64 // [generate, generateq)
		genQS    uint64 // [generateq, bookentry)  (incl. genrecap)
		emitFull uint64 // shared emitters, attributed to the full body
		emitQS   uint64 // shared emitters, attributed to the QS body
		wrapF    uint64 // gennodef prologue + its share of the gennd2 tail
		wrapQ    uint64 // gennodeq prologue + its share of the gennd2 tail
		p0scan   uint64 // [p0page, p0done): the pass-0 TT-move hunt
		attack   uint64 // attacked() and below, for the ttmovevalid estimate

		nGenFull uint64 // executions of the `generate` entry PC
		nGenQS   uint64 // executions of the `generateq` entry PC
		nSnode   uint64 // executions of `snode`
		nSnodeq  uint64 // executions of `snodeq`
		nSearch  uint64 // executions of the `search` entry PC
		nMake    uint64 // executions of `make`
		nAttack  uint64 // executions of `attacked`

		// Direct asm-side measurement of the OPPORTUNITY (the mirror
		// measures the same thing; this is the cross-check on the real
		// engine). stop0 is snode's "no TT move for this ply" exit, so
		// nSnode-nNoTT is the TT-available count. scut is the beta-cutoff
		// entry with Y = PLY, so PASSNO[PLY] there says which pass cut.
		nNoTT  uint64 // executions of `stop0`
		nCut   uint64 // executions of `scut`
		nCutP0 uint64 // ... at a full-width node while PASSNO == 0
	}

	run := func(fen string) *counters {
		p, err := ParseFEN(fen)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewMachine(bin, defs, p, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		SetFeatures(m, defs, shipped)
		SetBudget(m, defs, 30_000_000, 24)
		m.Mem.Main[defs["HALFMOVE"]] = p.Halfmove

		c := &counters{}
		plyAddr := defs["PLY"]
		passAddr := defs["PASSNO"]
		qsAddr := defs["QSKIND"]
		const (
			modeFull = iota
			modeQS
		)
		mode := modeFull
		exited, code, err := m.RunProfile(400_000_000_000, func(pc uint16, cyc uint8) {
			n := uint64(cyc)
			c.total += n
			switch {
			case pc >= L["generate"] && pc < L["generateq"]:
				mode = modeFull
				c.genFull += n
				if pc == L["generate"] {
					c.nGenFull++
				}
			case pc >= L["generateq"] && pc < L["bookentry"]:
				mode = modeQS
				c.genQS += n
				if pc == L["generateq"] {
					c.nGenQS++
				}
			case pc >= L["emitmove"] && pc < L["generate"]:
				if mode == modeQS {
					c.emitQS += n
				} else {
					c.emitFull += n
				}
			case pc >= L["gennodef"] && pc < L["gennd2"]:
				mode = modeFull
				c.wrapF += n
			case pc >= L["gennodeq"] && pc < L["gennodef"]:
				mode = modeQS
				c.wrapQ += n
			case pc >= L["gennd2"] && pc < L["curincheck"]:
				if mode == modeQS {
					c.wrapQ += n
				} else {
					c.wrapF += n
				}
			case pc >= L["p0page"] && pc < L["p0done"]:
				c.p0scan += n
			}
			if pc >= L["attacked"] && pc < L["make"] {
				c.attack += n
			}
			switch pc {
			case L["search"]:
				c.nSearch++
			case L["snode"]:
				c.nSnode++
			case L["snodeq"]:
				c.nSnodeq++
			case L["make"]:
				c.nMake++
			case L["attacked"]:
				c.nAttack++
			case L["stop0"]:
				c.nNoTT++
			case L["scut"]:
				c.nCut++
				ply := uint16(m.Mem.Main[plyAddr])
				if m.Mem.Main[qsAddr+ply] == 0 && m.Mem.Main[passAddr+ply] == 0 {
					c.nCutP0++
				}
			}
		})
		if err != nil || !exited || code > 2 {
			t.Fatalf("%s: exited=%v code=%d err=%v", fen[:20], exited, code, err)
		}
		return c
	}

	add := func(dst, src *counters) {
		dst.total += src.total
		dst.genFull += src.genFull
		dst.genQS += src.genQS
		dst.emitFull += src.emitFull
		dst.emitQS += src.emitQS
		dst.wrapF += src.wrapF
		dst.wrapQ += src.wrapQ
		dst.p0scan += src.p0scan
		dst.attack += src.attack
		dst.nGenFull += src.nGenFull
		dst.nGenQS += src.nGenQS
		dst.nSnode += src.nSnode
		dst.nSnodeq += src.nSnodeq
		dst.nSearch += src.nSearch
		dst.nMake += src.nMake
		dst.nAttack += src.nAttack
		dst.nNoTT += src.nNoTT
		dst.nCut += src.nCut
		dst.nCutP0 += src.nCutP0
	}

	agg := &counters{}
	perPhase := map[string]*counters{}
	for _, pos := range r5Positions {
		c := run(pos.fen)
		add(agg, c)
		if perPhase[pos.phase] == nil {
			perPhase[pos.phase] = &counters{}
		}
		add(perPhase[pos.phase], c)
	}

	report := func(name string, c *counters) string {
		fullTotal := c.genFull + c.emitFull + c.wrapF
		qsTotal := c.genQS + c.emitQS + c.wrapQ
		pct := func(v uint64) float64 { return 100 * float64(v) / float64(c.total) }
		per := func(v, n uint64) float64 {
			if n == 0 {
				return 0
			}
			return float64(v) / float64(n)
		}
		s := fmt.Sprintf("--- %s: %dM total cycles ---\n", name, c.total/1_000_000)
		s += fmt.Sprintf("  nodes: search=%d  full-width(snode)=%d  qs(snodeq)=%d  make=%d  attacked=%d\n",
			c.nSearch, c.nSnode, c.nSnodeq, c.nMake, c.nAttack)
		s += fmt.Sprintf("  calls: generate=%d  generateq(entry)=%d\n", c.nGenFull, c.nGenQS)
		s += fmt.Sprintf("  FULL-WIDTH generator (gennodef)   %10d cyc  %6.2f%% of total  %8.1f cyc/call\n",
			fullTotal, pct(fullTotal), per(fullTotal, c.nGenFull))
		s += fmt.Sprintf("      body [generate,generateq)     %10d cyc  %6.2f%%  %8.1f cyc/call\n",
			c.genFull, pct(c.genFull), per(c.genFull, c.nGenFull))
		s += fmt.Sprintf("      shared emitters               %10d cyc  %6.2f%%  %8.1f cyc/call\n",
			c.emitFull, pct(c.emitFull), per(c.emitFull, c.nGenFull))
		s += fmt.Sprintf("      gennodef wrap + gennd2        %10d cyc  %6.2f%%  %8.1f cyc/call\n",
			c.wrapF, pct(c.wrapF), per(c.wrapF, c.nGenFull))
		s += fmt.Sprintf("  QS generator (generateq+genrecap) %10d cyc  %6.2f%% of total  %8.1f cyc/qs-node\n",
			qsTotal, pct(qsTotal), per(qsTotal, c.nSnodeq))
		s += fmt.Sprintf("  BOTH generators combined          %10d cyc  %6.2f%% of total\n",
			fullTotal+qsTotal, pct(fullTotal+qsTotal))
		s += fmt.Sprintf("  pass-0 TT scan [p0page,p0done)    %10d cyc  %6.2f%% of total  %8.1f cyc/full-width-node\n",
			c.p0scan, pct(c.p0scan), per(c.p0scan, c.nGenFull))
		s += fmt.Sprintf("  attacked() subtree                %10d cyc  %6.2f%% of total  %8.1f cyc/call\n",
			c.attack, pct(c.attack), per(c.attack, c.nAttack))
		ttAvail := c.nSnode - c.nNoTT
		s += fmt.Sprintf("  OPPORTUNITY (asm-side): TT move available at %d/%d = %.1f%% of full-width nodes;\n"+
			"      pass-0 cutoffs %d = %.1f%% of TT-available = %.1f%% of ALL full-width nodes\n"+
			"      (all beta cutoffs: %d)\n",
			ttAvail, c.nSnode, 100*float64(ttAvail)/float64(max(c.nSnode, 1)),
			c.nCutP0, 100*float64(c.nCutP0)/float64(max(ttAvail, 1)),
			100*float64(c.nCutP0)/float64(max(c.nSnode, 1)), c.nCut)
		return s
	}

	out := report("AGGREGATE", agg)
	phases := make([]string, 0, len(perPhase))
	for ph := range perPhase {
		phases = append(phases, ph)
	}
	sort.Strings(phases)
	for _, ph := range phases {
		out += report(ph, perPhase[ph])
	}
	t.Logf("\n%s", out)
}
