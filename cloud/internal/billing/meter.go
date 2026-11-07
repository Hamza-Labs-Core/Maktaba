package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Meter accumulates per-server bandwidth in-memory and flushes to
// Postgres on an interval. We intentionally trade write volume for a short
// window of in-memory bytes: a relay node serving 1000 connected servers at
// 1 req/s would otherwise produce one INSERT per request.
//
// Tier enforcement reads from the same in-memory counter so a server
// that has just blown its cap is rejected immediately without waiting
// for the flush.
//
// Riding out a database blip (Epic 50 story 50.05): a flush that fails
// keeps its counts and retries every RetryInterval, summing new bytes into
// the same 5-minute buckets; the buffer is bounded at MaxBuffered (server,
// bucket) entries, past which a server's buckets merge into its oldest, so
// bytes are coarsened, never dropped. Allow reads each server's persisted
// month total at most once a MonthCacheTTL and, while the database is away,
// uses the last total it read (a home near its cap can go over by the bytes
// of the outage, never more).
type Meter struct {
	DB            *sql.DB
	FlushInterval time.Duration
	RetryInterval time.Duration // after a failed flush (default 5 s)
	MonthCacheTTL time.Duration // default 60 s
	MaxBuffered   int           // (server, bucket) entries (default 1,000,000)
	// FinalWait is how long Close keeps trying to flush while the database is
	// down (default 30 s); what is left is logged as relay_meter_lost_bytes.
	FinalWait time.Duration
	Logger    *slog.Logger
	now       func() time.Time
	// writer writes a flush batch (nil: the database; tests fail it)
	writer func(ctx context.Context, id string, batch map[bucketKey]counter) error
	// inflight is the batch a failed flush tried to write: retried as it is, under its
	// id, so a commit that reached the database but whose answer was lost (a
	// connection cut at COMMIT) is not counted twice; new bytes wait in pending
	inflight *flushBatch

	mu       sync.Mutex
	counters map[string]*counter // since the last flush attempt, by server id
	pending  map[bucketKey]*counter
	months   map[string]monthTotal // persisted month totals read, by server id
	// dbDownUntil: a month read failed; until then Allow uses the totals it has
	// without asking (a request must not wait on a database that is away)
	dbDownUntil time.Time
	refreshing  map[string]bool // month reads in the background, by server id

	stop       chan struct{}
	stopped    chan struct{} // closed when the flush loop has made its last flush
	once       sync.Once
	closeOnce  sync.Once
	flushCount atomic.Int64
	lostBytes  atomic.Int64
}

type counter struct {
	BytesIn  int64
	BytesOut int64
}

type flushBatch struct {
	id      string
	entries map[bucketKey]counter
}

type bucketKey struct {
	server string
	bucket time.Time // 5-minute bucket start (UTC)
}

type monthTotal struct {
	month time.Time
	bytes int64
	at    time.Time
}

// NewMeter wires the meter to a DB pool. Caller must Start it.
func NewMeter(db *sql.DB) *Meter {
	return &Meter{
		DB:            db,
		FlushInterval: 30 * time.Second,
		RetryInterval: 5 * time.Second,
		MonthCacheTTL: 60 * time.Second,
		MaxBuffered:   1_000_000,
		FinalWait:     30 * time.Second,
		counters:      make(map[string]*counter),
		pending:       make(map[bucketKey]*counter),
		months:        make(map[string]monthTotal),
		stop:          make(chan struct{}),
		stopped:       make(chan struct{}),
		now:           func() time.Time { return time.Now().UTC() },
	}
}

func (m *Meter) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Start kicks off the flush goroutine. Idempotent.
func (m *Meter) Start(ctx context.Context) {
	m.once.Do(func() {
		go m.flushLoop(ctx)
	})
}

func (m *Meter) flushLoop(ctx context.Context) {
	defer close(m.stopped)
	retry := m.RetryInterval
	if retry <= 0 {
		retry = 5 * time.Second
	}
	wait := m.FlushInterval
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			m.finalFlush()
			return
		case <-m.stop:
			m.finalFlush()
			return
		case <-timer.C:
			if err := m.flush(ctx); err != nil {
				wait = retry
			} else {
				wait = m.FlushInterval
			}
			timer.Reset(wait)
		}
	}
}

