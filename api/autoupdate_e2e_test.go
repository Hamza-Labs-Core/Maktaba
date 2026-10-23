package main

// Epic 35 phase B and D end to end on one PC (stories 35.4, 35.5, 35.7, 35.12;
// specs/epics/35-cluster-full-nodes-and-updates/install-kinds.md): a home and a full
// node, each a plain server install on the side-by-side layout (the launcher over
// bin/releases), the home on Maktaba's own Postgres. Three releases are built from this
// tree with versions stamped, signed by a local fake Sigstore (releaseverifytest, the
// trust root the -tags e2etrust builds take from MAKTABA_E2E_TRUSTED_ROOT) and published
// on a fake GitHub. It proves:
//
//   - Update now on the home installs 0.9.1 on the home (verified, staged, smoke-tested,
//     its migration rehearsed on a scratch copy, switched to under the launcher's guard,
//     the database dumped and migrated by the new release's start, watched, committed),
//     then on the node (prepared over the cluster channel, applied, watched, committed);
//   - a 0.9.2 whose API cannot run on the schema it migrated is rolled back on the home
//     alone: the previous release runs again, the dump taken before 0.9.2's migration is
//     put back, the version is blocked, and the node is never asked.
//
// Opt-in (it builds twelve binaries and runs two Postgres-backed machines):
// MAKTABA_E2E_UPDATE=1 and MAKTABA_TEST_PG_BIN (a Postgres 16 install: initdb, postgres,
// pg_ctl, pg_dump, pg_restore). It never reads this machine's own installs: every path
// is under t.TempDir().

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/install"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const updateRepo = rv.DefaultRepo

// buildRelease builds the four binaries of a release with version stamped (and the
// e2e trust root hook) into dir; schemaRev > 0 is the newest migration its API takes
// for its own (a release past this tree's schema revision).
func buildRelease(t *testing.T, dir, version string, schemaRev int) map[string][]byte {
	t.Helper()
	root, _ := filepath.Abs("..")
	out := map[string][]byte{}
	for _, b := range []struct{ name, dir, ldflag string }{
		{"maktaba-api", filepath.Join(root, "api"), "github.com/Hamza-Labs-Core/Maktaba/api/internal/version.Version"},
		{"maktaba-streaming", filepath.Join(root, "streaming"), "github.com/Hamza-Labs-Core/Maktaba/streaming/internal/version.Version"},
		{"maktaba-server", filepath.Join(root, "cmd", "maktaba-server"), "github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/version.Version"},
		{"maktaba-launcher", filepath.Join(root, "cmd", "maktaba-launcher"), "main.Version"},
	} {
		path := filepath.Join(dir, exeName(b.name))
		ld := "-X " + b.ldflag + "=" + version
		if b.name == "maktaba-api" && schemaRev > 0 {
			ld += fmt.Sprintf(" -X main.e2eSchemaRev=%d", schemaRev)
		}
		cmd := exec.Command("go", "build", "-tags", "e2etrust", "-ldflags", ld, "-o", path, ".")
		cmd.Dir = b.dir
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s %s: %v\n%s", b.name, version, err, o)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out[exeName(b.name)] = data
	}
	return out
}

// migrationFiles are the repository's migrations, without the slots in skip.
func migrationFiles(t *testing.T, skip ...string) map[string][]byte {
	t.Helper()
	dir := filepath.Join("..", "shared", "db", "migrations")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
next:
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".sql") {
			continue
		}
		for _, s := range skip {
			if strings.HasPrefix(n, s+"_") {
				continue next
			}
		}
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		out[n] = b
	}
	return out
}

