package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func packsTestAdmin(t *testing.T) (*packsAdmin, *packs.Runner, http.Handler) {
	t.Helper()
	m := &packs.Manager{Layout: packs.Layout{Root: filepath.Join(t.TempDir(), "components")}, Catalog: components.Default(),
		Version: "0.4.2", Platform: "linux-amd64", Log: io.Discard}
	run := &packs.Runner{M: m, Machine: store.Home}
	a := &packsAdmin{ctx: context.Background(), m: m, r: run, token: "tok"}
	return a, run, a.adminHandler(http.NotFoundHandler())
}

func packsCall(t *testing.T, h http.Handler, method, path, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// The loopback routes refuse a request without the per-boot token (none, a wrong one).
func TestPacksRoutesNeedTheToken(t *testing.T) {
	_, _, h := packsTestAdmin(t)
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/components/runtime.ml/install"}, {http.MethodPost, "/components/runtime.ml/remove"},
		{http.MethodGet, "/components/runtime.ml/progress"}, {http.MethodPost, "/components/offline"},
		{http.MethodGet, "/components/offline/maktaba-offline-0.4.2-linux-amd64-full.tar"},
		{http.MethodPost, "/components/import"},
	} {
		for _, tok := range []string{"", "nope"} {
			if code, _ := packsCall(t, h, r.method, r.path, tok); code != http.StatusForbidden {
				t.Errorf("%s %s with token %q: %d", r.method, r.path, tok, code)
			}
		}
	}
	// no reconciler on this machine: 503, still behind the token
	none := (&packsAdmin{token: "tok"}).adminHandler(http.NotFoundHandler())
	if code, _ := packsCall(t, none, http.MethodGet, "/components/runtime.ml/progress", "tok"); code != http.StatusServiceUnavailable {
		t.Fatalf("no manager: %d", code)
	}
}

// T08: removing an asset an enabled component requires is refused with the list.
func TestPacksRemoveRequiredBy(t *testing.T) {
	_, run, h := packsTestAdmin(t)
	run.Desired(map[string]store.Desired{"transcription": {ID: "transcription", Want: components.WantEnabled, Generation: 1}})
	code, out := packsCall(t, h, http.MethodPost, "/components/runtime.ml/remove", "tok")
	if code != http.StatusConflict || out["error"] != "component_required_by" {
		t.Fatalf("%d %v", code, out)
	}
	if by, _ := out["components"].([]any); len(by) == 0 || by[0] != "transcription" {
		t.Fatalf("components = %v", out["components"])
	}
	// disabled: the removal starts
	run.Desired(map[string]store.Desired{"transcription": {ID: "transcription", Want: components.WantInstalled, Generation: 2}})
	if code, _ := packsCall(t, h, http.MethodPost, "/components/runtime.ml/remove", "tok"); code != http.StatusAccepted {
		t.Fatalf("remove after disabling: %d", code)
	}
	packsIdle(t, run.M, "runtime.ml")
}

func TestPacksInstallAndProgressRoutes(t *testing.T) {
	a, _, h := packsTestAdmin(t)
	if code, _ := packsCall(t, h, http.MethodPost, "/components/transcription/install", "tok"); code != http.StatusNotFound {
		t.Fatalf("a component that is no asset: %d", code)
	}
	a.m.OnDisk = func(id string) bool { return id == "runtime.ml" }
	if code, out := packsCall(t, h, http.MethodPost, "/components/runtime.ml/install", "tok"); code != http.StatusOK || out["installed"] != true {
		t.Fatalf("installed already: %d %v", code, out)
	}
	code, out := packsCall(t, h, http.MethodGet, "/components/runtime.ml/progress", "tok")
	if code != http.StatusOK || out["installed"] != true || out["progress"] != nil {
		t.Fatalf("progress: %d %v", code, out)
	}
	// no network source: it starts, then fails with its reason
	if code, _ := packsCall(t, h, http.MethodPost, "/components/tool.yt-dlp/install", "tok"); code != http.StatusAccepted {
		t.Fatalf("install: %d", code)
	}
	packsIdle(t, a.m, "tool.yt-dlp")
	a.m.Disabled = true
	if code, out := packsCall(t, h, http.MethodPost, "/components/tool.yt-dlp/install", "tok"); code != http.StatusConflict ||
		out["error"] != packs.ReasonPacksDisabled {
		t.Fatalf("packs = off: %d %v", code, out)
	}
	if code, _ := packsCall(t, h, http.MethodGet, "/components/runtime.ml/install", "tok"); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET install: %d", code)
	}
}

