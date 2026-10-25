package main

import (
	"testing"
	"time"
)

// Why a machine keeps the link from becoming required (story 35.16 EC3), and its clock
// (EC2) first: a machine 5 minutes off cannot use its new certificate.
func TestLinkBlockers(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	day := now.Add(-25 * time.Hour)
	hour := now.Add(-time.Hour)
	for _, c := range []struct {
		n    LinkNode
		full bool
		skew time.Duration
		want string
	}{
		{LinkNode{Connected: true, HasCert: true, Since: &day}, true, 0, ""},
		{LinkNode{Connected: true, HasCert: true, Since: &day}, false, 0, "worker_only"},
		{LinkNode{Connected: true, HasCert: true, Since: &day}, true, 6 * time.Minute, "clock_skew"},
		{LinkNode{Connected: true, HasCert: true, Since: &day}, true, -6 * time.Minute, "clock_skew"},
		{LinkNode{Connected: false, HasCert: false}, true, 0, "no_cert"},
		{LinkNode{Connected: false, HasCert: true}, true, 0, "not_connected"},
		{LinkNode{Connected: true, HasCert: true, Since: &hour}, true, 0, "under_a_day"},
	} {
		if got := blocks(c.n, c.full, c.skew, now); got != c.want {
			t.Errorf("%+v full=%v skew=%v: %q, want %q", c.n, c.full, c.skew, got, c.want)
		}
	}
	lr := linkRulesFrom(map[string]any{"link_mode": "require", "link_max_streams": float64(8), "tls_web_https": true, "link_mode_x": 1})
	if lr.Mode != "require" || lr.MaxStreams != 8 || !lr.WebHTTPS || !lr.AutoRequire {
		t.Fatalf("rules: %+v", lr)
	}
	if linkRulesFrom(map[string]any{"link_mode": "bogus"}).Mode != "prefer" {
		t.Fatal("a bad mode is not prefer")
	}
}
