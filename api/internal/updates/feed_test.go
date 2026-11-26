package updates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// testFeedVerifier verifies feeds of o/r against a fake Sigstore's root, as
// ReleaseVerifier does against the real one.
type testFeedVerifier struct{ tm root.TrustedMaterial }

func (v testFeedVerifier) VerifyFeed(_ context.Context, in releaseverify.FeedInput) (*releaseverify.Feed, time.Time, error) {
	in.Identity, _ = releaseverify.NewIdentity("o/r")
	in.TrustedMaterial = v.tm
	return releaseverify.VerifyFeed(in)
}

// feedServer serves a channel's signed feed files as maktaba.tv's Worker does.
type feedServer struct {
	mu    sync.Mutex
	srv   *httptest.Server
	files map[string][]byte // "stable/linux-amd64.json" (and its .sigstore.json)
	down  int               // answer this status to everything (0: serve)
	hits  int
}

func newFeedServer(t *testing.T) *feedServer {
	f := &feedServer{files: map[string][]byte{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.hits++
		if f.down != 0 {
			w.WriteHeader(f.down)
			return
		}
		b, ok := f.files[strings.TrimPrefix(r.URL.Path, "/updates/v1/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// feedEnv is a fake GitHub (the files, and a list), a feed server and a fake Sigstore.
type feedEnv struct {
	t     *testing.T
	g     *fakeGitHub
	f     *feedServer
	ss    *releaseverifytest.Sigstore
	tm    root.TrustedMaterial
	srcs  []releaseverify.FeedSource
	pulls map[string]string
	at    time.Time
	sign  releaseverifytest.SignOptions
}

func newFeedEnv(t *testing.T) *feedEnv {
	ss := releaseverifytest.MustNew()
	tr, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	return &feedEnv{t: t, g: newFakeGitHub(t), f: newFeedServer(t), ss: ss, tm: tr, pulls: map[string]string{},
		at: time.Now().Add(-time.Hour), sign: releaseverifytest.GitHubFeed("o/r", "workflow_run")}
}

// platforms every fake release has an archive for: this machine's and two others
func feedTestPlatforms() []string {
	ps := []string{"linux/amd64", "windows/amd64", releaseverify.Platform()}
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// release adds a published release: its signed small files on the fake GitHub, an
// archive per platform; onList also puts it in GitHub's list.
func (e *feedEnv) release(tag string, prerelease, onList bool) {
	ver := NormalizeVersion(tag)
	man := &releaseverify.Manifest{V: 1, Version: ver, Tag: tag, Assets: map[string]releaseverify.Asset{}}
	sums := releaseverify.Checksums{}
	files := map[string][]byte{
		ChecksumsFile: []byte("sums " + tag + "\n"),
		BundleFile:    []byte(`{"bundle":"` + tag + `"}`),
		ManifestFile:  []byte(`{"v":1,"version":"` + ver + `","tag":"` + tag + `"}`),
	}
	for _, p := range feedTestPlatforms() {
		name := "maktaba-server-" + ver + "-" + releaseverify.FeedPlatform(p) + ".tar.gz"
		man.Assets[p] = releaseverify.Asset{Archive: name}
		files[name] = []byte("archive " + p)
	}
	var listed strings.Builder
	for n, b := range files {
		if n != ChecksumsFile && n != BundleFile {
			fmt.Fprintf(&listed, "%s  %s\n", sha256Hex(b), n)
		}
	}
	files[ChecksumsFile] = []byte(listed.String())
	for n, b := range files {
		sums[n] = sha256Hex(b)
	}
	src := releaseverify.FeedSource{Tag: tag, Prerelease: prerelease, PublishedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		URL: "https://example/" + tag, BundleSHA256: sums[BundleFile],
		Verified: &releaseverify.Result{Manifest: man, Checksums: sums, ChecksumsSHA256: sums[ChecksumsFile]}}
	var list []map[string]any
	e.g.set(func() {
		for n, b := range files {
			e.g.files[tag+"/"+n] = b
			u := releaseverify.ReleaseDownloadURL(e.g.srv.URL, "o/r", tag, n)
			src.Assets = append(src.Assets, releaseverify.FeedSourceAsset{Name: n, URL: u, Size: int64(len(b))})
			list = append(list, map[string]any{"name": n, "browser_download_url": u, "size": len(b)})
		}
		if onList {
			e.g.releases = append(e.g.releases, map[string]any{"tag_name": tag, "prerelease": prerelease,
				"published_at": "2026-11-01T00:00:00Z", "assets": list})
		}
	})
	e.srcs = append(e.srcs, src)
}

// publish writes and signs the feeds of both channels as update-feed.yml does.
func (e *feedEnv) publish() {
	e.t.Helper()
	e.at = e.at.Add(time.Minute)
	for _, ch := range []string{releaseverify.FeedStable, releaseverify.FeedBeta} {
		feeds, _, err := releaseverify.BuildFeeds(releaseverify.FeedBuild{Repo: "o/r", Channel: ch,
			DownloadBase: e.g.srv.URL, GeneratedAt: e.at, Withdrawn: e.pulls, Releases: e.srcs})
		if err != nil {
			e.t.Fatal(err)
		}
		for p, f := range feeds {
			b, _ := releaseverify.MarshalFeed(f)
			o := e.sign
			o.SignedAt = e.at
			bun, err := e.ss.SignBlob(b, o)
			if err != nil {
				e.t.Fatal(err)
			}
			e.f.mu.Lock()
			e.f.files[releaseverify.FeedPath(ch, p)] = b
			e.f.files[releaseverify.FeedPath(ch, p)+releaseverify.FeedBundleSuffix] = bun
			e.f.mu.Unlock()
		}
	}
}

func (e *feedEnv) discoverer(store *Store, current string, platforms ...string) *Discoverer {
	d := newDiscoverer(e.g, store, &fakeVerifier{}, &recNotifier{}, current)
	d.Feed = &FeedSource{URL: e.f.srv.URL + "/updates/v1", DownloadBase: e.g.srv.URL,
		Verifier: testFeedVerifier{tm: e.tm}, Platforms: func(context.Context) []string { return platforms }}
	return d
}

// listHits counts GitHub list requests (and optionally fails the list endpoint with 503).
type countingTransport struct {
	mu       sync.Mutex
	list     int
	failList bool
	base     http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/repos/o/r/releases" {
		c.mu.Lock()
		c.list++
		fail := c.failList
		c.mu.Unlock()
		if fail {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{},
				Body:       http.NoBody,
				Request:    r,
			}, nil
		}
	}
	return c.base.RoundTrip(r)
}

func countList(d *Discoverer) *countingTransport {
	c := &countingTransport{base: http.DefaultTransport}
	d.HTTP = &http.Client{Transport: c, Timeout: 10 * time.Second}
	return c
}

func catalogOf(t *testing.T, store *Store) map[string]Release {
	t.Helper()
	cat, err := store.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Release{}
	for _, r := range cat {
		out[r.Version] = r
	}
	return out
}

// The home reads the feed, not GitHub's list: it catalogs and verifies what the feed
// lists, marks what the feed pulled withdrawn, keeps only its machines' platforms' files,
// and chooses the newest release it may install.
func TestFeedIsReadInsteadOfGitHubsList(t *testing.T) {
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			ctx := context.Background()
			e := newFeedEnv(t)
			e.release("v1.5.0", false, false)
			e.release("v1.6.0", false, false)
			e.release("v1.7.0-rc.1", true, false)
			e.pulls["1.6.0"] = "rolled back on homes"
			e.publish()
			store := &Store{DB: be.db}
			d := e.discoverer(store, "1.4.0", "linux/amd64")
			n := &recNotifier{}
			d.Notifier = n
			c := countList(d)
			res, err := d.Check(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if c.list != 0 {
				t.Fatalf("GitHub's list was read %d times", c.list)
			}
			cat := catalogOf(t, store)
			if cat["1.5.0"].VerifyState != VerifyVerified || cat["1.6.0"].WithdrawnAt == nil {
				t.Fatalf("catalog: %+v / %+v (check %+v)", cat["1.5.0"], cat["1.6.0"], res)
			}
			if _, ok := cat["1.7.0-rc.1"]; ok {
				t.Fatal("a pre-release on the stable channel")
			}
			if n.find(NoticeReleaseWithdrew) == nil {
				t.Fatalf("no withdrawal notice: %v", n.kinds())
			}
			// this machine's and its machines' platforms' files from the feed; once verified,
			// every other signed file beside checksums.txt (the feed itself lists none of them)
			var names []string
			for _, a := range cat["1.5.0"].Assets {
				names = append(names, a.Name)
			}
			joined := strings.Join(names, " ")
			if !strings.Contains(joined, releaseverify.FeedPlatform(releaseverify.Platform())) ||
				!strings.Contains(joined, "linux-amd64") || !strings.Contains(joined, "windows-amd64") {
				t.Fatalf("1.5.0's files: %v", names)
			}
			if f := e.f.files["stable/linux-amd64.json"]; bytes.Contains(f, []byte("windows-amd64")) {
				t.Fatal("the linux-amd64 feed lists a Windows file")
			}
			st := store.LoadState(ctx)
			if st.LastCheckSource != SourceFeed || st.LastCheckErr != "" || len(st.FeedSignedAt) == 0 {
				t.Fatalf("state: %+v", st)
			}
			// the newest it may install: 1.5.0 (1.6.0 was pulled)
			all, _ := store.Catalog(ctx)
			if ch := ChooseTarget(all, DefaultPolicy(), "1.4.0", ""); ch.Target == nil || ch.Target.Version != "1.5.0" {
				t.Fatalf("chose %+v", ch.Target)
			}
			// a later pull of 1.5.0: the next check withdraws it, and so does the run's recheck
			e.pulls["1.5.0"] = "found broken"
			e.publish()
			reason, err := d.Recheck(ctx, cat["1.5.0"], ChannelStable)
			if err != nil || reason != WithdrawnGone {
				t.Fatalf("recheck of a pulled release: %q %v", reason, err)
			}
			if r, _ := store.Get(ctx, "1.5.0"); r.WithdrawnAt == nil {
				t.Fatal("1.5.0 not withdrawn")
			}
		})
	}
}

// Beta reads the beta feed, which lists pre-releases.
func TestFeedBetaChannel(t *testing.T) {
	ctx := context.Background()
	e := newFeedEnv(t)
	e.release("v1.5.0", false, false)
	e.release("v1.7.0-rc.1", true, false)
	e.publish()
	store := &Store{DB: backends(t)[0].db}
	d := e.discoverer(store, "1.4.0")
	d.Policy = func(context.Context) (Policy, error) { p := DefaultPolicy(); p.Channel = ChannelBeta; return p, nil }
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if cat := catalogOf(t, store); cat["1.7.0-rc.1"].VerifyState != VerifyVerified {
		t.Fatalf("beta: %+v", cat)
	}
}

// GitHub's list is read only when the feed cannot be reached: down, missing, refused
// connections.
func TestFeedFallsBackToGitHubOnlyWhenUnreachable(t *testing.T) {
	for name, breakIt := range map[string]func(e *feedEnv){
		"a 503":         func(e *feedEnv) { e.f.down = http.StatusServiceUnavailable },
		"not deployed":  func(e *feedEnv) { e.f.files = map[string][]byte{} },
		"no connection": func(e *feedEnv) { e.f.srv.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			e := newFeedEnv(t)
			e.release("v1.5.0", false, true)
			e.publish()
			breakIt(e)
			store := &Store{DB: backends(t)[0].db}
			d := e.discoverer(store, "1.4.0")
			c := countList(d)
			if _, err := d.Check(ctx); err != nil {
				t.Fatal(err)
			}
			if c.list == 0 {
				t.Fatal("GitHub's list was not read")
			}
			if catalogOf(t, store)["1.5.0"].VerifyState != VerifyVerified {
				t.Fatal("1.5.0 not found on GitHub")
			}
			st := store.LoadState(ctx)
			if st.LastCheckSource != SourceGitHub || st.FeedError == "" {
				t.Fatalf("state: %+v", st)
			}
		})
	}
}

// A feed that is reached but fails its checks stops the check: GitHub's list is never
// read instead, and the catalog stays as it was.
func TestFeedRefusedNeverFallsBack(t *testing.T) {
	for name, c := range map[string]struct {
		breakIt func(e *feedEnv)
		want    error
	}{
		"tampered": {func(e *feedEnv) {
			for k, b := range e.f.files {
				if !strings.HasSuffix(k, releaseverify.FeedBundleSuffix) {
					e.f.files[k] = bytes.Replace(b, []byte("1.5.0"), []byte("1.5.1"), 1)
				}
			}
		}, releaseverify.ErrSignature},
		"unsigned": {func(e *feedEnv) {
			for k := range e.f.files {
				if strings.HasSuffix(k, releaseverify.FeedBundleSuffix) {
					delete(e.f.files, k)
				}
			}
		}, releaseverify.ErrSignature},
		"signed by the release workflow": {func(e *feedEnv) {
			e.sign = releaseverifytest.GitHubRelease("o/r", "v1.5.0")
			e.publish()
		}, releaseverify.ErrIdentity},
		"too large": {func(e *feedEnv) {
			for k := range e.f.files {
				if !strings.HasSuffix(k, releaseverify.FeedBundleSuffix) {
					e.f.files[k] = bytes.Repeat([]byte(" "), releaseverify.MaxFeed+1)
				}
			}
		}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			e := newFeedEnv(t)
			e.release("v1.5.0", false, true)
			e.publish()
			c.breakIt(e)
			store := &Store{DB: backends(t)[0].db}
			d := e.discoverer(store, "1.4.0")
			cnt := countList(d)
			_, err := d.Check(ctx)
			if err == nil || !strings.Contains(err.Error(), "update feed refused") || (c.want != nil && !errors.Is(err, c.want)) {
				t.Fatalf("check: %v (want %v)", err, c.want)
			}
			if cnt.list != 0 {
				t.Fatal("GitHub's list was read after the feed was refused")
			}
			if len(catalogOf(t, store)) != 0 {
				t.Fatal("the catalog changed")
			}
			// a failed check like any other (the streak the Updates page and System health
			// report), with its own code
			if st := store.LoadState(ctx); !strings.Contains(st.LastCheckErr, "update feed refused") ||
				st.LastCheckCode != CheckFailFeedRefused || st.Failures != 1 || st.FailingSince == nil {
				t.Fatalf("state: %+v", st)
			}
		})
	}
}

