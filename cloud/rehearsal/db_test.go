//go:build cloudrehearsal

package rehearsal

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The database on its own nodes (Epic 50 story 50.18): tools/rehearsal-cloud/compose-db.yml over
// compose-ha.yml. db-1, db-2 and db-3 run deploy/cloud-ha's Patroni (one synchronous replica of
// two), etcd and PgBouncer; their callback moves the database's Floating IP (8, db.internal)
// and starts nothing.

var dbNodes = []string{"db-1", "db-2", "db-3"}

// dbServer is each node's server id at the database's fake Hetzner API.
var dbServer = map[string]int64{"node-a": 1, "node-b": 2, "db-1": 11, "db-2": 12, "db-3": 13}

// syncOf is the cluster's synchronous replica ("" none) and how many replicas stream, from
// Patroni's /cluster on the leader (pg_stat_replication hides its states from the app's role).
func (h *ha) syncOf(leader string) (string, int) {
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/cluster", h.api[leader]))
	if err != nil {
		return "", 0
	}
	defer func() { _ = resp.Body.Close() }()
	var c struct {
		Members []struct {
			Name  string `json:"name"`
			Role  string `json:"role"`
			State string `json:"state"`
		} `json:"members"`
	}
	if jsonDecode(resp, &c) != nil {
		return "", 0
	}
	sync, n := "", 0
	for _, m := range c.Members {
		if m.State != "streaming" {
			continue
		}
		n++
		if m.Role == "sync_standby" || m.Role == "quorum_standby" {
			sync = m.Name
		}
	}
	return sync, n
}

