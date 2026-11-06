//go:build integration

package relay

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/billing"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/db/dbtest"
	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/server"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/stores"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/migrations"
)

// Story 50.05 on Postgres, with the database cut off under a running relay
// (dbtest.Cut: what a Patroni failover looks like from the relay).

// blipWorld: a relay (cache, meter, registry) on a cuttable database, and a
// simulated home whose servers row is real.
type blipWorld struct {
	pool    *db.Pool // through the cut
	direct  *db.Pool // around it, for the test's own reads
	cut     *dbtest.Cut
	url     string
	srv     *httptest.Server
	tunnels *httptest.Server
	cache   *HomeCache
	meter   *billing.Meter
	counted atomic.Int64
	sv      stores.Server
	home    *simhome.Home
}

// countingMeter sums what the relay records, then meters for real.
type countingMeter struct {
	*billing.Meter
	n *atomic.Int64
}

func (c countingMeter) Record(ctx context.Context, sv stores.Server, in, out int64) {
	c.n.Add(in + out)
	c.Meter.Record(ctx, sv, in, out)
}

// realHome authenticates simulated home 1 as the real servers row.
type realHome struct {
	*simhome.Store
	sv stores.Server
}

func (h realHome) Authenticate(ctx context.Context, id, secret string) (stores.Server, error) {
	if _, err := h.Store.Authenticate(ctx, id, secret); err != nil {
		return stores.Server{}, err
	}
	return h.sv, nil
}

func newBlipWorld(t *testing.T) *blipWorld {
	t.Helper()
	direct, url := dbtest.MigratedURL(t)
	cutURL, cut := dbtest.Cuttable(t, url)
	pool, err := db.Open(cutURL, 8, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	ctx := context.Background()
	u, err := stores.NewUsers(direct.DB).Create(ctx, uuid.NewString()+"@example.test", "x", "B", true)
	if err != nil {
		t.Fatal(err)
	}
	_, _, slug := simhome.Creds(1)
	var sv stores.Server
	if err := direct.DB.QueryRowContext(ctx, `INSERT INTO servers (owner_user_id, name, slug, server_secret_hash, plan)
	        VALUES ($1, 'b', $2, 'x', 'pro') RETURNING id`, u.ID, slug).Scan(&sv.ID); err != nil {
		t.Fatal(err)
	}
	sv.Slug = slug
	w := &blipWorld{pool: pool, direct: direct, cut: cut, url: cutURL, sv: sv}
	servers := stores.NewServers(pool.DB)
	w.cache = &HomeCache{Lookup: servers}
	w.meter = billing.NewMeter(pool.DB)
	w.meter.FlushInterval, w.meter.RetryInterval = 300*time.Millisecond, 200*time.Millisecond
	w.meter.Start(ctx)
	t.Cleanup(w.meter.Close)
	reg := relaypkg.NewRegistry()
	deps := &Deps{Registry: reg, Cache: w.cache, Meter: countingMeter{w.meter, &w.counted}, PublicHost: "relay.test",
		HoldMax: -1, Logger: slog.New(slog.DiscardHandler)}
	w.srv = httptest.NewServer(deps.Handler())
	t.Cleanup(w.srv.Close)
	w.tunnels = httptest.NewServer(&relaypkg.TunnelServer{Registry: reg, Store: realHome{simhome.NewStore(), sv}, Logger: slog.New(slog.DiscardHandler)})
	t.Cleanup(w.tunnels.Close)
	api := simhome.API()
	t.Cleanup(api.Close)
	w.home = simhome.Start(1, "ws"+w.tunnels.URL[len("http"):]+"/v1/relay/ws", api.URL, simhome.Options{})
	t.Cleanup(w.home.Stop)
	waitOn(t, "the home online", func() bool { _, err := reg.Lookup(slug); return err == nil })
	return w
}

func (w *blipWorld) get(t *testing.T, slug, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, w.srv.URL+path, nil)
	req.Host = slug + ".relay.test"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, b
}

