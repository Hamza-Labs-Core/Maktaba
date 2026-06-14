package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

const vidID = "11111111-1111-1111-1111-111111111111"

// stubYT is a deterministic YouTubeSearcher for tests.
type stubYT struct {
	items []YTItem
	err   error
}

func (s stubYT) SearchYouTube(_ context.Context, _ string) ([]YTItem, error) {
	return s.items, s.err
}

// AC test_disabled_or_keyless_noop: no searcher ⇒ empty block, reason
// "disabled", and crucially the searcher is never consulted.
func TestFetchYouTube_Disabled(t *testing.T) {
	h := &Handler{} // nil YouTube
	block := h.fetchYouTube(context.Background(), "matrix")
	if block == nil || block.Reason != "disabled" || len(block.Items) != 0 {
		t.Fatalf("got %+v", block)
	}
}

// AC test_search_with_youtube_block: a populated stub yields items; with
// a nil DB every item is annotated "importable".
func TestFetchYouTube_Populated_Importable(t *testing.T) {
	h := &Handler{YouTube: stubYT{items: []YTItem{{YouTubeID: "abc", Title: "The Matrix"}}}}
	block := h.fetchYouTube(context.Background(), "matrix")
	if len(block.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(block.Items))
	}
	if block.Items[0].Match.State != "importable" {
		t.Fatalf("want importable, got %q", block.Items[0].Match.State)
	}
}

// AC test_youtube_failure_does_not_block_local: an adapter error maps to
// an empty block with a reason, never an error.
func TestFetchYouTube_Error_Reason(t *testing.T) {
	h := &Handler{YouTube: stubYT{err: ErrYouTubeRateLimited}}
	block := h.fetchYouTube(context.Background(), "x")
	if block.Reason != "rate_limited" || len(block.Items) != 0 {
		t.Fatalf("got %+v", block)
	}
	h2 := &Handler{YouTube: stubYT{err: context.DeadlineExceeded}}
	if h2.fetchYouTube(context.Background(), "x").Reason != "error" {
		t.Fatal("unknown error should map to reason=error")
	}
}

func TestYoutubeReason_Mapping(t *testing.T) {
	cases := map[error]string{
		ErrYouTubeNoKey:       "no_key",
		ErrYouTubeRateLimited: "rate_limited",
		ErrYouTubeDisabled:    "disabled",
	}
	for err, want := range cases {
		if got := youtubeReason(err); got != want {
			t.Errorf("%v -> %q want %q", err, got, want)
		}
	}
}

func TestQueryHash_Stable_CaseInsensitive(t *testing.T) {
	if queryHash("The Matrix") != queryHash("the matrix ") {
		t.Fatal("hash should normalise case + trim")
	}
}

// includes reports membership without false positives.
func TestRequest_Includes(t *testing.T) {
	r := Request{Include: []string{"youtube"}}
	if !r.includes("youtube") {
		t.Fatal("should include youtube")
	}
	if (Request{}).includes("youtube") {
		t.Fatal("empty include must be false")
	}
}

func ytReq(method, target, body string, admin bool) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	return r.WithContext(principal.WithPrincipal(r.Context(),
		&principal.Principal{UserID: "u1", IsAdmin: admin, AccessAllLibraries: admin}))
}

func ytMount() chi.Router {
	h := &Handler{} // nil DB, nil YouTube
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

// AC test_import_acl: a read-only user gets 403 on import.
func TestImportYouTube_ACL(t *testing.T) {
	rec := httptest.NewRecorder()
	ytMount().ServeHTTP(rec, ytReq(http.MethodPost, "/api/videos/"+vidID+"/import-youtube",
		`{"youtube_id":"abc"}`, false))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d", rec.Code)
	}
}

// Import requires youtube_id.
func TestImportYouTube_RequiresID(t *testing.T) {
	rec := httptest.NewRecorder()
	ytMount().ServeHTTP(rec, ytReq(http.MethodPost, "/api/videos/"+vidID+"/import-youtube", `{}`, true))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// SearchYouTube GET proxy returns a disabled block (no searcher) but 200.
func TestSearchYouTube_GET_Disabled(t *testing.T) {
	rec := httptest.NewRecorder()
	ytMount().ServeHTTP(rec, ytReq(http.MethodGet, "/api/search/youtube?q=matrix", "", false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	var block YTBlock
	if err := json.Unmarshal(rec.Body.Bytes(), &block); err != nil {
		t.Fatal(err)
	}
	if block.Reason != "disabled" {
		t.Fatalf("want disabled, got %q", block.Reason)
	}
}
