//go:build integration && !windows

package devmigrate

import (
	"context"
	"io"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// pgHome finds a Postgres install: $MAKTABA_TEST_PG_HOME (e.g. a Mac app
// payload's postgres/), PATH, or Debian/Ubuntu's /usr/lib/postgresql/<v>.
func pgHome(t *testing.T) string {
	t.Helper()
	if h := os.Getenv("MAKTABA_TEST_PG_HOME"); h != "" {
		return h
	}
	if p, err := exec.LookPath("pg_restore"); err == nil {
		if _, err := exec.LookPath("initdb"); err == nil {
			return filepath.Dir(filepath.Dir(p))
		}
	}
	dirs, _ := filepath.Glob("/usr/lib/postgresql/*/bin/pg_restore")
	if len(dirs) > 0 {
		return filepath.Dir(filepath.Dir(dirs[len(dirs)-1]))
	}
	t.Skip("no Postgres (initdb, pg_restore) on this machine")
	return ""
}

// idlePort is a port nothing listens on, for the stack's ports the move checks are
// free: below the ephemeral range (Linux 32768-, macOS 49152-), where no other test's
// listener on :0 can take it between this choice and that check (freePort's ports are
// handed out again).
func idlePort(t *testing.T) int {
	t.Helper()
	r := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(os.Getpid())))
	for range 200 {
		p := 20000 + r.IntN(10000)
		if len(PortsInUse([]string{strconv.Itoa(p)})) > 0 {
			continue
		}
		if ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			_ = ln.Close()
			return p
		}
	}
	t.Fatal("no idle port in 20000-29999")
	return 0
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// startCluster prepares a bundle cluster and runs its server for the test.
func startCluster(t *testing.T, home, root string) pgbundle.Bundle {
	t.Helper()
	b := pgbundle.Bundle{Home: home, Data: filepath.Join(root, "pg"), Keys: filepath.Join(root, "keys"),
		Backups: filepath.Join(root, "backups"), Port: freePort(t)}
	ctx := context.Background()
	if err := b.Prepare(ctx, io.Discard); err != nil {
		t.Fatal(err)
	}
	srv := exec.Command(b.Bin("postgres"), b.ServerArgs()...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Signal(os.Interrupt); _ = srv.Wait() })
	deadline := time.Now().Add(30 * time.Second)
	for pgbundle.Ready(ctx, b.Addr(), pgbundle.SuperUser, "postgres") != nil {
		if time.Now().After(deadline) {
			t.Fatal("source postgres did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return b
}

func password(t *testing.T, b pgbundle.Bundle, role string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(b.Keys, "postgres-"+role+".password"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

// TestRealMove runs the whole move against real Postgres servers: a "Docker"
// source (a cluster of its own; `docker exec` runs its tools on the host) with
// what the migrations make (trigram indexes, functions, triggers, grants to the
// worker group), the stack stopped by its stack.sh, the dump at one snapshot,
// the restore into a second (the app's) cluster, the counts, the files and
// server.toml.
func TestRealMove(t *testing.T) {
	home := pgHome(t)
	if os.Geteuid() == 0 {
		t.Skip("postgres refuses to run as root")
	}
	root, err := os.MkdirTemp("", "mkmv") // short: the unix socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx := context.Background()

	src := startCluster(t, home, filepath.Join(root, "src"))
	superPW, appPW := password(t, src, pgbundle.SuperUser), password(t, src, pgbundle.AppRole)
	psql := func(user, pw, sql string) string {
		t.Helper()
		cmd := exec.Command(src.Bin("psql"), "-X", "-q", "-At", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1",
			"-p", strconv.Itoa(src.Port), "-U", user, "-d", "maktaba")
		cmd.Env = append(os.Environ(), "PGPASSWORD="+pw)
		cmd.Stdin = strings.NewReader(sql)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("psql: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	psql(pgbundle.SuperUser, superPW, `CREATE ROLE maktaba_worker NOLOGIN;
CREATE ROLE maktaba_node_1e943eda006442e98692851ebab36f54 LOGIN PASSWORD 'x' IN ROLE maktaba_worker;`)
	psql(pgbundle.AppRole, appPW, `
CREATE TABLE goose_db_version (id serial PRIMARY KEY, version_id bigint NOT NULL, is_applied boolean NOT NULL);
INSERT INTO goose_db_version (version_id, is_applied) SELECT g, true FROM generate_series(0, 141) g;
CREATE TABLE instance_identity (id int PRIMARY KEY, instance_id uuid NOT NULL DEFAULT gen_random_uuid());
INSERT INTO instance_identity (id) VALUES (1);
CREATE TABLE libraries (id serial PRIMARY KEY, name text NOT NULL);
CREATE TABLE videos (id bigserial PRIMARY KEY, library_id int REFERENCES libraries(id), title text NOT NULL, updated_at timestamptz);
CREATE INDEX videos_title_trgm ON videos USING gin (title gin_trgm_ops);
CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.updated_at = now(); RETURN NEW; END $$;
CREATE TRIGGER videos_touch BEFORE UPDATE ON videos FOR EACH ROW EXECUTE FUNCTION touch();
CREATE TABLE transcripts (id bigserial PRIMARY KEY, video_id bigint REFERENCES videos(id));
CREATE TABLE transcript_segments (id bigserial PRIMARY KEY, transcript_id bigint REFERENCES transcripts(id), text text);
CREATE TABLE users (id serial PRIMARY KEY, name text);
CREATE TABLE streaming_hosts (host_id text PRIMARY KEY, host text, role text, heartbeat_at timestamptz DEFAULT now());
CREATE VIEW video_titles AS SELECT unaccent(title) AS t FROM videos;
GRANT SELECT, INSERT, UPDATE ON videos TO maktaba_worker;
INSERT INTO libraries (name) SELECT 'lib ' || g FROM generate_series(1, 3) g;
INSERT INTO videos (library_id, title) SELECT 1 + g % 3, 'درس ' || g FROM generate_series(1, 500) g;
INSERT INTO transcripts (video_id) SELECT id FROM videos WHERE id <= 200;
INSERT INTO transcript_segments (transcript_id, text) SELECT 1 + g % 200, 'segment ' || g FROM generate_series(1, 5000) g;
INSERT INTO users (name) VALUES ('admin'), ('me');
INSERT INTO streaming_hosts (host_id, host, role) SELECT instance_id::text, 'Mac', 'home' FROM instance_identity;
INSERT INTO streaming_hosts (host_id, host, role) VALUES ('1e943eda-0064-42e9-8692-851ebab36f54', 'PC', 'node');
`)

	// the stack's checkout (its stack.sh records the stop) and its files
	devHome := filepath.Join(root, "home")
	repo := filepath.Join(devHome, "src")
	stopped := filepath.Join(root, "stopped")
	writeFile(t, filepath.Join(repo, "tools", "dev", "stack.sh"), "touch "+stopped+"\n")
	writeFile(t, filepath.Join(devHome, ".config", "maktaba", "secrets.key"), "0123456789abcdef0123456789abcdef")
	writeFile(t, filepath.Join(devHome, ".maktaba", "stream-signing.key"), "stream")
	writeFile(t, filepath.Join(devHome, ".maktaba", "chroma", "chroma.sqlite3"), "index")

	// docker exec runs the source's tools on the host; docker stop is recorded
	var docker []string
	sys := Sys{
		Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			switch name {
			case "zsh":
				return exec.CommandContext(ctx, "sh", args...)
			case "docker":
			default:
				return exec.CommandContext(ctx, "false") // pgrep, ps: nothing runs
			}
			docker = append(docker, strings.Join(args, " "))
			switch args[0] {
			case "inspect":
				return exec.CommandContext(ctx, "printf", "%s", `[{"State":{"Running":true},"Config":{"Image":"postgres:16-alpine"},"Mounts":[{"Type":"volume","Name":"maktaba_pg_data","Destination":"/var/lib/postgresql/data"}]}]`)
			case "exec":
				rest := args[1:]
				if rest[0] == "-i" {
					rest = rest[1:]
				}
				tool := rest[1] // rest[0] is the container
				// the compose image's maktaba user is its superuser: stand in with this cluster's
				targs := append([]string{"-h", "127.0.0.1", "-p", strconv.Itoa(src.Port)}, rest[2:]...)
				for i := range targs {
					if targs[i] == "-U" && i+1 < len(targs) {
						targs[i+1] = pgbundle.SuperUser
					}
				}
				cmd := exec.CommandContext(ctx, src.Bin(tool), targs...)
				cmd.Env = append(os.Environ(), "PGPASSWORD="+superPW)
				return cmd
			}
			return exec.CommandContext(ctx, "true")
		},
		Getenv: func(string) string { return "" },
		Home:   devHome,
	}

	appRoot := filepath.Join(root, "app")
	cfgPath := filepath.Join(appRoot, "server.toml")
	cfg, _, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	target := pgbundle.Bundle{Home: home, Data: filepath.Join(appRoot, "postgres"), Keys: filepath.Join(appRoot, "keys"),
		Backups: filepath.Join(appRoot, "backups"), Port: freePort(t)}
	var out strings.Builder
	m := &Migration{Sys: sys, Container: DefaultContainer, Repo: repo, Out: &out, Ports: []string{strconv.Itoa(idlePort(t))}, Target: Target{
		Config: cfg, ConfigPath: cfgPath, State: appRoot, Keys: target.Keys, Bundle: &target}}

	p := m.MakePlan(ctx)
	if len(p.Problems) > 0 {
		t.Fatalf("problems: %q", p.Problems)
	}
	if p.Facts.Counts["videos"] != 500 || len(p.Facts.MachineRoles()) != 1 || len(p.Warnings) == 0 {
		t.Fatalf("plan facts %+v, warnings %q", p.Facts, p.Warnings)
	}
	// the stack "runs": the plan stops it (FindStack finds nothing, so mark it)
	p.Stack.Procs = []Proc{{Kind: "api"}}
	if err := m.Run(ctx, p); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Fatal("stack.sh stop did not run")
	}
	if !contains(docker, "stop "+DefaultContainer) {
		t.Fatalf("docker stop did not run: %q", docker)
	}
	for _, l := range docker {
		if strings.Contains(l, "pg_dump") && !strings.Contains(l, "--snapshot=") {
			t.Fatalf("dump without the snapshot: %s", l)
		}
	}

	// the app's database: the same rows, owned by the app role, the grants kept
	if err := target.Prepare(ctx, io.Discard); err != nil { // cluster exists: a no-op
		t.Fatal(err)
	}
	srv := exec.Command(target.Bin("postgres"), target.ServerArgs()...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Process.Signal(os.Interrupt); _ = srv.Wait() }()
	for pgbundle.Ready(ctx, target.Addr(), pgbundle.SuperUser, "postgres") != nil {
		time.Sleep(100 * time.Millisecond)
	}
	q, err := target.NewQuerier()
	if err != nil {
		t.Fatal(err)
	}
	one := func(sql string) string {
		t.Helper()
		rows, err := q.Query(ctx, "maktaba", sql)
		if err != nil || len(rows) == 0 {
			t.Fatalf("%s: %v %q", sql, err, rows)
		}
		return rows[0]
	}
	if got := one("SELECT count(*) FROM transcript_segments"); got != "5000" {
		t.Fatalf("segments = %s", got)
	}
	if got := one("SELECT string_agg(DISTINCT tableowner, ',') FROM pg_tables WHERE schemaname = 'public'"); got != "maktaba" {
		t.Fatalf("table owners = %s", got)
	}
	if got := one("SELECT has_table_privilege('maktaba_worker', 'videos', 'UPDATE')"); got != "t" {
		t.Fatalf("worker grant = %s", got)
	}
	if got := one("SELECT count(*) FROM video_titles"); got != "500" {
		t.Fatalf("view = %s", got)
	}
	// the cluster's grants on Postgres's own functions, in the fresh database too: the
	// slots' functions are the app role's alone
	if got := one(`SELECT has_function_privilege('maktaba_worker', 'pg_catalog.pg_drop_replication_slot(name)', 'EXECUTE')::text
		|| has_function_privilege('maktaba', 'pg_catalog.pg_drop_replication_slot(name)', 'EXECUTE')::text
		|| has_function_privilege('maktaba', 'pg_catalog.pg_reload_conf()', 'EXECUTE')::text`); got != "falsetruetrue" {
		t.Fatalf("function grants (worker slot, app slot, app reload) = %s", got)
	}
	// the app role can write (the API runs as it): the trigger fires
	appQ := exec.Command(target.Bin("psql"), "-X", "-At", "-h", "127.0.0.1", "-p", strconv.Itoa(target.Port), "-U", "maktaba", "-d", "maktaba",
		"-c", "UPDATE videos SET title = title WHERE id = 1 RETURNING updated_at IS NOT NULL")
	appQ.Env = append(os.Environ(), "PGPASSWORD="+password(t, target, pgbundle.AppRole))
	if b, err := appQ.CombinedOutput(); err != nil || !strings.HasPrefix(strings.TrimSpace(string(b)), "t") {
		t.Fatalf("the app role writes: %v %s", err, b)
	}

	// the joined machine's login is off in the source (Undo turns it on again)
	if got := psql(pgbundle.SuperUser, superPW, "SELECT rolcanlogin FROM pg_roles WHERE rolname = 'maktaba_node_1e943eda006442e98692851ebab36f54'"); got != "f" {
		t.Fatalf("machine role can still log in: %s", got)
	}

	// --replace: a move made again keeps the database it replaces in backups/
	m.Replace = true
	if err := srv.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	_ = srv.Wait()
	p2 := m.MakePlan(ctx)
	if len(p2.Problems) > 0 {
		t.Fatalf("replace problems: %q", p2.Problems)
	}
	if err := m.Run(ctx, p2); err != nil {
		t.Fatalf("replace: %v\n%s", err, out.String())
	}
	if kept, _ := filepath.Glob(filepath.Join(target.Backups, "maktaba-*.dump")); len(kept) != 1 {
		t.Fatalf("no safety dump of the replaced database: %v", kept)
	}
	srv = exec.Command(target.Bin("postgres"), target.ServerArgs()...)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	for pgbundle.Ready(ctx, target.Addr(), pgbundle.SuperUser, "postgres") != nil {
		time.Sleep(100 * time.Millisecond)
	}

	// the files, server.toml and the marker
	if b, _ := os.ReadFile(filepath.Join(target.Keys, "secrets.key")); string(b) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("secrets.key = %q", b)
	}
	if _, err := os.Stat(filepath.Join(appRoot, "chroma", "chroma.sqlite3")); err != nil {
		t.Fatal(err)
	}
	got, _, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.DatabaseURLSet || got.Media.PathMap != "/Volumes/Originals/="+devHome+"/nas-nfs/" || got.Transcription.AudioHours != 16 {
		t.Fatalf("server.toml: %+v %+v", got.Media, got.Transcription)
	}
	mk, err := m.readMarker()
	if err != nil || mk.Stage != "migrated" || mk.Counts["videos"] != 500 || mk.HostID == "" || mk.Volume != "maktaba_pg_data" {
		t.Fatalf("marker %+v %v", mk, err)
	}
	t.Log(out.String())
	if !strings.Contains(out.String(), "docker start "+DefaultContainer) ||
		!strings.Contains(out.String(), "ALTER ROLE maktaba_node_1e943eda006442e98692851ebab36f54 LOGIN;") {
		t.Fatalf("no undo steps:\n%s", out.String())
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
