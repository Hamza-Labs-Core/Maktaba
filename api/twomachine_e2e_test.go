package main

// Epic 35 phase C end to end on one PC (stories 35.8–35.11): a home and a full node —
// two `maktaba-server serve` processes with their own ports, data dirs and node
// configs — against one scratch Postgres. It proves:
//
//   - the node runs in node mode with its own limited database role, and its API refuses
//     to start with a role that can run DDL;
//   - a sign-in on the home is carried to the node by the cluster sign-in (single-use
//     code, PKCE), the node's cookie is its own;
//   - local / any / home routes are served as classed (home ones proxied with the user's
//     session, writes included, CSRF per address), the hub WebSocket through the proxy;
//   - signing out on the home blocks the node's session within a second;
//   - VOD on the node is opened on the node itself (no request reaches the home); a
//     video this node cannot read falls back to the home, and the Machines page says so;
//   - the Machines page lists every machine's web address;
//   - with the home gone, the node fails closed for signed-in requests while its local
//     routes answer.
//
// Opt-in (it builds three binaries): MAKTABA_TEST_PG_URL (a Postgres whose user may
// create databases and roles) and MAKTABA_E2E_TWO_MACHINE=1. MAKTABA_E2E_HOLD=<seconds>
// keeps both machines up at the end (manual checks, screenshots). It never reads this
// machine's own node config, keys or installs: every path is under t.TempDir().

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// TestMain doubles as the stand-in pipeline of the two machines: with
// MAKTABA_E2E_SLEEPER=1 the binary only serves /healthz on its logs port and waits.
func TestMain(m *testing.M) {
	if os.Getenv("MAKTABA_E2E_SLEEPER") == "1" {
		if addr := os.Getenv("MAKTABA_PIPELINE_LOGS_ADDR"); addr != "" {
			http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
			_ = http.ListenAndServe(addr, nil)
		}
		select {}
	}
	os.Exit(m.Run())
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func exeName(n string) string {
	if runtime.GOOS == "windows" {
		return n + ".exe"
	}
	return n
}

func buildBinaries(t *testing.T, dir string) {
	t.Helper()
	root, _ := filepath.Abs("..")
	for _, b := range []struct{ name, dir string }{
		{"maktaba-api", filepath.Join(root, "api")},
		{"maktaba-streaming", filepath.Join(root, "streaming")},
		{"maktaba-server", filepath.Join(root, "cmd", "maktaba-server")},
	} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, exeName(b.name)), ".")
		cmd.Dir = b.dir
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", b.name, err, out)
		}
	}
}

// machine is one running maktaba-server.
type machine struct {
	name string
	cmd  *exec.Cmd
	log  *lockedBuf
	web  string
	once sync.Once
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *lockedBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func (m *machine) stop() {
	m.once.Do(func() {
		if m.cmd == nil || m.cmd.Process == nil {
			return
		}
		if runtime.GOOS == "windows" {
			_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(m.cmd.Process.Pid)).Run()
		} else {
			_ = m.cmd.Process.Signal(os.Interrupt)
		}
		done := make(chan struct{})
		go func() { _ = m.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			_ = m.cmd.Process.Kill()
		}
	})
}

func startMachine(t *testing.T, name, bin string, env []string) *machine {
	t.Helper()
	m := &machine{name: name, log: &lockedBuf{}}
	m.cmd = exec.Command(filepath.Join(bin, exeName("maktaba-server")), "serve", "--no-mdns", "--bin-dir", bin)
	m.cmd.Env = append(os.Environ(), env...)
	m.cmd.Stdout, m.cmd.Stderr = m.log, m.log
	if err := m.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.stop()
		if t.Failed() {
			t.Logf("---- %s log (tail) ----\n%s", name, tail(m.log.String(), 6000))
		}
	})
	return m
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}

