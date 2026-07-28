// Command mkdsk builds asm/8fish.dsk: a bootable 5.25" Apple II disk carrying
// the copier, the UI payload, the resident opening book and the engine, loaded
// as one contiguous image by peterferrie's "Standard Delivery" boot loader.
// All the work is in internal/delivery, which internal/ui's tests call
// directly so the margins are gated rather than merely printed here.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zellyn/chess6502/internal/delivery"
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
