// Package clusterauth serves sign-ins across the machines of a cluster (Epic 35 story
// 35.9, design §10): on the home, the delegation endpoint a full node resolves a
// session, a bearer or a personal access token through, and the two halves of the
// cluster sign-in the home owns (authorize, exchange); on a node, the cluster sign-in's
// start and callback (node.go).
//
// Every endpoint here that a node calls must carry the node's signature: the
// delegation middleware (cluster/delegation.Accept) verified it and marked the request
// before it reaches these handlers; a request without that mark is refused.
package clusterauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/nodemode"
)

// Cookie names, as handlers/auth sets them (kept here to avoid an import cycle).
const (
	CookieSession = "mkt_sess"
	CookieCSRF    = "mkt_csrf"
)

// Wire shapes (cluster/delegation).
type (
	Principal         = delegation.Principal
	IntrospectRequest = delegation.IntrospectRequest
	IntrospectAnswer  = delegation.IntrospectAnswer
	ExchangeRequest   = delegation.ExchangeRequest
	ExchangeAnswer    = delegation.ExchangeAnswer
)

// SSOCodeTTL is how long a cluster sign-in code may be exchanged (design §10.5).
const SSOCodeTTL = 60 * time.Second

// Home serves the home's half.
type Home struct {
	DB       *sql.DB
	Sessions *sessions.Store
	Users    *users.Store
	ACL      *authz.ACLStore
	PAT      *pat.Store
	Nodes    cluster.Store
	// WebURL is the home's own web address (its login page for the cluster sign-in).
	WebURL string
	Now    func() time.Time
	Logger *slog.Logger
}

func (h *Home) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h *Home) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// Mount wires the home's routes.
func (h *Home) Mount(r chi.Router) {
	r.Post("/api/cluster/delegate/introspect", h.Introspect)
	r.Post("/api/cluster/delegate/signout", h.SignOut)
	r.Get("/api/cluster/sso/authorize", h.Authorize)
	r.Post("/api/cluster/sso/exchange", h.Exchange)
}

func unauthorized(w http.ResponseWriter, r *http.Request, detail string) {
	httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/unauthorized",
		Title: "unauthorized", Status: http.StatusUnauthorized, Detail: detail})
}

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func (h *Home) principalFor(ctx context.Context, userID string) (Principal, error) {
	u, err := h.Users.GetByID(ctx, userID)
	if err != nil {
		return Principal{}, err
	}
	p := Principal{UserID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin, AccessAllLibraries: u.IsAdmin,
		Libraries: []string{}, MustChangePassword: u.MustChangePassword}
	if !u.IsAdmin && h.ACL != nil {
		if libs, err := h.ACL.LibrariesFor(ctx, u.ID); err == nil && libs != nil {
			p.Libraries = libs
		}
	}
	return p, nil
}

