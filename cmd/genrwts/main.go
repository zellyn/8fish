// Command genrwts builds the resident ProRWTS2 read-only floppy driver blob
// from the vendored upstream source (docs/prorwts2-design.md §6).
//
// Inputs:
//
//	asm/third_party/PRORWTS2.S — peterferrie's ProRWTS2, BSD-3-Clause,
//	vendored PRISTINE from github.com/peterferrie/prorwts2 at commit
//	56f76d4045ad4c0d7daefa853899535f829bb87a (2022-10-07), CRLF normalized
//	to LF. The sha256 is pinned below: upstream drift (or a local edit of
//	the vendored file) fails this build loudly instead of shipping a driver
//	nobody re-measured.
//
// What it does:
//
//  1. applies the 8fish CONFIGURATION to the option block (the same
//     `name = value` lines upstream documents as user-defined options), plus
//     ONE structural patch: `dirbuf` — the driver's 512-byte directory
//     buffer — is moved to main-RAM op-time scratch ($1300, inside
//     MOVESTACK, dead outside a search) instead of the in-bank default that
//     would collide with the UI at $E000. Upstream explicitly supports
//     moving these buffers ("there are also buffers that can be moved").
//  2. assembles it with ACME (the binary a2audit vendors; ACME env var or
//     -acme overrides).
//  3. extracts the relocated FLOPPY driver image (the bytes ProRWTS2's own
//     init would have copied to `reloc`), PREBUILDS the 6-and-2 denibble
//     table that init builds at run time (we never run init: it needs a
//     live ProDOS), and BAKES the book region's directory block number
//     (rwts.DirBlock) into the `unrblocklo/hi` operands.
//  4. emits the three generated artifacts, all committed:
//     internal/rwts/rwtsblob.bin — the blob stage 2 delivers
//     internal/rwts/gen.go       — addresses/sizes for the Go gates
//     asm/rwts.inc               — the same numbers for asm/m8.s, plus the
//     slot-poke site table as .lobytes/.hibytes
//     macros (see m8rwtsinit)
//
// Run: go run ./cmd/genrwts   (from the repo root)
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/zellyn/chess6502/internal/rwts"
)

const (
	srcPath = "asm/third_party/PRORWTS2.S"
	// srcSHA256 pins the vendored source. If you update the vendor copy,
	// re-measure everything in docs/prorwts2-design.md before bumping this.
	srcSHA256 = "f9deea6ae5dc659fba01a5d45cce1f78304bc10e26e922dbf83b8870eec93487"
	// upstreamCommit is where the vendored copy came from.
	upstreamCommit = "56f76d4045ad4c0d7daefa853899535f829bb87a"

	// reloc is the driver's resident address: Language Card bank 1's free
	// tail (the resting bank — no bank switch to call it). Keep in sync with
	// the region list in internal/delivery's TestLanguageCardBank1Layout.
	reloc = 0xDC00
	// dirbufAddr is main-RAM op-time scratch: inside MOVESTACK ($0E00-$1FFF),
	// which is rebuilt by every search and dead between them. Loads run only
	// from command dispatch, never during a search or ponder.
	dirbufAddr = 0x1300
)

// config is the 8fish option set for the RESIDENT READ-ONLY build
// (docs/prorwts2-design.md §2.1). aligned_read=1 (all our reads are block
// multiples) eliminates encbuf; might_exist/poll_drive/check_chksum buy
// error REPORTING (status) instead of a hang or silent garbage when the
// drive is empty or the disk swapped; use_smartport=0 because there is no
// SmartPort init path to keep (init is never run).
var config = map[string]int{
	"verbose_info":  1,
	"enable_floppy": 1,
	"use_smartport": 0,
	"override_adr":  1,
	"aligned_read":  1,
	"enable_write":  0,
	"check_chksum":  1,
	"might_exist":   1,
	"poll_drive":    1,
	"allow_saplings": 1,
	"load_banked":   1,
	"lc_bank":       1,
}

// slotSiteNames are the self-modifying soft-switch operands inside the
// relocated FLOPPY driver whose low byte must gain the boot slot (slot<<4,
// the boot ROM's $2B). ProRWTS2's init pokes these; our m8rwtsinit pokes the
// same list at boot. The set is a property of the configuration, so it is
// asserted: a site appearing or vanishing after a vendor update fails here.
var slotSiteNames = []string{
	"unrseek",    // lda PHASEOFF,x  (seek phase select)
	"unrdrvoff3", // lda MOTOROFF    (miss/no-disk exit)
	"unrdrvoff4", // lda MOTOROFF    (read-done exit)
	"unrdrvon2",  // sta MOTORON     (prepdrive)
	"unrread1",   // lda Q6L         (readnib)
	"unrread2",   // lda Q6L         (poll)
	"unrread3",   // eor Q6L         (poll)
	"unrread4",   // ldx Q6L         (data prologue)
	"unrread5",   // ldx Q6L         (data body)
	"unrread6",   // ldx Q6L         (data checksum)
}

