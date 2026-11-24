package ghasset

// AssetURL against releases as GitHub really serves them (tests/fixtures/github-releases:
// ten of Maktaba's releases, some 190 assets each). A home's packs are found this way
// (packs/source.go), so a release grown past releaseLimit would strand every pack
// download, as the list grown past its cap stranded every home's update check on
// 2026-11-23.

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// feedHeadroom and assetGrowth are the update catalog's (updates.FeedHeadroom and its
// tests' AssetGrowth): a release may use 70% of the cap even with its assets doubled.
const (
	feedHeadroom = 0.70
	assetGrowth  = 2.0
)

func realReleases(t *testing.T) []json.RawMessage {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	f, err := os.Open(filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "tests", "fixtures", "github-releases", "page-1.json.gz"))
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
	var rels []json.RawMessage
	if err := json.Unmarshal(b, &rels); err != nil {
		t.Fatal(err)
	}
	return rels
}

func TestAssetURLRealRelease(t *testing.T) {
	rels := realReleases(t)
	largest := rels[0]
	for _, r := range rels {
		if len(r) > len(largest) {
			largest = r
		}
	}
	if float64(len(largest))*assetGrowth > feedHeadroom*releaseLimit {
		t.Errorf("the largest recorded release is %d bytes: twice that is over %.0f%% of releaseLimit %d",
			len(largest), 100*feedHeadroom, releaseLimit)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(largest, &rel); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(largest) }))
	t.Cleanup(srv.Close)
	// the last asset GitHub lists: the whole answer is read to find it
	last := rel.Assets[len(rel.Assets)-1]
	u, err := AssetURL(context.Background(), srv.Client(), srv.URL, "Hamza-Labs-Core/Maktaba", rel.Tag, last.Name)
	if err != nil || !strings.HasSuffix(u, "/releases/assets/"+strconv.FormatInt(last.ID, 10)) {
		t.Fatalf("AssetURL(%s) = %q, %v", last.Name, u, err)
	}
}
