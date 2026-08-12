# ProRWTS2 on the 8fish disk — design

Status: **FEATURE 1 SHIPPED (2026-08-08); FEATURE 2 SHIPPED (2026-08-12)** —
the big book and its resident read-only reader are implemented as specified
(§10 has the measured deltas), and save/load games shipped with the
amendments re-validated in docs/saveload-feasibility.md (§11 here has its
deltas).

Original status line: DESIGN ONLY (2026-08-08). No engine code, no `asm/`
edits, no build changes. This document specifies how peterferrie's ProRWTS2 (read+write ProDOS
driver, BSD-3-Clause) gives the shipped disk (1) a much larger opening book in
the idle transposition table and (2) save/load games — and answers the central
bootstrapping question: **if the driver is loaded on demand, what loads it?**

Written against the real code: `asm/m8.s` (the copier and chain loader),
`internal/delivery/delivery.go` (the Standard Delivery re-entry contract),
`asm/book.s` / `internal/book/book.go` (the resident book and its probe),
`asm/tt.s` (the transposition table), `asm/ui.s` (the LC memory map and the
game-history arrays), and the ProRWTS2 source itself (`PRORWTS2.S`, 3,928
lines, fetched and **assembled with ACME** — the size numbers below are
measured assembler output for 8fish's exact configurations, not the recon's
estimates). Numbers are flagged MEASURED or DERIVED throughout.

---

## 0. Summary and recommendation

| question | answer |
|---|---|
| Can ProRWTS2 be **fully** on-demand — nothing resident? | **No, and this is provable** (§1). After the game has run, every one of the chain loader's re-entry preconditions is destroyed, and the only reader that survives — the `$C600` boot ROM — can only re-run the whole fixed boot chain (7.1 s, read-only). Something must be resident. |
| What is the **minimum resident cost**? | **~960 B: a read-only ProRWTS2 build**, resident in **Language Card bank 1's free tail ($DC00-$DFBF)** — the resting bank, so calls need no bank switch. MEASURED: 658 B code + 192 B data, page-rounded 960, buffers relocated to main-RAM scratch used only during an operation. |
| Where did "load the loader on demand" go? | It survives, one level up: **the write-capable driver is the on-demand payload.** The resident reader loads the read+write build (MEASURED 1,114 B packed) from a disk file into dead engine scratch ($0E00) only when a save is requested, runs it, and discards it. Nothing write-capable is ever resident. |
| Big book: how big? | The TT window aux `$4000-$BFFF` is 32,768 B → **3,640 entries** at the existing 9-byte stride (today: 633). Loaded after the first board paint (~5 s, DERIVED), reloaded on New Game only if the game left book. Boot-to-board stays 7.13 s. |
| Saves: what is a saved game? | Header + the existing history arrays `UIHFROM/UIHTO/UIHFLAG` ($F800-$FAFF): 789 B used of a **1,024 B (2-block) pre-existing placeholder file**, overwritten in place — exactly the shape `enable_write` requires. Load = read + replay through the machinery takeback already uses. |
| Disk format: ProDOS volume vs. raw SD? | **Neither has to yield.** ProRWTS2 does not need a legal ProDOS *volume*, only ProDOS-*shaped* structures (a directory chain + index blocks) at a block number we choose, because with fixed-size in-place writes it never reads the volume bitmap. The SD region is untouched and boot cost is zero (§5). A real mountable volume is the priced alternative. |
| Toolchain? | ACME (binary already in `~/gh/a2audit/bin/acme`), vendored source pinned to a commit, plus a **small measured patch set** (§6): the 8fish option combination does not assemble upstream-clean (timed-loop page crossings; a 28-byte pad before `encsec` fixes it — MEASURED), and `init` must be replaced because it requires a running ProDOS (§2.3). |
| Go/no-go? | **Feature 1 (big book): GO**, with the resident reader. **Feature 2 (saves): GO after feature 1**, as the transient-writer second phase. Costs and gates in §8. |

---

## 1. The bootstrapping question: what can read the disk after boot?

This is the design's foundation, so it is argued first and bluntly.

### 1.1 The chain-load re-entry is dead once the game has run

`asm/m8.s` re-enters the Standard Delivery loader at `$0802` mid-boot (stage
2). The precondition list is documented in both `asm/m8.s` (lines 136-147) and
`internal/delivery/delivery.go`, and every entry on it is destroyed by playing
one game:

