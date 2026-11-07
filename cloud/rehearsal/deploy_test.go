//go:build cloudrehearsal

package rehearsal

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Deploys with no downtime (story 50.08): deploy/cloud-ha/bin/bluegreen, the script the
// leader runs, against the rehearsal's edge and relay processes.

// bluegreen runs the deploy script for tag with the rehearsal's commands; blue serves first.
func (c *cluster) bluegreen(t *testing.T, tag string, watch time.Duration) (string, error) {
	t.Helper()
	p := portsFor(c.project)
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "colour"), []byte("blue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(state, "deployed"), []byte("rehearsal\n"), 0o644)
	bg := filepath.Join(c.dir, "bg.sh")
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "deploy/cloud-ha/bin/bluegreen"), tag)
	cmd.Env = append(os.Environ(), c.env()...)
	cmd.Env = append(cmd.Env,
		"BG_PROJECT="+c.project,
		"STATE_DIR="+state,
		"START_CMD=bash "+bg+" start",
		"STOP_CMD=bash "+bg+" stop",
		fmt.Sprintf("READY_URL_blue=http://127.0.0.1:%d/readyz", p[1]+500),
		fmt.Sprintf("READY_URL_green=http://127.0.0.1:%d/readyz", p[1]+501),
		"READY_STABLE=25", fmt.Sprintf("WATCH=%d", int(watch.Seconds())), "CHECK_EVERY=2",
	)
	out, err := cmd.CombinedOutput()
	colour, _ := os.ReadFile(filepath.Join(state, "colour"))
	t.Logf("bluegreen %s: %v\n%s(colour now %s)", tag, err, out, strings.TrimSpace(string(colour)))
	return strings.TrimSpace(string(colour)), err
}

// version is what the edge's relay says it is.
func (c *cluster) version() string {
	req, _ := http.NewRequest(http.MethodGet, c.edge+"/.well-known/maktaba-cloud-version", nil)
	req.Host = "relay.test"
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// C05 deploy-blue-green: a new release over the running one with 1,000 homes and 100
// streams. No request fails, every home is on the new colour, no tunnel is dropped without
// a DRAIN, and the new release answers /version.
func deployBlueGreen(t *testing.T, c *cluster) Result {
	c.must("stop", "relay-green")
	n := envInt("REHEARSAL_HOMES", 1000)
	slugs := c.homesOnline(n)
	stop := c.load(slugs, envInt("REHEARSAL_STREAMS", 100))
	time.Sleep(5 * time.Second)
	start := time.Now()
	colour, err := c.bluegreen(t, "new", 30*time.Second)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("the deploy failed: %v", err)
	}
	waitFor(t, time.Minute, "every home on green", func() bool { return c.tunnels("a-green") == n })
	time.Sleep(3 * time.Second)
	stats, down := stop()
	v := c.version()
	t.Logf("deployed in %s (colour %s, version %q); %d requests, %d failed; probe down %s", took, colour, v, stats.OK+stats.Failed, stats.Failed, down)
	if colour != "green" || !strings.Contains(v, `"version":"new"`) {
		t.Errorf("after the deploy: colour %s, version %s", colour, v)
	}
	if stats.Failed > 0 {
		t.Errorf("%d requests failed; first: %+v", stats.Failed, stats.Failures[0])
	}
	if down > 0 {
		t.Errorf("the probe was down %s, budget 0", down)
	}
	logs, _ := c.compose("logs", "--no-color", "relay-blue")
	if strings.Contains(logs, "tunnel_drain_forced") {
		t.Error("blue force-closed a tunnel")
	}
	return Result{DowntimeMS: down.Milliseconds(), FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{fmt.Sprintf("deployed in %s; %d homes moved to green", took, n)}}
}

// C06 deploy-bad-release-rollback: a release that fails its readiness 20 s after it starts.
// The deploy stops before the edge or blue is touched, green is removed, blue keeps
// serving, no request fails, and the deploy exits non-zero.
func deployBadRelease(t *testing.T, c *cluster) Result {
	c.must("stop", "relay-green")
	n := envInt("REHEARSAL_HOMES", 300)
	slugs := c.homesOnline(n)
	stop := c.load(slugs, envInt("REHEARSAL_STREAMS", 50))
	time.Sleep(5 * time.Second)
	colour, err := c.bluegreen(t, "bad", 30*time.Second)
	time.Sleep(3 * time.Second)
	stats, down := stop()
	if err == nil {
		t.Error("the bad release's deploy exited 0")
	}
	if colour != "blue" {
		t.Errorf("colour after the failed deploy: %s", colour)
	}
	if c.running("relay-green") {
		t.Error("green still runs")
	}
	if got := c.tunnels("a-blue"); got != n {
		t.Errorf("%d of %d homes on blue", got, n)
	}
	t.Logf("bad release refused; %d requests, %d failed; probe down %s", stats.OK+stats.Failed, stats.Failed, down)
	if stats.Failed > 0 {
		t.Errorf("%d requests failed; first: %+v", stats.Failed, stats.Failures[0])
	}
	return Result{DowntimeMS: down.Milliseconds(), FailedRequests: stats.Failed, Requests: stats.OK + stats.Failed,
		Notes: []string{"the bad release stopped at its readiness; blue untouched"}}
}