// fakeGitHub serves the published releases' API and assets.
type fakeGitHub struct {
	mu       sync.Mutex
	srv      *httptest.Server
	releases []map[string]any
	files    map[string][]byte
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{files: map[string][]byte{}}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		base := "/repos/" + updateRepo + "/releases"
		switch p := r.URL.Path; {
		case p == base:
			_ = json.NewEncoder(w).Encode(g.releases)
		case strings.HasPrefix(p, base+"/tags/"):
			tag := strings.TrimPrefix(p, base+"/tags/")
			for _, rel := range g.releases {
				if rel["tag_name"] == tag {
					_ = json.NewEncoder(w).Encode(rel)
					return
				}
			}
			http.NotFound(w, r)
		case strings.HasPrefix(p, "/dl/"):
			b, ok := g.files[strings.TrimPrefix(p, "/dl/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) publish(r *releaseverifytest.FakeRelease) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var assets []map[string]any
	for n, b := range r.Files {
		g.files[r.Tag+"/"+n] = b
		assets = append(assets, map[string]any{"name": n, "browser_download_url": g.srv.URL + "/dl/" + r.Tag + "/" + n,
			"size": len(b)})
	}
	g.releases = append([]map[string]any{{"tag_name": r.Tag, "html_url": "https://github.test/" + r.Tag,
		"body": "notes " + r.Tag, "prerelease": false, "published_at": time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339),
		"assets": assets}}, g.releases...)
}

// installRelease installs r as the first release of the layout at root (as an install
// script would): staged and verified, current, the launcher beside it.
func installRelease(t *testing.T, root string, r *releaseverifytest.FakeRelease, tm *releaseverifytest.Sigstore) {
	t.Helper()
	trust, err := tm.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := rv.NewIdentity(updateRepo)
	assets := map[string]string{}
	for n := range r.Files {
		assets[n] = "mem://" + n
	}
	get := func(_ context.Context, u string, _ int64) ([]byte, error) {
		b, ok := r.Files[strings.TrimPrefix(u, "mem://")]
		if !ok {
			return nil, fmt.Errorf("404 %s", u)
		}
		return b, nil
	}
	l := layout.New(root)
	st, err := install.Stage(context.Background(), l, get, rv.Input{Tag: r.Tag, Identity: id, TrustedMaterial: trust}, assets, "")
	if err != nil {
		t.Fatalf("stage %s: %v", r.Tag, err)
	}
	if err := l.WritePointer(layout.CurrentFile, st.Version); err != nil {
		t.Fatal(err)
	}
	if err := layout.CopyFile(filepath.Join(st.Dir, exeName("maktaba-launcher")), l.LauncherPath()); err != nil {
		t.Fatal(err)
	}
}

// startLauncher runs a layout's launcher (the OS service's part).
func startLauncher(t *testing.T, name, root string, env []string) *machine {
	t.Helper()
	m := &machine{name: name, log: &lockedBuf{}}
	m.cmd = exec.Command(filepath.Join(root, exeName("maktaba-launcher")), "--root", root, "serve", "--", "--no-mdns")
	m.cmd.Env = append(os.Environ(), env...)
	m.cmd.Stdout, m.cmd.Stderr = m.log, m.log
	if err := m.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.stop()
		if t.Failed() {
			t.Logf("---- %s log (tail) ----\n%s", name, tail(m.log.String(), 12000))
			if dir := os.Getenv("MAKTABA_E2E_LOG_DIR"); dir != "" {
				_ = os.WriteFile(filepath.Join(dir, t.Name()+"-"+name+".log"), []byte(m.log.String()), 0o600)
			}
		}
	})
	return m
}

type updateRun struct {
	ID      string          `json:"id"`
	Version string          `json:"version"`
	State   string          `json:"state"`
	Error   json.RawMessage `json:"error"`
	Steps   []struct {
		Host, Step, State string
	} `json:"steps"`
	Targets []struct {
		Name      string `json:"name"`
		State     string `json:"state"`
		ToVersion string `json:"to_version"`
	} `json:"targets"`
	FinishedAt *time.Time `json:"finished_at"`
}