// T05: the database is cut for 4 s while the home streams: no request fails,
// none of them reads the database, and every byte is in bandwidth_monthly
// within the flush interval of the database's return. A home never seen
// gets 503 server_offline meanwhile.
func TestRelayRidesOutADatabaseCut(t *testing.T) {
	w := newBlipWorld(t)
	ctx := context.Background()
	if st, _ := w.get(t, w.sv.Slug, "/api/health"); st != 200 {
		t.Fatalf("before the cut: %d", st)
	}
	stop := make(chan struct{})
	var ok, failed atomic.Int64
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				st, body := w.get(t, w.sv.Slug, "/stream?bytes=50000&ms=50")
				if st == 200 && len(body) == 50000 {
					ok.Add(1)
				} else {
					failed.Add(1)
				}
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	w.cut.Down()
	misses := CacheCounts()[CacheMiss]
	time.Sleep(4 * time.Second)
	// a home this relay never served: it cannot tell who it is
	if st, body := w.get(t, "never-seen", "/api/health"); st != http.StatusServiceUnavailable {
		t.Errorf("an unknown home during the cut: %d %s", st, body)
	}
	if CacheCounts()[CacheMiss] != misses {
		t.Error("a request read the database during the cut")
	}
	w.cut.Up()
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
	if failed.Load() != 0 || ok.Load() == 0 {
		t.Fatalf("%d requests failed, %d ok, during the cut", failed.Load(), ok.Load())
	}
	// every byte the relay counted reaches the database soon after
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := w.meter.MonthlyBytes(ctx, w.sv.ID, billing.MonthStart(time.Now().UTC()))
		if err == nil && got == w.counted.Load() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bandwidth_monthly %d, the relay counted %d (err %v)", got, w.counted.Load(), err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("%d requests through a 4 s cut, 0 failed; %d bytes metered exactly", ok.Load(), w.counted.Load())
}

// T06: a plan change in the database reaches the next request within 1 s
// (the trigger's tier_changed drops the cached entry).
func TestPlanChangeReachesTheCache(t *testing.T) {
	w := newBlipWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := stores.Listen(ctx, w.url, []string{stores.ChannelTierChanged, stores.ChannelServerChanged}, nil,
		func(_, slug string) { w.cache.Invalidate(slug) }, w.cache.Expire); err != nil {
		t.Fatal(err)
	}
	if sv, err := w.cache.BySlug(ctx, w.sv.Slug); err != nil || sv.Plan != "pro" {
		t.Fatalf("cached: %+v %v", sv, err)
	}
	time.Sleep(200 * time.Millisecond) // the listener is up
	if _, err := w.direct.DB.ExecContext(ctx, `UPDATE servers SET plan = 'family' WHERE id = $1`, w.sv.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		sv, err := w.cache.BySlug(ctx, w.sv.Slug)
		if err == nil && sv.Plan == "family" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 1 s the relay still sees plan %q", sv.Plan)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// an owner's suspension drops it too (server_changed)
	if _, err := w.direct.DB.ExecContext(ctx, `UPDATE users SET status = 'suspended' WHERE id = (SELECT owner_user_id FROM servers WHERE id = $1)`, w.sv.ID); err != nil {
		t.Fatal(err)
	}
	waitOn(t, "the entry dropped", func() bool { return w.cache.Len() == 0 })
}

// T07: /readyz of the relay: the database away for 2 minutes with a warm
// cache is 200 degraded; past 5 minutes 503 db_unreachable.
func TestRelayReadinessDegrades(t *testing.T) {
	w := newBlipWorld(t)
	if _, err := w.cache.BySlug(context.Background(), w.sv.Slug); err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	rd := &server.LenientReadiness{Warm: w.cache.Warm}
	server.SetReadinessClock(rd, func() time.Time { return clock })
	mig := db.NewMigrator(w.pool, migrations.FS, ".")
	r := server.NewRouter(slog.New(slog.DiscardHandler), w.pool, mig, server.BuildInfo{}, nil, server.WithLenientReadiness(rd))
	rd.Every = time.Hour // the test runs each check itself
	probe := func() (int, map[string]any) {
		rd.Recheck(w.pool, mig)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if st, out := probe(); st != 200 || out["status"] != "ready" {
		t.Fatalf("up: %d %v", st, out)
	}
	w.cut.Down()
	if st, out := probe(); st != 200 || out["status"] != "degraded" || out["db"] != "unreachable" {
		t.Errorf("just cut: %d %v", st, out)
	}
	clock = clock.Add(2 * time.Minute)
	if st, out := probe(); st != 200 || out["status"] != "degraded" {
		t.Errorf("2 min: %d %v", st, out)
	}
	clock = clock.Add(4 * time.Minute)
	if st, out := probe(); st != http.StatusServiceUnavailable || out["reason"] != "db_unreachable" {
		t.Errorf("6 min: %d %v", st, out)
	}
	w.cut.Up()
	if st, out := probe(); st != 200 || out["status"] != "ready" {
		t.Errorf("back: %d %v", st, out)
	}
}
