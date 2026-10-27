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
