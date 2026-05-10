package libraries

import (
	"testing"
)

func TestCanonicalPath_StripsTrailingSlash(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/mnt/media", "/mnt/media"},
		{"/mnt/media/", "/mnt/media"},
		{"/mnt/media//", "/mnt/media"},
		{"  /mnt/media/  ", "/mnt/media"},
		{"/mnt/./media", "/mnt/media"},
		{"/mnt/media/sub/..", "/mnt/media"},
		{"/", "/"},
		{"", ""},
	}
	for _, c := range cases {
		if got := canonicalPath(c.in); got != c.want {
			t.Errorf("canonicalPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
