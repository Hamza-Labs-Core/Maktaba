// Viewer access (Epic 50 story 50.26): the publisher's invites and its channels' access.
//
// Two doors lead to the same actions, each with its own role rule:
//
//	the home (basic auth server_id:secret; the home's Broadcast page lets only its owner or an
//	admin call it, api/internal/broadcastaccess):
//	  GET    /v1/broadcast/access                       channels, limits, verification
//	  PUT    /v1/broadcast/channels/{id}/access         {access, max_viewers, indexable}
//	  PUT    /v1/broadcast/viewer-limits                {max_sessions_per_ip, daily_minutes}
//	  GET    /v1/broadcast/invites                      the invites and their uses
//	  POST   /v1/broadcast/invites                      {channels, label, max_uses, expires_in_days}
//	  DELETE /v1/broadcast/invites/{id}                 revoke one
//	  GET    /v1/broadcast/invites/{id}/devices         its guest passes (the edge holds them)
//	  DELETE /v1/broadcast/invites/{id}/devices/{pass}  revoke one device's pass
//
//	the account (bearer token): the same under /v1/broadcast/homes/{server}/…, for the home's
//	owner only; anyone else gets 404, as for a home that does not exist.
//
// A service admin may list a home's invites and revoke one (handlers/admin), never create one or
// open a channel. Every change bumps servers.broadcast_access_version and pushes the publisher's
// access state to the edge (a signed control post); a push that fails is retried by PushPending.
package broadcast

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lib/pq"

	billingpkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
)

// MaxLiveInvites bounds a publisher's live invites (the edge holds their codes).
const MaxLiveInvites = 200

// VerifyDays is how long after the first publish a publisher may open a channel (README §14.6).
const VerifyDays = 7

// actor names who made a change (the invite's created_by and revoked_by).
type actor struct {
	server string
	// user is the account for the cloud web app's door; "" for the home's.
	user string
}

func (a actor) String() string {
	if a.user != "" {
		return "owner:" + a.user
	}
	return "home"
}

type scopedFunc func(w http.ResponseWriter, r *http.Request, a actor)

// asHome admits a home by its id and secret.
func (d *Deps) asHome(h scopedFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := d.agent(w, r)
		if !ok {
			return
		}
		h(w, r, actor{server: id})
	}
}

// asOwner admits the signed-in account only for a home it owns: anyone else gets 404.
func (d *Deps) asOwner(h scopedFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.GetUserID(r.Context())
		server := chi.URLParam(r, "server")
		if uid == "" || !billingpkg.IsUUID(server) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		var one int
		err := d.DB.QueryRowContext(r.Context(), `SELECT 1 FROM servers WHERE id = $1 AND owner_user_id = $2`, server, uid).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "not_found", "")
			return
		}
		if err != nil {
			d.fail(w, r, "load home", err)
			return
		}
		h(w, r, actor{server: server, user: uid})
	}
}

// MountAccessAgent registers the home's access routes (outside the user auth group).
func MountAccessAgent(r chi.Router, d *Deps) {
	d.mountAccess(r, "/v1/broadcast", d.asHome)
}

// MountAccessUser registers the account's access routes (inside the bearer-token group).
func MountAccessUser(r chi.Router, d *Deps) {
	d.mountAccess(r, "/v1/broadcast/homes/{server}", d.asOwner)
}

