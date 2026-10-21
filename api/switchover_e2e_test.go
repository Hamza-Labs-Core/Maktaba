package main

// Switching the home, end to end on one PC (specs/epics/37-node-replica, "Switching the
// home"): two `maktaba-server serve` machines on 127.0.0.1 — a home A on its own bundled
// Postgres, and a full machine B whose supervisor keeps a copy of it — and an admin who
// asks A to hand over to B. Then, with no step by hand: B is the home (its copy promoted,
// A's secrets key and cluster key with it, its Postgres the home's), A is one of its
// machines (its database a copy of B's without copying again, its node.json written),
// both started again in their new parts; A's secret opens on B, a change made on B
// reaches A, the owner signs in on either, and B calls the switch done once A follows.
//
// Opt-in: MAKTABA_E2E_SWITCHOVER=1 and MAKTABA_TEST_PG_BIN (a Postgres 16 install); it
// builds the three binaries. Every path is under t.TempDir().

import (
	"bytes"
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

func TestSwitchTheHome(t *testing.T) {
	if os.Getenv("MAKTABA_E2E_SWITCHOVER") != "1" || testing.Short() {
		t.Skip("set MAKTABA_E2E_SWITCHOVER=1 (and MAKTABA_TEST_PG_BIN) to run the switch of the home")
	}
	pgPrograms := os.Getenv("MAKTABA_TEST_PG_BIN")
	if pgPrograms == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	buildBinaries(t, bin)
	repo, _ := filepath.Abs("..")
	self, _ := os.Executable()
	p := func() int { return freePort(t) }

	type box struct {
		dir                      string
		api, web, stream, pgPort int
		webURL                   string
	}
	a := box{dir: filepath.Join(tmp, "a"), api: p(), web: p(), stream: p(), pgPort: p()}
	b := box{dir: filepath.Join(tmp, "b"), api: p(), web: p(), stream: p(), pgPort: p()}
	// C: a machine that is off during the switch
	c := box{dir: filepath.Join(tmp, "c"), api: p(), web: p(), stream: p(), pgPort: p()}
	a.webURL = fmt.Sprintf("http://127.0.0.1:%d", a.web)
	b.webURL = fmt.Sprintf("http://localhost:%d", b.web) // another host: the browser keeps their cookies apart
	c.webURL = fmt.Sprintf("http://127.0.0.1:%d", c.web)
	toml := func(m box, home bool) string {
		extra := ""
		if home {
			extra = `role = "home"` + "\n"
		}
		pgOn := ""
		if home {
			pgOn = `enabled = "on"` + "\n"
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
`, m.api, filepath.ToSlash(filepath.Join(m.dir, "data")), m.web, m.webURL, p(), m.stream, p(), p(), p(), p(), extra,
			pgOn, filepath.ToSlash(pgPrograms), m.pgPort, m.pgPort, p())
		_ = os.MkdirAll(m.dir, 0o700)
		path := filepath.Join(m.dir, "server.toml")
		_ = os.WriteFile(path, []byte(body), 0o600)
		return path
	}
	env := func(m box, cfg string) []string {
		return []string{"MAKTABA_CONFIG=" + cfg, "MAKTABA_NODE_CONFIG=" + filepath.Join(m.dir, "node.json"),
			"MAKTABA_HOME_CLUSTER_KEY=" + filepath.Join(m.dir, "cluster.key"),
			"MAKTABA_HOME_AGENT_KEY=" + filepath.Join(m.dir, "agent.key"),
			"MAKTABA_SECRETS_KEY=" + filepath.Join(m.dir, "secrets.key"), "MAKTABA_DATA_DIR=" + filepath.Join(m.dir, "data"),
			"HF_HOME=" + filepath.Join(m.dir, "hf"), "MAKTABA_POSTGRES_HOME=" + pgPrograms,
			"MAKTABA_MIGRATIONS_DIR=" + filepath.Join(repo, "shared", "db", "migrations"),
			"MAKTABA_PYTHON=" + self, "MAKTABA_E2E_SLEEPER=1", "MAKTABA_DISCOVERY_MDNS=0", "MAKTABA_HSTS=0",
			"MAKTABA_HEALTH_WARM=0s", "MAKTABA_CLUSTER_DIR=" + filepath.Join(tmp, "clusterfiles"),
			"MAKTABA_ARCHIVE_DIR=" + filepath.Join(tmp, "archive"), "MAKTABA_UPDATE_DISABLE=1"}
	}
	get := func(u string) (int, []byte) {
		r, err := http.Get(u)
		if err != nil {
			return 0, nil
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, b
	}
	self200 := func(web string) func() bool {
		return func() bool {
			code, _ := get(web + "/api/cluster/self")
			return code == 200
		}
	}
	type selfInfo struct {
		Role string `json:"role"`
		Home *struct {
			WebURL string `json:"web_url"`
		} `json:"home"`
		Copy struct {
			State string `json:"state"`
		} `json:"copy"`
	}
	selfOf := func(web string) selfInfo {
		code, body := get(web + "/api/cluster/self")
		var s selfInfo
		if code == 200 {
			_ = json.Unmarshal(body, &s)
		}
		return s
	}

	// ---- A, the home, on its own Postgres ----
	homePriv, homePub, _ := cluster.GenerateKey()
	_ = os.MkdirAll(a.dir, 0o700)
	_ = os.WriteFile(filepath.Join(a.dir, "cluster.key"), homePriv, 0o600)
	aCfg := toml(a, true)
	ma := startMachine(t, "a", bin, env(a, aCfg))
	waitFor(t, "A, the home, on its own Postgres", 3*time.Minute, self200(a.webURL))
	readPW := func(dir, role string) string {
		raw, err := os.ReadFile(filepath.Join(dir, "postgres-"+role+".password"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(raw))
	}
	appDSN := func(m box) string {
		return (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba", readPW(m.dir, "maktaba")),
			Host: fmt.Sprintf("127.0.0.1:%d", m.pgPort), Path: "/maktaba", RawQuery: "sslmode=disable"}).String()
	}
	adb, err := sql.Open("postgres", appDSN(a))
	if err != nil {
		t.Fatal(err)
	}
	defer adb.Close()
	pw := filepath.Join(tmp, "owner.pw")
	_ = os.WriteFile(pw, []byte("owner-pass-123\n"), 0o600)
	add := exec.Command(filepath.Join(bin, exeName("maktaba-api")), "adduser", "--password-file", pw, "--admin", "owner")
	add.Env = append(os.Environ(), "DATABASE_URL="+appDSN(a))
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("adduser: %v\n%s", err, out)
	}
	// a secret of the home's, sealed with its key: it must open on the new home
	keyRaw, err := os.ReadFile(filepath.Join(a.dir, "secrets.key"))
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
	if _, err := adb.ExecContext(ctx, `INSERT INTO home_secrets (name, ciphertext, nonce, key_id) VALUES ('ext.plex.token', $1, $2, $3)`,
		ct, nonce, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adb.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at) VALUES ('test.switch', '"on a"', now())`); err != nil {
		t.Fatal(err)
	}

	// ---- B and C join A as full machines ----
	join := func(m box, name string) string {
		nodePriv, nodePub, _ := cluster.GenerateKey()
		id := uuid.NewString()
		role := cluster.RoleName(id)
		if err := (&cluster.PGRoles{DB: adb}).Create(ctx, role, "node-pass-0123456789"); err != nil {
			t.Fatal(err)
		}
		if _, err := adb.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role, approved_at,
		        approved_by, full_node) VALUES ($1, $2, 'approved', 'windows', 'amd64', $3, $4, now(), 'test', true)`,
			id, name, cluster.EncodeKey(nodePub), role); err != nil {
			t.Fatal(err)
		}
		nodeDSN := (&url.URL{Scheme: "postgres", User: url.UserPassword(role, "node-pass-0123456789"),
			Host: fmt.Sprintf("127.0.0.1:%d", a.pgPort), Path: "/maktaba", RawQuery: "sslmode=prefer"}).String()
		nodeCfg, _ := json.Marshal(map[string]any{"node_id": id, "name": name, "private_key": cluster.EncodeKey(nodePriv),
			"home":         map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d", a.api), "cluster_pub": cluster.EncodeKey(homePub)},
			"database_url": nodeDSN})
		if err := os.WriteFile(filepath.Join(m.dir, "node.json"), nodeCfg, 0o600); err != nil {
			t.Fatal(err)
		}
		return id
	}
	bCfg, cCfg := toml(b, false), toml(c, false) // first: they make the machines' directories
	nodeID := join(b, "b")
	cID := join(c, "c")
	mb := startMachine(t, "b", bin, env(b, bCfg))
	mc := startMachine(t, "c", bin, env(c, cCfg))
	waitFor(t, "B's copy following A", 6*time.Minute, func() bool { return selfOf(b.webURL).Copy.State == "following" })
	waitFor(t, "C's copy following A", 6*time.Minute, func() bool { return selfOf(c.webURL).Copy.State == "following" })
	// C is off during the switch: it learns of the new home when it is back
	mc.stop()

	// ---- the admin asks A to hand over to B ----
	admin := newBrowser(t)
	if r, body := admin.do("POST", a.webURL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "owner-pass-123"}, nil); r.StatusCode != 200 {
		t.Fatalf("sign-in on A: %d %s", r.StatusCode, tail(string(body), 300))
	}
	csrf := map[string]string{"X-Maktaba-CSRF": admin.cookie(a.webURL, "mkt_csrf")}
	type status struct {
		Candidates []struct {
			NodeID  string   `json:"node_id"`
			Ready   bool     `json:"ready"`
			Reasons []string `json:"reasons"`
		} `json:"candidates"`
	}
	var last string
	waitFor(t, "B ready to take over", 3*time.Minute, func() bool {
		_, body := admin.try("GET", a.webURL+"/api/cluster/switchover", nil, nil)
		last = string(body)
		var s status
		_ = json.Unmarshal(body, &s)
		for _, c := range s.Candidates {
			if c.NodeID == nodeID {
				return c.Ready
			}
		}
		return false
	})
	t.Logf("before the switch: %s", tail(last, 600))
	r, body := admin.do("POST", a.webURL+"/api/cluster/switchover", map[string]string{"to_node": nodeID}, csrf)
	if r.StatusCode != 202 {
		t.Fatalf("start the switch: %d %s", r.StatusCode, tail(string(body), 400))
	}
	// the switch as A tells it, for a run that fails
	var onA string
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the switch on A, last seen: %s", tail(onA, 2000))
		}
	})
	seeA := func() {
		if _, body := admin.try("GET", a.webURL+"/api/cluster/switchover", nil, nil); len(body) > 0 {
			onA = string(body)
		}
	}
	waitFor(t, "B the home", 6*time.Minute, func() bool { seeA(); return selfOf(b.webURL).Role == "home" })
	waitFor(t, "A one of B's machines", 6*time.Minute, func() bool {
		seeA()
		s := selfOf(a.webURL)
		return s.Role == "node" && s.Home != nil && s.Home.WebURL == b.webURL
	})

	// ---- C, back: it asks A, which hands it the new home's notice sealed for it ----
	mc = startMachine(t, "c2", bin, env(c, cCfg))
	waitFor(t, "C follows B by itself", 8*time.Minute, func() bool {
		s := selfOf(c.webURL)
		return s.Role == "node" && s.Home != nil && s.Home.WebURL == b.webURL && s.Copy.State == "following"
	})
	cNode, err := os.ReadFile(filepath.Join(c.dir, "node.json"))
	if err != nil || !strings.Contains(string(cNode), fmt.Sprintf(":%d", b.pgPort)) {
		t.Fatalf("C's node.json does not name B's database: %s %v", cNode, err)
	}
	if code, _ := get(fmt.Sprintf("%s/api/cluster/moved/%s", a.webURL, uuid.NewString())); code != http.StatusNotFound {
		t.Fatalf("a notice for a machine that has none: %d", code)
	}

	// ---- after: the secret opens on B, B's change reaches A, the owner signs in on both ----
	bdb, err := sql.Open("postgres", appDSN(b))
	if err != nil {
		t.Fatal(err)
	}
	defer bdb.Close()
	var sealed, n []byte
	var keyID string
	if err := bdb.QueryRowContext(ctx, `SELECT ciphertext, nonce, key_id FROM home_secrets WHERE name = 'ext.plex.token'`).
		Scan(&sealed, &n, &keyID); err != nil {
		t.Fatalf("the secret on the new home: %v", err)
	}
	bKeyRaw, _ := os.ReadFile(filepath.Join(b.dir, "secrets.key"))
	bKey, err := secrets.NewKey(bKeyRaw)
	if err != nil {
		t.Fatalf("the new home's secrets key: %v", err)
	}
	if plain, err := secrets.Open(bKey, "ext.plex.token", sealed, n); err != nil || string(plain) != "plex-token-value" {
		t.Fatalf("the secret opens on the new home: %q %v", plain, err)
	}
	if _, err := bdb.ExecContext(ctx, `UPDATE app_settings SET value = '"on b"' WHERE key = 'test.switch'`); err != nil {
		t.Fatalf("a change on the new home: %v", err)
	}
	asuper, err := sql.Open("postgres", (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba_admin",
		readPW(a.dir, "maktaba_admin")), Host: fmt.Sprintf("127.0.0.1:%d", a.pgPort), Path: "/maktaba",
		RawQuery: "sslmode=disable"}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer asuper.Close()
	waitFor(t, "B's change on A's copy", 2*time.Minute, func() bool {
		var v string
		_ = asuper.QueryRowContext(ctx, `SELECT value::text FROM app_settings WHERE key = 'test.switch'`).Scan(&v)
		return strings.Contains(v, "on b")
	})
	var aSecrets int
	_ = asuper.QueryRowContext(ctx, `SELECT count(*) FROM home_secrets`).Scan(&aSecrets)
	if aSecrets != 0 {
		t.Fatalf("the old home kept %d secrets", aSecrets)
	}
	onB := newBrowser(t)
	if r, body := onB.do("POST", b.webURL+"/api/auth/login",
		map[string]string{"username": "owner", "password": "owner-pass-123"}, nil); r.StatusCode != 200 {
		t.Fatalf("sign-in on the new home: %d %s", r.StatusCode, tail(string(body), 300))
	}
	var done string
	waitFor(t, "the switch done on the new home", 5*time.Minute, func() bool {
		_, body := onB.try("GET", b.webURL+"/api/cluster/switchover", nil, nil)
		done = string(body)
		var s struct {
			Recent []struct {
				State string                     `json:"state"`
				Integ map[string]json.RawMessage `json:"integrations"`
			} `json:"recent"`
			Term struct {
				Term int64 `json:"term"`
			} `json:"term"`
		}
		_ = json.Unmarshal(body, &s)
		return len(s.Recent) > 0 && s.Recent[0].State == "done" && s.Term.Term == 2 &&
			strings.Contains(string(s.Recent[0].Integ["secrets"]), `"moved"`)
	})
	t.Logf("the switch on the new home: %s", tail(done, 900))
	waitFor(t, "a sign-in on the old home, now a machine", 2*time.Minute, func() bool {
		code, _ := newBrowser(t).try("POST", a.webURL+"/api/auth/login",
			map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
		return code == 200
	})
	if hold := os.Getenv("MAKTABA_E2E_HOLD"); hold != "" {
		var secs int
		_, _ = fmt.Sscanf(hold, "%d", &secs)
		time.Sleep(time.Duration(secs) * time.Second)
	}
	_ = ma
	_ = mb
	_ = mc
	_ = cID
}

// try is do that does not fail the test when the machine does not answer (it may be
// starting again in its new part): status 0 then.
func (b *browser) try(method, u string, body any, hdr map[string]string) (int, []byte) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return 0, nil
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := b.c.Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}
