package graphql

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// withUser returns a request carrying an authenticated principal.
func withUser(req *http.Request, id string) *http.Request {
	ctx := principal.WithPrincipal(req.Context(), &principal.Principal{UserID: id})
	return req.WithContext(ctx)
}

func TestSchemaEndpoint_ServesSDL(t *testing.T) {
	h := Handler{}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/graphql/schema", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "type Query") || !strings.Contains(body, "continueWatching(limit: Int)") {
		t.Fatalf("SDL missing expected fields; got %d bytes", len(body))
	}
}

// A Handler with no resolvers preserves the legacy schema-only 501 so
// callers that wire it without a DB are unaffected.
func TestPost_NoResolvers_501(t *testing.T) {
	h := Handler{}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"query":"{ continueWatching { id } }"}`)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/graphql", body))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var env map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	errs := env["errors"].([]any)
	ext := errs[0].(map[string]any)["extensions"].(map[string]any)
	if ext["code"] != "schema-only" {
		t.Fatalf("expected schema-only, got %v", ext["code"])
	}
}

func TestPost_NoPrincipal_Forbidden(t *testing.T) {
	h := Handler{Resolvers: &Resolvers{}}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"query":"query { continueWatching { id } }"}`)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/graphql", body))
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("body=%s", rec.Body.String())
	}
	errs, ok := env["errors"].([]any)
	if !ok || len(errs) == 0 {
		t.Fatalf("expected auth error, got %s", rec.Body.String())
	}
	ext := errs[0].(map[string]any)["extensions"].(map[string]any)
	if ext["code"] != "forbidden" {
		t.Fatalf("expected forbidden, got %v", ext["code"])
	}
}

func TestPost_MalformedBody_400(t *testing.T) {
	h := Handler{Resolvers: &Resolvers{}}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`not json`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

// rootField correctly extracts the operation root field across the
// query forms the TV clients actually send.
func TestRootField_Extraction(t *testing.T) {
	h := Handler{}
	cases := map[string]string{
		`{ continueWatching(limit: 12) { id } }`:                                               "continueWatching",
		`query ContinueWatching($l:Int){ continueWatching { id } }`:                            "continueWatching",
		"query Recommendations($limit: Int = 12) {\n recommendations(limit: $limit) { id }\n}": "recommendations",
		`query Search($q:String!){ search(q:$q){ id } }`:                                       "search",
	}
	for q, want := range cases {
		got := h.rootField(&gqlRequest{Query: q})
		if got != want {
			t.Errorf("rootField(%q) = %q, want %q", q, got, want)
		}
	}
}

// An authenticated query for an unimplemented root field returns the
// honest schema-only 501 rather than fabricating data.
func TestPost_UnknownField_SchemaOnly(t *testing.T) {
	h := Handler{Resolvers: &Resolvers{}}
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"query":"query { libraries { id } }"}`)
	req := withUser(httptest.NewRequest(http.MethodPost, "/graphql", body), "u1")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
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
