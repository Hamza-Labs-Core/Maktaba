package broadcast

// Story 50.28 §3: a publisher's viewer analytics.
//
//	GET /v1/broadcast/analytics?server=<id>&period=today|7d|month|day[&date=YYYY-MM-DD][&tz=<IANA>][&format=csv]
//	    the account that owns the home (its cloud registration); anyone else 403
//	GET /v1/admin/broadcast/analytics?server=<id>&…
//	    a service admin, behind the console's gate; each view writes an audit row
//
// Both answer the same folded report (internal/broadcast.Load): no country or device with fewer
// than broadcast.analytics_k sessions in the period is named, and nothing personal is stored to
// begin with. A home that isn't the caller's is 403 whether or not it exists.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/audit"
	authmw "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/auth/middleware"
	bc "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// ActionAnalyticsView is the audit action of a service admin's view.
const ActionAnalyticsView = "admin.broadcast_analytics.view"

// ServerLookup finds a home (stores.Servers).
type ServerLookup interface {
	ByID(ctx context.Context, id string) (stores.Server, error)
}

// AnalyticsDeps wires the analytics routes.
type AnalyticsDeps struct {
	DB      *sql.DB
	Servers ServerLookup
	// Gate admits service admins (auth/middleware.RequireServiceAdmin). Nil refuses everyone.
	Gate   func(http.Handler) http.Handler
	Audit  *audit.Log
	Logger *slog.Logger
	// K is broadcast.analytics_k (default 5).
	K   int
	Now func() time.Time
}

// MountAnalyticsUser registers the publisher's route inside the bearer-token group.
func MountAnalyticsUser(r chi.Router, d *AnalyticsDeps) {
	r.Get("/v1/broadcast/analytics", d.UserAnalytics)
}

// MountAnalyticsAdmin registers the console's route behind the service-admin gate.
func MountAnalyticsAdmin(r chi.Router, d *AnalyticsDeps) {
	gate := d.Gate
	if gate == nil {
		gate = func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				analyticsErr(w, http.StatusForbidden, "admin_access_denied")
			})
		}
	}
	r.Group(func(g chi.Router) {
		g.Use(gate)
		g.Get("/v1/admin/broadcast/analytics", d.AdminAnalytics)
	})
}

func (d *AnalyticsDeps) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

var serverIDRE = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

// PublisherIDs are the publisher ids a home's broadcast events carry. 50.21's ingest token names
// the home's owner as the publisher and 50.20 names the home itself; both are read until those
// two settle on one, so neither order of their merges loses a home's history.
func PublisherIDs(s stores.Server) []string { return []string{s.ID, s.OwnerUserID} }

// UserAnalytics is the publisher's view: the home's owner only.
func (d *AnalyticsDeps) UserAnalytics(w http.ResponseWriter, r *http.Request) {
	uid := middleware.GetUserID(r.Context())
	if uid == "" {
		analyticsErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.URL.Query().Get("server")
	if !serverIDRE.MatchString(id) {
		analyticsErr(w, http.StatusBadRequest, "bad_server")
		return
	}
	s, err := d.Servers.ByID(r.Context(), id)
	if errors.Is(err, stores.ErrServerNotFound) || (err == nil && s.OwnerUserID != uid) {
		analyticsErr(w, http.StatusForbidden, "broadcast_analytics_forbidden")
		return
	}
	if err != nil {
		d.fail(w, r, "load home", err)
		return
	}
	level, allowance, err := d.levelOf(r.Context(), s)
	if err != nil {
		d.fail(w, r, "load plan", err)
		return
	}
	d.answer(w, r, s, level, allowance)
}

// AdminAnalytics is a service admin's view of any home, audited; always the full level.
func (d *AnalyticsDeps) AdminAnalytics(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("server")
	if !serverIDRE.MatchString(id) {
		analyticsErr(w, http.StatusBadRequest, "bad_server")
		return
	}
	s, err := d.Servers.ByID(r.Context(), id)
	if errors.Is(err, stores.ErrServerNotFound) {
		analyticsErr(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		d.fail(w, r, "load home", err)
		return
	}
	if d.Audit != nil {
		e := audit.Entry{Action: ActionAnalyticsView, TargetKind: "server", TargetID: s.ID,
			Detail: map[string]any{"period": r.URL.Query().Get("period")}, IP: middleware.GetClientIP(r.Context())}
		if u, ok := authmw.ServiceAdmin(r.Context()); ok {
			e.ActorKind, e.ActorID, e.ActorEmail = audit.ActorStaff, u.ID, u.Email
		}
		if err := d.Audit.Write(r.Context(), e); err != nil {
			d.fail(w, r, "audit", err)
			return
		}
	}
	_, allowance, err := d.levelOf(r.Context(), s)
	if err != nil {
		d.fail(w, r, "load plan", err)
		return
	}
	d.answer(w, r, s, bc.LevelFull, allowance)
}

// Allowances in viewer-hours a month (README §14.4).
const (
	AllowanceGuestSharing = 50.0
	AllowanceBroadcaster  = 5000.0
)

// levelOf is the home's analytics level and allowance: the Broadcaster tier (50.20's
// servers.broadcast_plan, read through to_jsonb so this works before and after that column)
// sees everything; Pro's guest sharing sees live viewers and hours.
func (d *AnalyticsDeps) levelOf(ctx context.Context, s stores.Server) (bc.Level, *float64, error) {
	var bp sql.NullString
	err := d.DB.QueryRowContext(ctx, `SELECT to_jsonb(s) ->> 'broadcast_plan' FROM servers s WHERE s.id = $1`, s.ID).Scan(&bp)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	if bp.String == "broadcaster" {
		a := AllowanceBroadcaster
		return bc.LevelFull, &a, nil
	}
	if strings.EqualFold(s.Plan, "pro") {
		a := AllowanceGuestSharing
		return bc.LevelHours, &a, nil
	}
	return bc.LevelHours, nil, nil
}

func (d *AnalyticsDeps) answer(w http.ResponseWriter, r *http.Request, s stores.Server, level bc.Level, allowance *float64) {
	q := r.URL.Query()
	loc := time.UTC
	if tz := q.Get("tz"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil || len(tz) > 64 {
			analyticsErr(w, http.StatusBadRequest, "bad_time_zone")
			return
		}
		loc = l
	}
	now := d.now()
	from, to, err := bc.Period(q.Get("period"), q.Get("date"), loc, now)
	if err != nil {
		analyticsErr(w, http.StatusBadRequest, "bad_period")
		return
	}
	k := d.K
	if k < 1 {
		k = bc.DefaultK
	}
	rep, err := bc.Load(r.Context(), d.DB, bc.Query{Publishers: PublisherIDs(s), From: from, To: to, Loc: loc,
		K: k, Level: level, Allowance: allowance, Now: now})
	if err != nil {
		d.fail(w, r, "load analytics", err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="broadcast-analytics-`+from.In(loc).Format("2006-01-02")+`.csv"`)
		if err := bc.WriteCSV(w, rep); err != nil {
			d.Logger.Warn("broadcast analytics csv", "err", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rep)
}

func (d *AnalyticsDeps) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	if d.Logger != nil {
		d.Logger.Error("broadcast analytics: "+what, "err", err, "request_id", middleware.GetRequestID(r.Context()))
	}
	analyticsErr(w, http.StatusInternalServerError, "internal_error")
}

func analyticsErr(w http.ResponseWriter, code int, kind string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": kind})
}
