package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
)

// A machine gets its database access back by asking (the live case of 2026-10-25: a home
// on its bundled Postgres whose re-made access never reached its machine). The home here
// never pushes one (MAKTABA_E2E_NO_ACCESS_PUSH): the node asks (access.request) and the
// home answers on its session, three times —
//   - its database moved (node.json's port refuses): its access made and sent;
//   - its role is one the home's user does not administer, as a restore makes it
//     (Postgres 16 refuses the app role its new password): the home has its supervisor
//     make the cluster grants again (mend-roles), then serves the request;
//   - the home's user lost CREATEROLE: the same mend, then the request served.
//
// Each time the node follows by itself and logs in again.
func TestNodeAsksForItsAccess(t *testing.T) {
	if os.Getenv("MAKTABA_E2E_TWO_MACHINE") != "1" || testing.Short() {
		t.Skip("set MAKTABA_E2E_TWO_MACHINE=1 (and MAKTABA_TEST_PG_BIN) to run the two-machine test")
	}
	pgBin := os.Getenv("MAKTABA_TEST_PG_BIN")
	if pgBin == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install: the home runs on its own bundled Postgres)")
	}
	c := &switchCluster{t: t, tmp: t.TempDir(), pgBin: pgBin}
	c.ctx = t.Context()
	c.bin = filepath.Join(c.tmp, "bin")
	buildBinaries(t, c.bin)
	c.repo, _ = filepath.Abs("..")
	c.self, _ = os.Executable()
	p := func() int { return freePort(t) }
	box := func(name string) switchBox {
		return switchBox{dir: filepath.Join(c.tmp, name), api: p(), web: p(), stream: p(), pgPort: p(), relay: p()}
	}
	c.a, c.b = box("a"), box("b")
	c.a.webURL = fmt.Sprintf("http://127.0.0.1:%d", c.a.web)
	c.b.webURL = fmt.Sprintf("http://localhost:%d", c.b.web)

	homePriv, homePub, _ := cluster.GenerateKey()
	c.homePub = homePub
	_ = os.MkdirAll(c.a.dir, 0o700)
	_ = os.WriteFile(filepath.Join(c.a.dir, "cluster.key"), homePriv, 0o600)
	c.aCfg = c.toml(c.a, true, false)
	c.ma = startMachine(t, "a", c.bin, append(isolated(c.env(c.a, c.aCfg), c.a.dir), "MAKTABA_E2E_NO_ACCESS_PUSH=1"))
	waitFor(t, "A, the home, on its own Postgres", 3*time.Minute, c.answers(c.a.webURL))
	var err error
	if c.adb, err = sql.Open("postgres", c.appDSN(c.a)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.adb.Close() })
	super, err := sql.Open("postgres", c.superDSN(c.a))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = super.Close() })

	// B joined A; its node.json points at a port that refuses (the database moved)
	c.bCfg = c.toml(c.b, false, false)
	nodeB := c.join(c.b, "b")
	role := cluster.RoleName(nodeB)
	nodeCfg := filepath.Join(c.b.dir, "node.json")
	raw, _ := os.ReadFile(nodeCfg)
	var n map[string]any
	_ = json.Unmarshal(raw, &n)
	moved, _ := url.Parse(n["database_url"].(string))
	moved.Host = fmt.Sprintf("127.0.0.1:%d", p())
	n["database_url"] = moved.String()
	raw, _ = json.Marshal(n)
	_ = os.WriteFile(nodeCfg, raw, 0o600)
	c.mb = startMachine(t, "b", c.bin, append(isolated(c.env(c.b, c.bCfg), c.b.dir), "MAKTABA_NODE_COPY=off",
		"MAKTABA_E2E_ACCESS_ASK=5s"))

	loggedIn := func() bool {
		b, err := os.ReadFile(nodeCfg)
		if err != nil || !strings.Contains(string(b), fmt.Sprintf(":%d/", c.a.pgPort)) {
			return false
		}
		var sessions int
		_ = super.QueryRowContext(c.ctx, `SELECT count(*) FROM pg_stat_activity WHERE usename = $1`, role).Scan(&sessions)
		return sessions > 0
	}
	start := time.Now()
	waitFor(t, "B asking for its access and logging in with it", 4*time.Minute, loggedIn)
	t.Logf("B got its access by asking in %s", time.Since(start).Round(time.Second))
	for log, want := range map[string]string{c.ma.log.String(): "cluster_access_requested",
		c.mb.log.String(): "this machine asks the home for one"} {
		if !strings.Contains(log, want) {
			t.Errorf("no %q in the log:\n%s", want, tail(log, 3000))
		}
	}
	if strings.Contains(c.ma.log.String(), "cluster_access_remade") {
		t.Error("the home pushed an access it was told not to")
	}

	// the node's role, as a restore makes it: the home's user does not administer it
	refuse := func() {
		t.Helper()
		c.exec(super, `ALTER ROLE `+pq.QuoteIdentifier(role)+` PASSWORD 'not-the-nodes-`+uuid.NewString()[:8]+`'`)
		c.exec(super, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1`, role)
	}
	c.exec(super, `REVOKE `+pq.QuoteIdentifier(role)+` FROM maktaba CASCADE`)
	refuse()
	start = time.Now()
	administered := func() bool {
		var admin bool
		_ = super.QueryRowContext(c.ctx, `SELECT EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid
		    JOIN pg_roles a ON a.oid = m.member WHERE r.rolname = $1 AND a.rolname = 'maktaba' AND m.admin_option)`, role).Scan(&admin)
		return admin
	}
	waitFor(t, "the home mending its grants on B's role, and B logging in again", 4*time.Minute, func() bool {
		return administered() && loggedIn() && strings.Count(c.ma.log.String(), "cluster_access_requested") >= 2
	})
	t.Logf("a role the home did not administer, mended and B back in %s", time.Since(start).Round(time.Second))
	if !strings.Contains(c.ma.log.String(), "cluster_roles_mended") {
		t.Errorf("the home's log does not say it mended its grants:\n%s", tail(c.ma.log.String(), 3000))
	}

	// the home's user loses CREATEROLE
	c.exec(super, `ALTER ROLE maktaba NOCREATEROLE`)
	refuse()
	start = time.Now()
	waitFor(t, "the home giving its user CREATEROLE again, and B logging in again", 4*time.Minute, func() bool {
		var can bool
		_ = super.QueryRowContext(c.ctx, `SELECT rolcreaterole FROM pg_roles WHERE rolname = 'maktaba'`).Scan(&can)
		return can && loggedIn() && strings.Count(c.ma.log.String(), "cluster_access_requested") >= 3
	})
	t.Logf("CREATEROLE mended and B back in %s", time.Since(start).Round(time.Second))
}

// isolated is a machine's environment with its own profile and logs under dir: nothing of
// it reaches this computer's own install (its %LOCALAPPDATA%\Maktaba, its home's folders).
func isolated(env []string, dir string) []string {
	prof := filepath.Join(dir, "profile")
	_ = os.MkdirAll(filepath.Join(prof, "AppData", "Local"), 0o700)
	_ = os.MkdirAll(filepath.Join(prof, "AppData", "Roaming"), 0o700)
	return append(env, "MAKTABA_LOG_DIR="+filepath.Join(dir, "logs"), "HOME="+prof, "USERPROFILE="+prof,
		"LOCALAPPDATA="+filepath.Join(prof, "AppData", "Local"), "APPDATA="+filepath.Join(prof, "AppData", "Roaming"),
		"XDG_CONFIG_HOME="+filepath.Join(prof, ".config"), "XDG_DATA_HOME="+filepath.Join(prof, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(prof, ".local", "state"))
}
