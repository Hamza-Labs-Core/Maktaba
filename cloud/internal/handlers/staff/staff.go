// Package staff is the operator console's sign-in (Epic 50 story 50.33 §2, §4, §5): the
// /v1/staff/* routes on the admin listener. A staff account is not a cloud account: it signs
// in with a password and then a second factor, its session lasts at most 8 hours (15 minutes
// idle), and its access token is signed with a key no customer token uses.
//
// Passkeys (§2 2a) and the passkey re-check come in the story's second part; until then a
// staff account's factor is a TOTP app, with recovery codes behind it.
package staff

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/argon2id"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/password"
	cmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/staffauth"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/turnstile"
)

// Cookie is the staff refresh cookie (50.32 §4): host-only on admin.maktaba.tv, never sent to
// a user host, and Strict, since nothing ever links into the console's refresh.
const Cookie = "__Host-maktaba_staff"

// RotateGrace is how long a just-rotated cookie is a lost race between two tabs rather than a
// replay.
const RotateGrace = 10 * time.Second

// Password lengths for a staff account (§1): longer than a customer's.
const (
	PasswordMin = 14
	PasswordMax = 256
)

// The owner alerts (50.10): each is an error line with this code, which
// maktaba_errors_total counts and deploy/cloud-ha/monitoring/alerts.yml pages on.
const (
	AlertLockout      = "staff_lockout"
	AlertRecoveryUsed = "staff_recovery_code_used"
)

// Deps is what the staff routes need.
type Deps struct {
	Staff  *stores.Staff
	Signer *staffauth.Signer
	// Box seals each TOTP secret (MAKTABA_CLOUD_STAFF_SECRET_KEY).
	Box *staffauth.Box
	// Gate admits the routes a signed-in staff account calls about itself.
	Gate *middleware.StaffGate
	// Turnstile is the bot check on sign-in (50.30); its WebOrigins name the console.
	Turnstile *turnstile.Guard
	// TurnstileSiteKey is the widget's public key, served to the console with the guard on.
	TurnstileSiteKey string
	Audit            *audit.Log
	Logger           *slog.Logger
	// Secure marks the cookie Secure (always, except a plain-HTTP test).
	Secure bool
	Now    func() time.Time
}

// Mount registers the /v1/staff/* routes on r, the admin listener's router.
func Mount(r interface {
	Post(pattern string, h http.HandlerFunc)
	Get(pattern string, h http.HandlerFunc)
	Method(method, pattern string, h http.Handler)
}, d *Deps) {
	r.Get("/v1/staff/config", d.Config)
	r.Post("/v1/staff/login", d.Login)
	r.Post("/v1/staff/mfa/totp", d.MFATOTP)
	r.Post("/v1/staff/mfa/recovery", d.MFARecovery)
	r.Post("/v1/staff/refresh", d.Refresh)
	r.Post("/v1/staff/logout", d.Logout)
	r.Post("/v1/staff/enroll/start", d.EnrollStart)
	r.Post("/v1/staff/enroll/finish", d.EnrollFinish)
	r.Method(http.MethodGet, "/v1/staff/me", d.Gate.Session(http.HandlerFunc(d.Me)))
	r.Method(http.MethodGet, "/v1/staff/sessions", d.Gate.Session(http.HandlerFunc(d.Sessions)))
	r.Method(http.MethodPost, "/v1/staff/sessions/revoke-all", d.Gate.Session(http.HandlerFunc(d.RevokeAll)))
	r.Method(http.MethodPost, "/v1/staff/reauth/totp", d.Gate.Session(http.HandlerFunc(d.ReauthTOTP)))
}

// RateLimitedPrefixes are the routes §6's per-IP limit covers: burst 5, then 1 per 12 s.
var RateLimitedPrefixes = []string{"/v1/staff/login", "/v1/staff/mfa/", "/v1/staff/reauth/", "/v1/staff/enroll/"}

const maxBody = 16 << 10

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// dummyHash is verified against for an unknown or unusable account, so a miss costs the same
// argon2id time as a wrong password (§6, timing).
var dummyHash = func() string {
	h, _ := argon2id.Hash("maktaba-staff-timing-equaliser", argon2id.DefaultParams())
	return h
}()

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}

func (d *Deps) fail(w http.ResponseWriter, what string, err error) {
	d.Logger.Error("staff: "+what, "err", err)
	writeErr(w, http.StatusServiceUnavailable, "unavailable")
}

