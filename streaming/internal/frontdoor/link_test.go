package frontdoor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A machine's stream goes through the home API's pull route (its cluster link); with the
// machine off the link, straight to its port, unless the link is required.
func TestLinkPullAndFallback(t *testing.T) {
	tok := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tok, []byte("t0k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	onLink := true
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Maktaba-Local-Token") != "t0k" || r.URL.Path != "/api/cluster/link/pull" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		if !onLink {
			http.Error(w, "not on the link", http.StatusServiceUnavailable)
			return
		}
		q := r.URL.Query()
		_, _ = io.WriteString(w, "link "+q.Get("node")+" "+q.Get("purpose")+" "+q.Get("path")+" "+r.Header.Get("X-Sig"))
	}))
	defer api.Close()
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(linkNodeHeader) != "" {
			t.Errorf("the link's header reached the machine")
		}
		_, _ = io.WriteString(w, "direct "+r.URL.RequestURI())
	}))
	defer node.Close()
	required := false
	tr := &LinkPull{APIURL: api.URL, TokenFile: tok, Base: http.DefaultTransport, Required: func() bool { return required }}
	get := func() (int, string) {
		req, _ := http.NewRequest(http.MethodGet, node.URL+"/stream/channel/7/live.ts?x=1", nil)
		req.Header.Set("X-Sig", "s1")
		markLink(req, "n1")
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, b := get(); code != 200 || b != "link n1 tv_pull /stream/channel/7/live.ts?x=1 s1" {
		t.Fatalf("over the link: %d %q", code, b)
	}
	onLink = false
	if code, b := get(); code != 200 || b != "direct /stream/channel/7/live.ts?x=1" {
		t.Fatalf("off the link: %d %q", code, b)
	}
	required = true
	if code, _ := get(); code != http.StatusServiceUnavailable {
		t.Fatalf("required, off the link: %d (a direct pull)", code)
	}
}

func TestLoopbackOnlyWhenRequired(t *testing.T) {
	required := false
	h := LoopbackOnlyWhen(func() bool { return required })(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	serve := func(remote string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/stream/channel/7/live.ts", nil)
		req.RemoteAddr = remote
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if serve("192.168.1.20:5000") != 200 {
		t.Fatal("prefer refused the network")
	}
	required = true
	if serve("192.168.1.20:5000") != http.StatusForbidden || serve("127.0.0.1:5000") != 200 {
		t.Fatal("require: the network must be refused and loopback served")
	}
}
