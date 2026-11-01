package telemetry

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

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/ratelimit"
)

var testNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

const (
	homeID = "HomeInstall0000001"
	appID  = "AppInstall00000001"
)

func serverBatch() map[string]any {
	return map[string]any{
		"type": "server", "install_id": homeID, "day": "2026-10-26",
		"report": map[string]any{
			"version": "0.3.26", "os": "darwin", "arch": "arm64", "install_kind": "mac-app",
			"library_size": "1k-10k", "libraries": "2-5", "users": "2-5", "machines": "1",
			"apps":     []any{map[string]any{"app": "web", "platform": "web", "people": "2-5", "requests": "101-1k", "errors": "0"}},
			"features": map[string]any{"remote_access": true, "channels": "6-20", "open_registration": false},
			"errors":   []any{map[string]any{"message": "db_timeout", "count": 3}},
		},
	}
}

func appBatch() map[string]any {
	return map[string]any{
		"type": "app", "install_id": appID, "app": "web", "platform": "web", "form_factor": "desktop",
		"app_version": "1.2.3", "os_version": "18.1", "locale": "ar",
		"events": []any{
			map[string]any{"kind": "app.open", "payload": map[string]any{"cold_start": true}, "ts": "2026-10-26T11:00:00Z"},
			map[string]any{"kind": "feature.used", "payload": map[string]any{"feature": "library", "count": 4}, "ts": "2026-10-26T11:00:00Z"},
			map[string]any{"kind": "player.start", "payload": map[string]any{"source": "direct", "codec": "h264"}, "ts": "2026-10-26T11:00:00Z"},
			map[string]any{"kind": "player.error", "payload": map[string]any{"error_kind": "net", "duration_ms": 1200}, "ts": "2026-10-26T11:00:00Z"},
			map[string]any{"kind": "error.uncaught", "payload": map[string]any{
				"error_message": "TypeError",
				"stack_first_5": "f (app.js:1:2)\ng (vendor.js:3)"}, "ts": "2026-10-26T11:00:00Z"},
		},
		"vitals": []any{map[string]any{"metric": "LCP", "value": 1234.5, "route": "/videos/:id"}},
	}
}

func envelope(batches ...map[string]any) []byte {
	raw := make([]any, len(batches))
	for i, b := range batches {
		raw[i] = b
	}
	out, _ := json.Marshal(map[string]any{"schema": 1, "batches": raw})
	return out
}

