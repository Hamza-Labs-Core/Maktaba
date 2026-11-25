// Package mail is the account mails: email verification, password reset, and notices.
// Templates writes them (plain text and HTML, English and Arabic); a Mailer delivers one:
// LogMailer writes it to the log (development, and a node without a provider's key), Resend
// sends it through resend.com (resend.go), and Guard keeps a deployment to its own recipients
// (policy.go). The cloud's roles never call these on a request: they put the mail in the
// outbox (package outbox), which delivers it.
package mail

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// The mails' kinds. A notice's kind is its own (NoticeKinds).
const (
	KindVerifyEmail       = "verify_email"
	KindPasswordReset     = "password_reset"
	KindAlreadyRegistered = "already_registered"
	KindTest              = "test"
)

// Message is one outbound mail.
type Message struct {
	// ID names the mail to the provider: sent again with the same ID, it is delivered once.
	ID      string
	To      string
	Subject string
	Body    string // plain text
	HTML    string // the same content; "" is a plain-text mail
	// Kind names the template ("verify_email", "password_reset", …) so
	// tests and logs can tell mails apart without parsing the body.
	Kind string
	Lang string // "en" or "ar"; "" is both (the moderation mails)
}

// Ask is a person's request that causes a mail: whom to, in which language ("" is the account's),
// from which network (a key of the requester's network; "" is not counted), and whether it passed
// the bot check.
type Ask struct {
	Kind, To, Lang, Net string
	Checked             bool
}

// Mailer delivers messages.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// ErrSuppressed: this deployment does not mail the recipient (Guard).
var ErrSuppressed = errors.New("mail: this deployment does not mail the recipient")

// LogMailer writes each message to the logger. Development and test
// deployments use it; the link in the body is what a user would click.
type LogMailer struct {
	Logger *slog.Logger
}

// Send logs m.
func (l LogMailer) Send(_ context.Context, m Message) error {
	l.Logger.Info("mail (log driver; not delivered)", "to", m.To, "kind", m.Kind, "subject", m.Subject, "body", m.Body)
	return nil
}

// Recorder keeps messages in memory, for tests.
type Recorder struct {
	mu   sync.Mutex
	Sent []Message
}

// Send records m.
func (r *Recorder) Send(_ context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Sent = append(r.Sent, m)
	return nil
}

// Last returns the most recent message of the given kind to addr.
func (r *Recorder) Last(kind, addr string) (Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.Sent) - 1; i >= 0; i-- {
		if r.Sent[i].Kind == kind && r.Sent[i].To == addr {
			return r.Sent[i], true
		}
	}
	return Message{}, false
}
