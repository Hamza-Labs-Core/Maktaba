package packs

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// Story 51.16 on a Core Mac (no Ollama in the app): the runtime.ollama pack and a
// model.llm.* model install through the Manager. The model's blobs come from Ollama's
// registry, each checked against the signed index's pins, then its manifest, checked
// against the pinned digest; the store is Ollama's own layout, so the server started
// afterwards has the model. A blob or a manifest that is not the pinned one installs
// nothing.

const tLLM = "model.llm.qwen2_5-7b"

// llmFixture is the fixture with the catalog's LLM pinned to a fake model (its manifest
// and two blobs) served by a fake Ollama registry, and a runtime.ollama pack in the index.
type llmFixture struct {
	*fixture
	manifest []byte
	blobs    map[string][]byte // by digest
	src      string
	models   string
	served   atomic.Int32 // the registry's answers
	bad      atomic.Bool  // the registry answers a manifest that is not the pinned one
}

func newLLMFixture(t *testing.T) *llmFixture {
	t.Helper()
	l := &llmFixture{blobs: map[string][]byte{}}
	weights, cfg := bytes.Repeat([]byte("q"), 6000), []byte(`{"model_format":"gguf"}`)
	l.blobs["sha256:"+hexOf(weights)], l.blobs["sha256:"+hexOf(cfg)] = weights, cfg
	l.manifest = []byte(fmt.Sprintf(`{"schemaVersion":2,"config":{"digest":"sha256:%s","size":%d},`+
		`"layers":[{"digest":"sha256:%s","size":%d}]}`, hexOf(cfg), len(cfg), hexOf(weights), len(weights)))
	l.src = "ollama:qwen2.5:7b@sha256:" + hexOf(l.manifest)
	var files []rv.IndexFile
	var toml strings.Builder
	for d, b := range l.blobs {
		h := strings.TrimPrefix(d, "sha256:")
		files = append(files, rv.IndexFile{Path: "blobs/sha256-" + h, SHA256: h, Size: int64(len(b))})
		fmt.Fprintf(&toml, "  { path = \"blobs/sha256-%s\", sha256 = \"%s\", size = %d },\n", h, h, len(b))
	}
	// the catalog's LLM entry, pinned to the fake model, on linux too (the fixture's)
	raw := string(components.Raw())
	re := regexp.MustCompile(`(?s)(id         = "model.llm.qwen2_5-7b".*?)source     = "[^"]*"(.*?)files      = \[\n.*?\n\]`)
	if !re.MatchString(raw) {
		t.Fatal("the catalog's LLM entry is not where the test looks")
	}
	raw = re.ReplaceAllString(raw, "${1}source     = \""+l.src+"\"${2}files      = [\n"+toml.String()+"]")
	cat, err := components.Load([]byte(raw))
	if err != nil {
		t.Fatalf("test catalog: %v", err)
	}
	pack, _ := buildPackOf(t, "runtime.ollama", map[string]string{"ollama/ollama": "#!/bin/sh\n", "ollama/llama-server": "#!/bin/sh\n"})
	l.fixture = newFixture(t, fixtureOpts{edit: func(idx *rv.ComponentsIndex) {
		idx.Models = append(idx.Models, rv.IndexModel{ID: tLLM, Source: l.src, Platforms: []string{tPlatform}, Files: files})
		idx.Packs = append(idx.Packs, rv.IndexPack{ID: "runtime.ollama", Platform: tPlatform,
			Name: "runtime.ollama-" + tVersion + "-" + tPlatform + ".tar.zst", SHA256: hexOf(pack.raw),
			Size: int64(len(pack.raw)), UnpackedBytes: pack.man.UnpackedBytes, FilesSHA256: pack.man.FilesSHA256,
			Requires: []string{}})
	}, extra: map[string][]byte{"runtime.ollama-" + tVersion + "-" + tPlatform + ".tar.zst": pack.raw}})
	l.m.Catalog = cat
	l.models = filepath.Join(filepath.Dir(l.root), "models", "ollama")
	l.m.OllamaModels = l.models
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.served.Add(1)
		switch {
		case r.URL.Path == "/v2/library/qwen2.5/manifests/sha256:"+hexOf(l.manifest) &&
			r.Header.Get("Accept") == "application/vnd.docker.distribution.manifest.v2+json":
			b := l.manifest
			if l.bad.Load() {
				b = bytes.Replace(b, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":3`), 1)
			}
			_, _ = w.Write(b)
		case strings.HasPrefix(r.URL.Path, "/v2/library/qwen2.5/blobs/"):
			b, ok := l.blobs[strings.TrimPrefix(r.URL.Path, "/v2/library/qwen2.5/blobs/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(reg.Close)
	l.m.Net.(*Net).OllamaRegistry = reg.URL
	return l
}

func TestCoreMacInstallsTheModelServerAndAModel(t *testing.T) {
	l := newLLMFixture(t)
	ctx := context.Background()
	if l.m.Installed("runtime.ollama") || l.m.Installed(tLLM) {
		t.Fatal("installed before any install")
	}
	// the model, its requirement (runtime.ollama, a pack) first
	if err := l.m.Install(ctx, tLLM, nil); err != nil {
		t.Fatalf("install %s: %v\n%s", tLLM, err, l.log.String())
	}
	if !l.m.Installed("runtime.ollama") {
		t.Fatal("runtime.ollama, the model's requirement, is not installed")
	}
	if cur := l.m.Layout.Current("runtime.ollama"); !exists(filepath.Join(l.m.Layout.Dir("runtime.ollama"), cur, "ollama", "ollama")) {
		t.Fatalf("runtime.ollama's ollama is not in its pack folder %q", cur)
	}
	if !l.m.Installed(tLLM) {
		t.Fatal("the model is not installed")
	}
	man, err := os.ReadFile(OllamaManifestPath(l.models, "library/qwen2.5", "7b"))
	if err != nil || !bytes.Equal(man, l.manifest) {
		t.Fatalf("the store's manifest %q, %v", man, err)
	}
	for d, b := range l.blobs {
		got, err := os.ReadFile(filepath.Join(l.models, "blobs", strings.Replace(d, ":", "-", 1)))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("blob %s: %v", d, err)
		}
	}
	// again: nothing is downloaded
	n := l.served.Load()
	if err := l.m.Install(ctx, tLLM, nil); err != nil || l.served.Load() != n {
		t.Fatalf("a second install: %v, %d requests", err, l.served.Load()-n)
	}
	// removed: the manifest and its blobs go, runtime.ollama stays
	if err := l.m.Remove(ctx, tLLM); err != nil {
		t.Fatal(err)
	}
	if l.m.Installed(tLLM) {
		t.Fatal("the model is still there")
	}
	for d := range l.blobs {
		if exists(filepath.Join(l.models, "blobs", strings.Replace(d, ":", "-", 1))) {
			t.Fatalf("blob %s kept after the removal", d)
		}
	}
	if !l.m.Installed("runtime.ollama") {
		t.Fatal("the model's removal took runtime.ollama")
	}
}

func TestAModelThatIsNotThePinnedOneIsNotInstalled(t *testing.T) {
	l := newLLMFixture(t)
	l.bad.Store(true)
	err := l.m.Install(context.Background(), tLLM, nil)
	if ReasonOf(err) != ReasonVerifyFailed {
		t.Fatalf("install of a manifest that is not the pinned one: %v (%s)", err, ReasonOf(err))
	}
	if l.m.Installed(tLLM) || exists(OllamaManifestPath(l.models, "library/qwen2.5", "7b")) {
		t.Fatal("a manifest that is not the pinned one is in the store")
	}
	// a blob that is not the pinned one is refused before any manifest
	l.bad.Store(false)
	for d := range l.blobs {
		l.blobs[d] = append([]byte("x"), l.blobs[d][1:]...)
		_ = os.Remove(filepath.Join(l.models, "blobs", strings.Replace(d, ":", "-", 1)))
		break
	}
	if err := l.m.Install(context.Background(), tLLM, nil); ReasonOf(err) != ReasonVerifyFailed || l.m.Installed(tLLM) {
		t.Fatalf("install of a blob that is not the pinned one: %v", err)
	}
}

func TestOllamaSources(t *testing.T) {
	d := "sha256:" + strings.Repeat("ab", 32)
	for src, want := range map[string]string{
		"ollama:qwen2.5:7b@" + d:     "library/qwen2.5 7b",
		"ollama:acme/llm:q4@" + d:    "acme/llm q4",
		"ollama:qwen2.5@" + d:        "",
		"ollama:qwen2.5:7b@sha256:x": "",
		"ollama:../x:7b@" + d:        "",
		"hf:org/x@" + d:              "",
	} {
		repo, tag, _, ok := ollamaSource(src)
		if got := strings.TrimSpace(repo + " " + tag); (ok && got != want) || (!ok && want != "") {
			t.Errorf("%s: %q %v, want %q", src, got, ok, want)
		}
	}
}
