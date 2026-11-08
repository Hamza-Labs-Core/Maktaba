package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

// The embedded first-party bundle parses, and its runner groups follow its manifests
// (story 54.23 §4): none while the release has no first-party plugin.
func TestFirstPartyBundleAndGroups(t *testing.T) {
	var doc struct {
		Schema  int   `json:"schema"`
		Plugins []any `json:"plugins"`
	}
	if err := json.Unmarshal(FirstParty, &doc); err != nil || doc.Schema != 1 {
		t.Fatalf("firstparty.json: %v", err)
	}
	groups := FirstPartyGroups()
	if len(doc.Plugins) == 0 && len(groups) != 0 {
		t.Fatalf("groups %v with no plugin", groups)
	}
	if len(doc.Plugins) > 0 && !strings.Contains(strings.Join(groups, ","), "fp") {
		t.Fatalf("no fingerprint runner in %v", groups)
	}
	if !strings.Contains(string(ReservedStages), "\ntranscribe\n") {
		t.Fatal("reserved_stages.txt has no transcribe")
	}
}

// The flow files embedded beside the bundle parse (story 54.39 §1): the schema a flow.toml
// is read against and the flow check's words in both languages.
func TestEmbeddedFlowFiles(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(FlowSchema, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("flow.schema.json: %v", err)
	}
	var words map[string]struct{ En, Ar string }
	if err := json.Unmarshal(FlowMessages, &words); err != nil || len(words) == 0 {
		t.Fatalf("flow-messages.json: %v", err)
	}
	for code, w := range words {
		if !strings.HasPrefix(code, "flow_") && !strings.HasPrefix(code, "plugin_flow_") || w.En == "" || w.Ar == "" {
			t.Errorf("%s: %+v", code, w)
		}
	}
}

// Each runner group's components follow the bundle (story 54.37 §4): every group the
// release runs serves at least one component (else it would be held for good), and the
// fingerprint runner serves every first-party component.
func TestFirstPartyGroupComponents(t *testing.T) {
	comps := FirstPartyGroupComponents()
	groups := FirstPartyGroups()
	if len(groups) == 0 {
		if len(comps) != 0 {
			t.Fatalf("components %v with no group", comps)
		}
		return
	}
	all := map[string]bool{}
	for g, cs := range comps {
		for _, c := range cs {
			if g != "fp" {
				all[c] = true
			}
		}
	}
	for _, g := range groups {
		if len(comps[g]) == 0 {
			t.Fatalf("group %s serves no component: %v", g, comps)
		}
	}
	fp := map[string]bool{}
	for _, c := range comps["fp"] {
		fp[c] = true
	}
	for c := range all {
		if !fp[c] {
			t.Fatalf("fp does not serve %s: %v", c, comps["fp"])
		}
	}
}

// Each first-party stage has its runner group as the pipeline reads it: a stage's own
// runner (books: net) before its plugin's (classify: light); a slotted runner's instance
// (ml-3) is its group's.
func TestFirstPartyStageGroups(t *testing.T) {
	g := FirstPartyStageGroups()
	for stage, want := range map[string]string{"books": "net", "detect": "light", "tag": "light"} {
		if g[stage] != want {
			t.Errorf("%s runs in %q, want %q", stage, g[stage], want)
		}
	}
	for in, want := range map[string]string{"ml-3": "ml", "ml": "ml", "light": "light", "core-media": "core-media"} {
		if got := RunnerGroupOf(in); got != want {
			t.Errorf("RunnerGroupOf(%q) = %q, want %q", in, got, want)
		}
	}
}