func (d *Deps) mountAccess(r chi.Router, base string, as func(scopedFunc) http.HandlerFunc) {
	r.Get(base+"/access", as(d.GetAccess))
	r.Put(base+"/channels/{id}/access", as(d.SetChannelAccess))
	r.Put(base+"/viewer-limits", as(d.SetViewerLimits))
	r.Get(base+"/invites", as(d.ListInvites))
	r.Post(base+"/invites", as(d.CreateInvite))
	r.Delete(base+"/invites/{id}", as(d.RevokeInvite))
	r.Get(base+"/invites/{id}/devices", as(d.ListDevices))
	r.Delete(base+"/invites/{id}/devices/{pass}", as(d.RevokeDevice))
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// ---- verification and the access view ---------------------------------------------------

// Verification is README §14.6's verified publisher, item by item.
type Verification struct {
	Verified  bool `json:"verified"`
	PaidPlan  bool `json:"paid_plan"`
	Email     bool `json:"email_confirmed"`
	DaysSince int  `json:"days_since_first_publish"`
	// FirstPublish is nil before the first publish.
	FirstPublish *time.Time `json:"first_publish,omitempty"`
}

func (d *Deps) verification(ctx context.Context, p billingpkg.Publisher) (Verification, error) {
	var v Verification
	var email bool
	var first sql.NullTime
	err := d.DB.QueryRowContext(ctx, `
        SELECT u.email_verified, (SELECT min(published_at) FROM broadcast_channels WHERE server_id = $1)
          FROM servers s JOIN users u ON u.id = s.owner_user_id WHERE s.id = $1`, p.ServerID).Scan(&email, &first)
	if err != nil {
		return v, err
	}
	v.PaidPlan = p.OwnerActive && p.Plan != "" && p.Plan != billingpkg.PlanFree
	v.Email = email
	if first.Valid {
		t := first.Time.UTC()
		v.FirstPublish = &t
		v.DaysSince = int(d.now().Sub(t) / (24 * time.Hour))
	}
	v.Verified = v.PaidPlan && v.Email && first.Valid && v.DaysSince >= VerifyDays
	return v, nil
}

// openRefusal says why a publisher may not set a channel to open ("" when it may).
func (d *Deps) openRefusal(ctx context.Context, p billingpkg.Publisher) (code string, status int, err error) {
	t := p.Tier()
	if t == nil || !t.AllowsAccess(billingpkg.AccessOpen) {
		return "broadcast_limit", http.StatusPaymentRequired, nil
	}
	if !d.OpenAccess {
		return "broadcast_open_not_enabled", http.StatusForbidden, nil
	}
	v, err := d.verification(ctx, p)
	if err != nil {
		return "", 0, err
	}
	if !v.Verified {
		return "broadcast_open_needs_verification", http.StatusForbidden, nil
	}
	return "", 0, nil
}

func writeOpenRefusal(w http.ResponseWriter, code string, status int) {
	switch code {
	case "broadcast_limit":
		limitErr(w, "access", 0)
	case "broadcast_open_needs_verification":
		writeJSON(w, status, map[string]string{"error": code, "key": "broadcast.openNeedsVerification",
			"message": "open channels are available once the account is verified: a paid plan, a confirmed email, and 7 days since the first broadcast"})
	default:
		writeJSON(w, status, map[string]string{"error": code, "key": "broadcast.openNotEnabled",
			"message": "open channels aren't available yet"})
	}
}

type channelView struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	EdgeID     string `json:"edge_id"`
	Access     string `json:"access"`
	MaxViewers int    `json:"max_viewers"`
	Indexable  bool   `json:"indexable"`
}

type accessView struct {
	Channels          []channelView `json:"channels"`
	MaxSessionsPerIP  int           `json:"max_sessions_per_ip"`
	DailyMinutes      int           `json:"daily_minutes"`
	MaxViewers        int           `json:"max_viewers"`
	OpenAccessEnabled bool          `json:"open_access_enabled"`
	OpenInPlan        bool          `json:"open_in_plan"`
	Verification      Verification  `json:"verification"`
	// ViewersPay is always false: viewers never pay (README §14.7).
	ViewersPay bool `json:"viewers_pay"`
}

