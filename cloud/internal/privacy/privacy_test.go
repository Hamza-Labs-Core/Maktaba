package privacy

import (
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestHashServerID(t *testing.T) {
	const id = "11111111-1111-1111-1111-111111111111"
	h1 := HashServerID("salt-a", id)
	h2 := HashServerID("salt-a", id)
	h3 := HashServerID("salt-b", id)

	if h1 != h2 {
		t.Errorf("hash not deterministic: %q != %q", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("different salt produced same hash: %q", h1)
	}
	if len(h1) != 16 {
		t.Errorf("hash len = %d, want 16", len(h1))
	}
	if strings.Contains(h1, id) {
		t.Errorf("hash leaks raw id: %q", h1)
	}
}

func TestNormalizeCountry(t *testing.T) {
	cases := map[string]string{
		"DE":   "DE",
		"de":   "DE",
		" us ": "US",
		"XX":   "", // Cloudflare unknown
		"T1":   "", // Tor
		"":     "",
		"D":    "",
		"DEU":  "",
		"D1":   "", // not two letters
		"12":   "",
	}
	for in, want := range cases {
		if got := NormalizeCountry(in); got != want {
			t.Errorf("NormalizeCountry(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCountryFromRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("CF-IPCountry", "FR")
	r.RemoteAddr = "203.0.113.7:443"
	if got := CountryFromRequest(r, ""); got != "FR" {
		t.Errorf("CountryFromRequest = %q, want FR", got)
	}

	// Unknown / absent header → "".
	r2 := httptest.NewRequest("GET", "/", nil)
	if got := CountryFromRequest(r2, ""); got != "" {
		t.Errorf("CountryFromRequest(absent) = %q, want ''", got)
	}
	r3 := httptest.NewRequest("GET", "/", nil)
	r3.Header.Set("CF-IPCountry", "XX")
	if got := CountryFromRequest(r3, ""); got != "" {
		t.Errorf("CountryFromRequest(XX) = %q, want ''", got)
	}

	// Custom header name is honoured.
	r4 := httptest.NewRequest("GET", "/", nil)
	r4.Header.Set("X-Geo", "JP")
	if got := CountryFromRequest(r4, "X-Geo"); got != "JP" {
		t.Errorf("CountryFromRequest(custom) = %q, want JP", got)
	}
}

func TestPolicyAndProcessingRecords(t *testing.T) {
	p := CurrentPolicy()
	if p.Controller == "" || p.Contact == "" || p.LawfulBasis == "" {
		t.Errorf("policy has empty required fields: %+v", p)
	}
	if p.RetentionDays != RetentionDays {
		t.Errorf("policy retention = %d, want %d", p.RetentionDays, RetentionDays)
	}
	if len(p.NotCollected) == 0 {
		t.Errorf("policy should enumerate not-collected categories")
	}

	recs := ProcessingRecords()
	if len(recs) == 0 {
		t.Fatal("expected at least one Article 30 record")
	}
	for _, a := range recs {
		if a.Name == "" || a.Purpose == "" || a.Retention == "" || len(a.Safeguards) == 0 {
			t.Errorf("incomplete processing activity: %+v", a)
		}
	}
	// the account mails' processor is named, and what it is given
	var mail ProcessingActivity
	for _, a := range recs {
		if a.Name == "Account mail" {
			mail = a
		}
	}
	if !strings.Contains(strings.Join(mail.Recipients, " "), "Resend") || !strings.Contains(strings.Join(mail.Categories, " "), "Email address") {
		t.Errorf("account mail: %+v", mail)
	}
	// where it is stored (the sending region is not where) and for how long, in both records
	for what, s := range map[string]string{"record": strings.Join(mail.Recipients, " ") + mail.Retention, "policy": strings.Join(p.Processors, " ")} {
		for _, must := range []string{"Resend", "stored in the United States", "30 days", "suppression list"} {
			if !strings.Contains(s, must) {
				t.Errorf("the %s does not say %q: %s", what, must, s)
			}
		}
		if strings.Contains(s, "EU region") {
			t.Errorf("the %s: %s", what, s)
		}
	}
	// the bot check's processor, said for when it is on
	if s := strings.Join(p.Processors, "; "); !strings.Contains(s, "Cloudflare (the bot check") || !strings.Contains(s, "when it is on") {
		t.Errorf("processors: %s", s)
	}
	if n := slices.IndexFunc(recs, func(a ProcessingActivity) bool { return a.Name == "Bot check (when it is on)" }); n < 0 || !strings.Contains(strings.Join(recs[n].Categories, " "), "IP address") {
		t.Error("the record of processing has no bot check")
	}
	// what an account holds is said, and "no IP addresses" of the analytics only
	if !strings.Contains(strings.Join(p.AccountData, " "), "Email address") || !strings.Contains(strings.Join(p.AccountData, " "), "35 days") {
		t.Errorf("account data: %v", p.AccountData)
	}
	for _, must := range []string{"public IP address", "how much it relayed", "request log", "email address"} {
		if !strings.Contains(strings.Join(p.AccountData, "; "), must) {
			t.Errorf("account data does not say %q", must)
		}
	}
	security := slices.IndexFunc(p.AccountData, func(s string) bool { return strings.HasPrefix(s, "Security audit records") })
	if security < 0 {
		t.Fatal("account data is missing security records")
	}
	for _, must := range []string{
		fmt.Sprintf("expire after %d days", AuditRetentionDays),
		fmt.Sprintf("expire after %d days", AbuseRetentionDays),
		"Deleting an account removes records linked directly to it or its homes",
		"Records linked only to an IP address expire on schedule",
	} {
		if !strings.Contains(p.AccountData[security], must) {
			t.Errorf("security records policy does not say %q", must)
		}
	}
	for _, n := range p.NotCollected {
		if strings.HasPrefix(n, "IP addresses") && n != "IP addresses in analytics" {
			t.Errorf("not collected: %q (accounts' sessions hold one)", n)
		}
	}
}

func TestPolicyHandler(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/privacy", nil)
	PolicyHandler(w, r)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "data_categories") {
		t.Errorf("policy body missing fields: %s", w.Body.String())
	}
}
