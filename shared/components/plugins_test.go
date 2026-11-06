package components

import (
	"slices"
	"strings"
	"testing"
)

// 54.37 T01: a static plugin entry, or an id starting plugin., is refused.
func TestCatalogRefusesStaticPluginEntries(t *testing.T) {
	base := string(Raw())
	for _, entry := range []string{
		"\n[[component]]\nid = \"plugin.dev.example.x\"\nkind = \"stages\"\ngroup = \"plugins\"\ntitle_key = \"a\"\ndesc_key = \"b\"\nstages = [\"p_x\"]\nplatforms = [\"darwin-arm64\"]\nsince = \"1\"\n",
		"\n[[component]]\nid = \"x.y\"\nkind = \"plugin\"\ngroup = \"plugins\"\ntitle_key = \"a\"\ndesc_key = \"b\"\nplatforms = [\"darwin-arm64\"]\nsince = \"1\"\n",
	} {
		if _, err := Load([]byte(base + entry)); err == nil || !strings.Contains(err.Error(), "plugin") {
			t.Fatalf("a static plugin entry was accepted: %v", err)
		}
	}
}

// 54.37 T02, T06: entries merge with the static catalog; the reason codes are listed.
func TestWithPluginEntries(t *testing.T) {
	static := Default()
	a := PluginEntry(PluginSpec{ID: "dev.example.a", Stages: []string{"p_aaaaaaaa_x"}, Platforms: []string{"any"}, TimeoutS: 600,
		Required: []string{"compute.llm"}, Optional: []string{"compute.embed"}})
	b := PluginEntry(PluginSpec{ID: "dev.example.b", Stages: []string{"p_bbbbbbbb_x"}, Platforms: []string{"linux-amd64"}, TimeoutS: 30})
	cat, err := With(static, []*Component{a, b})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cat.Get("plugin.dev.example.a")
	if !ok || got.Kind != KindPlugin || got.Group != PluginsGroup || got.Placement != "home" {
		t.Fatalf("entry %+v", got)
	}
	if got.Drain.GraceS != 120 || !got.Drain.Checkpoint || len(got.Platforms) != 4 || len(got.Bundles) != 0 {
		t.Fatalf("derived fields %+v", got)
	}
	if !slices.Equal(got.Requires, []string{PluginsHost, "llm"}) || !slices.Equal(got.Wants, []string{"search.semantic"}) {
		t.Fatalf("requires %v wants %v", got.Requires, got.Wants)
	}
	if c, _ := cat.ByStage("p_bbbbbbbb_x"); c == nil || c.ID != "plugin.dev.example.b" {
		t.Fatal("stage not owned")
	}
	if !slices.Contains(cat.Dependents(PluginsHost), "plugin.dev.example.b") {
		t.Fatal("plugins.host does not list its plugins")
	}
	order := cat.Order()
	if slices.Index(order, PluginsHost) > slices.Index(order, "plugin.dev.example.a") {
		t.Fatal("a plugin is ordered before its host")
	}
	if _, ok := static.Get("plugin.dev.example.a"); ok {
		t.Fatal("With changed the static catalog")
	}
	if _, err := With(static, []*Component{a, a}); err == nil {
		t.Fatal("an entry listed twice was accepted")
	}
	if _, err := With(static, []*Component{{ID: "plugin.x", Kind: KindStages}}); err == nil {
		t.Fatal("a non-plugin entry was accepted")
	}
	stolen := PluginEntry(PluginSpec{ID: "dev.example.c", Stages: []string{"transcribe"}})
	if _, err := With(static, []*Component{stolen}); err == nil {
		t.Fatal("a plugin took a core stage")
	}
	for _, r := range PluginReasons {
		if !static.IsReason(r) {
			t.Errorf("reason %s is not in catalog.toml", r)
		}
	}
	host, _ := static.Get(PluginsHost)
	if !host.OnDemand || !slices.Contains(host.Bundles, "core") {
		t.Fatalf("plugins.host %+v", host)
	}
}
