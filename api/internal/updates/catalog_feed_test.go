package updates

// The release feed as GitHub really serves it (tests/fixtures/github-releases/page-1.json.gz,
// recorded with record.sh: a home's own request, unauthenticated, ten releases of some 190
// assets each). Until 2026-11-24 the caps were tested only against lists made up in the
// test, at about 85% of the cap and never compared with the real answer; the real one grew
// past the cap and every home's check failed for over a day.

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// AssetGrowth is how much the newest releases' asset lists (and notes) may grow before a
// cap must change: the worst page the tests allow is listPerPage releases each this much
// larger than the largest recorded one, and it must still fit FeedHeadroom of the cap. From
// v0.3.150 to v0.3.158 the assets grew from 155 to 188 a release.
const AssetGrowth = 2.0

// realFeedPage is the recorded page, as GitHub answered it (read once for every test).
func realFeedPage(t testing.TB) []byte {
	t.Helper()
	realFeed.once.Do(loadRealFeed)
	if realFeed.err != nil {
		t.Fatal(realFeed.err)
	}
	return realFeed.page
}

// realReleases are the recorded page's releases, each its exact bytes.
func realReleases(t testing.TB) []json.RawMessage {
	t.Helper()
	realFeedPage(t)
	if len(realFeed.rels) != listPerPage {
		t.Fatalf("the recorded page has %d releases, want a full page of %d", len(realFeed.rels), listPerPage)
	}
	return realFeed.rels
}

var realFeed struct {
	once sync.Once
	page []byte
	rels []json.RawMessage
	err  error
}

func loadRealFeed() {
	_, here, _, _ := runtime.Caller(0)
	f, err := os.Open(filepath.Join(filepath.Dir(here), "..", "..", "..", "tests", "fixtures", "github-releases", "page-1.json.gz"))
	if err != nil {
		realFeed.err = err
		return
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		realFeed.err = err
		return
	}
	if realFeed.page, err = io.ReadAll(zr); err != nil {
		realFeed.err = err
		return
	}
	realFeed.err = json.Unmarshal(realFeed.page, &realFeed.rels)
}

// releaseSize is one release's size: in all, its assets', its largest asset's, and its
// asset count.
type releaseSize struct {
	tag                     string
	bytes, assets, maxAsset int
	n                       int
}

