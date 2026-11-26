package mail

import (
	"context"
	"fmt"
	netmail "net/mail"
	"strings"
)

// Guard keeps a deployment's mail to the recipients it may write to, and marks its subjects:
// QA mails nobody but the project's own addresses. An empty list is everyone (production).
type Guard struct {
	Next Mailer
	// Allow holds whole addresses ("a@example.test") and whole domains ("@example.test").
	Allow []string
	// NoReserved refuses the names nobody has (RFC 2606, RFC 6761): the deploy's tests register
	// ci-e2e-<n>@example.com on production too, and a mail to it would only bounce.
	NoReserved bool
	// Prefix goes before each subject ("[QA]").
	Prefix string
}

// Send passes m on, or answers ErrSuppressed.
func (g Guard) Send(ctx context.Context, m Message) error {
	_, err := g.SendID(ctx, m)
	return err
}

// SendID passes m on and returns the provider's message id when Next implements IDMailer.
func (g Guard) SendID(ctx context.Context, m Message) (string, error) {
	if !g.Allows(m.To) {
		return "", ErrSuppressed
	}
	if g.Prefix != "" {
		m.Subject = g.Prefix + " " + m.Subject
	}
	if idm, ok := g.Next.(IDMailer); ok {
		return idm.SendID(ctx, m)
	}
	return "", g.Next.Send(ctx, m)
}

// Allows reports whether addr may be mailed.
func (g Guard) Allows(addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndexByte(addr, '@')
	if at < 0 || (g.NoReserved && reserved(addr[at+1:])) {
		return false
	}
	if len(g.Allow) == 0 {
		return true
	}
	for _, e := range g.Allow {
		e = strings.ToLower(e)
		if e == addr || (strings.HasPrefix(e, "@") && e == addr[at:]) {
			return true
		}
	}
	return false
}

// reserved reports whether domain can belong to nobody.
func reserved(domain string) bool {
	domain = strings.TrimSuffix(domain, ".")
	for _, d := range []string{"example.com", "example.net", "example.org", "example", "test", "invalid", "localhost", "local"} {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

// ParseAllow checks an allow-list's entry: an address, or "@" and a domain.
func ParseAllow(e string) error {
	if d, ok := strings.CutPrefix(e, "@"); ok {
		e = "x@" + d
	}
	a, err := netmail.ParseAddress(e)
	if err != nil || a.Address != e || a.Name != "" {
		return fmt.Errorf("%q is not an address or @domain", e)
	}
	return nil
}
