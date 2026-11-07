//go:build integration

package relay

import (
	"context"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
)

// Story 50.04 T01 and T08 on Postgres: seq-ordered upserts, a Down that
// only removes its own row, lookups that skip this process and dead nodes,
// and the sweep of a node silent for 20 s.
func TestPGDirectory(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	users := stores.NewUsers(pool.DB)
	srvs := stores.NewServers(pool.DB)
	u, err := users.Create(ctx, "dir@example.test", "x", "D", true)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := srvs.MintClaim(ctx, u.ID)
	sv, err := srvs.RedeemClaim(ctx, code, "box", "dir-box", "h", "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	blue := NewPGDirectory(pool.DB, "a-blue", "127.0.0.1:8444")
	green := NewPGDirectory(pool.DB, "a-green", "127.0.0.1:8445")
	for _, d := range []*PGDirectory{blue, green} {
		if err := d.Start(ctx); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(blue.Up(ctx, "dir-box", sv.ID, 4))
	must(green.Up(ctx, "dir-box", sv.ID, 5))
	must(blue.Up(ctx, "dir-box", sv.ID, 3))
	must(blue.Down(ctx, "dir-box", 4))
	p, ok := blue.Lookup(ctx, "dir-box")
	if !ok || p.RelayID != "a-green" || p.Seq != 5 || p.Addr != "127.0.0.1:8445" {
		t.Fatalf("blue's lookup %+v %v, want a-green/5", p, ok)
	}
	if _, ok := green.Lookup(ctx, "dir-box"); ok {
		t.Error("green found itself")
	}
	// green dies: its heartbeat ages past 20 s; lookups skip it, a sweep removes its rows
	must2 := func(q string) {
		t.Helper()
		if _, err := pool.DB.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	must2(`UPDATE relay_nodes SET heartbeat_at = now() - interval '21 seconds' WHERE relay_id = 'a-green'`)
	blue.Forget("dir-box")
	if _, ok := blue.Lookup(ctx, "dir-box"); ok {
		t.Error("a dead node's tunnel was found")
	}
	must(blue.Heartbeat(ctx))
	var n int
	_ = pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM relay_tunnels WHERE relay_id = 'a-green'`).Scan(&n)
	if n != 0 {
		t.Errorf("a dead node's rows: %d", n)
	}
	_ = pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM relay_nodes WHERE relay_id = 'a-green'`).Scan(&n)
	if n != 0 {
		t.Errorf("a dead node: %d", n)
	}
	// a restarted process clears what its earlier run left
	must(blue.Up(ctx, "dir-box", sv.ID, 9))
	must(NewPGDirectory(pool.DB, "a-blue", "127.0.0.1:8444").Start(ctx))
	_ = pool.DB.QueryRowContext(ctx, `SELECT count(*) FROM relay_tunnels`).Scan(&n)
	if n != 0 {
		t.Errorf("a restarted relay's old rows: %d", n)
	}
}
