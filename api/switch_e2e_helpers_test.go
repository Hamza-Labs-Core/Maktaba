package main

// The three machines the switch tests run (switchover_e2e_test.go, emergency_e2e_test.go):
// a home A on its own bundled Postgres and two full machines B and C whose supervisors
// keep copies of it, every one a `maktaba-server serve` on 127.0.0.1.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// switchBox is one machine's ports and directory.
type switchBox struct {
	dir                             string
	api, web, stream, pgPort, relay int
	webURL                          string
}

// switchCluster is A and its two machines.
type switchCluster struct {
	t                           *testing.T
	ctx                         context.Context
	tmp, bin, repo, self, pgBin string
	homePub                     []byte
	a, b, c                     switchBox
	aCfg, bCfg, cCfg            string
	adb                         *sql.DB
	ma, mb, mc                  *machine
	nodeB, nodeC                string
}

type switchSelf struct {
	Role string `json:"role"`
	Term int64  `json:"term"`
	Home *struct {
		WebURL string `json:"web_url"`
	} `json:"home"`
	Copy struct {
		State string `json:"state"`
	} `json:"copy"`
}

// newSwitchCluster starts A (relay: with the cloud relay agent, a stand-in, and its
// credentials) and B and C following it. It skips unless MAKTABA_E2E_SWITCHOVER=1 and
// MAKTABA_TEST_PG_BIN are set.
func newSwitchCluster(t *testing.T, relay bool) *switchCluster {
	t.Helper()
	if os.Getenv("MAKTABA_E2E_SWITCHOVER") != "1" || testing.Short() {
		t.Skip("set MAKTABA_E2E_SWITCHOVER=1 (and MAKTABA_TEST_PG_BIN) to run the switch of the home")
	}
	pgBin := os.Getenv("MAKTABA_TEST_PG_BIN")
	if pgBin == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	c := &switchCluster{t: t, ctx: context.Background(), tmp: t.TempDir(), pgBin: pgBin}
	c.bin = filepath.Join(c.tmp, "bin")
	buildBinaries(t, c.bin)
	c.repo, _ = filepath.Abs("..")
	c.self, _ = os.Executable()
	p := func() int { return freePort(t) }
	box := func(name string) switchBox {
		return switchBox{dir: filepath.Join(c.tmp, name), api: p(), web: p(), stream: p(), pgPort: p(), relay: p()}
	}
	c.a, c.b, c.c = box("a"), box("b"), box("c")
	c.a.webURL = fmt.Sprintf("http://127.0.0.1:%d", c.a.web)
	c.b.webURL = fmt.Sprintf("http://localhost:%d", c.b.web) // another host: the browser keeps their cookies apart
	c.c.webURL = fmt.Sprintf("http://127.0.0.1:%d", c.c.web)

	// ---- A, the home, on its own Postgres ----
	homePriv, homePub, _ := cluster.GenerateKey()
	c.homePub = homePub
	_ = os.MkdirAll(c.a.dir, 0o700)
	_ = os.WriteFile(filepath.Join(c.a.dir, "cluster.key"), homePriv, 0o600)
	if relay {
		// the relay agent's credentials, as a claim leaves them (the stand-in reads nothing)
		_ = os.WriteFile(filepath.Join(c.a.dir, "cloudlink.cred"), []byte("sealed credentials"), 0o600)
	}
	c.aCfg = c.toml(c.a, true, relay)
	c.ma = startMachine(t, "a", c.bin, c.env(c.a, c.aCfg))
	waitFor(t, "A, the home, on its own Postgres", 3*time.Minute, c.answers(c.a.webURL))
	var err error
	if c.adb, err = sql.Open("postgres", c.appDSN(c.a)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.adb.Close() })
	pw := filepath.Join(c.tmp, "owner.pw")
	_ = os.WriteFile(pw, []byte("owner-pass-123\n"), 0o600)
	add := exec.Command(filepath.Join(c.bin, exeName("maktaba-api")), "adduser", "--password-file", pw, "--admin", "owner")
	add.Env = append(os.Environ(), "DATABASE_URL="+c.appDSN(c.a))
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("adduser: %v\n%s", err, out)
	}
	// a secret of the home's, sealed with its key: it must open on the new home
	keyRaw, err := os.ReadFile(filepath.Join(c.a.dir, "secrets.key"))
	if err != nil {
		t.Fatalf("the home's secrets key: %v", err)
	}
	key, err := secrets.NewKey(keyRaw)
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := secrets.Seal(key, "ext.plex.token", []byte("plex-token-value"))
	if err != nil {
		t.Fatal(err)
	}
	c.exec(c.adb, `INSERT INTO home_secrets (name, ciphertext, nonce, key_id) VALUES ('ext.plex.token', $1, $2, $3)`,
		ct, nonce, key.ID)
	c.exec(c.adb, `INSERT INTO app_settings (key, value, updated_at) VALUES ('test.switch', '"on a"', now())`)

	// ---- B and C join A as full machines ----
	c.bCfg, c.cCfg = c.toml(c.b, false, relay), c.toml(c.c, false, relay) // first: they make the machines' directories
	c.nodeB = c.join(c.b, "b")
	c.nodeC = c.join(c.c, "c")
	c.mb = startMachine(t, "b", c.bin, c.env(c.b, c.bCfg))
	c.mc = startMachine(t, "c", c.bin, c.env(c.c, c.cCfg))
	waitFor(t, "B's copy following A", 6*time.Minute, func() bool { return c.selfOf(c.b.webURL).Copy.State == "following" })
	waitFor(t, "C's copy following A", 6*time.Minute, func() bool { return c.selfOf(c.c.webURL).Copy.State == "following" })
	return c
}

