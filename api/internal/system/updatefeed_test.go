package system

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// The About page's check reads this machine's release feed, not GitHub's list; GitHub's
// list only when the feed cannot be reached, never when it is refused.
func TestAboutCheckReadsTheFeed(t *testing.T) {
	ss := releaseverifytest.MustNew()
	tr, _ := ss.TrustedRoot()
	var mu sync.Mutex
	files := map[string][]byte{}
	down, listReads := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch p := r.URL.Path; {
		case p == "/repos/o/r/releases":
			listReads++
			_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v1.7.0", "html_url": "https://example/v1.7.0"}})
		case strings.HasPrefix(p, "/updates/v1/"):
			if down != 0 {
				w.WriteHeader(down)
				return
			}
			b, ok := files[strings.TrimPrefix(p, "/updates/v1/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var srcs []releaseverify.FeedSource
	for _, tag := range []string{"v1.5.0", "v1.6.0"} {
		man := &releaseverify.Manifest{Assets: map[string]releaseverify.Asset{}}
		sums := releaseverify.Checksums{"release.json": strings.Repeat("a", 64)}
		src := releaseverify.FeedSource{Tag: tag, PublishedAt: time.Now(), URL: "https://example/" + tag,
			BundleSHA256: strings.Repeat("b", 64)}
		names := []string{"checksums.txt", "checksums.txt.sigstore.json", "release.json"}
		for _, p := range []string{releaseverify.Platform(), "windows/amd64"} {
			n := "maktaba-server-" + tag + "-" + releaseverify.FeedPlatform(p) + ".tar.gz"
			man.Assets[p] = releaseverify.Asset{Archive: n}
			sums[n] = strings.Repeat("c", 64)
			names = append(names, n)
		}
		for _, n := range names {
			src.Assets = append(src.Assets, releaseverify.FeedSourceAsset{Name: n, Size: 1,
				URL: releaseverify.ReleaseDownloadURL(srv.URL, "o/r", tag, n)})
		}
		src.Verified = &releaseverify.Result{Manifest: man, Checksums: sums, ChecksumsSHA256: strings.Repeat("d", 64)}
		srcs = append(srcs, src)
	}
	feeds, _, err := releaseverify.BuildFeeds(releaseverify.FeedBuild{Repo: "o/r", Channel: "stable", DownloadBase: srv.URL,
		GeneratedAt: time.Now(), Releases: srcs, Withdrawn: map[string]string{"1.6.0": "pulled"}})
	if err != nil {
		t.Fatal(err)
	}
	for p, f := range feeds {
		b, _ := releaseverify.MarshalFeed(f)
		bun, err := ss.SignBlob(b, releaseverifytest.GitHubFeed("o/r", "workflow_run"))
		if err != nil {
			t.Fatal(err)
		}
		files[releaseverify.FeedPath("stable", p)] = b
		files[releaseverify.FeedPath("stable", p)+releaseverify.FeedBundleSuffix] = bun
	}
	u := NewUpdater(UpdaterConfig{Repo: "o/r", CurrentVersion: "v1.4.0", APIBaseURL: srv.URL, FeedURL: srv.URL + "/updates/v1"})
	u.feedTrust = func(context.Context) (root.TrustedMaterial, error) { return tr, nil }
	ctx := context.Background()

	st, err := u.check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.LatestVersion != "v1.5.0" || !st.Available || listReads != 0 {
		t.Fatalf("from the feed: %+v (list read %d)", st, listReads)
	}
	for _, a := range st.Assets {
		if strings.Contains(a.Name, "windows") && releaseverify.Platform() != "windows/amd64" {
			t.Fatalf("another platform's file: %s", a.Name)
		}
	}
	// unreachable: GitHub's list
	down = http.StatusBadGateway
	if st, err = u.check(ctx); err != nil || st.LatestVersion != "v1.7.0" || listReads != 1 {
		t.Fatalf("feed down: %+v %v (list read %d)", st, err, listReads)
	}
	// refused: no fallback
	down = 0
	for k, b := range files {
		if !strings.HasSuffix(k, releaseverify.FeedBundleSuffix) {
			files[k] = bytes.Replace(b, []byte("v1.5.0"), []byte("v1.5.9"), 1)
		}
	}
	if _, err := u.check(ctx); err == nil || !strings.Contains(err.Error(), "update feed refused") || listReads != 1 {
		t.Fatalf("tampered feed: %v (list read %d)", err, listReads)
	}
	// a test's API base and no feed named: no feed (nothing reaches maktaba.tv)
	if NewUpdater(UpdaterConfig{APIBaseURL: srv.URL}).feedURL != "" ||
		NewUpdater(UpdaterConfig{}).feedURL != releaseverify.DefaultFeedURL ||
		NewUpdater(UpdaterConfig{Repo: "me/fork"}).feedURL != "" ||
		NewUpdater(UpdaterConfig{FeedURL: "off"}).feedURL != "" {
		t.Fatal("feed defaults")
	}
}
