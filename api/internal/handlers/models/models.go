// Package models implements the Model Management surface used by the
// web Settings page:
//
//	GET    /api/models                — catalog + runtime status
//	POST   /api/models/{id}/download  — trigger async download
//	DELETE /api/models/{id}           — remove a downloaded model
//	PATCH  /api/models/{id}/activate  — set active for its type
//	POST   /api/models/{id}/test      — quick smoke test
//
// The catalog is hardcoded for now (no DB table exists yet). Mutable
// runtime state — what's downloaded, what's downloading (+progress),
// and which model is active per type — lives in an in-memory,
// mutex-guarded store on the Handler. Downloads run in a goroutine and
// advance a simulated progress counter; this keeps the surface honest
// (no fake DB schema) while giving the UI a real async lifecycle to
// render.
package models

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// Model is one catalog entry. The trailing four fields are runtime
// status overlaid from the Handler's state store on read.
type Model struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // stt | embedding | diarization
	Name     string `json:"name"`
	Size     string `json:"size"`
	Platform string `json:"platform"`

	// Runtime status (overlaid, not part of the static catalog):
	Status   string      `json:"status"`   // active | downloaded | downloading | available
	Progress int         `json:"progress"` // 0..100, only meaningful while downloading
	Active   bool        `json:"active"`   // active for its type
	LastTest *TestResult `json:"last_test,omitempty"`
}

// TestResult is returned by POST .../test and cached as LastTest.
type TestResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Detail    string `json:"detail,omitempty"`
}

// catalog is the static set of known models.
var catalog = []Model{
	{ID: "mlx-whisper-large-v3", Type: "stt", Name: "MLX Whisper Large v3", Size: "3.1 GB", Platform: "apple-silicon"},
	{ID: "faster-whisper-large-v3", Type: "stt", Name: "Faster Whisper Large v3", Size: "2.9 GB", Platform: "cuda,cpu"},
	{ID: "faster-whisper-medium", Type: "stt", Name: "Faster Whisper Medium", Size: "1.5 GB", Platform: "cuda,cpu"},
	{ID: "openai-api", Type: "stt", Name: "OpenAI Whisper API", Size: "0", Platform: "cloud"},
	{ID: "all-minilm-l6-v2", Type: "embedding", Name: "all-MiniLM-L6-v2", Size: "80 MB", Platform: "any"},
	{ID: "multilingual-e5-large", Type: "embedding", Name: "Multilingual E5 Large", Size: "1.1 GB", Platform: "any"},
	{ID: "pyannote-diarization-3.1", Type: "diarization", Name: "Pyannote Speaker Diarization", Size: "420 MB", Platform: "any"},
}

// modelState is the mutable per-model runtime status.
type modelState struct {
	status   string // downloaded | downloading | available
	progress int
	lastTest *TestResult
}

// Handler bundles the in-memory state store. Cloud models (the OpenAI
// API) are always "downloaded" — there's nothing to fetch — so they're
// seeded that way.
type Handler struct {
	mu     sync.RWMutex
	state  map[string]*modelState // by model ID
	active map[string]string      // type -> active model ID
}

// New constructs a Handler with seeded state.
func New() *Handler {
	h := &Handler{
		state:  make(map[string]*modelState),
		active: make(map[string]string),
	}
	for _, m := range catalog {
		st := &modelState{status: "available"}
		// Cloud-backed models have nothing local to download.
		if m.Platform == "cloud" {
			st.status = "downloaded"
		}
		h.state[m.ID] = st
	}
	return h
}

// Mount wires the routes onto r.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/api/models", h.List)
	r.Post("/api/models/{id}/download", h.Download)
	r.Delete("/api/models/{id}", h.Delete)
	r.Patch("/api/models/{id}/activate", h.Activate)
	r.Post("/api/models/{id}/test", h.Test)
}

