// Command rehearsal-broadcast-monitor is the cloud's broadcast monitoring in the broadcast
// rehearsal (Epic 50 stories 50.28 and 50.29, scenario broadcast-observe): the maktaba_broadcast_*
// series the api role serves on its private metrics listener, made by the cloud's own code
// (broadcast.FoldMinute, broadcast.CostEstimate, broadcast.WriteSeries) from the rehearsal
// edge's Analytics Engine points, with the minute poll's timing (the minute before last). The
// witness's Prometheus (deploy/cloud-ha/monitoring/alerts.yml, unchanged) scrapes it.
//
// What stands in for what, rehearsal only:
//   - Analytics Engine and its SQL API: the edge's fake AE keeps every point it was given
//     (tools/rehearsal-cloud/broadcast/edge/entry.ts, /__rehearsal/ae); MinuteRows answers
//     minuteQuery's question over them, as the SQL API does over the dataset.
//   - broadcast_monitor in Postgres: the snapshot in memory.
//   - the revocation push's Took (broadcast.RecordRevocations): POST /revocations/took, which
//     the scenario calls once the edge answered its list PUT with 200, as the cloud's push does.
//   - the owner's ntfy topic: POST /ntfy takes Alertmanager's webhook (the phone receiver of
//     deploy/cloud-ha/monitoring/alertmanager.yml) and GET /ntfy lists what it took.
//
// Never deployed: tools/rehearsal-cloud/broadcast/Dockerfile.monitor builds it for CI.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/broadcast"
)

func main() {
	addr := flag.String("addr", ":9090", "the listener: /metrics, /revocations/took, /ntfy")
	edge := flag.String("edge", "http://edge:8787", "the rehearsal edge (its /__rehearsal/ae)")
	budget := flag.Float64("budget-usd", 0, "broadcast.monthly_budget_usd (0: no budget series)")
	token := flag.String("ntfy-token", "", "the bearer the ntfy stand-in expects from Alertmanager")
	poll := flag.Duration("poll", 5*time.Second, "how often the edge's points are read")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	m := newMonitor(*budget, *token)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go m.run(ctx, &edgeSource{base: *edge, http: &http.Client{Timeout: 10 * time.Second}}, *poll, logger)

	srv := &http.Server{Addr: *addr, Handler: m.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	logger.Info("rehearsal broadcast monitor listening", "addr", *addr, "edge", *edge)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("listener", "err", err)
		os.Exit(1)
	}
}

// Point is one Analytics Engine data point the edge wrote, with when it wrote it.
type Point struct {
	At      time.Time
	Blobs   []string
	Doubles []float64
}

func (p Point) blob(name string) string {
	if i := broadcast.BlobIndex(name); i >= 0 && i < len(p.Blobs) {
		return p.Blobs[i]
	}
	return ""
}

func (p Point) double(name string) float64 {
	if i := broadcast.DoubleIndex(name); i >= 0 && i < len(p.Doubles) {
		return p.Doubles[i]
	}
	return 0
}

// MinuteRows is minuteQuery's answer over points: beats and ingest in [from, to) by event,
// publisher and channel, n, each lag bucket's count (upload-to-visible at or under its bound)
// and the lag's sum. Every point is one event (_sample_interval 1: the rehearsal never samples).
func MinuteRows(points []Point, from, to time.Time) []broadcast.Row {
	type key struct{ event, pub, ch string }
	var order []key
	sums := map[key]broadcast.Row{}
	for _, p := range points {
		if p.At.Before(from) || !p.At.Before(to) {
			continue
		}
		ev := p.blob("event")
		if ev != "beat" && ev != "ingest" {
			continue
		}
		k := key{ev, p.blob("publisher"), p.blob("channel")}
		r, ok := sums[k]
		if !ok {
			r = broadcast.Row{"event": k.event, "publisher": k.pub, "channel": k.ch, "n": 0.0, "lag_ms": 0.0}
			for i := range broadcast.LagBuckets {
				r[broadcast.LagColumn(i)] = 0.0
			}
			sums[k] = r
			order = append(order, k)
		}
		lag := p.double("upload_to_visible_ms")
		r["n"] = r.Num("n") + 1
		r["lag_ms"] = r.Num("lag_ms") + lag
		for i, le := range broadcast.LagBuckets {
			if lag <= le*1000 {
				r[broadcast.LagColumn(i)] = r.Num(broadcast.LagColumn(i)) + 1
			}
		}
	}
	out := make([]broadcast.Row, 0, len(order))
	for _, k := range order {
		out = append(out, sums[k])
	}
	return out
}

// page is one webhook Alertmanager sent to the ntfy stand-in.
type page struct {
	At     time.Time       `json:"at"`
	AuthOK bool            `json:"auth_ok"`
	Body   json.RawMessage `json:"body"`
}

type monitor struct {
	budget float64
	token  string
	now    func() time.Time

	mu     sync.Mutex
	points []Point
	snap   broadcast.Snapshot
	folded time.Time // the last minute folded in
	// channelMinutes is live channel-minutes by rung count (the cost model's channel share)
	channelMinutes map[int]float64
	rev            sql.NullTime
	pages          []page
}

func newMonitor(budget float64, token string) *monitor {
	return &monitor{budget: budget, token: token, now: time.Now, channelMinutes: map[int]float64{}}
}

// keep is how long points are kept: the minute poll reads the minute before last.
const keep = 10 * time.Minute

func (m *monitor) add(ps []Point) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cut := m.now().Add(-keep)
	kept := m.points[:0]
	for _, p := range append(m.points, ps...) {
		if !p.At.Before(cut) {
			kept = append(kept, p)
		}
	}
	m.points = kept
}

