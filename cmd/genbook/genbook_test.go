package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zellyn/8fish/internal/book"
)

// TestOpeningsCompileCleanly (deliverable a): the committed SAN source
// parses and compiles with no error — every move legal through refchess.
func TestOpeningsCompileCleanly(t *testing.T) {
	if _, err := parseOpenings(filepath.Join("..", "..", openingsPath)); err != nil {
		t.Fatalf("openings.txt failed to compile: %v", err)
	}
}

// TestCompiledMatchesGeneratedLines (deliverable e): compiling the source
// reproduces the committed generated book.Lines byte-for-byte (same ECO,
// name, and UCI moves). If this fails, lines.go is stale — run
// `go run ./cmd/genbook`.
func TestCompiledMatchesGeneratedLines(t *testing.T) {
	lines, err := parseOpenings(filepath.Join("..", "..", openingsPath))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lines, book.Lines) {
		t.Fatalf("compiled openings.txt (%d lines) != committed book.Lines (%d lines); run `go run ./cmd/genbook`",
			len(lines), len(book.Lines))
	}
}

// TestRefusesMalformed (deliverable c): the compiler refuses a
// deliberately-malformed line, and the error names the opening and move.
func TestRefusesMalformed(t *testing.T) {
	_, err := parseOpenings(filepath.Join("testdata", "bad.txt"))
	if err == nil {
		t.Fatal("compiler accepted an illegal line")
	}
	msg := err.Error()
	for _, want := range []string{"Bogus Opening", "C99", "Ke3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// TestRefusesVariousBad checks the parser rejects a range of malformations
// with a clear error, using throwaway fixtures.
func TestRefusesVariousBad(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"bad ECO", "# X\nZ9: 1. e4\n", "ECO"},
		{"no name", "C20: 1. e4\n", "no name"},
		{"no colon", "# X\nC20 1. e4\n", "ECO: moves"},
		{"illegal move", "# X\nC20: 1. e4 e5 2. Bxf7\n", "Bxf7"},
		{"empty line list", "# X\nC20:\n", "no moves"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "o.txt")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			err := func() (e error) {
				_, e = parseOpenings(p)
				return
			}()
			if err == nil {
				t.Fatalf("accepted malformed input: %q", tc.body)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.want)
			}
		})
	}
}

// TestMoveNumberStripping checks the tolerant tokenizer.
func TestMoveNumberStripping(t *testing.T) {
	cases := map[string]string{
		"1.": "", "12.": "", "1": "", "1.e4": "e4",
		"e4": "e4", "O-O": "O-O", "Bxc3+": "Bxc3+", "e8=Q": "e8=Q",
	}
	for in, want := range cases {
		if got := stripMoveNumber(in); got != want {
			t.Errorf("stripMoveNumber(%q) = %q, want %q", in, got, want)
		}
	}
}
