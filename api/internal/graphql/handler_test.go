package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/ws"
)

// withUser returns a request carrying an authenticated principal.
func withUser(req *http.Request, id string, admin bool) *http.Request {
	ctx := principal.WithPrincipal(req.Context(), &principal.Principal{UserID: id, IsAdmin: admin, AccessAllLibraries: admin})
	return req.WithContext(ctx)
}

// fakeREST stands in for the API router: it records every sub-request
// and answers with an empty JSON object, or with status/body when set.
type fakeREST struct {
	mu     sync.Mutex
	calls  []string
	status int
	body   string
}

func (f *fakeREST) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	status, body := f.status, f.body
	f.mu.Unlock()
	if status == 0 {
		status, body = http.StatusOK, `{}`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (f *fakeREST) taken() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.calls...)
	f.calls = nil
	return out
}

func newTestHandler(rest http.Handler) *Handler {
	return &Handler{Resolvers: &Resolvers{Bridge: &Bridge{Router: rest}, Hub: ws.NewHub()}}
}

func post(t *testing.T, h http.Handler, req *http.Request) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("status %d, body not JSON: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, env
}

func gqlBody(query string, vars map[string]any) *strings.Reader {
	b, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	return strings.NewReader(string(b))
}

func errCode(env map[string]any) string {
	errs, _ := env["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	ext, _ := errs[0].(map[string]any)["extensions"].(map[string]any)
	code, _ := ext["code"].(string)
	return code
}

func TestSchemaEndpoint_ServesSDL(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/graphql/schema", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Body.String() != Schema {
		t.Fatalf("GET /graphql/schema does not serve the SDL (%d bytes)", rec.Body.Len())
	}
}

// Without the API database the endpoint answers 503 unavailable, never
// a schema-only 501.
func TestPost_NoResolvers_Unavailable(t *testing.T) {
	h := &Handler{}
	rec := httptest.NewRecorder()
	req := withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(`{ libraries { id } }`, nil)), "u1", true)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if got := errCode(env); got != "unavailable" {
		t.Fatalf("code = %q, want unavailable", got)
	}
}

func TestPost_NoPrincipal_Unauthenticated(t *testing.T) {
	rest := &fakeREST{}
	h := newTestHandler(rest)
	status, env := post(t, h, httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(`query { continueWatching { id } }`, nil)))
	if status != http.StatusUnauthorized || errCode(env) != "unauthenticated" {
		t.Fatalf("status=%d code=%q", status, errCode(env))
	}
	if calls := rest.taken(); len(calls) != 0 {
		t.Fatalf("an anonymous request reached REST: %v", calls)
	}
}

func TestPost_MalformedBody_400(t *testing.T) {
	h := newTestHandler(&fakeREST{})
	status, env := post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`not json`)), "u1", false))
	if status != http.StatusBadRequest || errCode(env) != "bad-request" {
		t.Fatalf("status=%d code=%q", status, errCode(env))
	}
}

func TestPost_InvalidQuery_ValidationError(t *testing.T) {
	h := newTestHandler(&fakeREST{})
	status, env := post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(`{ noSuchField }`, nil)), "u1", false))
	if status != http.StatusBadRequest || errCode(env) != "validation-failed" {
		t.Fatalf("status=%d code=%q env=%v", status, errCode(env), env)
	}
}

// A REST problem surfaces as a GraphQL error carrying the REST status
// and a code; a nullable lookup answers a REST 404 with null.
func TestRESTErrorsMapToGraphQLErrors(t *testing.T) {
	rest := &fakeREST{status: http.StatusForbidden, body: `{"type":"https://maktaba.dev/problems/forbidden","title":"forbidden","status":403,"detail":"admin-only"}`}
	h := newTestHandler(rest)
	_, env := post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql",
		gqlBody(`mutation { scanLibrary(id: "x") }`, nil)), "u1", false))
	if errCode(env) != "forbidden" {
		t.Fatalf("code = %q, env %v", errCode(env), env)
	}
	rest.status, rest.body = http.StatusNotFound, `{"title":"not found","status":404}`
	_, env = post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql",
		gqlBody(`{ video(id: "x") { id } }`, nil)), "u1", false))
	if env["errors"] != nil {
		t.Fatalf("404 lookup should be null without error: %v", env)
	}
	if data := env["data"].(map[string]any); data["video"] != nil {
		t.Fatalf("video = %v, want null", data["video"])
	}
}

