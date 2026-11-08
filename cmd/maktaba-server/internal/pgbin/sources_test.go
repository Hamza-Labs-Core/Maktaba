package pgbin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// behaviour is what a fake place answers to its n-th request for the archive (1-based):
// a status (200 serves body), or hang (no answer until the client gives up).
type behaviour func(n int) (status int, body []byte, hang bool)

func always(status int) behaviour {
	return func(int) (int, []byte, bool) { return status, nil, false }
}

func serves(body []byte) behaviour {
	return func(int) (int, []byte, bool) { return http.StatusOK, body, false }
}

// places are fake servers for Maktaba's CDN, GitHub's API (the tag lookup and the asset
// route), the asset route's redirect target (release-assets) and github.com's download
// door, each counting the archive requests it got.
type places struct {
	cdn, api, assets, gh *httptest.Server
	cdnHits, assetHits   atomic.Int32
	tagHits, ghHits      atomic.Int32
	mu                   sync.Mutex
	assetHeaders         []http.Header // what the redirect target was sent
	routeAccept          string        // the Accept header the asset route was sent
}

const assetID = "4242"

func newPlaces(t *testing.T, pin Pin, cdn, api, gh behaviour) *places {
	t.Helper()
	p := &places{}
	answer := func(w http.ResponseWriter, r *http.Request, b behaviour, n int) {
		status, body, hang := b(n)
		if hang {
			select {
			case <-r.Context().Done():
			case <-time.After(10 * time.Second):
			}
			return
		}
		if status != http.StatusOK {
			http.Error(w, http.StatusText(status), status)
			return
		}
		_, _ = w.Write(body)
	}
	p.cdn = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/postgres/"+Version+"/"+pin.Name() {
			http.NotFound(w, r)
			return
		}
		answer(w, r, cdn, int(p.cdnHits.Add(1)))
	}))
	p.assets = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/released/"+pin.Name() {
			http.NotFound(w, r)
			return
		}
		p.mu.Lock()
		p.assetHeaders = append(p.assetHeaders, r.Header.Clone())
		p.mu.Unlock()
		answer(w, r, api, int(p.assetHits.Add(1)))
	}))
	p.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+Repo+"/releases/tags/"+Version:
			p.tagHits.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]any{
				{"id": 1, "name": "postgresql-" + Version + "-other.tar.gz", "url": "https://elsewhere.invalid/1"},
				{"id": 4242, "name": pin.Name(), "url": "https://elsewhere.invalid/4242"},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/"+Repo+"/releases/assets/"+assetID:
			p.mu.Lock()
			p.routeAccept = r.Header.Get("Accept")
			p.mu.Unlock()
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "json", http.StatusUnsupportedMediaType)
				return
			}
			http.Redirect(w, r, p.assets.URL+"/released/"+pin.Name(), http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	p.gh = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/"+Repo+"/releases/download/"+Version+"/"+pin.Name() {
			http.NotFound(w, r)
			return
		}
		answer(w, r, gh, int(p.ghHits.Add(1)))
	}))
	t.Cleanup(func() { p.cdn.Close(); p.api.Close(); p.assets.Close(); p.gh.Close() })
	return p
}

// downloader is HTTPDownloader at the fakes, with a short wait for a first answer (a hang
// is a timeout) and no real backoff (each wait recorded).
func (p *places) downloader(waits *[]time.Duration) *Downloader {
	c := &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 300 * time.Millisecond}}
	d := HTTPDownloader(c, Mirrors{CDN: p.cdn.URL, API: p.api.URL, Releases: p.gh.URL + "/" + Repo + "/releases/download"})
	d.Tries = 2
	d.Backoff = time.Second
	d.Sleep = func(_ context.Context, w time.Duration) error {
		if waits != nil {
			*waits = append(*waits, w)
		}
		return nil
	}
	return d
}

