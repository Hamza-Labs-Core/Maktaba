// Package loopcheck is the catalog check for a Go process's loops (Epic 51, story 51.05,
// F1): it reads the module's source, never runs it, so every declared loop is seen
// whatever the runtime conditions. Used by tests in api/ and streaming/.
package loopcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// Marker is what a bare goroutine start in a start file must carry when it is not a
// loop (a one-shot, a per-request or per-event goroutine).
const Marker = "// not a loop:"

// Plain is a goroutine start that is not the runner: `<Pkg>.<Func>(…)` (Pkg is the last
// identifier before the call: safego, bg). NameArg is the argument with the goroutine's
// name, or -1 when it has none.
type Plain struct {
	Pkg, Func string
	NameArg   int
}

// Spec is one process's check.
type Spec struct {
	Host       string
	Root       string   // the module's directory
	StartFiles []string // globs under Root
	Declare    []string // the runner's declaring methods (Declare, Loop)
	Plain      []Plain
	Catalog    *components.Catalog // nil: components.Default()
}

type site struct {
	name string
	pos  token.Position
	line string
}

// Check fails t when the source and the catalog disagree:
//   - a declared loop is not in the catalog, or not declared by a literal name;
//   - a catalog loop of this host is started nowhere;
//   - a loop of a switchable component is started by a plain goroutine (the runner
//     could not stop it);
//   - a plain start in a start file names no catalog loop and does not carry Marker.
func Check(t *testing.T, s Spec) {
	t.Helper()
	cat := s.Catalog
	if cat == nil {
		cat = components.Default()
	}
	start := map[string]bool{}
	for _, g := range s.StartFiles {
		m, err := filepath.Glob(filepath.Join(s.Root, g))
		if err != nil || len(m) == 0 {
			t.Fatalf("start files %s: none (%v)", g, err)
		}
		for _, f := range m {
			start[filepath.Clean(f)] = true
		}
	}
	var declared, plain []site
	fset := token.NewFileSet()
	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != s.Root && (n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".")) {
				return filepath.SkipDir
			}
			if path != s.Root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir // another module
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		lines := strings.Split(string(src), "\n")
		inStart := start[filepath.Clean(path)]
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			line := lines[pos.Line-1]
			if slices.Contains(s.Declare, sel.Sel.Name) {
				name, ok := literal(call, 0)
				if !ok {
					t.Errorf("%s: %s must name its loop with a string literal", pos, sel.Sel.Name)
					return true
				}
				declared = append(declared, site{name, pos, line})
				return true
			}
			for _, p := range s.Plain {
				if sel.Sel.Name != p.Func || lastIdent(sel.X) != p.Pkg {
					continue
				}
				name := ""
				if p.NameArg >= 0 {
					name, _ = literal(call, p.NameArg)
				}
				if name != "" || inStart {
					plain = append(plain, site{name, pos, line})
				}
				if inStart {
					if _, ok := cat.ByLoop(s.Host, name); !ok && !strings.Contains(line, Marker) {
						t.Errorf("%s: %s.%s starts a goroutine that is not a catalog loop: declare it on the component runner, or say why it is not a loop (`%s <why>`)",
							pos, p.Pkg, p.Func, Marker)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, d := range declared {
		seen[d.name] = true
		if _, ok := cat.ByLoop(s.Host, d.name); !ok {
			t.Errorf("%s: loop %s:%s is declared but not in the catalog (shared/components/catalog.toml)", d.pos, s.Host, d.name)
		}
	}
	for _, p := range plain {
		if p.name == "" {
			continue
		}
		comp, ok := cat.ByLoop(s.Host, p.name)
		if !ok {
			continue // a named goroutine that is not a loop
		}
		seen[p.name] = true
		if comp.Switchable {
			t.Errorf("%s: loop %s:%s of %s is started by hand: declare it on the component runner so it stops with %s",
				p.pos, s.Host, p.name, comp.ID, comp.ID)
		}
	}
	loops := cat.LoopsOf(s.Host)
	names := make([]string, 0, len(loops))
	for n := range loops {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		if !seen[n] {
			t.Errorf("the catalog lists loop %s:%s (%s) but no code starts it", s.Host, n, loops[n].ID)
		}
	}
	if len(declared) == 0 {
		t.Errorf("no declared loop found under %s", s.Root)
	}
}

func literal(call *ast.CallExpr, i int) (string, bool) {
	if i >= len(call.Args) {
		return "", false
	}
	lit, ok := call.Args[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

func lastIdent(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}
