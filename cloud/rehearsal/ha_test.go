//go:build cloudrehearsal

package rehearsal

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
)

// The Phase 1 pair (story 50.07): tools/rehearsal-cloud/compose-ha.yml.

// ha is one scenario's pair.
type ha struct {
	*cluster
	pg  map[string]int // node -> host port of its Postgres
	api map[string]int // node -> host port of its Patroni REST API
}

func (h *ha) composeHA(args ...string) (string, error) {
	cmd := exec.Command("docker", append([]string{"compose", "-p", h.project, "-f", filepath.Join(h.dir, "compose-ha.yml")}, args...)...)
	cmd.Env = append(os.Environ(), h.envHA()...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *ha) mustHA(args ...string) string {
	h.t.Helper()
	out, err := h.composeHA(args...)
	if err != nil {
		h.t.Fatalf("docker compose (ha) %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (h *ha) envHA() []string {
	p := portsFor(h.project)
	node := os.Getenv("NODE_IMAGE")
	if node == "" {
		node = "maktaba-node:rehearsal"
	}
	return append(h.env(),
		"NODE_IMAGE="+node,
		fmt.Sprintf("A_PG_PORT=%d", p[0]+100), fmt.Sprintf("B_PG_PORT=%d", p[0]+101),
		fmt.Sprintf("A_API_PORT=%d", p[0]+200), fmt.Sprintf("B_API_PORT=%d", p[0]+201),
		fmt.Sprintf("FAKEHCLOUD_PORT=%d", p[0]+300),
		"FAKEHCLOUD_NETWORK="+h.project+"_fip",
		fmt.Sprintf("FAKEHCLOUD_SERVERS=1=%s-node-a-1@10.61.0.11,2=%s-node-b-1@10.61.0.12", h.project, h.project),
	)
}

// upHA starts the pair and waits for a leader with a streaming sync standby.
func upHA(t *testing.T, scenario string) *ha {
	t.Helper()
	c := &cluster{t: t, project: "mr-" + scenario, dir: filepath.Join(repoRoot(t), "tools/rehearsal-cloud")}
	p := portsFor(c.project)
	c.edge = fmt.Sprintf("http://127.0.0.1:%d", p[1])
	h := &ha{cluster: c, pg: map[string]int{"node-a": p[0] + 100, "node-b": p[0] + 101},
		api: map[string]int{"node-a": p[0] + 200, "node-b": p[0] + 201}}
	pki := filepath.Join(c.dir, ".work", "pki-"+scenario)
	_ = os.RemoveAll(pki)
	if out, err := exec.Command("bash", filepath.Join(repoRoot(t), "deploy/cloud-ha/bin/make-etcd-certs"), pki,
		"node-a=10.60.0.11", "node-b=10.60.0.12", "witness=10.60.0.13").CombinedOutput(); err != nil {
		t.Fatalf("make-etcd-certs: %v\n%s", err, out)
	}
	// the containers read them as root; the key files are 0600 on purpose in production
	_ = exec.Command("sh", "-c", "chmod 0644 "+pki+"/*").Run()
	_ = os.RemoveAll(filepath.Join(c.dir, ".work", "pki"))
	if err := os.Symlink("pki-"+scenario, filepath.Join(c.dir, ".work", "pki")); err != nil {
		t.Fatal(err)
	}
	_, _ = h.composeHA("down", "-v", "--remove-orphans")
	t.Cleanup(func() {
		h.stopHomes()
		if c.api != nil {
			c.api.close()
		}
		logs, _ := h.composeHA("logs", "--no-color", "--timestamps")
		var b strings.Builder
		b.WriteString(logs)
		for _, n := range []string{"node-a", "node-b"} {
			for _, f := range []string{"patroni.log", "relay.log", "etcd.log", "caddy.log"} {
				out, _ := exec.Command("docker", "exec", h.project+"-"+n+"-1", "cat", "/var/log/"+f).CombinedOutput()
				fmt.Fprintf(&b, "\n===== %s /var/log/%s\n%s", n, f, out)
			}
		}
		_ = os.WriteFile(filepath.Join(outDir(t), scenario+".log"), []byte(b.String()), 0o644)
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = h.composeHA("down", "-v", "--remove-orphans")
		}
	})
	h.mustHA("up", "-d")
	waitFor(t, 3*time.Minute, "a leader and a streaming sync standby", func() bool {
		l := h.leader()
		return l != "" && h.syncStandby(other(l))
	})
	waitFor(t, time.Minute, "the edge through the Floating IP", func() bool {
		resp, err := http.Get(c.edge + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == 200
	})
	api := simhome.API()
	c.api = &httptestServer{URL: api.URL, close: api.Close}
	return h
}

func other(n string) string {
	if n == "node-a" {
		return "node-b"
	}
	return "node-a"
}

// leader is the node whose Patroni says it leads ("" none).
func (h *ha) leader() string {
	for n, port := range h.api {
		resp, err := (&http.Client{Timeout: time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/leader", port))
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return n
			}
		}
	}
	return ""
}

// syncStandby: node is the streaming synchronous standby.
func (h *ha) syncStandby(node string) bool {
	resp, err := (&http.Client{Timeout: time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/sync", h.api[node]))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}

// db is the cloud database on node (the app's role).
func (h *ha) db(node string) *sql.DB {
	db, _ := sql.Open("postgres", fmt.Sprintf("postgres://maktaba:rehearsal@127.0.0.1:%d/maktaba?sslmode=disable&connect_timeout=2", h.pg[node]))
	db.SetMaxOpenConns(4)
	db.SetConnMaxLifetime(5 * time.Second)
	return db
}

// writable: node's Postgres takes writes now.
func writable(db *sql.DB) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var rec bool
	return db.QueryRowContext(ctx, `SELECT pg_is_in_recovery()`).Scan(&rec) == nil && !rec
}

// homesOnHA seeds n homes on the leader and runs them against the Floating IP.
func (h *ha) homesOnHA(n int) []string {
	h.t.Helper()
	l := h.leader()
	db := h.db(l)
	defer func() { _ = db.Close() }()
	waitFor(h.t, time.Minute, "the schema", func() bool {
		var ok bool
		return db.QueryRow(`SELECT to_regclass('relay_tunnels') IS NOT NULL`).Scan(&ok) == nil && ok
	})
	ids, err := seed.Homes(context.Background(), db, 0, n, "pro")
	if err != nil {
		h.t.Fatal(err)
	}
	ws := "ws" + strings.TrimPrefix(h.edge, "http") + "/v1/relay/ws"
	slugs := make([]string, n)
	for i := range n {
		_, _, slug := simhome.Creds(i)
		hm := simhome.Start(i, ws, h.cluster.api.URL, simhome.Options{ServerID: ids[slug], BaseWait: time.Second, MaxWait: 10 * time.Second, PingEvery: 10 * time.Second})
		h.mu.Lock()
		h.cluster.homes = append(h.cluster.homes, hm)
		h.mu.Unlock()
		slugs[i] = slug
	}
	waitFor(h.t, 3*time.Minute, fmt.Sprintf("%d homes online", n), func() bool { return h.tunnelsOn(l) == n })
	return slugs
}

// tunnelsOn counts node's relay's tunnels in the directory (read on the current primary).
func (h *ha) tunnelsOn(node string) int {
	for _, n := range []string{"node-a", "node-b"} {
		db := h.db(n)
		var c int
		err := db.QueryRow(`SELECT count(*) FROM relay_tunnels WHERE relay_id = $1`, node+"-blue").Scan(&c)
		_ = db.Close()
		if err == nil {
			return c
		}
	}
	return -1
}

// writer commits a row every 100 ms on whichever node is writable, and keeps the ids
// Postgres acknowledged (a commit returns only once the sync standby has it).
type writer struct {
	mu    sync.Mutex
	acked []int64
	stop  chan struct{}
	done  chan struct{}
}

func (h *ha) startWriter() *writer {
	w := &writer{stop: make(chan struct{}), done: make(chan struct{})}
	dbs := map[string]*sql.DB{"node-a": h.db("node-a"), "node-b": h.db("node-b")}
	for _, db := range dbs {
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS rehearsal_writes (id bigint PRIMARY KEY, at timestamptz NOT NULL DEFAULT now())`)
	}
	go func() {
		defer close(w.done)
		defer func() {
			for _, db := range dbs {
				_ = db.Close()
			}
		}()
		var next int64
		tk := time.NewTicker(100 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-tk.C:
			}
			next++
			for _, node := range h.writeOrder() {
				db := dbs[node]
				// a long deadline on purpose: a commit cancelled while it waits for the sync
				// standby is reported done by Postgres ("canceling wait for synchronous
				// replication") though only the primary has it; a partitioned primary's
				// waits end with its demotion instead, as an error
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				start := time.Now()
				_, err := db.ExecContext(ctx, `INSERT INTO rehearsal_writes (id) VALUES ($1)`, next)
				cancel()
				if err == nil && time.Since(start) < 59*time.Second {
					w.mu.Lock()
					w.acked = append(w.acked, next)
					w.mu.Unlock()
					break
				}
			}
		}
	}()
	return w
}

// writeOrder: the leader first (a write tried on the replica only fails).
func (h *ha) writeOrder() []string {
	if l := h.leader(); l != "" {
		return []string{l, other(l)}
	}
	return []string{"node-a", "node-b"}
}

func (w *writer) halt() []int64 {
	close(w.stop)
	<-w.done
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]int64(nil), w.acked...)
}

// lost is the acknowledged ids missing on node.
func (h *ha) lost(node string, acked []int64) []int64 {
	db := h.db(node)
	defer func() { _ = db.Close() }()
	have := map[int64]bool{}
	rows, err := db.Query(`SELECT id FROM rehearsal_writes`)
	if err != nil {
		h.t.Fatalf("reading the writes on %s: %v", node, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		have[id] = true
	}
	var missing []int64
	for _, id := range acked {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// C02 failover-kill-leader: 300 homes and 50 streams; the leader is killed. The other node
// leads within 45 s, holds the Floating IP and runs the relay; every home is back (p99
// within 45 s of the new relay coming up); no acknowledged commit is lost; the probe's
// downtime is within 3 min.
func failoverKillLeader(t *testing.T, _ *cluster) Result {
	h := upHA(t, "failover-kill-leader")
	n := envInt("REHEARSAL_HOMES", 300)
	slugs := h.homesOnHA(n)
	w := h.startWriter()
	stop := h.load(slugs, envInt("REHEARSAL_STREAMS", 50))
	time.Sleep(5 * time.Second)

	old := h.leader()
	start := time.Now()
	h.mustHA("kill", old)
	var promoted time.Duration
	waitFor(t, 90*time.Second, "the other node to lead", func() bool {
		if h.leader() == other(old) {
			promoted = time.Since(start)
			return true
		}
		return false
	})
	var back99, back100 time.Duration
	waitFor(t, 3*time.Minute, "every home back", func() bool {
		got := h.tunnelsOn(other(old))
		if back99 == 0 && got*100 >= n*99 {
			back99 = time.Since(start)
		}
		if got == n {
			back100 = time.Since(start)
			return true
		}
		return false
	})
	time.Sleep(5 * time.Second)
	stats, down := stop()
	acked := w.halt()
	missing := h.lost(other(old), acked)

	t.Logf("%s killed: %s led after %s; homes back p99 %s, all %s; probe down %s; %d requests, %d failed; %d commits, %d lost",
		old, other(old), promoted, back99, back100, down, stats.OK+stats.Failed, stats.Failed, len(acked), len(missing))
	if promoted > 45*time.Second {
		t.Errorf("promotion took %s, budget 45 s", promoted)
	}
	if down > 3*time.Minute {
		t.Errorf("down %s, budget 3 min", down)
	}
	if len(missing) > 0 {
		t.Errorf("%d acknowledged commits lost: %v", len(missing), missing[:min(10, len(missing))])
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: (3 * time.Minute).Milliseconds(), FailedRequests: stats.Failed,
		Requests: stats.OK + stats.Failed, ReconnectP99MS: back99.Milliseconds(),
		Notes: []string{fmt.Sprintf("promoted after %s; all homes back after %s; %d commits, %d lost", promoted, back100, len(acked), len(missing))}}
}

// C03 failover-network-partition: the leader is cut from the other node and the witness but
// keeps running. It stops taking writes and stops its relay within 40 s; the other node
// leads; at no instant do both take writes (checked every 250 ms); back on the network, the
// old leader rejoins as a replica and does not take the Floating IP.
func failoverNetworkPartition(t *testing.T, _ *cluster) Result {
	h := upHA(t, "failover-network-partition")
	old := h.leader()
	w := h.startWriter()
	dbs := map[string]*sql.DB{"node-a": h.db("node-a"), "node-b": h.db("node-b")}
	defer func() {
		for _, db := range dbs {
			_ = db.Close()
		}
	}()
	var two atomic.Int64
	var firstTwo atomic.Value
	watch := make(chan struct{})
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		tk := time.NewTicker(250 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-watch:
				return
			case <-tk.C:
			}
			if writable(dbs["node-a"]) && writable(dbs["node-b"]) {
				two.Add(1)
				firstTwo.CompareAndSwap(nil, time.Now().Format(time.RFC3339Nano))
			}
		}
	}()
	time.Sleep(3 * time.Second)

	start := time.Now()
	if out, err := exec.Command("docker", "network", "disconnect", h.project+"_private", h.project+"-"+old+"-1").CombinedOutput(); err != nil {
		t.Fatalf("partition: %v %s", err, out)
	}
	var readOnly, relayStopped time.Duration
	waitFor(t, 90*time.Second, old+" read-only and its relay stopped", func() bool {
		if readOnly == 0 && !writable(dbs[old]) {
			readOnly = time.Since(start)
		}
		if relayStopped == 0 {
			out, _ := exec.Command("docker", "exec", h.project+"-"+old+"-1", "pgrep", "-f", "maktaba-cloud serve").CombinedOutput()
			if strings.TrimSpace(string(out)) == "" {
				relayStopped = time.Since(start)
			}
		}
		return readOnly > 0 && relayStopped > 0
	})
	waitFor(t, 90*time.Second, other(old)+" to lead", func() bool { return writable(dbs[other(old)]) })
	promoted := time.Since(start)

	// the old leader back on the network: a replica, rewound if needed
	if out, err := exec.Command("docker", "network", "connect", "--ip", map[string]string{"node-a": "10.60.0.11", "node-b": "10.60.0.12"}[old],
		h.project+"_private", h.project+"-"+old+"-1").CombinedOutput(); err != nil {
		t.Fatalf("reconnect: %v %s", err, out)
	}
	waitFor(t, 3*time.Minute, old+" back as a streaming standby", func() bool { return h.syncStandby(old) })
	close(watch)
	<-watched
	acked := w.halt()
	missing := h.lost(other(old), acked)
	holder := h.fipHolder()

	t.Logf("%s cut off: read-only after %s, relay stopped after %s; %s writable after %s; two writers at %d instants; %d commits, %d lost; IP on server %d",
		old, readOnly, relayStopped, other(old), promoted, two.Load(), len(acked), len(missing), holder)
	if readOnly > 40*time.Second || relayStopped > 40*time.Second {
		t.Errorf("the cut-off leader took writes for %s and ran its relay for %s, budget 40 s", readOnly, relayStopped)
	}
	if two.Load() > 0 {
		t.Errorf("two writable primaries at %d checks; first at %v", two.Load(), firstTwo.Load())
	}
	if len(missing) > 0 {
		t.Errorf("%d acknowledged commits lost: %v", len(missing), missing[:min(10, len(missing))])
	}
	if want := map[string]int64{"node-a": 1, "node-b": 2}[other(old)]; holder != want {
		t.Errorf("the Floating IP is on server %d, want %d (the new leader's)", holder, want)
	}
	return Result{Notes: []string{fmt.Sprintf("read-only after %s; relay stopped after %s; other node writable after %s; two writers: %d", readOnly, relayStopped, promoted, two.Load())}}
}

// C04 switchover: deploy/cloud-ha/bin/switchover with traffic. The other node leads with
// no lost commit; the probe's pause is within 15 s.
func switchoverScenario(t *testing.T, _ *cluster) Result {
	h := upHA(t, "switchover")
	n := envInt("REHEARSAL_HOMES", 100)
	slugs := h.homesOnHA(n)
	w := h.startWriter()
	stop := h.load(slugs, envInt("REHEARSAL_STREAMS", 30))
	time.Sleep(5 * time.Second)
	old := h.leader()
	start := time.Now()
	out, err := exec.Command("docker", "exec", h.project+"-"+old+"-1", "env", "PATRONICTL=patronictl -c /etc/patroni/patroni.yml",
		"switchover", "--announced").CombinedOutput()
	if err != nil {
		t.Fatalf("switchover: %v\n%s", err, out)
	}
	took := time.Since(start)
	waitFor(t, 3*time.Minute, "every home back", func() bool { return h.tunnelsOn(other(old)) == n })
	time.Sleep(5 * time.Second)
	stats, down := stop()
	acked := w.halt()
	missing := h.lost(other(old), acked)
	t.Logf("switchover %s -> %s in %s; probe paused %s; %d requests, %d failed; %d commits, %d lost\n%s",
		old, other(old), took, down, stats.OK+stats.Failed, stats.Failed, len(acked), len(missing), out)
	if h.leader() != other(old) {
		t.Errorf("leader after the switchover: %s", h.leader())
	}
	if down > 15*time.Second {
		t.Errorf("remote play paused %s, budget 15 s", down)
	}
	if len(missing) > 0 {
		t.Errorf("%d acknowledged commits lost", len(missing))
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: 15000, FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("switchover in %s; %d commits, %d lost", took, len(acked), len(missing))}}
}

// fipHolder asks the fake Hetzner API which server holds the Floating IP.
func (h *ha) fipHolder() int64 {
	p := portsFor(h.project)
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/floating_ips/7", p[0]+300), nil)
	req.Header.Set("Authorization", "Bearer rehearsal-hcloud-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		FloatingIP struct {
			Server int64 `json:"server"`
		} `json:"floating_ip"`
	}
	_ = jsonDecode(resp, &out)
	return out.FloatingIP.Server
}
