// Package logs implements the troubleshooting log-collection surface:
//
//	GET /api/admin/logs/stream   — admin: recent structured logs (JSON) for the live viewer
//	GET /api/admin/logs/files    — admin: a machine's server.log, app.log, launcher.log over a window
//	GET /api/admin/logs/export   — admin: full diagnostics bundle (.tar.gz or JSON)
//	GET /api/diagnostics/export  — any authenticated user: reduced-scope bundle for support
//
// The bundle gathers, into a .tar.gz:
//
//	system-info.json     host / version / resource data
//	api-logs.jsonl       this service's ring-buffer logs
//	streaming-logs.jsonl proxied from the streaming service's /logs/recent
//	pipeline-logs.jsonl  proxied from the pipeline's /logs/recent
//	error-summary.json   deduplicated error counts (last N unique errors)
//	health-check.json    current cross-service health snapshot
//	files/*.log          admin: this machine's server.log, app.log, launcher.log over
//	                     ?since/until (node-<id>/files/*.log: that machine's, ?node=)
//
// Admin export sees every line; the user-facing diagnostics export is
// scoped to the requesting user's own log lines (plus un-attributed
// system lines) so one user can't pull another's activity.
package logs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/serversettings"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/version"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// PeerLog names a downstream service whose ring buffer the API proxies
// into the bundle. URL is the service's /logs/recent endpoint (on its
// internal/admin port).
type PeerLog struct {
	Service string
	URL     string
}

// Handler bundles the diagnostics-export dependencies. Every field is
// optional: a nil Ring/DB/empty Peers simply yields an emptier bundle
// rather than a failure, so a bare-bones dev server still produces a
// downloadable file.
type Handler struct {
	// Ring is this service's in-memory log buffer (mlog.Ring()).
	Ring *mlog.RingBuffer
	// DB backs the connection-stats and active-job sections. nil omits them.
	DB *sql.DB
	// DataDir is the volume whose free space is reported. Empty omits it.
	DataDir string
	// SchemaRev is the binary's expected schema revision.
	SchemaRev int
	// StartTime stamps process uptime.
	StartTime time.Time
	// Health, when set, is invoked to capture the health-check snapshot
	// (the /api/system/health aggregator handler is passed here).
	Health http.Handler
	// Peers are downstream services (streaming, pipeline) whose recent
	// logs are proxied into the bundle.
	Peers []PeerLog
	// Client is the HTTP client used to proxy peer logs. nil uses a
	// short-timeout default.
	Client *http.Client
	// FileSource reads a machine's own log files (files.go). nil: none are there.
	FileSource FileSource
	// IndexAddr is the log index role's address (index.go; "": MAKTABA_LOGS_ADDR).
	IndexAddr string
	// Supervisor keeps the diagnostics' settings (retention.go; nil: from the environment).
	Supervisor *serversettings.Supervisor
	// LogsRoot is <data>/logs, where crash reports and problem reports are kept
	// ("": MAKTABA_DATA_DIR/logs).
	LogsRoot string
}

// Mount wires the routes. Admin routes are gated with RequireAdmin; the
// diagnostics route is available to any authenticated principal (the
// security chain populates it) and self-scopes its contents.
func (h *Handler) Mount(r chi.Router) {
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/stream", h.Stream)
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/files", h.Files)
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/export", h.AdminExport)
	// Epic 53 story 53.09: the log index, admins only
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/search", h.proxyIndex("/search", false))
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/tail", h.proxyIndex("/tail", true))
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/around", h.proxyIndex("/around", false))
	// error groups (story 53.10)
	r.With(authmw.RequireAdmin).Get("/api/admin/errors", h.proxyIndex("/errors", false))
	r.With(authmw.RequireAdmin).Get("/api/admin/errors/{fp}", h.proxyIndex("/errors/{fp}", false))
	r.With(authmw.RequireAdmin).Post("/api/admin/errors/{fp}/status", h.errorStatus)
	// the Retention page and detailed logs (story 53.11)
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/retention", h.proxyConfig("/config/logs", ""))
	r.With(authmw.RequireAdmin).Put("/api/admin/logs/retention", h.proxyConfig("/config/logs", EventRetentionChanged))
	r.With(authmw.RequireAdmin).Post("/api/admin/logs/level", h.proxyConfig("/config/logs/level", EventDetailedLogs))
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/users", h.users)
	// the dashboards (story 53.12)
	r.With(authmw.RequireAdmin).Get("/api/admin/dashboards/{board}", h.dashboard)
	r.With(authmw.RequireAuth).Get("/api/diagnostics/export", h.UserExport)
	// crash reports and problem reports (stories 53.14, 53.15)
	r.With(authmw.RequireAuth).Post("/api/diagnostics/envelope", h.envelope)
	r.With(authmw.RequireAuth).Post("/api/reports", h.createReport)
	r.With(authmw.RequireAuth).Get("/api/reports", h.listReports)
	r.With(authmw.RequireAuth).Get("/api/reports/{id}", h.getReport)
	r.With(authmw.RequireAdmin).Post("/api/reports/{id}/status", h.changeReport("status"))
	r.With(authmw.RequireAdmin).Post("/api/reports/{id}/reply", h.changeReport("reply"))
	r.With(authmw.RequireAdmin).Get("/api/reports/{id}/bundle", h.reportBundle)
}

