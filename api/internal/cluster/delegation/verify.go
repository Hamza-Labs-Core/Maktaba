package delegation

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/linkctx"
)

// Rejection reasons (logged, and the problem's code).
const (
	ReasonMissing     = "signature_missing"
	ReasonSkew        = "clock_skew"
	ReasonUnknownNode = "unknown_node"
	ReasonUnknownKey  = "unknown_key"
	ReasonBadSig      = "bad_signature"
	ReasonReplay      = "replay"
	ReasonUnsigned    = "unsigned_body_not_allowed"
	ReasonSession     = "session_mismatch"
	// ReasonLinkRequired: a signed request over plain HTTP while the home requires the
	// cluster link (story 35.16 AC13): the machine's requests go on its link.
	ReasonLinkRequired = "link_required"
)

// RejectError is a refused signature.
type RejectError struct{ Reason string }

func (e *RejectError) Error() string { return "delegation: " + e.Reason }

// KeyLookup finds a node's key by id (the current key, or the previous one inside its
// overlap). ok is false for an unknown or revoked node or key.
type KeyLookup interface {
	Lookup(ctx context.Context, nodeID, keyID string) (secret []byte, ok bool)
}

// Verifier checks node signatures on the home.
type Verifier struct {
	Keys KeyLookup
	// Member reports whether the node is an approved member (a revoked node's keys
	// are deleted too; this is the belt to those braces). Nil: keys only.
	Member func(ctx context.Context, nodeID string) bool
	Now    func() time.Time
	Logger *slog.Logger

	mu   sync.Mutex
	seen map[string]time.Time
}

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) reject(r *http.Request, nodeID, reason string) error {
	if v.Logger != nil {
		v.Logger.Warn("cluster: a node's signed request was refused", "node_id", nodeID, "reason", reason,
			"method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr, "event", "delegation_rejected")
	}
	return &RejectError{Reason: reason}
}

// unsignedAllowed: a streamed body is accepted unhashed only for an upgrade or an
// upload (multipart, octet-stream).
func unsignedAllowed(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "multipart/form-data" || mt == "application/octet-stream"
}

// Verify checks r's signature and returns the node id. The body (when signed) is read
// and put back.
func (v *Verifier) Verify(r *http.Request) (string, error) {
	nodeID := r.Header.Get(HeaderNode)
	keyID := r.Header.Get(HeaderKey)
	tsRaw := r.Header.Get(HeaderTs)
	sigRaw := r.Header.Get(HeaderSig)
	nonce := r.Header.Get(HeaderNonce)
	if nodeID == "" || keyID == "" || tsRaw == "" || sigRaw == "" || nonce == "" || len(nonce) > 64 {
		return "", v.reject(r, nodeID, ReasonMissing)
	}
	u, err := uuid.Parse(nodeID)
	if err != nil {
		return "", v.reject(r, nodeID, ReasonUnknownNode)
	}
	nodeID = u.String()
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return "", v.reject(r, nodeID, ReasonMissing)
	}
	now := v.now()
	if d := now.Sub(time.Unix(ts, 0)); d > MaxSkew || d < -MaxSkew {
		return "", v.reject(r, nodeID, ReasonSkew)
	}
	if v.Member != nil && !v.Member(r.Context(), nodeID) {
		return "", v.reject(r, nodeID, ReasonUnknownNode)
	}
	secret, ok := v.Keys.Lookup(r.Context(), nodeID, keyID)
	if !ok {
		return "", v.reject(r, nodeID, ReasonUnknownKey)
	}
	bodyHash := EmptyBodyHash
	if strings.EqualFold(r.Header.Get(HeaderBody), "unsigned") {
		if !unsignedAllowed(r) {
			return "", v.reject(r, nodeID, ReasonUnsigned)
		}
		bodyHash = UnsignedPayload
	} else if r.Body != nil && r.Body != http.NoBody {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxSignedBody+1))
		_ = r.Body.Close()
		if err != nil || int64(len(b)) > maxSignedBody {
			return "", v.reject(r, nodeID, ReasonUnsigned)
		}
		r.Body = io.NopCloser(bytes.NewReader(b))
		bodyHash = hexSHA256(b)
	}
	sig, err := base64.StdEncoding.DecodeString(sigRaw)
	if err != nil {
		return "", v.reject(r, nodeID, ReasonBadSig)
	}
	want, _ := base64.StdEncoding.DecodeString(Sign(secret, r.Method, pathQuery(r), ts, nonce, bodyHash,
		SessionHash(r.Header.Get(HeaderDelegatedSession))))
	if !hmac.Equal(sig, want) {
		return "", v.reject(r, nodeID, ReasonBadSig)
	}
	if !v.fresh(nodeID+"|"+tsRaw+"|"+nonce, now) {
		return "", v.reject(r, nodeID, ReasonReplay)
	}
	return nodeID, nil
}

