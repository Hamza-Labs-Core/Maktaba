package components

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func load(t *testing.T, name string) error {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(b)
	return err
}

// T01: the real catalog loads, with about 30 switchable components, and requirements
// come first.
func TestDefaultLoads(t *testing.T) {
	c := Default()
	switchable := 0
	for _, comp := range c.Components() {
		if comp.Switchable {
			switchable++
		}
	}
	if switchable < 25 || switchable > 45 {
		t.Fatalf("%d switchable components, want about 30", switchable)
	}
	order := c.Order()
	if len(order) != len(c.IDs()) {
		t.Fatalf("order has %d ids, catalog %d", len(order), len(c.IDs()))
	}
	before := func(a, b string) {
		t.Helper()
		ia, ib := slices.Index(order, a), slices.Index(order, b)
		if ia < 0 || ib < 0 || ia > ib {
			t.Errorf("Order(): %s (%d) must come before %s (%d)", a, ia, b, ib)
		}
	}
	before("runtime.ml", "transcription")
	before("model.whisper-turbo", "transcription")
	before("transcription", "subtitles")
	before("livetv", "livetv.slots")
	before("cloud.link", "cloud.relay")
}

func TestAccessors(t *testing.T) {
	c := Default()
	cases := []struct {
		got  func() (*Component, bool)
		want string
	}{
		{func() (*Component, bool) { return c.ByStage("transcribe") }, "transcription"},
		{func() (*Component, bool) { return c.ByStage("scan") }, "core.pipeline"},
		{func() (*Component, bool) { return c.ByStage("plex_index") }, "ext.plex"},
		{func() (*Component, bool) { return c.ByProcess("pipeline", "stt-3") }, "transcription"},
		{func() (*Component, bool) { return c.ByProcess("pipeline", "") }, "core.pipeline"},
		{func() (*Component, bool) { return c.ByProcess("pipeline", "index") }, "search.semantic"},
		{func() (*Component, bool) { return c.ByProcess("tv-slot", "amber") }, "livetv.slots"},
		{func() (*Component, bool) { return c.ByProcess("ollama", "") }, "llm"},
		{func() (*Component, bool) { return c.ByLoop("api", "tvchange_controller") }, "livetv"},
		{func() (*Component, bool) { return c.ByLoop("pipeline", "channel-topup") }, "livetv"},
		{func() (*Component, bool) { return c.Get("llm") }, "llm"},
	}
	for i, tc := range cases {
		comp, ok := tc.got()
		if !ok || comp.ID != tc.want {
			t.Errorf("case %d: got %v, want %s", i, comp, tc.want)
		}
	}
	if _, ok := c.ByStage("nonsense"); ok {
		t.Error("an unknown stage has a component")
	}
	if _, ok := c.ByProcess("pipeline", "stt"); ok && len(c.MatchProcesses("pipeline", "stt")) != 0 {
		t.Error("stt-* must not match a bare stt")
	}
	reqs := c.Requires("transcription")
	if len(reqs) != 2 || !slices.Equal(reqs[0].AnyOf, []string{"runtime.ml"}) ||
		!slices.Equal(reqs[1].AnyOf, []string{"model.whisper-turbo", "model.whisper-large-v3", "model.whisper-medium"}) {
		t.Fatalf("Requires(transcription) = %v", reqs)
	}
	if deps := c.Dependents("livetv"); !slices.Contains(deps, "livetv.slots") || !slices.Contains(deps, "ext.dizquetv") {
		t.Fatalf("Dependents(livetv) = %v", deps)
	}
	tr, _ := c.Get("transcription")
	if tr.Drain.GraceS != 120 || !tr.Drain.Checkpoint {
		t.Fatalf("transcription drain = %+v", tr.Drain)
	}
	lv, _ := c.Get("livetv")
	if lv.StopGraceS != DefaultStopGraceS {
		t.Fatalf("a loop's stop grace defaults to %d, got %d", DefaultStopGraceS, lv.StopGraceS)
	}
	th, _ := c.Get("media.thumbnails")
	if th.Drain.GraceS != DefaultDrainGraceS || th.StopGraceS != 0 {
		t.Fatalf("defaults: %+v %d", th.Drain, th.StopGraceS)
	}
	wt, _ := c.Get("model.whisper-turbo")
	if !strings.Contains(wt.SourceFor("darwin-arm64"), "mlx-community") || !strings.Contains(wt.SourceFor("linux-amd64"), "faster-whisper") {
		t.Fatalf("whisper-turbo sources: %q / %q", wt.SourceFor("darwin-arm64"), wt.SourceFor("linux-amd64"))
	}
	if !c.IsReason("dependency_off") || c.IsReason("nonsense") || !c.IsPhase(PhaseRunning) {
		t.Fatal("IsReason / IsPhase")
	}
	full := c.InBundle("full")
	if !slices.Contains(full, "transcription") || slices.Contains(c.InBundle("core"), "transcription") {
		t.Fatalf("bundles: full %v", full)
	}
}