func testArchive(t *testing.T) ([]byte, Pin) {
	raw := archive(t, Pin{Triple: "test-triple"}, nil)
	return raw, pinFor(raw, false)
}

func ensure(t *testing.T, d *Downloader, pin Pin) (string, string, error) {
	t.Helper()
	root := t.TempDir()
	var log bytes.Buffer
	home, err := Ensure(context.Background(), root, pin, d, &log)
	if err == nil && !Installed(home) {
		t.Fatalf("Ensure gave %s, not an install", home)
	}
	if err != nil {
		if Installed(filepath.Join(root, pin.Dir())) {
			t.Fatal("installed after a failed download")
		}
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".*")); len(left) != 0 {
		t.Fatalf("leftovers %v", left)
	}
	return home, log.String(), err
}

// The CDN answers 503, then hangs: after its tries GitHub's API asset route (through its
// redirect) delivers, and github.com is not asked.
func TestEnsureFallsBackToTheAPIWhenTheCDNIsDown(t *testing.T) {
	raw, pin := testArchive(t)
	cdn := func(n int) (int, []byte, bool) {
		if n == 1 {
			return http.StatusServiceUnavailable, nil, false
		}
		return 0, nil, true
	}
	p := newPlaces(t, pin, cdn, serves(raw), serves(raw))
	var waits []time.Duration
	_, log, err := ensure(t, p.downloader(&waits), pin)
	if err != nil {
		t.Fatal(err)
	}
	if p.cdnHits.Load() != 2 || p.tagHits.Load() != 1 || p.assetHits.Load() != 1 || p.ghHits.Load() != 0 {
		t.Fatalf("hits: cdn %d tag %d asset %d github.com %d", p.cdnHits.Load(), p.tagHits.Load(), p.assetHits.Load(), p.ghHits.Load())
	}
	if !strings.Contains(log, "downloaded from GitHub's API") || !strings.Contains(log, "Maktaba's CDN failed (try 1 of 2): HTTP 503") ||
		!strings.Contains(log, "Maktaba's CDN failed (try 2)") {
		t.Fatalf("log:\n%s", log)
	}
	if len(waits) != 1 || waits[0] != time.Second {
		t.Fatalf("waits %v", waits)
	}
	if p.routeAccept != "application/octet-stream" {
		t.Fatalf("asset route Accept %q", p.routeAccept)
	}
	for _, h := range p.assetHeaders {
		if h.Get("Authorization") != "" || h.Get("Cookie") != "" {
			t.Fatalf("credentials sent to the redirect target: %v", h)
		}
	}
}

// The CDN and the API are down: github.com's door delivers.
func TestEnsureFallsBackToGitHubWhenTheCDNAndTheAPIAreDown(t *testing.T) {
	raw, pin := testArchive(t)
	p := newPlaces(t, pin, always(http.StatusBadGateway), always(http.StatusGatewayTimeout), serves(raw))
	_, log, err := ensure(t, p.downloader(nil), pin)
	if err != nil {
		t.Fatal(err)
	}
	if p.cdnHits.Load() != 2 || p.assetHits.Load() != 2 || p.ghHits.Load() != 1 {
		t.Fatalf("hits: cdn %d asset %d github.com %d", p.cdnHits.Load(), p.assetHits.Load(), p.ghHits.Load())
	}
	if !strings.Contains(log, "downloaded from github.com") {
		t.Fatalf("log:\n%s", log)
	}
}

// A 504 once is tried again at the same place, which then delivers.
func TestEnsureRetriesATransientFailure(t *testing.T) {
	raw, pin := testArchive(t)
	cdn := func(n int) (int, []byte, bool) {
		if n == 1 {
			return http.StatusGatewayTimeout, nil, false
		}
		return http.StatusOK, raw, false
	}
	p := newPlaces(t, pin, cdn, serves(raw), serves(raw))
	_, log, err := ensure(t, p.downloader(nil), pin)
	if err != nil {
		t.Fatal(err)
	}
	if p.cdnHits.Load() != 2 || p.tagHits.Load() != 0 || p.ghHits.Load() != 0 {
		t.Fatalf("hits: cdn %d tag %d github.com %d", p.cdnHits.Load(), p.tagHits.Load(), p.ghHits.Load())
	}
	if !strings.Contains(log, "downloaded from Maktaba's CDN") {
		t.Fatalf("log:\n%s", log)
	}
}

