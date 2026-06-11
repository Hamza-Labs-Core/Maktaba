package logs

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

// seedRing builds a ring buffer with a few representative lines.
func seedRing(t *testing.T) *mlog.RingBuffer {
	t.Helper()
	rb := mlog.NewRingBuffer(100)
	lg := mlog.NewLoggerWithRing(mlog.Options{Service: "api", Env: "prod", Version: "v0"}, rb)
	lg.Info("startup")
	lg.With("user_id", "u-alice").Info("alice did a thing")
	lg.With("user_id", "u-bob").Error("bob hit an error")
	lg.With("user_id", "u-bob").Error("bob hit an error") // dup → count 2
	lg.Error("system level error")
	return rb
}

func adminCtx(r *http.Request) *http.Request {
	return r.WithContext(principal.WithPrincipal(r.Context(),
		&principal.Principal{UserID: "admin", IsAdmin: true}))
}

func TestStreamReturnsEntries(t *testing.T) {
	h := &Handler{Ring: seedRing(t)}
	r := chi.NewRouter()
	h.Mount(r)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/admin/logs/stream", nil))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp streamResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 5 || len(resp.Entries) != 5 {
		t.Fatalf("want 5 entries, got %d", resp.Count)
	}
}

func TestStreamRejectsNonAdmin(t *testing.T) {
	h := &Handler{Ring: seedRing(t)}
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/logs/stream", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u1", IsAdmin: false}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: want 403, got %d", rec.Code)
	}
}

func TestAdminExportTarball(t *testing.T) {
	h := &Handler{Ring: seedRing(t), StartTime: time.Now().Add(-time.Minute)}
	r := chi.NewRouter()
	h.Mount(r)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/admin/logs/export", nil))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("content-type = %q", ct)
	}
	names := tarballEntries(t, rec.Body.Bytes())
	for _, want := range []string{"system-info.json", "api-logs.jsonl", "error-summary.json", "health-check.json", "jobs.json"} {
		if _, ok := names[want]; !ok {
			t.Errorf("bundle missing %q (have %v)", want, keys(names))
		}
	}
	// system-info.json must be valid JSON with the OS field.
	var sys SystemInfo
	if err := json.Unmarshal(names["system-info.json"], &sys); err != nil {
		t.Fatalf("system-info.json: %v", err)
	}
	if sys.OS == "" || sys.GoVersion == "" {
		t.Errorf("system-info incomplete: %+v", sys)
	}
}

func TestErrorSummaryDeduplicates(t *testing.T) {
	h := &Handler{Ring: seedRing(t)}
	r := chi.NewRouter()
	h.Mount(r)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/admin/logs/export?format=json", nil))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var bundle map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	var summary ErrorSummary
	if err := json.Unmarshal(bundle["error-summary.json"], &summary); err != nil {
		t.Fatalf("error-summary: %v", err)
	}
	// 3 error lines total (bob×2 + system×1), 2 unique messages.
	if summary.TotalErrors != 3 || summary.UniqueErrors != 2 {
		t.Fatalf("want total=3 unique=2, got total=%d unique=%d", summary.TotalErrors, summary.UniqueErrors)
	}
	if summary.Errors[0].Count != 2 {
		t.Errorf("expected the duplicated error first with count 2, got %+v", summary.Errors[0])
	}
}

func TestUserExportScopesToOwnLogs(t *testing.T) {
	h := &Handler{Ring: seedRing(t)}
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/api/diagnostics/export?format=json", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u-alice", IsAdmin: false}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var bundle map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &bundle)

	// api-logs.jsonl is embedded as a JSON string in the json bundle.
	var apiLogs string
	if err := json.Unmarshal(bundle["api-logs.jsonl"], &apiLogs); err != nil {
		t.Fatalf("api-logs: %v", err)
	}
	if strings.Contains(apiLogs, "u-bob") {
		t.Errorf("alice's scoped export leaked bob's lines:\n%s", apiLogs)
	}
	if !strings.Contains(apiLogs, "u-alice") {
		t.Errorf("alice's own line missing from scoped export:\n%s", apiLogs)
	}
	// Un-attributed system lines (no user_id) should still be present.
	if !strings.Contains(apiLogs, "startup") {
		t.Errorf("system line missing from scoped export:\n%s", apiLogs)
	}
	// Cross-user job status must be omitted from the user export.
	if _, ok := bundle["jobs.json"]; ok {
		t.Errorf("user export must not include jobs.json")
	}
}

// TestUserExportScopesPeerLogs guards the cross-service leak: the
// proxied streaming/pipeline logs must also be filtered to the
// requesting user, since the peer endpoints cannot pre-filter by user.
func TestUserExportScopesPeerLogs(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w,
			`{"ts":"2026-06-10T00:00:00Z","level":"info","service":"streaming","msg":"alice stream","user_id":"u-alice"}`+"\n"+
				`{"ts":"2026-06-10T00:00:01Z","level":"error","service":"streaming","msg":"bob stream","user_id":"u-bob"}`+"\n"+
				`{"ts":"2026-06-10T00:00:02Z","level":"info","service":"streaming","msg":"system tick"}`+"\n")
	}))
	defer peer.Close()

	h := &Handler{Ring: seedRing(t), Peers: []PeerLog{{Service: "streaming", URL: peer.URL}}}
	r := chi.NewRouter()
	h.Mount(r)

	req := httptest.NewRequest(http.MethodGet, "/api/diagnostics/export?format=json", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(),
		&principal.Principal{UserID: "u-alice", IsAdmin: false}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var bundle map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &bundle)
	var peerLogs string
	if err := json.Unmarshal(bundle["streaming-logs.jsonl"], &peerLogs); err != nil {
		t.Fatalf("streaming-logs: %v", err)
	}
	if strings.Contains(peerLogs, "u-bob") || strings.Contains(peerLogs, "bob stream") {
		t.Errorf("alice's scoped export leaked bob's peer lines:\n%s", peerLogs)
	}
	if !strings.Contains(peerLogs, "alice stream") {
		t.Errorf("alice's own peer line missing:\n%s", peerLogs)
	}
	// Un-attributed peer system lines stay (consistent with local scope).
	if !strings.Contains(peerLogs, "system tick") {
		t.Errorf("un-attributed peer system line missing:\n%s", peerLogs)
	}
}

// --- helpers ---

func tarballEntries(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		data, _ := io.ReadAll(tr)
		out[hdr.Name] = data
	}
	return out
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