// Introspect is POST /api/cluster/delegate/introspect (node-signed): a session id or a
// personal access token → the principal, the CSRF token's hash and when it expires; 401
// for anything that is not a live credential.
func (h *Home) Introspect(w http.ResponseWriter, r *http.Request) {
	node := delegation.NodeFrom(r.Context())
	if node == "" {
		unauthorized(w, r, "only a machine of this cluster may ask")
		return
	}
	var req IntrospectRequest
	if e := common.ReadJSON(r, &req, 8<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if req.Session == "" && req.PAT == "" {
		httperror.Write(w, r, httperror.BadRequest("session or pat required"))
		return
	}
	ans, err := h.Resolve(r.Context(), req)
	if err != nil {
		var no *Refusal
		if errors.As(err, &no) {
			unauthorized(w, r, no.Reason)
			return
		}
		httperror.Write(w, r, httperror.Internal("introspect"))
		return
	}
	common.WriteJSON(w, r, http.StatusOK, ans)
}

// SignOut is POST /api/cluster/delegate/signout (node-signed): the sign-outs a node
// made while this home did not answer (#145), handed over once it answers again. Its
// sessions are named by the SHA-256 of their id (the node keeps no id); a "sign out
// everywhere" ends the user's sessions and refresh tokens made before it. Every node
// drops them from its cache (the revocation hook).
//
// A machine ends only what was signed in through it: the sessions made through it
// (origin_node_id: a sign-in on it, or its cluster sign-in; their cookies are its own
// host's, so those are the sessions it serves), and "everywhere" only for a person who
// signed in through it at least once. Every machine's copy holds every session's hash;
// without this, any machine could sign everyone out. Anything else named is left as it
// is (and answered as taken: asking again would not change it).
func (h *Home) SignOut(w http.ResponseWriter, r *http.Request) {
	node := delegation.NodeFrom(r.Context())
	if node == "" {
		unauthorized(w, r, "only a machine of this cluster may ask")
		return
	}
	var req delegation.SignOutRequest
	if e := common.ReadJSON(r, &req, 64<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	if len(req.SessionSHA256) > 1000 || (req.Before != nil && req.UserID == "") {
		httperror.Write(w, r, httperror.BadRequest("at most 1000 sessions; a user with before"))
		return
	}
	if _, err := uuid.Parse(req.UserID); req.UserID != "" && err != nil {
		httperror.Write(w, r, httperror.BadRequest("user_id is not a user's id"))
		return
	}
	ctx := r.Context()
	ids, err := h.sessionsThrough(ctx, node, req.SessionSHA256)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("signout"))
		return
	}
	n := 0
	for _, id := range ids {
		if err := h.Sessions.Revoke(ctx, id); err != nil {
			httperror.Write(w, r, httperror.Internal("signout"))
			return
		}
		n++
	}
	if req.UserID != "" && req.Before != nil {
		through, err := h.signedInThrough(ctx, node, req.UserID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("signout"))
			return
		}
		if !through {
			h.log().Warn("home: a machine asked to sign out everywhere a person who never signed in through it; refused",
				"node_id", node, "user_id", req.UserID, "event", "cluster_node_signout_refused")
		} else {
			m, err := h.Sessions.RevokeAllForUserBefore(ctx, req.UserID, *req.Before)
			if err != nil {
				httperror.Write(w, r, httperror.Internal("signout"))
				return
			}
			n += int(m)
			if _, err := h.DB.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = now()
			             WHERE user_id = $1 AND revoked_at IS NULL AND issued_at < $2`, req.UserID, *req.Before); err != nil {
				httperror.Write(w, r, httperror.Internal("signout"))
				return
			}
		}
	}
	h.log().Info("home: sign-outs a machine made while this home did not answer", "node_id", node,
		"sessions", len(req.SessionSHA256), "through_it", len(ids), "user_id", req.UserID, "revoked", n,
		"event", "cluster_node_signouts")
	common.WriteJSON(w, r, http.StatusOK, delegation.SignOutAnswer{Revoked: n})
}

// sessionsThrough is the ids of the live sessions made through node (origin_node_id)
// whose id hashes (SHA-256, hex) to one of sha256Hex.
func (h *Home) sessionsThrough(ctx context.Context, node string, sha256Hex []string) ([]string, error) {
	nodeID, err := uuid.Parse(node)
	if len(sha256Hex) == 0 || err != nil {
		return nil, nil
	}
	want := make(map[string]bool, len(sha256Hex))
	for _, s := range sha256Hex {
		want[strings.ToLower(s)] = true
	}
	rows, err := h.DB.QueryContext(ctx, `SELECT CAST(id AS TEXT) FROM web_sessions
	        WHERE origin_node_id = $1 AND revoked_at IS NULL`, nodeID.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if want[hexSum(id)] {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// signedInThrough reports whether the user has a session made through node (live or
// not: the one they signed out of there may have ended already).
func (h *Home) signedInThrough(ctx context.Context, node, userID string) (bool, error) {
	nodeID, err := uuid.Parse(node)
	if err != nil {
		return false, nil
	}
	var through bool
	err = h.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM web_sessions
	        WHERE user_id = $1 AND origin_node_id = $2)`, userID, nodeID.String()).Scan(&through)
	return through, err
}

// Refusal: the credential is not a live one (Resolve); Reason says why.
type Refusal struct{ Reason string }

func (e *Refusal) Error() string { return "clusterauth: " + e.Reason }

