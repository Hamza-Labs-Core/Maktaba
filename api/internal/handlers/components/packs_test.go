package components

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	svc "github.com/Hamza-Labs-Core/Maktaba/api/internal/components"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/runner"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

// fakeSupervisor is the supervisor's loopback /components/ routes (cmd/maktaba-server
// serve_packs.go) as the API sees them, behind a per-boot token.
type fakeSupervisor struct {
	mu    sync.Mutex
	calls []string // "METHOD path"
	// built: the offline file is ready; imported: what an import answers
	built        bool
	importBody   []byte
	importType   string
	importRefuse bool
	// packsOff: [components] packs = "off" on this machine (offline mode)
	packsOff bool
}

const supToken = "boot-token"

func (s *fakeSupervisor) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	s.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+supToken {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	j := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch p := r.URL.Path; {
	case p == "/components/packs" && r.Method == http.MethodGet:
		s.mu.Lock()
		on := map[bool]string{true: "off", false: "on"}[s.packsOff]
		s.mu.Unlock()
		j(200, map[string]any{"packs": on})
	case p == "/components/runtime.ml/progress":
		j(200, map[string]any{"id": "runtime.ml", "installed": false, "progress": map[string]any{
			"phase": "installing", "percent": 42, "detail": "pack_downloading", "name": "runtime.ml.tar.zst",
			"done": 42, "total": 100, "busy": true}})
	case p == "/components/model.refine/progress":
		j(200, map[string]any{"id": "model.refine", "installed": false, "progress": map[string]any{
			"phase": "installing", "percent": 10, "reason": "disk_space", "params": map[string]any{"need": "4 GB"}, "busy": false}})
	case p == "/components/transcription/restart" && r.Method == http.MethodPost:
		j(200, map[string]any{"id": "transcription", "restarted": 2})
	case strings.HasSuffix(p, "/restart") && r.Method == http.MethodPost:
		j(200, map[string]any{"restarted": 0}) // none of its processes runs now
	case strings.HasSuffix(p, "/progress"):
		j(200, map[string]any{"installed": true, "progress": nil})
	case p == "/components/offline" && r.Method == http.MethodPost:
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["bundle"] == "nope" {
			j(400, map[string]any{"error": "bad_request", "detail": "unknown bundle"})
			return
		}
		j(202, map[string]any{"name": "maktaba-offline-1.2.3-linux-amd64-core.tar"})
	case p == "/components/offline/maktaba-offline-1.2.3-linux-amd64-core.tar":
		s.mu.Lock()
		built := s.built
		s.mu.Unlock()
		if !built {
			j(202, map[string]any{"name": "maktaba-offline-1.2.3-linux-amd64-core.tar",
				"progress": map[string]any{"phase": "offline", "percent": 30, "busy": true}})
			return
		}
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("Content-Disposition", `attachment; filename="maktaba-offline-1.2.3-linux-amd64-core.tar"`)
		_, _ = io.WriteString(w, "TARBYTES")
	case strings.HasPrefix(p, "/components/offline/"):
		http.NotFound(w, r)
	case p == "/components/import":
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.importBody, s.importType = b, r.Header.Get("Content-Type")
		refuse := s.importRefuse
		s.mu.Unlock()
		if refuse {
			j(409, map[string]any{"error": "offline_wrong_version", "detail": "the file is for 1.2.2",
				"params": map[string]any{"version": "1.2.2"}, "installed": []string{"model.whisper-medium"}})
			return
		}
		j(200, map[string]any{"installed": []string{"model.whisper-medium", "runtime.ml"}})
	default:
		http.NotFound(w, r)
	}
}

func (s *fakeSupervisor) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// packsFixture is the fixture with a supervisor behind its token.
func packsFixture(t *testing.T) (*fixture, *fakeSupervisor) {
	t.Helper()
	f := newFixture(t)
	return f, withSupervisor(t, f)
}

// withSupervisor routes f through a Handler whose Packs is a fake supervisor.
func withSupervisor(t *testing.T, f *fixture) *fakeSupervisor {
	t.Helper()
	return withSupervisorAndLoops(t, f, nil)
}

