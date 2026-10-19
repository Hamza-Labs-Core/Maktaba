// Package processes serves the process supervisor's state to the web
// app and records crashes for the control centre (Story 22.9).
//
//	GET /api/system/processes — admin: per-process state (api, streaming,
//	                             pipeline), restarts, last exit, last
//	                             probe, plus the supervisor's recent events
//
// maktaba-server's supervisor serves the same document on a loopback
// port ($MAKTABA_SUPERVISOR_URL + /processes); this handler proxies it
// so the browser never talks to that port. When the API runs without
// the supervisor (compose, a single --role) the route answers
// {"available": false} instead of an error.
//
// The Recorder polls the same document and writes each crash and hang
// into health_samples (proc.<role>.crash / proc.<role>.hang, value 1),
// so a crash stays visible in the control centre's health history after
// the process is back. The write is idempotent: the sample's key is the
// event's own timestamp.
package processes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	authmw "github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/middleware"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/common"
)

// Handler proxies the supervisor's state.
type Handler struct {
	// SupervisorURL is the supervisor's base URL ($MAKTABA_SUPERVISOR_URL).
	// Empty: the route reports available=false.
	SupervisorURL string
	// Client fetches the state; nil uses a short-timeout default.
	Client *http.Client
}

// Mount wires the route behind RequireAdmin.
func (h *Handler) Mount(r chi.Router) {
	r.With(authmw.RequireAdmin).Get("/api/system/processes", h.Get)
}

// Get implements GET /api/system/processes.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	doc, err := Fetch(r.Context(), h.client(), h.SupervisorURL)
	if err != nil {
		common.WriteJSON(w, r, http.StatusOK, map[string]any{
			"available": false,
			"reason":    err.Error(),
			"processes": []any{},
			"events":    []any{},
		})
		return
	}
	doc["available"] = true
	common.WriteJSON(w, r, http.StatusOK, doc)
}

func (h *Handler) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 3 * time.Second}
}

// errNoSupervisor: the API was started without a supervisor URL.
var errNoSupervisor = errors.New("not running under the maktaba-server supervisor")

// Fetch reads the supervisor's /processes document.
func Fetch(ctx context.Context, c *http.Client, base string) (map[string]any, error) {
	var doc map[string]any
	if err := fetchInto(ctx, c, base, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func fetchInto(ctx context.Context, c *http.Client, base string, v any) error {
	if strings.TrimSpace(base) == "" {
		return errNoSupervisor
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/processes", nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("supervisor unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("supervisor answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("supervisor state: %w", err)
	}
	return nil
}

// Event is the part of a supervisor event the recorder needs.
type Event struct {
	Seq    int64     `json:"seq"`
	At     time.Time `json:"at"`
	Role   string    `json:"role"`
	Kind   string    `json:"kind"`
	Reason string    `json:"reason"`
}

// Metric names the health_samples metric an event is recorded under, or
// "" for events that are not a failure.
func Metric(e Event) string {
	if e.Kind != "exited" || e.Role == "" {
		return ""
	}
	switch e.Reason {
	case "crash", "start_failed":
		return "proc." + e.Role + ".crash"
	case "hang":
		return "proc." + e.Role + ".hang"
	}
	return ""
}

// Recorder copies supervisor crash events into health_samples.
type Recorder struct {
	SupervisorURL string
	DB            *sql.DB
	Host          string
	Interval      time.Duration
	Client        *http.Client
	Logger        *slog.Logger

	lastSeq int64
}

// Run polls until ctx ends.
func (rc *Recorder) Run(ctx context.Context) {
	interval := rc.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, err := rc.Once(ctx); err != nil && rc.Logger != nil && !errors.Is(err, errNoSupervisor) {
			rc.Logger.Debug("processes: crash recorder poll failed", "err", err.Error(),
				"event", "proc_recorder_poll_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once records the failure events not yet seen and returns how many
// samples it wrote.
func (rc *Recorder) Once(ctx context.Context) (int, error) {
	client := rc.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	var doc struct {
		Events []Event `json:"events"`
	}
	if err := fetchInto(ctx, client, rc.SupervisorURL, &doc); err != nil {
		return 0, err
	}
	// A supervisor whose event log was wiped starts its sequence again.
	if n := len(doc.Events); n > 0 && doc.Events[n-1].Seq < rc.lastSeq {
		rc.lastSeq = 0
	}
	written := 0
	sqlite := isSQLite(rc.DB)
	maxSeq := rc.lastSeq
	for _, e := range doc.Events {
		if e.Seq <= rc.lastSeq {
			continue
		}
		if e.Seq > maxSeq {
			maxSeq = e.Seq
		}
		m := Metric(e)
		if m == "" {
			continue
		}
		var at any = e.At.UTC()
		if sqlite {
			at = e.At.UTC().Format("2006-01-02T15:04:05.000Z")
		}
		res, err := rc.DB.ExecContext(ctx,
			`INSERT INTO health_samples (at, host, metric, value) VALUES ($1, $2, $3, 1)
			 ON CONFLICT (metric, host, at) DO NOTHING`, at, rc.Host, m)
		if err != nil {
			return written, fmt.Errorf("record %s: %w", m, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			written++
		}
	}
	rc.lastSeq = maxSeq
	return written, nil
}

func isSQLite(db *sql.DB) bool {
	return db != nil && strings.Contains(strings.ToLower(fmt.Sprintf("%T", db.Driver())), "sqlite")
}
