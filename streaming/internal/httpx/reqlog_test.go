package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"

	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

func TestLogFailures(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := LogFailures(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stream/s1/v0/index.m3u8":
			Write(w, http.StatusGone, "transcode-stopped", "gone", "")
		case "/stream/s1/v0/seg9.ts":
			Write(w, http.StatusNotFound, "segment-not-found", "not yet", "")
		case "/stream/s1/boom":
			w.WriteHeader(http.StatusInternalServerError)
		case "/internal/tv/staging":
			http.NotFound(w, r)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	do := func(path string) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	do("/stream/s1/seg1.ts?sig=SECRET")
	do("/internal/tv/staging")
	if buf.Len() != 0 {
		t.Fatalf("a 200 is not logged: %s", buf.String())
	}

	do("/stream/s1/v0/index.m3u8?sig=SECRET")
	if strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("the signature leaked: %s", buf.String())
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{
		"event": "stream_request_failed", "level": "WARN", "method": "GET",
		"path": "/stream/s1/v0/index.m3u8", "status": float64(410), "problem": "transcode-stopped",
	} {
		if line[k] != v {
			t.Errorf("%s = %v, want %v", k, line[k], v)
		}
	}

	buf.Reset()
	do("/stream/s1/v0/seg9.ts")
	if !strings.Contains(buf.String(), `"level":"INFO"`) || !strings.Contains(buf.String(), "segment-not-found") {
		t.Fatalf("a segment not written yet is Info: %s", buf.String())
	}

	buf.Reset()
	do("/stream/s1/boom")
	if !strings.Contains(buf.String(), `"level":"ERROR"`) || !strings.Contains(buf.String(), `"status":500`) {
		t.Fatalf("a 500 is an error: %s", buf.String())
	}
}

// Epic 53 story 53.08: a failed playlist fetch logs the app's request id, from X-Request-Id or
// (a player that can't add headers) the rid parameter; the handlers' lines and the router's
// own id see the same one. Anything that isn't a UUID is not taken.
func TestLogFailuresCarriesTheRequestID(t *testing.T) {
	const rid = "01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60"
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	var inHeader string
	var onCtx bool
	var handlerBuf bytes.Buffer
	prev := mlog.Default
	mlog.Default = slog.New(slog.NewJSONHandler(&handlerBuf, nil))
	t.Cleanup(func() { mlog.Default = prev })
	h := LogFailures(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inHeader = r.Header.Get(middleware.RequestIDHeader)
		handlerBuf.Reset()
		mlog.From(r.Context()).Info("manifest_wait")
		onCtx = strings.Contains(handlerBuf.String(), `"request_id":"`+rid+`"`)
		Write(w, http.StatusGone, "transcode-stopped", "gone", "")
	}))
	for name, req := range map[string]*http.Request{
		"rid":    httptest.NewRequest(http.MethodGet, "/stream/s1/manifest.m3u8?sig=SECRET&rid="+rid, nil),
		"header": httptest.NewRequest(http.MethodGet, "/stream/s1/manifest.m3u8?sig=SECRET", nil),
	} {
		if name == "header" {
			req.Header.Set("X-Request-Id", strings.ToUpper(rid))
		}
		buf.Reset()
		inHeader, onCtx = "", false
		h.ServeHTTP(httptest.NewRecorder(), req)
		var line map[string]any
		if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
			t.Fatalf("%s: %v (%s)", name, err, buf.String())
		}
		if line["request_id"] != rid || inHeader != rid || !onCtx {
			t.Errorf("%s: line %v, header %q, on the context %v", name, line["request_id"], inHeader, onCtx)
		}
	}
	for _, bad := range []string{"not-an-id", "01932f6e7c1a7b2e9a512f5c3d4e5f60", "x\nlevel=ERROR"} {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, "/stream/s1/manifest.m3u8?rid="+url.QueryEscape(bad), nil)
		h.ServeHTTP(httptest.NewRecorder(), req)
		if strings.Contains(buf.String(), "request_id") {
			t.Errorf("%q was taken as an id: %s", bad, buf.String())
		}
	}
}
