package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/runtimecfg"
)

// IngestPath is Maktaba Cloud's ingest endpoint, under its API's origin.
const IngestPath = "/v1/telemetry"

const (
	// maxEnvelopeBatches and maxEnvelopeBytes keep a request within the
	// cloud's limits (20 batches, 512 KiB).
	maxEnvelopeBatches = 20
	maxEnvelopeBytes   = 480 << 10
	// tick is how often the sender looks for what is due.
	tick = time.Minute
	// kickDelay gathers an app's batches that arrive together into one request.
	kickDelay = 5 * time.Second
	// firstBackoff doubles each failed attempt, up to maxBackoff.
	firstBackoff = time.Minute
	maxBackoff   = 6 * time.Hour
)

// Settings keys the sender keeps its own state in (app_settings, not runtime:
// PATCH /api/settings refuses them).
const (
	keyServerInstallID = "telemetry.server_install_id"
	keyLastReportDay   = "telemetry.server_last_report_day"
)

// State is the server consent and the sender's own state.
type State interface {
	// Sharing is the admin's switch (telemetry.share_server_data).
	Sharing(ctx context.Context) bool
	// InstallID is the home's random id, made the first time it is needed.
	InstallID(ctx context.Context) (string, error)
	// NewInstallID replaces it (after "delete what this server sent").
	NewInstallID(ctx context.Context) (string, error)
	LastReportDay(ctx context.Context) string
	SetLastReportDay(ctx context.Context, day string) error
}

// PGState is the State in app_settings.
type PGState struct {
	DB       *sql.DB
	Settings *runtimecfg.Reader
}

func (s PGState) Sharing(ctx context.Context) bool {
	on, _ := s.Settings.Bool(ctx, runtimecfg.ShareServerData)
	return on
}

func (s PGState) text(ctx context.Context, key string) string {
	var raw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v
}

func (s PGState) set(ctx context.Context, key, value string) error {
	raw, _ := json.Marshal(value)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		key, string(raw), time.Now().UTC())
	return err
}

func (s PGState) InstallID(ctx context.Context) (string, error) {
	if id := s.text(ctx, keyServerInstallID); installIDRe.MatchString(id) {
		return id, nil
	}
	return s.NewInstallID(ctx)
}

func (s PGState) NewInstallID(ctx context.Context) (string, error) {
	id := NewInstallID()
	return id, s.set(ctx, keyServerInstallID, id)
}

func (s PGState) LastReportDay(ctx context.Context) string { return s.text(ctx, keyLastReportDay) }

func (s PGState) SetLastReportDay(ctx context.Context, day string) error {
	return s.set(ctx, keyLastReportDay, day)
}

const idAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// NewInstallID is 24 random characters (about 140 bits): an install's id,
// linked to nothing.
func NewInstallID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on a supported platform
	}
	for i, x := range b {
		b[i] = idAlphabet[int(x)%len(idAlphabet)]
	}
	return string(b)
}

// Sender delivers the outbox to Maktaba Cloud, and adds the home's report once
// a day while the admin's switch is on.
type Sender struct {
	Store    Store
	State    State
	Reporter *Reporter
	// URL is Maktaba Cloud's API origin; "" means nowhere to send (rows wait
	// and age out).
	URL    func() string
	Client *http.Client
	Logger *slog.Logger
	Now    func() time.Time

	kick chan struct{}
	once sync.Once

	mu       sync.Mutex
	lastSent time.Time
	lastErr  string
}

// Status is what the sender last did.
type Status struct {
	LastSentAt *time.Time `json:"last_sent_at"`
	LastError  string     `json:"last_error"`
}

func (s *Sender) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Sender) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Sender) init() {
	s.once.Do(func() { s.kick = make(chan struct{}, 1) })
}

// Kick asks for a delivery soon (an app's batch just arrived).
func (s *Sender) Kick() {
	if s == nil {
		return
	}
	s.init()
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Status is the last delivery's time and the last error since.
func (s *Sender) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{LastError: s.lastErr}
	if !s.lastSent.IsZero() {
		t := s.lastSent
		st.LastSentAt = &t
	}
	return st
}

// Run ticks until ctx ends.
func (s *Sender) Run(ctx context.Context) {
	s.init()
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
			select {
			case <-ctx.Done():
				return
			case <-time.After(kickDelay):
			}
		}
	}
}

// Tick prunes, adds the day's report when it is due, and delivers what is due.
func (s *Sender) Tick(ctx context.Context) {
	now := s.now()
	if n, err := s.Store.Prune(ctx, now.Add(-MaxAge)); err != nil {
		s.logger().Warn("telemetry: pruning the outbox failed", "err", err, "event", "telemetry_prune_failed")
	} else if n > 0 {
		s.logger().Info("telemetry: dropped batches Maktaba Cloud never got", "batches", n, "event", "telemetry_dropped")
	}
	s.report(ctx, now)
	s.deliver(ctx, now)
}

