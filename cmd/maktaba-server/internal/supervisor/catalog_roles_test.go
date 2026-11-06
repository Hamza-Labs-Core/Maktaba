package supervisor

import (
	"fmt"
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

// catalogInstances is every instance a role's Build* functions can return ("" is the
// role's main process). A new Role constant must be added here (the test reads the
// package's constants), and each instance must be in exactly one catalog component.
var catalogInstances = map[Role][]string{
	RoleAPI:       {""},
	RoleStreaming: {""},
	RolePostgres:  {""},
	RoleOllama:    {""},
	RoleRelay:     {""},
	RoleCopy:      {""},
	RoleLogs:      {""},
	RoleTVSlot:    SlotNames,
	RolePipeline:  pipelineInstances(),
}

func pipelineInstances() []string {
	out := []string{"", IndexInstance, ChromaCopyInstance}
	for i := 1; i <= MaxPipelineInstances; i++ {
		out = append(out, "stt-"+strconv.Itoa(i))
	}
	return out
}

// roleConstants are the string values of every constant of type Role declared in the
// package's own (non-test) files.
func roleConstants(t *testing.T) []Role {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []Role
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, s := range g.Specs {
				v := s.(*ast.ValueSpec)
				if id, ok := v.Type.(*ast.Ident); !ok || id.Name != "Role" {
					continue
				}
				for _, val := range v.Values {
					if lit, ok := val.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, _ := strconv.Unquote(lit.Value)
						out = append(out, Role(s))
					}
				}
			}
		}
	}
	return out
}

// stringConsts are the package's own string constants (Role ones and untyped ones such as
// IndexInstance), by name.
func stringConsts(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, s := range g.Specs {
				v := s.(*ast.ValueSpec)
				for i, val := range v.Values {
					if lit, ok := val.(*ast.BasicLit); ok && lit.Kind == token.STRING && i < len(v.Names) {
						out[v.Names[i].Name], _ = strconv.Unquote(lit.Value)
					}
				}
			}
		}
	}
	return out
}

// processLit is one Process{...} the source builds: its role and instance as written.
type processLit struct {
	where    string
	role     string // the Role constant's value; "" when it is not a constant (a variable)
	roleExpr string
	instance string // the instance's value; "" for the main process
	dynamic  string // an instance that is not a constant: its string prefix ("stt-") or "?"
}

// processLiterals are every Process composite literal in the module's own (non-test)
// files, read from the source of every package under root, not from a list of files: a
// process added anywhere (a new Build* file, cmd_serve.go) is found.
func processLiterals(t *testing.T, root string, consts map[string]string) []processLit {
	t.Helper()
	var out []processLit
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); path != root && (n == "testdata" || n == "vendor" || strings.HasPrefix(n, ".")) {
				return filepath.SkipDir
			}
			if path != root {
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
		if !strings.Contains(string(src), "Process{") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		isProcess := func(e ast.Expr) bool {
			switch x := e.(type) {
			case *ast.Ident:
				return x.Name == "Process"
			case *ast.SelectorExpr:
				return x.Sel.Name == "Process" && lastIdentOf(x.X) == "supervisor"
			}
			return false
		}
		visit := func(cl *ast.CompositeLit) {
			if len(cl.Elts) == 0 {
				return // Process{}: an error return, no process
			}
			p := processLit{where: fset.Position(cl.Pos()).String()}
			hasRole := false
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, _ := kv.Key.(*ast.Ident)
				if key == nil {
					continue
				}
				switch key.Name {
				case "Role":
					hasRole = true
					p.roleExpr = exprString(kv.Value)
					p.role = constValue(kv.Value, consts)
				case "Instance":
					if v, ok := strValue(kv.Value, consts); ok {
						p.instance = v
					} else if b, ok := kv.Value.(*ast.BinaryExpr); ok {
						if v, ok := strValue(b.X, consts); ok {
							p.dynamic = v
						} else {
							p.dynamic = "?"
						}
					} else {
						p.dynamic = "?"
					}
				}
			}
			if !hasRole {
				t.Errorf("%s: a Process with no Role", p.where)
			}
			out = append(out, p)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if isProcess(cl.Type) {
				visit(cl)
				return true
			}
			// []Process{{...}, ...}: the elements' type is elided
			if at, ok := cl.Type.(*ast.ArrayType); ok && isProcess(at.Elt) {
				for _, el := range cl.Elts {
					if inner, ok := el.(*ast.CompositeLit); ok && inner.Type == nil {
						visit(inner)
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
	return out
}

func lastIdentOf(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// constValue is a constant's value: a string literal, or a package constant by name
// (bare or supervisor.<Name>); "" otherwise.
func constValue(e ast.Expr, consts map[string]string) string {
	v, _ := strValue(e, consts)
	return v
}

func strValue(e ast.Expr, consts map[string]string) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			v, err := strconv.Unquote(x.Value)
			return v, err == nil
		}
	case *ast.Ident:
		v, ok := consts[x.Name]
		return v, ok
	case *ast.SelectorExpr:
		if lastIdentOf(x.X) == "supervisor" {
			v, ok := consts[x.Sel.Name]
			return v, ok
		}
	}
	return "", false
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return lastIdentOf(x.X) + "." + x.Sel.Name
	case *ast.BasicLit:
		return x.Value
	}
	return fmt.Sprintf("%T", e)
}

