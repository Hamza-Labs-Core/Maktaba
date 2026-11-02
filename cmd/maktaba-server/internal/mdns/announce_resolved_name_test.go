package mdns

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// resolved reads the name it is given as a DNS name in presentation form (found by the real
// run in CI): a "." would split the label and a "\" start an escape, so both are escaped;
// "%" is doubled for its specifiers; the escaped name fits the 63 bytes resolved allows,
// cut on a character boundary.
func TestResolvedInstance(t *testing.T) {
	for in, want := range map[string]string{
		"Maktaba (studio)":      "Maktaba (studio)",
		"Studio 1.2":            `Studio 1\.2`,
		`Back\slash "home"`:     `Back\\slash "home"`,
		"100% sure.":            `100%% sure\.`,
		"مكتبة البيت":           "مكتبة البيت",
		"tab\there":             "tabhere",
		strings.Repeat(".", 40): strings.Repeat(`\.`, 31),
	} {
		if got := resolvedInstance(in); got != want {
			t.Errorf("resolvedInstance(%q) = %q, want %q", in, got, want)
		}
	}
	long := resolvedInstance(strings.Repeat("مكتبة.", 12))
	trailing := len(long) - len(strings.TrimRight(long, `\`))
	if !utf8.ValidString(long) || len(long) > 63 || len(long) < 55 || trailing%2 != 0 {
		t.Errorf("long name %q (%d bytes)", long, len(long))
	}
}
