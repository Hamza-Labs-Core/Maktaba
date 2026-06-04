// Package models implements the Model Management surface used by the
// web Settings page:
//
//	GET    /api/models                — catalog + runtime status
//	POST   /api/models/{id}/download  — trigger async download
//	DELETE /api/models/{id}           — remove a downloaded model
//	PATCH  /api/models/{id}/activate  — set active for its type
//	POST   /api/models/{id}/test      — quick smoke test
//
// The real model lifecycle lives in the Python pipeline (where the
// Whisper / embedding / diarization models actually run); this handler
// is a thin proxy over the pipeline's gRPC model service. List overlays
// the pipeline's live status (installed / active / in-flight progress);
// download/delete/activate/test forward to the pipeline.
//
// When the pipeline is unavailable (not configured, or not running) the
// handler degrades gracefully: List returns a static fallback catalog so
// the UI still renders the known models as "available", and the mutating
// operations return 503 "pipeline offline".
package models

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Pipeline is the subset of the pipeline gRPC client this handler needs.
// Defined locally so tests can inject a hand-rolled fake without a gRPC
// stack.
type Pipeline interface {
	ListModels(ctx context.Context) ([]grpcpipeline.ModelInfo, error)
	DownloadModel(ctx context.Context, id string) (jobID string, err error)
	DeleteModel(ctx context.Context, id string) (deleted bool, err error)
	ActivateModel(ctx context.Context, id, modelType string) (grpcpipeline.ModelActivation, error)
	TestModel(ctx context.Context, id string) (grpcpipeline.ModelTestResult, error)
}

// modelView is the JSON shape returned by GET /api/models. The field set
// matches the web Settings page's Model interface; extra fields
// (size_bytes, installed, gated) are additive and ignored by older
// clients.
type modelView struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"` // stt | embedding | diarization
	Name      string      `json:"name"`
	Size      string      `json:"size"`
	SizeBytes int64       `json:"size_bytes,omitempty"`
	Platform  string      `json:"platform"`
	Gated     bool        `json:"gated,omitempty"`
	Installed bool        `json:"installed"`
	Active    bool        `json:"active"`
	Status    string      `json:"status"`   // active | downloaded | downloading | available
	Progress  int         `json:"progress"` // 0..100
	LastTest  *TestResult `json:"last_test,omitempty"`
}

// TestResult is returned by POST .../test.
type TestResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
}

// fallbackCatalog is the static set of known models, mirroring the
// pipeline registry. Used only when the pipeline is unreachable, so the
// Settings page still renders the catalog (all "available") instead of
// an empty list.
var fallbackCatalog = []modelView{
	{ID: "mlx-whisper-large-v3", Type: "stt", Name: "MLX Whisper Large v3", Size: "3.0 GB", Platform: "apple-silicon", Status: "available"},
	{ID: "faster-whisper-large-v3", Type: "stt", Name: "Faster Whisper Large v3", Size: "2.9 GB", Platform: "cuda,cpu", Status: "available"},
	{ID: "all-minilm-l6-v2", Type: "embedding", Name: "all-MiniLM-L6-v2", Size: "90.0 MB", Platform: "any", Status: "available"},
	{ID: "multilingual-e5-large", Type: "embedding", Name: "Multilingual E5 Large", Size: "2.1 GB", Platform: "any", Status: "available"},
	{ID: "pyannote-diarization-3.1", Type: "diarization", Name: "Pyannote Speaker Diarization 3.1", Size: "26.0 MB", Platform: "any", Gated: true, Status: "available"},
}

// knownIDs is the set of catalog ids, used to 404 unknown models before
// reaching the pipeline.
var knownIDs = func() map[string]struct{} {
	m := make(map[string]struct{}, len(fallbackCatalog))
	for _, e := range fallbackCatalog {
		m[e.ID] = struct{}{}
	}
	return m
}()

