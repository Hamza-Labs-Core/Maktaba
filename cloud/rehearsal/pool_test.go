//go:build cloudrehearsal

package rehearsal

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
)

// The api pool (Epic 50 story 50.17): tools/rehearsal-cloud/compose-pool.yml, three api nodes
// (api + worker each) behind HAProxy standing in for LB11, on one Postgres, with two relays
// caching homes.

// poolPorts are the scenario's host ports.
type poolPorts struct {
	lb, stats int
	api       [3]int
	relay     [2]int
}

func poolPortsOf(project string) poolPorts {
	p := portsFor(project)
	return poolPorts{lb: p[1] + 600, stats: p[1] + 606, api: [3]int{p[1] + 601, p[1] + 602, p[1] + 603},
		relay: [2]int{p[1] + 604, p[1] + 605}}
}

func (pp poolPorts) env() []string {
	return []string{fmt.Sprintf("LB_PORT=%d", pp.lb), fmt.Sprintf("LB_STATS_PORT=%d", pp.stats),
		fmt.Sprintf("API1_PORT=%d", pp.api[0]), fmt.Sprintf("API2_PORT=%d", pp.api[1]), fmt.Sprintf("API3_PORT=%d", pp.api[2]),
		fmt.Sprintf("RELAY1_PORT=%d", pp.relay[0]), fmt.Sprintf("RELAY2_PORT=%d", pp.relay[1])}
}

// upPool starts the pool and registers its teardown (logs kept first).
func upPool(t *testing.T, scenario string) *cluster {
	t.Helper()
	c := &cluster{t: t, project: "mr-" + scenario, dir: filepath.Join(repoRoot(t), "tools/rehearsal-cloud"), base: "compose-pool.yml"}
	c.extra = poolPortsOf(c.project).env()
	_, _ = c.compose("down", "-v", "--remove-orphans")
	t.Cleanup(func() {
		logs, _ := c.compose("logs", "--no-color", "--timestamps")
		_ = os.WriteFile(filepath.Join(outDir(t), scenario+".log"), []byte(logs), 0o644)
		if c.db != nil {
			_ = c.db.Close()
		}
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = c.compose("down", "-v", "--remove-orphans")
		}
	})
	c.must("up", "-d", "--wait")
	db, err := sql.Open("postgres", fmt.Sprintf("postgres://maktaba:rehearsal@127.0.0.1:%d/maktaba?sslmode=disable", portsFor(c.project)[0]))
	if err != nil {
		t.Fatal(err)
	}
	c.db = db
	return c
}

var poolHTTP = &http.Client{Timeout: 5 * time.Second}

// lbStatus is each node's state in HAProxy's stats ("UP", "DOWN", "UP 1/2", ...).
func lbStatus(pp poolPorts) map[string]string {
	resp, err := poolHTTP.Get(fmt.Sprintf("http://127.0.0.1:%d/stats;csv", pp.stats))
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	r := csv.NewReader(resp.Body)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil
	}
	col := -1
	for i, h := range rows[0] {
		if strings.TrimPrefix(h, "# ") == "status" {
			col = i
		}
	}
	out := map[string]string{}
	for _, row := range rows[1:] {
		if len(row) > col && col >= 0 && row[0] == "apis" && strings.HasPrefix(row[1], "api-") {
			out[row[1]] = row[col]
		}
	}
	return out
}

// poolLeader is the node whose worker holds the leader lock now ("" for none).
func poolLeader(db *sql.DB) string {
	var node string
	_ = db.QueryRow(`SELECT c.node FROM cloud_leaders c
        JOIN pg_locks k ON k.pid = c.backend_pid AND k.locktype = 'advisory' AND k.granted
             AND k.objsubid = 1 AND k.objid = 8472613
        WHERE c.lock_id = 8472613`).Scan(&node)
	return node
}

