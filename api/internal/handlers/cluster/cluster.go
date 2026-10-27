// Package cluster serves the home side of multi-instance Maktaba (Epic 31,
// specs/epics/31-multi-instance/design.md §8):
//
//	POST   /api/cluster/join                  public, rate-limited: a machine asks to join
//	GET    /api/cluster/join/{node_id}?pubkey= public, rate-limited: it polls for the answer
//	GET    /api/cluster                       admin: this home and every machine
//	PUT    /api/cluster/settings              admin: {accept_joins?, name?}
//	POST   /api/cluster/nodes/{id}/approve    admin: {code}
//	POST   /api/cluster/nodes/{id}/reject     admin
//	POST   /api/cluster/nodes/{id}/revoke     admin
//	PUT    /api/cluster/nodes/{id}            admin: {name?, drain?, path_map?, stages?}
//	DELETE /api/cluster/nodes/{id}            admin: rejected / revoked (or expired) only
//
// Errors are problem+json with a top-level "code" (joins_closed,
// already_member, wrong_code + attempts_left, expired, not_pending,
// roles_unsupported, unknown_stage, not_removable).
package cluster

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	mw "github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// Rules of design §3.4.
const (
	MaxCodeAttempts = 5
	RequestTTL      = 15 * time.Minute
	SealedTTL       = 15 * time.Minute
	AcceptJoinsTTL  = time.Hour
	// OnlineWindow: a machine whose worker (or heartbeat) was seen within
	// this window is online.
	OnlineWindow = 60 * time.Second

	DefaultJoinRatePerMin = 10
	// DefaultPollRatePerMin: the node polls every 2 s while it waits.
	DefaultPollRatePerMin = 60
	DefaultPGPort         = 5432

	maxJoinBody = 64 << 10
	maxNameLen  = 100
)

// Handler serves /api/cluster/*.
type Handler struct {
	Store cluster.Store
	Roles cluster.Roles
	// Advertiser re-publishes the mDNS announcement when the name or the
	// accept-joins switch changes. Nil: nothing is advertised.
	Advertiser *cluster.Advertiser
	// PGPort is the database port handed to machines
	// (MAKTABA_CLUSTER_PG_PORT); 0 means 5432.
	PGPort int
	// PGBundled: the database is this home's bundled Postgres, whose CA is PGCA (the
	// file; "" with its TLS off), sealed in a join's credentials.
	PGBundled bool
	PGCA      string
	// MendBundled asks the supervisor to give the bundled Postgres's app role its cluster
	// grants again (CREATEROLE, REPLICATION, ADMIN on the machines' roles: pgbundle's cluster
	// bootstrap), when it lacks them (a move or a restore without them); nil: not this home's
	// to mend.
	MendBundled func(ctx context.Context) error
	// RolesDB is the home's database as its own user (the roles' Postgres): a missing
	// group role is made again on it (cluster.EnsureWorkerGroup); nil: not made.
	RolesDB *sql.DB
	// PGPlain: the database is another one (an operator's own) and has no TLS at all
	// (`ssl` off): the machines are told so, as for a bundled one with its TLS off. One
	// with TLS has the operator's CA, which this home does not hold: the machines'
	// copies wait for one.
	PGPlain atomic.Bool
	// JoinRatePerMin / PollRatePerMin cap the public endpoints per IP;
	// 0 uses the defaults, negative disables the limiter (tests).
	JoinRatePerMin int
	PollRatePerMin int
	NowFunc        func() time.Time
	Logger         *slog.Logger
	// ClusterPub is the base64 public half of the home's cluster key
	// (design-agent.md §4.2), sealed into every approved machine's
	// credentials as home.cluster_pub.
	ClusterPub string
	// OnRevoke is called after a machine is revoked or removed: the
	// cluster channel closes its sessions at once (design-agent.md §2).
	OnRevoke func(nodeID string)
	// LiveStates are the connected machine agents' last update.state, by node id (the
	// hub's): a node's view carries its own (its release, its run, its login to this
	// home's database); nil: the stored one.
	LiveStates func() map[string]json.RawMessage
	// WorkerConvert says what becomes of each worker-only machine (the update driver's,
	// story 35.8 AC8): next_update or installer; nil: not said.
	WorkerConvert func(ctx context.Context) map[string]string
	// OnNodeChanged is called after an admin changes a machine's stage
	// switches or drain: its processes reload their stages at once.
	OnNodeChanged func(nodeID string)
	// Storage holds the storage servers and the home's share report (story
	// 31.3). Nil: those routes answer 404.
	Storage cluster.StorageStore
	// WebURL and Version are the home's own web address and release (Epic 35 story
	// 35.11), shown next to the machines'.
	WebURL  string
	Version string
	// JoinTLS signs a joining machine's CSR for its sealed credentials (story 35.16);
	// nil: the home has no cluster CA (the machine asks again on its channel session).
	JoinTLS func(ctx context.Context, nodeID, csrPEM string) (*cluster.PayloadTLS, error)
	// Link is the cluster link's state for the Machines page; nil: none.
	Link func(ctx context.Context) any

	// csrs are the pending join requests' CSRs (kept until approved or expired; a home
	// restarted meanwhile approves without one, and the machine asks on its channel)
	csrMu sync.Mutex
	csrs  map[string]pendingCSR
}

