package servers

import "testing"

func TestSlugRegex(t *testing.T) {
	cases := []struct {
		s  string
		ok bool
	}{
		{"abc", true},
		{"a", false},
		{"-abc", false},
		{"abc-", false},
		{"my-server-1", true},
		{"MyServer", false},
		{"a234567890123456789012345678901", false}, // 31 chars
	}
	for _, c := range cases {
		got := slugRE.MatchString(c.s)
		if got != c.ok {
			t.Errorf("slugRE(%q) = %v, want %v", c.s, got, c.ok)
		}
	}
}

func TestNewServerSecret(t *testing.T) {
	s1, err := newServerSecret()
	if err != nil {
		t.Fatalf("newServerSecret: %v", err)
	}
	s2, _ := newServerSecret()
	if s1 == s2 {
		t.Errorf("secrets should be unique")
	}
	if len(s1) < 32 {
		t.Errorf("secret too short: %q", s1)
	}
}
