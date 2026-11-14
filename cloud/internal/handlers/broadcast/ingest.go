// The ingest-token route serves the home's side of the broadcast edge's control plane (Epic 50 story
// 50.21 §2):
//
//	POST /v1/broadcast/ingest-token   a home asks for its channel's ingest token
//
// The home's agent authenticates as on every agent route: basic auth with its server id and
// secret, never a user token. The token is Ed25519-signed (internal/broadcast), bound to the
// publisher (the home's owner), the channel, the rungs and the plan's top bitrate, and valid 24 h;
// the home asks again every hour. What a plan allows comes from a Plans source: until the
// Broadcaster tier lands (story 50.20) the default is Pro's guest sharing.

package broadcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
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

// Plans answers what a home may publish (story 50.20 replaces the default with the
// Broadcaster tier's catalog, the agreement check and the channel count).
type Plans interface {
	ForServer(ctx context.Context, serverID string) (Allowance, error)
}

// IngestDeps wires the handler. A nil Key turns the route off (503 broadcast_disabled).
type IngestDeps struct {
	Agents    Agents
	Plans     Plans
	Key       *bc.Key
	IngestURL string
	Now       func() time.Time
	// GeoDeny is Maktaba's broadcast country deny list (story 50.23 §5), merged into every
	// token; nil: none. A failure to read it refuses the token (never a token without it).
	GeoDeny func(ctx context.Context) ([]string, error)
}

// DBGeoDeny reads broadcast_geo_deny.
func DBGeoDeny(db *sql.DB) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		rows, err := db.QueryContext(ctx, `SELECT country FROM broadcast_geo_deny ORDER BY country`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return out, rows.Err()
	}
}

// MountIngest registers the home agent's broadcast routes, outside the user auth group.
func MountIngest(r chi.Router, d *IngestDeps) {
	r.Post("/v1/broadcast/ingest-token", d.IngestToken)
}

type tokenReq struct {
	// Channel is the home's own channel id; the edge sees an opaque id derived from it.
	Channel string   `json:"channel"`
	Rungs   []string `json:"rungs"`
	// Geo is the channel's own country rule (story 50.23 §5): the publisher's restriction on
	// their own channel.
	Geo *struct {
		Mode      string   `json:"mode"`
		Countries []string `json:"countries"`
	} `json:"geo"`
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
	var deny []string
	if d.GeoDeny != nil {
		if deny, err = d.GeoDeny(r.Context()); err != nil {
			writeErr(w, 500, "geo_lookup_failed", "")
			return
		}
	}
	mode, countries := "", []string(nil)
	if req.Geo != nil {
		if len(req.Geo.Countries) > 250 || (req.Geo.Mode != "" && req.Geo.Mode != "allow" && req.Geo.Mode != "deny") {
			writeErr(w, 400, "invalid_geo", "")
			return
		}
		mode, countries = req.Geo.Mode, req.Geo.Countries
	}
	now := d.now()
	ch := bc.ChannelID(serverID, req.Channel)
	tok, err := bc.IssueIngest(*d.Key, bc.IngestClaims{
		Pub: allow.Publisher, Ch: ch, Rungs: req.Rungs, Home: serverID, Kbps: allow.MaxTopKbps,
		Geo: bc.MergeGeo(mode, countries, deny),
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

// GuestSharing is Pro's guest sharing (README §14.4, 50.20 §1): 2 rungs, up to 3,000 kbps.
var GuestSharing = Allowance{MaxRungs: 2, MaxTopKbps: 3000}

// DBPlans is the default Plans: an active Pro owner gets guest sharing; any other plan none.
// Story 50.20 replaces it with the Broadcaster tier (servers.broadcast_plan) and its catalog.
type DBPlans struct{ DB *sql.DB }

func (p DBPlans) ForServer(ctx context.Context, serverID string) (Allowance, error) {
	var userID, plan, status string
	err := p.DB.QueryRowContext(ctx, `
        SELECT u.id::text, u.plan, u.status
          FROM servers s JOIN users u ON u.id = s.owner_user_id
         WHERE s.id::text = $1`, serverID).Scan(&userID, &plan, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return Allowance{}, ErrUnknownServer
	}
	if err != nil {
		return Allowance{}, err
	}
	return AllowanceFor(userID, plan, status)
}

// AllowanceFor maps an owner's plan and account status to what the home may publish.
func AllowanceFor(userID, plan, status string) (Allowance, error) {
	if status != "active" || plan != billing.PlanPro {
		return Allowance{}, ErrNotInPlan
	}
	a := GuestSharing
	a.Publisher = userID
	return a, nil
}
