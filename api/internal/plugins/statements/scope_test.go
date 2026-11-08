package statements

import (
	"strings"
	"testing"
)

func TestSettingsInScope(t *testing.T) {
	tv := []string{"planner", "tv"}
	for _, c := range []struct {
		key      string
		sections []string
		want     bool
	}{
		{"rules.tv", tv, true},
		{"rules_version.planner", tv, true},
		// the tv rules still read the dizqueTV export's live list (slot 0215 keeps the row)
		{"rules.dizquetv", tv, true},
		{"rules.dizquetv", []string{"planner"}, false},
		{"rules_version.dizquetv", tv, false},
		{"rules.detect", tv, false},
		{"plugins.tv.maktaba.livetv", tv, false},
		{"", tv, false},
		{"rules.tv", nil, false},
	} {
		if got := settingsInScope(c.key, c.sections); got != c.want {
			t.Errorf("settingsInScope(%q, %v) = %v, want %v", c.key, c.sections, got, c.want)
		}
	}
}

func TestScopeValues(t *testing.T) {
	u := "0b8f8a52-61a4-4d0a-9d8a-9a3f4bd2d001"
	for _, c := range []struct {
		in   any
		want []string
		ok   bool
	}{
		{nil, nil, true},
		{u, []string{u}, true},
		{"{" + u + "}", []string{"{" + u + "}"}, true}, // a braced uuid is one id
		{"{" + u + "," + u + "}", []string{u, u}, true},
		{int64(42), []string{"42"}, true},
		{"{1,2}", []string{"1", "2"}, true},
		{`["a","b"]`, []string{"a", "b"}, true},
		{`[1,2]`, []string{"1", "2"}, true},
		{`[1,"a"]`, nil, false},
		{true, nil, false},
		{1.5, nil, false},
	} {
		got, ok := scopeValues(c.in)
		if ok != c.ok || strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("scopeValues(%v) = %v %v, want %v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestRegisterRefusesAScopeTheAPIDoesNotCheck(t *testing.T) {
	e := New(nil)
	for _, st := range []Statement{
		{Name: "x", Mode: "write", Scope: "season", ScopeArg: 1, Also: []Check{{"nope", 2}}},
		{Name: "x", Mode: "write", Scope: "catalog", Also: []Check{{"season", 1}}},
		{Name: "x", Mode: "write", Scope: "season", ScopeArg: 0},
	} {
		if err := e.Register("tv.maktaba.x", []Statement{st}); err == nil {
			t.Errorf("registered %+v", st)
		}
	}
	ok := Statement{Name: "x", Mode: "write", Scope: "season", ScopeArg: 1, Also: []Check{{"library", 2}}}
	if err := e.Register("tv.maktaba.x", []Statement{ok}); err != nil {
		t.Fatal(err)
	}
}

func TestSameIDReadsUUIDs(t *testing.T) {
	u := "0b8f8a52-61a4-4d0a-9d8a-9a3f4bd2d001"
	if !sameID(strings.ToUpper(u), u) || !sameID(strings.ReplaceAll(u, "-", ""), u) || sameID(u, "") || sameID("x", "x") {
		t.Fatal("sameID")
	}
}
