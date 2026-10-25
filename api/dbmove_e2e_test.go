package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// The home owns its machines' database access (the outage of 2026-10-24: the home's
// database moved from an operator Postgres on :5432 to the app's own on :54329, no role
// came along, and a node lost the home's database for good). Both halves, one after the
// other: the home's database "moves" for its node (the address the node had refuses: the
// home re-makes its access because it points elsewhere, refused_moved, and pushes the
// database's real address); then the node's role is gone from the database (its password
// refused: auth_failed, its role made again). Each time the node follows by itself and
// logs in again, within a minute or so. The logs page reads both machines' own log files
// (the home's from its supervisor, the node's over the machine channel).
func TestNodeFollowsTheHomeDatabase(t *testing.T) {
	if os.Getenv("MAKTABA_E2E_TWO_MACHINE") != "1" || testing.Short() {
		t.Skip("set MAKTABA_E2E_TWO_MACHINE=1 (and MAKTABA_TEST_PG_URL) to run the two-machine test")
	}
	db, dsn := clustertest.ScratchDB(t)
	ctx := context.Background()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	buildBinaries(t, bin)

	pw := filepath.Join(tmp, "owner.pw")
	_ = os.WriteFile(pw, []byte("owner-pass-123\n"), 0o600)
	add := exec.Command(filepath.Join(bin, exeName("maktaba-api")), "adduser", "--password-file", pw, "owner")
	add.Env = append(os.Environ(), "DATABASE_URL="+dsn)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("adduser: %v\n%s", err, out)
	}
	homePriv, homePub, _ := cluster.GenerateKey()
	nodePriv, nodePub, _ := cluster.GenerateKey()
	const nodeID = "7a2d3b4c-5e6f-4a71-8b82-a3c4d5e6f708"
	role := cluster.RoleName(nodeID)
	roles := &cluster.PGRoles{DB: db}
	if err := roles.Create(ctx, role, "node-pass-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = roles.Drop(context.Background(), role) })
	if _, err := db.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at, approved_by)
	       VALUES ($1, 'studio-gpu', 'approved', 'linux', 'amd64', $2, $3, now(), 'test')`, nodeID, cluster.EncodeKey(nodePub), role); err != nil {
		t.Fatal(err)
	}
	// the node reaches the database at an address the test can take away (the old one)
	nodeDSN := strings.Replace(clustertest.WithUser(t, dsn, role, "node-pass-old"), "sslmode=disable", "sslmode=prefer", 1)
	oldAddr := newTCPProxy(t, nodeDSN)

	homeDir, nodeDir := filepath.Join(tmp, "home"), filepath.Join(tmp, "node")
	_ = os.MkdirAll(homeDir, 0o700)
	_ = os.MkdirAll(nodeDir, 0o700)
	_ = os.WriteFile(filepath.Join(homeDir, "cluster.key"), homePriv, 0o600)
	p := func() int { return freePort(t) }
	toml := func(dir string, api int, extra string) string {
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
`, api, filepath.ToSlash(filepath.Join(dir, "data")), p(), p(), p(), p(), p(), p(), p(), extra, p())
		_ = os.WriteFile(path, []byte(body), 0o600)
		return path
	}
	hAPI := p()
	homeToml := toml(homeDir, hAPI, fmt.Sprintf("role = \"home\"\n[database]\nurl = %q", dsn))
	homeWebURL := fmt.Sprintf("http://127.0.0.1:%d", hAPI)
	nodeToml := toml(nodeDir, p(), "[database]\nurl = \"sqlite://unused.db\"")
	nodeCfgPath := filepath.Join(nodeDir, "node.json")
	nodeCfg, _ := json.Marshal(map[string]any{"node_id": nodeID, "name": "studio-gpu", "private_key": cluster.EncodeKey(nodePriv),
		"home":         map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d", hAPI), "cluster_pub": cluster.EncodeKey(homePub)},
		"database_url": oldAddr.dsn})
	_ = os.WriteFile(nodeCfgPath, nodeCfg, 0o600)
	self, _ := os.Executable()
	repo, _ := filepath.Abs("..")
	common := []string{"MAKTABA_PYTHON=" + self, "MAKTABA_E2E_SLEEPER=1", "MAKTABA_DISCOVERY_MDNS=0", "MAKTABA_HSTS=0",
		"MAKTABA_HEALTH_WARM=0s", "MAKTABA_CLUSTER_DIR=" + filepath.Join(tmp, "clusterfiles"),
		"MAKTABA_ARCHIVE_DIR=" + filepath.Join(tmp, "archive"), "MAKTABA_UPDATE_DISABLE=1"}
	home := startMachine(t, "home", bin, append(common, "MAKTABA_CONFIG="+homeToml,
		"MAKTABA_NODE_CONFIG="+filepath.Join(homeDir, "no-node.json"),
		"MAKTABA_HOME_CLUSTER_KEY="+filepath.Join(homeDir, "cluster.key"), "MAKTABA_HOME_AGENT_KEY="+filepath.Join(homeDir, "agent.key"),
		"MAKTABA_SECRETS_KEY="+filepath.Join(homeDir, "secrets.key"), "MAKTABA_DATA_DIR="+filepath.Join(homeDir, "data"),
		"MAKTABA_LOG_DIR="+filepath.Join(homeDir, "logs"),
		"MAKTABA_MIGRATIONS_DIR="+filepath.Join(repo, "shared", "db", "migrations")))
	home.web = homeWebURL
	waitFor(t, "the home's API", 90*time.Second, func() bool {
		r, err := http.Get(homeWebURL + "/api/cluster/self")
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == 200
	})
	node := startMachine(t, "node", bin, append(common, "MAKTABA_CONFIG="+nodeToml, "MAKTABA_NODE_CONFIG="+nodeCfgPath,
		"MAKTABA_DATA_DIR="+filepath.Join(nodeDir, "data"), "MAKTABA_LOG_DIR="+filepath.Join(nodeDir, "logs"),
		"MAKTABA_NODE_COPY=off"))
	admin := newBrowser(t)
	if r, b := admin.do("POST", homeWebURL+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil); r.StatusCode != 200 {
		t.Fatalf("sign-in on the home: %d %s", r.StatusCode, b)
	}
	homeDB := func() (said, ok bool, code string) {
		r, b := admin.do("GET", homeWebURL+"/api/cluster", nil, nil)
		if r == nil || r.StatusCode != 200 {
			return false, false, ""
		}
		var o struct {
			Nodes []struct {
				ID          string `json:"id"`
				UpdateState struct {
					HomeDB *struct {
						OK   bool   `json:"ok"`
						Code string `json:"code"`
					} `json:"home_db"`
				} `json:"update_state"`
			} `json:"nodes"`
		}
		_ = json.Unmarshal(b, &o)
		for _, n := range o.Nodes {
			if n.ID == nodeID && n.UpdateState.HomeDB != nil {
				return true, n.UpdateState.HomeDB.OK, n.UpdateState.HomeDB.Code
			}
		}
		return false, false, ""
	}
	waitFor(t, "the node logged in to the home's database, and said so", 3*time.Minute, func() bool {
		said, ok, _ := homeDB()
		return said && ok
	})

	// the move: the address the node had refuses (nothing listens there now)
	oldAddr.cut()
	_ = oldAddr.ln.Close()
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, role); err != nil {
		t.Fatal(err)
	}
	moved := time.Now()

	// the node follows it by itself: its role made again, node.json pointing at the
	// database's real address, a session of its own on it, and the home seeing it log in
	realPort := func() string {
		u, _ := url.Parse(dsn)
		return u.Port()
	}()
	loggedIn := func() bool {
		var can bool
		if err := db.QueryRowContext(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname = $1`, role).Scan(&can); err != nil || !can {
			return false
		}
		b, err := os.ReadFile(nodeCfgPath)
		if err != nil || !strings.Contains(string(b), ":"+realPort+"/") {
			return false
		}
		var sessions int
		_ = db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename = $1`, role).Scan(&sessions)
		said, ok, _ := homeDB()
		return sessions > 0 && said && ok
	}
	waitFor(t, "the node following the home's moved database", 3*time.Minute, loggedIn)
	t.Logf("the node followed the home's moved database in %s", time.Since(moved).Round(time.Second))
	// re-made because the database moved (the node's address refuses), not by luck
	if !strings.Contains(home.log.String(), "why=refused_moved") {
		t.Errorf("the home did not re-make the access for the moved database:\n%s", tail(home.log.String(), 3000))
	}

	// then its role is gone from the database (a move that carried no roles): its password
	// is refused, and its role is made again
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, role); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP ROLE `+role); err != nil {
		t.Fatalf("drop the node's role: %v", err)
	}
	dropped := time.Now()
	waitFor(t, "the node logging in with its role made again", 3*time.Minute, loggedIn)
	t.Logf("the node logged in with its role made again in %s", time.Since(dropped).Round(time.Second))
	if !strings.Contains(home.log.String(), "why=auth_failed") {
		t.Errorf("the home did not re-make the access for the refused password:\n%s", tail(home.log.String(), 3000))
	}
	if !strings.Contains(node.log.String(), "following it at") {
		t.Errorf("the node's log does not say it follows the home's database:\n%s", tail(node.log.String(), 3000))
	}
	if !strings.Contains(home.log.String(), "cluster_access_remade") {
		t.Errorf("the home's log does not say it made the access again:\n%s", tail(home.log.String(), 3000))
	}

	// the logs page: each machine's app.log over a window, the node's over the channel
	stamp := time.Now().UTC().Truncate(time.Second)
	for dir, line := range map[string]string{homeDir: "the home's update line", nodeDir: "the node's update line"} {
		_ = os.MkdirAll(filepath.Join(dir, "logs"), 0o700)
		body := stamp.Add(-time.Hour).Format(time.RFC3339) + " before the window\n" + stamp.Format(time.RFC3339) + " " + line + "\n"
		if err := os.WriteFile(filepath.Join(dir, "logs", "app.log"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	since := url.QueryEscape(stamp.Add(-time.Minute).Format(time.RFC3339))
	for q, want := range map[string]string{
		"files=app&since=" + since:                     "the home's update line",
		"files=app&since=" + since + "&node=" + nodeID: "the node's update line",
	} {
		r, b := admin.do("GET", homeWebURL+"/api/admin/logs/files?"+q, nil, nil)
		if r == nil || r.StatusCode != 200 || !strings.Contains(string(b), want) || strings.Contains(string(b), "before the window") {
			t.Errorf("the logs page's files (%s): %v %s", q, r, b)
		}
	}
}
