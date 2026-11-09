//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth/webauthntest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// passkeyAccount is a staff account enrolled with a passkey and no TOTP app.
type passkeyAccount struct {
	id, email string
	key       *webauthntest.Authenticator
}

// enrolPasskey makes a staff account holding role with `staff add`, adds a passkey through
// the enrolment link, and finishes the enrolment with the password and no TOTP code.
func (e *env) enrolPasskey(email, role string) passkeyAccount {
	e.t.Helper()
	var out bytes.Buffer
	if err := e.staffCLI(&out).run(context.Background(), "add", email, role); err != nil {
		e.t.Fatal(err)
	}
	_, link, _ := strings.Cut(out.String(), "/enroll#")
	link = strings.TrimSpace(link)
	acct := passkeyAccount{email: email, key: webauthntest.New("https://" + testAdminHost)}
	c := e.client()
	// with no passkey yet, no code: the enrolment can't finish
	if r := c.do("POST", "/v1/staff/enroll/finish", map[string]string{"token": link, "password": goodStaffPassword}); r.code != http.StatusBadRequest || r.errCode() != "mfa_required" {
		e.t.Fatalf("an enrolment with no factor: %d %s", r.code, r.body)
	}
	var opts protocol.CredentialCreation
	begin := c.mustStatus(c.do("POST", "/v1/staff/enroll/passkey/begin", map[string]string{"token": link}), http.StatusOK)
	if err := json.Unmarshal(begin.body, &opts); err != nil {
		e.t.Fatal(err)
	}
	if opts.Response.RelyingParty.ID != testAdminHost ||
		opts.Response.AuthenticatorSelection.UserVerification != protocol.VerificationRequired ||
		(opts.Response.Attestation != "" && opts.Response.Attestation != protocol.PreferNoAttestation) {
		e.t.Fatalf("registration options: %s", begin.body)
	}
	cred, err := acct.key.Register(&opts)
	if err != nil {
		e.t.Fatal(err)
	}
	c.mustStatus(c.do("POST", "/v1/staff/enroll/passkey/finish", map[string]any{
		"token": link, "name": "Laptop", "credential": json.RawMessage(cred)}), http.StatusOK)
	codes := c.mustStatus(c.do("POST", "/v1/staff/enroll/finish", map[string]string{
		"token": link, "password": goodStaffPassword}), http.StatusOK).json(e.t)
	if rc, _ := codes["recovery_codes"].([]any); len(rc) != 10 {
		e.t.Fatalf("recovery codes: %v", codes)
	}
	a, err := (&stores.Staff{DB: e.pool.DB}).ByEmail(context.Background(), email)
	if err != nil {
		e.t.Fatal(err)
	}
	acct.id = a.ID
	return acct
}

// passkeyStep is a passkey ceremony on c: begin at path+"/begin" with body, the
// authenticator's answer posted to path+"/finish" with body and the credential.
func passkeyStep(t *testing.T, c *client, key *webauthntest.Authenticator, path string, body map[string]any) (resp, []byte) {
	t.Helper()
	begin := c.mustStatus(c.do("POST", path+"/begin", body), http.StatusOK)
	var opts protocol.CredentialAssertion
	if err := json.Unmarshal(begin.body, &opts); err != nil {
		t.Fatal(err)
	}
	if opts.Response.UserVerification != protocol.VerificationRequired {
		t.Fatalf("assertion options: %s", begin.body)
	}
	answer, err := key.Assert(&opts)
	if err != nil {
		t.Fatal(err)
	}
	fin := map[string]any{"credential": json.RawMessage(answer)}
	for k, v := range body {
		fin[k] = v
	}
	return c.do("POST", path+"/finish", fin), answer
}

// passkeySignIn is §2 step 1 then 2a on a new client.
func (e *env) passkeySignIn(acct passkeyAccount) (*client, resp, string) {
	e.t.Helper()
	c, r := e.staffLogin(acct.email, goodStaffPassword)
	c.mustStatus(r, http.StatusOK)
	mfa, _ := r.json(e.t)["mfa_token"].(string)
	if m, _ := r.json(e.t)["methods"].([]any); len(m) == 0 || m[0] != "passkey" || slices.Contains(m, any("totp")) {
		e.t.Fatalf("methods for a passkey-only account: %v", r.json(e.t)["methods"])
	}
	fin, _ := passkeyStep(e.t, c, acct.key, "/v1/staff/mfa/passkey", map[string]any{"mfa_token": mfa})
	return c, fin, mfa
}

