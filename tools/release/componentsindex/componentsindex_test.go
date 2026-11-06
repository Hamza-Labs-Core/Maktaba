package componentsindex

import (
	"bytes"
	"errors"
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

func generate(t *testing.T, dist string) ([]byte, *rv.ComponentsIndex) {
	t.Helper()
	idx, err := Generate(Options{Catalog: components.Default(), Version: "0.4.2", Commit: commit, Published: published, Dist: dist})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	return b, idx
}

// The catalog's packs: an asset not in the payload whose source is its own pack, on each
// of its platforms; payload assets, models and runtime.ollama off the Mac (DeferredOn) left out.
func TestPacks(t *testing.T) {
	got := map[string]bool{}
	for _, p := range Packs(components.Default(), "") {
		got[p.ID+" "+p.Platform] = true
	}
	for _, want := range []string{"runtime.ml darwin-arm64", "runtime.ml windows-amd64", "runtime.ml linux-amd64",
		"runtime.ml linux-arm64", "tool.yt-dlp linux-amd64", "model.refine darwin-arm64", "model.refine windows-amd64",
		"tool.ffmpeg linux-amd64", "tool.ffmpeg linux-arm64", "runtime.ollama darwin-arm64"} {
		if !got[want] {
			t.Errorf("no pack %s", want)
		}
	}
	for k := range got {
		id, plat, _ := strings.Cut(k, " ")
		if (id == "runtime.ollama" && plat != "darwin-arm64") || strings.HasPrefix(id, "model.whisper") || (id == "tool.ffmpeg" && !strings.HasPrefix(plat, "linux-")) {
			t.Errorf("unexpected pack %s", k)
		}
	}
}

// Every pack the catalog asks of a release has a recipe (tools/release/packs/recipes): a
// pack with none fails every release's build leg, and with it the release. Every
// lang.hunspell.<code> shares lang.hunspell.sh when it has none of its own, as build.sh does.
func TestEveryPackHasARecipe(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Packs(components.Default(), "") {
		if seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		if _, err := os.Stat(recipeFor(p.ID)); err != nil {
			t.Errorf("%s: no recipe: %v", p.ID, err)
		}
	}
}

// recipeFor is build.sh's lookup: the pack's own recipe, else the shared Hunspell one.
func recipeFor(id string) string {
	own := filepath.Join("..", "packs", "recipes", id+".sh")
	if _, err := os.Stat(own); err != nil && strings.HasPrefix(id, "lang.hunspell.") {
		return filepath.Join("..", "packs", "recipes", "lang.hunspell.sh")
	}
	return own
}

func TestAHunspellLanguageUsesTheSharedRecipe(t *testing.T) {
	if got := recipeFor("lang.hunspell.fr"); filepath.Base(got) != "lang.hunspell.sh" {
		t.Fatalf("lang.hunspell.fr -> %s", got)
	}
	if got := recipeFor("tool.ffmpeg"); filepath.Base(got) != "tool.ffmpeg.sh" {
		t.Fatalf("tool.ffmpeg -> %s", got)
	}
}

// T08: generating twice writes the same bytes; server is the exact version; expires is
// published + 365 days; every pinned model is listed with its files.
func TestGenerateIsReproducible(t *testing.T) {
	dist := t.TempDir()
	buildPacks(t, dist, 0)
	b1, idx := generate(t, dist)
	b2, _ := generate(t, dist)
	if !bytes.Equal(b1, b2) {
		t.Fatal("two runs wrote different indexes")
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

func opts(dist string) Options {
	return Options{Catalog: components.Default(), Version: "0.4.2", Commit: commit, Published: published, Dist: dist}
}

// A failed leg's pack is missing: the index is refused, naming it, for any catalog
// platform, so a release never ships without some platform's packs.
func TestGenerateRefusesAMissingPack(t *testing.T) {
	// one build for every case: each hides its own pack and puts it back. The Hunspell
	// languages share one recipe and one shape, so one of them stands for the rest (each
	// case is a Generate, and every language on every platform broke the unit budget).
	dist := t.TempDir()
	buildPacks(t, dist, 0)
	aside := t.TempDir()
	for _, p := range Packs(components.Default(), "") {
		if strings.HasPrefix(p.ID, "lang.hunspell.") && p.ID != "lang.hunspell.fr" {
			continue
		}
		t.Run(p.ID+"/"+p.Platform, func(t *testing.T) {
			gone := p.Name("0.4.2")
			if err := os.Rename(filepath.Join(dist, gone), filepath.Join(aside, gone)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Rename(filepath.Join(aside, gone), filepath.Join(dist, gone)); err != nil {
					t.Error(err)
				}
			})
			idx, err := Generate(opts(dist))
			if !errors.Is(err, ErrMissing) || idx != nil {
				t.Fatalf("Generate = %v, %v; want ErrMissing", idx, err)
			}
			if !strings.Contains(err.Error(), gone) {
				t.Fatalf("%v does not name %s", err, gone)
			}
		})
	}
}

// Every platform's leg failed: one error names all of their packs.
func TestGenerateNamesEveryMissingPack(t *testing.T) {
	dist := t.TempDir()
	_, err := Generate(opts(dist))
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("an empty dist: %v", err)
	}
	for _, p := range Packs(components.Default(), "") {
		if !strings.Contains(err.Error(), p.Name("0.4.2")) {
			t.Errorf("%v does not name %s", err, p.Name("0.4.2"))
		}
	}
}

// Every catalog platform has packs: a missing platform cannot pass for one with none.
func TestEveryCatalogPlatformHasPacks(t *testing.T) {
	cat := components.Default()
	plats := map[string]bool{}
	for _, c := range cat.Components() {
		if c.Kind == components.KindAsset && !c.InPayload {
			for _, p := range c.Platforms {
				plats[p] = true
			}
		}
	}
	if len(plats) < 4 {
		t.Fatalf("catalog platforms %v", plats)
	}
	for p := range plats {
		if len(Packs(cat, p)) == 0 {
			t.Errorf("%s: no packs", p)
		}
	}
}

// A stray pack is an error.
func TestGenerateRefusesAStrayPack(t *testing.T) {
	dist := t.TempDir()
	buildPacks(t, dist, 0)
	if err := os.WriteFile(filepath.Join(dist, "evil-0.4.2-linux-amd64.tar.zst"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(opts(dist)); err == nil || errors.Is(err, ErrMissing) {
		t.Fatalf("a stray pack: %v", err)
	}
}

// Part of a release (--platform, --only): the packs of that platform or that one pack,
// each of which must be there; an Only id that is no pack of the platform is an error.
func TestGeneratePartOfARelease(t *testing.T) {
	cat := components.Default()
	dist := t.TempDir()
	for _, p := range Packs(cat, "linux-arm64") {
		b, err := os.ReadFile(filepath.Join(built[0], p.Name("0.4.2")))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dist, p.Name("0.4.2")), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o := opts(dist)
	o.Platform = "linux-arm64"
	idx, err := Generate(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Packs) != len(Packs(cat, "linux-arm64")) {
		t.Fatalf("%d packs", len(idx.Packs))
	}
	o.Only = []string{"tool.yt-dlp"}
	if idx, err = Generate(o); err == nil {
		// runtime.ml of linux-arm64 is in dist too, and not asked for: a stray
		t.Fatalf("an unasked pack in dist passed: %d packs", len(idx.Packs))
	}
	for _, p := range Packs(cat, "linux-arm64") {
		if p.ID == "tool.yt-dlp" {
			continue
		}
		if err := os.Remove(filepath.Join(dist, p.Name("0.4.2"))); err != nil {
			t.Fatal(err)
		}
	}
	if idx, err = Generate(o); err != nil || len(idx.Packs) != 1 || idx.Packs[0].ID != "tool.yt-dlp" {
		t.Fatalf("--only tool.yt-dlp: %v %v", idx, err)
	}
	o.Platform = "linux-amd64"
	if _, err := Generate(o); !errors.Is(err, ErrMissing) {
		t.Fatalf("another platform's pack: %v", err)
	}
	o.Only = []string{"model.whisper-turbo"}
	if _, err := Generate(o); err == nil || !strings.Contains(err.Error(), "not a pack of linux-amd64") {
		t.Fatalf("a model as --only: %v", err)
	}
}

// Split packs are listed with their parts, and CheckDist reads them.
func TestGenerateSplit(t *testing.T) {
	dist := t.TempDir()
	buildPacks(t, dist, 64)
	_, idx := generate(t, dist)
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