func lockHolders(db *sql.DB) int {
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted AND objsubid = 1 AND objid = 8472613`).Scan(&n)
	return n
}

// relayInvalidations is a relay's cache_invalidations_total.
func relayInvalidations(port int) int64 {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/metrics", port), nil)
	req.Host = "relay.test"
	resp, err := poolHTTP.Do(req)
	if err != nil {
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "cache_invalidations_total "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return -1
}

func apiRequest(method string, port int, path, body string) (int, string, error) {
	req, _ := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), strings.NewReader(body))
	req.Host = "api.test"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := poolHTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), nil
}

// poolLoad sends API requests through the load balancer from clients goroutines until stopped;
// stop returns how many there were, the failures, and the first few.
func poolLoad(pp poolPorts, clients int) (stop func() (ok, failed int64, first []string)) {
	ctx, cancel := context.WithCancel(context.Background())
	var nOK, nFailed atomic.Int64
	var mu sync.Mutex
	var firsts []string
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cl := &http.Client{Timeout: 10 * time.Second}
			for ctx.Err() == nil {
				req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/billing/plans", pp.lb), nil)
				req.Host = "api.test"
				resp, err := cl.Do(req)
				what := ""
				if err != nil {
					what = err.Error()
				} else {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						what = fmt.Sprintf("status %d", resp.StatusCode)
					}
				}
				if what == "" {
					nOK.Add(1)
				} else if ctx.Err() == nil {
					nFailed.Add(1)
					mu.Lock()
					if len(firsts) < 5 {
						firsts = append(firsts, time.Now().Format("15:04:05.000")+" "+what)
					}
					mu.Unlock()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
	}
	return func() (int64, int64, []string) {
		cancel()
		wg.Wait()
		mu.Lock()
		defer mu.Unlock()
		return nOK.Load(), nFailed.Load(), firsts
	}
}

// removalWatch records, per node, when its /readyz turned 503 (its SIGTERM: it drains) and when
// the load balancer then marked it DOWN.
type removalWatch struct {
	mu       sync.Mutex
	draining map[string]time.Time
	removed  map[string]time.Duration
	stop     context.CancelFunc
	done     chan struct{}
}

func watchRemovals(pp poolPorts) *removalWatch {
	ctx, cancel := context.WithCancel(context.Background())
	w := &removalWatch{draining: map[string]time.Time{}, removed: map[string]time.Duration{}, stop: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for ctx.Err() == nil {
			for i, port := range pp.api {
				node := fmt.Sprintf("api-%d", i+1)
				w.mu.Lock()
				_, seen := w.draining[node]
				w.mu.Unlock()
				if !seen {
					if code, body, err := apiRequest(http.MethodGet, port, "/readyz", ""); err == nil && code == 503 && strings.Contains(body, "draining") {
						w.mu.Lock()
						w.draining[node] = time.Now()
						w.mu.Unlock()
					}
				}
			}
			st := lbStatus(pp)
			w.mu.Lock()
			for node, at := range w.draining {
				if _, done := w.removed[node]; !done && strings.HasPrefix(st[node], "DOWN") {
					w.removed[node] = time.Since(at)
				}
			}
			w.mu.Unlock()
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return w
}

func (w *removalWatch) result() map[string]time.Duration {
	w.stop()
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.removed
}

// api-pool: the worker runs on one node and moves within 15 s when its holder is killed; the
// sign-in limit holds across the nodes; a plan change reaches every node's cache within a
// second; and a rolling deploy over the three nodes behind the load balancer fails no request,
// the balancer taking each stopping node out within 10 s.
func apiPool(t *testing.T, c *cluster) Result {
	pp := poolPortsOf(c.project)
	var notes []string
	waitFor(t, time.Minute, "the load balancer's three nodes up", func() bool {
		st := lbStatus(pp)
		return st["api-1"] == "UP" && st["api-2"] == "UP" && st["api-3"] == "UP"
	})

	// 1. the worker: exactly one node holds the lock; its worker killed, another holds it within 15 s
	var first string
	waitFor(t, 45*time.Second, "a worker holding the leader lock", func() bool { first = poolLeader(c.db); return first != "" })
	time.Sleep(3 * time.Second) // every worker has tried by now
	if n := lockHolders(c.db); n != 1 {
		t.Fatalf("%d sessions hold the worker's lock", n)
	}
	firstSvc := "worker-" + strings.TrimPrefix(first, "api-")
	killed := time.Now()
	c.must("kill", firstSvc)
	var next string
	waitFor(t, 45*time.Second, "another node's worker taking the lock", func() bool {
		next = poolLeader(c.db)
		return next != "" && next != first
	})
	moved := time.Since(killed)
	t.Logf("worker lock: %s held it; killed; %s took it %s later", first, next, moved)
	if moved > 16*time.Second { // 15 s, and the poll
		t.Errorf("the lock moved %s after its holder was killed, budget 15 s", moved)
	}
	if n := lockHolders(c.db); n != 1 {
		t.Errorf("%d sessions hold the worker's lock after the move", n)
	}
	notes = append(notes, fmt.Sprintf("worker lock moved from %s to %s in %s", first, next, moved.Round(100*time.Millisecond)))
	c.must("up", "-d", "--no-deps", firstSvc)

	// 2. the sign-in limit, round robin over the three nodes: refused once the shared burst (30)
	// is spent, not after three nodes' worth; malformed attempts, so each is quick
	start := time.Now()
	refusedAt := 0
	for i := 1; i <= 90 && refusedAt == 0; i++ {
		code, _, err := apiRequest(http.MethodPost, pp.api[i%3], "/v1/auth/login", `{"email":`)
		if err != nil {
			t.Fatalf("sign-in attempt %d: %v", i, err)
		}
		if code == http.StatusTooManyRequests {
			refusedAt = i
		}
	}
	elapsed := time.Since(start)
	refill := int(elapsed.Seconds()*5) + 1
	t.Logf("sign-in limit: first 429 at attempt %d across three nodes, %s (burst 30, refill up to %d)", refusedAt, elapsed, refill)
	if refusedAt < 31 || refusedAt > 31+refill {
		t.Errorf("the first refusal came at attempt %d of a shared burst of 30", refusedAt)
	}
	notes = append(notes, fmt.Sprintf("sign-in refused at attempt %d over 3 nodes", refusedAt))

	// 3. a plan change (what the billing webhook writes) reaches both relays' caches within 1 s
	if _, err := seed.Homes(context.Background(), c.db, 0, 1, "free"); err != nil {
		t.Fatal(err)
	}
	_, _, slug := simhome.Creds(0)
	time.Sleep(time.Second)
	before := [2]int64{relayInvalidations(pp.relay[0]), relayInvalidations(pp.relay[1])}
	if before[0] < 0 || before[1] < 0 {
		t.Fatalf("the relays' cache counters: %v", before)
	}
	if _, err := c.db.Exec(`UPDATE servers SET plan = 'basic' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	changed := time.Now()
	var heard time.Duration
	for time.Since(changed) < 5*time.Second {
		if relayInvalidations(pp.relay[0]) > before[0] && relayInvalidations(pp.relay[1]) > before[1] {
			heard = time.Since(changed)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("plan change: both relays dropped %s after %s", slug, heard)
	if heard == 0 || heard > time.Second {
		t.Errorf("the plan change reached both relays after %s, budget 1 s", heard)
	}
	notes = append(notes, fmt.Sprintf("plan change reached both relays in %s", heard.Round(time.Millisecond)))

	// 4. a rolling deploy under load (T06): no request fails; each stopping node is out of the
	// load balancer within 10 s of its SIGTERM
	waitFor(t, time.Minute, "every node up again", func() bool {
		st := lbStatus(pp)
		return st["api-1"] == "UP" && st["api-2"] == "UP" && st["api-3"] == "UP"
	})
	stopLoad := poolLoad(pp, 8)
	watch := watchRemovals(pp)
	time.Sleep(3 * time.Second)
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "deploy/cloud-ha/bin/api-rollout"), "new")
	cmd.Env = append(append(append(os.Environ(), c.env()...), c.extra...),
		"POOL_PROJECT="+c.project,
		fmt.Sprintf("API_NODES=%d %d %d", pp.api[0], pp.api[1], pp.api[2]),
		"RESTART_CMD=bash "+filepath.Join(c.dir, "pool.sh"),
		"READY_URL=http://127.0.0.1:{node}/readyz",
		"READY_STABLE=15", "CHECK_EVERY=1")
	rolled := time.Now()
	out, err := cmd.CombinedOutput()
	took := time.Since(rolled)
	t.Logf("api-rollout new (%s): %v\n%s", took, err, out)
	time.Sleep(3 * time.Second)
	ok, failed, firsts := stopLoad()
	removed := watch.result()
	if err != nil {
		t.Errorf("the rollout failed: %v", err)
	}
	t.Logf("rolling deploy: %d requests through the load balancer, %d failed %v; removals %v", ok+failed, failed, firsts, removed)
	if failed > 0 {
		t.Errorf("%d requests failed during the rolling deploy; first: %v", failed, firsts)
	}
	for _, node := range []string{"api-1", "api-2", "api-3"} {
		d, seen := removed[node]
		switch {
		case !seen:
			t.Errorf("%s: never seen draining and then out of the load balancer", node)
		case d > 10*time.Second+500*time.Millisecond: // 10 s, and the poll
			t.Errorf("%s: out of the load balancer %s after its SIGTERM, budget 10 s", node, d)
		}
	}
	for i, port := range pp.api {
		if _, body, err := apiRequest(http.MethodGet, port, "/.well-known/maktaba-cloud-version", ""); err != nil || !strings.Contains(body, `"version":"new"`) {
			t.Errorf("api-%d after the rollout: %s %v", i+1, body, err)
		}
	}
	waitFor(t, 45*time.Second, "a worker holding the lock after the rollout", func() bool { return poolLeader(c.db) != "" })
	if n := lockHolders(c.db); n != 1 {
		t.Errorf("%d sessions hold the worker's lock after the rollout", n)
	}
	notes = append(notes, fmt.Sprintf("rolling deploy of 3 nodes in %s, removals %v", took.Round(time.Second), removed))
	return Result{DowntimeMS: 0, BudgetMS: 0, FailedRequests: failed, Requests: ok + failed, Notes: notes}
}
