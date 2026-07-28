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

.PHONY: all hello perft banktest entropytest uitest m8 engine tables dsk test test-siblings clean

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

M8_SRCS = asm/m8.s asm/ui.s asm/entropy.inc asm/defs.inc asm/book.inc \
          asm/engsyms.inc asm/m8.cfg

asm/m8.bin: $(M8_SRCS)
	cd asm && $(CA65) -g m8.s -o m8.o
	cd asm && $(LD65) -C m8.cfg m8.o -o m8.bin -Ln m8.lbl

# The STANDARD DELIVERY layout of the same object file: the copier at $0C00
# and the payload staged at $0D00, so the one contiguous image the boot loader
# delivers ($0C00 to the end of engine.bin) fits diskii mksd's 44 KB cap. The
# payload is byte-identical to m8.bin -- nothing in it depends on where it was
# staged -- and internal/ui's TestDiskLayout asserts that.
# Its own object file, not m8.o: two links of one source must not share
# intermediates (see internal/asmbuild's withBuildLock on why that matters).
asm/m8sdboot.bin: $(M8_SRCS) asm/m8sd.cfg
	cd asm && $(CA65) -g m8.s -o m8sd.o
	cd asm && $(LD65) -C m8sd.cfg m8sd.o -o m8sd.bin -Ln m8sd.lbl

# The bootable disk. `diskii mksd` (bit.ly/a2diskii) writes peterferrie's
# Standard Delivery boot loader; without it there is no disk, so say so
# instead of producing nothing and exiting 0.
dsk: asm/8fish.dsk

asm/8fish.dsk: asm/m8sdboot.bin asm/m8.bin asm/engine.bin internal/book/bookblob.bin \
               cmd/mkdsk/main.go internal/delivery/delivery.go
	@command -v $(DISKII) >/dev/null 2>&1 || { \
	  echo "$(DISKII) not found on PATH: it builds the Standard Delivery boot disk." >&2; \
	  echo "  go install github.com/zellyn/diskii@latest" >&2; \
	  exit 1; }
	go run ./cmd/mkdsk

asm/tables.s: cmd/gentables/main.go cmd/gentables/pesto.go
	go run ./cmd/gentables

asm/perft.bin: asm/perft.s asm/board.s asm/movegen.s asm/defs.inc asm/tables.s asm/perft.cfg
	cd asm && $(CA65) perft.s -o perft.o
	cd asm && $(LD65) -C perft.cfg perft.o -o perft.bin -Ln perft.lbl

ENGINE_SRCS = asm/engine.s asm/search.s asm/tt.s asm/eval.s asm/board.s \
              asm/movegen.s asm/book.s asm/book.inc asm/defs.inc \
              asm/tables.s asm/engine.cfg

asm/engine.bin: $(ENGINE_SRCS)
	cd asm && $(CA65) -g engine.s -o engine.o
	cd asm && $(LD65) -C engine.cfg engine.o -o engine.bin -Ln engine.lbl

test:
	go build ./...
	go test ./...

# The sibling checkouts (go6502, goapple2) have their own test suites;
# run them too when present.
test-siblings:
	@if [ -d ../goapple2 ]; then (cd ../goapple2 && go test ./iie/); fi
	@if [ -d ../go6502 ]; then (cd ../go6502 && go test ./...); fi

clean:
	rm -f hello/hello.o hello/hello.bin asm/*.o asm/*.bin asm/*.lbl asm/*.dsk asm/*.img
