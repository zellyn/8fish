// Package asmbuild builds the 6502 test/engine binaries from source: it
// regenerates the generated asm tables (cmd/gentables) and then
// assembles/links perft.bin, banktest.bin, and engine.bin (plus their
// .lbl label files) with ca65/ld65. It is the single source of truth for
// that build sequence, shared by internal/chesstest's TestMain and
// internal/ucibridge's tests, both of which need the built binaries
// under asm/.
package asmbuild

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ErrCA65NotInstalled is returned by Build when ca65 is not on PATH.
// Callers generally want to treat this as a clean skip, not a failure:
// the assembler toolchain is an optional dev dependency.
var ErrCA65NotInstalled = errors.New("ca65 not installed")

// lockName is the advisory lockfile, created inside asm/ (gitignored).
const lockName = ".asmbuild.lock"

// buildMu serializes builds within one process.
var buildMu sync.Mutex

// withBuildLock serializes an asm/ build against every other build, in
// this process and in any other.
//
// Every build function here writes its intermediates and outputs to FIXED
// paths under asm/ (engine.o, engine.bin, engine.lbl, ...), because the
// tests that consume them expect them there. `go test ./...` runs packages
// in PARALLEL, and internal/chesstest, internal/ucibridge and others each
// build on startup — so without this, two ca65/ld65 invocations write the
// same object file at once and produce corruption such as
// "ld65: Read error at position 40960".
//
// That mattered more than a flaky build: a half-written object file can in
// principle yield a spurious PASS as easily as a spurious FAIL, and this
// project's entire method rests on trusting its gates. A gate that is
// quietly re-run until green is how a real defect gets through.
//
// A per-process temp directory would remove the shared resource outright,
// but the outputs are the interface — callers read asm/engine.bin — so the
// contention is inherent and serializing is the honest fix. BuildVariant
// writes distinct names, yet still needs the lock: Build regenerates the
// generated .s tables that BuildVariant's ca65 reads.
func withBuildLock(root string, fn func() error) error {
	buildMu.Lock()
	defer buildMu.Unlock()
	unlock, err := lockDir(filepath.Join(root, "asm"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// Build regenerates the generated tables and assembles/links
// perft.bin, banktest.bin, and engine.bin from the asm/ source. root is
// the chess6502 module root, relative or absolute; callers in
// internal/<pkg> pass "../..".
func Build(root string) error {
	if _, err := exec.LookPath("ca65"); err != nil {
		return ErrCA65NotInstalled
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	asm := filepath.Join(root, "asm")

	run := func(dir, name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s (in %s): %w\n%s", name, strings.Join(args, " "), dir, err, out)
		}
		return nil
	}

	steps := []struct {
		dir  string
		name string
		args []string
	}{
		{root, "go", []string{"run", "./cmd/gentables"}},
		{asm, "ca65", []string{"perft.s", "-o", "perft.o"}},
		{asm, "ld65", []string{"-C", "perft.cfg", "perft.o", "-o", "perft.bin", "-Ln", "perft.lbl"}},
		{asm, "ca65", []string{"banktest.s", "-o", "banktest.o"}},
		{asm, "ld65", []string{"-C", "banktest.cfg", "banktest.o", "-o", "banktest.bin"}},
		{asm, "ca65", []string{"-g", "engine.s", "-o", "engine.o"}},
		{asm, "ld65", []string{"-C", "engine.cfg", "engine.o", "-o", "engine.bin", "-Ln", "engine.lbl"}},
	}
	return withBuildLock(root, func() error {
		for _, s := range steps {
			if err := run(s.dir, s.name, s.args...); err != nil {
				return err
			}
		}
		return nil
	})
}

// BuildStandalone assembles and links a standalone asm/<name>.s image
// (with its own asm/<name>.cfg) into asm/<name>.bin — the small
// self-contained test drivers such as entropytest.s, which include only
// defs.inc and one module and so need none of the engine build. It does
// not regenerate the tables.
func BuildStandalone(root, name string) error {
	return BuildStandaloneAs(root, name, name)
}

// BuildStandaloneAs is BuildStandalone with the output name decoupled from
// the source name, plus optional ca65 -D defines: it assembles
// asm/<src>.s with asm/<out>.cfg into asm/<out>.bin / asm/<out>.lbl. The
// config follows the OUTPUT name because a link config also names any
// secondary output files it emits, and two variants of one source must not
// write the same ones.
//
// Needed because a source can have more than one meaningful build — the UI
// (asm/m8.s) is assembled once for real hardware, reading $C000/$C010, and
// once with -D HARNESSKBD so internal/ui can drive it from Go through the
// harness input traps. Two builds of one source must not fight over one
// output name.
func BuildStandaloneAs(root, src, out string, defines ...string) error {
	if _, err := exec.LookPath("ca65"); err != nil {
		return ErrCA65NotInstalled
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	asm := filepath.Join(root, "asm")
	run := func(cmdName string, args ...string) error {
		cmd := exec.Command(cmdName, args...)
		cmd.Dir = asm
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s (in %s): %w\n%s", cmdName, strings.Join(args, " "), asm, err, out)
		}
		return nil
	}
	ca65 := []string{"-g"} // -g so the .lbl carries every label: the UI's
	// tests read routine addresses out of it (to unit-test a single
	// routine) and derive the measured byte budget from label deltas.
	for _, d := range defines {
		ca65 = append(ca65, "-D", d)
	}
	ca65 = append(ca65, src+".s", "-o", out+".o")
	return withBuildLock(root, func() error {
		if err := run("ca65", ca65...); err != nil {
			return err
		}
		return run("ld65", "-C", out+".cfg", out+".o", "-o", out+".bin", "-Ln", out+".lbl")
	})
}

// BuildVariant assembles engine.s with extra ca65 -D defines into a
// distinct binary + label file under asm/, named "<out>.bin"/"<out>.lbl"
// (e.g. out="engine_noptcache", defines=["PTNOCACHE"]). It assumes the
// generated tables are already up to date (Build, run by TestMain,
// regenerates them), so it only re-assembles/links engine.s. Used by
// differential and cycle-measurement tests that need an A/B binary.
func BuildVariant(root, out string, defines ...string) error {
	if _, err := exec.LookPath("ca65"); err != nil {
		return ErrCA65NotInstalled
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	asm := filepath.Join(root, "asm")

	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = asm
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s (in %s): %w\n%s", name, strings.Join(args, " "), asm, err, out)
		}
		return nil
	}

	obj := out + ".o"
	ca65 := []string{"-g"}
	for _, d := range defines {
		ca65 = append(ca65, "-D", d)
	}
	ca65 = append(ca65, "engine.s", "-o", obj)
	return withBuildLock(root, func() error {
		if err := run("ca65", ca65...); err != nil {
			return err
		}
		return run("ld65", "-C", "engine.cfg", obj, "-o", out+".bin", "-Ln", out+".lbl")
	})
}

// BuildT is a testing.TB convenience wrapper around Build: it skips the
// test cleanly if ca65 is not installed, and fails it on any other build
// error. root is the chess6502 module root as Build expects it; callers
// in internal/<pkg> tests pass "../..".
func BuildT(t testing.TB, root string) {
	t.Helper()
	if err := Build(root); err != nil {
		if errors.Is(err, ErrCA65NotInstalled) {
			t.Skip("SKIP: ca65 not installed")
		}
		t.Fatal(err)
	}
}
