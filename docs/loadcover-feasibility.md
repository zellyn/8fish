# Feasibility: keep the boot SPLASH up during the big-book load

Status: **PHASE 1 COMPLETE — STOP. An aux-path blocker was found (exactly the
one the task flagged as "would corrupt it"). The core approach is feasible and
the size budgets all fit, but the task's Q2 assumption — "dirbuf stays in main
`$1300`, the driver only toggles `SETAUXWR/CLRAUXWR` around its data stores" —
is FALSE. There is a small, empirically-supported fix, but it reserves a new
aux scratch window (a memory-map decision), so this stops for sign-off rather
than forcing it.**

Goal recap: read each 8 KB book file DIRECTLY into its aux home
(`$4000 + 32*index`) via ProRWTS2 `allow_aux`, dropping the main-`$2000`
staging + `copyx`, so the full-screen splash (DHGR page 1, aux+main `$2000`)
stays visible through the ~9 s load.

---

## Q1 — Does `allow_aux=1` fit? YES (size), with a nuance worth noting.

Measured with a non-destructive trial assembly (ACME 0.97, the 8fish read-only
config from `cmd/genrwts`, only `allow_aux` flipped 0→1; committed files
untouched):

| | `allow_aux=0` (shipping) | `allow_aux=1` |
|---|---|---|
| floppy code | `$DC00-$DDEE` = **495 B** | `$DC00-$DDFB` = **508 B** (+13) |
| nibble tables (`bit2tbl`/`nibtbl`) | `$DE00-$DEBF`, `dataend $DEC0` | `$DE00-$DEBF`, `dataend $DEC0` (unchanged) |
| site table (10×2) appended | `$DEC0-$DED3` | `$DEC0-$DED3` (unchanged) |
| **blob size** | **724 B** | **724 B (SAME)** |
| resident footprint | `$DC00-$DED3` | `$DC00-$DED3` (SAME) |
| `dataend` vs `$DFC0` budget | pass | `$DEC0` — pass |
| new symbols | — | `auxreq=$51`, `setaux=$DC9A`, `trackd1` operand moved `$DD8F`→`$DD9C` |

**Why the blob stays 724 B:** the +13 code bytes land in the dead padding
between `codeend` and the page-aligned `bit2tbl` (`$DE00`). That padding was
17 B (`$DDEF-$DE00`); it becomes 4 B (`$DDFC-$DE00`). `dataend` is fixed at
`$DEC0`, so `genrwts`'s `blob := make([]byte, dataEnd-reloc)` (704) + 20-byte
site table = 724 either way. **The sha WILL change** (13 new code bytes plus
the `setaux` routine and `auxreq` handling); the size and footprint do not.
The 10 slot-poke sites still exist and are extracted by label, so `genrwts`'s
site assertions still hold.

