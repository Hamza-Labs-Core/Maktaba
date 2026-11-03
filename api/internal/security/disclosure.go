package security

import (
	"fmt"
	"strings"
	"time"
)

// Story 23.8 — coordinated disclosure. The repo ships a single
// DisclosurePolicy struct as the source of truth; the API serves it
// formatted as RFC 9116 at `/.well-known/security.txt` and the docs site
// renders it.
//
// Update the policy here; the formatter regenerates the served text. The
// expiry rolls forward by itself (ExpiryMonths), so it is never edited by hand.

// DisclosurePolicy captures the fields RFC 9116 cares about.
type DisclosurePolicy struct {
	Contact []string // mailto:/tel:/https:
	// Expires is a fixed expiry. Zero (the shipped policy) means a rolling one:
	// ExpiryMonths after the day the file is rendered.
	Expires         time.Time
	ExpiryMonths    int      // the rolling expiry, used when Expires is zero (1..MaxExpiryMonths)
	Encryption      []string // GPG fingerprints or HTTPS keys.openpgp URLs
	Acknowledgments []string // URLs to hall-of-fame pages
	PreferredLang   []string // BCP-47 list
	Policy          []string // URLs to the long-form policy
	Hiring          []string // optional
}

// MaxExpiryMonths keeps a rolling Expires under a year ahead, as RFC 9116 asks.
const MaxExpiryMonths = 11

// DefaultPolicy returns the shipped policy. Its Expires rolls: the API renders it
// per request, and maktaba.dev's build (web/packages/devportal/scripts/build.mjs,
// which reads ExpiryMonths from this function) renders it per deploy, with a
// monthly scheduled deploy (.github/workflows/devportal-deploy.yml).
func DefaultPolicy() DisclosurePolicy {
	return DisclosurePolicy{
		Contact:         []string{"mailto:security@maktaba.dev"},
		ExpiryMonths:    11,
		PreferredLang:   []string{"en", "ar"},
		Policy:          []string{"https://maktaba.dev/security/policy"},
		Acknowledgments: []string{"https://maktaba.dev/security/hall-of-fame"},
	}
}

// ExpiresAt is the Expires of a file rendered at now: a fixed Expires as it is,
// else the start of now's UTC day, ExpiryMonths later. A day past a short month's
// end rolls over (Mar 31 + 11 months is Mar 3), as JavaScript's Date.UTC does, so
// the API and the devportal build agree.
func (p DisclosurePolicy) ExpiresAt(now time.Time) time.Time {
	if !p.Expires.IsZero() || p.ExpiryMonths <= 0 {
		return p.Expires
	}
	d := now.UTC()
	return time.Date(d.Year(), d.Month()+time.Month(p.ExpiryMonths), d.Day(), 0, 0, 0, 0, time.UTC)
}

// Validate enforces RFC 9116 minimums.
func (p DisclosurePolicy) Validate() error {
	if len(p.Contact) == 0 {
		return fmt.Errorf("disclosure: at least one Contact required")
	}
	if p.Expires.IsZero() && p.ExpiryMonths == 0 {
		return fmt.Errorf("disclosure: Expires or ExpiryMonths required")
	}
	if p.ExpiryMonths < 0 || p.ExpiryMonths > MaxExpiryMonths {
		return fmt.Errorf("disclosure: ExpiryMonths %d outside 1..%d (RFC 9116: under a year)",
			p.ExpiryMonths, MaxExpiryMonths)
	}
	return nil
}

// SecurityTxt renders the policy as an RFC 9116 document, as it reads now.
func (p DisclosurePolicy) SecurityTxt() string { return p.SecurityTxtAt(time.Now()) }

// SecurityTxtAt renders the policy as it reads at now (the rolling Expires).
func (p DisclosurePolicy) SecurityTxtAt(now time.Time) string {
	var b strings.Builder
	writeList := func(field string, values []string) {
		for _, v := range values {
			b.WriteString(field)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteByte('\n')
		}
	}
	writeList("Contact", p.Contact)
	if exp := p.ExpiresAt(now); !exp.IsZero() {
		fmt.Fprintf(&b, "Expires: %s\n", exp.UTC().Format(time.RFC3339))
	}
	writeList("Encryption", p.Encryption)
	writeList("Acknowledgments", p.Acknowledgments)
	if len(p.PreferredLang) > 0 {
		fmt.Fprintf(&b, "Preferred-Languages: %s\n", strings.Join(p.PreferredLang, ", "))
	}
	writeList("Policy", p.Policy)
	writeList("Hiring", p.Hiring)
	return b.String()
}
