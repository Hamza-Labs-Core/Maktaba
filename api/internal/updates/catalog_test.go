package updates

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/notify"
)

// fakeGitHub serves a releases list, single releases by tag and their asset files.
type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	releases []map[string]any
	files    map[string][]byte // "<tag>/<name>"
	limited  bool
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{files: map[string][]byte{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.limited {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	p := r.URL.Path
	switch {
	case p == "/repos/o/r/releases":
		_ = json.NewEncoder(w).Encode(g.releases)
	case strings.HasPrefix(p, "/repos/o/r/releases/tags/"):
		tag := strings.TrimPrefix(p, "/repos/o/r/releases/tags/")
		for _, rel := range g.releases {
			if rel["tag_name"] == tag {
				_ = json.NewEncoder(w).Encode(rel)
				return
			}
		}
		http.NotFound(w, r)
	case strings.HasPrefix(p, "/o/r/releases/download/"):
		b, ok := g.files[strings.TrimPrefix(p, "/o/r/releases/download/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	default:
		http.NotFound(w, r)
	}
}

// add publishes a release with its three small files (manifest: whether release.json
// and the bundle exist).
func (g *fakeGitHub) add(tag string, prerelease, manifest bool, checksums string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	names := []string{ChecksumsFile}
	if manifest {
		names = append(names, BundleFile, ManifestFile)
	}
	var assets []map[string]any
	for _, n := range names {
		assets = append(assets, map[string]any{"name": n, "browser_download_url": g.srv.URL + "/o/r/releases/download/" + tag + "/" + n, "size": 10})
	}
	g.files[tag+"/"+ChecksumsFile] = []byte(checksums)
	g.files[tag+"/"+BundleFile] = []byte(`{"bundle":"` + tag + `"}`)
	g.files[tag+"/"+ManifestFile] = []byte(`{"v":1,"version":"` + NormalizeVersion(tag) + `","tag":"` + tag + `"}`)
	g.releases = append(g.releases, map[string]any{"tag_name": tag, "html_url": "https://example/" + tag,
		"body": "notes " + tag, "prerelease": prerelease, "published_at": "2026-10-01T00:00:00Z", "assets": assets})
}

func (g *fakeGitHub) set(fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn()
}

// fakeVerifier accepts every tag but those in fail.
type fakeVerifier struct {
	fail  map[string]*VerifyError
	calls []string
	extra map[string]func(*Manifest)
}

func (f *fakeVerifier) VerifyRelease(_ context.Context, tag string, files ReleaseFiles) (Verified, error) {
	f.calls = append(f.calls, tag)
	if e := f.fail[tag]; e != nil {
		return Verified{}, e
	}
	var m Manifest
	if err := json.Unmarshal(files.Manifest, &m); err != nil {
		return Verified{}, &VerifyError{Kind: KindManifest, File: ManifestFile, Detail: err.Error()}
	}
	m.Published = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if fn := f.extra[tag]; fn != nil {
		fn(&m)
	}
	return Verified{Manifest: m, ChecksumsSHA256: sha256Hex(files.Checksums), Identity: "san:" + tag}, nil
}

func newDiscoverer(g *fakeGitHub, store *Store, v Verifier, n notify.Notifier, current string) *Discoverer {
	return &Discoverer{Store: store, Verifier: v, Notifier: n, BaseURL: g.srv.URL, Repo: "o/r",
		Current: current, HTTP: g.srv.Client(),
		Policy: func(context.Context) (Policy, error) { return DefaultPolicy(), nil }}
}

// TC4 (story 35.3): every release of the channel newer than the watermark is cataloged;
// new entries are verified on sight; a failure is a critical notification at once.
func TestCatalogVerifiesNewEntries(t *testing.T) {
	disableAutoInstall(t) // the "available" notice is a build's that does not install by itself
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			ctx := context.Background()
			g := newFakeGitHub(t)
			g.add("v1.3.0", false, true, "old\n")      // below the running version
			g.add("v1.5.0", false, true, "good\n")     // verified
			g.add("v1.6.0", false, true, "bad\n")      // wrong identity
			g.add("v1.7.0-rc.1", true, true, "beta\n") // beta: not on stable
			g.add("v1.4.5", false, false, "legacy\n")  // before 35.1: no release.json
			store := &Store{DB: be.db}
			v := &fakeVerifier{fail: map[string]*VerifyError{"v1.6.0": {Kind: KindIdentity, File: BundleFile, Detail: "SAN"}}}
			n := &recNotifier{}
			d := newDiscoverer(g, store, v, n, "1.4.0")
			res, err := d.Check(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if res.Seen != 3 || len(res.Verified) != 1 || res.Verified[0] != "1.5.0" || len(res.Failed) != 1 || res.Failed[0] != "1.6.0" {
				t.Fatalf("check: %+v", res)
			}
			cat, _ := store.Catalog(ctx)
			states := map[string]string{}
			for _, r := range cat {
				states[r.Version] = r.VerifyState
			}
			if states["1.5.0"] != VerifyVerified || states["1.6.0"] != VerifyFailed || states["1.4.5"] != VerifyUnverified ||
				states["1.3.0"] != "" || states["1.7.0-rc.1"] != "" {
				t.Fatalf("catalog states: %v", states)
			}
			r16, _ := store.Get(ctx, "1.6.0")
			if r16.VerifyError == nil || r16.VerifyError.Kind != KindIdentity || r16.VerifyError.File != BundleFile {
				t.Fatalf("1.6.0 error: %+v", r16.VerifyError)
			}
			bad := n.find(NoticeVerifyFailed)
			if bad == nil || bad.p.Severity != notify.SeverityError || bad.p.Params["version"] != "1.6.0" {
				t.Fatalf("no critical notice: %v", n.kinds())
			}
			// auto_install is off in phase A: the verified release is announced as available
			if n.find(NoticeAvailable) == nil {
				t.Fatalf("no 'update available' notice: %v", n.kinds())
			}
			// a second check verifies nothing again
			calls := len(v.calls)
			if _, err := d.Check(ctx); err != nil {
				t.Fatal(err)
			}
			if len(v.calls) != calls {
				t.Fatalf("re-verified on the second check: %v", v.calls)
			}
			// GitHub rate limit: the catalog is kept, the next check waits
			g.set(func() { g.limited = true })
			if _, err := d.Check(ctx); !errors.Is(err, ErrRateLimited) {
				t.Fatalf("rate limited: %v", err)
			}
			if _, err := d.Check(ctx); !errors.Is(err, ErrRateLimited) {
				t.Fatalf("retry-after not honoured: %v", err)
			}
			if cat2, _ := store.Catalog(ctx); len(cat2) != len(cat) {
				t.Fatalf("catalog changed on a failed check")
			}
		})
	}
}

// A release a home looks at before its workflow attached release.json and the bundle (the
// release exists minutes earlier) is verified once they are there, not passed by forever;
// one that still has none is not downloaded again.
func TestCatalogVerifiesALateManifest(t *testing.T) {
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			ctx := context.Background()
			g := newFakeGitHub(t)
			g.add("v1.5.0", false, false, "good\n") // checksums.txt only, so far
			store := &Store{DB: be.db}
			v := &fakeVerifier{}
			d := newDiscoverer(g, store, v, &recNotifier{}, "1.4.0")
			if _, err := d.Check(ctx); err != nil {
				t.Fatal(err)
			}
			r, _ := store.Get(ctx, "1.5.0")
			if r.VerifyState != VerifyUnverified || r.VerifyError == nil || r.VerifyError.Kind != KindNoManifest {
				t.Fatalf("first look: %+v %+v", r.VerifyState, r.VerifyError)
			}
			if _, err := d.Check(ctx); err != nil {
				t.Fatal(err)
			}
			if len(v.calls) != 0 {
				t.Fatalf("verified without its files: %v", v.calls)
			}
			// the workflow attaches release.json and the bundle
			g.set(func() {
				g.releases = nil
			})
			g.add("v1.5.0", false, true, "good\n")
			res, err := d.Check(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Verified) != 1 || res.Verified[0] != "1.5.0" {
				t.Fatalf("not verified once its files are there: %+v (calls %v)", res, v.calls)
			}
			cat, _ := store.Catalog(ctx)
			if c := ChooseTarget(cat, DefaultPolicy(), "1.4.0", ""); c.Target == nil || c.Target.Version != "1.5.0" {
				t.Fatalf("not a candidate: %+v", c)
			}
		})
	}
}