// withSupervisorAndLoops is withSupervisor with loops as the API's Go-loop runner.
func withSupervisorAndLoops(t *testing.T, f *fixture, loops *runner.Runner) *fakeSupervisor {
	t.Helper()
	sup := &fakeSupervisor{}
	srv := httptest.NewServer(sup)
	t.Cleanup(srv.Close)
	tf := filepath.Join(t.TempDir(), "local.token")
	if err := os.WriteFile(tf, []byte(supToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &svc.Service{Store: f.st, Catalog: components.Default(), Licensed: func(string) bool { return true }}
	r := chi.NewRouter()
	(&Handler{Service: s, DB: f.db, Packs: &Packs{Sup: &serversettings.Supervisor{URL: srv.URL, TokenFile: tf}}, Loops: loops}).Mount(r)
	f.r = r
	return sup
}

func (f *fixture) actions(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE category = 'components' AND action = $1`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var packRoutes = []struct{ method, path, body string }{
	{"POST", "/api/components/offline", `{"bundle":"core"}`},
	{"GET", "/api/components/offline/maktaba-offline-1.2.3-linux-amd64-core.tar", ""},
	{"POST", "/api/components/import", "TAR"},
}

// T12: no sign-in is 401; a viewer, an admin's play sign-in, a token without the admin
// scope and a machine are 403; the supervisor is never called and nothing is recorded.
func TestPacksAdminOnly(t *testing.T) {
	f, sup := packsFixture(t)
	for _, c := range packRoutes {
		if rec := f.doType(c.method, c.path, c.body, "application/x-tar", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s as no one: %d", c.method, c.path, rec.Code)
		}
		for name, p := range map[string]*principal.Principal{"viewer": viewer(), "play": playAdmin(), "token": token(), "machine": machine()} {
			if rec := f.doType(c.method, c.path, c.body, "application/x-tar", p); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d", c.method, c.path, name, rec.Code)
			}
		}
	}
	if got := sup.called(); len(got) != 0 {
		t.Fatalf("the supervisor was called for a refused caller: %v", got)
	}
	if f.audits(t) != 0 {
		t.Fatalf("refused calls wrote %d audit rows", f.audits(t))
	}
	for name, p := range map[string]*principal.Principal{"admin": admin(), "manage": manageLink()} {
		for _, c := range packRoutes {
			if rec := f.doType(c.method, c.path, c.body, "application/x-tar", p); rec.Code/100 != 2 {
				t.Errorf("%s %s as %s: %d %s", c.method, c.path, name, rec.Code, rec.Body)
			}
		}
	}
}

func (f *fixture) doType(method, path, body, ct string, p *principal.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		if strings.HasPrefix(body, "{") {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

// The list and one component carry an asset's install progress from the supervisor
// (Installing 42%, its step), and a failure's reason; a component that is no asset has none.
func TestListAndShowCarryProgress(t *testing.T) {
	f, sup := packsFixture(t)
	var l svc.ListView
	decode(t, f.do("GET", "/api/components", "", admin()), 200, &l)
	by := map[string]svc.View{}
	for _, v := range l.Components {
		by[v.ID] = v
	}
	if p := by["runtime.ml"].Progress; p == nil || !p.Busy || p.Percent != 42 || p.Detail != "pack_downloading" || p.Done != 42 {
		t.Fatalf("runtime.ml progress: %+v", p)
	}
	if p := by["model.refine"].Progress; p == nil || p.Busy || p.Reason != "disk_space" || p.Params["need"] != "4 GB" {
		t.Fatalf("model.refine progress: %+v", p)
	}
	if by["transcription"].Progress != nil || by["tool.ffmpeg"].Progress != nil {
		t.Fatalf("progress where there is none: %+v %+v", by["transcription"].Progress, by["tool.ffmpeg"].Progress)
	}
	for _, c := range sup.called() {
		if strings.Contains(c, "/transcription/") {
			t.Fatalf("asked the supervisor about a component that is no asset: %s", c)
		}
	}
	var d svc.Detail
	decode(t, f.do("GET", "/api/components/runtime.ml", "", admin()), 200, &d)
	if d.Progress == nil || d.Progress.Percent != 42 {
		t.Fatalf("show: %+v", d.Progress)
	}
}

// Without a supervisor the lists carry no progress and the pack routes answer 503.
func TestNoSupervisor(t *testing.T) {
	f := newFixture(t)
	var l svc.ListView
	decode(t, f.do("GET", "/api/components", "", admin()), 200, &l)
	for _, v := range l.Components {
		if v.Progress != nil {
			t.Fatalf("%s has progress with no supervisor", v.ID)
		}
	}
	for _, c := range packRoutes {
		code(t, f.doType(c.method, c.path, c.body, "application/x-tar", admin()), http.StatusServiceUnavailable, CodeUnavailable)
	}
	// a supervisor that does not answer is the same
	tf := filepath.Join(t.TempDir(), "t")
	_ = os.WriteFile(tf, []byte(supToken), 0o600)
	s := &svc.Service{Store: f.st, Catalog: components.Default()}
	r := chi.NewRouter()
	(&Handler{Service: s, Packs: &Packs{Sup: &serversettings.Supervisor{URL: "http://127.0.0.1:1", TokenFile: tf}}}).Mount(r)
	f.r = r
	decode(t, f.do("GET", "/api/components", "", admin()), 200, &l)
	code(t, f.doType("POST", "/api/components/offline", `{}`, "", admin()), http.StatusServiceUnavailable, CodeUnavailable)
}

// Download for offline: the build starts (202, its name and url, audited), its progress
// shows while it builds, then the file streams; an unknown name or a bad request is refused.
func TestOfflineDownload(t *testing.T) {
	f, sup := packsFixture(t)
	var made map[string]string
	decode(t, f.do("POST", "/api/components/offline", `{"bundle":"core"}`, admin()), http.StatusAccepted, &made)
	if made["name"] != "maktaba-offline-1.2.3-linux-amd64-core.tar" || made["url"] != "/api/components/offline/"+made["name"] {
		t.Fatalf("made: %v", made)
	}
	if f.actions(t, svc.ActionOfflineMake) != 1 {
		t.Fatal("making the file is not audited")
	}
	var pending struct {
		Name     string               `json:"name"`
		Progress *svc.InstallProgress `json:"progress"`
	}
	decode(t, f.do("GET", made["url"], "", admin()), http.StatusAccepted, &pending)
	if pending.Progress == nil || pending.Progress.Percent != 30 {
		t.Fatalf("pending: %+v", pending)
	}
	sup.mu.Lock()
	sup.built = true
	sup.mu.Unlock()
	rec := f.do("GET", made["url"], "", admin())
	if rec.Code != 200 || rec.Body.String() != "TARBYTES" || rec.Header().Get("Content-Type") != "application/x-tar" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("file: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	if f.actions(t, svc.ActionOfflineDownload) != 1 {
		t.Fatal("the download is not audited")
	}
	code(t, f.do("GET", "/api/components/offline/maktaba-offline-9-x-core.tar", "", admin()), http.StatusNotFound, CodeOfflineUnknown)
	code(t, f.do("GET", "/api/components/offline/passwd", "", admin()), http.StatusNotFound, CodeOfflineUnknown)
	code(t, f.do("POST", "/api/components/offline", `{"bundle":"nope"}`, admin()), http.StatusBadRequest, "bad_request")
	code(t, f.do("POST", "/api/components/offline", `{"components":["nope"]}`, admin()), http.StatusNotFound, svc.CodeUnknown)
	if rec := f.do("POST", "/api/components/offline", `{"frob":1}`, admin()); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
}

// Import a file: the body streams to the supervisor as a tar; what it installed is
// audited and what the home wanted absent becomes installed (off). A JSON body (a path
// on the machine) is not taken from the web.
func TestImport(t *testing.T) {
	f, sup := packsFixture(t)
	if _, err := f.st.SetDesired(context.Background(), store.Desired{ID: "model.whisper-medium", Want: components.WantAbsent},
		store.Audit{By: "test", Action: "component.remove"}); err != nil {
		t.Fatal(err)
	}
	before := f.want(t, "runtime.ml")
	var got struct {
		Installed []string        `json:"installed"`
		Changed   []store.Desired `json:"changed"`
	}
	decode(t, f.doType("POST", "/api/components/import?name=home.tar", "TARBYTES", "application/octet-stream", admin()), 200, &got)
	if strings.Join(got.Installed, ",") != "model.whisper-medium,runtime.ml" || len(got.Changed) != 1 || got.Changed[0].ID != "model.whisper-medium" {
		t.Fatalf("import: %+v", got)
	}
	sup.mu.Lock()
	body, ct := string(sup.importBody), sup.importType
	sup.mu.Unlock()
	if body != "TARBYTES" || ct != "application/x-tar" {
		t.Fatalf("the supervisor got %q as %q", body, ct)
	}
	if w := f.want(t, "model.whisper-medium"); w.Want != components.WantInstalled {
		t.Fatalf("model.whisper-medium: %+v", w)
	}
	if w := f.want(t, "runtime.ml"); w.Want != before.Want || w.Generation != before.Generation {
		t.Fatalf("runtime.ml changed: %+v -> %+v", before, w)
	}
	if f.actions(t, svc.ActionImport) != 2 {
		t.Fatalf("import audit rows: %d", f.actions(t, svc.ActionImport))
	}
	n := len(sup.called())
	code(t, f.doType("POST", "/api/components/import", `{"path":"/etc"}`, "", admin()), http.StatusUnsupportedMediaType, CodeImportNeedsTar)
	if len(sup.called()) != n {
		t.Fatal("a JSON import reached the supervisor")
	}
}

// A refused import answers the supervisor's reason as the code, with its params and what
// went in before the refusal (which is recorded).
func TestImportRefused(t *testing.T) {
	f, sup := packsFixture(t)
	sup.importRefuse = true
	body := code(t, f.doType("POST", "/api/components/import", "TAR", "application/x-tar", admin()), http.StatusConflict, "offline_wrong_version")
	if p, _ := body["params"].(map[string]any); p["version"] != "1.2.2" {
		t.Fatalf("params: %v", body)
	}
	if in, _ := body["installed"].([]any); len(in) != 1 || in[0] != "model.whisper-medium" {
		t.Fatalf("installed: %v", body)
	}
	if f.actions(t, svc.ActionImport) != 1 {
		t.Fatal("what went in before the refusal is not recorded")
	}
}

func (s *fakeSupervisor) packsAsked() int {
	n := 0
	for _, c := range s.called() {
		if c == "GET /components/packs" {
			n++
		}
	}
	return n
}

// Offline mode ([components] packs = "off" on this machine, the supervisor's answer):
// an install of an asset not on disk is 409 packs_disabled, nothing written or recorded,
// whether its want is absent or wanted (Retry); an asset on disk and a component that is
// no asset are answered as before. The role comes first: a viewer is 403 and the
// supervisor is not asked.
func TestInstallRefusedWithPacksOff(t *testing.T) {
	f, sup := packsFixture(t)
	sup.packsOff = true
	if rec := f.do("POST", "/api/components/runtime.ml/install", "", viewer()); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer: %d", rec.Code)
	}
	if got := sup.called(); len(got) != 0 {
		t.Fatalf("the supervisor was asked for a viewer: %v", got)
	}
	if _, err := f.db.Exec(`UPDATE components SET want = 'absent' WHERE id = ?`, "model.refine"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"runtime.ml", "model.refine"} {
		before := f.want(t, id)
		body := code(t, f.do("POST", "/api/components/"+id+"/install", "", admin()), http.StatusConflict, svc.CodePacksDisabled)
		if d, _ := body["detail"].(string); !strings.Contains(d, "packs") {
			t.Fatalf("%s: %v", id, body)
		}
		if after := f.want(t, id); after.Want != before.Want || after.Generation != before.Generation {
			t.Fatalf("%s: written with packs off: %+v -> %+v", id, before, after)
		}
	}
	if n := f.audits(t); n != 0 {
		t.Fatalf("%d audit rows with packs off", n)
	}
	// on disk (the home's packs row says Installed): answered as it is
	if err := f.st.ReportStatus(context.Background(), store.Home, "packs", []store.Status{{ID: "tool.yt-dlp", Phase: components.PhaseOff,
		Conditions: []store.Condition{{Type: components.CondInstalled, Status: "true"}}}}); err != nil {
		t.Fatal(err)
	}
	gen := f.want(t, "tool.yt-dlp").Generation
	decode(t, f.do("POST", "/api/components/tool.yt-dlp/install", "", admin()), 200, nil)
	if f.want(t, "tool.yt-dlp").Generation != gen {
		t.Fatal("an asset on disk was written")
	}
	// not an asset: packs are not asked about
	n := sup.packsAsked()
	decode(t, f.do("POST", "/api/components/youtube/install", "", admin()), 200, nil)
	if sup.packsAsked() != n {
		t.Fatal("packs were asked about for a component that is no asset")
	}
	// packs on: the install is written as before (model.refine: absent to installed)
	sup.mu.Lock()
	sup.packsOff = false
	sup.mu.Unlock()
	decode(t, f.do("POST", "/api/components/model.refine/install", "", admin()), 200, nil)
	if w := f.want(t, "model.refine"); w.Want != components.WantInstalled {
		t.Fatalf("packs on: %+v", w)
	}
}
