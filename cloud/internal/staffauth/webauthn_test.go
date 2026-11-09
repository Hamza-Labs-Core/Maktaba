package staffauth

import (
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth/webauthntest"
)

const testConsole = "https://admin.maktaba.tv"

// register enrols a's passkey for u and returns the stored record, as the handler keeps it.
func register(t *testing.T, w *webauthn.WebAuthn, u PasskeyUser, a *webauthntest.Authenticator) (*webauthn.Credential, error) {
	t.Helper()
	opts, sess, err := w.BeginRegistration(u, RegistrationOptions(u)...)
	if err != nil {
		t.Fatal(err)
	}
	body, err := a.Register(opts)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(body)
	if err != nil {
		return nil, err
	}
	return w.CreateCredential(u, *sess, parsed)
}

func assert(t *testing.T, w *webauthn.WebAuthn, u PasskeyUser, a *webauthntest.Authenticator) (*webauthn.Credential, error) {
	t.Helper()
	opts, sess, err := w.BeginLogin(u, LoginOptions()...)
	if err != nil {
		t.Fatal(err)
	}
	body, err := a.Assert(opts)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return nil, err
	}
	return w.ValidateLogin(u, *sess, parsed)
}

// T05 (story 50.33): WebAuthn with a virtual authenticator. Register and assert succeed; an
// authenticator signing for another rpId is refused at both steps; an assertion whose counter
// went backwards verifies but is flagged (the handler disables the passkey); one without user
// verification is refused.
func TestPasskeyCeremonies(t *testing.T) {
	w, err := NewWebAuthn(testConsole)
	if err != nil {
		t.Fatal(err)
	}
	if w.Config.RPID != "admin.maktaba.tv" || len(w.Config.RPOrigins) != 1 || w.Config.RPOrigins[0] != testConsole {
		t.Fatalf("relying party: %q %v", w.Config.RPID, w.Config.RPOrigins)
	}
	u := PasskeyUser{ID: "6f1c2a4e-3b5d-4c7e-9f80-1a2b3c4d5e6f", Email: "ops@example.test"}
	a := webauthntest.New(testConsole)

	// register, and the record survives the store's round trip
	cred, err := register(t, w, u, a)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	raw, err := EncodeCredential(cred)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := DecodeCredential(raw)
	if err != nil || string(stored.ID) != string(a.CredentialID()) {
		t.Fatalf("stored record: %v", err)
	}
	u.Credentials = []webauthn.Credential{stored}

	// assert: the counter moves on, no warning
	got, err := assert(t, w, u, a)
	if err != nil {
		t.Fatalf("assert: %v", err)
	}
	if CounterWentBack(got) || got.Authenticator.SignCount != 1 {
		t.Fatalf("after a good assertion: warning %v, count %d", CounterWentBack(got), got.Authenticator.SignCount)
	}
	u.Credentials = []webauthn.Credential{*got}

	// another rpId: refused
	a.RPID = "evil.example"
	if _, err := assert(t, w, u, a); err == nil {
		t.Error("an assertion for another rpId was accepted")
	}
	other := webauthntest.New(testConsole)
	other.RPID = "evil.example"
	if _, err := register(t, w, PasskeyUser{ID: "x", Email: "x@example.test"}, other); err == nil {
		t.Error("a registration for another rpId was accepted")
	}
	a.RPID = ""

	// the counter goes backwards: the signature is good, the record is flagged
	a.Counter = 0 // the next assertion reports 1, which the record already holds
	back, err := assert(t, w, u, a)
	if err != nil {
		t.Fatalf("assert with an old counter: %v", err)
	}
	if !CounterWentBack(back) {
		t.Error("a counter that went backwards was not flagged")
	}

	// no user verification: refused, at sign-in and at registration
	a.Counter = 10
	a.NoUV = true
	if _, err := assert(t, w, u, a); err == nil {
		t.Error("an assertion without user verification was accepted")
	}
	noUV := webauthntest.New(testConsole)
	noUV.NoUV = true
	if _, err := register(t, w, PasskeyUser{ID: "y", Email: "y@example.test"}, noUV); err == nil {
		t.Error("a registration without user verification was accepted")
	}
}

// The relying party needs a web origin.
func TestNewWebAuthnNeedsAnOrigin(t *testing.T) {
	for _, o := range []string{"", "admin.maktaba.tv", "ftp://admin.maktaba.tv"} {
		if _, err := NewWebAuthn(o); err == nil {
			t.Errorf("%q was accepted", o)
		}
	}
	if w, err := NewWebAuthn("http://admin.test:8091"); err != nil || w.Config.RPID != "admin.test" || w.Config.RPOrigins[0] != "http://admin.test:8091" {
		t.Errorf("a test origin with a port: %v", err)
	}
}