// Resolve introspects one credential against h's database (the home's own, or a full
// node's copy of it, read only: the last-seen and last-used marks are best-effort):
// the principal, the CSRF token's hash and when the credential expires; a *Refusal
// for anything that is not a live credential, another error when the database did not
// say.
func (h *Home) Resolve(ctx context.Context, req IntrospectRequest) (*IntrospectAnswer, error) {
	var ans IntrospectAnswer
	switch {
	case req.Session != "":
		if _, err := uuid.Parse(req.Session); err != nil {
			return nil, &Refusal{Reason: "not a session"}
		}
		sess, err := h.Sessions.Lookup(ctx, req.Session)
		if errors.Is(err, sessions.ErrNotFound) {
			return nil, &Refusal{Reason: "session ended"}
		}
		if err != nil {
			return nil, err
		}
		p, err := h.livePrincipal(ctx, sess.UserID)
		if err != nil {
			return nil, err
		}
		_ = h.Sessions.TouchLastSeen(ctx, sess.ID, h.now())
		ans = IntrospectAnswer{Principal: p, Source: string(principal.SourceCookie),
			CSRFSHA256: hexSum(sess.CSRFToken), ExpiresAt: sess.ExpiresAt}
		if origin, _, err := h.Sessions.Origin(ctx, sess.ID); err == nil && origin != "" {
			ans.OriginNodeID = &origin
		}
	case req.PAT != "":
		if h.PAT == nil || !pat.IsPAT(req.PAT) {
			return nil, &Refusal{Reason: "not a token"}
		}
		tok, err := h.PAT.Authenticate(ctx, req.PAT)
		if errors.Is(err, pat.ErrNotFound) || errors.Is(err, pat.ErrMalformed) {
			return nil, &Refusal{Reason: "token not valid"}
		}
		if err != nil {
			return nil, err
		}
		p, err := h.livePrincipal(ctx, tok.UserID)
		if err != nil {
			return nil, err
		}
		// the must-change gate is a web-session rule (the home's PAT path does
		// not apply it either)
		p.MustChangePassword = false
		ans = IntrospectAnswer{Principal: p, Source: string(principal.SourcePAT), ExpiresAt: h.now().Add(time.Hour)}
		if tok.ExpiresAt != nil && tok.ExpiresAt.Before(ans.ExpiresAt) {
			ans.ExpiresAt = *tok.ExpiresAt
		}
	default:
		return nil, &Refusal{Reason: "session or pat required"}
	}
	return &ans, nil
}

// livePrincipal is principalFor with a user that is gone as a *Refusal (a database
// that does not answer is an error: no one is signed out for it).
func (h *Home) livePrincipal(ctx context.Context, userID string) (Principal, error) {
	p, err := h.principalFor(ctx, userID)
	if errors.Is(err, users.ErrNotFound) {
		return Principal{}, &Refusal{Reason: "user gone"}
	}
	return p, err
}

// clientIP is the browser's address the node forwarded (first X-Forwarded-For entry),
// when it is an IP; "" otherwise (web_sessions.ip is inet).
func clientIP(r *http.Request) string {
	first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-For"), ",")
	if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
		return ip.String()
	}
	return ""
}

// ValidReturnPath accepts only a relative path of this app: "/" then not "/" or "\"
// (no scheme, no host, no protocol-relative URL).
func ValidReturnPath(p string) bool {
	if p == "" || len(p) > 1024 || p[0] != '/' {
		return false
	}
	if len(p) > 1 && (p[1] == '/' || p[1] == '\\') {
		return false
	}
	if strings.ContainsAny(p, "\r\n\x00") || strings.Contains(p, "://") {
		return false
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == ""
}

func validChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil && len(b) == 32
}

