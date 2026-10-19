// Package settings implements Story 7.15:
//
//	GET  /api/settings
//	PATCH /api/settings
//	GET  /api/settings/stt-backends
//	POST /api/settings/stt-test
//
// Effective config is the merge of file/env defaults + the
// “app_settings“ table. Secret-bearing keys are redacted in the
// response with a sibling “*_present“ boolean so the UI knows the
// value exists without seeing it. Only keys in “runtimeKeys“ may be
// PATCHed at runtime; everything else is 403.
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// PipelineSettingsClient is the gRPC backend for “stt-backends“ and
// “stt-test“ (Pipeline Story 7.18).
type PipelineSettingsClient interface {
	ListBackends(ctx context.Context) ([]Backend, error)
	STTTest(ctx context.Context, backend string, config map[string]any) (STTTestResult, error)
}

// Backend is the AC-4 shape.
type Backend struct {
	Name             string   `json:"name"`
	Available        bool     `json:"available"`
	Version          string   `json:"version,omitempty"`
	Models           []string `json:"models,omitempty"`
	HWAccel          string   `json:"hwaccel,omitempty"`
	CostPerMinuteUSD *float64 `json:"cost_per_minute_usd,omitempty"`
}

// STTTestResult is the AC-5 shape.
type STTTestResult struct {
	OK         bool   `json:"ok"`
	LatencyMs  int64  `json:"latency_ms"`
	SampleText string `json:"sample_text,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Handler bundles deps.
type Handler struct {
	DB         *sql.DB
	Pipeline   PipelineSettingsClient
	FileEnvCfg map[string]any
	NowFunc    func() time.Time
	// Settings is the runtime settings reader the other handlers share; a
	// PATCH drops its cache.
	Settings *runtimecfg.Reader
}

func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/settings", h.Read)
	r.Patch("/api/settings", h.Patch)
	r.Get("/api/settings/stt-backends", h.STTBackends)
	r.Post("/api/settings/stt-test", h.STTTest)
}

// secretKeyPattern detects keys that should be redacted from the
// response. Matches anywhere in the dotted key path so
// “stt.openai.api_key“ is caught.
var secretKeyPattern = regexp.MustCompile(`(?i)(api_key|token|password|secret)`)

// runtimeKeys is the allowlist for PATCH, each with its value check. Trying to
// PATCH anything else returns 403 type=setting-not-runtime per AC-3. Every key
// is read by the handler it names (internal/runtimecfg); unset, that handler
// keeps its built-in default.
var runtimeKeys = map[string]func(json.RawMessage) bool{
	runtimecfg.SearchFTSWeight:      numberIn(0, 100),    // search: FTS list weight in the fusion (1)
	runtimecfg.SearchSemanticWeight: numberIn(0, 100),    // search: semantic list weight (1)
	runtimecfg.RecsCacheTTL:         numberIn(1, 86400),  // recommendations cache (60 s)
	runtimecfg.SessionURLTTL:        numberIn(60, 86400), // signed stream URLs (30 min; the signer caps 24 h)
	runtimecfg.PauseGrace:           numberIn(1, 3600),   // force pause after a requested pause waited this long
	runtimecfg.WSHeartbeat:          numberIn(5, 300),    // SSE keep-alive (30 s)
	runtimecfg.OpenRegistration:     isBool,              // self-service registration (off)
}

func numberIn(lo, hi float64) func(json.RawMessage) bool {
	return func(raw json.RawMessage) bool {
		var f float64
		if json.Unmarshal(raw, &f) != nil {
			return false
		}
		return f >= lo && f <= hi
	}
}

func isBool(raw json.RawMessage) bool {
	var b bool
	return json.Unmarshal(raw, &b) == nil
}

// Read returns the merged effective config with secrets redacted.
func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	merged := h.merged(r.Context())
	redacted := redactSecrets(merged)
	common.WriteJSON(w, r, http.StatusOK, redacted)
}

// Patch applies updates to “app_settings“. AC-3: non-runtime keys
// return 403.
func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return
	}
	var req map[string]json.RawMessage
	if e := common.ReadJSON(r, &req, 32<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	for k, v := range req {
		valid, ok := runtimeKeys[k]
		if ok && !valid(v) {
			httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: k, Message: "out of range"}}))
			return
		}
		if !ok {
			httperror.Write(w, r, &httperror.Error{
				Type:   "https://maktaba.dev/problems/setting-not-runtime",
				Title:  "not a runtime setting",
				Status: http.StatusForbidden,
				Detail: k + " is read-only at runtime",
			})
			return
		}
	}
	for k, v := range req {
		_, err := h.DB.ExecContext(r.Context(), `
			INSERT INTO app_settings (key, value, updated_at, updated_by)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at, updated_by = EXCLUDED.updated_by
		`, k, string(v), h.now(), p.UserID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("patch setting: "+err.Error()))
			return
		}
	}
	h.Settings.Invalidate() // this API uses the new values at once
	merged := h.merged(r.Context())
	common.WriteJSON(w, r, http.StatusOK, redactSecrets(merged))
}

// STTBackends implements AC-4.
func (h *Handler) STTBackends(w http.ResponseWriter, r *http.Request) {
	if h.Pipeline == nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": []Backend{}})
		return
	}
	b, err := h.Pipeline.ListBackends(r.Context())
	if err != nil {
		httperror.Write(w, r, httperror.Internal("backends: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"items": b})
}

// STTTestRequest is the AC-5 body.
type STTTestRequest struct {
	Backend string         `json:"backend"`
	Config  map[string]any `json:"config,omitempty"`
}

// STTTest implements AC-5.
func (h *Handler) STTTest(w http.ResponseWriter, r *http.Request) {
	if h.Pipeline == nil {
		httperror.Write(w, r, &httperror.Error{
			Type:   "https://maktaba.dev/problems/unavailable",
			Title:  "pipeline unavailable",
			Status: http.StatusServiceUnavailable,
		})
		return
	}
	var req STTTestRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	res, err := h.Pipeline.STTTest(r.Context(), req.Backend, req.Config)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("stt-test: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, res)
}

// merged returns the file/env defaults overlaid with the DB rows.
func (h *Handler) merged(ctx context.Context) map[string]any {
	out := map[string]any{}
	for k, v := range h.FileEnvCfg {
		out[k] = v
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT key, value FROM app_settings`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k string
			var v []byte
			if err := rows.Scan(&k, &v); err != nil {
				continue
			}
			var decoded any
			if err := json.Unmarshal(v, &decoded); err != nil {
				out[k] = string(v)
				continue
			}
			out[k] = decoded
		}
	}
	return out
}

// redactSecrets replaces values for keys that match secretKeyPattern
// with “"<redacted>"“ and adds a sibling “*_present: true“.
func redactSecrets(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		if secretKeyPattern.MatchString(k) {
			out[k] = "<redacted>"
			out[k+"_present"] = !isEmpty(v)
		} else {
			out[k] = v
		}
	}
	return out
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	}
	return false
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc()
	}
	return time.Now().UTC()
}
