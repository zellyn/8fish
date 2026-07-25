# chess6502 build. Requires ca65/ld65 (brew install cc65) and Go.

CA65 := ca65
LD65 := ld65

# Warn (once per make run) if the toolchain drifts from the tested version.
TESTED_CC65 := V2.18
CC65_VERSION := $(shell $(CA65) --version 2>&1 | head -1 | awk '{print $$2}')
ifneq ($(CC65_VERSION),$(TESTED_CC65))
$(warning ca65 is $(CC65_VERSION); this repo was last tested with $(TESTED_CC65))
endif

.PHONY: all hello perft banktest entropytest engine tables test test-siblings clean

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
	rm -f hello/hello.o hello/hello.bin asm/*.o asm/*.bin asm/*.lbl
