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
//     buffer — is moved to AUX op-time scratch ($0200-$03FF, the low
//     transposition-table window, dead before the first search) instead of
//     the in-bank default that would collide with the UI at $E000. With
//     allow_aux=1 dirbuf MUST live in aux (the driver reads it back under
//     RAMRD mid-loop), so the caller runs the whole call with aux read+write
//     on. Upstream explicitly supports moving these buffers.
//  2. assembles it with ACME (the binary a2audit vendors; ACME env var or
//     -acme overrides).
//  3. extracts the relocated FLOPPY driver image (the bytes ProRWTS2's own
//     init would have copied to `reloc`), PREBUILDS the 6-and-2 denibble
//     table that init builds at run time (we never run init: it needs a
//     live ProDOS), and BAKES the book region's directory block number
//     (rwts.DirBlock) into the `unrblocklo/hi` operands.
//  4. emits the generated artifacts, all committed:
//     internal/rwts/rwtsblob.bin — the blob stage 2 delivers
//     internal/rwts/gen.go       — addresses/sizes for the Go gates
//     asm/rwts.inc               — the same numbers for asm/m8.s, plus the
//     slot-poke site table as .lobytes/.hibytes
//     macros (see m8rwtsinit)
//
// It ALSO builds the TRANSIENT WRITE-CAPABLE driver (docs/prorwts2-design.md
// Feature 2, docs/saveload-feasibility.md): the same source with
// enable_write=1, allow_aux=0, load_banked=0, relocated to main $0E00 — the
// on-demand payload the save command loads from the RWTSW disk file, runs
// once to overwrite the SAVE file's blocks in place, and discards. Its
// artifacts: internal/rwts/rwtswblob.bin and asm/rwtsw.inc. Two write-build
// specifics beyond the read build's treatment:
//
//   - encbuf is patched APART from dirbuf. Upstream's fast_subindex=0 floppy
//     branch aliases encbuf = dirbuf ("writes come from cache"), which for a
//     multi-block write from a user buffer means the first sector's nibble
//     staging (encsec's `sta encbuf,y`) overwrites the sapling block list's
//     LO bytes in dirbuf — block 2+ of the file would be written to a
//     garbage block number. See the feasibility doc §2.
//   - the slot pokes come in TWO shapes: the ten $C08x soft-switch operands
//     (OR the slot into the low byte, as the resident build) and the four
//     unrslot1-4 sites, whose operand IS the raw slot<<4 immediate
//     (`ldx #$d1` placeholders inside the timed write loops).
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
	// dirbufAddr is the driver's 512-byte directory/index buffer. With
	// allow_aux=1 it must be an AUX address in the low TT region: the driver's
	// aux read path switches BOTH RAMRD and RAMWRT on for the whole read loop
	// (setaux uses auxreq for both), and it reads the sapling block-list back
	// out of dirbuf MID-LOOP -- so dirbuf must be coherent under aux, i.e. the
	// caller runs the whole call with SETAUXRD+SETAUXWR on and dirbuf lives in
	// aux, NOT in main (main $1300 would read aux garbage). aux $0200-$03FF is
	// the low transposition-table window, dead scratch before the first search
	// -- and the big book only loads at boot / New Game, before any search.
	// See docs/loadcover-feasibility.md. HAZARD: a save/load DURING a game
	// (not at a game boundary) would need to re-home dirbuf or save/restore
	// these aux bytes, because a mid-game TT is live here.
	dirbufAddr = 0x0200
	// auxReq is the driver's aux-request flag ($51, inside the $3C-$67 zp
	// window rwtszp swaps). allow_aux=1 defines it; set it to 1 in the driver
	// zp before a call to read straight into aux memory.
	auxReq = 0x51

	// The TRANSIENT WRITE build's homes (docs/saveload-feasibility.md §4/§6):
	// dead engine scratch (MOVESTACK, rebuilt by every search), used only
	// while a save is in flight. wDirBuf is where the assembler naturally
	// places dirbuf for this build ((dataend+$ff)&-256) — asserted, not
	// assumed; wEncBuf is the patched separate page (see the package doc).
	wReloc  = 0x0E00
	wDirBuf = 0x1200
	wEncBuf = 0x1400
	// wReqCmd is the driver's command byte ($54, in the swapped window),
	// defined only when enable_write=1. cmdwrite = 2.
	wReqCmd   = 0x54
	wCmdWrite = 2
)

