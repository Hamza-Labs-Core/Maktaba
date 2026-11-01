package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

func TestNewer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.2.0", "1.1.9", true},
		{"v1.2.0", "1.2.0", false},
		{"1.2.1", "1.2.0", true},
		{"1.2.0", "1.2.0", false},
		{"1.2.0-rc1", "1.2.0", false},
		{"2.0.0", "1.9.9", true},
		{"dev", "1.0.0", false}, // unparseable never beats a release
		{"1.0.0", "dev", true},
		{"1.10.0", "1.9.0", true}, // numeric, not lexical
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

const repo = releaseverify.DefaultRepo

type rig struct {
	c     *Client
	self  string
	rels  map[string]*releaseverifytest.FakeRelease
	mu    sync.Mutex
	paths []string
}

// newRig serves GitHub releases of repo: each FakeRelease signed by signer.
func newRig(t *testing.T, signer string, tags ...string) *rig {
	t.Helper()
	ss := releaseverifytest.MustNew()
	tr, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{rels: map[string]*releaseverifytest.FakeRelease{}}
	for _, tag := range tags {
		r.rels[tag] = ss.MustRelease(signer, tag, releaseverify.Platform())
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.paths = append(r.paths, req.URL.Path)
		r.mu.Unlock()
		mux.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	gh := func(rel *releaseverifytest.FakeRelease) ghRelease {
		g := ghRelease{TagName: rel.Tag, Prerelease: strings.Contains(rel.Tag, "-")}
		for name := range rel.Files {
			g.Assets = append(g.Assets, struct {
				Name string `json:"name"`
				URL  string `json:"browser_download_url"`
			}{name, srv.URL + "/dl/" + rel.Tag + "/" + name})
		}
		return g
	}
	mux.HandleFunc("/repos/"+repo+"/releases", func(w http.ResponseWriter, _ *http.Request) {
		var out []ghRelease
		for _, rel := range r.rels {
			out = append(out, gh(rel))
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/repos/"+repo+"/releases/tags/", func(w http.ResponseWriter, req *http.Request) {
		rel, ok := r.rels[strings.TrimPrefix(req.URL.Path, "/repos/"+repo+"/releases/tags/")]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_ = json.NewEncoder(w).Encode(gh(rel))
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, req *http.Request) {
		tag, name, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/dl/"), "/")
		rel, ok := r.rels[tag]
		if !ok || rel.Files[name] == nil {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(rel.Files[name])
	})
	id, _ := releaseverify.NewIdentity(repo)
	r.c = New(Options{Identity: id, APIBase: srv.URL,
		Trusted: func(context.Context) (root.TrustedMaterial, error) { return tr, nil }})
	r.self = filepath.Join(t.TempDir(), "maktaba-server")
	if err := os.WriteFile(r.self, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

func serverBin(rel *releaseverifytest.FakeRelease) string {
	for n, b := range rel.Binaries {
		if strings.HasPrefix(n, "maktaba-server") {
			return string(b)
		}
	}
	return ""
}

// TC8: the CLI reads GitHub Releases only and installs only what
// releaseverify accepted.
func TestCLIUpdateGitHubOnly(t *testing.T) {
	r := newRig(t, repo, "v1.4.0", "v1.5.0", "v1.6.0-rc.1")
	ctx := context.Background()
	rel, err := r.c.Latest(ctx)
	if err != nil || rel.Tag != "v1.5.0" {
		t.Fatalf("Latest = %+v, %v (stable skips the rc)", rel, err)
	}
	p, err := r.c.Prepare(ctx, rel, r.self)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if b, _ := os.ReadFile(r.self); string(b) != "OLD" {
		t.Fatal("Prepare wrote the binary")
	}
	if err := p.Install(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(r.self); string(b) != serverBin(r.rels["v1.5.0"]) {
		t.Fatalf("installed %q", b)
	}
	if b, _ := os.ReadFile(r.self + ".bak"); string(b) != "OLD" {
		t.Fatalf(".bak = %q", b)
	}
	for _, p := range r.paths {
		if !strings.HasPrefix(p, "/repos/"+repo+"/releases") && !strings.HasPrefix(p, "/dl/") {
			t.Fatalf("unexpected request %s", p)
		}
	}
	// The old feed is gone from the CLI.
	for _, f := range []string{"selfupdate.go", "../../cmd_update.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "releases.maktaba.tv") || strings.Contains(string(b), "manifest-url") {
			t.Fatalf("%s still references the old manifest feed", f)
		}
	}

	// Downgrade: v1.4.0 is below the watermark now.
	old, err := r.c.ByTag(ctx, "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.Prepare(ctx, old, r.self); !errors.Is(err, releaseverify.ErrDowngrade) {
		t.Fatalf("downgrade: %v", err)
	}
	// Rollback is the explicit way back, and keeps the watermark.
	if err := Rollback(r.self); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(r.self); string(b) != "OLD" {
		t.Fatalf("rollback = %q", b)
	}
	if w, _ := (releaseverify.WatermarkFile{Path: releaseverify.FlatWatermarkPath(r.self)}).Load(); w.Version != "1.5.0" {
		t.Fatalf("watermark after rollback = %q", w.Version)
	}
}

func TestCLIUpdateRefusesFork(t *testing.T) {
	r := newRig(t, "evil-fork/Maktaba", "v1.5.0")
	rel, err := r.c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.Prepare(context.Background(), rel, r.self); !errors.Is(err, releaseverify.ErrIdentity) {
		t.Fatalf("fork-signed release: %v", err)
	}
}

func TestCLIUpdateBetaChannel(t *testing.T) {
	r := newRig(t, repo, "v1.5.0", "v1.6.0-rc.1")
	r.c.o.Channel = "beta"
	rel, err := r.c.Latest(context.Background())
	if err != nil || rel.Tag != "v1.6.0-rc.1" {
		t.Fatalf("beta Latest = %+v, %v", rel, err)
	}
	if _, err := r.c.ByTag(context.Background(), "v9.9.9"); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("missing tag: %v", err)
	}
}