func waitFor(t *testing.T, what string, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type browser struct {
	t *testing.T
	c *http.Client
}

func newBrowser(t *testing.T) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, c: &http.Client{Jar: jar, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) cookie(base, name string) string {
	u, _ := url.Parse(base)
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func (b *browser) do(method, u string, body any, hdr map[string]string) (*http.Response, []byte) {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		b.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, u, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

func TestTwoMachinesOnePC(t *testing.T) {
	if os.Getenv("MAKTABA_E2E_TWO_MACHINE") != "1" || testing.Short() {
		t.Skip("set MAKTABA_E2E_TWO_MACHINE=1 (and MAKTABA_TEST_PG_URL) to run the two-machine test")
	}
	db, dsn := clustertest.ScratchDB(t)
	ctx := context.Background()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	buildBinaries(t, bin)

	// ---- the cluster's state: a user, a library, two videos, an approved node ----
	pw := filepath.Join(tmp, "owner.pw")
	_ = os.WriteFile(pw, []byte("owner-pass-123\n"), 0o600)
	add := exec.Command(filepath.Join(bin, exeName("maktaba-api")), "adduser", "--password-file", pw, "owner")
	add.Env = append(os.Environ(), "DATABASE_URL="+dsn)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("adduser: %v\n%s", err, out)
	}
	media := filepath.ToSlash(filepath.Join(tmp, "media"))
	_ = os.MkdirAll(media, 0o755)
	_ = os.WriteFile(filepath.Join(media, "both.mp4"), bytes.Repeat([]byte{1}, 4096), 0o644)
	_ = os.WriteFile(filepath.Join(media, "homeonly.mp4"), bytes.Repeat([]byte{2}, 4096), 0o644)
	const lib, v1, v2 = "11111111-0000-4000-8000-000000000001", "22222222-0000-4000-8000-000000000001", "22222222-0000-4000-8000-000000000002"
	exec1 := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec1(`INSERT INTO libraries (id, name, roots) VALUES ($1, 'Lectures', ARRAY[$2])`, lib, media)
	exec1(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, state, title, duration_sec)
	       VALUES ($1, $3, $4, $5, 'both.mp4', 4096, now(), 'ready', 'Both machines read this', 60),
	              ($2, $3, $6, 'E2ESHARE:/homeonly.mp4', 'homeonly.mp4', 4096, now(), 'ready', 'Only the home reads this', 60)`,
		v1, v2, lib, strings.Repeat("a", 64), media+"/both.mp4", strings.Repeat("b", 64))
	exec1(`INSERT INTO media_info (video_id, container, video_codec, width, height, bitrate_kbps)
	       VALUES ($1, 'mp4', 'h264', 1280, 720, 2000), ($2, 'mp4', 'h264', 1280, 720, 2000)`, v1, v2)
	// a short grace so the home-gone check is quick (rules.cluster)
	exec1(`INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.cluster', '{"delegate_unreachable_grace_seconds": 5}', now())`)

	homePriv, homePub, _ := cluster.GenerateKey()
	nodePriv, nodePub, _ := cluster.GenerateKey()
	const nodeID = "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7"
	role := cluster.RoleName(nodeID)
	if err := (&cluster.PGRoles{DB: db}).Create(ctx, role, "node-pass-xyz"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = (&cluster.PGRoles{DB: db}).Drop(context.Background(), role) })
	exec1(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by)
	       VALUES ($1, 'studio-node', 'approved', 'linux', 'amd64', $2, $3, now(), 'test')`, nodeID, cluster.EncodeKey(nodePub), role)
	nodeDSN := clustertest.WithUser(t, dsn, role, "node-pass-xyz")

	// ---- the two machines ----
	homeDir, nodeDir := filepath.Join(tmp, "home"), filepath.Join(tmp, "node")
	_ = os.MkdirAll(homeDir, 0o700)
	_ = os.MkdirAll(nodeDir, 0o700)
	_ = os.WriteFile(filepath.Join(homeDir, "cluster.key"), homePriv, 0o600)
	p := func() int { return freePort(t) }
	hAPI, hWeb, hStream, nAPI, nWeb, nStream := p(), p(), p(), p(), p(), p()
	toml := func(dir string, api, web, stream int, extra string) string {
		path := filepath.Join(dir, "server.toml")
		body := fmt.Sprintf(`[server]
listen = "127.0.0.1:%d"
data_dir = %q
web_listen = "127.0.0.1:%d"
admin_addr = "127.0.0.1:%d"
stream_addr = "127.0.0.1:%d"
streaming_grpc_addr = "127.0.0.1:%d"
streaming_admin_addr = "127.0.0.1:%d"
pipeline_grpc_addr = "127.0.0.1:%d"
pipeline_logs_addr = "127.0.0.1:%d"
%s
[supervisor]
admin_addr = "127.0.0.1:%d"
[tv]
slots = "off"
`, api, filepath.ToSlash(filepath.Join(dir, "data")), web, p(), stream, p(), p(), p(), p(), extra, p())
		_ = os.WriteFile(path, []byte(body), 0o600)
		return path
	}
	homeWebURL := fmt.Sprintf("http://127.0.0.1:%d", hWeb)
	// the node is opened as localhost: another host, so the browser keeps its cookies apart
	nodeWebURL := fmt.Sprintf("http://localhost:%d", nWeb)
	homeToml := toml(homeDir, hAPI, hWeb, hStream, fmt.Sprintf("web_public_url = %q\nrole = \"home\"\n[database]\nurl = %q", homeWebURL, dsn))
	nodeToml := toml(nodeDir, nAPI, nWeb, nStream, fmt.Sprintf("web_public_url = %q\n[database]\nurl = \"sqlite://unused.db\"", nodeWebURL))
	nodeCfg, _ := json.Marshal(map[string]any{"node_id": nodeID, "name": "studio-node", "private_key": cluster.EncodeKey(nodePriv),
		"home": map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d", hAPI), "cluster_pub": cluster.EncodeKey(homePub)},
		"database_url": nodeDSN})
	_ = os.WriteFile(filepath.Join(nodeDir, "node.json"), nodeCfg, 0o600)
	self, _ := os.Executable()
	repo, _ := filepath.Abs("..")
	common := []string{"MAKTABA_PYTHON=" + self, "MAKTABA_E2E_SLEEPER=1", "MAKTABA_DISCOVERY_MDNS=0", "MAKTABA_HSTS=0",
		"MAKTABA_HEALTH_WARM=0s", "MAKTABA_CLUSTER_DIR=" + filepath.Join(tmp, "clusterfiles"),
		"MAKTABA_ARCHIVE_DIR=" + filepath.Join(tmp, "archive"), "MAKTABA_UPDATE_DISABLE=1"}
	home := startMachine(t, "home", bin, append(common, "MAKTABA_CONFIG="+homeToml,
		"MAKTABA_NODE_CONFIG="+filepath.Join(homeDir, "no-node.json"),
		"MAKTABA_HOME_CLUSTER_KEY="+filepath.Join(homeDir, "cluster.key"), "MAKTABA_HOME_AGENT_KEY="+filepath.Join(homeDir, "agent.key"),
		"MAKTABA_SECRETS_KEY="+filepath.Join(homeDir, "secrets.key"), "MAKTABA_DATA_DIR="+filepath.Join(homeDir, "data"),
		"MAKTABA_MIGRATIONS_DIR="+filepath.Join(repo, "shared", "db", "migrations"),
		"MAKTABA_STREAM_PATH_MAP=E2ESHARE:/=>"+media+"/"))
	home.web = homeWebURL
	waitFor(t, "the home's API", 90*time.Second, func() bool {
		r, err := http.Get(homeWebURL + "/api/cluster/self")
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	node := startMachine(t, "node", bin, append(common, "MAKTABA_CONFIG="+nodeToml,
		"MAKTABA_NODE_CONFIG="+filepath.Join(nodeDir, "node.json"), "MAKTABA_DATA_DIR="+filepath.Join(nodeDir, "data"),
		fmt.Sprintf("MAKTABA_HOME_STREAM_URL=http://127.0.0.1:%d", hStream),
		// this machine's mount of the share is missing: the home-only video falls back
		"MAKTABA_STREAM_PATH_MAP=E2ESHARE:/=>"+filepath.ToSlash(filepath.Join(tmp, "no-such-share"))+"/"))
	node.web = nodeWebURL
	type selfInfo struct {
		Role           string `json:"role"`
		AgentConnected bool   `json:"agent_connected"`
		WebURL         string `json:"web_url"`
		Home           *struct {
			WebURL    string `json:"web_url"`
			Reachable bool   `json:"reachable"`
		} `json:"home"`
	}
	waitFor(t, "the node's machine agent and its delegation key", 90*time.Second, func() bool {
		r, err := http.Get(nodeWebURL + "/api/cluster/self")
		if err != nil {
			return false
		}
		defer r.Body.Close()
		var s selfInfo
		return json.NewDecoder(r.Body).Decode(&s) == nil && s.Role == "node" && s.AgentConnected &&
			s.Home != nil && s.Home.WebURL == homeWebURL
	})

	t.Run("node API refuses a DDL-capable role", func(t *testing.T) {
		cmd := exec.Command(filepath.Join(bin, exeName("maktaba-api")), "serve")
		cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn, "MAKTABA_NODE_MODE=1", "MAKTABA_NODE_ID="+nodeID,
			fmt.Sprintf("MAKTABA_HOME_API_URL=http://127.0.0.1:%d", hAPI), fmt.Sprintf("MAKTABA_HTTP_ADDR=127.0.0.1:%d", p()),
			fmt.Sprintf("MAKTABA_ADMIN_ADDR=127.0.0.1:%d", p()), "MAKTABA_AUTO_MIGRATE=")
		out, err := cmdWithTimeout(cmd, 30*time.Second)
		if err == nil || !strings.Contains(out, "can run DDL") || !strings.Contains(out, "superuser") {
			t.Fatalf("a superuser role must be refused: err=%v\n%s", err, tail(out, 2000))
		}
		cmd = exec.Command(filepath.Join(bin, exeName("maktaba-api")), "serve")
		cmd.Env = append(os.Environ(), "DATABASE_URL="+nodeDSN, "MAKTABA_NODE_MODE=1", "MAKTABA_NODE_ID="+nodeID,
			fmt.Sprintf("MAKTABA_HOME_API_URL=http://127.0.0.1:%d", hAPI), "MAKTABA_AUTO_MIGRATE=true")
		if out, err := cmdWithTimeout(cmd, 30*time.Second); err == nil || !strings.Contains(out, "never migrates") {
			t.Fatalf("node mode must refuse to migrate: err=%v\n%s", err, tail(out, 2000))
		}
	})

	// ---- sign in on the home, carry it to the node ----
	user := newBrowser(t)
	resp, body := user.do("POST", homeWebURL+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login on the home: %d %s", resp.StatusCode, body)
	}
	t.Run("cluster sign-in", func(t *testing.T) {
		resp, _ := user.do("GET", nodeWebURL+"/api/cluster/sso/start?return=/library", nil, nil)
		auth := resp.Header.Get("Location")
		if resp.StatusCode != 302 || !strings.HasPrefix(auth, homeWebURL+"/api/cluster/sso/authorize?") {
			t.Fatalf("start: %d %q", resp.StatusCode, auth)
		}
		resp, _ = user.do("GET", auth, nil, nil)
		cb := resp.Header.Get("Location")
		if resp.StatusCode != 302 || !strings.HasPrefix(cb, nodeWebURL+"/api/cluster/sso/callback?code=") {
			t.Fatalf("authorize: %d %q", resp.StatusCode, cb)
		}
		resp, _ = user.do("GET", cb, nil, nil)
		if resp.StatusCode != 302 || resp.Header.Get("Location") != "/library" {
			t.Fatalf("callback: %d %q", resp.StatusCode, resp.Header.Get("Location"))
		}
		for _, c := range resp.Cookies() {
			if c.Domain != "" {
				t.Fatalf("cookie %s must be host-only, has Domain %q", c.Name, c.Domain)
			}
		}
		if user.cookie(nodeWebURL, "mkt_sess") == "" || user.cookie(nodeWebURL, "mkt_sess") == user.cookie(homeWebURL, "mkt_sess") {
			t.Fatal("the node has its own (child) session cookie")
		}
		// the code is single use
		resp, _ = user.do("GET", cb, nil, nil)
		if resp.StatusCode == 302 {
			t.Fatal("a used code must be refused")
		}
		var origin, parent sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT CAST(origin_node_id AS TEXT), CAST(parent_session_id AS TEXT) FROM web_sessions WHERE id = $1`,
			user.cookie(nodeWebURL, "mkt_sess")).Scan(&origin, &parent); err != nil || origin.String != nodeID ||
			parent.String != user.cookie(homeWebURL, "mkt_sess") {
			t.Fatalf("child session: origin=%v parent=%v err=%v", origin, parent, err)
		}
	})

	t.Run("routes by placement class", func(t *testing.T) {
		for _, c := range []struct{ path, class string }{
			{"/api/system/version", "local"}, {"/api/cluster/self", "local"}, {"/api/system/processes", "local"},
			{"/api/videos", "any"}, {"/api/libraries", "any"}, {"/api/collections", "any"},
			{"/api/auth/me", "home"}, {"/api/cluster", "home"}, {"/api/me/sessions", "home"}, {"/api/queues", "home"},
		} {
			resp, body := user.do("GET", nodeWebURL+c.path, nil, nil)
			if resp.StatusCode != 200 || resp.Header.Get("X-Maktaba-Placement") != c.class {
				t.Errorf("%s: %d placement=%q, want 200 %s: %s", c.path, resp.StatusCode, resp.Header.Get("X-Maktaba-Placement"), c.class, tail(string(body), 300))
			}
		}
		_, body := user.do("GET", nodeWebURL+"/api/videos", nil, nil)
		if !strings.Contains(string(body), "Both machines read this") {
			t.Errorf("the node's any read lists the library: %s", tail(string(body), 300))
		}
		// a write through the node: CSRF per address, then proxied to the home
		resp, _ = user.do("PATCH", nodeWebURL+"/api/videos/"+v1, map[string]string{"title": "Renamed on the node"}, nil)
		if resp.StatusCode != 403 {
			t.Errorf("a write without the CSRF header: %d, want 403", resp.StatusCode)
		}
		resp, body = user.do("PATCH", nodeWebURL+"/api/videos/"+v1, map[string]string{"title": "Renamed on the node"},
			map[string]string{"X-Maktaba-CSRF": user.cookie(nodeWebURL, "mkt_csrf")})
		if resp.StatusCode != 200 || resp.Header.Get("X-Maktaba-Placement") != "home" {
			t.Errorf("a proxied write: %d %s", resp.StatusCode, tail(string(body), 300))
		}
		// the home's CSRF token does not work on the node (CSRF per address)
		resp, _ = user.do("PATCH", nodeWebURL+"/api/videos/"+v1, map[string]string{"title": "x"},
			map[string]string{"X-Maktaba-CSRF": user.cookie(homeWebURL, "mkt_csrf")})
		if resp.StatusCode != 403 {
			t.Errorf("the home's CSRF token on the node: %d, want 403", resp.StatusCode)
		}
		// anonymous: the node's any routes need a sign-in too
		if r, err := http.Get(nodeWebURL + "/api/videos"); err != nil || r.StatusCode != 401 {
			t.Errorf("anonymous any read: %v %v", r, err)
		}
	})

	t.Run("hub WebSocket through the node", func(t *testing.T) {
		wsURL := strings.Replace(nodeWebURL, "http://", "ws://", 1) + "/api/cluster/ws"
		h := http.Header{}
		h.Set("Cookie", "mkt_sess="+user.cookie(nodeWebURL, "mkt_sess"))
		h.Set("Origin", nodeWebURL)
		wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		c, resp, err := websocket.Dial(wctx, wsURL, &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			code := 0
			if resp != nil {
				code = resp.StatusCode
			}
			t.Fatalf("dial the hub through the node: %v (%d)", err, code)
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		if err := c.Write(wctx, websocket.MessageText, []byte(`{"op":"ping"}`)); err != nil {
			t.Fatal(err)
		}
		for {
			_, msg, err := c.Read(wctx)
			if err != nil {
				t.Fatalf("no pong from the home's hub through the node: %v", err)
			}
			if strings.Contains(string(msg), "pong") {
				break
			}
		}
	})

	homeRequests := func() int { return strings.Count(home.log.String(), "path=/api/stream/sessions ") }
	t.Run("VOD read on the node; fallback to the home", func(t *testing.T) {
		before := homeRequests()
		var resp *http.Response
		var body []byte
		// the node's streaming role may still be starting (its gRPC surface)
		waitFor(t, "the node's streaming role", 60*time.Second, func() bool {
			resp, body = user.do("POST", nodeWebURL+"/api/stream/sessions", map[string]string{"video_id": v1},
				map[string]string{"X-Maktaba-CSRF": user.cookie(nodeWebURL, "mkt_csrf")})
			return resp.StatusCode != http.StatusServiceUnavailable
		})
		if resp.StatusCode != 201 || resp.Header.Get("X-Maktaba-VOD") != "local" || resp.Header.Get("X-Maktaba-Placement") != "local" {
			t.Fatalf("a video this node reads: %d vod=%q %s", resp.StatusCode, resp.Header.Get("X-Maktaba-VOD"), tail(string(body), 300))
		}
		if strings.Contains(string(body), "via-home") {
			t.Fatalf("a local session must not go through the home: %s", body)
		}
		time.Sleep(500 * time.Millisecond)
		if homeRequests() != before {
			t.Fatal("the home saw the node's local VOD session")
		}
		resp, body = user.do("POST", nodeWebURL+"/api/stream/sessions", map[string]string{"video_id": v2},
			map[string]string{"X-Maktaba-CSRF": user.cookie(nodeWebURL, "mkt_csrf")})
		if resp.StatusCode != 201 || resp.Header.Get("X-Maktaba-VOD") != "home" || !strings.Contains(string(body), "/stream/via-home/") {
			t.Fatalf("a video this node cannot read: %d vod=%q %s", resp.StatusCode, resp.Header.Get("X-Maktaba-VOD"), tail(string(body), 300))
		}
		waitFor(t, "the Machines page to show the VOD fallback", 15*time.Second, func() bool {
			_, b := user.do("GET", homeWebURL+"/api/cluster", nil, nil)
			return strings.Contains(string(b), `"fallback":true`) && strings.Contains(string(b), "share not readable here")
		})
	})

	t.Run("Machines page lists each machine's web address", func(t *testing.T) {
		_, body := user.do("GET", homeWebURL+"/api/cluster", nil, nil)
		var v struct {
			Home  struct{ WebURL string `json:"web_url"` } `json:"home"`
			Nodes []struct {
				WebURL   string `json:"web_url"`
				FullNode bool   `json:"full_node"`
				Platform string `json:"platform"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(body, &v); err != nil || v.Home.WebURL != homeWebURL || len(v.Nodes) != 1 ||
			v.Nodes[0].WebURL != nodeWebURL || !v.Nodes[0].FullNode || v.Nodes[0].Platform == "" {
			t.Fatalf("machines: %v %s", err, tail(string(body), 600))
		}
	})

	if hold, _ := strconv.Atoi(os.Getenv("MAKTABA_E2E_HOLD")); hold > 0 {
		t.Logf("holding: home %s, node %s (user owner / owner-pass-123) for %ds", homeWebURL, nodeWebURL, hold)
		time.Sleep(time.Duration(hold) * time.Second)
	}

	t.Run("signing out on the home blocks the node within a second", func(t *testing.T) {
		resp, _ := user.do("GET", nodeWebURL+"/api/videos", nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("before: %d", resp.StatusCode)
		}
		resp, _ = user.do("POST", homeWebURL+"/api/auth/logout", nil, map[string]string{"X-Maktaba-CSRF": user.cookie(homeWebURL, "mkt_csrf")})
		if resp.StatusCode != 204 {
			t.Fatalf("logout: %d", resp.StatusCode)
		}
		start := time.Now()
		for {
			resp, _ := user.do("GET", nodeWebURL+"/api/videos", nil, nil)
			if resp.StatusCode == 401 {
				break
			}
			if time.Since(start) > time.Second {
				t.Fatalf("the node still serves the revoked session after %s (%d)", time.Since(start), resp.StatusCode)
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("the node refused the revoked session %s after the home's sign-out", time.Since(start).Round(time.Millisecond))
	})

	t.Run("home gone: the node fails closed, its local routes answer", func(t *testing.T) {
		// a fresh sign-in on the node (through the home), cached on the node
		u2 := newBrowser(t)
		resp, _ := u2.do("POST", nodeWebURL+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
		if resp.StatusCode != 200 || resp.Header.Get("X-Maktaba-Placement") != "home" {
			t.Fatalf("sign-in through the node: %d", resp.StatusCode)
		}
		var origin sql.NullString
		_ = db.QueryRowContext(ctx, `SELECT CAST(origin_node_id AS TEXT) FROM web_sessions WHERE id = $1`, u2.cookie(nodeWebURL, "mkt_sess")).Scan(&origin)
		if origin.String != nodeID {
			t.Fatalf("a sign-in through the node is tagged with it: %v", origin)
		}
		if r, _ := u2.do("GET", nodeWebURL+"/api/videos", nil, nil); r.StatusCode != 200 {
			t.Fatalf("signed in through the node: %d", r.StatusCode)
		}
		home.stop()
		waitFor(t, "503 home_unreachable on a signed-in request", 60*time.Second, func() bool {
			r, b := u2.do("GET", nodeWebURL+"/api/videos", nil, nil)
			return r.StatusCode == 503 && strings.Contains(string(b), "home_unreachable")
		})
		if r, _ := u2.do("GET", nodeWebURL+"/api/system/version", nil, nil); r.StatusCode != 200 {
			t.Fatalf("a local route with the home gone: %d", r.StatusCode)
		}
	})
}

func cmdWithTimeout(cmd *exec.Cmd, d time.Duration) (string, error) {
	var out lockedBuf
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return out.String(), nil // still running: it did not refuse
	}
}