// config is the 8fish option set for the RESIDENT READ-ONLY build
// (docs/prorwts2-design.md §2.1). aligned_read=1 (all our reads are block
// multiples) eliminates encbuf; might_exist/poll_drive/check_chksum buy
// error REPORTING (status) instead of a hang or silent garbage when the
// drive is empty or the disk swapped; use_smartport=0 because there is no
// SmartPort init path to keep (init is never run). allow_aux=1 lets the
// driver read a file DIRECTLY into aux memory (auxreq=1 in the driver zp),
// which the big-book loader uses to land each file straight at aux $4000+
// with no main-$2000 staging, so the boot splash on DHGR page 1 stays up
// through the load (docs/loadcover-feasibility.md).
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
	"allow_aux":     1,
}

// wConfig is the TRANSIENT WRITE-CAPABLE build: the read build's error
// reporting and sapling support, plus enable_write/detect_wp, minus the aux
// path (the save record is staged in and written from MAIN) and minus the
// banked load (it runs from main $0E00). MEASURED (feasibility doc §4):
// code 692 B + data 256 B = a 1,024 B blob, no timed-loop page-crossing pad
// needed at this config/reloc.
var wConfig = map[string]int{
	"verbose_info":  1,
	"enable_floppy": 1,
	"use_smartport": 0,
	"override_adr":  1,
	"aligned_read":  1,
	"enable_write":  1,
	"check_chksum":  1,
	"might_exist":   1,
	"poll_drive":    1,
	"detect_wp":     1,
	"allow_saplings": 1,
	"load_banked":   0,
	"lc_bank":       1,
	"allow_aux":     0,
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

// wImmSiteNames are the WRITE build's additional slot sites: `ldx #$d1`
// immediates inside the timed write loops whose operand must BECOME the raw
// slot<<4 (ProRWTS2's init stores `DEVNUM and #$70` there). Distinct poke
// rule from slotSiteNames (store, not OR-into-a-$C08x-low-byte).
var wImmSiteNames = []string{
	"unrslot1", // ldx #slot16 (prime drive / write prologue)
	"unrslot2", // ldx #slot16 (bit2tbl nibble loop)
	"unrslot3", // ldx #slot16 (encbuf nibble loop)
	"unrslot4", // ldx #slot16 (writenib2)
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

	acme := findACME()
	buildRead(string(src), acme)
	buildWrite(string(src), acme)
}

// assemble runs ACME over the patched source in a temp dir and returns the
// symbol table and the raw object image (a plain image of $0800..: the init
// code, then the unrelocated driver images).
func assemble(patched, acme string) (map[string]int, []byte) {
	tmp, err := os.MkdirTemp("", "genrwts")
	check(err)
	defer os.RemoveAll(tmp)
	asmPath := filepath.Join(tmp, "prorwts2-8fish.s")
	check(os.WriteFile(asmPath, []byte(patched), 0o644))

	cmd := exec.Command(acme, "-l", "sym.txt", "prorwts2-8fish.s")
	cmd.Dir = tmp
	out, err := cmd.CombinedOutput()
	if err != nil {
		fatal("genrwts: %s failed: %v\n%s", acme, err, out)
	}
	syms := parseSyms(filepath.Join(tmp, "sym.txt"))
	obj, err := os.ReadFile(filepath.Join(tmp, "prorwts2#060800"))
	check(err)
	return syms, obj
}

// needer returns a symbol lookup that fails loudly for the named build.
func needer(syms map[string]int, build string) func(string) int {
	return func(n string) int {
		v, ok := syms[n]
		if !ok {
			fatal("genrwts: symbol %q missing from the ACME symbol list (%s build)", n, build)
		}
		return v
	}
}

// prebuildNibTables fills nibtbl (and, if xlattbl >= 0, xlattbl) in the blob
// exactly as ProRWTS2's init loop would:
//
//	ldx #$16 / -- stx zp / txa / asl / bit zp / beq + / ora zp /
//	eor #$ff / and #$7e / - bcs + / lsr / bne - / tya /
//	sta nibtbl-$16,x / [txa / ora #$80 / sta xlattbl,y] / iny / + inx / bpl --
func prebuildNibTables(blob []byte, base, nibtbl, xlattbl int) {
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
			blob[nibtbl-base+(x-0x16)] = byte(y)
			if xlattbl >= 0 {
				blob[xlattbl-base+y] = byte(x) | 0x80
			}
			y++
		}
	}
	if y != 64 {
		fatal("genrwts: denibble table has %d valid nibbles, want 64", y)
	}
}

