package extcheck

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

const (
	fakeGoogleKey = "AIzaSy-google-SECRET-1"
	fakeOllamaKey = "ollama-SECRET-2"
)

// searchFake answers Google's and Ollama's search APIs in process (a
// RoundTripper: no socket is opened, nothing reaches the real services).
type searchFake struct {
	mu      sync.Mutex
	google  func(r *http.Request) (int, string)
	ollama  func(r *http.Request) (int, string)
	seen    []string
	failNet bool
}

func (f *searchFake) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.seen = append(f.seen, r.URL.Host+r.URL.Path)
	f.mu.Unlock()
	if f.failNet {
		return nil, errors.New("dial tcp: connection refused " + r.URL.String())
	}
	status, body := http.StatusNotFound, `{}`
	switch {
	case r.URL.Host == "google.test" && f.google != nil:
		status, body = f.google(r)
	case r.URL.Host == "ollama.test" && f.ollama != nil:
		status, body = f.ollama(r)
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)),
		Header: http.Header{"Content-Type": {"application/json"}}, Request: r}, nil
}

func googleOK(r *http.Request) (int, string) {
	q := r.URL.Query()
	if q.Get("key") != fakeGoogleKey {
		return 400, `{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","errors":[{"reason":"badRequest"}]}}`
	}
	if q.Get("cx") != "cx-1" {
		return 400, `{"error":{"code":400,"message":"Request contains an invalid argument.","errors":[{"reason":"badRequest"}]}}`
	}
	return 200, `{"searchInformation":{"totalResults":"1234"},"items":[{"link":"https://maktaba.example"}]}`
}

func ollamaOK(r *http.Request) (int, string) {
	if r.Header.Get("Authorization") != "Bearer "+fakeOllamaKey {
		return 401, `{"error":"unauthorized"}`
	}
	return 200, `{"results":[{"title":"t","url":"https://x.example","content":"c"}]}`
}

func newWebSearch(t *testing.T, f *searchFake, settings map[string]any, enabled bool, keys map[string]string) *WebSearch {
	t.Helper()
	store := secrets.NewMemory()
	for k, v := range keys {
		if err := store.Put(context.Background(), k, []byte(v), "test"); err != nil {
			t.Fatal(err)
		}
	}
	ext := fakeExt{}
	if enabled {
		ext[WebSearchExtension] = settings
	}
	return &WebSearch{
		Deps:      Deps{Ext: ext, Secrets: store, HTTP: &http.Client{Transport: f}},
		GoogleURL: "https://google.test/customsearch/v1",
		OllamaURL: "https://ollama.test/api/web_search",
	}
}

