//go:build cloudrehearsal

package rehearsal

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relaypkg "github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relaydns"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/fakecloudflare"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/streamgen"
)

// The relay pool's scenarios (Epic 50 story 50.16, T08 and T09): three relay machines in a pool
// (tools/rehearsal-cloud/compose-relay-pool.yml), 1,000 simulated homes each holding a primary and a
// standby tunnel, a stream generator through the round-robin edge.

var poolIPs = map[string]string{"r1": "10.50.0.31", "r2": "10.50.0.32", "r3": "10.50.0.33"}

// upRelayPool starts the pool's cluster with services (the relays a scenario begins with).
func upRelayPool(t *testing.T, scenario string, services ...string) *cluster {
	t.Helper()
	c := &cluster{t: t, project: "mr-" + scenario, dir: filepath.Join(repoRoot(t), "tools/rehearsal-cloud"), base: "compose-relay-pool.yml"}
	ports := portsFor(c.project)
	c.edge = fmt.Sprintf("http://127.0.0.1:%d", ports[1])
	c.extra = []string{fmt.Sprintf("R1_PORT=%d", ports[1]+510), fmt.Sprintf("R2_PORT=%d", ports[1]+511), fmt.Sprintf("R3_PORT=%d", ports[1]+512)}
	_, _ = c.compose("down", "-v", "--remove-orphans")
	t.Cleanup(func() {
		c.stopHomes()
		if c.api != nil {
			c.api.close()
		}
		logs, _ := c.compose("logs", "--no-color", "--timestamps")
		_ = os.WriteFile(filepath.Join(outDir(t), scenario+".log"), []byte(logs), 0o644)
		if c.db != nil {
			_ = c.db.Close()
		}
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = c.compose("down", "-v", "--remove-orphans")
		}
	})
	c.must(append([]string{"up", "-d", "--wait"}, services...)...)
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://maktaba:rehearsal@127.0.0.1:%d/maktaba?sslmode=disable", ports[0]))
	if err != nil {
		t.Fatal(err)
	}
	c.db = db
	waitFor(t, time.Minute, "the edge", func() bool {
		resp, err := http.Get(c.edge + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == 200
	})
	api := simhome.API()
	c.api = &httptestServer{URL: api.URL, close: api.Close}
	return c
}

// relayPort is relay id's published port on the host.
func (c *cluster) relayPort(id string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(id, "r"))
	return portsFor(c.project)[1] + 509 + n
}

// resolve maps a member's tunnel host (r2.relay.test) to its published port.
func (c *cluster) resolve(host string) (string, bool) {
	id, ok := strings.CutSuffix(host, ".relay.test")
	if !ok || poolIPs[id] == "" {
		return "", false
	}
	return fmt.Sprintf("127.0.0.1:%d", c.relayPort(id)), true
}

// poolHomes seeds n homes and runs them, all dialling the edge (the stable name: any relay).
func (c *cluster) poolHomes(n int) []*simhome.Home {
	c.t.Helper()
	ids, err := seed.Homes(context.Background(), c.db, 0, n, "pro")
	if err != nil {
		c.t.Fatal(err)
	}
	ws := "ws" + strings.TrimPrefix(c.edge, "http") + "/v1/relay/ws"
	homes := make([]*simhome.Home, 0, n)
	for i := range n {
		_, _, slug := simhome.Creds(i)
		h := simhome.Start(i, ws, c.api.URL, simhome.Options{ServerID: ids[slug], Resolve: c.resolve,
			BaseWait: 500 * time.Millisecond, MaxWait: 10 * time.Second, PingEvery: 5 * time.Second})
		c.mu.Lock()
		c.homes = append(c.homes, h)
		c.mu.Unlock()
		homes = append(homes, h)
	}
	return homes
}

