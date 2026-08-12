# Save/load games — feasibility re-validation (Phase 1)

Status: **GO, with one architecture amendment** (2026-08-12). The design in
`docs/prorwts2-design.md` §4 (Feature 2) predates three shipped changes —
`allow_aux` + the aux-direct big-book read, `dirbuf` at aux `$0200`, and the
splash/ponder work — and one budget fact it explicitly said to re-measure
before committing: the UICODE pool. Re-validated against the current tree,
everything holds EXCEPT the assumption that ~430 B of save/load glue can live
in UICODE: **UICODE is at 5,873 of 5,888 bytes — 15 bytes free.** The
amendment (§6): the save/load logic itself becomes a THIRD transient payload,
loaded from a disk file into dead engine scratch exactly the way the write
driver is, leaving only two ~40-byte command stubs resident, funded by
evicting boot-only routines into the boot-transient SPLASH segment.

Numbers below are MEASURED (assembled/inspected today) unless marked DERIVED.

## 1. auxreq + the main-RAM landing ($0E00) — HOLDS

The resident reader reads to MAIN when the driver zp byte `auxreq` ($51, in
the swapped window) is 0 — `m8splash` is the shipped, gated precedent
(`TestDiskSplash`): it stores 0 to RWTS_AUXREQ explicitly (the held image
retains 1 after any `m8bigbook`) and runs the call with NO RAMRD/RAMWRT
bracket. The write-driver load is the same call shape with `ldr=$0E00`,
`size=$0400`. Confirmed from the vendored source: `setaux` indexes
`CLRAUXRD,x / CLRAUXWR,x` by `auxreq`, so auxreq=0 CLEARS both switches and
every buffer/dest access is main. The SETAUXRD+SETAUXWR bracket is needed
ONLY for aux reads (m8bigbook); a main read must run with aux OFF, which is
the resting state at command-dispatch time.

## 2. dirbuf and the transient buffers — NO COLLISIONS, one patch required

**The resident driver's dirbuf ($0200) follows RAMRD/RAMWRT.** It is an
absolute address, so on a main-mode call (save/load, splash) the directory
and index blocks land in MAIN `$0200-$03FF`, not aux — the genrwts HAZARD
note ("a mid-game TT is live at aux $0200") applies only to aux-mode calls,
which save/load never makes. The big book (aux `$4000-$BFFF`) and the whole
of aux are untouched; a gate asserts it.

**Main `$0200-$03FF` mid-game is tolerable to clobber** (and the boot splash
read already clobbers it once today):

- `$0200-$020F` free; `$0210-$022C` entropy accumulator + book RNG + verify
  counters. ENTCNT/ENTROPY get overwritten by directory bytes — an entropy
  POOL accepts arbitrary bytes, and entkey keeps folding keystrokes after.
  BOOKRND/BOOKHIT are re-poked by `uibookrnd` before every probe.
- `$0230-$02B7` per-search arrays (ITSTART, CHECKERSQ/DIR, CLSPRES, UNDOPD)
  — rebuilt by every search, dead at dispatch time.
- `$0300-$0309` CEILMAX/UNSTCEIL/MINSPEND/STABLE — poked by `uilimits`
  before every timed search.
- `$03D6-$03E6` book-probe pads — live only inside `bookprobe`.
- `$03F2-$03F4` reset vector + power-up byte: overwritten with
  index/directory-block bytes, which for our layout are ZEROS at those
  offsets ($00 ≠ $00 EOR $A5), so Ctrl-Reset still cold-boots. Deterministic
  today via the boot splash read; save/load adds no new state.

**The transient write driver's own buffers** (measured build, §4): dirbuf
`$1200-$13FF`, encbuf `$1400-$15FF` — inside the dead `$0E00-$1FFF`
MOVESTACK window, clear of PIECESQ (`$0800-$081F`, LIVE), the per-ply undo
arrays (`$0900-$0DFF`), the DHGR main half (`$2000-$3FFF`), the resident
driver's aux `$0200`, and the save data at LC `$F800`.

