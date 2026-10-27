// Package serversettings is the admin's view of server.toml (Epic 45, story 45.3):
//
//	GET  /api/server/settings  the supervisor's settings document, plus where each
//	                           library folder is on this machine under the saved
//	                           path map and whether it can be read
//	PUT  /api/server/settings  a partial change (the supervisor validates and writes)
//	POST /api/server/restart   ask the launcher to start the server again
//
// server.toml is maktaba-server's: the API never writes it. These routes proxy the
// supervisor's loopback /config/ endpoints (MAKTABA_SUPERVISOR_URL, the per-boot
// token in MAKTABA_SUPERVISOR_TOKEN_FILE). Without a supervisor (compose, a lone
// --role api) GET answers {available: false} and the writes 409. Admins only, every
// other caller 403 before the supervisor is called; each write is audited
// (category 'server').
package serversettings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

// Audit actions (category 'server').
const (
	ActionSettingsChanged  = "settings-changed"
	ActionRestartRequested = "restart-requested"
)

// Supervisor is the supervisor's loopback admin.
type Supervisor struct {
	URL       string
	TokenFile string
	HTTP      *http.Client
}

// SupervisorFromEnv is the supervisor this API runs under (nil: none).
func SupervisorFromEnv() *Supervisor {
	u := strings.TrimRight(strings.TrimSpace(os.Getenv("MAKTABA_SUPERVISOR_URL")), "/")
	tf := strings.TrimSpace(os.Getenv("MAKTABA_SUPERVISOR_TOKEN_FILE"))
	if u == "" || tf == "" {
		return nil
	}
	return &Supervisor{URL: u, TokenFile: tf}
}

// call sends one request to <url>/config/<path> and returns its status and body.
func (s *Supervisor) call(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	tok, err := os.ReadFile(s.TokenFile)
	if err != nil {
		return 0, nil, fmt.Errorf("the supervisor's token: %w", err)
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL+"/config/"+path, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c := s.HTTP
	if c == nil {
		c = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("supervisor unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("the supervisor's answer: %w", err)
	}
	return resp.StatusCode, out, nil
}

// Handler serves the three routes.
type Handler struct {
	// Supervisor is nil without one.
	Supervisor *Supervisor
	// DB holds the libraries (reachability) and audit_log; nil: neither.
	DB *sql.DB
	// Shares maps a canonical root (smb://nas/share/...) to this machine's mount
	// (nil: sharepath's installed map).
	Shares func(string) string
}

// Mount wires the routes. Each checks the caller itself, so an anonymous caller
// gets the same 403 as a viewer.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/server/settings", h.admin(h.get))
	r.Put("/api/server/settings", h.admin(h.put))
	r.Post("/api/server/restart", h.admin(h.restart))
	h.mountStorage(r)
}

func (h *Handler) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if p := principal.FromContext(r.Context()); p == nil || !p.IsAdmin {
			httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
			return
		}
		next(w, r)
	}
}

var errNoSupervisor = errors.New("this API runs under no maktaba-server supervisor")

// document is the supervisor's settings document as the API reads it.
type document struct {
	Saved struct {
		Media struct {
			PathMap []struct {
				DB    string `json:"db"`
				Local string `json:"local"`
			} `json:"path_map"`
			Roots []string `json:"roots"`
		} `json:"media"`
	} `json:"saved"`
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"available": false, "reason": errNoSupervisor.Error()})
		return
	}
	code, body, err := h.Supervisor.call(r.Context(), http.MethodGet, "settings", nil)
	if err == nil && code != http.StatusOK {
		err = fmt.Errorf("the supervisor answered %d: %s", code, strings.TrimSpace(string(body)))
	}
	if err != nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"available": false, "reason": err.Error()})
		return
	}
	h.writeDoc(w, r, body)
}

// writeDoc answers the supervisor's document with available and reachability.
func (h *Handler) writeDoc(w http.ResponseWriter, r *http.Request, body []byte) {
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		httperror.Write(w, r, httperror.BadGateway("the supervisor's settings: "+err.Error()))
		return
	}
	var doc document
	_ = json.Unmarshal(body, &doc)
	out["available"] = true
	out["reachability"] = h.reachability(r.Context(), doc)
	common.WriteJSON(w, r, http.StatusOK, out)
}

func (h *Handler) put(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, common.MaxBodyBytes))
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("body too large"))
		return
	}
	if !json.Valid(body) {
		httperror.Write(w, r, httperror.BadRequest("the settings are not JSON"))
		return
	}
	// what the file held before, for the audit row's list of changes
	var before map[string]any
	if code, b, err := h.Supervisor.call(r.Context(), http.MethodGet, "settings", nil); err == nil && code == http.StatusOK {
		before = savedOf(b)
	}
	code, out, err := h.Supervisor.call(r.Context(), http.MethodPut, "settings", body)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway(err.Error()))
		return
	}
	if code != http.StatusOK {
		// 422 {errors: [{field, message}]} and 400 go back as the supervisor gave them
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(out)
		return
	}
	if changes := changedFields(before, savedOf(out)); len(changes) > 0 {
		h.audit(r, ActionSettingsChanged, map[string]any{"changed": changes})
	}
	h.writeDoc(w, r, out)
}

