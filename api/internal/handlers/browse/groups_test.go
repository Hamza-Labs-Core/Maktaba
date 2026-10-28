package browse

// The Series and Collections tabs (groups.go) on SQLite: the same SQL runs
// on both databases (the Postgres run is router/libtabs_pg_test.go).

import (
	"net/http"
	"slices"
	"testing"
)

const topicID = "00000000-0000-4000-8000-0000000007a1"

func groupsEnv(t *testing.T) *env {
	e := newEnv(t)
	for _, s := range []string{
		`ALTER TABLE series ADD COLUMN poster_path TEXT`,
		`CREATE TABLE topic_collections (id TEXT PRIMARY KEY, library_id TEXT, playlist_id TEXT, name TEXT, membership_hash TEXT)`,
		`CREATE TABLE topic_items (topic_id TEXT, video_id TEXT, position INTEGER, PRIMARY KEY (topic_id, video_id))`,
		`CREATE TABLE clip_groups (id TEXT PRIMARY KEY, library_id TEXT, kind TEXT, basis TEXT, name TEXT, topic TEXT, title TEXT, membership_hash TEXT)`,
		`CREATE TABLE clip_group_items (group_id TEXT, video_id TEXT, position INTEGER, PRIMARY KEY (group_id, video_id))`,
		`CREATE TABLE collections (id TEXT PRIMARY KEY, library_id TEXT, name TEXT, is_smart INTEGER NOT NULL DEFAULT 0,
			smart_query TEXT, hand_ordered INTEGER NOT NULL DEFAULT 0, dismissed_at TEXT)`,
		`CREATE TABLE collection_items (collection_id TEXT, video_id TEXT, position INTEGER, PRIMARY KEY (collection_id, video_id))`,
		`INSERT INTO topic_collections VALUES ('` + topicID + `', '` + libA + `', 'PL', 'الصيام', 'h')`,
		`INSERT INTO topic_items VALUES ('` + topicID + `', '` + v("a2") + `', 1), ('` + topicID + `', '` + v("a1") + `', 2), ('` + topicID + `', '` + v("a4") + `', 3)`,
		`INSERT INTO clip_groups VALUES ('g1', '` + libA + `', 'clips', 'rest', 'متنوعة', NULL, 'Alpha - مقاطع متنوعة', 'h'),
			('g2', '` + libB + `', 'standalone', 'rest', 'متنوعة', NULL, 'Beta - منفردات متنوعة', 'h')`,
		`INSERT INTO clip_group_items VALUES ('g1', '` + v("a3") + `', 1), ('g2', '` + v("b1") + `', 1)`,
		`INSERT INTO collections (id, library_id, name) VALUES ('c1', '` + libA + `', 'مختارات')`,
		`INSERT INTO collection_items VALUES ('c1', '` + v("a1") + `', 0), ('c1', '` + v("b1") + `', 1)`,
	} {
		if _, err := e.db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return e
}

func TestLibraryTabsSQLite(t *testing.T) {
	e := groupsEnv(t)
	var series struct {
		Items []LibrarySeriesItem
		Total int
		Next  *int
	}
	if code := e.do(asViewer(), "GET", "/api/libraries/"+libA+"/series?sort=recent", nil, &series); code != 200 {
		t.Fatalf("series: %d", code)
	}
	if len(series.Items) != 1 || series.Total != 1 || series.Next != nil {
		t.Fatalf("series %+v", series)
	}
	// Tafsir: a1 and a2 visible (a4 is missing); the viewer finished a2, a1 under way
	if s := series.Items[0]; s.Name != "Tafsir" || s.Episodes != 2 || s.Watched != 1 || s.InProgress != 1 ||
		s.PosterVideoID != v("a1") || s.LastWatchedAt == nil || s.LatestAddedAt == nil {
		t.Errorf("Tafsir card %+v", s)
	}

	var cols struct {
		Items  []LibraryCollection
		Counts map[string]int
	}
	if code := e.do(asViewer(), "GET", "/api/libraries/"+libA+"/collections", nil, &cols); code != 200 {
		t.Fatalf("collections: %d", code)
	}
	var got []string
	for _, c := range cols.Items {
		got = append(got, c.Kind+":"+c.Name)
	}
	if !slices.Equal(got, []string{"topic:الصيام", "clip:متنوعة", "curated:مختارات"}) {
		t.Fatalf("collections %v", got)
	}
	if c := cols.Items[0]; *c.Videos != 2 || !slices.Equal(c.PosterVideoIDs, names("a2", "a1")) || !c.Automatic {
		t.Errorf("topic card %+v", c)
	}
	if c := cols.Items[2]; *c.Videos != 1 || c.Automatic {
		t.Errorf("curated card %+v (b1 is not the viewer's)", c)
	}
	if cols.Counts["filler"] != 1 || cols.Counts["standalone"] != 0 || cols.Counts["all"] != 3 {
		t.Errorf("counts %v", cols.Counts)
	}

	var page struct {
		Collection AutoCollectionRef
		Items      []Card
	}
	if code := e.do(asViewer(), "GET", "/api/libraries/"+libA+"/collections/topic/"+topicID, nil, &page); code != 200 {
		t.Fatalf("topic page: %d", code)
	}
	if page.Collection.Name != "الصيام" || page.Collection.Videos != 2 || !slices.Equal(ids(page.Items), names("a2", "a1")) {
		t.Errorf("topic page %+v %v", page.Collection, ids(page.Items))
	}
	if c := page.Items[1]; c.Completed == nil || *c.Completed || c.PositionSec == nil {
		t.Errorf("the viewer's playback of a1 %+v", c)
	}
	for _, path := range []string{"/series", "/collections", "/collections/clip/00000000-0000-4000-8000-000000000001"} {
		if code := e.do(asViewer(), "GET", "/api/libraries/"+libB+path, nil, nil); code != http.StatusNotFound {
			t.Errorf("viewer, library B %s: %d, want 404", path, code)
		}
	}
	if code := e.do(nil, "GET", "/api/libraries/"+libA+"/series", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", code)
	}
}