func (c *switchCluster) exec(db *sql.DB, q string, args ...any) {
	c.t.Helper()
	if _, err := db.ExecContext(c.ctx, q, args...); err != nil {
		c.t.Fatalf("%s: %v", q, err)
	}
}

// toml is a machine's server.toml (home: A's own Postgres on; relay: the cloud relay, on
// the home only, every machine's status address its own).
func (c *switchCluster) toml(m switchBox, home, relay bool) string {
	extra, pgOn := "", ""
	if home {
		extra, pgOn = `role = "home"`+"\n", `enabled = "on"`+"\n"
	}
	cloud := ""
	if relay {
		cloud = fmt.Sprintf("[cloud]\nenabled = %v\nadmin_addr = \"127.0.0.1:%d\"\n", home, m.relay)
	}
	body := fmt.Sprintf(`[server]
listen = "127.0.0.1:%d"
data_dir = %q
web_listen = "127.0.0.1:%d"
web_public_url = %q
admin_addr = "127.0.0.1:%d"
stream_addr = "127.0.0.1:%d"
streaming_grpc_addr = "127.0.0.1:%d"
streaming_admin_addr = "127.0.0.1:%d"
pipeline_grpc_addr = "127.0.0.1:%d"
pipeline_logs_addr = "127.0.0.1:%d"
%s
[postgres]
%shome = %q
port = %d
copy_port = %d
listen = "loopback"

[supervisor]
admin_addr = "127.0.0.1:%d"

[tv]
slots = "off"

%s`, m.api, filepath.ToSlash(filepath.Join(m.dir, "data")), m.web, m.webURL, freePort(c.t), m.stream, freePort(c.t),
		freePort(c.t), freePort(c.t), freePort(c.t), extra, pgOn, filepath.ToSlash(c.pgBin), m.pgPort, m.pgPort,
		freePort(c.t), cloud)
	_ = os.MkdirAll(m.dir, 0o700)
	path := filepath.Join(m.dir, "server.toml")
	_ = os.WriteFile(path, []byte(body), 0o600)
	return path
}

func (c *switchCluster) env(m switchBox, cfg string) []string {
	return []string{"MAKTABA_CONFIG=" + cfg, "MAKTABA_NODE_CONFIG=" + filepath.Join(m.dir, "node.json"),
		"MAKTABA_HOME_CLUSTER_KEY=" + filepath.Join(m.dir, "cluster.key"),
		"MAKTABA_HOME_AGENT_KEY=" + filepath.Join(m.dir, "agent.key"),
		"MAKTABA_SECRETS_KEY=" + filepath.Join(m.dir, "secrets.key"), "MAKTABA_DATA_DIR=" + filepath.Join(m.dir, "data"),
		"HF_HOME=" + filepath.Join(m.dir, "hf"), "MAKTABA_POSTGRES_HOME=" + c.pgBin,
		"MAKTABA_MIGRATIONS_DIR=" + filepath.Join(c.repo, "shared", "db", "migrations"),
		"MAKTABA_PYTHON=" + c.self, "MAKTABA_E2E_SLEEPER=1", "MAKTABA_DISCOVERY_MDNS=0", "MAKTABA_HSTS=0",
		// the cloud relay agent: this test binary stands in for it (TestMain)
		"MAKTABA_CLOUDLINK_BIN=" + c.self, "MAKTABA_E2E_FAKE_CLOUDLINK=1",
		"MAKTABA_HEALTH_WARM=0s", "MAKTABA_CLUSTER_DIR=" + filepath.Join(c.tmp, "clusterfiles"),
		"MAKTABA_ARCHIVE_DIR=" + filepath.Join(c.tmp, "archive"), "MAKTABA_UPDATE_DISABLE=1"}
}