func (d *Deps) audit(r *http.Request, staffID, email, action string, detail map[string]any) {
	if d.Audit == nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	if ua := r.UserAgent(); ua != "" {
		detail["user_agent"] = ua
	}
	if err := d.Audit.Write(r.Context(), audit.Entry{
		ActorKind: audit.ActorStaff, ActorID: staffID, ActorEmail: email, Action: action,
		Detail: detail, IP: cmw.GetClientIP(r.Context()),
	}); err != nil {
		d.Logger.Error("staff: audit", "action", action, "err", err)
	}
}

func locked(w http.ResponseWriter, until, now time.Time) {
	w.Header().Set("Retry-After", strconv.Itoa(int(until.Sub(now).Seconds())+1))
	writeErr(w, http.StatusLocked, "account_locked")
}

// failStep counts a failed password or MFA step against a; on the step that locks it the
// lockout is audited and the owner alerted.
func (d *Deps) failStep(r *http.Request, a stores.StaffAccount, action string) {
	now := d.now()
	d.audit(r, a.ID, a.Email, action, nil)
	lockedNow, err := d.Staff.RecordFailure(r.Context(), a.ID, now)
	if err != nil {
		d.Logger.Error("staff: record failure", "err", err)
		return
	}
	if lockedNow {
		d.audit(r, a.ID, a.Email, audit.ActionStaffLocked, map[string]any{"until": now.Add(staffauth.LockFor)})
		d.Logger.Error("staff account locked after failed sign-in steps", "code", AlertLockout, "staff_id", a.ID)
	}
}

// Config is what the sign-in page needs before anyone signs in: the bot check's site key when
// the check is on ("" when off).
func (d *Deps) Config(w http.ResponseWriter, _ *http.Request) {
	key := ""
	if d.Turnstile != nil && d.Turnstile.Enabled {
		key = d.TurnstileSiteKey
	}
	writeJSON(w, http.StatusOK, map[string]string{"turnstile_site_key": key})
}

// --- step 1: the password ---

type loginReq struct {
	Email          string `json:"email"`
	Password       string `json:"password"`
	TurnstileToken string `json:"turnstile_token"`
}

// Login is §2 step 1. Every way the email, the password or the account can be wrong answers
// the same 401 after the same argon2id work.
func (d *Deps) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	if err := d.Turnstile.Check(r, req.TurnstileToken); err != nil {
		if errors.Is(err, turnstile.ErrFailed) {
			d.audit(r, "", "", audit.ActionStaffTurnstile, nil)
		}
		d.Turnstile.Require(w, r, req.TurnstileToken)
		return
	}
	ctx := r.Context()
	now := d.now()
	a, err := d.Staff.ByEmail(ctx, req.Email)
	if err != nil && !errors.Is(err, stores.ErrStaffNotFound) {
		d.fail(w, "lookup", err)
		return
	}
	if err != nil || a.Status != "active" || a.PasswordHash == "" {
		_ = argon2id.Verify(req.Password, dummyHash)
		d.audit(r, a.ID, a.Email, audit.ActionStaffPasswordFailed, nil)
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if isLocked, until := (staffauth.Lockout{LockedUntil: a.LockedUntil}).Locked(now); isLocked {
		_ = argon2id.Verify(req.Password, dummyHash)
		locked(w, until, now)
		return
	}
	if argon2id.Verify(req.Password, a.PasswordHash) != nil {
		d.failStep(r, a, audit.ActionStaffPasswordFailed)
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	methods, err := d.Staff.Methods(ctx, a.ID)
	if err != nil {
		d.fail(w, "methods", err)
		return
	}
	if !a.Active() || len(methods) == 0 {
		d.audit(r, a.ID, a.Email, audit.ActionStaffPasswordOK, map[string]any{"mfa": "none"})
		writeErr(w, http.StatusForbidden, "mfa_required")
		return
	}
	raw, err := staffauth.NewRawToken()
	if err != nil {
		d.fail(w, "mfa token", err)
		return
	}
	if err := d.Staff.NewMFAToken(ctx, staffauth.TokenHash(raw), a.ID, cmw.GetClientIP(ctx), now.Add(staffauth.MFATokenTTL)); err != nil {
		d.fail(w, "mfa token", err)
		return
	}
	d.audit(r, a.ID, a.Email, audit.ActionStaffPasswordOK, nil)
	writeJSON(w, http.StatusOK, map[string]any{"mfa_token": raw, "methods": methods})
}

// --- step 2: the second factor ---

type mfaReq struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
}

