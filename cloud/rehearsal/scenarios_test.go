//go:build cloudrehearsal

// Package rehearsal is the cloud rehearsal (Epic 50 story 50.06): the Phase 1
// cluster in Docker (tools/rehearsal-cloud/compose.yml), broken on purpose,
// each scenario measured against its budget. Run one scenario:
//
//	docker build -f tools/rehearsal-cloud/Dockerfile -t maktaba-cloud:rehearsal .
//	cd cloud && go test -tags cloudrehearsal -run 'TestScenario/drain-1k' -timeout 30m ./rehearsal/
//
// Never against a live machine: everything it touches is in its compose project.
package rehearsal

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/streamgen"
)

// Result is one scenario's verdict file (results/<scenario>.json).
type Result struct {
	Scenario       string   `json:"scenario"`
	Passed         bool     `json:"passed"`
	DowntimeMS     int64    `json:"downtime_ms"`
	BudgetMS       int64    `json:"budget_ms"`
	FailedRequests int64    `json:"failed_requests"`
	Requests       int64    `json:"requests"`
	ReconnectP50MS int64    `json:"reconnect_p50_ms,omitempty"`
	ReconnectP95MS int64    `json:"reconnect_p95_ms,omitempty"`
	ReconnectP99MS int64    `json:"reconnect_p99_ms,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

// scenarios not built yet: the story that builds each (expected-failures.txt).
func notBuilt(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	f, err := os.Open(filepath.Join(repoRoot(t), "tools/rehearsal-cloud/expected-failures.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			out[fields[0]] = strings.Join(fields[1:], " ")
		}
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// TestScenario runs every scenario (C01–C10); one with -run 'TestScenario/<name>'.
func TestScenario(t *testing.T) {
	skip := notBuilt(t)
	for _, s := range []struct {
		name string
		run  func(*testing.T, *cluster) Result
	}{
		{"drain-1k", drain1k},
		{"failover-kill-leader", failoverKillLeader},
		{"failover-network-partition", failoverNetworkPartition},
		{"switchover", switchoverScenario},
		{"deploy-blue-green", deployBlueGreen},
		{"deploy-bad-release-rollback", deployBadRelease},
		{"migration-n-minus-1", nil},
		{"db-blip", dbBlip},
		{"restore-pitr", nil},
		{"cert-on-standby", certOnStandby},
	} {
		t.Run(s.name, func(t *testing.T) {
			if why, ok := skip[s.name]; ok || s.run == nil {
				writeResult(t, Result{Scenario: s.name, Passed: false, Notes: []string{"not built yet: " + why}})
				t.Skipf("not built yet: %s", why)
			}
			var c *cluster
			switch s.name {
			case "cert-on-standby":
				c = upOnly(t, s.name)
			case "failover-kill-leader", "failover-network-partition", "switchover":
				// the pair's own cluster (compose-ha.yml), started by the scenario
			default:
				c = up(t, s.name)
			}
			r := s.run(t, c)
			r.Scenario = s.name
			r.Passed = !t.Failed()
			writeResult(t, r)
		})
	}
}

func writeResult(t *testing.T, r Result) {
	t.Helper()
	dir := outDir(t)
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, r.Scenario+".json"), b, 0o644); err != nil {
		t.Error(err)
	}
	t.Logf("result: %s", b)
}

func outDir(t *testing.T) string {
	dir := os.Getenv("REHEARSAL_OUT")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "cloud-rehearsal")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// cluster is one scenario's compose project.
type cluster struct {
	t       *testing.T
	project string
	dir     string // tools/rehearsal-cloud
	edge    string // http://127.0.0.1:<port>
	db      *sql.DB
	api     *httptestServer
	homes   []*simhome.Home
	mu      sync.Mutex
}

type httptestServer struct {
	URL   string
	close func()
}

func (c *cluster) compose(args ...string) (string, error) {
	cmd := exec.Command("docker", append([]string{"compose", "-p", c.project, "-f", filepath.Join(c.dir, "compose.yml")}, args...)...)
	cmd.Env = append(os.Environ(), c.env()...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (c *cluster) env() []string {
	ports := portsFor(c.project)
	img := os.Getenv("CLOUD_IMAGE")
	if img == "" {
		img = "maktaba-cloud:rehearsal"
	}
	caddy := os.Getenv("CADDY_IMAGE")
	if caddy == "" {
		caddy = "maktaba-caddy:rehearsal"
	}
	return []string{"CLOUD_IMAGE=" + img, "CADDY_IMAGE=" + caddy, fmt.Sprintf("PG_PORT=%d", ports[0]), fmt.Sprintf("EDGE_PORT=%d", ports[1]),
		fmt.Sprintf("CERT_A_PORT=%d", ports[1]+1000), fmt.Sprintf("CERT_B_PORT=%d", ports[1]+1001),
		fmt.Sprintf("BLUE_PORT=%d", ports[1]+500), fmt.Sprintf("GREEN_PORT=%d", ports[1]+501), fmt.Sprintf("EDGE_ADMIN_PORT=%d", ports[1]+502)}
}

// portsFor gives each scenario its own host ports (they may run on one machine).
func portsFor(project string) [2]int {
	h := 0
	for _, r := range project {
		h = (h*31 + int(r)) % 1000
	}
	return [2]int{15000 + h, 18000 + h}
}

func (c *cluster) must(args ...string) string {
	c.t.Helper()
	out, err := c.compose(args...)
	if err != nil {
		c.t.Fatalf("docker compose %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// up starts the scenario's cluster and registers its teardown (logs kept first).
func up(t *testing.T, scenario string) *cluster {
	t.Helper()
	c := &cluster{t: t, project: "mr-" + scenario, dir: filepath.Join(repoRoot(t), "tools/rehearsal-cloud")}
	ports := portsFor(c.project)
	c.edge = fmt.Sprintf("http://127.0.0.1:%d", ports[1])
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
	c.must("up", "-d", "--wait")
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

// upOnly is a scenario's compose project with nothing started (it starts its own
// services), torn down and its logs kept like up's.
func upOnly(t *testing.T, scenario string) *cluster {
	t.Helper()
	c := &cluster{t: t, project: "mr-" + scenario, dir: filepath.Join(repoRoot(t), "tools/rehearsal-cloud")}
	_, _ = c.compose("--profile", "certs", "down", "-v", "--remove-orphans")
	t.Cleanup(func() {
		logs, _ := c.compose("--profile", "certs", "logs", "--no-color", "--timestamps")
		_ = os.WriteFile(filepath.Join(outDir(t), scenario+".log"), []byte(logs), 0o644)
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = c.compose("--profile", "certs", "down", "-v", "--remove-orphans")
		}
	})
	return c
}

// homesOnline seeds n homes and runs them against the edge until each has a tunnel.
func (c *cluster) homesOnline(n int) []string {
	c.t.Helper()
	ctx := context.Background()
	ids, err := seed.Homes(ctx, c.db, 0, n, "pro")
	if err != nil {
		c.t.Fatal(err)
	}
	ws := "ws" + strings.TrimPrefix(c.edge, "http") + "/v1/relay/ws"
	slugs := make([]string, n)
	for i := range n {
		_, _, slug := simhome.Creds(i)
		h := simhome.Start(i, ws, c.api.URL, simhome.Options{ServerID: ids[slug], BaseWait: 500 * time.Millisecond, MaxWait: 10 * time.Second, PingEvery: 20 * time.Second})
		c.mu.Lock()
		c.homes = append(c.homes, h)
		c.mu.Unlock()
		slugs[i] = h.Slug
	}
	waitFor(c.t, 3*time.Minute, fmt.Sprintf("%d homes online", n), func() bool { return c.tunnels("") == n })
	return slugs
}

func (c *cluster) stopHomes() {
	c.mu.Lock()
	homes := c.homes
	c.homes = nil
	c.mu.Unlock()
	var wg sync.WaitGroup
	for _, h := range homes {
		wg.Add(1)
		go func() { defer wg.Done(); h.Stop() }()
	}
	wg.Wait()
}

// tunnels counts the directory's rows (on relayID, or all).
func (c *cluster) tunnels(relayID string) int {
	var n int
	q := `SELECT count(*) FROM relay_tunnels`
	args := []any{}
	if relayID != "" {
		q += ` WHERE relay_id = $1`
		args = append(args, relayID)
	}
	if err := c.db.QueryRow(q, args...).Scan(&n); err != nil {
		return -1
	}
	return n
}

func (c *cluster) running(service string) bool {
	out, _ := c.compose("ps", "--status", "running", "--services")
	for _, s := range strings.Fields(out) {
		if s == service {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// load starts the stream generator and the probe; stop returns what they saw.
func (c *cluster) load(slugs []string, clients int) (stop func() (streamgen.Stats, time.Duration)) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &streamgen.Gen{Edge: c.edge, Domain: "relay.test", Slugs: shuffled(slugs), Clients: clients,
		Segment: 256 << 10, Every: 2 * time.Second, SegmentMS: 300}
	p := &streamgen.Probe{Edge: c.edge, Host: slugs[0] + ".relay.test", Path: "/api/health"}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); g.Run(ctx) }()
	go func() { defer wg.Done(); p.Run(ctx) }()
	return func() (streamgen.Stats, time.Duration) {
		cancel()
		wg.Wait()
		d, _ := p.Downtime()
		return g.Stats(), d
	}
}

func shuffled(s []string) []string {
	out := append([]string(nil), s...)
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// C01 drain-1k: 1,000 homes and 100 streams on relay process blue; blue gets
// SIGTERM while the edge sends new work to green. Every home is on green
// within 15 s, no request fails, and blue exits within 150 s.
func drain1k(t *testing.T, c *cluster) Result {
	n := envInt("REHEARSAL_HOMES", 1000)
	slugs := c.homesOnline(n)
	if got := c.tunnels("a-blue"); got != n {
		t.Fatalf("%d of %d homes on blue (the edge's first upstream)", got, n)
	}
	stop := c.load(slugs, envInt("REHEARSAL_STREAMS", 100))
	time.Sleep(5 * time.Second)

	start := time.Now()
	c.must("kill", "-s", "SIGTERM", "relay-blue")
	var moved time.Duration
	waitFor(t, 60*time.Second, "every home on green", func() bool {
		if c.tunnels("a-green") == n && c.tunnels("a-blue") == 0 {
			moved = time.Since(start)
			return true
		}
		return false
	})
	waitFor(t, 160*time.Second, "blue to exit", func() bool { return !c.running("relay-blue") })
	exited := time.Since(start)
	time.Sleep(5 * time.Second)
	stats, down := stop()

	t.Logf("%d homes moved in %s; blue exited after %s; %d requests, %d failed; probe down %s", n, moved, exited, stats.OK+stats.Failed, stats.Failed, down)
	if moved > 15*time.Second {
		t.Errorf("the move took %s, budget 15 s", moved)
	}
	if exited > 150*time.Second {
		t.Errorf("blue exited after %s, budget 150 s", exited)
	}
	if stats.Failed > 0 {
		t.Errorf("%d requests failed; first: %+v", stats.Failed, stats.Failures[0])
	}
	if down > 0 {
		t.Errorf("the probe was down %s, budget 0", down)
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: 0, FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("moved %d homes in %s; blue exited after %s", n, moved, exited)}}
}

// C08 db-blip: 300 homes streaming; the database goes away for 15 s (50.07
// makes this a Patroni switchover). The relay serves the homes it knows
// throughout, and its metering equals what the stream generator received.
func dbBlip(t *testing.T, c *cluster) Result {
	n := envInt("REHEARSAL_HOMES", 300)
	slugs := c.homesOnline(n)
	stop := c.load(slugs, envInt("REHEARSAL_STREAMS", 100))
	time.Sleep(10 * time.Second) // every home served once: in the relay's cache

	c.must("kill", "postgres")
	time.Sleep(15 * time.Second)
	c.must("start", "postgres")
	waitFor(t, time.Minute, "the database back", func() bool { return c.db.Ping() == nil })
	time.Sleep(5 * time.Second)
	stats, down := stop()

	// the relay's meter flushes every 30 s and retries every 5 s after a failure
	var metered int64
	waitFor(t, 2*time.Minute, "the metering within 1 %", func() bool {
		_ = c.db.QueryRow(`SELECT coalesce(sum(bytes_out), 0) FROM bandwidth_monthly`).Scan(&metered)
		return within(metered, stats.Bytes, 0.01)
	})
	t.Logf("%d requests, %d failed through a 15 s database outage; probe down %s; metered %d of %d bytes", stats.OK+stats.Failed, stats.Failed, down, metered, stats.Bytes)
	if stats.Failed > 0 {
		t.Errorf("%d requests failed; first: %+v", stats.Failed, stats.Failures[0])
	}
	if down > 0 {
		t.Errorf("the probe was down %s, budget 0", down)
	}
	return Result{DowntimeMS: down.Milliseconds(), BudgetMS: 0, FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("metered %d bytes out, the generator received %d", metered, stats.Bytes)}}
}

// within: got is within frac of want (and not below it by more).
func within(got, want int64, frac float64) bool {
	if want == 0 {
		return got == 0
	}
	d := float64(got-want) / float64(want)
	return d >= -frac && d <= frac
}

func envInt(k string, def int) int {
	var v int
	if _, err := fmt.Sscan(os.Getenv(k), &v); err == nil && v > 0 {
		return v
	}
	return def
}

// C10 cert-on-standby (story 50.02): a fresh pair of node edges each gets its
// own wildcard certificate for relay.test and *.relay.test by DNS-01, within 2
// minutes and before any traffic, after the first CA (standing in for a failing
// Let's Encrypt) fails; a home's name is answered with the wildcard by both
// nodes, and no per-home certificate is ever requested.
func certOnStandby(t *testing.T, c *cluster) Result {
	work := filepath.Join(c.dir, ".work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	c.must("--profile", "certs", "up", "-d", "--wait", "pebble", "fakeacmedns")
	c.must("--profile", "certs", "cp", "pebble:/test/certs/pebble.minica.pem", filepath.Join(work, "pebble.minica.pem"))
	start := time.Now()
	c.must("--profile", "certs", "up", "-d", "cert-a", "cert-b")
	ports := portsFor(c.project)
	nodes := map[string]int{"a": ports[1] + 1000, "b": ports[1] + 1001}
	serials := map[string]string{}
	for node, port := range nodes {
		var cert *x509.Certificate
		waitFor(t, 2*time.Minute, "node "+node+"'s wildcard", func() bool {
			cert = peerCert(port, "anyhome.relay.test")
			return cert != nil && strings.Contains(cert.Issuer.CommonName, "Pebble")
		})
		// Caddy keeps one certificate per name: the wildcard for every home, and the
		// apex's own (two issuances per node every ~60 days, each name set well
		// inside Let's Encrypt's 5 duplicates a week)
		if !slices.Contains(cert.DNSNames, "*.relay.test") {
			t.Errorf("node %s: names %v, want *.relay.test", node, cert.DNSNames)
		}
		waitFor(t, 2*time.Minute, "node "+node+"'s apex certificate", func() bool {
			apex := peerCert(port, "relay.test")
			return apex != nil && slices.Contains(apex.DNSNames, "relay.test") && strings.Contains(apex.Issuer.CommonName, "Pebble")
		})
		for _, n := range cert.DNSNames {
			if n != "relay.test" && n != "*.relay.test" {
				t.Errorf("node %s: a per-home name %q", node, n)
			}
		}
		serials[node] = cert.SerialNumber.String()
		// another home's name: the same certificate, nothing new issued
		if other := peerCert(port, "otherhome.relay.test"); other == nil || other.SerialNumber.String() != serials[node] {
			t.Errorf("node %s: another home got another certificate", node)
		}
	}
	took := time.Since(start)
	if serials["a"] == serials["b"] {
		t.Error("the two nodes share one certificate; each must issue its own")
	}
	logs, _ := c.compose("--profile", "certs", "logs", "--no-color", "cert-a", "cert-b")
	if !strings.Contains(logs, "unreachable.test") {
		t.Error("the first CA was never tried (the fallback was not exercised)")
	}
	t.Logf("both nodes hold their own wildcard after %s (first CA failed, Pebble issued)", took)
	return Result{Notes: []string{fmt.Sprintf("both wildcards issued in %s; serials a=%s b=%s", took, serials["a"], serials["b"])}}
}

// peerCert is the certificate a TLS server on 127.0.0.1:port presents for sni.
func peerCert(port int, sni string) *x509.Certificate {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", fmt.Sprintf("127.0.0.1:%d", port),
		&tls.Config{ServerName: sni, InsecureSkipVerify: true}) //nolint:gosec // the test reads the certificate, Pebble's root is not trusted here
	if err != nil {
		return nil
	}
	defer func() { _ = conn.Close() }()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil
	}
	return certs[0]
}

func jsonDecode(resp *http.Response, v any) error { return json.NewDecoder(resp.Body).Decode(v) }