func sizeOf(t testing.TB, raw json.RawMessage) releaseSize {
	t.Helper()
	var r struct {
		Tag    string            `json:"tag_name"`
		Assets []json.RawMessage `json:"assets"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	s := releaseSize{tag: r.Tag, bytes: len(raw), n: len(r.Assets)}
	for _, a := range r.Assets {
		s.assets += len(a) + 1 // its comma
		s.maxAsset = max(s.maxAsset, len(a)+1)
	}
	return s
}

// The real page is read as a home reads it: every release, every asset, and the page's
// length counted.
func TestRealFeedPageReads(t *testing.T) {
	if testing.Short() {
		// reads the recorded 3.2 MB page (and more): past the unit tier's per-test cap under
		// -race on a loaded runner. Runs without -short (the integration tier)
		t.Skip("reads the real release feed's size; runs without -short (the integration tier)")
	}
	t.Parallel()
	page := realFeedPage(t)
	d := readDiscoverer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(page) })
	part, n, decodeErr, err := d.listPage(context.Background(), 1)
	if err != nil || decodeErr != nil {
		t.Fatal(err, decodeErr)
	}
	if n != int64(len(page)) || len(part) != listPerPage {
		t.Fatalf("read %d bytes and %d releases, want %d and %d", n, len(part), len(page), listPerPage)
	}
	for _, g := range part {
		if !ValidVersion(g.TagName) || len(g.Assets) < 150 || g.PublishedAt.IsZero() || g.HTMLURL == "" {
			t.Fatalf("%s read with %d assets", g.TagName, len(g.Assets))
		}
		r := g.release()
		if !hasSignedFiles(r) {
			t.Fatalf("%s: its signed files were not read", g.TagName)
		}
	}
	// the whole list: three pages of it
	rels, decodeErr, err := d.list(context.Background())
	if err != nil || decodeErr != nil || len(rels) != listPages*listPerPage {
		t.Fatalf("list: %d releases, %v, %v", len(rels), decodeErr, err)
	}
}

// The caps hold the real answers with room to spare: the recorded page and release
// within FeedHeadroom of their caps, and so the worst page the next releases can make
// (AssetGrowth times the largest recorded release, a full page of them).
func TestRealFeedHeadroom(t *testing.T) {
	t.Parallel()
	page := realFeedPage(t)
	var worst releaseSize
	for _, raw := range realReleases(t) {
		if s := sizeOf(t, raw); s.bytes > worst.bytes {
			worst = s
		}
	}
	room := func(limit int64) float64 { return FeedHeadroom * float64(limit) }
	if float64(len(page)) > room(listPageLimit) {
		t.Errorf("the recorded page is %d bytes, over %.0f%% of the page cap %d", len(page), 100*FeedHeadroom, listPageLimit)
	}
	if float64(worst.bytes) > room(tagReadLimit) {
		t.Errorf("the largest recorded release (%s) is %d bytes, over %.0f%% of the tag read's cap %d",
			worst.tag, worst.bytes, 100*FeedHeadroom, tagReadLimit)
	}
	// the worst release to come: its assets (and the rest: notes, names) grown by AssetGrowth
	grown := AssetGrowth * float64(worst.bytes)
	worstPage := float64(listPerPage)*grown + 2
	t.Logf("recorded: page %d bytes, largest release %s %d bytes (%d assets, %d bytes of them, the largest asset %d); "+
		"worst page at %.1fx: %.0f bytes, %.0f%% of %d; worst release %.0f bytes, %.0f%% of %d",
		len(page), worst.tag, worst.bytes, worst.n, worst.assets, worst.maxAsset, AssetGrowth,
		worstPage, 100*worstPage/float64(listPageLimit), listPageLimit, grown, 100*grown/float64(tagReadLimit), tagReadLimit)
	if worstPage > room(listPageLimit) {
		t.Errorf("a page of releases %.1fx the largest recorded one is %.0f bytes, over %.0f%% of the page cap %d: "+
			"raise listPageLimit or lower listPerPage", AssetGrowth, worstPage, 100*FeedHeadroom, listPageLimit)
	}
	if grown > room(tagReadLimit) {
		t.Errorf("a release %.1fx the largest recorded one is %.0f bytes, over %.0f%% of the tag read's cap %d",
			AssetGrowth, grown, 100*FeedHeadroom, tagReadLimit)
	}
	// what this test would have said of the caps before 2026-11-24: 30 a page under 8 MiB
	if old := 30 * float64(worst.bytes); old <= 8<<20 {
		t.Errorf("30 of the recorded releases are %.0f bytes, under the old 8 MiB: the incident's page should not fit", old)
	}
}

// feedServer serves a feed of real releases: the recorded page for every page of the list
// (pages, then an empty one), the first as /releases/latest and by its tag. Set
// page to change what the list serves.
type feedServer struct {
	srv  *httptest.Server
	page []byte
	rels []json.RawMessage
}

func newFeedServer(t *testing.T, repo string) *feedServer {
	t.Helper()
	f := &feedServer{page: realFeedPage(t), rels: realReleases(t)}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "/repos/" + repo + "/releases"
		switch p := r.URL.Path; {
		case p == base:
			if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") == "" {
				http.Error(w, "not the home's request", http.StatusBadRequest)
				return
			}
			_, _ = w.Write(f.page)
		case p == base+"/latest":
			_, _ = w.Write(f.rels[0])
		case strings.HasPrefix(p, base+"/tags/"):
			tag := strings.TrimPrefix(p, base+"/tags/")
			for _, raw := range f.rels {
				if sizeOf(t, raw).tag == tag {
					_, _ = w.Write(raw)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

const realRepo = "Hamza-Labs-Core/Maktaba"

func probeDiscoverer(f *feedServer) *Discoverer {
	return &Discoverer{BaseURL: f.srv.URL, Repo: realRepo, HTTP: f.srv.Client()}
}

// The feed check passes the real feed: every page and the tag read well inside their
// caps, and GitHub's latest release selectable with its signed files.
func TestProbeFeedReal(t *testing.T) {
	if testing.Short() {
		// reads the recorded 3.2 MB page (and more): past the unit tier's per-test cap under
		// -race on a loaded runner. Runs without -short (the integration tier)
		t.Skip("reads the real release feed's size; runs without -short (the integration tier)")
	}
	t.Parallel()
	f := newFeedServer(t, realRepo)
	rep := probeDiscoverer(f).ProbeFeed(context.Background(), "")
	latest := sizeOf(t, f.rels[0]).tag
	if !rep.OK() || rep.Latest != latest || rep.Target != latest || rep.Newest != NormalizeVersion(latest) {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.Pages) != listPages || rep.Pages[0].Bytes != int64(len(f.page)) || rep.Pages[0].Releases != listPerPage {
		t.Fatalf("pages %+v", rep.Pages)
	}
	if rep.Tag == nil || rep.Tag.Bytes != int64(len(f.rels[0])) || rep.Tag.Share() > FeedHeadroom {
		t.Fatalf("tag read %+v", rep.Tag)
	}
	// an expected tag that is there
	if rep := probeDiscoverer(f).ProbeFeed(context.Background(), sizeOf(t, f.rels[3]).tag); !rep.OK() {
		t.Fatalf("expect the fourth: %v", rep.Problems)
	}
}

// Each way a home could not read the feed, or soon could not, is a problem.
func TestProbeFeedProblems(t *testing.T) {
	if testing.Short() {
		// reads the recorded 3.2 MB page (and more): past the unit tier's per-test cap under
		// -race on a loaded runner. Runs without -short (the integration tier)
		t.Skip("reads the real release feed's size; runs without -short (the integration tier)")
	}
	t.Parallel()
	ctx := context.Background()
	t.Run("a page over the headroom", func(t *testing.T) {
		t.Parallel()
		f := newFeedServer(t, realRepo)
		// the real page with white space inside it: over 70% of the cap, still under it
		room := FeedHeadroom * float64(listPageLimit)
		pad := int(room) - len(f.page) + 1024
		f.page = append(append([]byte("["), bytes.Repeat([]byte(" "), pad)...), f.page[1:]...)
		rep := probeDiscoverer(f).ProbeFeed(ctx, "")
		if rep.OK() || !strings.Contains(strings.Join(rep.Problems, "\n"), "page 1 of the release list is") {
			t.Fatalf("problems %v", rep.Problems)
		}
	})
	t.Run("a page over the cap", func(t *testing.T) {
		t.Parallel()
		f := newFeedServer(t, realRepo)
		f.page = append(append([]byte("["), bytes.Repeat([]byte(" "), listPageLimit)...), f.page[1:]...)
		rep := probeDiscoverer(f).ProbeFeed(ctx, "")
		if rep.OK() || rep.Pages[0].Code != CheckFailTooLarge || !strings.Contains(rep.Problems[0], "cannot be read") {
			t.Fatalf("pages %+v, problems %v", rep.Pages, rep.Problems)
		}
	})
	t.Run("a release the home does not list", func(t *testing.T) {
		t.Parallel()
		f := newFeedServer(t, realRepo)
		rep := probeDiscoverer(f).ProbeFeed(ctx, "v9.9.9")
		if rep.OK() || !strings.Contains(rep.Problems[0], "v9.9.9 is not in the") {
			t.Fatalf("problems %v", rep.Problems)
		}
	})
	t.Run("a release without its signed files", func(t *testing.T) {
		t.Parallel()
		f := newFeedServer(t, "o/r") // the recorded assets are another repository's: not pinned
		rep := (&Discoverer{BaseURL: f.srv.URL, Repo: "o/r", HTTP: f.srv.Client()}).ProbeFeed(ctx, "")
		if rep.OK() || !strings.Contains(strings.Join(rep.Problems, "\n"), "without its signed files") {
			t.Fatalf("problems %v", rep.Problems)
		}
	})
	t.Run("a prerelease on the stable channel's latest", func(t *testing.T) {
		t.Parallel()
		f := newFeedServer(t, realRepo)
		first := sizeOf(t, f.rels[0]).tag
		f.page = bytes.Replace(f.page, []byte(`"prerelease":false`), []byte(`"prerelease":true`), 1)
		rep := probeDiscoverer(f).ProbeFeed(ctx, first)
		if rep.OK() || !strings.Contains(strings.Join(rep.Problems, "\n"), "passes over it: a prerelease") {
			t.Fatalf("problems %v", rep.Problems)
		}
	})
}

// A failed check is not log-only: each one is counted, with since when and on what, and
// from the FailingAfter-th in a row the admins are told; a check that works clears it.
func TestCheckFailingStreak(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := sqliteDB(t)
	g := newFakeGitHub(t)
	store := &Store{DB: db}
	n := &recNotifier{}
	now := time.Date(2026, 11, 23, 11, 2, 0, 0, time.UTC)
	d := newDiscoverer(g, store, &fakeVerifier{}, n, "1.4.0")
	d.Now = func() time.Time { return now }
	// GitHub answering an error: the streak is the same whatever the check failed on (what
	// each failure is called: TestCheckErrorCode; a list over its cap: TestProbeFeedProblems)
	failing := func() {
		g.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unicorn", http.StatusBadGateway)
		})
	}
	g.set(failing)
	first := now
	if _, err := d.Check(ctx); err == nil {
		t.Fatal("a check that failed answered no error")
	}
	st := store.LoadState(ctx)
	if st.Failures != 1 || st.LastCheckCode != CheckFailHTTP || st.FailingSince == nil || !st.FailingSince.Equal(first) ||
		!strings.Contains(st.LastCheckErr, "HTTP 502") {
		t.Fatalf("after one failure: %+v", st)
	}
	if n.find(NoticeCheckFailing) != nil {
		t.Fatal("raised after one failure")
	}
	now = now.Add(CheckInterval)
	_, _ = d.Check(ctx)
	st = store.LoadState(ctx)
	if st.Failures != 2 || !st.FailingSince.Equal(first) {
		t.Fatalf("after two failures: %+v", st)
	}
	w := n.find(NoticeCheckFailing)
	if w == nil || w.p.Params["code"] != CheckFailHTTP || w.p.Params["since"] != first.Format(time.RFC3339) || w.p.Link != "/admin/updates" {
		t.Fatalf("not raised after two failures: %v", n.kinds())
	}
	// the Updates page reads it
	svc := &Service{Store: store, Current: "1.4.0", Loc: time.UTC, Now: func() time.Time { return now }}
	ov, err := svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ov.CheckFailures != 2 || ov.LastCheckCode != CheckFailHTTP || ov.CheckFailingSince == nil || ov.LastCheckError == "" {
		t.Fatalf("overview %+v", ov)
	}
	// a working check clears the streak
	g.set(func() { g.srv.Config.Handler = http.HandlerFunc(g.serve) })
	now = now.Add(CheckInterval)
	if _, err := d.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if st = store.LoadState(ctx); st.Failures != 0 || st.FailingSince != nil || st.LastCheckCode != "" || st.LastCheckErr != "" {
		t.Fatalf("after a check that worked: %+v", st)
	}
}

func TestCheckErrorCode(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		err  error
		want string
	}{
		{&tooLargeError{url: "u", limit: 1}, CheckFailTooLarge},
		{fmt.Errorf("list: %w", &tooLargeError{url: "u", limit: 1}), CheckFailTooLarge},
		{fmt.Errorf("%w for 1m", ErrRateLimited), CheckFailRateLimited},
		{&httpStatusError{url: "u", code: 502}, CheckFailHTTP},
		{io.ErrUnexpectedEOF, CheckFailUnreachable},
	} {
		if got := CheckErrorCode(c.err); got != c.want {
			t.Errorf("CheckErrorCode(%v) = %s, want %s", c.err, got, c.want)
		}
	}
}
