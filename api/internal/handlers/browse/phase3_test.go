package browse

// Epic 45 Phase 3: the length, added-date and resolution facets (45.20).

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

type bandFacetsBody struct {
	Durations   []BandFacet `json:"durations"`
	Added       []BandFacet `json:"added"`
	Resolutions []BandFacet `json:"resolutions"`
}

func TestBandFacets(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{
		`INSERT INTO media_info VALUES ('` + v("a1") + `',1920,1080), ('` + v("a2") + `',640,480),
			('` + v("a4") + `',1280,720), ('` + v("b1") + `',3840,2160)`,
		// a2 was added 40 days ago: in the year, not the month
		`UPDATE videos SET created_at = '` + iso(now.AddDate(0, 0, -40)) + `' WHERE id = '` + v("a2") + `'`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var f bandFacetsBody
	if code := e.do(asViewer(), "GET", "/api/videos/facets", nil, &f); code != 200 {
		t.Fatal(code)
	}
	// library A's visible videos: a1 (1000 s, 1080p), a2 (600 s, 480p), a3 (no
	// length, no media_info); a4 (720p) is missing, so no "hd"
	if want := []BandFacet{{"short", 1}, {"medium", 1}}; !slices.Equal(f.Durations, want) {
		t.Errorf("durations %+v, want %+v", f.Durations, want)
	}
	if want := []BandFacet{{"week", 2}, {"month", 2}, {"year", 3}}; !slices.Equal(f.Added, want) {
		t.Errorf("added %+v, want %+v", f.Added, want)
	}
	if want := []BandFacet{{"sd", 1}, {"fhd", 1}}; !slices.Equal(f.Resolutions, want) {
		t.Errorf("resolutions %+v, want %+v (never library B's 4K)", f.Resolutions, want)
	}
	if code := e.do(asAdmin(), "GET", "/api/videos/facets?library="+libB, nil, &f); code != http.StatusOK {
		t.Fatal(code)
	}
	if want := []BandFacet{{"uhd", 1}}; !slices.Equal(f.Resolutions, want) {
		t.Errorf("admin, library B: resolutions %+v, want %+v", f.Resolutions, want)
	}
	if want := []BandFacet{{"medium", 2}}; !slices.Equal(f.Durations, want) {
		t.Errorf("admin, library B: durations %+v, want %+v", f.Durations, want)
	}
}

func entryShape(es []AddedEntry) []string {
	out := []string{}
	for _, e := range es {
		s := e.Kind + ":" + e.Video.ID
		if e.Series != nil {
			s += ":" + e.Series.Name + ":" + string(rune('0'+e.Count))
		}
		out = append(out, s)
	}
	return out
}

func TestRecentlyAddedGrouped(t *testing.T) {
	e := newEnv(t)
	var home struct {
		Grouped []AddedEntry `json:"recently_added_grouped"`
		Added   []Card       `json:"recently_added"`
	}
	if code := e.do(asViewer(), "GET", "/api/home", nil, &home); code != 200 {
		t.Fatal(code)
	}
	// a3 (no series), then Tafsir's two visible lessons as one entry at a2,
	// its newest (a4 is missing: never counted)
	want := []string{"video:" + v("a3"), "series:" + v("a2") + ":Tafsir:2"}
	if got := entryShape(home.Grouped); !slices.Equal(got, want) {
		t.Errorf("viewer grouped %v, want %v", got, want)
	}
	if len(home.Added) != 3 {
		t.Errorf("recently_added stays as it was: %v", ids(home.Added))
	}

	home.Grouped = nil // a fresh decode: omitted fields would keep the viewer's
	e.do(asAdmin(), "GET", "/api/home", nil, &home)
	// b1 is the only new lesson of its series: a video entry that names it
	want = []string{"video:" + v("b2"), "video:" + v("b1") + ":B renamed:0", "video:" + v("a3"), "series:" + v("a2") + ":Tafsir:2"}
	if got := entryShape(home.Grouped); !slices.Equal(got, want) {
		t.Errorf("admin grouped %v, want %v", got, want)
	}

	var hub struct {
		Rows struct {
			Grouped []AddedEntry `json:"recently_added_grouped"`
		} `json:"rows"`
	}
	if code := e.do(asAdmin(), "GET", "/api/libraries/"+libB+"/hub", nil, &hub); code != 200 {
		t.Fatal(code)
	}
	want = []string{"video:" + v("b2"), "video:" + v("b1") + ":B renamed:0"}
	if got := entryShape(hub.Rows.Grouped); !slices.Equal(got, want) {
		t.Errorf("hub B grouped %v, want %v", got, want)
	}
}

func TestFeatured(t *testing.T) {
	e := newEnv(t)
	if _, err := e.db.Exec(`UPDATE series SET created_at = CASE id WHEN $1 THEN '2026-01-01T00:00:00Z' ELSE '2026-06-01T00:00:00Z' END`, seriesA); err != nil {
		t.Fatal(err)
	}
	type homeDoc struct {
		Featured *Featured `json:"featured"`
	}
	hero := func(p *principal.Principal) *Featured {
		t.Helper()
		var h homeDoc
		if code := e.do(p, "GET", "/api/home", nil, &h); code != 200 {
			t.Fatal(code)
		}
		return h.Featured
	}
	// the newest series the caller may see, from its first lesson
	if f := hero(asViewer()); f == nil || f.Kind != "series" || f.Series.ID != seriesA || f.Video.ID != v("a1") || f.Series.Episodes != 2 || f.Picked {
		t.Errorf("viewer default %+v", f)
	}
	if f := hero(asAdmin()); f == nil || f.Series == nil || f.Series.ID != seriesB || f.Series.Name != "B renamed" || f.Video.ID != v("b1") {
		t.Errorf("admin default %+v", f)
	}

	// only admins read or pick
	for _, p := range []*principal.Principal{asViewer(), nil} {
		want := http.StatusForbidden
		if p == nil {
			want = http.StatusUnauthorized
		}
		if code := e.do(p, "GET", "/api/home/featured", nil, nil); code != want {
			t.Errorf("GET featured as %v: %d, want %d", p, code, want)
		}
		if code := e.do(p, "PUT", "/api/home/featured", map[string]any{"video_id": v("a2")}, nil); code != want {
			t.Errorf("PUT featured as %v: %d, want %d", p, code, want)
		}
	}
	for _, id := range []string{v("a4"), unknownL, "nope"} { // missing, unknown, malformed
		if code := e.do(asAdmin(), "PUT", "/api/home/featured", map[string]any{"video_id": id}, nil); code != http.StatusNotFound {
			t.Errorf("pick %s: %d, want 404", id, code)
		}
	}

	// a pick in library B: the admin sees it, the viewer of A keeps the default
	var got struct {
		VideoID *string `json:"video_id"`
		Video   *Card   `json:"video"`
	}
	if code := e.do(asAdmin(), "PUT", "/api/home/featured", map[string]any{"video_id": v("b2")}, &got); code != 200 || got.VideoID == nil || *got.VideoID != v("b2") || got.Video == nil {
		t.Fatalf("pick b2: %d %+v", code, got)
	}
	if f := hero(asAdmin()); f == nil || f.Kind != "video" || f.Video.ID != v("b2") || !f.Picked || f.Series != nil {
		t.Errorf("admin picked %+v", f)
	}
	if f := hero(asViewer()); f == nil || f.Kind != "series" || f.Video.ID != v("a1") {
		t.Errorf("viewer with a pick they may not see: %+v", f)
	}

	// a pick in a series names it
	e.do(asAdmin(), "PUT", "/api/home/featured", map[string]any{"video_id": v("a2")}, nil)
	if f := hero(asViewer()); f == nil || f.Kind != "video" || f.Video.ID != v("a2") || f.Series == nil || f.Series.Name != "Tafsir" {
		t.Errorf("viewer picked %+v", f)
	}

	// null goes back to the newest series
	got.VideoID = nil
	if code := e.do(asAdmin(), "PUT", "/api/home/featured", map[string]any{"video_id": nil}, &got); code != 200 || got.VideoID != nil {
		t.Errorf("clear: %d %+v", code, got)
	}
	if f := hero(asViewer()); f == nil || f.Picked {
		t.Errorf("after clearing %+v", f)
	}
}
