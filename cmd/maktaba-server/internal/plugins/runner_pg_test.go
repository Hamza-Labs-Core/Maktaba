package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgtest"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/store"
	"github.com/Hamza-Labs-Core/Maktaba/shared/components/testpg"
)

// scratchDB is a migrated database of its own on MAKTABA_TEST_PG_URL.
func scratchDB(t *testing.T) *sql.DB {
	t.Helper()
	base := testpg.Get()
	if base == "" {
		t.Skip("MAKTABA_TEST_PG_URL unset: a scratch Postgres whose user may create databases")
	}
	if testing.Short() {
		t.Skip("runs Postgres and builds maktaba-api")
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	name := "plugins_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a, err := sql.Open("postgres", base); err == nil {
			_, _ = a.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
			_, _ = a.Exec(`DROP DATABASE IF EXISTS ` + name)
			_ = a.Close()
		}
	})
	u, _ := url.Parse(base)
	u.Path = "/" + name
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := pgtest.Migrator(t, t.TempDir())(ctx, u.String()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	db, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type sink struct {
	mu  sync.Mutex
	cat *components.Catalog
}

func (s *sink) SetPluginCatalog(c *components.Catalog) { s.mu.Lock(); s.cat = c; s.mu.Unlock() }

func (s *sink) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cat == nil {
		return false
	}
	_, ok := s.cat.Get(id)
	return ok
}

func record(t *testing.T, db *sql.DB, in *Installed, prev *Installed) {
	t.Helper()
	var pv, pd, pp any
	if prev != nil {
		pv, pd, pp = prev.Version, prev.Digest, prev.Path
	}
	if _, err := db.Exec(`INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest, previous_version,
		previous_digest, previous_path) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (plugin_id) DO UPDATE SET
		version = excluded.version, digest = excluded.digest, path = excluded.path, manifest = excluded.manifest,
		previous_version = excluded.previous_version, previous_digest = excluded.previous_digest,
		previous_path = excluded.previous_path`,
		in.PluginID, in.Version, in.Digest, in.Path, string(in.Manifest), pv, pd, pp); err != nil {
		t.Fatal(err)
	}
}

func statusOf(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var phase string
	err := db.QueryRow(`SELECT phase FROM component_status WHERE machine = 'home' AND id = $1 AND part = $2`, id, Part).Scan(&phase)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return phase
}

