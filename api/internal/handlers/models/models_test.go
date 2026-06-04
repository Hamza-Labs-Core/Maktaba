package models

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	grpcpipeline "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
)

// fakePipeline is a hand-rolled stand-in for the model-management subset
// of the pipeline gRPC client. Each method returns its canned value /
// error and records the id it was called with.
type fakePipeline struct {
	models  []grpcpipeline.ModelInfo
	listErr error

	jobID        string
	downloadErr  error
	downloadedID string

	deleted   bool
	deleteErr error
	deletedID string

	activation  grpcpipeline.ModelActivation
	activateErr error
	activatedID string

	testRes  grpcpipeline.ModelTestResult
	testErr  error
	testedID string
}

func (f *fakePipeline) ListModels(_ context.Context) ([]grpcpipeline.ModelInfo, error) {
	return f.models, f.listErr
}

func (f *fakePipeline) DownloadModel(_ context.Context, id string) (string, error) {
	f.downloadedID = id
	return f.jobID, f.downloadErr
}

func (f *fakePipeline) DeleteModel(_ context.Context, id string) (bool, error) {
	f.deletedID = id
	return f.deleted, f.deleteErr
}

func (f *fakePipeline) ActivateModel(_ context.Context, id, _ string) (grpcpipeline.ModelActivation, error) {
	f.activatedID = id
	return f.activation, f.activateErr
}

func (f *fakePipeline) TestModel(_ context.Context, id string) (grpcpipeline.ModelTestResult, error) {
	f.testedID = id
	return f.testRes, f.testErr
}

func newRouter(p Pipeline) *chi.Mux {
	h := New(p)
	r := chi.NewRouter()
	h.Mount(r)
	return r
}

func do(t *testing.T, r *chi.Mux, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestListUsesPipeline(t *testing.T) {
	p := &fakePipeline{models: []grpcpipeline.ModelInfo{
		{ID: "all-minilm-l6-v2", Type: "embedding", Name: "all-MiniLM-L6-v2", Size: "90.0 MB", Platform: "any", Installed: true, Active: true, Status: "active", Progress: 100},
		{ID: "mlx-whisper-large-v3", Type: "stt", Name: "MLX Whisper Large v3", Size: "3.0 GB", Platform: "apple-silicon", Status: "available"},
	}}
	w := do(t, newRouter(p), http.MethodGet, "/api/models")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var got []modelView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2", len(got))
	}
	if got[0].ID != "all-minilm-l6-v2" || got[0].Status != "active" || !got[0].Active {
		t.Fatalf("first model mismatch: %+v", got[0])
	}
}

func TestListFallsBackToCatalogWhenNilPipeline(t *testing.T) {
	w := do(t, newRouter(nil), http.MethodGet, "/api/models")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var got []modelView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(fallbackCatalog) {
		t.Fatalf("got %d models, want %d (fallback catalog)", len(got), len(fallbackCatalog))
	}
	for _, m := range got {
		if m.Status != "available" {
			t.Errorf("offline model %s status = %q, want available", m.ID, m.Status)
		}
	}
}

func TestListFallsBackOnPipelineError(t *testing.T) {
	p := &fakePipeline{listErr: errors.New("connection refused")}
	w := do(t, newRouter(p), http.MethodGet, "/api/models")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (degraded)", w.Code)
	}
	var got []modelView
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != len(fallbackCatalog) {
		t.Fatalf("got %d models, want fallback %d", len(got), len(fallbackCatalog))
	}
}

func TestDownloadCallsPipeline(t *testing.T) {
	p := &fakePipeline{jobID: "job-xyz"}
	w := do(t, newRouter(p), http.MethodPost, "/api/models/all-minilm-l6-v2/download")
	if w.Code != http.StatusAccepted {
		t.Fatalf("download status = %d, want 202", w.Code)
	}
	if p.downloadedID != "all-minilm-l6-v2" {
		t.Fatalf("pipeline DownloadModel called with %q", p.downloadedID)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["job_id"] != "job-xyz" {
		t.Fatalf("job_id = %v, want job-xyz", body["job_id"])
	}
}

func TestDownloadOfflineReturns503(t *testing.T) {
	w := do(t, newRouter(nil), http.MethodPost, "/api/models/all-minilm-l6-v2/download")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline download status = %d, want 503", w.Code)
	}
}

func TestDownloadUnknownModelIs404(t *testing.T) {
	p := &fakePipeline{jobID: "j"}
	w := do(t, newRouter(p), http.MethodPost, "/api/models/nope/download")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown download status = %d, want 404", w.Code)
	}
	if p.downloadedID != "" {
		t.Fatalf("pipeline should not be called for unknown model")
	}
}

func TestDownloadPipelineErrorIs502(t *testing.T) {
	p := &fakePipeline{downloadErr: errors.New("pipeline: boom")}
	w := do(t, newRouter(p), http.MethodPost, "/api/models/all-minilm-l6-v2/download")
	if w.Code != http.StatusBadGateway {
		t.Fatalf("download error status = %d, want 502", w.Code)
	}
}

func TestDeleteCallsPipeline(t *testing.T) {
	p := &fakePipeline{deleted: true}
	w := do(t, newRouter(p), http.MethodDelete, "/api/models/all-minilm-l6-v2")
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", w.Code)
	}
	if p.deletedID != "all-minilm-l6-v2" {
		t.Fatalf("pipeline DeleteModel called with %q", p.deletedID)
	}
}

func TestDeleteNotPresentIs404(t *testing.T) {
	p := &fakePipeline{deleted: false} // pipeline says it wasn't installed
	w := do(t, newRouter(p), http.MethodDelete, "/api/models/all-minilm-l6-v2")
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete-absent status = %d, want 404", w.Code)
	}
}

func TestDeleteOfflineReturns503(t *testing.T) {
	w := do(t, newRouter(nil), http.MethodDelete, "/api/models/all-minilm-l6-v2")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline delete status = %d, want 503", w.Code)
	}
}

func TestActivateCallsPipeline(t *testing.T) {
	p := &fakePipeline{activation: grpcpipeline.ModelActivation{ID: "pyannote-diarization-3.1", Type: "diarization", Active: true}}
	w := do(t, newRouter(p), http.MethodPatch, "/api/models/pyannote-diarization-3.1/activate")
	if w.Code != http.StatusOK {
		t.Fatalf("activate status = %d", w.Code)
	}
	if p.activatedID != "pyannote-diarization-3.1" {
		t.Fatalf("pipeline ActivateModel called with %q", p.activatedID)
	}
}

func TestActivateOfflineReturns503(t *testing.T) {
	w := do(t, newRouter(nil), http.MethodPatch, "/api/models/all-minilm-l6-v2/activate")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline activate status = %d, want 503", w.Code)
	}
}

func TestTestCallsPipeline(t *testing.T) {
	p := &fakePipeline{testRes: grpcpipeline.ModelTestResult{OK: true, LatencyMs: 42, Detail: "ok"}}
	w := do(t, newRouter(p), http.MethodPost, "/api/models/all-minilm-l6-v2/test")
	if w.Code != http.StatusOK {
		t.Fatalf("test status = %d", w.Code)
	}
	if p.testedID != "all-minilm-l6-v2" {
		t.Fatalf("pipeline TestModel called with %q", p.testedID)
	}
	var res TestResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !res.OK || res.LatencyMs != 42 {
		t.Fatalf("test result mismatch: %+v", res)
	}
}

func TestTestOfflineReturns503(t *testing.T) {
	w := do(t, newRouter(nil), http.MethodPost, "/api/models/all-minilm-l6-v2/test")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline test status = %d, want 503", w.Code)
	}
}
