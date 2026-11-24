package selfupdate

// The client against the release feed as GitHub really serves it
// (tests/fixtures/github-releases/page-1.json.gz, a home's own request: ten releases of some
// 190 assets each). Until 2026-11-24 its caps were tested against nothing real; the real list
// grew past the update catalog's cap, and every home's check failed for over a day.

import (
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
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// feedHeadroom and assetGrowth are the update catalog's (updates.FeedHeadroom and its
// tests' AssetGrowth): an answer may use 70% of its cap, and must still fit when the
// newest releases' assets (and files) double.
const (
	feedHeadroom = 0.70
	assetGrowth  = 2.0
)

// the recorded page and its releases, read once for every test here
var (
	realOnce  sync.Once
	realBytes []byte
	realRels  []realRelease
	realErr   error
)

func realPage(t *testing.T) []byte {
	t.Helper()
	realOnce.Do(func() { realBytes, realRels, realErr = loadReal() })
	if realErr != nil {
		t.Fatal(realErr)
	}
	return realBytes
}

func loadReal() ([]byte, []realRelease, error) {
	_, here, _, _ := runtime.Caller(0)
	f, err := os.Open(filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "tests", "fixtures", "github-releases", "page-1.json.gz"))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, err
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, nil, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(b, &raws); err != nil {
		return nil, nil, err
	}
	rels := make([]realRelease, len(raws))
	for i, raw := range raws {
		if err := json.Unmarshal(raw, &rels[i]); err != nil {
			return nil, nil, err
		}
		rels[i].raw = raw
	}
	return b, rels, nil
}

type realRelease struct {
	raw    json.RawMessage
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func realReleases(t *testing.T) []realRelease {
	t.Helper()
	realPage(t)
	return realRels
}

// Latest and ByTag read the real feed: the newest stable release, every asset.
func TestRealFeed(t *testing.T) {
	t.Parallel()
	page := realPage(t)
	rels := realReleases(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if tag, ok := strings.CutPrefix(req.URL.Path, "/repos/"+repo+"/releases/tags/"); ok {
			for _, r := range rels {
				if r.Tag == tag {
					_, _ = w.Write(r.raw)
					return
				}
			}
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(page)
	}))
	t.Cleanup(srv.Close)
	id, _ := releaseverify.NewIdentity(repo)
	c := New(Options{Identity: id, APIBase: srv.URL})
	rel, err := c.Latest(context.Background())
	if err != nil || rel.Tag != rels[0].Tag || len(rel.Assets) != len(rels[0].Assets) {
		t.Fatalf("Latest = %+v, %v; want %s with %d assets", rel, err, rels[0].Tag, len(rels[0].Assets))
	}
	for _, n := range []string{releaseverify.ChecksumsFile, releaseverify.BundleFile, releaseverify.ManifestFile} {
		if rel.Assets[n] == "" {
			t.Fatalf("%s: no %s", rel.Tag, n)
		}
	}
	byTag, err := c.ByTag(context.Background(), rels[4].Tag)
	if err != nil || byTag.Tag != rels[4].Tag || len(byTag.Assets) != len(rels[4].Assets) {
		t.Fatalf("ByTag = %+v, %v", byTag, err)
	}
}

// Each cap holds the real answer, and the answer grown by assetGrowth, in under 70% of it.
func TestRealFeedHeadroom(t *testing.T) {
	t.Parallel()
	page := realPage(t)
	rels := realReleases(t)
	largest := 0
	for _, r := range rels {
		largest = max(largest, len(r.raw))
	}
	check := func(what string, size float64, limit int64) {
		t.Helper()
		if size > feedHeadroom*float64(limit) {
			t.Errorf("%s is %.0f bytes, over %.0f%% of its cap %d", what, size, 100*feedHeadroom, limit)
		}
	}
	check("the recorded page", float64(len(page)), listLimit)
	check("a page of releases twice the largest recorded one", float64(listPerPage)*assetGrowth*float64(largest)+2, listLimit)
	check("the largest recorded release, twice over", assetGrowth*float64(largest), releaseLimit)
	// the small signed files of the newest release, as GitHub sizes them
	newest := rels[0]
	small := map[string]bool{releaseverify.ChecksumsFile: true, releaseverify.BundleFile: true, releaseverify.ManifestFile: true,
		releaseverify.ComponentsIndexName(strings.TrimPrefix(newest.Tag, "v")): true}
	seen := 0
	for _, a := range newest.Assets {
		if small[a.Name] {
			seen++
			check(newest.Tag+"'s "+a.Name+", twice over", assetGrowth*float64(a.Size), smallFileLimit)
		}
	}
	if seen != len(small) {
		t.Fatalf("%s has %d of the %d small signed files", newest.Tag, seen, len(small))
	}
}

// An answer over its cap says so, never that it does not decode.
func TestGetTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v1.5.0","assets":[]}]`))
	}))
	t.Cleanup(srv.Close)
	id, _ := releaseverify.NewIdentity(repo)
	c := New(Options{Identity: id, APIBase: srv.URL})
	var rels []ghRelease
	if err := c.get(context.Background(), "/x", 35, &rels); err != nil || len(rels) != 1 {
		t.Fatalf("at the cap: %v, %d releases", err, len(rels))
	}
	err := c.get(context.Background(), "/x", 34, &rels)
	if !errors.Is(err, errTooLarge) || !strings.Contains(err.Error(), "larger than 34 bytes") {
		t.Fatalf("over the cap: %v", err)
	}
}