| precondition at `$0802` | state after the game has run |
|---|---|
| `$0800-$08FF`: the boot sector's own loader code | **overwritten** — `$0800` is `PIECESQ`, the live position (`asm/m8.cfg`) |
| `Y` = next sector, `X`/`$2B` = slot<<4, `$26` = 0 | registers long gone; `$2B`/`$26` are engine zero page |
| `$41` = the track the head is on | engine zero page; and the head has not moved *since boot*, so nothing knows the track anyway |
| `$0800` = `$01` (ROM's sector count) | same byte as `PIECESQ[0]` |
| drive motor on, disk spinning | motor off since boot |

Restoring all of that from cold means: a saved 256-byte copy of the boot
sector's code, motor-on plus spin-up handling, and re-deriving the head track
by reading an address field — i.e. **hand-rolling the fragile third of an
RWTS** (arm and sync handling, with no recalibration or retry). That is
~380-400 B resident plus exactly the class of code the project's hard rule
exists to avoid. **Rejected.**

### 1.2 The `$C600` boot ROM always works — but only as a reboot

The Disk II boot ROM is the one reader that needs zero resident bytes. But
entering it re-runs the fixed chain: boot sector → all 148 stage-1 sectors →
copier. Our code gets control only after ~5.4 s of re-reading, there is no way
to substitute a different page table before that (the table is on disk, in the
boot sector), and the path can never write. It is exactly the existing `Q`
behaviour (`TestDiskQuitReboots`). Usable as a degraded fallback for "reload
the book by rebooting", useless for saves. **Kept as fallback only** (§3.6).

### 1.3 Therefore: a resident READER, and everything else on demand

The honest answer to the prompt's question is:

> **Fully on-demand is impossible.** After boot, nothing in RAM can read the
> disk, and the only ROM path is a full reboot that can never write. The
> minimum resident cost is one robust reader. The good news, measured: that
> reader is only **960 bytes**, it fits in Language Card bank 1's free tail
> where calling it needs no bank switch, and once it exists *everything else*
> — the write-capable driver included — really can be loaded on demand and
> discarded. zellyn's "load the save-game code in on demand" idea is sound;
> it just sits one bootstrap level higher than a bare stub.

Three candidate residents were priced:

| candidate | resident B | verdict |
|---|---|---|
| SD re-entry stub + boot-sector copy | ~380-400 | fragile, hand-rolled sync/arm code, no recalibration — **reject** |
| ProRWTS2 **read-only** floppy build | **960** (MEASURED) | robust (own motor/seek/sync handling, checksum verify), upstream-maintained — **recommend** |
| ProRWTS2 read+write resident | 1,280 page-rounded (MEASURED) | only fits bank 2 at `$DB00` leaving **27 B** of UIDATA2 growth headroom — a growth collision magnet — **reject** (write stays transient) |

---

## 2. The architecture

```
RESIDENT (forever):
  LC bank 1  $DC00-$DFBF   ProRWTS2 read-only floppy driver     960 B (MEASURED)
  UICODE     $E000 region  glue: zp swap, call marshal, verify  ~300 B (DERIVED)
  M8VARS     $F7B6-$F7FF   driver zp image + latch + status     ~50 of 74 B free

ON DEMAND (during one operation, then discarded):
  MAIN $0E00-$12FF  R/W driver code+data (saves only)     1,114 B (MEASURED)
  MAIN $1300-$14FF  dirbuf                                  512 B
  MAIN $1500-$16FF  encbuf (writes only)                    512 B
  MAIN $1700-$1EFF  save-record staging                    ~1 KB
  MAIN $2000-$3FFF  big-book chunk staging (8 KB, DHGR main half — repainted after)
```

### 2.1 Measured driver sizes, and the exact configurations

Assembled with the ACME binary a2audit vendors, from the upstream
`PRORWTS2.S` (both drivers in the file; only the floppy driver is resident —
the HDD half is discarded at build time). All numbers are assembler-reported.

**Resident read-only build** (the recommended config):
`enable_floppy=1 aligned_read=1 allow_saplings=1 override_adr=1 might_exist=1
poll_drive=1 check_chksum=1 load_banked=1 lc_bank=1 reloc=$DC00`
→ **code 658 B, data (`nibtbl`/`bit2tbl`) 192 B**; page-rounded footprint
`$DC00-$DFBF` = 960 B, 64 B tail spare. `dirbuf` (512 B) relocated to main
`$1300`, live only during a call. `aligned_read=1` (all our reads are block
multiples) eliminates `encbuf` on the read side. `might_exist/poll_drive/
check_chksum` buy error *reporting* (`status` in zp) instead of a hang or
silent garbage when the drive is empty or the disk swapped: worth 57 B
(658 vs 601 minimal, MEASURED).

**Transient read+write build** (the on-demand save payload):
same + `enable_write=1` (and optionally `detect_wp=1`), `load_banked=0
reloc=$0E00` → **code 858 B (includes the 28-byte alignment pad, §6), data
256 B** (adds `xlattbl`); packed 1,114 B. Needs `dirbuf` + `encbuf` = 1,024 B
of op-time scratch. Writes occur in block multiples to a pre-existing file
whose size cannot change — the placeholder model in §4.

### 2.2 Why these homes are safe

- **LC bank 1 `$DC00-$DFBF`.** The free tail is `$DBE8-$DFFF` = 1,048 B
  (`asm/m8.s` line 58, gated by `internal/delivery.TestLanguageCardBank1Layout`).
  960 fits with the required page alignment. Bank 1 is the **resting state**
  — LCCODE (`ttfetch`/`bkfetch`) and DHTILES already live there — so the glue
  calls the driver with **no bank switching at all**, and bank 2's 1,307 B
  free tail stays reserved for what actually grows: UIDATA2, the segment
  every new string lands in. (The recon suggested bank 2; that was written
  when bank 2 was 4,096 B empty and before the driver was measured small
  enough for bank 1.) The bank-1 layout gate gains a region entry so any
  artwork/table growth collides in a test, not on the machine.
- **Main `$0E00-$1FFF` op-time scratch.** Engine per-ply undo/search arrays
  and MOVESTACK — dead between searches (`asm/m8.cfg`: "garbage until the
  first search"; they are rebuilt by every search). Driver operations run
  only from command dispatch, never during a search or ponder (a keypress
  ends pondering before `uidispatch` runs — `docs/ponder-design.md` §2).
- **Main `$2000-$3FFF` book staging.** The DHGR main half. Book loads happen
  with a status line up; the board is repainted after (188 ms, measured
  elsewhere). Staging through main is forced anyway: ProRWTS2 without
  `allow_aux` writes main only, and the m8bookaux pattern (glue at `$E000`
  runs with RAMWRT on, reads main, writes aux) already exists for exactly
  this copy.
- **Zero page.** The driver claims `$3C-$67` + `$50-$59` (API) — overlapping
  engine/UI zp (`CUROPENING` $3D, `RUNPTR` $3E-$3F, `curtrk` $40, ProDOS-
  constant slots $42-$47…). Protocol: the glue keeps a **driver zp image**
  (~45 B) in the `$F7B6+` hold; each call saves the engine's window, installs
  the image, runs the op, extracts the image (preserving persistent driver
  state — `curtrk`, `phase`), restores the engine's window. ~60-80 B of glue,
  and it makes the driver's zp usage invisible to everything else. A gate
  snapshots all 256 zp bytes around a driver call and requires equality.

### 2.3 The init problem: ProRWTS2 assumes ProDOS is running, and we have none

Read from source, not guessed: `init` calls the ProDOS MLI (`$BF00`,
GET_PREFIX `$C7` and READ_BLOCK `$80`), reads `DEVNUM` (`$BF30`), and calls
Monitor `SETKBD`/`SETVID`. On the 8fish disk ProDOS has never run. So **`init`
is replaced**, and this is the one real (small) source modification:

- What init actually *does* for the floppy path: pokes slot<<4 into ~14
  self-modifying sites (`unrseek+1`, `unrdrvon*+1`, `unrread*+1`, …), turns
  the motor on, and calls `readadr` once to learn the current track
  (`curtrk`). Everything else is prefix/directory discovery we don't need.
- Our replacement, run by the **copier at boot** (stage 2 delivers the
  driver blob): poke the slot bytes from `$2B` (the boot ROM's slot<<4,
  already a chain-load precondition), and seed `curtrk` in the held zp image
  **directly from the SD loader's `$41`** — at that moment the motor is still
  on and the head position is exactly known. Zero disk activity, ~40-60 B.
- The patch-site addresses are not hand-copied: `acme -l` emits the label
  file, and a small generator (`cmd/genrwts`, §6) turns it into an `.inc` of
  offsets plus the extracted driver blob, so upstream drift breaks the build,
  not the disk.
- The transient R/W driver gets the same treatment at materialization time
  (poke slot from the saved boot slot; `curtrk` from the resident driver's
  held image).

After init, the drivers are self-contained RWTS replacements: motor
on/off, phase stepping with proper delays, address-field sync (`readadr`
retries until the wanted sector arrives, which absorbs spin-up), and — with
`check_chksum=1` — checksum-verified reads. This is precisely the machinery
the hard rule says never to hand-roll, taken from the person who already got
it right.

---

## 3. Feature 1 (PRIMARY): the big opening book in the idle TT

### 3.1 The idea, restated against the real memory map

The TT is aux `$4000-$BFFF`, 4096 x 8 = 32,768 B (`asm/defs.inc` TTBASE
= $4000), and it is written **only by search** (`ttstore`) — never during book
play, because a book move does no searching (`m8engine`: probe hit → play, no
`uidrive`). So from boot until the first out-of-book move, the entire window
is dead freight. The big book borrows it:

```
game start        aux $4000-$BFFF = the BIG BOOK (loaded from disk)
in book           bookprobe reads it; every hit plays instantly, no search
first real search ttstore begins overwriting it   -> the book is gone
                  BIGBOOKOK latch closes          -> never probed again
New Game          if latch closed: reload from disk (~5 s), reopen latch
```

### 3.2 Capacity and format

| | today (resident) | big book |
|---|---|---|
| home | aux `$0800-$1E48` | aux `$4000-$BFFF` |
| size | 5,705 B | up to 32,768 B |
| entries (9 B stride: key4/from/to/flags/weight/nameID) | **633** | **3,640** = (32,768 − 8) / 9 |
| lines | 179 | ~5.7x the raw entry budget |
| name table | LC bank 2 `$D000-$D6A5`, ≤255 names | **the same table, shared** |

Format identical (`'BK'` header, sorted entries, binary search) so
`internal/book`'s compiler, the Go probe, and the asm probe are reused, and
`TestBookProbeParityASMvsGo` extends to the big blob mechanically. The one-byte
`nameID` caps names at 255; `cmd/genbook` gains a second output
(`bigbook.bin`) whose lines inherit names from their nearest named ancestor —
deep lines display the family name, which is correct opening practice anyway.
Add a 16-bit checksum to the big blob's header spare (the resident blob keeps
its layout untouched).

**The resident book stays exactly as it is.** It is the floor: always present,
never overwritten, probed whenever the latch is closed. The big book is a
superset; while the latch is open only the big book is probed.

### 3.3 The probe change (the one engine-image change)

`bookentry`/`bookprobe` (`asm/book.s`) bake `BOOK_BASE` as a constant. Change:
the probe takes its base page from a RAM byte (say `BOOKPG`, default
`>BOOK_BASE`), which the UI sets to `$40` while the latch is open. ~20-30 B in
the engine image (811 B spare to the `$BFF0` trap). `bkfetch`/`bkhdr` already
take arbitrary aux addresses, and the TT never probes below `$4000`, so
nothing else moves. Gate: `TestMicroAB` byte-identical (the harness loads no
book — the probe path is reached only via `bookentry`), probe parity run
against **both** bases, blob-integrity check after every probe as today.

### 3.4 The latch, and its interactions

`BIGBOOKOK`, one byte in the `$F7B6+` block:

- **Set** only by a verified load: `'BK'` magic + entry count sane + checksum
  over the blob (glue at `$E000` reads aux directly with RAMRD on — 32 KB at
  ~10 cycles/byte ≈ 0.3 s, DERIVED — instruction fetches at `$E000` are not
  remapped by RAMRD).
- **Cleared** at `mesearch` (`asm/m8.s` line 1144), the exact instruction
  where "out of book" becomes true — one `sta`. Also cleared on any load
  failure, and at boot until the first load verifies.
- **Takeback** back into book territory: the latch stays closed (the TT has
  been written; the big book is gone). The probe falls back to the resident
  book, which is intact, so behaviour degrades exactly to today's. No special
  case.
- **Pondering** (`docs/ponder-design.md`): a ponder search writes the TT.
  `m8ponder` therefore no-ops while the latch is open. In book the engine
  answers instantly anyway; the only loss is TT warmth for the *first* real
  search, which is the price of the feature and is called out here rather
  than discovered later. One `lda BIGBOOKOK / bne` in `m8ponder`.
- **The first search does NOT clear the TT.** Today the first search of every
  boot already runs against arbitrary aux power-on garbage — that is the
  shipped, gated semantics (`TestDiskPlays`). Book bytes are the same class:
  `ttfetch` verifies 24 hash bits and `ttmovevalid` validates any TT move
  against the generator before use (`asm/search.s` FT2_GENDEFER), so an
  aliased hit (expected ≈ probes x 2^-24 ≈ well under 1/game, DERIVED) is
  ordering noise, not corruption. An explicit 32 KB aux clear at latch-close
  would cost ~0.16 s once per game and make the state intentional; it is
  priced as an option, not required, and per "no relying on bugs" the
  decision (garbage-start is the defined semantics vs. clear-on-close) should
  be written into the code either way as a comment plus a gate.

### 3.5 Load and reload flows, with costs

| event | flow | cost (DERIVED from 36.8 ms/sector measured SD rate) |
|---|---|---|
| boot | board paints at 7.13 s exactly as today; then glue shows `LOADING OPENING LIBRARY...` on the status row and reads the book | ~5 s (128 sectors + seeks + 0.3 s aux copy + 0.3 s checksum); input opens at ~12 s |
| New Game, latch open (game never left book) | nothing — the book is intact | 0 |
| New Game, latch closed | same load with status row | ~5 s |
| mid-game | never — loads happen only at game boundaries | — |

Read as 4 files (`BOOK.0`-`BOOK.3`, 8,192 B each, saplings): each read lands
at `$2000` (override_adr), glue copies it to aux `$4000 + 8K*i` via RAMWRT.
Four opens cost four directory scans of one block each — noise. No
`enable_seek`, no `readseq`, no tree files, which is what keeps the resident
driver at 658 B of code.

Failure (drive empty, disk swapped, bad read): `status`/checksum fails, the
latch stays closed, one message (`8FISH DISK NOT FOUND - PLAYING FROM SMALL
BOOK`), and the game proceeds on the resident book. The feature can only ever
degrade to today's shipped behaviour.

### 3.6 Fallback variant if the driver is cut: SD stage 3

If only feature 1 were wanted with zero resident bytes: the boot chain gains a
stage 3 through the existing `$0802` re-entry (mid-boot, preconditions all
live, mechanism proven twice), staging 8 KB chunks through main and copying to
aux. Boot-to-board becomes ~11.9 s (+4.7 s, DERIVED), and the **only** reload
is `Q`-style reboot; games 2+ after leaving book run on the resident book
until then. Strictly worse product than the resident reader (slower boot,
no reload, still no saves) for ~960 B saved. Documented because it is the
cheapest possible big-book and needs nothing from ProRWTS2 at all.

### 3.7 Expected value, honestly

The 2026-07-28 widening (3,866 → 7,407 B) measured **+3 ± 10 Elo** — book
breadth is an instrument-limited Elo lever. The big book's value is product
value: 3,640 entries of named, curated opening play, variety across games,
and never being dumb in a mainline — plus the structural win that it costs
zero resident bytes during the middlegame. Do not gate this feature on Elo;
gate it on correctness and probe parity (per the Sargon-is-a-periodic-
benchmark and instrument-limit lessons in `docs/results.md`).

---

## 4. Feature 2 (SECONDARY): save/load games

### 4.1 What a saved game is

The UI already stores the whole game as three parallel byte arrays plus
scalars, and takeback already **replays from the start position** through them
(`asm/ui.s` lines 150-152 and the history comment). A save is therefore a
dump, and a load is a replay — no new game-state serialization concept:

| field | source | bytes |
|---|---|---|
| magic `8F`, format version | — | 3 |
| `UIHCNT`, `UIHUMAN`, `UILEVEL`, `UIRESULT`, `UIDHGR` | M8VARS | 5 |
| reserved | — | 8 |
| from / to / flags per ply | `UIHFROM/UIHTO/UIHFLAG` ($F800-$FAFF) | 3 x 255 = 765 |
| checksum (16-bit over all of the above) | computed | 2 |
| **total** | | **783 used, padded to 1,024 = 2 blocks** |

Not saved, rebuilt by replay: `PIECESQ`, the Zobrist hashes (`UIHASH0-3`),
`UISEEN`, castling/ep state — the load path is `uinew` + apply each recorded
move through the same `uifind`/`uiplay`/`uiapply` the game itself uses, so a
loaded game is *by construction* a position the referee reached (and a
corrupted record fails validation at the first bad ply, with the checksum
catching it before that). Games past 255 plies degrade exactly as the live
game does (`UIHFULL`): the save records the first 255 plies, and the UI says
so rather than pretending.

### 4.2 The placeholder-file model

`enable_write`: *"file must exist already and its size cannot be altered;
writes occur in multiples of block size."* So the disk ships **three
pre-formatted slot files** (`SAVE.1`-`SAVE.3`, 1,024 B each, valid-checksum
empty records), overwritten in place. The save UI is a slot picker, not a
filename prompt — which on a 40-column Apple IIe screen is the better UI
anyway. Slot rows show `EMPTY` or `PLY 37 - YOU ARE WHITE - LEVEL 5` read via
the resident driver (2 blocks each, instant).

### 4.3 The save flow (the on-demand part)

1. User picks a slot. Glue stages the record at `$1700`, checksums it.
2. **Resident reader loads the R/W driver file** (`RWTS.RW`, the pre-relocated
   1,114 B blob + patch table, 3 blocks) to `$0E00`; materializer pokes slot
   bytes and seeds `curtrk` from the resident driver's held image. (~100-150 B
   of UICODE glue, DERIVED.)