// bakeDirBlock pokes the book region's directory key block into the driver:
// init would have poked unrblocklo/hi from a ProDOS prefix walk; our disk
// layout is fixed at build time, so the operands are data.
func bakeDirBlock(blob []byte, need func(string) int, unreloc int) {
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
}

// operandSites resolves names to reloc-space OPERAND addresses (site+1),
// sorted, and verifies each assembled operand byte against ok.
func operandSites(blob []byte, need func(string) int, names []string, unreloc, base int,
	ok func(byte) bool, what string) []int {
	var sites []int
	for _, n := range names {
		sites = append(sites, need(n)-unreloc+base+1)
	}
	sort.Ints(sites)
	for _, s := range sites {
		if b := blob[s-base]; !ok(b) {
			fatal("genrwts: %s site $%04X operand is $%02X — the site list is stale", what, s, b)
		}
	}
	return sites
}

// buildRead assembles the RESIDENT READ-ONLY build and writes its blob and
// both generated includes. Its outputs are byte-for-byte the ones the
// shipping disk carries; nothing in the write build may change them.
func buildRead(src, acme string) {
	syms, obj := assemble(configure(src), acme)
	need := needer(syms, "read")

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
	if got := need("auxreq"); got != auxReq {
		fatal("genrwts: auxreq = $%04X, want $%04X (allow_aux must define it in the driver zp window)", got, auxReq)
	}
	if dataEnd > 0xDFC0 {
		fatal("genrwts: driver data ends at $%04X, past the $DFC0 budget in LC bank 1", dataEnd)
	}

	// The floppy driver's bytes are at file offset (unrelocdsk - $0800),
	// codeend-reloc long.
	codeLen := codeEnd - reloc
	off := unreloc - 0x800
	if off < 0 || off+codeLen > len(obj) {
		fatal("genrwts: driver image [$%X..+%d] outside the %d-byte ACME output", off, codeLen, len(obj))
	}

	blob := make([]byte, dataEnd-reloc)
	copy(blob, obj[off:off+codeLen])
	prebuildNibTables(blob, reloc, nibtbl, -1)
	bakeDirBlock(blob, need, unreloc)

	sites := operandSites(blob, need, slotSiteNames, unreloc, reloc,
		func(lo byte) bool { return lo == 0x80 || lo == 0x88 || lo == 0x89 || lo == 0x8C },
		"slot (soft-switch low byte)")
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

// buildWrite assembles the TRANSIENT WRITE-CAPABLE build and writes
// internal/rwts/rwtswblob.bin + asm/rwtsw.inc. The blob is the full
// reloc..dataend image (code, the gap, bit2tbl scratch, prebuilt
// nibtbl+xlattbl) — exactly dataend-reloc bytes, a whole number of blocks by
// construction is NOT assumed: the disk file pads it. The two slot-site
// tables are tucked into the code/data GAP (codeend..bit2tbl), so they ride
// inside the same 1,024 bytes instead of growing the file.
func buildWrite(src, acme string) {
	syms, obj := assemble(configureWrite(src), acme)
	need := needer(syms, "write")

	unreloc := need("unrelocdsk")
	codeEnd := need("codeend")
	bit2tbl := need("bit2tbl")
	nibtbl := need("nibtbl")
	xlattbl := need("xlattbl")
	dataEnd := need("dataend")
	trackd1 := need("trackd1")
	if entry := need("opendir"); entry != wReloc {
		fatal("genrwts: write build opendir = $%04X, want $%04X", entry, wReloc)
	}
	if got := need("dirbuf"); got != wDirBuf {
		fatal("genrwts: write build dirbuf = $%04X, want $%04X (the code grew across a page "+
			"boundary; re-measure the transient window in docs/saveload-feasibility.md §6)", got, wDirBuf)
	}
	if got := need("encbuf"); got != wEncBuf {
		fatal("genrwts: write build encbuf = $%04X, want $%04X (the anti-aliasing patch did "+
			"not take — encbuf sharing dirbuf corrupts multi-block writes)", got, wEncBuf)
	}
	if got := need("reqcmd"); got != wReqCmd {
		fatal("genrwts: write build reqcmd = $%04X, want $%04X", got, wReqCmd)
	}
	if _, aux := syms["auxreq"]; aux {
		fatal("genrwts: write build defines auxreq — allow_aux crept in; the transient driver " +
			"must be main-only")
	}

	codeLen := codeEnd - wReloc
	off := unreloc - 0x800
	if off < 0 || off+codeLen > len(obj) {
		fatal("genrwts: write driver image [$%X..+%d] outside the %d-byte ACME output", off, codeLen, len(obj))
	}
	blob := make([]byte, dataEnd-wReloc)
	copy(blob, obj[off:off+codeLen])
	prebuildNibTables(blob, wReloc, nibtbl, xlattbl)
	bakeDirBlock(blob, need, unreloc)

	swSites := operandSites(blob, need, slotSiteNames, unreloc, wReloc,
		func(lo byte) bool { return lo == 0x80 || lo == 0x88 || lo == 0x89 || lo == 0x8C },
		"write-build slot (soft-switch low byte)")
	immSites := operandSites(blob, need, wImmSiteNames, unreloc, wReloc,
		func(b byte) bool { return b == 0xD1 }, // upstream's `ldx #$d1` placeholder
		"write-build slot (raw slot<<4 immediate)")

	// The site tables live in the gap between codeend and bit2tbl.
	tblLen := 2*len(swSites) + 2*len(immSites)
	if codeLen+tblLen > bit2tbl-wReloc {
		fatal("genrwts: write build gap $%04X-$%04X cannot hold the %d-byte site tables",
			codeEnd, bit2tbl-1, tblLen)
	}
	slotLo := codeEnd
	slotHi := slotLo + len(swSites)
	immLo := slotHi + len(swSites)
	immHi := immLo + len(immSites)
	for i, s := range swSites {
		blob[slotLo-wReloc+i] = byte(s)
		blob[slotHi-wReloc+i] = byte(s >> 8)
	}
	for i, s := range immSites {
		blob[immLo-wReloc+i] = byte(s)
		blob[immHi-wReloc+i] = byte(s >> 8)
	}

	check(os.WriteFile("internal/rwts/rwtswblob.bin", blob, 0o644))
	check(os.WriteFile("asm/rwtsw.inc", []byte(genWInc(codeLen, len(blob), trackd1,
		slotLo, slotHi, len(swSites), immLo, immHi, len(immSites))), 0o644))

	fmt.Printf("genrwts: ProRWTS2 TRANSIENT WRITE driver\n")
	fmt.Printf("  code $%04X-$%04X (%d B), data to $%04X; blob %d B -> internal/rwts/rwtswblob.bin\n",
		wReloc, codeEnd-1, codeLen, dataEnd-1, len(blob))
	fmt.Printf("  entry $%04X, trackd1 $%04X, dirbuf $%04X, encbuf $%04X, %d+%d slot sites\n",
		wReloc, trackd1, wDirBuf, wEncBuf, len(swSites), len(immSites))
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
	// AUX low-TT op-time scratch instead (upstream supports moving it); with
	// allow_aux the caller runs the call under RAMRD+RAMWRT so dirbuf reads
	// back coherently from aux.
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

// configureWrite applies the write build's option set and its two structural
// patches: reloc (the load_banked=0 branch's $bc00 default) and the encbuf
// anti-aliasing fix (see the package doc). dirbuf is NOT patched — the
// assembler's natural placement ((dataend+$ff)&-256 = $1200) is asserted in
// buildWrite instead.
func configureWrite(src string) string {
	for k, v := range wConfig {
		pat := regexp.MustCompile(`(?m)^(\s*` + k + `\s*=\s*)\d+`)
		n := len(pat.FindAllString(src, -1))
		if n != 1 {
			fatal("genrwts: option %q matched %d times in %s, want 1", k, n, srcPath)
		}
		src = pat.ReplaceAllString(src, "${1}"+strconv.Itoa(v))
	}
	// reloc for the load_banked=0, load_high=0 branch.
	src = replaceOnce(src, "reloc     = $bc00", fmt.Sprintf("reloc     = $%04x", wReloc))
	// encbuf: its own page. Upstream's fast_subindex=0 alias (encbuf=dirbuf)
	// makes the first written sector's nibble staging destroy the block
	// list's lo bytes — fatal for any multi-block write from a user buffer.
	src = replaceOnce(src,
		"        encbuf = dirbuf ;writes come from cache",
		"        encbuf = dirbuf + $200 ;8fish: NOT aliased -- see cmd/genrwts")
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
	fmt.Fprintf(&b, "\tDirBufAddr = 0x%04X // 512-byte directory buffer, AUX op-time scratch (low TT)\n", dirbufAddr)
	b.WriteString("\n\t// The driver's zero-page window (saved/swapped around every call\n")
	b.WriteString("\t// by asm/m8.s rwtszp), and its API bytes inside it.\n")
	b.WriteString("\tZPLo    = 0x3C\n\tZPLen   = 0x2C // $3C-$67\n")
	b.WriteString("\tZPAuxReq = 0x51 // set to 1 to read straight into aux memory (allow_aux)\n")
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
	fmt.Fprintf(&b, "RWTS_DIRBUF   = $%04X   ; 512-byte op-time scratch, AUX low-TT (allow_aux)\n", dirbufAddr)
	fmt.Fprintf(&b, "RWTS_AUXREQ   = $%04X   ; set to 1 in the driver zp to read into aux\n", auxReq)
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

// genWInc emits asm/rwtsw.inc: the TRANSIENT WRITE driver's layout facts for
// asm/saveload.s (the on-demand save/load orchestrator).
func genWInc(codeLen, blobLen, trackd1, slotLo, slotHi, nSlot, immLo, immHi, nImm int) string {
	var b strings.Builder
	b.WriteString("; Generated by cmd/genrwts. DO NOT EDIT.\n")
	b.WriteString("; The TRANSIENT ProRWTS2 write-capable floppy driver (docs/prorwts2-design.md\n")
	b.WriteString("; Feature 2, docs/saveload-feasibility.md): loaded ON DEMAND from the RWTSW\n")
	b.WriteString("; disk file into dead engine scratch at main $0E00 by the save command,\n")
	b.WriteString("; materialized (slot pokes + trackd1 seed), run once to overwrite the SAVE\n")
	b.WriteString("; file's blocks in place, and discarded. Nothing write-capable is resident.\n\n")
	fmt.Fprintf(&b, "RWTSW_ENTRY   = $%04X   ; opendir: open the named file, then read/write per REQCMD\n", wReloc)
	fmt.Fprintf(&b, "RWTSW_SIZE    = %d   ; blob bytes ($%04X-$%04X), incl. prebuilt tables + site lists\n", blobLen, wReloc, wReloc+blobLen-1)
	fmt.Fprintf(&b, "RWTSW_CODE    = %d    ; of which code\n", codeLen)
	fmt.Fprintf(&b, "RWTSW_TRACKD1 = $%04X   ; current-track operand (seed from the RESIDENT driver's)\n", trackd1)
	fmt.Fprintf(&b, "RWTSW_DIRBUF  = $%04X   ; 512-byte directory/index scratch, MAIN\n", wDirBuf)
	fmt.Fprintf(&b, "RWTSW_ENCBUF  = $%04X   ; 512-byte write nibblize scratch, MAIN (NOT dirbuf)\n", wEncBuf)
	fmt.Fprintf(&b, "RWTSW_NSLOT   = %d     ; soft-switch operand addresses (ORA the slot into the lo byte):\n", nSlot)
	fmt.Fprintf(&b, "RWTSW_SLOTLO  = $%04X   ; their lo bytes...\n", slotLo)
	fmt.Fprintf(&b, "RWTSW_SLOTHI  = $%04X   ; ...and hi bytes\n", slotHi)
	fmt.Fprintf(&b, "RWTSW_NIMM    = %d      ; raw slot<<4 IMMEDIATE operands (STORE the slot):\n", nImm)
	fmt.Fprintf(&b, "RWTSW_IMMLO   = $%04X   ; their lo bytes...\n", immLo)
	fmt.Fprintf(&b, "RWTSW_IMMHI   = $%04X   ; ...and hi bytes\n", immHi)
	b.WriteString("; zero-page API: same $3C-$67 window as the resident driver (rwtszp serves\n")
	b.WriteString("; both), same STATUS/SIZELO/LDRLO/NAMLO bytes, PLUS the command byte —\n")
	b.WriteString("; there is NO auxreq in this build ($51 is unused; the write is main-only).\n")
	fmt.Fprintf(&b, "RWTSW_REQCMD  = $%02X     ; 1 = read, 2 = write (set before every call)\n", wReqCmd)
	fmt.Fprintf(&b, "RWTSW_CMDWRITE = %d      ; the write command\n", wCmdWrite)
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
