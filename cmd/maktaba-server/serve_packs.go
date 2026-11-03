package main

// Epic 51 story 51.16: the asset components' installs, removals and offline files, serve's
// side. The packs Manager does the work (internal/packs); here it gets this machine's
// facts (where the venv and the Hugging Face cache are, what was on disk before packs, the
// release's signer and trust root), its prepare hooks (runtime.ml and tool.yt-dlp rebuild
// the app's venv from the packs' wheels, never PyPI), and the supervisor's loopback routes
// the API proxies for its admins, behind the per-boot local token:
//
//	POST /components/{id}/install    202 {"id","progress"}: starts it now (200 when installed)
//	POST /components/{id}/remove     202; 409 component_required_by {"components":[...]}
//	GET  /components/{id}/progress   {"id","installed","progress":{...}|null}
//	POST /components/offline         202 {"name"}: builds maktaba-offline-<v>-<platform>-<bundle>.tar
//	GET  /components/offline/{name}  the file once built (202 while it is, 404 after 24 h)
//	POST /components/import          a tar body (streamed) or {"path": "..."}: 200 {"installed":[...]}

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/appvenv"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/componentseed"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/llmserver"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/packs"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// packsDir is the components folder: <state>/components.
func packsDir(cfg config.Config) string { return filepath.Join(cfg.StateDir(), "components") }

// hfHome is the pipeline's Hugging Face home as the supervisor's children get it
// (MAKTABA_MODEL_CACHE_DIR, else <state>/models; HF_HOME, else <cache>/huggingface).
func hfHome(cfg config.Config) string {
	if h := strings.TrimSpace(os.Getenv("HF_HOME")); h != "" {
		return h
	}
	cache := strings.TrimSpace(os.Getenv("MAKTABA_MODEL_CACHE_DIR"))
	if cache == "" {
		cache = filepath.Join(cfg.StateDir(), "models")
	}
	return filepath.Join(cache, "huggingface")
}

// packsVersion is the running version without its "v" ("" for a build of no release).
func packsVersion() string {
	v := strings.TrimPrefix(strings.TrimSpace(version.Version), "v")
	if !layout.ValidVersion(v) {
		return ""
	}
	return v
}

// venvStamp is what the pipeline's venv bootstrap left in the venv (maktaba-venv.json).
type venvStamp struct {
	State  string   `json:"state"`
	Extras []string `json:"extras"`
}

func readVenvStamp(venv string) (venvStamp, bool) {
	b, err := os.ReadFile(filepath.Join(venv, "maktaba-venv.json"))
	if err != nil {
		return venvStamp{}, false
	}
	var s venvStamp
	if json.Unmarshal(b, &s) != nil {
		return venvStamp{}, false
	}
	return s, true
}

// diskAssets answers what was on this machine before packs (componentseed's checks, as
// the seed saw them), read again at most every 5 s. runtime.ml (and model.refine, which
// comes with it) follows the venv's stamp when there is one: the extras it was built (or
// is being built) with; a removal rebuilds it with none.
type diskAssets struct {
	cfg        config.Config
	releaseDir string
	ollama     bool

	mu   sync.Mutex
	at   time.Time
	have map[string]bool
}

func (d *diskAssets) onDisk(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.have == nil || time.Since(d.at) > 5*time.Second {
		have := componentseed.Assets(components.Default(), componentseed.Disk{Config: d.cfg, ReleaseDir: d.releaseDir, Ollama: d.ollama})
		venvs := []string{filepath.Join(d.cfg.StateDir(), "venv")}
		if d.releaseDir != "" {
			venvs = append(venvs, filepath.Join(d.releaseDir, "venv"))
		}
		if supervisor.PythonOverride(d.cfg) == "" {
			for _, v := range venvs {
				if st, ok := readVenvStamp(v); ok {
					ml := slices.Contains(st.Extras, "stt")
					have["runtime.ml"], have["model.refine"] = ml, ml
					break
				}
			}
		}
		d.have, d.at = have, time.Now()
	}
	return d.have[id]
}