// Handler proxies model management to the pipeline. A nil pipeline means
// "offline": List degrades to the fallback catalog, mutations 503.
type Handler struct {
	pipeline Pipeline
}

// New constructs a Handler. Pass nil (or a nil-valued client) to run in
// the degraded, pipeline-offline mode.
func New(p Pipeline) *Handler {
	return &Handler{pipeline: p}
}

// Mount wires the routes onto r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/models", h.List)
	r.Post("/api/models/{id}/download", h.Download)
	r.Delete("/api/models/{id}", h.Delete)
	r.Patch("/api/models/{id}/activate", h.Activate)
	r.Post("/api/models/{id}/test", h.Test)
}

// offline reports whether no usable pipeline client is wired.
func (h *Handler) offline() bool {
	return h.pipeline == nil
}

// pipelineOffline is the 503 returned by mutating ops when the pipeline
// isn't reachable.
func pipelineOffline() *httperror.Error {
	e := httperror.Unavailable(5)
	e.Detail = "pipeline offline"
	return e
}

func known(id string) bool {
	_, ok := knownIDs[id]
	return ok
}

// List returns the catalog with the pipeline's live status overlaid.
// When the pipeline is unreachable it falls back to the static catalog
// so the UI still renders (degraded, not failed).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.offline() {
		common.WriteJSON(w, r, http.StatusOK, fallbackCatalog)
		return
	}
	infos, err := h.pipeline.ListModels(r.Context())
	if err != nil {
		// Pipeline configured but unreachable/erroring — degrade to the
		// static catalog rather than 5xx the Settings page.
		common.WriteJSON(w, r, http.StatusOK, fallbackCatalog)
		return
	}
	out := make([]modelView, 0, len(infos))
	for _, m := range infos {
		out = append(out, modelView{
			ID:        m.ID,
			Type:      m.Type,
			Name:      m.Name,
			Size:      m.Size,
			SizeBytes: m.SizeBytes,
			Platform:  m.Platform,
			Gated:     m.Gated,
			Installed: m.Installed,
			Active:    m.Active,
			Status:    m.Status,
			Progress:  m.Progress,
		})
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Download forwards to the pipeline and returns 202 + {job_id}.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !known(id) {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	if h.offline() {
		httperror.Write(w, r, pipelineOffline())
		return
	}
	jobID, err := h.pipeline.DownloadModel(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("pipeline download failed: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusAccepted, map[string]string{"job_id": jobID})
}

// Delete forwards to the pipeline. A model the pipeline didn't have is a
// 404; success is 204.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !known(id) {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	if h.offline() {
		httperror.Write(w, r, pipelineOffline())
		return
	}
	deleted, err := h.pipeline.DeleteModel(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("pipeline delete failed: "+err.Error()))
		return
	}
	if !deleted {
		httperror.Write(w, r, httperror.NotFound("model not installed: "+id))
		return
	}
	common.WriteNoContent(w)
}

// Activate forwards to the pipeline; the pipeline enforces "must be
// installed first" and infers the type from its catalog.
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !known(id) {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	if h.offline() {
		httperror.Write(w, r, pipelineOffline())
		return
	}
	act, err := h.pipeline.ActivateModel(r.Context(), id, "")
	if err != nil {
		// The pipeline rejects activating a not-yet-installed model.
		httperror.Write(w, r, httperror.BadRequest("pipeline activate failed: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, act)
}

// Test forwards to the pipeline and returns the smoke-test result.
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !known(id) {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	if h.offline() {
		httperror.Write(w, r, pipelineOffline())
		return
	}
	res, err := h.pipeline.TestModel(r.Context(), id)
	if err != nil {
		httperror.Write(w, r, httperror.BadGateway("pipeline test failed: "+err.Error()))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, TestResult{
		OK:        res.OK,
		LatencyMs: res.LatencyMs,
		Detail:    res.Detail,
		Error:     res.Error,
	})
}