3. R/W driver writes 2 blocks over the slot file, in place. `detect_wp`
   reports a write-protect tab as a message instead of a mystery.
4. **Verify**: resident reader reads the slot back and compares against the
   staged record — a full read-after-write check for the cost of one 2-block
   read (~0.2 s).
5. The whole `$0E00-$1EFF` window is forgotten. Nothing write-capable
   remains in RAM.

Total ~2-3 s (DERIVED). Load is steps 1-2's read side plus replay: resident
reader only, no R/W driver, no transient anything.

### 4.4 Why the writer must not be resident

The R/W build's 1,280 B page-rounded footprint fits nowhere reasonable:
bank 1's tail is 1,048 B (too small), and bank 2 placement at `$DB00` would
cap UIDATA2 growth at **27 bytes** — the next string added to the UI would
collide. Transient placement in `$0E00` costs zero resident bytes and runs in
RAM that is provably dead during command dispatch. This is zellyn's
load-on-demand instinct, validated: it just needs the resident reader under
it (§1.3).

---

## 5. The disk layout

### 5.1 Two formats, one disk, no fight

Standard Delivery keeps tracks ~0-12 exactly as today (187 of 560 sectors;
boot untouched, 7.13 s preserved, `TestDiskRoundTrip` unchanged). The
ProRWTS2 region lives in the free tracks above. The key finding (read from
source): the floppy driver walks a **directory chain and index blocks** whose
*starting block we control* in our replacement init, and with fixed-size
in-place writes it **never reads the volume bitmap or blocks 2-6 at all**. So
the "ProDOS filesystem" 8fish needs is ProDOS-shaped file structures at a
block number of our choosing — say block 208 (track 26) — and the classic
collision between ProDOS metadata (blocks 2-6 = track 0) and SD's stage-1
sectors **never happens**. Zero junk reads, zero boot cost, zero changes to
the SD region.