// fold is the worker's minute poll: the minute before last, once.
func (m *monitor) fold() {
	m.mu.Lock()
	defer m.mu.Unlock()
	minute := m.now().Truncate(time.Minute).Add(-2 * time.Minute)
	if !m.folded.IsZero() && !m.folded.Before(minute) {
		return
	}
	rows := MinuteRows(m.points, minute, minute.Add(time.Minute))
	snap, _ := broadcast.FoldMinute(m.snap, rows, minute)
	// the month so far, by the cost model: each live channel-minute's share by its rungs, and
	// the viewer-minutes (the worker reads the same from broadcast_usage_hourly)
	for _, n := range rungsPerChannel(m.points, minute, minute.Add(time.Minute)) {
		m.channelMinutes[n]++
	}
	hours := map[int]float64{}
	for r, mins := range m.channelMinutes {
		hours[r] = mins / 60
	}
	snap.CostUSD = broadcast.CostEstimate(snap.ViewerMinutesTotal, hours)
	m.snap, m.folded = snap, minute
}

// rungsPerChannel is how many rungs each channel ingested in [from, to).
func rungsPerChannel(points []Point, from, to time.Time) map[broadcast.LiveChannel]int {
	seen := map[broadcast.LiveChannel]map[string]bool{}
	for _, p := range points {
		if p.At.Before(from) || !p.At.Before(to) || p.blob("event") != "ingest" {
			continue
		}
		k := broadcast.LiveChannel{Publisher: p.blob("publisher"), Channel: p.blob("channel")}
		if seen[k] == nil {
			seen[k] = map[string]bool{}
		}
		seen[k][p.blob("rung")] = true
	}
	out := map[broadcast.LiveChannel]int{}
	for k, rs := range seen {
		out[k] = len(rs)
	}
	return out
}

func (m *monitor) took(at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.rev.Valid || at.After(m.rev.Time) {
		m.rev = sql.NullTime{Time: at, Valid: true}
	}
}

func (m *monitor) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		snap, rev, folded := m.snap, m.rev, !m.folded.IsZero()
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if !folded {
			// as the api role before the worker's first poll: no row, no broadcast series
			return
		}
		_ = broadcast.WriteSeries(w, snap, rev, m.budget, m.now())
	})
	mux.HandleFunc("POST /revocations/took", func(w http.ResponseWriter, r *http.Request) {
		at := m.now()
		if v := r.URL.Query().Get("at_ms"); v != "" {
			ms, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "at_ms must be unix milliseconds", http.StatusBadRequest)
				return
			}
			at = time.UnixMilli(ms)
		}
		m.took(at)
		writeJSON(w, map[string]any{"took": at.UTC()})
	})
	mux.HandleFunc("POST /ntfy", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !json.Valid(body) {
			http.Error(w, "a JSON body", http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.pages = append(m.pages, page{At: m.now(), AuthOK: m.token != "" && r.Header.Get("Authorization") == "Bearer "+m.token, Body: body})
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /ntfy", func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		pages := append([]page{}, m.pages...)
		m.mu.Unlock()
		writeJSON(w, pages)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// source is where the points come from.
type source interface {
	read(ctx context.Context) ([]Point, error)
}

// edgeSource reads the rehearsal edge's AE points written since a little before its last read,
// each Worker's after the last one taken. A Worker whose epoch changed (the edge redeployed: its
// isolates and their points are new) is taken from its start.
type edgeSource struct {
	base  string
	http  *http.Client
	after map[string]edgeCursor
	last  time.Time
}

type edgeCursor struct {
	epoch string
	seq   int64
}

type aeAnswer struct {
	Workers []struct {
		Worker string `json:"worker"`
		Epoch  string `json:"ae_epoch"`
		Points []struct {
			Seq     int64     `json:"seq"`
			AtMS    int64     `json:"at_ms"`
			Blobs   []string  `json:"blobs"`
			Doubles []float64 `json:"doubles"`
		} `json:"ae_points"`
	} `json:"workers"`
}

func (e *edgeSource) read(ctx context.Context) ([]Point, error) {
	if e.after == nil {
		e.after = map[string]edgeCursor{}
	}
	since := int64(0)
	if !e.last.IsZero() {
		since = e.last.Add(-30 * time.Second).UnixMilli()
	}
	started := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base+"/__rehearsal/ae?since_ms="+strconv.FormatInt(since, 10), nil)
	if err != nil {
		return nil, err
	}
	res, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("the edge's AE points: HTTP " + strconv.Itoa(res.StatusCode))
	}
	var a aeAnswer
	if err := json.NewDecoder(res.Body).Decode(&a); err != nil {
		return nil, err
	}
	var out []Point
	for _, w := range a.Workers {
		cur := e.after[w.Worker]
		if cur.epoch != w.Epoch {
			cur = edgeCursor{epoch: w.Epoch}
		}
		for _, p := range w.Points {
			if p.Seq <= cur.seq {
				continue
			}
			out = append(out, Point{At: time.UnixMilli(p.AtMS), Blobs: p.Blobs, Doubles: p.Doubles})
			cur.seq = p.Seq
		}
		e.after[w.Worker] = cur
	}
	e.last = started
	return out, nil
}

func (m *monitor) run(ctx context.Context, src source, poll time.Duration, logger *slog.Logger) {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		ps, err := src.read(ctx)
		if err != nil {
			logger.Warn("edge points not read", "err", err)
		} else {
			m.add(ps)
		}
		m.fold()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