// EC4: the same version re-published with another checksums.txt is re-verified (not
// installed) or, once installed, refused as a replay.
func TestCatalogRetag(t *testing.T) {
	ctx := context.Background()
	db := sqliteDB(t)
	g := newFakeGitHub(t)
	g.add("v1.5.0", false, true, "one\n")
	g.add("v1.6.0", false, true, "one\n")
	store := &Store{DB: db}
	v := &fakeVerifier{}
	n := &recNotifier{}
	d := newDiscoverer(g, store, v, n, "1.4.0")
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RaiseWatermark(ctx, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	d.Current = "1.5.0"
	g.set(func() {
		g.files["v1.5.0/"+ChecksumsFile] = []byte("two\n")
		g.files["v1.6.0/"+ChecksumsFile] = []byte("two\n")
	})
	before := len(v.calls)
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	r15, _ := store.Get(ctx, "1.5.0")
	if r15.VerifyError == nil || r15.VerifyError.Kind != KindReplay || n.find(NoticeReplay) == nil {
		t.Fatalf("installed re-tag not refused: %+v %v", r15.VerifyError, n.kinds())
	}
	r16, _ := store.Get(ctx, "1.6.0")
	if r16.VerifyState != VerifyVerified || r16.ChecksumsSHA256 != sha256Hex([]byte("two\n")) || len(v.calls) != before+1 {
		t.Fatalf("re-tag not re-verified: %+v calls=%v", r16, v.calls)
	}
}

// TC5 (story 35.3 AC6): just before a run the release is re-read; gone, turned
// prerelease on stable, or another checksums.txt -> withdrawn, not installed, notified.
func TestRecheckWithdrawn(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		mutate func(g *fakeGitHub)
		want   string
	}{
		{"still good", func(*fakeGitHub) {}, ""},
		{"gone", func(g *fakeGitHub) { g.releases = nil }, WithdrawnGone},
		{"turned prerelease", func(g *fakeGitHub) { g.releases[0]["prerelease"] = true }, WithdrawnPrerelease},
		{"checksums changed", func(g *fakeGitHub) { g.files["v1.5.0/"+ChecksumsFile] = []byte("other\n") }, WithdrawnChecksums},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := sqliteDB(t)
			g := newFakeGitHub(t)
			g.add("v1.5.0", false, true, "good\n")
			store := &Store{DB: db}
			n := &recNotifier{}
			d := newDiscoverer(g, store, &fakeVerifier{}, n, "1.4.0")
			if _, err := d.Check(ctx); err != nil {
				t.Fatal(err)
			}
			r, _ := store.Get(ctx, "1.5.0")
			g.set(func() { c.mutate(g) })
			got, err := d.Recheck(ctx, r, ChannelStable)
			if err != nil || got != c.want {
				t.Fatalf("Recheck = %q, %v; want %q", got, err, c.want)
			}
			r, _ = store.Get(ctx, "1.5.0")
			if (r.WithdrawnAt != nil) != (c.want != "") {
				t.Fatalf("withdrawn_at = %v", r.WithdrawnAt)
			}
			if c.want != "" && n.find(NoticeReleaseWithdrew) == nil {
				t.Fatalf("not notified: %v", n.kinds())
			}
			if c.want != "" {
				if ch := ChooseTarget([]Release{r}, DefaultPolicy(), "1.4.0", ""); ch.Target != nil {
					t.Fatal("a withdrawn release was chosen")
				}
			}
		})
	}
}

