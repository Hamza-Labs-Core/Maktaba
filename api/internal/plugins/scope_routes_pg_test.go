package plugins

// README §15.12 on the plugin API's routes: a route that writes by an id checks the id
// against the job, not only the token's libraries. A first-party job's token spans every
// library (for its reads); its writes stay in the job's own library.

import (
	"testing"
)

// asLibraryJob makes the fixture's token a library job's of libA whose token spans every
// library, as a first-party job's does (mintTrusted).
func (f *fixture) asLibraryJob(t *testing.T, m *Minted) {
	t.Helper()
	f.s.Tokens.mu.Lock()
	inv := f.s.Tokens.byID[m.InvocationID]
	inv.SubjectKind, inv.SubjectID, inv.LibraryID, inv.AllLibraries = "library", f.libA, f.libA, true
	f.s.Tokens.mu.Unlock()
}

func TestAWriteRouteKeepsToTheJobsLibrary(t *testing.T) {
	f := newFixture(t)
	m := f.mint(t)
	f.asLibraryJob(t, m)
	if code, c, _ := f.call(t, m.Token, "write/add-tags", map[string]any{"video": f.videoC, "tags": []string{"x"}}); code != 403 || c != CodeScope {
		t.Fatalf("another library's video from a library job whose token spans every library: %d %s", code, c)
	}
	if code, c, _ := f.call(t, m.Token, "write/add-tags", map[string]any{"video": f.videoB, "tags": []string{"x"}}); code != 200 {
		t.Fatalf("a video of the job's library: %d %s", code, c)
	}
	// a read stays open to every library the token spans
	if code, c, _ := f.call(t, m.Token, "library/video", map[string]any{"id": f.videoC}); code != 200 {
		t.Fatalf("a read of another library's video: %d %s", code, c)
	}
}

func TestASuggestionIsAboutASubjectOfTheJobsScope(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	var seriesA, seriesB, seasonB string
	if err := f.db.QueryRow(`INSERT INTO series (library_id, name) VALUES ($1, 'A') RETURNING id::text`, f.libA).Scan(&seriesA); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`INSERT INTO series (library_id, name) VALUES ($1, 'B') RETURNING id::text`, f.libB).Scan(&seriesB); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`INSERT INTO series_seasons (id, series_id, library_id, season, kind, name, membership_hash, detector_version)
		VALUES (gen_random_uuid(), $1, $2, 1, 'episodic', 'one', 'h', 1) RETURNING id::text`, seriesB, f.libB).Scan(&seasonB); err != nil {
		t.Fatal(err)
	}
	suggest := func(kind, id string) (int, string) {
		code, c, _ := f.call(t, tok, "suggest/add", map[string]any{"subject": map[string]string{"kind": kind, "id": id},
			"field": "title", "value": "Better", "reason": "x"})
		return code, c
	}
	// the grant is libA's only: libB's library, series and season are refused
	for _, c := range []struct{ kind, id string }{{"library", f.libB}, {"series", seriesB}, {"season", seasonB}} {
		if code, cc := suggest(c.kind, c.id); code != 403 || cc != CodeScope {
			t.Errorf("a suggestion on libB's %s: %d %s", c.kind, code, cc)
		}
	}
	if code, cc := suggest("series", seriesA); code != 200 {
		t.Errorf("a suggestion on libA's series: %d %s", code, cc)
	}
	if code, cc := suggest("library", f.libA); code != 200 {
		t.Errorf("a suggestion on libA: %d %s", code, cc)
	}
	// a kind with no row the API resolves
	if code, cc := suggest("dup_group", seriesA); code != 400 || cc != CodeInvalid {
		t.Errorf("a suggestion on a dup_group: %d %s", code, cc)
	}
}