func TestCostLimitExceeded(t *testing.T) {
	h := newTestHandler(&fakeREST{})
	q := `{ video(id: "x") { latestTranscript { segments(first: 100000) { id text } } } }`
	status, env := post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(q, nil)), "u1", false))
	if status != http.StatusBadRequest || errCode(env) != "cost-limit-exceeded" {
		t.Fatalf("status=%d code=%q", status, errCode(env))
	}
	// The same through a variable.
	q = `query($n: Int) { videos(limit: $n) { items { tags { name } audioTracks { codec } chapters { title } } } }`
	_, env = post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(q, map[string]any{"n": 5000})), "u1", false))
	if errCode(env) != "cost-limit-exceeded" {
		t.Fatalf("variable page size not costed: %v", env)
	}
}

func TestDepthLimitExceeded(t *testing.T) {
	h := newTestHandler(&fakeREST{})
	h.MaxDepth = 3
	q := `{ collections { items { latestTranscript { segments { words { text } } } } } }`
	status, env := post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(q, nil)), "u1", false))
	if status != http.StatusBadRequest || errCode(env) != "depth-limit-exceeded" {
		t.Fatalf("status=%d code=%q", status, errCode(env))
	}
}

// The standard introspection query is deep; it is exempt from the
// limits so client tooling can read the schema.
func TestIntrospectionAllowed(t *testing.T) {
	h := newTestHandler(&fakeREST{})
	q := `{ __schema { queryType { name } types { name kind fields { name type { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } } }`
	status, env := post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(q, nil)), "u1", false))
	if status != http.StatusOK || env["errors"] != nil {
		t.Fatalf("status=%d env=%v", status, env["errors"])
	}
}

func TestSubscriptionOverPOSTRejected(t *testing.T) {
	h := newTestHandler(&fakeREST{})
	status, env := post(t, h, withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(`subscription { jobUpdates { id } }`, nil)), "u1", false))
	if status != http.StatusBadRequest || errCode(env) != "bad-request" {
		t.Fatalf("status=%d code=%q", status, errCode(env))
	}
}

