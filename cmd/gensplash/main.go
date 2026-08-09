// Command gensplash compresses the hand-drawn boot splash
// assets/fish8-splash-dazzledraw-save.bin (a 16 KB double-hi-res screen, 8 KB
// aux then 8 KB main) into internal/splash/splashblob.bin: the magic-prefixed
// per-bank PackBits blob asm/m8.s decodes straight into DHGR page 1 at boot,
// and internal/delivery lays out as the disk's SPLASH file.
//
// The asset is the SINGLE source of truth. gensplash round-trips its own
// output (Decode(Encode(raw)) == raw) and refuses to write a blob that would
// not fit the on-disk SPLASH file (splash.DiskBytes), so a splash that grew
// past its block budget fails HERE rather than shipping a truncated logo.
//
// Run: go run ./cmd/gensplash   (from the repo root)
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/zellyn/chess6502/internal/splash"
)

const (
	assetPath = "assets/fish8-splash-dazzledraw-save.bin"
	blobPath  = "internal/splash/splashblob.bin"
)

func main() {
	raw, err := os.ReadFile(assetPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gensplash:", err)
		os.Exit(1)
	}
	if len(raw) != splash.RawBytes {
		fmt.Fprintf(os.Stderr, "gensplash: %s is %d B, want %d (8 KB aux + 8 KB main)\n",
			assetPath, len(raw), splash.RawBytes)
		os.Exit(1)
	}

	blob := splash.Encode(raw)

	// Round-trip sanity: the committed blob must decode back to the asset.
	back, err := splash.Decode(blob)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gensplash: blob failed to decode:", err)
		os.Exit(1)
	}
	if !bytes.Equal(back, raw) {
		fmt.Fprintln(os.Stderr, "gensplash: round-trip mismatch: Decode(Encode(raw)) != raw")
		os.Exit(1)
	}

	if len(blob) > splash.DiskBytes {
		fmt.Fprintf(os.Stderr, "gensplash: the compressed blob is %d B, %d MORE than the "+
			"%d-byte SPLASH file (%d blocks). The logo does not compress small enough; "+
			"simplify the artwork or grow the file.\n",
			len(blob), len(blob)-splash.DiskBytes, splash.DiskBytes, splash.DiskBytes/512)
		os.Exit(1)
	}

	if err := os.WriteFile(blobPath, blob, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gensplash:", err)
		os.Exit(1)
	}

	fmt.Printf("gensplash: %s (%d B) -> %s\n", assetPath, len(raw), blobPath)
	fmt.Printf("  compressed %d B -> %d B (%.1f%% of raw), fits the %d-byte SPLASH file "+
		"(%d blocks, %d B padding)\n",
		splash.RawBytes, len(blob), 100*float64(len(blob))/float64(splash.RawBytes),
		splash.DiskBytes, splash.DiskBytes/512, splash.DiskBytes-len(blob))
}
