package components

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"
)

var updatePluginGolden = flag.Bool("update-plugin-golden", false, "rewrite testdata/plugin_entries.dump.json")

// pluginEntriesDump is the merged catalog's dump of testdata/plugin_specs.json, cut to the
// plugin entries and the order: the text the Python twin must also answer (54.37 T02).
func pluginEntriesDump(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/plugin_specs.json")
	if err != nil {
		t.Fatal(err)
	}
	var in []struct {
		Manifest json.RawMessage `json:"manifest"`
		Since    string          `json:"since"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	var specs []PluginSpec
	for _, x := range in {
		s, err := PluginSpecOf(x.Manifest, x.Since)
		if err != nil {
			t.Fatal(err)
		}
		specs = append(specs, s)
	}
	cat, skipped := WithPlugins(Default(), specs)
	if len(skipped) > 0 {
		t.Fatalf("skipped %v", skipped)
	}
	full, err := cat.Dump()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(full, &doc); err != nil {
		t.Fatal(err)
	}
	var plugins []any
	for _, c := range doc["components"].([]any) {
		if c.(map[string]any)["kind"] == KindPlugin {
			plugins = append(plugins, c)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"components": plugins, "order": doc["order"]}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 54.37 T02 (go): the merged entries, as Python must derive them too.
func TestPluginEntriesGolden(t *testing.T) {
	got := pluginEntriesDump(t)
	if *updatePluginGolden {
		if err := os.WriteFile("testdata/plugin_entries.dump.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/plugin_entries.dump.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("testdata/plugin_entries.dump.json is stale: go test -run TestPluginEntriesGolden -update-plugin-golden")
	}
}

// The queue name is pluginmanifest.QueueName's (the shared vector).
func TestPluginQueueNameVector(t *testing.T) {
	raw, err := os.ReadFile("../plugins/testdata/stage_names.json")
	if err != nil {
		t.Fatal(err)
	}
	var v []struct{ ID, Stage, Name string }
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v {
		if got := PluginQueueName(c.ID, c.Stage); got != c.Name {
			t.Errorf("%s/%s: %s, want %s", c.ID, c.Stage, got, c.Name)
		}
	}
}
