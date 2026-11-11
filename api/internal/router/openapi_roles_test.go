package router

// Epic 59 story 59.16: the API description and the role rules cannot drift from the router.
//
//   - TestOpenAPIDescribesEveryRoute: every /api route the home mounts is in
//     shared/api/openapi.json, except the ones testdata/openapi_undocumented.txt lists (the
//     routes older than this check). A new route fails until it is described; a listed one
//     that gets described, or goes, fails until it leaves the list, so the list only shrinks.
//   - TestOpenAPIAdminRoutesRoleTable: every operation the description marks
//     `x-maktaba-role: admin` is called through the auth gate as each principal: no one gets
//     401, every principal that is not an admin 403, an admin neither.
//   - TestOpenAPIAdminRoutesOnANode: a full node forwards each of them to the home (placement
//     home), so the home's gate and handler, the table's, answer a node's callers too.

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/placement"
	pluginsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/plugins"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// RoleAdmin is the x-maktaba-role of an operation only admins may call.
const RoleAdmin = "admin"

// apiSurface is everyMount with the plugin admin routes (MountP54 needs the components
// service and starts loops, so its admin handler is mounted alone, over the same kind of
// database that is never reached).
func apiSurface(t *testing.T) chi.Router {
	t.Helper()
	r := everyMount(t)
	db, err := sql.Open("postgres", "postgres://x:x@127.0.0.1:1/x?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	(&pluginsh.Admin{L: &plugins.Lifecycle{DB: db}}).Mount(r)
	return r
}

var specParam = regexp.MustCompile(`\{[^}]+\}`)

// specKey is "METHOD /api/path" with each parameter as {}.
func specKey(method, path string) string {
	return strings.ToUpper(method) + " " + specParam.ReplaceAllString(path, "{}")
}

type specOp struct {
	Method, Path string // the path as the description writes it, with /api
	Role         string
}

// specOps reads shared/api/openapi.json: its operations by specKey.
func specOps(t *testing.T) map[string]specOp {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "shared", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]specOp{}
	for p, item := range doc.Paths {
		for m, rawOp := range item {
			switch m {
			case "get", "post", "put", "patch", "delete":
			default:
				continue
			}
			var o struct {
				Role string `json:"x-maktaba-role"`
			}
			if err := json.Unmarshal(rawOp, &o); err != nil {
				t.Fatalf("%s %s: %v", m, p, err)
			}
			switch o.Role {
			case "", RoleAdmin:
			default:
				t.Errorf("%s %s: x-maktaba-role %q is not one this check knows (admin)", m, p, o.Role)
			}
			op := specOp{Method: strings.ToUpper(m), Path: "/api" + p, Role: o.Role}
			out[specKey(op.Method, op.Path)] = op
		}
	}
	return out
}

