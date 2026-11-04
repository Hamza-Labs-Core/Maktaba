package packs

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const (
	tVersion  = "0.4.2"
	tPlatform = "linux-amd64"
	tRepo     = "Hamza-Labs-Core/Maktaba"
	// the catalog's linux whisper-turbo source
	tWhisperSrc  = "hf:dropbox-dash/faster-whisper-large-v3-turbo@0a363e9161cbc7ed1431c9597a8ceaf0c4f78fcf"
	tWhisperRepo = "dropbox-dash/faster-whisper-large-v3-turbo"
	tWhisperRev  = "0a363e9161cbc7ed1431c9597a8ceaf0c4f78fcf"
)

func hexOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fixture is a signed 0.4.2 release with a runtime.ml pack and the whisper-turbo model in
// its index, served by a fake GitHub and Hugging Face, and a Manager over a fresh state.
type fixture struct {
	t      *testing.T
	ss     *releaseverifytest.Sigstore
	tm     root.TrustedMaterial
	rel    *releaseverifytest.FakeRelease
	index  rv.ComponentsIndex
	pack   []byte
	model  map[string][]byte
	srv    *httptest.Server
	served map[string][]byte // what the server answers (a test may change it)

	mu       sync.Mutex
	requests []string // path + " " + Range of each request
	cutAt    int64    // > 0: the next pack answer stops after this many bytes

	m    *Manager
	root string
	hub  string
	log  bytes.Buffer
}

type fixtureOpts struct {
	pack     []byte                    // a pack's bytes (nil: built from a small tree)
	noIndex  bool                      // a release from before 51.15
	edit     func(*rv.ComponentsIndex) // changes to the index before signing
	modelAlt map[string][]byte         // the model files the index lists (nil: two small ones)
	extra    map[string][]byte         // release files by name (a pack of its own)
}

func buildPack(t *testing.T) ([]byte, componentpack.Manifest) {
	t.Helper()
	p, man := buildPackOf(t, "runtime.ml", map[string]string{
		"wheels/index.json":              `{"wheels":[]}`,
		"wheels/torch-2.4.0-py3-any.whl": "not really torch",
		"constraints.txt":                "torch==2.4.0 --hash=sha256:" + strings.Repeat("ab", 32) + "\n",
	})
	return p.raw, man
}

// builtPack is a pack's bytes and its manifest.
type builtPack struct {
	raw []byte
	man componentpack.Manifest
}

// buildPackOf builds id's pack of files (path: content).
func buildPackOf(t *testing.T, id string, files map[string]string) (builtPack, componentpack.Manifest) {
	t.Helper()
	tree := t.TempDir()
	for p, b := range files {
		full := filepath.Join(tree, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	b, err := componentpack.Build(componentpack.Options{Dir: tree, ID: id, Version: tVersion, Platform: tPlatform,
		ModTime: time.Unix(1_800_000_000, 0), Out: out})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, b.Name))
	if err != nil {
		t.Fatal(err)
	}
	return builtPack{raw: raw, man: b.Manifest}, b.Manifest
}

