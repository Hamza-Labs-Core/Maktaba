package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// setupEnv is one install's folders for the setup tests: server.toml (which
// sets the data dir, so the state, the keys and the logs are all temporary)
// and the stand-in programs.
type setupEnv struct {
	dir, config, state, logs, fakeLog string
}

func newSetupEnv(t *testing.T) *setupEnv {
	t.Helper()
	dir := t.TempDir()
	e := &setupEnv{
		dir:     dir,
		config:  filepath.Join(dir, "cfg", "server.toml"),
		state:   filepath.Join(dir, "state"),
		logs:    filepath.Join(dir, "logs"),
		fakeLog: filepath.Join(dir, "fake.log"),
	}
	if err := os.MkdirAll(filepath.Dir(e.config), 0o700); err != nil {
		t.Fatal(err)
	}
	toml := "[server]\ndata_dir = '" + e.state + "'\n"
	if err := os.WriteFile(e.config, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_CONFIG", e.config)
	t.Setenv("MAKTABA_LOG_DIR", e.logs)
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	t.Setenv("MAKTABA_POSTGRES_HOME", "")
	t.Setenv("MAKTABA_TEST_FAKE", "1")
	t.Setenv("MAKTABA_TEST_FAKE_LOG", e.fakeLog)
	t.Setenv("MAKTABA_TEST_FAKE_FAIL", "")
	t.Setenv("MAKTABA_TEST_FAKE_VENV_READY", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAKTABA_API_BIN", self)
	return e
}

// runStep runs one step; it returns the exit code, every event, and the last.
func runStep(t *testing.T, step string, in any) (int, []map[string]any, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(in)
	var out bytes.Buffer
	code := runSetupJSON(step, bytes.NewReader(raw), &out)
	var evs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%s: line %q: %v", step, line, err)
		}
		evs = append(evs, ev)
	}
	if strings.Contains(out.String(), "s3cret") || strings.Contains(out.String(), "hf_SECRET") ||
		strings.Contains(out.String(), "ACCOUNT-TOKEN") {
		t.Fatalf("%s printed a secret:\n%s", step, out.String())
	}
	return code, evs, evs[len(evs)-1]
}

func data(t *testing.T, ev map[string]any) map[string]any {
	t.Helper()
	if ev["type"] != "result" {
		t.Fatalf("not a result: %v", ev)
	}
	d, _ := ev["data"].(map[string]any)
	return d
}

