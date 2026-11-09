package staff

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Passkeys (story 50.33 §2 2a, §5, part 2). Each ceremony is two calls: begin draws a
// challenge, kept server-side for 5 minutes (one per account and purpose), and returns the
// options for navigator.credentials; finish takes the browser's answer, checks it against
// that challenge once, and acts. A passkey whose counter goes backwards is disabled, every
// session of the account ends, the step fails, and the owner is alerted (AlertPasskeyCounter).

// AlertPasskeyCounter is the owner alert for a passkey whose counter went backwards (50.10).
const AlertPasskeyCounter = "staff_passkey_counter"

// MaxPasskeyName is the longest name a passkey keeps.
const MaxPasskeyName = 64

type passkeyReq struct {
	Token      string          `json:"token"`
	MFAToken   string          `json:"mfa_token"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

// passkeysOn answers 404 when the relying party is not set up (no admin listener).
func (d *Deps) passkeysOn(w http.ResponseWriter) bool {
	if d.WebAuthn == nil {
		writeErr(w, http.StatusNotFound, "passkeys_off")
		return false
	}
	return true
}

// passkeyUser is a's enabled passkeys as WebAuthn sees them.
func (d *Deps) passkeyUser(ctx context.Context, a stores.StaffAccount) (staffauth.PasskeyUser, error) {
	u := staffauth.PasskeyUser{ID: a.ID, Email: a.Email}
	keys, err := d.Staff.Passkeys(ctx, a.ID, false)
	if err != nil {
		return u, err
	}
	for _, k := range keys {
		c, err := staffauth.DecodeCredential(k.Credential)
		if err != nil {
			d.Logger.Error("staff: stored passkey unreadable", "staff_id", a.ID, "passkey_id", k.ID, "err", err)
			continue
		}
		u.Credentials = append(u.Credentials, c)
	}
	return u, nil
}

// putChallenge keeps a ceremony's session for its finish.
func (d *Deps) putChallenge(ctx context.Context, staffID, purpose string, sess *webauthn.SessionData) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return d.Staff.PutChallenge(ctx, staffID, purpose, raw, d.now().Add(staffauth.ChallengeTTL))
}

// beginAssertion draws a sign-in or re-check challenge for a's passkeys and answers with its
// options; an account with no passkey is 400 no_passkey.
func (d *Deps) beginAssertion(w http.ResponseWriter, r *http.Request, a stores.StaffAccount, purpose string) {
	ctx := r.Context()
	u, err := d.passkeyUser(ctx, a)
	if err != nil {
		d.fail(w, "passkeys", err)
		return
	}
	if len(u.Credentials) == 0 {
		writeErr(w, http.StatusBadRequest, "no_passkey")
		return
	}
	opts, sess, err := d.WebAuthn.BeginLogin(u, staffauth.LoginOptions()...)
	if err != nil {
		d.fail(w, "passkey options", err)
		return
	}
	if err := d.putChallenge(ctx, a.ID, purpose, sess); err != nil {
		d.fail(w, "passkey challenge", err)
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

// The outcomes of checking an assertion.
type assertion int

const (
	assertionGood assertion = iota
	// assertionFailed is a wrong answer: it counts toward the lockout.
	assertionFailed
	// assertionExpired is an answer with no live challenge to check it against.
	assertionExpired
)

// checkAssertion checks the browser's answer against a's challenge for purpose, once: a good
// one moves the passkey's counter on; one whose counter went backwards disables the passkey.
func (d *Deps) checkAssertion(r *http.Request, a stores.StaffAccount, purpose string, body json.RawMessage) (assertion, error) {
	ctx := r.Context()
	raw, err := d.Staff.TakeChallenge(ctx, a.ID, purpose, d.now())
	if errors.Is(err, stores.ErrStaffTokenGone) {
		return assertionExpired, nil
	}
	if err != nil {
		return assertionFailed, err
	}
	var sess webauthn.SessionData
	if err := json.Unmarshal(raw, &sess); err != nil {
		return assertionFailed, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(body)
	if err != nil {
		return assertionFailed, nil
	}
	u, err := d.passkeyUser(ctx, a)
	if err != nil {
		return assertionFailed, err
	}
	cred, err := d.WebAuthn.ValidateLogin(u, sess, parsed)
	if err != nil {
		return assertionFailed, nil
	}
	if staffauth.CounterWentBack(cred) {
		if err := d.Staff.DisablePasskey(ctx, a.ID, cred.ID, d.now()); err != nil && !errors.Is(err, stores.ErrStaffNotFound) {
			return assertionFailed, err
		}
		// a copy of the key may exist, so a session it opened may be someone else's: every
		// session of the account ends with it (the refresh cookie then refreshes nothing)
		if err := d.Staff.RevokeAll(ctx, a.ID, "passkey_counter", d.now()); err != nil {
			return assertionFailed, err
		}
		d.audit(r, a.ID, a.Email, audit.ActionStaffPasskeyCounter, map[string]any{
			"sign_count": cred.Authenticator.SignCount, "sessions_ended": true})
		d.Logger.Error("staff passkey counter went backwards; passkey disabled, sessions ended", "code", AlertPasskeyCounter, "staff_id", a.ID)
		return assertionFailed, nil
	}
	rec, err := staffauth.EncodeCredential(cred)
	if err != nil {
		return assertionFailed, err
	}
	if err := d.Staff.UsePasskey(ctx, a.ID, cred.ID, cred.Authenticator.SignCount, rec, d.now()); err != nil {
		if errors.Is(err, stores.ErrStaffNotFound) {
			return assertionFailed, nil // disabled meanwhile
		}
		return assertionFailed, err
	}
	return assertionGood, nil
}

// --- §2 step 2a: sign-in ---

// MFAPasskeyBegin draws the sign-in challenge for the mfa_token's account. It spends no
// attempt: the answer does.
func (d *Deps) MFAPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !d.passkeysOn(w) {
		return
	}
	var req passkeyReq
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	now := d.now()
	id, err := d.Staff.MFATokenAccount(ctx, staffauth.TokenHash(req.MFAToken), cmw.GetClientIP(ctx), now)
	if errors.Is(err, stores.ErrStaffTokenGone) {
		writeErr(w, http.StatusUnauthorized, "mfa_token_invalid")
		return
	}
	if err != nil {
		d.fail(w, "mfa token", err)
		return
	}
	a, err := d.Staff.ByID(ctx, id)
	if err != nil {
		d.fail(w, "lookup", err)
		return
	}
	if !a.Active() {
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if isLocked, until := (staffauth.Lockout{LockedUntil: a.LockedUntil}).Locked(now); isLocked {
		locked(w, until, now)
		return
	}
	d.beginAssertion(w, r, a, stores.WebAuthnLogin)
}

// MFAPasskeyFinish checks the passkey's answer (one attempt of the mfa_token) and signs in
// with amr ["pwd","hwk"].
func (d *Deps) MFAPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	if !d.passkeysOn(w) {
		return
	}
	var req passkeyReq
	if !decode(w, r, &req) {
		return
	}
	a, ok := d.mfaAccount(w, r, mfaReq{MFAToken: req.MFAToken})
	if !ok {
		return
	}
	switch res, err := d.checkAssertion(r, a, stores.WebAuthnLogin, req.Credential); {
	case err != nil:
		d.fail(w, "passkey", err)
	case res == assertionExpired:
		writeErr(w, http.StatusBadRequest, "passkey_expired")
	case res == assertionFailed:
		d.failStep(r, a, audit.ActionStaffMFAFailed)
		writeErr(w, http.StatusUnauthorized, "mfa_failed")
	default:
		d.signIn(w, r, a, req.MFAToken, staffauth.AMRPasskey, map[string]any{"method": "passkey"})
	}
}

// --- §5: the re-check ---

// ReauthPasskeyBegin draws a re-check challenge for the signed-in account.
func (d *Deps) ReauthPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !d.passkeysOn(w) {
		return
	}
	a, ok := d.reauthAccount(w, r)
	if !ok {
		return
	}
	d.beginAssertion(w, r, a, stores.WebAuthnReauth)
}

// ReauthPasskeyFinish checks the passkey's answer and moves the session's auth_time to now;
// a wrong answer counts toward the lockout.
func (d *Deps) ReauthPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	if !d.passkeysOn(w) {
		return
	}
	var req passkeyReq
	if !decode(w, r, &req) {
		return
	}
	a, ok := d.reauthAccount(w, r)
	if !ok {
		return
	}
	switch res, err := d.checkAssertion(r, a, stores.WebAuthnReauth, req.Credential); {
	case err != nil:
		d.fail(w, "passkey", err)
	case res == assertionExpired:
		writeErr(w, http.StatusBadRequest, "passkey_expired")
	case res == assertionFailed:
		d.failStep(r, a, audit.ActionStaffMFAFailed)
		writeErr(w, http.StatusUnauthorized, "mfa_failed")
	default:
		d.reauthed(w, r, a, staffauth.AMRPasskey, "passkey")
	}
}

// reauthAccount is the signed-in account, unless it is locked.
func (d *Deps) reauthAccount(w http.ResponseWriter, r *http.Request) (stores.StaffAccount, bool) {
	ctx := r.Context()
	o, _ := middleware.ServiceAdmin(ctx)
	a, err := d.Staff.ByID(ctx, o.ID)
	if err != nil {
		d.fail(w, "lookup", err)
		return a, false
	}
	now := d.now()
	if isLocked, until := (staffauth.Lockout{LockedUntil: a.LockedUntil}).Locked(now); isLocked {
		locked(w, until, now)
		return a, false
	}
	return a, true
}

// --- §1: a passkey at enrolment ---

// EnrollPasskeyBegin draws a registration challenge for the enrolment link's account.
func (d *Deps) EnrollPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	if !d.passkeysOn(w) {
		return
	}
	var req passkeyReq
	if !decode(w, r, &req) {
		return
	}
	a, ok := d.enrolment(w, r, req.Token)
	if !ok {
		return
	}
	ctx := r.Context()
	u, err := d.passkeyUser(ctx, a)
	if err != nil {
		d.fail(w, "passkeys", err)
		return
	}
	opts, sess, err := d.WebAuthn.BeginRegistration(u, staffauth.RegistrationOptions(u)...)
	if err != nil {
		d.fail(w, "passkey options", err)
		return
	}
	if err := d.putChallenge(ctx, a.ID, stores.WebAuthnRegister, sess); err != nil {
		d.fail(w, "passkey challenge", err)
		return
	}
	writeJSON(w, http.StatusOK, opts)
}

// EnrollPasskeyFinish registers the passkey the browser made. The enrolment then finishes
// with the password (EnrollFinish), with or without a TOTP code.
func (d *Deps) EnrollPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	if !d.passkeysOn(w) {
		return
	}
	var req passkeyReq
	if !decode(w, r, &req) {
		return
	}
	a, ok := d.enrolment(w, r, req.Token)
	if !ok {
		return
	}
	ctx := r.Context()
	raw, err := d.Staff.TakeChallenge(ctx, a.ID, stores.WebAuthnRegister, d.now())
	if errors.Is(err, stores.ErrStaffTokenGone) {
		writeErr(w, http.StatusBadRequest, "passkey_expired")
		return
	}
	if err != nil {
		d.fail(w, "passkey challenge", err)
		return
	}
	var sess webauthn.SessionData
	if err := json.Unmarshal(raw, &sess); err != nil {
		d.fail(w, "passkey challenge", err)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(req.Credential)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "passkey_failed")
		return
	}
	u, err := d.passkeyUser(ctx, a)
	if err != nil {
		d.fail(w, "passkeys", err)
		return
	}
	cred, err := d.WebAuthn.CreateCredential(u, sess, parsed)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "passkey_failed")
		return
	}
	rec, err := staffauth.EncodeCredential(cred)
	if err != nil {
		d.fail(w, "passkey record", err)
		return
	}
	name := passkeyName(req.Name)
	if _, err := d.Staff.AddPasskey(ctx, a.ID, cred.ID, cred.PublicKey, cred.Authenticator.SignCount, name, rec); err != nil {
		if errors.Is(err, stores.ErrStaffPasskeyExists) {
			writeErr(w, http.StatusConflict, "passkey_exists")
			return
		}
		d.fail(w, "add passkey", err)
		return
	}
	d.audit(r, a.ID, a.Email, audit.ActionStaffPasskeyAdded, map[string]any{"name": name})
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "passkeys": len(u.Credentials) + 1})
}

// passkeyName is the name a passkey keeps: trimmed, at most MaxPasskeyName characters,
// "Passkey" when none is given.
func passkeyName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Passkey"
	}
	for utf8.RuneCountInString(s) > MaxPasskeyName {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}
