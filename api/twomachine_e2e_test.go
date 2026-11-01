package main

// Epic 35 phase C end to end on one PC (stories 35.8–35.11): a home and a full node —
// two `maktaba-server serve` processes with their own ports, data dirs and node
// configs — against one scratch Postgres. It proves:
//
//   - the node runs in node mode with its own limited database role, and its API refuses
//     to start with a role that can run DDL;
//   - a sign-in on the node (its sign-in form, checked by the home) is the home's, tagged
//     with the node, and the node's cookie is its own; the cluster sign-in's routes are
//     gone (404 on both machines);
//   - local / any / home routes are served as classed (home ones proxied with the user's
//     session, writes included, CSRF per address), the hub WebSocket through the proxy;
//   - signing out everywhere on the home blocks the node's session within a second;
//   - VOD on the node is opened on the node itself (no request reaches the home); a
//     video this node cannot read falls back to the home, and the Machines page says so;
//   - the Machines page lists every machine's web address;
//   - the node's supervisor makes its own database, subscribes it to the home's
//     publication and follows it (Epic 37, logical replication); with the home gone
//     (as a Mac that is switched off: its connections hang, nothing refuses them) the node runs on that copy: a signed-in user stays
//     in (marked X-Maktaba-Home: unreachable, a reload's /api/auth/me answered by the
//     node), plays a video it can read and is told one it cannot is unavailable, signs
//     in (a node-local session), signs out (ended on the node at once), and changes
//     are refused; its local routes answer;
//   - with the home's database gone too (the node's connections, and its copy's
//     subscription, hang through a proxy), the node reads and plays from its copy, even
//     after a restart in the outage;
//   - a node with no copy serves no one but its own routes with the home gone.
//
// Opt-in (it builds three binaries): MAKTABA_TEST_PG_URL (a Postgres whose user may
// create databases and roles, run with wal_level=logical and taking connections from
// 127.0.0.1: initdb --locale=C, as the bundled one is) and MAKTABA_E2E_TWO_MACHINE=1;
// MAKTABA_TEST_PG_BIN (a Postgres 16 install: initdb, postgres, pg_ctl) gives the node
// its copy. MAKTABA_E2E_HOLD=<seconds>
// keeps both machines up at the end (manual checks, screenshots). It never reads this
// machine's own node config, keys or installs: every path is under t.TempDir().

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
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

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/signedurl"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// TestMain doubles as the stand-in pipeline of the two machines: with
// MAKTABA_E2E_SLEEPER=1 the binary only serves /healthz on its logs port and waits.
func TestMain(m *testing.M) {
	// ...and of a home's cloud relay agent (switch_e2e_helpers_test.go): online on its
	// status address (only the relay's process has its credentials' key)
	if os.Getenv("MAKTABA_E2E_FAKE_CLOUDLINK") == "1" && os.Getenv("MAKTABA_CRED_KEY_HEX") != "" {
		if addr := os.Getenv("MAKTABA_ADMIN_ADDR"); addr != "" {
			http.HandleFunc("/admin/cloud-link", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"status":"online","slug":"e2e","attempts":0}`))
			})
			standInServe("the cloud relay agent", addr)
		}
		select {}
	}
	if os.Getenv("MAKTABA_E2E_SLEEPER") == "1" {
		if addr := os.Getenv("MAKTABA_PIPELINE_LOGS_ADDR"); addr != "" {
			http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
			standInServe("the pipeline", addr)
		}
		select {}
	}
	// never this machine's real node config: every process a test here starts
	// inherits a path that never exists (the machines set their own)
	dir, err := os.MkdirTemp("", "maktaba-nodecfg-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// standInServe serves a stand-in's address, or says why it cannot and ends (its
// supervisor starts it again, and the machine's log has the reason).
func standInServe(what, addr string) {
	err := http.ListenAndServe(addr, nil)
	fmt.Fprintln(os.Stderr, "e2e stand-in for "+what+": "+addr+":", err)
	os.Exit(1)
}

// handedPorts are the ports freePort handed out (each once).
var handedPorts = struct {
	sync.Mutex
	m map[int]bool
}{m: map[int]bool{}}

// freePort is a free loopback port for a process a test starts, below the systems'
// ephemeral ranges (Linux from 32768, Windows from 49152): a port the system picks for
// :0 is in that range, where an outgoing connection of any process may take it before
// the process binds it.
func freePort(t *testing.T) int {
	t.Helper()
	handedPorts.Lock()
	defer handedPorts.Unlock()
	for range 500 {
		p := 20000 + rand.IntN(12000)
		if handedPorts.m[p] {
			continue
		}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err != nil {
			continue
		}
		_ = l.Close()
		handedPorts.m[p] = true
		return p
	}
	t.Fatal("no free port below the ephemeral range")
	return 0
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

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

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
	// the runs play several videos at once: room for them on any runner (the cap is a
	// quarter of the CPUs: one on a 4-vCPU runner)
	m.cmd.Env = append(append(os.Environ(), "MAKTABA_TRANSCODE_MAX_CONCURRENT=8"), env...)
	m.cmd.Stdout, m.cmd.Stderr = m.log, m.log
	if err := m.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.stop()
		// MAKTABA_E2E_LOG_ALWAYS keeps the logs of a run that passed too (a flaky run's
		// passing twin, to compare)
		if t.Failed() || os.Getenv("MAKTABA_E2E_LOG_ALWAYS") != "" {
			t.Logf("---- %s log (tail) ----\n%s", name, tail(m.log.String(), 20000))
			// MAKTABA_E2E_LOG_DIR keeps each machine's whole log of a failed run (CI uploads
			// it). The folder is made here: a step that never made it lost every log, and a
			// flaky run then left 40 lines to go by.
			if dir := os.Getenv("MAKTABA_E2E_LOG_DIR"); dir != "" {
				path := filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "_")+"-"+name+".log")
				err := os.MkdirAll(dir, 0o755)
				if err == nil {
					err = os.WriteFile(path, []byte(m.log.String()), 0o600)
				}
				if err != nil {
					t.Logf("%s's whole log could not be kept: %v", name, err)
				} else {
					t.Logf("%s's whole log: %s", name, path)
				}
			}
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
	pgPrograms := os.Getenv("MAKTABA_TEST_PG_BIN")
	withCopy := pgPrograms != ""
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
	// real media (ffmpeg-generated), so VOD plays bytes end to end
	for _, name := range []string{"both.mp4", "homeonly.mp4", "direct.mp4"} {
		makeVideo(t, filepath.Join(media, name))
	}
	const lib, v1, v2 = "11111111-0000-4000-8000-000000000001", "22222222-0000-4000-8000-000000000001", "22222222-0000-4000-8000-000000000002"
	const v3 = "22222222-0000-4000-8000-000000000003"
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
	exec1(`INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime, state, title, duration_sec)
	       VALUES ($1, $2, $3, $4, 'direct.mp4', $5, now(), 'ready', 'Direct play', 14)`,
		v3, lib, strings.Repeat("c", 64), media+"/direct.mp4", fileSize(t, filepath.Join(media, "direct.mp4")))
	exec1(`INSERT INTO media_info (video_id, container, video_codec, width, height, bitrate_kbps)
	       VALUES ($1, 'mp4', 'h264', 640, 360, 1000), ($2, 'mp4', 'h264', 640, 360, 1000), ($3, 'mp4', 'h264', 640, 360, 1000)`, v1, v2, v3)
	// v3 is plain h264/aac mp4: a browser plays it directly (aud=streaming-direct)
	exec1(`INSERT INTO audio_tracks (video_id, track_index, codec, channels, is_default) VALUES ($1, 0, 'aac', 2, true)`, v3)
	// a short grace so the home-gone check is quick (rules.cluster); this home runs on the
	// test's own Postgres, not Maktaba's, which keeps copies only with the admin's rule
	exec1(`INSERT INTO app_settings (key, value, updated_at) VALUES ('rules.cluster',
	       '{"delegate_unreachable_grace_seconds": 5, "copies_on_operator_postgres": true}', now())`)

	homePriv, homePub, _ := cluster.GenerateKey()
	nodePriv, nodePub, _ := cluster.GenerateKey()
	const nodeID = "6f1c2a3b-4d5e-4f60-8a71-92b3c4d5e6f7"
	role := cluster.RoleName(nodeID)
	if err := (&cluster.PGRoles{DB: db}).Create(ctx, role, "node-pass-xyz"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = (&cluster.PGRoles{DB: db}).Drop(context.Background(), role) })
	// approved as an admin would, with a copy allowed (full_allowed, slot 0151): the node's
	// own report makes it full
	exec1(`INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by, full_allowed)
	       VALUES ($1, 'studio-node', 'approved', 'linux', 'amd64', $2, $3, now(), 'test', true)`, nodeID, cluster.EncodeKey(nodePub), role)
	// sslmode=prefer, as the join hands it (lib/pq takes no prefer: pgdsn, nodecopy)
	nodeDSN := strings.Replace(clustertest.WithUser(t, dsn, role, "node-pass-xyz"), "sslmode=disable", "sslmode=prefer", 1)
	// the node reaches the database through a proxy the test can cut (the home's
	// database gone, while the test's own connection stays)
	pgProxy := newTCPProxy(t, nodeDSN)
	nodeDSN = pgProxy.dsn

	// ---- the two machines ----
	homeDir, nodeDir := filepath.Join(tmp, "home"), filepath.Join(tmp, "node")
	_ = os.MkdirAll(homeDir, 0o700)
	_ = os.MkdirAll(nodeDir, 0o700)
	_ = os.WriteFile(filepath.Join(homeDir, "cluster.key"), homePriv, 0o600)
	p := func() int { return freePort(t) }
	hAPI, hWeb, hStream, nAPI, nWeb, nStream := p(), p(), p(), p(), p(), p()
	// the node reaches the home's API through a proxy too: a home switched off is one
	// whose connections hang
	apiProxy := newTCPProxy(t, fmt.Sprintf("http://127.0.0.1:%d", hAPI))
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
	nodeToml := toml(nodeDir, nAPI, nWeb, nStream, fmt.Sprintf("web_public_url = %q\n[database]\nurl = \"sqlite://unused.db\"\n[postgres]\ncopy_port = %d", nodeWebURL, p()))
	nodeCfg, _ := json.Marshal(map[string]any{"node_id": nodeID, "name": "studio-node", "private_key": cluster.EncodeKey(nodePriv),
		"home":         map[string]any{"url": apiProxy.dsn, "cluster_pub": cluster.EncodeKey(homePub)},
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
	nodeEnv := append(common, "MAKTABA_CONFIG="+nodeToml,
		"MAKTABA_NODE_CONFIG="+filepath.Join(nodeDir, "node.json"), "MAKTABA_DATA_DIR="+filepath.Join(nodeDir, "data"),
		fmt.Sprintf("MAKTABA_HOME_STREAM_URL=http://127.0.0.1:%d", hStream),
		// this machine's mount of the share is missing: the home-only video falls back
		"MAKTABA_STREAM_PATH_MAP=E2ESHARE:/=>"+filepath.ToSlash(filepath.Join(tmp, "no-such-share"))+"/")
	// its copy of the home's database (Epic 37) runs on the test's Postgres programs
	if withCopy {
		nodeEnv = append(nodeEnv, "MAKTABA_POSTGRES_HOME="+pgPrograms)
	} else {
		nodeEnv = append(nodeEnv, "MAKTABA_NODE_COPY=off")
	}
	node := startMachine(t, "node", bin, nodeEnv)
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

	// ---- sign in on the home, and on the node with its own sign-in form ----
	user := newBrowser(t)
	resp, body := user.do("POST", homeWebURL+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("login on the home: %d %s", resp.StatusCode, body)
	}
	t.Run("sign-in on the node", func(t *testing.T) {
		// the node checks a sign-in with its home, which it may not reach yet (503
		// home_unreachable: "it tries again"); every test after this one signs in here
		var resp *http.Response
		var body []byte
		waitFor(t, "the node to reach its home for a sign-in", 60*time.Second, func() bool {
			resp, body = user.do("POST", nodeWebURL+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
			if resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(string(body), "home_unreachable") {
				time.Sleep(time.Second) // a sign-in a second: no limiter takes it for guessing
				return false
			}
			return true
		})
		if resp.StatusCode != 200 {
			t.Fatalf("login on the node: %d %s", resp.StatusCode, body)
		}
		for _, c := range resp.Cookies() {
			if c.Domain != "" {
				t.Fatalf("cookie %s must be host-only, has Domain %q", c.Name, c.Domain)
			}
		}
		if user.cookie(nodeWebURL, "mkt_sess") == "" || user.cookie(nodeWebURL, "mkt_sess") == user.cookie(homeWebURL, "mkt_sess") {
			t.Fatal("the node has its own session cookie")
		}
		// a sign-in on the node is the home's, tagged with the node
		var origin, parent sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT CAST(origin_node_id AS TEXT), CAST(parent_session_id AS TEXT) FROM web_sessions WHERE id = $1`,
			user.cookie(nodeWebURL, "mkt_sess")).Scan(&origin, &parent); err != nil || origin.String != nodeID || parent.Valid {
			t.Fatalf("the node's session: origin=%v parent=%v err=%v", origin, parent, err)
		}
		// the cluster sign-in is gone: its routes answer 404 on both machines, signed in or not
		for _, u := range []string{nodeWebURL + "/api/cluster/sso/start?return=/library", nodeWebURL + "/api/cluster/sso/callback?code=x&state=y",
			homeWebURL + "/api/cluster/sso/authorize?node=" + nodeID, homeWebURL + "/api/cluster/sso/exchange"} {
			if resp, _ := user.do("GET", u, nil, nil); resp.StatusCode != http.StatusNotFound {
				t.Errorf("%s: %d, want 404", u, resp.StatusCode)
			}
			if r, err := http.Get(u); err != nil || r.StatusCode != http.StatusNotFound {
				t.Errorf("%s anonymous: %v %v, want 404", u, r, err)
			} else {
				_ = r.Body.Close()
			}
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
			return strings.Contains(string(b), `"fallback":true`) && strings.Contains(string(b), "cannot open")
		})
	})

	t.Run("VOD bytes play: signed stream URLs on the home, the node and via the home", func(t *testing.T) {
		csrf := func(base string) map[string]string {
			return map[string]string{"X-Maktaba-CSRF": user.cookie(base, "mkt_csrf")}
		}
		open := func(base, video string, force bool, wantVOD string) openSession {
			t.Helper()
			resp, body := user.do("POST", base+"/api/stream/sessions", map[string]any{"video_id": video, "force_transcode": force}, csrf(base))
			var s openSession
			if resp.StatusCode != 201 || json.Unmarshal(body, &s) != nil || s.SessionID == "" {
				t.Fatalf("open %s on %s: %d %s", video, base, resp.StatusCode, tail(string(body), 400))
			}
			if wantVOD != "" && resp.Header.Get("X-Maktaba-VOD") != wantVOD {
				t.Fatalf("open %s on %s: served %q, want %q", video, base, resp.Header.Get("X-Maktaba-VOD"), wantVOD)
			}
			if !strings.Contains(s.ManifestURL+s.DirectURL, "sig=") {
				t.Fatalf("the session's stream URL is not signed: %q %q", s.ManifestURL, s.DirectURL)
			}
			return s
		}

		// 1. the home, through its web port
		homeSess := open(homeWebURL, v1, true, "")
		homeSeg := playHLS(t, user, homeWebURL, homeSess.ManifestURL, "")
		// 2. the node: its own streaming role reads the share (local)
		nodeSess := open(nodeWebURL, v1, true, "local")
		if strings.Contains(nodeSess.ManifestURL, "via-home") {
			t.Fatalf("a local session goes through the home: %s", nodeSess.ManifestURL)
		}
		playHLS(t, user, nodeWebURL, nodeSess.ManifestURL, "local")
		// 3. the via-home fallback: the node's web port relays the home's streaming port
		viaSess := open(nodeWebURL, v2, true, "home")
		if !strings.HasPrefix(viaSess.ManifestURL, "/stream/via-home/") {
			t.Fatalf("fallback URL: %s", viaSess.ManifestURL)
		}
		playHLS(t, user, nodeWebURL, viaSess.ManifestURL, "home-vod-fallback")
		// 4. direct play (aud=streaming-direct), ranged, on the home
		direct := open(homeWebURL, v3, false, "")
		// manifest_url keeps the direct path too (the apps read only it), signed on its own
		if !strings.HasPrefix(direct.DirectURL, "/stream/direct/"+v3+"?sig=") ||
			!strings.HasPrefix(direct.ManifestURL, "/stream/direct/"+v3+"?sig=") {
			t.Fatalf("direct play URL: %q %q (mode %s)", direct.ManifestURL, direct.DirectURL, direct.Mode)
		}
		resp, body := user.do("GET", homeWebURL+direct.DirectURL, nil, map[string]string{"Range": "bytes=0-1023"})
		if resp.StatusCode != http.StatusPartialContent || len(body) != 1024 {
			t.Fatalf("direct play: %d, %d bytes", resp.StatusCode, len(body))
		}

		// each machine signs with its own stream key, beside its server.toml
		hk, err1 := os.ReadFile(filepath.Join(homeDir, "stream-signing.key"))
		nk, err2 := os.ReadFile(filepath.Join(nodeDir, "stream-signing.key"))
		if err1 != nil || err2 != nil || bytes.Equal(hk, nk) {
			t.Fatalf("per-machine stream keys: %v %v equal=%v", err1, err2, bytes.Equal(hk, nk))
		}

		// refused, signed in or not: tampered, expired, the node's signature on the
		// home, no signature; a good signature plays without a sign-in (story 48.12)
		u, _ := url.Parse(homeSess.ManifestURL)
		good := u.Query().Get("sig")
		bare := homeWebURL + u.Path
		// the first character: the last one of a 32-byte value carries two bits a decoder
		// ignores, so changing it may leave the same signature
		flip := "A"
		if strings.HasPrefix(good, "A") {
			flip = "B"
		}
		k, err := signedurl.ParseKey(hk)
		if err != nil {
			t.Fatal(err)
		}
		past := signedurl.FromKey(k)
		past.Now = func() time.Time { return time.Now().Add(-3 * time.Hour) }
		expired, _, err := past.SignStreamURL(u.Path, homeSess.SessionID, "33333333-0000-4000-8000-000000000001", lib, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		nu, _ := url.Parse(nodeSess.ManifestURL)
		for name, target := range map[string]string{
			"no signature":            bare,
			"tampered":                bare + "?sig=" + flip + good[1:],
			"expired":                 homeWebURL + expired,
			"the node's key":          homeWebURL + nu.Path + "?sig=" + nu.Query().Get("sig"),
			"segment without its sig": homeWebURL + strings.SplitN(homeSeg, "?", 2)[0],
		} {
			if resp, body := user.do("GET", target, nil, nil); resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s: %d %s, want 401/403", name, resp.StatusCode, tail(string(body), 200))
			}
			// without a sign-in too (an AirPlay / Cast receiver): the gate or the
			// streaming service still refuses it (Epic 48 story 48.12)
			if r, err := http.Get(target); err != nil {
				t.Errorf("%s, no sign-in: %v", name, err)
			} else {
				_ = r.Body.Close()
				if r.StatusCode != http.StatusUnauthorized && r.StatusCode != http.StatusForbidden {
					t.Errorf("%s, no sign-in: %d, want 401/403", name, r.StatusCode)
				}
			}
		}
		// Epic 48 story 48.12 (gap G2): a good signed URL plays without a sign-in on
		// the web port (an AirPlay or Cast receiver carries no cookie); the streaming
		// service verifies its ?sig=
		if r, err := http.Get(homeWebURL + homeSess.ManifestURL); err != nil {
			t.Errorf("a signed URL without a sign-in on the web port: %v", err)
		} else {
			_ = r.Body.Close()
			if r.StatusCode != http.StatusOK {
				t.Errorf("a signed URL without a sign-in on the web port: %d, want 200", r.StatusCode)
			}
		}
	})

	t.Run("Machines page lists each machine's web address", func(t *testing.T) {
		_, body := user.do("GET", homeWebURL+"/api/cluster", nil, nil)
		var v struct {
			Home struct {
				WebURL string `json:"web_url"`
			} `json:"home"`
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

	t.Run("signing out everywhere on the home blocks the node within a second", func(t *testing.T) {
		resp, _ := user.do("GET", nodeWebURL+"/api/videos", nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("before: %d", resp.StatusCode)
		}
		// the node's session is its own (signed in there), not the home session's: the home
		// ends it with "sign out everywhere", pushed to the node by user (auth.revoked)
		resp, _ = user.do("POST", homeWebURL+"/api/auth/logout-all", nil, map[string]string{"X-Maktaba-CSRF": user.cookie(homeWebURL, "mkt_csrf")})
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

	t.Run("home gone: the node runs on its copy of the home's database", func(t *testing.T) {
		if !withCopy {
			t.Skip("MAKTABA_TEST_PG_BIN unset: the node keeps no copy of the home's database")
		}
		// the node's supervisor copied the home's database on its own, and follows it
		waitFor(t, "the node's copy following the home", 4*time.Minute, func() bool {
			r, b := newBrowser(t).do("GET", nodeWebURL+"/api/cluster/self", nil, nil)
			var s struct {
				Copy struct {
					State string `json:"state"`
				} `json:"copy"`
			}
			return r.StatusCode == 200 && json.Unmarshal(b, &s) == nil && s.Copy.State == "following"
		})
		// and the home's Machines page says so
		if r, b := user.do("GET", homeWebURL+"/api/cluster/nodes", nil, nil); r.StatusCode == 200 &&
			!strings.Contains(string(b), `"state":"following"`) {
			t.Logf("the home's view of the copy: %s", tail(string(b), 400))
		}
		// sign-ins on the node through the home, before it goes away
		u2, leaver := newBrowser(t), newBrowser(t)
		for _, b := range []*browser{u2, leaver} {
			resp, _ := b.do("POST", nodeWebURL+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
			if resp.StatusCode != 200 || resp.Header.Get("X-Maktaba-Placement") != "home" {
				t.Fatalf("sign-in through the node: %d", resp.StatusCode)
			}
		}
		// the home is switched off: what the node sends it hangs
		apiProxy.hang()
		// past the grace the node answers from its copy, marked
		waitFor(t, "a read served from the copy (X-Maktaba-Home: unreachable)", 90*time.Second, func() bool {
			r, _ := u2.do("GET", nodeWebURL+"/api/videos", nil, nil)
			return r.StatusCode == 200 && r.Header.Get("X-Maktaba-Home") == "unreachable"
		})
		// a reload keeps the user signed in: the node answers the session probe
		if r, b := u2.do("GET", nodeWebURL+"/api/auth/me", nil, nil); r.StatusCode != 200 ||
			r.Header.Get("X-Maktaba-Home") != "unreachable" || !strings.Contains(string(b), `"username":"owner"`) {
			t.Fatalf("/api/auth/me with the home gone: %d %s", r.StatusCode, tail(string(b), 300))
		}
		// playback: a video this node reads opens and closes here; one it cannot is unavailable
		csrf := map[string]string{"X-Maktaba-CSRF": u2.cookie(nodeWebURL, "mkt_csrf")}
		r, b := u2.do("POST", nodeWebURL+"/api/stream/sessions", map[string]string{"video_id": v1}, csrf)
		var s openSession
		if r.StatusCode != 201 || r.Header.Get("X-Maktaba-VOD") != "local" || json.Unmarshal(b, &s) != nil || s.SessionID == "" {
			t.Fatalf("playback with the home gone: %d %s", r.StatusCode, tail(string(b), 300))
		}
		if r, b := u2.do("DELETE", nodeWebURL+"/api/stream/sessions/"+s.SessionID, nil, csrf); r.StatusCode != 204 {
			t.Fatalf("closing the session with the home gone: %d %s", r.StatusCode, tail(string(b), 300))
		}
		if r, b := u2.do("POST", nodeWebURL+"/api/stream/sessions", map[string]string{"video_id": v2}, csrf); r.StatusCode != 503 ||
			!strings.Contains(string(b), "video_unavailable_offline") {
			t.Fatalf("a video this node cannot read, with the home gone: %d %s", r.StatusCode, tail(string(b), 300))
		}
		// a sign-in is checked on the copy: valid on this machine only
		u3 := newBrowser(t)
		if r, b := u3.do("POST", nodeWebURL+"/api/auth/login",
			map[string]string{"username": "owner", "password": "owner-pass-123"}, nil); r.StatusCode != 200 ||
			!strings.Contains(string(b), `"session":"node"`) || !strings.HasPrefix(u3.cookie(nodeWebURL, "mkt_sess"), "nls_") {
			t.Fatalf("a sign-in with the home gone: %d %s", r.StatusCode, tail(string(b), 300))
		}
		if r, _ := u3.do("GET", nodeWebURL+"/api/videos", nil, nil); r.StatusCode != 200 {
			t.Fatalf("a read on the node-local sign-in: %d", r.StatusCode)
		}
		if r, _ := newBrowser(t).do("POST", nodeWebURL+"/api/auth/login",
			map[string]string{"username": "owner", "password": "wrong"}, nil); r.StatusCode != 401 {
			t.Fatalf("a wrong password with the home gone: %d", r.StatusCode)
		}
		// a change through the home waits for it
		if r, b := u2.do("PATCH", nodeWebURL+"/api/me", map[string]string{"display_name": "x"}, csrf); r.StatusCode != 503 ||
			!strings.Contains(string(b), "home_unreachable") {
			t.Fatalf("a change with the home gone: %d %s", r.StatusCode, tail(string(b), 300))
		}
		// a sign-out is the node's own: the session ends here at once (the home is told
		// when it is back)
		leaverCSRF := map[string]string{"X-Maktaba-CSRF": leaver.cookie(nodeWebURL, "mkt_csrf")}
		if r, b := leaver.do("POST", nodeWebURL+"/api/auth/logout", nil, leaverCSRF); r.StatusCode != 204 {
			t.Fatalf("a sign-out with the home gone: %d %s", r.StatusCode, tail(string(b), 300))
		}
		if r, _ := leaver.do("GET", nodeWebURL+"/api/videos", nil, nil); r.StatusCode != 401 {
			t.Fatalf("a read after the sign-out: %d, want 401", r.StatusCode)
		}
		// a credential the copy does not know is no one
		if r, _ := newBrowser(t).do("GET", nodeWebURL+"/api/videos", nil, map[string]string{
			"Cookie": "mkt_sess=6f1d2c3b-0000-4000-8000-00000000dead"}); r.StatusCode != 401 {
			t.Fatalf("an unknown session with the home gone: %d", r.StatusCode)
		}
		if r, _ := u2.do("GET", nodeWebURL+"/api/system/version", nil, nil); r.StatusCode != 200 {
			t.Fatalf("a local route with the home gone: %d", r.StatusCode)
		}

		t.Run("its database gone too: the node plays and reads from its copy, even after a restart", func(t *testing.T) {
			pgProxy.hang() // the node's connections and its copy's subscription: the home's database is gone
			csrf := map[string]string{"X-Maktaba-CSRF": u2.cookie(nodeWebURL, "mkt_csrf")}
			play := func(who string) {
				t.Helper()
				r, b := u2.do("POST", nodeWebURL+"/api/stream/sessions", map[string]any{"video_id": v1, "force_transcode": true}, csrf)
				var s openSession
				if r.StatusCode != 201 || json.Unmarshal(b, &s) != nil || s.SessionID == "" {
					t.Fatalf("%s: open with the home's database gone: %d %s", who, r.StatusCode, tail(string(b), 400))
				}
				playHLS(t, u2, nodeWebURL, s.ManifestURL, "local")
				if r, b := u2.do("DELETE", nodeWebURL+"/api/stream/sessions/"+s.SessionID, nil, csrf); r.StatusCode != 204 {
					t.Fatalf("%s: close with the home's database gone: %d %s", who, r.StatusCode, tail(string(b), 300))
				}
			}
			play("running node")
			waitFor(t, "a library read from the copy with the home's database gone", 30*time.Second, func() bool {
				r, _ := u2.do("GET", nodeWebURL+"/api/videos", nil, nil)
				return r.StatusCode == 200
			})

			// the node restarts in the outage: it starts without the home's database and plays
			node.stop()
			restartedNode := startMachine(t, "node (restarted)", bin, nodeEnv)
			restartedNode.web = nodeWebURL
			waitFor(t, "the restarted node's API", 90*time.Second, func() bool {
				r, err := http.Get(nodeWebURL + "/api/cluster/self")
				if err != nil {
					return false
				}
				r.Body.Close()
				return r.StatusCode == 200
			})
			// its copy and its streaming role may still be starting
			waitFor(t, "the restarted node's copy and streaming role", 90*time.Second, func() bool {
				r, b := u2.do("POST", nodeWebURL+"/api/stream/sessions", map[string]any{"video_id": v1, "force_transcode": true}, csrf)
				if r.StatusCode == 201 {
					var s openSession
					if json.Unmarshal(b, &s) == nil && s.SessionID != "" {
						_, _ = u2.do("DELETE", nodeWebURL+"/api/stream/sessions/"+s.SessionID, nil, csrf)
					}
					return true
				}
				return false
			})
			play("restarted node")
			if r, b := u2.do("GET", nodeWebURL+"/api/auth/me", nil, nil); r.StatusCode != 200 || !strings.Contains(string(b), `"username":"owner"`) {
				t.Fatalf("/api/auth/me on the restarted node: %d %s", r.StatusCode, tail(string(b), 300))
			}
			if r, b := newBrowser(t).do("POST", nodeWebURL+"/api/auth/login",
				map[string]string{"username": "owner", "password": "owner-pass-123"}, nil); r.StatusCode != 200 {
				t.Fatalf("a sign-in on the restarted node: %d %s", r.StatusCode, tail(string(b), 300))
			}
		})
	})

	t.Run("home gone, no copy: the node serves no one but its own routes", func(t *testing.T) {
		if withCopy {
			t.Skip("this node keeps a copy (the case above)")
		}
		u2 := newBrowser(t)
		resp, _ := u2.do("POST", nodeWebURL+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("sign-in through the node: %d", resp.StatusCode)
		}
		home.stop()
		waitFor(t, "a read refused (503 readable false)", 90*time.Second, func() bool {
			r, b := u2.do("GET", nodeWebURL+"/api/videos", nil, nil)
			return r.StatusCode == 503 && strings.Contains(string(b), `"readable":false`)
		})
		if r, b := newBrowser(t).do("POST", nodeWebURL+"/api/auth/login",
			map[string]string{"username": "owner", "password": "owner-pass-123"}, nil); r.StatusCode != 503 ||
			!strings.Contains(string(b), "home_unreachable") {
			t.Fatalf("a sign-in with the home gone and no copy: %d %s", r.StatusCode, tail(string(b), 300))
		}
		if r, _ := u2.do("GET", nodeWebURL+"/api/system/version", nil, nil); r.StatusCode != 200 {
			t.Fatalf("a local route with the home gone: %d", r.StatusCode)
		}
	})
}

// tcpProxy forwards the node's connections to the home (its database, its API); cut
// makes it a home that is gone (open connections dropped, new ones closed at once),
// hang one switched off (nothing answers, nothing is refused: open connections go
// silent, new ones are taken and never answered).
type tcpProxy struct {
	dsn     string
	ln      net.Listener
	target  string
	mu      sync.Mutex
	down    bool
	hanging bool
	conns   map[net.Conn]struct{}
	ups     map[net.Conn]struct{}
}

func newTCPProxy(t *testing.T, dsn string) *tcpProxy {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tcpProxy{ln: ln, target: u.Host, conns: map[net.Conn]struct{}{}, ups: map[net.Conn]struct{}{}}
	u.Host = ln.Addr().String()
	p.dsn = u.String()
	t.Cleanup(func() { _ = ln.Close(); p.cut() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p
}

func (p *tcpProxy) serve(c net.Conn) {
	p.mu.Lock()
	switch {
	case p.down:
		p.mu.Unlock()
		_ = c.Close()
		return
	case p.hanging:
		p.conns[c] = struct{}{}
		p.mu.Unlock()
		_, _ = io.Copy(io.Discard, c) // taken, never answered
		return
	}
	up, err := net.Dial("tcp", p.target)
	if err != nil {
		p.mu.Unlock()
		_ = c.Close()
		return
	}
	p.conns[c], p.conns[up], p.ups[up] = struct{}{}, struct{}{}, struct{}{}
	p.mu.Unlock()
	go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
	_, _ = io.Copy(c, up)
	p.mu.Lock()
	hanging := p.hanging
	p.mu.Unlock()
	if hanging {
		_, _ = io.Copy(io.Discard, c) // the client's side stays open and silent
		return
	}
	_ = c.Close()
}

func (p *tcpProxy) cut() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = true
	for c := range p.conns {
		_ = c.Close()
	}
	p.conns = map[net.Conn]struct{}{}
}

// hang: the far side is switched off. The connections to it end on this side only, so
// the node's connections go silent instead of being closed.
func (p *tcpProxy) hang() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hanging = true
	for c := range p.ups {
		_ = c.Close()
	}
	p.ups = map[net.Conn]struct{}{}
}

type openSession struct {
	SessionID   string `json:"session_id"`
	Mode        string `json:"mode"`
	ManifestURL string `json:"manifest_url"`
	DirectURL   string `json:"direct_url"`
}

// makeVideo writes a short real h264/aac mp4 (14 s) with ffmpeg.
func makeVideo(t *testing.T, path string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH (the VOD byte checks transcode a real fixture)")
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=25", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "14", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg fixture: %v\n%s", err, out)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Size()
}

func playlistURIs(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

// resolveRef resolves a playlist's child URI (path?query) against its playlist.
func resolveRef(base *url.URL, ref string) *url.URL {
	r, err := url.Parse(ref)
	if err != nil {
		return base
	}
	return base.ResolveReference(r)
}

// playHLS plays a session the way a browser does through a web port: the
// signed master playlist, its rendition playlist (relative URI, signature
// carried by the playlist) and two MPEG-TS segments. wantStream, when set, is
// the web port's X-Maktaba-Stream routing. Returns the first segment's URL.
func playHLS(t *testing.T, b *browser, base, manifest, wantStream string) string {
	t.Helper()
	resp, master := b.do("GET", base+manifest, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("manifest %s: %d %s", manifest, resp.StatusCode, tail(string(master), 300))
	}
	if wantStream != "" && resp.Header.Get("X-Maktaba-Stream") != wantStream {
		t.Fatalf("manifest %s routed %q, want %q", manifest, resp.Header.Get("X-Maktaba-Stream"), wantStream)
	}
	kids := playlistURIs(master)
	if len(kids) == 0 || !strings.Contains(kids[0], "sig=") {
		t.Fatalf("master playlist without signed children: %s", master)
	}
	mu, _ := url.Parse(manifest)
	rendition := resolveRef(mu, kids[0])
	var segs []string
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) && len(segs) < 2 {
		if r, idx := b.do("GET", base+rendition.String(), nil, nil); r.StatusCode == 200 {
			segs = playlistURIs(idx)
		}
		if len(segs) < 2 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	if len(segs) < 2 {
		t.Fatalf("rendition %s never listed two segments: %v", rendition.Path, segs)
	}
	var first string
	for _, sg := range segs[:2] {
		su := resolveRef(rendition, sg)
		r, data := b.do("GET", base+su.String(), nil, nil)
		if r.StatusCode != 200 || len(data) < 188 || data[0] != 0x47 {
			t.Fatalf("segment %s: %d, %d bytes", su.Path, r.StatusCode, len(data))
		}
		if first == "" {
			first = su.String()
		}
		t.Logf("%s: %s -> %d bytes of MPEG-TS", base, su.Path, len(data))
	}
	return first
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
