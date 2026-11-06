package logs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

func reportsRouter(t *testing.T, index string) (*Handler, http.Handler) {
	t.Helper()
	h := &Handler{LogsRoot: t.TempDir(), IndexAddr: index}
	r := chi.NewRouter()
	h.Mount(r)
	reportsRL = limiter{}
	envelopes = limiter{}
	return h, r
}

func as(p *principal.Principal, req *http.Request) *http.Request {
	if p == nil {
		return req
	}
	return req.WithContext(principal.WithPrincipal(req.Context(), p))
}

// Epic 53 story 53.14: an envelope is taken only with the device's consent, kept scrubbed
// under crashes/, and logged as one app_crashed line; nobody signed in is refused.
func TestEnvelope(t *testing.T) {
	h, r := reportsRouter(t, "")
	event := `{"exception":{"values":[{"type":"NSRangeException","value":"index 7 beyond bounds; token=abc","stacktrace":{"frames":[{"function":"main","in_app":false},{"function":"PlayerView.render","in_app":true}]}}]},"user":{"email":"sara@example.com"}}`
	env := func(consent string) string {
		return fmt.Sprintf("{\"event_id\":\"e1\",\"consent\":%q,\"app\":\"player-ios\",\"app_version\":\"0.3.84\",\"install_id\":\"i1\"}\n{\"type\":\"event\",\"length\":%d}\n%s\n{\"type\":\"session\"}\n{}\n{\"type\":\"attachment\"}\nlog line password=hunter2\n",
			consent, len(event), event)
	}
	post := func(body string, p *principal.Principal) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/diagnostics/envelope", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-sentry-envelope")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, as(p, req))
		return rec
	}
	viewer := &principal.Principal{UserID: "u-1"}
	if rec := post(env(ConsentCrashReports), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("nobody: %d", rec.Code)
	}
	if rec := post(env(""), viewer); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "crash_reports_off") {
		t.Fatalf("no consent: %d %s", rec.Code, rec.Body)
	}
	if rec := post(env(ConsentCrashReports), viewer); rec.Code != http.StatusOK {
		t.Fatalf("a viewer with consent: %d %s", rec.Code, rec.Body)
	}
	files, _ := filepath.Glob(filepath.Join(h.LogsRoot, "crashes", "*", "*.json"))
	if len(files) != 1 {
		t.Fatalf("kept %v", files)
	}
	b, _ := os.ReadFile(files[0])
	kept := string(b)
	for _, leak := range []string{"token=abc", "sara@example.com", "hunter2"} {
		if strings.Contains(kept, leak) {
			t.Errorf("kept unscrubbed: %q in %s", leak, kept)
		}
	}
	if !strings.Contains(kept, "NSRangeException") || !strings.Contains(kept, "player-ios") {
		t.Errorf("kept %s", kept)
	}
	if rec := post("not an envelope", viewer); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage: %d", rec.Code)
	}
	for i := 0; i < EnvelopesPerHour; i++ {
		post(env(ConsentCrashReports), &principal.Principal{UserID: "u-2"})
	}
	if rec := post(env(ConsentCrashReports), &principal.Principal{UserID: "u-2"}); rec.Code != http.StatusTooManyRequests ||
		rec.Header().Get("Retry-After") == "" {
		t.Errorf("over the hour's limit: %d", rec.Code)
	}
}

type hub struct {
	mu  sync.Mutex
	got []notify.Payload
}

func (h *hub) Notify(_ context.Context, _ string, p notify.Payload) error {
	h.mu.Lock()
	h.got = append(h.got, p)
	h.mu.Unlock()
	return nil
}

func reportForm(t *testing.T, report string, shot []byte) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("report", report)
	if shot != nil {
		fw, _ := mw.CreateFormFile("screenshot", "s.png")
		_, _ = fw.Write(shot)
	}
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

