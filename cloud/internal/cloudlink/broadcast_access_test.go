package cloudlink

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The agent passes the home's viewer-access calls to the cloud as the home (its server
// secret), and nothing else under /v1/broadcast (story 50.26).
func TestTheAgentPassesViewerAccessAsTheHome(t *testing.T) {
	var seen []string
	cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+id+":"+secret+" "+string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"i1"}`)
	}))
	defer cloud.Close()
	h := BroadcastAccessHandler(&LinkReporter{CloudAPI: cloud.URL, Creds: Credentials{ServerID: "srv-1", Secret: "s3"}})

	pass := []struct{ method, path, body string }{
		{http.MethodGet, "access", ""},
		{http.MethodPut, "channels/news/access", `{"access":"open"}`},
		{http.MethodPut, "viewer-limits", `{"max_sessions_per_ip":3}`},
		{http.MethodGet, "invites", ""},
		{http.MethodPost, "invites", `{"label":"x"}`},
		{http.MethodDelete, "invites/i1", ""},
		{http.MethodGet, "invites/i1/devices", ""},
		{http.MethodDelete, "invites/i1/devices/p1", ""},
	}
	for _, c := range pass {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(c.method, BroadcastAccessPrefix+c.path, strings.NewReader(c.body)))
		if rec.Code != http.StatusCreated || rec.Body.String() != `{"id":"i1"}` {
			t.Fatalf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	if len(seen) != len(pass) || seen[1] != `PUT /v1/broadcast/channels/news/access srv-1:s3 {"access":"open"}` {
		t.Fatalf("the cloud saw %q", seen)
	}

	// publishing, the state, other methods, other shapes: not passed on
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "channels"},
		{http.MethodGet, "state"},
		{http.MethodDelete, "channels/news"},
		{http.MethodPost, "invites/i1"},
		{http.MethodGet, "invites/../state"},
		{http.MethodGet, "invites/i1/devices/p1/x"},
	} {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(c.method, BroadcastAccessPrefix+c.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s: %d", c.method, c.path, rec.Code)
		}
	}
	if len(seen) != len(pass) {
		t.Fatalf("the cloud saw %d calls", len(seen))
	}
}

func TestViewerAccessUnreachableCloudIsABadGateway(t *testing.T) {
	h := BroadcastAccessHandler(&LinkReporter{CloudAPI: "http://127.0.0.1:1", Creds: Credentials{ServerID: "srv-1", Secret: "s3"}})
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, BroadcastAccessPrefix+"invites", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("%d", rec.Code)
	}
}
