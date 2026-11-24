package releasefeed

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// the real v0.3.161, recorded with `gh release view` and its three signed files
const real161 = "../../../shared/go/releaseverify/testdata/feed/v0.3.161"

const repo = releaseverify.DefaultRepo

// generated writes the feed of the real v0.3.161 (and a draft, and a release without
// signed files, both left out) into a new folder.
func generated(t *testing.T, mutate func(*Generate)) (string, error) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(real161, "release-view.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v releaseverify.GHReleaseView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	signed := t.TempDir()
	_ = os.MkdirAll(filepath.Join(signed, v.TagName), 0o755)
	for _, n := range []string{releaseverify.ChecksumsFile, releaseverify.BundleFile, releaseverify.ManifestFile} {
		f, _ := os.ReadFile(filepath.Join(real161, n))
		_ = os.WriteFile(filepath.Join(signed, v.TagName, n), f, 0o644)
	}
	draft := v
	draft.TagName, draft.IsDraft = "v0.3.162", true
	old := v
	old.TagName = "v0.1.0"
	out := t.TempDir()
	g := Generate{Views: []releaseverify.GHReleaseView{v, draft, old}, SignedDir: signed, OutDir: out, Repo: repo,
		Require: "v0.3.161", Now: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), Log: &bytes.Buffer{}}
	if mutate != nil {
		mutate(&g)
	}
	return out, g.Run()
}

func TestGenerateFromTheRealRelease(t *testing.T) {
	out, err := generated(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range []string{"stable", "beta"} {
		var ix releaseverify.FeedIndex
		b, err := os.ReadFile(filepath.Join(out, ch, "index.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &ix); err != nil {
			t.Fatal(err)
		}
		if len(ix.Platforms) != 5 {
			t.Fatalf("%s index: %+v", ch, ix.Platforms)
		}
		for _, p := range ix.Platforms {
			f, err := os.ReadFile(filepath.Join(out, ch, p.File))
			if err != nil {
				t.Fatal(err)
			}
			if p.SHA256 != sha256Hex(f) || len(p.Versions) != 1 || p.Versions[0] != "0.3.161" {
				t.Fatalf("%s/%s: %+v", ch, p.Platform, p)
			}
			if bytes.Contains(f, []byte("v0.3.162")) || bytes.Contains(f, []byte("v0.1.0")) {
				t.Fatalf("%s/%s lists the draft or the unsigned release", ch, p.File)
			}
		}
	}
}

func TestGenerateRequires(t *testing.T) {
	if _, err := generated(t, func(g *Generate) { g.Require = "v0.3.162" }); err == nil ||
		!strings.Contains(err.Error(), "not a published release") {
		t.Errorf("a draft required: %v", err)
	}
	if _, err := generated(t, func(g *Generate) { g.Withdrawn = map[string]string{"0.3.161": "pulled"} }); err == nil ||
		!strings.Contains(err.Error(), "does not list v0.3.161") {
		t.Errorf("a pulled release required: %v", err)
	}
	// its signature checked against another root: it does not verify, and it is required
	ss := releaseverifytest.MustNew()
	tr, _ := ss.TrustedRoot()
	if _, err := generated(t, func(g *Generate) { g.Trusted = tr }); err == nil ||
		!strings.Contains(err.Error(), "does not verify") {
		t.Errorf("an unverified release required: %v", err)
	}
	// not required: left out with a warning
	out, err := generated(t, func(g *Generate) { g.Trusted = tr; g.Require = "" })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(out, "stable", "index.json"))
	if !bytes.Contains(b, []byte(`"platforms":[]`)) {
		t.Errorf("an unverified release is in the feed: %s", b)
	}
}

func TestParseWithdrawn(t *testing.T) {
	w, err := ParseWithdrawn(`{"v0.3.158": "its migration timed out", "0.3.157": ""}`)
	if err != nil || w["0.3.158"] != "its migration timed out" || w["0.3.157"] != "withdrawn" {
		t.Fatalf("%v %v", w, err)
	}
	for _, bad := range []string{`[1]`, `{"latest": "x"}`} {
		if _, err := ParseWithdrawn(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if w, err := ParseWithdrawn(" "); err != nil || len(w) != 0 {
		t.Fatal(w, err)
	}
}

// sign signs every file of out as update-feed.yml would (cosign sign-blob), with the
// fake Sigstore.
func sign(t *testing.T, ss *releaseverifytest.Sigstore, out string, o releaseverifytest.SignOptions) {
	t.Helper()
	for _, ch := range []string{"stable", "beta"} {
		ents, _ := os.ReadDir(filepath.Join(out, ch))
		for _, e := range ents {
			p := filepath.Join(out, ch, e.Name())
			b, _ := os.ReadFile(p)
			bun, err := ss.SignBlob(b, o)
			if err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(p+releaseverify.FeedBundleSuffix, bun, 0o644)
		}
	}
}

func TestVerifyDir(t *testing.T) {
	out, err := generated(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	ss := releaseverifytest.MustNew()
	tr, _ := ss.TrustedRoot()
	if _, err := VerifyDir(out, repo, tr); err == nil {
		t.Fatal("an unsigned feed verified")
	}
	sign(t, ss, out, releaseverifytest.GitHubFeed(repo, "workflow_run"))
	files, err := VerifyDir(out, repo, tr)
	if err != nil || len(files) != 12 {
		t.Fatalf("%v %v", files, err)
	}
	// signed by the release workflow instead: refused
	sign(t, ss, out, releaseverifytest.GitHubRelease(repo, "v0.3.161"))
	if _, err := VerifyDir(out, repo, tr); err == nil {
		t.Fatal("a feed signed by release.yml verified")
	}
}

func TestLiveCheck(t *testing.T) {
	out, err := generated(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	ss := releaseverifytest.MustNew()
	tr, _ := ss.TrustedRoot()
	sign(t, ss, out, releaseverifytest.GitHubFeed(repo, "workflow_run"))
	// an older feed: v0.3.161 pulled in it
	stale, err := generated(t, func(g *Generate) { g.Require = ""; g.Withdrawn = map[string]string{"0.3.161": "pulled"} })
	if err != nil {
		t.Fatal(err)
	}
	sign(t, ss, stale, releaseverifytest.GitHubFeed(repo, "workflow_run"))
	var hidden string
	serve := out
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/updates/v1/")
		if hidden != "" && strings.HasPrefix(p, hidden) {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(serve, filepath.FromSlash(p)))
	}))
	defer srv.Close()
	l := Live{URL: srv.URL + "/updates/v1", Expect: out, Tag: "v0.3.161", Repo: repo, Trusted: tr}
	if err := l.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	// one platform not deployed
	hidden = "stable/windows-amd64.json"
	if err := l.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "stable/windows-amd64.json") {
		t.Fatalf("a missing platform: %v", err)
	}
	hidden = ""
	// the live feed is an older one: it does not list the release
	serve = stale
	if err := l.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "does not list v0.3.161") {
		t.Fatalf("a stale live feed: %v", err)
	}
	serve = out
	l.Tag = "v0.3.162"
	if err := l.Check(context.Background()); err != nil {
		t.Fatalf("no expected file lists v0.3.162, so nothing is required: %v", err)
	}
	l.Tag = "v0.3.161"
	// the live feed's signature is not the release feed's
	l.Trusted = nil
	if err := l.Check(context.Background()); err == nil {
		t.Fatal("a feed the embedded root does not trust passed")
	}
}
