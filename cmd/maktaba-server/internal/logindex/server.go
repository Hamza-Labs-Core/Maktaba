package logindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Sweep drops rows older than Retention and rows whose segment the keeper deleted, in
// batches of 10,000; then, while logs.db is over MaxBytes, the oldest debug and info rows
// go first. The file shrinks by incremental vacuum, never a full one.
func (ix *Index) Sweep(ctx context.Context) (int, error) {
	total := 0
	del := func(where string, args ...any) error {
		for ctx.Err() == nil {
			q := `SELECT id FROM lines WHERE ` + where + ` LIMIT 10000`
			rows, err := ix.db.QueryContext(ctx, q, args...)
			if err != nil {
				return err
			}
			var ids []any
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil {
					ids = append(ids, id)
				}
			}
			_ = rows.Close()
			if len(ids) == 0 {
				return nil
			}
			in := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
			tx, err := ix.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`DELETE FROM lines_fts WHERE rowid IN (`+in+`)`, ids...); err != nil {
				_ = tx.Rollback()
				return err
			}
			if _, err := tx.Exec(`DELETE FROM lines WHERE id IN (`+in+`)`, ids...); err != nil {
				_ = tx.Rollback()
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			total += len(ids)
		}
		return ctx.Err()
	}
	if err := del(`ts < ?`, ix.now().Add(-ix.Retention).UnixMilli()); err != nil {
		return total, err
	}
	// the segments the keeper's janitor removed
	rows, err := ix.db.QueryContext(ctx, `SELECT seg FROM positions`)
	if err != nil {
		return total, err
	}
	var gone []string
	for rows.Next() {
		var seg string
		if rows.Scan(&seg) == nil {
			if p, _ := ix.segFile(seg); p == "" {
				gone = append(gone, seg)
			}
		}
	}
	_ = rows.Close()
	for _, seg := range gone {
		if err := del(`seg = ?`, seg); err != nil {
			return total, err
		}
		_, _ = ix.db.ExecContext(ctx, `DELETE FROM positions WHERE seg = ?`, seg)
	}
	for _, lvl := range []int{LevelNum("debug"), LevelNum("info")} {
		for ix.MaxBytes > 0 && ix.Size() > ix.MaxBytes && ctx.Err() == nil {
			var oldest int64
			if err := ix.db.QueryRowContext(ctx, `SELECT MIN(ts) FROM lines WHERE level <= ?`, lvl).Scan(&oldest); err != nil || oldest == 0 {
				break
			}
			// an hour at a time, oldest first
			if err := del(`level <= ? AND ts < ?`, lvl, oldest+int64(time.Hour/time.Millisecond)); err != nil {
				return total, err
			}
			_, _ = ix.db.ExecContext(ctx, `PRAGMA incremental_vacuum(2000)`)
			_, _ = ix.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
		}
	}
	if err := ix.sweepErrors(ctx, ix.ErrorRetention); err != nil {
		return total, err
	}
	if err := ix.Rollup(ctx); err != nil {
		return total, err
	}
	if err := ix.sweepMetrics(ctx); err != nil {
		return total, err
	}
	if total > 0 {
		_, _ = ix.db.ExecContext(ctx, `PRAGMA incremental_vacuum(2000)`)
	}
	return total, nil
}

// Serve runs the role: ingest every Poll, sweep hourly, and answer on ln until ctx ends.
func (ix *Index) Serve(ctx context.Context, ln net.Listener, poll time.Duration) error {
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	var ready atomic.Bool
	go func() {
		t := time.NewTicker(poll)
		defer t.Stop()
		sweep := time.NewTicker(time.Hour)
		defer sweep.Stop()
		if _, err := ix.Sweep(ctx); err != nil {
			ix.Log("log_index_sweep_failed", "err", err)
		}
		for {
			if _, err := ix.Ingest(ctx); err != nil {
				ix.Log("log_index_ingest_failed", "err", err)
			} else {
				ready.Store(true)
			}
			select {
			case <-ctx.Done():
				return
			case <-sweep.C:
				if _, err := ix.Sweep(ctx); err != nil {
					ix.Log("log_index_sweep_failed", "err", err)
				}
			case <-t.C:
			}
		}
	}()
	// the dashboards' metrics (story 53.12)
	if len(ix.MetricsURLs) > 0 {
		go ix.scrapeLoop(ctx)
	}
	srv := &http.Server{Handler: ix.Handler(&ready), ReadHeaderTimeout: 10 * time.Second}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// MaxTails is how many live tails may be open at once.
const MaxTails = 10

// Handler serves /healthz, /readyz, /search, /tail and /around (loopback: the API
// proxies them to admins).
func (ix *Index) Handler(ready *atomic.Bool) http.Handler {
	var tails atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ready != nil && !ready.Load() {
			http.Error(w, "indexing", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		res, err := ix.Search(r.Context(), q)
		if err != nil {
			code := http.StatusInternalServerError
			if errors.Is(err, ErrQuery) {
				code = http.StatusBadRequest
			}
			writeErr(w, code, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("/around", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("id"))
			return
		}
		n := func(k string) int {
			v, _ := strconv.Atoi(r.URL.Query().Get(k))
			if v <= 0 || v > 200 {
				return 25
			}
			return v
		}
		res, err := ix.Around(r.Context(), id, n("before"), n("after"))
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("/tail", func(w http.ResponseWriter, r *http.Request) {
		q, err := parseQuery(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if tails.Add(1) > MaxTails {
			tails.Add(-1)
			writeErr(w, http.StatusTooManyRequests, errors.New("too many live tails"))
			return
		}
		defer tails.Add(-1)
		ix.tail(w, r, q)
	})
	ix.errorRoutes(mux)
	mux.HandleFunc("GET /dashboards/{board}", func(w http.ResponseWriter, r *http.Request) {
		rng := r.URL.Query().Get("range")
		if rng == "" {
			rng = "24h"
		}
		doc, err := ix.Board(r.Context(), r.PathValue("board"), rng)
		if errors.Is(err, ErrBoard) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, doc)
	})
	return mux
}

// tail streams matching lines as they are indexed (Server-Sent Events), from the newest
// row now; a heartbeat every 15 s.
func (ix *Index) tail(w http.ResponseWriter, r *http.Request, q Query) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("no streaming"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	_ = ix.db.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(id), 0) FROM lines`).Scan(&q.AfterID)
	q.Cursor, q.Limit = "", 500
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	_, _ = fmt.Fprint(w, ": tail\n\n")
	fl.Flush()
	for {
		woke := ix.wait()
		res, err := ix.Search(r.Context(), q)
		if err == nil {
			for i, l := range res.Lines {
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", res.IDs[i], l)
				if res.IDs[i] > q.AfterID {
					q.AfterID = res.IDs[i]
				}
			}
			if len(res.Lines) > 0 {
				fl.Flush()
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			fl.Flush()
		case <-woke:
		}
	}
}

func parseQuery(r *http.Request) (Query, error) {
	v := r.URL.Query()
	q := Query{Q: v.Get("q"), Service: v.Get("service"), Component: v.Get("component"), Level: v.Get("level"),
		Node: v.Get("node"), RequestID: v.Get("request_id"), JobID: v.Get("job_id"), UserH: v.Get("user_h"),
		Cursor: v.Get("cursor")}
	for k, dst := range map[string]*time.Time{"since": &q.Since, "until": &q.Until} {
		if s := v.Get(k); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return q, fmt.Errorf("%s: %w", k, ErrQuery)
			}
			*dst = t
		}
	}
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return q, fmt.Errorf("limit: %w", ErrQuery)
		}
		q.Limit = n
	}
	return q, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
