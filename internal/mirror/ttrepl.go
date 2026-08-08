package mirror

// TT replacement policy (depth-preferred + age bit), screened for the
// modern-technique adaptation round. The shipped TT always-replaces; with
// DepthPref a store into a slot holding a DEEPER entry for a DIFFERENT
// position from the CURRENT root move's generation is skipped, so deep
// results survive shallow traffic within a move. An age bit (the unused
// bit 7 of the packed depth/bound byte) is flipped every root move, so
// stale deep entries from earlier moves never block current stores.
//
// 6502 shape: the store path already reads the slot's depthBound for free
// (same page as the verify bytes); the policy is one EOR/CMP pair plus a
// branch, ~15 cycles per store (Costs.TTRepl), and one EOR #$80 per root
// move for the age flip. No new memory: the age bit lives in the entry.
//
// The zero value (DepthPref == false) is a byte-identical no-op — no age
// bits are ever written, and ttprobe's depth mask (&31) reads identically
// either way because depth is clamped to 31 on store.
type TTReplParams struct {
	DepthPref bool
}

func (t *TTReplParams) on() bool { return t.DepthPref }

// ttKeepOld reports whether the depth-preferred policy keeps the existing
// entry instead of storing the new one (depth is the new entry's depth).
// Same-position stores always replace (fresher bounds win).
func (e *Engine) ttKeepOld(old *ttEntry, verify uint32, depth int) bool {
	if e.cyc {
		e.Cyc.Est += uint64(e.Costs.TTRepl)
	}
	if old.depthBound&3 == 0 || old.verify == verify {
		return false // empty slot or same position: replace
	}
	if old.depthBound&0x80 != e.ttAge {
		return false // stale generation: replace
	}
	if int(old.depthBound>>2)&31 > depth {
		if e.cyc {
			e.Cyc.TTReplSkips++
		}
		return true // current-age deeper entry for another position: keep
	}
	return false
}
