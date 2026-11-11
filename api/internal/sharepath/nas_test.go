package sharepath

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The NAS rules agree with the pipeline's: both read the shared fixtures (story 59.13).
func TestNASRulesMatchTheSharedFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "testdata", "sharepath", "nas_local_roots.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Rule []struct {
			Platform string  `json:"platform"`
			Path     string  `json:"path"`
			Share    *string `json:"share"`
			Root     string  `json:"root"`
		} `json:"rule"`
		HostPath []struct {
			Platform string `json:"platform"`
			Path     string `json:"path"`
			Host     bool   `json:"host"`
		} `json:"host_path"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Rule) == 0 || len(fx.HostPath) == 0 {
		t.Fatal("no fixtures")
	}
	for _, c := range fx.Rule {
		share, root, ok := RuleShare(c.Platform, c.Path)
		switch {
		case c.Share == nil && ok:
			t.Errorf("%s %s: got share %q, want none", c.Platform, c.Path, share)
		case c.Share != nil && (!ok || share != *c.Share || root != c.Root):
			t.Errorf("%s %s: got (%q, %q, %v), want (%q, %q)", c.Platform, c.Path, share, root, ok, *c.Share, c.Root)
		}
	}
	for _, c := range fx.HostPath {
		if got := IsHostPath(c.Platform, c.Path); got != c.Host {
			t.Errorf("IsHostPath(%s, %s) = %v, want %v", c.Platform, c.Path, got, c.Host)
		}
	}
}
