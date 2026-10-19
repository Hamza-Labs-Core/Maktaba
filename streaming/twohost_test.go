package main

// Epic 32 end to end on one machine: two streaming processes (the home and a node,
// different ports and host ids) against one scratch Postgres, real ffmpeg (slates:
// the channels have no programmes). It proves
//
//   - one encode per channel cluster-wide (each channel runs on exactly one host);
//   - placement on the node when the home is full (home_first);
//   - the front door proxies the node's HLS (playlist and segments) and TS;
//   - failover: the node process is killed, the TS client on the home stays
//     connected and keeps receiving, the channel continues on the home at the
//     wall-clock position (its playlist's program date-time is now) with an HLS
//     discontinuity.
//
// Runs only with MAKTABA_TEST_PG_URL (a Postgres whose user may create databases)
// and ffmpeg on PATH; never in -short.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/pgtest"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type proc struct {
	cmd  *exec.Cmd
	logs *os.File
	name string
}

func startStreaming(t *testing.T, bin, name string, env []string) *proc {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), name+".log")
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "serve")
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout, cmd.Stderr = lf, lf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &proc{cmd: cmd, logs: lf, name: name}
	t.Cleanup(func() {
		p.kill()
		_ = lf.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			lines := strings.Split(string(b), "\n")
			if len(lines) > 60 {
				lines = lines[len(lines)-60:]
			}
			t.Logf("--- %s log (tail) ---\n%s", name, strings.Join(lines, "\n"))
		}
	})
	return p
}

func (p *proc) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
}

