package chesstest

// BankedClock implements chess-clock time management over per-move
// emulated-cycle budgets: unused cycles carry forward in a bank, and
// each move's spendable allocation is Base + bank/8, so the bank
// drains smoothly across several hard positions instead of feeding
// one greedy move.
//
// Total game time is CONSERVED. The engine's hard-abort ceiling can
// overspend a move's allocation on a thin/empty bank; rather than
// FORGIVE that excess (the old clamp-at-zero, which leaked ~13% of
// total compute), the bank goes NEGATIVE — a debt that subsequent
// moves' shrunken allocations repay, exactly like a chess clock. The
// per-game total then telescopes to sum(income) = N*Base. A minSpend
// floor (Base/4) keeps a deeply-negative bank from starving a move to
// zero; that floor is the only residual source of overspend. Only the
// POSITIVE side is capped (an underspender can't hoard unboundedly).
// This is the reference for the eventual 6502 driver port (a 24-bit
// SIGNED bank in zp; /8 is three shift chains).
type BankedClock struct {
	Base uint64 // per-move base allocation (income), cycles
	Cap  uint64 // positive bank ceiling; 0 = 8*Base
	bank int64  // signed carry: >0 saved, <0 debt (clawed-back overspend)
}

// minSpend is the per-move floor: a debt-laden bank never shrinks a
// move's allocation below Base/4.
func (c *BankedClock) minSpend() int64 { return int64(c.Base / 4) }

// Alloc returns the next move's spendable budget: income + bank/8. A
// negative (debt) bank shrinks the allocation so the overspend is
// repaid, but never below the minSpend floor and never negative.
func (c *BankedClock) Alloc() uint64 {
	a := int64(c.Base) + c.bank/8
	if lo := c.minSpend(); a < lo {
		a = lo
	}
	if a < 0 {
		a = 0
	}
	return uint64(a)
}

// Settle accounts a finished move: the bank gains Base (income) and
// loses what was actually spent. An overspend drives the bank NEGATIVE
// (debt) rather than clamping at zero, so total game compute is
// conserved. Only the positive side is capped.
func (c *BankedClock) Settle(spent uint64) {
	c.bank += int64(c.Base) - int64(spent)
	max := c.Cap
	if max == 0 {
		max = 8 * c.Base
	}
	if c.bank > int64(max) {
		c.bank = int64(max)
	}
}

// Bank exposes the current signed balance (diagnostics).
func (c *BankedClock) Bank() int64 { return c.bank }
