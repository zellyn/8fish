// Command mkdsk builds asm/8fish.dsk: a bootable 5.25" Apple II disk carrying
// the copier, the UI payload, the engine, the DHGR artwork and the resident
// opening book, delivered in TWO STAGES by peterferrie's "Standard Delivery"
// boot loader -- the second one read by re-entering the loader that is still
// sitting in $0800-$08FF after the first.
//
// All the work is in internal/delivery, whose package doc is the account of
// how that works and why it has to; internal/ui's tests call it directly, so
// the ledger is gated rather than merely printed here.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zellyn/8fish/internal/delivery"
)

func main() {
	root := flag.String("root", ".", "repo root")
	out := flag.String("o", "", "output .dsk (default <root>/asm/8fish.dsk)")
	img := flag.String("img", "", "where to write the contiguous memory image (default <root>/asm/8fish.img)")
	flag.Parse()

	dskPath := *out
	if dskPath == "" {
		dskPath = filepath.Join(*root, "asm", "8fish.dsk")
	}
	imgPath := *img
	if imgPath == "" {
		imgPath = filepath.Join(*root, "asm", "8fish.img")
	}

	l, err := delivery.Build(*root, imgPath, dskPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdsk:", err)
		os.Exit(1)
	}
	fmt.Printf("%s: %s\n", dskPath, l)
}
