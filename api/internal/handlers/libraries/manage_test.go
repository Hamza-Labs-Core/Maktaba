package libraries

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	_ "modernc.org/sqlite"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/libpaths"
)

const (
	libBig    = "11111111-1111-4111-8111-111111111111"
	libSmall  = "22222222-2222-4222-8222-222222222222"
	libNested = "33333333-3333-4333-8333-333333333333"
	libOther  = "44444444-4444-4444-8444-444444444444"
)

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// manageDB is the slice of the schema these routes read, by hand: the
// tests run on SQLite, production on Postgres.
func manageDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, roots TEXT NOT NULL,
			settings TEXT NOT NULL DEFAULT '{}', created_at TIMESTAMP, updated_at TIMESTAMP)`,
		`CREATE TABLE videos (id TEXT PRIMARY KEY, library_id TEXT NOT NULL, content_hash TEXT NOT NULL,
			path TEXT NOT NULL, state TEXT NOT NULL, UNIQUE (library_id, content_hash))`,
		`CREATE TABLE processing_jobs (id INTEGER PRIMARY KEY, subject_kind TEXT NOT NULL DEFAULT 'video',
			library_id TEXT, video_id TEXT, stage TEXT, state TEXT, created_at TIMESTAMP, finished_at TIMESTAMP, error TEXT)`,
		`CREATE TABLE library_roots (id INTEGER PRIMARY KEY, library_id TEXT NOT NULL, path TEXT NOT NULL,
			path_canonical TEXT NOT NULL, UNIQUE (library_id, path_canonical))`,
		`CREATE TABLE library_stats_cache (library_id TEXT PRIMARY KEY)`,
		`CREATE TABLE series (id TEXT PRIMARY KEY, library_id TEXT)`,
		`CREATE TABLE collections (id TEXT PRIMARY KEY, library_id TEXT NOT NULL, name TEXT NOT NULL,
			UNIQUE (library_id, name))`,
		`CREATE TABLE playback_state (video_id TEXT, user_id TEXT)`,
		`CREATE TABLE topic_collections (id TEXT PRIMARY KEY, library_id TEXT NOT NULL, playlist_id TEXT NOT NULL,
			name TEXT NOT NULL, UNIQUE (library_id, playlist_id))`,
		`CREATE TABLE channels (id TEXT PRIMARY KEY, library_id TEXT, number INTEGER NOT NULL, name TEXT NOT NULL,
			slug TEXT NOT NULL, UNIQUE (library_id, number), UNIQUE (library_id, slug))`,
		`CREATE TABLE library_acl (user_id TEXT, library_id TEXT, PRIMARY KEY (user_id, library_id))`,
		`CREATE TABLE user_library_pins (user_id TEXT NOT NULL, library_id TEXT NOT NULL, position INTEGER NOT NULL,
			created_at TEXT NOT NULL, PRIMARY KEY (user_id, library_id))`,
		`CREATE TABLE audit_log (id INTEGER PRIMARY KEY, category TEXT, action TEXT, actor_user_id TEXT,
			target_id TEXT, payload TEXT, ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func addLibrary(t *testing.T, db *sql.DB, id, name string, created time.Time, roots ...string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO libraries (id, name, roots, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)`,
		id, name, stringArray(roots), created)
}

func addVideo(t *testing.T, db *sql.DB, id, lib, hash, state string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO videos (id, library_id, content_hash, path, state) VALUES ($1, $2, $3, $4, $5)`,
		id, lib, hash, "/m/"+id+".mp4", state)
}

type fakeChecker map[string]error

func (f fakeChecker) Check(root string) error { return f[root] }

