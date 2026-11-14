package compat

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/client"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func gated(floor Floor, log *refusalLog) http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mw.Client(gate(floor, log)(ok))
}

func send(h http.Handler, method, path, header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	if header != "" {
		req.Header.Set(client.Header, header)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newLog(c *clock, buf *bytes.Buffer) *refusalLog {
	return &refusalLog{every: 24 * time.Hour, now: c.now, last: map[string]time.Time{},
		logger: slog.New(slog.NewJSONHandler(buf, nil))}
}

// The floor's refusal at each sign-in route: 426 client_too_old naming the floor; a newer
// build, a build of another platform, and a request without the header pass.
func TestGateRefusesBelowTheFloor(t *testing.T) {
	var buf bytes.Buffer
	h := gated(Floor{"tv-web/webos": "1.4.0"}, newLog(&clock{time.Unix(0, 0)}, &buf))
	for route := range SignInRoutes {
		method, path, _ := strings.Cut(route, " ")
		rec := send(h, method, path, "tv-web/1.3.9 (webos; tv)")
		if rec.Code != http.StatusUpgradeRequired {
			t.Fatalf("%s from 1.3.9 = %d, want 426", route, rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["code"] != Code || body["floor"] != "1.4.0" || body["client"] != "tv-web/webos" || body["type"] != TypeClientTooOld {
			t.Fatalf("%s body = %v", route, body)
		}
		for _, pass := range []string{"tv-web/1.4.0 (webos; tv)", "tv-web/2.0 (webos; tv)", "tv-web/1.0 (tizen; tv)", ""} {
			if c := send(h, method, path, pass).Code; c != http.StatusOK {
				t.Fatalf("%s from %q = %d, want 200", route, pass, c)
			}
		}
	}
	// any other route is not the gate's
	if c := send(h, "GET", "/api/me", "tv-web/1.3.9 (webos; tv)").Code; c != http.StatusOK {
		t.Fatalf("GET /api/me = %d, want 200", c)
	}
	if c := send(h, "GET", "/api/auth/login", "tv-web/1.3.9 (webos; tv)").Code; c != http.StatusOK {
		t.Fatalf("GET /api/auth/login = %d, want 200 (only the POST signs in)", c)
	}
}

// Each refused app/platform is logged once a day, so Logs says which TVs need an update
// without one line per attempt.
func TestGateLogsARefusedBuildOnceADay(t *testing.T) {
	var buf bytes.Buffer
	c := &clock{time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	h := gated(Floor{"tv-web/webos": "1.4.0", "tv/roku": "2.0.0"}, newLog(c, &buf))
	lines := func() int { return strings.Count(buf.String(), "\n") }
	for i := 0; i < 5; i++ {
		send(h, "POST", "/api/auth/login", "tv-web/1.3.9 (webos; tv)")
	}
	if n := lines(); n != 1 {
		t.Fatalf("%d lines after five refusals, want 1:\n%s", n, buf.String())
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	if line["level"] != "WARN" || line["app"] != "tv-web" || line["platform"] != "webos" ||
		line["version"] != "1.3.9" || line["floor"] != "1.4.0" || line["component"] != "compat" {
		t.Fatalf("line = %v", line)
	}
	// another app/platform has its own day
	send(h, "POST", "/api/auth/refresh", "tv/1.0.0 (roku; tv)")
	if n := lines(); n != 2 {
		t.Fatalf("%d lines, want 2 (the Roku's own)", n)
	}
	c.t = c.t.Add(23 * time.Hour)
	send(h, "POST", "/api/auth/login", "tv-web/1.3.9 (webos; tv)")
	if n := lines(); n != 2 {
		t.Fatalf("%d lines within the day, want 2", n)
	}
	c.t = c.t.Add(time.Hour)
	send(h, "POST", "/api/auth/login", "tv-web/1.3.9 (webos; tv)")
	if n := lines(); n != 3 {
		t.Fatalf("%d lines a day later, want 3", n)
	}
}

// The release's floor is empty: the gate passes every request through.
func TestGateWithTheReleaseFloor(t *testing.T) {
	if len(ReleaseFloor) != 0 {
		t.Skip("this release names a floor")
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := mw.Client(Gate(nil)(ok))
	if c := send(h, "POST", "/api/auth/login", "tv-web/0.0.1 (webos; tv)").Code; c != http.StatusOK {
		t.Fatalf("login = %d, want 200", c)
	}
}
