package health

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"time"
)

// Check is the contract a readiness probe target satisfies. A check is
// allowed to consult the *bounded* state of a single dependency — never
// to fan out into work or hold a long-running lock. The 200 ms per-check
// budget enforced by Ready.ServeHTTP keeps a misbehaving check from
// turning the readiness probe into a synthetic load test against the
// dep.
type Check interface {
	Name() string
	Run(ctx context.Context) error
}

// CheckFunc adapts a plain function to the Check interface.
type CheckFunc struct {
	N string
	F func(ctx context.Context) error
}

func (c CheckFunc) Name() string                  { return c.N }
func (c CheckFunc) Run(ctx context.Context) error { return c.F(ctx) }

// DBPing pings a database/sql connection pool. AC2: "DB connection pool
// has ≥ 1 healthy conn." A successful PingContext returns iff the pool
// either already holds a healthy conn or can dial a fresh one inside
// the context deadline.
type DBPing struct {
	DB *sql.DB
	// N overrides the default check name "db". Use when a service has
	// more than one pool (e.g. read-replica fan-out).
	N string
}

func (c *DBPing) Name() string {
	if c.N != "" {
		return c.N
	}
	return "db"
}

func (c *DBPing) Run(ctx context.Context) error {
	if c.DB == nil {
		return errors.New("db pool is nil")
	}
	return c.DB.PingContext(ctx)
}

// TCPDial is a fallback peer-reachability check that doesn't require a
// gRPC client to be wired in. Plan §3 sketches a GRPCPing using
// connectivity.State; we keep this lighter variant for the stub server
// stage of stories 22.x where gRPC clients aren't constructed yet.
// Dials and immediately closes — never holds the conn open.
type TCPDial struct {
	N    string
	Addr string
}

func (c *TCPDial) Name() string {
	if c.N != "" {
		return c.N
	}
	return "tcp"
}

func (c *TCPDial) Run(ctx context.Context) error {
	if c.Addr == "" {
		return errors.New("tcp addr is empty")
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.Addr, err)
	}
	_ = conn.Close()
	return nil
}

// PerCheckTimeout is the upper bound enforced on each individual
// check's context. Plan §8 calls for 200 ms per check inside an 800 ms
// cumulative budget — the cumulative bound is enforced by the parent
// Ready handler; this constant is exported so the same budget applies
// to ad-hoc Check invocations from tests.
const PerCheckTimeout = 200 * time.Millisecond