type pendingCSR struct {
	pem     string
	expires time.Time
}

// keepCSR remembers a join request's CSR until it expires.
func (h *Handler) keepCSR(nodeID, csr string, expires time.Time) {
	h.csrMu.Lock()
	defer h.csrMu.Unlock()
	if h.csrs == nil {
		h.csrs = map[string]pendingCSR{}
	}
	now := h.now()
	for id, p := range h.csrs {
		if now.After(p.expires) {
			delete(h.csrs, id)
		}
	}
	if csr == "" {
		delete(h.csrs, nodeID)
		return
	}
	h.csrs[nodeID] = pendingCSR{pem: csr, expires: expires}
}

// takeCSR is a pending request's CSR ("" for none), forgotten once taken.
func (h *Handler) takeCSR(nodeID string) string {
	h.csrMu.Lock()
	defer h.csrMu.Unlock()
	p, ok := h.csrs[nodeID]
	delete(h.csrs, nodeID)
	if !ok || h.now().After(p.expires) {
		return ""
	}
	return p.pem
}

// Mount wires the routes.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(mw.BodyLimit(maxJoinBody))
		r.With(limiter(h.JoinRatePerMin, DefaultJoinRatePerMin)...).Post("/api/cluster/join", h.Join)
		r.With(limiter(h.PollRatePerMin, DefaultPollRatePerMin)...).Get("/api/cluster/join/{node_id}", h.Poll)

		r.Get("/api/cluster", h.Overview)
		r.Put("/api/cluster/settings", h.Settings)
		r.Post("/api/cluster/nodes/{id}/approve", h.Approve)
		r.Post("/api/cluster/nodes/{id}/reject", h.Reject)
		r.Post("/api/cluster/nodes/{id}/revoke", h.Revoke)
		r.Put("/api/cluster/nodes/{id}", h.UpdateNode)
		r.Delete("/api/cluster/nodes/{id}", h.DeleteNode)
		h.mountStorage(r)
	})
}

func limiter(perMin, def int) []func(http.Handler) http.Handler {
	switch {
	case perMin < 0:
		return nil
	case perMin == 0:
		perMin = def
	}
	return []func(http.Handler) http.Handler{mw.PerIP(perMin)}
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc().UTC()
	}
	return time.Now().UTC()
}

func (h *Handler) log() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// pgTLS is how a machine verifies this home's database (the join's credentials): the
// bundled Postgres's CA, or "off" with its TLS off; for another database, "off" when it
// has no TLS (PGPlain), else neither (its TLS is its own).
func (h *Handler) pgTLS() (ca, tls string) {
	switch {
	case !h.PGBundled && h.PGPlain.Load():
		return "", "off"
	case !h.PGBundled:
		return "", ""
	case h.PGCA == "":
		return "", "off"
	}
	b, err := os.ReadFile(h.PGCA)
	if err != nil {
		h.log().Warn("cluster: this home's database CA cannot be read for a join", "err", err,
			"event", "cluster_database_ca_unreadable")
		return "", ""
	}
	return string(b), ""
}

func (h *Handler) pgPort() int {
	if h.PGPort > 0 {
		return h.PGPort
	}
	return DefaultPGPort
}

// Bootstrap makes sure the identity row exists (name = the machine's
// stable host name on first start) and announces the home.
func (h *Handler) Bootstrap(ctx context.Context, name string) (cluster.Identity, error) {
	if _, err := h.Store.EnsureIdentity(ctx, name); err != nil {
		return cluster.Identity{}, err
	}
	id, err := h.identity(ctx)
	if err != nil {
		return cluster.Identity{}, err
	}
	h.announce(ctx, id)
	return id, nil
}

