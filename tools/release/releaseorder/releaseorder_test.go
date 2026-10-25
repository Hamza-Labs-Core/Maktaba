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

// The guard's whole answer: a re-run of a published tag is published (nothing signed is
// built again), the newest release other than the tag is named (its commit must be in the
// tag's history), and only the highest stable is the latest (a maintenance release never).
func TestLocate(t *testing.T) {
	for _, c := range []struct {
		tag, allow string
		want       Place
	}{
		{tag: "v0.3.2", want: Place{Newest: "v0.3.1", Latest: true}},
		{tag: "v0.4.0-rc.1", want: Place{Newest: "v0.3.1"}},
		{tag: "v0.3.1", want: Place{Published: true, Newest: "v0.3.0", Latest: true}},
		{tag: "v0.2.5", allow: "v0.2.5", want: Place{Maintenance: true, Newest: "v0.3.1"}},
	} {
		got, err := Locate(c.tag, published, c.allow)
		if err != nil || got != c.want {
			t.Errorf("Locate(%s, allow %q) = %+v, %v; want %+v", c.tag, c.allow, got, err, c.want)
		}
	}
	if _, err := Locate("v0.3.0-rc.7", published, ""); err == nil {
		t.Fatal("refused as Guard refuses it")
	}
	if p, err := Locate("v0.1.0", nil, ""); err != nil || p.Newest != "" || !p.Latest {
		t.Fatalf("the first release: %+v %v", p, err)
	}
}