func main() {
	src, err := os.ReadFile(srcPath)
	check(err)
	if got := sha256.Sum256(src); hex.EncodeToString(got[:]) != srcSHA256 {
		fatal("genrwts: %s sha256 = %s, want %s\n"+
			"The vendored ProRWTS2 source changed (upstream commit %s). Re-verify the\n"+
			"configuration and measurements in docs/prorwts2-design.md, then update the pin.",
			srcPath, hex.EncodeToString(got[:]), srcSHA256, upstreamCommit)
	}

	patched := configure(string(src))

	tmp, err := os.MkdirTemp("", "genrwts")
	check(err)
	defer os.RemoveAll(tmp)
	asmPath := filepath.Join(tmp, "prorwts2-8fish.s")
	check(os.WriteFile(asmPath, []byte(patched), 0o644))

	acme := findACME()
	cmd := exec.Command(acme, "-l", "sym.txt", "prorwts2-8fish.s")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err != nil {
		fatal("genrwts: %s failed: %v\n%s", acme, err, out)
	}

	syms := parseSyms(filepath.Join(tmp, "sym.txt"))
	need := func(n string) int {
		v, ok := syms[n]
		if !ok {
			fatal("genrwts: symbol %q missing from the ACME symbol list", n)
		}
		return v
	}

	unreloc := need("unrelocdsk") // file-space base of the floppy driver image
	codeEnd := need("codeend")    // reloc-space, exclusive
	nibtbl := need("nibtbl")
	dataEnd := need("dataend")
	trackd1 := need("trackd1")
	entry := need("opendir")
	if entry != reloc {
		fatal("genrwts: opendir = $%04X, want the blob entry at $%04X (did rdwrpart grow a jmp?)", entry, reloc)
	}
	if got := need("dirbuf"); got != dirbufAddr {
		fatal("genrwts: dirbuf = $%04X, want $%04X (the dirbuf patch did not take)", got, dirbufAddr)
	}
	if dataEnd > 0xDFC0 {
		fatal("genrwts: driver data ends at $%04X, past the $DFC0 budget in LC bank 1", dataEnd)
	}

	// The assembled output file is a plain image of $0800..: the init code,
	// then the unrelocated driver images. The floppy driver's bytes are at
	// file offset (unrelocdsk - $0800), codeend-reloc long.
	obj, err := os.ReadFile(filepath.Join(tmp, "prorwts2#060800"))
	check(err)
	codeLen := codeEnd - reloc
	off := unreloc - 0x800
	if off < 0 || off+codeLen > len(obj) {
		fatal("genrwts: driver image [$%X..+%d] outside the %d-byte ACME output", off, codeLen, len(obj))
	}

	blob := make([]byte, dataEnd-reloc)
	copy(blob, obj[off:off+codeLen])

	// Prebuild the 6-and-2 denibble table exactly as init's loop would:
	//   ldx #$16 / -- stx zp / txa / asl / bit zp / beq + / ora zp /
	//   eor #$ff / and #$7e / - bcs + / lsr / bne - / tya /
	//   sta nibtbl-$16,x / iny / + inx / bpl --
	y := 0
	for x := 0x16; x <= 0x7F; x++ {
		a := byte(x << 1)
		if a&byte(x) == 0 {
			continue
		}
		a |= byte(x)
		a ^= 0xFF
		a &= 0x7E
		carry := false
		valid := false
		for {
			if carry {
				break
			}
			carry = a&1 == 1
			a >>= 1
			if a == 0 {
				valid = true
				break
			}
		}
		if valid {
			blob[nibtbl-reloc+(x-0x16)] = byte(y)
			y++
		}
	}
	if y != 64 {
		fatal("genrwts: denibble table has %d valid nibbles, want 64", y)
	}

	// Bake the book region's directory key block into the driver: init would
	// have poked unrblocklo/hi from a ProDOS prefix walk; our disk layout is
	// fixed at build time, so the operands are data.
	for _, b := range []struct {
		sym string
		val byte
	}{
		{"unrblocklo", byte(rwts.DirBlock)},
		{"unrblockhi", byte(rwts.DirBlock >> 8)},
	} {
		site := need(b.sym) - unreloc // blob offset of the instruction
		blob[site+1] = b.val
	}

	// The slot-poke sites, as reloc-space OPERAND addresses (site+1).
	var sites []int
	for _, n := range slotSiteNames {
		sites = append(sites, need(n)-unreloc+reloc+1)
	}
	sort.Ints(sites)
	for _, s := range sites {
		lo := blob[s-reloc]
		if lo != 0x80 && lo != 0x88 && lo != 0x89 && lo != 0x8C {
			fatal("genrwts: slot site $%04X operand is $%02X, not a slot-0 Disk II "+
				"soft switch low byte — the site list is stale", s, lo)
		}
	}
	// APPEND the site table to the blob itself (lo bytes, then hi bytes):
	// m8rwtsinit reads it out of Language Card bank 1 — the resting bank —
	// so the table costs the tight UICODE budget nothing.
	slotLo := reloc + len(blob)
	for _, s := range sites {
		blob = append(blob, byte(s))
	}
	slotHi := reloc + len(blob)
	for _, s := range sites {
		blob = append(blob, byte(s>>8))
	}

	check(os.WriteFile("internal/rwts/rwtsblob.bin", blob, 0o644))
	check(os.WriteFile("internal/rwts/gen.go", []byte(genGo(codeLen, len(blob), trackd1, slotLo, slotHi, sites)), 0o644))
	check(os.WriteFile("asm/rwts.inc", []byte(genInc(codeLen, len(blob), trackd1, slotLo, slotHi, sites)), 0o644))

	fmt.Printf("genrwts: ProRWTS2 read-only floppy driver (ACME %s)\n", acme)
	fmt.Printf("  code $%04X-$%04X (%d B), data to $%04X; blob %d B -> internal/rwts/rwtsblob.bin\n",
		reloc, codeEnd-1, codeLen, dataEnd-1, len(blob))
	fmt.Printf("  entry $%04X, trackd1 operand $%04X, dir key block %d, %d slot-poke sites\n",
		reloc, trackd1, rwts.DirBlock, len(sites))
}