// T05 end to end (story 50.33 part 2): an account enrols with a passkey alone, signs in with
// it (amr pwd+hwk) and reaches the admin routes; the challenge is good once; a passkey re-check
// opens a sensitive route; a counter that goes backwards disables the passkey, fails the step,
// is audited and alerts. Each passkey step is audited as staff.
func TestStaffPasskeys(t *testing.T) {
	e := newEnv(t)
	acct := e.enrolPasskey("keys@example.test", stores.StaffRoleServiceAdmin)
	if n := e.count(`SELECT count(*) FROM staff_passkeys WHERE staff_id = $1 AND name = 'Laptop' AND disabled_at IS NULL`, acct.id); n != 1 {
		t.Fatalf("passkeys stored: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM staff_totp WHERE staff_id = $1`, acct.id); n != 0 {
		t.Fatalf("a passkey-only enrolment left a TOTP factor: %d", n)
	}

	// sign in with the passkey
	c, fin, mfa := e.passkeySignIn(acct)
	s := c.mustStatus(fin, http.StatusOK).json(t)
	c.token, _ = s["access_token"].(string)
	claims, err := staffauth.NewSigner([]byte(testStaffTokenSecret)).Verify(c.token, time.Now())
	if err != nil || !slices.Equal(claims.AMR, []string{staffauth.AMRPassword, staffauth.AMRPasskey}) {
		t.Fatalf("token amr: %v %v", claims.AMR, err)
	}
	c.mustStatus(c.do("GET", "/v1/admin/fleet", nil), http.StatusOK)
	// the mfa_token is spent with the session (checked in the database: the client's per-IP
	// allowance, burst 5, is kept for the re-check below)
	if n := e.count(`SELECT count(*) FROM staff_mfa_tokens WHERE token_hash = $1`, staffauth.TokenHash(mfa)); n != 0 {
		t.Errorf("the mfa_token outlived the sign-in: %d", n)
	}

	// an answer is checked against its challenge once
	c2, r := e.staffLogin(acct.email, goodStaffPassword)
	mfa2, _ := r.json(t)["mfa_token"].(string)
	first, answer := passkeyStep(t, c2, acct.key, "/v1/staff/mfa/passkey", map[string]any{"mfa_token": mfa2})
	c2.mustStatus(first, http.StatusOK)
	c3, r := e.staffLogin(acct.email, goodStaffPassword)
	mfa3, _ := r.json(t)["mfa_token"].(string)
	if r := c3.do("POST", "/v1/staff/mfa/passkey/finish", map[string]any{"mfa_token": mfa3, "credential": json.RawMessage(answer)}); r.code != http.StatusBadRequest || r.errCode() != "passkey_expired" {
		t.Errorf("a replayed answer: %d %s", r.code, r.body)
	}

	// §5: a 6-minute-old check closes a sensitive route; a passkey re-check opens it
	e.registerVerified("victim@example.test")
	victimID := e.userID("victim@example.test")
	suspend := "/v1/admin/accounts/" + victimID + "/suspend"
	e.exec(`UPDATE staff_sessions SET auth_time = now() - interval '6 minutes' WHERE id = $1`, claims.SID)
	if r := c.do("POST", suspend, map[string]string{"reason": "test"}); r.code != http.StatusUnauthorized || r.errCode() != "reauth_required" {
		t.Fatalf("a stale check: %d %s", r.code, r.body)
	}
	re, _ := passkeyStep(t, c, acct.key, "/v1/staff/reauth/passkey", map[string]any{})
	c.token, _ = c.mustStatus(re, http.StatusOK).json(t)["access_token"].(string)
	if r := c.do("POST", suspend, map[string]string{"reason": "test"}); r.code >= 400 {
		t.Fatalf("after the passkey re-check: %d %s", r.code, r.body)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.reauth' AND actor_id = $1 AND detail->>'method' = 'passkey'`, acct.id); n != 1 {
		t.Errorf("passkey re-checks audited: %d", n)
	}

	// the counter goes backwards: refused, the passkey disabled, audited, alerted
	acct.key.Counter = 0
	_, back, _ := e.passkeySignIn(acct)
	if back.code != http.StatusUnauthorized || back.errCode() != "mfa_failed" {
		t.Fatalf("a counter that went backwards: %d %s", back.code, back.body)
	}
	if n := e.count(`SELECT count(*) FROM staff_passkeys WHERE staff_id = $1 AND disabled_at IS NOT NULL`, acct.id); n != 1 {
		t.Errorf("the passkey is not disabled: %d", n)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.passkey.counter_disabled' AND actor_kind = 'staff' AND actor_id = $1`, acct.id); n != 1 {
		t.Errorf("the disabling is not audited: %d", n)
	}
	if !strings.Contains(e.logs.String(), "staff_passkey_counter") {
		t.Error("no staff_passkey_counter alert line")
	}
	// with its only factor gone, the account needs a new enrolment
	if _, r := e.staffLogin(acct.email, goodStaffPassword); r.code != http.StatusForbidden || r.errCode() != "mfa_required" {
		t.Errorf("sign-in with the passkey disabled: %d %s", r.code, r.body)
	}
	for _, action := range []string{"staff.passkey.added", "staff.enrolled", "staff.signin.mfa_ok", "staff.signin.mfa_failed"} {
		if e.count(`SELECT count(*) FROM audit_log WHERE action = $1 AND actor_kind = 'staff' AND actor_id = $2`, action, acct.id) == 0 {
			t.Errorf("no %s row", action)
		}
	}
}

// A passkey answer from another account's authenticator, or a garbled one, is a failed step.
func TestStaffPasskeyWrongKey(t *testing.T) {
	e := newEnv(t)
	acct := e.enrolPasskey("one@example.test", stores.StaffRoleServiceAdmin)
	acct.key = webauthntest.New("https://" + testAdminHost) // a key the account never registered
	_, fin, _ := e.passkeySignIn(acct)
	if fin.code != http.StatusUnauthorized || fin.errCode() != "mfa_failed" {
		t.Fatalf("an unknown passkey: %d %s", fin.code, fin.body)
	}
	c, r := e.staffLogin(acct.email, goodStaffPassword)
	mfa, _ := r.json(t)["mfa_token"].(string)
	c.mustStatus(c.do("POST", "/v1/staff/mfa/passkey/begin", map[string]any{"mfa_token": mfa}), http.StatusOK)
	if r := c.do("POST", "/v1/staff/mfa/passkey/finish", map[string]any{"mfa_token": mfa, "credential": json.RawMessage(`{"id":"x"}`)}); r.code != http.StatusUnauthorized || r.errCode() != "mfa_failed" {
		t.Errorf("a garbled answer: %d %s", r.code, r.body)
	}
	if n := e.count(`SELECT count(*) FROM audit_log WHERE action = 'staff.signin.mfa_failed' AND actor_id = $1`, acct.id); n != 2 {
		t.Errorf("failed steps audited: %d", n)
	}
}

// The passkey re-check is enforced server-side: with no token, a customer's token, or a staff
// token minted without a second factor, both of its routes are refused before any challenge
// is drawn, and an mfa_token is no access token there.
func TestStaffPasskeyReauthNeedsASession(t *testing.T) {
	e := newEnv(t)
	acct := e.enrolPasskey("guard@example.test", stores.StaffRoleServiceAdmin)
	c, fin, _ := e.passkeySignIn(acct)
	c.mustStatus(fin, http.StatusOK)
	var sid string
	if err := e.pool.DB.QueryRow(`SELECT id FROM staff_sessions WHERE staff_id = $1 AND revoked_at IS NULL`, acct.id).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	pwdOnly := e.client()
	tok, err := staffauth.NewSigner([]byte(testStaffTokenSecret)).Issue(acct.id, sid, []string{staffauth.AMRPassword}, time.Now(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pwdOnly.token = tok
	customer := e.registerVerified("customer@example.test")
	withMFAToken := e.client()
	_, r := e.staffLogin(acct.email, goodStaffPassword)
	withMFAToken.token, _ = r.json(t)["mfa_token"].(string)

	for name, cl := range map[string]*client{"anonymous": e.client(), "customer": customer, "mfa_token": withMFAToken, "no second factor": pwdOnly} {
		for _, p := range []string{"/v1/staff/reauth/passkey/begin", "/v1/staff/reauth/passkey/finish"} {
			r := cl.do("POST", p, map[string]any{})
			want := http.StatusUnauthorized
			if name == "no second factor" {
				want = http.StatusForbidden
			}
			if r.code != want {
				t.Errorf("%s %s: %d %s, want %d", name, p, r.code, r.body, want)
			}
		}
	}
	if n := e.count(`SELECT count(*) FROM staff_webauthn_challenges WHERE staff_id = $1 AND purpose = 'reauth'`, acct.id); n != 0 {
		t.Errorf("a refused caller drew a re-check challenge: %d", n)
	}
}