| region | contents | sectors |
|---|---|---:|
| tracks 0-12 (as today) | SD boot + stage 1 + stage 2 (+ driver blob, +4) | ~191 |
| ProDOS-shaped region | directory chain (1 block) + index blocks (7) | 16 |
| | `BOOK.0`-`BOOK.3` (4 x 16 blocks) | 128 |
| | `RWTS.RW` (3 blocks incl. patch table) | 6 |
| | `SAVE.1`-`SAVE.3` (3 x 2 blocks) | 12 |
| **total** | | **~353 of 560; ~207 spare** |

Costed alternative — a **real, mountable ProDOS volume** (directory at blocks
2-5, bitmap at 6, SD's tracks marked used): needs ~10 stage-1 page-table
entries pointed at a junk page so the loader discards the sectors backing
blocks 2-6 (~0.4 s boot, DERIVED), plus bitmap bookkeeping in the build. Buys
the ability to pull saves off with ProDOS tools. Recommended: **custom
directory block; revisit only if save interchange ever matters** — the SD
region already makes the disk a non-standard artifact.

### 5.2 Pipeline changes (`internal/delivery`, `cmd/mkdsk`)

All Go, all gated:

- A `prodosish` builder: emits directory entries (storage type, name,
  key pointer, EOF, blocks-used), sapling index blocks, and data blocks at
  chosen physical sectors, honoring ProRWTS2's own block→track/sector
  translation (its floppy code reads physical address fields and applies the
  ProDOS skew — the builder must place block halves where the driver's
  translation expects them, and the gate below *executes* the driver to prove
  it rather than trusting the skew table transcription).
- `Build()` writes the region after stage 2; the ledger gains a third line
  (`TestDiskLedger` prints ProDOS-region sectors and fails on overlap with
  the SD span list).
- `cmd/genbook` emits `bigbook.bin` + its `.inc`; `cmd/genrwts` (new, small)
  runs ACME, extracts the two driver blobs and the patch-site tables from the
  label file, and fails the build if any expected label vanishes upstream.

---

## 6. Toolchain and build integration

- **Vendor `PRORWTS2.S`** (BSD-3-Clause header kept intact) under
  `vendor-asm/` or `asm/third_party/`, pinned by commit hash in a comment and
  in `cmd/genrwts`. ACME: the working binary already lives at
  `~/gh/a2audit/bin/acme` (a2audit's `build` script uses it); the Makefile
  takes `ACME ?=` with that path documented, alongside the existing ca65
  toolchain — ProRWTS2 is the only ACME input, linked to nothing (its output
  is a data blob to ca65/mkdsk, so the two toolchains never meet).
- **The measured patch set** (kept as one `.patch` file; each item verified
  by assembling during this design):
  1. **Timed-loop alignment pad.** `enable_write=1` floppy builds fail
     upstream-clean with `!serious "loop4 crosses a page"` — the 32-cycle
     nibble-write loops are page-crossing-checked, and 8fish's option combo
     shifts them badly. A `!fill 28` immediately before `encsec` (an
     `rts`-preceded routine entry — no fallthrough) assembles cleanly.
     MEASURED: pads 1-27 and 32 fail, 28 passes.
  2. **Combo holes.** Two `!if` guards reference symbols their configuration
     never defines (`encbuf` in the seek-offset path at line ~1348,
     `hddencbuf` in the HDD write path) — dummy defines, dead code either
     way (we discard the HDD driver and never seek).
  3. **`init` replacement** (§2.3): the MLI/prefix/DEVNUM discovery becomes
     a no-op behind an `!if` (`NO_PRODOS = 1`), keeping the slot-poke and
     drive-on logic reachable from our copier-side init.
  4. **Buffer placement**: `dirbuf`/`encbuf` assignments overridden to the
     main-RAM scratch addresses (upstream explicitly supports moving them).
- Per the adversarial-review memory: items 1 and 2 are candidates to
  upstream (they are generic "this combo doesn't assemble" fixes); item 3 is
  8fish-specific.

---

## 7. Gates (new; every one mutation-checked before it counts)

| gate | proves |
|---|---|
| `TestBank1DriverLayout` | driver region added to the bank-1 region list; overlap with LCCODE/DHTILES/tables is a failure, and the m8.cfg D2 cap keeps UIDATA2 out of `$DC00`+ **as a link error** |
| `TestDriverReads` | boots the shipping disk from `$C600`, calls the resident driver through the glue, reads a known file, compares bytes — the driver *executes* in the emulator (DiskCard slot-6 harness already exists: `internal/ui/diskboot.go`) |
| `TestZPProtocol` | all 256 zp bytes snapshot-equal around a driver call; mutation: drop one restore → named byte |
| `TestBigBookLoads` | after boot + load, aux `$4000` has magic/count/checksum; probe parity ASM-vs-Go over the big blob at base `$40` AND the resident blob at base `$08` |
| `TestOutOfBookLatch` | play out of book → `BIGBOOKOK` clear; subsequent probes hit the resident book only; New Game reload reopens it; mutation: remove the `mesearch` clear → big-book probe reads TT-mangled bytes and the test names it |
| `TestPonderRespectsTheLatch` | `m8ponder` no-ops in book (TT bytes at `$4000` untouched across a human think in book) |
| `TestSaveLoadRoundTrip` | play N plies on the booted disk, save to a slot, cold-reboot, load, FEN-equal to a refchess replay of the same moves; slot bytes verified **on the .dsk image itself** |
| `TestWriteTouchesOnlyTheSlot` | byte-compare the whole .dsk before/after a save: the 2 slot blocks differ, **nothing else does** — the anti-corruption gate the write path must never lose |
| `TestSaveDegradesHonestly` | wrong-checksum record refuses to load with a message; write-protected disk reports, doesn't hang |
| ledger | `TestDiskLedger` extended with the ProDOS-region line and SD-overlap check |

Existing gates that must stay green untouched: `TestMicroAB` vs
`microABGolden` (engine tree unchanged — the probe-base change is data-driven
and bookless runs never reach it), `TestBookProbeParityASMvsGo`,
`TestDiskBoots`/`TestDiskPlays`/`TestDiskBoardParity`, the videoscan suite,
and the `internal/delivery` layout suite.

---

## 8. Go/no-go, costs, and order

### Feature 1 — big book: **GO**

| cost | amount | against budget |
|---|---|---|
| LC bank 1 | 960 B resident driver | of 1,048 free → 88 B left (gated) |
| UICODE glue (driver calls, zp swap, load/verify, latch, status) | ~300 B (DERIVED) | of 1,075 free |
| UIDATA2 strings | ~60 B | of 1,307 free (driver no longer competes for bank 2) |
| M8VARS | ~50 B | of 74 free |
| engine image (probe base byte) | ~20-30 B | of 811 spare |
| disk | ~150 sectors | of 373 free |
| boot-to-board | unchanged 7.13 s | first input ~12 s on game 1 / post-book New Games |
| complexity/risk | ACME + vendored source + patch set; custom init; zp protocol | each individually small and gated; the driver itself is the most battle-tested code on the disk besides the boot ROM |

### Feature 2 — save/load: **GO, sequenced second**

| cost | amount |
|---|---|
| resident | **0 B** beyond feature 1's reader |
| UICODE (slot UI, marshal/checksum, replay-load, materializer) | ~430 B (DERIVED) — cumulative glue ~730 of 1,075, **the tightest budget in the design**; the arrow-keys work spends from the same pool, so re-measure before committing |
| UIDATA2 strings | ~100 B |
| disk | 18 sectors (slots + R/W driver file) |
| risk | the write path meets real hardware — mitigated by: never-hand-rolled driver, in-place fixed-size writes only, read-back verify, `TestWriteTouchesOnlyTheSlot`, `detect_wp` |

### Build order

1. Toolchain + vendored driver + patch set + `cmd/genrwts` + `TestDriverReads`
   (the driver demonstrably reads a file on the booted disk — everything else
   stacks on this).
2. Big book: genbook output, probe base byte, latch, load/reload flows, gates.
3. Saves: placeholder files, record format, transient writer, slot UI, gates.
4. (Independent, anytime) decide garbage-TT-start vs clear-on-latch-close and
   write the decision down in `asm/tt.s` with its gate.

### What would kill or defer it

- **UICODE pressure**: if glue estimates run over and the pool empties, the
  driver glue can move its cold half behind a bank-2 window like UIDATA2
  readers do — priced escape hatch, not a blocker.
- **Real-hardware surprises**: everything here executes in the emulator
  gates, but the write path especially should be exercised on the real IIe
  early (a sacrificial disk and `TestWriteTouchesOnlyTheSlot`'s image
  compared after the fact).
- **Upstream drift**: pinned commit; `cmd/genrwts` fails loudly.

---

## 9. Open questions (deliberately left open)

1. **Measured load time.** The ~5 s big-book figure is derived from SD's
   36.8 ms/sector; ProRWTS2's floppy throughput with our block placement
   should be measured in the emulator the day `TestDriverReads` exists, and
   the status line's wording sized to the truth.
2. **Clear-on-latch-close** (§3.4): correctness argument says optional;
   intentionality says decide and gate it. Small either way.
3. **Save-slot metadata row** (date? move count only?) — the IIe has no
   clock (`docs/ui-design.md` §6.2), so slots can show ply/side/level but
   never "when". Product call.
4. **Mountable-volume variant** (§5.1): revisit only if anyone actually asks
   to extract saves with ProDOS tools.
5. **Driver bank discipline check**: the relocated floppy driver is expected
   not to touch LC bank switches at run time (init-only concern upstream);
   `TestDriverReads` should assert bank 1 read+write is still selected on
   return, same shape as `TestBookNameRestoresBank1`.

---

## 10. Feature 1 implementation deltas (2026-08-08, measured)

What shipped differs from the design above in exactly these places; each was
a measurement replacing an estimate, or a densification the UICODE budget
forced. Everything else is as specified.

| design said | shipped | why |
|---|---|---|
| resident driver 658 B code / 960 B page-rounded | **495 B code / 724 B blob**, `$DC00-$DED3` | `use_smartport=0`: there is no SmartPort init path to keep once `init` is replaced (−163 B) |
| capacity 3,640 entries | **3,639** | the load-verify checksum trailer (§3.4's "checksum in the header spare" does not fit an 8-byte header that is already full; it lives in the window's last 2 bytes instead, with FIXED bounds — which also deleted ~70 B of count·9 arithmetic from the verify loop) |
| glue ~300 B UICODE | **374 B section + ~30 B hooks**, 10 B headroom left | measured; two densifications paid for it (fixed-window checksum; the slot-poke site table appended to the driver blob so `m8rwtsinit` reads it from bank 1) |
| copier-side init in the copier | split: the copier (255/256 B full) saves `$2B`/`$41` into payload bytes; **`m8rwtsinit` in UICODE** does the install at `m8main` time | the boot page had 19 B free; the staged blob at `$1D00` is intact until the first search, so the install can run from the payload |
| driver blob staged "+4" in stage 1 | **stage 2, 3 sectors at `$1D00`** (abutting the name table) | stage 2 is where every other lifted blob lives; the `$1D00-$1FBF` hole below the book landing was free |
| load ~5 s, input ~12 s | **+8.95 s load, input ~16.4 s** (board at 7.40 s) | ProRWTS2 reads 2-sector ProDOS blocks with address-field sync between; the 36.8 ms/sector SD figure was the wrong model. Acceptable: it is once per game 1 / post-book New Game only |
| `dirbuf` at `$1300` | as designed | one structural patch, anchored exact-match in cmd/genrwts |
| open question 1 (measured load time) | answered above | |
| open question 2 (clear-on-latch-close) | **garbage-start kept** as the defined semantics | the same `ttfetch` 24-bit verify that guards power-on garbage guards book leftovers; written down at `mesearch`'s close |

The book REGION note in §5 held exactly: directory key block 208, sapling
index blocks, fixed in-place files, zero track-0 metadata, zero boot cost.
`TestBlockOffsetsMatchTheCanonicalSkew` pins the builder's block→sector
arithmetic against the published ProDOS/DOS-3.3 table, and `TestDiskBigBook`
executes the driver against the real nibblised disk.

---

## Appendix: what was verified how

| claim | method |
|---|---|
| driver sizes (658+192 RO, 858+256 R/W, pad 28) | assembled `PRORWTS2.S` (upstream master, 3,928 lines) with a2audit's ACME binary, option lines patched to the §2.1 configs; `verbose_info=1` size dump read from assembler output |
| `enable_write` floppy combos fail upstream-clean | assembled: default+write fails `loop4 crosses a page`; pads 1-27/32 fail, 28 passes |
| `init` requires ProDOS (MLI $C7/$80, DEVNUM $BF30) | read from source lines 249-330 |
| init learns the track by reading an address field with the motor on | source: `unrdrvon1: lda MOTORON / jsr readadr / lda curtrk / sta trackd1` |
| zp usage $3C-$67, API $50-$59 | source zp equate block |
| buffers: dirbuf always; encbuf only for floppy writes (`aligned_read=1`); treebuf only for tree files | source buffer-assignment block near EOF |
| chain-load preconditions and their post-game destruction | `asm/m8.s` 136-147, `internal/delivery/delivery.go` package doc, `asm/m8.cfg` ($0800 = PIECESQ) |
| bank/segment free-space numbers | built `make m8`: `__UICODE_SIZE__` $12CD (4,813), `__UIDATA2_RUN__` $D700, `__UIDATA2_SIZE__` $3E5 (997); `asm/m8.s` bank-1 map; `TestLanguageCardBank1Layout` |
| TT geometry and validation | `asm/defs.inc` TTBASE $4000, 4096x8; `asm/tt.s` 24-bit verify; `asm/search.s` `ttmovevalid` |
| history arrays / replay-based takeback | `asm/ui.s` 150-166 and the history comment ("Takeback replays the game from the start position") |
| ponder state and its TT writes | `docs/ponder-design.md`; `asm/ui.s` PONDER* block |

---

## 11. Feature 2 implementation deltas (2026-08-12, measured)

Re-validated first (docs/saveload-feasibility.md — the design predated
allow_aux, the aux $0200 dirbuf, and the UICODE budget's exhaustion), then
built. What shipped differs from §4 in exactly these places:

| design said | shipped | why |
|---|---|---|
| write driver 1,114 B packed, 28-byte pad | **1,024 B exactly** (692 code + 256 data), no pad needed | use_smartport=0 and no aux path; the timed loops happen to land page-clean at this config/reloc. The slot-site tables ride in the code/data gap, so the blob stays one 2-block file |
| ~430 B of save/load glue in UICODE | **the glue is a THIRD transient payload** (asm/saveload.s, the SAVELOAD file, loaded to $1A00 on demand); only two ~40 B stubs are resident, funded by moving the boot-only m8bookaux/copyx/m8rwtsinit into the boot-transient SPLASH segment | UICODE measured 15 B from full — the §8 "re-measure before committing" warning fired. Main-RAM transient code also dodges every bank-discipline hazard the bank-2 escape hatch had |
| three save slots, slot-picker UI | **one slot** (`SAVE`), W saves / O loads | the single-slot model is the design's own §4.2 fallback; three slots is UI and directory growth with no new machinery, priced for later |
| dirbuf/encbuf at $1300/$1500 | dirbuf $1200 (assembler-natural, asserted), **encbuf $1400 by a required patch**: upstream's fast_subindex=0 branch ALIASES encbuf onto dirbuf, which corrupts any multi-block write from a user buffer (the first sector's nibble staging overwrites the block list) | found in Phase-1 source reading; the disk-integrity gate demonstrates the corruption when the patch is removed (a genuine stray-sector write) |
| save = header + arrays, checksum trailer | as designed, but PAGE-ALIGNED arrays (from/to/flags at record +$100/+$200/+$300) and the checksum in the header ($00E) — and only UIHCNT plies are copied, the tail zeroed | one absolute-indexed loop per array on the 6502; determinism (past UIHCNT the live arrays hold the spent splash code) |
| load = replay through takeback's machinery | replay goes through **uifind + uitrylegal + uiapply** (the typed-move path, not cmd_take's trusting one) | a checksummed-but-forged record must fail at the first illegal ply and leave a clean new game; gated |
| — (not priced) | the write driver's slot pokes come in TWO shapes: ten $C08x operand low bytes (OR) and four raw slot<<4 immediates (unrslot1-4, STORE) | enable_write's timed loops carry the slot as an immediate; cmd/genrwts emits both tables and verifies both operand kinds |

Gates: `TestDiskSaveLoadRoundTrip` (the whole pipeline on the real nibblised
disk, through the Disk II card's write path; ONLY the SAVE file's 4 sectors
of 560 may change; the written record is byte-identical to
internal/saveload.Encode; the position restores same-session and after a
cold reboot from the written medium) and `TestDiskLoadDegrades` (empty slot,
checksum-corrupt record, forged-legal-checksum record). Five mutations were
run against them — encbuf re-aliased, write aimed at BOOK0, a header byte
dropped, checksum validation skipped, replay validation skipped — and each
was caught by name.