// configure applies the option block edits and the dirbuf relocation patch.
// Every edit must match EXACTLY ONCE, so a vendor update that moves anything
// fails here rather than assembling a different driver.
func configure(src string) string {
	for k, v := range config {
		pat := regexp.MustCompile(`(?m)^(\s*` + k + `\s*=\s*)\d+`)
		n := len(pat.FindAllString(src, -1))
		if n != 1 {
			fatal("genrwts: option %q matched %d times in %s, want 1", k, n, srcPath)
		}
		src = pat.ReplaceAllString(src, "${1}"+strconv.Itoa(v))
	}
	// reloc for the banked, load_high=0 floppy build.
	src = replaceOnce(src, "reloc     = $d000", fmt.Sprintf("reloc     = $%04x", reloc))
	// dirbuf: the reloc>=$C000, load_high=0 floppy branch computes dirbuf just
	// above the in-bank data, which for us is the UI's own $E000. Pin it to
	// main-RAM op-time scratch instead (upstream supports moving it).
	src = replaceOnce(src,
		`      } else { ;load_high = 0
        !pseudopc ((dataend + $ff) & -256) {
          dirbuf = *
        }
        !if ((aligned_read xor 1) + enable_write) > 0 {
          encbuf = dirbuf + $200
        } ;aligned_read = 0 or enable_write = 1`,
		fmt.Sprintf(`      } else { ;load_high = 0
        dirbuf = $%04x
        !if ((aligned_read xor 1) + enable_write) > 0 {
          encbuf = dirbuf + $200
        } ;aligned_read = 0 or enable_write = 1`, dirbufAddr))
	return src
}

func replaceOnce(src, old, new string) string {
	if n := strings.Count(src, old); n != 1 {
		fatal("genrwts: patch anchor matched %d times, want 1:\n%s", n, old)
	}
	return strings.Replace(src, old, new, 1)
}

var symRe = regexp.MustCompile(`(?m)^\s*(\S+)\s*=\s*\$([0-9a-fA-F]+)`)

func parseSyms(path string) map[string]int {
	b, err := os.ReadFile(path)
	check(err)
	out := map[string]int{}
	for _, m := range symRe.FindAllStringSubmatch(string(b), -1) {
		v, err := strconv.ParseInt(m[2], 16, 32)
		if err == nil {
			out[m[1]] = int(v)
		}
	}
	return out
}

func findACME() string {
	if p := os.Getenv("ACME"); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, "gh", "a2audit", "bin", "acme")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "acme"
}