func (h *Handler) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 3 * time.Second}
}

// Stream returns the recent local-ring entries as a JSON array for the
// live log viewer. It applies the standard since/level/services/q/limit
// filter params (shared with the ring's RecentHandler).
func (h *Handler) Stream(w http.ResponseWriter, r *http.Request) {
	if h.Ring == nil {
		common.WriteJSON(w, r, http.StatusOK, streamResponse{Entries: []json.RawMessage{}})
		return
	}
	f := mlog.FilterFromQuery(r)
	if f.Limit == 0 {
		f.Limit = 1000 // viewer default — keep the poll payload bounded
	}
	entries := h.Ring.Entries(f)
	raws := make([]json.RawMessage, len(entries))
	for i, e := range entries {
		raws[i] = e.Raw
	}
	common.WriteJSON(w, r, http.StatusOK, streamResponse{Entries: raws, Count: len(raws)})
}

type streamResponse struct {
	Entries []json.RawMessage `json:"entries"`
	Count   int               `json:"count"`
}

// AdminExport builds the full diagnostics bundle.
func (h *Handler) AdminExport(w http.ResponseWriter, r *http.Request) {
	h.export(w, r, "")
}

// UserExport builds a reduced-scope bundle for support, scoped to the
// requesting user's own log lines.
func (h *Handler) UserExport(w http.ResponseWriter, r *http.Request) {
	// RequireAuth has already rejected the unauthenticated case; the
	// nil guard is belt-and-braces so a mis-wired chain fails closed.
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "authentication required"))
		return
	}
	h.export(w, r, p.UserID)
}

// export is the shared bundle builder. scopeUserID == "" means the full
// admin view; a non-empty value restricts log lines to that user (plus
// un-attributed system lines) and omits cross-user sections.
func (h *Handler) export(w http.ResponseWriter, r *http.Request, scopeUserID string) {
	ctx := r.Context()
	baseFilter := mlog.FilterFromQuery(r)

	sysInfo := h.systemInfo(ctx, scopeUserID != "")
	// the server's health (disks, budgets, failure reasons) is an admin's to read
	healthJSON := []byte(`{"status":"left out: an admin's export has it"}`)
	if scopeUserID == "" {
		healthJSON = h.healthSnapshot(r)
	}

	apiLogs := h.localLogs(baseFilter, scopeUserID)
	errSummary := errorSummary(h.scopedEntries(mlog.Filter{MinLevel: 0}, scopeUserID))

	// Peer logs are best-effort: a down service yields an empty file
	// with an inline note rather than failing the whole export. On a
	// user-scoped export the peers cannot pre-filter by user (their
	// /logs/recent has no user filter), so we apply the same per-user
	// scope to the proxied JSONL here — otherwise the bundle would leak
	// other users' streaming/pipeline lines.
	peerLogs := map[string][]byte{}
	peerQ := peerQuery(baseFilter)
	for _, peer := range h.Peers {
		data := h.fetchPeerLogs(ctx, peer, peerQ)
		if scopeUserID != "" {
			data = scopeJSONL(data, scopeUserID)
		}
		peerLogs[peer.Service] = data
	}

	files := map[string][]byte{
		"system-info.json":   mustJSON(sysInfo),
		"api-logs.jsonl":     apiLogs,
		"error-summary.json": mustJSON(errSummary),
		"health-check.json":  healthJSON,
	}
	for svc, data := range peerLogs {
		files[svc+"-logs.jsonl"] = data
	}
	// Admin export also includes active-job status and the machine's own
	// log files over the window (and a machine's, ?node=); the user export
	// omits them (cross-user data).
	if scopeUserID == "" {
		files["jobs.json"] = mustJSON(h.activeJobs(ctx))
		for k, v := range h.bundleFiles(ctx, "", "", r.URL.Query()) {
			files[k] = v
		}
		if node := strings.TrimSpace(r.URL.Query().Get("node")); node != "" && nodeIDRe.MatchString(node) {
			for k, v := range h.bundleFiles(ctx, node, "node-"+node+"/", r.URL.Query()) {
				files[k] = v
			}
		}
	}

	if r.URL.Query().Get("format") == "json" {
		h.writeJSONBundle(w, r, files)
		return
	}
	h.writeTarball(w, r, files)
}

