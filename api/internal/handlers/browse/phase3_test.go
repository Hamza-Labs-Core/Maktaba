package browse

// Epic 45 Phase 3: the length, added-date and resolution facets (45.20).

import (
	"net/http"
	"slices"
	"testing"
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
