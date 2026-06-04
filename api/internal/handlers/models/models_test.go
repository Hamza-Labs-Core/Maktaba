package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// newRouter wires a fresh Handler onto a chi router for testing.
func newRouter() (*Handler, *chi.Mux) {
	h := New()
	r := chi.NewRouter()
	h.Mount(r)
	return h, r
}

func do(t *testing.T, r *chi.Mux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestListReturnsCatalog(t *testing.T) {
	_, r := newRouter()
	w := do(t, r, http.MethodGet, "/api/models")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var got []Model
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(catalog) {
		t.Fatalf("got %d models, want %d", len(got), len(catalog))
	}
	// Cloud model is seeded downloaded.
	for _, m := range got {
		if m.ID == "openai-api" && m.Status != "downloaded" {
			t.Errorf("openai-api status = %q, want downloaded", m.Status)
		}
	}
}

func TestDownloadLifecycle(t *testing.T) {
	h, r := newRouter()
	const id = "all-minilm-l6-v2"

	w := do(t, r, http.MethodPost, "/api/models/"+id+"/download")
	if w.Code != http.StatusAccepted {
		t.Fatalf("download status = %d, want 202", w.Code)
	}

	// Wait for the simulated download to complete.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.RLock()
		done := h.state[id].status == "downloaded"
		h.mu.RUnlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.mu.RLock()
	st := h.state[id].status
	h.mu.RUnlock()
	if st != "downloaded" {
		t.Fatalf("after wait status = %q, want downloaded", st)
	}
}

func TestActivateRequiresDownload(t *testing.T) {
	_, r := newRouter()
	const id = "multilingual-e5-large"

	// Not downloaded yet → 400.
	if w := do(t, r, http.MethodPatch, "/api/models/"+id+"/activate"); w.Code != http.StatusBadRequest {
		t.Fatalf("activate-before-download status = %d, want 400", w.Code)
	}
}

func TestActivateAndTestAndDelete(t *testing.T) {
	h, r := newRouter()
	const id = "pyannote-diarization-3.1"

	// Force downloaded state directly (skip the timed goroutine).
	h.mu.Lock()
	h.state[id].status = "downloaded"
	h.mu.Unlock()

	if w := do(t, r, http.MethodPatch, "/api/models/"+id+"/activate"); w.Code != http.StatusOK {
		t.Fatalf("activate status = %d", w.Code)
	}
	h.mu.RLock()
	active := h.active["diarization"]
	h.mu.RUnlock()
	if active != id {
		t.Fatalf("active diarization = %q, want %q", active, id)
	}

	if w := do(t, r, http.MethodPost, "/api/models/"+id+"/test"); w.Code != http.StatusOK {
		t.Fatalf("test status = %d", w.Code)
	}

	if w := do(t, r, http.MethodDelete, "/api/models/"+id); w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", w.Code)
	}
	h.mu.RLock()
	st := h.state[id].status
	_, stillActive := h.active["diarization"]
	h.mu.RUnlock()
	if st != "available" {
		t.Errorf("after delete status = %q, want available", st)
	}
	if stillActive {
		t.Errorf("active slot not cleared after deleting active model")
	}
}

func TestUnknownModelIs404(t *testing.T) {
	_, r := newRouter()
	if w := do(t, r, http.MethodPost, "/api/models/nope/download"); w.Code != http.StatusNotFound {
		t.Fatalf("unknown download status = %d, want 404", w.Code)
	}
}

func TestCloudModelCannotBeDeleted(t *testing.T) {
	_, r := newRouter()
	if w := do(t, r, http.MethodDelete, "/api/models/openai-api"); w.Code != http.StatusBadRequest {
		t.Fatalf("delete cloud status = %d, want 400", w.Code)
	}
}