// healthyRelays is the pool's healthy members, read from the relays table.
func (c *cluster) healthyRelays() []relaypkg.PoolRelay {
	rs, err := relaypkg.PGPoolStore{DB: c.db}.Relays(context.Background())
	if err != nil {
		return nil
	}
	var out []relaypkg.PoolRelay
	for _, r := range rs {
		if r.Healthy() {
			out = append(out, r)
		}
	}
	return out
}

func ids(rs []relaypkg.PoolRelay) string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return strings.Join(out, ",")
}

// waitPlaced waits until the healthy set is want and every home holds its primary and standby
// where its assignment over it says; it returns how long that took.
func (c *cluster) waitPlaced(homes []*simhome.Home, want string, d time.Duration, what string) time.Duration {
	c.t.Helper()
	start := time.Now()
	last := ""
	for {
		hs := c.healthyRelays()
		off := 0
		example := ""
		if ids(hs) == want {
			for _, h := range homes {
				a := relaypkg.Assign(h.ID, hs)
				st := h.Sup.State()
				if st.Status != "online" || st.Relay != a.Primary || st.StandbyRelay != a.Standby {
					off++
					if example == "" {
						example = fmt.Sprintf("%s wants %+v, has %s on %q standby %q", h.Slug, a, st.Status, st.Relay, st.StandbyRelay)
					}
				}
			}
			if off == 0 {
				return time.Since(start)
			}
		} else {
			off = len(homes)
			example = "healthy relays " + ids(hs)
		}
		if time.Since(start) > d {
			c.t.Fatalf("timed out after %s waiting for %s: %d homes not in place (%s)", d, what, off, example)
		}
		if msg := fmt.Sprintf("%d", off/100); msg != last {
			last = msg
			c.t.Logf("%s: %d homes not in place yet (%s)", what, off, example)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// shares checks each relay's count of the primaries against its weight (equal capacities).
// The homes' server ids are random, so with a uniform hash a relay's count is binomial (n homes,
// p = 1/len(relays)); the bound is shareSigmas of its standard deviations, √(n·p·(1−p)).
func shares(t *testing.T, homes []*simhome.Home, relays []string) string {
	t.Helper()
	count := map[string]int{}
	for _, h := range homes {
		count[h.Sup.State().Relay]++
	}
	want, tol := shareBound(len(homes), len(relays))
	var parts []string
	for _, r := range relays {
		parts = append(parts, fmt.Sprintf("%s %d", r, count[r]))
		if math.Abs(float64(count[r])-want) > tol {
			t.Errorf("%s holds %d primaries, want %.0f ±%.0f (%.0f σ)", r, count[r], want, tol, shareSigmas)
		}
	}
	return strings.Join(parts, ", ")
}

// shareSigmas is how many binomial standard deviations a relay's count may stray: 4 fails a
// uniform hash about once in 5,000 runs (±18 % of a third at 1,000 homes on 3 relays, ±13 % of
// a half on 2). A flat ±10 % fails a uniform hash in about 7 % of runs at 1,000 homes on 3
// relays (2.2 σ). The hash's own accuracy is T01's ±2 % over 100,000 ids
// (cloud/internal/relay/pool_test.go).
const shareSigmas = 4.0

// shareBound is a relay's expected count of n homes over relays equal relays, and the bound.
func shareBound(n, relays int) (want, tol float64) {
	p := 1 / float64(relays)
	return float64(n) * p, shareSigmas * math.Sqrt(float64(n)*p*(1-p))
}

// failedAt is when request f failed: its start (Failure.At) plus how long it ran. A stream
// that started just before the kill and was cut by it failed after the kill, not before.
func failedAt(f streamgen.Failure) time.Time {
	took, err := time.ParseDuration(f.Took)
	if err != nil {
		return f.At
	}
	return f.At.Add(took)
}

// relayDNS runs relay-dns in the test against the cluster's relays table and a fake Cloudflare
// zone, probing each relay's /readyz at its published port.
func (c *cluster) relayDNS(ctx context.Context) *fakecloudflare.API {
	cf := fakecloudflare.New("zone-rehearsal", "rehearsal-cf-token")
	srv := httptest.NewServer(cf)
	c.t.Cleanup(srv.Close)
	ctl := &relaydns.Controller{
		Relays: relaypkg.PGPoolStore{DB: c.db},
		DNS:    &relaydns.Cloudflare{BaseURL: srv.URL, ZoneID: "zone-rehearsal", Token: "rehearsal-cf-token"},
		Names:  []string{"relay.test", "*.relay.test"},
		Probe: func(ctx context.Context, r relaypkg.PoolRelay) bool {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/readyz", c.relayPort(r.ID)), nil)
			resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
			if err != nil {
				return false
			}
			_ = resp.Body.Close()
			return resp.StatusCode == 200
		},
	}
	go ctl.Run(ctx, relaydns.Every)
	return cf
}

// rebalanceDrains reads relay id's maktaba_relay_tunnel_drains_total{reason="rebalance"}.
func (c *cluster) rebalanceDrains(id string) (int64, bool) {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", c.relayPort(id)))
	if err != nil {
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, `maktaba_relay_tunnel_drains_total{reason="rebalance"} `) {
			n, err := strconv.ParseInt(strings.Fields(line)[1], 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// T08 relay-pool-kill-one: three relays, 1,000 homes in their places and 100 streams through
// the round-robin edge; r3 is killed. No request fails 10 s after the kill (p99 of the homes'
// last failure ≤ 10 s); every home holds a new standby within 60 s; r3's address leaves the
// round robin within 30 s; the survivors hold their shares (shareSigmas).
func relayPoolKillOne(t *testing.T, c *cluster) Result {
	n := envInt("REHEARSAL_HOMES", 1000)
	homes := c.poolHomes(n)
	placed := c.waitPlaced(homes, "r1,r2,r3", 3*time.Minute, "every home in its place")
	before := shares(t, homes, []string{"r1", "r2", "r3"})
	var standbys int
	_ = c.db.QueryRow(`SELECT count(*) FROM relay_standby_tunnels`).Scan(&standbys)
	if standbys != n {
		t.Errorf("%d standby tunnels in the directory, want %d", standbys, n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cf := c.relayDNS(ctx)
	waitFor(t, time.Minute, "the round robin over the three relays", func() bool {
		return strings.Join(cf.IPs("relay.test"), ",") == "10.50.0.31,10.50.0.32,10.50.0.33"
	})
	slugs := make([]string, len(homes))
	hit := map[string]bool{}
	for i, h := range homes {
		slugs[i] = h.Slug
		if h.Sup.State().Relay == "r3" {
			hit[h.Slug] = true
		}
	}
	stop := c.load(slugs, envInt("REHEARSAL_STREAMS", 100))
	time.Sleep(5 * time.Second)

	killed := time.Now()
	c.must("kill", "r3")
	var gone atomic.Int64
	go func() {
		for ctx.Err() == nil {
			if !strings.Contains(strings.Join(cf.IPs("relay.test"), ","), "10.50.0.33") {
				gone.Store(int64(time.Since(killed)))
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()
	restandby := c.waitPlaced(homes, "r1,r2", 90*time.Second, "a new standby for every home")
	waitFor(t, 60*time.Second, "r3 out of the round robin", func() bool { return gone.Load() > 0 })
	dnsGone := time.Duration(gone.Load())
	time.Sleep(5 * time.Second)
	stats, down := stop()
	after := shares(t, homes, []string{"r1", "r2"})

	// each home's last failure after the kill
	lastFail := map[string]time.Duration{}
	late := 0
	for _, f := range stats.Failures {
		d := failedAt(f).Sub(killed)
		if d < 0 {
			t.Errorf("a request failed before the kill: %+v", f)
			continue
		}
		if d > lastFail[f.Slug] {
			lastFail[f.Slug] = d
		}
		if d > 10*time.Second {
			late++
		}
	}
	var lasts []time.Duration
	for _, d := range lastFail {
		lasts = append(lasts, d)
	}
	sort.Slice(lasts, func(i, j int) bool { return lasts[i] < lasts[j] })
	p99 := time.Duration(0)
	if len(lasts) > 0 {
		p99 = lasts[int(math.Ceil(0.99*float64(len(lasts))))-1]
	}
	t.Logf("%d homes placed in %s (%s); r3 killed with %d primaries on it; new standbys after %s (%s); r3 out of DNS after %s; %d requests, %d failed (%d over 10 s after the kill), p99 recovery %s; probe down %s",
		n, placed.Round(time.Second), before, len(hit), restandby.Round(time.Second), after, dnsGone.Round(time.Second),
		stats.OK+stats.Failed, stats.Failed, late, p99, down)
	if late > 0 {
		t.Errorf("%d requests failed more than 10 s after the kill; first: %+v", late, stats.Failures[0])
	}
	if p99 > 10*time.Second {
		t.Errorf("p99 recovery %s, budget 10 s", p99)
	}
	if restandby > 60*time.Second {
		t.Errorf("new standbys after %s, budget 60 s", restandby)
	}
	if dnsGone > 30*time.Second {
		t.Errorf("r3 left the round robin after %s, budget 30 s", dnsGone)
	}
	if got := strings.Join(cf.IPs("*.relay.test"), ","); got != "10.50.0.31,10.50.0.32" {
		t.Errorf("*.relay.test holds %s", got)
	}
	return Result{DowntimeMS: p99.Milliseconds(), BudgetMS: 10_000, FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("placed: %s; after r3 died: %s; new standbys in %s; r3 out of DNS in %s; %d failures in the first 10 s",
			before, after, restandby.Round(time.Second), dnsGone.Round(time.Second), stats.Failed-int64(late))}}
}

// T09 rebalance-add-relay: two relays, 1,000 homes in their places, 100 streams; a third relay
// starts. Each relay drains at most 20 homes a second; no request fails; only homes whose
// primary changed move (to r3); the final shares are within shareSigmas.
func rebalanceAddRelay(t *testing.T, c *cluster) Result {
	n := envInt("REHEARSAL_HOMES", 1000)
	homes := c.poolHomes(n)
	c.waitPlaced(homes, "r1,r2", 3*time.Minute, "every home in its place on two relays")
	first := map[string]string{} // each home's primary before r3
	for _, h := range homes {
		first[h.ID] = h.Sup.State().Relay
	}
	slugs := make([]string, len(homes))
	for i, h := range homes {
		slugs[i] = h.Slug
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// which homes' primaries move, watched throughout
	var mu sync.Mutex
	moved := map[string]bool{}
	go func() {
		for ctx.Err() == nil {
			for _, h := range homes {
				if st := h.Sup.State(); st.Status == "online" && st.Relay != "" && st.Relay != first[h.ID] {
					mu.Lock()
					moved[h.ID] = true
					mu.Unlock()
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	// each relay's rebalance drains, sampled each second
	maxRate := map[string]float64{}
	go func() {
		prev := map[string]int64{}
		at := map[string]time.Time{}
		for ctx.Err() == nil {
			for _, id := range []string{"r1", "r2", "r3"} {
				v, ok := c.rebalanceDrains(id)
				if !ok {
					continue
				}
				now := time.Now()
				if p, seen := prev[id]; seen {
					if el := now.Sub(at[id]).Seconds(); el >= 0.5 {
						r := float64(v-p) / el
						mu.Lock()
						maxRate[id] = math.Max(maxRate[id], r)
						mu.Unlock()
					}
				}
				prev[id], at[id] = v, now
			}
			time.Sleep(time.Second)
		}
	}()
	stop := c.load(slugs, envInt("REHEARSAL_STREAMS", 100))
	time.Sleep(5 * time.Second)

	c.must("up", "-d", "--wait", "r3")
	took := c.waitPlaced(homes, "r1,r2,r3", 3*time.Minute, "every home in its place on three relays")
	time.Sleep(3 * time.Second)
	stats, down := stop()
	cancel()
	final := shares(t, homes, []string{"r1", "r2", "r3"})

	all := relaysOf3()
	changed, wrongMove := 0, 0
	mu.Lock()
	for _, h := range homes {
		a := relaypkg.Assign(h.ID, all)
		if a.Primary != first[h.ID] {
			changed++
			if a.Primary != "r3" {
				t.Errorf("%s's primary changed %s → %s, not to the new relay", h.Slug, first[h.ID], a.Primary)
			}
		}
		if moved[h.ID] && a.Primary == first[h.ID] {
			wrongMove++
		}
	}
	rates := fmt.Sprintf("r1 %.1f/s, r2 %.1f/s", maxRate["r1"], maxRate["r2"])
	for id, r := range maxRate {
		if r > 22 { // 20 a second, with a second's sampling jitter
			t.Errorf("%s drained %.1f homes a second, budget 20", id, r)
		}
	}
	mu.Unlock()
	t.Logf("r3 joined; %d homes' primaries changed, all in place after %s; drain rates %s; shares %s; %d requests, %d failed; probe down %s",
		changed, took.Round(time.Second), rates, final, stats.OK+stats.Failed, stats.Failed, down)
	if wrongMove > 0 {
		t.Errorf("%d homes moved whose primary did not change", wrongMove)
	}
	if stats.Failed > 0 {
		t.Errorf("%d requests failed; first: %+v", stats.Failed, stats.Failures[0])
	}
	if down > 0 {
		t.Errorf("the probe was down %s, budget 0", down)
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: 0, FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("%d of %d homes moved to r3 in %s; most drains a second %s; final shares %s", changed, n, took.Round(time.Second), rates, final)}}
}

func relaysOf3() []relaypkg.PoolRelay {
	var out []relaypkg.PoolRelay
	for _, id := range []string{"r1", "r2", "r3"} {
		out = append(out, relaypkg.PoolRelay{ID: id, State: relaypkg.RelayActive, CapacityMbps: 1000})
	}
	return out
}

// The two rules relay-pool-kill-one judges by, without a cluster: a stream cut by the kill
// failed after it, and the share bound is 4 binomial standard deviations.
func TestRelayPoolJudging(t *testing.T) {
	killed := time.Date(2026, 11, 14, 5, 43, 23, 700_000_000, time.UTC)
	cut := streamgen.Failure{At: killed.Add(-180 * time.Millisecond), Took: "290ms"}
	if d := failedAt(cut).Sub(killed); d != 110*time.Millisecond {
		t.Errorf("a stream started 180 ms before the kill and cut 290 ms in failed %s after it, want 110ms", d)
	}
	before := streamgen.Failure{At: killed.Add(-time.Second), Took: "200ms"}
	if !failedAt(before).Before(killed) {
		t.Error("a request that ended 800 ms before the kill counted after it")
	}
	if odd := (streamgen.Failure{At: killed, Took: "?"}); !failedAt(odd).Equal(killed) {
		t.Error("an unreadable Took moved the failure")
	}
	for _, c := range []struct {
		n, relays int
		want, tol float64
	}{{1000, 3, 333.3, 59.6}, {1000, 2, 500, 63.2}, {1000, 1, 1000, 0}} {
		want, tol := shareBound(c.n, c.relays)
		if math.Abs(want-c.want) > 0.1 || math.Abs(tol-c.tol) > 0.1 {
			t.Errorf("shareBound(%d, %d) = %.1f ±%.1f, want %.1f ±%.1f", c.n, c.relays, want, tol, c.want, c.tol)
		}
	}
}
