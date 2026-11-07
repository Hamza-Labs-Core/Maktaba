package components

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

type gateCase struct {
	Name   string            `json:"name"`
	In     PluginGateInput   `json:"in"`
	Reason string            `json:"reason"`
	Params map[string]string `json:"params"`
}

// 54.37 T04 (go): every gate input gives the story's reason; the Python twin reads the
// same table.
func TestPluginGatesTable(t *testing.T) {
	raw, err := os.ReadFile("testdata/plugin_gates.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []gateCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	// story 54.21: this release serves plugin API 1.1 (compute), as the host's audit does
	if fmt.Sprintf("%d.%d", PluginAPIMajor, PluginAPIMinor) != "1.1" {
		t.Fatalf("serves %d.%d", PluginAPIMajor, PluginAPIMinor)
	}
	if len(doc.Cases) < 10 {
		t.Fatalf("only %d cases", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		got, params := PluginGate(c.In)
		if got != c.Reason {
			t.Errorf("%s: reason %q, want %q", c.Name, got, c.Reason)
		}
		for k, v := range c.Params {
			if fmt.Sprint(params[k]) != v {
				t.Errorf("%s: param %s = %v, want %s", c.Name, k, params[k], v)
			}
		}
	}
}

func TestLiveCatalogSwapsWhole(t *testing.T) {
	t.Cleanup(func() { SetLive(nil) })
	if Live() != Default() {
		t.Fatal("before a swap the live catalog is the static one")
	}
	e := PluginEntry(PluginSpec{ID: "dev.example.live", Stages: []string{"p_00000000_x"}})
	cat, err := With(Default(), []*Component{e})
	if err != nil {
		t.Fatal(err)
	}
	SetLive(cat)
	if _, ok := Live().Get("plugin.dev.example.live"); !ok {
		t.Fatal("the swapped catalog does not answer the plugin")
	}
	SetLive(nil)
	if _, ok := Live().Get("plugin.dev.example.live"); ok {
		t.Fatal("SetLive(nil) kept the plugin")
	}
}
