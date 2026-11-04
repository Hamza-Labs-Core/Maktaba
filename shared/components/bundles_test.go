package components

import (
	"errors"
	"slices"
	"testing"
)

func TestBundleSetCoreHasNoProcessComponent(t *testing.T) {
	cat := Default()
	set, err := cat.BundleSet("core", "", nil, "darwin-arm64")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"core.api", "media.thumbnails", "library.convert", "library.dedup", "backups", "queue_health", "analytics", "updates.auto"} {
		if !slices.Contains(set, want) {
			t.Errorf("core lacks %s: %v", want, set)
		}
	}
	for _, id := range set {
		c, _ := cat.Get(id)
		if c.Switchable && c.HasPart(KindProcess) {
			t.Errorf("core enables the process component %s (N9: 4 processes)", id)
		}
		if c.Kind == KindAsset {
			t.Errorf("core holds the asset %s", id)
		}
	}
}

// T02: a custom bundle with subtitles alone brings what subtitles requires.
func TestBundleSetCustomAddsRequirements(t *testing.T) {
	set, err := Default().BundleSet(BundleCustom, "core", []string{"subtitles"}, "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"subtitles", "transcription", "runtime.ml", "model.whisper-turbo"} {
		if !slices.Contains(set, want) {
			t.Errorf("custom with subtitles lacks %s: %v", want, set)
		}
	}
	if slices.Contains(set, "model.whisper-large-v3") || slices.Contains(set, "llm") {
		t.Errorf("custom took more than one model, or more than it needs: %v", set)
	}
}

func TestBundleSetRefusals(t *testing.T) {
	cat := Default()
	for _, c := range []struct {
		bundle, base string
		extra        []string
		code         string
	}{
		{"huge", "", nil, "bad_input"},
		{"core", "", []string{"llm"}, "bad_input"},
		{BundleCustom, "worker", nil, "bad_input"},
		{BundleCustom, "core", []string{"nope"}, "unknown_component"},
		{BundleCustom, "core", []string{"llm"}, ""},
	} {
		_, err := cat.BundleSet(c.bundle, c.base, c.extra, "darwin-arm64")
		var be *BundleError
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s %v: %v", c.bundle, c.extra, err)
		case c.code != "" && (!errors.As(err, &be) || be.Code != c.code):
			t.Errorf("%s %v: %v, want %s", c.bundle, c.extra, err, c.code)
		}
	}
}

func TestBundleSetPlatformUnsupported(t *testing.T) {
	cat, err := Load(Raw())
	if err != nil {
		t.Fatal(err)
	}
	c, _ := cat.Get("runtime.ollama")
	c.Platforms = []string{"darwin-arm64"}
	var be *BundleError
	if _, err := cat.BundleSet(BundleCustom, "core", []string{"llm"}, "windows-amd64"); !errors.As(err, &be) || be.Code != "platform_unsupported" {
		t.Fatalf("llm on windows without ollama: %v", err)
	}
	if _, err := cat.BundleSet(BundleCustom, "core", []string{"runtime.ollama"}, "windows-amd64"); !errors.As(err, &be) || be.ID != "runtime.ollama" {
		t.Fatalf("runtime.ollama on windows: %v", err)
	}
}

func TestAssetBytes(t *testing.T) {
	cat := Default()
	turbo, _ := cat.Get("model.whisper-turbo")
	mac, linux := turbo.AssetBytes("darwin-arm64"), turbo.AssetBytes("linux-amd64")
	if mac < 1_600_000_000 || linux < 1_600_000_000 || mac > 1_700_000_000 || linux > 1_700_000_000 {
		t.Fatalf("whisper-turbo: mac %d, linux %d (one source each, not both summed)", mac, linux)
	}
	ollama, _ := cat.Get("runtime.ollama")
	if ollama.AssetBytes("darwin-arm64") == 0 || ollama.AssetBytes("linux-amd64") == 0 {
		t.Fatalf("ollama: a pack on every platform (a Core Mac downloads it too)")
	}
	ml, _ := cat.Get("runtime.ml")
	if ml.AssetBytes("linux-amd64") == 0 {
		t.Fatal("runtime.ml has no size")
	}
	api, _ := cat.Get("core.api")
	if api.AssetBytes("linux-amd64") != 0 {
		t.Fatal("a process downloads nothing")
	}
}

func TestSetupWants(t *testing.T) {
	cat := Default()
	set, _ := cat.BundleSet("core", "", nil, "linux-amd64")
	w := cat.SetupWants(set)
	for id, want := range map[string]string{"core.api": WantEnabled, "media.thumbnails": WantEnabled,
		"transcription": WantInstalled, "runtime.ml": WantAbsent, "llm": WantInstalled} {
		if w[id] != want {
			t.Errorf("%s = %q, want %q", id, w[id], want)
		}
	}
}
