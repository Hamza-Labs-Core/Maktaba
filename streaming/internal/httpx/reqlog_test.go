package httpx

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	do := func(path string) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	do("/stream/s1/seg1.ts?sig=SECRET")
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