// TestEveryRootFieldIsWired walks the schema: every Query and Mutation
// field, called with stand-in arguments, must run at least one REST
// sub-request and must not answer 501 / schema-only / not_implemented;
// every Subscription field must open a stream.
func TestEveryRootFieldIsWired(t *testing.T) {
	rest := &fakeREST{}
	h := newTestHandler(rest)
	for _, opType := range []*ast.Definition{parsedSchema.Query, parsedSchema.Mutation} {
		kind := "query"
		if opType == parsedSchema.Mutation {
			kind = "mutation"
		}
		for _, f := range opType.Fields {
			if strings.HasPrefix(f.Name, "__") {
				continue
			}
			t.Run(kind+"."+f.Name, func(t *testing.T) {
				q := kind + " { " + f.Name + argLiteral(f.Arguments) + selectionFor(f.Type) + " }"
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(q, nil)), "u1", true))
				body := rec.Body.String()
				if rec.Code == http.StatusNotImplemented || strings.Contains(body, "schema-only") || strings.Contains(body, "not_implemented") {
					t.Fatalf("%s is not implemented: %d %s", f.Name, rec.Code, body)
				}
				if rec.Code != http.StatusOK {
					t.Fatalf("%s: status %d: %s", f.Name, rec.Code, body)
				}
				calls := rest.taken()
				if len(calls) == 0 {
					t.Fatalf("%s ran no REST route (%s)", f.Name, body)
				}
				for _, c := range calls {
					if !strings.Contains(c, " /api/") {
						t.Fatalf("%s called %q, not a REST route", f.Name, c)
					}
				}
			})
		}
	}
	for _, f := range parsedSchema.Subscription.Fields {
		if strings.HasPrefix(f.Name, "__") {
			continue
		}
		t.Run("subscription."+f.Name, func(t *testing.T) {
			// an admin: libraryEvents / playbackUpdates refuse a library the
			// caller cannot read (fields_pg_test covers a member)
			ctx, cancel := context.WithCancel(principal.WithPrincipal(context.Background(),
				&principal.Principal{UserID: "u1", IsAdmin: true, AccessAllLibraries: true}))
			defer cancel()
			q := "subscription { " + f.Name + argLiteral(f.Arguments) + selectionFor(f.Type) + " }"
			ch, err := h.schema().Subscribe(ctx, q, "", nil)
			if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
			select {
			case v := <-ch:
				b, _ := json.Marshal(v)
				t.Fatalf("%s: stream closed or errored at once: %s", f.Name, b)
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

// argLiteral renders stand-in values for the non-null arguments.
func argLiteral(args ast.ArgumentDefinitionList) string {
	var parts []string
	for _, a := range args {
		if !a.Type.NonNull {
			continue
		}
		parts = append(parts, a.Name+": "+valueLiteral(a.Type))
	}
	if len(parts) == 0 {
		return ""
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func valueLiteral(t *ast.Type) string {
	if t.Elem != nil {
		return "[" + valueLiteral(t.Elem) + "]"
	}
	switch t.NamedType {
	case "ID":
		return `"00000000-0000-0000-0000-000000000001"`
	case "String":
		return `"x"`
	case "Int":
		return "1"
	case "Float":
		return "1.5"
	case "Boolean":
		return "true"
	case "JSON":
		return "{}"
	case "Time":
		return `"2026-01-01T00:00:00Z"`
	}
	def := parsedSchema.Types[t.NamedType]
	var fields []string
	for _, f := range def.Fields {
		if f.Type.NonNull {
			fields = append(fields, f.Name+": "+valueLiteral(f.Type))
		}
	}
	return "{" + strings.Join(fields, ", ") + "}"
}

// selectionFor selects __typename on an object result.
func selectionFor(t *ast.Type) string {
	for t.Elem != nil {
		t = t.Elem
	}
	if def := parsedSchema.Types[t.NamedType]; def != nil && def.Kind == ast.Object {
		return " { __typename }"
	}
	return ""
}

// The derived Idempotency-Key: each mutation field's REST call gets the
// caller's key plus its position and name, never the caller's key itself.
func TestMutationIdempotencyKeyDerived(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	rest := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Method+" "+r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	h := newTestHandler(rest)
	req := withUser(httptest.NewRequest(http.MethodPost, "/graphql",
		gqlBody(`mutation { a: resumeJob(id: "1") { id } b: cancelJob(id: "2") { id } }`, nil)), "u1", true)
	req.Header.Set("Idempotency-Key", "k1")
	if status, env := post(t, h, req); status != http.StatusOK || env["errors"] != nil {
		t.Fatalf("status=%d env=%v", status, env)
	}
	want := []string{"POST k1:graphql:1:resumeJob", "POST k1:graphql:2:cancelJob"}
	if fmt.Sprint(keys) != fmt.Sprint(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	// A query sends no key.
	keys = nil
	req = withUser(httptest.NewRequest(http.MethodPost, "/graphql", gqlBody(`{ tags { id } }`, nil)), "u1", true)
	req.Header.Set("Idempotency-Key", "k2")
	post(t, h, req)
	if len(keys) != 1 || keys[0] != "GET " {
		t.Fatalf("query keys = %v", keys)
	}
}

func TestParity_ContinueWatchingInSDL(t *testing.T) {
	if !strings.Contains(Schema, "continueWatching(limit: Int)") {
		t.Fatal("continueWatching query missing from SDL — TV client contract broken")
	}
	if !strings.Contains(Schema, "type RailCard") {
		t.Fatal("RailCard type missing from SDL")
	}
}
