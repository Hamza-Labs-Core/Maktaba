package ratelimit

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// PG is a token bucket per subject that every api node shares, in Postgres
// (cloud_rate_buckets, Epic 50 story 50.17 §3): with three nodes behind a round-robin load
// balancer, a per-process limiter would give one address three times its limit. It guards
// the paths that face brute force (sign-in, password reset, mail, claim redeem, the console's
// sign-in), which see a few requests a second at most, so one UPSERT per request is cheap.
// The general per-IP abuse limits stay per process (Limiter).
//
// When the database can't answer, Fallback (this process's own bucket) decides: those paths
// need the database anyway, and a limit per node is still a limit.
type PG struct {
	DB       *sql.DB
	Name     string // the limit's name, the keys' prefix ("auth", "mail", "claim", ...)
	Capacity float64
	Rate     float64 // tokens per second
	Fallback *Limiter
	Logger   *slog.Logger
}

// NewPG returns a shared limiter with this burst capacity and refill rate (per second), and a
// per-process fallback with the same numbers.
func NewPG(db *sql.DB, name string, capacity, ratePerSec float64, logger *slog.Logger) *PG {
	return &PG{DB: db, Name: name, Capacity: capacity, Rate: ratePerSec,
		Fallback: NewLimiter(capacity, ratePerSec), Logger: logger}
}

// takeSQL refills the bucket for the time since its last take (up to the capacity) and takes
// one token when there is one, in one statement: concurrent requests on any node serialize on
// the row. A new key starts full, less the token taken. The answer is whether a row came
// back: ON CONFLICT ... WHERE writes and returns the row only when a token was there, so a
// refusal returns nothing and leaves the bucket as it was.
//
// (It used to return "updated_at = now()", reading an allowed take from the time it wrote. Two
// requests whose transactions start in the same microsecond share now(), so a refusal right
// after an allowed take read as allowed: 31 of 90 at once with a burst of 30.)
//
// A request whose transaction started before the bucket's last take (it waited on the row's
// lock) refills nothing and keeps the later updated_at: setting its own earlier now() would
// refill that gap a second time on the next take.
const takeSQL = `
INSERT INTO cloud_rate_buckets AS b (key, tokens, updated_at)
VALUES ($1, $2::float8 - 1, now())
ON CONFLICT (key) DO UPDATE SET
    tokens = LEAST($2::float8, b.tokens + GREATEST(0, EXTRACT(EPOCH FROM now() - b.updated_at)) * $3::float8) - 1,
    updated_at = GREATEST(b.updated_at, now())
WHERE LEAST($2::float8, b.tokens + GREATEST(0, EXTRACT(EPOCH FROM now() - b.updated_at)) * $3::float8) >= 1
RETURNING true`

// Allow takes a token from key's shared bucket and reports whether the request may go on.
func (p *PG) Allow(ctx context.Context, key string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ok, err := take(ctx, p.DB, p.Name+":"+key, p.Capacity, p.Rate)
	if err != nil {
		if p.Logger != nil {
			p.Logger.Warn("ratelimit: the shared bucket did not answer; this node's decides",
				"limit", p.Name, "err", err, "event", "ratelimit_shared_unavailable")
		}
		if p.Fallback == nil {
			return true
		}
		return p.Fallback.Allow(key)
	}
	return ok
}

// queryer is a *sql.DB or a *sql.Tx (the tests take inside one transaction).
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// take reports whether key's bucket had a token, and takes it. No row back is a refusal.
func take(ctx context.Context, q queryer, key string, capacity, rate float64) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, takeSQL, key, capacity, rate).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return ok, err
}

// RetryAfter is the whole seconds until one token refills, for a Retry-After header.
func (p *PG) RetryAfter() int {
	return (&Limiter{Rate: p.Rate}).retryAfter()
}

// Middleware is Limiter.Middleware with the shared bucket: keyFn picks the subject, "" skips
// the limit for that request.
func (p *PG) Middleware(keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := keyFn(r)
			if k == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !p.Allow(r.Context(), k) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", strconv.Itoa(p.RetryAfter()))
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate_limited"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SweepPG deletes the shared buckets idle longer than idle: any bucket idle that long has
// refilled, and a fresh one is the same. The worker runs it with its cleanup.
func SweepPG(ctx context.Context, db *sql.DB, idle time.Duration) (int64, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM cloud_rate_buckets WHERE updated_at < now() - make_interval(secs => $1)`, idle.Seconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