// Run does the housekeeping every interval until ctx ends: the
// accept-joins switch turns itself off after its hour (and the
// announcement follows), and sealed credentials are dropped once used.
func (h *Handler) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := h.identity(ctx); err != nil {
				h.log().Warn("cluster: housekeeping failed", "err", err, "event", "cluster_tick_failed")
			}
			if err := h.Store.ClearSealed(ctx, h.now().Add(-SealedTTL)); err != nil {
				h.log().Warn("cluster: clearing sealed credentials failed", "err", err, "event", "cluster_tick_failed")
			}
		}
	}
}

// identity reads the identity, first closing joins whose hour is up.
func (h *Handler) identity(ctx context.Context) (cluster.Identity, error) {
	closed, err := h.Store.CloseExpiredJoins(ctx, h.now())
	if err != nil {
		return cluster.Identity{}, err
	}
	id, err := h.Store.Identity(ctx)
	if err != nil {
		return cluster.Identity{}, err
	}
	if closed {
		h.announce(ctx, id)
	}
	return id, nil
}

func (h *Handler) announce(ctx context.Context, id cluster.Identity) {
	if err := h.Advertiser.Announce(ctx, id); err != nil {
		h.log().Warn("cluster: mDNS re-announce failed", "err", err, "event", "cluster_mdns_failed")
	}
}

// ---------- errors ----------

func problem(status int, code, title, detail string) *httperror.Error {
	return (&httperror.Error{
		Type:   "https://maktaba.dev/problems/" + strings.ReplaceAll(code, "_", "-"),
		Title:  title,
		Status: status,
		Detail: detail,
	}).With("code", code)
}

func errJoinsClosed(detail string) *httperror.Error {
	return problem(http.StatusForbidden, "joins_closed", "joins closed", detail)
}

func errInternal(w http.ResponseWriter, r *http.Request, what string, err error) {
	slog.ErrorContext(r.Context(), "cluster: "+what, "err", err, "event", "cluster_error")
	httperror.Write(w, r, httperror.Internal(what))
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, httperror.Forbidden("", "auth required"))
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

func actor(r *http.Request) string {
	if p := principal.FromContext(r.Context()); p != nil && p.UserID != "" {
		return "user:" + p.UserID
	}
	return "system"
}

// canonicalID parses a uuid into its canonical lowercase form.
func canonicalID(s string) (string, bool) {
	u, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", false
	}
	return u.String(), true
}

// ---------- public: join ----------

// JoinRequest is the body of POST /api/cluster/join.
type JoinRequest struct {
	NodeID       string          `json:"node_id"`
	Name         string          `json:"name"`
	OS           string          `json:"os"`
	Arch         string          `json:"arch"`
	Versions     json.RawMessage `json:"versions"`
	Capabilities json.RawMessage `json:"capabilities"`
	Storage      json.RawMessage `json:"storage"`
	Pubkey       string          `json:"pubkey"`
	// CSR is the machine's cluster-link certificate request (story 35.16; its key made
	// on the machine); an older machine sends none.
	CSR string `json:"csr,omitempty"`
}

// JoinHome identifies the home in the join answer.
type JoinHome struct {
	InstanceID string `json:"instance_id"`
	Name       string `json:"name"`
}