// finalFlush keeps trying for FinalWait while the database is away, then
// logs what could not be written.
func (m *Meter) finalFlush() {
	limit := m.FinalWait
	if limit <= 0 {
		limit = 30 * time.Second
	}
	deadline := time.Now().Add(limit)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := m.flush(ctx)
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			in, out := m.Buffered()
			m.lostBytes.Add(in + out)
			m.logger().Error("billing: the meter's last flush failed; bytes unrecorded",
				"relay_meter_lost_bytes", in+out, "err", err, "event", "relay_meter_lost_bytes")
			return
		}
		time.Sleep(min(time.Second, time.Until(deadline)))
	}
}

// Close stops the flush loop and waits for its final flush (up to FinalWait
// and its last attempt): the relay calls it on shutdown, and a process that
// exits first loses the bytes counted since the last tick.
func (m *Meter) Close() {
	m.closeOnce.Do(func() { close(m.stop) })
	m.once.Do(func() { close(m.stopped) }) // never started: nothing to wait for
	limit := m.FinalWait
	if limit <= 0 {
		limit = 30 * time.Second
	}
	select {
	case <-m.stopped:
	case <-time.After(limit + 15*time.Second):
	}
}

// Record adds to the in-memory counters. Called from the relay handler
// after each proxied request.
func (m *Meter) Record(_ context.Context, sv stores.Server, in, out int64) {
	if in == 0 && out == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.counters[sv.ID]
	if !ok {
		c = &counter{}
		m.counters[sv.ID] = c
	}
	c.BytesIn += in
	c.BytesOut += out
}

// Allow is the pre-request gate. We approve unless the running total
// for this month already exceeds the tier cap plus FreeOverageGrace.
func (m *Meter) Allow(ctx context.Context, sv stores.Server) error {
	tier, ok := Tiers[sv.Plan]
	if !ok {
		tier = Tiers[PlanFree]
	}
	month := MonthStart(m.now())
	monthBytes := m.persistedMonth(ctx, sv.ID, month)
	m.mu.Lock()
	monthBytes += m.unflushedLocked(sv.ID, month)
	m.mu.Unlock()
	limit := tier.BandwidthBytesPerMo + FreeOverageGrace
	if monthBytes > limit {
		return ErrOverLimit
	}
	return nil
}

// persistedMonth is serverID's month total in bandwidth_monthly, read at most
// once a MonthCacheTTL; while the database errors, the last one read (0 if
// none: a home this relay knows keeps playing through a blip).
func (m *Meter) persistedMonth(ctx context.Context, serverID string, month time.Time) int64 {
	ttl := m.MonthCacheTTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	now := m.now()
	m.mu.Lock()
	mt, ok := m.months[serverID]
	down := now.Before(m.dbDownUntil)
	m.mu.Unlock()
	if ok && mt.month.Equal(month) {
		// read in the background past the TTL: a request never waits on a
		// database that is away (a vanished one makes connects hang for their
		// timeout: found by the rehearsal's db-blip)
		if now.Sub(mt.at) >= ttl && !down {
			m.refreshMonth(serverID, month)
		}
		return mt.bytes
	}
	if down {
		return 0
	}
	total, err := m.readMonth(ctx, serverID, month)
	if err != nil {
		return 0
	}
	return total
}

// refreshMonth reads serverID's month total in the background (one at a time
// per server).
func (m *Meter) refreshMonth(serverID string, month time.Time) {
	m.mu.Lock()
	if m.refreshing == nil {
		m.refreshing = map[string]bool{}
	}
	if m.refreshing[serverID] {
		m.mu.Unlock()
		return
	}
	m.refreshing[serverID] = true
	m.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = m.readMonth(ctx, serverID, month)
		m.mu.Lock()
		delete(m.refreshing, serverID)
		m.mu.Unlock()
	}()
}

