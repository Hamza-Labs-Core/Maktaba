package clusterauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/delegation"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/homelink"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// CookieSSO holds a cluster sign-in in progress on the node: "<state>.<verifier>".
const CookieSSO = "mkt_sso"

// SelfInfo is GET /api/cluster/self (local on every machine, public): this machine,
// its home, and whether it can serve now. Never a library or user name.
type SelfInfo struct {
	Role      string `json:"role"` // home | node
	ID        string `json:"id"`   // instance id (home) or node id (node)
	Name      string `json:"name,omitempty"`
	Version   string `json:"version"`
	SchemaRev int    `json:"schema_rev"`
	// Term is the cluster's term on the home (Epic 37: each switch of the home begins
	// the next).
	Term      int64     `json:"term,omitempty"`
	WebURL    string    `json:"web_url,omitempty"`
	Home      *SelfHome `json:"home,omitempty"`
	Paused    string    `json:"paused,omitempty"`
	SSO       bool      `json:"sso"`
	AgentLink *bool     `json:"agent_connected,omitempty"`
	VOD       any       `json:"vod,omitempty"`
	// Copy is a full node's copy of its home's database (Epic 37): its supervisor's
	// status of it (state, lag, when it last applied the home's changes).
	Copy     json.RawMessage `json:"copy,omitempty"`
	ServedAt time.Time       `json:"served_at"`
	Machines []SelfRow       `json:"machines,omitempty"`
	// AcceptJoins: the home's "accept new machines" switch (its mDNS join=1).
	AcceptJoins bool `json:"accept_joins,omitempty"`
}

// SelfHome is a node's view of its home.
type SelfHome struct {
	InstanceID   string     `json:"instance_id,omitempty"`
	Name         string     `json:"name,omitempty"`
	WebURL       string     `json:"web_url,omitempty"`
	Version      string     `json:"version,omitempty"`
	Reachable    bool       `json:"reachable"`
	FailingSince *time.Time `json:"failing_since,omitempty"`
}

