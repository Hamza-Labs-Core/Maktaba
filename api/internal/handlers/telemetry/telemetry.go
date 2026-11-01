package telemetry

import (
	"context"
	"database/sql"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// Problem types of the pipe (beside the httperror registry's own).
const (
	TypeUnknownEventKind = "https://maktaba.tv/problems/unknown-event-kind"
	TypeUnknownMetric    = "https://maktaba.tv/problems/unknown-metric"
	TypeTooManyEvents    = "https://maktaba.tv/problems/too-many-events"
	TypeUnknownField     = "https://maktaba.tv/problems/unknown-field"
)

const (
	// MaxEvents and MaxMetrics are the per-request caps; a request over them is
	// refused whole (413), nothing of it kept.
	MaxEvents  = 100
	MaxMetrics = 50
	// MaxPayloadBytes caps one event's payload.
	MaxPayloadBytes = 16 << 10
	// maxBodyBytes caps a request body.
	maxBodyBytes = 1 << 20
	// maxStringRunes caps one payload string (an error message, its stack).
	maxStringRunes = 2048

	// EventsPerMinute is the per-IP budget of events and metrics together;
	// DeletesPerMinute that of "Delete what this app sent".
	EventsPerMinute  = 1000
	DeletesPerMinute = 30

	// maxVital is the largest Web Vital kept, in milliseconds (Maktaba Cloud's
	// limit).
	maxVital = 600000

	// futureSkew is how far ahead of the server a device's clock may be before
	// its time is truncated to the server's.
	futureSkew = time.Minute
)

// installIDRe: an install's random id. The web app's ids from before the
// rework (16 base32 characters) are ones too.
var installIDRe = regexp.MustCompile(`^[A-Za-z0-9]{16,32}$`)

// tokenRe is what an app's name, platform and form factor may be (the
// X-Maktaba-Client header's parts, Epic 44).
var tokenRe = regexp.MustCompile(`^[a-z0-9._-]{1,32}$`)

// Handler serves the home's part of usage data: the pipe apps post through,
// and the admin's view of the server's own report.
type Handler struct {
	DB *sql.DB
	// Enabled false is server.toml's [telemetry] enabled = "off": nothing
	// leaves this home, the server's data nor any app's (the POSTs answer 204
	// and keep nothing).
	Enabled bool
	Store   Store
	State   State
	// Reporter builds the preview the admin sees (the day's report as of now).
	Reporter *Reporter
	// Sender is kicked when an app's batch arrives; its status is the admin's.
	Sender *Sender
	Logger *slog.Logger

	// Events and Deletes are the per-IP budgets (nil: EventsPerMinute,
	// DeletesPerMinute). The IP is read for them only, never kept.
	Events  *middleware.IPBudget
	Deletes *middleware.IPBudget

	// Roots overrides the library roots read from the database (tests).
	Roots func(context.Context) ([]string, error)

	NowFunc func() time.Time

	cache *rootCache
}

func (h *Handler) now() time.Time {
	if h.NowFunc != nil {
		return h.NowFunc().UTC()
	}
	return time.Now().UTC()
}

func (h *Handler) logger() *slog.Logger {
	if h.Logger != nil {
		return h.Logger
	}
	return slog.Default()
}

// Mount wires the routes. Every one needs a signed-in user (the auth gate);
// the server's own are an admin's. Who posted an app's batch is never kept.
func (h *Handler) Mount(r chi.Router) {
	if h.Events == nil {
		h.Events = middleware.NewIPBudget(EventsPerMinute)
	}
	if h.Deletes == nil {
		h.Deletes = middleware.NewIPBudget(DeletesPerMinute)
	}
	loader := h.Roots
	if loader == nil {
		loader = loadRoots(h.DB)
	}
	h.cache = &rootCache{loader: loader}
	r.Post("/api/telemetry/app", h.PostApp)
	r.Delete("/api/telemetry/app/{installID}", h.ForgetApp)
	r.Get("/api/telemetry/server", h.ServerStatus)
	r.Post("/api/telemetry/server/forget", h.ForgetServer)
}

type eventIn struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
	TS      string         `json:"ts"`
}

type metricIn struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Route  string  `json:"route"`
}