func genGo(codeLen, blobLen, trackd1, slotLo, slotHi int, sites []int) string {
	var b strings.Builder
	b.WriteString("// Code generated by cmd/genrwts. DO NOT EDIT.\n\npackage rwts\n\n")
	b.WriteString("// Addresses are Language Card bank 1 run-time addresses.\nconst (\n")
	fmt.Fprintf(&b, "\tEntryAddr = 0x%04X // opendir: the read-a-file entry point\n", reloc)
	fmt.Fprintf(&b, "\tCodeLen   = %d // driver code bytes\n", codeLen)
	fmt.Fprintf(&b, "\tBlobLen   = %d // code + denibble tables + site table ($%04X-$%04X)\n", blobLen, reloc, reloc+blobLen-1)
	fmt.Fprintf(&b, "\tTrackD1   = 0x%04X // self-modifying current-track operand (seed with $41 at boot)\n", trackd1)
	fmt.Fprintf(&b, "\tSlotLoAddr = 0x%04X // the site table inside the blob: operand lo bytes...\n", slotLo)
	fmt.Fprintf(&b, "\tSlotHiAddr = 0x%04X // ...and hi bytes (read by m8rwtsinit from bank 1)\n", slotHi)
	fmt.Fprintf(&b, "\tDirBufAddr = 0x%04X // 512-byte directory buffer, main-RAM op-time scratch\n", dirbufAddr)
	b.WriteString("\n\t// The driver's zero-page window (saved/swapped around every call\n")
	b.WriteString("\t// by asm/m8.s rwtszp), and its API bytes inside it.\n")
	b.WriteString("\tZPLo    = 0x3C\n\tZPLen   = 0x2C // $3C-$67\n")
	b.WriteString("\tZPStatus = 0x50 // nonzero after a call = error\n")
	b.WriteString("\tZPSizeLo = 0x52 // 16-bit read size (block multiple)\n")
	b.WriteString("\tZPLdrLo  = 0x55 // 16-bit load address\n")
	b.WriteString("\tZPNamLo  = 0x57 // 16-bit pointer to a length-prefixed name\n")
	b.WriteString(")\n\n")
	b.WriteString("// SlotSites are the operand addresses whose low byte must be OR'd with the\n")
	b.WriteString("// boot slot (slot<<4, the boot ROM's $2B) before the driver's first call.\n")
	b.WriteString("var SlotSites = []uint16{")
	for i, s := range sites {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "0x%04X", s)
	}
	b.WriteString("}\n")
	return b.String()
}

func genInc(codeLen, blobLen, trackd1, slotLo, slotHi int, sites []int) string {
	var b strings.Builder
	b.WriteString("; Generated by cmd/genrwts. DO NOT EDIT.\n")
	b.WriteString("; The resident ProRWTS2 read-only floppy driver (docs/prorwts2-design.md):\n")
	b.WriteString("; a relocated image delivered by stage 2, copied to Language Card bank 1 at\n")
	b.WriteString("; RWTS_ENTRY by m8rwtsinit, which also pokes the boot slot into the operand\n")
	b.WriteString("; addresses listed at RWTS_SLOTLO/HI (a table appended to the blob itself,\n")
	b.WriteString("; so it reads from bank 1 and costs UICODE nothing) and seeds RWTS_TRACKD1\n")
	b.WriteString("; from the loader's $41.\n\n")
	fmt.Fprintf(&b, "RWTS_ENTRY    = $%04X   ; opendir: read the named file\n", reloc)
	fmt.Fprintf(&b, "RWTS_SIZE     = %d     ; blob bytes ($%04X-$%04X)\n", blobLen, reloc, reloc+blobLen-1)
	fmt.Fprintf(&b, "RWTS_CODE     = %d     ; of which code\n", codeLen)
	fmt.Fprintf(&b, "RWTS_TRACKD1  = $%04X   ; current-track operand (seed from $41)\n", trackd1)
	fmt.Fprintf(&b, "RWTS_DIRBUF   = $%04X   ; 512-byte op-time scratch in MOVESTACK\n", dirbufAddr)
	fmt.Fprintf(&b, "RWTS_NSLOT    = %d      ; slot-poke operand addresses, in the blob:\n", len(sites))
	fmt.Fprintf(&b, "RWTS_SLOTLO   = $%04X   ; their lo bytes...\n", slotLo)
	fmt.Fprintf(&b, "RWTS_SLOTHI   = $%04X   ; ...and hi bytes\n", slotHi)
	b.WriteString("; zero-page API (inside the $3C-$67 window rwtszp swaps):\n")
	b.WriteString("RWTS_ZP       = $3C     ; first byte of the driver's zp window\n")
	b.WriteString("RWTS_ZPN      = $2C     ; window length ($3C-$67)\n")
	b.WriteString("RWTS_STATUS   = $50     ; nonzero after the call = error\n")
	b.WriteString("RWTS_SIZELO   = $52     ; 16-bit read size (block multiple)\n")
	b.WriteString("RWTS_LDRLO    = $55     ; 16-bit load address\n")
	b.WriteString("RWTS_NAMLO    = $57     ; 16-bit pointer to length-prefixed name\n")
	return b.String()
}

func check(err error) {
	if err != nil {
		fatal("genrwts: %v", err)
	}
}

func fatal(f string, args ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", args...)
	os.Exit(1)
}
