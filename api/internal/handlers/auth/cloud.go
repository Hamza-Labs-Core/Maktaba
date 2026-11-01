package auth

// Linked accounts (Epic 42 story 42.7): a home user links a Maktaba Cloud
// account, then signs in to this home through the cloud with it.
//
//	POST   /api/auth/cloud            — sign in with a cloud assertion (public)
//	GET    /api/me/cloud-link         — my link (or none)
//	POST   /api/me/cloud-link/start   — the link step's nonce
//	POST   /api/me/cloud-link         — finish linking with a cloud link assertion
//	PATCH  /api/me/cloud-link         — set my link's scope (an admin, signed in here)
//	DELETE /api/me/cloud-link         — unlink
//	GET    /api/users/{id}/cloud-link — a user's link (admin)
//	DELETE /api/users/{id}/cloud-link — unlink a user (admin)
//
// The rules, all enforced here and never by the app:
//   - the cloud's assertion names who signs in, for one use, a minute; the
//     rights are the linked home user's, capped by the link's scope;
//   - a link is play (watch only) unless its owner, an admin signed in on
//     the home (not through the cloud), set it to manage; a play token is
//     no admin on any route, even an admin's;
//   - a cloud sign-in mints no credential that outlives its link (it
//     cannot link, set a scope, change the password, make an API token or
//     pair a device), and unlinking ends its sessions at once.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/jwt"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/refresh"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/securityaudit"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// CloudLinking wires linked accounts into the auth handler. Nil on the
// handler ⇒ the routes answer 404 and no credential is a cloud one.
type CloudLinking struct {
	Store    *cloudlink.Store
	Identity *cloudlink.IdentitySource
	// Gate is the link gate (its cache is dropped on a change here).
	Gate *cloudlink.Gate
}

// TypeNotCloudLinked: this home is not claimed in Maktaba Cloud.
const TypeNotCloudLinked = "https://maktaba.tv/problems/not-cloud-linked"

// TypeCloudSession: the request came from a cloud sign-in and the route
// needs a sign-in made on the home.
const TypeCloudSession = "https://maktaba.tv/problems/cloud-session"

func notCloudLinked() *httperror.Error {
	return &httperror.Error{Type: TypeNotCloudLinked, Title: "not linked to Maktaba Cloud",
		Status: http.StatusNotFound, Detail: "this home is not claimed in Maktaba Cloud"}
}

// RefuseCloudSession answers 403 for a cloud sign-in on a route that
// mints a credential of its own (true when it wrote the answer). Other
// packages (API tokens, pairing) call it too.
func RefuseCloudSession(w http.ResponseWriter, r *http.Request, p *principal.Principal) bool {
	if !p.ViaCloud() {
		return false
	}
	httperror.Write(w, r, &httperror.Error{Type: TypeCloudSession, Title: "sign in on the home",
		Status: http.StatusForbidden, Detail: "a sign-in through Maktaba Cloud cannot do this; sign in on the home"})
	return true
}

func unauthorized() *httperror.Error {
	return &httperror.Error{Type: "https://maktaba.tv/problems/unauthorized", Title: "unauthorized",
		Status: http.StatusUnauthorized, Detail: "authentication required"}
}

// cloudSignInFailed is every refused cloud sign-in (no oracle about why).
func cloudSignInFailed() *httperror.Error {
	return &httperror.Error{Type: TypeInvalidCredentials, Title: "cloud sign-in refused",
		Status: http.StatusUnauthorized, Detail: "the cloud sign-in was refused; sign in to the cloud again"}
}

type assertionRequest struct {
	Assertion string `json:"assertion"`
}

// cloudLinkView is GET /api/me/cloud-link's answer.
type cloudLinkView struct {
	// Available: this home is claimed in the cloud (linking works).
	Available bool `json:"available"`
	// ServerID / CloudAPI: where the app asks the cloud for a link
	// assertion (POST {cloud_api}/v1/homes/{server_id}/assertion).
	ServerID string          `json:"server_id,omitempty"`
	CloudAPI string          `json:"cloud_api,omitempty"`
	Link     *cloudlink.Link `json:"link"`
}

