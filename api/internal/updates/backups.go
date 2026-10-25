package updates

// The home's backups and their restore by hand (Epic 35 story 35.5 AC8): the Updates page
// lists the dumps the home keeps and puts one back. The home's supervisor does the work
// (cmd/maktaba-server/serve_restore.go: the kept release of the dump's schema, the
// database record, a restart); the API checks the admin and the typed confirmation,
// audits it, and tells the machines the home goes away for it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// Backup is one dump in the home's backups folder.
type Backup struct {
	Name  string    `json:"name"`
	Bytes int64     `json:"bytes"`
	At    time.Time `json:"at"`
	// Kind: update (taken before an update's migrations: From is the release whose
	// database it holds, To the update's), failed, replaced (by a restore), daily.
	Kind string `json:"kind"`
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// KeptRelease is a release the home keeps (a backup is put back on the one whose schema
// is its own).
type KeptRelease struct {
	Version string `json:"version"`
	Schema  int64  `json:"schema,omitempty"`
	Running bool   `json:"running,omitempty"`
}

// Backups is GET /api/cluster/updates/backups.
type Backups struct {
	Backups []Backup      `json:"backups"`
	Kept    []KeptRelease `json:"kept"`
	Running string        `json:"running"`
	// Restoring is a restore asked and not finished (the home's next start does it).
	Restoring string `json:"restoring,omitempty"`
}

// RestorePlan is what a restore of a backup does: the home restarts on Release (the kept
// release of the dump's Schema) and every change since At is lost.
type RestorePlan struct {
	Backup  string    `json:"backup"`
	Schema  int64     `json:"schema"`
	Release string    `json:"release"`
	Running string    `json:"running"`
	At      time.Time `json:"at"`
}

// RestoreRefused is the supervisor's refusal of a restore, with its code.
type RestoreRefused struct {
	Code   string
	Detail string
	Plan   *RestorePlan
}

func (e *RestoreRefused) Error() string { return e.Detail }

// BackupKeeper is the home's supervisor's restore (SupervisorClient).
type BackupKeeper interface {
	Backups(ctx context.Context) (*Backups, error)
	RestorePlan(ctx context.Context, name string) (*RestorePlan, error)
	Restore(ctx context.Context, name, by string) (*RestorePlan, error)
}

// Backups implements BackupKeeper.
func (c *SupervisorClient) Backups(ctx context.Context) (*Backups, error) {
	var out struct {
		Backups *Backups `json:"backups"`
	}
	if err := c.restoreCall(ctx, http.MethodGet, "/update/backups", nil, &out); err != nil {
		return nil, err
	}
	if out.Backups == nil {
		return &Backups{Backups: []Backup{}, Kept: []KeptRelease{}}, nil
	}
	return out.Backups, nil
}

// RestorePlan implements BackupKeeper.
func (c *SupervisorClient) RestorePlan(ctx context.Context, name string) (*RestorePlan, error) {
	var out struct {
		Plan *RestorePlan `json:"plan"`
	}
	if err := c.restoreCall(ctx, http.MethodGet, "/update/restore?backup="+url.QueryEscape(name), nil, &out); err != nil {
		return nil, err
	}
	return out.Plan, nil
}

// Restore implements BackupKeeper.
func (c *SupervisorClient) Restore(ctx context.Context, name, by string) (*RestorePlan, error) {
	var out struct {
		Plan *RestorePlan `json:"plan"`
	}
	// page: the API audits it and tells the machines (Restore); the supervisor does not
	body := map[string]any{"backup": name, "by": by, "confirm": true, "page": true}
	if err := c.restoreCall(ctx, http.MethodPost, "/update/restore", body, &out); err != nil {
		return nil, err
	}
	return out.Plan, nil
}

func (c *SupervisorClient) restoreCall(ctx context.Context, method, path string, body, out any) error {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return fmt.Errorf("%w: not running under the maktaba-server supervisor", ErrOrchestratorUnavailable)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("supervisor unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var doc struct {
		Code  string       `json:"code"`
		Error string       `json:"error"`
		Plan  *RestorePlan `json:"plan"`
	}
	_ = json.Unmarshal(raw, &doc)
	switch {
	case resp.StatusCode == http.StatusMethodNotAllowed || (resp.StatusCode == http.StatusNotFound && doc.Code == ""):
		// a supervisor from before the restore
		return fmt.Errorf("%w: supervisor answered %d", ErrOrchestratorUnavailable, resp.StatusCode)
	case resp.StatusCode >= 300:
		if doc.Error == "" {
			doc.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		if doc.Code == "" {
			doc.Code = "restore_failed"
		}
		return &RestoreRefused{Code: doc.Code, Detail: doc.Error, Plan: doc.Plan}
	}
	return json.Unmarshal(raw, out)
}

func (h *Handler) keeper(w http.ResponseWriter, r *http.Request) BackupKeeper {
	if h.Driver != nil {
		if k, ok := h.Driver.Home.(BackupKeeper); ok {
			return k
		}
	}
	httperror.Write(w, r, (&httperror.Error{Type: "about:blank#orchestrator_unavailable", Title: "service unavailable",
		Status: http.StatusServiceUnavailable, Detail: "this home's server cannot restore its backups"}).
		With("code", ErrOrchestratorUnavailable.Error()))
	return nil
}

// writeRestoreErr answers a supervisor's error: a refusal with its code (409), the
// supervisor missing (503), anything else 502.
func writeRestoreErr(w http.ResponseWriter, r *http.Request, err error) {
	var rr *RestoreRefused
	switch {
	case errors.As(err, &rr):
		e := httperror.Conflict("about:blank#"+rr.Code, rr.Detail).With("code", rr.Code)
		if rr.Plan != nil {
			e = e.With("plan", rr.Plan)
		}
		httperror.Write(w, r, e)
	case errors.Is(err, ErrOrchestratorUnavailable):
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank#orchestrator_unavailable", Title: "service unavailable",
			Status: http.StatusServiceUnavailable, Detail: err.Error()}).With("code", ErrOrchestratorUnavailable.Error()))
	default:
		httperror.Write(w, r, (&httperror.Error{Type: "about:blank#supervisor_unreachable", Title: "bad gateway",
			Status: http.StatusBadGateway, Detail: err.Error()}).With("code", "supervisor_unreachable"))
	}
}

