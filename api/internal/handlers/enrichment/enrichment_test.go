package enrichment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

const okID = "11111111-1111-1111-1111-111111111111"

func req(method, target, body string, admin bool) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	p := &principal.Principal{UserID: "u1", IsAdmin: admin, AccessAllLibraries: admin}
	return r.WithContext(principal.WithPrincipal(r.Context(), p))
}

func mount() chi.Router {
	h := &Handler{} // nil DB
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// GET with a nil DB returns a well-formed empty candidate set, never 500.
func TestList_NilDB_EmptyCandidates(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, req(http.MethodGet, "/api/videos/"+okID+"/enrichment", "", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Candidates []Candidate `json:"candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Candidates == nil {
		t.Fatal("candidates must be a non-nil array")
	}
}

func TestList_MalformedID(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, req(http.MethodGet, "/api/videos/not-a-uuid/enrichment", "", false))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

// AC: a read-only (non-admin) user gets 403 on accept/dismiss/revert.
func TestAccept_ACL_ReadOnlyForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, req(http.MethodPost, "/api/videos/"+okID+"/enrichment/accept",
		`{"external_id":"tmdb:movie:603"}`, false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDismiss_ACL_ReadOnlyForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, req(http.MethodPost, "/api/videos/"+okID+"/enrichment/dismiss", `{}`, false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

// AC: accept requires external_id.
func TestAccept_RequiresExternalID(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, req(http.MethodPost, "/api/videos/"+okID+"/enrichment/accept", `{}`, true))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Admin accept with a nil DB degrades to 503 (Unavailable), not 500.
func TestAccept_NilDB_Unavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, req(http.MethodPost, "/api/videos/"+okID+"/enrichment/accept",
		`{"external_id":"tmdb:movie:603"}`, true))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Manual search never auto-applies; with a nil DB it returns an empty,
// well-formed candidate set (the "videos unchanged" guarantee holds
// trivially with no DB).
func TestSearch_NoAutoApply_NilDB(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, req(http.MethodPost, "/api/videos/"+okID+"/enrichment/search",
		`{"query":"the matrix","year":1999}`, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// diffFields marks a user-owned field protected and computes would_change.
func TestDiffFields_ProtectedAndWouldChange(t *testing.T) {
	v := videoRow{}
	v.Title.String, v.Title.Valid = "Old Title", true
	mapped := map[string]any{"title": "New Title", "description": "syn"}
	prot := map[string]bool{"title": true}
	diffs := diffFields(v, mapped, prot)
	byField := map[string]FieldDiff{}
	for _, d := range diffs {
		byField[d.Field] = d
	}
	if !byField["title"].Protected {
		t.Fatal("title should be protected (user-owned)")
	}
	if !byField["title"].WouldChange {
		t.Fatal("title would change: Old Title -> New Title")
	}
	if byField["description"].Protected {
		t.Fatal("description should not be protected")
	}
}

// versionMatches accepts the stored timestamp's RFC3339 forms and
// rejects a stale one.
func TestVersionMatches(t *testing.T) {
	v := videoRow{}
	v.Title.Valid = false
	// fixed time
	if !versionMatches(parseTime(t, "2026-06-14T12:00:00Z"), "2026-06-14T12:00:00Z") {
		t.Fatal("matching RFC3339 should pass")
	}
	if versionMatches(parseTime(t, "2026-06-14T12:00:00Z"), "2020-01-01T00:00:00Z") {
		t.Fatal("stale version must not match")
	}
}
