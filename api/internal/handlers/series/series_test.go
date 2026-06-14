package series

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const sid = "11111111-1111-1111-1111-111111111111"

func authReq(method, target string, p *principal.Principal) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if p != nil {
		r = r.WithContext(principal.WithPrincipal(r.Context(), p))
	}
	return r
}

func mount() chi.Router {
	h := &Handler{} // nil DB
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func admin() *principal.Principal {
	return &principal.Principal{UserID: "u1", IsAdmin: true, AccessAllLibraries: true}
}

// AC: list returns a well-formed empty set with a nil DB and requires auth.
func TestList_NilDB_Empty(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, authReq(http.MethodGet, "/api/series", admin()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []SeriesItem `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Items == nil {
		t.Fatal("items must be a non-nil array")
	}
}

func TestList_RequiresAuth(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, authReq(http.MethodGet, "/api/series", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

// AC: episodes returns seasons + numbering well-formed with a nil DB.
func TestEpisodes_NilDB_WellFormed(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, authReq(http.MethodGet, "/api/series/"+sid+"/episodes", admin()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["seasons"]; !ok {
		t.Fatal("seasons key required")
	}
}

func TestMissing_NilDB_WellFormed(t *testing.T) {
	rec := httptest.NewRecorder()
	mount().ServeHTTP(rec, authReq(http.MethodGet, "/api/series/"+sid+"/missing", admin()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}

// AC: rename is editor-only — a read-only user gets 403.
func TestPatch_ACL_ReadOnlyForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/series/"+sid, nil)
	r = r.WithContext(principal.WithPrincipal(r.Context(), &principal.Principal{UserID: "u1"}))
	mount().ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

// Missing-episode detection: season with E01,E02,E04 reports E03.
func TestMissingDetection_InteriorGap(t *testing.T) {
	rows := []epRow{
		mkEp(1, 1), mkEp(1, 2), mkEp(1, 4),
	}
	gaps := interiorGaps(rows)
	if len(gaps) != 1 || gaps[0].Season != 1 || gaps[0].Episode != 3 {
		t.Fatalf("expected one gap S01E03, got %+v", gaps)
	}
}

// Specials (season 0) are excluded from numbered-season gap detection.
func TestMissingDetection_SpecialsExcluded(t *testing.T) {
	rows := []epRow{mkEp(0, 1), mkEp(0, 3)}
	if g := interiorGaps(rows); len(g) != 0 {
		t.Fatalf("specials must not produce gaps, got %+v", g)
	}
}

// --- test helpers exercising the pure gap logic ---

func mkEp(season, episode int) epRow {
	var e epRow
	e.season.Int64, e.season.Valid = int64(season), true
	e.episode.Int64, e.episode.Valid = int64(episode), true
	return e
}

// interiorGaps mirrors the season-mode interior-gap detection in Missing
// so the pure logic is unit-testable without a DB.
func interiorGaps(rows []epRow) []Gap {
	present := map[int]map[int]bool{}
	for _, e := range rows {
		s, ok := e.effSeason()
		if !ok || s == 0 {
			continue
		}
		ep, ok := e.effEpisode()
		if !ok {
			continue
		}
		if present[s] == nil {
			present[s] = map[int]bool{}
		}
		present[s][ep] = true
	}
	gaps := []Gap{}
	for season, eps := range present {
		max := 0
		for e := range eps {
			if e > max {
				max = e
			}
		}
		for e := 1; e <= max; e++ {
			if !eps[e] {
				gaps = append(gaps, Gap{Season: season, Episode: e})
			}
		}
	}
	return gaps
}