**One required source patch (write build only):** upstream's floppy
`load_high=0` buffer block sets `encbuf = dirbuf` ("writes come from
cache") when `fast_subindex=0`. For OUR use — a multi-block write from a
user buffer — that aliasing is corruption: `encsec` stages each sector's
6-bit bytes into `encbuf[0..255]`, which would overwrite the sapling block
list's LO bytes in `dirbuf[0..255]` after the first sector, sending block 2+
of the save file to garbage block numbers. The write build patches
`encbuf = dirbuf + $200`. (Same anchored-replace style as the existing
dirbuf patch; the disk-integrity gate is the mutation check.)

## 3. The big book, the splash region, and the live history — HOLDS

- Save/load performs NO aux access (auxreq=0 throughout, no RAMRD/RAMWRT),
  so the 100%-full big book at aux `$4000-$BFFF` is neither disturbed nor
  needed. If BIGBOOKOK is open at save/load time it STAYS open — a LOAD
  replays through `uiapply` (make/evalinit only, no search), so the TT
  window is never written and the latch state remains truthful.
- `$F800-$FAFF` holds the LIVE game history at save time. The SPLASH segment
  (225 B, `$F800-$F8E0`) is copied there at boot and executes once before
  `m8new`; from the first recorded ply on, those pages are history arrays.
  Save reads them; nothing writes them but `uiapply`.

## 4. The write build — MEASURED, fits with room to spare

Assembled from the pristine vendored `PRORWTS2.S` (same sha256 pin) with
ACME, config = the resident reader's flags MINUS `allow_aux`/`load_banked`/
`lc_bank`, PLUS `enable_write=1 detect_wp=1`, `reloc=$0E00`:

```
verbose_info=1 enable_floppy=1 use_smartport=0 override_adr=1 aligned_read=1
enable_write=1 check_chksum=1 might_exist=1 poll_drive=1 detect_wp=1
allow_saplings=1 load_banked=0 allow_aux=0   reloc=$0e00
```

- code `$0E00-$10B3` = **692 B**; data `$1100-$11FF` = 256 B (bit2tbl 86 +
  nibtbl 106 + xlattbl 64). Blob `$0E00-$11FF` = **1,024 B exactly = 2
  blocks** (vs the design's 1,114 B estimate — smaller because
  `use_smartport=0` and no aux path).
- **No page-crossing pad needed**: all seven timed-loop `!serious` checks
  pass at this config/reloc (the design's 28-byte pad was for its slightly
  different option set). The gap `$10B4-$10FF` (76 B) absorbs the appended
  slot-site tables, keeping the blob at 2 blocks.
- dirbuf `$1200-$13FF` (natural), encbuf patched to `$1400-$15FF`.
- zp API: same `$3C-$67` window (`rwtszp` serves both drivers unchanged);
  `status` $50, `sizelo/hi` $52/53, **`reqcmd` $54 (new: 2 = cmdwrite)**,
  `ldrlo/hi` $55/56, `namlo/hi` $57/58. `auxreq` does not exist in this
  build (allow_aux=0) — $51 is simply unused.
- genrwts additions needed: prebuild **xlattbl** (init builds it alongside
  nibtbl: `xlattbl[y] = x|$80` in the same valid-nibble loop) and poke the
  FOUR write-path slot sites `unrslot1-4`, whose operands are the raw
  slot<<4 IMMEDIATE (`ldx #$60`), unlike the ten `$C08x`-address sites —
  two site lists with two poke rules.
- `trackd1` operand at `$0F9D`; seeded at materialization time from the
  RESIDENT driver's own trackd1 operand in bank 1 (always current after the
  blob-load read that immediately precedes it).

**Fixed-size in-place writes confirmed from source:** the write path walks
the same directory-entry → index-block → data-block chain as reads
(`seekrdwr` with command=cmdwrite; `cmpsecwr` finds the sector's address
field and rewrites only its data field), size is rounded up to whole blocks
at open, and NOTHING in the floppy driver reads or writes the volume bitmap
or any metadata — the file's size and placement cannot change. The
placeholder-file model is exactly what the code implements.

## 5. Bootstrapping and disk space — HOLDS

The write-driver blob and the save-file are ordinary sapling files in the
existing directory (key block 208): the resident reader loads them like
BOOK0-3/SPLASH. Disk ledger today: **359 of 560 sectors used, 201 free** —
tracks 13-23 (blocks 104-191) are entirely empty, between the SD stages
(tracks 0-12, 197 sectors) and the splash+book region (tracks 24-34).
New files (≈16 sectors of 201): the write driver (1 index + 2 data blocks),
the save/load ORCHESTRATOR (§6; 1 index + ~3 data blocks), and one save
slot (1 index + 2 data blocks), all placed in blocks ≤191, clear of both
the SD region and the book region; the directory gains three entries (5 of
~12 slots used today).

## 6. The amendment: where the CODE lives (UICODE is full)

