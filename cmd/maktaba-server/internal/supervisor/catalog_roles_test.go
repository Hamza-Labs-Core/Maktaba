package supervisor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