func TestParseValid(t *testing.T) {
	bs, err := Parse(envelope(serverBatch(), appBatch(), map[string]any{"type": "forget", "install_id": appID}), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 3 || bs[0].Server == nil || bs[1].App == nil || bs[2].Type != "forget" {
		t.Fatalf("batches: %+v", bs)
	}
	a := bs[1].App
	if len(a.Events) != 4 || len(a.Errors) != 1 || len(a.Vitals) != 1 {
		t.Fatalf("app: %+v", a)
	}
	if got := a.Errors[0].Message; got != "TypeError" {
		t.Errorf("error kind: %q", got)
	}
	if got := a.Errors[0].FirstFrame; got != "f (app.js:1:2)" {
		t.Errorf("first frame: %q", got)
	}
	if bs[0].Server.Features["channels"] != "6-20" || bs[0].Server.Features["remote_access"] != true {
		t.Errorf("features: %v", bs[0].Server.Features)
	}
}

// The coordinator's amendment: app, platform, form factor, os, arch and
// install kind are bounded tokens, not a fixed list.
func TestParseAcceptsTokens(t *testing.T) {
	a := appBatch()
	a["app"], a["platform"], a["form_factor"] = "native", "androidtv", ""
	s := serverBatch()
	r := s["report"].(map[string]any)
	r["install_kind"], r["os"], r["arch"] = "freebsd-jail", "freebsd", "riscv64"
	r["apps"] = []any{map[string]any{"app": "unknown", "platform": "unknown", "people": "0", "requests": "1"}}
	if _, err := Parse(envelope(s, a), testNow); err != nil {
		t.Fatal(err)
	}
}

func TestParseRejections(t *testing.T) {
	type mut func(s, a map[string]any)
	ev := func(a map[string]any, e map[string]any) { a["events"] = []any{e} }
	rep := func(s map[string]any) map[string]any { return s["report"].(map[string]any) }
	cases := map[string]mut{
		"bad install id":     func(s, _ map[string]any) { s["install_id"] = "short" },
		"install id symbols": func(s, _ map[string]any) { s["install_id"] = "abcdefghijklmnop-" },
		"unknown type":       func(s, _ map[string]any) { s["type"] = "user" },
		"bad day":            func(s, _ map[string]any) { s["day"] = "26/10/2026" },
		"day too old":        func(s, _ map[string]any) { s["day"] = "2026-10-01" },
		"day in future":      func(s, _ map[string]any) { s["day"] = "2026-10-29" },
		"no report":          func(s, _ map[string]any) { delete(s, "report") },
		"bad version":        func(s, _ map[string]any) { rep(s)["version"] = "../x" },
		"bad os":             func(s, _ map[string]any) { rep(s)["os"] = "Mac OS" },
		"bad install kind":   func(s, _ map[string]any) { rep(s)["install_kind"] = "" },
		"count not range":    func(s, _ map[string]any) { rep(s)["users"] = "7" },
		"app count not range": func(s, _ map[string]any) {
			rep(s)["apps"] = []any{map[string]any{"app": "web", "platform": "web", "people": "3", "requests": "1"}}
		},
		"report app bad": func(s, _ map[string]any) {
			rep(s)["apps"] = []any{map[string]any{"app": "Web App", "platform": "web", "people": "1", "requests": "1"}}
		},
		"feature name":        func(s, _ map[string]any) { rep(s)["features"] = map[string]any{"Live TV": true} },
		"feature not allowed": func(s, _ map[string]any) { rep(s)["features"] = map[string]any{"sheikh_lectures": "1"} },
		"feature value":       func(s, _ map[string]any) { rep(s)["features"] = map[string]any{"channels": 3} },
		"feature object":      func(s, _ map[string]any) { rep(s)["features"] = map[string]any{"channels": map[string]any{}} },
		"error count":         func(s, _ map[string]any) { rep(s)["errors"] = []any{map[string]any{"message": "x", "count": -1}} },
		"error not a kind": func(s, _ map[string]any) {
			rep(s)["errors"] = []any{map[string]any{"message": "open /srv/Sara/a.mp4 failed", "count": 1}}
		},
		"error message empty": func(s, _ map[string]any) { rep(s)["errors"] = []any{map[string]any{"message": "  ", "count": 1}} },
		"app missing":         func(_, a map[string]any) { delete(a, "app") },
		"platform bad":        func(_, a map[string]any) { a["platform"] = "iOS 18" },
		"form factor bad":     func(_, a map[string]any) { a["form_factor"] = "Big Screen" },
		"app version bad":     func(_, a map[string]any) { a["app_version"] = "" },
		"locale bad":          func(_, a map[string]any) { a["locale"] = "english please" },
		"unknown event kind": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "search.query", "payload": map[string]any{}, "ts": "2026-10-26T11:00:00Z"})
		},
		"event ts bad": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "app.open", "payload": map[string]any{}, "ts": "yesterday"})
		},
		"payload object": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "app.open", "payload": map[string]any{"extra": map[string]any{"a": 1}}, "ts": "2026-10-26T11:00:00Z"})
		},
		"payload array": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "app.open", "payload": map[string]any{"cold_start": []any{true}}, "ts": "2026-10-26T11:00:00Z"})
		},
		"cold_start not bool": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "app.open", "payload": map[string]any{"cold_start": "yes"}, "ts": "2026-10-26T11:00:00Z"})
		},
		"feature not area": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "feature.used", "payload": map[string]any{"feature": "The Godfather"}, "ts": "2026-10-26T11:00:00Z"})
		},
		"count fractional": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "feature.used", "payload": map[string]any{"count": 1.5}, "ts": "2026-10-26T11:00:00Z"})
		},
		"count negative": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "feature.used", "payload": map[string]any{"count": -1}, "ts": "2026-10-26T11:00:00Z"})
		},
		"source unknown": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "player.start", "payload": map[string]any{"source": "torrent"}, "ts": "2026-10-26T11:00:00Z"})
		},
		"codec not code": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "player.start", "payload": map[string]any{"codec": "h264 from /Users/bob"}, "ts": "2026-10-26T11:00:00Z"})
		},
		"error message not str": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "error.uncaught", "payload": map[string]any{"error_message": 5}, "ts": "2026-10-26T11:00:00Z"})
		},
		"error message not a kind": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "error.uncaught", "payload": map[string]any{"error_message": "TypeError: cannot read 'Lecture 1'"}, "ts": "2026-10-26T11:00:00Z"})
		},
		"frame not code": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "error.uncaught", "payload": map[string]any{"error_message": "Error",
				"stack_first_5": "at f (https://nas.local/Users/sara/a.js:1:2)"}, "ts": "2026-10-26T11:00:00Z"})
		},
		// the hard rule: no identity, address or data, by allow-list at every level
		"payload field not allowed": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "app.open", "payload": map[string]any{"cold_start": true, "title": "secret film"}, "ts": "2026-10-26T11:00:00Z"})
		},
		"event field not allowed": func(_, a map[string]any) {
			ev(a, map[string]any{"kind": "app.open", "payload": map[string]any{}, "ts": "2026-10-26T11:00:00Z", "user": "sara"})
		},
		"vital field not allowed": func(_, a map[string]any) {
			a["vitals"] = []any{map[string]any{"metric": "LCP", "value": 1, "url": "https://nas.local/x"}}
		},
		"app user":         func(_, a map[string]any) { a["user"] = "sara" },
		"app email":        func(_, a map[string]any) { a["email"] = "sara@example.org" },
		"app ip":           func(_, a map[string]any) { a["ip"] = "192.0.2.1" },
		"app device name":  func(_, a map[string]any) { a["device_name"] = "Sara's iPhone" },
		"app server field": func(_, a map[string]any) { a["report"] = map[string]any{} },
		"server home name": func(s, _ map[string]any) { s["home_name"] = "Sara's NAS" },
		"server account":   func(s, _ map[string]any) { s["account_id"] = "a1" },
		"server app field": func(s, _ map[string]any) { s["events"] = []any{} },
		"report field":     func(s, _ map[string]any) { rep(s)["home_name"] = "Sara's NAS" },
		"report library":   func(s, _ map[string]any) { rep(s)["library_names"] = []any{"Lectures"} },
		"report app field": func(s, _ map[string]any) {
			rep(s)["apps"] = []any{map[string]any{"app": "web", "platform": "web", "people": "1", "requests": "1", "user": "sara"}}
		},
		"report error field": func(s, _ map[string]any) {
			rep(s)["errors"] = []any{map[string]any{"message": "db_timeout", "count": 1, "path": "/srv/x"}}
		},
		"vital metric":   func(_, a map[string]any) { a["vitals"] = []any{map[string]any{"metric": "XYZ", "value": 1}} },
		"vital value":    func(_, a map[string]any) { a["vitals"] = []any{map[string]any{"metric": "LCP", "value": -1}} },
		"vital no value": func(_, a map[string]any) { a["vitals"] = []any{map[string]any{"metric": "LCP"}} },
		"vital route": func(_, a map[string]any) {
			a["vitals"] = []any{map[string]any{"metric": "LCP", "value": 1, "route": "videos/The Godfather"}}
		},
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			s, a := serverBatch(), appBatch()
			m(s, a)
			_, err := Parse(envelope(s, a), testNow)
			if _, ok := err.(*ValidationError); !ok {
				t.Fatalf("want a ValidationError, got %v", err)
			}
		})
	}

	many := func(key string, n int, item any) map[string]any {
		a := appBatch()
		list := make([]any, n)
		for i := range list {
			list[i] = item
		}
		a[key] = list
		return a
	}
	if _, err := Parse(envelope(many("events", MaxEvents+1, map[string]any{"kind": "app.open", "payload": map[string]any{}, "ts": "2026-10-26T11:00:00Z"})), testNow); err == nil {
		t.Error("101 events accepted")
	}
	if _, err := Parse(envelope(many("vitals", MaxVitals+1, map[string]any{"metric": "LCP", "value": 1})), testNow); err == nil {
		t.Error("51 vitals accepted")
	}
	for _, body := range []string{`{}`, `{"schema":2,"batches":[{}]}`, `{"schema":1,"batches":[]}`, `[`, `{"schema":1,"batches":[5]}`,
		`{"schema":1,"batches":[{"type":"forget","install_id":"AppInstall00000001"}],"home":"Sara's NAS"}`,
		`{"schema":1,"batches":[{"type":"forget","install_id":"AppInstall00000001","user":"sara"}]}`} {
		if _, err := Parse([]byte(body), testNow); err == nil {
			t.Errorf("%s accepted", body)
		}
	}
	bs := make([]map[string]any, MaxBatches+1)
	for i := range bs {
		bs[i] = map[string]any{"type": "forget", "install_id": appID}
	}
	if _, err := Parse(envelope(bs...), testNow); err != ErrTooManyBatches {
		t.Errorf("21 batches: %v", err)
	}
}

