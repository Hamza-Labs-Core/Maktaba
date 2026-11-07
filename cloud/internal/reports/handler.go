package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/obs"
)

// Limits of what a home sends.
const (
	MaxReport     = 8 << 20
	MaxScreenshot = 1 << 20
	MaxReply      = 2000
)

// Audit actions of the service admin's report routes.
const (
	ActionView   = "admin.report.view"
	ActionStatus = "admin.report.status"
	ActionIssue  = "admin.report.issue"
	ActionReply  = "admin.report.reply"
)

// SecretChecker checks a home's server secret (stores.Servers).
type SecretChecker interface {
	VerifySecret(ctx context.Context, serverID, secret string) error
}

// AgentDeps are the routes a home's agent calls.
type AgentDeps struct {
	Store   *Store
	Servers SecretChecker
	// Refuse answers 503 to new reports (the rollback: homes keep them in their outbox)
	Refuse bool
	Logger *slog.Logger
}

// MountAgent registers the home's routes, authenticated by the server's secret (basic
// auth, id:secret), as the other agent routes are.
func MountAgent(r chi.Router, d *AgentDeps) {
	r.Post("/v1/homes/{id}/reports", d.Receive)
	r.Get("/v1/homes/{id}/reports/replies", d.Replies)
	r.Delete("/v1/homes/{id}/reports", d.DeleteAll)
}

func (d *AgentDeps) home(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, secret, ok := r.BasicAuth()
	if !ok || id != chi.URLParam(r, "id") || d.Servers.VerifySecret(r.Context(), id, secret) != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="maktaba-server"`)
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return "", false
	}
	return id, true
}

// sent is the report's part a home sends ("report"): the home's export of it.
type sent struct {
	ID       int    `json:"id"`
	When     string `json:"when"`
	App      string `json:"app"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Category string `json:"category"`
	Lines    []struct {
		FP string `json:"fp"`
	} `json:"lines"`
	Server []struct {
		FP string `json:"fp"`
	} `json:"server"`
}

// Receive keeps a report a home sends: a multipart form, the "report" part (JSON) and a
// "screenshot" (only when its sender chose it), at most 8 MB in all.
func (d *AgentDeps) Receive(w http.ResponseWriter, r *http.Request) {
	serverID, ok := d.home(w, r)
	if !ok {
		return
	}
	if d.Refuse {
		obs.Default.ProblemReport("refused")
		w.Header().Set("Retry-After", "3600")
		writeErr(w, http.StatusServiceUnavailable, "reports_paused", "Maktaba is not taking reports now; keep it and try later")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxReport)
	if err := r.ParseMultipartForm(MaxReport); err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "a report is at most 8 MB")
		return
	}
	raw := []byte(r.FormValue("report"))
	if len(raw) == 0 {
		if f, _, err := r.FormFile("report"); err == nil {
			raw, _ = io.ReadAll(io.LimitReader(f, MaxReport))
			_ = f.Close()
		}
	}
	var in sent
	if err := json.Unmarshal(raw, &in); err != nil || in.ID <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid_report", "the report part is not a home's report")
		return
	}
	var shot []byte
	if f, fh, err := r.FormFile("screenshot"); err == nil {
		defer f.Close()
		if fh.Size > MaxScreenshot {
			writeErr(w, http.StatusRequestEntityTooLarge, "too_large", "a screenshot is at most 1 MB")
			return
		}
		shot, _ = io.ReadAll(io.LimitReader(f, MaxScreenshot))
	}
	add := In{HomeReportID: in.ID, App: clip(in.App, 64), Version: clip(in.Version, 64), Platform: clip(in.Platform, 64),
		Category: clip(in.Category, 32), Body: raw, Screenshot: shot}
	if t, err := time.Parse(time.RFC3339, in.When); err == nil {
		add.HappenedAt = &t
	}
	seen := map[string]bool{}
	for _, fp := range append(fps(in.Lines), fps(in.Server)...) {
		if fp != "" && len(fp) <= 64 && !seen[fp] && len(add.Fingerprints) < 50 {
			seen[fp] = true
			add.Fingerprints = append(add.Fingerprints, fp)
		}
	}
	id, created, err := d.Store.Add(r.Context(), serverID, add)
	if err != nil {
		d.fail(w, r, "add report", err)
		return
	}
	code := http.StatusOK
	obs.Default.ProblemReport(map[bool]string{true: "created", false: "duplicate"}[created])
	if created {
		code = http.StatusCreated
		if d.Logger != nil {
			d.Logger.Info("problem report received", "report", id, "app", add.App, "category", add.Category,
				"event", "problem_report_received")
		}
	}
	writeJSON(w, code, map[string]any{"id": id})
}

