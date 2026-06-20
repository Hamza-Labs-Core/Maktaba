package metrics

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		q                 string
		wantKey           string
		wantApproxDaysAgo float64
	}{
		{"today", "today", 0},
		{"7d", "7d", 7},
		{"", "7d", 7},
		{"30d", "30d", 30},
		{"90d", "90d", 90},
		{"bogus", "7d", 7},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x?range="+c.q, nil)
		start, key := parseRange(r)
		if key != c.wantKey {
			t.Errorf("parseRange(%q) key = %q, want %q", c.q, key, c.wantKey)
		}
		daysAgo := now.Sub(start).Hours() / 24
		if daysAgo < c.wantApproxDaysAgo-1 || daysAgo > c.wantApproxDaysAgo+1.1 {
			t.Errorf("parseRange(%q) start = %v (%.1f days ago), want ~%.0f", c.q, start, daysAgo, c.wantApproxDaysAgo)
		}
	}
}

func TestDomainOf(t *testing.T) {
	cases := map[string]string{
		"alice@hamzalabs.com": "hamzalabs.com",
		"BOB@Hamza.IO":        "hamza.io",
		"noatsign":            "",
		"":                    "",
	}
	for in, want := range cases {
		if got := domainOf(in); got != want {
			t.Errorf("domainOf(%q) = %q, want %q", in, got, want)
		}
	}
}
