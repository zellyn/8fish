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
	"testing"
)

// ErrCA65NotInstalled is returned by Build when ca65 is not on PATH.
// Callers generally want to treat this as a clean skip, not a failure:
// the assembler toolchain is an optional dev dependency.
var ErrCA65NotInstalled = errors.New("ca65 not installed")

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
	for _, s := range steps {
		if err := run(s.dir, s.name, s.args...); err != nil {
			return err
		}
	}
	return nil
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
	if err := run("ca65", ca65...); err != nil {
		return err
	}
	return run("ld65", "-C", "engine.cfg", obj, "-o", out+".bin", "-Ln", out+".lbl")
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