// The CDN sends half the archive and stops: given up as a timeout after stallTimeout,
// tried again (the same), then the API delivers.
func TestEnsureGivesUpOnAStalledDownload(t *testing.T) {
	old := stallTimeout
	stallTimeout = 200 * time.Millisecond
	t.Cleanup(func() { stallTimeout = old })
	raw, pin := testArchive(t)
	var cdnHits atomic.Int32
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/postgres/"+Version+"/"+pin.Name() {
			http.NotFound(w, r)
			return
		}
		cdnHits.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		_, _ = w.Write(raw[:len(raw)/2])
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(cdn.Close)
	p := newPlaces(t, pin, always(http.StatusServiceUnavailable), serves(raw), serves(raw))
	d := p.downloader(nil)
	d.Sources[0].Fetch = HTTPDownloader(&http.Client{}, Mirrors{CDN: cdn.URL}).Sources[0].Fetch
	_, log, err := ensure(t, d, pin)
	if err != nil {
		t.Fatal(err)
	}
	if cdnHits.Load() != 2 || !strings.Contains(log, "no data for 200ms") || !strings.Contains(log, "downloaded from GitHub's API") {
		t.Fatalf("cdn hits %d, log:\n%s", cdnHits.Load(), log)
	}
}

// A 404 is not tried again: the next place is asked at once.
func TestEnsureMovesOnAtOnceFromA404(t *testing.T) {
	raw, pin := testArchive(t)
	p := newPlaces(t, pin, always(http.StatusNotFound), serves(raw), serves(raw))
	if _, _, err := ensure(t, p.downloader(nil), pin); err != nil {
		t.Fatal(err)
	}
	if p.cdnHits.Load() != 1 || p.assetHits.Load() != 1 {
		t.Fatalf("hits: cdn %d asset %d", p.cdnHits.Load(), p.assetHits.Load())
	}
}

// Every place fails: the error names each one's failure, and nothing is installed.
func TestEnsureNamesEverySourcesFailure(t *testing.T) {
	_, pin := testArchive(t)
	p := newPlaces(t, pin, always(http.StatusServiceUnavailable), always(http.StatusForbidden), always(http.StatusGatewayTimeout))
	_, _, err := ensure(t, p.downloader(nil), pin)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"Maktaba's CDN: HTTP 503", "GitHub's API: HTTP 403", "github.com: HTTP 504"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q lacks %q", err, want)
		}
	}
}

// The CDN serves other bytes: they are refused, not asked for again, and the API's
// archive is used.
func TestEnsureRefusesTheCDNsMismatchAndUsesTheNextSource(t *testing.T) {
	raw, pin := testArchive(t)
	bad := bytes.Clone(raw)
	bad[len(bad)/2] ^= 0xff
	p := newPlaces(t, pin, serves(bad), serves(raw), serves(raw))
	_, log, err := ensure(t, p.downloader(nil), pin)
	if err != nil {
		t.Fatal(err)
	}
	if p.cdnHits.Load() != 1 || p.assetHits.Load() != 1 || p.ghHits.Load() != 0 {
		t.Fatalf("hits: cdn %d asset %d github.com %d", p.cdnHits.Load(), p.assetHits.Load(), p.ghHits.Load())
	}
	if !strings.Contains(log, "Maktaba's CDN failed (try 1): archive SHA-256") || !strings.Contains(log, "downloaded from GitHub's API") {
		t.Fatalf("log:\n%s", log)
	}
}