func call(t *testing.T, h *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	admin := &principal.Principal{UserID: "a", IsAdmin: true, AccessAllLibraries: true}
	req = req.WithContext(principal.WithPrincipal(context.Background(), admin))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// Manage reports each library's counts, latest scan and the folders it
// shares, and groups the libraries sharing folders with the one holding the
// most videos as the target (the home's three duplicate folders).
func TestManageReportsConflictsAndCounts(t *testing.T) {
	db := manageDB(t)
	addLibrary(t, db, libBig, "Big", t0, "/m/a")
	addLibrary(t, db, libSmall, "Small", t0.Add(time.Hour), "/m/a/")
	addLibrary(t, db, libNested, "Nested", t0, "/m/a/sub")
	addLibrary(t, db, libOther, "Other", t0, "/m/b")
	for i, st := range []string{"ready", "ready_no_audio", "missing", "failed", "corrupted", "probed", "superseded"} {
		addVideo(t, db, "b"+itoa(i), libBig, "hb"+itoa(i), st)
	}
	addVideo(t, db, "s1", libSmall, "hs1", "ready")
	mustExec(t, db, `INSERT INTO processing_jobs (id, subject_kind, library_id, stage, state, created_at, finished_at, error)
		VALUES (1, 'library', $1, 'scan', 'failed', $2, $2, 'boom'), (2, 'library', $1, 'scan', 'running', $3, NULL, NULL),
		       (3, 'library', $4, 'scan', 'done', $2, $3, NULL), (4, 'video', $1, 'probe', 'running', $3, NULL, NULL)`,
		libBig, t0, t0.Add(time.Hour), libOther)

	h := &Handler{DB: db, PathChecker: fakeChecker{"/m/b": errPathNotFound}}
	rec := call(t, h, "GET", "/api/libraries/manage", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp ManageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	byName := map[string]ManagedLibrary{}
	for _, l := range resp.Items {
		byName[l.Name] = l
	}
	big := byName["Big"]
	if want := (LibraryCounts{Total: 7, Missing: 1, Failed: 2, Ready: 2, Processing: 1}); big.Counts != want {
		t.Errorf("Big counts = %+v, want %+v", big.Counts, want)
	}
	if big.LastScan == nil || big.LastScan.JobID != "2" || !big.Scanning {
		t.Errorf("Big last scan = %+v scanning=%v, want job 2 running", big.LastScan, big.Scanning)
	}
	other := byName["Other"]
	if other.LastScan == nil || other.LastScan.State != "done" || other.Scanning || other.LastScan.FinishedAt == nil {
		t.Errorf("Other last scan = %+v", other.LastScan)
	}
	if r := other.Roots[0]; r.Reachable || r.Problem == nil || *r.Problem != "not-found" {
		t.Errorf("Other root = %+v, want unreachable not-found", r)
	}
	if len(other.Conflicts) != 0 {
		t.Errorf("Other conflicts = %+v", other.Conflicts)
	}
	if byName["Nested"].LastScan != nil {
		t.Errorf("Nested has a scan")
	}

	small := byName["Small"]
	var sameAsBig bool
	for _, c := range small.Conflicts {
		if c.LibraryID == libBig && c.Kind == "same" && c.OtherCount == 7 {
			sameAsBig = true
		}
	}
	if !sameAsBig {
		t.Errorf("Small conflicts = %+v, want same as Big (7)", small.Conflicts)
	}
	var nested bool
	for _, c := range byName["Nested"].Conflicts {
		if c.LibraryID == libBig && c.Kind == "nested" {
			nested = true
		}
	}
	if !nested {
		t.Errorf("Nested conflicts = %+v, want nested in Big", byName["Nested"].Conflicts)
	}
	if len(resp.ConflictGroups) != 1 {
		t.Fatalf("groups = %+v, want one", resp.ConflictGroups)
	}
	g := resp.ConflictGroups[0]
	if g.SuggestedTarget != libBig || g.Kind != "same" || len(g.Libraries) != 3 {
		t.Errorf("group = %+v, want Big as target of three", g)
	}
}

// Two libraries naming the same folder, one as the NAS's path and one as
// this machine's mount, are the same folder.
func TestManageConflictThroughPathMap(t *testing.T) {
	t.Setenv(libpaths.EnvPathMap, "/Volumes/Originals/=/Users/me/nas-nfs/")
	if k := conflictKind("/Volumes/Originals/Chan", "/Users/me/nas-nfs/Chan"); k != "same" {
		t.Errorf("kind = %q, want same", k)
	}
	if k := conflictKind("/Volumes/Originals/Chan", "/Users/me/nas-nfs/Chan/x"); k != "nested" {
		t.Errorf("kind = %q, want nested", k)
	}
}

// A merge moves the videos the target does not hold, deletes the ones it
// does, adds the folders it does not cover and deletes the merged library.
func TestMerge(t *testing.T) {
	db := manageDB(t)
	addLibrary(t, db, libBig, "Big", t0, "/m/a", "/m/c/sub")
	addLibrary(t, db, libSmall, "Small", t0, "/m/a", "/m/c", "/m/d")
	addVideo(t, db, "b1", libBig, "h1", "ready")
	addVideo(t, db, "s1", libSmall, "h1", "ready") // the same file as b1
	addVideo(t, db, "s2", libSmall, "h2", "ready")
	addVideo(t, db, "s3", libSmall, "h3", "missing")
	mustExec(t, db, `INSERT INTO library_stats_cache VALUES ($1), ($2)`, libBig, libSmall)
	mustExec(t, db, `INSERT INTO series VALUES ('ser', $1)`, libSmall)
	mustExec(t, db, `INSERT INTO collections VALUES ('c1', $1, 'Fav'), ('c2', $2, 'Fav'), ('c3', $2, 'Tafsir')`, libBig, libSmall)
	mustExec(t, db, `INSERT INTO topic_collections VALUES ('t1', $1, 'PL1', 'Fiqh'), ('t2', $2, 'PL1', 'Fiqh'), ('t3', $2, 'PL2', 'Seerah')`, libBig, libSmall)
	mustExec(t, db, `INSERT INTO channels VALUES ('ch1', $1, 1, 'Quran', 'quran'), ('ch2', $1, 2, 'Kids', 'kids'),
		('ch3', $2, 1, 'Talks', 'talks'), ('ch4', $2, 5, 'Quran', 'quran'), ('ch5', $2, 7, 'Misc', 'misc')`, libBig, libSmall)
	mustExec(t, db, `INSERT INTO library_acl VALUES ('viewer', $1)`, libSmall)
	// u1 pinned the merged library alone, u2 both
	mustExec(t, db, `INSERT INTO user_library_pins VALUES ('u1', $1, 3, 'x'), ('u2', $1, 0, 'x'), ('u2', $2, 1, 'x')`, libSmall, libBig)
	mustExec(t, db, `INSERT INTO processing_jobs (id, subject_kind, library_id, video_id, stage, state)
		VALUES (1, 'video', $1, 's2', 'probe', 'pending'), (2, 'library', $1, NULL, 'scan', 'done')`, libSmall)
	h := &Handler{DB: db, NowFunc: func() time.Time { return t0 }}

	if rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/merge", `{"into":"`+libBig+`","confirm":"small"}`); rec.Code != http.StatusPreconditionFailed {
		t.Errorf("wrong confirm: %d, want 412", rec.Code)
	}
	if rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/merge", `{"into":"`+libSmall+`","confirm":"Small"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("into itself: %d, want 422", rec.Code)
	}
	if rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/merge", `{"into":"`+libOther+`","confirm":"Small"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown target: %d, want 404", rec.Code)
	}
	if rec := call(t, h, "POST", "/api/libraries/"+libOther+"/merge", `{"into":"`+libBig+`","confirm":"Other"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d, want 404", rec.Code)
	}

	rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/merge", `{"into":"`+libBig+`","confirm":"Small"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body.String())
	}
	var resp MergeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Moved != 2 || resp.DuplicatesRemoved != 1 || resp.RootsAdded != 2 {
		t.Errorf("resp = %+v, want moved 2, duplicates 1, roots added 2", resp)
	}
	// /m/c holds Big's /m/c/sub and replaces it; /m/a was already Big's
	if got := strings.Join(resp.Into.Roots, ","); got != "/m/a,/m/c,/m/d" {
		t.Errorf("roots = %s", got)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM videos WHERE library_id = $1`, libBig); n != 3 {
		t.Errorf("Big has %d videos, want 3", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM videos WHERE id = 's1'`); n != 0 {
		t.Errorf("the duplicate is still there")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM libraries WHERE id = $1`, libSmall); n != 0 {
		t.Errorf("the merged library is still there")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM library_stats_cache`); n != 0 {
		t.Errorf("%d stats cache rows left", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM library_roots WHERE library_id = $1`, libBig); n != 3 {
		t.Errorf("library_roots has %d rows for Big, want 3", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM series WHERE library_id = $1`, libBig); n != 1 {
		t.Errorf("the series did not move")
	}
	// what clashes is renamed and kept, the rest moves as it is
	names := func(q string) string {
		t.Helper()
		rows, err := db.Query(q, libBig)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return strings.Join(out, ",")
	}
	if got := names(`SELECT name FROM collections WHERE library_id = $1 ORDER BY id`); got != "Fav,Fav (Small),Tafsir" {
		t.Errorf("collections = %s", got)
	}
	if got := names(`SELECT playlist_id || ':' || name FROM topic_collections WHERE library_id = $1 ORDER BY id`); got != "PL1:Fiqh,PL1-merged:Fiqh (Small),PL2:Seerah" {
		t.Errorf("topic collections = %s", got)
	}
	// Talks clashes on number 1 (next free after 2, then 7 moving as is: 3);
	// Quran on its slug; Misc moves untouched
	if got := names(`SELECT number || ':' || slug || ':' || name FROM channels WHERE library_id = $1 ORDER BY id`); got != "1:quran:Quran,2:kids:Kids,3:talks:Talks (Small),5:quran-2:Quran (Small),7:misc:Misc" {
		t.Errorf("channels = %s", got)
	}
	// a merge never widens access: the merged library's grant is not copied
	if n := count(t, db, `SELECT COUNT(*) FROM library_acl WHERE library_id = $1`, libBig); n != 0 {
		t.Errorf("a grant was copied to the target")
	}
	// the merged library's pins point at the target, in their place
	if got := names(`SELECT user_id || ':' || position FROM user_library_pins WHERE library_id = $1 ORDER BY user_id`); got != "u1:3,u2:1" {
		t.Errorf("pins of the target = %s, want u1:3,u2:1", got)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM processing_jobs WHERE library_id = $1`, libBig); n != 1 {
		t.Errorf("the video job did not move (or the scan job did)")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM audit_log WHERE action = 'library-merged' AND target_id = $1`, libBig); n != 1 {
		t.Errorf("no audit row")
	}
}

