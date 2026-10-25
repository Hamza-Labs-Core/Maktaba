package nodecopy

import "testing"

func TestReusable(t *testing.T) {
	g := Grant{Slot: "maktaba_copy_c", SlotExists: true, Publication: "maktaba_copy_c", SystemID: "B"}
	base := sub{exists: true, slot: "maktaba_copy_c", pubs: []string{"maktaba_copy_c"}, conninfo: "host=b"}
	mark := Marker{SystemID: "B"}
	with := func(f func(*sub)) sub { s := base; f(&s); return s }
	cases := []struct {
		name string
		s    sub
		mk   Marker
		conn string
		want bool
	}{
		{"the same home, nothing changed", base, mark, "host=b", true},
		{"the home moved, every table copied", base, mark, "host=b2", true},
		{"tables mid-copy, the same server", with(func(s *sub) { s.syncing = 2 }), mark, "host=b", true},
		// the flake in TestSwitchTheHome: a table half copied from A, pointed at B
		{"tables mid-copy, pointed elsewhere", with(func(s *sub) { s.syncing = 1 }), mark, "host=b2", false},
		{"another home's database", base, Marker{SystemID: "A"}, "host=b", false},
		{"no marker yet", base, Marker{}, "host=b", true},
		{"no subscription", sub{}, mark, "host=b", false},
		{"another slot", with(func(s *sub) { s.slot = "x" }), mark, "host=b", false},
		{"another publication", with(func(s *sub) { s.pubs = []string{"x"} }), mark, "host=b", false},
	}
	for _, c := range cases {
		if got := reusable(c.s, g, c.mk, c.conn); got != c.want {
			t.Errorf("%s: reusable = %v, want %v", c.name, got, c.want)
		}
	}
	gone := g
	gone.SlotExists = false
	if reusable(base, gone, mark, "host=b") {
		t.Error("the home's slot is gone: reusable")
	}
}
