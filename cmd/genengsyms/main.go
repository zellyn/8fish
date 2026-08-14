// Command genengsyms regenerates asm/engsyms.inc from asm/engine.lbl — the
// symbol bridge that lets the on-device UI (asm/m8.s) call the engine by
// address without any engine source file being modified. All the work is in
// internal/engsyms, which internal/ui's tests call directly so the bridge is
// never a stale manual step.
package main

import (
	"fmt"
	"os"

	"github.com/zellyn/8fish/internal/engsyms"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	if err := engsyms.Generate(root); err != nil {
		fmt.Fprintln(os.Stderr, "genengsyms:", err)
		os.Exit(1)
	}
}
