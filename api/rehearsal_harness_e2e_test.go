//go:build unix

package main

// The update rehearsal's harness (docs/update-rehearsal.md, the CI job update-rehearsal):
// the owner's Maktaba.app reproduced on a scratch machine. Every update path the owner's
// Mac runs is driven here through the same code: the app bundle's layout
// (<home>/Applications/Maktaba.app/Contents/Resources/server: bin/, postgres/, migrations/,
// payload.json), the launcher run from that bin/ (it finds the bundle by its path, on
// any OS), its state root (~/Library/Application Support/Maktaba under the machine's own
// HOME: the launcher's guard, payload-override, the kept payloads, the database record
// state/database.json, the app's order app-install.json, updates/restart), the bundled
// Postgres (a release's own Linux payload's postgres/, with pgvector), and the desktop
// app's part of an update played by the harness (it stages what the home orders, and at
// the home's go stops the server, swaps the bundle and starts it again, as the app does).
//
// Two releases' real binaries (tools/rehearsal/build.sh): the previous published
// release, built from its tag, and this commit; signed by a local fake Sigstore and
// published on a fake GitHub, as the automatic update e2e does (-tags e2etrust).
//
// Nothing here reads or writes this machine's own installs: every process gets its own
// HOME under the rehearsal's work folder, its own free ports, and a PATH whose open and
// xdg-open do nothing (an update's nudge never starts a real app).

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

// ---- the kit ----

// rehearsalKit is what tools/rehearsal/build.sh made, and the bundled Postgres.
type rehearsalKit struct {
	dir    string // prev/, cur/, next/, versions.env
	pgHome string // a Postgres 16 install with pgvector: bin/postgres
	// the versions: the previous release, the newer one the live Mac's state rolled back
	// from, this commit, and this commit as the release after it
	prev, failed, cur, next string
	rev                     int // this commit's newest migration slot
}

const (
	envRehearsal = "MAKTABA_E2E_REHEARSAL"
	ownerUser    = "owner"
	ownerPass    = "owner-pass-123"
)

// rehearsalOn skips the test unless the rehearsal is asked for.
func rehearsalOn(t *testing.T) {
	t.Helper()
	if os.Getenv(envRehearsal) != "1" || testing.Short() {
		t.Skip("set MAKTABA_E2E_REHEARSAL=1, MAKTABA_REHEARSAL_KIT and MAKTABA_REHEARSAL_PG_HOME to run the update rehearsal " +
			"(docs/update-rehearsal.md)")
	}
}

