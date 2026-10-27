package main

// /config/ on the supervisor's loopback admin (Epic 45, story 45.3): the
// settings the web's Storage & discovery page changes in server.toml, and a
// restart to take them. The API proxies it for its admins; the per-boot local
// token is required on every route.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// configAdmin serves /config/settings, /config/restart and /config/storage
// (serve_storage.go).
type configAdmin struct {
	// path is the server.toml serve reads.
	path string
	// running is what this serve started with.
	running config.Editable
	token   string
	// restart asks the launcher to start serve again (nil: no launcher).
	restart func() error
	checks  config.Checks
	// reload asks serve to stop every process and start again (a storage move)
	reload func()

	mu sync.Mutex // one write at a time
}

// newConfigAdmin: restarts go through the launcher's restart request when serve
// runs under it (the file watchLauncher takes).
func newConfigAdmin(cfg config.Config, token, releaseDir string) *configAdmin {
	c := &configAdmin{path: config.Path(), running: config.EditableOf(cfg), token: token,
		reload: func() { time.AfterFunc(answerFirst, requestReload) }}
	if os.Getenv(layout.EnvLauncher) == "1" && releaseDir != "" {
		l := layout.New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(releaseDir)))))
		c.restart = l.RequestRestart
	}
	return c
}

// settingsDoc is GET /config/settings' answer.
type settingsDoc struct {
	File          string          `json:"file"`
	Saved         config.Editable `json:"saved"`
	Running       config.Editable `json:"running"`
	RestartNeeded bool            `json:"restart_needed"`
	CanRestart    bool            `json:"can_restart"`
}

// adminHandler adds /config/ to base.
func (c *configAdmin) adminHandler(base http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", base)
	mux.HandleFunc("/config/", func(w http.ResponseWriter, r *http.Request) {
		if !c.authorized(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if c.storageRoutes(w, r) {
			return
		}
		switch {
		case r.URL.Path == "/config/settings" && r.Method == http.MethodGet:
			c.get(w)
		case r.URL.Path == "/config/settings" && r.Method == http.MethodPut:
			c.put(w, r)
		case r.URL.Path == "/config/restart" && r.Method == http.MethodPost:
			c.postRestart(w)
		case r.URL.Path == "/config/settings" || r.URL.Path == "/config/restart":
			http.Error(w, "method", http.StatusMethodNotAllowed)
		default:
			http.NotFound(w, r)
		}
	})
	return mux
}

func (c *configAdmin) doc() (settingsDoc, error) {
	cfg, _, err := config.LoadFrom(c.path)
	if err != nil {
		return settingsDoc{}, err
	}
	saved := config.EditableOf(cfg)
	return settingsDoc{
		File:          c.path,
		Saved:         saved,
		Running:       c.running,
		RestartNeeded: !reflect.DeepEqual(saved, c.running),
		CanRestart:    c.restart != nil,
	}, nil
}

func (c *configAdmin) get(w http.ResponseWriter) {
	d, err := c.doc()
	if err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeConfigJSON(w, http.StatusOK, d)
}

func (c *configAdmin) put(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	var patch config.SettingsPatch
	if err := json.Unmarshal(body, &patch); err != nil {
		writeConfigJSON(w, http.StatusBadRequest, map[string]string{"error": "the settings: " + err.Error()})
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// load, change, save: every other key of the file stays as it is
	cfg, _, err := config.LoadFrom(c.path)
	if err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	saved := config.EditableOf(cfg)
	next := saved
	next.Media.PathMap = append([]config.PathPair{}, saved.Media.PathMap...)
	next.Media.Roots = append([]string{}, saved.Media.Roots...)
	changed := patch.Apply(&next)
	if errs := config.OnlyChanged(c.checks.Validate(next, saved, c.running), changed); len(errs) > 0 {
		writeConfigJSON(w, http.StatusUnprocessableEntity, map[string]any{"errors": errs})
		return
	}
	if len(changed) > 0 {
		next.Into(&cfg)
		if err := cfg.SaveTo(c.path); err != nil {
			writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		fmt.Fprintf(os.Stderr, "[serve] server.toml changed from the web: %v\n", changed)
	}
	c.get(w)
}

func (c *configAdmin) postRestart(w http.ResponseWriter) {
	if c.restart == nil {
		writeConfigJSON(w, http.StatusConflict, map[string]string{
			"error": "this server does not run under maktaba-launcher: restart the server yourself"})
		return
	}
	if err := c.restart(); err != nil {
		writeConfigJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeConfigJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
}

func writeConfigJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
