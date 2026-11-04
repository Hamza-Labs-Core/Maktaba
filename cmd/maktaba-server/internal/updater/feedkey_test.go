package updater

import "testing"

// Story 51.17: a Core app takes the Core feed key when the release has one, else the
// Full key (a superset); a Full app never takes the Core key.
func TestFeedKeyByFlavour(t *testing.T) {
	p := feedPlatform()
	both := []string{p + "-core-app", p + "-core", p + "-app", p, "other-os"}
	full := []string{p + "-app", p}
	for _, c := range []struct {
		keys []string
		core bool
		want string
	}{
		{both, true, p + "-core"},
		{both, false, p},
		{full, true, p},
		{full, false, p},
		{[]string{p + "-core"}, false, ""},
	} {
		k, ok := feedKey(append([]string(nil), c.keys...), c.core)
		if c.want == "" {
			if ok {
				t.Errorf("%v core=%v: %s, want none", c.keys, c.core, k)
			}
			continue
		}
		// a key of the same app (its installer key or the bare one) is fine
		if !ok || (k != c.want && k != c.want+"-app") {
			t.Errorf("%v core=%v: %q, want %s", c.keys, c.core, k, c.want)
		}
	}
}
