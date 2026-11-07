package plugins

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Story 54.19 T03 (home side): the shared vectors, signed by the store's TypeScript signer
// (store/commerce/entitlements.test.ts reproduces them), verified offline here.
func TestEntitlementVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../shared/plugins/testdata/entitlements/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Home struct {
			ID    string `json:"id"`
			KeyFP string `json:"key_fp"`
		} `json:"home"`
		Keys  []StoreKey `json:"keys"`
		Cases []struct {
			Name        string            `json:"name"`
			Entitlement SignedEntitlement `json:"entitlement"`
			Now         time.Time         `json:"now"`
			Want        string            `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) < 9 {
		t.Fatalf("%d cases", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		b, err := VerifyEntitlement(c.Entitlement, doc.Keys, doc.Home.ID, doc.Home.KeyFP, c.Now)
		got := ""
		if err != nil {
			got = err.(*EntitlementError).Code
		}
		if got != c.Want {
			t.Errorf("%s: %q, want %q", c.Name, got, c.Want)
		}
		if err == nil && b.PluginID != "dev.example.loudness" {
			t.Errorf("%s: plugin %q", c.Name, b.PluginID)
		}
	}
	all := make([]SignedEntitlement, 0, len(doc.Cases))
	for _, c := range doc.Cases {
		all = append(all, c.Entitlement)
	}
	ok, bad := EntitledPlugins(all, doc.Keys, doc.Home.ID, doc.Home.KeyFP, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	if !ok["dev.example.loudness"] || ok["dev.example.other"] || len(bad) == 0 {
		t.Fatalf("entitled %v, refused %v", ok, bad)
	}
}

func TestCanonicalJSONSortsAndKeepsNumbers(t *testing.T) {
	got, err := canonicalJSON([]byte(`{"b": 1, "a": {"y": "<x>", "x": [2.50, true]}}`))
	if err != nil || string(got) != `{"a":{"x":[2.50,true],"y":"<x>"},"b":1}` {
		t.Fatalf("%s %v", got, err)
	}
}