// BackupsList serves GET /api/cluster/updates/backups.
func (h *Handler) BackupsList(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	k := h.keeper(w, r)
	if k == nil {
		return
	}
	b, err := k.Backups(r.Context())
	if err != nil {
		writeRestoreErr(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, b)
}

// RestorePlanView serves GET /api/cluster/updates/backups/{name}/restore: what restoring
// it does (or why it cannot be done), changing nothing.
func (h *Handler) RestorePlanView(w http.ResponseWriter, r *http.Request) {
	if admin(w, r) == nil {
		return
	}
	k := h.keeper(w, r)
	if k == nil {
		return
	}
	pl, err := k.RestorePlan(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		writeRestoreErr(w, r, err)
		return
	}
	common.WriteJSON(w, r, http.StatusOK, pl)
}

// Restore serves POST /api/cluster/updates/backups/{name}/restore {confirm: "<name>"}:
// the admin types the backup's name (every change since it is lost); audited; the
// machines are told the home goes away for it.
func (h *Handler) Restore(w http.ResponseWriter, r *http.Request) {
	p := admin(w, r)
	if p == nil {
		return
	}
	name := chi.URLParam(r, "name")
	var req struct {
		Confirm string `json:"confirm"`
	}
	if e := common.ReadJSON(r, &req, maxBody); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if strings.TrimSpace(req.Confirm) != name {
		bad(w, r, "confirm_required", "type the backup's name to restore it: every change since it was taken is lost")
		return
	}
	k := h.keeper(w, r)
	if k == nil {
		return
	}
	by := h.Svc.Store.username(r.Context(), p.UserID)
	pl, err := k.Restore(r.Context(), name, by)
	if err != nil {
		writeRestoreErr(w, r, err)
		return
	}
	h.Svc.Store.Audit(r.Context(), p.UserID, "admin", "update.restore", name, map[string]any{
		"release": pl.Release, "schema": pl.Schema, "running": pl.Running, "backup_at": pl.At})
	if h.Driver != nil && h.Driver.Cluster != nil {
		go h.Driver.disconnectMachines(context.WithoutCancel(r.Context()), "home_restore")
	}
	common.WriteJSON(w, r, http.StatusAccepted, pl)
}

// RestoredNotice serves POST /internal/updates/restored, this machine's own supervisor only
// (its per-boot local token): a restore the command line asked for (maktaba-server
// restore), recorded as the page's is — audited, and the machines told the home goes away.
func (h *Handler) RestoredNotice(w http.ResponseWriter, r *http.Request) {
	if !mw.FromSupervisor(r) {
		httperror.Write(w, r, httperror.Forbidden("", "this machine's supervisor only"))
		return
	}
	var req struct {
		Backup   string    `json:"backup"`
		Release  string    `json:"release"`
		Schema   int64     `json:"schema"`
		Running  string    `json:"running"`
		BackupAt time.Time `json:"backup_at"`
		By       string    `json:"by"`
	}
	if e := common.ReadJSON(r, &req, maxBody); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if strings.TrimSpace(req.Backup) == "" {
		bad(w, r, "bad_request", "backup is required")
		return
	}
	h.Svc.Store.Audit(r.Context(), "", "cli", "update.restore", req.Backup, map[string]any{
		"release": req.Release, "schema": req.Schema, "running": req.Running, "backup_at": req.BackupAt, "by": req.By})
	if h.Driver != nil && h.Driver.Cluster != nil {
		go h.Driver.disconnectMachines(context.WithoutCancel(r.Context()), "home_restore")
	}
	w.WriteHeader(http.StatusNoContent)
}

// disconnectMachines tells every machine agent the home goes away (a restore by hand
// restarts it): best effort, each reconnects when the home is back.
func (d *Driver) disconnectMachines(ctx context.Context, reason string) {
	for _, sess := range d.Cluster.MachineAgents() {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, _, _ = d.Cluster.Command(cctx, sess, "disconnect", map[string]any{"reason": reason}, 2*time.Second)
		cancel()
	}
}

// username is a user's name for the record of who asked ("an admin" when not known).
func (s *Store) username(ctx context.Context, id string) string {
	var name string
	if s != nil && s.DB != nil && id != "" {
		_ = s.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, id).Scan(&name)
	}
	if strings.TrimSpace(name) == "" {
		return "an admin"
	}
	return name
}