// A feed older than one already read (maktaba.tv rolled back, or an old feed served
// again to hide a release or a pull) is not believed: GitHub's list is read instead.
func TestFeedOlderThanTheLastFallsBack(t *testing.T) {
	ctx := context.Background()
	e := newFeedEnv(t)
	e.release("v1.5.0", false, true)
	e.publish()
	old := map[string][]byte{}
	for k, v := range e.f.files {
		old[k] = v
	}
	e.release("v1.6.0", false, true)
	e.publish()
	store := &Store{DB: backends(t)[0].db}
	d := e.discoverer(store, "1.4.0")
	c := countList(d)
	if _, err := d.Check(ctx); err != nil || c.list != 0 {
		t.Fatalf("the newest feed: %v (list %d)", err, c.list)
	}
	signed := store.LoadState(ctx).FeedSignedAt
	e.f.mu.Lock()
	e.f.files = old
	e.f.mu.Unlock()
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.LoadState(ctx)
	if c.list == 0 || st.LastCheckSource != SourceGitHub || !strings.Contains(st.FeedError, "older") {
		t.Fatalf("an older feed: list %d, state %+v", c.list, st)
	}
	for k, v := range signed {
		if !st.FeedSignedAt[k].Equal(v) {
			t.Fatalf("the older feed's time was recorded: %v, was %v", st.FeedSignedAt[k], v)
		}
	}
}

