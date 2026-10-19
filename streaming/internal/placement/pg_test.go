package placement

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

func beat(t *testing.T, pool *pgxpool.Pool, id, role string, capacity int) {
	t.Helper()
	_, err := cluster.PGBeatStore{Pool: pool}.Beat(context.Background(), id, cluster.BeatInfo{
		Host: id, Role: role, Capacity: capacity, Load: map[string]any{"protocol": 1}})
	if err != nil {
		t.Fatalf("beat %s: %v", id, err)
	}
}

func addChannel(t *testing.T, pool *pgxpool.Pool, n int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO channels (id, number, name, slug, mode)
		VALUES ($1, $2, $3, $4, 'shuffle')`, id, n, "C", "c-"+id.String()[:8]); err != nil {
		t.Fatal(err)
	}
	return id
}

// TC4 of 32.1: a node's database role writes only its own streaming_hosts row, cannot
// read the stream key nor write placements, and acks only its own placements.
func TestHostBeatOwnRowOnly(t *testing.T) {
	dsn, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	var hasGroup bool
	_ = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')`).Scan(&hasGroup)
	if !hasGroup {
		t.Skip("the migrating user cannot create roles (no maktaba_worker)")
	}
	if err := cluster.EnsureSecret(ctx, pool); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	nodeA, nodeB := uuid.NewString(), uuid.NewString()
	roleA, roleB := "e32a_"+suffix, "e32b_"+suffix
	for _, r := range [][2]string{{nodeA, roleA}, {nodeB, roleB}} {
		if _, err := pool.Exec(ctx, `CREATE ROLE `+r[1]+` LOGIN PASSWORD 'pw-`+suffix+`' IN ROLE maktaba_worker`); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO cluster_nodes (id, name, state, node_pubkey, db_role) VALUES ($1, $2, 'approved', 'k', $2)`, r[0], r[1]); err != nil {
			t.Fatal(err)
		}
	}
	// roles are cluster-wide: drop them (they own nothing and hold no grants of their own)
	t.Cleanup(func() {
		for _, r := range []string{roleA, roleB} {
			_, _ = pool.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, r)
			_, _ = pool.Exec(context.Background(), `DROP ROLE IF EXISTS `+r)
		}
	})
	a, err := pgxpool.New(ctx, pgtest.WithUser(t, dsn, roleA, "pw-"+suffix))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Ping(ctx); err != nil {
		t.Skipf("cannot log in as a machine role here: %v", err)
	}
	ans, err := cluster.PGBeatStore{Pool: a}.Beat(ctx, nodeA, cluster.BeatInfo{Host: "pc", Role: "home", Capacity: 3})
	if err != nil {
		t.Fatalf("own beat: %v", err)
	}
	if ans.Enabled || len(ans.Key()) != 32 {
		t.Errorf("a new node starts disabled and gets the 32-byte key: %+v", ans)
	}
	var role string
	_ = pool.QueryRow(ctx, `SELECT role FROM streaming_hosts WHERE host_id = $1`, nodeA).Scan(&role)
	if role != "node" {
		t.Errorf("a node claiming the home role is stored as %q", role)
	}
	if _, err := (cluster.PGBeatStore{Pool: a}).Beat(ctx, nodeB, cluster.BeatInfo{Host: "x"}); err == nil {
		t.Error("node A beat for node B")
	}
	var key []byte
	if err := a.QueryRow(ctx, `SELECT stream_key FROM tv_cluster_secret`).Scan(&key); err == nil {
		t.Error("a node read the stream key")
	}
	ch := addChannel(t, pool, 900)
	if _, err := a.Exec(ctx, `INSERT INTO channel_placements (channel_id, host_id, generation, state, reason) VALUES ($1, $2, 1, 'live', 'x')`, ch, nodeA); err == nil {
		t.Error("a node wrote a placement")
	}
	if _, err := a.Exec(ctx, `UPDATE streaming_hosts SET enabled = true WHERE host_id = $1`, nodeA); err == nil {
		t.Error("a node switched itself on")
	}
	// placements: read yes, ack only its own
	beat(t, pool, "home-1", "home", 4)
	store := PGStore{Pool: pool}
	p, err := store.Place(ctx, ch, func(*Placement, map[string]int) (Decision, error) {
		return Decision{Host: "home-1", Reason: ReasonFirstRequest}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := (PGStore{Pool: a}).Get(ctx, ch); err != nil {
		t.Errorf("a node cannot read placements: %v", err)
	}
	if _, err := (PGStore{Pool: a}).Ack(ctx, ch, p.Gen); err == nil {
		t.Error("a node acked the home's placement")
	}
	if ok, err := store.Ack(ctx, ch, p.Gen); err != nil || !ok {
		t.Errorf("home ack: %v %v", ok, err)
	}
}

// Plan 32.2 §4: the advisory lock and the sequence across two Service instances (two
// home processes by mistake, EC1): one generation per channel; capacity respected.
func TestPGPlaceTwoServices(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	beat(t, pool, "home-1", "home", 1)
	beat(t, pool, "node-1", "node", 1)
	if _, err := pool.Exec(ctx, `UPDATE streaming_hosts SET enabled = true`); err != nil {
		t.Fatal(err)
	}
	s := DefaultSettings()
	s.NodesStream = true
	mk := func() *Service {
		store := PGStore{Pool: pool}
		h := &Hosts{Source: store, Settings: StaticSettings(s), Protocol: 1}
		h.Refresh(ctx)
		return &Service{Store: store, Hosts: h, Settings: StaticSettings(s), Self: "home-1"}
	}
	s1, s2 := mk(), mk()
	ch := addChannel(t, pool, 1)
	var wg sync.WaitGroup
	gens := make(chan int64, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		svc := s1
		if i%2 == 1 {
			svc = s2
		}
		go func() {
			defer wg.Done()
			p, err := svc.Resolve(ctx, ch)
			if err != nil {
				t.Error(err)
				return
			}
			gens <- p.Gen
		}()
	}
	wg.Wait()
	close(gens)
	seen := map[int64]bool{}
	for g := range gens {
		seen[g] = true
	}
	if len(seen) != 1 {
		t.Errorf("generations %v, want one", seen)
	}
	// one slot left on the node: two channels at once from the two services
	a, b := addChannel(t, pool, 2), addChannel(t, pool, 3)
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, err := s1.Resolve(ctx, a); errs <- err }()
	go func() { defer wg.Done(); _, err := s2.Resolve(ctx, b); errs <- err }()
	wg.Wait()
	close(errs)
	fails := 0
	for err := range errs {
		if err != nil {
			fails++
		}
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM channel_placements WHERE host_id = 'node-1' AND state <> 'released'`).Scan(&n)
	if n != 1 || fails != 1 {
		t.Errorf("node placements %d, refused %d: want 1 and 1", n, fails)
	}
}

// TC3 of 32.5: stale channel_runtime rows are cleaned.
func TestStaleRuntimeCleanup(t *testing.T) {
	_, pool := pgtest.ScratchDB(t)
	ctx := context.Background()
	beat(t, pool, "home-1", "home", 4)
	store := PGStore{Pool: pool}
	live, orphan := addChannel(t, pool, 1), addChannel(t, pool, 2)
	if _, err := store.Place(ctx, live, func(*Placement, map[string]int) (Decision, error) {
		return Decision{Host: "home-1", Reason: ReasonFirstRequest}, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []uuid.UUID{live, orphan} {
		if _, err := pool.Exec(ctx, `INSERT INTO channel_runtime (channel_id, host, state, host_id, heartbeat_at)
			VALUES ($1, 'mac', 'live', 'home-1', now() - interval '5 minutes')`, ch); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.CleanStaleRuntime(ctx)
	if err != nil || n != 1 {
		t.Fatalf("cleaned %d %v, want 1", n, err)
	}
	var left uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT channel_id FROM channel_runtime`).Scan(&left); err != nil || left != live {
		t.Errorf("kept %v %v, want the placed channel", left, err)
	}
}
