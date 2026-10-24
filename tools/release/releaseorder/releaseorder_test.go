package releaseorder

import (
	"strings"
	"testing"
)

// The tags this repository published around v0.3.0 (rc.7 came after v0.3.0).
var published = []string{"v0.2.0", "v0.3.0-rc.5", "v0.3.0-rc.6", "v0.3.0", "v0.3.1"}

func TestGuard(t *testing.T) {
	for _, c := range []struct {
		tag, allow  string
		ok, maint   bool
		errContains string
	}{
		{tag: "v0.3.2", ok: true},
		{tag: "v0.4.0-rc.1", ok: true},
		{tag: "v1.0.0", ok: true},
		{tag: "v0.3.1", ok: true}, // its own release, run again
		{tag: "v0.3.0-rc.7", errContains: "pre-release below the newest published release v0.3.1"},
		{tag: "v0.3.1-rc.1", errContains: "pre-release below"},
		{tag: "v0.2.5", errContains: "MAKTABA_RELEASE_ALLOW_TAG=v0.2.5"},
		{tag: "v0.2.5", allow: "v0.2.5", ok: true, maint: true},
		{tag: "v0.2.5", allow: "v0.2.6", errContains: "not above"},
		{tag: "0.3.2", errContains: "not a release tag"},
	} {
		maint, err := Guard(c.tag, published, c.allow)
		if c.ok != (err == nil) || maint != c.maint || (c.errContains != "" && (err == nil || !strings.Contains(err.Error(), c.errContains))) {
			t.Errorf("Guard(%s, allow %q) = %v, %v", c.tag, c.allow, maint, err)
		}
	}
	if _, err := Guard("v0.1.0", nil, ""); err != nil {
		t.Fatalf("the first release: %v", err)
	}
}

func TestPrevious(t *testing.T) {
	for tag, want := range map[string]string{
		"v0.3.2":      "v0.3.1",
		"v0.4.0":      "v0.3.1",
		"v0.4.0-rc.1": "v0.3.1",
		"v0.3.1":      "v0.3.0", // its own tag left out
		"v0.3.0":      "v0.2.0", // a stable tag: not its own release candidates
		"v0.3.0-rc.7": "v0.3.0-rc.6",
		"v0.1.0":      "",
	} {
		if got := Previous(tag, published); got != want {
			t.Errorf("Previous(%s) = %q, want %q", tag, got, want)
		}
	}
	if got := Previous("v0.4.0", append(published, "v0.4.0-rc.3")); got != "v0.3.1" {
		t.Errorf("a stable tag after its release candidates: %q", got)
	}
}

func TestLatest(t *testing.T) {
	for tag, want := range map[string]bool{
		"v0.3.2": true, "v1.0.0": true, "v0.3.1": true, // its own release still the highest
		"v0.4.0-rc.1": false, "v0.2.5": false, "v0.3.0": false,
	} {
		if got := Latest(tag, published); got != want {
			t.Errorf("Latest(%s) = %v, want %v", tag, got, want)
		}
	}
}
