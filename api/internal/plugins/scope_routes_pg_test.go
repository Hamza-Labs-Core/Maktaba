package plugins

// README §15.12 on the plugin API's routes: a route that writes or reads by an id checks the
// id against the job, not only the token's libraries. A first-party job's token spans every
// library; its writes and its reads stay in the job's own library.

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
}

// asFirstPartyVideoJob makes the fixture's token a first-party video job's of videoA in libA,
// whose token spans every library (mintTrusted).
func (f *fixture) asFirstPartyVideoJob(t *testing.T, m *Minted) {
	t.Helper()
	f.s.Tokens.mu.Lock()
	inv := f.s.Tokens.byID[m.InvocationID]
	inv.SubjectKind, inv.SubjectID, inv.LibraryID, inv.AllLibraries, inv.Trusted = "video", f.videoA, f.libA, true, true
	inv.Grants = append(inv.Grants, "transcript.read")
	f.s.Tokens.mu.Unlock()
}

// Every read route that takes an id refuses another library's row from a job of one library
// whose token spans every library, and answers the job's own library's row.
func TestAReadRouteKeepsToTheJobsLibrary(t *testing.T) {
	f := newFixture(t)
	m := f.mint(t)
	f.asLibraryJob(t, m)
	f.s.Tokens.mu.Lock()
	inv := f.s.Tokens.byID[m.InvocationID]
	inv.Grants = append(inv.Grants, "plugin-data.read:tv.maktaba.other", "transcript.read")
	f.s.Tokens.mu.Unlock()
	var seriesA, seriesB string
	if err := f.db.QueryRow(`INSERT INTO series (library_id, name) VALUES ($1, 'A') RETURNING id::text`, f.libA).Scan(&seriesA); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`INSERT INTO series (library_id, name) VALUES ($1, 'B') RETURNING id::text`, f.libB).Scan(&seriesB); err != nil {
		t.Fatal(err)
	}
	video := func(id string) map[string]any { return map[string]any{"video": id} }
	data := func(id string) map[string]any {
		return map[string]any{"subject": map[string]string{"kind": "video", "id": id}, "key": "k"}
	}
	exported := func(id string) map[string]any {
		return map[string]any{"plugin": "tv.maktaba.other", "subject": map[string]string{"kind": "video", "id": id}, "key": "k"}
	}
	for _, c := range []struct {
		route       string
		mine, other map[string]any
	}{
		{"library/video", map[string]any{"id": f.videoB}, map[string]any{"id": f.videoC}},
		{"transcript/segments", video(f.videoB), video(f.videoC)},
		{"transcript/words", video(f.videoB), video(f.videoC)},
		{"media/info", video(f.videoB), video(f.videoC)},
		{"library/series", map[string]any{"id": seriesA}, map[string]any{"id": seriesB}},
		{"library/list", map[string]any{"library": f.libA}, map[string]any{"library": f.libB}},
		{"data/get", data(f.videoB), data(f.videoC)},
		{"data/list", data(f.videoB), data(f.videoC)},
		{"exports/read", exported(f.videoB), exported(f.videoC)},
	} {
		if code, cc, body := f.call(t, m.Token, c.route, c.other); code != 403 || cc != CodeScope {
			t.Errorf("%s of another library's row: %d %s %s", c.route, code, cc, body)
		}
		if code, cc, body := f.call(t, m.Token, c.route, c.mine); code != 200 {
			t.Errorf("%s of the job's library's row: %d %s %s", c.route, code, cc, body)
		}
	}
	// data keyed by a subject that is no row (the catalog), or by a video that is gone
	if code, cc, _ := f.call(t, m.Token, "data/get", map[string]any{"subject": map[string]string{"kind": "catalog", "id": "all"}, "key": "k"}); code != 200 {
		t.Errorf("data of the catalog: %d %s", code, cc)
	}
	if code, cc, _ := f.call(t, m.Token, "data/get", data("0b8f8a52-61a4-4d0a-9d8a-9a3f4bd2d001")); code != 200 {
		t.Errorf("data of a video that is gone: %d %s", code, cc)
	}
}

// A third-party plugin's data on a video of a library it was not granted is refused.
func TestDataOfAnUngrantedLibrarysVideoIsRefused(t *testing.T) {
	f := newFixture(t)
	tok := f.mint(t).Token
	for _, route := range []string{"data/get", "data/list"} {
		body := func(id string) map[string]any {
			return map[string]any{"subject": map[string]string{"kind": "video", "id": id}, "key": "k"}
		}
		if code, cc, _ := f.call(t, tok, route, body(f.videoC)); code != 403 || cc != CodeScope {
			t.Errorf("%s of an ungranted library's video: %d %s", route, code, cc)
		}
		if code, cc, _ := f.call(t, tok, route, body(f.videoB)); code != 200 {
			t.Errorf("%s of a granted library's video: %d %s", route, code, cc)
		}
	}
}

// A first-party video job reads its own video by id, not the same library's other video.
func TestAFirstPartyVideoJobReadsItsOwnVideo(t *testing.T) {
	f := newFixture(t)
	m := f.mint(t)
	f.asFirstPartyVideoJob(t, m)
	for _, route := range []string{"library/video", "transcript/segments", "transcript/words", "media/info"} {
		body := func(id string) map[string]any {
			if route == "library/video" {
				return map[string]any{"id": id}
			}
			return map[string]any{"video": id}
		}
		if code, cc, _ := f.call(t, m.Token, route, body(f.videoB)); code != 403 || cc != CodeScope {
			t.Errorf("%s of the library's other video: %d %s", route, code, cc)
		}
		if code, cc, b := f.call(t, m.Token, route, body(f.videoA)); code != 200 {
			t.Errorf("%s of the job's own video: %d %s %s", route, code, cc, b)
		}
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
