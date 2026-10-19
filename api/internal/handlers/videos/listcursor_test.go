package videos

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// listDB is a SQLite videos table with the columns List reads. The time
// columns are DATETIME so the driver hands back time.Time.
func listDB(t *testing.T, titles []string, sameTime bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE videos (
		id TEXT PRIMARY KEY, library_id TEXT, path TEXT, filename TEXT,
		title TEXT, description TEXT, state TEXT, detected_language TEXT,
		duration_sec REAL, size_bytes INTEGER, content_hash TEXT, metadata BLOB,
		created_at DATETIME, updated_at DATETIME)`); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, title := range titles {
		ts := base
		if !sameTime {
			ts = base.Add(time.Duration(i) * time.Minute)
		}
		var tv any = title
		if title == "" {
			tv = nil
		}
		if _, err := db.Exec(`INSERT INTO videos VALUES ($1,'lib','p','f',$2,NULL,'ready',NULL,NULL,0,$3,NULL,$4,$4)`,
			fmt.Sprintf("00000000-0000-0000-0000-%012d", i), tv, fmt.Sprintf("h%d", i), ts); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func listPage(t *testing.T, h *Handler, qs url.Values) (ids []string, next *string, code int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/videos?"+qs.Encode(), nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u", IsAdmin: true, AccessAllLibraries: true}))
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusOK {
		return nil, nil, rr.Code
	}
	var body struct {
		Items []Video `json:"items"`
		Next  *string `json:"next"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, v := range body.Items {
		ids = append(ids, v.ID)
	}
	return ids, body.Next, rr.Code
}

// walk follows `next` until the last page and returns every id seen.
func walk(t *testing.T, h *Handler, sort string) []string {
	t.Helper()
	var all []string
	qs := url.Values{"sort": {sort}, "limit": {"2"}}
	for range 10 {
		ids, next, code := listPage(t, h, qs)
		if code != http.StatusOK {
			t.Fatalf("status %d", code)
		}
		all = append(all, ids...)
		if next == nil {
			return all
		}
		qs.Set("cursor", *next)
	}
	t.Fatal("pagination did not end")
	return nil
}

func assertEachOnce(t *testing.T, got []string, n int) {
	t.Helper()
	if len(got) != n {
		t.Fatalf("got %d rows %v, want %d", len(got), got, n)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("row %s repeated: %v", id, got)
		}
		seen[id] = true
	}
}

// LIB "Load more": the next page must continue after the cursor, not
// repeat page 1 (the handler used to ignore ?cursor=).
func TestListCursorPagesWithoutRepeats(t *testing.T) {
	titles := []string{"b", "a", "", "d", "c"}
	for _, tc := range []struct {
		sort     string
		sameTime bool
	}{
		{"updated_at", false},
		{"updated_at", true}, // every row has the same time: the id breaks ties
		{"created_at", false},
		{"title", false}, // includes a NULL title
	} {
		t.Run(fmt.Sprintf("%s/same=%v", tc.sort, tc.sameTime), func(t *testing.T) {
			h := &Handler{DB: listDB(t, titles, tc.sameTime)}
			assertEachOnce(t, walk(t, h, tc.sort), len(titles))
		})
	}
}

func TestListCursorRejectsBadOrForeignCursor(t *testing.T) {
	h := &Handler{DB: listDB(t, []string{"a", "b", "c"}, false)}
	_, next, _ := listPage(t, h, url.Values{"sort": {"updated_at"}, "limit": {"1"}})
	if next == nil {
		t.Fatal("want a next cursor")
	}
	for name, qs := range map[string]url.Values{
		"garbage":     {"cursor": {"%%%"}},
		"bad library": {"library": {"not-a-uuid"}}, // LIB-09: used to list every video
		"bad id":      {"cursor": {encodeListCursor("updated_at", Video{ID: "x"})}},
		"other sort":  {"sort": {"title"}, "cursor": {*next}},
	} {
		if _, _, code := listPage(t, h, qs); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
}
