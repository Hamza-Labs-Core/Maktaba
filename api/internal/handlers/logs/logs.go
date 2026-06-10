// Package logs implements the troubleshooting log-collection surface:
//
//	GET /api/admin/logs/stream   — admin: recent structured logs (JSON) for the live viewer
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
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
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
}

// Mount wires the routes. Admin routes are gated with RequireAdmin; the
// diagnostics route is available to any authenticated principal (the
// security chain populates it) and self-scopes its contents.
func (h *Handler) Mount(r chi.Router) {
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/stream", h.Stream)
	r.With(authmw.RequireAdmin).Get("/api/admin/logs/export", h.AdminExport)
	r.With(authmw.RequireAuth).Get("/api/diagnostics/export", h.UserExport)
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
	healthJSON := h.healthSnapshot(r)

	apiLogs := h.localLogs(baseFilter, scopeUserID)
	errSummary := errorSummary(h.scopedEntries(mlog.Filter{MinLevel: 0}, scopeUserID))

	// Peer logs are best-effort: a down service yields an empty file
	// with an inline note rather than failing the whole export.
	peerLogs := map[string][]byte{}
	for _, peer := range h.Peers {
		peerLogs[peer.Service] = h.fetchPeerLogs(ctx, peer, r.URL.RawQuery)
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
	// Admin export also includes active-job status; the user export
	// omits it (cross-user data).
	if scopeUserID == "" {
		files["jobs.json"] = mustJSON(h.activeJobs(ctx))
	}

	if r.URL.Query().Get("format") == "json" {
		h.writeJSONBundle(w, r, files)
		return
	}
	h.writeTarball(w, r, files)
}

// systemInfo assembles the host/version/resource snapshot.
func (h *Handler) systemInfo(ctx context.Context, scoped bool) SystemInfo {
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
// scopeUserID is set, restricted to lines belonging to that user (or
// lines with no user_id, which are un-attributed system events).
func (h *Handler) scopedEntries(f mlog.Filter, scopeUserID string) []mlog.Entry {
	if h.Ring == nil {
		return nil
	}
	entries := h.Ring.Entries(f)
	if scopeUserID == "" {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		var rec struct {
			UserID string `json:"user_id"`
		}
		_ = json.Unmarshal(e.Raw, &rec)
		if rec.UserID == "" || rec.UserID == scopeUserID {
			out = append(out, e)
		}
	}
	return out
}

// fetchPeerLogs proxies a downstream service's /logs/recent. On any
// error it returns a single JSONL note line so the bundle records that
// the service was unreachable rather than silently omitting it.
func (h *Handler) fetchPeerLogs(ctx context.Context, peer PeerLog, rawQuery string) []byte {
	url := peer.URL
	if rawQuery != "" {
		if bytes.ContainsRune([]byte(url), '?') {
			url += "&" + rawQuery
		} else {
			url += "?" + rawQuery
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		// newline-delimited (not a single JSON value) so they are
		// embedded as a JSON string to keep the envelope valid.
		if strings.HasSuffix(name, ".jsonl") {
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
