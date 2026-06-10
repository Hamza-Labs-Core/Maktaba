package billing

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Meter accumulates per-server bandwidth in-memory and flushes to
// Postgres on an interval. We intentionally trade durability (5-minute
// window of in-flight bytes) for write volume: a relay node serving
// 1000 connected servers at 1 req/s would otherwise produce one
// INSERT per request.
//
// Tier enforcement reads from the same in-memory counter so a server
// that has just blown its cap is rejected immediately without waiting
// for the flush.
type Meter struct {
	DB            *sql.DB
	FlushInterval time.Duration
	now           func() time.Time

	mu       sync.Mutex
	counters map[string]*counter // keyed by server id

	stop       chan struct{}
	once       sync.Once
	flushCount atomic.Int64
}

type counter struct {
	BytesIn  int64
	BytesOut int64
}

// NewMeter wires the meter to a DB pool. Caller must Start it.
func NewMeter(db *sql.DB) *Meter {
	return &Meter{
		DB:            db,
		FlushInterval: 30 * time.Second,
		counters:      make(map[string]*counter),
		stop:          make(chan struct{}),
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// Start kicks off the flush goroutine. Idempotent.
func (m *Meter) Start(ctx context.Context) {
	m.once.Do(func() {
		go m.flushLoop(ctx)
	})
}

func (m *Meter) flushLoop(ctx context.Context) {
	tick := time.NewTicker(m.FlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = m.flush(context.Background())
			return
		case <-m.stop:
			_ = m.flush(context.Background())
			return
		case <-tick.C:
			_ = m.flush(ctx)
		}
	}
}

// Close stops the flush loop and emits a final flush.
func (m *Meter) Close() { close(m.stop) }

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
	// Read the persisted total for the current month, plus the
	// in-memory delta.
	monthBytes, err := m.MonthlyBytes(ctx, sv.ID, MonthStart(m.now()))
	if err != nil {
		return err
	}
	m.mu.Lock()
	if c, ok := m.counters[sv.ID]; ok {
		monthBytes += c.BytesIn + c.BytesOut
	}
	m.mu.Unlock()
	limit := tier.BandwidthBytesPerMo + FreeOverageGrace
	if monthBytes > limit {
		return ErrOverLimit
	}
	return nil
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

func (m *Meter) flush(ctx context.Context) error {
	m.mu.Lock()
	if len(m.counters) == 0 {
		m.mu.Unlock()
		return nil
	}
	snapshot := m.counters
	m.counters = make(map[string]*counter, len(snapshot))
	m.mu.Unlock()

	now := m.now()
	month := MonthStart(now)
	bucket := now.Truncate(5 * time.Minute)
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for id, c := range snapshot {
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO bandwidth_samples (server_id, bucket_start, bytes_in, bytes_out)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT (server_id, bucket_start) DO UPDATE SET
                bytes_in  = bandwidth_samples.bytes_in  + EXCLUDED.bytes_in,
                bytes_out = bandwidth_samples.bytes_out + EXCLUDED.bytes_out
        `, id, bucket, c.BytesIn, c.BytesOut); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
            INSERT INTO bandwidth_monthly (server_id, month, bytes_in, bytes_out)
            VALUES ($1, $2, $3, $4)
            ON CONFLICT (server_id, month) DO UPDATE SET
                bytes_in  = bandwidth_monthly.bytes_in  + EXCLUDED.bytes_in,
                bytes_out = bandwidth_monthly.bytes_out + EXCLUDED.bytes_out
        `, id, month, c.BytesIn, c.BytesOut); err != nil {
			return err
		}
	}
	m.flushCount.Add(1)
	return tx.Commit()
}

// FlushCount returns how many flushes have run. For tests + diagnostics.
func (m *Meter) FlushCount() int64 { return m.flushCount.Load() }
