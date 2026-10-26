package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/middleware"
)

// Problem types of the sink (beside the httperror registry's own).
const (
	TypeUnknownEventKind = "https://maktaba.dev/problems/unknown-event-kind"
	TypeUnknownMetric    = "https://maktaba.dev/problems/unknown-metric"
	TypeTooManyEvents    = "https://maktaba.dev/problems/too-many-events"
)

const (
	// MaxEvents and MaxMetrics are the per-request caps; a request over them is
	// refused whole (413), nothing of it stored.
	MaxEvents  = 100
	MaxMetrics = 50
	// MaxPayloadBytes caps one event's stored payload.
	MaxPayloadBytes = 16 << 10
	// maxBodyBytes caps a request body.
	maxBodyBytes = 1 << 20
	// maxStringRunes caps one payload string (an error message, its stack).
	maxStringRunes = 2048

	// EventsPerMinute is the per-IP budget of events and metrics together;
	// DeletesPerMinute that of "Forget my device" (a pseudonym is 80+ random
	// bits, so guessing one is hopeless; the cap keeps it so).
	EventsPerMinute  = 1000
	DeletesPerMinute = 30

	// futureSkew is how far ahead of the server a device's clock may be before
	// its time is truncated to the server's (and a warning logged).
	futureSkew = time.Minute
)

// pseudonymRe: 16 characters made at opt-in (the web client's are base32).
var pseudonymRe = regexp.MustCompile(`^[A-Za-z0-9]{16}$`)

// Handler serves the sink. Enabled false is the server's opt-out
// ([telemetry] enabled = false): the POSTs answer 204 and store nothing.
// "Forget my device" works either way.
type Handler struct {
	DB      *sql.DB
	Enabled bool
	Logger  *slog.Logger

	// Events and Deletes are the per-IP budgets (nil: EventsPerMinute,
	// DeletesPerMinute). The IP is read for them only, never stored.
	Events  *middleware.IPBudget
	Deletes *middleware.IPBudget

	// Roots overrides the library roots read from the database (tests).
	Roots func(context.Context) ([]string, error)

	NowFunc func() time.Time

	cache *rootCache
}

// New is a Handler with its budgets made.
func New(db *sql.DB, enabled bool, logger *slog.Logger) *Handler {
	return &Handler{
		DB: db, Enabled: enabled, Logger: logger,
		Events:  middleware.NewIPBudget(EventsPerMinute),
		Deletes: middleware.NewIPBudget(DeletesPerMinute),
	}
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

// Mount wires the sink's routes. They are public (the auth gate's allowlist):
// the device's pseudonym is its only identity, and a signed-in user is never
// linked to it.
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
	r.Post("/api/telemetry", h.PostEvents)
	r.Post("/api/telemetry/web-vitals", h.PostWebVitals)
	r.Delete("/api/telemetry/devices/{pseudonym}", h.DeleteDevice)
}

// context the device sends once per request
type deviceContext struct {
	Pseudonym  string `json:"device_pseudonym"`
	AppVersion string `json:"app_version"`
	OS         string `json:"os"`
	OSVersion  string `json:"os_version"`
	Locale     string `json:"locale"`
}

type eventIn struct {
	EventKind string         `json:"event_kind"`
	Payload   map[string]any `json:"payload"`
	TS        string         `json:"ts"`
}

type eventsBody struct {
	deviceContext
	Events []eventIn `json:"events"`
}

type metricIn struct {
	Metric string  `json:"metric"`
	Value  float64 `json:"value"`
	Route  string  `json:"route"`
	TS     string  `json:"ts"`
}

type vitalsBody struct {
	Pseudonym string     `json:"device_pseudonym"`
	Metrics   []metricIn `json:"metrics"`
}

type eventRow struct {
	occurred time.Time
	kind     string
	payload  []byte
}

// PostEvents is POST /api/telemetry {device_pseudonym, app_version, os,
// os_version, locale, events: [{event_kind, payload, ts}]}: 204 when every event
// is stored, or none is (one transaction).
func (h *Handler) PostEvents(w http.ResponseWriter, r *http.Request) {
	if !h.Enabled {
		common.WriteNoContent(w)
		return
	}
	var b eventsBody
	if perr := common.ReadJSON(r, &b, maxBodyBytes); perr != nil {
		httperror.Write(w, r, perr)
		return
	}
	if len(b.Events) > MaxEvents {
		httperror.Write(w, r, tooMany("events", MaxEvents))
		return
	}
	if len(b.Events) == 0 {
		common.WriteNoContent(w)
		return
	}
	if !pseudonymRe.MatchString(b.Pseudonym) {
		httperror.Write(w, r, httperror.BadRequest("device_pseudonym must be 16 letters or digits"))
		return
	}
	if !h.allow(w, r, h.Events, len(b.Events)) {
		return
	}
	for _, e := range b.Events {
		if !IsKindAllowed(e.EventKind) {
			httperror.Write(w, r, &httperror.Error{
				Type: TypeUnknownEventKind, Title: "unknown event kind", Status: http.StatusBadRequest,
				Detail: clip(e.EventKind, 64),
			})
			return
		}
	}
	now := h.now()
	red, err := h.cache.get(r.Context(), now)
	if err != nil {
		// without the roots nothing is stored: a path could get through
		httperror.Write(w, r, httperror.Internal("telemetry: library roots").Wrap(err))
		return
	}
	rows := make([]eventRow, 0, len(b.Events))
	skewed := 0
	for _, e := range b.Events {
		payload := StripUnknownFields(e.EventKind, e.Payload)
		for k, v := range payload {
			if s, ok := v.(string); ok {
				payload[k] = clip(red.Apply(s), maxStringRunes)
			}
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			httperror.Write(w, r, httperror.Internal("telemetry: payload").Wrap(err))
			return
		}
		if len(raw) > MaxPayloadBytes {
			httperror.Write(w, r, &httperror.Error{
				Type: httperror.TypeBodyTooLarge, Title: "payload too large",
				Status: http.StatusRequestEntityTooLarge,
				Detail: "an event's payload exceeds " + strconv.Itoa(MaxPayloadBytes) + " bytes",
			})
			return
		}
		occurred, ahead := occurredAt(e.TS, now)
		if ahead {
			skewed++
		}
		rows = append(rows, eventRow{occurred: occurred, kind: e.EventKind, payload: raw})
	}
	if skewed > 0 {
		h.logger().Warn("telemetry: event times ahead of the server's; truncated to received_at",
			"events", skewed, "event", "telemetry_clock_skew")
	}
	dc := b.deviceContext
	if err := h.insertEvents(r.Context(), now, dc, rows); err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: store events").Wrap(err))
		return
	}
	common.WriteNoContent(w)
}