func wsLine(t *testing.T, res Result, key string) Line {
	t.Helper()
	for _, l := range res.Lines {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no %s line in %+v", key, res.Lines)
	return Line{}
}

func noSecrets(t *testing.T, res Result) {
	t.Helper()
	b, _ := json.Marshal(res)
	for _, k := range []string{fakeGoogleKey, fakeOllamaKey} {
		if strings.Contains(string(b), k) {
			t.Fatalf("a key leaked into the answer: %s", b)
		}
	}
}

func TestWebSearchGoogleWorks(t *testing.T) {
	f := &searchFake{google: googleOK}
	w := newWebSearch(t, f, map[string]any{"google_cx": "cx-1"}, true, map[string]string{GoogleKeySecret: fakeGoogleKey})
	res, err := w.Test(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !wsLine(t, res, "google").OK || !wsLine(t, res, "search").OK {
		t.Fatalf("want ok: %+v", res)
	}
	if !strings.Contains(wsLine(t, res, "google").Detail, "1234 results") {
		t.Errorf("detail: %q", wsLine(t, res, "google").Detail)
	}
	if o := wsLine(t, res, "ollama"); !o.Skipped || !o.Optional {
		t.Errorf("ollama without a key must be skipped: %+v", o)
	}
	noSecrets(t, res)
}

func TestWebSearchRejectedKeyAndBadCx(t *testing.T) {
	f := &searchFake{google: googleOK}
	w := newWebSearch(t, f, map[string]any{"google_cx": "cx-1"}, true, map[string]string{GoogleKeySecret: "wrong"})
	res, _ := w.Test(context.Background())
	if res.OK || wsLine(t, res, "google").Code != CodeTokenRejected {
		t.Fatalf("want token_rejected: %+v", res)
	}
	w = newWebSearch(t, f, map[string]any{"google_cx": "other"}, true, map[string]string{GoogleKeySecret: fakeGoogleKey})
	res, _ = w.Test(context.Background())
	if res.OK || wsLine(t, res, "google").Code != CodeNotFound {
		t.Fatalf("want not_found for a bad cx: %+v", res)
	}
	noSecrets(t, res)
}

func TestWebSearchQuotaAndNetwork(t *testing.T) {
	f := &searchFake{google: func(*http.Request) (int, string) {
		return 429, `{"error":{"code":429,"errors":[{"reason":"rateLimitExceeded"}]}}`
	}}
	w := newWebSearch(t, f, map[string]any{"google_cx": "cx-1"}, true, map[string]string{GoogleKeySecret: fakeGoogleKey})
	res, _ := w.Test(context.Background())
	if wsLine(t, res, "google").Code != CodeRateLimited {
		t.Fatalf("want rate_limited: %+v", res)
	}
	f = &searchFake{failNet: true}
	w = newWebSearch(t, f, map[string]any{"google_cx": "cx-1"}, true, map[string]string{GoogleKeySecret: fakeGoogleKey})
	res, _ = w.Test(context.Background())
	if g := wsLine(t, res, "google"); g.OK || g.Code != CodeUnreachable {
		t.Fatalf("want unreachable: %+v", g)
	}
	noSecrets(t, res) // the transport's error names the URL with the key: never copied
}

func TestWebSearchOllamaAlone(t *testing.T) {
	f := &searchFake{ollama: ollamaOK}
	w := newWebSearch(t, f, map[string]any{}, true, map[string]string{OllamaKeySecret: fakeOllamaKey})
	res, _ := w.Test(context.Background())
	if !res.OK || !wsLine(t, res, "ollama").OK || !wsLine(t, res, "google").Skipped {
		t.Fatalf("want ok through Ollama: %+v", res)
	}
	f = &searchFake{ollama: ollamaOK}
	w = newWebSearch(t, f, map[string]any{}, true, map[string]string{OllamaKeySecret: "bad"})
	res, _ = w.Test(context.Background())
	if res.OK || wsLine(t, res, "ollama").Code != CodeTokenRejected {
		t.Fatalf("want token_rejected: %+v", res)
	}
	noSecrets(t, res)
}

func TestWebSearchOneBackendIsEnough(t *testing.T) {
	f := &searchFake{google: googleOK, ollama: ollamaOK}
	w := newWebSearch(t, f, map[string]any{"google_cx": "cx-1"}, true,
		map[string]string{GoogleKeySecret: fakeGoogleKey, OllamaKeySecret: "bad"})
	res, _ := w.Test(context.Background())
	if !res.OK {
		t.Fatalf("a working Google must make the test pass: %+v", res)
	}
	if o := wsLine(t, res, "ollama"); o.OK || !o.Optional {
		t.Errorf("the failing Ollama is a warning: %+v", o)
	}
}

func TestWebSearchNothingSavedAndOff(t *testing.T) {
	f := &searchFake{}
	w := newWebSearch(t, f, nil, false, nil)
	res, _ := w.Test(context.Background())
	if res.OK || wsLine(t, res, "search").Code != CodeNotConfigured {
		t.Fatalf("want not_configured: %+v", res)
	}
	if e := wsLine(t, res, "enabled"); e.OK || !e.Optional {
		t.Errorf("off is a warning: %+v", e)
	}
	if len(f.seen) != 0 {
		t.Errorf("nothing saved: no request expected, got %v", f.seen)
	}
}
