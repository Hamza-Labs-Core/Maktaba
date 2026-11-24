package system

// The About check against GitHub's release list as GitHub really serves it
// (tests/fixtures/github-releases/page-1.json.gz, a home's own request: ten releases of some
// 190 assets each). Until 2026-11-24 its cap was tested against nothing real; the real list
// grew past the cap of the update catalog's read of it, and every home's check failed.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// listHeadroom and assetGrowth are the update catalog's (updates.ListHeadroom and the
// tests' AssetGrowth): an answer may use 70% of its cap, and must still fit when the
// newest releases' assets double.
const (
	listHeadroom = 0.70
	assetGrowth  = 2.0
)

func realReleasesPage(t *testing.T) []byte {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	f, err := os.Open(filepath.Join(filepath.Dir(here), "..", "..", "..", "tests", "fixtures", "github-releases", "page-1.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func listUpdater(t *testing.T, page []byte) *Updater {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/security-advisories") {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write(page)
	}))
	t.Cleanup(srv.Close)
	return NewUpdater(UpdaterConfig{CurrentVersion: "v0.3.100", Channel: "stable", APIBaseURL: srv.URL, Now: time.Now})
}

// The real page is read whole: every release with every asset.
func TestFetchReleasesRealPage(t *testing.T) {
	page := realReleasesPage(t)
	rels, err := listUpdater(t, page).fetchReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != releasesPerPage {
		t.Fatalf("%d releases, want %d", len(rels), releasesPerPage)
	}
	for _, r := range rels {
		if !validSemver(r.TagName) || len(r.Assets) < 150 || r.HTMLURL == "" {
			t.Fatalf("%s read with %d assets", r.TagName, len(r.Assets))
		}
	}
	if _, ok := selectLatest(rels, "stable"); !ok {
		t.Fatal("no release selected from the real page")
	}
}

// The cap holds the real page, and a page of releases twice the largest recorded one, in
// under 70% of it.
func TestFetchReleasesHeadroom(t *testing.T) {
	page := realReleasesPage(t)
	var rels []json.RawMessage
	if err := json.Unmarshal(page, &rels); err != nil {
		t.Fatal(err)
	}
	largest := 0
	for _, r := range rels {
		largest = max(largest, len(r))
	}
	room := listHeadroom * float64(releasesLimit)
	if float64(len(page)) > room {
		t.Errorf("the recorded page is %d bytes, over %.0f%% of the cap %d", len(page), 100*listHeadroom, releasesLimit)
	}
	worst := float64(releasesPerPage)*assetGrowth*float64(largest) + 2
	if worst > room {
		t.Errorf("a page of releases %.1fx the largest recorded one (%d bytes) is %.0f bytes, over %.0f%% of the cap %d",
			assetGrowth, largest, worst, 100*listHeadroom, releasesLimit)
	}
}

// An answer over the cap says so, never that it does not decode; one of exactly the cap is
// read.
func TestCappedBody(t *testing.T) {
	list := []byte(`[{"tag_name":"v1.0.0"}]`)
	for _, c := range []struct {
		limit int64
		over  bool
	}{{int64(len(list)), false}, {int64(len(list)) + 5, false}, {int64(len(list)) - 1, true}} {
		body := &cappedBody{r: bytes.NewReader(list), left: c.limit}
		rels, err := DecodeJSONArray[ghRelease](body)
		// over is what fetchReleases reads: the decoder may report the cut answer as its own end
		if body.over != c.over || (c.over && err == nil) || (!c.over && (err != nil || len(rels) != 1)) {
			t.Fatalf("limit %d: over %v, %d releases, err %v", c.limit, body.over, len(rels), err)
		}
	}
}

// The real page grown past the cap: the check says the answer is too large.
func TestFetchReleasesTooLarge(t *testing.T) {
	if testing.Short() {
		// it reads 16 MiB, some 2 s under -race: the unit tier's per-test cap is 1.2 s.
		// TestCappedBody proves the same rule on a small answer
		t.Skip("reads an answer the cap's size; runs without -short (the integration tier)")
	}
	page := realReleasesPage(t)
	big := append(append([]byte("["), bytes.Repeat([]byte(" "), releasesLimit)...), page[1:]...)
	_, err := listUpdater(t, big).fetchReleases(context.Background())
	if err == nil || !errors.Is(err, errReleasesTooLarge) || !strings.Contains(err.Error(), "larger than") ||
		strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want larger than the cap", err)
	}
}