func loadKit(t *testing.T) *rehearsalKit {
	t.Helper()
	rehearsalOn(t)
	k := &rehearsalKit{dir: os.Getenv("MAKTABA_REHEARSAL_KIT"), pgHome: os.Getenv("MAKTABA_REHEARSAL_PG_HOME")}
	if k.dir == "" || k.pgHome == "" {
		t.Fatal("MAKTABA_REHEARSAL_KIT (tools/rehearsal/build.sh's --out) and MAKTABA_REHEARSAL_PG_HOME (a Postgres 16 with " +
			"pgvector: tools/rehearsal/postgres.sh) are needed")
	}
	if _, err := os.Stat(filepath.Join(k.pgHome, "bin", "postgres")); err != nil {
		t.Fatalf("MAKTABA_REHEARSAL_PG_HOME: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(k.dir, "versions.env"))
	if err != nil {
		t.Fatalf("the kit: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, val, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "PREV":
			k.prev = val
		case "FAILED":
			k.failed = val
		case "CUR":
			k.cur = val
		case "NEXT":
			k.next = val
		case "REV":
			k.rev, _ = strconv.Atoi(val)
		}
	}
	if k.prev == "" || k.cur == "" || k.next == "" || k.rev == 0 {
		t.Fatalf("the kit's versions.env is incomplete: %s", b)
	}
	return k
}

// ---- the test-only migrations ----

// The rehearsal's own migrations, on top of this commit's (its API is built to take slot
// rev+1 for its own; a payload that ships rev+2 is a release whose API refuses the
// database it migrated: the health failure injected after the migrations).
func additiveMigration(slot int) (string, string) {
	return fmt.Sprintf("%04d_rehearsal_additive.sql", slot), `-- +goose Up
-- The update rehearsal's additive migration (a release with migrations; rollback-safe).
CREATE TABLE IF NOT EXISTS rehearsal_probe (id bigserial PRIMARY KEY, note text NOT NULL DEFAULT '');
INSERT INTO rehearsal_probe (note) VALUES ('added by the rehearsal');

-- +goose Down
DROP TABLE IF EXISTS rehearsal_probe;
`
}

func additiveNextMigration(slot int) (string, string) {
	return fmt.Sprintf("%04d_rehearsal_next.sql", slot), `-- +goose Up
-- The update rehearsal's second additive migration: past what the release's API takes for
-- its own, so the release cannot run on what it migrated (the injected health failure).
CREATE TABLE IF NOT EXISTS rehearsal_probe_next (id int);

-- +goose Down
DROP TABLE IF EXISTS rehearsal_probe_next;
`
}

func destructiveMigration(slot int) (string, string) {
	return fmt.Sprintf("%04d_rehearsal_destructive.sql", slot), `-- +goose Up
-- The update rehearsal's destructive migration: the vectors gone, an index and a column
-- dropped. Only the dump put back brings them back.
DROP INDEX IF EXISTS segment_embeddings_video;
TRUNCATE segment_embeddings;
ALTER TABLE collections DROP COLUMN IF EXISTS description;

-- +goose Down
SELECT 1;
`
}

// ---- payloads ----

// payloadSpec is one app payload: a variant of the kit's binaries, the version
// payload.json says, and the migrations it ships beside the kit's.
type payloadSpec struct {
	variant string // prev, cur, next
	version string
	extra   map[string]string // more migrations (name -> SQL)
}

// makePayload writes the app's server payload into dst: bin/ (the kit's binaries),
// postgres/ (the bundled Postgres), migrations/, payload.json.
func (k *rehearsalKit) makePayload(t *testing.T, dst string, p payloadSpec) {
	t.Helper()
	src := filepath.Join(k.dir, p.variant)
	if err := os.MkdirAll(filepath.Join(dst, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming", "maktaba-launcher"} {
		if err := copyFileMode(filepath.Join(src, b), filepath.Join(dst, "bin", b), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyDir(filepath.Join(src, "migrations"), filepath.Join(dst, "migrations")); err != nil {
		t.Fatal(err)
	}
	for name, body := range p.extra {
		if err := os.WriteFile(filepath.Join(dst, "migrations", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// the bundled Postgres: one install for every payload (a link the kept copies keep)
	if err := os.Symlink(k.pgHome, filepath.Join(dst, "postgres")); err != nil {
		t.Fatal(err)
	}
	pgVersion := "16.0.0"
	if out, err := exec.Command(filepath.Join(k.pgHome, "bin", "postgres"), "--version").Output(); err == nil {
		if m := regexp.MustCompile(`(\d+)\.(\d+)`).FindStringSubmatch(string(out)); m != nil {
			pgVersion = m[1] + "." + m[2] + ".0"
		}
	}
	doc := map[string]any{"schema": 1, "version": p.version, "platform": feedPlatformKey(),
		"postgres": map[string]any{"version": pgVersion}, "python": map[string]any{"version": "3.12"},
		"pipeline": map[string]any{"wheel": "maktaba_pipeline-" + p.version + "-py3-none-any.whl"}}
	raw, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(filepath.Join(dst, "payload.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newestSlot is the newest migration slot of a migrations folder.
func newestSlot(dir string) int {
	ents, _ := os.ReadDir(dir)
	best := 0
	for _, e := range ents {
		if len(e.Name()) > 5 && e.Name()[4] == '_' {
			if n, err := strconv.Atoi(e.Name()[:4]); err == nil && n > best {
				best = n
			}
		}
	}
	return best
}

// feedPlatformKey is the Tauri updater's platform of this machine (latest.json).
func feedPlatformKey() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	}
	return runtime.GOOS + "-" + arch
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// copyDir copies a folder (files with their modes, links as links).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFileMode(p, target, info.Mode().Perm())
		}
	})
}

// cloneTree copies a whole folder fast (a copy-on-write clone where the file system
// has one), keeping modes and links.
func cloneTree(t *testing.T, src, dst string) {
	t.Helper()
	_ = os.RemoveAll(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("cp", "-c", "-R", "-p", src, dst)
	} else {
		cmd = exec.Command("cp", "-a", "--reflink=auto", src, dst)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		if err := copyDir(src, dst); err != nil {
			t.Fatalf("copy %s: %v (%s)", src, err, out)
		}
	}
}

// ---- the rehearsal: fake GitHub, fake Sigstore, the releases ----

type rehearsal struct {
	t      *testing.T
	kit    *rehearsalKit
	work   string // this test's folder
	logDir string
	ss     *releaseverifytest.Sigstore
	trust  string // the fake Sigstore's trusted_root.json
	gh     *fakeGitHub
	python string // the pipeline's interpreter (a venv of the e2e wheel)
	shim   string // a PATH folder whose open and xdg-open do nothing
	start  time.Time
}

// newRehearsal sets up a rehearsal: its folder, the fake GitHub and Sigstore, the
// pipeline's stand-in environment.
func newRehearsal(t *testing.T) *rehearsal {
	t.Helper()
	k := loadKit(t)
	work := os.Getenv("MAKTABA_REHEARSAL_WORK")
	if work == "" {
		work = t.TempDir()
	} else {
		work = filepath.Join(work, strings.ReplaceAll(t.Name(), "/", "_"))
		_ = os.RemoveAll(work)
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	logDir := os.Getenv("MAKTABA_E2E_LOG_DIR")
	if logDir == "" {
		logDir = filepath.Join(work, "logs")
	}
	_ = os.MkdirAll(logDir, 0o755)
	r := &rehearsal{t: t, kit: k, work: work, logDir: logDir, start: time.Now()}
	r.ss = releaseverifytest.MustNew()
	trustJSON, err := r.ss.TrustedRootJSON()
	if err != nil {
		t.Fatal(err)
	}
	r.trust = filepath.Join(work, "trusted_root.json")
	_ = os.WriteFile(r.trust, trustJSON, 0o644)
	r.gh = newFakeGitHub(t)
	// the pipeline: the e2e's wheel in a venv of this machine's Python (it answers its
	// probes; the transcription and the TV's staging jobs never run)
	py := pipelinePython(t)
	venv := filepath.Join(work, "pipeline-venv")
	makeVenv(t, py, fakeWheel(t, k.cur), venv)
	r.python = filepath.Join(venv, "bin", "python")
	r.shim = filepath.Join(work, "shim")
	_ = os.MkdirAll(r.shim, 0o755)
	for _, n := range []string{"open", "xdg-open"} {
		_ = os.WriteFile(filepath.Join(r.shim, n), []byte("#!/bin/sh\necho \"rehearsal: $0 $*\" >&2\nexit 0\n"), 0o755)
	}
	r.logf("rehearsal: previous %s, failed %s, this commit %s (next %s), schema %d; work %s",
		k.prev, k.failed, k.cur, k.next, k.rev, work)
	return r
}

func (r *rehearsal) logf(format string, args ...any) {
	r.t.Helper()
	r.t.Logf("[%6.1fs] "+format, append([]any{time.Since(r.start).Seconds()}, args...)...)
}

// publish signs and publishes a release of the payload in dir (its version, binaries and
// migrations), with the app's updater feed (latest.json) naming this platform.
func (r *rehearsal) publish(version, dir string) *releaseverifytest.FakeRelease {
	t := r.t
	t.Helper()
	bins := map[string][]byte{}
	for _, b := range []string{"maktaba-server", "maktaba-api", "maktaba-streaming", "maktaba-launcher"} {
		raw, err := os.ReadFile(filepath.Join(dir, "bin", b))
		if err != nil {
			t.Fatal(err)
		}
		bins[b] = raw
	}
	mig := map[string][]byte{}
	ents, _ := os.ReadDir(filepath.Join(dir, "migrations"))
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			raw, _ := os.ReadFile(filepath.Join(dir, "migrations", e.Name()))
			mig[e.Name()] = raw
		}
	}
	tag := "v" + version
	rel, err := r.ss.NewReleaseWith(updateRepo, tag, runtime.GOOS+"/"+runtime.GOARCH, releaseverifytest.ReleaseOptions{
		Binaries: bins, Migrations: mig, MinUpgradeFrom: r.kit.prev, SchemaRev: newestSlot(filepath.Join(dir, "migrations")),
		Wheel: fakeWheel(t, version)})
	if err != nil {
		t.Fatal(err)
	}
	// the app's update (the harness's app installs the payload it was given for it) and
	// the feed that names it, files of this very release
	appFile := fmt.Sprintf("Maktaba_%s_%s.app.tar.gz", version, feedPlatformKey())
	rel.Files[appFile] = []byte("the rehearsal's app update " + version)
	base := r.gh.srv.URL + "/" + updateRepo + "/releases/download/" + tag + "/"
	feed, _ := json.Marshal(map[string]any{"version": version, "notes": "rehearsal", "pub_date": time.Now().UTC().Format(time.RFC3339),
		"platforms": map[string]any{feedPlatformKey(): map[string]any{"url": base + appFile, "signature": "rehearsal"}}})
	rel.Files["latest.json"] = feed
	if err := rel.Rebuild(r.ss, releaseverifytest.GitHubRelease(updateRepo, tag)); err != nil {
		t.Fatal(err)
	}
	r.gh.publish(rel)
	r.logf("published %s on the fake GitHub", tag)
	return rel
}

// ---- an app-bundle machine ----

// appMachine is one Mac app install: its HOME, the app with its payload, the state root,
// the launcher run from the payload, and the harness's part of the desktop app.
type appMachine struct {
	r       *rehearsal
	name    string
	dir     string // everything of this machine
	home    string // its HOME
	app     string // …/Maktaba.app
	payload string // …/Maktaba.app/Contents/Resources/server
	root    string // its state root (~/Library/Application Support/Maktaba): the launcher's root, data_dir

	api, web, admin, pg, sup int
	// server.toml's lines of this machine's own: in [server], in [postgres], and more
	// sections after
	serverExtra, pgExtra, tomlTail string
	// tvSlots is [tv] slots ("off" when unset: the TV slots listen on fixed ports, one
	// machine of a host only)
	tvSlots string
	env     []string

	mu       sync.Mutex
	cmd      *exec.Cmd
	exited   chan struct{}
	logFile  *os.File
	log      *lockedBuf
	appState appSide
	stopApp  chan struct{}
}

// appSide is what the harness's desktop app does and saw.
type appSide struct {
	available map[string]string // version -> the payload its update installs
	orders    []orderSeen
	refuse    bool // answer every order failed (nothing installed)
}

// orderSeen is one state of the app's order (app-install.json) the app saw or wrote.
type orderSeen struct {
	At      time.Time
	RunID   string
	Version string
	Kind    string
	State   string
}

// machinePaths fills a machine's paths under dir.
func (r *rehearsal) machinePaths(name, dir string) *appMachine {
	m := &appMachine{r: r, name: name, dir: dir, home: filepath.Join(dir, "home"), log: &lockedBuf{}}
	m.app = filepath.Join(m.home, "Applications", "Maktaba.app")
	m.payload = filepath.Join(m.app, "Contents", "Resources", "server")
	m.root = filepath.Join(m.home, "Library", "Application Support", "Maktaba")
	m.appState.available = map[string]string{}
	return m
}

// newAppMachine installs the payload of p as a fresh app (no database yet).
func (r *rehearsal) newAppMachine(name string, p payloadSpec) *appMachine {
	m := r.machinePaths(name, filepath.Join(r.work, name))
	_ = os.RemoveAll(m.dir)
	if err := os.MkdirAll(filepath.Join(m.app, "Contents", "MacOS"), 0o755); err != nil {
		r.t.Fatal(err)
	}
	// the shell's executable: the app is where it was (the update's preflight looks)
	_ = os.WriteFile(filepath.Join(m.app, "Contents", "MacOS", "Maktaba"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	r.kit.makePayload(r.t, m.payload, p)
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		r.t.Fatal(err)
	}
	return m
}

// machineFrom takes a machine prepared elsewhere (a seed) as its own: its whole folder
// copied, and its app-side state set up afresh.
func (r *rehearsal) machineFrom(name, seedDir string) *appMachine {
	m := r.machinePaths(name, filepath.Join(r.work, name))
	start := time.Now()
	cloneTree(r.t, seedDir, m.dir)
	r.logf("%s: the seeded install copied from %s in %s", name, seedDir, time.Since(start).Round(time.Second))
	return m
}

// ports picks the machine's ports (fresh for each start of the test: a seed's ports are
// another run's).
func (m *appMachine) ports() {
	if m.api != 0 {
		return
	}
	t := m.r.t
	m.api, m.web, m.admin, m.pg, m.sup = freePort(t), freePort(t), freePort(t), freePort(t), freePort(t)
}

func (m *appMachine) webURL() string { return fmt.Sprintf("http://127.0.0.1:%d", m.web) }
func (m *appMachine) apiURL() string { return fmt.Sprintf("http://127.0.0.1:%d", m.api) }

// writeToml writes server.toml in the state root (MAKTABA_CONFIG: the keys and the
// database's password files are beside it).
func (m *appMachine) writeToml() string {
	t := m.r.t
	m.ports()
	p := func() int { return freePort(t) }
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
pipeline_search_addr = "127.0.0.1:%d"
pipeline_index_logs_addr = "127.0.0.1:%d"
%s

[postgres]
port = %d
listen = "loopback"
%s

[supervisor]
admin_addr = "127.0.0.1:%d"

[tv]
slots = "%s"
%s
`, m.api, m.root, m.web, m.webURL(), m.admin, p(), p(), p(), p(), p(), p(), p(), m.serverExtra, m.pg, m.pgExtra, m.sup, orStr(m.tvSlots, "off"), m.tomlTail)
	path := filepath.Join(m.root, "server.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// environ is the launcher's environment: the machine's own HOME and folders, the fake
// GitHub and trust root, short watches, no discovery.
func (m *appMachine) environ() []string {
	r := m.r
	keep := []string{}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "MAKTABA_"), k == "HOME", k == "PATH", k == "XDG_DATA_HOME", k == "XDG_CONFIG_HOME",
			k == "XDG_STATE_HOME", k == "XDG_CACHE_HOME", k == "XDG_RUNTIME_DIR", k == "PGPASSWORD", k == "DATABASE_URL":
		default:
			keep = append(keep, kv)
		}
	}
	env := append(keep,
		"HOME="+m.home, "PATH="+r.shim+":"+os.Getenv("PATH"),
		"XDG_DATA_HOME="+filepath.Join(m.home, ".local", "share"), "XDG_CONFIG_HOME="+filepath.Join(m.home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(m.home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(m.home, ".cache"),
		"MAKTABA_CONFIG="+filepath.Join(m.root, "server.toml"),
		"MAKTABA_LOG_DIR="+filepath.Join(m.root, "logs"),
		"MAKTABA_NODE_CONFIG="+filepath.Join(m.root, "node.json"),
		"MAKTABA_HOME_CLUSTER_KEY="+filepath.Join(m.root, "cluster.key"),
		"MAKTABA_HOME_AGENT_KEY="+filepath.Join(m.root, "agent.key"),
		"MAKTABA_SECRETS_KEY="+filepath.Join(m.root, "secrets.key"),
		"MAKTABA_CLUSTER_DIR="+filepath.Join(m.root, "clusterfiles"),
		"MAKTABA_ARCHIVE_DIR="+filepath.Join(m.root, "archive"),
		"HF_HOME="+filepath.Join(m.root, "hf"),
		"MAKTABA_PYTHON="+r.python, "MAKTABA_PIPELINE_PYTHON="+r.python,
		"MAKTABA_DISCOVERY_MDNS=0", "MAKTABA_HSTS=0", "MAKTABA_HEALTH_WARM=0s",
		"MAKTABA_E2E_TRUSTED_ROOT="+r.trust, "MAKTABA_E2E_GITHUB_API="+r.gh.srv.URL,
		// a short watch, as the update e2e has: healthy 30 s commits, 90 s unhealthy rolls back
		"MAKTABA_UPDATE_WATCH_SECONDS=30", "MAKTABA_UPDATE_NODE_WATCH_SECONDS=15", "MAKTABA_UPDATE_HEALTH_SECONDS=90",
		"MAKTABA_UPDATE_INTERVAL_SEC=0", "MAKTABA_UPDATE_DISABLE=", "MAKTABA_MIGRATIONS_DIR=",
		"MAKTABA_TRANSCODE_MAX_CONCURRENT=2")
	return append(env, m.env...)
}

// start runs the launcher from the payload, as the app's login agent does
// (`maktaba-launcher serve`), with the harness's desktop app beside it.
func (m *appMachine) start() {
	t := m.r.t
	t.Helper()
	m.writeToml()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil {
		t.Fatalf("%s runs already", m.name)
	}
	if m.logFile == nil {
		f, err := os.OpenFile(filepath.Join(m.r.logDir, strings.ReplaceAll(t.Name(), "/", "_")+"-"+m.name+".log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		m.logFile = f
		t.Cleanup(func() { m.cleanup() })
	}
	fmt.Fprintf(m.logFile, "\n==== rehearsal: %s starts the launcher (%s) ====\n", time.Now().UTC().Format(time.RFC3339), m.payload)
	cmd := exec.Command(filepath.Join(m.payload, "bin", "maktaba-launcher"), "serve", "--", "--no-mdns")
	cmd.Dir = m.home
	cmd.Env = m.environ()
	w := io.MultiWriter(m.logFile, m.log)
	cmd.Stdout, cmd.Stderr = w, w
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	m.cmd, m.exited = cmd, done
	if m.stopApp == nil {
		m.stopApp = make(chan struct{})
		go m.runApp(m.stopApp)
	}
}

// stop stops the launcher as the app does before it swaps the bundle (and at a quit):
// SIGTERM, the launcher stops the server; killed after 2 minutes.
func (m *appMachine) stop() {
	m.mu.Lock()
	cmd, done := m.cmd, m.exited
	m.cmd = nil
	m.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
	m.killLeftovers()
}

// killLeftovers ends any process still running from this machine's folders (a server or
// Postgres a launcher killed hard left behind).
func (m *appMachine) killLeftovers() {
	_ = exec.Command("pkill", "-9", "-f", m.dir).Run()
}

func (m *appMachine) cleanup() {
	if m.stopApp != nil {
		close(m.stopApp)
		m.stopApp = nil
	}
	m.stop()
	m.killLeftovers()
	if m.logFile != nil {
		_ = m.logFile.Close()
	}
	if m.r.t.Failed() {
		m.r.t.Logf("---- %s log (tail; the whole log is in %s) ----\n%s", m.name, m.r.logDir, tail(m.log.String(), 15000))
	}
}

// ---- the harness's desktop app ----

// appRequestPath is the app's order (updater.AppRequestFile).
func (m *appMachine) appRequestPath() string {
	return filepath.Join(m.root, "updates", "app-install.json")
}

// offer makes the payload of p the app update the app installs for its version.
func (m *appMachine) offer(p payloadSpec) string {
	dir := filepath.Join(m.dir, "app-updates", p.version)
	_ = os.RemoveAll(dir)
	m.r.kit.makePayload(m.r.t, dir, p)
	m.mu.Lock()
	m.appState.available[p.version] = dir
	m.mu.Unlock()
	return dir
}

// orders is what the app saw of its orders.
func (m *appMachine) orders() []orderSeen {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]orderSeen(nil), m.appState.orders...)
}

// runApp plays the desktop app's side of an update (apps/desktop/src-tauri/src/
// autoinstall.rs, shell.rs install_update_from): an order is staged (the app's update for
// that exact version is there) and answered "staged"; at the home's go it says
// "installing", stops the server, replaces the app's payload with the new release's and
// starts the server again. A release it holds already (kind server) is the server's own
// switch. An order it has no update for fails ("no_app_update").
func (m *appMachine) runApp(stop chan struct{}) {
	last := ""
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		raw, err := os.ReadFile(m.appRequestPath())
		if err != nil {
			continue
		}
		var q map[string]any
		if json.Unmarshal(raw, &q) != nil {
			continue
		}
		str := func(k string) string { s, _ := q[k].(string); return s }
		seen := orderSeen{At: time.Now(), RunID: str("run_id"), Version: str("version"), Kind: str("kind"), State: str("state")}
		key := seen.RunID + "/" + seen.State
		if key == last {
			continue
		}
		last = key
		m.mu.Lock()
		m.appState.orders = append(m.appState.orders, seen)
		dir, ok := m.appState.available[seen.Version]
		refuse := m.appState.refuse
		m.mu.Unlock()
		fmt.Fprintf(m.logFile, "[rehearsal app] order %s for %s (%s): %s\n", seen.RunID, seen.Version, seen.Kind, seen.State)
		switch {
		case seen.State == "ordered" && (refuse || (seen.Kind == "app" && !ok)):
			m.answer(q, "failed", "no_app_update: the rehearsal's app has no update for "+seen.Version)
		case seen.State == "ordered":
			m.answer(q, "staged", "")
		case seen.State == "go" && seen.Kind == "app":
			m.answer(q, "installing", "")
			m.install(dir)
		}
	}
}

// answer writes the app's answer into the order, whole (as write_state does).
func (m *appMachine) answer(q map[string]any, state, errText string) {
	q["state"] = state
	q["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if errText != "" {
		q["error"] = errText
	}
	raw, _ := json.MarshalIndent(q, "", "  ")
	tmp := m.appRequestPath() + ".app-tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, m.appRequestPath())
	}
	m.mu.Lock()
	m.appState.orders = append(m.appState.orders, orderSeen{At: time.Now(), RunID: fmt.Sprint(q["run_id"]),
		Version: fmt.Sprint(q["version"]), Kind: fmt.Sprint(q["kind"]), State: state})
	m.mu.Unlock()
}

// install is the app's install of its update: the server stopped, the bundle's payload
// replaced, the server started again.
func (m *appMachine) install(dir string) {
	fmt.Fprintf(m.logFile, "[rehearsal app] installing %s: the server stops, the app's payload is replaced\n", dir)
	m.stop()
	old := m.payload + ".old"
	_ = os.RemoveAll(old)
	if err := os.Rename(m.payload, old); err != nil {
		fmt.Fprintf(m.logFile, "[rehearsal app] the swap failed: %v\n", err)
	} else if err := copyDir(dir, m.payload); err != nil {
		fmt.Fprintf(m.logFile, "[rehearsal app] the swap failed: %v\n", err)
		_ = os.RemoveAll(m.payload)
		_ = os.Rename(old, m.payload)
	} else {
		_ = os.RemoveAll(old)
	}
	m.start()
}

// installByHand replaces the app's payload as an install by other means would (the app's
// own "Install update", a dmg dragged over): stop, swap, start.
func (m *appMachine) installByHand(p payloadSpec) {
	m.r.logf("%s: installing %s by hand", m.name, p.version)
	dir := m.offer(p)
	m.install(dir)
}

// ---- reading the machine ----

func (m *appMachine) readFile(rel string) string {
	b, _ := os.ReadFile(filepath.Join(m.root, rel))
	return string(b)
}

// dbRecord is the database's record (state/database.json).
type dbRecord struct {
	Version string `json:"version"`
	State   string `json:"state"`
	Release string `json:"release"`
	From    string `json:"from"`
	Dump    string `json:"dump"`
	Failed  string `json:"failed"`
	Error   string `json:"error"`
	By      string `json:"by"`
	KeptFor string `json:"kept_for"`
}

func (m *appMachine) record() (dbRecord, map[string]any) {
	var rec dbRecord
	var all map[string]any
	raw, err := os.ReadFile(filepath.Join(m.root, "state", "database.json"))
	if err == nil {
		_ = json.Unmarshal(raw, &rec)
		_ = json.Unmarshal(raw, &all)
	}
	return rec, all
}

// journal is the machine's update journal (updates/journal.json): its state, step,
// error and TV step.
type journalView struct {
	State string `json:"state"`
	Step  string `json:"step"`
	From  string `json:"from"`
	Job   struct {
		RunID   string `json:"run_id"`
		Version string `json:"version"`
	} `json:"job"`
	Error *struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
	TV *struct {
		Outcome  string `json:"outcome"`
		ChangeID string `json:"change_id"`
	} `json:"tv"`
}

func (m *appMachine) journal() journalView {
	var j journalView
	_ = json.Unmarshal([]byte(m.readFile(filepath.Join("updates", "journal.json"))), &j)
	return j
}

// selfVersion is the version the machine's web answers with ("" when it does not).
func (m *appMachine) selfVersion() string {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(m.webURL() + "/api/cluster/self")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var s struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&s) != nil {
		return ""
	}
	return strings.TrimPrefix(s.Version, "v")
}

// password reads a role's password file (beside server.toml).
func (m *appMachine) password(role string) string {
	b, err := os.ReadFile(filepath.Join(m.root, "postgres-"+role+".password"))
	if err != nil {
		m.r.t.Fatalf("%s: %v", m.name, err)
	}
	return strings.TrimSpace(string(b))
}

func (m *appMachine) dsn(role, db string) string {
	return (&url.URL{Scheme: "postgres", User: url.UserPassword(role, m.password(role)),
		Host: fmt.Sprintf("127.0.0.1:%d", m.pg), Path: "/" + db, RawQuery: "sslmode=disable"}).String()
}

// superDB is the bundled Postgres's superuser on the library database (closed with the test).
func (m *appMachine) superDB(db string) *sql.DB {
	t := m.r.t
	d, err := sql.Open("postgres", m.dsn("maktaba_admin", db))
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// waitServing waits until the machine answers with version on its web, with its API ready.
func (m *appMachine) waitServing(version string, d time.Duration) {
	m.r.t.Helper()
	waitFor(m.r.t, m.name+" serving "+version, d, func() bool { return m.selfVersion() == version && m.ready() })
}

// ready: the API's readiness (its database), on its admin port.
func (m *appMachine) ready() bool {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", m.admin))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// addOwner makes the first admin with the payload's own maktaba-api.
func (m *appMachine) addOwner() {
	t := m.r.t
	pw := filepath.Join(m.dir, "owner.pw")
	_ = os.WriteFile(pw, []byte(ownerPass+"\n"), 0o600)
	add := exec.Command(filepath.Join(m.payload, "bin", "maktaba-api"), "adduser", "--password-file", pw, ownerUser)
	add.Env = append(m.environ(), "DATABASE_URL="+m.dsn("maktaba", "maktaba"))
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("adduser: %v\n%s", err, out)
	}
}

// ---- the admin ----

// admin is the owner on the home's web: the update's buttons.
type ownerSession struct {
	m    *appMachine
	b    *browser
	csrf map[string]string
}

func (m *appMachine) signIn() *ownerSession {
	a := &ownerSession{m: m}
	a.login()
	return a
}

func (a *ownerSession) login() {
	a.b = newBrowser(a.m.r.t)
	waitFor(a.m.r.t, "a sign-in on "+a.m.name, 3*time.Minute, func() bool {
		code, _ := a.b.try("POST", a.m.webURL()+"/api/auth/login", map[string]string{"username": ownerUser, "password": ownerPass}, nil)
		return code == 200
	})
	a.csrf = map[string]string{"X-Maktaba-CSRF": a.b.cookie(a.m.webURL(), "mkt_csrf")}
}

// get is an admin GET that signs in again across the home's restarts.
func (a *ownerSession) get(path string) (int, []byte) {
	code, body := a.b.try("GET", a.m.webURL()+path, nil, nil)
	if code == http.StatusUnauthorized {
		a.login()
		code, body = a.b.try("GET", a.m.webURL()+path, nil, nil)
	}
	return code, body
}

// updateNow asks the home to check, then to update to version now; the run's id.
func (a *ownerSession) updateNow(version string) string {
	t := a.m.r.t
	t.Helper()
	web := a.m.webURL()
	resp, body := a.b.do("POST", web+"/api/cluster/updates/check", map[string]any{}, a.csrf)
	if resp.StatusCode != 200 {
		t.Fatalf("check: %d %s", resp.StatusCode, body)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		resp, body = a.b.do("POST", web+"/api/cluster/updates/run", map[string]any{"confirm": true, "version": version}, a.csrf)
		if resp.StatusCode == http.StatusAccepted {
			break
		}
		if resp.StatusCode != http.StatusConflict || time.Now().After(deadline) ||
			!strings.Contains(string(body), "role_not_running") && !strings.Contains(string(body), "recent_crash") {
			_, procs := a.b.do("GET", web+"/api/system/processes", nil, nil)
			t.Fatalf("update now %s: %d %s\nprocesses: %s", version, resp.StatusCode, body, tail(string(procs), 4000))
		}
		time.Sleep(3 * time.Second)
	}
	var out struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal(body, &out)
	a.m.r.logf("the update to %s started (run %s)", version, out.RunID)
	return out.RunID
}

// runView is a run as the Updates page reads it.
type runView struct {
	updateRun
	Detail json.RawMessage `json:"detail"`
}

// follow reads the run until it ends.
func (a *ownerSession) follow(id string, d time.Duration) runView {
	t := a.m.r.t
	t.Helper()
	var run runView
	waitFor(t, "the update run "+id+" to end", d, func() bool {
		code, body := a.get("/api/cluster/updates/runs/" + id)
		if code != 200 {
			return false
		}
		run = runView{}
		_ = json.Unmarshal(body, &run)
		return run.FinishedAt != nil
	})
	a.m.r.logf("the run %s ended %s (error %s)", id, run.State, string(run.Error))
	return run
}

// ---- downtime ----

// outageMonitor polls the home's web (an API answer that reads the database) and
// sums the time it did not answer.
type outageMonitor struct {
	mu      sync.Mutex
	down    time.Duration
	longest time.Duration
	since   time.Time // when the current outage began (zero: up)
	spans   []string
	stop    chan struct{}
	done    chan struct{}
}

func (m *appMachine) watchDowntime() *outageMonitor {
	o := &outageMonitor{stop: make(chan struct{}), done: make(chan struct{})}
	start := time.Now()
	go func() {
		defer close(o.done)
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		stopped := false
		for {
			up := m.selfVersion() != ""
			now := time.Now()
			o.mu.Lock()
			switch {
			case !up && o.since.IsZero():
				o.since = now
			case up && !o.since.IsZero():
				d := now.Sub(o.since)
				o.down += d
				if d > o.longest {
					o.longest = d
				}
				o.spans = append(o.spans, fmt.Sprintf("%.1fs at +%.0fs", d.Seconds(), o.since.Sub(start).Seconds()))
				o.since = time.Time{}
			}
			o.mu.Unlock()
			if stopped {
				return // the last look, taken after the stop
			}
			select {
			case <-o.stop:
				stopped = true
			case <-tick.C:
			}
		}
	}()
	return o
}

// end stops the monitor: the total downtime (an outage still going counts to now).
func (o *outageMonitor) end() (time.Duration, []string) {
	close(o.stop)
	<-o.done
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.since.IsZero() {
		d := time.Since(o.since)
		o.down += d
		o.spans = append(o.spans, fmt.Sprintf("%.1fs (still down)", d.Seconds()))
	}
	return o.down, o.spans
}

// budget reports the downtime and fails the test above the budget.
func (r *rehearsal) budget(o *outageMonitor, what string, budget time.Duration) time.Duration {
	r.t.Helper()
	down, spans := o.end()
	r.logf("DOWNTIME %s: %s (budget %s; outages: %s)", what, down.Round(100*time.Millisecond), budget, strings.Join(spans, ", "))
	r.summary(fmt.Sprintf("| %s | %s | %s | %s |", r.t.Name(), what, down.Round(time.Second), budget))
	if down > budget {
		r.t.Errorf("%s: %s of downtime, above the budget of %s (%s)", what, down.Round(time.Second), budget, strings.Join(spans, ", "))
	}
	return down
}

// summary appends a line to the CI job's summary (GITHUB_STEP_SUMMARY), when there is one.
func (r *rehearsal) summary(line string) {
	p := os.Getenv("GITHUB_STEP_SUMMARY")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// ---- watching the record and the journal ----

// stateLog records every state the database's record and the update's journal took.
type stateLog struct {
	mu      sync.Mutex
	records []string // record states, in order (deduplicated runs)
	journal []string // journal states
	stop    chan struct{}
	done    chan struct{}
	at      map[string]time.Time // first time each journal state was seen
}

func (m *appMachine) watchStates() *stateLog {
	s := &stateLog{stop: make(chan struct{}), done: make(chan struct{}), at: map[string]time.Time{}}
	go func() {
		defer close(s.done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			rec, _ := m.record()
			j := m.journal()
			s.mu.Lock()
			if rec.State != "" && (len(s.records) == 0 || s.records[len(s.records)-1] != rec.State) {
				s.records = append(s.records, rec.State)
			}
			if j.State != "" && (len(s.journal) == 0 || s.journal[len(s.journal)-1] != j.State) {
				s.journal = append(s.journal, j.State)
				if _, ok := s.at[j.State]; !ok {
					s.at[j.State] = time.Now()
				}
			}
			s.mu.Unlock()
			select {
			case <-s.stop:
				return
			case <-tick.C:
			}
		}
	}()
	return s
}

func (s *stateLog) end() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.done
}

func (s *stateLog) recordStates() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.records...)
}

func (s *stateLog) journalStates() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.journal...)
}

// ---- the site serves ----

// assertServing checks the site the way a person would reach it: the API's own port and
// readiness, the web page, the API through the web, and a signed-in page of the library.
func (m *appMachine) assertServing(version string) {
	t := m.r.t
	t.Helper()
	c := http.Client{Timeout: 10 * time.Second}
	check := func(what, u string) {
		resp, err := c.Get(u)
		if err != nil {
			t.Errorf("%s does not serve %s: %v", m.name, what, err)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s: %s answered %d: %s", m.name, what, resp.StatusCode, tail(string(body), 300))
		}
	}
	check("the API (/healthz)", m.apiURL()+"/healthz")
	check("the API's readiness (/readyz)", fmt.Sprintf("http://127.0.0.1:%d/readyz", m.admin))
	check("the web (/)", m.webURL()+"/")
	check("the API through the web (/api/cluster/self)", m.webURL()+"/api/cluster/self")
	if v := m.selfVersion(); v != version {
		t.Errorf("%s answers as %q, want %q", m.name, v, version)
	}
	a := m.signIn()
	if code, body := a.get("/api/libraries"); code != 200 {
		t.Errorf("%s: the libraries answered %d: %s", m.name, code, tail(string(body), 300))
	}
	m.r.logf("%s serves %s: the API, its readiness, the web, the API through the web, the library", m.name, version)
}

// ---- small helpers ----

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// skipUnlessRequired skips a scenario this machine cannot run safely; on CI
// (MAKTABA_REHEARSAL_REQUIRE_ALL=1) that is a failure: every scenario must run there.
func (r *rehearsal) skipUnlessRequired(why string) {
	r.t.Helper()
	if os.Getenv("MAKTABA_REHEARSAL_REQUIRE_ALL") == "1" {
		r.t.Fatalf("this scenario cannot run here: %s", why)
	}
	r.t.Skip(why)
}

// tvSlotPortsFree reports whether the TV slots' fixed loopback ports (8791-8793, admin
// 9111-9113) are free: never on a machine where another Maktaba airs its TV.
func tvSlotPortsFree() bool {
	for _, p := range []int{8791, 8792, 8793, 9111, 9112, 9113} {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return false
		}
	}
	return true
}

// queryInt runs a query of one number.
func queryInt(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