func TestParseClampsTime(t *testing.T) {
	a := appBatch()
	a["events"] = []any{
		map[string]any{"kind": "app.open", "payload": map[string]any{"cold_start": false}, "ts": "2030-01-01T00:00:00Z"},
		map[string]any{"kind": "app.open", "payload": map[string]any{}, "ts": "2020-01-01T00:00:00Z"},
	}
	bs, err := Parse(envelope(a), testNow)
	if err != nil {
		t.Fatal(err)
	}
	ev := bs[0].App.Events
	if !ev[0].TS.Equal(testNow) {
		t.Errorf("future ts not clamped: %v", ev[0].TS)
	}
	if !ev[1].TS.Equal(testNow.Add(-MaxEventAge)) {
		t.Errorf("old ts not clamped: %v", ev[1].TS)
	}
}

func TestScrub(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"mac home":      {"open /Users/bob failed", "open <path> failed"},
		"linux home":    {"read /home/alice/.config/maktaba/x.db: denied", "read <path>: denied"},
		"windows home":  {`open C:\Users\carol\Videos\film.mkv`, "open <path>"},
		"source kept":   {"at main (/srv/app/src/main.go:12:3)", "at main (main.go:12:3)"},
		"win source":    {`at C:\build\app\index.js:4`, "at index.js:4"},
		"tilde":         {"bad ~/Movies/x.mkv", "bad <path>"},
		"ipv4":          {"dial tcp 192.168.1.20:8088: refused", "dial tcp <ip>:8088: refused"},
		"ipv6":          {"dial [fe80::1%en0]:80 failed", "dial <ip>:80 failed"},
		"ipv6 plain":    {"from 2001:db8::7 now", "from <ip> now"},
		"email":         {"user bob@example.com missing", "user <email> missing"},
		"url":           {"GET https://nas.home.arpa/api/x?token=1 500", "GET <url> 500"},
		"host":          {"lookup media.example.org: no such host", "lookup <host>: no such host"},
		"local host":    {"cannot reach mynas.local", "cannot reach <host>"},
		"query":         {"bad route /videos?q=godfather&x=1", "bad route /videos"},
		"js file kept":  {"in chunk.abc123.js", "in chunk.abc123.js"},
		"time kept":     {"at 12:30:45", "at 12:30:45"},
		"version kept":  {"version 0.3.26 failed", "version 0.3.26 failed"},
		"route kept":    {"no handler for /videos/:id", "no handler for /videos/:id"},
		"question kept": {"why?", "why?"},
	}
	for name, c := range cases {
		if got := Scrub(c.in); got != c.want {
			t.Errorf("%s: Scrub(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}

func TestFingerprintFoldsNumbers(t *testing.T) {
	if Fingerprint("app", "web", "timeout after 30s", "a.js:1") != Fingerprint("app", "web", "timeout after 31s", "a.js:1") {
		t.Error("numbers split a group")
	}
	if Fingerprint("app", "web", "x", "") == Fingerprint("app", "tv", "x", "") {
		t.Error("apps share a group")
	}
}

type fakeStore struct {
	calls   int
	batches []Batch
	err     error
}

func (f *fakeStore) Ingest(_ context.Context, b []Batch, _ time.Time) error {
	f.calls++
	f.batches = b
	return f.err
}

func newTestHandler(st Ingester) *Handler {
	return &Handler{Store: st, Now: func() time.Time { return testNow },
		PerIP: ratelimit.NewLimiter(100, 0.001), PerInstall: ratelimit.NewLimiter(100, 0.001)}
}

func post(h http.Handler, body []byte, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry", bytes.NewReader(body))
	req.RemoteAddr = ip + ":5000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerStatuses(t *testing.T) {
	st := &fakeStore{}
	h := newTestHandler(st)
	if rec := post(h, envelope(serverBatch(), appBatch()), "203.0.113.1"); rec.Code != http.StatusNoContent {
		t.Fatalf("valid: %d %s", rec.Code, rec.Body)
	}
	if st.calls != 1 || len(st.batches) != 2 {
		t.Fatalf("store: %+v", st)
	}
	if rec := post(h, []byte(`{"schema":1,"batches":[{"type":"nope","install_id":"AppInstall00000001"}]}`), "203.0.113.1"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid: %d", rec.Code)
	}
	big := bytes.Repeat([]byte(" "), MaxBody+1)
	if rec := post(h, big, "203.0.113.1"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: %d", rec.Code)
	}
	bs := make([]map[string]any, MaxBatches+1)
	for i := range bs {
		bs[i] = map[string]any{"type": "forget", "install_id": appID}
	}
	if rec := post(h, envelope(bs...), "203.0.113.1"); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("21 batches: %d", rec.Code)
	}
	if st.calls != 1 {
		t.Errorf("a rejected envelope reached the store")
	}
	st.err = context.DeadlineExceeded
	if rec := post(h, envelope(appBatch()), "203.0.113.1"); rec.Code != http.StatusInternalServerError {
		t.Errorf("store failure: %d", rec.Code)
	}
}

func TestHandlerRateLimits(t *testing.T) {
	st := &fakeStore{}
	h := newTestHandler(st)
	h.PerIP = ratelimit.NewLimiter(3, 0.01)
	for i := 0; i < 3; i++ {
		if rec := post(h, envelope(map[string]any{"type": "forget", "install_id": appID}), "203.0.113.7"); rec.Code != 204 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec := post(h, envelope(map[string]any{"type": "forget", "install_id": appID}), "203.0.113.7")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "100" {
		t.Fatalf("ip limit: %d retry-after %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := post(h, envelope(map[string]any{"type": "forget", "install_id": appID}), "203.0.113.8"); rec.Code != 204 {
		t.Fatalf("another address: %d", rec.Code)
	}

	// per install id, whatever the address; forget is never limited
	h = newTestHandler(st)
	h.PerInstall = ratelimit.NewLimiter(2, 0.05)
	for i := 0; i < 2; i++ {
		if rec := post(h, envelope(appBatch()), "198.51.100."+string(rune('1'+i))); rec.Code != 204 {
			t.Fatalf("install %d: %d", i, rec.Code)
		}
	}
	rec = post(h, envelope(appBatch()), "198.51.100.9")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "20" {
		t.Fatalf("install limit: %d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := post(h, envelope(map[string]any{"type": "forget", "install_id": appID}), "198.51.100.9"); rec.Code != 204 {
		t.Fatalf("forget limited: %d", rec.Code)
	}
}

// The hard rule: the ingest keeps no address. The rate limit's key is a keyed
// hash, never the address; nothing the handler logs or hands the store names
// the sender's address, whether it came as the connection's or in a header.
func TestIngestRecordsNoAddress(t *testing.T) {
	const addr, forwarded = "203.0.113.77", "198.51.100.66"
	var logs bytes.Buffer
	st := &fakeStore{}
	h := newTestHandler(st)
	h.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	for _, body := range [][]byte{envelope(serverBatch(), appBatch()), []byte(`{"schema":1,"batches":[{"type":"x"}]}`)} {
		req := httptest.NewRequest(http.MethodPost, "/v1/telemetry", bytes.NewReader(body))
		req.RemoteAddr = addr + ":5000"
		req.Header.Set("X-Forwarded-For", forwarded)
		req.Header.Set("X-Real-IP", forwarded)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if st.calls != 1 {
		t.Fatalf("%d ingests", st.calls)
	}
	stored, _ := json.Marshal(st.batches)
	for _, a := range []string{addr, forwarded, "203.0.113", "198.51.100"} {
		if strings.Contains(string(stored), a) || strings.Contains(logs.String(), a) {
			t.Errorf("the address %s reached the store or the log", a)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry", nil)
	req.RemoteAddr = addr + ":5000"
	// "203.0" and not "203": the key is a salted hex hash, which holds "203" by chance
	if k := senderKey(req); strings.Contains(k, addr) || strings.Contains(k, "203.0") {
		t.Errorf("the limiter's key holds the address: %s", k)
	}
	other := httptest.NewRequest(http.MethodPost, "/v1/telemetry", nil)
	other.RemoteAddr = "203.0.113.78:5000"
	if senderKey(req) == senderKey(other) {
		t.Error("two addresses share a limiter key")
	}
}