func (d *Deps) channels(ctx context.Context, q queryer, server string) ([]channelView, error) {
	rows, err := q.QueryContext(ctx, `
        SELECT id::text, slug, access, max_viewers, indexable FROM broadcast_channels
         WHERE server_id = $1 AND unpublished_at IS NULL ORDER BY published_at`, server)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []channelView{}
	for rows.Next() {
		var c channelView
		if err := rows.Scan(&c.ID, &c.Slug, &c.Access, &c.MaxViewers, &c.Indexable); err != nil {
			return nil, err
		}
		c.EdgeID = bc.ChannelID(server, c.Slug)
		out = append(out, c)
	}
	return out, rows.Err()
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// GetAccess is the publisher's access page: channels, limits and what open needs.
func (d *Deps) GetAccess(w http.ResponseWriter, r *http.Request, a actor) {
	ctx := r.Context()
	p, err := d.Store.Publisher(ctx, a.server)
	if err != nil {
		d.fail(w, r, "load publisher", err)
		return
	}
	v, err := d.verification(ctx, p)
	if err != nil {
		d.fail(w, r, "verification", err)
		return
	}
	chs, err := d.channels(ctx, d.DB, a.server)
	if err != nil {
		d.fail(w, r, "channels", err)
		return
	}
	out := accessView{Channels: chs, MaxViewers: p.Limits().MaxConcurrentViewers, OpenAccessEnabled: d.OpenAccess, Verification: v}
	if t := p.Tier(); t != nil {
		out.OpenInPlan = t.AllowsAccess(billingpkg.AccessOpen)
	}
	if err := d.DB.QueryRowContext(ctx, `SELECT broadcast_max_sessions_per_ip, broadcast_daily_minutes FROM servers WHERE id = $1`, a.server).
		Scan(&out.MaxSessionsPerIP, &out.DailyMinutes); err != nil {
		d.fail(w, r, "limits", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type channelAccessReq struct {
	Access     string `json:"access"`
	MaxViewers *int   `json:"max_viewers"`
	Indexable  *bool  `json:"indexable"`
}

// SetChannelAccess changes one channel's access, cap and indexing. Open is refused unless the
// plan allows it, broadcast.open_access.enabled is on and the publisher is verified.
func (d *Deps) SetChannelAccess(w http.ResponseWriter, r *http.Request, a actor) {
	ctx := r.Context()
	ch := chi.URLParam(r, "id")
	var req channelAccessReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil ||
		(req.Access != "" && req.Access != billingpkg.AccessInvite && req.Access != billingpkg.AccessOpen) {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {access: invite|open, max_viewers, indexable}")
		return
	}
	if !billingpkg.IsUUID(ch) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	p, err := d.Store.Publisher(ctx, a.server)
	if err != nil {
		d.fail(w, r, "load publisher", err)
		return
	}
	if req.Access == billingpkg.AccessOpen {
		code, status, err := d.openRefusal(ctx, p)
		if err != nil {
			d.fail(w, r, "open check", err)
			return
		}
		if code != "" {
			writeOpenRefusal(w, code, status)
			return
		}
	}
	if req.MaxViewers != nil && (*req.MaxViewers < 0 || *req.MaxViewers > p.Limits().MaxConcurrentViewers) {
		limitErr(w, "max_viewers", p.Limits().MaxConcurrentViewers)
		return
	}
	d.change(w, r, a.server, func(tx *sql.Tx) (int, error) {
		res, err := tx.ExecContext(ctx, `
            UPDATE broadcast_channels SET access = COALESCE(NULLIF($3, ''), access),
                   max_viewers = COALESCE($4, max_viewers), indexable = COALESCE($5, indexable)
             WHERE id = $1 AND server_id = $2 AND unpublished_at IS NULL`, ch, a.server, req.Access, req.MaxViewers, req.Indexable)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return http.StatusNotFound, nil
		}
		return http.StatusNoContent, nil
	})
}

type limitsReq struct {
	MaxSessionsPerIP *int `json:"max_sessions_per_ip"`
	DailyMinutes     *int `json:"daily_minutes"`
}

// SetViewerLimits changes the publisher's open sessions per network and daily minutes.
func (d *Deps) SetViewerLimits(w http.ResponseWriter, r *http.Request, a actor) {
	var req limitsReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req); err != nil ||
		(req.MaxSessionsPerIP != nil && (*req.MaxSessionsPerIP < 1 || *req.MaxSessionsPerIP > 50)) ||
		(req.DailyMinutes != nil && (*req.DailyMinutes < 0 || *req.DailyMinutes > 1440)) {
		writeErr(w, http.StatusBadRequest, "bad_request", "max_sessions_per_ip is 1 to 50; daily_minutes is 0 (none) to 1440")
		return
	}
	d.change(w, r, a.server, func(tx *sql.Tx) (int, error) {
		_, err := tx.ExecContext(r.Context(), `
            UPDATE servers SET broadcast_max_sessions_per_ip = COALESCE($2, broadcast_max_sessions_per_ip),
                   broadcast_daily_minutes = COALESCE($3, broadcast_daily_minutes) WHERE id = $1`,
			a.server, req.MaxSessionsPerIP, req.DailyMinutes)
		return http.StatusNoContent, err
	})
}

// change runs one access change in a transaction that also bumps the access version, then
// pushes the new state to the edge. fn answers the status to write (non-2xx: rolled back).
func (d *Deps) change(w http.ResponseWriter, r *http.Request, server string, fn func(tx *sql.Tx) (int, error)) {
	ctx := r.Context()
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		d.fail(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	status, err := fn(tx)
	if err != nil {
		d.fail(w, r, "change", err)
		return
	}
	if status >= 300 {
		writeErr(w, status, "not_found", "")
		return
	}
	if err := BumpAccess(ctx, tx, server); err != nil {
		d.fail(w, r, "bump", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "commit", err)
		return
	}
	d.pushOne(ctx, server)
	w.WriteHeader(status)
}

// BumpAccess marks a publisher's access state changed (the edge needs the new version).
func BumpAccess(ctx context.Context, q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, server string) error {
	_, err := q.ExecContext(ctx, `UPDATE servers SET broadcast_access_version = broadcast_access_version + 1 WHERE id = $1`, server)
	return err
}

// ---- invites ------------------------------------------------------------------------------

type createInviteReq struct {
	Channels      []string `json:"channels"`
	Label         string   `json:"label"`
	MaxUses       int      `json:"max_uses"`
	ExpiresInDays int      `json:"expires_in_days"`
}

// InviteView is one invite as the publisher sees it: labels and use counts, never identities.
type InviteView struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	ShortCode string    `json:"short_code"`
	Label     string    `json:"label"`
	Channels  []string  `json:"channels"`
	MaxUses   int       `json:"max_uses"`
	Uses      *int      `json:"uses"` // nil: the edge didn't answer
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    string    `json:"status"` // active | expired | revoked
}

// CreateInvite signs a new invite for one or all of the publisher's channels.
func (d *Deps) CreateInvite(w http.ResponseWriter, r *http.Request, a actor) {
	ctx := r.Context()
	if d.Key == nil {
		writeErr(w, http.StatusServiceUnavailable, "broadcast_disabled", "")
		return
	}
	var req createInviteReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "body must be {channels, label, max_uses, expires_in_days}")
		return
	}
	if req.MaxUses == 0 {
		req.MaxUses = bc.DefaultInviteUses
	}
	if req.ExpiresInDays == 0 {
		req.ExpiresInDays = int(bc.DefaultInviteTTL / (24 * time.Hour))
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.MaxUses < 1 || req.MaxUses > bc.MaxInviteUses || req.ExpiresInDays < 1 || req.ExpiresInDays > 365 ||
		len([]rune(req.Label)) > bc.MaxLabelLen || len(req.Channels) > bc.MaxInviteChannels {
		writeErr(w, http.StatusBadRequest, "bad_request", "max_uses is 1 to 500, expires_in_days 1 to 365, the label at most 80 characters")
		return
	}
	for _, c := range req.Channels {
		if !billingpkg.IsUUID(c) {
			writeErr(w, http.StatusBadRequest, "bad_channel", "")
			return
		}
	}
	p, err := d.Store.Publisher(ctx, a.server)
	if err != nil {
		d.fail(w, r, "load publisher", err)
		return
	}
	if p.Tier() == nil {
		writeErr(w, http.StatusPaymentRequired, "broadcast_needs_plan", "sharing channels with people outside your home needs the Pro plan")
		return
	}
	if !d.Store.Gated(p) {
		writeErr(w, http.StatusForbidden, "broadcast_not_enabled", "broadcasting isn't open yet")
		return
	}
	tx, err := d.DB.BeginTx(ctx, nil)
	if err != nil {
		d.fail(w, r, "begin", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	// the home's row lock: two creates at once can't both pass the live-invite count
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM servers WHERE id = $1 FOR UPDATE`, a.server).Scan(&live); err != nil {
		d.fail(w, r, "lock home", err)
		return
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM broadcast_invites WHERE server_id = $1 AND revoked_at IS NULL AND expires_at > now()`, a.server).Scan(&live); err != nil {
		d.fail(w, r, "count invites", err)
		return
	}
	if live >= MaxLiveInvites {
		limitErr(w, "max_invites", MaxLiveInvites)
		return
	}
	// every channel named must be one of this publisher's live channels: an invite opens only
	// its publisher's channels
	edge := []string{}
	if len(req.Channels) > 0 {
		rows, err := tx.QueryContext(ctx, `SELECT slug FROM broadcast_channels WHERE server_id = $1 AND id = ANY($2::uuid[]) AND unpublished_at IS NULL`,
			a.server, pq.Array(req.Channels))
		if err != nil {
			d.fail(w, r, "channels", err)
			return
		}
		for rows.Next() {
			var slug string
			if err := rows.Scan(&slug); err != nil {
				_ = rows.Close()
				d.fail(w, r, "channels", err)
				return
			}
			edge = append(edge, bc.ChannelID(a.server, slug))
		}
		_ = rows.Close()
		if len(edge) != len(dedupe(req.Channels)) {
			writeErr(w, http.StatusBadRequest, "bad_channel", "an invite names only this home's published channels")
			return
		}
	}
	now := d.now()
	exp := now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour).Truncate(time.Second)
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		d.fail(w, r, "id", err)
		return
	}
	token, err := bc.IssueInvite(*d.Key, bc.InviteClaims{Pub: a.server, Chs: edge, Inv: id, Max: req.MaxUses, Exp: exp.Unix()}, now)
	if err != nil {
		d.fail(w, r, "sign invite", err)
		return
	}
	var createdBy any
	if a.user != "" {
		createdBy = a.user
	}
	var code string
	for try := 0; ; try++ {
		if code, err = bc.NewShortCode(); err != nil {
			d.fail(w, r, "code", err)
			return
		}
		// a savepoint, so a code collision doesn't abort the transaction
		if _, err := tx.ExecContext(ctx, `SAVEPOINT code`); err != nil {
			d.fail(w, r, "savepoint", err)
			return
		}
		_, err = tx.ExecContext(ctx, `
            INSERT INTO broadcast_invites (id, server_id, channel_ids, label, max_uses, short_code, token, created_by, expires_at)
            VALUES ($1, $2, $3::uuid[], $4, $5, $6, $7, $8, $9)`,
			id, a.server, pq.Array(dedupe(req.Channels)), req.Label, req.MaxUses, code, token, createdBy, exp)
		if err == nil {
			break
		}
		if !isUnique(err) || try >= 5 {
			d.fail(w, r, "insert invite", err)
			return
		}
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT code`); err != nil {
			d.fail(w, r, "rollback savepoint", err)
			return
		}
	}
	if err := BumpAccess(ctx, tx, a.server); err != nil {
		d.fail(w, r, "bump", err)
		return
	}
	if err := tx.Commit(); err != nil {
		d.fail(w, r, "commit", err)
		return
	}
	d.pushOne(ctx, a.server)
	writeJSON(w, http.StatusCreated, InviteView{ID: id, URL: d.inviteURL(token), ShortCode: code, Label: req.Label,
		Channels: dedupe(req.Channels), MaxUses: req.MaxUses, Uses: ptr(0), CreatedAt: now, ExpiresAt: exp, Status: "active"})
}

func ptr[T any](v T) *T { return &v }

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.ToLower(s)
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (d *Deps) inviteURL(token string) string {
	base := strings.TrimRight(d.WatchURL, "/")
	if base == "" {
		base = "https://watch.maktaba.tv"
	}
	return base + "/i/" + token
}

// ListInvites lists the publisher's invites, newest first, with their uses from the edge.
func (d *Deps) ListInvites(w http.ResponseWriter, r *http.Request, a actor) {
	list, err := ListInvites(r.Context(), d.DB, a.server, d.now())
	if err != nil {
		d.fail(w, r, "list invites", err)
		return
	}
	uses := d.edgeUses(r.Context(), a.server)
	for i := range list {
		list[i].URL = d.inviteURL(list[i].URL)
		if uses != nil {
			list[i].Uses = ptr(uses[list[i].ID])
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": list})
}

// ListInvites reads a home's invites (URL holds the token; the caller makes the link).
func ListInvites(ctx context.Context, db *sql.DB, server string, now time.Time) ([]InviteView, error) {
	rows, err := db.QueryContext(ctx, `
        SELECT id::text, token, short_code, label, channel_ids::text[], max_uses, created_at, expires_at, revoked_at IS NOT NULL
          FROM broadcast_invites WHERE server_id = $1 ORDER BY created_at DESC LIMIT 500`, server)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []InviteView{}
	for rows.Next() {
		var v InviteView
		var revoked bool
		var chs []string
		if err := rows.Scan(&v.ID, &v.URL, &v.ShortCode, &v.Label, pq.Array(&chs), &v.MaxUses, &v.CreatedAt, &v.ExpiresAt, &revoked); err != nil {
			return nil, err
		}
		v.Channels = chs
		if v.Channels == nil {
			v.Channels = []string{}
		}
		switch {
		case revoked:
			v.Status = "revoked"
		case !v.ExpiresAt.After(now):
			v.Status = "expired"
		default:
			v.Status = "active"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RevokeInvite revokes one of the publisher's invites: its passes stop at the next heartbeat.
func (d *Deps) RevokeInvite(w http.ResponseWriter, r *http.Request, a actor) {
	id := chi.URLParam(r, "id")
	if !billingpkg.IsUUID(id) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	d.change(w, r, a.server, func(tx *sql.Tx) (int, error) {
		n, err := RevokeInviteTx(r.Context(), tx, a.server, id, a.String())
		if err != nil || n == 0 {
			return http.StatusNotFound, err
		}
		return http.StatusNoContent, nil
	})
}

// RevokeInviteTx revokes an invite of server (any server when server is ""); it answers how
// many rows changed (0: none such, or already revoked).
func RevokeInviteTx(ctx context.Context, q interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, server, id, by string) (int64, error) {
	res, err := q.ExecContext(ctx, `
        UPDATE broadcast_invites SET revoked_at = now(), revoked_by = $3
         WHERE id = $1 AND ($2 = '' OR server_id::text = $2) AND revoked_at IS NULL`, id, server, by)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListDevices lists one invite's guest passes as the edge holds them (random ids, first and
// last use; never an identity).
func (d *Deps) ListDevices(w http.ResponseWriter, r *http.Request, a actor) {
	id := chi.URLParam(r, "id")
	if !d.ownsInvite(w, r, a.server, id) {
		return
	}
	if d.Edge == nil || d.Key == nil {
		writeErr(w, http.StatusServiceUnavailable, "edge_unavailable", "")
		return
	}
	passes, err := d.edgePasses(r.Context(), a.server)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "edge_unavailable", "")
		return
	}
	out := []EdgePass{}
	for _, p := range passes.Passes {
		if p.Invite == id {
			out = append(out, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// RevokeDevice revokes one device's guest pass of one of the publisher's invites.
func (d *Deps) RevokeDevice(w http.ResponseWriter, r *http.Request, a actor) {
	id, pass := chi.URLParam(r, "id"), chi.URLParam(r, "pass")
	if len(pass) == 0 || len(pass) > 64 || strings.ContainsAny(pass, " /") {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return
	}
	if !d.ownsInvite(w, r, a.server, id) {
		return
	}
	d.change(w, r, a.server, func(tx *sql.Tx) (int, error) {
		_, err := tx.ExecContext(r.Context(), `
            INSERT INTO broadcast_revoked_passes (server_id, pass_id, invite_id) VALUES ($1, $2, $3)
            ON CONFLICT DO NOTHING`, a.server, pass, id)
		return http.StatusNoContent, err
	})
}

// ownsInvite writes 404 unless the invite is the publisher's.
func (d *Deps) ownsInvite(w http.ResponseWriter, r *http.Request, server, id string) bool {
	if !billingpkg.IsUUID(id) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return false
	}
	var one int
	err := d.DB.QueryRowContext(r.Context(), `SELECT 1 FROM broadcast_invites WHERE id = $1 AND server_id = $2`, id, server).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not_found", "")
		return false
	}
	if err != nil {
		d.fail(w, r, "load invite", err)
		return false
	}
	return true
}