// mfaAccount spends one attempt of the mfa_token and loads its account, writing the refusal
// when there is none to go on with.
func (d *Deps) mfaAccount(w http.ResponseWriter, r *http.Request, req mfaReq) (stores.StaffAccount, bool) {
	ctx := r.Context()
	now := d.now()
	id, err := d.Staff.TakeMFAAttempt(ctx, staffauth.TokenHash(req.MFAToken), cmw.GetClientIP(ctx), now)
	if errors.Is(err, stores.ErrStaffTokenGone) {
		writeErr(w, http.StatusUnauthorized, "mfa_token_invalid")
		return stores.StaffAccount{}, false
	}
	if err != nil {
		d.fail(w, "mfa attempt", err)
		return stores.StaffAccount{}, false
	}
	a, err := d.Staff.ByID(ctx, id)
	if err != nil {
		d.fail(w, "lookup", err)
		return stores.StaffAccount{}, false
	}
	if !a.Active() {
		writeErr(w, http.StatusUnauthorized, "invalid_credentials")
		return stores.StaffAccount{}, false
	}
	if isLocked, until := (staffauth.Lockout{LockedUntil: a.LockedUntil}).Locked(now); isLocked {
		locked(w, until, now)
		return stores.StaffAccount{}, false
	}
	return a, true
}

// checkTOTP verifies code against a's confirmed TOTP factor and spends its step.
func (d *Deps) checkTOTP(ctx context.Context, a stores.StaffAccount, code string, confirmedOnly bool) (bool, error) {
	sealed, last, confirmed, err := d.Staff.TOTP(ctx, a.ID)
	if errors.Is(err, stores.ErrStaffNotFound) || (err == nil && confirmedOnly && !confirmed) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	secret, err := d.Box.Open(sealed, totpWhat(a.ID))
	if err != nil {
		return false, err
	}
	step, ok := staffauth.VerifyTOTP(secret, code, d.now(), last)
	if !ok {
		return false, nil
	}
	return d.Staff.UseTOTPStep(ctx, a.ID, step)
}

// totpWhat binds a sealed TOTP secret to its account: copied to another row, it won't open.
func totpWhat(staffID string) string { return "staff_totp:" + staffID }

// MFATOTP is §2 step 2b.
func (d *Deps) MFATOTP(w http.ResponseWriter, r *http.Request) {
	var req mfaReq
	if !decode(w, r, &req) {
		return
	}
	a, ok := d.mfaAccount(w, r, req)
	if !ok {
		return
	}
	good, err := d.checkTOTP(r.Context(), a, req.Code, true)
	if err != nil {
		d.fail(w, "totp", err)
		return
	}
	if !good {
		d.failStep(r, a, audit.ActionStaffMFAFailed)
		writeErr(w, http.StatusUnauthorized, "mfa_failed")
		return
	}
	d.signIn(w, r, a, req.MFAToken, map[string]any{"method": "totp"})
}

// MFARecovery is §2 step 2c: the code is spent and the owner alerted. A recovery code counts
// as a one-time password in the session's amr.
func (d *Deps) MFARecovery(w http.ResponseWriter, r *http.Request) {
	var req mfaReq
	if !decode(w, r, &req) {
		return
	}
	a, ok := d.mfaAccount(w, r, req)
	if !ok {
		return
	}
	ctx := r.Context()
	codes, err := d.Staff.UnusedRecoveryCodes(ctx, a.ID)
	if err != nil {
		d.fail(w, "recovery codes", err)
		return
	}
	norm := staffauth.NormalizeRecoveryCode(req.Code)
	spent := false
	for _, c := range codes {
		if argon2id.Verify(norm, c.Hash) == nil {
			if spent, err = d.Staff.SpendRecoveryCode(ctx, c.ID, d.now()); err != nil {
				d.fail(w, "spend recovery code", err)
				return
			}
			break
		}
	}
	if !spent {
		d.failStep(r, a, audit.ActionStaffMFAFailed)
		writeErr(w, http.StatusUnauthorized, "mfa_failed")
		return
	}
	d.audit(r, a.ID, a.Email, audit.ActionStaffRecoveryUsed, map[string]any{"left": len(codes) - 1})
	d.Logger.Error("staff recovery code used", "code", AlertRecoveryUsed, "staff_id", a.ID, "left", len(codes)-1)
	d.signIn(w, r, a, req.MFAToken, map[string]any{"method": "recovery"})
}