func newFixture(t *testing.T, o fixtureOpts) *fixture {
	t.Helper()
	ss, err := releaseverifytest.New()
	if err != nil {
		t.Fatal(err)
	}
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ss: ss, tm: tm, served: map[string][]byte{}}
	man := componentpack.Manifest{UnpackedBytes: 1}
	if o.pack != nil {
		f.pack = o.pack
		var zr *zstd.Decoder
		zr, _ = zstd.NewReader(bytes.NewReader(o.pack))
		tr := tar.NewReader(zr)
		if _, err := tr.Next(); err == nil {
			_ = json.NewDecoder(tr).Decode(&man)
		}
		zr.Close()
	} else {
		f.pack, man = buildPack(t)
	}
	f.model = o.modelAlt
	if f.model == nil {
		f.model = map[string][]byte{"config.json": []byte(`{"model":"turbo"}`), "sub/model.bin": bytes.Repeat([]byte("w"), 5000)}
	}
	var mfiles []rv.IndexFile
	for p, b := range f.model {
		mfiles = append(mfiles, rv.IndexFile{Path: p, SHA256: hexOf(b), Size: int64(len(b))})
	}
	pub := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	name := componentpack.FileName("runtime.ml", tVersion, tPlatform)
	f.index = rv.ComponentsIndex{Schema: 1, Version: tVersion, Commit: strings.Repeat("0a", 20), Published: pub,
		Expires: pub.Add(rv.IndexLifetime), Server: rv.ServerRange(tVersion),
		Packs: []rv.IndexPack{{ID: "runtime.ml", Platform: tPlatform, Name: name, SHA256: hexOf(f.pack),
			Size: int64(len(f.pack)), UnpackedBytes: man.UnpackedBytes, FilesSHA256: man.FilesSHA256, Requires: []string{}}},
		Models: []rv.IndexModel{{ID: "model.whisper-turbo", Source: tWhisperSrc, Platforms: []string{tPlatform}, Files: mfiles}},
	}
	if o.edit != nil {
		o.edit(&f.index)
	}
	ib, err := json.MarshalIndent(f.index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	ro := releaseverifytest.ReleaseOptions{Extra: map[string][]byte{}}
	for _, p := range f.index.Packs {
		ro.Extra[p.Name] = f.pack
		if b, ok := o.extra[p.Name]; ok {
			ro.Extra[p.Name] = b
		}
	}
	if !o.noIndex {
		ro.ComponentsIndex = ib
	}
	f.rel, err = ss.NewReleaseWith(tRepo, "v"+tVersion, "linux/amd64", ro)
	if err != nil {
		t.Fatal(err)
	}
	for n, b := range f.rel.Files {
		f.served[n] = b
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	state := t.TempDir()
	f.root = filepath.Join(state, "components")
	f.hub = filepath.Join(state, "models", "huggingface", "hub")
	id, _ := rv.NewIdentity("")
	net := NewNet(tRepo, f.srv.Client())
	net.Releases, net.HF = f.srv.URL+"/releases/download", f.srv.URL+"/hf"
	f.m = &Manager{Layout: Layout{Root: f.root}, Catalog: components.Default(), Version: tVersion, Platform: tPlatform,
		Net: net, Trusted: func(context.Context) (root.TrustedMaterial, error) { return tm, nil }, Identity: id,
		Watermark: rv.IndexWatermarkPath(state), HFHub: f.hub, Log: &f.log}
	return f
}

func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path+" "+r.Header.Get("Range"))
	cut := f.cutAt
	f.mu.Unlock()
	var b []byte
	var ok bool
	switch {
	case strings.HasPrefix(r.URL.Path, "/releases/download/v"+tVersion+"/"):
		b, ok = f.served[strings.TrimPrefix(r.URL.Path, "/releases/download/v"+tVersion+"/")]
	case strings.HasPrefix(r.URL.Path, "/hf/"+tWhisperRepo+"/resolve/"+tWhisperRev+"/"):
		b, ok = f.model[strings.TrimPrefix(r.URL.Path, "/hf/"+tWhisperRepo+"/resolve/"+tWhisperRev+"/")]
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if cut > 0 && strings.HasSuffix(r.URL.Path, ".tar.zst") {
		f.mu.Lock()
		f.cutAt = 0
		f.mu.Unlock()
		// the connection drops after cut bytes of a whole answer
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b[:cut])
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		panic(http.ErrAbortHandler)
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}

func (f *fixture) reqs(sub string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		if strings.Contains(r, sub) {
			out = append(out, r)
		}
	}
	return out
}

// exists reports a path is there.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// logString is the manager's log so far, read under the lock its writes take.
func (f *fixture) logString() string {
	f.m.logMu.Lock()
	defer f.m.logMu.Unlock()
	return f.log.String()
}