func (h *Handler) cloudReady(w http.ResponseWriter, r *http.Request) bool {
	if h.Cloud == nil || h.Cloud.Store == nil {
		httperror.Write(w, r, notCloudLinked())
		return false
	}
	return true
}

// CloudSignIn signs in with a cloud sign-in assertion: the home user the
// assertion's cloud account is linked to, with the link's scope.
func (h *Handler) CloudSignIn(w http.ResponseWriter, r *http.Request) {
	started := h.now()
	if !h.cloudReady(w, r) {
		return
	}
	var req assertionRequest
	if err := common.ReadJSON(r, &req, 8*1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	id, pub, err := h.Cloud.Identity.Load()
	if err != nil {
		httperror.Write(w, r, notCloudLinked())
		return
	}
	fail := func(reason string, userID string) {
		h.audit(r.Context(), securityaudit.Entry{Event: securityaudit.EventLoginFailed, ActorUserID: userID,
			Payload: map[string]any{"via": "cloud", "reason": reason, "ip": remoteIP(r), "ua": r.UserAgent()}})
		h.padFailDelay(started)
		httperror.Write(w, r, cloudSignInFailed())
	}
	a, err := cloudlink.VerifyAssertion(req.Assertion, pub, cloudlink.PurposeSignIn, id.ServerID, h.now())
	if err != nil {
		fail(err.Error(), "")
		return
	}
	if err := h.Cloud.Store.UseAssertion(r.Context(), a); err != nil {
		if errors.Is(err, cloudlink.ErrReplayed) {
			fail("assertion replayed", "")
			return
		}
		httperror.Write(w, r, httperror.Internal("record assertion").Wrap(err))
		return
	}
	link, err := h.Cloud.Store.LinkByCloudUser(r.Context(), a.Sub)
	if errors.Is(err, cloudlink.ErrNotLinked) {
		fail("cloud account not linked", "")
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load link").Wrap(err))
		return
	}
	u, err := h.Users.GetByID(r.Context(), link.UserID)
	if errors.Is(err, users.ErrNotFound) || (err == nil && u.ID == users.SentinelAdminID) {
		fail("home user gone", link.UserID)
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load user").Wrap(err))
		return
	}
	if u.IsLocked(h.now()) {
		h.padFailDelay(started)
		h.writeLockedOut(w, r)
		return
	}
	if u.MustChangePassword {
		// a cloud sign-in cannot change the password: the web app on the
		// home does, signed in with the temporary one
		httperror.Write(w, r, passwordChangeRequired())
		return
	}
	h.audit(r.Context(), securityaudit.Entry{Event: securityaudit.EventLoginSuccess, ActorUserID: u.ID, TargetID: u.ID,
		Payload: map[string]any{"via": "cloud", "scope": link.Scope, "cloud_user_id": a.Sub, "ip": remoteIP(r), "ua": r.UserAgent()}})
	if isNativeClient(r) {
		h.respondCloudNative(w, r, u, link.Scope)
		return
	}
	h.respondCloudWeb(w, r, u, link.Scope)
}

// cloudUser is the user projection with is_admin capped by the scope.
func cloudUser(u *users.User, scope string) userResponse {
	out := toUserResponse(u)
	out.IsAdmin = u.IsAdmin && scope == principal.CloudScopeManage
	return out
}

// cloudNativeResponse is nativeLoginResponse plus the scope.
type cloudNativeResponse struct {
	nativeLoginResponse
	CloudScope string `json:"cloud_scope"`
}

// cloudAccessClaims is a cloud sign-in's access token: scp names the
// scope; a play token says no admin and lists the libraries the user
// reads (every library for an admin), so even a verifier that knows
// nothing of scp (the streaming service) sees a viewer.
func (h *Handler) cloudAccessClaims(ctx context.Context, u *users.User, scope string, now time.Time, ttl time.Duration) (jwt.Claims, error) {
	var libs []string
	var err error
	if scope == principal.CloudScopePlay && u.IsAdmin {
		libs, err = h.allLibraries(ctx)
	} else {
		libs, err = h.librariesFor(ctx, u)
	}
	if err != nil {
		return jwt.Claims{}, err
	}
	c := accessClaims(u, libs, now, ttl)
	c.IsAdmin = u.IsAdmin && scope == principal.CloudScopeManage
	c.Scp = scope
	return c, nil
}

func (h *Handler) allLibraries(ctx context.Context) ([]string, error) {
	return h.Cloud.Store.AllLibraries(ctx)
}

// CloudAccessTTL caps a cloud sign-in's access token: a machine (or the
// streaming service) verifies it offline, without the link gate, so an
// unlink reaches it when it expires.
const CloudAccessTTL = 5 * time.Minute

func (h *Handler) cloudAccessTTL() time.Duration {
	ttl := h.AccessTTL
	if ttl == 0 {
		ttl = DefaultAccessTTL
	}
	return min(ttl, CloudAccessTTL)
}

func (h *Handler) respondCloudNative(w http.ResponseWriter, r *http.Request, u *users.User, scope string) {
	accessTTL := h.cloudAccessTTL()
	now := h.now()
	claims, err := h.cloudAccessClaims(r.Context(), u, scope, now, accessTTL)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("resolve library acl").Wrap(err))
		return
	}
	access, err := jwt.Sign(h.Keys, claims)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("sign access token").Wrap(err))
		return
	}
	rt, err := h.RefreshTokens.Issue(r.Context(), refresh.IssueInput{
		UserID:     u.ID,
		ClientMeta: map[string]any{"ip": remoteIP(r), "ua": r.UserAgent(), "origin": "cloud", "cloud_scope": scope},
	})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("issue refresh token").Wrap(err))
		return
	}
	if err := h.Cloud.Store.RecordSignIn(r.Context(), cloudlink.KindRefresh, rt.FamilyID, u.ID, scope); err != nil {
		_ = h.RefreshTokens.RevokeFamily(r.Context(), rt.FamilyID)
		httperror.Write(w, r, httperror.Internal("record cloud sign-in").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, cloudNativeResponse{
		nativeLoginResponse: nativeLoginResponse{
			AccessToken:      access,
			AccessExpiresIn:  int(accessTTL.Seconds()),
			RefreshToken:     rt.Plaintext,
			RefreshExpiresIn: int(time.Until(rt.ExpiresAt).Seconds()),
			User:             cloudUser(u, scope),
		},
		CloudScope: scope,
	})
}

