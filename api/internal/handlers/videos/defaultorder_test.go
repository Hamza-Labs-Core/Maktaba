package videos

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// orderRow is one video of orderDB: added is created_at in minutes after a
// base time, changed updated_at (changed in the reverse order, so a list by
// updated_at is never the newest-added one by accident).
type orderRow struct {
	title, description string
	added, changed     int
}

func orderDB(t *testing.T, rows []orderRow) *sql.DB {
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
		created_at DATETIME, updated_at DATETIME, mtime DATETIME)`); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for i, r := range rows {
		if _, err := db.Exec(`INSERT INTO videos VALUES ($1,'lib','p','f',$2,$3,'ready',NULL,NULL,0,$4,NULL,$5,$6,$5)`,
			orderID(i), r.title, r.description, fmt.Sprintf("h%d", i),
			base.Add(time.Duration(r.added)*time.Minute), base.Add(time.Duration(r.changed)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func orderID(i int) string { return fmt.Sprintf("00000000-0000-0000-0000-%012d", i) }

// walkQuery follows `next` from qs (limit 1: every row is a page boundary)
// and returns every id in order.
func walkQuery(t *testing.T, h *Handler, qs url.Values) []string {
	t.Helper()
	qs.Set("limit", "1")
	var all []string
	for range 20 {
		ids, next, code := listPage(t, h, qs)
		if code != http.StatusOK {
			t.Fatalf("%v: status %d", qs, code)
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

func wantIDs(t *testing.T, name string, got []string, idx ...int) {
	t.Helper()
	want := make([]string, len(idx))
	for i, n := range idx {
		want[i] = orderID(n)
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// Every app's lists open newest-added first: a list with no sort is by
// created_at, newest first, with the id breaking ties, page after page.
func TestListWithoutSortIsNewestAddedFirst(t *testing.T) {
	h := &Handler{DB: orderDB(t, []orderRow{
		{title: "a", added: 1, changed: 9},
		{title: "b", added: 3, changed: 7},
		{title: "c", added: 2, changed: 8},
		{title: "d", added: 3, changed: 6}, // same added time as b: the id decides
		{title: "e", added: 0, changed: 10},
	})}
	wantIDs(t, "no sort", walkQuery(t, h, url.Values{}), 3, 1, 2, 0, 4)
	wantIDs(t, "sort=created_at", walkQuery(t, h, url.Values{"sort": {"created_at"}}), 3, 1, 2, 0, 4)
	// a cursor of the default is the created_at one: either continues the other
	ids, next, _ := listPage(t, h, url.Values{"limit": {"2"}})
	wantIDs(t, "first page", ids, 3, 1)
	rest := walkQuery(t, h, url.Values{"sort": {"created_at"}, "cursor": {*next}})
	wantIDs(t, "continued with sort=created_at", rest, 2, 0, 4)
}

// A sort the person picked wins over the default, with or without a search.
func TestListExplicitSortWinsOverTheDefault(t *testing.T) {
	h := &Handler{DB: orderDB(t, []orderRow{
		{title: "b go", added: 1, changed: 3},
		{title: "go a", added: 3, changed: 1},
		{title: "c", description: "go", added: 2, changed: 2},
	})}
	wantIDs(t, "updated_at", walkQuery(t, h, url.Values{"sort": {"updated_at"}}), 0, 2, 1)
	wantIDs(t, "title", walkQuery(t, h, url.Values{"sort": {"title"}, "order": {"asc"}}), 0, 2, 1)
	wantIDs(t, "created_at asc", walkQuery(t, h, url.Values{"sort": {"created_at"}, "order": {"asc"}}), 0, 2, 1)
	wantIDs(t, "q with title", walkQuery(t, h, url.Values{"q": {"go"}, "sort": {"title"}, "order": {"asc"}}), 0, 2, 1)
	wantIDs(t, "q with created_at", walkQuery(t, h, url.Values{"q": {"go"}, "sort": {"created_at"}}), 1, 2, 0)
}

// A search of the list (?q= and no sort) is by relevance: titles that start
// with q, then titles that hold it, then a match in the description only;
// newest-added first among each, and the cursor crosses the ranks.
func TestListSearchIsByRelevanceThenNewestAdded(t *testing.T) {
	h := &Handler{DB: orderDB(t, []orderRow{
		{title: "intro", description: "go", added: 9}, // 0: description only, newest of all
		{title: "learn go", added: 1},                 // 1: holds it
		{title: "go basics", added: 2},                // 2: starts with it
		{title: "go deeper", added: 5},                // 3: starts with it, newer
		{title: "more go", added: 4},                  // 4: holds it, newer
		{title: "rust", added: 8},                     // 5: no match
		{title: "go again", added: 5},                 // 6: same rank and time as 3: the id decides
	})}
	want := []int{6, 3, 2, 4, 1, 0}
	wantIDs(t, "no sort", walkQuery(t, h, url.Values{"q": {"go"}}), want...)
	wantIDs(t, "sort=relevance", walkQuery(t, h, url.Values{"q": {"go"}, "sort": {"relevance"}}), want...)
	// order does not turn a relevance list upside down
	wantIDs(t, "order=asc", walkQuery(t, h, url.Values{"q": {"go"}, "order": {"asc"}}), want...)
	// and the rank sits next to the jump bar's letter filter
	wantIDs(t, "letter", walkQuery(t, h, url.Values{"q": {"go"}, "letter": {"l"}}), 1)

	for name, qs := range map[string]url.Values{
		"relevance without q": {"sort": {"relevance"}},
		"a created_at cursor": {"q": {"go"}, "cursor": {encodeListCursor("created_at", "desc", Video{ID: orderID(1)})}},
		"a rank out of range": {"q": {"go"}, "cursor": {encodeListCursor(sortRelevance, "desc", Video{ID: orderID(1), rank: 7})}},
	} {
		if _, _, code := listPage(t, h, qs); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
}
