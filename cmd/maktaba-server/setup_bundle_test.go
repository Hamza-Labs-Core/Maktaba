package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/componentseed"
)

func bundleOpts(t *testing.T, ev map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, b := range data(t, ev)["bundles"].([]any) {
		m := b.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func warns(opt map[string]any) []string {
	var out []string
	for _, w := range opt["warnings"].([]any) {
		out = append(out, w.(map[string]any)["code"].(string))
	}
	return out
}

// T01: check_only on an 8 GB machine: four bundles with their sizes, Full warns
// low_memory, Core does not; nothing is written.
func TestStepBundleCheckOnly(t *testing.T) {
	e := newSetupEnv(t)
	prev := hostFacts
	t.Cleanup(func() { hostFacts = prev })
	hostFacts = func(string) (uint64, uint64) { return 8 << 30, 500 << 30 }
	code, _, ev := runStep(t, "bundle", map[string]any{"check_only": true})
	if code != 0 {
		t.Fatalf("exit %d: %v", code, ev)
	}
	opts := bundleOpts(t, ev)
	if len(opts) != 4 || opts["full"] == nil || opts["core"] == nil || opts["worker"] == nil || opts["custom"] == nil {
		t.Fatalf("bundles %v", opts)
	}
	full, core := opts["full"], opts["core"]
	if !slices.Contains(warns(full), "low_memory") || len(warns(core)) != 0 {
		t.Fatalf("warnings: full %v core %v", warns(full), warns(core))
	}
	if full["download_bytes"].(float64) <= core["download_bytes"].(float64) || core["download_bytes"].(float64) <= 0 {
		t.Fatalf("download: full %v core %v", full["download_bytes"], core["download_bytes"])
	}
	if core["needs_models"] != false || full["needs_models"] != true || core["needs_llm"] != false || full["needs_llm"] != true {
		t.Fatalf("needs: full %v core %v", full, core)
	}
	if data(t, ev)["preselected"] != "full" {
		t.Fatalf("preselected %v", data(t, ev)["preselected"])
	}
	if _, err := os.Stat(componentseed.ChoicePath(e.state)); err == nil {
		t.Fatal("check_only wrote the choice")
	}
}

// T02: custom with subtitles alone: what it requires is added; the choice is written and
// the venv follows it.
func TestStepBundleCustomAddsRequires(t *testing.T) {
	e := newSetupEnv(t)
	code, _, ev := runStep(t, "bundle", map[string]any{"bundle": "custom", "base": "core", "enable": []string{"subtitles"}})
	if code != 0 {
		t.Fatalf("exit %d: %v", code, ev)
	}
	d := data(t, ev)
	var enabled []string
	for _, id := range d["enabled"].([]any) {
		enabled = append(enabled, id.(string))
	}
	for _, want := range []string{"subtitles", "transcription", "runtime.ml", "model.whisper-turbo", "media.thumbnails"} {
		if !slices.Contains(enabled, want) {
			t.Errorf("enabled lacks %s: %v", want, enabled)
		}
	}
	if d["needs_models"] != true || d["needs_llm"] != false || d["venv_extras"] != "stt,search" {
		t.Fatalf("result %v", d)
	}
	ch, ok, err := componentseed.ReadChoice(e.state)
	if err != nil || !ok || ch.Bundle != "custom" || !ch.Has("runtime.ml") {
		t.Fatalf("choice %+v %v %v", ch, ok, err)
	}
	if extras, none := choiceVenvSpec(e.state); extras != "stt,search" || none {
		t.Fatalf("venv %q %v", extras, none)
	}
}

func TestStepBundleCoreAndErrors(t *testing.T) {
	e := newSetupEnv(t)
	code, _, ev := runStep(t, "bundle", map[string]any{"bundle": "core"})
	if code != 0 || data(t, ev)["venv_core"] != true || data(t, ev)["needs_models"] != false {
		t.Fatalf("core: %d %v", code, ev)
	}
	if extras, none := choiceVenvSpec(e.state); extras != "" || !none {
		t.Fatalf("core venv %q %v", extras, none)
	}
	for in, want := range map[string]map[string]any{
		"unknown_component": {"bundle": "custom", "enable": []string{"nope"}},
		"bad_input":         {"bundle": "huge"},
	} {
		if _, _, ev := runStep(t, "bundle", want); ev["code"] != in {
			t.Errorf("%v: %v, want %s", want, ev, in)
		}
	}
	if _, _, ev := runStep(t, "bundle", map[string]any{}); ev["code"] != "bad_input" {
		t.Errorf("no input: %v", ev)
	}
}

func TestPayloadFlavour(t *testing.T) {
	dir := t.TempDir()
	if flavourOf(dir) != "full" {
		t.Fatal("no payload.json is full")
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.json"), []byte(`{"flavour":"core"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if flavourOf(dir) != "core" {
		t.Fatal("a core payload")
	}
}
