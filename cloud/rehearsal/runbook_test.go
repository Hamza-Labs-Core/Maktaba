//go:build cloudrehearsal

package rehearsal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The one-click runbook actions (Epic 50 story 50.12) against the rehearsal pair: the same
// scripts the Cloud switchover, Cloud failover and Cloud DNS swap workflows run on the real
// one (deploy/cloud-ha/runbook/), reaching the nodes with docker exec instead of SSH.

// runbook runs deploy/cloud-ha/runbook/<script> against the pair, with env added.
func (h *ha) runbook(script string, env []string, args ...string) (string, error) {
	h.t.Helper()
	execScript := filepath.Join(h.t.TempDir(), "node-exec")
	body := fmt.Sprintf("#!/bin/sh\nexec docker exec %s-\"$1\"-1 bash -c \"$2\"\n", h.project)
	if err := os.WriteFile(execScript, []byte(body), 0o755); err != nil {
		h.t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{filepath.Join(repoRoot(h.t), "deploy/cloud-ha/runbook", script)}, args...)...)
	cmd.Env = append(append(os.Environ(),
		"RELAY_HOST_A=node-a", "RELAY_HOST_B=node-b", "NODE_EXEC="+execScript, "SUDO=",
		"PATRONI_URL=http://127.0.0.1:8008"), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// T04 runbook-switchover: the Cloud switchover workflow's script moves the leader under
// traffic; remote play pauses at most 15 s and no acknowledged commit is lost.
func runbookSwitchover(t *testing.T, _ *cluster) Result {
	h := upHA(t, "runbook-switchover")
	n := envInt("REHEARSAL_HOMES", 50)
	slugs := h.homesOnHA(n)
	w := h.startWriter()
	stop := h.load(slugs, envInt("REHEARSAL_STREAMS", 20))
	time.Sleep(5 * time.Second)
	old := h.leader()
	start := time.Now()
	out, err := h.runbook("switchover.sh", nil, "--announced")
	if err != nil {
		t.Fatalf("switchover.sh: %v\n%s", err, out)
	}
	took := time.Since(start)
	waitFor(t, 3*time.Minute, "every home back", func() bool { return h.tunnelsOn(other(old)) == n })
	time.Sleep(5 * time.Second)
	stats, down := stop()
	missing := h.lost(other(old), w.halt())
	t.Logf("switchover.sh %s -> %s in %s; probe paused %s; %d of %d requests failed\n%s", old, other(old), took, down, stats.Failed, stats.OK+stats.Failed, out)
	if h.leader() != other(old) {
		t.Errorf("leader after the switchover: %q", h.leader())
	}
	if down > 15*time.Second {
		t.Errorf("remote play paused %s, budget 15 s", down)
	}
	if len(missing) > 0 {
		t.Errorf("%d acknowledged commits lost", len(missing))
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: 15000, FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("the workflow's switchover.sh took %s", took.Round(time.Second))}}
}

// T06 runbook-forced-failover: the Cloud failover workflow's script, with "stop the old
// leader": the standby leads and takes writes, the old leader neither leads nor takes writes,
// and the homes come back on the new leader.
func runbookForcedFailover(t *testing.T, _ *cluster) Result {
	h := upHA(t, "runbook-forced-failover")
	n := envInt("REHEARSAL_HOMES", 50)
	h.homesOnHA(n)
	old := h.leader()
	start := time.Now()
	out, err := h.runbook("failover.sh", []string{"STOP_OLD=pkill -TERM -f 'patroni /etc/patroni/patroni.yml'"}, "--stop-old")
	if err != nil {
		t.Fatalf("failover.sh: %v\n%s", err, out)
	}
	took := time.Since(start)
	waitFor(t, 3*time.Minute, "every home on the new leader", func() bool { return h.tunnelsOn(other(old)) == n })
	back := time.Since(start)
	t.Logf("failover.sh %s -> %s in %s; homes back in %s\n%s", old, other(old), took, back, out)
	if h.leader() != other(old) {
		t.Errorf("leader after the failover: %q", h.leader())
	}
	nb := h.db(other(old))
	defer func() { _ = nb.Close() }()
	if !writable(nb) {
		t.Error("the new leader takes no writes")
	}
	waitFor(t, time.Minute, "the old leader fenced", func() bool {
		db := h.db(old)
		defer func() { _ = db.Close() }()
		return !writable(db)
	})
	return Result{DowntimeMS: back.Milliseconds(), BudgetMS: (3 * time.Minute).Milliseconds(), Requests: int64(n),
		Notes: []string{fmt.Sprintf("failover.sh took %s; every home on %s after %s; the old leader takes no writes", took.Round(time.Second), other(old), back.Round(time.Second))}}
}