// systemInfo assembles the host/version/resource snapshot.
func (h *Handler) systemInfo(_ context.Context, scoped bool) SystemInfo {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	info := SystemInfo{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Service:     "api",
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		GoVersion:   runtime.Version(),
		NumCPU:      runtime.NumCPU(),
		Goroutines:  runtime.NumGoroutine(),
		Version:     version.Version,
		BuildSHA:    version.Commit,
		BuildTime:   version.BuildDate,
		SchemaRev:   h.SchemaRev,
		Scoped:      scoped,
		Memory: MemInfo{
			AllocBytes:     mem.Alloc,
			SysBytes:       mem.Sys,
			HeapAllocBytes: mem.HeapAlloc,
			HeapInUseBytes: mem.HeapInuse,
			NumGC:          mem.NumGC,
		},
	}
	if !h.StartTime.IsZero() {
		info.UptimeSeconds = time.Since(h.StartTime).Seconds()
	}
	if h.DataDir != "" {
		if free, err := system.DiskFreeBytes(h.DataDir); err == nil {
			info.DiskFreeBytes = free
		}
	}
	if h.DB != nil {
		s := h.DB.Stats()
		info.DB = &DBStats{
			MaxOpenConnections: s.MaxOpenConnections,
			OpenConnections:    s.OpenConnections,
			InUse:              s.InUse,
			Idle:               s.Idle,
			WaitCount:          s.WaitCount,
			WaitDurationMs:     s.WaitDuration.Milliseconds(),
		}
	}
	return info
}

// healthSnapshot captures the aggregator's JSON. Falls back to a stub
// when no Health handler is wired.
func (h *Handler) healthSnapshot(r *http.Request) []byte {
	if h.Health == nil {
		return mustJSON(map[string]string{"status": "unknown", "reason": "aggregator unwired"})
	}
	rec := newBufferRecorder()
	probe := r.Clone(r.Context())
	h.Health.ServeHTTP(rec, probe)
	if rec.body.Len() == 0 {
		return mustJSON(map[string]string{"status": "unknown"})
	}
	return rec.body.Bytes()
}

