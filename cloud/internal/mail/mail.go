// Package mail sends the account mails: email verification, password
// reset, and notices. The only driver is LogMailer, which writes the
// message to the log; a real provider is a separate decision (it needs
// an account with one) and slots in behind Mailer.
package mail

import (
	"context"
	"log/slog"
	"net/url"
	"sync"
)

// Message is one outbound mail.
type Message struct {
	To      string
	Subject string
	Body    string
	// Kind names the template ("verify_email", "password_reset", …) so
	// tests and logs can tell mails apart without parsing the body.
	Kind string
}

// Mailer delivers messages.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// LogMailer writes each message to the logger. Development and test
// deployments use it; the link in the body is what a user would click.
type LogMailer struct {
	Logger *slog.Logger
}

func (l LogMailer) Send(_ context.Context, m Message) error {
	l.Logger.Info("mail (log driver; not delivered)", "to", m.To, "kind", m.Kind, "subject", m.Subject, "body", m.Body)
	return nil
}

// Recorder keeps messages in memory, for tests.
type Recorder struct {
	mu   sync.Mutex
	Sent []Message
}

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

// Templates builds the account mails. WebURL is the cloud web app's
// origin; the links land on its pages, which post the token to the API.
type Templates struct {
	WebURL string
}

func (t Templates) link(path, token string) string {
	return t.WebURL + path + "?token=" + url.QueryEscape(token)
}

// VerifyEmail is sent at registration and on resend.
func (t Templates) VerifyEmail(to, token string) Message {
	return Message{To: to, Kind: "verify_email", Subject: "Confirm your Maktaba Cloud email",
		Body: "Confirm your email to finish setting up your Maktaba Cloud account:\n\n" +
			t.link("/verify-email", token) + "\n\nThe link expires in 24 hours. If you did not sign up, ignore this mail."}
}

// PasswordReset is sent by "forgot password".
func (t Templates) PasswordReset(to, token string) Message {
	return Message{To: to, Kind: "password_reset", Subject: "Reset your Maktaba Cloud password",
		Body: "Someone asked to reset the password of your Maktaba Cloud account. To choose a new one:\n\n" +
			t.link("/reset-password", token) + "\n\nThe link expires in 1 hour and works once. If it was not you, ignore this mail."}
}

// AlreadyRegistered goes to an existing account when someone tries to
// register its address, so registration does not reveal which
// addresses have accounts.
func (t Templates) AlreadyRegistered(to string) Message {
	return Message{To: to, Kind: "already_registered", Subject: "Someone tried to sign up with your email",
		Body: "Someone tried to create a Maktaba Cloud account with this address, which already has one. " +
			"To sign in: " + t.WebURL + "/sign-in\nForgot your password? " + t.WebURL + "/forgot-password"}
}

// Notice is a plain account notice (suspension, deletion, payment).
func (t Templates) Notice(to, kind, subject, body string) Message {
	return Message{To: to, Kind: kind, Subject: subject, Body: body}
}
