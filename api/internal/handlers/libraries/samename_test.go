package libraries

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

// A name typed to confirm a merge or a delete matches however a person types it; another
// library's name never does.
func TestSameName(t *testing.T) {
	name := "د. عثمان الخميس"
	for _, typed := range []string{
		name,
		"  " + name + " ",
		"د.  عثمان   الخميس",
		norm.NFD.String(name),
		"\u2067" + name + "\u2069",
	} {
		if !sameName(typed, name) {
			t.Errorf("%q should match %q", typed, name)
		}
	}
	if sameName("dr. othman", "Dr. Othman") {
		t.Error("letter case still counts")
	}
	for _, typed := range []string{"", "د. عثمان", "Dr. Othman (dup)"} {
		if sameName(typed, name) {
			t.Errorf("%q must not match %q", typed, name)
		}
	}
}