// GET /components/packs says whether this machine installs packs (the API's install
// route refuses with packs_disabled when it does not), behind the token.
func TestPacksStateRoute(t *testing.T) {
	a, _, h := packsTestAdmin(t)
	if code, _ := packsCall(t, h, http.MethodGet, "/components/packs", ""); code != http.StatusForbidden {
		t.Fatalf("no token: %d", code)
	}
	if code, out := packsCall(t, h, http.MethodGet, "/components/packs", "tok"); code != http.StatusOK || out["packs"] != "on" {
		t.Fatalf("packs on: %d %v", code, out)
	}
	a.m.Disabled = true
	if code, out := packsCall(t, h, http.MethodGet, "/components/packs", "tok"); code != http.StatusOK || out["packs"] != "off" {
		t.Fatalf("packs off: %d %v", code, out)
	}
}

// A file for another version is refused by the import route with its reason.
func TestPacksImportRouteRefusesAnotherVersion(t *testing.T) {
	a, _, h := packsTestAdmin(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packs.OfflineManifestName),
		[]byte(`{"schema":1,"version":"0.4.1","platform":"linux-amd64","items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/components/import", strings.NewReader(`{"path":"`+filepath.ToSlash(dir)+`"}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), packs.ReasonOfflineWrongVersion) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	// a file of this version with nothing new: nothing installed
	if err := os.WriteFile(filepath.Join(dir, packs.OfflineManifestName),
		[]byte(`{"schema":1,"version":"0.4.2","platform":"linux-amd64","items":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/components/import", strings.NewReader(`{"path":"`+filepath.ToSlash(dir)+`"}`))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"installed":[]`) {
		t.Fatalf("import of nothing: %d %s", rec.Code, rec.Body.String())
	}
	_ = a
}

// The import's path is the CLI's file named absolutely, with no ".." in it; an id with a
// ".." or a separator names no component.
func TestPacksRoutesRefuseOtherPaths(t *testing.T) {
	_, _, h := packsTestAdmin(t)
	for _, p := range []string{"relative/offline.tar", "/srv/../etc/offline.tar"} {
		body, _ := json.Marshal(map[string]string{"path": p})
		req := httptest.NewRequest(http.MethodPost, "/components/import", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("import of %q: %d %s", p, rec.Code, rec.Body.String())
		}
	}
	for _, id := range []string{"runtime..ml", `runtime.ml\x`} {
		if code, _ := packsCall(t, h, http.MethodGet, "/components/"+id+"/progress", "tok"); code != http.StatusNotFound {
			t.Errorf("progress of %q: %d", id, code)
		}
	}
}

// The venv a pack brings: the extras of what is installed, the packs' wheels alone when
// each has its pack; a removal keeps the others' and builds with none when nothing is left.
// Story 51.16: the Manager puts the runtime.ollama pack and the language models where the
// model server looks for them (llmserver's pack folder, the bundled server's store).
func TestPacksManagerPutsOllamaWhereTheServerLooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("MAKTABA_CONFIG", filepath.Join(home, "server.toml"))
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	t.Setenv("MAKTABA_OLLAMA_BIN", "")
	cfg := config.Defaults()
	m := newPacksManager(cfg, "", io.Discard)
	if got, want := m.Layout.Dir("runtime.ollama"), llmserver.PackDir(cfg.StateDir()); got != want {
		t.Fatalf("runtime.ollama's folder %s, the server looks in %s", got, want)
	}
	if got, want := m.OllamaModels, llmserver.Planned(cfg, "").Models; got != want || got == "" {
		t.Fatalf("the models go to %q, the server's store is %q", got, want)
	}
}

// This machine's Manager downloads from the bases the environment names
// (MAKTABA_PACKS_URL, HF_ENDPOINT, MAKTABA_OLLAMA_REGISTRY).
func TestPacksManagerDownloadsFromTheEnvironmentsSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MAKTABA_CONFIG", filepath.Join(home, "server.toml"))
	t.Setenv(packs.EnvReleasesURL, "http://127.0.0.1:9/releases")
	t.Setenv(packs.EnvHFEndpoint, "http://127.0.0.1:9/hf")
	t.Setenv(packs.EnvOllamaRegistry, "http://127.0.0.1:9/ollama")
	n, ok := newPacksManager(config.Defaults(), "", io.Discard).Net.(*packs.Net)
	if !ok || n.Releases != "http://127.0.0.1:9/releases" || n.HF != "http://127.0.0.1:9/hf" || n.OllamaRegistry != "http://127.0.0.1:9/ollama" {
		t.Fatalf("the Manager's network source %+v", n)
	}
}

