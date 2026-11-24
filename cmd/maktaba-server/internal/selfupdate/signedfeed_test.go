package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// `maktaba-server update` finds the newest release in the feed (a pulled one never),
// installs it from the files the feed names, and reads GitHub's list only when the feed
// cannot be reached.
func TestLatestFromTheFeed(t *testing.T) {
	ss := releaseverifytest.MustNew()
	tr, _ := ss.TrustedRoot()
	id, _ := releaseverify.NewIdentity(repo)
	rels := map[string]*releaseverifytest.FakeRelease{}
	for _, tag := range []string{"v1.5.0", "v1.6.0"} {
		rels[tag] = ss.MustRelease(repo, tag, releaseverify.Platform())
	}
	var mu sync.Mutex
	feed := map[string][]byte{}
	down, listReads := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/repos/"+repo+"/releases":
			listReads++
			_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v1.6.0", "assets": []any{}}})
		case strings.HasPrefix(p, "/updates/v1/"):
			if down != 0 {
				w.WriteHeader(down)
				return
			}
			b, ok := feed[strings.TrimPrefix(p, "/updates/v1/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		case strings.HasPrefix(p, "/"+repo+"/releases/download/"):
			tag, name, _ := strings.Cut(strings.TrimPrefix(p, "/"+repo+"/releases/download/"), "/")
			if rel := rels[tag]; rel != nil && rel.Files[name] != nil {
				_, _ = w.Write(rel.Files[name])
				return
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var srcs []releaseverify.FeedSource
	for tag, rel := range rels {
		res, err := releaseverify.Verify(releaseverify.Input{Tag: tag, Identity: id, TrustedMaterial: tr,
			Checksums: rel.Files[releaseverify.ChecksumsFile], Bundle: rel.Files[releaseverify.BundleFile],
			Manifest: rel.Files[releaseverify.ManifestFile]})
		if err != nil {
			t.Fatal(err)
		}
		s := releaseverify.FeedSource{Tag: tag, PublishedAt: time.Now(), URL: "https://example/" + tag, Verified: res}
		sum := sha256.Sum256(rel.Files[releaseverify.BundleFile])
		s.BundleSHA256 = hex.EncodeToString(sum[:])
		for n, b := range rel.Files {
			s.Assets = append(s.Assets, releaseverify.FeedSourceAsset{Name: n, Size: int64(len(b)),
				URL: releaseverify.ReleaseDownloadURL(srv.URL, repo, tag, n)})
		}
		srcs = append(srcs, s)
	}
	feeds, _, err := releaseverify.BuildFeeds(releaseverify.FeedBuild{Repo: repo, Channel: "stable", DownloadBase: srv.URL,
		GeneratedAt: time.Now(), Releases: srcs, Withdrawn: map[string]string{"1.6.0": "pulled"}})
	if err != nil {
		t.Fatal(err)
	}
	for p, f := range feeds {
		b, _ := releaseverify.MarshalFeed(f)
		bun, err := ss.SignBlob(b, releaseverifytest.GitHubFeed(repo, "workflow_dispatch"))
		if err != nil {
			t.Fatal(err)
		}
		feed[releaseverify.FeedPath("stable", p)] = b
		feed[releaseverify.FeedPath("stable", p)+releaseverify.FeedBundleSuffix] = bun
	}
	c := New(Options{Identity: id, APIBase: srv.URL, FeedURL: srv.URL + "/updates/v1", FeedDownloadBase: srv.URL,
		Trusted: func(context.Context) (root.TrustedMaterial, error) { return tr, nil }})
	ctx := context.Background()
	rel, err := c.Latest(ctx)
	if err != nil || rel.Tag != "v1.5.0" || listReads != 0 {
		t.Fatalf("latest from the feed: %+v %v (list read %d)", rel, err, listReads)
	}
	self := filepath.Join(t.TempDir(), "maktaba-server")
	_ = os.WriteFile(self, []byte("OLD"), 0o755)
	p, err := c.Prepare(ctx, rel, self)
	if err != nil {
		t.Fatalf("prepare from the feed's files: %v", err)
	}
	if err := p.Install(); err != nil {
		t.Fatal(err)
	}
	// unreachable: GitHub's list
	mu.Lock()
	down = http.StatusServiceUnavailable
	mu.Unlock()
	if rel, err := c.Latest(ctx); err != nil || rel.Tag != "v1.6.0" || listReads != 1 {
		t.Fatalf("feed down: %+v %v (list read %d)", rel, err, listReads)
	}
	// refused: an error, GitHub's list not read
	mu.Lock()
	down = 0
	for k, b := range feed {
		if !strings.HasSuffix(k, releaseverify.FeedBundleSuffix) {
			feed[k] = bytes.Replace(b, []byte("v1.5.0"), []byte("v1.5.1"), 1)
		}
	}
	mu.Unlock()
	if _, err := c.Latest(ctx); err == nil || !strings.Contains(err.Error(), "update feed refused") || listReads != 1 {
		t.Fatalf("tampered feed: %v (list read %d)", err, listReads)
	}
	// defaults: the real API and repository read maktaba.tv's feed; a fork or a test's API none
	if New(Options{Identity: id}).o.FeedURL != releaseverify.DefaultFeedURL {
		t.Fatal("no default feed")
	}
	fork, _ := releaseverify.NewIdentity("me/fork")
	if New(Options{Identity: fork}).o.FeedURL != "" || New(Options{Identity: id, APIBase: srv.URL}).o.FeedURL != "" {
		t.Fatal("a fork or a test reads maktaba.tv")
	}
}