// appBody is an app's batch as it posts it.
type appBody struct {
	Consent    *bool      `json:"consent"`
	InstallID  string     `json:"install_id"`
	App        string     `json:"app"`
	Platform   string     `json:"platform"`
	FormFactor string     `json:"form_factor"`
	AppVersion string     `json:"app_version"`
	OSVersion  string     `json:"os_version"`
	Locale     string     `json:"locale"`
	Events     []eventIn  `json:"events"`
	Vitals     []metricIn `json:"vitals"`
}

// AppBatch is an app's batch as it goes to Maktaba Cloud (type "app").
type AppBatch struct {
	Type       string      `json:"type"`
	InstallID  string      `json:"install_id"`
	App        string      `json:"app"`
	Platform   string      `json:"platform"`
	FormFactor string      `json:"form_factor"`
	AppVersion string      `json:"app_version"`
	OSVersion  string      `json:"os_version"`
	Locale     string      `json:"locale"`
	Events     []EventOut  `json:"events"`
	Vitals     []MetricOut `json:"vitals"`
}

// EventOut is an allowed event, its payload scrubbed.
type EventOut struct {
	Kind    string         `json:"kind"`
	Payload map[string]any `json:"payload"`
	TS      string         `json:"ts"`
}

// MetricOut is a Web Vital, its route a template.
type MetricOut struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Route  string  `json:"route"`
}

func unauthorized() *httperror.Error {
	return &httperror.Error{Type: httperror.TypeForbidden, Title: "unauthorized",
		Status: http.StatusUnauthorized, Detail: "authentication required"}
}

func signedIn(w http.ResponseWriter, r *http.Request) bool {
	if principal.FromContext(r.Context()) == nil {
		httperror.Write(w, r, unauthorized())
		return false
	}
	return true
}

func adminOnly(w http.ResponseWriter, r *http.Request) bool {
	p := principal.FromContext(r.Context())
	if p == nil {
		httperror.Write(w, r, unauthorized())
		return false
	}
	if !p.IsAdmin {
		httperror.Write(w, r, httperror.Forbidden("", "admin-only"))
		return false
	}
	return true
}

