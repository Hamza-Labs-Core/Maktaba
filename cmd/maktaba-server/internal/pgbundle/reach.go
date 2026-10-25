package pgbundle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// What the bundled Postgres opens to the cluster's machines follows the cluster
// (specs/epics/37-node-replica): it listens beyond this machine, and pg_hba.conf lets
// them in, only while a machine may connect (one is approved, or joins are open); and
// then only from the machines' networks and each approved machine's own address.

// ReachEvery is how often WatchReach looks at the cluster.
const ReachEvery = 30 * time.Second

// Reach is what the cluster asks of the network.
type Reach struct {
	// Open: a machine may connect (one is approved, or joins are open)
	Open bool `json:"open"`
	// Addrs are the approved machines' own addresses (the hosts they joined from)
	Addrs []string `json:"addrs,omitempty"`
	// Require: the home requires the cluster link (rules.cluster link_mode, story 35.16):
	// the machines' roles need their client certificates too
	Require bool `json:"require,omitempty"`
}

// reachSQL reads the Reach: joins open (their hour not up) or a machine approved, and
// the approved machines' addresses.
const reachSQL = `SELECT
  COALESCE((SELECT accept_joins AND (accept_joins_until IS NULL OR accept_joins_until > now())
              FROM instance_identity WHERE id = 1), false)
  OR EXISTS (SELECT 1 FROM cluster_nodes WHERE state = 'approved'),
  COALESCE((SELECT string_agg(DISTINCT remote_addr, ',' ORDER BY remote_addr) FROM cluster_nodes
             WHERE state = 'approved' AND remote_addr IS NOT NULL AND remote_addr <> ''), '');`

// requireSQL reads whether the home requires the cluster link (story 35.16): asked apart,
// so a database without the settings table (not migrated yet) still answers the rest.
const requireSQL = `SELECT COALESCE((SELECT value->>'link_mode' FROM app_settings WHERE key = 'rules.cluster'), '') = 'require';`

// reachFile keeps the last Reach the cluster asked for, which a start follows (none
// yet: this machine only).
func (b Bundle) reachFile() string { return filepath.Join(b.Data, "maktaba-reach.json") }

// frozenMarker marks a home frozen for a switch (or fenced, a newer home known): the
// machines' database roles are not let in (pg_hba.conf), their copies' replication roles
// are (a copy takes the switch's marker over it).
const frozenMarker = "maktaba-frozen"

// Frozen reports whether the machines' roles are kept out (SetFrozen).
func (b Bundle) Frozen() bool {
	_, err := os.Stat(filepath.Join(b.Data, frozenMarker))
	return err == nil
}

// SetFrozen keeps the machines' database roles out of this server (on) or lets them in
// again: pg_hba.conf written at once; the caller reloads the server (and ends the
// sessions open): a session may set itself read-write, a role not let in has none.
func (b Bundle) SetFrozen(on bool) error {
	path := filepath.Join(b.Data, frozenMarker)
	if on {
		if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
			return err
		}
	} else if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return b.writeHBA()
}

// LastReach is the Reach the cluster last asked for (none yet: closed).
func (b Bundle) LastReach() Reach {
	var r Reach
	if raw, err := os.ReadFile(b.reachFile()); err == nil {
		_ = json.Unmarshal(raw, &r)
	}
	return r
}

// KeepReach records r for the next start: a machine made the home by a switch opens to
// the machines at once (switchover.Promote), not one look later.
func (b Bundle) KeepReach(r Reach) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := b.reachFile() + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.reachFile())
}

// listensOut: the server listens beyond this machine ([postgres] listen not
// "loopback", and a machine may connect).
func (b Bundle) listensOut() bool { return b.LAN && b.LastReach().Open }

// machineAddrs are the approved machines' addresses as pg_hba.conf networks (one host
// each); this machine's own, and what does not parse, are left out.
func (r Reach) machineAddrs() []string {
	var out []string
	for _, a := range r.Addrs {
		ip := net.ParseIP(strings.TrimSpace(a))
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}
		bits := 128
		if ip4 := ip.To4(); ip4 != nil {
			ip, bits = ip4, 32
		}
		n := (&net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}).String()
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// reach reads what the cluster asks for now from the running server.
func (b Bundle) reach(ctx context.Context, q Querier) (Reach, error) {
	rows, err := q.Query(ctx, AppDatabase, reachSQL)
	if err != nil {
		return Reach{}, err
	}
	if len(rows) != 1 {
		return Reach{}, fmt.Errorf("the cluster's reach: %d rows", len(rows))
	}
	open, addrs, ok := strings.Cut(rows[0], "|")
	if !ok {
		return Reach{}, fmt.Errorf("the cluster's reach: %q", rows[0])
	}
	r := Reach{Open: open == "t"}
	if req, err := q.Query(ctx, AppDatabase, requireSQL); err == nil && len(req) == 1 {
		r.Require = strings.TrimSpace(req[0]) == "t"
	}
	if addrs = strings.TrimSpace(addrs); addrs != "" {
		r.Addrs = strings.Split(addrs, ",")
	}
	return r, nil
}

// WatchReach keeps what this server opens to the machines as the cluster asks (Reach),
// every interval: pg_hba.conf at once (a reload), and whether it listens beyond this
// machine by starting it again (restart), which a listen address needs. A look the
// database does not answer (its tables not made yet) changes nothing. With [postgres]
// listen = "loopback" it never opens.
func (b Bundle) WatchReach(ctx context.Context, interval time.Duration, restart func() error, log io.Writer) {
	if !b.LAN {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	said := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := b.followReach(ctx, restart, log)
		if ctx.Err() != nil {
			return
		}
		if msg := fmt.Sprint(err); err != nil && msg != said {
			fmt.Fprintf(log, "[postgres] what the machines may reach could not be checked (trying again): %v\n", err)
			said = msg
		} else if err == nil {
			said = ""
		}
	}
}

// maintenance counts the runs a restart of Postgres would break (the update's dump, its
// migrations, a restore: Hold); WatchReach leaves the server as it is while one runs.
var maintenance atomic.Int32

// Hold marks a maintenance run until the returned release is called: a change of
// what the machines may reach waits for it (the next look after it ends).
func Hold() (release func()) {
	maintenance.Add(1)
	var once sync.Once
	return func() { once.Do(func() { maintenance.Add(-1) }) }
}

// followReach is one look of WatchReach.
func (b Bundle) followReach(ctx context.Context, restart func() error, log io.Writer) error {
	if maintenance.Load() > 0 {
		return nil
	}
	q, err := b.NewQuerier()
	if err != nil {
		return err
	}
	qctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	now, err := b.reach(qctx, q)
	if err != nil {
		return err
	}
	was := b.LastReach()
	if now.Open == was.Open && slices.Equal(now.Addrs, was.Addrs) && now.Require == was.Require {
		return nil
	}
	if err := b.KeepReach(now); err != nil {
		return err
	}
	if err := b.writeHBA(); err != nil {
		return err
	}
	if now.Require != was.Require && now.Open {
		// the client certificates' CA is a start argument (ssl_ca_file): started again
		fmt.Fprintln(log, "[postgres] the cluster link is required now or no more: the machines' certificates follow (starting it again)")
		return restart()
	}
	if now.Open != was.Open {
		if now.Open {
			fmt.Fprintln(log, "[postgres] a machine may connect: listening for the cluster's machines (starting it again)")
		} else {
			fmt.Fprintln(log, "[postgres] no machine may connect: this machine only (starting it again)")
		}
		return restart()
	}
	return q.Exec(qctx, AppDatabase, "SELECT pg_reload_conf();")
}