// JoinResponse is the 202 answer of POST /api/cluster/join.
type JoinResponse struct {
	RequestID  string    `json:"request_id"`
	HomePubkey string    `json:"home_pubkey"`
	Home       JoinHome  `json:"home"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// Join implements POST /api/cluster/join.
func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := h.identity(ctx)
	if err != nil {
		errInternal(w, r, "read identity", err)
		return
	}
	if ok, reason := h.Roles.Supported(ctx); !ok {
		httperror.Write(w, r, errJoinsClosed(reason))
		return
	}
	if !id.AcceptJoins {
		httperror.Write(w, r, errJoinsClosed("this home is not accepting new machines"))
		return
	}

	var req JoinRequest
	// Lenient on unknown fields: newer nodes may send more.
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httperror.Write(w, r, httperror.BadRequest("invalid json: "+err.Error()))
		return
	}
	nodeID, ok := canonicalID(req.NodeID)
	if !ok {
		httperror.Write(w, r, httperror.BadRequest("node_id must be a uuid"))
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxNameLen {
		httperror.Write(w, r, httperror.BadRequest("name is required (at most 100 characters)"))
		return
	}
	nodePub, err := cluster.DecodeKey(req.Pubkey)
	if err != nil {
		httperror.Write(w, r, httperror.BadRequest("pubkey must be a base64 32-byte X25519 key"))
		return
	}
	for field, raw := range map[string]json.RawMessage{"versions": req.Versions, "capabilities": req.Capabilities, "storage": req.Storage} {
		if !isObjectOrAbsent(raw) {
			httperror.Write(w, r, httperror.BadRequest(field+" must be an object"))
			return
		}
	}

	if old, err := h.Store.Node(ctx, nodeID); err == nil && (old.State == cluster.StateApproved || old.State == cluster.StateRevoked) {
		httperror.Write(w, r, problem(http.StatusConflict, "already_member", "already a member",
			"this machine is already known to the home; an admin must remove it before it can ask again"))
		return
	} else if err == nil && old.State == cluster.StatePending && !expired(old, h.now()) {
		// a live request is only renewed by the machine that made it: anyone who
		// saw the node id (it is in the poll URL) must not swap in their own key
		stored, kerr := cluster.DecodeKey(old.NodePubkey)
		if kerr != nil || subtle.ConstantTimeCompare(stored, nodePub) != 1 {
			httperror.Write(w, r, problem(http.StatusConflict, "pending_elsewhere", "already asking",
				"a request for this machine is waiting with another key; it expires in 15 minutes"))
			return
		}
	} else if err != nil && !errors.Is(err, cluster.ErrNotFound) {
		errInternal(w, r, "read node", err)
		return
	}

	homePriv, homePub, err := cluster.GenerateKey()
	if err != nil {
		errInternal(w, r, "generate key", err)
		return
	}
	now := h.now()
	expires := now.Add(RequestTTL)
	hp, hpriv := cluster.EncodeKey(homePub), cluster.EncodeKey(homePriv)
	addr, apiURL := clientIP(r), requestBaseURL(r)
	n := cluster.Node{
		ID:           nodeID,
		Name:         name,
		OS:           optStr(req.OS),
		Arch:         optStr(req.Arch),
		Versions:     objOrEmpty(req.Versions),
		Capabilities: objOrEmpty(req.Capabilities),
		Storage:      objOrEmpty(req.Storage),
		NodePubkey:   cluster.EncodeKey(nodePub),
		HomePubkey:   &hp,
		HomePrivkey:  &hpriv,
		ExpiresAt:    &expires,
		RemoteAddr:   &addr,
		APIURL:       &apiURL,
		CreatedAt:    now,
	}
	if err := h.Store.PutPending(ctx, n); err != nil {
		if errors.Is(err, cluster.ErrAlreadyMember) {
			httperror.Write(w, r, problem(http.StatusConflict, "already_member", "already a member",
				"this machine is already known to the home"))
			return
		}
		errInternal(w, r, "store join request", err)
		return
	}
	if len(req.CSR) > 8<<10 {
		req.CSR = "" // not a certificate request; the machine asks on its channel instead
	}
	h.keepCSR(nodeID, strings.TrimSpace(req.CSR), expires)
	h.log().Info("cluster: join request", "node_id", nodeID, "remote", addr, "event", "cluster_join_request")
	common.WriteJSON(w, r, http.StatusAccepted, JoinResponse{
		RequestID:  nodeID,
		HomePubkey: hp,
		Home:       JoinHome{InstanceID: id.InstanceID, Name: id.Name},
		ExpiresAt:  expires,
	})
}

// PollResponse is the answer of GET /api/cluster/join/{node_id}.
type PollResponse struct {
	State  string            `json:"state"`
	Sealed *cluster.Envelope `json:"sealed,omitempty"`
}

// Poll implements GET /api/cluster/join/{node_id}?pubkey=. The pubkey
// must be the one the request was made with; anything else is a 404 so
// the endpoint reveals nothing about other machines.
func (h *Handler) Poll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	notFound := func() { httperror.Write(w, r, httperror.NotFound("join request")) }
	nodeID, ok := canonicalID(chi.URLParam(r, "node_id"))
	if !ok {
		notFound()
		return
	}
	// A '+' that was not percent-encoded arrives as a space.
	given, err := cluster.DecodeKey(strings.ReplaceAll(r.URL.Query().Get("pubkey"), " ", "+"))
	if err != nil {
		notFound()
		return
	}
	now := h.now()
	n, err := h.Store.Node(ctx, nodeID)
	if err == nil && n.State == cluster.StateApproved && n.Sealed != nil {
		// credentials still waiting: drop them if their machine already runs (only
		// while there are some, so a poll is not a write each time)
		if err = h.Store.ClearSealed(ctx, now.Add(-SealedTTL)); err == nil {
			n, err = h.Store.Node(ctx, nodeID)
		}
	}
	if errors.Is(err, cluster.ErrNotFound) {
		notFound()
		return
	} else if err != nil {
		errInternal(w, r, "read node", err)
		return
	}
	stored, err := cluster.DecodeKey(n.NodePubkey)
	if err != nil || subtle.ConstantTimeCompare(stored, given) != 1 {
		notFound()
		return
	}
	out := PollResponse{State: n.State}
	switch {
	case n.State == cluster.StatePending && expired(n, now):
		out.State = "expired"
	case n.State == cluster.StateApproved && n.Sealed != nil &&
		(n.ApprovedAt == nil || now.Sub(*n.ApprovedAt) < SealedTTL):
		// the housekeeping loop clears it after SealedTTL; until then, only its
		// own machine (the pubkey above) can fetch it
		var env cluster.Envelope
		if json.Unmarshal(n.Sealed, &env) == nil {
			out.Sealed = &env
		}
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

func expired(n cluster.Node, now time.Time) bool {
	return n.State == cluster.StatePending && n.ExpiresAt != nil && !now.Before(*n.ExpiresAt)
}

// ---------- admin ----------

// Home is the home card of GET /api/cluster (and the PUT settings answer).
type Home struct {
	InstanceID       string     `json:"instance_id"`
	Name             string     `json:"name"`
	AcceptJoins      bool       `json:"accept_joins"`
	AcceptJoinsUntil *time.Time `json:"accept_joins_until"`
	RolesSupported   bool       `json:"roles_supported"`
	RolesReason      *string    `json:"roles_reason"`
	// Story 31.3: the home's per-share storage check (as the pipeline on
	// the home reports it) and its "share -> local mount" override.
	Storage json.RawMessage `json:"storage,omitempty"`
	PathMap json.RawMessage `json:"path_map,omitempty"`
	// Canonicalize is the automatic conversion of the stored paths to
	// canonical share paths (state, pending admin request).
	Canonicalize *cluster.CanonStatus `json:"canonicalize,omitempty"`
	// Epic 35 story 35.11: the home's own web address and release, listed with the
	// machines' (WebURL: MAKTABA_WEB_URL, set by the supervisor).
	WebURL  *string `json:"web_url"`
	Version string  `json:"version"`
}

// Overview is GET /api/cluster.
type Overview struct {
	Home  Home   `json:"home"`
	Nodes []Node `json:"nodes"`
	// Link is the cluster link (story 35.16): its mode, the CA's fingerprint, each
	// machine's link and certificate.
	Link any `json:"link,omitempty"`
}

func (h *Handler) home(ctx context.Context, id cluster.Identity) Home {
	ok, reason := h.Roles.Supported(ctx)
	out := Home{
		InstanceID:       id.InstanceID,
		Name:             id.Name,
		AcceptJoins:      id.AcceptJoins,
		AcceptJoinsUntil: id.AcceptJoinsUntil,
		RolesSupported:   ok,
	}
	if !ok {
		out.RolesReason = &reason
	}
	return out
}

// Overview implements GET /api/cluster.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	now := h.now()
	id, err := h.identity(ctx)
	if err != nil {
		errInternal(w, r, "read identity", err)
		return
	}
	nodes, err := h.views(ctx, now)
	if err != nil {
		errInternal(w, r, "list machines", err)
		return
	}
	home := h.home(ctx, id)
	h.homeStorage(r, &home)
	if h.WebURL != "" {
		u := h.WebURL
		home.WebURL = &u
	}
	home.Version = h.Version
	out := Overview{Home: home, Nodes: nodes}
	if h.Link != nil {
		out.Link = h.Link(ctx)
	}
	common.WriteJSON(w, r, http.StatusOK, out)
}

// SettingsRequest is the body of PUT /api/cluster/settings.
type SettingsRequest struct {
	AcceptJoins *bool   `json:"accept_joins"`
	Name        *string `json:"name"`
}

// Settings implements PUT /api/cluster/settings. Turning joins on opens
// them for AcceptJoinsTTL.
func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var req SettingsRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	var name string
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 63 {
			httperror.Write(w, r, httperror.BadRequest("name must be 1 to 63 characters"))
			return
		}
	}
	if req.AcceptJoins != nil && *req.AcceptJoins {
		if ok, reason := h.Roles.Supported(ctx); !ok {
			httperror.Write(w, r, problem(http.StatusConflict, "roles_unsupported", "joins unavailable", reason))
			return
		}
	}
	id, err := h.identity(ctx)
	if err != nil {
		errInternal(w, r, "read identity", err)
		return
	}
	changed := false
	if req.Name != nil && name != id.Name {
		if id, err = h.Store.SetName(ctx, name); err != nil {
			errInternal(w, r, "rename", err)
			return
		}
		changed = true
	}
	if req.AcceptJoins != nil {
		var until *time.Time
		if *req.AcceptJoins {
			t := h.now().Add(AcceptJoinsTTL)
			until = &t
		}
		if id, err = h.Store.SetAcceptJoins(ctx, *req.AcceptJoins, until); err != nil {
			errInternal(w, r, "switch joins", err)
			return
		}
		changed = true
	}
	if changed {
		h.announce(ctx, id)
	}
	common.WriteJSON(w, r, http.StatusOK, h.home(ctx, id))
}

// node loads the {id} node or writes 404.
func (h *Handler) node(w http.ResponseWriter, r *http.Request) (cluster.Node, bool) {
	id, ok := canonicalID(chi.URLParam(r, "id"))
	if !ok {
		httperror.Write(w, r, httperror.NotFound("machine"))
		return cluster.Node{}, false
	}
	n, err := h.Store.Node(r.Context(), id)
	if errors.Is(err, cluster.ErrNotFound) {
		httperror.Write(w, r, httperror.NotFound("machine"))
		return cluster.Node{}, false
	} else if err != nil {
		errInternal(w, r, "read node", err)
		return cluster.Node{}, false
	}
	return n, true
}

func notPending(n cluster.Node) *httperror.Error {
	return problem(http.StatusConflict, "not_pending", "not pending", "the machine is "+n.State).With("state", n.State)
}

// ApproveRequest is the body of POST /api/cluster/nodes/{id}/approve.
type ApproveRequest struct {
	Code string `json:"code"`
	// Full: the admin allows the machine a copy of this database (a full machine) once it
	// reports it can be one; off by default. Never the machine's own choice (slot 0151).
	Full bool `json:"full"`
}

// Approve implements POST /api/cluster/nodes/{id}/approve.
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var req ApproveRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	n, ok := h.node(w, r)
	if !ok {
		return
	}
	now := h.now()
	if n.State != cluster.StatePending {
		httperror.Write(w, r, notPending(n))
		return
	}
	if expired(n, now) {
		httperror.Write(w, r, problem(http.StatusGone, "expired", "request expired",
			"the request expired; the machine must ask again"))
		return
	}
	if ok, reason := h.Roles.Supported(ctx); !ok {
		httperror.Write(w, r, problem(http.StatusConflict, "roles_unsupported", "joins unavailable", reason))
		return
	}
	shared, nodePub, homePub, err := sharedSecret(n)
	if err != nil {
		errInternal(w, r, "derive keys", err)
		return
	}
	want, err := cluster.SAS(shared, nodePub, homePub)
	if err != nil {
		errInternal(w, r, "derive code", err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(normalizeCode(req.Code)), []byte(want)) != 1 {
		attempts, err := h.Store.WrongCode(ctx, n.ID, MaxCodeAttempts, now)
		if err != nil {
			errInternal(w, r, "count attempt", err)
			return
		}
		left := MaxCodeAttempts - attempts
		if left < 0 {
			left = 0
		}
		e := problem(http.StatusBadRequest, "wrong_code", "wrong code",
			"the code does not match the one the machine shows").With("attempts_left", left)
		if left == 0 {
			e.Detail = "too many wrong codes: the request was rejected"
			e = e.With("state", cluster.StateRejected)
		}
		h.log().WarnContext(r.Context(), "cluster: wrong pairing code", "node_id", n.ID, "attempts", attempts, "event", "cluster_wrong_code")
		httperror.Write(w, r, e)
		return
	}

	sealed, role, err := h.provision(ctx, r, n, shared, nodePub, homePub)
	if err != nil {
		errInternal(w, r, "provision machine", err)
		return
	}
	if err := h.Store.Approve(ctx, n.ID, role, sealed, actor(r), now); err != nil {
		if derr := h.Roles.Drop(ctx, role); derr != nil {
			h.log().WarnContext(r.Context(), "cluster: dropping role after failed approval", "err", derr, "role", role)
		}
		errInternal(w, r, "approve", err)
		return
	}
	if req.Full {
		if err := h.Store.UpdateNode(ctx, n.ID, cluster.NodePatch{FullAllowed: &req.Full}, now); err != nil {
			errInternal(w, r, "allow a full machine", err)
			return
		}
	}
	h.log().Info("cluster: machine approved", "node_id", n.ID, "role", role, "full_allowed", req.Full,
		"by", actor(r), "event", "cluster_approved")
	h.writeNode(w, r, n.ID)
}

func normalizeCode(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func sharedSecret(n cluster.Node) (shared, nodePub, homePub []byte, err error) {
	if n.HomePrivkey == nil || n.HomePubkey == nil {
		return nil, nil, nil, errors.New("the request has no home key")
	}
	homePriv, err := cluster.DecodeKey(*n.HomePrivkey)
	if err != nil {
		return nil, nil, nil, err
	}
	if homePub, err = cluster.DecodeKey(*n.HomePubkey); err != nil {
		return nil, nil, nil, err
	}
	if nodePub, err = cluster.DecodeKey(n.NodePubkey); err != nil {
		return nil, nil, nil, err
	}
	shared, err = cluster.SharedSecret(homePriv, nodePub)
	return shared, nodePub, homePub, err
}

// provision creates the machine's role and seals its credentials.
func (h *Handler) provision(ctx context.Context, r *http.Request, n cluster.Node,
	shared, nodePub, homePub []byte) (json.RawMessage, string, error) {
	id, err := h.identity(ctx)
	if err != nil {
		return nil, "", err
	}
	dbName, err := h.Roles.DatabaseName(ctx)
	if err != nil {
		return nil, "", err
	}
	apiURL := requestBaseURL(r)
	if n.APIURL != nil && *n.APIURL != "" {
		apiURL = *n.APIURL
	}
	host := "localhost"
	if u, err := url.Parse(apiURL); err == nil && u.Hostname() != "" {
		host = u.Hostname()
	}
	if dbHost := h.machineHost(); dbHost != "" {
		host = dbHost // the database is on another machine than the home
	}
	password, err := cluster.NewPassword()
	if err != nil {
		return nil, "", err
	}
	role := cluster.RoleName(n.ID)
	dbCA, dbTLS := h.pgTLS()
	var linkTLS *cluster.PayloadTLS
	if csr := h.takeCSR(n.ID); csr != "" && h.JoinTLS != nil {
		if t, err := h.JoinTLS(ctx, n.ID, csr); err == nil {
			linkTLS = t
		} else {
			h.log().WarnContext(ctx, "cluster: the joining machine's certificate request was refused; it asks again on its channel",
				"node_id", n.ID, "err", err, "event", "cluster_join_csr_refused")
		}
	}
	payload, err := cluster.MarshalPayload(cluster.Payload{
		DatabaseCA:  dbCA,
		DatabaseTLS: dbTLS,
		DatabaseURL: cluster.DatabaseURL(role, password, host, h.machinePort(ctx), dbName),
		Home:        cluster.PayloadHome{APIURL: apiURL, ClusterPub: h.ClusterPub, InstanceID: id.InstanceID, Name: id.Name},
		NodeID:      n.ID,
		TLS:         linkTLS,
		V:           cluster.PayloadVersion,
	})
	if err != nil {
		return nil, "", err
	}
	key, err := cluster.SealKey(shared, nodePub, homePub)
	if err != nil {
		return nil, "", err
	}
	env, err := cluster.Seal(key, n.ID, payload)
	if err != nil {
		return nil, "", err
	}
	sealed, err := json.Marshal(env)
	if err != nil {
		return nil, "", err
	}
	if err := h.Roles.Create(ctx, role, password); err != nil {
		return nil, "", err
	}
	return sealed, role, nil
}

// Reject implements POST /api/cluster/nodes/{id}/reject.
func (h *Handler) Reject(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	n, ok := h.node(w, r)
	if !ok {
		return
	}
	if n.State != cluster.StatePending {
		httperror.Write(w, r, notPending(n))
		return
	}
	if err := h.Store.Reject(r.Context(), n.ID, h.now()); err != nil {
		errInternal(w, r, "reject", err)
		return
	}
	h.writeNode(w, r, n.ID)
}

// Revoke implements POST /api/cluster/nodes/{id}/revoke: the machine's
// role stops logging in, its password changes and its sessions end (when
// the home's user may end them; otherwise the machine stops at its next
// heartbeat). Its claimed jobs go back to the queue through the reaper
// once their heartbeats stop.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	n, ok := h.node(w, r)
	if !ok {
		return
	}
	if n.State != cluster.StateApproved && n.State != cluster.StateRevoked {
		httperror.Write(w, r, problem(http.StatusConflict, "not_approved", "not approved",
			"only an approved machine can be revoked").With("state", n.State))
		return
	}
	// the state first: a copy's grant reads it under a lock (slot 0147), so one under way
	// ends before and one after is refused; then its roles, which no grant re-enables now
	// (a revoked one again: its roles, if disabling them failed the first time)
	if n.State == cluster.StateApproved {
		if err := h.Store.Revoke(ctx, n.ID, h.now()); err != nil {
			errInternal(w, r, "revoke", err)
			return
		}
	}
	if n.DBRole != nil && *n.DBRole != "" {
		ended, err := h.Roles.Disable(ctx, *n.DBRole)
		if err != nil {
			errInternal(w, r, "disable role", err)
			return
		}
		h.log().Info("cluster: machine revoked", "node_id", n.ID, "sessions_ended", ended, "event", "cluster_revoked")
	}
	if h.OnRevoke != nil {
		h.OnRevoke(n.ID)
	}
	h.writeNode(w, r, n.ID)
}

// UpdateRequest is the body of PUT /api/cluster/nodes/{id}. path_map:
// an object sets an admin map, null hands it back to the machine's
// inference; stages: true / false switch a stage, null makes it automatic.
type UpdateRequest struct {
	Name    *string          `json:"name"`
	Drain   *bool            `json:"drain"`
	PathMap json.RawMessage  `json:"path_map"`
	Stages  map[string]*bool `json:"stages"`
	// FullAllowed: an admin allows (true) or takes back (false) the machine's copy of this
	// database; it takes effect with what the machine reports (full_node, slot 0151).
	FullAllowed *bool `json:"full_allowed"`
}

// UpdateNode implements PUT /api/cluster/nodes/{id}.
func (h *Handler) UpdateNode(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	var req UpdateRequest
	if e := common.ReadJSON(r, &req, 0); e != nil {
		httperror.Write(w, r, e)
		return
	}
	n, ok := h.node(w, r)
	if !ok {
		return
	}
	patch := cluster.NodePatch{Drain: req.Drain, FullAllowed: req.FullAllowed}
	if req.FullAllowed != nil && n.State != cluster.StateApproved {
		httperror.Write(w, r, httperror.BadRequest("only an approved machine can be allowed a copy"))
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > maxNameLen {
			httperror.Write(w, r, httperror.BadRequest("name must be 1 to 100 characters"))
			return
		}
		patch.Name = &name
	}
	switch {
	case req.PathMap == nil:
	case string(req.PathMap) == "null":
		patch.ClearPathMap = true
	case isObject(req.PathMap):
		patch.PathMap = req.PathMap
	default:
		httperror.Write(w, r, httperror.BadRequest("path_map must be an object or null"))
		return
	}
	if len(req.Stages) > 0 {
		if err := h.Store.SetStages(ctx, n.ID, req.Stages); err != nil {
			if errors.Is(err, cluster.ErrUnknownStage) {
				httperror.Write(w, r, problem(http.StatusBadRequest, "unknown_stage", "unknown stage", err.Error()))
				return
			}
			errInternal(w, r, "switch stages", err)
			return
		}
	}
	if err := h.Store.UpdateNode(ctx, n.ID, patch, h.now()); err != nil {
		errInternal(w, r, "update machine", err)
		return
	}
	if req.FullAllowed != nil {
		h.log().Info("cluster: a machine's copy allowed or taken back", "node_id", n.ID,
			"full_allowed", *req.FullAllowed, "by", actor(r), "event", "cluster_full_allowed")
	}
	if h.OnNodeChanged != nil && (req.Drain != nil || len(req.Stages) > 0 || req.FullAllowed != nil) {
		h.OnNodeChanged(n.ID)
	}
	h.writeNode(w, r, n.ID)
}

// DeleteNode implements DELETE /api/cluster/nodes/{id}: a rejected or
// revoked machine (or an expired request) is removed, with its role.
func (h *Handler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	n, ok := h.node(w, r)
	if !ok {
		return
	}
	if n.State != cluster.StateRejected && n.State != cluster.StateRevoked && !expired(n, h.now()) {
		httperror.Write(w, r, problem(http.StatusConflict, "not_removable", "not removable",
			"only a rejected or revoked machine can be removed").With("state", n.State))
		return
	}
	if err := h.Store.DeleteNode(ctx, n.ID); err != nil {
		errInternal(w, r, "remove machine", err)
		return
	}
	if h.OnRevoke != nil {
		h.OnRevoke(n.ID)
	}
	if n.DBRole != nil && *n.DBRole != "" {
		if err := h.Roles.Drop(ctx, *n.DBRole); err != nil {
			h.log().WarnContext(r.Context(), "cluster: dropping a removed machine's role failed", "role", *n.DBRole, "err", err)
		}
	}
	common.WriteNoContent(w)
}

// ---------- request helpers ----------

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestBaseURL is the API URL the caller used: scheme + Host header.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p == "https" || p == "http" {
		scheme = p
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	return scheme + "://" + host
}

func optStr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func isObject(raw json.RawMessage) bool {
	var m map[string]any
	return json.Unmarshal(raw, &m) == nil && m != nil
}

func isObjectOrAbsent(raw json.RawMessage) bool {
	return raw == nil || string(raw) == "null" || isObject(raw)
}

func objOrEmpty(raw json.RawMessage) json.RawMessage {
	if raw == nil || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}
