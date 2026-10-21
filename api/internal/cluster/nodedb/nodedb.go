// Package nodedb is a full node's database for reads (specs/epics/37-node-replica):
// one *sql.DB whose connections go to this machine's own copy of the home's database
// while it answers, and to the home's otherwise, so a node reads locally whenever it
// can and still reads while its copy is being made or rebuilt. Writes never go
// through it: they are the home's (the node role's handle on the home's database).
package nodedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"log/slog"
	"sync/atomic"
	"time"
)

// Source is one database the reads can go to.
type Source struct {
	Name string // "local" or "home", for the log
	Conn driver.Connector
	// up: the last ping answered (unknown counts as up until a ping says otherwise)
	down atomic.Bool
}

// Router picks the source of every new connection: the local copy while it answers,
// else the home's, else the local copy again (the home gone and the copy not
// answering either: the error the caller gets is the copy's).
type Router struct {
	Local, Home *Source
	Logger      *slog.Logger

	usingHome atomic.Bool
}

var _ driver.Connector = (*Router)(nil)

// Connect implements driver.Connector.
func (r *Router) Connect(ctx context.Context) (driver.Conn, error) {
	pick, other := r.Local, r.Home
	if r.Local == nil || (r.Local.down.Load() && r.Home != nil && !r.Home.down.Load()) {
		pick, other = r.Home, r.Local
	}
	c, err := pick.Conn.Connect(ctx)
	if err != nil && other != nil && ctx.Err() == nil {
		pick.down.Store(true)
		if c2, err2 := other.Conn.Connect(ctx); err2 == nil {
			r.note(other)
			return c2, nil
		}
	}
	if err == nil {
		r.note(pick)
	}
	return c, err
}

// note logs when the reads move between the copy and the home.
func (r *Router) note(s *Source) {
	home := s == r.Home
	if r.usingHome.Swap(home) == home || r.Logger == nil {
		return
	}
	if home {
		r.Logger.Info("node: reads go to the home's database (this machine's copy does not answer)",
			"event", "node_reads_home")
	} else {
		r.Logger.Info("node: reads go to this machine's copy of the home's database", "event", "node_reads_local")
	}
}

// Driver implements driver.Connector.
func (r *Router) Driver() driver.Driver {
	if r.Local != nil {
		return r.Local.Conn.Driver()
	}
	return r.Home.Conn.Driver()
}

// Open is the router's *sql.DB; connections live at most maxLife, so the reads come
// back to the copy soon after it answers again.
func (r *Router) Open(maxLife time.Duration) *sql.DB {
	db := sql.OpenDB(r)
	if maxLife <= 0 {
		maxLife = time.Minute
	}
	db.SetConnMaxLifetime(maxLife)
	return db
}

// Watch pings each source every `every` (2 s each at most) until ctx ends, so a dead
// one is skipped at the next connection rather than waited on.
func (r *Router) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		r.CheckAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckAll pings each source once.
func (r *Router) CheckAll(ctx context.Context) {
	for _, s := range []*Source{r.Local, r.Home} {
		if s != nil {
			r.check(ctx, s)
		}
	}
}

func (r *Router) check(ctx context.Context, s *Source) {
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := s.Conn.Connect(pctx)
	if err == nil {
		if p, ok := c.(driver.Pinger); ok {
			err = p.Ping(pctx)
		}
		_ = c.Close()
	}
	if ctx.Err() != nil {
		return
	}
	was := s.down.Swap(err != nil)
	if was != (err != nil) && r.Logger != nil {
		if err != nil {
			r.Logger.Warn("node: a database the reads use does not answer", "source", s.Name, "err", err,
				"event", "node_db_down")
		} else {
			r.Logger.Info("node: a database the reads use answers again", "source", s.Name, "event", "node_db_up")
		}
	}
}

// Down reports whether a source's last check failed.
func (s *Source) Down() bool { return s != nil && s.down.Load() }
