//go:build broadcastrehearsal

package rehearsal

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	viewers "github.com/Hamza-Labs-Core/Maktaba/tools/broadcast-viewers"
)

// The rehearsal's parts on the runner: compose ports (one scenario at a time per job).
const (
	edgePort        = 18787
	edgeControlPort = 18788
	homePort        = 18090
	// the home reaches the edge on the compose network, as location 0
	edgeFromHome = "http://edge:8787"
	// pub is the rehearsal's publisher, which is its home: the edge refuses an ingest token
	// whose pub is not its home (#1257), as the cloud mints them
	pub = "home-rehearsal"
)

// rungs are the test pattern's ladder: the channel's own encode copied, two encoded in software.
var rungs = []string{"src", "480p", "360p"}

// abort ends a scenario after a failed setup check (recovered by the runner).
type abort struct{}

// stack is one scenario's compose project and the clients for it.
type stack struct {
	t       *testing.T
	r       *Result
	project string
	edge    string
	control string
	home    string
	key     ed25519.PrivateKey
	client  *http.Client
	// skew is how far the edge's fast clock runs ahead of real time; viewer tokens are minted on
	// the edge's time, or a day's jump would expire them
	skew time.Duration

	mu        sync.Mutex
	published map[string]time.Time // "<rung>/<seq>" -> the home's PUT answered 201
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func outDir(t *testing.T) string {
	if d := os.Getenv("REHEARSAL_OUT"); d != "" {
		return d
	}
	return filepath.Join(repoRoot(t), "tools/broadcast-viewers/rehearsal/results")
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// check records one pass condition; fatal also ends the scenario.
func (s *stack) check(name string, ok bool, counter string, detail ...any) bool {
	s.t.Helper()
	d := strings.TrimSpace(fmt.Sprintln(detail...))
	s.r.Checks = append(s.r.Checks, Check{Name: name, OK: ok, Counter: counter, Detail: d})
	if ok {
		s.t.Logf("ok   %s %s", name, d)
	} else {
		s.t.Logf("FAIL %s [%s] %s", name, counter, d)
	}
	return ok
}

func (s *stack) must(name string, ok bool, detail ...any) {
	s.t.Helper()
	if !s.check(name, ok, "setup", detail...) {
		panic(abort{})
	}
}

func (s *stack) timing(name string, d time.Duration) {
	if s.r.Timings == nil {
		s.r.Timings = map[string]float64{}
	}
	s.r.Timings[name] = float64(d.Milliseconds()) / 1000
}

func (s *stack) note(format string, a ...any) {
	s.r.Notes = append(s.r.Notes, fmt.Sprintf(format, a...))
	s.t.Logf("note %s", fmt.Sprintf(format, a...))
}

// compose runs docker compose on the scenario's project.
func compose(t *testing.T, project string, env []string, args ...string) (string, error) {
	file := filepath.Join(repoRoot(t), "tools/rehearsal-cloud/broadcast/compose.yml")
	cmd := exec.Command("docker", append([]string{"compose", "-p", project, "-f", file}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// up starts the stack (the edge, then the home) for one scenario; its logs are kept whatever
// happens (the tests-keep-logs rule) and it is removed afterwards.
func up(t *testing.T, r *Result, scenario string, env ...string) *stack {
	project := "bc-" + strings.TrimPrefix(scenario, "broadcast-")
	env = append(env, fmt.Sprintf("EDGE_PORT=%d", edgePort), fmt.Sprintf("EDGE_CONTROL_PORT=%d", edgeControlPort),
		fmt.Sprintf("HOME_PORT=%d", homePort))
	s := &stack{t: t, r: r, project: project, edge: fmt.Sprintf("http://127.0.0.1:%d", edgePort),
		control: fmt.Sprintf("http://127.0.0.1:%d", edgeControlPort), home: fmt.Sprintf("http://127.0.0.1:%d", homePort),
		key: viewers.CloudTestKey("cloud-1"), client: &http.Client{Timeout: 30 * time.Second}, published: map[string]time.Time{}}
	_, _ = compose(t, project, env, "down", "-v", "--remove-orphans")
	t.Cleanup(func() {
		logs, _ := compose(t, project, env, "logs", "--no-color", "--timestamps")
		_ = os.MkdirAll(outDir(t), 0o755)
		_ = os.WriteFile(filepath.Join(outDir(t), scenario+"-stack.log"), []byte(logs), 0o644)
		if os.Getenv("REHEARSAL_KEEP") == "" {
			_, _ = compose(t, project, env, "down", "-v", "--remove-orphans")
		}
	})
	t0 := time.Now()
	out, err := compose(t, project, env, "up", "-d", "--wait", "--wait-timeout", "180")
	s.must("the stack starts (edge in Miniflare, home)", err == nil, out)
	s.timing("stack_up", time.Since(t0))
	return s
}

func (s *stack) do(method, u string, body []byte, hdr map[string]string) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

func newChannel(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// publish gives the home the channel and its ingest token, as the API does after a publish.
func (s *stack) publish(ch string) {
	tok, err := viewers.IngestToken(s.key, viewers.IngestClaims{Pub: pub, Channel: ch, Home: pub,
		Rungs: rungs, Kbps: 10000, IssuedAt: time.Now()})
	s.must("an ingest token signed as the cloud", err == nil, err)
	body, _ := json.Marshal(map[string]any{"channel": ch, "token": tok, "ingest_url": edgeFromHome, "rungs": rungs, "name": "Rehearsal"})
	code, raw, err := s.do(http.MethodPost, s.home+"/publish", body, nil)
	s.must("the home takes the channel", err == nil && code == http.StatusOK, code, string(raw), err)
}

func (s *stack) viewerToken(ch string) func(string) (string, error) {
	return func(session string) (string, error) {
		return viewers.ViewerToken(viewers.RehearsalViewerKey, viewers.ViewerClaims{Pub: pub, Channel: ch, Session: session,
			Mode: "full", Exp: time.Now().Add(s.skew + 10*time.Minute).Unix()})
	}
}

// homeMetrics is the home's record: the broadcaster's state and report, and every PUT.
type homeMetrics struct {
	State     string         `json:"state"`
	Detail    string         `json:"detail"`
	Report    map[string]any `json:"report"`
	Running   int            `json:"running"`
	EncoderOn bool           `json:"encoder_on"`
	Puts      []struct {
		Rung   string `json:"rung"`
		Seq    int64  `json:"seq"`
		Status int    `json:"status"`
		Bytes  int64  `json:"bytes"`
		AtMS   int64  `json:"at_ms"`
	} `json:"puts"`
}

func (s *stack) homeMetrics() homeMetrics {
	var m homeMetrics
	code, raw, err := s.do(http.MethodGet, s.home+"/metrics", nil, nil)
	if err != nil || code != http.StatusOK || json.Unmarshal(raw, &m) != nil {
		s.t.Logf("home metrics: %d %v", code, err)
	}
	return m
}

// follow keeps the home's upload times for the viewers' latency after the home.
func (s *stack) follow(ctx context.Context) {
	go func() {
		for ctx.Err() == nil {
			m := s.homeMetrics()
			s.mu.Lock()
			for _, p := range m.Puts {
				k := p.Rung + "/" + strconv.FormatInt(p.Seq, 10)
				if p.Status == http.StatusCreated {
					if _, ok := s.published[k]; !ok {
						s.published[k] = time.UnixMilli(p.AtMS)
					}
				}
			}
			s.mu.Unlock()
			viewersSleep(ctx, 500*time.Millisecond)
		}
	}()
}

func viewersSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (s *stack) publishedAt(rung string, seq int64) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.published[rung+"/"+strconv.FormatInt(seq, 10)]
	return at, ok
}

func (s *stack) fleet(ch string, locations int) *viewers.Fleet {
	return &viewers.Fleet{Locations: locations, Rungs: rungs, Template: viewers.Config{
		Base: s.edge, Pub: pub, Channel: ch, Token: s.viewerToken(ch), Published: s.publishedAt,
		HTTP: &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 2048, MaxConnsPerHost: 0}},
	}}
}

// edgeCounters is entry.ts's counters, per Worker (location × version).
type edgeCounters struct {
	Locations int `json:"locations"`
	Workers   []struct {
		Worker          string         `json:"worker"`
		Location        int            `json:"location"`
		Version         int            `json:"version"`
		R2Get           int            `json:"r2_get"`
		R2GetByKey      map[string]int `json:"r2_get_by_key"`
		IngestPutByRung map[string]int `json:"ingest_put_by_rung"`
		DOGet           int            `json:"do_get"`
		AEByEvent       map[string]int `json:"ae_by_event"`
		AEIngestGlass   int            `json:"ae_ingest_glass"`
		AEByCode        map[string]int `json:"ae_by_code"`
		Requests        int            `json:"requests"`
	} `json:"workers"`
}

func (s *stack) counters() edgeCounters {
	var c edgeCounters
	code, raw, err := s.do(http.MethodGet, s.edge+"/__rehearsal/counters", nil, nil)
	if err != nil || code != http.StatusOK || json.Unmarshal(raw, &c) != nil {
		s.t.Logf("edge counters: %d %v", code, err)
	}
	return c
}

// segmentReads is R2 reads of one channel's live segments between two counter readings:
// object key -> location -> reads.
func segmentReads(a, b edgeCounters, ch string) map[string]map[int]int {
	before := map[string]int{}
	for _, w := range a.Workers {
		for k, n := range w.R2GetByKey {
			before[w.Worker+"|"+k] = n
		}
	}
	out := map[string]map[int]int{}
	for _, w := range b.Workers {
		for k, n := range w.R2GetByKey {
			d := n - before[w.Worker+"|"+k]
			if d <= 0 || !strings.Contains(k, "/"+ch+"/") || strings.Contains(k, "/slate/") {
				continue
			}
			if out[k] == nil {
				out[k] = map[int]int{}
			}
			out[k][w.Location] += d
		}
	}
	return out
}

// playlist is one viewer's look at a rung's playlist (the probes of the functional scenarios).
func (s *stack) playlist(ch, rung string) (int, string, viewers.MediaPlaylist) {
	tok, _ := s.viewerToken(ch)("probe")
	code, raw, err := s.do(http.MethodGet, s.edge+"/"+tok+"/"+ch+"/"+rung+"/index.m3u8", nil, map[string]string{"X-Rehearsal-Session": "probe"})
	if err != nil {
		return 0, "", viewers.MediaPlaylist{}
	}
	var e struct {
		Error string `json:"error"`
	}
	if code != http.StatusOK {
		_ = json.Unmarshal(raw, &e)
	}
	return code, e.Error, viewers.ParseMedia(string(raw))
}

// waitFor polls cond every 250 ms until it holds or the time is up; it says how long it took.
func waitFor(within time.Duration, cond func() bool) (time.Duration, bool) {
	t0 := time.Now()
	for time.Since(t0) < within {
		if cond() {
			return time.Since(t0), true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return time.Since(t0), cond()
}

// liveOnEveryRung: each rung's playlist ends in two or more of the home's segments.
func (s *stack) liveOnEveryRung(ch string) bool {
	for _, r := range rungs {
		code, _, pl := s.playlist(ch, r)
		live := 0
		for _, seg := range pl.Segments {
			if !seg.Slate {
				live++
			}
		}
		if code != http.StatusOK || live < 2 || pl.Segments[len(pl.Segments)-1].Slate {
			return false
		}
	}
	return true
}

func (s *stack) waitLive(ch string) {
	d, ok := waitFor(3*time.Minute, func() bool { return s.liveOnEveryRung(ch) })
	m := s.homeMetrics()
	s.must("the channel is live on every rung", ok, d, "home:", m.State, m.Detail, len(m.Puts), "puts")
	s.timing("live_after_publish", d)
}

// installList pushes a signed revocation list to the edge as the cloud's push does (50.25 §2).
func (s *stack) installList(version int64, entries []viewers.RevocationEntry) {
	list, err := viewers.RevocationList(s.key, version, entries, time.Now())
	s.must("a revocation list signed as the cloud", err == nil, err)
	code, raw, err := s.do(http.MethodPut, s.edge+"/v1/control/revocations", []byte(list), nil)
	s.must(fmt.Sprintf("the edge takes list v%d", version), err == nil && code == http.StatusOK, code, string(raw), err)
}

// deploy redeploys every edge Worker with vars (Miniflare restarted, the state kept on disk).
func (s *stack) deploy(vars map[string]string) time.Duration {
	body, _ := json.Marshal(map[string]any{"vars": vars})
	t0 := time.Now()
	code, raw, err := s.do(http.MethodPost, s.control+"/deploy", body, nil)
	s.must("the edge redeploys", err == nil && code == http.StatusOK, code, string(raw), err)
	return time.Since(t0)
}