// readMonth reads serverID's month total and keeps it, or notes the database
// away for 5 s.
func (m *Meter) readMonth(ctx context.Context, serverID string, month time.Time) (int64, error) {
	now := m.now()
	total, err := m.MonthlyBytes(ctx, serverID, month)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.dbDownUntil = now.Add(5 * time.Second)
		return 0, err
	}
	m.months[serverID] = monthTotal{month: month, bytes: total, at: now}
	return total, nil
}

// unflushedLocked is serverID's bytes this month not yet in the database.
func (m *Meter) unflushedLocked(serverID string, month time.Time) int64 {
	var n int64
	if c, ok := m.counters[serverID]; ok {
		n += c.BytesIn + c.BytesOut
	}
	for k, c := range m.pending {
		if k.server == serverID && MonthStart(k.bucket).Equal(month) {
			n += c.BytesIn + c.BytesOut
		}
	}
	if m.inflight != nil {
		for k, c := range m.inflight.entries {
			if k.server == serverID && MonthStart(k.bucket).Equal(month) {
				n += c.BytesIn + c.BytesOut
			}
		}
	}
	return n
}

// ErrOverLimit is the sentinel returned by Allow when a server has
// exhausted its plan's bandwidth for the current month.
var ErrOverLimit = errors.New("billing: monthly bandwidth limit reached")

// MonthlyBytes returns the persisted byte count for a given month.
func (m *Meter) MonthlyBytes(ctx context.Context, serverID string, month time.Time) (int64, error) {
	var in, out sql.NullInt64
	err := m.DB.QueryRowContext(ctx, `
        SELECT bytes_in, bytes_out FROM bandwidth_monthly
        WHERE server_id = $1 AND month = $2
    `, serverID, month).Scan(&in, &out)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return in.Int64 + out.Int64, nil
}

// Buffered is the bytes counted and not yet written (in, out).
func (m *Meter) Buffered() (in, out int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.counters {
		in, out = in+c.BytesIn, out+c.BytesOut
	}
	for _, c := range m.pending {
		in, out = in+c.BytesIn, out+c.BytesOut
	}
	if m.inflight != nil {
		for _, c := range m.inflight.entries {
			in, out = in+c.BytesIn, out+c.BytesOut
		}
	}
	return in, out
}

// BufferedEntries is how many (server, bucket) entries wait for a flush.
func (m *Meter) BufferedEntries() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.pending)
	if m.inflight != nil {
		n += len(m.inflight.entries)
	}
	return n
}

// LostBytes is what a final flush could not write (relay_meter_lost_bytes).
func (m *Meter) LostBytes() int64 { return m.lostBytes.Load() }

// stage moves the counters since the last attempt into their 5-minute
// bucket of the pending buffer, merging buckets past MaxBuffered.
func (m *Meter) stageLocked(now time.Time) {
	bucket := now.Truncate(5 * time.Minute)
	for id, c := range m.counters {
		k := bucketKey{server: id, bucket: bucket}
		p, ok := m.pending[k]
		if !ok {
			p = &counter{}
			m.pending[k] = p
		}
		p.BytesIn += c.BytesIn
		p.BytesOut += c.BytesOut
	}
	m.counters = make(map[string]*counter, len(m.counters))
	limit := m.MaxBuffered
	if limit <= 0 {
		limit = 1_000_000
	}
	if len(m.pending) > limit {
		m.coarsenLocked()
	}
}

// coarsenLocked merges each server's pending buckets into its oldest one:
// the per-server total stays exact, only the 5-minute detail is lost.
func (m *Meter) coarsenLocked() {
	oldest := map[string]time.Time{}
	for k := range m.pending {
		if t, ok := oldest[k.server]; !ok || k.bucket.Before(t) {
			oldest[k.server] = k.bucket
		}
	}
	merged := make(map[bucketKey]*counter, len(oldest))
	for k, c := range m.pending {
		mk := bucketKey{server: k.server, bucket: oldest[k.server]}
		t, ok := merged[mk]
		if !ok {
			t = &counter{}
			merged[mk] = t
		}
		t.BytesIn += c.BytesIn
		t.BytesOut += c.BytesOut
	}
	m.pending = merged
}