// signIn ends a successful MFA step: the mfa_token is spent, a session starts, its cookie is
// set and its first access token returned.
func (d *Deps) signIn(w http.ResponseWriter, r *http.Request, a stores.StaffAccount, mfaToken string, detail map[string]any) {
	ctx := r.Context()
	now := d.now()
	if err := d.Staff.SpendMFAToken(ctx, staffauth.TokenHash(mfaToken)); err != nil {
		d.fail(w, "spend mfa token", err)
		return
	}
	raw, err := staffauth.NewRawToken()
	if err != nil {
		d.fail(w, "cookie", err)
		return
	}
	amr := []string{staffauth.AMRPassword, staffauth.AMRTOTP}
	sess, err := d.Staff.NewSession(ctx, a.ID, staffauth.TokenHash(raw), amr, cmw.GetClientIP(ctx), truncate(r.UserAgent(), 256), now)
	if err != nil {
		d.fail(w, "session", err)
		return
	}
	if err := d.Staff.ClearFailures(ctx, a.ID, now); err != nil {
		d.Logger.Error("staff: clear failures", "err", err)
	}
	d.audit(r, a.ID, a.Email, audit.ActionStaffMFAOK, detail)
	d.setCookie(w, raw, sess.SignedInAt.Add(staffauth.SessionCap))
	d.issue(w, a, sess)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (d *Deps) setCookie(w http.ResponseWriter, raw string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: Cookie, Value: raw, Path: "/", HttpOnly: true, Secure: d.Secure,
		SameSite: http.SameSiteStrictMode, Expires: expires,
	})
}

func (d *Deps) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: Cookie, Value: "", Path: "/", HttpOnly: true, Secure: d.Secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