// Epic 53 story 53.15: a report is stored with the home's side (its requests' lines and
// the warn lines around its time), the admins are notified, a screenshot needs its preview,
// an admin reads and changes it, its sender sees status and reply only, another viewer
// nothing.
func TestReports(t *testing.T) {
	var asked []string
	var mu sync.Mutex
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.RawQuery)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"lines":[{"msg":"server_line","request_id":"r1"}]}`)
	}))
	defer index.Close()
	h, r := reportsRouter(t, strings.TrimPrefix(index.URL, "http://"))
	n := &hub{}
	SetReportNotifier(n)
	t.Cleanup(func() { SetReportNotifier(nil) })
	sara := &principal.Principal{UserID: "u-sara"}
	omar := &principal.Principal{UserID: "u-omar"}
	admin := &principal.Principal{UserID: "u-admin", IsAdmin: true}
	do := func(method, path string, body io.Reader, ct string, p *principal.Principal) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, body)
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, as(p, req))
		return rec
	}
	report := `{"category":"playback","text":"it stopped, my token=abc","app":"web","version":"0.3.84","platform":"web","install_id":"b1","request_ids":["r1"],"lines":[{"msg":"app_line"}]}`
	body, ct := reportForm(t, report, nil)
	if rec := do("POST", "/api/reports", body, ct, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("nobody: %d", rec.Code)
	}
	body, ct = reportForm(t, report, nil)
	rec := do("POST", "/api/reports", body, ct, sara)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"id":1`) {
		t.Fatalf("sent: %d %s", rec.Code, rec.Body)
	}
	if len(n.got) != 1 || n.got[0].Link != "/admin/reports/1" {
		t.Errorf("admins notified: %+v", n.got)
	}
	if len(asked) != 2 || !strings.Contains(asked[0], "request_id=r1") || !strings.Contains(asked[1], "level=warn") {
		t.Errorf("the index was asked %v", asked)
	}
	// a screenshot without its preview is refused
	body, ct = reportForm(t, report, []byte("png"))
	if rec := do("POST", "/api/reports", body, ct, sara); rec.Code != http.StatusBadRequest {
		t.Errorf("an unpreviewed screenshot: %d", rec.Code)
	}
	body, ct = reportForm(t, strings.Replace(report, `"category"`, `"screenshot_previewed":true,"category"`, 1), []byte("png"))
	if rec := do("POST", "/api/reports", body, ct, sara); rec.Code != http.StatusCreated {
		t.Errorf("a previewed screenshot: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(h.LogsRoot, "reports", "2", "screenshot.png")); err != nil {
		t.Errorf("the screenshot is kept: %v", err)
	}
	// the admin reads all of it
	rec = do("GET", "/api/reports/1", nil, "", admin)
	var full Report
	_ = json.Unmarshal(rec.Body.Bytes(), &full)
	if full.Category != "playback" || len(full.Server) != 1 || len(full.Lines) != 1 || strings.Contains(rec.Body.String(), "token=abc") {
		t.Fatalf("the admin's report: %s", rec.Body)
	}
	if rec := do("POST", "/api/reports/1/status", strings.NewReader(`{"status":"looking"}`), "application/json", admin); rec.Code != 200 {
		t.Fatalf("status: %d", rec.Code)
	}
	if rec := do("POST", "/api/reports/1/reply", strings.NewReader(`{"reply":"Fixed in 0.3.85"}`), "application/json", admin); rec.Code != 200 {
		t.Fatalf("reply: %d", rec.Code)
	}
	if rec := do("POST", "/api/reports/1/status", strings.NewReader(`{"status":"looking"}`), "application/json", sara); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer changes a status: %d", rec.Code)
	}
	// its sender: status and reply, never the server's part
	rec = do("GET", "/api/reports/1", nil, "", sara)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Fixed in 0.3.85") || strings.Contains(rec.Body.String(), "server_line") {
		t.Fatalf("the sender's view: %d %s", rec.Code, rec.Body)
	}
	// another viewer: not theirs
	if rec := do("GET", "/api/reports/1", nil, "", omar); rec.Code != http.StatusNotFound {
		t.Errorf("another viewer: %d", rec.Code)
	}
	if rec := do("GET", "/api/reports", nil, "", omar); strings.Contains(rec.Body.String(), `"id":1`) {
		t.Errorf("another viewer's list: %s", rec.Body)
	}
	if rec := do("GET", "/api/reports", nil, "", sara); !strings.Contains(rec.Body.String(), `"id":2`) ||
		!strings.Contains(rec.Body.String(), `"id":1`) {
		t.Errorf("the sender's list: %s", rec.Body)
	}
	if rec := do("GET", "/api/reports/1/bundle", nil, "", admin); rec.Code != 200 || rec.Header().Get("Content-Type") != "application/gzip" {
		t.Errorf("bundle: %d", rec.Code)
	}
	if rec := do("GET", "/api/reports/1/bundle", nil, "", sara); rec.Code != http.StatusForbidden {
		t.Errorf("a viewer's bundle: %d", rec.Code)
	}
	bad, ct := reportForm(t, `{"category":"nope"}`, nil)
	if rec := do("POST", "/api/reports", bad, ct, sara); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad category: %d", rec.Code)
	}
}
