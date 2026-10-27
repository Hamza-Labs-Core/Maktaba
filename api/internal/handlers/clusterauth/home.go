// Package clusterauth serves sign-ins across the machines of a cluster (Epic 35 story
// 35.9, design §10): on the home, the delegation endpoint a full node resolves a
// session, a bearer or a personal access token through, and the sign-outs a node made
// while the home did not answer; on every machine, its /api/cluster/self (node.go). A
// sign-in on a node is the sign-in form, checked against the home's accounts (the
// cluster sign-in that carried a home's session to a node is gone: RemovedRoutes).
//
// Every endpoint here that a node calls must carry the node's signature: the
// delegation middleware (cluster/delegation.Accept) verified it and marked the request
// before it reaches these handlers; a request without that mark is refused.
package clusterauth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/authz"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/cloudlink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/pat"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/sessions"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/users"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
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
)

// Home serves the home's half.
type Home struct {
	DB       *sql.DB
	Sessions *sessions.Store
	Users    *users.Store
	ACL      *authz.ACLStore
	PAT      *pat.Store
	Now      func() time.Time
	Logger   *slog.Logger
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
		httperror.Write(w, r, httperror.Internal("introspect").Wrap(err))
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
		httperror.Write(w, r, httperror.Internal("signout").Wrap(err))
		return
	}
	n := 0
	for _, id := range ids {
		if err := h.Sessions.Revoke(ctx, id); err != nil {
			httperror.Write(w, r, httperror.Internal("signout").Wrap(err))
			return
		}
		n++
	}
	if req.UserID != "" && req.Before != nil {
		through, err := h.signedInThrough(ctx, node, req.UserID)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("signout").Wrap(err))
			return
		}
		if !through {
			h.log().WarnContext(r.Context(), "home: a machine asked to sign out everywhere a person who never signed in through it; refused",
				"node_id", node, "user_id", req.UserID, "event", "cluster_node_signout_refused")
		} else {
			m, err := h.Sessions.RevokeAllForUserBefore(ctx, req.UserID, *req.Before)
			if err != nil {
				httperror.Write(w, r, httperror.Internal("signout").Wrap(err))
				return
			}
			n += int(m)
			if _, err := h.DB.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = now()
			             WHERE user_id = $1 AND revoked_at IS NULL AND issued_at < $2`, req.UserID, *req.Before); err != nil {
				httperror.Write(w, r, httperror.Internal("signout").Wrap(err))
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
		// a session a Maktaba Cloud sign-in made keeps its link's scope on
		// every machine: a play one is no admin (Epic 42 story 42.7)
		scope, err := (&cloudlink.Store{DB: h.DB}).ScopeOf(ctx, cloudlink.KindSession, sess.ID)
		if err != nil {
			return nil, err
		}
		if scope == principal.CloudScopePlay {
			if err := capPlay(ctx, h.DB, &p); err != nil {
				return nil, err
			}
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