// Every place serves an archive that is not the pinned one (other bytes, fewer, more):
// each is refused, each place asked once, and nothing is unpacked.
func TestEnsureRefusesAMismatchFromEverySource(t *testing.T) {
	raw, pin := testArchive(t)
	bad := bytes.Clone(raw)
	bad[0] ^= 0xff
	p := newPlaces(t, pin, serves(bad), serves(raw[:len(raw)-1]), serves(append(bytes.Clone(raw), 0)))
	_, _, err := ensure(t, p.downloader(nil), pin)
	if !errors.Is(err, errMismatch) || !errors.Is(err, errTooLarge) {
		t.Fatalf("%v", err)
	}
	if p.cdnHits.Load() != 1 || p.assetHits.Load() != 1 || p.ghHits.Load() != 1 {
		t.Fatalf("hits: cdn %d asset %d github.com %d", p.cdnHits.Load(), p.assetHits.Load(), p.ghHits.Load())
	}
	for _, want := range []string{"Maktaba's CDN: archive SHA-256", "GitHub's API: archive of", "github.com:"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("%q lacks %q", err, want)
		}
	}
}

// The API's release has no file of the pin's name: the API is not asked again.
func TestEnsureMovesOnWhenTheReleaseLacksTheAsset(t *testing.T) {
	raw, pin := testArchive(t)
	other := pin
	other.Triple = "another-triple"
	p := newPlaces(t, other, always(http.StatusServiceUnavailable), serves(raw), serves(raw))
	// the CDN and github.com of `other` answer 404 for pin's name; the API lists other's
	_, _, err := ensure(t, p.downloader(nil), pin)
	if err == nil || !strings.Contains(err.Error(), "GitHub's API: release "+Version+": "+pin.Name()+": no such asset") {
		t.Fatalf("%v", err)
	}
	if p.tagHits.Load() != 1 {
		t.Fatalf("tag lookups %d", p.tagHits.Load())
	}
}

// The real places are Maktaba's CDN at postgres/<version>/<name>, GitHub's API for the
// theseus-rs release, then github.com's door (pins.env's URL).
func TestDefaultMirrors(t *testing.T) {
	d := HTTPDownloader(nil, DefaultMirrors())
	var names []string
	for _, s := range d.Sources {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "Maktaba's CDN,GitHub's API,github.com" {
		t.Fatalf("sources %v", names)
	}
	mac, _ := For("darwin", "arm64")
	if got := DefaultCDN + "/" + mac.CDNPath(); got != "https://plugins-cdn.maktaba.tv/postgres/16.15.0/postgresql-16.15.0-aarch64-apple-darwin.tar.gz" {
		t.Fatalf("CDN URL %s", got)
	}
	if mac.URL() != "https://github.com/theseus-rs/postgresql-binaries/releases/download/16.15.0/postgresql-16.15.0-aarch64-apple-darwin.tar.gz" {
		t.Fatalf("github.com URL %s", mac.URL())
	}
	if d := HTTPDownloader(nil, Mirrors{Releases: DefaultReleases}); len(d.Sources) != 1 {
		t.Fatalf("an empty base is not left out: %d sources", len(d.Sources))
	}
}

// The release workflow's list of archives (tools/release/postgres-cdn.txt: name, SHA-256,
// size) is pins: what it publishes to the CDN is what a machine accepts.
func TestTheCDNListIsThePins(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "tools", "release", "postgres-cdn.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, p := range pins {
		want[p.CDNPath()] = p.SHA256 + " " + strconv.FormatInt(p.Size, 10)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("line %q: want <path> <sha256> <size>", line)
		}
		got[f[0]] = f[1] + " " + f[2]
	}
	if len(got) != len(want) {
		t.Fatalf("the list has %d archives, pins %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: the list says %q, pins %q", k, got[k], v)
		}
	}
}
