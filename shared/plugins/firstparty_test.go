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