// AC10: a check that has not succeeded for 14 days warns (a freeze hint).
func TestCheckStaleWarns(t *testing.T) {
	ctx := context.Background()
	db := sqliteDB(t)
	g := newFakeGitHub(t)
	store := &Store{DB: db}
	n := &recNotifier{}
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	d := newDiscoverer(g, store, &fakeVerifier{}, n, "1.4.0")
	d.Now = func() time.Time { return now }
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	g.set(func() { g.srv.Config.Handler = http.NotFoundHandler() })
	now = now.Add(13 * 24 * time.Hour)
	_, _ = d.Check(ctx)
	if n.find(NoticeCheckStale) != nil {
		t.Fatal("warned after 13 days")
	}
	now = now.Add(2 * 24 * time.Hour)
	_, _ = d.Check(ctx)
	if w := n.find(NoticeCheckStale); w == nil || w.p.Severity != notify.SeverityWarn {
		t.Fatalf("no stale warning: %v", n.kinds())
	}
}

// A release's assets are the pinned repository's own: a download named elsewhere (another
// host, another repository) is dropped at discovery, so no machine ever fetches it.
func TestPinnedAssets(t *testing.T) {
	d := &Discoverer{BaseURL: "https://api.github.com", Repo: "Hamza-Labs-Core/Maktaba"}
	r := Release{Tag: "v1.5.0", Assets: []Asset{
		{Name: "release.json", URL: "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.5.0/release.json"},
		{Name: "checksums.txt", URL: "https://github.com/hamza-labs-core/maktaba/releases/download/v1.5.0/checksums.txt"},
		{Name: "api.json", URL: "https://api.github.com/repos/Hamza-Labs-Core/Maktaba/releases/assets/42"},
		{Name: "evil.tar.gz", URL: "https://evil.example/Hamza-Labs-Core/Maktaba/releases/download/v1.5.0/evil.tar.gz"},
		{Name: "fork.tar.gz", URL: "https://github.com/someone/Maktaba/releases/download/v1.5.0/fork.tar.gz"},
		{Name: "plain.tar.gz", URL: "http://github.com/Hamza-Labs-Core/Maktaba/releases/download/v1.5.0/plain.tar.gz"},
		{Name: "climb.tar.gz", URL: "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/../../../evil/x"},
	}}
	var kept []string
	for _, a := range d.pinnedAssets(r) {
		kept = append(kept, a.Name)
	}
	if strings.Join(kept, ",") != "release.json,checksums.txt,api.json" {
		t.Fatalf("kept %v", kept)
	}
}