// checkProcessLiterals: every process the source builds is one catalogInstances lists
// (so TestCatalogRoles maps it to exactly one component). A literal instance must be
// listed; a computed one ("stt-"+n, a slot's name) needs its role's listed set (with that
// prefix); a role that is not a constant (BuildProcess's role) may only build the main
// process of each role.
func checkProcessLiterals(lits []processLit, instances map[Role][]string) []string {
	var errs []string
	for _, p := range lits {
		if p.role == "" {
			if p.instance != "" || p.dynamic != "" {
				errs = append(errs, fmt.Sprintf("%s: Process{Role: %s} with an instance: name the role with a Role constant", p.where, p.roleExpr))
			}
			continue
		}
		listed, ok := instances[Role(p.role)]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: role %q is not in catalogInstances: add it, and the process to shared/components/catalog.toml", p.where, p.role))
			continue
		}
		switch {
		case p.dynamic != "":
			n := 0
			for _, inst := range listed {
				if inst != "" && strings.HasPrefix(inst, strings.TrimSuffix(p.dynamic, "?")) {
					n++
				}
			}
			if p.dynamic == "?" {
				n = len(listed) - 1
			}
			if n < 1 {
				errs = append(errs, fmt.Sprintf("%s: %s instances %q... are not in catalogInstances", p.where, p.role, p.dynamic))
			}
		case !slices.Contains(listed, p.instance):
			errs = append(errs, fmt.Sprintf("%s: process %s/%s is not in catalogInstances: add it, and to shared/components/catalog.toml", p.where, p.role, p.instance))
		}
	}
	return errs
}

// T06, from the source: every Process the module builds, anywhere, is a listed one.
func TestCatalogRolesFromTheSource(t *testing.T) {
	consts := stringConsts(t)
	lits := processLiterals(t, filepath.Join("..", ".."), consts)
	if len(lits) < 8 {
		t.Fatalf("found only %d Process literals: %+v", len(lits), lits)
	}
	for _, e := range checkProcessLiterals(lits, catalogInstances) {
		t.Error(e)
	}
	// and each listed role is built somewhere (by its constant, or by BuildProcess's role)
	built := map[string]bool{}
	variable := false
	for _, p := range lits {
		built[p.role] = true
		variable = variable || p.role == ""
	}
	for role := range catalogInstances {
		byBuildProcess := variable && slices.Contains([]Role{RoleAPI, RoleStreaming, RolePipeline}, role)
		if !built[string(role)] && !byBuildProcess {
			t.Errorf("catalogInstances lists %q, which no Process literal builds", role)
		}
	}
}

// A process added in a new file, with a new instance or a role the list lacks, is found.
func TestCatalogRolesFindANewProcess(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "newpart"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package newpart

import "example/supervisor"

func Build(n string) []supervisor.Process {
	return []supervisor.Process{
		{Role: supervisor.RolePipeline, Instance: "brand-new"},
		{Role: supervisor.RoleTVSlot, Instance: n},
		{Role: "new-role"},
	}
}

func Err() (supervisor.Process, error) { return supervisor.Process{}, nil }
`
	if err := os.WriteFile(filepath.Join(root, "internal", "newpart", "build.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	consts := stringConsts(t)
	lits := processLiterals(t, root, consts)
	if len(lits) != 3 {
		t.Fatalf("found %d literals: %+v", len(lits), lits)
	}
	errs := checkProcessLiterals(lits, catalogInstances)
	want := []string{"process pipeline/brand-new is not in catalogInstances", `role "new-role" is not in catalogInstances`}
	if len(errs) != len(want) {
		t.Fatalf("errors %q, want %d", errs, len(want))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Errorf("error %q does not say %q", errs[i], w)
		}
	}
}

// T06 (story 51.01): every process the supervisor can run belongs to exactly one
// catalog component.
func TestCatalogRoles(t *testing.T) {
	cat := components.Default()
	roles := roleConstants(t)
	if len(roles) < 8 {
		t.Fatalf("found only %d Role constants: %v", len(roles), roles)
	}
	for _, r := range roles {
		if _, ok := catalogInstances[r]; !ok {
			t.Errorf("role %q has no instances in catalogInstances: add them, and the role to shared/components/catalog.toml", r)
		}
	}
	for role, instances := range catalogInstances {
		if !slices.Contains(roles, role) {
			t.Errorf("catalogInstances lists %q, which is not a Role constant", role)
		}
		for _, inst := range instances {
			got := cat.MatchProcesses(string(role), inst)
			switch len(got) {
			case 1:
			case 0:
				t.Errorf("process %s/%s is in no catalog component", role, inst)
			default:
				ids := make([]string, len(got))
				for i, c := range got {
					ids[i] = c.ID
				}
				t.Errorf("process %s/%s is in %d components: %v", role, inst, len(got), ids)
			}
		}
	}
	// and every catalog pattern names a role the supervisor has
	for _, c := range cat.Components() {
		for _, p := range c.Processes {
			role, _, _ := strings.Cut(p, "/")
			if !slices.Contains(roles, Role(role)) {
				t.Errorf("%s: process %q names no supervisor role", c.ID, p)
			}
		}
	}
}
