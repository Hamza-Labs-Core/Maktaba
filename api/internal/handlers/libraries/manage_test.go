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
	// the clashing "Fav" stays behind (it goes with the merged library); "Tafsir" moves
	if n := count(t, db, `SELECT COUNT(*) FROM collections WHERE library_id = $1`, libBig); n != 2 {
		t.Errorf("Big has %d collections, want 2", n)
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
	h := &Handler{DB: db}
	rec := call(t, h, "POST", "/api/libraries/"+libBig+"/empty-trash", "")
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
	rec := call(t, &Handler{DB: db}, "GET", "/api/libraries/"+libBig+"/impact", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("impact: %d %s", rec.Code, rec.Body.String())
	}
	var got ImpactResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if want := (ImpactResponse{Videos: 2, PlaybackStates: 2, Series: 1}); got != want {
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
	if got.Parent == nil {
		t.Errorf("no parent")
	}

	// the local form works too, and answers in the database's form
	rec = call(t, h, "GET", "/api/libraries/folders?path="+root+"/c", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"/Volumes/NAS/c/deeper"`) {
		t.Errorf("local form: %d %s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"/Volumes/NAS/missing", "/Volumes/NAS/file.txt", "relative"} {
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