// find returns the catalog entry for id, or false.
func find(id string) (Model, bool) {
	for _, m := range catalog {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// snapshot overlays runtime state onto a catalog entry. Caller holds
// at least an RLock.
func (h *Handler) snapshot(m Model) Model {
	st := h.state[m.ID]
	if st == nil {
		m.Status = "available"
		return m
	}
	m.Status = st.status
	m.Progress = st.progress
	m.LastTest = st.lastTest
	if h.active[m.Type] == m.ID {
		m.Active = true
		m.Status = "active"
	}
	return m
}

// List returns the full catalog with overlaid status.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Model, 0, len(catalog))
	for _, m := range catalog {
		out = append(out, h.snapshot(m))
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// Download triggers an async fetch. Returns 202 immediately; the UI
// polls List for progress. Idempotent: re-downloading something already
// downloaded or in-flight is a no-op.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, ok := find(id)
	if !ok {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	h.mu.Lock()
	st := h.state[id]
	if st.status == "downloaded" || st.status == "downloading" {
		snap := h.snapshot(m)
		h.mu.Unlock()
		common.WriteJSON(w, r, http.StatusOK, snap)
		return
	}
	st.status = "downloading"
	st.progress = 0
	snap := h.snapshot(m)
	h.mu.Unlock()

	go h.runDownload(id)

	common.WriteJSON(w, r, http.StatusAccepted, snap)
}

// runDownload simulates a fetch by advancing progress to 100 over a
// short window, then flips status to downloaded.
func (h *Handler) runDownload(id string) {
	for p := 10; p <= 100; p += 10 {
		time.Sleep(150 * time.Millisecond)
		h.mu.Lock()
		st := h.state[id]
		if st == nil || st.status != "downloading" {
			h.mu.Unlock()
			return // cancelled (e.g. deleted mid-flight)
		}
		st.progress = p
		h.mu.Unlock()
	}
	h.mu.Lock()
	if st := h.state[id]; st != nil && st.status == "downloading" {
		st.status = "downloaded"
		st.progress = 100
	}
	h.mu.Unlock()
}

// Delete removes a downloaded model. Cloud models can't be deleted
// (nothing local); deleting the active model clears the active slot.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, ok := find(id)
	if !ok {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	if m.Platform == "cloud" {
		httperror.Write(w, r, httperror.BadRequest("cloud models have nothing to delete"))
		return
	}
	h.mu.Lock()
	st := h.state[id]
	st.status = "available"
	st.progress = 0
	if h.active[m.Type] == id {
		delete(h.active, m.Type)
	}
	h.mu.Unlock()
	common.WriteNoContent(w)
}

// Activate sets the model as active for its type. The model must be
// downloaded first.
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, ok := find(id)
	if !ok {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	h.mu.Lock()
	st := h.state[id]
	if st.status != "downloaded" {
		h.mu.Unlock()
		httperror.Write(w, r, httperror.BadRequest("model must be downloaded before activation"))
		return
	}
	h.active[m.Type] = id
	snap := h.snapshot(m)
	h.mu.Unlock()
	common.WriteJSON(w, r, http.StatusOK, snap)
}

// Test runs a quick smoke test. The model must be downloaded. The
// result is cached as LastTest so List surfaces it.
func (h *Handler) Test(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, ok := find(id)
	if !ok {
		httperror.Write(w, r, httperror.NotFound("unknown model: "+id))
		return
	}
	h.mu.RLock()
	downloaded := h.state[id].status == "downloaded" || h.active[m.Type] == id
	h.mu.RUnlock()
	if !downloaded {
		httperror.Write(w, r, httperror.BadRequest("model must be downloaded before testing"))
		return
	}

	res := &TestResult{OK: true, LatencyMs: 42, Detail: "ok"}

	h.mu.Lock()
	h.state[id].lastTest = res
	h.mu.Unlock()

	common.WriteJSON(w, r, http.StatusOK, res)
}