func pendingOf(t *testing.T, e *setupEnv) setupPending {
	t.Helper()
	var p setupPending
	b, err := os.ReadFile(filepath.Join(e.state, "state", "setup.json"))
	if err != nil {
		t.Fatalf("no pending state: %v", err)
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSetupCheck(t *testing.T) {
	newSetupEnv(t)
	prev := hostFacts
	t.Cleanup(func() { hostFacts = prev })

	hostFacts = func(string) (uint64, uint64) { return 32 << 30, 500 << 30 }
	code, _, ev := runStep(t, "check", map[string]any{})
	d := data(t, ev)
	if code != 0 || d["ok"] != setupPlatformOK() || d["ram_bytes"] != float64(32<<30) {
		t.Fatalf("check = %d %v", code, d)
	}
	runs, _ := d["runs"].([]any)
	if len(runs) < 3 {
		t.Fatalf("runs = %v", runs)
	}

	// too little disk blocks; little memory only warns
	hostFacts = func(string) (uint64, uint64) { return 6 << 30, 10 << 30 }
	_, _, ev = runStep(t, "check", map[string]any{})
	d = data(t, ev)
	warns, _ := d["warnings"].([]any)
	if d["ok"] != false || len(warns) < 2 {
		t.Fatalf("little disk and memory: %v", d)
	}
}

// setupPlatformOK: this test machine is one the app runs the server on.
func setupPlatformOK() bool {
	ok, _ := setupPlatform(goosArch())
	return ok
}

func TestSetupPlatforms(t *testing.T) {
	for _, c := range []struct {
		goos, arch string
		ok         bool
	}{
		{"darwin", "arm64", true}, {"windows", "amd64", true}, {"linux", "amd64", true}, {"linux", "arm64", true},
		{"darwin", "amd64", false}, {"windows", "arm64", false}, {"freebsd", "amd64", false},
	} {
		if ok, why := setupPlatform(c.goos, c.arch); ok != c.ok || (!ok && why == "") {
			t.Errorf("%s/%s = %v %q", c.goos, c.arch, ok, why)
		}
	}
}

func TestSetupAdmin(t *testing.T) {
	e := newSetupEnv(t)
	for _, c := range []struct {
		in    map[string]string
		code  string
		field string
	}{
		{map[string]string{"username": "", "password": "s3cret-long"}, "bad_username", "username"},
		{map[string]string{"username": " admin", "password": "s3cret-long"}, "bad_username", "username"},
		{map[string]string{"username": "admin", "password": "s3cret"}, "weak_password", "password"},
		{map[string]string{"username": "admin", "password": strings.Repeat("x", 257)}, "weak_password", "password"},
	} {
		code, _, ev := runStep(t, "admin", c.in)
		if code != 1 || ev["code"] != c.code || ev["field"] != c.field {
			t.Errorf("%v = %d %v", c.in, code, ev)
		}
	}
	code, _, ev := runStep(t, "admin", map[string]string{"username": "admin", "password": "s3cret-long"})
	if code != 0 || data(t, ev)["username"] != "admin" {
		t.Fatalf("admin = %d %v", code, ev)
	}
	p := pendingOf(t, e)
	if p.Admin == nil || p.Admin.Username != "admin" || !strings.HasPrefix(p.Admin.PasswordHash, "$argon2id$") {
		t.Fatalf("pending admin = %+v", p.Admin)
	}
	raw, _ := os.ReadFile(filepath.Join(e.state, "state", "setup.json"))
	if strings.Contains(string(raw), "s3cret") {
		t.Fatal("the password must never be written down, only its hash")
	}
	calls := fakeCalls(t, e.fakeLog)
	if len(calls) != 1 || calls[0].Args[0] != "hash-password" || calls[0].Stdin != "s3cret-long" {
		t.Fatalf("calls = %+v (the password goes on stdin, never argv)", calls)
	}
	if info, err := os.Stat(filepath.Join(e.state, "state", "setup.json")); err == nil && !isWindows() && info.Mode().Perm() != 0o600 {
		t.Errorf("setup.json mode %v", info.Mode().Perm())
	}
}

func TestSetupLibraries(t *testing.T) {
	e := newSetupEnv(t)
	lib := filepath.Join(e.dir, "Lectures")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, ev := runStep(t, "libraries", map[string]any{"roots": []string{filepath.Join(e.dir, "nope")}})
	if code != 1 || ev["code"] != "root_missing" || ev["field"] != "roots" {
		t.Fatalf("missing root = %d %v", code, ev)
	}
	code, _, ev = runStep(t, "libraries", map[string]any{
		"roots":    []string{lib + string(filepath.Separator), lib, " "},
		"path_map": []map[string]string{{"from": "/Volumes/Originals", "to": `Y:\`}},
	})
	if code != 0 {
		t.Fatalf("libraries = %d %v", code, ev)
	}
	roots, _ := data(t, ev)["roots"].([]any)
	if len(roots) != 1 {
		t.Fatalf("roots = %v (a folder given twice is one root)", roots)
	}
	p := pendingOf(t, e)
	if len(p.Roots) != 1 || p.Roots[0] != lib {
		t.Fatalf("pending roots = %v", p.Roots)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(p.PathMap), &m); err != nil || m["/Volumes/Originals"] != `Y:\` {
		t.Fatalf("path map = %q (%v)", p.PathMap, err)
	}
	if code, _, ev := runStep(t, "libraries", map[string]any{"roots": []string{lib},
		"path_map": []map[string]string{{"from": "/a", "to": ""}}}); code != 1 || ev["code"] != "bad_path_map" {
		t.Fatalf("half a path map line = %d %v", code, ev)
	}
}

// fakeHF is a Hugging Face that knows one good token, which accepted the
// gated model's terms, and one that did not.
func fakeHF(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch {
		case r.URL.Path == "/api/whoami-v2" && (auth == "Bearer hf_SECRET" || auth == "Bearer hf_NOTERMS"):
			_, _ = w.Write([]byte(`{"name":"me"}`))
		case r.URL.Path == "/api/whoami-v2":
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/resolve/main/config.json") && auth == "Bearer hf_SECRET":
			w.Header().Set("Location", "https://cdn.example/x")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodHead:
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	prev := hfBaseURL
	hfBaseURL = srv.URL
	t.Cleanup(func() { hfBaseURL = prev })
}

func TestSetupModels(t *testing.T) {
	spawnsPrograms(t)
	e := newSetupEnv(t)
	fakeHF(t)
	for _, c := range []struct{ token, code string }{
		{"hf_BAD", "hf_token_invalid"}, {"hf_NOTERMS", "hf_gated"}, {"", "hf_token_invalid"},
	} {
		code, _, ev := runStep(t, "models", map[string]string{"source": "huggingface", "hf_token": c.token})
		if code != 1 || ev["code"] != c.code || ev["field"] != "hf_token" {
			t.Errorf("token %q = %d %v", c.token, code, ev)
		}
	}
	if code, _, ev := runStep(t, "models", map[string]string{"source": "huggingface", "hf_token": "hf_SECRET"}); code != 0 {
		t.Fatalf("good token = %d %v", code, ev)
	}
	tok := filepath.Join(e.state, "models", "huggingface", "token")
	if b, err := os.ReadFile(tok); err != nil || string(b) != "hf_SECRET" {
		t.Fatalf("the token is saved where the pipeline's HF client reads it: %q %v", b, err)
	}
	if p := pendingOf(t, e); p.Models == nil || p.Models.Source != "huggingface" {
		t.Fatalf("pending models = %+v", p.Models)
	}

	// the NAS copy: the share's models folder, or the share itself
	share := filepath.Join(e.dir, "share")
	if code, _, ev := runStep(t, "models", map[string]string{"source": "nas", "path": share}); code != 1 || ev["code"] != "nas_missing" {
		t.Fatalf("no copy = %d %v", code, ev)
	}
	rev := filepath.Join(share, "maktaba", "models", shareModelFolder(defaultRefineModel), "abc123")
	if err := os.MkdirAll(rev, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, ev := runStep(t, "models", map[string]string{"source": "nas", "path": share}); code != 1 {
		t.Fatalf("a copy without its manifest (being written) is not one: %d %v", code, ev)
	}
	if err := os.WriteFile(filepath.Join(rev, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, ev := runStep(t, "models", map[string]string{"source": "nas", "path": share}); code != 0 {
		t.Fatalf("nas = %d %v", code, ev)
	}
	if p := pendingOf(t, e); p.Models == nil || p.Models.Path != filepath.Join(share, "maktaba", "models") {
		t.Fatalf("pending models = %+v", p.Models)
	}

	// the download (after the venv): progress relayed, the share passed on
	if code, _, ev := runStep(t, "models", map[string]string{"action": "download"}); code != 1 || ev["code"] != "venv_missing" {
		t.Fatalf("download before the venv = %d %v", code, ev)
	}
	copyTestBinary(t, venvPythonFor(filepath.Join(e.state, "venv")))
	code, evs, ev := runStep(t, "models", map[string]string{"action": "download"})
	if code != 0 || len(evs) < 2 || evs[0]["stage"] != "download" || evs[0]["done"] != float64(100) || evs[0]["total"] != float64(400) {
		t.Fatalf("download = %d %v", code, evs)
	}
	if items, _ := data(t, ev)["models"].(map[string]any); items["cohere"] != "ready" {
		t.Fatalf("items = %v", data(t, ev))
	}
	calls := fakeCalls(t, e.fakeLog)
	last := calls[len(calls)-1]
	if !strings.Contains(strings.Join(last.Args, " "), "--share "+filepath.Join(share, "maktaba", "models")) {
		t.Fatalf("download args = %v (the NAS copy is passed on)", last.Args)
	}
	t.Setenv("MAKTABA_TEST_FAKE_FAIL", "gated")
	if code, _, ev := runStep(t, "models", map[string]string{"action": "download"}); code != 1 || ev["code"] != "hf_gated" {
		t.Fatalf("gated download = %d %v", code, ev)
	}
}

func venvPythonFor(venv string) string {
	if isWindows() {
		return filepath.Join(venv, "Scripts", "python.exe")
	}
	return filepath.Join(venv, "bin", "python")
}

// fakePlexTV is plex.tv with one PIN (42), signed in once authorised is set.
type fakePlex struct {
	mu         sync.Mutex
	authorised bool
	clientIDs  []string
}

func newFakePlex(t *testing.T) *fakePlex {
	t.Helper()
	fp := &fakePlex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		defer fp.mu.Unlock()
		fp.clientIDs = append(fp.clientIDs, r.Header.Get("X-Plex-Client-Identifier"))
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"code":"ABCD"}`))
		case r.URL.Path == "/api/v2/pins/42" && fp.authorised:
			_, _ = w.Write([]byte(`{"id":42,"authToken":"ACCOUNT-TOKEN"}`))
		case r.URL.Path == "/api/v2/pins/42":
			_, _ = w.Write([]byte(`{"id":42,"authToken":null}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	prev := plexTVURL
	plexTVURL = srv.URL
	t.Cleanup(func() { plexTVURL = prev })
	return fp
}

func TestSetupPlex(t *testing.T) {
	e := newSetupEnv(t)
	fp := newFakePlex(t)
	code, _, ev := runStep(t, "plex", map[string]string{"action": "pin"})
	d := data(t, ev)
	if code != 0 || d["pin_id"] != float64(42) || d["code"] != "ABCD" ||
		!strings.HasPrefix(d["link_url"].(string), "https://app.plex.tv/auth#?clientID=maktaba-") {
		t.Fatalf("pin = %d %v", code, d)
	}
	if _, _, ev := runStep(t, "plex", map[string]any{"action": "poll", "pin_id": 42}); data(t, ev)["linked"] != false {
		t.Fatalf("waiting = %v", ev)
	}
	fp.mu.Lock()
	fp.authorised = true
	fp.mu.Unlock()
	if _, _, ev := runStep(t, "plex", map[string]any{"action": "poll", "pin_id": 42}); data(t, ev)["linked"] != true {
		t.Fatalf("linked = %v", ev)
	}
	tok := filepath.Join(filepath.Dir(e.config), "plex-setup.token")
	if b, err := os.ReadFile(tok); err != nil || string(b) != "ACCOUNT-TOKEN" {
		t.Fatalf("the account token waits in the keys folder: %q %v", b, err)
	}
	p := pendingOf(t, e)
	fp.mu.Lock()
	ids := append([]string(nil), fp.clientIDs...)
	fp.mu.Unlock()
	for _, id := range ids {
		if id != p.Plex.ClientID {
			t.Fatalf("every call has the install's one client id: %v vs %q", ids, p.Plex.ClientID)
		}
	}
	if !p.Plex.Linked {
		t.Fatal("pending plex not linked")
	}
	if code, _, ev := runStep(t, "plex", map[string]any{"action": "poll", "pin_id": 7}); code != 1 || ev["code"] != "pin_expired" {
		t.Fatalf("unknown pin = %d %v", code, ev)
	}
	// skip forgets it all
	if _, _, ev := runStep(t, "plex", map[string]string{"action": "skip"}); data(t, ev)["linked"] != false {
		t.Fatalf("skip = %v", ev)
	}
	if _, err := os.Stat(tok); !os.IsNotExist(err) {
		t.Fatal("skip must delete the account token")
	}
	if pendingOf(t, e).Plex != nil {
		t.Fatal("skip must forget the sign-in")
	}
}

// fakePayload lays out an app payload whose Python is the stand-in.
func fakePayload(t *testing.T, e *setupEnv) string {
	t.Helper()
	payload := filepath.Join(e.dir, "payload")
	py := filepath.Join(payload, "python", "bin", "python3")
	if isWindows() {
		py = filepath.Join(payload, "python", "python.exe")
	}
	copyTestBinary(t, py)
	for name, body := range map[string]string{
		"payload.json": `{"schema":1,"python":{"version":"3.12.15"}}`,
		filepath.Join("pipeline", "maktaba_pipeline-1.4.0-py3-none-any.whl"): "wheel",
		filepath.Join("pipeline", "constraints.txt"):                         "x==1 --hash=sha256:00",
	} {
		p := filepath.Join(payload, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("MAKTABA_APP_BUNDLE", payload)
	return payload
}

func TestSetupVenv(t *testing.T) {
	spawnsPrograms(t)
	e := newSetupEnv(t)
	if code, _, ev := runStep(t, "venv", map[string]any{}); code != 1 || ev["code"] != "server_missing" {
		t.Fatalf("outside the app = %d %v", code, ev)
	}
	fakePayload(t, e)
	code, evs, ev := runStep(t, "venv", map[string]any{})
	if code != 0 {
		t.Fatalf("venv = %d %v", code, evs)
	}
	d := data(t, ev)
	if d["python"] != "3.12.15" || d["pipeline"] != "1.4.0" || d["reused"] != false {
		t.Fatalf("result = %v", d)
	}
	// progress: the bootstrap's 0..1 as one bar out of 1000, never going back
	var last float64
	for _, p := range evs[:len(evs)-1] {
		if p["stage"] != "venv" || p["total"] != float64(1000) || p["done"].(float64) < last {
			t.Fatalf("progress %v", p)
		}
		last = p["done"].(float64)
	}
	if last != 1000 {
		t.Fatalf("the bar ends full: %v", last)
	}
	calls := fakeCalls(t, e.fakeLog)
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[0].Args, " "), "--check") ||
		!strings.Contains(strings.Join(calls[1].Args, " "), "--venv "+filepath.Join(e.state, "venv")) {
		t.Fatalf("calls = %+v", calls)
	}
	// up to date: reused, nothing built
	t.Setenv("MAKTABA_TEST_FAKE_VENV_READY", "1")
	if _, _, ev := runStep(t, "venv", map[string]any{}); data(t, ev)["reused"] != true {
		t.Fatalf("reuse = %v", ev)
	}
	t.Setenv("MAKTABA_TEST_FAKE_VENV_READY", "")
	t.Setenv("MAKTABA_TEST_FAKE_FAIL", "venv")
	code, _, ev = runStep(t, "venv", map[string]any{})
	if code != 1 || ev["code"] != "venv_failed" || !strings.Contains(ev["message"].(string), "No matching distribution") ||
		!strings.Contains(ev["message"].(string), filepath.Join(e.logs, "venv.log")) {
		t.Fatalf("failed venv = %d %v", code, ev)
	}
}

func TestSetupFinish(t *testing.T) {
	spawnsPrograms(t)
	e := newSetupEnv(t)
	prev := setupDatabase
	t.Cleanup(func() { setupDatabase = prev })
	var dbRuns int
	setupDatabase = func(_ *setupRun, fn func(string) error) error {
		dbRuns++
		return fn("postgres://maktaba@127.0.0.1:54329/maktaba")
	}
	if code, _, ev := runStep(t, "finish", map[string]any{}); code != 1 || ev["code"] != "admin_missing" {
		t.Fatalf("finish before admin = %d %v", code, ev)
	}

	lib := filepath.Join(e.dir, "Lectures")
	_ = os.MkdirAll(lib, 0o755)
	newFakePlex(t).authorised = true
	for _, st := range []struct {
		step string
		in   any
	}{
		{"admin", map[string]string{"username": "owner", "password": "s3cret-long"}},
		{"libraries", map[string]any{"roots": []string{lib}, "path_map": []map[string]string{{"from": "/m", "to": "M:/"}}}},
		{"plex", map[string]string{"action": "pin"}},
		{"plex", map[string]any{"action": "poll", "pin_id": 42}},
	} {
		if code, _, ev := runStep(t, st.step, st.in); code != 0 {
			t.Fatalf("%s = %d %v", st.step, code, ev)
		}
	}
	code, evs, ev := runStep(t, "finish", map[string]any{})
	if code != 0 {
		t.Fatalf("finish = %d %v", code, evs)
	}
	d := data(t, ev)
	if d["web_url"] != "http://127.0.0.1:8088" || d["supervisor_url"] != "http://127.0.0.1:9103" {
		t.Fatalf("finish = %v", d)
	}
	if plex, _ := d["plex"].(map[string]any); plex["linked"] != true || plex["server"] != "Home" {
		t.Fatalf("plex = %v", d["plex"])
	}
	// server.toml has the libraries and the path map
	saved, found, err := config.LoadFrom(e.config)
	if err != nil || !found || len(saved.Media.Roots) != 1 || saved.Media.Roots[0] != lib ||
		saved.Media.PathMap != `{"/m":"M:/"}` || saved.Server.DataDir != e.state {
		t.Fatalf("server.toml: %+v %v", saved.Media, err)
	}
	// migrate, then the admin from its hash (through a file, never argv), then plex
	calls := fakeCalls(t, e.fakeLog)
	var names []string
	for _, c := range calls[1:] { // after the admin step's hash-password
		names = append(names, c.Args[0])
		if c.DB != "postgres://maktaba@127.0.0.1:54329/maktaba" {
			t.Errorf("%s ran against %q", c.Args[0], c.DB)
		}
		for _, a := range c.Args {
			if strings.Contains(a, "$argon2id$") || a == "ACCOUNT-TOKEN" {
				t.Fatalf("a secret in argv: %v", c.Args)
			}
		}
	}
	if strings.Join(names, ",") != "migrate,adduser,plex-link" {
		t.Fatalf("calls = %v", names)
	}
	add := calls[2]
	if add.Args[len(add.Args)-1] != "owner" {
		t.Fatalf("adduser args = %v", add.Args)
	}
	for f, body := range add.Files {
		if !strings.HasPrefix(body, "$argon2id$") {
			t.Fatalf("adduser got %q", body)
		}
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatal("the hash file must be removed after adduser")
		}
	}
	for f, body := range calls[3].Files {
		if body != "ACCOUNT-TOKEN" {
			t.Fatalf("plex-link got %q", body)
		}
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatal("the Plex account token must be deleted after plex-link")
		}
	}
	if _, err := os.Stat(filepath.Join(e.state, "state", "setup.json")); !os.IsNotExist(err) {
		t.Fatal("nothing may wait after finish (the hash included)")
	}
	if dbRuns != 1 {
		t.Fatalf("the database ran %d times", dbRuns)
	}
}

// An admin seeded by an earlier finish (adduser exits 3) is not an error, and
// a failing migration is reported as one.
func TestSetupFinishAgainAndFailures(t *testing.T) {
	spawnsPrograms(t)
	e := newSetupEnv(t)
	prev := setupDatabase
	t.Cleanup(func() { setupDatabase = prev })
	setupDatabase = func(_ *setupRun, fn func(string) error) error { return fn("postgres://x@127.0.0.1:1/m") }
	admin := func() {
		if code, _, ev := runStep(t, "admin", map[string]string{"username": "owner", "password": "s3cret-long"}); code != 0 {
			t.Fatalf("admin = %d %v", code, ev)
		}
	}
	admin()
	t.Setenv("MAKTABA_TEST_FAKE_FAIL", "migrate")
	if code, _, ev := runStep(t, "finish", map[string]any{}); code != 1 || ev["code"] != "migrate_failed" {
		t.Fatalf("migrate failure = %d %v", code, ev)
	}
	if pendingOf(t, e).Admin == nil {
		t.Fatal("a failed finish keeps what was collected (it can run again)")
	}
	t.Setenv("MAKTABA_TEST_FAKE_FAIL", "adduser-exists")
	if code, _, ev := runStep(t, "finish", map[string]any{}); code != 0 {
		t.Fatalf("finish again = %d %v", code, ev)
	}
}

// status after finish: configured (server.toml and its database).
func TestSetupStatusAfterFinish(t *testing.T) {
	e := newSetupEnv(t)
	if err := os.WriteFile(e.config, []byte("[server]\ndata_dir = '"+e.state+"'\n[database]\nurl = 'postgres://m@127.0.0.1/m'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, ev := runStep(t, "status", map[string]any{})
	if data(t, ev)["configured"] != true {
		t.Fatalf("status = %v", ev)
	}
}
