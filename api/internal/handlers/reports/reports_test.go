package reports

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/queues/queuestest"
)

const libID = "0a1b2c3d-0000-4000-8000-00000000abcd"

type fakePlanner struct {
	busy     bool
	err      error
	started  []string
	statusIn pipeline.PlanPreviewStatus
}

func (f *fakePlanner) PlanPreviewStart(_ context.Context, kind string) (bool, error) {
	f.started = append(f.started, kind)
	return f.busy, f.err
}

func (f *fakePlanner) PlanPreviewStatus(context.Context) (pipeline.PlanPreviewStatus, error) {
	return f.statusIn, f.err
}

type fixture struct {
	dir, thumbs string
	r           chi.Router
	planner     *fakePlanner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir, thumbs := t.TempDir(), t.TempDir()
	db := queuestest.New(&queuestest.Rule{
		Match: "from libraries",
		Cols:  []string{"id", "name"},
		Rows:  [][]driver.Value{{libID, "قناة الاختبار"}},
	})
	p := &fakePlanner{}
	h := &Handler{DB: db.DB, Dir: dir, ThumbDir: thumbs, Pipeline: p}
	r := chi.NewRouter()
	h.Mount(r)
	return &fixture{dir: dir, thumbs: thumbs, r: r, planner: p}
}

func (f *fixture) write(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) do(method, target, body string, p *principal.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func admin() *principal.Principal  { return &principal.Principal{UserID: "u1", IsAdmin: true} }
func viewer() *principal.Principal { return &principal.Principal{UserID: "u2"} }

func TestList_KindsTitlesAndHiddenFiles(t *testing.T) {
	f := newFixture(t)
	f.write(t, libID+".html", "<p>review</p>")
	f.write(t, PlanPage, "<p>plan</p>")
	f.write(t, AuditPage, "<p>audit</p>")
	f.write(t, "notes-2.html", "<p>x</p>")
	f.write(t, libID+".part.html", "half") // a page being written
	f.write(t, "readme.txt", "no")         // not a report
	f.write(t, ".hidden.html", "no")       // does not match the name pattern
	rec := f.do("GET", "/api/reports", "", admin())
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var got struct{ Reports []Report }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range got.Reports {
		order = append(order, r.Kind+":"+r.Title)
	}
	want := []string{"plan:Channel plan", "audit:Channel audit", "review:قناة الاختبار", "other:notes-2.html"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", order, want)
	}
	if got.Reports[2].LibraryID == nil || *got.Reports[2].LibraryID != libID {
		t.Errorf("review page lost its library id: %+v", got.Reports[2])
	}
}

func TestReports_FollowTheLibraryACL(t *testing.T) {
	f := newFixture(t)
	other := "0a1b2c3d-0000-4000-8000-00000000ffff"
	f.write(t, libID+".html", "<p>mine</p>")
	f.write(t, other+".html", "<p>theirs</p>")
	f.write(t, PlanPage, "<p>plan</p>")
	granted := &principal.Principal{UserID: "u3", Libraries: []string{libID}}
	rec := f.do("GET", "/api/reports", "", granted)
	var got struct{ Reports []Report }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(got.Reports) != 1 || got.Reports[0].Name != libID+".html" {
		t.Fatalf("a restricted principal sees %+v", got.Reports)
	}
	if rec := f.do("GET", "/api/reports/"+libID+".html", "", granted); rec.Code != 200 {
		t.Errorf("own review page -> %d", rec.Code)
	}
	for _, name := range []string{other + ".html", PlanPage} {
		if rec := f.do("GET", "/api/reports/"+name, "", granted); rec.Code != http.StatusNotFound {
			t.Errorf("%s -> %d, want 404", name, rec.Code)
		}
	}
	// a viewer with no libraries sees nothing; an all-library principal everything
	if rec := f.do("GET", "/api/reports", "", viewer()); !strings.Contains(rec.Body.String(), `"reports":[]`) {
		t.Errorf("no-library viewer: %s", rec.Body.String())
	}
	all := &principal.Principal{UserID: "u4", AccessAllLibraries: true}
	if rec := f.do("GET", "/api/reports/"+PlanPage, "", all); rec.Code != 200 {
		t.Errorf("all-library principal plan page -> %d", rec.Code)
	}
}

func TestList_MissingFolderIsEmpty(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.dir); err != nil {
		t.Fatal(err)
	}
	rec := f.do("GET", "/api/reports", "", viewer())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"reports":[]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestReads_NeedASignedInPrincipal(t *testing.T) {
	f := newFixture(t)
	f.write(t, PlanPage, "x")
	for _, p := range []string{"/api/reports", "/api/reports/" + PlanPage, "/api/reports/channel-plan/preview", "/api/thumbnails/abcdef123456/small.jpg"} {
		if rec := f.do("GET", p, "", nil); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s anonymous: %d", p, rec.Code)
		}
	}
}