// fresh records a (node, ts, sig) and reports whether it was new within the window.
func (v *Verifier) fresh(id string, now time.Time) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seen == nil {
		v.seen = map[string]time.Time{}
	}
	if len(v.seen) > 50000 {
		for k, t := range v.seen {
			if now.Sub(t) > 2*MaxSkew {
				delete(v.seen, k)
			}
		}
	}
	if t, ok := v.seen[id]; ok && now.Sub(t) <= 2*MaxSkew {
		return false
	}
	v.seen[id] = now
	return true
}

type ctxKey struct{}

// WithNode marks ctx as a request a node signed.
func WithNode(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, nodeID)
}

// NodeFrom is the verified node id of a signed request ("" otherwise).
func NodeFrom(ctx context.Context) string {
	s, _ := ctx.Value(ctxKey{}).(string)
	return s
}

// Accept is the home's middleware for requests from nodes (design §11.2): a request
// that carries any node header must verify, else 401 (and the rejection is logged). A
// verified request is marked (NodeFrom); its delegated session becomes the request's
// session cookie, so the home resolves the user itself (it never trusts a node's
// claim about a user). Requests without node headers pass untouched.
func Accept(v *Verifier, sessionCookie string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// over its link the machine is proved by its certificate (story 35.16): no
			// signature is needed, and none is checked
			if nodeID, ok := linkctx.Node(r.Context()); ok {
				if !delegatedSession(w, r, v, nodeID, sessionCookie) {
					return
				}
				r.Header.Del(HeaderVia)
				next.ServeHTTP(w, r.WithContext(WithNode(r.Context(), nodeID)))
				return
			}
			if !Signed(r) {
				next.ServeHTTP(w, r)
				return
			}
			if linkctx.Require() {
				writeReject(w, ReasonLinkRequired)
				return
			}
			if v == nil || v.Keys == nil {
				writeReject(w, ReasonUnknownNode)
				return
			}
			nodeID, err := v.Verify(r)
			if err != nil {
				var re *RejectError
				reason := ReasonBadSig
				if errors.As(err, &re) {
					reason = re.Reason
				}
				writeReject(w, reason)
				return
			}
			if !delegatedSession(w, r, v, nodeID, sessionCookie) {
				return
			}
			// the marker is ours now: the router's StripVia must not see a forged one
			r.Header.Del(HeaderVia)
			next.ServeHTTP(w, r.WithContext(WithNode(r.Context(), nodeID)))
		})
	}
}

// delegatedSession puts the user's session the machine acts for in the request's cookie
// (a cookie that names another one is refused); false when it answered.
func delegatedSession(w http.ResponseWriter, r *http.Request, v *Verifier, nodeID, sessionCookie string) bool {
	if sid := r.Header.Get(HeaderDelegatedSession); sid != "" && sessionCookie != "" {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" && c.Value != sid {
			if v != nil {
				_ = v.reject(r, nodeID, ReasonSession)
			}
			writeReject(w, ReasonSession)
			return false
		}
		setCookie(r, sessionCookie, sid)
	}
	return true
}

// setCookie replaces (or adds) one cookie in the request's Cookie header.
func setCookie(r *http.Request, name, value string) {
	var kept []string
	for _, c := range r.Cookies() {
		if c.Name != name {
			kept = append(kept, c.Name+"="+c.Value)
		}
	}
	kept = append(kept, name+"="+value)
	r.Header.Set("Cookie", strings.Join(kept, "; "))
}

func writeReject(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "https://maktaba.tv/problems/node-signature", "title": "node signature refused",
		"status": http.StatusUnauthorized, "code": reason,
	})
}