func TestAutoUpdateHomeAndNode(t *testing.T) {
	if os.Getenv("MAKTABA_E2E_UPDATE") != "1" || testing.Short() {
		t.Skip("set MAKTABA_E2E_UPDATE=1 and MAKTABA_TEST_PG_BIN to run the automatic update test")
	}
	pgBin := os.Getenv("MAKTABA_TEST_PG_BIN")
	if pgBin == "" {
		t.Skip("MAKTABA_TEST_PG_BIN unset (a Postgres 16 install)")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	platform := runtime.GOOS + "/" + runtime.GOARCH

	// ---- three releases, signed by the fake Sigstore ----
	ss := releaseverifytest.MustNew()
	trustJSON, err := ss.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	trustFile := filepath.Join(tmp, "trusted_root.json")
	_ = os.WriteFile(trustFile, trustJSON, 0o644)
	newRel := func(version string, mig map[string][]byte, schemaRev int) *releaseverifytest.FakeRelease {
		bins := buildRelease(t, filepath.Join(tmp, "build-"+version), version, schemaRev)
		rev := schemaRev
		if rev == 0 {
			rev = 149
		}
		r, err := ss.NewReleaseWith(updateRepo, "v"+version, platform, releaseverifytest.ReleaseOptions{Binaries: bins,
			Migrations: mig, MinUpgradeFrom: "0.9.0", SchemaRev: rev})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// 0.9.0 ships this tree's migrations; 0.9.1 adds slot 0150 (its API built to know it);
	// 0.9.2 adds slot 0151 too, past what its API knows: it refuses the database it migrated
	next := migrationFiles(t)
	next["0150_e2e_next.sql"] = []byte("-- +goose Up\nCREATE TABLE IF NOT EXISTS e2e_next (id integer);\n" +
		"-- +goose Down\nDROP TABLE IF EXISTS e2e_next;\n")
	broken := map[string][]byte{}
	for k, v := range next {
		broken[k] = v
	}
	broken["0151_e2e_probe.sql"] = []byte("-- +goose Up\nCREATE TABLE IF NOT EXISTS e2e_probe (id integer);\n" +
		"-- +goose Down\nDROP TABLE IF EXISTS e2e_probe;\n")
	r090 := newRel("0.9.0", migrationFiles(t), 0)
	r091 := newRel("0.9.1", next, 150)
	r092 := newRel("0.9.2", broken, 150)
	gh := newFakeGitHub(t)

	// ---- the two installs ----
	homeRoot, nodeRoot := filepath.Join(tmp, "home-install"), filepath.Join(tmp, "node-install")
	installRelease(t, homeRoot, r090, ss)
	installRelease(t, nodeRoot, r090, ss)
	homeDir, nodeDir := filepath.Join(tmp, "home"), filepath.Join(tmp, "node")
	_ = os.MkdirAll(homeDir, 0o700)
	_ = os.MkdirAll(nodeDir, 0o700)
	homePriv, homePub, _ := cluster.GenerateKey()
	_ = os.WriteFile(filepath.Join(homeDir, "cluster.key"), homePriv, 0o600)
	p := func() int { return freePort(t) }
	hAPI, hWeb, hAdmin, hPG, nAPI, nWeb, nAdmin := p(), p(), p(), p(), p(), p(), p()
	homeWeb := fmt.Sprintf("http://127.0.0.1:%d", hWeb)
	nodeWeb := fmt.Sprintf("http://localhost:%d", nWeb)
	writeToml := func(dir string, api, web, admin int, webURL, extra string) string {
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
[supervisor]
admin_addr = "127.0.0.1:%d"

[tv]
slots = "off"
`, api, filepath.ToSlash(filepath.Join(dir, "data")), web, webURL, admin, p(), p(), p(), p(), p(), extra, p())
		path := filepath.Join(dir, "server.toml")
		_ = os.WriteFile(path, []byte(body), 0o600)
		return path
	}
	homeToml := writeToml(homeDir, hAPI, hWeb, hAdmin, homeWeb, fmt.Sprintf(`role = "home"

[postgres]
enabled = "on"
home = %q
port = %d
listen = "loopback"`, filepath.ToSlash(pgBin), hPG))
	nodeToml := writeToml(nodeDir, nAPI, nWeb, nAdmin, nodeWeb, fmt.Sprintf("\n[database]\nurl = \"sqlite://unused.db\"\n[postgres]\ncopy_port = %d", p()))
	self, _ := os.Executable()
	common := []string{"MAKTABA_PYTHON=" + self, "MAKTABA_E2E_SLEEPER=1", "MAKTABA_DISCOVERY_MDNS=0", "MAKTABA_HSTS=0",
		"MAKTABA_HEALTH_WARM=0s", "MAKTABA_CLUSTER_DIR=" + filepath.Join(tmp, "clusterfiles"),
		"MAKTABA_ARCHIVE_DIR=" + filepath.Join(tmp, "archive"),
		"MAKTABA_E2E_TRUSTED_ROOT=" + trustFile, "MAKTABA_E2E_GITHUB_API=" + gh.srv.URL,
		"MAKTABA_PG_BIN=" + filepath.Join(pgBin, "bin"),
		// a short watch: the home's release must stay healthy 30 s, a node's 15 s
		"MAKTABA_UPDATE_WATCH_SECONDS=30", "MAKTABA_UPDATE_NODE_WATCH_SECONDS=15", "MAKTABA_UPDATE_HEALTH_SECONDS=120",
		"MAKTABA_UPDATE_INTERVAL_SEC=0", "MAKTABA_UPDATE_DISABLE=", "MAKTABA_MIGRATIONS_DIR="}
	home := startLauncher(t, "home", homeRoot, append(common, "MAKTABA_CONFIG="+homeToml,
		"MAKTABA_NODE_CONFIG="+filepath.Join(homeDir, "no-node.json"),
		"MAKTABA_HOME_CLUSTER_KEY="+filepath.Join(homeDir, "cluster.key"), "MAKTABA_HOME_AGENT_KEY="+filepath.Join(homeDir, "agent.key"),
		"MAKTABA_SECRETS_KEY="+filepath.Join(homeDir, "secrets.key"), "MAKTABA_DATA_DIR="+filepath.Join(homeDir, "data"),
		"HF_HOME="+filepath.Join(homeDir, "hf")))
	home.web = homeWeb
	selfVersion := func(web string) string {
		r, err := http.Get(web + "/api/cluster/self")
		if err != nil {
			return ""
		}
		defer r.Body.Close()
		var s struct {
			Version        string `json:"version"`
			AgentConnected bool   `json:"agent_connected"`
		}
		if r.StatusCode != 200 || json.NewDecoder(r.Body).Decode(&s) != nil {
			return ""
		}
		return s.Version
	}
	waitFor(t, "the home on 0.9.0 (its database made and migrated)", 4*time.Minute, func() bool {
		return selfVersion(homeWeb) == "0.9.0"
	})

	// ---- the owner, and the node joined ----
	readPW := func(role string) string {
		b, err := os.ReadFile(filepath.Join(homeDir, "postgres-"+role+".password"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	appDSN := (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba", readPW("maktaba")),
		Host: fmt.Sprintf("127.0.0.1:%d", hPG), Path: "/maktaba", RawQuery: "sslmode=disable"}).String()
	superDSN := (&url.URL{Scheme: "postgres", User: url.UserPassword("maktaba_admin", readPW("maktaba_admin")),
		Host: fmt.Sprintf("127.0.0.1:%d", hPG), Path: "/maktaba", RawQuery: "sslmode=disable"}).String()
	adb, err := sql.Open("postgres", superDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adb.Close() })
	pw := filepath.Join(tmp, "owner.pw")
	_ = os.WriteFile(pw, []byte("owner-pass-123\n"), 0o600)
	add := exec.Command(filepath.Join(homeRoot, "bin", "releases", "0.9.0", exeName("maktaba-api")), "adduser", "--password-file", pw, "owner")
	add.Env = append(os.Environ(), "DATABASE_URL="+appDSN)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("adduser: %v\n%s", err, out)
	}
	nodePriv, nodePub, _ := cluster.GenerateKey()
	nodeID := uuid.NewString()
	role := cluster.RoleName(nodeID)
	if err := (&cluster.PGRoles{DB: adb}).Create(ctx, role, "node-pass-0123456789"); err != nil {
		t.Fatal(err)
	}
	if _, err := adb.ExecContext(ctx, `INSERT INTO cluster_nodes (id, name, state, os, arch, node_pubkey, db_role,
	        approved_at, approved_by, full_node, web_url) VALUES ($1, 'studio-node', 'approved', $2, $3, $4, $5, now(), 'test', true, $6)`,
		nodeID, runtime.GOOS, runtime.GOARCH, cluster.EncodeKey(nodePub), role, nodeWeb); err != nil {
		t.Fatal(err)
	}
	nodeDSN := (&url.URL{Scheme: "postgres", User: url.UserPassword(role, "node-pass-0123456789"),
		Host: fmt.Sprintf("127.0.0.1:%d", hPG), Path: "/maktaba", RawQuery: "sslmode=prefer"}).String()
	nodeCfg, _ := json.Marshal(map[string]any{"node_id": nodeID, "name": "studio-node", "private_key": cluster.EncodeKey(nodePriv),
		"home": map[string]any{"url": fmt.Sprintf("http://127.0.0.1:%d", hAPI), "cluster_pub": cluster.EncodeKey(homePub)},
		"database_url": nodeDSN})
	_ = os.WriteFile(filepath.Join(nodeDir, "node.json"), nodeCfg, 0o600)
	node := startLauncher(t, "node", nodeRoot, append(common, "MAKTABA_CONFIG="+nodeToml,
		"MAKTABA_NODE_CONFIG="+filepath.Join(nodeDir, "node.json"), "MAKTABA_DATA_DIR="+filepath.Join(nodeDir, "data"),
		// its copy of the home's database (Epic 37) runs on the test's Postgres programs
		"MAKTABA_POSTGRES_HOME="+pgBin, "HF_HOME="+filepath.Join(nodeDir, "hf")))
	node.web = nodeWeb
	waitFor(t, "the node on 0.9.0 with its machine agent", 3*time.Minute, func() bool {
		r, err := http.Get(nodeWeb + "/api/cluster/self")
		if err != nil {
			return false
		}
		defer r.Body.Close()
		var s struct {
			Version        string `json:"version"`
			AgentConnected bool   `json:"agent_connected"`
		}
		return json.NewDecoder(r.Body).Decode(&s) == nil && s.Version == "0.9.0" && s.AgentConnected
	})
	// copyFollows waits for the node's copy to follow the home at schema rev, subscribed
	// after since (a copy made anew)
	type copyStatus struct {
		State       string     `json:"state"`
		Detail      string     `json:"detail"`
		LocalSchema int64      `json:"local_schema"`
		Subscribed  *time.Time `json:"subscribed"`
	}
	copyFollows := func(what string, rev int64, since time.Time) copyStatus {
		t.Helper()
		var c copyStatus
		waitFor(t, what, 5*time.Minute, func() bool {
			r, err := http.Get(nodeWeb + "/api/cluster/self")
			if err != nil {
				return false
			}
			defer r.Body.Close()
			var s struct {
				Copy *copyStatus `json:"copy"`
			}
			if json.NewDecoder(r.Body).Decode(&s) != nil || s.Copy == nil {
				return false
			}
			c = *s.Copy
			return c.State == "following" && c.LocalSchema == rev && c.Subscribed != nil && c.Subscribed.After(since)
		})
		return c
	}
	copyFollows("the node's copy following the home", 149, time.Time{})

	// ---- Update now, as the owner ----
	var admin *browser
	var csrf map[string]string
	signIn := func() {
		admin = newBrowser(t)
		waitFor(t, "a sign-in on the home", 3*time.Minute, func() bool {
			code, _ := admin.try("POST", homeWeb+"/api/auth/login", map[string]string{"username": "owner", "password": "owner-pass-123"}, nil)
			return code == 200
		})
		csrf = map[string]string{"X-Maktaba-CSRF": admin.cookie(homeWeb, "mkt_csrf")}
	}
	signIn()
	updateNow := func(version string) string {
		t.Helper()
		resp, body := admin.do("POST", homeWeb+"/api/cluster/updates/check", map[string]any{}, csrf)
		if resp.StatusCode != 200 {
			t.Fatalf("check: %d %s", resp.StatusCode, body)
		}
		// the home must be healthy first (every role running, no crash for a while: the
		// scheduler's blockers, which Update now keeps)
		deadline := time.Now().Add(4 * time.Minute)
		for {
			resp, body = admin.do("POST", homeWeb+"/api/cluster/updates/run", map[string]any{"confirm": true, "version": version}, csrf)
			if resp.StatusCode == http.StatusAccepted {
				break
			}
			if resp.StatusCode != http.StatusConflict || time.Now().After(deadline) ||
				!(strings.Contains(string(body), "role_not_running") || strings.Contains(string(body), "recent_crash")) {
				_, procs := admin.do("GET", homeWeb+"/api/system/processes", nil, nil)
				t.Fatalf("update now %s: %d %s\nprocesses: %s", version, resp.StatusCode, body, tail(string(procs), 4000))
			}
			time.Sleep(3 * time.Second)
		}
		var out struct {
			RunID string `json:"run_id"`
		}
		_ = json.Unmarshal(body, &out)
		return out.RunID
	}
	// follow reads the run until it ends (the home restarts meanwhile: its web port and
	// the session come and go)
	follow := func(id string, d time.Duration) updateRun {
		t.Helper()
		var run updateRun
		waitFor(t, "the update run "+id+" to end", d, func() bool {
			code, body := admin.try("GET", homeWeb+"/api/cluster/updates/runs/"+id, nil, nil)
			if code == http.StatusUnauthorized {
				signIn()
				return false
			}
			if code != 200 {
				return false
			}
			run = updateRun{}
			_ = json.Unmarshal(body, &run)
			return run.FinishedAt != nil
		})
		return run
	}

	gh.publish(r091)
	run := follow(updateNow("0.9.1"), 15*time.Minute)
	if run.State != "done" {
		t.Fatalf("the update to 0.9.1 ended %s: %s (steps %+v, targets %+v)", run.State, run.Error, run.Steps, run.Targets)
	}
	if len(run.Targets) != 1 || run.Targets[0].State != "healthy" {
		t.Fatalf("targets %+v", run.Targets)
	}
	rehearsed := false
	for _, s := range run.Steps {
		if s.Host == "home" && s.Step == "rehearse" && s.State == "done" {
			rehearsed = true
		}
	}
	if !rehearsed {
		t.Fatalf("the home's migration was not rehearsed: %+v", run.Steps)
	}
	if v := selfVersion(homeWeb); v != "0.9.1" {
		t.Fatalf("the home answers as %q", v)
	}
	if v := selfVersion(nodeWeb); v != "0.9.1" {
		t.Fatalf("the node answers as %q", v)
	}
	for name, root := range map[string]string{"home": homeRoot, "node": nodeRoot} {
		if cur, _ := layout.New(root).Current(); cur != "0.9.1" {
			t.Fatalf("the %s's current release is %q", name, cur)
		}
		if p, _ := layout.New(root).ReadPending(); p != nil {
			t.Fatalf("the %s's release is still pending: %+v", name, p)
		}
	}
	var applied int64
	if err := adb.QueryRowContext(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&applied); err != nil || applied != 150 {
		t.Fatalf("the home's schema is at %d (%v), want 150", applied, err)
	}
	if _, err := os.Stat(filepath.Join(homeDir, "data", "backups", "pre-0.9.0-to-0.9.1.dump")); err != nil {
		t.Fatalf("no pre-update dump: %v", err)
	}
	// the node's copy waited for the node's update, then copied the migrated home anew
	copyFollows("the node's copy following the migrated home", 150, time.Time{})

	// ---- a release that cannot run on what it migrated: rolled back on the home alone ----
	beforeRollback := time.Now()
	gh.publish(r092)
	run = follow(updateNow("0.9.2"), 15*time.Minute)
	if run.State != "rolled_back" || len(run.Targets) != 0 {
		t.Fatalf("the update to 0.9.2 ended %s: %s (targets %+v)", run.State, run.Error, run.Targets)
	}
	waitFor(t, "the home on 0.9.1 again", 3*time.Minute, func() bool { return selfVersion(homeWeb) == "0.9.1" })
	if cur, _ := layout.New(homeRoot).Current(); cur != "0.9.1" {
		t.Fatalf("the home's current release is %q", cur)
	}
	if v := selfVersion(nodeWeb); v != "0.9.1" {
		t.Fatalf("the node answers as %q after the home's rollback", v)
	}
	var probe, nextTable sql.NullString
	_ = adb.QueryRowContext(ctx, `SELECT to_regclass('e2e_probe')::text, to_regclass('e2e_next')::text`).Scan(&probe, &nextTable)
	if probe.Valid || !nextTable.Valid {
		t.Fatalf("the dump taken before 0.9.2's migration was not put back: e2e_probe %v, e2e_next %v", probe, nextTable)
	}
	// the dump put back took the node's slot with it: its copy is made anew from it
	copyFollows("the node's copy made anew from the database put back", 150, beforeRollback)
	resp, body := admin.do("GET", homeWeb+"/api/cluster/updates", nil, nil)
	if resp.StatusCode == 200 && !strings.Contains(string(body), `"blocked_reason":"rolled_back"`) {
		t.Fatalf("0.9.2 is not blocked: %s", tail(string(body), 2000))
	}
}