// The size cap holds a real feed with room to spare: v0.3.161's (190 files on GitHub)
// generated by the release workflow's own code is read and its release verified.
func TestFeedCapAndARealGeneratedFeed(t *testing.T) {
	ctx := context.Background()
	e := newFeedEnv(t)
	dir := "../../../shared/go/releaseverify/testdata/feed/v0.3.161"
	b, err := os.ReadFile(filepath.Join(dir, "release-view.json"))
	if err != nil {
		t.Fatal(err)
	}
	var view releaseverify.GHReleaseView
	if err := json.Unmarshal(b, &view); err != nil {
		t.Fatal(err)
	}
	read := func(n string) []byte { b, _ := os.ReadFile(filepath.Join(dir, n)); return b }
	id, _ := releaseverify.NewIdentity("")
	res, err := releaseverify.Verify(releaseverify.Input{Tag: view.TagName, Identity: id,
		Checksums: read(ChecksumsFile), Bundle: read(BundleFile), Manifest: read(ManifestFile)})
	if err != nil {
		t.Fatal(err)
	}
	// the same release, served by the fake GitHub as o/r
	for i := range view.Assets {
		n := view.Assets[i].Name
		view.Assets[i].URL = releaseverify.ReleaseDownloadURL(e.g.srv.URL, "o/r", view.TagName, n)
		if n == ChecksumsFile || n == BundleFile || n == ManifestFile {
			e.g.files[view.TagName+"/"+n] = read(n)
		}
	}
	e.srcs = []releaseverify.FeedSource{view.Source(res, sha256Hex(read(BundleFile)))}
	e.publish()
	for k, v := range e.f.files {
		if !strings.HasSuffix(k, releaseverify.FeedBundleSuffix) && len(v) > releaseverify.MaxFeed/16 {
			t.Fatalf("%s: %d bytes, within 16× of the cap", k, len(v))
		}
	}
	store := &Store{DB: backends(t)[0].db}
	d := e.discoverer(store, "0.3.150", "linux/amd64", "linux/arm64", "windows/amd64")
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	r := catalogOf(t, store)["0.3.161"]
	if r.VerifyState != VerifyVerified {
		t.Fatalf("0.3.161: %+v", r)
	}
	// the feed lists the machines' platforms' files; once verified, the catalog holds every
	// file the signed checksums.txt lists too (a machine's job asks for packs and plugins by
	// name), each beside checksums.txt, and keeps them across the next check
	sums, _ := releaseverify.ParseChecksums(read(ChecksumsFile))
	check := func(when string) {
		r := catalogOf(t, store)["0.3.161"]
		urls := map[string]string{}
		for _, a := range r.Assets {
			urls[a.Name] = a.URL
		}
		for n := range sums {
			if urls[n] != releaseverify.ReleaseDownloadURL(e.g.srv.URL, "o/r", "v0.3.161", n) {
				t.Fatalf("%s: %s is at %q", when, n, urls[n])
			}
		}
		if _, ok := urls["lang.en-0.3.161-darwin-arm64.tar.zst"]; !ok {
			t.Fatalf("%s: a pack is missing", when)
		}
	}
	check("after the first check")
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	check("after the next check")
}