// newPacksManager is this machine's Manager.
func newPacksManager(cfg config.Config, releaseDir string, log io.Writer) *packs.Manager {
	exe, _ := os.Executable()
	probe := cfg
	probe.LLM.Enabled, probe.LLM.URL = "auto", ""
	_, ollama, _ := llmserver.Resolve(probe, exe)
	disk := &diskAssets{cfg: cfg, releaseDir: releaseDir, ollama: ollama}
	id, err := rv.IdentityFromEnv()
	if err != nil {
		id, _ = rv.NewIdentity("")
	}
	trust := &rv.RootStore{Dir: filepath.Join(cfg.Server.DataDir, "sigstore")}
	home := hfHome(cfg)
	net := packs.NewNet(id.Repo(), nil)
	net.Token = func() string {
		if t := strings.TrimSpace(os.Getenv("HF_TOKEN")); t != "" {
			return t
		}
		b, _ := os.ReadFile(filepath.Join(home, "token"))
		return strings.TrimSpace(string(b))
	}
	m := &packs.Manager{
		Layout:   packs.Layout{Root: packsDir(cfg)},
		Catalog:  components.Default(),
		Version:  packsVersion(),
		Platform: componentseed.Platform(),
		Net:      net,
		Trusted: func(ctx context.Context) (root.TrustedMaterial, error) {
			if err := trust.Refresh(ctx); err != nil {
				fmt.Fprintf(log, "[packs] the Sigstore trusted root was not refreshed (%v): the last good one\n", err)
			}
			tm, _, err := trust.TrustedMaterial()
			return tm, err
		},
		Identity:  id,
		Watermark: rv.IndexWatermarkPath(cfg.StateDir()),
		HFHub:     filepath.Join(home, "hub"),
		FreeDisk:  hostinfo.FreeDisk,
		OnDisk:    disk.onDisk,
		Disabled:  !cfg.Components.PacksOn(),
		Log:       log,
	}
	v := &packVenv{cfg: cfg, m: m, log: log}
	m.Prepare = map[string]packs.PrepareFunc{"runtime.ml": v.prepare, "tool.yt-dlp": v.prepare}
	m.Cleanup = map[string]packs.CleanupFunc{"runtime.ml": v.cleanup, "tool.yt-dlp": v.cleanup}
	return m
}

// packVenv rebuilds the app's pipeline venv (<state>/venv, through the venv gate's
// appvenv) when runtime.ml or tool.yt-dlp comes or goes: the extras are those of what is
// installed, and pip takes the packs' wheels alone when every extra has its pack.
type packVenv struct {
	cfg config.Config
	m   *packs.Manager
	log io.Writer
	mu  sync.Mutex // one rebuild at a time
}

// venvRebuild is appvenv.Run (tests replace it).
var venvRebuild = appvenv.Run

func (v *packVenv) spec(adding, addingDir, removing string) (appvenv.Spec, error) {
	payload := config.AppBundle()
	if payload == "" {
		return appvenv.Spec{}, errors.New("this install builds the pipeline's environment when a release is staged: " +
			"a pack's runtime is installed by the desktop app and the Linux package only")
	}
	spec := appvenv.Spec{Payload: payload, Venv: filepath.Join(v.cfg.StateDir(), "venv")}
	has := func(id string) bool {
		switch id {
		case adding:
			return true
		case removing:
			return false
		}
		return v.m.Installed(id)
	}
	var extras []string
	offline := true
	for _, a := range []struct {
		id     string
		extras []string
	}{{"runtime.ml", []string{"stt", "search"}}, {"tool.yt-dlp", []string{"mediaintel"}}} {
		dir := ""
		if a.id == adding {
			dir = addingDir
		} else if cur := v.m.Layout.Current(a.id); cur != "" {
			dir = filepath.Join(v.m.Layout.Dir(a.id), cur)
		}
		wheels := filepath.Join(dir, "wheels")
		if dir != "" {
			if _, err := os.Stat(wheels); err == nil {
				if spec.Wheelhouse == "" {
					spec.Wheelhouse = wheels
				} else {
					spec.Wheelhouses = append(spec.Wheelhouses, wheels)
				}
			} else {
				dir = ""
			}
		}
		if has(a.id) {
			extras = append(extras, a.extras...)
			offline = offline && dir != ""
		}
	}
	spec.Offline = offline && spec.Wheelhouse != ""
	if len(extras) == 0 {
		spec.NoExtras = true
	} else {
		spec.Extras = strings.Join(extras, ",")
	}
	return spec, nil
}

func (v *packVenv) run(ctx context.Context, spec appvenv.Spec, report func(int)) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if f, err := config.OpenLog("venv.log"); err == nil {
		defer f.Close()
		spec.Log = f
	}
	fmt.Fprintf(v.log, "[packs] the pipeline's venv is rebuilt with extras %q (offline %t)\n", spec.Extras, spec.Offline)
	_, err := venvRebuild(ctx, spec, func(p appvenv.Progress) {
		if report != nil {
			report(int(p.Fraction * 100))
		}
	})
	return err
}

