// Command pullart extracts a single BIN file from a ProDOS-order 800K disk
// image (.po) and writes its raw contents to a file. It exists because the
// DazzleDraw art disk is a plain 800K ProDOS volume that `diskii` declines to
// open ("can only open disk-sized images and .hdv"), and the board art (CHESS2)
// is authored on that disk. This reads the volume directory, finds the named
// entry, walks its index block, and writes EOF bytes — enough for a seedling or
// sapling BIN, which is all a 16 KB A2FC screen is.
//
// Usage:
//
//	go run ./cmd/pullart -po <disk.po> -name CHESS2 -out assets/chess2-dazzledraw-save.bin
//
// It is deliberately narrow: 512-byte blocks, storage type 1 (seedling) or 2
// (sapling), no tree files, no subdirectories. The art files are 16 KB, which
// is 32 blocks — a sapling with one index block, exactly what this handles.
package main

import (
	"flag"
	"fmt"
	"os"
)

const blockSize = 512

func block(d []byte, n int) []byte { return d[n*blockSize : (n+1)*blockSize] }

func main() {
	po := flag.String("po", "", "ProDOS .po disk image")
	name := flag.String("name", "", "file name to extract (e.g. CHESS2)")
	out := flag.String("out", "", "output path")
	flag.Parse()
	if *po == "" || *name == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: pullart -po <disk.po> -name CHESS2 -out <path>")
		os.Exit(2)
	}
	d, err := os.ReadFile(*po)
	if err != nil {
		fail(err)
	}
	if len(d) < 3*blockSize || len(d)%blockSize != 0 {
		fail(fmt.Errorf("%s is %d bytes: not a block-aligned disk image", *po, len(d)))
	}

	data, err := extract(d, *name)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("pullart: %s (%d B) from %s -> %s\n", *name, len(data), *po, *out)
}

// extract walks the volume directory (a linked list of blocks starting at
// block 2) for a file entry named want, then reads its contents.
func extract(d []byte, want string) ([]byte, error) {
	n := 2
	for n != 0 {
		b := block(d, n)
		next := int(b[2]) | int(b[3])<<8
		// 13 entries of 39 bytes each, after the 4-byte prev/next header.
		for i := 0; i < 13; i++ {
			off := 4 + i*39
			if off+39 > blockSize {
				break
			}
			e := b[off : off+39]
			st, nameLen := e[0]>>4, int(e[0]&0x0F)
			// storage type 1 (seedling) or 2 (sapling); skip dir headers
			// (13/14/15), deleted (0) and empty (nameLen 0) entries.
			if (st != 1 && st != 2) || nameLen == 0 {
				continue
			}
			if string(e[1:1+nameLen]) != want {
				continue
			}
			key := int(e[0x11]) | int(e[0x12])<<8
			eof := int(e[0x15]) | int(e[0x16])<<8 | int(e[0x17])<<16
			return readFile(d, st, key, eof)
		}
		n = next
	}
	return nil, fmt.Errorf("pullart: %q not found in the volume directory", want)
}

// readFile returns the first eof bytes of a seedling (one data block) or
// sapling (an index block of up-to-256 block pointers) file.
func readFile(d []byte, st byte, key, eof int) ([]byte, error) {
	if st == 1 { // seedling: key IS the single data block
		return append([]byte(nil), block(d, key)[:eof]...), nil
	}
	// sapling: key is an index block, low bytes then high bytes.
	idx := block(d, key)
	nblk := (eof + blockSize - 1) / blockSize
	if nblk > 256 {
		return nil, fmt.Errorf("pullart: %d blocks exceeds a sapling's 256", nblk)
	}
	out := make([]byte, 0, nblk*blockSize)
	for i := 0; i < nblk; i++ {
		ptr := int(idx[i]) | int(idx[256+i])<<8
		out = append(out, block(d, ptr)...)
	}
	return out[:eof], nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "pullart:", err)
	os.Exit(1)
}