// 54.38 T04 and 54.07 T01/T02 (the supervisor's half): an install's entry reaches the
// reconciler, its gate follows the policy and grants, an update switches the pointer and
// keeps one previous version, a crash between rename and pointer is mended, and want
// absent removes the files, the install row and the queued jobs, keeping the data.
func TestRunnerFollowsTheInstalls(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()
	f, _ := newFiles(t)
	cat := &sink{}
	restarts := 0
	st := store.New(db)
	r := &Runner{Files: f, DB: db, Machine: store.Home, Version: "0.3.83", Catalog: cat, Status: st,
		RestartPipeline: func() { restarts++ }, Log: os.Stderr}
	cid := components.PluginPrefix + testID

	r.Pass(ctx) // nothing installed: the first pass records the set
	v1, err := f.Install(ctx, bytes.NewReader(pkg(t, testID, "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	record(t, db, v1, nil)
	if _, err := st.SetDesired(ctx, store.Desired{ID: cid, Want: components.WantInstalled},
		store.Audit{By: "admin", ActorSource: "web", Action: "component.install"}); err != nil {
		t.Fatal(err)
	}
	r.Desired(map[string]store.Desired{cid: {ID: cid, Want: components.WantInstalled, Generation: 1}})
	r.Pass(ctx)
	if !cat.has(cid) {
		t.Fatal("the reconciler's catalog has no plugin entry")
	}
	if restarts != 1 {
		t.Fatalf("restarts %d after the install, want 1", restarts)
	}
	if got := statusOf(t, db, cid); got != components.PhaseOff {
		t.Fatalf("files part %q, want off", got)
	}
	if g := r.Gate(cid); g != components.ReasonUnreviewedDisallow {
		t.Fatalf("gate %q, want unreviewed_disallowed with the policy off", g)
	}
	if _, err := db.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, '{"allow_unreviewed": true}')`, PolicyKey); err != nil {
		t.Fatal(err)
	}
	r.Pass(ctx)
	if g := r.Gate(cid); g != "" {
		t.Fatalf("gate %q, want open", g)
	}

	// an update: 1.1.0 beside 1.0.0, then plugin_installs names it (previous 1.0.0)
	v2, err := f.Install(ctx, bytes.NewReader(pkg(t, testID, "1.1.0")))
	if err != nil {
		t.Fatal(err)
	}
	record(t, db, v2, v1)
	r.Pass(ctx)
	if f.Layout.Current(testID) != v2.Folder || restarts != 2 {
		t.Fatalf("current %s restarts %d", f.Layout.Current(testID), restarts)
	}
	v3, err := f.Install(ctx, bytes.NewReader(pkg(t, testID, "1.2.0")))
	if err != nil {
		t.Fatal(err)
	}
	record(t, db, v3, v2)
	r.Pass(ctx)
	folders := f.Layout.Folders(testID)
	if len(folders) != 2 || f.Layout.Current(testID) != v3.Folder {
		t.Fatalf("folders %v current %s: want 1.2.0 and the kept 1.1.0", folders, f.Layout.Current(testID))
	}

	// a crash between the rename and the pointer: the pointer follows plugin_installs
	_ = f.Layout.SetCurrent(testID, v2.Folder)
	r.Pass(ctx)
	if f.Layout.Current(testID) != v3.Folder {
		t.Fatalf("current %s after a crash, want %s", f.Layout.Current(testID), v3.Folder)
	}

	// want absent: files, install row, grants and queued jobs go; data stays
	stage := components.PluginQueueName(testID, "hello")
	for _, q := range []string{
		`INSERT INTO job_stages (stage, label, subject_kinds, pool, lane, extension) VALUES ('` + stage + `', 'hello',
			'{catalog}', 'plugins', 'sources', 'plugin:` + testID + `') ON CONFLICT DO NOTHING`,
		`INSERT INTO plugin_grants (plugin_id, capabilities) VALUES ('` + testID + `', '{tags.write}')`,
		`INSERT INTO plugin_data (plugin_id, subject_kind, subject_id, key, value, bytes) VALUES ('` + testID + `', 'video', 'v', 'k', '1', 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO processing_jobs (stage, subject_kind, subject_id, state) VALUES ($1, 'catalog', '00000000-0000-0000-0000-00000000ca7a', 'pending')`, stage); err != nil {
		t.Fatal(err)
	}
	r.Desired(map[string]store.Desired{cid: {ID: cid, Want: components.WantAbsent, Generation: 3}})
	r.Pass(ctx)
	if _, err := os.Stat(f.Layout.Dir(testID)); !os.IsNotExist(err) {
		t.Fatalf("the plugin's folder stayed: %v", err)
	}
	var n int
	_ = db.QueryRow(`SELECT (SELECT count(*) FROM plugin_installs) + (SELECT count(*) FROM plugin_grants) +
		(SELECT count(*) FROM processing_jobs WHERE stage = $1)`, stage).Scan(&n)
	if n != 0 {
		t.Fatalf("%d install, grant or job rows stayed", n)
	}
	_ = db.QueryRow(`SELECT count(*) FROM plugin_data`).Scan(&n)
	if n != 1 {
		t.Fatal("an uninstall without purge dropped the plugin's data")
	}
	if restarts != 4 {
		t.Fatalf("restarts %d, want one for each change of the installed set (4)", restarts)
	}
	if _, err := os.Stat(filepath.Join(f.Layout.Root)); err != nil {
		t.Fatal(err)
	}
}

// A plugin installed after the pipeline worker started and before this runner's first
// read (the tables were not there yet) restarts the worker: it is not in its graph.
func TestFirstPassRestartsForAnInstallAfterTheWorkerStarted(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()
	f, _ := newFiles(t)
	restarts := 0
	r := &Runner{Files: f, DB: db, Machine: store.Home, Version: "0.3.83", Catalog: &sink{},
		RestartPipeline: func() { restarts++ }, PipelineStarted: func() time.Time { return time.Now().Add(-time.Hour) }}
	in, err := f.Install(ctx, bytes.NewReader(pkg(t, testID, "1.0.0")))
	if err != nil {
		t.Fatal(err)
	}
	record(t, db, in, nil)
	r.Pass(ctx)
	if restarts != 1 {
		t.Fatalf("restarts %d, want 1", restarts)
	}
	r.Pass(ctx)
	if restarts != 1 {
		t.Fatalf("restarts %d after a second pass, want 1", restarts)
	}
}