// T02: a requirement cycle is a load error that names it.
func TestCycleIsAnError(t *testing.T) {
	err := load(t, "cycle.toml")
	if err == nil || !strings.Contains(err.Error(), "requires cycle: a -> b -> a") {
		t.Fatalf("got %v", err)
	}
}

// T03: an unknown field names the field (and the component).
func TestUnknownFieldIsAnError(t *testing.T) {
	err := load(t, "unknown_field.toml")
	if err == nil || !strings.Contains(err.Error(), `unknown field "stagez"`) || !strings.Contains(err.Error(), "(a)") {
		t.Fatalf("got %v", err)
	}
}

// T10: core.* is never switchable.
func TestCoreSwitchableIsAnError(t *testing.T) {
	err := load(t, "core_switchable.toml")
	if err == nil || !strings.Contains(err.Error(), "core.* components must have switchable = false") {
		t.Fatalf("got %v", err)
	}
}

// T11: conflicts are listed on both sides.
func TestConflictsOnOneSideIsAnError(t *testing.T) {
	err := load(t, "conflicts_one_side.toml")
	if err == nil || !strings.Contains(err.Error(), "conflicts must be listed on both sides") {
		t.Fatalf("got %v", err)
	}
}

// A stage in two components names both.
func TestStageInTwoComponentsIsAnError(t *testing.T) {
	err := load(t, "stage_twice.toml")
	if err == nil || !strings.Contains(err.Error(), `stage "thumbnail" is in both a and b`) {
		t.Fatalf("got %v", err)
	}
}

func TestFieldRules(t *testing.T) {
	base, err := os.ReadFile(filepath.Join("testdata", "conflicts_one_side.toml"))
	if err != nil {
		t.Fatal(err)
	}
	fixed := strings.Replace(string(base), `conflicts = ["b"]`, "", 1)
	if _, err := Load([]byte(fixed)); err != nil {
		t.Fatalf("the fixed fixture must load: %v", err)
	}
	cases := map[string]struct{ from, to, want string }{
		"bad kind":             {`kind      = "loop"`, `kind      = "loop+stages"`, `bad kind`},
		"kind without list":    {`kind      = "loop"`, `kind      = "stages+loop"`, `but no stages`},
		"bad group":            {`group     = "system"`, `group     = "nope"`, `bad group`},
		"bad title key":        {`title_key = "components.a.title"`, `title_key = "x"`, `title_key must be`},
		"bad platform":         {`platforms = ["linux-amd64"]`, `platforms = ["beos"]`, `bad platforms entry`},
		"unknown requirement":  {`since     = "0.3.66"`, "since = \"0.3.66\"\nrequires = [\"zz\"]", `requires unknown component "zz"`},
		"bad since":            {`since     = "0.3.66"`, `since     = "soon"`, `since must be`},
		"only core unswitched": {`since     = "0.3.66"`, "since = \"0.3.66\"\nswitchable = false", `only core.*`},
		"bad loop host":        {`loops     = ["api:a"]`, `loops     = ["web:a"]`, `bad loops entry`},
		"asset with loops":     {`kind      = "loop"`, `kind      = "asset"`, `an asset has no loops`},
		"source on non-asset":  {`since     = "0.3.66"`, "since = \"0.3.66\"\nsource = \"hf:x\"", `for assets only`},
		"unknown feature":      {`since     = "0.3.66"`, "since = \"0.3.66\"\nfeatures = [\"nope\"]", `bad features entry`},
		"bad phases":           {`"failed"]`, `"failed", "zz"]`, `phases must be exactly`},
	}
	for name, tc := range cases {
		src := strings.Replace(fixed, tc.from, tc.to, 1)
		if src == fixed {
			t.Fatalf("%s: the replacement did not apply", name)
		}
		_, err := Load([]byte(src))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

// T09 (Go side): the dump of the embedded catalog is testdata/catalog.dump.json, which
// the pipeline's loader must answer too ("make catalog-sync" rewrites it).
func TestDumpMatchesTheSharedFixture(t *testing.T) {
	got, err := Default().Dump()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "catalog.dump.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/catalog.dump.json is stale: run make catalog-sync")
	}
}

