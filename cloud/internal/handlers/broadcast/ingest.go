// The home's side of the broadcast edge's control plane (Epic 50 story 50.21 §2):
//
//	POST /v1/broadcast/ingest-token   a home asks for its channel's ingest token
//
// The home's agent authenticates as on every agent route: basic auth with its server id and
// secret, never a user token. The token is Ed25519-signed (internal/broadcast), bound to the
// publisher (the home's owner), the channel, the rungs and the plan's top bitrate, and valid 24 h;
// the home asks again every hour. What a plan allows is story 50.20's tier (StorePlans).

package broadcast

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

// Agents checks a home agent's credential (stores.Servers).
type Agents interface {
	VerifySecret(ctx context.Context, serverID, secret string) error
}

// Allowance is what a home's owner's plan lets it publish.
type Allowance struct {
	// Publisher is the cloud account that publishes (the home's owner).
	Publisher  string
	MaxRungs   int
	MaxTopKbps int
}

var (
	// ErrNotInPlan is a home whose owner's plan does not include broadcasting.
	ErrNotInPlan = errors.New("broadcast: not in plan")
	// ErrUnknownServer is a home the cloud does not know.
	ErrUnknownServer = errors.New("broadcast: unknown server")
)

// Plans answers what a home may publish.
type Plans interface {
	ForServer(ctx context.Context, serverID string) (Allowance, error)
}

// IngestDeps wires the ingest-token route. A nil Key turns it off (503 broadcast_disabled).
type IngestDeps struct {
	Agents    Agents
	Plans     Plans
	Key       *bc.Key
	IngestURL string
	Now       func() time.Time
}

// MountIngest registers the home agent's ingest-token route, outside the user auth group.
func MountIngest(r chi.Router, d *IngestDeps) {
	r.Post("/v1/broadcast/ingest-token", d.IngestToken)
}

type tokenReq struct {
	// Channel is the home's own channel id; the edge sees an opaque id derived from it.
	Channel string   `json:"channel"`
	Rungs   []string `json:"rungs"`
}

type tokenResp struct {
	Token        string    `json:"token"`
	Channel      string    `json:"channel"`
	ExpiresAt    time.Time `json:"expires_at"`
	RefreshAfter int       `json:"refresh_after_seconds"`
	IngestURL    string    `json:"ingest_url"`
	Rungs        []string  `json:"rungs"`
	MaxTopKbps   int       `json:"max_top_kbps"`
}

var localChannelRE = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

func (d *IngestDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// IngestToken issues a home's ingest token for one channel.
func (d *IngestDeps) IngestToken(w http.ResponseWriter, r *http.Request) {
	if d.Key == nil || d.Plans == nil {
		writeErr(w, 503, "broadcast_disabled", "")
		return
	}
	serverID, secret, ok := r.BasicAuth()
	if !ok || serverID == "" || d.Agents.VerifySecret(r.Context(), serverID, secret) != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="maktaba-server"`)
		writeErr(w, 401, "unauthorized", "")
		return
	}
	var req tokenReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || !localChannelRE.MatchString(req.Channel) {
		writeErr(w, 400, "invalid_body", "")
		return
	}
	allow, err := d.Plans.ForServer(r.Context(), serverID)
	switch {
	case errors.Is(err, ErrNotInPlan):
		writeErr(w, 403, "broadcast_not_in_plan", "")
		return
	case errors.Is(err, ErrUnknownServer):
		writeErr(w, 404, "not_found", "")
		return
	case err != nil:
		writeErr(w, 500, "plan_lookup_failed", "")
		return
	}
	if len(req.Rungs) == 0 || len(req.Rungs) > allow.MaxRungs {
		writeErr(w, 403, "plan_rungs", "")
		return
	}
	now := d.now()
	ch := bc.ChannelID(serverID, req.Channel)
	tok, err := bc.IssueIngest(*d.Key, bc.IngestClaims{
		Pub: allow.Publisher, Ch: ch, Rungs: req.Rungs, Home: serverID, Kbps: allow.MaxTopKbps,
	}, now)
	if errors.Is(err, bc.ErrBadClaims) {
		writeErr(w, 400, "invalid_rungs", "")
		return
	}
	if err != nil {
		writeErr(w, 500, "sign_failed", "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, tokenResp{
		Token: tok, Channel: ch, ExpiresAt: now.Add(bc.IngestTTL).UTC(),
		RefreshAfter: int(bc.RefreshEvery / time.Second), IngestURL: d.IngestURL,
		Rungs: req.Rungs, MaxTopKbps: allow.MaxTopKbps,
	})
}

// StorePlans is the Plans of story 50.20's BroadcastStore: the publisher's tier, behind the
// broadcasting gate (a test publisher passes it while it is shut).
type StorePlans struct{ Store *billingpkg.BroadcastStore }

func (p StorePlans) ForServer(ctx context.Context, serverID string) (Allowance, error) {
	pub, err := p.Store.Publisher(ctx, serverID)
	if errors.Is(err, billingpkg.ErrUnknownPublisher) {
		return Allowance{}, ErrUnknownServer
	}
	if err != nil {
		return Allowance{}, err
	}
	return allowanceOf(pub, p.Store.Gated(pub))
}

// allowanceOf maps a publisher and the gate to what its home may publish.
func allowanceOf(p billingpkg.Publisher, gated bool) (Allowance, error) {
	t := p.Tier()
	if t == nil || !gated {
		return Allowance{}, ErrNotInPlan
	}
	return Allowance{Publisher: p.OwnerID, MaxRungs: t.MaxRungs, MaxTopKbps: t.MaxTopKbps}, nil
}