// A fork's homes and MAKTABA_UPDATE_FEED=off read GitHub only.
func TestDefaultFeed(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	if got := DefaultFeed("", env("")); got != releaseverify.DefaultFeedURL {
		t.Fatal(got)
	}
	if got := DefaultFeed("Hamza-Labs-Core/Maktaba", env("")); got != releaseverify.DefaultFeedURL {
		t.Fatal(got)
	}
	if got := DefaultFeed("me/fork", env("")); got != "" {
		t.Fatal(got)
	}
	if got := DefaultFeed("", env("off")); got != "" {
		t.Fatal(got)
	}
}

// The catalog of a release read from the feed lists only its machines' platforms' files;
// the home's mirror still serves a machine any other file the signed checksums.txt lists
// (a pack), downloaded from beside checksums.txt and kept only when its digest matches.
func TestMirrorServesASignedFileTheFeedDoesNotList(t *testing.T) {
	ctx := context.Background()
	pack := []byte("a pack of another platform")
	files := map[string][]byte{"runtime.lang-1.5.0-linux-arm64.tar.zst": pack, ManifestFile: []byte(`{"v":1}`)}
	sums := ""
	for n, b := range files {
		sums += sha256Hex(b) + "  " + n + "\n"
	}
	files[ChecksumsFile] = []byte(sums)
	files["bad.tar.zst"] = []byte("not listed")
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[filepath.Base(r.URL.Path)]
		if !ok || !strings.HasPrefix(r.URL.Path, "/o/r/releases/download/v1.5.0/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	defer gh.Close()
	store := &Store{DB: backends(t)[0].db}
	if _, err := store.Seen(ctx, Release{Version: "1.5.0", Tag: "v1.5.0", Channel: ChannelStable, Assets: []Asset{
		{Name: ChecksumsFile, URL: gh.URL + "/o/r/releases/download/v1.5.0/" + ChecksumsFile}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVerified(ctx, "1.5.0", Verified{Manifest: Manifest{V: 1, Version: "1.5.0", Tag: "v1.5.0"},
		ChecksumsSHA256: sha256Hex(files[ChecksumsFile])}); err != nil {
		t.Fatal(err)
	}
	m := &Mirror{Store: store, Dir: t.TempDir()}
	defer m.Wait()
	p, sum, err := m.Artifact(ctx, "1.5.0", "runtime.lang-1.5.0-linux-arm64.tar.zst")
	if err != nil || sum != sha256Hex(pack) {
		t.Fatalf("the pack: %q %q %v", p, sum, err)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, pack) {
		t.Fatal("the kept copy")
	}
	if _, _, err := m.Artifact(ctx, "1.5.0", "bad.tar.zst"); !errors.Is(err, errNotMirrored) {
		t.Fatalf("a file checksums.txt does not list: %v", err)
	}
}

// A feed signed more than FeedMaxAge ago (an old feed served again; the real one is
// signed every week) is not believed: GitHub's list is read.
func TestFeedTooOldFallsBack(t *testing.T) {
	ctx := context.Background()
	e := newFeedEnv(t)
	e.release("v1.5.0", false, true)
	e.at = time.Now().Add(-releaseverify.FeedMaxAge - time.Hour)
	e.publish()
	store := &Store{DB: backends(t)[0].db}
	d := e.discoverer(store, "1.4.0")
	c := countList(d)
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if st := store.LoadState(ctx); c.list == 0 || st.LastCheckSource != SourceGitHub || !strings.Contains(st.FeedError, "ago") {
		t.Fatalf("a feed too old: list %d, state %+v", c.list, st)
	}
}

// #1668: a feed signed more than FeedFreshAge (8 days) ago — up to FeedMaxAge (21 days) —
// is still read, but does not count as a fresh read: the home checks GitHub's list too, and
// when GitHub cannot be read it keeps the feed's releases and withdrawals, leaves
// LastCheckOK at the feed's signing time, and reports feed_old so automatic installs wait.
func TestFeedNineDaysOldFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	signedAt := now.Add(-9 * 24 * time.Hour)

	t.Run("github reachable", func(t *testing.T) {
		ctx := context.Background()
		e := newFeedEnv(t)
		e.release("v1.5.0", false, true)
		e.release("v1.5.5", false, true)
		e.pulls["1.5.5"] = "pulled in feed"
		// publish() adds 1 minute to e.at
		e.at = signedAt.Add(-time.Minute)
		e.publish()
		// GitHub also has a newer release the 9-day-old feed does not know about
		e.release("v1.6.0", false, true)

		store := &Store{DB: backends(t)[0].db}
		d := e.discoverer(store, "1.4.0")
		d.Now = func() time.Time { return now }
		c := countList(d)
		if _, err := d.Check(ctx); err != nil {
			t.Fatal(err)
		}
		if c.list == 0 {
			t.Fatal("GitHub's list was not checked for a 9-day-old feed")
		}
		cat := catalogOf(t, store)
		if cat["1.6.0"].VerifyState != VerifyVerified {
			t.Fatalf("1.6.0 from GitHub not verified: %+v", cat["1.6.0"])
		}
		if cat["1.5.5"].WithdrawnAt == nil {
			t.Fatalf("1.5.5 pulled in feed was not withdrawn: %+v", cat["1.5.5"])
		}
		st := store.LoadState(ctx)
		if st.LastCheckSource != SourceGitHub || st.LastCheckCode != "" || st.LastCheckOK == nil || !st.LastCheckOK.Equal(now) {
			t.Fatalf("state: %+v", st)
		}
	})

	t.Run("github down reads feed but holds auto install with feed_old", func(t *testing.T) {
		ctx := context.Background()
		e := newFeedEnv(t)
		e.release("v1.5.0", false, false)
		e.release("v1.6.0", false, false)
		e.pulls["1.6.0"] = "pulled"
		e.at = signedAt.Add(-time.Minute)
		e.publish()
		// Make GitHub's /releases listing fail while asset downloads still succeed
		store := &Store{DB: backends(t)[0].db}
		d := e.discoverer(store, "1.4.0")
		d.Now = func() time.Time { return now }
		c := countList(d)
		c.failList = true
		_, err := d.Check(ctx)
		if !errors.Is(err, errFeedOld) || CheckErrorCode(err) != CheckFailFeedOld {
			t.Fatalf("check err = %v (code %q), want errFeedOld / %q", err, CheckErrorCode(err), CheckFailFeedOld)
		}
		if c.list == 0 {
			t.Fatal("GitHub's list was not tried for a 9-day-old feed")
		}
		cat := catalogOf(t, store)
		if cat["1.5.0"].VerifyState != VerifyVerified || cat["1.6.0"].WithdrawnAt == nil {
			t.Fatalf("9-day-old feed was not read: 1.5.0=%+v 1.6.0=%+v", cat["1.5.0"], cat["1.6.0"])
		}
		st := store.LoadState(ctx)
		if st.LastCheckSource != SourceFeed || st.LastCheckCode != CheckFailFeedOld || st.Failures != 1 {
			t.Fatalf("state: %+v", st)
		}
		if st.LastCheckOK == nil || !st.LastCheckOK.Equal(signedAt) {
			t.Fatalf("LastCheckOK = %v, want feed signing time %v", st.LastCheckOK, signedAt)
		}
		if why := catalogStale(st, now, CheckInterval); why == "" {
			t.Fatal("catalogStale should hold automatic installs on a 9-day-old feed")
		}
	})

	t.Run("previously fresh feed ages past 8 days", func(t *testing.T) {
		ctx := context.Background()
		e := newFeedEnv(t)
		e.release("v1.5.0", false, false)
		e.at = signedAt.Add(-time.Minute)
		e.publish()
		store := &Store{DB: backends(t)[0].db}
		d := e.discoverer(store, "1.4.0")
		// First check at 7 days + 23 hours: fresh feed
		c := countList(d)
		cur := signedAt.Add(releaseverify.FeedFreshAge - time.Hour)
		d.Now = func() time.Time { return cur }
		if _, err := d.Check(ctx); err != nil {
			t.Fatal(err)
		}
		// Two hours later (past 8 days), GitHub is down
		cur = signedAt.Add(releaseverify.FeedFreshAge + time.Hour)
		c.failList = true
		_, err := d.Check(ctx)
		if !errors.Is(err, errFeedOld) {
			t.Fatalf("expected errFeedOld, got %v", err)
		}
		st := store.LoadState(ctx)
		if st.LastCheckOK == nil || !st.LastCheckOK.Equal(signedAt) {
			t.Fatalf("LastCheckOK = %v, want capped at signing time %v", st.LastCheckOK, signedAt)
		}
		if why := catalogStale(st, cur, CheckInterval); why == "" {
			t.Fatal("catalogStale should hold automatic installs once feed ages past 8 days")
		}
	})
}

// A machine on a platform with no feed does not turn the feed off for the home; its files
// come from beside checksums.txt. A release only another platform's feed lists is not one.
func TestFeedMachinePlatformWithoutAFeed(t *testing.T) {
	ctx := context.Background()
	e := newFeedEnv(t)
	e.release("v1.5.0", false, true)
	e.publish()
	// a release in another platform's feed only
	e.f.mu.Lock()
	for k := range e.f.files {
		if strings.Contains(k, "windows-amd64") && releaseverify.Platform() != "windows/amd64" {
			delete(e.f.files, k)
		}
	}
	e.f.mu.Unlock()
	store := &Store{DB: backends(t)[0].db}
	d := e.discoverer(store, "1.4.0", "freebsd/riscv64", "windows/amd64")
	c := countList(d)
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if c.list != 0 || store.LoadState(ctx).LastCheckSource != SourceFeed {
		t.Fatalf("a machine's missing feed turned the feed off (list %d)", c.list)
	}
	r := catalogOf(t, store)["1.5.0"]
	found := false
	for _, a := range r.Assets {
		found = found || a.Name == "maktaba-server-1.5.0-windows-amd64.tar.gz"
	}
	if r.VerifyState != VerifyVerified || !found {
		t.Fatalf("1.5.0: %s, the Windows archive beside checksums.txt: %v", r.VerifyState, found)
	}
}
