package packs

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// github.com's download door answers 504 to everything: the release's signed files and
// the pack come through GitHub's API asset route, are verified against the signed index
// as ever, and install; the log says where they came from.
func TestInstallFallsBackToTheAPIWhenTheDoorIsDown(t *testing.T) {
	f := newFixture(t, fixtureOpts{api: true})
	f.doorDown = http.StatusGatewayTimeout
	var log bytes.Buffer
	f.m.Net.(*Net).Log = &log
	if err := f.m.Install(context.Background(), "runtime.ml", nil); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	f.installedPack(t)
	if len(f.reqs("/assets/")) == 0 || len(f.reqs("/releases/assets/")) == 0 {
		t.Fatalf("not through the asset route: %q", f.requests)
	}
	// each fetch of a file: the door's three tries, then the API (an install reads the signed files more than once)
	if n := len(f.reqs("/releases/download/v" + tVersion + "/" + rv.ChecksumsFile)); n == 0 || n%3 != 0 {
		t.Fatalf("checksums.txt asked at the door %d times", n)
	}
	l := log.String()
	if !strings.Contains(l, rv.ChecksumsFile+" downloaded from GitHub's API") || !strings.Contains(l, "from github.com failed (try 1 of 3): HTTP 504") {
		t.Fatalf("log:\n%s", l)
	}
}

// Through the API too, a pack whose bytes are not the index's is refused: verify_failed.
func TestInstallRefusesAPackOfAnotherDigestFromTheAPI(t *testing.T) {
	f := newFixture(t, fixtureOpts{api: true})
	f.doorDown = http.StatusBadGateway
	name := f.index.Packs[0].Name
	bad := append([]byte(nil), f.served[name]...)
	bad[len(bad)/2] ^= 0xff
	f.served[name] = bad
	wantReason(t, f.m.Install(context.Background(), "runtime.ml", nil), ReasonVerifyFailed)
	if len(f.reqs("/assets/"+name)) == 0 {
		t.Fatalf("the pack was not fetched through the API: %q", f.requests)
	}
}

// one place's fake: the door at /releases/download, the API at /api, the asset route's
// target at /assets; door answers the n-th door request.
func netAt(t *testing.T, door func(n int) int, apiDown bool) (*Net, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var doorHits, apiHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodGet:
			http.NotFound(w, r)
		case r.URL.Path == "/releases/download/v1.0.0/"+rv.ChecksumsFile:
			if s := door(int(doorHits.Add(1))); s != http.StatusOK {
				http.Error(w, http.StatusText(s), s)
				return
			}
			_, _ = w.Write([]byte("door"))
		case r.URL.Path == "/api/repos/o/r/releases/tags/v1.0.0":
			apiHits.Add(1)
			if apiDown {
				http.Error(w, "down", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"assets":[{"id":5,"name":"` + rv.ChecksumsFile + `"}]}`))
		case r.URL.Path == "/api/repos/o/r/releases/assets/5" && r.Header.Get("Accept") == "application/octet-stream":
			http.Redirect(w, r, "/assets/c", http.StatusFound)
		case r.URL.Path == "/assets/c":
			_, _ = w.Write([]byte("api"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	n := NewNet("o/r", srv.Client())
	n.Releases, n.API = srv.URL+"/releases/download", srv.URL+"/api"
	n.Sleep = func(context.Context, time.Duration) error { return nil }
	return n, &doorHits, &apiHits
}

func fetchChecksums(n *Net) (string, error) {
	var b []byte
	err := n.releaseFile(context.Background(), "1.0.0", rv.ChecksumsFile, func(u, accept string) error {
		var err error
		b, err = n.small(context.Background(), u, accept)
		return err
	})
	return string(b), err
}

func TestReleaseFileRetriesATransientFailureAtTheDoor(t *testing.T) {
	n, door, api := netAt(t, func(i int) int {
		if i == 1 {
			return http.StatusGatewayTimeout
		}
		return http.StatusOK
	}, false)
	if b, err := fetchChecksums(n); err != nil || b != "door" || door.Load() != 2 || api.Load() != 0 {
		t.Fatalf("%q %v (door %d, api %d)", b, err, door.Load(), api.Load())
	}
}

// A 404 at the door is the answer: not found, the API not asked.
func TestReleaseFileTakesTheDoors404(t *testing.T) {
	n, door, api := netAt(t, func(int) int { return http.StatusNotFound }, false)
	if _, err := fetchChecksums(n); !errors.Is(err, errNotFound) || door.Load() != 1 || api.Load() != 0 {
		t.Fatalf("%v (door %d, api %d)", err, door.Load(), api.Load())
	}
}

func TestReleaseFileNamesBothFailures(t *testing.T) {
	n, _, api := netAt(t, func(int) int { return http.StatusBadGateway }, true)
	_, err := fetchChecksums(n)
	if err == nil || !strings.Contains(err.Error(), "github.com: HTTP 502") || !strings.Contains(err.Error(), "GitHub's API: release v1.0.0: HTTP 503") ||
		api.Load() != 3 {
		t.Fatalf("%v (api %d)", err, api.Load())
	}
}

// An update's job (URLs: the catalog's asset URLs) asks once, as before, and never the API.
func TestReleaseFileOfAnUpdateJobAsksOnce(t *testing.T) {
	n, door, api := netAt(t, func(int) int { return http.StatusGatewayTimeout }, false)
	n.URLs = map[string]string{rv.ChecksumsFile: strings.TrimSuffix(n.Releases, "/releases/download") + "/releases/download/v1.0.0/" + rv.ChecksumsFile}
	if _, err := fetchChecksums(n); err == nil || door.Load() != 1 || api.Load() != 0 {
		t.Fatalf("%v (door %d, api %d)", err, door.Load(), api.Load())
	}
}

// A mirror the home set (MAKTABA_PACKS_URL) is not backed by GitHub's API.
func TestAMirrorClearsTheAPI(t *testing.T) {
	n := NewNet("o/r", nil)
	n.API = "https://api.github.com"
	n.WithEnv(func(k string) string { return map[string]string{EnvReleasesURL: "https://mirror.example/rel"}[k] })
	if n.API != "" || n.Releases != "https://mirror.example/rel" {
		t.Fatalf("API %q Releases %q", n.API, n.Releases)
	}
	d := NewNet("o/r", nil)
	d.API = "https://api.github.com"
	d.WithEnv(func(string) string { return "" })
	if d.API == "" {
		t.Fatal("no mirror set, the API was cleared")
	}
}