func fps(lines []struct {
	FP string `json:"fp"`
}) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.FP)
	}
	return out
}

// Replies is a home's answered reports since a time (?since=RFC 3339; none: all).
func (d *AgentDeps) Replies(w http.ResponseWriter, r *http.Request) {
	serverID, ok := d.home(w, r)
	if !ok {
		return
	}
	since := time.Time{}
	if s := r.URL.Query().Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_since", "since is an RFC 3339 time")
			return
		}
		since = t
	}
	out, err := d.Store.RepliesSince(r.Context(), serverID, since)
	if err != nil {
		d.fail(w, r, "replies", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"replies": out})
}

// DeleteAll deletes every report of the home at once (its admin's "Delete what Maktaba has").
func (d *AgentDeps) DeleteAll(w http.ResponseWriter, r *http.Request) {
	serverID, ok := d.home(w, r)
	if !ok {
		return
	}
	n, err := d.Store.DeleteHome(r.Context(), serverID)
	if err != nil {
		d.fail(w, r, "delete reports", err)
		return
	}
	if d.Logger != nil {
		d.Logger.Info("a home's problem reports deleted at its request", "deleted", n, "event", "problem_reports_deleted")
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

func (d *AgentDeps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	if d.Logger != nil {
		d.Logger.Error("reports: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
	}
	writeErr(w, http.StatusInternalServerError, "internal_error", "")
}

// AdminDeps are the service admin's routes.
type AdminDeps struct {
	Store  *Store
	DB     *sql.DB
	Logger *slog.Logger
	// Gate admits service admins only (auth/middleware.RequireServiceAdmin); nil refuses everyone
	Gate func(http.Handler) http.Handler
}

// MountAdmin registers /v1/admin/reports behind the service admin's gate. A home admin's
// cloud token is refused like any account without the role: a home has its reports at home.
func MountAdmin(r chi.Router, d *AdminDeps) {
	gate := d.Gate
	if gate == nil {
		gate = func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeErr(w, http.StatusForbidden, "admin_access_denied", "")
			})
		}
	}
	r.Group(func(g chi.Router) {
		g.Use(gate)
		g.Get("/v1/admin/reports", d.List)
		g.Get("/v1/admin/reports/{id}", d.Get)
		g.Get("/v1/admin/reports/{id}/screenshot", d.Screenshot)
		g.Post("/v1/admin/reports/{id}/status", d.SetStatus)
		g.Post("/v1/admin/reports/{id}/issue", d.SetIssue)
		g.Post("/v1/admin/reports/{id}/reply", d.SetReply)
	})
}

// List is the reports across homes (?version, app, category, status, fp, limit, offset).
func (d *AdminDeps) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := d.Store.List(r.Context(), Filter{Version: q.Get("version"), App: q.Get("app"),
		Category: q.Get("category"), Status: q.Get("status"), Fingerprint: q.Get("fp"), Limit: limit, Offset: offset})
	if err != nil {
		d.fail(w, r, "list", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": list, "total": total})
}

func reportID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return 0, false
	}
	return id, true
}

