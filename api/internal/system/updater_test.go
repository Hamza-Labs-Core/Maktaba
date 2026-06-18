package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int // sign
	}{
		{"v1.10.0", "v1.9.9", 1},   // numeric, not lexical
		{"1.9.9", "1.10.0", -1},    //
		{"v1.4.2", "v1.4.2", 0},    // equal
		{"1.5.0", "1.5.0-rc.1", 1}, // release outranks its prerelease
		{"1.5.0-rc.1", "1.5.0", -1},
		{"1.5.0-rc.2", "1.5.0-rc.1", 0}, // same numeric core, both prerelease
		{"v2.0.0", "v1.99.99", 1},
		{"dev", "v1.0.0", -1}, // unparseable never newer
	}
	for _, c := range cases {
		got := compareSemver(c.a, c.b)
		if sign(got) != c.want {
			t.Errorf("compareSemver(%q,%q)=%d want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}

func TestSelectLatestChannel(t *testing.T) {
	rels := []ghRelease{
		{TagName: "v1.4.2", HTMLURL: "u142"},
		{TagName: "v1.5.0-rc.1", HTMLURL: "u150rc", Prerelease: true},
		{TagName: "v1.4.1", HTMLURL: "u141"},
		{TagName: "draft", Draft: true},
		{TagName: "not-semver", HTMLURL: "ubad"},
	}

	stable, ok := selectLatest(rels, "stable")
	if !ok || stable.TagName != "v1.4.2" {
		t.Fatalf("stable latest = %q (ok=%v), want v1.4.2", stable.TagName, ok)
	}

	beta, ok := selectLatest(rels, "beta")
	if !ok || beta.TagName != "v1.5.0-rc.1" {
		t.Fatalf("beta latest = %q (ok=%v), want v1.5.0-rc.1", beta.TagName, ok)
	}
}

func TestSelectLatestSuffixPrerelease(t *testing.T) {
	// A release whose tag has a -rc suffix but Prerelease flag unset must
	// still be treated as a prerelease for the stable channel.
	rels := []ghRelease{
		{TagName: "v2.0.0-rc.1"},
		{TagName: "v1.9.0"},
	}
	stable, ok := selectLatest(rels, "stable")
	if !ok || stable.TagName != "v1.9.0" {
		t.Fatalf("stable = %q, want v1.9.0 (rc suffix excluded)", stable.TagName)
	}
}

// ghStub returns an httptest server serving the given releases and counts
// how many times it was hit.
func ghStub(t *testing.T, rels []ghRelease) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(rels)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestStatusAvailableAndAssets(t *testing.T) {
	srv, _ := ghStub(t, []ghRelease{
		{TagName: "v1.4.2", HTMLURL: "url", Body: "notes",
			Assets: []ghAsset{{Name: "checksums.txt", URL: "c", Size: 1}}},
	})
	u := NewUpdater(UpdaterConfig{
		CurrentVersion: "v1.4.1", Channel: "stable",
		APIBaseURL: srv.URL, Now: time.Now,
	})
	s := u.Status(context.Background(), true)
	if !s.Available || s.LatestVersion != "v1.4.2" {
		t.Fatalf("got available=%v latest=%q, want true/v1.4.2", s.Available, s.LatestVersion)
	}
	if len(s.Assets) != 1 || s.Assets[0].Name != "checksums.txt" {
		t.Fatalf("assets not mapped: %+v", s.Assets)
	}
	if s.ReleaseNotes != "notes" || s.ReleaseURL != "url" {
		t.Fatalf("release metadata missing: %+v", s)
	}
}

func TestStatusNotAvailableWhenCurrent(t *testing.T) {
	srv, _ := ghStub(t, []ghRelease{{TagName: "v1.4.2"}})
	u := NewUpdater(UpdaterConfig{CurrentVersion: "v1.4.2", APIBaseURL: srv.URL})
	if s := u.Status(context.Background(), true); s.Available {
		t.Fatalf("should not be available when on latest: %+v", s)
	}
}

func TestStatusCacheWithinTTL(t *testing.T) {
	srv, hits := ghStub(t, []ghRelease{{TagName: "v1.4.2"}})
	u := NewUpdater(UpdaterConfig{
		CurrentVersion: "v1.4.1", APIBaseURL: srv.URL,
		Interval: time.Hour, Now: time.Now,
	})
	_ = u.Status(context.Background(), false) // first: fetch
	first := atomic.LoadInt32(hits)
	_ = u.Status(context.Background(), false) // second: cached
	if got := atomic.LoadInt32(hits); got != first {
		t.Fatalf("expected cache hit (no new fetch); hits %d -> %d", first, got)
	}
	_ = u.Status(context.Background(), true) // refresh: fetch
	if got := atomic.LoadInt32(hits); got != first+1 {
		t.Fatalf("refresh should bypass cache; hits=%d want %d", got, first+1)
	}
}

func TestStatusDisabledNoNetwork(t *testing.T) {
	srv, hits := ghStub(t, []ghRelease{{TagName: "v9.9.9"}})
	u := NewUpdater(UpdaterConfig{
		CurrentVersion: "v1.0.0", APIBaseURL: srv.URL, Disabled: true,
	})
	s := u.Status(context.Background(), true)
	if !s.Disabled || s.Available {
		t.Fatalf("disabled updater should report disabled, not available: %+v", s)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatalf("disabled updater must not hit the network")
	}
}

func TestStatusServesStaleOnError(t *testing.T) {
	// First serve a good release to seed the cache, then a 500.
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode([]ghRelease{{TagName: "v1.4.2"}})
	}))
	t.Cleanup(srv.Close)

	u := NewUpdater(UpdaterConfig{CurrentVersion: "v1.4.1", APIBaseURL: srv.URL})
	first := u.Status(context.Background(), true)
	if !first.Available {
		t.Fatalf("seed status should be available")
	}
	fail.Store(true)
	stale := u.Status(context.Background(), true) // refresh fails
	if stale.LatestVersion != "v1.4.2" {
		t.Fatalf("should serve stale cache on error; got %+v", stale)
	}
}

func TestHandlerEnvelope(t *testing.T) {
	srv, _ := ghStub(t, []ghRelease{{TagName: "v1.4.2", HTMLURL: "url"}})
	u := NewUpdater(UpdaterConfig{CurrentVersion: "v1.4.1", APIBaseURL: srv.URL})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/system/updates?refresh=true", nil)
	u.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got UpdateStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Available || got.LatestVersion != "v1.4.2" {
		t.Fatalf("envelope = %+v", got)
	}
}
