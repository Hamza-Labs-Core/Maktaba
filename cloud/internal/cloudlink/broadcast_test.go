package cloudlink

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Story 50.22: the agent forwards the four broadcast calls with the server's credential,
// body and answer intact, and refuses every other path and method.
func TestBroadcastHandlerForwardsOnlyItsRoutes(t *testing.T) {
	var seen []string
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "srv-1" || secret != "s3cret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+string(b))
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer cloud.Close()
	h := BroadcastHandler(&LinkReporter{CloudAPI: cloud.URL, Creds: Credentials{ServerID: "srv-1", Secret: "s3cret"}})
	call := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:9123"
		req.Header.Set("Content-Type", "application/json")
		h(rec, req)
		return rec
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/admin/cloud-link/broadcast/state", ""},
		{"POST", "/admin/cloud-link/broadcast/channels", `{"slug":"a"}`},
		{"DELETE", "/admin/cloud-link/broadcast/channels/0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60", ""},
		{"POST", "/admin/cloud-link/broadcast/ingest-token", `{"channel":"c"}`},
	} {
		if rec := call(c.method, c.path, c.body); rec.Code != http.StatusCreated || rec.Body.String() != `{"ok":true}` {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	want := []string{"GET /v1/broadcast/state ", `POST /v1/broadcast/channels {"slug":"a"}`,
		"DELETE /v1/broadcast/channels/0b9a4b5e-5d1c-4a43-9a5e-1c2b3d4e5f60 ", `POST /v1/broadcast/ingest-token {"channel":"c"}`}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("cloud saw %q", seen)
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/admin/cloud-link/broadcast/agreement"},
		{"POST", "/admin/cloud-link/broadcast/state"},
		{"DELETE", "/admin/cloud-link/broadcast/channels/../../servers"},
		{"GET", "/admin/cloud-link/broadcast/"},
		{"PUT", "/admin/cloud-link/broadcast/channels"},
	} {
		if rec := call(c.method, c.path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", c.method, c.path, rec.Code)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("a refused call reached the cloud: %q", seen)
	}
}

// What a web page in a browser on the home machine could send never reaches the cloud: a
// rebound Host, an Origin, a cross-site fetch, a POST that is not JSON.
func TestBroadcastHandlerRefusesBrowsers(t *testing.T) {
	reached := 0
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached++
		w.WriteHeader(http.StatusCreated)
	}))
	defer cloud.Close()
	h := BroadcastHandler(&LinkReporter{CloudAPI: cloud.URL, Creds: Credentials{ServerID: "srv-1", Secret: "s3cret"}})
	send := func(host string, hdr map[string]string) int {
		req := httptest.NewRequest("POST", "/admin/cloud-link/broadcast/channels", strings.NewReader(`{}`))
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	json := map[string]string{"Content-Type": "application/json"}
	for name, c := range map[string]struct {
		host string
		hdr  map[string]string
	}{
		"rebound host": {"evil.example:9123", json},
		"origin":       {"127.0.0.1:9123", map[string]string{"Content-Type": "application/json", "Origin": "https://evil.example"}},
		"cross-site":   {"127.0.0.1:9123", map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"}},
		"text/plain":   {"127.0.0.1:9123", map[string]string{"Content-Type": "text/plain"}},
		"form":         {"localhost:9123", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}},
	} {
		if code := send(c.host, c.hdr); code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, code)
		}
	}
	if reached != 0 {
		t.Fatalf("%d refused calls reached the cloud", reached)
	}
	// the home's API: loopback, JSON, no browser headers (IPv6 loopback too)
	for _, host := range []string{"127.0.0.1:9123", "[::1]:9123", "localhost:9123"} {
		if code := send(host, json); code != http.StatusCreated {
			t.Errorf("%s: %d", host, code)
		}
	}
}
