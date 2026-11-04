package componentseed

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
)

func TestChoiceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadChoice(dir); ok || err != nil {
		t.Fatalf("no file: ok %v err %v", ok, err)
	}
	c := Choice{Bundle: "custom", Base: "core", Enable: []string{"subtitles"}, Enabled: []string{"subtitles"}, ChosenAt: time.Now()}
	if err := WriteChoice(dir, c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadChoice(dir)
	if err != nil || !ok || got.Bundle != "custom" || got.Base != "core" || len(got.Enable) != 1 {
		t.Fatalf("read %+v %v %v", got, ok, err)
	}
	if fi, _ := os.Stat(ChoicePath(dir)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := os.WriteFile(ChoicePath(dir), []byte(`{"bundle":"huge"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadChoice(dir); ok || err == nil {
		t.Fatal("an unknown bundle was read")
	}
}

func TestChoiceVenvExtras(t *testing.T) {
	cat := components.Default()
	for _, c := range []struct {
		bundle, base string
		enable       []string
		extras       string
		none         bool
	}{
		{"full", "", nil, "", false},
		{"worker", "", nil, "", false},
		{"core", "", nil, "", true},
		{"custom", "core", []string{"subtitles"}, "stt,search", false},
		{"custom", "core", []string{"youtube"}, "mediaintel", false},
		{"custom", "core", []string{"library.repair"}, "", true}, // repair only wants yt-dlp
	} {
		ch := Choice{Bundle: c.bundle, Base: c.base, Enable: c.enable}
		ch.Enabled = ch.Set(cat, "linux-amd64")
		extras, none := ch.VenvExtras()
		if extras != c.extras || none != c.none {
			t.Errorf("%s %v: extras %q none %v, want %q %v", c.bundle, c.enable, extras, none, c.extras, c.none)
		}
	}
}

// T03: setup chose Core: the first seed is the Core set, not today's switches.
func TestSeedFromCoreChoice(t *testing.T) {
	db := sqliteHome(t)
	if _, err := db.Exec(`INSERT INTO extensions (name, enabled) VALUES ('plex', 1)`); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	path, cfg := writeToml(t, t.TempDir(), "")
	var log bytes.Buffer
	h := home(st, path, cfg, &log, nil)
	h.Facts.LLMProcess = true                                                    // a Full dmg: its ollama is there
	h.Facts.Assets = map[string]bool{"runtime.ollama": true, "runtime.ml": true} // a Core venv has its python too
	h.Facts.Choice = &Choice{Bundle: "core"}
	if err := h.Run(ctx); err != nil {
		t.Fatal(err)
	}
	d, _, err := st.Desired(ctx, store.Home)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"core.api": "enabled", "media.thumbnails": "enabled", "library.dedup": "enabled", "backups": "enabled",
		"transcription": "installed", "search.semantic": "installed", "llm": "installed", "livetv": "installed",
		"livetv.slots": "installed", "youtube": "installed", "cluster": "installed",
		"runtime.ml": "absent", "model.whisper-turbo": "absent", "runtime.ollama": "installed",
		"ext.plex": "enabled", // the owner's own extension row
	} {
		if d[id].Want != want {
			t.Errorf("%s = %q, want %q", id, d[id].Want, want)
		}
	}
	m, _ := st.Meta(ctx)
	if m.Bundle != "core" {
		t.Fatalf("meta bundle %q", m.Bundle)
	}
	if !bytes.Contains(log.Bytes(), []byte("bundle core")) {
		t.Fatalf("log: %s", log.String())
	}
	for _, c := range components.Default().Components() {
		if c.Switchable && c.HasPart(components.KindProcess) && d[c.ID].Want == components.WantEnabled && c.Kind != components.KindExtension {
			t.Errorf("core enables the process component %s", c.ID)
		}
	}
}

// T03: no choice file seeds Full (today's behaviour), the same rows as before.
func TestSeedWithoutChoiceIsFull(t *testing.T) {
	cat := components.Default()
	f := today()
	if BundleOf(f) != "full" {
		t.Fatal(BundleOf(f))
	}
	f.Choice = &Choice{Bundle: "full"}
	a, b := Plan(cat, today()), Plan(cat, f)
	for id := range a {
		if a[id] != b[id] {
			t.Errorf("%s: %q without a choice, %q with full", id, a[id], b[id])
		}
	}
}

func TestSeedFromCustomChoice(t *testing.T) {
	f := today()
	f.Choice = &Choice{Bundle: "custom", Base: "core", Enable: []string{"subtitles"}}
	w := PlanChoice(components.Default(), f, *f.Choice)
	for id, want := range map[string]string{"subtitles": "enabled", "transcription": "enabled",
		"runtime.ml": "installed", "model.whisper-turbo": "installed", "search.semantic": "installed", "media.thumbnails": "enabled"} {
		if w[id] != want {
			t.Errorf("%s = %q, want %q", id, w[id], want)
		}
	}
}
