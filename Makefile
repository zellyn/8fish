# chess6502 build. Requires ca65/ld65 (brew install cc65) and Go.

CA65 := ca65
LD65 := ld65

# Warn (once per make run) if the toolchain drifts from the tested version.
TESTED_CC65 := V2.18
CC65_VERSION := $(shell $(CA65) --version 2>&1 | head -1 | awk '{print $$2}')
ifneq ($(CC65_VERSION),$(TESTED_CC65))
$(warning ca65 is $(CC65_VERSION); this repo was last tested with $(TESTED_CC65))
endif

DISKII := diskii

.PHONY: all hello perft banktest entropytest uitest m8 engine tables tiles check-tiles pull-art dsk test test-full test-siblings clean

# Pull the CHESS2 board art off a DazzleDraw .po disk image, GEOMETRY-CHECK it,
# and regenerate the tile blob from it. The art is authored on an 800K ProDOS
# volume that `diskii` won't open, so cmd/pullart reads it directly.
#   make pull-art PO=~/Downloads/1652_Dazzle_Draw_v1.2_Apple_IIe.po
# then `make dsk` to rebuild the disk. `gentiles -check` fails the target if the
# new art breaks a geometry assumption, so a bad redraw stops HERE, loudly,
# rather than shipping garbage.
pull-art:
	@test -n "$(PO)" || { echo "usage: make pull-art PO=<path-to-DazzleDraw.po>" >&2; exit 2; }
	go run ./cmd/pullart -po "$(PO)" -name CHESS2 -out assets/chess2-dazzledraw-save.bin
	go run ./cmd/gentiles -check
	go run ./cmd/gentiles
	@echo "art pulled and geometry-checked; tile blob regenerated. Run 'make dsk' to rebuild the disk."

all: hello perft engine test

hello: hello/hello.bin
	go run ./cmd/a2run -bin hello/hello.bin -org 0x2000

hello/hello.bin: hello/hello.s hello/raw2000.cfg
	$(CA65) hello/hello.s -o hello/hello.o
	$(LD65) -C hello/raw2000.cfg hello/hello.o -o $@

perft: asm/perft.bin

banktest: asm/banktest.bin
	go run ./cmd/a2run -bin asm/banktest.bin

entropytest: asm/entropytest.bin

uitest: asm/uitest.bin

# The on-device 8fish user interface: two BLOADable files, m8boot.bin ($0800,
# the run-once copier) and m8.bin (the UI itself, copied to Language Card RAM
# at $E000). Costs the engine image zero bytes; see docs/ui-design.md.
m8: asm/m8.bin

engine: asm/engine.bin

tables: asm/tables.s

tiles: internal/tiles/tileblob.bin

# Read the artwork and report EVERY broken assumption at once, writing
# nothing. This is what you run while REDRAWING the board -- `make tiles`
# stops at the first problem, which is right for a generator and useless for
# a redraw. Exit 0 means the drawing slices and loses nothing.
#
# The same check runs inside `make test` (cmd/gentiles'
# TestCheckOnCommittedArtwork requires exit 0), so this target is the
# convenient spelling, not the gate. A redraw that breaks an assumption fails
# the suite whether or not anyone remembers to type this.
check-tiles:
	go run ./cmd/gentiles -check

asm/banktest.bin: asm/banktest.s asm/banktest.cfg
	cd asm && $(CA65) banktest.s -o banktest.o
	cd asm && $(LD65) -C banktest.cfg banktest.o -o banktest.bin

# Standalone driver that exercises the entropy collector (asm/entropy.inc)
# under the harness emulator; driven by internal/entropy's tests.
asm/entropytest.bin: asm/entropytest.s asm/entropy.inc asm/defs.inc asm/entropytest.cfg
	cd asm && $(CA65) entropytest.s -o entropytest.o
	cd asm && $(LD65) -C entropytest.cfg entropytest.o -o entropytest.bin

