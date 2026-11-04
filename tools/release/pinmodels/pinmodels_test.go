package pinmodels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

const (
	mainSHA = "1111111111111111111111111111111111111111"
	mlxSHA  = "2222222222222222222222222222222222222222"
)

func hexOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

var manifest = []byte(`{"schemaVersion":2,"config":{"digest":"sha256:` + strings.Repeat("c", 64) + `","size":487},"layers":[{"mediaType":"application/vnd.ollama.image.model","digest":"sha256:` + strings.Repeat("d", 64) + `","size":4683073952}]}`)

// fake answers Hugging Face's revision, tree and resolve calls and the ollama registry.
func fake(t *testing.T) *httptest.Server {
	t.Helper()
	weights := strings.Repeat("e", 64)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasPrefix(p, "/api/models/Systran/faster-whisper-medium/revision/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": mainSHA, "gated": false})
		case strings.HasPrefix(p, "/api/models/mlx-community/whisper-medium-mlx/revision/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": mlxSHA, "gated": false})
		case p == "/api/models/Systran/faster-whisper-medium/tree/"+mainSHA:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"type": "file", "path": ".gitattributes", "size": 10},
				{"type": "file", "path": "README.md", "size": 10},
				{"type": "file", "path": "config.json", "size": 2},
				{"type": "file", "path": "model.bin", "size": 1527906378, "lfs": map[string]any{"oid": weights, "size": 1527906378}},
				{"type": "file", "path": "extra.safetensors", "size": 5, "lfs": map[string]any{"oid": weights, "size": 5}},
				{"type": "directory", "path": "sub"},
			})
		case p == "/api/models/mlx-community/whisper-medium-mlx/tree/"+mlxSHA:
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"type": "file", "path": "weights.npz", "size": 9, "lfs": map[string]any{"oid": weights, "size": 9}},
			})
		case p == "/Systran/faster-whisper-medium/resolve/"+mainSHA+"/config.json":
			_, _ = w.Write([]byte("{}"))
		case p == "/v2/library/qwen2.5/manifests/7b":
			_, _ = w.Write(manifest)
		default:
			t.Errorf("unexpected %s", p)
			http.NotFound(w, r)
		}
	}))
}

// tiny is a catalog of one unpinned model, as the catalog was before 51.15.
func tiny(t *testing.T) []byte {
	t.Helper()
	raw := string(components.Raw())
	head := raw[:strings.Index(raw, "[[component]]")]
	return []byte(head + `
[[component]]
id         = "model.whisper-medium"
kind       = "asset"
group      = "models"
title_key  = "components.model.whisper-medium.title"
desc_key   = "components.model.whisper-medium.desc"
source     = "hf:Systran/faster-whisper-medium"
sources    = { darwin-arm64 = "hf:mlx-community/whisper-medium-mlx" }
in_payload = false
platforms  = ["darwin-arm64", "linux-amd64"]
since      = "0.3.66"
`)
}

// The pins are what the API answers: LFS files by their oid, small files hashed, docs and
// files outside the rules left out, per-platform files tagged with their source.
func TestPinSourceHF(t *testing.T) {
	srv := fake(t)
	defer srv.Close()
	c := &Client{HF: srv.URL, Registry: srv.URL}
	p, err := c.PinSource(context.Background(), "hf:Systran/faster-whisper-medium", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "hf:Systran/faster-whisper-medium@"+mainSHA {
		t.Fatalf("source %s", p.Source)
	}
	want := []File{{Path: "config.json", SHA256: hexOf([]byte("{}")), Size: 2}, {Path: "model.bin", SHA256: strings.Repeat("e", 64), Size: 1527906378}}
	if len(p.Files) != 2 || p.Files[0] != want[0] || p.Files[1] != want[1] {
		t.Fatalf("files %+v", p.Files)
	}
}

func TestPinSourceOllama(t *testing.T) {
	srv := fake(t)
	defer srv.Close()
	c := &Client{HF: srv.URL, Registry: srv.URL}
	p, err := c.PinSource(context.Background(), "ollama:qwen2.5:7b", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Source != "ollama:qwen2.5:7b@sha256:"+hexOf(manifest) || len(p.Files) != 2 {
		t.Fatalf("%+v", p)
	}
	// a tag that moved keeps the pin and its files
	old := "ollama:qwen2.5:7b@sha256:" + strings.Repeat("0", 64)
	kept := []File{{Path: "blobs/sha256-" + strings.Repeat("1", 64), SHA256: strings.Repeat("1", 64), Size: 3}}
	p, err = c.PinSource(context.Background(), old, false, kept)
	if err != nil || p.Source != old || len(p.Files) != 1 || len(p.Skipped) != 1 {
		t.Fatalf("%+v, %v", p, err)
	}
}

// PinCatalog rewrites an unpinned block so that the catalog's lint accepts it (it refused
// it before); a second run changes nothing.
func TestPinCatalogRewrites(t *testing.T) {
	srv := fake(t)
	defer srv.Close()
	c := &Client{HF: srv.URL, Registry: srv.URL}
	in := tiny(t)
	if _, err := components.Load(in); err == nil || !strings.Contains(err.Error(), "must be pinned") {
		t.Fatalf("an unpinned model loaded: %v", err)
	}
	out, results, err := c.PinCatalog(context.Background(), in, false)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := components.Load(out)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := cat.Get("model.whisper-medium")
	if m.Source != "hf:Systran/faster-whisper-medium@"+mainSHA || m.SourceFor("darwin-arm64") != "hf:mlx-community/whisper-medium-mlx@"+mlxSHA {
		t.Fatalf("sources %s %v", m.Source, m.Sources)
	}
	if len(m.FilesOf(m.Source)) != 2 || len(m.FilesOf(m.SourceFor("darwin-arm64"))) != 1 || len(results) != 1 {
		t.Fatalf("files %+v", m.Files)
	}
	again, _, err := c.PinCatalog(context.Background(), out, false)
	if err != nil || string(again) != string(out) {
		t.Fatalf("a second run changed the file (%v):\n%s", err, again)
	}
}

func TestRules(t *testing.T) {
	ct2 := RulesFor("Systran/faster-whisper-medium")
	for p, want := range map[string]bool{"model.bin": true, "vocabulary.txt": true, "README.md": false, "extra.safetensors": false} {
		if ct2.keep(p) != want {
			t.Errorf("ct2 %s: %v", p, !want)
		}
	}
	emb := RulesFor("intfloat/multilingual-e5-small")
	for p, want := range map[string]bool{"model.safetensors": true, "onnx/model.onnx": false, "README.md": false} {
		if emb.keep(p) != want {
			t.Errorf("embedder %s: %v", p, !want)
		}
	}
}