func TestPackVenvSpec(t *testing.T) {
	payload := t.TempDir()
	t.Setenv("MAKTABA_APP_BUNDLE", payload)
	t.Setenv("MAKTABA_PYTHON", "")
	cfg := config.Config{}
	cfg.Server.DataDir = filepath.Join(t.TempDir(), "data")
	m := &packs.Manager{Layout: packs.Layout{Root: packsDir(cfg)}, Catalog: components.Default(), Version: "0.4.2",
		Platform: "linux-amd64", Log: io.Discard}
	v := &packVenv{cfg: cfg, m: m, log: io.Discard}
	staging := filepath.Join(t.TempDir(), ".staging-0.4.2_abc")
	if err := os.MkdirAll(filepath.Join(staging, "wheels"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec, err := v.spec("runtime.ml", staging, "")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Extras != "stt,search" || !spec.Offline || spec.Wheelhouse != filepath.Join(staging, "wheels") || spec.Payload != payload {
		t.Fatalf("adding runtime.ml: %+v", spec)
	}
	// yt-dlp on a home whose ML runtime came from PyPI (no pack): PyPI allowed
	m.OnDisk = func(id string) bool { return id == "runtime.ml" }
	ytStaging := filepath.Join(t.TempDir(), "yt")
	_ = os.MkdirAll(filepath.Join(ytStaging, "wheels"), 0o755)
	spec, _ = v.spec("tool.yt-dlp", ytStaging, "")
	if spec.Extras != "stt,search,mediaintel" || spec.Offline {
		t.Fatalf("adding yt-dlp beside a PyPI venv: %+v", spec)
	}
	// removing the only one: no extras
	m.OnDisk = nil
	spec, _ = v.spec("", "", "runtime.ml")
	if !spec.NoExtras || spec.Extras != "" {
		t.Fatalf("removing runtime.ml: %+v", spec)
	}
	// the rebuild runs the venv builder with it
	var got appvenv.Spec
	venvRebuild = func(_ context.Context, s appvenv.Spec, _ func(appvenv.Progress)) (appvenv.Result, error) {
		got = s
		return appvenv.Result{}, nil
	}
	t.Cleanup(func() { venvRebuild = appvenv.Run })
	if err := v.cleanup(context.Background(), "runtime.ml"); err != nil || !got.NoExtras {
		t.Fatalf("cleanup: %v %+v", err, got)
	}
	// no app payload: a pack's runtime is refused
	t.Setenv("MAKTABA_APP_BUNDLE", "")
	if err := v.prepare(context.Background(), "runtime.ml", staging, nil); err == nil {
		t.Fatal("prepared without an app payload")
	}
}

// packsIdle waits for an action the routes started on id to end.
func packsIdle(t *testing.T, m *packs.Manager, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if p, ok := m.Progress(id); ok && !p.Busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not end", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type fakeRestarter struct{ asked []string }

func (f *fakeRestarter) RestartComponent(id string) int {
	f.asked = append(f.asked, id)
	return 2
}

type fakeLoopRestarter struct{ asked []string }

func (f *fakeLoopRestarter) RestartLoops(id string) int {
	f.asked = append(f.asked, id)
	return 1
}

// POST /components/{id}/restart asks the supervisor to restart the component's
// processes, behind the token, with or without a reconciler; no supervisor is 503.
func TestPacksRestartRoute(t *testing.T) {
	f := &fakeRestarter{}
	h := (&packsAdmin{token: "tok", sup: f}).adminHandler(http.NotFoundHandler())
	if code, _ := packsCall(t, h, http.MethodPost, "/components/transcription/restart", ""); code != http.StatusForbidden {
		t.Fatalf("no token: %d", code)
	}
	code, out := packsCall(t, h, http.MethodPost, "/components/transcription/restart", "tok")
	if code != http.StatusOK || out["restarted"] != float64(2) || len(f.asked) != 1 || f.asked[0] != "transcription" {
		t.Fatalf("%d %v %v", code, out, f.asked)
	}
	if code, _ := packsCall(t, h, http.MethodGet, "/components/transcription/restart", "tok"); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", code)
	}
	if code, _ := packsCall(t, h, http.MethodPost, "/components/../restart", "tok"); code == http.StatusOK {
		t.Fatalf("a bad id: %d", code)
	}
	// #636: the supervisor's own loops of the component too
	l := &fakeLoopRestarter{}
	h = (&packsAdmin{token: "tok", sup: f, loops: l}).adminHandler(http.NotFoundHandler())
	code, out = packsCall(t, h, http.MethodPost, "/components/backups/restart", "tok")
	if code != http.StatusOK || out["restarted"] != float64(3) || len(l.asked) != 1 || l.asked[0] != "backups" {
		t.Fatalf("%d %v %v", code, out, l.asked)
	}
	none := (&packsAdmin{token: "tok"}).adminHandler(http.NotFoundHandler())
	if code, _ := packsCall(t, none, http.MethodPost, "/components/transcription/restart", "tok"); code != http.StatusServiceUnavailable {
		t.Fatalf("no supervisor: %d", code)
	}
}