func (h *Handler) restart(w http.ResponseWriter, r *http.Request) {
	if h.Supervisor == nil {
		httperror.Write(w, r, httperror.Conflict("no-supervisor", errNoSupervisor.Error()+": restart the server yourself"))
		return
	}
	code, out, err := h.Supervisor.call(r.Context(), http.MethodPost, "restart", []byte("{}"))
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway(err.Error()))
		return
	}
	if code/100 == 2 {
		h.audit(r, ActionRestartRequested, map[string]any{})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(out)
}

// savedOf is the "saved" document flattened to "media.path_map", "server.listen", ….
func savedOf(body []byte) map[string]any {
	var doc struct {
		Saved map[string]map[string]any `json:"saved"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.Saved == nil {
		return nil
	}
	out := map[string]any{}
	for section, keys := range doc.Saved {
		for k, v := range keys {
			out[section+"."+k] = v
		}
	}
	return out
}

// changedFields: each key whose value differs, with its new value. These settings
// hold no secret (paths, addresses, URLs, a switch); without the document from
// before, every key is listed.
func changedFields(before, after map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range after {
		if old, ok := before[k]; !ok || !reflect.DeepEqual(old, v) {
			out[k] = v
		}
	}
	return out
}

func (h *Handler) audit(r *http.Request, action string, payload map[string]any) {
	if h.DB == nil {
		return
	}
	b, _ := json.Marshal(payload)
	var actor any
	if p := principal.FromContext(r.Context()); p != nil {
		if _, err := uuid.Parse(p.UserID); err == nil {
			actor = p.UserID
		}
	}
	// best-effort, as the other audit writers: a missing row never undoes the change
	_, _ = h.DB.ExecContext(r.Context(), `INSERT INTO audit_log (category, action, actor_user_id, payload)
		VALUES ('server', $1, $2, $3)`, action, actor, string(b))
}

// Reach is one library folder, where it is on this machine and whether it can be read.
type Reach struct {
	LibraryID   string `json:"library_id"`
	LibraryName string `json:"library_name"`
	Root        string `json:"root"`
	Local       string `json:"local"`
	Reachable   bool   `json:"reachable"`
	// Problem: not-found, not-readable or not-a-directory ("" when reachable).
	Problem string `json:"problem,omitempty"`
}

// reachability maps every library root through the saved path map (a canonical
// smb:// root through this machine's mounts first) and looks at it.
func (h *Handler) reachability(ctx context.Context, doc document) []Reach {
	out := []Reach{}
	if h.DB == nil {
		return out
	}
	var pairs []string
	for _, p := range doc.Saved.Media.PathMap {
		pairs = append(pairs, p.DB+"="+p.Local)
	}
	pm, _ := agent.ParsePathMap(strings.Join(pairs, ";"))
	rows, err := h.DB.QueryContext(ctx, `SELECT id, name, roots FROM libraries ORDER BY name, id`)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, name string
		var raw sql.NullString
		if rows.Scan(&id, &name, &raw) != nil {
			continue
		}
		for _, root := range parseRoots(raw.String) {
			local := root
			if sharepath.IsCanonical(root) || strings.HasPrefix(strings.ToLower(root), "file:///") {
				local = h.shares(root)
			}
			local = filepath.FromSlash(pm.ToLocal(local))
			rc := Reach{LibraryID: id, LibraryName: name, Root: root, Local: local}
			rc.Problem = h.problem(local)
			rc.Reachable = rc.Problem == ""
			out = append(out, rc)
		}
	}
	return out
}

func (h *Handler) shares(p string) string {
	if h.Shares != nil {
		return h.Shares(p)
	}
	return sharepath.ToLocal(p)
}

func (*Handler) problem(local string) string {
	if !filepath.IsAbs(local) {
		return "not-found"
	}
	fi, err := os.Stat(local)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "not-found"
	case err != nil:
		return "not-readable"
	case !fi.IsDir():
		return "not-a-directory"
	}
	f, err := os.Open(local)
	if err != nil {
		return "not-readable"
	}
	_ = f.Close()
	return ""
}

// parseRoots reads libraries.roots: a Postgres TEXT[] literal ({a,"b c"}) or the
// JSON array the SQLite shim stores.
func parseRoots(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var xs []string
		_ = json.Unmarshal([]byte(s), &xs)
		return xs
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil
	}
	body := s[1 : len(s)-1]
	var out []string
	var cur strings.Builder
	quoted, esc, seen := false, false, false
	for _, r := range body {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '"':
			quoted = !quoted
			seen = true
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
			seen = false
		default:
			cur.WriteRune(r)
			seen = true
		}
	}
	if seen || cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