// SelfRow is one machine's address (the picker learns the cluster's other addresses
// from a machine it reached, story 35.11 EC2).
type SelfRow struct {
	Role   string `json:"role"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	WebURL string `json:"web_url,omitempty"`
}

// SelfFunc builds this machine's SelfInfo.
type SelfFunc func(ctx context.Context) SelfInfo

// Node serves the node's half of the cluster sign-in and its /api/cluster/self.
type Node struct {
	Link   homelink.Link
	NodeID string
	// Self builds /api/cluster/self (it carries the home's web address the sign-in
	// goes through).
	Self          SelfFunc
	SecureCookies bool
	// SSOEnabled reads rules.cluster.sso (nil: on).
	SSOEnabled func(ctx context.Context) bool
	Logger     *slog.Logger
}

// MountSelf mounts GET /api/cluster/self alone (the home serves it too).
func MountSelf(r chi.Router, self SelfFunc) {
	r.Get("/api/cluster/self", func(w http.ResponseWriter, r *http.Request) {
		info := SelfInfo{Role: "home"}
		if self != nil {
			info = self(r.Context())
		}
		info.ServedAt = time.Now().UTC()
		w.Header().Set("Cache-Control", "no-store")
		common.WriteJSON(w, r, http.StatusOK, info)
	})
}

// Mount wires the node's sign-in routes (start, callback).
func (n *Node) Mount(r chi.Router) {
	r.Get("/api/cluster/sso/start", n.Start)
	r.Get("/api/cluster/sso/callback", n.Callback)
}

func randomB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Start is GET /api/cluster/sso/start?return=/library on the node: a PKCE verifier and a
// state kept in a short cookie, then the home's authorize (design §10.5).
func (n *Node) Start(w http.ResponseWriter, r *http.Request) {
	if n == nil || n.Link == nil {
		httperror.Write(w, r, httperror.NotFound("cluster sign-in starts on a machine, not the home"))
		return
	}
	if n.SSOEnabled != nil && !n.SSOEnabled(r.Context()) {
		httperror.Write(w, r, httperror.Forbidden("https://maktaba.dev/problems/sso-off", "cluster sign-in is switched off"))
		return
	}
	ret := r.URL.Query().Get("return")
	if ret == "" {
		ret = "/"
	}
	if !ValidReturnPath(ret) {
		httperror.Write(w, r, httperror.BadRequest("return must be a path of this app"))
		return
	}
	homeWeb := ""
	if n.Self != nil {
		if s := n.Self(r.Context()); s.Home != nil {
			homeWeb = s.Home.WebURL
		}
	}
	if homeWeb == "" {
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/home-unreachable",
			Title: "the home is not reachable", Status: http.StatusServiceUnavailable,
			Detail: "this machine does not know its home's web address yet"})
		return
	}
	verifier, err1 := randomB64(32)
	state, err2 := randomB64(16)
	if err1 != nil || err2 != nil {
		httperror.Write(w, r, httperror.Internal("random"))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: CookieSSO, Value: state + "." + verifier, Path: "/api/cluster/sso",
		MaxAge: 300, HttpOnly: true, Secure: n.SecureCookies, SameSite: http.SameSiteLaxMode})
	q := url.Values{}
	q.Set("node", n.NodeID)
	q.Set("challenge", S256(verifier))
	q.Set("state", state)
	q.Set("return", ret)
	http.Redirect(w, r, strings.TrimRight(homeWeb, "/")+"/api/cluster/sso/authorize?"+q.Encode(), http.StatusFound)
}

// Callback is GET /api/cluster/sso/callback?code=&state= on the node: the state must
// match the cookie; the code and the verifier go to the home (signed), whose answer is
// a child session this machine's host keeps as its own cookie.
func (n *Node) Callback(w http.ResponseWriter, r *http.Request) {
	if n == nil || n.Link == nil {
		httperror.Write(w, r, httperror.NotFound("not a machine"))
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	c, err := r.Cookie(CookieSSO)
	if err != nil || code == "" || state == "" {
		httperror.Write(w, r, httperror.BadRequest("no cluster sign-in in progress on this machine"))
		return
	}
	wantState, verifier, ok := strings.Cut(c.Value, ".")
	http.SetCookie(w, &http.Cookie{Name: CookieSSO, Value: "", Path: "/api/cluster/sso", MaxAge: -1,
		HttpOnly: true, Secure: n.SecureCookies, SameSite: http.SameSiteLaxMode})
	if !ok || subtle.ConstantTimeCompare([]byte(wantState), []byte(state)) != 1 {
		httperror.Write(w, r, httperror.BadRequest("the sign-in does not belong to this browser"))
		return
	}
	ans, err := n.Link.Exchange(r.Context(), delegation.ExchangeRequest{Code: code, Verifier: verifier})
	if err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, homelink.ErrUnreachable) {
			status = http.StatusServiceUnavailable
		}
		httperror.Write(w, r, &httperror.Error{Type: "https://maktaba.dev/problems/sso-refused",
			Title: "cluster sign-in refused", Status: status, Detail: err.Error()})
		return
	}
	maxAge := int(time.Until(ans.ExpiresAt).Seconds())
	// the same cookies as a sign-in (handlers/auth respondWeb): host-only, no Domain
	http.SetCookie(w, &http.Cookie{Name: CookieSession, Value: ans.SessionID, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: n.SecureCookies, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: CookieCSRF, Value: ans.CSRFToken, Path: "/", MaxAge: maxAge,
		HttpOnly: false, Secure: n.SecureCookies, SameSite: http.SameSiteLaxMode})
	dest := ans.ReturnPath
	if !ValidReturnPath(dest) {
		dest = "/"
	}
	if n.Logger != nil {
		n.Logger.Info("node: signed in through the home", "user_id", ans.Principal.UserID, "event", "node_sso_signed_in")
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// HomeInfoCache keeps the home's /api/cluster/self for a node (30 s). Only the first
// read waits for the home (3 s at most); past it a stale answer is given at once and
// read again in the background, so a home that does not answer never holds up the
// machine's own /api/cluster/self.
type HomeInfoCache struct {
	Link homelink.Link
	TTL  time.Duration

	mu      sync.Mutex
	at      time.Time
	info    SelfInfo
	ok      bool
	reading bool
}

func (c *HomeInfoCache) read(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	raw, err := c.Link.Self(ctx)
	cancel()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reading = false
	if err != nil {
		return
	}
	var s SelfInfo
	if json.Unmarshal(raw, &s) == nil {
		c.info, c.ok, c.at = s, true, time.Now()
	}
}

// Get returns the home's self (cached; stale on failure).
func (c *HomeInfoCache) Get(ctx context.Context) (SelfInfo, bool) {
	ttl := c.TTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	c.mu.Lock()
	switch {
	case c.ok && time.Since(c.at) < ttl, c.reading:
	case c.ok:
		c.reading = true
		go c.read(context.WithoutCancel(ctx))
	default:
		c.reading = true
		c.mu.Unlock()
		c.read(ctx)
		c.mu.Lock()
	}
	defer c.mu.Unlock()
	return c.info, c.ok
}
