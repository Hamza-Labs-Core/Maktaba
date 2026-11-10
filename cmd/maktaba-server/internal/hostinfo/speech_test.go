package hostinfo

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Story 59.06 T02: the default model by tier, CPU features and GPU.
func TestSpeechDefaultsByTier(t *testing.T) {
	avx2 := CPU{AVX2: true}
	gpu := GPUs{NVIDIA: []GPU{{Name: "RTX 3060", MemoryMB: 12288}}}
	small := GPUs{NVIDIA: []GPU{{Name: "GT 1030", MemoryMB: 2048}}}
	cases := []struct {
		name   string
		tier   Tier
		m      Machine
		on     bool
		model  string
		reason string
	}{
		{"light", TierLight, Machine{Arch: "amd64", CPU: avx2}, false, ModelBase, SpeechLight},
		{"light arm", TierLight, Machine{Arch: "arm64"}, false, ModelBase, SpeechLight},
		{"light cuda", TierLight, Machine{Arch: "amd64", GPU: gpu, Memory: Memory{LimitBytes: 5 * GiB}}, false, ModelTurbo, SpeechLight},
		{"standard avx2", TierStandard, Machine{Arch: "amd64", CPU: avx2}, true, ModelSmall, ""},
		{"standard J4125", TierStandard, Machine{Arch: "amd64"}, true, ModelBase, SpeechNoAVX2},
		{"standard arm", TierStandard, Machine{Arch: "arm64"}, true, ModelBase, SpeechARM},
		{"standard cuda", TierStandard, Machine{Arch: "amd64", GPU: gpu}, true, ModelTurbo, SpeechCUDA},
		{"standard 2 GB GPU", TierStandard, Machine{Arch: "amd64", CPU: avx2, GPU: small}, true, ModelSmall, ""},
		{"strong avx2", TierStrong, Machine{Arch: "amd64", CPU: avx2}, true, ModelTurbo, ""},
		{"strong cuda", TierStrong, Machine{Arch: "amd64", GPU: gpu}, true, ModelTurbo, SpeechCUDA},
		{"strong no avx2", TierStrong, Machine{Arch: "amd64"}, true, ModelSmall, SpeechNoAVX2},
		{"strong arm", TierStrong, Machine{Arch: "arm64"}, true, ModelSmall, SpeechARM},
	}
	for _, c := range cases {
		d := DefaultsFor(c.tier, c.m)
		if d.SpeechOn != c.on || d.SpeechModel != c.model || d.SpeechReason != c.reason {
			t.Errorf("%s: on %v model %s reason %q, want %v %s %q", c.name, d.SpeechOn, d.SpeechModel, d.SpeechReason, c.on, c.model, c.reason)
		}
	}
	if !DefaultsFor(TierLight, Machine{}).SpeechInQuietHours || DefaultsFor(TierStandard, Machine{}).SpeechInQuietHours {
		t.Fatal("only Light transcribes in the quiet hours alone")
	}
}

func TestSpeechOfferedAndNextSmaller(t *testing.T) {
	if got := SpeechOffered(TierLight, ModelBase); !slices.Equal(got, []string{"tiny", "base"}) {
		t.Fatalf("light: %v", got)
	}
	if got := SpeechOffered(TierLight, ModelTurbo); !slices.Equal(got, []string{"tiny", "base", "large-v3-turbo"}) {
		t.Fatalf("light with a GPU: %v", got)
	}
	if got := SpeechOffered(TierStandard, ModelSmall); !slices.Equal(got, []string{"tiny", "base", "small", "medium"}) {
		t.Fatalf("standard: %v", got)
	}
	all := SpeechOffered(TierStrong, ModelTurbo)
	if len(all) != len(SpeechModels) {
		t.Fatalf("strong: %v", all)
	}
	for _, c := range []struct{ from, want string }{{"medium", "large-v3-turbo"}, {"large-v3-turbo", "small"}, {"small", "base"}, {"base", "tiny"}} {
		if got, ok := SpeechNextSmaller(c.from, all); !ok || got != c.want {
			t.Errorf("next smaller than %s: %s %v", c.from, got, ok)
		}
	}
	if _, ok := SpeechNextSmaller("tiny", all); ok {
		t.Fatal("tiny is the fastest")
	}
	if got, ok := SpeechNextSmaller("small", []string{"small", "medium"}); ok {
		t.Fatalf("none offered below small: %s", got)
	}
}

// Story 59.06 T04: a 6 GB limit holds one small worker; more memory holds more.
func TestSpeechWorkersFit(t *testing.T) {
	for _, c := range []struct {
		limit uint64
		model string
		want  int
	}{{6 * GiB, ModelSmall, 1}, {16 * GiB, ModelSmall, 5}, {4 * GiB, ModelTurbo, 1}, {6 * GiB, ModelBase, 3}, {0, ModelSmall, -1}} {
		if got := SpeechWorkersFit(c.limit, c.model); got != c.want {
			t.Errorf("%v GiB %s: %d, want %d", GB(c.limit), c.model, got, c.want)
		}
	}
}

// The sizes are the catalog's model packs and the pipeline's engine.SIZES.
func TestSpeechModelsMatchTheCatalogAndThePipeline(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	catalog, err := os.ReadFile(filepath.Join(root, "shared", "components", "catalog.toml"))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := os.ReadFile(filepath.Join(root, "pipeline", "src", "maktaba_pipeline", "stt", "engine.py"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range SpeechModels {
		if s.Name != ModelTurbo && !strings.Contains(string(catalog), `"hf:`+s.Repo+`@`) {
			t.Errorf("%s: no catalog pack with source %s", s.Name, s.Repo)
		}
		if s.Name != ModelTurbo && !strings.Contains(string(engine), `"`+s.Name+`": "`+s.Repo+`"`) {
			t.Errorf("%s: engine.SIZES does not name %s", s.Name, s.Repo)
		}
	}
	if !strings.Contains(string(engine), `"large-v3-turbo": DEFAULT_MODEL[CT2]`) || !strings.Contains(string(engine), `CT2: "dropbox-dash/faster-whisper-large-v3-turbo"`) {
		t.Error("engine.SIZES' large-v3-turbo is not the default model")
	}
}
