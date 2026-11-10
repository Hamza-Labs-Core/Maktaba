package libraries

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/mediaaccess"
)

// fakeAccess answers each root's check from a table and counts the checks: every check is a
// probe write on a real disk, so the tests pin when one runs.
type fakeAccess struct {
	mu    sync.Mutex
	res   map[string]mediaaccess.Result
	calls int
}

func (f *fakeAccess) Check(root string) mediaaccess.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if r, ok := f.res[root]; ok {
		return r
	}
	return mediaaccess.Result{Path: root, Access: mediaaccess.ReadWrite}
}

func (f *fakeAccess) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func accessHandler(t *testing.T, fa *fakeAccess) *Handler {
	t.Helper()
	return &Handler{DB: manageDB(t), PathChecker: fakeChecker{}, Access: fa,
		NowFunc:  func() time.Time { return t0 },
		Platform: func() string { return "dsm" },
		RunsAs:   func() mediaaccess.RunsAs { return mediaaccess.RunsAs{UID: 1026, GID: 100, Name: "maktaba"} }}
}

func decodeAs[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

// A library added is checked at once; its access is stored and returned.
func TestCreateChecksAccess(t *testing.T) {
	fa := &fakeAccess{res: map[string]mediaaccess.Result{
		"/volume1/Movies": {Access: mediaaccess.ReadOnly, MountReadOnly: true},
	}}
	h := accessHandler(t, fa)
	rec := call(t, h, "POST", "/api/libraries", `{"name":"Movies","roots":["/volume1/Movies"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	lib := decodeAs[Library](t, rec.Body.String())
	if lib.Access == nil || *lib.Access != "ro" || lib.AccessCheckedAt == nil || fa.n() != 1 {
		t.Fatalf("created %+v after %d checks", lib, fa.n())
	}
	var stored string
	if err := h.DB.QueryRow(`SELECT access FROM libraries WHERE id = $1`, lib.ID).Scan(&stored); err != nil || stored != "ro" {
		t.Fatalf("stored %q %v", stored, err)
	}
	// a rename runs no check; a new root does
	call(t, h, "PATCH", "/api/libraries/"+lib.ID, `{"name":"Films"}`)
	if fa.n() != 1 {
		t.Fatalf("a rename checked: %d", fa.n())
	}
	rec = call(t, h, "PATCH", "/api/libraries/"+lib.ID, `{"roots":["/volume1/Films"]}`)
	if got := decodeAs[Library](t, rec.Body.String()); fa.n() != 2 || got.Access == nil || *got.Access != "rw" {
		t.Fatalf("roots changed: %+v, %d checks", got, fa.n())
	}
}

// T10: reading libraries, and starting (mounting the routes), never checks: a probe write
// would wake a sleeping disk.
func TestReadsNeverCheck(t *testing.T) {
	fa := &fakeAccess{}
	h := accessHandler(t, fa)
	addLibrary(t, h.DB, libBig, "Big", t0, "/m/a")
	for _, path := range []string{"/api/libraries", "/api/libraries/" + libBig, "/api/libraries/" + libBig + "/access"} {
		if rec := call(t, h, "GET", path, ""); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if fa.n() != 0 {
		t.Fatalf("%d checks on reads", fa.n())
	}
}

// Check access reports each root with the fix in the platform's words, and who Maktaba runs as.
func TestCheckAccessReport(t *testing.T) {
	fa := &fakeAccess{res: map[string]mediaaccess.Result{
		"/volume1/Movies": {Access: mediaaccess.ReadOnly, MountReadOnly: true},
		"/volume1/Shows":  {Access: mediaaccess.ReadWrite, Partial: true, Unreadable: []string{"/volume1/Shows/a.mkv"}},
		"/volume1/Kids":   {Access: mediaaccess.ReadWrite},
	}}
	h := accessHandler(t, fa)
	addLibrary(t, h.DB, libBig, "Big", t0, "/volume1/Movies", "/volume1/Shows", "/volume1/Kids")
	rec := call(t, h, "POST", "/api/libraries/"+libBig+"/check-access", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}
	rep := decodeAs[AccessReport](t, rec.Body.String())
	if rep.Access == nil || *rep.Access != "ro" || !rep.Partial || len(rep.Roots) != 3 || rep.Platform != "dsm" ||
		rep.RunsAs.UID != 1026 {
		t.Fatalf("report %+v", rep)
	}
	movies, shows, kids := rep.Roots[0], rep.Roots[1], rep.Roots[2]
	if movies.Fix == nil || movies.Fix.Key != "library.fix.dsm" || movies.Fix.Params["share"] != "Movies" ||
		movies.Fix.Params["name"] != "maktaba" || !movies.MountReadOnly {
		t.Errorf("movies %+v", movies)
	}
	if shows.Fix == nil || len(shows.Unreadable) != 1 {
		t.Errorf("shows %+v", shows)
	}
	if kids.Fix != nil {
		t.Errorf("a root that needs nothing has a fix: %+v", kids.Fix)
	}
	if n := count(t, h.DB, `SELECT COUNT(*) FROM audit_log WHERE action = 'access-checked'`); n != 1 {
		t.Errorf("audit rows %d", n)
	}
	// the stored check is what the page reads, without checking again
	rep2 := decodeAs[AccessReport](t, call(t, h, "GET", "/api/libraries/"+libBig+"/access", "").Body.String())
	if fa.n() != 3 || rep2.Access == nil || *rep2.Access != "ro" || len(rep2.Roots) != 3 {
		t.Fatalf("read %+v after %d checks", rep2, fa.n())
	}
	lib := decodeAs[Library](t, call(t, h, "GET", "/api/libraries/"+libBig, "").Body.String())
	if lib.Access == nil || *lib.Access != "ro" || !lib.AccessPartial {
		t.Fatalf("library %+v", lib)
	}
	// the admin pages read it from the manage list too
	if body := call(t, h, "GET", "/api/libraries/manage", "").Body.String(); !strings.Contains(body, `"access":"ro"`) ||
		!strings.Contains(body, `"access_partial":true`) {
		t.Fatalf("manage: %s", body)
	}
}

// A write that failed after the last check (the pipeline records it) makes the next read check
// again, and the failure stays listed until someone checks by hand.
func TestWriteFailureRechecks(t *testing.T) {
	fa := &fakeAccess{}
	h := accessHandler(t, fa)
	addLibrary(t, h.DB, libBig, "Big", t0, "/m/a")
	failed := t0.Add(time.Minute)
	detail := `{"write_failures":[{"path":"/m/a/x.en.srt","at":"` + failed.Format(time.RFC3339) + `"}],` +
		`"recheck_after":"` + failed.Format(time.RFC3339) + `"}`
	mustExec(t, h.DB, `UPDATE libraries SET access = 'rw', access_checked_at = $2, access_detail = $3 WHERE id = $1`,
		libBig, t0, detail)
	h.NowFunc = func() time.Time { return t0.Add(2 * time.Minute) }
	rep := decodeAs[AccessReport](t, call(t, h, "GET", "/api/libraries/"+libBig+"/access", "").Body.String())
	if fa.n() != 1 || len(rep.WriteFailures) != 1 || !rep.Partial {
		t.Fatalf("after a failed write: %+v, %d checks", rep, fa.n())
	}
	call(t, h, "GET", "/api/libraries/"+libBig+"/access", "")
	if fa.n() != 1 {
		t.Fatalf("checked again with nothing new: %d", fa.n())
	}
	rep = decodeAs[AccessReport](t, call(t, h, "POST", "/api/libraries/"+libBig+"/check-access", "").Body.String())
	if len(rep.WriteFailures) != 0 || rep.Partial {
		t.Fatalf("a check by hand starts afresh: %+v", rep)
	}
}

// No scan runs for a library whose folders cannot be listed; a purge never touches a
// read-only library.
func TestRefusalsOnAccess(t *testing.T) {
	h := accessHandler(t, &fakeAccess{})
	addLibrary(t, h.DB, libBig, "Big", t0, "/m/a")
	addLibrary(t, h.DB, libSmall, "Small", t0, "/m/b")
	mustExec(t, h.DB, `UPDATE libraries SET access = 'none' WHERE id = $1`, libBig)
	mustExec(t, h.DB, `UPDATE libraries SET access = 'ro' WHERE id = $1`, libSmall)
	rec := call(t, h, "POST", "/api/libraries/"+libBig+"/scan", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"library_no_access"`) {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}
	rec = call(t, h, "DELETE", "/api/libraries/"+libSmall+"?purge=true&confirm=Small", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"library_read_only"`) {
		t.Fatalf("purge: %d %s", rec.Code, rec.Body)
	}
	if n := count(t, h.DB, `SELECT COUNT(*) FROM libraries WHERE id = $1`, libSmall); n != 1 {
		t.Fatal("the library was deleted")
	}
	// a read-only library can still be scanned, and removed from Maktaba without a purge
	if rec := call(t, h, "POST", "/api/libraries/"+libSmall+"/scan", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("scan ro: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, h, "DELETE", "/api/libraries/"+libSmall, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete ro: %d %s", rec.Code, rec.Body)
	}
}