func (v *packVenv) prepare(ctx context.Context, id, dir string, report func(int)) error {
	if py := supervisor.PythonOverride(v.cfg); py != "" {
		fmt.Fprintf(v.log, "[packs] %s: the pipeline runs %s (MAKTABA_PYTHON or [pipeline] python): its venv is not rebuilt\n", id, py)
		return nil
	}
	spec, err := v.spec(id, dir, "")
	if err != nil {
		return err
	}
	return v.run(ctx, spec, report)
}

func (v *packVenv) cleanup(ctx context.Context, id string) error {
	if supervisor.PythonOverride(v.cfg) != "" || config.AppBundle() == "" {
		return nil
	}
	spec, err := v.spec("", "", id)
	if err != nil {
		return err
	}
	return v.run(ctx, spec, nil)
}

// packsAdmin serves the routes of this file for m (nil: a machine with no reconciler,
// every route 503).
type packsAdmin struct {
	ctx   context.Context
	m     *packs.Manager
	r     *packs.Runner
	token string

	mu      sync.Mutex
	offline map[string]*offlineJob
}

type offlineJob struct {
	path  string
	done  bool
	err   error
	items []string
	at    time.Time
}

// offlineKeep is how long a built offline file is kept.
const offlineKeep = 24 * time.Hour

func (a *packsAdmin) authorized(r *http.Request) bool {
	return a.token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.token)) == 1
}

// adminHandler adds /components/ to base.
func (a *packsAdmin) adminHandler(base http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/components/", func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if a.m == nil {
			writeConfigJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "components_unavailable"})
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/components/")
		switch {
		case rest == "offline" && r.Method == http.MethodPost:
			a.postOffline(w, r)
		case strings.HasPrefix(rest, "offline/") && r.Method == http.MethodGet:
			a.getOffline(w, r, strings.TrimPrefix(rest, "offline/"))
		case rest == "import" && r.Method == http.MethodPost:
			a.postImport(w, r)
		default:
			id, verb, ok := strings.Cut(rest, "/")
			if !ok || id == "" {
				http.NotFound(w, r)
				return
			}
			switch {
			case verb == "install" && r.Method == http.MethodPost:
				a.postInstall(w, id)
			case verb == "remove" && r.Method == http.MethodPost:
				a.postRemove(w, id)
			case verb == "progress" && r.Method == http.MethodGet:
				a.getProgress(w, id)
			case verb == "install" || verb == "remove" || verb == "progress":
				http.Error(w, "method", http.StatusMethodNotAllowed)
			default:
				http.NotFound(w, r)
			}
		}
	})
	return mux
}

func reasonJSON(err error) map[string]any {
	out := map[string]any{"error": packs.ReasonOf(err), "detail": err.Error()}
	if p := packs.ParamsOf(err); p != nil {
		out["params"] = p
	}
	return out
}

func (a *packsAdmin) asset(w http.ResponseWriter, id string) bool {
	c, ok := a.m.Catalog.Get(id)
	if !ok || c.Kind != components.KindAsset {
		writeConfigJSON(w, http.StatusNotFound, map[string]string{"error": "component_unknown", "id": id})
		return false
	}
	return true
}

func (a *packsAdmin) postInstall(w http.ResponseWriter, id string) {
	if !a.asset(w, id) {
		return
	}
	switch {
	case a.m.Disabled:
		writeConfigJSON(w, http.StatusConflict, map[string]string{"error": packs.ReasonPacksDisabled})
		return
	case a.m.Busy(id):
		writeConfigJSON(w, http.StatusConflict, map[string]string{"error": packs.ReasonBusy})
		return
	case a.m.Installed(id):
		writeConfigJSON(w, http.StatusOK, map[string]any{"id": id, "installed": true})
		return
	}
	go func() { _ = a.m.Install(a.ctx, id, nil) }()
	writeConfigJSON(w, http.StatusAccepted, map[string]any{"id": id, "progress": "/components/" + id + "/progress"})
}