# Proof-of-concept image for the on-device UI renderer (asm/ui.s): a $4000
# stub copies the UI into Language Card RAM at $E000 and runs it there.
# Driven by internal/ui's tests.
asm/uitest.bin: asm/uitest.s asm/ui.s asm/defs.inc asm/uitest.cfg
	cd asm && $(CA65) uitest.s -o uitest.o
	cd asm && $(LD65) -C uitest.cfg uitest.o -o uitest.bin

# The UI's symbol bridge: the engine entry points asm/m8.s calls by address.
# A REAL dependency on engine.bin, so the failure mode of an engine refactor
# is a broken build, never a UI linked against stale addresses.
asm/engsyms.inc: asm/engine.bin internal/engsyms/engsyms.go cmd/genengsyms/main.go
	go run ./cmd/genengsyms

M8_SRCS = asm/m8.s asm/ui.s asm/dhgr.s asm/entropy.inc asm/defs.inc asm/book.inc \
          asm/tiledefs.inc asm/tiles.inc asm/engsyms.inc asm/m8.cfg

asm/m8.bin: $(M8_SRCS)
	cd asm && $(CA65) -g m8.s -o m8.o
	cd asm && $(LD65) -C m8.cfg m8.o -o m8.bin -Ln m8.lbl

# The STANDARD DELIVERY layout of the same object file: the copier at $0D00
# and the payload staged at $0E00. -D SDCHAIN adds the CHAIN LOADER to the
# copier -- the code that re-enters the surviving boot loader with a fresh page
# table to read stage 2 -- which only the disk build can have, because only it
# is reached with the loader's sequential read state live. The payload is
# byte-identical to m8.bin (the conditional is confined to the BOOT segment),
# and internal/ui's TestDiskLayout asserts that.
# Its own object file, not m8.o: two links of one source must not share
# intermediates (see internal/asmbuild's withBuildLock on why that matters).
asm/m8sdboot.bin: $(M8_SRCS) asm/m8sd.cfg
	cd asm && $(CA65) -g -D SDCHAIN m8.s -o m8sd.o
	cd asm && $(LD65) -C m8sd.cfg m8sd.o -o m8sd.bin -Ln m8sd.lbl

# The bootable disk. `diskii mksd` (bit.ly/a2diskii) writes peterferrie's
# Standard Delivery boot loader; without it there is no disk, so say so
# instead of producing nothing and exiting 0.
dsk: asm/8fish.dsk

asm/8fish.dsk: asm/m8sdboot.bin asm/m8.bin asm/engine.bin internal/book/bookblob.bin \
               internal/tiles/tileblob.bin cmd/mkdsk/main.go internal/delivery/delivery.go
	@command -v $(DISKII) >/dev/null 2>&1 || { \
	  echo "$(DISKII) not found on PATH: it builds the Standard Delivery boot disk." >&2; \
	  echo "  go install github.com/zellyn/diskii@latest" >&2; \
	  exit 1; }
	go run ./cmd/mkdsk

asm/tables.s: cmd/gentables/main.go cmd/gentables/pesto.go
	go run ./cmd/gentables

# The board tiles are sliced from the hand-drawn DazzleDraw artwork, which
# is the single source of truth. ONE gentiles run writes THREE outputs --
# internal/tiles/tileblob.bin (embedded by package tiles, and committed) and
# asm/tiledefs.inc + asm/tiles.inc, committed generated files exactly like
# asm/book.inc. The generator asserts every geometric assumption against the
# actual pixels, so a re-drawn board fails HERE rather than rendering garbage
# on the IIe.
#
# ONE recipe, THREE outputs, and make must not build a consumer of one before
# the recipe has run. The obvious spelling is a GNU Make grouped target
# (`out1 out2 out3 &: deps`) -- but `&:` needs make >= 4.3 and macOS still
# ships 3.81, which parses it as four INDEPENDENT targets (one of them named
# literally `&`). Their mtimes then float apart, and `make dsk` will assemble
# the copier from a stale asm/tiledefs.inc and then regenerate the blob:
#
#   cd asm && ca65 -g -D SDCHAIN m8.s ...   <- stage-2 page table, OLD sizes
#   go run ./cmd/gentiles                   <- blob regenerated
#   go run ./cmd/mkdsk                      <- disk built from the NEW blob
#
# which ships a disk whose stage-2 page table disagrees with the disk, exits
# 0, and prints a normal-looking ledger. The Go gates cannot catch it: they
# re-assemble from the regenerated .inc, so they never see the artifact `make
# dsk` produced. Verified by reproduction on GNU Make 3.81, 2026-08-01.
#
# So express the same thing in a way 3.81 understands: the blob carries the
# real rule, and the two .inc files depend on the blob with an empty recipe
# (the gentiles run that made the blob already wrote them). Any consumer of a
# .inc therefore forces the blob to be up to date FIRST.
internal/tiles/tileblob.bin: assets/chess2-dazzledraw-save.bin cmd/gentiles/main.go internal/tiles/tiles.go
	go run ./cmd/gentiles