// flush writes a batch in one transaction: the one a failed flush left (as
// it was, under the same id), else the pending buffer as a new batch. A batch
// whose id the database already has is not written again: its commit had
// reached the database (found by the db-blip test: the database counted 800 KB
// more than the relay after a connection cut at COMMIT).
func (m *Meter) flush(ctx context.Context) error {
	// the batch left by a failed flush first, then what is pending: until nothing is left
	for {
		more, err := m.flushOne(ctx)
		if err != nil || !more {
			return err
		}
	}
}

// flushOne writes one batch; more reports whether one was written.
func (m *Meter) flushOne(ctx context.Context) (more bool, err error) {
	m.mu.Lock()
	m.stageLocked(m.now())
	if m.inflight == nil {
		if len(m.pending) == 0 {
			m.mu.Unlock()
			return false, nil
		}
		b := &flushBatch{id: uuid.NewString(), entries: make(map[bucketKey]counter, len(m.pending))}
		for k, c := range m.pending {
			b.entries[k] = *c
		}
		m.pending = make(map[bucketKey]*counter)
		m.inflight = b
	}
	batch := m.inflight
	m.mu.Unlock()

	write := m.writer
	if write == nil {
		write = m.write
	}
	if err := write(ctx, batch.id, batch.entries); err != nil {
		return false, err
	}
	// written: the batch is done, and its bytes count in the month totals read,
	// so Allow does not miss them until its next read
	m.mu.Lock()
	m.inflight = nil
	for k, c := range batch.entries {
		if mt, ok := m.months[k.server]; ok && mt.month.Equal(MonthStart(k.bucket)) {
			mt.bytes += c.BytesIn + c.BytesOut
			m.months[k.server] = mt
		}
	}
	m.mu.Unlock()
	m.flushCount.Add(1)
	return true, nil
}

func (m *Meter) write(ctx context.Context, id string, batch map[bucketKey]counter) error {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// the batch's id first: a batch already applied (its earlier COMMIT reached the
	// database, its answer did not reach us) is done
	res, err := tx.ExecContext(ctx, `INSERT INTO relay_meter_flushes (id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	// a day of ids is enough to recognise a retry
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_meter_flushes WHERE at < now() - interval '1 day'`); err != nil {
		return err
	}
	for k, c := range batch {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO bandwidth_samples (server_id, bucket_start, bytes_in, bytes_out)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT (server_id, bucket_start) DO UPDATE SET
                bytes_in  = bandwidth_samples.bytes_in  + EXCLUDED.bytes_in,
                bytes_out = bandwidth_samples.bytes_out + EXCLUDED.bytes_out
        `, k.server, k.bucket, c.BytesIn, c.BytesOut); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO bandwidth_monthly (server_id, month, bytes_in, bytes_out)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT (server_id, month) DO UPDATE SET
                bytes_in  = bandwidth_monthly.bytes_in  + EXCLUDED.bytes_in,
                bytes_out = bandwidth_monthly.bytes_out + EXCLUDED.bytes_out
        `, k.server, MonthStart(k.bucket), c.BytesIn, c.BytesOut); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FlushCount returns how many flushes have run. For tests + diagnostics.
func (m *Meter) FlushCount() int64 { return m.flushCount.Load() }

// WriteMetrics renders the meter's buffer on /metrics.
func (m *Meter) WriteMetrics(w io.Writer) error {
	_, err := fmt.Fprintf(w, "# HELP maktaba_relay_meter_buffered_entries Metering entries waiting for a database flush.\n"+
		"# TYPE maktaba_relay_meter_buffered_entries gauge\nmaktaba_relay_meter_buffered_entries %d\n"+
		"# HELP maktaba_relay_meter_lost_bytes_total Bytes a final flush could not write.\n"+
		"# TYPE maktaba_relay_meter_lost_bytes_total counter\nmaktaba_relay_meter_lost_bytes_total %d\n",
		m.BufferedEntries(), m.LostBytes())
	return err
}
