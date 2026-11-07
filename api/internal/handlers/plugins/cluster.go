package plugins

// The plugin routes between a home and its joined machines (story 54.42).
//
// On the home, for a request a machine signed (delegation.NodeFrom):
//
//	POST   /internal/cluster/plugins/invocations       {job_id}: a token for a job the machine runs
//	DELETE /internal/cluster/plugins/invocations/{id}  the job ended
//	POST   /internal/cluster/plugin-api/v1/{if}/{fn}   a write the machine's plugin API forwards
//	POST   /internal/cluster/plugins/outcome           a job's outcome, for the breaker
//	GET    /internal/cluster/plugins/files/{id}/*      an installed package's files
//
// On a machine, for its own processes (the supervisor's local token):
//
//	POST   /internal/plugins/outcome                   sent on to the home's
//
// A request no machine signed gets 401: none of these is a user's.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	pl "github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins"
)

// Cluster serves the home's routes for its machines.
type Cluster struct {
	L      *pl.Lifecycle
	Tokens *pl.Tokens
	Server *pl.Server
	Now    func() time.Time
}

func nodeOnly(w http.ResponseWriter, r *http.Request) bool {
	if delegation.NodeFrom(r.Context()) != "" {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "machine_only"})
	return false
}

// Mount adds the home's machine routes to r.
func (c *Cluster) Mount(r chi.Router) {
	r.Post(pl.ClusterMintPath, c.mint)
	r.Delete(pl.ClusterMintPath+"/{id}", c.end)
	r.Post(pl.ClusterAPIPrefix+"*", c.call)
	r.Post(pl.ClusterOutcomePath, c.outcome)
	r.Get(pl.ClusterFilesPrefix+"{id}/*", c.files)
}

func (c *Cluster) mint(w http.ResponseWriter, r *http.Request) {
	if !nodeOnly(w, r) {
		return
	}
	var in struct {
		JobID int64 `json:"job_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil || in.JobID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "plugin_invalid"})
		return
	}
	inv, err := c.Tokens.MintFor(r.Context(), in.JobID)
	var ref *pl.Refusal
	switch {
	case errors.As(err, &ref):
		writeJSON(w, ref.Status, map[string]string{"code": ref.Code, "detail": ref.Detail})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal"})
	default:
		writeJSON(w, http.StatusOK, inv)
	}
}

func (c *Cluster) end(w http.ResponseWriter, r *http.Request) {
	if !nodeOnly(w, r) {
		return
	}
	ended, refused := c.Tokens.EndCounting(r.Context(), chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"ended": ended, "refusals": refused})
}

// call serves a forwarded write as the plugin API: the same token, capability, scope,
// quota and lineage checks as a call on the home's own socket.
func (c *Cluster) call(w http.ResponseWriter, r *http.Request) {
	if !nodeOnly(w, r) {
		return
	}
	route := chi.URLParam(r, "*")
	if !pl.IsForwarded(route) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/plugin-api/v1/" + route
	c.Server.ServeHTTP(w, r2)
}

func (c *Cluster) outcome(w http.ResponseWriter, r *http.Request) {
	if !nodeOnly(w, r) {
		return
	}
	var o pl.Outcome
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&o); err != nil || o.PluginID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "plugin_invalid"})
		return
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	why, err := pl.RecordOutcome(r.Context(), c.L.DB, o, now(), func(ctx context.Context, id, reason string) error {
		_, err := c.L.Trip(ctx, id, reason)
		return err
	})
	if err != nil {
		slog.Default().Warn("plugin_outcome_not_recorded", "plugin", o.PluginID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "internal"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"tripped": why})
}

// files serves one file of an installed package's folder (the current or the kept
// previous version): files.json, its signatures, and the files files.json lists. The
// machine checks every byte against files.json and files.json against a signature.
func (c *Cluster) files(w http.ResponseWriter, r *http.Request) {
	if !nodeOnly(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	rest := chi.URLParam(r, "*")
	folder, file, ok := strings.Cut(rest, "/")
	clean := path.Clean("/" + file)[1:]
	if !ok || folder == "" || clean == "" || clean != file || strings.Contains(folder, "..") || strings.ContainsAny(folder, `/\`) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "plugin_invalid"})
		return
	}
	var cur, prev string
	if err := c.L.DB.QueryRowContext(r.Context(), `SELECT path, COALESCE(previous_path, '') FROM plugin_installs
		WHERE plugin_id = $1`, id).Scan(&cur, &prev); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	var dir string
	for _, p := range []string{cur, prev} {
		if p != "" && filepath.Base(p) == folder {
			dir = p
		}
	}
	if dir == "" || !listed(dir, clean) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	p, ok := within(dir, clean)
	if !ok || !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found"})
		return
	}
	http.ServeFile(w, r, p)
}

// within is name under dir, cleaned, ok false when it would leave dir (or be dir).
func within(dir, name string) (string, bool) {
	p := filepath.Clean(filepath.Join(dir, filepath.FromSlash(name)))
	if !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", false
	}
	return p, true
}

// listed reports whether a package folder serves name: files.json, a signature, or a file
// files.json lists, under the pack's path rule (componentpack.SafePluginPath: a name
// files.json lists that breaks it is never served).
func listed(dir, name string) bool {
	switch name {
	case componentpack.PluginFilesName, componentpack.PluginDeveloperSig, componentpack.PluginStoreSig, componentpack.PluginManifestName:
		p, ok := within(dir, name)
		if !ok || !strings.HasPrefix(p, filepath.Clean(dir)+string(os.PathSeparator)) {
			return false
		}
		_, err := os.Stat(p)
		return err == nil
	}
	if !componentpack.SafePluginPath(name) {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, componentpack.PluginFilesName))
	if err != nil {
		return false
	}
	var list []struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return false
	}
	for _, f := range list {
		if f.Path == name && componentpack.SafePluginPath(f.Path) {
			return true
		}
	}
	return false
}

// Outcome is a machine's POST /internal/plugins/outcome: its dispatcher's outcome of one
// plugin job, sent on to the home (the machine cannot write plugin_health).
func Outcome(remote pl.Remote) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !machineOnly(w, r) {
			return
		}
		var o pl.Outcome
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&o); err != nil || o.PluginID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "plugin_invalid"})
			return
		}
		why, err := remote.Outcome(r.Context(), o)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"code": pl.CodeHomeUnreachable})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"tripped": why})
	}
}