asm/tiledefs.inc asm/tiles.inc: internal/tiles/tileblob.bin
	@:

asm/perft.bin: asm/perft.s asm/board.s asm/movegen.s asm/defs.inc asm/tables.s asm/perft.cfg
	cd asm && $(CA65) perft.s -o perft.o
	cd asm && $(LD65) -C perft.cfg perft.o -o perft.bin -Ln perft.lbl

ENGINE_SRCS = asm/engine.s asm/search.s asm/tt.s asm/eval.s asm/board.s \
              asm/movegen.s asm/book.s asm/book.inc asm/defs.inc \
              asm/tables.s asm/engine.cfg

asm/engine.bin: $(ENGINE_SRCS)
	cd asm && $(CA65) -g engine.s -o engine.o
	cd asm && $(LD65) -C engine.cfg engine.o -o engine.bin -Ln engine.lbl

# Agent worktrees live under .claude/worktrees/, which is INSIDE the repo but
# OUTSIDE the workspace that the root go.work declares (`use (.)` means the
# root, not any worktree of it). So in a worktree every Go command fails with
#
#   pattern ./...: directory prefix . does not contain modules listed in go.work
#
# which reads like a build error rather than a missing config, and made `make
# test` impossible to run for any agent working in a worktree -- another gate
# that could not pass. Generate a worktree-local go.work instead of expecting
# each agent to discover this. --git-common-dir resolves to the MAIN repo's
# .git from inside a worktree, so the sibling checkouts are derivable.
#
# go.work is gitignored, and this rule only fires when the file is absent, so
# it never touches the root workspace.
go.work:
	@root="$$(dirname "$$(git rev-parse --path-format=absolute --git-common-dir)")"; \
	 sibs="$$(dirname "$$root")"; \
	 printf 'go 1.26.2\n\nuse (\n\t.\n\t%s/go6502\n\t%s/goapple2\n)\n' "$$sibs" "$$sibs" > go.work; \
	 echo "generated a worktree-local go.work (the root workspace does not cover worktrees)"

# `test` is the gate you can actually run on every change: -short skips the
# long diagnostics and finishes in about a minute.
#
# It is split from `test-full` because plain `go test ./...` COULD NOT PASS.
# internal/chesstest and internal/mirror take ~49 and ~47 minutes, and Go's
# per-package timeout defaults to 10 minutes, so the target failed every time
# regardless of the code. A gate that always fails is worse than no gate: it
# trains you to ignore it, and this project's whole method rests on trusting
# its gates (see the asmbuild build race, which could turn a corrupt object
# file into a spurious PASS).
test: go.work
	go build ./...
	go test -short ./...

# The complete suite, including the ~50-minute parity and cycle-model
# diagnostics. This is the pre-merge gate. The timeout is per PACKAGE, and
# the two slow ones need ~49 min each, so 90 min leaves real headroom.
test-full: go.work
	go build ./...
	go test -timeout 5400s ./...

# The sibling checkouts (go6502, goapple2) have their own test suites;
# run them too when present.
test-siblings:
	@if [ -d ../goapple2 ]; then (cd ../goapple2 && go test ./iie/); fi
	@if [ -d ../go6502 ]; then (cd ../go6502 && go test ./...); fi

clean:
	rm -f hello/hello.o hello/hello.bin asm/*.o asm/*.bin asm/*.lbl asm/*.dsk asm/*.img
