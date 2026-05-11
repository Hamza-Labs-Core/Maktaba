# Implementation Plan — Story 25.11 Bandwidth metering & accounting

> Companion to [story-25-11-bandwidth-metering.md](story-25-11-bandwidth-metering.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Where counted | Inside the relay proxy (25.9) at the io.Reader/Writer boundary. Both directions counted as bytes pass `w.Write` (out) and request body read (in). Headers excluded. |
| Hot store | Redis hash per `(server_id, utc_date)` with two fields `in` and `out`. Auto-expires 48h after the UTC day ends (to allow late flush). Also writes hourly key `bw:hourly:{sid}:{hour}` (24-key rolling window) consumed by abuse detection (25.25). |
| Cold store | Postgres `bandwidth_samples(server_id, bucket_start, bytes_in, bytes_out)` with `(server_id, bucket_start)` PK (5-min granularity, source of truth for billing). Flushed every 60s. |
| Stream gauge | Redis hash `streams:{server_id}` indexed by `stream_id`; cleaned by reaper at 90s inactivity. The active-stream Postgres mirror is best-effort only. |
| Monthly rollup | Cron `0 10 1 * *` (00:10 UTC on day 1): aggregate prior month into `bandwidth_monthly`. |
| Read API | `GET /api/me/usage`, `GET /api/servers/{id}/usage`, `GET /api/admin/usage`. |
| Out of scope | Tier enforcement (25.12 consumes counters). Per-IP abuse counters (25.25). |

## 1. Migration `00050001_bandwidth.sql` (slot 0005 per README)

```sql
-- +goose Up
CREATE TABLE bandwidth_samples (
    server_id     UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    bucket_start  TIMESTAMPTZ NOT NULL,                  -- 5-min UTC bucket
    user_id       UUID,                                  -- denormalized for fast per-user queries; nullable post-purge
    bytes_in      BIGINT NOT NULL DEFAULT 0,
    bytes_out     BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, bucket_start)
);
CREATE INDEX bandwidth_samples_bucket_idx ON bandwidth_samples(bucket_start);
CREATE INDEX bandwidth_samples_user_idx ON bandwidth_samples(user_id, bucket_start DESC);

CREATE TABLE bandwidth_monthly (
    server_id        UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    month            DATE NOT NULL,                       -- first-of-month UTC
    bytes_in         BIGINT NOT NULL DEFAULT 0,
    bytes_out        BIGINT NOT NULL DEFAULT 0,
    peak_concurrent_streams INTEGER NOT NULL DEFAULT 0,
    over_limit_at    TIMESTAMPTZ,
    PRIMARY KEY (server_id, month)
);
CREATE INDEX bandwidth_monthly_month_idx ON bandwidth_monthly(month);

CREATE TABLE streams_active (
    server_id     UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL,
    stream_id     TEXT NOT NULL,        -- relay-allocated; opaque
    opened_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    bytes_so_far  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (server_id, stream_id)
);
CREATE INDEX streams_active_last_seen_idx ON streams_active(last_seen_at);
CREATE INDEX streams_active_user_idx ON streams_active(user_id);

-- +goose Down
DROP TABLE IF EXISTS streams_active, bandwidth_monthly, bandwidth_samples;
```

## 2. Hot-path meter (relay)

```go
// cloud/internal/relay/meter.go
type Meter struct {
    redis *redis.Client
    clock clock.Clock
}

type bodyCounter struct {
    serverID, userID uuid.UUID
    dir              string   // "in" | "out"
    n                atomic.Int64
    m                *Meter
}

func (m *Meter) NewBodyCounter(s, u uuid.UUID, dir string) *bodyCounter {
    return &bodyCounter{serverID: s, userID: u, dir: dir, m: m}
}

func (b *bodyCounter) Add(n int64) {
    if n <= 0 { return }
    b.n.Add(n)
    // Cheap: just touch the hot Redis key. Real flush is in 60s job.
    date := b.m.clock.Now().UTC().Format("2006-01-02")
    key := fmt.Sprintf("bw:%s:%s", b.serverID, date)
    if err := b.m.redis.HIncrBy(context.Background(), key, b.dir, n).Err(); err != nil {
        meterDropped.Inc()
    }
}

func (b *bodyCounter) Flush(ctx context.Context, streamID string) {
    // Update last_seen_at + bytes_so_far in streams_active.
    b.m.redis.HSet(ctx, "streams:"+b.serverID.String(), streamID, encodeStreamState(b.n.Load(), b.m.clock.Now()))
}
```

Wrap response and request bodies with counting reader/writer:

```go
type meteredReader struct{ r io.Reader; c *bodyCounter }
func (m *meteredReader) Read(p []byte) (int, error) {
    n, err := m.r.Read(p); m.c.Add(int64(n)); return n, err
}

type meteredWriter struct{ w io.Writer; c *bodyCounter }
func (m *meteredWriter) Write(p []byte) (int, error) {
    n, err := m.w.Write(p); m.c.Add(int64(n)); return n, err
}
```

In `Proxy.serve` (25.9): wrap `r.Body` with `meteredReader` for `bytes_in`, wrap response writer's writes with `meteredWriter` for `bytes_out`. Headers byte-counted separately and discarded — they're not added.

## 3. Stream registration

```go
func (m *Meter) OpenStream(ctx context.Context, s, u uuid.UUID, isStream bool) (release func(), err error) {
    if !isStream { return func(){}, nil }
    streamID := uuid.NewString()
    key := "streams:" + s.String()
    m.redis.HSet(ctx, key, streamID, encodeStreamState(0, time.Now()))
    m.redis.Expire(ctx, key, 6*time.Hour)
    return func() {
        m.redis.HDel(context.Background(), key, streamID)
    }, nil
}
```

`isStream` = matches stream path patterns (see 25.9). The cap on concurrent streams (25.12) checks `HLEN streams:{server_id}` before opening.

## 4. Flush worker (every 60s)

```go
// cloud/internal/jobs/flush_bandwidth.go
type FlushJob struct{ db *pgxpool.Pool; redis *redis.Client; clock clock.Clock }

func (j *FlushJob) Run(ctx context.Context) error {
    t := time.NewTicker(60 * time.Second); defer t.Stop()
    for {
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-t.C:
            if err := j.tick(ctx); err != nil { slog.Error("flush_bandwidth", "err", err) }
        }
    }
}

func (j *FlushJob) tick(ctx context.Context) error {
    // Iterate the bw:* keyspace. SCAN with MATCH bw:* COUNT 500.
    iter := j.redis.Scan(ctx, 0, "bw:*", 500).Iterator()
    for iter.Next(ctx) {
        key := iter.Val()
        // Atomic flush: HGETALL then DELETE the deltas via HINCRBY -n.
        h, err := j.redis.HGetAll(ctx, key).Result()
        if err != nil { continue }
        bin, _ := strconv.ParseInt(h["in"], 10, 64)
        bout, _ := strconv.ParseInt(h["out"], 10, 64)
        // Re-subtract what we read so concurrent writes accumulate from zero
        if bin == 0 && bout == 0 { continue }
        if err := j.redis.HIncrBy(ctx, key, "in", -bin).Err(); err != nil { continue }
        if err := j.redis.HIncrBy(ctx, key, "out", -bout).Err(); err != nil { continue }
        // key shape: bw:<server_id>:<YYYY-MM-DD>
        parts := strings.SplitN(strings.TrimPrefix(key, "bw:"), ":", 2)
        if len(parts) != 2 { continue }
        sid, err := uuid.Parse(parts[0]); if err != nil { continue }
        date, _ := time.Parse("2006-01-02", parts[1])
        // Resolve user_id (cached LRU; falls through to DB).
        uid, _ := j.userResolver.Resolve(ctx, sid)
        _, _ = j.db.Exec(ctx, `
            INSERT INTO bandwidth_samples(server_id, date, user_id, bytes_in, bytes_out, updated_at)
            VALUES ($1,$2,$3,$4,$5, now())
            ON CONFLICT (server_id, date) DO UPDATE
              SET bytes_in = bandwidth_samples.bytes_in + EXCLUDED.bytes_in,
                  bytes_out = bandwidth_samples.bytes_out + EXCLUDED.bytes_out,
                  updated_at = now()
        `, sid, date, uid, bin, bout)
    }
    return iter.Err()
}
```

Delta-subtract design ensures concurrent writes don't lose counts: a write during flush stays in Redis and is picked up by the next tick.

## 5. Stale stream reaper (every 30s)

```go
func ReapStaleStreams(ctx context.Context, redisCli *redis.Client, db *pgxpool.Pool, clock clock.Clock) error {
    iter := redisCli.Scan(ctx, 0, "streams:*", 500).Iterator()
    for iter.Next(ctx) {
        key := iter.Val()
        h, _ := redisCli.HGetAll(ctx, key).Result()
        for streamID, raw := range h {
            bytes, lastSeen := decodeStreamState(raw)
            if clock.Now().Sub(lastSeen) > 90*time.Second {
                redisCli.HDel(ctx, key, streamID)
                // Final bytes were already accounted via meteredWriter; nothing to flush.
                _, _ = bytes  // unused; logging in real impl
            }
        }
    }
    return nil
}
```

## 6. Monthly rollup (cron)

```go
func RollupMonth(ctx context.Context, db *pgxpool.Pool, ym string) error {
    // Acquire advisory lock to prevent dual runs (idempotency safety net).
    _, _ = db.Exec(ctx, `SELECT pg_advisory_xact_lock(8472613)`)
    _, err := db.Exec(ctx, `
        INSERT INTO bandwidth_monthly (user_id, year_month, bytes_in, bytes_out, peak_concurrent_streams)
        SELECT user_id, $1::TEXT,
               COALESCE(SUM(bytes_in), 0),
               COALESCE(SUM(bytes_out), 0),
               0   -- peak filled by separate query (see below)
        FROM bandwidth_samples
        WHERE date >= ($1 || '-01')::DATE
          AND date < ($1 || '-01')::DATE + INTERVAL '1 month'
        GROUP BY user_id
        ON CONFLICT (user_id, year_month) DO UPDATE
          SET bytes_in = EXCLUDED.bytes_in,
              bytes_out = EXCLUDED.bytes_out
    `, ym)
    if err != nil { return err }
    // peak concurrent streams: pre-aggregated from observability time-series; v1 stub 0.
    return nil
}
```

Idempotent: rerunning the same `ym` overwrites with the same values.

## 7. Read API

```go
// GET /api/me/usage?from=YYYY-MM-DD&to=YYYY-MM-DD
func meUsage(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        uid := currentUserID(r)
        from, to := parseRange(r, 30)
        rows, _ := s.repo.UsageByUser(r.Context(), uid, from, to)
        writeJSON(w, 200, map[string]any{"days": rows})
    }
}
```

Query (cache via 60s pgbouncer):

```sql
SELECT date, SUM(bytes_in) AS bin, SUM(bytes_out) AS bout
FROM bandwidth_samples
WHERE user_id = $1 AND date BETWEEN $2 AND $3
GROUP BY date ORDER BY date;
```

Index `bandwidth_samples(user_id, date DESC)` keeps p95 < 200ms.

## 8. Test plan

### 8.1 Unit

| Test | Pins |
|---|---|
| `TestCounterAdd1k1MB` | Total exactly 1 GiB; concurrent writes safe via atomic. |
| `TestRedisHIncrByDeltaModel` | Flush subtract → concurrent writes during flush conserved. |
| `TestStaleReaperWindow` | 89s OK; 91s reaped. |
| `TestHeaderBytesExcluded` | mock request with only headers → counter +0. |
| `TestOverflowDefenseInt64` | 2 GiB single transfer ok; no int wrap. |

### 8.2 Integration (Postgres + Redis)

| Test | Pins |
|---|---|
| `Test100MBHLSChunkFlush` | 100 MB out; within 60s the daily row equals 100 MB. |
| `TestRedisFailoverPreservesService` | Stop Redis; meter writes drop (counted in metric); requests still complete. |
| `TestMonthlyRollupIdempotent` | Run twice; second is no-op. |
| `TestUsageEndpointP95Under200ms` | 30d window query benchmarked. |
| `TestClientRSTMidBody` | Partial bytes accounted accurately. |
| `TestDSTBoundaryRollup` | UTC rows unchanged across DST. |
| `TestSuspendedMidStream` | Subscription cancel mid-stream → stream completes; bytes counted; final invoice includes them. |

## 9. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| Counter accuracy vs consistency | Eventual; <60s drift acceptable; monthly rollup rounds up to nearest MiB. | Doc. |
| Negative deltas | Refuse; alarm on metric `bw_negative_delta`. | Reaper. |
| Per-IP abuse counters | Distinct path (25.25). | Cross-story. |
| Server-reported bandwidth | Ignored for billing. | Spec. |
| Refunds | Not auto-applied; admin path in 25.21. | Cross-story. |
| Suspended mid-stream | Open stream completes; next blocked. | `TestSuspendedMidStream`. |
| Cross-month stream | Bytes attributed to date when relayed; spans days/months naturally. | Spec. |
| Free-tier counter | Counter runs (so UI shows 0 GB); enforcement at 25.12. | Spec. |
| GDPR delete | `user_id` nulled after 90d post-delete; daily rows dropped 90d after. | Cross-story 25.5. |
| Redis cluster failover | Sentinel replicates Lua + counters; loss bounded to <5s. | Ops doc. |
| Header bytes | Excluded from counters. | `TestHeaderBytesExcluded`. |

## 10. Dependencies

- 25.1 (foundation).
- 25.6 (servers).
- 25.8 (tunnel registry; streams_active uses server_id).
- 25.9 (meter wired into proxy reader/writer).
- 25.5 (GDPR data retention).
- 25.21 (admin reporting consumes monthly rollups).

## 11. Acceptance checklist

- [ ] Migration 00050001 (bandwidth) applies.
- [ ] Meter wraps proxy body streams.
- [ ] Redis flush every 60s; delta-subtract model.
- [ ] Stale stream reaper at 30s; 90s window.
- [ ] Monthly rollup idempotent.
- [ ] `/api/me/usage` + `/api/servers/{id}/usage` + admin endpoint.
- [ ] Counter metrics: `bandwidth_counter_dropped_total`, etc.
- [ ] Tests in §8 pass.