// PostApp is POST /api/telemetry/app: an app's batch, forwarded to Maktaba
// Cloud when the body says its user agreed (consent: true). Without that, or
// with server.toml's [telemetry] off, 204 and nothing is kept. The admin's
// server switch plays no part: the app's user decided for the app.
func (h *Handler) PostApp(w http.ResponseWriter, r *http.Request) {
	if !signedIn(w, r) {
		return
	}
	var b appBody
	if perr := common.ReadJSON(r, &b, maxBodyBytes); perr != nil {
		httperror.Write(w, r, perr)
		return
	}
	if !h.Enabled || b.Consent == nil || !*b.Consent {
		common.WriteNoContent(w)
		return
	}
	if len(b.Events) > MaxEvents {
		httperror.Write(w, r, tooMany("events", MaxEvents))
		return
	}
	if len(b.Vitals) > MaxMetrics {
		httperror.Write(w, r, tooMany("metrics", MaxMetrics))
		return
	}
	if len(b.Events)+len(b.Vitals) == 0 {
		common.WriteNoContent(w)
		return
	}
	if !installIDRe.MatchString(b.InstallID) {
		httperror.Write(w, r, httperror.BadRequest("install_id must be 16 to 32 letters or digits"))
		return
	}
	for _, f := range []struct{ name, v string }{{"app", b.App}, {"platform", b.Platform}} {
		if !tokenRe.MatchString(f.v) {
			httperror.Write(w, r, httperror.BadRequest(f.name+" must be a short lowercase name"))
			return
		}
	}
	if b.FormFactor != "" && !tokenRe.MatchString(b.FormFactor) {
		httperror.Write(w, r, httperror.BadRequest("form_factor must be a short lowercase name"))
		return
	}
	if !h.allow(w, r, h.Events, len(b.Events)+len(b.Vitals)) {
		return
	}
	checked := make([]map[string]any, len(b.Events))
	for i, e := range b.Events {
		if !IsKindAllowed(e.Kind) {
			httperror.Write(w, r, &httperror.Error{
				Type: TypeUnknownEventKind, Title: "unknown event kind", Status: http.StatusBadRequest,
				Detail: clip(e.Kind, 64),
			})
			return
		}
		payload, field := CheckFields(e.Kind, e.Payload)
		if field != "" {
			httperror.Write(w, r, &httperror.Error{
				Type: TypeUnknownField, Title: "field not allowed", Status: http.StatusBadRequest,
				Detail: clip(e.Kind+"."+field, 96) + " is not on the allow-list (or is not a scalar)",
			})
			return
		}
		checked[i] = payload
	}
	for _, m := range b.Vitals {
		if _, ok := Metrics[m.Metric]; !ok {
			httperror.Write(w, r, &httperror.Error{
				Type: TypeUnknownMetric, Title: "unknown metric", Status: http.StatusBadRequest,
				Detail: clip(m.Metric, 16),
			})
			return
		}
		if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) || m.Value < 0 {
			httperror.Write(w, r, httperror.BadRequest("a metric's value must be a number of 0 or more"))
			return
		}
	}
	now := h.now()
	red, err := h.cache.get(r.Context(), now)
	if err != nil {
		// without the roots nothing is kept: a path could get through
		httperror.Write(w, r, httperror.Internal("telemetry: library roots").Wrap(err))
		return
	}
	out := AppBatch{
		Type: KindApp, InstallID: b.InstallID, App: b.App, Platform: b.Platform, FormFactor: b.FormFactor,
		AppVersion: version(b.AppVersion, "unknown"), OSVersion: version(b.OSVersion, ""), Locale: locale(b.Locale),
		Events: make([]EventOut, 0, len(b.Events)), Vitals: make([]MetricOut, 0, len(b.Vitals)),
	}
	for i, e := range b.Events {
		payload, ok := Normalize(e.Kind, checked[i])
		if !ok {
			continue
		}
		size := 0
		for k, v := range payload {
			if s, ok := v.(string); ok {
				s = clip(red.Apply(s), maxStringRunes)
				payload[k] = s
				size += len(s)
			}
		}
		if size > MaxPayloadBytes {
			httperror.Write(w, r, &httperror.Error{
				Type: httperror.TypeBodyTooLarge, Title: "payload too large",
				Status: http.StatusRequestEntityTooLarge,
				Detail: "an event's payload exceeds " + strconv.Itoa(MaxPayloadBytes) + " bytes",
			})
			return
		}
		occurred, _ := occurredAt(e.TS, now)
		out.Events = append(out.Events, EventOut{Kind: e.Kind, Payload: payload, TS: occurred.Format(time.RFC3339)})
	}
	for _, m := range b.Vitals {
		if m.Value > maxVital {
			continue // a page open for ten minutes says nothing about its speed
		}
		route := RouteTemplate(m.Route)
		if !strings.HasPrefix(route, "/") {
			route = clip("/"+route, 120)
		}
		out.Vitals = append(out.Vitals, MetricOut{Metric: m.Metric, Value: m.Value, Route: route})
	}
	if len(out.Events)+len(out.Vitals) == 0 {
		common.WriteNoContent(w)
		return
	}
	if err := h.Store.Add(r.Context(), KindApp, b.InstallID, out); err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: queue the batch").Wrap(err))
		return
	}
	h.Sender.Kick()
	common.WriteNoContent(w)
}

// ForgetApp is DELETE /api/telemetry/app/{installID}: "Delete what this app
// sent". The install's waiting batches go at once, and Maktaba Cloud is asked
// to delete what it holds of it. The id is the app's own secret (random, never
// shown elsewhere); 204 also when there was nothing, so the answer says nothing
// about which ids exist.
func (h *Handler) ForgetApp(w http.ResponseWriter, r *http.Request) {
	if !signedIn(w, r) {
		return
	}
	id := chi.URLParam(r, "installID")
	if !installIDRe.MatchString(id) {
		httperror.Write(w, r, httperror.BadRequest("install id must be 16 to 32 letters or digits"))
		return
	}
	if !h.allow(w, r, h.Deletes, 1) {
		return
	}
	if err := h.Store.DropInstall(r.Context(), id); err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: forget the app").Wrap(err))
		return
	}
	if h.Enabled {
		if err := h.Store.Add(r.Context(), KindForget, id, map[string]string{"type": KindForget, "install_id": id}); err != nil {
			httperror.Write(w, r, httperror.Internal("telemetry: forget the app").Wrap(err))
			return
		}
		h.Sender.Kick()
	}
	common.WriteNoContent(w)
}

// serverView is GET /api/telemetry/server.
type serverView struct {
	// Sharing is the admin's switch; Enabled false is server.toml's
	// [telemetry] off, which overrides it (and every app's consent).
	Sharing bool `json:"sharing"`
	Enabled bool `json:"enabled"`
	// Pending is how many of the server's reports wait to go, OldestPending
	// since when.
	Pending       int        `json:"pending"`
	OldestPending *time.Time `json:"oldest_pending"`
	LastSentAt    *time.Time `json:"last_sent_at"`
	LastError     string     `json:"last_error"`
	// Preview is the report as it would go now: what the admin agrees to.
	Preview ServerReport `json:"preview"`
}