// Every licence a component names is a flag of the API's declarations.
func TestLicencesAreDeclaredFlags(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "api", "internal", "flags", "declarations.go"))
	if err != nil {
		t.Skipf("the API's flag declarations are not beside this module: %v", err)
	}
	for _, comp := range Default().Components() {
		if comp.Licence == "" {
			continue
		}
		if !regexp.MustCompile(`=\s*"` + regexp.QuoteMeta(comp.Licence) + `"`).Match(b) {
			t.Errorf("%s: licence %q is not a flag in api/internal/flags/declarations.go", comp.ID, comp.Licence)
		}
	}
}

// The copy the pipeline embeds is this file, byte for byte (T05, Go side).
func TestPipelineCopyIsCurrent(t *testing.T) {
	copyPath := filepath.Join("..", "..", "pipeline", "src", "maktaba_pipeline", "components", "catalog.toml")
	b, err := os.ReadFile(copyPath)
	if err != nil {
		t.Skipf("no pipeline beside this module: %v", err)
	}
	if !bytes.Equal(b, Raw()) {
		t.Fatal("pipeline/src/maktaba_pipeline/components/catalog.toml differs: run make catalog-sync")
	}
}

// Story 51.15 T11: a model's source is pinned to a commit (or an ollama digest) and has
// its files; a branch, an unpinned source or a file of no source is refused, naming the
// entry.
func TestModelSourcesArePinned(t *testing.T) {
	raw := string(Raw())
	const medium = `source     = "hf:Systran/faster-whisper-medium@08e178d48790749d25932bbc082711ddcfdfbc4f"`
	if !strings.Contains(raw, medium) {
		t.Fatal("the test's anchor is gone from the catalog")
	}
	for name, c := range map[string]struct{ old, new, want string }{
		"branch": {medium, `source     = "hf:Systran/faster-whisper-medium@main"`,
			`component model.whisper-medium: source "hf:Systran/faster-whisper-medium@main" must be pinned`},
		"unpinned": {medium, `source     = "hf:Systran/faster-whisper-medium"`, "must be pinned"},
		"other commit, no files": {`darwin-arm64 = "hf:mlx-community/whisper-large-v3-turbo@a4aaeec0636e6fef84abdcbe3544cb2bf7e9f6fb" }`,
			`darwin-arm64 = "hf:mlx-community/whisper-large-v3-turbo@` + strings.Repeat("1", 40) + `" }`, "has no files"},
		"ollama tag":         {`"ollama:qwen2.5:7b@sha256:`, `"ollama:qwen2.5:7b@latest`, "must be pinned as ollama"},
		"gated not an asset": {`kind      = "loop"`, "kind      = \"loop\"\ngated     = true", "for assets only"},
	} {
		src := strings.Replace(raw, c.old, c.new, 1)
		if src == raw {
			t.Fatalf("%s: nothing replaced", name)
		}
		_, err := Load([]byte(src))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v; want %q", name, err, c.want)
		}
	}
	m, _ := Default().Get("model.whisper-turbo")
	if len(m.FilesOf(m.SourceFor("darwin-arm64"))) == 0 || len(m.FilesOf(m.Source)) == 0 {
		t.Fatal("model.whisper-turbo has no files for one of its sources")
	}
	if c, _ := Default().Get("model.cohere-arabic"); !c.Gated {
		t.Fatal("model.cohere-arabic is gated")
	}
}