// S256 is the PKCE challenge of a verifier: b64url(sha256(verifier)), no padding.
func S256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Authorize is GET /api/cluster/sso/authorize?node=&challenge=&state=&return= (the
// browser, on the home's address). Signed in on the home: a one-time code (32 bytes,
// stored hashed, 60 s, single use, bound to the node and the S256 challenge) and a
// redirect to the node's callback. Not signed in: the home's sign-in page, which comes
// back here.
func (h *Home) Authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	nodeID, challenge, state, ret := q.Get("node"), q.Get("challenge"), q.Get("state"), q.Get("return")
	if ret == "" {
		ret = "/"
	}
	if _, err := uuid.Parse(nodeID); err != nil || !validChallenge(challenge) || state == "" || len(state) > 128 ||
		!ValidReturnPath(ret) {
		httperror.Write(w, r, httperror.BadRequest("node, challenge (S256), state and a relative return path are required"))
		return
	}
	ctx := r.Context()
	rules := nodemode.ReadRules(ctx, h.DB)
	if !rules.SSO {
		httperror.Write(w, r, httperror.Forbidden("https://maktaba.dev/problems/sso-off",
			"cluster sign-in is switched off (rules.cluster.sso)"))
		return
	}
	n, err := h.Nodes.Node(ctx, nodeID)
	if err != nil || n.State != cluster.StateApproved || !n.FullNode || n.WebURL == nil || *n.WebURL == "" {
		httperror.Write(w, r, httperror.Forbidden("https://maktaba.dev/problems/sso-node",
			"that machine is not a full machine of this cluster"))
		return
	}
	c, cerr := r.Cookie(CookieSession)
	p := principal.FromContext(ctx)
	var sess *sessions.Session
	if cerr == nil && c.Value != "" && p != nil && p.Source == principal.SourceCookie {
		sess, _ = h.Sessions.Lookup(ctx, c.Value)
	}
	if sess == nil {
		// sign in on the home first; its login page comes back to this URL
		next := r.URL.RequestURI()
		dest := strings.TrimRight(h.WebURL, "/") + "/login?next=" + url.QueryEscape(next)
		if h.WebURL == "" {
			dest = "/login?next=" + url.QueryEscape(next)
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		httperror.Write(w, r, httperror.Internal("code"))
		return
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := h.DB.ExecContext(ctx, `INSERT INTO cluster_sso_codes
		(code_hash, node_id, user_id, parent_session_id, pkce_challenge, return_path, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		hexSum(code), n.ID, sess.UserID, sess.ID, challenge, ret, h.now(), h.now().Add(SSOCodeTTL)); err != nil {
		h.log().Warn("cluster: sign-in code not stored", "err", err, "event", "cluster_sso_store_failed")
		httperror.Write(w, r, httperror.Internal("store code"))
		return
	}
	h.log().Info("cluster: sign-in carried to a machine", "node_id", n.ID, "user_id", sess.UserID,
		"event", "cluster_sso_authorized")
	dest := strings.TrimRight(*n.WebURL, "/") + "/api/cluster/sso/callback?code=" + url.QueryEscape(code) +
		"&state=" + url.QueryEscape(state)
	http.Redirect(w, r, dest, http.StatusFound)
}

// ErrCode is a refused code (unknown, used, expired, another node's, wrong verifier).
var ErrCode = errors.New("cluster sign-in code refused")

// Exchange is POST /api/cluster/sso/exchange (node-signed): the code, once, by the node
// it was issued for, with the PKCE verifier → a child session of the home session
// (parent_session_id, origin_node_id). A wrong verifier burns the code.
func (h *Home) Exchange(w http.ResponseWriter, r *http.Request) {
	node := delegation.NodeFrom(r.Context())
	if node == "" {
		unauthorized(w, r, "only a machine of this cluster may exchange a code")
		return
	}
	var req ExchangeRequest
	if e := common.ReadJSON(r, &req, 4<<10); e != nil {
		httperror.Write(w, r, e)
		return
	}
	ctx := r.Context()
	var codeNode, userID, parent, challenge, ret string
	err := h.DB.QueryRowContext(ctx, `UPDATE cluster_sso_codes SET used_at = $2
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING CAST(node_id AS TEXT), CAST(user_id AS TEXT), CAST(parent_session_id AS TEXT), pkce_challenge, return_path`,
		hexSum(req.Code), h.now()).Scan(&codeNode, &userID, &parent, &challenge, &ret)
	refuse := func(why string) {
		h.log().Warn("cluster: sign-in code refused", "node_id", node, "reason", why, "event", "cluster_sso_refused")
		unauthorized(w, r, "code refused")
	}
	if err != nil {
		refuse("unknown, used or expired")
		return
	}
	if !strings.EqualFold(codeNode, node) {
		refuse("issued for another machine")
		return
	}
	if req.Verifier == "" || S256(req.Verifier) != challenge {
		refuse("verifier does not match")
		return
	}
	ps, err := h.Sessions.Lookup(ctx, parent)
	if err != nil || ps.UserID != userID {
		refuse("the home session ended")
		return
	}
	child, err := h.Sessions.Create(ctx, sessions.CreateInput{UserID: userID, OriginNodeID: node,
		ParentSessionID: parent, IP: clientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		httperror.Write(w, r, httperror.Internal("create session"))
		return
	}
	p, err := h.principalFor(ctx, userID)
	if err != nil {
		refuse("user gone")
		return
	}
	common.WriteJSON(w, r, http.StatusOK, ExchangeAnswer{SessionID: child.ID, CSRFToken: child.CSRFToken,
		ExpiresAt: child.ExpiresAt, ReturnPath: ret, Principal: p})
}