func waitFor(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

var pdtRe = regexp.MustCompile(`#EXT-X-PROGRAM-DATE-TIME:(\S+)`)

func TestTwoHostsOneMachine(t *testing.T) {
	if os.Getenv("MAKTABA_TEST_PG_URL") == "" || testing.Short() {
		t.Skip("MAKTABA_TEST_PG_URL unset (or -short): skipping the two-host test")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dsn, pool := pgtest.ScratchDB(t)
	bin := filepath.Join(t.TempDir(), "maktaba-streaming")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command(pgtest.GoBin(), "build", "-o", bin, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	ctx := context.Background()
	ch1, ch2 := uuid.New(), uuid.New()
	for i, id := range []uuid.UUID{ch1, ch2} {
		if _, err := pool.Exec(ctx, `INSERT INTO channels (id, number, name, slug, mode) VALUES ($1, $2, $3, $4, 'shuffle')`,
			id, 101+i, fmt.Sprintf("Test %d", i+1), fmt.Sprintf("test-%d", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	// a light software encode; other machines may stream
	if _, err := pool.Exec(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES
		('rules.tv_output', '{"encoder":"software","resolution":"854x480","fps":15,"video_kbps":600,"audio_kbps":64,"segment_seconds":2,"warm_grace_seconds":120}', now()),
		('rules.tv_cluster', '{"nodes_stream":true,"dead_after_seconds":15}', now())`); err != nil {
		t.Fatal(err)
	}

	homePort, nodePort := freePort(t), freePort(t)
	nodeID := uuid.NewString()
	noNode := filepath.Join(t.TempDir(), "no-node.json") // never this machine's real node config
	common := []string{"DATABASE_URL=" + dsn, "MAKTABA_NODE_CONFIG=" + noNode, "MAKTABA_FFMPEG_BIN=ffmpeg",
		"MAKTABA_FFPROBE_BIN=ffprobe", "MAKTABA_HEALTH_WARM=0s", "MAKTABA_JWKS_URL=", "MAKTABA_LOG_LEVEL=info"}
	home := startStreaming(t, bin, "home", append(common,
		"MAKTABA_TV_HOST_ROLE=home", "MAKTABA_CLUSTER_ROLE=home",
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", homePort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", freePort(t)),
		fmt.Sprintf("MAKTABA_STREAMING_GRPC_ADDR=127.0.0.1:%d", freePort(t)),
		fmt.Sprintf("MAKTABA_PUBLIC_BASE_URL=http://127.0.0.1:%d", homePort),
		"MAKTABA_CACHE_ROOT="+filepath.Join(t.TempDir(), "home-cache")))
	_ = home
	node := startStreaming(t, bin, "node", append(common,
		"MAKTABA_TV_HOST_ROLE=node", "MAKTABA_NODE_ID="+nodeID,
		fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", nodePort),
		fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", freePort(t)),
		fmt.Sprintf("MAKTABA_PUBLIC_BASE_URL=http://127.0.0.1:%d", nodePort),
		"MAKTABA_CACHE_ROOT="+filepath.Join(t.TempDir(), "node-cache")))

	var homeID string
	waitFor(t, "both heartbeats", 90*time.Second, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM streaming_hosts WHERE heartbeat_at > now() - interval '10 seconds'`).Scan(&n)
		return n == 2
	})
	if err := pool.QueryRow(ctx, `SELECT host_id FROM streaming_hosts WHERE role = 'home'`).Scan(&homeID); err != nil {
		t.Fatal(err)
	}
	// the admin switches the node on; the home carries one channel only
	if _, err := pool.Exec(ctx, `UPDATE streaming_hosts SET enabled = true WHERE host_id = $1`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE streaming_hosts SET capacity_override = 1 WHERE host_id = $1`, homeID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second) // one beat + one host refresh

	homeURL := fmt.Sprintf("http://127.0.0.1:%d", homePort)
	client := &http.Client{Timeout: 60 * time.Second}
	playlist := func(ch uuid.UUID) (int, string) {
		resp, err := client.Get(homeURL + "/stream/channel/" + ch.String() + "/index.m3u8")
		if err != nil {
			return 0, err.Error()
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(b)
	}
	placedOn := func(ch uuid.UUID) (string, string) {
		var h, reason string
		_ = pool.QueryRow(ctx, `SELECT host_id, reason FROM channel_placements WHERE channel_id = $1 AND state <> 'released'`, ch).Scan(&h, &reason)
		return h, reason
	}

	// channel 1: the home
	waitFor(t, "channel 1 on the home", 60*time.Second, func() bool {
		code, body := playlist(ch1)
		return code == 200 && strings.Contains(body, "#EXTINF")
	})
	if h, _ := placedOn(ch1); h != homeID {
		t.Fatalf("channel 1 placed on %q, want the home %q", h, homeID)
	}
	// channel 2: the home is full → the node; the playlist comes through the home
	var body2 string
	waitFor(t, "channel 2 through the front door", 60*time.Second, func() bool {
		code, body := playlist(ch2)
		body2 = body
		return code == 200 && strings.Contains(body, "#EXTINF")
	})
	if h, _ := placedOn(ch2); h != nodeID {
		t.Fatalf("channel 2 placed on %q, want the node %q", h, nodeID)
	}
	seg := regexp.MustCompile(`seg-\d+\.ts`).FindString(body2)
	resp, err := client.Get(homeURL + "/stream/channel/" + ch2.String() + "/" + seg)
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(sb) < 188 || sb[0] != 0x47 {
		t.Fatalf("segment through the front door: %d, %d bytes", resp.StatusCode, len(sb))
	}
	// one encode per channel: each channel's runtime row names its placement's host,
	// and each host reports only its own channel
	waitFor(t, "runtime rows", 20*time.Second, func() bool {
		var a, b string
		_ = pool.QueryRow(ctx, `SELECT host_id FROM channel_runtime WHERE channel_id = $1`, ch1).Scan(&a)
		_ = pool.QueryRow(ctx, `SELECT host_id FROM channel_runtime WHERE channel_id = $1`, ch2).Scan(&b)
		return a == homeID && b == nodeID
	})
	waitFor(t, "hosts reporting their own stations", 20*time.Second, func() bool {
		var ha, na string
		_ = pool.QueryRow(ctx, `SELECT active::text FROM streaming_hosts WHERE host_id = $1`, homeID).Scan(&ha)
		_ = pool.QueryRow(ctx, `SELECT active::text FROM streaming_hosts WHERE host_id = $1`, nodeID).Scan(&na)
		return strings.Contains(ha, ch1.String()) && !strings.Contains(ha, ch2.String()) &&
			strings.Contains(na, ch2.String()) && !strings.Contains(na, ch1.String())
	})
	t.Logf("placement: channel 1 on the home, channel 2 on the node; HLS and segments proxied")

	// a TS client (live.ts, the same splicing path as /auto/v{n}) on the home
	tsReq, _ := http.NewRequest("GET", homeURL+"/stream/channel/"+ch2.String()+"/live.ts", nil)
	tsResp, err := http.DefaultClient.Do(tsReq)
	if err != nil {
		t.Fatal(err)
	}
	defer tsResp.Body.Close()
	if tsResp.StatusCode != 200 {
		t.Fatalf("live.ts: %d", tsResp.StatusCode)
	}
	var total atomic.Int64
	var lastByte atomic.Int64
	readErr := make(chan error, 1)
	go func() {
		r := bufio.NewReader(tsResp.Body)
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				total.Add(int64(n))
				lastByte.Store(time.Now().UnixNano())
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()
	waitFor(t, "TS bytes from the node", 30*time.Second, func() bool { return total.Load() > 100_000 })

	// room on the home for the failover, then the node dies
	if _, err := pool.Exec(ctx, `UPDATE streaming_hosts SET capacity_override = 2 WHERE host_id = $1`, homeID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	before := total.Load()
	killedAt := time.Now()
	node.kill()
	t.Logf("node killed after %d TS bytes", before)
	waitFor(t, "TS bytes again after the node died", 25*time.Second, func() bool {
		return total.Load() > before+50_000 && lastByte.Load() > killedAt.UnixNano()
	})
	resumed := time.Since(killedAt)
	select {
	case err := <-readErr:
		t.Fatalf("the TS client's connection closed: %v", err)
	default:
	}
	h, reason := placedOn(ch2)
	if h != homeID || reason != "failover" {
		t.Errorf("after the failover channel 2 is on %q (%s), want the home (failover)", h, reason)
	}
	t.Logf("TS client stayed connected; data resumed %v after the kill", resumed.Round(100*time.Millisecond))

	// the new station joined at the wall clock, with an HLS discontinuity
	var pl string
	waitFor(t, "channel 2's playlist from the home", 30*time.Second, func() bool {
		code, body := playlist(ch2)
		pl = body
		return code == 200 && strings.Contains(body, "#EXTINF")
	})
	if !strings.Contains(pl, "#EXT-X-DISCONTINUITY") {
		t.Errorf("no discontinuity after the failover:\n%s", pl)
	}
	m := pdtRe.FindAllStringSubmatch(pl, -1)
	if len(m) == 0 {
		t.Fatalf("no program date-time:\n%s", pl)
	}
	pdt, err := time.Parse("2006-01-02T15:04:05.000-0700", m[len(m)-1][1])
	if err != nil {
		pdt, err = time.Parse(time.RFC3339Nano, m[len(m)-1][1])
	}
	if err != nil {
		t.Fatalf("program date-time %q: %v", m[len(m)-1][1], err)
	}
	if d := time.Since(pdt); d < -10*time.Second || d > 40*time.Second {
		t.Errorf("the channel did not continue at the wall clock: last segment at %v (%v ago)", pdt, d)
	}
	t.Logf("failover station: discontinuity present, last segment's program time %v ago", time.Since(pdt).Round(time.Second))
	var placements int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM channel_placements WHERE state <> 'released'`).Scan(&placements)
	if placements != 2 {
		t.Errorf("active placements = %d, want 2", placements)
	}
}
