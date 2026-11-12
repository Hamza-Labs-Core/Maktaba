package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The router's parental gate refuses every signed-in user until MountP6 gives it the
// policy cache (P6Deps.ParentalGate). A full node mounts the library with a MountP6
// call of its own (mountNode): when that call left the gate out, every content route
// of a second machine answered 503 to everyone (TestTwoMachinesOnePC, the replica
// job). So every P6Deps this program builds names the gate.
func TestEveryP6MountGivesTheParentalGateItsCache(t *testing.T) {
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	mounts := 0
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "P6Deps" {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "router" {
				return true
			}
			mounts++
			for _, el := range lit.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "ParentalGate" {
						return true
					}
				}
			}
			t.Errorf("%s: a router.P6Deps without ParentalGate: its routes refuse every signed-in user",
				fset.Position(lit.Pos()))
			return true
		})
	}
	if mounts < 2 {
		t.Fatalf("found %d router.P6Deps (the home's and the node's are expected): this test no longer reads the mounts", mounts)
	}
}