// localLogs renders this service's ring entries to JSONL, applying the
// per-user scope when set.
func (h *Handler) localLogs(f mlog.Filter, scopeUserID string) []byte {
	var buf bytes.Buffer
	for _, e := range h.scopedEntries(f, scopeUserID) {
		buf.Write(e.Raw)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// scopedEntries returns ring entries filtered by f and, when
// scopeUserID is set, restricted to lines belonging to that user. Lines
// with no user_id are the server's own (other users' library paths and
// errors among them): an admin's export only.
func (h *Handler) scopedEntries(f mlog.Filter, scopeUserID string) []mlog.Entry {
	if h.Ring == nil {
		return nil
	}
	entries := h.Ring.Entries(f)
	if scopeUserID == "" {
		return entries
	}
	out := entries[:0:0]
	hash := mlog.HashUser(scopeUserID)
	for _, e := range entries {
		if ownedBy(e.Raw, scopeUserID, hash) {
			out = append(out, e)
		}
	}
	return out
}

// ownedBy reports whether a log line belongs to the user: it names them by user_h (their
// hash under this machine's log key, Epic 53 story 53.01) or, on a line from before, by
// user_id. A line that does not parse belongs to no one (fail-closed).
func ownedBy(raw []byte, userID, hash string) bool {
	var rec struct {
		UserID string `json:"user_id"`
		UserH  string `json:"user_h"`
	}
	if json.Unmarshal(raw, &rec) != nil {
		return false
	}
	return (rec.UserID != "" && rec.UserID == userID) || (hash != "" && rec.UserH == hash)
}

// peerQuery is the filter a peer's /logs/recent is asked for, encoded again from the
// parsed filter: only the parameters that endpoint reads, never the caller's raw query, so
// a caller cannot hand an internal service a parameter of its own choosing.
func peerQuery(f mlog.Filter) url.Values {
	q := url.Values{}
	switch {
	case f.MinLevel <= slog.LevelDebug:
		q.Set("level", "debug")
	case f.MinLevel >= slog.LevelError:
		q.Set("level", "error")
	case f.MinLevel >= slog.LevelWarn:
		q.Set("level", "warn")
	default:
		q.Set("level", "info")
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339Nano))
	}
	if !f.Until.IsZero() {
		q.Set("until", f.Until.UTC().Format(time.RFC3339Nano))
	}
	if len(f.Services) > 0 {
		names := make([]string, 0, len(f.Services))
		for s := range f.Services {
			names = append(names, s)
		}
		sort.Strings(names)
		q.Set("services", strings.Join(names, ","))
	}
	if f.Search != "" {
		q.Set("q", f.Search)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return q
}

// fetchPeerLogs proxies a downstream service's /logs/recent. On any
// error it returns a single JSONL note line so the bundle records that
// the service was unreachable rather than silently omitting it. The
// peer's address is the server's own configuration (PeerLog.URL); only
// the query, from peerQuery (over any the configuration gives), follows
// the request, after its "?".
func (h *Handler) fetchPeerLogs(ctx context.Context, peer PeerLog, filter url.Values) []byte {
	base, configured, _ := strings.Cut(peer.URL, "?")
	q, _ := url.ParseQuery(configured)
	for k, v := range filter {
		q[k] = v
	}
	target := base
	if enc := q.Encode(); enc != "" {
		target = base + "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return peerNote(peer.Service, err.Error())
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return peerNote(peer.Service, err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return peerNote(peer.Service, fmt.Sprintf("status %d", resp.StatusCode))
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, 16<<20)); err != nil {
		return peerNote(peer.Service, "read: "+err.Error())
	}
	return buf.Bytes()
}

func peerNote(service, reason string) []byte {
	line, _ := json.Marshal(map[string]string{
		"ts":      time.Now().UTC().Format(time.RFC3339),
		"level":   "warn",
		"service": service,
		"msg":     "logs unavailable for export",
		"reason":  reason,
	})
	return append(line, '\n')
}

// activeJobs counts processing_jobs by state. Best-effort: returns an
// empty map (with an error note) when the DB is unwired or the query
// fails, never failing the export.
func (h *Handler) activeJobs(ctx context.Context) JobStatus {
	if h.DB == nil {
		return JobStatus{ByState: map[string]int{}, Note: "database unwired"}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := h.DB.QueryContext(ctx,
		`SELECT state, count(*) FROM processing_jobs GROUP BY state`)
	if err != nil {
		return JobStatus{ByState: map[string]int{}, Note: err.Error()}
	}
	defer rows.Close()
	out := JobStatus{ByState: map[string]int{}}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			out.Note = err.Error()
			break
		}
		out.ByState[state] = n
		out.Total += n
	}
	if err := rows.Err(); err != nil && out.Note == "" {
		out.Note = err.Error()
	}
	return out
}

// writeTarball streams the files as a gzip-compressed tar.
func (h *Handler) writeTarball(w http.ResponseWriter, _ *http.Request, files map[string][]byte) {
	stamp := time.Now().UTC().Format("20060102-150405")
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="maktaba-diagnostics-%s.tar.gz"`, stamp))

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, name := range sortedKeys(files) {
		data := files[name]
		hdr := &tar.Header{
			Name:    name,
			Mode:    0o644,
			Size:    int64(len(data)),
			ModTime: time.Now().UTC(),
		}
		if tw.WriteHeader(hdr) != nil {
			break
		}
		if _, err := tw.Write(data); err != nil {
			break
		}
	}
	_ = tw.Close()
	_ = gz.Close()
}

// writeJSONBundle returns the same files as a single JSON object,
// embedding the JSONL log files as string arrays so the whole bundle is
// inspectable without un-taring (used by tests and the ?format=json
// debug path).
func (h *Handler) writeJSONBundle(w http.ResponseWriter, r *http.Request, files map[string][]byte) {
	out := map[string]json.RawMessage{}
	for name, data := range files {
		// .json files are embedded verbatim; .jsonl files are
		// newline-delimited (not a single JSON value), and the log files
		// and notes are text, so they are embedded as a JSON string to
		// keep the envelope valid.
		if !strings.HasSuffix(name, ".json") {
			out[name] = mustJSON(string(data))
		} else {
			out[name] = json.RawMessage(data)
		}
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func mustJSON(v any) []byte {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		data, _ = json.Marshal(map[string]string{"error": err.Error()})
	}
	return data
}