func (h *Handler) respondCloudWeb(w http.ResponseWriter, r *http.Request, u *users.User, scope string) {
	sess, err := h.Sessions.Create(r.Context(), sessions.CreateInput{UserID: u.ID, IP: remoteIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("create session").Wrap(err))
		return
	}
	if err := h.Cloud.Store.RecordSignIn(r.Context(), cloudlink.KindSession, sess.ID, u.ID, scope); err != nil {
		_ = h.Sessions.Revoke(r.Context(), sess.ID)
		httperror.Write(w, r, httperror.Internal("record cloud sign-in").Wrap(err))
		return
	}
	h.setSessionCookies(w, sess)
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"user": cloudUser(u, scope), "cloud_scope": scope})
}

// cloudScopeOfRefresh is the scope a refresh family was cloud-signed in
// with, checked against the link now: ("", nil) for a family a cloud
// sign-in did not make; errNoLink when its link is gone.
func (h *Handler) cloudScopeOfRefresh(ctx context.Context, familyID, userID string) (string, error) {
	if h.Cloud == nil || h.Cloud.Store == nil {
		return "", nil
	}
	scope, err := h.Cloud.Store.ScopeOf(ctx, cloudlink.KindRefresh, familyID)
	if err != nil || scope == "" {
		return "", err
	}
	link, err := h.Cloud.Store.LinkFor(ctx, userID)
	if errors.Is(err, cloudlink.ErrNotLinked) {
		return "", errNoLink
	}
	if err != nil {
		return "", err
	}
	if link.Scope == principal.CloudScopePlay {
		scope = principal.CloudScopePlay
	}
	return scope, nil
}

