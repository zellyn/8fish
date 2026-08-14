// Command a2run is a headless test harness for 6502 binaries targeting the
// Apple IIe 128K memory system. It loads a raw binary into main RAM, jumps
// to an entry point, and runs at full host speed until the program stores
// to the exit trap, the cycle limit is reached, or the CPU hits an unknown
// opcode.
//
// Harness I/O conventions (see docs/testing.md; on real hardware these
// addresses are plain RAM, and $BF00-$BFFF is reserved in both banks):
//
//	store to $BFF0 (main bank): emit the byte to stdout
//	store to $BFFF (main bank): exit; the stored value is the exit code
//
// Traps fire only on main-bank stores; aux writes (RAMWRT on) to the same
// addresses are ordinary memory writes. Reads of $C019 return VBL status
// derived from the cycle counter.
//
// Cycle counts and emulated time (at 1.0205 MHz effective IIe speed) are
// reported to stderr. The cycle limit is checked between instructions, so
// runs may overshoot it by up to 7 cycles.
//
// This command is a thin CLI wrapper: the emulation core lives in package
// harness (github.com/zellyn/8fish/harness), so 6502 binaries can also
// be run in-process from Go tests.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/zellyn/8fish/harness"
)

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func main() {
	var (
		binfile   = flag.String("bin", "", "raw binary to load (required)")
		org       = flag.Uint("org", 0x2000, "load address")
		entry     = flag.Uint("entry", 0xFFFFFFFF, "entry point (default: org)")
		maxcycles = flag.Uint64("maxcycles", 1<<40, "cycle limit")
		coutAddr  = flag.Uint("cout", 0xBFF0, "character-output trap address")
		exitAddr  = flag.Uint("exit", 0xBFFF, "exit trap address")
		romfile   = flag.String("rom", "", "optional 12K $D000-$FFFF ROM image")
		trace     = flag.Bool("trace", false, "print each instruction to stderr")
		dump      = flag.String("dump", "", "hex range start:end to dump to stderr at exit")
	)
	flag.Parse()
	if *binfile == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *coutAddr > 0xFFFF || *exitAddr > 0xFFFF {
		fail("-cout/-exit addresses must be <= 0xFFFF")
	}

	prog, err := os.ReadFile(*binfile)
	if err != nil {
		fail("%v", err)
	}
	if *org > 0xFFFF {
		fail("-org %#x out of range", *org)
	}
	if int(*org)+len(prog) > 0x10000 {
		fail("binary (%d bytes at -org %#x) overruns 64K by %d bytes",
			len(prog), *org, int(*org)+len(prog)-0x10000)
	}
	if *entry == 0xFFFFFFFF {
		*entry = *org
	} else if *entry > 0xFFFF {
		fail("-entry %#x out of range", *entry)
	}

	var rom []byte
	if *romfile != "" {
		rom, err = os.ReadFile(*romfile)
		if err != nil || len(rom) != harness.ROMSize {
			fail("bad ROM file (need %d bytes): %v", harness.ROMSize, err)
		}
	}

	m, err := harness.New(harness.Config{
		Bin:      prog,
		Org:      uint16(*org),
		Entry:    uint16(*entry),
		CoutAddr: uint16(*coutAddr),
		ExitAddr: uint16(*exitAddr),
		ROM:      rom,
		Cout:     os.Stdout,
		Trace:    *trace,
	})
	if err != nil {
		fail("%v", err)
	}

	start := time.Now()
	exited, exitCode, runErr := m.Run(*maxcycles)
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(3)
	}
	wall := time.Since(start)

	emulated := time.Duration(float64(m.Cycles) / harness.EffectiveHz * float64(time.Second))
	speedup := "-"
	if wall > 0 {
		speedup = fmt.Sprintf("%.0fx", float64(emulated)/float64(wall))
	}
	fmt.Fprintf(os.Stderr, "cycles: %d  emulated: %s  wall: %s (%s)\n",
		m.Cycles, emulated.Round(time.Microsecond), wall.Round(time.Microsecond), speedup)
	if len(m.Mem.Unhandled) > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: unhandled soft-switch accesses: %v\n", m.Mem.Unhandled)
	}
	if *dump != "" {
		var lo, hi uint32
		if _, err := fmt.Sscanf(*dump, "%x:%x", &lo, &hi); err != nil || lo > hi || hi > 0x10000 {
			fail("bad -dump range %q", *dump)
		}
		for a := lo; a < hi; a += 16 {
			fmt.Fprintf(os.Stderr, "%04X:", a)
			for b := a; b < a+16 && b < hi; b++ {
				fmt.Fprintf(os.Stderr, " %02X", m.Mem.Peek(uint16(b)))
			}
			fmt.Fprintln(os.Stderr)
		}
	}
	if !exited {
		fmt.Fprintln(os.Stderr, "cycle limit reached")
		os.Exit(4)
	}
	os.Exit(int(exitCode))
}
