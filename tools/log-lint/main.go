// log-lint enforces Story 21.1 AC-4: structured-log messages must be
// constant strings — no user/runtime data concatenated into the `msg`
// argument. Variable data belongs in explicit key/value fields so it is
// queryable and so PII can be redacted by the logging layer; a
// `"user " + name + " did X"` style message defeats redaction and
// blows up log cardinality.
//
// The gap doc (Epic 21, Story 21.1 AC-4) records this as the unenforced
// clause: "the AC's enforcement mechanism — the AST concat lint — does
// not exist". This tool is that mechanism, mirroring the existing
// tools/migration-lint convention (standalone Go tool, stdlib only,
// human-readable output, exit 0 = clean / non-zero = violations, does
// not bail on the first finding).
//
// Rule: in a call to a logger method whose name is one of
// Debug/Info/Warn/Error/DebugContext/InfoContext/WarnContext/
// ErrorContext/Log/LogAttrs, the message argument (the first string
// argument, or the arg after the level/ctx for Log/LogAttrs) must be a
// plain string literal or an untyped string constant — not a binary
// `+` concatenation and not an fmt.Sprintf call.
//
// Scope: every Go module listed via --dirs (defaults to the repo's
// service + shared modules). Vendored, generated and _test.go files are
// skipped — tests legitimately build dynamic assertion messages and are
// not production log sites.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// logMethods are the slog/logger call names whose message argument is
// rule-checked.
var logMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true,
	"DebugContext": true, "InfoContext": true,
	"WarnContext": true, "ErrorContext": true,
	"Log": true, "LogAttrs": true,
}

// msgArgIndex returns the index of the message argument for a given
// logger method. Log/LogAttrs take (ctx, level, msg, ...); the rest
// take (msg, ...). Returns -1 if there is no message arg to check.
func msgArgIndex(method string, nargs int) int {
	switch method {
	case "Log", "LogAttrs":
		if nargs >= 3 {
			return 2
		}
		return -1
	default:
		if nargs >= 1 {
			return 0
		}
		return -1
	}
}

type violation struct {
	pos  token.Position
	kind string
}

func main() {
	var dirsCSV string
	flag.StringVar(&dirsCSV, "dirs",
		"api,streaming,shared/log/go,shared/health/go,shared/metrics/go,"+
			"shared/tracing/go,shared/errrpt/go",
		"Comma-separated module roots to scan (relative to repo root)")
	flag.Parse()

	var all []violation
	for _, d := range strings.Split(dirsCSV, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			fmt.Fprintf(os.Stderr, "log-lint: resolve %q: %v\n", d, err)
			os.Exit(2)
		}
		if info, statErr := os.Stat(abs); statErr != nil || !info.IsDir() {
			// A configured module may not exist in every checkout;
			// skip rather than fail (mirrors migration-lint tolerance).
			continue
		}
		vs, err := scanDir(abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "log-lint: scan %q: %v\n", d, err)
			os.Exit(2)
		}
		all = append(all, vs...)
	}

	if len(all) == 0 {
		fmt.Println("log-lint: clean — no message concatenation found")
		return
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].pos.Filename != all[j].pos.Filename {
			return all[i].pos.Filename < all[j].pos.Filename
		}
		return all[i].pos.Line < all[j].pos.Line
	})
	for _, v := range all {
		fmt.Printf("%s:%d: log message uses %s — put runtime data in explicit fields, not the msg (Story 21.1 AC-4)\n",
			v.pos.Filename, v.pos.Line, v.kind)
	}
	fmt.Fprintf(os.Stderr, "log-lint: %d violation(s)\n", len(all))
	os.Exit(1)
}

func scanDir(root string) ([]violation, error) {
	var out []violation
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == "vendor" || base == "testdata" ||
				(base != "." && strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			method := sel.Sel.Name
			if !logMethods[method] {
				return true
			}
			idx := msgArgIndex(method, len(call.Args))
			if idx < 0 || idx >= len(call.Args) {
				return true
			}
			if k := badMsgExpr(call.Args[idx]); k != "" {
				out = append(out, violation{pos: fset.Position(call.Args[idx].Pos()), kind: k})
			}
			return true
		})
		return nil
	})
	return out, err
}

// badMsgExpr returns a non-empty description if e is a disallowed
// message expression (string `+` concat or fmt.Sprintf). A plain
// literal, an identifier (named const), or a selector const is allowed
// — those are static at the call site.
func badMsgExpr(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BinaryExpr:
		if x.Op == token.ADD && (isStringy(x.X) || isStringy(x.Y)) {
			return "string concatenation"
		}
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
			if pkg, ok := sel.X.(*ast.Ident); ok &&
				pkg.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Sprint") {
				return "fmt." + sel.Sel.Name
			}
		}
	}
	return ""
}

// isStringy reports whether e looks like it contributes string content
// to a `+` (a string literal or any non-numeric expression). Numeric
// literals can't form a log message so a `+` between them is not our
// concern; anything else in a `+` feeding a msg arg is suspicious.
func isStringy(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.BasicLit:
		return x.Kind == token.STRING
	case *ast.BinaryExpr:
		return isStringy(x.X) || isStringy(x.Y)
	default:
		// Identifiers, selectors, calls in a `+` next to a string are
		// the exact "msg + variable" pattern we flag.
		return true
	}
}