// ServerStatus is GET /api/telemetry/server (an admin's): the switch, how the
// delivery goes, and exactly what the day's report holds.
func (h *Handler) ServerStatus(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	v := serverView{Sharing: h.State.Sharing(r.Context()), Enabled: h.Enabled}
	n, oldest, err := h.Store.Pending(r.Context(), KindServer)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: the outbox").Wrap(err))
		return
	}
	v.Pending, v.OldestPending = n, oldest
	if h.Sender != nil {
		st := h.Sender.Status()
		v.LastSentAt, v.LastError = st.LastSentAt, st.LastError
	}
	if h.Reporter != nil {
		v.Preview = h.Reporter.Build(r.Context(), h.now(), true)
	}
	common.WriteJSON(w, r, http.StatusOK, v)
}

// ForgetServer is POST /api/telemetry/server/forget (an admin's): "Delete what
// this server sent". The server's waiting reports go, Maktaba Cloud is asked to
// delete what it holds under the home's id, and the home takes a new id (a
// later report is not linked to the earlier ones).
func (h *Handler) ForgetServer(w http.ResponseWriter, r *http.Request) {
	if !adminOnly(w, r) {
		return
	}
	ctx := r.Context()
	old, err := h.State.InstallID(ctx)
	if err == nil {
		err = h.Store.DropKind(ctx, KindServer)
	}
	if err == nil && h.Enabled {
		err = h.Store.Add(ctx, KindForget, old, map[string]string{"type": KindForget, "install_id": old})
	}
	if err == nil {
		_, err = h.State.NewInstallID(ctx)
	}
	if err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: forget the server").Wrap(err))
		return
	}
	h.logger().Info("telemetry: the admin deleted what this server sent", "event", "telemetry_server_forgotten")
	h.Sender.Kick()
	common.WriteNoContent(w)
}

// allow takes n from r's IP budget, or answers 429.
func (h *Handler) allow(w http.ResponseWriter, r *http.Request, b *middleware.IPBudget, n int) bool {
	ok, retry := b.Allow(r, n)
	if ok {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	httperror.Write(w, r, &httperror.Error{
		Type: httperror.TypeRateLimited, Title: "too many requests", Status: http.StatusTooManyRequests,
		Detail: "per-IP usage data limit exceeded",
	})
	return false
}

func tooMany(what string, limit int) *httperror.Error {
	return &httperror.Error{
		Type: TypeTooManyEvents, Title: "too many " + what, Status: http.StatusRequestEntityTooLarge,
		Detail: "at most " + strconv.Itoa(limit) + " " + what + " per request; none was kept",
	}
}

// occurredAt is the device's time for an event: now when it sent none (or one
// that does not parse), and now, with ahead true, when it is past now by more
// than futureSkew.
func occurredAt(ts string, now time.Time) (t time.Time, ahead bool) {
	if ts == "" {
		return now, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return now, false
	}
	parsed = parsed.UTC()
	if parsed.After(now.Add(futureSkew)) {
		return now, true
	}
	if parsed.After(now) {
		return now, false
	}
	return parsed, false
}

// staticSegment is a route segment kept as it is: a lowercase word ("videos",
// "live-tv") or a template parameter (":videoId"). Anything else is an id.
var staticSegment = regexp.MustCompile(`^(?:[a-z][a-z-]{0,31}|:[A-Za-z][A-Za-z0-9_]{0,31})$`)

// RouteTemplate turns a route into its template: the query and fragment go, and
// a segment that is not a static word becomes ":id" (/videos/3f2a…/watch →
// /videos/:id/watch). "" for an empty route.
func RouteTemplate(route string) string {
	route = strings.TrimSpace(route)
	if i := strings.IndexAny(route, "?#"); i >= 0 {
		route = route[:i]
	}
	if route == "" {
		return ""
	}
	parts := strings.Split(route, "/")
	for i, p := range parts {
		if p != "" && !staticSegment.MatchString(p) {
			parts[i] = ":id"
		}
	}
	return clip(strings.Join(parts, "/"), 120)
}

// clip shortens s to n runes (on a rune boundary).
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