// Get is one report; reading it (the home's owner is named) is audited.
func (d *AdminDeps) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	rep, err := d.Store.Get(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		d.fail(w, r, "get", err)
		return
	}
	if err := audit.WriteTx(r.Context(), d.DB, d.entry(r, ActionView, id, nil)); err != nil {
		d.fail(w, r, "audit", err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// Screenshot is a report's screenshot.
func (d *AdminDeps) Screenshot(w http.ResponseWriter, r *http.Request) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	b, err := d.Store.Screenshot(r.Context(), id)
	if errors.Is(err, ErrNotFound) || (err == nil && len(b) == 0) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err != nil {
		d.fail(w, r, "screenshot", err)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(b))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(b)
}

// change runs one audited change in a transaction: a change whose audit row cannot be
// written does not happen.
func (d *AdminDeps) change(w http.ResponseWriter, r *http.Request, action string, detail map[string]any,
	do func(ctx context.Context, tx *sql.Tx, id int64) error) {
	id, ok := reportID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		d.fail(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if err := do(ctx, tx, id); errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	} else if err != nil {
		d.fail(w, r, action, err)
		return
	}
	if err := audit.WriteTx(ctx, tx, d.entry(r, action, id, detail)); err != nil {
		d.fail(w, r, "audit", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "commit", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "request body is not valid JSON")
		return false
	}
	return true
}

// SetStatus is {"status": new|triaged|fixed|closed, "fixed_in": "0.3.95"} (a version only when fixed).
func (d *AdminDeps) SetStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status  string `json:"status"`
		FixedIn string `json:"fixed_in"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !Statuses[in.Status] {
		writeErr(w, http.StatusBadRequest, "invalid_status", "new, triaged, fixed or closed")
		return
	}
	fixedIn := ""
	if in.Status == "fixed" {
		fixedIn = clip(strings.TrimSpace(in.FixedIn), 32)
	}
	d.change(w, r, ActionStatus, map[string]any{"status": in.Status, "fixed_in": fixedIn},
		func(ctx context.Context, tx *sql.Tx, id int64) error {
			return d.Store.SetStatus(ctx, tx, id, in.Status, fixedIn)
		})
}

// SetIssue is {"url": "https://github.com/…/issues/123"} (empty: unlinked).
func (d *AdminDeps) SetIssue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.URL != "" {
		u, err := url.Parse(in.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(in.URL) > 300 {
			writeErr(w, http.StatusBadRequest, "invalid_url", "an https address of the issue")
			return
		}
	}
	d.change(w, r, ActionIssue, map[string]any{"url": in.URL},
		func(ctx context.Context, tx *sql.Tx, id int64) error { return d.Store.SetIssue(ctx, tx, id, in.URL) })
}

// SetReply is {"reply": "…"}: the home's admin sees it in the report, and its sender in My reports.
func (d *AdminDeps) SetReply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reply string `json:"reply"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Reply = strings.TrimSpace(in.Reply)
	if in.Reply == "" || utf8.RuneCountInString(in.Reply) > MaxReply {
		writeErr(w, http.StatusBadRequest, "invalid_reply", "a reply of 1 to 2,000 characters")
		return
	}
	d.change(w, r, ActionReply, map[string]any{"length": utf8.RuneCountInString(in.Reply)},
		func(ctx context.Context, tx *sql.Tx, id int64) error { return d.Store.SetReply(ctx, tx, id, in.Reply) })
}

func (d *AdminDeps) entry(r *http.Request, action string, id int64, detail map[string]any) audit.Entry {
	e := audit.Entry{Action: action, TargetKind: "problem_report", TargetID: strconv.FormatInt(id, 10), Detail: detail,
		IP: middleware.GetClientIP(r.Context())}
	if u, ok := authmw.ServiceAdmin(r.Context()); ok {
		e.ActorID, e.ActorEmail = u.ID, u.Email
	}
	return e
}

func (d *AdminDeps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	if d.Logger != nil {
		d.Logger.Error("admin reports: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
	}
	writeErr(w, http.StatusInternalServerError, "internal_error", "")
}

// RunSweeper deletes the reports past their 90 days, once an hour until ctx ends.
func RunSweeper(ctx context.Context, s *Store, logger *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := s.Sweep(ctx); err != nil && ctx.Err() == nil && logger != nil {
			logger.Warn("reports: the retention sweep failed", "err", err, "event", "problem_reports_sweep_failed")
		} else if n > 0 && logger != nil {
			logger.Info("reports: past their 90 days, deleted", "deleted", n, "event", "problem_reports_swept")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"error": kind, "message": msg})
}