type staffView struct {
	ID    string   `json:"id"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

func viewOf(a stores.StaffAccount) staffView {
	roles := a.Roles
	if roles == nil {
		roles = []string{}
	}
	return staffView{ID: a.ID, Email: a.Email, Roles: roles}
}

// issue answers with a new access token for sess.
func (d *Deps) issue(w http.ResponseWriter, a stores.StaffAccount, sess stores.StaffSession) {
	now := d.now()
	tok, err := d.Signer.Issue(a.ID, sess.ID, sess.AMR, sess.AuthTime, now)
	if err != nil {
		d.fail(w, "issue token", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok,
		"expires_in":   int(staffauth.AccessTTL.Seconds()),
		"idle_ends_at": sess.LastActiveAt.Add(staffauth.IdleTimeout),
		"capped_at":    sess.SignedInAt.Add(staffauth.SessionCap),
		"staff":        viewOf(a),
	})
}

// --- the session ---

// Refresh is a new access token for the cookie's session, which rotates (§4). A session idle
// for 15 minutes, or 8 hours old, ends here; a rotated cookie presented again ends every
// session of its account.
func (d *Deps) Refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(Cookie)
	if err != nil || c.Value == "" {
		writeErr(w, http.StatusUnauthorized, "no_session")
		return
	}
	ctx := r.Context()
	now := d.now()
	oldHash := staffauth.TokenHash(c.Value)
	sess, err := d.Staff.SessionByCookie(ctx, oldHash, now, RotateGrace)
	switch {
	case errors.Is(err, stores.ErrStaffRotatedRecently):
		writeErr(w, http.StatusUnauthorized, "no_session")
		return
	case errors.Is(err, stores.ErrStaffReplay):
		d.audit(r, sess.StaffID, "", audit.ActionStaffReplay, nil)
		d.Logger.Warn("staff: rotated cookie replayed; every session revoked", "staff_id", sess.StaffID)
		d.clearCookie(w)
		writeErr(w, http.StatusUnauthorized, "no_session")
		return
	case errors.Is(err, stores.ErrStaffSessionEnd):
		d.clearCookie(w)
		writeErr(w, http.StatusUnauthorized, "no_session")
		return
	case err != nil:
		d.fail(w, "session", err)
		return
	}
	a, err := d.Staff.ByID(ctx, sess.StaffID)
	if err != nil && !errors.Is(err, stores.ErrStaffNotFound) {
		d.fail(w, "lookup", err)
		return
	}
	if err != nil || !a.Active() {
		_ = d.Staff.End(ctx, sess.ID, "account", now)
		d.clearCookie(w)
		writeErr(w, http.StatusUnauthorized, "no_session")
		return
	}
	if err := staffauth.CheckSession(sess.Times(), now); err != nil {
		reason, action := "idle", audit.ActionStaffIdleEnded
		if errors.Is(err, staffauth.ErrSessionCapped) {
			reason, action = "capped", audit.ActionStaffCapped
		}
		if err := d.Staff.End(ctx, sess.ID, reason, now); err != nil {
			d.Logger.Error("staff: end session", "err", err)
		}
		d.audit(r, a.ID, a.Email, action, map[string]any{"session": sess.ID})
		d.clearCookie(w)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "no_session", "reason": reason})
		return
	}
	raw, err := staffauth.NewRawToken()
	if err != nil {
		d.fail(w, "cookie", err)
		return
	}
	if err := d.Staff.Rotate(ctx, sess.ID, oldHash, staffauth.TokenHash(raw), now); err != nil {
		if errors.Is(err, stores.ErrStaffSessionEnd) {
			writeErr(w, http.StatusUnauthorized, "no_session")
			return
		}
		d.fail(w, "rotate", err)
		return
	}
	d.setCookie(w, raw, sess.SignedInAt.Add(staffauth.SessionCap))
	d.issue(w, a, sess)
}

// Logout ends the cookie's session.
func (d *Deps) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(Cookie); err == nil && c.Value != "" {
		ctx := r.Context()
		sess, err := d.Staff.SessionByCookie(ctx, staffauth.TokenHash(c.Value), d.now(), RotateGrace)
		if err == nil {
			if err := d.Staff.End(ctx, sess.ID, "signout", d.now()); err != nil {
				d.Logger.Error("staff: end session", "err", err)
			}
			d.audit(r, sess.StaffID, "", audit.ActionStaffSignout, map[string]any{"session": sess.ID})
		}
	}
	d.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// Me is the signed-in staff account, its roles and its factors: whatever its roles, it may
// see itself.
func (d *Deps) Me(w http.ResponseWriter, r *http.Request) {
	o, _ := middleware.ServiceAdmin(r.Context())
	a, err := d.Staff.ByID(r.Context(), o.ID)
	if err != nil {
		d.fail(w, "lookup", err)
		return
	}
	methods, err := d.Staff.Methods(r.Context(), o.ID)
	if err != nil {
		d.fail(w, "methods", err)
		return
	}
	if methods == nil {
		methods = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"staff": viewOf(a), "methods": methods, "session": o.SessionID})
}

type sessionView struct {
	ID           string    `json:"id"`
	SignedInAt   time.Time `json:"signed_in_at"`
	LastActiveAt time.Time `json:"last_active_at"`
	IP           string    `json:"ip"`
	UserAgent    string    `json:"user_agent"`
	Current      bool      `json:"current"`
}

// Sessions are the signed-in account's live sessions (the console's Signed-in devices).
func (d *Deps) Sessions(w http.ResponseWriter, r *http.Request) {
	o, _ := middleware.ServiceAdmin(r.Context())
	list, err := d.Staff.Sessions(r.Context(), o.ID)
	if err != nil {
		d.fail(w, "sessions", err)
		return
	}
	out := make([]sessionView, 0, len(list))
	for _, s := range list {
		out = append(out, sessionView{ID: s.ID, SignedInAt: s.SignedInAt, LastActiveAt: s.LastActiveAt,
			IP: s.IP, UserAgent: s.UserAgent, Current: s.ID == o.SessionID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// RevokeAll signs the account out everywhere, this session included.
func (d *Deps) RevokeAll(w http.ResponseWriter, r *http.Request) {
	o, _ := middleware.ServiceAdmin(r.Context())
	if err := d.Staff.RevokeAll(r.Context(), o.ID, "signout_all", d.now()); err != nil {
		d.fail(w, "revoke all", err)
		return
	}
	d.audit(r, o.ID, o.Email, audit.ActionStaffSignout, map[string]any{"all": true})
	d.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

type codeReq struct {
	Code string `json:"code"`
}

// ReauthTOTP is §5's fresh check: a TOTP code moves the session's auth_time to now, and the
// sensitive admin routes are open for 5 minutes. A failed code counts toward the lockout.
func (d *Deps) ReauthTOTP(w http.ResponseWriter, r *http.Request) {
	var req codeReq
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	o, _ := middleware.ServiceAdmin(ctx)
	a, err := d.Staff.ByID(ctx, o.ID)
	if err != nil {
		d.fail(w, "lookup", err)
		return
	}
	now := d.now()
	if isLocked, until := (staffauth.Lockout{LockedUntil: a.LockedUntil}).Locked(now); isLocked {
		locked(w, until, now)
		return
	}
	good, err := d.checkTOTP(ctx, a, req.Code, true)
	if err != nil {
		d.fail(w, "totp", err)
		return
	}
	if !good {
		d.failStep(r, a, audit.ActionStaffMFAFailed)
		writeErr(w, http.StatusUnauthorized, "mfa_failed")
		return
	}
	if err := d.Staff.Reauthed(ctx, o.SessionID, staffauth.AMRTOTP, now); err != nil {
		d.fail(w, "reauth", err)
		return
	}
	sess, err := d.Staff.Session(ctx, o.SessionID)
	if err != nil {
		d.fail(w, "session", err)
		return
	}
	d.audit(r, a.ID, a.Email, audit.ActionStaffReauth, map[string]any{"method": "totp"})
	d.issue(w, a, sess)
}

// --- enrolment (§1) ---

type enrollReq struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

func (d *Deps) enrolment(w http.ResponseWriter, r *http.Request, token string) (stores.StaffAccount, bool) {
	a, err := d.Staff.Enrolment(r.Context(), staffauth.TokenHash(token), d.now())
	if errors.Is(err, stores.ErrStaffTokenGone) || errors.Is(err, stores.ErrStaffNotFound) {
		writeErr(w, http.StatusGone, "enrolment_link_expired")
		return a, false
	}
	if err != nil {
		d.fail(w, "enrolment", err)
		return a, false
	}
	if a.Status != "active" {
		writeErr(w, http.StatusGone, "enrolment_link_expired")
		return a, false
	}
	return a, true
}

// EnrollStart opens an enrolment link: a new TOTP secret for the person's authenticator app
// (unconfirmed until EnrollFinish). The link stays live: opening it again draws a new secret.
func (d *Deps) EnrollStart(w http.ResponseWriter, r *http.Request) {
	var req enrollReq
	if !decode(w, r, &req) {
		return
	}
	a, ok := d.enrolment(w, r, req.Token)
	if !ok {
		return
	}
	secret, err := staffauth.NewTOTPSecret()
	if err != nil {
		d.fail(w, "totp secret", err)
		return
	}
	sealed, err := d.Box.Seal(secret, totpWhat(a.ID))
	if err != nil {
		d.fail(w, "seal", err)
		return
	}
	if err := d.Staff.PutTOTP(r.Context(), a.ID, sealed); err != nil {
		d.fail(w, "put totp", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email": a.Email, "totp_secret": secret, "totp_uri": staffauth.TOTPURI(secret, a.Email),
	})
}

// PasswordProblem is why a staff password is refused, or "" (§1: 14–256 characters and the
// cloud's breached-password check).
func PasswordProblem(p string) string {
	switch {
	case len(p) < PasswordMin:
		return "password_too_short"
	case len(p) > PasswordMax:
		return "password_too_long"
	}
	switch password.Validate(p) {
	case nil:
		return ""
	case password.ErrLeaked:
		return "password_breached"
	default:
		return "password_invalid"
	}
}

// EnrollFinish sets the password and confirms the TOTP factor with a code from the app; the
// answer carries the 10 recovery codes, shown once. The person then signs in.
func (d *Deps) EnrollFinish(w http.ResponseWriter, r *http.Request) {
	var req enrollReq
	if !decode(w, r, &req) {
		return
	}
	if p := PasswordProblem(req.Password); p != "" {
		writeErr(w, http.StatusBadRequest, p)
		return
	}
	a, ok := d.enrolment(w, r, req.Token)
	if !ok {
		return
	}
	ctx := r.Context()
	good, err := d.checkTOTP(ctx, a, req.Code, false)
	if err != nil {
		d.fail(w, "totp", err)
		return
	}
	if !good {
		writeErr(w, http.StatusBadRequest, "mfa_failed")
		return
	}
	hash, err := argon2id.Hash(req.Password, argon2id.DefaultParams())
	if err != nil {
		d.fail(w, "hash", err)
		return
	}
	codes, hashes, err := staffauth.NewRecoveryCodes()
	if err != nil {
		d.fail(w, "recovery codes", err)
		return
	}
	if err := d.Staff.FinishEnrolment(ctx, staffauth.TokenHash(req.Token), a.ID, hash, hashes, d.now()); err != nil {
		if errors.Is(err, stores.ErrStaffTokenGone) {
			writeErr(w, http.StatusGone, "enrolment_link_expired")
			return
		}
		d.fail(w, "finish enrolment", err)
		return
	}
	d.audit(r, a.ID, a.Email, audit.ActionStaffEnrolled, map[string]any{"methods": []string{"totp"}})
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}