// fakeCloudflare stands in for the two Cloudflare DNS calls dns-swap.sh makes.
type fakeCloudflare struct {
	mu   sync.Mutex
	recs map[string]map[string]any // id -> record
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.Header.Get("Authorization") != "Bearer rehearsal-cf-token" {
		reply(403, map[string]any{"success": false})
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones/z1/dns_records":
		var out []map[string]any
		for _, rec := range f.recs {
			if rec["name"] == r.URL.Query().Get("name") {
				out = append(out, rec)
			}
		}
		reply(200, map[string]any{"success": true, "result": out})
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/zones/z1/dns_records/"):
		rec, ok := f.recs[strings.TrimPrefix(r.URL.Path, "/zones/z1/dns_records/")]
		if !ok {
			reply(404, map[string]any{"success": false})
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		for k, v := range body {
			rec[k] = v
		}
		reply(200, map[string]any{"success": true, "result": rec})
	default:
		reply(404, map[string]any{"success": false})
	}
}

func (f *fakeCloudflare) content(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, _ := f.recs[id]["content"].(string)
	return s
}

// T05 runbook-dns-swap: the Hetzner API down (the fake answers 503) and the leader killed:
// Patroni fails over, but the Floating IP can't follow. The Cloud DNS swap workflow's script
// points relay.test and *.relay.test at the new leader's own address through a stand-in
// Cloudflare, and the relay answers there.
func runbookDNSSwap(t *testing.T, _ *cluster) Result {
	h := upHA(t, "runbook-dns-swap")
	old := h.leader()
	p := portsFor(h.project)
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/fake/outage", p[0]+300), bytes.NewBufferString(`{"down":true}`))
	req.Header.Set("Authorization", "Bearer rehearsal-hcloud-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the Hetzner API outage: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("the Hetzner API outage: HTTP %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	start := time.Now()
	h.mustHA("kill", old)
	waitFor(t, 2*time.Minute, "the other node leads", func() bool { return h.leader() == other(old) })
	promoted := time.Since(start)

	fipAddr := map[string]string{"node-a": "10.61.0.11", "node-b": "10.61.0.12"}
	cf := &fakeCloudflare{recs: map[string]map[string]any{
		"r1": {"id": "r1", "name": "relay.test", "type": "A", "content": "10.61.0.40", "ttl": 300},
		"r2": {"id": "r2", "name": "*.relay.test", "type": "A", "content": "10.61.0.40", "ttl": 300},
	}}
	srv := httptest.NewServer(cf)
	defer srv.Close()
	out, err := h.runbook("dns-swap.sh", []string{
		"CF_API=" + srv.URL, "CF_API_TOKEN=rehearsal-cf-token", "CF_ZONE_ID=z1", "RELAY_DOMAIN=relay.test",
		"PUBLIC_IP_A=" + fipAddr["node-a"], "PUBLIC_IP_B=" + fipAddr["node-b"],
	}, "--to", "leader")
	if err != nil {
		t.Fatalf("dns-swap.sh: %v\n%s", err, out)
	}
	want := fipAddr[other(old)]
	if cf.content("r1") != want || cf.content("r2") != want {
		t.Fatalf("the records point at %s and %s, want %s\n%s", cf.content("r1"), cf.content("r2"), want, out)
	}
	// the relay answers at the address the names now point at (a client on the fip network)
	var ready time.Duration
	waitFor(t, 2*time.Minute, "the relay ready at "+want, func() bool {
		c := exec.Command("docker", "run", "--rm", "--network", h.project+"_fip", "curlimages/curl:8.11.1",
			"-fsS", "-o", "/dev/null", "--max-time", "3", "-H", "Host: relay.test", "http://"+want+"/readyz")
		if c.Run() == nil {
			ready = time.Since(start)
			return true
		}
		return false
	})
	t.Logf("leader %s killed with the Hetzner API down; %s led after %s; dns-swap.sh pointed the names at %s; the relay ready there after %s\n%s",
		old, other(old), promoted, want, ready, out)
	return Result{DowntimeMS: ready.Milliseconds(), BudgetMS: (5 * time.Minute).Milliseconds(),
		Notes: []string{fmt.Sprintf("the Floating IP stuck (fake Hetzner API 503); names on %s's address; relay ready after %s", other(old), ready.Round(time.Second))}}
}