Measured: `__UICODE_SIZE__ = $16F1` → **15 bytes free** of the $E000-$F6FF
pool. The design priced ~430 B of resident glue and said "re-measure before
committing"; measured, it does not fit, and no amount of shaving gets 400 B
from 15. Two structural escapes were considered:

- **Bank-2-resident code** (the design's priced hatch): free bank-2 space is
  2,316 B, but code running at `$Dxxx` in bank 2 cannot call the driver
  (bank 1), `uiputs`/`uisetmsg` (they restore bank 1 under the caller), or
  switch banks itself without vanishing mid-instruction — every call needs a
  $E000+ trampoline with re-select discipline. Workable but a bank-
  discipline bug factory.
- **A transient orchestrator, chosen**: the save/load logic is COLD and
  already needs the disk in the drive, so it ships as a third on-demand
  payload — a `SAVELOAD` file the resident reader pulls to main
  **`$1A00-$1FFF`** (top of the dead MOVESTACK window) and jumps into. Main
  RAM code has NO bank hazards: it may call the bank-1 driver, uisetmsg,
  uiapply, anything. Resident cost: two dispatch stubs + one shared
  load-and-run helper (~90-120 B), funded by moving BOOT-ONLY routines
  (`m8rwtsinit` ~45 B, `m8bookaux` ~30 B — both run before `m8new`) into
  the boot-transient SPLASH segment ($F800, 287 B free), which the copier
  already delivers on both build paths. Everything write-capable AND nearly
  everything save/load-shaped is then transient — the design's own
  architecture, applied once more.

Transient-window layout during an operation (all dead engine scratch,
rebuilt by the next search; PIECESQ at $0800 and the undo arrays at
$0900-$0DFF untouched):

```
$0E00-$11FF  write-driver blob (SAVE only; also the read-back verify buffer)
$1200-$13FF  write-driver dirbuf     $1400-$15FF  write-driver encbuf
$1600-$19FF  save-record staging (1,024 B = the 2-block record)
$1A00-$1FFF  the orchestrator (save+load logic, replay loop)
```

LOAD's replay generates ply-0 move lists from `$0E00` (≤ ~1 KB), well below
the orchestrator at `$1A00`, and the record has already been copied into the
LC history arrays before replay begins.

## 7. Harness findings (the gates can be built, with two local pieces)

goapple2's Disk II card fully models nibble WRITES (`MODE_WRITE|MODE_LOAD`
→ `writeOne`), and `disk.NybbleToDos16` denibblises a modified disk back to
.dsk bytes — but two properties need local (internal/ui) code, not goapple2
changes:

- **`disk.Nybble.writeable` is private and always false** — there is no
  setter, so a loaded .dsk is write-protected (detect_wp would report it,
  honestly!). internal/ui gets a ~30-line writable `cards.Disk`
  implementation wrapping the same nibblised `Tracks` (constructed via
  `disk.DiskFromFile(...).Tracks`). A `SetWriteable` upstream in goapple2
  is the better long-term home; noted for a separate change.
- **The emulated write splice starts ~5 nibbles early.** The disk position
  only advances on data-register access, so ProRWTS2's timed post-address
  delay (`ldy #$24` loop) doesn't move the medium as it does on hardware;
  the driver's first 5 sync bytes land ON the address field's checksum pair
  + epilogue instead of in the gap. Harmless to the driver (its `readadr`
  never reads address checksums or epilogues; re-reads, re-writes, and the
  data field are all clean) and nonexistent on hardware, but goapple2's
  `readOneSector` VERIFIES the address checksum and would refuse the
  written sector. The harness therefore extracts sectors with its own
  tolerant reader (address checksum not enforced; DATA checksum still
  enforced). Budget check: the written stream (5 sync + 3 prologue + 343
  data+checksum + 4 epilogue = 355 nibbles) fits the 382-nibble span
  between address fields (intraSync 8 + data field 349 + preSync 20 + the
  5 overwritten bytes), so the next sector's address field is never touched.

The round-trip and disk-integrity gates then run exactly as specified: boot
the real .dsk, play, SAVE via keystrokes, extract all 560 sectors before and
after — only the save file's 4 data sectors may differ.

## 8. Verdict

**GO.** All five re-validation items hold; the two real discoveries are the
`encbuf = dirbuf` aliasing patch (a write-corruption bug for our access
pattern, caught before any code was written) and the UICODE exhaustion,
which the transient-orchestrator amendment resolves within the design's own
on-demand philosophy. The write build is measured at 1,024 B — 2 blocks,
smaller than designed — and the whole feature costs ≈16 of 201 free sectors
and ~0 net resident bytes (stubs funded by boot-only evictions).