var errNoLink = errors.New("auth: the cloud link is gone")

// GetCloudLink answers the caller's link and whether linking works here.
func (h *Handler) GetCloudLink(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return
	}
	if !h.cloudReady(w, r) {
		return
	}
	out := cloudLinkView{}
	if id, _, err := h.Cloud.Identity.Load(); err == nil {
		out.Available, out.ServerID, out.CloudAPI = true, id.ServerID, id.CloudAPI
	}
	link, err := h.Cloud.Store.LinkFor(r.Context(), p.UserID)
	switch {
	case err == nil:
		out.Link = link
	case !errors.Is(err, cloudlink.ErrNotLinked):
		httperror.Write(w, r, httperror.Internal("load link").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// StartCloudLink makes the link step's nonce: the app passes it to the
// cloud, which signs a link assertion carrying it.
func (h *Handler) StartCloudLink(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return
	}
	if RefuseCloudSession(w, r, p) || !h.cloudReady(w, r) {
		return
	}
	if p.UserID == users.SentinelAdminID {
		httperror.Write(w, r, httperror.Forbidden("", "the admin token has no account to link"))
		return
	}
	id, _, err := h.Cloud.Identity.Load()
	if err != nil {
		httperror.Write(w, r, notCloudLinked())
		return
	}
	nonce, exp, err := h.Cloud.Store.StartLink(r.Context(), p.UserID)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("start link").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusCreated, map[string]any{
		"nonce": nonce, "expires_at": exp, "server_id": id.ServerID, "cloud_api": id.CloudAPI})
}

// CompleteCloudLink links the caller to the cloud account a link
// assertion names (scope play).
func (h *Handler) CompleteCloudLink(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return
	}
	if RefuseCloudSession(w, r, p) || !h.cloudReady(w, r) {
		return
	}
	if p.UserID == users.SentinelAdminID {
		httperror.Write(w, r, httperror.Forbidden("", "the admin token has no account to link"))
		return
	}
	var req assertionRequest
	if err := common.ReadJSON(r, &req, 8*1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	id, pub, err := h.Cloud.Identity.Load()
	if err != nil {
		httperror.Write(w, r, notCloudLinked())
		return
	}
	a, err := cloudlink.VerifyAssertion(req.Assertion, pub, cloudlink.PurposeLink, id.ServerID, h.now())
	if err != nil {
		httperror.Write(w, r, &httperror.Error{Type: TypeInvalidCredentials, Title: "link refused",
			Status: http.StatusUnprocessableEntity, Detail: "the cloud's link answer was refused; start again"})
		return
	}
	link, err := h.Cloud.Store.CompleteLink(r.Context(), p.UserID, a)
	switch {
	case errors.Is(err, cloudlink.ErrNonce), errors.Is(err, cloudlink.ErrReplayed):
		httperror.Write(w, r, &httperror.Error{Type: TypeInvalidCredentials, Title: "link refused",
			Status: http.StatusUnprocessableEntity, Detail: "this link step expired or was used; start again"})
		return
	case errors.Is(err, cloudlink.ErrCloudAccountTaken):
		httperror.Write(w, r, httperror.Conflict("", "that Maktaba Cloud account is linked to another user of this home"))
		return
	case err != nil:
		httperror.Write(w, r, httperror.Internal("link").Wrap(err))
		return
	}
	h.forgetCloud(p.UserID)
	h.audit(r.Context(), securityaudit.Entry{Event: securityaudit.EventCloudLinked, ActorUserID: p.UserID, TargetID: p.UserID,
		Payload: map[string]any{"cloud_user_id": a.Sub, "scope": link.Scope}})
	common.WriteJSON(w, r, http.StatusOK, link)
}

