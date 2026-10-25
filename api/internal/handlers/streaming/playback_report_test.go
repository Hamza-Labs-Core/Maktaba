package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	mlog "github.com/Hamza-Labs-Core/Maktaba/shared/log/go"
)

const reportSession = "0b6f2a4e-6c1d-4f7e-9f38-3f1f3c2d9a10"

// captureLog points the shared logger at a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := mlog.Default
	mlog.Default = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	t.Cleanup(func() { mlog.Default = prev })
	return &buf
}

func postReport(t *testing.T, h *Handler, p *principal.Principal, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/api/stream/sessions/{id}/errors", h.PostPlaybackError)
	req := httptest.NewRequest(http.MethodPost, "/api/stream/sessions/"+session+"/errors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test-agent")
	if p != nil {
		req = req.WithContext(principal.WithPrincipal(context.Background(), p))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPostPlaybackError_LogsPlaybackFailed(t *testing.T) {
	buf := captureLog(t)
	now := time.Now()
	h := &Handler{NowFunc: func() time.Time { return now }}
	body := `{"video_id":"v1","mode":"transcode","engine":"hls.js","outcome":"fatal",
		"code":"fragLoadError","message":"segment failed","http_status":404,
		"url":"http://h/stream/s/seg3.ts?sig=SECRET#x","position_sec":12.5}`
	w := postReport(t, h, &principal.Principal{UserID: "u1"}, reportSession, body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("signature leaked into the log: %s", buf)
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line: %v: %s", err, buf)
	}
	want := map[string]any{
		"msg": "playback_failed", "event": "playback_failed", "level": "ERROR",
		"session_id": reportSession, "video_id": "v1", "user_id": "u1",
		"mode": "transcode", "engine": "hls.js", "outcome": "fatal",
		"code": "fragLoadError", "error": "segment failed",
		"http_status": float64(404), "url": "http://h/stream/s/seg3.ts",
		"position_sec": 12.5, "user_agent": "test-agent",
	}
	for k, v := range want {
		if line[k] != v {
			t.Errorf("%s = %v, want %v", k, line[k], v)
		}
	}
}

func TestPostPlaybackError_ReopenIsWarnAndRateLimited(t *testing.T) {
	buf := captureLog(t)
	now := time.Now()
	h := &Handler{NowFunc: func() time.Time { return now }}
	p := &principal.Principal{UserID: "u1"}
	body := `{"video_id":"v1","outcome":"reopen","code":"levelLoadError"}`
	for i := 0; i < 5; i++ {
		if w := postReport(t, h, p, reportSession, body); w.Code != http.StatusAccepted {
			t.Fatalf("status %d", w.Code)
		}
	}
	if n := strings.Count(buf.String(), "playback_failed"); n != 2 { // msg + event of one line
		t.Fatalf("want one line in the interval, got %d mentions: %s", n, buf)
	}
	if !strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Fatalf("reopen should be a warning: %s", buf)
	}
	now = now.Add(2 * reportInterval)
	postReport(t, h, p, reportSession, body)
	if n := strings.Count(buf.String(), `"event":"playback_failed"`); n != 2 {
		t.Fatalf("after the interval a new line is written, got %d", n)
	}
}

func TestPostPlaybackError_Rejects(t *testing.T) {
	captureLog(t)
	h := &Handler{}
	if w := postReport(t, h, nil, reportSession, `{}`); w.Code != http.StatusForbidden {
		t.Fatalf("no principal: %d", w.Code)
	}
	if w := postReport(t, h, &principal.Principal{UserID: "u1"}, "nope", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", w.Code)
	}
}

func TestClipAndStripQuery(t *testing.T) {
	long := strings.Repeat("ب", reportFieldMax) // 2 bytes each
	c := clip(long)
	if len(c) > reportFieldMax+len("…") || !strings.HasSuffix(c, "…") {
		t.Fatalf("clip: %d bytes", len(c))
	}
	if got := stripQuery("/stream/a/index.m3u8?sig=x"); got != "/stream/a/index.m3u8" {
		t.Fatalf("stripQuery: %q", got)
	}
	if got := stripQuery("https://user:pw@h/p?q#f"); got != "https://h/p" {
		t.Fatalf("stripQuery userinfo: %q", got)
	}
}