// report adds the home's report of the day while the switch is on. Off, the
// errors counted are let go and no server batch waits.
func (s *Sender) report(ctx context.Context, now time.Time) {
	if !s.State.Sharing(ctx) {
		if s.Reporter != nil && s.Reporter.Errors != nil {
			s.Reporter.Errors.Take()
		}
		if err := s.Store.DropKind(ctx, KindServer); err != nil {
			s.logger().Warn("telemetry: dropping the server's batches failed", "err", err, "event", "telemetry_drop_failed")
		}
		return
	}
	day := now.Format("2006-01-02")
	if s.Reporter == nil || s.State.LastReportDay(ctx) == day {
		return
	}
	id, err := s.State.InstallID(ctx)
	if err != nil {
		s.logger().Warn("telemetry: the home's install id failed", "err", err, "event", "telemetry_install_id_failed")
		return
	}
	rep := s.Reporter.Build(ctx, now, false)
	batch := map[string]any{"type": KindServer, "install_id": id, "day": day, "report": rep}
	if err := s.Store.Add(ctx, KindServer, id, batch); err != nil {
		if s.Reporter.Errors != nil {
			s.Reporter.Errors.Put(rep.Errors)
		}
		s.logger().Warn("telemetry: queueing the day's report failed", "err", err, "event", "telemetry_report_failed")
		return
	}
	if err := s.State.SetLastReportDay(ctx, day); err != nil {
		s.logger().Warn("telemetry: recording the day's report failed", "err", err, "event", "telemetry_report_failed")
	}
}

// deliver sends what is due, a request of up to maxEnvelopeBatches at a time.
func (s *Sender) deliver(ctx context.Context, now time.Time) {
	url := ""
	if s.URL != nil {
		url = strings.TrimRight(strings.TrimSpace(s.URL()), "/")
	}
	if url == "" {
		return
	}
	for i := 0; i < 10; i++ { // at most 200 batches a tick
		rows, err := s.Store.Due(ctx, now, maxEnvelopeBatches)
		if err != nil {
			s.logger().Warn("telemetry: reading the outbox failed", "err", err, "event", "telemetry_outbox_failed")
			return
		}
		if len(rows) == 0 {
			return
		}
		rows = fit(rows)
		if !s.send(ctx, url, now, rows) {
			return
		}
	}
}

// fit keeps the leading rows that fit in one request (always the first).
func fit(rows []Row) []Row {
	size := 0
	for i, r := range rows {
		size += len(r.Body) + 1
		if i > 0 && size > maxEnvelopeBytes {
			return rows[:i]
		}
	}
	return rows
}

// send posts rows; true when the next request may follow at once.
func (s *Sender) send(ctx context.Context, url string, now time.Time, rows []Row) bool {
	status, retryAfter, err := s.post(ctx, url, rows)
	switch {
	case err == nil && status/100 == 2:
		ids := make([]int64, len(rows))
		for i, r := range rows {
			ids[i] = r.ID
		}
		if err := s.Store.Sent(ctx, ids); err != nil {
			s.logger().Warn("telemetry: clearing sent batches failed", "err", err, "event", "telemetry_outbox_failed")
			return false
		}
		s.mu.Lock()
		s.lastSent, s.lastErr = now, ""
		s.mu.Unlock()
		return true
	case err == nil && status/100 == 4 && status != http.StatusTooManyRequests && status != http.StatusRequestTimeout:
		if len(rows) > 1 { // one of them was refused: each goes alone, the bad one is dropped
			for _, r := range rows {
				s.send(ctx, url, now, []Row{r})
			}
			return false
		}
		s.logger().Warn("telemetry: Maktaba Cloud refused a batch; dropped",
			"status", status, "kind", rows[0].Kind, "event", "telemetry_refused")
		_ = s.Store.Sent(ctx, []int64{rows[0].ID})
		s.setErr(fmt.Sprintf("refused (%d)", status))
		return true
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	} else {
		reason = "status " + strconv.Itoa(status)
	}
	s.setErr(reason)
	for _, r := range rows {
		wait := backoff(r.Attempts)
		if retryAfter > wait {
			wait = retryAfter
		}
		if err := s.Store.Retry(ctx, r.ID, now.Add(wait), reason); err != nil {
			s.logger().Warn("telemetry: putting a batch off failed", "err", err, "event", "telemetry_outbox_failed")
		}
	}
	s.logger().Info("telemetry: Maktaba Cloud not reached; will retry", "reason", reason,
		"batches", len(rows), "event", "telemetry_retry")
	return false
}

func (s *Sender) setErr(e string) {
	s.mu.Lock()
	s.lastErr = e
	s.mu.Unlock()
}

// backoff is the wait after attempts failures: a minute, doubled each time,
// at most maxBackoff.
func backoff(attempts int) time.Duration {
	d := firstBackoff
	for i := 0; i < attempts && d < maxBackoff; i++ {
		d *= 2
	}
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

func (s *Sender) post(ctx context.Context, url string, rows []Row) (int, time.Duration, error) {
	batches := make([]json.RawMessage, len(rows))
	for i, r := range rows {
		batches[i] = r.Body
	}
	body, err := json.Marshal(map[string]any{"schema": 1, "batches": batches})
	if err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+IngestPath, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	c := s.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		var uerr interface{ Timeout() bool }
		if errors.As(err, &uerr) && uerr.Timeout() {
			return 0, 0, errors.New("timed out")
		}
		return 0, 0, errors.New("unreachable")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	var after time.Duration
	if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
		after = time.Duration(secs) * time.Second
	}
	return resp.StatusCode, after, nil
}