func (h *Handler) insertEvents(ctx context.Context, now time.Time, dc deviceContext, rows []eventRow) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO telemetry_events
		(received_at, occurred_at, device_pseudonym, event_kind, app_version, os, os_version, locale, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, row := range rows {
		if _, err := stmt.ExecContext(ctx, now, row.occurred, dc.Pseudonym, row.kind,
			nullable(clip(dc.AppVersion, 64)), nullable(clip(dc.OS, 32)),
			nullable(clip(dc.OSVersion, 32)), nullable(clip(dc.Locale, 16)), string(row.payload)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PostWebVitals is POST /api/telemetry/web-vitals {device_pseudonym, metrics:
// [{metric, value, route, ts}]}: 204 when every metric is stored, or none is.
// A route is stored as its template (an id in it becomes ":id").
func (h *Handler) PostWebVitals(w http.ResponseWriter, r *http.Request) {
	if !h.Enabled {
		common.WriteNoContent(w)
		return
	}
	var b vitalsBody
	if perr := common.ReadJSON(r, &b, maxBodyBytes); perr != nil {
		httperror.Write(w, r, perr)
		return
	}
	if len(b.Metrics) > MaxMetrics {
		httperror.Write(w, r, tooMany("metrics", MaxMetrics))
		return
	}
	if len(b.Metrics) == 0 {
		common.WriteNoContent(w)
		return
	}
	if !pseudonymRe.MatchString(b.Pseudonym) {
		httperror.Write(w, r, httperror.BadRequest("device_pseudonym must be 16 letters or digits"))
		return
	}
	if !h.allow(w, r, h.Events, len(b.Metrics)) {
		return
	}
	for _, m := range b.Metrics {
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
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: store metrics").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	for _, m := range b.Metrics {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO telemetry_web_vitals
			(received_at, device_pseudonym, metric, value, route) VALUES ($1, $2, $3, $4, $5)`,
			now, b.Pseudonym, m.Metric, m.Value, nullable(RouteTemplate(m.Route))); err != nil {
			httperror.Write(w, r, httperror.Internal("telemetry: store metrics").Wrap(err))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: store metrics").Wrap(err))
		return
	}
	common.WriteNoContent(w)
}

// DeleteDevice is DELETE /api/telemetry/devices/{pseudonym}: every row of the
// pseudonym goes, events and metrics in one transaction. Anyone holding the
// pseudonym may (it is the device's self-asserted bearer); 204 also when there
// was nothing to delete, so the answer says nothing about which pseudonyms exist.
func (h *Handler) DeleteDevice(w http.ResponseWriter, r *http.Request) {
	p := chi.URLParam(r, "pseudonym")
	if !pseudonymRe.MatchString(p) {
		httperror.Write(w, r, httperror.BadRequest("pseudonym must be 16 letters or digits"))
		return
	}
	if !h.allow(w, r, h.Deletes, 1) {
		return
	}
	tx, err := h.DB.BeginTx(r.Context(), nil)
	if err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: forget device").Wrap(err))
		return
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM telemetry_events WHERE device_pseudonym = $1`,
		`DELETE FROM telemetry_web_vitals WHERE device_pseudonym = $1`,
	} {
		if _, err := tx.ExecContext(r.Context(), q, p); err != nil {
			httperror.Write(w, r, httperror.Internal("telemetry: forget device").Wrap(err))
			return
		}
	}
	if err := tx.Commit(); err != nil {
		httperror.Write(w, r, httperror.Internal("telemetry: forget device").Wrap(err))
		return
	}
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
		Detail: "per-IP telemetry limit exceeded",
	})
	return false
}

func tooMany(what string, max int) *httperror.Error {
	return &httperror.Error{
		Type: TypeTooManyEvents, Title: "too many " + what, Status: http.StatusRequestEntityTooLarge,
		Detail: "at most " + strconv.Itoa(max) + " " + what + " per request; none was stored",
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
	return clip(strings.Join(parts, "/"), 128)
}

// clip shortens s to n runes (on a rune boundary).
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func nullable(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}