type scopeRequest struct {
	Scope string `json:"scope"`
}

// SetCloudLinkScope sets the caller's link scope. manage is an admin's,
// for their own link, signed in on the home; play is anyone's.
func (h *Handler) SetCloudLinkScope(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return
	}
	if RefuseCloudSession(w, r, p) || !h.cloudReady(w, r) {
		return
	}
	var req scopeRequest
	if err := common.ReadJSON(r, &req, 1024); err != nil {
		httperror.Write(w, r, err)
		return
	}
	switch req.Scope {
	case principal.CloudScopePlay:
	case principal.CloudScopeManage:
		if !p.IsAdmin {
			httperror.Write(w, r, httperror.Forbidden("", "manage is for an admin's own link"))
			return
		}
	default:
		httperror.Write(w, r, httperror.Unprocessable([]httperror.FieldError{{Field: "scope", Message: "play or manage"}}))
		return
	}
	link, err := h.Cloud.Store.SetScope(r.Context(), p.UserID, req.Scope)
	if errors.Is(err, cloudlink.ErrNotLinked) {
		httperror.Write(w, r, httperror.NotFound("cloud link"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("set scope").Wrap(err))
		return
	}
	h.forgetCloud(p.UserID)
	h.audit(r.Context(), securityaudit.Entry{Event: securityaudit.EventCloudLinkScope, ActorUserID: p.UserID, TargetID: p.UserID,
		Payload: map[string]any{"scope": link.Scope}})
	common.WriteJSON(w, r, http.StatusOK, link)
}

// UnlinkCloud removes the caller's link (from any sign-in: cutting it
// only takes access away).
func (h *Handler) UnlinkCloud(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return
	}
	h.unlink(w, r, p, p.UserID)
}

// AdminGetCloudLink answers a user's link (admin).
func (h *Handler) AdminGetCloudLink(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		writeAdminGate(w, r, p)
		return
	}
	if !h.cloudReady(w, r) {
		return
	}
	if _, err := uuid.Parse(chi.URLParam(r, "id")); err != nil {
		httperror.Write(w, r, httperror.NotFound("user"))
		return
	}
	link, err := h.Cloud.Store.LinkFor(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, cloudlink.ErrNotLinked) {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{"link": nil})
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("load link").Wrap(err))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, map[string]any{"link": link})
}

// AdminUnlinkCloud removes a user's link (admin).
func (h *Handler) AdminUnlinkCloud(w http.ResponseWriter, r *http.Request) {
	p := principal.FromContext(r.Context())
	if p == nil || !p.IsAdmin {
		writeAdminGate(w, r, p)
		return
	}
	h.unlink(w, r, p, chi.URLParam(r, "id"))
}

func (h *Handler) unlink(w http.ResponseWriter, r *http.Request, p *principal.Principal, userID string) {
	if !h.cloudReady(w, r) {
		return
	}
	if _, err := uuid.Parse(userID); err != nil {
		httperror.Write(w, r, httperror.NotFound("cloud link"))
		return
	}
	err := h.Cloud.Store.Unlink(r.Context(), userID)
	if errors.Is(err, cloudlink.ErrNotLinked) {
		httperror.Write(w, r, httperror.NotFound("cloud link"))
		return
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("unlink").Wrap(err))
		return
	}
	h.forgetCloud(userID)
	h.audit(r.Context(), securityaudit.Entry{Event: securityaudit.EventCloudUnlinked, ActorUserID: p.UserID, TargetID: userID})
	common.WriteNoContent(w)
}

func (h *Handler) forgetCloud(userID string) {
	if h.Cloud != nil && h.Cloud.Gate != nil {
		h.Cloud.Gate.Forget(userID)
	}
}