func TestPage_ServesWithAScriptlessPolicy(t *testing.T) {
	f := newFixture(t)
	f.write(t, PlanPage, `<!doctype html><p>hi</p><script>alert(1)</script>`)
	rec := f.do("GET", "/api/reports/"+PlanPage, "", admin())
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<p>hi</p>") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if ct := h.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	csp := h.Get("Content-Security-Policy")
	for _, must := range []string{"default-src 'none'", "img-src 'self'", "form-action 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, must) {
			t.Errorf("CSP lacks %q: %s", must, csp)
		}
	}
	if strings.Contains(csp, "script-src") && !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("CSP allows script: %s", csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("headers: %v", h)
	}
}

func TestPage_NamesNeverBecomePaths(t *testing.T) {
	f := newFixture(t)
	f.write(t, PlanPage, "x")
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.dir), "secret.html"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"..%2Fsecret.html", "%2e%2e%2fsecret.html", "..%5Csecret.html", "a%2Fb.html",
		"missing.html", "channel-plan.txt", "x.part.html", ".html", "%00.html",
	} {
		rec := f.do("GET", "/api/reports/"+name, "", admin())
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q -> %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// a name that is a directory is not a report either
	if err := os.Mkdir(filepath.Join(f.dir, "dir.html"), 0o700); err != nil {
		t.Fatal(err)
	}
	if rec := f.do("GET", "/api/reports/dir.html", "", admin()); rec.Code != http.StatusNotFound {
		t.Errorf("a directory served: %d", rec.Code)
	}
}

func TestPage_SymlinkOutOfTheFolderIsNotServed(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(t.TempDir(), "secret.html")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.dir, "link.html")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if rec := f.do("GET", "/api/reports/link.html", "", admin()); rec.Code != http.StatusNotFound {
		t.Fatalf("symlink served: %d %s", rec.Code, rec.Body.String())
	}
}