// join registers a full machine on A and writes its node.json.
func (c *switchCluster) join(m switchBox, name string) string {
	nodePriv, nodePub, _ := cluster.GenerateKey()
	id := uuid.NewString()
	role := cluster.RoleName(id)
	if err := (&cluster.PGRoles{DB: c.adb}).Create(c.ctx, role, "node-pass-0123456789"); err != nil {
		c.t.Fatal(err)
	}
	c.exec(c.adb, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at,
	        approved_by, full_node, web_url) VALUES ($1, $2, 'approved', 'windows', 'amd64', $3, $4, now(), 'test', true, $5)`,
		id, name, cluster.EncodeKey(nodePub), role, m.webURL)
	nodeDSN := (&url.URL{Scheme: "postgres", User: url.UserPassword(role, "node-pass-0123456789"),
		Host: fmt.Sprintf("127.0.0.1:%d", c.a.pgPort), Path: "/maktaba", RawQuery: "sslmode=prefer"}).String()
	nodeCfg, _ := json.Marshal(map[string]any{"node_id": id, "name": name, "private_key": cluster.EncodeKey(nodePriv),
		"home": map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d", c.a.api), "name": "a",
			"cluster_pub": cluster.EncodeKey(c.homePub)},
		"database_url": nodeDSN})
	if err := os.WriteFile(filepath.Join(m.dir, "node.json"), nodeCfg, 0o600); err != nil {
		c.t.Fatal(err)
	}
	return id
}

func (c *switchCluster) readPW(dir, role string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "postgres-"+role+".password"))
	if err != nil {
		c.t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func (c *switchCluster) appDSN(m switchBox) string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba", c.readPW(m.dir, "maktaba")),
		Host: fmt.Sprintf("127.0.0.1:%d", m.pgPort), Path: "/maktaba", RawQuery: "sslmode=disable"}).String()
}

func (c *switchCluster) superDSN(m switchBox) string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba_admin", c.readPW(m.dir, "maktaba_admin")),
		Host: fmt.Sprintf("127.0.0.1:%d", m.pgPort), Path: "/maktaba", RawQuery: "sslmode=disable"}).String()
}

func (c *switchCluster) get(u string) (int, []byte) {
	r, err := http.Get(u)
	if err != nil {
		return 0, nil
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, b
}

func (c *switchCluster) answers(web string) func() bool {
	return func() bool {
		code, _ := c.get(web + "/api/cluster/self")
		return code == 200
	}
}

func (c *switchCluster) selfOf(web string) switchSelf {
	code, body := c.get(web + "/api/cluster/self")
	var s switchSelf
	if code == 200 {
		_ = json.Unmarshal(body, &s)
	}
	return s
}

// signIn signs the owner in on a machine: the browser and its CSRF header.
func (c *switchCluster) signIn(web string) (*browser, map[string]string) {
	c.t.Helper()
	br := newBrowser(c.t)
	var code int
	var body []byte
	waitFor(c.t, "a sign-in on "+web, 2*time.Minute, func() bool {
		code, body = br.try("POST", web+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
		return code == 200
	})
	if code != 200 {
		c.t.Fatalf("sign-in on %s: %d %s", web, code, tail(string(body), 300))
	}
	return br, map[string]string{"X-Maktaba-CSRF": br.cookie(web, "mkt_csrf")}
}

// relayUp reports whether a machine's cloud relay agent answers on its status address.
func (c *switchCluster) relayUp(m switchBox) bool {
	code, body := c.get(fmt.Sprintf("http://127.0.0.1:%d/admin/cloud-link", m.relay))
	return code == 200 && strings.Contains(string(body), `"online"`)
}
