package componentsindex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/componentpack"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

const commit = "0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"

var published = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// built holds every pack the catalog asks for, each a small tree, once per part size
// (TestMain): each pack is compressed at zstd's best level, and building them in each test
// took 1.4 s of a 400 ms budget on CI's runners (with -race).
var built = map[int64]string{}

func TestMain(m *testing.M) {
	code := func() int {
		root, err := os.MkdirTemp("", "componentsindex-test-")
		if err != nil {
			panic(err)
		}
		defer func() { _ = os.RemoveAll(root) }()
		for _, partSize := range []int64{0, 64} {
			dist := filepath.Join(root, fmt.Sprintf("parts-%d", partSize))
			if err := writePacks(dist, partSize); err != nil {
				panic(err)
			}
			built[partSize] = dist
		}
		return m.Run()
	}()
	os.Exit(code)
}

func writePacks(dist string, partSize int64) error {
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return err
	}
	for _, p := range Packs(components.Default(), "") {
		stage, err := os.MkdirTemp(filepath.Dir(dist), "stage-")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(stage, "constraints.txt"), []byte(p.ID+" "+p.Platform+"\n"+strings.Repeat("x", 300)), 0o644); err != nil {
			return err
		}
		if _, err := componentpack.Build(componentpack.Options{Dir: stage, ID: p.ID, Version: "0.4.2", Platform: p.Platform,
			ModTime: published, Out: dist, PartSize: partSize}); err != nil {
			return err
		}
	}
	return nil
}

// buildPacks copies every pack the catalog asks for (built once, TestMain) into dist.
func buildPacks(t *testing.T, dist string, partSize int64) {
	t.Helper()
	from, ok := built[partSize]
	if !ok {
		t.Fatalf("no packs built with part size %d", partSize)
	}
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dist, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func generate(t *testing.T, dist string) ([]byte, *rv.ComponentsIndex, []string) {
	t.Helper()
	idx, missing, err := Generate(Options{Catalog: components.Default(), Version: "0.4.2", Commit: commit, Published: published, Dist: dist})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	return b, idx, missing
}

// The catalog's packs: an asset not in the payload whose source is its own pack, on each
// of its platforms; deferred ones (runtime.ollama off the Mac) and payload assets left out.
func TestPacks(t *testing.T) {
	got := map[string]bool{}
	for _, p := range Packs(components.Default(), "") {
		got[p.ID+" "+p.Platform] = true
	}
	for _, want := range []string{"runtime.ml darwin-arm64", "runtime.ml windows-amd64", "runtime.ml linux-amd64",
		"runtime.ml linux-arm64", "tool.yt-dlp linux-amd64", "runtime.ollama darwin-arm64"} {
		if !got[want] {
			t.Errorf("no pack %s", want)
		}
	}
	for k := range got {
		id, platform, _ := strings.Cut(k, " ")
		if (id == "runtime.ollama" && platform != "darwin-arm64") || id == "model.refine" || strings.HasPrefix(id, "model.whisper") {
			t.Errorf("unexpected pack %s", k)
		}
	}
}

// T08: generating twice writes the same bytes; server is the exact version; expires is
// published + 365 days; every pinned model is listed with its files.
func TestGenerateIsReproducible(t *testing.T) {
	dist := t.TempDir()
	buildPacks(t, dist, 0)
	b1, idx, missing := generate(t, dist)
	b2, _, _ := generate(t, dist)
	if !bytes.Equal(b1, b2) {
		t.Fatal("two runs wrote different indexes")
	}
	if len(missing) != 0 {
		t.Fatalf("missing %v", missing)
	}
	if idx.Server != "=0.4.2" || !idx.Expires.Equal(published.Add(365*24*time.Hour)) {
		t.Fatalf("server %q, expires %s", idx.Server, idx.Expires)
	}
	if len(idx.Packs) != len(Packs(components.Default(), "")) {
		t.Fatalf("%d packs", len(idx.Packs))
	}
	turbo := 0
	for _, m := range idx.Models {
		if m.ID == "model.whisper-turbo" {
			turbo++
			if len(m.Files) == 0 {
				t.Fatalf("%s has no files", m.Source)
			}
		}
		if m.ID == "model.cohere-arabic" && !m.Gated {
			t.Fatal("cohere is gated")
		}
	}
	if turbo != 2 {
		t.Fatalf("model.whisper-turbo has %d sources, want 2 (CT2 and MLX)", turbo)
	}
	if err := CheckDist(idx, dist); err != nil {
		t.Fatal(err)
	}
}

// A failed leg's pack is missing (not an error); a stray pack is.
func TestGenerateMissingAndStray(t *testing.T) {
	dist := t.TempDir()
	buildPacks(t, dist, 0)
	gone := componentpack.FileName("tool.yt-dlp", "0.4.2", "windows-amd64")
	if err := os.Remove(filepath.Join(dist, gone)); err != nil {
		t.Fatal(err)
	}
	_, idx, missing := generate(t, dist)
	if len(missing) != 1 || missing[0] != gone || len(idx.Packs) != len(Packs(components.Default(), ""))-1 {
		t.Fatalf("missing %v", missing)
	}
	if err := os.WriteFile(filepath.Join(dist, "evil-0.4.2-linux-amd64.tar.zst"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Generate(Options{Catalog: components.Default(), Version: "0.4.2", Commit: commit, Published: published, Dist: dist}); err == nil {
		t.Fatal("a stray pack was indexed")
	}
}

// Split packs are listed with their parts, and CheckDist reads them.
func TestGenerateSplit(t *testing.T) {
	dist := t.TempDir()
	buildPacks(t, dist, 64)
	_, idx, _ := generate(t, dist)
	for _, p := range idx.Packs {
		if len(p.Parts) < 2 {
			t.Fatalf("%s: %d parts", p.Name, len(p.Parts))
		}
	}
	if err := CheckDist(idx, dist); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dist, idx.Packs[0].Parts[1].Name)); err != nil {
		t.Fatal(err)
	}
	if err := CheckDist(idx, dist); err == nil {
		t.Fatal("a missing part passed")
	}
}
