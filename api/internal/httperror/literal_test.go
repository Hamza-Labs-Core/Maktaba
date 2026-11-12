package httperror

import (
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T03: no Go string literal outside this package names a problem type URI; every type is
// made by Type, TypeForCode or one of the constants, so the registry check and the pages
// see them all. The cloud module (cloud/) is held to the same rule.
func TestNoProblemTypeLiteralsOutsideTheRegistry(t *testing.T) {
	root := repoRoot(t)
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, dir := range []string{"api", "cloud"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == self || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			scanned++
			for _, at := range problemLiterals(t, path) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d: a problem type literal; use httperror.Type(slug), TypeForCode(code) or a constant", rel, at)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 500 {
		t.Fatalf("scanned only %d Go files: is the walk rooted at the checkout?", scanned)
	}
}

// problemLiterals returns the lines of path's string literals that hold the type base.
func problemLiterals(t *testing.T, path string) []int {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "maktaba.tv/problems/") {
		return nil
	}
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	var lines []int
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return lines
		}
		if tok == token.STRING && strings.Contains(lit, "maktaba.tv/problems/") {
			lines = append(lines, fset.Position(pos).Line)
		}
	}
}

// The scan finds a literal, in a backquoted string too, and ignores comments.
func TestProblemLiteralsScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.go")
	src := "package x\n// see https://maktaba.tv/problems/seat-limit\nvar a = \"https://maktaba.tv/problems/seat-limit\"\nvar b = `{\"type\":\"https://maktaba.tv/problems/x\"}`\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := problemLiterals(t, path); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("lines %v, want [3 4]", got)
	}
}