func (a *packsAdmin) postRemove(w http.ResponseWriter, id string) {
	if !a.asset(w, id) {
		return
	}
	if a.r != nil {
		if by := packs.RequiredBy(a.m.Catalog, id, a.r.Want(), a.m.Installed); len(by) > 0 {
			writeConfigJSON(w, http.StatusConflict, map[string]any{"error": "component_required_by", "components": by})
			return
		}
	}
	if a.m.Busy(id) {
		writeConfigJSON(w, http.StatusConflict, map[string]string{"error": packs.ReasonBusy})
		return
	}
	go func() { _ = a.m.Remove(a.ctx, id) }()
	writeConfigJSON(w, http.StatusAccepted, map[string]any{"id": id, "progress": "/components/" + id + "/progress"})
}

func (a *packsAdmin) getProgress(w http.ResponseWriter, id string) {
	if !a.asset(w, id) {
		return
	}
	out := map[string]any{"id": id, "installed": a.m.Installed(id), "progress": nil}
	if p, ok := a.m.Progress(id); ok {
		out["progress"] = p
	}
	writeConfigJSON(w, http.StatusOK, out)
}

func (a *packsAdmin) offlineDir() string { return filepath.Join(a.m.Layout.Root, ".offline-files") }

// sweep deletes offline files past offlineKeep (and their jobs).
func (a *packsAdmin) sweepLocked() {
	for name, j := range a.offline {
		if j.done && time.Since(j.at) > offlineKeep {
			_ = os.Remove(j.path)
			delete(a.offline, name)
		}
	}
	entries, _ := os.ReadDir(a.offlineDir())
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > offlineKeep {
			_ = os.Remove(filepath.Join(a.offlineDir(), e.Name()))
		}
	}
}

func (a *packsAdmin) postOffline(w http.ResponseWriter, r *http.Request) {
	var req packs.OfflineRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if req.Platform == "" {
		req.Platform = a.m.Platform
	}
	if _, err := a.m.OfflineItems(req); err != nil {
		writeConfigJSON(w, http.StatusBadRequest, reasonJSON(err))
		return
	}
	name := packs.OfflineName(a.m.Version, req.Platform, req.Bundle)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.offline == nil {
		a.offline = map[string]*offlineJob{}
	}
	a.sweepLocked()
	if j, ok := a.offline[name]; ok && !j.done {
		writeConfigJSON(w, http.StatusAccepted, map[string]any{"name": name})
		return
	}
	if err := os.MkdirAll(a.offlineDir(), 0o755); err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	j := &offlineJob{path: filepath.Join(a.offlineDir(), name), at: time.Now()}
	a.offline[name] = j
	go func() {
		tmp := j.path + ".partial"
		f, err := os.Create(tmp)
		var items []string
		if err == nil {
			items, err = a.m.MakeOffline(a.ctx, f, req)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err == nil {
			err = os.Rename(tmp, j.path)
		} else {
			_ = os.Remove(tmp)
		}
		a.mu.Lock()
		j.done, j.err, j.items, j.at = true, err, items, time.Now()
		a.mu.Unlock()
	}()
	writeConfigJSON(w, http.StatusAccepted, map[string]any{"name": name})
}

func (a *packsAdmin) getOffline(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" || strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, "maktaba-offline-") || !strings.HasSuffix(name, ".tar") {
		http.NotFound(w, r)
		return
	}
	a.mu.Lock()
	a.sweepLocked()
	j, ok := a.offline[name]
	var job offlineJob
	if ok {
		job = *j
	}
	a.mu.Unlock()
	switch {
	case !ok:
		http.NotFound(w, r)
	case !job.done:
		p, _ := a.m.Progress("offline")
		writeConfigJSON(w, http.StatusAccepted, map[string]any{"name": name, "progress": p})
	case job.err != nil:
		writeConfigJSON(w, http.StatusConflict, reasonJSON(job.err))
	default:
		f, err := os.Open(job.path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		http.ServeContent(w, r, name, job.at, f)
	}
}

func (a *packsAdmin) postImport(w http.ResponseWriter, r *http.Request) {
	var ids []string
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Path string `json:"path"`
		}
		if derr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); derr != nil || body.Path == "" {
			writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
			return
		}
		ids, err = a.m.Import(r.Context(), body.Path)
	} else {
		ids, err = a.m.ImportFrom(r.Context(), http.MaxBytesReader(w, r.Body, 64<<30))
	}
	if err != nil {
		out := reasonJSON(err)
		out["installed"] = ids
		writeConfigJSON(w, http.StatusConflict, out)
		return
	}
	if ids == nil {
		ids = []string{}
	}
	writeConfigJSON(w, http.StatusOK, map[string]any{"installed": ids})
}