**UICODE glue budget (task #63: ~10 B headroom):** NOT a risk. The aux path
grows the DRIVER (absorbed as above, costs zero resident), not the glue. In
`m8bigbook` the glue **shrinks**: we DROP `RAMWRTON/copyx/RAMWRTOFF` + the
`$40+32*i` aux-page computation + `uidhoff`/`m_loading`/`uipaintmsg`, and ADD
only `auxreq=1` (2–3 B in the driver zp) plus a `SETAUXRD/SETAUXWR` bracket.
Net is neutral-to-negative. (Re-measure `__UICODE_SIZE__` in Phase 2 to be
sure, but there is no plausible way this overflows.)

Verdict Q1: **GO on size.**

---

## Q2 — Does the aux read path work with our driver? NO, not as the task assumes. **BLOCKER.**

The task's model — "the driver toggles `SETAUXWR/CLRAUXWR` around its data
stores while its own scratch (`dirbuf $1300`) stays in main" — does not match
the code. Traced from `asm/third_party/PRORWTS2.S`:

1. **`setaux` switches BOTH read and write, for the whole read loop, not just
   around stores.** For our config (`aligned_read=1`), `copyblock` does
   `ldx auxreq / jsr setaux` (lines 1417-1419) BEFORE `rdwrloop`, and `setaux`
   (line 1615) is `sta CLRAUXRD,x / sta CLRAUXWR,x`. With `x=auxreq=1` that is
   `$C003` (SETAUXRD / RAMRD on) **and** `$C005` (SETAUXWR / RAMWRT on). Aux
   read+write stay on until `rdwrdone` (line 1613, `ldx #0`) turns them back
   off. There is no read-main/write-aux mode: both switches share `auxreq`.

2. **The driver reads its sapling block-list from `dirbuf` MID-LOOP, under
   that aux window.** `skiptree` (line 1556) does `ldx dirbuf,y /
   lda dirbuf+256,y` once per data block, inside `rdwrloop`. `dirbuf` is at
   main `$1300`, which is inside the `$0200-$BFFF` range that RAMRD remaps.
   With RAMRD on, those reads come from **aux `$1300` — garbage** — so the
   driver would seek to wrong blocks and the checksum verify would fail (the
   feature would silently degrade to the resident book, never covering the
   load). The directory/index-block reads during open have the same problem in
   reverse (written to main with aux off, read back under aux).

3. **This is a documented ProRWTS2 constraint, not a surprise.** The option
   header (lines 43-46) says `allow_aux` *"requires load_high to be set for
   arbitrary memory access; else driver must be running from same memory
   target."* Our driver runs from LC bank 1 `$DC00` — outside the switched
   range — so its **code and nibble tables (`bit2tbl`/`nibtbl` at `$DE00+`) are
   safe** (RAMRD/RAMWRT only remap `$0200-$BFFF`; `$D000+` follows the LC/ALTZP
   state). The problem is ONLY that `genrwts` relocated **`dirbuf` to main
   `$1300`** (to save 512 resident bytes; the upstream default `$DF00` would
   collide with UICODE at `$E000`) — and `$1300` is inside the switched range.
   `dirbuf` is the SOLE offending buffer: `encbuf`/`treebuf` are not allocated
   (`aligned_read=1`, no write/trees), and `readbuff` is only in the
   init/rwts_mode code we never assemble.

Verdict Q2: **BLOCKER as specified.** Reading straight to aux with `dirbuf` at
main `$1300` corrupts the load.

### The fix (small, and empirically supported)

Move `dirbuf` out of the switched range into **aux** — the one clean 512-byte
home is **aux `$0200-$03FF`**, which a boot-to-`mloop` dump confirmed is
**all-zero / unused** (resident book is aux `$0800-$1E48`; the 80-col text page
is aux `$0400-$07FF`; the splash is aux `$2000-$3FFF`; the big book is aux
`$4000-$BFFF`). Then, so that the OPEN-phase directory/index reads land in the
same aux `dirbuf` the loop reads back, the **caller must set `SETAUXRD+SETAUXWR`
before `jsr RWTS_ENTRY`** (with `auxreq=1`), and the driver's `rdwrdone`
restores main on return. Zero page (API bytes) follows ALTZP, not RAMRD, so it
is unaffected; the name string (`f_book`, `$F8xx`) and the driver's baked
operands are in the LC range and unaffected.

Cost of the fix: ~1 line in `cmd/genrwts` (`dirbufAddr` → an aux address),
~10 lines in `m8bigbook` (aux bracket + `auxreq`, replacing the dropped
`copyx`), and one NEW gate reserving aux `$0200-$03FF` as driver scratch during
the load (a memory-map contract, like `TestBookClearsTheAuxTextPage`). This is
NOT "dirbuf stays in main" — it changes the aux memory map, which is why this
report stops for sign-off.

---

## Q3 — Boundary/verify: byte-identical? YES (once Q2 is fixed).

`mbbverify` reads aux `$4000-$BFFF` back under `RAMRDON` and 16-bit-sums it
against the trailer — unchanged. Reading each file straight to aux
`$4000+32*i` lands the **same file bytes at the same aux addresses** as today's
stage-to-`$2000`-then-`copyx`-to-`$4000` path; only the route differs. So the
verify sees identical bytes and still passes. **GO on Q3, conditional on the
Q2 fix.**

---

## Overall verdict

- **Size / budget: GO.** Blob stays 724 B (`$DC00-$DED3`), sha changes,
  `engine.bin` untouched, UICODE glue shrinks.
- **Aux path: BLOCKER as specified** — `dirbuf` at main `$1300` is read under
  RAMRD and corrupts the load. The task's Q2 mechanism assumption is wrong.
- **Fix exists and is small** (relocate `dirbuf` to the empirically-free aux
  `$0200-$03FF` + bracket each call with `SETAUXRD/SETAUXWR`), but it reserves a
  new aux scratch window.

**Recommendation:** proceed to Phase 2 using the fix above, with the first
Phase-2 step being the empirical confirmation (build the `allow_aux` blob with
`dirbuf` in aux `$0200`, wire the aux bracket, run `TestDiskBigBook` — it fails
loudly if any of this is wrong). Per the task's "STOP and report an aux-path
blocker with options, do not force it," this is presented for sign-off on the
new aux-scratch reservation before regenerating the committed driver blob.

### Evidence
- Trial assembly (non-destructive): `allow_aux=1` → code 508 B, `dataend
  $DEC0`, blob 724 B; `auxreq $51`, `setaux $DC9A`, `trackd1` operand `$DD9C`.
- Code trace: `PRORWTS2.S` `setaux` (1615), `copyblock` aligned branch
  (1417-1419), `skiptree` dirbuf read (1556), `rdwrdone` (1613), option header
  (43-46).
- Boot-to-`mloop` aux dump: `$0200-$03FF` all-zero; resident book `$0800-$1E48`;
  text page `$0400-$07FF` in use; old `trackd1` operand = `$22` (track 34).
</content>
