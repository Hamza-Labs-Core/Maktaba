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
// Update the policy here; the formatter regenerates the served text.

// DisclosurePolicy captures the fields RFC 9116 cares about.
type DisclosurePolicy struct {
	Contact         []string  // mailto:/tel:/https:
	Expires         time.Time // when this file should be replaced
	Encryption      []string  // GPG fingerprints or HTTPS keys.openpgp URLs
	Acknowledgments []string  // URLs to hall-of-fame pages
	PreferredLang   []string  // BCP-47 list
	Policy          []string  // URLs to the long-form policy
	Hiring          []string  // optional
}

// DefaultPolicy returns the shipped policy. Tests assert that this
// validates and renders cleanly; tools/release-checklist verifies the
// Expires date is far enough in the future.
func DefaultPolicy() DisclosurePolicy {
	return DisclosurePolicy{
		Contact:         []string{"mailto:security@maktaba.dev"},
		Expires:         time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		PreferredLang:   []string{"en", "ar"},
		Policy:          []string{"https://maktaba.dev/security/policy"},
		Acknowledgments: []string{"https://maktaba.dev/security/hall-of-fame"},
	}
}

// Validate enforces RFC 9116 minimums.
func (p DisclosurePolicy) Validate() error {
	if len(p.Contact) == 0 {
		return fmt.Errorf("disclosure: at least one Contact required")
	}
	if p.Expires.IsZero() {
		return fmt.Errorf("disclosure: Expires required")
	}
	return nil
}

// SecurityTxt renders the policy as an RFC 9116 document.
func (p DisclosurePolicy) SecurityTxt() string {
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
	if !p.Expires.IsZero() {
		fmt.Fprintf(&b, "Expires: %s\n", p.Expires.UTC().Format(time.RFC3339))
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