func TestEmptyTrash(t *testing.T) {
	db := manageDB(t)
	addLibrary(t, db, libBig, "Big", t0, "/m/a")
	addLibrary(t, db, libOther, "Other", t0, "/m/b")
	addVideo(t, db, "b1", libBig, "h1", "missing")
	addVideo(t, db, "b2", libBig, "h2", "ready")
	addVideo(t, db, "o1", libOther, "h3", "missing")
	h := &Handler{DB: db, PathChecker: fakeChecker{"/m/b": errPathNotFound}}
	// Other's folder is out of reach: its missing videos may be an unmounted
	// share, so nothing goes
	rec := call(t, h, "POST", "/api/libraries/"+libOther+"/empty-trash", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "library-roots-unreachable") ||
		!strings.Contains(rec.Body.String(), "/m/b") {
		t.Fatalf("unreachable folder: %d %s, want 409 naming /m/b", rec.Code, rec.Body.String())
	}
	if n := count(t, db, `SELECT COUNT(*) FROM videos WHERE library_id = $1`, libOther); n != 1 {
		t.Fatalf("a video of the unreachable library was deleted")
	}
	rec = call(t, h, "POST", "/api/libraries/"+libBig+"/empty-trash", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"removed":1`) {
		t.Fatalf("empty trash: %d %s", rec.Code, rec.Body.String())
	}
	if n := count(t, db, `SELECT COUNT(*) FROM videos`); n != 2 {
		t.Errorf("%d videos left, want 2 (another library's trash stays)", n)
	}
	if rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/empty-trash", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
}

// Impact counts the library's data; a table the database lacks counts 0.
func TestImpact(t *testing.T) {
	db := manageDB(t)
	addLibrary(t, db, libBig, "Big", t0, "/m/a")
	addVideo(t, db, "b1", libBig, "h1", "ready")
	addVideo(t, db, "b2", libBig, "h2", "ready")
	mustExec(t, db, `INSERT INTO playback_state VALUES ('b1', 'u1'), ('b1', 'u2'), ('x', 'u1')`)
	mustExec(t, db, `INSERT INTO series VALUES ('ser', $1)`, libBig)
	mustExec(t, db, `INSERT INTO library_acl VALUES ('u1', $1), ('u2', $1), ('u3', 'another')`, libBig)
	rec := call(t, &Handler{DB: db}, "GET", "/api/libraries/"+libBig+"/impact", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("impact: %d %s", rec.Code, rec.Body.String())
	}
	var got ImpactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := (ImpactResponse{Videos: 2, PlaybackStates: 2, Series: 1, Grants: 2}); got != want {
		t.Errorf("impact = %+v, want %+v", got, want)
	}
}

// The picker lists folders only, no hidden ones, sorted, in the database's
// form, and answers 404 for what it cannot open.
func TestFolders(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"b", "A", ".hidden", "c/deeper"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(libpaths.EnvPathMap, "/Volumes/NAS/="+root+"/")
	t.Setenv(EnvMediaRoots, "/Volumes/NAS/c")
	t.Setenv("HOME", t.TempDir())
	db := manageDB(t)
	addLibrary(t, db, libBig, "Big", t0, "/Volumes/NAS/b/chan")
	h := &Handler{DB: db}

	rec := call(t, h, "GET", "/api/libraries/folders?path=/Volumes/NAS", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var got FolderListing
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "/Volumes/NAS" || got.LocalPath != filepath.Clean(root) || got.Truncated {
		t.Errorf("listing = %+v", got)
	}
	var names []string
	for _, e := range got.Entries {
		names = append(names, e.Name+"="+e.Path)
	}
	if want := "A=/Volumes/NAS/A,b=/Volumes/NAS/b,c=/Volumes/NAS/c"; strings.Join(names, ",") != want {
		t.Errorf("entries = %v, want %s", names, want)
	}
	// a place's own folder has no parent: the picker never goes above its places
	if got.Parent != nil {
		t.Errorf("parent of a place = %q, want none", *got.Parent)
	}
	rec = call(t, h, "GET", "/api/libraries/folders?path=/Volumes/NAS/A", "")
	got = FolderListing{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("A: %d %s", rec.Code, rec.Body.String())
	}
	if got.Parent == nil || *got.Parent != "/Volumes/NAS" {
		t.Errorf("parent of A = %v, want /Volumes/NAS", got.Parent)
	}

	// the local form works too, and answers in the database's form
	rec = call(t, h, "GET", "/api/libraries/folders?path="+root+"/c", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"/Volumes/NAS/c/deeper"`) {
		t.Errorf("local form: %d %s", rec.Code, rec.Body.String())
	}
	// a folder outside every place is not listed, though it is there
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/Volumes/NAS/missing", "/Volumes/NAS/file.txt", "relative", outside, root + "/../" + filepath.Base(outside)} {
		if rec := call(t, h, "GET", "/api/libraries/folders?path="+p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, rec.Code)
		}
	}

	rec = call(t, h, "GET", "/api/libraries/folders", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("places: %d %s", rec.Code, rec.Body.String())
	}
	var places struct{ Places []Place }
	if err := json.Unmarshal(rec.Body.Bytes(), &places); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, p := range places.Places {
		paths = append(paths, p.Path)
	}
	joined := strings.Join(paths, ",")
	// the map's target, the media root, the library's parent folder, each once
	if !strings.HasPrefix(joined, "/Volumes/NAS,/Volumes/NAS/c,/Volumes/NAS/b") {
		t.Errorf("places = %v", paths)
	}
	if places.Places[0].Name != "NAS" {
		t.Errorf("place name = %q, want NAS", places.Places[0].Name)
	}
}

