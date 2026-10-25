package homemirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The signature is the home's (api/internal/cluster/delegation): the shared vectors.
func TestSignatureMatchesTheVectors(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "shared", "testdata", "cluster",
		"delegation_vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Key   string `json:"key_utf8"`
		Cases []struct {
			Name        string `json:"name"`
			Method      string `json:"method"`
			PathQuery   string `json:"path_query"`
			Ts          int64  `json:"ts"`
			Nonce       string `json:"nonce"`
			BodySHA256  string `json:"body_sha256"`
			SessionHash string `json:"session_hash"`
			Canonical   string `json:"canonical"`
			Sig         string `json:"sig"`
			Key         string `json:"key_utf8"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no vectors")
	}
	for _, c := range doc.Cases {
		key := c.Key
		if key == "" {
			key = doc.Key
		}
		got := canonical(c.Method, c.PathQuery, c.Ts, c.Nonce, c.BodySHA256, c.SessionHash)
		if got != c.Canonical {
			t.Errorf("%s: canonical %q, want %q", c.Name, got, c.Canonical)
		}
		if sig := signature([]byte(key), got); sig != c.Sig {
			t.Errorf("%s: signature %s, want %s", c.Name, sig, c.Sig)
		}
	}
}

// home is a stand-in home: it checks each request's signature like the home's verifier
// and serves the release files it holds (status overrides per path).
type home struct {
	t      *testing.T
	srv    *httptest.Server
	key    Key
	files  map[string][]byte
	mu     sync.Mutex
	status map[string][]int // status codes to answer first, per path
	hits   map[string]int
}

func newHome(t *testing.T) *home {
	h := &home{t: t, key: Key{ID: "k1", Secret: []byte("0123456789abcdef0123456789abcdef")},
		files: map[string][]byte{}, status: map[string][]int{}, hits: map[string]int{}}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.hits[r.URL.Path]++
		var code int
		if s := h.status[r.URL.Path]; len(s) > 0 {
			code, h.status[r.URL.Path] = s[0], s[1:]
		}
		h.mu.Unlock()
		ts, _ := strconv.ParseInt(r.Header.Get(headerTs), 10, 64)
		want := signature(h.key.Secret, canonical(r.Method, r.URL.EscapedPath(), ts, r.Header.Get(headerNonce),
			emptyBodyHash, ""))
		if r.Header.Get(headerNode) != "node-1" || r.Header.Get(headerKey) != h.key.ID || r.Header.Get(headerSig) != want {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		if code != 0 {
			if code == http.StatusAccepted {
				w.Header().Set("Retry-After", "1")
			}
			w.WriteHeader(code)
			return
		}
		b, ok := h.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, filepath.Base(r.URL.Path), time.Time{}, strings.NewReader(string(b)))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *home) hit(path string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits[path]
}

func (h *home) client() *Client {
	return &Client{Home: h.srv.URL, NodeID: "node-1", Pause: time.Millisecond, WheelPoll: time.Millisecond,
		Key: func() (Key, bool) { return h.key, true }}
}

// github is a stand-in GitHub serving every release file as "github:<name>".
func github(t *testing.T) (*httptest.Server, *int) {
	gh, _, n := githubWithObjects(t)
	return gh, n
}

// githubWithObjects is a stand-in GitHub that answers a release asset as the real one
// does: a 302 to its object host (the second server, at a path that is no release
// asset's), which serves the file as "github:<name>". n counts GitHub's answers.
func githubWithObjects(t *testing.T) (gh, objects *httptest.Server, n *int) {
	n = new(int)
	var mu sync.Mutex
	objects = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "github:"+filepath.Base(r.URL.Path))
	}))
	t.Cleanup(objects.Close)
	gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*n++
		mu.Unlock()
		if strings.Contains(r.URL.Path, "/releases/download/") {
			http.Redirect(w, r, objects.URL+"/github-production-release-asset/"+filepath.Base(r.URL.Path)+"?X-Amz-Signature=x",
				http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "github:"+filepath.Base(r.URL.Path))
	}))
	t.Cleanup(gh.Close)
	return gh, objects, n
}

func fetch(t *testing.T, c *http.Client, u string, hdr map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// A release file comes from the home's mirror, signed, ranges and all, and again when it
// is asked for again and matched the checksums the mirror served; GitHub is not asked.
func TestTransportPrefersTheMirror(t *testing.T) {
	h := newHome(t)
	manifest := []byte(`{"version":"1.5.0"}`)
	h.files["/api/cluster/updates/artifacts/1.5.0/release.json"] = manifest
	h.files["/api/cluster/updates/artifacts/1.5.0/checksums.txt"] = []byte(sum(manifest) + "  release.json\n")
	gh, ghHits := github(t)
	c := &http.Client{Transport: h.client().Transport(nil)}
	if code, _ := fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/checksums.txt", nil); code != http.StatusOK {
		t.Fatalf("checksums.txt: %d", code)
	}
	code, body := fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/release.json", nil)
	if code != http.StatusOK || body != `{"version":"1.5.0"}` {
		t.Fatalf("release.json: %d %q", code, body)
	}
	code, body = fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/release.json", map[string]string{"Range": "bytes=1-9"})
	if code != http.StatusPartialContent || body != `"version"` {
		t.Fatalf("a range: %d %q", code, body)
	}
	if *ghHits != 0 {
		t.Fatalf("GitHub asked %d times", *ghHits)
	}
}

// A file the home does not hold goes to GitHub at once; a home that fails goes to GitHub
// after three tries; a home that does not take the signature too.
func TestTransportFallsBackToGitHub(t *testing.T) {
	h := newHome(t)
	gh, ghHits := github(t)
	c := &http.Client{Transport: h.client().Transport(nil)}
	code, body := fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/latest.json", nil)
	if code != http.StatusOK || body != "github:latest.json" || h.hit("/api/cluster/updates/artifacts/1.5.0/latest.json") != 1 {
		t.Fatalf("a 404: %d %q (home asked %d times)", code, body, h.hit("/api/cluster/updates/artifacts/1.5.0/latest.json"))
	}
	p := "/api/cluster/updates/artifacts/1.5.0/checksums.txt"
	h.files[p] = []byte("sums")
	h.status[p] = []int{503, 503, 503}
	code, body = fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/checksums.txt", nil)
	if code != http.StatusOK || body != "github:checksums.txt" || h.hit(p) != 3 {
		t.Fatalf("a failing home: %d %q after %d tries", code, body, h.hit(p))
	}
	// a home that could not fetch the file itself: GitHub at once
	h.status[p] = []int{502}
	before := h.hit(p)
	if code, body = fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/checksums.txt", nil); body != "github:checksums.txt" ||
		h.hit(p) != before+1 {
		t.Fatalf("a 502: %d %q after %d tries", code, body, h.hit(p)-before)
	}
	// two failures and then the home answers: the home's file
	h.status[p] = []int{503, 500}
	if code, body = fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/checksums.txt", nil); body != "sums" {
		t.Fatalf("a home that answers on the third try: %d %q", code, body)
	}
	other := h.client()
	other.Key = func() (Key, bool) { return Key{ID: "k1", Secret: []byte("another key, not the home's one")}, true }
	ghBefore := *ghHits
	c2 := &http.Client{Transport: other.Transport(nil)}
	if code, body = fetch(t, c2, gh.URL+"/o/r/releases/download/v1.5.0/checksums.txt", nil); body != "github:checksums.txt" ||
		*ghHits != ghBefore+1 {
		t.Fatalf("a refused signature: %d %q", code, body)
	}
}

// What is not a release file, and a node with no key yet, go straight to where they were
// sent.
func TestTransportLeavesOtherRequests(t *testing.T) {
	h := newHome(t)
	gh, ghHits := github(t)
	c := &http.Client{Transport: h.client().Transport(nil)}
	if _, body := fetch(t, c, gh.URL+"/repos/o/r/releases", nil); body != "github:releases" {
		t.Fatalf("the releases API: %q", body)
	}
	nokey := h.client()
	nokey.Key = func() (Key, bool) { return Key{}, false }
	c = &http.Client{Transport: nokey.Transport(nil)}
	if _, body := fetch(t, c, gh.URL+"/o/r/releases/download/v1.5.0/release.json", nil); body != "github:release.json" {
		t.Fatalf("no key: %q", body)
	}
	if *ghHits != 2 {
		t.Fatalf("GitHub asked %d times", *ghHits)
	}
	for path, ok := range map[string]bool{
		"/o/r/releases/download/v1.5.0/release.json": true,
		"/o/r/releases/download/v1.5.0/a/b":          false,
		"/releases/download/v1.5.0/release.json":     false,
		"/o/r/releases/latest":                       false,
	} {
		req, _ := http.NewRequest(http.MethodGet, "https://github.com"+path, nil)
		if _, _, got := releaseAsset(req.URL); got != ok {
			t.Errorf("releaseAsset(%s) = %v", path, got)
		}
	}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// The wheelhouse: waited for while the home downloads it, each wheel checked against the
// index, the index written last; none kept by the home is no wheelhouse (PyPI).
func TestWheelhouse(t *testing.T) {
	h := newHome(t)
	base := "/api/cluster/updates/wheelhouse/1.5.0/linux-amd64"
	wheel := []byte("wheel anyio")
	idx, _ := json.Marshal(map[string]any{"version": "1.5.0", "platform": "linux/amd64", "python": "3.12",
		"files":   []map[string]any{{"name": "anyio-4.4.0-py3-none-any.whl", "sha256": sum(wheel), "size": len(wheel)}},
		"missing": []string{"torch==2.4.0"}})
	h.files[base] = idx
	h.files[base+"/anyio-4.4.0-py3-none-any.whl"] = wheel
	h.status[base] = []int{http.StatusAccepted}
	dir := filepath.Join(t.TempDir(), "wheelhouse")
	ok, err := h.client().Wheelhouse(context.Background(), "v1.5.0", "linux/amd64", dir)
	if err != nil || !ok {
		t.Fatalf("Wheelhouse = %v, %v", ok, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "anyio-4.4.0-py3-none-any.whl")); string(b) != string(wheel) {
		t.Fatalf("the wheel: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, IndexFile)); err != nil {
		t.Fatal("no index written")
	}
	if n := h.hit(base); n != 2 {
		t.Fatalf("the index asked %d times, want 2 (once while it was downloaded)", n)
	}
	// fetched again: the wheel it has is kept
	if ok, err := h.client().Wheelhouse(context.Background(), "1.5.0", "linux/amd64", dir); !ok || err != nil ||
		h.hit(base+"/anyio-4.4.0-py3-none-any.whl") != 1 {
		t.Fatalf("again: %v %v, the wheel fetched %d times", ok, err, h.hit(base+"/anyio-4.4.0-py3-none-any.whl"))
	}
	// a wheel that is not the one the index lists is refused, and no index is left
	h.files[base+"/anyio-4.4.0-py3-none-any.whl"] = []byte("wheel swapped")
	_ = os.Remove(filepath.Join(dir, "anyio-4.4.0-py3-none-any.whl"))
	if ok, err := h.client().Wheelhouse(context.Background(), "1.5.0", "linux/amd64", dir); ok || err == nil {
		t.Fatalf("a swapped wheel: %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dir, IndexFile)); err == nil {
		t.Fatal("an index was left beside a swapped wheel")
	}
	// a home that keeps none
	if ok, err := h.client().Wheelhouse(context.Background(), "1.5.0", "darwin/arm64", t.TempDir()); ok || err != nil {
		t.Fatalf("none kept: %v %v", ok, err)
	}
	// still downloading at the end of the wait: none
	h.status[base] = []int{http.StatusAccepted, http.StatusAccepted, http.StatusAccepted}
	c := h.client()
	c.WheelWait = time.Millisecond
	if ok, err := c.Wheelhouse(context.Background(), "1.5.0", "linux/amd64", t.TempDir()); ok || err == nil {
		t.Fatalf("never ready: %v %v", ok, err)
	}
}

// The TUF repository's requests to the home are signed (the trusted root comes through it
// when Sigstore's is out of reach); with no key yet none goes.
func TestSignedTransport(t *testing.T) {
	h := newHome(t)
	h.files["/api/cluster/updates/tuf/timestamp.json"] = []byte(`{"ts":1}`)
	c := &http.Client{Transport: h.client().SignedTransport(nil)}
	if code, body := fetch(t, c, h.srv.URL+"/api/cluster/updates/tuf/timestamp.json", nil); code != http.StatusOK ||
		body != `{"ts":1}` {
		t.Fatalf("signed: %d %q", code, body)
	}
	nokey := h.client()
	nokey.Key = func() (Key, bool) { return Key{}, false }
	if _, err := (&http.Client{Transport: nokey.SignedTransport(nil)}).Get(h.srv.URL + "/api/cluster/updates/tuf/timestamp.json"); err == nil {
		t.Fatal("a request went without a key")
	}
}

// A copy the mirror served that did not match the checksums the mirror served, asked for
// again (the stage's verification failed and it tries again), comes from GitHub; with
// GitHub out of reach, from the mirror again (verified again). Over the link alike: the
// rule is the transport's. Story 35.12 review F1.
func TestTransportAsksGitHubForACopyThatDidNotVerify(t *testing.T) {
	h := newHome(t)
	archive := []byte("the archive")
	base := "/api/cluster/updates/artifacts/1.5.0/"
	h.files[base+"checksums.txt"] = []byte(sum(archive) + "  maktaba.tar.gz\n")
	h.files[base+"maktaba.tar.gz"] = []byte("a damaged copy")
	gh, ghHits := github(t)
	c := &http.Client{Transport: h.client().Transport(nil)}
	u := gh.URL + "/o/r/releases/download/v1.5.0/"
	fetch(t, c, u+"checksums.txt", nil)
	if _, body := fetch(t, c, u+"maktaba.tar.gz", nil); body != "a damaged copy" || *ghHits != 0 {
		t.Fatalf("first: %q (GitHub %d)", body, *ghHits)
	}
	if _, body := fetch(t, c, u+"maktaba.tar.gz", nil); body != "github:maktaba.tar.gz" || *ghHits != 1 {
		t.Fatalf("asked again: %q (GitHub %d)", body, *ghHits)
	}
	// the bundle and checksums.txt, which nothing lists, alike
	if _, body := fetch(t, c, u+"checksums.txt", nil); body != "github:checksums.txt" {
		t.Fatalf("checksums.txt asked again: %q", body)
	}
	// GitHub out of reach (an offline node): the mirror again
	gh.Close()
	if _, body := fetch(t, c, u+"maktaba.tar.gz", nil); body != "a damaged copy" {
		t.Fatalf("GitHub away: %q", body)
	}
}

// GitHub answers a release asset with a 302 to its object host: a copy from the home that
// did not verify is taken from there (over the link alike), and when the object host does
// not answer, from the mirror again. Recheck of #287.
func TestTransportFollowsGitHubToItsObjectHost(t *testing.T) {
	archive := []byte("the archive")
	sums := []byte(sum(archive) + "  maktaba.tar.gz\n")
	serve := func(w http.ResponseWriter, path string) {
		switch filepath.Base(path) {
		case "checksums.txt":
			_, _ = w.Write(sums)
		case "maktaba.tar.gz":
			_, _ = io.WriteString(w, "a damaged copy")
		default:
			http.NotFound(w, nil)
		}
	}
	// the signed path, and the cluster link (a local API that carries the mirror's requests)
	h := newHome(t)
	h.files["/api/cluster/updates/artifacts/1.5.0/checksums.txt"] = sums
	h.files["/api/cluster/updates/artifacts/1.5.0/maktaba.tar.gz"] = []byte("a damaged copy")
	tok := filepath.Join(t.TempDir(), "token")
	_ = os.WriteFile(tok, []byte("t0k"), 0o600)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r.URL.Query().Get("path"))
	}))
	t.Cleanup(api.Close)
	overLink := h.client()
	overLink.Link = &LocalLink{API: api.URL, TokenFile: tok}
	for name, c := range map[string]*Client{"signed": h.client(), "link": overLink} {
		gh, objects, ghHits := githubWithObjects(t)
		hc := &http.Client{Transport: c.Transport(nil)}
		u := gh.URL + "/o/r/releases/download/v1.5.0/"
		fetch(t, hc, u+"checksums.txt", nil)
		if _, body := fetch(t, hc, u+"maktaba.tar.gz", nil); body != "a damaged copy" {
			t.Fatalf("%s: first: %q", name, body)
		}
		if code, body := fetch(t, hc, u+"maktaba.tar.gz", nil); code != http.StatusOK || body != "github:maktaba.tar.gz" ||
			*ghHits != 1 {
			t.Fatalf("%s: asked again: %d %q (GitHub %d)", name, code, body, *ghHits)
		}
		// GitHub answers but its object host does not: the mirror again
		objects.Close()
		if _, body := fetch(t, hc, u+"maktaba.tar.gz", nil); body != "a damaged copy" {
			t.Fatalf("%s: the object host away: %q", name, body)
		}
	}
	// a redirect from https to http, or to another scheme, is not followed
	tr := &transport{c: h.client(), base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"http://objects.example/x"}},
			Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	req, _ := http.NewRequest(http.MethodGet, "https://github.com/o/r/releases/download/v1.5.0/x", nil)
	if _, err := tr.github(req); err == nil {
		t.Fatal("followed a redirect from https to http")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