func TestThumbnail(t *testing.T) {
	f := newFixture(t)
	h := "9f86d081884c7d659a2feaa0c55ad015"
	if err := os.MkdirAll(filepath.Join(f.thumbs, h), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.thumbs, h, "small.jpg"), []byte("\xff\xd8jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := f.do("GET", "/api/thumbnails/"+h+"/small.jpg", "", admin())
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	// a caller without the video's library (here: no video, no database)
	// does not get it: the content hash is not a secret
	if rec := f.do("GET", "/api/thumbnails/"+h+"/small.jpg", "", viewer()); rec.Code != http.StatusNotFound {
		t.Fatalf("viewer without the library: %d", rec.Code)
	}
	for _, bad := range []string{"..", "short", "a%2Fb12345678", "%2e%2e%2f%2e%2e", "unknownhash12345"} {
		if rec := f.do("GET", "/api/thumbnails/"+bad+"/small.jpg", "", admin()); rec.Code != http.StatusNotFound {
			t.Errorf("%q -> %d", bad, rec.Code)
		}
	}
}

func TestPreview_StartNeedsAdminAndRelaysTheKind(t *testing.T) {
	f := newFixture(t)
	if rec := f.do("POST", "/api/reports/channel-plan/preview", `{"kind":"preview"}`, viewer()); rec.Code != 403 {
		t.Fatalf("viewer: %d", rec.Code)
	}
	if rec := f.do("POST", "/api/reports/channel-plan/preview", `{"kind":"preview"}`, nil); rec.Code != 403 {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	if len(f.planner.started) != 0 {
		t.Fatal("a forbidden request started a preview")
	}
	rec := f.do("POST", "/api/reports/channel-plan/preview", `{"kind":"stored"}`, admin())
	if rec.Code != http.StatusAccepted || len(f.planner.started) != 1 || f.planner.started[0] != "stored" {
		t.Fatalf("%d %s %v", rec.Code, rec.Body.String(), f.planner.started)
	}
	rec = f.do("POST", "/api/reports/channel-plan/preview", `{}`, admin())
	if rec.Code != http.StatusAccepted || f.planner.started[1] != "preview" {
		t.Fatalf("default kind: %d %v", rec.Code, f.planner.started)
	}
	if rec = f.do("POST", "/api/reports/channel-plan/preview", `{"kind":"rm -rf"}`, admin()); rec.Code != 400 {
		t.Fatalf("bad kind: %d", rec.Code)
	}
}

func TestPreview_BusyOfflineAndStatus(t *testing.T) {
	f := newFixture(t)
	f.planner.busy = true
	if rec := f.do("POST", "/api/reports/channel-plan/preview", `{}`, admin()); rec.Code != http.StatusConflict {
		t.Fatalf("busy: %d", rec.Code)
	}
	f.planner.busy = false
	f.planner.statusIn = pipeline.PlanPreviewStatus{Running: true, Phase: "writing the page", Log: []string{"a"}}
	// the plan spans every library: a library-scoped viewer gets 404
	if rec := f.do("GET", "/api/reports/channel-plan/preview", "", viewer()); rec.Code != http.StatusNotFound {
		t.Fatalf("viewer: %d", rec.Code)
	}
	rec := f.do("GET", "/api/reports/channel-plan/preview", "", admin())
	var st pipeline.PlanPreviewStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.Running || st.Phase != "writing the page" {
		t.Fatalf("%d %s %v", rec.Code, rec.Body.String(), err)
	}
	f.planner.err = errors.New("dial failed")
	if rec = f.do("GET", "/api/reports/channel-plan/preview", "", admin()); rec.Code != http.StatusBadGateway {
		t.Fatalf("pipeline error: %d", rec.Code)
	}
	// no pipeline client at all
	h := &Handler{Dir: f.dir, Pipeline: nil}
	r := chi.NewRouter()
	h.Mount(r)
	req := httptest.NewRequest("GET", "/api/reports/channel-plan/preview", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), admin()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("offline: %d", w.Code)
	}
}

// A report page follows the system theme with a dark fallback even when it
// was written without one (pagetheme.Inject), and stays scriptless.
func TestPage_FollowsTheSystemTheme(t *testing.T) {
	f := newFixture(t)
	f.write(t, PlanPage, `<!doctype html><html><head><title>p</title></head><body><p>hi</p></body></html>`)
	rec := f.do("GET", "/api/reports/"+PlanPage, "", admin())
	if rec.Code != 200 {
		t.Fatalf("%d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`<meta name="color-scheme" content="dark light">`, "color-scheme:dark light", "@media (prefers-color-scheme: light)", "<p>hi</p>"} {
		if !strings.Contains(body, want) {
			t.Errorf("report lacks %q: %s", want, body)
		}
	}
	// a page that already declares its scheme is served untouched
	own := `<!doctype html><style>:root{color-scheme:dark light}</style><p>own</p>`
	f.write(t, AuditPage, own)
	if rec := f.do("GET", "/api/reports/"+AuditPage, "", admin()); rec.Body.String() != own {
		t.Errorf("page with its own color-scheme changed: %s", rec.Body.String())
	}
}