func TestOverlapDetail(t *testing.T) {
	for _, c := range []struct{ a, b, want string }{
		{"/m/a/", "/m/a", "same folder"},
		{"/m/a/x", "/m/a", "is inside"},
		{"/m", "/m/a", "contains"},
	} {
		if got := overlapDetail(c.a, c.b); !strings.Contains(got, c.want) {
			t.Errorf("overlapDetail(%s, %s) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// OSPathChecker checks a root in the database's form where this machine
// reaches it.
func TestOSPathCheckerUsesPathMap(t *testing.T) {
	root := t.TempDir()
	t.Setenv(libpaths.EnvPathMap, "/Volumes/NAS/="+root+"/")
	if err := (OSPathChecker{}).Check("/Volumes/NAS"); err != nil {
		t.Errorf("mapped root: %v", err)
	}
	if err := (OSPathChecker{}).Check("/Volumes/NAS/gone"); !errors.Is(err, errPathNotFound) {
		t.Errorf("missing mapped root: %v", err)
	}
}

// A folder that does not answer (a share gone away) gives up after the
// timeout instead of holding the request.
func TestFoldersTimeout(t *testing.T) {
	root := t.TempDir()
	t.Setenv(libpaths.EnvPathMap, "/Volumes/NAS/="+root+"/")
	release := make(chan struct{})
	defer close(release)
	oldList, oldTimeout := listFolders, folderListTimeout
	listFolders = func(string, []Place) ([]string, error) { <-release; return nil, nil }
	folderListTimeout = 20 * time.Millisecond
	t.Cleanup(func() { listFolders, folderListTimeout = oldList, oldTimeout })

	rec := call(t, &Handler{DB: manageDB(t)}, "GET", "/api/libraries/folders?path=/Volumes/NAS", "")
	if rec.Code != http.StatusGatewayTimeout || !strings.Contains(rec.Body.String(), "folder did not answer") {
		t.Fatalf("hung folder: %d %s, want 504", rec.Code, rec.Body.String())
	}
}

// blockingChecker never answers for its hung roots until released, and
// counts the checks it was asked for.
type blockingChecker struct {
	hung    map[string]bool
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (b *blockingChecker) Check(root string) error {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	if b.hung[root] {
		<-b.release
	}
	return nil
}

func (b *blockingChecker) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// A root that timed out is reported as such for a minute without a new
// check, so page loads do not pile up checks stuck on the same mount.
func TestCheckRootsRemembersATimeout(t *testing.T) {
	hung := "/hung-" + t.Name()
	ck := &blockingChecker{hung: map[string]bool{hung: true}, release: make(chan struct{})}
	defer close(ck.release)
	old := rootCheckTimeout
	rootCheckTimeout = 20 * time.Millisecond
	t.Cleanup(func() {
		rootCheckTimeout = old
		hungRoots.Lock()
		delete(hungRoots.until, hung)
		hungRoots.Unlock()
	})
	now := t0
	h := &Handler{PathChecker: ck, NowFunc: func() time.Time { return now }}

	if got := h.checkRoots([]string{hung, "/fine"}); got[hung] != errRootTimeout || got["/fine"] != nil {
		t.Fatalf("first check = %v", got)
	}
	if n := ck.count(); n != 2 {
		t.Fatalf("%d checks, want 2", n)
	}
	now = now.Add(30 * time.Second)
	if got := h.checkRoots([]string{hung, "/fine"}); got[hung] != errRootTimeout {
		t.Fatalf("second check = %v", got)
	}
	if n := ck.count(); n != 3 {
		t.Errorf("%d checks, want 3: the hung root is not checked again", n)
	}
	now = now.Add(hungRootFor)
	h.checkRoots([]string{hung})
	if n := ck.count(); n != 4 {
		t.Errorf("%d checks, want 4: after a minute the hung root is checked again", n)
	}
}

// Epic 47 (story 47.5): a merged video whose file the target already holds
// leaves its path, and the copies it had, as copies of the target's video.
func TestMergeKeepsTheDuplicatePathsAsCopies(t *testing.T) {
	db := manageDB(t)
	for _, s := range []string{
		`ALTER TABLE videos ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 7`,
		`ALTER TABLE videos ADD COLUMN mtime TIMESTAMP`,
		`CREATE TABLE video_copies (video_id TEXT NOT NULL, path TEXT NOT NULL, size_bytes INTEGER NOT NULL,
			mtime TIMESTAMP, seen_at TIMESTAMP NOT NULL, PRIMARY KEY (video_id, path))`,
	} {
		mustExec(t, db, s)
	}
	addLibrary(t, db, libBig, "Big", t0, "/m/a")
	addLibrary(t, db, libSmall, "Small", t0, "/m/b")
	addVideo(t, db, "b1", libBig, "h1", "ready")
	addVideo(t, db, "s1", libSmall, "h1", "ready") // the same file as b1
	addVideo(t, db, "s2", libSmall, "h2", "ready")
	mustExec(t, db, `INSERT INTO video_copies VALUES ('s1', '/m/b/again.mp4', 7, NULL, $1), ('s2', '/m/b/s2-copy.mp4', 7, NULL, $1)`, t0)
	h := &Handler{DB: db, NowFunc: func() time.Time { return t0 }}
	rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/merge", `{"into":"`+libBig+`","confirm":"Small"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body.String())
	}
	rows, err := db.Query(`SELECT video_id || ' ' || path FROM video_copies ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	want := "b1 /m/b/again.mp4,b1 /m/s1.mp4,s2 /m/b/s2-copy.mp4"
	if strings.Join(got, ",") != want {
		t.Errorf("copies = %v, want %s", got, want)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM videos WHERE id = 's1'`); n != 0 {
		t.Errorf("the duplicate row is still there")
	}
}