// dbFIPHolder asks the database's fake Hetzner API which server holds Floating IP 8.
func (h *ha) dbFIPHolder() int64 {
	p := portsFor(h.project)
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/floating_ips/8", p[0]+301), nil)
	req.Header.Set("Authorization", "Bearer rehearsal-hcloud-token")
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
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

// threeDBReady: a leader, one synchronous and one asynchronous replica streaming, and the
// database's Floating IP on the leader.
func threeDBReady(h *ha) bool {
	l := h.leader()
	if l == "" {
		return false
	}
	s, n := h.syncOf(l)
	return s != "" && n == 2 && h.dbFIPHolder() == dbServer[l]
}

// apiProbe asks the api's /readyz (its database ping) every 250 ms: the longest run of
// failures, and how many failed.
type apiProbe struct {
	stop    chan struct{}
	done    chan struct{}
	failed  atomic.Int64
	ok      atomic.Int64
	longest time.Duration
}

func startAPIProbe(url string) *apiProbe {
	p := &apiProbe{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		var downSince time.Time
		c := &http.Client{Timeout: 2 * time.Second}
		for {
			select {
			case <-p.stop:
				return
			case <-time.After(250 * time.Millisecond):
			}
			resp, err := c.Get(url)
			good := err == nil && resp.StatusCode == 200
			if resp != nil {
				_ = resp.Body.Close()
			}
			switch {
			case good && !downSince.IsZero():
				if d := time.Since(downSince); d > p.longest {
					p.longest = d
				}
				downSince = time.Time{}
				p.ok.Add(1)
			case good:
				p.ok.Add(1)
			default:
				p.failed.Add(1)
				if downSince.IsZero() {
					downSince = time.Now()
				}
			}
		}
	}()
	return p
}

func (p *apiProbe) halt() time.Duration { close(p.stop); <-p.done; return p.longest }

// workerHeartbeat is the worker lock's holder and its last heartbeat, read on node.
func (h *ha) workerHeartbeat(node string) (string, time.Time) {
	db := h.db(node)
	defer func() { _ = db.Close() }()
	var who string
	var at time.Time
	_ = db.QueryRow(`SELECT node, heartbeat_at FROM cloud_leaders WHERE lock_id = 8472613`).Scan(&who, &at)
	return who, at
}

// pitrRows writes a row every 200 ms with its commit time until stop is closed.
type pitrRow struct {
	id int64
	at time.Time
}

func pitrWriter(db *sql.DB, stop <-chan struct{}) func() []pitrRow {
	var mu sync.Mutex
	var rows []pitrRow
	done := make(chan struct{})
	go func() {
		defer close(done)
		for id := int64(1); ; id++ {
			select {
			case <-stop:
				return
			case <-time.After(200 * time.Millisecond):
			}
			var at time.Time
			if err := db.QueryRow(`INSERT INTO rehearsal_pitr (id) VALUES ($1) RETURNING at`, id).Scan(&at); err == nil {
				mu.Lock()
				rows = append(rows, pitrRow{id, at})
				mu.Unlock()
			}
		}
	}()
	return func() []pitrRow { <-done; mu.Lock(); defer mu.Unlock(); return rows }
}

// T03, with the acceptance criteria around it (story 50.18): three database nodes, the app
// tier (a relay, two workers, an api) through PgBouncer on db.internal.
//   - each commit is on a replica (one synchronous standby of two);
//   - the synchronous replica stopped for maintenance: the other becomes synchronous and
//     writes go on waiting for it (RPO 0);
//   - the primary killed under traffic: the synchronous replica leads within 45 s, the
//     database's Floating IP points at it, no acknowledged commit is lost, the relay's homes
//     and streams come back, the api's database check passes again and the workers' lock
//     (through cloud_session) is held again, with no change to any app's settings;
//   - T05: the new leader backs up to both repositories, and a point-in-time restore from R2
//     brings back exactly the rows up to T.
func dbNodesFailover(t *testing.T, _ *cluster) Result {
	extra := &haExtra{
		files:      []string{"compose-db.yml"},
		nodes:      dbNodes,
		services:   []string{"db-1", "db-2", "db-3", "fakehcloud-db", "dbfront", "front", "s3-repo1", "s3-repo2"},
		env:        []string{"FRONT_TARGET=relay.test:8443", "DB_JOIN=now"},
		noEdgeWait: true,
		relay:      "relay-1",
		ready:      threeDBReady,
	}
	s3Backups(t, extra)
	h := upHAWith(t, "db-nodes-failover", extra)
	s3Buckets(t)
	h.mustHA("up", "-d", "relay", "worker-1", "worker-2", "api-1")
	waitFor(t, 2*time.Minute, "the relay through the edge", func() bool {
		resp, err := http.Get(h.edge + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == 200
	})
	apiURL := fmt.Sprintf("http://127.0.0.1:%d/readyz", portsFor(h.project)[0]+302)
	waitFor(t, time.Minute, "the api ready", func() bool {
		resp, err := http.Get(apiURL)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == 200
	})
	l := h.leader()
	waitFor(t, time.Minute, "a worker holding the lock through cloud_session", func() bool {
		who, at := h.workerHeartbeat(l)
		return who != "" && time.Since(at) < 30*time.Second
	})

	// AC 1 and 2: a commit waits for one replica; with the synchronous one down, the other
	sync0, _ := h.syncOf(l)
	if sync0 == "" {
		t.Fatalf("%s has no synchronous replica", l)
	}
	w := h.startWriter()
	time.Sleep(3 * time.Second)
	h.mustHA("stop", "-t", "10", sync0)
	var resynced time.Duration
	start := time.Now()
	waitFor(t, 90*time.Second, "the other replica synchronous", func() bool {
		s, _ := h.syncOf(l)
		if s != "" && s != sync0 {
			resynced = time.Since(start)
			return true
		}
		return false
	})
	time.Sleep(5 * time.Second)
	maintenanceHeld := w.held()
	h.mustHA("start", sync0)
	waitFor(t, 2*time.Minute, sync0+" back, streaming", func() bool { _, n := h.syncOf(l); return n == 2 })

	// T03: the primary killed under traffic
	n := envInt("REHEARSAL_HOMES", 100)
	slugs := h.homesOnHA(n)
	stop := h.load(slugs, envInt("REHEARSAL_STREAMS", 30))
	probe := startAPIProbe(apiURL)
	time.Sleep(5 * time.Second)
	cand, _ := h.syncOf(l)
	killed := time.Now()
	h.mustHA("kill", l)
	var promoted, ipMoved time.Duration
	waitFor(t, 90*time.Second, "a replica leading, the Floating IP on it", func() bool {
		nl := h.leader()
		if promoted == 0 && nl != "" && nl != l {
			promoted = time.Since(killed)
		}
		if promoted > 0 && ipMoved == 0 && h.dbFIPHolder() == dbServer[nl] {
			ipMoved = time.Since(killed)
		}
		return promoted > 0 && ipMoved > 0
	})
	nl := h.leader()
	waitFor(t, 2*time.Minute, "every home on the relay", func() bool { return h.tunnelsBy("relay-1") == n })
	var lockBack time.Duration
	waitFor(t, 2*time.Minute, "the workers' lock held again", func() bool {
		who, at := h.workerHeartbeat(nl)
		if who != "" && at.After(killed) {
			lockBack = time.Since(killed)
			return true
		}
		return false
	})
	time.Sleep(5 * time.Second)
	stats, down := stop()
	apiDown := probe.halt()
	acked := w.halt()
	missing := h.lost(nl, acked)
	t.Logf("maintenance: %s stopped, the other replica synchronous after %s, writes held at most %s", sync0, resynced, maintenanceHeld)
	t.Logf("%s killed: %s led after %s (the synchronous replica was %s), the Floating IP on it after %s; relay probe down %s, %d of %d requests failed; api down %s (%d of %d checks failed); the workers' lock back after %s; %d commits, %d lost",
		l, nl, promoted, cand, ipMoved, down, stats.Failed, stats.OK+stats.Failed, apiDown, probe.failed.Load(), probe.failed.Load()+probe.ok.Load(), lockBack, len(acked), len(missing))
	if nl != cand {
		t.Errorf("%s leads; the synchronous replica was %s", nl, cand)
	}
	// T03's budget is the promotion (as failover-kill-leader's); the Floating IP then follows
	// the new primary's callback (the stand-in Hetzner takes 5 s an assign)
	if promoted > 45*time.Second {
		t.Errorf("the synchronous replica led after %s, budget 45 s", promoted)
	}
	if ipMoved-promoted > 15*time.Second {
		t.Errorf("the Floating IP followed %s after the promotion, budget 15 s", ipMoved-promoted)
	}
	if len(missing) > 0 {
		t.Errorf("%d acknowledged commits lost: %v", len(missing), missing[:min(10, len(missing))])
	}
	if maintenanceHeld > 15*time.Second {
		t.Errorf("writes held %s while a replica was down for maintenance", maintenanceHeld)
	}
	if down > 2*time.Minute || apiDown > 2*time.Minute {
		t.Errorf("relay down %s, api down %s: budget 2 min", down, apiDown)
	}

	// T05: backups keep running from the new layout, and a point-in-time restore works
	backup, err := exec.Command("docker", "exec", "-u", "postgres", "-e", "PATRONI_URL=http://127.0.0.1:8008", "-e", "CLOUD_HA_ENV=/nonexistent",
		h.project+"-"+nl+"-1", "maktaba-backup", "full").CombinedOutput()
	if err != nil {
		t.Fatalf("the full backup on %s: %v\n%s", nl, err, backup)
	}
	db := h.db(nl)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS rehearsal_pitr (id bigint PRIMARY KEY, at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	pstop := make(chan struct{})
	collect := pitrWriter(db, pstop)
	time.Sleep(15 * time.Second)
	target := time.Now().UTC()
	time.Sleep(15 * time.Second)
	close(pstop)
	rows := collect()
	h.waitArchived(nl)
	time.Sleep(5 * time.Second)
	var want int
	var lastBefore int64
	for _, r := range rows {
		if !r.at.After(target) {
			want++
			lastBefore = r.id
		}
	}
	got, err := h.restoreAt(l, 2, target, "SELECT count(*), coalesce(max(id),0), coalesce(min(id),0) FROM rehearsal_pitr")
	if err != nil {
		t.Fatalf("restore from repo2: %v\n%s", err, got)
	}
	wantLine := fmt.Sprintf("%d|%d|1", want, lastBefore)
	t.Logf("the new leader backed up; %d rows written, %d up to T; restored from R2 %q (want %q)", len(rows), want, got, wantLine)
	if got != wantLine {
		t.Errorf("restored %q from repo2, want %q (count|max id|min id up to T)", got, wantLine)
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: (2 * time.Minute).Milliseconds(), FailedRequests: stats.Failed,
		Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("the synchronous replica led with the Floating IP after %s; %d commits, %d lost; api down %s; workers' lock back after %s; replica maintenance held writes %s; PITR from R2 exact",
			ipMoved.Round(time.Millisecond), len(acked), len(missing), apiDown.Round(time.Millisecond), lockBack.Round(time.Second), maintenanceHeld.Round(time.Millisecond))}}
}

// T04 (story 50.18 §4): the Phase 1 pair serving 100 homes and 30 streams, with a writer;
// deploy/cloud-ha/bin/migrate-db-nodes all moves the database to db-1..3 (join, the pair's
// cloud stops following, the database's Floating IP, repoint, switchover, db-3, etcd, retire,
// backup). Writes are held at most 15 s and remote play pauses at most 15 s; no acknowledged
// commit is lost; db-1 leads with a synchronous replica, the pair has left Patroni and etcd,
// and the new leader took a full backup to both repositories.
func dbNodesMigrate(t *testing.T, _ *cluster) Result {
	all := []string{"node-a", "node-b", "db-1", "db-2", "db-3"}
	extra := &haExtra{
		files:    []string{"compose-db.yml"},
		nodes:    all,
		services: []string{"witness", "node-a", "node-b", "fakehcloud", "front", "db-1", "db-2", "db-3", "fakehcloud-db", "dbfront", "s3-repo1", "s3-repo2"},
		env: []string{"DB_JOIN=later", "PAIR_DB_FLOATING_IP_ID=8",
			"DB_ETCD_HOSTS=10.60.0.11:2379,10.60.0.12:2379,10.60.0.13:2379,10.60.0.31:2379,10.60.0.32:2379,10.60.0.33:2379"},
		ready: func(h *ha) bool {
			l := h.leader()
			return (l == "node-a" || l == "node-b") && h.syncStandby(other(l))
		},
	}
	s3Backups(t, extra)
	h := upHAWith(t, "db-nodes-migrate", extra)
	s3Buckets(t)
	old := h.leader()
	n := envInt("REHEARSAL_HOMES", 100)
	slugs := h.homesOnHA(n)
	w := h.startWriter()
	stop := h.load(slugs, envInt("REHEARSAL_STREAMS", 30))
	time.Sleep(5 * time.Second)

	execScript := filepath.Join(t.TempDir(), "node-exec")
	body := fmt.Sprintf("#!/bin/sh\nexec docker exec %s-\"$1\"-1 bash -c \"$2\"\n", h.project)
	if err := os.WriteFile(execScript, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "deploy/cloud-ha/bin/migrate-db-nodes"), "all", "--announced")
	cmd.Env = append(os.Environ(), "RELAY_HOST_A=node-a", "RELAY_HOST_B=node-b", "DB_HOSTS=db-1 db-2 db-3", "WITNESS_HOST=witness",
		"NODE_EXEC="+execScript, "SUDO=", "SVC=/opt/rehearsal-node/svc.sh", "PATRONI_URL=http://127.0.0.1:8008",
		"PATRONICTL=patronictl -c /etc/patroni/patroni.yml", "CLOUD_RESTART=true", "DB_SSLMODE=disable", "WAIT=180",
		"BACKUP_CMD=gosu postgres env CLOUD_HA_ENV=/etc/maktaba/cloud-ha.env maktaba-backup full")
	out, err := cmd.CombinedOutput()
	took := time.Since(start)
	if err != nil {
		t.Fatalf("migrate-db-nodes all: %v after %s\n%s", err, took, out)
	}
	waitFor(t, 3*time.Minute, "every home back on "+old+"'s relay", func() bool { return h.tunnelsOn(old) == n })
	time.Sleep(5 * time.Second)
	stats, down := stop()
	held := w.held()
	acked := w.halt()
	missing := h.lost("db-1", acked)

	// the new layout
	members, _ := exec.Command("docker", "exec", h.project+"-db-1-1", "patronictl", "-c", "/etc/patroni/patroni.yml", "list", "-f", "json").Output()
	etcd, _ := exec.Command("docker", "exec", h.project+"-db-1-1", "sh", "-c",
		"etcdctl --endpoints=https://127.0.0.1:2379 --cacert=/etc/etcd/pki/ca.crt --cert=/etc/etcd/pki/db-1.crt --key=/etc/etcd/pki/db-1.key member list -w simple | awk -F', ' '{print $3}'").Output()
	etcdNames := strings.Fields(string(etcd))
	sort.Strings(etcdNames)
	sync, streaming := h.syncOf("db-1")
	t.Logf("migrate-db-nodes all took %s; writes held at most %s; remote play paused %s; %d of %d requests failed; %d commits, %d lost; members %s; etcd %v; db-1's synchronous replica %s of %d\n%s",
		took, held, down, stats.Failed, stats.OK+stats.Failed, len(acked), len(missing), members, etcdNames, sync, streaming, out)
	if h.leader() != "db-1" {
		t.Errorf("leader after the move: %q, want db-1", h.leader())
	}
	if sync == "" || streaming != 2 {
		t.Errorf("db-1 has synchronous replica %q of %d streaming, want one of two", sync, streaming)
	}
	if strings.Contains(string(members), `"node-a"`) || strings.Contains(string(members), `"node-b"`) {
		t.Errorf("the pair is still in Patroni's list: %s", members)
	}
	if strings.Join(etcdNames, " ") != "db-1 db-2 db-3" {
		t.Errorf("etcd's members: %v, want db-1 db-2 db-3", etcdNames)
	}
	if hold := h.dbFIPHolder(); hold != dbServer["db-1"] {
		t.Errorf("the database's Floating IP is on server %d, want %d (db-1)", hold, dbServer["db-1"])
	}
	if held > 15*time.Second {
		t.Errorf("writes held %s, budget 15 s", held)
	}
	if down > 15*time.Second {
		t.Errorf("remote play paused %s, budget 15 s", down)
	}
	if len(missing) > 0 {
		t.Errorf("%d acknowledged commits lost: %v", len(missing), missing[:min(10, len(missing))])
	}
	if !strings.Contains(string(out), "backup: a full backup from db-1") {
		t.Errorf("no full backup from db-1 after the move")
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: 15000, FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("the move took %s; writes held at most %s; %d commits, %d lost; db-1 leads, the pair gone from Patroni and etcd, a full backup from db-1",
			took.Round(time.Second), held.Round(time.Millisecond), len(acked), len(missing))}}
}