// mountedAPIRoutes is every "METHOD /api/..." route of the surface, by specKey.
func mountedAPIRoutes(t *testing.T, r chi.Router) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return nil // a Handle route lists every method; these five describe it
		}
		if strings.HasPrefix(route, "/api/") {
			out[specKey(method, route)] = method + " " + route
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const undocumentedFile = "testdata/openapi_undocumented.txt"

func readUndocumented(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(undocumentedFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOpenAPIDescribesEveryRoute(t *testing.T) {
	ops := specOps(t)
	routes := mountedAPIRoutes(t, apiSurface(t))
	if len(routes) < 400 {
		t.Fatalf("only %d /api routes walked: a surface is missing from apiSurface", len(routes))
	}
	listed := readUndocumented(t)
	var missing, stale []string
	for key, route := range routes {
		if _, ok := ops[key]; !ok && !listed[route] {
			missing = append(missing, route)
		}
	}
	for route := range listed {
		m, p, _ := strings.Cut(route, " ")
		key := specKey(m, p)
		if _, mounted := routes[key]; !mounted {
			stale = append(stale, route+" (no longer mounted)")
		} else if _, ok := ops[key]; ok {
			stale = append(stale, route+" (now described)")
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("shared/api/openapi.json does not describe %d route(s); describe each (en in openapi.json and openapi.yaml, "+
			"ar in openapi.ar.json; x-maktaba-role: admin when only admins may call it):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("remove from %s:\n  %s", undocumentedFile, strings.Join(stale, "\n  "))
	}
}

// rolePrincipal is one caller of the role table.
type rolePrincipal struct {
	name  string
	p     *principal.Principal
	admin bool
}

const roleLibrary = "6f1d2c3b-4a5e-4f60-8a7b-9c0d1e2f3a4b"

// rolePrincipals are the callers the tables of Epic 58 story 58.17 name that a principal can
// be on this server: a restricted child and a guardian are viewers to the role check, and
// no one is the gate's.
func rolePrincipals() []rolePrincipal {
	cloud := func(scope string) *principal.Principal {
		p := &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a2", IsAdmin: true, AccessAllLibraries: true,
			Source: principal.SourceJWT}
		p.ApplyCloudScope(scope, []string{roleLibrary})
		return p
	}
	return []rolePrincipal{
		{name: "no one"},
		{name: "viewer", p: &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c1", Source: principal.SourceCookie}},
		{name: "viewer with the library", p: &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c2",
			Libraries: []string{roleLibrary}, Source: principal.SourceJWT}},
		{name: "viewer's API token", p: &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000c3",
			Libraries: []string{roleLibrary}, Source: principal.SourcePAT}},
		{name: "an admin's cloud play sign-in", p: cloud(principal.CloudScopePlay)},
		{name: "admin", p: &principal.Principal{UserID: "00000000-0000-0000-0000-0000000000a1", IsAdmin: true,
			AccessAllLibraries: true, Source: principal.SourceCookie}, admin: true},
		{name: "an admin's cloud manage sign-in", p: cloud(principal.CloudScopeManage), admin: true},
	}
}

// specPath fills a described path's parameters.
func specPath(p string) string {
	return specParam.ReplaceAllStringFunc(p, func(string) string { return roleLibrary })
}

func adminOps(t *testing.T) []specOp {
	t.Helper()
	var out []specOp
	for _, op := range specOps(t) {
		if op.Role == RoleAdmin {
			out = append(out, op)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path+out[i].Method < out[j].Path+out[j].Method })
	if len(out) == 0 {
		t.Fatal("no operation is marked x-maktaba-role: admin")
	}
	return out
}

// T01: each admin route × every principal, through the gate (RequireAuthExcept, as the
// home and every full node run it).
func TestOpenAPIAdminRoutesRoleTable(t *testing.T) {
	surface := apiSurface(t)
	routes := mountedAPIRoutes(t, surface)
	gate := authmw.RequireAuthExcept(authmw.DefaultPublicAllowlist())(surface)
	for _, op := range adminOps(t) {
		if _, ok := routes[specKey(op.Method, op.Path)]; !ok {
			t.Errorf("%s %s is described as admin only but not mounted", op.Method, op.Path)
			continue
		}
		path := specPath(op.Path)
		for _, who := range rolePrincipals() {
			req := httptest.NewRequest(op.Method, path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			if who.p != nil {
				p := *who.p
				req = req.WithContext(principal.WithPrincipal(req.Context(), &p))
			}
			rec := httptest.NewRecorder()
			gate.ServeHTTP(rec, req)
			switch {
			case who.p == nil && rec.Code != http.StatusUnauthorized:
				t.Errorf("%s %s as %s: %d, want 401", op.Method, op.Path, who.name, rec.Code)
			case who.p != nil && !who.admin && rec.Code != http.StatusForbidden:
				t.Errorf("%s %s as %s: %d, want 403", op.Method, op.Path, who.name, rec.Code)
			case who.admin && (rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden):
				t.Errorf("%s %s as %s: %d, want past the role check", op.Method, op.Path, who.name, rec.Code)
			}
		}
	}
}

// T02: a full node forwards every admin route to the home.
func TestOpenAPIAdminRoutesOnANode(t *testing.T) {
	for _, op := range adminOps(t) {
		if c, _ := Placement.Classify(op.Method, specParam.ReplaceAllString(op.Path, "{id}")); c != placement.Home {
			t.Errorf("%s %s is %s on a full node, want home (forwarded to the home, whose role check the table proves)",
				op.Method, op.Path, c)
		}
	}
}
